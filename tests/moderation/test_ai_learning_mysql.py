# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
"""真实 MySQL 的学习样本幂等、任务租约、规则合并与命中审计。"""

from unittest.mock import AsyncMock

import pytest

from core.infrastructure.database.mysql import utc_now_naive
from core.moderation.ai_learning_repository import (
    AILearningJobs,
    AIRuleFeedback,
    AIRules,
    AISamples,
)

from .conftest import ADMIN, CHAT, USER
from .test_mysql import TEST_DSN
from .test_mysql import mysql_repository as mysql_repository

pytestmark = [
    pytest.mark.integration,
    pytest.mark.database,
    pytest.mark.skipif(not TEST_DSN, reason="GUARD_MYSQL_TEST_DSN not configured"),
]


@pytest.fixture
def stores(guard, mysql_repository):
    guard.config.ai_learning.enabled = True
    guard.group(CHAT).ai_learning_enabled = True
    guard.learning.samples = AISamples(mysql_repository)
    guard.learning.jobs = AILearningJobs(mysql_repository)
    guard.learning.rules = AIRules(mysql_repository)
    guard.learning.feedback = AIRuleFeedback(mysql_repository)
    guard.learning.extractor = AsyncMock(return_value={"model": "test-model", "candidates": []})
    return guard.learning


async def admin_submit(learning, *, message_id=42, sender=ADMIN):
    from datetime import UTC, datetime
    from types import SimpleNamespace

    original = SimpleNamespace(
        id=message_id,
        chat_id=CHAT,
        sender_id=USER,
        message="加我微信秒进福利群",
        action=None,
        media=None,
        date=datetime(2026, 11, 1, tzinfo=UTC),
    )
    reporter = SimpleNamespace(
        id=sender, first_name="管理员", last_name=None, username="admin", bot=False
    )
    user = SimpleNamespace(id=USER, first_name="成员", last_name=None, username="u")
    return await learning.submit_admin_report(CHAT, reporter, original, user, "广告引流")


async def test_mysql_admin_report_is_idempotent_and_enqueues_once(guard, stores, mysql_repository):
    first, created = await admin_submit(stores)
    second, again = await admin_submit(stores)
    assert created and not again and first["id"] == second["id"]
    assert first["payload"]["reporters"][0]["user_id"] == ADMIN
    async with mysql_repository.lock(USER) as store:
        assert len((await stores.jobs.list())["rows"]) == 1
        job = await stores.jobs.get(store, first["id"])
        assert job and job["status"] == "queued"
        # 不同管理员重复上报：样本不重建，上报人累计。
        third, again = await admin_submit(stores, sender=11)
        assert not again
        assert len(third["payload"]["reporters"]) == 2
        assert len((await stores.jobs.list())["rows"]) == 1


async def test_mysql_learning_job_lease_is_exclusive_and_restart_recovers(stores, mysql_repository):
    sample, _ = await admin_submit(stores)
    candidate = (await stores.jobs.due())[0]
    other = AILearningJobs(mysql_repository)
    async with mysql_repository.lock(USER) as store:
        first = await stores.jobs.claim(store, candidate)
        assert first and not await other.claim(store, candidate)
        assert not await stores.jobs.save(store, candidate, "done", {})
        assert (await stores.jobs.get(store, sample["id"]))["token"] == first["token"]
    # 租约到期后另一实例可接管并递增尝试次数。
    async with mysql_repository.lock(USER) as store:
        await store.execute(
            "UPDATE moderation_ai_learning_jobs SET retry_at=%s WHERE sample_id=%s",
            (utc_now_naive(), sample["id"]),
        )
    async with mysql_repository.lock(USER) as store:
        second = await other.claim(store, first)
        assert second and second["token"] != first["token"]
        assert second["attempts"] == first["attempts"] + 1


async def test_mysql_rule_upsert_merges_support_and_refreshes_stats(stores, mysql_repository):
    sample, _ = await admin_submit(stores)
    rule = {
        "id": "a" * 32,
        "rule_key": "b" * 64,
        "chat_id": CHAT,
        "rule_type": "message",
        "match_kind": "keyword",
        "value": "加我微信秒进",
        "normalized_value": "加我微信秒进",
        "action": "delete",
        "status": "candidate",
        "confidence": 0.9,
        "explanation": "重复广告",
        "sample_ids": [sample["id"]],
    }
    async with mysql_repository.lock(USER) as store:
        row = await stores.rules.upsert(store, rule)
        assert row["sample_ids"] == [sample["id"]]
        # 同 key 再次提交：合并样本而不是新建规则。
        rule["id"] = "c" * 32
        rule["sample_ids"] = [sample["id"], "d" * 32]
        merged = await stores.rules.upsert(store, rule)
        assert merged["id"] == row["id"] and len(merged["sample_ids"]) == 2
        listing = await stores.rules.list(chat_id=CHAT)
        assert listing["total"] == 1
        # 样本转正后统计随之更新。
        sample_row = await stores.samples.get(store, sample["id"])
        await stores.samples.save(store, sample_row, label="positive")
        linked = await stores.samples.by_ids(store, merged["sample_ids"])
        await stores.rules.refresh_stats(store, merged, linked)
        refreshed = await stores.rules.get(store, merged["id"])
        assert refreshed["positives"] == 1 and refreshed["negatives"] == 0


async def test_mysql_hit_audit_and_correction_pause_the_rule(stores, mysql_repository):
    sample, _ = await admin_submit(stores)
    rule = {
        "id": "a" * 32,
        "rule_key": "b" * 64,
        "chat_id": CHAT,
        "rule_type": "link",
        "match_kind": "domain",
        "value": "spam.example.top",
        "normalized_value": "spam.example.top",
        "action": "delete",
        "status": "active",
        "confidence": 0.9,
        "explanation": "重复引流域名",
        "sample_ids": [sample["id"]],
    }
    async with mysql_repository.lock(USER) as store:
        row = await stores.rules.upsert(store, rule)
        await stores.rules.record_hit(
            store, row, CHAT, USER, "e" * 32, "delete", {"target": "ai_link"}
        )
        hits = await stores.rules.hits_for(store, CHAT, USER)
        assert len(hits) == 1 and hits[0]["rule_id"] == row["id"]
        assert hits[0]["evidence"]["target"] == "ai_link"
        assert (await stores.rules.get(store, row["id"]))["hits"] == 1
        # 人工纠正两次达到阈值后规则自动暂停并可追溯。
        stores.settings().negative_pause_threshold = 2
        case = {"chat_id": CHAT, "user_id": USER, "id": "e" * 32}
        await stores.correct_rules_for_user(store, case, "telegram:10")
        await stores.correct_rules_for_user(store, case, "telegram:10")
        paused = await stores.rules.get(store, row["id"])
        assert paused["status"] == "disabled" and paused["corrections"] == 2
        assert paused["version"] == 2
        feedback = await stores.feedback.list(rule_id=row["id"])
        assert feedback["total"] == 2 and all(
            row_["polarity"] == "negative" for row_ in feedback["rows"]
        )


async def test_mysql_expired_rules_stop_matching_after_cache_refresh(stores, mysql_repository):
    from datetime import timedelta

    from core.moderation.ai_learning import AIRuleCache

    rule = {
        "id": "a" * 32,
        "rule_key": "b" * 64,
        "chat_id": CHAT,
        "rule_type": "message",
        "match_kind": "keyword",
        "value": "过期关键词",
        "normalized_value": "过期关键词",
        "action": "delete",
        "status": "active",
        "confidence": 0.9,
        "explanation": "测试",
        "sample_ids": [],
    }
    async with mysql_repository.lock(USER) as store:
        row = await stores.rules.upsert(store, rule)
        await stores.rules.set_status(store, row, "active", activate=True, expires_days=1)
        cache = AIRuleCache()
        await cache.refresh(stores.rules, force=True)
        assert cache.entries[CHAT]
        await store.execute(
            "UPDATE moderation_ai_rules SET expires_at=%s WHERE id=%s",
            (utc_now_naive() - timedelta(days=1), row["id"]),
        )
    changed = await stores.rules.expire_rules()
    assert changed == 1
    await cache.refresh(stores.rules, force=True)
    assert not cache.entries


async def test_mysql_shared_scopes_load_and_explicit_switch(stores, mysql_repository):
    from core.moderation.ai_learning import AIRuleCache
    from core.moderation.moderator import GuardError

    other_chat = -1009999999999
    sample, _ = await admin_submit(stores)
    rule = {
        "id": "a" * 32,
        "rule_key": "b" * 64,
        "chat_id": CHAT,
        "rule_type": "message",
        "match_kind": "keyword",
        "value": "共享关键词",
        "normalized_value": "共享关键词",
        "action": "delete",
        "status": "active",
        "confidence": 0.9,
        "explanation": "跨群重复广告",
        "sample_ids": [sample["id"]],
    }
    async with mysql_repository.lock(USER) as store:
        row = await stores.rules.upsert(store, rule)
        # 全局关闭时拒绝共享设置。
        with pytest.raises(GuardError):
            await stores.set_rule_shares(store, row, [other_chat], "webui:admin")
        stores.settings().shared_rules_enabled = True
        shared = await stores.set_rule_shares(store, row, [other_chat, other_chat], "webui:admin")
        assert shared == [other_chat]
        assert await stores.rules.scopes(store, row["id"]) == [other_chat]
        # 共享开关传入后缓存为两个群装配同一规则；未开启时不装配目标群。
        cache = AIRuleCache()
        await cache.refresh(stores.rules, force=True)
        assert other_chat not in cache.entries
        await cache.refresh(stores.rules, force=True, shared_enabled=True)
        assert cache.entries[CHAT] and cache.entries[other_chat]
        assert cache.entries[other_chat][0]["id"] == row["id"]
        scopes = await stores.feedback.list(rule_id=row["id"])
        assert any(item["kind"] == "scope_change" for item in scopes["rows"])


async def test_mysql_expiry_transition_leaves_lifecycle_audit(stores, mysql_repository):
    from datetime import timedelta

    from core.moderation.ai_learning import AIRuleCache

    sample, _ = await admin_submit(stores)
    rule = {
        "id": "a" * 32,
        "rule_key": "b" * 64,
        "chat_id": CHAT,
        "rule_type": "message",
        "match_kind": "keyword",
        "value": "到期关键词",
        "normalized_value": "到期关键词",
        "action": "delete",
        "status": "active",
        "confidence": 0.9,
        "explanation": "测试",
        "sample_ids": [sample["id"]],
    }
    async with mysql_repository.lock(USER) as store:
        row = await stores.rules.upsert(store, rule)
        await stores.transition_rule(
            store, row, "active", actor="webui:admin", activate=True, bump_version=False
        )
    expiring = await stores.rules.expiring()
    assert not expiring
    async with mysql_repository.lock(USER) as store:
        await store.execute(
            "UPDATE moderation_ai_rules SET expires_at=%s WHERE id=%s",
            (utc_now_naive() - timedelta(days=1), row["id"]),
        )
    expiring = await stores.rules.expiring()
    assert [item["id"] for item in expiring] == [row["id"]]
    async with mysql_repository.pool.acquire() as connection:
        from core.moderation.repository import GuardStore

        await stores.transition_rule(
            GuardStore(connection), expiring[0], "expired", actor="system:expiry"
        )
    async with mysql_repository.lock(USER) as store:
        expired = await stores.rules.get(store, row["id"])
        assert expired["status"] == "expired"
        audit = await stores.feedback.list(rule_id=row["id"])
        kinds = [item["kind"] for item in audit["rows"]]
        assert "lifecycle" in kinds
    cache = AIRuleCache()
    await cache.refresh(stores.rules, force=True)
    assert not cache.entries
