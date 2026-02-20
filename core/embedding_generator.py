# Copyright 2026 Sakura-Bot
#
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可，
# 并附加非商业使用限制条款。
#
# - 署名：必须提供本项目的原始来源链接
# - 非商业：禁止任何商业用途和分发
# - 相同方式共享：衍生作品必须采用相同的许可证
#
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
# 许可证全文：参见 LICENSE 文件

"""
Embedding生成器 - 将文本转换为向量
支持多种API服务（OpenAI兼容）
"""

import logging
import os

from openai import OpenAI

logger = logging.getLogger(__name__)


class EmbeddingGenerator:
    """Embedding生成器"""

    def __init__(self):
        """初始化Embedding生成器"""
        self.api_key = os.getenv("EMBEDDING_API_KEY")
        self.api_base = os.getenv("EMBEDDING_API_BASE", "https://api.siliconflow.cn/v1/embeddings")
        self.model = os.getenv("EMBEDDING_MODEL", "BAAI/bge-m3")
        self.dimension = int(os.getenv("EMBEDDING_DIMENSION", "1024"))

        if not self.api_key:
            logger.warning("未设置EMBEDDING_API_KEY，Embedding功能将不可用")
            self.client = None
        else:
            try:
                self.client = OpenAI(api_key=self.api_key, base_url=self.api_base)
                logger.info(f"Embedding生成器初始化成功: {self.model}")
            except Exception as e:
                logger.error(f"Embedding生成器初始化失败: {type(e).__name__}: {e}")
                self.client = None

    def is_available(self) -> bool:
        """检查Embedding服务是否可用"""
        return self.client is not None

    def generate(self, text: str) -> list[float] | None:
        """
        生成单个文本的embedding

        Args:
            text: 输入文本

        Returns:
            向量列表，失败返回None
        """
        if not self.client:
            logger.warning("Embedding服务不可用")
            return None

        try:
            response = self.client.embeddings.create(model=self.model, input=text)

            embedding = response.data[0].embedding
            logger.debug(f"成功生成embedding，维度: {len(embedding)}")
            return embedding

        except Exception as e:
            logger.error(f"生成embedding失败: {type(e).__name__}: {e}")
            return None

    def batch_generate(self, texts: list[str]) -> list[list[float] | None]:
        """
        批量生成embedding

        Args:
            texts: 输入文本列表

        Returns:
            向量列表
        """
        if not self.client:
            logger.warning("Embedding服务不可用")
            return [None] * len(texts)

        try:
            response = self.client.embeddings.create(model=self.model, input=texts)

            embeddings = [item.embedding for item in response.data]
            logger.info(f"成功批量生成{len(embeddings)}个embedding")
            return embeddings

        except Exception as e:
            logger.error(f"批量生成embedding失败: {type(e).__name__}: {e}")
            return [None] * len(texts)


# 创建全局Embedding生成器实例
embedding_generator = None


def get_embedding_generator():
    """获取全局Embedding生成器实例"""
    global embedding_generator
    if embedding_generator is None:
        embedding_generator = EmbeddingGenerator()
    return embedding_generator
