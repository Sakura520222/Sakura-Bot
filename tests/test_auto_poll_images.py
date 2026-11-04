from types import SimpleNamespace
from unittest.mock import AsyncMock, MagicMock

import pytest

from core.handlers import auto_poll_handler as module
from core.handlers import channel_comment_welcome as welcome


@pytest.fixture
def photo_task():
    return {
        "channel_id": "@ch",
        "discussion_id": 123,
        "forward_msg_id": 456,
        "message_text": "",
        "media_message": SimpleNamespace(id=456),
    }


async def test_image_only_poll_is_generated_and_replies_to_original_post(monkeypatch, photo_task):
    client = SimpleNamespace(send_message=AsyncMock(return_value=SimpleNamespace(id=999)))
    handler = module.AutoPollHandler(client)
    monkeypatch.setattr(module, "is_auto_poll_enabled_for_channel", lambda channel: True)
    collect = AsyncMock(return_value=("", ["data:image/png;base64,IMAGE"]))
    generate = AsyncMock(
        return_value={"question": "你会给这张图起什么名字？", "options": ["好笑", "可爱"]}
    )
    monkeypatch.setattr(module, "collect_poll_images", collect)
    monkeypatch.setattr(module, "generate_poll_from_summary", generate)
    await handler._process_poll_task(photo_task)
    generate.assert_awaited_once_with("", images=["data:image/png;base64,IMAGE"])
    assert client.send_message.await_args.kwargs["reply_to"] == 456
    assert handler._processed_count == 1


@pytest.mark.parametrize("disabled_during_generation", [False, True])
async def test_disabling_prevents_media_work_or_sending(
    monkeypatch, photo_task, disabled_during_generation
):
    client = SimpleNamespace(send_message=AsyncMock())
    handler = module.AutoPollHandler(client)
    enabled = [disabled_during_generation]
    monkeypatch.setattr(module, "is_auto_poll_enabled_for_channel", lambda channel: enabled[0])
    collect = AsyncMock(return_value=("", ["data:image/png;base64,IMAGE"]))

    async def generate(*args, **kwargs):
        enabled[0] = False
        return {"question": "测试", "options": ["A", "B"]}

    monkeypatch.setattr(module, "collect_poll_images", collect)
    monkeypatch.setattr(module, "generate_poll_from_summary", AsyncMock(side_effect=generate))
    await handler._process_poll_task(photo_task)
    client.send_message.assert_not_awaited()
    assert handler._processed_count == 0
    if not disabled_during_generation:
        collect.assert_not_awaited()


async def test_image_failure_skips_the_poll_instead_of_posting_generic_options(
    monkeypatch, photo_task
):
    client = SimpleNamespace(send_message=AsyncMock())
    handler = module.AutoPollHandler(client)
    monkeypatch.setattr(module, "is_auto_poll_enabled_for_channel", lambda channel: True)
    monkeypatch.setattr(
        module, "collect_poll_images", AsyncMock(return_value=("", ["data:image/png;base64,IMAGE"]))
    )
    monkeypatch.setattr(module, "generate_poll_from_summary", AsyncMock(return_value=None))
    monkeypatch.setattr(module, "record_error", MagicMock())
    await handler._process_poll_task(photo_task)
    client.send_message.assert_not_awaited()
    assert handler._failed_count == 1 and handler._processed_count == 0


async def test_discussion_forward_passes_the_image_reference_to_poll_queue(monkeypatch):
    from telethon.tl.types import PeerChannel

    welcome._message_cache.clear()
    client = AsyncMock()
    client.return_value = SimpleNamespace(full_chat=SimpleNamespace(linked_chat_id=99))
    handler = welcome.CommentWelcomeHandler(client)
    handler._resolve_linked_channel = AsyncMock(return_value=("ch", "@ch"))
    handler._get_channel_identifier = AsyncMock(return_value="ch")
    poll_handler = SimpleNamespace(_running=True, enqueue_poll_task=MagicMock(return_value=True))
    monkeypatch.setattr(module, "get_auto_poll_handler", lambda: poll_handler)
    msg = SimpleNamespace(
        id=456,
        chat_id=-100123,
        text="",
        grouped_id=None,
        photo=SimpleNamespace(id=1),
        document=None,
        fwd_from=SimpleNamespace(from_id=PeerChannel(99), channel_post=1),
    )
    event = SimpleNamespace(message=msg, chat=SimpleNamespace(megagroup=True))
    await handler.handle_discussion_message(event)
    poll_handler.enqueue_poll_task.assert_called_once_with(
        channel_id="@ch",
        discussion_id=-100123,
        forward_msg_id=456,
        message_text="",
        media_message=msg,
    )
    welcome._message_cache.clear()
