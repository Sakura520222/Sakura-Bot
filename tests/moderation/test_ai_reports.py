# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
"""AI 举报从入队到处罚的行为测试，模型调用期间人工接管及重启恢复。"""

import asyncio
from copy import deepcopy
from datetime import timedelta
from types import SimpleNamespace
from unittest.mock import AsyncMock

import pytest
from telethon.tl.functions.users import GetFullUserRequest

from core.infrastructure.database.mysql import utc_now_naive
from core.moderation.ai_report_classifier import Verdict
from core.moderation.ai_reports import AIReportReview
from core.moderation.notification import notification_text

from .ai_report_fakes import MemoryAIJobs
from .conftest import ADMIN, CHAT, USER
from .test_reports import report_event


def decision(**changes):
    return {
        "model": "test-model",
        "verdict": {
            "verdict": "violation",
            "confidence": 0.99,
            "category": "spam",
            "reason": "原消息为明确广告。",
            "evidence": [{"source": "message", "quote": "广告"}],
            **changes,
        },
    }


@pytest.fixture
def ai(guard, store, monkeypatch):
    guard.group(CHAT).ai_report_review_enabled = True
    guard.ai_reports.jobs = MemoryAIJobs()
    guard.ai_reports.classifier = AsyncMock(return_value=decision())
    store.connection = SimpleNamespace(begin=AsyncMock(), commit=AsyncMock(), rollback=AsyncMock())
    original = type(guard.client).__call__

    async def rpc(client, request):
        if isinstance(request, GetFullUserRequest):
            return SimpleNamespace(
                full_user=SimpleNamespace(id=USER, about="我的个人简介"), users=[client.users[USER]]
            )
        return await original(client, request)

    monkeypatch.setattr(type(guard.client), "__call__", rpc)
    return guard.ai_reports


async def queue(guard, store, *, elapsed=True):
    event = report_event(guard)
    case, _ = await guard.reports.submit(
        CHAT, guard.client.users[event.sender_id], await event.get_reply_message(), "举报", event.id
    )
    if elapsed:
        store.cases[case["id"]]["created_at"] -= timedelta(minutes=11)
        guard.ai_reports.jobs.rows[case["id"]]["retry_at"] = utc_now_naive()
    return case


async def test_timeout_defaults_and_no_retroactive_processing(guard, store, ai):
    assert guard.group(CHAT).ai_report_review_delay_minutes == 10
    first = await queue(guard, store, elapsed=False)
    await ai.run_once()
    ai.classifier.assert_not_awaited()
    assert (await store.case(first["id"]))["status"] == "pending"
    guard.group(CHAT).ai_report_review_enabled = False
    event = report_event(guard, message_id=50)
    second, _ = await guard.reports.submit(
        CHAT, guard.client.users[event.sender_id], await event.get_reply_message(), "举报", event.id
    )
    guard.group(CHAT).ai_report_review_enabled = True
    assert second["id"] not in ai.jobs.rows


async def test_clear_violation_bans_and_immediately_clears_history_once(guard, store, ai):
    case = await queue(guard, store)
    notices = deepcopy(store.notifications)
    await ai.process((await ai.jobs.due())[0])
    result = await store.case(case["id"])
    assert result["action"] == "ban" and result["status"] == "pending"
    assert result["reviewer_actor"] == "system:ai_report" and result["reviewer_id"] is None
    assert result["payload"]["ban_message_cleanup"]["status"] == "deleted"
    assert result["payload"]["ai_report_review"]["state"] == "applied"
    context = ai.classifier.await_args.args[0]
    assert context["profile"]["bio"] == "我的个人简介"
    assert context["profile"]["nickname"] == "正常成员"
    assert context["profile"]["usernames"] == ["alice"]
    assert "nickname_keywords" in context["policy"]
    assert not store.black and not store.white
    guard.ban_history.revoke.assert_awaited_once_with(CHAT, USER, 999)
    await ai.run_once()
    assert ai.classifier.await_count == 1
    assert {key: row["message_id"] for key, row in store.notifications.items()} == {
        key: row["message_id"] for key, row in notices.items()
    }


@pytest.mark.parametrize(
    "changes",
    [
        {"verdict": "uncertain"},
        {"verdict": "clear"},
        {"confidence": 0.94},
        {"evidence": []},
        {"evidence": [{"source": "bio", "quote": "幻觉证据"}]},
        {"category": "rule_match"},
        {"category": "other"},
    ],
)
async def test_uncertain_invalid_or_ungrounded_verdict_stays_manual(guard, store, ai, changes):
    ai.classifier.return_value = decision(**changes)
    case = await queue(guard, store)
    await ai.run_once()
    result = await store.case(case["id"])
    assert result["action"] == "report" and result["status"] == "pending"
    assert result["payload"]["ai_report_review"]["state"] == "manual"
    assert not any(call[0] in {"ban", "unban", "delete", "restrict"} for call in guard.client.calls)


async def test_enabled_matching_rule_controls_action_instead_of_ai_default(guard, store, ai):
    guard.group(CHAT).message_filter = True
    guard.group(CHAT).message_keywords = ["广告"]
    guard.group(CHAT).message_action = "warn"
    case = await queue(guard, store)
    await ai.run_once()
    result = await store.case(case["id"])
    assert result["action"] == "warn" and result["status"] == "done"
    assert result["payload"]["ai_report_review"]["action"] == "warn"
    assert not any(call[0] == "ban" for call in guard.client.calls)


@pytest.mark.parametrize(
    "change", ["human", "disable", "policy", "whitelist", "admin", "message", "profile"]
)
async def test_manual_handling_or_changed_evidence_during_ai_call_wins(guard, store, ai, change):
    case = await queue(guard, store)
    started, release = asyncio.Event(), asyncio.Event()

    async def model(context):
        assert not guard.repository.locks[USER].locked()
        started.set()
        await release.wait()
        return decision()

    ai.classifier = model
    running = asyncio.create_task(ai.run_once())
    await asyncio.wait_for(started.wait(), 2)
    if change == "human":
        await guard.reviews.review(case["id"], "unban", ADMIN)
    elif change == "disable":
        guard.group(CHAT).ai_report_review_enabled = False
    elif change == "policy":
        guard.group(CHAT).ai_report_review_action = "delete"
    elif change == "whitelist":
        store.white.add((CHAT, USER))
    elif change == "admin":
        guard.client.admins.add(USER)
    elif change == "message":
        guard.client.source_messages[CHAT, 42].message = "已修改"
    else:
        guard.client.users[USER].first_name = "新名字"
    release.set()
    await running
    assert not any(call[0] == "ban" for call in guard.client.calls)
    assert (await store.case(case["id"]))["status"] == (
        "ignored" if change == "human" else "pending"
    )
    assert ai.jobs.rows[case["id"]]["status"] == "manual"


async def test_failures_retry_three_times_without_indefinite_processing(guard, store, ai):
    case = await queue(guard, store)
    ai.classifier.side_effect = TimeoutError("never store provider secrets")
    for _ in range(3):
        await ai.run_once()
        ai.jobs.rows[case["id"]]["retry_at"] = utc_now_naive()
    assert ai.classifier.await_count == 3
    assert ai.jobs.rows[case["id"]]["status"] == "manual"
    result = await store.case(case["id"])
    assert result["action"] == "report" and result["status"] == "pending"
    assert "never store" not in str(result)


async def test_expired_lease_recovers_after_restart(guard, store, ai):
    case = await queue(guard, store)
    job = ai.jobs.rows[case["id"]]
    job.update(status="running", attempts=1, retry_at=utc_now_naive() - timedelta(seconds=1))
    restarted = AIReportReview(guard)
    restarted.jobs, restarted.classifier = ai.jobs, ai.classifier
    guard.ai_reports = restarted
    await restarted.run_once()
    assert restarted.jobs.rows[case["id"]]["status"] == "applied"


async def test_ai_never_kicks_a_blacklisted_user_or_unbans_an_existing_ban(guard, store, ai):
    guard.group(CHAT).ai_report_review_action = "kick"
    store.black[USER] = {"user_id": USER}
    case = await queue(guard, store)
    await ai.run_once()
    assert (await store.case(case["id"]))["action"] == "report"
    assert not any(call[0] in {"ban", "unban"} for call in guard.client.calls)


async def test_an_external_ban_prevents_ai_from_executing_an_old_report(guard, store, ai):
    case = await queue(guard, store)
    guard.client.member_rights[CHAT, USER]["view_messages"] = True
    guard.client.ban_actors[CHAT, USER] = ADMIN
    await ai.run_once()
    ai.classifier.assert_not_awaited()
    assert (await store.case(case["id"]))["action"] == "report"


@pytest.mark.parametrize(
    "finish_reason,content,valid",
    [
        ("stop", None, False),
        ("length", "{}", False),
        ("stop", "not json", False),
        (
            "stop",
            '{"verdict":"violation","confidence":0.99,"category":"spam","reason":"广告","evidence":[{"source":"message","quote":"广告"}]}',
            True,
        ),
    ],
)
async def test_model_gateway_uses_current_runtime_and_rejects_incomplete_json(
    monkeypatch, finish_reason, content, valid
):
    import core.ai.ai_client as gateway
    from core.moderation.ai_report_classifier import classify

    create = AsyncMock(
        return_value=SimpleNamespace(
            choices=[
                SimpleNamespace(
                    finish_reason=finish_reason, message=SimpleNamespace(content=content)
                )
            ]
        )
    )
    configured = SimpleNamespace(chat=SimpleNamespace(completions=SimpleNamespace(create=create)))
    client = SimpleNamespace(with_options=lambda **kwargs: configured)
    monkeypatch.setattr(gateway, "get_async_ai_runtime", lambda: (client, "updated-model"))
    if valid:
        assert (await classify({"sample": "synthetic"}))["model"] == "updated-model"
    else:
        with pytest.raises(ValueError):
            await classify({"sample": "synthetic"})
    request = create.await_args.kwargs
    assert request["model"] == "updated-model" and request["response_format"] == {
        "type": "json_object"
    }
    assert request["messages"][1]["role"] == "user"


async def test_notification_escapes_model_text_and_shows_decision(guard, store, ai):
    case = await queue(guard, store)
    ai.classifier.return_value = decision(verdict="uncertain", reason="<b>不确定</b>")
    await ai.run_once()
    text = notification_text(await store.case(case["id"]), link_profile=True)
    assert "&lt;b&gt;不确定&lt;/b&gt;" in text and "继续等待管理员" in text


def test_verdict_schema_rejects_actions_targets_nonfinite_and_extra_fields():
    for changes in ({"action": "blacklist"}, {"user_id": 99}, {"confidence": float("nan")}):
        with pytest.raises(ValueError):
            Verdict.model_validate(decision(**changes)["verdict"])
