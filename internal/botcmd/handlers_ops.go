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
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Sakura520222/Sakura-Bot/internal/config"
	"github.com/Sakura520222/Sakura-Bot/internal/i18n"
	"github.com/Sakura520222/Sakura-Bot/internal/logging"
	"github.com/Sakura520222/Sakura-Bot/internal/mtproto"
)

func (r *Router) dispatchOpsCommand(
	ctx context.Context,
	bot mtproto.Messenger,
	msg *mtproto.IncomingMessage,
	canonical string,
	rawArgs string,
	replyTo int,
) {
	chatID := msg.ChatID
	switch canonical {
	case "history":
		r.cmdHistory(ctx, bot, chatID, replyTo, rawArgs)
	case "export":
		r.cmdExport(ctx, bot, chatID, replyTo, rawArgs)
	case "stats":
		r.cmdStats(ctx, bot, chatID, replyTo, rawArgs)

	case "pause":
		if r.deps.Scheduler == nil {
			r.reply(ctx, bot, chatID, replyTo, "⚠️ 调度器未初始化")
			return
		}
		if r.deps.Scheduler.IsPaused() {
			r.reply(ctx, bot, chatID, replyTo, "ℹ️ 定时任务已处于暂停状态")
			return
		}
		r.deps.Scheduler.Pause()
		r.reply(ctx, bot, chatID, replyTo, "⏸️ 已暂停所有定时总结任务")

	case "resume":
		if r.deps.Scheduler == nil {
			r.reply(ctx, bot, chatID, replyTo, "⚠️ 调度器未初始化")
			return
		}
		if !r.deps.Scheduler.IsPaused() {
			r.reply(ctx, bot, chatID, replyTo, "ℹ️ 定时任务正在运行中")
			return
		}
		r.deps.Scheduler.Resume()
		r.reply(ctx, bot, chatID, replyTo, "▶️ 已恢复所有定时总结任务")

	case "restart":
		r.reply(ctx, bot, chatID, replyTo, "🔄 正在重启机器人...")
		if r.deps.TriggerRestart != nil {
			r.deps.TriggerRestart("telegram_command_restart")
		}

	case "shutdown":
		r.reply(ctx, bot, chatID, replyTo, "🛑 正在停止机器人...")
		if r.deps.TriggerShutdown != nil {
			r.deps.TriggerShutdown()
		}

	case "showloglevel":
		r.reply(ctx, bot, chatID, replyTo, fmt.Sprintf("📋 当前日志级别: `%s`", logging.GetDefaultLevel()))

	case "setloglevel":
		lvl := strings.ToUpper(strings.TrimSpace(rawArgs))
		if !ensureLogLevelValid(lvl) {
			r.reply(ctx, bot, chatID, replyTo, "用法：`/setloglevel <DEBUG|INFO|WARNING|ERROR|CRITICAL>`")
			return
		}
		_ = logging.SetDefaultLevel(lvl)
		_ = r.deps.Config.UpdateConfig(func(doc map[string]any) error {
			doc["log_level"] = lvl
			return nil
		})
		r.reply(ctx, bot, chatID, replyTo, fmt.Sprintf("✅ 日志级别已修改为 `%s`", lvl))

	case "clearcache":
		ch := strings.TrimSpace(rawArgs)
		cleared := bot.ClearLinkedChatCache(ch)
		if ch != "" {
			r.reply(ctx, bot, chatID, replyTo, fmt.Sprintf("🧹 已清除频道 `%s` 讨论组缓存（%d 条）", ch, cleared))
		} else {
			r.reply(ctx, bot, chatID, replyTo, fmt.Sprintf("🧹 已清除全部讨论组缓存（共 %d 条）", cleared))
		}

	case "db_clear":
		if strings.TrimSpace(rawArgs) != "CONFIRM" {
			r.reply(ctx, bot, chatID, replyTo, "⚠️ **危险操作警告**\n此操作将清空所有历史总结、订阅、对话与转发数据！\n如确认执行，请发送：`/db_clear CONFIRM`")
			return
		}
		if r.deps.DB == nil {
			r.reply(ctx, bot, chatID, replyTo, "❌ 数据库未连接")
			return
		}
		_, totalDeleted, err := r.deps.DB.ClearAllData(ctx)
		if r.deps.VectorClearer != nil {
			_ = r.deps.VectorClearer.ClearAllCollections(ctx)
		}
		if r.deps.TimeManager != nil {
			_, _ = r.deps.TimeManager.ClearAll()
		}
		if err != nil {
			r.reply(ctx, bot, chatID, replyTo, fmt.Sprintf("⚠️ 数据库部分清空（共删除 %d 行），存在错误: %v", totalDeleted, err))
			return
		}
		r.reply(ctx, bot, chatID, replyTo, fmt.Sprintf("✅ 数据库、向量存储及总结时间已全部清空（共删除 %d 行）", totalDeleted))

	case "changelog":
		r.cmdChangelog(ctx, bot, chatID, replyTo, rawArgs)

	case "update":
		r.cmdUpdate(ctx, bot, chatID, replyTo, rawArgs)

	case "qa_status", "qa_stats":
		if r.deps.QAManager == nil {
			r.reply(ctx, bot, chatID, replyTo, "⚠️ QA 进程管理器未初始化")
			return
		}
		st := r.deps.QAManager.Status()
		stateIcon := "⏹ 未运行"
		if st.Running {
			stateIcon = "🟢 运行中"
		}
		pidStr := "-"
		if st.PID != nil {
			pidStr = strconv.Itoa(*st.PID)
		}
		upStr := "-"
		if st.UptimeSeconds != nil {
			upStr = fmt.Sprintf("%.0fs", *st.UptimeSeconds)
		}
		r.reply(ctx, bot, chatID, replyTo, fmt.Sprintf(
			"🤖 **QA Bot 状态**\n• 状态: %s\n• PID: `%s`\n• 运行时长: `%s`\n• 运行时: `%s`（配置: `%s`）\n• 已启用: `%v`",
			stateIcon, pidStr, upStr, st.Runtime, st.ConfiguredRuntime, st.Enabled,
		))

	case "qa_start":
		if r.deps.QAManager == nil {
			r.reply(ctx, bot, chatID, replyTo, "⚠️ QA 进程管理器未初始化")
			return
		}
		res := r.deps.QAManager.Start()
		r.reply(ctx, bot, chatID, replyTo, res.Message)

	case "qa_stop":
		if r.deps.QAManager == nil {
			r.reply(ctx, bot, chatID, replyTo, "⚠️ QA 进程管理器未初始化")
			return
		}
		res := r.deps.QAManager.Stop()
		r.reply(ctx, bot, chatID, replyTo, res.Message)

	case "qa_restart":
		if r.deps.QAManager == nil {
			r.reply(ctx, bot, chatID, replyTo, "⚠️ QA 进程管理器未初始化")
			return
		}
		res := r.deps.QAManager.Restart()
		r.reply(ctx, bot, chatID, replyTo, res.Message)

	case "qa_runtime":
		if r.deps.QAManager == nil {
			r.reply(ctx, bot, chatID, replyTo, "⚠️ QA 进程管理器未初始化")
			return
		}
		arg := strings.TrimSpace(rawArgs)
		if arg == "" {
			st := r.deps.QAManager.Status()
			r.reply(ctx, bot, chatID, replyTo, fmt.Sprintf("⚙️ 当前配置运行时: `%s`（实际运行: `%s`）", st.ConfiguredRuntime, st.Runtime))
			return
		}
		res := r.deps.QAManager.SetRuntime(arg)
		msgText, _ := res["message"].(string)
		r.reply(ctx, bot, chatID, replyTo, msgText)

	case "forwarding", "forwarding_status_only":
		if canonical == "forwarding" && strings.TrimSpace(rawArgs) != "" {
			r.cmdForwardingAddRule(ctx, bot, chatID, replyTo, rawArgs, true)
		} else {
			r.cmdForwardingStatus(ctx, bot, chatID, replyTo)
		}
	case "forwarding_enable":
		r.cmdForwardingToggle(ctx, bot, chatID, replyTo, true)
	case "forwarding_disable":
		r.cmdForwardingToggle(ctx, bot, chatID, replyTo, false)
	case "forwarding_stats":
		r.cmdForwardingStats(ctx, bot, chatID, replyTo)
	case "forwarding_add_rule":
		r.cmdForwardingAddRule(ctx, bot, chatID, replyTo, rawArgs, false)
	case "forwarding_remove_rule":
		r.cmdForwardingRemoveRule(ctx, bot, chatID, replyTo, rawArgs)
	case "forwarding_keywords":
		r.cmdForwardingListField(ctx, bot, chatID, replyTo, rawArgs, "keywords", ",")
	case "forwarding_blacklist":
		r.cmdForwardingListField(ctx, bot, chatID, replyTo, rawArgs, "blacklist", ",")
	case "forwarding_patterns":
		r.cmdForwardingListField(ctx, bot, chatID, replyTo, rawArgs, "patterns", ";;")
	case "forwarding_blacklist_patterns":
		r.cmdForwardingListField(ctx, bot, chatID, replyTo, rawArgs, "blacklist_patterns", ";;")
	case "forwarding_copy_mode":
		r.cmdForwardingBoolField(ctx, bot, chatID, replyTo, rawArgs, "copy_mode")
	case "forwarding_original_only":
		r.cmdForwardingBoolField(ctx, bot, chatID, replyTo, rawArgs, "forward_original_only")
	case "forwarding_rule_info":
		r.cmdForwardingRuleInfo(ctx, bot, chatID, replyTo, rawArgs)
	case "forwarding_footer":
		r.cmdForwardingFooter(ctx, bot, chatID, replyTo, rawArgs)
	case "forwarding_default_footer":
		r.cmdForwardingDefaultFooter(ctx, bot, chatID, replyTo, rawArgs)
	case "forwarding_help":
		r.reply(ctx, bot, chatID, replyTo, forwardingHelpText)

	case "userbot_status":
		r.cmdUserBotStatus(ctx, bot, chatID, replyTo)
	case "userbot_join":
		r.cmdUserBotJoin(ctx, bot, chatID, replyTo, rawArgs)
	case "userbot_leave":
		r.cmdUserBotLeave(ctx, bot, chatID, replyTo, rawArgs)
	case "userbot_list":
		r.cmdUserBotList(ctx, bot, chatID, replyTo)
	}
}

func (r *Router) cmdHistory(ctx context.Context, bot mtproto.Messenger, chatID int64, replyTo int, rawArgs string) {
	if r.deps.DB == nil {
		r.reply(ctx, bot, chatID, replyTo, "❌ 数据库未连接")
		return
	}
	parts := strings.Fields(rawArgs)
	channelID := ""
	var since time.Time
	if len(parts) >= 1 {
		channelID = config.NormalizeChannelID(parts[0])
		if !containsChannel(r.deps.Config.Channels(), channelID) {
			r.reply(ctx, bot, chatID, replyTo, i18n.T("error.channel_not_found", map[string]string{"channel": channelID}))
			return
		}
	}
	if len(parts) >= 2 {
		days, err := strconv.Atoi(parts[1])
		if err != nil || days <= 0 {
			r.reply(ctx, bot, chatID, replyTo, i18n.T("history.days_invalid", nil))
			return
		}
		since = time.Now().UTC().Add(-time.Duration(days) * 24 * time.Hour)
	}

	records, err := r.deps.DB.GetSummariesSince(ctx, channelID, since, 10)
	if err != nil {
		r.reply(ctx, bot, chatID, replyTo, "❌ 查询历史记录失败: "+err.Error())
		return
	}
	if len(records) == 0 {
		r.reply(ctx, bot, chatID, replyTo, "📭 暂无历史总结记录")
		return
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("📋 **历史总结记录（最近 %d 条）**\n\n", len(records)))
	for _, rec := range records {
		preview := strings.ReplaceAll(truncateRunesStr(rec.SummaryText, 150), "\n", " ")
		b.WriteString(fmt.Sprintf(
			"🔹 **%s** (%s) — %s\n   📊 处理消息: %d 条\n   💬 %s\n\n",
			rec.CreatedAt.UTC().Format("2006-01-02 15:04"),
			rec.SummaryType,
			rec.ChannelName,
			rec.MessageCount,
			preview,
		))
	}
	_, _ = bot.SendLongMessage(ctx, chatID, b.String(), mtproto.SendOptions{ParseMode: "markdown", NoWebpage: true})
}

func (r *Router) cmdExport(ctx context.Context, bot mtproto.Messenger, chatID int64, replyTo int, rawArgs string) {
	if r.deps.DB == nil {
		r.reply(ctx, bot, chatID, replyTo, "❌ 数据库未连接")
		return
	}
	parts := strings.Fields(rawArgs)
	format := "json"
	channelID := ""
	for _, p := range parts {
		lp := strings.ToLower(p)
		if lp == "json" || lp == "csv" || lp == "md" {
			format = lp
		} else if channelID == "" {
			channelID = config.NormalizeChannelID(p)
		}
	}
	if channelID != "" && !containsChannel(r.deps.Config.Channels(), channelID) {
		r.reply(ctx, bot, chatID, replyTo, i18n.T("error.channel_not_found", map[string]string{"channel": channelID}))
		return
	}

	rows, err := r.deps.DB.ExportSummariesForChannel(ctx, channelID, 10000)
	if err != nil {
		r.reply(ctx, bot, chatID, replyTo, "❌ 导出失败: "+err.Error())
		return
	}
	if len(rows) == 0 {
		r.reply(ctx, bot, chatID, replyTo, "📭 暂无可导出的历史总结数据")
		return
	}

	fileBytes, fileName := formatExportPayload(rows, format)
	_, err = bot.SendMedia(ctx, chatID, mtproto.MediaUpload{
		Data:          fileBytes,
		FileName:      fileName,
		ForceDocument: true,
		Caption:       fmt.Sprintf("📦 历史总结导出完成（格式: %s，共 %d 条）", strings.ToUpper(format), len(rows)),
	}, mtproto.SendOptions{ReplyToMsgID: replyTo})
	if err != nil {
		r.reply(ctx, bot, chatID, replyTo, "❌ 发送导出文件失败: "+err.Error())
	}
}

func (r *Router) cmdStats(ctx context.Context, bot mtproto.Messenger, chatID int64, replyTo int, rawArgs string) {
	if r.deps.DB == nil {
		r.reply(ctx, bot, chatID, replyTo, "❌ 数据库未连接")
		return
	}
	parts := strings.Fields(rawArgs)
	channelID := ""
	days := 0
	if len(parts) >= 1 {
		if d, err := strconv.Atoi(parts[0]); err == nil && d > 0 {
			days = d
		} else {
			channelID = config.NormalizeChannelID(parts[0])
		}
	}
	if len(parts) >= 2 {
		if d, err := strconv.Atoi(parts[1]); err == nil && d > 0 {
			days = d
		}
	}

	st, err := r.deps.DB.GetSummaryStats(ctx, channelID, days)
	if err != nil || st == nil || st.TotalCount == 0 {
		r.reply(ctx, bot, chatID, replyTo, "📭 暂无统计数据")
		return
	}
	var b strings.Builder
	b.WriteString("📊 **总结统计概览**\n\n")
	b.WriteString(fmt.Sprintf("• 总总结次数: %d 次\n", st.TotalCount))
	b.WriteString(fmt.Sprintf("• 累计处理消息: %d 条（场均 %.1f 条）\n", st.TotalMessages, st.AvgMessages))
	b.WriteString(fmt.Sprintf("• 近 7 天: %d 次 | 近 30 天: %d 次\n", st.WeekCount, st.MonthCount))
	if channelID == "" {
		if ranks, rerr := r.deps.DB.GetChannelRankingLimit(ctx, 5); rerr == nil && len(ranks) > 0 {
			b.WriteString("\n🏆 **活跃频道 Top 5**\n")
			for i, rk := range ranks {
				name := rk.ChannelName
				if name == "" {
					name = rk.ChannelID
				}
				b.WriteString(fmt.Sprintf("%d. %s — %d 次总结（%d 条消息）\n", i+1, name, rk.SummaryCount, rk.TotalMessages))
			}
		}
	}
	r.reply(ctx, bot, chatID, replyTo, b.String())
}

func (r *Router) cmdChangelog(ctx context.Context, bot mtproto.Messenger, chatID int64, replyTo int, rawArgs string) {
	count := 5
	if n, err := strconv.Atoi(strings.TrimSpace(rawArgs)); err == nil && n >= 1 && n <= 30 {
		count = n
	}
	workDir := r.deps.WorkDir
	if workDir == "" {
		workDir = "."
	}
	gitCmd := exec.CommandContext(ctx, "git", "-C", workDir, "log", fmt.Sprintf("-n%d", count), "--pretty=format:• %h %s (%cs)")
	if out, err := gitCmd.Output(); err == nil && len(bytes.TrimSpace(out)) > 0 {
		r.reply(ctx, bot, chatID, replyTo, "📝 **最近更新记录**\n\n"+string(out))
		return
	}
	if raw, err := os.ReadFile(filepath.Join(workDir, "CHANGELOG.md")); err == nil {
		r.reply(ctx, bot, chatID, replyTo, truncateRunesStr(string(raw), 3500))
		return
	}
	r.reply(ctx, bot, chatID, replyTo, "📭 未找到更新日志")
}

func (r *Router) cmdUpdate(ctx context.Context, bot mtproto.Messenger, chatID int64, replyTo int, rawArgs string) {
	check := strings.EqualFold(strings.TrimSpace(rawArgs), "check")
	r.updateApplication(ctx, check, "telegram_command_update", chatID, func(result applicationUpdate) {
		icon := "❌ "
		if result.success {
			icon = "✅ "
		}
		message := icon + result.message
		if check && result.git != "" {
			message += "\n\n" + result.git
		}
		r.reply(ctx, bot, chatID, replyTo, message)
	})
}

func (r *Router) cmdForwardingStatus(ctx context.Context, bot mtproto.Messenger, chatID int64, replyTo int) {
	fwd := r.deps.Config.ForwardingConfig()
	state := "⏸️ 已禁用"
	if fwd.Enabled {
		state = "✅ 已启用"
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("🔁 **频道转发状态**\n• 状态: %s\n• 默认底栏: `%v`\n• 规则数量: %d\n", state, fwd.ShowDefaultFooter, len(fwd.Rules)))
	for i, rule := range fwd.Rules {
		b.WriteString(fmt.Sprintf("%d. `%s` -> `%s` (copy=%v, orig_only=%v)\n", i+1, rule.SourceChannel, rule.TargetChannel, rule.CopyMode, rule.ForwardOriginalOnly))
	}
	r.reply(ctx, bot, chatID, replyTo, b.String())
}

func (r *Router) cmdForwardingToggle(ctx context.Context, bot mtproto.Messenger, chatID int64, replyTo int, enabled bool) {
	_ = r.deps.Config.UpdateConfig(func(doc map[string]any) error {
		fm := ensureForwardingMap(doc)
		fm["enabled"] = enabled
		return nil
	})
	if enabled {
		r.reply(ctx, bot, chatID, replyTo, "✅ 转发功能已启用")
	} else {
		r.reply(ctx, bot, chatID, replyTo, "⏸️ 转发功能已禁用")
	}
}

func (r *Router) cmdForwardingStats(ctx context.Context, bot mtproto.Messenger, chatID int64, replyTo int) {
	if r.deps.DB == nil {
		r.cmdForwardingStatus(ctx, bot, chatID, replyTo)
		return
	}
	st, err := r.deps.DB.GetForwardingStats(ctx, 0)
	if err != nil || len(st) == 0 {
		r.cmdForwardingStatus(ctx, bot, chatID, replyTo)
		return
	}
	raw, _ := json.MarshalIndent(st, "", "  ")
	r.reply(ctx, bot, chatID, replyTo, "📊 **转发统计**\n```json\n"+string(raw)+"\n```")
}

func (r *Router) cmdForwardingAddRule(ctx context.Context, bot mtproto.Messenger, chatID int64, replyTo int, rawArgs string, autoEnable bool) {
	parts := strings.Fields(rawArgs)
	if len(parts) < 2 {
		r.reply(ctx, bot, chatID, replyTo, "用法：`/forwarding_add_rule <source_channel> <target_channel>`")
		return
	}
	src := config.NormalizeChannelID(parts[0])
	dst := config.NormalizeChannelID(parts[1])
	dup := false
	_ = r.deps.Config.UpdateConfig(func(doc map[string]any) error {
		fm := ensureForwardingMap(doc)
		if autoEnable {
			fm["enabled"] = true
		}
		rules, _ := fm["rules"].([]any)
		for _, item := range rules {
			if rm, ok := item.(map[string]any); ok && rm["source_channel"] == src && rm["target_channel"] == dst {
				dup = true
				return nil
			}
		}
		rules = append(rules, map[string]any{
			"source_channel":        src,
			"target_channel":        dst,
			"keywords":              []string{},
			"blacklist":             []string{},
			"patterns":              []string{},
			"blacklist_patterns":    []string{},
			"copy_mode":             false,
			"forward_original_only": false,
		})
		fm["rules"] = rules
		return nil
	})
	if dup {
		r.reply(ctx, bot, chatID, replyTo, fmt.Sprintf("ℹ️ 转发规则已存在: `%s` -> `%s`", src, dst))
		return
	}
	r.reply(ctx, bot, chatID, replyTo, fmt.Sprintf("✅ 已添加转发规则: `%s` -> `%s`", src, dst))
}

func (r *Router) cmdForwardingRemoveRule(ctx context.Context, bot mtproto.Messenger, chatID int64, replyTo int, rawArgs string) {
	parts := strings.Fields(rawArgs)
	if len(parts) == 0 {
		r.reply(ctx, bot, chatID, replyTo, "用法：`/forwarding_remove_rule <规则序号>` 或 `/forwarding_remove_rule <源频道> <目标频道>`")
		return
	}
	removed := false
	_ = r.deps.Config.UpdateConfig(func(doc map[string]any) error {
		fm := ensureForwardingMap(doc)
		rules, _ := fm["rules"].([]any)
		if len(parts) >= 2 {
			src := config.NormalizeChannelID(parts[0])
			dst := config.NormalizeChannelID(parts[1])
			next := make([]any, 0, len(rules))
			for _, item := range rules {
				rm, _ := item.(map[string]any)
				if !removed && rm != nil && rm["source_channel"] == src && rm["target_channel"] == dst {
					removed = true
					continue
				}
				next = append(next, item)
			}
			fm["rules"] = next
			return nil
		}
		idx, err := strconv.Atoi(parts[0])
		if err != nil || idx < 1 || idx > len(rules) {
			return nil
		}
		fm["rules"] = append(rules[:idx-1], rules[idx:]...)
		removed = true
		return nil
	})
	if !removed {
		r.reply(ctx, bot, chatID, replyTo, "⚠️ 未找到匹配的转发规则")
		return
	}
	r.reply(ctx, bot, chatID, replyTo, "🗑️ 已删除转发规则")
}

func (r *Router) cmdForwardingListField(ctx context.Context, bot mtproto.Messenger, chatID int64, replyTo int, rawArgs, field, sep string) {
	idxStr, rest, ok := strings.Cut(strings.TrimSpace(rawArgs), " ")
	idx, err := strconv.Atoi(strings.TrimSpace(idxStr))
	if err != nil || idx < 1 {
		r.reply(ctx, bot, chatID, replyTo, fmt.Sprintf("用法：`<规则序号> [词1%s词2 | clear]`", sep))
		return
	}
	rest = strings.TrimSpace(rest)
	if !ok || rest == "" {
		fwd := r.deps.Config.ForwardingConfig()
		if idx > len(fwd.Rules) {
			r.reply(ctx, bot, chatID, replyTo, "❌ 规则序号超出范围")
			return
		}
		raw, _ := json.Marshal(fwd.Rules[idx-1])
		r.reply(ctx, bot, chatID, replyTo, fmt.Sprintf("📋 规则 #%d:\n```json\n%s\n```", idx, string(raw)))
		return
	}
	var values []string
	if strings.ToLower(rest) != "clear" {
		for _, v := range strings.Split(rest, sep) {
			if t := strings.TrimSpace(v); t != "" {
				values = append(values, t)
			}
		}
	}
	updated := false
	_ = r.deps.Config.UpdateConfig(func(doc map[string]any) error {
		fm := ensureForwardingMap(doc)
		rules, _ := fm["rules"].([]any)
		if idx > len(rules) {
			return nil
		}
		if rm, ok := rules[idx-1].(map[string]any); ok {
			if values == nil {
				values = []string{}
			}
			rm[field] = values
			updated = true
		}
		return nil
	})
	if !updated {
		r.reply(ctx, bot, chatID, replyTo, "❌ 规则序号无效")
		return
	}
	r.reply(ctx, bot, chatID, replyTo, fmt.Sprintf("✅ 已更新规则 #%d 的 `%s`（共 %d 项）", idx, field, len(values)))
}

func (r *Router) cmdForwardingBoolField(ctx context.Context, bot mtproto.Messenger, chatID int64, replyTo int, rawArgs, field string) {
	parts := strings.Fields(rawArgs)
	if len(parts) < 2 {
		r.reply(ctx, bot, chatID, replyTo, "用法：`<规则序号> <on|off>`")
		return
	}
	idx, err := strconv.Atoi(parts[0])
	val, ok := parseCmdBool(parts[1])
	if err != nil || idx < 1 || !ok {
		r.reply(ctx, bot, chatID, replyTo, "❌ 参数格式无效")
		return
	}
	updated := false
	_ = r.deps.Config.UpdateConfig(func(doc map[string]any) error {
		fm := ensureForwardingMap(doc)
		rules, _ := fm["rules"].([]any)
		if idx > len(rules) {
			return nil
		}
		if rm, ok := rules[idx-1].(map[string]any); ok {
			rm[field] = val
			updated = true
		}
		return nil
	})
	if !updated {
		r.reply(ctx, bot, chatID, replyTo, "❌ 规则序号无效")
		return
	}
	r.reply(ctx, bot, chatID, replyTo, fmt.Sprintf("✅ 已设置规则 #%d `%s` = `%v`", idx, field, val))
}

func (r *Router) cmdForwardingRuleInfo(ctx context.Context, bot mtproto.Messenger, chatID int64, replyTo int, rawArgs string) {
	idx, err := strconv.Atoi(strings.TrimSpace(rawArgs))
	fwd := r.deps.Config.ForwardingConfig()
	if err != nil || idx < 1 || idx > len(fwd.Rules) {
		r.reply(ctx, bot, chatID, replyTo, "❌ 请输入有效规则序号（从 1 开始）")
		return
	}
	raw, _ := json.MarshalIndent(fwd.Rules[idx-1], "", "  ")
	r.reply(ctx, bot, chatID, replyTo, fmt.Sprintf("📋 **转发规则 #%d 详情**\n```json\n%s\n```", idx, string(raw)))
}

func (r *Router) cmdForwardingFooter(ctx context.Context, bot mtproto.Messenger, chatID int64, replyTo int, rawArgs string) {
	idxStr, rest, ok := strings.Cut(strings.TrimSpace(rawArgs), " ")
	idx, err := strconv.Atoi(strings.TrimSpace(idxStr))
	if !ok || err != nil || idx < 1 {
		r.reply(ctx, bot, chatID, replyTo, "用法：`/forwarding_footer <规则序号> <自定义模板 | clear>`")
		return
	}
	footer := strings.TrimSpace(rest)
	if strings.EqualFold(footer, "clear") {
		footer = ""
	}
	updated := false
	_ = r.deps.Config.UpdateConfig(func(doc map[string]any) error {
		fm := ensureForwardingMap(doc)
		rules, _ := fm["rules"].([]any)
		if idx > len(rules) {
			return nil
		}
		if rm, ok := rules[idx-1].(map[string]any); ok {
			if footer == "" {
				delete(rm, "custom_footer")
			} else {
				rm["custom_footer"] = footer
			}
			updated = true
		}
		return nil
	})
	if !updated {
		r.reply(ctx, bot, chatID, replyTo, "❌ 规则序号无效")
		return
	}
	r.reply(ctx, bot, chatID, replyTo, fmt.Sprintf("✅ 已更新规则 #%d 底栏配置", idx))
}

func (r *Router) cmdForwardingDefaultFooter(ctx context.Context, bot mtproto.Messenger, chatID int64, replyTo int, rawArgs string) {
	val, ok := parseCmdBool(rawArgs)
	if !ok {
		r.reply(ctx, bot, chatID, replyTo, "用法：`/forwarding_default_footer <on|off>`")
		return
	}
	_ = r.deps.Config.UpdateConfig(func(doc map[string]any) error {
		fm := ensureForwardingMap(doc)
		fm["show_default_footer"] = val
		return nil
	})
	r.reply(ctx, bot, chatID, replyTo, fmt.Sprintf("✅ 默认底栏显示 = `%v`", val))
}

func (r *Router) cmdUserBotStatus(ctx context.Context, bot mtproto.Messenger, chatID int64, replyTo int) {
	ub := r.resolveUserBot()
	if ub == nil {
		r.reply(ctx, bot, chatID, replyTo, "🤖 UserBot 未启用或未初始化")
		return
	}
	st := ub.Status()
	r.reply(ctx, bot, chatID, replyTo, fmt.Sprintf(
		"👤 **UserBot 状态**\n• 已启用: `%v`\n• 已连接: `%v`\n• 可用: `%v`\n• 手机号: `%s`",
		st.Enabled, st.Connected, st.Available, st.PhonePreview,
	))
}

func (r *Router) cmdUserBotJoin(ctx context.Context, bot mtproto.Messenger, chatID int64, replyTo int, rawArgs string) {
	ub := r.resolveUserBot()
	if ub == nil {
		r.reply(ctx, bot, chatID, replyTo, "❌ UserBot 未初始化")
		return
	}
	ch := strings.TrimSpace(rawArgs)
	if ch == "" {
		r.reply(ctx, bot, chatID, replyTo, "用法：`/userbot_join <@channel 或邀请链接>`")
		return
	}
	res := ub.JoinChannel(ctx, ch)
	r.reply(ctx, bot, chatID, replyTo, res.Message)
}

func (r *Router) cmdUserBotLeave(ctx context.Context, bot mtproto.Messenger, chatID int64, replyTo int, rawArgs string) {
	ub := r.resolveUserBot()
	if ub == nil {
		r.reply(ctx, bot, chatID, replyTo, "❌ UserBot 未初始化")
		return
	}
	ch := strings.TrimSpace(rawArgs)
	if ch == "" {
		r.reply(ctx, bot, chatID, replyTo, "用法：`/userbot_leave <@channel 或频道链接>`")
		return
	}
	res := ub.LeaveChannel(ctx, ch)
	r.reply(ctx, bot, chatID, replyTo, res.Message)
}

func (r *Router) cmdUserBotList(ctx context.Context, bot mtproto.Messenger, chatID int64, replyTo int) {
	ub := r.resolveUserBot()
	if ub == nil {
		r.reply(ctx, bot, chatID, replyTo, "❌ UserBot 未初始化")
		return
	}
	list, err := ub.ListJoinedChannels(ctx)
	if err != nil {
		r.reply(ctx, bot, chatID, replyTo, "❌ 获取频道列表失败: "+err.Error())
		return
	}
	if len(list) == 0 {
		r.reply(ctx, bot, chatID, replyTo, "📭 UserBot 当前未加入任何频道")
		return
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("📺 **UserBot 已加入频道（共 %d 个）**\n\n", len(list)))
	for i, ch := range list {
		if i >= 50 {
			b.WriteString(fmt.Sprintf("... 其余 %d 个已省略\n", len(list)-50))
			break
		}
		b.WriteString(fmt.Sprintf("%d. %s (`%d`)\n", i+1, ch.Title, ch.MarkedID))
	}
	r.reply(ctx, bot, chatID, replyTo, b.String())
}

func (r *Router) resolveUserBot() UserBotController {
	if r.deps.UserBot != nil {
		return r.deps.UserBot()
	}
	return nil
}

func ensureForwardingMap(doc map[string]any) map[string]any {
	fm, ok := doc["forwarding"].(map[string]any)
	if !ok || fm == nil {
		fm = map[string]any{}
		doc["forwarding"] = fm
	}
	if _, exists := fm["rules"]; !exists {
		fm["rules"] = []any{}
	}
	return fm
}

func formatExportPayload(rows []map[string]any, format string) ([]byte, string) {
	ts := time.Now().UTC().Format("20060102_150405")
	switch format {
	case "csv":
		var buf bytes.Buffer
		buf.Write([]byte{0xEF, 0xBB, 0xBF}) // UTF-8 BOM
		w := csv.NewWriter(&buf)
		_ = w.Write([]string{"id", "channel_id", "channel_name", "summary_type", "message_count", "created_at", "summary_text"})
		for _, r := range rows {
			_ = w.Write([]string{
				fmt.Sprintf("%v", r["id"]),
				fmt.Sprintf("%v", r["channel_id"]),
				fmt.Sprintf("%v", r["channel_name"]),
				fmt.Sprintf("%v", r["summary_type"]),
				fmt.Sprintf("%v", r["message_count"]),
				fmt.Sprintf("%v", r["created_at"]),
				fmt.Sprintf("%v", r["summary_text"]),
			})
		}
		w.Flush()
		return buf.Bytes(), fmt.Sprintf("summaries_%s.csv", ts)

	case "md":
		var b strings.Builder
		b.WriteString("# Sakura Bot 历史总结导出\n\n")
		for _, r := range rows {
			b.WriteString(fmt.Sprintf("## %v (%v)\n\n", r["channel_name"], r["created_at"]))
			b.WriteString(fmt.Sprintf("%v\n\n---\n\n", r["summary_text"]))
		}
		return []byte(b.String()), fmt.Sprintf("summaries_%s.md", ts)

	default:
		b, _ := json.MarshalIndent(rows, "", "  ")
		return b, fmt.Sprintf("summaries_%s.json", ts)
	}
}

func truncateRunesStr(s string, maxRunes int) string {
	if maxRunes <= 0 || utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	runes := []rune(s)
	return string(runes[:maxRunes]) + "..."
}

const forwardingHelpText = `📖 **频道消息转发命令帮助**

• /forwarding [源频道 目标频道] - 查看状态或快速添加启用规则
• /forwarding_enable | /forwarding_disable - 启用或禁用转发
• /forwarding_stats - 查看转发统计
• /forwarding_add_rule <源频道> <目标频道> - 添加规则
• /forwarding_remove_rule <序号 | 源频道 目标频道> - 删除规则
• /forwarding_keywords <序号> <词1,词2 | clear> - 关键词白名单
• /forwarding_blacklist <序号> <词1,词2 | clear> - 关键词黑名单
• /forwarding_patterns <序号> <正则1;;正则2 | clear> - 正则白名单
• /forwarding_blacklist_patterns <序号> <正则1;;正则2 | clear> - 正则黑名单
• /forwarding_copy_mode <序号> <on|off> - 复制模式开关
• /forwarding_original_only <序号> <on|off> - 只转发原创开关
• /forwarding_footer <序号> <模板 | clear> - 自定义底栏模板
• /forwarding_default_footer <on|off> - 默认底栏开关
• /forwarding_rule_info <序号> - 查看规则详情`
