// Copyright 2026 Sakura-Bot
// SPDX-License-Identifier: AGPL-3.0-or-later

package webapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Sakura520222/Sakura-Bot/internal/mtproto"
	"github.com/Sakura520222/Sakura-Bot/internal/summary"
	"github.com/gotd/td/tg"
)

func TestWebUISummarySurvivesRequestCancellation(t *testing.T) {
	server := New(Deps{
		Config: webTestConfig(t), Logger: webTestLogger(),
		BotClient: mtproto.NewClientFromTG(tg.NewClient(nil)), SummaryService: &summary.Service{},
	})
	release := make(chan struct{})
	channelSeen := make(chan string, 1)
	server.summaryJobs = newSummaryJobs(t.Context(), server.logger, func(ctx context.Context, channel string, _ bool, _ string) summary.GenerateResult {
		channelSeen <- channel
		select {
		case <-release:
			if ctx.Err() == nil {
				return summary.GenerateResult{Error: "模型未返回完整总结"}
			}
		case <-ctx.Done():
		}
		return summary.GenerateResult{Error: ctx.Err().Error()}
	})
	t.Cleanup(server.StopSummaryJobs)
	admin := webTestAdmin(t, server)
	req := httptest.NewRequest(http.MethodPost, "/api/summaries/https%3A%2F%2Ft.me%2FMyChannel/summarize?async=1", nil)
	req.Header.Set("Authorization", "Bearer "+admin)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	response := httptest.NewRecorder()
	server.ServeHTTP(response, req.WithContext(ctx))
	cancel()

	decodeJob := func(w *httptest.ResponseRecorder) summaryJob {
		t.Helper()
		var body struct {
			Success bool       `json:"success"`
			Data    summaryJob `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || !body.Success || body.Data.ID == "" {
			t.Fatalf("malformed job response: %s, %v", w.Body, err)
		}
		return body.Data
	}
	if response.Code != http.StatusAccepted {
		t.Fatalf("POST returned %d: %s", response.Code, response.Body)
	}
	job := decodeJob(response)
	if job.Status != "running" || <-channelSeen != "https://t.me/MyChannel" {
		t.Fatalf("job did not start with the expected channel: %+v", job)
	}
	duplicate := decodeJob(webTestResponse(t, server, http.MethodPost, "/api/summaries/@mychannel/summarize?async=1", "", admin, http.StatusAccepted))
	if duplicate.ID != job.ID {
		t.Fatalf("duplicate request started a second job: %+v", duplicate)
	}
	statusURL := "/api/summaries/jobs/" + job.ID
	webTestResponse(t, server, http.MethodGet, statusURL, "", "", http.StatusUnauthorized)
	close(release)
	server.summaryJobs.wg.Wait()
	result := decodeJob(webTestResponse(t, server, http.MethodGet, statusURL, "", admin, http.StatusOK))
	if result.Status != "failed" || result.Result == nil || result.Result.Message != "模型未返回完整总结" {
		t.Fatalf("unexpected completed job: %+v, result %+v", result, result.Result)
	}
}
