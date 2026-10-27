// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

// 本文件是 core/handlers/submission_handler.py（投稿状态机）的移植：
// 六态 FSM WAITING_TITLE→WAITING_CONTENT→WAITING_ANONYMOUS→WAITING_MEDIA→
// SELECT_CHANNEL→CONFIRM→END，行为规格见 result-2.md §2.8、设计 cont-2 §7、
// 对抗评审 B-3b/R11（已裁决进 README）。
//
// 与 PTB ConversationHandler 的语义对齐要点：
//   - 会话按用户维度跟踪（per_message=False 等价），状态仅存内存 map，重启即丢；
//   - 无活跃会话时才检查入口（/submit、/start submit 深链接、^📝 投稿$）；
//   - 有活跃会话时按 Python states[fallbacks] 的处理器列表顺序逐个匹配，
//     全部不命中则本 FSM 放行 update（对齐 PTB "未处理则交给组内其他 handler"），
//     且会话保持原状态；
//   - /submit 在会话内不匹配任何状态处理器 → 放行（Python 无第二个 /submit
//     handler，update 被静默丢弃，不会触发重入弃置；弃置逻辑仅在会话外
//     重入时可达，仍逐行移植）。
//
// 文案纪律：Python 经 get_text 取的文案走 internal/i18n 的 submission.* key
// （session_expired / previous_discarded / previous_discarded_no_title）；
// Python 硬编码的流程文案逐字保留为硬编码（i18n 词典中若干同名 key 与
// Python 硬编码文本存在差异，逐字对齐优先，评审纪律 6）。
package handlers

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/i18n"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/tg"
)

// ────────────────────────────── 常量 ──────────────────────────────

// maxMediaSize 媒体文件大小上限 20MB（submission_handler.py:54）。
const maxMediaSize int64 = 20 * 1024 * 1024

// chPattern 回调数据 ^ch_\d+$ 过滤器（submission_handler.py:620）。
// 注意 Python 的 \d 匹配 Unicode 十进制数字，Go regexp 的 \d 仅限 [0-9]；
// 回调数据由本文件生成（恒为 ASCII 数字），实际不可达差异。
var chPattern = regexp.MustCompile(`^ch_\d+$`)

// subState 投稿会话状态（submission_handler.py:51 的 range(6)；END 为会话终止哨兵）。
type subState int

const (
	subWaitingTitle subState = iota
	subWaitingContent
	subWaitingAnonymous
	subWaitingMedia
	subSelectChannel
	subConfirm
	// subEnd 对齐 ConversationHandler.END：从处理器返回即结束会话。
	subEnd subState = -1
)

// errMediaTooLarge 流式下载时实际字节数超过 20MB（评审 B-3b 裁决的偏差点：
// Python 无下载后大小校验、超限文件照单全收进内存；Go 侧中断下载。
// 元数据预检路径的回复文案与 Python 逐字一致）。
var errMediaTooLarge = errors.New("media file exceeds 20MB")

// ────────────────────────────── 依赖窄接口 ──────────────────────────────

// TelegramAPI 投稿 FSM 所需的 Telegram 能力窄接口（消费者包内定义；
// 生产实现为 TGClientAdapter，包装 internal/tg.Client 的统一超时/429 语义）。
type TelegramAPI interface {
	// SendMessage 发送文本消息（reply_text 等价：PTB v20+ 默认不引用原消息）。
	SendMessage(ctx context.Context, params *bot.SendMessageParams) (*models.Message, error)
	// EditMessageText 编辑消息文本（select_channel 的 query.edit_message_text）。
	EditMessageText(ctx context.Context, params *bot.EditMessageTextParams) (*models.Message, error)
	// AnswerCallbackQuery 应答回调（select_channel 开头的 query.answer()）。
	AnswerCallbackQuery(ctx context.Context, params *bot.AnswerCallbackQueryParams) (bool, error)
	// GetFile 获取文件元信息（file_path/file_size，对齐 PTB bot.get_file）。
	GetFile(ctx context.Context, fileID string) (*models.File, error)
	// DownloadLink 构造文件下载直链（go-telegram/bot 的 FileDownloadLink）。
	DownloadLink(file *models.File) string
}

// TGClientAdapter TelegramAPI 的生产实现：复用 tg.Client 的统一 5s 超时与
// 429 单次重试包装；GetFile/DownloadLink 经 tg.Client.Bot() 补齐（tg 包未导出
// 的 call 泛型在此按同等语义复刻，避免改动已完成的兄弟包）。
type TGClientAdapter struct {
	Client *tg.Client
}

// SendMessage 实现 TelegramAPI。
func (a TGClientAdapter) SendMessage(ctx context.Context, params *bot.SendMessageParams) (*models.Message, error) {
	return a.Client.SendMessage(ctx, params)
}

// EditMessageText 实现 TelegramAPI。
func (a TGClientAdapter) EditMessageText(ctx context.Context, params *bot.EditMessageTextParams) (*models.Message, error) {
	return a.Client.EditMessageText(ctx, params)
}

// AnswerCallbackQuery 实现 TelegramAPI。
func (a TGClientAdapter) AnswerCallbackQuery(ctx context.Context, params *bot.AnswerCallbackQueryParams) (bool, error) {
	return a.Client.AnswerCallbackQuery(ctx, params)
}

// DownloadLink 实现 TelegramAPI。
func (a TGClientAdapter) DownloadLink(file *models.File) string {
	return a.Client.Bot().FileDownloadLink(file)
}

// GetFile 实现 TelegramAPI：5s 超时 + RetryAfter∈[0,60s] 单次睡眠重试
// （README 硬规则 4，与 tg.api 的 call 泛型语义一致）。
func (a TGClientAdapter) GetFile(ctx context.Context, fileID string) (*models.File, error) {
	var (
		res *models.File
		err error
	)
	run := func(ctx context.Context) error {
		res, err = a.Client.Bot().GetFile(ctx, &bot.GetFileParams{FileID: fileID})
		return err
	}

	callCtx, cancel := context.WithTimeout(ctx, tg.DefaultAPITimeout)
	err = run(callCtx)
	cancel()
	if err == nil {
		return res, nil
	}

	var flood *bot.TooManyRequestsError
	if !errors.As(err, &flood) || flood.RetryAfter < 0 || flood.RetryAfter > 60 {
		return res, err
	}
	select {
	case <-ctx.Done():
		return res, ctx.Err()
	case <-time.After(time.Duration(flood.RetryAfter)*time.Second + time.Second):
	}
	callCtx2, cancel2 := context.WithTimeout(ctx, tg.DefaultAPITimeout)
	defer cancel2()
	_ = run(callCtx2)
	return res, err
}

// SubmissionRecord 一条投稿入库记录（字段与 submission_repo.py:76-92 的
// INSERT 列序一致；nil 指针对应 SQL NULL）。
type SubmissionRecord struct {
	SubmitterID   int64
	SubmitterName string
	Title         string
	Content       *string
	MediaJSON     *string
	TargetChannel *string
	IsAnonymous   bool
}

// SubmissionStore 投稿持久化窄接口（消费者包内定义）。
// 生产实现移植 submission_repo.py:48-99 的 create_submission：
//
//	INSERT INTO submissions
//	(submitter_id, submitter_name, title, content, media_files,
//	 target_channel, is_anonymous, status)
//	VALUES (%s, %s, %s, %s, %s, %s, %s, 'pending')
//
// 返回自增投稿 ID；失败返回非 nil error（对应 Python 捕获异常后返回 None）。
type SubmissionStore interface {
	CreateSubmission(ctx context.Context, rec *SubmissionRecord) (int64, error)
}

// UserRegistrar 深链接 /start submit 入口先注册用户的窄接口
// （submission_handler.py:544-552 延迟导入 get_qa_user_system().register_user；
// 生产实现为 *db.Manager.RegisterUser 或 internal/qauser，QA 侧恒 is_admin=0）。
type UserRegistrar interface {
	RegisterUser(ctx context.Context, userID int64, username, firstName *string, isAdmin bool) error
}

// ────────────────────────────── 处理器 ──────────────────────────────

// Deps SubmissionHandler 的装配依赖。
type Deps struct {
	// API Telegram 发送/编辑/应答/取文件能力（必填）。
	API TelegramAPI
	// Store 投稿入库（必填）。
	Store SubmissionStore
	// Registrar 深链接用户注册；nil 等价 Python 的 `if user_system:` 为假分支。
	Registrar UserRegistrar
	// Channels 频道列表来源，每次调用热读（对齐 config_module.CHANNELS 动态值，
	// submission_handler.py:278-282；生产装配 config.Channels）。
	Channels func() []string
	// Log 日志；nil 时用 slog.Default()。
	Log *slog.Logger
}

// SubmissionHandler 投稿流程处理器（对齐 SubmissionHandler）。
type SubmissionHandler struct {
	api       TelegramAPI
	store     SubmissionStore
	registrar UserRegistrar
	channels  func() []string
	log       *slog.Logger

	// download 媒体字节下载注入点；生产为流式临时文件实现（评审 B-3b）。
	download func(ctx context.Context, api TelegramAPI, f *models.File) ([]byte, error)

	mu    sync.Mutex
	users map[int64]*subSession
}

// subSession 单用户投稿会话数据（对齐 Python 的 state dict：
// {"title": None, "content": None, "is_anonymous": False, "media_files": []}
// 外加 PTB ConversationHandler 跟踪的当前状态）。
//
// title 用空串表示 None（预览 `state["title"] or "(未设置)"` 对 None/"" 同义）；
// content 用指针区分 None 与已设置的空串（strip 后可能为 ""）。
type subSession struct {
	state         subState
	title         string
	content       *string
	isAnonymous   bool
	mediaFiles    []mediaFile
	targetChannel string
}

// NewSubmissionHandler 构造投稿处理器。API/Store 缺失属装配错误，立即 panic
// （对齐 engine 包 NewConversations 的快速失败约定）。
func NewSubmissionHandler(d Deps) *SubmissionHandler {
	if d.API == nil {
		panic("handlers.NewSubmissionHandler: API 不能为空")
	}
	if d.Store == nil {
		panic("handlers.NewSubmissionHandler: Store 不能为空")
	}
	log := d.Log
	if log == nil {
		log = slog.Default()
	}
	channels := d.Channels
	if channels == nil {
		channels = func() []string { return nil }
	}
	return &SubmissionHandler{
		api:       d.API,
		store:     d.Store,
		registrar: d.Registrar,
		channels:  channels,
		log:       log,
		download:  downloadViaTempFile,
		users:     make(map[int64]*subSession),
	}
}

// GetUserState 上报用户是否存在进行中的投稿会话
// （qa_bot.py:985-988 的 submission_active 供申诉层拦截用）。
func (h *SubmissionHandler) GetUserState(userID int64) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.users[userID] != nil
}

// ClearUserState 清除用户投稿会话（qa_bot.py:551-570 会话外 /cancel_submit 消费）。
func (h *SubmissionHandler) ClearUserState(userID int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.users, userID)
}

func (h *SubmissionHandler) getSession(userID int64) *subSession {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.users[userID]
}

// setSession 写入/替换会话。
func (h *SubmissionHandler) setSession(userID int64, s *subSession) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.users[userID] = s
}

// deleteSession 移除会话。
func (h *SubmissionHandler) deleteSession(userID int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.users, userID)
}

// HandleUpdate 投稿 FSM 总入口：返回 true 表示 update 被本 FSM 消费，
// dispatcher 应停止向后传递（对齐 PTB 同组先匹配先得）；false 表示放行。
//
// 顶层 recover 兜底（README 硬规则 1 的补充防线）：panic 记日志且视为已消费，
// 避免状态不明的投稿上下文漏进 AI 问答。
func (h *SubmissionHandler) HandleUpdate(ctx context.Context, u *models.Update) (handled bool) {
	defer func() {
		if r := recover(); r != nil {
			h.log.Error(fmt.Sprintf("投稿处理异常: %v", r))
			handled = true
		}
	}()

	su := normalizeSubmissionUpdate(u)
	if su == nil {
		return false
	}

	if h.getSession(su.userID) != nil {
		return h.dispatchState(ctx, su)
	}
	return h.handleEntry(ctx, su)
}

// handleEntry 入口检查（ConversationHandler entry_points，submission_handler.py:565-569）：
// 仅在无活跃会话时到达。
//
// 深链接 /start submit 优先于普通 /start（qa_bot.py:993-999 注释、R11）；
// 不带 submit 参数的 /start 不属于本 FSM 入口，放行给 /start 命令处理器
// （cont-2 §5.2 第 3 步的裁决入口清单："/submit、/start submit、^📝 投稿$"）。
func (h *SubmissionHandler) handleEntry(ctx context.Context, su *subUpdate) bool {
	if su.msg == nil {
		return false
	}
	if su.cmd != "" {
		switch {
		case su.cmd == "submit":
			h.startSubmission(ctx, su)
			return true
		case su.cmd == "start" && len(su.args) > 0 && su.args[0] == "submit":
			h.startSubmissionDeepLink(ctx, su)
			return true
		}
		return false
	}
	// MessageHandler(filters.Regex("^📝 投稿$")) —— 文本按钮入口。
	if pyAnchoredMatch(tg.MenuSubmit, su.text) {
		h.startSubmission(ctx, su)
		return true
	}
	return false
}

// dispatchState 会话内状态分发：按 Python states[当前态] 处理器列表顺序
// 逐个匹配，再试 fallbacks（每态的兜底与态内 cancel 处理器等价，合并判定）；
// 全部不命中 → 会话保持、update 放行（返回 false）。
func (h *SubmissionHandler) dispatchState(ctx context.Context, su *subUpdate) bool {
	sess := h.getSession(su.userID)
	if sess == nil {
		// 分发与会话读取之间无让出点，理论不可达；防御性放行。
		return false
	}

	// fallbacks（submission_handler.py:637-642）先于状态列表并不影响结果：
	// 两条兜底（/cancel_submit 与 ❌ 取消投稿）均已出现在每个状态的处理器列表中。
	var (
		next    subState
		handled bool
	)
	switch sess.state {
	case subWaitingTitle:
		next, handled = h.stateWaitingTitle(ctx, su)
	case subWaitingContent:
		next, handled = h.stateWaitingContent(ctx, su)
	case subWaitingAnonymous:
		next, handled = h.stateWaitingAnonymous(ctx, su)
	case subWaitingMedia:
		next, handled = h.stateWaitingMedia(ctx, su)
	case subSelectChannel:
		next, handled = h.stateSelectChannel(ctx, su)
	case subConfirm:
		next, handled = h.stateConfirm(ctx, su)
	default:
		return false
	}

	if handled && next == subEnd {
		// ConversationHandler.END：清会话（cancel/confirm/无效频道路径已在
		// 处理器内清数据；session_expired 路径数据本就缺失）。
		h.deleteSession(su.userID)
	}
	return handled
}

// stateWaitingTitle 对齐 submission_handler.py:571-577。
func (h *SubmissionHandler) stateWaitingTitle(ctx context.Context, su *subUpdate) (subState, bool) {
	switch {
	case su.msg != nil && pyAnchoredMatch(tg.SubmitMenuCancel, su.text):
		return h.cancelSubmission(ctx, su), true
	case su.msg != nil && su.text != "" && su.cmd == "":
		return h.receiveTitle(ctx, su), true
	case su.msg != nil && su.cmd == "cancel_submit":
		return h.cancelSubmission(ctx, su), true
	}
	return subEnd, false
}

// stateWaitingContent 对齐 submission_handler.py:578-588。
func (h *SubmissionHandler) stateWaitingContent(ctx context.Context, su *subUpdate) (subState, bool) {
	switch {
	case su.msg != nil && pyAnchoredMatch(tg.SubmitMenuSkipContent, su.text):
		return h.skipContent(ctx, su), true
	case su.msg != nil && pyAnchoredMatch(tg.SubmitMenuCancel, su.text):
		return h.cancelSubmission(ctx, su), true
	case su.msg != nil && su.text != "" && su.cmd == "":
		return h.receiveContent(ctx, su), true
	case su.msg != nil && su.cmd == "skip":
		return h.skipContent(ctx, su), true
	case su.msg != nil && su.cmd == "cancel_submit":
		return h.cancelSubmission(ctx, su), true
	}
	return subEnd, false
}

// stateWaitingAnonymous 对齐 submission_handler.py:589-600。
func (h *SubmissionHandler) stateWaitingAnonymous(ctx context.Context, su *subUpdate) (subState, bool) {
	switch {
	case su.msg != nil && (pyAnchoredMatch(tg.SubmitMenuAnonymousYes, su.text) || pyAnchoredMatch(tg.SubmitMenuAnonymousNo, su.text)):
		return h.chooseAnonymous(ctx, su), true
	case su.msg != nil && pyAnchoredMatch(tg.SubmitMenuCancel, su.text):
		return h.cancelSubmission(ctx, su), true
	case su.msg != nil && su.cmd == "cancel_submit":
		return h.cancelSubmission(ctx, su), true
	}
	return subEnd, false
}

// stateWaitingMedia 对齐 submission_handler.py:601-618
// （filters.PHOTO | filters.VIDEO | filters.Document.ALL | filters.AUDIO）。
func (h *SubmissionHandler) stateWaitingMedia(ctx context.Context, su *subUpdate) (subState, bool) {
	switch {
	case su.msg != nil && pyAnchoredMatch(tg.SubmitMenuSkipMedia, su.text):
		return h.goToConfirmFromMessage(ctx, su), true
	case su.msg != nil && pyAnchoredMatch(tg.SubmitMenuDoneMedia, su.text):
		return h.goToConfirmFromMessage(ctx, su), true
	case su.msg != nil && pyAnchoredMatch(tg.SubmitMenuCancel, su.text):
		return h.cancelSubmission(ctx, su), true
	case su.msg != nil && submissionHasMedia(su.msg):
		return h.receiveMedia(ctx, su), true
	case su.msg != nil && su.cmd == "skip":
		return h.goToConfirmFromMessage(ctx, su), true
	case su.msg != nil && su.cmd == "done":
		return h.goToConfirmFromMessage(ctx, su), true
	case su.msg != nil && su.cmd == "cancel_submit":
		return h.cancelSubmission(ctx, su), true
	}
	return subEnd, false
}

// stateSelectChannel 对齐 submission_handler.py:619-625。
func (h *SubmissionHandler) stateSelectChannel(ctx context.Context, su *subUpdate) (subState, bool) {
	switch {
	case su.query != nil && chPattern.MatchString(su.query.Data):
		return h.selectChannel(ctx, su), true
	case su.msg != nil && pyAnchoredMatch(tg.SubmitMenuCancel, su.text):
		return h.cancelSubmission(ctx, su), true
	case su.msg != nil && su.cmd == "cancel_submit":
		return h.cancelSubmission(ctx, su), true
	}
	return subEnd, false
}

// stateConfirm 对齐 submission_handler.py:626-635。
func (h *SubmissionHandler) stateConfirm(ctx context.Context, su *subUpdate) (subState, bool) {
	switch {
	case su.msg != nil && pyAnchoredMatch(tg.SubmitMenuConfirm, su.text):
		return h.confirmSubmission(ctx, su), true
	case su.msg != nil && pyAnchoredMatch(tg.SubmitMenuCancel, su.text):
		return h.cancelSubmission(ctx, su), true
	case su.msg != nil && su.cmd == "confirm":
		return h.confirmSubmission(ctx, su), true
	case su.msg != nil && su.cmd == "cancel_submit":
		return h.cancelSubmission(ctx, su), true
	}
	return subEnd, false
}

// ────────────────────────────── 入口与各态处理器 ──────────────────────────────

// startSubmission 处理 /submit、📝 投稿 按钮（submission_handler.py:83-115）。
func (h *SubmissionHandler) startSubmission(ctx context.Context, su *subUpdate) {
	userName := displayName(su.user)

	// 重入弃置旧稿（submission_handler.py:92-96）。PTB 语义下入口仅在无活跃
	// 会话时触发，此分支属防御性代码，仍逐行移植。
	// （串行分发下先删后回复与 Python 的"回复→清除"顺序对外不可区分。）
	h.mu.Lock()
	old := h.users[su.userID]
	delete(h.users, su.userID)
	h.mu.Unlock()
	if old != nil {
		h.reply(ctx, su.chatID, buildDiscardedNotice(old), nil)
		h.log.Info(fmt.Sprintf("用户 %s 重新开始投稿流程，已清理旧状态", userName))
	}

	// 初始化投稿会话（submission_handler.py:99-104）。
	h.setSession(su.userID, &subSession{state: subWaitingTitle})

	h.reply(ctx, su.chatID, promptTitleMessage, tg.BuildSubmissionTitleKeyboard())
	h.log.Info(fmt.Sprintf("用户 %s 开始投稿流程", userName))
}

// startSubmissionDeepLink 处理深链接 /start submit（submission_handler.py:538-554）：
// 先注册用户再进入投稿流程；注册失败按 Python 异常传播语义中止入口（PTB 错误通道）。
func (h *SubmissionHandler) startSubmissionDeepLink(ctx context.Context, su *subUpdate) {
	if h.registrar != nil {
		err := h.registrar.RegisterUser(ctx, su.userID,
			strPtrOrNil(su.user.Username), strPtrOrNil(su.user.FirstName), false)
		if err != nil {
			h.log.Error(fmt.Sprintf("深链接注册用户失败: %T: %v", err, err))
			return
		}
	}
	h.startSubmission(ctx, su)
}

// promptTitleMessage 标题阶段引导文案（submission_handler.py:106-112 原文）。
const promptTitleMessage = "📝 投稿流程\n\n" +
	"请发送您的投稿标题（必填）。\n\n" +
	"💡 提示：\n" +
	"• 发送 /cancel_submit 可随时取消投稿\n" +
	"• 标题将作为您投稿的摘要展示"

// receiveTitle 接收投稿标题（submission_handler.py:117-144）。
func (h *SubmissionHandler) receiveTitle(ctx context.Context, su *subUpdate) subState {
	sess := h.getSession(su.userID)
	if sess == nil {
		h.reply(ctx, su.chatID, i18n.T("submission.session_expired", nil), tg.BuildQAMainMenuKeyboard())
		return subEnd
	}

	// Python len 为码点数 → utf8.RuneCountInString（README 硬规则 3）。
	title := strings.TrimSpace(su.text)
	if utf8.RuneCountInString(title) > 500 {
		h.reply(ctx, su.chatID, "❌ 标题不能超过 500 个字符，请重新输入。", tg.BuildSubmissionTitleKeyboard())
		return subWaitingTitle
	}

	sess.title = title

	h.reply(ctx, su.chatID,
		fmt.Sprintf("📝 标题已记录：%s\n\n现在请发送投稿正文（可选），或使用以下命令：\n• /skip — 跳过正文，直接进入下一步\n• /cancel_submit — 取消投稿", title),
		tg.BuildSubmissionContentKeyboard())
	sess.state = subWaitingContent
	return subWaitingContent
}

// receiveContent 接收投稿正文（submission_handler.py:146-159）。
func (h *SubmissionHandler) receiveContent(ctx context.Context, su *subUpdate) subState {
	sess := h.getSession(su.userID)
	if sess == nil {
		h.reply(ctx, su.chatID, i18n.T("submission.session_expired", nil), tg.BuildQAMainMenuKeyboard())
		return subEnd
	}

	stripped := strings.TrimSpace(su.text)
	sess.content = &stripped

	return h.askAnonymous(ctx, su, sess, "📝 正文已记录。")
}

// skipContent 跳过正文输入（submission_handler.py:161-172）。
func (h *SubmissionHandler) skipContent(ctx context.Context, su *subUpdate) subState {
	sess := h.getSession(su.userID)
	if sess == nil {
		h.reply(ctx, su.chatID, i18n.T("submission.session_expired", nil), tg.BuildQAMainMenuKeyboard())
		return subEnd
	}
	return h.askAnonymous(ctx, su, sess, "📝 已跳过正文。")
}

// askAnonymous 询问投稿者是否匿名（submission_handler.py:174-183）。
func (h *SubmissionHandler) askAnonymous(ctx context.Context, su *subUpdate, sess *subSession, prefix string) subState {
	message := prefix + "\n\n" +
		"请选择是否匿名投稿：\n" +
		"• 匿名投稿 — 发布到频道时隐藏您的用户名\n" +
		"• 署名投稿 — 发布到频道时显示您的用户名\n" +
		"• /cancel_submit — 取消投稿"
	h.reply(ctx, su.chatID, message, tg.BuildSubmissionAnonymousKeyboard())
	sess.state = subWaitingAnonymous
	return subWaitingAnonymous
}

// chooseAnonymous 处理是否匿名投稿选择（submission_handler.py:185-210）。
func (h *SubmissionHandler) chooseAnonymous(ctx context.Context, su *subUpdate) subState {
	// `if not update.message or not update.message.text: return WAITING_ANONYMOUS`
	if su.msg == nil || su.text == "" {
		return subWaitingAnonymous
	}

	sess := h.getSession(su.userID)
	if sess == nil {
		h.reply(ctx, su.chatID, i18n.T("submission.session_expired", nil), tg.BuildQAMainMenuKeyboard())
		return subEnd
	}

	text := strings.TrimSpace(su.text)
	sess.isAnonymous = text == tg.SubmitMenuAnonymousYes
	anonymousText := "署名投稿"
	if sess.isAnonymous {
		anonymousText = "匿名投稿"
	}

	h.reply(ctx, su.chatID,
		fmt.Sprintf("✅ 已选择：%s\n\n现在请发送媒体文件（图片、视频、文件等），或使用以下命令：\n• /skip — 跳过媒体文件\n• /done — 完成投稿\n• /cancel_submit — 取消投稿", anonymousText),
		tg.BuildSubmissionMediaKeyboard())
	sess.state = subWaitingMedia
	return subWaitingMedia
}

// receiveMedia 接收媒体文件（submission_handler.py:212-268）。
func (h *SubmissionHandler) receiveMedia(ctx context.Context, su *subUpdate) subState {
	sess := h.getSession(su.userID)
	if sess == nil {
		h.reply(ctx, su.chatID, i18n.T("submission.session_expired", nil), tg.BuildQAMainMenuKeyboard())
		return subEnd
	}

	info, ok := extractMediaInfo(su.msg)
	if !ok {
		// 文案与 i18n submission.media_not_recognized 同文，但 Python 侧为硬编码，保持硬编码。
		h.reply(ctx, su.chatID, "❌ 无法识别该媒体类型，请发送图片、视频或文件。\n或使用 /done 完成投稿。", tg.BuildSubmissionMediaKeyboard())
		return subWaitingMedia
	}

	// 检查文件大小（元数据预检；submission_handler.py:231-238）。
	fileSize := mediaMetadataSize(su.msg)
	if fileSize > 0 && fileSize > maxMediaSize {
		h.reply(ctx, su.chatID,
			fmt.Sprintf("❌ 文件过大（%.1fMB），最大支持 20MB。\n请发送更小的文件。", float64(fileSize)/1024/1024),
			tg.BuildSubmissionMediaKeyboard())
		return subWaitingMedia
	}

	// 外层 try（submission_handler.py:240-266）：下载内部已捕获异常并记日志，
	// 返回空字节走"下载失败"分支；成功路径（base64/入库会话/回复）异常才走
	// 外层 except 的截断报错分支。
	if err := h.acceptMedia(ctx, su, sess, info); err != nil {
		h.log.Error(fmt.Sprintf("下载媒体文件失败: %T: %v", err, err))
		h.reply(ctx, su.chatID,
			fmt.Sprintf("❌ 文件下载失败: %s\n请重新发送或使用 /skip 跳过。", truncateRunes(err.Error(), 100)),
			tg.BuildSubmissionMediaKeyboard())
	}
	return subWaitingMedia
}

// acceptMedia receive_media 的 try 块：下载→base64→追加会话→回复。
func (h *SubmissionHandler) acceptMedia(ctx context.Context, su *subUpdate, sess *subSession, info mediaFile) error {
	data, err := h.downloadMediaBytes(ctx, su.msg)
	if err != nil {
		// Python _download_media_to_bytes 捕获异常后返回 None → 走"下载失败"分支。
		h.reply(ctx, su.chatID, "❌ 文件下载失败，请重新发送或跳过。", tg.BuildSubmissionMediaKeyboard())
		return nil
	}
	if len(data) == 0 {
		h.reply(ctx, su.chatID, "❌ 文件下载失败，请重新发送或跳过。", tg.BuildSubmissionMediaKeyboard())
		return nil
	}

	info.fileData = base64.StdEncoding.EncodeToString(data)
	info.fileSize = int64(len(data))
	data = nil
	debug.FreeOSMemory() // 评审 B-3b：媒体缓冲用完立即归还 OS（串行分发下停顿可接受）

	sess.mediaFiles = append(sess.mediaFiles, info)
	count := len(sess.mediaFiles)
	h.reply(ctx, su.chatID,
		fmt.Sprintf("✅ 已接收第 %d 个媒体文件。\n\n继续发送更多文件，或使用以下命令：\n• /done — 完成投稿\n• /skip — 跳过（如果没有更多媒体）\n• /cancel_submit — 取消投稿", count),
		tg.BuildSubmissionMediaKeyboard())
	return nil
}

// goToConfirmFromMessage skip_media / done_media 共用（submission_handler.py:270-276）。
func (h *SubmissionHandler) goToConfirmFromMessage(ctx context.Context, su *subUpdate) subState {
	return h.goToConfirm(ctx, su)
}

// goToConfirm 进入频道选择阶段（submission_handler.py:284-309）。
func (h *SubmissionHandler) goToConfirm(ctx context.Context, su *subUpdate) subState {
	sess := h.getSession(su.userID)
	if sess == nil {
		h.reply(ctx, su.chatID, i18n.T("submission.session_expired", nil), tg.BuildQAMainMenuKeyboard())
		return subEnd
	}

	channels := h.channels() // 每次热读 config.Channels()（submission_handler.py:278-282）
	if len(channels) == 0 {
		// 没有配置频道，直接进入确认。
		return h.showConfirm(ctx, su, sess)
	}

	// 构建频道选择按钮（channel_name = ch.rstrip("/").split("/")[-1]）。
	keyboard := make([][]models.InlineKeyboardButton, 0, len(channels))
	for i, ch := range channels {
		keyboard = append(keyboard, []models.InlineKeyboardButton{{
			Text:         "📢 " + lastSegment(ch),
			CallbackData: fmt.Sprintf("ch_%d", i),
		}})
	}
	h.reply(ctx, su.chatID, "请选择投稿目标频道：", &models.InlineKeyboardMarkup{InlineKeyboard: keyboard})
	sess.state = subSelectChannel
	return subSelectChannel
}

// selectChannel 处理频道选择回调（submission_handler.py:311-337）。
func (h *SubmissionHandler) selectChannel(ctx context.Context, su *subUpdate) subState {
	q := su.query
	// await query.answer()：失败仅记日志（PTB 交错误通道，流程继续）。
	if _, err := h.api.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{CallbackQueryID: q.ID}); err != nil {
		h.log.Error(fmt.Sprintf("应答频道选择回调失败: %s", err))
	}

	sess := h.getSession(q.From.ID)
	if sess == nil {
		// 注意：此路径 edit_message_text 不带主菜单键盘（与 Python 一致）。
		h.editCallbackMessage(ctx, q, i18n.T("submission.session_expired", nil))
		return subEnd
	}

	data := q.Data
	if data != "" && strings.HasPrefix(data, "ch_") {
		idx, convErr := strconv.Atoi(data[3:])
		channels := h.channels()
		if convErr != nil {
			// Python int() ValueError 会向上抛给 PTB 错误通道：会话保持、无回复。
			// ^ch_\d+$ 已过滤非数字，此处仅超高位数可达。
			h.log.Error(fmt.Sprintf("频道选择序号解析失败: %s", convErr))
			return subSelectChannel
		}
		if idx >= 0 && idx < len(channels) {
			sess.targetChannel = channels[idx]
			h.editCallbackMessage(ctx, q, fmt.Sprintf("✅ 已选择频道: %s", lastSegment(channels[idx])))
			return h.showConfirmViaQuery(ctx, q)
		}
		h.deleteSession(q.From.ID)
		h.editCallbackMessage(ctx, q, "❌ 频道选择无效，请重新发送 /submit。")
		return subEnd
	}

	return subSelectChannel
}

// showConfirmViaQuery 通过 callback query 显示确认信息（submission_handler.py:339-369）。
func (h *SubmissionHandler) showConfirmViaQuery(ctx context.Context, q *models.CallbackQuery) subState {
	sess := h.getSession(q.From.ID)
	if sess == nil {
		h.replyToCallbackMessage(ctx, q, i18n.T("submission.session_expired", nil), tg.BuildQAMainMenuKeyboard())
		return subEnd
	}

	sess.state = subConfirm
	h.replyToCallbackMessage(ctx, q, buildConfirmPreview(sess), tg.BuildSubmissionConfirmKeyboard())
	return subConfirm
}

// showConfirm 通过 update message 显示确认信息（submission_handler.py:371-401）。
func (h *SubmissionHandler) showConfirm(ctx context.Context, su *subUpdate, sess *subSession) subState {
	if sess == nil {
		h.reply(ctx, su.chatID, i18n.T("submission.session_expired", nil), tg.BuildQAMainMenuKeyboard())
		return subEnd
	}

	sess.state = subConfirm
	h.reply(ctx, su.chatID, buildConfirmPreview(sess), tg.BuildSubmissionConfirmKeyboard())
	return subConfirm
}

// confirmSubmission 确认并提交投稿（submission_handler.py:403-443）。
func (h *SubmissionHandler) confirmSubmission(ctx context.Context, su *subUpdate) subState {
	sess := h.getSession(su.userID)
	if sess == nil {
		h.reply(ctx, su.chatID, i18n.T("submission.session_expired", nil), tg.BuildQAMainMenuKeyboard())
		return subEnd
	}

	userName := displayName(su.user)

	rec := &SubmissionRecord{
		SubmitterID:   su.userID,
		SubmitterName: userName,
		Title:         sess.title,
		Content:       sess.content,
		TargetChannel: strPtrOrNil(sess.targetChannel),
		IsAnonymous:   sess.isAnonymous,
	}
	mediaJSON, jsonErr := mediaFilesJSON(sess.mediaFiles)
	if jsonErr != nil {
		// 媒体 JSON 序列化失败（理论不可达）：按 repo 捕获异常语义降级为 NULL。
		h.log.Error(fmt.Sprintf("媒体文件 JSON 序列化失败: %T: %v", jsonErr, jsonErr))
		mediaJSON = nil
	}
	rec.MediaJSON = mediaJSON

	// SubmissionService.submit → repo.create_submission（submission_service.py:26-75、
	// submission_repo.py:48-99）。Go 侧两层合并：store 的 error 对齐 repo 捕获
	// 异常后返回 None 的分支；service 的 except 分支在 Python 中不可达（repo
	// 自吞异常），不复刻。
	id, err := h.store.CreateSubmission(ctx, rec)
	if err != nil {
		h.log.Error(fmt.Sprintf("创建投稿记录失败: %T: %v", err, err))
		id = 0
	} else {
		h.log.Info(fmt.Sprintf("创建投稿记录成功: ID=%d, 投稿者=%s", id, userName))
		h.log.Info(fmt.Sprintf("投稿提交成功: ID=%d, 投稿者=%s", id, userName))
	}

	if id != 0 {
		// MainBot 的定时任务会自动检测 pending 投稿并通知管理员（QA 不发通知）。
		h.reply(ctx, su.chatID,
			fmt.Sprintf("✅ 投稿已成功提交，请等待管理员审核。\n\n您的投稿ID: %d", id),
			tg.BuildQAMainMenuKeyboard())
		h.log.Info(fmt.Sprintf("用户 %s 投稿提交成功: ID=%d", userName, id))
	} else {
		h.reply(ctx, su.chatID, "❌ 投稿提交失败，请稍后重试。", tg.BuildQAMainMenuKeyboard())
	}

	h.deleteSession(su.userID)
	return subEnd
}

// cancelSubmission 取消投稿（submission_handler.py:445-454）。
func (h *SubmissionHandler) cancelSubmission(ctx context.Context, su *subUpdate) subState {
	h.deleteSession(su.userID)
	h.reply(ctx, su.chatID, "✅ 投稿已取消。", tg.BuildQAMainMenuKeyboard())
	h.log.Info(fmt.Sprintf("用户 %s 取消了投稿", usernameOrID(su.user)))
	return subEnd
}

// ────────────────────────────── 确认预览 ──────────────────────────────

// buildConfirmPreview 构建投稿预览确认文本（submission_handler.py:357-367 与
// :389-399 两处相同的 f-string 拼接，逐字对齐；正文截 200 码点加省略号）。
func buildConfirmPreview(sess *subSession) string {
	title := sess.title
	if title == "" {
		title = "(未设置)"
	}
	content := "(无)"
	if sess.content != nil && *sess.content != "" {
		content = *sess.content
	}

	contentShown := content
	if runes := []rune(content); len(runes) > 200 {
		contentShown = string(runes[:200]) + "..."
	}

	mediaCount := len(sess.mediaFiles)
	channelName := "(未选择)"
	if sess.targetChannel != "" {
		channelName = lastSegment(sess.targetChannel)
	}
	anonymousText := "否"
	if sess.isAnonymous {
		anonymousText = "是"
	}

	return fmt.Sprintf("📝 投稿预览确认\n\n标题：%s\n正文：%s\n媒体文件：%d 个\n匿名投稿：%s\n目标频道：%s\n\n请确认是否提交：\n• /confirm — 确认提交\n• /cancel_submit — 取消投稿",
		title, contentShown, mediaCount, anonymousText, channelName)
}

// buildDiscardedNotice 构建旧投稿已取消提示（submission_handler.py:73-81）。
func buildDiscardedNotice(state *subSession) string {
	oldTitle := strings.TrimSpace(state.title) // str(state.get("title") or "").strip()
	if oldTitle == "" {
		return i18n.T("submission.previous_discarded_no_title", nil)
	}
	runes := []rune(oldTitle)
	displayTitle := oldTitle
	if len(runes) > 20 {
		displayTitle = string(runes[:20]) + "..."
	}
	return i18n.T("submission.previous_discarded", map[string]string{"title": displayTitle})
}

// ────────────────────────────── 媒体提取与下载 ──────────────────────────────

// submissionHasMedia 对齐过滤器 PHOTO|VIDEO|Document.ALL|AUDIO 的判定面。
func submissionHasMedia(m *models.Message) bool {
	return len(m.Photo) > 0 || m.Video != nil || m.Document != nil || m.Audio != nil
}

// extractMediaInfo 从消息中提取媒体信息（submission_handler.py:456-490；
// photo 取最大尺寸 = 数组末位）。键序与 Python dict 字面量序一致，
// 由 mediaFile.marshalJSON 在序列化时体现。
func extractMediaInfo(m *models.Message) (mediaFile, bool) {
	switch {
	case len(m.Photo) > 0:
		p := m.Photo[len(m.Photo)-1]
		return mediaFile{kind: "photo", fileID: p.FileID, fileUniqueID: p.FileUniqueID}, true
	case m.Video != nil:
		return mediaFile{kind: "video", fileID: m.Video.FileID, fileUniqueID: m.Video.FileUniqueID,
			mimeType: m.Video.MimeType, fileName: m.Video.FileName}, true
	case m.Document != nil:
		return mediaFile{kind: "document", fileID: m.Document.FileID, fileUniqueID: m.Document.FileUniqueID,
			fileName: m.Document.FileName, mimeType: m.Document.MimeType}, true
	case m.Audio != nil:
		return mediaFile{kind: "audio", fileID: m.Audio.FileID, fileUniqueID: m.Audio.FileUniqueID,
			fileName: m.Audio.FileName, mimeType: m.Audio.MimeType}, true
	}
	return mediaFile{}, false
}

// mediaMetadataSize 获取消息中媒体文件的大小（submission_handler.py:492-502）。
// 无媒体或未知返回 0（对应 Python None 的假值语义：预检跳过）。
func mediaMetadataSize(m *models.Message) int64 {
	switch {
	case len(m.Photo) > 0:
		return int64(m.Photo[len(m.Photo)-1].FileSize)
	case m.Video != nil:
		return m.Video.FileSize
	case m.Document != nil:
		return m.Document.FileSize
	case m.Audio != nil:
		return m.Audio.FileSize
	}
	return 0
}

// submissionFileID 下载目标 file_id（submission_handler.py:504-527 的取值分支）。
func submissionFileID(m *models.Message) string {
	switch {
	case len(m.Photo) > 0:
		return m.Photo[len(m.Photo)-1].FileID
	case m.Video != nil:
		return m.Video.FileID
	case m.Document != nil:
		return m.Document.FileID
	case m.Audio != nil:
		return m.Audio.FileID
	}
	return ""
}

// downloadMediaBytes 下载媒体文件到内存（对齐 _download_media_to_bytes：
// 内部捕获全部异常并记日志，失败返回空数据；调用方据空值回复下载失败）。
func (h *SubmissionHandler) downloadMediaBytes(ctx context.Context, m *models.Message) ([]byte, error) {
	fileID := submissionFileID(m)
	if fileID == "" {
		return nil, nil
	}

	f, err := h.api.GetFile(ctx, fileID)
	if err != nil {
		h.log.Error(fmt.Sprintf("下载媒体文件到内存失败: %T: %v", err, err))
		return nil, err
	}

	data, err := h.download(ctx, h.api, f)
	if err != nil {
		h.log.Error(fmt.Sprintf("下载媒体文件到内存失败: %T: %v", err, err))
		return nil, err
	}
	return data, nil
}

// tgFileHTTPClient 文件 CDN 下载专用客户端：连接/响应头 5s（对齐 PTB HTTPX
// 的 connect/read 超时），不设总超时（大文件慢链路合法）。
var tgFileHTTPClient = &http.Client{
	Transport: &http.Transport{
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 5 * time.Second,
	},
}

// downloadViaTempFile 生产下载实现（评审 B-3b 裁决）：流式写临时文件并施加
// 20MB 硬上限，确认大小后再整体读入内存交由调用方 base64；
// 临时文件用完即删。逐读 5s 空闲超时模拟 PTB read-timeout。
func downloadViaTempFile(ctx context.Context, api TelegramAPI, f *models.File) ([]byte, error) {
	tmp, err := os.CreateTemp("", "sakura-submit-*.bin")
	if err != nil {
		return nil, err
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}()

	// 逐读空闲超时：每次 Read 前重置 5s 看门狗，读阻塞超时即取消请求上下文
	// （取消请求是 http 载荷读阻塞的标准解锁手段）。
	dlCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	req, err := http.NewRequestWithContext(dlCtx, http.MethodGet, api.DownloadLink(f), nil)
	if err != nil {
		return nil, err
	}
	resp, err := tgFileHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("下载媒体文件到内存失败: HTTP %s", resp.Status)
	}

	watchdog := time.AfterFunc(5*time.Second, cancel)
	defer watchdog.Stop()
	body := &idleTimeoutReader{r: resp.Body, watchdog: watchdog, d: 5 * time.Second}

	// LimitReader(max+1)：多读 1 字节用于探测超限。
	n, err := io.Copy(tmp, io.LimitReader(body, maxMediaSize+1))
	if err != nil {
		return nil, err
	}
	if n > maxMediaSize {
		return nil, errMediaTooLarge
	}

	return os.ReadFile(tmpPath)
}

// idleTimeoutReader 每次 Read 前重置看门狗定时器；单次读阻塞超过 d 即触发
// cancel 取消底层请求（对齐 PTB HTTPX read=5s 的逐操作超时语义）。
type idleTimeoutReader struct {
	r        io.Reader
	watchdog *time.Timer
	d        time.Duration
}

// Read 实现 io.Reader。
func (w *idleTimeoutReader) Read(p []byte) (int, error) {
	w.watchdog.Reset(w.d)
	n, err := w.r.Read(p)
	return n, err
}

// ────────────────────────────── 媒体 JSON（Python dict 键序复刻）──────────────────────────────

// mediaFile 单个媒体文件信息。字段序列化键序 = Python dict 字面量插入序：
// receive_media 在 _extract_media_info 的 dict 之后追加 file_data/file_size。
type mediaFile struct {
	kind         string // photo / video / document / audio
	fileID       string
	fileUniqueID string
	mimeType     string // video/document/audio
	fileName     string // video/document/audio
	fileData     string // base64
	fileSize     int64
}

// marshalJSON 复刻 json.dumps(media_info, ensure_ascii=False)：
//   - 键序按 Python 插入序（photo 无 mime_type/file_name 键；
//     video 先 mime_type 后 file_name；document/audio 相反）；
//   - 分隔符为 json.dumps 默认的 ", " 与 ": "；
//   - 不转义 HTML 字符（SetEscapeHTML(false) 对齐 ensure_ascii=False）；
//   - 空 mime_type/file_name 序列化为 null（PTB Optional[str] 为 None，
//     Go models 中缺失即空串，二者在真实 Telegram 载荷中等价）。
func (m *mediaFile) marshalJSON() ([]byte, error) {
	var w pyJSONWriter
	w.open()
	if err := w.strPair("file_id", m.fileID); err != nil {
		return nil, err
	}
	if err := w.strPair("type", m.kind); err != nil {
		return nil, err
	}
	if err := w.strPair("file_unique_id", m.fileUniqueID); err != nil {
		return nil, err
	}
	switch m.kind {
	case "video":
		if err := w.nullableStrPair("mime_type", m.mimeType); err != nil {
			return nil, err
		}
		if err := w.nullableStrPair("file_name", m.fileName); err != nil {
			return nil, err
		}
	case "document", "audio":
		if err := w.nullableStrPair("file_name", m.fileName); err != nil {
			return nil, err
		}
		if err := w.nullableStrPair("mime_type", m.mimeType); err != nil {
			return nil, err
		}
	}
	if err := w.strPair("file_data", m.fileData); err != nil {
		return nil, err
	}
	if err := w.intPair("file_size", m.fileSize); err != nil {
		return nil, err
	}
	w.close()
	return []byte(w.String()), nil
}

// mediaFilesJSON 序列化整个媒体列表（json.dumps(list, ensure_ascii=False)）；
// 空列表返回 nil（对齐 `json.dumps(...) if media_files else None` 的 SQL NULL）。
func mediaFilesJSON(files []mediaFile) (*string, error) {
	if len(files) == 0 {
		return nil, nil
	}
	parts := make([]string, 0, len(files))
	for i := range files {
		b, err := files[i].marshalJSON()
		if err != nil {
			return nil, err
		}
		parts = append(parts, string(b))
	}
	s := "[" + strings.Join(parts, ", ") + "]"
	return &s, nil
}

// pyJSONWriter 按 Python json.dumps 默认分隔符写有序 JSON 对象。
type pyJSONWriter struct {
	sb    strings.Builder
	first bool
}

// open 写对象起始花括号。
func (w *pyJSONWriter) open() {
	w.sb.WriteByte('{')
	w.first = true
}

// close 写对象结束花括号。
func (w *pyJSONWriter) close() {
	w.sb.WriteByte('}')
}

// String 返回已构建的 JSON 文本。
func (w *pyJSONWriter) String() string { return w.sb.String() }

// writeKey 写键与前导逗号。
func (w *pyJSONWriter) writeKey(key string) error {
	if !w.first {
		w.sb.WriteString(", ")
	}
	w.first = false
	encoded, err := pyJSONEncode(key)
	if err != nil {
		return err
	}
	w.sb.WriteString(encoded)
	w.sb.WriteString(": ")
	return nil
}

// strPair 写非空即字符串的键值对。
func (w *pyJSONWriter) strPair(key, val string) error {
	if err := w.writeKey(key); err != nil {
		return err
	}
	encoded, err := pyJSONEncode(val)
	if err != nil {
		return err
	}
	w.sb.WriteString(encoded)
	return nil
}

// nullableStrPair 空串写 null（Python None 语义），否则写字符串。
func (w *pyJSONWriter) nullableStrPair(key, val string) error {
	if err := w.writeKey(key); err != nil {
		return err
	}
	if val == "" {
		w.sb.WriteString("null")
		return nil
	}
	encoded, err := pyJSONEncode(val)
	if err != nil {
		return err
	}
	w.sb.WriteString(encoded)
	return nil
}

// intPair 写整型键值对。
func (w *pyJSONWriter) intPair(key string, val int64) error {
	if err := w.writeKey(key); err != nil {
		return err
	}
	w.sb.WriteString(strconv.FormatInt(val, 10))
	return nil
}

// pyJSONEncode 单值 JSON 编码：不转义 HTML（ensure_ascii=False 语义）。
func pyJSONEncode(v any) (string, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return "", err
	}
	return strings.TrimSuffix(buf.String(), "\n"), nil
}

// ────────────────────────────── 工具函数 ──────────────────────────────

// subUpdate 归一化后的投稿相关 update 视图。
type subUpdate struct {
	msg    *models.Message
	query  *models.CallbackQuery
	text   string
	cmd    string   // 命令名（无斜杠、去 @bot 后缀）；非命令为 ""
	args   []string // 命令参数（PTB context.args = text.split()[1:]）
	user   *models.User
	userID int64
	chatID int64
}

// normalizeSubmissionUpdate 提取 message（含 edited_message，对齐 PTB
// MessageHandler 对 effective_message 的匹配）或 callback_query。
// 无 effective_user 时返回 nil（防御：Python 侧将 AttributeError）。
func normalizeSubmissionUpdate(u *models.Update) *subUpdate {
	if u == nil {
		return nil
	}
	msg := u.Message
	if msg == nil {
		msg = u.EditedMessage
	}
	if msg == nil && u.CallbackQuery == nil {
		return nil
	}

	su := &subUpdate{}
	if msg != nil {
		su.msg = msg
		su.text = msg.Text
		su.chatID = msg.Chat.ID
		su.user = msg.From
		if strings.HasPrefix(su.text, "/") {
			fields := strings.Fields(su.text)
			if len(fields) > 0 {
				su.cmd = strings.SplitN(strings.TrimPrefix(fields[0], "/"), "@", 2)[0]
				su.args = fields[1:]
			}
		}
	}
	if u.CallbackQuery != nil {
		su.query = u.CallbackQuery
		su.user = &u.CallbackQuery.From
		if qm := u.CallbackQuery.Message.Message; qm != nil {
			su.chatID = qm.Chat.ID
		}
	}
	if su.user == nil {
		return nil
	}
	su.userID = su.user.ID
	return su
}

// pyAnchoredMatch 复刻 re.search(rf"^{literal}$", text) 的锚定匹配：
// Python 的 `$` 允许结尾多一个换行，故额外接受 literal+"\n"。
// 按钮文案均无正则元字符，字面量比对即可。
func pyAnchoredMatch(literal, text string) bool {
	return text == literal || text == literal+"\n"
}

// lastSegment 对齐 ch.rstrip("/").split("/")[-1]。
func lastSegment(channel string) string {
	trimmed := strings.TrimRight(channel, "/")
	if idx := strings.LastIndex(trimmed, "/"); idx >= 0 {
		return trimmed[idx+1:]
	}
	return trimmed
}

// displayName 对齐 username or first_name or str(user_id)。
func displayName(u *models.User) string {
	if u == nil {
		return ""
	}
	if u.Username != "" {
		return u.Username
	}
	if u.FirstName != "" {
		return u.FirstName
	}
	return strconv.FormatInt(u.ID, 10)
}

// usernameOrID 对齐 update.effective_user.username or user_id（取消日志用）。
func usernameOrID(u *models.User) string {
	if u == nil {
		return ""
	}
	if u.Username != "" {
		return u.Username
	}
	return strconv.FormatInt(u.ID, 10)
}

// strPtrOrNil 空串转 nil（Go models 的缺省空串 ⇔ PTB Optional 的 None）。
func strPtrOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// truncateRunes str(e)[:100] 的码点截断。
func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// reply 发送消息；失败仅记日志（PTB 将异常交错误通道，流程继续）。
func (h *SubmissionHandler) reply(ctx context.Context, chatID int64, text string, markup models.ReplyMarkup) {
	if _, err := h.api.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:      chatID,
		Text:        text,
		ReplyMarkup: markup,
	}); err != nil {
		h.log.Error(fmt.Sprintf("投稿消息发送失败: %s", err))
	}
}

// editCallbackMessage query.edit_message_text 等价（聊天消息或内联消息）。
func (h *SubmissionHandler) editCallbackMessage(ctx context.Context, q *models.CallbackQuery, text string) {
	params := &bot.EditMessageTextParams{Text: text}
	switch {
	case q.Message.Message != nil:
		params.ChatID = q.Message.Message.Chat.ID
		params.MessageID = q.Message.Message.ID
	case q.InlineMessageID != "":
		params.InlineMessageID = q.InlineMessageID
	default:
		h.log.Error("投稿回调缺少可编辑消息")
		return
	}
	if _, err := h.api.EditMessageText(ctx, params); err != nil && !tg.IsNotModified(err) {
		h.log.Error(fmt.Sprintf("投稿回调消息编辑失败: %s", err))
	}
}

// replyToCallbackMessage query.message.reply_text 等价（向回调来源聊天发新消息）。
func (h *SubmissionHandler) replyToCallbackMessage(ctx context.Context, q *models.CallbackQuery, text string, markup models.ReplyMarkup) {
	if q.Message.Message == nil {
		h.log.Error("投稿回调缺少来源消息，无法回复")
		return
	}
	h.reply(ctx, q.Message.Message.Chat.ID, text, markup)
}
