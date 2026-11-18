// Copyright 2026 Sakura-Bot
// SPDX-License-Identifier: AGPL-3.0-or-later

package mainbot

import (
	"context"
	"log/slog"
	"strings"

	"github.com/Sakura520222/Sakura-Bot/internal/botcmd"
	"github.com/Sakura520222/Sakura-Bot/internal/forwarding"
	"github.com/Sakura520222/Sakura-Bot/internal/mtproto"
	"github.com/Sakura520222/Sakura-Bot/internal/poll"
	"github.com/Sakura520222/Sakura-Bot/internal/rag"
	"github.com/Sakura520222/Sakura-Bot/internal/submission"
	"github.com/Sakura520222/Sakura-Bot/internal/welcome"
	"github.com/Sakura520222/Sakura-Bot/internal/wiki"
	"golang.org/x/sync/errgroup"
)

const eventShards = 8

type eventQueue struct {
	ctx   context.Context
	lanes [eventShards]chan func(context.Context)
}

func newEventQueue(ctx context.Context, group *errgroup.Group, logger *slog.Logger) *eventQueue {
	q := &eventQueue{ctx: ctx}
	for i := range q.lanes {
		lane := make(chan func(context.Context), 32)
		q.lanes[i] = lane
		group.Go(func() error {
			for ctx.Err() == nil {
				select {
				case <-ctx.Done():
					return nil
				case job := <-lane:
					func() {
						defer func() {
							if err := recover(); err != nil {
								logger.Error("处理 Telegram 事件时发生 panic", "panic_type", slog.AnyValue(err).Kind())
							}
						}()
						job(ctx)
					}()
				}
			}
			return nil
		})
	}
	return q
}

// submit preserves event order within a peer and applies backpressure to the
// Telegram transport when a shard is full.
func (q *eventQueue) submit(eventCtx context.Context, peerID int64, job func(context.Context)) {
	select {
	case q.lanes[uint64(peerID)%eventShards] <- job:
	case <-q.ctx.Done():
	case <-eventCtx.Done():
	}
}

func bindMessages(
	queue *eventQueue, bot, user *mtproto.Dispatcher, userClient func() mtproto.Messenger,
	router *botcmd.Router, ragHandler *rag.Handler, forward *forwarding.Service,
	polls *poll.Service, welcomes *welcome.Handler, wikiRuntime *wiki.Runtime,
) {
	bot.OnNewMessage(func(ctx context.Context, msg *mtproto.IncomingMessage) {
		if msg == nil {
			return
		}
		// Poll senders may be waiting for this exact discussion update.
		polls.NotifyDiscussionMessage(msg)
		queue.submit(ctx, msg.ChatID, func(ctx context.Context) {
			if !wikiRuntime.HandleCommand(ctx, msg) {
				router.HandleMessage(ctx, msg)
			}
			welcomes.HandleDiscussionMessage(ctx, msg)
			if userClient() == nil {
				ragHandler.HandleIncomingMessage(ctx, msg)
				forward.HandleIncomingMessage(ctx, msg)
			}
		})
	})
	bot.OnMessageEdited(func(ctx context.Context, msg *mtproto.IncomingMessage) {
		if msg != nil && userClient() == nil {
			queue.submit(ctx, msg.ChatID, func(ctx context.Context) { ragHandler.HandleIncomingMessage(ctx, msg) })
		}
	})
	bot.OnMessageDeleted(func(ctx context.Context, ev mtproto.DeletedMessagesEvent) {
		if userClient() == nil {
			queue.submit(ctx, ev.ChatID, func(ctx context.Context) { ragHandler.HandleDeletedEvent(ctx, ev) })
		}
	})
	if user == nil {
		return
	}
	user.OnNewMessage(func(ctx context.Context, msg *mtproto.IncomingMessage) {
		if msg != nil {
			queue.submit(ctx, msg.ChatID, func(ctx context.Context) {
				ragHandler.HandleIncomingMessage(ctx, msg)
				forward.HandleIncomingMessage(ctx, msg)
				wikiRuntime.HandleMessage(ctx, msg)
			})
		}
	})
	user.OnMessageEdited(func(ctx context.Context, msg *mtproto.IncomingMessage) {
		if msg != nil {
			queue.submit(ctx, msg.ChatID, func(ctx context.Context) {
				ragHandler.HandleIncomingMessage(ctx, msg)
				wikiRuntime.HandleMessage(ctx, msg)
			})
		}
	})
	user.OnMessageDeleted(func(ctx context.Context, ev mtproto.DeletedMessagesEvent) {
		queue.submit(ctx, ev.ChatID, func(ctx context.Context) {
			ragHandler.HandleDeletedEvent(ctx, ev)
			wikiRuntime.HandleDelete(ctx, ev)
		})
	})
}

func bindCallbacks(
	queue *eventQueue, dispatcher *mtproto.Dispatcher, bot mtproto.Messenger,
	router *botcmd.Router, welcomes *welcome.Handler, polls *poll.Service,
	requests *submission.RequestService, reviews *submission.ReviewService, wikiRuntime *wiki.Runtime,
) {
	dispatcher.OnCallbackQuery(func(ctx context.Context, ev mtproto.CallbackEvent) {
		data := ev.DataString()
		switch {
		case strings.HasPrefix(data, "sg:"):
			// Guard owns its own callback registration.
			return
		case strings.HasPrefix(data, botcmd.MenuCallbackPrefix+":"):
			queue.submit(ctx, ev.ChatID, func(ctx context.Context) { _ = router.HandleMenuCallback(ctx, ev) })
		case strings.HasPrefix(data, "req_summary:"):
			queue.submit(ctx, ev.ChatID, func(ctx context.Context) { _ = welcomes.HandleSummaryRequestCallback(ctx, ev) })
		case strings.HasPrefix(data, "request_regen_"):
			queue.submit(ctx, ev.ChatID, func(ctx context.Context) { _ = polls.HandleVoteRegenRequestCallback(ctx, bot, ev) })
		case strings.HasPrefix(data, "regen_poll_"):
			queue.submit(ctx, ev.ChatID, func(ctx context.Context) { _ = polls.HandleAdminRegenCallback(ctx, bot, ev) })
		case strings.HasPrefix(data, "confirm_summary_"), strings.HasPrefix(data, "reject_summary_"):
			queue.submit(ctx, ev.ChatID, func(ctx context.Context) { _ = requests.HandleCallback(ctx, ev) })
		case strings.HasPrefix(data, "submission_"):
			queue.submit(ctx, ev.ChatID, func(ctx context.Context) { _ = reviews.HandleCallback(ctx, ev) })
		default:
			queue.submit(ctx, ev.ChatID, func(ctx context.Context) { wikiRuntime.HandleCallback(ctx, ev) })
		}
	})
}
