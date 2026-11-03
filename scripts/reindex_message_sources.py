#!/usr/bin/env python3
# Copyright 2026 Sakura-Bot
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
# 许可证全文：参见 LICENSE 文件
"""离线重放原文归档；默认仅校验，--apply 才调用 Embedding/向量服务。"""

import argparse
import asyncio
import importlib
import json
import os
import sys
from datetime import datetime
from pathlib import Path
from tempfile import TemporaryDirectory

REPOSITORY_ROOT = Path(__file__).resolve().parents[1]


def validate_source(record):
    if not isinstance(record, dict):
        raise ValueError("Source must be a JSON object")
    if not isinstance(record.get("channel_id"), str) or not record["channel_id"].strip():
        raise ValueError("Missing channel_id")
    if type(record.get("message_id")) is not int or record["message_id"] <= 0:
        raise ValueError("Invalid message_id")
    if not isinstance(record.get("text"), str) or not record["text"].strip():
        raise ValueError("Missing text")
    if not isinstance(record.get("created_at"), str):
        raise ValueError("Missing created_at")
    datetime.fromisoformat(record["created_at"])
    for field in ("channel_name", "channel_username"):
        if record.get(field) is not None and not isinstance(record[field], str):
            raise ValueError("Invalid source metadata")
    return record


async def replay_sources(directory: Path, *, apply=False, indexer=None):
    """单条损坏记录不影响其余记录；确认完整写入后才算成功。"""
    report = {
        "dry_run": not apply,
        "input_count": 0,
        "valid_count": 0,
        "processed_count": 0,
        "failed_count": 0,
        "failures": [],
    }
    if not await asyncio.to_thread(directory.is_dir):
        report.update(
            failed_count=1, failures=[{"source_id": str(directory), "error": "MissingDirectory"}]
        )
        return report
    if apply and indexer is None:
        raise ValueError("Applying requires an indexer")
    for path in await asyncio.to_thread(lambda: sorted(directory.glob("*.json"))):
        report["input_count"] += 1
        source_id = path.name
        try:
            record = validate_source(
                json.loads(await asyncio.to_thread(path.read_text, encoding="utf-8"))
            )
            source_id = f"{record['channel_id']}:{record['message_id']}"
            report["valid_count"] += 1
            if apply:
                success = await indexer(
                    channel_id=record["channel_id"],
                    channel_name=record.get("channel_name") or record["channel_id"],
                    message_id=record["message_id"],
                    text=record["text"],
                    channel_username=record.get("channel_username"),
                    created_at=record["created_at"],
                )
                if success is not True:
                    raise RuntimeError("IncompleteIndexing")
                report["processed_count"] += 1
        except Exception as exc:
            report["failed_count"] += 1
            report["failures"].append({"source_id": source_id, "error": type(exc).__name__})
    return report


async def run(directory, apply=False):
    if not apply:
        return await replay_sources(directory)
    # index_message 会写源归档；使用临时影子目录，原归档及扩展元数据保持逐字不变。
    # 必须停掉写入中的 Bot 后执行，避免旧归档覆盖较新的向量。
    sys.path.insert(0, str(REPOSITORY_ROOT))
    importlib.import_module("core.ai.source_archive")
    from core.handlers.realtime_rag_handler import RealtimeRAGHandler

    previous = os.environ.get("MESSAGE_SOURCE_DIR")
    with TemporaryDirectory(prefix="sakura-reindex-") as shadow:
        os.environ["MESSAGE_SOURCE_DIR"] = shadow
        try:
            return await replay_sources(
                directory, apply=True, indexer=RealtimeRAGHandler().index_message
            )
        finally:
            if previous is None:
                os.environ.pop("MESSAGE_SOURCE_DIR", None)
            else:
                os.environ["MESSAGE_SOURCE_DIR"] = previous


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--source-dir",
        type=Path,
        default=Path(os.getenv("MESSAGE_SOURCE_DIR", "data/message_sources")),
    )
    parser.add_argument("--apply", action="store_true", help="实际调用向量服务；执行前停掉 Bot")
    args = parser.parse_args(argv)
    try:
        report = asyncio.run(run(args.source_dir, args.apply))
    except Exception as exc:
        report = {
            "dry_run": not args.apply,
            "processed_count": 0,
            "failed_count": 1,
            "failures": [{"source_id": "initialization", "error": type(exc).__name__}],
        }
    print(json.dumps(report, ensure_ascii=False, indent=2))
    return 1 if report["failed_count"] else 0


if __name__ == "__main__":
    raise SystemExit(main())
