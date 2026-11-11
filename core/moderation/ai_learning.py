# Copyright 2026 Sakura-Bot
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
"""AI 自适应风控：样本收集、异步学习、候选规则与实时 Matcher 缓存。

AI 只负责分析样本并生成候选规则；实时消息检测始终由本地确定性 Matcher
执行，任何学习/数据库异常都不能影响原有手工群管规则。
"""

import asyncio
import hashlib
import logging
import re
import time
import uuid
from datetime import timedelta

from telethon import events

from core.i18n.i18n import t
from core.infrastructure.database.mysql import utc_now_naive

from .ai_learning_repository import AILearningJobs as LearningJobStore
from .ai_learning_repository import (
    AIRuleFeedback,
    AIRules,
    AISamples,
)
from .ai_reports import message_snapshot
from .ai_rule_extractor import IncompleteAIResponse, extract, sanitize_candidates
from .matcher import Match, compiled, evidence_window, extract_links, normalize
from .moderator import GuardError
from .repository import GuardStore
from .telegram import call

logger = logging.getLogger(__name__)
AI_ACTOR = "system:ai_learning"
ADMIN_REPORT_PATTERN = re.compile(r"\s*@admin\s+上报(?:\s+(.*?))?\s*", re.IGNORECASE | re.DOTALL)
# 举报 AI 复核队列沿用 3 次重试；学习任务允许更多次暂缓后转人工。
MAX_JOB_ATTEMPTS = 5
CACHE_TTL_SECONDS = 30
MAINTENANCE_INTERVAL = timedelta(minutes=10)


def sample_key(chat_id, message_id):
    return hashlib.sha256(f"ai-sample:{chat_id}:{message_id}".encode()).hexdigest()


def rule_key(chat_id, rule_type, match_kind, normalized_value):
    return hashlib.sha256(
        f"{chat_id}:{rule_type}:{match_kind}:{normalized_value}".encode()
    ).hexdigest()


def nickname_text(user):
    return " ".join(
        value
        for value in (getattr(user, "first_name", None), getattr(user, "last_name", None))
        if value
    )


def usernames(user):
    return list(
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
    )


class AIRuleCache:
    """已激活 AI 规则的进程内缓存；整体替换实现原子切换，命中路径不查库。"""

    def __init__(self):
        self.entries: dict[int, list[dict]] = {}
        self.loaded_at = 0.0
        self.dirty = False
        self.shared_enabled = False

    def invalidate(self):
        self.dirty = True

    async def refresh(self, rules, *, force=False, shared_enabled=False):
        now = time.monotonic()
        if not force and not self.dirty and now - self.loaded_at < CACHE_TTL_SECONDS:
            return
        rows = await rules.active_rules()
        entries: dict[int, list[dict]] = {}
        for row in rows:
            entry = {
                "id": row["id"],
                "version": row["version"],
                "rule_type": row["rule_type"],
                "match_kind": row["match_kind"],
                "needle": normalize(row["value"]),
                "value": row["value"],
                "action": row["action"],
            }
            if entry["match_kind"] == "regex":
                try:
                    entry["pattern"] = compiled(row["value"])
                except Exception:
                    logger.warning("Guard AI 规则正则编译失败，已跳过 rule=%s", row["id"])
                    continue
            # 共享作用域仅在全局显式开启时生效；本群规则始终加载。
            for chat_id in dict.fromkeys(
                [row["chat_id"], *(row.get("shared_chats", []) if shared_enabled else [])]
            ):
                entries.setdefault(chat_id, []).append(entry)
        # 先构建完成再整体替换，并发消息不会看到半更新规则集。
        self.entries = entries
        self.loaded_at = now
        self.dirty = False
        self.shared_enabled = shared_enabled

    def match(self, chat_id, user, message):
        entries = self.entries.get(chat_id) or ()
        if not entries:
            return None
        texts: list[tuple[str, str]] = []
        if user is not None:
            texts.append(("nickname", nickname_text(user)[:2048]))
            for username in usernames(user):
                texts.append(("username", username))
        if message is not None:
            text = (getattr(message, "message", "") or "")[:16384]
            texts.append(("message", text))
            for link in extract_links(message):
                texts.append(("link", link))
        for entry in entries:
            for target, original in texts:
                if entry["rule_type"] != target and entry["rule_type"] != "pattern":
                    continue
                if entry["rule_type"] == "pattern" and target not in {"message", "link"}:
                    continue
                normalized = normalize(original)
                position = -1
                if entry["match_kind"] == "regex":
                    try:
                        result = entry["pattern"].search(normalized, timeout=0.01)
                        if result:
                            position = result.start()
                    except TimeoutError:
                        continue
                elif not entry["needle"]:
                    continue
                else:
                    position = normalized.find(entry["needle"])
                if position < 0:
                    continue
                raw_evidence, normalized_evidence = evidence_window(original, normalized, position)
                return Match(
                    f"ai_{entry['rule_type']}",
                    f"ai-{entry['id'][:12]}",
                    entry["value"],
                    raw_evidence,
                    normalized_evidence,
                    entry["action"],
                    ai_rule={"id": entry["id"], "version": entry["version"]},
                )
        return None


class AILearning:
    def __init__(self, service):
        self.service = service
        self.samples = AISamples(service.repository)
        self.jobs = LearningJobStore(service.repository)
        self.rules = AIRules(service.repository)
        self.feedback = AIRuleFeedback(service.repository)
        self.extractor = extract
        self.cache = AIRuleCache()
        self.worker = None
        self.wake_event = asyncio.Event()
        self._maintenance_at = utc_now_naive()

    # ── 配置与开关 ──
    def settings(self):
        return self.service.config.ai_learning

    def enabled(self, chat_id):
        group = self.service.group(chat_id)
        return self.settings().enabled and group.enabled and group.ai_learning_enabled

    def source_allowed(self, source):
        return source in self.settings().sources

    def type_allowed(self, rule_type):
        targets = set(self.settings().targets)
        if rule_type in {"nickname", "username", "link"}:
            return rule_type in targets
        return "message" in targets or "caption" in targets

    def wake(self):
        self.wake_event.set()
        self.cache.invalidate()

    # ── 管理员 @admin 上报 ──
    async def handle_report(self, event):
        text = getattr(getattr(event, "message", None), "message", "") or ""
        match = ADMIN_REPORT_PATTERN.fullmatch(text)
        if (
            not match
            or not event.is_group
            or not event.sender_id
            or event.sender_id <= 0
            or event.sender_id == self.service.bot_id
            or getattr(event.message, "edit_date", None)
        ):
            return
        try:
            reporter = await call(self.service.client.get_entity(event.sender_id))
            if getattr(reporter, "bot", False):
                return
            # 仅认系统管理员、本群审核员或显式授权的群管理员；与 Bot 自身权限无关。
            if not await self.service.authorized(event.sender_id, event.chat_id):
                raise GuardError("error.permission_denied")
            if not self.enabled(event.chat_id):
                raise GuardError("guard.ai_learning_disabled")
            if not getattr(event.message, "reply_to_msg_id", None):
                raise GuardError("guard.ai_report_usage")
            original = await call(event.get_reply_message())
            target_id = getattr(original, "sender_id", None)
            if (
                original is None
                or getattr(original, "chat_id", None) != event.chat_id
                or not isinstance(target_id, int)
                or target_id <= 0
                or target_id in {reporter.id, self.service.bot_id}
                or getattr(original, "action", None)
            ):
                raise GuardError("guard.ai_report_invalid_target")
            user = await call(self.service.client.get_entity(target_id))
            sample, created = await self.submit_admin_report(
                event.chat_id, reporter, original, user, (match[1] or "").strip()[:256]
            )
            key = "guard.ai_report_submitted" if created else "guard.ai_report_duplicate"
            await event.reply(t(key, sample_id=sample["id"][:12]), parse_mode=None)
        except GuardError as error:
            await event.reply(t(str(error)), parse_mode=None)
        except Exception:
            logger.exception(
                "Guard AI 上报处理失败 chat=%s reporter=%s", event.chat_id, event.sender_id
            )
            await event.reply(t("guard.ai_report_failed"), parse_mode=None)
        raise events.StopPropagation

    async def submit_admin_report(self, chat_id, reporter, original, user, note):
        """上报只创建样本与异步任务；不处罚目标、不直接生成生效规则。"""
        if not self.source_allowed("admin_report"):
            raise GuardError("guard.ai_learning_disabled")
        entry = {
            "user_id": reporter.id,
            "name": nickname_text(reporter)[:256],
            "username": getattr(reporter, "username", None),
            "at": utc_now_naive().isoformat(),
            "note": note,
        }
        async with self.service.repository.lock(user.id) as store:
            existing = await self.samples.by_key(store, sample_key(chat_id, original.id))
            if existing:
                reporters = existing["payload"].setdefault("reporters", [])
                if not any(item.get("user_id") == reporter.id for item in reporters):
                    reporters.append(entry)
                    await self.samples.save(store, existing)
                return existing, False
            case = await store.case_for_event(
                hashlib.sha256(f"report:{chat_id}:{original.id}".encode()).hexdigest()
            )
            payload = self._evidence_payload(chat_id, user, original)
            payload["reporters"] = [entry]
            sample = {
                "id": uuid.uuid4().hex,
                "idempotency_key": sample_key(chat_id, original.id),
                "chat_id": chat_id,
                "user_id": user.id,
                "message_id": original.id,
                "source": "admin_report",
                "case_id": case["id"] if case else None,
                "label": "pending",
                "status": "new",
                "payload": payload,
            }
            row, created = await self.samples.create(store, sample)
            if created:
                await self.feedback.record(
                    store,
                    rule_id=None,
                    sample_id=row["id"],
                    case_id=case["id"] if case else None,
                    chat_id=chat_id,
                    user_id=user.id,
                    kind="admin_report",
                    polarity="neutral",
                    actor=f"telegram:{reporter.id}",
                    payload={"note": note},
                )
                await self.samples.save(store, row, status="queued")
                await self.jobs.enqueue(store, row)
                self.wake_event.set()
            return row, created

    def _evidence_payload(self, chat_id, user, original):
        return {
            "nickname": nickname_text(user)[:256],
            "usernames": usernames(user)[:20],
            "user_id": user.id,
            "message": message_snapshot(original),
            "submitted_at": utc_now_naive().isoformat(),
        }

    # ── 成员举报与审核结果接入 ──
    async def record_member_report(self, store, case, reporter=None):
        """普通成员举报仅作为待验证样本；与管理员确认样本权重不同。

        同一原消息的重复举报不新建样本，但累计不同举报人，
        避免案件去重丢失有效反馈。
        """
        if not self.enabled(case["chat_id"]) or not self.source_allowed("member_report"):
            return
        try:
            message_id = case["payload"].get("message_id")
            if not isinstance(message_id, int):
                return
            existing = await self.samples.by_key(store, sample_key(case["chat_id"], message_id))
            if existing:
                payload_changed = False
                if reporter is not None:
                    reporters = existing["payload"].setdefault("reporters", [])
                    if not any(item.get("user_id") == reporter.id for item in reporters):
                        reporters.append(
                            {
                                "user_id": reporter.id,
                                "name": nickname_text(reporter)[:256],
                                "username": getattr(reporter, "username", None),
                                "at": utc_now_naive().isoformat(),
                                "note": "",
                            }
                        )
                        payload_changed = True
                case_id = case["id"] if not existing.get("case_id") else None
                if payload_changed or case_id is not None:
                    await self.samples.save(
                        store,
                        {**existing, "case_id": case_id or existing.get("case_id")},
                    )
                return
            reporter = case["payload"].get("reporter") or {}
            payload = {
                "nickname": (case["payload"].get("name") or "")[:256],
                "usernames": [value for value in [case["payload"].get("username")] if value],
                "user_id": case["user_id"],
                "message": case["payload"].get("reported_message") or {},
                "reporters": [
                    {
                        "user_id": reporter.get("user_id"),
                        "name": reporter.get("name", ""),
                        "username": reporter.get("username"),
                        "at": case["created_at"].isoformat() if case.get("created_at") else "",
                        "note": case["payload"].get("evidence", {}).get("reason", ""),
                    }
                ],
                "submitted_at": case["created_at"].isoformat() if case.get("created_at") else "",
            }
            sample = {
                "id": uuid.uuid4().hex,
                "idempotency_key": sample_key(case["chat_id"], message_id),
                "chat_id": case["chat_id"],
                "user_id": case["user_id"],
                "message_id": message_id,
                "source": "member_report",
                "case_id": case["id"],
                "label": "pending",
                "status": "new",
                "payload": payload,
            }
            row, created = await self.samples.create(store, sample)
            if created:
                await self.samples.save(store, row, status="queued")
                await self.jobs.enqueue(store, row)
                self.wake_event.set()
        except Exception:
            logger.warning("Guard AI 学习样本写入失败 case=%s", case.get("id"), exc_info=True)

    async def record_review_outcome(self, store, case, result, action=None):
        """将管理员最终成功操作结果写入样本反馈；失败/超时不算确认违规。"""
        if not self.settings().enabled or not self.source_allowed("review_outcome"):
            return
        try:
            if action is None:
                history = (result["payload"].get("review_history") or [-1, {}])[-1]
                action = history.get("action")
            if case["action"] == "report":
                # 用户举报案件：驳回=负样本；确认封禁/拉黑/踢出=正样本；仅删除=弱反馈。
                polarity = {
                    "blacklist": "positive",
                    "ban": "positive",
                    "kick": "positive",
                    "allow": "negative",
                    "unban": "negative",
                    "delete": "neutral",
                }.get(action)
            else:
                # 自动处罚案件：仅人工纠错（解封/放行）构成负反馈。
                polarity = "negative" if action in {"allow", "unban"} else None
            if polarity is None:
                return
            actor = result.get("reviewer_actor") or f"telegram:{result.get('reviewer_id')}"
            if case["action"] == "report":
                sample = await self.samples.by_case(store, case["id"])
                if sample is None:
                    message_id = case["payload"].get("message_id")
                    if isinstance(message_id, int):
                        sample = await self.samples.by_key(
                            store, sample_key(case["chat_id"], message_id)
                        )
                if sample is None:
                    return
                await self.samples.save(store, sample, label=polarity)
                await self.feedback.record(
                    store,
                    rule_id=None,
                    sample_id=sample["id"],
                    case_id=case["id"],
                    chat_id=case["chat_id"],
                    user_id=case["user_id"],
                    kind=f"review_{action}",
                    polarity=polarity,
                    actor=actor,
                    payload={"status": result["status"]},
                )
                await self.refresh_rules_for_sample(store, sample["id"])
                if polarity == "negative":
                    await self.correct_rules_for_user(store, case, actor, kind="review_correction")
            elif polarity == "negative":
                # 自动处罚被人工纠正：命中该用户的 AI 规则记误判并复核。
                await self.correct_rules_for_user(store, case, actor)
        except Exception:
            logger.warning("Guard AI 审核反馈写入失败 case=%s", case.get("id"), exc_info=True)

    async def record_ai_review(self, store, case):
        """举报超时 AI 复核仅在最终确认且处罚成功后记为正样本。"""
        if not self.enabled(case["chat_id"]) or not self.source_allowed("ai_review"):
            return
        try:
            sample = await self.samples.by_case(store, case["id"])
            if sample is None:
                return
            await self.samples.save(store, sample, label="positive")
            await self.feedback.record(
                store,
                rule_id=None,
                sample_id=sample["id"],
                case_id=case["id"],
                chat_id=case["chat_id"],
                user_id=case["user_id"],
                kind="ai_review",
                polarity="positive",
                actor=AI_ACTOR,
                payload={"status": case["status"]},
            )
            await self.refresh_rules_for_sample(store, sample["id"])
        except Exception:
            logger.warning("Guard AI 复核样本回写失败 case=%s", case.get("id"), exc_info=True)

    async def record_whitelist(self, store, chat_id, user_id, actor):
        """加入白名单是对相关规则的负反馈/误判信号。"""
        if not self.settings().enabled:
            return
        try:
            samples = await self.samples.for_user_chat(store, chat_id, user_id, limit=20)
            for sample in samples:
                if sample["label"] == "positive":
                    await self.samples.save(store, sample, label="negative")
                    await self.feedback.record(
                        store,
                        rule_id=None,
                        sample_id=sample["id"],
                        case_id=sample.get("case_id"),
                        chat_id=chat_id,
                        user_id=user_id,
                        kind="whitelist",
                        polarity="negative",
                        actor=str(actor),
                        payload={},
                    )
                    await self.refresh_rules_for_sample(store, sample["id"])
            await self.correct_rules_for_user(
                store,
                {"chat_id": chat_id, "user_id": user_id, "id": None},
                str(actor),
                kind="whitelist",
            )
        except Exception:
            logger.warning(
                "Guard AI 白名单反馈写入失败 chat=%s user=%s", chat_id, user_id, exc_info=True
            )

    async def refresh_rules_for_sample(self, store, sample_id):
        for rule in await self.rules.for_sample(store, sample_id):
            samples = await self.samples.by_ids(store, rule["sample_ids"])
            await self.rules.refresh_stats(store, rule, samples)
            await self._apply_feedback_policy(store, rule)

    async def correct_rules_for_user(self, store, case, actor, *, kind="hit_correction"):
        """对最近命中该用户的 AI 规则记录误判，达到阈值后自动暂停。"""
        hits = await self.rules.hits_for(store, case["chat_id"], case["user_id"], limit=20)
        corrected = set()
        for hit in hits:
            if hit["rule_id"] in corrected:
                continue
            corrected.add(hit["rule_id"])
            rule = await self.rules.get(store, hit["rule_id"])
            if not rule or rule["status"] not in {"active", "candidate"}:
                continue
            await self.rules.add_correction(store, rule)
            await self.feedback.record(
                store,
                rule_id=rule["id"],
                sample_id=None,
                case_id=case.get("id"),
                chat_id=case["chat_id"],
                user_id=case["user_id"],
                kind=kind,
                polarity="negative",
                actor=actor,
                payload={"status": rule["status"]},
            )
            await self._apply_feedback_policy(store, rule)

    async def _apply_feedback_policy(self, store, rule):
        settings = self.settings()
        rule = await self.rules.get(store, rule["id"])
        if rule["status"] != "active":
            return
        if rule["negatives"] >= settings.negative_pause_threshold or (
            rule["corrections"] >= settings.negative_pause_threshold
        ):
            await self.transition_rule(
                store,
                rule,
                "disabled",
                actor="system:negative_threshold",
                error="negative_feedback_threshold",
                note={
                    "negatives": rule["negatives"],
                    "corrections": rule["corrections"],
                },
            )

    async def transition_rule(
        self,
        store,
        rule,
        status,
        *,
        actor,
        error=None,
        activate=False,
        bump_version=True,
        note=None,
    ):
        """唯一的规则状态流转入口：每次流转都留下可追溯审计。"""
        await self.rules.set_status(
            store,
            rule,
            status,
            actor=actor,
            error=error,
            activate=activate,
            bump_version=bump_version,
            expires_days=self.settings().rule_expiry_days,
        )
        await self.feedback.record(
            store,
            rule_id=rule["id"],
            sample_id=None,
            case_id=None,
            chat_id=rule["chat_id"],
            user_id=0,
            kind="lifecycle",
            polarity="neutral",
            actor=actor,
            payload={"from": rule["status"], "to": status, "error": error, **(note or {})},
        )
        self.cache.invalidate()

    async def set_rule_action(self, store, rule, action, actor):
        """管理员手动调整命中动作（封禁等高影响动作由此显式启用）。"""
        if action not in {"delete", "warn", "mute", "kick", "ban"}:
            raise GuardError("guard.invalid_callback")
        updated = await self.rules.set_action(store, rule, action)
        await self.feedback.record(
            store,
            rule_id=rule["id"],
            sample_id=None,
            case_id=None,
            chat_id=rule["chat_id"],
            user_id=0,
            kind="action_change",
            polarity="neutral",
            actor=str(actor),
            payload={"from": rule["action"], "to": action},
        )
        self.cache.invalidate()
        return updated

    async def set_rule_shares(self, store, rule, chat_ids, actor):
        """显式授权的跨群共享作用域；全局关闭时拒绝设置。"""
        settings = self.settings()
        if not settings.shared_rules_enabled:
            raise GuardError("guard.ai_learning_sharing_disabled")
        wanted = sorted({int(chat_id) for chat_id in chat_ids if -(2**63) < int(chat_id) < 0})
        if not all(str(chat_id).startswith("-") for chat_id in wanted):
            raise GuardError("guard.invalid_group")
        await self.rules.set_scopes(store, rule, wanted, actor)
        await self.feedback.record(
            store,
            rule_id=rule["id"],
            sample_id=None,
            case_id=None,
            chat_id=rule["chat_id"],
            user_id=0,
            kind="scope_change",
            polarity="neutral",
            actor=str(actor),
            payload={"shared_chats": wanted},
        )
        self.cache.invalidate()
        return wanted

    async def record_appeal_allowed(self, store, case, actor):
        """申诉成功是明确的误判信号：翻转正样本并纠正相关规则。"""
        if not self.settings().enabled:
            return
        try:
            await self.record_whitelist(store, case["chat_id"], case["user_id"], actor)
            await self.feedback.record(
                store,
                rule_id=None,
                sample_id=None,
                case_id=case["id"],
                chat_id=case["chat_id"],
                user_id=case["user_id"],
                kind="appeal_allowed",
                polarity="negative",
                actor=str(actor),
                payload={},
            )
        except Exception:
            logger.warning("Guard AI 申诉反馈写入失败 case=%s", case.get("id"), exc_info=True)

    async def report_context(self, store, case, config):
        """举报 AI 复核的相似证据：同群相似样本与该用户的重复命中行为。

        以案件创建时间为上界，复核期间新到达的样本不会改变上下文指纹；
        学习关闭时返回空段，不影响原有复核语义。
        """
        if not self.settings().enabled:
            return {"similar_samples": [], "recent_rule_hits": []}
        before = case["created_at"]
        samples = await self.samples.similar(
            store,
            case["chat_id"],
            before=before,
            window_days=config.ai_report_review_history_window_days,
            limit=config.ai_report_review_history_limit,
        )
        hits = [
            hit
            for hit in await self.rules.hits_for(
                store, case["chat_id"], case["user_id"], limit=config.ai_report_review_history_limit
            )
            if hit["created_at"] <= before
        ]
        return {
            "similar_samples": [
                {
                    "id": row["id"],
                    "source": row["source"],
                    "label": row["label"],
                    "nickname": (row["payload"] or {}).get("nickname", ""),
                    "usernames": (row["payload"] or {}).get("usernames", []),
                    "message": (row["payload"] or {}).get("message") or {},
                    "reporters": len((row["payload"] or {}).get("reporters", [])),
                }
                for row in samples
                if row.get("case_id") != case["id"]
            ][: max(1, config.ai_report_review_history_limit)],
            "recent_rule_hits": [
                {
                    "rule_id": hit["rule_id"],
                    "version": hit["version"],
                    "action": hit["action"],
                    "created_at": hit["created_at"].isoformat(),
                }
                for hit in hits
            ][: max(1, config.ai_report_review_history_limit)],
        }

    # ── 学习任务 worker ──
    async def run_once(self):
        for candidate in await self.jobs.due():
            try:
                await self.process(candidate)
            except asyncio.CancelledError:
                raise
            except Exception as error:
                logger.warning(
                    "Guard AI 学习任务暂缓 sample=%s error=%s",
                    candidate["sample_id"],
                    type(error).__name__,
                )

    async def process(self, candidate):
        service = self.service
        async with service.repository.lock(candidate["user_id"]) as store:
            job = await self.jobs.get(store, candidate["sample_id"])
            if not job or job["token"] != candidate["token"]:
                return
            # 租约需覆盖最长 180s 的模型调用，避免在途任务被误判失联而重复抢占。
            job = await self.jobs.claim(store, candidate, lease_minutes=6)
            if not job:
                return
            sample = await self.samples.get(store, candidate["sample_id"])
            if not sample or sample["label"] == "negative":
                if sample:
                    await self.samples.save(store, sample, status="skipped")
                await self.jobs.save(store, job, "skipped", job["payload"], error="NegativeSample")
                return
            if not self.enabled(sample["chat_id"]) or not self.source_allowed(sample["source"]):
                await self.jobs.save(store, job, "skipped", job["payload"], error="Disabled")
                return
            if job["attempts"] > MAX_JOB_ATTEMPTS:
                await self.jobs.save(
                    store, job, "manual", job["payload"], error="RetryLimitReached"
                )
                await self.samples.save(store, sample, status="new", error="RetryLimitReached")
                return
            context = await self._context(store, sample)
            await self.jobs.save(store, job, "running", job["payload"], delay=180)

        # 网络模型调用期间不持有数据库连接或用户锁。
        try:
            result = await self.extractor(context)
            failure = None
        except IncompleteAIResponse as error:
            # 自有校验错误的消息是安全诊断信息（finish 原因），进任务错误列。
            failure = f"{type(error).__name__}:{str(error)[:64]}"
            result = {}
        except Exception as error:
            failure = type(error).__name__
            result = {}

        async with service.repository.lock(candidate["user_id"]) as store:
            current = await self.jobs.get(store, candidate["sample_id"])
            if not current or current["token"] != job["token"] or current["status"] != "running":
                return
            if failure:
                if job["attempts"] >= MAX_JOB_ATTEMPTS:
                    await self.jobs.save(
                        store, job, "manual", job["payload"], delay=0, error=failure
                    )
                    await self.samples.save(store, sample, status="new", error=failure)
                else:
                    await self.jobs.save(
                        store, job, "queued", job["payload"], delay=60, error=failure
                    )
                return
            activated = False
            for item in sanitize_candidates(result, context):
                if not self.type_allowed(item["rule_type"]):
                    continue
                rule = {
                    "id": uuid.uuid4().hex,
                    "rule_key": rule_key(
                        sample["chat_id"],
                        item["rule_type"],
                        item["match_kind"],
                        item["normalized_value"],
                    ),
                    "chat_id": sample["chat_id"],
                    "rule_type": item["rule_type"],
                    "match_kind": item["match_kind"],
                    "value": item["value"],
                    "normalized_value": item["normalized_value"],
                    "action": self.settings().default_action,
                    "status": "candidate",
                    "confidence": item["confidence"],
                    "model": result.get("model"),
                    "explanation": item["explanation"],
                    "sample_ids": list(dict.fromkeys([sample["id"], *item["evidence_sample_ids"]])),
                }
                row = await self.rules.upsert(store, rule)
                linked = await self.samples.by_ids(store, row["sample_ids"])
                await self.rules.refresh_stats(store, row, linked)
                if await self._maybe_activate(store, row):
                    activated = True
            await self.samples.save(store, sample, status="learned", error=None)
            await self.jobs.save(
                store,
                job,
                "done",
                {
                    **job["payload"],
                    "result": {
                        "model": result.get("model"),
                        "rules": len(result.get("candidates", [])),
                    },
                },
            )
            if activated:
                self.cache.invalidate()

    async def _maybe_activate(self, store, rule):
        """达到配置门槛才自动激活；默认策略只生成候选等待管理员确认。"""
        settings = self.settings()
        rule = await self.rules.get(store, rule["id"])
        if rule["status"] != "candidate" or settings.activation != "auto":
            return False
        if (
            rule["positives"] >= settings.min_positive_samples
            and rule["reporters"] >= settings.min_reporters
            and rule["target_users"] >= settings.min_target_users
            and rule["confidence"] >= settings.min_confidence
        ):
            await self.transition_rule(
                store,
                rule,
                "active",
                actor="system:auto_threshold",
                activate=True,
                bump_version=False,
                note={
                    "positives": rule["positives"],
                    "reporters": rule["reporters"],
                    "target_users": rule["target_users"],
                    "confidence": rule["confidence"],
                },
            )
            return True
        return False

    async def _context(self, store, sample):
        settings = self.settings()
        similar = await self.samples.similar(
            store,
            sample["chat_id"],
            before=utc_now_naive(),
            window_days=settings.analysis_window_days,
            limit=settings.history_sample_limit,
        )
        rows = [sample] + [row for row in similar if row["id"] != sample["id"]]
        rules = await self.rules.for_chat(store, sample["chat_id"])
        return {
            "targets": settings.targets,
            "samples": [
                {
                    "id": row["id"],
                    "source": row["source"],
                    "label": row["label"],
                    "user_id": row["user_id"],
                    "nickname": (row["payload"] or {}).get("nickname", ""),
                    "usernames": (row["payload"] or {}).get("usernames", []),
                    "message": (row["payload"] or {}).get("message") or {},
                    "reporters": len((row["payload"] or {}).get("reporters", [])),
                }
                for row in rows
                if row["label"] != "negative"
            ][: max(1, settings.history_sample_limit)],
            "existing_rules": [
                {
                    "rule_type": row["rule_type"],
                    "match_kind": row["match_kind"],
                    "value": row["value"],
                    "status": row["status"],
                }
                for row in rules
            ],
        }

    # ── 实时命中与记录 ──
    def match_rule(self, config, chat_id, user, message):
        if not config.ai_learning_enabled or not self.settings().enabled:
            return None
        try:
            return self.cache.match(chat_id, user, message)
        except Exception:
            logger.warning("Guard AI 规则匹配异常 chat=%s", chat_id, exc_info=True)
            return None

    async def record_hit(self, store, chat_id, user_id, match, case_id):
        """处罚意图落库后记录命中审计；失败不影响处罚本身。"""
        try:
            info = match.ai_rule or {}
            rule = await self.rules.get(store, info.get("id"))
            if not rule:
                return
            await self.rules.record_hit(
                store, rule, chat_id, user_id, case_id, match.action, match.evidence()
            )
        except Exception:
            logger.warning(
                "Guard AI 规则命中记录失败 chat=%s user=%s", chat_id, user_id, exc_info=True
            )

    # ── 生命周期 ──
    async def tick(self):
        """由主恢复循环驱动：刷新缓存并低频执行规则过期与留存清理。"""
        if not self.settings().enabled:
            self.cache.entries = {}
            self.cache.loaded_at = time.monotonic()
            return
        try:
            await self.cache.refresh(
                self.rules, shared_enabled=self.settings().shared_rules_enabled
            )
        except Exception as error:
            logger.warning("Guard AI 规则缓存刷新失败 error=%s", type(error).__name__)
        if utc_now_naive() >= self._maintenance_at:
            self._maintenance_at = utc_now_naive() + MAINTENANCE_INTERVAL
            try:
                # 逐条过期并留审计；单条失败不影响其余规则。
                pool = getattr(self.service.repository, "pool", None)
                for rule in await self.rules.expiring():
                    if pool is None:
                        break
                    try:
                        async with pool.acquire() as connection:
                            await self.transition_rule(
                                GuardStore(connection), rule, "expired", actor="system:expiry"
                            )
                    except Exception:
                        logger.warning("Guard AI 规则过期失败 rule=%s", rule["id"])
            except Exception as error:
                logger.warning("Guard AI 规则过期查询失败 error=%s", type(error).__name__)
            try:
                await self.rules.cleanup_samples(days=self.settings().sample_retention_days)
                await self.rules.cleanup_hits(days=self.settings().sample_retention_days)
            except Exception as error:
                logger.warning("Guard AI 样本留存清理失败 error=%s", type(error).__name__)

    async def start(self):
        self.worker = asyncio.create_task(self._run(), name="sakura-guard-ai-learning")

    async def stop(self):
        if self.worker:
            self.worker.cancel()
            try:
                await self.worker
            except asyncio.CancelledError:
                pass
            self.worker = None

    async def _run(self):
        while True:
            self.wake_event.clear()
            try:
                await self.run_once()
            except Exception as error:
                logger.warning("Guard AI 学习队列读取失败 error=%s", type(error).__name__)
            try:
                await asyncio.wait_for(self.wake_event.wait(), timeout=5)
            except TimeoutError:
                pass
