# Copyright 2026 Sakura-Bot
#
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可，
# 并附加非商业使用限制条款。

"""
测试频道评论区注册检查功能
"""

from unittest.mock import AsyncMock, MagicMock, patch

import pytest

from core.channel_comment_welcome import handle_summary_request_callback
from core.database_sqlite import SQLiteManager


@pytest.fixture
def mock_db():
    """创建模拟数据库"""
    db = MagicMock(spec=SQLiteManager)
    return db


@pytest.fixture
def mock_event():
    """创建模拟回调事件"""
    event = MagicMock()
    event.sender_id = 12345
    event.answer = AsyncMock()
    event.edit = AsyncMock()
    return event


@pytest.mark.asyncio
async def test_handle_summary_request_user_not_registered(mock_db, mock_event):
    """测试用户未注册时的处理"""
    # 设置回调数据
    channel_id = "test_channel"
    msg_id = 123
    mock_event.data = f"req_summary:{channel_id}:{msg_id}".encode()

    # 模拟数据库返回用户未注册
    mock_db.is_user_registered = AsyncMock(return_value=False)
    mock_db.check_pending_summary_request = AsyncMock(return_value=False)

    # 使用 patch 替换 get_db_manager
    with patch("core.channel_comment_welcome.get_db_manager", return_value=mock_db):
        # 执行处理
        await handle_summary_request_callback(mock_event)

    # 验证：用户未注册，应该显示注册按钮
    mock_event.edit.assert_called_once()
    args, kwargs = mock_event.edit.call_args
    assert "注册" in args[0] or "register" in args[0].lower()

    # 验证按钮存在
    buttons = kwargs.get("buttons")
    assert buttons is not None

    # 验证没有调用 answer (因为用了 edit)
    mock_event.answer.assert_called_once()


@pytest.mark.asyncio
async def test_handle_summary_request_user_registered(mock_db, mock_event):
    """测试用户已注册时的处理"""
    # 设置回调数据
    channel_id = "test_channel"
    msg_id = 123
    mock_event.data = f"req_summary:{channel_id}:{msg_id}".encode()

    # 模拟数据库返回用户已注册
    mock_db.is_user_registered = AsyncMock(return_value=True)
    mock_db.check_pending_summary_request = AsyncMock(return_value=False)
    mock_db.add_summary_request = AsyncMock(return_value=1)

    # 使用 patch 替换 get_db_manager
    with patch("core.channel_comment_welcome.get_db_manager", return_value=mock_db):
        # 执行处理
        await handle_summary_request_callback(mock_event)

    # 验证：用户已注册，应该添加请求
    mock_db.add_summary_request.assert_called_once()

    # 验证调用参数
    call_args = mock_db.add_summary_request.call_args
    assert call_args[1]["channel_id"] == channel_id
    assert call_args[1]["message_id"] == msg_id
    assert call_args[1]["requested_by"] == mock_event.sender_id

    # 验证有 answer 响应
    mock_event.answer.assert_called_once()


@pytest.mark.asyncio
async def test_handle_summary_request_invalid_callback_data(mock_db, mock_event):
    """测试无效的回调数据"""
    # 设置无效的回调数据
    mock_event.data = b"invalid_data"

    # 使用 patch 替换 get_db_manager
    with patch("core.channel_comment_welcome.get_db_manager", return_value=mock_db):
        # 执行处理
        await handle_summary_request_callback(mock_event)

    # 验证：应该返回错误消息
    mock_event.answer.assert_called_once_with("无效的请求")

    # 验证没有调用数据库方法
    assert not mock_db.is_user_registered.called


@pytest.mark.asyncio
async def test_handle_summary_request_pending_request_exists(mock_db, mock_event):
    """测试已存在待处理请求的情况"""
    # 设置回调数据
    channel_id = "test_channel"
    msg_id = 123
    mock_event.data = f"req_summary:{channel_id}:{msg_id}".encode()

    # 模拟数据库返回用户已注册，但有待处理的请求
    mock_db.is_user_registered = AsyncMock(return_value=True)
    mock_db.check_pending_summary_request = AsyncMock(return_value=True)

    # 使用 patch 替换 get_db_manager
    with patch("core.channel_comment_welcome.get_db_manager", return_value=mock_db):
        # 执行处理
        await handle_summary_request_callback(mock_event)

    # 验证：有待处理请求，不应该添加新请求
    assert not mock_db.add_summary_request.called

    # 验证 answer 被调用
    mock_event.answer.assert_called_once()
