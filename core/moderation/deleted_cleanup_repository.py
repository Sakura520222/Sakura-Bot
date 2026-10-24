# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
"""已销号清理任务、成员处理意图及跨进程互斥；不改动群管案件或黑白名单。"""

import asyncio
import uuid
from contextlib import asynccontextmanager

from core.infrastructure.database.mysql import utc_now_naive

from .repository import GuardStore, encode

ACTIVE_CLEANUP = {"running", "waiting"}


class DeletedCleanupStore:
    def __init__(self, store):
        self.store = store

    async def active(self, bot_id):
        return await self.store.one(
            "SELECT * FROM moderation_deleted_cleanup_jobs WHERE active_key=%s", (bot_id,)
        )

    async def requested(self, bot_id, request_id):
        return await self.store.one(
            "SELECT * FROM moderation_deleted_cleanup_jobs WHERE bot_id=%s AND request_id=%s",
            (bot_id, request_id),
        )

    async def get(self, bot_id, job_id):
        return await self.store.one(
            "SELECT * FROM moderation_deleted_cleanup_jobs WHERE id=%s AND bot_id=%s",
            (job_id, bot_id),
        )

    async def latest_scheduled(self, bot_id):
        return await self.store.one(
            """SELECT created_at FROM moderation_deleted_cleanup_jobs WHERE bot_id=%s
            AND trigger_type='scheduled' ORDER BY created_at DESC LIMIT 1""",
            (bot_id,),
        )

    async def create(self, bot_id, actor, request_id, groups, requested_ids, trigger="manual"):
        now = utc_now_naive()
        job = {
            "id": uuid.uuid4().hex,
            "bot_id": bot_id,
            "request_id": request_id,
            "actor": str(actor),
            "trigger_type": trigger,
            "status": "running",
            "payload": {"groups": groups, "group_index": 0, "requested_chat_ids": requested_ids},
            "error": None,
            "retry_at": now,
            "created_at": now,
            "updated_at": now,
        }
        await self.store.execute(
            """INSERT INTO moderation_deleted_cleanup_jobs
            (id,bot_id,request_id,active_key,actor,trigger_type,status,payload,error,retry_at,created_at,updated_at)
            VALUES (%s,%s,%s,%s,%s,%s,%s,%s,NULL,%s,%s,%s)""",
            (
                job["id"],
                bot_id,
                request_id,
                bot_id,
                str(actor),
                trigger,
                job["status"],
                encode(job["payload"]),
                now,
                now,
                now,
            ),
        )
        # 旧清理记录里的已移除销号账号仍可有遗留消息；仅复用同 Bot、同群的审计身份，
        # 新任务再次核验 deleted 标记后才删除，不改写旧任务或黑白名单。
        for group in groups:
            await self.store.execute(
                """INSERT IGNORE INTO moderation_deleted_cleanup_items
                (job_id,chat_id,user_id,access_hash,status,updated_at)
                SELECT %s,i.chat_id,i.user_id,MAX(i.access_hash),'removing',%s
                FROM moderation_deleted_cleanup_items i
                JOIN moderation_deleted_cleanup_jobs j ON j.id=i.job_id
                WHERE j.bot_id=%s AND i.chat_id=%s AND i.status='removed' AND i.job_id<>%s
                GROUP BY i.chat_id,i.user_id""",
                (job["id"], now, bot_id, group["chat_id"], job["id"]),
            )
        return job

    async def save(self, job):
        await self.store.execute(
            """UPDATE moderation_deleted_cleanup_jobs SET status=%s,payload=%s,error=%s,
            retry_at=%s,active_key=%s,updated_at=%s WHERE id=%s""",
            (
                job["status"],
                encode(job["payload"]),
                job["error"],
                job["retry_at"],
                job["bot_id"] if job["status"] in ACTIVE_CLEANUP else None,
                utc_now_naive(),
                job["id"],
            ),
        )

    async def candidates(self, job_id, chat_id, users):
        for user in users:
            await self.store.execute(
                """INSERT IGNORE INTO moderation_deleted_cleanup_items
                (job_id,chat_id,user_id,access_hash,status,updated_at) VALUES (%s,%s,%s,%s,'pending',%s)""",
                (job_id, chat_id, user.id, user.access_hash, utc_now_naive()),
            )

    async def pending(self, job_id, chat_id):
        return await self.store.one(
            """SELECT * FROM moderation_deleted_cleanup_items WHERE job_id=%s AND chat_id=%s
            AND status IN ('pending','removing') ORDER BY user_id LIMIT 1""",
            (job_id, chat_id),
        )

    async def item(self, row, status, message=""):
        await self.store.execute(
            """UPDATE moderation_deleted_cleanup_items SET status=%s,message=%s,
            updated_at=%s WHERE job_id=%s AND chat_id=%s AND user_id=%s""",
            (status, message[:256], utc_now_naive(), row["job_id"], row["chat_id"], row["user_id"]),
        )

    async def attempt(self, row, delta=1):
        await self.store.execute(
            """UPDATE moderation_deleted_cleanup_items SET attempts=GREATEST(0,attempts+%s)
            WHERE job_id=%s AND chat_id=%s AND user_id=%s""",
            (delta, row["job_id"], row["chat_id"], row["user_id"]),
        )

    async def history(self, bot_id, limit=10):
        return await self.store.all(
            "SELECT * FROM moderation_deleted_cleanup_jobs WHERE bot_id=%s ORDER BY created_at DESC LIMIT %s",
            (bot_id, limit),
        )

    async def view(self, job):
        counts = await self.store.all(
            "SELECT status,COUNT(*) AS total FROM moderation_deleted_cleanup_items WHERE job_id=%s GROUP BY status",
            (job["id"],),
        )
        results = await self.store.all(
            """SELECT chat_id,user_id,status,message,updated_at FROM moderation_deleted_cleanup_items
            WHERE job_id=%s AND status NOT IN ('pending','removing') ORDER BY updated_at DESC LIMIT 100""",
            (job["id"],),
        )
        return {
            "id": job["id"],
            "actor": job["actor"],
            "trigger_type": job["trigger_type"],
            "status": job["status"],
            "groups": job["payload"]["groups"],
            "checked_groups": job["payload"]["group_index"],
            "error": job["error"],
            "counts": {row["status"]: int(row["total"]) for row in counts},
            "results": results,
            **{key: job[key].isoformat() + "Z" for key in ("created_at", "updated_at", "retry_at")},
        }


class DeletedCleanupRepository:
    def __init__(self, pool):
        self.pool = pool

    @asynccontextmanager
    async def locked(self, bot_id):
        async with self.pool.acquire() as connection:
            store = GuardStore(connection)
            name = f"sakura_guard:deleted:{bot_id}"
            try:
                acquired = await asyncio.wait_for(
                    store.one("SELECT GET_LOCK(%s,0) AS acquired", (name,)), 5
                )
            except BaseException:
                connection.close()
                raise
            if not acquired or acquired["acquired"] != 1:
                yield None
                return
            try:
                yield DeletedCleanupStore(store)
            finally:
                try:
                    await connection.rollback()
                    released = await asyncio.wait_for(
                        store.one("SELECT RELEASE_LOCK(%s) AS released", (name,)), 5
                    )
                    if not released or released["released"] != 1:
                        connection.close()
                except BaseException:
                    connection.close()
                    raise

    @asynccontextmanager
    async def read(self):
        async with self.pool.acquire() as connection:
            yield DeletedCleanupStore(GuardStore(connection))
