# Copyright 2026 Sakura-Bot
#
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可

"""
测试主 Bot 内联菜单功能。
"""

from unittest.mock import AsyncMock, MagicMock, patch

import pytest

from core.commands.other_commands import handle_help, handle_start
from core.handlers import mainbot_menu_handler
from core.handlers.mainbot_menu_handler import handle_mainbot_menu_callback
from core.telegram.mainbot_keyboards import (
    CMD_CHANNELS,
    CMD_FORWARDING_STATUS,
    MENU_BASIC,
    MENU_CONFIG,
    MENU_FORWARDING,
    MENU_MAIN,
    MENU_QA,
    build_callback,
    build_mainbot_menu_keyboard,
)


@pytest.fixture
def mock_event():
    """创建模拟 Telethon 事件。"""
    event = MagicMock()
    event.sender_id = 123456
    event.text = "/start"
    event.data = b""
    event.reply = AsyncMock()
    event.answer = AsyncMock()
    event.edit = AsyncMock()
    event.respond = AsyncMock()
    event.message = MagicMock()
    return event


@pytest.mark.asyncio
async def test_handle_start_contains_inline_menu(mock_event):
    """测试 /start 回复包含主 Bot 内联菜单。"""
    await handle_start(mock_event)

    mock_event.reply.assert_called_once()
    _, kwargs = mock_event.reply.call_args
    assert kwargs.get("buttons") is not None
    assert kwargs.get("link_preview") is False


@pytest.mark.asyncio
async def test_handle_help_contains_inline_menu(mock_event):
    """测试 /help 回复包含主 Bot 内联菜单。"""
    mock_event.text = "/help"

    await handle_help(mock_event)

    mock_event.reply.assert_called_once()
    _, kwargs = mock_event.reply.call_args
    assert kwargs.get("buttons") is not None
    assert kwargs.get("link_preview") is False


@pytest.mark.asyncio
async def test_menu_callback_edits_to_category(mock_event):
    """测试分类菜单按钮会编辑当前消息。"""
    mock_event.data = build_callback("menu", MENU_BASIC)

    await handle_mainbot_menu_callback(mock_event)

    mock_event.answer.assert_called_once()
    mock_event.edit.assert_called_once()
    args, kwargs = mock_event.edit.call_args
    assert "基础高频功能" in args[0]
    assert kwargs.get("buttons") is not None


@pytest.mark.asyncio
async def test_command_callback_dispatches_existing_handler(mock_event):
    """测试命令按钮会分发到现有处理器。"""
    mock_event.data = build_callback("cmd", CMD_CHANNELS)

    func = AsyncMock()
    with patch.dict(mainbot_menu_handler._COMMAND_HANDLERS, {CMD_CHANNELS: func}):
        await handle_mainbot_menu_callback(mock_event)

    mock_event.answer.assert_called_once()
    func.assert_awaited_once()
    command_event = func.await_args.args[0]
    assert command_event.text == "/showchannels"
    assert command_event.sender_id == mock_event.sender_id


@pytest.mark.asyncio
async def test_forwarding_callback_uses_command_event_context(mock_event):
    """测试转发按钮无需原始 message 上下文也可执行。"""
    mock_event.data = build_callback("cmd", CMD_FORWARDING_STATUS)
    mock_event.message = None
    forwarding_handler = MagicMock()
    forwarding_handler.enabled = True
    forwarding_handler.config = {"rules": []}

    with (
        patch(
            "core.forwarding.get_forwarding_handler",
            return_value=forwarding_handler,
        ),
        patch(
            "core.commands.forwarding_commands.cmd_forwarding_status",
            new=AsyncMock(),
        ) as func,
    ):
        await handle_mainbot_menu_callback(mock_event)

    mock_event.answer.assert_called_once()
    func.assert_awaited_once()
    message = func.await_args.args[1]
    assert message.message == "/forwarding"
    assert message.sender_id == mock_event.sender_id


@pytest.mark.asyncio
async def test_unknown_callback_safe_response(mock_event):
    """测试未知回调操作会安全提示。"""
    mock_event.data = b"mainbot:unknown:value"

    await handle_mainbot_menu_callback(mock_event)

    mock_event.answer.assert_called_once()
    mock_event.edit.assert_called_once()
    args, kwargs = mock_event.edit.call_args
    assert "未知" in args[0]
    assert kwargs.get("buttons") is not None


def test_build_mainbot_menu_keyboard_has_buttons():
    """测试主菜单按钮构建结果包含关键入口。"""
    buttons = build_mainbot_menu_keyboard()

    assert buttons
    assert all(row for row in buttons)
    assert len(buttons) == 4

    button_texts = [button.text for row in buttons for button in row]
    assert "📌 基础功能" in button_texts
    assert "🤖 QA 控制" in button_texts
    assert "🔁 频道转发" in button_texts
    assert "⚙️ 配置入口" in button_texts
    assert "❓ 完整帮助" in button_texts
    assert "🛡 Sakura Guard" in button_texts


def test_build_callback_rejects_oversized_data():
    """测试过长 callback_data 会被拒绝。"""
    with pytest.raises(ValueError, match="64 字节"):
        build_callback("cmd", "x" * 80)


@pytest.mark.parametrize("menu", [MENU_MAIN, MENU_BASIC, MENU_QA, MENU_FORWARDING, MENU_CONFIG])
def test_build_mainbot_menu_keyboard_callback_data_within_limit(menu, inline_button_payload):
    """测试主菜单回调数据不超过 Telegram 限制。"""
    buttons = build_mainbot_menu_keyboard(menu)
    assert all(len(inline_button_payload(button)) <= 64 for row in buttons for button in row)
