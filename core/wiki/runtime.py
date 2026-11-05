"""Wiki work stays inside the main service lifecycle and its APScheduler."""

import asyncio
import logging
from datetime import UTC, datetime, timedelta

from .collector import SourceCollector
from .engine import BudgetExceeded, WikiEngine
from .models import WikiConfig, alias_key, canonical_id, utc
from .outbox import EventOutbox
from .reader import PublicReader
from .store import LeaseLost, WikiStore, decode

logger = logging.getLogger(__name__)
_runtime = None


def get_wiki_runtime():
    return _runtime


async def start_wiki(db, client, scheduler, bot_client=None):
    global _runtime
    runtime = WikiRuntime(WikiStore(db), client, scheduler)
    runtime.bot_client = bot_client
    _runtime = runtime
    scheduler.add_job(
        runtime.tick,
        "interval",
        seconds=15,
        id="wiki_tick",
        replace_existing=True,
        max_instances=1,
        coalesce=True,
    )
    try:
        await runtime.start(bot_client)
    except Exception as error:
        logger.warning("Wiki initialization unavailable: %s", type(error).__name__)
        # Retry through the existing scheduler without taking down the main Bot.
        runtime.ready = False
    return runtime


class WikiRuntime:
    def __init__(self, store, client, scheduler):
        self.store, self.client, self.scheduler = store, client, scheduler
        self.collector = SourceCollector(store, client)
        self.engine = WikiEngine(store)
        self.reader = PublicReader(store)
        self.tasks = set()
        self.listeners = []
        self.ready = False
        self._last_refresh = None
        self.bot_client = None
        self.outbox = EventOutbox()
        self.enabled_channels = set()
        self.known_channels = set()

    async def start(self, bot_client=None):
        await self.store.initialize()
        await self.flush_outbox()
        self.ready = True
        if self.client and not self.listeners:
            from telethon import events

            for event_type, handler in [
                (events.NewMessage, self.on_message),
                (events.MessageEdited, self.on_message),
                (events.MessageDeleted, self.on_delete),
            ]:
                event = event_type()
                self.client.add_event_handler(handler, event)
                self.listeners.append((handler, event))
        if bot_client:
            from .commands import register_wiki_commands

            register_wiki_commands(bot_client, self)
        # Network verification runs in the scheduled tick, not the startup path.

    async def stop(self):
        self.ready = False
        if self.scheduler and self.scheduler.get_job("wiki_tick"):
            self.scheduler.remove_job("wiki_tick")
        if self.client:
            for handler, event in self.listeners:
                self.client.remove_event_handler(handler, event)
        for task in self.tasks:
            task.cancel()
        await asyncio.gather(*self.tasks, return_exceptions=True)
        self.tasks.clear()

    async def refresh_channels(self):
        import core.config as config_module

        allowed = set()
        for configured in config_module.CHANNELS:
            old = None
            try:
                key = alias_key(configured)
                old = await self.store.channel(key)
                if old:
                    allowed.add(old["id"])
                if self.client and self.client.is_connected():
                    from telethon.tl.types import PeerChannel

                    target = PeerChannel(int(key)) if key.isdecimal() else key
                    async with asyncio.timeout(15):
                        entity = await self.client.get_entity(target)
                    channel = await self.store.register_channel(entity, configured)
                    allowed.add(channel["id"])
            except Exception as error:
                logger.info("Wiki channel verification deferred: %s", type(error).__name__)
                if old:
                    await self.store.query(
                        "UPDATE wiki_channels SET access_verified=FALSE,verified_until=NULL WHERE id=%s",
                        (old["id"],),
                    )
        existing = await self.store.query("SELECT id,enabled FROM wiki_channels", fetch=True)
        self.known_channels = {row["id"] for row in existing}
        self.enabled_channels = {row["id"] for row in existing if row["enabled"]}
        for row in existing:
            if row["id"] not in allowed:
                await self.store.query(
                    "UPDATE wiki_channels SET access_verified=FALSE,verified_until=NULL WHERE id=%s",
                    (row["id"],),
                )
        self._last_refresh = utc()

    async def tick(self):
        if not self.ready:
            try:
                await self.start(self.bot_client)
            except Exception as error:
                logger.warning("Wiki initialization deferred: %s", type(error).__name__)
                return
        try:
            if not await self.flush_outbox():
                return
            if not self._last_refresh or utc() - self._last_refresh >= timedelta(minutes=1):
                await self.refresh_channels()
            for row in await self.store.query(
                "SELECT * FROM wiki_channels WHERE enabled=TRUE", fetch=True
            ):
                config = WikiConfig.model_validate(decode(row["config"]))
                if config.paused or row["active_job"]:
                    continue
                due = schedule_due(config, row.get("scheduled_at"))
                dirty = row["dirty_at"] and utc() - row["dirty_at"] >= timedelta(
                    seconds=config.debounce_seconds
                )
                if config.frequency != "manual" and (due or dirty):
                    reconcile = not row["reconciled_at"] or utc() - row[
                        "reconciled_at"
                    ] >= timedelta(days=7)
                    await self.store.enqueue(row["id"], "reconcile" if reconcile else "update")
                    await self.store.query(
                        "UPDATE wiki_channels SET scheduled_at=UTC_TIMESTAMP(6) WHERE id=%s",
                        (row["id"],),
                    )
            while len(self.tasks) < 2:
                job = await self.store.claim_job()
                if not job:
                    break
                task = asyncio.create_task(self.run_job(job))
                self.tasks.add(task)
                task.add_done_callback(self.tasks.discard)
        except Exception as error:
            logger.warning("Wiki tick deferred: %s", type(error).__name__)

    async def run_job(self, job):
        async def keep_lease():
            while True:
                await asyncio.sleep(20)
                await self.store.heartbeat(job)

        heartbeat = asyncio.create_task(keep_lease())
        try:
            channel = await self.store.channel(job["channel_id"])
            if job["phase"] == "collect":
                if job["kind"] in {"backfill", "reconcile"}:
                    await self.collector.import_local(job, channel)
                    await self.collector.history(job, channel)
                    await self.collector.reconcile(job, channel)
                    await self.store.query(
                        "UPDATE wiki_channels SET reconciled_at=UTC_TIMESTAMP(6) WHERE id=%s",
                        (channel["id"],),
                    )
                elif job["kind"] == "update":
                    # Recover new messages missed while UserBot was disconnected.
                    last = await self.store.one(
                        "SELECT MAX(message_id) id FROM wiki_sources WHERE channel_id=%s AND source_type='message'",
                        (channel["id"],),
                    )
                    job["cursor"].setdefault("history_id", last["id"] or 0)
                    await self.collector.history(job, channel)
                channel = await self.store.channel(job["channel_id"])
                job["progress"]["source_generation"] = channel["source_generation"]
                await self.store.checkpoint(job, "extract")
            if job["phase"] == "extract":
                await self.engine.generate(job, channel)
            if job["phase"] == "assemble":
                await self.engine.assemble(job, channel)
            if job["phase"] == "ready":
                fresh = await self.store.channel(channel["id"])
                if fresh["source_generation"] != job["progress"]["source_generation"]:
                    # A concurrent edit cannot be hidden behind a completed stale cursor.
                    job["cursor"].pop("source_id", None)
                    job["cursor"].pop("source_group", None)
                    job["cursor"].pop("summary_topics", None)
                    job["cursor"].pop("page_after", None)
                    job["cursor"]["revisions"] = {}
                    job["progress"]["source_generation"] = fresh["source_generation"]
                    job["progress"]["review_rejections"] = 0
                    await self.store.checkpoint(job, "extract")
                    await self.store.finish(job, "partial", delay=15)
                    return
                if (
                    fresh["config"].publication_mode == "auto"
                    and job["progress"]["review_complete"]
                    and job["progress"].get("pages")
                ):
                    pub = await self.engine.knowledge.publish(
                        channel["id"], job["id"], "wiki", running_job=job
                    )
                    job["progress"]["publication_id"] = pub
                    job["progress"]["published"] = True
                await self.store.query(
                    "UPDATE wiki_channels SET dirty_at=NULL WHERE id=%s AND source_generation=%s",
                    (channel["id"], job["progress"]["source_generation"]),
                )
                await self.store.finish(
                    job, "published" if job["progress"]["published"] else "completed"
                )
        except asyncio.CancelledError:
            raise
        except LeaseLost:
            logger.info("Wiki job lease superseded")
        except BudgetExceeded:
            await self.store.finish(job, "paused", "token_budget_reached")
        except Exception as error:
            # Only class names are persisted/logged. Provider payloads may contain secrets/text.
            logger.warning("Wiki job deferred: %s", type(error).__name__)
            try:
                channel = await self.store.channel(job["channel_id"])
                retries = channel["config"].retries if channel else 3
                delay = max(
                    min(3600, 2 ** (job["attempts"] + 1) * 15), int(getattr(error, "seconds", 0))
                )
                await self.collector.coverage(job, "partial", type(error).__name__)
                await self.store.finish(
                    job,
                    "failed" if job["attempts"] >= retries else "partial",
                    type(error).__name__,
                    delay,
                )
            except LeaseLost:
                pass
            except Exception:
                logger.warning("Wiki job persistence unavailable; lease recovery will retry")
        finally:
            heartbeat.cancel()
            await asyncio.gather(heartbeat, return_exceptions=True)

    async def on_message(self, event):
        try:
            if not event.is_channel:
                return
            chat = event.chat or await event.get_chat()
            cid = canonical_id(chat.id)
            if cid not in self.enabled_channels:
                candidate = await self.store.channel(cid)
                if not candidate or not candidate["enabled"]:
                    return
                self.enabled_channels.add(cid)
            message = event.message
            from core.media.image_pipeline import image_media

            media_key = image_media(message)
            record = {
                "kind": "message",
                "channel_id": cid,
                "message_id": message.id,
                "media_key": media_key,
                "category": "image" if media_key else "text",
                "body": getattr(message, "message", None) or "",
                "created_at": message.date.isoformat(),
                "edited_at": getattr(message, "edit_date", None).isoformat()
                if getattr(message, "edit_date", None)
                else None,
            }
            key = await asyncio.to_thread(self.outbox.put, record)
            channel = await self.store.channel(cid)
            if channel and channel["enabled"]:
                channel = await self.store.register_channel(chat, channel["id"])
                await self.collector.capture(channel, event.message)
            await asyncio.to_thread(self.outbox.acknowledge, key)
        except Exception as error:
            logger.warning("Wiki capture deferred: %s", type(error).__name__)

    async def on_delete(self, event):
        try:
            if event.chat_id:
                cid = canonical_id(event.chat_id)
                if cid not in self.known_channels and not await self.store.channel(cid):
                    return
                key = await asyncio.to_thread(
                    self.outbox.put, {"kind": "delete", "channel_id": cid, "ids": event.deleted_ids}
                )
                channel = await self.store.channel(cid)
                if channel:
                    await self.store.revoke(
                        channel["id"], message_ids=event.deleted_ids, deleted=True
                    )
                await asyncio.to_thread(self.outbox.acknowledge, key)
        except Exception as error:
            logger.warning("Wiki deletion deferred: %s", type(error).__name__)

    async def flush_outbox(self):
        pending = await asyncio.to_thread(self.outbox.pending)
        if pending:
            await self.store.query(
                "UPDATE wiki_channels SET access_verified=FALSE,verified_until=NULL WHERE enabled=TRUE"
            )
            self._last_refresh = None
        for key, record in pending:
            channel = await self.store.channel(record["channel_id"])
            if channel:
                if record["kind"] == "delete":
                    await self.store.revoke(channel["id"], message_ids=record["ids"], deleted=True)
                elif channel["enabled"]:
                    # These are actual Telegram events, unlike unverified archive imports.
                    await self.store.put_source(
                        channel["id"],
                        "message",
                        record["message_id"],
                        record["body"],
                        record["created_at"],
                        edited_at=record.get("edited_at"),
                        message_id=record["message_id"],
                        original_length=len(record["body"]),
                        verified=True,
                        media_key=record.get("media_key"),
                        category=record.get("category", "text"),
                    )
                    if not self.client or not self.client.is_connected():
                        continue
                    from telethon.tl.types import PeerChannel

                    message = await self.client.get_messages(
                        PeerChannel(int(channel["id"])), ids=record["message_id"]
                    )
                    if message and getattr(message, "date", None):
                        await self.collector.capture(channel, message)
                    else:
                        await self.store.revoke(
                            channel["id"], message_ids=[record["message_id"]], deleted=True
                        )
            await asyncio.to_thread(self.outbox.acknowledge, key)
        return not await asyncio.to_thread(self.outbox.pending, 1)


def schedule_due(config, previous, now=None):
    if config.frequency == "manual":
        return False
    from apscheduler.triggers.cron import CronTrigger

    expression = {"daily": "0 3 * * *", "weekly": "0 3 * * 0"}.get(config.frequency, config.cron)
    trigger = CronTrigger.from_crontab(expression, timezone=config.timezone)
    now = now or datetime.now(UTC)
    start = previous.replace(tzinfo=UTC) if previous else now - timedelta(days=7)
    next_time = trigger.get_next_fire_time(start, start + timedelta(microseconds=1))
    return next_time is not None and next_time <= now
