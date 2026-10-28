// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

// appeal.go：助手 Bot 自助申诉子系统（组 -1），逐行移植以下 Python 模块
// （行为规格 result-2.md §2.9，唯一事实来源 qa_bot.py 闭包）：
//   - core/handlers/appeal_handler.py（544 行）：消息/回调入口、材料收集、
//     审核回执与通知投递 job；
//   - core/moderation/appeal_inbox.py（303 行）：begin/supplement/append/
//     submit/withdraw/cancel/review_input/quick_review/read_request 输入边界；
//   - core/handlers/appeal_messages.py（176 行）：edit_or_send 卡片复用与
//     user/input/review/finish_input 消息层；
//   - 仓储方法（transition/事件/通知/请求队列）复用 internal/db/guard。
//
// 硬性不变量（qa-bot-go/README 规则、评审 B-1/B-4 与 spec §2.9）：
//   - 仅私聊判定：群内申诉命令回 appeal.private_only 并终止；
//   - 判定「可能属于申诉」即短路；任何异常也置 handled（fail-closed，
//     绝不让申诉证据漏给 AI）；
//   - 全部长度判定按 rune（README 硬规则 3）；
//   - 写路径全部在用户级 GET_LOCK 内（guard.WithLock，同连接取放）。
package handlers

import (
	"context"
	crand "crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/db"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/db/guard"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/i18n"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/tg"
)

// 入口正则与动作集合（appeal_handler.py:87,239 与 appeal_repository.py:15-17）。
var (
	// deepLinkRe 等价 re.fullmatch(r"appeal_[0-9a-f]{32}")（R4：RE2 用 ^$ 锚点）。
	deepLinkRe = regexp.MustCompile(`^appeal_[0-9a-f]{32}$`)
	// hex32Re 等价 re.fullmatch(r"[0-9a-f]{32}")（回调里的申诉 ID 校验）。
	hex32Re = regexp.MustCompile(`^[0-9a-f]{32}$`)
	// tagRe 用于回执分组时的去标签长度估算（appeal_handler.py:512）。
	tagRe = regexp.MustCompile(`<[^>]+>`)
)

// 审核动作集合（appeal_repository.py:16-17）。
var (
	reviewActions = map[string]bool{"approve": true, "allow": true, "global": true, "reject": true, "more": true}
	readActions   = map[string]bool{"evidence": true, "audit": true}
)

// 输入收集限额（appeal_inbox.py:71-99；README 硬规则 3 的 rune 计数）。
const (
	// appealTextLimit 草稿累计文本上限（字）。
	appealTextLimit = 2000
	// appealReviewTextLimit review 模式单次 1-1000 字必填。
	appealReviewTextLimit = 1000
	// appealFileLimit 每轮附件数上限。
	appealFileLimit = 5
	// appealFileSizeLimit 单附件大小上限（20MB）。
	appealFileSizeLimit = 20 * 1024 * 1024
	// appealMessagesCap 每条输入记录的消息 ID 去重窗口（最近 30 条）。
	appealMessagesCap = 30
	// appealCooldownSubmissions 24h 提交配额（appeal_inbox.py:36-37,144）。
	appealCooldownSubmissions = 3
)

// GuardError 携带 i18n key 的业务异常（对齐 core/moderation/moderator.py:46
// 的 GuardError(ValueError)）。Python except GuardError → t(str(error))；
// TypeError/ValueError（如 int() 解析失败）→ guard.invalid_callback。
// Go 侧把解析失败统一折算为该哨兵错误，走同一应答分支。
type GuardError struct{ Key string }

// Error 返回 i18n key（与 Python str(GuardError(key)) 等价）。
func (e *GuardError) Error() string { return e.Key }

// guardErr 构造业务异常。
func guardErr(key string) *GuardError { return &GuardError{Key: key} }

// ─────────────────────────────────────────────────────────────────────
// 行值与 JSON 载荷访问工具：guard.Row 的 payload 已在 guard.All/One
// 解析为 map/list；JSON 数字统一为 float64，需按 Python int 语义读取。
// ─────────────────────────────────────────────────────────────────────

// asMap 断言 map（nil 安全）。
func asMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return nil
}

// asList 断言列表（nil 安全）。
func asList(v any) []any {
	if l, ok := v.([]any); ok {
		return l
	}
	return nil
}

// pyOr 等价 Python `a or b`（a 为真值取 a，否则 b）。
func pyOr(a, b any) any {
	if pyTruthy(a) {
		return a
	}
	return b
}

// pyTruthy 等价 Python 真值判定（None/空串/0/空容器为假）。
func pyTruthy(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case string:
		return t != ""
	case bool:
		return t
	case float64:
		return t != 0
	case int64:
		return t != 0
	case int:
		return t != 0
	case []any:
		return len(t) > 0
	case map[string]any:
		return len(t) > 0
	default:
		return true
	}
}

// pyStr 等价 Python str() 的常用分支：str 原样；JSON 数值按 Python int
// 输出（float64(2) → "2" 而非 "2.0"：payload 数值均由 Python int 写入）；
// datetime 按 str(datetime) 的 "YYYY-MM-DD HH:MM:SS[.ffffff]" 形态。
func pyStr(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case []byte:
		return string(t)
	case float64:
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'g', -1, 64)
	case int64:
		return strconv.FormatInt(t, 10)
	case int:
		return strconv.Itoa(t)
	case bool:
		if t {
			return "True"
		}
		return "False"
	case time.Time:
		return pyStrDatetime(t)
	default:
		return fmt.Sprintf("%v", t)
	}
}

// pyStrDatetime 等价 Python str(datetime)：无微秒输出秒级；有微秒恒 6 位
// （如 .123000）。源案例 created_at 的字符串化依赖该形态。
func pyStrDatetime(t time.Time) string {
	base := t.Format("2006-01-02 15:04:05")
	micros := t.Nanosecond() / 1000
	if micros != 0 {
		return fmt.Sprintf("%s.%06d", base, micros)
	}
	return base
}

// pyInt 等价 Python int() 的宽松读取：JSON float64/int64、数字字符串；
// 不可能值归零（调用点字段均由本系统写入，类型恒定）。
func pyInt(v any) int64 {
	switch t := v.(type) {
	case nil:
		return 0
	case float64:
		return int64(t)
	case int64:
		return t
	case int:
		return int64(t)
	case json.Number:
		n, _ := t.Int64()
		return n
	case string:
		n, _ := strconv.ParseInt(strings.TrimSpace(t), 10, 64)
		return n
	default:
		return 0
	}
}

// ─────────────────────────────────────────────────────────────────────
// Handler 主体（appeal_handler.py:38-61 AppealHandler 构造与依赖）。
// ─────────────────────────────────────────────────────────────────────

// Telegram 是申诉子系统消费的 Telegram 窄接口（消费者包内定义，Go 惯例）：
// 生产实现为 *tg.Client（5s 超时 + 429 RetryAfter≤60s 单次重试；
// SendMessageWithTimeout/EditMessageTextWithTimeout 承载申诉卡片 25s 例外）；
// 测试注入 fake。
type Telegram interface {
	SendMessageWithTimeout(ctx context.Context, timeout time.Duration, params *bot.SendMessageParams) (*models.Message, error)
	EditMessageTextWithTimeout(ctx context.Context, timeout time.Duration, params *bot.EditMessageTextParams) (*models.Message, error)
	AnswerCallbackQuery(ctx context.Context, params *bot.AnswerCallbackQueryParams) (bool, error)
}

// AppealHandler 组 -1 申诉处理器：全部写路径经 guard.Repository 的用户级
// GET_LOCK；IsAdmin/SubmissionActive 由 main 装配（Python 侧为 is_admin
// （auth.py，fail-closed）与投稿 ConversationHandler 的激活回调）。
type AppealHandler struct {
	repo *guard.Repository
	tg   Telegram
	// raw 用于回执附件的 send_photo/send_document/send_video
	//（appeal_handler.py:528 getattr(bot, f"send_{kind}")；直连底层 Bot，
	// 由 sendMedia 补 25s 超时与 429 语义）。
	raw *bot.Bot
	// log 语义对齐 Python logger（模块级 logger = logging.getLogger(__name__)）。
	log tg.Logger
	// IsAdmin 判定 global 动作权限（appeal_messages.py:119 /
	// appeal_inbox.py:240-243）；nil 一律 false（fail-closed）。
	IsAdmin func(userID int64) bool
	// SubmissionActive 投稿状态机激活回调；nil 等价 Python 默认
	// lambda actor: False。
	SubmissionActive func(actor int64) bool
}

// NewAppealHandler 构造申诉处理器。api/raw 允许为 nil（纯仓储单测场景），
// 此时消息/投递路径会以错误返回并在上层按 fail-closed 处理。
func NewAppealHandler(repo *guard.Repository, api Telegram, raw *bot.Bot, log tg.Logger) *AppealHandler {
	if log == nil {
		log = tg.NopLogger{}
	}
	return &AppealHandler{repo: repo, tg: api, raw: raw, log: log}
}

// isAdminNilSafe 判定管理员（nil 实现回退 false，fail-closed）。
func (h *AppealHandler) isAdminNilSafe(userID int64) bool {
	if h.IsAdmin == nil {
		return false
	}
	return h.IsAdmin(userID)
}

// submissionActiveNilSafe 投稿激活钩子（nil 等价恒 False）。
func (h *AppealHandler) submissionActiveNilSafe(actor int64) bool {
	if h.SubmissionActive == nil {
		return false
	}
	return h.SubmissionActive(actor)
}

// isPrivateMessage 等价 appeal_handler.py:52-60 private()：
// 仅私聊（chat.type == private 且 chat.id == user.id 且非 bot）。
func isPrivateMessage(msg *models.Message) bool {
	return msg != nil && msg.From != nil &&
		msg.Chat.Type == models.ChatTypePrivate &&
		msg.Chat.ID == msg.From.ID &&
		!msg.From.IsBot
}

// isPrivateCallback 等价同一 private() 在回调 update 上的取值：
// effective_user = callback_query.from，effective_chat = 卡片消息所属会话。
func isPrivateCallback(query *models.CallbackQuery, chatID int64, chatType models.ChatType) bool {
	return query.From.ID != 0 &&
		chatType == models.ChatTypePrivate &&
		chatID == query.From.ID &&
		!query.From.IsBot
}

// cbOrigin 是回调/消息路径里的 origin (chat_id, message_id) 二元组
// （Python tuple；set=false 等价 None）。
type cbOrigin struct {
	chatID    int64
	messageID int64
	set       bool
}

// originOf 构造已设置的 origin。
func originOf(chatID, messageID int64) cbOrigin { return cbOrigin{chatID, messageID, true} }

// messageIDFor 等价 Python `origin[1] if origin and origin[0] == actor else None`
// 的 0 值形态（0 = 无目标消息）。
func (o cbOrigin) messageIDFor(actor int64) int64 {
	if o.set && o.chatID == actor {
		return o.messageID
	}
	return 0
}

// commandParse 是消息入口的纯解析结果（appeal_handler.py:66-72）。
type commandParse struct {
	parts        []string // 等价 text.split(maxsplit=1)
	command      string   // parts[0].split("@")[0].lower()
	deep         bool     // /start appeal_… 深链接
	entryCommand bool     // /appeal /appeals /cancel_appeal、菜单文本或深链接
}

// parseCommandText 等价 appeal_handler.py:66-72 的入口判定（纯函数）。
// menuAppeal 传入 tg.MenuAppeal（Python 模块期常量 QA_MENU_APPEAL）。
func parseCommandText(text, menuAppeal string) commandParse {
	parts := splitMaxOnce(text)
	p := commandParse{parts: parts}
	if len(parts) > 0 {
		p.command = strings.ToLower(strings.SplitN(parts[0], "@", 2)[0])
	}
	p.deep = p.command == "/start" && len(parts) > 1 && strings.HasPrefix(parts[1], "appeal")
	p.entryCommand = p.command == "/appeal" || p.command == "/appeals" ||
		p.command == "/cancel_appeal" || text == menuAppeal || p.deep
	return p
}

// splitMaxOnce 等价 Python text.split(maxsplit=1)：跳过前导空白后，把首个
// 空白「连续段」整体作为分隔符（余段保持原样，含尾随空白）。空/纯空白
// 返回空切片（Python 空白串 split(maxsplit=1) → []）。
func splitMaxOnce(s string) []string {
	s = strings.TrimLeftFunc(s, unicode.IsSpace)
	if s == "" {
		return nil
	}
	idx := strings.IndexFunc(s, unicode.IsSpace)
	if idx < 0 {
		return []string{s}
	}
	return []string{s[:idx], strings.TrimLeftFunc(s[idx:], unicode.IsSpace)}
}

// isAppealMenuText 判定 7 个主菜单/投稿键盘文本（appeal_handler.py:101-109）。
func isAppealMenuText(text string) bool {
	switch text {
	case tg.MenuAsk, tg.MenuChannels, tg.MenuClear, tg.MenuHelp,
		tg.MenuStatus, tg.MenuSubmit, tg.MenuSubscriptions:
		return true
	}
	return false
}

// HandleMessage 组 -1 消息入口（appeal_handler.py:62-132 message 的逐行
// 移植）。返回 handled：判定「可能属于申诉」即 true（分发器据此短路，
// 绝不让申诉证据漏给 AI）；任何非 GuardError 异常也置 true（fail-closed）。
func (h *AppealHandler) HandleMessage(ctx context.Context, update *models.Update) bool {
	if update == nil {
		return false
	}
	msg := update.Message
	if msg == nil || msg.From == nil {
		// Python：message = update.message; if not message or not effective_user: return
		return false
	}
	text := strings.TrimSpace(msg.Text)
	cp := parseCommandText(text, tg.MenuAppeal)

	if !isPrivateMessage(msg) {
		if cp.entryCommand {
			h.replyMessage(ctx, msg, i18n.T("appeal.private_only", nil))
			return true
		}
		return false
	}

	actor := msg.From.ID
	handled := cp.entryCommand
	err := h.messageFlow(ctx, msg, text, cp, actor, &handled)

	var ge *GuardError
	switch {
	case err == nil:
	case errors.As(err, &ge):
		// except GuardError：回复键文案。Python 语义下此时 handled 恒为
		// true（GuardError 只在入口命中或已有草稿的分支内抛出）。
		h.replyMessage(ctx, msg, i18n.T(ge.Key, nil))
	default:
		// except Exception：fail-closed——不能在无法确认是否有申诉输入时，
		// 把潜在申诉证据发给 AI（appeal_handler.py:125-130）。
		handled = true
		h.log.Errorf("助手申诉输入失败 actor=%d: %v", actor, err)
		h.replyMessage(ctx, msg, i18n.T("appeal.unavailable", nil))
	}
	return handled
}

// messageFlow 是 message() 的 try 块主体（appeal_handler.py:80-122）。
func (h *AppealHandler) messageFlow(ctx context.Context, msg *models.Message, text string, cp commandParse, actor int64, handled *bool) error {
	entry, err := h.repo.Input(ctx, actor)
	if err != nil {
		return err
	}
	if cp.entryCommand && h.submissionActiveNilSafe(actor) {
		return guardErr("appeal.submission_busy")
	}
	switch {
	case cp.command == "/cancel_appeal":
		cancelled, err := h.inboxCancel(ctx, actor, "")
		if err != nil {
			return err
		}
		return h.msgFinishInput(ctx, actor, cancelled, true)

	case cp.deep && deepLinkRe.MatchString(cp.parts[1]):
		appeal, err := h.inboxBegin(ctx, actor, strings.TrimPrefix(cp.parts[1], "appeal_"))
		if err != nil {
			return err
		}
		return h.msgShowUser(ctx, actor, appeal, cbOrigin{})

	case cp.entryCommand:
		if entry != nil && cp.command != "/appeals" {
			return h.msgShowInput(ctx, actor, entry, cbOrigin{})
		}
		kind := "cases"
		if cp.command == "/appeals" {
			kind = "history"
		}
		return h.listRows(ctx, actor, kind, 0, cbOrigin{})

	case entry != nil:
		// 进入材料收集路径：从此处起必定短路（appeal_handler.py:98）。
		*handled = true
		if cp.command == "/start" {
			return h.msgShowInput(ctx, actor, entry, cbOrigin{})
		}
		if strings.HasPrefix(text, "/") || isAppealMenuText(text) {
			return guardErr("appeal.input_busy")
		}
		media := extractMedia(msg)
		if media == nil && msg.Text == "" {
			return guardErr("appeal.media_types")
		}
		inputText := text
		if inputText == "" {
			// Python：text or (message.caption or "").strip()
			inputText = strings.TrimSpace(msg.Caption)
		}
		newEntry, err := h.inboxAppend(ctx, actor, inputText, media, int64(msg.ID))
		if err != nil {
			return err
		}
		if rowStr(newEntry, "mode") == "review" {
			// review 模式收到文本即直接提交（appeal_handler.py:118-120）。
			if _, err := h.inboxSubmit(ctx, actor, rowStr(newEntry, "token")); err != nil {
				return err
			}
			return h.msgFinishInput(ctx, actor, newEntry, false)
		}
		return h.msgShowInput(ctx, actor, newEntry, cbOrigin{})
	}
	return nil
}

// replyMessage 等价 message.reply_text（纯文本、引用原消息）。
func (h *AppealHandler) replyMessage(ctx context.Context, msg *models.Message, text string) {
	if h.tg == nil {
		return
	}
	_, err := h.tg.SendMessageWithTimeout(ctx, tg.DefaultAPITimeout, &bot.SendMessageParams{
		ChatID: msg.Chat.ID,
		Text:   text,
		ReplyParameters: &models.ReplyParameters{
			ChatID:    msg.Chat.ID,
			MessageID: msg.ID,
		},
	})
	if err != nil {
		h.log.Errorf("回复申诉提示失败 chat=%d: %v", msg.Chat.ID, err)
	}
}

// ─────────────────────────────────────────────────────────────────────
// 回调入口（appeal_handler.py:211-304 callback 的逐行移植）。
// ─────────────────────────────────────────────────────────────────────

// HandleCallback 组 -1 回调入口。Python 以 try/finally 的
// ApplicationHandlerStop 结束：除前置守卫（无 query/消息/actor）直接返回
// 外，任何路径都终止分发（返回 true）。
func (h *AppealHandler) HandleCallback(ctx context.Context, update *models.Update) bool {
	if update == nil {
		return false
	}
	query := update.CallbackQuery
	if query == nil {
		return false
	}
	var chatID int64
	var messageID int64
	var chatType models.ChatType
	switch {
	case query.Message.Message != nil:
		m := query.Message.Message
		chatID, messageID, chatType = m.Chat.ID, int64(m.ID), m.Chat.Type
	case query.Message.InaccessibleMessage != nil:
		m := query.Message.InaccessibleMessage
		chatID, messageID, chatType = m.Chat.ID, int64(m.MessageID), m.Chat.Type
	default:
		// Python：if not actor or not query or not query.message: return（不终止）。
		return false
	}
	if query.From.ID == 0 {
		// actor 为 None 的等价形态（Telegram 用户 ID 恒非 0）。
		return false
	}
	h.callbackBody(ctx, query, chatID, messageID, chatType)
	// finally: raise ApplicationHandlerStop
	return true
}

// callbackBody 是 callback() 的 try/except 链：
// GuardError → 键文案 alert；TypeError/ValueError（解析失败）→
// guard.invalid_callback alert；其余异常 → unavailable alert。
func (h *AppealHandler) callbackBody(ctx context.Context, query *models.CallbackQuery, chatID, messageID int64, chatType models.ChatType) {
	actor := query.From.ID
	answer := func(text string, alert bool) {
		if h.tg == nil {
			return
		}
		if _, err := h.tg.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
			CallbackQueryID: query.ID,
			Text:            text,
			ShowAlert:       alert,
		}); err != nil {
			h.log.Errorf("应答回调查询失败 actor=%d: %v", actor, err)
		}
	}

	err := h.callbackFlow(ctx, query, actor, chatID, messageID, chatType, answer)

	var ge *GuardError
	switch {
	case err == nil:
	case errors.As(err, &ge):
		// GuardError 与 int() 解析失败（TypeError/ValueError 的等价折算，
		// 见 callbackFlow 内 guardErr("guard.invalid_callback")）统一走
		// 键文案 alert（appeal_handler.py:296-299）。
		answer(i18n.T(ge.Key, nil), true)
	default:
		h.log.Errorf("助手申诉按钮处理失败 actor=%d: %v", actor, err)
		answer(i18n.T("appeal.unavailable", nil), true)
	}
}

// callbackAnswer 是流程内应答闭包类型。
type callbackAnswer func(text string, alert bool)

// callbackFlow 是 callback() 的 try 块主体（appeal_handler.py:216-295）。
func (h *AppealHandler) callbackFlow(ctx context.Context, query *models.CallbackQuery, actor, chatID, messageID int64, chatType models.ChatType, answer callbackAnswer) error {
	parts := strings.Split(query.Data, ":")
	if len(parts) < 3 || parts[0] != "ap" {
		return guardErr("guard.invalid_callback")
	}
	action := parts[1]
	origin := originOf(chatID, messageID)

	// 意图类动作与投稿状态互斥（appeal_handler.py:222-223）。
	if action == "review" || action == "new" || action == "supp" {
		if h.submissionActiveNilSafe(actor) {
			return guardErr("appeal.submission_busy")
		}
	}

	// ── 审核人卡片上的免私聊动作 ──────────────────────────────────
	switch action {
	case "options", "admin", "check":
		want := 4
		if action == "check" {
			want = 5
		}
		if len(parts) != want {
			return guardErr("guard.invalid_callback")
		}
		revision, convErr := strconv.ParseInt(parts[3], 10, 64)
		if convErr != nil {
			return guardErr("guard.invalid_callback")
		}
		confirm := ""
		if action == "check" {
			confirm = parts[4]
		}
		if err := h.msgReview(ctx, actor, parts[2], revision, origin, action == "options", confirm); err != nil {
			return err
		}
		answer("", false)
		return nil

	case "review", "read":
		if len(parts) != 5 || !hex32Re.MatchString(parts[2]) {
			return guardErr("guard.invalid_callback")
		}
		revision, convErr := strconv.ParseInt(parts[3], 10, 64)
		if convErr != nil {
			return guardErr("guard.invalid_callback")
		}
		if action == "read" {
			if err := h.inboxReadRequest(ctx, actor, parts[2], revision, origin, parts[4]); err != nil {
				return err
			}
		} else if parts[4] == "approve" || parts[4] == "allow" || parts[4] == "global" {
			// quick_review（approve/allow/global）：入 moderation_appeal_requests 队列。
			if err := h.inboxQuickReview(ctx, actor, parts[2], revision, origin, parts[4]); err != nil {
				return err
			}
		} else {
			// reject/more：进入私聊输入收集（appeal_handler.py:247-254）。
			entry, err := h.inboxReviewInput(ctx, actor, parts[2], revision, origin, parts[4])
			if err != nil {
				return err
			}
			if err := h.msgShowInput(ctx, actor, entry, origin); err != nil {
				var ge *GuardError
				if errors.As(err, &ge) {
					return err
				}
				// except TelegramError：无法私聊触达审核人。
				answer(i18n.T("appeal.open_private", nil), true)
				return nil
			}
		}
		text := i18n.T("appeal.queued", nil)
		if action == "review" && (parts[4] == "reject" || parts[4] == "more") {
			text = i18n.T("appeal.review_private", nil)
		}
		answer(text, false)
		return nil
	}

	// ── 以下动作仅私聊可用（appeal_handler.py:261-262）────────────
	if !isPrivateCallback(query, chatID, chatType) {
		return guardErr("appeal.private_only")
	}

	switch {
	case action == "list" && len(parts) == 4:
		offset, convErr := strconv.ParseInt(parts[3], 10, 64)
		if convErr != nil {
			return guardErr("guard.invalid_callback")
		}
		return h.listRows(ctx, actor, parts[2], offset, origin)

	case action == "new" && len(parts) == 3:
		if h.submissionActiveNilSafe(actor) {
			return guardErr("appeal.submission_busy")
		}
		appeal, err := h.inboxBegin(ctx, actor, parts[2])
		if err != nil {
			return err
		}
		return h.msgShowUser(ctx, actor, appeal, origin)

	case action == "view" && len(parts) == 3:
		appeal, err := h.repo.Get(ctx, parts[2])
		if err != nil {
			return err
		}
		return h.msgShowUser(ctx, actor, appeal, origin)

	case action == "supp" && len(parts) == 4:
		revision, convErr := strconv.ParseInt(parts[3], 10, 64)
		if convErr != nil {
			return guardErr("guard.invalid_callback")
		}
		entry, err := h.inboxSupplement(ctx, actor, parts[2], revision)
		if err != nil {
			return err
		}
		return h.msgShowInput(ctx, actor, entry, origin)

	case action == "withdraw" && len(parts) == 4:
		revision, convErr := strconv.ParseInt(parts[3], 10, 64)
		if convErr != nil {
			return guardErr("guard.invalid_callback")
		}
		appeal, err := h.inboxWithdraw(ctx, actor, parts[2], revision)
		if err != nil {
			return err
		}
		return h.msgShowUser(ctx, actor, appeal, origin)

	case action == "input" && len(parts) == 4:
		switch parts[3] {
		case "cancel":
			entry, err := h.inboxCancel(ctx, actor, parts[2])
			if err != nil {
				return err
			}
			return h.msgFinishInput(ctx, actor, entry, true)
		case "submit":
			entry, err := h.repo.Input(ctx, actor)
			if err != nil {
				return err
			}
			appeal, err := h.inboxSubmit(ctx, actor, parts[2])
			if err != nil {
				return err
			}
			if appeal != nil {
				return h.msgShowUser(ctx, actor, appeal, origin)
			}
			if entry == nil {
				// Python：finish_input(None) → TypeError → unavailable。
				return fmt.Errorf("申诉输入缺失 actor=%d", actor)
			}
			return h.msgFinishInput(ctx, actor, entry, false)
		default:
			return guardErr("guard.invalid_callback")
		}

	default:
		return guardErr("guard.invalid_callback")
	}
}

// ─────────────────────────────────────────────────────────────────────
// 列表页与媒体提取（appeal_handler.py:134-209）。
// ─────────────────────────────────────────────────────────────────────

// listRows 渲染「可申诉的封禁 / 我的申诉」分页（appeal_handler.py:156-209
// list_rows 的逐行移植）。单行首页直接展开对应用户卡片；目标消息复用时
// 先让位旧通知行，再 edit_or_send。
func (h *AppealHandler) listRows(ctx context.Context, actor int64, kind string, offset int64, origin cbOrigin) error {
	if (kind != "cases" && kind != "history") || offset < 0 || offset > 10000 {
		return guardErr("guard.invalid_callback")
	}
	rows, err := h.repo.UserRows(ctx, actor, kind == "cases", offset)
	if err != nil {
		return err
	}

	// 单条记录直接展开为用户卡片（appeal_handler.py:160-163）。
	if len(rows) == 1 && offset == 0 {
		if kind == "cases" {
			appeal, err := h.inboxBegin(ctx, actor, rowStr(rows[0], "id"))
			if err != nil {
				return err
			}
			return h.msgShowUser(ctx, actor, appeal, origin)
		}
		return h.msgShowUser(ctx, actor, rows[0], origin)
	}

	var items [][]models.InlineKeyboardButton
	for _, row := range rows[:minInt(len(rows), 5)] {
		var payload map[string]any
		if kind == "cases" {
			payload = asMap(row["payload"])
		} else {
			payload = asMap(asMap(asMap(row["payload"])["source"])["payload"])
		}
		label := truncRunes(pyStr(pyOr(payload["chat_title"], rowInt64(row, "chat_id"))), 35)
		if kind == "history" {
			label += " · " + i18n.T("appeal.status_"+rowStr(row, "status"), nil)
		}
		data := fmt.Sprintf("ap:%s:%s", mapKey(kind, "cases", "new", "view"), rowStr(row, "id"))
		items = append(items, []models.InlineKeyboardButton{{Text: label, CallbackData: data}})
	}

	var pages []models.InlineKeyboardButton
	if offset != 0 {
		prev := offset - 5
		if prev < 0 {
			prev = 0
		}
		pages = append(pages, uiButton("previous", fmt.Sprintf("ap:list:%s:%d", kind, prev)))
	}
	if len(rows) > 5 {
		pages = append(pages, uiButton("next", fmt.Sprintf("ap:list:%s:%d", kind, offset+5)))
	}
	if len(pages) > 0 {
		items = append(items, pages)
	}
	// 底部切换按钮：cases 页给「我的申诉」，history 页给「可申诉的封禁」。
	items = append(items, []models.InlineKeyboardButton{
		uiButton(mapKey(kind, "cases", "history", "new"),
			fmt.Sprintf("ap:list:%s:0", mapKey(kind, "cases", "history", "cases"))),
	})

	text := i18n.T(appealListTextKey(len(rows) > 0, kind), nil)

	return h.repo.WithLock(ctx, actor, func(s *guard.Store) error {
		messageID := origin.messageIDFor(actor)
		if messageID != 0 {
			// 翻页复用同一消息：让位旧申诉的用户通知行
			//（appeal_handler.py:203-208 的直写 SQL）。
			if _, err := s.Exec(ctx, `UPDATE moderation_appeal_notifications n JOIN moderation_appeals a ON a.id=n.appeal_id
				SET n.message_id=NULL,n.synced_revision=a.revision
				WHERE n.target_id=? AND n.kind='user' AND n.message_id=?`, actor, messageID); err != nil {
				return err
			}
		}
		_, err := h.editOrSend(ctx, actor, text, &models.InlineKeyboardMarkup{InlineKeyboard: items}, messageID, true)
		return err
	})
}

// mapKey 是条件取值小工具（等价 Python 条件表达式）。
func mapKey(kind, when, then, other string) string {
	if kind == when {
		return then
	}
	return other
}

// appealListTextKey 等价 appeal_handler.py:193-199 的三元链：
// 有记录且 cases → choose；有记录 → history_title；否则 empty。
func appealListTextKey(hasRows bool, kind string) string {
	switch {
	case hasRows && kind == "cases":
		return "appeal.choose"
	case hasRows:
		return "appeal.history_title"
	default:
		return "appeal.empty"
	}
}

// minInt 返回较小值。
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// extractMedia 等价 appeal_handler.py:134-147 extract_media：
// photo（取最大尺寸即最后一个 PhotoSize）/document/video 三类，只存
// file_id 元数据（类型、大小、所在消息 ID），不下载内容。
func extractMedia(msg *models.Message) map[string]any {
	if msg == nil {
		return nil
	}
	// photo → 最后一个尺寸（appeal_handler.py:138-139）。
	if len(msg.Photo) > 0 {
		photo := msg.Photo[len(msg.Photo)-1]
		return map[string]any{
			"type":       "photo",
			"file_id":    photo.FileID,
			"size":       int64(photo.FileSize),
			"message_id": int64(msg.ID),
		}
	}
	if msg.Document != nil {
		return map[string]any{
			"type":       "document",
			"file_id":    msg.Document.FileID,
			"size":       msg.Document.FileSize,
			"message_id": int64(msg.ID),
		}
	}
	if msg.Video != nil {
		return map[string]any{
			"type":       "video",
			"file_id":    msg.Video.FileID,
			"size":       msg.Video.FileSize,
			"message_id": int64(msg.ID),
		}
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────
// 输入边界（core/moderation/appeal_inbox.py 的逐行移植；全部在
// guard.WithLock 内执行，对齐 Python `async with self.repository.lock`）。
// ─────────────────────────────────────────────────────────────────────

// ownAppeal 等价 AppealInbox.own（appeal_inbox.py:21-24）：
// 申诉不存在或不属于该用户即 guard.invalid_callback。
func ownAppeal(appeal guard.Row, userID int64) error {
	if appeal == nil || rowInt64(appeal, "user_id") != userID {
		return guardErr("guard.invalid_callback")
	}
	return nil
}

// inboxBegin 开启新申诉草稿（appeal_inbox.py:26-48 begin 的逐行移植）：
// 校验案件归属与可申诉性 → 活跃申诉复用 → 草稿互斥 → 24h 冷却与
// 3 次/24h 配额 → 记录黑名单快照并建 draft。
func (h *AppealHandler) inboxBegin(ctx context.Context, userID int64, caseID string) (guard.Row, error) {
	var out guard.Row
	err := h.repo.WithLock(ctx, userID, func(s *guard.Store) error {
		caseRow, err := s.Case(ctx, caseID)
		if err != nil {
			return err
		}
		if caseRow == nil || rowInt64(caseRow, "user_id") != userID || !eligibleCase(caseRow) {
			return guardErr("appeal.no_case")
		}
		existing, err := s.Active(ctx, rowInt64(caseRow, "chat_id"), userID)
		if err != nil {
			return err
		}
		if existing != nil {
			out = existing
			return nil
		}
		draft, err := s.Input(ctx, userID)
		if err != nil {
			return err
		}
		if draft != nil {
			return guardErr("appeal.input_busy")
		}
		// 冷却判定短路：同 case 24h 内被拒，或 24h 内提交已达 3 次。
		cooling, err := s.RejectedRecently(ctx, caseRow)
		if err != nil {
			return err
		}
		if !cooling {
			count, err := s.SubmissionCount(ctx, userID)
			if err != nil {
				return err
			}
			cooling = count >= appealCooldownSubmissions
		}
		if cooling {
			return guardErr("appeal.cooldown")
		}
		black, err := s.Blacklist(ctx, userID)
		if err != nil {
			return err
		}
		out, err = s.Create(ctx, caseRow, map[string]any{
			"source": map[string]any{
				"id":      caseRow["id"],
				"action":  caseRow["action"],
				"status":  caseRow["status"],
				"payload": caseRow["payload"],
			},
			"source_created_at": pyStrDatetimeValue(caseRow["created_at"]),
			"source_signature":  sourceSignature(caseRow),
			"blacklisted":       black != nil,
			"evidence":          []any{},
		})
		return err
	})
	return out, err
}

// pyStrDatetimeValue 对 DB 时间列做 str(datetime) 字符串化（time.Time 走
// Python 形态；字符串/字节列原样）。
func pyStrDatetimeValue(v any) string {
	if t, ok := v.(time.Time); ok {
		return pyStrDatetime(t)
	}
	return pyStr(v)
}

// inboxSupplement 发起补充材料输入（appeal_inbox.py:50-69 supplement）：
// 仅 need_info 且版本匹配；证据轮数上限 6。
func (h *AppealHandler) inboxSupplement(ctx context.Context, userID int64, appealID string, revision int64) (guard.Row, error) {
	var out guard.Row
	err := h.repo.WithLock(ctx, userID, func(s *guard.Store) error {
		appeal, err := s.Get(ctx, appealID)
		if err != nil {
			return err
		}
		if err := ownAppeal(appeal, userID); err != nil {
			return err
		}
		if rowStr(appeal, "status") != "need_info" || rowInt64(appeal, "revision") != revision {
			return guardErr("appeal.stale")
		}
		draft, err := s.Input(ctx, userID)
		if err != nil {
			return err
		}
		if draft != nil {
			return guardErr("appeal.input_busy")
		}
		if int64(len(asList(asMap(appeal["payload"])["evidence"]))) >= 6 {
			return guardErr("appeal.evidence_limit")
		}
		out, err = s.SaveInput(ctx, userID, appealID, "supplement",
			map[string]any{"text": "", "files": []any{}, "revision": revision}, "")
		return err
	})
	return out, err
}

// inboxAppend 追加申诉材料（appeal_inbox.py:71-99 append 的逐行移植）：
// 消息 ID 去重（最近 30 条）；review 模式 1-1000 字必填；草稿/补充模式
// 累计 ≤2000 字、附件 ≤5 个且单个 ≤20MB（仅存 file_id 元数据）。
func (h *AppealHandler) inboxAppend(ctx context.Context, actorID int64, text string, media map[string]any, messageID int64) (guard.Row, error) {
	var out guard.Row
	err := h.repo.WithLock(ctx, actorID, func(s *guard.Store) error {
		entry, err := s.Input(ctx, actorID)
		if err != nil {
			return err
		}
		if entry == nil {
			return guardErr("appeal.stale")
		}
		appeal, err := s.Get(ctx, rowStr(entry, "appeal_id"))
		if err != nil {
			return err
		}
		if err := validateInput(entry, appeal); err != nil {
			return err
		}
		payload := copyMap(asMap(entry["payload"]))
		messages := asList(payload["messages"])
		for _, m := range messages {
			// message_id 去重（appeal_inbox.py:79-80）。
			if pyInt(m) == messageID {
				out = entry
				return nil
			}
		}
		mode := rowStr(entry, "mode")
		if mode == "review" {
			length := utf8.RuneCountInString(text)
			if media != nil || text == "" || length < 1 || length > appealReviewTextLimit {
				return guardErr("appeal.reason_required")
			}
			payload["text"] = text
		} else {
			// 累计文本（非空段落以换行拼接）。
			segments := make([]string, 0, 2)
			if prev := pyStr(payload["text"]); prev != "" {
				segments = append(segments, prev)
			}
			if text != "" {
				segments = append(segments, text)
			}
			combined := strings.Join(segments, "\n")
			if utf8.RuneCountInString(combined) > appealTextLimit {
				return guardErr("appeal.text_limit")
			}
			if text == "" && media == nil {
				return guardErr("appeal.media_types")
			}
			payload["text"] = combined
			if media != nil {
				files := asList(payload["files"])
				if int64(len(files)) >= appealFileLimit || pyInt(media["size"]) > appealFileSizeLimit {
					return guardErr("appeal.file_limit")
				}
				payload["files"] = append(files, media)
			}
		}
		payload["messages"] = appendCapped(messages, messageID, appealMessagesCap)
		out, err = s.SaveInput(ctx, actorID, rowStr(entry, "appeal_id"), mode, payload, rowStr(entry, "token"))
		return err
	})
	return out, err
}

// appendCapped 等价 Python `[*old, v][-limit:]`（保留最近 limit 条）。
func appendCapped(old []any, v int64, limit int) []any {
	out := append(append([]any{}, old...), v)
	if len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}

// copyMap 浅拷贝 map（等价 Python dict(payload)）。
func copyMap(src map[string]any) map[string]any {
	dst := make(map[string]any, len(src)+2)
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

// validateInput 等价 appeal_inbox.py:101-111 validate_input（静态方法）：
// 草稿要求申诉仍处 draft；补充/审核要求状态与版本均匹配。
func validateInput(entry, appeal guard.Row) error {
	if appeal == nil {
		return guardErr("appeal.stale")
	}
	mode := rowStr(entry, "mode")
	payload := asMap(entry["payload"])
	switch mode {
	case "draft":
		if rowStr(appeal, "status") != "draft" {
			return guardErr("appeal.stale")
		}
	case "supplement", "review":
		want := "pending"
		if mode == "supplement" {
			want = "need_info"
		}
		if rowStr(appeal, "status") != want || rowInt64(appeal, "revision") != pyInt(payload["revision"]) {
			return guardErr("appeal.stale")
		}
	}
	return nil
}

// inboxSubmit 提交输入（appeal_inbox.py:113-167 submit 的逐行移植）：
//   - review 模式：原因必填（快速审核三键有默认文案），事务内入
//     moderation_appeal_requests 队列（幂等 id = token 补零至 64 位）并清
//     输入，返回 nil；
//   - draft/supplement：文本 1-2000 必填、24h 配额，事务内 transition
//     （draft→submitted / supplement→pending）并清输入，返回迁移后申诉行。
func (h *AppealHandler) inboxSubmit(ctx context.Context, actorID int64, token string) (guard.Row, error) {
	var result guard.Row
	err := h.repo.WithLock(ctx, actorID, func(s *guard.Store) error {
		entry, err := s.Input(ctx, actorID)
		if err != nil {
			return err
		}
		if entry == nil || rowStr(entry, "token") != token {
			return guardErr("appeal.stale")
		}
		appeal, err := s.Get(ctx, rowStr(entry, "appeal_id"))
		if err != nil {
			return err
		}
		if err := validateInput(entry, appeal); err != nil {
			return err
		}
		payload := asMap(entry["payload"])
		mode := rowStr(entry, "mode")

		if mode == "review" {
			reason := pyStr(payload["text"])
			if reason == "" {
				switch pyStr(payload["action"]) {
				case "approve", "allow", "global":
					reason = i18n.T("appeal.default_approve", nil)
				}
			}
			if reason == "" {
				return guardErr("appeal.reason_required")
			}
			origin := asList(payload["origin"])
			return s.Atomic(ctx, func(s *guard.Store) error {
				if err := s.QueueRequest(ctx, rowStr(appeal, "id"), actorID,
					pyStr(payload["action"]), pyInt(payload["revision"]),
					pyInt(listAt(origin, 0)), pyInt(listAt(origin, 1)),
					reason, padRight64(token)); err != nil {
					return err
				}
				return s.ClearInput(ctx, actorID, token)
			})
		}

		if err := ownAppeal(appeal, actorID); err != nil {
			return err
		}
		trimmed := strings.TrimSpace(pyStr(payload["text"]))
		if length := utf8.RuneCountInString(trimmed); length < 1 || length > appealTextLimit {
			return guardErr("appeal.text_required")
		}
		if mode == "draft" {
			count, err := s.SubmissionCount(ctx, actorID)
			if err != nil {
				return err
			}
			if count >= appealCooldownSubmissions {
				return guardErr("appeal.cooldown")
			}
		}
		appealPayload := asMap(appeal["payload"])
		updated := copyMap(appealPayload)
		evidence := map[string]any{
			"text":     trimmed,
			"files":    asList(payload["files"]),
			"messages": asList(payload["messages"]),
			"at":       db.PythonISOFormat(db.NowUTC()),
			"user_id":  actorID,
		}
		updated["evidence"] = append(asList(appealPayload["evidence"]), evidence)

		status, action := "pending", "supplemented"
		if mode == "draft" {
			status, action = "submitted", "submitted"
		}
		return s.Atomic(ctx, func(s *guard.Store) error {
			r, err := s.TransitionTx(ctx, appeal, status,
				fmt.Sprintf("telegram:%d", actorID), action,
				guard.TransitionOptions{Payload: updated, Detail: evidence})
			if err != nil {
				return err
			}
			result = r
			return s.ClearInput(ctx, actorID, token)
		})
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// listAt 取列表元素（越界返回 nil，等价 Python 索引存在性由调用方保证）。
func listAt(list []any, i int) any {
	if i < len(list) {
		return list[i]
	}
	return nil
}

// padRight64 等价 Python token.ljust(64, "0")。
func padRight64(token string) string {
	if len(token) >= 64 {
		return token
	}
	return token + strings.Repeat("0", 64-len(token))
}

// inboxWithdraw 撤回申诉（appeal_inbox.py:169-185 withdraw）：仅进行中
// 状态且版本匹配；事务内 transition 到 withdrawn 并连带清输入。
func (h *AppealHandler) inboxWithdraw(ctx context.Context, userID int64, appealID string, revision int64) (guard.Row, error) {
	var result guard.Row
	err := h.repo.WithLock(ctx, userID, func(s *guard.Store) error {
		appeal, err := s.Get(ctx, appealID)
		if err != nil {
			return err
		}
		if err := ownAppeal(appeal, userID); err != nil {
			return err
		}
		switch rowStr(appeal, "status") {
		case "draft", "submitted", "pending", "need_info":
		default:
			return guardErr("appeal.stale")
		}
		if rowInt64(appeal, "revision") != revision {
			return guardErr("appeal.stale")
		}
		return s.Atomic(ctx, func(s *guard.Store) error {
			r, err := s.TransitionTx(ctx, appeal, "withdrawn",
				fmt.Sprintf("telegram:%d", userID), "withdrawn", guard.TransitionOptions{})
			if err != nil {
				return err
			}
			result = r
			entry, err := s.Input(ctx, userID)
			if err != nil {
				return err
			}
			if entry != nil && rowStr(entry, "appeal_id") == appealID {
				return s.ClearInput(ctx, userID, rowStr(entry, "token"))
			}
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// inboxCancel 取消当前输入（appeal_inbox.py:187-199 cancel）：token 传空串
// 等价 Python 的 token=None（不校验）；未提交草稿连带撤回申诉。
func (h *AppealHandler) inboxCancel(ctx context.Context, actorID int64, token string) (guard.Row, error) {
	var out guard.Row
	err := h.repo.WithLock(ctx, actorID, func(s *guard.Store) error {
		entry, err := s.Input(ctx, actorID)
		if err != nil {
			return err
		}
		if entry == nil || (token != "" && token != rowStr(entry, "token")) {
			return guardErr("appeal.stale")
		}
		appeal, err := s.Get(ctx, rowStr(entry, "appeal_id"))
		if err != nil {
			return err
		}
		entryToken := rowStr(entry, "token")
		err = s.Atomic(ctx, func(s *guard.Store) error {
			if rowStr(entry, "mode") == "draft" && appeal != nil && rowStr(appeal, "status") == "draft" {
				if _, err := s.TransitionTx(ctx, appeal, "withdrawn",
					fmt.Sprintf("telegram:%d", actorID), "withdrawn", guard.TransitionOptions{}); err != nil {
					return err
				}
			}
			return s.ClearInput(ctx, actorID, entryToken)
		})
		if err != nil {
			return err
		}
		out = entry
		return nil
	})
	return out, err
}

// inboxReviewInput 进入审核意见输入（appeal_inbox.py:201-229 review_input）：
// 已有同目标 review 输入时直接复用；不同申诉/模式的输入互斥。
func (h *AppealHandler) inboxReviewInput(ctx context.Context, actorID int64, appealID string, revision int64, origin cbOrigin, action string) (guard.Row, error) {
	if !reviewActions[action] {
		return nil, guardErr("guard.invalid_callback")
	}
	var out guard.Row
	err := h.repo.WithLock(ctx, actorID, func(s *guard.Store) error {
		appeal, err := s.Get(ctx, appealID)
		if err != nil {
			return err
		}
		if err := h.checkOrigin(ctx, s, appeal, actorID, revision, origin); err != nil {
			return err
		}
		if rowStr(appeal, "status") != "pending" {
			return guardErr("appeal.stale")
		}
		existing, err := s.Input(ctx, actorID)
		if err != nil {
			return err
		}
		if existing != nil {
			payload := asMap(existing["payload"])
			sameTarget := rowStr(existing, "mode") == "review" && rowStr(existing, "appeal_id") == appealID
			if !sameTarget {
				return guardErr("appeal.input_busy")
			}
			if pyStr(payload["action"]) == action && pyInt(payload["revision"]) == revision &&
				pyInt(listAt(asList(payload["origin"]), 0)) == origin.chatID &&
				pyInt(listAt(asList(payload["origin"]), 1)) == origin.messageID {
				out = existing
				return nil
			}
		}
		out, err = s.SaveInput(ctx, actorID, appealID, "review", map[string]any{
			"action":   action,
			"revision": revision,
			"origin":   []any{origin.chatID, origin.messageID},
			"text":     "",
		}, "")
		return err
	})
	return out, err
}

// inboxQuickReview 快速审核（appeal_inbox.py:231-279 quick_review 的逐行
// 移植）：approve/allow/global 入 moderation_appeal_requests 队列；
// global 需系统管理员；allow 撞全局黑名单时记审计并拒绝；幂等 ID =
// sha256(review:…)（guard.QuickReviewRequestID，同键去重）。
func (h *AppealHandler) inboxQuickReview(ctx context.Context, actorID int64, appealID string, revision int64, origin cbOrigin, action string) error {
	if action != "approve" && action != "allow" && action != "global" {
		return guardErr("guard.invalid_callback")
	}
	return h.repo.WithLock(ctx, actorID, func(s *guard.Store) error {
		appeal, err := s.Get(ctx, appealID)
		if err != nil {
			return err
		}
		if err := h.checkOrigin(ctx, s, appeal, actorID, revision, origin); err != nil {
			return err
		}
		if rowStr(appeal, "status") != "pending" {
			return guardErr("appeal.stale")
		}
		if action == "global" {
			if !h.isAdminNilSafe(actorID) {
				return guardErr("appeal.global_admin_required")
			}
		} else if black, err := s.Blacklist(ctx, rowInt64(appeal, "user_id")); err != nil {
			return err
		} else if black != nil {
			// 拦截结果直接返回点击提示，不把注定失败的操作显示为“正在处理”
			//（appeal_inbox.py:245-253）。
			if err := s.Event(ctx, appeal, fmt.Sprintf("telegram:%d", actorID),
				"request_denied", rowStr(appeal, "status"),
				map[string]any{"action": action, "error": "guard.blacklist_conflict"}); err != nil {
				return err
			}
			return guardErr("guard.blacklist_conflict")
		}
		entry, err := s.Input(ctx, actorID)
		if err != nil {
			return err
		}
		if entry != nil && (rowStr(entry, "mode") != "review" || rowStr(entry, "appeal_id") != appealID) {
			return guardErr("appeal.input_busy")
		}
		latest, err := s.One(ctx, `SELECT id,status FROM moderation_appeal_requests
			WHERE appeal_id=? AND actor_id=? AND revision=? AND action=?
			ORDER BY created_at DESC,id DESC LIMIT 1`,
			appealID, actorID, revision, action)
		if err != nil {
			return err
		}
		if latest != nil && rowStr(latest, "status") != "failed" {
			// 已有同键请求在途/完成：幂等直接返回（appeal_inbox.py:263-264）。
			return nil
		}
		latestID := ""
		if latest != nil {
			latestID = rowStr(latest, "id")
		}
		requestID := guard.QuickReviewRequestID(appealID, actorID, revision, origin.chatID, origin.messageID, action, latestID)
		return s.Atomic(ctx, func(s *guard.Store) error {
			if err := s.QueueRequest(ctx, appealID, actorID, action, revision,
				origin.chatID, origin.messageID, i18n.T("appeal.default_approve", nil), requestID); err != nil {
				return err
			}
			if entry != nil {
				return s.ClearInput(ctx, actorID, rowStr(entry, "token"))
			}
			return nil
		})
	})
}

// checkOrigin 等价 appeal_inbox.py:281-293 check_origin：审核人身份只能来自
// 同步到当前版本的服务器卡片（reviewer 通知行匹配）。
func (h *AppealHandler) checkOrigin(ctx context.Context, s *guard.Store, appeal guard.Row, actorID, revision int64, origin cbOrigin) error {
	if appeal == nil || rowInt64(appeal, "user_id") == actorID || rowInt64(appeal, "revision") != revision {
		return guardErr("appeal.stale")
	}
	if origin.chatID > 0 && origin.chatID != actorID {
		return guardErr("guard.invalid_callback")
	}
	notices, err := s.Notices(ctx, rowStr(appeal, "id"))
	if err != nil {
		return err
	}
	for _, row := range notices {
		if rowInt64(row, "target_id") == origin.chatID &&
			rowInt64(row, "message_id") == origin.messageID &&
			rowStr(row, "kind") == "reviewer" &&
			rowInt64(row, "synced_revision") == revision {
			return nil
		}
	}
	return guardErr("guard.invalid_callback")
}

// inboxReadRequest 请求材料/审计明细（appeal_inbox.py:295-303 read_request）：
// 幂等 ID 用随机 uuid（重复点击产生新请求由主进程侧校验去重）。
func (h *AppealHandler) inboxReadRequest(ctx context.Context, actorID int64, appealID string, revision int64, origin cbOrigin, action string) error {
	if !readActions[action] {
		return guardErr("guard.invalid_callback")
	}
	return h.repo.WithLock(ctx, actorID, func(s *guard.Store) error {
		appeal, err := s.Get(ctx, appealID)
		if err != nil {
			return err
		}
		if err := h.checkOrigin(ctx, s, appeal, actorID, revision, origin); err != nil {
			return err
		}
		return s.QueueRequest(ctx, appealID, actorID, action, revision,
			origin.chatID, origin.messageID, "", padRight64(uuid4Hex32()))
	})
}

// uuid4Hex32 生成 uuid4().hex 等价的 32 位十六进制串（stdlib crypto/rand）。
func uuid4Hex32() string {
	var b [16]byte
	if _, err := crand.Read(b[:]); err != nil {
		panic(fmt.Sprintf("crypto/rand 读取失败: %v", err))
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return hex.EncodeToString(b[:])
}

// ─────────────────────────────────────────────────────────────────────
// 消息层（core/handlers/appeal_messages.py 的逐行移植）：
// edit_or_send 卡片复用与 user/input/review/finish_input。
// ─────────────────────────────────────────────────────────────────────

// editOrSend 等价 appeal_messages.py:16-41 edit_or_send：优先原位编辑
// （HTML + 关闭链接预览 + 25s 超时）；"not modified" 视为成功；"not found" /
// "can't be edited" / "no text in the message" 回退为新消息；fallback=false
// 时回退被禁用（返回 0，等价 None）。其余错误原样上抛。
func (h *AppealHandler) editOrSend(ctx context.Context, chatID int64, text string, markup models.ReplyMarkup, messageID int64, fallback bool) (int64, error) {
	send := func() (int64, error) {
		if h.tg == nil {
			return 0, errors.New("telegram 客户端未装配")
		}
		msg, err := h.tg.SendMessageWithTimeout(ctx, tg.AppealCardTimeout, &bot.SendMessageParams{
			ChatID:             chatID,
			Text:               text,
			ParseMode:          models.ParseModeHTML,
			ReplyMarkup:        markup,
			LinkPreviewOptions: previewDisabled(),
		})
		if err != nil {
			return 0, err
		}
		return int64(msg.ID), nil
	}
	if messageID != 0 {
		if h.tg == nil {
			return 0, errors.New("telegram 客户端未装配")
		}
		_, err := h.tg.EditMessageTextWithTimeout(ctx, tg.AppealCardTimeout, &bot.EditMessageTextParams{
			ChatID:             chatID,
			MessageID:          int(messageID),
			Text:               text,
			ParseMode:          models.ParseModeHTML,
			ReplyMarkup:        markup,
			LinkPreviewOptions: previewDisabled(),
		})
		if err == nil {
			return messageID, nil
		}
		// 仅 BadRequest 语义进入回退判定；网络/超时等直接上抛
		//（对齐 Python except BadRequest）。
		if errors.Is(err, bot.ErrorBadRequest) {
			reason := strings.ToLower(err.Error())
			if strings.Contains(reason, "not modified") {
				return messageID, nil
			}
			if !strings.Contains(reason, "not found") &&
				!strings.Contains(reason, "can't be edited") &&
				!strings.Contains(reason, "no text in the message") {
				return 0, err
			}
		} else {
			return 0, err
		}
	}
	if !fallback {
		return 0, nil
	}
	return send()
}

// previewDisabled 等价 disable_web_page_preview=True 的现代参数形态。
func previewDisabled() *models.LinkPreviewOptions {
	disabled := true
	return &models.LinkPreviewOptions{IsDisabled: &disabled}
}

// msgShowUser 等价 appeal_handler.py:152-154 show_user：归属校验后渲染
// 用户侧申诉卡片。
func (h *AppealHandler) msgShowUser(ctx context.Context, actor int64, appeal guard.Row, origin cbOrigin) error {
	if err := ownAppeal(appeal, actor); err != nil {
		return err
	}
	return h.msgUser(ctx, actor, rowStr(appeal, "id"), origin)
}

// msgUser 渲染用户申诉卡片或输入界面（appeal_messages.py:48-72 user 的
// 逐行移植）：有未完成 draft/supplement 输入且目标一致时改显示输入界面；
// 成功后事务内绑定用户卡片并回写输入的 ui_message_id。
func (h *AppealHandler) msgUser(ctx context.Context, actor int64, appealID string, origin cbOrigin) error {
	return h.repo.WithLock(ctx, actor, func(s *guard.Store) error {
		appeal, err := s.Get(ctx, appealID)
		if err != nil {
			return err
		}
		if err := ownAppeal(appeal, actor); err != nil {
			return err
		}
		entry, err := s.Input(ctx, actor)
		if err != nil {
			return err
		}
		mode := ""
		if entry != nil {
			mode = rowStr(entry, "mode")
		}
		editing := entry != nil && rowStr(entry, "appeal_id") == appealID &&
			(mode == "draft" || mode == "supplement")
		if editing {
			if err := validateInput(entry, appeal); err != nil {
				return err
			}
		}
		notices, err := s.Notices(ctx, appealID)
		if err != nil {
			return err
		}
		var notice guard.Row
		for _, n := range notices {
			if rowInt64(n, "target_id") == actor {
				notice = n
				break
			}
		}
		messageID := origin.messageIDFor(actor)
		if messageID == 0 && notice != nil {
			messageID = rowInt64(notice, "message_id")
		}
		text := uiCard(appeal, false)
		markup := uiButtons(appeal, false, false, "")
		if editing {
			text = uiInputPrompt(entry, appeal)
			markup = uiInputButtons(entry)
		}
		newID, err := h.editOrSend(ctx, actor, text, markup, messageID, true)
		if err != nil {
			return err
		}
		return s.Atomic(ctx, func(s *guard.Store) error {
			if err := s.BindUserCard(ctx, appeal, newID); err != nil {
				return err
			}
			if editing {
				return s.InputMessage(ctx, actor, rowStr(entry, "token"), newID)
			}
			return nil
		})
	})
}

// msgShowInput 等价 appeal_handler.py:149-150 show_input（分发到
// messages.input）。
func (h *AppealHandler) msgShowInput(ctx context.Context, actor int64, entry guard.Row, origin cbOrigin) error {
	return h.msgInput(ctx, actor, entry, origin)
}

// msgInput 渲染输入界面（appeal_messages.py:74-99 input 的逐行移植）：
// 非 review 模式复用用户卡片路径；review 模式持申诉人锁渲染审核卡片 +
// 输入提示（不同时持有两把锁，避免跨群/多角色相互等待）。
func (h *AppealHandler) msgInput(ctx context.Context, actor int64, entry guard.Row, origin cbOrigin) error {
	if entry == nil {
		return guardErr("appeal.stale")
	}
	if rowStr(entry, "mode") != "review" {
		return h.msgUser(ctx, actor, rowStr(entry, "appeal_id"), origin)
	}
	initial, err := h.repo.Get(ctx, rowStr(entry, "appeal_id"))
	if err != nil {
		return err
	}
	if initial == nil {
		return guardErr("appeal.stale")
	}
	return h.repo.WithLock(ctx, rowInt64(initial, "user_id"), func(s *guard.Store) error {
		current, err := s.Input(ctx, actor)
		if err != nil {
			return err
		}
		if current == nil || rowStr(current, "token") != rowStr(entry, "token") {
			return guardErr("appeal.stale")
		}
		appeal, err := s.Get(ctx, rowStr(entry, "appeal_id"))
		if err != nil {
			return err
		}
		if err := validateInput(current, appeal); err != nil {
			return err
		}
		payload := asMap(current["payload"])
		sourceOrigin := asList(payload["origin"])
		messageID := pyInt(payload["ui_message_id"])
		if messageID == 0 && pyInt(listAt(sourceOrigin, 0)) == actor {
			messageID = pyInt(listAt(sourceOrigin, 1))
		}
		text := uiCard(appeal, true) + "\n\n" + uiInputPrompt(current, nil)
		newID, err := h.editOrSend(ctx, actor, text, uiInputButtons(current), messageID, true)
		if err != nil {
			return err
		}
		return s.InputMessage(ctx, actor, rowStr(current, "token"), newID)
	})
}

// msgReview 渲染审核卡片（appeal_messages.py:101-129 review 的逐行移植）：
// options/check 需 pending 状态；confirm=global 需系统管理员并追加确认问句；
// 成功后把消息绑定为 reviewer 通知行。
func (h *AppealHandler) msgReview(ctx context.Context, actor int64, appealID string, revision int64, origin cbOrigin, expanded bool, confirm string) error {
	initial, err := h.repo.Get(ctx, appealID)
	if err != nil {
		return err
	}
	if initial == nil {
		return guardErr("appeal.stale")
	}
	return h.repo.WithLock(ctx, rowInt64(initial, "user_id"), func(s *guard.Store) error {
		appeal, err := s.Get(ctx, appealID)
		if err != nil {
			return err
		}
		if err := h.checkOrigin(ctx, s, appeal, actor, revision, origin); err != nil {
			return err
		}
		if (expanded || confirm != "") && rowStr(appeal, "status") != "pending" {
			return guardErr("appeal.stale")
		}
		text := uiCard(appeal, true)
		if confirm != "" {
			if confirm != "allow" && confirm != "global" {
				return guardErr("guard.invalid_callback")
			}
			if confirm == "global" && !h.isAdminNilSafe(actor) {
				return guardErr("appeal.global_admin_required")
			}
			text += "\n\n" + i18n.T("appeal.confirm_"+confirm, nil)
		}
		newID, err := h.editOrSend(ctx, origin.chatID, text,
			uiButtons(appeal, true, expanded, confirm), origin.messageID, true)
		if err != nil {
			return err
		}
		return s.SaveNotice(ctx, appeal, origin.chatID, newID, "reviewer")
	})
}

// msgFinishInput 输入结束的收尾渲染（appeal_messages.py:131-176
// finish_input 的逐行移植）：非 review 复用用户卡片；review 按审核人
// 是否仍持有原卡片消息决定原位刷新或纯提示。
func (h *AppealHandler) msgFinishInput(ctx context.Context, actor int64, entry guard.Row, cancelled bool) error {
	if entry == nil {
		// Python：entry["mode"] → TypeError → 上层 unavailable。
		return fmt.Errorf("申诉输入缺失 actor=%d", actor)
	}
	if rowStr(entry, "mode") != "review" {
		return h.msgUser(ctx, actor, rowStr(entry, "appeal_id"), cbOrigin{})
	}
	initial, err := h.repo.Get(ctx, rowStr(entry, "appeal_id"))
	if err != nil {
		return err
	}
	if initial == nil {
		return nil
	}
	return h.repo.WithLock(ctx, rowInt64(initial, "user_id"), func(s *guard.Store) error {
		appeal, err := s.Get(ctx, rowStr(entry, "appeal_id"))
		if err != nil {
			return err
		}
		payload := asMap(entry["payload"])
		sourceOrigin := asList(payload["origin"])
		originChat := pyInt(listAt(sourceOrigin, 0))
		originMsg := pyInt(listAt(sourceOrigin, 1))
		uiMessageID := pyInt(payload["ui_message_id"])

		noticeText := i18n.T("appeal.review_submitted", nil)
		if cancelled {
			noticeText = i18n.T("appeal.review_cancelled", nil)
		}
		if originChat == actor {
			notices, err := s.Notices(ctx, rowStr(appeal, "id"))
			if err != nil {
				return err
			}
			var notice guard.Row
			for _, n := range notices {
				if rowStr(n, "kind") == "reviewer" &&
					rowInt64(n, "target_id") == originChat &&
					rowInt64(n, "message_id") == originMsg {
					notice = n
					break
				}
			}
			if notice == nil {
				_, err := h.editOrSend(ctx, actor, noticeText, nil, uiMessageID, cancelled)
				return err
			}
			text := uiCard(appeal, true)
			if !cancelled && rowStr(appeal, "status") == "pending" {
				text += "\n\n" + noticeText
			}
			newID, err := h.editOrSend(ctx, actor, text, uiButtons(appeal, true, false, ""), originMsg, cancelled)
			if err != nil {
				return err
			}
			if cancelled {
				return s.SaveNotice(ctx, appeal, actor, newID, "reviewer")
			}
			return nil
		}
		_, err = h.editOrSend(ctx, actor, noticeText, nil, uiMessageID, true)
		return err
	})
}

// ─────────────────────────────────────────────────────────────────────
// 通知投递 job（appeal_handler.py:306-544 deliver/deliver_notice/
// deliver_result 的逐行移植）。main 以独立 goroutine 启动：
// 先 sleep 5s，再以 5s ticker 驱动（对齐 PTB job interval=5, first=5）。
// ─────────────────────────────────────────────────────────────────────

// appealDeliverInterval 通知投递轮询周期（qa_bot.py:991 interval=5, first=5）。
const (
	appealDeliverInterval = 5 * time.Second
	appealDeliverFirst    = 5 * time.Second
	// appealResultRetrySeconds 审核回执发送失败后的固定重试间隔
	//（appeal_handler.py:540-543）。
	appealResultRetrySeconds = 60 * time.Second
)

// RunAppealDeliver 阻塞运行申诉投递 job（main 起 goroutine 调用）：
// sleep 5s 后每 5s 执行一轮 Deliver；ctx 取消即退出。每轮 recover 记日志，
// panic 不杀进程（README 硬规则 1）。
func (h *AppealHandler) RunAppealDeliver(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(appealDeliverFirst):
	}
	ticker := time.NewTicker(appealDeliverInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			func() {
				defer func() {
					if r := recover(); r != nil {
						h.log.Errorf("助手申诉通知轮询失败，将重试: panic=%v", r)
					}
				}()
				h.Deliver(ctx)
			}()
		}
	}
}

// Deliver 单轮投递（appeal_handler.py:306-314 deliver 的逐行移植）：
// 先通知行后请求回执；deliver_notice 内部自捕获异常，deliver_result
// 的异常会中止本轮剩余回执（对齐 Python 控制流），统一记日志等下轮。
func (h *AppealHandler) Deliver(ctx context.Context) {
	notices, results, err := h.repo.Deliveries(ctx)
	if err != nil {
		h.log.Errorf("助手申诉通知轮询失败，将重试: %v", err)
		return
	}
	for _, notice := range notices {
		if err := h.deliverNotice(ctx, notice); err != nil {
			h.log.Errorf("助手申诉通知轮询失败，将重试: %v", err)
		}
	}
	for _, request := range results {
		if err := h.deliverResult(ctx, request); err != nil {
			h.log.Errorf("助手申诉通知轮询失败，将重试: %v", err)
			return
		}
	}
}

// deliverNotice 投递单条申诉卡片通知（appeal_handler.py:316-392
// deliver_notice 的逐行移植）：锁内复核通知行的新鲜度 → edit_or_send
// 复用卡片 → 用户侧旧消息附提醒 → 事务内落进度；失败按
// min(1800, 15*2^min(attempts,7)) 秒写回 retry_at。
func (h *AppealHandler) deliverNotice(ctx context.Context, candidate guard.Row) error {
	return h.repo.WithLock(ctx, rowInt64(candidate, "user_id"), func(s *guard.Store) error {
		appeal, err := s.Get(ctx, rowStr(candidate, "appeal_id"))
		if err != nil {
			return err
		}
		if appeal == nil {
			// Python：appeal["id"] → TypeError → 由 deliver 外层捕获中止本轮。
			return fmt.Errorf("申诉不存在 appeal=%s", rowStr(candidate, "appeal_id"))
		}
		appealID := rowStr(appeal, "id")
		notices, err := s.Notices(ctx, appealID)
		if err != nil {
			return err
		}
		var notice guard.Row
		for _, n := range notices {
			if rowInt64(n, "target_id") == rowInt64(candidate, "target_id") {
				notice = n
				break
			}
		}
		if notice == nil || rowStr(notice, "kind") == "retired" ||
			rowInt64(notice, "synced_revision") >= rowInt64(appeal, "revision") {
			return nil
		}

		kind := rowStr(notice, "kind")
		reviewer := kind == "reviewer"
		targetID := rowInt64(notice, "target_id")

		newID, err := h.editOrSend(ctx, targetID,
			uiCard(appeal, reviewer), uiButtons(appeal, reviewer, false, ""),
			rowInt64(notice, "message_id"), true)
		if err == nil && kind == "user" && rowInt64(notice, "message_id") != 0 {
			// 旧消息上刷新成功才补发状态提醒（appeal_handler.py:341-357）。
			if alert := uiUserAlert(appeal); alert != "" {
				err = h.sendAlert(ctx, targetID, alert, appealID)
			}
		}
		if err != nil {
			attempts := rowInt64(notice, "attempts") + 1
			errName := errTypeName(err)
			if _, uerr := s.Exec(ctx, `UPDATE moderation_appeal_notifications
				SET error=?,attempts=?,retry_at=? WHERE appeal_id=? AND target_id=?`,
				errName, attempts,
				db.MysqlDatetime6(db.NowUTC().Add(appealNoticeBackoff(attempts))),
				appealID, targetID); uerr != nil {
				return uerr
			}
			if rowStr(notice, "error") != errName {
				if eerr := s.Event(ctx, appeal, "assistant", "notification_failed",
					rowStr(appeal, "status"),
					map[string]any{"target_id": targetID, "error": errName}); eerr != nil {
					return eerr
				}
			}
			h.log.Warningf("申诉通知发送失败 target=%d: %v", targetID, err)
			return nil
		}
		return s.Atomic(ctx, func(s *guard.Store) error {
			if err := s.SaveNotice(ctx, appeal, targetID, newID, kind); err != nil {
				return err
			}
			return s.Event(ctx, appeal, "assistant", "notification_sent",
				rowStr(appeal, "status"),
				map[string]any{
					"target_id":  targetID,
					"message_id": newID,
					"revision":   rowInt64(appeal, "revision"),
				})
		})
	})
}

// sendAlert 发送用户状态提醒（HTML + 刷新按钮，25s 超时）。
func (h *AppealHandler) sendAlert(ctx context.Context, chatID int64, text, appealID string) error {
	if h.tg == nil {
		return errors.New("telegram 客户端未装配")
	}
	_, err := h.tg.SendMessageWithTimeout(ctx, tg.AppealCardTimeout, &bot.SendMessageParams{
		ChatID:             chatID,
		Text:               text,
		ParseMode:          models.ParseModeHTML,
		LinkPreviewOptions: previewDisabled(),
		ReplyMarkup: &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{
			{uiButton("refresh", "ap:view:"+appealID)},
		}},
	})
	return err
}

// appealNoticeBackoff 等价 appeal_handler.py:379 的
// min(1800, 15 * 2 ** min(attempts, 7)) 秒退避。
func appealNoticeBackoff(attempts int64) time.Duration {
	shift := attempts
	if shift > 7 {
		shift = 7
	}
	seconds := int64(15) << shift
	if seconds > 1800 {
		seconds = 1800
	}
	return time.Duration(seconds) * time.Second
}

// errTypeName 等价 Python type(error).__name__（写入 error 列的诊断字段，
// 非用户可见文案；Go 类型名与 Python 类名不同属可接受差异）。
func errTypeName(err error) string {
	if err == nil {
		return ""
	}
	return reflect.TypeOf(err).String()
}

// resultItem 是审核回执投递项：text 为 HTML 文本；fileID 仅媒体项使用
// （kind ∈ photo/document/video，对齐 Python (kind, value) 二元组）。
type resultItem struct {
	kind   string
	text   string
	fileID string
}

// deliverResult 投递审核请求回执（appeal_handler.py:394-544 deliver_result
// 的逐行移植）：done 的 quick_review 动作原地同步审核卡片不重复回执；
// 失败回执刷新审核卡片；evidence/audit 分支逐项渲染；文本段按去标签
// 3500 字分组；delivery_cursor 断点续传；失败 60s 后重试。
func (h *AppealHandler) deliverResult(ctx context.Context, request guard.Row) error {
	appeal, err := h.repo.Get(ctx, rowStr(request, "appeal_id"))
	if err != nil {
		return err
	}
	if appeal == nil {
		// Python：appeal["user_id"] → TypeError → 由 deliver 外层捕获。
		return fmt.Errorf("申诉不存在 appeal=%s", rowStr(request, "appeal_id"))
	}
	return h.repo.WithLock(ctx, rowInt64(appeal, "user_id"), func(s *guard.Store) error {
		current, err := s.One(ctx, "SELECT * FROM moderation_appeal_requests WHERE id=?", rowStr(request, "id"))
		if err != nil {
			return err
		}
		if current == nil {
			return fmt.Errorf("审核请求不存在 request=%s", rowStr(request, "id"))
		}
		if pyTruthy(current["notified"]) {
			return nil
		}
		// 审核结果已经原地同步到审核卡片，不再发送重复成功回执
		//（appeal_handler.py:402-408）。
		if rowStr(current, "status") == "done" && reviewActions[rowStr(current, "action")] {
			_, err := s.Exec(ctx, "UPDATE moderation_appeal_requests SET notified=TRUE WHERE id=?", rowStr(current, "id"))
			return err
		}
		actorID := rowInt64(current, "actor_id")
		payload := asMap(current["payload"])
		if payload == nil {
			payload = map[string]any{}
		}

		items, err := h.buildResultItems(ctx, s, current, appeal, payload)
		if err != nil {
			return err
		}
		items = groupResultItems(items)

		cursor := pyInt(payload["delivery_cursor"])
		for index, item := range items {
			if int64(index) < cursor {
				continue
			}
			var sendErr error
			if item.kind == "text" {
				sendErr = h.sendResultText(ctx, actorID, item.text)
			} else {
				sendErr = h.sendMedia(ctx, actorID, item.kind, item.fileID)
			}
			if sendErr != nil {
				// 失败：写回 60s 重试；游标保持已推进的位置（断点续传）。
				if _, uerr := s.Exec(ctx,
					"UPDATE moderation_appeal_requests SET retry_at=? WHERE id=?",
					db.MysqlDatetime6(db.NowUTC().Add(appealResultRetrySeconds)),
					rowStr(current, "id")); uerr != nil {
					return uerr
				}
				h.log.Warningf("申诉审核回执发送失败 request=%s: %v", rowStr(current, "id"), sendErr)
				return nil
			}
			payload["delivery_cursor"] = index + 1
			if _, uerr := s.Exec(ctx,
				"UPDATE moderation_appeal_requests SET payload=? WHERE id=?",
				db.EncodeJSONMust(payload), rowStr(current, "id")); uerr != nil {
				return uerr
			}
		}
		_, err = s.Exec(ctx, "UPDATE moderation_appeal_requests SET notified=TRUE WHERE id=?", rowStr(current, "id"))
		return err
	})
}

// sendResultText 以 HTML 发送回执文本段（25s 超时，等价
// wait_for(bot.send_message(actor, value, parse_mode="HTML"), 25)）。
func (h *AppealHandler) sendResultText(ctx context.Context, chatID int64, text string) error {
	if h.tg == nil {
		return errors.New("telegram 客户端未装配")
	}
	_, err := h.tg.SendMessageWithTimeout(ctx, tg.AppealCardTimeout, &bot.SendMessageParams{
		ChatID:             chatID,
		Text:               text,
		ParseMode:          models.ParseModeHTML,
		LinkPreviewOptions: previewDisabled(),
	})
	return err
}

// sendMedia 等价 wait_for(getattr(bot, f"send_{kind}")(actor, value), 25)：
// 按 file_id 转发媒体；直连底层 Bot，补 25s 超时与 429（RetryAfter≤60s）
// 单次睡眠重试语义（对齐 tg 包统一行为）。
func (h *AppealHandler) sendMedia(ctx context.Context, chatID int64, kind, fileID string) error {
	if h.raw == nil {
		return errors.New("媒体发送客户端未装配")
	}
	call := func(ctx context.Context) error {
		callCtx, cancel := context.WithTimeout(ctx, tg.AppealCardTimeout)
		defer cancel()
		var err error
		switch kind {
		case "photo":
			_, err = h.raw.SendPhoto(callCtx, &bot.SendPhotoParams{
				ChatID: chatID, Photo: &models.InputFileString{Data: fileID}})
		case "document":
			_, err = h.raw.SendDocument(callCtx, &bot.SendDocumentParams{
				ChatID: chatID, Document: &models.InputFileString{Data: fileID}})
		case "video":
			_, err = h.raw.SendVideo(callCtx, &bot.SendVideoParams{
				ChatID: chatID, Video: &models.InputFileString{Data: fileID}})
		default:
			err = fmt.Errorf("未知回执媒体类型 %q", kind)
		}
		return err
	}
	err := call(ctx)
	var flood *bot.TooManyRequestsError
	if err != nil && errors.As(err, &flood) && flood.RetryAfter >= 0 && flood.RetryAfter <= 60 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(flood.RetryAfter)*time.Second + time.Second):
		}
		return call(ctx)
	}
	return err
}

// buildResultItems 构造回执项列表（appeal_handler.py:409-507 的分支移植）：
//   - error：失败文案（审核人卡片可刷新时原位刷新并保存，返回空列表）；
//   - evidence：源封禁证据快照 + 逐条材料（文本 + 附件媒体）；
//   - audit：审计明细逐条；
//   - 兜底：review_ack 状态确认。
func (h *AppealHandler) buildResultItems(ctx context.Context, s *guard.Store, current, appeal guard.Row, payload map[string]any) ([]resultItem, error) {
	actorID := rowInt64(current, "actor_id")
	appealID := rowStr(appeal, "id")

	if errKey := rowStr(current, "error"); errKey != "" {
		failure := i18n.T("appeal.request_failed", map[string]string{
			"error": html.EscapeString(i18n.T(errKey, nil)),
		})
		notices, err := s.Notices(ctx, appealID)
		if err != nil {
			return nil, err
		}
		var notice guard.Row
		for _, n := range notices {
			if rowStr(n, "kind") == "reviewer" &&
				rowInt64(n, "target_id") == actorID &&
				rowInt64(n, "target_id") == rowInt64(current, "origin_chat_id") &&
				rowInt64(n, "message_id") == rowInt64(current, "origin_message_id") {
				notice = n
				break
			}
		}
		if notice != nil {
			latest, err := s.Get(ctx, appealID)
			if err != nil {
				return nil, err
			}
			newID, err := h.editOrSend(ctx, actorID,
				uiCard(latest, true)+"\n\n"+failure, uiButtons(latest, true, false, ""),
				rowInt64(notice, "message_id"), true)
			if err != nil {
				return nil, err
			}
			if err := s.SaveNotice(ctx, latest, actorID, newID, "reviewer"); err != nil {
				return nil, err
			}
			return nil, nil
		}
		return []resultItem{{kind: "text", text: failure}}, nil
	}

	if evidence := asList(payload["evidence"]); evidence != nil {
		items := make([]resultItem, 0, len(evidence)+2)
		source := asMap(payload["source"])
		original := asMap(source["payload"])
		snapshot := asMap(original["evidence"])
		if len(source) > 0 {
			items = append(items, resultItem{kind: "text", text: i18n.T("appeal.source_evidence", map[string]string{
				"case_id": pyStr(source["id"]),
				"at":      html.EscapeString(pyStr(pyOr(payload["source_created_at"], "—"))),
				"reason":  html.EscapeString(truncRunes(pyStr(pyOr(snapshot["reason"], "—")), 300)),
				"rule":    html.EscapeString(truncRunes(pyStr(pyOr(snapshot["rule_id"], "—")), 100)),
				"original": html.EscapeString(truncRunes(
					pyStr(pyOr(snapshot["original"], "—")), 2048)),
				"link": html.EscapeString(truncRunes(
					pyStr(pyOr(pyOr(original["evidence_link"], ""), "—")), 300)),
			})})
			if pyTruthy(snapshot["normalized"]) {
				items = append(items, resultItem{kind: "text", text: i18n.T("appeal.source_normalized", map[string]string{
					"text": html.EscapeString(truncRunes(pyStr(snapshot["normalized"]), 2048)),
				})})
			}
		}
		for index, raw := range evidence {
			item := asMap(raw)
			itemText := i18n.T("appeal.evidence_item", map[string]string{
				"index": strconv.Itoa(index + 1),
				"at":    pyStr(item["at"]),
				"text":  html.EscapeString(pyStr(item["text"])),
			})
			items = append(items, resultItem{kind: "text", text: itemText})
			for _, rawMedia := range asList(item["files"]) {
				media := asMap(rawMedia)
				items = append(items, resultItem{kind: pyStr(media["type"]), fileID: pyStr(media["file_id"])})
			}
		}
		return items, nil
	}

	if audit := asList(payload["audit"]); audit != nil {
		items := make([]resultItem, 0, len(audit))
		for _, raw := range audit {
			item := asMap(raw)
			detail := asMap(item["detail"])
			items = append(items, resultItem{kind: "text", text: i18n.T("appeal.audit_item", map[string]string{
				"at":     pyStr(item["at"]),
				"actor":  html.EscapeString(pyStr(item["actor"])),
				"action": i18n.T("appeal.event_"+pyStr(item["action"]), nil),
				"status": i18n.T("appeal.status_"+pyStr(item["status"]), nil),
				"reason": html.EscapeString(truncRunes(
					pyStr(pyOr(pyOr(detail["reason"], detail["error"]), "—")), 1000)),
			})})
		}
		return items, nil
	}

	return []resultItem{{kind: "text", text: i18n.T("appeal.review_ack", map[string]string{
		"status": i18n.T("appeal.status_"+pyStr(pyOr(payload["status"], "pending")), nil),
	})}}, nil
}

// groupResultItems 等价 appeal_handler.py:508-516 的分组：相邻文本段合并，
// 合并后去标签并反转义长度 ≤3500 才收下，否则另起一段。
func groupResultItems(items []resultItem) []resultItem {
	grouped := make([]resultItem, 0, len(items))
	for _, item := range items {
		if item.kind == "text" && len(grouped) > 0 && grouped[len(grouped)-1].kind == "text" {
			combined := grouped[len(grouped)-1].text + "\n\n" + item.text
			if utf8.RuneCountInString(stripTagsUnescape(combined)) <= 3500 {
				grouped[len(grouped)-1].text = combined
				continue
			}
		}
		grouped = append(grouped, item)
	}
	return grouped
}

// stripTagsUnescape 等价 unescape(re.sub(r"<[^>]+>", "", s))：
// 去除 HTML 标签后反转义实体，用于分组长度估算。
func stripTagsUnescape(s string) string {
	return html.UnescapeString(tagRe.ReplaceAllString(s, ""))
}

// ─────────────────────────────────────────────────────────────────────
// 申诉侧纯函数（core/moderation/appeals.py:24-39 与 notification.py:32）。
// ─────────────────────────────────────────────────────────────────────

// sourceSignature 等价 appeals.py:24-31 source_signature：
// sha256(json.dumps([id, action, status, payload.review_generation 默认 0]))。
// Python json.dumps 默认分隔符为 ", " / ": "，逐字保留。
func sourceSignature(caseRow guard.Row) string {
	generation := 0
	if v, ok := asMap(caseRow["payload"])["review_generation"]; ok && v != nil {
		generation = int(pyInt(v))
	}
	raw := fmt.Sprintf(`["%s", "%s", "%s", %d]`,
		rowStr(caseRow, "id"), rowStr(caseRow, "action"), rowStr(caseRow, "status"), generation)
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// isBanCase 等价 notification.py:32-34 is_ban_case：
// action == ban 或 payload.review_ban_confirmed 真值。
func isBanCase(caseRow guard.Row) bool {
	return rowStr(caseRow, "action") == "ban" || pyTruthy(asMap(caseRow["payload"])["review_ban_confirmed"])
}

// eligibleCase 等价 appeals.py:34-39 eligible：
// 封禁类案件且状态处于可申诉窗口。
func eligibleCase(caseRow guard.Row) bool {
	if caseRow == nil {
		return false
	}
	switch rowStr(caseRow, "status") {
	case "pending", "blacklisted", "done":
	default:
		return false
	}
	return isBanCase(caseRow) || rowStr(caseRow, "status") == "blacklisted"
}

// rowStr 提取字符串列（nil 安全；DB 文本列已在 guard.All 统一为 string）。
func rowStr(row guard.Row, key string) string {
	if row == nil {
		return ""
	}
	return pyStr(row[key])
}

// rowInt64 提取整型列/JSON 数值（int64 驱动整型列与 float64 JSON 数字
// 统一转 int64）。
func rowInt64(row guard.Row, key string) int64 {
	if row == nil {
		return 0
	}
	return pyInt(row[key])
}
