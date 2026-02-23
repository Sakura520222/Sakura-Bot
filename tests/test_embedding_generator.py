"""测试 Embedding Generator 模块

Copyright 2026 Sakura-Bot
本项目采用 AGPL-3.0 许可
"""

import os
from unittest.mock import MagicMock, patch

import pytest

from core.embedding_generator import EmbeddingGenerator, get_embedding_generator


@pytest.mark.unit
class TestEmbeddingGeneratorInit:
    """初始化测试"""

    @patch.dict(os.environ, {"EMBEDDING_API_KEY": "test_key"})
    @patch("core.embedding_generator.OpenAI")
    def test_init_with_api_key(self, mock_openai):
        """测试使用API key初始化"""
        generator = EmbeddingGenerator()

        assert generator.api_key == "test_key"
        assert generator.is_available()

    @patch("core.embedding_generator.OpenAI")
    def test_init_without_api_key(self, mock_openai):
        """测试没有API key初始化"""
        with patch.dict(os.environ, {}, clear=True):
            generator = EmbeddingGenerator()

        assert generator.api_key is None
        assert not generator.is_available()

    @patch.dict(
        os.environ,
        {
            "EMBEDDING_API_KEY": "test_key",
            "EMBEDDING_API_BASE": "https://api.test.com",
            "EMBEDDING_MODEL": "test-model",
            "EMBEDDING_DIMENSION": "512",
        },
    )
    @patch("core.embedding_generator.OpenAI")
    def test_init_with_custom_config(self, mock_openai):
        """测试自定义配置初始化"""
        generator = EmbeddingGenerator()

        assert generator.api_base == "https://api.test.com"
        assert generator.model == "test-model"
        assert generator.dimension == 512


@pytest.mark.unit
class TestIsAvailable:
    """可用性测试"""

    @patch.dict(os.environ, {"EMBEDDING_API_KEY": "test_key"})
    @patch("core.embedding_generator.OpenAI")
    def test_is_available_when_client_exists(self, mock_openai):
        """测试客户端存在时返回True"""
        generator = EmbeddingGenerator()

        assert generator.is_available() is True

    @patch("core.embedding_generator.OpenAI")
    def test_is_available_when_client_missing(self, mock_openai):
        """测试客户端不存在时返回False"""
        with patch.dict(os.environ, {}, clear=True):
            generator = EmbeddingGenerator()

        assert generator.is_available() is False


@pytest.mark.unit
class TestGenerate:
    """生成单个embedding测试"""

    @patch.dict(os.environ, {"EMBEDDING_API_KEY": "test_key"})
    @patch("core.embedding_generator.OpenAI")
    def test_generate_success(self, mock_openai):
        """测试成功生成embedding"""
        mock_response = MagicMock()
        mock_response.data = [MagicMock(embedding=[0.1, 0.2, 0.3])]
        mock_client = MagicMock()
        mock_client.embeddings.create.return_value = mock_response
        mock_openai.return_value = mock_client

        generator = EmbeddingGenerator()
        result = generator.generate("test text")

        assert result == [0.1, 0.2, 0.3]

    @patch("core.embedding_generator.OpenAI")
    def test_generate_unavailable(self, mock_openai):
        """测试服务不可用时生成"""
        with patch.dict(os.environ, {}, clear=True):
            generator = EmbeddingGenerator()

        result = generator.generate("test text")

        assert result is None

    @patch.dict(os.environ, {"EMBEDDING_API_KEY": "test_key"})
    @patch("core.embedding_generator.OpenAI")
    def test_generate_api_error(self, mock_openai):
        """测试API错误"""
        mock_client = MagicMock()
        mock_client.embeddings.create.side_effect = Exception("API Error")
        mock_openai.return_value = mock_client

        generator = EmbeddingGenerator()
        result = generator.generate("test text")

        assert result is None


@pytest.mark.unit
class TestBatchGenerate:
    """批量生成embedding测试"""

    @patch.dict(os.environ, {"EMBEDDING_API_KEY": "test_key"})
    @patch("core.embedding_generator.OpenAI")
    def test_batch_generate_success(self, mock_openai):
        """测试成功批量生成"""
        mock_response = MagicMock()
        mock_response.data = [
            MagicMock(embedding=[0.1, 0.2]),
            MagicMock(embedding=[0.3, 0.4]),
            MagicMock(embedding=[0.5, 0.6]),
        ]
        mock_client = MagicMock()
        mock_client.embeddings.create.return_value = mock_response
        mock_openai.return_value = mock_client

        generator = EmbeddingGenerator()
        result = generator.batch_generate(["text1", "text2", "text3"])

        assert len(result) == 3
        assert result[0] == [0.1, 0.2]
        assert result[1] == [0.3, 0.4]
        assert result[2] == [0.5, 0.6]

    @patch("core.embedding_generator.OpenAI")
    def test_batch_generate_unavailable(self, mock_openai):
        """测试服务不可用时批量生成"""
        with patch.dict(os.environ, {}, clear=True):
            generator = EmbeddingGenerator()

        result = generator.batch_generate(["text1", "text2"])

        assert result == [None, None]

    @patch.dict(os.environ, {"EMBEDDING_API_KEY": "test_key"})
    @patch("core.embedding_generator.OpenAI")
    def test_batch_generate_api_error(self, mock_openai):
        """测试批量生成API错误"""
        mock_client = MagicMock()
        mock_client.embeddings.create.side_effect = Exception("API Error")
        mock_openai.return_value = mock_client

        generator = EmbeddingGenerator()
        result = generator.batch_generate(["text1", "text2"])

        assert result == [None, None]


@pytest.mark.unit
class TestGetEmbeddingGenerator:
    """获取全局实例测试"""

    @patch.dict(os.environ, {"EMBEDDING_API_KEY": "test_key"})
    @patch("core.embedding_generator.OpenAI")
    def test_singleton_pattern(self, mock_openai):
        """测试单例模式"""
        import core.embedding_generator

        core.embedding_generator.embedding_generator = None

        gen1 = get_embedding_generator()
        gen2 = get_embedding_generator()

        assert gen1 is gen2

    @patch.dict(os.environ, {"EMBEDDING_API_KEY": "test_key"})
    @patch("core.embedding_generator.OpenAI")
    def test_returns_embedding_generator_instance(self, mock_openai):
        """测试返回EmbeddingGenerator实例"""
        import core.embedding_generator

        core.embedding_generator.embedding_generator = None

        generator = get_embedding_generator()

        assert isinstance(generator, EmbeddingGenerator)


if __name__ == "__main__":
    pytest.main([__file__, "-v"])
