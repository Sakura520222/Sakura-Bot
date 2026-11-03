# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
import json
from types import SimpleNamespace
from unittest.mock import AsyncMock, MagicMock

import pytest

from core.ai.summary_pipeline import SummaryIntegrityError, summarize_complete
from core.ai.text_budget import pack_texts, split_text, token_upper_bound


def response(content="complete", reason="stop"):
    return SimpleNamespace(
        choices=[SimpleNamespace(message=SimpleNamespace(content=content), finish_reason=reason)]
    )


@pytest.fixture
def runtime(tmp_path, monkeypatch):
    monkeypatch.setenv("SUMMARY_AUDIT_DIR", str(tmp_path))
    monkeypatch.setenv("SUMMARY_CONTEXT_TOKENS", "2048")
    monkeypatch.setenv("SUMMARY_OUTPUT_TOKENS", "256")
    monkeypatch.setattr("core.ai.summary_pipeline.asyncio.sleep", AsyncMock())
    client = MagicMock()
    client.chat.completions.create = AsyncMock(return_value=response())
    return client, tmp_path


@pytest.mark.parametrize(
    "text",
    ["汉字" * 1000, "def foo():\n    return 42\n" * 100, "hello" * 1000, " 🥳👨‍👩‍👦\n" * 500],
)
def test_unicode_lossless_budget(text):
    chunks = split_text(text, 127)
    assert "".join(chunks) == text
    assert all(token_upper_bound(chunk) <= 127 for chunk in chunks)


async def test_short_input_single_call_and_exact_audit(runtime):
    client, path = runtime
    messages = ["  原文🥳\n", "end"]
    assert await summarize_complete(messages, "总结", client, "test") == "complete"
    client.chat.completions.create.assert_awaited_once()
    audit = json.loads(next(path.glob("*.json")).read_text(encoding="utf-8"))
    assert audit["messages"] == messages
    assert audit["processed_count"] == 2
    assert audit["status"] == "completed"


async def test_all_chunks_including_tail_and_head_in_final(runtime):
    client, path = runtime

    async def complete(**kwargs):
        text = kwargs["messages"][1]["content"]
        return response(" ".join(word for word in ("FIRST", "LAST") if word in text) or "middle")

    client.chat.completions.create.side_effect = complete
    text = "FIRST" + "中文 English 🥳 code()\n" * 300 + "LAST"
    result = await summarize_complete([text], "summarize", client, "test")
    assert "FIRST" in result and "LAST" in result
    audit = json.loads(next(path.glob("*.json")).read_text(encoding="utf-8"))
    assert audit["messages"] == [text]
    assert audit["processed_chunks"] == audit["chunk_count"] > 1
    assert audit["failed_count"] == 0
    for call in client.chat.completions.create.call_args_list:
        assert sum(token_upper_bound(m["content"]) for m in call.kwargs["messages"]) + 256 < 2048


@pytest.mark.parametrize("reason", ["length", "content_filter", None])
async def test_incomplete_output_fails_and_audits(runtime, reason):
    client, path = runtime
    client.chat.completions.create.return_value = response("partial", reason)
    with pytest.raises(SummaryIntegrityError, match="Incomplete model output"):
        await summarize_complete(["data"], "prompt", client, "test")
    assert client.chat.completions.create.await_count == 3
    audit = json.loads(next(path.glob("*.json")).read_text(encoding="utf-8"))
    assert audit["status"] == "failed"
    assert audit["failed_count"] == 1
    assert "summary" not in audit


async def test_chunk_failure_no_partial_success(runtime):
    client, path = runtime
    client.chat.completions.create.side_effect = [
        response(),
        ConnectionError("down"),
        ConnectionError("down"),
        ConnectionError("down"),
    ]
    with pytest.raises(SummaryIntegrityError):
        await summarize_complete(["x" * 8000], "prompt", client, "test")
    audit = json.loads(next(path.glob("*.json")).read_text(encoding="utf-8"))
    assert audit["processed_chunks"] == 1
    assert audit["failed_count"] == 1
    assert audit["status"] == "failed"


def test_packing_never_drops_suffix():
    chunks = pack_texts(["first" + "中" * 1000 + "last"], 100)
    assert "".join(chunks) == "first" + "中" * 1000 + "last"


async def test_rag_indexes_tail_preserves_text_and_timestamp(monkeypatch, tmp_path):
    from core.ai.embedding_generator import get_embedding_generator
    from core.ai.vector_store import VectorStore
    from core.handlers.realtime_rag_handler import RealtimeRAGHandler

    monkeypatch.setenv("VECTOR_BACKEND", "lite")
    monkeypatch.setenv("VECTOR_DB_PATH", str(tmp_path / "vectors"))
    monkeypatch.setenv("EMBEDDING_DIMENSION", "2")
    monkeypatch.setenv("EMBEDDING_INPUT_TOKENS", "128")
    store = VectorStore(reader=False)
    generator = get_embedding_generator()
    monkeypatch.setattr(generator, "is_available", lambda: True)
    monkeypatch.setattr(
        generator,
        "batch_generate",
        lambda texts: [[0.0, 1.0] if "TAIL" in t else [1.0, 0.0] for t in texts],
    )
    monkeypatch.setattr(generator, "generate", lambda text: [0.0, 1.0])
    monkeypatch.setattr("core.ai.vector_store.get_vector_store", lambda: store)
    handler = RealtimeRAGHandler()
    handler._running = True
    text = "  首段 " + "混合🥳 code\n" * 200 + "TAIL\n "
    assert handler.enqueue_message(
        "123", "channel", 9, text, created_at="2020-01-01T00:00:00+00:00"
    )
    await handler._process_batch([await handler._queue.get()])
    rows = store.messages_collection.get(include=["documents", "metadatas"])
    ordered = sorted(
        zip(rows["documents"], rows["metadatas"], strict=True), key=lambda p: p[1]["chunk_index"]
    )
    assert "".join(p[0] for p in ordered) == text
    assert all(p[1]["created_at"] == "2020-01-01T00:00:00+00:00" for p in ordered)
    found = store.search_messages("TAIL", top_k=1)
    assert "TAIL" in found[0]["summary_text"]
    assert found[0]["message_id"] == 9
    assert handler.get_stats()["processed_count"] == 1
    assert handler._generations == {}
    # 长文编辑成短文时旧分块必须删除，删除事件也兼容旧单条 ID。
    assert handler.enqueue_message_update("123", "channel", 9, "short")
    await handler._process_batch([await handler._queue.get()])
    assert store.messages_collection.get()["ids"] == ["123:9"]
    assert store.delete_message("123:9")
    assert store.messages_collection.count() == 0


async def test_fetch_complete_source_and_order(monkeypatch, tmp_path):
    from datetime import UTC, datetime

    from core.telegram import messaging

    monkeypatch.setenv("MESSAGE_SOURCE_DIR", str(tmp_path))
    text = "  FIRST 中文🥳\n" + "def foo(): return 42\n" * 100 + "LAST  "
    source = [
        SimpleNamespace(id=n, text=text + str(n), date=datetime(2020, 1, n, tzinfo=UTC))
        for n in (1, 2)
    ]

    async def iterator(*args, **kwargs):
        for message in source:
            yield message

    client = MagicMock()
    client.iter_messages = iterator
    userbot = MagicMock()
    userbot.get_client.return_value = client
    userbot.is_available.return_value = True
    monkeypatch.setattr(messaging, "get_userbot_client", lambda: userbot)
    fetched = await messaging.fetch_last_week_messages(["https://t.me/example"])
    assert text + "1" in fetched["https://t.me/example"][0]
    assert text + "2" in fetched["https://t.me/example"][1]
    records = [json.loads(path.read_text(encoding="utf-8")) for path in tmp_path.glob("*.json")]
    assert sorted(record["text"] for record in records) == [text + "1", text + "2"]
    assert all(record["post_url"].startswith("https://t.me/example/") for record in records)


async def test_rag_failure_does_not_claim_success_and_keeps_source(monkeypatch, tmp_path):
    from core.ai.source_archive import source_path
    from core.handlers.realtime_rag_handler import RealtimeRAGHandler

    monkeypatch.setenv("MESSAGE_SOURCE_DIR", str(tmp_path))
    generator, store = MagicMock(), MagicMock()
    store.is_messages_available.return_value = True
    generator.is_available.return_value = True
    generator.batch_generate.return_value = [None]
    monkeypatch.setattr("core.ai.embedding_generator.get_embedding_generator", lambda: generator)
    monkeypatch.setattr("core.ai.vector_store.get_vector_store", lambda: store)
    handler = RealtimeRAGHandler()
    assert not await handler.index_message("chan", "name", 8, "  original  ")
    assert handler.get_stats()["failed_count"] == 1
    store.replace_message_chunks.assert_not_called()
    assert json.loads(source_path("chan", 8).read_text(encoding="utf-8"))["text"] == "  original  "


def test_retrieval_serialization_preserves_full_tail():
    from core.ai.agent_tools import ToolExecutor

    text = "x" * 2000 + "TAIL"
    result = ToolExecutor._serialize_result({"summary_text": text, "metadata": {}})
    assert result["summary_text"] == text


async def test_merge_failure_is_failed_even_after_all_inputs_processed(runtime):
    client, path = runtime

    async def complete(**kwargs):
        if kwargs["messages"][1]["content"].startswith("合并"):
            raise ConnectionError("secret=not-for-audit")
        return response("mapped")

    client.chat.completions.create.side_effect = complete
    with pytest.raises(SummaryIntegrityError):
        await summarize_complete(["x" * 8000], "prompt", client, "test")
    audit = json.loads(next(path.glob("*.json")).read_text(encoding="utf-8"))
    assert audit["processed_count"] == 1
    assert audit["failed_count"] > 0
    assert audit["status"] == "failed"
    assert "secret" not in audit["error"]


async def test_every_chunk_has_source_metadata_and_link(runtime):
    client, _ = runtime
    message = (
        "频道: chan\n消息ID: 12\n发布时间: 2020-01-01\n内容: "
        + "x" * 5000
        + "\n链接: https://t.me/chan/12"
    )
    await summarize_complete([message], "prompt", client, "test")
    source_calls = [
        call.kwargs["messages"][1]["content"]
        for call in client.chat.completions.create.call_args_list
        if not call.kwargs["messages"][1]["content"].startswith("合并")
    ]
    assert len(source_calls) > 1
    assert all("消息ID: 12" in text and "https://t.me/chan/12" in text for text in source_calls)


async def test_delete_during_embedding_cannot_resurrect_message(monkeypatch, tmp_path):
    import asyncio
    import threading

    from core.ai.source_archive import source_path
    from core.handlers.realtime_rag_handler import RealtimeRAGHandler

    monkeypatch.setenv("MESSAGE_SOURCE_DIR", str(tmp_path))
    entered, release = threading.Event(), threading.Event()

    def embed(texts):
        entered.set()
        release.wait(timeout=5)
        return [[1.0, 0.0]]

    generator, store = MagicMock(), MagicMock()
    generator.batch_generate.side_effect = embed
    monkeypatch.setattr("core.ai.embedding_generator.get_embedding_generator", lambda: generator)
    monkeypatch.setattr("core.ai.vector_store.get_vector_store", lambda: store)
    handler = RealtimeRAGHandler()
    task = asyncio.create_task(handler.index_message("chan", "name", 8, "original"))
    await asyncio.to_thread(entered.wait, 5)
    try:
        await handler.handle_message_delete("chan", 8)
    finally:
        release.set()
    assert not await task
    assert handler._generations == {}
    store.replace_message_chunks.assert_not_called()
    assert not source_path("chan", 8).exists()


def test_embeddings_align_by_index_not_response_order():
    from core.ai.embedding_generator import EmbeddingGenerator

    generator = EmbeddingGenerator.__new__(EmbeddingGenerator)
    generator.client = MagicMock()
    generator.model = "test"
    generator.client.embeddings.create.return_value = SimpleNamespace(
        data=[
            SimpleNamespace(index=1, embedding=[0.0, 1.0]),
            SimpleNamespace(index=0, embedding=[1.0, 0.0]),
        ]
    )
    assert generator.batch_generate(["head", "tail"]) == [[1.0, 0.0], [0.0, 1.0]]
    generator.client.embeddings.create.return_value.data[1].index = 1
    assert generator.batch_generate(["head", "tail"]) == [None, None]


async def test_summary_fetch_includes_image_only_ocr_at_source_time(monkeypatch, tmp_path):
    from datetime import UTC, datetime

    from core.telegram import messaging

    monkeypatch.setenv("MESSAGE_SOURCE_DIR", str(tmp_path))
    published = datetime(2020, 1, 2, tzinfo=UTC)
    image = SimpleNamespace(id=42, text=None, date=published)
    full_ocr = "[Image OCR]\n" + "中文 English 🐱 code()\n" * 100 + "IMAGE_TAIL"

    async def iterator(*args, **kwargs):
        yield image

    client = MagicMock()
    client.iter_messages = iterator
    userbot = MagicMock()
    userbot.get_client.return_value = client
    userbot.is_available.return_value = True
    enrich = AsyncMock(return_value=full_ocr)
    monkeypatch.setattr(messaging, "get_userbot_client", lambda: userbot)
    monkeypatch.setattr(messaging, "enrich_message", enrich)

    fetched = await messaging.fetch_last_week_messages(["https://t.me/example"])
    enrich.assert_awaited_once_with(client, image, "https://t.me/example")
    assert full_ocr in fetched["https://t.me/example"][0]
    assert published.isoformat() in fetched["https://t.me/example"][0]
    record = json.loads(next(tmp_path.glob("*.json")).read_text(encoding="utf-8"))
    assert record["text"] == full_ocr
    assert record["created_at"] == published.isoformat()
    assert record["post_url"] == "https://t.me/example/42"
