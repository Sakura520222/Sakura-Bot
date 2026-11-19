// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package mtproto

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/gotd/td/telegram/message/markup"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

const defaultLongMessageMaxRunes = 4000

// MessageID converts a persisted Telegram message identifier to the protocol's
// signed 32-bit range. Zero indicates a missing or invalid identifier.
func MessageID(id int64) int {
	if id <= 0 || id > math.MaxInt32 {
		return 0
	}
	return int(id)
}

func cryptoRandInt64() int64 {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return 1
	}
	v := int64(binary.BigEndian.Uint64(b[:]))
	if v == 0 {
		return 1
	}
	return v
}

// BuildInlineMarkup 将 [][]InlineButton 转换为 gotd/td 的 *tg.ReplyInlineMarkup。
func BuildInlineMarkup(rows [][]InlineButton) *tg.ReplyInlineMarkup {
	if len(rows) == 0 {
		return nil
	}
	tgRows := make([]tg.KeyboardInlineButtonRow, 0, len(rows))
	for _, row := range rows {
		if len(row) == 0 {
			continue
		}
		btns := make([]tg.KeyboardInlineButton, 0, len(row))
		for _, b := range row {
			switch {
			case b.URL != "":
				btns = append(btns, markup.URL(b.Text, b.URL))
			default:
				btns = append(btns, markup.Callback(b.Text, []byte(b.Data)))
			}
		}
		if len(btns) > 0 {
			tgRows = append(tgRows, markup.InlineButtonRow(btns...))
		}
	}
	if len(tgRows) == 0 {
		return nil
	}
	return &tg.ReplyInlineMarkup{Rows: tgRows}
}

func (c *Client) prepareTextAndEntities(ctx context.Context, text string, parseMode string, explicit []EntitySpan) (string, []tg.MessageEntityClass) {
	resolveUser := func(uid int64) tg.InputUserClass {
		if iu, err := c.Peers().ResolveInputUser(ctx, nil, uid); err == nil {
			return iu
		}
		return &tg.InputUser{UserID: uid, AccessHash: 0}
	}

	if len(explicit) > 0 {
		return text, entitiesToTGWithResolver(explicit, resolveUser)
	}

	switch strings.ToLower(strings.TrimSpace(parseMode)) {
	case "markdown", "md":
		plain, spans := ParseMarkdownSpans(text)
		return plain, entitiesToTGWithResolver(spans, resolveUser)
	case "html":
		plain, spans := ParseHTMLSpans(text)
		return plain, entitiesToTGWithResolver(spans, resolveUser)
	default:
		return text, nil
	}
}

// SendMessage 发送单条文本消息，返回发送成功后的 Telegram 消息 ID。
func (c *Client) SendMessage(ctx context.Context, target any, text string, opts SendOptions) (int, error) {
	return c.sendMessageWithID(ctx, target, text, opts, cryptoRandInt64())
}

func (c *Client) sendMessageWithID(ctx context.Context, target any, text string, opts SendOptions, randomID int64) (int, error) {
	if c.noEmoji {
		text, opts = cleanMessageEmoji(text, opts)
	}
	api := c.API()
	if api == nil {
		return 0, errors.New("mtproto client not initialized")
	}

	peer, err := c.Peers().ResolveInputPeer(ctx, api, target)
	if err != nil {
		return 0, err
	}

	req := &tg.MessagesSendMessageRequest{
		NoWebpage: opts.NoWebpage,
		Silent:    opts.Silent,
		Peer:      peer,
		RandomID:  randomID,
	}
	if opts.ReplyToMsgID > 0 {
		req.SetReplyTo(&tg.InputReplyToMessage{ReplyToMsgID: opts.ReplyToMsgID})
	}
	if markup := BuildInlineMarkup(c.cleanButtons(opts.Buttons)); markup != nil {
		req.SetReplyMarkup(markup)
	}
	if c.richMessages {
		req.SetRichMessage(c.inputRichMessage(ctx, text, opts))
	} else {
		sendText, entities := c.prepareTextAndEntities(ctx, text, opts.ParseMode, opts.Entities)
		req.Message = sendText
		if len(entities) > 0 {
			req.SetEntities(entities)
		}
	}

	upd, err := api.MessagesSendMessage(ctx, req)
	if c.richMessages && isRichFormattingError(err) && isRichMarkupMode(opts) {
		req.SetRichMessage(plainRichMessage(richMarkupPlainText(text, opts.ParseMode)))
		upd, err = api.MessagesSendMessage(ctx, req)
	}
	if err != nil {
		return 0, err
	}
	c.Peers().ApplyUpdates(upd)
	return extractSentMessageID(upd, randomID), nil
}

// SendLongMessage 智能分段发送长消息，内置“温和修复 -> 激进剥离 -> 纯文本无实体兜底”三级降级策略。
func (c *Client) SendLongMessage(ctx context.Context, target any, text string, opts SendOptions) ([]int, error) {
	mode := strings.ToLower(strings.TrimSpace(opts.ParseMode))
	preserveMD := mode != "plain" && mode != "html"
	parts := SplitMessageSmart(text, defaultLongMessageMaxRunes, preserveMD)
	if len(parts) == 0 {
		return nil, nil
	}

	sentIDs := make([]int, 0, len(parts))
	for i, part := range parts {
		chunkOpts := opts
		if i > 0 {
			chunkOpts.ReplyToMsgID = 0
		}
		if i < len(parts)-1 {
			chunkOpts.Buttons = nil
		}

		msgID, err := c.sendChunkWithFallback(ctx, target, part, chunkOpts)
		if err != nil {
			return sentIDs, err
		}
		sentIDs = append(sentIDs, msgID)
	}
	return sentIDs, nil
}

func (c *Client) sendChunkWithFallback(ctx context.Context, target any, chunk string, opts SendOptions) (int, error) {
	mode := strings.ToLower(strings.TrimSpace(opts.ParseMode))
	if c.richMessages {
		if mode == "" && len(opts.Entities) == 0 {
			opts.ParseMode = "markdown"
		}
		return c.SendMessage(ctx, target, chunk, opts)
	}
	if len(opts.Entities) > 0 || mode == "plain" {
		return c.SendMessage(ctx, target, chunk, opts)
	}

	if mode == "html" {
		id, err := c.SendMessage(ctx, target, chunk, opts)
		if err == nil || !isEntityFormatRPCError(err) {
			return id, err
		}
		plain, _ := ParseHTMLSpans(chunk)
		plainOpts := opts
		plainOpts.ParseMode = "plain"
		return c.SendMessage(ctx, target, plain, plainOpts)
	}

	// 默认及 markdown 模式：1) 温和校验修复后以 markdown 发送 -> 2) 激进清理后发送 -> 3) 纯文本兜底
	mild := chunk
	if ok, _ := ValidateMessageEntities(mild); !ok {
		mild = SanitizeMarkdown(mild, false)
	}
	mdOpts := opts
	mdOpts.ParseMode = "markdown"
	id, err := c.SendMessage(ctx, target, mild, mdOpts)
	if err == nil {
		return id, nil
	}
	if !isEntityFormatRPCError(err) {
		return 0, err
	}

	aggressive := SanitizeMarkdown(chunk, true)
	plainOpts := opts
	plainOpts.ParseMode = "plain"
	plainOpts.Entities = nil
	id, err2 := c.SendMessage(ctx, target, aggressive, plainOpts)
	if err2 == nil {
		return id, nil
	}
	if !isEntityFormatRPCError(err2) {
		return 0, err2
	}

	rawPlain := strings.NewReplacer("**", "", "__", "", "~~", "", "`", "", "*", "").Replace(chunk)
	return c.SendMessage(ctx, target, rawPlain, plainOpts)
}

func isEntityFormatRPCError(err error) bool {
	if err == nil {
		return false
	}
	if tgerr.Is(
		err,
		"ENTITY_BOUNDS_INVALID",
		"ENTITIES_TOO_LONG",
		"ENTITY_MENTION_USER_INVALID",
		"MESSAGE_EMPTY",
	) {
		return true
	}
	low := strings.ToLower(err.Error())
	return strings.Contains(low, "entity") || strings.Contains(low, "entities") || strings.Contains(low, "invalid bounds")
}

// EditMessage 编辑已发送消息的正文、实体或内联按钮（MESSAGE_NOT_MODIFIED 视为幂等成功）。
func (c *Client) EditMessage(ctx context.Context, target any, messageID int, text string, opts SendOptions) error {
	if c.noEmoji {
		text, opts = cleanMessageEmoji(text, opts)
	}
	api := c.API()
	if api == nil {
		return errors.New("mtproto client not initialized")
	}

	peer, err := c.Peers().ResolveInputPeer(ctx, api, target)
	if err != nil {
		return err
	}

	req := &tg.MessagesEditMessageRequest{
		NoWebpage: opts.NoWebpage,
		Peer:      peer,
		ID:        messageID,
	}
	if text != "" || opts.Buttons == nil {
		if c.richMessages {
			req.SetRichMessage(c.inputRichMessage(ctx, text, opts))
		} else {
			editText, entities := c.prepareTextAndEntities(ctx, text, opts.ParseMode, opts.Entities)
			req.SetMessage(editText)
			if len(entities) > 0 {
				req.SetEntities(entities)
			}
		}
	}
	if markup := BuildInlineMarkup(c.cleanButtons(opts.Buttons)); markup != nil {
		req.SetReplyMarkup(markup)
	} else if opts.Buttons != nil && !c.richMessages {
		req.SetReplyMarkup(&tg.ReplyInlineMarkup{})
	}
	upd, err := api.MessagesEditMessage(ctx, req)
	if c.richMessages && req.RichMessage != nil && isRichFormattingError(err) && isRichMarkupMode(opts) {
		req.SetRichMessage(plainRichMessage(richMarkupPlainText(text, opts.ParseMode)))
		upd, err = api.MessagesEditMessage(ctx, req)
	}
	if err != nil {
		if tgerr.Is(err, "MESSAGE_NOT_MODIFIED") {
			return nil
		}
		return err
	}
	c.Peers().ApplyUpdates(upd)
	return nil
}

// DeleteMessages 批量撤回/删除指定会话内的消息（自动区分超级群/频道与普通会话）。
func (c *Client) DeleteMessages(ctx context.Context, target any, messageIDs []int) error {
	if len(messageIDs) == 0 {
		return nil
	}
	api := c.API()
	if api == nil {
		return errors.New("mtproto client not initialized")
	}

	peer, err := c.Peers().ResolveInputPeer(ctx, api, target)
	if err != nil {
		return err
	}

	if chPeer, ok := peer.(*tg.InputPeerChannel); ok {
		_, err := api.ChannelsDeleteMessages(ctx, &tg.ChannelsDeleteMessagesRequest{
			Channel: &tg.InputChannel{
				ChannelID:  chPeer.ChannelID,
				AccessHash: chPeer.AccessHash,
			},
			ID: messageIDs,
		})
		return err
	}

	_, err = api.MessagesDeleteMessages(ctx, &tg.MessagesDeleteMessagesRequest{
		Revoke: true,
		ID:     messageIDs,
	})
	return err
}

// ForwardMessages 原生批量转发消息，返回目标会话内生成的新消息 ID 列表。
func (c *Client) ForwardMessages(ctx context.Context, fromPeer, toPeer any, messageIDs []int) ([]int, error) {
	if len(messageIDs) == 0 {
		return nil, nil
	}
	api := c.API()
	if api == nil {
		return nil, errors.New("mtproto client not initialized")
	}

	fromIn, err := c.Peers().ResolveInputPeer(ctx, api, fromPeer)
	if err != nil {
		return nil, fmt.Errorf("resolve fromPeer: %w", err)
	}
	toIn, err := c.Peers().ResolveInputPeer(ctx, api, toPeer)
	if err != nil {
		return nil, fmt.Errorf("resolve toPeer: %w", err)
	}

	randomIDs := make([]int64, len(messageIDs))
	for i := range randomIDs {
		randomIDs[i] = cryptoRandInt64()
	}

	upd, err := api.MessagesForwardMessages(ctx, &tg.MessagesForwardMessagesRequest{
		FromPeer: fromIn,
		ID:       messageIDs,
		RandomID: randomIDs,
		ToPeer:   toIn,
	})
	if err != nil {
		return nil, err
	}
	c.Peers().ApplyUpdates(upd)
	return extractMultipleSentMessageIDs(upd, randomIDs), nil
}

// PinMessage 置顶指定会话中的单条消息。
func (c *Client) PinMessage(ctx context.Context, target any, messageID int, silent bool) error {
	api := c.API()
	if api == nil {
		return errors.New("mtproto client not initialized")
	}
	peer, err := c.Peers().ResolveInputPeer(ctx, api, target)
	if err != nil {
		return err
	}
	upd, err := api.MessagesUpdatePinnedMessage(ctx, &tg.MessagesUpdatePinnedMessageRequest{
		Silent: silent,
		Peer:   peer,
		ID:     messageID,
	})
	if err != nil {
		return err
	}
	c.Peers().ApplyUpdates(upd)
	return nil
}

// SendPoll 发送原生 Telegram 投票（tg.InputMediaPoll），支持内联按钮与回复目标（对齐 poll_handlers.py）。
func (c *Client) SendPoll(ctx context.Context, target any, opts PollSendOptions) (int, error) {
	api := c.API()
	if api == nil {
		return 0, errors.New("mtproto client not initialized")
	}
	if len(opts.Options) < 2 {
		return 0, errors.New("poll requires at least 2 options")
	}

	peer, err := c.Peers().ResolveInputPeer(ctx, api, target)
	if err != nil {
		return 0, err
	}

	publicVoters := opts.PublicVoters
	// 频道广播模式下不支持公开投票，强制转为匿名投票（对齐 poll_handlers.py:130-131）
	if chPeer, ok := peer.(*tg.InputPeerChannel); ok {
		if cp, exists := c.Peers().GetByMarkedID(ChannelMarkedID(chPeer.ChannelID)); exists && cp.Broadcast && !cp.Megagroup {
			publicVoters = false
		}
	}

	question := truncateRunes(strings.TrimSpace(opts.Question), 250)
	if c.noEmoji {
		question = strings.TrimSpace(StripEmoji(question))
		if question == "" {
			question = "Poll"
		}
	}
	maxOpts := min(len(opts.Options), 10)
	answers := make([]tg.PollAnswerClass, 0, maxOpts)
	for i := 0; i < maxOpts; i++ {
		optClean := truncateRunes(strings.TrimSpace(opts.Options[i]), 100)
		if c.noEmoji {
			optClean = strings.TrimSpace(StripEmoji(optClean))
			if optClean == "" {
				optClean = fmt.Sprintf("%d", i+1)
			}
		}
		answers = append(answers, &tg.PollAnswer{
			Text:   tg.TextWithEntities{Text: optClean},
			Option: []byte{byte(i)},
		})
	}

	pollMedia := &tg.InputMediaPoll{
		Poll: tg.Poll{
			ID:             0,
			Question:       tg.TextWithEntities{Text: question},
			Answers:        answers,
			PublicVoters:   publicVoters,
			MultipleChoice: opts.MultipleChoice,
		},
	}

	randomID := cryptoRandInt64()
	req := &tg.MessagesSendMediaRequest{
		Peer:     peer,
		Media:    pollMedia,
		RandomID: randomID,
	}
	if opts.ReplyToMsgID > 0 {
		req.SetReplyTo(&tg.InputReplyToMessage{ReplyToMsgID: opts.ReplyToMsgID})
	}
	if markup := BuildInlineMarkup(c.cleanButtons(opts.Buttons)); markup != nil {
		req.SetReplyMarkup(markup)
	}

	upd, err := api.MessagesSendMedia(ctx, req)
	if err != nil {
		return 0, err
	}
	c.Peers().ApplyUpdates(upd)
	return extractSentMessageID(upd, randomID), nil
}

// AnswerCallback 响应内联键盘按钮回调查询。
func (c *Client) AnswerCallback(ctx context.Context, queryID int64, message string, alert bool) error {
	if c.noEmoji {
		message = strings.TrimSpace(StripEmoji(message))
	}
	api := c.API()
	if api == nil {
		return errors.New("mtproto client not initialized")
	}
	req := &tg.MessagesSetBotCallbackAnswerRequest{
		Alert:   alert,
		QueryID: queryID,
	}
	if message != "" {
		req.SetMessage(message)
	}
	_, err := api.MessagesSetBotCallbackAnswer(ctx, req)
	return err
}

// SetBotCommands 注册客户端菜单中的斜杠命令列表（bots.setBotCommands）。
func (c *Client) SetBotCommands(ctx context.Context, langCode string, commands []BotCommandDef) error {
	api := c.API()
	if api == nil {
		return errors.New("mtproto client not initialized")
	}

	tgCmds := make([]tg.BotCommand, 0, len(commands))
	for _, cmd := range commands {
		name := strings.TrimPrefix(strings.TrimSpace(cmd.Command), "/")
		if name == "" {
			continue
		}
		tgCmds = append(tgCmds, tg.BotCommand{
			Command:     name,
			Description: strings.TrimSpace(cmd.Description),
		})
	}

	_, err := api.BotsSetBotCommands(ctx, &tg.BotsSetBotCommandsRequest{
		Scope:    &tg.BotCommandScopeDefault{},
		LangCode: strings.TrimSpace(langCode),
		Commands: tgCmds,
	})
	return err
}

func truncateRunes(s string, maxRunes int) string {
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	return string(r[:maxRunes])
}

func extractSentMessageID(u tg.UpdatesClass, randomID int64) int {
	switch v := u.(type) {
	case *tg.UpdateShortSentMessage:
		return v.ID
	case *tg.Updates:
		return extractMessageIDFromUpdateSlice(v.Updates, randomID)
	case *tg.UpdatesCombined:
		return extractMessageIDFromUpdateSlice(v.Updates, randomID)
	case *tg.UpdateShort:
		return extractMessageIDFromUpdateSlice([]tg.UpdateClass{v.Update}, randomID)
	}
	return 0
}

func extractMessageIDFromUpdateSlice(upds []tg.UpdateClass, randomID int64) int {
	if randomID != 0 {
		for _, upd := range upds {
			if mid, ok := upd.(*tg.UpdateMessageID); ok && mid.RandomID == randomID && mid.ID > 0 {
				return mid.ID
			}
		}
	}
	for _, upd := range upds {
		switch item := upd.(type) {
		case *tg.UpdateNewMessage:
			if id := messageClassID(item.Message); id > 0 {
				return id
			}
		case *tg.UpdateNewChannelMessage:
			if id := messageClassID(item.Message); id > 0 {
				return id
			}
		case *tg.UpdateEditMessage:
			if id := messageClassID(item.Message); id > 0 {
				return id
			}
		case *tg.UpdateEditChannelMessage:
			if id := messageClassID(item.Message); id > 0 {
				return id
			}
		}
	}
	for _, upd := range upds {
		if mid, ok := upd.(*tg.UpdateMessageID); ok && mid.ID > 0 {
			return mid.ID
		}
	}
	return 0
}

func extractMultipleSentMessageIDs(u tg.UpdatesClass, randomIDs []int64) []int {
	var upds []tg.UpdateClass
	switch v := u.(type) {
	case *tg.Updates:
		upds = v.Updates
	case *tg.UpdatesCombined:
		upds = v.Updates
	case *tg.UpdateShortSentMessage:
		if v.ID > 0 {
			return []int{v.ID}
		}
		return nil
	}

	byRandom := make(map[int64]int, len(randomIDs))
	var msgIDs []int
	for _, upd := range upds {
		switch item := upd.(type) {
		case *tg.UpdateMessageID:
			if item.ID > 0 {
				byRandom[item.RandomID] = item.ID
			}
		case *tg.UpdateNewMessage:
			if id := messageClassID(item.Message); id > 0 {
				msgIDs = append(msgIDs, id)
			}
		case *tg.UpdateNewChannelMessage:
			if id := messageClassID(item.Message); id > 0 {
				msgIDs = append(msgIDs, id)
			}
		}
	}

	if len(byRandom) == len(randomIDs) && len(randomIDs) > 0 {
		ordered := make([]int, 0, len(randomIDs))
		for _, rid := range randomIDs {
			if id, ok := byRandom[rid]; ok {
				ordered = append(ordered, id)
			}
		}
		if len(ordered) == len(randomIDs) {
			return ordered
		}
	}
	return msgIDs
}

func messageClassID(mc tg.MessageClass) int {
	switch m := mc.(type) {
	case *tg.Message:
		return m.ID
	case *tg.MessageService:
		return m.ID
	default:
		return 0
	}
}
