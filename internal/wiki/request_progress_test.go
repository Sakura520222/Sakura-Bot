// Copyright 2026 Sakura-Bot
// SPDX-License-Identifier: AGPL-3.0-or-later

package wiki

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Sakura520222/Sakura-Bot/internal/llm"
)

func TestWikiRequestPersistsSSEProgressBeforeCompletion(t *testing.T) {
	store := isolatedWikiStore(t)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	if err := store.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	ch, err := store.RegisterChannel(ctx, ChannelEntity{ID: 900003, Title: "Progress test", Username: "wiki_progress_test", Broadcast: true}, "")
	if err != nil {
		t.Fatal(err)
	}
	cfg := DefaultWikiConfig()
	cfg.Enabled = true
	if err := store.ConfigureChannel(ctx, ch.ID, cfg, "integration"); err != nil {
		t.Fatal(err)
	}
	id, err := store.EnqueueJob(ctx, nil, ch.ID, "update", "integration", "", false)
	if err != nil {
		t.Fatal(err)
	}
	job, err := store.ClaimJob(ctx)
	if err != nil || job == nil || job.ID != id {
		t.Fatalf("did not claim fixture job: %v (%v)", job, err)
	}

	content, finish := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flush := w.(http.Flusher)
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"思💡\"}}]}\n\n")
		flush.Flush()
		select {
		case <-content:
		case <-r.Context().Done():
			return
		}
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"答案\"}}]}\n\n")
		flush.Flush()
		select {
		case <-finish:
		case <-r.Context().Done():
			return
		}
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)
	type outcome struct {
		content string
		err     error
	}
	result := make(chan outcome, 1)
	engine := NewEngine(store, nil, nil)
	go func() {
		text, err := engine.Request(ctx, job, cfg, LLMRoute{
			Model:  "fixture-model",
			Caller: &configLLMCaller{aiCfg: llm.AIConfig{BaseURL: server.URL, Model: "fixture-model"}},
		}, "测试", "input", "整理 Telegram 图文", 1)
		result <- outcome{content: text, err: err}
	}()

	readProgress := func() map[string]any {
		t.Helper()
		var raw []byte
		if err := store.DB.QueryRowContext(ctx, "SELECT progress FROM wiki_jobs WHERE id=?", id).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "思💡") {
			t.Fatal("progress must not retain raw reasoning")
		}
		return decodeMapJSON(raw)
	}
	waitProgress := func(received, output int, state string) {
		t.Helper()
		for {
			p := readProgress()
			active, _ := p["active_requests"].(map[string]any)
			for _, raw := range active {
				meta, _ := raw.(map[string]any)
				if mapInt(meta, "received_chars") == received && mapInt(meta, "output_chars") == output && mapString(meta, "state") == state {
					return
				}
			}
			select {
			case completed := <-result:
				t.Fatalf("request ended before persisting %q: %+v", state, completed)
			case <-ctx.Done():
				t.Fatalf("timed out waiting for %q progress", state)
			case <-time.After(20 * time.Millisecond):
			}
		}
	}
	waitProgress(2, 0, "模型处理中")
	// Verify the next stage is also persisted after the shared write interval.
	select {
	case <-time.After(modelProgressInterval):
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	close(content)
	waitProgress(4, 2, "接收正文")
	close(finish)
	select {
	case completed := <-result:
		if completed.err != nil || completed.content != "答案" {
			t.Fatalf("unexpected completed result: %+v", completed)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	recent, _ := readProgress()["recent_requests"].([]any)
	if len(recent) != 1 {
		t.Fatalf("completed request missing: %+v", recent)
	}
	meta, _ := recent[0].(map[string]any)
	if mapInt(meta, "received_chars") != 4 || mapInt(meta, "output_chars") != 2 || mapString(meta, "state") != "完成" {
		t.Fatalf("completed request lost progress: %+v", meta)
	}
}
