# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
"""机器人发言开关的事件级回归，所有 Telegram 操作均使用模拟客户端。"""

from types import SimpleNamespace
from unittest.mock import AsyncMock

import pytest
from telethon import events
from telethon.errors import UserNotParticipantError

from .conftest import CHAT, USER


async def send(guard, *, message_id=42, text="正常内容", edited=False, via_bot_id=None):
    event = SimpleNamespace(
        chat_id=CHAT,
        sender_id=USER,
        is_group=True,
        id=message_id,
        message=SimpleNamespace(
            id=message_id,
            message=text,
            entities=[],
            edit_date="edited" if edited else None,
            via_bot_id=via_bot_id,
        ),
    )
    try:
        await guard.handle_message(event)
    except events.StopPropagation:
        pass


async def test_bot_messages_default_to_allowed(guard, store):
    guard.client.users[USER].bot = True
    assert not guard.group(CHAT).block_bot_messages
    await send(guard)
    assert not guard.client.calls and not store.cases


@pytest.mark.parametrize("text,edited", [("正常内容", False), ("图片说明", True), (None, False)])
async def test_enabled_bot_block_deletes_and_audits_messages_without_keyword_filter(
    guard, store, text, edited
):
    guard.group(CHAT).block_bot_messages = True
    guard.group(CHAT).nickname_filter = False
    guard.client.users[USER].bot = True
    assert not guard.group(CHAT).message_filter
    await send(guard, text=text, edited=edited)
    case = next(iter(store.cases.values()))
    assert case["action"] == "delete" and case["status"] == "done"
    assert case["payload"]["evidence"]["target"] == "bot_message"
    assert case["payload"]["evidence"]["rule_id"] == "bot-message-block"
    assert ("delete", CHAT, (42,)) in guard.client.calls
    assert not any(call[0] in {"ban", "unban", "restrict"} for call in guard.client.calls)
    await send(guard, text=text, edited=edited)
    assert len(store.cases) == 1
    assert sum(call[0] == "delete" for call in guard.client.calls) == 1


@pytest.mark.parametrize("protection", ["admin", "trusted_bot", "whitelist", "self"])
async def test_bot_block_preserves_existing_exemptions(guard, store, protection):
    guard.group(CHAT).block_bot_messages = True
    guard.client.users[USER].bot = True
    if protection == "admin":
        guard.client.admins.add(USER)
    elif protection == "trusted_bot":
        guard.group(CHAT).trusted_bot_ids = [USER]
    elif protection == "whitelist":
        store.white.add((CHAT, USER))
    else:
        guard.bot_id = USER
    await send(guard)
    assert not guard.client.calls and not store.cases


async def test_bot_block_does_not_affect_humans_or_inline_bot_results(guard, store):
    guard.group(CHAT).block_bot_messages = True
    await send(guard, via_bot_id=99999)
    assert not guard.client.calls and not store.cases


async def test_bot_block_requires_enabled_group(guard, store):
    guard.group(CHAT).block_bot_messages = True
    guard.group(CHAT).enabled = False
    guard.client.users[USER].bot = True
    await send(guard)
    assert not guard.client.calls and not store.cases


async def test_bot_block_hot_reload_can_enable_and_disable_without_changing_other_groups(
    guard, store
):
    guard.client.users[USER].bot = True
    config = guard.config.model_dump()
    config["groups"][str(CHAT)]["block_bot_messages"] = True
    guard.reload({"sakura_guard": config})
    await send(guard)
    assert ("delete", CHAT, (42,)) in guard.client.calls
    config["groups"][str(CHAT)]["block_bot_messages"] = False
    guard.reload({"sakura_guard": config})
    await send(guard, message_id=43)
    assert ("delete", CHAT, (43,)) not in guard.client.calls
    assert not guard.group(CHAT - 1).block_bot_messages
    assert len(store.cases) == 1


async def test_bot_message_switch_does_not_block_joining_by_itself(guard, store):
    guard.group(CHAT).block_bot_messages = True
    guard.client.users[USER].bot = True
    await guard.process_join(CHAT, guard.client.users[USER], "bot-join")
    assert not guard.client.calls and not store.cases


@pytest.mark.parametrize("source", ["nickname", "global_blacklist"])
async def test_existing_nickname_and_global_blacklist_still_take_priority(guard, store, source):
    guard.group(CHAT).block_bot_messages = True
    guard.client.users[USER].bot = True
    if source == "nickname":
        guard.client.users[USER].first_name = "主页"
    else:
        guard.group(CHAT).share_global_blacklist = True
        store.black[USER] = {"user_id": USER}
    await send(guard)
    assert next(iter(store.cases.values()))["action"] == "ban"
    assert next(iter(store.cases.values()))["payload"]["evidence"]["target"] == source


async def test_failed_bot_message_deletion_is_audited_as_failed(guard, store):
    guard.group(CHAT).block_bot_messages = True
    guard.client.users[USER].bot = True
    guard.client.delete_messages = AsyncMock(side_effect=PermissionError("delete denied"))
    await send(guard)
    case = next(iter(store.cases.values()))
    assert case["action"] == "delete" and case["status"] == "execution_failed"
    assert case["error"] == "PermissionError"


async def test_guest_bot_messages_are_not_exempt_for_being_nonmembers(guard, store):
    guard.group(CHAT).block_bot_messages = True
    guard.client.users[USER].bot = True
    guard.client.fail["permissions"] = UserNotParticipantError(request=None)
    await send(guard)
    assert ("delete", CHAT, (42,)) in guard.client.calls
    assert next(iter(store.cases.values()))["status"] == "done"


async def test_unknown_bot_permissions_still_fail_closed(guard, store):
    guard.group(CHAT).block_bot_messages = True
    guard.client.users[USER].bot = True
    guard.client.fail["permissions"] = RuntimeError("permissions unavailable")
    await send(guard)
    assert not guard.client.calls and not store.cases


@pytest.mark.parametrize("change", ["disabled", "whitelist"])
async def test_retried_bot_deletion_honors_current_switch_and_whitelist(guard, store, change):
    guard.group(CHAT).block_bot_messages = True
    guard.client.users[USER].bot = True
    guard.client.delete_messages = AsyncMock(side_effect=PermissionError("delete denied"))
    await send(guard)
    case = next(iter(store.cases.values()))
    if change == "disabled":
        guard.group(CHAT).block_bot_messages = False
    else:
        store.white.add((CHAT, USER))
    await store.update_case(case["id"], "executing")
    await guard.execute_case(store, await store.case(case["id"]))
    assert (await store.case(case["id"]))["status"] == "cancelled"
    assert guard.client.delete_messages.await_count == 1
