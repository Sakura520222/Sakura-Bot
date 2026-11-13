package handlers

import (
	"context"
	"fmt"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/contracts"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/db"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/engine"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/media"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/tg"
	"github.com/go-telegram/bot/models"
	"time"
)

type messageBilling interface {
	ReserveMessage(context.Context, string, int64) contracts.QuotaCheckResult
	FinishReservation(context.Context, string, bool)
}

func messageKey(msg *models.Message) string { return fmt.Sprintf("%d:%d", msg.Chat.ID, msg.ID) }

type jobContextKey struct{}

func messageScope(msg *models.Message) string {
	if msg.Chat.Type == models.ChatTypePrivate || msg.Chat.Type == "" {
		return "private"
	}
	return fmt.Sprintf("chat:%d:thread:%d", msg.Chat.ID, msg.MessageThreadID)
}
func (c *Commands) submitAI(ctx context.Context, msg *models.Message, user int64, query, hint, placeholder, errPrefix string) {
	scope := engine.Scope(ctx)
	if scope == "private" {
		scope = messageScope(msg)
		ctx = engine.WithScope(ctx, scope)
	}
	if c.community != nil {
		var allowed bool
		ctx, allowed = c.community.admissionContext(ctx, msg, user)
		if !allowed {
			return
		}
	}
	if c.access != nil && !c.access(ctx, msg, user) {
		return
	}
	turnID := ""
	reserve := func() contracts.QuotaCheckResult {
		if c.community != nil {
			var allowed bool
			ctx, allowed = c.community.admissionContext(ctx, msg, user)
			if !allowed {
				return contracts.QuotaCheckResult{Message: "本群助手访问权限已变更，请联系管理员。"}
			}
		}
		if billing, ok := c.quota.(interface {
			ReserveTurnMessage(context.Context, string, int64, string) contracts.QuotaCheckResult
		}); ok {
			return billing.ReserveTurnMessage(ctx, messageKey(msg), user, turnID)
		}
		if billing, ok := c.quota.(messageBilling); ok {
			return billing.ReserveMessage(ctx, messageKey(msg), user)
		}
		return c.quota.CheckQuota(ctx, user)
	}
	if c.pool == nil {
		q := reserve()
		if !q.Allowed {
			c.replyPlain(ctx, msg, q.Message)
			return
		}
		if c.community != nil && user < 0 {
			c.community.say(ctx, msg, anonymousAdmissionNotice, nil)
		}
		c.runStream(ctx, msg, user, query, hint, placeholder, q, errPrefix)
		return
	}
	job, existing, err := c.pool.AcquireScoped(user, scope)
	if err != nil {
		c.replyPlain(ctx, msg, err.Error())
		return
	}
	submitted := existing
	defer func() {
		if !submitted {
			job.Release()
		}
	}()
	turnID = job.ID
	input := engine.TurnInput{Query: query}
	if existing {
		if ref, e := media.TelegramRef(msg); e == nil && ref != nil {
			input.Image = ref
			input.Download = func(ctx context.Context, r media.Ref) (string, error) {
				if c.files == nil {
					return "", fmt.Errorf("image client unavailable")
				}
				return media.Download(ctx, c.files, r)
			}
		}
	}
	q, accepted := job.Admit(input, messageKey(msg), reserve)
	if !accepted {
		if existing && job.Sealed() {
			c.submitAI(ctx, msg, user, query, hint, placeholder, errPrefix)
			return
		}
		c.replyPlain(ctx, msg, q.Message)
		return
	}

	if existing {
		return
	}
	refresh, _ := ctx.Value(publicRefreshKey{}).(func(context.Context) string)
	wakeWord := engine.CommentWakeWord(ctx)
	job.Submit(func(work context.Context) {
		work = engine.WithScope(work, scope)
		work = engine.WithCommentWakeWord(work, wakeWord)
		work = engine.WithTurn(work, job)
		if refresh != nil {
			work = engine.WithPublicContext(work, refresh)
		}
		work = context.WithValue(work, jobContextKey{}, job)
		if c.community != nil {
			var allowed bool
			work, allowed = c.community.admissionContext(work, msg, user)
			if !allowed || (c.access != nil && !c.access(work, msg, user)) {
				c.finishBilling(work, msg, user, false)
				return
			}
			if user < 0 {
				c.community.say(work, msg, anonymousAdmissionNotice, nil)
			}
		}
		if c.community != nil && msg.Chat.Type != models.ChatTypePrivate {
			root := c.community.DB.CommentRoot(work, msg.Chat.ID, msg.ID)
			work = tg.WithReplyObserver(work, func(reply *models.Message, text string) {
				_ = c.community.DB.SaveComment(work, db.CommentRecord{ChatID: msg.Chat.ID, MessageID: reply.ID, RootID: root, UserID: c.community.TG.Bot().ID(), AuthorRole: "assistant", Text: text, Date: reply.Date, ReplyID: msg.ID, Edited: reply.EditDate > 0})
			})
		}
		c.runStream(work, msg, user, query, hint, placeholder, q, errPrefix)
	})
	submitted = true
}
func (c *Commands) finishBilling(ctx context.Context, msg *models.Message, user int64, success bool) {
	base := context.WithoutCancel(ctx)
	if job, ok := ctx.Value(jobContextKey{}).(*AIJob); ok {
		base = job.CleanupContext()
	}
	cleanup, cancel := context.WithTimeout(base, 5*time.Second)
	defer cancel()
	if billing, ok := c.quota.(messageBilling); ok {
		ids := []string{messageKey(msg)}
		if job, ok := ctx.Value(jobContextKey{}).(*AIJob); ok {
			ids = job.ReservationIDs()
		}
		for _, id := range ids {
			billing.FinishReservation(cleanup, id, success && ctx.Err() == nil)
		}
	} else if !success || ctx.Err() != nil {
		c.quota.RefundQuota(cleanup, user)
	}
}

func mediaScope(ctx context.Context, user int64, msg *models.Message) media.Scope {
	s := media.Scope{UserID: user, ChatID: msg.Chat.ID}
	if engine.Scope(ctx) != "private" {
		s.Thread = engine.Scope(ctx)
	}
	return s
}
