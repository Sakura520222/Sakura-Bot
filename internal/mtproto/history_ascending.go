// Copyright 2026 Sakura-Bot
// 本项目采用 AGPL-3.0 许可
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot

package mtproto

import (
	"cmp"
	"context"
	"errors"
	"math"
	"slices"
	"time"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

// WalkChannelHistoryAfter streams a channel's history in ascending ID order.
// The persisted cursor can therefore resume after each successfully handled message.
func (c *Client) WalkChannelHistoryAfter(ctx context.Context, channelRef string, start time.Time, afterID int, visit func(*IncomingMessage) error) error {
	if afterID < 0 || afterID > math.MaxInt32 || visit == nil {
		return errors.New("invalid Telegram history cursor")
	}
	api := c.API()
	if api == nil {
		return errors.New("mtproto client not initialized")
	}
	peers := c.Peers()
	peer, err := peers.ResolveInputPeer(ctx, api, channelRef)
	if err != nil {
		return err
	}
	offsetID := afterID + 1
	var offsetDate int
	if afterID == 0 && !start.IsZero() {
		// Telegram gives ID offsets precedence over dates. The first request can use the date.
		offsetID = 0
		offsetDate = int(start.Unix())
	}
	for afterID < math.MaxInt32 {
		// A negative offset walks toward newer messages from the exclusive lower ID.
		res, err := api.MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{
			Peer: peer, OffsetID: offsetID, OffsetDate: offsetDate, AddOffset: -historyPageLimit, Limit: historyPageLimit,
		})
		if delay, ok := tgerr.AsFloodWait(err); ok {
			return &FloodWaitError{Seconds: int(delay / time.Second), Cause: err}
		}
		if err != nil {
			return err
		}
		messages, users, chats := unpackMessagesClass(res)
		if len(messages) == 0 {
			return nil
		}
		peers.ApplyUsersChats(users, chats)
		entities := tg.EntitiesFrom(users, chats)
		slices.SortFunc(messages, func(a, b tg.MessageClass) int {
			return cmp.Compare(messageClassID(a), messageClassID(b))
		})
		lastID := afterID
		for _, raw := range messages {
			if err := ctx.Err(); err != nil {
				return err
			}
			id := messageClassID(raw)
			if id <= lastID {
				continue
			}
			lastID = id
			m, ok := raw.(*tg.Message)
			if !ok || m == nil || (!start.IsZero() && time.Unix(int64(m.Date), 0).Before(start)) {
				continue
			}
			if err := visit(convertTGMessage(m, entities, peers, c.SelfID())); err != nil {
				return err
			}
		}
		if lastID == afterID {
			return nil
		}
		if _, complete := res.(*tg.MessagesMessages); complete {
			return nil
		}
		afterID, offsetID, offsetDate = lastID, lastID+1, 0
	}
	return nil
}
