# Copyright 2026 Sakura-Bot
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
"""以 Telegram 的访客消息来源字段绑定调用者，分用户锁恢复联动封禁。"""

import hashlib
import uuid
from datetime import timedelta

from telethon.tl.types import PeerUser

from core.i18n.i18n import t
from core.infrastructure.database.mysql import utc_now_naive

from .matcher import Match


def guest_actor(message):
    peer = getattr(message, "guestchat_via_from", None)
    sender = getattr(message, "sender_id", None)
    if (
        isinstance(peer, PeerUser)
        and type(peer.user_id) is int
        and 0 < peer.user_id < 2**63
        and type(sender) is int
        and sender > 0
        and peer.user_id != sender
    ):
        return peer.user_id
    return None


def guest_source(message):
    actor = guest_actor(message)
    if actor is None:
        return None
    return {
        "user_id": actor,
        "bot_id": message.sender_id,
        "message_id": message.id,
        "chat_id": message.chat_id,
        "source": "telegram:guestchat_via_from",
    }


def blacklist_task(payload, reviewer_id, actor, *, retry=False):
    """仅显式人工拉黑决定创建全局联动意图；普通/AI 封禁不升级。"""
    if not payload.get("guest_actor") or not (
        actor == "webui:admin"
        or (type(reviewer_id) is int and reviewer_id > 0 and actor == f"telegram:{reviewer_id}")
    ):
        return payload
    previous = payload.get("guest_ban", {})
    if previous.get("action") == "blacklist":
        if not retry or previous.get("status") != "skipped":
            return payload
        payload = {
            **payload,
            "guest_ban_history": [*payload.get("guest_ban_history", []), previous],
        }
    return {
        **payload,
        "guest_ban": {
            "action": "blacklist",
            "operation_id": uuid.uuid4().hex,
            "status": "pending",
            "attempts": 0,
            "actor": actor,
            "reviewer_id": reviewer_id,
            "requested_at": utc_now_naive().isoformat(),
        },
        "guest_ban_pending": True,
        "report_notice_dirty": True,
    }


def current_blacklist_task(parent, operation_id):
    return bool(
        parent
        and parent["status"] == "blacklisted"
        and parent["payload"].get("guest_ban_pending")
        and parent["payload"].get("guest_ban", {}).get("action") == "blacklist"
        and parent["payload"]["guest_ban"].get("operation_id") == operation_id
    )


class GuestMessages:
    def __init__(self, service):
        self.service = service

    @staticmethod
    def blacklist_key(case_id, task):
        return hashlib.sha256(
            f"guest-blacklist:{case_id}:{task['operation_id']}".encode()
        ).hexdigest()

    async def prepare(self, store, case, *, blacklist_by=None):
        if blacklist_by is not None:
            payload = blacklist_task(case["payload"], *blacklist_by, retry=True)
            if payload != case["payload"]:
                await store.update_case(
                    case["id"], case["status"], error=case.get("error"), payload=payload
                )
                self.service._wake.set()
            return
        source = case["payload"].get("guest_actor")
        if not source or case["payload"].get("guest_ban"):
            return
        if (
            case["action"] != "ban"
            and not case["payload"].get("review_ban_confirmed")
            and case["status"] != "blacklisted"
        ):
            return
        payload = {
            **case["payload"],
            "guest_ban": {"status": "pending", "attempts": 0, "operation_id": uuid.uuid4().hex},
            "guest_ban_pending": True,
            "report_notice_dirty": True,
        }
        await store.update_case(
            case["id"], case["status"], error=case.get("error"), payload=payload
        )
        self.service._wake.set()

    async def blacklist_allowed(self, store, parent, task):
        if not current_blacklist_task(parent, task.get("operation_id")):
            return False
        actor, reviewer = task.get("actor"), task.get("reviewer_id")
        if actor == "webui:admin":
            reviewer, web = "admin", True
        elif type(reviewer) is int and reviewer > 0 and actor == f"telegram:{reviewer}":
            web = False
        else:
            return False
        return bool(
            self.service.group(parent["chat_id"]).enabled
            and await self.service.authorized(reviewer, parent["chat_id"], web=web)
            and await store.blacklist(parent["user_id"])
        )

    async def blacklist_caller(self, store, parent, task, user, child):
        # 独立关联审计，不改写可能由其他群/管理员创建的现有封禁案件。
        key = self.blacklist_key(parent["id"], task)
        existing = await store.case_for_event(key)
        if existing:
            return existing
        latest = await self.service.repository.get_case(parent["id"])
        if not await self.blacklist_allowed(store, latest, task):
            return None
        if await self.service.protected(parent["chat_id"], user, applicant=True) or (
            await store.whitelisted(parent["chat_id"], user.id)
            and not await store.blacklist(user.id)
        ):
            return None
        match = Match(
            "guest_invoker",
            "guest-invoker-blacklist",
            t("guard.guest_blacklist_reason"),
            str(parent["payload"]["guest_actor"]["message_id"]),
            str(user.id),
            "ban",
        )
        payload = await self.service.snapshot(parent["chat_id"], user, match)
        payload.update(
            guest_source_case=parent["id"],
            guest_source=parent["payload"]["guest_actor"],
            guest_ban_case=child["id"],
            guest_blacklist_authorization=task,
            notification_event="guest_link_audit",
        )
        latest = await self.service.repository.get_case(parent["id"])
        if not await self.blacklist_allowed(store, latest, task):
            return None
        if await self.service.protected(parent["chat_id"], user, applicant=True) or (
            await store.whitelisted(parent["chat_id"], user.id)
            and not await store.blacklist(user.id)
        ):
            return None
        audit = {
            "id": key[:32],
            "event_key": key,
            "chat_id": parent["chat_id"],
            "user_id": user.id,
            "action": "guest_blacklist",
            "status": "done",
            "payload": payload,
            "reviewer_id": task.get("reviewer_id"),
            "reviewer_actor": "system:guest_message",
            "reviewed_at": utc_now_naive(),
            "created_at": utc_now_naive(),
        }
        await store.finish_review(
            audit, "blacklist", guest_parent=(parent["id"], task["operation_id"])
        )
        return await store.case(audit["id"])

    async def resume(self, case_id):
        service = self.service
        source_case = await service.repository.get_case(case_id)
        if not source_case or not source_case["payload"].get("guest_ban_pending"):
            return
        source = source_case["payload"].get("guest_actor", {})
        task = source_case["payload"].get("guest_ban", {})
        if task.get("retry_at") and task["retry_at"] > utc_now_naive().isoformat():
            return
        user_id = source.get("user_id")
        if (
            type(user_id) is not int
            or not 0 < user_id < 2**63
            or user_id in {service.bot_id, source_case["user_id"]}
            or source.get("source") != "telegram:guestchat_via_from"
            or (source.get("chat_id"), source.get("bot_id"), source.get("message_id"))
            != (
                source_case["chat_id"],
                source_case["user_id"],
                source_case["payload"].get("message_id"),
            )
        ):
            result = {"status": "skipped", "error": "InvalidGuestBinding"}
        else:
            try:
                # 不持有机器人用户锁时再取得调用者锁，避免两种身份之间出现锁顺序死锁。
                async with service.repository.lock(user_id) as store:
                    parent = await service.repository.get_case(case_id)
                    valid = (
                        parent
                        and parent["payload"].get("guest_ban_pending")
                        and parent["status"] in {"pending", "blacklisted", "execution_failed"}
                        and (
                            parent["action"] == "ban"
                            or parent["payload"].get("review_ban_confirmed")
                            or parent["status"] == "blacklisted"
                        )
                        and service.group(parent["chat_id"]).enabled
                    )
                    if not valid:
                        result = {"status": "skipped", "error": "SourceDecisionChanged"}
                    elif task.get("action") == "blacklist" and not await self.blacklist_allowed(
                        store, parent, task
                    ):
                        result = {
                            "status": "skipped",
                            "error": "GuestBlacklistAuthorizationChanged",
                        }
                    elif task.get("action") == "blacklist" and (
                        audit := await store.case_for_event(self.blacklist_key(case_id, task))
                    ):
                        listed = await store.blacklist(user_id)
                        result = {
                            "status": "blacklisted" if listed else "skipped",
                            "case_id": audit["id"],
                            "error": None if listed else "GuestBlacklistRemoved",
                        }
                    else:
                        user = await service.load_message_user(user_id)
                        config = service.group(parent["chat_id"])
                        exempt = await service.protected(
                            parent["chat_id"], user, applicant=True
                        ) or (
                            await store.whitelisted(parent["chat_id"], user_id)
                            and not (
                                config.share_global_blacklist and await store.blacklist(user_id)
                            )
                        )
                        if exempt:
                            result = {"status": "skipped", "error": "ProtectedGuestCaller"}
                        else:
                            match = Match(
                                "guest_invoker",
                                "guest-invoker-ban",
                                t("guard.guest_ban_reason"),
                                str(source["message_id"]),
                                str(source["message_id"]),
                                "ban",
                            )
                            child = await service.punish(
                                store,
                                parent["chat_id"],
                                user,
                                match,
                                f"guest:{case_id}:{user_id}",
                                extra_payload={
                                    "guest_source_case": case_id,
                                    "guest_source": source,
                                    "notification_event": "guest_link_audit",
                                },
                            )
                            if child and child["status"] == "execution_failed":
                                await store.update_case(child["id"], "executing")
                                await service.execute_case(store, await store.case(child["id"]))
                                child = await store.case(child["id"])
                            result = {
                                "status": "banned"
                                if child and child["status"] in {"pending", "blacklisted"}
                                else "skipped"
                                if child
                                and child["status"]
                                in {
                                    "allowed",
                                    "unbanned",
                                    "cancelled",
                                    "kicked",
                                    "unmuted",
                                    "ignored",
                                }
                                else "pending",
                                "case_id": child["id"] if child else None,
                                "error": child.get("error") if child else "GuestBanNotApplied",
                            }
                            if result["status"] == "banned" and task.get("action") == "blacklist":
                                audit = await self.blacklist_caller(
                                    store, parent, task, user, child
                                )
                                listed = await store.blacklist(user_id)
                                result = {
                                    "status": "blacklisted" if audit and listed else "skipped",
                                    "case_id": audit["id"] if audit else child["id"],
                                    "error": None
                                    if audit and listed
                                    else "GuestBlacklistDecisionChanged",
                                }
            except Exception as error:
                result = {"status": "pending", "error": type(error).__name__}
        async with service.repository.lock(source_case["user_id"]) as store:
            parent = await store.case(case_id)
            if (
                not parent
                or not parent["payload"].get("guest_ban_pending")
                or parent["payload"].get("guest_ban", {}).get("operation_id")
                != task.get("operation_id")
            ):
                return
            attempts = task.get("attempts", 0) + 1
            result.update(attempts=attempts, updated_at=utc_now_naive().isoformat())
            if result["status"] == "pending":
                result["retry_at"] = (
                    utc_now_naive() + timedelta(seconds=min(900, 30 * attempts))
                ).isoformat()
            payload = {
                **parent["payload"],
                "guest_ban": {**task, **result},
                "guest_ban_pending": result["status"] == "pending",
                "report_notice_dirty": True,
            }
            await store.update_case(
                case_id, parent["status"], error=parent.get("error"), payload=payload
            )
            await service.notifier.sync(
                store, await store.case(case_id), service.group(parent["chat_id"])
            )
