# Copyright 2026 Sakura-Bot
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
"""可重启恢复的入群验证状态机；按钮绑定用户、群与验证消息。"""

import hashlib
import json
import logging
import re
import secrets
import uuid
from datetime import UTC, datetime, timedelta

from telethon import Button
from telethon.errors import MessageIdInvalidError, MessageNotModifiedError, UserNotParticipantError

from core.i18n.i18n import t
from core.infrastructure.database.mysql import utc_now_naive

from .config import GuardGroup
from .matcher import Match
from .moderator import GuardError
from .notification import RETIRED_NOTICES, stable_random_id
from .repository import ACTIVE_VERIFICATIONS
from .telegram import call, same_rights, verification_rights

logger = logging.getLogger(__name__)
CHALLENGE_VERSION = 3
ADMIN_ACTIONS = {b"b": "ban", b"x": "blacklist", b"s": "skip"}
ADMIN_RESULTS = {"ban": "admin_banned", "blacklist": "admin_blacklisted", "skip": "admin_skipped"}
ADMIN_ACTIONABLE_STATES = {"waiting", "passing", "cancelling", "held"}


def answer_context(session):
    payload = session["payload"]
    values = [
        session["id"],
        session["chat_id"],
        session["user_id"],
        payload["generation"],
        payload["question"],
        payload["options"],
    ]
    return hashlib.sha256(json.dumps(values, separators=(",", ":")).encode()).hexdigest()


def prepare_answer_tokens(session):
    payload = session["payload"]
    context = answer_context(session)
    if payload.get("answer_context") != context or not payload.get("answer_tokens"):
        payload["answer_tokens"] = {secrets.token_hex(12): value for value in payload["options"]}
        payload["answer_context"] = context


def answer_value(session, token):
    payload = session["payload"]
    if payload.get("answer_context") != answer_context(session):
        raise GuardError("guard.verification_refresh")
    value = payload.get("answer_tokens", {}).get(token)
    question = re.fullmatch(r"([1-9]|1[0-9]|20) \+ ([1-9]|1[0-9]|20)", payload["question"])
    if type(value) is not int or value not in payload["options"] or not question:
        raise GuardError("guard.invalid_callback")
    return value, sum(map(int, question.groups()))


def verified_answer(session):
    """恢复任务必须重验答题凭据，不能把一个 passing 字符串当作放行授权。"""
    payload = session["payload"]
    proof = payload.get("answer_receipt")
    if not isinstance(proof, dict):
        return False
    try:
        value, solution = answer_value(session, proof["token"])
        accepted_at = datetime.fromisoformat(proof["accepted_at"])
        return (
            type(proof["actor_id"]) is int
            and proof["actor_id"] == session["user_id"]
            and proof["session_id"] == session["id"]
            and proof["chat_id"] == session["chat_id"]
            and proof["message_id"] == session["message_id"]
            and proof["context"] == answer_context(session)
            and proof["value"] == value == solution
            and str(proof["query_id"]) in payload.get("handled_queries", [])
            and int(proof["query_id"]) > 0
            and accepted_at <= session["expires_at"]
            and accepted_at <= utc_now_naive()
        )
    except (GuardError, KeyError, TypeError, ValueError):
        return False


def verified_skip(session):
    intent = session["payload"].get("admin_action", {})
    try:
        authorized_at = datetime.fromisoformat(intent["authorized_at"])
        if authorized_at > utc_now_naive():
            return False
    except (KeyError, TypeError, ValueError):
        return False
    return (
        session["status"] == "admin_pending"
        and intent.get("action") == "skip"
        and type(intent.get("actor_id")) is int
        and intent["actor_id"] > 0
        and isinstance(intent.get("case_id"), str)
        and bool(re.fullmatch(r"[0-9a-f]{32}", intent["case_id"]))
        and intent.get("session_id") == session["id"]
        and intent.get("chat_id") == session["chat_id"]
        and intent.get("message_id") == session["message_id"]
    )


def verification_state_text(status):
    key = f"guard.session_{status}"
    label = t(key)
    return t(
        "guard.verification_state", status=t("guard.session_processing") if label == key else label
    )


def user_identity(user):
    name = " ".join(
        str(value)
        for value in (getattr(user, "first_name", None), getattr(user, "last_name", None))
        if value
    )
    return {
        "name": " ".join(name.split())[:256] or t("guard.unnamed_user"),
        "username": getattr(user, "username", None),
    }


def challenge_buttons(session, config=None):
    config = config or GuardGroup()
    prefix = f"sg:v:{session['id']}:"
    rows = (
        []
        if session["status"] == "held"
        else [
            [
                Button.inline(str(value), (prefix + "a" + token).encode())
                for token, value in session["payload"]["answer_tokens"].items()
            ],
        ]
    )
    management = [
        Button.inline(t(f"guard.verify_{action}_button"), (prefix + code.decode()).encode())
        for code, action in ADMIN_ACTIONS.items()
        if action in config.verification_admin_actions and action != "skip"
    ]
    if management:
        rows.append(management)
    if "skip" in config.verification_admin_actions:
        rows.append([Button.inline(t("guard.verify_skip_button"), (prefix + "s").encode())])
    return rows


def challenge_policy(config):
    return {
        key: getattr(config, key)
        for key in (
            "verification_max_attempts",
            "verification_timeout_action",
            "verification_wrong_action",
            "verification_admin_actions",
        )
    }


def challenge_text(session, identity, config=None):
    config = config or GuardGroup()
    return t(
        "guard.challenge_held" if session["status"] == "held" else "guard.challenge",
        name=identity["name"],
        username="@" + identity["username"] if identity.get("username") else t("guard.no_username"),
        user_id=session["user_id"],
        question=session["payload"]["question"],
        seconds=max(0, int((session["expires_at"] - utc_now_naive()).total_seconds())),
        attempts=config.verification_max_attempts,
        wrong_action=t(f"guard.verify_action_{config.verification_wrong_action}"),
        timeout_action=t(f"guard.verify_action_{config.verification_timeout_action}"),
        admin_hint="\n" + t("guard.admin_verification_hint")
        if config.verification_admin_actions
        else "",
    )


class Verification:
    def __init__(self, service):
        self.service = service
        self.actions = service.actions

    async def begin(self, store, chat_id, user, token):
        existing = await store.verification(chat_id, user.id)
        if existing and existing["status"] in ACTIVE_VERIFICATIONS:
            # 重放入群事件、批准事件与 ChatAction 竞争都复用当前挑战，不延长超时。
            return existing
        if existing and existing["payload"].get("join_token") == token:
            return existing
        original = await self.actions.rights(chat_id, user.id)
        if (
            existing
            and existing["status"] == "left"
            and same_rights(original, verification_rights(existing["payload"]["original_rights"]))
        ):
            # 成员退群后限制可能仍保留；重复加入时不能把验证禁言当成原有权限。
            original = existing["payload"]["original_rights"]
        restricted = verification_rights(original)
        first, second = secrets.randbelow(20) + 1, secrets.randbelow(20) + 1
        answer = first + second
        options = {answer}
        while len(options) < 4:
            options.add(secrets.randbelow(50) + 1)
        options = list(options)
        secrets.SystemRandom().shuffle(options)
        config = self.service.group(chat_id)
        session = {
            "id": uuid.uuid4().hex,
            "chat_id": chat_id,
            "user_id": user.id,
            "status": "preparing",
            "expires_at": utc_now_naive() + timedelta(seconds=config.verification_timeout),
            "message_id": None,
            "attempts": 0,
            "error": None,
            "payload": {
                "join_token": token,
                "question": f"{first} + {second}",
                "options": options,
                "correct": options.index(answer),
                "original_rights": original,
                "restricted_rights": restricted,
                "requested_rights": restricted.copy(),
                "generation": 0,
                "lifecycle": True,
            },
        }
        await store.save_verification(session)
        await self.tick(store, session)
        return session

    async def send_challenge(self, store, session, user):
        payload = session["payload"]
        held = session["status"] == "held"
        prepare_answer_tokens(session)
        await store.save_verification(session)
        identity = user_identity(user)
        config = self.service.group(session["chat_id"])
        session["message_id"] = await self.actions.send_once(
            session["chat_id"],
            challenge_text(session, identity, config),
            challenge_buttons(session, config),
            stable_random_id(f"verify:{session['id']}:{payload['generation']}"),
        )
        payload.update(
            identity,
            challenge_version=CHALLENGE_VERSION,
            challenge_policy=challenge_policy(config),
            held_message=held,
        )
        session["status"] = "held" if held else "waiting"
        session["error"] = None
        await store.save_verification(session)
        await self.record_start(store, session)

    async def record_start(self, store, session):
        if (
            session["payload"].get("start_recorded")
            and session["payload"].get("start_message_id") == session["message_id"]
        ):
            await self.sync_notification_controls(store, session)
            return
        await self.service.record_event(
            store,
            session["chat_id"],
            session["user_id"],
            "verify_start",
            session["id"],
            "verification_start",
            message_id=session["message_id"],
            verification_state=session["status"],
        )
        session["payload"]["start_recorded"] = True
        session["payload"]["start_message_id"] = session["message_id"]
        await store.save_verification(session)
        await self.sync_notification_controls(store, session)

    async def record_end(self, store, session):
        if (
            not session["payload"].get("lifecycle")
            or session["payload"].get("lifecycle_recorded")
            or session["status"] not in {"passed", "cancelled", "left"}
        ):
            return
        await self.service.record_event(
            store,
            session["chat_id"],
            session["user_id"],
            "verify_pass" if session["status"] == "passed" else "verify_cancel",
            session["id"],
            "verification_end",
            message_id=session["message_id"],
            verification_state=session["status"],
        )
        session["payload"]["lifecycle_recorded"] = True
        await store.save_verification(session)

    async def restore(self, session):
        if not verified_answer(session) and not verified_skip(session):
            raise GuardError("guard.verification_release_required")
        if verified_skip(session) and not await self.can_manage(
            session["payload"]["admin_action"]["actor_id"], session["chat_id"]
        ):
            raise GuardError("error.permission_denied")
        current = await self.actions.rights(session["chat_id"], session["user_id"])
        original = session["payload"]["original_rights"]
        if same_rights(current, original):
            return
        expected = session["payload"].get("requested_rights") or verification_rights(original)
        if not same_rights(current, expected):
            # 人工禁言/封禁不可被验证按钮覆盖。管理员修复权限后本意图可继续恢复。
            raise GuardError("guard.permissions_changed")
        await self.actions.set_rights(session["chat_id"], session["user_id"], original)

    async def finish_message(self, store, session):
        config = self.service.group(session["chat_id"])
        cleanup = session.get("message_id") and (
            session["payload"].get("cleanup_pending", True)
            or (
                config.verification_message_mode == "result"
                and session["payload"].get("message_result_status") != session["status"]
            )
        )
        try:
            if cleanup:
                if config.verification_message_mode == "delete":
                    await call(
                        self.actions.client.delete_messages(
                            session["chat_id"], [session["message_id"]]
                        )
                    )
                else:
                    text = t(
                        "guard.verification_finished",
                        name=session["payload"].get("name", str(session["user_id"])),
                        username="@" + session["payload"]["username"]
                        if session["payload"].get("username")
                        else t("guard.no_username"),
                        user_id=session["user_id"],
                        status=t(f"guard.session_{session['status']}"),
                    )
                    try:
                        await call(
                            self.actions.client.edit_message(
                                session["chat_id"],
                                session["message_id"],
                                text,
                                buttons=None,
                                parse_mode=None,
                                link_preview=False,
                            )
                        )
                    except (MessageNotModifiedError, MessageIdInvalidError):
                        pass
                    session["payload"]["message_result_status"] = session["status"]
        except Exception as error:
            session["payload"]["cleanup_pending"] = True
            session["error"] = f"cleanup:{type(error).__name__}"
            await store.save_verification(session)
            logger.warning("Guard 验证消息清理失败 session=%s", session["id"], exc_info=True)
        else:
            session["payload"]["cleanup_pending"] = False
            if session["status"] not in ACTIVE_VERIFICATIONS or (
                session.get("error") or ""
            ).startswith("cleanup:"):
                session["error"] = None
            await store.save_verification(session)
        await self.record_end(store, session)
        await self.sync_notification_controls(store, session)

    async def sync_notification_controls(self, store, session):
        policy = [
            session["status"],
            self.service.group(session["chat_id"]).verification_admin_actions,
            session.get("message_id"),
        ]
        if session["payload"].get("notification_controls_policy") == policy:
            return
        cases = []
        for action in ["verify_start", "verify_pass", "verify_cancel"]:
            event_key = hashlib.sha256(
                f"event:{session['chat_id']}:{session['user_id']}:{action}:{session['id']}".encode()
            ).hexdigest()
            case = await store.case_for_event(event_key)
            if case is not None:
                cases.append(case)
        failure_id = session["payload"].get("failure_intent", {}).get("case_id")
        if failure_id:
            failure = await store.case(failure_id)
            if failure is not None:
                cases.append(failure)
        for key in ["case_id", "notification_case_id"]:
            related_id = session["payload"].get("admin_action", {}).get(key)
            if related_id and related_id not in {case["id"] for case in cases}:
                related = await store.case(related_id)
                if related is not None:
                    cases.append(related)
        for case in cases:
            payload = dict(case["payload"])
            payload.update(
                verification_state=session["status"], verification_admin_actions=policy[1]
            )
            self.link_message(payload, session)
            if payload != case["payload"]:
                await store.update_case(
                    case["id"], case["status"], error=case.get("error"), payload=payload
                )
                for notice in (
                    await store.notices(case["id"])
                    if not payload.get("notification_replaced_by")
                    else []
                ):
                    if notice["target_id"]:
                        await store.notice(
                            case["id"], notice["target_id"], notice["message_id"], None, None
                        )
                await self.service.notifier.sync(
                    store, await store.case(case["id"]), self.service.group(session["chat_id"])
                )
        session["payload"]["notification_controls_policy"] = policy
        await store.save_verification(session)

    async def resume_timeout(self, store, session):
        config = self.service.group(session["chat_id"])
        if "failure_intent" not in session["payload"]:
            wrong = session["attempts"] >= config.verification_max_attempts
            session["payload"]["failure_intent"] = {
                "case_id": uuid.uuid4().hex,
                "action": config.verification_wrong_action
                if wrong
                else config.verification_timeout_action,
                "reason": "wrong" if wrong else "timeout",
            }
            await store.save_verification(session)
        intent = session["payload"]["failure_intent"]
        case = await store.case(intent["case_id"])
        if case is None:
            user = await call(self.actions.client.get_entity(session["user_id"]))
            action = intent["action"]
            evidence = Match(
                "verification",
                "verification-failed",
                t(f"guard.verify_reason_{intent['reason']}"),
                str(user.id),
                str(user.id),
                "verify_hold" if action == "allow" else action,
            )
            payload = await self.service.snapshot(session["chat_id"], user, evidence)
            self.link_message(payload, session)
            payload["verification_failure"] = {**intent, "session_id": session["id"]}
            payload["verification_result"] = {
                "ban": "failed_banned",
                "kick": "failed_kicked",
                "mute": "held",
                "warn": "held",
                "allow": "held",
            }[action]
            payload["mute_until"] = int(datetime.now(UTC).timestamp()) + config.mute_seconds
            await store.create_case(
                {
                    "id": intent["case_id"],
                    "event_key": hashlib.sha256(
                        f"verify-failed:{session['id']}".encode()
                    ).hexdigest(),
                    "chat_id": session["chat_id"],
                    "user_id": user.id,
                    "action": evidence.action,
                    "status": "executing",
                    "payload": payload,
                }
            )
            case = await store.case(intent["case_id"])
        if case["status"] == "executing":
            await self.execute_failure_case(store, case)
            case = await store.case(case["id"])
        if case["status"] == "executing":
            session["error"] = case.get("error") or "guard.operation_failed"
        else:
            session["status"] = case["payload"]["verification_result"]
            session["error"] = None
            session["payload"]["cleanup_pending"] = True
        await store.save_verification(session)
        await self.finish_message(store, session)
        if session["status"] == "held":
            await self.refresh_challenge(store, session)
        await self.service.notifier.sync(store, case, config)

    async def execute_failure_case(self, store, case):
        intent = case["payload"]["verification_failure"]
        session = await store.verification_by_id(intent["session_id"])
        if (
            not session
            or (session["chat_id"], session["user_id"]) != (case["chat_id"], case["user_id"])
            or session["payload"].get("failure_intent", {}).get("case_id") != case["id"]
        ):
            await store.update_case(
                case["id"],
                "cancelled",
                payload={**case["payload"], "verification_result": "cancelled"},
            )
            return
        try:
            action = intent["action"]
            if action == "kick" and case["payload"].get("kick_banned"):
                await self.actions.unban(case["chat_id"], case["user_id"])
                await store.update_case(case["id"], "done")
                return
            config = self.service.group(case["chat_id"])
            user = await call(self.actions.client.get_entity(case["user_id"]))
            if (
                not config.enabled
                or not config.verify_join
                or await self.service.protected(case["chat_id"], user, applicant=True)
                or await store.whitelisted(case["chat_id"], user.id)
            ):
                await store.update_case(
                    case["id"],
                    "cancelled",
                    payload={**case["payload"], "verification_result": "held"},
                )
                return
            active = await store.active_ban(case["chat_id"], case["user_id"])
            if active and active["id"] != case["id"]:
                await store.update_case(
                    case["id"],
                    "cancelled",
                    payload={**case["payload"], "verification_result": "blocked"},
                )
                return
            if action in {"allow", "warn", "mute"}:
                matched = await self.service.match(store, config, case["chat_id"], user)
                if matched and matched.action in {"ban", "kick"}:
                    await self.service.punish(
                        store, case["chat_id"], user, matched, f"verify:{session['id']}"
                    )
                    await store.update_case(
                        case["id"],
                        "cancelled",
                        payload={**case["payload"], "verification_result": "blocked"},
                    )
                    return
            if action == "ban":
                await self.actions.ban(case["chat_id"], case["user_id"])
            elif action == "kick":
                await self.actions.ban(case["chat_id"], case["user_id"])
                case["payload"]["kick_banned"] = True
                await store.update_case(case["id"], "executing", payload=case["payload"])
                await self.actions.unban(case["chat_id"], case["user_id"])
            elif action == "mute":
                # 验证失败不能变成一个到期后自动放行的临时禁言。
                rights = verification_rights(session["payload"]["original_rights"])
                current = await self.actions.rights(case["chat_id"], case["user_id"])
                if not same_rights(current, rights):
                    if not same_rights(
                        current, session["payload"]["original_rights"]
                    ) and not same_rights(
                        current, verification_rights(session["payload"]["original_rights"])
                    ):
                        raise GuardError("guard.permissions_changed")
                    await self.actions.set_rights(case["chat_id"], case["user_id"], rights)
            elif action == "warn":
                await self.actions.send_once(
                    case["chat_id"],
                    t(
                        "guard.warning",
                        user_id=case["user_id"],
                        reason=case["payload"]["evidence"]["reason"],
                    ),
                    None,
                    stable_random_id(f"verify-warn:{case['id']}"),
                )
            elif action != "allow":
                raise GuardError("guard.invalid_callback")
            await store.update_case(case["id"], "pending" if action == "ban" else "done")
        except Exception as error:
            await store.update_case(
                case["id"],
                "executing",
                error=str(error) if isinstance(error, GuardError) else type(error).__name__,
            )

    async def tick(self, store, session):
        config = self.service.group(session["chat_id"])
        try:
            if session["status"] not in ACTIVE_VERIFICATIONS:
                await self.finish_message(store, session)
                return
            if session["status"] == "admin_pending":
                await self.resume_admin(store, session)
                return
            if session["status"] == "kicking":
                await self.resume_timeout(store, session)
                return
            user = await call(self.actions.client.get_entity(session["user_id"]))
            if session["status"] != "kicking":
                try:
                    await self.actions.permissions(session["chat_id"], session["user_id"])
                except UserNotParticipantError:
                    session["status"] = "left"
                    await store.save_verification(session)
                    await self.finish_message(store, session)
                    return
            if session["status"] == "cancelling" or (
                session["status"] == "passing" and not verified_answer(session)
            ):
                # 旧版本或异常状态没有可核验凭据时，保留禁言、题目、次数和截止时间。
                session["status"] = "waiting"
                session["payload"].pop("answer_receipt", None)
                await store.save_verification(session)
            active_ban = await store.active_ban(session["chat_id"], session["user_id"])
            if active_ban:
                session["status"] = "blocked"
                await store.save_verification(session)
                await self.finish_message(store, session)
                return
            if session["status"] != "held" and (
                session["expires_at"] <= utc_now_naive()
                or session["attempts"] >= config.verification_max_attempts
            ):
                # 恢复权限失败也不能无限越过截止时间；超时先于 passing 重试。
                session["status"] = "kicking"
                await store.save_verification(session)
                await self.resume_timeout(store, session)
                return
            elif session["status"] == "passing":
                # 昵称或全局黑名单在验证过程中发生变化时，也不能绕过检测。
                matched = await self.service.match(store, config, session["chat_id"], user)
                if matched and matched.action in {"ban", "kick"}:
                    await self.service.punish(
                        store, session["chat_id"], user, matched, f"verify:{session['id']}"
                    )
                    session["status"] = "blocked"
                else:
                    await self.restore(session)
                    if matched:
                        await self.service.punish(
                            store, session["chat_id"], user, matched, f"verify:{session['id']}"
                        )
                    session["status"] = "passed"
            elif session["status"] == "preparing":
                current = await self.actions.rights(session["chat_id"], session["user_id"])
                expected = verification_rights(session["payload"]["original_rights"])
                if not same_rights(
                    current, session["payload"]["original_rights"]
                ) and not same_rights(current, expected):
                    raise GuardError("guard.permissions_changed")
                session["payload"]["requested_rights"] = expected
                session["payload"]["restricted_rights"] = expected.copy()
                await store.save_verification(session)
                await self.actions.set_rights(session["chat_id"], session["user_id"], expected)
                observed = await self.actions.rights(session["chat_id"], session["user_id"])
                # GetParticipant 可能暂时返回旧权限；它只能用于确认，不能替换已落盘意图。
                session["payload"]["restriction_confirmed"] = same_rights(observed, expected)
                await store.save_verification(session)
                await self.send_challenge(store, session, user)
                return
            elif session["status"] in {"waiting", "held"}:
                message = await call(
                    self.actions.client.get_messages(session["chat_id"], ids=session["message_id"])
                )
                if not message or getattr(message, "id", None) is None:
                    session["payload"]["generation"] += 1
                    session["message_id"] = None
                    await store.save_verification(session)
                    await self.send_challenge(store, session, user)
                    return
                identity = user_identity(user)
                if (
                    session["payload"].get("challenge_version") != CHALLENGE_VERSION
                    or session["payload"].get("challenge_policy") != challenge_policy(config)
                    or session["payload"].get("held_message", False)
                    != (session["status"] == "held")
                    or session["payload"].get("answer_context") != answer_context(session)
                    or any(session["payload"].get(key) != value for key, value in identity.items())
                ):
                    # 原地升级已在等待中的验证，保留题目、次数、消息 ID 和截止时间。
                    prepare_answer_tokens(session)
                    await store.save_verification(session)
                    try:
                        await call(
                            self.actions.client.edit_message(
                                session["chat_id"],
                                session["message_id"],
                                challenge_text(session, identity, config),
                                buttons=challenge_buttons(session, config),
                                parse_mode=None,
                                link_preview=False,
                            )
                        )
                    except MessageNotModifiedError:
                        pass
                    session["payload"].update(
                        identity,
                        challenge_version=CHALLENGE_VERSION,
                        challenge_policy=challenge_policy(config),
                        lifecycle=True,
                        held_message=session["status"] == "held",
                    )
                await self.record_start(store, session)
            session["error"] = None
            await store.save_verification(session)
            if session["status"] not in ACTIVE_VERIFICATIONS:
                await self.finish_message(store, session)
        except Exception as error:
            session["error"] = str(error) if isinstance(error, GuardError) else type(error).__name__
            await store.save_verification(session)
            logger.warning(
                "Guard 验证操作待恢复 session=%s error=%s", session["id"], session["error"]
            )

    async def refresh_challenge(self, store, session):
        session["payload"]["challenge_version"] = 0
        # tick 的 held 分支不会再次执行失败处理。
        await self.tick(store, session)

    @staticmethod
    def link_message(payload, session):
        message_id = session.get("message_id")
        if message_id is None:
            return
        link = payload.get("chat_link") or f"https://t.me/c/{str(session['chat_id'])[4:]}"
        payload.update(
            message_id=message_id,
            evidence_link=f"{link}/{message_id}",
            verification_message=True,
            verification_session_id=session["id"],
        )

    async def can_manage(self, actor_id, chat_id):
        if not isinstance(actor_id, int) or actor_id <= 0:
            return False
        if actor_id in self.service.default_admins():
            return True
        try:
            permissions = await self.actions.permissions(chat_id, actor_id)
            rights = getattr(getattr(permissions, "participant", None), "admin_rights", None)
            return bool(
                permissions.is_creator
                or (permissions.is_admin and getattr(rights, "ban_users", False))
            )
        except Exception:
            return False

    async def notification_callback(self, event):
        matched = re.fullmatch(rb"sg:n:([0-9a-f]{32}):([bxs])", event.data)
        if not matched:
            raise GuardError("guard.invalid_callback")
        case_id = matched[1].decode()
        case = await self.service.repository.get_case(case_id)
        session_id = case["payload"].get("verification_session_id") if case else None
        if not isinstance(session_id, str) or not re.fullmatch(r"[0-9a-f]{32}", session_id):
            raise GuardError("guard.invalid_callback")
        await self.admin_callback(
            event, session_id, ADMIN_ACTIONS[matched[2]], notice_case_id=case_id
        )

    async def valid_admin_origin(self, store, session, event, notice_case_id):
        if notice_case_id is None:
            return (session["chat_id"], session["message_id"]) == (event.chat_id, event.message_id)
        case = await store.case(notice_case_id)
        if not case or (case["chat_id"], case["user_id"]) != (
            session["chat_id"],
            session["user_id"],
        ):
            return False
        if case["payload"].get("verification_session_id") != session["id"]:
            return False
        if case["action"] not in {"verify_start", "verify_pass", "verify_skip"} and not case[
            "payload"
        ].get("verification_failure"):
            return False
        return any(
            (row["target_id"], row["message_id"]) == (event.chat_id, event.message_id)
            and row["synced_status"] not in RETIRED_NOTICES
            for row in await store.notices(notice_case_id)
        )

    async def admin_callback(self, event, session_id, action, *, notice_case_id=None):
        query_id = getattr(getattr(event, "query", None), "query_id", None)
        if type(query_id) is not int or query_id <= 0:
            raise GuardError("guard.invalid_callback")
        initial = await self.service.repository.get_verification(session_id)
        if not initial or (
            notice_case_id is None
            and (initial["chat_id"], initial["message_id"])
            != (
                event.chat_id,
                event.message_id,
            )
        ):
            raise GuardError("guard.invalid_callback")
        # 与答题、超时和案件审核使用同一目标用户锁，不按点击者加锁。
        async with self.service.repository.lock(initial["user_id"]) as store:
            session = await store.verification_by_id(session_id)
            if not session or not await self.valid_admin_origin(
                store, session, event, notice_case_id
            ):
                raise GuardError("guard.invalid_callback")
            if not await self.can_manage(event.sender_id, session["chat_id"]):
                raise GuardError("error.permission_denied")
            post_verification = (
                notice_case_id is not None
                and session["status"] in {"passed", "admin_skipped"}
                and action in {"ban", "blacklist"}
            )
            if (
                session["status"] not in ADMIN_ACTIONABLE_STATES | {"admin_pending"}
                and not post_verification
            ):
                await event.answer(verification_state_text(session["status"]), alert=True)
                return
            if session["status"] == "admin_pending":
                if session["payload"]["admin_action"]["action"] != action:
                    raise GuardError("guard.already_processing")
                if action == "skip" and (
                    not verified_skip(session)
                    or session["payload"]["admin_action"]["actor_id"] != event.sender_id
                ):
                    # 旧版没有授权凭据，或原操作者已离开时，新的管理员点击形成新凭据。
                    previous = await store.case(session["payload"]["admin_action"]["case_id"])
                    if previous and previous["status"] == "executing":
                        await store.update_case(
                            previous["id"],
                            "cancelled",
                            payload={**previous["payload"], "verification_result": "held"},
                        )
                    session["status"] = "held"
            if session["status"] != "admin_pending":
                config = self.service.group(session["chat_id"])
                if action not in config.verification_admin_actions:
                    raise GuardError("guard.verification_action_disabled")
                if (
                    session["expires_at"] <= utc_now_naive()
                    and session["status"] != "held"
                    and not post_verification
                ):
                    await self.tick(store, session)
                    await event.answer(verification_state_text(session["status"]), alert=True)
                    return
                user = await call(self.actions.client.get_entity(session["user_id"]))
                if action != "skip" and (
                    await self.service.protected(session["chat_id"], user)
                    or await store.whitelisted(session["chat_id"], user.id)
                ):
                    raise GuardError("guard.verification_target_protected")
                if await store.active_ban(session["chat_id"], user.id):
                    raise GuardError("guard.verification_blocked")
                session["payload"]["admin_action"] = {
                    "action": action,
                    "actor_id": event.sender_id,
                    "case_id": uuid.uuid4().hex,
                    "authorized_at": utc_now_naive().isoformat(),
                    "session_id": session["id"],
                    "chat_id": session["chat_id"],
                    "message_id": session["message_id"],
                    "notification_case_id": notice_case_id,
                    "origin_chat_id": event.chat_id,
                    "origin_message_id": event.message_id,
                }
                session["status"] = "admin_pending"
                session["error"] = None
                # 首次管理员选择先落盘；另一种操作、答题和超时不能覆盖它。
                await store.save_verification(session)
                await self.sync_notification_controls(store, session)
            await self.tick(store, session)
            key = (
                f"guard.{ADMIN_RESULTS[action]}"
                if session["status"] == ADMIN_RESULTS[action]
                else "guard.verification_blocked"
                if session["status"] == "blocked"
                else "guard.operation_failed"
            )
            if session["status"] == "admin_pending":
                reason = session.get("error") or "guard.operation_failed"
                await event.answer(
                    t(
                        "guard.admin_operation_pending",
                        reason=t(reason)
                        if reason.startswith("guard.")
                        else t("guard.operation_failed"),
                    ),
                    alert=True,
                )
            else:
                await event.answer(t(key), alert=True)

    async def resume_admin(self, store, session):
        intent = session["payload"]["admin_action"]
        action, case_id = intent["action"], intent["case_id"]
        if action not in ADMIN_RESULTS:
            raise GuardError("guard.invalid_callback")
        case = await store.case(case_id)
        if case is None:
            user = await call(self.actions.client.get_entity(session["user_id"]))
            match = Match(
                "verification",
                f"admin-verify-{action}",
                t(f"guard.verify_reason_{action}"),
                str(user.id),
                str(user.id),
                "verify_skip" if action == "skip" else "ban",
            )
            payload = await self.service.snapshot(session["chat_id"], user, match)
            self.link_message(payload, session)
            payload["verification_admin"] = {**intent, "session_id": session["id"]}
            payload["verification_result"] = ADMIN_RESULTS[action]
            case = {
                "id": case_id,
                "event_key": hashlib.sha256(
                    f"verify-admin:{session['id']}:{case_id}".encode()
                ).hexdigest(),
                "chat_id": session["chat_id"],
                "user_id": session["user_id"],
                "action": match.action,
                "status": "executing",
                "payload": payload,
                "reviewer_id": intent["actor_id"],
                "reviewer_actor": f"telegram:{intent['actor_id']}",
                "reviewed_at": utc_now_naive(),
            }
            await store.create_case(case)
            case = await store.case(case_id)
        if case["status"] == "executing":
            await self.execute_admin_case(store, case)
            case = await store.case(case_id)
        if case["status"] == "executing":
            session["error"] = case["error"] or "guard.operation_failed"
            await store.save_verification(session)
            return
        session["status"] = (
            "cancelled" if case["status"] == "allowed" else case["payload"]["verification_result"]
        )
        session["error"] = None
        await store.save_verification(session)
        await self.finish_message(store, session)
        await self.service.notifier.sync(store, case, self.service.group(session["chat_id"]))

    async def execute_admin_case(self, store, case):
        intent = case["payload"]["verification_admin"]
        session = await store.verification_by_id(intent["session_id"])
        if (
            not session
            or (session["chat_id"], session["user_id"]) != (case["chat_id"], case["user_id"])
            or session["status"] != "admin_pending"
            or session["payload"].get("admin_action", {}).get("case_id") != case["id"]
        ):
            payload = {**case["payload"], "verification_result": "cancelled"}
            await store.update_case(case["id"], "cancelled", payload=payload)
            return
        try:
            user = await call(self.actions.client.get_entity(case["user_id"]))
            action = intent["action"]
            if action == "skip":
                if not verified_skip(session) or intent != session["payload"].get("admin_action"):
                    raise GuardError("error.permission_denied")
                config = self.service.group(case["chat_id"])
                active_ban = await store.active_ban(case["chat_id"], case["user_id"])
                matched = (
                    await self.service.match(store, config, case["chat_id"], user)
                    if config.enabled
                    else None
                )
                if active_ban or (matched and matched.action in {"ban", "kick"}):
                    if not active_ban:
                        await self.service.punish(
                            store, case["chat_id"], user, matched, f"verify:{session['id']}"
                        )
                    payload = {**case["payload"], "verification_result": "blocked"}
                    await store.update_case(
                        case["id"], "cancelled", error="guard.verification_blocked", payload=payload
                    )
                    return
                await self.restore(session)
                if matched:
                    await self.service.punish(
                        store, case["chat_id"], user, matched, f"verify:{session['id']}"
                    )
                await store.update_case(case["id"], "done")
            elif action in {"ban", "blacklist"}:
                if await self.service.protected(
                    case["chat_id"], user, applicant=True
                ) or await store.whitelisted(case["chat_id"], user.id):
                    payload = {**case["payload"], "verification_result": "held"}
                    await store.update_case(case["id"], "cancelled", payload=payload)
                    return
                await self.actions.ban(case["chat_id"], case["user_id"])
                if action == "blacklist":
                    # 封禁确认成功后，黑名单与案件终态复用现有原子事务。
                    await store.finish_review(case, "blacklist")
                else:
                    await store.update_case(case["id"], "pending")
            else:
                raise GuardError("guard.invalid_callback")
        except Exception as error:
            # 已接受的人工意图可恢复；失败不伪装成已封禁/已跳过。
            await store.update_case(
                case["id"],
                "executing",
                error=str(error) if isinstance(error, GuardError) else type(error).__name__,
            )

    async def callback(self, event):
        admin = re.fullmatch(rb"sg:v:([0-9a-f]{32}):([bxs])", event.data)
        if admin:
            await self.admin_callback(event, admin[1].decode(), ADMIN_ACTIONS[admin[2]])
            return
        matched = re.fullmatch(rb"sg:v:([0-9a-f]{32}):(?:a([0-9a-f]{24})|([0-3]))", event.data)
        if not matched:
            raise GuardError("guard.invalid_callback")
        if type(event.sender_id) is not int or event.sender_id <= 0:
            raise GuardError("guard.invalid_callback")
        async with self.service.repository.lock(event.sender_id) as store:
            session = await store.verification_by_id(matched[1].decode())
            if (
                not session
                or session["user_id"] != event.sender_id
                or (session["chat_id"], session["message_id"]) != (event.chat_id, event.message_id)
            ):
                raise GuardError("guard.invalid_callback")
            if session["status"] != "waiting":
                await event.answer(verification_state_text(session["status"]), alert=True)
                return
            if (
                session["expires_at"] <= utc_now_naive()
                or session["attempts"]
                >= self.service.group(session["chat_id"]).verification_max_attempts
            ):
                await self.tick(store, session)
                await event.answer(t("guard.verification_expired"), alert=True)
                return
            if matched[3] or session["payload"].get("answer_context") != answer_context(session):
                await self.refresh_challenge(store, session)
                await event.answer(t("guard.verification_refresh"), alert=True)
                return
            value, solution = answer_value(session, matched[2].decode())
            query_id = getattr(getattr(event, "query", None), "query_id", None)
            if type(query_id) is not int or query_id <= 0:
                raise GuardError("guard.invalid_callback")
            query_id = str(query_id)
            handled = session["payload"].setdefault("handled_queries", [])
            if query_id in handled:
                await event.answer(t("guard.wrong_answer"), alert=True)
                return
            handled.append(query_id)
            receipt = {
                "actor_id": event.sender_id,
                "session_id": session["id"],
                "chat_id": event.chat_id,
                "message_id": event.message_id,
                "context": answer_context(session),
                "token": matched[2].decode(),
                "value": value,
                "query_id": query_id,
                "accepted_at": utc_now_naive().isoformat(),
            }
            session["payload"].setdefault("answer_events", []).append(receipt)
            logger.info(
                "Guard 验证答题 session=%s actor=%s value=%s correct=%s",
                session["id"],
                event.sender_id,
                value,
                value == solution,
            )
            if value != solution:
                session["attempts"] += 1
                if (
                    session["attempts"]
                    >= self.service.group(session["chat_id"]).verification_max_attempts
                ):
                    session["status"] = "kicking"
                await store.save_verification(session)
                if session["status"] == "kicking":
                    await self.tick(store, session)
                await event.answer(t("guard.wrong_answer"), alert=True)
                return
            session["status"] = "passing"
            session["payload"]["answer_receipt"] = receipt
            session["payload"]["answer_accepted_at"] = utc_now_naive().isoformat()
            session["payload"]["answer_actor_id"] = event.sender_id
            await store.save_verification(session)
            await self.tick(store, session)
            await event.answer(
                t("guard.verification_passed")
                if session["status"] == "passed"
                else t("guard.operation_failed"),
                alert=True,
            )
