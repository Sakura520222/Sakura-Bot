# Copyright 2026 Sakura-Bot
#
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
#
# - 署名：必须提供本项目的原始来源链接
# - 相同方式共享：衍生作品必须采用相同的许可证
#
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
# 许可证全文：参见 LICENSE 文件

"""一次性迁移脚本：ChromaDB -> SQLite(WAL) lite 向量库（整库搬运向量，不重嵌入）

【必须在全停机窗口执行】低峰热迁移会因主进程持有连接 + WAL 伴生文件导致数据丢失/损坏：
    docker compose stop sakura-bot
    python scripts/migrate_vectors_to_sqlite.py --yes

流程（防损坏顺序，不可调整）：
    1. 硬校验停机窗口（chroma 主库伴生文件/写锁探测，--assume-stopped 可跳过）；
    2. 分页读取 chroma 的 summaries/messages 两个 collection（两表全空需 --allow-empty）；
    3. 全部写入 <VECTOR_DB_PATH>/lite_index.sqlite3.tmp（synchronous=FULL，
       COMMIT 后显式 wal_checkpoint(TRUNCATE) 确保提交帧全部合并进主文件）；
    4. 校验：行数 == chroma count() 实时值、逐行 dim == EMBEDDING_DIMENSION（默认 1024）、
       每表抽样至多 10 条与 chroma 源向量余弦 > 0.999999；
    5. 断言 .tmp-wal/.tmp-shm 伴生文件不存在（WAL 未合并即拒绝换名）；
       删除已存在的 lite_index.sqlite3 及其 -wal/-shm 伴生文件（避免旧 WAL 重放进新库）；
    6. os.replace 原子改名 + fsync 目录（Windows 不支持目录 fsync，尽力而为）；
    7. 写幂等标记 data/.vector_migration_done（存在即退出，--force 可重跑）。

本脚本独立运行：仅依赖 chromadb/numpy/标准库，不 import 业务 core 包。
DDl 与 core/ai/vector_lite.py 的 schema 保持一致（schema_version=1）。
"""

import argparse
import json
import os
import sqlite3
from datetime import UTC, datetime
from pathlib import Path

import numpy as np

try:
    import chromadb
except ImportError:  # pragma: no cover
    chromadb = None

SCHEMA_VERSION = 1
TABLES = {
    "summaries": "summaries_vectors",
    "messages": "messages_vectors",
}
PAGE_SIZE = 1000
SAMPLE_SIZE = 10
COSINE_THRESHOLD = 0.999999

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


def _parse_args() -> argparse.Namespace:
    """解析命令行参数"""
    parser = argparse.ArgumentParser(
        description="将 ChromaDB 向量整库迁移到 SQLite(WAL) lite 向量库（需全停机窗口）"
    )
    parser.add_argument(
        "--vector-db-path",
        default=os.getenv("VECTOR_DB_PATH", "data/vectors"),
        help="chroma 数据目录，同时决定 lite 库位置（默认 VECTOR_DB_PATH 或 data/vectors）",
    )
    parser.add_argument(
        "--marker",
        default=os.path.join("data", ".vector_migration_done"),
        help="幂等标记文件路径（默认 data/.vector_migration_done）",
    )
    parser.add_argument("--force", action="store_true", help="忽略幂等标记强制重跑")
    parser.add_argument("--yes", action="store_true", help="跳过全停机确认提示")
    parser.add_argument(
        "--assume-stopped",
        action="store_true",
        help="跳过停机窗口硬校验（伴生文件/写锁探测），自行确认主/QA 进程已停止",
    )
    parser.add_argument(
        "--allow-empty",
        action="store_true",
        help="允许源 chroma 两个 collection 均为空时继续（防止 --vector-db-path 打错"
        "时凭空建空库顶掉既有目标库）",
    )
    parser.add_argument(
        "--verify-only",
        action="store_true",
        help="仅对已存在的 lite 库执行校验（对照 chroma 源），不迁移",
    )
    return parser.parse_args()


def _expected_dim() -> int:
    """读取目标向量维度"""
    return int(os.getenv("EMBEDDING_DIMENSION", "1024"))


def _remove_file(path: str) -> None:
    """删除文件（不存在则忽略）"""
    try:
        os.remove(path)
    except FileNotFoundError:
        pass


def _fsync_dir(dir_path: str) -> None:
    """fsync 目录落盘（Windows 不支持打开目录，尽力而为）"""
    try:
        fd = os.open(dir_path, os.O_RDONLY)
    except OSError:
        return
    try:
        os.fsync(fd)
    except OSError:
        pass
    finally:
        os.close(fd)


def _init_schema(conn: sqlite3.Connection) -> None:
    """建库结构并初始化版本号（幂等）"""
    for stmt in _SCHEMA_STATEMENTS:
        conn.execute(stmt)
    conn.executemany(
        "INSERT OR IGNORE INTO store_meta (key, value) VALUES (?, ?)",
        [
            ("schema_version", SCHEMA_VERSION),
            ("summaries_version", 1),
            ("messages_version", 1),
        ],
    )


def _connect_sqlite(path: str) -> sqlite3.Connection:
    """以 WAL 模式打开 SQLite 连接（显式事务管理）

    synchronous=FULL：一次性关键路径，宁可慢也要保证提交帧落盘
    （NORMAL 在断电时可能丢失最近事务，迁移不容许）。
    """
    conn = sqlite3.connect(path)
    conn.isolation_level = None
    conn.execute("PRAGMA journal_mode=WAL")
    conn.execute("PRAGMA synchronous=FULL")
    conn.execute("PRAGMA foreign_keys=OFF")
    return conn


def _get_chroma_collection(client, name: str):
    """获取（或创建）chroma collection，与主程序 get_or_create 语义一致"""
    return client.get_or_create_collection(name=name, metadata={"hnsw:space": "cosine"})


def _assert_shutdown_window(vector_db_path: str, assume_stopped: bool) -> None:
    """硬校验停机窗口（尽力而为）：chroma 主库不应有伴生文件/活动写锁

    - 伴生文件探测：任一进程持有 chroma 库连接且发生过写访问时，chroma.sqlite3
      的 -journal/-wal/-shm 伴生文件存在；全部进程干净退出后由 SQLite 删除。
    - 写锁探测：以独立读写连接尝试 BEGIN IMMEDIATE 短暂占锁后立即回滚——
      其他进程正在写时会因锁冲突超时失败。
    两项均通过只能说明"当前没有进程活跃"，不能证明已停机；故保留 --assume-stopped
    逃生口，命中时给出明确退出信息而非仅靠 y/N 提示。
    """
    chroma_db = os.path.join(vector_db_path, "chroma.sqlite3")
    if not os.path.exists(chroma_db):
        return
    companions = [
        suffix for suffix in ("-journal", "-wal", "-shm") if os.path.exists(chroma_db + suffix)
    ]
    if companions and not assume_stopped:
        raise SystemExit(
            f"[终止] 检测到 {chroma_db} 的伴生文件 {companions}，疑似仍有进程持有 chroma 库；"
            "请先停止主/QA 进程（docker compose stop sakura-bot）再重试，"
            "确认已停机可加 --assume-stopped 跳过该检查"
        )
    if not companions:
        conn = None
        try:
            conn = sqlite3.connect(chroma_db, timeout=1.0)
            conn.isolation_level = None
            conn.execute("PRAGMA busy_timeout=1000")
            conn.execute("BEGIN IMMEDIATE")
            conn.execute("ROLLBACK")
        except sqlite3.Error as e:
            if not assume_stopped:
                raise SystemExit(
                    f"[终止] chroma 主库写锁探测失败（{type(e).__name__}: {e}），"
                    "疑似仍有进程活跃；请先停止主/QA 进程再重试，"
                    "确认已停机可加 --assume-stopped 跳过该检查"
                ) from e
            print(f"[警告] --assume-stopped：忽略写锁探测失败（{type(e).__name__}: {e}）")
        finally:
            if conn is not None:
                conn.close()
    if companions:
        print(f"[警告] --assume-stopped：忽略伴生文件 {companions}（请自行确认全停机）")
    print("[校验] 停机窗口检查通过（尽力而为，非绝对证明）")


def _assert_no_companion_files(db_path: str) -> None:
    """换名前断言 -wal/-shm 伴生文件不存在（存在即意味着提交帧尚未合并进主文件）"""
    leftovers = [suffix for suffix in ("-wal", "-shm") if os.path.exists(db_path + suffix)]
    if leftovers:
        raise SystemExit(
            f"[终止] {db_path} 换名前仍存在伴生文件 {leftovers}，WAL 可能未合并，"
            "拒绝 os.replace 以避免丢失已提交数据"
        )


def _fetch_all_from_chroma(collection, label: str) -> dict:
    """分页拉取整个 collection，返回 {id: {"embedding","document","metadata"}}"""
    data: dict = {}
    offset = 0
    while True:
        batch = collection.get(
            include=["embeddings", "metadatas", "documents"],
            limit=PAGE_SIZE,
            offset=offset,
        )
        ids = batch.get("ids") or []
        if not ids:
            break
        # 注意：chroma 返回的 embeddings 是 numpy ndarray，`or` 短路对其求真值会抛
        # ValueError，必须显式判 None
        embeddings = batch.get("embeddings")
        if embeddings is None:
            embeddings = [None] * len(ids)
        documents = batch.get("documents") or [None] * len(ids)
        metadatas = batch.get("metadatas") or [{}] * len(ids)
        # strict=False：embeddings/documents/metadatas 可能是补位兜底列表，长度可短于 ids
        for doc_id, emb, doc, meta in zip(ids, embeddings, documents, metadatas, strict=False):
            data[str(doc_id)] = {"embedding": emb, "document": doc, "metadata": meta or {}}
        if len(ids) < PAGE_SIZE:
            break
        offset += PAGE_SIZE
    print(f"[迁移] {label}: 读取 {len(data)} 行")
    return data


def _write_tmp_db(tmp_path: str, expected_dim: int, tables_data: dict) -> None:
    """将两个集合的数据写入临时库文件（维度不符立即终止，拒绝脏数据）"""
    conn = _connect_sqlite(tmp_path)
    try:
        _init_schema(conn)
        stored_at = datetime.now(UTC).isoformat()
        conn.execute("BEGIN IMMEDIATE")
        for label, table in TABLES.items():
            payload = []
            for doc_id, item in tables_data[label].items():
                emb = np.asarray(item["embedding"], dtype="<f4").reshape(-1)
                if emb.shape[0] != expected_dim:
                    raise SystemExit(
                        f"[终止] {label}/{doc_id} 维度 {emb.shape[0]} != {expected_dim}，拒绝写入脏数据"
                    )
                payload.append(
                    (
                        doc_id,
                        emb.tobytes(),
                        item["document"] or "",
                        json.dumps(item["metadata"], ensure_ascii=False, default=str),
                        expected_dim,
                        stored_at,
                    )
                )
            conn.executemany(
                f"INSERT OR REPLACE INTO {table} "
                "(id, embedding, document, metadata_json, dim, stored_at) VALUES (?, ?, ?, ?, ?, ?)",
                payload,
            )
            print(f"[迁移] {table}: 写入 {len(payload)} 行 -> {tmp_path}")
        conn.execute("COMMIT")
        # 显式截断 checkpoint：close 的隐式 checkpoint 并非有保证行为，若提交帧
        # 残留 .tmp-wal，后续 os.replace 只换主文件会丢掉 WAL 里已提交的行
        conn.execute("PRAGMA wal_checkpoint(TRUNCATE)")
    except Exception:
        conn.execute("ROLLBACK")
        raise
    finally:
        conn.close()


def _cosine(a: np.ndarray, b: np.ndarray) -> float:
    """余弦相似度（零向量以 1e-12 兜底）"""
    denom = max(float(np.linalg.norm(a)) * float(np.linalg.norm(b)), 1e-12)
    return float(np.dot(a, b) / denom)


def _verify_db(db_path: str, chroma_client, expected_dim: int) -> bool:
    """校验 lite 库：行数 == chroma count()、逐行维度、抽样与源向量余弦一致"""
    ok = True
    uri = f"file:{Path(db_path).as_posix()}?mode=ro"
    conn = sqlite3.connect(uri, uri=True)
    try:
        for label, table in TABLES.items():
            collection = _get_chroma_collection(chroma_client, label)
            sqlite_count = conn.execute(f"SELECT COUNT(*) FROM {table}").fetchone()[0]
            chroma_count = collection.count()
            if sqlite_count != chroma_count:
                print(f"[校验失败] {label}: sqlite {sqlite_count} 行 != chroma {chroma_count} 行")
                ok = False
            else:
                print(f"[校验] {label}: 行数一致 = {sqlite_count}")

            bad = conn.execute(
                f"SELECT COUNT(*) FROM {table} WHERE dim != ? OR LENGTH(embedding) != ?",
                (expected_dim, expected_dim * 4),
            ).fetchone()[0]
            if bad:
                print(f"[校验失败] {label}: {bad} 行维度异常（期望 {expected_dim}）")
                ok = False

            sample_rows = conn.execute(
                f"SELECT id, embedding FROM {table} ORDER BY RANDOM() LIMIT ?",
                (SAMPLE_SIZE,),
            ).fetchall()
            for doc_id, blob in sample_rows:
                local = np.frombuffer(blob, dtype="<f4")
                remote = collection.get(ids=[doc_id], include=["embeddings"]).get("embeddings")
                # ndarray 求真值非法，显式判 None/空（embeddings 是 numpy ndarray）
                if remote is None or len(remote) == 0:
                    print(f"[校验失败] {label}/{doc_id}: chroma 中缺失源向量")
                    ok = False
                    continue
                remote_vec = np.asarray(remote[0], dtype="<f4").reshape(-1)
                cos = _cosine(local, remote_vec)
                if cos <= COSINE_THRESHOLD:
                    print(f"[校验失败] {label}/{doc_id}: 余弦 {cos} <= {COSINE_THRESHOLD}")
                    ok = False
            print(f"[校验] {label}: 抽样 {len(sample_rows)} 条完成")
    finally:
        conn.close()
    return ok


def main() -> int:
    """脚本入口：迁移（含强制校验）或仅校验"""
    args = _parse_args()
    if chromadb is None:
        print("[终止] chromadb 未安装，无法读取源库")
        return 1

    final_db = os.path.join(args.vector_db_path, "lite_index.sqlite3")

    if os.path.exists(args.marker) and not args.force:
        print(f"[退出] 幂等标记已存在: {args.marker}（如需重跑请加 --force）")
        return 0

    if args.verify_only:
        chroma_client = chromadb.PersistentClient(path=args.vector_db_path)
        if not os.path.exists(final_db):
            print(f"[终止] lite 库不存在: {final_db}")
            return 1
        return 0 if _verify_db(final_db, chroma_client, _expected_dim()) else 1

    # 停机窗口硬校验必须在脚本自身打开 chroma 之前执行，避免自身连接干扰判断
    _assert_shutdown_window(args.vector_db_path, args.assume_stopped)

    if not args.yes:
        print("[警告] 本脚本必须在全停机窗口执行（docker compose stop sakura-bot）。")
        answer = input("确认两个进程均已停止并继续迁移? [y/N] ").strip().lower()
        if answer not in ("y", "yes"):
            print("[退出] 已取消")
            return 1

    chroma_client = chromadb.PersistentClient(path=args.vector_db_path)

    tables_data = {}
    for label in TABLES:
        tables_data[label] = _fetch_all_from_chroma(
            _get_chroma_collection(chroma_client, label), label
        )

    # 空源守卫：--vector-db-path 打错时 PersistentClient 会凭空创建空 chroma 目录，
    # 0==0 校验照样通过，空 lite 库换名后将顶掉既有目标库
    if not any(tables_data.values()) and not args.allow_empty:
        print(
            "[终止] 源 chroma 两个 collection 均为 0 行：请确认 --vector-db-path 指向正确；"
            "如确为空库迁移，请加 --allow-empty"
        )
        return 1

    tmp_db = final_db + ".tmp"
    for suffix in ("", "-wal", "-shm"):
        _remove_file(tmp_db + suffix)
    _write_tmp_db(tmp_db, _expected_dim(), tables_data)

    if not _verify_db(tmp_db, chroma_client, _expected_dim()):
        print(f"[终止] 校验未通过，源数据未动；请检查临时文件: {tmp_db}")
        return 1

    # 换名前断言临时库无 WAL 残留（提交帧未合并则拒绝继续）
    _assert_no_companion_files(tmp_db)

    # 关键顺序：先删旧库及 WAL 伴生文件（防旧 WAL 重放进新库），再原子换名 + fsync 目录
    for suffix in ("", "-wal", "-shm"):
        _remove_file(final_db + suffix)
    os.replace(tmp_db, final_db)
    _fsync_dir(os.path.dirname(os.path.abspath(final_db)) or ".")
    print(f"[迁移] 已原子换名: {final_db}")

    os.makedirs(os.path.dirname(os.path.abspath(args.marker)), exist_ok=True)
    with open(args.marker, "w", encoding="utf-8") as fh:
        fh.write(datetime.now(UTC).isoformat() + "\n")
    print(f"[完成] 幂等标记已写入: {args.marker}")
    print("[完成] 迁移成功；可继续运行 --verify-only 复查，随后切换 VECTOR_BACKEND=lite")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
