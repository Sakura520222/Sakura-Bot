# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
"""AI 学习存储替身；真实 SQL、事务与租约由 MySQL 集成测试验证。"""

import uuid
from copy import deepcopy
from datetime import timedelta
from types import SimpleNamespace

from core.infrastructure.database.mysql import utc_now_naive


class MemoryAISamples:
    def __init__(self):
        self.rows = {}

    async def create(self, store, sample):
        for row in self.rows.values():
            if row["idempotency_key"] == sample["idempotency_key"]:
                return deepcopy(row), False
        row = deepcopy(
            {
                **sample,
                "label": sample.get("label", "pending"),
                "status": sample.get("status", "new"),
                "error": None,
                "created_at": utc_now_naive(),
                "updated_at": utc_now_naive(),
            }
        )
        self.rows[row["id"]] = row
        return deepcopy(row), True

    async def by_key(self, store, idempotency_key):
        return deepcopy(
            next(
                (row for row in self.rows.values() if row["idempotency_key"] == idempotency_key),
                None,
            )
        )

    async def get(self, store, sample_id):
        return deepcopy(self.rows.get(sample_id))

    async def by_case(self, store, case_id):
        return deepcopy(
            next((row for row in self.rows.values() if row.get("case_id") == case_id), None)
        )

    async def save(self, store, sample, *, label=None, status=None, error=None, payload=None):
        # 与 SQL 实现一致：未显式传值时采用传入样本上的当前字段。
        row = self.rows[sample["id"]]
        row.update(
            label=sample["label"] if label is None else label,
            status=sample["status"] if status is None else status,
            error=sample.get("error") if error is None else error,
            payload=deepcopy(payload) if payload is not None else deepcopy(sample["payload"]),
            updated_at=utc_now_naive(),
        )
        return deepcopy(row)

    async def by_ids(self, store, sample_ids):
        return [deepcopy(self.rows[sid]) for sid in sample_ids if sid in self.rows]

    async def similar(self, store, chat_id, *, before, window_days, limit):
        cutoff = before - timedelta(days=window_days)
        rows = [
            deepcopy(row)
            for row in self.rows.values()
            if row["chat_id"] == chat_id
            and cutoff <= row["created_at"] <= before
            and row["label"] != "negative"
        ]
        return sorted(rows, key=lambda row: row["created_at"], reverse=True)[:limit]

    async def for_user_chat(self, store, chat_id, user_id, *, limit=20):
        rows = [
            deepcopy(row)
            for row in self.rows.values()
            if row["chat_id"] == chat_id and row["user_id"] == user_id
        ]
        return sorted(rows, key=lambda row: row["created_at"], reverse=True)[:limit]

    async def list(self, **filters):
        rows = sorted(self.rows.values(), key=lambda row: row["created_at"], reverse=True)
        return {"rows": [deepcopy(row) for row in rows], "total": len(rows)}


class MemoryLearningJobs:
    def __init__(self):
        self.rows = {}

    async def enqueue(self, store, sample):
        if sample["id"] in self.rows:
            return
        now = utc_now_naive()
        self.rows[sample["id"]] = dict(
            id=uuid.uuid4().hex,
            sample_id=sample["id"],
            chat_id=sample["chat_id"],
            user_id=sample["user_id"],
            token=uuid.uuid4().hex,
            status="queued",
            attempts=0,
            payload={},
            error=None,
            retry_at=now,
            created_at=now,
            updated_at=now,
        )

    async def due(self, *, limit=10):
        return deepcopy(
            [
                row
                for row in self.rows.values()
                if row["status"] in {"queued", "running"} and row["retry_at"] <= utc_now_naive()
            ][:limit]
        )

    async def get(self, store, sample_id):
        return deepcopy(self.rows.get(sample_id))

    async def claim(self, store, candidate, *, lease_minutes=3):
        current = self.rows[candidate["sample_id"]]
        if (
            current["token"] != candidate["token"]
            or current["status"] not in {"queued", "running"}
            or current["retry_at"] > utc_now_naive()
        ):
            return None
        current.update(
            token=uuid.uuid4().hex,
            status="running",
            attempts=current["attempts"] + 1,
            retry_at=utc_now_naive() + timedelta(minutes=lease_minutes),
            updated_at=utc_now_naive(),
        )
        return deepcopy(current)

    async def save(self, store, job, state, payload, *, delay=0, error=None):
        current = self.rows[job["sample_id"]]
        if current["token"] != job["token"]:
            return False
        current.update(
            status=state,
            payload=deepcopy(payload),
            error=error,
            retry_at=utc_now_naive() + timedelta(seconds=delay),
            updated_at=utc_now_naive(),
        )
        return True

    async def retry_later(self, store, job, *, delay, error):
        current = self.rows[job["sample_id"]]
        current.update(
            status="queued",
            attempts=max(0, current["attempts"] - 1),
            retry_at=utc_now_naive() + timedelta(seconds=delay),
            error=error,
        )

    async def list(self, **filters):
        rows = sorted(self.rows.values(), key=lambda row: row["updated_at"], reverse=True)
        counts = {}
        for row in rows:
            counts[row["status"]] = counts.get(row["status"], 0) + 1
        return {"rows": [deepcopy(row) for row in rows], "total": len(rows), "counts": counts}


class MemoryAIRules:
    def __init__(self):
        self.rows = {}
        self.hits = []
        self._scopes = {}
        self._hit_seq = 0

    async def upsert(self, store, rule):
        for row in self.rows.values():
            if row["rule_key"] == rule["rule_key"]:
                row["sample_ids"] = list(dict.fromkeys([*row["sample_ids"], *rule["sample_ids"]]))
                row["confidence"] = max(row["confidence"], rule["confidence"])
                row["explanation"] = rule["explanation"]
                row["model"] = rule.get("model")
                row["updated_at"] = utc_now_naive()
                return deepcopy(row)
        row = deepcopy(
            {
                **rule,
                "status": rule.get("status", "candidate"),
                "action": rule.get("action", "delete"),
                "positives": 0,
                "negatives": 0,
                "reporters": 0,
                "target_users": 0,
                "corrections": 0,
                "hits": 0,
                "last_hit_at": None,
                "version": 1,
                "expires_at": None,
                "error": None,
                "actor": rule.get("actor"),
                "created_at": utc_now_naive(),
                "updated_at": utc_now_naive(),
            }
        )
        self.rows[row["id"]] = row
        return deepcopy(row)

    async def by_key(self, store, rule_key):
        return deepcopy(
            next((row for row in self.rows.values() if row["rule_key"] == rule_key), None)
        )

    async def get(self, store, rule_id):
        return deepcopy(self.rows.get(rule_id))

    async def refresh_stats(self, store, rule, samples):
        row = self.rows[rule["id"]]
        reporters, users = set(), set()
        for sample in samples:
            users.add(sample["user_id"])
            for reporter in (sample["payload"] or {}).get("reporters", []):
                if isinstance(reporter.get("user_id"), int):
                    reporters.add(reporter["user_id"])
        row.update(
            positives=sum(1 for sample in samples if sample["label"] == "positive"),
            negatives=sum(1 for sample in samples if sample["label"] == "negative"),
            reporters=len(reporters),
            target_users=len(users),
            updated_at=utc_now_naive(),
        )

    async def set_status(
        self,
        store,
        rule,
        status,
        *,
        actor=None,
        error=None,
        activate=False,
        bump_version=False,
        expires_days=30,
    ):
        row = self.rows[rule["id"]]
        row.update(
            status=status,
            actor=actor or row.get("actor"),
            error=error,
            version=row["version"] + (1 if bump_version else 0),
            expires_at=utc_now_naive() + timedelta(days=expires_days)
            if activate
            else row.get("expires_at"),
            updated_at=utc_now_naive(),
        )
        return deepcopy(row)

    async def record_hit(self, store, rule, chat_id, user_id, case_id, action, evidence):
        self._hit_seq += 1
        self.hits.append(
            dict(
                id=self._hit_seq,
                rule_id=rule["id"],
                chat_id=chat_id,
                user_id=user_id,
                case_id=case_id,
                version=rule["version"],
                action=action,
                evidence=deepcopy(evidence),
                created_at=utc_now_naive(),
            )
        )
        row = self.rows[rule["id"]]
        row.update(hits=row["hits"] + 1, last_hit_at=utc_now_naive(), updated_at=utc_now_naive())

    async def hits_for(self, store, chat_id, user_id, *, limit=20):
        return deepcopy(
            [
                hit
                for hit in reversed(self.hits)
                if hit["chat_id"] == chat_id and hit["user_id"] == user_id
            ][:limit]
        )

    async def for_sample(self, store, sample_id):
        return deepcopy([row for row in self.rows.values() if sample_id in row["sample_ids"]])

    async def for_chat(self, store, chat_id, *, limit=100):
        rows = [deepcopy(row) for row in self.rows.values() if row["chat_id"] == chat_id]
        return sorted(rows, key=lambda row: row["updated_at"], reverse=True)[:limit]

    async def add_correction(self, store, rule):
        row = self.rows[rule["id"]]
        row.update(corrections=row["corrections"] + 1, updated_at=utc_now_naive())

    async def set_action(self, store, rule, action):
        row = self.rows[rule["id"]]
        row.update(action=action, version=row["version"] + 1, updated_at=utc_now_naive())
        return deepcopy(row)

    async def active_rules(self):
        return deepcopy(
            [
                {**row, "shared_chats": sorted(self._scopes.get(row["id"], []))}
                for row in self.rows.values()
                if row["status"] == "active"
                and (row.get("expires_at") is None or row["expires_at"] > utc_now_naive())
            ]
        )

    async def scopes(self, store, rule_id):
        return sorted(self._scopes.get(rule_id, []))

    async def set_scopes(self, store, rule, chat_ids, actor):
        self._scopes[rule["id"]] = sorted(set(chat_ids))

    async def expiring(self, *, now=None):
        return deepcopy(
            [
                row
                for row in self.rows.values()
                if row["status"] == "active"
                and row.get("expires_at") is not None
                and row["expires_at"] <= (now or utc_now_naive())
            ]
        )

    async def list(self, **filters):
        rows = sorted(self.rows.values(), key=lambda row: row["updated_at"], reverse=True)
        return {"rows": [deepcopy(row) for row in rows], "total": len(rows)}

    async def recent_hits(self, rule_id, *, limit=50):
        return deepcopy([hit for hit in reversed(self.hits) if hit["rule_id"] == rule_id][:limit])

    async def expire_rules(self, *, now=None):
        changed = 0
        for row in self.rows.values():
            if (
                row["status"] == "active"
                and row.get("expires_at") is not None
                and row["expires_at"] <= (now or utc_now_naive())
            ):
                row.update(status="expired", updated_at=utc_now_naive())
                changed += 1
        return changed

    async def cleanup_samples(self, *, days):
        return 0

    async def cleanup_hits(self, *, days):
        return 0


class MemoryAIRuleFeedback:
    def __init__(self):
        self.rows = []
        self._seq = 0

    async def record(self, store, **fields):
        self._seq += 1
        self.rows.append(dict(id=self._seq, created_at=utc_now_naive(), **fields))

    async def list(self, **filters):
        return {"rows": deepcopy(list(reversed(self.rows))), "total": len(self.rows)}


def wired(guard):
    """把内存替身接入学习服务，返回替身集合。"""
    samples, jobs, rules = MemoryAISamples(), MemoryLearningJobs(), MemoryAIRules()
    feedback = MemoryAIRuleFeedback()
    guard.learning.samples = samples
    guard.learning.jobs = jobs
    guard.learning.rules = rules
    guard.learning.feedback = feedback
    return SimpleNamespace(samples=samples, jobs=jobs, rules=rules, feedback=feedback)
