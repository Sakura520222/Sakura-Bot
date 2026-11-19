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
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

func TestConvertGuestCallerFromNativeTelegramPeer(t *testing.T) {
	msg := &tg.Message{ID: 32, PeerID: &tg.PeerChannel{ChannelID: 12345}}
	msg.SetFromID(&tg.PeerUser{UserID: 34})
	msg.SetViaBotID(35)
	for _, test := range []struct {
		name   string
		caller tg.PeerClass
		want   int64
	}{
		{"Telegram guest context", &tg.PeerUser{UserID: 9007199254740993}, 9007199254740993},
		{"same account as sender", &tg.PeerUser{UserID: 34}, 0},
		{"channel is not an account", &tg.PeerChannel{ChannelID: 42}, 0},
		{"inline bot alone is not a guest caller", nil, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			msg.SetGuestchatViaFrom(test.caller)
			if got := convertTGMessage(msg, tg.Entities{}, nil, 1).GuestCallerID; got != test.want {
				t.Fatalf("GuestCallerID=%d, want %d", got, test.want)
			}
		})
	}
}

func TestClientMessengerRPCsViaMockInvoker(t *testing.T) {
	ctx := context.Background()
	const (
		channelRawID    = int64(1234567890)
		channelMarkedID = int64(-1001234567890)
		discRawID       = int64(1987654321)
		discMarkedID    = int64(-1001987654321)
	)

	baseTime := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

	var (
		gotCommands  *tg.BotsSetBotCommandsRequest
		gotSendMsg   *tg.MessagesSendMessageRequest
		gotSendMedia *tg.MessagesSendMediaRequest
		sendAttempts int
	)

	invoker := mockInvokerFunc(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
		switch req := input.(type) {
		case *tg.BotsSetBotCommandsRequest:
			gotCommands = req
			var b tg.BoolTrue
			return encodeReply(output, &b)

		case *tg.MessagesSendMessageRequest:
			sendAttempts++
			gotSendMsg = req
			// 若文本包含特定触发词且附带实体，模拟首次 ENTITY_BOUNDS_INVALID 以验证 SendLongMessage 降级
			if strings.Contains(req.Message, "TRIGGER_ENTITY_ERR") && len(req.Entities) > 0 {
				return tgerr.New(400, "ENTITY_BOUNDS_INVALID")
			}
			upd := &tg.Updates{
				Updates: []tg.UpdateClass{
					&tg.UpdateMessageID{ID: 1000 + sendAttempts, RandomID: req.RandomID},
				},
			}
			return encodeReply(output, upd)

		case *tg.MessagesSendMediaRequest:
			gotSendMedia = req
			upd := &tg.Updates{
				Updates: []tg.UpdateClass{
					&tg.UpdateMessageID{ID: 2048, RandomID: req.RandomID},
				},
			}
			return encodeReply(output, upd)

		case *tg.MessagesGetHistoryRequest:
			ch := &tg.Channel{
				ID:        channelRawID,
				Title:     "Sakura Channel",
				Username:  "sakura_channel",
				Photo:     &tg.ChatPhotoEmpty{},
				Broadcast: true,
			}
			ch.SetAccessHash(999111222)

			// 模拟 MTProto 默认按消息 ID 倒序返回（含早于 startTime 的旧消息、需排除的报告消息、以及正常消息）
			msgs := []tg.MessageClass{
				&tg.Message{
					ID:      104,
					PeerID:  &tg.PeerChannel{ChannelID: channelRawID},
					Date:    int(baseTime.Add(3 * time.Hour).Unix()),
					Message: "最新消息 104",
				},
				&tg.Message{
					ID:      103,
					PeerID:  &tg.PeerChannel{ChannelID: channelRawID},
					Date:    int(baseTime.Add(2 * time.Hour).Unix()),
					Message: "周报总结消息（应排除）",
				},
				&tg.Message{
					ID:      102,
					PeerID:  &tg.PeerChannel{ChannelID: channelRawID},
					Date:    int(baseTime.Add(1 * time.Hour).Unix()),
					Message: "较早消息 102",
				},
				&tg.Message{
					ID:      101,
					PeerID:  &tg.PeerChannel{ChannelID: channelRawID},
					Date:    int(baseTime.Add(-2 * time.Hour).Unix()),
					Message: "超出时间窗口的旧消息 101",
				},
			}
			return encodeReply(output, &tg.MessagesChannelMessages{
				Messages: msgs,
				Chats:    []tg.ChatClass{ch},
			})

		case *tg.ChannelsGetFullChannelRequest:
			cf := &tg.ChannelFull{
				ID:        channelRawID,
				ChatPhoto: &tg.PhotoEmpty{},
			}
			cf.SetLinkedChatID(discRawID)
			discCh := &tg.Channel{
				ID:        discRawID,
				Title:     "Sakura Discussion",
				Photo:     &tg.ChatPhotoEmpty{},
				Megagroup: true,
			}
			discCh.SetAccessHash(333444555)
			return encodeReply(output, &tg.MessagesChatFull{
				FullChat: cf,
				Chats:    []tg.ChatClass{discCh},
			})

		case *tg.MessagesGetDiscussionMessageRequest:
			root := &tg.Message{
				ID:      501,
				PeerID:  &tg.PeerChannel{ChannelID: discRawID},
				Date:    int(baseTime.Unix()),
				Message: "原帖正文",
			}
			fwd := tg.MessageFwdHeader{
				Date:           int(baseTime.Unix()),
				SavedFromMsgID: req.MsgID,
			}
			fwd.SetSavedFromPeer(&tg.PeerChannel{ChannelID: channelRawID})
			root.SetFwdFrom(fwd)
			return encodeReply(output, &tg.MessagesDiscussionMessage{
				Messages: []tg.MessageClass{root},
			})

		case *tg.MessagesGetRepliesRequest:
			r1 := &tg.Message{
				ID:      502,
				PeerID:  &tg.PeerChannel{ChannelID: discRawID},
				Date:    int(baseTime.Add(time.Minute).Unix()),
				Message: "助手回复",
			}
			r1.SetFromID(&tg.PeerUser{UserID: 777001})
			rh := &tg.MessageReplyHeader{}
			rh.SetReplyToMsgID(501)
			r1.SetReplyTo(rh)

			r2 := &tg.Message{
				ID:      503,
				PeerID:  &tg.PeerChannel{ChannelID: discRawID},
				Date:    int(baseTime.Add(2 * time.Minute).Unix()),
				Message: "普通读者评论",
			}
			r2.SetFromID(&tg.PeerUser{UserID: 888002})

			return encodeReply(output, &tg.MessagesChannelMessages{
				Messages: []tg.MessageClass{r2, r1},
				Users: []tg.UserClass{
					&tg.User{ID: 777001, Bot: true, FirstName: "QA Assistant", Username: "sakura_qa_bot"},
					&tg.User{ID: 888002, FirstName: "Bob", Username: "bob_reader"},
				},
			})

		case *tg.UploadGetFileRequest:
			// 返回 64 字节测试内容供 maxBytes 下载上限校验
			return encodeReply(output, &tg.UploadFile{
				Type:  &tg.StorageFileJpeg{},
				Bytes: bytes.Repeat([]byte("A"), 64),
			})

		default:
			t.Fatalf("unexpected RPC request %T", input)
			return nil
		}
	})

	client := NewClientFromInvoker(invoker, WithSelf(111000, "sakura_main_bot"))
	_ = client.Peers().Put(CachedPeer{
		MarkedID:      channelMarkedID,
		RawID:         channelRawID,
		Kind:          PeerKindChannel,
		AccessHash:    999111222,
		HasAccessHash: true,
		Username:      "sakura_channel",
		Title:         "Sakura Channel",
		Broadcast:     true,
	})

	// 1. SetBotCommands
	err := client.SetBotCommands(ctx, "zh", []BotCommandDef{
		{Command: "/start", Description: "开始使用"},
		{Command: "summary", Description: "生成总结"},
	})
	if err != nil {
		t.Fatalf("SetBotCommands failed: %v", err)
	}
	if gotCommands == nil || gotCommands.LangCode != "zh" || len(gotCommands.Commands) != 2 || gotCommands.Commands[0].Command != "start" {
		t.Fatalf("unexpected BotsSetBotCommandsRequest: %#v", gotCommands)
	}

	// 2. SendMessage（Markdown 富文本 + 内联按钮 + 回复）
	sentID, err := client.SendMessage(ctx, "@sakura_channel", "🌸 **加粗标题**", SendOptions{
		ParseMode:    "markdown",
		ReplyToMsgID: 88,
		NoWebpage:    true,
		Buttons: [][]InlineButton{
			{{Text: "重新生成", Data: "regen_poll_88"}, {Text: "打开频道", URL: "https://t.me/sakura_channel"}},
		},
	})
	if err != nil || sentID != 1001 {
		t.Fatalf("SendMessage failed: id=%d err=%v", sentID, err)
	}
	if gotSendMsg == nil || gotSendMsg.Message != "🌸 加粗标题" || len(gotSendMsg.Entities) != 1 || !gotSendMsg.NoWebpage {
		t.Fatalf("unexpected MessagesSendMessageRequest: %#v", gotSendMsg)
	}

	// 3. SendLongMessage 实体异常自动降级到纯文本重试
	ids, err := client.SendLongMessage(ctx, channelMarkedID, "**TRIGGER_ENTITY_ERR** 正文", SendOptions{
		ParseMode: "markdown",
	})
	if err != nil || len(ids) != 1 {
		t.Fatalf("SendLongMessage fallback failed: ids=%v err=%v", ids, err)
	}
	if gotSendMsg.Message != "TRIGGER_ENTITY_ERR 正文" || len(gotSendMsg.Entities) != 0 {
		t.Fatalf("expected fallback to strip markdown and send without entities, got msg=%q ents=%d", gotSendMsg.Message, len(gotSendMsg.Entities))
	}

	// 4. SendPoll（广播频道自动将 PublicVoters 强制置为 false）
	pollID, err := client.SendPoll(ctx, channelMarkedID, PollSendOptions{
		Question:     "本周最佳话题？",
		Options:      []string{"AI 总结", "群管防护", "向量检索"},
		PublicVoters: true,
		ReplyToMsgID: 1001,
		Buttons:      [][]InlineButton{{{Text: "换一题", Data: "request_regen_1001"}}},
	})
	if err != nil || pollID != 2048 {
		t.Fatalf("SendPoll failed: id=%d err=%v", pollID, err)
	}
	imp, ok := gotSendMedia.Media.(*tg.InputMediaPoll)
	if !ok || imp.Poll.PublicVoters || len(imp.Poll.Answers) != 3 {
		t.Fatalf("unexpected InputMediaPoll (broadcast PublicVoters should be false): %#v", gotSendMedia.Media)
	}

	// 5. FetchChannelHistory（时间升序 reverse=True + 排除报告消息 103 + 过滤旧消息 101）
	history, chInfo, err := client.FetchChannelHistory(ctx, "@sakura_channel", baseTime, map[int]bool{103: true})
	if err != nil {
		t.Fatalf("FetchChannelHistory failed: %v", err)
	}
	if chInfo == nil || chInfo.MarkedID != channelMarkedID {
		t.Fatalf("unexpected ChannelInfo: %+v", chInfo)
	}
	if len(history) != 2 || history[0].ID != 102 || history[1].ID != 104 {
		t.Fatalf("expected chronological [102, 104], got %+v", history)
	}
	if history[0].PostLink != "https://t.me/sakura_channel/102" {
		t.Fatalf("unexpected PostLink: %q", history[0].PostLink)
	}

	// 6. GetLinkedDiscussionChatID 缓存读写与清理
	linkedID, err := client.GetLinkedDiscussionChatID(ctx, "@sakura_channel")
	if err != nil || linkedID != discMarkedID {
		t.Fatalf("GetLinkedDiscussionChatID failed: id=%d err=%v", linkedID, err)
	}
	if len(client.LinkedChatCacheChannels()) == 0 {
		t.Fatalf("expected non-empty LinkedChatCacheChannels")
	}
	if cleared := client.ClearLinkedChatCache("@sakura_channel"); cleared == 0 {
		t.Fatalf("expected ClearLinkedChatCache to remove cached entry")
	}

	// 7. BackfillDiscussionComments（根消息 + 评论角色识别 + 截断检测）
	comments, truncated, _, err := client.BackfillDiscussionComments(ctx, "@sakura_channel", 77, 1, 777001)
	if err != nil {
		t.Fatalf("BackfillDiscussionComments failed: %v", err)
	}
	if !truncated || len(comments) != 2 {
		t.Fatalf("expected truncated=true and 2 records (root + 1 reply), got truncated=%v len=%d", truncated, len(comments))
	}
	if comments[0].AuthorRole != "channel" || comments[0].MessageID != 501 {
		t.Fatalf("unexpected root comment: %+v", comments[0])
	}

	commentsFull, truncatedFull, _, err := client.BackfillDiscussionComments(ctx, "@sakura_channel", 77, 10, 777001)
	if err != nil || truncatedFull || len(commentsFull) != 3 {
		t.Fatalf("expected full 3 comments without truncation, got truncated=%v len=%d err=%v", truncatedFull, len(commentsFull), err)
	}
	roles := map[int64]string{}
	for _, cm := range commentsFull {
		roles[cm.MessageID] = cm.AuthorRole
	}
	if roles[502] != "assistant" || roles[503] != "user" {
		t.Fatalf("unexpected comment roles: %+v", roles)
	}

	// 8. DownloadMediaBytes maxBytes 硬限制拦截
	att := &MediaAttachment{
		Kind: "document",
		Raw: &tg.Document{
			ID:         901,
			AccessHash: 902,
			Size:       0, // 模拟未知元信息大小，验证流式读取硬拦截
		},
	}
	if _, err := client.DownloadMediaBytes(ctx, att, 32); !errors.Is(err, ErrMediaTooLarge) {
		t.Fatalf("expected ErrMediaTooLarge when stream exceeds 32 bytes, got %v", err)
	}
	data, err := client.DownloadMediaBytes(ctx, att, 128)
	if err != nil || len(data) != 64 {
		t.Fatalf("expected 64 downloaded bytes, got len=%d err=%v", len(data), err)
	}
}

func TestDispatcherEventsPanicRecoveryAndTerminalAuth(t *testing.T) {
	ctx := context.Background()
	peers := NewMemoryPeerCache()
	disp := NewDispatcher(peers, nil)

	var (
		gotNewMsg  *IncomingMessage
		gotJoinReq JoinRequestEvent
		gotPart    []ParticipantEvent
	)

	// 第一个订阅者故意触发 panic，验证不会阻断后续订阅者执行
	_ = disp.OnNewMessage(func(_ context.Context, _ *IncomingMessage) {
		panic("simulated handler panic")
	})
	unsubMsg := disp.OnNewMessage(func(_ context.Context, msg *IncomingMessage) {
		gotNewMsg = msg
	})
	_ = disp.OnJoinRequest(func(_ context.Context, ev JoinRequestEvent) {
		gotJoinReq = ev
	})
	_ = disp.OnParticipant(func(_ context.Context, ev ParticipantEvent) {
		gotPart = append(gotPart, ev)
	})

	u := &tg.User{ID: 3001, FirstName: "Sakura", Username: "sakura_member"}
	u.SetAccessHash(888999)
	ch := &tg.Channel{ID: 1234567890, Title: "Test Supergroup", Megagroup: true}
	ch.SetAccessHash(444555)

	txtMsg := &tg.Message{
		ID:      12,
		PeerID:  &tg.PeerChannel{ChannelID: 1234567890},
		Date:    1700001000,
		Message: "hello group",
	}
	txtMsg.SetFromID(&tg.PeerUser{UserID: 3001})

	svcJoin := &tg.MessageService{
		ID:     13,
		PeerID: &tg.PeerChannel{ChannelID: 1234567890},
		Date:   1700001005,
		Action: &tg.MessageActionChatJoinedByLink{InviterID: 1},
	}
	svcJoin.SetFromID(&tg.PeerUser{UserID: 3001})

	jr := &tg.UpdateBotChatInviteRequester{
		Peer:   &tg.PeerChannel{ChannelID: 1234567890},
		Date:   1700001010,
		UserID: 3001,
		About:  "我想入群交流",
	}
	jr.SetQueryID(991122)

	batch := &tg.Updates{
		Updates: []tg.UpdateClass{
			&tg.UpdateNewChannelMessage{Message: txtMsg},
			&tg.UpdateNewChannelMessage{Message: svcJoin},
			jr,
		},
		Users: []tg.UserClass{u},
		Chats: []tg.ChatClass{ch},
	}

	if err := disp.Handle(ctx, batch); err != nil {
		t.Fatalf("Dispatcher.Handle failed: %v", err)
	}

	// 验证 Entities 在事件分发前已写入 PeerCache
	if cp, ok := peers.GetByMarkedID(3001); !ok || cp.AccessHash != 888999 {
		t.Fatalf("expected PeerCache populated before handler execution, got %+v", cp)
	}
	if gotNewMsg == nil || gotNewMsg.Text != "hello group" || !gotNewMsg.IsSupergroup || gotNewMsg.Sender.Username != "sakura_member" {
		t.Fatalf("unexpected IncomingMessage: %+v", gotNewMsg)
	}
	if len(gotPart) != 1 || gotPart[0].Kind != "join" || gotPart[0].ActionMessageID != 13 {
		t.Fatalf("unexpected service message ParticipantEvent: %+v", gotPart)
	}
	if !gotJoinReq.HasQueryID || gotJoinReq.QueryID != 991122 || gotJoinReq.Bio != "我想入群交流" {
		t.Fatalf("unexpected JoinRequestEvent: %+v", gotJoinReq)
	}

	// 验证取消订阅后不再收到消息
	unsubMsg()
	gotNewMsg = nil
	_ = disp.Handle(ctx, &tg.Updates{
		Updates: []tg.UpdateClass{&tg.UpdateNewChannelMessage{Message: txtMsg}},
	})
	if gotNewMsg != nil {
		t.Fatalf("expected unsubscribed handler not to fire")
	}

	// 验证 TerminalAuthenticator 空手机号与 "cancel" 立即干净中止
	emptyAuth := NewTerminalAuthenticator("", strings.NewReader("12345\n"), &bytes.Buffer{})
	if _, err := emptyAuth.Phone(ctx); !errors.Is(err, ErrEmptyPhoneNumber) {
		t.Fatalf("expected ErrEmptyPhoneNumber, got %v", err)
	}
	cancelAuth := NewTerminalAuthenticator("+8613800138000", strings.NewReader("cancel\n"), &bytes.Buffer{})
	if _, err := cancelAuth.Code(ctx, &tg.AuthSentCode{}); !errors.Is(err, ErrAuthCancelled) {
		t.Fatalf("expected ErrAuthCancelled on 'cancel', got %v", err)
	}
}

func TestUserBotManagerAndAdditionalEventTypes(t *testing.T) {
	ctx := context.Background()
	var (
		joinedPublic bool
		joinedInvite string
		leftChannel  bool
	)

	invoker := mockInvokerFunc(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
		switch req := input.(type) {
		case *tg.ContactsResolveUsernameRequest:
			ch := &tg.Channel{
				ID:        1555666777,
				Title:     "Public Tech Channel",
				Username:  req.Username,
				Photo:     &tg.ChatPhotoEmpty{},
				Broadcast: true,
			}
			ch.SetAccessHash(123987456)
			return encodeReply(output, &tg.ContactsResolvedPeer{
				Peer:  &tg.PeerChannel{ChannelID: 1555666777},
				Chats: []tg.ChatClass{ch},
			})

		case *tg.ChannelsJoinChannelRequest:
			joinedPublic = true
			return encodeReply(output, &tg.MessagesChatInviteJoinResultOk{
				Updates: &tg.Updates{},
			})

		case *tg.MessagesImportChatInviteRequest:
			joinedInvite = req.Hash
			ch := &tg.Channel{
				ID:        1666777888,
				Title:     "Private Invite Channel",
				Photo:     &tg.ChatPhotoEmpty{},
				Broadcast: true,
			}
			ch.SetAccessHash(456123789)
			return encodeReply(output, &tg.MessagesChatInviteJoinResultOk{
				Updates: &tg.Updates{
					Chats: []tg.ChatClass{ch},
				},
			})

		case *tg.ChannelsLeaveChannelRequest:
			leftChannel = true
			return encodeReply(output, &tg.Updates{})

		case *tg.MessagesGetDialogsRequest:
			ch := &tg.Channel{
				ID:        1555666777,
				Title:     "Public Tech Channel",
				Username:  "tech_chan",
				Photo:     &tg.ChatPhotoEmpty{},
				Broadcast: true,
			}
			ch.SetAccessHash(123987456)
			return encodeReply(output, &tg.MessagesDialogs{
				Dialogs: []tg.DialogClass{
					&tg.Dialog{Peer: &tg.PeerChannel{ChannelID: 1555666777}, TopMessage: 1},
				},
				Chats: []tg.ChatClass{ch},
			})

		case *tg.MessagesEditMessageRequest:
			return encodeReply(output, nil) // not reached if MESSAGE_NOT_MODIFIED tested below

		default:
			t.Fatalf("unexpected RPC %T", input)
			return nil
		}
	})

	cli := NewClientFromInvoker(invoker, WithSelf(5001, "userbot_tester"))
	mgr := NewUserBotManager(UserBotOptions{
		Enabled:       true,
		PhoneNumber:   "+8613800138000",
		FallbackToBot: true,
	})
	mgr.InjectClient(cli)

	st := mgr.Status()
	if !st.Available || !st.Connected || st.PhonePreview != "+86****8000" {
		t.Fatalf("unexpected UserBotStatus: %+v", st)
	}

	batchRes := mgr.JoinAllForwardingChannels(ctx, []string{
		"https://t.me/tech_chan",
		"https://t.me/+SecretHashXYZ",
		"https://t.me/tech_chan", // 重复项应自动去重
	})
	if !batchRes.Success || batchRes.Total != 2 || batchRes.SuccessCount != 2 || !joinedPublic || joinedInvite != "SecretHashXYZ" {
		t.Fatalf("unexpected JoinAllForwardingChannels result: %+v", batchRes)
	}

	joinedList, err := mgr.ListJoinedChannels(ctx)
	if err != nil || len(joinedList) != 1 || joinedList[0].Username != "tech_chan" {
		t.Fatalf("ListJoinedChannels mismatch: list=%+v err=%v", joinedList, err)
	}

	leaveRes := mgr.LeaveChannel(ctx, "@tech_chan")
	if !leaveRes.Success || !leftChannel {
		t.Fatalf("LeaveChannel failed: %+v", leaveRes)
	}

	// 验证其余实时事件：编辑、删除、按钮回调、用户名变更、UpdateChannelParticipant
	disp := cli.Dispatcher()
	var (
		gotEdited  *IncomingMessage
		gotDeleted DeletedMessagesEvent
		gotCb      CallbackEvent
		gotUName   UserNameEvent
		gotChPart  ParticipantEvent
	)
	_ = disp.OnMessageEdited(func(_ context.Context, m *IncomingMessage) { gotEdited = m })
	_ = disp.OnMessageDeleted(func(_ context.Context, ev DeletedMessagesEvent) { gotDeleted = ev })
	_ = disp.OnCallbackQuery(func(_ context.Context, ev CallbackEvent) { gotCb = ev })
	_ = disp.OnUserName(func(_ context.Context, ev UserNameEvent) { gotUName = ev })
	_ = disp.OnParticipant(func(_ context.Context, ev ParticipantEvent) { gotChPart = ev })

	editMsg := &tg.Message{
		ID:      91,
		PeerID:  &tg.PeerChannel{ChannelID: 1555666777},
		Date:    1700002000,
		Message: "edited content",
	}
	editMsg.SetEditDate(1700002050)

	cbUpd := &tg.UpdateBotCallbackQuery{
		QueryID: 7788,
		UserID:  6001,
		Peer:    &tg.PeerChannel{ChannelID: 1555666777},
		MsgID:   91,
	}
	cbUpd.SetData([]byte("sg:r:9:u:0"))

	partUpd := &tg.UpdateChannelParticipant{
		ChannelID: 1555666777,
		Date:      1700002100,
		ActorID:   5001,
		UserID:    6001,
		Qts:       15,
	}
	partUpd.SetNewParticipant(&tg.ChannelParticipantBanned{
		Peer:         &tg.PeerUser{UserID: 6001},
		KickedBy:     5001,
		Date:         1700002100,
		BannedRights: tg.ChatBannedRights{ViewMessages: true},
	})

	err = disp.Handle(ctx, &tg.Updates{
		Updates: []tg.UpdateClass{
			&tg.UpdateEditChannelMessage{Message: editMsg},
			&tg.UpdateDeleteChannelMessages{ChannelID: 1555666777, Messages: []int{91, 92}},
			cbUpd,
			&tg.UpdateUserName{
				UserID:    6001,
				FirstName: "NewFirst",
				Usernames: []tg.Username{{Username: "new_alias", Active: true}},
			},
			partUpd,
		},
	})
	if err != nil {
		t.Fatalf("Dispatcher.Handle multi-events failed: %v", err)
	}

	if gotEdited == nil || gotEdited.EditDate == nil || gotEdited.Text != "edited content" {
		t.Fatalf("unexpected edited message: %+v", gotEdited)
	}
	if gotDeleted.ChannelID != 1555666777 || len(gotDeleted.MessageIDs) != 2 {
		t.Fatalf("unexpected deleted event: %+v", gotDeleted)
	}
	if gotCb.DataString() != "sg:r:9:u:0" || gotCb.QueryID != 7788 {
		t.Fatalf("unexpected callback event: %+v", gotCb)
	}
	if gotUName.Username != "new_alias" || gotUName.FirstName != "NewFirst" {
		t.Fatalf("unexpected username event: %+v", gotUName)
	}
	if gotChPart.Kind != "kick" || !gotChPart.IsBanned || gotChPart.QTS != 15 {
		t.Fatalf("unexpected channel participant event: %+v", gotChPart)
	}
}
