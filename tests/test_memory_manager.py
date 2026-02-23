"""测试 Memory Manager 模块

Copyright 2026 Sakura-Bot
本项目采用 AGPL-3.0 许可
"""

from unittest.mock import AsyncMock, MagicMock, patch

import pytest

from core.memory_manager import MemoryManager, get_memory_manager


@pytest.mark.unit
class TestMemoryManagerInit:
    """初始化测试"""

    @patch("core.memory_manager.get_db_manager")
    def test_init(self, mock_get_db):
        """测试初始化"""
        mock_db = MagicMock()
        mock_get_db.return_value = mock_db

        manager = MemoryManager()

        assert manager.db == mock_db


@pytest.mark.unit
class TestExtractMetadata:
    """提取元数据测试"""

    @patch("core.memory_manager.client_llm")
    @patch("core.memory_manager.get_llm_model")
    @patch("core.memory_manager.get_db_manager")
    def test_extract_metadata_success(self, mock_get_db, mock_model, mock_client):
        """测试成功提取元数据"""
        mock_db = MagicMock()
        mock_get_db.return_value = mock_db
        mock_model.return_value = "test-model"

        mock_response = MagicMock()
        mock_response.choices[0].message.content.strip.return_value = """
        {
            "keywords": ["AI", "ML"],
            "topics": ["技术"],
            "sentiment": "positive",
            "entities": ["OpenAI"]
        }
        """
        mock_client.chat.completions.create.return_value = mock_response

        manager = MemoryManager()
        metadata = manager.extract_metadata("Test summary")

        assert "keywords" in metadata
        assert "topics" in metadata

    @patch("core.memory_manager.get_db_manager")
    def test_extract_metadata_default(self, mock_get_db):
        """测试提取元数据失败时返回默认值"""
        mock_db = MagicMock()
        mock_get_db.return_value = mock_db

        manager = MemoryManager()
        metadata = manager.extract_metadata("Test")

        assert metadata["keywords"] == []
        assert metadata["topics"] == []
        assert metadata["sentiment"] == "neutral"


@pytest.mark.unit
class TestUpdateChannelProfile:
    """更新频道画像测试"""

    @patch("core.memory_manager.get_db_manager")
    @pytest.mark.asyncio
    async def test_update_channel_profile(self, mock_get_db):
        """测试更新频道画像"""
        mock_db = MagicMock()
        mock_db.update_channel_profile = AsyncMock()
        mock_get_db.return_value = mock_db

        manager = MemoryManager()

        await manager.update_channel_profile(
            channel_id="https://t.me/test",
            channel_name="Test Channel",
            summary_text="Test summary",
            metadata={"keywords": ["AI"], "topics": ["Tech"]},
        )

        mock_db.update_channel_profile.assert_called_once()


@pytest.mark.unit
class TestGetChannelContext:
    """获取频道上下文测试"""

    @patch("core.memory_manager.get_db_manager")
    @pytest.mark.asyncio
    async def test_get_channel_context_with_profile(self, mock_get_db):
        """测试获取有画像的频道上下文"""
        mock_db = MagicMock()
        mock_db.get_channel_profile = AsyncMock(
            return_value={
                "style": "tech",
                "topics": ["AI", "ML"],
                "tone": "neutral",
                "total_summaries": 10,
            }
        )
        mock_get_db.return_value = mock_db

        manager = MemoryManager()
        context = await manager.get_channel_context("https://t.me/test")

        assert "技术专业" in context or "特点" in context

    @patch("core.memory_manager.get_db_manager")
    @pytest.mark.asyncio
    async def test_get_channel_context_no_profile(self, mock_get_db):
        """测试获取无画像的频道上下文"""
        mock_db = MagicMock()
        mock_db.get_channel_profile = AsyncMock(return_value=None)
        mock_get_db.return_value = mock_db

        manager = MemoryManager()
        context = await manager.get_channel_context("https://t.me/test")

        assert "test" in context.lower()

    @patch("core.memory_manager.get_db_manager")
    @pytest.mark.asyncio
    async def test_get_channel_context_no_channel(self, mock_get_db):
        """测试获取无频道ID的上下文"""
        mock_db = MagicMock()
        mock_get_db.return_value = mock_db

        manager = MemoryManager()
        context = await manager.get_channel_context(None)

        assert "多频道" in context


@pytest.mark.unit
class TestSearchSummaries:
    """搜索总结测试"""

    @patch("core.memory_manager.get_db_manager")
    @pytest.mark.asyncio
    async def test_search_summaries_no_filters(self, mock_get_db):
        """测试无过滤条件的搜索"""
        mock_db = MagicMock()
        mock_db.get_summaries = AsyncMock(return_value=[{"summary_text": "test"}])
        mock_get_db.return_value = mock_db

        manager = MemoryManager()
        results = await manager.search_summaries()

        assert len(results) == 1

    @patch("core.memory_manager.get_db_manager")
    @pytest.mark.asyncio
    async def test_search_summaries_with_keywords(self, mock_get_db):
        """测试带关键词的搜索"""
        mock_db = MagicMock()
        mock_db.get_summaries = AsyncMock(
            return_value=[{"summary_text": "AI is great", "keywords": '["AI"]', "topics": "[]"}]
        )
        mock_get_db.return_value = mock_db

        manager = MemoryManager()
        results = await manager.search_summaries(keywords=["AI"])

        assert len(results) == 1

    @patch("core.memory_manager.get_db_manager")
    @pytest.mark.asyncio
    async def test_search_summaries_empty_result(self, mock_get_db):
        """测试搜索无结果"""
        mock_db = MagicMock()
        mock_db.get_summaries = AsyncMock(return_value=[])
        mock_get_db.return_value = mock_db

        manager = MemoryManager()
        results = await manager.search_summaries(keywords=["nonexistent"])

        assert len(results) == 0


@pytest.mark.unit
class TestGetMemoryManager:
    """获取全局实例测试"""

    @patch("core.memory_manager.get_db_manager")
    def test_singleton(self, mock_get_db):
        """测试单例模式"""
        import core.memory_manager

        core.memory_manager.memory_manager = None

        manager1 = get_memory_manager()
        manager2 = get_memory_manager()

        assert manager1 is manager2

    @patch("core.memory_manager.get_db_manager")
    def test_returns_memory_manager_instance(self, mock_get_db):
        """测试返回MemoryManager实例"""
        import core.memory_manager

        core.memory_manager.memory_manager = None

        manager = get_memory_manager()

        assert isinstance(manager, MemoryManager)


if __name__ == "__main__":
    pytest.main([__file__, "-v"])
