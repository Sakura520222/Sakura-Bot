// Copyright 2026 Sakura-Bot
// SPDX-License-Identifier: AGPL-3.0-or-later

package webapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime"
	"net"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Sakura520222/Sakura-Bot/internal/wiki"
)

var wikiChannelIDPattern = regexp.MustCompile(`^[1-9][0-9]{0,18}$`)

var wikiAssistantHTTP = &http.Client{
	Transport: &http.Transport{
		DialContext:           (&net.Dialer{Timeout: 4 * time.Second}).DialContext,
		ResponseHeaderTimeout: 20 * time.Second,
		IdleConnTimeout:       60 * time.Second,
		MaxIdleConnsPerHost:   4,
	},
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

type wikiQuestion struct {
	ID       string  `json:"id"`
	Question string  `json:"question"`
	Parent   *string `json:"parent,omitempty"`
	Article  *struct {
		Channel  string `json:"channel"`
		Page     string `json:"page"`
		Revision string `json:"revision"`
		Selected string `json:"selected"`
	} `json:"article,omitempty"`
}

func (q *wikiQuestion) validate() bool {
	q.Question = strings.TrimSpace(q.Question)
	if !wikiIDPattern.MatchString(q.ID) || q.Question == "" || utf8.RuneCountInString(q.Question) > 4000 ||
		(q.Parent != nil && !wikiIDPattern.MatchString(*q.Parent)) {
		return false
	}
	a := q.Article
	return a == nil || (wikiChannelIDPattern.MatchString(a.Channel) && wikiIDPattern.MatchString(a.Page) &&
		wikiIDPattern.MatchString(a.Revision) && utf8.RuneCountInString(a.Selected) <= 500)
}

func (s *Server) registerWikiAssistantRoutes(mux *http.ServeMux) {
	prefix := "/api/wiki/assistant"
	mux.HandleFunc("GET "+prefix+"/status", s.wikiAssistant)
	mux.HandleFunc("POST "+prefix+"/checkin", s.wikiAssistant)
	mux.HandleFunc("POST "+prefix+"/chat", s.wikiAssistant)
	mux.HandleFunc("GET "+prefix+"/chat/{turn}", s.wikiAssistant)
	mux.HandleFunc("DELETE "+prefix+"/chat/{turn}", s.wikiAssistant)
}

func (s *Server) wikiAssistant(w http.ResponseWriter, r *http.Request) {
	turn := r.PathValue("turn")
	if turn != "" && !wikiIDPattern.MatchString(turn) {
		wikiNotFound(w)
		return
	}
	if r.Method != http.MethodGet && s.wikiSameOrigin(w, r) == nil {
		return
	}
	user := s.wikiRequireUser(w, r)
	if user == nil {
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/wiki/assistant")
	settings := wiki.LoginSettingsFrom(s.deps.Config)
	token := wiki.AssistantBearer(s.deps.Config.QABotToken())
	if path == "/checkin" && !settings.AssistantCheckin {
		writeHTTPError(w, http.StatusForbidden, "Wiki 签到未启用")
		return
	}
	if !settings.AssistantEnabled || token == "" {
		writeHTTPError(w, http.StatusNotFound, "Wiki 助手未启用")
		return
	}
	session := wikiCookie(r, wiki.CookieLogout)
	if _, ok := wiki.TokenHashBytes(session); !ok {
		writeHTTPError(w, http.StatusUnauthorized, "请重新登录 Wiki")
		return
	}
	var body io.Reader
	if r.Method == http.MethodPost && path == "/chat" {
		var question wikiQuestion
		if readJSONStrict(r, &question, 1<<16) != nil || !question.validate() {
			writeHTTPError(w, http.StatusUnprocessableEntity, "无效的 Wiki 问题")
			return
		}
		encoded, _ := json.Marshal(question)
		body = bytes.NewReader(encoded)
	}
	stream := strings.HasPrefix(path, "/chat") && r.Method != http.MethodDelete
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	timeout := 20 * time.Second
	if stream {
		timeout = 45 * time.Second
	}
	timer := time.AfterFunc(timeout, cancel)
	defer timer.Stop()
	upstreamURL := "http://127.0.0.1:" + strconv.Itoa(s.deps.Config.WikiAssistantPort()) + path
	req, err := http.NewRequestWithContext(ctx, r.Method, upstreamURL, body)
	if err != nil {
		writeHTTPError(w, http.StatusServiceUnavailable, "Go 助手暂不可用，请稍后重试")
		return
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Wiki-User", strconv.FormatInt(user.TelegramID, 10))
	req.Header.Set("X-Wiki-Session", session)
	response, err := wikiAssistantHTTP.Do(req)
	if err != nil {
		writeHTTPError(w, http.StatusServiceUnavailable, "Go 助手暂不可用，请稍后重试")
		return
	}
	defer response.Body.Close()
	mediaType, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if stream && response.StatusCode == http.StatusOK && mediaType == "text/event-stream" {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		if flusher != nil {
			flusher.Flush()
		}
		buf := make([]byte, 8<<10)
		for {
			timer.Reset(timeout)
			n, readErr := response.Body.Read(buf)
			if n > 0 {
				if _, err := w.Write(buf[:n]); err != nil {
					return
				}
				if flusher != nil {
					flusher.Flush()
				}
			}
			if readErr != nil {
				return
			}
		}
	}
	if response.StatusCode == http.StatusNoContent {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if mediaType != "application/json" || !slices.Contains([]int{200, 400, 401, 403, 404, 409, 410, 503}, response.StatusCode) {
		writeHTTPError(w, http.StatusServiceUnavailable, "Go 助手响应暂不可用")
		return
	}
	const maxJSON = 1 << 20
	data, err := io.ReadAll(io.LimitReader(response.Body, maxJSON+1))
	var result map[string]any
	if err != nil || len(data) > maxJSON || json.Unmarshal(data, &result) != nil || result == nil {
		writeHTTPError(w, http.StatusServiceUnavailable, "Go 助手响应暂不可用")
		return
	}
	if path == "/status" && response.StatusCode == http.StatusOK && !settings.AssistantCheckin {
		result["checkin_enabled"] = false
	}
	writeJSON(w, response.StatusCode, result)
}
