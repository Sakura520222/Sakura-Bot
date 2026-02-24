"""
测试问答Bot用户系统管理
"""

from unittest.mock import AsyncMock, MagicMock, patch

import pytest

from core.qa_user_system import QAUserSystem


@pytest.fixture
def qa_user_system():
    """创建QAUserSystem实例"""
    with patch("core.qa_user_system.get_db_manager") as mock_get_db:
        mock_db = MagicMock()
        mock_get_db.return_value = mock_db
        system = QAUserSystem()
        system.db = mock_db
        return system


@pytest.mark.asyncio
async def test_create_summary_request_success(qa_user_system):
    """测试成功创建总结请求"""
    user_id = 123456
    channel_id = "https://t.me/test_channel"
    channel_name = "Test Channel"
    expected_request_id = 100

    # 模拟数据库返回请求ID
    qa_user_system.db.create_request = AsyncMock(return_value=expected_request_id)

    result = await qa_user_system.create_summary_request(user_id, channel_id, channel_name)

    assert result["success"] is True
    assert result["request_id"] == expected_request_id
    assert "频道:" in result["message"]
    assert "Test Channel" in result["message"]
    assert f"请求ID: {expected_request_id}" in result["message"]

    # 验证异步调用
    qa_user_system.db.create_request.assert_awaited_once_with(
        request_type="summary",
        requested_by=user_id,
        target_channel=channel_id,
        params={"channel_name": channel_name},
    )


@pytest.mark.asyncio
async def test_create_summary_request_failure(qa_user_system):
    """测试创建总结请求失败"""
    user_id = 123456
    channel_id = "https://t.me/test_channel"
    channel_name = "Test Channel"

    # 模拟数据库返回None（失败）
    qa_user_system.db.create_request = AsyncMock(return_value=None)

    result = await qa_user_system.create_summary_request(user_id, channel_id, channel_name)

    assert result["success"] is False
    assert "创建请求失败" in result["message"]


@pytest.mark.asyncio
async def test_create_summary_request_exception(qa_user_system):
    """测试创建总结请求时发生异常"""
    user_id = 123456
    channel_id = "https://t.me/test_channel"
    channel_name = "Test Channel"

    # 模拟数据库抛出异常
    qa_user_system.db.create_request = AsyncMock(side_effect=Exception("Database error"))

    result = await qa_user_system.create_summary_request(user_id, channel_id, channel_name)

    assert result["success"] is False
    assert "创建请求时发生错误" in result["message"]


@pytest.mark.asyncio
async def test_create_summary_request_without_channel_name(qa_user_system):
    """测试没有频道名称时创建请求"""
    user_id = 123456
    channel_id = "https://t.me/test_channel"
    expected_request_id = 101

    # 模拟数据库返回请求ID
    qa_user_system.db.create_request = AsyncMock(return_value=expected_request_id)

    result = await qa_user_system.create_summary_request(user_id, channel_id, None)

    assert result["success"] is True
    assert result["request_id"] == expected_request_id

    # 验证参数包含None的channel_name
    qa_user_system.db.create_request.assert_awaited_once_with(
        request_type="summary",
        requested_by=user_id,
        target_channel=channel_id,
        params={"channel_name": None},
    )


@pytest.mark.asyncio
async def test_register_user_new_user(qa_user_system):
    """测试注册新用户"""
    user_id = 123456
    username = "testuser"
    first_name = "Test"

    # 模拟用户不存在
    qa_user_system.db.is_user_registered = AsyncMock(return_value=False)
    # 模拟注册成功
    qa_user_system.db.register_user = AsyncMock(return_value=True)

    result = await qa_user_system.register_user(user_id, username, first_name)

    assert result["success"] is True
    assert result["new_user"] is True
    assert "注册成功" in result["message"]

    qa_user_system.db.is_user_registered.assert_awaited_once_with(user_id)
    qa_user_system.db.register_user.assert_awaited_once_with(user_id, username, first_name)


@pytest.mark.asyncio
async def test_register_user_existing_user(qa_user_system):
    """测试已存在的用户"""
    user_id = 123456
    username = "testuser"
    first_name = "Test"

    # 模拟用户已存在
    qa_user_system.db.is_user_registered = AsyncMock(return_value=True)
    # 模拟更新活跃时间
    qa_user_system.db.update_user_activity = AsyncMock(return_value=True)

    result = await qa_user_system.register_user(user_id, username, first_name)

    assert result["success"] is True
    assert result["new_user"] is False
    assert "欢迎回来" in result["message"]

    qa_user_system.db.is_user_registered.assert_awaited_once_with(user_id)
    qa_user_system.db.update_user_activity.assert_awaited_once_with(user_id)


@pytest.mark.asyncio
async def test_add_subscription_success(qa_user_system):
    """测试添加订阅成功"""
    user_id = 123456
    channel_id = "https://t.me/test_channel"
    channel_name = "Test Channel"

    # 模拟未订阅
    qa_user_system.db.is_subscribed = AsyncMock(return_value=False)
    # 模拟添加成功
    qa_user_system.db.add_subscription = AsyncMock(return_value=True)

    result = await qa_user_system.add_subscription(user_id, channel_id, channel_name)

    assert result["success"] is True
    assert "已订阅" in result["message"]
    assert "Test Channel" in result["message"]


@pytest.mark.asyncio
async def test_add_subscription_already_subscribed(qa_user_system):
    """测试添加已订阅的频道"""
    user_id = 123456
    channel_id = "https://t.me/test_channel"
    channel_name = "Test Channel"

    # 模拟已订阅
    qa_user_system.db.is_subscribed = AsyncMock(return_value=True)

    result = await qa_user_system.add_subscription(user_id, channel_id, channel_name)

    assert result["success"] is False
    assert "已经订阅" in result["message"]


@pytest.mark.asyncio
async def test_remove_subscription_success(qa_user_system):
    """测试移除订阅成功"""
    user_id = 123456
    channel_id = "https://t.me/test_channel"

    # 模拟移除成功
    qa_user_system.db.remove_subscription = AsyncMock(return_value=1)

    result = await qa_user_system.remove_subscription(user_id, channel_id)

    assert result["success"] is True
    assert "已取消" in result["message"]


@pytest.mark.asyncio
async def test_remove_subscription_not_found(qa_user_system):
    """测试移除不存在的订阅"""
    user_id = 123456
    channel_id = "https://t.me/test_channel"

    # 模拟未找到订阅
    qa_user_system.db.remove_subscription = AsyncMock(return_value=0)

    result = await qa_user_system.remove_subscription(user_id, channel_id)

    assert result["success"] is False
    assert "未找到" in result["message"]


@pytest.mark.asyncio
async def test_get_user_subscriptions(qa_user_system):
    """测试获取用户订阅列表"""
    user_id = 123456

    mock_subscriptions = [
        {
            "id": 1,
            "user_id": user_id,
            "channel_id": "https://t.me/channel1",
            "channel_name": "Channel 1",
            "sub_type": "summary",
            "created_at": "2026-02-24",
        },
        {
            "id": 2,
            "user_id": user_id,
            "channel_id": "https://t.me/channel2",
            "channel_name": "Channel 2",
            "sub_type": "summary",
            "created_at": "2026-02-23",
        },
    ]

    qa_user_system.db.get_user_subscriptions = AsyncMock(return_value=mock_subscriptions)

    result = await qa_user_system.get_user_subscriptions(user_id)

    assert len(result) == 2
    assert result[0]["channel_name"] == "Channel 1"
    assert result[1]["channel_name"] == "Channel 2"


@pytest.mark.asyncio
async def test_get_available_channels(qa_user_system):
    """测试获取可用频道列表"""
    mock_channels = [
        {
            "channel_id": "https://t.me/channel1",
            "channel_name": "Channel 1",
            "last_summary_time": "2026-02-24",
        },
        {
            "channel_id": "https://t.me/channel2",
            "channel_name": "Channel 2",
            "last_summary_time": "2026-02-23",
        },
    ]

    qa_user_system.db.get_all_channels = AsyncMock(return_value=mock_channels)

    result = await qa_user_system.get_available_channels()

    assert len(result) == 2
    assert result[0]["channel_name"] == "Channel 1"


def test_format_channels_list(qa_user_system):
    """测试格式化频道列表"""
    channels = [
        {
            "channel_id": "https://t.me/test_channel",
            "channel_name": "Test Channel",
            "last_summary_time": "2026-02-24",
        }
    ]

    result = qa_user_system.format_channels_list(channels)

    assert "可订阅频道列表" in result
    assert "Test Channel" in result
    assert "https://t.me/test_channel" in result
    assert "2026-02-24" in result


def test_format_channels_list_empty(qa_user_system):
    """测试格式化空频道列表"""
    result = qa_user_system.format_channels_list([])

    assert "暂无可订阅的频道" in result


def test_format_subscriptions_list(qa_user_system):
    """测试格式化订阅列表"""
    subscriptions = [
        {
            "channel_id": "https://t.me/test_channel",
            "channel_name": "Test Channel",
            "created_at": "2026-02-24",
        }
    ]

    result = qa_user_system.format_subscriptions_list(subscriptions)

    assert "我的订阅" in result
    assert "Test Channel" in result
    assert "2026-02-24" in result


def test_format_subscriptions_list_empty(qa_user_system):
    """测试格式化空订阅列表"""
    result = qa_user_system.format_subscriptions_list([])

    assert "还没有订阅任何频道" in result
    assert "/listchannels" in result


def test_format_date_field_datetime(qa_user_system):
    """测试格式化datetime对象"""
    from datetime import datetime

    dt = datetime(2026, 2, 24, 12, 30, 45)
    result = qa_user_system._format_date_field(dt)

    assert result == "2026-02-24"


def test_format_date_field_string(qa_user_system):
    """测试格式化日期字符串"""
    result = qa_user_system._format_date_field("2026-02-24")

    assert result == "2026-02-24"


def test_format_date_field_invalid_string(qa_user_system):
    """测试格式化无效日期字符串"""
    result = qa_user_system._format_date_field("invalid-date")

    assert result == ""


def test_format_date_field_none(qa_user_system):
    """测试格式化None值"""
    result = qa_user_system._format_date_field(None)

    assert result == ""
