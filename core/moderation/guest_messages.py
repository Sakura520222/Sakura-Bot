# Copyright 2026 Sakura-Bot
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
"""以 Telegram 的访客消息来源字段绑定调用者，分用户锁恢复联动封禁。"""

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


class GuestMessages:
    def __init__(self, service):
        self.service = service

    async def prepare(self, store, case):
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
            "guest_ban": {"status": "pending", "attempts": 0},
            "guest_ban_pending": True,
            "report_notice_dirty": True,
        }
        await store.update_case(
            case["id"], case["status"], error=case.get("error"), payload=payload
        )
        self.service._wake.set()

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
                                else "pending",
                                "case_id": child["id"] if child else None,
                                "error": child.get("error") if child else "GuestBanNotApplied",
                            }
            except Exception as error:
                result = {"status": "pending", "error": type(error).__name__}
        async with service.repository.lock(source_case["user_id"]) as store:
            parent = await store.case(case_id)
            if not parent or not parent["payload"].get("guest_ban_pending"):
                return
            attempts = task.get("attempts", 0) + 1
            result.update(attempts=attempts, updated_at=utc_now_naive().isoformat())
            if result["status"] == "pending":
                result["retry_at"] = (
                    utc_now_naive() + timedelta(seconds=min(900, 30 * attempts))
                ).isoformat()
            payload = {
                **parent["payload"],
                "guest_ban": result,
                "guest_ban_pending": result["status"] == "pending",
                "report_notice_dirty": True,
            }
            await store.update_case(
                case_id, parent["status"], error=parent.get("error"), payload=payload
            )
            await service.notifier.sync(
                store, await store.case(case_id), service.group(parent["chat_id"])
            )
