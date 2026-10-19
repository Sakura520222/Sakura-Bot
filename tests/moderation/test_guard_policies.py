# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
from datetime import timedelta
from unittest.mock import AsyncMock

import pytest

from core.infrastructure.database.mysql import utc_now_naive
from core.moderation.config import DEFAULT_NOTIFICATION_EVENTS, GuardGroup, GuardRule
from core.moderation.matcher import Matcher
from core.moderation.moderator import GuardError
from core.moderation.verification import challenge_buttons

from .conftest import ADMIN, CHAT, USER
from .test_verification import begin, callback
from .test_verification_admin import admin_button


async def expire(guard, store):
    session = await begin(guard, store)
    session["expires_at"] = utc_now_naive() - timedelta(seconds=1)
    await store.save_verification(session)
    await guard.recover_once()
    return await store.verification(CHAT, USER)


@pytest.mark.parametrize(
    ("action", "status"),
    [
        ("ban", "failed_banned"),
        ("kick", "failed_kicked"),
        ("mute", "held"),
        ("warn", "held"),
        ("allow", "held"),
    ],
)
async def test_timeout_uses_configured_action_cleans_message_and_notifies_admins(
    guard, store, action, status
):
    guard.config.groups[str(CHAT)].verification_timeout_action = action
    saved = await expire(guard, store)
    assert saved["status"] == status
    assert ((CHAT, saved["message_id"]) in guard.client.sent) is (status == "held")
    if status != "held":
        assert saved["payload"]["cleanup_pending"] is False
    assert not await store.whitelisted(CHAT, USER) and not store.black
    rights = guard.client.member_rights[(CHAT, USER)]
    assert rights["view_messages"] is (action == "ban")
    assert rights["send_messages"] is (status == "held")
    case = next(
        case for case in store.cases.values() if case["payload"].get("verification_failure")
    )
    assert case["action"] == ("verify_hold" if action == "allow" else action)
    assert {row["target_id"] for row in await store.notices(case["id"])} == {ADMIN, 11}


async def test_wrong_answer_policy_is_independent_and_limit_is_configurable(guard, store):
    config = guard.config.groups[str(CHAT)]
    config.verification_max_attempts = 2
    config.verification_wrong_action = "mute"
    config.verification_timeout_action = "allow"
    session = await begin(guard, store)
    text = guard.client.sent[(CHAT, session["message_id"])]["text"]
    assert "答错 2 次" in text and "禁言" in text and "等待管理员" in text
    await guard.verification.callback(callback(session, correct=False))
    assert (await store.verification(CHAT, USER))["status"] == "waiting"
    await guard.verification.callback(callback(session, correct=False))
    saved = await store.verification(CHAT, USER)
    assert saved["attempts"] == 2 and saved["status"] == "held"


async def test_pending_permission_restore_cannot_run_past_deadline(guard, store):
    session = await begin(guard, store)
    guard.client.member_rights[(CHAT, USER)]["invite_users"] = True
    await guard.verification.callback(callback(session))
    pending = await store.verification(CHAT, USER)
    assert pending["status"] == "passing" and pending["error"] == "guard.permissions_changed"
    pending["expires_at"] = utc_now_naive() - timedelta(seconds=1)
    await store.save_verification(pending)
    await guard.recover_once()
    finished = await store.verification(CHAT, USER)
    assert finished["status"] == "failed_banned"
    assert guard.client.member_rights[(CHAT, USER)]["view_messages"]
    assert (CHAT, session["message_id"]) not in guard.client.sent


async def test_delete_failure_remains_retryable_after_terminal_status_without_reban(
    guard, store, monkeypatch
):
    original = guard.client.delete_messages
    monkeypatch.setattr(
        guard.client, "delete_messages", AsyncMock(side_effect=RuntimeError("offline"))
    )
    session = await expire(guard, store)
    assert session["status"] == "failed_banned" and session["payload"]["cleanup_pending"]
    assert (CHAT, session["message_id"]) in guard.client.sent
    bans = sum(call[0] == "ban" for call in guard.client.calls)
    monkeypatch.setattr(guard.client, "delete_messages", original)
    await guard.recover_once()
    saved = await store.verification(CHAT, USER)
    assert not saved["payload"]["cleanup_pending"]
    assert (CHAT, session["message_id"]) not in guard.client.sent
    assert sum(call[0] == "ban" for call in guard.client.calls) == bans


async def test_legacy_terminal_messages_are_cleaned_without_retroactive_bans(guard, store):
    session = await begin(guard, store)
    session["status"] = "expired"
    session["payload"].pop("lifecycle")
    guard.client.member_rights[(CHAT, USER)] = dict(session["payload"]["original_rights"])
    await store.save_verification(session)
    await guard.recover_once()
    assert (CHAT, session["message_id"]) not in guard.client.sent
    assert not any(call[0] == "ban" for call in guard.client.calls)


async def test_result_mode_preserves_result_with_no_active_buttons(guard, store):
    guard.config.groups[str(CHAT)].verification_message_mode = "result"
    session = await expire(guard, store)
    message = guard.client.sent[(CHAT, session["message_id"])]
    assert "验证已结束" in message["text"] and "已封禁" in message["text"]
    assert message["buttons"] is None
    assert session["payload"]["cleanup_pending"] is False


async def test_disabled_admin_action_is_hidden_and_rejected_even_with_forged_data(guard, store):
    config = guard.config.groups[str(CHAT)]
    config.kick_button_enabled = False
    config.verification_admin_actions = ["skip"]
    session = await begin(guard, store)
    assert [len(row) for row in challenge_buttons(session, config)] == [4, 1, 1]
    with pytest.raises(GuardError, match="action_disabled"):
        await guard.verification.callback(admin_button(session, "x"))
    config.verification_admin_actions = []
    await guard.recover_once()
    buttons = guard.client.sent[(CHAT, session["message_id"])]["buttons"]
    assert len(buttons) == 2
    assert buttons[-1][0].type.url == f"tg://user?id={USER}"
    assert not any(case["payload"].get("verification_admin") for case in store.cases.values())


async def test_verification_start_and_pass_notify_all_default_admins_once(guard, store):
    config = guard.group(CHAT)
    assert config.notification_enabled and config.notification_events == DEFAULT_NOTIFICATION_EVENTS
    session = await begin(guard, store)
    await guard.verification.callback(callback(session))
    await guard.recover_once()
    events = [
        case for case in store.cases.values() if case["action"] in {"verify_start", "verify_pass"}
    ]
    assert {case["action"] for case in events} == {"verify_start", "verify_pass"}
    for case in events:
        assert {row["target_id"] for row in await store.notices(case["id"])} == {ADMIN, 11}
    assert len(events) == 2


@pytest.mark.parametrize("mode", ["off", "end_only"])
async def test_notification_switch_and_event_selection_are_respected(guard, store, mode):
    config = guard.config.groups[str(CHAT)]
    config.notification_enabled = mode != "off"
    config.notification_events = ["verification_end"]
    session = await begin(guard, store)
    assert not any(chat in {ADMIN, 11} for chat, _ in guard.client.sent)
    await guard.verification.callback(callback(session))
    assert any(chat in {ADMIN, 11} for chat, _ in guard.client.sent) is (mode == "end_only")
    start = next(case for case in store.cases.values() if case["action"] == "verify_start")
    notices = await store.notices(start["id"])
    assert notices[0]["target_id"] == 0 and notices[0]["synced_status"] == "done"


async def test_nickname_default_and_per_rule_actions_are_configurable(guard):
    user = guard.client.users[USER]
    user.first_name = "主页"
    config = GuardGroup(nickname_action="warn")
    assert Matcher(config).nickname(user).action == "warn"
    config.nickname_keywords = []
    config.rules = [GuardRule(id="custom", target="nickname", value="主页", action="mute")]
    assert Matcher(config).nickname(user).action == "mute"
    config.rules[0].action = "delete"
    assert Matcher(config).nickname(user).action == "warn"


async def test_non_removing_nickname_action_does_not_bypass_join_verification(guard, store):
    guard.config.groups[str(CHAT)].nickname_action = "warn"
    guard.client.users[USER].first_name = "主页"
    session = await begin(guard, store)
    assert session["status"] == "waiting"
    await guard.verification.callback(callback(session))
    assert (await store.verification(CHAT, USER))["status"] == "passed"
    assert not guard.client.member_rights[(CHAT, USER)]["view_messages"]
