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
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Sakura520222/Sakura-Bot/internal/config"
	"github.com/Sakura520222/Sakura-Bot/internal/db"
	"github.com/Sakura520222/Sakura-Bot/internal/llm"
	"github.com/Sakura520222/Sakura-Bot/internal/netpolicy"
	"github.com/Sakura520222/Sakura-Bot/internal/tg"
	"github.com/go-telegram/bot/models"
)

func (s *Server) registerAIRoutes(mux *http.ServeMux) {
	// v1 AI Profiles（/api/ai/profiles*）
	mux.HandleFunc("GET /api/ai/profiles", s.handleGetAIProfiles)
	mux.HandleFunc("PUT /api/ai/profiles", s.handlePutAIProfiles)
	mux.HandleFunc("POST /api/ai/profiles/{profile_id}/models", s.handlePostAIProfileModels)
	mux.HandleFunc("POST /api/ai/profiles/{profile_id}/test", s.handlePostAIProfileTest)

	// v2 AI Providers（/api/ai/providers*）
	mux.HandleFunc("GET /api/ai/providers", s.handleGetAIProviders)
	mux.HandleFunc("PUT /api/ai/providers", s.handlePutAIProviders)
	mux.HandleFunc("POST /api/ai/providers/{provider_id}/models", s.handlePostAIProviderModels)
	mux.HandleFunc("POST /api/ai/providers/{provider_id}/test", s.handlePostAIProviderTest)

	// QA Assistant（/api/ai/assistant*）
	mux.HandleFunc("GET /api/ai/assistant", s.handleGetAssistantConfig)
	mux.HandleFunc("POST /api/ai/assistant/diagnostics", s.handlePostAssistantDiagnostics)
	mux.HandleFunc("PUT /api/ai/assistant", s.handlePutAssistantConfig)
	mux.HandleFunc("POST /api/ai/assistant/backfill", s.handlePostAssistantBackfill)

	// Legacy AI Config / Quota / Prompts
	mux.HandleFunc("GET /api/ai/config", s.handleGetLegacyAIConfig)
	mux.HandleFunc("PUT /api/ai/config", s.handlePutLegacyAIConfig)
	mux.HandleFunc("GET /api/ai/qa-quota", s.handleGetQAQuota)
	mux.HandleFunc("PUT /api/ai/qa-quota", s.handlePutQAQuota)
	mux.HandleFunc("GET /api/ai/prompts", s.handleGetPrompts)
	mux.HandleFunc("GET /api/ai/prompts/{prompt_type}", s.handleGetPromptByType)
	mux.HandleFunc("PUT /api/ai/prompts/{prompt_type}", s.handlePutPromptByType)
	mux.HandleFunc("POST /api/ai/prompts/{prompt_type}/reset", s.handleResetPromptByType)
}

// ────────────────────────────── v1 Profiles & v2 Providers ──────────────────────────────

func (s *Server) handleGetAIProfiles(w http.ResponseWriter, _ *http.Request) {
	if s.deps.Config == nil {
		writeHTTPError(w, http.StatusServiceUnavailable, "配置服务未就绪")
		return
	}
	view, err := s.deps.Config.PublicAIProfiles()
	if err != nil {
		writeHTTPError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeEnvelopeOK(w, view, "")
}

func (s *Server) handlePutAIProfiles(w http.ResponseWriter, r *http.Request) {
	if s.deps.Config == nil {
		writeHTTPError(w, http.StatusServiceUnavailable, "配置服务未就绪")
		return
	}
	var body map[string]any
	if err := readJSONBody(r, &body, 1<<20); err != nil || body == nil {
		writeHTTPError(w, http.StatusBadRequest, "无效的 AI 配置 JSON")
		return
	}
	view, err := s.deps.Config.SaveAIProfiles(body)
	if err != nil {
		if errors.Is(err, config.ErrConfigConflict) {
			writeHTTPError(w, http.StatusConflict, err.Error())
			return
		}
		writeHTTPError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.recordAudit(r, "ai.profiles.update", "ai_profiles", "", true, "已保存 AI 档案配置", 0)
	writeEnvelopeOK(w, view, "AI 模型档案和路由已保存并立即生效")
}

func (s *Server) handlePostAIProfileModels(w http.ResponseWriter, r *http.Request) {
	if s.deps.Config == nil {
		writeHTTPError(w, http.StatusServiceUnavailable, "配置服务未就绪")
		return
	}
	profileID := r.PathValue("profile_id")
	var body map[string]any
	_ = readJSONBody(r, &body, 128*1024)
	conn, err := s.deps.Config.ResolveProfileConnection(profileID, body)
	if err != nil {
		if errors.Is(err, config.ErrProfileNotFound) {
			writeHTTPError(w, http.StatusNotFound, "档案不存在")
			return
		}
		writeHTTPError(w, http.StatusBadRequest, err.Error())
		return
	}
	upstream := s.aiProbeHTTP(w, conn)
	if upstream == nil {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	modelsList, lerr := llm.ListModels(ctx, conn.BaseURL, conn.APIKey, upstream)
	if lerr != nil {
		writeHTTPError(w, http.StatusBadGateway, lerr.Error())
		return
	}
	writeEnvelopeOK(w, modelsList, "")
}

func (s *Server) handlePostAIProfileTest(w http.ResponseWriter, r *http.Request) {
	if s.deps.Config == nil {
		writeHTTPError(w, http.StatusServiceUnavailable, "配置服务未就绪")
		return
	}
	profileID := r.PathValue("profile_id")
	var body map[string]any
	if err := readJSONBody(r, &body, 128*1024); err != nil {
		writeHTTPError(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	conn, err := s.deps.Config.ResolveProfileConnection(profileID, body)
	if err != nil {
		if errors.Is(err, config.ErrProfileNotFound) {
			writeHTTPError(w, http.StatusNotFound, "档案不存在")
			return
		}
		writeHTTPError(w, http.StatusBadRequest, err.Error())
		return
	}
	kind, _ := body["kind"].(string)
	if kind == "" {
		kind = "connection"
	}
	modelName, _ := body["model"].(string)
	if strings.TrimSpace(modelName) == "" {
		modelName = conn.Model
	}
	upstream := s.aiProbeHTTP(w, conn)
	if upstream == nil {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if err := llm.TestConnection(ctx, conn.BaseURL, conn.APIKey, strings.TrimSpace(modelName), kind, upstream); err != nil {
		if kind != "connection" && kind != "call" {
			writeHTTPError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeHTTPError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeEnvelopeOK(w, nil, "连接测试通过")
}

func (s *Server) handleGetAIProviders(w http.ResponseWriter, _ *http.Request) {
	if s.deps.Config == nil {
		writeHTTPError(w, http.StatusServiceUnavailable, "配置服务未就绪")
		return
	}
	view, err := s.deps.Config.PublicAIProviders()
	if err != nil {
		writeHTTPError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeEnvelopeOK(w, view, "")
}

func (s *Server) handlePutAIProviders(w http.ResponseWriter, r *http.Request) {
	if s.deps.Config == nil {
		writeHTTPError(w, http.StatusServiceUnavailable, "配置服务未就绪")
		return
	}
	var body map[string]any
	if err := readJSONBody(r, &body, 1<<20); err != nil || body == nil {
		writeHTTPError(w, http.StatusBadRequest, "无效的厂商配置 JSON")
		return
	}
	view, err := s.deps.Config.SaveAIProviders(body)
	if err != nil {
		if errors.Is(err, config.ErrConfigConflict) {
			writeHTTPError(w, http.StatusConflict, err.Error())
			return
		}
		writeHTTPError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.recordAudit(r, "ai.providers.update", "ai_providers", "", true, "已保存 AI 厂商与路由配置", 0)
	writeEnvelopeOK(w, view, "厂商、已添加模型和用途配置已保存并校验")
}

func (s *Server) handlePostAIProviderModels(w http.ResponseWriter, r *http.Request) {
	if s.deps.Config == nil {
		writeHTTPError(w, http.StatusServiceUnavailable, "配置服务未就绪")
		return
	}
	providerID := r.PathValue("provider_id")
	var body map[string]any
	_ = readJSONBody(r, &body, 128*1024)
	conn, err := s.deps.Config.ResolveProviderConnection(providerID, body)
	if err != nil {
		if errors.Is(err, config.ErrProfileNotFound) {
			writeHTTPError(w, http.StatusNotFound, "厂商不存在")
			return
		}
		writeHTTPError(w, http.StatusBadRequest, err.Error())
		return
	}
	upstream := s.aiProbeHTTP(w, conn)
	if upstream == nil {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	modelsList, lerr := llm.ListModels(ctx, conn.BaseURL, conn.APIKey, upstream)
	if lerr != nil {
		writeHTTPError(w, http.StatusBadGateway, lerr.Error())
		return
	}
	writeEnvelopeOK(w, modelsList, "")
}

func (s *Server) handlePostAIProviderTest(w http.ResponseWriter, r *http.Request) {
	if s.deps.Config == nil {
		writeHTTPError(w, http.StatusServiceUnavailable, "配置服务未就绪")
		return
	}
	providerID := r.PathValue("provider_id")
	var body map[string]any
	if err := readJSONBody(r, &body, 128*1024); err != nil {
		writeHTTPError(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	conn, err := s.deps.Config.ResolveProviderConnection(providerID, body)
	if err != nil {
		if errors.Is(err, config.ErrProfileNotFound) {
			writeHTTPError(w, http.StatusNotFound, "厂商不存在")
			return
		}
		writeHTTPError(w, http.StatusBadRequest, err.Error())
		return
	}
	kind, _ := body["kind"].(string)
	if kind == "" {
		kind = "connection"
	}
	modelName, _ := body["model"].(string)
	upstream := s.aiProbeHTTP(w, conn)
	if upstream == nil {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if err := llm.TestConnection(ctx, conn.BaseURL, conn.APIKey, strings.TrimSpace(modelName), kind, upstream); err != nil {
		if kind != "connection" && kind != "call" {
			writeHTTPError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeHTTPError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeEnvelopeOK(w, nil, "连接测试通过")
}

// aiProbeHTTP permits private destinations only for the configured provider origin.
func (s *Server) aiProbeHTTP(w http.ResponseWriter, conn config.AIConnection) *http.Client {
	if !conn.SavedOrigin {
		if _, err := netpolicy.PublicURL(conn.BaseURL); err != nil {
			writeHTTPError(w, http.StatusBadRequest, "草稿仅可测试公网服务。请先保存内网地址或非标准端口的厂商配置")
			return nil
		}
		return netpolicy.NewPublicHTTPClient(llm.DefaultTestConnectionTimeout, nil)
	}

	client := http.Client{Transport: directAIProbeTransport}
	if s.deps.UpstreamHTTP != nil {
		client = *s.deps.UpstreamHTTP
	}
	checkRedirect := client.CheckRedirect
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 || !netpolicy.SameOrigin(req.URL, via[0].URL) || req.URL.User != nil {
			return http.ErrUseLastResponse
		}
		if checkRedirect != nil {
			return checkRedirect(req, via)
		}
		return nil
	}
	return &client
}

var directAIProbeTransport = func() *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return transport
}()

// ────────────────────────────── QA Assistant (/api/ai/assistant*) ──────────────────────────────

type assistantConfigPayload struct {
	Assistant config.AssistantSettings `json:"assistant"`
	Pool      config.PoolConfig        `json:"pool"`
}

func (s *Server) handleGetAssistantConfig(w http.ResponseWriter, _ *http.Request) {
	if s.deps.Config == nil {
		writeHTTPError(w, http.StatusServiceUnavailable, "配置服务未就绪")
		return
	}
	asst, err := s.deps.Config.AssistantSettings()
	if err != nil {
		writeHTTPError(w, http.StatusBadRequest, err.Error())
		return
	}
	poolCfg, perr := s.deps.Config.AIPoolConfig()
	if perr != nil {
		writeHTTPError(w, http.StatusBadRequest, perr.Error())
		return
	}
	writeEnvelopeOK(w, map[string]any{
		"assistant": asst,
		"pool":      poolCfg,
		"channels":  s.deps.Config.Channels(),
	}, "")
}

func validateAssistantRequest(req assistantConfigPayload, configuredChannels []string) error {
	if req.Assistant.Timezone == "" {
		return errors.New("timezone 不能为空")
	}
	if _, err := time.LoadLocation(req.Assistant.Timezone); err != nil {
		return fmt.Errorf("无效的 IANA 时区: %s", req.Assistant.Timezone)
	}
	if req.Assistant.MaxMessages < 1 || req.Assistant.MaxMessages > 64 ||
		req.Assistant.InboxCapacity < 1 || req.Assistant.InboxCapacity > 64 ||
		req.Assistant.PerUserConcurrency < 1 || req.Assistant.PerUserConcurrency > 16 {
		return errors.New("assistant 容量参数超出范围")
	}
	if req.Pool.Workers < 1 || req.Pool.Workers > 32 ||
		req.Pool.QueueCapacity < 1 || req.Pool.QueueCapacity > 1024 ||
		req.Pool.TimeoutSeconds < 1 || req.Pool.TimeoutSeconds > 1800 {
		return errors.New("pool 并发池参数超出范围")
	}

	normSet := make(map[string]bool, len(configuredChannels))
	for _, ch := range configuredChannels {
		normSet[config.NormalizeChannelID(ch)] = true
		normSet[ch] = true
	}
	seenReq := make(map[string]bool)
	for _, rc := range req.Assistant.RequiredChannels {
		n := config.NormalizeChannelID(rc)
		if !normSet[rc] && !normSet[n] {
			return fmt.Errorf("订阅频道未在监听列表中: %s", rc)
		}
		if seenReq[n] {
			return fmt.Errorf("重复的订阅频道: %s", rc)
		}
		seenReq[n] = true
	}
	seenCom := make(map[string]bool)
	for _, cm := range req.Assistant.Comments {
		n := config.NormalizeChannelID(cm.Channel)
		if !normSet[cm.Channel] && !normSet[n] {
			return fmt.Errorf("评论规则频道未在监听列表中: %s", cm.Channel)
		}
		if seenCom[n] {
			return fmt.Errorf("重复的评论规则频道: %s", cm.Channel)
		}
		seenCom[n] = true
		w := strings.TrimSpace(cm.WakeWord)
		if len([]rune(w)) < 1 || len([]rune(w)) > 32 {
			return errors.New("wake_word 长度需在 1..32 之间")
		}
		if cm.AnonymousDailyLimit < 1 || cm.AnonymousDailyLimit > 1000 {
			return errors.New("anonymous_daily_limit 需在 1..1000 之间")
		}
	}
	return nil
}

func (s *Server) runAssistantDiagnostics(ctx context.Context, req assistantConfigPayload) []map[string]any {
	qaToken := ""
	if s.deps.Config != nil {
		qaToken = strings.TrimSpace(s.deps.Config.QABotToken())
	}
	api := s.deps.DirectBotAPI
	if api == nil {
		api = tg.NewDirectBotAPI()
	}

	var checkTargets []struct {
		Channel       string
		NeedDiscuss   bool
		RequireSubChk bool
	}
	targetMap := make(map[string]int)
	if req.Assistant.RequireSubscription {
		for _, ch := range req.Assistant.RequiredChannels {
			targetMap[ch] = len(checkTargets)
			checkTargets = append(checkTargets, struct {
				Channel       string
				NeedDiscuss   bool
				RequireSubChk bool
			}{Channel: ch, RequireSubChk: true})
		}
	}
	for _, cm := range req.Assistant.Comments {
		if !cm.Enabled {
			continue
		}
		if idx, exists := targetMap[cm.Channel]; exists {
			checkTargets[idx].NeedDiscuss = true
		} else {
			targetMap[cm.Channel] = len(checkTargets)
			checkTargets = append(checkTargets, struct {
				Channel       string
				NeedDiscuss   bool
				RequireSubChk bool
			}{Channel: cm.Channel, NeedDiscuss: true})
		}
	}
	if len(checkTargets) == 0 {
		return []map[string]any{}
	}

	if qaToken == "" {
		out := make([]map[string]any, 0, len(checkTargets))
		for _, t := range checkTargets {
			out = append(out, map[string]any{
				"channel": t.Channel,
				"ok":      false,
				"stage":   "identity",
				"message": "未配置 QA_BOT_TOKEN",
			})
		}
		return out
	}

	me, err := api.GetMe(ctx, qaToken)
	if err != nil || me == nil {
		out := make([]map[string]any, 0, len(checkTargets))
		for _, t := range checkTargets {
			out = append(out, map[string]any{
				"channel": t.Channel,
				"ok":      false,
				"stage":   "identity",
				"message": fmt.Sprintf("无法获取助手身份: %v", err),
			})
		}
		return out
	}

	out := make([]map[string]any, 0, len(checkTargets))
	for _, t := range checkTargets {
		chatRef := formatBotAPIChatTarget(t.Channel)
		chatInfo, cerr := api.GetChat(ctx, qaToken, chatRef)
		if cerr != nil || chatInfo == nil {
			out = append(out, map[string]any{
				"channel": t.Channel,
				"ok":      false,
				"stage":   "channel_info",
				"message": fmt.Sprintf("无法读取频道信息: %v", cerr),
			})
			continue
		}
		mem, merr := api.GetChatMember(ctx, qaToken, chatInfo.ID, me.ID)
		if merr != nil || mem == nil || !isChatMemberAdminOrMember(mem, true) {
			out = append(out, map[string]any{
				"channel": t.Channel,
				"ok":      false,
				"stage":   "channel_membership",
				"message": "助手机器人必须是该频道的管理员",
			})
			continue
		}
		if t.NeedDiscuss {
			if chatInfo.LinkedChatID == 0 {
				out = append(out, map[string]any{
					"channel": t.Channel,
					"ok":      false,
					"stage":   "discussion_link",
					"message": "频道未绑定关联讨论群",
				})
				continue
			}
			dMem, dmerr := api.GetChatMember(ctx, qaToken, chatInfo.LinkedChatID, me.ID)
			if dmerr != nil || dMem == nil || !isChatMemberAdminOrMember(dMem, false) {
				out = append(out, map[string]any{
					"channel": t.Channel,
					"ok":      false,
					"stage":   "discussion_membership",
					"message": "助手机器人尚未加入该频道的关联讨论群",
				})
				continue
			}
			if !me.CanReadAllGroupMessages && !isChatMemberAdminOrMember(dMem, true) {
				out = append(out, map[string]any{
					"channel": t.Channel,
					"ok":      false,
					"stage":   "discussion_visibility",
					"message": "助手机器人需关闭隐私模式或在讨论群内设为管理员以接收唤醒回复",
				})
				continue
			}
		}
		out = append(out, map[string]any{
			"channel": t.Channel,
			"ok":      true,
			"stage":   "complete",
			"message": "权限检查通过",
		})
	}
	return out
}

func formatBotAPIChatTarget(ch string) any {
	n := config.NormalizeChannelID(ch)
	if strings.HasPrefix(n, "-") {
		return n
	}
	if strings.HasPrefix(n, "@") {
		return n
	}
	return "@" + n
}

func isChatMemberAdminOrMember(m *models.ChatMember, requireAdmin bool) bool {
	if m == nil {
		return false
	}
	switch m.Type {
	case models.ChatMemberTypeOwner, models.ChatMemberTypeAdministrator:
		return true
	case models.ChatMemberTypeMember, models.ChatMemberTypeRestricted:
		return !requireAdmin
	default:
		return false
	}
}

func (s *Server) handlePostAssistantDiagnostics(w http.ResponseWriter, r *http.Request) {
	if s.deps.Config == nil {
		writeHTTPError(w, http.StatusServiceUnavailable, "配置服务未就绪")
		return
	}
	var req assistantConfigPayload
	if err := readJSONStrict(r, &req, 256*1024); err != nil {
		writeHTTPError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if err := validateAssistantRequest(req, s.deps.Config.Channels()); err != nil {
		writeHTTPError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	diags := s.runAssistantDiagnostics(r.Context(), req)
	writeEnvelopeOK(w, diags, "")
}

func (s *Server) handlePutAssistantConfig(w http.ResponseWriter, r *http.Request) {
	if s.deps.Config == nil {
		writeHTTPError(w, http.StatusServiceUnavailable, "配置服务未就绪")
		return
	}
	var req assistantConfigPayload
	if err := readJSONStrict(r, &req, 256*1024); err != nil {
		writeHTTPError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if err := validateAssistantRequest(req, s.deps.Config.Channels()); err != nil {
		writeHTTPError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	diags := s.runAssistantDiagnostics(r.Context(), req)
	for _, d := range diags {
		if ok, _ := d["ok"].(bool); !ok {
			writeHTTPError(w, http.StatusUnprocessableEntity, map[string]any{
				"message":     "助手权限检查未通过",
				"diagnostics": diags,
			})
			return
		}
	}

	err := s.deps.Config.UpdateConfig(func(doc map[string]any) error {
		doc["qa_assistant"] = req.Assistant
		doc["ai_pool"] = req.Pool
		return nil
	})
	if err != nil {
		writeHTTPError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.recordAudit(r, "ai.assistant.update", "qa_assistant", "", true, "已更新问答与评论助手设置", 0)
	writeEnvelopeOK(w, nil, "已保存；频道、订阅与签到配置立即生效，并发容量与回合限制重启助手后生效")
}

func (s *Server) handlePostAssistantBackfill(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Channel string `json:"channel"`
		PostID  int    `json:"post_id"`
		Limit   int    `json:"limit"`
	}
	body.Limit = 5000
	if err := readJSONStrict(r, &body, 64*1024); err != nil {
		writeHTTPError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if body.PostID < 1 || body.Limit < 1 || body.Limit > 20000 || strings.TrimSpace(body.Channel) == "" {
		writeHTTPError(w, http.StatusUnprocessableEntity, "参数无效")
		return
	}
	if s.deps.Config == nil {
		writeHTTPError(w, http.StatusServiceUnavailable, "配置服务未就绪")
		return
	}
	asst, err := s.deps.Config.AssistantSettings()
	if err != nil {
		writeHTTPError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	normCh := config.NormalizeChannelID(body.Channel)
	ruleEnabled := false
	for _, cm := range asst.Comments {
		if (cm.Channel == body.Channel || config.NormalizeChannelID(cm.Channel) == normCh) && cm.Enabled {
			ruleEnabled = true
			break
		}
	}
	if !ruleEnabled {
		writeHTTPError(w, http.StatusUnprocessableEntity, "请先为该频道启用评论助手规则")
		return
	}
	if s.deps.DB == nil {
		writeHTTPError(w, http.StatusConflict, "数据库未连接")
		return
	}
	if s.deps.UserBot == nil {
		writeHTTPError(w, http.StatusConflict, "UserBot 未连接，无法回填历史评论")
		return
	}
	client := s.deps.UserBot.Client()
	if client == nil || !client.IsConnected() {
		writeHTTPError(w, http.StatusConflict, "UserBot 未连接，无法回填历史评论")
		return
	}
	if profile := client.SelfProfile(); profile == nil || profile.IsBot {
		writeHTTPError(w, http.StatusConflict, "历史回溯需要已登录的 UserBot 用户账号")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 180*time.Second)
	defer cancel()
	channel, err := client.ResolveChannel(ctx, normCh)
	if err != nil || channel == nil || channel.MarkedID >= 0 {
		s.writeAssistantBackfillError(w, ctx, err)
		return
	}
	assistantTokenID, _, _ := strings.Cut(s.deps.Config.QABotToken(), ":")
	assistantID, _ := strconv.ParseInt(assistantTokenID, 10, 64)
	comments, truncated, visibility, err := client.BackfillDiscussionComments(ctx, normCh, body.PostID, body.Limit, assistantID)
	if err != nil {
		s.writeAssistantBackfillError(w, ctx, err)
		return
	}

	records := make([]db.CommentRecord, 0, len(comments))
	for _, c := range comments {
		records = append(records, db.CommentRecord{
			ChatID:     c.ChatID,
			MessageID:  int(c.MessageID),
			RootID:     int(c.RootID),
			ReplyID:    int(c.ReplyID),
			Channel:    c.Channel,
			UserID:     c.UserID,
			AuthorRole: c.AuthorRole,
			Text:       c.Text,
			Media:      c.MediaLabel,
			Date:       int(c.MessageDate.Unix()),
			Edited:     c.Edited,
		})
	}
	if len(records) > 0 {
		records[0].SourceChannelID = channel.MarkedID
		records[0].SourceMessageID = body.PostID
	}
	inserted, err := s.deps.DB.BackfillComments(ctx, channel.MarkedID, body.PostID, records)
	if err != nil {
		s.writeAssistantBackfillError(w, ctx, err)
		return
	}
	writeEnvelopeOK(w, map[string]any{
		"inserted":   inserted,
		"truncated":  truncated,
		"visibility": visibility,
	}, "")
}

func (s *Server) writeAssistantBackfillError(w http.ResponseWriter, ctx context.Context, err error) {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		writeHTTPError(w, http.StatusGatewayTimeout, "回填超时，可幂等重试")
		return
	}
	if err != nil {
		s.logger.Warn("评论历史回溯失败", "error", err)
	}
	writeHTTPError(w, http.StatusBadGateway, "历史回溯失败，请确认帖子、UserBot 权限以及 Go 助手表结构已初始化")
}

// ────────────────────────────── Legacy /api/ai/config, /qa-quota, /prompts ──────────────────────────────

func (s *Server) handleGetLegacyAIConfig(w http.ResponseWriter, _ *http.Request) {
	if s.deps.Config == nil {
		writeHTTPError(w, http.StatusServiceUnavailable, "配置服务未就绪")
		return
	}
	view, err := s.deps.Config.LegacyAIConfig()
	if err != nil {
		writeHTTPError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeEnvelopeOK(w, view, "")
}

func (s *Server) handlePutLegacyAIConfig(w http.ResponseWriter, r *http.Request) {
	if s.deps.Config == nil {
		writeHTTPError(w, http.StatusServiceUnavailable, "配置服务未就绪")
		return
	}
	var body struct {
		BaseURL *string `json:"base_url"`
		Model   *string `json:"model"`
		APIKey  *string `json:"api_key"`
	}
	if err := readJSONBody(r, &body, 64*1024); err != nil {
		writeHTTPError(w, http.StatusBadRequest, "无效的请求体")
		return
	}
	hasCtrl := func(str string) bool {
		for _, ch := range str {
			if ch < 32 {
				return true
			}
		}
		return false
	}

	if body.BaseURL != nil {
		u := strings.TrimSpace(*body.BaseURL)
		if u != "" {
			if hasCtrl(u) {
				writeHTTPError(w, http.StatusBadRequest, "base_url 包含非法控制字符")
				return
			}
			parsed, err := url.Parse(u)
			if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
				writeHTTPError(w, http.StatusBadRequest, "base_url 必须为合法的 HTTP/HTTPS 地址且不含凭据或查询参数")
				return
			}
		}
	}
	if body.Model != nil && hasCtrl(strings.TrimSpace(*body.Model)) {
		writeHTTPError(w, http.StatusBadRequest, "model 包含非法控制字符")
		return
	}
	if body.APIKey != nil && hasCtrl(strings.TrimSpace(*body.APIKey)) {
		writeHTTPError(w, http.StatusBadRequest, "api_key 包含非法控制字符")
		return
	}

	err := s.deps.Config.UpdateConfig(func(doc map[string]any) error {
		aiMap, ok := doc["ai"].(map[string]any)
		if !ok || aiMap == nil {
			aiMap = map[string]any{}
			doc["ai"] = aiMap
		}
		if body.BaseURL != nil {
			aiMap["base_url"] = strings.TrimSpace(*body.BaseURL)
		}
		if body.Model != nil {
			aiMap["model"] = strings.TrimSpace(*body.Model)
		}
		if body.APIKey != nil {
			aiMap["api_key"] = strings.TrimSpace(*body.APIKey)
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, config.ErrConfigConflict) {
			writeHTTPError(w, http.StatusConflict, err.Error())
			return
		}
		writeHTTPError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.recordAudit(r, "ai.config.update", "ai", "", true, "已更新顶层 AI 配置", 0)
	writeEnvelopeOK(w, nil, "AI 配置已保存并立即生效，后续请求使用新配置")
}

func (s *Server) handleGetQAQuota(w http.ResponseWriter, _ *http.Request) {
	if s.deps.Config == nil {
		writeHTTPError(w, http.StatusServiceUnavailable, "配置服务未就绪")
		return
	}
	q, err := s.deps.Config.EffectiveQuota()
	if err != nil {
		writeHTTPError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeEnvelopeOK(w, map[string]any{
		"user_daily_limit":  q.UserDaily,
		"total_daily_limit": q.TotalDaily,
	}, "")
}

func (s *Server) handlePutQAQuota(w http.ResponseWriter, r *http.Request) {
	if s.deps.Config == nil {
		writeHTTPError(w, http.StatusServiceUnavailable, "配置服务未就绪")
		return
	}
	var body struct {
		UserDailyLimit  *int `json:"user_daily_limit"`
		TotalDailyLimit *int `json:"total_daily_limit"`
	}
	if err := readJSONStrict(r, &body, 32*1024); err != nil {
		writeHTTPError(w, http.StatusBadRequest, err.Error())
		return
	}
	if body.UserDailyLimit == nil && body.TotalDailyLimit == nil {
		writeHTTPError(w, http.StatusBadRequest, "请至少提供一个配额字段")
		return
	}
	if body.UserDailyLimit != nil && (*body.UserDailyLimit < 1 || *body.UserDailyLimit > 2147483647) {
		writeHTTPError(w, http.StatusBadRequest, "user_daily_limit 需在 1..2147483647 之间")
		return
	}
	if body.TotalDailyLimit != nil && (*body.TotalDailyLimit < 10 || *body.TotalDailyLimit > 2147483647) {
		writeHTTPError(w, http.StatusBadRequest, "total_daily_limit 需在 10..2147483647 之间")
		return
	}

	err := s.deps.Config.UpdateConfig(func(doc map[string]any) error {
		qMap, ok := doc["qa_quota"].(map[string]any)
		if !ok || qMap == nil {
			qMap = map[string]any{}
			doc["qa_quota"] = qMap
		}
		if body.UserDailyLimit != nil {
			qMap["user_daily_limit"] = *body.UserDailyLimit
		}
		if body.TotalDailyLimit != nil {
			qMap["total_daily_limit"] = *body.TotalDailyLimit
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, config.ErrConfigConflict) {
			writeHTTPError(w, http.StatusConflict, err.Error())
			return
		}
		writeHTTPError(w, http.StatusBadRequest, err.Error())
		return
	}
	q, _ := s.deps.Config.EffectiveQuota()
	writeEnvelopeOK(w, map[string]any{
		"user_daily_limit":  q.UserDaily,
		"total_daily_limit": q.TotalDaily,
	}, "问答配额已更新")
}

func parsePromptTypeParam(raw string) (config.PromptType, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "summary":
		return config.PromptSummary, true
	case "poll":
		return config.PromptPoll, true
	case "qa":
		return config.PromptQA, true
	default:
		return "", false
	}
}

func (s *Server) handleGetPrompts(w http.ResponseWriter, _ *http.Request) {
	if s.deps.Config == nil {
		writeHTTPError(w, http.StatusServiceUnavailable, "配置服务未就绪")
		return
	}
	kinds := []config.PromptType{config.PromptSummary, config.PromptPoll, config.PromptQA}
	out := make([]map[string]any, 0, len(kinds))
	for _, k := range kinds {
		content, _ := s.deps.Config.GetPrompt(k)
		isDef, _ := s.deps.Config.IsDefaultPrompt(k)
		out = append(out, map[string]any{
			"prompt_type": string(k),
			"content":     content,
			"is_default":  isDef,
		})
	}
	writeEnvelopeOK(w, out, "")
}

func (s *Server) handleGetPromptByType(w http.ResponseWriter, r *http.Request) {
	pt, ok := parsePromptTypeParam(r.PathValue("prompt_type"))
	if !ok {
		writeHTTPError(w, http.StatusBadRequest, "不支持的提示词类型")
		return
	}
	if s.deps.Config == nil {
		writeHTTPError(w, http.StatusServiceUnavailable, "配置服务未就绪")
		return
	}
	content, err := s.deps.Config.GetPrompt(pt)
	if err != nil {
		writeHTTPError(w, http.StatusInternalServerError, err.Error())
		return
	}
	isDef, _ := s.deps.Config.IsDefaultPrompt(pt)
	writeEnvelopeOK(w, map[string]any{
		"prompt_type": string(pt),
		"content":     content,
		"is_default":  isDef,
	}, "")
}

func (s *Server) handlePutPromptByType(w http.ResponseWriter, r *http.Request) {
	pt, ok := parsePromptTypeParam(r.PathValue("prompt_type"))
	if !ok {
		writeHTTPError(w, http.StatusBadRequest, "不支持的提示词类型")
		return
	}
	var body struct {
		Content string `json:"content"`
	}
	if err := readJSONBody(r, &body, 512*1024); err != nil {
		writeHTTPError(w, http.StatusBadRequest, "无效的请求体")
		return
	}
	if strings.TrimSpace(body.Content) == "" {
		writeHTTPError(w, http.StatusBadRequest, "提示词内容不能为空")
		return
	}
	if s.deps.Config == nil {
		writeHTTPError(w, http.StatusServiceUnavailable, "配置服务未就绪")
		return
	}
	if err := s.deps.Config.SavePrompt(pt, body.Content); err != nil {
		writeHTTPError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.recordAudit(r, "ai.prompt.update", string(pt), "", true, "已更新提示词", 0)
	writeEnvelopeOK(w, nil, "提示词已保存并生效")
}

func (s *Server) handleResetPromptByType(w http.ResponseWriter, r *http.Request) {
	pt, ok := parsePromptTypeParam(r.PathValue("prompt_type"))
	if !ok {
		writeHTTPError(w, http.StatusBadRequest, "不支持的提示词类型")
		return
	}
	if s.deps.Config == nil {
		writeHTTPError(w, http.StatusServiceUnavailable, "配置服务未就绪")
		return
	}
	if err := s.deps.Config.ResetPrompt(pt); err != nil {
		writeHTTPError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.recordAudit(r, "ai.prompt.reset", string(pt), "", true, "已恢复默认提示词", 0)
	writeEnvelopeOK(w, nil, "已恢复默认提示词")
}

var _ = slices.Contains[[]string, string]
