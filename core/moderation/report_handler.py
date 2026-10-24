# Copyright 2026 Sakura-Bot
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
"""群成员引用举报：先保存证据，再通知管理员，等待明确的审核决定。"""

import hashlib
import logging
import re
import uuid

from telethon import events

from core.i18n.i18n import t

from .ai_report_repository import atomic
from .ai_reports import message_snapshot
from .matcher import Match, normalize
from .moderator import GuardError
from .telegram import call

logger = logging.getLogger(__name__)
REPORT_PATTERN = re.compile(r"\s*@admin\s+举报(?:\s+(.*?))?\s*", re.IGNORECASE | re.DOTALL)


class ReportHandler:
    def __init__(self, service):
        self.service = service

    async def handle(self, event):
        text = getattr(getattr(event, "message", None), "message", "") or ""
        match = REPORT_PATTERN.fullmatch(text)
        if (
            not match
            or not event.is_group
            or not event.sender_id
            or event.sender_id <= 0
            or event.sender_id == self.service.bot_id
            or getattr(event.message, "edit_date", None)
        ):
            return
        try:
            reporter = await call(self.service.client.get_entity(event.sender_id))
            if getattr(reporter, "bot", False):
                return
            permissions = await self.service.actions.permissions(event.chat_id, self.service.bot_id)
            if not (permissions.is_admin or permissions.is_creator):
                raise GuardError("guard.report_not_managed")
            self.service.directory.observe(event.chat_id, getattr(event, "chat", None))
            config = self.service.group(event.chat_id)
            if not config.notification_enabled or "moderation" not in config.notification_events:
                raise GuardError("guard.report_notifications_disabled")
            if not (
                config.notification_ids or self.service.default_admins() or config.review_chat_id
            ):
                raise GuardError("guard.report_no_admins")
            if not getattr(event.message, "reply_to_msg_id", None):
                raise GuardError("guard.report_usage")
            original = await call(event.get_reply_message())
            target_id = getattr(original, "sender_id", None)
            if (
                original is None
                or getattr(original, "chat_id", None) != event.chat_id
                or not isinstance(target_id, int)
                or target_id <= 0
                or target_id in {reporter.id, self.service.bot_id}
                or getattr(original, "action", None)
            ):
                raise GuardError("guard.report_invalid_target")
            case, created = await self.submit(
                event.chat_id, reporter, original, (match[1] or "").strip()[:256], event.id
            )
            notices = await self._notices(case)
            delivered = any(row.get("message_id") and not row.get("error") for row in notices)
            key = (
                "guard.report_submitted"
                if created and delivered
                else "guard.report_queued"
                if created
                else "guard.report_duplicate"
            )
            await event.reply(t(key, case_id=case["id"]), parse_mode=None)
        except GuardError as error:
            await event.reply(t(str(error)), parse_mode=None)
        except Exception:
            logger.exception(
                "Guard 举报处理失败 chat=%s reporter=%s", event.chat_id, event.sender_id
            )
            await event.reply(t("guard.report_failed"), parse_mode=None)
        raise events.StopPropagation

    async def _notices(self, case):
        async with self.service.repository.lock(case["user_id"]) as store:
            return await store.notices(case["id"])

    async def submit(self, chat_id, reporter, original, reason, report_message_id):
        # 对原消息去重，与目标用户的处罚及审核共用数据库锁，重启后仍有效。
        event_key = hashlib.sha256(f"report:{chat_id}:{original.id}".encode()).hexdigest()
        async with self.service.repository.lock(original.sender_id) as store:
            existing = await store.case_for_event(event_key)
            if existing:
                await self.service.notifier.sync(store, existing, self.service.group(chat_id))
                return existing, False
            user = await call(self.service.client.get_entity(original.sender_id))
            text = (getattr(original, "message", "") or "")[:2048]
            evidence = Match(
                "report",
                "user-report",
                reason or t("guard.report_reason"),
                text,
                normalize(text)[:2048],
                "report",
            )
            payload = await self.service.snapshot(chat_id, user, evidence, original)
            payload.update(
                reported_message=message_snapshot(original),
                reporter={
                    "user_id": reporter.id,
                    "name": " ".join(
                        value
                        for value in (
                            getattr(reporter, "first_name", None),
                            getattr(reporter, "last_name", None),
                        )
                        if value
                    )[:256],
                    "username": getattr(reporter, "username", None),
                },
                report_message_id=report_message_id,
                report_link=f"{payload['chat_link']}/{report_message_id}"
                if payload["chat_link"]
                else "",
                has_media=bool(getattr(original, "media", None)),
                message_date=str(getattr(original, "date", "") or ""),
            )
            case = {
                "id": uuid.uuid4().hex,
                "event_key": event_key,
                "chat_id": chat_id,
                "user_id": user.id,
                "action": "report",
                "status": "pending",
                "payload": payload,
            }
            if self.service.ai_reports.enabled(case):
                async with atomic(store):
                    await store.create_case(case)
                    await self.service.ai_reports.enqueue(store, case)
            else:
                await store.create_case(case)
            case = await store.case(case["id"])
            await self.service.notifier.sync(store, case, self.service.group(chat_id))
            return case, True
