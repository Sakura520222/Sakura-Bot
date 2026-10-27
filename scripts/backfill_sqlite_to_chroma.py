# Copyright 2026 Sakura-Bot
#
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
#
# - 署名：必须提供本项目的原始来源链接
# - 相同方式共享：衍生作品必须采用相同的许可证
#
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
# 许可证全文：参见 LICENSE 文件

"""反向回填脚本：SQLite(WAL) lite 向量库 -> ChromaDB

用途：删除 chroma 代码路径之前的安全网（A-4 修复）——lite 运行期间新增的向量只存在于
SQLite；执行本脚本回填后，任意时刻回滚到 chroma 后端都不会丢失数据。
字段映射与正向迁移（scripts/migrate_vectors_to_sqlite.py）互逆：
    sqlite(id, embedding BLOB, document, metadata_json) -> chroma(ids, embeddings, documents, metadatas)
写入使用 upsert（幂等，可重复执行）；--verify 抽样校验两侧余弦 > 0.999999。

用法：
    python scripts/backfill_sqlite_to_chroma.py --yes
    python scripts/backfill_sqlite_to_chroma.py --verify-only
"""

import argparse
import json
import os
import sqlite3
from pathlib import Path

import numpy as np

try:
    import chromadb
except ImportError:  # pragma: no cover
    chromadb = None

TABLES = {
    "summaries": "summaries_vectors",
    "messages": "messages_vectors",
}
PAGE_SIZE = 500
SAMPLE_SIZE = 10
COSINE_THRESHOLD = 0.999999


def _parse_args() -> argparse.Namespace:
    """解析命令行参数"""
    parser = argparse.ArgumentParser(
        description="将 SQLite lite 向量库反向回填到 ChromaDB（删码前的回滚安全网）"
    )
    parser.add_argument(
        "--vector-db-path",
        default=os.getenv("VECTOR_DB_PATH", "data/vectors"),
        help="chroma 数据目录，同时决定 lite 库位置（默认 VECTOR_DB_PATH 或 data/vectors）",
    )
    parser.add_argument(
        "--sqlite-db",
        default=None,
        help="lite 库文件路径（默认 <vector-db-path>/lite_index.sqlite3）",
    )
    parser.add_argument("--yes", action="store_true", help="跳过执行确认提示")
    parser.add_argument(
        "--verify-only", action="store_true", help="仅校验 chroma 与 lite 库的一致性，不回填"
    )
    return parser.parse_args()


def _open_sqlite_ro(path: str) -> sqlite3.Connection:
    """以只读模式打开 lite 库"""
    uri = f"file:{Path(path).as_posix()}?mode=ro"
    return sqlite3.connect(uri, uri=True)


def _get_chroma_collection(client, name: str):
    """获取（或创建）chroma collection，与主程序 get_or_create 语义一致"""
    return client.get_or_create_collection(name=name, metadata={"hnsw:space": "cosine"})


def _cosine(a: np.ndarray, b: np.ndarray) -> float:
    """余弦相似度（零向量以 1e-12 兜底）"""
    denom = max(float(np.linalg.norm(a)) * float(np.linalg.norm(b)), 1e-12)
    return float(np.dot(a, b) / denom)


def _table_count(conn: sqlite3.Connection, table: str) -> int:
    """读取 lite 表行数"""
    return int(conn.execute(f"SELECT COUNT(*) FROM {table}").fetchone()[0])


def _backfill_table(conn: sqlite3.Connection, collection, table: str) -> int:
    """分页读取 lite 表并 upsert 回 chroma，返回回填总行数"""
    total = _table_count(conn, table)
    written = 0
    offset = 0
    while True:
        rows = conn.execute(
            f"SELECT id, embedding, document, metadata_json FROM {table} ORDER BY id LIMIT ? OFFSET ?",
            (PAGE_SIZE, offset),
        ).fetchall()
        if not rows:
            break
        ids = [row[0] for row in rows]
        embeddings = [np.frombuffer(row[1], dtype="<f4").tolist() for row in rows]
        documents = [row[2] for row in rows]
        metadatas = [json.loads(row[3]) for row in rows]
        collection.upsert(ids=ids, embeddings=embeddings, documents=documents, metadatas=metadatas)
        written += len(ids)
        offset += PAGE_SIZE
        print(f"[回填] {table}: {written}/{total}")
    return written


def _verify_against_chroma(conn: sqlite3.Connection, chroma_client) -> bool:
    """校验 chroma 与 lite 库一致：行数相等 + 抽样余弦 > 阈值"""
    ok = True
    for label, table in TABLES.items():
        collection = _get_chroma_collection(chroma_client, label)
        lite_count = _table_count(conn, table)
        chroma_count = collection.count()
        if lite_count != chroma_count:
            print(f"[校验失败] {label}: chroma {chroma_count} 行 != lite {lite_count} 行")
            ok = False
        else:
            print(f"[校验] {label}: 行数一致 = {lite_count}")

        sample_rows = conn.execute(
            f"SELECT id, embedding FROM {table} ORDER BY RANDOM() LIMIT ?",
            (SAMPLE_SIZE,),
        ).fetchall()
        for doc_id, blob in sample_rows:
            local = np.frombuffer(blob, dtype="<f4")
            remote = collection.get(ids=[doc_id], include=["embeddings"]).get("embeddings")
            if not remote:
                print(f"[校验失败] {label}/{doc_id}: chroma 中缺失向量")
                ok = False
                continue
            remote_vec = np.asarray(remote[0], dtype="<f4").reshape(-1)
            cos = _cosine(local, remote_vec)
            if cos <= COSINE_THRESHOLD:
                print(f"[校验失败] {label}/{doc_id}: 余弦 {cos} <= {COSINE_THRESHOLD}")
                ok = False
        print(f"[校验] {label}: 抽样 {len(sample_rows)} 条完成")
    return ok


def main() -> int:
    """脚本入口：回填（可跟 --verify 抽样校验）或仅校验"""
    args = _parse_args()
    if chromadb is None:
        print("[终止] chromadb 未安装，无法写入目标库")
        return 1

    sqlite_db = args.sqlite_db or os.path.join(args.vector_db_path, "lite_index.sqlite3")
    if not os.path.exists(sqlite_db):
        print(f"[终止] lite 库不存在: {sqlite_db}")
        return 1

    chroma_client = chromadb.PersistentClient(path=args.vector_db_path)
    conn = _open_sqlite_ro(sqlite_db)

    try:
        if args.verify_only:
            return 0 if _verify_against_chroma(conn, chroma_client) else 1

        if not args.yes:
            print("[提示] 本脚本将向 ChromaDB upsert 全部 lite 向量（幂等，可重复执行）。")
            answer = input("继续回填? [y/N] ").strip().lower()
            if answer not in ("y", "yes"):
                print("[退出] 已取消")
                return 1

        total = 0
        for label, table in TABLES.items():
            collection = _get_chroma_collection(chroma_client, label)
            total += _backfill_table(conn, collection, table)
        print(f"[完成] 回填完成，共写入 {total} 行")

        print("[校验] 开始回填后一致性校验")
        return 0 if _verify_against_chroma(conn, chroma_client) else 1
    finally:
        conn.close()


if __name__ == "__main__":
    raise SystemExit(main())
