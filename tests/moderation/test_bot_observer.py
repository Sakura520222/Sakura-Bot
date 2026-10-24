# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
"""其他 Bot 消息补检、结构过滤动作优先级和两路投递去重。"""

from types import SimpleNamespace
from unittest.mock import AsyncMock

import pytest
from telethon import Button, events
from telethon.tl.types import KeyboardButtonRow, ReplyInlineMarkup

from .conftest import CHAT, USER


def button_event(guard):
    guard.client.users[USER].bot = True
    guard.client.admins.add(guard.bot_id)
    return SimpleNamespace(
        id=55,
        chat_id=CHAT,
        is_group=True,
        sender_id=USER,
        get_sender=AsyncMock(return_value=guard.client.users[USER]),
        message=SimpleNamespace(
            id=55,
            message="广告",
            entities=[],
            edit_date=None,
            reply_markup=ReplyInlineMarkup(
                [KeyboardButtonRow([Button.url("打开", "https://example.com")])]
            ),
        ),
    )


async def test_specific_button_ban_beats_generic_bot_delete_and_purges_messages(guard, store):
    event = button_event(guard)
    config = guard.group(CHAT)
    config.block_bot_messages = True
    config.bot_message_action = "delete"
    config.block_button_messages = True
    config.button_message_action = "ban"
    await guard.bot_observer.handle(event)
    case = next(iter(store.cases.values()))
    assert case["action"] == "ban" and case["status"] == "pending"
    assert case["payload"]["evidence"]["target"] == "button_message"
    guard.ban_history.revoke.assert_awaited_once_with(CHAT, USER, guard.bot_id)
    with pytest.raises(events.StopPropagation):
        await guard.handle_message(event)
    assert sum(call[0] == "ban" for call in guard.client.calls) == 1
    assert len(store.cases) == 1


@pytest.mark.parametrize(
    "exemption", ["admin", "whitelist", "trusted", "disabled", "human", "self"]
)
async def test_observer_preserves_identity_permissions_and_existing_exemptions(
    guard, store, exemption
):
    event = button_event(guard)
    guard.group(CHAT).block_button_messages = True
    guard.group(CHAT).button_message_action = "ban"
    if exemption == "admin":
        guard.client.admins.add(USER)
    elif exemption == "whitelist":
        store.white.add((CHAT, USER))
    elif exemption == "trusted":
        guard.group(CHAT).trusted_bot_ids = [USER]
    elif exemption == "disabled":
        guard.group(CHAT).enabled = False
    elif exemption == "human":
        guard.client.users[USER].bot = False
    else:
        guard.bot_id = USER
    await guard.bot_observer.handle(event)
    assert not store.cases and not guard.client.calls


async def test_observer_lifecycle_detaches_all_handlers(guard):
    client = SimpleNamespace(
        add_event_handler=lambda *args: None, remove_event_handler=lambda *args: None
    )
    guard.bot_observer.attach(client)
    assert len(guard.bot_observer.handlers) == 2
    await guard.stop()
    assert not guard.bot_observer.handlers and guard.bot_observer.client is None
