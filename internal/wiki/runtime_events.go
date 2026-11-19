// Copyright 2026 Sakura-Bot
// 本项目采用 AGPL-3.0 许可
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot

package wiki

import (
	"context"
	"errors"
	"maps"
	"strconv"
	"sync"
	"time"

	"github.com/Sakura520222/Sakura-Bot/internal/mtproto"
)

// HandleMessage captures channel posts and edits from the UserBot.
func (r *Runtime) HandleMessage(ctx context.Context, msg *mtproto.IncomingMessage) {
	if msg == nil || msg.ID <= 0 || !(msg.IsBroadcast || msg.IsSupergroup) {
		return
	}
	id, err := CanonicalChannelID(strconv.FormatInt(msg.ChatID, 10))
	if err != nil || !r.isManagedChannel(id, msg.ChatUsername) {
		return
	}
	mediaKey := extractStaticMediaKey(msg)
	category := "text"
	if mediaKey != "" {
		category = "image"
	}
	var group string
	if msg.GroupedID != 0 {
		group = strconv.FormatInt(msg.GroupedID, 10)
	}
	var editedAt *string
	if msg.EditDate != nil {
		edited := FormatISO(*msg.EditDate)
		editedAt = &edited
	}
	r.captureEvent(ctx, OutboxEvent{
		Kind:       "message",
		ChannelID:  id,
		MessageID:  int64(msg.ID),
		MediaKey:   mediaKey,
		MediaGroup: group,
		Category:   category,
		Body:       msg.Text,
		CreatedAt:  FormatISO(msg.Date),
		EditedAt:   editedAt,
	}, msg)
}

// HandleDelete revokes known channel messages, even when the Wiki is disabled.
func (r *Runtime) HandleDelete(ctx context.Context, ev mtproto.DeletedMessagesEvent) {
	chatID := ev.ChannelID
	if chatID == 0 {
		chatID = ev.ChatID
	}
	id, err := CanonicalChannelID(strconv.FormatInt(chatID, 10))
	if err != nil {
		return
	}
	if !r.isManagedChannel(id, "") {
		// A delete may precede cache warm-up or race an API-side config update.
		// Preserve the event if MySQL is unavailable and reconcile it on recovery.
		if r.Store == nil || r.Store.DB == nil {
			return
		}
		if ch, err := r.Store.GetChannel(ctx, id); err == nil && ch == nil {
			return
		}
	}
	ids := make([]int64, 0, len(ev.MessageIDs))
	for _, messageID := range ev.MessageIDs {
		if messageID > 0 {
			ids = append(ids, int64(messageID))
		}
	}
	if len(ids) == 0 {
		return
	}
	r.captureEvent(ctx, OutboxEvent{Kind: "delete", ChannelID: id, IDs: ids}, nil)
}

func (r *Runtime) isManagedChannel(id, username string) bool {
	r.stateMu.RLock()
	_, known := r.known[id]
	r.stateMu.RUnlock()
	if known {
		return true
	}
	if r.cfg != nil {
		for _, configured := range r.cfg.Channels() {
			if alias, err := AliasKey(configured); err == nil && (alias == id || equalChannelAlias(alias, username)) {
				return true
			}
		}
	}
	return false
}

func equalChannelAlias(a, b string) bool {
	key, err := AliasKey(b)
	return err == nil && a == key
}

func (r *Runtime) channelEventLock(id string) *sync.Mutex {
	n, _ := strconv.ParseUint(id, 10, 64)
	return &r.eventMu[n%uint64(len(r.eventMu))]
}

func (r *Runtime) captureEvent(ctx context.Context, event OutboxEvent, msg *mtproto.IncomingMessage) {
	lock := r.channelEventLock(event.ChannelID)
	lock.Lock()
	defer lock.Unlock()

	key, err := r.outbox.Put(event)
	if err != nil && r.Ready() {
		// A temporary disk error need not discard a live event while MySQL remains available.
		writeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		writeErr := r.applyEvent(writeCtx, event, msg)
		cancel()
		if writeErr == nil {
			r.logger.Warn("Wiki 实时事件已直写数据库，本地缓冲暂不可用", "channel", event.ChannelID, "error", err)
			return
		}
		err = errors.Join(err, writeErr)
	}
	if err == nil {
		if r.Ready() {
			ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			if err = r.applyEvent(ctx, event, msg); err == nil {
				err = r.outbox.Acknowledge(key)
			}
			if err == nil {
				r.removeRecoveryKey(key)
				return
			}
		}
		r.stateMu.Lock()
		r.recovery[key] = true
		r.stateMu.Unlock()
		r.Wake()
	}
	if err != nil {
		r.logger.Warn("Wiki 实时事件写入延后", "channel", event.ChannelID, "kind", event.Kind, "error", err)
	}
}

func (r *Runtime) removeRecoveryKey(key string) {
	r.stateMu.Lock()
	delete(r.recovery, key)
	r.stateMu.Unlock()
}

func (r *Runtime) flushOutbox(ctx context.Context) (bool, error) {
	r.stateMu.RLock()
	keys := maps.Clone(r.recovery)
	r.stateMu.RUnlock()
	if len(keys) == 0 {
		return true, nil
	}
	// Public access remains revoked until the pending changes have been reconciled.
	if _, err := r.Store.DB.ExecContext(ctx, `
		UPDATE wiki_channels SET access_verified=FALSE, verified_until=NULL
		WHERE enabled=TRUE AND (access_verified=TRUE OR verified_until IS NOT NULL)
	`); err != nil {
		return false, err
	}
	r.lastRefresh = time.Time{}
	pending, err := r.outbox.Pending(100, keys)
	if err != nil {
		return false, err
	}
	if len(pending) == 0 {
		onDisk, err := r.outbox.Keys()
		if err != nil {
			return false, err
		}
		for key := range keys {
			if !onDisk[key] {
				r.removeRecoveryKey(key)
			}
		}
		if r.hasRecovery() {
			return false, errors.New("Wiki 实时事件恢复文件不可读取")
		}
		return true, nil
	}
	for _, item := range pending {
		lock := r.channelEventLock(item.Event.ChannelID)
		lock.Lock()
		r.stateMu.RLock()
		stillPending := r.recovery[item.Key]
		r.stateMu.RUnlock()
		if stillPending {
			replay, cancel := context.WithTimeout(ctx, 15*time.Second)
			err = r.applyEvent(replay, item.Event, nil)
			cancel()
			if err == nil {
				err = r.outbox.Acknowledge(item.Key)
			}
			if err == nil {
				r.removeRecoveryKey(item.Key)
			}
		}
		lock.Unlock()
		if err != nil {
			if errors.Is(err, errWikiUserBotOffline) {
				return false, nil
			}
			return false, err
		}
	}
	if r.hasRecovery() {
		r.Wake()
		return false, nil
	}
	return true, nil
}

func (r *Runtime) hasRecovery() bool {
	r.stateMu.RLock()
	defer r.stateMu.RUnlock()
	return len(r.recovery) > 0
}

var errWikiUserBotOffline = errors.New("Wiki UserBot is offline")

func (r *Runtime) applyEvent(ctx context.Context, event OutboxEvent, msg *mtproto.IncomingMessage) error {
	ch, err := r.Store.GetChannel(ctx, event.ChannelID)
	if err != nil || ch == nil {
		return err
	}
	switch event.Kind {
	case "delete":
		return r.Collector.CaptureDelete(ctx, ch.ID, event.IDs)
	case "message", "upsert":
		if !ch.Enabled {
			return nil
		}
		if msg == nil {
			// Fetch the current Telegram state to preserve access and exclusion policies
			// even when this event was written by an older release.
			user := r.userBot()
			if user == nil || !user.IsConnected() {
				return errWikiUserBotOffline
			}
			found, err := user.GetMessages(ctx, "-100"+ch.ID, []int{int(event.MessageID)})
			if err != nil {
				return err
			}
			for _, message := range found {
				if message != nil && int64(message.ID) == event.MessageID && !message.Date.IsZero() {
					msg = message
					break
				}
			}
			if msg == nil {
				return r.Collector.CaptureDelete(ctx, ch.ID, []int64{event.MessageID})
			}
		}
		_, _, err = r.Collector.CaptureMessage(ctx, ch, msg)
		return err
	default:
		return errors.New("未知的 Wiki 实时事件类型")
	}
}
