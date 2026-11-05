# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot

"""A small local ingress outbox bridges MySQL outages; MySQL remains authoritative."""

import json
import os
import tempfile
from pathlib import Path

from .models import digest


class EventOutbox:
    def __init__(self, root=None):
        self.root = Path(root or os.getenv("WIKI_EVENT_DIR", "data/wiki_events"))

    def put(self, record):
        self.root.mkdir(parents=True, exist_ok=True)
        key = digest(record)
        path = self.root / f"{key}.json"
        if path.exists():
            return key
        temporary = None
        try:
            with tempfile.NamedTemporaryFile(
                mode="w", encoding="utf-8", dir=self.root, delete=False
            ) as stream:
                temporary = Path(stream.name)
                json.dump(record, stream, ensure_ascii=False, default=str)
                stream.flush()
                os.fsync(stream.fileno())
            temporary.replace(path)
        finally:
            if temporary:
                temporary.unlink(missing_ok=True)
        return key

    def pending(self, limit=100):
        result = []
        if not self.root.exists():
            return result
        for path in self.root.glob("*.json"):
            result.append((path.stem, json.loads(path.read_text(encoding="utf-8"))))
            if len(result) >= limit:
                break
        return sorted(result, key=lambda pair: pair[1]["kind"] != "delete")

    def acknowledge(self, key):
        if len(key) != 64 or any(char not in "0123456789abcdef" for char in key):
            raise ValueError("Invalid outbox key")
        (self.root / f"{key}.json").unlink(missing_ok=True)
