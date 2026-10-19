# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
import asyncio
from datetime import timedelta
from unittest.mock import AsyncMock

import pytest

from core.infrastructure.database.mysql import utc_now_naive
from core.moderation.moderator import GuardError, Moderator
from core.moderation.verification import ADMIN_RESULTS, CHALLENGE_VERSION

from .conftest import ADMIN, CHAT, USER
from .test_verification import begin, callback


def operation_cases(store):
    return [
        case
        for case in store.cases.values()
        if case["action"] not in {"verify_start", "verify_pass", "verify_cancel"}
    ]


def admin_button(session, action="b", actor=ADMIN, **overrides):
    event = callback(session, user_id=actor, **overrides)
    event.data = f"sg:v:{session['id']}:{action}".encode()
    return event


async def test_challenge_shows_identity_and_admin_buttons(guard, store):
    session = await begin(guard, store)
    message = guard.client.sent[(CHAT, session["message_id"])]
    assert "昵称：正常成员" in message["text"]
    assert "用户名：@alice" in message["text"] and str(USER) in message["text"]
    assert [len(row) for row in message["buttons"]] == [4, 2, 1]
    controls = [button for row in message["buttons"][1:] for button in row]
    assert [button.text for button in controls] == ["一键封禁", "封禁并加入黑名单", "跳过验证"]
    assert [button.type.data.rsplit(b":", 1)[1] for button in controls] == [b"b", b"x", b"s"]
    assert all(len(button.type.data) <= 64 for button in controls)


async def test_missing_username_and_multiline_name_have_safe_plain_text_fallbacks(guard, store):
    user = guard.client.users[USER]
    user.first_name, user.last_name, user.username = "A\nB <管理员>", "C", None
    session = await begin(guard, store)
    text = guard.client.sent[(CHAT, session["message_id"])]["text"]
    assert "昵称：A B <管理员> C\n用户名：未设置" in text


async def test_legacy_challenge_is_upgraded_without_resetting_session(guard, store):
    session = await begin(guard, store)
    original = {key: session[key] for key in ["id", "message_id", "expires_at"]}
    session["attempts"] = 2
    session["payload"].pop("challenge_version")
    session["payload"].pop("name")
    session["payload"].pop("username")
    await store.save_verification(session)
    await guard.recover_once()
    updated = await store.verification(CHAT, USER)
    assert {key: updated[key] for key in original} == original
    assert updated["attempts"] == 2
    assert updated["payload"]["question"] == session["payload"]["question"]
    assert updated["payload"]["challenge_version"] == CHALLENGE_VERSION
    assert len(guard.client.sent[(CHAT, session["message_id"])]["buttons"]) == 3
    guard.client.users[USER].first_name = "改名后的成员"
    await guard.recover_once()
    assert "改名后的成员" in guard.client.sent[(CHAT, session["message_id"])]["text"]


@pytest.mark.parametrize(("button", "action"), [("b", "ban"), ("x", "blacklist"), ("s", "skip")])
async def test_admin_actions_have_distinct_effects_and_are_idempotent(guard, store, button, action):
    guard.client.member_rights[(CHAT, USER)]["send_polls"] = True
    original = dict(guard.client.member_rights[(CHAT, USER)])
    session = await begin(guard, store)
    event = admin_button(session, button)
    await guard.verification.callback(event)
    saved = await store.verification(CHAT, USER)
    assert saved["status"] == ADMIN_RESULTS[action]
    assert bool(await store.blacklist(USER)) is (action == "blacklist")
    assert not await store.whitelisted(CHAT, USER)
    case = operation_cases(store)[0]
    assert case["status"] == {"ban": "pending", "blacklist": "blacklisted", "skip": "done"}[action]
    assert case["reviewer_id"] == ADMIN and case["reviewer_actor"] == f"telegram:{ADMIN}"
    assert case["reviewed_at"] is not None
    if action == "skip":
        assert guard.client.member_rights[(CHAT, USER)] == original
        assert "跳过" in event.answer.await_args.args[0]
    else:
        assert guard.client.member_rights[(CHAT, USER)]["view_messages"]
    calls = list(guard.client.calls)
    await guard.verification.callback(event)
    await guard.verification.callback(callback(session))
    assert guard.client.calls == calls
    assert len(operation_cases(store)) == 1
    assert (CHAT, session["message_id"]) not in guard.client.sent


@pytest.mark.parametrize("button", ["b", "x", "s"])
@pytest.mark.parametrize("actor", [USER, USER + 1, -1])
async def test_members_cannot_use_admin_buttons(guard, store, button, actor):
    session = await begin(guard, store)
    with pytest.raises(GuardError, match="permission_denied"):
        await guard.verification.callback(admin_button(session, button, actor=actor))
    assert (await store.verification(CHAT, USER))["status"] == "waiting"
    assert not operation_cases(store) and not store.black


async def test_group_moderator_works_without_case_review_opt_in_and_revocation_is_live(
    guard, store
):
    actor = USER + 9
    guard.client.admins.add(actor)
    assert not guard.group(CHAT).allow_group_admin_review
    session = await begin(guard, store)
    await guard.verification.callback(admin_button(session, "s", actor=actor))
    assert (await store.verification(CHAT, USER))["status"] == "admin_skipped"
    session = await guard.verification.begin(store, CHAT, guard.client.users[USER], "new-join")
    guard.client.admins.remove(actor)
    with pytest.raises(GuardError, match="permission_denied"):
        await guard.verification.callback(admin_button(session, "x", actor=actor))
    assert not store.black


async def test_limited_admin_and_unavailable_permissions_cannot_escalate_privileges(
    guard, store, monkeypatch
):
    actor = USER + 9
    guard.client.admins.add(actor)
    session = await begin(guard, store)
    permissions = await guard.client.get_permissions(CHAT, actor)
    permissions.participant.admin_rights.ban_users = False
    monkeypatch.setattr(guard.client, "get_permissions", AsyncMock(return_value=permissions))
    with pytest.raises(GuardError, match="permission_denied"):
        await guard.verification.callback(admin_button(session, "b", actor=actor))
    monkeypatch.setattr(
        guard.client, "get_permissions", AsyncMock(side_effect=RuntimeError("offline"))
    )
    with pytest.raises(GuardError, match="permission_denied"):
        await guard.verification.callback(admin_button(session, "x", actor=actor))
    assert not operation_cases(store)


@pytest.mark.parametrize("overrides", [{"chat_id": CHAT - 1}, {"message_id": 999999}])
async def test_admin_button_is_bound_to_original_group_and_message(guard, store, overrides):
    session = await begin(guard, store)
    with pytest.raises(GuardError, match="invalid_callback"):
        await guard.verification.callback(admin_button(session, "x", **overrides))
    assert not operation_cases(store)


async def test_old_challenge_cannot_operate_on_replacement_or_rejoined_user(guard, store):
    old = await begin(guard, store)
    await guard.client.delete_messages(CHAT, [old["message_id"]])
    await guard.recover_once()
    with pytest.raises(GuardError, match="invalid_callback"):
        await guard.verification.callback(admin_button(old))
    current = await store.verification(CHAT, USER)
    current["status"] = "left"
    await store.save_verification(current)
    await guard.verification.begin(store, CHAT, guard.client.users[USER], "rejoin")
    with pytest.raises(GuardError, match="invalid_callback"):
        await guard.verification.callback(admin_button(current, "x"))
    assert not operation_cases(store)


@pytest.mark.parametrize("protected", ["admin", "whitelist"])
async def test_stale_challenge_cannot_ban_a_newly_protected_user(guard, store, protected):
    session = await begin(guard, store)
    if protected == "admin":
        guard.client.admins.add(USER)
    else:
        store.white.add((CHAT, USER))
    with pytest.raises(GuardError, match="target_protected"):
        await guard.verification.callback(admin_button(session, "x"))
    assert not operation_cases(store) and not store.black


@pytest.mark.parametrize("trigger", ["nickname", "blacklist"])
async def test_admin_skip_does_not_bypass_other_moderation(guard, store, trigger):
    session = await begin(guard, store)
    if trigger == "nickname":
        guard.client.users[USER].first_name = "主頁"
    else:
        guard.config.groups[str(CHAT)].share_global_blacklist = True
        store.black[USER] = {"user_id": USER}
    event = admin_button(session, "s")
    await guard.verification.callback(event)
    assert (await store.verification(CHAT, USER))["status"] == "blocked"
    assert guard.client.member_rights[(CHAT, USER)]["view_messages"]
    assert not await store.whitelisted(CHAT, USER)
    assert "已跳过" not in event.answer.await_args.args[0]


async def test_skip_preserves_manual_restrictions_and_recovers_only_after_they_are_fixed(
    guard, store
):
    session = await begin(guard, store)
    guard.client.member_rights[(CHAT, USER)]["invite_users"] = True
    await guard.verification.callback(admin_button(session, "s"))
    saved = await store.verification(CHAT, USER)
    assert saved["status"] == "admin_pending" and saved["error"] == "guard.permissions_changed"
    assert guard.client.member_rights[(CHAT, USER)]["invite_users"]
    guard.client.member_rights[(CHAT, USER)] = dict(session["payload"]["restricted_rights"])
    await guard.recover_once()
    assert (await store.verification(CHAT, USER))["status"] == "admin_skipped"


@pytest.mark.parametrize(
    ("button", "action", "failure"),
    [("b", "ban", "ban"), ("x", "blacklist", "ban"), ("s", "skip", "restrict")],
)
async def test_accepted_admin_intent_recovers_after_telegram_failure_and_restart(
    guard, store, button, action, failure
):
    session = await begin(guard, store)
    guard.client.fail[failure] = RuntimeError("offline")
    event = admin_button(session, button)
    await guard.verification.callback(event)
    pending = await store.verification(CHAT, USER)
    assert pending["status"] == "admin_pending"
    assert operation_cases(store)[0]["status"] == "executing"
    assert not store.black
    assert (
        "已封禁" not in event.answer.await_args.args[0]
        and "已跳过" not in event.answer.await_args.args[0]
    )
    other = "s" if button != "s" else "b"
    with pytest.raises(GuardError, match="already_processing"):
        await guard.verification.callback(admin_button(session, other))
    guard.client.fail.clear()
    restarted = Moderator(
        guard.client, guard.repository, guard.config, default_admins=guard.default_admins
    )
    restarted.bot_id = guard.bot_id
    await restarted.recover_once()
    assert (await store.verification(CHAT, USER))["status"] == ADMIN_RESULTS[action]
    assert len(operation_cases(store)) == 1


async def test_blacklist_transaction_failure_never_reports_success_and_can_resume(guard, store):
    session = await begin(guard, store)
    store.fail_finish = 1
    await guard.verification.callback(admin_button(session, "x"))
    assert guard.client.member_rights[(CHAT, USER)]["view_messages"]
    assert not store.black
    assert (await store.verification(CHAT, USER))["status"] == "admin_pending"
    await guard.recover_once()
    assert (await store.verification(CHAT, USER))["status"] == "admin_blacklisted"
    assert (await store.blacklist(USER))["reviewer_id"] == ADMIN


async def test_intent_must_persist_before_any_admin_side_effect(guard, store, monkeypatch):
    session = await begin(guard, store)
    original = store.save_verification

    async def reject_intent(value):
        if value["status"] == "admin_pending":
            raise ConnectionError("write failed")
        await original(value)

    monkeypatch.setattr(store, "save_verification", reject_intent)
    with pytest.raises(ConnectionError):
        await guard.verification.callback(admin_button(session, "x"))
    assert not operation_cases(store) and not store.black
    assert not any(call[0] == "ban" for call in guard.client.calls)
    assert (await store.verification(CHAT, USER))["status"] == "waiting"


async def test_failed_final_session_write_recovers_without_repeating_confirmed_ban(
    guard, store, monkeypatch
):
    session = await begin(guard, store)
    original = store.save_verification

    async def reject_final(value):
        if value["status"] == "admin_banned":
            raise ConnectionError("write failed")
        await original(value)

    monkeypatch.setattr(store, "save_verification", reject_final)
    with pytest.raises(ConnectionError):
        await guard.verification.callback(admin_button(session, "b"))
    assert operation_cases(store)[0]["status"] == "pending"
    monkeypatch.setattr(store, "save_verification", original)
    await guard.recover_once()
    assert (await store.verification(CHAT, USER))["status"] == "admin_banned"
    assert sum(call[0] == "ban" for call in guard.client.calls) == 1


@pytest.mark.parametrize("buttons", [("b", "x"), ("x", "s"), ("s", "b")])
async def test_opposing_admin_clicks_serialize_to_one_durable_choice(guard, store, buttons):
    session = await begin(guard, store)
    await asyncio.gather(
        *(guard.verification.callback(admin_button(session, button)) for button in buttons)
    )
    saved = await store.verification(CHAT, USER)
    assert saved["status"] in set(ADMIN_RESULTS.values())
    assert len(operation_cases(store)) == 1
    assert bool(store.black) is (saved["status"] == "admin_blacklisted")
    assert not await store.whitelisted(CHAT, USER)


async def test_correct_answer_and_admin_ban_cannot_unban_each_other(guard, store):
    session = await begin(guard, store)
    await asyncio.gather(
        guard.verification.callback(admin_button(session, "x")),
        guard.verification.callback(callback(session)),
    )
    saved = await store.verification(CHAT, USER)
    assert saved["status"] in {"passed", "admin_blacklisted"}
    assert guard.client.member_rights[(CHAT, USER)]["view_messages"] is (
        saved["status"] == "admin_blacklisted"
    )


async def test_expired_admin_button_cannot_override_timeout_or_add_blacklist(guard, store):
    session = await begin(guard, store)
    session["expires_at"] = utc_now_naive() - timedelta(seconds=1)
    await store.save_verification(session)
    await guard.verification.callback(admin_button(session, "x"))
    assert (await store.verification(CHAT, USER))["status"] == "failed_banned"
    assert not store.black
    assert guard.client.member_rights[(CHAT, USER)]["view_messages"]
    assert not any(case["payload"].get("verification_admin") for case in store.cases.values())
