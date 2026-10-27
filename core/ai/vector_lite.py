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
轻量向量存储引擎 - SQLite(WAL) BLOB 存储 + numpy 内存矩阵暴力余弦搜索

设计要点：
- 存储布局：单文件 SQLite（WAL 模式），位于 VECTOR_DB_PATH 目录内
  （默认 data/vectors/lite_index.sqlite3）；BLOB 为显式小端 float32（"<f4"），
  每行携带 dim 列供写入与加载时校验；store_meta 存放 schema/集合版本号。
- 计算模型：进程内 numpy float32 矩阵，行向量预归一化，余弦相似度 = 矩阵 @ 归一化查询向量，
  距离 = 1 - 相似度；暴力全量打分 + argpartition 截取 top_k（精确解，非 HNSW 近似）。
- 一致性：SQLite 为唯一事实源。per-collection 版本号存于 store_meta，每次写事务内同步自增；
  搜索前检查版本号，不一致即整表重载内存矩阵，保证"别进程写过后我第一次搜就能看到"。
- 并发：进程内 threading.RLock 全方法串行（仅覆盖 SQLite 操作段，embedding 网络调用在锁外）；
  跨进程依赖 WAL（读不阻塞写）+ busy_timeout。
- 初始化失败不锁存：连接失败/表缺失时 is_ready() 返回 False，但每次操作都会重新尝试建连。
- id 格式契约不可变：summaries 为 str(MySQL summaries.id)，messages 为
  f"{channel_id}:{message_id}"（尾部数字即真实 Telegram 消息 ID，被搜索结果解析依赖）。
"""

from __future__ import annotations

import json
import logging
import os
import sqlite3
import threading
from dataclasses import dataclass
from datetime import UTC, datetime

logger = logging.getLogger(__name__)

try:
    import numpy as np

    NUMPY_AVAILABLE = True
except ImportError:  # pragma: no cover - numpy 为硬依赖，此分支仅为优雅降级兜底
    NUMPY_AVAILABLE = False
    np = None
    logger.warning("numpy 未安装，lite 向量后端不可用")

# 集合 -> SQLite 表名（映射固定，搜索结果的 doc_id 解析与 WebUI 路由均依赖集合语义）
TABLE_SUMMARIES = "summaries_vectors"
TABLE_MESSAGES = "messages_vectors"

VERSION_KEYS = {
    TABLE_SUMMARIES: "summaries_version",
    TABLE_MESSAGES: "messages_version",
}

SCHEMA_VERSION = 1

# WAL 依赖同机文件系统（容器 data/ 为本地挂载，满足）；若未来 data/ 变为 NFS，
# WAL 会损坏，可将此常量回退为 "DELETE"。
_JOURNAL_MODE = "WAL"
_BUSY_TIMEOUT_MS = 5000

# 与 scripts/migrate_vectors_to_sqlite.py 中的 DDL 保持一致（schema_version=1）
_SCHEMA_STATEMENTS = (
    """
    CREATE TABLE IF NOT EXISTS store_meta (
        key   TEXT PRIMARY KEY,
        value INTEGER NOT NULL
    )
    """,
    """
    CREATE TABLE IF NOT EXISTS summaries_vectors (
        id            TEXT PRIMARY KEY,
        embedding     BLOB NOT NULL,
        document      TEXT NOT NULL,
        metadata_json TEXT NOT NULL,
        dim           INTEGER NOT NULL,
        stored_at     TEXT NOT NULL
    ) WITHOUT ROWID
    """,
    """
    CREATE TABLE IF NOT EXISTS messages_vectors (
        id            TEXT PRIMARY KEY,
        embedding     BLOB NOT NULL,
        document      TEXT NOT NULL,
        metadata_json TEXT NOT NULL,
        dim           INTEGER NOT NULL,
        stored_at     TEXT NOT NULL
    ) WITHOUT ROWID
    """,
)


@dataclass
class _CollectionState:
    """单个集合的内存态（与磁盘 store_meta 版本号对齐）"""

    ids: list[str]
    matrix: np.ndarray | None  # (N, dim) float32，行向量已归一化；None 表示尚未加载
    documents: list[str]
    metadatas: list[dict]
    version: int = -1  # 已加载的 store_meta 版本号，-1 表示尚未加载/缓存已作废


class LiteEngine:
    """SQLite(WAL) + numpy 内存矩阵的轻量向量引擎（chroma 的进程内替代品）

    主进程内单实例持有一个读写连接（RLock 串行化）；跨进程读者（QA 等）经 WAL
    并发读取同一文件。所有公开方法同步阻塞；失败不锁存，每次操作重新尝试建连。
    """

    def __init__(self, db_path: str, expected_dim: int):
        """初始化引擎

        构造只保存参数，不做任何磁盘 I/O；真正的连接/建表延迟到首次操作
        （保持与 chroma 版一致的"构造永不抛异常"边界，由调用方 try/except 兜底）。

        Args:
            db_path: SQLite 数据库文件路径（含 lite_index.sqlite3 文件名）
            expected_dim: 向量维度（来自 EMBEDDING_DIMENSION），写入/加载/查询时校验

        Raises:
            RuntimeError: numpy 不可用时抛出（调用方降级为后端不可用）
        """
        if not NUMPY_AVAILABLE:
            raise RuntimeError("numpy 未安装，lite 向量后端不可用")
        self.db_path = db_path
        self.expected_dim = int(expected_dim)
        self._lock = threading.RLock()
        self._conn: sqlite3.Connection | None = None
        self._schema_ready = False
        self._states: dict[str, _CollectionState] = {
            table: _CollectionState(ids=[], matrix=None, documents=[], metadatas=[])
            for table in (TABLE_SUMMARIES, TABLE_MESSAGES)
        }

    # ── 连接与模式管理 ────────────────────────────────────────────────────

    def _close_connection(self) -> None:
        """关闭并丢弃当前连接（失败后重建，不锁存错误）"""
        conn, self._conn = self._conn, None
        self._schema_ready = False
        if conn is not None:
            try:
                conn.close()
            except Exception:  # 关闭失败无需上抛
                pass

    def _ensure_connection(self) -> sqlite3.Connection:
        """确保连接可用；失败抛 sqlite3.Error，由重试框架决定是否重建重试"""
        if self._conn is not None:
            return self._conn
        parent = os.path.dirname(os.path.abspath(self.db_path))
        # chroma PersistentClient 同样会创建目录，保持等价行为
        os.makedirs(parent, exist_ok=True)
        conn = sqlite3.connect(
            self.db_path, check_same_thread=False, timeout=_BUSY_TIMEOUT_MS / 1000
        )
        conn.isolation_level = None  # 关闭隐式事务，写事务用显式 BEGIN IMMEDIATE/COMMIT 管理
        conn.execute(f"PRAGMA journal_mode={_JOURNAL_MODE}")
        conn.execute(f"PRAGMA busy_timeout={_BUSY_TIMEOUT_MS}")
        conn.execute("PRAGMA synchronous=NORMAL")
        conn.execute("PRAGMA foreign_keys=OFF")
        self._conn = conn
        self._schema_ready = False
        return conn

    def _ensure_schema(self, conn: sqlite3.Connection) -> None:
        """建表与初始化版本号（writer 首写建表；幂等，单连接生命周期内只执行一次）"""
        if self._schema_ready:
            return
        for stmt in _SCHEMA_STATEMENTS:
            conn.execute(stmt)
        conn.execute(
            "INSERT OR IGNORE INTO store_meta (key, value) VALUES (?, ?), (?, ?), (?, ?)",
            (
                "schema_version",
                SCHEMA_VERSION,
                VERSION_KEYS[TABLE_SUMMARIES],
                1,
                VERSION_KEYS[TABLE_MESSAGES],
                1,
            ),
        )
        self._schema_ready = True

    def _run_with_retry(self, op):
        """在锁内执行 SQLite 操作段；连接级异常时重建连接并重试一次（不锁存失败）"""
        try:
            return op()
        except sqlite3.IntegrityError:
            # 约束冲突（如主键重复）是确定性错误，重建连接重试无意义，直接上抛
            raise
        except sqlite3.Error as e:
            logger.warning(f"SQLite 操作失败，重建连接后重试: {type(e).__name__}: {e}")
            self._close_connection()
            return op()

    @staticmethod
    def _safe_rollback(conn: sqlite3.Connection) -> None:
        """尽力回滚当前事务（无活动事务时静默忽略）"""
        try:
            conn.execute("ROLLBACK")
        except sqlite3.Error:
            pass

    def _read_version(self, conn: sqlite3.Connection, table: str) -> int:
        """读取集合版本号（微秒级，搜索前用于失效检查）"""
        row = conn.execute(
            "SELECT value FROM store_meta WHERE key = ?", (VERSION_KEYS[table],)
        ).fetchone()
        return int(row[0]) if row else 0

    @staticmethod
    def _bump_version(conn: sqlite3.Connection, table: str) -> None:
        """写事务内自增集合版本号（与数据变更同事务，原子生效）"""
        conn.execute(
            "UPDATE store_meta SET value = value + 1 WHERE key = ?", (VERSION_KEYS[table],)
        )

    # ── 向量编解码 ────────────────────────────────────────────────────────

    def _as_vector(self, embedding) -> np.ndarray:
        """转换为小端 float32 一维向量并校验维度

        Raises:
            ValueError: 维度与 expected_dim 不符（复刻 chroma 维度校验异常语义）
        """
        arr = np.asarray(embedding, dtype="<f4").reshape(-1)
        if arr.shape[0] != self.expected_dim:
            raise ValueError(f"向量维度不匹配: 期望 {self.expected_dim}，实际 {arr.shape[0]}")
        return arr

    def _encode_embedding(self, embedding) -> bytes:
        """编码为 BLOB（显式小端 float32，跨语言契约的一部分）"""
        return self._as_vector(embedding).tobytes()

    @staticmethod
    def _normalize(arr: np.ndarray) -> np.ndarray:
        """归一化（零向量以 1e-12 兜底，复刻设计约定的 max(norm, 1e-12)）"""
        return arr / max(float(np.linalg.norm(arr)), 1e-12)

    # ── 内存矩阵加载与同步 ────────────────────────────────────────────────

    def _get_fresh_state(self, conn: sqlite3.Connection, table: str) -> _CollectionState:
        """检查 store_meta 版本号，必要时整表重载内存矩阵（须持有 _lock）"""
        state = self._states[table]
        version = self._read_version(conn, table)
        if state.matrix is not None and state.version == version:
            return state
        self._load_state(conn, table, state, version)
        return state

    def _load_state(
        self, conn: sqlite3.Connection, table: str, state: _CollectionState, version: int
    ) -> None:
        """整表加载内存矩阵（行向量预归一化；维度/元数据异常行跳过并告警）"""
        rows = conn.execute(
            f"SELECT id, embedding, document, metadata_json, dim FROM {table} ORDER BY id"
        ).fetchall()
        ids: list[str] = []
        documents: list[str] = []
        metadatas: list[dict] = []
        vectors: list[np.ndarray] = []
        for doc_id, blob, document, metadata_json, dim in rows:
            if dim != self.expected_dim or len(blob) != self.expected_dim * 4:
                logger.warning(f"lite 加载跳过维度异常行: table={table} id={doc_id} dim={dim}")
                continue
            try:
                metadata = json.loads(metadata_json)
            except (ValueError, TypeError):
                logger.warning(f"lite 加载跳过元数据损坏行: table={table} id={doc_id}")
                continue
            vectors.append(np.frombuffer(blob, dtype="<f4"))
            ids.append(doc_id)
            documents.append(document)
            metadatas.append(metadata)
        if vectors:
            matrix = np.stack(vectors).astype("<f4", copy=False)
            norms = np.linalg.norm(matrix, axis=1, keepdims=True)
            matrix = matrix / np.maximum(norms, 1e-12)
        else:
            matrix = np.zeros((0, self.expected_dim), dtype="<f4")
        state.ids = ids
        state.documents = documents
        state.metadatas = metadatas
        state.matrix = matrix
        state.version = version
        logger.debug(f"lite 内存矩阵加载完成: table={table} rows={len(ids)} version={version}")

    def _sync_memory_after_write(self, table: str, conn: sqlite3.Connection, apply_fn) -> None:
        """写事务提交后同步内存矩阵（增量更新，写路径零全量重载开销）

        内存同步失败仅作废缓存（下次读取全量重载），绝不让已提交的写事务报错。
        """
        state = self._states[table]
        try:
            if state.matrix is None:
                # 尚未加载过：直接作废，交由下次读取全量加载（自然包含本次写入）
                state.version = -1
                return
            apply_fn(state)
            state.version = self._read_version(conn, table)
        except Exception as e:
            logger.warning(f"lite 内存矩阵同步失败，将全量重载: {type(e).__name__}: {e}")
            state.version = -1
            state.matrix = None

    @staticmethod
    def _memory_insert_or_replace(
        state: _CollectionState, doc_id: str, vec: np.ndarray, document: str, metadata: dict
    ) -> None:
        """内存矩阵增量 upsert（已加载前提下调用）"""
        try:
            idx = state.ids.index(doc_id)
        except ValueError:
            state.ids.append(doc_id)
            state.documents.append(document)
            state.metadatas.append(metadata)
            state.matrix = np.vstack([state.matrix, vec.reshape(1, -1)])
        else:
            state.matrix[idx] = vec
            state.documents[idx] = document
            state.metadatas[idx] = metadata

    @staticmethod
    def _memory_remove(state: _CollectionState, doc_ids: set[str]) -> None:
        """内存矩阵批量删除（已加载前提下调用；一次布尔掩码重建，避免逐行拷贝）"""
        indices = [i for i, doc_id in enumerate(state.ids) if doc_id in doc_ids]
        if not indices:
            return
        keep = np.ones(state.matrix.shape[0], dtype=bool)
        keep[indices] = False
        state.matrix = state.matrix[keep]
        state.ids = [doc_id for i, doc_id in enumerate(state.ids) if keep[i]]
        state.documents = [doc for i, doc in enumerate(state.documents) if keep[i]]
        state.metadatas = [meta for i, meta in enumerate(state.metadatas) if keep[i]]

    # ── 对外操作（全部同步阻塞；锁仅覆盖 SQLite 操作段） ──────────────────

    def is_ready(self, table: str) -> bool:
        """集合是否就绪（连接 + 建表 + 版本号可读）；失败不锁存，每次调用重新尝试"""
        try:
            with self._lock:
                conn = self._ensure_connection()
                self._ensure_schema(conn)
                self._read_version(conn, table)
            return True
        except Exception as e:
            logger.debug(f"lite 向量集合未就绪: table={table} ({type(e).__name__}: {e})")
            return False

    def count(self, table: str) -> int:
        """权威行数（SELECT COUNT(*)，与磁盘一致，供 WebUI 统计与清空循环使用）"""
        with self._lock:

            def _op() -> int:
                conn = self._ensure_connection()
                self._ensure_schema(conn)
                row = conn.execute(f"SELECT COUNT(*) FROM {table}").fetchone()
                return int(row[0]) if row else 0

            return self._run_with_retry(_op)

    def get_rows(
        self,
        table: str,
        ids: list[str] | None = None,
        limit: int | None = None,
        offset: int | None = None,
    ) -> list[tuple]:
        """按 id 升序取原始行 (id, embedding BLOB, document, metadata_json)

        Args:
            table: 表名常量
            ids: 过滤的 id 列表；None 表示全表
            limit: LIMIT 数量；None 表示不限
            offset: OFFSET 偏移；None/0 视为 0

        Returns:
            行元组列表（顺序固定为 ORDER BY id，分页稳定；chroma 的近似插入序无调用方依赖）
        """
        with self._lock:

            def _op() -> list[tuple]:
                conn = self._ensure_connection()
                self._ensure_schema(conn)
                sql = f"SELECT id, embedding, document, metadata_json FROM {table}"
                params: list = []
                if ids:
                    placeholders = ", ".join("?" for _ in ids)
                    sql += f" WHERE id IN ({placeholders})"
                    params.extend(str(doc_id) for doc_id in ids)
                sql += " ORDER BY id"
                if limit is not None:
                    sql += " LIMIT ?"
                    params.append(int(limit))
                    if offset:
                        sql += " OFFSET ?"
                        params.append(int(offset))
                return conn.execute(sql, params).fetchall()

            return self._run_with_retry(_op)

    def insert(self, table: str, doc_id: str, embedding, document: str, metadata: dict) -> None:
        """插入单行

        主键冲突抛 sqlite3.IntegrityError（复刻 chroma add 对重复 id 抛异常的语义）；
        维度不符抛 ValueError。失败抛异常，由调用方降级。

        Args:
            table: 表名常量
            doc_id: 文档 id（str(summary_id) 或 f"{channel_id}:{message_id}"）
            embedding: 向量（长度须等于 expected_dim）
            document: 原文
            metadata: 元数据 dict
        """
        blob = self._encode_embedding(embedding)
        vec = self._normalize(self._as_vector(embedding))
        doc_id = str(doc_id)
        document = str(document)
        metadata_json = json.dumps(metadata, ensure_ascii=False, default=str)
        stored_at = datetime.now(UTC).isoformat()
        with self._lock:

            def _op() -> None:
                conn = self._ensure_connection()
                self._ensure_schema(conn)
                conn.execute("BEGIN IMMEDIATE")
                try:
                    conn.execute(
                        f"INSERT INTO {table} "
                        "(id, embedding, document, metadata_json, dim, stored_at) "
                        "VALUES (?, ?, ?, ?, ?, ?)",
                        (doc_id, blob, document, metadata_json, self.expected_dim, stored_at),
                    )
                    self._bump_version(conn, table)
                    conn.execute("COMMIT")
                except Exception:
                    self._safe_rollback(conn)
                    raise
                self._sync_memory_after_write(
                    table,
                    conn,
                    lambda state: self._memory_insert_or_replace(
                        state, doc_id, vec, document, metadata
                    ),
                )

            self._run_with_retry(_op)

    def upsert_many(self, table: str, rows: list[tuple]) -> int:
        """单事务批量 upsert（幂等，复刻 chroma upsert 语义）

        任一行维度不符抛 ValueError，整批不写入（复刻 chroma 对整批向量校验失败的行为）。

        Args:
            table: 表名常量
            rows: (doc_id, embedding, document, metadata) 元组列表

        Returns:
            写入行数
        """
        prepared = []
        for row_doc_id, row_embedding, row_document, row_metadata in rows:
            arr = self._as_vector(row_embedding)
            prepared.append(
                (
                    str(row_doc_id),
                    arr.tobytes(),
                    str(row_document),
                    json.dumps(row_metadata, ensure_ascii=False, default=str),
                    row_metadata,
                    self._normalize(arr),
                )
            )
        stored_at = datetime.now(UTC).isoformat()
        with self._lock:

            def _op() -> int:
                conn = self._ensure_connection()
                self._ensure_schema(conn)
                conn.execute("BEGIN IMMEDIATE")
                try:
                    for doc_id, blob, document, metadata_json, _meta, _vec in prepared:
                        conn.execute(
                            f"INSERT OR REPLACE INTO {table} "
                            "(id, embedding, document, metadata_json, dim, stored_at) "
                            "VALUES (?, ?, ?, ?, ?, ?)",
                            (doc_id, blob, document, metadata_json, self.expected_dim, stored_at),
                        )
                    self._bump_version(conn, table)
                    conn.execute("COMMIT")
                except Exception:
                    self._safe_rollback(conn)
                    raise

                def _apply(state: _CollectionState) -> None:
                    for doc_id, _blob, document, _mj, metadata, vec in prepared:
                        self._memory_insert_or_replace(state, doc_id, vec, document, metadata)

                self._sync_memory_after_write(table, conn, _apply)
                return len(prepared)

            return self._run_with_retry(_op)

    def delete(self, table: str, ids: list[str]) -> int:
        """幂等删除（不校验存在性，复刻 chroma delete 语义）

        Args:
            table: 表名常量
            ids: 文档 id 列表（WebUI 清空循环一次可达 1000 个，单语句直发）

        Returns:
            实际删除行数

        Raises:
            sqlite3.Error: 重试一次后仍失败的数据库错误
        """
        if not ids:
            return 0
        str_ids = [str(doc_id) for doc_id in ids]
        with self._lock:

            def _op() -> int:
                conn = self._ensure_connection()
                self._ensure_schema(conn)
                placeholders = ", ".join("?" for _ in str_ids)
                conn.execute("BEGIN IMMEDIATE")
                try:
                    cursor = conn.execute(
                        f"DELETE FROM {table} WHERE id IN ({placeholders})", str_ids
                    )
                    deleted = int(cursor.rowcount)
                    if deleted > 0:
                        self._bump_version(conn, table)
                    conn.execute("COMMIT")
                except Exception:
                    self._safe_rollback(conn)
                    raise
                self._sync_memory_after_write(
                    table, conn, lambda state: self._memory_remove(state, set(str_ids))
                )
                return deleted

            return self._run_with_retry(_op)

    def search(
        self,
        table: str,
        query_embedding,
        limit: int | None = None,
        filter_metadata: dict | None = None,
    ) -> list[dict]:
        """暴力余弦搜索，返回按相似度降序的行

        Args:
            table: 表名常量
            query_embedding: 查询向量；维度不符时记日志并返回 []（复刻 chroma 维度
                不匹配抛异常被外层捕获为 [] 的行为）
            limit: 返回上限；None 表示返回全部匹配行（供调用方做后置日期过滤后再截断）
            filter_metadata: 元数据精确等值过滤（内存 mask，多键 AND，不做类型强转，
                与 chroma $eq 语义一致）

        Returns:
            [{"doc_id", "document", "metadata", "similarity"}, ...] 按相似度降序
        """
        try:
            q = self._as_vector(query_embedding)
        except ValueError as e:
            logger.warning(f"lite 查询向量维度不匹配，返回空结果: {e}")
            return []
        q = self._normalize(q)
        with self._lock:

            def _op() -> list[dict]:
                conn = self._ensure_connection()
                self._ensure_schema(conn)
                state = self._get_fresh_state(conn, table)
                matrix = state.matrix
                if matrix is None or matrix.shape[0] == 0:
                    return []
                if limit is not None and int(limit) <= 0:
                    return []
                if filter_metadata:
                    mask = np.array(
                        [
                            all(m.get(k) == v for k, v in filter_metadata.items())
                            for m in state.metadatas
                        ],
                        dtype=bool,
                    )
                    positions = np.nonzero(mask)[0]
                else:
                    positions = np.arange(matrix.shape[0])
                if positions.size == 0:
                    return []
                sims = matrix[positions] @ q
                order = np.argsort(-sims, kind="stable")
                if limit is not None and int(limit) < order.size:
                    k = int(limit)
                    part = np.argpartition(-sims, k - 1)[:k]
                    order = part[np.argsort(-sims[part], kind="stable")]
                results: list[dict] = []
                for pos in order:
                    src = int(positions[pos])
                    results.append(
                        {
                            "doc_id": state.ids[src],
                            "document": state.documents[src],
                            "metadata": state.metadatas[src],
                            "similarity": float(sims[pos]),
                        }
                    )
                return results

            return self._run_with_retry(_op)


class _CollectionAdapter:
    """chroma collection 兼容适配器（仅覆盖 WebUI 路由实际使用的 count/get/delete）

    契约要点（对齐 core/web_api/routes/vector_store.py 的全部触点）：
    - count()：权威行数（同库 SELECT COUNT(*)，满足"清空后 stats 立即反映"）
    - get(include=..., limit=..., offset=...)：返回"扁平" dict
      {"ids": [...], "documents": [...]|None, "metadatas": [...]|None, "embeddings": [...]|None}，
      未请求的键为 None；embeddings 元素为 np.ndarray（路由仅取 len() 即维度）
    - get(ids=[...])：按 id 过滤；未命中返回 {"ids": []}
    - delete(ids=[...])：幂等，一次可接受 1000 个 id（WebUI 清空循环的实际批量）
    - 分页/列举顺序为 ORDER BY id（chroma 为近似插入序；无调用方依赖具体顺序，仅依赖分页稳定）
    """

    def __init__(self, engine: LiteEngine, table: str):
        """绑定引擎与表名

        Args:
            engine: LiteEngine 实例
            table: 表名常量（TABLE_SUMMARIES / TABLE_MESSAGES）
        """
        self.engine = engine
        self.table = table

    @property
    def name(self) -> str:
        """集合名（summaries/messages），仅用于日志展示"""
        return "summaries" if self.table == TABLE_SUMMARIES else "messages"

    def count(self) -> int:
        """等价 chroma collection.count()"""
        return self.engine.count(self.table)

    def get(self, ids=None, include=None, limit=None, offset=None) -> dict:
        """等价 chroma collection.get(...) 的扁平返回形状（未请求键为 None）"""
        rows = self.engine.get_rows(self.table, ids=ids, limit=limit, offset=offset)
        wanted = set(include or [])
        result: dict = {
            "ids": [row[0] for row in rows],
            "documents": None,
            "metadatas": None,
            "embeddings": None,
        }
        if "documents" in wanted:
            result["documents"] = [row[2] for row in rows]
        if "metadatas" in wanted:
            result["metadatas"] = [json.loads(row[3]) for row in rows]
        if "embeddings" in wanted:
            result["embeddings"] = [np.frombuffer(row[1], dtype="<f4") for row in rows]
        return result

    def delete(self, ids) -> None:
        """等价 chroma collection.delete(ids=...)（幂等，返回 None）"""
        self.engine.delete(self.table, [str(doc_id) for doc_id in ids])
