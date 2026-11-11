package tg

import (
	"context"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/contracts"
	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"strings"
	"time"
	"unicode/utf8"
)

func (c *Client) SendRichMessage(ctx context.Context, p *bot.SendRichMessageParams) (*models.Message, error) {
	return call(ctx, c.timeout, func(ctx context.Context) (*models.Message, error) { return c.b.SendRichMessage(ctx, p) })
}
func (c *Client) SendRichMessageDraft(ctx context.Context, p *bot.SendRichMessageDraftParams) (bool, error) {
	return call(ctx, c.timeout, func(ctx context.Context) (bool, error) { return c.b.SendRichMessageDraft(ctx, p) })
}

// SplitRich keeps ordinary paragraphs, fenced code and table rows intact. Fences
// are closed/reopened at a boundary; table headers are repeated in continuation.
func SplitRich(text string) []string {
	const limit = 30000
	parts := []string{}
	var buf strings.Builder
	fence := ""
	tableHeader := ""
	previousLine := ""
	lines := 0
	flush := func() {
		s := buf.String()
		if fence != "" {
			s += "\n```"
		}
		if strings.TrimSpace(s) != "" {
			parts = append(parts, s)
		}
		buf.Reset()
		lines = 0
		if fence != "" {
			buf.WriteString(fence + "\n")
		} else if tableHeader != "" {
			buf.WriteString(tableHeader)
		}
	}
	for _, line := range strings.SplitAfter(text, "\n") {
		trim := strings.TrimSpace(line)
		if fence == "" && strings.Contains(trim, "|") && strings.Contains(trim, "---") {
			tableHeader = previousLine + line
		} else if fence == "" && !strings.Contains(trim, "|") {
			tableHeader = ""
		}
		if utf8.RuneCountInString(buf.String())+utf8.RuneCountInString(line) > limit || lines >= 350 {
			flush()
		}
		// A single pathological line has no paragraph boundary; preserve every rune.
		runes := []rune(line)
		for len(runes) > limit-100 {
			buf.WriteString(string(runes[:limit-100]))
			runes = runes[limit-100:]
			flush()
		}
		line = string(runes)
		buf.WriteString(line)
		lines++
		if strings.HasPrefix(trim, "```") {
			if fence == "" {
				fence = trim
			} else {
				fence = ""
			}
		}
		previousLine = line
	}
	if buf.Len() > 0 {
		flush()
	}
	return parts
}
func (c *Client) StreamRich(ctx context.Context, msg *models.Message, chunks <-chan StreamChunk) bool {
	typingCtx, stop := context.WithCancel(ctx)
	defer stop()
	go func() {
		ticker := time.NewTicker(4 * time.Second)
		defer ticker.Stop()
		for {
			_, _ = call(typingCtx, c.timeout, func(ctx context.Context) (bool, error) {
				return c.b.SendChatAction(ctx, &bot.SendChatActionParams{ChatID: msg.Chat.ID, MessageThreadID: msg.MessageThreadID, Action: models.ChatActionTyping})
			})
			select {
			case <-typingCtx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	text := ""
	last := ""
	ids := []int{}
	done := false
	private := msg.Chat.Type == models.ChatTypePrivate
	draft := msg.ID
	if draft == 0 {
		draft = 1
	}
	publish := func(final bool) error {
		parts := SplitRich(text)
		if len(parts) == 0 {
			return nil
		}
		if private && !final {
			_, err := c.SendRichMessageDraft(ctx, &bot.SendRichMessageDraftParams{ChatID: msg.Chat.ID, MessageThreadID: msg.MessageThreadID, DraftID: draft, RichMessage: models.InputRichMessage{Markdown: parts[len(parts)-1]}})
			return err
		}
		for i, part := range parts {
			rich := models.InputRichMessage{Markdown: part}
			if i < len(ids) {
				edited, err := c.EditMessageText(ctx, &bot.EditMessageTextParams{ChatID: msg.Chat.ID, MessageID: ids[i], RichMessage: &rich})
				if err != nil && !IsNotModified(err) {
					return err
				}
				if err == nil && edited != nil {
					if observer, ok := ctx.Value(replyObserverKey{}).(func(*models.Message, string)); ok {
						observer(edited, part)
					}
				}
			} else {
				m, err := c.SendRichMessage(ctx, &bot.SendRichMessageParams{ChatID: msg.Chat.ID, MessageThreadID: msg.MessageThreadID, ReplyParameters: &models.ReplyParameters{MessageID: msg.ID}, RichMessage: rich})
				if err != nil {
					return err
				}
				ids = append(ids, m.ID)
				if observer, ok := ctx.Value(replyObserverKey{}).(func(*models.Message, string)); ok {
					observer(m, part)
				}
			}
		}
		return nil
	}
	for {
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
			if text != "" && (text != last || private) {
				if err := publish(false); err != nil {
					c.log.Errorf("富文本预览失败: %v", err)
				} else {
					last = text
				}
			}
		case chunk, ok := <-chunks:
			if !ok {
				if !done {
					return false
				}
				if err := publish(true); err != nil {
					c.log.Errorf("富文本发送失败: %v", err)
					return false
				}
				return true
			}
			if chunk.Text == contracts.CtrlDone {
				done = true
				continue
			}
			if chunk.Text == contracts.CtrlNewSession {
				continue
			}
			if strings.HasPrefix(chunk.Text, contracts.CtrlErrPrefix) {
				_, _ = c.SendMessage(ctx, &bot.SendMessageParams{ChatID: msg.Chat.ID, Text: strings.TrimPrefix(chunk.Text, contracts.CtrlErrPrefix), ReplyParameters: &models.ReplyParameters{MessageID: msg.ID}})
				return false
			}
			text += chunk.Text
		}
	}
}

type replyObserverKey struct{}

func WithReplyObserver(ctx context.Context, observer func(*models.Message, string)) context.Context {
	return context.WithValue(ctx, replyObserverKey{}, observer)
}
