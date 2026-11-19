// Copyright 2026 Sakura-Bot
// SPDX-License-Identifier: AGPL-3.0-or-later

package mtproto

import (
	"context"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

func TestNativeRichMessagesKeepReplyButtonsAndStableID(t *testing.T) {
	symbol := 0
	var firstID int64
	invoker := mockInvokerFunc(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
		symbol++
		switch req := input.(type) {
		case *tg.MessagesSendMessageRequest:
			if req.Message != "" || len(req.Entities) != 0 || req.RichMessage == nil {
				t.Fatalf("expected native Rich Message, got %+v", req)
			}
			reply, ok := req.ReplyTo.(*tg.InputReplyToMessage)
			if !ok || reply.ReplyToMsgID != 55 || req.ReplyMarkup == nil || !req.Silent {
				t.Fatalf("missing reply or buttons: %+v", req)
			}
			if symbol == 1 {
				md, ok := req.RichMessage.(*tg.InputRichMessageMarkdown)
				if !ok || md.Markdown != "**正文**" {
					t.Fatalf("unexpected Markdown payload: %+v", req.RichMessage)
				}
				firstID = req.RandomID
				return tgerr.New(400, "RICH_MESSAGE_MARKDOWN_INVALID")
			}
			plain, ok := req.RichMessage.(*tg.InputRichMessage)
			if !ok || len(plain.Blocks) != 1 || req.RandomID != firstID {
				t.Fatalf("retry changed message kind or ID: %+v", req)
			}
			return encodeReply(output, &tg.UpdateShortSentMessage{ID: 22})
		case *tg.MessagesEditMessageRequest:
			if req.ID != 22 || (req.ReplyMarkup == nil) != (symbol == 5) {
				t.Fatalf("unexpected edit: %+v", req)
			}
			if symbol != 4 {
				if req.RichMessage == nil || len(req.Entities) != 0 || req.Message != "" {
					t.Fatalf("edit bypassed rich transport: %+v", req)
				}
			} else if _, hasText := req.GetMessage(); hasText || req.RichMessage != nil {
				t.Fatalf("markup-only edit sent new content: %+v", req)
			}
			return encodeReply(output, &tg.Updates{})
		default:
			t.Fatalf("unexpected RPC %T", input)
			return nil
		}
	})
	client := NewClientFromInvoker(invoker, WithRichMessages())
	if err := client.Peers().Put(CachedPeer{MarkedID: 321, RawID: 321, Kind: PeerKindUser, AccessHash: 123, HasAccessHash: true}); err != nil {
		t.Fatal(err)
	}
	buttons := [][]InlineButton{{{Text: "管理", Data: "test"}}}
	actions := NewGuardActions(client)
	id, err := actions.SendOnce(t.Context(), 321, "**正文**", SendOptions{
		ParseMode: "markdown", Buttons: buttons, ReplyToMsgID: 55, Silent: true,
	}, 77)
	if err != nil || id != 22 || firstID != 77 {
		t.Fatalf("unexpected send: id=%d err=%v randomID=%d", id, err, firstID)
	}
	if err := client.EditMessage(t.Context(), int64(321), id, "<b>更新</b>\n详情", SendOptions{ParseMode: "html", Buttons: buttons}); err != nil {
		t.Fatal(err)
	}
	if err := client.EditMessage(t.Context(), int64(321), id, "", SendOptions{Buttons: buttons}); err != nil {
		t.Fatal(err)
	}
	if err := client.EditMessage(t.Context(), int64(321), id, "已完成", SendOptions{Buttons: [][]InlineButton{}}); err != nil {
		t.Fatal(err)
	}
	if symbol != 5 {
		t.Fatalf("expected five native RPC calls, got %d", symbol)
	}
}

func TestRichTextPreservesClassicHTMLLineBreaks(t *testing.T) {
	client := NewClient()
	rich := client.inputRichMessage(t.Context(), "<b>通知</b>\n详情", SendOptions{ParseMode: "html"}).(*tg.InputRichMessage)
	block := rich.Blocks[0].(*tg.PageBlockParagraph)
	parts := block.Text.(*tg.TextConcat).Texts
	if len(parts) != 2 || parts[0].(*tg.TextBold).Text.(*tg.TextPlain).Text != "通知" ||
		parts[1].(*tg.TextPlain).Text != "\n详情" {
		t.Fatalf("unexpected rich text layout: %+v", parts)
	}
}

func TestRichHTMLUserLinkBecomesNativeProfileMention(t *testing.T) {
	client := NewClient()
	if err := client.Peers().Put(CachedPeer{MarkedID: 12345, RawID: 12345, Kind: PeerKindUser, AccessHash: 456, HasAccessHash: true}); err != nil {
		t.Fatal(err)
	}
	message := client.inputRichMessage(t.Context(), `用户：<a href="tg://user?id=12345">示例 &amp; 成员</a>`, SendOptions{ParseMode: "html"}).(*tg.InputRichMessage)
	if len(message.Users) != 1 || message.Users[0].(*tg.InputUser).AccessHash != 456 {
		t.Fatalf("missing profile reference: %+v", message.Users)
	}
	parts := message.Blocks[0].(*tg.PageBlockParagraph).Text.(*tg.TextConcat).Texts
	name, ok := parts[1].(*tg.TextMentionName)
	if !ok || name.UserID != 12345 || name.Text.(*tg.TextPlain).Text != "示例 & 成员" {
		t.Fatalf("missing profile mention: %+v", parts)
	}
}

func TestStripEmojiPreservesLinksAndPlainDigits(t *testing.T) {
	text := "👩🏽‍💻链接𠀀1️⃣ 🇨🇳 #1*️"
	spans := []EntitySpan{{Offset: 7, Length: 4, Kind: "text_url", URL: "https://example.test"}}
	cleaned, got := stripEmojiSpans(text, spans)
	if cleaned != "链接𠀀  #1*" || len(got) != 1 || got[0].Offset != 0 || got[0].Length != 4 || got[0].URL != spans[0].URL {
		t.Fatalf("unexpected sanitized text: %q %+v", cleaned, got)
	}
}
