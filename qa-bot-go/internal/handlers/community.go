package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/config"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/contracts"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/db"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/engine"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/tg"
	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf16"
)

type publicRefreshKey struct{}
type Community struct {
	Config *config.Config
	DB     *db.Manager
	TG     *tg.Client
	mu     sync.Mutex
	chats  map[string]communityChat
}
type communityChat struct {
	info    *models.ChatFullInfo
	expires time.Time
}

func channelTarget(ch string) any {
	if strings.HasPrefix(ch, "https://t.me/") {
		return "@" + strings.TrimPrefix(ch, "https://t.me/")
	}
	return ch
}
func (c *Community) chat(ctx context.Context, ch string) (*models.ChatFullInfo, error) {
	c.mu.Lock()
	cached := c.chats[ch]
	c.mu.Unlock()
	if cached.info != nil && time.Now().Before(cached.expires) {
		return cached.info, nil
	}
	return c.loadChat(ctx, ch)
}

// Authorization always refreshes Telegram linkage, independently of display caches.
func (c *Community) loadChat(ctx context.Context, ch string) (*models.ChatFullInfo, error) {
	v, e := c.TG.GetChat(ctx, &bot.GetChatParams{ChatID: channelTarget(ch)})
	if e != nil {
		return nil, e
	}
	c.mu.Lock()
	if c.chats == nil {
		c.chats = map[string]communityChat{}
	}
	c.chats[ch] = communityChat{v, time.Now().Add(time.Minute)}
	c.mu.Unlock()
	return v, nil
}

// matchedRule resolves a mapped post's owner before selecting a group fallback.
// A disabled post owner cannot borrow another channel's policy in a shared group.
func (c *Community) matchedRule(ctx context.Context, msg *models.Message) (config.CommentAssistant, int, bool) {
	settings, err := c.Config.AssistantSettings()
	if err != nil || msg == nil || (msg.Chat.Type != models.ChatTypeGroup && msg.Chat.Type != models.ChatTypeSupergroup) {
		return config.CommentAssistant{}, 0, false
	}
	root, owner := 0, ""
	for current, depth := msg, 0; current != nil && current.Chat.ID == msg.Chat.ID && depth < 64; current, depth = current.ReplyToMessage, depth+1 {
		if found := c.DB.CommentRoot(ctx, msg.Chat.ID, current.ID); found > 0 {
			root, owner = found, c.DB.CommentChannel(ctx, msg.Chat.ID, found)
			break
		}
	}
	if root == 0 && msg.MessageThreadID > 0 {
		root = c.DB.CommentRoot(ctx, msg.Chat.ID, msg.MessageThreadID)
		if root > 0 {
			owner = c.DB.CommentChannel(ctx, msg.Chat.ID, root)
		}
	}
	var fallback config.CommentAssistant
	foundFallback := false
	actor, _, _ := effectiveUserMessage(&models.Update{Message: msg})
	anonymous := actor != nil && actor.Anonymous
	for _, rule := range settings.Comments {
		if !rule.Enabled {
			continue
		}
		info, err := c.loadChat(ctx, rule.Channel)
		if err != nil || info.LinkedChatID != msg.Chat.ID {
			continue
		}
		if owner != "" {
			if owner == rule.Channel {
				return rule, root, true
			}
			continue
		}
		for current, depth := msg, 0; current != nil && current.Chat.ID == msg.Chat.ID && depth < 64; current, depth = current.ReplyToMessage, depth+1 {
			if current.IsAutomaticForward {
				if _, _, valid := linkedAutomaticPost(current, info); valid {
					return rule, current.ID, true
				}
			}
		}
		ruleWake := commentWakeRequested(msg, rule.WakeWord, 0)
		fallbackWake := commentWakeRequested(msg, fallback.WakeWord, 0)
		// Explicit channel wake words retain their policy. Unqualified anonymous
		// mentions, replies and active follow-ups select an authorized linked rule.
		preferAnonymous := anonymous && !ruleWake && !fallbackWake && rule.AnonymousEnabled && !fallback.AnonymousEnabled
		if !foundFallback || (ruleWake && !fallbackWake) || preferAnonymous {
			fallback, foundFallback = rule, true
		}
	}
	if owner != "" {
		return config.CommentAssistant{}, 0, false
	}
	// An automatic channel post with no verified enabled owner is not a group query.
	for current, depth := msg, 0; current != nil && current.Chat.ID == msg.Chat.ID && depth < 64; current, depth = current.ReplyToMessage, depth+1 {
		if current.IsAutomaticForward {
			return config.CommentAssistant{}, 0, false
		}
	}
	return fallback, 0, foundFallback
}

func (c *Community) admissionContext(ctx context.Context, msg *models.Message, user int64) (context.Context, bool) {
	if msg.Chat.Type == models.ChatTypePrivate || msg.Chat.Type == "" {
		return ctx, user > 0
	}
	actor, _, valid := effectiveUserMessage(&models.Update{Message: msg})
	if !valid || actor.ID != user {
		return ctx, false
	}
	rule, _, allowed := c.matchedRule(ctx, msg)
	if !allowed {
		return ctx, false
	}
	if actor.Anonymous {
		if !rule.AnonymousEnabled || rule.AnonymousDailyLimit < 1 {
			return ctx, false
		}
		ctx = contracts.WithAnonymousQuota(ctx, msg.Chat.ID, rule.AnonymousDailyLimit, rule.Channel)
	}
	return ctx, true
}

func member(m *models.ChatMember) bool {
	if m == nil {
		return false
	}
	switch m.Type {
	case models.ChatMemberTypeMember, models.ChatMemberTypeOwner, models.ChatMemberTypeAdministrator:
		return true
	case models.ChatMemberTypeRestricted:
		return m.Restricted != nil && m.Restricted.IsMember
	}
	return false
}
func (c *Community) Access(ctx context.Context, msg *models.Message, user int64) bool {
	s, e := c.Config.AssistantSettings()
	if e != nil {
		c.say(ctx, msg, "助手配置无效，请联系管理员。", nil)
		return false
	}
	if msg.Chat.Type != models.ChatTypePrivate && msg.Chat.Type != "" {
		if _, allowed := c.admissionContext(ctx, msg, user); !allowed {
			return false
		}
		if user < 0 {
			return true
		}
	}
	return c.subscriptionAccess(ctx, msg, user, s)
}

// A verification callback's actor is q.From, while its message is our own prompt.
func (c *Community) callbackAccess(ctx context.Context, msg *models.Message, actor models.User) bool {
	if actor.ID <= 0 || actor.IsBot {
		return false
	}
	settings, err := c.Config.AssistantSettings()
	if err != nil {
		return false
	}
	if msg.Chat.Type != models.ChatTypePrivate {
		if _, _, allowed := c.matchedRule(ctx, msg); !allowed {
			return false
		}
	}
	return c.subscriptionAccess(ctx, msg, actor.ID, settings)
}

func (c *Community) subscriptionAccess(ctx context.Context, msg *models.Message, user int64, s config.AssistantSettings) bool {
	message, buttons := c.subscriptionProblem(ctx, user, s)
	if message == "" {
		return true
	}
	var keyboard models.ReplyMarkup
	if len(buttons) != 0 {
		buttons = append(buttons, []models.InlineKeyboardButton{{Text: "我已订阅", CallbackData: "qa:verify"}})
		keyboard = &models.InlineKeyboardMarkup{InlineKeyboard: buttons}
	}
	c.say(ctx, msg, message, keyboard)
	return false
}

// WebSubscription uses the same Telegram membership checks without sending a message.
func (c *Community) WebSubscription(ctx context.Context, user int64) string {
	settings, err := c.Config.AssistantSettings()
	if err != nil || user <= 0 {
		return "助手配置无效，请联系管理员。"
	}
	message, buttons := c.subscriptionProblem(ctx, user, settings)
	for _, row := range buttons {
		for _, button := range row {
			message += "\n" + button.Text + " " + button.URL
		}
	}
	return message
}

func (c *Community) subscriptionProblem(ctx context.Context, user int64, s config.AssistantSettings) (string, [][]models.InlineKeyboardButton) {
	if !s.RequireSubscription {
		return "", nil
	}
	me, e := c.TG.GetMe(ctx)
	if e != nil {
		return "订阅验证暂不可用，请稍后重试。", nil
	}
	buttons := [][]models.InlineKeyboardButton{}
	for _, ch := range s.RequiredChannels {
		chat, e := c.chat(ctx, ch)
		if e != nil {
			return "无法查询必订阅频道，请联系管理员。", nil
		}
		self, e := c.TG.GetChatMember(ctx, &bot.GetChatMemberParams{ChatID: chat.ID, UserID: me.ID})
		if e != nil || self == nil || (self.Type != models.ChatMemberTypeAdministrator && self.Type != models.ChatMemberTypeOwner) {
			return "助手 Bot 需要成为必订阅频道的管理员后才能验证。", nil
		}
		m, e := c.TG.GetChatMember(ctx, &bot.GetChatMemberParams{ChatID: chat.ID, UserID: user})
		if e != nil {
			return "订阅验证服务异常，请稍后重试。", nil
		}
		if !member(m) {
			url := ch
			if chat.Username != "" {
				url = "https://t.me/" + chat.Username
			} else if chat.InviteLink != "" {
				url = chat.InviteLink
			}
			buttons = append(buttons, []models.InlineKeyboardButton{{Text: "加入 " + chat.Title, URL: url}})
		}
	}
	if len(buttons) == 0 {
		return "", nil
	}
	return "请先订阅以下全部频道，然后重新验证。", buttons
}
func (c *Community) say(ctx context.Context, msg *models.Message, text string, keyboard models.ReplyMarkup) {
	_, _ = c.TG.SendMessage(ctx, &bot.SendMessageParams{ChatID: msg.Chat.ID, MessageThreadID: msg.MessageThreadID, Text: text, ReplyParameters: &models.ReplyParameters{MessageID: msg.ID}, ReplyMarkup: keyboard})
}
func (c *Community) ScopeContext(ctx context.Context, msg *models.Message) context.Context {
	root := c.DB.CommentRoot(ctx, msg.Chat.ID, msg.ID)
	if root == 0 && msg.ReplyToMessage != nil && msg.ReplyToMessage.Chat.ID == msg.Chat.ID {
		root = c.DB.CommentRoot(ctx, msg.Chat.ID, msg.ReplyToMessage.ID)
	}
	if root == 0 && msg.MessageThreadID > 0 {
		root = c.DB.CommentRoot(ctx, msg.Chat.ID, msg.MessageThreadID)
	}
	if root > 0 {
		return engine.WithScope(ctx, fmt.Sprintf("chat:%d:post:%d", msg.Chat.ID, root))
	}
	return ctx
}

// linkedAutomaticPost separates the current sending channel from the original
// forward source. SenderChat identifies the linked channel for automatic posts.
func linkedAutomaticPost(msg *models.Message, channel *models.ChatFullInfo) (int64, int, bool) {
	if msg == nil || channel == nil || !msg.IsAutomaticForward || msg.ID <= 0 || msg.Chat.ID != channel.LinkedChatID {
		return 0, 0, false
	}
	var origin struct {
		Chat struct {
			ID int64 `json:"id"`
		} `json:"chat"`
		MessageID int `json:"message_id"`
	}
	b, _ := json.Marshal(msg.ForwardOrigin)
	_ = json.Unmarshal(b, &origin)
	if msg.SenderChat != nil {
		return origin.Chat.ID, origin.MessageID, msg.SenderChat.ID == channel.ID
	}
	// Compatibility for older updates without sender_chat: only direct originals
	// can be verified this way; never guess the forwarding channel from text.
	return origin.Chat.ID, origin.MessageID, origin.Chat.ID == channel.ID
}

func (c *Community) capture(ctx context.Context, msg *models.Message, ch string) int {
	if msg == nil {
		return 0
	}
	root := c.DB.CommentRoot(ctx, msg.Chat.ID, msg.ID)
	if root == 0 && msg.MessageThreadID > 0 {
		root = c.DB.CommentRoot(ctx, msg.Chat.ID, msg.MessageThreadID)
	}
	reply := 0
	var sourceChannelID int64
	var sourceMessageID int
	if msg.IsAutomaticForward {
		info, err := c.chat(ctx, ch)
		if err != nil {
			return 0
		}
		var valid bool
		sourceChannelID, sourceMessageID, valid = linkedAutomaticPost(msg, info)
		if !valid {
			return 0
		}
		root = msg.ID
		// A forwarded channel post retains the *original* source ID. Do not map it
		// to this group's root or overwrite the original channel's own discussion.
		if sourceChannelID == info.ID && sourceMessageID > 0 {
			_ = c.DB.MapPost(ctx, info.ID, sourceMessageID, msg.Chat.ID, root)
		}
	} else if msg.ReplyToMessage != nil && msg.ReplyToMessage.Chat.ID == msg.Chat.ID {
		reply = msg.ReplyToMessage.ID
		if from := msg.ReplyToMessage.From; from != nil && from.IsBot && from.ID == c.TG.Bot().ID() && msg.ReplyToMessage.Chat.ID == msg.Chat.ID {
			if err := c.DB.MarkAssistantComment(ctx, msg.Chat.ID, reply, from.ID); err != nil {
				slog.Error("更新助手评论身份失败", "error", err)
			}
		}
		replyRoot := c.DB.CommentRoot(ctx, msg.Chat.ID, reply)
		if replyRoot == 0 {
			replyRoot = c.capture(ctx, msg.ReplyToMessage, ch)
		}
		if replyRoot > 0 {
			root = replyRoot
		}
	}

	text := msg.Text
	if text == "" {
		text = msg.Caption
	}
	authorRole := "unknown"
	user := int64(0)
	if msg.From != nil {
		user = msg.From.ID
		authorRole = "user"
		if msg.From.IsBot {
			authorRole = "bot"
			if user == c.TG.Bot().ID() {
				authorRole = "assistant"
			}
		}
	}
	if msg.SenderChat != nil {
		user = 0
		authorRole = "external_channel_identity"
		if msg.SenderChat.ID == msg.Chat.ID {
			authorRole = "anonymous_group_admin"
		}
	}
	if msg.IsAutomaticForward {
		authorRole = "channel_post"
		user = 0
	}
	media := ""
	if len(msg.Photo) > 0 {
		media = "photo"
	}
	if msg.Document != nil {
		media = "document: " + msg.Document.FileName
	}
	if msg.Video != nil {
		media = "video（仅可见说明，未读取视频）"
	}
	if msg.Voice != nil {
		media = "voice（未转录）"
	}
	if msg.Audio != nil {
		media = "audio（未转录）"
	}
	if msg.Sticker != nil {
		media = "sticker"
	}
	if msg.Animation != nil {
		media = "animation（未读取动画）"
	}
	if text == "" && media == "" {
		text = "[此消息无可见文字]"
	}
	if err := c.DB.SaveComment(ctx, db.CommentRecord{ChatID: msg.Chat.ID, MessageID: msg.ID, RootID: root, Channel: ch, SourceChannelID: sourceChannelID, SourceMessageID: sourceMessageID, UserID: user, AuthorRole: authorRole, Text: text, Date: msg.Date, ReplyID: reply, Edited: msg.EditDate > 0, Media: media}); err != nil {
		slog.Error("评论持久化失败", "error", err)
		return 0
	}
	_ = c.DB.ReconcileComments(ctx, msg.Chat.ID)
	return c.DB.CommentRoot(ctx, msg.Chat.ID, msg.ID)
}

// commentWakeRequested checks Telegram's reply sender ID, never a display name or
// quoted text. The caller must still validate the enabled channel and post root.
func commentWakeRequested(msg *models.Message, wakeWord string, assistantID int64, usernames ...string) bool {
	if msg == nil {
		return false
	}
	query := msg.Text
	if query == "" {
		query = msg.Caption
	}
	if wakeWord != "" && strings.HasPrefix(strings.TrimLeftFunc(query, unicode.IsSpace), wakeWord) {
		return true
	}
	entities := msg.Entities
	if msg.Text == "" {
		entities = msg.CaptionEntities
	}
	encoded := utf16.Encode([]rune(query))
	for _, entity := range entities {
		if entity.Offset < 0 || entity.Length <= 0 || entity.Offset > len(encoded)-entity.Length {
			continue
		}
		if entity.Type == models.MessageEntityTypeTextMention && entity.User != nil && assistantID > 0 && entity.User.ID == assistantID {
			return true
		}
		if entity.Type == models.MessageEntityTypeMention && len(usernames) > 0 && usernames[0] != "" {
			mention := string(utf16.Decode(encoded[entity.Offset : entity.Offset+entity.Length]))
			if strings.EqualFold(mention, "@"+usernames[0]) {
				return true
			}
		}
	}
	reply := msg.ReplyToMessage
	return assistantID > 0 && reply != nil && reply.Chat.ID == msg.Chat.ID &&
		reply.From != nil && reply.From.ID == assistantID && reply.From.IsBot
}

// EarlyUpdate runs before private-chat FSMs so public comments never enter appeals/submission state.
func (c *Commands) EarlyUpdate(ctx context.Context, upd *models.Update) bool {
	community := c.community
	if community == nil || upd == nil {
		return false
	}
	if q := upd.CallbackQuery; q != nil && q.Data == "qa:verify" {
		_, _ = community.TG.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{CallbackQueryID: q.ID})
		if q.Message.Message != nil && community.callbackAccess(ctx, q.Message.Message, q.From) {
			community.say(ctx, q.Message.Message, "订阅验证通过，可以发送问题了。", nil)
		}
		return true
	}
	if post := upd.EditedChannelPost; post != nil {
		settings, e := community.Config.AssistantSettings()
		if e != nil {
			return true
		}
		for _, rule := range settings.Comments {
			if !rule.Enabled {
				continue
			}
			info, e := community.chat(ctx, rule.Channel)
			if e == nil && info.ID == post.Chat.ID {
				text := post.Text
				if text == "" {
					text = post.Caption
				}
				_ = community.DB.EditPost(ctx, post.Chat.ID, post.ID, text)
			}
		}
		return true
	}
	msg := upd.Message
	edited := false
	if msg == nil {
		msg = upd.EditedMessage
		edited = true
	}
	if msg == nil {
		return false
	}
	if msg.Chat.Type == models.ChatTypePrivate {
		return false
	}
	settings, e := community.Config.AssistantSettings()
	if e != nil {
		return true
	}
	rule, root, linked := community.matchedRule(ctx, msg)
	if !linked {
		return true
	}
	captured := community.capture(ctx, msg, rule.Channel)
	if captured > 0 {
		root = captured
	}
	actor, _, valid := effectiveUserMessage(&models.Update{Message: msg})
	if edited || !valid {
		return true
	}
	ctx = engine.WithScope(ctx, messageScope(msg))
	if root > 0 {
		ctx = engine.WithScope(ctx, fmt.Sprintf("chat:%d:post:%d", msg.Chat.ID, root))
	}
	wakeRequested := commentWakeRequested(msg, rule.WakeWord, community.TG.Bot().ID())
	// Resolve the actual authenticated bot username only for entity-based mentions.
	if !wakeRequested && (len(msg.Entities) > 0 || len(msg.CaptionEntities) > 0) {
		if me, err := community.TG.GetMe(ctx); err == nil {
			wakeRequested = commentWakeRequested(msg, rule.WakeWord, me.ID, me.Username)
		}
	}
	if actor.Anonymous && !rule.AnonymousEnabled {
		if wakeRequested {
			community.say(ctx, msg, "本群匿名管理员助手访问尚未获管理员授权。请在评论助手设置中启用匿名群组身份并设置群级额度。", nil)
		}
		return true
	}
	if actor.Anonymous {
		ctx = contracts.WithAnonymousQuota(ctx, msg.Chat.ID, rule.AnonymousDailyLimit, rule.Channel)
	}
	if msg.Text == "签到" {
		if actor.Anonymous {
			community.say(ctx, msg, "本群匿名管理员使用共享群级额度，签到与个人积分仅支持真实用户。", nil)
		} else if settings.CheckinEnabled {
			if billing, ok := c.quota.(interface {
				Checkin(context.Context, int64) string
			}); ok {
				community.say(ctx, msg, billing.Checkin(ctx, actor.ID), nil)
			}
		}
		return true
	}
	if cmd, _, yes := parseCommand(msg); yes {
		switch cmd {
		case "status":
			c.Status(ctx, upd)
		case "clear":
			c.Clear(ctx, upd)
		case "help":
			c.Help(ctx, upd)
		}
		return true
	}
	active := c.pool != nil && c.pool.ActiveScope(actor.ID, engine.Scope(ctx))
	if !active && !wakeRequested {
		return true
	}
	if root > 0 {
		refresh := func(ctx context.Context) string {
			snapshot, err := community.DB.CommentContext(ctx, msg.Chat.ID, root)
			if err != nil {
				return "评论快照读取失败，不能声称已获取完整讨论。"
			}
			return snapshot
		}
		ctx = context.WithValue(ctx, publicRefreshKey{}, refresh)
	}
	ctx = engine.WithCommentWakeWord(ctx, rule.WakeWord)
	c.HandleMessage(ctx, upd)
	return true
}
