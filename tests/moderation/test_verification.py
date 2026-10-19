# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
import asyncio
from datetime import timedelta
from itertools import count
from types import SimpleNamespace
from unittest.mock import AsyncMock

import pytest

from core.infrastructure.database.mysql import utc_now_naive
from core.moderation.moderator import GuardError, Moderator

from .conftest import CHAT, USER

_queries = count(1000)


async def begin(guard, store):
    guard.config.groups[str(CHAT)].verify_join = True
    await guard.process_join(CHAT, guard.client.users[USER], "join-1")
    return await store.verification(CHAT, USER)


def callback(session, *, correct=True, user_id=USER, chat_id=CHAT, message_id=None):
    index = session["payload"]["correct"]
    if not correct:
        index = (index + 1) % 4
    return SimpleNamespace(
        sender_id=user_id,
        chat_id=chat_id,
        message_id=message_id if message_id is not None else session["message_id"],
        data=(
            f"sg:v:{session['id']}:a"
            + next(
                token
                for token, value in session["payload"]["answer_tokens"].items()
                if value == session["payload"]["options"][index]
            )
        ).encode(),
        query=SimpleNamespace(query_id=next(_queries)),
        answer=AsyncMock(),
    )


async def test_success_restores_exact_original_restrictions(guard, store):
    guard.client.member_rights[(CHAT, USER)]["send_polls"] = True
    original = dict(guard.client.member_rights[(CHAT, USER)])
    session = await begin(guard, store)
    assert session["status"] == "waiting"
    assert guard.client.member_rights[(CHAT, USER)]["send_messages"]
    event = callback(session)
    await guard.verification.callback(event)
    assert (await store.verification(CHAT, USER))["status"] == "passed"
    assert guard.client.member_rights[(CHAT, USER)] == original
    assert "验证通过" in event.answer.await_args.args[0]


@pytest.mark.parametrize(
    "overrides", [{"user_id": USER + 1}, {"chat_id": CHAT - 1}, {"message_id": 999999}]
)
async def test_verification_button_is_bound_to_user_chat_and_message(guard, store, overrides):
    session = await begin(guard, store)
    with pytest.raises(GuardError, match="invalid_callback"):
        await guard.verification.callback(callback(session, **overrides))
    assert (await store.verification(CHAT, USER))["status"] == "waiting"
    assert guard.client.member_rights[(CHAT, USER)]["send_messages"]


async def test_timeout_is_recovered_after_restart_and_keeps_group_ban(guard, store):
    session = await begin(guard, store)
    session["expires_at"] = utc_now_naive() - timedelta(seconds=1)
    await store.save_verification(session)
    restarted = Moderator(
        guard.client, guard.repository, guard.config, default_admins=guard.default_admins
    )
    restarted.bot_id = guard.bot_id
    await restarted.recover_once()
    assert (await store.verification(CHAT, USER))["status"] == "failed_banned"
    assert ("ban", CHAT, USER) in guard.client.calls
    assert ("unban", CHAT, USER) not in guard.client.calls
    assert guard.client.member_rights[(CHAT, USER)]["view_messages"]
    assert (CHAT, session["message_id"]) not in guard.client.sent
    assert not store.black


async def test_verification_timeout_ban_failure_keeps_retryable_phase(guard, store):
    session = await begin(guard, store)
    session["expires_at"] = utc_now_naive() - timedelta(seconds=1)
    await store.save_verification(session)
    guard.client.fail["ban"] = RuntimeError("offline")
    await guard.recover_once()
    saved = await store.verification(CHAT, USER)
    assert saved["status"] == "kicking" and saved["error"]
    guard.client.fail.clear()
    await guard.recover_once()
    assert (await store.verification(CHAT, USER))["status"] == "failed_banned"
    assert ("unban", CHAT, USER) not in guard.client.calls
    assert guard.client.member_rights[(CHAT, USER)]["view_messages"]


async def test_disabling_guard_does_not_release_an_unverified_member(guard, store):
    session = await begin(guard, store)
    guard.reload({})
    await guard.recover_once()
    assert (await store.verification(CHAT, USER))["status"] == "waiting"
    assert guard.client.member_rights[(CHAT, USER)]["send_messages"]
    assert not any(call[0] == "ban" for call in guard.client.calls)
    await guard.verification.callback(callback(session))
    assert (await store.verification(CHAT, USER))["status"] == "passed"


async def test_deleted_challenge_is_replaced_and_old_message_cannot_answer(guard, store):
    old = await begin(guard, store)
    await guard.client.delete_messages(CHAT, [old["message_id"]])
    await guard.recover_once()
    new = await store.verification(CHAT, USER)
    assert old["message_id"] != new["message_id"]
    assert old["id"] == new["id"] and old["expires_at"] == new["expires_at"]
    with pytest.raises(GuardError):
        await guard.verification.callback(callback(old))
    await guard.verification.callback(callback(new))
    assert (await store.verification(CHAT, USER))["status"] == "passed"


async def test_duplicate_joins_and_parallel_answers_do_not_reset_or_repeat_permissions(
    guard, store
):
    session = await begin(guard, store)
    await guard.process_join(CHAT, guard.client.users[USER], "join-replay")
    assert (await store.verification(CHAT, USER))["id"] == session["id"]
    await asyncio.gather(
        guard.verification.callback(callback(session)),
        guard.verification.callback(callback(session)),
    )
    assert (await store.verification(CHAT, USER))["status"] == "passed"
    assert sum(call[0] == "restrict" for call in guard.client.calls) == 2


async def test_wrong_answers_are_bounded(guard, store):
    session = await begin(guard, store)
    for _ in range(3):
        await guard.verification.callback(callback(session, correct=False))
    saved = await store.verification(CHAT, USER)
    assert saved["attempts"] == 3 and saved["status"] == "failed_banned"


async def test_replayed_wrong_callback_does_not_spend_more_attempts(guard, store):
    session = await begin(guard, store)
    event = callback(session, correct=False)
    event.query = SimpleNamespace(query_id=123456)
    await guard.verification.callback(event)
    await guard.verification.callback(event)
    assert (await store.verification(CHAT, USER))["attempts"] == 1


async def test_manual_permission_change_is_not_overwritten(guard, store):
    session = await begin(guard, store)
    guard.client.member_rights[(CHAT, USER)]["invite_users"] = True
    event = callback(session)
    await guard.verification.callback(event)
    saved = await store.verification(CHAT, USER)
    assert saved["status"] == "passing" and saved["error"] == "guard.permissions_changed"
    assert guard.client.member_rights[(CHAT, USER)]["invite_users"]
    assert "验证通过" not in event.answer.await_args.args[0]


async def test_leave_and_rejoin_replaces_challenge_and_preserves_original_permissions(guard, store):
    original = dict(guard.client.member_rights[(CHAT, USER)])
    session = await begin(guard, store)
    await guard.handle_join(
        SimpleNamespace(chat_id=CHAT, user_left=True, user_kicked=False, user_ids=[USER])
    )
    assert (await store.verification(CHAT, USER))["status"] == "left"
    await guard.process_join(CHAT, guard.client.users[USER], "join-2")
    new = await store.verification(CHAT, USER)
    assert new["id"] != session["id"]
    await guard.verification.callback(callback(new))
    assert guard.client.member_rights[(CHAT, USER)] == original


async def test_rename_during_verification_is_checked_again(guard, store):
    session = await begin(guard, store)
    guard.client.users[USER].first_name = "主頁"
    event = callback(session)
    await guard.verification.callback(event)
    assert (await store.verification(CHAT, USER))["status"] == "blocked"
    assert (
        next(case for case in store.cases.values() if case["action"] == "ban")["status"]
        == "pending"
    )
    assert "验证通过" not in event.answer.await_args.args[0]
