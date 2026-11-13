package handlers

import (
	"context"

	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/contracts"
	"github.com/go-telegram/bot/models"
)

// modelContext supplies verified routing facts independently of the user's text.
func (c *Community) modelContext(ctx context.Context, msg *models.Message) context.Context {
	if msg.Chat.Type != models.ChatTypeGroup && msg.Chat.Type != models.ChatTypeSupergroup {
		return ctx
	}
	settings, err := c.Config.AssistantSettings()
	if err != nil {
		return ctx
	}
	facts := contracts.TelegramContext{
		ChatID: msg.Chat.ID, ChatTitle: msg.Chat.Title, ThreadID: messageTopicID(msg),
		AssistantID: c.TG.Bot().ID(), Channels: []contracts.TelegramChannel{},
	}
	if me, err := c.TG.GetMe(ctx); err == nil && me != nil && me.IsBot && me.ID == facts.AssistantID {
		facts.AssistantUsername = me.Username
	}
	owner := ""
	if root := c.DB.CommentRoot(ctx, msg.Chat.ID, msg.ID); root > 0 {
		owner = c.DB.CommentChannel(ctx, msg.Chat.ID, root)
	}
	seen := map[int64]bool{}
	for _, rule := range settings.Comments {
		if !rule.Enabled || (owner != "" && owner != rule.Channel) {
			continue
		}
		info, err := c.chat(ctx, rule.Channel)
		if err != nil || info.LinkedChatID != msg.Chat.ID || seen[info.ID] {
			continue
		}
		seen[info.ID] = true
		facts.Channels = append(facts.Channels, contracts.TelegramChannel{
			ID: info.ID, Title: info.Title, Username: info.Username, URL: rule.Channel,
		})
	}
	return contracts.WithTelegramContext(ctx, facts)
}
