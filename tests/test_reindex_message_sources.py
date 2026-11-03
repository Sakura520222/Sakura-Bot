# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
import json
from unittest.mock import AsyncMock

from scripts.reindex_message_sources import main, replay_sources


def source():
    return {
        "channel_id": "123",
        "message_id": 7,
        "text": "  完整🥳\n" + "x" * 900 + "TAIL  ",
        "created_at": "2020-01-02T03:04:05+08:00",
        "channel_name": "name",
        "channel_username": "example",
        "post_url": "https://t.me/example/7",
        "extra": {"preserve": True},
    }


async def test_malformed_isolated_and_exact_replay(tmp_path):
    record = source()
    path = tmp_path / "valid.json"
    original = json.dumps(record, ensure_ascii=False).encode()
    path.write_bytes(original)
    (tmp_path / "broken.json").write_text("{broken")
    indexer = AsyncMock(return_value=True)
    result = await replay_sources(tmp_path, apply=True, indexer=indexer)
    assert result["input_count"] == 2
    assert result["processed_count"] == 1
    assert result["failed_count"] == 1
    assert result["failures"][0]["source_id"] == "broken.json"
    kwargs = indexer.call_args.kwargs
    for key in (
        "text",
        "created_at",
        "channel_id",
        "message_id",
        "channel_username",
        "channel_name",
    ):
        assert kwargs[key] == record[key]
    assert path.read_bytes() == original


async def test_false_ack_is_failure(tmp_path):
    (tmp_path / "valid.json").write_text(json.dumps(source()))
    result = await replay_sources(tmp_path, apply=True, indexer=AsyncMock(return_value=False))
    assert result["processed_count"] == 0
    assert result["failed_count"] == 1
    assert result["failures"][0]["source_id"] == "123:7"


async def test_dry_run_does_not_index(tmp_path):
    (tmp_path / "valid.json").write_text(json.dumps(source()))
    indexer = AsyncMock()
    result = await replay_sources(tmp_path, indexer=indexer)
    assert result["dry_run"]
    assert result["valid_count"] == 1
    assert result["processed_count"] == 0
    indexer.assert_not_called()


def test_cli_missing_or_malformed_returns_nonzero(tmp_path, capsys):
    assert main(["--source-dir", str(tmp_path / "missing")]) == 1
    (tmp_path / "bad.json").write_text('{"channel_id": "123"}')
    assert main(["--source-dir", str(tmp_path)]) == 1
    assert '"failed_count": 1' in capsys.readouterr().out


async def test_apply_uses_shadow_archive_and_restores_environment(tmp_path, monkeypatch):
    import os
    from pathlib import Path

    from core.handlers.realtime_rag_handler import RealtimeRAGHandler
    from scripts.reindex_message_sources import run

    record = source()
    path = tmp_path / "original.json"
    content = json.dumps(record, ensure_ascii=False)
    path.write_text(content)
    monkeypatch.setenv("MESSAGE_SOURCE_DIR", str(tmp_path))

    async def index(self, **kwargs):
        shadow = Path(os.environ["MESSAGE_SOURCE_DIR"])
        assert shadow != tmp_path
        (shadow / "rewritten.json").write_text("changed")
        assert kwargs["created_at"] == record["created_at"]
        return True

    monkeypatch.setattr(RealtimeRAGHandler, "index_message", index)
    result = await run(tmp_path, apply=True)
    assert result["processed_count"] == 1
    assert result["failed_count"] == 0
    assert path.read_text() == content
    assert list(tmp_path.glob("*.json")) == [path]
    assert os.environ["MESSAGE_SOURCE_DIR"] == str(tmp_path)
