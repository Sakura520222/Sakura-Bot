// Copyright 2026 Sakura-Bot
// SPDX-License-Identifier: AGPL-3.0-or-later

package webapi

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/Sakura520222/Sakura-Bot/internal/db"
	"github.com/Sakura520222/Sakura-Bot/internal/logging"
	"github.com/Sakura520222/Sakura-Bot/internal/mtproto"
	"github.com/Sakura520222/Sakura-Bot/internal/process"
	"github.com/Sakura520222/Sakura-Bot/internal/version"
)

// QAProcess is the assistant lifecycle and health interface used by the WebUI.
type QAProcess interface {
	Start() process.QAActionResult
	Stop(time.Duration) process.QAActionResult
	Restart() process.QAActionResult
	Status() process.QAStatus
	SetRuntime(string) map[string]any
	CheckHealth() (healthy, shouldRestart bool, message string)
}

func (s *Server) registerSystemRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/system/status", s.handleSystemStatus)
	mux.HandleFunc("POST /api/system/pause", s.handleSchedulerState(true))
	mux.HandleFunc("POST /api/system/resume", s.handleSchedulerState(false))
	mux.HandleFunc("PUT /api/system/log-level", s.handleSystemLogLevel)
	mux.HandleFunc("POST /api/system/restart", s.handleSystemRestart)
	mux.HandleFunc("POST /api/system/database/clear-and-restart", s.handleSystemDatabaseClear)
	mux.HandleFunc("POST /api/system/qa-bot/start", s.handleQAAction("start", QAProcess.Start, true))
	mux.HandleFunc("POST /api/system/qa-bot/stop", s.handleQAAction("stop", func(q QAProcess) process.QAActionResult {
		return q.Stop(5 * time.Second)
	}, false))
	mux.HandleFunc("POST /api/system/qa-bot/restart", s.handleQAAction("restart", QAProcess.Restart, true))
	mux.HandleFunc("POST /api/system/qa-bot/runtime", s.handleQARuntime)
	mux.HandleFunc("POST /api/system/qa-bot/health", s.handleQAHealth)
	mux.HandleFunc("POST /api/system/config/reload", s.handleSystemConfigReload)
	mux.HandleFunc("GET /api/system/cache/discussion", s.handleDiscussionCacheStatus)
	mux.HandleFunc("DELETE /api/system/cache/discussion", s.handleDiscussionCacheClear)
	mux.HandleFunc("GET /api/system/database/status", s.handleSystemDatabaseStatus)
	mux.HandleFunc("POST /api/system/database/cleanup/forwarded-messages", s.handleSystemCleanup(
		"forwarded_messages", "转发记录", (*db.Manager).CleanupForwardedMessages,
	))
	mux.HandleFunc("POST /api/system/database/cleanup/poll-regenerations", s.handleSystemCleanup(
		"poll_regenerations", "投票重生成记录", (*db.Manager).CleanupPollRegenerations,
	))
	mux.HandleFunc("POST /api/system/database/cleanup/audit-logs", s.handleSystemCleanup(
		"audit_logs", "审计记录", (*db.Manager).CleanupAuditLogs,
	))
	mux.HandleFunc("GET /api/system/logs/recent", s.handleRecentSystemLogs)
	mux.HandleFunc("GET /api/system/audit-logs", s.handleSystemAuditLogs)
}

func (s *Server) handleSystemStatus(w http.ResponseWriter, r *http.Request) {
	scheduler := s.deps.Scheduler
	state := "running"
	if scheduler != nil && scheduler.IsPaused() {
		state = "paused"
	}
	channelCount, forwardingEnabled := 0, false
	if cfg := s.deps.Config; cfg != nil {
		channelCount = len(cfg.Channels())
		forwardingEnabled = cfg.ForwardingConfig().Enabled
	}
	qaStatus := process.QAStatus{Runtime: "go", ConfiguredRuntime: "go"}
	if manager := s.deps.QAManager; manager != nil {
		qaStatus = manager.Status()
	}
	uptime := 0.0
	if !s.startedAt.IsZero() {
		uptime = max(0, time.Since(s.startedAt).Seconds())
	}
	selectedClient := s.systemTelegramClient()
	cacheSize := 0
	if selectedClient != nil {
		cacheSize = len(selectedClient.LinkedChatCacheChannels())
	}
	audit := map[string]any{"recent_count": 0}
	if store := s.deps.DB; store != nil && store.DB != nil {
		rows, err := store.ListAuditLogs(r.Context(), 5)
		if err != nil {
			s.logger.Warn("无法读取运维审计摘要", "error", err)
			audit["message"] = "无法读取运维审计记录"
		} else {
			audit["recent_count"] = len(rows)
			audit["recent"] = rows
		}
	}
	writeEnvelopeOK(w, map[string]any{
		"status": state, "version": version.Version, "log_level": logging.GetDefaultLevel(),
		"channel_count": channelCount, "forwarding_enabled": forwardingEnabled,
		"qa_bot_running": qaStatus.Running, "qa_bot": qaStatus,
		"userbot_connected": selectedClient != nil && selectedClient.IsConnected(),
		"uptime_seconds":    uptime, "database": s.systemDatabaseStatus(r.Context()),
		"cache": map[string]int{"size": cacheSize}, "logs": systemLogStatus(s.systemLogPath()), "audit": audit,
	}, "")
}

func (s *Server) handleSchedulerState(pause bool) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		scheduler := s.deps.Scheduler
		if scheduler == nil {
			writeHTTPError(w, http.StatusServiceUnavailable, "调度器不可用")
			return
		}
		if pause {
			if scheduler.IsPaused() {
				writeEnvelopeFail(w, "Bot 已处于暂停状态", nil)
				return
			}
			scheduler.Pause()
			writeEnvelopeOK(w, nil, "Bot 已暂停")
			return
		}
		if !scheduler.IsPaused() {
			writeEnvelopeFail(w, "Bot 已处于运行状态", nil)
			return
		}
		scheduler.Resume()
		writeEnvelopeOK(w, nil, "Bot 已恢复")
	}
}

func (s *Server) handleSystemLogLevel(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Level string `json:"level"`
	}
	if err := readJSONBody(r, &body, 4096); err != nil {
		writeHTTPError(w, http.StatusUnprocessableEntity, "日志级别无效")
		return
	}
	level := strings.ToUpper(strings.TrimSpace(body.Level))
	switch level {
	case "DEBUG", "INFO", "WARNING", "ERROR", "CRITICAL":
	default:
		writeHTTPError(w, http.StatusUnprocessableEntity, "无效的日志级别")
		return
	}
	cfg := s.deps.Config
	if cfg == nil {
		writeHTTPError(w, http.StatusServiceUnavailable, "配置不可用")
		return
	}
	err := cfg.UpdateConfig(func(doc map[string]any) error {
		doc["log_level"] = level
		return nil
	})
	if err != nil {
		s.logger.Error("修改日志级别失败", "error", err)
		writeHTTPError(w, http.StatusInternalServerError, "修改日志级别失败")
		return
	}
	logging.SetDefaultLevel(level)
	writeEnvelopeOK(w, nil, "日志级别已修改为: "+level)
}

func (s *Server) handleSystemRestart(w http.ResponseWriter, _ *http.Request) {
	if s.deps.TriggerRestart == nil {
		writeHTTPError(w, http.StatusServiceUnavailable, "Bot 进程控制器不可用")
		return
	}
	s.deps.TriggerRestart("webui_restart")
	writeEnvelopeOK(w, nil, "正在重启 Bot...")
}

func (s *Server) handleSystemDatabaseClear(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	store := s.deps.DB
	if store == nil || store.DB == nil {
		writeHTTPError(w, http.StatusServiceUnavailable, "数据库管理器不可用")
		return
	}
	if s.deps.TriggerRestart == nil {
		writeHTTPError(w, http.StatusServiceUnavailable, "Bot 进程控制器不可用")
		return
	}
	results, deleted, err := store.ClearAllData(r.Context())
	if err != nil {
		failed := make([]string, 0)
		for name, count := range results {
			if count < 0 {
				failed = append(failed, name)
			}
		}
		slices.Sort(failed)
		message := "清空数据库失败"
		if len(failed) > 0 {
			message = "清空数据库部分失败: " + strings.Join(failed, ", ")
		}
		s.logger.Error(message, "error", err)
		s.recordAudit(r, "database.clear_and_restart", "", "{}", false, message, int(time.Since(start).Milliseconds()))
		writeHTTPError(w, http.StatusInternalServerError, message)
		return
	}
	// A successful clear includes the audit table.
	s.logger.Warn("已通过 WebUI 清空数据库并请求重启 Bot", "deleted", deleted, "tables", results)
	s.deps.TriggerRestart("webui_clear_database_restart")
	writeEnvelopeOK(w, map[string]any{"deleted_total": deleted, "tables": results},
		fmt.Sprintf("已清空数据库（共 %d 条记录），正在重启 Bot...", deleted))
}

func (s *Server) handleQAAction(action string, invoke func(QAProcess) process.QAActionResult, includePID bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		if s.deps.QAManager == nil {
			writeHTTPError(w, http.StatusServiceUnavailable, "QA 管理器不可用")
			return
		}
		result := invoke(s.deps.QAManager)
		s.recordAudit(r, "qa_bot."+action, "", "", result.Success, result.Message, int(time.Since(start).Milliseconds()))
		var data map[string]any
		if includePID {
			data = map[string]any{"pid": result.PID}
		}
		writeSystemResult(w, result.Success, result.Message, data)
	}
}

func (s *Server) handleQARuntime(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	if s.deps.QAManager == nil {
		writeHTTPError(w, http.StatusServiceUnavailable, "QA 管理器不可用")
		return
	}
	var body struct {
		Runtime string `json:"runtime"`
	}
	if err := readJSONBody(r, &body, 4096); err != nil {
		writeHTTPError(w, http.StatusBadRequest, "运行时参数无效")
		return
	}
	result := s.deps.QAManager.SetRuntime(strings.TrimSpace(body.Runtime))
	success, _ := result["success"].(bool)
	message, _ := result["message"].(string)
	s.recordAudit(r, "qa_bot.runtime", "", "", success, message, int(time.Since(start).Milliseconds()))
	writeSystemResult(w, success, message, map[string]any{"runtime": result["runtime"], "restart": result["restart"]})
}

func (s *Server) handleQAHealth(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	if s.deps.QAManager == nil {
		writeHTTPError(w, http.StatusServiceUnavailable, "QA 管理器不可用")
		return
	}
	healthy, shouldRestart, message := s.deps.QAManager.CheckHealth()
	s.recordAudit(r, "qa_bot.health", "", "", healthy, message, int(time.Since(start).Milliseconds()))
	writeEnvelopeOK(w, map[string]any{"healthy": healthy, "should_restart": shouldRestart, "message": message}, "")
}

func writeSystemResult(w http.ResponseWriter, ok bool, message string, data map[string]any) {
	if data == nil {
		writeJSON(w, http.StatusOK, map[string]any{"success": ok, "message": message})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": ok, "message": message, "data": data})
}

func (s *Server) handleSystemConfigReload(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	cfg := s.deps.Config
	if cfg == nil {
		writeHTTPError(w, http.StatusServiceUnavailable, "配置不可用")
		return
	}
	rolledBack, err := cfg.Reload(r.Context())
	if err != nil {
		message := "配置重载失败"
		if rolledBack {
			message += "，已恢复上一份有效配置"
		}
		s.logger.Error(message, "error", err)
		s.recordAudit(r, "config.reload", "", "", false, message, int(time.Since(start).Milliseconds()))
		writeHTTPError(w, http.StatusInternalServerError, message)
		return
	}
	const message = "配置已重载"
	s.recordAudit(r, "config.reload", "", "", true, message, int(time.Since(start).Milliseconds()))
	writeEnvelopeOK(w, map[string]int{"config_keys": len(cfg.RawSnapshot())}, message)
}

func (s *Server) systemTelegramClient() *mtproto.Client {
	if manager := s.deps.UserBot; manager != nil {
		if client := manager.Client(); client != nil && client.IsConnected() {
			return client
		}
	}
	return s.deps.BotClient
}

func (s *Server) handleDiscussionCacheStatus(w http.ResponseWriter, _ *http.Request) {
	channels := []string{}
	if client := s.systemTelegramClient(); client != nil {
		channels = client.LinkedChatCacheChannels()
	}
	writeEnvelopeOK(w, map[string]any{"size": len(channels), "channels": channels}, "")
}

func (s *Server) handleDiscussionCacheClear(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	channel := r.URL.Query().Get("channel")
	cleared := 0
	if client := s.systemTelegramClient(); client != nil && (channel == "" || strings.TrimSpace(channel) != "") {
		cleared = client.ClearLinkedChatCache(channel)
	}
	target := channel
	message := "频道缓存不存在: " + channel
	if channel == "" {
		target = "all"
		message = fmt.Sprintf("已清理全部讨论组缓存: %d 条", cleared)
	} else if cleared > 0 {
		message = "已清理频道缓存: " + channel
	}
	s.recordAudit(r, "cache.discussion.clear", target, "", true, message, int(time.Since(start).Milliseconds()))
	writeEnvelopeOK(w, map[string]int{"cleared": cleared}, message)
}

func (s *Server) systemDatabaseStatus(ctx context.Context) map[string]any {
	store := s.deps.DB
	if store == nil || store.DB == nil {
		return map[string]any{"available": false, "message": "数据库管理器不可用"}
	}
	status, err := store.DatabaseStatus(ctx)
	if err != nil {
		s.logger.Warn("数据库状态查询失败", "error", err)
		return map[string]any{"available": false, "message": "数据库状态查询失败"}
	}
	return status
}

func (s *Server) handleSystemDatabaseStatus(w http.ResponseWriter, r *http.Request) {
	writeEnvelopeOK(w, s.systemDatabaseStatus(r.Context()), "")
}

func (s *Server) handleSystemCleanup(action, label string, cleanup func(*db.Manager, context.Context, int) (int64, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		body := struct {
			Days int `json:"days"`
		}{Days: 30}
		if err := readJSONBody(r, &body, 4096); err != nil || body.Days < 1 || body.Days > 3650 {
			writeHTTPError(w, http.StatusUnprocessableEntity, "days 必须为 1 到 3650 的整数")
			return
		}
		store := s.deps.DB
		if store == nil || store.DB == nil {
			writeHTTPError(w, http.StatusServiceUnavailable, "数据库管理器不可用")
			return
		}
		count, err := cleanup(store, r.Context(), body.Days)
		params := fmt.Sprintf(`{"days": %d}`, body.Days)
		duration := int(time.Since(start).Milliseconds())
		if err != nil {
			message := "清理" + label + "失败"
			s.logger.Warn(message, "error", err)
			s.recordAudit(r, "database.cleanup."+action, "", params, false, message, duration)
			writeHTTPError(w, http.StatusInternalServerError, message)
			return
		}
		message := fmt.Sprintf("已清理 %d 条旧%s", count, label)
		s.recordAudit(r, "database.cleanup."+action, "", params, true, message, duration)
		writeEnvelopeOK(w, map[string]any{"deleted_count": count, "days": body.Days}, message)
	}
}

func (s *Server) systemLogPath() string {
	options := logging.DefaultMainOptions()
	if cfg := s.deps.Config; cfg != nil && cfg.Env != nil {
		options.LoadEnv(cfg.Env)
	}
	return options.BaseLogFilePath
}

func systemLogStatus(path string) map[string]any {
	var size int64
	info, err := os.Stat(path)
	exists := err == nil && info.Mode().IsRegular()
	if exists {
		size = info.Size()
	}
	return map[string]any{"exists": exists, "size_bytes": size, "path": path}
}

func (s *Server) handleRecentSystemLogs(w http.ResponseWriter, r *http.Request) {
	query := wikiQuery{request: r}
	count := query.number("lines", 100, 1, 1000)
	level := query.text("level", "", 0)
	keyword := query.text("keyword", "", 100)
	if !query.valid(w) {
		return
	}

	path := s.systemLogPath()
	lines, err := recentLogLines(path, count, level, keyword)
	message := ""
	if errors.Is(err, os.ErrNotExist) {
		message = "日志文件不存在"
	} else if err != nil {
		s.logger.Warn("读取最近日志失败", "error", err)
		writeHTTPError(w, http.StatusInternalServerError, "读取最近日志失败")
		return
	}
	writeEnvelopeOK(w, map[string]any{"lines": lines, "total_returned": len(lines), "path": filepath.Base(path)}, message)
}

// Scan the last three pages of lines without reading a full rotated file.
func recentLogLines(path string, count int, level, keyword string) ([]string, error) {
	selected := []string{}
	file, err := os.Open(path)
	if err != nil {
		return selected, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return selected, err
	}
	if !info.Mode().IsRegular() {
		return selected, os.ErrNotExist
	}

	const maxBytes = 8 << 20
	start := max(0, info.Size()-maxBytes)
	data, err := io.ReadAll(io.NewSectionReader(file, start, info.Size()-start))
	if err != nil {
		return selected, err
	}
	if start > 0 {
		end := bytes.IndexByte(data, '\n')
		if end < 0 {
			return selected, nil
		}
		data = data[end+1:]
	}
	if len(data) == 0 {
		return selected, nil
	}
	data = bytes.TrimSuffix(data, []byte{'\n'})
	level, keyword = strings.ToLower(level), strings.ToLower(keyword)
	for candidates := 0; candidates < count*3 && len(selected) < count; candidates++ {
		prev := bytes.LastIndexByte(data, '\n')
		line := strings.TrimSuffix(strings.ToValidUTF8(string(data[prev+1:]), "\uFFFD"), "\r")
		lower := strings.ToLower(line)
		if strings.Contains(lower, level) && strings.Contains(lower, keyword) {
			selected = append(selected, line)
		}
		if prev < 0 {
			break
		}
		data = data[:prev]
	}
	slices.Reverse(selected)
	return selected, nil
}

func (s *Server) handleSystemAuditLogs(w http.ResponseWriter, r *http.Request) {
	query := wikiQuery{request: r}
	limit := query.number("limit", 50, 1, 200)
	if !query.valid(w) {
		return
	}
	items := []db.AuditRow{}
	if store := s.deps.DB; store != nil && store.DB != nil {
		rows, err := store.ListAuditLogs(r.Context(), limit)
		if err != nil {
			s.replyDatabaseError(w, err)
			return
		}
		if rows != nil {
			items = rows
		}
	}
	writeEnvelopeOK(w, map[string]any{"items": items, "total": len(items)}, "")
}
