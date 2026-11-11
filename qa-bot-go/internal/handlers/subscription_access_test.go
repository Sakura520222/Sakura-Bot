package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/config"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/tg"
	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestSubscriptionAccessAndImmediateDisable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	write := func(enabled bool) {
		b, _ := json.Marshal(map[string]any{"channels": []string{"https://t.me/test"}, "qa_assistant": map[string]any{"require_subscription": enabled, "required_channels": []string{"https://t.me/test"}}})
		if e := os.WriteFile(path, b, 0600); e != nil {
			t.Fatal(e)
		}
	}
	write(true)
	cfg := config.New(filepath.Join(dir, "missing.env"), path)
	if e := cfg.Init(); e != nil {
		t.Fatal(e)
	}
	var status atomic.Value
	status.Store("left")
	var fail atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if fail.Load() {
			w.WriteHeader(500)
			_, _ = w.Write([]byte(`{"ok":false,"error_code":500,"description":"failed"}`))
			return
		}
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		args := map[string]any{}
		if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
			_ = r.ParseMultipartForm(1 << 20)
			for key, values := range r.Form {
				args[key] = values[0]
			}
		} else if strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
			_ = r.ParseForm()
			for key, values := range r.Form {
				args[key] = values[0]
			}
		} else {
			_ = json.NewDecoder(r.Body).Decode(&args)
		}
		result := any(map[string]any{"message_id": 1, "date": 1, "chat": map[string]any{"id": 1, "type": "private"}})
		switch method {
		case "getMe":
			result = map[string]any{"id": 123, "is_bot": true, "first_name": "QA"}
		case "getChat":
			result = map[string]any{"id": -1001, "type": "channel", "title": "test", "username": "test", "accent_color_id": 1, "max_reaction_count": 1}
		case "getChatMember":
			s := status.Load().(string)
			if fmt.Sprint(args["user_id"]) == "123" {
				s = "administrator"
			}
			result = map[string]any{"status": s, "user": map[string]any{"id": 1, "first_name": "test", "is_bot": false}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": result})
	}))
	defer server.Close()
	b, e := bot.New("123:test", bot.WithServerURL(server.URL), bot.WithSkipGetMe())
	if e != nil {
		t.Fatal(e)
	}
	c := &Community{Config: cfg, TG: tg.NewClient(b, nil, 0)}
	msg := &models.Message{ID: 1, Chat: models.Chat{ID: 1, Type: models.ChatTypePrivate}}
	if c.Access(context.Background(), msg, 1) {
		t.Fatal("unsubscribed user admitted")
	}
	status.Store("member")
	if !c.Access(context.Background(), msg, 1) {
		t.Fatal("member rejected")
	}
	fail.Store(true)
	if c.Access(context.Background(), msg, 1) {
		t.Fatal("API failure allowed access")
	}
	write(false)
	if !c.Access(context.Background(), msg, 1) {
		t.Fatal("disable not immediate")
	}
}
