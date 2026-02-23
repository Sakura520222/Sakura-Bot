"""测试 Reranker 模块

Copyright 2026 Sakura-Bot
本项目采用 AGPL-3.0 许可
"""

from unittest.mock import MagicMock, patch

import pytest

from core.reranker import Reranker, get_reranker


@pytest.mark.unit
class TestRerankerInit:
    """Reranker 初始化测试"""

    def test_init_with_api_key(self, monkeypatch):
        """测试使用API KEY初始化"""
        monkeypatch.setenv("RERANKER_API_KEY", "test_api_key")
        monkeypatch.setenv("RERANKER_MODEL", "test_model")
        monkeypatch.setenv("RERANKER_TOP_K", "10")
        monkeypatch.setenv("RERANKER_FINAL", "3")

        r = Reranker()

        assert r.api_key == "test_api_key"
        assert r.model == "test_model"
        assert r.top_k == 10
        assert r.final_k == 3

    def test_init_without_api_key(self, monkeypatch):
        """测试没有API KEY初始化"""
        monkeypatch.delenv("RERANKER_API_KEY", raising=False)

        r = Reranker()

        assert r.api_key is None
        assert not r.is_available()

    def test_init_with_defaults(self, monkeypatch):
        """测试使用默认值初始化"""
        monkeypatch.setenv("RERANKER_API_KEY", "test_key")
        monkeypatch.delenv("RERANKER_MODEL", raising=False)
        monkeypatch.delenv("RERANKER_TOP_K", raising=False)
        monkeypatch.delenv("RERANKER_FINAL", raising=False)

        r = Reranker()

        assert r.model == "BAAI/bge-reranker-v2-m3"
        assert r.top_k == 20
        assert r.final_k == 5


@pytest.mark.unit
class TestRerankerIsAvailable:
    """Reranker 可用性测试"""

    def test_is_available_with_key(self, monkeypatch):
        """测试有API KEY时可用"""
        monkeypatch.setenv("RERANKER_API_KEY", "test_key")

        r = Reranker()
        assert r.is_available() is True

    def test_is_available_without_key(self, monkeypatch):
        """测试没有API KEY时不可用"""
        monkeypatch.delenv("RERANKER_API_KEY", raising=False)

        r = Reranker()
        assert r.is_available() is False


@pytest.mark.unit
class TestRerankerRerank:
    """Reranker 重排序测试"""

    def test_rerank_without_api_key(self, monkeypatch):
        """测试没有API KEY时返回原始结果"""
        monkeypatch.delenv("RERANKER_API_KEY", raising=False)

        r = Reranker()
        candidates = [
            {"summary_id": 1, "summary_text": "文档1"},
            {"summary_id": 2, "summary_text": "文档2"},
            {"summary_id": 3, "summary_text": "文档3"},
        ]

        result = r.rerank("测试查询", candidates)

        # 当候选少于final_k时，返回所有候选
        assert len(result) == min(len(candidates), r.final_k)
        assert result == candidates

    def test_rerank_with_custom_top_k(self, monkeypatch):
        """测试自定义top_k"""
        monkeypatch.delenv("RERANKER_API_KEY", raising=False)

        r = Reranker()
        candidates = [{"summary_id": i, "summary_text": f"文档{i}"} for i in range(10)]

        result = r.rerank("测试查询", candidates, top_k=3)

        assert len(result) == 3

    def test_rerank_empty_candidates(self, monkeypatch):
        """测试空候选列表"""
        monkeypatch.setenv("RERANKER_API_KEY", "test_key")

        r = Reranker()
        result = r.rerank("测试查询", [])

        assert result == []

    @patch("core.reranker.httpx.Client")
    def test_rerank_success(self, mock_client_class, monkeypatch):
        """测试成功重排序"""
        monkeypatch.setenv("RERANKER_API_KEY", "test_key")

        # Mock响应
        mock_response = MagicMock()
        mock_response.json.return_value = {
            "results": [
                {"index": 2, "relevance_score": 0.95},
                {"index": 0, "relevance_score": 0.85},
                {"index": 1, "relevance_score": 0.75},
            ]
        }

        mock_client = MagicMock()
        mock_client.post.return_value = mock_response
        mock_client.__enter__ = MagicMock(return_value=mock_client)
        mock_client.__exit__ = MagicMock(return_value=False)
        mock_client_class.return_value = mock_client

        r = Reranker()
        candidates = [
            {"summary_id": 1, "summary_text": "文档1"},
            {"summary_id": 2, "summary_text": "文档2"},
            {"summary_id": 3, "summary_text": "文档3"},
        ]

        result = r.rerank("测试查询", candidates)

        assert len(result) == 3
        assert result[0]["summary_id"] == 3  # index 2
        assert result[0]["rerank_score"] == 0.95
        assert result[1]["summary_id"] == 1  # index 0
        assert result[1]["rerank_score"] == 0.85
        assert result[2]["summary_id"] == 2  # index 1
        assert result[2]["rerank_score"] == 0.75

    @patch("core.reranker.httpx.Client")
    def test_rerank_with_missing_score(self, mock_client_class, monkeypatch):
        """测试响应中缺少relevance_score"""
        monkeypatch.setenv("RERANKER_API_KEY", "test_key")

        # Mock响应（没有relevance_score）
        mock_response = MagicMock()
        mock_response.json.return_value = {
            "results": [
                {"index": 0},
                {"index": 1},
            ]
        }

        mock_client = MagicMock()
        mock_client.post.return_value = mock_response
        mock_client.__enter__ = MagicMock(return_value=mock_client)
        mock_client.__exit__ = MagicMock(return_value=False)
        mock_client_class.return_value = mock_client

        r = Reranker()
        candidates = [
            {"summary_id": 1, "summary_text": "文档1"},
            {"summary_id": 2, "summary_text": "文档2"},
        ]

        result = r.rerank("测试查询", candidates)

        assert len(result) == 2
        assert result[0]["rerank_score"] == 0  # 默认值

    @patch("core.reranker.httpx.Client")
    def test_rerank_api_error(self, mock_client_class, monkeypatch):
        """测试API调用失败"""
        monkeypatch.setenv("RERANKER_API_KEY", "test_key")

        mock_client = MagicMock()
        mock_client.post.side_effect = Exception("API错误")
        mock_client.__enter__ = MagicMock(return_value=mock_client)
        mock_client.__exit__ = MagicMock(return_value=False)
        mock_client_class.return_value = mock_client

        r = Reranker()
        candidates = [
            {"summary_id": 1, "summary_text": "文档1"},
            {"summary_id": 2, "summary_text": "文档2"},
        ]

        result = r.rerank("测试查询", candidates, top_k=1)

        # 失败时返回原始结果（截断到top_k）
        assert len(result) == 1
        assert result[0]["summary_id"] == 1

    @patch("core.reranker.httpx.Client")
    def test_rerank_invalid_response_format(self, mock_client_class, monkeypatch):
        """测试API返回格式异常"""
        monkeypatch.setenv("RERANKER_API_KEY", "test_key")

        # Mock响应（缺少results字段）
        mock_response = MagicMock()
        mock_response.json.return_value = {"error": "invalid format"}

        mock_client = MagicMock()
        mock_client.post.return_value = mock_response
        mock_client.__enter__ = MagicMock(return_value=mock_client)
        mock_client.__exit__ = MagicMock(return_value=False)
        mock_client_class.return_value = mock_client

        r = Reranker()
        candidates = [
            {"summary_id": 1, "summary_text": "文档1"},
            {"summary_id": 2, "summary_text": "文档2"},
        ]

        result = r.rerank("测试查询", candidates, top_k=1)

        # 格式异常时返回原始结果
        assert len(result) == 1
        assert result[0]["summary_id"] == 1

    @patch("core.reranker.httpx.Client")
    def test_rerank_preserves_original_fields(self, mock_client_class, monkeypatch):
        """测试保留原始文档的所有字段"""
        monkeypatch.setenv("RERANKER_API_KEY", "test_key")

        mock_response = MagicMock()
        mock_response.json.return_value = {
            "results": [
                {"index": 0, "relevance_score": 0.9},
            ]
        }

        mock_client = MagicMock()
        mock_client.post.return_value = mock_response
        mock_client.__enter__ = MagicMock(return_value=mock_client)
        mock_client.__exit__ = MagicMock(return_value=False)
        mock_client_class.return_value = mock_client

        r = Reranker()
        candidates = [
            {
                "summary_id": 1,
                "summary_text": "文档1",
                "extra_field": "额外数据",
                "another_field": 123,
            }
        ]

        result = r.rerank("测试查询", candidates)

        assert len(result) == 1
        assert result[0]["summary_id"] == 1
        assert result[0]["summary_text"] == "文档1"
        assert result[0]["extra_field"] == "额外数据"
        assert result[0]["another_field"] == 123
        assert result[0]["rerank_score"] == 0.9


@pytest.mark.unit
class TestGetReranker:
    """获取Reranker实例测试"""

    def test_get_reranker_singleton(self, monkeypatch):
        """测试单例模式"""
        monkeypatch.setenv("RERANKER_API_KEY", "test_key")

        # 重置全局变量
        import core.reranker

        core.reranker.reranker = None

        r1 = get_reranker()
        r2 = get_reranker()

        assert r1 is r2

    def test_get_reranker_creates_instance(self, monkeypatch):
        """测试创建实例"""
        monkeypatch.setenv("RERANKER_API_KEY", "test_key")

        # 重置全局变量
        import core.reranker

        core.reranker.reranker = None

        r = get_reranker()

        assert isinstance(r, Reranker)
        assert r.is_available()


if __name__ == "__main__":
    pytest.main([__file__, "-v"])
