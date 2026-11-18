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
	"fmt"
	"strings"

	"github.com/Sakura520222/Sakura-Bot/internal/i18n"
	"github.com/Sakura520222/Sakura-Bot/internal/mtproto"
)

const (
	MenuCallbackPrefix = "mainbot"
	MaxCallbackBytes   = 64

	MenuMain       = "main"
	MenuBasic      = "basic"
	MenuQA         = "qa"
	MenuForwarding = "forwarding"
	MenuConfig     = "config"

	CmdHelp             = "help"
	CmdSummary          = "summary"
	CmdChannels         = "channels"
	CmdHistory          = "history"
	CmdStats            = "stats"
	CmdQAStatus         = "qa_status"
	CmdQAStart          = "qa_start"
	CmdQAStop           = "qa_stop"
	CmdQARestart        = "qa_restart"
	CmdQAStats          = "qa_stats"
	CmdForwardingStatus = "fwd_status"
	CmdForwardingStats  = "fwd_stats"
	CmdForwardingHelp   = "fwd_help"
	CmdShowPrompt       = "show_prompt"
	CmdShowAIConfig     = "show_ai"
	CmdShowSchedule     = "show_schedule"
	CmdShowPoll         = "show_poll"
	CmdConfigHelp       = "config_help"
	CmdGuard            = "guard"
)

// ConfigHelpText 是配置菜单点击“配置用法”时发送的只读安全说明（对齐 mainbot_keyboards.py:CONFIG_HELP_TEXT）。
const ConfigHelpText = `⚙️ **配置入口说明**

以下配置类命令通常需要参数或多步输入，按钮不会直接执行危险操作。

**AI 与提示词**
/showprompt - 查看当前总结提示词
/setprompt - 设置总结提示词
/showpollprompt - 查看投票提示词
/setpollprompt - 设置投票提示词
/showaicfg - 查看 AI 配置
/setaicfg - 设置 AI 配置

**频道与定时**
/showchannelschedule - 查看频道时间配置
/setchannelschedule - 设置频道时间配置
/deletechannelschedule - 删除频道时间配置
/channelpoll - 查看频道投票配置
/setchannelpoll - 设置频道投票配置
/deletechannelpoll - 删除频道投票配置

**提示**：如需修改配置，请发送对应命令并按提示操作。`

// CategoryGroup 表示命令目录分组（对齐 core/bot_commands.py:BOT_COMMANDS）。
type CategoryGroup struct {
	Category string
	I18nKey  string
	Commands []mtproto.BotCommandDef
}

// BotCommandGroups 定义主 Bot 全部 79 条斜杠命令分组（严格对齐 core/bot_commands.py）。
var BotCommandGroups = []CategoryGroup{
	{
		Category: "Sakura Wiki",
		I18nKey:  "wiki",
		Commands: []mtproto.BotCommandDef{
			{Command: "wiki_status", Description: "查看频道 Wiki 状态"},
			{Command: "wiki_build", Description: "管理员触发 Wiki 历史构建"},
			{Command: "wiki_pause", Description: "管理员暂停 Wiki 更新"},
		},
	},
	{
		Category: "Sakura Guard",
		I18nKey:  "guard",
		Commands: []mtproto.BotCommandDef{
			{Command: "guard", Description: "查看群管状态与帮助"},
			{Command: "guard_on", Description: "启用指定群组的智能群管"},
			{Command: "guard_off", Description: "关闭指定群组的智能群管"},
			{Command: "guard_verify", Description: "开关入群验证"},
			{Command: "guard_bots", Description: "开关拦截机器人发言"},
			{Command: "guard_addword", Description: "添加全局昵称关键词"},
			{Command: "guard_delword", Description: "删除全局昵称关键词"},
			{Command: "guard_whitelist", Description: "管理本群白名单"},
			{Command: "guard_logs", Description: "查看群管案件"},
			{Command: "guard_unban", Description: "审核案件并解除封禁加入白名单"},
		},
	},
	{
		Category: "基础核心",
		I18nKey:  "basic_core",
		Commands: []mtproto.BotCommandDef{
			{Command: "start", Description: "查看欢迎消息和帮助"},
			{Command: "help", Description: "查看完整命令列表"},
			{Command: "summary", Description: "立即生成本周频道消息汇总"},
		},
	},
	{
		Category: "频道管理",
		I18nKey:  "channel",
		Commands: []mtproto.BotCommandDef{
			{Command: "showchannels", Description: "查看当前频道列表"},
			{Command: "addchannel", Description: "添加频道"},
			{Command: "deletechannel", Description: "删除频道"},
		},
	},
	{
		Category: "定时推送",
		I18nKey:  "schedule_push",
		Commands: []mtproto.BotCommandDef{
			{Command: "showchannelschedule", Description: "查看频道自动总结时间配置"},
			{Command: "setchannelschedule", Description: "设置频道自动总结时间"},
			{Command: "deletechannelschedule", Description: "删除频道自动总结时间配置"},
			{Command: "clearsummarytime", Description: "清除上次总结时间记录"},
			{Command: "setsendtosource", Description: "设置是否将报告发送回源频道"},
		},
	},
	{
		Category: "AI配置",
		I18nKey:  "ai",
		Commands: []mtproto.BotCommandDef{
			{Command: "showprompt", Description: "查看当前提示词"},
			{Command: "setprompt", Description: "设置自定义提示词"},
			{Command: "showpollprompt", Description: "查看当前投票提示词"},
			{Command: "setpollprompt", Description: "设置投票提示词"},
			{Command: "showaicfg", Description: "查看AI配置"},
			{Command: "setaicfg", Description: "设置AI配置"},
		},
	},
	{
		Category: "频道互动",
		I18nKey:  "channel_interaction",
		Commands: []mtproto.BotCommandDef{
			{Command: "channelpoll", Description: "查看频道投票配置"},
			{Command: "setchannelpoll", Description: "设置频道投票配置"},
			{Command: "deletechannelpoll", Description: "删除频道投票配置"},
			{Command: "showcommentwelcome", Description: "查看频道评论区欢迎配置"},
			{Command: "setcommentwelcome", Description: "设置频道评论区欢迎配置"},
			{Command: "deletecommentwelcome", Description: "删除频道评论区欢迎配置"},
			{Command: "showautopoll", Description: "查看自动趣味投票配置"},
			{Command: "setautopoll", Description: "设置自动趣味投票配置"},
			{Command: "deleteautopoll", Description: "删除自动趣味投票配置"},
		},
	},
	{
		Category: "统计历史",
		I18nKey:  "stats_history",
		Commands: []mtproto.BotCommandDef{
			{Command: "history", Description: "查看历史总结"},
			{Command: "export", Description: "导出历史记录"},
			{Command: "stats", Description: "查看统计数据"},
		},
	},
	{
		Category: "系统运维",
		I18nKey:  "system_ops",
		Commands: []mtproto.BotCommandDef{
			{Command: "pause", Description: "暂停所有定时任务"},
			{Command: "resume", Description: "恢复所有定时任务"},
			{Command: "restart", Description: "重启机器人"},
			{Command: "shutdown", Description: "彻底停止机器人"},
			{Command: "showloglevel", Description: "查看当前日志级别"},
			{Command: "setloglevel", Description: "设置日志级别"},
			{Command: "clearcache", Description: "清除讨论组ID缓存"},
			{Command: "db_clear", Description: "清空数据库（危险操作）"},
			{Command: "changelog", Description: "查看更新日志"},
			{Command: "update", Description: "一键更新机器人"},
			{Command: "qa_status", Description: "查看问答Bot运行状态"},
			{Command: "qa_start", Description: "启动问答Bot"},
			{Command: "qa_stop", Description: "停止问答Bot"},
			{Command: "qa_restart", Description: "重启问答Bot"},
			{Command: "qa_stats", Description: "查看问答Bot详细统计"},
			{Command: "qa_runtime", Description: "查看/切换问答Bot运行时（Go|Python）"},
		},
	},
	{
		Category: "偏好设置",
		I18nKey:  "preferences",
		Commands: []mtproto.BotCommandDef{
			{Command: "language", Description: "切换界面语言"},
		},
	},
	{
		Category: "频道转发",
		I18nKey:  "forwarding",
		Commands: []mtproto.BotCommandDef{
			{Command: "forwarding", Description: "查看转发状态或快速添加规则"},
			{Command: "forwarding_enable", Description: "启用转发功能"},
			{Command: "forwarding_disable", Description: "禁用转发功能"},
			{Command: "forwarding_stats", Description: "查看转发统计"},
			{Command: "forwarding_add_rule", Description: "添加转发规则"},
			{Command: "forwarding_remove_rule", Description: "删除转发规则"},
			{Command: "forwarding_keywords", Description: "设置关键词白名单"},
			{Command: "forwarding_blacklist", Description: "设置关键词黑名单"},
			{Command: "forwarding_patterns", Description: "设置正则白名单"},
			{Command: "forwarding_blacklist_patterns", Description: "设置正则黑名单"},
			{Command: "forwarding_copy_mode", Description: "设置复制模式"},
			{Command: "forwarding_original_only", Description: "设置只转发原创"},
			{Command: "forwarding_rule_info", Description: "查看规则详情"},
			{Command: "forwarding_footer", Description: "设置转发底栏"},
			{Command: "forwarding_default_footer", Description: "启用/禁用默认底栏"},
			{Command: "forwarding_help", Description: "查看转发命令帮助"},
		},
	},
	{
		Category: "UserBot管理",
		I18nKey:  "userbot",
		Commands: []mtproto.BotCommandDef{
			{Command: "userbot_status", Description: "查看 UserBot 状态"},
			{Command: "userbot_join", Description: "UserBot 加入频道"},
			{Command: "userbot_leave", Description: "UserBot 离开频道"},
			{Command: "userbot_list", Description: "列出已加入频道"},
		},
	},
}

// AllBotCommands 展平返回全部 79 条斜杠命令定义（供 SetBotCommands 注册使用）。
func AllBotCommands() []mtproto.BotCommandDef {
	total := 0
	for _, g := range BotCommandGroups {
		total += len(g.Commands)
	}
	out := make([]mtproto.BotCommandDef, 0, total)
	for _, g := range BotCommandGroups {
		out = append(out, g.Commands...)
	}
	return out
}

// GenerateHelpText 生成完整的国际化命令帮助文档（对齐 bot_commands.py:generate_help_text）。
func GenerateHelpText() string {
	var b strings.Builder
	title := i18n.T("help.title", nil)
	if title == "help.title" {
		title = "📖 **Sakura Bot 命令帮助**"
	}
	b.WriteString(title)
	b.WriteString("\n\n")

	for _, g := range BotCommandGroups {
		secKey := "help.section_" + g.I18nKey
		secTitle := i18n.T(secKey, nil)
		if secTitle == secKey || secTitle == "" {
			secTitle = g.Category
		}
		b.WriteString("**")
		b.WriteString(secTitle)
		b.WriteString("**\n")
		for _, c := range g.Commands {
			descKey := "cmd." + c.Command
			desc := i18n.T(descKey, nil)
			if desc == descKey || desc == "" {
				desc = c.Description
			}
			b.WriteString(fmt.Sprintf("/%s - %s\n", c.Command, desc))
		}
		b.WriteByte('\n')
	}
	return strings.TrimSpace(b.String())
}

var menuTitles = map[string]string{
	MenuMain:       "🌸 Sakura Bot 主菜单",
	MenuBasic:      "📌 基础高频功能",
	MenuQA:         "🤖 QA Bot 控制",
	MenuForwarding: "🔁 频道转发功能",
	MenuConfig:     "⚙️ 配置入口",
}

var menuDescriptions = map[string]string{
	MenuMain:       "请选择要使用的功能分类：",
	MenuBasic:      "选择常用功能。部分功能可能需要管理员权限。",
	MenuQA:         "管理独立运行的 QA Bot。仅管理员可用。",
	MenuForwarding: "查看频道转发状态、统计和帮助。",
	MenuConfig:     "配置类功能入口。为避免误操作，写入类配置只展示用法提示。",
}

// BuildMenuCallback 构造不超过 64 字节的主 Bot 内联菜单回调字符串。
func BuildMenuCallback(action, value string) string {
	s := fmt.Sprintf("%s:%s:%s", MenuCallbackPrefix, action, value)
	if len(s) > MaxCallbackBytes {
		panic("mainbot callback_data exceeds 64 bytes: " + s)
	}
	return s
}

// BuildMenuText 返回指定菜单页标题与描述。
func BuildMenuText(menu string) string {
	title, ok := menuTitles[menu]
	if !ok {
		title = menuTitles[MenuMain]
	}
	desc, ok := menuDescriptions[menu]
	if !ok {
		desc = menuDescriptions[MenuMain]
	}
	return title + "\n\n" + desc
}

// BuildMenuKeyboard 返回主 Bot 指定菜单页的内联按钮矩阵（对齐 mainbot_keyboards.py:build_mainbot_menu_keyboard）。
func BuildMenuKeyboard(menu string) [][]mtproto.InlineButton {
	switch menu {
	case MenuBasic:
		return [][]mtproto.InlineButton{
			{
				{Text: "📝 立即总结", Data: BuildMenuCallback("cmd", CmdSummary)},
				{Text: "📚 频道列表", Data: BuildMenuCallback("cmd", CmdChannels)},
			},
			{
				{Text: "📋 历史总结", Data: BuildMenuCallback("cmd", CmdHistory)},
				{Text: "📊 统计数据", Data: BuildMenuCallback("cmd", CmdStats)},
			},
			{{Text: "❓ 帮助", Data: BuildMenuCallback("cmd", CmdHelp)}},
			{{Text: "⬅️ 返回主菜单", Data: BuildMenuCallback("menu", MenuMain)}},
		}
	case MenuQA:
		return [][]mtproto.InlineButton{
			{
				{Text: "📊 QA 状态", Data: BuildMenuCallback("cmd", CmdQAStatus)},
				{Text: "📈 QA 统计", Data: BuildMenuCallback("cmd", CmdQAStats)},
			},
			{
				{Text: "▶️ 启动 QA", Data: BuildMenuCallback("cmd", CmdQAStart)},
				{Text: "⏹ 停止 QA", Data: BuildMenuCallback("cmd", CmdQAStop)},
			},
			{{Text: "🔄 重启 QA", Data: BuildMenuCallback("cmd", CmdQARestart)}},
			{{Text: "⬅️ 返回主菜单", Data: BuildMenuCallback("menu", MenuMain)}},
		}
	case MenuForwarding:
		return [][]mtproto.InlineButton{
			{
				{Text: "📡 转发状态", Data: BuildMenuCallback("cmd", CmdForwardingStatus)},
				{Text: "📊 转发统计", Data: BuildMenuCallback("cmd", CmdForwardingStats)},
			},
			{{Text: "📖 转发帮助", Data: BuildMenuCallback("cmd", CmdForwardingHelp)}},
			{{Text: "⬅️ 返回主菜单", Data: BuildMenuCallback("menu", MenuMain)}},
		}
	case MenuConfig:
		return [][]mtproto.InlineButton{
			{
				{Text: "🧠 查看提示词", Data: BuildMenuCallback("cmd", CmdShowPrompt)},
				{Text: "🔧 AI 配置", Data: BuildMenuCallback("cmd", CmdShowAIConfig)},
			},
			{
				{Text: "⏰ 定时配置", Data: BuildMenuCallback("cmd", CmdShowSchedule)},
				{Text: "🗳 投票配置", Data: BuildMenuCallback("cmd", CmdShowPoll)},
			},
			{{Text: "📖 配置用法", Data: BuildMenuCallback("cmd", CmdConfigHelp)}},
			{{Text: "⬅️ 返回主菜单", Data: BuildMenuCallback("menu", MenuMain)}},
		}
	default:
		return [][]mtproto.InlineButton{
			{
				{Text: "📌 基础功能", Data: BuildMenuCallback("menu", MenuBasic)},
				{Text: "🤖 QA 控制", Data: BuildMenuCallback("menu", MenuQA)},
			},
			{
				{Text: "🔁 频道转发", Data: BuildMenuCallback("menu", MenuForwarding)},
				{Text: "⚙️ 配置入口", Data: BuildMenuCallback("menu", MenuConfig)},
			},
			{{Text: "❓ 完整帮助", Data: BuildMenuCallback("cmd", CmdHelp)}},
			{{Text: "🛡 Sakura Guard", Data: BuildMenuCallback("cmd", CmdGuard)}},
		}
	}
}
