# Copyright 2026 Sakura-Bot
#
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可，
# 并附加非商业使用限制条款。

"""
测试投票重新生成的删除逻辑
"""

from unittest.mock import AsyncMock, MagicMock, patch

import pytest
from telethon.errors import (
    ChatAdminRequiredError,
    FloodWaitError,
    MessageDeleteForbiddenError,
    MessageIdInvalidError,
)

from core.i18n import set_language
from core.poll_regeneration_handlers import regenerate_poll


@pytest.fixture
def mock_client():
    """模拟 Telegram 客户端"""
    client = AsyncMock()
    return client


@pytest.fixture
def sample_regen_data_channel():
    """频道模式的投票重新生成数据"""
    return {
        "poll_message_id": 12345,
        "button_message_id": 12346,
        "summary_text": "这是测试总结内容",
        "channel_name": "测试频道",
        "timestamp": "2026-02-28T12:00:00",
        "send_to_channel": True,
        "vote_count": 0,
        "voters": [],
    }


@pytest.fixture
def sample_regen_data_discussion():
    """讨论组模式的投票重新生成数据"""
    return {
        "poll_message_id": 12345,
        "button_message_id": 12346,
        "summary_text": "这是测试总结内容",
        "channel_name": "测试频道",
        "timestamp": "2026-02-28T12:00:00",
        "send_to_channel": False,
        "vote_count": 0,
        "voters": [],
        "discussion_forward_msg_id": 78900,
    }


class TestPollRegenerationDelete:
    """测试投票重新生成的删除逻辑"""

    @pytest.mark.asyncio
    async def test_delete_success_channel_mode(self, mock_client, sample_regen_data_channel):
        """测试频道模式下删除成功"""
        # 设置语言为中文
        set_language("zh-CN")

        # 模拟删除成功
        mock_client.delete_messages = AsyncMock()

        # 模拟生成投票
        with patch(
            "core.ai_client.generate_poll_from_summary",
            new_callable=AsyncMock,
        ) as mock_generate:
            mock_generate.return_value = {
                "question": "测试问题",
                "options": ["选项1", "选项2"],
            }

            # 模拟发送投票
            mock_client.send_message = AsyncMock()
            mock_msg = MagicMock()
            mock_msg.id = 99999
            mock_client.send_message.return_value = mock_msg

            # 执行
            result = await regenerate_poll(
                mock_client, "https://t.me/test_channel", 100, sample_regen_data_channel
            )

            # 验证
            assert result is True
            mock_client.delete_messages.assert_called_once()

    @pytest.mark.asyncio
    async def test_delete_success_discussion_mode(self, mock_client, sample_regen_data_discussion):
        """测试讨论组模式下删除成功"""
        # 设置语言为中文
        set_language("zh-CN")

        # 模拟获取讨论组ID
        with patch(
            "core.config.get_discussion_group_id_cached",
            new_callable=AsyncMock,
        ) as mock_get_discussion:
            mock_get_discussion.return_value = -1001234567890

            # 模拟删除成功
            mock_client.delete_messages = AsyncMock()

            # 模拟生成投票
            with patch(
                "core.ai_client.generate_poll_from_summary",
                new_callable=AsyncMock,
            ) as mock_generate:
                mock_generate.return_value = {
                    "question": "测试问题",
                    "options": ["选项1", "选项2"],
                }

                # 模拟发送投票
                mock_client.send_message = AsyncMock()
                mock_msg = MagicMock()
                mock_msg.id = 99999
                mock_client.send_message.return_value = mock_msg

                # 执行
                result = await regenerate_poll(
                    mock_client, "https://t.me/test_channel", 100, sample_regen_data_discussion
                )

                # 验证
                assert result is True
                mock_client.delete_messages.assert_called_once()

    @pytest.mark.asyncio
    async def test_delete_permission_denied(self, mock_client, sample_regen_data_channel):
        """测试删除权限不足时中止流程"""
        # 设置语言为中文
        set_language("zh-CN")

        # 创建 mock request
        mock_request = MagicMock()

        # 模拟权限不足错误
        mock_client.delete_messages = AsyncMock(side_effect=ChatAdminRequiredError(mock_request))

        # 执行
        result = await regenerate_poll(
            mock_client, "https://t.me/test_channel", 100, sample_regen_data_channel
        )

        # 验证：删除失败，返回 False，不会生成新投票
        assert result is False
        mock_client.delete_messages.assert_called_once()

    @pytest.mark.asyncio
    async def test_delete_message_not_exist(self, mock_client, sample_regen_data_channel):
        """测试消息不存在时继续执行（视为删除成功）"""
        # 设置语言为中文
        set_language("zh-CN")

        # 创建 mock request
        mock_request = MagicMock()

        # 模拟消息不存在错误
        mock_client.delete_messages = AsyncMock(side_effect=MessageIdInvalidError(mock_request))

        # 模拟生成投票
        with patch(
            "core.ai_client.generate_poll_from_summary",
            new_callable=AsyncMock,
        ) as mock_generate:
            mock_generate.return_value = {
                "question": "测试问题",
                "options": ["选项1", "选项2"],
            }

            # 模拟发送投票
            mock_client.send_message = AsyncMock()
            mock_msg = MagicMock()
            mock_msg.id = 99999
            mock_client.send_message.return_value = mock_msg

            # 执行
            result = await regenerate_poll(
                mock_client, "https://t.me/test_channel", 100, sample_regen_data_channel
            )

            # 验证：消息不存在视为删除成功，应该继续生成新投票
            assert result is True

    @pytest.mark.asyncio
    async def test_delete_with_flood_wait_retry(self, mock_client, sample_regen_data_channel):
        """测试 FloodWait 错误时的重试机制"""
        # 设置语言为中文
        set_language("zh-CN")

        # 创建 mock request
        mock_request = MagicMock()

        # 模拟第一次 FloodWait，第二次成功
        mock_client.delete_messages = AsyncMock(
            side_effect=[FloodWaitError(mock_request, 2), None]  # 第一次等待2秒，第二次成功
        )

        # 模拟生成投票
        with patch(
            "core.ai_client.generate_poll_from_summary",
            new_callable=AsyncMock,
        ) as mock_generate:
            mock_generate.return_value = {
                "question": "测试问题",
                "options": ["选项1", "选项2"],
            }

            # 模拟发送投票
            mock_client.send_message = AsyncMock()
            mock_msg = MagicMock()
            mock_msg.id = 99999
            mock_client.send_message.return_value = mock_msg

            # 执行
            result = await regenerate_poll(
                mock_client, "https://t.me/test_channel", 100, sample_regen_data_channel
            )

            # 验证：应该重试并成功
            assert result is True
            assert mock_client.delete_messages.call_count == 2

    @pytest.mark.asyncio
    async def test_delete_general_error_retry_then_abort(
        self, mock_client, sample_regen_data_channel
    ):
        """测试一般错误重试后仍失败，中止流程"""
        # 设置语言为中文
        set_language("zh-CN")

        # 模拟连续3次失败
        mock_client.delete_messages = AsyncMock(side_effect=Exception("网络错误"))

        # 执行
        result = await regenerate_poll(
            mock_client, "https://t.me/test_channel", 100, sample_regen_data_channel
        )

        # 验证：重试3次后仍失败，返回 False
        assert result is False
        assert mock_client.delete_messages.call_count == 3

    @pytest.mark.asyncio
    async def test_delete_without_button_id(self, mock_client):
        """测试没有按钮ID时的删除"""
        # 设置语言为中文
        set_language("zh-CN")

        # 没有按钮ID的数据
        regen_data = {
            "poll_message_id": 12345,
            "summary_text": "测试总结",
            "send_to_channel": True,
        }

        # 模拟删除成功
        mock_client.delete_messages = AsyncMock()

        # 模拟生成投票
        with patch(
            "core.ai_client.generate_poll_from_summary",
            new_callable=AsyncMock,
        ) as mock_generate:
            mock_generate.return_value = {
                "question": "测试问题",
                "options": ["选项1", "选项2"],
            }

            # 模拟发送投票
            mock_client.send_message = AsyncMock()
            mock_msg = MagicMock()
            mock_msg.id = 99999
            mock_client.send_message.return_value = mock_msg

            # 执行
            result = await regenerate_poll(
                mock_client, "https://t.me/test_channel", 100, regen_data
            )

            # 验证：应该只删除投票消息
            assert result is True
            mock_client.delete_messages.assert_called_once()
            # 验证只传了一个消息ID
            call_args = mock_client.delete_messages.call_args
            assert len(call_args[0][1]) == 1  # 第二个参数是消息ID列表，应该只有一个

    @pytest.mark.asyncio
    async def test_delete_forbidden_error(self, mock_client, sample_regen_data_channel):
        """测试删除禁止错误时中止流程"""
        # 设置语言为中文
        set_language("zh-CN")

        # 创建 mock request
        mock_request = MagicMock()

        # 模拟删除禁止错误
        mock_client.delete_messages = AsyncMock(
            side_effect=MessageDeleteForbiddenError(mock_request)
        )

        # 执行
        result = await regenerate_poll(
            mock_client, "https://t.me/test_channel", 100, sample_regen_data_channel
        )

        # 验证：删除失败，返回 False
        assert result is False
        mock_client.delete_messages.assert_called_once()

    @pytest.mark.asyncio
    async def test_discussion_group_fallback_to_channel(
        self, mock_client, sample_regen_data_discussion
    ):
        """测试讨论组ID获取失败时回退到频道删除（删除成功但发送失败）"""
        # 设置语言为中文
        set_language("zh-CN")

        # 模拟获取讨论组ID失败，返回None（所有调用都返回None）
        with patch(
            "core.config.get_discussion_group_id_cached",
            new_callable=AsyncMock,
        ) as mock_get_discussion:
            mock_get_discussion.return_value = None

            # 模拟频道删除成功（回退）
            mock_client.delete_messages = AsyncMock()

            # 模拟生成投票
            with patch(
                "core.ai_client.generate_poll_from_summary",
                new_callable=AsyncMock,
            ) as mock_generate:
                mock_generate.return_value = {
                    "question": "测试问题",
                    "options": ["选项1", "选项2"],
                }

                # 执行
                result = await regenerate_poll(
                    mock_client, "https://t.me/test_channel", 100, sample_regen_data_discussion
                )

                # 验证：虽然删除成功，但发送新投票时再次获取讨论组ID失败，整体失败
                assert result is False
                # 验证删除被调用（回退到频道删除）
                mock_client.delete_messages.assert_called_once()

    @pytest.mark.asyncio
    async def test_send_poll_after_delete_success(self, mock_client, sample_regen_data_channel):
        """测试删除成功后正确发送新投票"""
        # 设置语言为中文
        set_language("zh-CN")

        # 模拟删除成功
        mock_client.delete_messages = AsyncMock()

        # 模拟生成投票
        with patch(
            "core.ai_client.generate_poll_from_summary",
            new_callable=AsyncMock,
        ) as mock_generate:
            mock_generate.return_value = {
                "question": "测试问题",
                "options": ["选项1", "选项2", "选项3"],
            }

            # 模拟发送投票
            mock_client.send_message = AsyncMock()
            mock_msg = MagicMock()
            mock_msg.id = 99999
            mock_client.send_message.return_value = mock_msg

            # 执行
            result = await regenerate_poll(
                mock_client, "https://t.me/test_channel", 100, sample_regen_data_channel
            )

            # 验证
            assert result is True
            # 验证删除被调用
            mock_client.delete_messages.assert_called_once()
            # 验证新投票被发送
            mock_client.send_message.assert_called_once()
            # 验证生成投票被调用
            mock_generate.assert_called_once_with("这是测试总结内容")
