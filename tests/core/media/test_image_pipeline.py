# Copyright 2026 Sakura-Bot
# AGPL-3.0; https://github.com/Sakura520222/Sakura-Bot

import asyncio
import io
from datetime import UTC, datetime, timedelta
from types import SimpleNamespace
from unittest.mock import AsyncMock, MagicMock

import pytest
from PIL import Image, UnidentifiedImageError

from core.media.image_pipeline import (
    BoundedWriter,
    ImagePipeline,
    channel_key,
    combined_text,
    image_media,
)
from core.media.image_store import ImageStore, utc_naive


def message(mid=1, photo=10, caption="caption", date=None, grouped_id=None):
    return SimpleNamespace(
        id=mid,
        photo=SimpleNamespace(id=photo) if photo else None,
        document=None,
        message=caption,
        date=date or datetime(2020, 1, 1, tzinfo=UTC),
        grouped_id=grouped_id,
        file=SimpleNamespace(size=100),
    )


def store_mock():
    store = MagicMock()
    for name in (
        "initialize",
        "capture",
        "delete",
        "get",
        "query",
        "asset",
        "claim_digest",
        "recognize",
        "save_asset",
        "update_job",
    ):
        setattr(store, name, AsyncMock())
    return store


def test_image_detection_handles_photo_document_and_album():
    assert image_media(message()) == "photo:10"
    assert image_media(message(grouped_id=44)) == "photo:10"
    msg = message(photo=None)
    for mime in ("image/png", "image/jpeg"):
        msg.document = SimpleNamespace(id=99, mime_type=mime)
        assert image_media(msg) == "document:99"
    msg.document.mime_type = "application/pdf"
    assert image_media(msg) is None


def test_caption_and_date_are_preserved():
    caption = "  caption\n" + "长" * 10000
    assert combined_text(caption, {"ocr": "OCR", "description": "visual"}).startswith(
        caption + "\n\n"
    )
    assert channel_key(-10012345) == "12345"
    assert utc_naive(datetime(2000, 1, 1, tzinfo=UTC)) == datetime(2000, 1, 1)


def test_bounded_writer_rejects_before_write():
    target = io.BytesIO()
    writer = BoundedWriter(target, 3)
    writer.write(b"abc")
    with pytest.raises(ValueError, match="image_size_limit"):
        writer.write(b"d")
    assert target.getvalue() == b"abc"


@pytest.mark.asyncio
async def test_capture_album_members_preserves_original_links_and_time(tmp_path):
    store = store_mock()
    pipeline = ImagePipeline(store, tmp_path)
    chat = SimpleNamespace(id=123, title="频道", username="channel")
    for mid in (1, 2, 3):
        await pipeline.capture(message(mid=mid, grouped_id=42), chat)
    rows = [call.args[0] for call in store.capture.await_args_list]
    assert [r["message_id"] for r in rows] == [1, 2, 3]
    assert {r["grouped_id"] for r in rows} == {"42"}
    assert rows[0]["source_date"] == datetime(2020, 1, 1)
    assert rows[0]["source_link"] == "https://t.me/channel/1"
    chat.username = None
    await pipeline.capture(message(), chat)
    assert store.capture.await_args.args[0]["source_link"] == "https://t.me/c/123/1"


@pytest.mark.asyncio
async def test_removed_media_invalidates_old_ocr(tmp_path):
    store = store_mock()
    pipeline = ImagePipeline(store, tmp_path)
    assert not await pipeline.capture(message(photo=None), SimpleNamespace(id=123))
    store.delete.assert_awaited_once_with("123", 1)


@pytest.mark.asyncio
async def test_download_content_addressing_and_signature_validation(tmp_path):
    pipeline = ImagePipeline(store_mock(), tmp_path)
    data = io.BytesIO()
    Image.new("RGB", (2, 2)).save(data, format="PNG")

    async def download(msg, file):
        file.write(data.getvalue())

    pipeline.client = SimpleNamespace(download_media=download)
    a = await pipeline._download(message())
    b = await pipeline._download(message())
    assert a == b
    assert a[1] == "image/png"
    assert len(list(tmp_path.glob("*/*"))) == 1
    assert not list(tmp_path.glob("download-*"))
    data = io.BytesIO(b"not an image")
    with pytest.raises(UnidentifiedImageError):
        await pipeline._download(message())
    assert not list(tmp_path.glob("download-*"))


def asset_and_job():
    digest = "a" * 64
    asset = {"sha256": digest, "recognized": False, "ocr": "", "description": ""}
    job = {
        "channel_id": "123",
        "message_id": 1,
        "revision": "rev",
        "lease_token": "lease",
        "sha256": digest,
        "deleted": False,
        "caption": "original",
        "attempts": 0,
    }
    return asset, job


@pytest.mark.asyncio
async def test_repeated_images_share_recognition_but_keep_each_reference(tmp_path):
    store = store_mock()
    asset, job = asset_and_job()
    store.asset.side_effect = lambda _: dict(asset)
    store.get.side_effect = lambda channel, mid: {**job, "message_id": mid}

    async def recognized(digest, ocr, description, model, job):
        asset.update(recognized=True, ocr=ocr, description=description)
        return True

    store.recognize.side_effect = recognized
    recognize = AsyncMock(return_value={"ocr": "OCR", "description": "visual", "model": "vision"})
    publish = AsyncMock(return_value=True)
    pipeline = ImagePipeline(store, tmp_path, recognizer=recognize, publisher=publish)
    await asyncio.gather(pipeline.process(job), pipeline.process({**job, "message_id": 2}))
    assert recognize.await_count == 1
    assert publish.await_count == 2
    assert (
        publish.await_args.args[1] == "original\n\n[Image OCR]\nOCR\n\n[Image description]\nvisual"
    )
    assert store.update_job.await_count == 2


@pytest.mark.asyncio
async def test_delete_or_edit_while_recognizing_does_not_publish(tmp_path):
    store = store_mock()
    asset, job = asset_and_job()
    store.asset.return_value = asset
    store.get.return_value = {**job, "revision": "edited"}
    recognize = AsyncMock(return_value={"ocr": "old", "description": "", "model": "v"})
    publish = AsyncMock()
    pipeline = ImagePipeline(store, tmp_path, recognizer=recognize, publisher=publish)
    await pipeline.process(job)
    publish.assert_not_awaited()
    store.update_job.assert_not_awaited()


@pytest.mark.asyncio
async def test_recognition_or_index_failure_not_marked_complete(tmp_path):
    store = store_mock()
    asset, job = asset_and_job()
    store.asset.return_value = asset
    store.get.return_value = job
    recognize = AsyncMock(side_effect=RuntimeError("provider failed"))
    pipeline = ImagePipeline(
        store, tmp_path, recognizer=recognize, publisher=AsyncMock(return_value=False)
    )
    with pytest.raises(RuntimeError):
        await pipeline.process(job)
    store.update_job.assert_not_awaited()
    recognize.side_effect = None
    recognize.return_value = {"ocr": "text", "description": "", "model": "v"}
    with pytest.raises(RuntimeError, match="image_index_unavailable"):
        await pipeline.process(job)
    store.update_job.assert_not_awaited()


@pytest.mark.asyncio
async def test_disabled_vision_stores_without_consuming_retries(tmp_path):
    store = store_mock()
    asset, job = asset_and_job()
    store.asset.return_value = asset
    store.get.return_value = job
    pipeline = ImagePipeline(store, tmp_path)
    pipeline.vision_enabled = lambda: False
    await pipeline.process(job)
    store.update_job.assert_awaited_once_with(job, "stored", sha256=job["sha256"])


@pytest.mark.asyncio
async def test_backfill_respects_half_open_original_time_range(tmp_path):
    store = store_mock()
    pipeline = ImagePipeline(store, tmp_path)
    start = datetime(2020, 1, 1, tzinfo=UTC)
    end = start + timedelta(days=7)

    async def iterator(*args, **kwargs):
        assert kwargs["limit"] == 100
        for mid, date in enumerate((start - timedelta(days=1), start, end)):
            yield message(mid=mid, date=date)

    client = SimpleNamespace(
        get_entity=AsyncMock(return_value=SimpleNamespace(id=123, username=None)),
        iter_messages=iterator,
    )
    assert await pipeline.backfill(client, "channel", start, end, limit=100) == 1
    assert store.capture.await_args.args[0]["source_date"] == start.replace(tzinfo=None)


@pytest.mark.asyncio
async def test_retry_has_bound_and_lease_revision_guard():
    store = ImageStore(None)
    store.query = AsyncMock(return_value=1)
    _, job = asset_and_job()
    await store.update_job({**job, "attempts": 2}, "pending", error="TimeoutError")
    sql, args = store.query.await_args.args
    assert args[0] == "failed"
    assert args[2] == 3
    assert "revision=%s" in sql and "lease_token=%s" in sql and "deleted=FALSE" in sql


@pytest.mark.asyncio
async def test_failed_job_restart_and_force_reprocessing():
    store = ImageStore(None)
    _, job = asset_and_job()
    store.get = AsyncMock(return_value=job)
    store.query = AsyncMock()
    assert await store.retry("123", 1, force=True)
    queries = [call.args[0] for call in store.query.await_args_list]
    assert any("recognized=FALSE" in query for query in queries)
    assert any("WHERE a.sha256=%s AND m.deleted=FALSE" in query for query in queries)
    store.get.return_value = {**job, "deleted": True}
    assert not await store.retry("123", 1)


@pytest.mark.asyncio
async def test_claim_recovers_expired_lease():
    store = ImageStore(None)
    store.query = AsyncMock(side_effect=[1, []])
    assert await store.claim() is None
    sql = store.query.await_args_list[0].args[0]
    assert "lease_until<UTC_TIMESTAMP(6)" in sql
    assert "LIMIT 1" in sql


@pytest.mark.asyncio
async def test_cleanup_defaults_to_dry_run_and_requires_stopped_worker(tmp_path):
    store = store_mock()
    digest = "b" * 64
    store.query.return_value = [{"sha256": digest, "storage_path": "/arbitrary/path"}]
    pipeline = ImagePipeline(store, tmp_path)
    path = tmp_path / digest[:2] / digest
    path.parent.mkdir()
    path.write_bytes(b"original")
    assert await pipeline.cleanup_orphans(datetime.now(UTC)) == [digest]
    assert path.exists()
    assert store.query.await_count == 1
    pipeline.tasks = [MagicMock()]
    with pytest.raises(RuntimeError, match="stop_pipeline"):
        await pipeline.cleanup_orphans(datetime.now(UTC), dry_run=False)


@pytest.mark.asyncio
async def test_summary_never_silently_omits_unprocessed_image(monkeypatch, tmp_path):
    import core.media.image_pipeline as module

    pipeline = ImagePipeline(store_mock(), tmp_path)
    monkeypatch.setattr(module, "get_image_pipeline", lambda: pipeline)
    result = await module.enrich_message(None, message(caption=""), "channel")
    assert "unavailable" in result
    pipeline.tasks = [MagicMock()]
    pipeline.capture = AsyncMock(side_effect=RuntimeError("db error"))
    client = SimpleNamespace(get_entity=AsyncMock(return_value=SimpleNamespace(id=123)))
    result = await module.enrich_message(client, message(caption="original"), "channel")
    assert result.startswith("original")
    assert "error" in result


@pytest.mark.asyncio
async def test_summary_reads_saved_images_with_workers_stopped(monkeypatch, tmp_path):
    import core.media.image_pipeline as module

    store = store_mock()
    store.text = AsyncMock(return_value={"ocr": "保存的文字", "description": "保存的画面"})
    store.get.return_value = {"deleted": False, "media_id": "photo:10"}
    pipeline = ImagePipeline(store, tmp_path)
    pipeline.capture = AsyncMock()
    monkeypatch.setattr(module, "get_image_pipeline", lambda: pipeline)
    client = SimpleNamespace(get_entity=AsyncMock(return_value=SimpleNamespace(id=123)))
    result = await module.enrich_message(client, message(), "channel")
    assert "保存的文字" in result and "保存的画面" in result
    pipeline.capture.assert_not_awaited()
    store.get.return_value = {"deleted": False, "media_id": "photo:999"}
    result = await module.enrich_message(client, message(), "channel")
    assert "保存的文字" not in result and "unavailable" in result


@pytest.mark.asyncio
async def test_poll_image_bytes_are_durable_and_link_original_reference(tmp_path):
    import hashlib

    store = store_mock()
    pipeline = ImagePipeline(store, tmp_path)
    image = io.BytesIO()
    Image.new("RGB", (2, 2)).save(image, format="PNG")
    data = image.getvalue()
    digest = hashlib.sha256(data).hexdigest()
    for _ in range(2):
        assert await pipeline.store_poll_image(data, "image/png", "photo:10") == digest
    path = tmp_path / digest[:2] / digest
    assert path.read_bytes() == data
    assert list(tmp_path.glob("*/*")) == [path]
    store.save_asset.assert_awaited_with(digest, "image/png", len(data), str(path))
    sql, args = store.query.await_args.args
    assert "media_id=%s" in sql and "deleted=FALSE" in sql
    assert args == (digest, "photo:10")


@pytest.mark.asyncio
@pytest.mark.parametrize("poll_first", [False, True])
async def test_poll_and_channel_share_recognition_and_original_index(tmp_path, poll_first):
    store = store_mock()
    asset, job = asset_and_job()
    current = dict(job)
    store.asset.side_effect = lambda digest: dict(asset)
    store.get.side_effect = lambda *args: dict(current)
    entered, release = asyncio.Event(), asyncio.Event()

    async def claim(digest):
        current["lease_token"] = "poll-lease"
        return dict(current)

    async def recognize(image):
        entered.set()
        await release.wait()
        return {"ocr": "角色数值", "description": "角色图片", "model": "vision"}

    async def save(digest, ocr, description, model, source):
        assert source["revision"] == current["revision"]
        assert source["lease_token"] == current["lease_token"]
        asset.update(recognized=True, ocr=ocr, description=description, model=model)
        return True

    store.claim_digest.side_effect = claim
    store.recognize.side_effect = save
    recognizer = AsyncMock(side_effect=recognize)
    publisher = AsyncMock(return_value=True)
    pipeline = ImagePipeline(store, tmp_path, recognizer=recognizer, publisher=publisher)
    first = asyncio.create_task(
        pipeline.describe_for_poll(job["sha256"]) if poll_first else pipeline.process(job)
    )
    await entered.wait()
    second = asyncio.create_task(
        pipeline.process(job) if poll_first else pipeline.describe_for_poll(job["sha256"])
    )
    release.set()
    await asyncio.gather(first, second)
    recognizer.assert_awaited_once()
    store.recognize.assert_awaited_once()
    publisher.assert_awaited_once()
    assert "角色数值" in publisher.await_args.args[1]
    assert "角色图片" in publisher.await_args.args[1]
    assert publisher.await_args.args[0]["message_id"] == job["message_id"]
    # A later request (including after restart) uses the durable asset alone.
    assert (await ImagePipeline(store, tmp_path).describe_for_poll(job["sha256"]))[
        "ocr"
    ] == "角色数值"
    recognizer.assert_awaited_once()


@pytest.mark.asyncio
async def test_saved_poll_description_survives_index_outage(tmp_path):
    store = store_mock()
    asset, job = asset_and_job()
    store.asset.side_effect = lambda digest: dict(asset)
    store.get.return_value = job
    store.claim_digest.return_value = job

    async def save(digest, ocr, description, model, source):
        asset.update(recognized=True, ocr=ocr, description=description)
        return True

    store.recognize.side_effect = save
    pipeline = ImagePipeline(
        store,
        tmp_path,
        recognizer=AsyncMock(return_value={"ocr": "OCR", "description": "visible", "model": "v"}),
        publisher=AsyncMock(return_value=False),
    )
    result = await pipeline.describe_for_poll(job["sha256"])
    assert result["recognized"] and result["description"] == "visible"
    store.update_job.assert_awaited_once_with(job, "pending", error="RuntimeError")


@pytest.mark.asyncio
async def test_poll_recognition_uses_pinned_client_when_channel_ocr_disabled(monkeypatch, tmp_path):
    import core.ai.ai_client as ai

    config = SimpleNamespace(
        vision_enabled=False,
        supports_vision=True,
        vision_ocr=False,
        vision_description=False,
        model="vision",
    )
    response = SimpleNamespace(
        choices=[
            SimpleNamespace(
                finish_reason="stop",
                message=SimpleNamespace(content='{"ocr":"", "description":"角色"}'),
            )
        ]
    )
    client = MagicMock()
    client.chat.completions.create = AsyncMock(return_value=response)
    client.with_options.side_effect = AssertionError(
        "must use configured client without a new timeout"
    )
    runtime = MagicMock(return_value=(client, "vision"))
    monkeypatch.setattr(ai, "get_async_ai_runtime", runtime)
    path = tmp_path / "image"
    path.write_bytes(b"image")
    result = await ImagePipeline(store_mock(), tmp_path)._recognize(
        {"storage_path": str(path), "mime_type": "image/png"}, for_poll=True, config=config
    )
    assert result == {"ocr": "", "description": "角色", "model": "vision"}
    runtime.assert_called_once_with(purpose="vision", config=config)
    client.with_options.assert_not_called()


@pytest.mark.asyncio
async def test_publish_awaits_index_success_with_source_timestamp(monkeypatch, tmp_path):
    import sys

    handler = SimpleNamespace(index_message=AsyncMock(return_value=True))
    monkeypatch.setitem(
        sys.modules,
        "core.handlers.realtime_rag_handler",
        SimpleNamespace(get_realtime_rag_handler=lambda: handler),
    )
    pipeline = ImagePipeline(store_mock(), tmp_path)
    record = {
        "channel_id": "123",
        "message_id": 1,
        "channel_name": "name",
        "channel_username": "channel",
        "source_date": datetime(2020, 1, 1),
    }
    record.update(revision="revision", deleted=False)
    pipeline.store.get.return_value = record
    assert await pipeline._publish(record, "full OCR")
    assert handler.index_message.await_args.kwargs["created_at"] == "2020-01-01T00:00:00+00:00"


@pytest.mark.asyncio
@pytest.mark.parametrize(
    "finish,content", [("length", '{"ocr":"clipped","description":""}'), ("stop", "")]
)
async def test_vision_rejects_partial_or_empty_responses(monkeypatch, tmp_path, finish, content):
    import sys

    config = SimpleNamespace(
        vision_enabled=True,
        supports_vision=True,
        vision_ocr=True,
        vision_description=True,
        model="vision",
    )
    client = SimpleNamespace(
        chat=SimpleNamespace(
            completions=SimpleNamespace(
                create=AsyncMock(
                    return_value=SimpleNamespace(
                        choices=[
                            SimpleNamespace(
                                finish_reason=finish, message=SimpleNamespace(content=content)
                            )
                        ]
                    )
                )
            )
        )
    )
    monkeypatch.setitem(
        sys.modules,
        "core.config.ai_runtime",
        SimpleNamespace(effective_ai_config=lambda **kwargs: config),
    )
    monkeypatch.setitem(
        sys.modules,
        "core.ai.ai_client",
        SimpleNamespace(get_async_ai_runtime=lambda **kwargs: (client, "vision")),
    )
    path = tmp_path / "image"
    path.write_bytes(b"image")
    pipeline = ImagePipeline(store_mock(), tmp_path)
    with pytest.raises(ValueError):
        await pipeline._recognize({"storage_path": str(path), "mime_type": "image/png"})


@pytest.mark.asyncio
async def test_capture_is_idempotent_but_edit_resets_job():
    store = ImageStore(None)
    record = {
        "channel_id": "123",
        "message_id": 1,
        "media_id": "photo:10",
        "grouped_id": None,
        "channel_name": "name",
        "channel_username": None,
        "source_date": datetime(2020, 1, 1),
        "source_link": "https://t.me/c/123/1",
        "caption": "caption",
    }
    store.get = AsyncMock(return_value={**record, "deleted": False, "sha256": "a" * 64})
    store.query = AsyncMock()
    await store.capture(record)
    store.query.assert_not_awaited()
    await store.capture({**record, "caption": "changed"})
    assert store.query.await_args.args[1][-2] == "a" * 64
    store.get.return_value["deleted"] = True
    await store.capture(record)
    # 已清理的 tombstone 不能复用可能已回收的文件哈希。
    assert store.query.await_args.args[1][-2] is None


@pytest.mark.asyncio
async def test_delete_during_indexing_cleans_late_vectors(monkeypatch, tmp_path):
    import sys

    handler = SimpleNamespace(
        index_message=AsyncMock(return_value=True), handle_message_delete=AsyncMock()
    )
    monkeypatch.setitem(
        sys.modules,
        "core.handlers.realtime_rag_handler",
        SimpleNamespace(get_realtime_rag_handler=lambda: handler),
    )
    store = store_mock()
    store.get.return_value = {"deleted": True}
    pipeline = ImagePipeline(store, tmp_path)
    record = {
        "channel_id": "123",
        "message_id": 1,
        "channel_name": "name",
        "channel_username": None,
        "source_date": datetime(2020, 1, 1),
        "revision": "rev",
    }
    assert not await pipeline._publish(record, "OCR")
    handler.handle_message_delete.assert_awaited_once_with("123", 1)


@pytest.mark.asyncio
async def test_force_retry_discards_inflight_result_and_recognizes_again(tmp_path):
    store = store_mock()
    asset, old_job = asset_and_job()
    current = dict(old_job)
    store.asset.side_effect = lambda digest: dict(asset)
    store.get.side_effect = lambda channel, mid: dict(current)
    started, release = asyncio.Event(), asyncio.Event()
    calls = 0

    async def recognize(image):
        nonlocal calls
        calls += 1
        if calls == 1:
            started.set()
            await release.wait()
            return {"ocr": "obsolete", "description": "", "model": "old"}
        return {"ocr": "fresh", "description": "", "model": "new"}

    async def guarded_write(digest, ocr, description, model, job):
        if current["revision"] != job["revision"] or current["lease_token"] != job["lease_token"]:
            return False
        asset.update(ocr=ocr, description=description, model=model, recognized=True)
        return True

    async def force_update(sql, args):
        # 一个原子 SQL 必须同时废弃共享识别缓存和所有活引用的租约。
        assert "a.recognized=FALSE" in sql and "m.revision=%s" in sql
        asset["recognized"] = False
        current.update(revision=args[0], lease_token=None)
        return 1

    store.recognize.side_effect = guarded_write
    store.query.side_effect = force_update
    publish = AsyncMock(return_value=True)
    pipeline = ImagePipeline(store, tmp_path, recognizer=recognize, publisher=publish)
    old_task = asyncio.create_task(pipeline.process(old_job))
    await started.wait()
    assert await ImageStore.retry(store, "123", 1, force=True)
    current["lease_token"] = "new-lease"
    new_job = dict(current)
    fresh_task = asyncio.create_task(pipeline.process(new_job))
    release.set()
    await asyncio.gather(old_task, fresh_task)
    assert calls == 2
    assert asset["ocr"] == "fresh"
    publish.assert_awaited_once()
    assert "fresh" in publish.await_args.args[1]
    assert "obsolete" not in publish.await_args.args[1]
    store.update_job.assert_awaited_once()


@pytest.mark.asyncio
async def test_recognition_write_atomically_checks_revision_and_lease():
    store = ImageStore(None)
    _, job = asset_and_job()
    store.query = AsyncMock(return_value=0)
    assert not await store.recognize(job["sha256"], "ocr", "description", "model", job)
    sql, args = store.query.await_args.args
    assert "JOIN message_images" in sql
    assert "m.revision=%s" in sql and "m.lease_token=%s" in sql and "m.deleted=FALSE" in sql
    assert args[-2:] == (job["revision"], job["lease_token"])


@pytest.mark.asyncio
async def test_empty_new_recognition_still_updates_index(tmp_path):
    store = store_mock()
    asset, job = asset_and_job()
    job["caption"] = ""
    store.asset.return_value = asset
    store.get.return_value = job
    store.recognize.return_value = True
    publish = AsyncMock(return_value=True)
    pipeline = ImagePipeline(
        store,
        tmp_path,
        publisher=publish,
        recognizer=AsyncMock(return_value={"ocr": "", "description": "", "model": "new"}),
    )
    await pipeline.process(job)
    publish.assert_awaited_once_with(job, "")
    store.update_job.assert_awaited_once()


@pytest.mark.asyncio
async def test_empty_publication_removes_old_ocr_vectors(monkeypatch, tmp_path):
    import sys

    handler = SimpleNamespace(
        index_message=AsyncMock(), handle_message_delete=AsyncMock(return_value=True)
    )
    monkeypatch.setitem(
        sys.modules,
        "core.handlers.realtime_rag_handler",
        SimpleNamespace(get_realtime_rag_handler=lambda: handler),
    )
    store = store_mock()
    _, record = asset_and_job()
    store.get.return_value = record
    pipeline = ImagePipeline(store, tmp_path)
    assert await pipeline._publish(record, "")
    handler.handle_message_delete.assert_awaited_once_with("123", 1)
    handler.index_message.assert_not_awaited()
