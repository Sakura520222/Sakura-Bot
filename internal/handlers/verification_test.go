package handlers

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/Sakura520222/Sakura-Bot/internal/db/guard"
)

type verificationBot struct {
	messages []*bot.SendMessageParams
}

func (b *verificationBot) SendMessage(_ context.Context, params *bot.SendMessageParams) (*models.Message, error) {
	b.messages = append(b.messages, params)
	return &models.Message{ID: len(b.messages)}, nil
}

func (b *verificationBot) AnswerCallbackQuery(context.Context, *bot.AnswerCallbackQueryParams) (bool, error) {
	return true, nil
}

func (b *verificationBot) EditMessageText(context.Context, *bot.EditMessageTextParams) (*models.Message, error) {
	return &models.Message{}, nil
}

func TestVerificationQuestionPayloadLimits(t *testing.T) {
	id, secret := strings.Repeat("a", 32), strings.Repeat("b", 24)
	if payload := "verify_" + id + secret; len(payload) > 64 || !verificationStart.MatchString(payload) {
		t.Fatal("Telegram deep link payload exceeds limits")
	}
	nonces := map[string]bool{}
	for range 10 {
		question, err := newVerificationQuestion(nil)
		if err != nil || len(question.Options) != 4 || question.Answer < 0 || question.Answer > 3 || nonces[question.Nonce] {
			t.Fatalf("invalid or repeated challenge: %v", err)
		}
		data := fmt.Sprintf("sv:%s:%s:%d", id, question.Nonce, question.Answer)
		if len(data) > 64 || !verificationCallback.MatchString(data) {
			t.Fatal("Telegram callback payload exceeds limits")
		}
		nonces[question.Nonce] = true
	}
}

func TestVerificationQuestionUsesTwoButtonsPerRow(t *testing.T) {
	telegram := &verificationBot{}
	handler := &VerificationHandler{tg: telegram}
	state := &guard.Verification{
		ActorID: 1, SessionID: strings.Repeat("a", 32), Steps: 2,
		Question: &guard.VerificationQuestion{
			Prompt:  "从左到右请选择 / From left to right, choose the 第三个 / third",
			Options: []string{"彩虹 / Rainbow", "星星 / Star", "月亮 / Moon", "雨滴 / Rain"},
			Nonce:   strings.Repeat("b", 24), Answer: 2,
		},
	}
	handler.sendQuestion(context.Background(), state, 0)
	message := telegram.messages[0]
	if want := "1/2\n请选择 3 号按钮 / Choose button 3"; message.Text != want {
		t.Fatalf("got prompt %q, want %q", message.Text, want)
	}
	rows := message.ReplyMarkup.(models.InlineKeyboardMarkup).InlineKeyboard
	if len(rows) != 2 || len(rows[0]) != 2 || len(rows[1]) != 2 {
		t.Fatalf("wanted two rows of two buttons, got %v", rows)
	}
	third := rows[1][0]
	if third.Text != "3. 月亮 / Moon" || !strings.HasSuffix(third.CallbackData, ":2") {
		t.Fatalf("third button got %v", third)
	}
}

func TestVerificationMySQLBindsActorAndConsumesEachStepOnce(t *testing.T) {
	dsn := os.Getenv("MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("MYSQL_TEST_DSN is required for integration test")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	repo := guard.New(db)
	if err := repo.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	userID := time.Now().UnixMicro()
	sessionID, secret := fmt.Sprintf("%032x", userID), strings.Repeat("b", 24)
	const botID int64 = 12345
	payload := fmt.Sprintf(`{"restriction_confirmed":true,"assistant":{"version":1,"state":"pending","secret":"%s","username":"test_bot","bot_id":%d,"steps":2,"max_attempts":3}}`, secret, botID)
	_, err = db.ExecContext(ctx, `INSERT INTO moderation_verification_sessions
		(id,chat_id,user_id,status,payload,message_id,expires_at,updated_at)
		VALUES (?,-100123,?,'waiting',?,445,UTC_TIMESTAMP()+INTERVAL 10 MINUTE,UTC_TIMESTAMP())`, sessionID, userID, payload)
	if err != nil {
		t.Fatal(err)
	}
	defer db.ExecContext(ctx, "DELETE FROM moderation_verification_sessions WHERE id=?", sessionID)
	defer db.ExecContext(ctx, "DELETE FROM moderation_appeal_inputs WHERE actor_id=?", userID)
	telegram := &verificationBot{}
	handler := &VerificationHandler{repo: repo, tg: telegram, username: "test_bot", botID: botID}
	read := func() *guard.Verification {
		t.Helper()
		var state *guard.Verification
		err := repo.WithLock(ctx, userID, func(store *guard.Store) error {
			var err error
			state, err = store.Verification(ctx, sessionID, userID)
			return err
		})
		if err != nil || state == nil {
			t.Fatalf("cannot read shared state: %v", err)
		}
		return state
	}
	start := func(actor int64) {
		handler.HandleUpdate(ctx, &models.Update{Message: &models.Message{
			Chat: models.Chat{ID: actor, Type: models.ChatTypePrivate}, From: &models.User{ID: actor},
			Text: "/start verify_" + sessionID + secret,
		}})
	}
	start(userID + 1)
	if read().State != "pending" {
		t.Fatal("another user consumed the link")
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO moderation_cases
		(id,event_key,chat_id,user_id,action,status,payload,created_at,updated_at)
		VALUES (?,?,-100123,?,'ban','pending','{}',UTC_TIMESTAMP(),UTC_TIMESTAMP())`, sessionID, sessionID+sessionID, userID); err != nil {
		t.Fatal(err)
	}
	defer db.ExecContext(ctx, "DELETE FROM moderation_cases WHERE id=?", sessionID)
	if _, err := db.ExecContext(ctx, `INSERT INTO moderation_appeals
		(id,case_id,chat_id,user_id,status,payload,retry_at,created_at,updated_at)
		VALUES (?,?,-100123,?,'draft','{}',UTC_TIMESTAMP(),UTC_TIMESTAMP(),UTC_TIMESTAMP())`, sessionID, sessionID, userID); err != nil {
		t.Fatal(err)
	}
	defer db.ExecContext(ctx, "DELETE FROM moderation_appeals WHERE id=?", sessionID)
	defer db.ExecContext(ctx, "DELETE FROM moderation_appeal_inputs WHERE actor_id=?", userID)
	if err := repo.WithLock(ctx, userID, func(store *guard.Store) error {
		_, err := store.SaveInput(ctx, userID, sessionID, "draft", map[string]any{}, "")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	start(userID)
	if read().State != "pending" {
		t.Fatal("verification interrupted an active appeal")
	}
	if _, err := db.ExecContext(ctx, "DELETE FROM moderation_appeal_inputs WHERE actor_id=?", userID); err != nil {
		t.Fatal(err)
	}
	start(userID)
	state := read()
	if state.State != "in_progress" || state.Question == nil || len(state.Receipts) != 0 {
		t.Fatal("opening the deep link was counted as a step")
	}
	click := func(question *guard.VerificationQuestion, answer int) {
		handler.HandleUpdate(ctx, &models.Update{CallbackQuery: &models.CallbackQuery{
			ID: "query", From: models.User{ID: userID},
			Message: models.MaybeInaccessibleMessage{Message: &models.Message{
				From: &models.User{ID: botID, IsBot: true}, Chat: models.Chat{ID: userID, Type: models.ChatTypePrivate},
			}},
			Data: fmt.Sprintf("sv:%s:%s:%d", sessionID, question.Nonce, answer),
		}})
	}
	click(state.Question, state.Question.Answer)
	click(state.Question, state.Question.Answer)
	state = read()
	if state.State != "in_progress" || len(state.Receipts) != 1 || state.Question == nil {
		t.Fatal("a replay advanced the current step")
	}
	click(state.Question, state.Question.Answer)
	state = read()
	if state.State != "completed" || len(state.Receipts) != 2 || state.Secret != "" || state.CompletedAt == 0 {
		t.Fatal("completion was not persisted with two independent receipts")
	}
}
