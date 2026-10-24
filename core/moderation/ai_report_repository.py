# Copyright 2026 Sakura-Bot
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
"""举报 AI 队列：每案件唯一、带租约的提交与可恢复执行。"""

import uuid
from contextlib import asynccontextmanager
from datetime import timedelta

from core.infrastructure.database.mysql import utc_now_naive

from .repository import GuardStore, encode


@asynccontextmanager
async def atomic(store):
    await store.connection.begin()
    try:
        yield
        await store.connection.commit()
    except BaseException:
        await store.connection.rollback()
        raise


class AIReportJobs:
    def __init__(self, repository):
        self.repository = repository

    async def enqueue(self, store, case, delay):
        now = utc_now_naive()
        await store.execute(
            """INSERT INTO moderation_ai_reports
            (case_id,user_id,status,token,attempts,payload,retry_at,created_at,updated_at)
            VALUES (%s,%s,'queued',%s,0,'{}',%s,%s,%s)""",
            (case["id"], case["user_id"], uuid.uuid4().hex, now + delay, now, now),
        )

    async def due(self):
        async with self.repository.pool.acquire() as connection:
            return await GuardStore(connection).all(
                """SELECT * FROM moderation_ai_reports WHERE status IN ('queued','running','executing')
                AND retry_at<=%s ORDER BY retry_at,created_at LIMIT 20""",
                (utc_now_naive(),),
            )

    async def get(self, store, case_id):
        return await store.one("SELECT * FROM moderation_ai_reports WHERE case_id=%s", (case_id,))

    async def claim(self, store, candidate):
        token, now = uuid.uuid4().hex, utc_now_naive()
        changed = await store.execute(
            """UPDATE moderation_ai_reports SET status='running',token=%s,attempts=attempts+1,
            retry_at=%s,updated_at=%s WHERE case_id=%s AND token=%s
            AND status IN ('queued','running') AND retry_at<=%s""",
            (token, now + timedelta(minutes=3), now, candidate["case_id"], candidate["token"], now),
        )
        return await self.get(store, candidate["case_id"]) if changed else None

    async def save(self, store, job, state, payload, *, delay=0, error=None):
        now = utc_now_naive()
        return await store.execute(
            """UPDATE moderation_ai_reports SET status=%s,payload=%s,error=%s,retry_at=%s,
            updated_at=%s WHERE case_id=%s AND token=%s""",
            (
                state,
                encode(payload),
                error,
                now + timedelta(seconds=delay),
                now,
                job["case_id"],
                job["token"],
            ),
        )

    async def convert(self, store, case, action, payload):
        now = utc_now_naive()
        return await store.execute(
            """UPDATE moderation_cases SET action=%s,status='executing',payload=%s,
            reviewer_id=NULL,reviewer_actor='system:ai_report',reviewed_at=%s,error=NULL,
            updated_at=%s WHERE id=%s AND action='report' AND status='pending'
            AND reviewer_actor IS NULL AND reviewer_id IS NULL""",
            (action, encode(payload), now, now, case["id"]),
        )

    async def restore(self, store, case, payload):
        return await store.execute(
            """UPDATE moderation_cases SET action='report',status='pending',payload=%s,
            reviewer_id=NULL,reviewer_actor=NULL,reviewed_at=NULL,error=NULL,updated_at=%s
            WHERE id=%s AND status='executing' AND reviewer_actor='system:ai_report'""",
            (encode(payload), utc_now_naive(), case["id"]),
        )

    async def reschedule(self, store, job, deadline):
        await store.execute(
            """UPDATE moderation_ai_reports SET status='queued',attempts=GREATEST(0,attempts-1),
            retry_at=%s,updated_at=%s WHERE case_id=%s AND token=%s""",
            (deadline, utc_now_naive(), job["case_id"], job["token"]),
        )
