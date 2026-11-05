# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot

"""Paged full-history scanning plus import of complete on-disk/SQL sources."""

import asyncio
import json
import os
import re
import sqlite3
import time
from pathlib import Path

from .models import alias_key, digest, utc
from .store import decode, encode


class SourceCollector:
    def __init__(self, store, client=None):
        self.store, self.client = store, client
        self.summary_refs = {}

    @staticmethod
    def policy_exclusion(config, created_at, category):
        if config.history_since and utc(created_at) < utc(config.history_since):
            return "before_history_since"
        if category not in config.message_types:
            return "message_type"
        return ""

    @staticmethod
    def summary_text(value):
        lines = value.splitlines()
        if lines and lines[0].lstrip().startswith("📋"):
            lines = lines[1:]
        value = re.sub(r"\[([^\]]+)\]\([^)]*\)", r"\1", "\n".join(lines))
        return re.sub(r"[\s*_`#>]+", "", value)

    async def prepare_summary_refs(self, channel):
        aliases = await self.store.query(
            "SELECT alias FROM wiki_channel_aliases WHERE channel_id=%s",
            (channel["id"],),
            fetch=True,
        )
        keys, refs, after = {row["alias"] for row in aliases}, {}, 0
        while True:
            rows = await self.store.query(
                "SELECT id,channel_id,created_at,summary_text,summary_message_ids FROM summaries WHERE id>%s ORDER BY id LIMIT 100",
                (after,),
                fetch=True,
            )
            if not rows:
                break
            for row in rows:
                after = row["id"]
                try:
                    belongs = alias_key(row["channel_id"]) in keys
                except ValueError:
                    belongs = False
                if belongs:
                    text = self.summary_text(row["summary_text"] or "")
                    if not text or not row["created_at"]:
                        continue
                    for mid in decode(row.get("summary_message_ids"), []) or []:
                        if str(mid).isdecimal():
                            refs.setdefault(int(mid), []).append((utc(row["created_at"]), text))
        self.summary_refs[channel["id"]] = refs

    def is_summary(self, channel, message_id, body, created_at):
        plain = self.summary_text(body)
        if not plain:
            return False
        for sent_at, text in self.summary_refs.get(channel, {}).get(message_id, []):
            # Legacy records can contain administrator-DM IDs. A number alone is
            # not a channel message identity: require matching send time and body.
            if abs((utc(created_at) - sent_at).total_seconds()) <= 600:
                if plain == text or (len(plain) >= 200 and plain in text):
                    return True
        return False

    async def repair_exclusions(self, channel):
        await self.prepare_summary_refs(channel)
        rows = await self.store.query(
            "SELECT * FROM wiki_sources WHERE channel_id=%s AND source_type='message' AND excluded=TRUE AND deleted=FALSE ORDER BY message_id",
            (channel["id"],),
            fetch=True,
        )
        known, inspect = {}, []
        for row in rows:
            reason = self.policy_exclusion(channel["config"], row["created_at"], row["category"])
            if reason:
                known.setdefault(reason, []).append(row["id"])
            else:
                inspect.append(row)
        for reason, ids in known.items():
            for start in range(0, len(ids), 250):
                batch = ids[start : start + 250]
                await self.store.query(
                    "UPDATE wiki_sources SET exclusion_reason=%s WHERE id IN ("
                    + ",".join(["%s"] * len(batch))
                    + ")",
                    (reason, *batch),
                )
        restored = []
        if inspect:
            if not self.client or not self.client.is_connected():
                raise ConnectionError("UserBot 未连接，旧排除记录暂未修改")
            from telethon.tl.types import PeerChannel

            for start in range(0, len(inspect), 100):
                batch = inspect[start : start + 100]
                messages = await self.client.get_messages(
                    PeerChannel(int(channel["id"])), ids=[row["message_id"] for row in batch]
                )
                for row, message in zip(batch, messages, strict=True):
                    if message and getattr(message, "date", None):
                        _, excluded = await self.capture(channel, message)
                        if not excluded:
                            restored.append(row["message_id"])
                    else:
                        await self.store.query(
                            "UPDATE wiki_sources SET exclusion_reason='unavailable' WHERE id=%s",
                            (row["id"],),
                        )
        counts = await self.store.one(
            "SELECT SUM(excluded) excluded FROM wiki_sources WHERE channel_id=%s AND source_type='message' AND deleted=FALSE",
            (channel["id"],),
        )
        await self.store.query(
            "UPDATE wiki_channels SET coverage=JSON_SET(coverage,'$.excluded',%s) WHERE id=%s",
            (int(counts["excluded"] or 0), channel["id"]),
        )
        if restored:
            placeholders = ",".join(["%s"] * len(restored))
            rows = await self.store.query(
                f"SELECT message_id FROM wiki_sources WHERE channel_id=%s AND source_type='message' AND excluded=FALSE AND message_id IN ({placeholders})",
                (channel["id"], *restored),
                fetch=True,
            )
            restored = [row["message_id"] for row in rows]
        return restored

    async def capture(self, channel, message, *, summary_ids=()):
        from core.media.image_pipeline import get_image_pipeline, image_media

        config = channel["config"]
        body = getattr(message, "message", None) or ""
        image = image_media(message)
        kind = "image" if image else "text" if body else "other"
        if channel["id"] not in self.summary_refs:
            await self.prepare_summary_refs(channel)
        reason = self.policy_exclusion(config, message.date, kind)
        if not reason and getattr(message, "action", None):
            reason = "service"
        if not reason and self.is_summary(channel["id"], message.id, body, message.date):
            reason = "generated_summary"
        excluded = bool(reason)
        source = await self.store.put_source(
            channel["id"],
            "message",
            message.id,
            body,
            message.date,
            edited_at=getattr(message, "edit_date", None),
            verified=True,
            message_id=message.id,
            original_length=len(body),
            excluded=excluded,
            exclusion_reason=reason,
            media_group=str(getattr(message, "grouped_id", "") or ""),
            category=kind,
            media_key=image,
        )
        if image and config.include_images and not excluded:
            pipeline = get_image_pipeline()
            if pipeline.tasks:
                # Reuse the existing durable image queue; never wait for vision here.
                from types import SimpleNamespace

                await pipeline.capture(
                    message,
                    SimpleNamespace(
                        id=int(channel["id"]), title=channel["title"], username=channel["username"]
                    ),
                )
            await self.capture_image(channel, message.id)
        return source, excluded

    async def capture_image(self, channel, message_id):
        from core.media.image_pipeline import combined_text

        row = await self.store.one(
            """SELECT m.*,a.ocr,a.description,a.updated_at FROM message_images m
            JOIN image_assets a ON a.sha256=m.sha256 WHERE m.channel_id=%s AND m.message_id=%s
            AND m.deleted=FALSE AND a.recognized=TRUE""",
            (channel["id"], message_id),
        )
        original = await self.store.one(
            "SELECT * FROM wiki_sources WHERE id=%s",
            (digest(channel["id"], "message", str(message_id)),),
        )
        if (
            not row
            or not original
            or original["deleted"]
            or original["excluded"]
            or not original["verified"]
        ):
            return
        await self.store.put_source(
            channel["id"],
            "image",
            message_id,
            combined_text("", row),
            row["source_date"],
            edited_at=max(row["updated_at"], original["edited_at"]),
            verified=True,
            message_id=message_id,
            excluded=not channel["config"].include_images,
            exclusion_reason="images_disabled" if not channel["config"].include_images else "",
            media_group=row.get("grouped_id"),
            category="image",
            media_key=row["media_id"],
        )

    async def import_local(self, job, channel):
        await self.store.report(job, "正在导入已有归档和总结", stage="import")
        await self.prepare_summary_refs(channel)
        aliases = await self.store.query(
            "SELECT alias FROM wiki_channel_aliases WHERE channel_id=%s",
            (channel["id"],),
            fetch=True,
        )
        keys = {row["alias"] for row in aliases}
        root = Path(os.getenv("MESSAGE_SOURCE_DIR", "data/message_sources"))
        # Directory entries are small; bodies are read individually outside the event loop.
        paths = await asyncio.to_thread(lambda: sorted(root.glob("*.json")))
        for path in paths:
            if path.name <= job["cursor"].get("archive", ""):
                continue
            try:
                record = await asyncio.to_thread(
                    lambda path=path: json.loads(path.read_text(encoding="utf-8"))
                )
                if alias_key(record.get("channel_id", "")) in keys:
                    await self.store.put_source(
                        channel["id"],
                        "message",
                        record["message_id"],
                        record["text"],
                        record["created_at"],
                        edited_at=record.get("edited_at"),
                        message_id=int(record["message_id"]),
                        original_length=len(record["text"]),
                    )
            except (ValueError, KeyError, OSError):
                job["progress"]["import_failures"] = job["progress"].get("import_failures", 0) + 1
            job["cursor"]["archive"] = path.name
            await self.store.checkpoint(job)
        vector_path = Path(os.getenv("VECTOR_DB_PATH", "data/vectors")) / "lite_index.sqlite3"
        if vector_path.exists():
            while True:
                try:
                    batch = await asyncio.to_thread(
                        read_vector_batch, vector_path, job["cursor"].get("vector", "")
                    )
                except (sqlite3.Error, OSError):
                    job["progress"]["vector_import_unavailable"] = True
                    break
                if not batch:
                    break
                for row in batch:
                    meta = decode(row["metadata"])
                    try:
                        if (
                            alias_key(meta.get("channel_id", "")) in keys
                            and meta.get("message_id")
                            and meta.get("created_at")
                        ):
                            await self.store.put_source(
                                channel["id"],
                                "message",
                                meta["message_id"],
                                row["document"],
                                meta["created_at"],
                                message_id=int(meta["message_id"]),
                                original_length=len(row["document"]),
                            )
                    except (ValueError, KeyError):
                        job["progress"]["import_failures"] = (
                            job["progress"].get("import_failures", 0) + 1
                        )
                    job["cursor"]["vector"] = row["id"]
                await self.store.checkpoint(job)
        # Enumerate every summary with keyset pagination, normalize legacy URLs before matching.
        while True:
            rows = await self.store.query(
                "SELECT * FROM summaries WHERE id>%s ORDER BY id LIMIT 100",
                (job["cursor"].get("summary", 0),),
                fetch=True,
            )
            if not rows:
                break
            for row in rows:
                try:
                    belongs = alias_key(row["channel_id"]) in keys
                except ValueError:
                    belongs = False
                if belongs:
                    # Summaries are derived context, never masquerade as verified original posts.
                    await self.store.put_source(
                        channel["id"],
                        "summary",
                        row["id"],
                        row["summary_text"],
                        row["created_at"],
                        excluded=not channel["config"].include_summaries,
                        exclusion_reason="summaries_disabled"
                        if not channel["config"].include_summaries
                        else "",
                    )
                    summary_ids = decode(row.get("summary_message_ids"), [])
                    job["cursor"].setdefault("summary_message_ids", []).extend(summary_ids)
                job["cursor"]["summary"] = row["id"]
            await self.store.checkpoint(job)

    async def history(self, job, channel):
        if not self.client or not self.client.is_connected():
            raise ConnectionError("Wiki UserBot is offline")
        from telethon.tl.types import PeerChannel

        entity = await self.client.get_entity(PeerChannel(int(channel["id"])))
        await self.store.register_channel(entity, channel["id"])
        if "estimated_total" not in job["progress"]:
            count = await self.client.get_messages(entity, limit=0)
            job["progress"]["estimated_total"] = getattr(count, "total", None)
        summary_ids = set(job["cursor"].get("summary_message_ids", []))
        await self.store.report(job, "正在读取 Telegram 历史消息", stage="history")
        last_checkpoint = time.monotonic()
        while True:
            count = 0
            # min_id + reverse scans the entire accessible history, not the previous week.
            async for message in self.client.iter_messages(
                entity,
                min_id=job["cursor"].get("history_id", 0),
                reverse=True,
                limit=100,
                wait_time=1,
            ):
                _, excluded = await self.capture(channel, message, summary_ids=summary_ids)
                job["cursor"]["history_id"] = message.id
                job["progress"]["enumerated"] += 1
                job["progress"]["excluded"] += int(excluded)
                job["progress"].setdefault("earliest", utc(message.date).isoformat())
                job["progress"]["latest"] = utc(message.date).isoformat()
                count += 1
                if count % 25 == 0 or time.monotonic() - last_checkpoint >= 2:
                    await self.store.checkpoint(job)
                    last_checkpoint = time.monotonic()
            if count == 0:
                break
            await self.store.checkpoint(job)
            await self.coverage(job, "partial", "scanning")
        await self.coverage(job, "accessible", "all_currently_accessible_messages_enumerated")

    async def reconcile(self, job, channel):
        """Re-read saved IDs to observe offline edits/deletes and unverified imports."""
        if not self.client or not self.client.is_connected():
            raise ConnectionError("Wiki UserBot is offline")
        from telethon.tl.types import PeerChannel

        # The history scan has just verified current messages. Only older or imported
        # records need another Telegram lookup to detect deletions and offline edits.
        cutoff = job["created_at"]
        counts = await self.store.one(
            """SELECT COUNT(*) total FROM wiki_sources WHERE channel_id=%s
            AND source_type='message' AND deleted=FALSE AND message_id>%s
            AND (verified=FALSE OR observed_at<%s)""",
            (channel["id"], job["cursor"].get("reconcile_id", 0), cutoff),
        )
        await self.store.report(
            job,
            "正在核对未确认的历史记录",
            stage="reconcile",
            reconcile_total=counts["total"],
            reconciled=0,
        )
        while True:
            rows = await self.store.query(
                """SELECT message_id FROM wiki_sources WHERE channel_id=%s
                AND source_type='message' AND deleted=FALSE AND message_id>%s
                AND (verified=FALSE OR observed_at<%s) ORDER BY message_id LIMIT 100""",
                (channel["id"], job["cursor"].get("reconcile_id", 0), cutoff),
                fetch=True,
            )
            if not rows:
                break
            messages = await self.client.get_messages(
                PeerChannel(int(channel["id"])), ids=[r["message_id"] for r in rows]
            )
            for row, message in zip(rows, messages, strict=True):
                if message and getattr(message, "date", None):
                    await self.capture(
                        channel, message, summary_ids=job["cursor"].get("summary_message_ids", [])
                    )
                else:
                    await self.store.revoke(
                        channel["id"], message_ids=[row["message_id"]], deleted=True
                    )
                job["cursor"]["reconcile_id"] = row["message_id"]
                job["progress"]["reconciled"] += 1
            await self.store.checkpoint(job)
        await self.coverage(job, "accessible", "all_currently_accessible_messages_enumerated")

    async def coverage(self, job, status, reason):
        counts = await self.store.one(
            """SELECT COUNT(*) indexed,MIN(created_at) earliest,MAX(created_at) latest,
            SUM(excluded) excluded FROM wiki_sources WHERE channel_id=%s AND source_type='message' AND deleted=FALSE""",
            (job["channel_id"],),
        )
        coverage = {
            **counts,
            "status": status,
            "reason": reason,
            "enumerated": job["progress"].get("enumerated", 0),
            "estimated_total": job["progress"].get("estimated_total"),
            "failures": job["progress"].get("collection_failures", 0),
            "missing_ranges": [],
            "updated_at": utc().isoformat(),
        }
        if job["progress"].get("import_failures"):
            coverage["status"] = "partial"
            coverage["reason"] = "some_archived_sources_unreadable"
        await self.store.query(
            "UPDATE wiki_channels SET coverage=%s WHERE id=%s",
            (encode(coverage), job["channel_id"]),
        )


def read_vector_batch(path, after):
    # SQLite is an optional import source, never the definition of history completeness.
    with sqlite3.connect(path.resolve().as_uri() + "?mode=ro", uri=True) as connection:
        connection.row_factory = sqlite3.Row
        return [
            dict(row)
            for row in connection.execute(
                "SELECT id,document,metadata_json AS metadata FROM messages_vectors WHERE id>? ORDER BY id LIMIT 100",
                (after,),
            )
        ]
