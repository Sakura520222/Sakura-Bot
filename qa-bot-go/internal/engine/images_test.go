package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/contracts"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/llm"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/media"
)

func TestImagesSurviveToolLoopFallbackAndForcedFinal(t *testing.T) {
	for _, mode := range []string{"tools", "fallback", "forced"} {
		t.Run(mode, func(t *testing.T) {
			var mu sync.Mutex
			var bodies []struct {
				Model    string
				Messages []llm.Message
				Tools    json.RawMessage
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					Model    string
					Messages []llm.Message
					Tools    json.RawMessage
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error("invalid multimodal JSON")
				}
				mu.Lock()
				bodies = append(bodies, body)
				count := len(bodies)
				mu.Unlock()
				if mode == "fallback" && count == 1 {
					w.WriteHeader(400)
					_, _ = io.WriteString(w, "PRIVATE_IMAGE api-secret")
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				if (mode == "tools" && count == 1) || (mode == "forced" && count <= AgentMaxIterations) {
					_, _ = io.WriteString(w, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call","type":"function","function":{"name":"list_channels","arguments":"{}"}}]}}]}`+"\n\n")
				} else {
					_, _ = io.WriteString(w, contentLine("红色物体")+"\n\n")
				}
				_, _ = io.WriteString(w, "data: [DONE]\n\n")
			}))
			defer server.Close()
			store := &fakeStore{}
			var configCalls int
			chat := llm.NewClient(llm.AIConfigFunc(func() llm.AIConfig {
				configCalls++
				return llm.AIConfig{APIKey: "api-secret", BaseURL: server.URL, Model: fmt.Sprintf("model-%d", configCalls), SupportsVision: true}
			}))
			e := New(Deps{Chat: chat, Tools: &fakeTools{schemas: json.RawMessage(`[]`), exec: &fakeExecutor{}}, Conversations: NewConversations(store), Persona: constPersona("persona"), Memory: constMemory(""), Logger: discardLogger()})
			images := media.NewStore(5 * time.Minute)
			scope := media.Scope{UserID: 1, ChatID: 10}
			_, _, _ = images.Add(scope, media.Ref{FileID: "one", MessageID: 1})
			_, _, _ = images.Add(scope, media.Ref{FileID: "two", MessageID: 2})
			ctx := media.WithRequest(context.Background(), &media.Request{Store: images, Scope: scope, Download: func(_ context.Context, r media.Ref) (string, error) {
				return "data:image/png;base64,PRIVATE_IMAGE_" + r.FileID, nil
			}})
			chunks := collectChunks(t, e.ProcessQueryStream(ctx, "这是什么", 1, ""))
			if chunks[len(chunks)-1] != CtrlChunk(contracts.CtrlDone) {
				t.Fatal("image request did not complete")
			}
			if configCalls != 1 {
				t.Fatal("configuration changed within a request")
			}
			want := 2
			if mode == "forced" {
				want = AgentMaxIterations + 1
			}
			mu.Lock()
			defer mu.Unlock()
			if len(bodies) != want {
				t.Fatalf("requests=%d want=%d", len(bodies), want)
			}
			for _, body := range bodies {
				if body.Model != "model-1" {
					t.Fatal("model changed during request")
				}
				found := 0
				for _, message := range body.Messages {
					if len(message.Parts) > 0 {
						found++
						if len(message.Parts) != 3 || message.Parts[0].Text != "这是什么" || message.Parts[2].ImageURL.URL != "data:image/png;base64,PRIVATE_IMAGE_two" {
							t.Fatal("image content changed")
						}
					}
				}
				if found != 1 {
					t.Fatal("multimodal user message lost or duplicated")
				}
			}
			store.mu.Lock()
			defer store.mu.Unlock()
			for _, message := range store.saved {
				if strings.Contains(message.Content, "PRIVATE_IMAGE") || strings.Contains(message.Content, "base64") {
					t.Fatal("image written to history")
				}
			}
		})
	}
}

func TestImageFailureDoesNotSendTextOnly(t *testing.T) {
	h := newHarness(t, nil)
	images := media.NewStore(5 * time.Minute)
	scope := media.Scope{UserID: 1, ChatID: 1}
	_, _, _ = images.Add(scope, media.Ref{FileID: "file", MessageID: 1})
	ctx := media.WithRequest(context.Background(), &media.Request{Store: images, Scope: scope, Download: func(context.Context, media.Ref) (string, error) {
		t.Fatal("download while capability disabled")
		return "", nil
	}})
	chunks := collectChunks(t, h.engine.ProcessQueryStream(ctx, "这是什么", 1, ""))
	if len(chunks) != 1 || chunks[0].Kind != ChunkErr || !strings.Contains(chunks[0].Err, "未开启图片输入") {
		t.Fatal("missing capability error")
	}
	if h.llm.requestCount() != 0 {
		t.Fatal("sent a text-only fallback")
	}
}
