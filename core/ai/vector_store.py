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
向量存储管理器 - 双后端门面

通过环境变量 VECTOR_BACKEND 选择后端（strip+lower；未知值告警后回退 chroma）：
- chroma（默认）：ChromaDB 持久化向量库；chromadb 延迟至本后端初始化时才 import，
  因此 lite 模式绝不会加载 chromadb。
- lite：SQLite(WAL) 单文件 + numpy 内存矩阵暴力余弦（实现见 core/ai/vector_lite.py）。

纯读者进程（QA 等）设置 VECTOR_BACKEND_READER=1（或构造时传 reader=True）：
lite 后端以 mode=ro 只读连接打开、绝不执行 DDL/写事务，库缺失或迁移未完成时
is_available() 返回 False 降级，而非静默创建空库。

本模块保持既有对外契约不变：VectorStore 类、get_vector_store() 懒加载单例、
模块属性 vector_store 可被外部置 None（tests 依赖）、搜索结果形状逐字段兼容、
collection/messages_collection 属性与 WebUI 路由的触点（count/get/delete）兼容。
"""

import logging
import os
import re
from datetime import UTC
from typing import Any

from core.ai.vector_lite import (
    TABLE_MESSAGES,
    TABLE_SUMMARIES,
    CollectionAdapter,
    LiteEngine,
)

logger = logging.getLogger(__name__)

_VALID_BACKENDS = ("chroma", "lite")

# 纯读者进程标记（QA 进程由 qa_bot.py 在启动时 setdefault 置 1）
_READER_ENV = "VECTOR_BACKEND_READER"
_READER_TRUTHY = {"1", "true", "yes", "on"}


class VectorStore:
    """向量存储管理器"""

    def __init__(self, reader: bool | None = None):
        """初始化向量存储（构造永不抛异常，任何失败降级为不可用）

        Args:
            reader: 纯读者模式；None（缺省）时读环境变量 VECTOR_BACKEND_READER。
                仅影响 lite 后端（mode=ro 只读连接、不执行 DDL/写事务）。
        """
        self.backend = self._resolve_backend()
        self.embedding_dimension = self._resolve_embedding_dimension()
        self.readonly = self._resolve_reader(reader)

        self.client = None
        self.collection = None
        self.messages_collection = None
        self._engine: LiteEngine | None = None

        if self.backend == "lite":
            self._init_lite_backend()
        else:
            self._init_chroma_backend()

    @staticmethod
    def _resolve_backend() -> str:
        """解析 VECTOR_BACKEND（strip+lower；未知值告警后回退 chroma）"""
        backend = os.getenv("VECTOR_BACKEND", "chroma").strip().lower()
        if backend not in _VALID_BACKENDS:
            logger.warning(f"未知 VECTOR_BACKEND={backend!r}，回退为 chroma")
            return "chroma"
        return backend

    @staticmethod
    def _resolve_reader(explicit: bool | None) -> bool:
        """解析读者模式：显式参数优先，否则读 VECTOR_BACKEND_READER（真值 1/true/yes/on）"""
        if explicit is not None:
            return bool(explicit)
        return os.getenv(_READER_ENV, "").strip().lower() in _READER_TRUTHY

    @staticmethod
    def _resolve_embedding_dimension() -> int:
        """解析 EMBEDDING_DIMENSION（lite 后端用于写入/加载/查询的真实维度校验）"""
        try:
            return int(os.getenv("EMBEDDING_DIMENSION", "1024"))
        except ValueError:
            logger.warning("EMBEDDING_DIMENSION 不是合法整数，回退为 1024")
            return 1024

    def _init_chroma_backend(self) -> None:
        """chroma 后端初始化（chromadb 延迟至此才 import）"""
        if self.readonly:
            # chroma PersistentClient 无只读打开方式，reader 标志仅对 lite 后端生效
            logger.info("chroma 后端不支持只读模式，reader 标志被忽略，按读写打开")
        try:
            import chromadb

            # 获取配置
            vector_db_path = os.getenv("VECTOR_DB_PATH", "data/vectors")

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

    def _init_lite_backend(self) -> None:
        """lite 后端初始化（引擎构造不做磁盘 I/O；连接/schema 校验延迟到首次操作）"""
        try:
            vector_db_path = os.getenv("VECTOR_DB_PATH", "data/vectors")
            db_path = os.path.join(vector_db_path, "lite_index.sqlite3")
            engine = LiteEngine(db_path, self.embedding_dimension, readonly=self.readonly)
            self._engine = engine
            self.client = engine
            self.collection = CollectionAdapter(engine, TABLE_SUMMARIES)
            self.messages_collection = CollectionAdapter(engine, TABLE_MESSAGES)
            logger.info(
                f"向量存储初始化成功(lite): {db_path}"
                + ("（只读读者模式）" if self.readonly else "")
            )
        except Exception as e:
            logger.error(f"向量存储初始化失败: {type(e).__name__}: {e}")
            self.client = None
            self.collection = None
            self.messages_collection = None
            self._engine = None

    def is_available(self) -> bool:
        """检查向量存储是否可用

        chroma 后端：等价旧的 self.collection is not None；
        lite 后端：引擎连接/建表/版本号可读（失败不锁存，每次重新尝试）。
        """
        if self._engine is not None:
            return self._engine.is_ready(TABLE_SUMMARIES)
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

            # 生成embedding（网络调用，lite 引擎锁之外执行）
            embedding = emb_gen.generate(text)
            if embedding is None:
                logger.error(f"生成embedding失败: summary_id={summary_id}")
                return False

            if self.backend == "lite":
                # 重复 id 抛 IntegrityError / 维度不符抛 ValueError → 下方 except 返回 False
                self._engine.insert(TABLE_SUMMARIES, str(summary_id), embedding, text, metadata)
            else:
                # 添加到ChromaDB（用 add 非 upsert：重复 id 抛异常）
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
                        仅返回 created_at >= date_after 的结果（naive 值按 UTC 处理）
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
            if self.backend == "lite":
                # 引擎删除幂等（不校验存在性），复刻 chroma delete 语义
                self._engine.delete(TABLE_SUMMARIES, [str(summary_id)])
            else:
                self.collection.delete(ids=[str(summary_id)])
            logger.info(f"成功删除向量: summary_id={summary_id}")
            return True

        except Exception as e:
            logger.error(f"删除向量失败: {type(e).__name__}: {e}")
            return False

    # ── Messages Collection 方法 ──────────────────────────────────────────

    def is_messages_available(self) -> bool:
        """检查消息向量存储是否可用（语义同 is_available）"""
        if self._engine is not None:
            return self._engine.is_ready(TABLE_MESSAGES)
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
            if self.backend == "lite":
                # 重复 id 抛 IntegrityError → 下方 except 返回 False
                self._engine.insert(TABLE_MESSAGES, str(message_id), embedding, text, metadata)
            else:
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
            if self.backend == "lite":
                # 单事务批量 upsert（幂等）；任一行维度不符抛 ValueError → except 返回 0
                # strict=True：入参列表长度不齐时抛 ValueError（对齐 chroma 校验失败整批拒绝）
                self._engine.upsert_many(
                    TABLE_MESSAGES,
                    list(zip(ids, embeddings, texts, metadatas, strict=True)),
                )
            else:
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
            if self.backend == "lite":
                self._engine.delete(TABLE_MESSAGES, [str(message_id)])
            else:
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
            if self.backend == "lite":
                self._engine.upsert_many(
                    TABLE_MESSAGES, [(str(message_id), embedding, text, metadata)]
                )
            else:
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
            collection: chroma collection 实例（chroma 后端）或 CollectionAdapter（lite 后端）
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

        # embedding 生成是网络调用，必须发生在 lite 引擎锁之外（引擎仅在搜索段持锁）
        query_embedding = emb_gen.generate(query)
        if query_embedding is None:
            return []

        need_date_filter = date_after or date_before

        if self.backend == "lite":
            # lite：无日期过滤时引擎内直接截 top_k；有日期过滤时取全量过滤集，
            # 截断推迟到下方日期过滤之后（不复刻 chroma 路径的 top_k*3 超采）
            rows = collection.engine.search(
                collection.table,
                query_embedding,
                limit=None if need_date_filter else top_k,
                filter_metadata=filter_metadata,
            )
            ids_seq = [row["doc_id"] for row in rows]
            documents_seq = [row["document"] for row in rows]
            metadatas_seq = [row["metadata"] for row in rows]
            distances_seq = [1.0 - row["similarity"] for row in rows]
        else:
            # 构建 where 过滤条件
            where_conditions = []
            if filter_metadata:
                for k, v in filter_metadata.items():
                    where_conditions.append({k: {"$eq": v}})

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

            if not (results and results["ids"] and len(results["ids"]) > 0):
                return []
            ids_seq = results["ids"][0]
            documents_seq = results["documents"][0] if results["documents"] else []
            metadatas_seq = results["metadatas"][0] if results["metadatas"] else []
            distances_seq = results["distances"][0] if results["distances"] else []

        # 格式化结果（结果形状与 doc_id 解析规则自 chroma 版逐行移植，语义不得改动）
        formatted = []
        for i in range(len(ids_seq)):
            doc_id = ids_seq[i]
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

            metadata = metadatas_seq[i] if metadatas_seq else {}
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
                    "summary_text": documents_seq[i],
                    "metadata": metadata,
                    "distance": distances_seq[i] if distances_seq else 0,
                    "similarity": 1 - distances_seq[i] if distances_seq else 0,
                    "doc_id": doc_id,  # 保留原始 ID
                }
            )

        # 后置日期过滤
        if need_date_filter:
            from datetime import datetime

            def _parse_dt(s: str) -> datetime | None:
                try:
                    parsed = datetime.fromisoformat(s)
                except (ValueError, TypeError):
                    return None
                if parsed.tzinfo is None:
                    # naive 值按 UTC 补时区（有意行为差异：修复 naive vs aware 比较抛
                    # TypeError 导致整体返回 [] 的陷阱；date 参数为 LLM 透传，格式不可控）
                    parsed = parsed.replace(tzinfo=UTC)
                return parsed

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
