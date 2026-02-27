# Copyright 2026 Sakura-Bot
#
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可，
# 并附加非商业使用限制条款。

"""
测试 send_long_message 的实体验证和修复功能
"""

from unittest.mock import AsyncMock, MagicMock

import pytest

from core.telegram.messaging import send_long_message
from core.telegram_client_utils import sanitize_markdown, validate_message_entities


@pytest.mark.asyncio
async def test_send_long_message_validates_entities():
    """测试 send_long_message 在发送前验证实体"""
    # 创建模拟的 Telegram 客户端
    mock_client = MagicMock()
    mock_client.send_message = AsyncMock()

    # 准备测试消息（包含有效的格式）
    valid_text = "📋 **测试频道**\n\n这是一个**粗体**文本和`代码`示例"

    # 调用函数
    await send_long_message(mock_client, 12345, valid_text)

    # 验证消息被发送了一次（格式有效，无需修复）
    assert mock_client.send_message.call_count == 1
    call_args = mock_client.send_message.call_args
    assert call_args[0][0] == 12345
    assert call_args[1]["link_preview"] is False


@pytest.mark.asyncio
async def test_send_long_message_fixes_invalid_entities():
    """测试 send_long_message 自动修复无效实体"""
    mock_client = MagicMock()
    mock_client.send_message = AsyncMock()

    # 准备测试消息（包含无效的格式：奇数个 **）
    invalid_text = "这是一个**粗体但未闭合的文本"

    # 调用函数
    await send_long_message(mock_client, 12345, invalid_text)

    # 验证消息被发送了一次（格式被自动修复）
    assert mock_client.send_message.call_count == 1
    # 验证发送的消息已被修复（移除了未闭合的 **）
    sent_text = mock_client.send_message.call_args[0][1]
    assert "**" not in sent_text  # 应该被移除


@pytest.mark.asyncio
async def test_send_long_message_handles_entity_bounds_error():
    """测试 send_long_message 处理 EntityBoundsInvalidError"""
    mock_client = MagicMock()

    # 第一次调用抛出实体边界错误，第二次成功
    error = Exception("EntityBoundsInvalidError: Some of provided entities have invalid bounds")
    mock_client.send_message = AsyncMock(side_effect=[error, None])

    # 准备测试消息（包含可能触发错误的格式）
    text = "消息文本"

    # 调用函数
    await send_long_message(mock_client, 12345, text)

    # 验证消息被发送了两次（第一次失败，第二次成功）
    assert mock_client.send_message.call_count == 2


@pytest.mark.asyncio
async def test_send_long_message_with_channel_title():
    """测试带标题的消息发送"""
    mock_client = MagicMock()
    mock_client.send_message = AsyncMock()

    text = "这是测试消息"
    channel_title = "测试频道"

    # 调用函数（带标题）
    await send_long_message(
        mock_client, 12345, text, channel_title=channel_title, show_pagination=True
    )

    # 验证发送的消息包含标题
    sent_text = mock_client.send_message.call_args[0][1]
    assert "📋" in sent_text
    assert channel_title in sent_text


@pytest.mark.asyncio
async def test_send_long_message_long_content_splits():
    """测试长消息分段时的格式验证"""
    mock_client = MagicMock()
    mock_client.send_message = AsyncMock()

    # 创建一个超过 4000 字符的长消息
    long_text = "这是很长的消息内容。" * 600  # 约 4800 字符

    # 调用函数
    await send_long_message(mock_client, 12345, long_text)

    # 验证消息被分段发送（至少 2 次）
    assert mock_client.send_message.call_count >= 2


def test_validate_message_entities_valid():
    """测试验证有效实体"""
    # 有效的格式
    valid_text = "这是**粗体**和`代码`和[链接](https://example.com)"
    is_valid, error = validate_message_entities(valid_text)
    assert is_valid is True
    assert error == "所有实体完整"


def test_validate_message_entities_invalid_bold():
    """测试检测无效粗体标记"""
    # 无效的格式（奇数个 **）
    invalid_text = "这是**粗体但未闭合"
    is_valid, error = validate_message_entities(invalid_text)
    assert is_valid is False
    assert "粗体标记" in error


def test_validate_message_entities_invalid_code():
    """测试检测无效代码标记"""
    # 无效的格式（奇数个 `）
    invalid_text = "这是`代码但未闭合"
    is_valid, error = validate_message_entities(invalid_text)
    assert is_valid is False
    assert "内联代码标记" in error


def test_sanitize_markdown_gentle_mode():
    """测试温和模式修复 Markdown"""
    # 未闭合的 ** 标记
    invalid_text = "这是**粗体但未闭合的文本"
    sanitized = sanitize_markdown(invalid_text, aggressive=False)
    # 温和模式应该移除未闭合的标记
    assert "**" not in sanitized


def test_sanitize_markdown_aggressive_mode():
    """测试激进模式修复 Markdown"""
    # 包含各种格式的文本
    text = "这是**粗体**、`代码`和[链接](url)的文本"
    sanitized = sanitize_markdown(text, aggressive=True)
    # 激进模式应该移除所有格式标记，只保留内容
    assert "**" not in sanitized
    assert "`" not in sanitized
    assert "[" not in sanitized
    assert "]" not in sanitized
    assert "粗体" in sanitized
    assert "代码" in sanitized
    assert "链接" in sanitized


def test_sanitize_markdown_preserves_content():
    """测试修复时保留文本内容"""
    # 未闭合的粗体标记
    invalid_text = "这是**未闭合的粗体文本，后面还有普通文本"
    sanitized = sanitize_markdown(invalid_text, aggressive=False)
    # 应该保留文本内容
    assert "这是" in sanitized
    assert "未闭合的粗体文本" in sanitized
    assert "后面还有普通文本" in sanitized


@pytest.mark.asyncio
async def test_send_long_message_preserves_link_preview_setting():
    """测试发送消息时正确设置 link_preview 参数"""
    mock_client = MagicMock()
    mock_client.send_message = AsyncMock()

    text = "测试消息"

    # 调用函数
    await send_long_message(mock_client, 12345, text)

    # 验证 link_preview 参数被正确设置
    call_args = mock_client.send_message.call_args
    assert call_args[1]["link_preview"] is False


@pytest.mark.asyncio
async def test_send_long_message_empty_text():
    """测试发送空文本"""
    mock_client = MagicMock()
    mock_client.send_message = AsyncMock()

    # 空文本
    empty_text = ""

    # 调用函数（应该正常处理）
    await send_long_message(mock_client, 12345, empty_text)

    # 验证消息被发送
    assert mock_client.send_message.call_count == 1
