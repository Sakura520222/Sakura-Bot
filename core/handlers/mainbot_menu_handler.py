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
主 Bot 内联菜单回调处理器。
"""

import logging
from collections.abc import Awaitable, Callable

from core.commands.ai_config_commands import handle_show_ai_config
from core.commands.channel_commands import handle_show_channels
from core.commands.guard_commands import handle_guard
from core.commands.other_commands import (
    handle_help,
    handle_show_channel_poll,
    handle_show_channel_schedule,
)
from core.commands.prompt_commands import handle_show_prompt
from core.commands.qa_control_commands import (
    handle_qa_restart,
    handle_qa_start,
    handle_qa_stats,
    handle_qa_status,
    handle_qa_stop,
)
from core.commands.summary_commands import handle_manual_summary
from core.history_handlers import handle_history, handle_stats
from core.telegram.mainbot_keyboards import (
    CALLBACK_PREFIX,
    CMD_CHANNELS,
    CMD_CONFIG_HELP,
    CMD_FORWARDING_HELP,
    CMD_FORWARDING_STATS,
    CMD_FORWARDING_STATUS,
    CMD_GUARD,
    CMD_HELP,
    CMD_HISTORY,
    CMD_QA_RESTART,
    CMD_QA_START,
    CMD_QA_STATS,
    CMD_QA_STATUS,
    CMD_QA_STOP,
    CMD_SHOW_AI_CONFIG,
    CMD_SHOW_POLL,
    CMD_SHOW_PROMPT,
    CMD_SHOW_SCHEDULE,
    CMD_STATS,
    CMD_SUMMARY,
    CONFIG_HELP_TEXT,
    MENU_CONFIG,
    MENU_MAIN,
    build_mainbot_menu_keyboard,
    build_menu_text,
)

logger = logging.getLogger(__name__)

CommandHandler = Callable[[object], Awaitable[None]]

_COMMAND_TEXTS = {
    CMD_GUARD: "/guard",
    CMD_HELP: "/help",
    CMD_SUMMARY: "/summary",
    CMD_CHANNELS: "/showchannels",
    CMD_HISTORY: "/history",
    CMD_STATS: "/stats",
    CMD_QA_STATUS: "/qa_status",
    CMD_QA_START: "/qa_start",
    CMD_QA_STOP: "/qa_stop",
    CMD_QA_RESTART: "/qa_restart",
    CMD_QA_STATS: "/qa_stats",
    CMD_SHOW_PROMPT: "/showprompt",
    CMD_SHOW_AI_CONFIG: "/showaicfg",
    CMD_SHOW_SCHEDULE: "/showchannelschedule",
    CMD_SHOW_POLL: "/channelpoll",
    CMD_FORWARDING_STATUS: "/forwarding",
    CMD_FORWARDING_STATS: "/forwarding_stats",
    CMD_FORWARDING_HELP: "/forwarding_help",
}

_COMMAND_HANDLERS: dict[str, CommandHandler] = {
    CMD_GUARD: handle_guard,
    CMD_HELP: handle_help,
    CMD_SUMMARY: handle_manual_summary,
    CMD_CHANNELS: handle_show_channels,
    CMD_HISTORY: handle_history,
    CMD_STATS: handle_stats,
    CMD_QA_STATUS: handle_qa_status,
    CMD_QA_START: handle_qa_start,
    CMD_QA_STOP: handle_qa_stop,
    CMD_QA_RESTART: handle_qa_restart,
    CMD_QA_STATS: handle_qa_stats,
    CMD_SHOW_PROMPT: handle_show_prompt,
    CMD_SHOW_AI_CONFIG: handle_show_ai_config,
    CMD_SHOW_SCHEDULE: handle_show_channel_schedule,
    CMD_SHOW_POLL: handle_show_channel_poll,
}


class MainBotMenuCommandEvent:
    """让回调事件以普通命令事件形式复用现有处理器。"""

    def __init__(self, source_event, client, text: str):
        self._source_event = source_event
        self.sender_id = source_event.sender_id
        self.chat_id = getattr(source_event, "chat_id", None)
        self.text = text
        self.raw_text = text
        self.message = text
        self.client = client or getattr(source_event, "client", None)

    async def reply(self, *args, **kwargs):
        """将命令回复转为 callback 消息后续回复。"""
        return await self._source_event.respond(*args, **kwargs)


async def handle_mainbot_menu_callback(event, client=None) -> None:
    """处理主 Bot 内联菜单按钮点击。"""
    answered = False
    try:
        await event.answer()
        answered = True

        data = _decode_callback_data(event.data)
        if not data:
            return

        parts = data.split(":", maxsplit=2)
        if len(parts) != 3 or parts[0] != CALLBACK_PREFIX:
            return

        action, value = parts[1], parts[2]
        logger.info(f"收到主菜单回调: action={action}, value={value}, sender={event.sender_id}")

        if action == "menu":
            await _show_menu(event, value)
            return

        if action == "cmd":
            await _dispatch_command(event, client, value)
            return

        await event.edit("⚠️ 未知的菜单操作", buttons=build_mainbot_menu_keyboard(MENU_MAIN))

    except Exception as e:
        logger.error(f"处理主菜单回调失败: {type(e).__name__}: {e}", exc_info=True)
        if not answered:
            try:
                await event.answer("处理按钮操作失败，请稍后再试", alert=True)
            except Exception:
                pass


def _decode_callback_data(raw_data) -> str:
    """解码 Telethon callback data。"""
    if isinstance(raw_data, bytes):
        return raw_data.decode(errors="backslashreplace")
    if isinstance(raw_data, str):
        return raw_data
    return ""


async def _show_menu(event, menu: str) -> None:
    """编辑当前消息为指定菜单页。"""
    await event.edit(
        build_menu_text(menu),
        buttons=build_mainbot_menu_keyboard(menu),
        link_preview=False,
    )


async def _dispatch_command(event, client, command_key: str) -> None:
    """分发菜单按钮到现有命令处理器或安全提示。"""
    if command_key == CMD_CONFIG_HELP:
        await event.respond(
            CONFIG_HELP_TEXT,
            buttons=build_mainbot_menu_keyboard(MENU_CONFIG),
            link_preview=False,
        )
        return

    if command_key in {
        CMD_FORWARDING_STATUS,
        CMD_FORWARDING_STATS,
        CMD_FORWARDING_HELP,
    }:
        await _dispatch_forwarding_command(event, client, command_key)
        return

    handler = _COMMAND_HANDLERS.get(command_key)
    if not handler:
        await event.respond("⚠️ 未找到对应功能", buttons=build_mainbot_menu_keyboard(MENU_MAIN))
        return

    command_event = MainBotMenuCommandEvent(
        event,
        client,
        _COMMAND_TEXTS.get(command_key, f"/{command_key}"),
    )
    try:
        await handler(command_event)
    except Exception as e:
        logger.error(
            f"处理菜单命令 {command_key} 失败: {type(e).__name__}: {e}",
            exc_info=True,
        )
        try:
            await event.respond("❌ 处理菜单命令失败，请稍后再试或查看日志")
        except Exception as notify_error:
            logger.error(
                f"发送菜单命令失败提示失败: {type(notify_error).__name__}: {notify_error}",
                exc_info=True,
            )


async def _dispatch_forwarding_command(event, client, command_key: str) -> None:
    """分发频道转发按钮命令。"""
    try:
        from core.commands.forwarding_commands import (
            cmd_forwarding_help,
            cmd_forwarding_stats,
            cmd_forwarding_status,
        )
        from core.forwarding import get_forwarding_handler

        handler = get_forwarding_handler()
        if not handler:
            await event.respond(
                "⚠️ 转发功能未初始化", buttons=build_mainbot_menu_keyboard(MENU_MAIN)
            )
            return

        message = MainBotMenuCommandEvent(
            event,
            client,
            _COMMAND_TEXTS.get(command_key, "/forwarding"),
        )

        if command_key == CMD_FORWARDING_STATUS:
            await cmd_forwarding_status(client, message, handler)
        elif command_key == CMD_FORWARDING_STATS:
            await cmd_forwarding_stats(client, message, handler)
        elif command_key == CMD_FORWARDING_HELP:
            await cmd_forwarding_help(client, message, handler)

    except Exception as e:
        logger.error(f"处理转发菜单命令失败: {type(e).__name__}: {e}", exc_info=True)
        await event.respond("❌ 处理转发菜单命令失败，请查看日志")
