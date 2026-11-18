package handlers

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"math/big"
	"regexp"
	"strconv"
	"strings"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/Sakura520222/Sakura-Bot/internal/db/guard"
	"github.com/Sakura520222/Sakura-Bot/internal/tg"
)

var (
	verificationStart    = regexp.MustCompile(`^verify_([a-f0-9]{32})([a-f0-9]{24})$`)
	verificationCallback = regexp.MustCompile(`^sv:([a-f0-9]{32}):([a-f0-9]{24}):([0-3])$`)
)

const verificationInvalid = "验证链接或按钮已失效，或不属于你。 / This verification link or button is no longer valid for you."

type VerificationHandler struct {
	repo     *guard.Repository
	tg       verificationTelegram
	botID    int64
	username string
}

type verificationTelegram interface {
	SendMessage(context.Context, *bot.SendMessageParams) (*models.Message, error)
	EditMessageText(context.Context, *bot.EditMessageTextParams) (*models.Message, error)
	AnswerCallbackQuery(context.Context, *bot.AnswerCallbackQueryParams) (bool, error)
}

func NewVerificationHandler(repo *guard.Repository, client *tg.Client, botID int64, username string) *VerificationHandler {
	return &VerificationHandler{repo: repo, tg: client, botID: botID, username: username}
}

func (h *VerificationHandler) Matches(upd *models.Update) bool {
	if upd == nil {
		return false
	}
	if upd.CallbackQuery != nil && strings.HasPrefix(upd.CallbackQuery.Data, "sv:") {
		return true
	}
	_, _, found := verificationCommand(upd.Message)
	return found
}

func verificationCommand(message *models.Message) (string, string, bool) {
	if message == nil {
		return "", "", false
	}
	parts := strings.Fields(message.Text)
	if len(parts) < 2 || !strings.HasPrefix(strings.ToLower(parts[1]), "verify_") {
		return "", "", false
	}
	name, target, _ := strings.Cut(parts[0], "@")
	if !strings.EqualFold(name, "/start") {
		return "", "", false
	}
	if len(parts) != 2 {
		return "", target, true
	}
	return parts[1], target, true
}

// Each attempt gets disjoint callback tokens, including attempts after a wrong answer.
func newVerificationQuestion(types []string) (*guard.VerificationQuestion, error) {
	var nonce [12]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	words := []string{"月亮 / Moon", "太阳 / Sun", "星星 / Star", "云朵 / Cloud", "雨滴 / Rain", "雪花 / Snow", "闪电 / Lightning", "彩虹 / Rainbow"}
	for i := 0; i < 4; i++ {
		pick, err := rand.Int(rand.Reader, big.NewInt(int64(len(words)-i)))
		if err != nil {
			return nil, err
		}
		j := i + int(pick.Int64())
		words[i], words[j] = words[j], words[i]
	}
	if len(types) == 0 {
		types = []string{"word", "position"}
	}
	questionType, err := rand.Int(rand.Reader, big.NewInt(int64(len(types))))
	if err != nil {
		return nil, err
	}
	choice, err := rand.Int(rand.Reader, big.NewInt(4))
	if err != nil {
		return nil, err
	}
	answer := int(choice.Int64())
	prompt := "请选择 / Select: " + words[answer]
	if types[questionType.Int64()] == "position" {
		prompt = verificationPositionPrompt(answer)
	}
	return &guard.VerificationQuestion{Prompt: prompt, Options: words[:4], Nonce: hex.EncodeToString(nonce[:]), Answer: answer}, nil
}

func verificationPositionPrompt(answer int) string {
	return fmt.Sprintf("请选择 %d 号按钮 / Choose button %d", answer+1, answer+1)
}

func (h *VerificationHandler) HandleUpdate(ctx context.Context, upd *models.Update) bool {
	if !h.Matches(upd) {
		return false
	}
	if query := upd.CallbackQuery; query != nil {
		h.callback(ctx, query)
		return true
	}
	msg := upd.Message
	if !isPrivateMessage(msg) || msg.ForwardOrigin != nil || msg.SenderChat != nil {
		return true
	}
	value, target, _ := verificationCommand(msg)
	if target != "" && !strings.EqualFold(target, h.username) {
		return true
	}
	parts := verificationStart.FindStringSubmatch(value)
	if parts == nil {
		h.send(ctx, msg.Chat.ID, verificationInvalid, nil)
		return true
	}
	var state *guard.Verification
	err := h.repo.WithLock(ctx, msg.From.ID, func(s *guard.Store) error {
		current, err := s.Verification(ctx, parts[1], msg.From.ID)
		if err != nil || !h.valid(current) || subtle.ConstantTimeCompare([]byte(current.Secret), []byte(parts[2])) != 1 {
			return err
		}
		entry, err := s.Input(ctx, msg.From.ID)
		if err != nil {
			return err
		}
		if entry != nil {
			return errVerificationAppeal
		}
		if current.State == "pending" {
			current.State = "in_progress"
			current.StartedAt = current.Now
			current.Question, err = newVerificationQuestion(current.QuestionTypes)
			if err != nil {
				return err
			}
			saved, err := s.SaveVerification(ctx, current)
			if err != nil || !saved {
				return err
			}
		}
		if current.State == "in_progress" && current.Question != nil {
			state = current
		}
		return nil
	})
	if state == nil {
		h.send(ctx, msg.Chat.ID, verificationResult(err), nil)
	} else {
		h.sendQuestion(ctx, state, 0)
	}
	return true
}

var errVerificationAppeal = fmt.Errorf("verification appeal active")

func verificationResult(err error) string {
	switch err {
	case nil:
		return verificationInvalid
	case errVerificationAppeal:
		return "请先完成或取消当前申诉。 / Finish or cancel your current appeal first."
	default:
		return "验证服务暂时不可用，请稍后打开群内链接重试。 / Verification is temporarily unavailable. Reopen the group link to retry."
	}
}

func (h *VerificationHandler) valid(state *guard.Verification) bool {
	return h.botID > 0 && state != nil && state.BotID == h.botID && state.Version == 1 &&
		state.Steps >= 2 && state.Steps <= 5 && state.MaxAttempts >= 1 && state.MaxAttempts <= 10
}

func (h *VerificationHandler) callback(ctx context.Context, q *models.CallbackQuery) {
	parts := verificationCallback.FindStringSubmatch(q.Data)
	msg := q.Message.Message
	if parts == nil || msg == nil || msg.From == nil || msg.From.ID != h.botID ||
		!isPrivateCallback(q, msg.Chat.ID, msg.Chat.Type) {
		h.answer(ctx, q.ID, verificationInvalid)
		return
	}
	var next *guard.Verification
	var answer string
	err := h.repo.WithLock(ctx, q.From.ID, func(s *guard.Store) error {
		state, err := s.Verification(ctx, parts[1], q.From.ID)
		if err != nil || !h.valid(state) || state.State != "in_progress" || state.Question == nil ||
			state.Question.Nonce != parts[2] || len(state.Receipts) >= state.Steps || state.Attempts >= state.MaxAttempts {
			return err
		}
		entry, err := s.Input(ctx, q.From.ID)
		if err != nil {
			return err
		}
		if entry != nil {
			return errVerificationAppeal
		}
		option, _ := strconv.Atoi(parts[3])
		if option == state.Question.Answer {
			state.Receipts = append(state.Receipts, state.Question.Nonce)
		} else {
			state.Attempts++
			answer = "答案错误，请再试一次。 / Incorrect. Try again."
		}
		switch {
		case len(state.Receipts) == state.Steps:
			state.State, state.CompletedAt = "completed", state.Now
			state.Question, state.Secret = nil, ""
			answer = "已完成验证，等待群内机器人检查并恢复权限。 / Complete. The group bot will check and restore your permissions."
		case state.Attempts >= state.MaxAttempts:
			state.State, state.Question, state.Secret = "failed", nil, ""
			answer = "验证失败。 / Verification failed."
		default:
			state.Question, err = newVerificationQuestion(state.QuestionTypes)
			if err != nil {
				return err
			}
		}
		saved, err := s.SaveVerification(ctx, state)
		if err != nil || !saved {
			answer = ""
			return err
		}
		next = state
		return nil
	})
	if next == nil {
		h.answer(ctx, q.ID, verificationResult(err))
		return
	}
	h.answer(ctx, q.ID, answer)
	if next.Question != nil {
		h.sendQuestion(ctx, next, msg.ID)
	} else {
		h.editOrSend(ctx, next.ActorID, msg.ID, answer, models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{}})
	}
}

func (h *VerificationHandler) sendQuestion(ctx context.Context, state *guard.Verification, messageID int) {
	question := state.Question
	buttons := make([]models.InlineKeyboardButton, len(question.Options))
	for i, option := range question.Options {
		buttons[i] = models.InlineKeyboardButton{
			Text:         fmt.Sprintf("%d. %s", i+1, option),
			CallbackData: fmt.Sprintf("sv:%s:%s:%d", state.SessionID, question.Nonce, i),
		}
	}
	rows := make([][]models.InlineKeyboardButton, 0, (len(buttons)+1)/2)
	for first := 0; first < len(buttons); first += 2 {
		rows = append(rows, buttons[first:min(first+2, len(buttons))])
	}
	prompt := question.Prompt
	if strings.HasPrefix(prompt, "从左到右请选择 / From left to right, choose the ") {
		// Keep open challenges usable when the user resumes them after an upgrade.
		prompt = verificationPositionPrompt(question.Answer)
	}
	h.editOrSend(ctx, state.ActorID, messageID, fmt.Sprintf("%d/%d\n%s", len(state.Receipts)+1, state.Steps, prompt),
		models.InlineKeyboardMarkup{InlineKeyboard: rows})
}

func (h *VerificationHandler) editOrSend(ctx context.Context, chatID int64, messageID int, text string, markup models.InlineKeyboardMarkup) {
	if messageID != 0 {
		_, err := h.tg.EditMessageText(ctx, &bot.EditMessageTextParams{ChatID: chatID, MessageID: messageID, Text: text, ReplyMarkup: markup})
		if err == nil {
			return
		}
	}
	h.send(ctx, chatID, text, markup)
}

func (h *VerificationHandler) send(ctx context.Context, chatID int64, text string, markup models.ReplyMarkup) {
	_, _ = h.tg.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: text, ReplyMarkup: markup})
}

func (h *VerificationHandler) answer(ctx context.Context, id, text string) {
	_, _ = h.tg.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{CallbackQueryID: id, Text: text, ShowAlert: text != ""})
}
