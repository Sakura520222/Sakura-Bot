# Copyright 2026 Sakura-Bot
#
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
#
# - 署名：必须提供本项目的原始来源链接
# - 相同方式共享：衍生作品必须采用相同的许可证
#
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
# 许可证全文：参见 LICENSE 文件

"""
命令注册器

统一管理所有Telegram命令的注册，避免在main.py中重复代码。
"""

import logging
from typing import TYPE_CHECKING

from telethon.events import CallbackQuery, NewMessage

if TYPE_CHECKING:
    from telethon import TelegramClient

from core.commands.ai_config_commands import (
    handle_ai_config_input,
    handle_set_ai_config,
    handle_show_ai_config,
)
from core.commands.auto_poll_commands import (
    handle_delete_auto_poll,
    handle_set_auto_poll,
    handle_show_auto_poll,
)
from core.commands.channel_commands import (
    handle_add_channel,
    handle_delete_channel,
    handle_show_channels,
)
from core.commands.comment_welcome_commands import (
    handle_delete_comment_welcome,
    handle_set_comment_welcome,
    handle_show_comment_welcome,
)
from core.commands.forwarding_commands import (
    cmd_forwarding_add_and_enable,
    cmd_forwarding_add_rule,
    cmd_forwarding_blacklist,
    cmd_forwarding_blacklist_patterns,
    cmd_forwarding_copy_mode,
    cmd_forwarding_default_footer,
    cmd_forwarding_disable,
    cmd_forwarding_enable,
    cmd_forwarding_footer,
    cmd_forwarding_help,
    cmd_forwarding_keywords,
    cmd_forwarding_original_only,
    cmd_forwarding_patterns,
    cmd_forwarding_remove_rule,
    cmd_forwarding_rule_info,
    cmd_forwarding_stats,
    cmd_forwarding_status,
)
from core.commands.other_commands import (
    handle_changelog,
    handle_clear_cache,
    handle_clear_database,
    handle_delete_channel_poll,
    handle_delete_channel_schedule,
    handle_help,
    handle_language,
    handle_pause,
    handle_restart,
    handle_resume,
    handle_set_channel_poll,
    handle_set_channel_schedule,
    handle_set_log_level,
    handle_set_send_to_source,
    handle_show_channel_poll,
    handle_show_channel_schedule,
    handle_show_log_level,
    handle_shutdown,
    handle_start,
    handle_update,
)
from core.commands.prompt_commands import (
    handle_poll_prompt_input,
    handle_prompt_input,
    handle_set_poll_prompt,
    handle_set_prompt,
    handle_show_poll_prompt,
    handle_show_prompt,
)
from core.commands.qa_control_commands import (
    handle_qa_restart,
    handle_qa_start,
    handle_qa_stats,
    handle_qa_status,
    handle_qa_stop,
)
from core.commands.summary_commands import (
    handle_clear_summary_time,
    handle_manual_summary,
)
from core.commands.userbot_commands import (
    handle_userbot_join,
    handle_userbot_leave,
    handle_userbot_list,
    handle_userbot_status,
)
from core.handlers.mainbot_menu_handler import handle_mainbot_menu_callback
from core.handlers.mainbot_request_handler import get_mainbot_request_handler
from core.history_handlers import handle_export, handle_history, handle_stats
from core.services.poll.poll_regeneration_handlers import (
    handle_poll_regeneration_callback,
    handle_vote_regen_request_callback,
)


def _is_submission_review_callback(event) -> bool:
    """判断是否为投稿审核回调。"""
    data = getattr(event, "data", None)
    return isinstance(data, bytes) and data.startswith(
        (
            b"submission_aiopt_",
            b"submission_approve_",
            b"submission_signapprove_",
            b"submission_reject_",
            b"submission_restore_",
        )
    )


class CommandRegistrar:
    """命令注册器 - 统一管理所有命令和回调的注册"""

    def __init__(self):
        self.logger = logging.getLogger(__name__)

    async def register_all_commands(self, client: "TelegramClient") -> None:
        """注册所有命令处理器

        Args:
            client: Telegram客户端实例
        """
        self.logger.info("开始注册命令处理器...")

        # 1. 基础命令
        self._register_basic_commands(client)

        # 2. 核心功能命令
        self._register_core_commands(client)

        # 3. AI配置命令
        self._register_ai_commands(client)

        # 4. 频道管理命令
        self._register_channel_commands(client)

        # 5. 自动化配置命令
        self._register_schedule_commands(client)

        # 6. 投票配置命令
        self._register_poll_commands(client)

        # 7. 系统控制命令
        self._register_system_commands(client)

        # 8. 日志与调试命令
        self._register_debug_commands(client)

        # 9. 历史记录命令
        self._register_history_commands(client)

        # 10. 语言设置命令
        self._register_language_commands(client)

        # 11. 评论区欢迎配置命令
        self._register_comment_welcome_commands(client)

        # 12. 自动趣味投票配置命令
        self._register_auto_poll_commands(client)

        # 13. 问答Bot控制命令
        self._register_qabot_commands(client)

        # 14. 频道消息转发命令
        await self._register_forwarding_commands(client)

        # 15. UserBot命令
        self._register_userbot_commands(client)

        # 16. 输入处理器（非命令消息）
        self._register_input_handlers(client)

        # 17. 回调查询处理器
        self._register_callback_handlers(client)

        self.logger.info("命令处理器注册完成")

    def _register_basic_commands(self, client: "TelegramClient") -> None:
        """注册基础命令"""
        client.add_event_handler(handle_start, NewMessage(pattern="/start|/开始"))
        client.add_event_handler(handle_help, NewMessage(pattern="/help|/帮助"))

    def _register_core_commands(self, client: "TelegramClient") -> None:
        """注册核心功能命令"""
        client.add_event_handler(handle_manual_summary, NewMessage(pattern="/立即总结|/summary"))

    def _register_ai_commands(self, client: "TelegramClient") -> None:
        """注册AI配置命令"""
        client.add_event_handler(
            handle_show_prompt, NewMessage(pattern="/showprompt|/show_prompt|/查看提示词")
        )
        client.add_event_handler(
            handle_set_prompt, NewMessage(pattern="/setprompt|/set_prompt|/设置提示词")
        )
        client.add_event_handler(
            handle_show_poll_prompt,
            NewMessage(pattern="/showpollprompt|/show_poll_prompt|/查看投票提示词"),
        )
        client.add_event_handler(
            handle_set_poll_prompt,
            NewMessage(pattern="/setpollprompt|/set_poll_prompt|/设置投票提示词"),
        )
        client.add_event_handler(
            handle_show_ai_config, NewMessage(pattern="/showaicfg|/show_aicfg|/查看AI配置")
        )
        client.add_event_handler(
            handle_set_ai_config, NewMessage(pattern="/setaicfg|/set_aicfg|/设置AI配置")
        )

    def _register_channel_commands(self, client: "TelegramClient") -> None:
        """注册频道管理命令"""
        client.add_event_handler(
            handle_show_channels, NewMessage(pattern="/showchannels|/show_channels|/查看频道列表")
        )
        client.add_event_handler(
            handle_add_channel, NewMessage(pattern="/addchannel|/add_channel|/添加频道")
        )
        client.add_event_handler(
            handle_delete_channel, NewMessage(pattern="/deletechannel|/delete_channel|/删除频道")
        )

    def _register_schedule_commands(self, client: "TelegramClient") -> None:
        """注册自动化配置命令"""
        client.add_event_handler(
            handle_show_channel_schedule,
            NewMessage(pattern="/showchannelschedule|/show_channel_schedule|/查看频道时间配置"),
        )
        client.add_event_handler(
            handle_set_channel_schedule,
            NewMessage(pattern="/setchannelschedule|/set_channel_schedule|/设置频道时间配置"),
        )
        client.add_event_handler(
            handle_delete_channel_schedule,
            NewMessage(pattern="/deletechannelschedule|/delete_channel_schedule|/删除频道时间配置"),
        )
        client.add_event_handler(
            handle_clear_summary_time,
            NewMessage(pattern="/clearsummarytime|/clear_summary_time|/清除总结时间"),
        )
        client.add_event_handler(
            handle_set_send_to_source,
            NewMessage(pattern="/setsendtosource|/set_send_to_source|/设置报告发送回源频道"),
        )

    def _register_poll_commands(self, client: "TelegramClient") -> None:
        """注册投票配置命令"""
        client.add_event_handler(
            handle_show_channel_poll,
            NewMessage(pattern="/channelpoll|/channel_poll|/查看频道投票配置"),
        )
        client.add_event_handler(
            handle_set_channel_poll,
            NewMessage(pattern="/setchannelpoll|/set_channel_poll|/设置频道投票配置"),
        )
        client.add_event_handler(
            handle_delete_channel_poll,
            NewMessage(pattern="/deletechannelpoll|/delete_channel_poll|/删除频道投票配置"),
        )

    def _register_system_commands(self, client: "TelegramClient") -> None:
        """注册系统控制命令"""
        client.add_event_handler(handle_pause, NewMessage(pattern="/pause|/暂停"))
        client.add_event_handler(handle_resume, NewMessage(pattern="/resume|/恢复"))
        client.add_event_handler(handle_restart, NewMessage(pattern="/restart|/重启"))
        client.add_event_handler(handle_shutdown, NewMessage(pattern="/shutdown|/关机"))

    def _register_debug_commands(self, client: "TelegramClient") -> None:
        """注册日志与调试命令"""
        client.add_event_handler(
            handle_show_log_level, NewMessage(pattern="/showloglevel|/show_log_level|/查看日志级别")
        )
        client.add_event_handler(
            handle_set_log_level, NewMessage(pattern="/setloglevel|/set_log_level|/设置日志级别")
        )
        client.add_event_handler(
            handle_clear_cache, NewMessage(pattern="/clearcache|/clear_cache|/清除缓存")
        )
        client.add_event_handler(handle_clear_database, NewMessage(pattern="/db_clear|/dbclear"))
        client.add_event_handler(handle_changelog, NewMessage(pattern="/changelog|/更新日志"))
        client.add_event_handler(handle_update, NewMessage(pattern="/update|/更新"))

    def _register_history_commands(self, client: "TelegramClient") -> None:
        """注册历史记录命令"""
        client.add_event_handler(handle_history, NewMessage(pattern="/history|/历史"))
        client.add_event_handler(handle_export, NewMessage(pattern="/export|/导出"))
        client.add_event_handler(handle_stats, NewMessage(pattern="/stats|/统计"))

    def _register_language_commands(self, client: "TelegramClient") -> None:
        """注册语言设置命令"""
        client.add_event_handler(handle_language, NewMessage(pattern="/language|/语言"))

    def _register_comment_welcome_commands(self, client: "TelegramClient") -> None:
        """注册评论区欢迎配置命令"""
        client.add_event_handler(
            handle_show_comment_welcome,
            NewMessage(pattern="/showcommentwelcome|/show_comment_welcome|/查看评论区欢迎"),
        )
        client.add_event_handler(
            handle_set_comment_welcome,
            NewMessage(pattern="/setcommentwelcome|/set_comment_welcome|/设置评论区欢迎"),
        )
        client.add_event_handler(
            handle_delete_comment_welcome,
            NewMessage(pattern="/deletecommentwelcome|/delete_comment_welcome|/删除评论区欢迎"),
        )

    def _register_auto_poll_commands(self, client: "TelegramClient") -> None:
        """注册自动趣味投票配置命令"""
        client.add_event_handler(
            handle_show_auto_poll,
            NewMessage(pattern="/showautopoll|/show_auto_poll|/查看自动投票"),
        )
        client.add_event_handler(
            handle_set_auto_poll,
            NewMessage(pattern="/setautopoll|/set_auto_poll|/设置自动投票"),
        )
        client.add_event_handler(
            handle_delete_auto_poll,
            NewMessage(pattern="/deleteautopoll|/delete_auto_poll|/删除自动投票"),
        )

    def _register_qabot_commands(self, client: "TelegramClient") -> None:
        """注册问答Bot控制命令"""
        client.add_event_handler(handle_qa_status, NewMessage(pattern="/qa_status|/qa_状态"))
        client.add_event_handler(handle_qa_start, NewMessage(pattern="/qa_start|/qa_启动"))
        client.add_event_handler(handle_qa_stop, NewMessage(pattern="/qa_stop|/qa_停止"))
        client.add_event_handler(handle_qa_restart, NewMessage(pattern="/qa_restart|/qa_重启"))
        client.add_event_handler(handle_qa_stats, NewMessage(pattern="/qa_stats|/qa_统计"))

    async def _register_forwarding_commands(self, client: "TelegramClient") -> None:
        """注册频道消息转发命令"""
        from core.forwarding import get_forwarding_handler

        async def handle_forwarding_status(event):
            handler = get_forwarding_handler()
            if handler:
                await cmd_forwarding_status(client, event.message, handler)
            else:
                await event.message.reply("转发功能未初始化")

        async def handle_forwarding_quick(event):
            """/forwarding 的智能处理：有参数=添加并启用，无参数=查看状态"""
            handler = get_forwarding_handler()
            if handler:
                await cmd_forwarding_add_and_enable(client, event.message, handler)
            else:
                await event.message.reply("转发功能未初始化")

        async def handle_forwarding_enable(event):
            handler = get_forwarding_handler()
            if handler:
                await cmd_forwarding_enable(client, event.message, handler)
            else:
                await event.message.reply("转发功能未初始化")

        async def handle_forwarding_disable(event):
            handler = get_forwarding_handler()
            if handler:
                await cmd_forwarding_disable(client, event.message, handler)
            else:
                await event.message.reply("转发功能未初始化")

        async def handle_forwarding_stats(event):
            handler = get_forwarding_handler()
            if handler:
                await cmd_forwarding_stats(client, event.message, handler)
            else:
                await event.message.reply("转发功能未初始化")

        async def handle_forwarding_footer(event):
            handler = get_forwarding_handler()
            if handler:
                await cmd_forwarding_footer(client, event.message, handler)
            else:
                await event.message.reply("转发功能未初始化")

        async def handle_forwarding_default_footer(event):
            handler = get_forwarding_handler()
            if handler:
                await cmd_forwarding_default_footer(client, event.message, handler)
            else:
                await event.message.reply("转发功能未初始化")

        async def handle_forwarding_add_rule(event):
            handler = get_forwarding_handler()
            if handler:
                await cmd_forwarding_add_rule(client, event.message, handler)
            else:
                await event.message.reply("转发功能未初始化")

        async def handle_forwarding_remove_rule(event):
            handler = get_forwarding_handler()
            if handler:
                await cmd_forwarding_remove_rule(client, event.message, handler)
            else:
                await event.message.reply("转发功能未初始化")

        async def handle_forwarding_keywords(event):
            handler = get_forwarding_handler()
            if handler:
                await cmd_forwarding_keywords(client, event.message, handler)
            else:
                await event.message.reply("转发功能未初始化")

        async def handle_forwarding_blacklist(event):
            handler = get_forwarding_handler()
            if handler:
                await cmd_forwarding_blacklist(client, event.message, handler)
            else:
                await event.message.reply("转发功能未初始化")

        async def handle_forwarding_patterns(event):
            handler = get_forwarding_handler()
            if handler:
                await cmd_forwarding_patterns(client, event.message, handler)
            else:
                await event.message.reply("转发功能未初始化")

        async def handle_forwarding_blacklist_patterns(event):
            handler = get_forwarding_handler()
            if handler:
                await cmd_forwarding_blacklist_patterns(client, event.message, handler)
            else:
                await event.message.reply("转发功能未初始化")

        async def handle_forwarding_copy_mode(event):
            handler = get_forwarding_handler()
            if handler:
                await cmd_forwarding_copy_mode(client, event.message, handler)
            else:
                await event.message.reply("转发功能未初始化")

        async def handle_forwarding_original_only(event):
            handler = get_forwarding_handler()
            if handler:
                await cmd_forwarding_original_only(client, event.message, handler)
            else:
                await event.message.reply("转发功能未初始化")

        async def handle_forwarding_rule_info(event):
            handler = get_forwarding_handler()
            if handler:
                await cmd_forwarding_rule_info(client, event.message, handler)
            else:
                await event.message.reply("转发功能未初始化")

        async def handle_forwarding_help(event):
            handler = get_forwarding_handler()
            if handler:
                await cmd_forwarding_help(client, event.message, handler)
            else:
                await event.message.reply("转发功能未初始化")

        client.add_event_handler(handle_forwarding_quick, NewMessage(pattern=r"/forwarding\s+.*"))
        client.add_event_handler(handle_forwarding_status, NewMessage(pattern="/forwarding$"))
        client.add_event_handler(handle_forwarding_status, NewMessage(pattern="/转发状态"))
        client.add_event_handler(
            handle_forwarding_enable, NewMessage(pattern="/forwarding_enable|/启用转发")
        )
        client.add_event_handler(
            handle_forwarding_disable, NewMessage(pattern="/forwarding_disable|/禁用转发")
        )
        client.add_event_handler(
            handle_forwarding_stats, NewMessage(pattern="/forwarding_stats|/转发统计")
        )
        client.add_event_handler(
            handle_forwarding_footer, NewMessage(pattern="/forwarding_footer|/转发底栏")
        )
        client.add_event_handler(
            handle_forwarding_default_footer,
            NewMessage(pattern="/forwarding_default_footer|/默认底栏"),
        )
        client.add_event_handler(
            handle_forwarding_add_rule, NewMessage(pattern="/forwarding_add_rule|/添加转发规则")
        )
        client.add_event_handler(
            handle_forwarding_remove_rule,
            NewMessage(pattern="/forwarding_remove_rule|/删除转发规则"),
        )
        client.add_event_handler(
            handle_forwarding_keywords,
            NewMessage(pattern="/forwarding_keywords|/关键词白名单"),
        )
        client.add_event_handler(
            handle_forwarding_blacklist,
            NewMessage(pattern="/forwarding_blacklist|/关键词黑名单"),
        )
        client.add_event_handler(
            handle_forwarding_patterns,
            NewMessage(pattern="/forwarding_patterns|/正则白名单"),
        )
        client.add_event_handler(
            handle_forwarding_blacklist_patterns,
            NewMessage(pattern="/forwarding_blacklist_patterns|/正则黑名单"),
        )
        client.add_event_handler(
            handle_forwarding_copy_mode,
            NewMessage(pattern="/forwarding_copy_mode|/复制模式"),
        )
        client.add_event_handler(
            handle_forwarding_original_only,
            NewMessage(pattern="/forwarding_original_only|/只转发原创"),
        )
        client.add_event_handler(
            handle_forwarding_rule_info,
            NewMessage(pattern="/forwarding_rule_info|/规则详情"),
        )
        client.add_event_handler(
            handle_forwarding_help,
            NewMessage(pattern="/forwarding_help|/转发帮助"),
        )

    def _register_userbot_commands(self, client: "TelegramClient") -> None:
        """注册UserBot命令"""

        async def handle_userbot_status_cmd(event):
            await handle_userbot_status(client, event.message)

        async def handle_userbot_join_cmd(event):
            await handle_userbot_join(client, event.message)

        async def handle_userbot_leave_cmd(event):
            await handle_userbot_leave(client, event.message)

        async def handle_userbot_list_cmd(event):
            await handle_userbot_list(client, event.message)

        client.add_event_handler(
            handle_userbot_status_cmd, NewMessage(pattern="/userbot_status|/userbot_状态")
        )
        client.add_event_handler(
            handle_userbot_join_cmd, NewMessage(pattern="/userbot_join|/userbot_加入")
        )
        client.add_event_handler(
            handle_userbot_leave_cmd, NewMessage(pattern="/userbot_leave|/userbot_离开")
        )
        client.add_event_handler(
            handle_userbot_list_cmd, NewMessage(pattern="/userbot_list|/userbot_列表")
        )

    def _register_input_handlers(self, client: "TelegramClient") -> None:
        """注册输入处理器（非命令消息）"""
        # 只处理非命令消息作为提示词或AI配置输入
        client.add_event_handler(
            handle_prompt_input, NewMessage(func=lambda e: not e.text.startswith("/"))
        )
        client.add_event_handler(
            handle_poll_prompt_input, NewMessage(func=lambda e: not e.text.startswith("/"))
        )
        client.add_event_handler(handle_ai_config_input, NewMessage(func=lambda e: True))

    def _register_callback_handlers(self, client: "TelegramClient") -> None:
        """注册回调查询处理器"""

        # 主 Bot 内联菜单回调
        async def handle_menu_callback(event):
            await handle_mainbot_menu_callback(event, client)

        client.add_event_handler(
            handle_menu_callback,
            CallbackQuery(func=lambda e: e.data and e.data.startswith(b"mainbot:")),
        )
        self.logger.info("主 Bot 内联菜单回调处理器已注册")

        # 投票重新生成回调
        client.add_event_handler(
            handle_poll_regeneration_callback,
            CallbackQuery(func=lambda e: e.data.startswith(b"regen_poll_")),
        )
        self.logger.info("投票重新生成回调处理器已注册")

        # 投票重新生成请求回调
        client.add_event_handler(
            handle_vote_regen_request_callback,
            CallbackQuery(func=lambda e: e.data.startswith(b"request_regen_")),
        )
        self.logger.info("投票重新生成请求回调处理器已注册")

        # 请求处理回调（跨Bot通信）
        request_handler = get_mainbot_request_handler()

        async def handle_request_callback(event):
            await request_handler.handle_callback_query(event, client)

        client.add_event_handler(
            handle_request_callback,
            CallbackQuery(
                func=lambda e: (
                    e.data
                    and (
                        e.data.startswith(b"confirm_summary_")
                        or e.data.startswith(b"reject_summary_")
                    )
                )
            ),
        )
        self.logger.info("请求处理回调处理器已注册")

        # 申请周报总结按钮回调
        from core.handlers.channel_comment_welcome import handle_summary_request_callback

        client.add_event_handler(
            handle_summary_request_callback,
            CallbackQuery(func=lambda e: e.data and e.data.startswith(b"req_summary:")),
        )
        self.logger.info("申请周报总结按钮回调处理器已注册")

        # 投稿审核回调
        from core.handlers.submission_review_handler import get_submission_review_handler

        submission_review_handler = get_submission_review_handler()

        async def handle_submission_callback(event):
            await submission_review_handler.handle_callback(event, client)

        client.add_event_handler(
            handle_submission_callback,
            CallbackQuery(func=_is_submission_review_callback),
        )
        self.logger.info("投稿审核回调处理器已注册")
