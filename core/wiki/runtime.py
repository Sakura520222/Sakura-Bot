# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot

"""Wiki work stays inside the main service lifecycle and its APScheduler."""

import asyncio
import logging
from datetime import UTC, datetime, timedelta

from .accounts import WikiAccounts
from .collector import SourceCollector
from .community import Community
from .engine import BudgetExceeded, WikiEngine, WikiResponseError
from .knowledge import PageEditConflict
from .models import WikiConfig, alias_key, canonical_id, utc
from .outbox import EventOutbox
from .reader import PublicReader
from .store import LeaseLost, WikiSchemaError, WikiStore, decode
from .web_research import WebResearch

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
        runtime.record_startup_error(error)
        # Retry through the existing scheduler without taking down the main Bot.
        runtime.ready = False
    return runtime


class WikiRuntime:
    def __init__(self, store, client, scheduler):
        self.store, self.client, self.scheduler = store, client, scheduler
        self.accounts = WikiAccounts(store)
        self.collector = SourceCollector(store, client)
        self.engine = WikiEngine(store)
        self.community = Community(store, self.engine.knowledge)
        self.research = WebResearch(store)
        self.engine.research = self.research
        self.reader = PublicReader(store)
        self.avatar = None
        self.tasks = set()
        self.listeners = []
        self.ready = False
        self.startup_error = ""
        self._last_refresh = None
        self._last_account_cleanup = None
        self.bot_client = None
        self.outbox = EventOutbox()
        self.recovery_keys = set()
        self._outbox_restored = False
        self.enabled_channels = set()
        self.known_channels = set()

    async def start(self, bot_client=None):
        await self.store.initialize()
        if not self._outbox_restored:
            self.recovery_keys.update(await asyncio.to_thread(self.outbox.keys))
            self._outbox_restored = True
        await self.flush_outbox()
        self.ready = True
        self.startup_error = ""
        if self.client or bot_client:
            from .avatars import WikiAvatarService

            self.avatar = self.avatar or WikiAvatarService(
                self.store, lambda: (self.client, self.bot_client)
            )
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

    def record_startup_error(self, error):
        self.ready = False
        self.startup_error = (
            str(error)
            if isinstance(error, WikiSchemaError)
            else f"Wiki 启动失败（{type(error).__name__}）"
        )
        logger.warning("%s", self.startup_error)

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

    def wake(self, delay=0):
        if self.scheduler:
            scheduled = self.scheduler.get_job("wiki_tick")
            when = datetime.now(UTC) + timedelta(seconds=delay)
            if scheduled and (not scheduled.next_run_time or scheduled.next_run_time > when):
                self.scheduler.modify_job("wiki_tick", next_run_time=when)

    async def tick(self):
        if not self.ready:
            try:
                await self.start(self.bot_client)
            except Exception as error:
                self.record_startup_error(error)
                return
        try:
            if not await self.flush_outbox():
                return
            if not self._last_refresh or utc() - self._last_refresh >= timedelta(minutes=1):
                await self.refresh_channels()
            if not self._last_account_cleanup or utc() - self._last_account_cleanup >= timedelta(
                hours=1
            ):
                await self.accounts.cleanup()
                await self.research.prune()
                self._last_account_cleanup = utc()
            for row in await self.store.query(
                """SELECT c.*,EXISTS (SELECT 1 FROM wiki_edit_proposals e
                WHERE e.channel_id=c.id AND e.status='approved' AND e.revision_id IS NULL) queued_edit
                FROM wiki_channels c WHERE c.enabled=TRUE""",
                fetch=True,
            ):
                config = WikiConfig.model_validate(decode(row["config"]))
                if row["queued_edit"]:
                    await self.community.dispatch_approved(row["id"])
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
            while True:
                job = await self.store.claim_job()
                if not job:
                    break
                task = asyncio.create_task(self.run_job(job))
                self.tasks.add(task)
                task.add_done_callback(self.tasks.discard)
        except Exception as error:
            logger.warning("Wiki tick deferred (%s): %s", type(error).__name__, error)

    async def run_job(self, job):
        owner = asyncio.current_task()

        async def keep_lease():
            try:
                while True:
                    await asyncio.sleep(20)
                    await self.store.heartbeat(job)
            except LeaseLost:
                owner.cancel()

        heartbeat = asyncio.create_task(keep_lease())
        page_edit = job["kind"] == "page_edit"
        try:
            channel = await self.store.channel(job["channel_id"])
            if job["phase"] == "collect":
                if job["kind"] in {"backfill", "reconcile"}:
                    await self.collector.import_local(job, channel)
                    await self.collector.history(job, channel)
                    # 全量枚举一完成就推进核对水位：之后的核对循环、提取或发布
                    # 失败都不会再把日常调度带回全量历史扫描（核对循环自身可从
                    # reconcile_id 续跑）。
                    await self.store.query(
                        "UPDATE wiki_channels SET reconciled_at=UTC_TIMESTAMP(6) WHERE id=%s",
                        (channel["id"],),
                    )
                    await self.collector.reconcile(job, channel)
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
            if job["phase"] == "ready" and not job["cursor"].get("review_policy"):
                # An unfinished pre-upgrade job may have reached ready using the old flags.
                await self.store.checkpoint(job, "review")
            if job["phase"] == "assemble":
                await self.store.report(job, "正在整理文章与知识地图", stage="assemble")
                await self.engine.assemble(job, channel)
            if job["phase"] == "review":
                await self.engine.review(job, channel)
            if job["phase"] == "ready":
                if job["kind"] in {"backfill", "reconcile"}:
                    await self.collector.coverage(
                        job, "accessible", "all_currently_accessible_messages_enumerated"
                    )
                fresh = await self.store.channel(channel["id"])
                # New messages do not invalidate a completed historical snapshot.
                # Publication still checks every included source/hash transactionally.
                if not page_edit:
                    job["progress"]["newer_sources_pending"] = (
                        fresh["source_generation"] != job["progress"]["source_generation"]
                    )
                if (
                    fresh["config"].publication_mode == "auto"
                    and job["progress"]["review_complete"]
                    and job["progress"].get("pages")
                ):
                    await self.store.report(
                        job,
                        "正在发布词条修订" if page_edit else "正在发布频道内容",
                        stage="publish",
                    )
                    pub = await self.engine.knowledge.publish(
                        channel["id"], job["id"], "wiki", running_job=job
                    )
                    job["progress"]["publication_id"] = pub
                    job["progress"]["published"] = True
                    if self.avatar and not page_edit:
                        task = asyncio.create_task(self.refresh_avatar(channel["id"]))
                        self.tasks.add(task)
                        task.add_done_callback(self.tasks.discard)
                if job["progress"]["published"]:
                    job["progress"]["activity"] = (
                        "词条修订已发布" if page_edit else "构建完成，内容已发布"
                    )
                elif job["progress"].get("review_rejections"):
                    job["progress"]["activity"] = "部分修订未通过来源审校，请在草稿中处理"
                else:
                    job["progress"]["activity"] = (
                        "词条修订已通过审校，等待发布" if page_edit else "构建完成"
                    )
                job["progress"]["stage"] = "done"
                job["progress"]["active_requests"] = {}
                if not page_edit:
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
        except BudgetExceeded as error:
            job["progress"]["error_detail"] = str(error)
            job["progress"]["activity"] = "任务预算不足，已保留当前进度"
            await self.store.finish(job, "paused", "token_budget_reached")
        except Exception as error:
            if isinstance(error, (WikiResponseError, PageEditConflict)):
                detail = str(error)
            elif getattr(error, "status_code", None):
                detail = f"模型服务返回 HTTP {error.status_code}"
            elif getattr(error, "seconds", None):
                detail = f"Telegram 限流，等待 {error.seconds} 秒后继续"
            elif isinstance(error, TimeoutError) or "Timeout" in type(error).__name__:
                detail = "请求长时间未返回数据，正在保留进度并安排重试"
            else:
                detail = f"处理失败：{type(error).__name__}"
            job["progress"].update(error_detail=detail, activity=detail, active_requests={})
            logger.warning("Wiki job %s (%s): %s", job["id"], job["phase"], detail)
            try:
                channel = await self.store.channel(job["channel_id"])
                retries = channel["config"].retries if channel else 3
                delay = int(getattr(error, "seconds", 0))
                if not delay and getattr(error, "status_code", None) == 429:
                    response = getattr(error, "response", None)
                    retry_after = response.headers.get("Retry-After", "5") if response else "5"
                    delay = int(retry_after) if retry_after.isdecimal() else 5
                if not delay:
                    delay = (
                        1
                        if isinstance(error, (WikiResponseError, ValueError))
                        else min(10, 2 ** job["attempts"])
                    )
                if page_edit and (
                    isinstance(error, PageEditConflict)
                    or str(error) == "Publication includes revoked, rejected or hidden evidence"
                ):
                    if not isinstance(error, PageEditConflict):
                        job["progress"].update(
                            error_detail="修订引用的来源已失效", activity="修订引用的来源已失效"
                        )
                    await self.store.finish(job, "failed", "page_edit_conflict")
                    return
                if str(error) == "Publication includes revoked, rejected or hidden evidence":
                    job["cursor"].pop("page_after", None)
                    job["cursor"]["revisions"] = {}
                    job["cursor"].pop("dropped_pages", None)
                    # 兜底全量重建：把所有页面标记为脏，防止失效登记遗漏的页面
                    await self.store.query(
                        "INSERT IGNORE INTO wiki_dirty_pages SELECT %s,id FROM wiki_pages WHERE channel_id=%s AND hidden=FALSE",
                        (job["channel_id"], job["channel_id"]),
                    )
                    job["progress"].update(
                        assembled_pages=0, pages=0, activity="来源有更新，正在刷新文章"
                    )
                    await self.store.checkpoint(job, "assemble")
                    delay = 1
                if str(error) == "Publication requires independent review of each new revision":
                    job["progress"]["review_complete"] = False
                    await self.store.checkpoint(job, "review")
                if job["phase"] == "collect":
                    job["progress"]["collection_failures"] = (
                        job["progress"].get("collection_failures", 0) + 1
                    )
                    await self.collector.coverage(job, "partial", type(error).__name__)
                await self.store.finish(
                    job,
                    "failed" if job["attempts"] >= retries else "partial",
                    type(error).__name__,
                    delay,
                )
                self.wake(delay)
            except LeaseLost:
                pass
            except Exception:
                logger.warning("Wiki job persistence unavailable; lease recovery will retry")
        finally:
            heartbeat.cancel()
            await asyncio.gather(heartbeat, return_exceptions=True)

    async def refresh_avatar(self, channel_id):
        """发布成功后后台回源一次频道头像；任何失败都只记录，不影响任务结果。"""
        try:
            await self.avatar.refresh(channel_id)
        except Exception as error:
            logger.info("Wiki avatar refresh deferred: %s", type(error).__name__)

    async def on_message(self, event):
        key = None
        try:
            if not event.is_channel:
                return
            chat = event.chat or await event.get_chat()
            cid = canonical_id(chat.id)
            if cid not in self.enabled_channels:
                if cid not in self.known_channels:
                    import core.config as config_module

                    # 新配置的频道仍可立即采集，普通群组不逐消息查询数据库。
                    candidates = {cid, (getattr(chat, "username", None) or "").lower()}
                    allowed = set()
                    for configured in config_module.CHANNELS:
                        try:
                            allowed.add(alias_key(configured))
                        except ValueError:
                            continue
                    if not candidates.intersection(allowed):
                        return
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
            self.recovery_keys.discard(key)
        except Exception as error:
            if key:
                self.recovery_keys.add(key)
            logger.warning("Wiki capture deferred: %s", type(error).__name__)

    async def on_delete(self, event):
        key = None
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
                self.recovery_keys.discard(key)
        except Exception as error:
            if key:
                self.recovery_keys.add(key)
            logger.warning("Wiki deletion deferred: %s", type(error).__name__)

    async def flush_outbox(self):
        if not self.recovery_keys:
            return True
        # 仅回放确认失败或重启遗留的事件；健康的在途写入由原处理器确认。
        pending = await asyncio.to_thread(self.outbox.pending, 100, self.recovery_keys.copy())
        if not pending:
            self.recovery_keys.intersection_update(await asyncio.to_thread(self.outbox.keys))
            return not self.recovery_keys
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
            self.recovery_keys.discard(key)
        return not self.recovery_keys


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
