# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
"""人工拉黑访客来源、重试与换机器人/群的行为回归。"""

import asyncio
from copy import deepcopy

import pytest
from telethon.tl.types import PeerUser

from core.moderation.appeals import eligible
from core.moderation.guest_messages import blacklist_task
from core.moderation.notification import notification_text

from .conftest import ADMIN, CHAT, USER
from .test_guest_messages import CALLER, event, send


async def source_case(guard, store):
    await send(guard, event(guard))
    return next(row for row in store.cases.values() if row["user_id"] == USER)


async def blacklist(guard, case):
    return await guard.reviews.review(case["id"], "blacklist", ADMIN)


async def retry(guard, store, case_id):
    parent = await store.case(case_id)
    parent["payload"]["guest_ban"].pop("retry_at", None)
    await store.update_case(case_id, parent["status"], payload=parent["payload"])
    await guard.guest_messages.resume(case_id)
    return await store.case(case_id)


async def test_blacklist_links_caller_with_independent_authorized_audit(guard, store):
    parent = await blacklist(guard, await source_case(guard, store))
    assert USER in store.black and CALLER not in store.black
    assert "全局拉黑待处理" in notification_text(parent)
    await guard.guest_messages.resume(parent["id"])
    parent = await store.case(parent["id"])
    assert parent["payload"]["guest_ban"]["status"] == "blacklisted"
    audit = await store.case(parent["payload"]["guest_ban"]["case_id"])
    assert (
        audit["status"] == "done" and audit["action"] == "guest_blacklist" and CALLER in store.black
    )
    assert not eligible(audit)
    ban_case = await store.case(audit["payload"]["guest_ban_case"])
    assert eligible(ban_case) and (await store.active_ban(CHAT, CALLER))["id"] == ban_case["id"]
    assert audit["reviewer_actor"] == "system:guest_message"
    assert audit["payload"]["guest_blacklist_authorization"]["actor"] == f"telegram:{ADMIN}"
    assert audit["payload"]["guest_source_case"] == parent["id"]
    assert audit["payload"]["guest_source"]["user_id"] == CALLER
    assert "已联动封禁并加入全局黑名单" in notification_text(parent)
    assert not store.white


async def test_later_blacklist_upgrades_completed_local_ban(guard, store):
    parent = await source_case(guard, store)
    await guard.guest_messages.resume(parent["id"])
    parent = await store.case(parent["id"])
    child_id = parent["payload"]["guest_ban"]["case_id"]
    assert not store.black
    await blacklist(guard, parent)
    await guard.guest_messages.resume(parent["id"])
    assert CALLER in store.black
    assert (await store.case(child_id))["reviewer_actor"] == "system:guest_message"
    assert sum(c[0] == "ban" and c[2] == CALLER for c in guard.client.calls) == 1


async def test_new_explicit_blacklist_rechecks_an_external_unban(guard, store):
    parent = await source_case(guard, store)
    await guard.guest_messages.resume(parent["id"])
    parent = await store.case(parent["id"])
    old_id = parent["payload"]["guest_ban"]["case_id"]
    await guard.actions.unban(CHAT, CALLER)  # Telegram 人工解封，旧数据库案件尚未变化。
    await blacklist(guard, parent)
    await guard.guest_messages.resume(parent["id"])
    assert CALLER in store.black and (await store.case(old_id))["status"] == "cancelled"
    assert sum(c[0] == "ban" and c[2] == CALLER for c in guard.client.calls) == 2


async def test_old_blacklisted_source_can_explicitly_complete_the_new_link_once(guard, store):
    parent = await source_case(guard, store)
    await guard.guest_messages.resume(parent["id"])
    local = deepcopy((await store.case(parent["id"]))["payload"]["guest_ban"])
    parent = await blacklist(guard, parent)
    parent["payload"]["guest_ban"] = local
    parent["payload"]["guest_ban_pending"] = False
    await store.update_case(parent["id"], "blacklisted", payload=parent["payload"])
    await blacklist(guard, parent)
    await asyncio.gather(*(guard.guest_messages.resume(parent["id"]) for _ in range(2)))
    count = len(store.cases)
    await blacklist(guard, await store.case(parent["id"]))
    await guard.guest_messages.resume(parent["id"])
    assert len(store.cases) == count and CALLER in store.black


@pytest.mark.parametrize("change", ["revoked", "admin", "whitelist", "disabled", "source_removed"])
async def test_link_rechecks_live_authorization_and_exemptions(guard, store, change):
    parent = await blacklist(guard, await source_case(guard, store))
    if change == "revoked":
        guard.group(CHAT).reviewer_ids = [12345]
        guard.group(CHAT).allow_group_admin_review = False
    elif change == "admin":
        guard.client.admins.add(CALLER)
    elif change == "whitelist":
        store.white.add((CHAT, CALLER))
    elif change == "disabled":
        guard.group(CHAT).enabled = False
    else:
        del store.black[USER]
    await guard.guest_messages.resume(parent["id"])
    assert CALLER not in store.black
    assert not any(c[0] == "ban" and c[2] == CALLER for c in guard.client.calls)
    assert (await store.case(parent["id"]))["payload"]["guest_ban"]["status"] == "skipped"


async def test_blacklist_commit_failure_remains_pending_and_recovers(guard, store):
    parent = await blacklist(guard, await source_case(guard, store))
    store.fail_finish = 1
    await guard.guest_messages.resume(parent["id"])
    parent = await store.case(parent["id"])
    assert parent["payload"]["guest_ban_pending"] and CALLER not in store.black
    assert parent["payload"]["guest_ban"]["error"] == "ConnectionError"
    parent = await retry(guard, store, parent["id"])
    assert parent["payload"]["guest_ban"]["status"] == "blacklisted" and CALLER in store.black


async def test_explicit_retry_after_exemption_change_records_a_new_intent(guard, store):
    parent = await blacklist(guard, await source_case(guard, store))
    original = parent["payload"]["guest_ban"]["operation_id"]
    store.white.add((CHAT, CALLER))
    await guard.guest_messages.resume(parent["id"])
    store.white.remove((CHAT, CALLER))
    await blacklist(guard, await store.case(parent["id"]))
    parent = await store.case(parent["id"])
    assert parent["payload"]["guest_ban"]["operation_id"] != original
    assert parent["payload"]["guest_ban_history"][0]["status"] == "skipped"
    await guard.guest_messages.resume(parent["id"])
    assert CALLER in store.black


async def test_completed_audit_does_not_revive_a_removed_global_decision(guard, store):
    parent = await blacklist(guard, await source_case(guard, store))
    await guard.guest_messages.resume(parent["id"])
    parent = await store.case(parent["id"])
    parent["payload"]["guest_ban_pending"] = True  # 模拟提交已成功、父卡片回写前断线。
    await store.update_case(parent["id"], parent["status"], payload=parent["payload"])
    del store.black[CALLER]
    calls = deepcopy(guard.client.calls)
    parent = await retry(guard, store, parent["id"])
    assert CALLER not in store.black and parent["payload"]["guest_ban"]["status"] == "skipped"
    assert guard.client.calls == calls


async def test_new_blacklist_intent_is_not_overwritten_by_an_older_ban_worker(guard, store):
    parent = await source_case(guard, store)
    punish = guard.punish

    async def upgrade(*args, **kwargs):
        await blacklist(guard, await store.case(parent["id"]))
        return await punish(*args, **kwargs)

    guard.punish = upgrade
    await guard.guest_messages.resume(parent["id"])
    guard.punish = punish
    parent = await store.case(parent["id"])
    assert parent["payload"]["guest_ban_pending"]
    assert parent["payload"]["guest_ban"]["action"] == "blacklist"
    await guard.guest_messages.resume(parent["id"])
    assert CALLER in store.black


async def test_global_caller_id_blocks_another_bot_in_another_shared_group(guard, store):
    parent = await blacklist(guard, await source_case(guard, store))
    await guard.guest_messages.resume(parent["id"])
    other_chat = CHAT - 1
    config = guard.group(CHAT).model_copy(deep=True)
    config.share_global_blacklist = True
    config.block_button_messages = False
    config.block_bot_messages = False
    guard.config.groups[str(other_chat)] = config
    e = event(guard, 75)
    e.message.chat_id = other_chat
    other = deepcopy(guard.client.users[USER])
    other.id = USER + 1
    guard.client.users[other.id] = other
    e.message.sender_id = other.id
    e.message.from_id = PeerUser(other.id)
    match = await guard.match(store, config, other_chat, other, e.message)
    assert match.rule_id == "global-guest-caller-id" and match.action == "ban"
    config.share_global_blacklist = False
    assert await guard.match(store, config, other_chat, other, e.message) is None
    config.share_global_blacklist = True
    e.message.guestchat_via_from = None
    e.message.via_bot_id = CALLER
    e.message.fwd_from = PeerUser(CALLER)
    assert await guard.match(store, config, other_chat, other, e.message) is None


def test_ai_actor_cannot_create_a_global_link_intent():
    payload = {"guest_actor": {"user_id": CALLER}}
    assert blacklist_task(payload, ADMIN, "system:ai_report") == payload
