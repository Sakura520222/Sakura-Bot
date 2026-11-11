package tg

import (
	"context"
	"encoding/json"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/contracts"
	"github.com/go-telegram/bot/models"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

func TestRichStreamPrivateDraftAndGroupEdit(t *testing.T) {
	for _, kind := range []models.ChatType{models.ChatTypePrivate, models.ChatTypeSupergroup} {
		t.Run(string(kind), func(t *testing.T) {
			var mu sync.Mutex
			calls := []string{}
			draftSeen := make(chan struct{}, 1)
			c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				payload := map[string]any{}
				if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
					_ = r.ParseMultipartForm(1 << 20)
					for key, values := range r.Form {
						payload[key] = values[0]
					}
				} else {
					_ = json.NewDecoder(r.Body).Decode(&payload)
				}
				method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
				mu.Lock()
				calls = append(calls, method)
				mu.Unlock()
				if _, ok := payload["parse_mode"]; ok {
					t.Error("legacy parse_mode used")
				}
				if method == "sendRichMessageDraft" {
					if kind != models.ChatTypePrivate {
						t.Error("draft sent to group")
					}
					select {
					case draftSeen <- struct{}{}:
					default:
					}
					writeJSON(w, `{"ok":true,"result":true}`)
					return
				}
				if method == "sendChatAction" {
					writeJSON(w, `{"ok":true,"result":true}`)
					return
				}
				if _, ok := payload["rich_message"]; !ok {
					t.Error("missing rich content")
				}
				writeJSON(w, `{"ok":true,"result":{"message_id":42,"date":1,"chat":{"id":7,"type":"private"}}}`)
				if method == "sendRichMessage" {
					select {
					case draftSeen <- struct{}{}:
					default:
					}
				}
			})
			ch := make(chan StreamChunk)
			finished := make(chan bool)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			go func() {
				finished <- c.StreamRich(ctx, &models.Message{ID: 1, Chat: models.Chat{ID: 7, Type: kind}}, ch)
			}()
			ch <- StreamChunk{Text: "# 标题\n\n|a|b|\n|---|---|\n|1|2|"}
			select {
			case <-draftSeen:
			case <-ctx.Done():
				t.Fatal("no streaming preview")
			}
			ch <- StreamChunk{Text: "\n\n第二阶段"}
			ch <- StreamChunk{Text: contracts.CtrlDone}
			close(ch)
			if !<-finished {
				t.Fatal("stream failed")
			}
			mu.Lock()
			defer mu.Unlock()
			joined := strings.Join(calls, ",")
			if !strings.Contains(joined, "sendRichMessage") {
				t.Fatal(joined)
			}
			if kind != models.ChatTypePrivate && !strings.Contains(joined, "editMessageText") {
				t.Fatal("group answer not edited", joined)
			}
		})
	}
}
func TestSplitRichFencesAndUnicode(t *testing.T) {
	s := "```go\n" + strings.Repeat("你好🐱\n", 9000) + "```\n"
	parts := SplitRich(s)
	if len(parts) < 2 {
		t.Fatal("not split")
	}
	for _, p := range parts {
		if utf8.RuneCountInString(p) > 32768 {
			t.Fatal("oversized part")
		}
		if strings.Count(p, "```")%2 != 0 {
			t.Fatal("unbalanced code fence")
		}
	}
}
