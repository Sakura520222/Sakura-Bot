# Copyright 2026 Sakura-Bot
#
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可，
# 并附加非商业使用限制条款。

"""
UserBot 客户端测试模块

测试 UserBot 客户端的初始化、启动、状态查询等功能
"""

from unittest.mock import AsyncMock, MagicMock, patch

import pytest

from core.userbot_client import (
    UserBotClient,
    get_userbot_client,
    init_userbot_client,
    set_userbot_client,
)


@pytest.fixture
def mock_settings():
    """模拟配置"""
    with patch("core.userbot_client.get_settings") as mock:
        settings = MagicMock()
        settings.userbot_enabled = True
        settings.userbot_phone_number = "+1234567890"
        settings.userbot_session_path = "data/sessions/test_user_session"
        settings.telegram.api_id = 12345
        settings.telegram.api_hash = "test_hash"
        mock.return_value = settings
        yield settings


@pytest.fixture
def mock_telethon_client():
    """模拟 Telethon 客户端"""
    client = MagicMock()
    client.is_connected.return_value = True
    client.connect = AsyncMock()
    client.disconnect = AsyncMock()
    client.is_user_authorized = AsyncMock(return_value=True)
    client.get_me = AsyncMock()
    client.send_code_request = AsyncMock()
    client.sign_in = AsyncMock()
    client.iter_messages = AsyncMock()
    return client


@pytest.mark.asyncio
async def test_userbot_client_init():
    """测试 UserBot 客户端初始化"""
    client = UserBotClient(
        api_id=12345, api_hash="test_hash", session_path="test_session", phone_number="+1234567890"
    )

    assert client.client is None
    assert not client._is_initialized
    assert not client._is_connected
    assert client.api_id == 12345
    assert client.api_hash == "test_hash"
    assert client.session_path == "test_session"
    assert client.phone_number == "+1234567890"


@pytest.mark.asyncio
async def test_userbot_client_start_success(mock_telethon_client):
    """测试 UserBot 客户端启动成功"""
    client = UserBotClient(
        api_id=12345, api_hash="test_hash", session_path="test_session", phone_number="+1234567890"
    )

    # 模拟已授权的用户
    mock_user = MagicMock()
    mock_user.id = 123456
    mock_user.first_name = "Test"
    mock_user.username = "testuser"
    mock_user.phone = "+1234567890"
    mock_telethon_client.get_me.return_value = mock_user

    with patch("core.userbot_client.TelegramClient", return_value=mock_telethon_client):
        success = await client.start()

        assert success
        assert client._is_initialized
        assert client._is_connected
        assert client.client is not None


@pytest.mark.asyncio
async def test_userbot_client_start_not_authorized(mock_telethon_client):
    """测试 UserBot 客户端未授权（需要输入验证码）"""
    client = UserBotClient(
        api_id=12345, api_hash="test_hash", session_path="test_session", phone_number="+1234567890"
    )

    # 模拟未授权的用户
    mock_telethon_client.is_user_authorized.return_value = False

    with patch("core.userbot_client.TelegramClient", return_value=mock_telethon_client):
        # 由于需要交互式输入验证码，在测试环境中会失败
        success = await client.start()

        assert not success
        assert not client._is_initialized


@pytest.mark.asyncio
async def test_userbot_client_start_connection_error(mock_telethon_client):
    """测试 UserBot 客户端连接失败"""
    client = UserBotClient(
        api_id=12345, api_hash="test_hash", session_path="test_session", phone_number="+1234567890"
    )

    # 模拟连接失败
    mock_telethon_client.connect.side_effect = Exception("Connection failed")

    with patch("core.userbot_client.TelegramClient", return_value=mock_telethon_client):
        success = await client.start()

        assert not success
        assert not client._is_initialized
        assert not client._is_connected


@pytest.mark.asyncio
async def test_userbot_client_get_status(mock_telethon_client):
    """测试获取 UserBot 状态"""
    client = UserBotClient(
        api_id=12345, api_hash="test_hash", session_path="test_session", phone_number="+1234567890"
    )

    # 模拟已启动的客户端
    mock_user = MagicMock()
    mock_user.id = 123456
    mock_user.first_name = "Test"
    mock_user.username = "testuser"
    mock_user.phone = "+1234567890"
    mock_user.last_name = None
    mock_telethon_client.get_me.return_value = mock_user
    mock_telethon_client.is_connected.return_value = True

    with patch("core.userbot_client.TelegramClient", return_value=mock_telethon_client):
        await client.start()
        status = await client.get_status()

        assert status["available"]
        assert status["connected"]
        assert status["initialized"]
        assert status["user_info"]["username"] == "testuser"
        assert status["session_path"] == "test_session"


@pytest.mark.asyncio
async def test_userbot_client_get_status_not_initialized():
    """测试未初始化的客户端状态"""
    client = UserBotClient(
        api_id=12345, api_hash="test_hash", session_path="test_session", phone_number="+1234567890"
    )
    status = await client.get_status()

    assert not status["available"]
    assert not status["connected"]
    assert "error" in status  # 未初始化应该返回错误信息
    assert status["error"] == "客户端未初始化"


@pytest.mark.asyncio
async def test_userbot_client_is_available():
    """测试 is_available 方法"""
    client = UserBotClient(
        api_id=12345, api_hash="test_hash", session_path="test_session", phone_number="+1234567890"
    )

    # 未初始化
    assert not client.is_available()

    # 模拟已初始化
    client._is_initialized = True
    client._is_connected = True
    client.client = MagicMock()
    assert client.is_available()


@pytest.mark.asyncio
async def test_userbot_client_get_client():
    """测试获取 Telethon 客户端"""
    client = UserBotClient(
        api_id=12345, api_hash="test_hash", session_path="test_session", phone_number="+1234567890"
    )

    # 未初始化返回 None
    assert client.get_client() is None

    # 已初始化返回客户端
    mock_client = MagicMock()
    client.client = mock_client
    assert client.get_client() is mock_client


@pytest.mark.asyncio
async def test_init_userbot_client_disabled():
    """测试 UserBot 未启用时的初始化"""
    with patch("core.userbot_client.get_settings") as mock:
        settings = MagicMock()
        settings.userbot.userbot_enabled = False
        mock.return_value = settings

        client = await init_userbot_client()
        assert client is None


@pytest.mark.asyncio
async def test_init_userbot_client_success():
    """测试成功初始化 UserBot 客户端"""
    with patch("core.userbot_client.get_settings") as mock:
        settings = MagicMock()
        settings.userbot.userbot_enabled = True
        settings.userbot.userbot_phone_number = "+1234567890"
        settings.userbot.userbot_session_path = "data/sessions/user_session"
        settings.telegram.api_id = 12345
        settings.telegram.api_hash = "test_hash"
        mock.return_value = settings

        client = await init_userbot_client()
        assert client is not None
        assert client.api_id == 12345
        assert client.api_hash == "test_hash"
        assert client.phone_number == "+1234567890"


@pytest.mark.asyncio
async def test_get_userbot_client_singleton():
    """测试全局单例"""
    # 未设置时应该返回 None
    set_userbot_client(None)
    client1 = get_userbot_client()
    assert client1 is None

    # 设置后应该返回同一个实例
    test_client = UserBotClient(
        api_id=12345, api_hash="test_hash", session_path="test_session", phone_number="+1234567890"
    )
    set_userbot_client(test_client)

    client1 = get_userbot_client()
    client2 = get_userbot_client()
    assert client1 is client2
    assert client1 is test_client

    # 清理
    set_userbot_client(None)


@pytest.mark.asyncio
async def test_userbot_client_stop():
    """测试停止 UserBot 客户端"""
    client = UserBotClient(
        api_id=12345, api_hash="test_hash", session_path="test_session", phone_number="+1234567890"
    )

    mock_client = MagicMock()
    mock_client.is_connected.return_value = True
    mock_client.disconnect = AsyncMock()
    client.client = mock_client
    client._is_connected = True
    client._is_initialized = True

    await client.stop()

    assert client._is_connected is False
    assert client._is_initialized is False
    assert client.client is None
    mock_client.disconnect.assert_called_once()
