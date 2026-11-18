// Copyright 2026 Sakura-Bot
// SPDX-License-Identifier: AGPL-3.0-or-later

package webapi

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/Sakura520222/Sakura-Bot/internal/config"
)

func (s *Server) registerForwardingRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/forwarding", s.withConfig(s.forwardingConfig))
	mux.HandleFunc("PUT /api/forwarding/enabled", s.withConfig(s.forwardingEnabled))
	mux.HandleFunc("PUT /api/forwarding/footer", s.withConfig(s.forwardingFooter))
	mux.HandleFunc("POST /api/forwarding/rules", s.withConfig(s.mutateForwardingRule))
	mux.HandleFunc("PUT /api/forwarding/rules/{rule_index}", s.withConfig(s.mutateForwardingRule))
	mux.HandleFunc("DELETE /api/forwarding/rules/{rule_index}", s.withConfig(s.mutateForwardingRule))
}

func (s *Server) withConfig(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.deps.Config == nil {
			writeHTTPError(w, http.StatusServiceUnavailable, "配置服务未就绪")
			return
		}
		next(w, r)
	}
}

func configObject(doc map[string]any, key string) map[string]any {
	nested, ok := doc[key].(map[string]any)
	if !ok || nested == nil {
		nested = make(map[string]any)
		doc[key] = nested
	}
	return nested
}

func (s *Server) configWriteError(w http.ResponseWriter, err error) {
	if errors.Is(err, config.ErrConfigConflict) {
		writeHTTPError(w, http.StatusConflict, err.Error())
		return
	}
	s.logger.Error("保存配置失败", "error", err)
	writeHTTPError(w, http.StatusInternalServerError, "保存配置失败")
}

func (s *Server) forwardingConfig(w http.ResponseWriter, _ *http.Request) {
	cfg := s.deps.Config.ForwardingConfig()
	writeEnvelopeOK(w, struct {
		config.ForwardingConfig
		RuleCount int `json:"rule_count"`
	}{cfg, len(cfg.Rules)}, "")
}

func (s *Server) forwardingEnabled(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	if readJSONBody(r, &body, 1<<16) != nil || body.Enabled == nil {
		writeHTTPError(w, http.StatusUnprocessableEntity, "无效的转发开关")
		return
	}
	message := "转发功能已禁用"
	if *body.Enabled {
		message = "转发功能已启用"
	}
	s.setForwardingFlag(w, "enabled", *body.Enabled, message)
}

func (s *Server) forwardingFooter(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Show *bool `json:"show_default_footer"`
	}
	if readJSONBody(r, &body, 1<<16) != nil {
		writeHTTPError(w, http.StatusUnprocessableEntity, "无效的页脚设置")
		return
	}
	if body.Show == nil {
		writeEnvelopeOK(w, nil, "页脚配置已更新")
		return
	}
	s.setForwardingFlag(w, "show_default_footer", *body.Show, "页脚配置已更新")
}

func (s *Server) setForwardingFlag(w http.ResponseWriter, key string, value bool, message string) {
	err := s.deps.Config.UpdateConfig(func(doc map[string]any) error {
		configObject(doc, "forwarding")[key] = value
		return nil
	})
	if err != nil {
		s.configWriteError(w, err)
		return
	}
	writeEnvelopeOK(w, nil, message)
}

var errForwardingRule = errors.New("invalid forwarding rule operation")

func (s *Server) mutateForwardingRule(w http.ResponseWriter, r *http.Request) {
	var index int
	if r.Method != http.MethodPost {
		var err error
		index, err = strconv.Atoi(r.PathValue("rule_index"))
		if err != nil {
			writeHTTPError(w, http.StatusUnprocessableEntity, "无效的规则索引")
			return
		}
	}
	var rule config.ForwardingRule
	if r.Method != http.MethodDelete {
		if readJSONBody(r, &rule, 1<<20) != nil {
			writeHTTPError(w, http.StatusUnprocessableEntity, "无效的转发规则")
			return
		}
		rule.SourceChannel = config.NormalizeChannelID(rule.SourceChannel)
		rule.TargetChannel = config.NormalizeChannelID(rule.TargetChannel)
		if rule.SourceChannel == "" || rule.TargetChannel == "" {
			writeEnvelopeFail(w, "无效的源频道或目标频道 URL", nil)
			return
		}
	}
	var message string
	err := s.deps.Config.UpdateConfig(func(doc map[string]any) error {
		fwd := configObject(doc, "forwarding")
		rules, _ := fwd["rules"].([]any)
		if _, exists := fwd["rules"]; !exists {
			rules, _ = doc["forwarding_rules"].([]any)
		}
		if r.Method != http.MethodPost && (index < 0 || index >= len(rules)) {
			message = fmt.Sprintf("无效的规则索引: %d", index)
			return errForwardingRule
		}
		switch r.Method {
		case http.MethodPost:
			for _, raw := range rules {
				old, _ := raw.(map[string]any)
				if old["source_channel"] == rule.SourceChannel && old["target_channel"] == rule.TargetChannel {
					message = "该转发规则已存在"
					return errForwardingRule
				}
			}
			rules = append(rules, rule)
			message = fmt.Sprintf("转发规则已添加: %s -> %s", rule.SourceChannel, rule.TargetChannel)
		case http.MethodPut:
			rules[index] = rule
			message = fmt.Sprintf("转发规则已更新: #%d", index)
		case http.MethodDelete:
			old, _ := rules[index].(map[string]any)
			message = fmt.Sprintf("转发规则已删除: %v -> %v", old["source_channel"], old["target_channel"])
			rules = append(rules[:index], rules[index+1:]...)
		}
		fwd["rules"] = rules
		return nil
	})
	if errors.Is(err, errForwardingRule) {
		writeEnvelopeFail(w, message, nil)
		return
	}
	if err != nil {
		s.configWriteError(w, err)
		return
	}
	writeEnvelopeOK(w, nil, message)
}
