import base64
import hashlib
from types import SimpleNamespace
from unittest.mock import AsyncMock

import pytest

from core.media import image_descriptions


@pytest.fixture
def pipeline(monkeypatch):
    import core.infrastructure.database.manager as db
    import core.media.image_pipeline as images

    store = SimpleNamespace(get=AsyncMock(return_value=None))
    pipeline = SimpleNamespace(
        store=store,
        client=None,
        capture=AsyncMock(),
        store_poll_image=AsyncMock(),
        describe_for_poll=AsyncMock(return_value={"ocr": "图中文字", "description": "角色立绘"}),
    )
    monkeypatch.setattr(db, "get_db_manager", lambda: SimpleNamespace(pool=object()))
    monkeypatch.setattr(images, "get_image_pipeline", lambda: pipeline)
    return pipeline


async def test_poll_uses_persisted_ocr_and_description_in_image_order(pipeline):
    images = ["data:image/png;base64," + base64.b64encode(data).decode() for data in (b"a", b"b")]
    config = object()
    descriptions = await image_descriptions.saved_poll_descriptions(images, config)
    assert descriptions == [
        f"图{index}：\n图片描述：角色立绘\n图中文字：图中文字" for index in (1, 2)
    ]
    assert [call.args[0] for call in pipeline.describe_for_poll.await_args_list] == [
        hashlib.sha256(data).hexdigest() for data in (b"a", b"b")
    ]
    assert all(
        call.kwargs["config"] is config for call in pipeline.describe_for_poll.await_args_list
    )


async def test_discussion_arriving_first_registers_original_channel_message(pipeline):
    peer = SimpleNamespace(channel_id=1944394067)
    forward = SimpleNamespace(from_id=peer, channel_post=12876)
    photo = SimpleNamespace(id=10)
    forwarded = SimpleNamespace(fwd_from=forward, photo=photo)
    chat = SimpleNamespace(id=1944394067, username="Nahida_Leak")
    original = SimpleNamespace(id=12876, photo=photo, chat=chat)
    client = SimpleNamespace(get_messages=AsyncMock(return_value=original))
    await image_descriptions.register_poll_image(b"image", "image/png", forwarded, client)
    client.get_messages.assert_awaited_once_with(peer, ids=12876)
    pipeline.capture.assert_awaited_once_with(original, chat)
    pipeline.store_poll_image.assert_awaited_once_with(b"image", "image/png", "photo:10")


async def test_existing_source_is_reused_without_fetching_original(pipeline):
    pipeline.store.get.return_value = {"deleted": False, "media_id": "photo:10"}
    forwarded = SimpleNamespace(
        fwd_from=SimpleNamespace(from_id=SimpleNamespace(channel_id=123), channel_post=1),
        photo=SimpleNamespace(id=10),
    )
    client = SimpleNamespace(get_messages=AsyncMock())
    await image_descriptions.register_poll_image(b"image", "image/png", forwarded, client)
    client.get_messages.assert_not_awaited()
    pipeline.capture.assert_not_awaited()


async def test_deleted_or_replaced_original_cannot_publish_stale_image(pipeline):
    forwarded = SimpleNamespace(
        fwd_from=SimpleNamespace(from_id=SimpleNamespace(channel_id=123), channel_post=1),
        photo=SimpleNamespace(id=10),
    )
    client = SimpleNamespace(get_messages=AsyncMock(return_value=None))
    with pytest.raises(ValueError, match="poll_image_source_changed"):
        await image_descriptions.register_poll_image(b"image", "image/png", forwarded, client)
    pipeline.capture.assert_not_awaited()
    pipeline.store_poll_image.assert_not_awaited()


def test_empty_saved_results_and_invalid_data_urls_are_rejected():
    with pytest.raises(ValueError, match="empty_saved_image_description"):
        image_descriptions.description_text({"ocr": "", "description": " "})
    with pytest.raises(ValueError, match="invalid_image_data_url"):
        image_descriptions.image_data("https://example.com/private.png")
