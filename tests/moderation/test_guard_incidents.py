# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
"""访客、改名、直接加入、结构过滤以及即时历史清理的事件级回归。"""

from datetime import UTC, datetime
from types import SimpleNamespace

import pytest
from telethon import Button, events, utils
from telethon.errors import UserNotParticipantError
from telethon.tl import types
from telethon.tl.types import (
    ChannelParticipant,
    ChannelParticipantBanned,
    ChatBannedRights,
    KeyboardButtonRow,
    MessageFwdHeader,
    PeerUser,
    ReplyInlineMarkup,
    UpdateChannelParticipant,
    UpdateUserName,
    Username,
)

from core.moderation.config import GuardGroup
from core.moderation.matcher import Matcher

from .conftest import ADMIN, CHAT, USER
from .test_moderation import message_event


def structural_message(kind):
    return SimpleNamespace(
        id=42,
        message="无关键词的消息",
        edit_date=None,
        entities=[],
        reply_markup=ReplyInlineMarkup(
            [
                getattr(types, "KeyboardInlineButtonRow", KeyboardButtonRow)(
                    [Button.inline("打开", b"open")]
                )
            ]
        )
        if kind == "button"
        else None,
        fwd_from=MessageFwdHeader(datetime.now(UTC), from_name="隐藏来源")
        if kind == "forwarded"
        else None,
    )


@pytest.mark.parametrize("kind", ["button", "forwarded"])
@pytest.mark.parametrize("action", ["delete", "warn", "mute", "kick", "ban"])
async def test_structural_filters_execute_own_actions_without_keyword_filter(
    guard, store, kind, action
):
    group = guard.group(CHAT)
    setattr(group, f"block_{kind}_messages", True)
    setattr(group, f"{kind}_message_action", action)
    assert not group.message_filter
    message = structural_message(kind)
    event = SimpleNamespace(chat_id=CHAT, sender_id=USER, is_group=True, id=42, message=message)
    guard.client.admins.add(guard.bot_id)
    with pytest.raises(events.StopPropagation):
        await guard.handle_message(event)
    case = next(iter(store.cases.values()))
    assert case["action"] == action
    assert case["payload"]["evidence"]["target"] == f"{kind}_message"
    if action == "ban":
        assert case["payload"]["ban_message_cleanup"]["status"] == "deleted"
        guard.ban_history.revoke.assert_awaited_once()
    else:
        guard.ban_history.revoke.assert_not_awaited()


@pytest.mark.parametrize("kind", ["button", "forwarded"])
def test_structural_filters_default_off_and_ignore_missing_metadata(kind):
    group = GuardGroup(enabled=True)
    assert Matcher(group).message(structural_message(kind)) is None
    setattr(group, f"block_{kind}_messages", True)
    message = SimpleNamespace(message="ordinary", reply_markup=ReplyInlineMarkup([]), fwd_from=None)
    assert Matcher(group).message(message) is None


async def test_guest_human_with_forbidden_name_is_not_exempted(guard, store, monkeypatch):
    guard.client.users[USER].first_name = "主页广告"
    guard.client.admins.add(guard.bot_id)
    original = guard.client.get_permissions

    async def permissions(chat_id, user_id):
        if user_id == USER and not guard.client.member_rights[chat_id, user_id]["view_messages"]:
            raise UserNotParticipantError(None)
        return await original(chat_id, user_id)

    monkeypatch.setattr(guard.client, "get_permissions", permissions)
    await message_event(guard)
    case = next(iter(store.cases.values()))
    assert case["status"] == "pending"
    assert ("ban", CHAT, USER) in guard.client.calls
    assert case["payload"]["ban_message_cleanup"]["status"] == "deleted"


async def test_name_change_uses_update_and_checks_every_enabled_group(guard, store):
    other = CHAT - 1
    guard.config.groups[str(other)] = GuardGroup(enabled=True, verify_join=False)
    guard.client.admins.add(guard.bot_id)
    # 模拟 get_entity 尚未反映的昵称/多用户名更新。
    update = UpdateUserName(USER, "主页广告", "", [Username(username="extra_alias", active=True)])
    await guard.handle_profile_update(update)
    assert {row["chat_id"] for row in store.cases.values()} == {CHAT, other}
    assert all(
        row["payload"]["ban_message_cleanup"]["status"] == "deleted" for row in store.cases.values()
    )
    assert guard.client.users[USER].first_name == "正常成员"


async def test_active_alternate_username_is_filtered_but_inactive_alias_is_not(guard, store):
    guard.config.nickname_keywords = ["spam"]
    update = UpdateUserName(
        USER,
        "normal",
        "",
        [Username(username="ordinary", active=True), Username(username="spam_alias", active=True)],
    )
    await guard.handle_profile_update(update)
    assert len(store.cases) == 1


async def test_raw_member_join_checks_nickname_without_chat_action(guard, store):
    guard.client.users[USER].first_name = "主页"
    update = UpdateChannelParticipant(
        channel_id=utils.resolve_id(CHAT)[0],
        date=datetime.now(UTC),
        actor_id=ADMIN,
        user_id=USER,
        prev_participant=None,
        new_participant=ChannelParticipant(USER, datetime.now(UTC)),
        qts=1,
    )
    await guard.handle_membership_update(update)
    assert len(store.cases) == 1
    assert ("ban", CHAT, USER) in guard.client.calls


async def test_repeated_name_after_external_unban_creates_new_ban_and_new_cleanup(guard, store):
    guard.client.admins.add(guard.bot_id)
    update = UpdateUserName(USER, "主页", "", [])
    await guard.handle_profile_update(update)
    first = next(iter(store.cases.values()))
    assert first["payload"]["ban_message_cleanup"]["status"] == "deleted"
    await guard.actions.unban(CHAT, USER)
    await guard.handle_profile_update(UpdateUserName(USER, "正常名字", "", []))
    await guard.handle_profile_update(update)
    assert len(store.cases) == 2
    assert store.cases[first["id"]]["status"] == "cancelled"
    assert guard.ban_history.revoke.await_count == 2


@pytest.mark.parametrize("action", ["ban", "blacklist"])
async def test_reviewed_permanent_bans_clean_history_before_returning(guard, store, action):
    from .test_reports import report_case, send

    await send(guard)
    case = await guard.reviews.review(report_case(store)["id"], action, ADMIN)
    assert case["payload"]["ban_message_cleanup"]["status"] == "deleted"
    guard.ban_history.revoke.assert_awaited_once_with(CHAT, USER, guard.bot_id)


@pytest.mark.parametrize("action", ["b", "x"])
async def test_verification_admin_ban_cleans_history_before_callback_returns(guard, store, action):
    from .test_verification import begin
    from .test_verification_admin import admin_button, operation_cases

    guard.client.admins.add(guard.bot_id)
    session = await begin(guard, store)
    await guard.verification.callback(admin_button(session, action))
    assert operation_cases(store)[0]["payload"]["ban_message_cleanup"]["status"] == "deleted"
    guard.ban_history.revoke.assert_awaited_once()


async def test_history_cleanup_accepts_original_external_banner_and_cancels_new_generation(
    guard, store, monkeypatch
):
    from core.moderation.matcher import Match

    guard.client.admins.add(guard.bot_id)
    original = guard.client.get_permissions
    initial = ChannelParticipantBanned(
        PeerUser(USER),
        ADMIN,
        datetime.now(UTC),
        ChatBannedRights(None, view_messages=True),
        left=True,
    )

    async def permissions(chat_id, user_id):
        result = await original(chat_id, user_id)
        if user_id == USER:
            result.participant = initial
        return result

    monkeypatch.setattr(guard.client, "get_permissions", permissions)
    case = await guard.punish(
        store,
        CHAT,
        guard.client.users[USER],
        Match("nickname", "test", "主页", "主页", "主页"),
        "preexisting-ban",
    )
    assert case["payload"]["ban_message_cleanup"]["status"] == "deleted"
    assert case["payload"]["ban_message_cleanup"]["source_ban"]["actor"] == ADMIN
    guard.ban_history.revoke.assert_awaited_once()
    # 保留重试意图后出现另一轮封禁，不能执行旧清理。
    guard.ban_history.revoke.reset_mock()
    store.cases[case["id"]]["payload"]["ban_message_cleanup"]["status"] = "pending"
    initial.kicked_by = ADMIN + 1
    await guard.ban_history.resume(store, await store.case(case["id"]))
    assert (await store.case(case["id"]))["payload"]["ban_message_cleanup"]["status"] == "cancelled"
    guard.ban_history.revoke.assert_not_awaited()
