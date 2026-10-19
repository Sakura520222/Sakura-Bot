# Copyright 2026 Sakura-Bot
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
"""案件审核；首次持久化选择获胜，同一选择可安全恢复。"""

import re
from datetime import datetime, timedelta

from telethon import utils
from telethon.errors import UserNotParticipantError
from telethon.tl.types import MessageEmpty, PeerChat

from core.i18n.i18n import t
from core.infrastructure.database.mysql import utc_now_naive

from .moderator import GuardError
from .notification import RETIRED_NOTICES, case_status, is_ban_case
from .repository import ACTIVE_VERIFICATIONS
from .telegram import (
    TelegramActionError,
    call,
    mutate,
    mute_rights,
    same_rights,
    verification_rights,
)


class ReviewHandler:
    def __init__(self, service):
        self.service = service

    async def callback(self, event):
        match = re.fullmatch(rb"sg:r:([0-9a-f]{32}):([abukdj])(?::([0-9]{1,8}))?", event.data)
        if not match:
            await event.answer(t("guard.invalid_callback"), alert=True)
            return
        case_id = match[1].decode()
        action = {
            b"a": "allow",
            b"b": "blacklist",
            b"u": "unban",
            b"k": "ban",
            b"d": "delete",
            b"j": "kick",
        }[match[2]]
        result = await self.review(
            case_id,
            action,
            event.sender_id,
            origin=(event.chat_id, event.message_id),
            expected_generation=int(match[3] or b"0"),
        )
        await event.answer(
            t("guard.report_delete_done")
            if action == "delete"
            else t("guard.review_result", status=case_status(result)),
            alert=True,
        )

    async def review(
        self,
        case_id,
        action,
        reviewer_id,
        *,
        origin=None,
        web=False,
        expected_chat_id=None,
        expected_generation=None,
    ):
        if action not in {"allow", "blacklist", "unban", "ban", "delete", "kick"}:
            raise GuardError("guard.invalid_callback")
        # 初次读取只定位锁；锁内再次读取目标与状态，完全不采信回调里的群/用户。
        initial = await self.service.repository.get_case(case_id)
        if not initial or (expected_chat_id is not None and initial["chat_id"] != expected_chat_id):
            raise GuardError("guard.invalid_callback")
        if not await self.service.authorized(reviewer_id, initial["chat_id"], web=web):
            raise GuardError("error.permission_denied")
        async with self.service.repository.lock(initial["user_id"]) as store:
            case = await store.case(case_id)
            if not case or case["action"] not in {"ban", "mute", "warn", "report"}:
                raise GuardError("guard.invalid_callback")
            if not await self.service.authorized(reviewer_id, case["chat_id"], web=web):
                raise GuardError("error.permission_denied")
            if (
                action != "delete"
                and expected_generation is not None
                and expected_generation != case["payload"].get("review_generation", 0)
            ):
                raise GuardError("guard.invalid_callback")
            if (
                case["payload"].get("verification_failure")
                and case["payload"].get("verification_result") == "held"
            ):
                raise GuardError("guard.verification_release_required")
            if origin is not None:
                notices = await store.notices(case_id)
                if not any(
                    (row["target_id"], row["message_id"]) == origin
                    and row["synced_status"] not in RETIRED_NOTICES
                    for row in notices
                ):
                    raise GuardError("guard.invalid_callback")
            if action == "delete":
                await self.request_message_deletion(store, case, reviewer_id, web=web)
                result = await store.case(case_id)
                await self.service.notifier.sync(store, result, self.service.group(case["chat_id"]))
                if result["payload"]["report_message_deletion"]["status"] != "deleted":
                    raise GuardError("guard.report_delete_pending")
                return result
            if case["status"] == "kicked":
                return case
            if (
                action == "kick"
                and case["status"] != "reviewing_kick"
                and not self.service.group(case["chat_id"]).kick_button_enabled
            ):
                raise GuardError("guard.kick_disabled")
            if case["status"] in {
                "allowed",
                "blacklisted",
                "unbanned",
                "unmuted",
                "ignored",
            } and not (action == "kick" and case["action"] == "report"):
                return case
            if action == "ban" and is_ban_case(case):
                return case
            report_pending = case["action"] == "report" and not is_ban_case(case)
            if report_pending and action == "allow":
                raise GuardError("guard.invalid_callback")
            if (
                (case["status"] == "pending" and (is_ban_case(case) or report_pending))
                or (case["status"] == "done" and case["action"] in {"mute", "warn"})
                or (
                    action == "kick"
                    and case["action"] == "report"
                    and case["status"]
                    in {"allowed", "blacklisted", "unbanned", "unmuted", "ignored"}
                )
            ):
                if action == "kick":
                    await self.check_kick_conflicts(store, case)
                if (
                    action in {"allow", "unban"}
                    and not report_pending
                    and await store.blacklist(case["user_id"])
                ):
                    raise GuardError("guard.blacklist_conflict")
                web_admin = web and reviewer_id == "admin"
                if not await store.claim_review(
                    case_id,
                    action,
                    None if web_admin else reviewer_id,
                    reviewer_actor="webui:admin" if web_admin else f"telegram:{reviewer_id}",
                ):
                    raise GuardError("guard.operation_failed")
                case = await store.case(case_id)
            elif case["status"] != f"reviewing_{action}":
                raise GuardError("guard.already_processing")
            await self.resume(store, case)
            result = await store.case(case_id)
            await self.service.notifier.sync(store, result, self.service.group(case["chat_id"]))
            if result["status"].startswith("reviewing_"):
                raise GuardError("guard.operation_failed")
            return result

    async def save_message_deletion(self, store, case, deletion):
        payload = {
            **case["payload"],
            "report_message_deletion": deletion,
            "report_notice_dirty": True,
        }
        await store.update_case(
            case["id"], case["status"], error=case.get("error"), payload=payload
        )
        case["payload"] = payload

    async def request_message_deletion(self, store, case, reviewer_id, *, web=False):
        message_id = case["payload"].get("message_id")
        if case["action"] != "report" or type(message_id) is not int or not 0 < message_id < 2**31:
            raise GuardError("guard.invalid_callback")
        deletion = case["payload"].get("report_message_deletion")
        if deletion and deletion["status"] == "deleted":
            return
        if not deletion:
            # 删除意图和首次操作人先落盘，独立于封禁/忽略等审核决定。
            await self.save_message_deletion(
                store,
                case,
                {
                    "status": "pending",
                    "chat_id": case["chat_id"],
                    "user_id": case["user_id"],
                    "message_id": message_id,
                    "actor": "webui:admin"
                    if web and reviewer_id == "admin"
                    else f"telegram:{reviewer_id}",
                    "requested_at": utc_now_naive().isoformat(),
                    "attempts": 0,
                },
            )
        await self.resume_message_deletion(store, case, force=True)

    async def resume_message_deletion(self, store, case, *, force=False):
        deletion = case["payload"].get("report_message_deletion")
        if case["action"] != "report" or not deletion or deletion["status"] != "pending":
            return
        if (
            not force
            and deletion.get("retry_at")
            and datetime.fromisoformat(deletion["retry_at"]) > utc_now_naive()
        ):
            return
        try:
            message_id = case["payload"].get("message_id")
            if type(message_id) is not int or not 0 < message_id < 2**31:
                raise GuardError("guard.invalid_callback")
            if (deletion.get("chat_id"), deletion.get("user_id"), deletion.get("message_id")) != (
                case["chat_id"],
                case["user_id"],
                message_id,
            ):
                raise GuardError("guard.report_delete_target_changed")
            permissions = await self.service.actions.permissions(
                case["chat_id"], self.service.bot_id
            )
            if not (permissions.is_admin or permissions.is_creator):
                raise GuardError("guard.report_delete_permission_error")
            original = await call(self.service.client.get_messages(case["chat_id"], ids=message_id))
            if original is not None and not isinstance(original, MessageEmpty):
                if (
                    getattr(original, "chat_id", None),
                    getattr(original, "sender_id", None),
                    getattr(original, "id", None),
                ) != (case["chat_id"], case["user_id"], message_id):
                    raise GuardError("guard.report_delete_target_changed")
                await mutate(self.service.client.delete_messages(case["chat_id"], [message_id]))
                remaining = await call(
                    self.service.client.get_messages(case["chat_id"], ids=message_id)
                )
                if remaining is not None and not isinstance(remaining, MessageEmpty):
                    raise GuardError("guard.report_delete_unconfirmed")
            await self.save_message_deletion(
                store,
                case,
                {
                    **deletion,
                    "status": "deleted",
                    "completed_at": utc_now_naive().isoformat(),
                    "error": None,
                    "retry_at": None,
                },
            )
        except Exception as error:
            attempts = deletion.get("attempts", 0) + 1
            reason = (
                str(error)
                if isinstance(error, (GuardError, TelegramActionError))
                else type(error).__name__
            )
            await self.save_message_deletion(
                store,
                case,
                {
                    **deletion,
                    "status": "pending",
                    "attempts": attempts,
                    "error": reason,
                    "retry_at": (
                        utc_now_naive()
                        + timedelta(seconds=min(900, 30 * 2 ** min(attempts - 1, 5)))
                    ).isoformat(),
                },
            )

    async def resume(self, store, case):
        action = case["status"].removeprefix("reviewing_")
        try:
            if action in {"allow", "unban"}:
                report_only = case["action"] == "report" and not is_ban_case(case)
                if not report_only and await store.blacklist(case["user_id"]):
                    config = self.service.group(case["chat_id"])
                    if config.enabled and config.share_global_blacklist:
                        # 解封后数据库提交失败期间，另一群可能已永久拉黑。
                        # 先恢复共享黑名单禁令，避免恢复中的解封意图绕过新禁令。
                        await self.service.actions.ban(case["chat_id"], case["user_id"])
                    raise GuardError("guard.blacklist_conflict")
                await self.release(store, case)
            elif action == "kick":
                await self.kick_member(store, case)
            elif action in {"blacklist", "ban"}:
                user = await call(self.service.client.get_entity(case["user_id"]))
                if await self.service.protected(case["chat_id"], user, applicant=True):
                    raise GuardError("guard.verification_target_protected")
                # 复核现有禁令，未跨群执行任何批量封禁。
                await self.service.actions.ban(case["chat_id"], case["user_id"])
                session = await store.verification(case["chat_id"], case["user_id"])
                if session and session["status"] in set(ACTIVE_VERIFICATIONS) | {
                    "passed",
                    "admin_skipped",
                }:
                    session["status"] = "blocked"
                    await store.save_verification(session)
                    await self.service.verification.finish_message(store, session)
            else:
                raise GuardError("guard.invalid_callback")
            await store.finish_review(case, action)
        except Exception as error:
            await store.update_case(
                case["id"],
                case["status"],
                error=str(error) if isinstance(error, GuardError) else type(error).__name__,
            )

    async def check_kick_conflicts(self, store, case):
        if await store.blacklist(case["user_id"]):
            raise GuardError("guard.blacklist_conflict")
        other = await store.active_ban(case["chat_id"], case["user_id"], exclude_case_id=case["id"])
        if other and other["id"] != case["id"]:
            raise GuardError("guard.other_ban_conflict")

    async def save_kick_progress(self, store, case, **changes):
        payload = {**case["payload"], **changes, "report_notice_dirty": True}
        await store.update_case(
            case["id"], case["status"], error=case.get("error"), payload=payload
        )
        case["payload"] = payload

    async def kick_member(self, store, case, *, manage_verification=True):
        """临时封禁确认落盘后，只恢复解封阶段，避免重复踢出重新入群的用户。"""
        if not case["payload"].get("kick_unbanned"):
            await self.check_kick_conflicts(store, case)
        chat_id, user_id = case["chat_id"], case["user_id"]
        basic_group = utils.resolve_id(chat_id)[1] is PeerChat
        if not basic_group and not case["payload"].get("kick_unbanned"):
            own = await self.service.actions.permissions(chat_id, self.service.bot_id)
            rights = getattr(own.participant, "admin_rights", None)
            if not own.is_creator and not getattr(rights, "ban_users", False):
                raise GuardError("guard.permissions_required")
        freshly_banned = False
        if not case["payload"].get("kick_banned"):
            user = await call(self.service.client.get_entity(user_id))
            if await self.service.protected(chat_id, user, applicant=True):
                raise GuardError("guard.verification_target_protected")
            current = {} if basic_group else await self.service.actions.rights(chat_id, user_id)
            if current.get("view_messages"):
                permissions = await self.service.actions.permissions(chat_id, user_id)
                if not (
                    is_ban_case(case)
                    or (case["payload"].get("review_history") or [{}])[-1].get("action")
                    == "blacklist"
                    or case["payload"].get("kick_ban_intended")
                ):
                    raise GuardError("guard.other_ban_conflict")
                if getattr(permissions.participant, "kicked_by", None) != self.service.bot_id:
                    raise GuardError("guard.permissions_changed")
            if manage_verification:
                session = await store.verification(chat_id, user_id)
                if session and session["status"] == "admin_pending":
                    raise GuardError("guard.already_processing")
                if not case["payload"].get("kick_session_checked"):
                    await self.save_kick_progress(
                        store,
                        case,
                        kick_session_checked=True,
                        kick_session_id=session["id"] if session else None,
                    )
                elif (session["id"] if session else None) != case["payload"].get("kick_session_id"):
                    raise GuardError("guard.permissions_changed")
                if session and session["status"] in ACTIVE_VERIFICATIONS:
                    session["status"] = "admin_kick_pending"
                    session["payload"]["cleanup_pending"] = True
                    await store.save_verification(session)
                    await self.service.verification.finish_message(store, session)
            if basic_group:
                await self.service.actions.kick_basic_group(chat_id, user_id)
            else:
                if not current.get("view_messages"):
                    await self.save_kick_progress(store, case, kick_ban_intended=True)
                    await self.service.actions.ban(chat_id, user_id)
                    freshly_banned = True
            await self.save_kick_progress(store, case, kick_banned=True)
        if not basic_group and not case["payload"].get("kick_unbanned"):
            try:
                current = await self.service.actions.rights(chat_id, user_id)
            except UserNotParticipantError:
                current = {}
            if freshly_banned or current.get("view_messages"):
                if not freshly_banned:
                    permissions = await self.service.actions.permissions(chat_id, user_id)
                    if getattr(permissions.participant, "kicked_by", None) != self.service.bot_id:
                        raise GuardError("guard.permissions_changed")
                await self.service.actions.unban(chat_id, user_id)
        if not case["payload"].get("kick_unbanned"):
            await self.save_kick_progress(store, case, kick_unbanned=True)
        if manage_verification and case["payload"].get("kick_session_id"):
            session = await store.verification_by_id(case["payload"]["kick_session_id"])
            if session:
                session["status"] = "admin_kicked"
                session["error"] = None
                session["payload"]["cleanup_pending"] = True
                await store.save_verification(session)
                await self.service.verification.finish_message(store, session)

    async def release(self, store, case):
        chat_id, user_id = case["chat_id"], case["user_id"]
        if case["action"] == "report" and not is_ban_case(case):
            return
        other = await store.active_ban(chat_id, user_id)
        if other and other["id"] != case["id"]:
            raise GuardError("guard.other_ban_conflict")
        if not is_ban_case(case) and case["action"] == "warn":
            return
        if not is_ban_case(case) and await store.later_mute(case):
            raise GuardError("guard.permissions_changed")
        current = await self.service.actions.rights(chat_id, user_id)
        if is_ban_case(case):
            # 已解封后重新入群、另行禁言时，不用旧通知清除当前限制。
            if current.get("view_messages"):
                await self.service.actions.unban(chat_id, user_id)
            return
        payload = case["payload"]
        original = payload.get("original_rights")
        if original is None or "mute_until" not in payload:
            raise GuardError("guard.permissions_changed")
        expected = mute_rights(original, payload["mute_until"])
        session = await store.verification(chat_id, user_id)
        if session and session["status"] in ACTIVE_VERIFICATIONS:
            baseline = session["payload"]["original_rights"]
            if not same_rights(baseline, expected) and not same_rights(baseline, original):
                raise GuardError("guard.permissions_changed")
            if not same_rights(current, verification_rights(baseline)) and not same_rights(
                current, expected
            ):
                raise GuardError("guard.permissions_changed")
            session["payload"]["original_rights"] = original
            restricted = verification_rights(original)
            session["payload"].update(
                requested_rights=restricted, restricted_rights=restricted.copy()
            )
            await store.save_verification(session)
            if not same_rights(current, restricted):
                await self.service.actions.set_rights(chat_id, user_id, restricted)
            payload["release_deferred_verification"] = True
            return
        if same_rights(current, original):
            return
        if not same_rights(current, expected):
            raise GuardError("guard.permissions_changed")
        await self.service.actions.set_rights(chat_id, user_id, original)
