// Copyright 2026 Sakura-Bot
// SPDX-License-Identifier: AGPL-3.0-or-later

package webapi

import (
	"errors"
	"fmt"
	"maps"
	"math"
	"net/http"
	"reflect"
	"slices"
	"strconv"

	"github.com/Sakura520222/Sakura-Bot/internal/config"
	"github.com/Sakura520222/Sakura-Bot/internal/guard"
)

type guardConflict struct {
	current map[string]any
	fields  []string
}

func (e *guardConflict) Error() string { return "guard.config_conflict" }

var (
	errGuardMissingGroup = errors.New("missing guard group")
	errGuardInvalidPatch = errors.New("invalid guard settings patch")
)

type guardChanges struct {
	Changes  map[string]any `json:"changes"`
	Previous map[string]any `json:"previous"`
}

func readGuardChanges(r *http.Request) (guardChanges, bool) {
	var body guardChanges
	err := readJSONStrict(r, &body, 1<<20)
	return body, err == nil && len(body.Changes) != 0 &&
		slices.Equal(slices.Sorted(maps.Keys(body.Changes)), slices.Sorted(maps.Keys(body.Previous)))
}

func checkGuardChanges(now map[string]any, body guardChanges) error {
	var conflicts []string
	for key, value := range body.Changes {
		current, ok := now[key]
		if !ok || (key != "review_chat_id" && (value == nil || body.Previous[key] == nil)) {
			return errGuardInvalidPatch
		}
		if !reflect.DeepEqual(current, body.Previous[key]) && !reflect.DeepEqual(current, value) {
			conflicts = append(conflicts, key)
		}
	}
	if len(conflicts) > 0 {
		slices.Sort(conflicts)
		return &guardConflict{current: now, fields: conflicts}
	}
	return nil
}

func (s *Server) guardConfigWriteError(w http.ResponseWriter, err error) {
	var conflict *guardConflict
	switch {
	case errors.As(err, &conflict):
		writeHTTPError(w, http.StatusConflict, map[string]any{
			"message": s.guardMessage(conflict.Error()), "current": conflict.current, "fields": conflict.fields,
		})
	case errors.Is(err, errGuardMissingGroup):
		writeHTTPError(w, http.StatusNotFound, s.guardMessage("guard.group_not_found"))
	case errors.Is(err, errGuardInvalidPatch):
		writeHTTPError(w, http.StatusUnprocessableEntity, "无效的群管设置")
	case errors.Is(err, config.ErrConfigPersistence):
		s.logger.Error("保存 Guard 设置失败", "error", err)
		writeHTTPError(w, http.StatusServiceUnavailable, "配置保存失败")
	case errors.Is(err, config.ErrConfigConflict):
		writeHTTPError(w, http.StatusConflict, err.Error())
	default:
		s.guardError(w, err)
	}
}

func (s *Server) guardUpdateConfig(g *guard.Service, change func(*guard.GuardConfig, map[string]any) error) error {
	if s.deps.Config == nil {
		return config.ErrConfigPersistence
	}
	err := s.deps.Config.UpdateConfig(func(doc map[string]any) error {
		guard.MigrateNicknameKeywords(doc)
		cfg, err := guard.ParseGuardConfig(doc)
		if err != nil {
			return err
		}
		if err := change(&cfg, configObject(doc, "sakura_guard")); err != nil {
			return err
		}
		if _, err := guard.ParseGuardConfig(doc); err != nil {
			return fmt.Errorf("%w: %v", errGuardInvalidPatch, err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	return g.ReloadFromAppConfig()
}

func (s *Server) guardAddGroup(w http.ResponseWriter, r *http.Request, g *guard.Service, _ any) {
	var body struct {
		ChatID int64 `json:"chat_id"`
	}
	if readJSONBody(r, &body, 1<<14) != nil || body.ChatID >= 0 || body.ChatID == math.MinInt64 {
		writeHTTPError(w, http.StatusUnprocessableEntity, s.guardMessage("guard.invalid_group"))
		return
	}
	key := strconv.FormatInt(body.ChatID, 10)
	var created bool
	err := s.guardUpdateConfig(g, func(cfg *guard.GuardConfig, section map[string]any) error {
		if _, exists := cfg.Groups[key]; !exists {
			configObject(section, "groups")[key] = guard.StructToMap(guard.DefaultGuardGroup())
			created = true
		}
		return nil
	})
	if err != nil {
		s.guardConfigWriteError(w, err)
		return
	}
	g.Directory().Remember(body.ChatID, "")
	g.Directory().Refresh(false)
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true, "created": created, "chat_id": key, "group": g.Group(body.ChatID),
	})
}

func (s *Server) guardSaveGroup(w http.ResponseWriter, r *http.Request, g *guard.Service, _ any) {
	id, ok := s.guardChatID(w, r)
	if !ok {
		return
	}
	group := guard.DefaultGuardGroup()
	if readJSONStrict(r, &group, 1<<20) != nil {
		writeHTTPError(w, http.StatusUnprocessableEntity, "无效的群管设置")
		return
	}
	if err := group.Validate(); err != nil {
		writeHTTPError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if err := g.SaveGroup(r.Context(), id, group); err != nil {
		s.guardConfigWriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

var guardSwitches = []string{
	"enabled", "kick_button_enabled", "verify_join", "nickname_filter", "message_filter",
	"block_bot_messages", "block_button_messages", "block_forwarded_messages", "block_contact_messages",
	"ai_report_review_enabled", "ai_learning_enabled", "allow_anonymous_admin_report", "share_global_blacklist",
	"ban_any_traditional", "allow_group_admin_review", "notification_enabled", "verification_notice_cleanup",
}

func (s *Server) guardSwitchGroup(w http.ResponseWriter, r *http.Request, g *guard.Service, _ any) {
	var body struct {
		Field string `json:"field"`
		Value *bool  `json:"value"`
	}
	if readJSONStrict(r, &body, 1<<14) != nil || body.Value == nil || !slices.Contains(guardSwitches, body.Field) {
		writeHTTPError(w, http.StatusUnprocessableEntity, "无效的群管开关")
		return
	}
	s.updateGuardGroup(w, r, g, guardChanges{Changes: map[string]any{body.Field: *body.Value}})
}

func (s *Server) guardPatchGroup(w http.ResponseWriter, r *http.Request, g *guard.Service, _ any) {
	body, ok := readGuardChanges(r)
	if !ok {
		writeHTTPError(w, http.StatusUnprocessableEntity, "请提供修改字段及其原值")
		return
	}
	s.updateGuardGroup(w, r, g, body)
}

func (s *Server) updateGuardGroup(w http.ResponseWriter, r *http.Request, g *guard.Service, body guardChanges) {
	id, ok := s.guardChatID(w, r)
	if !ok {
		return
	}
	key := strconv.FormatInt(id, 10)
	err := s.guardUpdateConfig(g, func(cfg *guard.GuardConfig, section map[string]any) error {
		current, exists := cfg.Groups[key]
		if !exists {
			return errGuardMissingGroup
		}
		if body.Previous != nil {
			if err := checkGuardChanges(guard.StructToMap(current), body); err != nil {
				return err
			}
		}
		updated, err := guard.ParseGuardGroupWithPatch(current, body.Changes)
		if err != nil {
			return fmt.Errorf("%w: %v", errGuardInvalidPatch, err)
		}
		if err := g.ValidateGroup(r.Context(), id, updated); err != nil {
			return err
		}
		configObject(section, "groups")[key] = guard.StructToMap(updated)
		return nil
	})
	if err != nil {
		s.guardConfigWriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "group": g.Group(id)})
}

func (s *Server) guardPatchDeleted(w http.ResponseWriter, r *http.Request, g *guard.Service, _ any) {
	body, ok := readGuardChanges(r)
	if !ok {
		writeHTTPError(w, http.StatusUnprocessableEntity, "请提供修改字段及其原值")
		return
	}
	err := s.guardUpdateConfig(g, func(cfg *guard.GuardConfig, section map[string]any) error {
		if err := checkGuardChanges(guard.StructToMap(cfg.DeletedUserCleanup), body); err != nil {
			return err
		}
		maps.Copy(configObject(section, "deleted_user_cleanup"), body.Changes)
		return nil
	})
	if err != nil {
		s.guardConfigWriteError(w, err)
		return
	}
	s.recordAudit(r, "guard.deleted_cleanup.settings", "", "", true, "已更新已销号成员定期清理设置", 0)
	writeJSON(w, http.StatusOK, g.Config().DeletedUserCleanup)
}

func (s *Server) guardPatchNicknames(w http.ResponseWriter, r *http.Request, g *guard.Service, _ any) {
	var body struct {
		Keywords []string `json:"nickname_keywords"`
		Previous []string `json:"previous"`
	}
	if readJSONStrict(r, &body, 64<<20) != nil || body.Keywords == nil || body.Previous == nil {
		writeHTTPError(w, http.StatusUnprocessableEntity, "无效的昵称关键词")
		return
	}
	keywords, err := guard.ValidateWords(body.Keywords, 50000)
	previous, prevErr := guard.ValidateWords(body.Previous, 50000)
	if err != nil || prevErr != nil {
		writeHTTPError(w, http.StatusUnprocessableEntity, "无效的昵称关键词")
		return
	}
	err = s.guardUpdateConfig(g, func(cfg *guard.GuardConfig, section map[string]any) error {
		current := cfg.NicknameKeywords
		if !slices.Equal(current, previous) && !slices.Equal(current, keywords) {
			return &guardConflict{current: map[string]any{"nickname_keywords": current}, fields: []string{"nickname_keywords"}}
		}
		section["nickname_keywords"] = keywords
		return nil
	})
	if err != nil {
		s.guardConfigWriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "nickname_keywords": g.Config().NicknameKeywords})
}

func validRetention(days int) bool { return days >= 7 && days <= 3650 }

func (s *Server) guardRetention(w http.ResponseWriter, r *http.Request, g *guard.Service, _ any) {
	var body struct {
		Days     int  `json:"retention_days"`
		Previous *int `json:"previous"`
	}
	patch := r.Method == http.MethodPatch
	if readJSONStrict(r, &body, 1<<14) != nil || !validRetention(body.Days) ||
		(patch && (body.Previous == nil || !validRetention(*body.Previous))) {
		writeHTTPError(w, http.StatusUnprocessableEntity, "无效的记录保留天数")
		return
	}
	err := s.guardUpdateConfig(g, func(cfg *guard.GuardConfig, section map[string]any) error {
		if patch && cfg.RetentionDays != *body.Previous && cfg.RetentionDays != body.Days {
			return &guardConflict{current: map[string]any{"retention_days": cfg.RetentionDays}, fields: []string{"retention_days"}}
		}
		section["retention_days"] = body.Days
		return nil
	})
	if err != nil {
		s.guardConfigWriteError(w, err)
		return
	}
	data := map[string]any{"success": true}
	if patch {
		data["retention_days"] = g.Config().RetentionDays
	}
	writeJSON(w, http.StatusOK, data)
}
