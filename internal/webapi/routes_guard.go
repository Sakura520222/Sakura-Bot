// Copyright 2026 Sakura-Bot
// SPDX-License-Identifier: AGPL-3.0-or-later

package webapi

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/Sakura520222/Sakura-Bot/internal/db"
	guarddb "github.com/Sakura520222/Sakura-Bot/internal/db/guard"
	"github.com/Sakura520222/Sakura-Bot/internal/guard"
	"github.com/Sakura520222/Sakura-Bot/internal/i18n"
	"github.com/Sakura520222/Sakura-Bot/internal/wiki"
)

type guardHandler func(http.ResponseWriter, *http.Request, *guard.Service, any)

func (s *Server) registerGuardRoutes(mux *http.ServeMux) {
	for _, route := range []struct {
		method, path string
		handler      guardHandler
	}{
		{"GET", "/config", s.guardConfig},
		{"GET", "/groups", s.guardDirectory},
		{"POST", "/groups", s.guardAddGroup},
		{"GET", "/groups/{chat_id}/verification", s.guardVerifyDiagnostics},
		{"PUT", "/groups/{chat_id}", s.guardSaveGroup},
		{"PATCH", "/groups/{chat_id}", s.guardSwitchGroup},
		{"PATCH", "/groups/{chat_id}/settings", s.guardPatchGroup},
		{"GET", "/chats", s.guardDirectory},
		{"POST", "/chats/leave-preview", s.guardChatCleanup},
		{"POST", "/chats/leave", s.guardChatCleanup},
		{"POST", "/chats/history-cleanup-preview", s.guardChatCleanup},
		{"POST", "/chats/history-cleanup", s.guardChatCleanup},
		{"GET", "/chats/deleted-users", s.guardDeletedStatus},
		{"POST", "/chats/deleted-users", s.guardDeletedStart},
		{"GET", "/chats/deleted-users/jobs/{job_id}", s.guardDeletedJob},
		{"PATCH", "/chats/deleted-users/settings", s.guardPatchDeleted},
		{"PATCH", "/nickname-keywords", s.guardPatchNicknames},
		{"PATCH", "/retention", s.guardRetention},
		{"PUT", "/retention", s.guardRetention},
		{"GET", "/records/{table}", s.guardRecords},
		{"POST", "/cases/{case_id}/review", s.guardReview},
		{"POST", "/cases/{case_id}/retry", s.guardRetry},
		{"PUT", "/whitelist", s.guardWhitelist},
		{"DELETE", "/blacklist/{user_id}", s.guardRemoveBlacklist},
	} {
		mux.HandleFunc(route.method+" /api/guard"+route.path, s.withGuard(route.handler))
	}
	s.registerGuardLearningRoutes(mux)
}

func (s *Server) withGuard(next guardHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		service := s.deps.Guard
		if service == nil {
			writeHTTPError(w, http.StatusServiceUnavailable, s.guardMessage("guard.unavailable"))
			return
		}
		user := wikiActor(r)
		if user == "admin" {
			next(w, r, service, "admin")
			return
		}
		id, err := strconv.ParseInt(user, 10, 64)
		if err != nil || !slices.Contains(service.DefaultAdmins(), id) {
			writeHTTPError(w, http.StatusForbidden, s.guardMessage("error.permission_denied"))
			return
		}
		next(w, r, service, id)
	}
}

func (s *Server) guardMessage(code string) string {
	return i18n.T(code, nil)
}

func (s *Server) guardError(w http.ResponseWriter, err error) {
	var guardErr guard.GuardError
	var cleanupErr guard.ChatCleanupError
	if errors.As(err, &guardErr) || errors.As(err, &cleanupErr) {
		writeHTTPError(w, http.StatusConflict, s.guardMessage(err.Error()))
		return
	}
	s.logger.Error("Sakura Guard 请求失败", "error", err)
	writeHTTPError(w, http.StatusInternalServerError, "Sakura Guard 请求失败")
}

func (s *Server) guardSuccess(w http.ResponseWriter, data any, err error) {
	if err != nil {
		s.guardError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, data)
}

func (s *Server) guardConfig(w http.ResponseWriter, _ *http.Request, g *guard.Service, _ any) {
	writeJSON(w, http.StatusOK, map[string]any{
		"config": g.Config(), "defaults": guard.DefaultGuardGroup(),
		"runtime": map[string]bool{
			"ai_report_worker_running":    g.AIReports() != nil && g.AIReports().IsRunning(),
			"bot_message_observer_active": g.IsObserverActive(),
		},
	})
}

func (s *Server) guardDirectory(w http.ResponseWriter, r *http.Request, g *guard.Service, _ any) {
	refresh := false
	if value := r.URL.Query().Get("refresh"); value != "" {
		var err error
		refresh, err = strconv.ParseBool(value)
		if err != nil {
			writeHTTPError(w, http.StatusUnprocessableEntity, "无效的 refresh 参数")
			return
		}
	}
	g.Directory().Refresh(refresh)
	data := g.Directory().Snapshot()
	if r.URL.Path == "/api/guard/chats" {
		data["cleanup"] = g.ChatCleanup().Status()
		data["history_cleanup"] = g.HistoryCleanup().Status()
	}
	writeJSON(w, http.StatusOK, data)
}

func (s *Server) guardChatID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("chat_id"), 10, 64)
	if err != nil || id == math.MinInt64 || id >= 0 {
		writeHTTPError(w, http.StatusUnprocessableEntity, s.guardMessage("guard.invalid_group"))
		return 0, false
	}
	return id, true
}

func (s *Server) guardVerifyDiagnostics(w http.ResponseWriter, r *http.Request, g *guard.Service, _ any) {
	if chat, ok := s.guardChatID(w, r); ok {
		data, err := g.Repository().VerificationDiagnostics(r.Context(), chat, g.Verification().ConfiguredAssistantID())
		s.guardSuccess(w, data, err)
	}
}

func (s *Server) guardChatCleanup(w http.ResponseWriter, r *http.Request, g *guard.Service, actor any) {
	cleanup := g.ChatCleanup()
	if strings.Contains(r.URL.Path, "history-cleanup") {
		cleanup = g.HistoryCleanup()
	}
	user := fmt.Sprint(actor)
	if strings.HasSuffix(r.URL.Path, "-preview") {
		data, err := cleanup.Preview(user)
		s.guardSuccess(w, data, err)
		return
	}
	var body struct {
		Token     string `json:"token"`
		Confirmed bool   `json:"confirmed"`
	}
	if readJSONStrict(r, &body, 1<<14) != nil || len(body.Token) < 32 || len(body.Token) > 128 || !body.Confirmed {
		writeHTTPError(w, http.StatusUnprocessableEntity, "请先预览并确认操作名单")
		return
	}
	audit := func(ctx context.Context, action, actor, target string, ok bool, message string) {
		if s.deps.DB != nil {
			err := s.deps.DB.RecordAuditLog(ctx, db.AuditEntry{
				Action: action, Actor: actor, Target: target, Success: ok, Message: message,
			})
			if err != nil {
				s.logger.Warn("记录 Guard 清理审计失败", "error", err)
			}
		}
	}
	data, err := cleanup.Start(body.Token, user, audit)
	s.guardSuccess(w, data, err)
}

func (s *Server) guardDeletedStatus(w http.ResponseWriter, r *http.Request, g *guard.Service, _ any) {
	data, err := g.DeletedCleanup().Status(r.Context())
	s.guardSuccess(w, data, err)
}

func (s *Server) guardDeletedStart(w http.ResponseWriter, r *http.Request, g *guard.Service, actor any) {
	var body struct {
		RequestID string  `json:"request_id"`
		ChatIDs   []int64 `json:"chat_ids"`
		Confirmed bool    `json:"confirmed"`
	}
	if readJSONStrict(r, &body, 1<<18) != nil || !wikiIDPattern.MatchString(body.RequestID) || !body.Confirmed ||
		(body.ChatIDs != nil && (len(body.ChatIDs) == 0 || len(body.ChatIDs) > 5000)) ||
		slices.ContainsFunc(body.ChatIDs, func(id int64) bool { return id >= 0 || id == math.MinInt64 }) {
		writeHTTPError(w, http.StatusUnprocessableEntity, "请确认清理已销号成员和清理范围")
		return
	}
	slices.Sort(body.ChatIDs)
	data, err := g.DeletedCleanup().Start(r.Context(), fmt.Sprint(actor), body.RequestID, slices.Compact(body.ChatIDs))
	if err == nil {
		s.recordAudit(r, "guard.deleted_cleanup.start", fmt.Sprint(data["id"]), "", true, "已受理已销号成员清理", 0)
	}
	s.guardSuccess(w, data, err)
}

func (s *Server) guardDeletedJob(w http.ResponseWriter, r *http.Request, g *guard.Service, _ any) {
	data, err := g.DeletedCleanup().Job(r.Context(), r.PathValue("job_id"))
	if err == nil && data == nil {
		writeHTTPError(w, http.StatusNotFound, "清理任务不存在")
		return
	}
	s.guardSuccess(w, data, err)
}

func (q *wikiQuery) optionalInt64(key string) *int64 {
	value := q.text(key, "", 0)
	if value == "" {
		return nil
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		q.err = key
		return nil
	}
	return &n
}

func (s *Server) guardRecords(w http.ResponseWriter, r *http.Request, g *guard.Service, _ any) {
	table := r.PathValue("table")
	if !slices.Contains([]string{"cases", "whitelist", "blacklist", "verifications", "notifications"}, table) {
		writeHTTPError(w, http.StatusUnprocessableEntity, "无效的群管记录类型")
		return
	}
	q := wikiQuery{request: r}
	filter := guarddb.RecordFilter{
		ChatID: q.optionalInt64("chat_id"), UserID: q.optionalInt64("user_id"), Status: q.text("status", "", 0),
		Limit: q.number("limit", 50, 1, 200), Offset: q.number("offset", 0, 0, math.MaxInt/200), Sanitize: true,
	}
	if q.valid(w) {
		rows, err := g.Repository().QueryRecords(r.Context(), table, filter)
		s.guardSuccess(w, rows, err)
	}
}

func (s *Server) guardReview(w http.ResponseWriter, r *http.Request, g *guard.Service, actor any) {
	var body struct {
		Action     string `json:"action"`
		Generation *int64 `json:"generation"`
	}
	if readJSONBody(r, &body, 1<<14) != nil || !slices.Contains([]string{"allow", "blacklist", "unban", "ban", "delete", "kick"}, body.Action) ||
		(body.Generation != nil && (*body.Generation < 0 || *body.Generation > 99999999)) {
		writeHTTPError(w, http.StatusUnprocessableEntity, "无效的审核操作")
		return
	}
	row, err := g.Reviews().Review(r.Context(), r.PathValue("case_id"), body.Action, actor, true, nil, body.Generation)
	s.guardSuccess(w, map[string]any{"success": true, "case": row}, err)
}

func guardID(row guarddb.Row, key string) int64 {
	switch value := row[key].(type) {
	case int64:
		return value
	case int:
		return int64(value)
	case uint64:
		return int64(value)
	}
	value, _ := strconv.ParseInt(fmt.Sprint(row[key]), 10, 64)
	return value
}

func (s *Server) guardRetry(w http.ResponseWriter, r *http.Request, g *guard.Service, _ any) {
	ctx, id := r.Context(), r.PathValue("case_id")
	row, err := g.Repository().GetCase(ctx, id)
	if err != nil {
		s.guardError(w, err)
		return
	}
	if row == nil {
		writeHTTPError(w, http.StatusNotFound, s.guardMessage("guard.invalid_callback"))
		return
	}
	err = g.Repository().WithLock(ctx, guardID(row, "user_id"), func(st *guarddb.Store) error {
		current, err := st.Case(ctx, id)
		if err != nil || current == nil {
			return guard.GuardError("guard.invalid_callback")
		}
		payload, _ := current["payload"].(map[string]any)
		cleanup, _ := payload["ban_message_cleanup"].(map[string]any)
		if cleanup["status"] == "pending" && current["status"] != "execution_failed" {
			g.BanHistory().Resume(ctx, st, current, true)
			row, err = st.Case(ctx, id)
			if err == nil && row != nil {
				g.Notifier().Sync(ctx, st, row, g.Group(guardID(current, "chat_id")))
			}
			return err
		}
		if current["status"] != "execution_failed" {
			return guard.GuardError("guard.already_processing")
		}
		if err := st.UpdateCase(ctx, id, "executing", nil, nil); err != nil {
			return err
		}
		current, err = st.Case(ctx, id)
		if err != nil {
			return err
		}
		if err := g.ExecuteCase(ctx, st, current); err != nil {
			return err
		}
		row, err = st.Case(ctx, id)
		return err
	})
	if err == nil && row["status"] == "execution_failed" {
		writeHTTPError(w, http.StatusBadGateway, s.guardMessage("guard.operation_failed"))
		return
	}
	s.guardSuccess(w, map[string]any{"success": true, "case": row}, err)
}

func (s *Server) guardWhitelist(w http.ResponseWriter, r *http.Request, g *guard.Service, actor any) {
	body := struct {
		ChatID  int64 `json:"chat_id"`
		UserID  int64 `json:"user_id"`
		Enabled bool  `json:"enabled"`
	}{Enabled: true}
	if readJSONBody(r, &body, 1<<14) != nil || body.ChatID >= 0 || body.UserID <= 0 {
		writeHTTPError(w, http.StatusUnprocessableEntity, "无效的白名单操作")
		return
	}
	ctx := r.Context()
	err := g.Repository().WithLock(ctx, body.UserID, func(st *guarddb.Store) error {
		action := "whitelist_remove"
		if body.Enabled {
			blacklisted, err := st.Blacklisted(ctx, body.UserID)
			if err != nil {
				return err
			}
			if blacklisted {
				return guard.GuardError("guard.blacklist_conflict")
			}
			var reviewer *int64
			reviewerActor := "webui:admin"
			if id, ok := actor.(int64); ok {
				reviewer = &id
				reviewerActor = "telegram:" + strconv.FormatInt(id, 10)
			}
			if err := st.AddWhitelist(ctx, body.ChatID, body.UserID, reviewer, reviewerActor); err != nil {
				return err
			}
			g.Learning().RecordWhitelist(ctx, st, body.ChatID, body.UserID, fmt.Sprint(actor))
			action = "whitelist_add"
		} else if err := st.RemoveWhitelist(ctx, body.ChatID, body.UserID); err != nil {
			return err
		}
		return g.RecordEvent(ctx, st, body.ChatID, body.UserID, action, wiki.NewID(), "whitelist", actor, nil)
	})
	s.guardSuccess(w, map[string]bool{"success": true}, err)
}

func (s *Server) guardRemoveBlacklist(w http.ResponseWriter, r *http.Request, g *guard.Service, actor any) {
	id, err := strconv.ParseInt(r.PathValue("user_id"), 10, 64)
	if err != nil || id <= 0 {
		writeHTTPError(w, http.StatusUnprocessableEntity, "无效的用户 ID")
		return
	}
	ctx := r.Context()
	err = g.Repository().WithLock(ctx, id, func(st *guarddb.Store) error {
		previous, err := st.Blacklist(ctx, id)
		if err != nil || previous == nil {
			return err
		}
		return st.Atomic(ctx, func(tx *guarddb.Store) error {
			caseID := wiki.NewID()
			payload, _ := previous["payload"].(map[string]any)
			err := tx.CreateCaseRow(ctx, guarddb.Row{
				"id": caseID, "event_key": caseID + strings.Repeat("0", 32),
				"chat_id": guardID(previous, "source_chat_id"), "user_id": id,
				"action": "blacklist_remove", "status": "done",
				"payload": map[string]any{
					"name":     payload["name"],
					"evidence": map[string]string{"target": "global_blacklist", "rule_id": "manual", "reason": "admin:" + fmt.Sprint(actor)},
				},
			})
			if err != nil {
				return err
			}
			_, err = tx.RemoveBlacklist(ctx, id)
			return err
		})
	})
	s.guardSuccess(w, map[string]bool{"success": true}, err)
}
