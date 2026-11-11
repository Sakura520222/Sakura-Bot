package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/db"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/tg"
	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

func forwardedDiscussionFixture(t *testing.T) *models.Message {
	t.Helper()
	var msg models.Message
	err := json.Unmarshal([]byte(`{"message_id":97073,"chat":{"id":-1001857954538,"type":"supergroup"},"sender_chat":{"id":-1002039687763,"type":"channel"},"is_automatic_forward":true,"forward_origin":{"type":"channel","date":1794409918,"chat":{"id":-1001944394067,"type":"channel"},"message_id":13009},"text":"助手 Bot 升级公告及使用说明"}`), &msg)
	if err != nil {
		t.Fatal(err)
	}
	return &msg
}
func TestLinkedAutomaticPostDistinguishesForwardOrigin(t *testing.T) {
	channel := &models.ChatFullInfo{ID: -1002039687763, LinkedChatID: -1001857954538}
	for _, tc := range []struct {
		name   string
		change func(*models.Message)
		want   bool
	}{
		{"cross-channel forwarded post", func(*models.Message) {}, true},
		{"wrong sending channel", func(m *models.Message) { m.SenderChat.ID = -1001944394067 }, false},
		{"manual forward in group", func(m *models.Message) { m.IsAutomaticForward = false }, false},
		{"unrelated group", func(m *models.Message) { m.Chat.ID = -1001958325890 }, false},
		{"no current channel evidence", func(m *models.Message) { m.SenderChat = nil }, false},
		{"hidden original source", func(m *models.Message) { m.ForwardOrigin = nil }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			msg := forwardedDiscussionFixture(t)
			tc.change(msg)
			source, post, valid := linkedAutomaticPost(msg, channel)
			if valid != tc.want {
				t.Fatalf("valid=%v want=%v", valid, tc.want)
			}
			if tc.name == "cross-channel forwarded post" && (source != -1001944394067 || post != 13009) {
				t.Fatal("original source was overwritten", source, post)
			}
		})
	}
	// Legacy direct originals without sender_chat still have authoritative origin.
	msg := forwardedDiscussionFixture(t)
	msg.SenderChat = nil
	originChannel := &models.ChatFullInfo{ID: -1001944394067, LinkedChatID: msg.Chat.ID}
	if _, _, ok := linkedAutomaticPost(msg, originChannel); !ok {
		t.Fatal("direct original compatibility lost")
	}
}
func TestForwardedPostCaptureKeepsOriginalDiscussion(t *testing.T) {
	dsn := os.Getenv("MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("MYSQL_TEST_DSN not set")
	}
	database, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx := context.Background()
	manager := &db.Manager{DB: database}
	if err = manager.InitComments(ctx); err != nil {
		t.Fatal(err)
	}
	msg := forwardedDiscussionFixture(t)
	chat := -time.Now().UnixNano()
	current, source := chat+1, chat+2
	// Decode an actual Bot API shape while isolating database IDs for this test.
	raw, _ := json.Marshal(msg)
	var payload map[string]any
	_ = json.Unmarshal(raw, &payload)
	payload["chat"].(map[string]any)["id"] = chat
	payload["sender_chat"].(map[string]any)["id"] = current
	payload["forward_origin"].(map[string]any)["chat"].(map[string]any)["id"] = source
	raw, _ = json.Marshal(payload)
	if err = json.Unmarshal(raw, msg); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		database.Exec("DELETE FROM qa_comments WHERE chat_id=?", chat)
		database.Exec("DELETE FROM qa_posts WHERE channel_id IN (?,?)", source, current)
	})
	if err = manager.MapPost(ctx, source, 13009, chat+3, 66661); err != nil {
		t.Fatal(err)
	}
	client, err := bot.New("123:test", bot.WithSkipGetMe())
	if err != nil {
		t.Fatal(err)
	}
	const url = "https://t.me/current"
	community := &Community{DB: manager, TG: tg.NewClient(client, nil, 0), chats: map[string]communityChat{url: {info: &models.ChatFullInfo{ID: current, LinkedChatID: chat}, expires: time.Now().Add(time.Minute)}}}
	query := &models.Message{ID: 97077, Chat: msg.Chat, From: &models.User{ID: 7}, Text: "小妲，你好", ReplyToMessage: msg}
	if root := community.capture(ctx, query, url); root != 97073 {
		t.Fatal("forwarded discussion root not recovered", root)
	}
	if manager.CommentChannel(ctx, chat, 97073) != url {
		t.Fatal("root assigned to original channel")
	}
	var mappedChat int64
	var root int
	if err = database.QueryRow("SELECT chat_id,root_id FROM qa_posts WHERE channel_id=? AND post_id=13009", source).Scan(&mappedChat, &root); err != nil || mappedChat != chat+3 || root != 66661 {
		t.Fatal("original discussion overwritten", mappedChat, root, err)
	}
	err = database.QueryRow("SELECT root_id FROM qa_posts WHERE channel_id=? AND post_id=13009", current).Scan(&root)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("original post ID falsely used as destination post ID", err)
	}
	snapshot, err := manager.CommentContext(ctx, chat, 97073)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(snapshot, fmt.Sprintf(`"source_channel_id":%d`, source)) || !strings.Contains(snapshot, `"source_message_id":13009`) {
		t.Fatal("original source metadata missing", snapshot)
	}
}
