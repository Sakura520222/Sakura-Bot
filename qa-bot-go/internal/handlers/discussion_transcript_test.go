package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/config"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/contracts"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/db"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/engine"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/llm"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/tg"
	"github.com/go-sql-driver/mysql"
	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

type transcriptTools struct{}

func (transcriptTools) ToolSchemasJSON() json.RawMessage { return json.RawMessage(`[]`) }
func (transcriptTools) NewExecutor() engine.ToolExecutor { return transcriptTools{} }
func (transcriptTools) Execute(context.Context, string, map[string]any) (string, error) {
	return `{}`, nil
}

type transcriptMemory struct{}

func (transcriptMemory) GetChannelContext(context.Context) (string, error) {
	return "系统支持多个频道，当前所在位置须以 Telegram 路由信息为准。", nil
}

type transcriptBilling struct {
	discussionBilling
	finished chan bool
}

func (b *transcriptBilling) ReserveMessage(ctx context.Context, key string, actor int64) contracts.QuotaCheckResult {
	return b.ReserveTurnMessage(ctx, key, actor, "")
}
func (b *transcriptBilling) FinishReservation(_ context.Context, _ string, success bool) {
	b.finished <- success
}

type transcriptMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func TestAnonymousNonForumTranscriptUsesCurrentTelegramContext(t *testing.T) {
	dsn := os.Getenv("MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("MYSQL_TEST_DSN not set")
	}
	mysqlConfig, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	mysqlConfig.ParseTime, mysqlConfig.Loc = true, time.UTC
	database, err := sql.Open("mysql", mysqlConfig.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx := context.Background()
	manager := &db.Manager{DB: database}
	if err := manager.InitDatabase(ctx); err != nil {
		t.Fatal(err)
	}
	if err := manager.InitComments(ctx); err != nil {
		t.Fatal(err)
	}
	chat := models.Chat{ID: -time.Now().UnixNano(), Type: models.ChatTypeSupergroup, Title: "小樱的频道讨论群"}
	defer func() {
		database.ExecContext(ctx, "DELETE FROM conversation_history WHERE user_id=?", chat.ID)
		database.ExecContext(ctx, "DELETE FROM qa_comments WHERE chat_id=?", chat.ID)
	}()
	const channelURL = "https://t.me/sakura_context_news"
	const channelTitle = "小樱资讯频道"
	const username = "NahidaQABot"
	const firstQuestion = "小妲目前这是哪个频道"
	const firstAnswer = "这里是小樱资讯频道的讨论群。"
	const secondQuestion = "我刚刚问了你什么"
	const thirdQuestion = "@NahidaQABot 你是区"
	answers := []string{firstAnswer, "你刚才问当前是哪个频道。", "我是当前群里的助手 Bot。"}
	var llmMu sync.Mutex
	requests := [][]transcriptMessage{}
	llmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []transcriptMessage `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		llmMu.Lock()
		index := len(requests)
		requests = append(requests, request.Messages)
		llmMu.Unlock()
		if index >= len(answers) {
			t.Error("unexpected repeated model request")
			w.WriteHeader(500)
			return
		}
		payload, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": answers[index]}}}})
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", payload)
	}))
	defer llmServer.Close()
	var telegramMu sync.Mutex
	telegramMessages := map[int]models.Message{}
	telegramSequence := 1000
	telegramServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		result := any(true)
		switch method {
		case "getMe":
			result = models.User{ID: 123, IsBot: true, FirstName: "小妲", Username: username}
		case "getChat":
			if fmt.Sprint(args["chat_id"]) == strconv.FormatInt(chat.ID, 10) {
				result = &models.ChatFullInfo{ID: chat.ID, Type: chat.Type, Title: chat.Title}
			} else {
				result = &models.ChatFullInfo{ID: -100520222, Type: models.ChatTypeChannel, Title: channelTitle, Username: "sakura_context_news", LinkedChatID: chat.ID}
			}
		case "sendRichMessage", "sendMessage", "editMessageText":
			text, _ := args["text"].(string)
			if value, exists := args["rich_message"]; exists {
				raw, _ := json.Marshal(value)
				if encoded, ok := value.(string); ok {
					raw = []byte(encoded)
				}
				var rich struct {
					Markdown string `json:"markdown"`
				}
				_ = json.Unmarshal(raw, &rich)
				text = rich.Markdown
			}
			telegramMu.Lock()
			id, _ := strconv.Atoi(fmt.Sprint(args["message_id"]))
			if id == 0 {
				telegramSequence++
				id = telegramSequence
			}
			message := models.Message{ID: id, Chat: chat, From: &models.User{ID: 123, IsBot: true, FirstName: "小妲", Username: username}, Text: text, Date: int(time.Now().Unix())}
			telegramMessages[id] = message
			telegramMu.Unlock()
			result = message
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": result})
	}))
	defer telegramServer.Close()
	sdk, err := bot.New("123:test", bot.WithServerURL(telegramServer.URL), bot.WithSkipGetMe())
	if err != nil {
		t.Fatal(err)
	}
	telegram := tg.NewClient(sdk, nil, 0)
	path := filepath.Join(t.TempDir(), "config.json")
	raw, _ := json.Marshal(map[string]any{"channels": []string{channelURL}, "qa_assistant": map[string]any{"comments": []any{map[string]any{"channel": channelURL, "enabled": true, "wake_word": "小妲", "anonymous_enabled": true, "anonymous_daily_limit": 20}}}})
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	cfg := config.New(filepath.Join(t.TempDir(), "missing.env"), path)
	if err := cfg.Init(); err != nil {
		t.Fatal(err)
	}
	conversations := engine.NewConversations(manager)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	actualEngine := engine.New(engine.Deps{Chat: llm.NewClient(llm.AIConfigFunc(func() llm.AIConfig { return llm.AIConfig{APIKey: "test", BaseURL: llmServer.URL, Model: "test-model"} })), Tools: transcriptTools{}, Conversations: conversations, Persona: fakePersona{value: "你是小妲。"}, Memory: transcriptMemory{}, Logger: logger})
	billing := &transcriptBilling{discussionBilling: discussionBilling{seen: map[string]bool{}}, finished: make(chan bool, 3)}
	community := &Community{Config: cfg, DB: manager, TG: telegram}
	commands := newTestCommands(&fakeTG{}, &fakeQuota{}, &fakeSessions{}, &fakeEngine{})
	commands.tg, commands.engine, commands.sessions, commands.quota = telegram, actualEngine, conversations, billing
	commands.community, commands.access = community, community.Access
	commands.pool = NewAIPool(ctx, 1, 2, time.Minute)
	defer commands.pool.Close()
	send := func(msg *models.Message) {
		t.Helper()
		if !commands.EarlyUpdate(ctx, &models.Update{Message: msg}) {
			t.Fatal("group transcript escaped community routing")
		}
		select {
		case success := <-billing.finished:
			if !success {
				t.Fatal("transcript turn failed")
			}
		case <-time.After(5 * time.Second):
			t.Fatal("transcript turn did not complete")
		}
	}
	anonymousFrom := &models.User{ID: 1087968824, IsBot: true, FirstName: "GroupAnonymousBot"}
	first := &models.Message{ID: 100, Chat: chat, From: anonymousFrom, SenderChat: &chat, Text: firstQuestion}
	send(first)
	telegramMu.Lock()
	var botReply models.Message
	for _, message := range telegramMessages {
		if message.Text == firstAnswer {
			botReply = message
			break
		}
	}
	telegramMu.Unlock()
	if botReply.ID == 0 {
		t.Fatal("first streamed Telegram answer missing")
	}
	second := &models.Message{ID: 101, Chat: chat, From: anonymousFrom, SenderChat: &chat, Text: secondQuestion, MessageThreadID: first.ID, ReplyToMessage: &botReply}
	send(second)
	third := &models.Message{ID: 102, Chat: chat, From: anonymousFrom, SenderChat: &chat, Text: thirdQuestion, MessageThreadID: first.ID, Entities: []models.MessageEntity{{Type: models.MessageEntityTypeMention, Length: len("@" + username)}}}
	send(third)
	llmMu.Lock()
	captured := append([][]transcriptMessage(nil), requests...)
	llmMu.Unlock()
	if len(captured) != 3 {
		t.Fatalf("model requests=%d want3", len(captured))
	}
	for _, index := range []int{0, 2} {
		if len(captured[index]) == 0 || captured[index][0].Role != "system" {
			t.Fatal("system message missing")
		}
		for _, fact := range []string{chat.Title, channelURL, channelTitle, username} {
			if !strings.Contains(captured[index][0].Content, fact) {
				t.Errorf("request%d system context missing %q", index, fact)
			}
		}
	}
	for _, want := range []transcriptMessage{{Role: "user", Content: firstQuestion}, {Role: "assistant", Content: firstAnswer}} {
		found := false
		for _, message := range captured[1] {
			if message.Role == want.Role && strings.Contains(message.Content, want.Content) {
				found = true
			}
		}
		if !found {
			t.Errorf("second request lost prior %s %q", want.Role, want.Content)
		}
	}
	for index, question := range []string{firstQuestion, secondQuestion, thirdQuestion} {
		last := captured[index][len(captured[index])-1]
		if last.Role != "user" || last.Content != question {
			t.Fatalf("request%d original query rewritten: %+v", index, last)
		}
	}
	for _, scope := range billing.scopes {
		if scope != fmt.Sprintf("chat:%d:thread:0", chat.ID) {
			t.Errorf("nonforum reply changed scope to %q", scope)
		}
	}
	var sessions int
	if err := database.QueryRowContext(ctx, "SELECT COUNT(DISTINCT session_id) FROM conversation_history WHERE user_id=?", chat.ID).Scan(&sessions); err != nil || sessions != 1 {
		t.Fatal("transcript split into multiple sessions", sessions, err)
	}
}
