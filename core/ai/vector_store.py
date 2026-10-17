# Copyright 2026 Sakura-Bot
#
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
#
# - 署名：必须提供本项目的原始来源链接
# - 相同方式共享：衍生作品必须采用相同的许可证
#
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
# 许可证全文：参见 LICENSE 文件

"""
向量存储管理器 - 使用ChromaDB存储和检索向量
"""

import logging
import os
import re
from typing import Any

logger = logging.getLogger(__name__)

try:
    import chromadb

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
            self.messages_collection = None
            return

        try:
            # 获取配置
            vector_db_path = os.getenv("VECTOR_DB_PATH", "data/vectors")
            int(os.getenv("EMBEDDING_DIMENSION", "1024"))

            # 创建持久化客户端
            self.client = chromadb.PersistentClient(path=vector_db_path)

            # 获取或创建 summaries collection（总结向量）
            self.collection = self.client.get_or_create_collection(
                name="summaries",
                metadata={"hnsw:space": "cosine"},  # 使用余弦相似度
            )

            # 获取或创建 messages collection（频道原始消息向量）
            self.messages_collection = self.client.get_or_create_collection(
                name="messages",
                metadata={"hnsw:space": "cosine"},
            )

            logger.info(f"向量存储初始化成功: {vector_db_path}")

        except Exception as e:
            logger.error(f"向量存储初始化失败: {type(e).__name__}: {e}")
            self.client = None
            self.collection = None
            self.messages_collection = None

    def is_available(self) -> bool:
        """检查向量存储是否可用"""
        return self.collection is not None

    def add_summary(self, summary_id: int, text: str, metadata: dict[str, Any]) -> bool:
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
            from core.ai.embedding_generator import get_embedding_generator

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
                metadatas=[metadata],
            )

            logger.info(f"成功添加向量: summary_id={summary_id}")
            return True

        except Exception as e:
            logger.error(f"添加向量失败: {type(e).__name__}: {e}")
            return False

    def search_similar(
        self,
        query: str,
        top_k: int = 20,
        filter_metadata: dict | None = None,
        date_after: str | None = None,
        date_before: str | None = None,
    ) -> list[dict[str, Any]]:
        """
        语义搜索相似的总结

        Args:
            query: 查询文本
            top_k: 返回结果数量
            filter_metadata: 元数据过滤条件（如{"channel_id": "xxx"}）
            date_after: 时间下限，ISO格式字符串（如 "2024-01-01T00:00:00"），
                        仅返回 created_at >= date_after 的结果
            date_before: 时间上限，ISO格式字符串，仅返回 created_at <= date_before 的结果

        Returns:
            匹配的总结列表，每个包含summary_id, summary_text, metadata, distance
        """
        if not self.collection:
            logger.warning("向量存储不可用")
            return []

        try:
            return self._search_collection(
                collection=self.collection,
                query=query,
                top_k=top_k,
                filter_metadata=filter_metadata,
                date_after=date_after,
                date_before=date_before,
            )
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

    # ── Messages Collection 方法 ──────────────────────────────────────────

    def is_messages_available(self) -> bool:
        """检查消息向量存储是否可用"""
        return self.messages_collection is not None

    def add_message(
        self, message_id: int, text: str, metadata: dict[str, Any], embedding: list[float]
    ) -> bool:
        """
        添加频道消息向量到 messages collection

        Args:
            message_id: 消息ID（使用 channel_id:message_id 组合确保唯一）
            text: 消息文本
            metadata: 元数据（channel_id, channel_name, created_at, sender_id）
            embedding: 预生成的向量（由调用方批量生成）

        Returns:
            是否成功
        """
        if not self.messages_collection:
            logger.warning("消息向量存储不可用")
            return False

        try:
            self.messages_collection.add(
                ids=[str(message_id)],
                embeddings=[embedding],
                documents=[text],
                metadatas=[metadata],
            )
            logger.debug(f"成功添加消息向量: message_id={message_id}")
            return True

        except Exception as e:
            logger.error(f"添加消息向量失败: {type(e).__name__}: {e}")
            return False

    def add_messages_batch(
        self,
        ids: list[str],
        texts: list[str],
        metadatas: list[dict[str, Any]],
        embeddings: list[list[float]],
    ) -> int:
        """
        批量添加频道消息向量

        Args:
            ids: 消息ID列表
            texts: 消息文本列表
            metadatas: 元数据列表
            embeddings: 向量列表

        Returns:
            成功添加的数量
        """
        if not self.messages_collection:
            logger.warning("消息向量存储不可用")
            return 0

        try:
            self.messages_collection.upsert(
                ids=ids,
                embeddings=embeddings,
                documents=texts,
                metadatas=metadatas,
            )
            logger.info(f"批量添加消息向量: {len(ids)} 条")
            return len(ids)

        except Exception as e:
            logger.error(f"批量添加消息向量失败: {type(e).__name__}: {e}")
            return 0

    def search_messages(
        self,
        query: str,
        top_k: int = 20,
        filter_metadata: dict | None = None,
        date_after: str | None = None,
        date_before: str | None = None,
    ) -> list[dict[str, Any]]:
        """
        语义搜索频道消息

        Args:
            query: 查询文本
            top_k: 返回结果数量
            filter_metadata: 元数据过滤条件
            date_after: 时间下限，ISO格式
            date_before: 时间上限，ISO格式

        Returns:
            匹配的消息列表
        """
        if not self.messages_collection:
            return []

        try:
            return self._search_collection(
                collection=self.messages_collection,
                query=query,
                top_k=top_k,
                filter_metadata=filter_metadata,
                date_after=date_after,
                date_before=date_before,
            )
        except Exception as e:
            logger.error(f"搜索消息向量失败: {type(e).__name__}: {e}")
            return []

    def search_all(
        self,
        query: str,
        top_k: int = 20,
        filter_metadata: dict | None = None,
        date_after: str | None = None,
        date_before: str | None = None,
    ) -> list[dict[str, Any]]:
        """
        同时搜索 summaries 和 messages 两个 collection，合并结果按相似度排序

        Args:
            query: 查询文本
            top_k: 最终返回结果数量
            filter_metadata: 元数据过滤条件
            date_after: 时间下限，ISO格式
            date_before: 时间上限，ISO格式

        Returns:
            合并后的结果列表，按相似度降序排列，截取 top_k
        """
        all_results = []

        # 搜索 summaries
        if self.collection:
            try:
                summary_results = self._search_collection(
                    collection=self.collection,
                    query=query,
                    top_k=top_k,
                    filter_metadata=filter_metadata,
                    date_after=date_after,
                    date_before=date_before,
                )
                for r in summary_results:
                    r["source"] = "summary"
                all_results.extend(summary_results)
            except Exception as e:
                logger.error(f"搜索summaries失败: {type(e).__name__}: {e}")

        # 搜索 messages
        if self.messages_collection:
            try:
                message_results = self._search_collection(
                    collection=self.messages_collection,
                    query=query,
                    top_k=top_k,
                    filter_metadata=filter_metadata,
                    date_after=date_after,
                    date_before=date_before,
                )
                for r in message_results:
                    r["source"] = "message"
                all_results.extend(message_results)
            except Exception as e:
                logger.error(f"搜索messages失败: {type(e).__name__}: {e}")

        # 按相似度降序排序，截取 top_k
        all_results.sort(key=lambda x: x.get("similarity", 0), reverse=True)
        return all_results[:top_k]

    def delete_message(self, message_id: int | str) -> bool:
        """
        删除消息向量

        Args:
            message_id: 消息ID（支持 int 或 "channel_id:msg_id" 格式的字符串）

        Returns:
            是否成功
        """
        if not self.messages_collection:
            return False

        try:
            self.messages_collection.delete(ids=[str(message_id)])
            logger.info(f"成功删除消息向量: message_id={message_id}")
            return True

        except Exception as e:
            logger.error(f"删除消息向量失败: {type(e).__name__}: {e}")
            return False

    def update_message(
        self, message_id: int | str, text: str, metadata: dict[str, Any], embedding: list[float]
    ) -> bool:
        """
        更新消息向量（upsert）

        Args:
            message_id: 消息ID（支持 int 或 "channel_id:msg_id" 格式的字符串）
            text: 新的文本内容
            metadata: 新的元数据
            embedding: 新的向量

        Returns:
            是否成功
        """
        if not self.messages_collection:
            return False

        try:
            self.messages_collection.upsert(
                ids=[str(message_id)],
                embeddings=[embedding],
                documents=[text],
                metadatas=[metadata],
            )
            logger.debug(f"成功更新消息向量: message_id={message_id}")
            return True

        except Exception as e:
            logger.error(f"更新消息向量失败: {type(e).__name__}: {e}")
            return False

    # ── 内部通用搜索方法 ──────────────────────────────────────────────────

    def _search_collection(
        self,
        collection,
        query: str,
        top_k: int = 20,
        filter_metadata: dict | None = None,
        date_after: str | None = None,
        date_before: str | None = None,
    ) -> list[dict[str, Any]]:
        """
        通用 collection 搜索方法

        Args:
            collection: ChromaDB collection 实例
            query: 查询文本
            top_k: 返回结果数量
            filter_metadata: 元数据过滤条件
            date_after: 时间下限
            date_before: 时间上限

        Returns:
            匹配结果列表
        """
        from core.ai.embedding_generator import get_embedding_generator

        emb_gen = get_embedding_generator()

        if not emb_gen.is_available():
            return []

        query_embedding = emb_gen.generate(query)
        if query_embedding is None:
            return []

        # 构建 where 过滤条件
        where_conditions = []
        if filter_metadata:
            for k, v in filter_metadata.items():
                where_conditions.append({k: {"$eq": v}})

        need_date_filter = date_after or date_before
        fetch_k = top_k * 3 if need_date_filter else top_k

        query_params = {
            "query_embeddings": [query_embedding],
            "n_results": fetch_k,
            "include": ["metadatas", "documents", "distances"],
        }

        if len(where_conditions) == 1:
            query_params["where"] = where_conditions[0]
        elif len(where_conditions) > 1:
            query_params["where"] = {"$and": where_conditions}

        # 检查文档数量
        try:
            total_count = collection.count()
            if total_count == 0:
                return []
            if fetch_k > total_count:
                query_params["n_results"] = total_count
        except Exception as e:
            logger.warning(f"获取collection文档数量失败: {type(e).__name__}: {e}")

        results = collection.query(**query_params)

        # 格式化结果
        formatted = []
        if results and results["ids"] and len(results["ids"]) > 0:
            for i in range(len(results["ids"][0])):
                doc_id = results["ids"][0][i]
                # 区分两种文档 ID：
                # - summaries collection：数字 ID，即真实的数据库总结ID
                # - messages collection：字符串 "channel_id:message_id"，
                #   其中 message_id 是真实的 Telegram 频道消息 ID。
                # 历史 Bug：曾把字符串 ID 的 md5 哈希当作 summary_id 返回，
                # 模型会把这个内部哈希当成消息 ID 拼进链接。现在：
                # - summary_id 仅在数字 ID（真实总结）时填充
                # - 消息文档解析出真实 message_id，供链接构造使用
                summary_id = None
                message_id = None
                try:
                    summary_id = int(doc_id)
                except (ValueError, TypeError):
                    match = re.search(r":(\d+)$", str(doc_id))
                    if match:
                        try:
                            message_id = int(match.group(1))
                        except ValueError:
                            message_id = None

                metadata = results["metadatas"][0][i] if results["metadatas"] else {}
                # 元数据中的 message_id 优先（入库时显式保存的真实消息ID）
                if message_id is None and metadata.get("message_id"):
                    try:
                        message_id = int(metadata["message_id"])
                    except (TypeError, ValueError):
                        message_id = None

                formatted.append(
                    {
                        "summary_id": summary_id,
                        "message_id": message_id,
                        "summary_text": results["documents"][0][i],
                        "metadata": metadata,
                        "distance": results["distances"][0][i] if results["distances"] else 0,
                        "similarity": (
                            1 - results["distances"][0][i] if results["distances"] else 0
                        ),
                        "doc_id": doc_id,  # 保留原始 ID
                    }
                )

        # 后置日期过滤
        if need_date_filter:
            from datetime import datetime

            def _parse_dt(s: str) -> datetime | None:
                try:
                    return datetime.fromisoformat(s)
                except (ValueError, TypeError):
                    return None

            after_dt = _parse_dt(date_after) if date_after else None
            before_dt = _parse_dt(date_before) if date_before else None

            filtered = []
            for s in formatted:
                created_str = s["metadata"].get("created_at", "")
                created_dt = _parse_dt(created_str)
                if after_dt and (created_dt is None or created_dt < after_dt):
                    continue
                if before_dt and (created_dt is None or created_dt > before_dt):
                    continue
                filtered.append(s)
            formatted = filtered[:top_k]

        return formatted

    def get_stats(self) -> dict[str, Any]:
        """
        获取向量存储统计信息

        Returns:
            统计信息字典，包含 summaries 和 messages 两个 collection 的统计
        """
        stats = {"available": False, "summaries": {}, "messages": {}}

        # Summaries collection 统计
        if self.collection:
            try:
                count = self.collection.count()
                stats["summaries"] = {"available": True, "total_vectors": count}
                stats["available"] = True
            except Exception as e:
                logger.error(f"获取summaries统计失败: {type(e).__name__}: {e}")
                stats["summaries"] = {"available": True, "error": str(e)}

        # Messages collection 统计
        if self.messages_collection:
            try:
                count = self.messages_collection.count()
                stats["messages"] = {"available": True, "total_vectors": count}
                stats["available"] = True
            except Exception as e:
                logger.error(f"获取messages统计失败: {type(e).__name__}: {e}")
                stats["messages"] = {"available": True, "error": str(e)}

        # 兼容旧接口
        if self.collection and "total_vectors" not in stats["summaries"]:
            stats["total_vectors"] = 0
        elif self.collection:
            stats["total_vectors"] = stats["summaries"].get("total_vectors", 0)

        return stats


# 创建全局向量存储实例
vector_store = None


def get_vector_store():
    """获取全局向量存储实例"""
    global vector_store
    if vector_store is None:
        vector_store = VectorStore()
    return vector_store
