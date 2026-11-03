# Copyright 2026 Sakura-Bot
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
# 许可证全文：参见 LICENSE 文件
"""独立于向量服务的完整原文归档，失败重试不会丢失正文。"""

import hashlib
import json
import os
from pathlib import Path
from tempfile import NamedTemporaryFile


def source_path(channel_id, message_id):
    key = hashlib.sha256(f"{channel_id}:{message_id}".encode()).hexdigest()
    return Path(os.getenv("MESSAGE_SOURCE_DIR", "data/message_sources")) / f"{key}.json"


def archive_source(channel_id, message_id, text, created_at, **metadata):
    path = source_path(channel_id, message_id)
    path.parent.mkdir(parents=True, exist_ok=True)
    record = dict(
        metadata,
        channel_id=str(channel_id),
        message_id=message_id,
        text=text,
        created_at=created_at,
    )
    temporary = None
    try:
        with NamedTemporaryFile(mode="w", encoding="utf-8", dir=path.parent, delete=False) as file:
            temporary = Path(file.name)
            json.dump(record, file, ensure_ascii=False)
            file.flush()
            os.fsync(file.fileno())
        temporary.replace(path)
    finally:
        if temporary is not None:
            temporary.unlink(missing_ok=True)


def delete_source(channel_id, message_id):
    source_path(channel_id, message_id).unlink(missing_ok=True)
