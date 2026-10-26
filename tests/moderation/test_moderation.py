# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
import asyncio
from types import SimpleNamespace
from unittest.mock import AsyncMock

import pytest
from telethon import events
from telethon.errors import UserNotParticipantError

from core.moderation.config import GuardGroup
from core.moderation.matcher import Match
from core.moderation.moderator import GuardError
from core.moderation.notification import review_buttons

from .conftest import ADMIN, CHAT, USER


async def ban_case(guard, store, token="event-1"):
    return await guard.punish(
        store,
        CHAT,
        await guard.client.get_entity(USER),
        Match("nickname", "bad-nick", "主页", "主頁", "主页"),
        token,
    )


async def message_event(guard, text="normal", edit=False):
    event = SimpleNamespace(
        chat_id=CHAT,
        sender_id=USER,
        is_group=True,
        id=42,
        message=SimpleNamespace(
            id=42, message=text, entities=[], edit_date="edited" if edit else None
        ),
    )
    try:
        await guard.handle_message(event)
    except events.StopPropagation:
        pass


async def test_disabled_group_has_no_telegram_or_database_effect(guard, store):
    guard.config.groups[str(CHAT)].enabled = False
    guard.client.users[USER].first_name = "主页"
    await message_event(guard)
    await guard.process_join(CHAT, guard.client.users[USER], "join")
    assert not guard.client.calls and not store.cases and not store.sessions


async def test_nickname_enforced_when_content_is_clean_and_case_replay_is_idempotent(guard, store):
    guard.client.users[USER].first_name = "主頁"
    await message_event(guard)
    await message_event(guard)
    assert len(store.cases) == 1
    case = next(iter(store.cases.values()))
    assert case["status"] == "pending" and case["payload"]["name"] == "主頁"
    assert [call[0] for call in guard.client.calls] == ["ban"]
    notices = await store.notices(case["id"])
    assert {notice["target_id"] for notice in notices} == {ADMIN, 11}
    notification = guard.client.sent[(ADMIN, notices[0]["message_id"])]
    assert str(CHAT) in notification["text"] and str(USER) in notification["text"]
    assert [row[0].text for row in notification["buttons"]] == [
        "仅解除封禁",
        "解除并加入白名单",
        "永久拉黑并入库标记",
        "仅踢出",
    ]


@pytest.mark.parametrize("protection", ["admin", "trusted_bot", "whitelist"])
async def test_protected_members_are_not_punished(guard, store, protection):
    guard.client.users[USER].first_name = "主页"
    if protection == "admin":
        guard.client.admins.add(USER)
    elif protection == "trusted_bot":
        guard.client.users[USER].bot = True
        guard.config.groups[str(CHAT)].trusted_bot_ids = [USER]
    else:
        store.white.add((CHAT, USER))
    await message_event(guard)
    assert not guard.client.calls and not store.cases


async def test_unknown_permissions_fail_closed(guard, store):
    guard.client.fail["permissions"] = RuntimeError("unavailable")
    guard.client.users[USER].first_name = "主页"
    await message_event(guard)
    assert not store.cases and not guard.client.calls


@pytest.mark.parametrize("action", ["delete", "warn", "mute", "kick", "ban"])
async def test_edited_captions_execute_configured_actions(guard, store, action):
    group = guard.config.groups[str(CHAT)]
    group.message_filter = True
    group.message_keywords = ["spam"]
    group.message_action = action
    await message_event(guard, text="caption ＳＰＡＭ", edit=True)
    case = next(iter(store.cases.values()))
    assert case["action"] == action
    assert case["status"] == ("pending" if action == "ban" else "done")
    assert case["payload"]["message_id"] == 42


async def test_failed_ban_does_not_get_review_buttons(guard, store):
    guard.client.fail["ban"] = PermissionError("insufficient rights")
    case = await ban_case(guard, store)
    assert case["status"] == "execution_failed"
    assert review_buttons(case) is None
    assert all(not message["buttons"] for message in guard.client.sent.values())
    assert all("执行失败" in message["text"] for message in guard.client.sent.values())


async def test_successful_ban_with_failed_commit_is_recovered(guard, store):
    store.fail_pending = 1
    with pytest.raises(ConnectionError):
        await ban_case(guard, store)
    assert next(iter(store.cases.values()))["status"] == "executing"
    assert not store.notifications
    await guard.recover_once()
    assert next(iter(store.cases.values()))["status"] == "pending"
    assert len(store.cases) == 1


async def test_unauthorized_or_forged_review_never_unbans(guard, store):
    case = await ban_case(guard, store)
    for reviewer, origin in [(9999, None), (ADMIN, (CHAT, 123)), (ADMIN, (ADMIN, 999999))]:
        with pytest.raises(GuardError):
            await guard.reviews.review(case["id"], "allow", reviewer, origin=origin)
    assert not any(call[0] == "unban" for call in guard.client.calls)
    assert (await store.case(case["id"]))["status"] == "pending"


async def test_unban_and_whitelist_are_idempotent_and_sync_all_notices(guard, store):
    case = await ban_case(guard, store)
    notice = (await store.notices(case["id"]))[0]
    result = await guard.reviews.review(
        case["id"], "allow", ADMIN, origin=(notice["target_id"], notice["message_id"])
    )
    again = await guard.reviews.review(case["id"], "blacklist", 11)
    assert result["status"] == again["status"] == "allowed"
    assert result["reviewer_id"] == ADMIN
    assert (CHAT, USER) in store.white and USER not in store.black
    assert sum(call[0] == "unban" for call in guard.client.calls) == 1
    for message in guard.client.sent.values():
        assert message["buttons"] is None
        assert "不会自动重新拉入群" in message["text"]


async def test_simultaneous_opposing_reviews_first_valid_decision_wins(guard, store):
    case = await ban_case(guard, store)
    results = await asyncio.gather(
        guard.reviews.review(case["id"], "blacklist", ADMIN),
        guard.reviews.review(case["id"], "allow", 11),
    )
    assert all(result["status"] == "blacklisted" for result in results)
    assert USER in store.black and (CHAT, USER) not in store.white
    assert not any(call[0] == "unban" for call in guard.client.calls)


async def test_unban_failure_keeps_durable_review_intent_and_recovery(guard, store):
    case = await ban_case(guard, store)
    guard.client.fail["unban"] = RuntimeError("offline")
    with pytest.raises(GuardError):
        await guard.reviews.review(case["id"], "allow", ADMIN)
    assert (await store.case(case["id"]))["status"] == "reviewing_allow"
    assert (CHAT, USER) not in store.white
    guard.client.fail.clear()
    await guard.recover_once()
    assert (await store.case(case["id"]))["status"] == "allowed"


async def test_database_failure_after_unban_is_never_reported_success(guard, store):
    case = await ban_case(guard, store)
    store.fail_finish = 1
    with pytest.raises(GuardError):
        await guard.reviews.review(case["id"], "allow", ADMIN)
    saved = await store.case(case["id"])
    assert saved["status"] == "reviewing_allow" and saved["error"] == "ConnectionError"
    assert (CHAT, USER) not in store.white
    await guard.recover_once()
    assert (await store.case(case["id"]))["status"] == "allowed"


async def test_global_blacklist_conflict_prevents_unban(guard, store):
    case = await ban_case(guard, store)
    store.black[USER] = {"user_id": USER}
    with pytest.raises(GuardError, match="blacklist_conflict"):
        await guard.reviews.review(case["id"], "allow", ADMIN)
    assert (await store.case(case["id"]))["status"] == "pending"
    assert not any(call[0] == "unban" for call in guard.client.calls)


async def test_global_sharing_is_explicit_and_user_id_survives_rename(guard, store):
    store.black[USER] = {"user_id": USER}
    user = guard.client.users[USER]
    user.first_name = "new innocent name"
    group = guard.config.groups[str(CHAT)]
    assert await guard.match(store, group, CHAT, user) is None
    group.share_global_blacklist = True
    store.white.add((CHAT, USER))
    assert (await guard.match(store, group, CHAT, user)).target == "global_blacklist"
    guard.config.groups[str(CHAT - 1)] = GuardGroup(enabled=False, share_global_blacklist=True)
    await message_event(guard)
    assert all(row["chat_id"] == CHAT for row in store.cases.values())


async def test_notification_failure_fallback_and_later_retry(guard, store):
    guard.config.groups[str(CHAT)].review_chat_id = CHAT - 10
    guard.client.fail[ADMIN] = RuntimeError("private chat blocked")
    case = await ban_case(guard, store)
    notices = await store.notices(case["id"])
    assert any(row["target_id"] == ADMIN and row["error"] for row in notices)
    assert any(row["target_id"] == CHAT - 10 and row["message_id"] for row in notices)
    guard.client.fail.clear()
    for notice in store.notifications.values():
        notice["retry_at"] = case["created_at"]
    await guard.recover_once()
    assert all(not row["error"] for row in await store.notices(case["id"]))


async def test_group_admin_review_requires_opt_in(guard):
    guard.client.admins.add(USER)
    assert not await guard.authorized(USER, CHAT)
    guard.config.groups[str(CHAT)].allow_group_admin_review = True
    assert await guard.authorized(USER, CHAT)
    assert not await guard.authorized(USER, CHAT, web=True)


async def test_join_request_match_rejects_instead_of_claiming_a_ban(guard, store):
    from telethon.tl.types import PeerChannel

    guard.client.fail["permissions"] = UserNotParticipantError(request=None)
    guard.client.users[USER].first_name = "主頁"
    await guard.handle_join_request(
        SimpleNamespace(peer=PeerChannel(1234567890), user_id=USER, date="today", qts=1)
    )
    case = next(iter(store.cases.values()))
    assert case["action"] == "reject" and case["status"] == "done"
    assert ("reject", CHAT, USER) in guard.client.calls
    assert not review_buttons(case)


async def test_start_stop_registers_main_bot_only_and_cleans_up(guard):
    guard.recover_once = AsyncMock()
    await guard.start()
    assert guard.client.add_event_handler.call_count == 8
    assert any(callback == guard.handle_membership_update for callback, _ in guard._handlers)
    await guard.stop()
    assert guard.client.remove_event_handler.call_count == 8 and guard._worker is None


async def test_partial_message_kick_still_unbans_after_group_is_disabled(guard, store):
    guard.client.fail["unban"] = RuntimeError("offline")
    case = await guard.punish(
        store,
        CHAT,
        guard.client.users[USER],
        Match("message", "kick", "spam", "spam", "spam", "kick"),
        "kick-1",
    )
    assert case["status"] == "executing" and case["payload"]["kick_banned"]
    guard.client.fail.clear()
    guard.config.groups[str(CHAT)].enabled = False
    await guard.recover_once()
    assert (await store.case(case["id"]))["status"] == "done"
    assert sum(call[0] == "ban" for call in guard.client.calls) == 1
    assert not guard.client.member_rights[(CHAT, USER)]["view_messages"]


async def test_existing_webui_admin_identity_is_audited_without_telegram_impersonation(
    guard, store
):
    case = await ban_case(guard, store)
    with pytest.raises(GuardError):
        await guard.reviews.review(case["id"], "allow", "admin")
    result = await guard.reviews.review(case["id"], "allow", "admin", web=True)
    assert result["status"] == "allowed"
    assert result["reviewer_id"] is None and result["reviewer_actor"] == "webui:admin"


async def test_global_blacklist_added_during_partial_allow_is_enforced(guard, store):
    case = await ban_case(guard, store)
    store.fail_finish = 1
    with pytest.raises(GuardError):
        await guard.reviews.review(case["id"], "allow", ADMIN)
    store.black[USER] = {"user_id": USER}
    guard.config.groups[str(CHAT)].share_global_blacklist = True
    await guard.recover_once()
    assert guard.client.member_rights[(CHAT, USER)]["view_messages"]
    assert (await store.case(case["id"]))["error"] == "guard.blacklist_conflict"
    assert (CHAT, USER) not in store.white
    del store.black[USER]
    await guard.recover_once()
    assert (await store.case(case["id"]))["status"] == "allowed"
