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
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

// GuardCallTimeout 为 Sakura Guard 群管单次 Telegram RPC 调用的统一超时（对齐 core/moderation/telegram.py:94-95 的 25s）。
const GuardCallTimeout = 25 * time.Second

// StableRandomID 使用 SHA-256 前 8 字节（大端有符号 int64）生成确定性的消息去重 random_id
// （逐字节对齐 core/moderation/notification.py:stable_random_id）。
func StableRandomID(seed string) int64 {
	sum := sha256.Sum256([]byte(seed))
	return int64(binary.BigEndian.Uint64(sum[:8]))
}

// HasAnyRestriction 判断 BannedRights 是否包含任意受限布尔权限位（不含 UntilDate）。
func (b BannedRights) HasAnyRestriction() bool {
	return b.ViewMessages ||
		b.SendMessages ||
		b.SendMedia ||
		b.SendStickers ||
		b.SendGifs ||
		b.SendGames ||
		b.SendInline ||
		b.EmbedLinks ||
		b.SendPolls ||
		b.ChangeInfo ||
		b.InviteUsers ||
		b.PinMessages ||
		b.ManageTopics ||
		b.SendPhotos ||
		b.SendVideos ||
		b.SendRoundvideos ||
		b.SendAudios ||
		b.SendVoices ||
		b.SendDocs ||
		b.SendPlain
}

func applyAllVerificationRestrictions(b *BannedRights) {
	b.SendMessages = true
	b.SendMedia = true
	b.SendPlain = true
	b.SendStickers = true
	b.SendGifs = true
	b.SendGames = true
	b.SendInline = true
	b.EmbedLinks = true
	b.SendPolls = true
	b.SendPhotos = true
	b.SendVideos = true
	b.SendRoundvideos = true
	b.SendAudios = true
	b.SendVoices = true
	b.SendDocs = true
}

// VerificationRights 在原有限制基础上叠加全部 15 项发言限制并将 until_date 置 0
// （对齐 core/moderation/telegram.py:verification_rights）。
func VerificationRights(original BannedRights) BannedRights {
	res := original
	applyAllVerificationRestrictions(&res)
	res.UntilDate = 0
	return res
}

// MuteRights 计算叠加禁言后的成员权限，保留原有的永久限制或更晚解封时间
// （对齐 core/moderation/telegram.py:mute_rights）。
func MuteRights(original BannedRights, muteUntil int64) BannedRights {
	indefinite := original.UntilDate == 0 && original.HasAnyRestriction()
	res := original
	res.SendMessages = true
	res.SendMedia = true
	res.SendPlain = true
	if indefinite {
		res.UntilDate = 0
	} else {
		res.UntilDate = max(original.UntilDate, muteUntil)
	}
	return res
}

// ComparableRights 归一化 Telegram 的永久日期（>366 天）、过期时间、聚合发言位及空限制日期
// （对齐 core/moderation/telegram.py:comparable_rights）。
func ComparableRights(v BannedRights) BannedRights {
	return comparableRightsAt(v, time.Now().UTC().Unix())
}

func comparableRightsAt(v BannedRights, nowUnix int64) BannedRights {
	if v.UntilDate != 0 && v.UntilDate < nowUnix {
		return BannedRights{}
	}
	res := v
	if res.UntilDate > nowUnix+366*86400 {
		res.UntilDate = 0
	}
	if res.SendMessages {
		applyAllVerificationRestrictions(&res)
	}
	if !res.HasAnyRestriction() {
		res.UntilDate = 0
	}
	return res
}

// SameRights 判断两组受限权限在 Telegram 归一化语义下是否等价（对齐 telegram.py:same_rights）。
func SameRights(a, b BannedRights) bool {
	now := time.Now().UTC().Unix()
	return comparableRightsAt(a, now) == comparableRightsAt(b, now)
}

// BannedRightsToMap 将 BannedRights 序列化为与 Python moderation_cases / moderation_verification_sessions 完全一致的键值映射。
func BannedRightsToMap(r BannedRights) map[string]any {
	return map[string]any{
		"view_messages":    r.ViewMessages,
		"send_messages":    r.SendMessages,
		"send_media":       r.SendMedia,
		"send_stickers":    r.SendStickers,
		"send_gifs":        r.SendGifs,
		"send_games":       r.SendGames,
		"send_inline":      r.SendInline,
		"embed_links":      r.EmbedLinks,
		"send_polls":       r.SendPolls,
		"change_info":      r.ChangeInfo,
		"invite_users":     r.InviteUsers,
		"pin_messages":     r.PinMessages,
		"manage_topics":    r.ManageTopics,
		"send_photos":      r.SendPhotos,
		"send_videos":      r.SendVideos,
		"send_roundvideos": r.SendRoundvideos,
		"send_audios":      r.SendAudios,
		"send_voices":      r.SendVoices,
		"send_docs":        r.SendDocs,
		"send_plain":       r.SendPlain,
		"until_date":       r.UntilDate,
	}
}

// BannedRightsFromMap 从持久化 JSON 字典还原 BannedRights（兼容数值/字符串/空值的 until_date）。
func BannedRightsFromMap(m map[string]any) BannedRights {
	if len(m) == 0 {
		return BannedRights{}
	}
	return BannedRights{
		ViewMessages:    mapBool(m, "view_messages"),
		SendMessages:    mapBool(m, "send_messages"),
		SendMedia:       mapBool(m, "send_media"),
		SendStickers:    mapBool(m, "send_stickers"),
		SendGifs:        mapBool(m, "send_gifs"),
		SendGames:       mapBool(m, "send_games"),
		SendInline:      mapBool(m, "send_inline"),
		EmbedLinks:      mapBool(m, "embed_links"),
		SendPolls:       mapBool(m, "send_polls"),
		ChangeInfo:      mapBool(m, "change_info"),
		InviteUsers:     mapBool(m, "invite_users"),
		PinMessages:     mapBool(m, "pin_messages"),
		ManageTopics:    mapBool(m, "manage_topics"),
		SendPhotos:      mapBool(m, "send_photos"),
		SendVideos:      mapBool(m, "send_videos"),
		SendRoundvideos: mapBool(m, "send_roundvideos"),
		SendAudios:      mapBool(m, "send_audios"),
		SendVoices:      mapBool(m, "send_voices"),
		SendDocs:        mapBool(m, "send_docs"),
		SendPlain:       mapBool(m, "send_plain"),
		UntilDate:       mapInt64(m, "until_date"),
	}
}

func mapBool(m map[string]any, key string) bool {
	v, ok := m[key]
	if !ok || v == nil {
		return false
	}
	switch x := v.(type) {
	case bool:
		return x
	case float64:
		return x != 0
	case int:
		return x != 0
	case int64:
		return x != 0
	case json.Number:
		n, _ := x.Int64()
		return n != 0
	case string:
		low := strings.ToLower(strings.TrimSpace(x))
		return low == "true" || low == "1"
	default:
		return false
	}
}

func mapInt64(m map[string]any, key string) int64 {
	v, ok := m[key]
	if !ok || v == nil {
		return 0
	}
	switch x := v.(type) {
	case int64:
		return x
	case int:
		return int64(x)
	case float64:
		return int64(x)
	case json.Number:
		if i, err := x.Int64(); err == nil {
			return i
		}
		if f, err := x.Float64(); err == nil {
			return int64(f)
		}
	case string:
		s := strings.TrimSpace(x)
		if s == "" {
			return 0
		}
		if i, err := strconv.ParseInt(s, 10, 64); err == nil {
			return i
		}
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			return t.UTC().Unix()
		}
	}
	return 0
}

// BannedRightsToTG 将 BannedRights 转换为 tg.ChatBannedRights（已过期的 until_date 按 telegram.py:125 视作无限制）。
func BannedRightsToTG(r BannedRights) tg.ChatBannedRights {
	if r.UntilDate != 0 && r.UntilDate < time.Now().UTC().Unix() {
		return tg.ChatBannedRights{}
	}
	return tg.ChatBannedRights{
		ViewMessages:    r.ViewMessages,
		SendMessages:    r.SendMessages,
		SendMedia:       r.SendMedia,
		SendStickers:    r.SendStickers,
		SendGifs:        r.SendGifs,
		SendGames:       r.SendGames,
		SendInline:      r.SendInline,
		EmbedLinks:      r.EmbedLinks,
		SendPolls:       r.SendPolls,
		ChangeInfo:      r.ChangeInfo,
		InviteUsers:     r.InviteUsers,
		PinMessages:     r.PinMessages,
		ManageTopics:    r.ManageTopics,
		SendPhotos:      r.SendPhotos,
		SendVideos:      r.SendVideos,
		SendRoundvideos: r.SendRoundvideos,
		SendAudios:      r.SendAudios,
		SendVoices:      r.SendVoices,
		SendDocs:        r.SendDocs,
		SendPlain:       r.SendPlain,
		UntilDate:       int(r.UntilDate),
	}
}

// BannedRightsFromTG 从 tg.ChatBannedRights 提取 BannedRights。
func BannedRightsFromTG(r tg.ChatBannedRights) BannedRights {
	var until int64
	if r.UntilDate > 0 {
		until = int64(r.UntilDate)
	}
	return BannedRights{
		ViewMessages:    r.ViewMessages,
		SendMessages:    r.SendMessages,
		SendMedia:       r.SendMedia,
		SendStickers:    r.SendStickers,
		SendGifs:        r.SendGifs,
		SendGames:       r.SendGames,
		SendInline:      r.SendInline,
		EmbedLinks:      r.EmbedLinks,
		SendPolls:       r.SendPolls,
		ChangeInfo:      r.ChangeInfo,
		InviteUsers:     r.InviteUsers,
		PinMessages:     r.PinMessages,
		ManageTopics:    r.ManageTopics,
		SendPhotos:      r.SendPhotos,
		SendVideos:      r.SendVideos,
		SendRoundvideos: r.SendRoundvideos,
		SendAudios:      r.SendAudios,
		SendVoices:      r.SendVoices,
		SendDocs:        r.SendDocs,
		SendPlain:       r.SendPlain,
		UntilDate:       until,
	}
}

var knownTelethonErrorCodes = map[string]string{
	"CHAT_FORWARDS_RESTRICTED": "ChatForwardsRestrictedError",
	"MESSAGE_ID_INVALID":       "MessageIdInvalidError",
	"MESSAGE_IDS_EMPTY":        "MessageIdsEmptyError",
	"MESSAGE_EMPTY":            "MessageEmptyError",
	"MESSAGE_NOT_MODIFIED":     "MessageNotModifiedError",
	"MESSAGE_DELETE_FORBIDDEN": "MessageDeleteForbiddenError",
	"CHANNEL_PRIVATE":          "ChannelPrivateError",
	"CHANNEL_INVALID":          "ChannelInvalidError",
	"CHAT_ADMIN_REQUIRED":      "ChatAdminRequiredError",
	"CHAT_WRITE_FORBIDDEN":     "ChatWriteForbiddenError",
	"USER_NOT_PARTICIPANT":     "UserNotParticipantError",
	"USER_ALREADY_PARTICIPANT": "UserAlreadyParticipantError",
	"PARTICIPANT_ID_INVALID":   "ParticipantIdInvalidError",
	"USER_ID_INVALID":          "UserIdInvalidError",
	"USER_DEACTIVATED":         "UserDeactivatedError",
	"USER_DEACTIVATED_BAN":     "UserDeactivatedBanError",
}

// RPCErrorCode 将底层 MTProto RPC 错误归一为与 Telethon 兼容的异常类名（如 ChannelPrivateError、FloodWaitError）。
func RPCErrorCode(err error) string {
	if err == nil {
		return ""
	}
	var actErr *TelegramActionError
	if errors.As(err, &actErr) && actErr.Code != "" {
		return actErr.Code
	}
	var fwErr *FloodWaitError
	if errors.As(err, &fwErr) {
		return "FloodWaitError"
	}
	if _, ok := tgerr.AsFloodWait(err); ok {
		return "FloodWaitError"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "TimeoutError"
	}
	if errors.Is(err, context.Canceled) {
		return "CancelledError"
	}
	if rpcErr, ok := tgerr.As(err); ok && rpcErr != nil {
		if mapped, exists := knownTelethonErrorCodes[rpcErr.Type]; exists {
			return mapped
		}
		if rpcErr.Type != "" {
			return upperSnakeToTelethonError(rpcErr.Type)
		}
	}
	return "TelegramRPCError"
}

func upperSnakeToTelethonError(s string) string {
	parts := strings.Split(strings.ToLower(s), "_")
	var b strings.Builder
	for _, p := range parts {
		if p == "" {
			continue
		}
		b.WriteString(strings.ToUpper(p[:1]))
		if len(p) > 1 {
			b.WriteString(p[1:])
		}
	}
	if b.Len() == 0 {
		return "TelegramRPCError"
	}
	b.WriteString("Error")
	return b.String()
}

func normalizeCallError(err error) error {
	if err == nil {
		return nil
	}
	if d, ok := tgerr.AsFloodWait(err); ok {
		secs := max(1, int(d.Seconds()))
		return &FloodWaitError{Seconds: secs, Cause: err}
	}
	return err
}

func wrapActionError(err error) error {
	if err == nil {
		return nil
	}
	var existing *TelegramActionError
	if errors.As(err, &existing) {
		return err
	}
	norm := normalizeCallError(err)
	return &TelegramActionError{
		Code:  RPCErrorCode(norm),
		Cause: norm,
	}
}

// DeletedParticipantsPage 表示单页成员扫描结果（供已销号成员清理 deleted_cleanup 使用）。
type DeletedParticipantsPage struct {
	DeletedUsers []ProfileSummary `json:"deleted_users"`
	Scanned      int              `json:"scanned"`
	NextOffset   int              `json:"next_offset"`
	Total        int              `json:"total"`
	Done         bool             `json:"done"`
}

// DeletedHistoryPage 表示单页群历史消息扫描结果（供销号遗留历史清理 deleted_history 使用）。
type DeletedHistoryPage struct {
	DeletedMessageIDs []int            `json:"deleted_message_ids"`
	MessagesBySender  map[int64][]int  `json:"messages_by_sender"`
	DeletedSenders    []ProfileSummary `json:"deleted_senders,omitempty"`
	Scanned           int              `json:"scanned"`
	NextOffsetID      int              `json:"next_offset_id"`
	Done              bool             `json:"done"`
}

// GuardActions 封装 Sakura Guard 群管所需的带 25s 超时与错误分类的底层 MTProto 动作（对齐 core/moderation/telegram.py）。
type GuardActions struct {
	client  *Client
	timeout time.Duration
}

// NewGuardActions 创建绑定到指定 *Client 的 GuardActions。
func NewGuardActions(client *Client) *GuardActions {
	return &GuardActions{
		client:  client,
		timeout: GuardCallTimeout,
	}
}

// Client 返回底层 *Client。
func (g *GuardActions) Client() *Client { return g.client }

func (g *GuardActions) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	t := g.timeout
	if t <= 0 {
		t = GuardCallTimeout
	}
	return context.WithTimeout(ctx, t)
}

func (g *GuardActions) api() (*tg.Client, error) {
	if g == nil || g.client == nil || g.client.API() == nil {
		return nil, errors.New("guard mtproto client not initialized")
	}
	return g.client.API(), nil
}

// Permissions 查询指定用户在群组/超级群内的成员角色与封禁/管理权限（对齐 telegram.py:permissions）。
func (g *GuardActions) Permissions(ctx context.Context, chatID, userID int64) (*ParticipantPermissions, error) {
	api, err := g.api()
	if err != nil {
		return nil, err
	}
	if userID <= 0 {
		return nil, fmt.Errorf("invalid user_id %d", userID)
	}

	callCtx, cancel := g.withTimeout(ctx)
	defer cancel()

	kind, rawChatID := ParseMarkedPeerID(chatID)
	switch kind {
	case PeerKindChat:
		res, err := api.MessagesGetFullChat(callCtx, rawChatID)
		if err != nil {
			return nil, normalizeCallError(err)
		}
		g.client.Peers().ApplyUsersChats(res.Users, res.Chats)
		cf, ok := res.FullChat.(*tg.ChatFull)
		if !ok || cf == nil {
			return nil, errors.New("unexpected chat full type")
		}
		cp, ok := cf.Participants.(*tg.ChatParticipants)
		if !ok || cp == nil {
			return nil, errors.New("chat participants unavailable")
		}
		for _, pc := range cp.Participants {
			switch p := pc.(type) {
			case *tg.ChatParticipantCreator:
				if p.UserID == userID {
					return &ParticipantPermissions{
						UserID:            userID,
						IsMember:          true,
						IsAdmin:           true,
						IsCreator:         true,
						CanBanUsers:       true,
						CanDeleteMessages: true,
						CanInviteUsers:    true,
						CanPinMessages:    true,
					}, nil
				}
			case *tg.ChatParticipantAdmin:
				if p.UserID == userID {
					return &ParticipantPermissions{
						UserID:            userID,
						IsMember:          true,
						IsAdmin:           true,
						CanBanUsers:       true,
						CanDeleteMessages: true,
						CanInviteUsers:    true,
						CanPinMessages:    true,
						Date:              int64(p.Date),
					}, nil
				}
			case *tg.ChatParticipant:
				if p.UserID == userID {
					return &ParticipantPermissions{
						UserID:   userID,
						IsMember: true,
						Date:     int64(p.Date),
					}, nil
				}
			}
		}
		return nil, tgerr.New(400, "USER_NOT_PARTICIPANT")

	case PeerKindChannel:
		inputCh, err := g.client.Peers().ResolveInputChannel(callCtx, api, chatID)
		if err != nil {
			return nil, normalizeCallError(err)
		}
		userPeer, err := g.client.Peers().ResolveInputPeer(callCtx, nil, userID)
		if err != nil {
			userPeer = &tg.InputPeerUser{UserID: userID, AccessHash: 0}
		}

		res, err := api.ChannelsGetParticipant(callCtx, &tg.ChannelsGetParticipantRequest{
			Channel:     inputCh,
			Participant: userPeer,
		})
		if err != nil {
			return nil, normalizeCallError(err)
		}
		g.client.Peers().ApplyUsersChats(res.Users, res.Chats)
		return mapChannelParticipantPermissions(userID, res.Participant), nil

	default:
		return nil, fmt.Errorf("chat_id %d is not a group or channel", chatID)
	}
}

func mapChannelParticipantPermissions(userID int64, part tg.ChannelParticipantClass) *ParticipantPermissions {
	perms := &ParticipantPermissions{UserID: userID}
	switch p := part.(type) {
	case *tg.ChannelParticipant:
		perms.IsMember = true
		perms.Date = int64(p.Date)
	case *tg.ChannelParticipantSelf:
		perms.IsMember = true
		perms.Date = int64(p.Date)
	case *tg.ChannelParticipantCreator:
		perms.IsMember = true
		perms.IsAdmin = true
		perms.IsCreator = true
		perms.CanBanUsers = true
		perms.CanDeleteMessages = true
		perms.CanInviteUsers = true
		perms.CanPinMessages = true
	case *tg.ChannelParticipantAdmin:
		perms.IsMember = true
		perms.IsAdmin = true
		perms.CanBanUsers = p.AdminRights.BanUsers
		perms.CanDeleteMessages = p.AdminRights.DeleteMessages
		perms.CanInviteUsers = p.AdminRights.InviteUsers
		perms.CanPinMessages = p.AdminRights.PinMessages
		perms.Date = int64(p.Date)
	case *tg.ChannelParticipantBanned:
		br := BannedRightsFromTG(p.BannedRights)
		perms.IsMember = !p.Left && !p.BannedRights.ViewMessages
		perms.IsBanned = p.BannedRights.ViewMessages
		perms.KickedBy = p.KickedBy
		perms.Date = int64(p.Date)
		perms.BannedRights = br
	case *tg.ChannelParticipantLeft:
		perms.IsMember = false
	}
	return perms
}

// Rights 读取指定成员在群内的当前受限权限字典（对齐 telegram.py:rights）。
func (g *GuardActions) Rights(ctx context.Context, chatID, userID int64) (BannedRights, error) {
	perms, err := g.Permissions(ctx, chatID, userID)
	if err != nil {
		return BannedRights{}, err
	}
	return perms.BannedRights, nil
}

// SetRights 调用 channels.EditBanned 写入群成员限制位（对齐 telegram.py:set_rights）。
func (g *GuardActions) SetRights(ctx context.Context, chatID, userID int64, rights BannedRights) error {
	api, err := g.api()
	if err != nil {
		return wrapActionError(err)
	}

	callCtx, cancel := g.withTimeout(ctx)
	defer cancel()

	inputCh, err := g.client.Peers().ResolveInputChannel(callCtx, api, chatID)
	if err != nil {
		return wrapActionError(err)
	}
	participant, err := g.client.Peers().ResolveInputPeer(callCtx, api, userID)
	if err != nil {
		return wrapActionError(err)
	}

	upd, err := api.ChannelsEditBanned(callCtx, &tg.ChannelsEditBannedRequest{
		Channel:      inputCh,
		Participant:  participant,
		BannedRights: BannedRightsToTG(rights),
	})
	if err != nil {
		return wrapActionError(err)
	}
	g.client.Peers().ApplyUpdates(upd)
	return nil
}

// Ban 在超级群中设置 ViewMessages=true 永久封禁，或在基础普通群中调用 KickBasicGroup 移除成员（对齐 telegram.py:ban）。
func (g *GuardActions) Ban(ctx context.Context, chatID, userID int64) error {
	if kind, _ := ParseMarkedPeerID(chatID); kind == PeerKindChat {
		return g.KickBasicGroup(ctx, chatID, userID)
	}
	return g.SetRights(ctx, chatID, userID, BannedRights{
		ViewMessages: true,
		UntilDate:    0,
	})
}

// Unban 解除超级群成员封禁与限制（对齐 telegram.py:unban）。
func (g *GuardActions) Unban(ctx context.Context, chatID, userID int64) error {
	return g.SetRights(ctx, chatID, userID, BannedRights{})
}

// KickBasicGroup 在基础普通群中调用 messages.DeleteChatUser(RevokeHistory=false) 移除指定用户。
func (g *GuardActions) KickBasicGroup(ctx context.Context, chatID, userID int64) error {
	api, err := g.api()
	if err != nil {
		return wrapActionError(err)
	}

	callCtx, cancel := g.withTimeout(ctx)
	defer cancel()

	_, rawChatID := ParseMarkedPeerID(chatID)
	if rawChatID <= 0 {
		if chatID < 0 {
			rawChatID = -chatID
		} else {
			rawChatID = chatID
		}
	}
	inputUser, err := g.client.Peers().ResolveInputUser(callCtx, api, userID)
	if err != nil {
		return wrapActionError(err)
	}

	upd, err := api.MessagesDeleteChatUser(callCtx, &tg.MessagesDeleteChatUserRequest{
		RevokeHistory: false,
		ChatID:        rawChatID,
		UserID:        inputUser,
	})
	if err != nil {
		return wrapActionError(err)
	}
	g.client.Peers().ApplyUpdates(upd)
	return nil
}

// JoinRequest 批准或拒绝入群申请（messages.HideChatJoinRequest，对齐 telegram.py:join_request）。
func (g *GuardActions) JoinRequest(ctx context.Context, chatID, userID int64, approved bool) error {
	api, err := g.api()
	if err != nil {
		return wrapActionError(err)
	}

	callCtx, cancel := g.withTimeout(ctx)
	defer cancel()

	peer, err := g.client.Peers().ResolveInputPeer(callCtx, api, chatID)
	if err != nil {
		return wrapActionError(err)
	}
	inputUser, err := g.client.Peers().ResolveInputUser(callCtx, api, userID)
	if err != nil {
		return wrapActionError(err)
	}

	upd, err := api.MessagesHideChatJoinRequest(callCtx, &tg.MessagesHideChatJoinRequestRequest{
		Approved: approved,
		Peer:     peer,
		UserID:   inputUser,
	})
	if err != nil {
		return wrapActionError(err)
	}
	g.client.Peers().ApplyUpdates(upd)
	return nil
}

// AnswerJoinRequestQuery 响应入群申请实时回调查询（bots.SetJoinChatResults，对齐 telegram.py:answer_join_request_query）。
// action 取值："approve" -> JoinChatBotResultApproved, "reject" -> JoinChatBotResultDeclined, "manual" -> JoinChatBotResultQueued。
func (g *GuardActions) AnswerJoinRequestQuery(ctx context.Context, queryID int64, action string, expiresAt float64) error {
	api, err := g.api()
	if err != nil {
		return wrapActionError(err)
	}

	nowSec := float64(time.Now().UTC().UnixNano()) / 1e9
	remaining := expiresAt - nowSec
	if remaining <= 0 {
		return &TelegramActionError{Code: "JoinRequestQueryExpired"}
	}

	var result tg.JoinChatBotResultClass
	switch action {
	case "approve":
		result = &tg.JoinChatBotResultApproved{}
	case "reject":
		result = &tg.JoinChatBotResultDeclined{}
	case "manual":
		result = &tg.JoinChatBotResultQueued{}
	default:
		return &TelegramActionError{
			Code:  "InvalidJoinRequestAction",
			Cause: fmt.Errorf("unknown join request action %q", action),
		}
	}

	timeout := time.Duration(remaining * float64(time.Second))
	if timeout > GuardCallTimeout {
		timeout = GuardCallTimeout
	}
	qCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	accepted, err := api.BotsSetJoinChatResults(qCtx, &tg.BotsSetJoinChatResultsRequest{
		QueryID: queryID,
		Result:  result,
	})
	if err != nil {
		return wrapActionError(err)
	}
	if !accepted {
		return &TelegramActionError{Code: "JoinRequestQueryNotAnswered"}
	}
	return nil
}

// ForwardOnce 使用固定 randomID 执行幂等去重转发（对齐 telegram.py:forward_once）。
func (g *GuardActions) ForwardOnce(ctx context.Context, targetID, sourceChatID int64, messageID int, randomID int64) error {
	api, err := g.api()
	if err != nil {
		return wrapActionError(err)
	}

	callCtx, cancel := g.withTimeout(ctx)
	defer cancel()

	fromPeer, err := g.client.Peers().ResolveInputPeer(callCtx, api, sourceChatID)
	if err != nil {
		return wrapActionError(err)
	}
	toPeer, err := g.client.Peers().ResolveInputPeer(callCtx, api, targetID)
	if err != nil {
		return wrapActionError(err)
	}

	upd, err := api.MessagesForwardMessages(callCtx, &tg.MessagesForwardMessagesRequest{
		FromPeer: fromPeer,
		ID:       []int{messageID},
		RandomID: []int64{randomID},
		ToPeer:   toPeer,
	})
	if err != nil {
		return wrapActionError(err)
	}
	g.client.Peers().ApplyUpdates(upd)
	return nil
}

// SendOnce 使用固定 randomID 发送幂等去重通知消息并返回消息 ID（对齐 telegram.py:send_once）。
func (g *GuardActions) SendOnce(
	ctx context.Context,
	targetID int64,
	text string,
	buttons [][]InlineButton,
	randomID int64,
	entities []EntitySpan,
) (int, error) {
	api, err := g.api()
	if err != nil {
		return 0, wrapActionError(err)
	}

	callCtx, cancel := g.withTimeout(ctx)
	defer cancel()

	peer, err := g.client.Peers().ResolveInputPeer(callCtx, api, targetID)
	if err != nil {
		return 0, wrapActionError(err)
	}

	_, tgEntities := g.client.prepareTextAndEntities(callCtx, text, "plain", entities)

	req := &tg.MessagesSendMessageRequest{
		NoWebpage: true,
		Peer:      peer,
		Message:   text,
		RandomID:  randomID,
	}
	if markup := BuildInlineMarkup(buttons); markup != nil {
		req.SetReplyMarkup(markup)
	}
	if len(tgEntities) > 0 {
		req.SetEntities(tgEntities)
	}

	upd, err := api.MessagesSendMessage(callCtx, req)
	if err != nil {
		return 0, wrapActionError(err)
	}
	g.client.Peers().ApplyUpdates(upd)

	msgID := extractSentMessageID(upd, randomID)
	if msgID <= 0 {
		return 0, errors.New("Telegram did not return a notification message ID")
	}
	return msgID, nil
}

// LeaveChat 退出指定群组或频道（普通群使用 DeleteChatUser(InputUserSelf)，超级群/频道使用 LeaveChannel）。
func (g *GuardActions) LeaveChat(ctx context.Context, chatID int64) error {
	api, err := g.api()
	if err != nil {
		return wrapActionError(err)
	}

	callCtx, cancel := g.withTimeout(ctx)
	defer cancel()

	kind, rawID := ParseMarkedPeerID(chatID)
	if kind == PeerKindChat {
		upd, err := api.MessagesDeleteChatUser(callCtx, &tg.MessagesDeleteChatUserRequest{
			ChatID: rawID,
			UserID: &tg.InputUserSelf{},
		})
		if err != nil {
			return wrapActionError(err)
		}
		g.client.Peers().ApplyUpdates(upd)
		return nil
	}

	inputCh, err := g.client.Peers().ResolveInputChannel(callCtx, api, chatID)
	if err != nil {
		return wrapActionError(err)
	}
	upd, err := api.ChannelsLeaveChannel(callCtx, inputCh)
	if err != nil {
		return wrapActionError(err)
	}
	g.client.Peers().ApplyUpdates(upd)
	return nil
}

// GetFullUserBio 调用 users.GetFullUser 获取目标用户的完整资料摘要（含 Bio 个人简介）。
func (g *GuardActions) GetFullUserBio(ctx context.Context, userID int64) (*ProfileSummary, error) {
	api, err := g.api()
	if err != nil {
		return nil, err
	}
	if userID <= 0 {
		return nil, fmt.Errorf("invalid user_id %d", userID)
	}

	callCtx, cancel := g.withTimeout(ctx)
	defer cancel()

	inputUser, err := g.client.Peers().ResolveInputUser(callCtx, api, userID)
	if err != nil {
		return nil, normalizeCallError(err)
	}

	res, err := api.UsersGetFullUser(callCtx, inputUser)
	if err != nil {
		return nil, normalizeCallError(err)
	}
	g.client.Peers().ApplyUsersChats(res.Users, res.Chats)

	prof := ProfileSummary{
		ID:  userID,
		Bio: res.FullUser.About,
	}
	for _, uc := range res.Users {
		if u, ok := uc.(*tg.User); ok && u.ID == userID {
			prof = tgUserToProfileSummary(u)
			prof.Bio = res.FullUser.About
			break
		}
	}
	return &prof, nil
}

// ScanDeletedParticipants 分页扫描群内已销号（deleted=true）的活跃成员（对齐 deleted_cleanup.py:_scan）。
func (g *GuardActions) ScanDeletedParticipants(ctx context.Context, chatID int64, offset, limit int) (*DeletedParticipantsPage, error) {
	api, err := g.api()
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 200
	}

	callCtx, cancel := g.withTimeout(ctx)
	defer cancel()

	selfID := g.client.SelfID()
	kind, rawID := ParseMarkedPeerID(chatID)

	if kind == PeerKindChat {
		res, err := api.MessagesGetFullChat(callCtx, rawID)
		if err != nil {
			return nil, normalizeCallError(err)
		}
		g.client.Peers().ApplyUsersChats(res.Users, res.Chats)

		cf, ok := res.FullChat.(*tg.ChatFull)
		if !ok || cf == nil {
			return nil, errors.New("Telegram 未提供成员列表，本群未完成扫描")
		}
		cp, ok := cf.Participants.(*tg.ChatParticipants)
		if !ok || cp == nil {
			return nil, errors.New("Telegram 未提供成员列表，本群未完成扫描")
		}

		activeIDs := make(map[int64]struct{}, len(cp.Participants))
		for _, pc := range cp.Participants {
			if uid, _ := chatParticipantActiveUserID(pc); uid > 0 {
				activeIDs[uid] = struct{}{}
			}
		}

		var deleted []ProfileSummary
		for _, uc := range res.Users {
			u, ok := uc.(*tg.User)
			if !ok || u == nil || !u.Deleted || u.ID <= 0 || u.ID == selfID {
				continue
			}
			if _, isMember := activeIDs[u.ID]; isMember {
				deleted = append(deleted, tgUserToProfileSummary(u))
			}
		}
		return &DeletedParticipantsPage{
			DeletedUsers: deleted,
			Scanned:      len(cp.Participants),
			NextOffset:   len(cp.Participants),
			Total:        len(cp.Participants),
			Done:         true,
		}, nil
	}

	inputCh, err := g.client.Peers().ResolveInputChannel(callCtx, api, chatID)
	if err != nil {
		return nil, normalizeCallError(err)
	}

	res, err := api.ChannelsGetParticipants(callCtx, &tg.ChannelsGetParticipantsRequest{
		Channel: inputCh,
		Filter:  &tg.ChannelParticipantsSearch{Q: ""},
		Offset:  offset,
		Limit:   limit,
		Hash:    0,
	})
	if err != nil {
		return nil, normalizeCallError(err)
	}

	cp, ok := res.(*tg.ChannelsChannelParticipants)
	if !ok || cp == nil {
		return &DeletedParticipantsPage{Done: true}, nil
	}
	g.client.Peers().ApplyUsersChats(cp.Users, cp.Chats)

	activeIDs := make(map[int64]struct{}, len(cp.Participants))
	for _, pc := range cp.Participants {
		if uid := channelParticipantActiveUserID(pc); uid > 0 {
			activeIDs[uid] = struct{}{}
		}
	}

	var deleted []ProfileSummary
	for _, uc := range cp.Users {
		u, ok := uc.(*tg.User)
		if !ok || u == nil || !u.Deleted || u.ID <= 0 || u.ID == selfID {
			continue
		}
		if _, isMember := activeIDs[u.ID]; isMember {
			deleted = append(deleted, tgUserToProfileSummary(u))
		}
	}

	nextOffset := offset + len(cp.Participants)
	done := len(cp.Participants) == 0 || nextOffset >= cp.Count
	return &DeletedParticipantsPage{
		DeletedUsers: deleted,
		Scanned:      len(cp.Participants),
		NextOffset:   nextOffset,
		Total:        cp.Count,
		Done:         done,
	}, nil
}

// ScanHistoryDeletedSenders 逐页扫描群组历史消息中的作者归属及已销号发送者（对齐 deleted_history.py:page）。
func (g *GuardActions) ScanHistoryDeletedSenders(ctx context.Context, chatID int64, offsetID, limit int) (*DeletedHistoryPage, error) {
	api, err := g.api()
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 100
	}

	callCtx, cancel := g.withTimeout(ctx)
	defer cancel()

	peer, err := g.client.Peers().ResolveInputPeer(callCtx, api, chatID)
	if err != nil {
		return nil, normalizeCallError(err)
	}

	res, err := api.MessagesGetHistory(callCtx, &tg.MessagesGetHistoryRequest{
		Peer:     peer,
		OffsetID: offsetID,
		Limit:    limit,
	})
	if err != nil {
		return nil, normalizeCallError(err)
	}

	rawMsgs, users, chats := unpackMessagesClass(res)
	g.client.Peers().ApplyUsersChats(users, chats)

	deletedUsers := make(map[int64]ProfileSummary)
	for _, uc := range users {
		if u, ok := uc.(*tg.User); ok && u != nil && u.Deleted && u.ID > 0 {
			deletedUsers[u.ID] = tgUserToProfileSummary(u)
		}
	}

	page := &DeletedHistoryPage{
		MessagesBySender: make(map[int64][]int),
	}

	minID := 0
	nonEmptyCount := 0
	seenDeletedSender := make(map[int64]struct{})

	for _, mc := range rawMsgs {
		var (
			msgID  int
			peerID tg.PeerClass
			fromID tg.PeerClass
		)
		switch m := mc.(type) {
		case *tg.Message:
			if m == nil {
				continue
			}
			msgID = m.ID
			peerID = m.PeerID
			fromID, _ = m.GetFromID()
		case *tg.MessageService:
			if m == nil {
				continue
			}
			msgID = m.ID
			peerID = m.PeerID
			fromID, _ = m.GetFromID()
		default:
			continue
		}

		if PeerMarkedID(peerID) != chatID {
			return nil, errors.New("历史消息群组不匹配，未删除")
		}

		nonEmptyCount++
		if msgID > 0 && (minID == 0 || msgID < minID) {
			minID = msgID
		}

		if pu, ok := fromID.(*tg.PeerUser); ok && pu.UserID > 0 {
			page.MessagesBySender[pu.UserID] = append(page.MessagesBySender[pu.UserID], msgID)
			if prof, isDel := deletedUsers[pu.UserID]; isDel {
				page.DeletedMessageIDs = append(page.DeletedMessageIDs, msgID)
				if _, exists := seenDeletedSender[pu.UserID]; !exists {
					seenDeletedSender[pu.UserID] = struct{}{}
					page.DeletedSenders = append(page.DeletedSenders, prof)
				}
			}
		}
	}

	if nonEmptyCount == 0 {
		page.Done = true
		return page, nil
	}
	if offsetID > 0 && minID >= offsetID {
		return nil, errors.New("历史消息分页未前进，停止清理")
	}

	page.Scanned = nonEmptyCount
	page.NextOffsetID = minID
	page.Done = false
	return page, nil
}
