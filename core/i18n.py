# Copyright 2026 Sakura-Bot
#
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可，
# 并附加非商业使用限制条款。
#
# - 署名：必须提供本项目的原始来源链接
# - 非商业：禁止任何商业用途和分发
# - 相同方式共享：衍生作品必须采用相同的许可证
#
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
# 许可证全文：参见 LICENSE 文件

"""
国际化（I18n）模块

提供多语言支持，允许用户切换界面语言。
当前支持：zh-CN（简体中文）、en-US（英语）
"""

import logging
from typing import Optional

logger = logging.getLogger(__name__)


# ==================== 翻译文本字典 ====================

# 中文翻译（简体中文）
MESSAGE_ZH_CN = {
    # ========== 权限相关 ==========
    "error.permission_denied": "您没有权限执行此命令",
    "error.not_admin": "您不是管理员",
    "error.admin_only": "只有管理员可以执行此操作",
    # ========== 语言设置 ==========
    "language.current": "当前语言：{language}",
    "language.changed": "语言已更改为：{language}",
    "language.invalid": "无效的语言代码：{language}\n\n支持的语言：\n• zh-CN - 简体中文\n• en-US - 英语",
    "language.usage": "使用格式：/language <语言代码>\n\n示例：\n/language zh-CN\n/language en-US",
    "language.supported": "支持的语言：\n• zh-CN - 简体中文\n• en-US - 英语",
    # ========== 欢迎消息 ==========
    "welcome.title": "🌸 **欢迎使用 Sakura-Bot**",
    "welcome.description": "🤖 我是Telegram智能频道管理助手，专门帮助频道主自动化管理 Telegram 频道内容。",
    "welcome.features_title": "✨ **主要功能**",
    "welcome.feature_summary": "• 📊 AI智能总结频道消息",
    "welcome.feature_schedule": "• ⏰ 支持每天/每周自动总结",
    "welcome.feature_custom": "• 🎯 自定义总结风格和频率",
    "welcome.feature_poll": "• 📝 自动生成投票互动",
    "welcome.feature_multi": "• 👥 多频道同时管理",
    "welcome.feature_history": "• 📜 历史总结记录与查询",
    "welcome.commands_title": "📚 **常用命令**",
    "welcome.command_basic": "**基础命令**\n/start - 查看此欢迎消息\n/summary - 立即生成本周汇总",
    "welcome.command_config": "**配置命令**\n/showchannels - 查看频道列表\n/addchannel - 添加监控频道\n/setchannelschedule - 设置自动总结时间",
    "welcome.command_history": "**历史记录**\n/history - 查看历史总结\n/export - 导出历史记录\n/stats - 查看统计数据",
    "welcome.command_admin": "**管理命令**\n/pause - 暂停定时任务\n/resume - 恢复定时任务\n/changelog - 查看更新日志",
    "welcome.tip": "💡 **提示**\n• 发送 /help 查看完整命令列表\n• 更多信息请访问项目[开源仓库](https://github.com/Sakura520222/Sakura-Bot)",
    # ========== 帮助消息 ==========
    "help.title": "📚 **Sakura-Bot - 完整命令列表**",
    "help.section_basic": "**🤖 基础命令**",
    "help.section_prompt": "**⚙️ 提示词管理**",
    "help.section_ai": "**🤖 AI 配置**",
    "help.section_log": "**📊 日志管理**",
    "help.section_control": "**🔄 机器人控制**",
    "help.section_channel": "**📺 频道管理**",
    "help.section_schedule": "**⏰ 时间配置**",
    "help.section_data": "**🗑️ 数据管理**",
    "help.section_report": "**📤 报告设置**",
    "help.section_poll": "**🗳️ 投票配置**",
    "help.section_cache": "**💾 缓存管理**",
    "help.section_history": "**📜 历史记录**",
    "help.section_language": "**🌐 语言设置**",
    "help.new_feature": " (新功能)",
    "help.tip": "---\n💡 **提示**\n• 大多数命令支持中英文别名\n• 配置类命令需要管理员权限\n• 使用 /start 查看快速入门指南",
    # ========== 命令描述 ==========
    "cmd.start": "/start - 查看欢迎消息和基本介绍",
    "cmd.help": "/help - 查看此完整命令列表",
    "cmd.summary": "/summary - 立即生成本周频道消息汇总",
    "cmd.changelog": "/changelog - 查看项目更新日志",
    "cmd.update": "/update - 一键更新机器人",
    "cmd.showprompt": "/showprompt - 查看当前使用的提示词",
    "cmd.setprompt": "/setprompt - 设置自定义提示词",
    "cmd.showpollprompt": "/showpollprompt - 查看当前投票提示词",
    "cmd.setpollprompt": "/setpollprompt - 设置自定义投票提示词",
    "cmd.showaicfg": "/showaicfg - 查看当前 AI 配置信息",
    "cmd.setaicfg": "/setaicfg - 设置自定义 AI 配置（API Key、Base URL、Model）",
    "cmd.showloglevel": "/showloglevel - 查看当前日志级别",
    "cmd.setloglevel": "/setloglevel - 设置日志级别（DEBUG/INFO/WARNING/ERROR/CRITICAL）",
    "cmd.restart": "/restart - 重启机器人",
    "cmd.shutdown": "/shutdown - 彻底停止机器人",
    "cmd.pause": "/pause - 暂停所有定时任务",
    "cmd.resume": "/resume - 恢复所有定时任务",
    "cmd.showchannels": "/showchannels - 查看当前监控的频道列表",
    "cmd.addchannel": "/addchannel - 添加新频道到监控列表\n• 示例：/addchannel https://t.me/examplechannel",
    "cmd.deletechannel": "/deletechannel - 从监控列表中删除频道\n• 示例：/deletechannel https://t.me/examplechannel",
    "cmd.showchannelschedule": "/showchannelschedule - 查看频道自动总结时间配置",
    "cmd.setchannelschedule": "/setchannelschedule - 设置频道自动总结时间\n• 每天：/setchannelschedule 频道 daily 小时 分钟\n• 每周：/setchannelschedule 频道 weekly 星期,星期 小时 分钟",
    "cmd.deletechannelschedule": "/deletechannelschedule - 删除频道自动总结时间配置",
    "cmd.clearsummarytime": "/clearsummarytime - 清除上次总结时间记录",
    "cmd.setsendtosource": "/setsendtosource - 设置是否将报告发送回源频道",
    "cmd.channelpoll": "/channelpoll - 查看频道投票配置",
    "cmd.setchannelpoll": "/setchannelpoll - 设置频道投票配置\n• 格式：/setchannelpoll 频道 true/false channel/discussion",
    "cmd.deletechannelpoll": "/deletechannelpoll - 删除频道投票配置",
    "cmd.clearcache": "/clearcache - 清除讨论组ID缓存\n• /clearcache - 清除所有缓存\n• /clearcache 频道URL - 清除指定频道缓存",
    "cmd.history": "/history - 查看历史总结\n• /history - 查看所有频道最近10条\n• /history channel1 - 查看指定频道\n• /history channel1 30 - 查看最近30天",
    "cmd.export": "/export - 导出历史记录\n• /export - 导出所有记录为JSON\n• /export channel1 csv - 导出为CSV\n• /export channel1 md - 导出为md",
    "cmd.stats": "/stats - 查看统计数据\n• /stats - 查看所有频道统计\n• /stats channel1 - 查看指定频道统计",
    "cmd.language": "/language - 切换界面语言\n• /language - 查看当前语言\n• /language zh-CN - 切换为中文\n• /language en-US - 切换为英文",
    # ========== 通用消息 ==========
    "success": "操作成功",
    "failed": "操作失败",
    "error.unknown": "发生未知错误",
    "error.invalid_command": "无效的命令格式",
    "error.invalid_parameter": "无效的参数：{parameter}",
    "error.channel_not_found": "频道 {channel} 不在配置列表中",
    "error.channel_exists": "频道 {channel} 已存在于列表中",
    "error.channel_not_in_list": "频道 {channel} 不在列表中",
    "error.no_channels": "当前没有配置任何频道",
    "error.file_not_found": "文件 {filename} 不存在",
    # ========== 频道管理 ==========
    "channel.list_title": "当前配置的频道列表：",
    "channel.add_success": "频道 {channel} 已成功添加到列表中\n\n当前频道数量：{count}",
    "channel.add_failed": "添加频道时出错：{error}",
    "channel.add_invalid_url": "请提供有效的频道URL",
    "channel.delete_success": "频道 {channel} 已成功从列表中删除\n\n当前频道数量：{count}",
    "channel.delete_failed": "删除频道时出错：{error}",
    "channel.will_skip": "频道 {channel} 不在配置列表中，将跳过",
    "channel.no_valid": "没有找到有效的指定频道",
    "channel.unknown": "未知频道",
    "channel.all": "所有频道",
    # ========== 总结相关 ==========
    "summary.generating": "正在为您生成总结...",
    "summary.no_messages": "📋 **{channel} 频道汇总**\n\n该频道自上次总结以来没有新消息。",
    "summary.error": "生成总结时出错：{error}",
    "summary.daily_title": "{channel} 日报 {date}",
    "summary.weekly_title": "{channel} 周报 {start_date}-{end_date}",
    "summary.start_processing": "开始处理频道 {channel} 的消息，共 {count} 条消息",
    # ========== 总结时间管理 ==========
    "summarytime.clear_all_success": "已成功清除所有频道的上次总结时间记录。下次总结将重新抓取过去一周的消息。",
    "summarytime.clear_all_failed": "上次总结时间记录文件不存在，无需清除。",
    "summarytime.clear_channel_success": "已成功清除频道 {channel} 的上次总结时间记录。",
    "summarytime.clear_channel_not_exist": "频道 {channel} 的上次总结时间记录不存在，无需清除。",
    "summarytime.clear_empty_file": "上次总结时间记录文件内容为空，无需清除。",
    "summarytime.clear_error": "清除上次总结时间记录时出错：{error}",
    # ========== 日志级别管理 ==========
    "loglevel.current": "当前日志级别：{level}\n\n可用日志级别：DEBUG, INFO, WARNING, ERROR, CRITICAL",
    "loglevel.invalid": "无效的日志级别：{level}\n\n可用日志级别：DEBUG, INFO, WARNING, ERROR, CRITICAL",
    "loglevel.set_success": "日志级别已成功更改为：{level}\n\n之前的级别：{old_level}",
    "loglevel.set_error": "设置日志级别时出错：{error}",
    # ========== 机器人控制 ==========
    "bot.restarting": "正在重启机器人...",
    "bot.shutting_down": "正在关闭机器人...",
    "bot.paused": "机器人已暂停。定时任务已停止，但手动命令仍可执行。\n使用 /resume 或 /恢复 恢复运行。",
    "bot.resumed": "机器人已恢复运行。定时任务将继续执行。",
    "bot.already_paused": "机器人已经处于暂停状态",
    "bot.already_running": "机器人已经在运行状态",
    "bot.invalid_state_pause": "机器人当前状态为 {state}，无法暂停",
    "bot.invalid_state_resume": "机器人当前状态为 {state}，无法恢复",
    # ========== 调度配置 ==========
    "schedule.all_title": "所有频道的自动总结时间配置：",
    "schedule.usage_daily": "每天模式：/setchannelschedule {channel} daily 23 0",
    "schedule.usage_weekly": "每周模式：/setchannelschedule {channel} weekly mon,thu 14 30",
    "schedule.usage_old": "旧格式：/setchannelschedule {channel} mon 9 0",
    "schedule.invalid_params": "请提供完整的参数。可用格式：\n\n每天模式：/setchannelschedule <频道> daily <小时> <分钟>\n  例如：/setchannelschedule channel daily 23 0\n\n每周模式：/setchannelschedule <频道> weekly <星期> <小时> <分钟>\n  例如：/setchannelschedule channel weekly mon,thu 23 0\n  例如：/setchannelschedule channel weekly sun 9 0\n\n旧格式（向后兼容）：/setchannelschedule <频道> <星期> <小时> <分钟>\n  例如：/setchannelschedule channel mon 9 0",
    "schedule.daily_need_time": "每天模式需要提供小时和分钟：/setchannelschedule channel daily 23 0",
    "schedule.weekly_need_params": "每周模式需要提供星期、小时和分钟：/setchannelschedule channel weekly mon,thu 23 0",
    "schedule.invalid_time": "小时和分钟必须是数字",
    "schedule.set_success": "已成功设置频道 {channel} 的自动总结时间：\n\n• 频率：{frequency}\n• 时间：{hour:02d}:{minute:02d}\n\n下次自动总结将在每天 {hour:02d}:{minute:02d} 执行。",
    "schedule.set_success_weekly": "已成功设置频道 {channel} 的自动总结时间：\n\n• 频率：每周\n• 星期：{days}\n• 时间：{hour:02d}:{minute:02d}\n\n下次自动总结将在每周{days} {hour:02d}:{minute:02d} 执行。",
    "schedule.set_success_old": "已成功设置频道 {channel} 的自动总结时间：\n\n• 星期几：{day_cn} ({day})\n• 时间：{hour:02d}:{minute:02d}\n\n下次自动总结将在每周{day_cn} {hour:02d}:{minute:02d}执行。",
    "schedule.set_failed": "设置失败，请检查日志",
    "schedule.delete_success": "已成功删除频道 {channel} 的自动总结时间配置。\n该频道将使用默认时间配置：每周一 09:00",
    "schedule.delete_error": "删除频道时间配置时出错：{error}",
    "schedule.delete_channel_param": "请提供频道参数：/deletechannelschedule 频道\n\n例如：/deletechannelschedule examplechannel",
    # ========== 投票配置 ==========
    "poll.all_title": "所有频道的投票配置：",
    "poll.channel_title": "频道 {channel} 的投票配置：",
    "poll.status_global": "使用全局配置",
    "poll.status_enabled": "启用",
    "poll.status_disabled": "禁用",
    "poll.location_channel": "频道",
    "poll.location_discussion": "讨论组",
    "poll.info": "• 状态：{status}\n• 发送位置：{location}",
    "poll.usage_set": "/setchannelpoll {channel} true|false channel|discussion",
    "poll.usage_delete": "/deletechannelpoll {channel}",
    "poll.invalid_params": "请提供完整的参数。可用格式：\n\n/setchannelpoll <频道> <enabled> <location>\n\n参数说明：\n• 频道：频道URL或名称\n• enabled：true（启用）或 false（禁用）\n• location：channel（频道）或 discussion（讨论组）\n\n示例：\n/setchannelpoll channel1 true channel\n/setchannelpoll channel1 false discussion\n/setchannelpoll channel1 false channel",
    "poll.invalid_enabled": "无效的enabled参数: {enabled}\n\n有效值：true, false, 1, 0, yes, no",
    "poll.invalid_location": "无效的location参数: {location}\n\n有效值：channel, discussion",
    "poll.set_success": "已成功设置频道 {channel} 的投票配置：\n\n• 状态：{status}\n• 发送位置：{location}",
    "poll.set_note_disabled": "\n注意：投票功能已禁用，不会发送投票。",
    "poll.set_note_channel": "\n注意：投票将直接发送到频道，回复总结消息。",
    "poll.set_note_discussion": "\n注意：投票将发送到讨论组，回复转发消息。",
    "poll.set_failed": "设置失败，请检查日志",
    "poll.delete_success": "已成功删除频道 {channel} 的投票配置。\n\n该频道将使用全局投票配置：\n• 状态：{status}\n• 发送位置：讨论组（默认）",
    "poll.delete_failed": "删除频道投票配置失败，请检查日志",
    "poll.delete_channel_param": "请提供频道参数：/deletechannelpoll 频道\n\n例如：/deletechannelpoll examplechannel",
    "poll.delete_error": "删除频道投票配置时出错：{error}",
    # ========== 投票重新生成 ==========
    "poll_regen.feature_disabled": "❌ 该功能已禁用",
    "poll_regen.invalid_format": "❌ 无效的请求格式",
    "poll_regen.data_not_found": "❌ 未找到相关投票数据",
    "poll_regen.already_voted": "⚠️ 您已经投票过了 (当前: {count}/{threshold})",
    "poll_regen.request_button": "👍 请求重新生成 ({count}/{threshold})",
    "poll_regen.admin_button": "🔄 重新生成投票 (管理员)",
    "poll_regen.vote_success": "✅ 您已成功投票 ({count}/{threshold})",
    "poll_regen.admin_only": "❌ 只有管理员可以重新生成投票",
    "poll_regen.regen_in_progress": "⏳ 正在重新生成投票,请稍候...",
    "poll_regen.threshold_reached": "🎉 投票数达到阈值: {count}/{threshold}, 开始自动重新生成投票",
    "poll_regen.current_progress": "当前投票进度: {count}/{threshold}",
    "poll_regen.poll_deleted": "✅ 成功删除旧投票和按钮",
    "poll_regen.delete_warning": "删除旧消息时出错",
    "poll_regen.generating": "开始生成新的投票内容...",
    "poll_regen.generated": "✅ 新投票生成成功",
    "poll_regen.sent_to_channel": "✅ 新投票已发送到频道",
    "poll_regen.sent_to_discussion": "✅ 新投票已发送到讨论组",
    "poll_regen.updated_storage": "✅ 已更新存储中的投票ID",
    "poll_regen.no_discussion": "频道没有绑定讨论组",
    "poll_regen.no_forward_id": "未找到存储的转发消息ID,无法重新生成投票",
    "poll_regen.using_forward_id": "使用存储的转发消息ID",
    "poll_regen.default_question": "频道调研",
    # ========== 报告发送配置 ==========
    "report.invalid_value": "无效的值：{value}\n\n可用值：true, false, 1, 0, yes, no",
    "report.set_success": "已成功将报告发送回源频道的设置更改为：{value}\n\n当前状态：{status}",
    "report.current_status": "当前报告发送回源频道的设置：{value}\n\n当前状态：{status}\n\n使用格式：/setsendtosource true|false",
    "report.set_error": "设置报告发送回源频道选项时出错：{error}",
    # ========== 缓存管理 ==========
    "cache.admin_only": "❌ 只有管理员可以清除缓存",
    "cache.clear_channel_success": "✅ 已清除频道 {channel} 的讨论组ID缓存",
    "cache.clear_all_success": "✅ 已清除所有讨论组ID缓存（共 {count} 条）",
    "cache.clear_error": "❌ 清除缓存时出错：{error}",
    # ========== 历史记录 ==========
    "history.no_records": "❌ 频道 {channel} 暂无历史总结记录",
    "history.all_no_records": "❌ 暂无历史总结记录",
    "history.title_suffix": "历史总结",
    "history.found_count": "共找到 {count} 条记录，显示最近 {display} 条:\n\n",
    "history.messages": "条",
    "history.times": "次",
    "history.total_summaries": "总总结次数",
    "history.total_messages": "总处理消息",
    "history.avg_per_summary": "平均每次",
    "history.last_summary": "最近总结",
    "history.title": "📜 **历史总结记录**\n\n频道：{channel}\n共找到 {count} 条记录",
    "history.all_title": "📜 **历史总结记录**\n\n频道：{channel}\n共找到 {count} 条记录（显示最近10条）",
    "history.item": "{index}. {type} - {time}\n   消息数：{count}{link}\n   {preview}",
    "history.days_invalid": "天数必须是数字，例如：/history channel1 30",
    "history.processing": "处理消息",
    "history.key_points": "核心要点",
    "history.tip_export": "💡 提示: 使用 /export 导出完整记录",
    "history.query_error": "查询历史记录时出错：{error}",
    "history.exporting": "📦 正在导出历史记录，请稍候...",
    "history.export_done": "✅ 导出成功\n格式: {format}\n文件: {filename}",
    "history.export_no_data": "❌ 导出失败：没有数据可导出或不支持的格式",
    "history.overview_title": "📊 **频道统计概览**",
    "history.ranking_title": "🏆 **频道排行** (按总结次数)",
    "history.ranking_item": "{index}. **{name}**\n   总结: {summary_count} 次 | 消息: {total_messages} 条 | 平均: {avg_messages} 条/次",
    "history.overall_stats": "📈 **总体统计**",
    "history.overall_summary": "• 总总结次数: {total} 次\n• 总处理消息: {messages} 条\n• 频道数量: {channels} 个",
    "history.time_distribution": "⏰ **时间分布**",
    "history.week_count": "• 本周: {count} 次",
    "history.month_count": "• 本月: {count} 次",
    "history.db_info": "💾 **数据库信息**",
    "history.db_records": "• 记录数: {count} 条",
    "history.minutes_ago": "{minutes} 分钟前",
    "history.hours_ago": "{hours} 小时前",
    "history.days_ago": "{days} 天前",
    "history.type_daily": "日报",
    "history.type_weekly": "周报",
    "history.type_manual": "手动总结",
    "history.unknown_time": "未知时间",
    "history.view_full": "\n   📝 查看完整: https://t.me/{channel}/{msg_id}",
    "history.stats_no_data": "❌ 频道 {channel} 暂无统计数据",
    "history.stats_title": "📊 **频道统计数据** - {channel}",
    "history.stats_summary": "📈 总体统计\n• 总总结数：{total}\n• 总处理消息数：{messages}",
    "history.stats_type": "\n📋 类型分布",
    "history.stats_type_item": "• {name}：{count} 次",
    "history.stats_last_summary": "\n⏰ 最近总结：{time}",
    "history.stats_ranking_title": "\n🏆 频道排行",
    "history.stats_ranking_item": "{index}. {channel} - {summary_count} 次总结，{total_messages} 条消息，平均 {avg_messages} 条/次",
    "history.stats_error": "获取统计数据时出错：{error}",
    "history.export_error": "❌ 导出历史记录时出错：{error}",
    "history.invalid_format": "❌ 不支持的导出格式：{format}\n支持的格式：json, csv, md",
    # ========== 消息发送相关 ==========
    "messaging.channel_title_fallback": "频道周报汇总",
    "messaging.send_success": "✅ 总结已成功发送到频道 {channel}",
    "messaging.send_forbidden": "⚠️ **频道发送失败**\n\n频道：{channel}\n原因：机器人没有在该频道发送消息的权限\n\n可能原因：\n• 频道设置为仅讨论组模式\n• 机器人未获得发送消息的权限\n• 频道未启用机器人功能\n\n建议：检查频道管理员权限设置\n\n📊 **总结内容如下：**",
    "messaging.send_error": "❌ 向频道 {channel} 发送报告失败：\n{error}",
    # ========== AI 配置 ==========
    "aicfg.title": "🤖 **当前 AI 配置**",
    "aicfg.api_key": "• API Key：{value}",
    "aicfg.base_url": "• Base URL：{value}",
    "aicfg.model": "• 模型：{value}",
    "aicfg.not_set": "未设置",
    "aicfg.setting": "正在设置 AI 配置...",
    "aicfg.set_prompt": "请依次发送以下AI配置参数，或发送/skip跳过：\n\n1. API Key\n2. Base URL\n3. Model\n\n发送/cancel取消设置",
    "aicfg.cancel": "已取消设置 AI 配置",
    "aicfg.cancelled": "已取消AI配置设置",
    "aicfg.in_progress": "您正在设置AI配置中，请先完成当前配置或发送/cancel取消设置，然后再执行其他命令",
    "aicfg.api_key_set": "API Key已设置为：{key}\n\n请发送Base URL，或发送/skip跳过",
    "aicfg.base_url_set": "Base URL已设置为：{url}\n\n请发送Model，或发送/skip跳过",
    "aicfg.updated": "AI配置已更新：\n\n",
    "aicfg.completed": "AI配置已完成设置，当前配置：\n\n",
    "aicfg.saved": "已保存AI配置到文件",
    "aicfg.set_error": "设置 AI 配置时出错：{error}",
    "aicfg.usage": "使用 /setaicfg 命令设置自定义 AI 配置\n格式：/setaicfg <api_key> <base_url> <model>",
    # ========== 提示词管理 ==========
    "prompt.current_title": "📝 **当前提示词**",
    "prompt.current_content": "\n\n内容：\n```\n{content}\n```",
    "prompt.setting": "正在设置提示词...",
    "prompt.set_success": "提示词已成功更新",
    "prompt.cancel": "已取消设置提示词",
    "prompt.set_error": "设置提示词时出错：{error}",
    "prompt.error_command": "请发送提示词内容，不要发送命令。如果要取消设置，请重新发送命令。",
    "prompt.poll_current_title": "📝 **当前投票提示词**",
    "prompt.poll_setting": "正在设置投票提示词...",
    "prompt.poll_set_success": "投票提示词已成功更新",
    "prompt.poll_cancel": "已取消设置投票提示词",
    "prompt.poll_set_error": "设置投票提示词时出错：{error}",
    # ========== 变更日志 ==========
    "changelog.caption": "📄 项目的完整变更日志文件",
    "changelog.not_found": "更新日志文件 {filename} 不存在",
    "changelog.send_error": "发送变更日志文件时出错：{error}",
    # ========== 星期映射 ==========
    "day.mon": "周一",
    "day.tue": "周二",
    "day.wed": "周三",
    "day.thu": "周四",
    "day.fri": "周五",
    "day.sat": "周六",
    "day.sun": "周日",
    "day.monday": "周一",
    "day.tuesday": "周二",
    "day.wednesday": "周三",
    "day.thursday": "周四",
    "day.friday": "周五",
    "day.saturday": "周六",
    "day.sunday": "周日",
    "day.mondays": "周一、周二",
    "day.tuesdays": "周二、周三",
    "day.wednesdays": "周三、周四",
    "day.thursdays": "周四、周五",
    "day.fridays": "周五、周六",
    "day.saturdays": "周六、周日",
    "day.sundays": "周日、周一",
    # ========== 状态描述 ==========
    "status.enabled": "开启",
    "status.disabled": "关闭",
    "status.on": "开启",
    "status.off": "关闭",
    # ========== 日期/时间相关 ==========
    "date.weekday.monday": "星期一",
    "date.weekday.tuesday": "星期二",
    "date.weekday.wednesday": "星期三",
    "date.weekday.thursday": "星期四",
    "date.weekday.friday": "星期五",
    "date.weekday.saturday": "星期六",
    "date.weekday.sunday": "星期日",
    "date.frequency.daily": "每天",
    "date.frequency.weekly": "每周",
    # ========== 投票相关（补充） ==========
    "poll.generating": "正在生成投票内容...",
    "poll.default_question": "你对本周总结有什么看法？",
    "poll.default_options.0": "非常满意",
    "poll.default_options.1": "比较满意",
    "poll.default_options.2": "一般",
    "poll.default_options.3": "有待改进",
    "poll.send_success": "✅ 投票已发送",
    "poll.send_failed": "❌ 投票发送失败",
    "poll.waiting_forward": "⏳ 等待频道消息转发到讨论组...",
    "poll.forward_timeout": "⏱️ 等待转发消息超时（10秒），可能转发延迟或未成功",
    "poll.no_discussion_group": "⚠️ 频道 {channel} 没有绑定讨论组，无法发送投票到评论区",
    "poll.bot_not_in_discussion": "⚠️ 机器人未加入讨论组 {group_id} 或没有权限",
    # ========== 总结类型 ==========
    "summary_type.daily": "日报",
    "summary_type.weekly": "周报",
    "summary_type.manual": "手动总结",
    # ========== 调度格式标题 ==========
    "schedule.format_header": "\n使用格式：\n",
    # ========== 投票超时回退 ==========
    "poll.timeout_fallback": "📊 **投票：{question}**\n\n{options}",
    # ========== 问答Bot控制 ==========
    "qabot.status_title": "📊 **问答Bot运行状态**",
    "qabot.status_running": "🟢 运行中",
    "qabot.status_stopped": "🔴 已停止",
    "qabot.status_not_running": "未运行",
    "qabot.status_uptime": "运行时间",
    "qabot.status_pid": "进程ID",
    "qabot.status_feature_enabled": "功能开关",
    "qabot.status_enabled": "✅ 已启用",
    "qabot.status_disabled": "❌ 已禁用",
    "qabot.status_token_configured": "Token配置",
    "qabot.status_token_configured_yes": "✅ 已配置",
    "qabot.status_token_configured_no": "❌ 未配置",
    "qabot.status_tip_start": "💡 **提示**: 使用 `/qa_start` 启动问答Bot",
    "qabot.stats_title": "📈 **详细统计信息**",
    "qabot.stats_unavailable": "⚠️ 统计数据暂时不可用",
    "qabot.management_commands": "🔧 **管理命令**",
    "qabot.already_running": "⚠️ 问答Bot已在运行中 (PID: {pid})",
    "qabot.not_enabled": "❌ 问答Bot未启用 (QA_BOT_ENABLED=False)",
    "qabot.token_not_configured": "❌ 未配置QA_BOT_TOKEN，无法启动",
    "qabot.starting": "⏳ 正在启动问答Bot...",
    "qabot.start_success": "✅ {message}",
    "qabot.tip_view_status": "💡 使用 `/qa_status` 查看运行状态",
    "qabot.stopping": "⏳ 正在停止问答Bot...",
    "qabot.stop_success": "✅ {message}",
    "qabot.not_running": "⚠️ 问答Bot未运行，无需停止",
    "qabot.restarting": "⏳ 正在重启问答Bot...",
    "qabot.restart_success": "✅ {message}",
    "qabot.detailed_stats_title": "📊 **问答Bot详细统计**",
    "qabot.running_status": "**运行状态**",
    "qabot.stats_running": "• 状态: 🟢 运行中",
    "qabot.stats_stopped": "• 状态: 🔴 已停止",
    "qabot.user_stats": "**用户统计**",
    "qabot.query_stats": "**查询统计**",
    "qabot.subscription_stats": "**订阅统计**",
    "qabot.request_stats": "**请求统计**",
    "qabot.top_users": "**🏆 活跃用户排行 (前10)**",
    "qabot.channel_distribution": "**📢 频道订阅分布**",
    "qabot.stats_error": "⚠️ 无法获取详细统计数据",
    "qabot.tip_view_brief": "💡 使用 `/qa_status` 查看简要状态",
    "qabot.total_users": "• 总用户数: {count}",
    "qabot.active_users": "• 活跃用户数 (7天内): {count}",
    "qabot.new_users_today": "• 今日新增用户: {count}",
    "qabot.queries_today": "• 今日查询次数: {count}",
    "qabot.queries_week": "• 本周查询次数: {count}",
    "qabot.total_queries": "• 总查询次数: {count}",
    "qabot.total_subscriptions": "• 总订阅数: {count}",
    "qabot.active_subscriptions": "• 活跃订阅数: {count}",
    "qabot.pending_requests": "• 待处理请求数: {count}",
    "qabot.completed_requests_today": "• 今日完成请求: {count}",
    "qabot.total_requests": "• 总请求数: {count}",
    "qabot.user_rank_item": "{index}. {name} - {count} 次查询",
    "qabot.channel_sub_item": "• {channel}: {count} 个订阅",
    # ========== QA Bot 控制命令描述 ==========
    "qabot.cmd.qa_start": "启动问答Bot",
    "qabot.cmd.qa_stop": "停止问答Bot",
    "qabot.cmd.qa_restart": "重启问答Bot",
    "qabot.cmd.qa_stats": "查看详细统计",
    # ========== QA Bot 进程消息 ==========
    "qabot.started": "问答Bot启动成功 (PID: {pid})",
    "qabot.stopped": "问答Bot已成功停止 (PID: {pid})",
    "qabot.force_stopped": "问答Bot已强制停止",
    "qabot.not_running_short": "问答Bot未运行",
    "qabot.restarted": "问答Bot重启成功 (新PID: {pid})",
    "qabot.running_normal": "问答Bot运行正常 (PID: {pid}, 运行时间: {uptime}秒)",
    # ========== QA Bot 自动恢复 ==========
    "qabot.auto_restart": "⚠️ **问答Bot自动恢复**",
    "qabot.process_not_running": "问答Bot进程未运行，需要自动重启",
    "qabot.attempting_recovery": "尝试自动恢复中...",
    "qabot.recovered": "✅ **问答Bot已恢复**\n\n问答Bot重启成功 (新PID: {pid})",
    "qabot.recovery_failed": "❌ **问答Bot自动恢复失败**\n\n{message}\n\n请手动检查并重启",
    # ========== 数据库迁移 ==========
    "database.migrate.checking": "正在检查数据库迁移准备状态...",
    "database.migrate.check_result": "数据库迁移检查结果",
    "database.migrate.sqlite_found": "SQLite数据库已找到",
    "database.migrate.sqlite_not_found": "未找到SQLite数据库",
    "database.migrate.total_records": "总记录数",
    "database.migrate.table_details": "表详情",
    "database.migrate.mysql_configured": "MySQL已配置",
    "database.migrate.mysql_not_configured": "MySQL未配置",
    "database.migrate.mysql_connected": "MySQL连接成功",
    "database.migrate.mysql_connect_failed": "MySQL连接失败",
    "database.migrate.status": "状态",
    "database.migrate.ready": "可以开始迁移",
    "database.migrate.not_ready": "暂不能开始迁移",
    "database.migrate.reason": "原因",
    "database.migrate.can_start": "可以开始迁移",
    "database.migrate.check_failed": "检查失败",
    "database.migrate.starting": "开始迁移数据库",
    "database.migrate.please_wait": "迁移过程可能需要几分钟，请耐心等待",
    "database.migrate.check_progress": "查看进度",
    "database.migrate.already_in_progress": "迁移正在进行中，请稍候",
    "database.migrate.success": "数据库迁移成功",
    "database.migrate.failed": "数据库迁移失败",
    "database.migrate.migrated": "已迁移",
    "database.migrate.failed_count": "失败",
    "database.migrate.verification_passed": "数据验证通过",
    "database.migrate.verification_warning": "数据验证警告，请检查记录数",
    "database.migrate.backup_location": "备份位置",
    "database.migrate.next_step": "后续步骤",
    "database.migrate.step1": "修改 .env 文件中的 DATABASE_TYPE=mysql",
    "database.migrate.step2": "重启机器人以使用MySQL数据库",
    "database.migrate.step3": "确认运行正常后可删除SQLite备份文件",
    "database.migrate.error": "错误",
    "database.migrate.current_status": "当前迁移状态",
    "database.migrate.progress": "进度",
    "database.migrate.message": "消息",
    "database.migrate.table_stats": "表统计",
    "database.migrate.status_failed": "获取状态失败",
    "database.migrate.no_migration_in_progress": "当前没有进行中的迁移",
    "database.startup_old_database": "⚠️ **检测到旧数据库**\n\n当前使用的是SQLite数据库。\n\n💡 建议迁移到MySQL以获得更好的性能和并发支持。\n\n使用 /migrate_check 查看迁移准备状态",
    "database.migrate.mysql_has_data": "MySQL数据库中已存在数据，请确认是否覆盖",
    "database.migrate.check_passed": "迁移条件检查通过",
    "database.migrate.mysql_connect_error": "MySQL连接失败",
    "database.migrate.mysql_incomplete": "MySQL配置不完整",
    "database.migrate.check_general_error": "检查失败",
    "database.migrate.not_initialized": "数据库未初始化，请先调用 check_migration_ready()",
    "database.migrate.start_migrating": "开始迁移",
    "database.migrate.migrating_table": "正在迁移表",
    "database.migrate.migration_complete": "迁移成功完成",
    "database.migrate.migration_failed": "迁移失败",
    "database.migrate.auto_switch": "✅ 已自动切换到 MySQL 数据库",
    "database.migrate.restart_required": "⚠️ 需要重启机器人以使用 MySQL 数据库",
    "database.migrate.restart_command": "📝 重启命令",
    "database.migrate.manual_switch_required": "⚠️ 自动切换失败，需要手动切换",
    "database.migrate.manual_switch_steps": "📝 手动切换步骤",
    "database.migrate.sqlite_deleted": "✅ 原 SQLite 数据库文件已删除",
    "database.migrate.sqlite_delete_failed": "⚠️ 删除 SQLite 数据库文件失败",
    "database.migrate.auto_restart_required": "🔄 机器人将自动重启以应用 MySQL 数据库配置",
    "database.migrate.restart_in_3s": "✅ 3 秒后自动重启，重启后将使用 MySQL 数据库",
    "database.migrate.next_steps": "🎉 迁移成功！后续步骤",
    "database.migrate.warning_keep_backup": "⚠️ 请保留备份文件一段时间，确认 MySQL 工作正常后再删除",
    # ========== 数据库清空 ==========
    "database.clear.warning": "⚠️ 危险操作：清空 MySQL 数据库",
    "database.clear.current_data": "当前数据统计",
    "database.clear.total_summaries": "总结总数",
    "database.clear.irreversible": "🚨 此操作不可逆！所有数据将被永久删除！",
    "database.clear.confirm_instruction": "请仔细确认后执行以下命令",
    "database.clear.confirm_command": "确认清空",
    "database.clear.cancel_command": "取消操作",
    "database.clear.not_mysql": "当前未使用 MySQL 数据库",
    "database.clear.current_type": "当前数据库类型",
    "database.clear.only_mysql": "此命令仅支持 MySQL 数据库",
    "database.clear.prepare_failed": "准备清空数据库失败",
    "database.clear.clearing": "正在清空数据库",
    "database.clear.completed": "数据库清空完成",
    "database.clear.cleared_tables": "已清空的表",
    "database.clear.failed_tables": "清空失败的表",
    "database.clear.note": "💡 表结构已保留，可以继续使用",
    "database.clear.failed": "清空数据库失败",
    "database.clear.no_pending": "没有待确认的清空操作",
    "database.clear.cancelled": "已取消清空数据库操作",
    # ========== Comment Welcome Message ==========
    "comment_welcome.message": "🌸 评论区已开放，快来抢占沙发吧～",
    "comment_welcome.button": "申请周报总结",
    "comment_welcome.already_requested": "正在努力总结中，请稍候",
    "comment_welcome.request_sent": "已收到您的请求，我们会尽快处理",
    # ========== Comment Welcome Config ==========
    "comment_welcome.config.title": "📢 评论区欢迎配置",
    "comment_welcome.config.channel": "频道 {channel}",
    "comment_welcome.config.status": "• 状态",
    "comment_welcome.config.message": "• 欢迎消息",
    "comment_welcome.config.button": "• 按钮文本",
    "comment_welcome.config.action": "• 按钮行为",
    "comment_welcome.config.enabled": "✅ 已启用",
    "comment_welcome.config.disabled": "❌ 已禁用",
    "comment_welcome.config.no_config": "💡 该频道未配置，使用默认设置",
    "comment_welcome.config.all_title": "所有频道的评论区欢迎配置",
    "comment_welcome.config.set_success": "✅ 已成功设置频道 {channel} 的评论区欢迎配置",
    "comment_welcome.config.set_failed": "❌ 设置失败：{error}",
    "comment_welcome.config.delete_success": "✅ 已删除频道 {channel} 的评论区欢迎配置",
    "comment_welcome.config.delete_failed": "❌ 删除失败：{error}",
    "comment_welcome.config.invalid_params": "❌ 参数无效",
    "comment_welcome.config.usage": '用法：/setcommentwelcome <channel> <enabled> [welcome_message] [button_text]\n\n示例：\n/setcommentwelcome channel1 true\n/setcommentwelcome channel1 true "欢迎来到评论区" "申请总结"\n/setcommentwelcome channel1 false',
    # ========== 更新命令 ==========
    "update.checking": "🔄 正在检查更新...",
    "update.latest": "✅ 当前已是最新版本 {version}",
    "update.new_found": "✅ 发现新版本 {remote} (当前: {local})，正在更新...",
    "update.updating": "🔄 正在更新代码...",
    "update.installing_deps": "📦 正在安装依赖...",
    "update.success": "✅ 更新完成，正在重启...",
    "update.error": "❌ 更新失败: {error}",
    "update.no_git": "❌ 当前目录不是 Git 仓库",
    "update.git_error": "❌ Git 操作失败: {error}",
    "update.deps_error": "❌ 依赖安装失败: {error}",
    # ========== 数据库自动降级 ==========
    "database.fallback.mysql_failed": "❌ MySQL 连接失败",
    "database.fallback.error": "错误详情",
    "database.fallback.host": "MySQL 主机",
    "database.fallback.port": "端口",
    "database.fallback.database": "数据库",
    "database.fallback.user": "用户",
    "database.fallback.auto_switching": "🔄 自动降级到 SQLite...",
    "database.fallback.success": "✅ 已自动切换到 SQLite 数据库",
    "database.fallback.config_updated": "✅ 配置文件已更新",
    "database.fallback.config_error": "⚠️ 配置文件更新失败，请手动修改 data/.env 中的 DATABASE_TYPE=sqlite",
    "database.fallback.tip": "💡 SQLite 数据库已就绪，机器人将继续运行",
    "database.fallback.manual_required": "⚠️ 需要手动操作",
    "database.fallback.manual_steps": "请执行以下步骤：\n1. 修改 data/.env 文件\n2. 将 DATABASE_TYPE=mysql 改为 DATABASE_TYPE=sqlite\n3. 重启机器人",
}

# 英文翻译
MESSAGE_EN_US = {
    # ========== Permission Related ==========
    "error.permission_denied": "You do not have permission to execute this command",
    "error.not_admin": "You are not an administrator",
    "error.admin_only": "Only administrators can perform this operation",
    # ========== Language Settings ==========
    "language.current": "Current language: {language}",
    "language.changed": "Language changed to: {language}",
    "language.invalid": "Invalid language code: {language}\n\nSupported languages:\n• zh-CN - Simplified Chinese\n• en-US - English",
    "language.usage": "Usage: /language <language_code>\n\nExamples:\n/language zh-CN\n/language en-US",
    "language.supported": "Supported languages:\n• zh-CN - Simplified Chinese\n• en-US - English",
    # ========== Welcome Message ==========
    "welcome.title": "🌸 **Welcome to Sakura-Channel Summary Assistant**",
    "welcome.description": "🤖 I am an intelligent Telegram channel management assistant, specializing in helping channel owners automate Telegram channel content management.",
    "welcome.features_title": "✨ **Main Features**",
    "welcome.feature_summary": "• 📊 AI-powered channel message summarization",
    "welcome.feature_schedule": "• ⏰ Support for daily/weekly automatic summaries",
    "welcome.feature_custom": "• 🎯 Customizable summary styles and frequency",
    "welcome.feature_poll": "• 📝 Automatic poll generation",
    "welcome.feature_multi": "• 👥 Multi-channel management",
    "welcome.feature_history": "• 📜 Summary history records and queries",
    "welcome.commands_title": "📚 **Common Commands**",
    "welcome.command_basic": "**Basic Commands**\n/start - View this welcome message\n/summary - Generate weekly summary immediately",
    "welcome.command_config": "**Configuration Commands**\n/showchannels - View channel list\n/addchannel - Add monitoring channel\n/setchannelschedule - Set automatic summary time",
    "welcome.command_history": "**History**\n/history - View summary history\n/export - Export history records\n/stats - View statistics",
    "welcome.command_admin": "**Management Commands**\n/pause - Pause scheduled tasks\n/resume - Resume scheduled tasks\n/changelog - View changelog",
    "welcome.tip": "💡 **Tips**\n• Send /help to view complete command list\n• Visit [GitHub repository](https://github.com/Sakura520222/Sakura-Bot) for more information",
    # ========== Help Message ==========
    "help.title": "📚 **Sakura-Channel Summary Assistant - Complete Command List**",
    "help.section_basic": "**🤖 Basic Commands**",
    "help.section_prompt": "**⚙️ Prompt Management**",
    "help.section_ai": "**🤖 AI Configuration**",
    "help.section_log": "**📊 Log Management**",
    "help.section_control": "**🔄 Bot Control**",
    "help.section_channel": "**📺 Channel Management**",
    "help.section_schedule": "**⏰ Schedule Configuration**",
    "help.section_data": "**🗑️ Data Management**",
    "help.section_report": "**📤 Report Settings**",
    "help.section_poll": "**🗳️ Poll Configuration**",
    "help.section_cache": "**💾 Cache Management**",
    "help.section_history": "**📜 History**",
    "help.section_language": "**🌐 Language Settings**",
    "help.new_feature": " (New Feature)",
    "help.tip": "---\n💡 **Tips**\n• Most commands support Chinese/English aliases\n• Configuration commands require admin permissions\n• Use /start for quick start guide",
    # ========== Command Descriptions ==========
    "cmd.start": "/start - View welcome message and basic introduction",
    "cmd.help": "/help - View this complete command list",
    "cmd.summary": "/summary - Generate weekly channel message summary immediately",
    "cmd.changelog": "/changelog - View project changelog",
    "cmd.update": "/update - One-click bot update",
    "cmd.showprompt": "/showprompt - View current prompt",
    "cmd.setprompt": "/setprompt - Set custom prompt",
    "cmd.showpollprompt": "/showpollprompt - View current poll prompt",
    "cmd.setpollprompt": "/setpollprompt - Set custom poll prompt",
    "cmd.showaicfg": "/showaicfg - View current AI configuration",
    "cmd.setaicfg": "/setaicfg - Set custom AI configuration (API Key, Base URL, Model)",
    "cmd.showloglevel": "/showloglevel - View current log level",
    "cmd.setloglevel": "/setloglevel - Set log level (DEBUG/INFO/WARNING/ERROR/CRITICAL)",
    "cmd.restart": "/restart - Restart bot",
    "cmd.shutdown": "/shutdown - Shutdown bot completely",
    "cmd.pause": "/pause - Pause all scheduled tasks",
    "cmd.resume": "/resume - Resume all scheduled tasks",
    "cmd.showchannels": "/showchannels - View currently monitored channel list",
    "cmd.addchannel": "/addchannel - Add new channel to monitoring list\n• Example: /addchannel https://t.me/examplechannel",
    "cmd.deletechannel": "/deletechannel - Remove channel from monitoring list\n• Example: /deletechannel https://t.me/examplechannel",
    "cmd.showchannelschedule": "/showchannelschedule - View channel auto-summary schedule",
    "cmd.setchannelschedule": "/setchannelschedule - Set channel auto-summary schedule\n• Daily: /setchannelschedule channel daily hour minute\n• Weekly: /setchannelschedule channel weekly day,day hour minute",
    "cmd.deletechannelschedule": "/deletechannelschedule - Delete channel auto-summary schedule",
    "cmd.clearsummarytime": "/clearsummarytime - Clear last summary time record",
    "cmd.setsendtosource": "/setsendtosource - Set whether to send report back to source channel",
    "cmd.channelpoll": "/channelpoll - View channel poll configuration",
    "cmd.setchannelpoll": "/setchannelpoll - Set channel poll configuration\n• Format: /setchannelpoll channel true/false channel/discussion",
    "cmd.deletechannelpoll": "/deletechannelpoll - Delete channel poll configuration",
    "cmd.clearcache": "/clearcache - Clear discussion group ID cache\n• /clearcache - Clear all cache\n• /clearcache channel_url - Clear specific channel cache",
    "cmd.history": "/history - View summary history\n• /history - View 10 most recent from all channels\n• /history channel1 - View specific channel\n• /history channel1 30 - View last 30 days",
    "cmd.export": "/export - Export history records\n• /export - Export all as JSON\n• /export channel1 csv - Export as CSV\n• /export channel1 md - Export as Markdown",
    "cmd.stats": "/stats - View statistics\n• /stats - View all channel stats\n• /stats channel1 - View specific channel stats",
    "cmd.language": "/language - Switch interface language\n• /language - View current language\n• /language zh-CN - Switch to Chinese\n• /language en-US - Switch to English",
    # ========== Common Messages ==========
    "success": "Operation successful",
    "failed": "Operation failed",
    "error.unknown": "An unknown error occurred",
    "error.invalid_command": "Invalid command format",
    "error.invalid_parameter": "Invalid parameter: {parameter}",
    "error.channel_not_found": "Channel {channel} is not in the configuration list",
    "error.channel_exists": "Channel {channel} already exists in the list",
    "error.channel_not_in_list": "Channel {channel} is not in the list",
    "error.no_channels": "No channels are currently configured",
    "error.file_not_found": "File {filename} not found",
    # ========== Channel Management ==========
    "channel.list_title": "Currently configured channel list:",
    "channel.add_success": "Channel {channel} has been successfully added to the list\n\nTotal channels: {count}",
    "channel.add_failed": "Error adding channel: {error}",
    "channel.add_invalid_url": "Please provide a valid channel URL",
    "channel.delete_success": "Channel {channel} has been successfully removed from the list\n\nTotal channels: {count}",
    "channel.delete_failed": "Error removing channel: {error}",
    "channel.will_skip": "Channel {channel} is not in the configuration list, will skip",
    "channel.no_valid": "No valid specified channels found",
    "channel.unknown": "Unknown channel",
    "channel.all": "All channels",
    # ========== Summary Related ==========
    "summary.generating": "Generating summary for you...",
    "summary.no_messages": "📋 **{channel} Channel Summary**\n\nThere are no new messages in this channel since the last summary.",
    "summary.error": "Error generating summary: {error}",
    "summary.daily_title": "{channel} Daily Report {date}",
    "summary.weekly_title": "{channel} Weekly Report {start_date}-{end_date}",
    "summary.start_processing": "Starting to process messages from channel {channel}, total {count} messages",
    # ========== Summary Time Management ==========
    "summarytime.clear_all_success": "Successfully cleared last summary time records for all channels. Next summary will fetch messages from the past week.",
    "summarytime.clear_all_failed": "Last summary time record file does not exist, nothing to clear.",
    "summarytime.clear_channel_success": "Successfully cleared last summary time record for channel {channel}.",
    "summarytime.clear_channel_not_exist": "Last summary time record for channel {channel} does not exist, nothing to clear.",
    "summarytime.clear_empty_file": "Last summary time record file is empty, nothing to clear.",
    "summarytime.clear_error": "Error clearing last summary time record: {error}",
    # ========== Log Level Management ==========
    "loglevel.current": "Current log level: {level}\n\nAvailable log levels: DEBUG, INFO, WARNING, ERROR, CRITICAL",
    "loglevel.invalid": "Invalid log level: {level}\n\nAvailable log levels: DEBUG, INFO, WARNING, ERROR, CRITICAL",
    "loglevel.set_success": "Log level successfully changed to: {level}\n\nPrevious level: {old_level}",
    "loglevel.set_error": "Error setting log level: {error}",
    # ========== Bot Control ==========
    "bot.restarting": "Restarting bot...",
    "bot.shutting_down": "Shutting down bot...",
    "bot.paused": "Bot has been paused. Scheduled tasks have stopped, but manual commands can still be executed.\nUse /resume to resume running.",
    "bot.resumed": "Bot has resumed running. Scheduled tasks will continue to execute.",
    "bot.already_paused": "Bot is already paused",
    "bot.already_running": "Bot is already running",
    "bot.invalid_state_pause": "Bot current state is {state}, cannot pause",
    "bot.invalid_state_resume": "Bot current state is {state}, cannot resume",
    # ========== Schedule Configuration ==========
    "schedule.all_title": "Auto-summary schedule for all channels:",
    "schedule.usage_daily": "Daily mode: /setchannelschedule {channel} daily 23 0",
    "schedule.usage_weekly": "Weekly mode: /setchannelschedule {channel} weekly mon,thu 14 30",
    "schedule.usage_old": "Old format: /setchannelschedule {channel} mon 9 0",
    "schedule.invalid_params": "Please provide complete parameters. Available formats:\n\nDaily mode: /setchannelschedule <channel> daily <hour> <minute>\n  Example: /setchannelschedule channel daily 23 0\n\nWeekly mode: /setchannelschedule <channel> weekly <day,day> <hour> <minute>\n  Example: /setchannelschedule channel weekly mon,thu 23 0\n  Example: /setchannelschedule channel weekly sun 9 0\n\nOld format (backward compatible): /setchannelschedule <channel> <day> <hour> <minute>\n  Example: /setchannelschedule channel mon 9 0",
    "schedule.daily_need_time": "Daily mode requires hour and minute: /setchannelschedule channel daily 23 0",
    "schedule.weekly_need_params": "Weekly mode requires day, hour and minute: /setchannelschedule channel weekly mon,thu 23 0",
    "schedule.invalid_time": "Hour and minute must be numbers",
    "schedule.set_success": "Successfully set auto-summary time for channel {channel}:\n\n• Frequency: {frequency}\n• Time: {hour:02d}:{minute:02d}\n\nNext auto-summary will execute daily at {hour:02d}:{minute:02d}.",
    "schedule.set_success_weekly": "Successfully set auto-summary time for channel {channel}:\n\n• Frequency: Weekly\n• Days: {days}\n• Time: {hour:02d}:{minute:02d}\n\nNext auto-summary will execute on {days} at {hour:02d}:{minute:02d}.",
    "schedule.set_success_old": "Successfully set auto-summary time for channel {channel}:\n\n• Day: {day_cn} ({day})\n• Time: {hour:02d}:{minute:02d}\n\nNext auto-summary will execute on {day_cn} at {hour:02d}:{minute:02d}.",
    "schedule.set_failed": "Setting failed, please check logs",
    "schedule.delete_success": "Successfully deleted auto-summary schedule for channel {channel}.\nThis channel will use default schedule: Monday 09:00",
    "schedule.delete_error": "Error deleting channel schedule: {error}",
    "schedule.delete_channel_param": "Please provide channel parameter: /deletechannelschedule channel\n\nExample: /deletechannelschedule examplechannel",
    # ========== Poll Configuration ==========
    "poll.all_title": "Poll configuration for all channels:",
    "poll.channel_title": "Poll configuration for channel {channel}:",
    "poll.status_global": "Use global configuration",
    "poll.status_enabled": "Enabled",
    "poll.status_disabled": "Disabled",
    "poll.location_channel": "Channel",
    "poll.location_discussion": "Discussion",
    "poll.info": "• Status: {status}\n• Location: {location}",
    "poll.usage_set": "/setchannelpoll {channel} true|false channel|discussion",
    "poll.usage_delete": "/deletechannelpoll {channel}",
    "poll.invalid_params": "Please provide complete parameters. Available formats:\n\n/setchannelpoll <channel> <enabled> <location>\n\nParameter description:\n• Channel: Channel URL or name\n• enabled: true (enable) or false (disable)\n• location: channel (channel) or discussion (discussion group)\n\nExamples:\n/setchannelpoll channel1 true channel\n/setchannelpoll channel1 false discussion\n/setchannelpoll channel1 false channel",
    "poll.invalid_enabled": "Invalid enabled parameter: {enabled}\n\nValid values: true, false, 1, 0, yes, no",
    "poll.invalid_location": "Invalid location parameter: {location}\n\nValid values: channel, discussion",
    "poll.set_success": "Successfully set poll configuration for channel {channel}:\n\n• Status: {status}\n• Location: {location}",
    "poll.set_note_disabled": "\nNote: Poll feature is disabled, no polls will be sent.",
    "poll.set_note_channel": "\nNote: Polls will be sent directly to the channel, replying to the summary message.",
    "poll.set_note_discussion": "\nNote: Polls will be sent to the discussion group, replying to the forwarded message.",
    "poll.set_failed": "Setting failed, please check logs",
    "poll.delete_success": "Successfully deleted poll configuration for channel {channel}.\n\nThis channel will use global poll configuration:\n• Status: {status}\n• Location: Discussion group (default)",
    "poll.delete_failed": "Failed to delete channel poll configuration, please check logs",
    "poll.delete_channel_param": "Please provide channel parameter: /deletechannelpoll channel\n\nExample: /deletechannelpoll examplechannel",
    "poll.delete_error": "Error deleting channel poll configuration: {error}",
    # ========== Poll Regeneration ==========
    "poll_regen.feature_disabled": "❌ This feature is disabled",
    "poll_regen.invalid_format": "❌ Invalid request format",
    "poll_regen.data_not_found": "❌ Poll data not found",
    "poll_regen.already_voted": "⚠️ You have already voted (Current: {count}/{threshold})",
    "poll_regen.request_button": "👍 Request Regeneration ({count}/{threshold})",
    "poll_regen.admin_button": "🔄 Regenerate Poll (Admin)",
    "poll_regen.vote_success": "✅ You have successfully voted ({count}/{threshold})",
    "poll_regen.admin_only": "❌ Only administrators can regenerate polls",
    "poll_regen.regen_in_progress": "⏳ Regenerating poll, please wait...",
    "poll_regen.threshold_reached": "🎉 Vote threshold reached: {count}/{threshold}, starting automatic poll regeneration",
    "poll_regen.current_progress": "Current vote progress: {count}/{threshold}",
    "poll_regen.poll_deleted": "✅ Successfully deleted old poll and buttons",
    "poll_regen.delete_warning": "Error deleting old messages",
    "poll_regen.generating": "Starting to generate new poll content...",
    "poll_regen.generated": "✅ New poll generated successfully",
    "poll_regen.sent_to_channel": "✅ New poll sent to channel",
    "poll_regen.sent_to_discussion": "✅ New poll sent to discussion group",
    "poll_regen.updated_storage": "✅ Updated poll ID in storage",
    "poll_regen.no_discussion": "Channel has no linked discussion group",
    "poll_regen.no_forward_id": "Forwarded message ID not found in storage, cannot regenerate poll",
    "poll_regen.using_forward_id": "Using stored forwarded message ID",
    "poll_regen.default_question": "Channel Survey",
    # ========== Report Send Configuration ==========
    "report.invalid_value": "Invalid value: {value}\n\nAvailable values: true, false, 1, 0, yes, no",
    "report.set_success": "Successfully changed report send to source channel setting to: {value}\n\nCurrent status: {status}",
    "report.current_status": "Current report send to source channel setting: {value}\n\nCurrent status: {status}\n\nUsage: /setsendtosource true|false",
    "report.set_error": "Error setting report send to source channel option: {error}",
    # ========== Cache Management ==========
    "cache.admin_only": "❌ Only administrators can clear cache",
    "cache.clear_channel_success": "✅ Successfully cleared discussion group ID cache for channel {channel}",
    "cache.clear_all_success": "✅ Successfully cleared all discussion group ID cache (total {count} entries)",
    "cache.clear_error": "❌ Error clearing cache: {error}",
    # ========== History Records ==========
    "history.no_records": "❌ Channel {channel} has no summary history records",
    "history.all_no_records": "❌ No summary history records",
    "history.title_suffix": "Summary History",
    "history.found_count": "Found {count} records, showing most recent {display}:\n\n",
    "history.messages": "messages",
    "history.times": "times",
    "history.total_summaries": "Total summaries",
    "history.total_messages": "Total messages processed",
    "history.avg_per_summary": "Average per summary",
    "history.last_summary": "Last summary",
    "history.title": "📜 **Summary History Records**\n\nChannel: {channel}\nFound {count} records",
    "history.all_title": "📜 **Summary History Records**\n\nChannel: {channel}\nFound {count} records (showing most recent 10)",
    "history.item": "{index}. {type} - {time}\n   Message count: {count}{link}\n   {preview}",
    "history.days_invalid": "Days must be a number, for example: /history channel1 30",
    "history.processing": "Processed messages",
    "history.key_points": "Key points",
    "history.tip_export": "💡 Tip: Use /export to export complete records",
    "history.query_error": "Error querying history records: {error}",
    "history.exporting": "📦 Exporting history records, please wait...",
    "history.export_done": "✅ Export successful\nFormat: {format}\nFile: {filename}",
    "history.export_no_data": "❌ Export failed: No data to export or unsupported format",
    "history.overview_title": "📊 **Channel Statistics Overview**",
    "history.ranking_title": "🏆 **Channel Ranking** (by summary count)",
    "history.ranking_item": "{index}. **{name}**\n   Summaries: {summary_count} | Messages: {total_messages} | Average: {avg_messages} messages/summary",
    "history.overall_stats": "📈 **Overall Statistics**",
    "history.overall_summary": "• Total summaries: {total}\n• Total messages processed: {messages}\n• Number of channels: {channels}",
    "history.time_distribution": "⏰ **Time Distribution**",
    "history.week_count": "• This week: {count}",
    "history.month_count": "• This month: {count}",
    "history.db_info": "💾 **Database Information**",
    "history.db_records": "• Records: {count}",
    "history.minutes_ago": "{minutes} minutes ago",
    "history.hours_ago": "{hours} hours ago",
    "history.days_ago": "{days} days ago",
    "history.type_daily": "Daily Report",
    "history.type_weekly": "Weekly Report",
    "history.type_manual": "Manual Summary",
    "history.unknown_time": "Unknown time",
    "history.view_full": "\n   📝 View full: https://t.me/{channel}/{msg_id}",
    "history.stats_no_data": "❌ Channel {channel} has no statistical data",
    "history.stats_title": "📊 **Channel Statistics** - {channel}",
    "history.stats_summary": "📈 Overall Statistics\n• Total summaries: {total}\n• Total messages processed: {messages}",
    "history.stats_type": "\n📋 Type Distribution",
    "history.stats_type_item": "• {name}: {count} times",
    "history.stats_last_summary": "\n⏰ Last summary: {time}",
    "history.stats_ranking_title": "\n🏆 Channel Ranking",
    "history.stats_ranking_item": "{index}. {channel} - {summary_count} summaries, {total_messages} messages, average {avg_messages} messages/summary",
    "history.stats_error": "Error getting statistics: {error}",
    "history.export_error": "❌ Error exporting history records: {error}",
    "history.invalid_format": "❌ Unsupported export format: {format}\nSupported formats: json, csv, md",
    # ========== Messaging Related ==========
    "messaging.channel_title_fallback": "Channel Weekly Summary",
    "messaging.send_success": "✅ Summary successfully sent to channel {channel}",
    "messaging.send_forbidden": "⚠️ **Channel Send Failed**\n\nChannel: {channel}\nReason: Bot does not have permission to send messages in this channel\n\nPossible reasons:\n• Channel is set to discussion-only mode\n• Bot has not been granted message sending permission\n• Channel has not enabled bot functionality\n\nSuggestion: Check channel administrator permission settings\n\n📊 **Summary content:**",
    "messaging.send_error": "❌ Failed to send report to channel {channel}:\n{error}",
    # ========== AI Configuration ==========
    "aicfg.title": "🤖 **Current AI Configuration**",
    "aicfg.api_key": "• API Key: {value}",
    "aicfg.base_url": "• Base URL: {value}",
    "aicfg.model": "• Model: {value}",
    "aicfg.not_set": "Not set",
    "aicfg.setting": "Setting AI configuration...",
    "aicfg.set_prompt": "Please send the following AI configuration parameters in order, or send /skip to skip:\n\n1. API Key\n2. Base URL\n3. Model\n\nSend /cancel to cancel setting",
    "aicfg.cancel": "AI configuration setting cancelled",
    "aicfg.cancelled": "AI configuration setting cancelled",
    "aicfg.in_progress": "You are currently setting AI configuration. Please complete the current configuration or send /cancel to cancel, then execute other commands",
    "aicfg.api_key_set": "API Key has been set to: {key}\n\nPlease send Base URL, or send /skip to skip",
    "aicfg.base_url_set": "Base URL has been set to: {url}\n\nPlease send Model, or send /skip to skip",
    "aicfg.updated": "AI configuration has been updated:\n\n",
    "aicfg.completed": "AI configuration has been completed, current configuration:\n\n",
    "aicfg.saved": "AI configuration has been saved to file",
    "aicfg.set_error": "Error setting AI configuration: {error}",
    "aicfg.usage": "Use /setaicfg command to set custom AI configuration\nFormat: /setaicfg <api_key> <base_url> <model>",
    # ========== Prompt Management ==========
    "prompt.current_title": "📝 **Current Prompt**",
    "prompt.current_content": "\n\nContent:\n```\n{content}\n```",
    "prompt.setting": "Setting prompt...",
    "prompt.set_success": "Prompt has been successfully updated",
    "prompt.cancel": "Prompt setting cancelled",
    "prompt.set_error": "Error setting prompt: {error}",
    "prompt.error_command": "Please send the prompt content, do not send commands. To cancel, send the command again.",
    "prompt.poll_current_title": "📝 **Current Poll Prompt**",
    "prompt.poll_setting": "Setting poll prompt...",
    "prompt.poll_set_success": "Poll prompt has been successfully updated",
    "prompt.poll_cancel": "Poll prompt setting cancelled",
    "prompt.poll_set_error": "Error setting poll prompt: {error}",
    # ========== Changelog ==========
    "changelog.caption": "📄 Complete changelog file for the project",
    "changelog.not_found": "Changelog file {filename} not found",
    "changelog.send_error": "Error sending changelog file: {error}",
    # ========== Day Mapping ==========
    "day.mon": "Monday",
    "day.tue": "Tuesday",
    "day.wed": "Wednesday",
    "day.thu": "Thursday",
    "day.fri": "Friday",
    "day.sat": "Saturday",
    "day.sun": "Sunday",
    "day.monday": "Monday",
    "day.tuesday": "Tuesday",
    "day.wednesday": "Wednesday",
    "day.thursday": "Thursday",
    "day.friday": "Friday",
    "day.saturday": "Saturday",
    "day.sunday": "Sunday",
    "day.mondays": "Monday, Tuesday",
    "day.tuesdays": "Tuesday, Wednesday",
    "day.wednesdays": "Wednesday, Thursday",
    "day.thursdays": "Thursday, Friday",
    "day.fridays": "Friday, Saturday",
    "day.saturdays": "Saturday, Sunday",
    "day.sundays": "Sunday, Monday",
    # ========== Status Description ==========
    "status.enabled": "Enabled",
    "status.disabled": "Disabled",
    "status.on": "On",
    "status.off": "Off",
    # ========== Date/Time Related ==========
    "date.weekday.monday": "Monday",
    "date.weekday.tuesday": "Tuesday",
    "date.weekday.wednesday": "Wednesday",
    "date.weekday.thursday": "Thursday",
    "date.weekday.friday": "Friday",
    "date.weekday.saturday": "Saturday",
    "date.weekday.sunday": "Sunday",
    "date.frequency.daily": "Daily",
    "date.frequency.weekly": "Weekly",
    # ========== Poll Related (Supplement) ==========
    "poll.generating": "Generating poll content...",
    "poll.default_question": "What do you think about this week's summary?",
    "poll.default_options.0": "Very satisfied",
    "poll.default_options.1": "Satisfied",
    "poll.default_options.2": "Average",
    "poll.default_options.3": "Needs improvement",
    "poll.send_success": "✅ Poll sent",
    "poll.send_failed": "❌ Poll send failed",
    "poll.waiting_forward": "⏳ Waiting for channel message to be forwarded to discussion group...",
    "poll.forward_timeout": "⏱️ Waiting for forward message timeout (10 seconds), possibly delayed or failed",
    "poll.no_discussion_group": "⚠️ Channel {channel} has no linked discussion group, cannot send poll to comments",
    "poll.bot_not_in_discussion": "⚠️ Bot not in discussion group {group_id} or no permission",
    # ========== Summary Type ==========
    "summary_type.daily": "Daily Report",
    "summary_type.weekly": "Weekly Report",
    "summary_type.manual": "Manual Summary",
    # ========== Schedule Format Header ==========
    "schedule.format_header": "\nUsage format:\n",
    # ========== Poll Timeout Fallback ==========
    "poll.timeout_fallback": "📊 **Poll: {question}**\n\n{options}",
    # ========== Q&A Bot Control ==========
    "qabot.status_title": "📊 **Q&A Bot Status**",
    "qabot.status_running": "🟢 Running",
    "qabot.status_stopped": "🔴 Stopped",
    "qabot.status_not_running": "Not running",
    "qabot.status_uptime": "Uptime",
    "qabot.status_pid": "Process ID",
    "qabot.status_feature_enabled": "Feature Switch",
    "qabot.status_enabled": "✅ Enabled",
    "qabot.status_disabled": "❌ Disabled",
    "qabot.status_token_configured": "Token Config",
    "qabot.status_token_configured_yes": "✅ Configured",
    "qabot.status_token_configured_no": "❌ Not configured",
    "qabot.status_tip_start": "💡 **Tip**: Use `/qa_start` to start Q&A Bot",
    "qabot.stats_title": "📈 **Statistics**",
    "qabot.stats_unavailable": "⚠️ Statistics temporarily unavailable",
    "qabot.management_commands": "🔧 **Management Commands**",
    "qabot.already_running": "⚠️ Q&A Bot is already running (PID: {pid})",
    "qabot.not_enabled": "❌ Q&A Bot is not enabled (QA_BOT_ENABLED=False)",
    "qabot.token_not_configured": "❌ QA_BOT_TOKEN not configured, cannot start",
    "qabot.starting": "⏳ Starting Q&A Bot...",
    "qabot.start_success": "✅ {message}",
    "qabot.tip_view_status": "💡 Use `/qa_status` to view status",
    "qabot.stopping": "⏳ Stopping Q&A Bot...",
    "qabot.stop_success": "✅ {message}",
    "qabot.not_running": "⚠️ Q&A Bot is not running, nothing to stop",
    "qabot.restarting": "⏳ Restarting Q&A Bot...",
    "qabot.restart_success": "✅ {message}",
    "qabot.detailed_stats_title": "📊 **Q&A Bot Detailed Statistics**",
    "qabot.running_status": "**Running Status**",
    "qabot.stats_running": "• Status: 🟢 Running",
    "qabot.stats_stopped": "• Status: 🔴 Stopped",
    "qabot.user_stats": "**User Statistics**",
    "qabot.query_stats": "**Query Statistics**",
    "qabot.subscription_stats": "**Subscription Statistics**",
    "qabot.request_stats": "**Request Statistics**",
    "qabot.top_users": "**🏆 Top Active Users (Top 10)**",
    "qabot.channel_distribution": "**📢 Channel Subscription Distribution**",
    "qabot.stats_error": "⚠️ Unable to fetch detailed statistics",
    "qabot.tip_view_brief": "💡 Use `/qa_status` to view brief status",
    "qabot.total_users": "• Total users: {count}",
    "qabot.active_users": "• Active users (7 days): {count}",
    "qabot.new_users_today": "• New users today: {count}",
    "qabot.queries_today": "• Queries today: {count}",
    "qabot.queries_week": "• Queries this week: {count}",
    "qabot.total_queries": "• Total queries: {count}",
    "qabot.total_subscriptions": "• Total subscriptions: {count}",
    "qabot.active_subscriptions": "• Active subscriptions: {count}",
    "qabot.pending_requests": "• Pending requests: {count}",
    "qabot.completed_requests_today": "• Completed requests today: {count}",
    "qabot.total_requests": "• Total requests: {count}",
    "qabot.user_rank_item": "{index}. {name} - {count} queries",
    "qabot.channel_sub_item": "• {channel}: {count} subscriptions",
    # ========== QA Bot Control Command Descriptions ==========
    "qabot.cmd.qa_start": "Start Q&A Bot",
    "qabot.cmd.qa_stop": "Stop Q&A Bot",
    "qabot.cmd.qa_restart": "Restart Q&A Bot",
    "qabot.cmd.qa_stats": "View detailed statistics",
    # ========== QA Bot Process Messages ==========
    "qabot.started": "Q&A Bot started successfully (PID: {pid})",
    "qabot.stopped": "Q&A Bot stopped successfully (PID: {pid})",
    "qabot.force_stopped": "Q&A Bot force stopped",
    "qabot.not_running_short": "Q&A Bot not running",
    "qabot.restarted": "Q&A Bot restarted successfully (new PID: {pid})",
    "qabot.running_normal": "Q&A Bot running normally (PID: {pid}, Uptime: {uptime}s)",
    # ========== QA Bot Auto Recovery ==========
    "qabot.auto_restart": "⚠️ **Q&A Bot Auto-Restart**",
    "qabot.process_not_running": "Q&A Bot process not running, needs auto-restart",
    "qabot.attempting_recovery": "Attempting auto-recovery...",
    "qabot.recovered": "✅ **Q&A Bot Recovered**\n\nQ&A Bot restarted successfully (new PID: {pid})",
    "qabot.recovery_failed": "❌ **Q&A Bot Auto-Recovery Failed**\n\n{message}\n\nPlease check manually and restart",
    # ========== Database Migration ==========
    "database.migrate.checking": "Checking database migration readiness...",
    "database.migrate.check_result": "Database Migration Check Results",
    "database.migrate.sqlite_found": "SQLite database found",
    "database.migrate.sqlite_not_found": "SQLite database not found",
    "database.migrate.total_records": "Total Records",
    "database.migrate.table_details": "Table Details",
    "database.migrate.mysql_configured": "MySQL configured",
    "database.migrate.mysql_not_configured": "MySQL not configured",
    "database.migrate.mysql_connected": "MySQL connection successful",
    "database.migrate.mysql_connect_failed": "MySQL connection failed",
    "database.migrate.status": "Status",
    "database.migrate.ready": "Ready to migrate",
    "database.migrate.not_ready": "Not ready to migrate",
    "database.migrate.reason": "Reason",
    "database.migrate.can_start": "Can start migration",
    "database.migrate.check_failed": "Check failed",
    "database.migrate.starting": "Starting database migration",
    "database.migrate.please_wait": "Migration may take a few minutes, please be patient",
    "database.migrate.check_progress": "Check progress",
    "database.migrate.already_in_progress": "Migration is in progress, please wait",
    "database.migrate.success": "Database migration successful",
    "database.migrate.failed": "Database migration failed",
    "database.migrate.migrated": "Migrated",
    "database.migrate.failed_count": "Failed",
    "database.migrate.verification_passed": "Data verification passed",
    "database.migrate.verification_warning": "Data verification warning, please check record count",
    "database.migrate.backup_location": "Backup location",
    "database.migrate.next_step": "Next steps",
    "database.migrate.step1": "Modify DATABASE_TYPE=mysql in .env file",
    "database.migrate.step2": "Restart bot to use MySQL database",
    "database.migrate.step3": "Delete SQLite backup file after confirming normal operation",
    "database.migrate.error": "Error",
    "database.migrate.current_status": "Current migration status",
    "database.migrate.progress": "Progress",
    "database.migrate.message": "Message",
    "database.migrate.table_stats": "Table statistics",
    "database.migrate.status_failed": "Failed to get status",
    "database.migrate.no_migration_in_progress": "No migration in progress",
    "database.startup_old_database": "⚠️ **Old Database Detected**\n\nCurrently using SQLite database.\n\n💡 Recommend migrating to MySQL for better performance and concurrency.\n\nUse /migrate_check to check migration readiness",
    "database.migrate.mysql_has_data": "MySQL database already contains data, please confirm if you want to overwrite",
    "database.migrate.check_passed": "Migration conditions check passed",
    "database.migrate.mysql_connect_error": "MySQL connection failed",
    "database.migrate.mysql_incomplete": "MySQL configuration incomplete",
    "database.migrate.check_general_error": "Check failed",
    "database.migrate.not_initialized": "Database not initialized, please call check_migration_ready() first",
    "database.migrate.start_migrating": "Start migrating",
    "database.migrate.migrating_table": "Migrating table",
    "database.migrate.migration_complete": "Migration completed successfully",
    "database.migrate.migration_failed": "Migration failed",
    "database.migrate.auto_switch": "✅ Automatically switched to MySQL database",
    "database.migrate.restart_required": "⚠️ Bot restart required to use MySQL database",
    "database.migrate.restart_command": "📝 Restart command",
    "database.migrate.manual_switch_required": "⚠️ Auto-switch failed, manual switch required",
    "database.migrate.manual_switch_steps": "📝 Manual switch steps",
    "database.migrate.sqlite_deleted": "✅ Original SQLite database file deleted",
    "database.migrate.sqlite_delete_failed": "⚠️ Failed to delete SQLite database file",
    "database.migrate.auto_restart_required": "🔄 Bot will auto-restart to apply MySQL database configuration",
    "database.migrate.restart_in_3s": "✅ Auto-restart in 3 seconds, MySQL database will be used after restart",
    # ========== Database Clear ==========
    "database.clear.warning": "⚠️ Dangerous Operation: Clear MySQL Database",
    "database.clear.current_data": "Current data statistics",
    "database.clear.total_summaries": "Total summaries",
    "database.clear.irreversible": "🚨 This operation is irreversible! All data will be permanently deleted!",
    "database.clear.confirm_instruction": "Please confirm carefully before executing the following command",
    "database.clear.confirm_command": "Confirm clear",
    "database.clear.cancel_command": "Cancel operation",
    "database.clear.not_mysql": "Currently not using MySQL database",
    "database.clear.current_type": "Current database type",
    "database.clear.only_mysql": "This command only supports MySQL database",
    "database.clear.prepare_failed": "Failed to prepare database clear",
    "database.clear.clearing": "Clearing database",
    "database.clear.completed": "Database clear completed",
    "database.clear.cleared_tables": "Cleared tables",
    "database.clear.failed_tables": "Failed tables",
    "database.clear.note": "💡 Table structure has been preserved, can continue to use",
    "database.clear.failed": "Database clear failed",
    "database.clear.no_pending": "No pending clear operation",
    "database.clear.cancelled": "Database clear operation cancelled",
    # ========== Comment Welcome Message ==========
    "comment_welcome.message": "🌸Comments section is now open, come and grab a seat~",
    "comment_welcome.button": "Request Weekly Summary",
    "comment_welcome.already_requested": "Working on the summary, please wait",
    "comment_welcome.request_sent": "Your request has been received, we will process it ASAP",
    # ========== Comment Welcome Config ==========
    "comment_welcome.config.title": "📢 Comment Welcome Configuration",
    "comment_welcome.config.channel": "Channel {channel}",
    "comment_welcome.config.status": "• Status",
    "comment_welcome.config.message": "• Welcome Message",
    "comment_welcome.config.button": "• Button Text",
    "comment_welcome.config.action": "• Button Action",
    "comment_welcome.config.enabled": "✅ Enabled",
    "comment_welcome.config.disabled": "❌ Disabled",
    "comment_welcome.config.no_config": "💡 Channel not configured, using default settings",
    "comment_welcome.config.all_title": "Comment Welcome Configuration for All Channels",
    "comment_welcome.config.set_success": "✅ Successfully set comment welcome configuration for channel {channel}",
    "comment_welcome.config.set_failed": "❌ Setting failed: {error}",
    "comment_welcome.config.delete_success": "✅ Successfully deleted comment welcome configuration for channel {channel}",
    "comment_welcome.config.delete_failed": "❌ Deletion failed: {error}",
    "comment_welcome.config.invalid_params": "❌ Invalid parameters",
    "comment_welcome.config.usage": 'Usage: /setcommentwelcome <channel> <enabled> [welcome_message] [button_text]\n\nExamples:\n/setcommentwelcome channel1 true\n/setcommentwelcome channel1 true "Welcome to comments" "Request Summary"\n/setcommentwelcome channel1 false',
    # ========== Update Command ==========
    "update.checking": "🔄 Checking for updates...",
    "update.latest": "✅ Already up to date (version {version})",
    "update.new_found": "✅ New version found: {remote} (current: {local}), updating...",
    "update.updating": "🔄 Updating code...",
    "update.installing_deps": "📦 Installing dependencies...",
    "update.success": "✅ Update completed, restarting...",
    "update.error": "❌ Update failed: {error}",
    "update.no_git": "❌ Not a Git repository",
    "update.git_error": "❌ Git operation failed: {error}",
    "update.deps_error": "❌ Dependency installation failed: {error}",
    # ========== Database Auto Fallback ==========
    "database.fallback.mysql_failed": "❌ MySQL Connection Failed",
    "database.fallback.error": "Error Details",
    "database.fallback.host": "MySQL Host",
    "database.fallback.port": "Port",
    "database.fallback.database": "Database",
    "database.fallback.user": "User",
    "database.fallback.auto_switching": "🔄 Auto-fallback to SQLite...",
    "database.fallback.success": "✅ Automatically switched to SQLite database",
    "database.fallback.config_updated": "✅ Configuration file updated",
    "database.fallback.config_error": "⚠️ Failed to update configuration file, please manually modify DATABASE_TYPE=sqlite in data/.env",
    "database.fallback.tip": "💡 SQLite database is ready, bot will continue running",
    "database.fallback.manual_required": "⚠️ Manual operation required",
    "database.fallback.manual_steps": "Please follow these steps:\n1. Modify data/.env file\n2. Change DATABASE_TYPE=mysql to DATABASE_TYPE=sqlite\n3. Restart the bot",
}


class I18nManager:
    """国际化管理器（单例模式）"""

    _instance: Optional["I18nManager"] = None

    def __new__(cls) -> "I18nManager":
        """实现单例模式"""
        if cls._instance is None:
            cls._instance = super().__new__(cls)
            cls._instance._initialized = False
        return cls._instance

    def __init__(self):
        """初始化国际化管理器"""
        if self._initialized:
            return

        self._initialized = True
        self._current_language = "zh-CN"  # 默认语言
        self._supported_languages = ["zh-CN", "en-US"]

        # 加载翻译文本
        self._messages: dict[str, dict[str, str]] = {"zh-CN": MESSAGE_ZH_CN, "en-US": MESSAGE_EN_US}

        logger.info(f"国际化管理器初始化完成，当前语言：{self._current_language}")

    def get_language(self) -> str:
        """获取当前语言

        Returns:
            str: 当前语言代码（如 'zh-CN'）
        """
        return self._current_language

    def set_language(self, language: str) -> bool:
        """设置当前语言

        Args:
            language: 语言代码（如 'zh-CN' 或 'en-US'）

        Returns:
            bool: 是否成功设置语言
        """
        if language not in self._supported_languages:
            logger.warning(f"不支持的语言：{language}，支持的语言：{self._supported_languages}")
            return False

        old_language = self._current_language
        self._current_language = language
        logger.info(f"语言已从 {old_language} 更改为 {language}")
        return True

    def get_supported_languages(self) -> list:
        """获取支持的语言列表

        Returns:
            list: 支持的语言代码列表
        """
        return self._supported_languages.copy()

    def get_text(self, key: str, **kwargs) -> str:
        """获取指定 key 的翻译文本

        支持变量插值和回退机制：
        1. 首先尝试从当前语言获取
        2. 如果当前语言不存在该 key，回退到 zh-CN
        3. 如果回退后仍不存在，返回 key 本身

        Args:
            key: 翻译文本的 key
            **kwargs: 用于文本插值的变量

        Returns:
            str: 翻译后的文本
        """
        # 获取当前语言的翻译
        message = self._messages.get(self._current_language, {}).get(key)

        # 如果当前语言没有该 key，回退到中文
        if message is None:
            logger.debug(f"key '{key}' 在语言 '{self._current_language}' 中不存在，回退到 zh-CN")
            message = self._messages.get("zh-CN", {}).get(key)

        # 如果回退后仍不存在，返回 key 本身
        if message is None:
            logger.warning(f"key '{key}' 在所有语言中都不存在，返回 key 本身")
            return key

        # 支持变量插值
        try:
            if kwargs:
                return message.format(**kwargs)
            return message
        except (KeyError, ValueError) as e:
            logger.error(f"文本插值失败 (key={key}, kwargs={kwargs}): {e}")
            return message


# 全局单例实例
_i18n_manager = I18nManager()


def get_language() -> str:
    """获取当前语言（快捷函数）"""
    return _i18n_manager.get_language()


def set_language(language: str) -> bool:
    """设置当前语言（快捷函数）

    Args:
        language: 语言代码

    Returns:
        bool: 是否成功设置
    """
    return _i18n_manager.set_language(language)


def get_supported_languages() -> list:
    """获取支持的语言列表（快捷函数）"""
    return _i18n_manager.get_supported_languages()


def get_text(key: str, **kwargs) -> str:
    """获取翻译文本（快捷函数）

    Args:
        key: 翻译文本的 key
        **kwargs: 用于文本插值的变量

    Returns:
        str: 翻译后的文本
    """
    return _i18n_manager.get_text(key, **kwargs)


def t(key: str, **kwargs) -> str:
    """获取翻译文本的简写别名

    Args:
        key: 翻译文本的 key
        **kwargs: 用于文本插值的变量

    Returns:
        str: 翻译后的文本
    """
    return get_text(key, **kwargs)
