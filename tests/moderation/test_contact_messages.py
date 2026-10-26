# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
"""原生联系人过滤：只处罚发送者，复用权限、清理、热更新和重试约束。"""

import json
from types import SimpleNamespace

import pytest
from telethon import events
from telethon.tl.types import MessageMediaContact, MessageMediaDocument

from core.moderation.ai_reports import match_message, message_snapshot
from core.moderation.config import GuardGroup
from core.moderation.matcher import Matcher

from .conftest import ADMIN, CHAT, USER


def contact():
    return MessageMediaContact(
        phone_number="15550001111",
        first_name="联系人广告",
        last_name="卡片内容",
        vcard="BEGIN:VCARD sensitive data END:VCARD",
        user_id=ADMIN,
    )


def event(guard, media=None, *, edited=False, forwarded=False):
    guard.client.admins.add(guard.bot_id)
    message = SimpleNamespace(
        id=91,
        chat_id=CHAT,
        sender_id=USER,
        message="",
        entities=[],
        media=contact() if media is None else media,
        edit_date="edited" if edited else None,
        fwd_from=object() if forwarded else None,
    )
    guard.client.source_messages[CHAT, message.id] = message
    return SimpleNamespace(
        id=message.id, chat_id=CHAT, sender_id=USER, is_group=True, message=message
    )


async def send(guard, value):
    try:
        await guard.handle_message(value)
    except events.StopPropagation:
        pass


async def test_contact_filter_is_opt_in_and_default_action_is_ban(guard, store):
    assert not guard.group(CHAT).block_contact_messages
    assert GuardGroup().contact_message_action == "ban"
    await send(guard, event(guard))
    assert not store.cases and not guard.client.calls


@pytest.mark.parametrize(
    "edited,forwarded,is_bot", [(False, False, False), (True, False, False), (False, True, True)]
)
async def test_contact_ban_targets_sender_deletes_first_and_cleans_history(
    guard, store, edited, forwarded, is_bot
):
    group = guard.group(CHAT)
    group.block_contact_messages = True
    group.block_bot_messages = True
    group.block_forwarded_messages = True
    group.forwarded_message_action = group.bot_message_action = "delete"
    guard.client.users[USER].bot = is_bot
    value = event(guard, edited=edited, forwarded=forwarded)
    await send(guard, value)
    case = next(iter(store.cases.values()))
    assert case["action"] == "ban" and case["status"] == "pending"
    assert case["user_id"] == USER and case["payload"]["evidence"]["target"] == "contact_message"
    assert "联系人广告" in case["payload"]["evidence"]["original"]
    assert guard.client.calls[:2] == [("delete", CHAT, (91,)), ("ban", CHAT, USER)]
    assert not any(call[0] == "ban" and call[2] == ADMIN for call in guard.client.calls)
    assert case["payload"]["ban_message_cleanup"]["status"] == "deleted"
    guard.ban_history.revoke.assert_awaited_once_with(CHAT, USER, guard.bot_id)
    assert not store.black and not store.white
    serialized = json.dumps(case["payload"])
    assert contact().phone_number not in serialized and contact().vcard not in serialized
    await send(guard, value)
    assert len(store.cases) == 1
    assert sum(call[0] == "ban" for call in guard.client.calls) == 1


@pytest.mark.parametrize("action", ["delete", "warn", "mute", "kick"])
async def test_contact_actions_do_not_start_ban_history_cleanup(guard, store, action):
    guard.group(CHAT).block_contact_messages = True
    guard.group(CHAT).contact_message_action = action
    await send(guard, event(guard))
    case = next(iter(store.cases.values()))
    assert case["action"] == action and case["status"] == "done"
    guard.ban_history.revoke.assert_not_awaited()
    assert "ban_message_cleanup" not in case["payload"]
    assert not store.black and not store.white


@pytest.mark.parametrize("protection", ["admin", "whitelist", "trusted_bot", "self", "disabled"])
async def test_contact_filter_preserves_existing_exemptions(guard, store, protection):
    guard.group(CHAT).block_contact_messages = True
    if protection == "admin":
        guard.client.admins.add(USER)
    elif protection == "whitelist":
        store.white.add((CHAT, USER))
    elif protection == "trusted_bot":
        guard.client.users[USER].bot = True
        guard.group(CHAT).trusted_bot_ids = [USER]
    elif protection == "self":
        guard.bot_id = USER
    else:
        guard.group(CHAT).enabled = False
    await send(guard, event(guard))
    assert not guard.client.calls and not store.cases


@pytest.mark.parametrize(
    "media",
    [SimpleNamespace(first_name="联系人", user_id=ADMIN), MessageMediaDocument(document=None)],
)
async def test_contact_filter_does_not_match_text_or_vcard_documents(guard, store, media):
    guard.group(CHAT).block_contact_messages = True
    value = event(guard, media)
    value.message.message = "联系人：测试，联系电话 15550001111，contact.vcf"
    await send(guard, value)
    assert not guard.client.calls and not store.cases


@pytest.mark.parametrize("change", ["switch", "whitelist", "admin"])
async def test_retry_rechecks_contact_switch_and_permissions(guard, store, change):
    guard.group(CHAT).block_contact_messages = True
    guard.client.fail["ban"] = RuntimeError("unavailable")
    await send(guard, event(guard))
    case = next(iter(store.cases.values()))
    assert case["status"] == "execution_failed"
    guard.client.fail.clear()
    if change == "switch":
        guard.group(CHAT).block_contact_messages = False
    elif change == "whitelist":
        store.white.add((CHAT, USER))
    else:
        guard.client.admins.add(USER)
    await store.update_case(case["id"], "executing")
    await guard.execute_case(store, await store.case(case["id"]))
    assert (await store.case(case["id"]))["status"] == "cancelled"
    assert sum(call[0] == "ban" for call in guard.client.calls) == 1


def test_contact_evidence_survives_ai_snapshot_without_exporting_phone_or_vcard():
    original = SimpleNamespace(media=contact(), message="", entities=[])
    snapshot = message_snapshot(original)
    assert snapshot["contact"] == {"first_name": "联系人广告", "last_name": "卡片内容"}
    assert contact().phone_number not in json.dumps(snapshot) and contact().vcard not in json.dumps(
        snapshot
    )
    restored = match_message(snapshot)
    match = Matcher(GuardGroup(block_contact_messages=True)).message(restored)
    assert match.target == "contact_message" and match.action == "ban"
    assert restored.media.user_id == 0
    assert "contact" not in message_snapshot(SimpleNamespace(message="普通消息"))
