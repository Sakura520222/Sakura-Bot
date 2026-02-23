"""测试 Conversation Manager 模块

Copyright 2026 Sakura-Bot
本项目采用 AGPL-3.0 许可
"""

from datetime import UTC, datetime, timedelta
from unittest.mock import AsyncMock, MagicMock, patch

import pytest

from core.conversation_manager import (
    ConversationManager,
    get_conversation_manager,
)


@pytest.mark.unit
class TestConversationManagerInit:
    """初始化测试"""

    @patch("core.conversation_manager.get_db_manager")
    def test_init(self, mock_get_db):
        """测试初始化"""
        mock_db = MagicMock()
        mock_get_db.return_value = mock_db

        manager = ConversationManager()

        assert manager.db == mock_db
        assert manager._session_cache == {}


@pytest.mark.unit
class TestGetOrCreateSession:
    """获取或创建会话测试"""

    @patch("core.conversation_manager.get_db_manager")
    def test_create_new_session(self, mock_get_db):
        """测试创建新会话"""
        mock_db = MagicMock()
        mock_get_db.return_value = mock_db

        manager = ConversationManager()
        session_id, is_new = manager.get_or_create_session(123)

        assert is_new is True
        assert session_id is not None
        assert 123 in manager._session_cache

    @patch("core.conversation_manager.get_db_manager")
    def test_reuse_existing_session(self, mock_get_db):
        """测试复用现有会话"""
        mock_db = MagicMock()
        mock_get_db.return_value = mock_db

        manager = ConversationManager()

        # 第一次调用
        session_id1, is_new1 = manager.get_or_create_session(123)
        # 第二次调用
        session_id2, is_new2 = manager.get_or_create_session(123)

        assert is_new1 is True
        assert is_new2 is False
        assert session_id1 == session_id2

    @patch("core.conversation_manager.get_db_manager")
    def test_session_timeout(self, mock_get_db):
        """测试会话超时"""
        mock_db = MagicMock()
        mock_get_db.return_value = mock_db

        manager = ConversationManager()

        # 创建会话
        session_id1, _ = manager.get_or_create_session(123)

        # 修改缓存中的时间为超时时间
        manager._session_cache[123]["last_active"] = datetime.now(UTC) - timedelta(minutes=31)

        # 再次获取应该创建新会话
        session_id2, is_new = manager.get_or_create_session(123)

        assert is_new is True
        assert session_id1 != session_id2

    @patch("core.conversation_manager.get_db_manager")
    def test_different_users_separate_sessions(self, mock_get_db):
        """测试不同用户有独立会话"""
        mock_db = MagicMock()
        mock_get_db.return_value = mock_db

        manager = ConversationManager()

        session_id1, _ = manager.get_or_create_session(123)
        session_id2, _ = manager.get_or_create_session(456)

        assert session_id1 != session_id2
        assert 123 in manager._session_cache
        assert 456 in manager._session_cache


@pytest.mark.unit
class TestSaveMessage:
    """保存消息测试"""

    @patch("core.conversation_manager.get_db_manager")
    @pytest.mark.asyncio
    async def test_save_message_success(self, mock_get_db):
        """测试成功保存消息"""
        mock_db = MagicMock()
        mock_db.save_conversation = AsyncMock(return_value=True)
        mock_get_db.return_value = mock_db

        manager = ConversationManager()
        manager.get_or_create_session(123)

        success = await manager.save_message(123, "session1", "user", "Hello")

        assert success is True
        mock_db.save_conversation.assert_called_once()

    @patch("core.conversation_manager.get_db_manager")
    @pytest.mark.asyncio
    async def test_save_message_failure(self, mock_get_db):
        """测试保存消息失败"""
        mock_db = MagicMock()
        mock_db.save_conversation = AsyncMock(return_value=False)
        mock_get_db.return_value = mock_db

        manager = ConversationManager()

        success = await manager.save_message(123, "session1", "user", "Hello")

        assert success is False


@pytest.mark.unit
class TestGetConversationHistory:
    """获取对话历史测试"""

    @patch("core.conversation_manager.get_db_manager")
    @pytest.mark.asyncio
    async def test_get_history_success(self, mock_get_db):
        """测试成功获取历史"""
        mock_db = MagicMock()
        mock_db.get_conversation_history = AsyncMock(
            return_value=[
                {"role": "user", "content": "Hello"},
                {"role": "assistant", "content": "Hi"},
            ]
        )
        mock_get_db.return_value = mock_db

        manager = ConversationManager()

        history = await manager.get_conversation_history(123, "session1")

        assert len(history) == 2
        assert history[0]["role"] == "user"

    @patch("core.conversation_manager.get_db_manager")
    @pytest.mark.asyncio
    async def test_get_history_empty(self, mock_get_db):
        """测试获取空历史"""
        mock_db = MagicMock()
        mock_db.get_conversation_history = AsyncMock(return_value=[])
        mock_get_db.return_value = mock_db

        manager = ConversationManager()

        history = await manager.get_conversation_history(123, "session1")

        assert history == []


@pytest.mark.unit
class TestClearUserHistory:
    """清除用户历史测试"""

    @patch("core.conversation_manager.get_db_manager")
    @pytest.mark.asyncio
    async def test_clear_all_history(self, mock_get_db):
        """测试清除所有历史"""
        mock_db = MagicMock()
        mock_db.clear_user_conversations = AsyncMock(return_value=5)
        mock_get_db.return_value = mock_db

        manager = ConversationManager()
        manager._session_cache[123] = {"session_id": "session1", "last_active": datetime.now(UTC)}

        deleted = await manager.clear_user_history(123)

        assert deleted == 5
        assert 123 not in manager._session_cache

    @patch("core.conversation_manager.get_db_manager")
    @pytest.mark.asyncio
    async def test_clear_specific_session(self, mock_get_db):
        """测试清除特定会话"""
        mock_db = MagicMock()
        mock_db.clear_user_conversations = AsyncMock(return_value=2)
        mock_get_db.return_value = mock_db

        manager = ConversationManager()
        manager._session_cache[123] = {"session_id": "session1", "last_active": datetime.now(UTC)}

        deleted = await manager.clear_user_history(123, "session1")

        assert deleted == 2
        assert 123 not in manager._session_cache


@pytest.mark.unit
class TestFormatConversationContext:
    """格式化对话上下文测试"""

    def test_format_empty_history(self):
        """测试格式化空历史"""
        manager = ConversationManager()

        result = manager.format_conversation_context([])

        assert result == ""

    def test_format_history_with_messages(self):
        """测试格式化有消息的历史"""
        manager = ConversationManager()

        history = [
            {"role": "user", "content": "Hello"},
            {"role": "assistant", "content": "Hi there"},
        ]

        result = manager.format_conversation_context(history)

        assert "用户" in result
        assert "助手" in result
        assert "Hello" in result
        assert "Hi there" in result

    def test_format_truncates_long_messages(self):
        """测试截断过长消息"""
        manager = ConversationManager()

        long_content = "A" * 600
        history = [{"role": "user", "content": long_content}]

        result = manager.format_conversation_context(history)

        assert "..." in result
        assert len(result) < 650


@pytest.mark.unit
class TestGetSessionInfo:
    """获取会话信息测试"""

    @patch("core.conversation_manager.get_db_manager")
    @pytest.mark.asyncio
    async def test_get_session_info(self, mock_get_db):
        """测试获取会话信息"""
        mock_db = MagicMock()
        mock_db.get_conversation_history = AsyncMock(return_value=[{}, {}])
        mock_get_db.return_value = mock_db

        manager = ConversationManager()
        manager.get_or_create_session(123)

        info = await manager.get_session_info(123)

        assert info is not None
        assert "session_id" in info
        assert "message_count" in info
        assert info["message_count"] == 2

    @patch("core.conversation_manager.get_db_manager")
    @pytest.mark.asyncio
    async def test_get_session_info_not_found(self, mock_get_db):
        """测试获取不存在会话的信息"""
        mock_db = MagicMock()
        mock_get_db.return_value = mock_db

        manager = ConversationManager()

        info = await manager.get_session_info(123)

        assert info is None


@pytest.mark.unit
class TestCleanupOldSessions:
    """清理旧会话测试"""

    @patch("core.conversation_manager.get_db_manager")
    @pytest.mark.asyncio
    async def test_cleanup_old_sessions(self, mock_get_db):
        """测试清理旧会话"""
        mock_db = MagicMock()
        mock_db.delete_old_conversations = AsyncMock(return_value=10)
        mock_get_db.return_value = mock_db

        manager = ConversationManager()
        manager._session_cache[123] = {
            "session_id": "session1",
            "last_active": datetime.now(UTC) - timedelta(minutes=31),
        }

        deleted = await manager.cleanup_old_sessions()

        assert deleted == 10
        assert 123 not in manager._session_cache


@pytest.mark.unit
class TestGetConversationManager:
    """获取全局实例测试"""

    @patch("core.conversation_manager.get_db_manager")
    def test_singleton(self, mock_get_db):
        """测试单例模式"""
        import core.conversation_manager

        core.conversation_manager.conversation_manager = None

        manager1 = get_conversation_manager()
        manager2 = get_conversation_manager()

        assert manager1 is manager2

    @patch("core.conversation_manager.get_db_manager")
    def test_returns_conversation_manager_instance(self, mock_get_db):
        """测试返回ConversationManager实例"""
        import core.conversation_manager

        core.conversation_manager.conversation_manager = None

        manager = get_conversation_manager()

        assert isinstance(manager, ConversationManager)


if __name__ == "__main__":
    pytest.main([__file__, "-v"])
