import asyncio
from types import SimpleNamespace
from unittest.mock import AsyncMock

import pytest
from telegram import Message, Update


def update(
    message_id, *, text=None, caption=None, photo=False, document=False, album=None, user=1, chat=10
):
    msg = {
        "message_id": message_id,
        "date": 1,
        "chat": {"id": chat, "type": "private"},
        "from": {"id": user, "is_bot": False, "first_name": "Test"},
    }
    if text:
        msg["text"] = text
    if photo:
        msg["photo"] = [
            {
                "file_id": str(message_id),
                "file_unique_id": str(message_id),
                "width": 10,
                "height": 10,
                "file_size": 100,
            }
        ]
    if document:
        msg["document"] = {
            "file_id": str(message_id),
            "file_unique_id": str(message_id),
            "file_name": "photo.png",
            "mime_type": "image/png",
            "file_size": 100,
        }
    if caption:
        msg["caption"] = caption
    if album:
        msg["media_group_id"] = album
    return Update.de_json({"update_id": message_id, "message": msg}, None)


@pytest.fixture
def qa(monkeypatch):
    monkeypatch.setenv("QA_BOT_TOKEN", "123456:test_qa_token")
    from qa_bot import QABot

    q = QABot.__new__(QABot)
    q._image_context = None
    q._image_albums = {}
    q.user_system = SimpleNamespace(register_user=AsyncMock())
    q.quota_manager = SimpleNamespace(
        check_quota=AsyncMock(return_value={"allowed": True}), refund_quota=AsyncMock()
    )
    q.conversation_mgr = SimpleNamespace(clear_user_history=AsyncMock(return_value=0))
    q._stream_and_edit = AsyncMock(return_value=True)
    monkeypatch.setattr(Message, "reply_text", AsyncMock())
    monkeypatch.setattr(
        "core.config.ai_runtime.effective_ai_config",
        lambda **kw: SimpleNamespace(supports_vision=True),
    )
    return q


async def test_standalone_photo_then_question_and_caption_document(qa):
    context = SimpleNamespace(bot=object())
    await qa.handle_message(update(1, photo=True), context)
    qa.quota_manager.check_quota.assert_not_awaited()
    await qa.handle_message(update(2, text="这是什么"), context)
    options = qa._stream_and_edit.await_args.kwargs
    assert options["query"] == "这是什么"
    assert options["image_request"].scope == (1, 10)
    lease = qa._images().begin((1, 10), "s", "这是什么")
    assert [r.message_id for r in lease.refs] == [1]
    qa._images().finish(lease, True)
    await qa.handle_message(update(3, document=True, caption="这是什么颜色？"), context)
    assert qa._stream_and_edit.await_args.kwargs["query"] == "这是什么颜色？"
    assert qa._stream_and_edit.await_args.kwargs["image_request"].require_images


async def test_album_debounces_caption_and_keeps_every_member_in_message_order(qa):
    context = SimpleNamespace(bot=object())
    await qa.handle_message(update(3, photo=True, album="album", caption="比较这些图片"), context)
    await qa.handle_message(update(1, photo=True, album="album", caption="第一张的补充"), context)
    await qa.handle_message(update(2, photo=True, album="album"), context)
    await asyncio.sleep(1.1)
    qa._stream_and_edit.assert_awaited_once()
    assert qa._stream_and_edit.await_args.kwargs["query"] == "第一张的补充\n比较这些图片"
    lease = qa._images().begin((1, 10), "s", "比较这些图片")
    assert [r.message_id for r in lease.refs] == [1, 2, 3]
    qa._images().finish(lease, True)


async def test_clear_cancels_pending_album_and_removes_images(qa):
    context = SimpleNamespace(bot=object())
    await qa.handle_message(update(1, photo=True, album="album", caption="这是什么"), context)
    await qa.clear_command(update(2, text="/clear"), context)
    await asyncio.sleep(1)
    qa._stream_and_edit.assert_not_awaited()
    assert not qa._images().has_context((1, 10))
