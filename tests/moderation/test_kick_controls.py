# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
"""各管理消息的仅踢出操作及临时封禁补偿恢复。"""

from copy import deepcopy
from datetime import timedelta
from unittest.mock import AsyncMock

import pytest

from core.infrastructure.database.mysql import utc_now_naive
from core.moderation.moderator import GuardError, Moderator
from core.moderation.notification import review_buttons

from .conftest import ADMIN, CHAT, USER
from .test_notification_controls import lifecycle_case, notice_event, punish
from .test_reports import report_case, send
from .test_verification import begin, callback
from .test_verification_admin import admin_button


async def make_case(guard, store, kind):
    if kind == "report":
        await send(guard)
        return report_case(store)
    return await punish(guard, store, kind)


@pytest.mark.parametrize("kind", ["report", "ban", "warn", "mute"])
async def test_each_moderation_notification_can_kick_only_and_replay_is_safe(guard, store, kind):
    case = await make_case(guard, store, kind)
    assert "仅踢出" in [row[0].text for row in review_buttons(case)]
    event = await notice_event(store, case, "j")
    await guard.reviews.callback(event)
    saved = await store.case(case["id"])
    assert saved["status"] == "kicked" and saved["payload"]["kick_unbanned"]
    assert not guard.client.member_rights[CHAT, USER]["view_messages"]
    assert not store.white and not store.black
    assert [call[0] for call in guard.client.calls if call[0] in {"ban", "unban"}] == [
        "ban",
        "unban",
    ]
    assert "仅踢出" in event.answer.call_args.args[0]
    assert saved["reviewer_actor"] == f"telegram:{ADMIN}"
    before = list(guard.client.calls)
    await guard.reviews.callback(event)
    assert guard.client.calls == before
    assert "仅踢出" not in [row[0].text for row in review_buttons(saved) or []]


@pytest.mark.parametrize("kind", ["report", "ban", "warn", "mute"])
async def test_only_authorized_reviewers_can_kick_from_the_bound_notice(guard, store, kind):
    case = await make_case(guard, store, kind)
    before = list(guard.client.calls)
    with pytest.raises(GuardError, match="permission_denied"):
        await guard.reviews.callback(await notice_event(store, case, "j", actor=USER))
    event = await notice_event(store, case, "j")
    event.message_id += 100
    with pytest.raises(GuardError, match="invalid_callback"):
        await guard.reviews.callback(event)
    assert guard.client.calls == before


async def test_disabled_kick_is_hidden_and_forged_click_is_rejected(guard, store):
    guard.group(CHAT).kick_button_enabled = False
    case = await punish(guard, store, "warn")
    assert "仅踢出" not in [row[0].text for row in review_buttons(case, guard.group(CHAT))]
    with pytest.raises(GuardError, match="kick_disabled"):
        await guard.reviews.review(case["id"], "kick", ADMIN)
    session = await begin(guard, store)
    with pytest.raises(GuardError, match="action_disabled"):
        await guard.verification.callback(admin_button(session, "j"))
    assert not any(call[0] in {"ban", "unban"} for call in guard.client.calls)


@pytest.mark.parametrize("surface", ["challenge", "start", "passed", "held"])
async def test_verification_messages_offer_kick_only_with_legacy_action_lists(
    guard, store, surface
):
    assert "kick" not in guard.group(CHAT).verification_admin_actions
    session = await begin(guard, store)
    if surface == "passed":
        await guard.verification.callback(callback(session))
        saved = await store.verification(CHAT, USER)
        saved["expires_at"] = utc_now_naive() - timedelta(days=1)
        await store.save_verification(saved)
    elif surface == "held":
        session = await store.verification(CHAT, USER)
        session["status"] = "held"
        await store.save_verification(session)
        await guard.verification.refresh_challenge(store, session)
    if surface == "challenge":
        message = guard.client.sent[CHAT, session["message_id"]]
        assert "仅踢出" in [button.text for row in message["buttons"] for button in row]
        event = admin_button(session, "j")
        await guard.verification.callback(event)
    else:
        case = lifecycle_case(store, "verify_pass" if surface == "passed" else "verify_start")
        assert "仅踢出" in [row[0].text for row in review_buttons(case, guard.group(CHAT))]
        event = await notice_event(store, case, "j", prefix="n")
        await guard.verification.notification_callback(event)
    assert (await store.verification(CHAT, USER))["status"] == "admin_kicked"
    assert not guard.client.member_rights[CHAT, USER]["view_messages"]
    assert not store.white and not store.black
    before = list(guard.client.calls)
    if surface == "challenge":
        await guard.verification.callback(event)
    elif surface in {"start", "held"}:
        with pytest.raises(GuardError, match="invalid_callback"):
            await guard.verification.notification_callback(event)
    else:
        await guard.verification.notification_callback(event)
    assert guard.client.calls == before


async def test_kick_remains_available_when_other_verification_controls_are_hidden(guard, store):
    guard.group(CHAT).verification_admin_actions = []
    session = await begin(guard, store)
    controls = guard.client.sent[CHAT, session["message_id"]]["buttons"][1:]
    assert [button.text for row in controls for button in row] == ["仅踢出"]


@pytest.mark.parametrize("conflict", ["blacklist", "other_ban", "manual_ban", "protected"])
async def test_kick_never_clears_unrelated_bans_or_kicks_administrators(guard, store, conflict):
    case = await make_case(guard, store, "report")
    if conflict == "blacklist":
        store.black[USER] = {"user_id": USER}
    elif conflict == "other_ban":
        other = {**case, "id": "f" * 32, "event_key": "f" * 64, "action": "ban"}
        await store.create_case(other)
    elif conflict == "manual_ban":
        guard.client.member_rights[CHAT, USER]["view_messages"] = True
        guard.client.ban_actors[CHAT, USER] = ADMIN
    else:
        guard.client.admins.add(USER)
    with pytest.raises(GuardError):
        await guard.reviews.review(case["id"], "kick", ADMIN)
    assert not guard.client.calls


async def test_kick_checks_other_bans_even_when_the_current_case_is_a_ban(guard, store):
    case = await punish(guard, store, "ban")
    other = deepcopy(case)
    other.update(id="e" * 32, event_key="e" * 64)
    await store.create_case(other)
    with pytest.raises(GuardError, match="other_ban_conflict"):
        await guard.reviews.review(case["id"], "kick", ADMIN)
    assert [call[0] for call in guard.client.calls] == ["ban"]


async def test_unban_failure_resumes_only_compensation_after_restart_and_disable(guard, store):
    case = await make_case(guard, store, "report")
    guard.client.fail["unban"] = ConnectionError("unavailable")
    with pytest.raises(GuardError, match="operation_failed"):
        await guard.reviews.review(case["id"], "kick", ADMIN)
    assert guard.client.member_rights[CHAT, USER]["view_messages"]
    guard.group(CHAT).kick_button_enabled = False
    guard.group(CHAT).enabled = False
    guard.client.fail.clear()
    restarted = Moderator(
        guard.client, guard.repository, guard.config, default_admins=guard.default_admins
    )
    restarted.bot_id = guard.bot_id
    await restarted.recover_once()
    assert (await store.case(case["id"]))["status"] == "kicked"
    assert sum(call[0] == "ban" for call in guard.client.calls) == 1
    assert not guard.client.member_rights[CHAT, USER]["view_messages"]


async def test_a_new_manual_ban_is_not_removed_by_kick_recovery(guard, store):
    case = await make_case(guard, store, "report")
    guard.client.fail["unban"] = ConnectionError("unavailable")
    with pytest.raises(GuardError):
        await guard.reviews.review(case["id"], "kick", ADMIN)
    guard.client.fail.clear()
    guard.client.ban_actors[CHAT, USER] = ADMIN
    before = list(guard.client.calls)
    await guard.recover_once()
    assert guard.client.calls == before
    assert (await store.case(case["id"]))["error"] == "guard.permissions_changed"


@pytest.mark.parametrize("phase", ["kick_banned", "kick_unbanned"])
async def test_rpc_success_followed_by_checkpoint_failure_is_idempotent(
    guard, store, monkeypatch, phase
):
    case = await make_case(guard, store, "report")
    update = store.update_case
    failures = 1

    async def fail_checkpoint(*args, **kwargs):
        nonlocal failures
        if failures and kwargs.get("payload", {}).get(phase):
            failures -= 1
            raise ConnectionError("commit failed after Telegram success")
        return await update(*args, **kwargs)

    monkeypatch.setattr(store, "update_case", fail_checkpoint)
    with pytest.raises(GuardError):
        await guard.reviews.review(case["id"], "kick", ADMIN)
    await guard.recover_once()
    assert (await store.case(case["id"]))["status"] == "kicked"
    assert [call[0] for call in guard.client.calls] == ["ban", "unban"]


async def test_final_commit_retry_does_not_affect_a_new_join_verification(guard, store):
    case = await make_case(guard, store, "report")
    store.fail_finish = 1
    with pytest.raises(GuardError):
        await guard.reviews.review(case["id"], "kick", ADMIN)
    new_session = await begin(guard, store)
    before = list(guard.client.calls)
    await guard.recover_once()
    assert (await store.case(case["id"]))["status"] == "kicked"
    assert (await store.verification(CHAT, USER))["id"] == new_session["id"]
    assert (await store.verification(CHAT, USER))["status"] == "waiting"
    assert guard.client.calls == before


async def test_report_kick_ends_the_current_verification_without_later_timeout_ban(guard, store):
    session = await begin(guard, store)
    case = await make_case(guard, store, "report")
    await guard.reviews.review(case["id"], "kick", ADMIN)
    saved = await store.verification(CHAT, USER)
    assert saved["status"] == "admin_kicked"
    before = list(guard.client.calls)
    await guard.verification.callback(callback(session))
    await guard.recover_once()
    assert guard.client.calls == before
    assert not guard.client.member_rights[CHAT, USER]["view_messages"]


async def test_basic_group_kick_uses_native_removal_without_a_persistent_ban(guard, store):
    await send(guard, chat_id=-12345)
    case = report_case(store)
    result = await guard.reviews.review(case["id"], "kick", ADMIN)
    assert result["status"] == "kicked"
    assert guard.client.calls == [("kick", -12345, USER)]
    assert not store.white and not store.black


async def test_existing_notices_gain_kick_without_replacement_and_setting_changes_apply(
    guard, store
):
    case = await make_case(guard, store, "report")
    before = await store.notices(case["id"])
    store.cases[case["id"]]["payload"].pop("controls_version")
    await guard.recover_once()
    assert (await store.case(case["id"]))["payload"]["controls_version"] == 2
    guard.group(CHAT).kick_button_enabled = False
    await guard.recover_once()
    assert all(
        "仅踢出" not in [row[0].text for row in msg["buttons"]]
        for msg in guard.client.sent.values()
    )
    assert {(row["target_id"], row["message_id"]) for row in before} == {
        (row["target_id"], row["message_id"]) for row in await store.notices(case["id"])
    }


async def test_database_failure_before_claim_has_no_kick_effect(guard, store, monkeypatch):
    case = await make_case(guard, store, "report")
    monkeypatch.setattr(store, "claim_review", AsyncMock(side_effect=ConnectionError("offline")))
    with pytest.raises(ConnectionError):
        await guard.reviews.review(case["id"], "kick", ADMIN)
    assert not guard.client.calls


async def test_reviewed_report_can_still_kick_without_changing_lists(guard, store):
    case = await make_case(guard, store, "report")
    await guard.reviews.review(case["id"], "unban", ADMIN)
    result = await guard.reviews.review(case["id"], "kick", ADMIN)
    assert result["status"] == "kicked" and not store.white and not store.black
    assert [item["action"] for item in result["payload"]["review_history"]] == ["unban", "kick"]


async def test_verification_unban_compensation_does_not_require_a_user_profile(
    guard, store, monkeypatch
):
    session = await begin(guard, store)
    guard.client.fail["unban"] = ConnectionError("offline")
    await guard.verification.callback(admin_button(session, "j"))
    guard.client.fail.clear()
    monkeypatch.setattr(
        guard.client, "get_entity", AsyncMock(side_effect=ValueError("profile gone"))
    )
    await guard.recover_once()
    assert (await store.verification(CHAT, USER))["status"] == "admin_kicked"
    assert not guard.client.member_rights[CHAT, USER]["view_messages"]


async def test_losing_bot_ban_rights_never_claims_temporary_ban_was_removed(
    guard, store, monkeypatch
):
    from types import SimpleNamespace

    case = await make_case(guard, store, "report")
    guard.client.fail["unban"] = ConnectionError("offline")
    with pytest.raises(GuardError):
        await guard.reviews.review(case["id"], "kick", ADMIN)
    guard.client.fail.clear()
    permissions = guard.client.get_permissions

    async def limited(chat_id, user_id):
        if user_id == guard.bot_id:
            return SimpleNamespace(
                is_admin=False, is_creator=False, participant=SimpleNamespace(admin_rights=None)
            )
        return await permissions(chat_id, user_id)

    monkeypatch.setattr(guard.client, "get_permissions", limited)
    await guard.recover_once()
    assert (await store.case(case["id"]))["status"] == "reviewing_kick"
    assert (await store.case(case["id"]))["error"] == "guard.permissions_required"
    assert guard.client.member_rights[CHAT, USER]["view_messages"]
