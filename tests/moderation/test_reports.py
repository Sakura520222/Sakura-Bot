# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
"""引用举报的真实受理、通知与审核流程，仅模拟 Telegram 和存储边界。"""

import asyncio
from datetime import UTC, datetime, timedelta
from types import SimpleNamespace
from unittest.mock import AsyncMock

import pytest
from telethon import events
from telethon.errors import ChatForwardsRestrictedError, MessageIdInvalidError
from telethon.tl.custom import Message
from telethon.tl.functions.messages import ForwardMessagesRequest
from telethon.tl.types import (
    InputMessageEntityMentionName,
    InputPeerChannel,
    InputPeerUser,
    MessageEntityTextUrl,
    MessageReplyHeader,
    PeerChannel,
    PeerUser,
)

from core.i18n.i18n import set_language
from core.infrastructure.database.mysql import utc_now_naive
from core.moderation.moderator import GuardError, Moderator
from core.moderation.notification import case_status, notification_text, review_buttons
from core.moderation.telegram import TelegramActions

from .conftest import ADMIN, CHAT, USER

REPORTER = 300


def report_event(guard, *, text="@admin 举报", reporter=REPORTER, message_id=42, chat_id=CHAT):
    guard.client.admins.add(guard.bot_id)
    guard.client.users[reporter] = SimpleNamespace(
        id=reporter, first_name="举报人🌸", last_name=None, username="reporter", bot=False
    )
    original = SimpleNamespace(
        id=message_id,
        chat_id=chat_id,
        sender_id=USER,
        message="被举报的广告内容",
        action=None,
        media=None,
        date=datetime(2026, 10, 19, 1, 0, tzinfo=UTC),
    )
    return SimpleNamespace(
        chat_id=chat_id,
        chat=None,
        sender_id=reporter,
        is_group=True,
        id=message_id + 100,
        message=SimpleNamespace(
            id=message_id + 100, message=text, reply_to_msg_id=message_id, edit_date=None
        ),
        get_reply_message=AsyncMock(return_value=original),
        reply=AsyncMock(),
    )


async def send(guard, event=None, **kwargs):
    event = event or report_event(guard, **kwargs)
    with pytest.raises(events.StopPropagation):
        await guard.reports.handle(event)
    return event


def report_case(store):
    return next(row for row in store.cases.values() if row["action"] == "report")


async def test_report_works_without_automatic_guard_and_applies_no_penalty(guard, store):
    guard.group(CHAT).enabled = False
    event = await send(guard, text="@admin 举报 广告骚扰")
    case = report_case(store)
    assert case["action"] == "report" and case["status"] == "pending"
    assert case["user_id"] == USER and case["chat_id"] == CHAT
    assert case["payload"]["reporter"]["user_id"] == REPORTER
    assert case["payload"]["evidence"]["reason"] == "广告骚扰"
    assert case["payload"]["evidence"]["original"] == "被举报的广告内容"
    assert case["payload"]["evidence_link"] == "https://t.me/c/1234567890/42"
    assert case["payload"]["report_link"] == "https://t.me/c/1234567890/142"
    assert not guard.client.calls and not store.white and not store.black
    assert len(guard.client.forwarded) == 2
    assert {request.to_peer for request in guard.client.forwarded.values()} == {ADMIN, 11}
    assert all(
        request.from_peer == CHAT and request.id == [42]
        for request in guard.client.forwarded.values()
    )
    notices = await store.notices(case["id"])
    assert {row["target_id"] for row in notices} == {ADMIN, 11}
    for row in notices:
        message = guard.client.sent[row["target_id"], row["message_id"]]
        assert "尚未处罚" in message["text"] and "已封禁" not in message["text"]
        assert str(REPORTER) in message["text"] and str(USER) in message["text"]
        assert [button[0].text for button in message["buttons"]] == [
            "忽略本次举报",
            "一键封禁",
            "封禁并加入黑名单",
        ]
    assert "已提交管理员审核" in event.reply.call_args.args[0]


@pytest.mark.parametrize("language", ["zh-CN", "en-US"])
async def test_user_content_is_literal_and_both_users_have_correct_profile_mentions(
    guard, store, language
):
    set_language(language)
    event = report_event(guard, text="@admin 举报 <b>骗子</b> & **广告**")
    guard.client.users[REPORTER].first_name = '<a href="https://evil.example">举报人🌸</a>'
    guard.client.users[USER].first_name = "<b>被举报人🌸</b>"
    event.get_reply_message.return_value.message = "<b>消息🌸</b> & [fake](https://evil.example)"
    await send(guard, event)
    case = report_case(store)
    for notice in await store.notices(case["id"]):
        message = guard.client.sent[notice["target_id"], notice["message_id"]]
        assert message["text"] == notification_text(case)
        assert "<b>消息🌸</b>" in message["text"]
        mentions = [
            entity
            for entity in message["entities"]
            if isinstance(entity, InputMessageEntityMentionName)
        ]
        assert {entity.user_id.user_id for entity in mentions} == {USER, REPORTER}
        for mention in mentions:
            assert bytes(mention)
            label = (
                message["text"]
                .encode("utf-16-le")[mention.offset * 2 : (mention.offset + mention.length) * 2]
                .decode("utf-16-le")
            )
            assert label == guard.client.users[mention.user_id.user_id].first_name
        assert all(
            entity.url.startswith("https://t.me/")
            for entity in message["entities"]
            if isinstance(entity, MessageEntityTextUrl)
        )


@pytest.mark.parametrize(
    "text",
    [
        "normal",
        "@admin",
        "@admin举报",
        "@someone 举报",
        "转发 @admin 举报",
        "@admin 举报人",
        "/report",
    ],
)
async def test_unrelated_text_does_not_trigger_reports(guard, store, text):
    event = report_event(guard, text=text)
    await guard.reports.handle(event)
    assert not store.cases and not event.reply.called and not guard.client.sent


@pytest.mark.parametrize("kind", ["private", "channel", "anonymous", "bot", "edit", "self_bot"])
async def test_private_channel_anonymous_bot_and_edited_messages_are_ignored(guard, store, kind):
    event = report_event(guard)
    if kind in {"private", "channel"}:
        event.is_group = False
    elif kind == "anonymous":
        event.sender_id = CHAT
    elif kind == "bot":
        guard.client.users[REPORTER].bot = True
    elif kind == "edit":
        event.message.edit_date = datetime.now(UTC)
    else:
        event.sender_id = guard.bot_id
    await guard.reports.handle(event)
    assert not store.cases and not event.reply.called


@pytest.mark.parametrize(
    "kind", ["no_reply", "deleted", "self", "main_bot", "channel_identity", "other_chat", "service"]
)
async def test_invalid_replies_receive_feedback_without_a_case(guard, store, kind):
    event = report_event(guard)
    original = event.get_reply_message.return_value
    if kind == "no_reply":
        event.message.reply_to_msg_id = None
    elif kind == "deleted":
        event.get_reply_message.return_value = None
    elif kind in {"self", "main_bot", "channel_identity"}:
        original.sender_id = {"self": REPORTER, "main_bot": guard.bot_id, "channel_identity": CHAT}[
            kind
        ]
    elif kind == "other_chat":
        original.chat_id = CHAT - 1
    else:
        original.action = object()
    await send(guard, event)
    assert not store.cases and not guard.client.sent
    assert "请引用" in event.reply.call_args.args[0]


@pytest.mark.parametrize(
    "kind", ["not_admin", "permissions_unavailable", "disabled", "event_disabled", "no_targets"]
)
async def test_unmanaged_groups_and_unavailable_notifications_are_not_accepted(guard, store, kind):
    event = report_event(guard)
    if kind == "not_admin":
        guard.client.admins.discard(guard.bot_id)
    elif kind == "permissions_unavailable":
        guard.client.fail["permissions"] = RuntimeError("unavailable")
    elif kind == "disabled":
        guard.group(CHAT).notification_enabled = False
    elif kind == "event_disabled":
        guard.group(CHAT).notification_events = []
    else:
        guard.default_admins = lambda: []
    await send(guard, event)
    assert not store.cases and not guard.client.sent and event.reply.called


async def test_duplicate_original_message_is_idempotent_across_concurrent_events_and_restart(
    guard, store
):
    first, second = report_event(guard), report_event(guard, reporter=REPORTER + 1)
    await asyncio.gather(send(guard, first), send(guard, second))
    assert len(store.cases) == 1 and len(guard.client.sent) == 2
    case_id = report_case(store)["id"]
    restarted = Moderator(
        guard.client, guard.repository, guard.config, default_admins=guard.default_admins
    )
    restarted.bot_id = guard.bot_id
    duplicate = await send(restarted)
    assert len(store.cases) == 1 and len(guard.client.sent) == 2
    assert (
        case_id in duplicate.reply.call_args.args[0]
        and "已有举报" in duplicate.reply.call_args.args[0]
    )


async def test_rate_limit_is_per_reporter_and_group_and_does_not_block_duplicates(
    guard, store, monkeypatch
):
    await send(guard)
    limited = await send(guard, message_id=43)
    assert len(store.cases) == 1 and "60 秒" in limited.reply.call_args.args[0]
    await send(guard)  # 同一消息仍可查询案件 ID。
    await send(guard, message_id=43, reporter=REPORTER + 1)
    await send(guard, chat_id=CHAT - 1, message_id=44)
    assert len(store.cases) == 3
    deadline = guard.reports._cooldowns[CHAT, REPORTER]
    monkeypatch.setattr(
        "core.moderation.report_handler.time", SimpleNamespace(monotonic=lambda: deadline + 1)
    )
    await send(guard, message_id=45)
    assert len(store.cases) == 4


async def test_database_failure_does_not_send_notices_or_consume_cooldown(
    guard, store, monkeypatch
):
    create = store.create_case
    monkeypatch.setattr(
        store, "create_case", AsyncMock(side_effect=ConnectionError("database offline"))
    )
    await send(guard)
    assert not store.cases and not guard.client.sent and not guard.client.calls
    monkeypatch.setattr(store, "create_case", create)
    await send(guard)
    assert len(store.cases) == 1


async def test_notification_failures_are_queued_and_recovered_without_duplicate_cases(guard, store):
    guard.client.fail[ADMIN] = RuntimeError("cannot message admin")
    guard.client.fail[11] = RuntimeError("cannot message admin")
    event = await send(guard)
    case = report_case(store)
    assert "暂未送达" in event.reply.call_args.args[0] and len(store.cases) == 1
    assert not guard.client.calls
    guard.client.fail.clear()
    for notice in store.notifications.values():
        notice["retry_at"] = utc_now_naive() - timedelta(seconds=1)
    await guard.recover_once()
    await guard.recover_once()
    assert len(guard.client.sent) == 2 and len(store.cases) == 1
    assert all(not row["error"] for row in await store.notices(case["id"]))


async def test_private_notification_failure_uses_review_chat(guard, store):
    guard.group(CHAT).notification_ids = [ADMIN]
    guard.group(CHAT).review_chat_id = -1002222222222
    guard.client.fail[ADMIN] = RuntimeError("cannot message admin")
    event = await send(guard)
    notices = await store.notices(report_case(store)["id"])
    assert any(row["target_id"] == -1002222222222 and row["message_id"] for row in notices)
    assert "已提交管理员审核" in event.reply.call_args.args[0]


async def test_media_and_long_messages_keep_a_bounded_snapshot_and_source_link(guard, store):
    event = report_event(guard)
    event.get_reply_message.return_value.media = object()
    event.get_reply_message.return_value.message = "🌸" * 5000
    await send(guard, event)
    case = report_case(store)
    assert len(case["payload"]["evidence"]["original"]) == 2048
    assert case["payload"]["has_media"]
    for message in guard.client.sent.values():
        assert len(message["text"].encode("utf-16-le")) // 2 <= 4096
        assert "🌸" * 1000 + "…" in message["text"]
    event = report_event(guard, message_id=43, reporter=REPORTER + 1)
    event.get_reply_message.return_value.message = ""
    event.get_reply_message.return_value.media = object()
    await send(guard, event)
    assert any("媒体消息" in message["text"] for message in guard.client.sent.values())


@pytest.mark.parametrize("language", ["zh-CN", "en-US"])
async def test_report_notification_fits_telegram_with_long_emoji_names_reason_and_content(
    guard, store, language
):
    await send(guard)
    set_language(language)
    case = report_case(store)
    case["payload"].update(chat_title="🌸" * 128, name="🌸" * 256, username="u" * 32)
    case["payload"]["reporter"].update(name="🌸" * 256, username="r" * 32)
    case["payload"]["evidence"].update(reason="🌸" * 256, original="🌸" * 2048)
    assert len(notification_text(case).encode("utf-16-le")) // 2 <= 4096


@pytest.mark.parametrize(
    "action,status", [("unban", "ignored"), ("ban", "pending"), ("blacklist", "blacklisted")]
)
async def test_administrator_can_dismiss_ban_or_blacklist_a_report(guard, store, action, status):
    guard.group(CHAT).enabled = False
    await send(guard)
    case = report_case(store)
    result = await guard.reviews.review(case["id"], action, ADMIN)
    assert result["status"] == status and result["reviewer_id"] == ADMIN
    assert result["payload"]["review_history"][-1]["action"] == action
    assert not store.white
    if action == "unban":
        assert not guard.client.calls and not store.black and review_buttons(result) is None
        assert "已忽略" in case_status(result)
    else:
        assert guard.client.calls == [("ban", CHAT, USER)]
        assert bool(store.black) == (action == "blacklist")
        if action == "ban":
            assert result["payload"]["review_generation"] == 1
            with pytest.raises(GuardError, match="guard.invalid_callback"):
                await guard.reviews.review(case["id"], "unban", ADMIN, expected_generation=0)
            result = await guard.reviews.review(case["id"], "unban", ADMIN, expected_generation=1)
            assert result["status"] == "unbanned"


async def test_dismissing_a_report_never_releases_existing_bans_or_blacklists(guard, store):
    await send(guard)
    case = report_case(store)
    store.black[USER] = {"user_id": USER}
    await store.create_case({**case, "id": "a" * 32, "event_key": "b" * 64, "action": "ban"})
    result = await guard.reviews.review(case["id"], "unban", ADMIN)
    assert result["status"] == "ignored" and USER in store.black and not guard.client.calls


async def test_review_checks_permissions_original_notice_and_allowed_actions(guard, store):
    await send(guard)
    case = report_case(store)
    for action, actor, kwargs in [
        ("ban", REPORTER, {}),
        ("ban", ADMIN, {"origin": (CHAT, 999)}),
        ("allow", ADMIN, {}),
    ]:
        with pytest.raises(GuardError):
            await guard.reviews.review(case["id"], action, actor, **kwargs)
    assert not guard.client.calls and (await store.case(case["id"]))["status"] == "pending"
    guard.client.admins.add(USER)
    with pytest.raises(GuardError, match="guard.operation_failed"):
        await guard.reviews.review(case["id"], "ban", ADMIN)
    assert not guard.client.calls and not store.black


async def test_failed_review_persists_first_choice_and_recovers(guard, store):
    await send(guard)
    case = report_case(store)
    guard.client.fail["ban"] = RuntimeError("temporary failure")
    with pytest.raises(GuardError, match="guard.operation_failed"):
        await guard.reviews.review(case["id"], "blacklist", ADMIN)
    assert (await store.case(case["id"]))["status"] == "reviewing_blacklist"
    with pytest.raises(GuardError, match="guard.already_processing"):
        await guard.reviews.review(case["id"], "unban", ADMIN)
    guard.client.fail.clear()
    await guard.recover_once()
    assert (await store.case(case["id"]))["status"] == "blacklisted" and USER in store.black


async def test_callback_from_original_notice_reports_the_correct_unpunished_status(guard, store):
    await send(guard)
    case = report_case(store)
    notice = (await store.notices(case["id"]))[0]
    event = SimpleNamespace(
        data=f"sg:r:{case['id']}:u:0".encode(),
        sender_id=ADMIN,
        chat_id=notice["target_id"],
        message_id=notice["message_id"],
        answer=AsyncMock(),
    )
    await guard.reviews.callback(event)
    assert "已忽略本次举报" in event.answer.call_args.args[0] and not guard.client.calls


async def test_native_telethon_reply_and_message_properties_are_supported(guard, store):
    event = report_event(guard)
    event.message = Message(
        id=142,
        peer_id=PeerChannel(1234567890),
        from_id=PeerUser(REPORTER),
        message="@admin 举报",
        reply_to=MessageReplyHeader(reply_to_msg_id=42),
    )
    event.get_reply_message.return_value = Message(
        id=42,
        peer_id=PeerChannel(1234567890),
        from_id=PeerUser(USER),
        message="证据消息",
    )
    await send(guard, event)
    assert report_case(store)["payload"]["evidence"]["original"] == "证据消息"


async def test_basic_group_without_a_permalink_still_delivers_evidence_and_ids(guard, store):
    await send(guard, chat_id=-12345)
    case = report_case(store)
    assert not case["payload"]["evidence_link"] and not case["payload"]["report_link"]
    assert case["chat_id"] == -12345 and len(guard.client.forwarded) == 2


async def test_raw_forward_request_serializes_source_destination_message_and_stable_id():
    client = AsyncMock()
    client.get_input_entity.side_effect = [
        InputPeerChannel(1234567890, 42),
        InputPeerUser(ADMIN, 99),
    ]
    await TelegramActions(client).forward_once(ADMIN, CHAT, 42, 123456789)
    request = client.call_args.args[0]
    assert isinstance(request, ForwardMessagesRequest) and bytes(request)
    assert request.from_peer.channel_id == 1234567890 and request.to_peer.user_id == ADMIN
    assert request.id == [42] and request.random_id == [123456789]


@pytest.mark.parametrize("error", [ChatForwardsRestrictedError(None), MessageIdInvalidError(None)])
async def test_protected_or_deleted_original_uses_snapshot_without_losing_review_controls(
    guard, store, error
):
    guard.client.fail["forward"] = error
    event = await send(guard)
    case = report_case(store)
    assert "已提交管理员审核" in event.reply.call_args.args[0]
    assert not guard.client.forwarded and len(guard.client.sent) == 2
    for message in guard.client.sent.values():
        assert "无法转发" in message["text"] and "被举报的广告内容" in message["text"]
        assert len(message["buttons"]) == 3
    attempts = len(guard.client.forward_attempts)
    await guard.reviews.review(case["id"], "unban", ADMIN)
    assert len(guard.client.forward_attempts) == attempts


async def test_transient_forward_failure_retries_evidence_before_marking_delivery_complete(
    guard, store
):
    guard.client.fail["forward"] = ConnectionError("temporary failure")
    event = await send(guard)
    assert "暂未送达" in event.reply.call_args.args[0] and not guard.client.sent
    guard.client.fail.clear()
    for notice in store.notifications.values():
        notice["retry_at"] = utc_now_naive() - timedelta(seconds=1)
    await guard.recover_once()
    assert len(guard.client.forwarded) == len(guard.client.sent) == 2
    await guard.reviews.review(report_case(store)["id"], "unban", ADMIN)
    assert len(guard.client.forwarded) == len(guard.client.forward_attempts) - 2 == 2


async def test_forward_succeeded_but_commit_failed_reuses_the_same_random_id(
    guard, store, monkeypatch
):
    update = store.update_case
    failures = 1

    async def fail_after_forward(*args, **kwargs):
        nonlocal failures
        if failures and kwargs.get("payload", {}).get("report_evidence"):
            failures -= 1
            raise ConnectionError("commit failed after Telegram accepted evidence")
        return await update(*args, **kwargs)

    monkeypatch.setattr(store, "update_case", fail_after_forward)
    await send(guard)
    for notice in store.notifications.values():
        notice["retry_at"] = utc_now_naive() - timedelta(seconds=1)
    await guard.recover_once()
    assert len(guard.client.sent) == len(guard.client.forwarded) == 2
    first = [request for request in guard.client.forward_attempts if request.to_peer == ADMIN]
    assert len(first) == 2 and first[0].random_id == first[1].random_id
