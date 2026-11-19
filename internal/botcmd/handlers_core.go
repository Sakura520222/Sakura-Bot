// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package botcmd

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Sakura520222/Sakura-Bot/internal/config"
	"github.com/Sakura520222/Sakura-Bot/internal/db"
	"github.com/Sakura520222/Sakura-Bot/internal/i18n"
	"github.com/Sakura520222/Sakura-Bot/internal/logging"
	"github.com/Sakura520222/Sakura-Bot/internal/mtproto"
	"github.com/Sakura520222/Sakura-Bot/internal/process"
	"github.com/Sakura520222/Sakura-Bot/internal/summary"
)

// WikiCommandHandler 抽象 /wiki_* 斜杠命令处理器（由 internal/wiki 实现）。
type WikiCommandHandler interface {
	HandleWikiCommand(ctx context.Context, bot mtproto.Messenger, msg *mtproto.IncomingMessage, cmd string, arg string) error
}

// GuardCommandHandler 抽象 /guard* 斜杠命令处理器（由 internal/guard 实现）。
type GuardCommandHandler interface {
	HandleGuardCommand(ctx context.Context, bot mtproto.Messenger, msg *mtproto.IncomingMessage, cmd string, rawArgs string) error
}

// UserBotController 抽象 UserBot 管理命令与 WebUI 操作所需的接口（由 *mtproto.Client 适配器实现）。
type UserBotController interface {
	Status() mtproto.UserBotStatus
	JoinChannel(ctx context.Context, channelRef string) mtproto.JoinLeaveResult
	LeaveChannel(ctx context.Context, channelRef string) mtproto.JoinLeaveResult
	ListJoinedChannels(ctx context.Context) ([]mtproto.ChannelInfo, error)
}

// SchedulerController 抽象调度器暂停/恢复接口（由 *scheduler.Scheduler 实现）。
type SchedulerController interface {
	IsPaused() bool
	Pause()
	Resume()
}

// QAController 抽象助手 Bot 子进程控制器（由 *process.QAManager 实现）。
type QAController interface {
	Status() process.QAStatus
	Start() process.QAActionResult
	Stop() process.QAActionResult
	Restart() process.QAActionResult
	SetRuntime(rt string) map[string]any
}

// HistoryDB 抽象历史记录、统计、清空数据库与审计接口（由 *db.Manager 实现）。
type HistoryDB interface {
	GetSummariesSince(ctx context.Context, channelID string, since time.Time, limit int) ([]db.SummaryRecord, error)
	GetSummaryStats(ctx context.Context, channelID string, days int) (*db.SummaryStats, error)
	GetChannelRankingLimit(ctx context.Context, limit int) ([]db.ChannelRankRow, error)
	ExportSummariesForChannel(ctx context.Context, channelID string, limit int) ([]map[string]any, error)
	GetForwardingStats(ctx context.Context, days int) (map[string]any, error)
	ClearAllData(ctx context.Context) (map[string]int64, int64, error)
	RecordAuditLog(ctx context.Context, entry db.AuditEntry) error
}

// VectorClearer 抽象清空向量库接口（供 /db_clear 使用）。
type VectorClearer interface {
	ClearAllCollections(ctx context.Context) error
}

// Deps 注入主 Bot 命令路由器与 WebUI 命令中心所需的全部组件。
type Deps struct {
	Config                *config.Config
	BotClient             func() mtproto.Messenger
	UserBot               func() UserBotController
	SummaryService        *summary.Service
	TimeManager           *summary.TimeManager
	Scheduler             SchedulerController
	QAManager             QAController
	DB                    HistoryDB
	VectorClearer         VectorClearer
	WikiHandler           WikiCommandHandler
	GuardHandler          GuardCommandHandler
	TriggerRestart        func(reason string)
	TriggerReleaseRestart func(reason, executable, staticDir string)
	TriggerShutdown       func()
	WorkDir               string
	Logger                *slog.Logger
}

// Router 负责主 Bot 79 条命令、别名、对话式配置状态机、内联菜单及 WebUI 命令中心执行。
type Router struct {
	deps     Deps
	states   *UserStateStore
	updateMu sync.Mutex
	menuMu   sync.Mutex
	menuRuns map[menuActor]menuRun
}

// NewRouter 创建主 Bot 命令路由器。
func NewRouter(deps Deps) *Router {
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	return &Router{
		deps:     deps,
		states:   NewUserStateStore(),
		menuRuns: make(map[menuActor]menuRun),
	}
}

// States 返回内部状态机实例（方便测试核查）。
func (r *Router) States() *UserStateStore { return r.states }

// SetWikiHandler 允许在 WikiRuntime 初始化完成后延迟注入处理器。
func (r *Router) SetWikiHandler(h WikiCommandHandler) { r.deps.WikiHandler = h }

// SetGuardHandler 允许在 Sakura Guard 初始化完成后延迟注入处理器。
func (r *Router) SetGuardHandler(h GuardCommandHandler) { r.deps.GuardHandler = h }

// HandleMessage 统一分发主 Bot 收到的用户消息（优先处理活跃 FSM 输入，其次匹配斜杠命令及中文别名）。
func (r *Router) HandleMessage(ctx context.Context, msg *mtproto.IncomingMessage) bool {
	if msg == nil || msg.IsOut || msg.IsBroadcast {
		return false
	}
	bot := r.resolveBot()
	if bot == nil {
		return false
	}
	text := strings.TrimSpace(msg.Text)
	if text == "" {
		return false
	}

	if r.acceptMenuInput(ctx, bot, msg, text) {
		return true
	}

	if !strings.HasPrefix(text, "/") {
		return false
	}

	cmd, rawArgs := splitCommandAndArgs(text)
	canonical := canonicalCommand(cmd)
	if canonical == "" {
		return false
	}

	ctx = r.menuContext(ctx, msg.SenderID)
	if err := r.openMenuCommand(ctx, bot, msg, canonical, rawArgs); err != nil {
		r.deps.Logger.Warn("主 Bot 命令面板发送失败", "command", canonical, "chat", msg.ChatID, "error", err)
	}
	return true
}

func (r *Router) handleFSMInput(ctx context.Context, bot mtproto.Messenger, msg *mtproto.IncomingMessage, text string) bool {
	uid := msg.SenderID
	mode := r.states.Mode(uid)
	if mode == FSMNone {
		return false
	}

	switch mode {
	case FSMPrompt:
		lower := strings.ToLower(text)
		if lower == "/cancel" || strings.HasPrefix(lower, "/cancel@") {
			r.states.Cancel(uid)
			r.reply(ctx, bot, msg.ChatID, msg.ID, "✅ 已取消提示词设置")
			return true
		}
		if strings.HasPrefix(text, "/") {
			r.reply(ctx, bot, msg.ChatID, msg.ID, i18n.Text(ctx, "prompt.error_command", nil))
			return true
		}
		if r.states.ConsumePrompt(uid) && r.deps.Config != nil {
			if err := r.deps.Config.SavePrompt(config.PromptSummary, text); err != nil {
				r.reply(ctx, bot, msg.ChatID, msg.ID, "❌ 保存提示词失败: "+err.Error())
			} else {
				r.reply(ctx, bot, msg.ChatID, msg.ID, i18n.Text(ctx, "prompt.updated", nil))
			}
		}
		return true

	case FSMPollPrompt:
		lower := strings.ToLower(text)
		if lower == "/cancel" || strings.HasPrefix(lower, "/cancel@") {
			r.states.Cancel(uid)
			r.reply(ctx, bot, msg.ChatID, msg.ID, "✅ 已取消投票提示词设置")
			return true
		}
		if strings.HasPrefix(text, "/") {
			r.reply(ctx, bot, msg.ChatID, msg.ID, i18n.Text(ctx, "prompt.error_command", nil))
			return true
		}
		if r.states.ConsumePollPrompt(uid) && r.deps.Config != nil {
			if err := r.deps.Config.SavePrompt(config.PromptPoll, text); err != nil {
				r.reply(ctx, bot, msg.ChatID, msg.ID, "❌ 保存投票提示词失败: "+err.Error())
			} else {
				r.reply(ctx, bot, msg.ChatID, msg.ID, i18n.Text(ctx, "poll_prompt.updated", nil))
			}
		}
		return true

	case FSMAIConfig:
		stepRes := r.states.StepAIConfig(uid, text)
		if !stepRes.Handled {
			return false
		}
		if stepRes.Cancelled {
			r.reply(ctx, bot, msg.ChatID, msg.ID, i18n.Text(ctx, "aicfg.cancelled", nil))
			return true
		}
		if stepRes.OtherCommand {
			r.reply(ctx, bot, msg.ChatID, msg.ID, i18n.Text(ctx, "aicfg.in_progress", nil))
			return true
		}
		if !stepRes.Done {
			switch stepRes.NextStep {
			case "base_url":
				r.reply(ctx, bot, msg.ChatID, msg.ID, "请输入 AI Base URL。")
			case "model":
				r.reply(ctx, bot, msg.ChatID, msg.ID, "请输入 AI 模型名称。")
			}
			return true
		}
		if r.deps.Config != nil && len(stepRes.Updates) > 0 {
			if err := r.deps.Config.UpdateConfig(func(doc map[string]any) error {
				for k, v := range stepRes.Updates {
					doc[k] = v
				}
				return nil
			}); err != nil {
				r.reply(ctx, bot, msg.ChatID, msg.ID, "❌ 保存 AI 配置失败: "+err.Error())
				return true
			}
		}
		eff := r.deps.Config.EffectiveAIConfig("summary")
		masked := "***"
		if eff.APIKey == "" {
			masked = "(未设置)"
		}
		r.reply(ctx, bot, msg.ChatID, msg.ID, fmt.Sprintf("✅ AI 配置已更新\n• API Key: %s\n• Base URL: %s\n• Model: %s", masked, eff.BaseURL, eff.Model))
		return true
	}
	return false
}

func (r *Router) dispatchCanonical(
	ctx context.Context,
	bot mtproto.Messenger,
	msg *mtproto.IncomingMessage,
	canonical string,
	rawArgs string,
	replyTo int,
) {
	// 1. Wiki 命令委托（/wiki_status 带频道公开，其余管理员）
	if strings.HasPrefix(canonical, "wiki_") {
		if r.deps.WikiHandler == nil {
			r.reply(ctx, bot, msg.ChatID, replyTo, "⚠️ Sakura Wiki 模块未启用")
			return
		}
		_ = r.deps.WikiHandler.HandleWikiCommand(ctx, bot, msg, canonical, strings.TrimSpace(rawArgs))
		return
	}

	// 2. Guard 命令委托（群组管理员或 ADMIN_LIST 由 Guard 模块内部校验）
	if canonical == "guard" || strings.HasPrefix(canonical, "guard_") {
		if r.deps.GuardHandler == nil {
			r.reply(ctx, bot, msg.ChatID, replyTo, "⚠️ Sakura Guard 模块未启用")
			return
		}
		_ = r.deps.GuardHandler.HandleGuardCommand(ctx, bot, msg, canonical, rawArgs)
		return
	}

	// 4. 其余管理命令严格执行 fail-closed 管理员鉴权
	if !r.isAdmin(msg.SenderID) {
		r.reply(ctx, bot, msg.ChatID, replyTo, i18n.Text(ctx, "error.permission_denied", nil))
		return
	}

	switch canonical {
	case "summary":
		r.cmdManualSummary(ctx, bot, msg.ChatID, replyTo, rawArgs)
	case "showchannels":
		r.cmdShowChannels(ctx, bot, msg.ChatID, replyTo)
	case "addchannel":
		r.cmdAddChannel(ctx, bot, msg.ChatID, replyTo, rawArgs)
	case "deletechannel":
		r.cmdDeleteChannel(ctx, bot, msg.ChatID, replyTo, rawArgs)
	case "showchannelschedule":
		r.cmdShowSchedule(ctx, bot, msg.ChatID, replyTo, rawArgs)
	case "setchannelschedule":
		r.cmdSetSchedule(ctx, bot, msg.ChatID, replyTo, rawArgs)
	case "deletechannelschedule":
		r.cmdDeleteSchedule(ctx, bot, msg.ChatID, replyTo, rawArgs)
	case "clearsummarytime":
		r.cmdClearSummaryTime(ctx, bot, msg.ChatID, replyTo, rawArgs)
	case "setsendtosource":
		r.cmdSetSendToSource(ctx, bot, msg.ChatID, replyTo, rawArgs)
	case "showprompt":
		p, _ := r.deps.Config.GetPrompt(config.PromptSummary)
		_, _ = bot.SendLongMessage(ctx, msg.ChatID, "🧠 **当前总结提示词**\n\n"+p, mtproto.SendOptions{ParseMode: "markdown", NoWebpage: true})
	case "setprompt":
		r.states.StartPrompt(msg.SenderID)
		r.reply(ctx, bot, msg.ChatID, replyTo, i18n.Text(ctx, "prompt.enter_new", nil))
	case "showpollprompt":
		p, _ := r.deps.Config.GetPrompt(config.PromptPoll)
		_, _ = bot.SendLongMessage(ctx, msg.ChatID, "🗳 **当前投票提示词**\n\n"+p, mtproto.SendOptions{ParseMode: "markdown", NoWebpage: true})
	case "setpollprompt":
		r.states.StartPollPrompt(msg.SenderID)
		r.reply(ctx, bot, msg.ChatID, replyTo, i18n.Text(ctx, "poll_prompt.enter_new", nil))
	case "showaicfg":
		eff := r.deps.Config.EffectiveAIConfig("summary")
		preview := config.MaskLegacyAPIKeyPreview(eff.APIKey)
		if preview == "" {
			preview = "(未配置)"
		}
		r.reply(ctx, bot, msg.ChatID, replyTo, fmt.Sprintf("🔧 **当前 AI 配置**\n• API Key: `%s`\n• Base URL: `%s`\n• Model: `%s`", preview, eff.BaseURL, eff.Model))
	case "setaicfg":
		r.states.StartAIConfig(msg.SenderID)
		r.reply(ctx, bot, msg.ChatID, replyTo, "请输入 AI API Key。")
	case "channelpoll":
		r.cmdShowChannelPoll(ctx, bot, msg.ChatID, replyTo, rawArgs)
	case "setchannelpoll":
		r.cmdSetChannelPoll(ctx, bot, msg.ChatID, replyTo, rawArgs)
	case "deletechannelpoll":
		r.cmdDeleteChannelPoll(ctx, bot, msg.ChatID, replyTo, rawArgs)
	case "showcommentwelcome":
		r.cmdShowCommentWelcome(ctx, bot, msg.ChatID, replyTo, rawArgs)
	case "setcommentwelcome":
		r.cmdSetCommentWelcome(ctx, bot, msg.ChatID, replyTo, rawArgs)
	case "deletecommentwelcome":
		r.cmdDeleteCommentWelcome(ctx, bot, msg.ChatID, replyTo, rawArgs)
	case "showautopoll":
		r.cmdShowAutoPoll(ctx, bot, msg.ChatID, replyTo, rawArgs)
	case "setautopoll":
		r.cmdSetAutoPoll(ctx, bot, msg.ChatID, replyTo, rawArgs)
	case "deleteautopoll":
		r.cmdDeleteAutoPoll(ctx, bot, msg.ChatID, replyTo, rawArgs)
	default:
		r.dispatchOpsCommand(ctx, bot, msg, canonical, rawArgs, replyTo)
	}
}

func (r *Router) cmdManualSummary(ctx context.Context, bot mtproto.Messenger, chatID int64, replyTo int, rawArgs string) {
	if r.deps.SummaryService == nil {
		r.reply(ctx, bot, chatID, replyTo, "❌ 总结服务未初始化")
		return
	}
	var targets []string
	fields := strings.Fields(rawArgs)
	if len(fields) > 0 {
		for _, f := range fields {
			targets = append(targets, config.NormalizeChannelID(f))
		}
	} else if r.deps.Config != nil {
		targets = r.deps.Config.Channels()
	}
	if len(targets) == 0 {
		r.reply(ctx, bot, chatID, replyTo, i18n.Text(ctx, "summary.no_channels", nil))
		return
	}
	r.reply(ctx, bot, chatID, replyTo, fmt.Sprintf("⏳ 开始为 %d 个频道生成总结，请稍候...", len(targets)))
	var lines []string
	for _, ch := range targets {
		res := r.deps.SummaryService.GenerateChannelSummary(ctx, ch, false, "manual")
		if res.Success {
			lines = append(lines, fmt.Sprintf("✅ %s（%d 条消息）", res.ChannelName, res.MessageCount))
		} else {
			lines = append(lines, fmt.Sprintf("⚠️ %s：%s", ch, res.Error))
		}
	}
	r.reply(ctx, bot, chatID, 0, "📋 **手动总结执行完毕**\n"+strings.Join(lines, "\n"))
}

func (r *Router) cmdShowChannels(ctx context.Context, bot mtproto.Messenger, chatID int64, replyTo int) {
	channels := r.deps.Config.Channels()
	if len(channels) == 0 {
		r.reply(ctx, bot, chatID, replyTo, "📭 当前未配置任何监听频道")
		return
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("📺 **当前频道列表（共 %d 个）**\n\n", len(channels)))
	for i, ch := range channels {
		b.WriteString(fmt.Sprintf("%d. `%s`\n", i+1, ch))
	}
	r.reply(ctx, bot, chatID, replyTo, b.String())
}

func (r *Router) cmdAddChannel(ctx context.Context, bot mtproto.Messenger, chatID int64, replyTo int, rawArgs string) {
	args := strings.Fields(rawArgs)
	if len(args) == 0 {
		r.reply(ctx, bot, chatID, replyTo, "用法：`/addchannel <@channel 或 https://t.me/channel>`")
		return
	}
	var added, skipped []string
	err := r.deps.Config.UpdateConfig(func(doc map[string]any) error {
		cur := extractStringSlice(doc["channels"])
		for _, a := range args {
			norm := config.NormalizeChannelID(a)
			if containsChannel(cur, norm) {
				skipped = append(skipped, norm)
				continue
			}
			cur = append(cur, norm)
			added = append(added, norm)
		}
		doc["channels"] = cur
		return nil
	})
	if err != nil {
		r.reply(ctx, bot, chatID, replyTo, "❌ 添加频道失败: "+err.Error())
		return
	}
	var msg []string
	if len(added) > 0 {
		msg = append(msg, "✅ 已添加: "+strings.Join(added, ", "))
	}
	if len(skipped) > 0 {
		msg = append(msg, "ℹ️ 已存在: "+strings.Join(skipped, ", "))
	}
	r.reply(ctx, bot, chatID, replyTo, strings.Join(msg, "\n"))
}

func (r *Router) cmdDeleteChannel(ctx context.Context, bot mtproto.Messenger, chatID int64, replyTo int, rawArgs string) {
	args := strings.Fields(rawArgs)
	if len(args) == 0 {
		r.reply(ctx, bot, chatID, replyTo, "用法：`/deletechannel <@channel 或 https://t.me/channel>`")
		return
	}
	var removed, missing []string
	err := r.deps.Config.UpdateConfig(func(doc map[string]any) error {
		cur := extractStringSlice(doc["channels"])
		for _, a := range args {
			norm := config.NormalizeChannelID(a)
			next, ok := removeChannelEquivalent(cur, norm)
			if !ok {
				missing = append(missing, norm)
				continue
			}
			cur = next
			removed = append(removed, norm)
			for _, subKey := range []string{"summary_schedules", "channel_poll_settings", "channel_auto_poll_settings", "channel_comment_welcome"} {
				if sm, ok := doc[subKey].(map[string]any); ok {
					if k, found := config.FindEquivalentChannelKey(sm, norm); found {
						delete(sm, k)
					}
				}
			}
		}
		doc["channels"] = cur
		return nil
	})
	if err != nil {
		r.reply(ctx, bot, chatID, replyTo, "❌ 删除频道失败: "+err.Error())
		return
	}
	var msg []string
	if len(removed) > 0 {
		msg = append(msg, "🗑️ 已删除: "+strings.Join(removed, ", "))
	}
	if len(missing) > 0 {
		msg = append(msg, "⚠️ 未找到: "+strings.Join(missing, ", "))
	}
	r.reply(ctx, bot, chatID, replyTo, strings.Join(msg, "\n"))
}

func (r *Router) cmdShowSchedule(ctx context.Context, bot mtproto.Messenger, chatID int64, replyTo int, rawArgs string) {
	arg := strings.TrimSpace(rawArgs)
	if arg != "" {
		norm := config.NormalizeChannelID(arg)
		sc := r.deps.Config.ChannelSchedule(norm)
		r.reply(ctx, bot, chatID, replyTo, fmt.Sprintf("⏰ `%s`: %s %02d:%02d %v", norm, sc.Frequency, sc.Hour, sc.Minute, sc.Days))
		return
	}
	channels := r.deps.Config.Channels()
	if len(channels) == 0 {
		r.reply(ctx, bot, chatID, replyTo, "📭 当前未配置任何频道")
		return
	}
	var lines []string
	for _, ch := range channels {
		sc := r.deps.Config.ChannelSchedule(ch)
		lines = append(lines, fmt.Sprintf("• `%s`: %s %02d:%02d %v", ch, sc.Frequency, sc.Hour, sc.Minute, sc.Days))
	}
	r.reply(ctx, bot, chatID, replyTo, "⏰ **频道自动总结调度配置**\n\n"+strings.Join(lines, "\n"))
}

func (r *Router) cmdSetSchedule(ctx context.Context, bot mtproto.Messenger, chatID int64, replyTo int, rawArgs string) {
	parts := strings.Fields(rawArgs)
	if len(parts) < 3 {
		r.reply(ctx, bot, chatID, replyTo, "用法：\n`/setchannelschedule <channel> daily <HH:MM>`\n`/setchannelschedule <channel> weekly <mon,wed,...> <HH:MM>`")
		return
	}
	norm := config.NormalizeChannelID(parts[0])
	freq := strings.ToLower(parts[1])
	var days []string
	var hmRaw string
	switch freq {
	case "daily":
		hmRaw = parts[2]
	case "weekly":
		if len(parts) < 4 {
			r.reply(ctx, bot, chatID, replyTo, "weekly 模式需提供星期列表与 HH:MM")
			return
		}
		for _, d := range strings.Split(parts[2], ",") {
			if t := strings.ToLower(strings.TrimSpace(d)); t != "" {
				days = append(days, t)
			}
		}
		hmRaw = parts[3]
	default:
		r.reply(ctx, bot, chatID, replyTo, "❌ 频率必须是 daily 或 weekly")
		return
	}
	hour, minute, ok := parseHHMM(hmRaw)
	if !ok {
		r.reply(ctx, bot, chatID, replyTo, "❌ 时间格式无效，请使用 HH:MM（00:00 - 23:59）")
		return
	}
	sched := config.SummarySchedule{Frequency: freq, Hour: hour, Minute: minute, Days: days}
	if err := sched.Validate(); err != nil {
		r.reply(ctx, bot, chatID, replyTo, "❌ "+err.Error())
		return
	}
	err := r.deps.Config.UpdateConfig(func(doc map[string]any) error {
		sm, _ := doc["summary_schedules"].(map[string]any)
		if sm == nil {
			sm = map[string]any{}
		}
		entry := map[string]any{
			"frequency": sched.Frequency,
			"hour":      sched.Hour,
			"minute":    sched.Minute,
		}
		if len(sched.Days) > 0 {
			entry["days"] = sched.Days
		}
		sm[norm] = entry
		doc["summary_schedules"] = sm
		return nil
	})
	if err != nil {
		r.reply(ctx, bot, chatID, replyTo, "❌ 保存调度失败: "+err.Error())
		return
	}
	r.reply(ctx, bot, chatID, replyTo, fmt.Sprintf("✅ 已更新 `%s` 调度为 %s %02d:%02d", norm, freq, hour, minute))
}

func (r *Router) cmdDeleteSchedule(ctx context.Context, bot mtproto.Messenger, chatID int64, replyTo int, rawArgs string) {
	ch := strings.TrimSpace(rawArgs)
	if ch == "" {
		r.reply(ctx, bot, chatID, replyTo, "用法：`/deletechannelschedule <channel>`")
		return
	}
	norm := config.NormalizeChannelID(ch)
	_ = r.deps.Config.UpdateConfig(func(doc map[string]any) error {
		if sm, ok := doc["summary_schedules"].(map[string]any); ok {
			if k, found := config.FindEquivalentChannelKey(sm, norm); found {
				delete(sm, k)
			}
		}
		return nil
	})
	r.reply(ctx, bot, chatID, replyTo, fmt.Sprintf("✅ 已重置 `%s` 为默认调度", norm))
}

func (r *Router) cmdClearSummaryTime(ctx context.Context, bot mtproto.Messenger, chatID int64, replyTo int, rawArgs string) {
	if r.deps.TimeManager == nil {
		r.reply(ctx, bot, chatID, replyTo, "❌ 时间管理器未初始化")
		return
	}
	args := strings.Fields(rawArgs)
	if len(args) == 0 {
		n, err := r.deps.TimeManager.ClearAll()
		if err != nil {
			r.reply(ctx, bot, chatID, replyTo, "❌ 清除失败: "+err.Error())
			return
		}
		r.reply(ctx, bot, chatID, replyTo, fmt.Sprintf("✅ 已清除全部 %d 个频道的上次总结时间记录", n))
		return
	}
	removed := 0
	for _, a := range args {
		if ok, _ := r.deps.TimeManager.DeleteChannel(a); ok {
			removed++
		}
	}
	r.reply(ctx, bot, chatID, replyTo, fmt.Sprintf("✅ 已清除 %d 个频道的上次总结时间记录", removed))
}

func (r *Router) cmdSetSendToSource(ctx context.Context, bot mtproto.Messenger, chatID int64, replyTo int, rawArgs string) {
	arg := strings.TrimSpace(rawArgs)
	if arg == "" {
		cur := r.deps.Config.SendReportToSource()
		r.reply(ctx, bot, chatID, replyTo, fmt.Sprintf("📡 当前报告发送回源频道：`%v`", cur))
		return
	}
	val, ok := parseCmdBool(arg)
	if !ok {
		r.reply(ctx, bot, chatID, replyTo, "❌ 参数无效，请使用 true/false/on/off/开/关")
		return
	}
	_ = r.deps.Config.UpdateConfig(func(doc map[string]any) error {
		doc["send_report_to_source"] = val
		return nil
	})
	r.reply(ctx, bot, chatID, replyTo, fmt.Sprintf("✅ 已设置 send_report_to_source = `%v`", val))
}

func (r *Router) cmdShowChannelPoll(ctx context.Context, bot mtproto.Messenger, chatID int64, replyTo int, rawArgs string) {
	global := r.deps.Config.EnablePoll()
	settings := r.deps.Config.PollSettings()
	arg := strings.TrimSpace(rawArgs)
	if arg != "" {
		norm := config.NormalizeChannelID(arg)
		cfg := r.deps.Config.ChannelPollConfig(norm)
		r.reply(ctx, bot, chatID, replyTo, fmt.Sprintf("🗳 `%s` 投票配置: enabled=%v send_to_channel=%v（全局=%v）", norm, ptrBoolStr(cfg.Enabled), ptrBoolStr(cfg.SendToChannel), global))
		return
	}
	var lines []string
	lines = append(lines, fmt.Sprintf("🗳 **投票配置**（全局开关: `%v`）", global))
	for ch, s := range settings {
		lines = append(lines, fmt.Sprintf("• `%s`: enabled=%s send_to_channel=%s", ch, ptrBoolStr(s.Enabled), ptrBoolStr(s.SendToChannel)))
	}
	r.reply(ctx, bot, chatID, replyTo, strings.Join(lines, "\n"))
}

func (r *Router) cmdSetChannelPoll(ctx context.Context, bot mtproto.Messenger, chatID int64, replyTo int, rawArgs string) {
	parts := strings.Fields(rawArgs)
	if len(parts) < 2 {
		r.reply(ctx, bot, chatID, replyTo, "用法：`/setchannelpoll <channel> <on|off> [channel|discussion]`")
		return
	}
	norm := config.NormalizeChannelID(parts[0])
	enabled, ok := parseCmdBool(parts[1])
	if !ok {
		r.reply(ctx, bot, chatID, replyTo, "❌ 第二个参数请使用 on/off 或 true/false")
		return
	}
	sendToCh := false
	hasMode := false
	if len(parts) >= 3 {
		switch strings.ToLower(parts[2]) {
		case "channel":
			sendToCh = true
			hasMode = true
		case "discussion":
			sendToCh = false
			hasMode = true
		}
	}
	_ = r.deps.Config.UpdateConfig(func(doc map[string]any) error {
		pm, _ := doc["channel_poll_settings"].(map[string]any)
		if pm == nil {
			pm = map[string]any{}
		}
		entry, _ := pm[norm].(map[string]any)
		if entry == nil {
			entry = map[string]any{}
		}
		entry["enabled"] = enabled
		if hasMode {
			entry["send_to_channel"] = sendToCh
		}
		pm[norm] = entry
		doc["channel_poll_settings"] = pm
		return nil
	})
	r.reply(ctx, bot, chatID, replyTo, fmt.Sprintf("✅ 已更新 `%s` 投票配置", norm))
}

func (r *Router) cmdDeleteChannelPoll(ctx context.Context, bot mtproto.Messenger, chatID int64, replyTo int, rawArgs string) {
	ch := strings.TrimSpace(rawArgs)
	if ch == "" {
		r.reply(ctx, bot, chatID, replyTo, "用法：`/deletechannelpoll <channel>`")
		return
	}
	norm := config.NormalizeChannelID(ch)
	_ = r.deps.Config.UpdateConfig(func(doc map[string]any) error {
		if pm, ok := doc["channel_poll_settings"].(map[string]any); ok {
			if k, found := config.FindEquivalentChannelKey(pm, norm); found {
				delete(pm, k)
			}
		}
		return nil
	})
	r.reply(ctx, bot, chatID, replyTo, fmt.Sprintf("✅ 已删除 `%s` 独立投票配置", norm))
}

func (r *Router) cmdShowCommentWelcome(ctx context.Context, bot mtproto.Messenger, chatID int64, replyTo int, rawArgs string) {
	cw := r.deps.Config.CommentWelcomeSettings()
	arg := strings.TrimSpace(rawArgs)
	if arg != "" {
		norm := config.NormalizeChannelID(arg)
		eff := cw.GetForChannel(norm)
		r.reply(ctx, bot, chatID, replyTo, fmt.Sprintf("💬 `%s` 评论区欢迎: enabled=`%v` message=%q", norm, eff.Enabled, eff.WelcomeMessage))
		return
	}
	r.reply(ctx, bot, chatID, replyTo, fmt.Sprintf("💬 **评论区欢迎默认配置**\n• enabled: `%v`\n• 频道覆盖数: %d", cw.Default.Enabled, len(cw.ChannelOverrides)))
}

func (r *Router) cmdSetCommentWelcome(ctx context.Context, bot mtproto.Messenger, chatID int64, replyTo int, rawArgs string) {
	parts := strings.Fields(rawArgs)
	if len(parts) < 2 {
		r.reply(ctx, bot, chatID, replyTo, "用法：`/setcommentwelcome <channel> <on|off> [welcome_message]`")
		return
	}
	norm := config.NormalizeChannelID(parts[0])
	enabled, ok := parseCmdBool(parts[1])
	if !ok {
		r.reply(ctx, bot, chatID, replyTo, "❌ 请使用 on/off")
		return
	}
	customMsg := ""
	if len(parts) >= 3 {
		idx := strings.Index(rawArgs, parts[1])
		if idx >= 0 {
			customMsg = strings.TrimSpace(rawArgs[idx+len(parts[1]):])
		}
	}
	_ = r.deps.Config.UpdateConfig(func(doc map[string]any) error {
		m, _ := doc["channel_comment_welcome"].(map[string]any)
		if m == nil {
			m = map[string]any{}
		}
		entry, _ := m[norm].(map[string]any)
		if entry == nil {
			entry = map[string]any{}
		}
		entry["enabled"] = enabled
		if customMsg != "" {
			entry["welcome_message"] = customMsg
		}
		m[norm] = entry
		doc["channel_comment_welcome"] = m
		return nil
	})
	r.reply(ctx, bot, chatID, replyTo, fmt.Sprintf("✅ 已更新 `%s` 评论区欢迎配置", norm))
}

func (r *Router) cmdDeleteCommentWelcome(ctx context.Context, bot mtproto.Messenger, chatID int64, replyTo int, rawArgs string) {
	ch := strings.TrimSpace(rawArgs)
	if ch == "" {
		r.reply(ctx, bot, chatID, replyTo, "用法：`/deletecommentwelcome <channel>`")
		return
	}
	norm := config.NormalizeChannelID(ch)
	_ = r.deps.Config.UpdateConfig(func(doc map[string]any) error {
		if m, ok := doc["channel_comment_welcome"].(map[string]any); ok {
			if k, found := config.FindEquivalentChannelKey(m, norm); found {
				delete(m, k)
			}
		}
		return nil
	})
	r.reply(ctx, bot, chatID, replyTo, fmt.Sprintf("✅ 已删除 `%s` 评论区欢迎覆盖配置", norm))
}

func (r *Router) cmdShowAutoPoll(ctx context.Context, bot mtproto.Messenger, chatID int64, replyTo int, rawArgs string) {
	global := r.deps.Config.EnableAutoPoll()
	arg := strings.TrimSpace(rawArgs)
	if arg != "" {
		norm := config.NormalizeChannelID(arg)
		eff := r.deps.Config.IsAutoPollEnabledForChannel(norm)
		r.reply(ctx, bot, chatID, replyTo, fmt.Sprintf("🎲 `%s` 自动趣味投票生效状态: `%v`（全局开关: `%v`）", norm, eff, global))
		return
	}
	overrides := r.deps.Config.ChannelAutoPollSettings()
	r.reply(ctx, bot, chatID, replyTo, fmt.Sprintf("🎲 **自动趣味投票配置**\n• 全局开关: `%v`\n• 频道覆盖数: %d", global, len(overrides)))
}

func (r *Router) cmdSetAutoPoll(ctx context.Context, bot mtproto.Messenger, chatID int64, replyTo int, rawArgs string) {
	parts := strings.Fields(rawArgs)
	if len(parts) == 1 {
		if en, ok := parseCmdBool(parts[0]); ok {
			_ = r.deps.Config.UpdateConfig(func(doc map[string]any) error {
				doc["enable_auto_poll"] = en
				return nil
			})
			r.reply(ctx, bot, chatID, replyTo, fmt.Sprintf("✅ 已设置全局自动趣味投票 = `%v`", en))
			return
		}
	}
	if len(parts) < 2 {
		r.reply(ctx, bot, chatID, replyTo, "用法：`/setautopoll <on|off>` 或 `/setautopoll <channel> <on|off>`")
		return
	}
	norm := config.NormalizeChannelID(parts[0])
	enabled, ok := parseCmdBool(parts[1])
	if !ok {
		r.reply(ctx, bot, chatID, replyTo, "❌ 请使用 on/off")
		return
	}
	_ = r.deps.Config.UpdateConfig(func(doc map[string]any) error {
		if enabled {
			doc["enable_auto_poll"] = true
		}
		m, _ := doc["channel_auto_poll_settings"].(map[string]any)
		if m == nil {
			m = map[string]any{}
		}
		m[norm] = map[string]any{"enabled": enabled}
		doc["channel_auto_poll_settings"] = m
		return nil
	})
	r.reply(ctx, bot, chatID, replyTo, fmt.Sprintf("✅ 已更新 `%s` 自动趣味投票 = `%v`", norm, enabled))
}

func (r *Router) cmdDeleteAutoPoll(ctx context.Context, bot mtproto.Messenger, chatID int64, replyTo int, rawArgs string) {
	ch := strings.TrimSpace(rawArgs)
	if ch == "" {
		r.reply(ctx, bot, chatID, replyTo, "用法：`/deleteautopoll <channel>`")
		return
	}
	norm := config.NormalizeChannelID(ch)
	_ = r.deps.Config.UpdateConfig(func(doc map[string]any) error {
		if m, ok := doc["channel_auto_poll_settings"].(map[string]any); ok {
			if k, found := config.FindEquivalentChannelKey(m, norm); found {
				delete(m, k)
			}
		}
		return nil
	})
	r.reply(ctx, bot, chatID, replyTo, fmt.Sprintf("✅ 已删除 `%s` 自动趣味投票独立配置", norm))
}

func (r *Router) isAdmin(userID int64) bool {
	return r.deps.Config != nil && r.deps.Config.IsAdmin(userID)
}

func (r *Router) resolveBot() mtproto.Messenger {
	if r.deps.BotClient != nil {
		return r.deps.BotClient()
	}
	return nil
}

func (r *Router) reply(ctx context.Context, bot mtproto.Messenger, chatID int64, replyTo int, text string) {
	_, _ = bot.SendMessage(ctx, chatID, text, mtproto.SendOptions{
		ParseMode:    "markdown",
		ReplyToMsgID: replyTo,
		NoWebpage:    true,
	})
}

func splitCommandAndArgs(text string) (string, string) {
	trimmed := strings.TrimSpace(text)
	if idx := strings.IndexAny(trimmed, " \t\r\n"); idx >= 0 {
		cmd := trimmed[1:idx]
		args := strings.TrimSpace(trimmed[idx+1:])
		if at := strings.IndexByte(cmd, '@'); at >= 0 {
			cmd = cmd[:at]
		}
		return cmd, args
	}
	cmd := trimmed[1:]
	if at := strings.IndexByte(cmd, '@'); at >= 0 {
		cmd = cmd[:at]
	}
	return cmd, ""
}

var aliasMap = map[string]string{
	"开始":             "start",
	"帮助":             "help",
	"立即总结":           "summary",
	"show_channels":  "showchannels",
	"查看频道列表":         "showchannels",
	"add_channel":    "addchannel",
	"添加频道":           "addchannel",
	"delete_channel": "deletechannel",
	"删除频道":           "deletechannel",

	"show_channel_schedule":   "showchannelschedule",
	"查看频道时间配置":                "showchannelschedule",
	"set_channel_schedule":    "setchannelschedule",
	"设置频道时间配置":                "setchannelschedule",
	"delete_channel_schedule": "deletechannelschedule",
	"删除频道时间配置":                "deletechannelschedule",
	"clear_summary_time":      "clearsummarytime",
	"清除总结时间":                  "clearsummarytime",
	"set_send_to_source":      "setsendtosource",
	"设置报告发送回源频道":              "setsendtosource",

	"show_prompt":      "showprompt",
	"查看提示词":            "showprompt",
	"set_prompt":       "setprompt",
	"设置提示词":            "setprompt",
	"show_poll_prompt": "showpollprompt",
	"查看投票提示词":          "showpollprompt",
	"set_poll_prompt":  "setpollprompt",
	"设置投票提示词":          "setpollprompt",
	"show_aicfg":       "showaicfg",
	"查看ai配置":           "showaicfg",
	"set_aicfg":        "setaicfg",
	"设置ai配置":           "setaicfg",

	"channel_poll":           "channelpoll",
	"查看频道投票配置":               "channelpoll",
	"set_channel_poll":       "setchannelpoll",
	"设置频道投票配置":               "setchannelpoll",
	"delete_channel_poll":    "deletechannelpoll",
	"删除频道投票配置":               "deletechannelpoll",
	"show_comment_welcome":   "showcommentwelcome",
	"查看评论区欢迎":                "showcommentwelcome",
	"set_comment_welcome":    "setcommentwelcome",
	"设置评论区欢迎":                "setcommentwelcome",
	"delete_comment_welcome": "deletecommentwelcome",
	"删除评论区欢迎":                "deletecommentwelcome",
	"show_auto_poll":         "showautopoll",
	"查看自动投票":                 "showautopoll",
	"set_auto_poll":          "setautopoll",
	"设置自动投票":                 "setautopoll",
	"delete_auto_poll":       "deleteautopoll",
	"删除自动投票":                 "deleteautopoll",

	"历史":             "history",
	"导出":             "export",
	"统计":             "stats",
	"暂停":             "pause",
	"恢复":             "resume",
	"重启":             "restart",
	"关机":             "shutdown",
	"show_log_level": "showloglevel",
	"查看日志级别":         "showloglevel",
	"set_log_level":  "setloglevel",
	"设置日志级别":         "setloglevel",
	"clear_cache":    "clearcache",
	"清除缓存":           "clearcache",
	"dbclear":        "db_clear",
	"更新日志":           "changelog",
	"更新":             "update",

	"qa_状态":  "qa_status",
	"qa_启动":  "qa_start",
	"qa_停止":  "qa_stop",
	"qa_重启":  "qa_restart",
	"qa_统计":  "qa_stats",
	"qa_运行时": "qa_runtime",
	"语言":     "language",

	"转发状态":   "forwarding_status_only",
	"启用转发":   "forwarding_enable",
	"禁用转发":   "forwarding_disable",
	"转发统计":   "forwarding_stats",
	"添加转发规则": "forwarding_add_rule",
	"删除转发规则": "forwarding_remove_rule",
	"关键词白名单": "forwarding_keywords",
	"关键词黑名单": "forwarding_blacklist",
	"正则白名单":  "forwarding_patterns",
	"正则黑名单":  "forwarding_blacklist_patterns",
	"复制模式":   "forwarding_copy_mode",
	"只转发原创":  "forwarding_original_only",
	"规则详情":   "forwarding_rule_info",
	"转发底栏":   "forwarding_footer",
	"默认底栏":   "forwarding_default_footer",
	"转发帮助":   "forwarding_help",

	"userbot_状态": "userbot_status",
	"userbot_加入": "userbot_join",
	"userbot_离开": "userbot_leave",
	"userbot_列表": "userbot_list",
}

func canonicalCommand(raw string) string {
	lower := strings.ToLower(strings.TrimSpace(raw))
	if mapped, ok := aliasMap[lower]; ok {
		return mapped
	}
	for _, g := range BotCommandGroups {
		for _, c := range g.Commands {
			if c.Command == lower {
				return lower
			}
		}
	}
	return ""
}

func parseHHMM(s string) (int, int, bool) {
	hStr, mStr, ok := strings.Cut(strings.TrimSpace(s), ":")
	if !ok {
		return 0, 0, false
	}
	h, err1 := strconv.Atoi(hStr)
	m, err2 := strconv.Atoi(mStr)
	if err1 != nil || err2 != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, 0, false
	}
	return h, m, true
}

func parseCmdBool(s string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "1", "yes", "on", "开", "启用":
		return true, true
	case "false", "0", "no", "off", "关", "禁用":
		return false, true
	default:
		return false, false
	}
}

func ptrBoolStr(b *bool) string {
	if b == nil {
		return "继承全局"
	}
	if *b {
		return "true"
	}
	return "false"
}

func extractStringSlice(v any) []string {
	switch arr := v.(type) {
	case []string:
		return append([]string(nil), arr...)
	case []any:
		out := make([]string, 0, len(arr))
		for _, item := range arr {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
		return out
	default:
		return []string{}
	}
}

func containsChannel(list []string, target string) bool {
	tk := config.ComparableChannelKey(target)
	for _, c := range list {
		if config.ComparableChannelKey(c) == tk {
			return true
		}
	}
	return false
}

func removeChannelEquivalent(list []string, target string) ([]string, bool) {
	tk := config.ComparableChannelKey(target)
	out := make([]string, 0, len(list))
	removed := false
	for _, c := range list {
		if config.ComparableChannelKey(c) == tk {
			removed = true
			continue
		}
		out = append(out, c)
	}
	return out, removed
}

func ensureLogLevelValid(level string) bool {
	switch strings.ToUpper(strings.TrimSpace(level)) {
	case "DEBUG", "INFO", "WARNING", "ERROR", "CRITICAL":
		return true
	default:
		return false
	}
}

var _ = logging.GetDefaultLevel
