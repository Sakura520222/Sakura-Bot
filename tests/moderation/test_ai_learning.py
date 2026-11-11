# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
"""AI 自适应风控：样本入口、反馈闭环、候选规则与实时缓存匹配。"""

from datetime import UTC, datetime
from types import SimpleNamespace
from unittest.mock import AsyncMock

import pytest
from telethon import events

from core.moderation.ai_rule_extractor import sanitize_candidates

from .ai_learning_fakes import wired
from .conftest import ADMIN, CHAT, USER

REPORTER = 300
SECOND_ADMIN = 11


@pytest.fixture
def learning(guard, store):
    guard.config.ai_learning.enabled = True
    guard.group(CHAT).ai_learning_enabled = True
    fake = wired(guard)
    guard.learning.extractor = AsyncMock(return_value={"model": "test-model", "candidates": []})
    fake.extractor = guard.learning.extractor
    return fake


def submit_event(guard, *, text="@admin 上报 引流广告", sender=ADMIN, message_id=42, chat_id=CHAT):
    original = SimpleNamespace(
        id=message_id,
        chat_id=chat_id,
        sender_id=USER,
        message="加我微信秒进福利群",
        action=None,
        media=None,
        date=datetime(2026, 11, 1, tzinfo=UTC),
    )
    guard.client.source_messages[chat_id, message_id] = original
    return SimpleNamespace(
        chat_id=chat_id,
        chat=None,
        sender_id=sender,
        is_group=True,
        id=message_id + 100,
        message=SimpleNamespace(
            id=message_id + 100, message=text, reply_to_msg_id=message_id, edit_date=None
        ),
        get_reply_message=AsyncMock(return_value=original),
        reply=AsyncMock(),
    )


async def submit(guard, event=None, **kwargs):
    event = event or submit_event(guard, **kwargs)
    with pytest.raises(events.StopPropagation):
        await guard.learning.handle_report(event)
    return event


def only_sample(fake):
    assert len(fake.samples.rows) == 1
    return next(iter(fake.samples.rows.values()))


# ── 管理员 @admin 上报 ──


async def test_admin_report_creates_sample_and_job_without_punishing(guard, store, learning):
    event = await submit(guard)
    sample = only_sample(learning)
    assert sample["source"] == "admin_report" and sample["label"] == "pending"
    assert sample["user_id"] == USER and sample["chat_id"] == CHAT
    assert sample["payload"]["reporters"][0]["user_id"] == ADMIN
    assert sample["payload"]["message"]["text"] == "加我微信秒进福利群"
    assert sample["id"][:12] in event.reply.call_args.args[0]
    assert (
        sample["id"] in learning.jobs.rows
        and learning.jobs.rows[sample["id"]]["status"] == "queued"
    )
    # 上报只入库：不创建案件、不处罚、不通知管理员。
    assert not store.cases and not guard.client.calls and not store.notifications


async def test_duplicate_admin_reports_are_idempotent_but_accumulate_reporters(guard, learning):
    await submit(guard)
    await submit(guard, sender=SECOND_ADMIN, text="@admin 上报 换个管理员再报")
    sample = only_sample(learning)
    assert [item["user_id"] for item in sample["payload"]["reporters"]] == [ADMIN, SECOND_ADMIN]
    assert len(learning.jobs.rows) == 1
    third = await submit(guard, sender=SECOND_ADMIN)
    assert len(learning.jobs.rows) == 1
    assert "已有学习样本" in third.reply.call_args.args[0]


async def test_admin_report_requires_explicit_authorization(guard, learning):
    guard.client.users[REPORTER] = SimpleNamespace(
        id=REPORTER, first_name="成员", last_name=None, username=None, bot=False
    )
    event = await submit(guard, sender=REPORTER)
    assert "权限" in event.reply.call_args.args[0] and not learning.samples.rows
    # 群管理员仅在显式开启“授权本群管理员审核”后才可上报。
    guard.group(CHAT).allow_group_admin_review = True
    guard.client.admins.add(REPORTER)
    await submit(guard, sender=REPORTER, text="@admin 上报")
    assert len(learning.samples.rows) == 1


@pytest.mark.parametrize(
    "kind", ["no_reply", "self", "main_bot", "channel_identity", "other_chat", "service"]
)
async def test_invalid_replies_are_rejected_without_samples(guard, learning, kind):
    event = submit_event(guard)
    original = event.get_reply_message.return_value
    if kind == "no_reply":
        event.message.reply_to_msg_id = None
    elif kind in {"self", "main_bot", "channel_identity"}:
        original.sender_id = {"self": ADMIN, "main_bot": guard.bot_id, "channel_identity": CHAT}[
            kind
        ]
    elif kind == "other_chat":
        original.chat_id = CHAT - 1
    else:
        original.action = object()
    await submit(guard, event)
    expected = "请引用" if kind == "no_reply" else "无法上报"
    assert not learning.samples.rows and expected in event.reply.call_args.args[0]


async def test_admin_report_rejected_when_learning_disabled(guard, learning):
    guard.config.ai_learning.enabled = False
    event = await submit(guard)
    assert "未启用" in event.reply.call_args.args[0] and not learning.samples.rows
    guard.config.ai_learning.enabled = True
    guard.config.ai_learning.sources = ["member_report"]
    event = await submit(guard)
    assert "未启用" in event.reply.call_args.args[0] and not learning.samples.rows


@pytest.mark.parametrize("text", ["@admin 举报 广告", "@admin", "上报", "@admin 上报人"])
async def test_unrelated_text_does_not_trigger_learning(guard, learning, text):
    event = submit_event(guard, text=text)
    await guard.learning.handle_report(event)
    assert not learning.samples.rows and not event.reply.called


# ── 成员举报样本与审核反馈 ──


async def test_member_reports_become_pending_samples_linked_to_cases(guard, store, learning):
    from .test_reports import report_event

    event = report_event(guard)
    with pytest.raises(events.StopPropagation):
        await guard.reports.handle(event)
    sample = only_sample(learning)
    assert sample["source"] == "member_report" and sample["case_id"] in store.cases
    assert sample["label"] == "pending" and sample["payload"]["reporters"][0]["user_id"] == 300
    # 同一原消息由管理员补充上报时不重复建样本，只补上报人。
    await submit(guard)
    assert len(learning.samples.rows) == 1
    assert len(sample["payload"]["reporters"]) == 2


async def test_member_reports_do_not_sample_when_disabled(guard, store, learning):
    from .test_reports import report_event

    guard.config.ai_learning.enabled = False
    event = report_event(guard)
    with pytest.raises(events.StopPropagation):
        await guard.reports.handle(event)
    assert len(store.cases) == 1 and not learning.samples.rows


@pytest.mark.parametrize("action,label", [("blacklist", "positive"), ("unban", "negative")])
async def test_review_outcomes_update_sample_labels(guard, store, learning, action, label):
    from .test_reports import send

    await send(guard)
    case = next(row for row in store.cases.values() if row["action"] == "report")
    await guard.reviews.review(case["id"], action, ADMIN)
    sample = only_sample(learning)
    assert sample["label"] == label
    feedback = [row for row in learning.feedback.rows if row["sample_id"] == sample["id"]]
    assert feedback and feedback[-1]["kind"] == f"review_{action}"
    assert feedback[-1]["polarity"] == label


async def test_delete_only_review_is_recorded_as_neutral(guard, store, learning):
    case = {
        "id": "c" * 32,
        "chat_id": CHAT,
        "user_id": USER,
        "action": "report",
        "status": "pending",
        "payload": {"message_id": 42, "evidence": {}},
    }
    sample, _ = await learning.samples.create(
        store,
        {
            "id": "s" * 32,
            "idempotency_key": "k" * 64,
            "chat_id": CHAT,
            "user_id": USER,
            "message_id": 42,
            "source": "member_report",
            "case_id": case["id"],
            "payload": {"reporters": []},
        },
    )
    result = {**case, "status": "pending", "payload": {**case["payload"], "review_history": []}}
    await guard.learning.record_review_outcome(store, case, result, action="delete")
    assert learning.samples.rows[sample["id"]]["label"] == "neutral"


async def test_ai_review_confirms_positive_only_when_applied(guard, store, learning):
    from .test_reports import send

    await send(guard)
    case = next(row for row in store.cases.values() if row["action"] == "report")
    sample = only_sample(learning)
    await guard.learning.record_ai_review(store, {**case, "status": "pending"})
    assert learning.samples.rows[sample["id"]]["label"] == "positive"


async def test_whitelist_flips_positive_samples_and_corrects_hit_rules(guard, store, learning):
    sample, _ = await learning.samples.create(
        store,
        {
            "id": "s" * 32,
            "idempotency_key": "k" * 64,
            "chat_id": CHAT,
            "user_id": USER,
            "message_id": 42,
            "source": "admin_report",
            "case_id": None,
            "label": "positive",
            "payload": {"reporters": []},
        },
    )
    rule = await learning.rules.upsert(
        store,
        {
            "id": "r" * 32,
            "rule_key": "a" * 64,
            "chat_id": CHAT,
            "rule_type": "message",
            "match_kind": "keyword",
            "value": "加我微信秒进",
            "normalized_value": "加我微信秒进",
            "action": "delete",
            "status": "active",
            "confidence": 0.9,
            "explanation": "重复广告",
            "sample_ids": [sample["id"]],
        },
    )
    await learning.rules.record_hit(
        store, rule, CHAT, USER, "c" * 32, "delete", {"target": "ai_message"}
    )
    await guard.learning.record_whitelist(store, CHAT, USER, "webui:admin")
    assert learning.samples.rows[sample["id"]]["label"] == "negative"
    assert learning.rules.rows[rule["id"]]["corrections"] == 1


# ── 学习任务：提取、校验、激活与降权 ──


def candidate(value="加我微信秒进", **changes):
    return {
        "rule_type": "message",
        "match_kind": "keyword",
        "value": value,
        "confidence": 0.9,
        "evidence_sample_ids": ["s" * 32],
        "explanation": "多账号重复发送同一段广告",
        **changes,
    }


async def test_learning_job_creates_candidate_rules_and_dedupes(guard, store, learning):
    await submit(guard)
    sample = only_sample(learning)
    learning.extractor.return_value = {
        "model": "test-model",
        "candidates": [candidate(evidence_sample_ids=[sample["id"]])],
    }
    await guard.learning.process((await learning.jobs.due())[0])
    rule = next(iter(learning.rules.rows.values()))
    assert rule["status"] == "candidate" and rule["chat_id"] == CHAT
    assert rule["sample_ids"] == [sample["id"]] and rule["action"] == "delete"
    assert learning.samples.rows[sample["id"]]["status"] == "learned"
    assert learning.jobs.rows[sample["id"]]["status"] == "done"
    # 另一条同文案样本再次学习：合并支持样本而不是生成第二条规则。
    event = submit_event(guard, message_id=43)
    await submit(guard, event)
    other = next(row for row in learning.samples.rows.values() if row["message_id"] == 43)
    learning.extractor.return_value["candidates"] = [candidate(evidence_sample_ids=[other["id"]])]
    await guard.learning.process((await learning.jobs.due())[0])
    assert len(learning.rules.rows) == 1
    assert sorted(rule["sample_ids"]) == sorted([sample["id"], other["id"]])


async def test_extractor_failure_retries_and_keeps_the_sample(guard, store, learning):
    from core.infrastructure.database.mysql import utc_now_naive

    await submit(guard)
    sample = only_sample(learning)
    learning.extractor.side_effect = TimeoutError
    await guard.learning.process((await learning.jobs.due())[0])
    job = learning.jobs.rows[sample["id"]]
    assert job["status"] == "queued" and job["attempts"] == 1 and job["error"] == "TimeoutError"
    assert learning.samples.rows[sample["id"]]["status"] == "queued"
    learning.extractor.side_effect = None
    # 退避到期后同一任务恢复执行；样本在成功前不会被丢弃。
    learning.jobs.rows[sample["id"]]["retry_at"] = utc_now_naive()
    await guard.learning.process((await learning.jobs.due())[0])
    assert learning.jobs.rows[sample["id"]]["status"] == "done"


async def test_manual_activation_policy_keeps_candidates(guard, store, learning):
    from .test_reports import send

    await send(guard)
    sample = only_sample(learning)
    learning.samples.rows[sample["id"]]["label"] = "positive"
    learning.extractor.return_value = {
        "model": "test-model",
        "candidates": [candidate(evidence_sample_ids=[sample["id"]])],
    }
    await guard.learning.process((await learning.jobs.due())[0])
    rule = next(iter(learning.rules.rows.values()))
    assert rule["status"] == "candidate" and rule["positives"] == 1


async def test_auto_activation_requires_configured_thresholds(guard, store, learning):
    from .test_reports import send

    settings = guard.config.ai_learning
    settings.activation = "auto"
    settings.min_positive_samples = 1
    settings.min_reporters = 2
    settings.min_target_users = 1
    await send(guard)
    sample = only_sample(learning)
    learning.samples.rows[sample["id"]]["label"] = "positive"
    learning.extractor.return_value = {
        "model": "test-model",
        "candidates": [candidate(evidence_sample_ids=[sample["id"]])],
    }
    await guard.learning.process((await learning.jobs.due())[0])
    rule = next(iter(learning.rules.rows.values()))
    # 单一举报人不足以自动激活。
    assert rule["status"] == "candidate" and rule["reporters"] == 1
    learning.samples.rows[sample["id"]]["payload"]["reporters"].append({"user_id": SECOND_ADMIN})
    await learning.rules.refresh_stats(store, rule, [learning.samples.rows[sample["id"]]])
    activated = await guard.learning._maybe_activate(store, rule)
    assert activated and learning.rules.rows[rule["id"]]["status"] == "active"


async def test_negative_feedback_pauses_active_rules(guard, store, learning):
    settings = guard.config.ai_learning
    settings.negative_pause_threshold = 1
    sample, _ = await learning.samples.create(
        store,
        {
            "id": "s" * 32,
            "idempotency_key": "k" * 64,
            "chat_id": CHAT,
            "user_id": USER,
            "message_id": 42,
            "source": "member_report",
            "case_id": "c" * 32,
            "label": "positive",
            "payload": {"reporters": [{"user_id": REPORTER}]},
        },
    )
    rule = await learning.rules.upsert(
        store,
        {
            "id": "r" * 32,
            "rule_key": "a" * 64,
            "chat_id": CHAT,
            "rule_type": "message",
            "match_kind": "keyword",
            "value": "加我微信秒进",
            "normalized_value": "加我微信秒进",
            "action": "delete",
            "status": "active",
            "confidence": 0.9,
            "explanation": "重复广告",
            "sample_ids": [sample["id"]],
        },
    )
    case = {
        "id": "c" * 32,
        "chat_id": CHAT,
        "user_id": USER,
        "action": "report",
        "status": "ignored",
        "payload": {"message_id": 42},
    }
    result = {
        **case,
        "status": "ignored",
        "payload": {**case["payload"], "review_history": [{"action": "unban"}]},
    }
    await guard.learning.record_review_outcome(store, case, result)
    assert learning.samples.rows[sample["id"]]["label"] == "negative"
    assert learning.rules.rows[rule["id"]]["status"] == "disabled"
    assert learning.rules.rows[rule["id"]]["error"] == "negative_feedback_threshold"


# ── 候选安全校验 ──


def fake_context(sample_ids):
    return {"samples": [{"id": sid} for sid in sample_ids]}


def test_sanitize_candidates_validates_evidence_values_and_regexes():
    context = fake_context(["s" * 32])
    approved = sanitize_candidates(
        {
            "candidates": [
                candidate(),
                candidate(value="加", evidence_sample_ids=["missing"]),  # 证据不存在
                candidate(value="a"),  # 归一化后过短
                candidate(match_kind="regex", value="(\\d+){3}"),  # 捕获组正则
                candidate(match_kind="regex", value="加我.{10,20}秒进"),  # 限量正则
                candidate(match_kind="domain", value="https://t.me/evil/invite"),  # 非域名
                candidate(match_kind="domain", value="https://spam.example.top/landing"),
                candidate(value="正常讨论"),  # 与前一个 key 不同，通过
            ]
        },
        context,
    )
    assert [(item["value"], item["match_kind"]) for item in approved] == [
        ("加我微信秒进", "keyword"),
        ("加我.{10,20}秒进", "regex"),
        ("spam.example.top", "domain"),
        ("正常讨论", "keyword"),
    ]
    assert all(item["normalized_value"] for item in approved)


# ── 实时缓存匹配与命中记录 ──


async def test_cached_rules_match_messages_and_record_hits(guard, store, learning):

    rule = await learning.rules.upsert(
        store,
        {
            "id": "r" * 32,
            "rule_key": "a" * 64,
            "chat_id": CHAT,
            "rule_type": "message",
            "match_kind": "keyword",
            "value": "加我微信秒进",
            "normalized_value": "加我微信秒进",
            "action": "delete",
            "status": "active",
            "confidence": 0.9,
            "explanation": "重复广告",
            "sample_ids": [],
        },
    )
    await guard.learning.cache.refresh(learning.rules, force=True)
    # 插入空格的规避写法经归一化后仍可命中规则。
    message = SimpleNamespace(message="前缀 加我微信 秒进福利群 后缀", id=77)
    match = guard.learning.match_rule(guard.group(CHAT), CHAT, guard.client.users[USER], message)
    assert match and match.ai_rule == {"id": rule["id"], "version": 1}
    assert match.rule_id == f"ai-{rule['id'][:12]}" and match.action == "delete"
    # 手工规则优先：同样的内容命中手工关键词时不落到 AI 规则。
    config = guard.group(CHAT)
    config.message_filter = True
    config.message_keywords = ["加我"]
    manual = await guard.match(store, config, CHAT, guard.client.users[USER], message)
    assert manual.rule_id == "message-word-0" and not manual.ai_rule
    config.message_keywords = []
    config.message_filter = False
    # 命中后进入现有 punish 流程，并留下命中审计。
    ai_match = await guard.match(store, config, CHAT, guard.client.users[USER], message)
    case = await guard.punish(store, CHAT, guard.client.users[USER], ai_match, "learn-hit", message)
    assert case["payload"]["evidence"]["ai_rule"]["id"] == rule["id"]
    assert learning.rules.rows[rule["id"]]["hits"] == 1
    assert learning.rules.hits[0]["case_id"] == case["id"]
    assert learning.rules.hits[0]["action"] == "delete"
    # 学习关闭后缓存不再参与匹配，手工规则不受影响。
    guard.config.ai_learning.enabled = False
    assert guard.learning.match_rule(config, CHAT, guard.client.users[USER], message) is None
    config.message_filter = True
    config.message_keywords = ["加我"]
    manual = await guard.match(store, config, CHAT, guard.client.users[USER], message)
    assert manual and manual.rule_id == "message-word-0" and not manual.ai_rule


async def test_rule_expiry_and_cache_refresh(guard, store, learning):
    from datetime import timedelta

    from core.infrastructure.database.mysql import utc_now_naive

    rule = await learning.rules.upsert(
        store,
        {
            "id": "r" * 32,
            "rule_key": "a" * 64,
            "chat_id": CHAT,
            "rule_type": "message",
            "match_kind": "keyword",
            "value": "过期规则",
            "normalized_value": "过期规则",
            "action": "delete",
            "status": "active",
            "confidence": 0.9,
            "explanation": "过期",
            "sample_ids": [],
        },
    )
    learning.rules.rows[rule["id"]]["expires_at"] = utc_now_naive() - timedelta(days=1)
    await guard.learning.cache.refresh(learning.rules, force=True)
    assert not guard.learning.cache.entries
    assert learning.rules.rows[rule["id"]]["status"] == "active"  # 维护任务负责过期
    await learning.rules.expire_rules()
    assert learning.rules.rows[rule["id"]]["status"] == "expired"


# ── P2：跨群共享作用域、流转审计、申诉联动与举报 AI 复核增强 ──

OTHER_CHAT = -1009999999999


async def _active_rule(learning, store, *, chat_id=CHAT):
    return await learning.rules.upsert(
        store,
        {
            "id": "r" * 32,
            "rule_key": "a" * 64,
            "chat_id": chat_id,
            "rule_type": "message",
            "match_kind": "keyword",
            "value": "共享关键词",
            "normalized_value": "共享关键词",
            "action": "delete",
            "status": "active",
            "confidence": 0.9,
            "explanation": "跨群重复广告",
            "sample_ids": [],
        },
    )


async def test_shared_scopes_apply_only_when_explicitly_enabled(guard, store, learning):
    from types import SimpleNamespace

    from core.moderation.config import GuardGroup

    guard.config.groups[str(OTHER_CHAT)] = GuardGroup(enabled=True, ai_learning_enabled=False)
    other_config = guard.config.groups[str(OTHER_CHAT)]
    rule = await _active_rule(learning, store)
    message = SimpleNamespace(message="快来共享关键词领取", id=88)
    other_user = SimpleNamespace(id=USER + 1, first_name="别群成员", last_name=None, username=None)
    # 未显式开启共享：规则只在来源群生效。
    await learning.rules.set_scopes(store, rule, [OTHER_CHAT], "webui:admin")
    await guard.learning.cache.refresh(learning.rules, force=True)
    assert CHAT in guard.learning.cache.entries
    assert OTHER_CHAT not in guard.learning.cache.entries
    # 开启共享后目标群生效；目标群仍需单独开启本群 AI 学习。
    guard.config.ai_learning.shared_rules_enabled = True
    await guard.learning.cache.refresh(learning.rules, force=True, shared_enabled=True)
    assert guard.learning.cache.match(OTHER_CHAT, other_user, message)
    assert guard.learning.match_rule(other_config, OTHER_CHAT, other_user, message) is None
    # 目标群开启本群学习后命中，来源群配置不受影响。
    other_config.ai_learning_enabled = True
    hit = guard.learning.match_rule(other_config, OTHER_CHAT, other_user, message)
    assert hit and hit.ai_rule["id"] == rule["id"]


async def test_set_rule_shares_validates_the_global_switch(guard, store, learning):
    from core.moderation.moderator import GuardError

    rule = await _active_rule(learning, store)
    with pytest.raises(GuardError, match="guard.ai_learning_sharing_disabled"):
        await guard.learning.set_rule_shares(store, rule, [OTHER_CHAT], "webui:admin")
    guard.config.ai_learning.shared_rules_enabled = True
    shared = await guard.learning.set_rule_shares(
        store, rule, [OTHER_CHAT, OTHER_CHAT], "webui:admin"
    )
    assert shared == [OTHER_CHAT]
    assert await learning.rules.scopes(store, rule["id"]) == [OTHER_CHAT]
    assert any(
        row["kind"] == "scope_change" and row["rule_id"] == rule["id"]
        for row in learning.feedback.rows
    )


async def test_rule_transitions_leave_lifecycle_audit(guard, store, learning):
    sample, _ = await learning.samples.create(
        store,
        {
            "id": "s" * 32,
            "idempotency_key": "k" * 64,
            "chat_id": CHAT,
            "user_id": USER,
            "message_id": 42,
            "source": "admin_report",
            "case_id": None,
            "label": "positive",
            "payload": {"reporters": [{"user_id": ADMIN}, {"user_id": SECOND_ADMIN}]},
        },
    )
    rule = await learning.rules.upsert(
        store,
        {
            "id": "r" * 32,
            "rule_key": "a" * 64,
            "chat_id": CHAT,
            "rule_type": "message",
            "match_kind": "keyword",
            "value": "审计关键词",
            "normalized_value": "审计关键词",
            "action": "delete",
            "status": "candidate",
            "confidence": 0.9,
            "explanation": "重复广告",
            "sample_ids": [sample["id"]],
        },
    )
    settings = guard.config.ai_learning
    settings.activation = "auto"
    settings.min_positive_samples = 1
    settings.min_reporters = 2
    settings.min_target_users = 1
    await learning.rules.refresh_stats(store, rule, [learning.samples.rows[sample["id"]]])
    assert await guard.learning._maybe_activate(store, rule)
    assert learning.rules.rows[rule["id"]]["status"] == "active"
    lifecycle = [row for row in learning.feedback.rows if row["kind"] == "lifecycle"]
    assert lifecycle[-1]["payload"]["from"] == "candidate"
    assert lifecycle[-1]["payload"]["to"] == "active"
    assert lifecycle[-1]["actor"] == "system:auto_threshold"
    # 负反馈暂停同样留下审计并可追溯。
    settings.negative_pause_threshold = 1
    learning.samples.rows[sample["id"]]["label"] = "negative"
    await learning.rules.refresh_stats(store, rule, [learning.samples.rows[sample["id"]]])
    await guard.learning._apply_feedback_policy(store, await learning.rules.get(store, rule["id"]))
    assert learning.rules.rows[rule["id"]]["status"] == "disabled"
    transitions = [row["payload"] for row in learning.feedback.rows if row["kind"] == "lifecycle"]
    assert transitions[-1]["to"] == "disabled" and transitions[-1]["negatives"] == 1


async def test_appeal_success_flips_samples_and_corrects_rules(guard, store, learning):
    sample, _ = await learning.samples.create(
        store,
        {
            "id": "s" * 32,
            "idempotency_key": "k" * 64,
            "chat_id": CHAT,
            "user_id": USER,
            "message_id": 42,
            "source": "member_report",
            "case_id": "c" * 32,
            "label": "positive",
            "payload": {"reporters": [{"user_id": REPORTER}]},
        },
    )
    rule = await _active_rule(learning, store)
    await learning.rules.record_hit(
        store, rule, CHAT, USER, "c" * 32, "delete", {"target": "ai_message"}
    )
    case = {"id": "c" * 32, "chat_id": CHAT, "user_id": USER}
    await guard.learning.record_appeal_allowed(store, case, "telegram:10")
    assert learning.samples.rows[sample["id"]]["label"] == "negative"
    assert learning.rules.rows[rule["id"]]["corrections"] == 1
    assert any(row["kind"] == "appeal_allowed" for row in learning.feedback.rows)


async def test_report_context_is_bounded_and_stable(guard, store, learning):
    from datetime import timedelta

    from core.infrastructure.database.mysql import utc_now_naive

    config = guard.group(CHAT)
    case_created = utc_now_naive()
    case = {
        "id": "c" * 32,
        "chat_id": CHAT,
        "user_id": USER,
        "action": "report",
        "created_at": case_created,
    }
    # 学习关闭：空段且不影响原有复核语义。
    guard.config.ai_learning.enabled = False
    empty = await guard.learning.report_context(store, case, config)
    assert empty == {"similar_samples": [], "recent_rule_hits": []}
    guard.config.ai_learning.enabled = True
    await learning.samples.create(
        store,
        {
            "id": "s" * 32,
            "idempotency_key": "k" * 64,
            "chat_id": CHAT,
            "user_id": USER,
            "message_id": 42,
            "source": "member_report",
            "case_id": case["id"],
            "payload": {"reporters": [{"user_id": REPORTER}], "message": {"text": "本案消息"}},
        },
    )
    other, _ = await learning.samples.create(
        store,
        {
            "id": "t" * 32,
            "idempotency_key": "j" * 64,
            "chat_id": CHAT,
            "user_id": USER + 1,
            "message_id": 43,
            "source": "admin_report",
            "case_id": None,
            "label": "positive",
            "payload": {"reporters": [{"user_id": ADMIN}], "message": {"text": "相似广告"}},
        },
    )
    # 案件创建之后的样本不进入上下文，保证复核期间指纹稳定。
    learning.samples.rows[other["id"]]["created_at"] = case_created - timedelta(minutes=1)
    later, _ = await learning.samples.create(
        store,
        {
            "id": "u" * 32,
            "idempotency_key": "i" * 64,
            "chat_id": CHAT,
            "user_id": USER + 2,
            "message_id": 44,
            "source": "admin_report",
            "case_id": None,
            "payload": {"reporters": [{"user_id": ADMIN}], "message": {"text": "迟到样本"}},
        },
    )
    learning.samples.rows[later["id"]]["created_at"] = case_created + timedelta(minutes=1)
    context = await guard.learning.report_context(store, case, config)
    assert [row["id"] for row in context["similar_samples"]] == [other["id"]]
    assert context["similar_samples"][0]["reporters"] == 1
    # 该用户此前的规则命中构成重复行为证据。
    rule = await _active_rule(learning, store)
    await learning.rules.record_hit(
        store, rule, CHAT, USER, "b" * 32, "delete", {"target": "ai_message"}
    )
    learning.rules.hits[-1]["created_at"] = case_created - timedelta(minutes=5)
    context = await guard.learning.report_context(store, case, config)
    assert context["recent_rule_hits"][0]["rule_id"] == rule["id"]


def test_actionable_honors_configured_threshold_and_categories():
    from core.moderation.ai_report_classifier import Verdict, actionable

    verdict = Verdict.model_validate(
        {
            "verdict": "violation",
            "confidence": 0.9,
            "category": "spam",
            "reason": "广告",
            "evidence": [{"source": "message", "quote": "广告"}],
        }
    )
    context = {
        "message": {"text": "广告"},
        "profile": {"nickname": "", "usernames": []},
        "blacklist": {"matched": False, "shared": False},
        "rule_match": None,
    }
    # 缺省保持旧行为：0.95 门槛；阈值放宽后可执行。
    assert not actionable(verdict, context)
    assert actionable(verdict, context, threshold=0.9)
    # 类别白名单收紧后即使置信度达标也不自动执行。
    assert not actionable(verdict, context, threshold=0.9, categories={"scam"})


async def test_duplicate_member_reports_accumulate_reporters(guard, store, learning):
    from .test_reports import report_event, send

    await send(guard)
    second = report_event(guard, reporter=REPORTER + 1)
    with pytest.raises(events.StopPropagation):
        await guard.reports.handle(second)
    # 案件仍去重为一条，但两个举报人都累计进同一条学习样本。
    sample = only_sample(learning)
    assert [item["user_id"] for item in sample["payload"]["reporters"]] == [REPORTER, REPORTER + 1]
    assert len(learning.jobs.rows) == 1


async def test_outcome_recorded_when_review_completes_via_recovery(guard, store, learning):
    from core.moderation.moderator import GuardError

    from .test_reports import send

    await send(guard)
    case = next(row for row in store.cases.values() if row["action"] == "report")
    guard.client.fail["ban"] = RuntimeError("temporary failure")
    with pytest.raises(GuardError):
        await guard.reviews.review(case["id"], "blacklist", ADMIN)
    guard.client.fail.clear()
    await guard.recover_once()
    assert store.cases[case["id"]]["status"] == "blacklisted"
    # 后台恢复完成的审核同样写入正样本反馈，且只记一次。
    assert only_sample(learning)["label"] == "positive"
    assert len([row for row in learning.feedback.rows if row["kind"] == "review_blacklist"]) == 1


async def test_set_rule_action_updates_bumps_version_and_audits(guard, store, learning):
    from core.moderation.moderator import GuardError

    rule = await _active_rule(learning, store)
    assert rule["action"] == "delete" and rule["version"] == 1
    with pytest.raises(GuardError):
        await guard.learning.set_rule_action(store, rule, "explode", "webui:admin")
    updated = await guard.learning.set_rule_action(store, rule, "ban", "webui:admin")
    assert updated["action"] == "ban" and updated["version"] == 2
    changes = [row for row in learning.feedback.rows if row["kind"] == "action_change"]
    assert changes[-1]["payload"] == {"from": "delete", "to": "ban"}
    assert guard.learning.cache.dirty  # 缓存失效，动作变化立即进入下一次刷新


async def test_extractor_truncation_reports_precise_error_and_requeue_recovers(
    guard, store, learning
):
    from core.infrastructure.database.mysql import utc_now_naive
    from core.moderation.ai_rule_extractor import IncompleteAIResponse

    await submit(guard)
    sample = only_sample(learning)
    learning.extractor.side_effect = IncompleteAIResponse("finish=length")
    await guard.learning.process((await learning.jobs.due())[0])
    # 错误列带具体 finish 原因，而不是笼统的 ValueError。
    job = learning.jobs.rows[sample["id"]]
    assert job["error"] == "IncompleteAIResponse:finish=length"
    # 连续失败到上限转人工后，管理员重新排队可恢复（尝试次数清零）。
    for _ in range(4):
        learning.jobs.rows[sample["id"]]["retry_at"] = utc_now_naive()
        await guard.learning.process((await learning.jobs.due())[0])
    assert learning.jobs.rows[sample["id"]]["status"] == "manual"
    assert await learning.jobs.requeue(store, sample["id"])
    job = learning.jobs.rows[sample["id"]]
    assert job["status"] == "queued" and job["attempts"] == 0 and job["error"] is None
    learning.extractor.side_effect = None
    await guard.learning.process((await learning.jobs.due())[0])
    assert learning.jobs.rows[sample["id"]]["status"] == "done"
    # 已完成/排队中的任务不可重复排队。
    assert not await learning.jobs.requeue(store, sample["id"])
