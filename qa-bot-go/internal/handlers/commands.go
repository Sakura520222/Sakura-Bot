// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

// handlers 包的命令层：qa_bot.py:149-679 的 12 条命令、菜单按钮与
// AI 问答主路径（handle_message/_stream_and_edit 序列）的移植。
//
// 分发语义（对齐 PTB 组 0 内"首个匹配 handler 生效"）：
// 命令（bot_command 实体 offset=0）→ 6 主菜单文本 → 7 游离投稿按钮 →
// 普通文本（filters.TEXT & ~filters.COMMAND）→ AI 问答。
// 申诉层（组 -1）与投稿 FSM 入口由 main 在 Route 之前接线，本包不感知。
package handlers

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/contracts"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/engine"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/qauser"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/tg"
)

// TGClient 命令层对 Telegram 层的窄接口（消费者包内定义，生产实现 *tg.Client，
// 同时满足 tg.Editor 供 StreamAndEdit 消费；测试注入 fake）。
type TGClient interface {
	tg.Editor
	// SendMessage 发送消息（5s 超时 + 429 单次重试由 *tg.Client 统一封装）。
	SendMessage(ctx context.Context, params *bot.SendMessageParams) (*models.Message, error)
}

// StreamEngine 问答引擎窄接口（生产实现 *engine.Engine）。
type StreamEngine interface {
	ProcessQueryStream(ctx context.Context, query string, userID int64, channelHint string) <-chan engine.Chunk
}

// Sessions 会话管理窄接口（生产实现 *engine.Conversations；/status 与 /clear 消费）。
type Sessions interface {
	// GetSessionInfo 用户不在缓存或失败时返回 nil（Python None，内部自捕获）。
	GetSessionInfo(ctx context.Context, userID int64) *engine.SessionInfo
	// ClearUserHistory sessionID 传空串清除全部历史并驱逐缓存（Python session=None）。
	ClearUserHistory(ctx context.Context, userID int64, sessionID string) int64
}

// QuotaManager 配额管理窄接口（quota_manager.py 的 check_quota/refund_quota/
// get_usage_status；生产实现由 main 装配 db.Manager 组合）。Python 三个方法
// 内部自捕获异常从不向调用方抛错（check_quota 异常 → "⚠️ **系统错误**" 拒绝
// 结果），因此 Go 侧仅返回值、无 error。
type QuotaManager interface {
	// CheckQuota 检查并原子预留一次配额（qa_bot.py:655 调用点）。
	CheckQuota(ctx context.Context, userID int64) contracts.QuotaCheckResult
	// RefundQuota 回退一次配额占用（内部失败不扣费）。
	RefundQuota(ctx context.Context, userID int64) bool
	// GetUsageStatus 获取使用状态（不消耗配额；/status 消费）。
	GetUsageStatus(ctx context.Context, userID int64) UsageStatus
}

// UsageStatus /status 渲染所需的字段子集（对齐 get_usage_status 返回字典）。
type UsageStatus struct {
	// Err 非空等价 Python 返回 dict 含 "error" 键（/status 回"❌ 获取状态失败"）。
	Err string
	// IsAdmin 管理员分支（渲染 "🌟 管理员状态"）。
	IsAdmin bool
	// UsedToday 今日已用（Python "used_today"）。
	UsedToday int64
	// Remaining 剩余次数（Python "remaining"；管理员为 -1）。
	Remaining int64
	// TotalUsedToday 系统总使用（Python "total_used_today"，管理员分支渲染）。
	TotalUsedToday int64
}

// PersonaProvider 人格描述来源（生产实现 *config.Config 的 Persona，每次重读盘）。
type PersonaProvider interface {
	Persona() string
}

// SubmissionStates 投稿会话状态清理窄接口（生产实现为投稿 FSM 处理器，
// 对齐 submission_handler.clear_user_state；nil 时按"无状态可清"跳过）。
type SubmissionStates interface {
	ClearUserState(userID int64)
}

// CommandsDeps Commands 的装配依赖（除 log 外均不可为 nil；submissions 允许 nil）。
type CommandsDeps struct {
	// TG Telegram 客户端（发送/编辑）。
	TG TGClient
	// Users 用户系统（internal/qauser）。
	Users *qauser.System
	// Quota 配额管理器。
	Quota QuotaManager
	// Sessions 会话管理器。
	Sessions Sessions
	// Engine 问答引擎。
	Engine StreamEngine
	// Persona 人格来源（/view_persona 每次调用重读，对齐 get_qa_bot_persona）。
	Persona PersonaProvider
	// Submissions 投稿状态清理器（/cancel_submit 会话外路径；可为 nil）。
	Submissions SubmissionStates
	// Log 日志器；nil 时静默。
	Log tg.Logger
	// Now 单调时钟注入（测试用）；nil 时 time.Now。
	Now func() time.Time
}

// Commands 承载 QA Bot 命令层与 AI 问答主路径（qa_bot.py QABot 命令方法的移植）。
type Commands struct {
	tg          TGClient
	users       *qauser.System
	quota       QuotaManager
	sessions    Sessions
	engine      StreamEngine
	persona     PersonaProvider
	submissions SubmissionStates
	log         tg.Logger
	now         func() time.Time
}

// NewCommands 装配命令层。
func NewCommands(d CommandsDeps) *Commands {
	log := d.Log
	if log == nil {
		log = tg.NopLogger{}
	}
	now := d.Now
	if now == nil {
		now = time.Now
	}
	return &Commands{
		tg:          d.TG,
		users:       d.Users,
		quota:       d.Quota,
		sessions:    d.Sessions,
		engine:      d.Engine,
		persona:     d.Persona,
		submissions: d.Submissions,
		log:         log,
		now:         now,
	}
}

// ─────────────────────────────────────────────────────────────────────
// 用户可见文案（qa_bot.py 硬编码中文逐字拷贝，含空格/全角标点，禁止改动措辞）
// ─────────────────────────────────────────────────────────────────────

// welcomeMessage /start 欢迎语（qa_bot.py:163-177）。
const welcomeMessage = `🤖 **你好！我是你的智能助手。**

我可以陪你聊天、回答问题、协助编程和写作；
同时我接入了频道管理系统，可以按需查询频道的历史总结、消息记录和统计数据。

🌟 **你可以试着这样问我：**
• 日常对话："今天心情不太好，陪我聊聊"
• 知识与创作："帮我写一段 Python 脚本" / "用一句话解释什么是 RAG"
• 频道查询："最近频道里发生了什么新鲜事？"
• 指定频道："@channel 最近有什么更新？"

💡 **小提示：**
- 需要查频道资料时我会自动检索，不需要时直接回答，不用你操心
- 我会记住我们的对话上下文（30分钟内），可以用代词追问，比如"那它呢？""这个怎么样？"
- 发送 /status 可查看你的使用配额`

// helpText /help 帮助（qa_bot.py:187-226；缩进属于字符串内容，逐字保留）。
const helpText = `📚 <b>使用帮助</b>

    <b>基础命令</b>
    • <code>/start</code> - 查看欢迎信息
    • <code>/help</code> - 显示这份帮助文档
    • <code>/status</code> - 查看使用配额和会话状态
    • <code>/clear</code> - 清除对话记忆，重新开始
    • <code>/view_persona</code> - 查看当前助手人格设定
    • <code>/ask &lt;频道&gt; &lt;问题&gt;</code> - 限定指定频道查询

    <b>订阅管理</b>
    • <code>/listchannels</code> - 列出可订阅的频道
    • <code>/subscribe</code> - 订阅频道总结推送
    • <code>/unsubscribe</code> - 取消频道订阅
    • <code>/mysubscriptions</code> - 查看我的订阅列表
    • <code>/request_summary</code> - 请求生成频道总结

    <b>自然对话</b>
直接发送消息即可，例如：
• "你好，介绍一下你自己"
• "帮我写一段 Python 代码"
• "上周频道里发生了什么？"
• "统计学是什么？"

    <b>指定频道查询</b>
    • <code>/ask @channel_name 最近有什么更新？</code>
    • <code>/ask https://t.me/channel_name 分析一下 AI 讨论</code>
• 也可以直接说："在 channel_name 频道里查 AI"

    <b>多轮对话</b>
• 我会记住我们的对话上下文（30分钟内）
• 你可以使用代词追问："那它呢？"、"这个怎么样？"
• 对话超时后会自动开始新会话

    <b>功能特点</b>
✅ 通用 AI 助手：闲聊、知识问答、编程创作都可以
✅ 按需检索：涉及频道内容时自动查询历史总结与消息
✅ 上下文感知（多轮对话）
✅ 频道订阅推送
✅ 总结请求功能`

// subscribeUsage /subscribe 无参用法（qa_bot.py:372-380）。
const subscribeUsage = `📖 **订阅频道**

使用方法:
` + "`/subscribe <频道链接>`" + `

示例:
` + "`/subscribe https://t.me/channel_name`" + `

💡 使用 ` + "`/listchannels`" + ` 查看可订阅频道`

// requestSummaryUsage /request_summary 无参用法（qa_bot.py:472-479，HTML）。
const requestSummaryUsage = `📝 <b>请求生成总结</b>

使用方法:
<code>/request_summary &lt;频道链接&gt;</code>

此命令会向管理员提交请求，请管理员为指定频道生成总结。

💡 使用 <code>/listchannels</code> 查看可用的频道。`

// askUsage /ask 参数不足用法（qa_bot.py:586-595，HTML；不带主键盘）。
const askUsage = `🔎 <b>指定频道查询</b>

使用方法:
<code>/ask &lt;频道链接或@用户名&gt; &lt;问题&gt;</code>

示例:
<code>/ask @channel_name 最近有什么更新？</code>
<code>/ask https://t.me/channel_name 分析一下 AI 讨论</code>

💡 使用 <code>/listchannels</code> 查看可查询频道。`

// personaTruncateSuffix 人格超长截断后缀（qa_bot.py:316）。
const personaTruncateSuffix = "\n\n... (内容过长，已截断)"

// personaMaxShow 人格显示截断长度（Python len 语义 = Unicode 码点，硬规则 3）。
const personaMaxShow = 3500

// statusResetLine /status 尾部固定行（qa_bot.py:279）。
const statusResetLine = "\n\n📅 重置时间：每日 00:00 (UTC)"

// ─────────────────────────────────────────────────────────────────────
// 更新抽取与分发
// ─────────────────────────────────────────────────────────────────────

// effectiveUserMessage 抽取 update.effective_user / update.message
// （本包所有命令仅消费 Message 更新；callback 走申诉层）。
func effectiveUserMessage(upd *models.Update) (*models.User, *models.Message, bool) {
	if upd == nil || upd.Message == nil || upd.Message.From == nil {
		return nil, nil, false
	}
	return upd.Message.From, upd.Message, true
}

// optStr 空串映射为 nil（SQL NULL，对齐 Python username=None）。
func optStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// parseCommand 复刻 PTB CommandHandler 的参数语义：首个实体为 offset=0 的
// bot_command 才视为命令；命令名去掉 @botname 后缀并小写匹配；
// context.args = text.split()[1:]（Python str.split() 无参语义 → strings.Fields）。
func parseCommand(msg *models.Message) (cmd string, args []string, ok bool) {
	if len(msg.Entities) == 0 {
		return "", nil, false
	}
	e := msg.Entities[0]
	if e.Type != models.MessageEntityTypeBotCommand || e.Offset != 0 ||
		e.Length <= 0 || e.Length > len(msg.Text) {
		return "", nil, false
	}
	name := msg.Text[1:e.Length] // 命令为 ASCII，UTF-16 长度与字节一致
	if i := strings.IndexByte(name, '@'); i >= 0 {
		name = name[:i]
	}
	return strings.ToLower(name), strings.Fields(msg.Text)[1:], true
}

// Route 命令/菜单/游离按钮/普通文本的分发链（qa_bot.py:1010-1048 组 0 注册序；
// 申诉层与投稿 FSM 由 main 在此前接线，未匹配的 update 静默丢弃——与 PTB
// 无匹配 handler 时一致）。
func (c *Commands) Route(ctx context.Context, upd *models.Update) {
	defer func() {
		if r := recover(); r != nil {
			c.log.Errorf("处理更新失败: %T: %v", r, r)
		}
	}()

	_, msg, ok := effectiveUserMessage(upd)
	if !ok || msg.Text == "" {
		return
	}

	if cmd, _, isCmd := parseCommand(msg); isCmd {
		switch cmd {
		case "start":
			c.Start(ctx, upd)
		case "help":
			c.Help(ctx, upd)
		case "status":
			c.Status(ctx, upd)
		case "clear":
			c.Clear(ctx, upd)
		case "view_persona":
			c.ViewPersona(ctx, upd)
		case "ask":
			c.Ask(ctx, upd)
		case "listchannels":
			c.ListChannels(ctx, upd)
		case "subscribe":
			c.Subscribe(ctx, upd)
		case "unsubscribe":
			c.Unsubscribe(ctx, upd)
		case "mysubscriptions":
			c.MySubscriptions(ctx, upd)
		case "request_summary":
			c.RequestSummary(ctx, upd)
		case "cancel_submit":
			c.CancelSubmissionOutsideConversation(ctx, upd)
		}
		return
	}

	stripped := strings.TrimSpace(msg.Text)
	switch stripped {
	case tg.MenuAsk, tg.MenuChannels, tg.MenuSubscriptions, tg.MenuStatus, tg.MenuClear, tg.MenuHelp:
		c.HandleMenuButton(ctx, upd)
		return
	case tg.SubmitMenuCancel, tg.SubmitMenuSkipContent, tg.SubmitMenuSkipMedia,
		tg.SubmitMenuDoneMedia, tg.SubmitMenuConfirm, tg.SubmitMenuAnonymousYes, tg.SubmitMenuAnonymousNo:
		c.HandleStraySubmissionButton(ctx, upd)
		return
	}

	// filters.TEXT & ~filters.COMMAND → AI 问答。
	c.HandleMessage(ctx, upd)
}

// register 自动注册用户（/start、/listchannels、/subscribe、/request_summary、
// /ask、handle_message 共用；返回值 Python 未消费）。
func (c *Commands) register(ctx context.Context, u *models.User) {
	_ = c.users.RegisterUser(ctx, u.ID, optStr(u.Username), &u.FirstName)
}

// reply 发送回复（Python update.message.reply_text 等价；PTB v20+ 默认不引用
// 原消息，故不设 ReplyParameters）。失败记日志（Python 异常交 PTB 错误通道）。
func (c *Commands) reply(ctx context.Context, msg *models.Message, text string, parseMode models.ParseMode, keyboard *models.ReplyKeyboardMarkup) {
	params := &bot.SendMessageParams{
		ChatID:    msg.Chat.ID,
		Text:      text,
		ParseMode: parseMode,
	}
	// Python reply_markup=None → 请求体省略该字段；nil 键盘同样省略
	//（避免 *models.ReplyKeyboardMarkup 装入 interface 后的 typed-nil）。
	if keyboard != nil {
		params.ReplyMarkup = keyboard
	}
	if _, err := c.tg.SendMessage(ctx, params); err != nil {
		c.log.Errorf("发送消息失败: %s", err.Error())
	}
}

// replyMarkdown / replyHTML / replyPlain 常用快捷形态（主键盘随 Python 各调用点逐处对齐）。
func (c *Commands) replyMarkdown(ctx context.Context, msg *models.Message, text string) {
	c.reply(ctx, msg, text, models.ParseModeMarkdownV1, tg.BuildQAMainMenuKeyboard())
}

func (c *Commands) replyHTML(ctx context.Context, msg *models.Message, text string) {
	c.reply(ctx, msg, text, models.ParseModeHTML, tg.BuildQAMainMenuKeyboard())
}

func (c *Commands) replyPlain(ctx context.Context, msg *models.Message, text string) {
	c.reply(ctx, msg, text, "", nil)
}

// ─────────────────────────────────────────────────────────────────────
// 12 条命令（qa_bot.py:149-625 逐条对齐）
// ─────────────────────────────────────────────────────────────────────

// Start 处理/start命令（qa_bot.py:154-183）：自动注册 + Markdown 欢迎语 + 主键盘。
func (c *Commands) Start(ctx context.Context, upd *models.Update) {
	user, msg, ok := effectiveUserMessage(upd)
	if !ok {
		return
	}
	c.register(ctx, user)
	c.replyMarkdown(ctx, msg, welcomeMessage)
}

// Help 处理/help命令（qa_bot.py:185-232）：HTML 帮助 + 主键盘。
func (c *Commands) Help(ctx context.Context, upd *models.Update) {
	_, msg, ok := effectiveUserMessage(upd)
	if !ok {
		return
	}
	c.replyHTML(ctx, msg, helpText)
}

// Status 处理/status命令（qa_bot.py:234-286）：配额 + 会话状态，HTML；
// get_usage_status 失败（dict 含 "error" 键）→ "❌ 获取状态失败，请稍后再试。"。
func (c *Commands) Status(ctx context.Context, upd *models.Update) {
	user, msg, ok := effectiveUserMessage(upd)
	if !ok {
		return
	}
	statusInfo := c.quota.GetUsageStatus(ctx, user.ID)

	// 异步服务失败时返回可观测的错误提示，而不是渲染空数据（qa_bot.py:239-242）。
	if statusInfo.Err != "" {
		c.replyPlain(ctx, msg, "❌ 获取状态失败，请稍后再试。")
		return
	}

	// 构建配额状态文本（qa_bot.py:244-257）。
	var quotaText string
	if statusInfo.IsAdmin {
		quotaText = fmt.Sprintf("🌟 <b>管理员状态</b>\n\n你拥有无限制访问的特权。\n\n📊 今日总使用：%d次", statusInfo.TotalUsedToday)
	} else {
		quotaText = fmt.Sprintf("📊 <b>配额状态</b>\n\n• 今日已使用: %d 次\n• 剩余次数: %d 次", statusInfo.UsedToday, statusInfo.Remaining)
	}

	// 获取会话信息（qa_bot.py:259-273）。
	sessionText := ""
	if info := c.sessions.GetSessionInfo(ctx, user.ID); info != nil {
		statusEmoji := "⚪ 已超时"
		if info.IsActive {
			statusEmoji = "🟢 活跃中"
		}
		// 会话ID 前 8 字符（uuid 为 ASCII，切片安全）。
		preview := info.SessionID
		if len(preview) > 8 {
			preview = preview[:8]
		}
		sessionText = fmt.Sprintf("\n\n🧠 <b>当前会话状态</b>\n• 会话ID: <code>%s...</code>\n• 消息数: %d 条\n• 状态: %s",
			preview, info.MessageCount, statusEmoji)
	}

	message := fmt.Sprintf("📊 <b>系统状态</b>\n\n%s%s%s", quotaText, sessionText, statusResetLine)

	// 使用HTML模式以避免Markdown解析错误（qa_bot.py:281-286）。
	c.replyHTML(ctx, msg, message)
}

// Clear 处理/clear命令（qa_bot.py:288-305）：清除全部对话历史，Markdown。
func (c *Commands) Clear(ctx context.Context, upd *models.Update) {
	user, msg, ok := effectiveUserMessage(upd)
	if !ok {
		return
	}
	deletedCount := c.sessions.ClearUserHistory(ctx, user.ID, "")
	message := fmt.Sprintf("🗑️ **对话记忆已清除**\n\n已清除 **%d** 条对话记录。\n\n现在，我们的对话是全新的开始。有什么可以帮你的吗？", deletedCount)
	c.replyMarkdown(ctx, msg, message)
}

// ViewPersona 处理/view_persona命令（qa_bot.py:307-338）：显示人格，>3500 码点截断。
func (c *Commands) ViewPersona(ctx context.Context, upd *models.Update) {
	_, msg, ok := effectiveUserMessage(upd)
	if !ok {
		return
	}
	persona := c.persona.Persona()

	// 限制显示长度，避免消息过长（Python len = Unicode 码点 → 按 rune 计）。
	personaPreview := persona
	if utf8.RuneCountInString(persona) > personaMaxShow {
		personaPreview = string([]rune(persona)[:personaMaxShow]) + personaTruncateSuffix
	}

	message := "📋 **当前助手人格设定**\n\n```\n" + personaPreview +
		"\n```\n\n💡 **提示**：\n人格设定可通过以下方式修改：\n1. 修改 `data/qa_persona.txt` 文件\n" +
		"2. 在 `data/config.json` 中设置 `qa_bot_persona` 字段\n" +
		"3. 在 `.env` 文件中设置 `QA_BOT_PERSONA` 环境变量\n\n修改后需重启Bot生效。"

	c.replyMarkdown(ctx, msg, message)
}

// ListChannels 处理/listchannels命令（qa_bot.py:340-359）。
func (c *Commands) ListChannels(ctx context.Context, upd *models.Update) {
	user, msg, ok := effectiveUserMessage(upd)
	if !ok {
		return
	}
	c.register(ctx, user)
	channels := c.users.GetAvailableChannels(ctx)
	message := c.users.FormatChannelsList(channels)
	c.replyMarkdown(ctx, msg, message)
}

// Subscribe 处理/subscribe命令（qa_bot.py:361-408）：无参给用法；频道名从目录
// 精确匹配 channel_id，否则取 URL 尾段（channel_url.split("/")[-1]）。
func (c *Commands) Subscribe(ctx context.Context, upd *models.Update) {
	user, msg, ok := effectiveUserMessage(upd)
	if !ok {
		return
	}
	c.register(ctx, user)

	_, args, _ := parseCommand(msg)
	if len(args) < 1 {
		c.replyMarkdown(ctx, msg, subscribeUsage)
		return
	}

	channelURL := args[0]

	// 获取频道列表，查找频道名称（qa_bot.py:390-400）。
	channels := c.users.GetAvailableChannels(ctx)
	channelName := ""
	for _, ch := range channels {
		if ch.ChannelID == channelURL {
			channelName = ch.ChannelName
			break
		}
	}
	if channelName == "" {
		// 从URL中提取频道名作为备用（Python channel_url.split("/")[-1]）。
		channelName = lastPathSegment(channelURL)
	}

	result := c.users.AddSubscription(ctx, user.ID, channelURL, channelName)
	c.replyMarkdown(ctx, msg, result.Message)
}

// Unsubscribe 处理/unsubscribe命令（qa_bot.py:410-442）：无参列出订阅。
func (c *Commands) Unsubscribe(ctx context.Context, upd *models.Update) {
	user, msg, ok := effectiveUserMessage(upd)
	if !ok {
		return
	}

	_, args, _ := parseCommand(msg)
	if len(args) < 1 {
		// 如果没有参数，显示订阅列表让用户选择（qa_bot.py:416-427）。
		subs := c.users.GetUserSubscriptions(ctx, user.ID)
		var message string
		if len(subs) == 0 {
			message = "您还没有订阅任何频道。"
		} else {
			lines := []string{"📚 **取消订阅**\n\n请使用频道链接取消订阅：\n"}
			for _, sub := range subs {
				// sub.get('channel_name', sub.get('channel_id'))：键恒存在，
				// NULL 列 → Python None → 字面 "None"。
				name := "None"
				if sub.ChannelName.Valid {
					name = sub.ChannelName.String
				}
				lines = append(lines,
					fmt.Sprintf("• `%s`", name),
					fmt.Sprintf("  `%s`", sub.ChannelID),
					"",
				)
			}
			lines = append(lines, "使用方法: `/unsubscribe <频道链接>`")
			message = strings.Join(lines, "\n")
		}
		c.replyMarkdown(ctx, msg, message)
		return
	}

	channelURL := args[0]
	result := c.users.RemoveSubscription(ctx, user.ID, channelURL)
	c.replyMarkdown(ctx, msg, result.Message)
}

// MySubscriptions 处理/mysubscriptions命令（qa_bot.py:444-457）。
func (c *Commands) MySubscriptions(ctx context.Context, upd *models.Update) {
	user, msg, ok := effectiveUserMessage(upd)
	if !ok {
		return
	}
	subs := c.users.GetUserSubscriptions(ctx, user.ID)
	message := c.users.FormatSubscriptionsList(subs)
	c.replyMarkdown(ctx, msg, message)
}

// RequestSummary 处理/request_summary命令（qa_bot.py:459-506）：
// 写 request_queue 并回执 HTML（含 request_id）。
func (c *Commands) RequestSummary(ctx context.Context, upd *models.Update) {
	user, msg, ok := effectiveUserMessage(upd)
	if !ok {
		return
	}
	c.register(ctx, user)

	_, args, _ := parseCommand(msg)
	if len(args) < 1 {
		c.replyHTML(ctx, msg, requestSummaryUsage)
		return
	}

	channelURL := args[0]

	// 获取频道名称（qa_bot.py:489-498）。
	channels := c.users.GetAvailableChannels(ctx)
	channelName := ""
	for _, ch := range channels {
		if ch.ChannelID == channelURL {
			channelName = ch.ChannelName
			break
		}
	}
	if channelName == "" {
		channelName = lastPathSegment(channelURL)
	}

	result := c.users.CreateSummaryRequest(ctx, user.ID, channelURL, channelName)
	c.replyHTML(ctx, msg, result.Message)
}

// Ask 处理/ask命令（qa_bot.py:577-630）：限定指定频道查询。
// 参数<2 给用法（HTML，不带主键盘）；配额检查 → 占位 → 流式；失败退款。
func (c *Commands) Ask(ctx context.Context, upd *models.Update) {
	user, msg, ok := effectiveUserMessage(upd)
	if !ok {
		return
	}

	c.register(ctx, user)

	_, args, _ := parseCommand(msg)
	if len(args) < 2 {
		c.reply(ctx, msg, askUsage, models.ParseModeHTML, nil)
		return
	}

	channelHint := args[0]
	query := strings.TrimSpace(strings.Join(args[1:], " "))
	if query == "" {
		c.replyPlain(ctx, msg, "请提供要查询的问题。")
		return
	}

	// 配额检查（Python check_quota 内部自捕获异常，从不抛出）。
	quotaCheck := c.quota.CheckQuota(ctx, user.ID)
	if !quotaCheck.Allowed {
		// quota_check.get("message", "配额不足")：键恒存在。
		c.replyPlain(ctx, msg, quotaCheck.Message)
		return
	}

	c.runStream(ctx, msg, user.ID, query, channelHint,
		tg.PlaceholderThinkingChannel, quotaCheck, "处理指定频道查询失败")
}

// HandleMessage 处理用户消息（AI 问答主路径，qa_bot.py:632-684 的移植）。
func (c *Commands) HandleMessage(ctx context.Context, upd *models.Update) {
	// 防御性检查：忽略非用户消息（如频道事件、系统消息）。
	user, msg, ok := effectiveUserMessage(upd)
	if !ok {
		return
	}
	userID := user.ID
	query := msg.Text
	if query == "" || strings.TrimSpace(query) == "" {
		return
	}

	// 自动注册用户（确保数据库中有用户信息）。
	c.register(ctx, user)

	c.log.Infof("收到查询: user_id=%d, query=%s", userID, query)

	// 配额检查（Python check_quota 内部自捕获异常，从不抛出；
	// 外层 except 分支在 Go 侧不可达，见 QuotaManager 接口注释）。
	quotaCheck := c.quota.CheckQuota(ctx, userID)
	if !quotaCheck.Allowed {
		c.replyPlain(ctx, msg, quotaCheck.Message)
		return
	}

	c.runStream(ctx, msg, userID, query, "",
		tg.PlaceholderThinking, quotaCheck, "处理消息失败")
}

// runStream 占位 → 流式编辑 → 失败退款的公共序列
// （qa_bot.py:615-630 / 664-684 的 try/except 结构移植）：
//   - 正常完成但内部失败（success=False）→ 仅退款；
//   - 异常（Go 侧以 panic 模拟 Python 异常路径）→ 退款 + "❌ 抱歉…"（该回复
//     再包一层 try/except pass，失败静默）。
func (c *Commands) runStream(ctx context.Context, msg *models.Message, userID int64,
	query, channelHint, placeholderText string, quotaCheck contracts.QuotaCheckResult, errLogPrefix string) {

	var streamErr error
	func() {
		defer func() {
			if r := recover(); r != nil {
				streamErr = fmt.Errorf("%T: %v", r, r)
			}
		}()

		// 发送初始占位消息（纯文本，无 parse_mode）。
		placeholder, err := c.tg.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: msg.Chat.ID,
			Text:   placeholderText,
		})
		if err != nil {
			streamErr = err
			return
		}

		// 流式处理并实时编辑消息（_stream_and_edit 等价：复用 internal/tg 状态机）。
		chunks := adaptEngineChunks(ctx, c.engine.ProcessQueryStream(ctx, query, userID, channelHint))
		success := tg.StreamAndEdit(ctx, c.tg, c.log, c.now,
			tg.MessageRef{ChatID: placeholder.Chat.ID, MessageID: placeholder.ID},
			chunks,
			tg.QuotaCheck{IsAdmin: quotaCheck.IsAdmin, Remaining: int(quotaCheck.Remaining)})

		// 内部失败时回退本次配额占用（普通用户不为失败请求扣费）。
		if !success {
			c.quota.RefundQuota(ctx, userID)
		}
	}()

	if streamErr != nil {
		// 异常发生在配额预留之后，回退本次占用。
		c.log.Errorf("%s: %s", errLogPrefix, streamErr.Error())
		c.quota.RefundQuota(ctx, userID)
		// Python：错误回复自身再包 try/except pass（失败静默）。
		func() {
			defer func() { _ = recover() }()
			c.replyPlain(ctx, msg, tg.ErrQueryFailedText)
		}()
	}
}

// adaptEngineChunks 把引擎 Chunk 通道还原为 Python 字符串协议通道
// （Text → 原文；Ctrl → 控制标记全文；Err → "__ERROR__:"+文案），
// 供 tg.StreamAndEdit 消费。ctx 取消时停止投递（消费端自行合成中断块，
// 对齐 asyncio 取消语义），不泄漏 goroutine。
func adaptEngineChunks(ctx context.Context, src <-chan engine.Chunk) <-chan tg.StreamChunk {
	out := make(chan tg.StreamChunk)
	go func() {
		defer close(out)
		for chunk := range src {
			var text string
			switch chunk.Kind {
			case engine.ChunkText:
				text = chunk.Text
			case engine.ChunkCtrl:
				text = chunk.Ctrl
			default: // ChunkErr
				text = contracts.CtrlErrPrefix + chunk.Err
			}
			select {
			case out <- tg.StreamChunk{Text: text}:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out
}

// lastPathSegment 复刻 Python channel_url.split("/")[-1]。
func lastPathSegment(url string) string {
	if i := strings.LastIndex(url, "/"); i >= 0 {
		return url[i+1:]
	}
	return url
}
