# Copyright 2026 Sakura-Bot
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
"""管理员超时未处理的举报：读取证据、释放用户锁调用 AI、重新核验后执行。"""

import asyncio
import hashlib
import json
import logging
from datetime import UTC, timedelta
from types import SimpleNamespace

from telethon.tl.functions.users import GetFullUserRequest
from telethon.tl.types import MessageEntityTextUrl

from core.infrastructure.database.mysql import utc_now_naive

from .ai_report_classifier import Verdict, actionable, classify
from .ai_report_repository import AIReportJobs, atomic
from .matcher import extract_links
from .telegram import call

logger = logging.getLogger(__name__)
AI_ACTOR = "system:ai_report"


def fingerprint(value):
    return hashlib.sha256(
        json.dumps(value, sort_keys=True, ensure_ascii=False).encode()
    ).hexdigest()


def message_snapshot(message):
    buttons = [
        {
            "text": (getattr(button, "text", "") or "")[:256],
            "url": (
                getattr(button, "url", None)
                or getattr(getattr(button, "type", None), "url", "")
                or ""
            )[:2048],
        }
        for row in getattr(getattr(message, "reply_markup", None), "rows", None) or []
        for button in getattr(row, "buttons", None) or []
    ][:20]
    return {
        "text": (getattr(message, "message", "") or "")[:16384],
        "links": [url[:2048] for url in extract_links(message)[:20]],
        "buttons": buttons,
        "forwarded": getattr(message, "fwd_from", None) is not None,
        "has_media": bool(getattr(message, "media", None)),
        "edit_date": str(getattr(message, "edit_date", "") or ""),
    }


def match_message(snapshot):
    return SimpleNamespace(
        message=snapshot["text"],
        entities=[MessageEntityTextUrl(0, 0, url) for url in snapshot["links"]],
        reply_markup=SimpleNamespace(rows=[SimpleNamespace(buttons=snapshot["buttons"])])
        if snapshot["buttons"]
        else None,
        fwd_from=True if snapshot["forwarded"] else None,
    )


def untouched(case):
    return bool(
        case
        and case["action"] == "report"
        and case["status"] == "pending"
        and not case.get("reviewer_actor")
        and not case.get("reviewer_id")
        and not case["payload"].get("review_history")
        and not case["payload"].get("report_message_deletion")
    )


class AIReportReview:
    def __init__(self, service):
        self.service = service
        self.jobs = AIReportJobs(service.repository)
        self.classifier = classify
        self.worker = None
        self.wake_event = asyncio.Event()

    def enabled(self, case):
        config = self.service.group(case["chat_id"])
        return config.enabled and config.ai_report_review_enabled

    def policy_hash(self, case):
        return fingerprint(
            {
                "group": self.service.group(case["chat_id"]).model_dump(),
                "nickname_keywords": self.service.config.nickname_keywords,
            }
        )

    async def enqueue(self, store, case):
        if self.enabled(case):
            config = self.service.group(case["chat_id"])
            await self.jobs.enqueue(
                store, case, timedelta(minutes=config.ai_report_review_delay_minutes)
            )
            await self.annotate(
                store, case, "queued", delay_minutes=config.ai_report_review_delay_minutes
            )
            self.wake_event.set()

    async def profile(self, user_id):
        user = await call(self.service.client.get_entity(user_id))
        bio, bio_error = None, None
        try:
            result = await call(
                self.service.client(
                    GetFullUserRequest(await self.service.actions.input_user(user_id))
                )
            )
            if result.full_user.id != user_id:
                raise ValueError("ProfileIdentityMismatch")
            user = next(item for item in result.users if item.id == user_id)
            bio = (result.full_user.about or "")[:4096]
        except Exception as error:
            bio_error = type(error).__name__
        if user.id != user_id:
            raise ValueError("ProfileIdentityMismatch")
        profile = {
            "nickname": " ".join(
                filter(None, [getattr(user, "first_name", None), getattr(user, "last_name", None)])
            )[:256],
            "usernames": list(
                dict.fromkeys(
                    filter(
                        None,
                        [
                            getattr(user, "username", None),
                            *(
                                alias.username
                                for alias in getattr(user, "usernames", None) or []
                                if getattr(alias, "active", False)
                            ),
                        ],
                    )
                )
            )[:20],
            "bio": bio,
            "bio_error": bio_error,
            "is_bot": bool(getattr(user, "bot", False)),
        }
        return user, profile

    async def context(self, store, case):
        """只发送被举报用户自身的资料与消息；本地确定规则的优先级和动作。"""
        config = self.service.group(case["chat_id"])
        user, profile = await self.profile(case["user_id"])
        existing = await store.active_ban(case["chat_id"], user.id)
        if existing and existing["id"] != case["id"]:
            raise ValueError("ExistingBanCase")
        if await self.service.protected(case["chat_id"], user, allow_nonmember=True):
            raise ValueError("ProtectedUser")
        from telethon.errors import UserNotParticipantError

        try:
            member = await self.service.actions.permissions(case["chat_id"], user.id)
        except UserNotParticipantError:
            member = None
        participant = getattr(member, "participant", None)
        if getattr(getattr(participant, "banned_rights", None), "view_messages", False):
            # 只恢复本 AI 执行意图之后由主 Bot 发起的封禁，不接管其他人的新封禁。
            date, started = getattr(participant, "date", None), case.get("reviewed_at")
            own_retry = (
                case["status"] == "executing"
                and case["action"] == "ban"
                and case.get("reviewer_actor") == AI_ACTOR
                and getattr(participant, "kicked_by", None) == self.service.bot_id
                and date is not None
                and started is not None
                and date.replace(tzinfo=UTC) >= started.replace(tzinfo=UTC) - timedelta(seconds=1)
            )
            if not own_retry:
                raise ValueError("AlreadyBannedOutsideAIReview")
        black = await store.blacklist(user.id)
        if await store.whitelisted(case["chat_id"], user.id) and not (
            black and config.share_global_blacklist
        ):
            raise ValueError("WhitelistedUser")
        message = await call(
            self.service.client.get_messages(case["chat_id"], ids=case["payload"]["message_id"])
        )
        snapshot = case["payload"].get("reported_message")
        if not snapshot or not message or getattr(message, "sender_id", None) != user.id:
            raise ValueError("ReportEvidenceUnavailable")
        if message_snapshot(message) != snapshot:
            raise ValueError("ReportMessageChanged")
        hit = await self.service.match(
            store, config, case["chat_id"], user, match_message(snapshot)
        )
        return {
            "profile": profile,
            "message": snapshot,
            "blacklist": {
                "matched": bool(black),
                "shared": config.share_global_blacklist,
                "source_case": black.get("case_id") if black else None,
            },
            "rule_match": hit.evidence() if hit else None,
            "policy": {
                "nickname_filter": config.nickname_filter,
                "nickname_keywords": self.service.config.nickname_keywords[:200],
                "nickname_keyword_count": len(self.service.config.nickname_keywords),
                "nickname_action": config.nickname_action,
                "message_filter": config.message_filter,
                "message_keywords": config.message_keywords,
                "message_action": config.message_action,
                "rules": [rule.model_dump() for rule in config.rules],
                "block_bot_messages": config.block_bot_messages,
                "block_button_messages": config.block_button_messages,
                "block_forwarded_messages": config.block_forwarded_messages,
                "fallback_action": config.ai_report_review_action,
            },
        }

    async def annotate(self, store, case, state, **details):
        payload = dict(case["payload"])
        payload["ai_report_review"] = {
            **payload.get("ai_report_review", {}),
            **details,
            "state": state,
            "updated_at": utc_now_naive().isoformat(),
        }
        payload["report_notice_dirty"] = True
        await store.update_case(
            case["id"], case["status"], error=case.get("error"), payload=payload
        )

    async def manual(self, store, case, job, reason):
        await self.jobs.save(store, job, "manual", job["payload"], error=reason)
        # 人工已接手时只结束 AI 任务，不改动人工正在编辑的案件。
        if untouched(case):
            await self.annotate(store, case, "manual", error=reason)

    async def run_once(self):
        for candidate in await self.jobs.due():
            try:
                await self.process(candidate)
            except asyncio.CancelledError:
                raise
            except Exception as error:
                # 不记录上游异常正文，防止把密钥或请求内容泄露进公共日志。
                logger.warning(
                    "Guard AI 举报复核暂缓 case=%s error=%s",
                    candidate["case_id"],
                    type(error).__name__,
                )

    async def process(self, candidate):
        service = self.service
        async with service.repository.lock(candidate["user_id"]) as store:
            job = await self.jobs.get(store, candidate["case_id"])
            case = await store.case(candidate["case_id"])
            if not job or job["token"] != candidate["token"]:
                return
            if job["status"] == "executing":
                await self.complete(store, case, job)
                return
            job = await self.jobs.claim(store, job)
            if not job:
                return
            if not untouched(case) or not self.enabled(case):
                await self.manual(store, case, job, "AlreadyHandledOrDisabled")
                return
            if job["attempts"] > 3:
                await self.manual(store, case, job, "RetryLimitReached")
                return
            # 修改等待时间后，以当前配置重新计算到期时间，不能提前启动。
            deadline = case["created_at"] + timedelta(
                minutes=service.group(case["chat_id"]).ai_report_review_delay_minutes
            )
            if deadline > utc_now_naive():
                await self.jobs.reschedule(store, job, deadline)
                return
            if await store.active_ban(case["chat_id"], case["user_id"]):
                await self.manual(store, case, job, "ExistingBanCase")
                return
            policy = self.policy_hash(case)
            try:
                context = await self.context(store, case)
            except Exception as error:
                await self.manual(store, case, job, type(error).__name__ + ":EvidenceCheck")
                return
            audit = {
                **job["payload"],
                "input": context,
                "policy_hash": policy,
                "started_at": utc_now_naive().isoformat(),
            }
            await self.jobs.save(store, job, "running", audit, delay=180)

        # 网络模型调用期间不持有 MySQL 连接或 Guard 用户锁；人工可随时处理。
        try:
            result = await self.classifier(context)
            verdict = Verdict.model_validate(result["verdict"])
            failure = None
        except Exception as error:
            failure = type(error).__name__
            result = {"error": failure}

        async with service.repository.lock(candidate["user_id"]) as store:
            current = await self.jobs.get(store, candidate["case_id"])
            case = await store.case(candidate["case_id"])
            if not current or current["token"] != job["token"] or current["status"] != "running":
                return  # 旧租约迟到的结果没有权力修改新任务。
            audit.update(result=result, finished_at=utc_now_naive().isoformat())
            job["payload"] = audit
            if not untouched(case) or not self.enabled(case) or self.policy_hash(case) != policy:
                await self.manual(store, case, job, "AlreadyHandledOrPolicyChanged")
                return
            if failure:
                state = "queued" if job["attempts"] < 3 else "manual"
                audit["errors"] = [*audit.get("errors", []), failure]
                await self.jobs.save(store, job, state, audit, delay=60, error=failure)
                if state == "manual":
                    await self.annotate(store, case, "manual", error=failure)
                return
            if not actionable(verdict, context):
                await self.jobs.save(store, job, "manual", audit)
                await self.annotate(store, case, "manual", input=context, **result)
                return
            try:
                fresh = await self.context(store, case)
            except Exception as error:
                await self.manual(store, case, job, type(error).__name__ + ":Recheck")
                return
            if fingerprint(fresh) != fingerprint(context) or self.policy_hash(case) != policy:
                await self.manual(store, case, job, "EvidenceOrPolicyChanged")
                return
            hit = context["rule_match"]
            action = (
                hit["action"] if hit else service.group(case["chat_id"]).ai_report_review_action
            )
            if action == "kick" and context["blacklist"]["matched"]:
                await self.manual(store, case, job, "GlobalBlacklistConflict")
                return
            payload = dict(case["payload"])
            payload["ai_report_review"] = {
                "state": "executing",
                **result,
                "input": context,
                "policy_hash": policy,
                "token": job["token"],
                "action": action,
                "updated_at": utc_now_naive().isoformat(),
            }
            payload["report_notice_dirty"] = True
            payload["review_generation"] = payload.get("review_generation", 0) + 1
            if action == "mute":
                payload["original_rights"] = await service.actions.rights(
                    case["chat_id"], case["user_id"]
                )
                payload["mute_until"] = int(
                    (
                        utc_now_naive()
                        + timedelta(seconds=service.group(case["chat_id"]).mute_seconds)
                    )
                    .replace(tzinfo=UTC)
                    .timestamp()
                )
            async with atomic(store):
                if not await self.jobs.convert(store, case, action, payload):
                    raise RuntimeError("AIReviewClaimLost")
                await self.jobs.save(store, job, "executing", audit)
            await self.complete(store, await store.case(case["id"]), job)

    async def execution_allowed(self, store, case):
        info = case["payload"].get("ai_report_review")
        if not info or info.get("state") != "executing" or case.get("reviewer_actor") != AI_ACTOR:
            return True
        job = await self.jobs.get(store, case["id"])
        valid = bool(
            job
            and job["status"] == "executing"
            and job["token"] == info.get("token")
            and case["action"] == info.get("action")
            and self.enabled(case)
            and self.policy_hash(case) == info.get("policy_hash")
        )
        if valid:
            try:
                valid = fingerprint(await self.context(store, case)) == fingerprint(info["input"])
            except Exception:
                valid = False
        if valid and self.policy_hash(case) == info["policy_hash"]:
            return True
        payload = {
            **case["payload"],
            "ai_report_review": {**info, "state": "manual", "error": "ExecutionRecheckFailed"},
            "report_notice_dirty": True,
        }
        async with atomic(store):
            await self.jobs.restore(store, case, payload)
            if job:
                await self.jobs.save(
                    store, job, "manual", job["payload"], error="ExecutionRecheckFailed"
                )
        return False

    async def complete(self, store, case, job):
        if not case:
            return
        if case.get("reviewer_actor") != AI_ACTOR:
            await self.jobs.save(store, job, "skipped", job["payload"], error="HumanHandled")
            return
        if case["status"] == "executing":
            await self.service.execute_case(store, case)
            case = await store.case(case["id"])
        if case["status"] == "executing":
            await self.jobs.save(store, job, "executing", job["payload"], delay=30)
            return
        state = (
            "applied"
            if case["status"] in {"pending", "done"} and case["action"] != "report"
            else "manual"
        )
        await self.jobs.save(store, job, state, job["payload"], error=case.get("error"))
        await self.annotate(store, case, state, error=case.get("error"))
        await self.service.notifier.sync(
            store, await store.case(case["id"]), self.service.group(case["chat_id"])
        )

    async def start(self):
        self.worker = asyncio.create_task(self.run(), name="sakura-guard-ai-reports")

    async def stop(self):
        if self.worker:
            self.worker.cancel()
            try:
                await self.worker
            except asyncio.CancelledError:
                pass
            self.worker = None

    async def run(self):
        while True:
            self.wake_event.clear()
            try:
                await self.run_once()
            except Exception as error:
                logger.warning("Guard AI 队列读取失败 error=%s", type(error).__name__)
            try:
                await asyncio.wait_for(self.wake_event.wait(), timeout=5)
            except TimeoutError:
                pass
