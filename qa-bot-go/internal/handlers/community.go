package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/config"
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
	if !s.RequireSubscription {
		return true
	}
	me, e := c.TG.GetMe(ctx)
	if e != nil {
		c.say(ctx, msg, "订阅验证暂不可用，请稍后重试。", nil)
		return false
	}
	buttons := [][]models.InlineKeyboardButton{}
	missing := false
	for _, ch := range s.RequiredChannels {
		chat, e := c.chat(ctx, ch)
		if e != nil {
			c.say(ctx, msg, "无法查询必订阅频道，请联系管理员。", nil)
			return false
		}
		self, e := c.TG.GetChatMember(ctx, &bot.GetChatMemberParams{ChatID: chat.ID, UserID: me.ID})
		if e != nil || self == nil || (self.Type != models.ChatMemberTypeAdministrator && self.Type != models.ChatMemberTypeOwner) {
			c.say(ctx, msg, "助手 Bot 需要成为必订阅频道的管理员后才能验证。", nil)
			return false
		}
		m, e := c.TG.GetChatMember(ctx, &bot.GetChatMemberParams{ChatID: chat.ID, UserID: user})
		if e != nil {
			c.say(ctx, msg, "订阅验证服务异常，请稍后重试。", nil)
			return false
		}
		if !member(m) {
			missing = true
			url := ch
			if chat.Username != "" {
				url = "https://t.me/" + chat.Username
			} else if chat.InviteLink != "" {
				url = chat.InviteLink
			}
			buttons = append(buttons, []models.InlineKeyboardButton{{Text: "加入 " + chat.Title, URL: url}})
		}
	}
	if !missing {
		return true
	}
	buttons = append(buttons, []models.InlineKeyboardButton{{Text: "我已订阅", CallbackData: "qa:verify"}})
	c.say(ctx, msg, "请先订阅以下全部频道，然后重新验证。", &models.InlineKeyboardMarkup{InlineKeyboard: buttons})
	return false
}
func (c *Community) say(ctx context.Context, msg *models.Message, text string, keyboard models.ReplyMarkup) {
	_, _ = c.TG.SendMessage(ctx, &bot.SendMessageParams{ChatID: msg.Chat.ID, Text: text, ReplyParameters: &models.ReplyParameters{MessageID: msg.ID}, ReplyMarkup: keyboard})
}
func (c *Community) ScopeContext(ctx context.Context, msg *models.Message) context.Context {
	root := c.DB.CommentRoot(ctx, msg.Chat.ID, msg.ID)
	if root == 0 && msg.ReplyToMessage != nil {
		root = c.DB.CommentRoot(ctx, msg.Chat.ID, msg.ReplyToMessage.ID)
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
	} else if msg.ReplyToMessage != nil {
		reply = msg.ReplyToMessage.ID
		if from := msg.ReplyToMessage.From; from != nil && from.IsBot && from.ID == c.TG.Bot().ID() && msg.ReplyToMessage.Chat.ID == msg.Chat.ID {
			if err := c.DB.MarkAssistantComment(ctx, msg.Chat.ID, reply, from.ID); err != nil {
				slog.Error("更新助手评论身份失败", "error", err)
			}
		}
		root = c.DB.CommentRoot(ctx, msg.Chat.ID, reply)
		if root == 0 {
			root = c.capture(ctx, msg.ReplyToMessage, ch)
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
	if msg.IsAutomaticForward {
		authorRole = "channel_post"
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
func commentWakeRequested(msg *models.Message, wakeWord string, assistantID int64) bool {
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
	reply := msg.ReplyToMessage
	return assistantID > 0 && reply != nil && reply.Chat.ID == msg.Chat.ID &&
		reply.From != nil && reply.From.ID == assistantID && reply.From.IsBot
}

// EarlyUpdate runs before private-chat FSMs so public comments never enter appeals/submission state.
func (c *Commands) EarlyUpdate(ctx context.Context, upd *models.Update) bool {
	community := c.community
	if community == nil {
		return false
	}
	if q := upd.CallbackQuery; q != nil && q.Data == "qa:verify" {
		_, _ = community.TG.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{CallbackQueryID: q.ID})
		if q.Message.Message != nil && community.Access(ctx, q.Message.Message, q.From.ID) {
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
	for _, rule := range settings.Comments {
		if !rule.Enabled {
			continue
		}
		info, e := community.chat(ctx, rule.Channel)
		if e != nil || info.LinkedChatID != msg.Chat.ID {
			continue
		}
		if msg.IsAutomaticForward {
			if _, _, valid := linkedAutomaticPost(msg, info); !valid {
				return true
			}
		}
		root := community.capture(ctx, msg, rule.Channel)
		if root > 0 && community.DB.CommentChannel(ctx, msg.Chat.ID, root) != rule.Channel {
			return true
		}
		if edited || msg.From == nil || msg.From.IsBot || msg.IsAutomaticForward {
			return true
		}
		ctx = community.ScopeContext(ctx, msg)
		if msg.Text == "签到" {
			if settings.CheckinEnabled {
				if billing, ok := c.quota.(interface {
					Checkin(context.Context, int64) string
				}); ok {
					community.say(ctx, msg, billing.Checkin(ctx, msg.From.ID), nil)
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
		wakeRequested := commentWakeRequested(msg, rule.WakeWord, community.TG.Bot().ID())
		if root == 0 {
			if wakeRequested {
				community.say(ctx, msg, "未能识别帖子上下文，请回复频道帖子后重试。", nil)
			}
			return true
		}
		active := c.pool != nil && c.pool.ActiveScope(msg.From.ID, engine.Scope(ctx))
		if !active && !wakeRequested {
			return true
		}
		refresh := func(ctx context.Context) string {
			s, e := community.DB.CommentContext(ctx, msg.Chat.ID, root)
			if e != nil {
				return "评论快照读取失败，不能声称已获取完整讨论。"
			}
			return s
		}
		ctx = context.WithValue(ctx, publicRefreshKey{}, refresh)
		ctx = engine.WithCommentWakeWord(ctx, rule.WakeWord)
		c.HandleMessage(ctx, upd)
		return true
	}
	return true
}
