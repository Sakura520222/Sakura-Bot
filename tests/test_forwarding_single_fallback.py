# Copyright 2026 Sakura-Bot
#
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可

"""
测试单消息转发回退策略。
"""

from unittest.mock import AsyncMock, MagicMock, patch

import pytest

from core.forwarding.forwarding_handler import ForwardingHandler
from core.forwarding.media_utils import ForwardStrategy


def make_message():
    """构造单消息 mock。"""
    message = MagicMock()
    message.id = 123
    message.message = "测试消息"
    message.media = MagicMock()
    message.chat_id = 456
    message.date = MagicMock()
    message.date.timestamp.return_value = 1234567890
    return message


def make_handler():
    """构造转发处理器 mock。"""
    db = MagicMock()
    db.add_forwarded_message = AsyncMock()
    db.update_forwarding_stats = AsyncMock()

    monitoring_client = MagicMock()
    sending_client = MagicMock()
    sending_client.send_message = AsyncMock()

    handler = ForwardingHandler(db, monitoring_client, sending_client)
    handler._generate_footer = AsyncMock(return_value="底栏")
    handler._generate_content_hash = MagicMock(return_value="hash")
    return handler


@pytest.mark.asyncio
@pytest.mark.unit
async def test_forward_single_copy_mode_falls_back_to_download_on_send_failure():
    """测试复制模式发送失败后回退到下载转发。"""
    handler = make_handler()
    message = make_message()
    handler.sending_client.send_message.side_effect = RuntimeError("send failed")
    handler._forward_single_with_download = AsyncMock(return_value=True)

    with patch(
        "core.forwarding.forwarding_handler.decide_forward_strategy",
        new=AsyncMock(
            return_value={
                "strategy": ForwardStrategy.MEMORY,
                "reason": "测试策略",
                "total_size": 1,
            }
        ),
    ):
        result = await handler._forward_single_message(
            message,
            "target_channel",
            "source_channel",
            {"copy_mode": True},
        )

    assert result is True
    handler._forward_single_with_download.assert_awaited_once_with(
        message,
        "target_channel",
        "source_channel",
        {"copy_mode": True},
    )
    handler.db.add_forwarded_message.assert_not_awaited()
    handler.db.update_forwarding_stats.assert_not_awaited()


@pytest.mark.asyncio
@pytest.mark.unit
async def test_forward_single_forward_fallback_copy_failure_uses_download():
    """测试转发模式回退复制失败后继续回退到下载转发。"""
    handler = make_handler()
    message = make_message()
    handler._forward_messages_with_fallback = AsyncMock(side_effect=RuntimeError("forward failed"))
    handler.sending_client.send_message.side_effect = RuntimeError("send failed")
    handler._forward_single_with_download = AsyncMock(return_value=True)

    with patch(
        "core.forwarding.forwarding_handler.decide_forward_strategy",
        new=AsyncMock(
            return_value={
                "strategy": ForwardStrategy.MEMORY,
                "reason": "测试策略",
                "total_size": 1,
            }
        ),
    ):
        result = await handler._forward_single_message(
            message,
            "target_channel",
            "source_channel",
            {"copy_mode": False},
        )

    assert result is True
    handler._forward_single_with_download.assert_awaited_once_with(
        message,
        "target_channel",
        "source_channel",
        {"copy_mode": False},
    )
    handler.db.add_forwarded_message.assert_not_awaited()
    handler.db.update_forwarding_stats.assert_not_awaited()
