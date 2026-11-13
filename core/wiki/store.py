# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot

"""MySQL persistence, identity mapping and fenced per-channel jobs."""

import asyncio
import base64
import json
import uuid
from contextlib import asynccontextmanager
from contextvars import ContextVar
from datetime import timedelta

import aiomysql

from .models import WikiConfig, alias_key, canonical_id, chunks, digest, utc
from .schema import (
    CHANNEL_COVERS_TABLE,
    DIRTY_PAGES_TABLE,
    SCHEMA,
    SCHEMA_VERSION,
    WIKI_ACCOUNT_TABLES,
)

_read_snapshot = ContextVar("wiki_read_snapshot", default=None)


def encode(value):
    return json.dumps(value, ensure_ascii=False, default=str, sort_keys=True)


def decode(value, default=None):
    if value is None:
        return {} if default is None else default
    return json.loads(value) if isinstance(value, (str, bytes)) else value


class LeaseLost(RuntimeError):
    pass


class WikiSchemaError(RuntimeError):
    def __init__(self, table, error):
        self.table = table
        self.mysql_code = error.args[0] if error.args and isinstance(error.args[0], int) else None
        reason = f"MySQL {self.mysql_code}" if self.mysql_code else type(error).__name__
        super().__init__(f"Wiki 建表失败：{table}（{reason}）")


class WikiStore:
    def __init__(self, db):
        self.db = db
        self._checkpoint_locks = {}

    @asynccontextmanager
    async def snapshot(self):
        existing = _read_snapshot.get()
        if existing and existing[0] is self:
            yield
            return
        async with self.db.pool.acquire() as connection:
            await connection.query("SET TRANSACTION ISOLATION LEVEL REPEATABLE READ")
            await connection.begin()
            try:
                async with connection.cursor(aiomysql.DictCursor) as cursor:
                    token = _read_snapshot.set((self, cursor))
                    try:
                        yield
                    finally:
                        _read_snapshot.reset(token)
            finally:
                await connection.rollback()

    @asynccontextmanager
    async def transaction(self):
        async with self.db.pool.acquire() as connection:
            await connection.begin()
            try:
                async with connection.cursor(aiomysql.DictCursor) as cursor:
                    yield cursor
                await connection.commit()
            except BaseException:
                await connection.rollback()
                raise

    async def query(self, sql, args=(), *, fetch=False):
        snapshot = _read_snapshot.get()
        if snapshot and snapshot[0] is self:
            cursor = snapshot[1]
            await cursor.execute(sql, args)
            return list(await cursor.fetchall()) if fetch else cursor.rowcount
        async with self.transaction() as cursor:
            await cursor.execute(sql, args)
            return list(await cursor.fetchall()) if fetch else cursor.rowcount

    async def one(self, sql, args=()):
        rows = await self.query(sql, args, fetch=True)
        return rows[0] if rows else None

    async def initialize(self):
        try:
            version = await self.one("SELECT MAX(version) version FROM wiki_schema")
        except aiomysql.ProgrammingError as error:
            if error.args[0] != 1146:
                raise
            version = None
        if version and version["version"] is not None:
            if version["version"] != SCHEMA_VERSION:
                raise RuntimeError("Unsupported Wiki schema version")
            column = await self.one("SHOW COLUMNS FROM wiki_sources LIKE 'exclusion_reason'")
            if not column:
                await self.query(
                    "ALTER TABLE wiki_sources ADD COLUMN exclusion_reason VARCHAR(32) NOT NULL DEFAULT ''"
                )
            column = await self.one("SHOW COLUMNS FROM wiki_chunks LIKE 'applied_key'")
            if not column:
                await self.query(
                    "ALTER TABLE wiki_chunks ADD COLUMN applied_key CHAR(64) NOT NULL DEFAULT ''"
                )
            column = await self.one("SHOW COLUMNS FROM wiki_publications LIKE 'content_hash'")
            if not column:
                await self.query(
                    "ALTER TABLE wiki_publications ADD COLUMN content_hash CHAR(64) NOT NULL DEFAULT ''"
                )
            index = await self.one(
                "SHOW INDEX FROM wiki_page_revisions WHERE Key_name='wiki_feed_latest'"
            )
            if not index:
                try:
                    await self.query(
                        "ALTER TABLE wiki_page_revisions ADD INDEX wiki_feed_latest(created_at DESC,page_id DESC)"
                    )
                except aiomysql.OperationalError as error:
                    if error.args[0] != 1061:  # Another starting worker created the same index.
                        raise
            await self.query(DIRTY_PAGES_TABLE)
            await self.query(CHANNEL_COVERS_TABLE)
            for statement in WIKI_ACCOUNT_TABLES:
                await self.query(statement)
            return
        for statement in SCHEMA:
            try:
                await self.query(statement)
            except Exception as error:
                raise WikiSchemaError(statement.split()[5], error) from error
        await self.query(
            "INSERT IGNORE INTO wiki_schema VALUES (%s,UTC_TIMESTAMP(6))", (SCHEMA_VERSION,)
        )

    async def channel(self, key):
        try:
            key = alias_key(key)
        except ValueError:
            return None
        row = await self.one(
            """SELECT c.*,cover.digest cover_digest FROM wiki_channels c JOIN wiki_channel_aliases a
            ON a.channel_id=c.id LEFT JOIN wiki_channel_covers cover ON cover.channel_id=c.id
            WHERE a.alias=%s""",
            (key,),
        )
        if row:
            row["config"] = WikiConfig.model_validate(decode(row["config"]))
            row["coverage"] = decode(row["coverage"])
        return row

    async def register_channel(self, entity, configured_alias):
        channel = canonical_id(entity.id)
        username = getattr(entity, "username", None)
        access_verified = bool(
            username
            and getattr(entity, "broadcast", False)
            and not getattr(entity, "restricted", False)
            and not getattr(entity, "noforwards", False)
        )
        async with self.transaction() as cur:
            await cur.execute(
                """INSERT INTO wiki_channels
                (id,title,username,access_verified,verified_until,config,coverage,updated_at)
                VALUES (%s,%s,%s,%s,%s,%s,%s,UTC_TIMESTAMP(6))
                ON DUPLICATE KEY UPDATE title=VALUES(title),username=VALUES(username),
                access_verified=VALUES(access_verified),verified_until=VALUES(verified_until)""",
                (
                    channel,
                    getattr(entity, "title", channel),
                    username,
                    access_verified,
                    utc() + timedelta(minutes=3),
                    encode(WikiConfig().model_dump(mode="json")),
                    encode({"status": "unavailable", "reason": "not_scanned"}),
                ),
            )
            for alias in {
                channel,
                alias_key(configured_alias),
                *([alias_key(username)] if username else []),
            }:
                # Reassigned usernames must never merge two different Telegram channel IDs.
                await cur.execute(
                    "SELECT channel_id FROM wiki_channel_aliases WHERE alias=%s FOR UPDATE",
                    (alias,),
                )
                existing = await cur.fetchone()
                if existing and existing["channel_id"] != channel:
                    await cur.execute(
                        "UPDATE wiki_channels SET access_verified=FALSE,verified_until=NULL WHERE id=%s",
                        (existing["channel_id"],),
                    )
                    await cur.execute("DELETE FROM wiki_channel_aliases WHERE alias=%s", (alias,))
                await cur.execute(
                    "INSERT IGNORE INTO wiki_channel_aliases VALUES (%s,%s)", (alias, channel)
                )
        return await self.channel(channel)

    async def configure(self, channel, config: WikiConfig, actor):
        async with self.transaction() as cur:
            await cur.execute("SELECT id FROM wiki_channels WHERE id=%s FOR UPDATE", (channel,))
            if not await cur.fetchone():
                raise ValueError("Unknown channel")
            await cur.execute(
                """UPDATE wiki_channels SET config=%s,enabled=%s,public=%s,
                updated_at=UTC_TIMESTAMP(6),dirty_at=UTC_TIMESTAMP(6),source_generation=source_generation+1 WHERE id=%s""",
                (encode(config.model_dump(mode="json")), config.enabled, config.public, channel),
            )
            # Policy changes invalidate evidence immediately, including past snapshots.
            await cur.execute(
                """UPDATE wiki_sources SET excluded=(excluded OR
                (source_type='summary' AND %s=FALSE) OR (source_type='image' AND %s=FALSE)
                OR (source_type='message' AND NOT JSON_CONTAINS(%s,JSON_QUOTE(category)))
                OR (%s IS NOT NULL AND created_at<%s)) WHERE channel_id=%s""",
                (
                    config.include_summaries,
                    config.include_images,
                    encode(config.message_types),
                    config.history_since,
                    utc(config.history_since) if config.history_since else None,
                    channel,
                ),
            )
            # 策略翻转可能影响任意来源的可见性：保守登记全部页面待局部重建
            await cur.execute(
                "INSERT IGNORE INTO wiki_dirty_pages SELECT %s,id FROM wiki_pages WHERE channel_id=%s",
                (channel, channel),
            )
            await self.audit(
                cur, channel, "configure", channel, actor, config.model_dump(mode="json")
            )
            if not config.enabled or config.paused:
                await cur.execute(
                    """UPDATE wiki_jobs SET status='paused',lease_token=NULL,lease_until=NULL
                    WHERE channel_id=%s AND status IN ('running','pending','partial')""",
                    (channel,),
                )

    async def set_cover(self, channel, digest, image, actor):
        async with self.transaction() as cur:
            await cur.execute("SELECT id FROM wiki_channels WHERE id=%s FOR UPDATE", (channel,))
            if not await cur.fetchone():
                return False
            if image is None:
                await cur.execute("DELETE FROM wiki_channel_covers WHERE channel_id=%s", (channel,))
                if not cur.rowcount:
                    return True
            else:
                await cur.execute(
                    "SELECT digest FROM wiki_channel_covers WHERE channel_id=%s", (channel,)
                )
                previous = await cur.fetchone()
                if previous and previous["digest"] == digest:
                    return True
                await cur.execute(
                    """INSERT INTO wiki_channel_covers (channel_id,digest,image) VALUES (%s,%s,FROM_BASE64(%s))
                    AS uploaded ON DUPLICATE KEY UPDATE digest=uploaded.digest,image=uploaded.image""",
                    (channel, digest, base64.b64encode(image).decode("ascii")),
                )
            await cur.execute(
                "UPDATE wiki_channels SET updated_at=UTC_TIMESTAMP(6) WHERE id=%s",
                (channel,),
            )
            await self.audit(
                cur, channel, "remove_cover" if image is None else "upload_cover", channel, actor
            )
            return True

    async def audit(self, cur, channel, action, target, actor, details=None):
        await cur.execute(
            """INSERT INTO wiki_review_actions
            (channel_id,action,target_id,actor,details,created_at) VALUES (%s,%s,%s,%s,%s,UTC_TIMESTAMP(6))""",
            (channel, action, target, str(actor)[:128], encode(details or {})),
        )

    async def put_source(
        self,
        channel,
        source_type,
        external_id,
        body,
        created_at,
        *,
        edited_at=None,
        verified=False,
        message_id=None,
        original_length=0,
        excluded=False,
        exclusion_reason=None,
        media_group=None,
        category="text",
        media_key=None,
    ):
        source_id = digest(channel, source_type, str(external_id))
        content_hash = digest(body, media_key)
        changed_at = utc(edited_at or created_at)
        async with self.transaction() as cur:
            # Same lock order as atomic publication and all source revocations.
            await cur.execute("SELECT id FROM wiki_channels WHERE id=%s FOR UPDATE", (channel,))
            if not await cur.fetchone():
                return None
            await cur.execute("SELECT * FROM wiki_sources WHERE id=%s FOR UPDATE", (source_id,))
            old = await cur.fetchone()
            if old and (
                old["deleted"]
                or (changed_at < old["edited_at"] and (old["verified"] or not verified))
                or (old["verified"] and not verified)
            ):
                return source_id
            if (
                old
                and old["content_hash"] == content_hash
                and bool(old["verified"]) == verified
                and bool(old["excluded"]) == excluded
                and old["category"] == category
            ):
                if verified:
                    await cur.execute(
                        "UPDATE wiki_sources SET observed_at=UTC_TIMESTAMP(6),exclusion_reason=COALESCE(%s,exclusion_reason) WHERE id=%s",
                        (exclusion_reason, source_id),
                    )
                return source_id
            await cur.execute(
                """INSERT INTO wiki_sources
                (id,channel_id,source_type,external_id,message_id,body,original_length,content_hash,
                 created_at,edited_at,observed_at,verified,excluded,media_group,category,media_key,exclusion_reason)
                VALUES (%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,UTC_TIMESTAMP(6),%s,%s,%s,%s,%s,%s)
                ON DUPLICATE KEY UPDATE body=VALUES(body),original_length=VALUES(original_length),created_at=VALUES(created_at),
                content_hash=VALUES(content_hash),revision=revision+1,edited_at=VALUES(edited_at),
                observed_at=VALUES(observed_at),verified=VALUES(verified),excluded=VALUES(excluded),media_group=VALUES(media_group),category=VALUES(category),media_key=VALUES(media_key),exclusion_reason=VALUES(exclusion_reason)""",
                (
                    source_id,
                    channel,
                    source_type,
                    str(external_id),
                    message_id,
                    body,
                    original_length,
                    content_hash,
                    utc(created_at),
                    changed_at,
                    verified,
                    excluded,
                    media_group,
                    category,
                    media_key,
                    exclusion_reason or "",
                ),
            )
            for chunk in chunks(source_id, body, content_hash):
                await cur.execute(
                    """INSERT IGNORE INTO wiki_chunks
                    (id,source_id,content_hash,start_offset,end_offset) VALUES (%s,%s,%s,%s,%s)""",
                    (chunk["id"], source_id, content_hash, chunk["start"], chunk["end"]),
                )
            if not excluded or (old and not old["excluded"]):
                await cur.execute(
                    """UPDATE wiki_channels SET source_generation=source_generation+1,
                    dirty_at=UTC_TIMESTAMP(6) WHERE id=%s""",
                    (channel,),
                )
        return source_id

    async def revoke(
        self, channel, *, source_id=None, message_ids=None, actor="telegram", deleted=False
    ):
        async with self.transaction() as cur:
            await cur.execute("SELECT id FROM wiki_channels WHERE id=%s FOR UPDATE", (channel,))
            if not await cur.fetchone():
                return
            changed = 0
            if source_id:
                await cur.execute(
                    "UPDATE wiki_sources SET hidden=TRUE WHERE id=%s AND channel_id=%s AND hidden=FALSE",
                    (source_id, channel),
                )
                changed += cur.rowcount
            else:
                for message_id in message_ids or []:
                    # Tombstone even an unseen message: old backfill cannot resurrect it.
                    for kind in ("message", "image"):
                        sid = digest(channel, kind, str(message_id))
                        await cur.execute(
                            """INSERT INTO wiki_sources
                            (id,channel_id,source_type,external_id,message_id,body,content_hash,created_at,edited_at,observed_at,deleted)
                            VALUES (%s,%s,%s,%s,%s,'',%s,UTC_TIMESTAMP(6),UTC_TIMESTAMP(6),UTC_TIMESTAMP(6),TRUE)
                            ON DUPLICATE KEY UPDATE revision=revision+IF(deleted=FALSE,1,0),deleted=TRUE,body='',content_hash=VALUES(content_hash)""",
                            (sid, channel, kind, str(message_id), message_id, digest("deleted")),
                        )
                        changed += cur.rowcount
            if not changed:
                return
            await cur.execute(
                "UPDATE wiki_channels SET source_generation=source_generation+1,dirty_at=UTC_TIMESTAMP(6) WHERE id=%s",
                (channel,),
            )
            # 撤销会让相关页面的事实立即不可见：登记失效页面供增量装配局部重建
            if source_id:
                await cur.execute(
                    """INSERT IGNORE INTO wiki_dirty_pages
                    SELECT %s,f.page_id FROM wiki_facts f JOIN wiki_chunks ch ON ch.id=f.chunk_id
                    WHERE ch.source_id=%s""",
                    (channel, source_id),
                )
            elif message_ids:
                placeholders = ",".join(["%s"] * len(message_ids))
                await cur.execute(
                    f"""INSERT IGNORE INTO wiki_dirty_pages
                    SELECT DISTINCT %s,f.page_id FROM wiki_facts f
                    JOIN wiki_chunks ch ON ch.id=f.chunk_id JOIN wiki_sources s ON s.id=ch.source_id
                    WHERE s.channel_id=%s AND s.message_id IN ({placeholders})""",
                    (channel, channel, *message_ids),
                )
            await self.audit(
                cur,
                channel,
                "delete_source" if deleted else "hide_source",
                source_id,
                actor,
                {"messages": message_ids or []},
            )

    async def enqueue(self, channel, kind="update", actor="scheduler", page_id=None):
        if kind not in {"backfill", "update", "reconcile", "reindex", "page"}:
            raise ValueError("Unknown Wiki job kind")
        async with self.transaction() as cur:
            await cur.execute("SELECT * FROM wiki_channels WHERE id=%s FOR UPDATE", (channel,))
            record = await cur.fetchone()
            if not record or not record["enabled"]:
                raise ValueError("Wiki is disabled")
            if kind == "page":
                await cur.execute(
                    "SELECT id FROM wiki_pages WHERE id=%s AND channel_id=%s", (page_id, channel)
                )
                if not await cur.fetchone():
                    raise ValueError("A page in this channel is required")
            if record["active_job"]:
                return record["active_job"]
            job_id = uuid.uuid4().hex
            progress = {
                "enumerated": 0,
                "processed": 0,
                "excluded": 0,
                "input_tokens": 0,
                "output_tokens": 0,
                "cache_hits": 0,
                "generation_complete": False,
                "review_complete": False,
                "published": False,
            }
            await cur.execute(
                """INSERT INTO wiki_jobs
                (id,channel_id,kind,`cursor`,progress,next_attempt,created_at,updated_at)
                VALUES (%s,%s,%s,%s,%s,UTC_TIMESTAMP(6),UTC_TIMESTAMP(6),UTC_TIMESTAMP(6))""",
                (job_id, channel, kind, encode({"page_id": page_id}), encode(progress)),
            )
            await cur.execute(
                "UPDATE wiki_channels SET active_job=%s WHERE id=%s", (job_id, channel)
            )
            await self.audit(cur, channel, "enqueue", job_id, actor, {"kind": kind})
            return job_id

    async def claim_job(self):
        async with self.transaction() as cur:
            await cur.execute("""SELECT j.id FROM wiki_jobs j JOIN wiki_channels c ON c.id=j.channel_id
                WHERE c.enabled=TRUE AND JSON_EXTRACT(c.config,'$.paused')=FALSE
                AND c.active_job=j.id AND ((j.status IN ('pending','partial') AND j.next_attempt<=UTC_TIMESTAMP(6))
                OR (j.status='running' AND j.lease_until<UTC_TIMESTAMP(6)))
                ORDER BY c.public DESC,j.created_at LIMIT 1 FOR UPDATE SKIP LOCKED""")
            selected = await cur.fetchone()
            if not selected:
                return None
            await cur.execute("SELECT * FROM wiki_jobs WHERE id=%s", (selected["id"],))
            job = await cur.fetchone()
            job["lease_token"] = uuid.uuid4().hex
            await cur.execute(
                """UPDATE wiki_jobs SET status='running',error_code=NULL,
                progress=JSON_SET(progress,'$.error_detail','',
                    '$.reserved_tokens',0,'$.active_requests',JSON_OBJECT()),lease_token=%s,
                lease_until=DATE_ADD(UTC_TIMESTAMP(6),INTERVAL 90 SECOND),updated_at=UTC_TIMESTAMP(6) WHERE id=%s""",
                (job["lease_token"], job["id"]),
            )
            job["cursor"], job["progress"] = decode(job["cursor"]), decode(job["progress"])
            job["status"] = "running"
            job["error_code"] = None
            job["progress"]["error_detail"] = ""
            job["progress"]["reserved_tokens"] = 0
            job["progress"]["active_requests"] = {}
            return job

    async def fence(self, cur, job):
        await cur.execute(
            """SELECT id FROM wiki_jobs WHERE id=%s AND lease_token=%s AND status='running'
            AND lease_until>UTC_TIMESTAMP(6) FOR UPDATE""",
            (job["id"], job["lease_token"]),
        )
        if not await cur.fetchone():
            raise LeaseLost("Wiki job lease expired or was cancelled")

    async def heartbeat(self, job):
        count = await self.query(
            """UPDATE wiki_jobs SET lease_until=DATE_ADD(UTC_TIMESTAMP(6),INTERVAL 90 SECOND)
            WHERE id=%s AND lease_token=%s AND status='running' AND lease_until>UTC_TIMESTAMP(6)""",
            (job["id"], job["lease_token"]),
        )
        if not count:
            raise LeaseLost("Wiki job lease lost")

    async def checkpoint(self, job, phase=None, *, progress_only=False):
        if phase:
            job["phase"] = phase
        lock = self._checkpoint_locks.setdefault(job["id"], asyncio.Lock())
        async with lock:
            async with self.transaction() as cur:
                await self.fence(cur, job)
                if progress_only:
                    await cur.execute(
                        "UPDATE wiki_jobs SET progress=%s,updated_at=UTC_TIMESTAMP(6) WHERE id=%s",
                        (encode(job["progress"]), job["id"]),
                    )
                else:
                    await cur.execute(
                        "UPDATE wiki_jobs SET phase=%s,`cursor`=%s,progress=%s,updated_at=UTC_TIMESTAMP(6) WHERE id=%s",
                        (job["phase"], encode(job["cursor"]), encode(job["progress"]), job["id"]),
                    )

    async def report(self, job, activity, *, progress_only=False, **details):
        progress = job["progress"]
        if activity != progress.get("activity"):
            events = progress.setdefault("events", [])
            events.append({"time": utc().isoformat(), "text": activity})
            del events[:-12]
        progress.update(activity=activity, **details)
        await self.checkpoint(job, progress_only=progress_only)

    # 终态任务仅保留发布/展示所需的清单与叙述，窗口游标等临时状态不再回写
    TERMINAL_CURSOR_KEYS = (
        "page_id",
        "revisions",
        "dropped_pages",
        "profile_narrative",
        "profile_generation",
        "summary_topics",
    )

    async def finish(self, job, status="completed", error=None, delay=0):
        async with self.transaction() as cur:
            await cur.execute(
                "SELECT id FROM wiki_channels WHERE id=%s FOR UPDATE", (job["channel_id"],)
            )
            await self.fence(cur, job)
            cursor = job["cursor"]
            if status in {"completed", "published"}:
                cursor = {key: cursor[key] for key in self.TERMINAL_CURSOR_KEYS if key in cursor}
            await cur.execute(
                """UPDATE wiki_jobs SET status=%s,error_code=%s,attempts=attempts+%s,
                `cursor`=%s,progress=%s,lease_token=NULL,lease_until=NULL,
                next_attempt=DATE_ADD(UTC_TIMESTAMP(6),INTERVAL %s SECOND),updated_at=UTC_TIMESTAMP(6) WHERE id=%s""",
                (
                    status,
                    error,
                    int(bool(error)),
                    encode(cursor),
                    encode(job["progress"]),
                    delay,
                    job["id"],
                ),
            )
            if status in {"completed", "published", "failed"}:
                await cur.execute(
                    "UPDATE wiki_channels SET active_job=NULL WHERE id=%s AND active_job=%s",
                    (job["channel_id"], job["id"]),
                )
            if status == "failed":
                # A terminal failure must not immediately create another identical job
                # through the dirty/debounce path and defeat the retry limit.
                await cur.execute(
                    "UPDATE wiki_channels SET dirty_at=NULL,scheduled_at=UTC_TIMESTAMP(6) WHERE id=%s",
                    (job["channel_id"],),
                )

    async def control_job(self, channel, job_id, action, actor):
        if action not in {"pause", "resume", "retry"}:
            raise ValueError("Unknown job action")
        async with self.transaction() as cur:
            await cur.execute(
                "SELECT active_job FROM wiki_channels WHERE id=%s FOR UPDATE", (channel,)
            )
            channel_row = await cur.fetchone()
            await cur.execute(
                "SELECT * FROM wiki_jobs WHERE id=%s AND channel_id=%s FOR UPDATE",
                (job_id, channel),
            )
            job = await cur.fetchone()
            if not job or not channel_row:
                raise ValueError("Unknown job")
            if channel_row["active_job"] not in (None, job_id):
                raise ValueError("Another job is active")
            if job["status"] in {"completed", "published"}:
                raise ValueError("Completed jobs are immutable; start a new job")
            progress = decode(job["progress"])
            progress["active_requests"] = {}
            if action != "pause":
                progress.update(error_detail="", activity="等待后台执行")
            await cur.execute(
                """UPDATE wiki_jobs SET status=%s,lease_token=NULL,lease_until=NULL,
                attempts=0,error_code=NULL,next_attempt=UTC_TIMESTAMP(6),updated_at=UTC_TIMESTAMP(6),progress=%s WHERE id=%s""",
                ("paused" if action == "pause" else "pending", encode(progress), job_id),
            )
            await cur.execute(
                "UPDATE wiki_channels SET active_job=%s WHERE id=%s", (job_id, channel)
            )
            await self.audit(cur, channel, action, job_id, actor)

    async def source_batch(self, channel, after="", limit=100, page_id=None, group=None):
        if page_id:
            return await self.query(
                """SELECT s.* FROM wiki_sources s WHERE s.channel_id=%s AND s.id>%s
                AND s.deleted=FALSE AND s.hidden=FALSE AND s.excluded=FALSE
                AND EXISTS (SELECT 1 FROM wiki_facts f JOIN wiki_chunks ch ON ch.id=f.chunk_id WHERE ch.source_id=s.id AND f.page_id=%s)
                ORDER BY s.id LIMIT %s""",
                (channel, after, page_id, limit),
                fetch=True,
            )
        kind_filter = (
            " AND source_type='summary'"
            if group == "summary"
            else " AND source_type<>'summary'"
            if group == "sources"
            else ""
        )
        return await self.query(
            f"""SELECT * FROM wiki_sources WHERE channel_id=%s AND id>%s
            AND deleted=FALSE AND hidden=FALSE AND excluded=FALSE {kind_filter} ORDER BY id LIMIT %s""",
            (channel, after, limit),
            fetch=True,
        )
