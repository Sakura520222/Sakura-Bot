# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
"""举报原消息的独立删除、权限校验、通知更新及中断恢复。"""

import asyncio
from datetime import timedelta
from types import SimpleNamespace
from unittest.mock import AsyncMock

import pytest
from telethon.errors import MessageDeleteForbiddenError

from core.i18n.i18n import set_language
from core.infrastructure.database.mysql import utc_now_naive
from core.moderation.moderator import GuardError, Moderator
from core.moderation.notification import notification_text, review_buttons

from .conftest import ADMIN, CHAT, USER
from .test_reports import REPORTER, report_case, report_event, send


async def test_delete_only_removes_reported_original_and_keeps_review_actions(guard, store):
    await send(guard)
    case = report_case(store)
    before = await store.notices(case["id"])
    result = await guard.reviews.review(case["id"], "delete", ADMIN)
    assert guard.client.calls == [("delete", CHAT, (42,))]
    assert guard.client.deleted == {(CHAT, 42)}
    assert result["status"] == "pending" and result["reviewer_id"] is None
    assert not store.white and not store.black
    deletion = result["payload"]["report_message_deletion"]
    assert deletion["status"] == "deleted" and deletion["actor"] == f"telegram:{ADMIN}"
    assert deletion["completed_at"] and not deletion["error"]
    assert [row[0].text for row in review_buttons(result)] == [
        "忽略本次举报",
        "一键封禁",
        "封禁并加入黑名单",
        "仅踢出",
    ]
    assert {(row["target_id"], row["message_id"]) for row in before} == {
        (row["target_id"], row["message_id"]) for row in await store.notices(case["id"])
    }
    assert len(guard.client.sent) == 2 and len(guard.client.forward_attempts) == 2
    assert all("原消息处理：已删除" in message["text"] for message in guard.client.sent.values())
    await guard.reviews.review(case["id"], "ban", ADMIN)
    assert (await store.case(case["id"]))["payload"]["review_ban_confirmed"]


async def test_concurrent_and_repeated_delete_clicks_are_idempotent(guard, store):
    await send(guard)
    case = report_case(store)
    await asyncio.gather(
        guard.reviews.review(case["id"], "delete", ADMIN),
        guard.reviews.review(case["id"], "delete", 11),
    )
    await guard.reviews.review(case["id"], "delete", 11)
    assert guard.client.calls == [("delete", CHAT, (42,))]
    assert (await store.case(case["id"]))["payload"]["report_message_deletion"][
        "actor"
    ] == f"telegram:{ADMIN}"


@pytest.mark.parametrize(
    "kind", ["unauthorized", "wrong_origin", "retired_origin", "other_chat", "not_report"]
)
async def test_delete_rejects_untrusted_callbacks_without_telegram_effects(guard, store, kind):
    await send(guard)
    case = report_case(store)
    actor, kwargs = ADMIN, {}
    if kind == "unauthorized":
        actor = REPORTER
    elif kind == "wrong_origin":
        kwargs["origin"] = (CHAT, 42)
    elif kind == "retired_origin":
        notice = (await store.notices(case["id"]))[0]
        await store.notice(case["id"], notice["target_id"], notice["message_id"], "removed")
        kwargs["origin"] = (notice["target_id"], notice["message_id"])
    elif kind == "other_chat":
        kwargs["expected_chat_id"] = CHAT - 1
    else:
        store.cases[case["id"]]["action"] = "warn"
    with pytest.raises(GuardError):
        await guard.reviews.review(case["id"], "delete", actor, **kwargs)
    assert not guard.client.calls
    assert not (await store.case(case["id"]))["payload"].get("report_message_deletion")


@pytest.mark.parametrize("change", ["chat_id", "sender_id", "id"])
async def test_source_identity_is_checked_before_deleting(guard, store, change):
    event = report_event(guard)
    await send(guard, event)
    original = event.get_reply_message.return_value
    setattr(original, change, getattr(original, change) + 1)
    with pytest.raises(GuardError, match="guard.report_delete_pending"):
        await guard.reviews.review(report_case(store)["id"], "delete", ADMIN)
    assert not guard.client.calls
    assert (
        report_case(store)["payload"]["report_message_deletion"]["error"]
        == "guard.report_delete_target_changed"
    )


async def test_already_absent_message_is_success_without_deleting_other_messages(guard, store):
    await send(guard)
    guard.client.source_messages.clear()
    result = await guard.reviews.review(report_case(store)["id"], "delete", ADMIN)
    assert not guard.client.calls
    assert result["payload"]["report_message_deletion"]["status"] == "deleted"


async def test_lost_bot_admin_rights_never_claims_message_was_deleted(guard, store):
    await send(guard)
    guard.client.admins.discard(guard.bot_id)
    with pytest.raises(GuardError, match="guard.report_delete_pending"):
        await guard.reviews.review(report_case(store)["id"], "delete", ADMIN)
    assert not guard.client.calls
    assert (
        report_case(store)["payload"]["report_message_deletion"]["error"]
        == "guard.report_delete_permission_error"
    )


async def test_delete_permission_failure_is_not_success_and_recovers_after_restart(guard, store):
    await send(guard)
    case = report_case(store)
    guard.client.fail["delete"] = MessageDeleteForbiddenError(None)
    with pytest.raises(GuardError, match="guard.report_delete_pending"):
        await guard.reviews.review(case["id"], "delete", ADMIN)
    pending = await store.case(case["id"])
    assert pending["payload"]["report_message_deletion"]["status"] == "pending"
    assert all(
        "重试删除被举报消息" in [row[0].text for row in message["buttons"]]
        for message in guard.client.sent.values()
    )
    guard.client.fail.clear()
    await guard.recover_once()  # 到达退避时间前不重复执行。
    assert len(guard.client.calls) == 1
    store.cases[case["id"]]["payload"]["report_message_deletion"]["retry_at"] = (
        utc_now_naive() - timedelta(seconds=1)
    ).isoformat()
    restarted = Moderator(
        guard.client, guard.repository, guard.config, default_admins=guard.default_admins
    )
    restarted.bot_id = guard.bot_id
    await restarted.recover_once()
    result = await store.case(case["id"])
    assert result["payload"]["report_message_deletion"]["status"] == "deleted"
    assert result["payload"]["report_message_deletion"]["actor"] == f"telegram:{ADMIN}"
    assert result["status"] == "pending" and not store.white and not store.black


async def test_database_failure_before_intent_never_deletes_a_message(guard, store, monkeypatch):
    await send(guard)
    monkeypatch.setattr(store, "update_case", AsyncMock(side_effect=ConnectionError("offline")))
    with pytest.raises(ConnectionError):
        await guard.reviews.review(report_case(store)["id"], "delete", ADMIN)
    assert not guard.client.calls


async def test_crash_after_deletion_before_commit_recovers_without_second_delete(
    guard, store, monkeypatch
):
    await send(guard)
    case = report_case(store)
    update = store.update_case
    failures = 1

    async def fail_completed_commit(*args, **kwargs):
        nonlocal failures
        if (
            failures
            and kwargs.get("payload", {}).get("report_message_deletion", {}).get("status")
            == "deleted"
        ):
            failures -= 1
            raise ConnectionError("commit failed after Telegram deletion")
        return await update(*args, **kwargs)

    monkeypatch.setattr(store, "update_case", fail_completed_commit)
    with pytest.raises(GuardError, match="guard.report_delete_pending"):
        await guard.reviews.review(case["id"], "delete", ADMIN)
    await guard.reviews.review(case["id"], "delete", 11)
    assert guard.client.calls == [("delete", CHAT, (42,))]
    assert report_case(store)["payload"]["report_message_deletion"]["actor"] == f"telegram:{ADMIN}"


async def test_api_response_without_actual_deletion_is_retried(guard, store, monkeypatch):
    await send(guard)
    monkeypatch.setattr(guard.client, "delete_messages", AsyncMock(return_value=None))
    with pytest.raises(GuardError, match="guard.report_delete_pending"):
        await guard.reviews.review(report_case(store)["id"], "delete", ADMIN)
    assert (
        report_case(store)["payload"]["report_message_deletion"]["error"]
        == "guard.report_delete_unconfirmed"
    )


async def test_pending_delete_cannot_be_retargeted_by_a_later_payload_change(guard, store):
    await send(guard)
    case = report_case(store)
    guard.client.fail["delete"] = MessageDeleteForbiddenError(None)
    with pytest.raises(GuardError):
        await guard.reviews.review(case["id"], "delete", ADMIN)
    guard.client.fail.clear()
    store.cases[case["id"]]["payload"]["message_id"] = 43
    guard.client.source_messages[CHAT, 43] = SimpleNamespace(id=43, sender_id=USER, chat_id=CHAT)
    with pytest.raises(GuardError):
        await guard.reviews.review(case["id"], "delete", ADMIN)
    assert len(guard.client.calls) == 1 and not guard.client.deleted


@pytest.mark.parametrize("action", ["ban", "blacklist", "unban"])
async def test_delete_is_available_after_other_review_decisions(guard, store, action):
    await send(guard)
    case = report_case(store)
    reviewed = await guard.reviews.review(case["id"], action, ADMIN)
    if action in {"ban", "blacklist"}:
        assert reviewed["payload"]["ban_message_cleanup"]["status"] == "deleted"
        assert not any(row[0].text == "删除被举报消息" for row in review_buttons(reviewed))
        return
    assert any(row[0].text == "删除被举报消息" for row in review_buttons(reviewed))
    result = await guard.reviews.review(
        case["id"], "delete", "admin", web=True, expected_generation=0
    )
    assert result["status"] == reviewed["status"]
    assert result["payload"]["report_message_deletion"]["actor"] == "webui:admin"
    assert guard.client.calls[-1] == ("delete", CHAT, (42,))


async def test_callback_deletes_only_bound_message_and_returns_specific_result(guard, store):
    await send(guard)
    case = report_case(store)
    notice = (await store.notices(case["id"]))[0]
    event = SimpleNamespace(
        data=f"sg:r:{case['id']}:d:0".encode(),
        sender_id=ADMIN,
        chat_id=notice["target_id"],
        message_id=notice["message_id"],
        answer=AsyncMock(),
    )
    await guard.reviews.callback(event)
    assert "被举报消息已删除" in event.answer.call_args.args[0]
    assert guard.client.calls == [("delete", CHAT, (42,))]


async def test_existing_pending_report_notifications_are_updated_in_place(guard, store):
    await send(guard)
    case = report_case(store)
    before = await store.notices(case["id"])
    store.cases[case["id"]]["payload"].pop("report_controls_version")
    for notice in before:
        message = guard.client.sent[notice["target_id"], notice["message_id"]]
        message["buttons"] = message["buttons"][:3]
    await guard.recover_once()
    assert len(guard.client.sent) == len(guard.client.forward_attempts) == 2
    assert all(
        "删除被举报消息" in [row[0].text for row in message["buttons"]]
        for message in guard.client.sent.values()
    )
    assert {(row["target_id"], row["message_id"]) for row in before} == {
        (row["target_id"], row["message_id"]) for row in await store.notices(case["id"])
    }


@pytest.mark.parametrize("language", ["zh-CN", "en-US"])
async def test_deletion_details_remain_within_telegram_text_limit(guard, store, language):
    await send(guard)
    set_language(language)
    case = report_case(store)
    case["payload"].update(chat_title="🌸" * 128, name="🌸" * 256, username="u" * 32)
    case["payload"]["reporter"].update(name="🌸" * 256, username="r" * 32)
    case["payload"]["evidence"].update(reason="🌸" * 256, original="🌸" * 2048)
    case["payload"]["report_message_deletion"] = {
        "status": "pending",
        "actor": "telegram:999999999999999999",
        "requested_at": utc_now_naive().isoformat(),
        "error": "guard.report_delete_target_changed",
    }
    assert len(notification_text(case).encode("utf-16-le")) // 2 <= 4096
