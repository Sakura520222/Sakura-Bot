// Copyright 2026 Sakura-Bot
// 本项目采用 AGPL-3.0 许可
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot

package mtproto

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
)

func TestWalkChannelHistoryAfterResumesAcrossShortPages(t *testing.T) {
	const channelID int64 = 1234567890
	const ref = "-1001234567890"
	start := time.Unix(1700000000, 0).UTC()
	channel := &tg.Channel{ID: channelID, Broadcast: true, Title: "Wiki History", Photo: &tg.ChatPhotoEmpty{}}
	channel.SetAccessHash(1234)

	mockMessage := func(id int, date time.Time) *tg.Message {
		return &tg.Message{ID: id, Date: int(date.Unix()), PeerID: &tg.PeerChannel{ChannelID: channelID}, Message: "test"}
	}
	for _, test := range []struct {
		name        string
		since       time.Time
		after       int
		firstOffset int
		firstDate   int
		want        []int
	}{
		{"initial date bound", start, 0, 0, int(start.Unix()), []int{9, 12, 14, 15}},
		{"resume by last committed ID", time.Time{}, 10, 11, 0, []int{12, 14, 15}},
	} {
		t.Run(test.name, func(t *testing.T) {
			page := 0
			client := NewClientFromInvoker(mockInvokerFunc(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
				req, ok := input.(*tg.MessagesGetHistoryRequest)
				if !ok {
					return fmt.Errorf("unexpected MTProto request %T", input)
				}
				page++
				if req.AddOffset != -historyPageLimit || req.Limit != historyPageLimit || req.MinID != 0 || req.MaxID != 0 {
					t.Fatalf("invalid ascending offset parameters: %+v", req)
				}
				var batch []tg.MessageClass
				switch page {
				case 1:
					if req.OffsetID != test.firstOffset || req.OffsetDate != test.firstDate {
						t.Fatalf("first request offset id/date = %d/%d", req.OffsetID, req.OffsetDate)
					}
					batch = []tg.MessageClass{mockMessage(12, start.Add(time.Minute)), mockMessage(9, start), mockMessage(8, start.Add(-time.Minute))}
				case 2:
					if req.OffsetID != 13 || req.OffsetDate != 0 {
						t.Fatalf("next request offset id/date = %d/%d", req.OffsetID, req.OffsetDate)
					}
					batch = []tg.MessageClass{mockMessage(15, start.Add(time.Hour)), mockMessage(14, start.Add(time.Hour)), mockMessage(12, start.Add(time.Minute))}
				case 3:
					if req.OffsetID != 16 || req.OffsetDate != 0 {
						t.Fatalf("next request offset id/date = %d/%d", req.OffsetID, req.OffsetDate)
					}
				default:
					t.Fatal("requested a page after receiving an empty result")
				}
				return encodeReply(output, &tg.MessagesChannelMessages{Count: 16, Messages: batch, Chats: []tg.ChatClass{channel}})
			}), WithSelf(50, "userbot"))
			client.Peers().ApplyUsersChats(nil, []tg.ChatClass{channel})
			var got []int
			err := client.WalkChannelHistoryAfter(t.Context(), ref, test.since, test.after, func(m *IncomingMessage) error {
				got = append(got, m.ID)
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got, test.want) || page != 3 {
				t.Fatalf("visited %v across %d pages, want %v across 3 pages", got, page, test.want)
			}
		})
	}
}
