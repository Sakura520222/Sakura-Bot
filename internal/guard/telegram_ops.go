// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package guard

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	guarddb "github.com/Sakura520222/Sakura-Bot/internal/db/guard"
	"github.com/Sakura520222/Sakura-Bot/internal/mtproto"
	"github.com/Sakura520222/Sakura-Bot/internal/tg"
)

// TelegramOps 抽象 Sakura Guard 所需的全部 MTProto 群管 RPC 与 Bot API 销历史接口。
type TelegramOps interface {
	BotID() int64
	KnownGroupPeers() []mtproto.CachedPeer
	ResolveChannel(ctx context.Context, ref string) (*mtproto.ChannelInfo, error)
	Permissions(ctx context.Context, chatID, userID int64) (*mtproto.ParticipantPermissions, error)
	Rights(ctx context.Context, chatID, userID int64) (mtproto.BannedRights, error)
	SetRights(ctx context.Context, chatID, userID int64, rights mtproto.BannedRights) error
	Ban(ctx context.Context, chatID, userID int64) error
	Unban(ctx context.Context, chatID, userID int64) error
	KickBasicGroup(ctx context.Context, chatID, userID int64) error
	DeleteMessages(ctx context.Context, chatID int64, messageIDs []int) error
	GetMessages(ctx context.Context, chatID int64, messageIDs []int) ([]*mtproto.IncomingMessage, error)
	DownloadMediaBytes(ctx context.Context, media *mtproto.MediaAttachment, maxBytes int64) ([]byte, error)
	SendMessage(ctx context.Context, target any, text string, opts mtproto.SendOptions) (int, error)
	SendOnce(ctx context.Context, targetID int64, text string, opts mtproto.SendOptions, randomID int64) (int, error)
	EditMessage(ctx context.Context, target any, messageID int, text string, opts mtproto.SendOptions) error
	ForwardOnce(ctx context.Context, targetID, sourceChatID int64, messageID int, randomID int64) error
	AnswerCallback(ctx context.Context, queryID int64, message string, alert bool) error
	JoinRequest(ctx context.Context, chatID, userID int64, approved bool) error
	AnswerJoinRequestQuery(ctx context.Context, queryID int64, action string, expiresAt float64) error
	LeaveChat(ctx context.Context, chatID int64) error
	GetFullUserBio(ctx context.Context, userID int64) (*mtproto.ProfileSummary, error)
	ScanDeletedParticipants(ctx context.Context, chatID int64, offset, limit int) (*mtproto.DeletedParticipantsPage, error)
	ScanHistoryDeletedSenders(ctx context.Context, chatID int64, offsetID, limit int) (*mtproto.DeletedHistoryPage, error)
	RevokeMessagesViaBotAPI(ctx context.Context, botToken string, chatID, userID int64) error
}

// MTProtoTelegramOps 将 *mtproto.Client、*mtproto.GuardActions 与 *tg.DirectBotAPI 适配为 TelegramOps。
type MTProtoTelegramOps struct {
	client  *mtproto.Client
	actions *mtproto.GuardActions
	botAPI  *tg.DirectBotAPI
}

// NewMTProtoTelegramOps 构造生产环境 TelegramOps 适配器。
func NewMTProtoTelegramOps(client *mtproto.Client, botAPI *tg.DirectBotAPI) *MTProtoTelegramOps {
	if botAPI == nil {
		botAPI = tg.NewDirectBotAPI()
	}
	var actions *mtproto.GuardActions
	if client != nil {
		actions = mtproto.NewGuardActions(client)
	}
	return &MTProtoTelegramOps{
		client:  client,
		actions: actions,
		botAPI:  botAPI,
	}
}

func (o *MTProtoTelegramOps) BotID() int64 {
	if o == nil || o.client == nil {
		return 0
	}
	return o.client.SelfID()
}

func (o *MTProtoTelegramOps) KnownGroupPeers() []mtproto.CachedPeer {
	if o == nil || o.client == nil || o.client.Peers() == nil {
		return nil
	}
	return o.client.Peers().KnownGroupEntities()
}

func (o *MTProtoTelegramOps) ResolveChannel(ctx context.Context, ref string) (*mtproto.ChannelInfo, error) {
	return o.client.ResolveChannel(ctx, ref)
}

func (o *MTProtoTelegramOps) Permissions(ctx context.Context, chatID, userID int64) (*mtproto.ParticipantPermissions, error) {
	return o.actions.Permissions(ctx, chatID, userID)
}

func (o *MTProtoTelegramOps) Rights(ctx context.Context, chatID, userID int64) (mtproto.BannedRights, error) {
	return o.actions.Rights(ctx, chatID, userID)
}

func (o *MTProtoTelegramOps) SetRights(ctx context.Context, chatID, userID int64, rights mtproto.BannedRights) error {
	return o.actions.SetRights(ctx, chatID, userID, rights)
}

func (o *MTProtoTelegramOps) Ban(ctx context.Context, chatID, userID int64) error {
	return o.actions.Ban(ctx, chatID, userID)
}

func (o *MTProtoTelegramOps) Unban(ctx context.Context, chatID, userID int64) error {
	return o.actions.Unban(ctx, chatID, userID)
}

func (o *MTProtoTelegramOps) KickBasicGroup(ctx context.Context, chatID, userID int64) error {
	return o.actions.KickBasicGroup(ctx, chatID, userID)
}

func (o *MTProtoTelegramOps) DeleteMessages(ctx context.Context, chatID int64, messageIDs []int) error {
	return o.client.DeleteMessages(ctx, chatID, messageIDs)
}

func (o *MTProtoTelegramOps) GetMessages(ctx context.Context, chatID int64, messageIDs []int) ([]*mtproto.IncomingMessage, error) {
	return o.client.GetMessages(ctx, chatID, messageIDs)
}

func (o *MTProtoTelegramOps) DownloadMediaBytes(ctx context.Context, media *mtproto.MediaAttachment, maxBytes int64) ([]byte, error) {
	return o.client.DownloadMediaBytes(ctx, media, maxBytes)
}

func (o *MTProtoTelegramOps) SendMessage(ctx context.Context, target any, text string, opts mtproto.SendOptions) (int, error) {
	return o.client.SendMessage(ctx, target, text, opts)
}

func (o *MTProtoTelegramOps) SendOnce(ctx context.Context, targetID int64, text string, opts mtproto.SendOptions, randomID int64) (int, error) {
	return o.actions.SendOnce(ctx, targetID, text, opts, randomID)
}

func (o *MTProtoTelegramOps) EditMessage(ctx context.Context, target any, messageID int, text string, opts mtproto.SendOptions) error {
	return o.client.EditMessage(ctx, target, messageID, text, opts)
}

func (o *MTProtoTelegramOps) ForwardOnce(ctx context.Context, targetID, sourceChatID int64, messageID int, randomID int64) error {
	return o.actions.ForwardOnce(ctx, targetID, sourceChatID, messageID, randomID)
}

func (o *MTProtoTelegramOps) AnswerCallback(ctx context.Context, queryID int64, message string, alert bool) error {
	return o.client.AnswerCallback(ctx, queryID, message, alert)
}

func (o *MTProtoTelegramOps) JoinRequest(ctx context.Context, chatID, userID int64, approved bool) error {
	return o.actions.JoinRequest(ctx, chatID, userID, approved)
}

func (o *MTProtoTelegramOps) AnswerJoinRequestQuery(ctx context.Context, queryID int64, action string, expiresAt float64) error {
	return o.actions.AnswerJoinRequestQuery(ctx, queryID, action, expiresAt)
}

func (o *MTProtoTelegramOps) LeaveChat(ctx context.Context, chatID int64) error {
	return o.actions.LeaveChat(ctx, chatID)
}

func (o *MTProtoTelegramOps) GetFullUserBio(ctx context.Context, userID int64) (*mtproto.ProfileSummary, error) {
	return o.actions.GetFullUserBio(ctx, userID)
}

func (o *MTProtoTelegramOps) ScanDeletedParticipants(ctx context.Context, chatID int64, offset, limit int) (*mtproto.DeletedParticipantsPage, error) {
	return o.actions.ScanDeletedParticipants(ctx, chatID, offset, limit)
}

func (o *MTProtoTelegramOps) ScanHistoryDeletedSenders(ctx context.Context, chatID int64, offsetID, limit int) (*mtproto.DeletedHistoryPage, error) {
	return o.actions.ScanHistoryDeletedSenders(ctx, chatID, offsetID, limit)
}

func (o *MTProtoTelegramOps) RevokeMessagesViaBotAPI(ctx context.Context, botToken string, chatID, userID int64) error {
	return o.botAPI.BanChatMember(ctx, botToken, chatID, userID, true, 0)
}

// ────────────────────────────── 内部通用工具 ──────────────────────────────

func newUUIDHex() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		sum := sha256.Sum256([]byte(strconv.FormatInt(time.Now().UnixNano(), 10)))
		return hex.EncodeToString(sum[:16])
	}
	return hex.EncodeToString(b[:])
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func rowPayload(row guarddb.Row) map[string]any {
	if row == nil {
		return map[string]any{}
	}
	switch p := row["payload"].(type) {
	case map[string]any:
		return p
	case string:
		var m map[string]any
		_ = json.Unmarshal([]byte(p), &m)
		if m != nil {
			return m
		}
	case []byte:
		var m map[string]any
		_ = json.Unmarshal(p, &m)
		if m != nil {
			return m
		}
	}
	return map[string]any{}
}

func rowStr(row map[string]any, key string) string {
	if row == nil {
		return ""
	}
	switch v := row[key].(type) {
	case string:
		return v
	case []byte:
		return string(v)
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", v)
	}
}

func rowInt64(row map[string]any, key string) int64 {
	if row == nil {
		return 0
	}
	switch v := row[key].(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case float64:
		return int64(v)
	case json.Number:
		n, _ := v.Int64()
		return n
	case string:
		n, _ := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		return n
	default:
		return 0
	}
}

func rowBool(row map[string]any, key string) bool {
	if row == nil {
		return false
	}
	switch v := row[key].(type) {
	case bool:
		return v
	case int64:
		return v != 0
	case int:
		return v != 0
	case float64:
		return v != 0
	case string:
		s := strings.ToLower(strings.TrimSpace(v))
		return s == "true" || s == "1"
	default:
		return false
	}
}

func isBasicGroupID(chatID int64) bool {
	kind, _ := mtproto.ParseMarkedPeerID(chatID)
	return kind == mtproto.PeerKindChat
}
