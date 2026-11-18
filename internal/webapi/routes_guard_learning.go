// Copyright 2026 Sakura-Bot
// SPDX-License-Identifier: AGPL-3.0-or-later

package webapi

import (
	"errors"
	"fmt"
	"maps"
	"math"
	"net/http"
	"slices"
	"strings"

	guarddb "github.com/Sakura520222/Sakura-Bot/internal/db/guard"
	"github.com/Sakura520222/Sakura-Bot/internal/guard"
)

func (s *Server) registerGuardLearningRoutes(mux *http.ServeMux) {
	for _, route := range []struct {
		method, path string
		handler      guardHandler
	}{
		{"GET", "", s.guardLearningConfig},
		{"PATCH", "", s.guardPatchLearning},
		{"GET", "/rules", s.guardLearningRules},
		{"POST", "/rules/{rule_id}", s.guardManageRule},
		{"GET", "/rules/{rule_id}", s.guardRuleDetail},
		{"PATCH", "/rules/{rule_id}", s.guardRuleAction},
		{"PUT", "/rules/{rule_id}/scopes", s.guardRuleScopes},
		{"GET", "/rules/{rule_id}/hits", s.guardRuleHits},
		{"GET", "/samples", s.guardLearningSamples},
		{"POST", "/samples/backfill", s.guardBackfillSamples},
		{"POST", "/samples/{sample_id}/{action}", s.guardReviewSample},
		{"GET", "/jobs", s.guardLearningJobs},
		{"POST", "/jobs/{sample_id}/retry", s.guardRetryLearningJob},
	} {
		mux.HandleFunc(route.method+" /api/guard/ai-learning"+route.path, s.withGuard(route.handler))
	}
}

func (s *Server) guardLearningConfig(w http.ResponseWriter, _ *http.Request, g *guard.Service, _ any) {
	learning := g.Learning()
	writeJSON(w, http.StatusOK, map[string]any{
		"settings": g.Config().AILearning,
		"defaults": guard.DefaultAILearningSettings(),
		"runtime":  map[string]any{"worker_running": learning.IsRunning(), "cached_rules": learning.Cache().CachedCount()},
	})
}

func (s *Server) guardPatchLearning(w http.ResponseWriter, r *http.Request, g *guard.Service, _ any) {
	body, ok := readGuardChanges(r)
	if !ok {
		writeHTTPError(w, http.StatusUnprocessableEntity, "请提供修改字段及其原值")
		return
	}
	err := s.guardUpdateConfig(g, func(cfg *guard.GuardConfig, section map[string]any) error {
		current := guard.StructToMap(cfg.AILearning)
		err := checkGuardChanges(current, body)
		var conflict *guardConflict
		if errors.As(err, &conflict) {
			filtered := make(map[string]any, len(conflict.fields))
			for _, field := range conflict.fields {
				filtered[field] = current[field]
			}
			conflict.current = filtered
		}
		if err != nil {
			return err
		}
		maps.Copy(configObject(section, "ai_learning"), body.Changes)
		return nil
	})
	if err != nil {
		s.guardConfigWriteError(w, err)
		return
	}
	s.recordAudit(r, "guard.ai_learning.settings", "", "", true, "已更新 AI 智能风控学习设置", 0)
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "settings": g.Config().AILearning})
}

func (s *Server) guardLearningRules(w http.ResponseWriter, r *http.Request, g *guard.Service, _ any) {
	q := wikiQuery{request: r}
	chat := q.optionalInt64("chat_id")
	ruleType := q.enum("rule_type", "", "", "nickname", "username", "message", "link", "pattern")
	status := q.enum("status", "", "", "candidate", "active", "disabled", "rejected", "expired")
	limit, offset := q.number("limit", 50, 1, 200), q.number("offset", 0, 0, math.MaxInt/200)
	if q.valid(w) {
		rows, err := g.Repository().ListAIRules(r.Context(), chat, ruleType, status, limit, offset)
		s.guardSuccess(w, rows, err)
	}
}

var errAIRuleMissing = errors.New("规则不存在")

func (s *Server) guardRuleReply(w http.ResponseWriter, value any, err error) {
	if errors.Is(err, errAIRuleMissing) {
		writeHTTPError(w, http.StatusNotFound, err.Error())
		return
	}
	s.guardSuccess(w, value, err)
}

func (s *Server) guardRuleDetail(w http.ResponseWriter, r *http.Request, g *guard.Service, _ any) {
	ctx, id := r.Context(), r.PathValue("rule_id")
	var rule guarddb.Row
	var shared []int64
	err := g.Repository().Reader(ctx, func(st *guarddb.Store) error {
		var err error
		rule, err = st.GetAIRule(ctx, id)
		if err == nil && rule == nil {
			return errAIRuleMissing
		}
		if err == nil {
			shared, err = st.AIRuleScopes(ctx, id)
		}
		return err
	})
	if shared == nil {
		shared = []int64{}
	}
	s.guardRuleReply(w, map[string]any{
		"rule": rule, "shared_chats": shared, "sharing_enabled": g.Config().AILearning.SharedRulesEnabled,
	}, err)
}

func (s *Server) guardManageRule(w http.ResponseWriter, r *http.Request, g *guard.Service, actor any) {
	var body struct {
		Action string `json:"action"`
	}
	if readJSONStrict(r, &body, 1<<14) != nil || !slices.Contains([]string{"activate", "reject", "pause", "resume", "delete"}, body.Action) {
		writeHTTPError(w, http.StatusUnprocessableEntity, "无效的规则操作")
		return
	}
	ctx, id := r.Context(), r.PathValue("rule_id")
	target := map[string]string{"activate": "active", "resume": "active", "reject": "rejected", "pause": "disabled"}[body.Action]
	allowed := map[string][]string{
		"activate": {"candidate", "disabled", "expired"}, "reject": {"candidate"},
		"pause": {"active"}, "resume": {"disabled"},
	}[body.Action]
	err := g.Repository().Reader(ctx, func(st *guarddb.Store) error {
		if body.Action == "delete" {
			rule, err := st.GetAIRule(ctx, id)
			if err != nil {
				return err
			}
			if rule == nil {
				return errAIRuleMissing
			}
			return st.DeleteAIRuleCascade(ctx, id)
		}
		return st.Atomic(ctx, func(tx *guarddb.Store) error {
			rule, err := tx.GetAIRuleLocked(ctx, id)
			if err != nil {
				return err
			}
			if rule == nil {
				return errAIRuleMissing
			}
			state, _ := rule["status"].(string)
			if !slices.Contains(allowed, state) {
				return guard.GuardError(fmt.Sprintf("规则当前状态 %s 不允许该操作", state))
			}
			return g.Learning().TransitionRule(ctx, tx, rule, target, "webui:"+fmt.Sprint(actor), target == "active", map[string]any{"operation": body.Action})
		})
	})
	if err == nil {
		g.Learning().Wake()
		s.recordAudit(r, "guard.ai_learning.rule."+body.Action, id, "", true, "AI 规则操作已执行", 0)
	}
	s.guardRuleReply(w, map[string]bool{"success": true}, err)
}

func (s *Server) guardRuleAction(w http.ResponseWriter, r *http.Request, g *guard.Service, actor any) {
	var body struct {
		Action string `json:"action"`
	}
	if readJSONStrict(r, &body, 1<<14) != nil || !slices.Contains([]string{"delete", "warn", "mute", "kick", "ban"}, body.Action) {
		writeHTTPError(w, http.StatusUnprocessableEntity, "无效的规则命中动作")
		return
	}
	ctx, id := r.Context(), r.PathValue("rule_id")
	var rule guarddb.Row
	changed := false
	err := g.Repository().Reader(ctx, func(st *guarddb.Store) error {
		return st.Atomic(ctx, func(tx *guarddb.Store) error {
			var err error
			rule, err = tx.GetAIRuleLocked(ctx, id)
			if err != nil {
				return err
			}
			if rule == nil {
				return errAIRuleMissing
			}
			if rule["action"] != body.Action {
				rule, err = g.Learning().SetRuleAction(ctx, tx, rule, body.Action, "webui:"+fmt.Sprint(actor))
				changed = err == nil
			}
			return err
		})
	})
	if err == nil && changed {
		g.Learning().Wake()
		s.recordAudit(r, "guard.ai_learning.rule.action", id, "", true, "AI 规则命中动作已调整", 0)
	}
	s.guardRuleReply(w, map[string]any{"success": true, "rule": rule}, err)
}

func (s *Server) guardRuleScopes(w http.ResponseWriter, r *http.Request, g *guard.Service, actor any) {
	var body struct {
		ChatIDs []int64 `json:"chat_ids"`
	}
	if readJSONStrict(r, &body, 1<<16) != nil || len(body.ChatIDs) > 500 ||
		slices.ContainsFunc(body.ChatIDs, func(id int64) bool { return id >= 0 || id == math.MinInt64 }) {
		writeHTTPError(w, http.StatusUnprocessableEntity, "无效的群组 ID")
		return
	}
	slices.Sort(body.ChatIDs)
	ctx, id := r.Context(), r.PathValue("rule_id")
	var shared []int64
	err := g.Repository().Reader(ctx, func(st *guarddb.Store) error {
		return st.Atomic(ctx, func(tx *guarddb.Store) error {
			rule, err := tx.GetAIRuleLocked(ctx, id)
			if err != nil {
				return err
			}
			if rule == nil {
				return errAIRuleMissing
			}
			shared, err = g.Learning().SetRuleShares(ctx, tx, rule, slices.Compact(body.ChatIDs), "webui:"+fmt.Sprint(actor))
			return err
		})
	})
	if err == nil {
		g.Learning().Wake()
		s.recordAudit(r, "guard.ai_learning.rule.scopes", id, "", true, "已更新 AI 规则共享群组", 0)
	}
	s.guardRuleReply(w, map[string]any{"success": true, "shared_chats": shared}, err)
}

func (s *Server) guardRuleHits(w http.ResponseWriter, r *http.Request, g *guard.Service, _ any) {
	q := wikiQuery{request: r}
	limit := q.number("limit", 50, 1, 200)
	if q.valid(w) {
		rows, err := g.Repository().RecentAIRuleHits(r.Context(), r.PathValue("rule_id"), limit)
		if rows == nil {
			rows = []guarddb.Row{}
		}
		s.guardSuccess(w, map[string]any{"rows": rows}, err)
	}
}

func (s *Server) guardLearningSamples(w http.ResponseWriter, r *http.Request, g *guard.Service, _ any) {
	q := wikiQuery{request: r}
	filter := guarddb.AISampleFilter{
		ChatID: q.optionalInt64("chat_id"),
		Label:  q.enum("label", "", "", "pending", "positive", "negative", "neutral"),
		Status: q.enum("status", "", "", "new", "queued", "learned", "skipped", "invalid"),
		Source: q.enum("source", "", "", "admin_report", "member_report", "review_outcome", "ai_review", "rule_auto_ban", "admin_blacklist"),
		Limit:  q.number("limit", 50, 1, 200), Offset: q.number("offset", 0, 0, math.MaxInt/200),
	}
	if q.valid(w) {
		page, err := g.Repository().ListAISamples(r.Context(), filter)
		s.guardSuccess(w, page, err)
	}
}

func (s *Server) guardBackfillSamples(w http.ResponseWriter, r *http.Request, g *guard.Service, _ any) {
	q := wikiQuery{request: r}
	chat := q.optionalInt64("chat_id")
	cursor := q.id("cursor", false)
	if !q.valid(w) {
		return
	}
	if chat == nil || *chat >= 0 || *chat == math.MinInt64 {
		writeHTTPError(w, http.StatusUnprocessableEntity, s.guardMessage("guard.invalid_group"))
		return
	}
	learning := g.Learning()
	if !learning.Enabled(*chat) || (!learning.SourceAllowed("rule_auto_ban") && !learning.SourceAllowed("admin_blacklist")) {
		writeHTTPError(w, http.StatusConflict, s.guardMessage("guard.ai_learning_backfill_disabled"))
		return
	}
	data, err := learning.BackfillEnforcement(r.Context(), *chat, cursor)
	s.guardSuccess(w, data, err)
}

func (s *Server) guardReviewSample(w http.ResponseWriter, r *http.Request, g *guard.Service, actor any) {
	id, action := r.PathValue("sample_id"), r.PathValue("action")
	if !slices.Contains([]string{"confirm", "correct", "delete"}, action) {
		writeHTTPError(w, http.StatusUnprocessableEntity, "无效的学习样本操作")
		return
	}
	found, err := g.Learning().ReviewSample(r.Context(), id, action, fmt.Sprint(actor))
	if err == nil && !found {
		writeHTTPError(w, http.StatusNotFound, s.guardMessage("guard.invalid_callback"))
		return
	}
	s.guardSuccess(w, map[string]bool{"success": true}, err)
}

func (s *Server) guardLearningJobs(w http.ResponseWriter, r *http.Request, g *guard.Service, _ any) {
	q := wikiQuery{request: r}
	chat := q.optionalInt64("chat_id")
	status := q.enum("status", "", "", "queued", "running", "done", "manual", "skipped")
	limit, offset := q.number("limit", 50, 1, 200), q.number("offset", 0, 0, math.MaxInt/200)
	if q.valid(w) {
		rows, err := g.Repository().ListAILearningJobs(r.Context(), chat, status, limit, offset)
		s.guardSuccess(w, rows, err)
	}
}

func (s *Server) guardRetryLearningJob(w http.ResponseWriter, r *http.Request, g *guard.Service, _ any) {
	ctx, id := r.Context(), strings.TrimSpace(r.PathValue("sample_id"))
	err := g.Repository().Reader(ctx, func(st *guarddb.Store) error {
		requeued, err := st.RequeueAILearningJob(ctx, id, false)
		if err == nil && !requeued {
			return guard.GuardError("仅转人工的任务可重新排队")
		}
		return err
	})
	if err == nil {
		g.Learning().Wake()
		s.recordAudit(r, "guard.ai_learning.job.retry", id, "", true, "学习任务已重新排队", 0)
	}
	s.guardSuccess(w, map[string]bool{"success": true}, err)
}
