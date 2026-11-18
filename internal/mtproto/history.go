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
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/gotd/td/tg"
)

const (
	historyPageLimit  = 100
	backfillPageLimit = 100
)

func unpackMessagesClass(res tg.MessagesMessagesClass) ([]tg.MessageClass, []tg.UserClass, []tg.ChatClass) {
	switch v := res.(type) {
	case *tg.MessagesMessages:
		return v.Messages, v.Users, v.Chats
	case *tg.MessagesMessagesSlice:
		return v.Messages, v.Users, v.Chats
	case *tg.MessagesChannelMessages:
		return v.Messages, v.Users, v.Chats
	default:
		return nil, nil, nil
	}
}

// GetMessages 按消息 ID 批量读取指定会话中的消息并转换为 []*IncomingMessage（自动过滤已删除空消息）。
func (c *Client) GetMessages(ctx context.Context, target any, messageIDs []int) ([]*IncomingMessage, error) {
	if len(messageIDs) == 0 {
		return nil, nil
	}
	api := c.API()
	if api == nil {
		return nil, errors.New("mtproto client not initialized")
	}

	peer, err := c.Peers().ResolveInputPeer(ctx, api, target)
	if err != nil {
		return nil, err
	}

	inputIDs := make([]tg.InputMessageClass, len(messageIDs))
	for i, id := range messageIDs {
		inputIDs[i] = &tg.InputMessageID{ID: id}
	}

	var res tg.MessagesMessagesClass
	if chPeer, ok := peer.(*tg.InputPeerChannel); ok {
		res, err = api.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{
			Channel: &tg.InputChannel{
				ChannelID:  chPeer.ChannelID,
				AccessHash: chPeer.AccessHash,
			},
			ID: inputIDs,
		})
	} else {
		res, err = api.MessagesGetMessages(ctx, inputIDs)
	}
	if err != nil {
		return nil, err
	}

	msgs, users, chats := unpackMessagesClass(res)
	c.Peers().ApplyUsersChats(users, chats)
	entities := tg.EntitiesFrom(users, chats)

	out := make([]*IncomingMessage, 0, len(msgs))
	for _, mc := range msgs {
		if m, ok := mc.(*tg.Message); ok && m != nil {
			out = append(out, convertTGMessage(m, entities, c.Peers(), c.SelfID()))
		}
	}
	return out, nil
}

// FetchChannelHistory 抓取指定频道自 startTime 起的历史消息（时间升序 reverse=True，跳过 excludeIDs）。
func (c *Client) FetchChannelHistory(
	ctx context.Context,
	channelRef string,
	startTime time.Time,
	excludeIDs map[int]bool,
) ([]*IncomingMessage, *ChannelInfo, error) {
	var collected []*IncomingMessage
	info, err := c.walkChannelHistory(ctx, channelRef, startTime, time.Time{}, excludeIDs, 0, func(msg *IncomingMessage) error {
		collected = append(collected, msg)
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	// 按消息 ID 与时间升序排列（对齐 Telethon iter_messages(..., reverse=True)）
	sort.Slice(collected, func(i, j int) bool {
		if collected[i].ID != collected[j].ID {
			return collected[i].ID < collected[j].ID
		}
		return collected[i].Date.Before(collected[j].Date)
	})
	return collected, info, nil
}

// WalkChannelHistoryRange visits at most limit messages in [start, end),
// newest first, without accumulating the history in memory.
func (c *Client) WalkChannelHistoryRange(ctx context.Context, channelRef string, start, end time.Time, limit int, visit func(*IncomingMessage) error) error {
	if start.IsZero() || end.IsZero() || !start.Before(end) || limit <= 0 || visit == nil {
		return errors.New("invalid Telegram history range")
	}
	_, err := c.walkChannelHistory(ctx, channelRef, start, end, nil, limit, visit)
	return err
}

func (c *Client) walkChannelHistory(
	ctx context.Context, channelRef string, startTime, endTime time.Time,
	excludeIDs map[int]bool, limit int, visit func(*IncomingMessage) error,
) (*ChannelInfo, error) {
	api := c.API()
	if api == nil {
		return nil, errors.New("mtproto client not initialized")
	}

	peer, err := c.Peers().ResolveInputPeer(ctx, api, channelRef)
	if err != nil {
		return nil, err
	}

	var startUnix int64
	if !startTime.IsZero() {
		startUnix = startTime.UTC().Unix()
	}

	var (
		offsetID int
		seen     = make(map[int]struct{})
		visited  int
	)

	for limit == 0 || visited < limit {
		pageLimit := historyPageLimit
		if limit > 0 {
			pageLimit = min(pageLimit, limit-visited)
		}
		var offsetDate int
		if offsetID == 0 && !endTime.IsZero() {
			// The API accepts whole seconds. Round up and filter exact bounds below.
			offsetDate = int(endTime.Add(time.Second - time.Nanosecond).Unix())
		}
		res, err := api.MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{
			Peer:       peer,
			OffsetID:   offsetID,
			OffsetDate: offsetDate,
			Limit:      pageLimit,
		})
		if err != nil {
			return nil, err
		}

		rawMsgs, users, chats := unpackMessagesClass(res)
		c.Peers().ApplyUsersChats(users, chats)
		if len(rawMsgs) == 0 {
			break
		}
		entities := tg.EntitiesFrom(users, chats)

		minID := 0
		reachedBeforeStart := false

		for _, mc := range rawMsgs {
			id := messageClassID(mc)
			if id > 0 && (minID == 0 || id < minID) {
				minID = id
			}
			m, ok := mc.(*tg.Message)
			if !ok || m == nil {
				continue
			}
			if startUnix > 0 && int64(m.Date) < startUnix {
				reachedBeforeStart = true
				continue
			}
			if !endTime.IsZero() {
				date := time.Unix(int64(m.Date), 0)
				if date.Before(startTime) {
					reachedBeforeStart = true
					continue
				}
				if !date.Before(endTime) {
					continue
				}
			}
			if excludeIDs != nil && excludeIDs[m.ID] {
				continue
			}
			if _, dup := seen[m.ID]; dup {
				continue
			}
			seen[m.ID] = struct{}{}
			if err := visit(convertTGMessage(m, entities, c.Peers(), c.SelfID())); err != nil {
				return nil, err
			}
			visited++
			if limit > 0 && visited >= limit {
				break
			}
		}

		if reachedBeforeStart || len(rawMsgs) < pageLimit || minID <= 0 {
			break
		}
		if offsetID > 0 && minID >= offsetID {
			break
		}
		offsetID = minID
	}

	return c.buildChannelInfoFromPeer(ctx, channelRef, peer), nil
}

func (c *Client) buildChannelInfoFromPeer(ctx context.Context, channelRef string, peer tg.InputPeerClass) *ChannelInfo {
	if chPeer, ok := peer.(*tg.InputPeerChannel); ok {
		marked := ChannelMarkedID(chPeer.ChannelID)
		if cp, exists := c.Peers().GetByMarkedID(marked); exists {
			return cp.ToChannelInfo(c.lookupCachedLinkedChat(channelRef, marked))
		}
		if resolved, err := c.ResolveChannel(ctx, channelRef); err == nil && resolved != nil {
			return resolved
		}
		return &ChannelInfo{
			ID:         chPeer.ChannelID,
			MarkedID:   marked,
			AccessHash: chPeer.AccessHash,
		}
	}
	if chatPeer, ok := peer.(*tg.InputPeerChat); ok {
		marked := ChatMarkedID(chatPeer.ChatID)
		if cp, exists := c.Peers().GetByMarkedID(marked); exists {
			return cp.ToChannelInfo(0)
		}
		return &ChannelInfo{
			ID:       chatPeer.ChatID,
			MarkedID: marked,
		}
	}
	return nil
}

// BackfillDiscussionComments 使用 UserBot 回溯频道帖子在关联讨论群中的根消息与可见评论历史
// （对齐 core/services/qa_comment_backfill.py:backfill_post）。
func (c *Client) BackfillDiscussionComments(
	ctx context.Context,
	channelRef string,
	postID int,
	limit int,
	assistantBotID int64,
) ([]BackfilledComment, bool, string, error) {
	if postID <= 0 {
		return nil, false, "", errors.New("postID must be positive")
	}
	if limit <= 0 {
		limit = 5000
	}

	api := c.API()
	if api == nil {
		return nil, false, "", errors.New("mtproto client not initialized")
	}

	channelPeer, err := c.Peers().ResolveInputPeer(ctx, api, channelRef)
	if err != nil {
		return nil, false, "", err
	}
	currentChannelID := inputPeerMarkedID(channelPeer)
	if currentChannelID == 0 {
		return nil, false, "", fmt.Errorf("cannot determine marked id for channel %q", channelRef)
	}

	disc, err := api.MessagesGetDiscussionMessage(ctx, &tg.MessagesGetDiscussionMessageRequest{
		Peer:  channelPeer,
		MsgID: postID,
	})
	if err != nil {
		return nil, false, "", err
	}
	c.Peers().ApplyUsersChats(disc.Users, disc.Chats)
	discEntities := tg.EntitiesFrom(disc.Users, disc.Chats)

	var candidates []*tg.Message
	for _, mc := range disc.Messages {
		m, ok := mc.(*tg.Message)
		if !ok || m == nil {
			continue
		}
		if matchesDiscussionPost(m, currentChannelID, postID) {
			candidates = append(candidates, m)
		}
	}
	if len(candidates) != 1 {
		return nil, false, "", errors.New("无法唯一识别该帖子的讨论根消息")
	}

	root := candidates[0]
	chatID := PeerMarkedID(root.PeerID)
	rootID := int64(root.ID)

	comments := make([]BackfilledComment, 0, min(limit+1, 128))
	comments = append(comments, buildBackfilledComment(root, chatID, rootID, channelRef, assistantBotID, discEntities, c.Peers()))

	var (
		offsetID  int
		seen      int
		truncated bool
		seenMsgID = map[int]struct{}{root.ID: {}}
	)

	for {
		batchSize := min(backfillPageLimit, (limit+1)-seen)
		if batchSize <= 0 {
			batchSize = 1
		}
		res, err := api.MessagesGetReplies(ctx, &tg.MessagesGetRepliesRequest{
			Peer:     channelPeer,
			MsgID:    postID,
			OffsetID: offsetID,
			Limit:    batchSize,
		})
		if err != nil {
			return nil, false, "", err
		}

		rawMsgs, users, chats := unpackMessagesClass(res)
		c.Peers().ApplyUsersChats(users, chats)
		if len(rawMsgs) == 0 {
			break
		}
		entities := tg.EntitiesFrom(users, chats)

		minID := 0
		for _, mc := range rawMsgs {
			id := messageClassID(mc)
			if id > 0 && (minID == 0 || id < minID) {
				minID = id
			}
			m, ok := mc.(*tg.Message)
			if !ok || m == nil {
				continue
			}
			if _, dup := seenMsgID[m.ID]; dup {
				continue
			}
			if seen == limit {
				truncated = true
				break
			}
			seenMsgID[m.ID] = struct{}{}
			comments = append(comments, buildBackfilledComment(m, chatID, rootID, channelRef, assistantBotID, entities, c.Peers()))
			seen++
		}

		if truncated || len(rawMsgs) < batchSize || minID <= 0 {
			break
		}
		if offsetID > 0 && minID >= offsetID {
			break
		}
		offsetID = minID
	}

	if truncated {
		return comments, true, "仅 UserBot 可见历史；已有实时记录保持不变", nil
	}
	return comments, false, "仅 UserBot 可见历史，不证明已删除或不可见评论完整", nil
}

func inputPeerMarkedID(peer tg.InputPeerClass) int64 {
	switch p := peer.(type) {
	case *tg.InputPeerChannel:
		return ChannelMarkedID(p.ChannelID)
	case *tg.InputPeerChat:
		return ChatMarkedID(p.ChatID)
	case *tg.InputPeerUser:
		return p.UserID
	default:
		return 0
	}
}

func matchesDiscussionPost(m *tg.Message, currentChannelID int64, postID int) bool {
	if PeerMarkedID(m.PeerID) == currentChannelID {
		return false
	}
	header, ok := m.GetFwdFrom()
	if !ok {
		return false
	}
	if savedPeer, hasSaved := header.GetSavedFromPeer(); hasSaved && savedPeer != nil {
		return PeerMarkedID(savedPeer) == currentChannelID && header.SavedFromMsgID == postID
	}
	if originPeer, hasOrigin := header.GetFromID(); hasOrigin && originPeer != nil {
		return PeerMarkedID(originPeer) == currentChannelID && header.ChannelPost == postID
	}
	return false
}

func buildBackfilledComment(
	m *tg.Message,
	chatID int64,
	rootID int64,
	channelRef string,
	assistantBotID int64,
	e tg.Entities,
	peers *PeerCache,
) BackfilledComment {
	var (
		userID     int64
		authorName string
		username   string
		role       = "user"
		replyID    int64
	)

	if rh, ok := m.GetReplyTo(); ok {
		if rhm, ok := rh.(*tg.MessageReplyHeader); ok {
			if rmid, ok := rhm.GetReplyToMsgID(); ok && rmid > 0 {
				replyID = int64(rmid)
			}
		}
	}

	if fromPeer, ok := m.GetFromID(); ok {
		switch fp := fromPeer.(type) {
		case *tg.PeerUser:
			userID = fp.UserID
			prof := resolveProfileSummary(fp.UserID, e, peers)
			authorName = joinNonEmptySpace(prof.FirstName, prof.LastName)
			username = prof.Username
			switch {
			case assistantBotID > 0 && fp.UserID == assistantBotID:
				role = "assistant"
			case prof.IsBot:
				role = "bot"
			default:
				role = "user"
			}
		case *tg.PeerChannel:
			chMarked := ChannelMarkedID(fp.ChannelID)
			if e.Channels != nil {
				if ch, ok := e.Channels[fp.ChannelID]; ok && ch != nil {
					authorName = ch.Title
					username = normalizeUsername(ch.Username)
				}
			}
			if authorName == "" && peers != nil {
				if cp, ok := peers.GetByMarkedID(chMarked); ok {
					authorName = cp.Title
					username = cp.Username
				}
			}
			if chMarked == chatID {
				role = "admin"
			} else {
				role = "channel"
			}
		case *tg.PeerChat:
			role = "admin"
			if e.Chats != nil {
				if ch, ok := e.Chats[fp.ChatID]; ok && ch != nil {
					authorName = ch.Title
				}
			}
		}
	} else if int64(m.ID) == rootID {
		role = "channel"
		if fh, ok := m.GetFwdFrom(); ok && fh.FromName != "" {
			authorName = fh.FromName
		}
	} else {
		role = "admin"
	}

	mediaLabel := ""
	if media, ok := m.GetMedia(); ok && media != nil {
		if _, isEmpty := media.(*tg.MessageMediaEmpty); !isEmpty {
			mediaLabel = strings.TrimPrefix(media.TypeName(), "messageMedia")
			if mediaLabel == "" {
				mediaLabel = media.TypeName()
			}
		}
	}

	_, edited := m.GetEditDate()

	return BackfilledComment{
		ChatID:      chatID,
		MessageID:   int64(m.ID),
		RootID:      rootID,
		ReplyID:     replyID,
		Channel:     channelRef,
		UserID:      userID,
		AuthorName:  authorName,
		Username:    username,
		AuthorRole:  role,
		Text:        m.Message,
		MediaLabel:  mediaLabel,
		MessageDate: time.Unix(int64(m.Date), 0).UTC(),
		Edited:      edited,
	}
}
