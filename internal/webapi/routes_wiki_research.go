// Copyright 2026 Sakura-Bot
// SPDX-License-Identifier: AGPL-3.0-or-later

package webapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Sakura520222/Sakura-Bot/internal/wiki"
)

var researchMessages = map[string]string{
	"search_disabled_or_empty":      "联网检索未启用或关键词为空。",
	"no_search_provider_configured": "没有启用可用的搜索源。",
	"search_providers_unavailable":  "已启用的搜索源本次均未能响应。",
	"no_general_web_provider":       "未启用通用网页搜索源，现有专题索引本次未找到可用资料。",
	"no_search_evidence":            "已启用的搜索源本次没有返回可用资料，无法据此判断网上是否有新消息。",
	"search_unavailable":            "联网检索暂时不可用。",
	"fetch_disabled":                "公开网页读取未启用。",
	"fetch_unsupported_type":        "链接指向暂不支持读取的文件类型。",
	"fetch_no_text":                 "网页已打开，但没有可提取的正文。",
	"fetch_unavailable":             "本次无法打开网页。",
}

func (s *Server) registerWikiResearchRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/internal/wiki-research", s.wikiResearch)
	mux.HandleFunc("GET /api/wiki/admin/research", s.wikiAdmin(s.withConfig(s.wikiResearchSettings)))
	mux.HandleFunc("PUT /api/wiki/admin/research", s.wikiAdmin(s.withConfig(s.wikiSaveResearchSettings)))
}

func (s *Server) wikiResearch(w http.ResponseWriter, r *http.Request) {
	if s.deps.Config == nil || !wiki.VerifyInternalBearer(r.Header.Get("Authorization"), s.deps.Config.QABotToken()) {
		writeHTTPError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	operation := r.URL.Query().Get("operation")
	if operation != "search" && operation != "fetch" {
		writeHTTPError(w, http.StatusNotFound, "Not Found")
		return
	}
	var body struct {
		Value string `json:"value"`
	}
	if readJSONStrict(r, &body, 1<<14) != nil || strings.TrimSpace(body.Value) == "" ||
		utf8.RuneCountInString(body.Value) > 2048 {
		writeHTTPError(w, http.StatusUnprocessableEntity, "无效的检索请求")
		return
	}
	service := s.wikiRuntime(w, false)
	if service == nil {
		return
	}
	var (
		result map[string]any
		err    error
	)
	if operation == "search" {
		result, err = service.Research.Search(r.Context(), body.Value, nil, true)
	} else {
		result, err = service.Research.Fetch(r.Context(), body.Value)
	}
	if err == nil {
		writeJSON(w, http.StatusOK, result)
		return
	}
	var unavailable *wiki.SearchUnavailableError
	if !errors.As(err, &unavailable) {
		writeJSON(w, http.StatusOK, map[string]string{"error": "invalid_public_url_or_configuration"})
		return
	}
	code := unavailable.Error()
	message := researchMessages[code]
	if status, err := strconv.Atoi(strings.TrimPrefix(code, "fetch_http_")); operation == "fetch" &&
		strings.HasPrefix(code, "fetch_http_") && err == nil && status >= 100 && status <= 599 {
		message = fmt.Sprintf("目标网站返回 HTTP %d。", status)
	}
	if message == "" {
		code = operation + "_unavailable"
		message = researchMessages[code]
	}
	writeJSON(w, http.StatusOK, map[string]string{"error": code, "message": message})
}

func (s *Server) wikiResearchSettings(w http.ResponseWriter, _ *http.Request) {
	raw, _ := s.deps.Config.SubSection("wiki_search")
	cfg, err := wiki.ParseSearchSettings(raw)
	if err != nil {
		cfg = wiki.DefaultSearchSettings()
	}
	writeJSON(w, http.StatusOK, wiki.MaskedSearchSettings(cfg))
}

func (s *Server) wikiSaveResearchSettings(w http.ResponseWriter, r *http.Request) {
	type providerInput struct {
		Enabled bool    `json:"enabled"`
		APIKey  *string `json:"api_key"`
	}
	defaults := wiki.DefaultSearchSettings()
	body := struct {
		Enabled     bool                     `json:"enabled"`
		Order       []string                 `json:"order"`
		Providers   map[string]providerInput `json:"providers"`
		ResultLimit int                      `json:"result_limit"`
		Timeout     int                      `json:"timeout"`
		Concurrency int                      `json:"concurrency"`
		TTLHours    int                      `json:"ttl_hours"`
	}{Order: defaults.Order, ResultLimit: defaults.ResultLimit, Timeout: defaults.Timeout, Concurrency: defaults.Concurrency, TTLHours: defaults.TTLHours}
	if readJSONStrict(r, &body, 1<<16) != nil || len(body.Order) == 0 {
		writeHTTPError(w, http.StatusUnprocessableEntity, "搜索设置无效")
		return
	}
	cfg := wiki.SearchSettings{
		Enabled: body.Enabled, Order: body.Order, Providers: map[string]wiki.ProviderSetting{},
		ResultLimit: body.ResultLimit, Timeout: body.Timeout, Concurrency: body.Concurrency, TTLHours: body.TTLHours,
	}
	errInvalid := errors.New("invalid research settings")
	err := s.deps.Config.UpdateConfig(func(doc map[string]any) error {
		raw, _ := json.Marshal(doc["wiki_search"])
		previous, _ := wiki.ParseSearchSettings(raw)
		cfg.DailyBudget = previous.DailyBudget
		for name, input := range body.Providers {
			key := previous.Providers[name].APIKey
			if input.APIKey != nil {
				key = *input.APIKey
			}
			cfg.Providers[name] = wiki.ProviderSetting{Enabled: input.Enabled, APIKey: key}
		}
		if err := cfg.Validate(); err != nil {
			return fmt.Errorf("%w: %v", errInvalid, err)
		}
		doc["wiki_search"] = cfg
		return nil
	})
	if errors.Is(err, errInvalid) {
		writeHTTPError(w, http.StatusUnprocessableEntity, "搜索设置无效")
		return
	}
	if err != nil {
		s.configWriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, wiki.MaskedSearchSettings(cfg))
}
