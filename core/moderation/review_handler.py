# Copyright 2026 Sakura-Bot
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
"""案件审核；首次持久化选择获胜，同一选择可安全恢复。"""

import re

from core.i18n.i18n import t

from .moderator import GuardError
from .notification import RETIRED_NOTICES, case_status, is_ban_case
from .repository import ACTIVE_VERIFICATIONS
from .telegram import call, mute_rights, same_rights, verification_rights


class ReviewHandler:
    def __init__(self, service):
        self.service = service

    async def callback(self, event):
        match = re.fullmatch(rb"sg:r:([0-9a-f]{32}):([abuk])(?::([0-9]{1,8}))?", event.data)
        if not match:
            await event.answer(t("guard.invalid_callback"), alert=True)
            return
        case_id = match[1].decode()
        action = {b"a": "allow", b"b": "blacklist", b"u": "unban", b"k": "ban"}[match[2]]
        result = await self.review(
            case_id,
            action,
            event.sender_id,
            origin=(event.chat_id, event.message_id),
            expected_generation=int(match[3] or b"0"),
        )
        await event.answer(t("guard.review_result", status=case_status(result)), alert=True)

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
        if action not in {"allow", "blacklist", "unban", "ban"}:
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
            if expected_generation is not None and expected_generation != case["payload"].get(
                "review_generation", 0
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
            if case["status"] in {"allowed", "blacklisted", "unbanned", "unmuted", "ignored"}:
                return case
            if action == "ban" and is_ban_case(case):
                return case
            report_pending = case["action"] == "report" and not is_ban_case(case)
            if report_pending and action == "allow":
                raise GuardError("guard.invalid_callback")
            if (case["status"] == "pending" and (is_ban_case(case) or report_pending)) or (
                case["status"] == "done" and case["action"] in {"mute", "warn"}
            ):
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
