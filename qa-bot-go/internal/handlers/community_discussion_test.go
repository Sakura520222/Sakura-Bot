package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/config"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/contracts"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/db"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/engine"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/qauser"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/tg"
	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

type discussionBilling struct {
	fakeQuota
	seen     map[string]bool
	actors   []int64
	scopes   []string
	limits   []int
	channels []string
	checkins int
	statuses int
}

func (b *discussionBilling) ReserveTurnMessage(ctx context.Context, key string, user int64, _ string) contracts.QuotaCheckResult {
	if b.seen[key] {
		return contracts.QuotaCheckResult{Message: "duplicate"}
	}
	b.seen[key] = true
	b.actors = append(b.actors, user)
	b.scopes = append(b.scopes, engine.Scope(ctx))
	_, limit, _ := contracts.AnonymousQuota(ctx)
	b.limits = append(b.limits, limit)
	b.channels = append(b.channels, contracts.AnonymousChannel(ctx))
	return contracts.QuotaCheckResult{Allowed: true}
}
func (b *discussionBilling) Checkin(context.Context, int64) string { b.checkins++; return "checkin" }
func (b *discussionBilling) GetUsageStatus(context.Context, int64) UsageStatus {
	b.statuses++
	return UsageStatus{}
}

type actorRegistrationStore struct {
	fakeUserStore
	registered []int64
}

func (s *actorRegistrationStore) RegisterUser(_ context.Context, user int64, _, _ *string, _ bool) error {
	s.registered = append(s.registered, user)
	return nil
}
func TestAnonymousActorIsNeverRegisteredAsUser(t *testing.T) {
	store := &actorRegistrationStore{}
	commands := newTestCommands(&fakeTG{}, &fakeQuota{}, &fakeSessions{}, &fakeEngine{})
	commands.users = qauser.New(store)
	commands.register(context.Background(), &messageActor{ID: -1001, Anonymous: true})
	commands.register(context.Background(), &messageActor{ID: 1087968824, Anonymous: true})
	commands.register(context.Background(), &messageActor{ID: 7})
	if len(store.registered) != 1 || store.registered[0] != 7 {
		t.Fatal("anonymous identity registered as real user", store.registered)
	}
}

func TestLinkedDiscussionRoutingAndRevocation(t *testing.T) {
	dsn := os.Getenv("MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("MYSQL_TEST_DSN not set")
	}
	database, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	manager := &db.Manager{DB: database}
	ctx := context.Background()
	if err = manager.InitComments(ctx); err != nil {
		t.Fatal(err)
	}
	chat := models.Chat{ID: -time.Now().UnixNano(), Type: models.ChatTypeSupergroup, IsForum: true}
	t.Cleanup(func() { database.Exec("DELETE FROM qa_comments WHERE chat_id=?", chat.ID) })
	var link atomic.Int64
	var systemMessages atomic.Int64
	link.Store(chat.ID)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		result := any(map[string]any{"message_id": 999, "date": 1, "chat": map[string]any{"id": chat.ID, "type": "supergroup"}})
		switch r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:] {
		case "sendMessage":
			systemMessages.Add(1)
		case "getMe":
			result = map[string]any{"id": 123, "is_bot": true, "first_name": "QA", "username": "ActualBot"}
		case "getChatMember":
			result = map[string]any{"status": "administrator", "user": map[string]any{"id": 123, "is_bot": true, "first_name": "QA"}}
		case "getChat":
			result = map[string]any{"id": -1005, "type": "channel", "title": "test", "linked_chat_id": link.Load(), "accent_color_id": 1, "max_reaction_count": 1}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": result})
	}))
	defer server.Close()
	client, err := bot.New("123:test", bot.WithServerURL(server.URL), bot.WithSkipGetMe())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config.json")
	enabled, anonymous := true, false
	secondAnonymous := false
	limit := 4
	requireSubscription := false
	write := func() {
		raw, _ := json.Marshal(map[string]any{"channels": []string{"https://t.me/first", "https://t.me/second"}, "qa_assistant": map[string]any{
			"require_subscription": requireSubscription,
			"required_channels":    []string{"https://t.me/first"},
			"comments": []map[string]any{
				{"channel": "https://t.me/first", "enabled": true, "wake_word": "小妲", "anonymous_enabled": anonymous, "anonymous_daily_limit": limit},
				{"channel": "https://t.me/second", "enabled": enabled, "wake_word": "二号", "anonymous_enabled": secondAnonymous, "anonymous_daily_limit": limit},
			},
		}})
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write()
	cfg := config.New(filepath.Join(t.TempDir(), "missing.env"), path)
	if err := cfg.Init(); err != nil {
		t.Fatal(err)
	}
	community := &Community{Config: cfg, DB: manager, TG: tg.NewClient(client, nil, 0)}
	commands := newTestCommands(&fakeTG{sendOK: true}, &fakeQuota{}, &fakeSessions{}, &fakeEngine{chunks: []engine.Chunk{engine.CtrlChunk(contracts.CtrlDone)}})
	billing := &discussionBilling{seen: map[string]bool{}}
	commands.quota, commands.community, commands.access = billing, community, community.Access
	probe := &commentContextProbe{seen: make(chan [2]string, 20)}
	commands.engine = probe
	next := 0
	send := func(text string, change func(*models.Message)) *models.Message {
		next++
		msg := &models.Message{ID: next, Chat: chat, From: &models.User{ID: 7}, Text: text, MessageThreadID: 50}
		if change != nil {
			change(msg)
		}
		if !commands.EarlyUpdate(ctx, &models.Update{Message: msg}) {
			t.Fatal("public update escaped routing")
		}
		return msg
	}
	requireSubscription = true
	write()
	prompt := &models.Message{ID: 850, Chat: chat, Text: "请订阅", From: &models.User{ID: 123, IsBot: true}}
	if !community.callbackAccess(ctx, prompt, models.User{ID: 7}) {
		t.Fatal("human subscription callback rejected due to assistant prompt sender")
	}
	if community.callbackAccess(ctx, prompt, models.User{ID: 123, IsBot: true}) {
		t.Fatal("bot callback actor admitted")
	}
	link.Store(chat.ID - 1)
	if community.callbackAccess(ctx, prompt, models.User{ID: 7}) {
		t.Fatal("unlinked subscription callback admitted")
	}
	link.Store(chat.ID)
	requireSubscription = false
	write()
	original := "　小妲，保留完整原文"
	first := send(original, nil)
	if len(billing.actors) != 1 || billing.scopes[0] != fmt.Sprintf("chat:%d:thread:50", chat.ID) {
		t.Fatal("ordinary discussion not admitted", billing.scopes)
	}
	if got := <-probe.seen; got != [2]string{"小妲", original} {
		t.Fatal("query rewritten", got)
	}
	commands.EarlyUpdate(ctx, &models.Update{Message: first})
	if len(billing.actors) != 1 {
		t.Fatal("duplicate charged")
	}
	alternateOriginal := "　 二号，保留第二个频道的唤醒词"
	alternate := send(alternateOriginal, nil)
	if len(billing.actors) != 2 {
		t.Fatal("alternate configured wake did not admit exactly once", billing.actors)
	}
	if got := <-probe.seen; got != [2]string{"二号", alternateOriginal} {
		t.Fatal("alternate query rewritten or wrong rule chosen", got)
	}
	commands.EarlyUpdate(ctx, &models.Update{Message: alternate})
	if len(billing.actors) != 2 || len(probe.seen) != 0 {
		t.Fatal("shared discussion produced repeated alternate answer")
	}
	send("follow up", func(m *models.Message) {
		m.ReplyToMessage = &models.Message{ID: 900, Chat: chat, From: &models.User{ID: 123, IsBot: true}}
	})
	send("😀 @ActualBot explain", func(m *models.Message) {
		m.Entities = []models.MessageEntity{{Type: models.MessageEntityTypeMention, Offset: 3, Length: 10}}
	})
	before := len(billing.actors)
	for _, change := range []func(*models.Message){
		func(m *models.Message) { m.SenderChat = &models.Chat{ID: -1008, Type: models.ChatTypeChannel} },
		func(m *models.Message) { m.From.IsBot = true },
		func(m *models.Message) { m.Chat.ID-- },
		func(m *models.Message) { m.EditDate = 1 },
	} {
		send("小妲", change)
	}
	if len(billing.actors) != before {
		t.Fatal("invalid actor admitted")
	}
	send("小妲", func(m *models.Message) { m.From = nil; m.SenderChat = &chat })
	if len(billing.actors) != before {
		t.Fatal("anonymous policy default bypassed")
	}
	// A bot mention/reply without a channel wake word uses an explicit anonymous
	// policy from an enabled linked channel, even when the first rule disables it.
	secondAnonymous = true
	requireSubscription = true
	write()
	before = len(billing.actors)
	beforeMessages := systemMessages.Load()
	answerClient := commands.tg.(*fakeTG)
	beforeAnswers := len(answerClient.sent)
	beforeQueries := len(probe.seen)
	send("@ActualBot anonymous", func(m *models.Message) {
		m.From = nil
		m.SenderChat = &chat
		m.Entities = []models.MessageEntity{{Type: models.MessageEntityTypeMention, Length: 10}}
	})
	send("anonymous reply", func(m *models.Message) {
		m.From = nil
		m.SenderChat = &chat
		m.ReplyToMessage = &models.Message{ID: 902, Chat: chat, From: &models.User{ID: 123, IsBot: true}}
	})
	if len(billing.actors) != before+2 || billing.channels[len(billing.channels)-1] != "https://t.me/second" {
		t.Fatal("anonymous fallback ignored allowed linked rule", billing.channels)
	}
	if systemMessages.Load() != beforeMessages {
		t.Fatal("authorized anonymous questions sent extra system messages")
	}
	if len(answerClient.sent) != beforeAnswers+2 || len(probe.seen) != beforeQueries+2 {
		t.Fatal("authorized anonymous questions did not each produce one answer")
	}
	before = len(billing.actors)
	send("小妲 explicit denied policy", func(m *models.Message) { m.From = nil; m.SenderChat = &chat })
	if len(billing.actors) != before {
		t.Fatal("explicit channel wake borrowed another anonymous policy")
	}
	if err := manager.SaveComment(ctx, db.CommentRecord{ChatID: chat.ID, MessageID: 702, RootID: 702, Channel: "https://t.me/first", Text: "first post"}); err != nil {
		t.Fatal(err)
	}
	send("@ActualBot mapped denied policy", func(m *models.Message) {
		m.From = nil
		m.SenderChat = &chat
		m.Entities = []models.MessageEntity{{Type: models.MessageEntityTypeMention, Length: 10}}
		m.ReplyToMessage = &models.Message{ID: 702, Chat: chat}
	})
	if len(billing.actors) != before {
		t.Fatal("mapped post owner borrowed another anonymous policy")
	}
	anonymousPool := NewAIPool(ctx, 1, 1, time.Minute)
	t.Cleanup(anonymousPool.Close)
	commands.pool = anonymousPool
	blockerJob, _, err := anonymousPool.AcquireScoped(99, "private")
	if err != nil {
		t.Fatal(err)
	}
	workerEntered, releaseWorker := make(chan struct{}), make(chan struct{})
	blockerJob.Submit(func(context.Context) { close(workerEntered); <-releaseWorker })
	<-workerEntered
	beforeMessages = systemMessages.Load()
	beforeAnswers = len(answerClient.sent)
	beforeQueries = len(probe.seen)
	send("二号 anonymous worker question", func(m *models.Message) { m.From = nil; m.SenderChat = &chat })
	workerJob, existing, err := anonymousPool.AcquireScoped(chat.ID, fmt.Sprintf("chat:%d:thread:50", chat.ID))
	close(releaseWorker)
	if err != nil || !existing {
		t.Fatal("anonymous worker question was not queued", err)
	}
	select {
	case <-workerJob.done:
	case <-time.After(2 * time.Second):
		t.Fatal("anonymous worker question did not finish")
	}
	if systemMessages.Load() != beforeMessages {
		t.Fatal("authorized anonymous worker question sent an extra system message")
	}
	if len(answerClient.sent) != beforeAnswers+1 || len(probe.seen) != beforeQueries+1 {
		t.Fatal("authorized anonymous worker question did not produce one answer")
	}
	anonymousJob, _, err := anonymousPool.AcquireScoped(chat.ID, fmt.Sprintf("chat:%d:thread:50", chat.ID))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(anonymousJob.Release)
	anonymousJob.Admit(engine.TurnInput{Query: "initial"}, "anonymous-seed", func() contracts.QuotaCheckResult { return contracts.QuotaCheckResult{Allowed: true} })
	send("anonymous active follow-up", func(m *models.Message) { m.From = nil; m.SenderChat = &chat })
	if got := anonymousJob.Take(false); len(got) != 1 || got[0].Query != "anonymous active follow-up" || billing.channels[len(billing.channels)-1] != "https://t.me/second" {
		t.Fatal("anonymous active fallback failed", got, billing.channels)
	}
	anonymousJob.Release()
	commands.pool = nil
	anonymous = true
	requireSubscription = true
	write()
	send("小妲 anonymous", func(m *models.Message) { m.From = &models.User{ID: 1087968824, IsBot: true}; m.SenderChat = &chat })
	if billing.actors[len(billing.actors)-1] != chat.ID || billing.limits[len(billing.limits)-1] != limit {
		t.Fatal("anonymous accounting identity incorrect", billing.actors, billing.limits)
	}
	send("签到", func(m *models.Message) { m.From = nil; m.SenderChat = &chat })
	send("/status", func(m *models.Message) {
		m.From = nil
		m.SenderChat = &chat
		m.Entities = []models.MessageEntity{{Type: models.MessageEntityTypeBotCommand, Length: 7}}
	})
	if billing.checkins != 0 || billing.statuses != 0 {
		t.Fatal("anonymous used personal accounting")
	}
	limit = 2
	write()
	send("小妲", func(m *models.Message) { m.SenderChat = &chat })
	if billing.limits[len(billing.limits)-1] != 2 {
		t.Fatal("quota update stale")
	}
	before = len(billing.actors)
	link.Store(chat.ID - 1)
	send("小妲", nil)
	if len(billing.actors) != before {
		t.Fatal("removed link remained authorized")
	}
	link.Store(chat.ID)
	if err := manager.SaveComment(ctx, db.CommentRecord{ChatID: chat.ID, MessageID: 700, RootID: 700, Channel: "https://t.me/second", Text: "post"}); err != nil {
		t.Fatal(err)
	}
	send("二号", func(m *models.Message) { m.ReplyToMessage = &models.Message{ID: 700, Chat: chat} })
	if billing.scopes[len(billing.scopes)-1] != fmt.Sprintf("chat:%d:post:700", chat.ID) {
		t.Fatal("post owner lost", billing.scopes)
	}
	send("二号 thread", func(m *models.Message) {
		m.MessageThreadID = 700
		m.ReplyToMessage = &models.Message{ID: 701, Chat: chat}
	})
	if billing.scopes[len(billing.scopes)-1] != fmt.Sprintf("chat:%d:post:700", chat.ID) {
		t.Fatal("thread-only post context lost", billing.scopes)
	}
	crossChatReply := &models.Message{ID: 898, Chat: chat, MessageThreadID: 50, ReplyToMessage: &models.Message{ID: 700, Chat: models.Chat{ID: chat.ID - 1}}}
	if got := engine.Scope(community.ScopeContext(engine.WithScope(ctx, messageScope(crossChatReply)), crossChatReply)); got != fmt.Sprintf("chat:%d:thread:50", chat.ID) {
		t.Fatal("cross-chat reply selected local post", got)
	}
	threadMessage := &models.Message{ID: 899, Chat: chat, MessageThreadID: 700}
	if got := engine.Scope(community.ScopeContext(engine.WithScope(ctx, messageScope(threadMessage)), threadMessage)); got != fmt.Sprintf("chat:%d:post:700", chat.ID) {
		t.Fatal("command post scope lost", got)
	}
	enabled = false
	write()
	before = len(billing.actors)
	send("小妲", func(m *models.Message) { m.ReplyToMessage = &models.Message{ID: 700, Chat: chat} })
	if len(billing.actors) != before {
		t.Fatal("disabled post owner borrowed enabled peer rule")
	}
	// Active turns reuse the same actor/thread inbox without a new wake word.
	pool := NewAIPool(ctx, 1, 1, time.Minute)
	defer pool.Close()
	commands.pool = pool
	job, _, err := pool.AcquireScoped(7, fmt.Sprintf("chat:%d:thread:50", chat.ID))
	if err != nil {
		t.Fatal(err)
	}
	defer job.Release()
	job.Admit(engine.TurnInput{Query: "initial"}, "seed", func() contracts.QuotaCheckResult { return contracts.QuotaCheckResult{Allowed: true} })
	send("injected", nil)
	if got := job.Take(false); len(got) != 1 || got[0].Query != "injected" {
		t.Fatal("active follow-up not injected", got)
	}
	anonymous = false
	write()
	if _, ok := community.admissionContext(ctx, &models.Message{ID: 800, Chat: chat, Text: "小妲", SenderChat: &chat}, chat.ID); ok {
		t.Fatal("anonymous revocation stale")
	}
	job.Release()
	queuedPool := NewAIPool(ctx, 1, 2, time.Minute)
	defer queuedPool.Close()
	commands.pool = queuedPool
	blocker, _, err := queuedPool.AcquireScoped(99, "private")
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	blocker.Submit(func(context.Context) { close(entered); <-release })
	<-entered
	queryCount := len(probe.seen)
	send("小妲 queued", nil)
	queued, existing, err := queuedPool.AcquireScoped(7, fmt.Sprintf("chat:%d:thread:50", chat.ID))
	if err != nil || !existing {
		t.Fatal("queued turn missing", err)
	}
	link.Store(chat.ID - 1)
	close(release)
	select {
	case <-queued.done:
	case <-time.After(2 * time.Second):
		t.Fatal("revoked queued turn did not finish")
	}
	if len(probe.seen) != queryCount || billing.refund != 1 {
		t.Fatal("queued revocation executed engine or failed refund", billing.refund)
	}
}
