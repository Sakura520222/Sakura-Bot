# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot

"""图片元数据和持久化任务；图片二进制仅存文件系统。"""

import uuid
from datetime import UTC

import aiomysql

SCHEMA = (
    """CREATE TABLE IF NOT EXISTS image_assets (
        sha256 CHAR(64) PRIMARY KEY, mime_type VARCHAR(64) NOT NULL,
        byte_size BIGINT NOT NULL, storage_path VARCHAR(512) NOT NULL,
        ocr LONGTEXT, description LONGTEXT, recognized BOOLEAN NOT NULL DEFAULT FALSE,
        model VARCHAR(255), updated_at DATETIME(6) NOT NULL
    ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4""",
    """CREATE TABLE IF NOT EXISTS message_images (
        channel_id VARCHAR(64) NOT NULL, message_id BIGINT NOT NULL,
        media_id VARCHAR(128) NOT NULL, grouped_id VARCHAR(64),
        channel_name VARCHAR(255) NOT NULL, channel_username VARCHAR(255),
        source_date DATETIME(6) NOT NULL, source_link VARCHAR(512) NOT NULL,
        caption LONGTEXT NOT NULL, sha256 CHAR(64),
        status VARCHAR(32) NOT NULL DEFAULT 'pending',
        attempts INT NOT NULL DEFAULT 0, last_error VARCHAR(255),
        next_attempt DATETIME(6) NOT NULL, lease_until DATETIME(6), lease_token CHAR(32),
        revision CHAR(32) NOT NULL, deleted BOOLEAN NOT NULL DEFAULT FALSE,
        PRIMARY KEY(channel_id, message_id), INDEX image_jobs(status,next_attempt),
        INDEX image_hash(sha256), INDEX image_source_time(channel_id,source_date)
    ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4""",
)


def utc_naive(value):
    if value.tzinfo is None:
        return value
    return value.astimezone(UTC).replace(tzinfo=None)


class ImageStore:
    def __init__(self, db):
        self.db = db

    async def query(self, sql, args=(), fetch=False):
        async with self.db.pool.acquire() as conn:
            async with conn.cursor(aiomysql.DictCursor) as cursor:
                await cursor.execute(sql, args)
                return await cursor.fetchall() if fetch else cursor.rowcount

    async def initialize(self):
        for sql in SCHEMA:
            await self.query(sql)

    async def capture(self, record):
        """同一媒体的重复事件不会重新排队已失败的任务；编辑说明文字会重新索引。"""
        current = await self.get(record["channel_id"], record["message_id"])
        if (
            current
            and not current["deleted"]
            and all(current[key] == record[key] for key in ("media_id", "caption"))
        ):
            return
        same_media = (
            current and not current["deleted"] and current["media_id"] == record["media_id"]
        )
        await self.query(
            """INSERT INTO message_images
            (channel_id,message_id,media_id,grouped_id,channel_name,channel_username,
             source_date,source_link,caption,sha256,next_attempt,revision)
            VALUES (%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,UTC_TIMESTAMP(6),%s)
            ON DUPLICATE KEY UPDATE media_id=VALUES(media_id),grouped_id=VALUES(grouped_id),
            channel_name=VALUES(channel_name),channel_username=VALUES(channel_username),
            source_date=VALUES(source_date),source_link=VALUES(source_link),caption=VALUES(caption),
            sha256=VALUES(sha256),status='pending',attempts=0,last_error=NULL,
            next_attempt=UTC_TIMESTAMP(6),lease_until=NULL,lease_token=NULL,
            revision=VALUES(revision),deleted=FALSE""",
            tuple(
                record[k]
                for k in (
                    "channel_id",
                    "message_id",
                    "media_id",
                    "grouped_id",
                    "channel_name",
                    "channel_username",
                    "source_date",
                    "source_link",
                    "caption",
                )
            )
            + (current["sha256"] if same_media else None, uuid.uuid4().hex),
        )

    async def get(self, channel_id, message_id):
        rows = await self.query(
            "SELECT * FROM message_images WHERE channel_id=%s AND message_id=%s",
            (channel_id, message_id),
            True,
        )
        return rows[0] if rows else None

    async def claim(self):
        token = uuid.uuid4().hex
        await self.query(
            """UPDATE message_images SET status='processing',lease_token=%s,
            lease_until=DATE_ADD(UTC_TIMESTAMP(6),INTERVAL 10 MINUTE)
            WHERE deleted=FALSE AND ((status='pending' AND next_attempt<=UTC_TIMESTAMP(6))
              OR (status='processing' AND lease_until<UTC_TIMESTAMP(6)))
            ORDER BY next_attempt LIMIT 1""",
            (token,),
        )
        rows = await self.query("SELECT * FROM message_images WHERE lease_token=%s", (token,), True)
        return rows[0] if rows else None

    async def claim_digest(self, digest):
        """Called under the shared recognition lock; retain revision/lease fencing."""
        token = uuid.uuid4().hex
        await self.query(
            """UPDATE message_images SET status='processing',lease_token=%s,
            lease_until=DATE_ADD(UTC_TIMESTAMP(6),INTERVAL 10 MINUTE)
            WHERE sha256=%s AND deleted=FALSE ORDER BY source_date LIMIT 1""",
            (token, digest),
        )
        rows = await self.query("SELECT * FROM message_images WHERE lease_token=%s", (token,), True)
        return rows[0] if rows else None

    async def update_job(self, job, status, sha256=None, error=None, max_attempts=3):
        attempts = job["attempts"] + (1 if error else 0)
        if error:
            status = "failed" if attempts >= max_attempts else "pending"
        return await self.query(
            """UPDATE message_images SET status=%s,sha256=COALESCE(%s,sha256),attempts=%s,
            last_error=%s,next_attempt=DATE_ADD(UTC_TIMESTAMP(6),INTERVAL %s SECOND),
            lease_until=NULL,lease_token=NULL WHERE channel_id=%s AND message_id=%s
            AND revision=%s AND deleted=FALSE AND lease_token=%s""",
            (
                status,
                sha256,
                attempts,
                error,
                min(300, 2**attempts),
                job["channel_id"],
                job["message_id"],
                job["revision"],
                job["lease_token"],
            ),
        )

    async def asset(self, sha256):
        rows = await self.query("SELECT * FROM image_assets WHERE sha256=%s", (sha256,), True)
        return rows[0] if rows else None

    async def save_asset(self, digest, mime, size, path):
        await self.query(
            """INSERT IGNORE INTO image_assets
            (sha256,mime_type,byte_size,storage_path,updated_at)
            VALUES (%s,%s,%s,%s,UTC_TIMESTAMP(6))""",
            (digest, mime, size, path),
        )

    async def recognize(self, digest, ocr, description, model, job):
        """版本检查和写入在同一条 UPDATE 中完成，防止强制重识别后旧请求又写入缓存。"""
        return bool(
            await self.query(
                """UPDATE image_assets a JOIN message_images m ON m.sha256=a.sha256
            SET a.ocr=%s,a.description=%s,a.model=%s,a.recognized=TRUE,
            a.updated_at=UTC_TIMESTAMP(6) WHERE a.sha256=%s AND m.channel_id=%s
            AND m.message_id=%s AND m.revision=%s AND m.lease_token=%s AND m.deleted=FALSE""",
                (
                    ocr,
                    description,
                    model,
                    digest,
                    job["channel_id"],
                    job["message_id"],
                    job["revision"],
                    job["lease_token"],
                ),
            )
        )

    async def text(self, channel_id, message_id):
        rows = await self.query(
            """SELECT a.ocr,a.description FROM message_images m JOIN image_assets a
            ON a.sha256=m.sha256 WHERE m.channel_id=%s AND m.message_id=%s
            AND m.deleted=FALSE AND a.recognized=TRUE""",
            (channel_id, message_id),
            True,
        )
        return rows[0] if rows else {}

    async def delete(self, channel_id, message_id):
        await self.query(
            """UPDATE message_images SET deleted=TRUE,status='deleted',revision=%s,
            caption='',lease_token=NULL WHERE channel_id=%s AND message_id=%s""",
            (uuid.uuid4().hex, channel_id, message_id),
        )

    async def retry(self, channel_id, message_id, force=False):
        job = await self.get(channel_id, message_id)
        if not job or job["deleted"]:
            return False
        if force and job["sha256"]:
            # 缓存作废和所有引用的版本变更由同一条 UPDATE 完成。
            await self.query(
                """UPDATE image_assets a JOIN message_images m ON m.sha256=a.sha256
                SET a.recognized=FALSE,m.status='pending',m.attempts=0,
                m.next_attempt=UTC_TIMESTAMP(6),m.revision=%s,m.lease_token=NULL
                WHERE a.sha256=%s AND m.deleted=FALSE""",
                (uuid.uuid4().hex, job["sha256"]),
            )
        else:
            await self.query(
                """UPDATE message_images SET status='pending',attempts=0,next_attempt=UTC_TIMESTAMP(6),
                revision=%s,lease_token=NULL WHERE channel_id=%s AND message_id=%s AND deleted=FALSE""",
                (uuid.uuid4().hex, channel_id, message_id),
            )
        return True

    async def stats(self):
        rows = await self.query(
            "SELECT status,COUNT(*) AS count,SUM(attempts) AS retries FROM message_images GROUP BY status",
            fetch=True,
        )
        return {
            row["status"]: {"count": row["count"], "retries": int(row["retries"] or 0)}
            for row in rows
        }
