# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
"""封禁后的本群用户历史清理；先记录意图，独立重试，不追溯旧封禁。"""

import logging
from datetime import datetime, timedelta

from telegram import Bot
from telegram.error import RetryAfter
from telethon import utils
from telethon.errors import UserNotParticipantError
from telethon.tl.types import PeerChat

from core.i18n.i18n import t
from core.infrastructure.database.mysql import utc_now_naive

from .telegram import call

logger = logging.getLogger(__name__)


def ban_generation(member):
    participant = getattr(member, "participant", None)
    date = getattr(participant, "date", None)
    return {
        "actor": getattr(participant, "kicked_by", None),
        "date": date.isoformat() if isinstance(date, datetime) else None,
    }


class BotIdentityMismatch(ValueError):
    pass


class BanHistoryCleanup:
    def __init__(self, service):
        self.service = service

    async def save(self, store, case, cleanup):
        payload = {
            **case["payload"],
            "ban_message_cleanup": cleanup,
            "ban_message_cleanup_dirty": True,
        }
        original = payload.get("report_message_deletion")
        if (
            cleanup["status"] == "deleted"
            and original
            and (original.get("chat_id"), original.get("user_id"), original.get("message_id"))
            == (case["chat_id"], case["user_id"], payload.get("message_id"))
        ):
            payload["report_message_deletion"] = {
                **original,
                "status": "deleted",
                "completed_at": cleanup["completed_at"],
                "error": None,
                "retry_at": None,
                "completed_via": "ban_message_cleanup",
            }
        await store.update_case(
            case["id"], case["status"], error=case.get("error"), payload=payload
        )
        case["payload"] = payload
        self.service._wake.set()

    async def prepare(self, store, case):
        # 只由确认成功的永久封禁路径调用；旧案件不会因升级生成清理任务。
        if case["payload"].get("ban_message_cleanup"):
            return
        source_ban = None
        try:
            member = await self.service.actions.permissions(case["chat_id"], case["user_id"])
            if getattr(getattr(member.participant, "banned_rights", None), "view_messages", False):
                source_ban = ban_generation(member)
        except Exception:
            # 封禁已成功；身份查询失败仍保留清理意图，由恢复任务重新核验。
            pass
        await self.save(
            store,
            case,
            {
                "status": "pending",
                "chat_id": case["chat_id"],
                "user_id": case["user_id"],
                "bot_id": self.service.bot_id,
                "actor": case.get("reviewer_actor") or "system:auto_ban",
                "requested_at": utc_now_naive().isoformat(),
                "attempts": 0,
                "error": None,
                "retry_at": None,
                "source_ban": source_ban,
            },
        )

    async def finish(self, store, case):
        """封禁终态落盘后立即清理，再发送最终通知；失败仍由持久化队列恢复。"""
        try:
            current = await store.case(case["id"])
            if current:
                await self.resume(store, current)
        except Exception:
            logger.warning("Guard 封禁消息清理待恢复 case=%s", case["id"], exc_info=True)

    async def revoke(self, chat_id, user_id, bot_id):
        # MTProto 的 deleteParticipantHistory 仅限用户帐号；复用主 Bot Token 调用 Bot API。
        from core.config import BOT_TOKEN

        async with Bot(BOT_TOKEN) as bot:
            if bot.id != bot_id:
                raise BotIdentityMismatch("Main bot identity changed")
            result = await bot.ban_chat_member(
                chat_id,
                user_id,
                revoke_messages=True,
                read_timeout=15,
                write_timeout=10,
                connect_timeout=10,
                pool_timeout=5,
            )
            if result is not True:
                raise ValueError("HistoryDeletionUnconfirmed")

    async def resume(self, store, case, *, force=False):
        cleanup = case["payload"].get("ban_message_cleanup")
        if not cleanup or cleanup.get("status") != "pending":
            return
        # 事务尚未确认封禁终态时先等待；解封/踢出等后续决定会取消清理。
        if case["status"] in {"executing", "reviewing_ban", "reviewing_blacklist"}:
            return
        target = (case["chat_id"], case["user_id"], self.service.bot_id)
        if (
            case["status"] not in {"pending", "blacklisted", "execution_failed"}
            or (cleanup.get("chat_id"), cleanup.get("user_id"), cleanup.get("bot_id")) != target
            or type(case["chat_id"]) is not int
            or not -(2**63) < case["chat_id"] < 0
            or type(case["user_id"]) is not int
            or case["user_id"] <= 0
            or case["user_id"] == self.service.bot_id
        ):
            await self.cancel(store, case, cleanup)
            return
        if (
            not force
            and cleanup.get("retry_at")
            and datetime.fromisoformat(cleanup["retry_at"]) > utc_now_naive()
        ):
            return
        try:
            basic = utils.resolve_id(case["chat_id"])[1] is PeerChat
            own = await self.service.actions.permissions(case["chat_id"], self.service.bot_id)
            rights = getattr(getattr(own, "participant", None), "admin_rights", None)
            if not (
                own.is_creator
                or (
                    own.is_admin
                    and (
                        basic
                        or (
                            getattr(rights, "ban_users", False)
                            and getattr(rights, "delete_messages", False)
                        )
                    )
                )
            ):
                raise PermissionError("MissingBanOrDeletePermission")
            try:
                member = await self.service.actions.permissions(case["chat_id"], case["user_id"])
            except UserNotParticipantError:
                member = None
            if basic:
                # 普通群没有永久禁令。只有确认目标已被移除时才撤回其旧消息，重新入群则取消。
                if member is not None:
                    await self.cancel(store, case, cleanup)
                    return
            elif member is None:
                await self.cancel(store, case, cleanup)
                return
            participant = getattr(member, "participant", None)
            if not basic and (
                member.is_admin
                or member.is_creator
                or not getattr(getattr(participant, "banned_rights", None), "view_messages", False)
                or (
                    ban_generation(member) != cleanup["source_ban"]
                    if cleanup.get("source_ban") is not None
                    else getattr(participant, "kicked_by", self.service.bot_id)
                    != self.service.bot_id
                )
            ):
                await self.cancel(store, case, cleanup)
                return
            await call(self.revoke(case["chat_id"], case["user_id"], self.service.bot_id))
        except BotIdentityMismatch:
            await self.cancel(store, case, cleanup)
        except Exception as error:
            attempts = cleanup.get("attempts", 0) + 1
            delay = min(900, 30 * 2 ** min(attempts - 1, 5))
            if isinstance(error, RetryAfter):
                wait = error.retry_after
                delay = max(1, wait.total_seconds() if isinstance(wait, timedelta) else wait)
            # 不保存异常原文，防止 HTTP 错误携带包含主 Bot Token 的 URL。
            await self.save(
                store,
                case,
                {
                    **cleanup,
                    "attempts": attempts,
                    "error": type(error).__name__,
                    "retry_at": (utc_now_naive() + timedelta(seconds=delay)).isoformat(),
                },
            )
        else:
            await self.save(
                store,
                case,
                {
                    **cleanup,
                    "status": "deleted",
                    "completed_at": utc_now_naive().isoformat(),
                    "error": None,
                    "retry_at": None,
                },
            )

    async def cancel(self, store, case, cleanup):
        await self.save(
            store,
            case,
            {
                **cleanup,
                "status": "cancelled",
                "completed_at": utc_now_naive().isoformat(),
                "error": "guard.ban_history_cancelled",
                "retry_at": None,
            },
        )


def history_cleanup_text(cleanup):
    state = t(f"guard.ban_history_{cleanup['status']}")
    error = cleanup.get("error")
    return t("guard.ban_history_details", state=state) + (
        f"（{error}）" if error and not error.startswith("guard.") else ""
    )
