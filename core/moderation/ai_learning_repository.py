# Copyright 2026 Sakura-Bot
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
"""AI 学习样本、任务、规则与反馈的 MySQL 持久化；带租约的任务与幂等写入。"""

import json
import uuid
from datetime import timedelta

from core.infrastructure.database.mysql import utc_now_naive

from .repository import GuardStore, encode


def _loads(value):
    return json.loads(value) if isinstance(value, str) else value


def decode_sample(row):
    if row:
        row["payload"] = _loads(row.get("payload"))
    return row


def decode_job(row):
    if row:
        row["payload"] = _loads(row.get("payload"))
    return row


def decode_rule(row):
    if row:
        row["sample_ids"] = _loads(row.get("sample_ids")) or []
    return row


def decode_hit(row):
    if row:
        row["evidence"] = _loads(row.get("evidence"))
    return row


class AISamples:
    """学习样本：同一条原消息幂等，不同上报人累计而不是覆盖。"""

    def __init__(self, repository):
        self.repository = repository

    async def create(self, store, sample):
        now = utc_now_naive()
        changed = await store.execute(
            """INSERT INTO moderation_ai_samples
            (id,idempotency_key,chat_id,user_id,message_id,source,case_id,label,status,payload,error,created_at,updated_at)
            VALUES (%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s)
            ON DUPLICATE KEY UPDATE id=id""",
            (
                sample["id"],
                sample["idempotency_key"],
                sample["chat_id"],
                sample["user_id"],
                sample.get("message_id"),
                sample["source"],
                sample.get("case_id"),
                sample.get("label", "pending"),
                sample.get("status", "new"),
                encode(sample["payload"]),
                sample.get("error"),
                now,
                now,
            ),
        )
        row = await self.by_key(store, sample["idempotency_key"])
        return row, bool(changed)

    async def by_key(self, store, idempotency_key):
        row = await store.one(
            "SELECT * FROM moderation_ai_samples WHERE idempotency_key=%s", (idempotency_key,)
        )
        return decode_sample(row)

    async def get(self, store, sample_id):
        return decode_sample(
            await store.one("SELECT * FROM moderation_ai_samples WHERE id=%s", (sample_id,))
        )

    async def by_case(self, store, case_id):
        return decode_sample(
            await store.one("SELECT * FROM moderation_ai_samples WHERE case_id=%s", (case_id,))
        )

    async def save(self, store, sample, *, label=None, status=None, error=None, payload=None):
        await store.execute(
            """UPDATE moderation_ai_samples SET label=%s,status=%s,error=%s,payload=%s,updated_at=%s
            WHERE id=%s""",
            (
                sample["label"] if label is None else label,
                sample["status"] if status is None else status,
                sample.get("error") if error is None else error,
                encode(sample["payload"] if payload is None else payload),
                utc_now_naive(),
                sample["id"],
            ),
        )

    async def by_ids(self, store, sample_ids):
        if not sample_ids:
            return []
        placeholders = ",".join(["%s"] * len(sample_ids))
        rows = await store.all(
            f"SELECT * FROM moderation_ai_samples WHERE id IN ({placeholders})",
            tuple(sample_ids),
        )
        return [decode_sample(row) for row in rows]

    async def similar(self, store, chat_id, *, before, window_days, limit):
        """仅取同群分析窗口内的少量样本作为上下文，不拉取无边界的群历史。"""
        rows = await store.all(
            """SELECT * FROM moderation_ai_samples WHERE chat_id=%s AND created_at>=%s
            AND created_at<=%s AND label<>'negative' ORDER BY created_at DESC LIMIT %s""",
            (chat_id, before - timedelta(days=window_days), before, max(1, min(limit, 100))),
        )
        return [decode_sample(row) for row in rows]

    async def for_user_chat(self, store, chat_id, user_id, *, limit=20):
        rows = await store.all(
            """SELECT * FROM moderation_ai_samples WHERE chat_id=%s AND user_id=%s
            ORDER BY created_at DESC LIMIT %s""",
            (chat_id, user_id, limit),
        )
        return [decode_sample(row) for row in rows]

    async def list(self, *, chat_id=None, label=None, status=None, limit=50, offset=0):
        async with self.repository.pool.acquire() as connection:
            store = GuardStore(connection)
            clauses, values = [], []
            if chat_id is not None:
                clauses.append("chat_id=%s")
                values.append(chat_id)
            if label:
                clauses.append("label=%s")
                values.append(label)
            if status:
                clauses.append("status=%s")
                values.append(status)
            where = " WHERE " + " AND ".join(clauses) if clauses else ""
            total = await store.one(
                f"SELECT COUNT(*) AS total FROM moderation_ai_samples{where}", tuple(values)
            )
            rows = await store.all(
                f"""SELECT * FROM moderation_ai_samples{where}
                ORDER BY created_at DESC LIMIT %s OFFSET %s""",
                (*values, min(max(limit, 1), 200), max(offset, 0)),
            )
            return {"rows": [decode_sample(row) for row in rows], "total": total["total"]}


class AILearningJobs:
    """每样本唯一的学习任务；租约与重试语义与举报 AI 队列一致。"""

    def __init__(self, repository):
        self.repository = repository

    async def enqueue(self, store, sample):
        now = utc_now_naive()
        await store.execute(
            """INSERT INTO moderation_ai_learning_jobs
            (id,sample_id,chat_id,user_id,status,token,attempts,payload,retry_at,created_at,updated_at)
            VALUES (%s,%s,%s,%s,'queued',%s,0,'{}',%s,%s,%s)
            ON DUPLICATE KEY UPDATE id=id""",
            (
                uuid.uuid4().hex,
                sample["id"],
                sample["chat_id"],
                sample["user_id"],
                uuid.uuid4().hex,
                now,
                now,
                now,
            ),
        )

    async def due(self, *, limit=10):
        async with self.repository.pool.acquire() as connection:
            rows = await GuardStore(connection).all(
                """SELECT * FROM moderation_ai_learning_jobs WHERE status IN ('queued','running')
                AND retry_at<=%s ORDER BY retry_at,created_at LIMIT %s""",
                (utc_now_naive(), max(1, min(limit, 50))),
            )
            return [decode_job(row) for row in rows]

    async def get(self, store, sample_id):
        return decode_job(
            await store.one(
                "SELECT * FROM moderation_ai_learning_jobs WHERE sample_id=%s", (sample_id,)
            )
        )

    async def claim(self, store, candidate, *, lease_minutes=3):
        token, now = uuid.uuid4().hex, utc_now_naive()
        changed = await store.execute(
            """UPDATE moderation_ai_learning_jobs SET status='running',token=%s,attempts=attempts+1,
            retry_at=%s,updated_at=%s WHERE sample_id=%s AND token=%s
            AND status IN ('queued','running') AND retry_at<=%s""",
            (
                token,
                now + timedelta(minutes=lease_minutes),
                now,
                candidate["sample_id"],
                candidate["token"],
                now,
            ),
        )
        return await self.get(store, candidate["sample_id"]) if changed else None

    async def save(self, store, job, state, payload, *, delay=0, error=None):
        now = utc_now_naive()
        return bool(
            await store.execute(
                """UPDATE moderation_ai_learning_jobs SET status=%s,payload=%s,error=%s,retry_at=%s,
                updated_at=%s WHERE sample_id=%s AND token=%s""",
                (
                    state,
                    encode(payload),
                    error,
                    now + timedelta(seconds=delay),
                    now,
                    job["sample_id"],
                    job["token"],
                ),
            )
        )

    async def retry_later(self, store, job, *, delay, error):
        now = utc_now_naive()
        await store.execute(
            """UPDATE moderation_ai_learning_jobs SET status='queued',attempts=GREATEST(0,attempts-1),
            retry_at=%s,error=%s,updated_at=%s WHERE sample_id=%s AND token=%s""",
            (now + timedelta(seconds=delay), error, now, job["sample_id"], job["token"]),
        )

    async def list(self, *, chat_id=None, status=None, limit=50, offset=0):
        async with self.repository.pool.acquire() as connection:
            store = GuardStore(connection)
            clauses, values = [], []
            if chat_id is not None:
                clauses.append("chat_id=%s")
                values.append(chat_id)
            if status:
                clauses.append("status=%s")
                values.append(status)
            where = " WHERE " + " AND ".join(clauses) if clauses else ""
            total = await store.one(
                f"SELECT COUNT(*) AS total FROM moderation_ai_learning_jobs{where}", tuple(values)
            )
            counts = await store.all(
                "SELECT status,COUNT(*) AS total FROM moderation_ai_learning_jobs GROUP BY status"
            )
            rows = await store.all(
                f"""SELECT * FROM moderation_ai_learning_jobs{where}
                ORDER BY updated_at DESC LIMIT %s OFFSET %s""",
                (*values, min(max(limit, 1), 200), max(offset, 0)),
            )
            return {
                "rows": [decode_job(row) for row in rows],
                "total": total["total"],
                "counts": {row["status"]: row["total"] for row in counts},
            }


class AIRules:
    """AI 动态规则：与人工规则分表管理，状态流转带版本与审计字段。"""

    def __init__(self, repository):
        self.repository = repository

    async def upsert(self, store, rule):
        """按 rule_key 幂等合并支持样本；管理员已拒绝的规则不自动复活。"""
        now = utc_now_naive()
        existing = await self.by_key(store, rule["rule_key"])
        if existing:
            merged = list(dict.fromkeys([*existing["sample_ids"], *rule["sample_ids"]]))
            await store.execute(
                """UPDATE moderation_ai_rules SET sample_ids=%s,confidence=GREATEST(confidence,%s),
                explanation=%s,model=%s,updated_at=%s WHERE id=%s""",
                (
                    encode(merged),
                    rule["confidence"],
                    rule["explanation"],
                    rule.get("model"),
                    now,
                    existing["id"],
                ),
            )
            return await self.by_key(store, rule["rule_key"])
        await store.execute(
            """INSERT INTO moderation_ai_rules
            (id,rule_key,chat_id,rule_type,match_kind,value,normalized_value,action,status,
            confidence,model,actor,explanation,sample_ids,created_at,updated_at)
            VALUES (%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s)""",
            (
                rule["id"],
                rule["rule_key"],
                rule["chat_id"],
                rule["rule_type"],
                rule["match_kind"],
                rule["value"],
                rule["normalized_value"],
                rule["action"],
                rule.get("status", "candidate"),
                rule["confidence"],
                rule.get("model"),
                rule.get("actor"),
                rule["explanation"],
                encode(rule["sample_ids"]),
                now,
                now,
            ),
        )
        return await self.by_key(store, rule["rule_key"])

    async def by_key(self, store, rule_key):
        return decode_rule(
            await store.one("SELECT * FROM moderation_ai_rules WHERE rule_key=%s", (rule_key,))
        )

    async def get(self, store, rule_id):
        return decode_rule(
            await store.one("SELECT * FROM moderation_ai_rules WHERE id=%s", (rule_id,))
        )

    async def get_locked(self, store, rule_id):
        return decode_rule(
            await store.one("SELECT * FROM moderation_ai_rules WHERE id=%s FOR UPDATE", (rule_id,))
        )

    async def refresh_stats(self, store, rule, samples):
        """由样本最新标签重算统计；样本标签纠正后规则数据随之修正。"""
        positives = sum(1 for row in samples if row["label"] == "positive")
        negatives = sum(1 for row in samples if row["label"] == "negative")
        reporters, users = set(), set()
        for row in samples:
            users.add(row["user_id"])
            for reporter in (row["payload"] or {}).get("reporters", []):
                if isinstance(reporter.get("user_id"), int):
                    reporters.add(reporter["user_id"])
        await store.execute(
            """UPDATE moderation_ai_rules SET positives=%s,negatives=%s,reporters=%s,target_users=%s,
            updated_at=%s WHERE id=%s""",
            (positives, negatives, len(reporters), len(users), utc_now_naive(), rule["id"]),
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
        await store.execute(
            """UPDATE moderation_ai_rules SET status=%s,actor=%s,error=%s,version=version+%s,
            expires_at=IF(%s,%s,expires_at),updated_at=%s WHERE id=%s""",
            (
                status,
                actor or rule.get("actor"),
                error,
                1 if bump_version else 0,
                activate,
                utc_now_naive() + timedelta(days=expires_days) if activate else None,
                utc_now_naive(),
                rule["id"],
            ),
        )

    async def record_hit(self, store, rule, chat_id, user_id, case_id, action, evidence):
        now = utc_now_naive()
        await store.execute(
            """INSERT INTO moderation_ai_rule_hits
            (rule_id,chat_id,user_id,case_id,version,action,evidence,created_at)
            VALUES (%s,%s,%s,%s,%s,%s,%s,%s)""",
            (rule["id"], chat_id, user_id, case_id, rule["version"], action, encode(evidence), now),
        )
        # 命中计数走单独小语句；高频命中由审计表承载，避免主表写放大。
        await store.execute(
            """UPDATE moderation_ai_rules SET hits=hits+1,last_hit_at=%s,updated_at=%s WHERE id=%s""",
            (now, now, rule["id"]),
        )

    async def hits_for(self, store, chat_id, user_id, *, limit=20):
        rows = await store.all(
            """SELECT * FROM moderation_ai_rule_hits WHERE chat_id=%s AND user_id=%s
            ORDER BY id DESC LIMIT %s""",
            (chat_id, user_id, limit),
        )
        return [decode_hit(row) for row in rows]

    async def for_sample(self, store, sample_id):
        rows = await store.all(
            "SELECT * FROM moderation_ai_rules WHERE JSON_CONTAINS(sample_ids, %s)",
            (f'"{sample_id}"',),
        )
        return [decode_rule(row) for row in rows]

    async def for_chat(self, store, chat_id, *, limit=100):
        rows = await store.all(
            "SELECT * FROM moderation_ai_rules WHERE chat_id=%s ORDER BY updated_at DESC LIMIT %s",
            (chat_id, limit),
        )
        return [decode_rule(row) for row in rows]

    async def add_correction(self, store, rule):
        await store.execute(
            "UPDATE moderation_ai_rules SET corrections=corrections+1,updated_at=%s WHERE id=%s",
            (utc_now_naive(), rule["id"]),
        )

    async def active_rules(self):
        """已激活规则连同显式共享作用域；共享开关由调用方按配置过滤。"""
        async with self.repository.pool.acquire() as connection:
            store = GuardStore(connection)
            rows = await store.all(
                """SELECT * FROM moderation_ai_rules WHERE status='active'
                AND (expires_at IS NULL OR expires_at>%s) ORDER BY created_at""",
                (utc_now_naive(),),
            )
            rules = [decode_rule(row) for row in rows]
            scopes = await store.all("SELECT rule_id,chat_id FROM moderation_ai_rule_scopes")
        shared: dict[str, list[int]] = {}
        for row in scopes:
            shared.setdefault(row["rule_id"], []).append(row["chat_id"])
        for rule in rules:
            rule["shared_chats"] = sorted(shared.get(rule["id"], []))
        return rules

    async def scopes(self, store, rule_id):
        rows = await store.all(
            "SELECT chat_id FROM moderation_ai_rule_scopes WHERE rule_id=%s ORDER BY chat_id",
            (rule_id,),
        )
        return sorted(row["chat_id"] for row in rows)

    async def set_scopes(self, store, rule, chat_ids, actor):
        now = utc_now_naive()
        wanted = sorted(set(chat_ids))
        current = await self.scopes(store, rule["id"])
        for chat_id in [cid for cid in current if cid not in wanted]:
            await store.execute(
                "DELETE FROM moderation_ai_rule_scopes WHERE rule_id=%s AND chat_id=%s",
                (rule["id"], chat_id),
            )
        for chat_id in [cid for cid in wanted if cid not in current]:
            await store.execute(
                """INSERT INTO moderation_ai_rule_scopes (rule_id,chat_id,actor,created_at)
                VALUES (%s,%s,%s,%s)""",
                (rule["id"], chat_id, str(actor), now),
            )

    async def expiring(self, *, now=None):
        async with self.repository.pool.acquire() as connection:
            rows = await GuardStore(connection).all(
                """SELECT * FROM moderation_ai_rules WHERE status='active'
                AND expires_at IS NOT NULL AND expires_at<=%s""",
                (now or utc_now_naive(),),
            )
            return [decode_rule(row) for row in rows]

    async def list(self, *, chat_id=None, rule_type=None, status=None, limit=50, offset=0):
        async with self.repository.pool.acquire() as connection:
            store = GuardStore(connection)
            clauses, values = [], []
            if chat_id is not None:
                clauses.append("chat_id=%s")
                values.append(chat_id)
            if rule_type:
                clauses.append("rule_type=%s")
                values.append(rule_type)
            if status:
                clauses.append("status=%s")
                values.append(status)
            where = " WHERE " + " AND ".join(clauses) if clauses else ""
            total = await store.one(
                f"SELECT COUNT(*) AS total FROM moderation_ai_rules{where}", tuple(values)
            )
            rows = await store.all(
                f"""SELECT * FROM moderation_ai_rules{where}
                ORDER BY updated_at DESC LIMIT %s OFFSET %s""",
                (*values, min(max(limit, 1), 200), max(offset, 0)),
            )
            return {"rows": [decode_rule(row) for row in rows], "total": total["total"]}

    async def recent_hits(self, rule_id, *, limit=50):
        async with self.repository.pool.acquire() as connection:
            rows = await GuardStore(connection).all(
                "SELECT * FROM moderation_ai_rule_hits WHERE rule_id=%s ORDER BY id DESC LIMIT %s",
                (rule_id, min(max(limit, 1), 200)),
            )
            return [decode_hit(row) for row in rows]

    async def expire_rules(self, *, now=None):
        """兼容旧批量过期；新代码优先用 expiring() 逐条流转以保留审计。"""
        async with self.repository.pool.acquire() as connection:
            return await GuardStore(connection).execute(
                """UPDATE moderation_ai_rules SET status='expired',updated_at=%s
                WHERE status='active' AND expires_at IS NOT NULL AND expires_at<=%s""",
                (utc_now_naive(), now or utc_now_naive()),
            )

    async def cleanup_samples(self, *, days):
        """仅删除已处理且不再被任何规则引用的过期样本；引用中的样本保留。"""
        async with self.repository.pool.acquire() as connection:
            store = GuardStore(connection)
            return await store.execute(
                """DELETE FROM moderation_ai_samples WHERE status IN ('learned','skipped','invalid')
                AND created_at<%s AND NOT EXISTS (
                SELECT 1 FROM moderation_ai_rules
                WHERE JSON_CONTAINS(moderation_ai_rules.sample_ids,
                JSON_QUOTE(moderation_ai_samples.id))) LIMIT 200""",
                (utc_now_naive() - timedelta(days=days),),
            )

    async def cleanup_hits(self, *, days):
        async with self.repository.pool.acquire() as connection:
            return await GuardStore(connection).execute(
                "DELETE FROM moderation_ai_rule_hits WHERE created_at<%s LIMIT 500",
                (utc_now_naive() - timedelta(days=days),),
            )


class AIRuleFeedback:
    """正负反馈审计：来源、操作者、案件与执行结果全部可追溯。"""

    def __init__(self, repository):
        self.repository = repository

    async def record(
        self,
        store,
        *,
        rule_id,
        sample_id,
        case_id,
        chat_id,
        user_id,
        kind,
        polarity,
        actor,
        payload,
    ):
        await store.execute(
            """INSERT INTO moderation_ai_rule_feedback
            (rule_id,sample_id,case_id,chat_id,user_id,kind,polarity,actor,payload,created_at)
            VALUES (%s,%s,%s,%s,%s,%s,%s,%s,%s,%s)""",
            (
                rule_id,
                sample_id,
                case_id,
                chat_id,
                user_id,
                kind,
                polarity,
                actor,
                encode(payload or {}),
                utc_now_naive(),
            ),
        )

    async def list(self, *, rule_id=None, chat_id=None, limit=50, offset=0):
        async with self.repository.pool.acquire() as connection:
            store = GuardStore(connection)
            clauses, values = [], []
            if rule_id:
                clauses.append("rule_id=%s")
                values.append(rule_id)
            if chat_id is not None:
                clauses.append("chat_id=%s")
                values.append(chat_id)
            where = " WHERE " + " AND ".join(clauses) if clauses else ""
            total = await store.one(
                f"SELECT COUNT(*) AS total FROM moderation_ai_rule_feedback{where}", tuple(values)
            )
            rows = await store.all(
                f"""SELECT * FROM moderation_ai_rule_feedback{where}
                ORDER BY id DESC LIMIT %s OFFSET %s""",
                (*values, min(max(limit, 1), 200), max(offset, 0)),
            )
            return {"rows": rows, "total": total["total"]}
