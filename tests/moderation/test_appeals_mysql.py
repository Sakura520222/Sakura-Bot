# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
"""在专用随机 MySQL schema 内验证跨进程申诉、事务与故障恢复。"""

import asyncio
from datetime import timedelta
from types import SimpleNamespace

import aiomysql
import pytest
from telegram.error import Forbidden

from core.handlers.appeal_handler import AppealHandler
from core.infrastructure.database.mysql import utc_now_naive
from core.moderation.appeal_inbox import AppealInbox
from core.moderation.appeal_repository import AppealRepository, AppealStore
from core.moderation.appeals import AppealWorkflow
from core.moderation.config import GuardGroup
from core.moderation.matcher import Match
from core.moderation.moderator import GuardError
from core.moderation.repository import GuardRepository

from .conftest import ADMIN, CHAT, USER
from .test_mysql import TEST_DSN, create_ban
from .test_mysql import mysql_repository as mysql_repository

pytestmark = [
    pytest.mark.integration,
    pytest.mark.database,
    pytest.mark.skipif(not TEST_DSN, reason="GUARD_MYSQL_TEST_DSN not configured"),
]


class AssistantBot:
    def __init__(self):
        self.sent = []
        self.fail = set()
        self.media = []

    async def send_message(self, chat_id, text, **kwargs):
        if chat_id in self.fail:
            raise Forbidden("test: bot blocked")
        self.sent.append((chat_id, text, kwargs))
        return SimpleNamespace(message_id=len(self.sent))

    async def edit_message_text(self, chat_id, message_id, **kwargs):
        if chat_id in self.fail:
            raise Forbidden("test: bot blocked")
        return True

    async def send_photo(self, chat_id, photo):
        self.media.append((chat_id, photo))


@pytest.fixture
async def setup_appeal(guard, mysql_repository):
    case = await create_ban(guard, mysql_repository)
    repository = AppealRepository(mysql_repository)
    inbox = AppealInbox(repository)
    workflow = AppealWorkflow(guard, repository)
    handler = AppealHandler(repository)
    bot = AssistantBot()
    return SimpleNamespace(
        guard=guard,
        database=mysql_repository,
        repository=repository,
        inbox=inbox,
        workflow=workflow,
        handler=handler,
        bot=bot,
        case=case,
    )


async def submitted(env):
    appeal = await env.inbox.begin(USER, env.case["id"])
    entry = await env.repository.input(USER)
    await env.inbox.append(USER, "我已更正昵称，请重新核实本次封禁。", None, 100)
    await env.inbox.submit(USER, entry["token"])
    await env.workflow.recover_once()
    await env.handler.deliver(SimpleNamespace(bot=env.bot))
    return await env.repository.get(appeal["id"])


async def queue(env, appeal, action, *, actor=ADMIN, reason="已核实本次封禁的情况"):
    async with env.repository.reader() as store:
        notice = next(row for row in await store.notices(appeal["id"]) if row["target_id"] == actor)
    entry = await env.inbox.review_input(
        actor, appeal["id"], appeal["revision"], (actor, notice["message_id"]), action
    )
    if reason:
        await env.inbox.append(actor, reason, None, 200)
    await env.inbox.submit(actor, entry["token"])


async def retry_now(env, appeal_id):
    async with env.repository.lock(USER) as store:
        await store.guard.execute(
            "UPDATE moderation_appeals SET retry_at=%s WHERE id=%s", (utc_now_naive(), appeal_id)
        )


async def test_draft_identity_restart_and_duplicate_submission(setup_appeal):
    env = setup_appeal
    with pytest.raises(GuardError):
        await env.inbox.begin(USER + 1, env.case["id"])
    first, second = await asyncio.gather(
        env.inbox.begin(USER, env.case["id"]), env.inbox.begin(USER, env.case["id"])
    )
    assert first["id"] == second["id"]
    await env.inbox.append(
        USER,
        "请核实封禁，我已更正相关问题。",
        {"type": "photo", "file_id": "same-bot-file", "size": 12},
        90,
    )
    restarted = AppealRepository(GuardRepository(env.database.pool))
    entry = await restarted.input(USER)
    assert entry["payload"]["files"][0]["file_id"] == "same-bot-file"
    result = await AppealInbox(restarted).submit(USER, entry["token"])
    assert result["status"] == "submitted" and await restarted.input(USER) is None
    with pytest.raises(GuardError):
        await env.inbox.submit(USER, entry["token"])
    assert (await restarted.user_rows(USER))[0]["id"] == first["id"]
    assert not await restarted.user_rows(USER + 1)


@pytest.mark.parametrize("action,whitelist", [("approve", False), ("allow", True)])
async def test_approval_reuses_group_ban_and_whitelist_with_audit(setup_appeal, action, whitelist):
    env = setup_appeal
    appeal = await submitted(env)
    await queue(env, appeal, action)
    await env.workflow.recover_once()
    current = await env.repository.get(appeal["id"])
    assert current["status"] == "approved" and current["active_key"] is None
    assert not env.guard.client.member_rights[CHAT, USER]["view_messages"]
    async with env.repository.reader() as store:
        assert await store.guard.whitelisted(CHAT, USER) == whitelist
        assert not await store.guard.whitelisted(CHAT - 1, USER)
        actions = [row["action"] for row in await store.events(appeal["id"])]
        assert {"created", "submitted", "accepted", "approved_intent", "approved"} <= set(actions)
    original = await env.database.get_case(env.case["id"])
    assert original["payload"]["review_history"][-1]["appeal_id"] == appeal["id"]


async def test_rejection_keeps_ban_and_enforces_cooldown(setup_appeal):
    env = setup_appeal
    appeal = await submitted(env)
    await queue(env, appeal, "reject", reason="证据与封禁时记录一致，暂不解除")
    await env.workflow.recover_once()
    current = await env.repository.get(appeal["id"])
    assert current["status"] == "rejected" and len(current["payload"]["evidence"]) == 1
    assert env.guard.client.member_rights[CHAT, USER]["view_messages"]
    with pytest.raises(GuardError, match="appeal.cooldown"):
        await env.inbox.begin(USER, env.case["id"])


async def test_more_evidence_preserves_original_and_invalidates_old_buttons(setup_appeal):
    env = setup_appeal
    appeal = await submitted(env)
    await queue(env, appeal, "more", reason="请补充昵称更正的截图和说明")
    await env.workflow.recover_once()
    current = await env.repository.get(appeal["id"])
    assert current["status"] == "need_info"
    with pytest.raises(GuardError, match="appeal.stale"):
        await env.inbox.supplement(USER, appeal["id"], appeal["revision"])
    entry = await env.inbox.supplement(USER, appeal["id"], current["revision"])
    await env.inbox.append(USER, "这是更正后的情况说明，请再次核实。", None, 300)
    current = await env.inbox.submit(USER, entry["token"])
    assert current["status"] == "pending" and len(current["payload"]["evidence"]) == 2
    assert current["payload"]["source"] == appeal["payload"]["source"]


async def test_opposing_concurrent_admin_reviews_have_one_winner(setup_appeal):
    env = setup_appeal
    appeal = await submitted(env)
    await asyncio.gather(
        queue(env, appeal, "approve", actor=ADMIN), queue(env, appeal, "reject", actor=11)
    )
    await env.workflow.recover_once()
    current = await env.repository.get(appeal["id"])
    assert current["status"] in {"approved", "rejected"}
    async with env.repository.reader() as store:
        requests = await store.guard.all(
            "SELECT * FROM moderation_appeal_requests WHERE appeal_id=%s", (appeal["id"],)
        )
    assert sorted(row["status"] for row in requests) == ["done", "failed"]


async def test_commit_failure_after_unban_rolls_back_whitelist_and_recovers_once(
    setup_appeal, monkeypatch
):
    env = setup_appeal
    appeal = await submitted(env)
    original = AppealStore._transition
    failures = 1

    async def fail_once(self, item, status, *args, **kwargs):
        nonlocal failures
        if status == "approved" and failures:
            failures -= 1
            raise aiomysql.OperationalError(2013, "injected final commit failure")
        return await original(self, item, status, *args, **kwargs)

    monkeypatch.setattr(AppealStore, "_transition", fail_once)
    await queue(env, appeal, "allow")
    await env.workflow.recover_once()
    assert (await env.repository.get(appeal["id"]))["status"] == "executing"
    async with env.repository.reader() as store:
        assert not await store.guard.whitelisted(CHAT, USER)
    assert not env.guard.client.member_rights[CHAT, USER]["view_messages"]
    before = sum(call[0] == "unban" for call in env.guard.client.calls)
    await retry_now(env, appeal["id"])
    restarted = AppealWorkflow(env.guard, AppealRepository(GuardRepository(env.database.pool)))
    await restarted.recover_once()
    assert (await env.repository.get(appeal["id"]))["status"] == "approved"
    assert sum(call[0] == "unban" for call in env.guard.client.calls) == before


async def test_permission_revoked_during_retry_requires_new_admin_decision(setup_appeal):
    env = setup_appeal
    appeal = await submitted(env)
    env.guard.client.fail["unban"] = PermissionError("temporary failure")
    await queue(env, appeal, "approve")
    await env.workflow.recover_once()
    assert (await env.repository.get(appeal["id"]))["status"] == "executing"
    env.guard.default_admins = lambda: [11]
    env.guard.client.fail.clear()
    await retry_now(env, appeal["id"])
    await env.workflow.recover_once()
    current = await env.repository.get(appeal["id"])
    assert current["status"] == "pending" and current["error"] == "error.permission_denied"
    assert env.guard.client.member_rights[CHAT, USER]["view_messages"]


async def test_global_blacklist_requires_explicit_system_admin_action(setup_appeal):
    env = setup_appeal
    await env.guard.reviews.review(env.case["id"], "blacklist", ADMIN)
    appeal = await submitted(env)
    await queue(env, appeal, "approve")
    await env.workflow.recover_once()
    assert (await env.repository.get(appeal["id"]))["status"] == "pending"
    await queue(env, appeal, "global")
    await env.workflow.recover_once()
    current = await env.repository.get(appeal["id"])
    assert current["status"] == "approved" and not current["payload"]["blacklisted"]
    async with env.repository.reader() as store:
        assert not await store.guard.blacklist(USER)
        assert not await store.guard.whitelisted(CHAT, USER)


async def test_notification_failures_are_durable_and_retry_independently(setup_appeal):
    env = setup_appeal
    env.bot.fail.add(ADMIN)
    appeal = await submitted(env)
    async with env.repository.lock(USER) as store:
        notices = {row["target_id"]: row for row in await store.notices(appeal["id"])}
        assert notices[ADMIN]["error"] == "Forbidden" and notices[ADMIN]["attempts"] == 1
        assert notices[11]["synced_revision"] == appeal["revision"]
        await store.guard.execute(
            "UPDATE moderation_appeal_notifications SET retry_at=%s WHERE appeal_id=%s",
            (utc_now_naive(), appeal["id"]),
        )
    env.bot.fail.clear()
    before = len(env.bot.sent)
    await env.handler.deliver(SimpleNamespace(bot=env.bot))
    assert len(env.bot.sent) == before + 1 and env.bot.sent[-1][0] == ADMIN


async def test_withdraw_does_not_unban_and_old_admin_request_is_rejected(setup_appeal):
    env = setup_appeal
    appeal = await submitted(env)
    await queue(env, appeal, "approve")
    await env.inbox.withdraw(USER, appeal["id"], appeal["revision"])
    await env.workflow.recover_once()
    assert (await env.repository.get(appeal["id"]))["status"] == "withdrawn"
    assert env.guard.client.member_rights[CHAT, USER]["view_messages"]


async def test_source_and_complete_audit_survive_guard_retention_cleanup(setup_appeal):
    env = setup_appeal
    appeal = await submitted(env)
    await queue(env, appeal, "reject")
    await env.workflow.recover_once()
    async with env.repository.lock(USER) as store:
        await store.guard.execute(
            "UPDATE moderation_cases SET created_at=%s WHERE id=%s",
            (utc_now_naive() - timedelta(days=100), env.case["id"]),
        )
    await env.database.cleanup(7)
    assert await env.database.get_case(env.case["id"])
    async with env.repository.reader() as store:
        assert len(await store.events(appeal["id"])) >= 4


async def test_photo_evidence_is_reused_by_same_assistant_bot(setup_appeal):
    env = setup_appeal
    appeal = await env.inbox.begin(USER, env.case["id"])
    entry = await env.repository.input(USER)
    await env.inbox.append(
        USER,
        "这是截图证据，烦请管理员重新复核。",
        {"type": "photo", "file_id": "assistant-file-id", "size": 120},
        100,
    )
    await env.inbox.submit(USER, entry["token"])
    await env.workflow.recover_once()
    await env.handler.deliver(SimpleNamespace(bot=env.bot))
    appeal = await env.repository.get(appeal["id"])
    async with env.repository.reader() as store:
        notice = next(row for row in await store.notices(appeal["id"]) if row["target_id"] == ADMIN)
    await env.inbox.read_request(
        ADMIN, appeal["id"], appeal["revision"], (ADMIN, notice["message_id"]), "evidence"
    )
    await env.workflow.recover_once()
    await env.handler.deliver(SimpleNamespace(bot=env.bot))
    assert env.bot.media == [(ADMIN, "assistant-file-id")]


async def test_expired_draft_releases_input_and_does_not_fill_work_queue(setup_appeal):
    env = setup_appeal
    appeal = await env.inbox.begin(USER, env.case["id"])
    assert not (await env.repository.work())[0]
    async with env.repository.lock(USER) as store:
        await store.guard.execute(
            "UPDATE moderation_appeals SET updated_at=%s WHERE id=%s",
            (utc_now_naive() - timedelta(days=2), appeal["id"]),
        )
    await env.workflow.recover_once()
    assert (await env.repository.get(appeal["id"]))["status"] == "withdrawn"
    assert await env.repository.input(USER) is None


async def test_external_guard_review_closes_waiting_appeal_without_stale_release(setup_appeal):
    env = setup_appeal
    appeal = await submitted(env)
    await env.guard.reviews.review(env.case["id"], "unban", ADMIN)
    await env.workflow.recover_once()
    assert (await env.repository.get(appeal["id"]))["status"] == "superseded"
    assert not env.guard.client.member_rights[CHAT, USER]["view_messages"]


async def test_changed_review_recipients_retire_old_buttons_and_delivery(setup_appeal):
    env = setup_appeal
    appeal = await submitted(env)
    env.guard.group(CHAT).reviewer_ids = [11]
    env.workflow.routing_changed()
    await env.workflow.recover_once()
    async with env.repository.reader() as store:
        notices = {row["target_id"]: row for row in await store.notices(appeal["id"])}
        assert notices[ADMIN]["kind"] == "retired" and notices[11]["kind"] == "reviewer"
    with pytest.raises(GuardError):
        await env.inbox.review_input(
            ADMIN,
            appeal["id"],
            appeal["revision"],
            (ADMIN, notices[ADMIN]["message_id"]),
            "approve",
        )


async def test_repeated_main_workers_do_not_reprocess_a_completed_request(setup_appeal):
    env = setup_appeal
    appeal = await submitted(env)
    await queue(env, appeal, "approve")
    other = AppealWorkflow(env.guard, AppealRepository(GuardRepository(env.database.pool)))
    await asyncio.gather(env.workflow.recover_once(), other.recover_once())
    assert (await env.repository.get(appeal["id"]))["status"] == "approved"
    async with env.repository.reader() as store:
        events = await store.events(appeal["id"])
    assert len([row for row in events if row["action"] == "approved"]) == 1


async def test_new_global_blacklist_refreshes_controls_without_unbanning_other_groups(setup_appeal):
    env = setup_appeal
    appeal = await submitted(env)
    other_chat = CHAT - 1
    env.guard.config.groups[str(other_chat)] = GuardGroup(enabled=True, verify_join=False)
    async with env.database.lock(USER) as store:
        other = await env.guard.punish(
            store,
            other_chat,
            env.guard.client.users[USER],
            Match("nickname", "other", "another reason", "original", "normalized"),
            "other-group",
        )
    await env.guard.reviews.review(other["id"], "blacklist", ADMIN)
    await queue(env, appeal, "approve")
    await env.workflow.recover_once()
    current = await env.repository.get(appeal["id"])
    assert current["status"] == "pending" and current["payload"]["blacklisted"]
    await env.handler.deliver(SimpleNamespace(bot=env.bot))
    await queue(env, current, "global")
    await env.workflow.recover_once()
    assert (await env.repository.get(appeal["id"]))["status"] == "approved"
    assert not env.guard.client.member_rights[CHAT, USER]["view_messages"]
    assert env.guard.client.member_rights[other_chat, USER]["view_messages"]


async def test_unsubmitted_cancelled_draft_does_not_notify_reviewers(setup_appeal):
    env = setup_appeal
    await env.inbox.begin(USER, env.case["id"])
    await env.inbox.append(USER, "尚未确认提交的私人草稿内容。", None, 50)
    await env.inbox.cancel(USER)
    await env.workflow.recover_once()
    await env.handler.deliver(SimpleNamespace(bot=env.bot))
    assert [item[0] for item in env.bot.sent] == [USER]
    assert "私人草稿" not in env.bot.sent[0][1]
