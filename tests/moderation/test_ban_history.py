# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
import json
from datetime import datetime, timedelta
from unittest.mock import AsyncMock, patch

import pytest

from core.infrastructure.database.mysql import utc_now_naive
from core.infrastructure.telegram_bot_api import RetryAfterError
from core.moderation import ban_history
from core.moderation.ban_history import BanHistoryCleanup, BotIdentityMismatch
from core.moderation.matcher import Match
from core.moderation.moderator import GuardError
from core.moderation.notification import notification_text

from .conftest import ADMIN, CHAT, USER
from .test_reports import report_case, send


async def reviewed_ban(guard, store, action="ban"):
    await send(guard)
    # 模拟清理前进程被中断，验证持久化恢复；即时路径由 test_guard_incidents 覆盖。
    with patch.object(guard.ban_history, "finish", AsyncMock()):
        return await guard.reviews.review(report_case(store)["id"], action, ADMIN)


@pytest.mark.parametrize("action", ["ban", "blacklist"])
async def test_review_ban_queues_cleanup_then_recovers_once_without_changing_evidence(
    guard, store, action
):
    case = await reviewed_ban(guard, store, action)
    cleanup = case["payload"]["ban_message_cleanup"]
    assert cleanup["status"] == "pending" and cleanup["actor"] == f"telegram:{ADMIN}"
    guard.ban_history.revoke.assert_not_awaited()
    evidence = case["payload"]["evidence"].copy()
    before = {(row["target_id"], row["message_id"]) for row in await store.notices(case["id"])}
    await guard.recover_once()
    await guard.recover_once()
    final = await store.case(case["id"])
    assert final["status"] == ("blacklisted" if action == "blacklist" else "pending")
    assert final["payload"]["ban_message_cleanup"]["status"] == "deleted"
    assert final["payload"]["evidence"] == evidence
    guard.ban_history.revoke.assert_awaited_once_with(CHAT, USER, guard.bot_id)
    assert before == {
        (row["target_id"], row["message_id"]) for row in await store.notices(case["id"])
    }
    assert "全部历史消息" in notification_text(final)
    assert all("已清理该用户" in row["text"] for row in guard.client.sent.values())


async def test_failed_ban_never_prepares_or_executes_history_deletion(guard, store):
    await send(guard)
    guard.client.fail["ban"] = RuntimeError("offline")
    with pytest.raises(GuardError):
        await guard.reviews.review(report_case(store)["id"], "ban", ADMIN)
    assert "ban_message_cleanup" not in report_case(store)["payload"]
    guard.ban_history.revoke.assert_not_awaited()


@pytest.mark.parametrize("action", ["unban", "kick"])
async def test_report_dismissal_and_kick_do_not_delete_user_history(guard, store, action):
    case = await reviewed_ban(guard, store, action)
    assert "ban_message_cleanup" not in case["payload"]
    await guard.recover_once()
    guard.ban_history.revoke.assert_not_awaited()


async def test_automatic_ban_prepares_the_same_cleanup_but_warn_mute_and_delete_do_not(
    guard, store
):
    guard.client.admins.add(guard.bot_id)
    user = guard.client.users[USER]
    async with guard.repository.lock(USER) as locked:
        case = await guard.punish(
            locked, CHAT, user, Match("nickname", "bad", "bad", "bad", "bad"), "auto-ban"
        )
    assert case["payload"]["ban_message_cleanup"]["status"] == "deleted"
    await guard.recover_once()
    guard.ban_history.revoke.assert_awaited_once_with(CHAT, USER, guard.bot_id)


async def test_api_failure_preserves_ban_and_is_retried_after_deadline(guard, store):
    case = await reviewed_ban(guard, store)
    guard.ban_history.revoke.side_effect = RetryAfterError("Too Many Requests: retry after 75", 75)
    before = utc_now_naive()
    await guard.recover_once()
    current = await store.case(case["id"])
    cleanup = current["payload"]["ban_message_cleanup"]
    assert current["status"] == "pending" and cleanup["status"] == "pending"
    assert cleanup["attempts"] == 1 and cleanup["error"] == "RetryAfterError"
    assert datetime.fromisoformat(cleanup["retry_at"]) >= before + timedelta(seconds=75)
    assert guard.client.member_rights[CHAT, USER]["view_messages"]
    await guard.recover_once()
    assert guard.ban_history.revoke.await_count == 1
    guard.ban_history.revoke.side_effect = None
    await guard.ban_history.resume(store, await store.case(case["id"]), force=True)
    assert (await store.case(case["id"]))["payload"]["ban_message_cleanup"]["status"] == "deleted"


@pytest.mark.parametrize(
    "change", ["unban", "external_unban", "other_admin_ban", "promoted", "target", "bot"]
)
async def test_changed_ban_or_target_cancels_without_rebanning_or_deleting(guard, store, change):
    case = await reviewed_ban(guard, store)
    if change == "unban":
        await guard.reviews.review(case["id"], "unban", ADMIN)
    elif change == "external_unban":
        guard.client.member_rights[CHAT, USER]["view_messages"] = False
    elif change == "other_admin_ban":
        guard.client.ban_actors[CHAT, USER] = ADMIN
    elif change == "promoted":
        guard.client.admins.add(USER)
    elif change == "target":
        store.cases[case["id"]]["payload"]["ban_message_cleanup"]["chat_id"] = CHAT - 1
    else:
        guard.bot_id += 1
    calls = list(guard.client.calls)
    await guard.ban_history.resume(store, await store.case(case["id"]))
    assert (await store.case(case["id"]))["payload"]["ban_message_cleanup"]["status"] == "cancelled"
    assert guard.client.calls == calls
    guard.ban_history.revoke.assert_not_awaited()


async def test_legacy_ban_is_not_migrated_into_a_destructive_cleanup(guard, store):
    case = await reviewed_ban(guard, store)
    store.cases[case["id"]]["payload"].pop("ban_message_cleanup")
    await guard.recover_once()
    assert "ban_message_cleanup" not in (await store.case(case["id"]))["payload"]
    guard.ban_history.revoke.assert_not_awaited()


async def test_full_history_cleanup_also_finishes_pending_single_message_deletion(guard, store):
    from core.moderation.notification import review_buttons

    case = await reviewed_ban(guard, store)
    store.cases[case["id"]]["payload"]["report_message_deletion"] = {
        "status": "pending",
        "chat_id": CHAT,
        "user_id": USER,
        "message_id": 42,
        "actor": f"telegram:{ADMIN}",
        "requested_at": utc_now_naive().isoformat(),
    }
    await guard.ban_history.resume(store, await store.case(case["id"]))
    result = await store.case(case["id"])
    assert result["payload"]["report_message_deletion"]["status"] == "deleted"
    assert result["payload"]["report_message_deletion"]["completed_via"] == "ban_message_cleanup"
    assert "删除被举报消息" not in [button.text for row in review_buttons(result) for button in row]


async def test_missing_delete_rights_and_error_text_never_claim_success_or_leak_tokens(
    guard, store
):
    case = await reviewed_ban(guard, store)
    guard.ban_history.revoke.side_effect = RuntimeError(
        "https://api.telegram.org/botSECRET/banChatMember"
    )
    await guard.recover_once()
    cleanup = (await store.case(case["id"]))["payload"]["ban_message_cleanup"]
    assert cleanup["status"] == "pending" and cleanup["error"] == "RuntimeError"
    assert "SECRET" not in json.dumps(cleanup)


@pytest.mark.parametrize("action", ["b", "x"])
async def test_verification_administrator_bans_also_prepare_history_cleanup(guard, store, action):
    from .test_verification import begin
    from .test_verification_admin import admin_button, operation_cases

    session = await begin(guard, store)
    await guard.verification.callback(admin_button(session, action))
    case = operation_cases(store)[0]
    assert case["payload"]["ban_message_cleanup"]["status"] == "pending"
    assert case["payload"]["ban_message_cleanup"]["actor"] == f"telegram:{ADMIN}"


async def test_verification_failure_ban_prepares_history_cleanup(guard, store):
    from .test_verification import begin

    session = await begin(guard, store)
    session["expires_at"] = utc_now_naive() - timedelta(seconds=1)
    await store.save_verification(session)
    await guard.verification.tick(store, session)
    cases = [row for row in store.cases.values() if row["action"] == "ban"]
    assert len(cases) == 1 and cases[0]["payload"]["ban_message_cleanup"]["status"] == "pending"


async def test_basic_group_removed_user_history_is_queued(guard, store, monkeypatch):
    from telethon.errors import UserNotParticipantError

    chat = -42
    await send(guard, chat_id=chat)
    permissions = guard.client.get_permissions

    async def after_removal(chat_id, user_id):
        if (chat_id, user_id) == (chat, USER) and ("kick", chat, USER) in guard.client.calls:
            raise UserNotParticipantError(request=None)
        return await permissions(chat_id, user_id)

    monkeypatch.setattr(guard.client, "get_permissions", after_removal)
    case = await guard.reviews.review(report_case(store)["id"], "ban", ADMIN)
    await guard.ban_history.resume(store, case)
    guard.ban_history.revoke.assert_awaited_once_with(chat, USER, guard.bot_id)
    assert (await store.case(case["id"]))["payload"]["ban_message_cleanup"]["status"] == "deleted"


@pytest.mark.parametrize("identity,answer", [(999, True), (998, True), (999, False)])
async def test_bot_api_adapter_revokes_all_messages_and_checks_bot_identity(
    guard, monkeypatch, identity, answer
):
    """身份从 token 前缀解析（无 getMe 往返）；撤销必须带 revoke_messages=True"""

    calls = []

    async def fake_ban_chat_member(token, chat_id, user_id, *, revoke_messages=False, timeout=None):
        calls.append((token, chat_id, user_id, revoke_messages))
        return answer

    monkeypatch.setattr(ban_history, "bot_id_from_token", lambda token: identity)
    monkeypatch.setattr(ban_history, "ban_chat_member", fake_ban_chat_member)
    cleanup = BanHistoryCleanup(guard)
    if identity != 999:
        with pytest.raises(BotIdentityMismatch):
            await cleanup.revoke(CHAT, USER, 999)
        assert calls == []
    elif not answer:
        with pytest.raises(ValueError, match="HistoryDeletionUnconfirmed"):
            await cleanup.revoke(CHAT, USER, 999)
        assert len(calls) == 1
    else:
        await cleanup.revoke(CHAT, USER, 999)
        _, chat_id, user_id, revoke_messages = calls[-1]
        assert revoke_messages is True
        assert chat_id == CHAT and user_id == USER
