"""测试 Vector Store 模块

Copyright 2026 Sakura-Bot
本项目采用 AGPL-3.0 许可
"""

from unittest.mock import MagicMock, patch

import pytest

from core.vector_store import VectorStore, get_vector_store


@pytest.mark.unit
class TestVectorStoreInit:
    """初始化测试"""

    @patch("core.vector_store.CHROMADB_AVAILABLE", True)
    @patch("core.vector_store.chromadb.PersistentClient")
    def test_init_success(self, mock_client):
        """测试成功初始化"""
        mock_collection = MagicMock()
        mock_client.return_value.get_or_create_collection.return_value = mock_collection

        with patch.dict("os.environ", {"VECTOR_DB_PATH": "test_vectors"}):
            store = VectorStore()

        assert store.client is not None

    @patch("core.vector_store.CHROMADB_AVAILABLE", False)
    def test_init_chromadb_not_available(self):
        """测试ChromaDB不可用"""
        store = VectorStore()

        assert store.client is None
        assert store.collection is None


@pytest.mark.unit
class TestIsAvailable:
    """可用性测试"""

    @patch("core.vector_store.CHROMADB_AVAILABLE", False)
    def test_is_available_when_unavailable(self):
        """测试不可用时返回False"""
        store = VectorStore()

        assert store.is_available() is False

    @patch("core.vector_store.CHROMADB_AVAILABLE", True)
    @patch("core.vector_store.chromadb.PersistentClient")
    def test_is_available_when_available(self, mock_client):
        """测试可用时返回True"""
        mock_collection = MagicMock()
        mock_client.return_value.get_or_create_collection.return_value = mock_collection

        store = VectorStore()

        assert store.is_available() is True


@pytest.mark.unit
class TestAddSummary:
    """添加总结测试"""

    @patch("core.vector_store.CHROMADB_AVAILABLE", False)
    def test_add_summary_unavailable(self):
        """测试不可用时添加失败"""
        store = VectorStore()

        result = store.add_summary(1, "test", {})

        assert result is False

    @patch("core.vector_store.CHROMADB_AVAILABLE", True)
    @patch("core.vector_store.chromadb.PersistentClient")
    @patch("core.embedding_generator.get_embedding_generator")
    def test_add_summary_success(self, mock_get_emb, mock_client):
        """测试成功添加"""
        mock_collection = MagicMock()
        mock_client.return_value.get_or_create_collection.return_value = mock_collection

        mock_emb_gen = MagicMock()
        mock_emb_gen.is_available.return_value = True
        mock_emb_gen.generate.return_value = [0.1, 0.2]
        mock_get_emb.return_value = mock_emb_gen

        store = VectorStore()
        result = store.add_summary(1, "test summary", {"channel_id": "test"})

        assert result is True

    @patch("core.vector_store.CHROMADB_AVAILABLE", True)
    @patch("core.vector_store.chromadb.PersistentClient")
    @patch("core.embedding_generator.get_embedding_generator")
    def test_add_summary_embedding_failed(self, mock_get_emb, mock_client):
        """测试embedding生成失败"""
        mock_collection = MagicMock()
        mock_client.return_value.get_or_create_collection.return_value = mock_collection

        mock_emb_gen = MagicMock()
        mock_emb_gen.is_available.return_value = True
        mock_emb_gen.generate.return_value = None
        mock_get_emb.return_value = mock_emb_gen

        store = VectorStore()
        result = store.add_summary(1, "test", {})

        assert result is False


@pytest.mark.unit
class TestSearchSimilar:
    """搜索相似总结测试"""

    @patch("core.vector_store.CHROMADB_AVAILABLE", False)
    def test_search_similar_unavailable(self):
        """测试不可用时搜索失败"""
        store = VectorStore()

        results = store.search_similar("test query")

        assert results == []

    @patch("core.vector_store.CHROMADB_AVAILABLE", True)
    @patch("core.vector_store.chromadb.PersistentClient")
    @patch("core.embedding_generator.get_embedding_generator")
    def test_search_similar_success(self, mock_get_emb, mock_client):
        """测试成功搜索"""
        mock_collection = MagicMock()
        mock_collection.count.return_value = 10
        mock_collection.query.return_value = {
            "ids": [[1, 2]],
            "documents": [["text1", "text2"]],
            "metadatas": [[{"channel": "test"}, {"channel": "test"}]],
            "distances": [[0.1, 0.2]],
        }
        mock_client.return_value.get_or_create_collection.return_value = mock_collection

        mock_emb_gen = MagicMock()
        mock_emb_gen.is_available.return_value = True
        mock_emb_gen.generate.return_value = [0.1, 0.2]
        mock_get_emb.return_value = mock_emb_gen

        store = VectorStore()
        results = store.search_similar("test")

        assert len(results) == 2
        assert results[0]["summary_id"] == 1

    @patch("core.vector_store.CHROMADB_AVAILABLE", True)
    @patch("core.vector_store.chromadb.PersistentClient")
    @patch("core.embedding_generator.get_embedding_generator")
    def test_search_similar_empty_results(self, mock_get_emb, mock_client):
        """测试搜索无结果"""
        mock_collection = MagicMock()
        mock_collection.count.return_value = 0
        mock_client.return_value.get_or_create_collection.return_value = mock_collection

        mock_emb_gen = MagicMock()
        mock_emb_gen.is_available.return_value = True
        mock_emb_gen.generate.return_value = [0.1, 0.2]
        mock_get_emb.return_value = mock_emb_gen

        store = VectorStore()
        results = store.search_similar("test")

        assert results == []


@pytest.mark.unit
class TestDeleteSummary:
    """删除总结测试"""

    @patch("core.vector_store.CHROMADB_AVAILABLE", False)
    def test_delete_summary_unavailable(self):
        """测试不可用时删除失败"""
        store = VectorStore()

        result = store.delete_summary(1)

        assert result is False

    @patch("core.vector_store.CHROMADB_AVAILABLE", True)
    @patch("core.vector_store.chromadb.PersistentClient")
    def test_delete_summary_success(self, mock_client):
        """测试成功删除"""
        mock_collection = MagicMock()
        mock_client.return_value.get_or_create_collection.return_value = mock_collection

        store = VectorStore()
        result = store.delete_summary(1)

        assert result is True


@pytest.mark.unit
class TestGetStats:
    """获取统计信息测试"""

    @patch("core.vector_store.CHROMADB_AVAILABLE", False)
    def test_get_stats_unavailable(self):
        """测试不可用时获取统计"""
        store = VectorStore()
        stats = store.get_stats()

        assert stats["available"] is False

    @patch("core.vector_store.CHROMADB_AVAILABLE", True)
    @patch("core.vector_store.chromadb.PersistentClient")
    def test_get_stats_success(self, mock_client):
        """测试成功获取统计"""
        mock_collection = MagicMock()
        mock_collection.count.return_value = 100
        mock_client.return_value.get_or_create_collection.return_value = mock_collection

        store = VectorStore()
        stats = store.get_stats()

        assert stats["available"] is True
        assert stats["total_vectors"] == 100


@pytest.mark.unit
class TestGetVectorStore:
    """获取全局实例测试"""

    @patch("core.vector_store.CHROMADB_AVAILABLE", False)
    def test_singleton(self):
        """测试单例模式"""
        import core.vector_store

        core.vector_store.vector_store = None

        store1 = get_vector_store()
        store2 = get_vector_store()

        assert store1 is store2

    @patch("core.vector_store.CHROMADB_AVAILABLE", False)
    def test_returns_vector_store_instance(self):
        """测试返回VectorStore实例"""
        import core.vector_store

        core.vector_store.vector_store = None

        store = get_vector_store()

        assert isinstance(store, VectorStore)


if __name__ == "__main__":
    pytest.main([__file__, "-v"])
