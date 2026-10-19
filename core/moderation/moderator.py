# Copyright 2026 Sakura-Bot
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
"""主 Bot 群管服务：显式启用、规则优先、持久化意图及安全恢复。"""

import asyncio
import hashlib
import logging
import uuid
from datetime import UTC, datetime, timedelta
from types import SimpleNamespace

from telethon import events, utils
from telethon.errors import UserNotParticipantError
from telethon.tl.types import (
    PeerChannel,
    UpdateChannel,
    UpdateChannelParticipant,
    UpdateChatParticipantAdmin,
)

from core.i18n.i18n import t
from core.infrastructure.database.mysql import utc_now_naive

from .chat_cleanup import ChatCleanup
from .config import GuardConfig, GuardGroup
from .groups import GroupDirectory
from .history_cleanup import HistoryCleanup
from .matcher import Match, Matcher, normalize
from .notification import Notifier, stable_random_id
from .repository import ACTIVE_VERIFICATIONS
from .telegram import TelegramActionError, TelegramActions, call, mutate, mute_rights

logger = logging.getLogger(__name__)


class GuardError(ValueError):
    """可安全显示的业务错误，消息使用 i18n key。"""


class Moderator:
    def __init__(self, client, repository, config=None, default_admins=None):
        self.client = client
        self.repository = repository
        self.config = config or GuardConfig()
        self.actions = TelegramActions(client)
        self.default_admins = default_admins or self._default_admins
        self.notifier = Notifier(self.actions, self.default_admins)
        self.bot_id = None
        self._handlers = []
        self._worker = None
        self._wake = asyncio.Event()
        self.directory = GroupDirectory(
            client, lambda: self.config.groups, lambda: self.bot_id, repository
        )
        self.chat_cleanup = ChatCleanup(
            self.directory, lambda actor: actor == "admin" or actor in self.default_admins()
        )
        self.history_cleanup = HistoryCleanup(
            self.directory, lambda actor: actor == "admin" or actor in self.default_admins()
        )
        from .report_handler import ReportHandler
        from .review_handler import ReviewHandler
        from .verification import Verification

        self.reviews = ReviewHandler(self)
        self.reports = ReportHandler(self)
        self.verification = Verification(self)

    @staticmethod
    def _default_admins():
        import core.config as config_module

        return [value for value in config_module.ADMIN_LIST if isinstance(value, int) and value > 0]

    def group(self, chat_id):
        return self.config.groups.get(str(chat_id), GuardGroup())

    def reload(self, config):
        # 先校验完整快照，再整体替换，避免半更新的规则集。
        self.config = GuardConfig.model_validate(config.get("sakura_guard", {}))
        self._wake.set()

    async def start(self):
        me = await call(self.client.get_me())
        self.bot_id = me.id
        from telethon.tl.types import UpdateBotChatInviteRequester

        for callback, builder in (
            (self.reports.handle, events.NewMessage(incoming=True)),
            (self.handle_message, events.NewMessage(incoming=True)),
            (self.handle_message, events.MessageEdited(incoming=True)),
            (self.handle_join, events.ChatAction()),
            (self.handle_join_request, events.Raw(types=UpdateBotChatInviteRequester)),
            (
                self.handle_membership_update,
                events.Raw(
                    types=(UpdateChannel, UpdateChannelParticipant, UpdateChatParticipantAdmin)
                ),
            ),
            (self.handle_callback, events.CallbackQuery(pattern=rb"^sg:")),
        ):
            self.client.add_event_handler(callback, builder)
            self._handlers.append((callback, builder))
        self._worker = asyncio.create_task(self._run(), name="sakura-guard-recovery")
        self.directory.refresh(force=True)

    async def stop(self):
        for callback, builder in self._handlers:
            self.client.remove_event_handler(callback, builder)
        self._handlers.clear()
        await self.chat_cleanup.stop()
        await self.history_cleanup.stop()
        await self.directory.stop()
        if self._worker:
            self._worker.cancel()
            try:
                await self._worker
            except asyncio.CancelledError:
                pass
            self._worker = None

    async def validate_group(self, chat_id, config):
        if not config.enabled:
            return
        chat = await call(self.client.get_entity(chat_id))
        if not getattr(chat, "megagroup", False) or not self.directory.is_group(chat):
            raise GuardError("guard.supergroup_required")
        permissions = await self.actions.permissions(chat_id, self.bot_id)
        rights = getattr(permissions.participant, "admin_rights", None)
        if not rights or not rights.ban_users or not rights.delete_messages:
            raise GuardError("guard.permissions_required")
        if config.join_requests == "approve" and not rights.invite_users:
            raise GuardError("guard.invite_permission_required")

    async def authorized(self, user_id, chat_id, *, web=False):
        if web and user_id == "admin":
            # 现有管理 Token 登录没有 Telegram 身份，审核时单独记录 webui:admin。
            return True
        if not isinstance(user_id, int) or user_id <= 0:
            return False
        config = self.group(chat_id)
        # WebUI 已认证管理员仍按实时 REPORT_ADMIN_IDS 复核，撤权立即生效。
        if web:
            return user_id in self.default_admins()
        if user_id in (config.reviewer_ids or self.default_admins()):
            return True
        if config.allow_group_admin_review:
            try:
                permissions = await self.actions.permissions(chat_id, user_id)
                return bool(permissions.is_admin or permissions.is_creator)
            except Exception:
                return False
        return False

    async def protected(self, chat_id, user, *, applicant=False, allow_nonmember=False):
        if user.id == self.bot_id:
            return True
        if getattr(user, "bot", False) and user.id in self.group(chat_id).trusted_bot_ids:
            return True
        try:
            permissions = await self.actions.permissions(chat_id, user.id)
            return bool(permissions.is_admin or permissions.is_creator)
        except UserNotParticipantError:
            # 访客 Bot 可以发言但不是群成员；明确的非成员身份不代表管理员豁免。
            return not (applicant or allow_nonmember)
        except Exception:
            # 无法确定成员权限时禁止自动处罚。
            logger.warning(
                "Guard 无法确认成员权限 chat=%s user=%s", chat_id, user.id, exc_info=True
            )
            return True

    async def match(self, store, config, chat_id, user, message=None):
        if config.share_global_blacklist and await store.blacklist(user.id):
            await store.touch_blacklist(user.id)
            return Match(
                "global_blacklist",
                "global-user-id",
                "permanent_blacklist",
                str(user.id),
                str(user.id),
            )
        if await store.whitelisted(chat_id, user.id):
            return None
        matcher = Matcher(config)
        nickname = matcher.nickname(user)
        if nickname:
            return nickname
        if message is not None and config.block_bot_messages and getattr(user, "bot", False):
            original = (getattr(message, "message", "") or "")[:2048]
            return Match(
                "bot_message",
                "bot-message-block",
                t("guard.bot_message_reason"),
                original,
                normalize(original)[:2048],
                config.bot_message_action,
            )
        return matcher.message(message) if message is not None else None

    async def snapshot(self, chat_id, user, evidence, message=None):
        chat = await call(self.client.get_entity(chat_id))
        username = getattr(chat, "username", None)
        link = (
            f"https://t.me/{username}"
            if username
            else f"https://t.me/c/{str(chat_id)[4:]}"
            if str(chat_id).startswith("-100")
            else ""
        )
        message_id = getattr(message, "id", None)
        return {
            "chat_title": getattr(chat, "title", str(chat_id))[:256],
            "chat_link": link,
            "name": " ".join(
                value
                for value in (getattr(user, "first_name", None), getattr(user, "last_name", None))
                if value
            )[:256],
            "username": getattr(user, "username", None),
            "evidence": evidence.evidence(),
            "message_id": message_id,
            "evidence_link": f"{link}/{message_id}" if link and message_id else "",
        }

    async def handle_message(self, event):
        chat_id = event.chat_id
        if event.is_group or getattr(event, "is_channel", False):
            self.directory.observe(chat_id, getattr(event, "chat", None))
        if (
            not event.is_group
            or not self.group(chat_id).enabled
            or not event.sender_id
            or event.sender_id <= 0
        ):
            return
        try:
            # get_entity 重新获取用户快照，不依赖可能过时的 event.sender 缓存。
            user = await call(self.client.get_entity(event.sender_id))
            if await self.protected(
                chat_id,
                user,
                allow_nonmember=self.group(chat_id).block_bot_messages
                and bool(getattr(user, "bot", False)),
            ):
                return
            async with self.repository.lock(user.id) as store:
                config = self.group(chat_id)
                if not config.enabled:
                    return
                match = await self.match(store, config, chat_id, user, event.message)
                if match:
                    token = f"message:{event.id}:{getattr(event.message, 'edit_date', None)}"
                    await self.punish(store, chat_id, user, match, token, event.message)
                    raise events.StopPropagation
        except events.StopPropagation:
            raise
        except Exception:
            logger.exception("Guard 消息检测失败 chat=%s", chat_id)

    async def handle_membership_update(self, update):
        if isinstance(update, (UpdateChannelParticipant, UpdateChatParticipantAdmin)):
            if update.user_id != self.bot_id:
                return
        chat_id = (
            -update.chat_id
            if isinstance(update, UpdateChatParticipantAdmin)
            else utils.get_peer_id(PeerChannel(update.channel_id))
        )
        self.directory.invalidate(chat_id)
        self.directory.refresh()

    async def handle_join(self, event):
        chat_id = event.chat_id
        self.directory.observe(
            chat_id,
            getattr(event, "chat", None),
            left=bool((event.user_left or event.user_kicked) and self.bot_id in event.user_ids),
        )
        if not self.group(chat_id).enabled:
            return
        try:
            if event.user_left or event.user_kicked:
                for user_id in event.user_ids:
                    async with self.repository.lock(user_id) as store:
                        session = await store.verification(chat_id, user_id)
                        # 超时踢出的 leave 事件不抢占已经持久化的踢出意图。
                        if session and session["status"] in {"waiting", "preparing"}:
                            session["status"] = "left"
                            await store.save_verification(session)
                return
            if not (event.user_joined or event.user_added):
                return
            for user_id in event.user_ids:
                user = await call(self.client.get_entity(user_id))
                action_message = getattr(event, "action_message", None)
                original = getattr(event, "original_update", None)
                identity = (
                    getattr(action_message, "id", None)
                    or ":".join(
                        str(getattr(original, key, "")) for key in ("date", "qts", "pts", "version")
                    ).strip(":")
                    or uuid.uuid4().hex
                )
                await self.process_join(chat_id, user, f"join:{identity}")
        except Exception:
            logger.exception("Guard 入群检测失败 chat=%s", chat_id)

    async def process_join(self, chat_id, user, token):
        if not self.group(chat_id).enabled or await self.protected(chat_id, user):
            return
        async with self.repository.lock(user.id) as store:
            config = self.group(chat_id)
            if not config.enabled:
                return
            match = await self.match(store, config, chat_id, user)
            if match:
                await self.punish(store, chat_id, user, match, token)
                if match.action in {"warn", "mute"} and config.verify_join:
                    await self.verification.begin(store, chat_id, user, token)
            elif config.verify_join and not await store.whitelisted(chat_id, user.id):
                await self.verification.begin(store, chat_id, user, token)

    async def handle_join_request(self, update):
        chat_id = utils.get_peer_id(update.peer)
        config = self.group(chat_id)
        if not config.enabled:
            return
        try:
            user = await call(self.client.get_entity(update.user_id))
            if await self.protected(chat_id, user, applicant=True):
                return
            async with self.repository.lock(user.id) as store:
                config = self.group(chat_id)
                if not config.enabled:
                    return
                matched = await self.match(store, config, chat_id, user)
                token = f"request:{update.date}:{getattr(update, 'qts', 0)}"
                if matched:
                    matched = Match(
                        "join_request",
                        matched.rule_id,
                        matched.reason,
                        matched.original,
                        matched.normalized,
                        config.join_request_match_action,
                    )
                    await self.punish(store, chat_id, user, matched, token)
                elif config.join_requests == "approve":
                    matched = Match(
                        "join_request",
                        "request-approve",
                        "approve_then_group_verification",
                        "",
                        "",
                        "approve",
                    )
                    await self.punish(store, chat_id, user, matched, token)
                else:
                    matched = Match(
                        "join_request", "request-manual", "manual_join_request", "", "", "manual"
                    )
                    await self.punish(store, chat_id, user, matched, token)
        except Exception:
            logger.exception("Guard 入群申请处理失败 chat=%s", chat_id)

    async def record_event(
        self,
        store,
        chat_id,
        user_id,
        action,
        token,
        category,
        *,
        actor_id=None,
        message_id=None,
        verification_state=None,
    ):
        event_key = hashlib.sha256(
            f"event:{chat_id}:{user_id}:{action}:{token}".encode()
        ).hexdigest()
        case = await store.case_for_event(event_key)
        if case is None:
            try:
                user = await call(self.client.get_entity(user_id))
            except Exception:
                user = SimpleNamespace(
                    id=user_id, first_name=str(user_id), last_name=None, username=None
                )
            evidence = Match(
                category, action, t(f"guard.event_{action}"), str(user_id), str(user_id), action
            )
            try:
                payload = await self.snapshot(chat_id, user, evidence)
            except Exception:
                payload = {
                    "chat_title": str(chat_id),
                    "name": getattr(user, "first_name", str(user_id)),
                    "username": getattr(user, "username", None),
                    "evidence": evidence.evidence(),
                }
            payload["notification_event"] = category
            case = {
                "id": uuid.uuid4().hex,
                "event_key": event_key,
                "chat_id": chat_id,
                "user_id": user_id,
                "action": action,
                "status": "done",
                "payload": payload,
                "reviewer_id": actor_id if isinstance(actor_id, int) else None,
                "reviewer_actor": "webui:admin"
                if actor_id == "admin"
                else f"telegram:{actor_id}"
                if actor_id
                else None,
                "reviewed_at": utc_now_naive() if actor_id else None,
            }
            await store.create_case(case)
            case = await store.case(case["id"])
        if message_id is not None:
            payload = dict(case["payload"])
            link = payload.get("chat_link") or (
                f"https://t.me/c/{str(chat_id)[4:]}" if str(chat_id).startswith("-100") else ""
            )
            payload.update(
                message_id=message_id,
                evidence_link=f"{link}/{message_id}" if link else "",
                verification_message=True,
                verification_session_id=token,
            )
            if verification_state is not None:
                payload.update(
                    verification_state=verification_state,
                    verification_admin_actions=self.group(chat_id).verification_admin_actions,
                )
            await store.update_case(
                case["id"], case["status"], error=case.get("error"), payload=payload
            )
            case = await store.case(case["id"])
            # 补发验证消息后原地更新已投递的通知；状态相同也不能跳过内容更新。
            for notice in await store.notices(case["id"]):
                await store.notice(
                    case["id"], notice["target_id"], notice["message_id"], None, None
                )
        await self.notifier.sync(store, case, self.group(chat_id))
        return case

    async def punish(self, store, chat_id, user, match, token, message=None):
        if not self.group(chat_id).enabled:
            return None
        event_key = hashlib.sha256(
            f"{chat_id}:{user.id}:{token}:{match.rule_id}".encode()
        ).hexdigest()
        existing = await store.case_for_event(event_key)
        if existing:
            return existing
        if match.action == "ban":
            existing = await store.active_ban(chat_id, user.id)
            if existing:
                if existing["status"] == "pending":
                    await self.actions.ban(chat_id, user.id)
                return existing
        payload = await self.snapshot(chat_id, user, match, message)
        case = {
            "id": uuid.uuid4().hex,
            "event_key": event_key,
            "chat_id": chat_id,
            "user_id": user.id,
            "action": match.action,
            "status": "executing",
            "payload": payload,
        }
        if match.action == "mute":
            payload["restriction_started_at"] = utc_now_naive().isoformat()
            payload["original_rights"] = await self.actions.rights(chat_id, user.id)
            payload["mute_until"] = int(
                (
                    datetime.now(UTC) + timedelta(seconds=self.group(chat_id).mute_seconds)
                ).timestamp()
            )
        await store.create_case(case)
        await self.execute_case(store, await store.case(case["id"]))
        return await store.case(case["id"])

    async def execute_case(self, store, case):
        if case["payload"].get("verification_failure"):
            await self.verification.execute_failure_case(store, case)
            return
        if case["payload"].get("verification_admin"):
            await self.verification.execute_admin_case(store, case)
            return
        chat_id, user_id = case["chat_id"], case["user_id"]
        config = self.group(chat_id)
        if case["action"] == "kick" and case["payload"].get("kick_banned"):
            try:
                await self.actions.unban(chat_id, user_id)
            except TelegramActionError as error:
                await store.update_case(case["id"], "executing", error=str(error))
            else:
                await store.update_case(case["id"], "done")
            await self.notifier.sync(store, await store.case(case["id"]), config)
            return
        if not config.enabled:
            await store.update_case(case["id"], "cancelled")
            return
        action, payload = case["action"], case["payload"]
        bot_message = payload.get("evidence", {}).get("target") == "bot_message"
        if bot_message and (
            not config.block_bot_messages or await store.whitelisted(chat_id, user_id)
        ):
            await store.update_case(case["id"], "cancelled")
            return
        try:
            user = await call(self.client.get_entity(user_id))
            if await self.protected(
                chat_id,
                user,
                applicant=action in {"approve", "reject", "manual", "ban", "kick"},
                allow_nonmember=bot_message,
            ):
                await store.update_case(case["id"], "cancelled")
                return
            if action == "ban":
                await self.actions.ban(chat_id, user_id)
                session = await store.verification(chat_id, user_id)
                if session and session["status"] in ACTIVE_VERIFICATIONS:
                    session["status"] = "blocked"
                    await store.save_verification(session)
            elif action == "delete":
                await mutate(self.client.delete_messages(chat_id, [payload["message_id"]]))
            elif action == "warn":
                await self.actions.send_once(
                    chat_id,
                    t("guard.warning", user_id=user_id, reason=payload["evidence"]["reason"]),
                    None,
                    stable_random_id(f"warn:{case['id']}"),
                )
            elif action == "mute":
                rights = mute_rights(payload["original_rights"], payload["mute_until"])
                await self.actions.set_rights(chat_id, user_id, rights)
            elif action == "kick":
                if not payload.get("kick_banned"):
                    await self.actions.ban(chat_id, user_id)
                    payload["kick_banned"] = True
                    await store.update_case(case["id"], "executing", payload=payload)
                await self.actions.unban(chat_id, user_id)
            elif action in {"reject", "approve"}:
                try:
                    await self.actions.join_request(chat_id, user_id, action == "approve")
                except TelegramActionError as error:
                    if str(error) not in {
                        "HideRequesterMissingError",
                        "UserAlreadyParticipantError",
                    }:
                        raise
                    # 申请不存在无法推断已批准；只在确认成员存在时恢复批准后的验证。
                    if action == "approve":
                        await self.actions.permissions(chat_id, user_id)
                if action == "approve" and config.verify_join:
                    await self.verification.begin(store, chat_id, user, f"request:{case['id']}")
        except TelegramActionError as error:
            # 踢出已完成 ban 阶段时必须继续恢复 unban，不能留下意外永久封禁。
            status = (
                "executing"
                if action == "kick" and payload.get("kick_banned")
                else "execution_failed"
            )
            await store.update_case(case["id"], status, error=str(error))
            logger.exception("Guard 处罚执行失败 case=%s", case["id"])
        else:
            await store.update_case(
                case["id"],
                "pending" if action == "ban" else "manual" if action == "manual" else "done",
            )
        await self.notifier.sync(store, await store.case(case["id"]), config)

    async def handle_callback(self, event):
        try:
            if event.data.startswith(b"sg:r:"):
                await self.reviews.callback(event)
            elif event.data.startswith(b"sg:v:"):
                await self.verification.callback(event)
            elif event.data.startswith(b"sg:n:"):
                await self.verification.notification_callback(event)
            else:
                await event.answer(t("guard.invalid_callback"), alert=True)
        except GuardError as error:
            await event.answer(t(str(error)), alert=True)
        except Exception:
            logger.exception("Guard 回调处理失败")
            await event.answer(t("guard.operation_failed"), alert=True)
        raise events.StopPropagation

    async def recover_once(self):
        cases, sessions = await self.repository.work()
        for candidate in cases:
            try:
                async with self.repository.lock(candidate["user_id"]) as store:
                    case = await store.case(candidate["id"])
                    if not case:
                        continue
                    if case["status"] == "executing":
                        await self.execute_case(store, case)
                    elif case["status"].startswith("reviewing_"):
                        await self.reviews.resume(store, case)
                    await self.notifier.sync(
                        store, await store.case(case["id"]), self.group(case["chat_id"])
                    )
            except Exception:
                logger.exception("Guard 案件恢复失败 case=%s", candidate["id"])
        for candidate in sessions:
            try:
                async with self.repository.lock(candidate["user_id"]) as store:
                    session = await store.verification_by_id(candidate["id"])
                    if session and (
                        session["status"] in ACTIVE_VERIFICATIONS
                        or (
                            session.get("message_id")
                            and session["payload"].get("cleanup_pending", True)
                        )
                        or (
                            session["payload"].get("lifecycle")
                            and not session["payload"].get("lifecycle_recorded")
                            and session["status"] in {"passed", "cancelled", "left"}
                        )
                    ):
                        await self.verification.tick(store, session)
            except Exception:
                logger.exception("Guard 验证恢复失败 session=%s", candidate["id"])

    async def _run(self):
        cleanup_at = utc_now_naive()
        while True:
            try:
                await self.recover_once()
                if utc_now_naive() >= cleanup_at:
                    await self.repository.cleanup(self.config.retention_days)
                    cleanup_at = utc_now_naive() + timedelta(hours=24)
            except Exception:
                logger.exception("Guard 后台恢复失败，将重试")
            self._wake.clear()
            try:
                await asyncio.wait_for(self._wake.wait(), timeout=5)
            except TimeoutError:
                pass
