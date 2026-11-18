// Copyright 2026 Sakura-Bot
// SPDX-License-Identifier: AGPL-3.0-or-later

package webapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Sakura520222/Sakura-Bot/internal/process"
)

type systemQAProbe struct {
	stopped bool
}

func (*systemQAProbe) Start() process.QAActionResult {
	pid := 12345
	return process.QAActionResult{Success: true, Message: "started", PID: &pid}
}

func (q *systemQAProbe) Stop(timeout time.Duration) process.QAActionResult {
	q.stopped = timeout == 5*time.Second
	return process.QAActionResult{Success: q.stopped, Message: "stopped"}
}

func (q *systemQAProbe) Restart() process.QAActionResult {
	return q.Start()
}

func (*systemQAProbe) Status() process.QAStatus {
	return process.QAStatus{Runtime: "go", Running: true}
}

func (*systemQAProbe) SetRuntime(runtime string) map[string]any {
	return map[string]any{"success": runtime == "go", "runtime": "go", "message": "selected", "restart": nil}
}

func (*systemQAProbe) CheckHealth() (bool, bool, string) {
	return false, true, "process not running"
}

func TestSystemRoutesProcessContracts(t *testing.T) {
	manager := &systemQAProbe{}
	restartReason := ""
	s := &Server{logger: slog.Default(), deps: Deps{
		QAManager:      manager,
		TriggerRestart: func(reason string) { restartReason = reason },
	}}
	mux := http.NewServeMux()
	s.registerSystemRoutes(mux)

	do := func(path, body string) map[string]any {
		t.Helper()
		resp := httptest.NewRecorder()
		mux.ServeHTTP(resp, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
		if resp.Code != http.StatusOK {
			t.Fatalf("%s returned %d: %s", path, resp.Code, resp.Body.String())
		}
		var parsed map[string]any
		if err := json.Unmarshal(resp.Body.Bytes(), &parsed); err != nil {
			t.Fatal(err)
		}
		return parsed
	}
	start := do("/api/system/qa-bot/start", "")
	if !reflect.DeepEqual(start, map[string]any{
		"success": true, "message": "started", "data": map[string]any{"pid": 12345.0},
	}) {
		t.Fatalf("start response = %v", start)
	}
	stop := do("/api/system/qa-bot/stop", "")
	if !manager.stopped || !reflect.DeepEqual(stop, map[string]any{"success": true, "message": "stopped"}) {
		t.Fatalf("stop response = %v", stop)
	}
	health := do("/api/system/qa-bot/health", "")
	if !reflect.DeepEqual(health, map[string]any{
		"success": true, "data": map[string]any{"healthy": false, "should_restart": true, "message": "process not running"},
	}) {
		t.Fatalf("health response = %v", health)
	}
	runtime := do("/api/system/qa-bot/runtime", `{"runtime":"go"}`)
	if runtime["success"] != true || runtime["data"].(map[string]any)["runtime"] != "go" {
		t.Fatalf("runtime response = %v", runtime)
	}
	do("/api/system/restart", "")
	if restartReason != "webui_restart" {
		t.Fatalf("restart callback received %q", restartReason)
	}
}

func TestSystemAndStatsRoutesValidationAndEmptyResponse(t *testing.T) {
	s := &Server{logger: slog.Default()}
	mux := http.NewServeMux()
	s.registerSystemRoutes(mux)
	s.registerStatsRoutes(mux)

	cases := []struct {
		method, path, body string
		status             int
		contains           string
	}{
		{"GET", "/api/system/audit-logs", "", 200, `"items":[]`},
		{"GET", "/api/system/logs/recent?lines=1001", "", 422, `"detail"`},
		{"GET", "/api/system/database/status", "", 200, `"available":false`},
		{"POST", "/api/system/database/cleanup/poll-regenerations", `{"days":0}`, 422, `"detail"`},
		{"POST", "/api/system/database/cleanup/poll-regenerations", `{"days":2}`, 503, `"detail"`},
		{"GET", "/api/stats", "", 200, `"success":false`},
		{"GET", "/api/stats/summaries?offset=-1", "", 422, `"detail"`},
		{"GET", "/api/stats/summaries?limit=99", "", 200, `"success":false`},
		{"GET", "/api/stats/ranking", "", 200, `"success":false`},
		{"GET", "/api/stats/export", "", 200, `"success":false`},
	}
	for _, tc := range cases {
		t.Run(tc.path+" "+tc.body, func(t *testing.T) {
			resp := httptest.NewRecorder()
			mux.ServeHTTP(resp, httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body)))
			if resp.Code != tc.status || !strings.Contains(resp.Body.String(), tc.contains) {
				t.Fatalf("response = %d %s", resp.Code, resp.Body.String())
			}
		})
	}
}

func TestSystemAndStatsRoutesRequireAdminAuthentication(t *testing.T) {
	manager := &systemQAProbe{}
	server := New(Deps{QAManager: manager})
	for _, route := range []struct{ method, path string }{
		{http.MethodPost, "/api/system/qa-bot/stop"},
		{http.MethodGet, "/api/stats/export"},
	} {
		response := httptest.NewRecorder()
		server.ServeHTTP(response, httptest.NewRequest(route.method, route.path, nil))
		if response.Code != http.StatusUnauthorized {
			t.Errorf("%s returned %d; want %d", route.path, response.Code, http.StatusUnauthorized)
		}
	}
	if manager.stopped {
		t.Fatal("an unauthenticated request stopped the assistant")
	}
}

func TestRecentLogLinesTailAndFilters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "system.log")
	testLog := "ERROR ignored outside tail\r\nDEBUG one\r\nERROR qa first\r\n" +
		"ERROR database forged\r\nERROR QA second\r\nERROR qa third\r\nINFO done"
	if err := os.WriteFile(path, []byte(testLog), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := recentLogLines(path, 2, "error", "qa")
	want := []string{"ERROR QA second", "ERROR qa third"}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("recent log lines = %q, %v; want %q", got, err, want)
	}
}
