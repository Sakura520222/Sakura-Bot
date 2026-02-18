# -*- coding: utf-8 -*-
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
向量存储管理器 - 使用ChromaDB存储和检索向量
"""

import logging
import os
from typing import List, Dict, Any, Optional
from datetime import datetime

logger = logging.getLogger(__name__)

try:
    import chromadb
    from chromadb.config import Settings
    CHROMADB_AVAILABLE = True
except ImportError:
    CHROMADB_AVAILABLE = False
    logger.warning("ChromaDB未安装，向量搜索功能将不可用")


class VectorStore:
    """向量存储管理器"""

    def __init__(self):
        """初始化向量存储"""
        if not CHROMADB_AVAILABLE:
            logger.error("ChromaDB未安装，请运行: pip install chromadb")
            self.client = None
            self.collection = None
            return

        try:
            # 获取配置
            vector_db_path = os.getenv("VECTOR_DB_PATH", "data/vectors")
            embedding_dimension = int(os.getenv("EMBEDDING_DIMENSION", "1024"))

            # 创建持久化客户端
            self.client = chromadb.PersistentClient(path=vector_db_path)

            # 获取或创建collection
            self.collection = self.client.get_or_create_collection(
                name="summaries",
                metadata={"hnsw:space": "cosine"}  # 使用余弦相似度
            )

            logger.info(f"向量存储初始化成功: {vector_db_path}")

        except Exception as e:
            logger.error(f"向量存储初始化失败: {type(e).__name__}: {e}")
            self.client = None
            self.collection = None

    def is_available(self) -> bool:
        """检查向量存储是否可用"""
        return self.collection is not None

    def add_summary(self, summary_id: int, text: str,
                   metadata: Dict[str, Any]) -> bool:
        """
        添加总结向量到存储

        Args:
            summary_id: 总结ID
            text: 总结文本
            metadata: 元数据（channel_id, channel_name, created_at等）

        Returns:
            是否成功
        """
        if not self.collection:
            logger.warning("向量存储不可用")
            return False

        try:
            from core.embedding_generator import get_embedding_generator
            emb_gen = get_embedding_generator()

            if not emb_gen.is_available():
                logger.warning("Embedding服务不可用")
                return False

            # 生成embedding
            embedding = emb_gen.generate(text)
            if embedding is None:
                logger.error(f"生成embedding失败: summary_id={summary_id}")
                return False

            # 添加到ChromaDB
            self.collection.add(
                ids=[str(summary_id)],
                embeddings=[embedding],
                documents=[text],
                metadatas=[metadata]
            )

            logger.info(f"成功添加向量: summary_id={summary_id}")
            return True

        except Exception as e:
            logger.error(f"添加向量失败: {type(e).__name__}: {e}")
            return False

    def search_similar(self, query: str, top_k: int = 20,
                      filter_metadata: Optional[Dict] = None) -> List[Dict[str, Any]]:
        """
        语义搜索相似的总结

        Args:
            query: 查询文本
            top_k: 返回结果数量
            filter_metadata: 元数据过滤条件（如{"channel_id": "xxx"}）

        Returns:
            匹配的总结列表，每个包含summary_id, summary_text, metadata, distance
        """
        if not self.collection:
            logger.warning("向量存储不可用")
            return []

        try:
            from core.embedding_generator import get_embedding_generator
            emb_gen = get_embedding_generator()

            if not emb_gen.is_available():
                logger.warning("Embedding服务不可用")
                return []

            # 生成查询向量
            query_embedding = emb_gen.generate(query)
            if query_embedding is None:
                logger.error("生成查询向量失败")
                return []

            # 构建查询参数
            query_params = {
                "query_embeddings": [query_embedding],
                "n_results": top_k,
                "include": ["metadatas", "documents", "distances"]
            }

            # 添加过滤条件
            if filter_metadata:
                query_params["where"] = filter_metadata

            # 执行搜索
            results = self.collection.query(**query_params)

            # 格式化结果
            summaries = []
            if results and results['ids'] and len(results['ids']) > 0:
                for i in range(len(results['ids'][0])):
                    summary_id = int(results['ids'][0][i])
                    text = results['documents'][0][i]
                    metadata = results['metadatas'][0][i] if results['metadatas'] else {}
                    distance = results['distances'][0][i] if results['distances'] else 0

                    summaries.append({
                        'summary_id': summary_id,
                        'summary_text': text,
                        'metadata': metadata,
                        'distance': distance,
                        'similarity': 1 - distance  # 余弦距离转相似度
                    })

            logger.info(f"语义搜索完成: 找到 {len(summaries)} 条结果")
            return summaries

        except Exception as e:
            logger.error(f"语义搜索失败: {type(e).__name__}: {e}")
            return []

    def delete_summary(self, summary_id: int) -> bool:
        """
        删除总结向量

        Args:
            summary_id: 总结ID

        Returns:
            是否成功
        """
        if not self.collection:
            return False

        try:
            self.collection.delete(ids=[str(summary_id)])
            logger.info(f"成功删除向量: summary_id={summary_id}")
            return True

        except Exception as e:
            logger.error(f"删除向量失败: {type(e).__name__}: {e}")
            return False

    def get_stats(self) -> Dict[str, Any]:
        """
        获取向量存储统计信息

        Returns:
            统计信息字典
        """
        if not self.collection:
            return {"available": False}

        try:
            count = self.collection.count()
            return {
                "available": True,
                "total_vectors": count
            }

        except Exception as e:
            logger.error(f"获取统计信息失败: {type(e).__name__}: {e}")
            return {"available": True, "error": str(e)}


# 创建全局向量存储实例
vector_store = None

def get_vector_store():
    """获取全局向量存储实例"""
    global vector_store
    if vector_store is None:
        vector_store = VectorStore()
    return vector_store