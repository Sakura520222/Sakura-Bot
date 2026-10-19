# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
import asyncio
from copy import deepcopy
from datetime import timedelta
from types import SimpleNamespace
from unittest.mock import AsyncMock

import pytest

from core.infrastructure.database.mysql import utc_now_naive
from core.moderation.matcher import Match
from core.moderation.moderator import GuardError, Moderator
from core.moderation.notification import review_buttons

from .conftest import ADMIN, CHAT, USER
from .test_verification import begin, callback


async def punish(guard, store, action, token="notification-case"):
    return await guard.punish(
        store,
        CHAT,
        guard.client.users[USER],
        Match("message", "message-word-0", "test", "1", "1", action),
        token,
    )


async def notice_event(store, case, code, *, actor=ADMIN, prefix="r"):
    notice = next(row for row in await store.notices(case["id"]) if row["target_id"] == ADMIN)
    data = f"sg:{prefix}:{case['id']}:{code}"
    if prefix == "r":
        data += f":{case['payload'].get('review_generation', 0)}"
    return SimpleNamespace(
        sender_id=actor,
        chat_id=notice["target_id"],
        message_id=notice["message_id"],
        query=SimpleNamespace(query_id=9000),
        data=data.encode(),
        answer=AsyncMock(),
    )


def lifecycle_case(store, action):
    return next(case for case in store.cases.values() if case["action"] == action)


async def test_only_unban_preserves_lists_and_replay_has_no_side_effect(guard, store):
    store.white.add((CHAT, USER))
    case = await punish(guard, store, "ban")
    event = await notice_event(store, case, "u")
    await guard.reviews.callback(event)
    saved = await store.case(case["id"])
    assert saved["status"] == "unbanned" and await store.whitelisted(CHAT, USER)
    assert not store.black and not guard.client.member_rights[(CHAT, USER)]["view_messages"]
    calls = list(guard.client.calls)
    await guard.reviews.callback(event)
    assert guard.client.calls == calls and len(saved["payload"]["review_history"]) == 1


async def test_only_unban_does_not_add_whitelist(guard, store):
    case = await punish(guard, store, "ban")
    await guard.reviews.callback(await notice_event(store, case, "u"))
    assert (await store.case(case["id"]))["status"] == "unbanned"
    assert not await store.whitelisted(CHAT, USER) and not store.black


async def test_old_ban_notice_cannot_release_a_new_member_mute(guard, store):
    case = await punish(guard, store, "ban")
    await guard.actions.unban(CHAT, USER)
    guard.client.member_rights[(CHAT, USER)]["send_messages"] = True
    guard.client.member_rights[(CHAT, USER)]["invite_users"] = True
    current = deepcopy(guard.client.member_rights[(CHAT, USER)])
    before = list(guard.client.calls)
    await guard.reviews.callback(await notice_event(store, case, "u"))
    assert guard.client.calls == before
    assert guard.client.member_rights[(CHAT, USER)] == current


@pytest.mark.parametrize("action", ["unban", "allow"])
async def test_release_cannot_bypass_global_blacklist(guard, store, action):
    case = await punish(guard, store, "ban")
    store.black[USER] = {"user_id": USER}
    with pytest.raises(GuardError, match="blacklist_conflict"):
        await guard.reviews.review(case["id"], action, ADMIN)
    assert guard.client.member_rights[(CHAT, USER)]["view_messages"]


async def test_only_unban_commit_failure_recovers_without_repeating_rpc(guard, store):
    case = await punish(guard, store, "ban")
    store.fail_finish = 1
    with pytest.raises(GuardError):
        await guard.reviews.callback(await notice_event(store, case, "u"))
    assert (await store.case(case["id"]))["status"] == "reviewing_unban"
    restarted = Moderator(
        guard.client, guard.repository, guard.config, default_admins=guard.default_admins
    )
    await restarted.recover_once()
    assert (await store.case(case["id"]))["status"] == "unbanned"
    assert sum(item[0] == "unban" for item in guard.client.calls) == 1
    assert not await store.whitelisted(CHAT, USER)


@pytest.mark.parametrize("kind", ["mute", "warn"])
@pytest.mark.parametrize(
    ("code", "status"), [("u", None), ("a", "allowed"), ("k", "pending"), ("b", "blacklisted")]
)
async def test_mute_and_warning_buttons_have_distinct_effects(guard, store, kind, code, status):
    guard.client.member_rights[(CHAT, USER)]["send_polls"] = True
    original = deepcopy(guard.client.member_rights[(CHAT, USER)])
    case = await punish(guard, store, kind)
    assert len(review_buttons(case)) == 4
    await guard.reviews.callback(await notice_event(store, case, code))
    saved = await store.case(case["id"])
    assert saved["status"] == (status or ("unmuted" if kind == "mute" else "ignored"))
    assert bool(await store.whitelisted(CHAT, USER)) is (code == "a")
    assert bool(store.black) is (code == "b")
    if code in {"u", "a"}:
        assert guard.client.member_rights[(CHAT, USER)] == original
    else:
        assert guard.client.member_rights[(CHAT, USER)]["view_messages"]
    assert saved["reviewer_actor"] == f"telegram:{ADMIN}"


async def test_warning_escalation_invalidates_the_original_buttons(guard, store):
    case = await punish(guard, store, "warn")
    original_allow = await notice_event(store, case, "a")
    await guard.reviews.callback(await notice_event(store, case, "k"))
    saved = await store.case(case["id"])
    assert saved["status"] == "pending" and saved["payload"]["review_generation"] == 1
    assert len(review_buttons(saved)) == 3
    with pytest.raises(GuardError, match="invalid_callback"):
        await guard.reviews.callback(original_allow)
    assert guard.client.member_rights[(CHAT, USER)][
        "view_messages"
    ] and not await store.whitelisted(CHAT, USER)
    await guard.reviews.callback(await notice_event(store, saved, "u"))
    assert (await store.case(case["id"]))["status"] == "unbanned"


async def test_old_mute_cannot_remove_a_later_identical_indefinite_mute(guard, store):
    guard.client.member_rights[(CHAT, USER)]["send_polls"] = True
    first = await punish(guard, store, "mute", "mute-first")
    second = await punish(guard, store, "mute", "mute-second")
    assert guard.client.member_rights[(CHAT, USER)]["until_date"] == 0
    with pytest.raises(GuardError):
        await guard.reviews.callback(await notice_event(store, first, "u"))
    assert (await store.case(first["id"]))["error"] == "guard.permissions_changed"
    assert guard.client.member_rights[(CHAT, USER)]["send_messages"]
    await guard.reviews.callback(await notice_event(store, second, "u"))
    assert (await store.case(second["id"]))["status"] == "unmuted"
    assert guard.client.member_rights[(CHAT, USER)]["send_messages"]


async def test_unmute_preserves_pending_verification_and_updates_its_restore_baseline(guard, store):
    case = await punish(guard, store, "mute")
    await begin(guard, store)
    await guard.reviews.callback(await notice_event(store, case, "u"))
    saved = await store.verification(CHAT, USER)
    assert (
        saved["status"] == "waiting" and guard.client.member_rights[(CHAT, USER)]["send_messages"]
    )
    assert not saved["payload"]["original_rights"]["send_messages"]
    assert (await store.case(case["id"]))["payload"]["release_deferred_verification"]
    await guard.verification.callback(callback(saved))
    assert (await store.verification(CHAT, USER))["status"] == "passed"
    assert not guard.client.member_rights[(CHAT, USER)]["send_messages"]


@pytest.mark.parametrize("kind", ["ban", "mute", "warn"])
async def test_notification_review_rejects_non_admin_and_forged_origins(guard, store, kind):
    case = await punish(guard, store, kind)
    with pytest.raises(GuardError, match="permission_denied"):
        await guard.reviews.callback(await notice_event(store, case, "u", actor=USER))
    forged = await notice_event(store, case, "u")
    forged.message_id += 100000
    with pytest.raises(GuardError, match="invalid_callback"):
        await guard.reviews.callback(forged)


@pytest.mark.parametrize(
    ("code", "status"), [("b", "admin_banned"), ("x", "admin_blacklisted"), ("s", "admin_skipped")]
)
async def test_start_notification_can_manage_the_original_verification(guard, store, code, status):
    await begin(guard, store)
    case = lifecycle_case(store, "verify_start")
    assert len(review_buttons(case, guard.group(CHAT))) == 3
    await guard.verification.notification_callback(
        await notice_event(store, case, code, prefix="n")
    )
    assert (await store.verification(CHAT, USER))["status"] == status
    assert not await store.whitelisted(CHAT, USER)
    controls = review_buttons(await store.case(case["id"]), guard.group(CHAT))
    assert controls is None
    if code == "s":
        result = next(row for row in store.cases.values() if row["action"] == "verify_skip")
        assert len(review_buttons(result, guard.group(CHAT))) == 2


@pytest.mark.parametrize("code", ["b", "x"])
async def test_passed_notification_can_ban_after_original_verification_deadline(guard, store, code):
    session = await begin(guard, store)
    await guard.verification.callback(callback(session))
    saved = await store.verification(CHAT, USER)
    saved["expires_at"] = utc_now_naive() - timedelta(days=1)
    await store.save_verification(saved)
    case = lifecycle_case(store, "verify_pass")
    assert len(review_buttons(case, guard.group(CHAT))) == 2
    event = await notice_event(store, case, code, prefix="n")
    await guard.verification.notification_callback(event)
    assert (await store.verification(CHAT, USER))["status"] == (
        "admin_banned" if code == "b" else "admin_blacklisted"
    )
    calls = list(guard.client.calls)
    await guard.verification.notification_callback(event)
    assert guard.client.calls == calls


async def test_passing_replaces_start_notification_with_result_controls(guard, store):
    session = await begin(guard, store)
    start = lifecycle_case(store, "verify_start")
    old_skip = await notice_event(store, start, "s", prefix="n")
    await guard.verification.callback(callback(session))
    updated = await store.case(start["id"])
    assert updated["payload"]["verification_state"] == "passed"
    assert review_buttons(updated, guard.group(CHAT)) is None
    assert len(review_buttons(lifecycle_case(store, "verify_pass"), guard.group(CHAT))) == 2
    before = list(guard.client.calls)
    with pytest.raises(GuardError, match="invalid_callback"):
        await guard.verification.notification_callback(old_skip)
    assert guard.client.calls == before


async def test_old_notification_cannot_manage_a_new_join_session(guard, store):
    session = await begin(guard, store)
    case = lifecycle_case(store, "verify_start")
    event = await notice_event(store, case, "b", prefix="n")
    session["status"] = "left"
    await store.save_verification(session)
    await guard.verification.begin(store, CHAT, guard.client.users[USER], "new-join")
    with pytest.raises(GuardError, match="invalid_callback"):
        await guard.verification.notification_callback(event)


@pytest.mark.parametrize("change", ["actor", "origin", "disabled"])
async def test_verification_notification_permissions_and_binding_are_enforced(guard, store, change):
    await begin(guard, store)
    case = lifecycle_case(store, "verify_start")
    event = await notice_event(store, case, "s", prefix="n")
    expected = "invalid_callback"
    if change == "actor":
        event.sender_id = USER
        expected = "permission_denied"
    elif change == "origin":
        event.chat_id = ADMIN + 500
    else:
        guard.group(CHAT).verification_admin_actions = ["ban"]
        expected = "verification_action_disabled"
    with pytest.raises(GuardError, match=expected):
        await guard.verification.notification_callback(event)
    assert (await store.verification(CHAT, USER))["status"] == "waiting"


async def test_opposing_notification_operations_cannot_override_the_first_choice(guard, store):
    case = await punish(guard, store, "warn")
    ban = await notice_event(store, case, "k")
    allow = await notice_event(store, case, "a")
    results = await asyncio.gather(
        guard.reviews.callback(ban), guard.reviews.callback(allow), return_exceptions=True
    )
    saved = await store.case(case["id"])
    assert (
        saved["status"] == "pending" and guard.client.member_rights[(CHAT, USER)]["view_messages"]
    )
    assert any(isinstance(result, GuardError) for result in results)
    assert not await store.whitelisted(CHAT, USER)
