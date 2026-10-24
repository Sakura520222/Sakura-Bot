# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
"""队列存储替身；真实 SQL、事务和租约另由 MySQL 集成测试验证。"""

import uuid
from copy import deepcopy
from datetime import timedelta

from core.infrastructure.database.mysql import utc_now_naive


class MemoryAIJobs:
    def __init__(self):
        self.rows = {}

    async def enqueue(self, store, case, delay):
        now = utc_now_naive()
        self.rows[case["id"]] = dict(
            case_id=case["id"],
            user_id=case["user_id"],
            token=uuid.uuid4().hex,
            status="queued",
            attempts=0,
            payload={},
            retry_at=now + delay,
            created_at=now,
        )

    async def due(self):
        return deepcopy(
            [
                row
                for row in self.rows.values()
                if row["status"] in {"queued", "running", "executing"}
                and row["retry_at"] <= utc_now_naive()
            ]
        )

    async def get(self, store, case_id):
        return deepcopy(self.rows.get(case_id))

    async def claim(self, store, candidate):
        current = self.rows[candidate["case_id"]]
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
            retry_at=utc_now_naive() + timedelta(minutes=3),
        )
        return deepcopy(current)

    async def save(self, store, job, state, payload, *, delay=0, error=None):
        current = self.rows[job["case_id"]]
        if current["token"] != job["token"]:
            return False
        current.update(
            status=state,
            payload=deepcopy(payload),
            error=error,
            retry_at=utc_now_naive() + timedelta(seconds=delay),
        )
        return True

    async def convert(self, store, case, action, payload):
        current = store.cases[case["id"]]
        if current["action"] != "report" or current["status"] != "pending":
            return False
        current.update(
            action=action,
            status="executing",
            payload=deepcopy(payload),
            reviewer_id=None,
            reviewer_actor="system:ai_report",
        )
        return True

    async def restore(self, store, case, payload):
        store.cases[case["id"]].update(
            action="report",
            status="pending",
            reviewer_id=None,
            reviewer_actor=None,
            payload=deepcopy(payload),
        )
