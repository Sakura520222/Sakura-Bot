# Copyright 2026 Sakura-Bot
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
"""持久化多目标审核通知，失败回退与状态同步。"""

import hashlib
import logging
from datetime import timedelta
from html import escape

from telethon import Button
from telethon.errors import MessageIdInvalidError, MessageNotModifiedError
from telethon.extensions import html
from telethon.tl.types import InputMessageEntityMentionName, MessageEntityTextUrl

from core.i18n.i18n import t
from core.infrastructure.database.mysql import utc_now_naive

from .telegram import call

logger = logging.getLogger(__name__)
RETIRED_NOTICES = {"deleting", "removed"}


def stable_random_id(value):
    return int.from_bytes(hashlib.sha256(value.encode()).digest()[:8], "big", signed=True)


def is_ban_case(case):
    return case["action"] == "ban" or bool(case["payload"].get("review_ban_confirmed"))


def review_buttons(case, config=None):
    if case.get("payload", {}).get("notification_replaced_by"):
        return None
    generation = case.get("payload", {}).get("review_generation", 0)
    prefix = f"sg:r:{case['id']}:"
    if is_ban_case(case) and case["status"] == "pending":
        return [
            [Button.inline(t(key), f"{prefix}{code}:{generation}".encode())]
            for code, key in [
                ("u", "guard.unban_button"),
                ("a", "guard.allow_button"),
                ("b", "guard.blacklist_button"),
            ]
        ]
    verification_failure = bool(case["payload"].get("verification_failure"))
    if case["action"] in {"mute", "warn"} and case["status"] == "done" and not verification_failure:
        return [
            [Button.inline(t(key), f"{prefix}{code}:{generation}".encode())]
            for code, key in [
                (
                    "u",
                    "guard.unmute_button"
                    if case["action"] == "mute"
                    else "guard.ignore_warning_button",
                ),
                (
                    "a",
                    "guard.unmute_allow_button"
                    if case["action"] == "mute"
                    else "guard.whitelist_button",
                ),
                ("k", "guard.verify_ban_button"),
                ("b", "guard.verify_blacklist_button"),
            ]
        ]
    if case["action"] not in {"verify_start", "verify_pass", "verify_skip"} and not (
        verification_failure and case["payload"].get("verification_result") == "held"
    ):
        return None
    state = case["payload"].get("verification_state") or (
        "admin_skipped"
        if case["action"] == "verify_skip" and case["status"] == "done"
        else "held"
        if verification_failure and case["payload"].get("verification_result") == "held"
        else None
    )
    active = state in {"waiting", "passing", "cancelling", "held"}
    if not active and state not in {"passed", "admin_skipped"}:
        return None
    allowed = (
        config.verification_admin_actions
        if config
        else case["payload"].get("verification_admin_actions", ["ban", "blacklist", "skip"])
    )
    return [
        [Button.inline(t(f"guard.verify_{action}_button"), f"sg:n:{case['id']}:{code}".encode())]
        for code, action in [("b", "ban"), ("x", "blacklist"), ("s", "skip")]
        if action in allowed and (action != "skip" or active)
    ] or None


def notification_text(case, *, link_profile=False):
    payload = case["payload"]
    evidence = payload.get("evidence", {})
    status = t(f"guard.status_{case['status']}")
    if case["action"] in {"verify_start", "verify_pass", "verify_cancel"} and payload.get(
        "verification_state"
    ):
        status = t(f"guard.session_{payload['verification_state']}")
    elif payload.get("release_deferred_verification"):
        status = t("guard.status_release_waiting")
    elif (
        case["status"] == "allowed" and not is_ban_case(case) and case["action"] in {"mute", "warn"}
    ):
        status = t(f"guard.status_{case['action']}_allowed")
    elif payload.get("verification_result") == "held" and case["status"] in {"done", "cancelled"}:
        status = t("guard.session_held")
    values = dict(
        case_id=case["id"],
        chat=payload.get("chat_title", ""),
        chat_id=case["chat_id"],
        chat_link=payload.get("chat_link", ""),
        name=payload.get("name") or str(case["user_id"]),
        user_id=case["user_id"],
        username="@" + payload["username"] if payload.get("username") else "—",
        target=evidence.get("target", ""),
        rule=evidence.get("rule_id", ""),
        reason=evidence.get("reason", "")[:256],
        original=evidence.get("original", "")[:200],
        normalized=evidence.get("normalized", "")[:200],
        action="ban" if case["payload"].get("review_ban_confirmed") else case["action"],
        status=status,
        time=str(case["created_at"]) + " UTC",
        message_id=payload.get("message_id") or "—",
        evidence_link=t("guard.verification_link", link=payload["evidence_link"])
        if payload.get("verification_message") and payload.get("evidence_link")
        else payload.get("evidence_link", ""),
        reviewer=case.get("reviewer_actor") or case.get("reviewer_id") or "—",
        reviewed_at=str(case.get("reviewed_at") or "—"),
        error=case.get("error") or "—",
    )
    if link_profile:
        # 仅由程序生成链接，所有用户内容保持原样显示，不能注入 HTML。
        values = {key: escape(str(value)) for key, value in values.items()}
        values["name"] = f'<a href="tg://user?id={int(case["user_id"])}">{values["name"]}</a>'
        for key in ("chat_link", "evidence_link"):
            link = payload.get(key)
            if link and link.startswith("https://t.me/"):
                escaped = escape(link)
                values[key] = values[key].replace(escaped, f'<a href="{escaped}">{escaped}</a>')
    return t("guard.notification", **values)


def notification_event(case):
    payload = case["payload"]
    if payload.get("notification_event"):
        return payload["notification_event"]
    if payload.get("verification_admin"):
        return "admin_action"
    if (
        payload.get("verification_failure")
        or payload.get("evidence", {}).get("rule_id") == "verification-failed"
    ):
        return "verification_fail"
    if case["status"] == "blacklisted" or case["action"] == "blacklist_remove":
        return "blacklist"
    if case["status"] == "allowed" or case["action"].startswith("whitelist_"):
        return "whitelist"
    return "join_request" if case["action"] in {"approve", "reject", "manual"} else "moderation"


class Notifier:
    def __init__(self, actions, default_admins):
        self.actions = actions
        self.default_admins = default_admins

    @staticmethod
    async def save_payload(store, case, **changes):
        payload = {**case["payload"], **changes}
        if payload != case["payload"]:
            await store.update_case(
                case["id"], case["status"], error=case.get("error"), payload=payload
            )
            case["payload"] = payload

    async def prepare_cleanup(self, store, case, config, notices, targets, enabled):
        payload = case["payload"]
        final = (
            case["action"] == "verify_pass"
            or bool(payload.get("verification_failure"))
            or bool(payload.get("verification_admin"))
        )
        if case["status"] in {"executing", "execution_failed"}:
            return None
        session_id = (
            payload.get("verification_session_id")
            or (payload.get("verification_failure") or {}).get("session_id")
            or (payload.get("verification_admin") or {}).get("session_id")
        )
        if not final or not session_id:
            if payload.get("notification_cleanup_pending"):
                await self.save_payload(
                    store,
                    case,
                    notification_cleanup_pending=False,
                    notification_cleanup_retry_at=None,
                )
            return None
        event_key = hashlib.sha256(
            f"event:{case['chat_id']}:{case['user_id']}:verify_start:{session_id}".encode()
        ).hexdigest()
        parent = await store.case_for_event(event_key)
        if (
            not parent
            or parent["id"] == case["id"]
            or (parent["chat_id"], parent["user_id"]) != (case["chat_id"], case["user_id"])
        ):
            if case["payload"].get("notification_cleanup_pending"):
                await self.save_payload(
                    store,
                    case,
                    notification_cleanup_pending=False,
                    notification_cleanup_retry_at=None,
                )
            return None
        if "notification_cleanup_enabled" not in payload:
            await self.save_payload(
                store, case, notification_cleanup_enabled=config.verification_notice_cleanup
            )
        if not case["payload"]["notification_cleanup_enabled"]:
            await self.save_payload(
                store, case, notification_cleanup_pending=False, notification_cleanup_retry_at=None
            )
            return None
        if parent["payload"].get("notification_replaced_by") not in {None, case["id"]}:
            await self.save_payload(
                store, case, notification_cleanup_pending=False, notification_cleanup_retry_at=None
            )
            return None
        potential = set(targets) | set(notices)
        if enabled and config.review_chat_id:
            potential.add(config.review_chat_id)
        potential.discard(0)
        if not potential:
            await self.save_payload(
                store, case, notification_cleanup_pending=False, notification_cleanup_retry_at=None
            )
            return None
        pending = any(
            row["target_id"] in potential
            and row["target_id"] != 0
            and row["synced_status"] != "removed"
            for row in await store.notices(parent["id"])
        )
        # 先持久化清理意图，再发送结果；发送后的任意中断都能继续清理。
        await self.save_payload(
            store,
            case,
            verification_session_id=session_id,
            notification_cleanup_source=parent["id"],
            notification_cleanup_pending=pending,
            notification_cleanup_retry_at=case["payload"].get("notification_cleanup_retry_at")
            or "1970-01-01T00:00:00",
        )
        await self.save_payload(
            store, parent, notification_replaced_by=case["id"], notification_cleanup_pending=pending
        )
        return parent

    async def cleanup_replaced(self, store, case, parent):
        delivered = {row["target_id"]: row for row in await store.notices(case["id"])}
        pending = []
        now = utc_now_naive()
        for row in await store.notices(parent["id"]):
            target = row["target_id"]
            replacement = delivered.get(target)
            if not target or not replacement or row["synced_status"] == "removed":
                continue
            if (
                not replacement.get("message_id")
                or replacement.get("error")
                or replacement["synced_status"] != case["status"]
            ):
                pending.append(replacement.get("retry_at") or now + timedelta(seconds=30))
                continue
            if row.get("error") and row["synced_status"] == "deleting" and row["retry_at"] > now:
                pending.append(row["retry_at"])
                continue
            message_id = row.get("message_id")
            await store.notice(
                parent["id"], target, message_id, "deleting", None, row.get("attempts", 0)
            )
            try:
                # 同一消息已被替换时不能误删结果通知；未送达的开始通知只需写入墓碑。
                if message_id and message_id != replacement["message_id"]:
                    await call(self.actions.client.delete_messages(target, [message_id]))
            except MessageIdInvalidError:
                pass
            except Exception as error:
                await store.notice(
                    parent["id"],
                    target,
                    message_id,
                    "deleting",
                    f"cleanup:{type(error).__name__}",
                    row.get("attempts", 0) + 1,
                )
                updated = next(
                    item
                    for item in await store.notices(parent["id"])
                    if item["target_id"] == target
                )
                pending.append(updated["retry_at"])
                logger.warning(
                    "Guard 旧验证通知清理待重试 case=%s target=%s error=%s",
                    parent["id"],
                    target,
                    type(error).__name__,
                )
                continue
            await store.notice(
                parent["id"], target, message_id, "removed", None, row.get("attempts", 0)
            )
        retry = min(pending).isoformat(timespec="seconds") if pending else None
        await self.save_payload(
            store,
            case,
            notification_cleanup_pending=bool(pending),
            notification_cleanup_retry_at=retry,
        )
        await self.save_payload(store, parent, notification_cleanup_pending=bool(pending))

    async def sync(self, store, case, config):
        if case["action"] == "verify_start":
            current = await store.case(case["id"])
            if (current or case)["payload"].get("notification_replaced_by"):
                return
        notices = {row["target_id"]: row for row in await store.notices(case["id"])}
        enabled = (
            config.notification_enabled and notification_event(case) in config.notification_events
        )
        targets = (config.notification_ids or self.default_admins()) if enabled else []
        targets = list(dict.fromkeys(targets))
        parent = await self.prepare_cleanup(store, case, config, notices, targets, enabled)
        if not enabled and not any(notices):
            if notices.get(0, {}).get("synced_status") != case["status"]:
                await store.notice(case["id"], 0, None, case["status"], None)
            if parent:
                await self.cleanup_replaced(store, case, parent)
            return
        failed = not targets
        text, entities = html.parse(notification_text(case, link_profile=True))
        buttons = review_buttons(case, config)
        profile_url = f"tg://user?id={int(case['user_id'])}"
        try:
            user = await self.actions.input_user(case["user_id"])
        except Exception as error:
            # 资料解析异常不应阻断成员处理通知；已注销或无权访问的账号仍保留 ID。
            entities = [
                entity
                for entity in entities
                if not isinstance(entity, MessageEntityTextUrl) or entity.url != profile_url
            ]
            logger.warning(
                "Guard 用户资料引用解析失败 case=%s error=%s", case["id"], type(error).__name__
            )
        else:
            entities = [
                InputMessageEntityMentionName(entity.offset, entity.length, user)
                if isinstance(entity, MessageEntityTextUrl) and entity.url == profile_url
                else entity
                for entity in entities
            ]

        async def deliver(target):
            row = notices.get(target, {})
            if row.get("synced_status") in RETIRED_NOTICES:
                return True
            message_id = row.get("message_id")
            if row.get("synced_status") == case["status"] and not row.get("error"):
                return True
            if row.get("error") and row.get("retry_at", utc_now_naive()) > utc_now_naive():
                return False
            try:
                if message_id:
                    try:
                        await call(
                            self.actions.client.edit_message(
                                target,
                                message_id,
                                text,
                                buttons=buttons,
                                formatting_entities=entities,
                                parse_mode=None,
                                link_preview=False,
                            )
                        )
                    except MessageNotModifiedError:
                        pass
                else:
                    message_id = await self.actions.send_once(
                        target,
                        text,
                        buttons,
                        stable_random_id(f"guard:{case['id']}:{target}"),
                        entities=entities,
                    )
            except Exception as error:
                logger.warning(
                    "Guard 通知投递失败 case=%s target=%s error=%s",
                    case["id"],
                    target,
                    type(error).__name__,
                )
                await store.notice(
                    case["id"],
                    target,
                    message_id,
                    row.get("synced_status"),
                    type(error).__name__,
                    row.get("attempts", 0) + 1,
                )
                return False
            await store.notice(case["id"], target, message_id, case["status"])
            return True

        # 原收件人即使已从配置移除，仍更新其历史审核消息。
        for target in list(dict.fromkeys([*targets, *(value for value in notices if value)])):
            if not await deliver(target):
                failed = True
        if enabled and failed and config.review_chat_id:
            await deliver(config.review_chat_id)
        elif enabled and not targets and not notices:
            logger.error("Guard 案件未配置通知目标 case=%s", case["id"])
            await store.notice(case["id"], 0, None, None, "no_notification_target", 1)
        if parent:
            await self.cleanup_replaced(store, case, parent)
