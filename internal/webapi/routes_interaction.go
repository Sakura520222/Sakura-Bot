// Copyright 2026 Sakura-Bot
// SPDX-License-Identifier: AGPL-3.0-or-later

package webapi

import (
	"errors"
	"net/http"

	"github.com/Sakura520222/Sakura-Bot/internal/config"
)

func (s *Server) registerInteractionRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/interaction/channels", func(w http.ResponseWriter, r *http.Request) {
		writeEnvelopeOK(w, map[string]any{"channels": s.deps.Config.Channels()}, "")
	})
	mux.HandleFunc("GET /api/interaction/poll-settings", s.pollSettings)
	mux.HandleFunc("PUT /api/interaction/poll-settings/global", s.updateGlobalPoll)
	mux.HandleFunc("PUT /api/interaction/poll-settings/{channel...}", s.updateChannelPoll)
	mux.HandleFunc("DELETE /api/interaction/poll-settings/{channel...}", s.deleteChannelPoll)
	mux.HandleFunc("GET /api/interaction/auto-poll", s.autoPollSettings)
	mux.HandleFunc("PUT /api/interaction/auto-poll", s.updateAutoPoll)
	mux.HandleFunc("PUT /api/interaction/auto-poll/{channel...}", s.updateAutoPoll)
	mux.HandleFunc("DELETE /api/interaction/auto-poll/{channel...}", s.deleteAutoPoll)
	mux.HandleFunc("GET /api/interaction/comment-welcome", s.commentWelcome)
	mux.HandleFunc("PUT /api/interaction/comment-welcome/default", s.updateCommentWelcome)
	mux.HandleFunc("PUT /api/interaction/comment-welcome/{channel...}", s.updateCommentWelcome)
}

func (s *Server) pollSettings(w http.ResponseWriter, _ *http.Request) {
	cfg := s.deps.Config
	writeEnvelopeOK(w, map[string]any{
		"settings":    settingsMap(cfg.RawSnapshot(), "channel_poll_settings"),
		"enable_poll": cfg.EnablePoll(), "poll_regen_threshold": cfg.PollRegenThreshold(),
		"enable_vote_regen_request": cfg.EnableVoteRegenRequest(), "public_voters": cfg.PollPublicVoters(),
	}, "")
}

func (s *Server) updateGlobalPoll(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled   *bool `json:"enable_poll"`
		Threshold *int  `json:"poll_regen_threshold"`
		Public    *bool `json:"public_voters"`
		VoteRegen *bool `json:"enable_vote_regen_request"`
	}
	if err := readJSONBody(r, &body, 8192); err != nil {
		writeHTTPError(w, http.StatusUnprocessableEntity, "无效的全局投票配置")
		return
	}
	if body.Threshold != nil && *body.Threshold < 1 {
		writeHTTPError(w, http.StatusBadRequest, "重新生成阈值必须 >= 1")
		return
	}
	s.saveInteraction(w, "全局投票设置已更新", func(doc map[string]any) error {
		if body.Threshold != nil {
			doc["poll_regen_threshold"] = *body.Threshold
		}
		setOptionalBool(doc, "enable_poll", body.Enabled)
		setOptionalBool(doc, "public_voters", body.Public)
		setOptionalBool(doc, "enable_vote_regen_request", body.VoteRegen)
		return nil
	})
}

func (s *Server) updateChannelPoll(w http.ResponseWriter, r *http.Request) {
	var body map[string]*bool
	if err := readJSONBody(r, &body, 8192); err != nil || body == nil {
		writeHTTPError(w, http.StatusUnprocessableEntity, "无效的频道投票配置")
		return
	}
	ch := config.NormalizeChannelID(r.PathValue("channel"))
	s.saveInteraction(w, "投票设置已更新: "+ch, func(doc map[string]any) error {
		set := settingsMap(doc, "channel_poll_settings")
		item := settingsMap(set, equivalentlyKeyed(set, ch))
		for _, field := range []string{"enabled", "send_to_channel", "public_voters"} {
			value, provided := body[field]
			if !provided {
				continue
			}
			if value == nil {
				delete(item, field)
			} else {
				item[field] = *value
			}
		}
		return nil
	})
}

func (s *Server) deleteChannelPoll(w http.ResponseWriter, r *http.Request) {
	ch := config.NormalizeChannelID(r.PathValue("channel"))
	s.saveInteraction(w, "频道投票配置已删除: "+ch, func(doc map[string]any) error {
		settings := settingsMap(doc, "channel_poll_settings")
		delete(settings, equivalentlyKeyed(settings, ch))
		return nil
	})
}

func (s *Server) autoPollSettings(w http.ResponseWriter, _ *http.Request) {
	cfg := s.deps.Config
	writeEnvelopeOK(w, map[string]any{
		"enabled":          cfg.EnableAutoPoll(),
		"channel_settings": settingsMap(cfg.RawSnapshot(), "channel_auto_poll_settings"),
	}, "")
}

var errAutoPollDisabled = errors.New("请先开启全局自动趣味投票")

func (s *Server) updateAutoPoll(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	if err := readJSONBody(r, &body, 8192); err != nil || body.Enabled == nil {
		writeHTTPError(w, http.StatusUnprocessableEntity, "缺少自动投票开关")
		return
	}
	ch := config.NormalizeChannelID(r.PathValue("channel"))
	message := "频道自动投票设置已更新: " + ch
	if ch == "" {
		message = "自动投票已禁用"
		if *body.Enabled {
			message = "自动投票已启用"
		}
	}
	s.saveInteraction(w, message, func(doc map[string]any) error {
		if ch == "" {
			doc["enable_auto_poll"] = *body.Enabled
			return nil
		}
		if enabled, _ := doc["enable_auto_poll"].(bool); !enabled {
			return errAutoPollDisabled
		}
		set := settingsMap(doc, "channel_auto_poll_settings")
		set[equivalentlyKeyed(set, ch)] = map[string]bool{"enabled": *body.Enabled}
		return nil
	})
}

func (s *Server) deleteAutoPoll(w http.ResponseWriter, r *http.Request) {
	ch := config.NormalizeChannelID(r.PathValue("channel"))
	s.saveInteraction(w, "频道自动投票配置已删除: "+ch, func(doc map[string]any) error {
		if enabled, _ := doc["enable_auto_poll"].(bool); !enabled {
			return errAutoPollDisabled
		}
		set := settingsMap(doc, "channel_auto_poll_settings")
		delete(set, equivalentlyKeyed(set, ch))
		return nil
	})
}

func (s *Server) commentWelcome(w http.ResponseWriter, _ *http.Request) {
	doc := s.deps.Config.RawSnapshot()
	writeEnvelopeOK(w, map[string]any{
		"default":           settingsMap(settingsMap(doc, "comment_welcome"), "default"),
		"channel_overrides": settingsMap(doc, "channel_comment_welcome"),
	}, "")
}

func (s *Server) updateCommentWelcome(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled      *bool   `json:"enabled"`
		Welcome      *string `json:"welcome_message"`
		ButtonText   *string `json:"button_text"`
		ButtonAction *string `json:"button_action"`
	}
	if err := readJSONBody(r, &body, 32*1024); err != nil {
		writeHTTPError(w, http.StatusUnprocessableEntity, "无效的评论区欢迎配置")
		return
	}
	ch := config.NormalizeChannelID(r.PathValue("channel"))
	s.saveInteraction(w, "评论区欢迎配置已更新", func(doc map[string]any) error {
		if ch != "" {
			set := settingsMap(doc, "channel_comment_welcome")
			setOptionalBool(settingsMap(set, equivalentlyKeyed(set, ch)), "enabled", body.Enabled)
			return nil
		}
		item := settingsMap(settingsMap(doc, "comment_welcome"), "default")
		setOptionalBool(item, "enabled", body.Enabled)
		for key, value := range map[string]*string{
			"welcome_message": body.Welcome, "button_text": body.ButtonText, "button_action": body.ButtonAction,
		} {
			if value != nil {
				item[key] = *value
			}
		}
		return nil
	})
}

func (s *Server) saveInteraction(w http.ResponseWriter, message string, mutate func(map[string]any) error) {
	err := s.deps.Config.UpdateConfig(mutate)
	switch {
	case errors.Is(err, errAutoPollDisabled):
		writeHTTPError(w, http.StatusBadRequest, err.Error())
	case err != nil:
		writeHTTPError(w, http.StatusInternalServerError, "保存互动配置失败")
	default:
		writeEnvelopeOK(w, nil, message)
	}
}

func equivalentlyKeyed(doc map[string]any, channel string) string {
	if key, ok := config.FindEquivalentChannelKey(doc, channel); ok {
		return key
	}
	return channel
}

func setOptionalBool(doc map[string]any, key string, value *bool) {
	if value != nil {
		doc[key] = *value
	}
}
