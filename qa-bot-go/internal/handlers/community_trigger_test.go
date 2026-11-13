package handlers

import (
	"context"
	"testing"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

func TestCommentWakeRequested(t *testing.T) {
	const assistantID int64 = 123
	chat := models.Chat{ID: -1001, Type: models.ChatTypeSupergroup}
	reply := func(id int64, isBot bool) *models.Message {
		return &models.Message{ID: 20, Chat: chat, From: &models.User{ID: id, IsBot: isBot, FirstName: "小妲"}}
	}
	for _, tc := range []struct {
		name string
		msg  *models.Message
		want bool
	}{
		{"plain comment", &models.Message{Chat: chat, Text: "继续解释一下"}, false},
		{"reply to assistant without wake word", &models.Message{Chat: chat, Text: "继续解释一下", ReplyToMessage: reply(assistantID, true)}, true},
		{"caption reply to assistant", &models.Message{Chat: chat, Caption: "这张图呢？", ReplyToMessage: reply(assistantID, true)}, true},
		{"another bot with same name", &models.Message{Chat: chat, Text: "继续解释一下", ReplyToMessage: reply(456, true)}, false},
		{"another user with same name", &models.Message{Chat: chat, Text: "继续解释一下", ReplyToMessage: reply(456, false)}, false},
		{"missing reply sender", &models.Message{Chat: chat, Text: "继续解释一下", ReplyToMessage: &models.Message{ID: 20, Chat: chat}}, false},
		{"external reply is not local assistant reply", &models.Message{Chat: chat, Text: "继续解释一下", ReplyToMessage: &models.Message{ID: 20, Chat: models.Chat{ID: -1002}, From: &models.User{ID: assistantID, IsBot: true}}}, false},
		{"quoted name in ordinary text", &models.Message{Chat: chat, Text: "引用小妲的话：继续解释一下"}, false},
		{"unicode whitespace wake word", &models.Message{Chat: chat, Text: "　 小妲，解释一下"}, true},
		{"caption wake word", &models.Message{Chat: chat, Caption: "小妲，这张图呢？"}, true},
		{"nil message", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var text, caption string
			if tc.msg != nil {
				text, caption = tc.msg.Text, tc.msg.Caption
			}
			if got := commentWakeRequested(tc.msg, "小妲", assistantID); got != tc.want {
				t.Fatalf("trigger=%v want=%v", got, tc.want)
			}
			if tc.msg != nil && (tc.msg.Text != text || tc.msg.Caption != caption) {
				t.Fatal("trigger rewrote original message")
			}
		})
	}
	if commentWakeRequested(&models.Message{Chat: chat, Text: "hello", ReplyToMessage: reply(0, true)}, "", 0) {
		t.Fatal("unknown bot identity triggered reply")
	}
}

func TestAssistantReplyTriggersAfterTurnClosed(t *testing.T) {
	// Use the authenticated SDK identity without another getMe network request.
	client, err := bot.New("123:test", bot.WithSkipGetMe())
	if err != nil {
		t.Fatal(err)
	}
	pool := NewAIPool(context.Background(), 1, 1, time.Minute)
	defer pool.Close()
	job, _, err := pool.AcquireScoped(7, "chat:-1001:post:10")
	if err != nil {
		t.Fatal(err)
	}
	job.Release()
	if pool.ActiveScope(7, "chat:-1001:post:10") {
		t.Fatal("turn still active")
	}
	msg := &models.Message{Chat: models.Chat{ID: -1001}, Text: "再详细一点", ReplyToMessage: &models.Message{Chat: models.Chat{ID: -1001}, From: &models.User{ID: 123, IsBot: true}}}
	if !commentWakeRequested(msg, "小妲", client.ID()) {
		t.Fatal("reply cannot start a new turn after completion")
	}
}

func TestDiscussionScopeIgnoresOrdinaryReplyTarget(t *testing.T) {
	msg := &models.Message{Chat: models.Chat{ID: -1001, Type: models.ChatTypeSupergroup}, MessageThreadID: 50}
	want := messageScope(msg)
	for _, id := range []int{4, 99} {
		msg.ReplyToMessage = &models.Message{ID: id, Chat: msg.Chat}
		if got := messageScope(msg); got != want {
			t.Fatalf("reply split scope: %q != %q", got, want)
		}
	}
}

func TestEffectiveActorRejectsSenderDisguises(t *testing.T) {
	chat := models.Chat{ID: -1001, Type: models.ChatTypeSupergroup}
	for _, tc := range []struct {
		name string
		msg  models.Message
		want int64
	}{
		{"real user", models.Message{Chat: chat, Text: "hi", From: &models.User{ID: 7}}, 7},
		{"anonymous fake from", models.Message{Chat: chat, Text: "hi", From: &models.User{ID: 1087968824, IsBot: true}, SenderChat: &chat}, chat.ID},
		{"anonymous missing from", models.Message{Chat: chat, Text: "hi", SenderChat: &chat}, chat.ID},
		{"external sender chat", models.Message{Chat: chat, Text: "hi", From: &models.User{ID: 7}, SenderChat: &models.Chat{ID: -1002, Type: models.ChatTypeChannel}}, 0},
		{"bot", models.Message{Chat: chat, Text: "hi", From: &models.User{ID: 123, IsBot: true}}, 0},
		{"negative fake user", models.Message{Chat: chat, Text: "hi", From: &models.User{ID: -7}}, 0},
		{"automatic forward", models.Message{Chat: chat, Text: "hi", IsAutomaticForward: true, SenderChat: &chat}, 0},
		{"service", models.Message{Chat: chat, SenderChat: &chat, NewChatTitle: "hi"}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := tc.msg.From
			actor, msg, ok := effectiveUserMessage(&models.Update{Message: &tc.msg})
			if ok != (tc.want != 0) || ok && (actor.ID != tc.want || msg != &tc.msg) {
				t.Fatalf("actor=%+v ok=%v want=%d", actor, ok, tc.want)
			}
			if tc.msg.From != original {
				t.Fatal("raw Telegram sender mutated")
			}
		})
	}
}

func TestMentionTriggerUsesTelegramEntitiesAndUTF16(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		entity     models.MessageEntity
		want       bool
	}{
		{"actual username", "😀 @ActualBot explain", models.MessageEntity{Type: models.MessageEntityTypeMention, Offset: 3, Length: 10}, true},
		{"case insensitive", "@actualbot explain", models.MessageEntity{Type: models.MessageEntityTypeMention, Length: 10}, true},
		{"unverified text", "@ActualBot explain", models.MessageEntity{}, false},
		{"wrong username", "@OtherBot explain", models.MessageEntity{Type: models.MessageEntityTypeMention, Length: 9}, false},
		{"text mention actual id", "助手 explain", models.MessageEntity{Type: models.MessageEntityTypeTextMention, Length: 2, User: &models.User{ID: 123}}, true},
		{"text mention wrong id", "助手 explain", models.MessageEntity{Type: models.MessageEntityTypeTextMention, Length: 2, User: &models.User{ID: 456}}, false},
		{"invalid range", "@ActualBot", models.MessageEntity{Type: models.MessageEntityTypeMention, Offset: 1 << 30, Length: 10}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			msg := &models.Message{Text: tc.text, Entities: []models.MessageEntity{tc.entity}}
			if got := commentWakeRequested(msg, "小妲", 123, "ActualBot"); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
			msg.Text, msg.Entities, msg.Caption, msg.CaptionEntities = "", nil, tc.text, []models.MessageEntity{tc.entity}
			if got := commentWakeRequested(msg, "小妲", 123, "ActualBot"); got != tc.want {
				t.Fatalf("caption got %v want %v", got, tc.want)
			}
		})
	}
}
