# Copyright 2026 Sakura-Bot
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
"""助手 Bot 自助申诉入口，不调用 AI、不消耗问答配额、不持有解封能力。"""

import asyncio
import logging
import re
from datetime import timedelta
from html import escape

from telegram import InlineKeyboardButton, InlineKeyboardMarkup
from telegram.error import BadRequest, TelegramError
from telegram.ext import ApplicationHandlerStop

from core.i18n.i18n import t
from core.infrastructure.database.mysql import utc_now_naive
from core.moderation.appeal_inbox import AppealInbox
from core.moderation.appeal_repository import AppealRepository
from core.moderation.appeal_ui import button, buttons, card, input_buttons, input_prompt
from core.moderation.moderator import GuardError
from core.moderation.repository import GuardRepository, encode
from core.telegram.keyboards import (
    QA_MENU_APPEAL,
    QA_MENU_ASK,
    QA_MENU_CHANNELS,
    QA_MENU_CLEAR,
    QA_MENU_HELP,
    QA_MENU_STATUS,
    QA_MENU_SUBMIT,
    QA_MENU_SUBSCRIPTIONS,
    build_qa_main_menu_keyboard,
)

logger = logging.getLogger(__name__)


class AppealHandler:
    def __init__(self, repository=None, submission_active=None):
        self.repository = repository
        self.inbox = AppealInbox(repository) if repository else None
        self.submission_active = submission_active or (lambda actor: False)

    async def initialize(self, pool):
        guard = GuardRepository(pool)
        await guard.initialize()
        self.repository = AppealRepository(guard)
        self.inbox = AppealInbox(self.repository)

    @staticmethod
    def private(update):
        return bool(
            update.effective_user
            and update.effective_chat
            and update.effective_chat.type == "private"
            and update.effective_chat.id == update.effective_user.id
            and not update.effective_user.is_bot
        )

    async def message(self, update, context):
        message = update.message
        if not message or not update.effective_user:
            return
        text = (message.text or "").strip()
        parts = text.split(maxsplit=1)
        command = parts[0].split("@")[0].lower() if parts else ""
        deep = command == "/start" and len(parts) > 1 and parts[1].startswith("appeal")
        entry_command = (
            command in {"/appeal", "/appeals", "/cancel_appeal"} or text == QA_MENU_APPEAL or deep
        )
        if not self.private(update):
            if entry_command:
                await message.reply_text(t("appeal.private_only"))
                raise ApplicationHandlerStop
            return
        actor = update.effective_user.id
        handled = entry_command
        try:
            entry = await self.repository.input(actor)
            if entry_command and self.submission_active(actor):
                raise GuardError("appeal.submission_busy")
            if command == "/cancel_appeal":
                await self.inbox.cancel(actor)
                await message.reply_text(
                    t("appeal.cancelled"), reply_markup=build_qa_main_menu_keyboard()
                )
            elif deep and re.fullmatch(r"appeal_[0-9a-f]{32}", parts[1]):
                appeal = await self.inbox.begin(actor, parts[1][7:])
                await self.show_user(context.bot, actor, appeal)
            elif entry_command:
                if entry and command != "/appeals":
                    await self.show_input(context.bot, actor, entry)
                else:
                    await self.list_rows(
                        context.bot, actor, "history" if command == "/appeals" else "cases"
                    )
            elif entry:
                handled = True
                if command == "/start":
                    await self.show_input(context.bot, actor, entry)
                elif text.startswith("/") or text in {
                    QA_MENU_ASK,
                    QA_MENU_CHANNELS,
                    QA_MENU_CLEAR,
                    QA_MENU_HELP,
                    QA_MENU_STATUS,
                    QA_MENU_SUBMIT,
                    QA_MENU_SUBSCRIPTIONS,
                }:
                    raise GuardError("appeal.input_busy")
                else:
                    media = self.extract_media(message)
                    if not media and not message.text:
                        raise GuardError("appeal.media_types")
                    entry = await self.inbox.append(
                        actor, text or (message.caption or "").strip(), media, message.message_id
                    )
                    await self.show_input(context.bot, actor, entry)
        except GuardError as error:
            await message.reply_text(t(str(error)))
        except Exception:
            if not handled:
                # 不能在无法确认是否有申诉输入时，把潜在申诉证据发给 AI。
                handled = True
            logger.exception("助手申诉输入失败 actor=%s", actor)
            await message.reply_text(t("appeal.unavailable"))
        if handled:
            raise ApplicationHandlerStop

    @staticmethod
    def extract_media(message):
        for kind in ("photo", "document", "video"):
            item = getattr(message, kind, None)
            if kind == "photo" and item:
                item = item[-1]
            if item:
                return {
                    "type": kind,
                    "file_id": item.file_id,
                    "size": item.file_size or 0,
                    "message_id": message.message_id,
                }
        return None

    async def show_input(self, bot, actor, entry):
        await bot.send_message(
            actor, input_prompt(entry), parse_mode="HTML", reply_markup=input_buttons(entry)
        )

    async def show_user(self, bot, actor, appeal):
        self.inbox.own(appeal, actor)
        entry = await self.repository.input(actor)
        if entry and entry["appeal_id"] == appeal["id"] and appeal["status"] == "draft":
            await self.show_input(bot, actor, entry)
        else:
            await bot.send_message(
                actor,
                card(appeal),
                parse_mode="HTML",
                reply_markup=buttons(appeal),
                disable_web_page_preview=True,
            )

    async def list_rows(self, bot, actor, kind, offset=0):
        if kind not in {"cases", "history"} or offset < 0 or offset > 10000:
            raise GuardError("guard.invalid_callback")
        rows = await self.repository.user_rows(actor, cases=kind == "cases", offset=offset)
        items = []
        for row in rows[:5]:
            payload = row["payload"] if kind == "cases" else row["payload"]["source"]["payload"]
            label = str(payload.get("chat_title") or row["chat_id"])[:35]
            if kind == "history":
                label += " · " + t(f"appeal.status_{row['status']}")
            items.append(
                [
                    InlineKeyboardButton(
                        label,
                        callback_data=f"ap:{'new' if kind == 'cases' else 'view'}:{row['id']}",
                    )
                ]
            )
        pages = []
        if offset:
            pages.append(button("previous", f"ap:list:{kind}:{max(0, offset - 5)}"))
        if len(rows) > 5:
            pages.append(button("next", f"ap:list:{kind}:{offset + 5}"))
        if pages:
            items.append(pages)
        items.append(
            [
                button(
                    "history" if kind == "cases" else "new",
                    f"ap:list:{'history' if kind == 'cases' else 'cases'}:0",
                )
            ]
        )
        await bot.send_message(
            actor,
            t(
                "appeal.choose"
                if rows and kind == "cases"
                else "appeal.history_title"
                if rows
                else "appeal.empty"
            ),
            reply_markup=InlineKeyboardMarkup(items),
        )

    async def callback(self, update, context):
        query = update.callback_query
        actor = update.effective_user.id if update.effective_user else None
        if not actor or not query or not query.message:
            return
        try:
            parts = (query.data or "").split(":")
            if len(parts) < 3 or parts[0] != "ap":
                raise GuardError("guard.invalid_callback")
            action = parts[1]
            if action in {"review", "new", "supp"} and self.submission_active(actor):
                raise GuardError("appeal.submission_busy")
            if action in {"review", "read"}:
                if len(parts) != 5 or not re.fullmatch(r"[0-9a-f]{32}", parts[2]):
                    raise GuardError("guard.invalid_callback")
                revision = int(parts[3])
                origin = (query.message.chat.id, query.message.message_id)
                if action == "read":
                    await self.inbox.read_request(actor, parts[2], revision, origin, parts[4])
                else:
                    entry = await self.inbox.review_input(
                        actor, parts[2], revision, origin, parts[4]
                    )
                    try:
                        await self.show_input(context.bot, actor, entry)
                    except TelegramError:
                        await query.answer(t("appeal.open_private"), show_alert=True)
                        return
                await query.answer(
                    t("appeal.queued" if action == "read" else "appeal.review_private"),
                    show_alert=True,
                )
                return
            if not self.private(update):
                raise GuardError("appeal.private_only")
            if action == "list" and len(parts) == 4:
                await self.list_rows(context.bot, actor, parts[2], int(parts[3]))
            elif action == "new" and len(parts) == 3:
                if self.submission_active(actor):
                    raise GuardError("appeal.submission_busy")
                appeal = await self.inbox.begin(actor, parts[2])
                await self.show_user(context.bot, actor, appeal)
            elif action == "view" and len(parts) == 3:
                await self.show_user(context.bot, actor, await self.repository.get(parts[2]))
            elif action == "supp" and len(parts) == 4:
                entry = await self.inbox.supplement(actor, parts[2], int(parts[3]))
                await self.show_input(context.bot, actor, entry)
            elif action == "withdraw" and len(parts) == 4:
                appeal = await self.inbox.withdraw(actor, parts[2], int(parts[3]))
                await self.show_user(context.bot, actor, appeal)
            elif action == "input" and len(parts) == 4:
                if parts[3] == "cancel":
                    await self.inbox.cancel(actor, parts[2])
                    await context.bot.send_message(
                        actor, t("appeal.cancelled"), reply_markup=build_qa_main_menu_keyboard()
                    )
                elif parts[3] == "submit":
                    appeal = await self.inbox.submit(actor, parts[2])
                    if appeal:
                        await self.show_user(context.bot, actor, appeal)
                    else:
                        await context.bot.send_message(
                            actor, t("appeal.queued"), reply_markup=build_qa_main_menu_keyboard()
                        )
                else:
                    raise GuardError("guard.invalid_callback")
            else:
                raise GuardError("guard.invalid_callback")
            await query.answer()
        except GuardError as error:
            await query.answer(t(str(error)), show_alert=True)
        except (TypeError, ValueError):
            await query.answer(t("guard.invalid_callback"), show_alert=True)
        except Exception:
            logger.exception("助手申诉按钮处理失败 actor=%s", actor)
            await query.answer(t("appeal.unavailable"), show_alert=True)
        finally:
            raise ApplicationHandlerStop

    async def deliver(self, context):
        try:
            notices, results = await self.repository.deliveries()
            for notice in notices:
                await self.deliver_notice(context.bot, notice)
            for request in results:
                await self.deliver_result(context.bot, request)
        except Exception:
            logger.exception("助手申诉通知轮询失败，将重试")

    async def deliver_notice(self, bot, candidate):
        async with self.repository.lock(candidate["user_id"]) as store:
            appeal = await store.get(candidate["appeal_id"])
            notice = next(
                (
                    row
                    for row in await store.notices(appeal["id"])
                    if row["target_id"] == candidate["target_id"]
                ),
                None,
            )
            if (
                not notice
                or notice["kind"] == "retired"
                or notice["synced_revision"] >= appeal["revision"]
            ):
                return
            try:
                kwargs = {
                    "text": card(appeal, reviewer=notice["kind"] == "reviewer"),
                    "parse_mode": "HTML",
                    "reply_markup": buttons(appeal, reviewer=notice["kind"] == "reviewer"),
                    "disable_web_page_preview": True,
                }
                message_id = notice["message_id"]
                if notice["kind"] == "user" or appeal["status"] == "pending":
                    message_id = None
                if message_id:
                    try:
                        await asyncio.wait_for(
                            bot.edit_message_text(
                                chat_id=notice["target_id"], message_id=message_id, **kwargs
                            ),
                            timeout=25,
                        )
                    except BadRequest as error:
                        if "not modified" not in str(error).lower():
                            if (
                                "not found" not in str(error).lower()
                                and "can't be edited" not in str(error).lower()
                            ):
                                raise
                            message_id = None
                if not message_id:
                    sent = await asyncio.wait_for(
                        bot.send_message(chat_id=notice["target_id"], **kwargs), timeout=25
                    )
                    message_id = sent.message_id
                async with store.atomic():
                    await store.guard.execute(
                        """UPDATE moderation_appeal_notifications SET message_id=%s,synced_revision=%s,
                        error=NULL,attempts=0,retry_at=%s WHERE appeal_id=%s AND target_id=%s""",
                        (
                            message_id,
                            appeal["revision"],
                            utc_now_naive(),
                            appeal["id"],
                            notice["target_id"],
                        ),
                    )
                    await store.event(
                        appeal,
                        "assistant",
                        "notification_sent",
                        appeal["status"],
                        {
                            "target_id": notice["target_id"],
                            "message_id": message_id,
                            "revision": appeal["revision"],
                        },
                    )
            except Exception as error:
                attempts = notice["attempts"] + 1
                await store.guard.execute(
                    """UPDATE moderation_appeal_notifications SET error=%s,attempts=%s,retry_at=%s
                    WHERE appeal_id=%s AND target_id=%s""",
                    (
                        type(error).__name__,
                        attempts,
                        utc_now_naive() + timedelta(seconds=min(1800, 15 * 2 ** min(attempts, 7))),
                        appeal["id"],
                        notice["target_id"],
                    ),
                )
                if notice.get("error") != type(error).__name__:
                    await store.event(
                        appeal,
                        "assistant",
                        "notification_failed",
                        appeal["status"],
                        {"target_id": notice["target_id"], "error": type(error).__name__},
                    )
                logger.warning("申诉通知发送失败 target=%s", notice["target_id"], exc_info=True)

    async def deliver_result(self, bot, request):
        appeal = await self.repository.get(request["appeal_id"])
        async with self.repository.lock(appeal["user_id"]) as store:
            current = await store.guard.one(
                "SELECT * FROM moderation_appeal_requests WHERE id=%s", (request["id"],)
            )
            if current["notified"]:
                return
            payload = current["payload"]
            items = []
            if current.get("error"):
                items.append(
                    ("text", t("appeal.request_failed", error=escape(t(current["error"]))))
                )
            elif "evidence" in payload:
                source = payload.get("source", {})
                original = source.get("payload", {})
                snapshot = original.get("evidence", {})
                if source:
                    items.append(
                        (
                            "text",
                            t(
                                "appeal.source_evidence",
                                case_id=source["id"],
                                at=escape(payload.get("source_created_at", "—")),
                                reason=escape(str(snapshot.get("reason", "—"))[:300]),
                                rule=escape(str(snapshot.get("rule_id", "—"))[:100]),
                                original=escape(str(snapshot.get("original", "—"))[:2048]),
                                link=escape(str(original.get("evidence_link") or "—")[:300]),
                            ),
                        )
                    )
                    if snapshot.get("normalized"):
                        items.append(
                            (
                                "text",
                                t(
                                    "appeal.source_normalized",
                                    text=escape(str(snapshot["normalized"])[:2048]),
                                ),
                            )
                        )
                for index, item in enumerate(payload["evidence"], 1):
                    items.append(
                        (
                            "text",
                            t(
                                "appeal.evidence_item",
                                index=index,
                                at=item["at"],
                                text=escape(item["text"]),
                            ),
                        )
                    )
                    items.extend(
                        (media["type"], media["file_id"]) for media in item.get("files", [])
                    )
            elif "audit" in payload:
                for item in payload["audit"]:
                    detail = item.get("detail", {})
                    items.append(
                        (
                            "text",
                            t(
                                "appeal.audit_item",
                                at=item["at"],
                                actor=escape(item["actor"]),
                                action=t(f"appeal.event_{item['action']}"),
                                status=t(f"appeal.status_{item['status']}"),
                                reason=escape(
                                    str(detail.get("reason") or detail.get("error") or "—")[:1000]
                                ),
                            ),
                        )
                    )
            else:
                items.append(
                    (
                        "text",
                        t(
                            "appeal.review_ack",
                            status=t(f"appeal.status_{payload.get('status', 'pending')}"),
                        ),
                    )
                )
            try:
                for index, (kind, value) in enumerate(items):
                    if index < payload.get("delivery_cursor", 0):
                        continue
                    if kind == "text":
                        await asyncio.wait_for(
                            bot.send_message(current["actor_id"], value, parse_mode="HTML"),
                            timeout=25,
                        )
                    else:
                        await asyncio.wait_for(
                            getattr(bot, f"send_{kind}")(current["actor_id"], value), timeout=25
                        )
                    payload["delivery_cursor"] = index + 1
                    await store.guard.execute(
                        "UPDATE moderation_appeal_requests SET payload=%s WHERE id=%s",
                        (encode(payload), current["id"]),
                    )
                await store.guard.execute(
                    "UPDATE moderation_appeal_requests SET notified=TRUE WHERE id=%s",
                    (current["id"],),
                )
            except Exception:
                await store.guard.execute(
                    "UPDATE moderation_appeal_requests SET retry_at=%s WHERE id=%s",
                    (utc_now_naive() + timedelta(seconds=60), current["id"]),
                )
                logger.warning("申诉审核回执发送失败 request=%s", current["id"], exc_info=True)
