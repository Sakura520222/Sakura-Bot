# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
from datetime import UTC, timedelta

import pytest

from core.infrastructure.database.mysql import utc_now_naive
from core.moderation.moderator import GuardError
from core.moderation.telegram import rights_dict, rights_object, same_rights, verification_rights

from .conftest import CHAT, USER
from .test_verification import begin, callback
from .test_verification_admin import admin_button


@pytest.mark.parametrize("state", ["passing", "cancelling"])
@pytest.mark.parametrize(
    ("button", "result"),
    [("b", "admin_banned"), ("x", "admin_blacklisted"), ("s", "admin_skipped")],
)
async def test_admin_can_finish_an_uncompleted_recovery_phase(guard, store, state, button, result):
    session = await begin(guard, store)
    session["status"] = state
    session["error"] = "guard.permissions_changed"
    await store.save_verification(session)
    event = admin_button(session, button)
    await guard.verification.callback(event)
    saved = await store.verification(CHAT, USER)
    assert saved["status"] == result
    assert "当前状态" not in event.answer.await_args.args[0]
    assert bool(store.black) is (button == "x")
    assert (CHAT, session["message_id"]) not in guard.client.sent
    assert not await store.whitelisted(CHAT, USER)


async def test_non_admin_still_cannot_take_over_permission_recovery(guard, store):
    session = await begin(guard, store)
    session["status"] = "passing"
    await store.save_verification(session)
    with pytest.raises(GuardError, match="permission_denied"):
        await guard.verification.callback(admin_button(session, "x", actor=USER + 100))
    assert (await store.verification(CHAT, USER))["status"] == "passing"
    assert not store.black


async def test_terminal_callback_localizes_state_without_repeating_action(guard, store):
    session = await begin(guard, store)
    await guard.verification.callback(callback(session))
    calls = list(guard.client.calls)
    event = admin_button(session, "b")
    await guard.verification.callback(event)
    assert "验证通过" in event.answer.await_args.args[0]
    assert "passed" not in event.answer.await_args.args[0]
    assert guard.client.calls == calls


async def test_empty_immediate_permission_read_cannot_erase_requested_restrictions(
    guard, store, monkeypatch
):
    get_permissions = guard.client.get_permissions
    stale_returned = False

    async def stale_once(chat, user):
        nonlocal stale_returned
        permissions = await get_permissions(chat, user)
        if (
            user == USER
            and not stale_returned
            and any(call[0] == "restrict" for call in guard.client.calls)
        ):
            stale_returned = True
            permissions.participant.banned_rights = None
        return permissions

    monkeypatch.setattr(guard.client, "get_permissions", stale_once)
    session = await begin(guard, store)
    assert not session["payload"]["restriction_confirmed"]
    assert session["payload"]["restricted_rights"]["send_messages"]
    assert session["payload"]["requested_rights"]["send_photos"]
    await guard.verification.callback(callback(session))
    saved = await store.verification(CHAT, USER)
    assert saved["status"] == "passed"
    assert saved["payload"]["answer_actor_id"] == USER
    assert saved["payload"]["answer_accepted_at"]


@pytest.mark.parametrize("button", [None, "s"])
async def test_legacy_empty_checkpoint_is_reconstructed_for_safe_restore(guard, store, button):
    session = await begin(guard, store)
    session["payload"]["restricted_rights"] = rights_dict(None)
    session["payload"].pop("requested_rights")
    await store.save_verification(session)
    event = callback(session) if button is None else admin_button(session, button)
    await guard.verification.callback(event)
    saved = await store.verification(CHAT, USER)
    assert saved["status"] == ("passed" if button is None else "admin_skipped")
    assert not guard.client.member_rights[(CHAT, USER)]["send_messages"]


async def test_telegram_permanent_date_and_aggregate_flags_compare_by_effect(guard, store):
    session = await begin(guard, store)
    current = guard.client.member_rights[(CHAT, USER)]
    current["until_date"] = 2147483647
    assert same_rights(current, session["payload"]["requested_rights"])
    await guard.verification.callback(callback(session))
    assert (await store.verification(CHAT, USER))["status"] == "passed"


async def test_expired_and_failed_sessions_remain_immune_to_admin_replay(guard, store):
    session = await begin(guard, store)
    session["expires_at"] = utc_now_naive() - timedelta(seconds=1)
    await store.save_verification(session)
    await guard.recover_once()
    before = list(guard.client.calls)
    for button in ["b", "x", "s"]:
        event = admin_button(session, button)
        await guard.verification.callback(event)
        assert "已封禁" in event.answer.await_args.args[0]
    assert guard.client.calls == before
    assert not store.black


def test_normalization_keeps_nonverification_permissions_and_finite_deadlines():
    original = rights_dict(None)
    original["invite_users"] = True
    restricted = verification_rights(original)
    assert restricted["invite_users"]
    assert not same_rights(restricted, {**restricted, "pin_messages": True})
    finite = int(utc_now_naive().replace(tzinfo=UTC).timestamp()) + 300
    assert not same_rights({**restricted, "until_date": finite}, restricted)
    assert same_rights(
        rights_dict(rights_object({**restricted, "until_date": 2147483647})), restricted
    )
