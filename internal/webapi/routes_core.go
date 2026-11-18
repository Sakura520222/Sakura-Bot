// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package webapi

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Sakura520222/Sakura-Bot/internal/botcmd"
	"github.com/Sakura520222/Sakura-Bot/internal/config"
	"github.com/Sakura520222/Sakura-Bot/internal/db"
	"github.com/Sakura520222/Sakura-Bot/internal/logging"
	"github.com/Sakura520222/Sakura-Bot/internal/mtproto"
	"github.com/Sakura520222/Sakura-Bot/internal/version"
)

func (s *Server) registerCoreRoutes(mux *http.ServeMux) {
	// 健康检查
	mux.HandleFunc("GET /api/health", s.handleHealth)

	// 认证（/api/auth/*）
	mux.HandleFunc("POST /api/auth/login", s.handleAuthLogin)
	mux.HandleFunc("POST /api/auth/telegram-callback", s.handleAuthTelegramCallback)
	mux.HandleFunc("GET /api/auth/status", s.handleAuthStatus)
	mux.HandleFunc("POST /api/auth/refresh", s.handleAuthRefresh)

	// 仪表板（/api/dashboard）
	mux.HandleFunc("GET /api/dashboard", s.handleDashboard)

	// 频道管理（/api/channels）
	mux.HandleFunc("GET /api/channels", s.handleGetChannels)
	mux.HandleFunc("POST /api/channels", s.handleAddChannel)
	mux.HandleFunc("DELETE /api/channels", s.handleDeleteChannel)

	// 命令中心（/api/commands）
	mux.HandleFunc("GET /api/commands", s.handleGetCommands)
	mux.HandleFunc("POST /api/commands/{operation_id}/execute", s.handleExecuteCommand)

	// 手动总结触发（/api/summaries/{channel...}/summarize）
	mux.HandleFunc("POST /api/summaries/", s.handleSummarizeChannel)

	// UserBot 管理（/api/userbot/*）
	mux.HandleFunc("GET /api/userbot/status", s.handleUserBotStatus)
	mux.HandleFunc("POST /api/userbot/join", s.handleUserBotJoin)
	mux.HandleFunc("POST /api/userbot/leave", s.handleUserBotLeave)
	mux.HandleFunc("GET /api/userbot/channels", s.handleUserBotChannels)
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"service": "sakura-bot-webui",
	})
}

// ────────────────────────────── /api/auth ──────────────────────────────

func (s *Server) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token string `json:"token"`
	}
	if err := readJSONBody(r, &body, 64*1024); err != nil {
		writeHTTPError(w, http.StatusBadRequest, "无效的登录请求")
		return
	}
	inputTok := strings.TrimSpace(body.Token)
	if inputTok == "" {
		writeEnvelopeFail(w, "请输入管理 Token", nil)
		return
	}

	now := time.Now().UTC()
	botTok := s.botToken()

	if s.devMode() && inputTok == "dev" {
		jwtStr, err := CreateAccessToken("dev", botTok, now)
		if err != nil {
			writeHTTPError(w, http.StatusInternalServerError, "签发 Token 失败")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"success":      true,
			"access_token": jwtStr,
			"message":      "开发模式登录成功",
		})
		return
	}

	expectedAdmin := ComputeAdminToken(botTok)
	if expectedAdmin != "" && subtle.ConstantTimeCompare([]byte(inputTok), []byte(expectedAdmin)) == 1 {
		jwtStr, err := CreateAccessToken("admin", botTok, now)
		if err != nil {
			writeHTTPError(w, http.StatusInternalServerError, "签发 Token 失败")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"success":      true,
			"access_token": jwtStr,
			"message":      "登录成功",
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"success":      false,
		"access_token": "",
		"message":      "管理 Token 错误",
	})
}

func (s *Server) handleAuthTelegramCallback(w http.ResponseWriter, r *http.Request) {
	fields := make(map[string]string)
	for k, vals := range r.URL.Query() {
		if len(vals) > 0 {
			fields[k] = vals[0]
		}
	}
	if fields["hash"] == "" {
		var bodyMap map[string]any
		if err := readJSONBody(r, &bodyMap, 64*1024); err == nil {
			for k, v := range bodyMap {
				if v != nil {
					fields[k] = fmt.Sprintf("%v", v)
				}
			}
		}
	}

	now := time.Now().UTC()
	uid, hashHex, err := VerifyTelegramLoginWidget(fields, s.botToken(), now)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"success":      false,
			"access_token": "",
			"message":      "Telegram 签名校验失败或已过期",
		})
		return
	}

	if s.deps.Config == nil || !s.deps.Config.IsAdmin(uid) {
		writeJSON(w, http.StatusOK, map[string]any{
			"success":      false,
			"access_token": "",
			"message":      "当前账号无系统管理员权限",
		})
		return
	}

	// 消费 10 分钟内的唯一 hash，避免同一登录凭证被重复使用。
	if s.deps.ConsumeLegacyLoginReplay == nil {
		writeHTTPError(w, http.StatusServiceUnavailable, "Telegram 登录暂时不可用")
		return
	}
	ok, rerr := s.deps.ConsumeLegacyLoginReplay(r.Context(), hashHex, now)
	if rerr != nil {
		writeHTTPError(w, http.StatusInternalServerError, "Telegram 登录暂时不可用")
		return
	}
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{
			"success":      false,
			"access_token": "",
			"message":      "登录凭证已被使用，请重新发起授权",
		})
		return
	}

	jwtStr, err := CreateAccessToken(strconv.FormatInt(uid, 10), s.botToken(), now)
	if err != nil {
		writeHTTPError(w, http.StatusInternalServerError, "Telegram 登录暂时不可用")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success":      true,
		"access_token": jwtStr,
		"message":      "Telegram 管理员登录成功",
	})
}

func (s *Server) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	raw := ExtractBearerOrQueryToken(r)
	if raw != "" {
		if u, err := VerifyAccessToken(raw, s.botToken(), s.devMode(), time.Now().UTC()); err == nil {
			writeJSON(w, http.StatusOK, map[string]any{
				"authenticated": true,
				"user_id":       u.UserID,
				"dev_mode":      s.devMode(),
			})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"authenticated": false,
		"user_id":       "",
		"dev_mode":      s.devMode(),
	})
}

func (s *Server) handleAuthRefresh(w http.ResponseWriter, r *http.Request) {
	u, ok := UserFromContext(r.Context())
	if !ok || u.UserID == "" {
		writeHTTPError(w, http.StatusUnauthorized, "Token 无效或已过期")
		return
	}
	jwtStr, err := CreateAccessToken(u.UserID, s.botToken(), time.Now().UTC())
	if err != nil {
		writeHTTPError(w, http.StatusInternalServerError, "刷新 Token 失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success":      true,
		"access_token": jwtStr,
		"message":      "Token 已刷新",
	})
}

// ────────────────────────────── /api/dashboard ──────────────────────────────

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	botStatus := "running"
	if s.deps.Scheduler != nil && s.deps.Scheduler.IsPaused() {
		botStatus = "paused"
	}

	var channels []string
	var schedCount int
	var fwdEnabled bool
	var fwdCount int
	var aiModel string
	var qaUsername any

	if cfg := s.deps.Config; cfg != nil {
		channels = cfg.Channels()
		schedCount = len(cfg.SummarySchedules())
		fwdCfg := cfg.ForwardingConfig()
		fwdEnabled = fwdCfg.Enabled
		fwdCount = len(fwdCfg.Rules)
		aiModel = cfg.EffectiveAIConfig("summary").Model
		if u := strings.TrimSpace(cfg.QABotUsername()); u != "" {
			qaUsername = u
		} else if cfg.QABotToken() != "" {
			qaUsername = "configured"
		}
	}

	totalSummaries := 0
	if s.deps.DB != nil {
		if cnt, err := s.deps.DB.CountSummaries(r.Context(), "", nil, nil); err == nil {
			totalSummaries = int(cnt)
		}
	}

	writeEnvelopeOK(w, map[string]any{
		"bot_status":            botStatus,
		"version":               version.Version,
		"channel_count":         len(channels),
		"schedule_count":        schedCount,
		"forwarding_enabled":    fwdEnabled,
		"forwarding_rule_count": fwdCount,
		"total_summaries":       totalSummaries,
		"log_level":             logging.GetDefaultLevel(),
		"uptime_seconds":        time.Since(s.startedAt).Seconds(),
		"ai_model":              aiModel,
		"qa_bot_username":       qaUsername,
	}, "")
}

// ────────────────────────────── /api/channels ──────────────────────────────

func (s *Server) handleGetChannels(w http.ResponseWriter, _ *http.Request) {
	if s.deps.Config == nil {
		writeEnvelopeOK(w, map[string]any{"channels": []any{}, "total": 0}, "")
		return
	}
	chs := s.deps.Config.Channels()
	scheds := s.deps.Config.SummarySchedules()
	polls := s.deps.Config.PollSettings()

	items := make([]map[string]any, 0, len(chs))
	for _, ch := range chs {
		_, hasSched := scheds[ch]
		_, hasPoll := polls[ch]
		items = append(items, map[string]any{
			"url":               ch,
			"has_schedule":      hasSched,
			"has_poll_settings": hasPoll,
		})
	}
	writeEnvelopeOK(w, map[string]any{
		"channels": items,
		"total":    len(items),
	}, "")
}

func (s *Server) handleAddChannel(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ChannelURL string `json:"channel_url"`
	}
	if err := readJSONBody(r, &body, 64*1024); err != nil {
		writeHTTPError(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	ch := config.NormalizeChannelID(body.ChannelURL)
	if strings.TrimSpace(body.ChannelURL) == "" || ch == "https://t.me/" {
		writeEnvelopeFail(w, "无效的频道地址或用户名", nil)
		return
	}
	if s.deps.Config == nil {
		writeHTTPError(w, http.StatusServiceUnavailable, "配置服务未初始化")
		return
	}
	var duplicate bool
	err := s.deps.Config.UpdateConfig(func(doc map[string]any) error {
		cur := s.deps.Config.Channels()
		if slices.Contains(cur, ch) {
			duplicate = true
			return nil
		}
		cur = append(cur, ch)
		doc["channels"] = cur
		return nil
	})
	if err != nil {
		writeEnvelopeFail(w, fmt.Sprintf("添加频道失败: %v", err), nil)
		return
	}
	if duplicate {
		writeEnvelopeFail(w, fmt.Sprintf("频道 %s 已存在", ch), nil)
		return
	}
	s.recordAudit(r, "channel.add", ch, "", true, "已添加频道", 0)
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": fmt.Sprintf("频道 %s 添加成功", ch), "data": nil})
}

func (s *Server) handleDeleteChannel(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ChannelURL string `json:"channel_url"`
	}
	if err := readJSONBody(r, &body, 64*1024); err != nil {
		writeHTTPError(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	target := strings.TrimSpace(body.ChannelURL)
	normTarget := config.NormalizeChannelID(target)
	if target == "" || s.deps.Config == nil {
		writeEnvelopeFail(w, "请指定要删除的频道", nil)
		return
	}

	var found bool
	err := s.deps.Config.UpdateConfig(func(doc map[string]any) error {
		cur := s.deps.Config.Channels()
		filtered := make([]string, 0, len(cur))
		for _, c := range cur {
			if c == target || config.NormalizeChannelID(c) == normTarget {
				found = true
				continue
			}
			filtered = append(filtered, c)
		}
		if !found {
			return nil
		}
		doc["channels"] = filtered
		if sm, ok := doc["summary_schedules"].(map[string]any); ok {
			delete(sm, target)
			delete(sm, normTarget)
		}
		if pm, ok := doc["channel_poll_settings"].(map[string]any); ok {
			delete(pm, target)
			delete(pm, normTarget)
		}
		if qa, ok := doc["qa_assistant"].(map[string]any); ok {
			if reqChs, ok := qa["required_channels"].([]any); ok {
				kept := make([]any, 0, len(reqChs))
				for _, rc := range reqChs {
					sStr := fmt.Sprint(rc)
					if sStr != target && config.NormalizeChannelID(sStr) != normTarget {
						kept = append(kept, rc)
					}
				}
				qa["required_channels"] = kept
			}
			if comms, ok := qa["comments"].([]any); ok {
				kept := make([]any, 0, len(comms))
				for _, item := range comms {
					if m, ok := item.(map[string]any); ok {
						chStr := fmt.Sprint(m["channel"])
						if chStr == target || config.NormalizeChannelID(chStr) == normTarget {
							continue
						}
					}
					kept = append(kept, item)
				}
				qa["comments"] = kept
			}
		}
		return nil
	})
	if err != nil {
		writeEnvelopeFail(w, fmt.Sprintf("删除频道失败: %v", err), nil)
		return
	}
	if !found {
		writeEnvelopeFail(w, "未找到该频道", nil)
		return
	}
	s.recordAudit(r, "channel.delete", normTarget, "", true, "已删除频道及关联设置", 0)
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "频道已删除", "data": nil})
}

// ────────────────────────────── /api/commands ──────────────────────────────

func (s *Server) handleGetCommands(w http.ResponseWriter, _ *http.Request) {
	writeEnvelopeOK(w, botcmd.ListWebUICommands(), "")
}

func (s *Server) handleExecuteCommand(w http.ResponseWriter, r *http.Request) {
	opID := strings.TrimSpace(r.PathValue("operation_id"))
	if s.deps.CommandRouter == nil {
		writeHTTPError(w, http.StatusServiceUnavailable, "命令路由器未初始化")
		return
	}
	var body struct {
		Params      map[string]any `json:"params"`
		Confirm     bool           `json:"confirm"`
		ConfirmText string         `json:"confirm_text"`
	}
	if err := readJSONBody(r, &body, 256*1024); err != nil {
		writeHTTPError(w, http.StatusBadRequest, "请求参数解析失败")
		return
	}
	if body.Params == nil {
		body.Params = map[string]any{}
	}

	started := time.Now()
	res, err := s.deps.CommandRouter.ExecuteWebUICommand(r.Context(), opID, body.Params, body.Confirm, body.ConfirmText, wikiActor(r))
	durMS := int(time.Since(started).Milliseconds())
	paramSummaryBytes, _ := json.Marshal(body.Params)

	if err != nil {
		s.recordAudit(r, "command."+opID, opID, string(paramSummaryBytes), false, err.Error(), durMS)
		writeHTTPError(w, http.StatusBadRequest, err.Error())
		return
	}

	ok, _ := res["success"].(bool)
	msg, _ := res["message"].(string)
	s.recordAudit(r, "command."+opID, opID, string(paramSummaryBytes), ok, msg, durMS)
	writeJSON(w, http.StatusOK, res)
}

// ────────────────────────────── /api/summaries/{channel...}/summarize ──────────────────────────────

func (s *Server) handleSummarizeChannel(w http.ResponseWriter, r *http.Request) {
	const prefix = "/api/summaries/"
	const suffix = "/summarize"
	rawPath := r.URL.Path
	if !strings.HasPrefix(rawPath, prefix) || !strings.HasSuffix(rawPath, suffix) {
		writeHTTPError(w, http.StatusNotFound, "Not Found")
		return
	}
	channelRef := strings.Trim(rawPath[len(prefix):len(rawPath)-len(suffix)], "/")
	if channelRef == "" {
		writeHTTPError(w, http.StatusBadRequest, "频道参数不能为空")
		return
	}
	if s.deps.SummaryService == nil || s.deps.BotClient == nil || !s.deps.BotClient.IsConnected() {
		writeHTTPError(w, http.StatusServiceUnavailable, "Telegram 客户端尚未连接")
		return
	}

	res := s.deps.SummaryService.GenerateChannelSummary(r.Context(), channelRef, false, "manual")
	if !res.Success {
		errMsg := res.Error
		if errMsg == "" {
			errMsg = "当前时间窗口内无新消息或生成失败"
		}
		writeEnvelopeFail(w, errMsg, nil)
		return
	}

	preview := res.SummaryText
	if utf8.RuneCountInString(preview) > 300 {
		preview = string([]rune(preview)[:300]) + "..."
	}
	writeEnvelopeOK(w, map[string]any{
		"channel_name":    res.ChannelName,
		"message_count":   res.MessageCount,
		"summary_preview": preview,
		"summary_length":  utf8.RuneCountInString(res.SummaryText),
	}, "总结生成并推送成功")
}

// ────────────────────────────── /api/userbot/* ──────────────────────────────

func (s *Server) handleUserBotStatus(w http.ResponseWriter, _ *http.Request) {
	if s.deps.UserBot == nil {
		enabled := false
		fallback := true
		if s.deps.Config != nil {
			enabled = s.deps.Config.UserBotEnabled()
			fallback = s.deps.Config.UserBotFallbackToBot()
		}
		writeEnvelopeOK(w, map[string]any{
			"enabled":         enabled,
			"connected":       false,
			"phone_preview":   "",
			"fallback_to_bot": fallback,
		}, "")
		return
	}
	st := s.deps.UserBot.Status()
	writeEnvelopeOK(w, map[string]any{
		"enabled":         st.Enabled,
		"connected":       st.Connected,
		"phone_preview":   st.PhonePreview,
		"fallback_to_bot": st.FallbackToBot,
	}, "")
}

func (s *Server) handleUserBotJoin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ChannelURL string `json:"channel_url"`
	}
	if err := readJSONBody(r, &body, 64*1024); err != nil || strings.TrimSpace(body.ChannelURL) == "" {
		writeEnvelopeFail(w, "请提供有效的频道链接或用户名", nil)
		return
	}
	if s.deps.UserBot == nil {
		writeEnvelopeFail(w, "UserBot 未启用或未连接", nil)
		return
	}
	res := s.deps.UserBot.JoinChannel(r.Context(), strings.TrimSpace(body.ChannelURL))
	writeUserBotOperation(w, res)
}

func (s *Server) handleUserBotLeave(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ChannelURL string `json:"channel_url"`
	}
	if err := readJSONBody(r, &body, 64*1024); err != nil || strings.TrimSpace(body.ChannelURL) == "" {
		writeEnvelopeFail(w, "请提供有效的频道链接或用户名", nil)
		return
	}
	if s.deps.UserBot == nil {
		writeEnvelopeFail(w, "UserBot 未启用或未连接", nil)
		return
	}
	res := s.deps.UserBot.LeaveChannel(r.Context(), strings.TrimSpace(body.ChannelURL))
	writeUserBotOperation(w, res)
}

func (s *Server) handleUserBotChannels(w http.ResponseWriter, r *http.Request) {
	if s.deps.UserBot == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "UserBot 未启用或未连接",
		})
		return
	}
	list, err := s.deps.UserBot.ListJoinedChannels(r.Context())
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	items := make([]map[string]any, 0, len(list))
	for _, ch := range list {
		items = append(items, userBotChannelView(ch))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success":  true,
		"channels": items,
		"count":    len(items),
	})
}

func userBotChannelView(channel mtproto.ChannelInfo) map[string]any {
	var username any
	if channel.Username != "" {
		username = channel.Username
	}
	return map[string]any{"id": channel.ID, "title": channel.Title, "username": username}
}

func writeUserBotOperation(w http.ResponseWriter, result mtproto.JoinLeaveResult) {
	var info map[string]any
	if result.ChannelInfo != nil {
		info = userBotChannelView(*result.ChannelInfo)
	}
	writeJSON(w, http.StatusOK, struct {
		mtproto.JoinLeaveResult
		ChannelInfo map[string]any `json:"channel_info,omitempty"`
	}{result, info})
}

func (s *Server) recordAudit(r *http.Request, action, target, paramsSummary string, success bool, message string, durationMS int) {
	if s.deps.DB == nil {
		return
	}
	actor := "unknown"
	if u, ok := UserFromContext(r.Context()); ok && u.UserID != "" {
		actor = u.UserID
	}
	_ = s.deps.DB.RecordAuditLog(r.Context(), db.AuditEntry{
		Action:        action,
		Actor:         actor,
		Target:        target,
		ParamsSummary: paramsSummary,
		Success:       success,
		Message:       message,
		DurationMs:    durationMS,
	})
}
