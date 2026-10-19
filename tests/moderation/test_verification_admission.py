# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
"""放行授权必须来自本人的正确答案或已校验权限的管理员跳过。"""

from datetime import timedelta
from types import SimpleNamespace

import pytest

from core.infrastructure.database.mysql import utc_now_naive
from core.moderation.moderator import GuardError, Moderator
from core.moderation.verification import challenge_buttons, verified_answer

from .conftest import ADMIN, CHAT, USER
from .test_verification import begin, callback
from .test_verification_admin import admin_button


async def pending_answer(guard, store):
    session = await begin(guard, store)
    guard.client.fail["restrict"] = RuntimeError("injected restore failure")
    await guard.verification.callback(callback(session))
    guard.client.fail.clear()
    saved = await store.verification(CHAT, USER)
    assert saved["status"] == "passing" and verified_answer(saved)
    return saved


async def test_wrong_cached_index_cannot_make_a_wrong_value_pass(guard, store):
    session = await begin(guard, store)
    session["payload"]["correct"] = (session["payload"]["correct"] + 1) % 4
    await store.save_verification(session)
    await guard.verification.callback(callback(session))
    saved = await store.verification(CHAT, USER)
    assert saved["status"] == "waiting" and saved["attempts"] == 1
    assert not saved["payload"].get("answer_receipt")
    assert guard.client.member_rights[(CHAT, USER)]["send_messages"]


async def test_actual_correct_value_passes_even_if_cached_index_is_wrong(guard, store):
    session = await begin(guard, store)
    event = callback(session)
    session["payload"]["correct"] = (session["payload"]["correct"] + 1) % 4
    await store.save_verification(session)
    await guard.verification.callback(event)
    saved = await store.verification(CHAT, USER)
    assert saved["status"] == "passed" and verified_answer(saved)


@pytest.mark.parametrize("state", ["passing", "cancelling"])
async def test_unproven_recovery_state_never_releases_member(guard, store, state):
    session = await begin(guard, store)
    session["status"] = state
    session["payload"].update(answer_actor_id=USER, answer_accepted_at=utc_now_naive().isoformat())
    await store.save_verification(session)
    before = list(guard.client.calls)
    await guard.recover_once()
    saved = await store.verification(CHAT, USER)
    assert saved["status"] == "waiting" and saved["expires_at"] == session["expires_at"]
    assert saved["attempts"] == session["attempts"]
    assert guard.client.calls == before
    assert guard.client.member_rights[(CHAT, USER)]["send_messages"]


@pytest.mark.parametrize(
    ("field", "value"),
    [
        ("actor_id", USER + 1),
        ("chat_id", CHAT - 1),
        ("message_id", 999999),
        ("session_id", "0" * 32),
        ("token", "0" * 24),
        ("context", "wrong"),
        ("value", -1),
        ("query_id", "999999999"),
        ("accepted_at", "invalid"),
        ("accepted_at", "2099-01-01T00:00:00"),
    ],
)
async def test_recovery_rechecks_every_answer_binding(guard, store, field, value):
    session = await pending_answer(guard, store)
    session["payload"]["answer_receipt"][field] = value
    await store.save_verification(session)
    before = list(guard.client.calls)
    restarted = Moderator(
        guard.client, guard.repository, guard.config, default_admins=lambda: [ADMIN]
    )
    await restarted.recover_once()
    assert (await store.verification(CHAT, USER))["status"] == "waiting"
    assert guard.client.calls == before
    assert guard.client.member_rights[(CHAT, USER)]["send_messages"]


async def test_valid_answer_receipt_survives_restart(guard, store):
    session = await pending_answer(guard, store)
    proof = session["payload"]["answer_receipt"]
    assert proof["actor_id"] == USER
    assert proof["value"] == sum(map(int, session["payload"]["question"].split(" + ")))
    restarted = Moderator(
        guard.client, guard.repository, guard.config, default_admins=lambda: [ADMIN]
    )
    await restarted.recover_once()
    assert (await store.verification(CHAT, USER))["status"] == "passed"
    assert not guard.client.member_rights[(CHAT, USER)]["send_messages"]


async def test_correct_receipt_is_committed_before_permission_restore(guard, store, monkeypatch):
    session = await begin(guard, store)
    restore = guard.actions.set_rights

    async def checked_restore(chat_id, user_id, rights):
        persisted = await store.verification(chat_id, user_id)
        assert persisted["status"] == "passing" and verified_answer(persisted)
        await restore(chat_id, user_id, rights)

    monkeypatch.setattr(guard.actions, "set_rights", checked_restore)
    await guard.verification.callback(callback(session))
    assert (await store.verification(CHAT, USER))["status"] == "passed"


@pytest.mark.parametrize("query_id", [None, 0, -1, "123", True])
async def test_missing_or_invalid_telegram_query_cannot_authorize_release(guard, store, query_id):
    session = await begin(guard, store)
    event = callback(session)
    event.query = SimpleNamespace(query_id=query_id)
    with pytest.raises(GuardError, match="invalid_callback"):
        await guard.verification.callback(event)
    assert (await store.verification(CHAT, USER))["status"] == "waiting"
    assert guard.client.member_rights[(CHAT, USER)]["send_messages"]


async def test_legacy_numeric_callback_refreshes_without_authorizing_release(guard, store):
    session = await begin(guard, store)
    event = callback(session)
    event.data = f"sg:v:{session['id']}:{session['payload']['correct']}".encode()
    await guard.verification.callback(event)
    assert (await store.verification(CHAT, USER))["status"] == "waiting"
    assert "重新作答" in event.answer.await_args.args[0]
    assert guard.client.member_rights[(CHAT, USER)]["send_messages"]


async def test_other_member_cannot_answer_with_the_correct_issued_token(guard, store):
    session = await begin(guard, store)
    with pytest.raises(GuardError, match="invalid_callback"):
        await guard.verification.callback(callback(session, user_id=ADMIN))
    saved = await store.verification(CHAT, USER)
    assert saved["status"] == "waiting" and not saved["payload"].get("answer_events")


async def test_old_token_is_invalid_even_when_rebound_to_a_new_message_id(guard, store):
    session = await begin(guard, store)
    old = callback(session)
    await guard.client.delete_messages(CHAT, [session["message_id"]])
    await guard.recover_once()
    new = await store.verification(CHAT, USER)
    old.message_id = new["message_id"]
    with pytest.raises(GuardError, match="invalid_callback"):
        await guard.verification.callback(old)
    assert (await store.verification(CHAT, USER))["status"] == "waiting"
    assert all(len(button.type.data) <= 64 for row in challenge_buttons(new) for button in row)


@pytest.mark.parametrize("action", ["allow", "warn", "mute"])
async def test_timeout_cannot_automatically_release_and_admin_can_explicitly_skip(
    guard, store, action
):
    guard.config.groups[str(CHAT)].verification_timeout_action = action
    session = await begin(guard, store)
    session["expires_at"] = utc_now_naive() - timedelta(seconds=1)
    await store.save_verification(session)
    await guard.recover_once()
    held = await store.verification(CHAT, USER)
    assert held["status"] == "held"
    assert guard.client.member_rights[(CHAT, USER)]["send_messages"]
    assert not guard.client.member_rights[(CHAT, USER)]["until_date"]
    assert "保持禁言" in guard.client.sent[(CHAT, held["message_id"])]["text"]
    before = list(guard.client.calls)
    await guard.recover_once()
    assert guard.client.calls == before
    await guard.verification.callback(admin_button(held, "s"))
    assert (await store.verification(CHAT, USER))["status"] == "admin_skipped"
    assert not guard.client.member_rights[(CHAT, USER)]["send_messages"]


@pytest.mark.parametrize("change", ["disabled", "verify_off", "whitelist", "promoted"])
async def test_configuration_and_exemption_changes_cannot_release_started_verification(
    guard, store, change
):
    session = await begin(guard, store)
    config = guard.config.groups[str(CHAT)]
    if change == "disabled":
        config.enabled = False
    elif change == "verify_off":
        config.verify_join = False
    elif change == "whitelist":
        store.white.add((CHAT, USER))
    else:
        guard.client.admins.add(USER)
    await guard.recover_once()
    assert (await store.verification(CHAT, USER))["status"] == "waiting"
    assert guard.client.member_rights[(CHAT, USER)]["send_messages"]
    session["expires_at"] = utc_now_naive() - timedelta(seconds=1)
    await store.save_verification(session)
    await guard.recover_once()
    held = await store.verification(CHAT, USER)
    assert held["status"] == "held"
    assert guard.client.member_rights[(CHAT, USER)]["send_messages"]
    await guard.verification.callback(admin_button(held, "s"))
    assert (await store.verification(CHAT, USER))["status"] == "admin_skipped"


async def test_direct_restore_and_unproven_admin_intent_are_rejected(guard, store):
    session = await begin(guard, store)
    with pytest.raises(GuardError, match="verification_release_required"):
        await guard.verification.restore(session)
    session["status"] = "admin_pending"
    session["payload"]["admin_action"] = {"action": "skip", "actor_id": ADMIN}
    with pytest.raises(GuardError, match="verification_release_required"):
        await guard.verification.restore(session)
    assert guard.client.member_rights[(CHAT, USER)]["send_messages"]


async def test_changed_question_invalidates_an_existing_correct_receipt(guard, store):
    session = await pending_answer(guard, store)
    session["payload"]["question"] = "1 + 1"
    await store.save_verification(session)
    await guard.recover_once()
    saved = await store.verification(CHAT, USER)
    assert saved["status"] == "waiting"
    assert guard.client.member_rights[(CHAT, USER)]["send_messages"]


async def test_answer_commit_failure_cannot_release_member(guard, store, monkeypatch):
    session = await begin(guard, store)
    save = store.save_verification

    async def reject_proof(value):
        if value["status"] == "passing":
            raise ConnectionError("injected answer receipt commit failure")
        await save(value)

    monkeypatch.setattr(store, "save_verification", reject_proof)
    before = list(guard.client.calls)
    with pytest.raises(ConnectionError):
        await guard.verification.callback(callback(session))
    assert (await store.verification(CHAT, USER))["status"] == "waiting"
    assert guard.client.calls == before


async def test_revoked_admin_cannot_finish_skip_until_an_admin_clicks_again(guard, store):
    session = await begin(guard, store)
    actor = USER + 9
    guard.client.admins.add(actor)
    guard.client.fail["restrict"] = RuntimeError("injected restore failure")
    await guard.verification.callback(admin_button(session, "s", actor=actor))
    guard.client.fail.clear()
    guard.client.admins.remove(actor)
    await guard.recover_once()
    saved = await store.verification(CHAT, USER)
    assert saved["status"] == "admin_pending" and saved["error"] == "error.permission_denied"
    assert guard.client.member_rights[(CHAT, USER)]["send_messages"]
    await guard.verification.callback(admin_button(saved, "s"))
    assert (await store.verification(CHAT, USER))["status"] == "admin_skipped"
    assert not guard.client.member_rights[(CHAT, USER)]["send_messages"]


async def test_unproven_legacy_skip_requires_a_new_authorized_click(guard, store):
    session = await begin(guard, store)
    guard.client.fail["restrict"] = RuntimeError("injected restore failure")
    await guard.verification.callback(admin_button(session, "s"))
    guard.client.fail.clear()
    saved = await store.verification(CHAT, USER)
    saved["payload"]["admin_action"].pop("authorized_at")
    await store.save_verification(saved)
    case_id = saved["payload"]["admin_action"]["case_id"]
    store.cases[case_id]["payload"]["verification_admin"].pop("authorized_at")
    await guard.recover_once()
    assert (await store.verification(CHAT, USER))["status"] == "admin_pending"
    assert guard.client.member_rights[(CHAT, USER)]["send_messages"]
    await guard.verification.callback(admin_button(saved, "s"))
    assert (await store.verification(CHAT, USER))["status"] == "admin_skipped"
    assert store.cases[case_id]["status"] == "cancelled"
