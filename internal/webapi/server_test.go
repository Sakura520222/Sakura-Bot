// Copyright 2026 Sakura-Bot
// SPDX-License-Identifier: AGPL-3.0-or-later

package webapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Sakura520222/Sakura-Bot/internal/config"
	"github.com/Sakura520222/Sakura-Bot/internal/guard"
	"github.com/Sakura520222/Sakura-Bot/internal/wiki"
)

const mainTestToken = "1234:main-token"
const assistantTestToken = "5678:assistant-token"

func webTestConfig(t *testing.T) *config.Config {
	t.Helper()
	t.Setenv("TELEGRAM_BOT_TOKEN", mainTestToken)
	t.Setenv("QA_BOT_TOKEN", assistantTestToken)
	t.Setenv("WEBUI_DEV_MODE", "false")
	t.Setenv("REPORT_ADMIN_IDS", "42")
	t.Setenv("WIKI_LOGIN_ENABLED", "true")
	t.Setenv("WIKI_LOGIN_ORIGIN", "https://wiki.example.test")
	t.Setenv("WIKI_TELEGRAM_CLIENT_SECRET", "test-secret")
	dir := t.TempDir()
	return config.New(filepath.Join(dir, ".env"), filepath.Join(dir, "config.json"))
}

func webTestLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func webTestResponse(t *testing.T, server http.Handler, method, path, body, bearer string, status int) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if bearer != "" {
		r.Header.Set("Authorization", "Bearer "+bearer)
	}
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	server.ServeHTTP(w, r)
	if w.Code != status {
		t.Fatalf("%s %s status = %d, want %d, body = %s", method, path, w.Code, status, w.Body)
	}
	return w
}

func webTestAdmin(t *testing.T, server http.Handler) string {
	t.Helper()
	w := webTestResponse(t, server, "POST", "/api/auth/login", `{"token":"`+ComputeAdminToken(mainTestToken)+`"}`, "", 200)
	var payload struct {
		Success bool   `json:"success"`
		Access  string `json:"access_token"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil || !payload.Success || payload.Access == "" {
		t.Fatalf("unexpected admin login result %s, error = %v", w.Body.String(), err)
	}
	return payload.Access
}

func TestWebUIHealthStaticAndAuth(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	for path, contents := range map[string]string{"index.html": "<main>Sakura WebUI</main>", "assets/index.js": "console.log('Sakura')"} {
		if err := os.WriteFile(filepath.Join(dir, path), []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	server := New(Deps{Config: webTestConfig(t), StaticDir: dir, Logger: webTestLogger()})
	if w := webTestResponse(t, server, "GET", "/api/health", "", "", 200); !strings.Contains(w.Body.String(), "sakura-bot-webui") {
		t.Fatal(w.Body.String())
	}
	for _, path := range []string{"/", "/login", "/wiki/channel-alpha/page"} {
		if w := webTestResponse(t, server, "GET", path, "", "", 200); !strings.Contains(w.Body.String(), "Sakura WebUI") {
			t.Fatalf("route %s returned %s", path, w.Body)
		}
	}
	webTestResponse(t, server, "GET", "/assets/missing.js", "", "", 404)
	if w := webTestResponse(t, server, "GET", "/assets/index.js", "", "", 200); !strings.Contains(w.Body.String(), "console.log") {
		t.Fatal(w.Body.String())
	}
	webTestResponse(t, server, "GET", "/api/dashboard", "", "", 401)
	admin := webTestAdmin(t, server)
	webTestResponse(t, server, "GET", "/api/dashboard", "", admin, 200)
	webTestResponse(t, server, "GET", "/api/missing", "", admin, 404)
	webTestResponse(t, server, "GET", "/api/wiki/admin/status", "", admin, 200)
	forged, err := CreateAccessToken("admin", "", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	webTestResponse(t, New(Deps{Logger: webTestLogger()}), "GET", "/api/dashboard", "", forged, 401)
}

func TestWikiCredentialAndOriginBoundaries(t *testing.T) {
	cfg := webTestConfig(t)
	if wiki.LoginConfigFrom(cfg) == nil {
		t.Fatal("test Wiki login configuration did not load")
	}
	server := New(Deps{Config: cfg, Logger: webTestLogger()})
	webTestResponse(t, server, "GET", "/api/public/wiki/channels", "", "", 503)
	webTestResponse(t, server, "GET", "/api/wiki/account/bookmarks", "", "", 401)
	if w := webTestResponse(t, server, "GET", "https://wrong.example.test/api/wiki/account/session", "", "", 200); !strings.Contains(w.Body.String(), `"enabled":false`) {
		t.Fatal(w.Body.String())
	}
	accountURL := "https://wiki.example.test/api/wiki/account/start"
	req := httptest.NewRequest("POST", accountURL, strings.NewReader("{}"))
	req.Header.Set("Origin", "https://wrong.example.test")
	req.Header.Set("X-Wiki-Request", "1")
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden || w.Header().Get("Access-Control-Allow-Origin") != "" ||
		w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("cross-origin account request = %d / %v", w.Code, w.Header())
	}
	req = httptest.NewRequest("POST", accountURL, strings.NewReader("{}"))
	req.Header.Set("Origin", "https://wiki.example.test")
	req.Header.Set("X-Wiki-Request", "1")
	w = httptest.NewRecorder()
	server.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("same origin request did not reach account service: %d", w.Code)
	}
	const internalPath = "/api/internal/wiki-research?operation=search"
	webTestResponse(t, server, "POST", internalPath, `{"value":"sakura"}`, webTestAdmin(t, server), 401)
	webTestResponse(t, server, "POST", internalPath, `{"value":"sakura"}`, wiki.InternalResearchToken(assistantTestToken), 503)

	cookieResponse := httptest.NewRecorder()
	setWikiSession(cookieResponse, &wiki.SessionTokens{Access: "access", Refresh: "refresh", Logout: "logout", RemainingSeconds: 1200})
	cookies := cookieResponse.Result().Cookies()
	if len(cookies) != 3 || cookies[0].MaxAge != 900 {
		t.Fatalf("unexpected session cookies: %v", cookies)
	}
	for _, c := range cookies {
		if !c.Secure || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Domain != "" || c.Path != "/" {
			t.Fatalf("unexpected Wiki cookie constraints: %v", c)
		}
	}
}

func TestPublicWikiConditionalResponse(t *testing.T) {
	s := &Server{
		logger:       webTestLogger(),
		wikiPublicRL: NewIPRateLimiter(120, time.Minute, 4096),
	}
	handler := s.wrapMiddlewares(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"items": []string{"wiki-channel"}})
	}))
	const path = "/api/public/wiki/feed"
	first := webTestResponse(t, handler, "GET", path, "", "", 200)
	tag := first.Header().Get("ETag")
	if !strings.HasPrefix(tag, `W/"`) || first.Header().Get("Cache-Control") != "private, no-cache, must-revalidate" {
		t.Fatalf("conditional caching headers = %v", first.Header())
	}
	for _, method := range []string{"GET", "HEAD"} {
		r := httptest.NewRequest(method, path, nil)
		r.Header.Set("If-None-Match", tag)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != http.StatusNotModified || w.Body.Len() != 0 || w.Header().Get("ETag") != tag {
			t.Fatalf("%s conditional response = %d / %v / %q", method, w.Code, w.Header(), w.Body.String())
		}
	}
}

func TestGuardOptimisticConfigAndForwarding(t *testing.T) {
	cfg := webTestConfig(t)
	logger := slog.New(slog.NewTextHandler(t.Output(), nil))
	service := guard.NewService(nil, cfg, nil, nil, logger)
	server := New(Deps{Config: cfg, Guard: service, Logger: logger})
	admin := webTestAdmin(t, server)
	webTestResponse(t, server, "GET", "/api/guard/config", "", "", 401)
	unprivileged, err := CreateAccessToken("43", mainTestToken, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	webTestResponse(t, server, "GET", "/api/guard/config", "", unprivileged, 403)
	webTestResponse(t, server, "PATCH", "/api/guard/retention", `{"retention_days":45,"previous":90}`, admin, 200)
	if service.Config().RetentionDays != 45 {
		t.Fatalf("runtime Guard config did not reload, days = %d", service.Config().RetentionDays)
	}
	w := webTestResponse(t, server, "PATCH", "/api/guard/retention", `{"retention_days":60,"previous":90}`, admin, 409)
	if !strings.Contains(w.Body.String(), `"retention_days":45`) {
		t.Fatalf("conflict response omitted current configuration: %s", w.Body)
	}
	webTestResponse(t, server, "PATCH", "/api/guard/retention", `{"retention_days":45,"previous":90}`, admin, 200)
	webTestResponse(t, server, "POST", "/api/guard/chats/leave", `{"token":"012345678901234567890123456789012","confirmed":false}`, admin, 422)

	const addRule = `{"source_channel":"@source","target_channel":"https://t.me/target","copy_mode":true}`
	webTestResponse(t, server, "PUT", "/api/forwarding/enabled", `{"enabled":true}`, admin, 200)
	webTestResponse(t, server, "POST", "/api/forwarding/rules", addRule, admin, 200)
	w = webTestResponse(t, server, "POST", "/api/forwarding/rules", addRule, admin, 200)
	if !strings.Contains(w.Body.String(), `"success":false`) {
		t.Fatalf("duplicate rule did not fail: %s", w.Body)
	}
	forwarding := cfg.ForwardingConfig()
	if !forwarding.Enabled || len(forwarding.Rules) != 1 || forwarding.Rules[0].SourceChannel != "https://t.me/source" || !forwarding.Rules[0].CopyMode {
		t.Fatalf("unexpected forwarding settings = %+v", forwarding)
	}
	webTestResponse(t, server, "DELETE", "/api/forwarding/rules/0", "", admin, 200)
	if len(cfg.ForwardingConfig().Rules) != 0 {
		t.Fatal("forwarding rule was not removed")
	}
}
