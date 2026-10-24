# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
"""真实 MySQL 的举报入队原子性、租约互斥与恢复。"""

from datetime import timedelta
from unittest.mock import AsyncMock

import pytest

from core.infrastructure.database.mysql import utc_now_naive
from core.moderation.ai_report_repository import AIReportJobs
from core.moderation.ai_reports import AIReportReview
from core.moderation.repository import GuardRepository, GuardStore

from .conftest import ADMIN, CHAT, USER
from .test_ai_reports import ai as ai
from .test_ai_reports import decision
from .test_mysql import TEST_DSN
from .test_mysql import mysql_repository as mysql_repository
from .test_reports import report_event

pytestmark = [
    pytest.mark.integration,
    pytest.mark.database,
    pytest.mark.skipif(not TEST_DSN, reason="GUARD_MYSQL_TEST_DSN not configured"),
]


async def submit(guard, repository):
    guard.repository = repository
    guard.ai_reports.jobs = AIReportJobs(repository)
    event = report_event(guard)
    return await guard.reports.submit(
        CHAT, guard.client.users[event.sender_id], await event.get_reply_message(), "举报", event.id
    )


async def make_due(repository, case):
    async with repository.lock(USER) as store:
        await store.execute(
            "UPDATE moderation_cases SET created_at=%s WHERE id=%s",
            (utc_now_naive() - timedelta(minutes=11), case["id"]),
        )
        await store.execute(
            "UPDATE moderation_ai_reports SET retry_at=%s WHERE case_id=%s",
            (utc_now_naive() - timedelta(seconds=1), case["id"]),
        )


async def test_mysql_new_report_enqueues_once_and_restart_executes_and_audits(
    guard, ai, mysql_repository
):
    await mysql_repository.initialize()
    case, created = await submit(guard, mysql_repository)
    assert created
    duplicate, created = await submit(guard, mysql_repository)
    assert not created and duplicate["id"] == case["id"]
    assert not await ai.jobs.due()
    await make_due(mysql_repository, case)
    restarted = AIReportReview(guard)
    restarted.classifier = AsyncMock(return_value=decision())
    guard.ai_reports = restarted
    await restarted.run_once()
    result = await mysql_repository.get_case(case["id"])
    assert result["action"] == "ban" and result["status"] == "pending"
    assert result["payload"]["ai_report_review"]["state"] == "applied"
    assert result["payload"]["ban_message_cleanup"]["status"] == "deleted"
    async with mysql_repository.lock(USER) as store:
        audit = await restarted.jobs.get(store, case["id"])
        assert audit["status"] == "applied" and audit["attempts"] == 1
        assert audit["payload"]["input"]["profile"]["bio"] == "我的个人简介"
        assert not await store.blacklist(USER) and not await store.whitelisted(CHAT, USER)
    await restarted.run_once()
    assert restarted.classifier.await_count == 1


async def test_mysql_enqueue_failure_rolls_back_case_and_can_be_resubmitted(
    guard, ai, mysql_repository, monkeypatch
):
    guard.repository = mysql_repository
    ai.jobs = AIReportJobs(mysql_repository)
    original = ai.jobs.enqueue
    monkeypatch.setattr(ai.jobs, "enqueue", AsyncMock(side_effect=ConnectionError("injected")))
    event = report_event(guard)
    with pytest.raises(ConnectionError):
        await guard.reports.submit(
            CHAT,
            guard.client.users[event.sender_id],
            await event.get_reply_message(),
            "举报",
            event.id,
        )
    assert (await mysql_repository.query("cases"))["total"] == 0
    monkeypatch.setattr(ai.jobs, "enqueue", original)
    case, created = await submit(guard, mysql_repository)
    assert created and case["action"] == "report"


async def test_mysql_stale_worker_cannot_claim_or_commit_another_lease(guard, ai, mysql_repository):
    case, _ = await submit(guard, mysql_repository)
    await make_due(mysql_repository, case)
    candidate = (await ai.jobs.due())[0]
    other = AIReportJobs(GuardRepository(mysql_repository.pool))
    async with mysql_repository.lock(USER) as store:
        first = await ai.jobs.claim(store, candidate)
        assert first and not await other.claim(store, candidate)
        await store.execute(
            "UPDATE moderation_ai_reports SET retry_at=%s WHERE case_id=%s",
            (utc_now_naive() - timedelta(seconds=1), case["id"]),
        )
        second = await other.claim(store, first)
        assert second and second["token"] != first["token"]
        assert not await ai.jobs.save(store, first, "manual", {"stale": True})
        assert (await ai.jobs.get(store, case["id"]))["token"] == second["token"]


async def test_mysql_human_decision_during_model_call_is_not_overwritten(
    guard, ai, mysql_repository
):
    case, _ = await submit(guard, mysql_repository)
    await make_due(mysql_repository, case)

    async def model(context):
        await guard.reviews.review(case["id"], "unban", ADMIN)
        return decision()

    ai.classifier = model
    await ai.run_once()
    result = await mysql_repository.get_case(case["id"])
    assert result["status"] == "ignored" and result["reviewer_actor"] == f"telegram:{ADMIN}"
    assert not any(call[0] == "ban" for call in guard.client.calls)


async def test_mysql_policy_change_before_execution_restores_manual_report(
    guard, ai, mysql_repository
):
    case, _ = await submit(guard, mysql_repository)
    await make_due(mysql_repository, case)
    original = guard.execute_case

    async def before_action(store, row):
        guard.group(CHAT).ai_report_review_enabled = False
        await original(store, row)

    guard.execute_case = before_action
    await ai.run_once()
    result = await mysql_repository.get_case(case["id"])
    assert result["action"] == "report" and result["status"] == "pending"
    assert not result["reviewer_actor"] and not any(call[0] == "ban" for call in guard.client.calls)


async def test_mysql_queue_uses_fk_and_cascades_ended_report_retention(guard, ai, mysql_repository):
    case, _ = await submit(guard, mysql_repository)
    await guard.reviews.review(case["id"], "unban", ADMIN)
    async with mysql_repository.pool.acquire() as connection:
        store = GuardStore(connection)
        await store.execute("DELETE FROM moderation_cases WHERE id=%s", (case["id"],))
        assert not await ai.jobs.get(store, case["id"])
