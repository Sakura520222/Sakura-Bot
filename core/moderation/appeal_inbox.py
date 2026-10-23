# Copyright 2026 Sakura-Bot
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
"""助手 Bot 的持久化输入边界。身份只能由 Telegram update 提供。"""

import hashlib
import uuid

from core.i18n.i18n import t
from core.infrastructure.database.mysql import utc_now_naive

from .appeal_repository import READ_ACTIONS, REVIEW_ACTIONS
from .appeals import eligible, source_signature
from .moderator import GuardError


class AppealInbox:
    def __init__(self, repository):
        self.repository = repository

    @staticmethod
    def own(appeal, user_id):
        if not appeal or appeal["user_id"] != user_id:
            raise GuardError("guard.invalid_callback")

    async def begin(self, user_id, case_id):
        async with self.repository.lock(user_id) as store:
            case = await store.guard.case(case_id)
            if not case or case["user_id"] != user_id or not eligible(case):
                raise GuardError("appeal.no_case")
            existing = await store.active(case["chat_id"], user_id)
            if existing:
                return existing
            if await store.input(user_id):
                raise GuardError("appeal.input_busy")
            if await store.rejected_recently(case) or await store.submission_count(user_id) >= 3:
                raise GuardError("appeal.cooldown")
            black = await store.guard.blacklist(user_id)
            return await store.create(
                case,
                {
                    "source": {key: case[key] for key in ("id", "action", "status", "payload")},
                    "source_created_at": str(case["created_at"]),
                    "source_signature": source_signature(case),
                    "blacklisted": bool(black),
                    "evidence": [],
                },
            )

    async def supplement(self, user_id, appeal_id, revision):
        async with self.repository.lock(user_id) as store:
            appeal = await store.get(appeal_id)
            self.own(appeal, user_id)
            if appeal["status"] != "need_info" or appeal["revision"] != revision:
                raise GuardError("appeal.stale")
            if await store.input(user_id):
                raise GuardError("appeal.input_busy")
            if len(appeal["payload"].get("evidence", [])) >= 6:
                raise GuardError("appeal.evidence_limit")
            return await store.save_input(
                user_id,
                appeal_id,
                "supplement",
                {
                    "text": "",
                    "files": [],
                    "revision": revision,
                },
            )

    async def append(self, actor_id, text, media, message_id):
        async with self.repository.lock(actor_id) as store:
            entry = await store.input(actor_id)
            if not entry:
                raise GuardError("appeal.stale")
            appeal = await store.get(entry["appeal_id"])
            self.validate_input(entry, appeal)
            payload = dict(entry["payload"])
            if message_id in payload.get("messages", []):
                return entry
            if entry["mode"] == "review":
                if media or not text or not 1 <= len(text) <= 1000:
                    raise GuardError("appeal.reason_required")
                payload["text"] = text
            else:
                combined = "\n".join(value for value in (payload.get("text", ""), text) if value)
                if len(combined) > 2000:
                    raise GuardError("appeal.text_limit")
                if not text and not media:
                    raise GuardError("appeal.media_types")
                payload["text"] = combined
                if media:
                    if len(payload["files"]) >= 5 or media.get("size", 0) > 20 * 1024 * 1024:
                        raise GuardError("appeal.file_limit")
                    payload["files"] = [*payload["files"], media]
            payload["messages"] = [*payload.get("messages", []), message_id][-30:]
            return await store.save_input(
                actor_id, entry["appeal_id"], entry["mode"], payload, token=entry["token"]
            )

    @staticmethod
    def validate_input(entry, appeal):
        if not appeal:
            raise GuardError("appeal.stale")
        if entry["mode"] == "draft" and appeal["status"] != "draft":
            raise GuardError("appeal.stale")
        if entry["mode"] in {"supplement", "review"} and (
            appeal["status"] != ("need_info" if entry["mode"] == "supplement" else "pending")
            or appeal["revision"] != entry["payload"]["revision"]
        ):
            raise GuardError("appeal.stale")

    async def submit(self, actor_id, token):
        async with self.repository.lock(actor_id) as store:
            entry = await store.input(actor_id)
            if not entry or entry["token"] != token:
                raise GuardError("appeal.stale")
            appeal = await store.get(entry["appeal_id"])
            self.validate_input(entry, appeal)
            payload = entry["payload"]
            if entry["mode"] == "review":
                reason = payload.get("text") or (
                    t("appeal.default_approve")
                    if payload["action"] in {"approve", "allow", "global"}
                    else ""
                )
                if not reason:
                    raise GuardError("appeal.reason_required")
                async with store.atomic():
                    await store.queue_request(
                        appeal["id"],
                        actor_id,
                        payload["action"],
                        payload["revision"],
                        payload["origin"],
                        reason,
                        token.ljust(64, "0"),
                    )
                    await store.clear_input(actor_id, token)
                return None
            self.own(appeal, actor_id)
            if not 1 <= len(payload.get("text", "").strip()) <= 2000:
                raise GuardError("appeal.text_required")
            if entry["mode"] == "draft" and await store.submission_count(actor_id) >= 3:
                raise GuardError("appeal.cooldown")
            evidence = {
                "text": payload["text"].strip(),
                "files": payload["files"],
                "messages": payload.get("messages", []),
                "at": utc_now_naive().isoformat(),
                "user_id": actor_id,
            }
            updated = {
                **appeal["payload"],
                "evidence": [*appeal["payload"].get("evidence", []), evidence],
            }
            async with store.atomic():
                result = await store._transition(
                    appeal,
                    "submitted" if entry["mode"] == "draft" else "pending",
                    f"telegram:{actor_id}",
                    "submitted" if entry["mode"] == "draft" else "supplemented",
                    payload=updated,
                    detail=evidence,
                )
                await store.clear_input(actor_id, token)
            return result

    async def withdraw(self, user_id, appeal_id, revision):
        async with self.repository.lock(user_id) as store:
            appeal = await store.get(appeal_id)
            self.own(appeal, user_id)
            if (
                appeal["status"] not in {"draft", "submitted", "pending", "need_info"}
                or appeal["revision"] != revision
            ):
                raise GuardError("appeal.stale")
            async with store.atomic():
                result = await store._transition(
                    appeal, "withdrawn", f"telegram:{user_id}", "withdrawn"
                )
                entry = await store.input(user_id)
                if entry and entry["appeal_id"] == appeal_id:
                    await store.clear_input(user_id, entry["token"])
                return result

    async def cancel(self, actor_id, token=None):
        async with self.repository.lock(actor_id) as store:
            entry = await store.input(actor_id)
            if not entry or (token is not None and token != entry["token"]):
                raise GuardError("appeal.stale")
            appeal = await store.get(entry["appeal_id"])
            async with store.atomic():
                if entry["mode"] == "draft" and appeal["status"] == "draft":
                    await store._transition(
                        appeal, "withdrawn", f"telegram:{actor_id}", "withdrawn"
                    )
                await store.clear_input(actor_id, entry["token"])
            return entry

    async def review_input(self, actor_id, appeal_id, revision, origin, action):
        if action not in REVIEW_ACTIONS:
            raise GuardError("guard.invalid_callback")
        async with self.repository.lock(actor_id) as store:
            appeal = await store.get(appeal_id)
            await self.check_origin(store, appeal, actor_id, revision, origin)
            if appeal["status"] != "pending":
                raise GuardError("appeal.stale")
            existing = await store.input(actor_id)
            if existing:
                if existing["mode"] != "review" or existing["appeal_id"] != appeal_id:
                    raise GuardError("appeal.input_busy")
                if (
                    existing["payload"].get("action"),
                    existing["payload"].get("revision"),
                    existing["payload"].get("origin"),
                ) == (action, revision, list(origin)):
                    return existing
            return await store.save_input(
                actor_id,
                appeal_id,
                "review",
                {
                    "action": action,
                    "revision": revision,
                    "origin": list(origin),
                    "text": "",
                },
            )

    async def quick_review(self, actor_id, appeal_id, revision, origin, action):
        if action not in {"approve", "allow", "global"}:
            raise GuardError("guard.invalid_callback")
        async with self.repository.lock(actor_id) as store:
            appeal = await store.get(appeal_id)
            await self.check_origin(store, appeal, actor_id, revision, origin)
            if appeal["status"] != "pending":
                raise GuardError("appeal.stale")
            if action == "global":
                from core.infrastructure.utils.auth import is_admin

                if not is_admin(actor_id):
                    raise GuardError("appeal.global_admin_required")
            elif await store.guard.blacklist(appeal["user_id"]):
                # 拦截结果直接返回点击提示，不把注定失败的操作显示为“正在处理”。
                await store.event(
                    appeal,
                    f"telegram:{actor_id}",
                    "request_denied",
                    appeal["status"],
                    {"action": action, "error": "guard.blacklist_conflict"},
                )
                raise GuardError("guard.blacklist_conflict")
            entry = await store.input(actor_id)
            if entry and (entry["mode"] != "review" or entry["appeal_id"] != appeal_id):
                raise GuardError("appeal.input_busy")
            latest = await store.guard.one(
                """SELECT id,status FROM moderation_appeal_requests
                WHERE appeal_id=%s AND actor_id=%s AND revision=%s AND action=%s
                ORDER BY created_at DESC,id DESC LIMIT 1""",
                (appeal_id, actor_id, revision, action),
            )
            if latest and latest["status"] != "failed":
                return
            request_id = hashlib.sha256(
                f"review:{appeal_id}:{actor_id}:{revision}:{origin}:{action}:{latest['id'] if latest else ''}".encode()
            ).hexdigest()
            async with store.atomic():
                await store.queue_request(
                    appeal_id,
                    actor_id,
                    action,
                    revision,
                    origin,
                    t("appeal.default_approve"),
                    request_id,
                )
                if entry:
                    await store.clear_input(actor_id, entry["token"])

    @staticmethod
    async def check_origin(store, appeal, actor_id, revision, origin):
        if not appeal or appeal["user_id"] == actor_id or appeal["revision"] != revision:
            raise GuardError("appeal.stale")
        if origin[0] > 0 and origin[0] != actor_id:
            raise GuardError("guard.invalid_callback")
        if not any(
            (row["target_id"], row["message_id"]) == origin
            and row["kind"] == "reviewer"
            and row["synced_revision"] == revision
            for row in await store.notices(appeal["id"])
        ):
            raise GuardError("guard.invalid_callback")

    async def read_request(self, actor_id, appeal_id, revision, origin, action):
        if action not in READ_ACTIONS:
            raise GuardError("guard.invalid_callback")
        async with self.repository.lock(actor_id) as store:
            appeal = await store.get(appeal_id)
            await self.check_origin(store, appeal, actor_id, revision, origin)
            await store.queue_request(
                appeal_id, actor_id, action, revision, origin, "", uuid.uuid4().hex.ljust(64, "0")
            )
