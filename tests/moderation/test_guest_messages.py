# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
"""真实 guestchat_via_from 绑定、销号访客、独立删除及调用者联动的行为测试。"""

from types import SimpleNamespace

import pytest
from telethon import events
from telethon.tl.types import PeerChannel, PeerUser

from core.moderation.guest_messages import guest_actor

from .conftest import CHAT, USER

CALLER = 400


def event(guard, mid=70):
    guard.client.admins.add(guard.bot_id)
    guard.client.users[USER].bot = True
    guard.client.users[CALLER] = SimpleNamespace(
        id=CALLER, first_name="Elena", last_name=None, username="caller", bot=False
    )
    guard.group(CHAT).block_button_messages = True
    guard.group(CHAT).button_message_action = "ban"
    message = SimpleNamespace(
        id=mid,
        chat_id=CHAT,
        sender_id=USER,
        from_id=PeerUser(USER),
        guestchat_via_from=PeerUser(CALLER),
        message="访客广告",
        entities=[],
        edit_date=None,
        reply_markup=SimpleNamespace(rows=[SimpleNamespace(buttons=[object()])]),
    )
    guard.client.source_messages[CHAT, mid] = message
    return SimpleNamespace(id=mid, chat_id=CHAT, sender_id=USER, is_group=True, message=message)


async def send(guard, e):
    with pytest.raises(events.StopPropagation):
        await guard.handle_message(e)


async def test_guest_ban_deletes_message_before_ban_and_bans_real_caller_once(guard, store):
    e = event(guard)
    await send(guard, e)
    parent = next(iter(store.cases.values()))
    assert guard.client.calls[:2] == [("delete", CHAT, (70,)), ("ban", CHAT, USER)]
    assert parent["payload"]["guest_actor"]["user_id"] == CALLER
    await guard.recover_once()
    parent = await store.case(parent["id"])
    assert parent["payload"]["guest_ban"]["status"] == "banned"
    child = await store.case(parent["payload"]["guest_ban"]["case_id"])
    assert child["user_id"] == CALLER and child["action"] == "ban" and child["status"] == "pending"
    assert child["payload"]["ban_message_cleanup"]["status"] == "deleted"
    assert not store.black and not store.white
    await send(guard, e)
    await guard.recover_once()
    assert sum(call[0] == "ban" and call[2] == CALLER for call in guard.client.calls) == 1


async def test_already_banned_guest_bot_does_not_swallow_a_new_callers_message(guard, store):
    e = event(guard)
    await send(guard, e)
    second = event(guard, 71)
    await send(guard, second)
    assert (CHAT, 70) in guard.client.deleted and (CHAT, 71) in guard.client.deleted
    assert len(store.cases) == 2
    await guard.recover_once()
    assert sum(call[0] == "ban" and call[2] == CALLER for call in guard.client.calls) == 1


async def test_deactivated_guest_bot_profile_lookup_does_not_leave_its_ad_visible(guard, store):
    e = event(guard)
    original = guard.client.get_entity

    async def lookup(identifier):
        if getattr(identifier, "user_id", identifier) == USER:
            raise KeyError(USER)
        return await original(identifier)

    guard.client.get_entity = lookup
    await send(guard, e)
    assert (CHAT, 70) in guard.client.deleted
    await guard.recover_once()
    parent = next(row for row in store.cases.values() if row["user_id"] == USER)
    assert parent["payload"]["guest_ban"]["status"] == "banned"


async def test_ban_rpc_failure_still_removes_the_matched_ad(guard, store):
    e = event(guard)
    guard.client.fail["ban"] = RuntimeError("unavailable")
    await send(guard, e)
    parent = next(iter(store.cases.values()))
    assert parent["status"] == "execution_failed"
    assert parent["payload"]["report_message_deletion"]["status"] == "deleted"
    assert (CHAT, 70) in guard.client.deleted


@pytest.mark.parametrize("protection", ["admin", "whitelist", "self"])
async def test_guest_callers_keep_admin_and_whitelist_protection(guard, store, protection):
    e = event(guard)
    if protection == "admin":
        guard.client.admins.add(CALLER)
    elif protection == "whitelist":
        store.white.add((CHAT, CALLER))
    else:
        guard.bot_id = CALLER
    await send(guard, e)
    await guard.recover_once()
    assert not any(call[0] == "ban" and call[2] == CALLER for call in guard.client.calls)


@pytest.mark.parametrize("peer", [None, PeerChannel(123), PeerUser(-1), PeerUser(USER)])
def test_only_telegram_guest_user_peer_is_a_valid_link(peer):
    assert (
        guest_actor(
            SimpleNamespace(
                sender_id=USER,
                guestchat_via_from=peer,
                via_bot_id=CALLER,
                fwd_from=PeerUser(CALLER),
            )
        )
        is None
    )
