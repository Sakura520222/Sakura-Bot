# Copyright 2026 Sakura-Bot
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
"""主进程封禁复核：实时授权、持久化决定、复用 Guard 解封与名单。"""

import asyncio
import hashlib
import json
import logging
from datetime import datetime, timedelta

from telethon.errors import UserNotParticipantError

from core.infrastructure.database.mysql import utc_now_naive

from .appeal_repository import READ_ACTIONS, REVIEW_ACTIONS, AppealRepository
from .moderator import GuardError
from .notification import is_ban_case
from .telegram import rights_dict, same_rights

logger = logging.getLogger(__name__)


def source_signature(case):
    value = [
        case["id"],
        case["action"],
        case["status"],
        case["payload"].get("review_generation", 0),
    ]
    return hashlib.sha256(json.dumps(value).encode()).hexdigest()


def eligible(case):
    return bool(
        case
        and (is_ban_case(case) or case["status"] == "blacklisted")
        and case["status"] in {"pending", "blacklisted", "done"}
    )


def ban_context(permissions):
    participant = permissions.participant
    date = getattr(participant, "date", None)
    return {
        "rights": rights_dict(getattr(participant, "banned_rights", None)),
        "actor": getattr(participant, "kicked_by", None),
        "date": date.isoformat() if isinstance(date, datetime) else None,
    }


class AppealWorkflow:
    def __init__(self, service, repository=None):
        self.service = service
        self.repository = repository or AppealRepository(service.repository)
        self._worker = None
        self._routing_changed = False

    def routing_changed(self):
        self._routing_changed = True

    async def start(self):
        self._worker = asyncio.create_task(self._run(), name="sakura-ban-appeals")

    async def stop(self):
        if self._worker:
            self._worker.cancel()
            try:
                await self._worker
            except asyncio.CancelledError:
                pass

    async def _run(self):
        while True:
            try:
                await self.recover_once()
            except Exception:
                logger.exception("封禁复核后台任务失败，将重试")
            await asyncio.sleep(5)

    async def recover_once(self):
        if self._routing_changed:
            await self.repository.invalidate_routing()
            self._routing_changed = False
        appeals, requests = await self.repository.work()
        for request in requests:
            try:
                async with self.repository.lock(request["user_id"]) as store:
                    current = await store.guard.one(
                        "SELECT * FROM moderation_appeal_requests WHERE id=%s", (request["id"],)
                    )
                    if current and current["status"] == "pending":
                        await self.process_request(store, current)
            except Exception:
                logger.exception("申诉审核请求暂未完成 request=%s", request["id"])
        for candidate in appeals:
            try:
                async with self.repository.lock(candidate["user_id"]) as store:
                    appeal = await store.get(candidate["id"])
                    if not appeal:
                        continue
                    if appeal["status"] == "draft":
                        if appeal["updated_at"] < utc_now_naive() - timedelta(days=1):
                            async with store.atomic():
                                await store._transition(appeal, "withdrawn", "system", "expired")
                                entry = await store.input(appeal["user_id"])
                                if entry and entry["appeal_id"] == appeal["id"]:
                                    await store.clear_input(appeal["user_id"], entry["token"])
                        continue
                    if appeal["status"] in {"submitted", "pending", "need_info"}:
                        case = await store.guard.case(appeal["case_id"])
                        valid = (
                            eligible(case)
                            and source_signature(case) == appeal["payload"]["source_signature"]
                        )
                        if appeal["status"] == "submitted" or not valid:
                            appeal = await store.transition(
                                appeal,
                                "pending" if valid else "superseded",
                                "system",
                                "accepted" if valid else "superseded",
                            )
                    if appeal["status"] == "executing" and appeal["retry_at"] <= utc_now_naive():
                        await self.execute(store, appeal)
                        appeal = await store.get(appeal["id"])
                    if appeal["published_revision"] < appeal["revision"]:
                        await self.publish(store, appeal)
            except Exception:
                logger.exception("申诉状态恢复失败 appeal=%s", candidate["id"])

    async def publish(self, store, appeal):
        config = self.service.group(appeal["chat_id"])
        targets = [
            *(config.reviewer_ids or self.service.default_admins()),
            *config.notification_ids,
        ]
        if appeal["payload"].get("blacklisted"):
            targets.extend(self.service.default_admins())
        if config.review_chat_id:
            targets.append(config.review_chat_id)
        if not appeal["payload"].get("evidence"):
            # 从未确认提交的草稿撤回/过期，不向管理员提交任何审核通知。
            targets = []
        await store.publish(appeal, targets)

    async def check_request(self, store, appeal, request):
        if request["action"] not in REVIEW_ACTIONS | READ_ACTIONS:
            raise GuardError("guard.invalid_callback")
        if request["actor_id"] == appeal["user_id"] or not await self.service.authorized(
            request["actor_id"], appeal["chat_id"]
        ):
            raise GuardError("error.permission_denied")
        if (
            request["action"] == "global"
            and request["actor_id"] not in self.service.default_admins()
        ):
            raise GuardError("appeal.global_admin_required")
        if request["revision"] != appeal["revision"]:
            raise GuardError("appeal.stale")
        origin = (request["origin_chat_id"], request["origin_message_id"])
        if origin[0] > 0 and origin[0] != request["actor_id"]:
            raise GuardError("guard.invalid_callback")
        if not any(
            row["kind"] == "reviewer"
            and (row["target_id"], row["message_id"]) == origin
            and row["synced_revision"] == appeal["revision"]
            for row in await store.notices(appeal["id"])
        ):
            raise GuardError("guard.invalid_callback")
        if request["action"] in REVIEW_ACTIONS and appeal["status"] != "pending":
            raise GuardError("appeal.stale")

    async def process_request(self, store, request):
        appeal = await store.get(request["appeal_id"])
        if not appeal:
            await store.finish_request(request, error="guard.invalid_callback")
            return
        # 意图或决定已提交、但请求回执提交失败时，不能重复执行该按钮。
        if appeal["payload"].get("last_request_id") == request["id"]:
            await store.finish_request(request, payload={"status": appeal["status"]})
            return
        try:
            await self.check_request(store, appeal, request)
            action = request["action"]
            if action == "evidence":
                await store.finish_request(
                    request,
                    payload={
                        "evidence": appeal["payload"].get("evidence", []),
                        "source": appeal["payload"]["source"],
                        "source_created_at": appeal["payload"].get("source_created_at", "—"),
                    },
                )
                return
            if action == "audit":
                events = await store.events(appeal["id"])
                await store.finish_request(
                    request,
                    payload={
                        "audit": [
                            {
                                "actor": row["actor"],
                                "action": row["action"],
                                "at": str(row["created_at"]),
                                "status": row["to_status"],
                                "detail": row["payload"],
                            }
                            for row in events
                        ]
                    },
                )
                return
            if action == "more" and len(appeal["payload"].get("evidence", [])) >= 6:
                raise GuardError("appeal.evidence_limit")
            reason = request["payload"].get("reason", "").strip()
            if not 1 <= len(reason) <= 1000:
                raise GuardError("appeal.reason_required")
            decision = {
                "actor_id": request["actor_id"],
                "action": action,
                "reason": reason,
                "authorized_at": utc_now_naive().isoformat(),
                "request_id": request["id"],
            }
            payload = {
                **appeal["payload"],
                "last_request_id": request["id"],
                "last_review": decision,
            }
            if action in {"reject", "more"}:
                appeal = await store.transition(
                    appeal,
                    "rejected" if action == "reject" else "need_info",
                    f"telegram:{request['actor_id']}",
                    "rejected" if action == "reject" else "requested_info",
                    payload=payload,
                    detail=decision,
                )
            else:
                await self.check_source(store, appeal)
                black = await store.guard.blacklist(appeal["user_id"])
                if black and action != "global":
                    raise GuardError("guard.blacklist_conflict")
                decision["blacklist_case_id"] = black["case_id"] if black else None
                decision["ban"] = await self.current_ban(appeal)
                payload.update(decision=decision, blacklisted=bool(black))
                appeal = await store.transition(
                    appeal,
                    "executing",
                    f"telegram:{request['actor_id']}",
                    "approved_intent",
                    payload=payload,
                    detail=decision,
                )
                await self.execute(store, appeal)
                appeal = await store.get(appeal["id"])
            await self.publish(store, appeal)
            await store.finish_request(request, payload={"status": appeal["status"]})
        except GuardError as error:
            refresh_blacklist = str(error) == "guard.blacklist_conflict" and not appeal[
                "payload"
            ].get("blacklisted")
            async with store.atomic():
                await store.event(
                    appeal,
                    f"telegram:{request['actor_id']}",
                    "request_denied",
                    appeal["status"],
                    {"action": request["action"], "error": str(error)},
                )
                if refresh_blacklist:
                    appeal = await store._transition(
                        appeal,
                        appeal["status"],
                        "system",
                        "blacklist_changed",
                        payload={**appeal["payload"], "blacklisted": True},
                        error=str(error),
                    )
                await store.finish_request(request, error=str(error))
            if refresh_blacklist:
                await self.publish(store, appeal)

    async def check_source(self, store, appeal):
        case = await store.guard.case(appeal["case_id"])
        if (
            not eligible(case)
            or (case["chat_id"], case["user_id"]) != (appeal["chat_id"], appeal["user_id"])
            or source_signature(case) != appeal["payload"]["source_signature"]
        ):
            raise GuardError("appeal.source_changed")
        other = await store.guard.active_ban(
            appeal["chat_id"], appeal["user_id"], exclude_case_id=case["id"]
        )
        if other:
            raise GuardError("guard.other_ban_conflict")
        return case

    async def current_ban(self, appeal):
        try:
            permissions = await self.service.actions.permissions(
                appeal["chat_id"], appeal["user_id"]
            )
        except UserNotParticipantError:
            return {"rights": rights_dict(None), "actor": None, "date": None}
        return ban_context(permissions)

    async def execute(self, store, appeal):
        try:
            decision = appeal["payload"]["decision"]
            actor = decision["actor_id"]
            if actor == appeal["user_id"] or not await self.service.authorized(
                actor, appeal["chat_id"]
            ):
                raise GuardError("error.permission_denied")
            if decision["action"] == "global" and actor not in self.service.default_admins():
                raise GuardError("appeal.global_admin_required")
            case = await self.check_source(store, appeal)
            black = await store.guard.blacklist(appeal["user_id"])
            if black and (
                decision["action"] != "global"
                or black["case_id"] != decision.get("blacklist_case_id")
            ):
                config = self.service.group(appeal["chat_id"])
                if config.enabled and config.share_global_blacklist:
                    await self.service.actions.ban(appeal["chat_id"], appeal["user_id"])
                raise GuardError("guard.blacklist_conflict")
            current = await self.current_ban(appeal)
            if current["rights"].get("view_messages"):
                baseline = decision["ban"]
                if (
                    not same_rights(current["rights"], baseline["rights"])
                    or current["actor"] != baseline["actor"]
                    or current["date"] != baseline["date"]
                ):
                    raise GuardError("guard.permissions_changed")
                own = await self.service.actions.permissions(appeal["chat_id"], self.service.bot_id)
                if not own.is_creator and not getattr(
                    getattr(own.participant, "admin_rights", None), "ban_users", False
                ):
                    raise GuardError("guard.permissions_required")
                # 黑名单记录也代表已执行的封禁；复用 Guard 的跨记录冲突检查和解封操作。
                await self.service.reviews.release(store.guard, {**case, "action": "ban"})
            await store.finish_approval(appeal, case)
        except GuardError as error:
            # 撤权、来源变化和名单冲突必须重新由管理员决定，不能自动跨越。
            await store.transition(
                appeal,
                "superseded" if str(error) == "appeal.source_changed" else "pending",
                "system",
                "execution_blocked",
                error=str(error),
                detail={"error": str(error)},
            )
        except Exception as error:
            # RPC 或数据库失败保留已授权意图。重启后核对当前权限，只补齐剩余阶段。
            await store.execution_error(appeal, type(error).__name__)
            logger.warning("申诉解封暂未完成 appeal=%s", appeal["id"], exc_info=True)
