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
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

func encodeReply(out bin.Decoder, msg bin.Encoder) error {
	var buf bin.Buffer
	if err := msg.Encode(&buf); err != nil {
		return err
	}
	return out.Decode(&buf)
}

func TestBannedRightsAlgebraAndMapConversion(t *testing.T) {
	now := time.Now().UTC().Unix()

	// 1. VerificationRights 保留原有非发言限制（如 InviteUsers），叠加全部 15 项发言限制，且 UntilDate=0
	orig := BannedRights{
		InviteUsers: true,
		PinMessages: true,
		UntilDate:   now + 3600,
	}
	vr := VerificationRights(orig)
	if !vr.InviteUsers || !vr.PinMessages {
		t.Fatalf("VerificationRights lost original non-speech restrictions: %+v", vr)
	}
	if vr.UntilDate != 0 {
		t.Fatalf("VerificationRights UntilDate must be 0, got %d", vr.UntilDate)
	}
	vrMap := BannedRightsToMap(vr)
	for _, key := range VerificationRestrictionKeys {
		if v, _ := vrMap[key].(bool); !v {
			t.Errorf("VerificationRights expected %s=true", key)
		}
	}
	restored := BannedRightsFromMap(vrMap)
	if restored != vr {
		t.Fatalf("BannedRightsFromMap round-trip mismatch: got %+v, want %+v", restored, vr)
	}

	// 2. MuteRights：若原有限制为永久（UntilDate=0 且存在限制位），禁言后仍保持永久 UntilDate=0
	permOrig := BannedRights{EmbedLinks: true, UntilDate: 0}
	mrPerm := MuteRights(permOrig, now+600)
	if mrPerm.UntilDate != 0 || !mrPerm.SendMessages || !mrPerm.SendMedia || !mrPerm.SendPlain || !mrPerm.EmbedLinks {
		t.Fatalf("MuteRights on permanent restriction failed: %+v", mrPerm)
	}

	// 若原有无限制，则取 muteUntil；若原有解封时间更晚，则保留更晚时间
	mrTimed := MuteRights(BannedRights{}, now+600)
	if mrTimed.UntilDate != now+600 {
		t.Fatalf("expected UntilDate=%d, got %d", now+600, mrTimed.UntilDate)
	}
	mrLonger := MuteRights(BannedRights{SendStickers: true, UntilDate: now + 7200}, now+600)
	if mrLonger.UntilDate != now+7200 {
		t.Fatalf("expected longer UntilDate=%d preserved, got %d", now+7200, mrLonger.UntilDate)
	}

	// 3. ComparableRights & SameRights：
	//    - SendMessages=true 等价于全部 15 项发言限制全开；
	//    - UntilDate > now + 366d 归一化为永久 0；
	//    - UntilDate < now（已过期）归一化为空限制；
	//    - 仅设置 UntilDate 但无任何布尔限制位时归一化为 UntilDate=0。
	a := BannedRights{SendMessages: true, UntilDate: now + 400*86400}
	b := VerificationRights(BannedRights{})
	if !SameRights(a, b) {
		t.Fatalf("expected SendMessages+forever to equal VerificationRights: comp(a)=%+v comp(b)=%+v", ComparableRights(a), ComparableRights(b))
	}

	expired := BannedRights{SendMessages: true, UntilDate: now - 10}
	if !SameRights(expired, BannedRights{}) {
		t.Fatalf("expected expired rights to equal empty rights, got %+v", ComparableRights(expired))
	}

	emptyWithDate := BannedRights{UntilDate: now + 1000}
	if ComparableRights(emptyWithDate).UntilDate != 0 {
		t.Fatalf("expected empty restrictions to normalize UntilDate=0")
	}

	// 4. StableRandomID 确定性校验
	id1 := StableRandomID("guard:case-42:10001")
	id2 := StableRandomID("guard:case-42:10001")
	id3 := StableRandomID("guard:case-42:10002")
	if id1 == 0 || id1 != id2 || id1 == id3 {
		t.Fatalf("unexpected StableRandomID values: id1=%d id2=%d id3=%d", id1, id2, id3)
	}
}

func TestGuardActionsRPCsViaMockInvoker(t *testing.T) {
	ctx := context.Background()
	const (
		supergroupMarkedID = int64(-1001234567890)
		supergroupRawID    = int64(1234567890)
		basicChatMarkedID  = int64(-555666)
		basicChatRawID     = int64(555666)
		targetUserID       = int64(200300)
		adminID            = int64(900001)
	)

	var (
		gotEditBanned  *tg.ChannelsEditBannedRequest
		gotJoinResult  *tg.BotsSetJoinChatResultsRequest
		gotSendOnce    *tg.MessagesSendMessageRequest
		gotForwardOnce *tg.MessagesForwardMessagesRequest
		gotDeleteChatU *tg.MessagesDeleteChatUserRequest
		gotHideJoinReq *tg.MessagesHideChatJoinRequestRequest
	)

	invoker := mockInvokerFunc(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
		switch req := input.(type) {
		case *tg.ChannelsEditBannedRequest:
			gotEditBanned = req
			return encodeReply(output, &tg.Updates{})

		case *tg.BotsSetJoinChatResultsRequest:
			gotJoinResult = req
			var b tg.BoolTrue
			return encodeReply(output, &b)

		case *tg.MessagesSendMessageRequest:
			gotSendOnce = req
			upd := &tg.Updates{
				Updates: []tg.UpdateClass{
					&tg.UpdateMessageID{
						ID:       8881,
						RandomID: req.RandomID,
					},
				},
			}
			return encodeReply(output, upd)

		case *tg.MessagesForwardMessagesRequest:
			gotForwardOnce = req
			return encodeReply(output, &tg.Updates{})

		case *tg.MessagesDeleteChatUserRequest:
			gotDeleteChatU = req
			return encodeReply(output, &tg.Updates{})

		case *tg.MessagesHideChatJoinRequestRequest:
			gotHideJoinReq = req
			return encodeReply(output, &tg.Updates{})

		case *tg.ChannelsGetParticipantRequest:
			part := &tg.ChannelsChannelParticipant{
				Participant: &tg.ChannelParticipantAdmin{
					UserID: targetUserID,
					Date:   1700000100,
					AdminRights: tg.ChatAdminRights{
						BanUsers:       true,
						DeleteMessages: true,
						InviteUsers:    true,
					},
				},
			}
			return encodeReply(output, part)

		case *tg.UsersGetFullUserRequest:
			u := &tg.User{
				ID:        targetUserID,
				FirstName: "Sakura",
				Username:  "sakura_user",
			}
			u.SetAccessHash(123456)
			res := &tg.UsersUserFull{
				FullUser: tg.UserFull{
					ID:    targetUserID,
					About: "Bio from GetFullUser 🌸",
				},
				Users: []tg.UserClass{u},
			}
			return encodeReply(output, res)

		case *tg.ChannelsGetParticipantsRequest:
			delUser := &tg.User{ID: 701, Deleted: true, FirstName: "Deleted"}
			normalUser := &tg.User{ID: 702, Deleted: false, FirstName: "Active"}
			res := &tg.ChannelsChannelParticipants{
				Count: 2,
				Participants: []tg.ChannelParticipantClass{
					&tg.ChannelParticipant{UserID: 701, Date: 1700000000},
					&tg.ChannelParticipant{UserID: 702, Date: 1700000000},
				},
				Users: []tg.UserClass{delUser, normalUser},
			}
			return encodeReply(output, res)

		case *tg.MessagesGetHistoryRequest:
			m1 := &tg.Message{ID: 50, PeerID: &tg.PeerChannel{ChannelID: supergroupRawID}, Message: "spam"}
			m1.SetFromID(&tg.PeerUser{UserID: 701})
			m2 := &tg.Message{ID: 49, PeerID: &tg.PeerChannel{ChannelID: supergroupRawID}, Message: "ok"}
			m2.SetFromID(&tg.PeerUser{UserID: 702})
			res := &tg.MessagesChannelMessages{
				Messages: []tg.MessageClass{m1, m2},
				Users: []tg.UserClass{
					&tg.User{ID: 701, Deleted: true},
					&tg.User{ID: 702, Deleted: false},
				},
			}
			return encodeReply(output, res)

		default:
			t.Fatalf("unexpected RPC request %T", input)
			return nil
		}
	})

	client := NewClientFromInvoker(invoker, WithSelf(999999, "sakura_guard_bot"))
	_ = client.Peers().Put(CachedPeer{
		MarkedID:      supergroupMarkedID,
		RawID:         supergroupRawID,
		Kind:          PeerKindChannel,
		AccessHash:    555666777,
		HasAccessHash: true,
		Megagroup:     true,
	})
	guard := NewGuardActions(client)

	// 1. SetRights
	vr := VerificationRights(BannedRights{})
	if err := guard.SetRights(ctx, supergroupMarkedID, targetUserID, vr); err != nil {
		t.Fatalf("SetRights failed: %v", err)
	}
	if gotEditBanned == nil || !gotEditBanned.BannedRights.SendMessages || !gotEditBanned.BannedRights.SendPolls {
		t.Fatalf("unexpected EditBanned request: %#v", gotEditBanned)
	}

	// 2. AnswerJoinRequestQuery（正常批准 + 过期拦截）
	expFuture := float64(time.Now().UTC().Add(8*time.Second).UnixNano()) / 1e9
	if err := guard.AnswerJoinRequestQuery(ctx, 424242, "approve", expFuture); err != nil {
		t.Fatalf("AnswerJoinRequestQuery(approve) failed: %v", err)
	}
	if gotJoinResult == nil || gotJoinResult.QueryID != 424242 {
		t.Fatalf("unexpected BotsSetJoinChatResultsRequest: %#v", gotJoinResult)
	}
	if _, ok := gotJoinResult.Result.(*tg.JoinChatBotResultApproved); !ok {
		t.Fatalf("expected *tg.JoinChatBotResultApproved, got %T", gotJoinResult.Result)
	}

	expPast := float64(time.Now().UTC().Add(-2*time.Second).UnixNano()) / 1e9
	err := guard.AnswerJoinRequestQuery(ctx, 424242, "manual", expPast)
	var actErr *TelegramActionError
	if !errors.As(err, &actErr) || actErr.Code != "JoinRequestQueryExpired" {
		t.Fatalf("expected JoinRequestQueryExpired, got %v", err)
	}

	// 3. SendOnce
	rndSend := StableRandomID("guard:case-1:admin")
	msgID, err := guard.SendOnce(
		ctx,
		adminID,
		"审核通知正文",
		[][]InlineButton{{{Text: "解封", Data: "sg:r:1:u:0"}}},
		rndSend,
		[]EntitySpan{{Kind: "bold", Offset: 0, Length: 4}},
	)
	if err != nil {
		t.Fatalf("SendOnce failed: %v", err)
	}
	if msgID != 8881 || gotSendOnce == nil || gotSendOnce.RandomID != rndSend || !gotSendOnce.NoWebpage {
		t.Fatalf("unexpected SendOnce behavior: id=%d req=%#v", msgID, gotSendOnce)
	}

	// 4. ForwardOnce
	rndFwd := StableRandomID("guard-report-evidence:1:admin")
	if err := guard.ForwardOnce(ctx, adminID, supergroupMarkedID, 77, rndFwd); err != nil {
		t.Fatalf("ForwardOnce failed: %v", err)
	}
	if gotForwardOnce == nil || len(gotForwardOnce.ID) != 1 || gotForwardOnce.ID[0] != 77 || gotForwardOnce.RandomID[0] != rndFwd {
		t.Fatalf("unexpected ForwardOnce request: %#v", gotForwardOnce)
	}

	// 5. Ban 普通群 -> KickBasicGroup(RevokeHistory=false)
	if err := guard.Ban(ctx, basicChatMarkedID, targetUserID); err != nil {
		t.Fatalf("Ban basic group failed: %v", err)
	}
	if gotDeleteChatU == nil || gotDeleteChatU.ChatID != basicChatRawID || gotDeleteChatU.RevokeHistory {
		t.Fatalf("unexpected DeleteChatUser request: %#v", gotDeleteChatU)
	}

	// 6. JoinRequest / Permissions / GetFullUserBio / ScanDeletedParticipants / ScanHistoryDeletedSenders
	if err := guard.JoinRequest(ctx, supergroupMarkedID, targetUserID, true); err != nil {
		t.Fatalf("JoinRequest failed: %v", err)
	}
	if gotHideJoinReq == nil || !gotHideJoinReq.Approved {
		t.Fatalf("unexpected HideChatJoinRequest: %#v", gotHideJoinReq)
	}

	perms, err := guard.Permissions(ctx, supergroupMarkedID, targetUserID)
	if err != nil {
		t.Fatalf("Permissions failed: %v", err)
	}
	if !perms.IsAdmin || !perms.CanBanUsers || !perms.CanDeleteMessages {
		t.Fatalf("unexpected Permissions: %+v", perms)
	}

	bioProf, err := guard.GetFullUserBio(ctx, targetUserID)
	if err != nil || bioProf.Bio != "Bio from GetFullUser 🌸" {
		t.Fatalf("GetFullUserBio failed: prof=%+v err=%v", bioProf, err)
	}

	delPage, err := guard.ScanDeletedParticipants(ctx, supergroupMarkedID, 0, 200)
	if err != nil || len(delPage.DeletedUsers) != 1 || delPage.DeletedUsers[0].ID != 701 || !delPage.Done {
		t.Fatalf("ScanDeletedParticipants mismatch: page=%+v err=%v", delPage, err)
	}

	histPage, err := guard.ScanHistoryDeletedSenders(ctx, supergroupMarkedID, 0, 100)
	if err != nil || len(histPage.DeletedMessageIDs) != 1 || histPage.DeletedMessageIDs[0] != 50 || histPage.NextOffsetID != 49 {
		t.Fatalf("ScanHistoryDeletedSenders mismatch: page=%+v err=%v", histPage, err)
	}

	// 7. 验证 RPC 错误包装为 *TelegramActionError 并保留 FloodWaitError / Telethon 错误名
	errClient := NewClientFromInvoker(mockInvokerFunc(func(_ context.Context, _ bin.Encoder, _ bin.Decoder) error {
		return tgerr.New(400, "CHAT_FORWARDS_RESTRICTED")
	}))
	errGuard := NewGuardActions(errClient)
	fErr := errGuard.ForwardOnce(ctx, adminID, supergroupMarkedID, 1, 99)
	if !errors.As(fErr, &actErr) || actErr.Code != "ChatForwardsRestrictedError" {
		t.Fatalf("expected ChatForwardsRestrictedError, got %v", fErr)
	}
}
