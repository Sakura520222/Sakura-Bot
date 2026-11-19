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
	"fmt"
	"time"
)

// InlineButton 表示 Telegram 内联键盘按钮（Data 与 URL 二选一）。
type InlineButton struct {
	Text string `json:"text"`
	Data string `json:"data,omitempty"`
	URL  string `json:"url,omitempty"`
}

// BotCommandDef 表示注册到 Telegram 客户端菜单的斜杠命令。
type BotCommandDef struct {
	Command     string `json:"command"`
	Description string `json:"description"`
}

// EntitySpan 表示消息富文本实体（Offset 与 Length 按 Telegram 规范采用 UTF-16 code units）。
type EntitySpan struct {
	Kind     string `json:"kind"` // "bold"|"italic"|"code"|"pre"|"text_url"|"url"|"strike"|"underline"|"spoiler"|"mention"|"text_mention"
	Offset   int    `json:"offset"`
	Length   int    `json:"length"`
	URL      string `json:"url,omitempty"`
	Language string `json:"language,omitempty"`
	UserID   int64  `json:"user_id,omitempty"`
}

// ProfileSummary 表示用户或群组实体的基础摘要。
type ProfileSummary struct {
	ID              int64    `json:"id"`
	AccessHash      int64    `json:"access_hash,omitempty"`
	IsBot           bool     `json:"is_bot,omitempty"`
	IsDeleted       bool     `json:"is_deleted,omitempty"`
	FirstName       string   `json:"first_name,omitempty"`
	LastName        string   `json:"last_name,omitempty"`
	Username        string   `json:"username,omitempty"`
	ActiveUsernames []string `json:"active_usernames,omitempty"`
	Phone           string   `json:"phone,omitempty"`
	HasPhoto        bool     `json:"has_photo,omitempty"`
	Bio             string   `json:"bio,omitempty"`
}

// MediaAttachment 表示消息携带的媒体附件元数据及底层句柄。
type MediaAttachment struct {
	Kind             string `json:"kind"` // "photo"|"document"|"video"|"audio"|"voice"|"sticker"|"gif"|"contact"|"poll"|"webpage"|"other"
	MediaID          string `json:"media_id,omitempty"`
	FileSize         int64  `json:"file_size,omitempty"`
	MimeType         string `json:"mime_type,omitempty"`
	FileName         string `json:"file_name,omitempty"`
	Width            int    `json:"width,omitempty"`
	Height           int    `json:"height,omitempty"`
	ContactFirstName string `json:"contact_first_name,omitempty"`
	ContactLastName  string `json:"contact_last_name,omitempty"`
	ContactPhone     string `json:"contact_phone,omitempty"`
	ContactUserID    int64  `json:"contact_user_id,omitempty"`
	Raw              any    `json:"-"` // 底层 *tg.MessageMediaPhoto / *tg.MessageMediaDocument 等，供 DownloadMedia 使用
}

// IsStaticImage 判断附件是否为可做视觉识别的静态图片（Photo 或 image/png、image/jpeg 文档）。
func (m *MediaAttachment) IsStaticImage() bool {
	if m == nil {
		return false
	}
	if m.Kind == "photo" {
		return true
	}
	if m.Kind == "document" {
		return m.MimeType == "image/png" || m.MimeType == "image/jpeg"
	}
	return false
}

// ForwardHeader 表示转发来源头信息（对齐 Telethon MessageFwdHeader）。
type ForwardHeader struct {
	FromID          int64     `json:"from_id,omitempty"` // Marked peer ID
	FromName        string    `json:"from_name,omitempty"`
	ChannelPost     int       `json:"channel_post,omitempty"`
	SavedFromPeerID int64     `json:"saved_from_peer_id,omitempty"`
	SavedFromMsgID  int       `json:"saved_from_msg_id,omitempty"`
	PostAuthor      string    `json:"post_author,omitempty"`
	Date            time.Time `json:"date"`
}

// IncomingMessage 是实时事件与单条消息查询统一使用的标准化消息结构。
type IncomingMessage struct {
	ID            int              `json:"id"`
	ChatID        int64            `json:"chat_id"` // Bot API 标记 ID（频道/超群 -100xxx，普通群 -xxx，私聊 +xxx）
	ChatTitle     string           `json:"chat_title,omitempty"`
	ChatUsername  string           `json:"chat_username,omitempty"`
	IsBroadcast   bool             `json:"is_broadcast,omitempty"`
	IsSupergroup  bool             `json:"is_supergroup,omitempty"`
	IsBasicGroup  bool             `json:"is_basic_group,omitempty"`
	IsPrivate     bool             `json:"is_private,omitempty"`
	IsOut         bool             `json:"is_out,omitempty"`
	SenderID      int64            `json:"sender_id,omitempty"` // 频道广播或匿名发言可能为 0
	SenderChatID  int64            `json:"sender_chat_id,omitempty"`
	Sender        ProfileSummary   `json:"sender"`
	ViaBotID      int64            `json:"via_bot_id,omitempty"`
	GuestCallerID int64            `json:"guest_caller_id,omitempty"`
	Text          string           `json:"text"`
	Date          time.Time        `json:"date"`
	EditDate      *time.Time       `json:"edit_date,omitempty"`
	GroupedID     int64            `json:"grouped_id,omitempty"`
	ReplyToMsgID  int              `json:"reply_to_msg_id,omitempty"`
	ReplyToTopID  int              `json:"reply_to_top_id,omitempty"`
	FwdFrom       *ForwardHeader   `json:"fwd_from,omitempty"`
	Entities      []EntitySpan     `json:"entities,omitempty"`
	HasButtons    bool             `json:"has_buttons,omitempty"`
	Media         *MediaAttachment `json:"media,omitempty"`
	PostLink      string           `json:"post_link,omitempty"`
	Raw           any              `json:"-"`
}

// CallbackEvent 表示内联按钮回调查询事件。
type CallbackEvent struct {
	QueryID   int64          `json:"query_id"`
	SenderID  int64          `json:"sender_id"`
	Sender    ProfileSummary `json:"sender"`
	ChatID    int64          `json:"chat_id"`
	MessageID int            `json:"message_id"`
	Data      []byte         `json:"data"`
}

// DataString 返回回调载荷字符串。
func (e CallbackEvent) DataString() string { return string(e.Data) }

// JoinRequestEvent 表示入群申请事件（UpdateBotChatInviteRequester）。
type JoinRequestEvent struct {
	ChatID     int64          `json:"chat_id"`
	UserID     int64          `json:"user_id"`
	User       ProfileSummary `json:"user"`
	Bio        string         `json:"bio,omitempty"`
	Date       time.Time      `json:"date"`
	QueryID    int64          `json:"query_id,omitempty"`
	HasQueryID bool           `json:"has_query_id,omitempty"`
	QTS        int            `json:"qts,omitempty"`
}

// UserNameEvent 表示用户昵称/用户名变更事件（UpdateUserName）。
type UserNameEvent struct {
	UserID          int64    `json:"user_id"`
	FirstName       string   `json:"first_name"`
	LastName        string   `json:"last_name"`
	Username        string   `json:"username"`
	ActiveUsernames []string `json:"active_usernames"`
}

// DeletedMessagesEvent 表示消息删除事件（UpdateDeleteChannelMessages / UpdateDeleteMessages）。
type DeletedMessagesEvent struct {
	ChatID     int64 `json:"chat_id"` // 若全局普通消息删除未知会话则为 0
	ChannelID  int64 `json:"channel_id,omitempty"`
	MessageIDs []int `json:"message_ids"`
}

// ParticipantEvent 表示群成员变动或权限变更事件。
type ParticipantEvent struct {
	ChatID           int64          `json:"chat_id"`
	ActorID          int64          `json:"actor_id"`
	UserID           int64          `json:"user_id"`
	User             ProfileSummary `json:"user"`
	Kind             string         `json:"kind"` // "join"|"add"|"leave"|"kick"|"permissions"|"admin"
	WasPresent       bool           `json:"was_present"`
	IsPresent        bool           `json:"is_present"`
	IsBanned         bool           `json:"is_banned"`
	IsAdmin          bool           `json:"is_admin"`
	KickedBy         int64          `json:"kicked_by,omitempty"`
	BannedRights     BannedRights   `json:"banned_rights,omitempty"`
	PrevBannedRights BannedRights   `json:"prev_banned_rights,omitempty"`
	Date             time.Time      `json:"date"`
	QTS              int            `json:"qts,omitempty"`
	ActionMessageID  int            `json:"action_message_id,omitempty"`
}

// SendOptions 控制文本消息发送行为。
type SendOptions struct {
	ParseMode    string           // "" | "markdown" | "html" | "plain"
	ReplyToMsgID int              // >0 时作为回复
	Buttons      [][]InlineButton // 可选内联键盘
	NoWebpage    bool             // 禁用链接预览
	Silent       bool             // 静默推送
	Entities     []EntitySpan     // 显式实体（非空时优先于 ParseMode）
}

// PollSendOptions 控制原生投票发送参数。
type PollSendOptions struct {
	Question       string
	Options        []string
	PublicVoters   bool
	MultipleChoice bool
	ReplyToMsgID   int
	Buttons        [][]InlineButton
}

// MediaUpload 描述待上传发送的单个媒体项（用于转发重传、投稿发布等）。
type MediaUpload struct {
	Data          []byte // 内存数据（与 FilePath 二选一）
	FilePath      string // 本地文件路径
	FileName      string
	MimeType      string
	ForceDocument bool
	Caption       string
	ParseMode     string
}

// ChannelInfo 描述频道/超级群的解析详情。
type ChannelInfo struct {
	ID              int64    `json:"id"`        // 原始正整数 Channel ID
	MarkedID        int64    `json:"marked_id"` // -100<ID> 或 -<ID>
	AccessHash      int64    `json:"access_hash,omitempty"`
	Title           string   `json:"title"`
	Username        string   `json:"username,omitempty"`
	ActiveUsernames []string `json:"active_usernames,omitempty"`
	Broadcast       bool     `json:"broadcast"`
	Megagroup       bool     `json:"megagroup"`
	Forum           bool     `json:"forum,omitempty"`
	Monoforum       bool     `json:"monoforum,omitempty"`
	Restricted      bool     `json:"restricted,omitempty"`
	NoForwards      bool     `json:"noforwards,omitempty"`
	LinkedChatID    int64    `json:"linked_chat_id,omitempty"` // 关联讨论群或频道的 MarkedID（无则为 0）
	PhotoID         int64    `json:"photo_id,omitempty"`
}

// UserBotStatus 表示 UserBot 运行时状态（对齐 WebUI /api/userbot/status 与 get_status）。
type UserBotStatus struct {
	Available     bool            `json:"available"`
	Connected     bool            `json:"connected"`
	Initialized   bool            `json:"initialized"`
	Enabled       bool            `json:"enabled"`
	SessionPath   string          `json:"session_path,omitempty"`
	PhoneNumber   string          `json:"phone_number,omitempty"`
	PhonePreview  string          `json:"phone_preview,omitempty"`
	FallbackToBot bool            `json:"fallback_to_bot"`
	UserInfo      *ProfileSummary `json:"user_info,omitempty"`
	Error         string          `json:"error,omitempty"`
}

// JoinLeaveResult 表示 UserBot 加入/退出频道结果。
type JoinLeaveResult struct {
	Success       bool         `json:"success"`
	Message       string       `json:"message"`
	Error         string       `json:"error,omitempty"`
	Channel       string       `json:"channel,omitempty"`
	AlreadyJoined bool         `json:"already_joined,omitempty"`
	NotJoined     bool         `json:"not_joined,omitempty"`
	ChannelInfo   *ChannelInfo `json:"channel_info,omitempty"`
}

// BackfilledComment 表示 UserBot 回溯讨论群帖子得到的单条可见评论。
type BackfilledComment struct {
	ChatID      int64     `json:"chat_id"`
	MessageID   int64     `json:"message_id"`
	RootID      int64     `json:"root_id"`
	ReplyID     int64     `json:"reply_id,omitempty"`
	Channel     string    `json:"channel"`
	UserID      int64     `json:"user_id"`
	AuthorName  string    `json:"author_name"`
	Username    string    `json:"username,omitempty"`
	AuthorRole  string    `json:"author_role"` // "user"|"admin"|"channel"|"assistant"|"bot"
	Text        string    `json:"text"`
	MediaLabel  string    `json:"media_label,omitempty"`
	MessageDate time.Time `json:"message_date"`
	Edited      bool      `json:"edited,omitempty"`
}

// VerificationRestrictionKeys 列举入群验证冻结的全部 15 项发言权限位（对齐 core/moderation/telegram.py:29-45）。
var VerificationRestrictionKeys = []string{
	"send_messages",
	"send_media",
	"send_plain",
	"send_stickers",
	"send_gifs",
	"send_games",
	"send_inline",
	"embed_links",
	"send_polls",
	"send_photos",
	"send_videos",
	"send_roundvideos",
	"send_audios",
	"send_voices",
	"send_docs",
}

// BannedRights 表示群成员受限权限字典（布尔位 + until_date Unix 秒时间戳）。
type BannedRights struct {
	ViewMessages    bool  `json:"view_messages"`
	SendMessages    bool  `json:"send_messages"`
	SendMedia       bool  `json:"send_media"`
	SendStickers    bool  `json:"send_stickers"`
	SendGifs        bool  `json:"send_gifs"`
	SendGames       bool  `json:"send_games"`
	SendInline      bool  `json:"send_inline"`
	EmbedLinks      bool  `json:"embed_links"`
	SendPolls       bool  `json:"send_polls"`
	ChangeInfo      bool  `json:"change_info"`
	InviteUsers     bool  `json:"invite_users"`
	PinMessages     bool  `json:"pin_messages"`
	ManageTopics    bool  `json:"manage_topics"`
	SendPhotos      bool  `json:"send_photos"`
	SendVideos      bool  `json:"send_videos"`
	SendRoundvideos bool  `json:"send_roundvideos"`
	SendAudios      bool  `json:"send_audios"`
	SendVoices      bool  `json:"send_voices"`
	SendDocs        bool  `json:"send_docs"`
	SendPlain       bool  `json:"send_plain"`
	UntilDate       int64 `json:"until_date"`
}

// ParticipantPermissions 描述群成员在指定会话内的角色与权限快照。
type ParticipantPermissions struct {
	UserID            int64        `json:"user_id"`
	IsMember          bool         `json:"is_member"`
	IsAdmin           bool         `json:"is_admin"`
	IsCreator         bool         `json:"is_creator"`
	IsBanned          bool         `json:"is_banned"`
	CanBanUsers       bool         `json:"can_ban_users"`
	CanDeleteMessages bool         `json:"can_delete_messages"`
	CanInviteUsers    bool         `json:"can_invite_users"`
	CanPinMessages    bool         `json:"can_pin_messages"`
	KickedBy          int64        `json:"kicked_by,omitempty"`
	Date              int64        `json:"date,omitempty"`
	BannedRights      BannedRights `json:"banned_rights"`
}

// TelegramActionError 专用于标记 Telegram RPC 副作用失败（与本地数据库错误区分，对齐 telegram.py:98）。
type TelegramActionError struct {
	Code  string
	Cause error
}

func (e *TelegramActionError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %v", e.Code, e.Cause)
	}
	return e.Code
}

func (e *TelegramActionError) Unwrap() error { return e.Cause }

// FloodWaitError 表示 Telegram FLOOD_WAIT_X 限流错误。
type FloodWaitError struct {
	Seconds int
	Cause   error
}

func (e *FloodWaitError) Error() string {
	return fmt.Sprintf("FLOOD_WAIT_%d", e.Seconds)
}

func (e *FloodWaitError) Unwrap() error { return e.Cause }

// Messenger 抽象主 Bot 与 UserBot 共用的核心消息/频道/媒体能力，便于业务模块解耦与单元测试。
type Messenger interface {
	SelfID() int64
	SelfUsername() string
	IsConnected() bool
	ResolveChannel(ctx context.Context, ref string) (*ChannelInfo, error)
	GetLinkedDiscussionChatID(ctx context.Context, channelRef string) (int64, error)
	ClearLinkedChatCache(channelRef string) int
	LinkedChatCacheChannels() []string
	SendMessage(ctx context.Context, target any, text string, opts SendOptions) (int, error)
	SendLongMessage(ctx context.Context, target any, text string, opts SendOptions) ([]int, error)
	EditMessage(ctx context.Context, target any, messageID int, text string, opts SendOptions) error
	DeleteMessages(ctx context.Context, target any, messageIDs []int) error
	ForwardMessages(ctx context.Context, fromPeer, toPeer any, messageIDs []int) ([]int, error)
	PinMessage(ctx context.Context, target any, messageID int, silent bool) error
	SendPoll(ctx context.Context, target any, opts PollSendOptions) (int, error)
	SendMedia(ctx context.Context, target any, item MediaUpload, opts SendOptions) (int, error)
	SendAlbum(ctx context.Context, target any, items []MediaUpload, opts SendOptions) ([]int, error)
	GetMessages(ctx context.Context, target any, messageIDs []int) ([]*IncomingMessage, error)
	FetchChannelHistory(ctx context.Context, channelRef string, startTime time.Time, excludeIDs map[int]bool) ([]*IncomingMessage, *ChannelInfo, error)
	DownloadMediaBytes(ctx context.Context, media *MediaAttachment, maxBytes int64) ([]byte, error)
	DownloadMediaToFile(ctx context.Context, media *MediaAttachment, destPath string, maxBytes int64) (int64, error)
	DownloadProfilePhoto(ctx context.Context, target any, maxBytes int64) ([]byte, int64, error)
	AnswerCallback(ctx context.Context, queryID int64, message string, alert bool) error
	SetBotCommands(ctx context.Context, langCode string, commands []BotCommandDef) error
}
