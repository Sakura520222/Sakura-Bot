# Copyright 2026 Sakura-Bot
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
"""有界规则匹配与 Unicode、简繁归一化，不调用 LLM。"""

import logging
import time
import unicodedata
from dataclasses import asdict, dataclass
from functools import lru_cache
from urllib.parse import unquote

import regex
from opencc import OpenCC
from telethon.tl.types import MessageEntityTextUrl, MessageEntityUrl, MessageMediaContact

from .config import DEFAULT_WORDS, GuardGroup

logger = logging.getLogger(__name__)
_converter = OpenCC("t2s")


@lru_cache(maxsize=2048)
def normalize(text: str) -> str:
    text = unicodedata.normalize("NFKC", text).casefold()
    return _converter.convert(
        "".join(
            char
            for char in text
            if not char.isspace() and unicodedata.category(char) not in {"Cf", "Cc", "Mn", "Me"}
        )
    )


@lru_cache(maxsize=512)
def compiled(pattern: str):
    # 保留正则语法与空白语义；规则作用于已归一化的文本。
    return regex.compile(pattern, regex.IGNORECASE | regex.VERSION1)


def evidence_window(original: str, normalized: str, position: int):
    """围绕命中位置截取证据，避免长消息只保留未命中的开头。"""
    if len(original) <= 2048 and len(normalized) <= 2048:
        return original, normalized
    left, right = 0, len(original)
    while left < right:
        middle = (left + right) // 2
        if len(normalize(original[:middle])) < position:
            left = middle + 1
        else:
            right = middle
    raw_start, normalized_start = max(0, left - 256), max(0, position - 256)
    return original[raw_start : raw_start + 2048], normalized[
        normalized_start : normalized_start + 2048
    ]


@dataclass(frozen=True)
class Match:
    target: str
    rule_id: str
    reason: str
    original: str
    normalized: str
    action: str = "ban"

    def evidence(self):
        return asdict(self)


def extract_links(message) -> list[str]:
    text = getattr(message, "message", "") or ""
    links = regex.findall(r"(?:https?://|tg://|www\.)[^\s<>]+", text, flags=regex.I)
    # Telegram 的 offset/length 使用 UTF-16 code units。
    encoded = text.encode("utf-16-le")
    for entity in getattr(message, "entities", None) or []:
        if isinstance(entity, MessageEntityTextUrl):
            links.append(entity.url)
        elif isinstance(entity, MessageEntityUrl):
            links.append(
                encoded[entity.offset * 2 : (entity.offset + entity.length) * 2].decode(
                    "utf-16-le", errors="replace"
                )
            )
    return list(dict.fromkeys(unquote(link)[:8192] for link in links))[:100]


class Matcher:
    def __init__(self, config: GuardGroup, nickname_keywords: list[str] | None = None):
        self.config = config
        self.nickname_keywords = DEFAULT_WORDS if nickname_keywords is None else nickname_keywords
        self.regex_remaining = 0.025
        self._budget_logged = False

    def _match(self, target: str, original: str, words: list[str], action: str):
        original = original[:16384]
        normalized = normalize(original)
        for index, word in enumerate(words):
            needle = normalize(word)
            position = normalized.find(needle) if needle else -1
            if position >= 0:
                raw_evidence, normalized_evidence = evidence_window(original, normalized, position)
                return Match(
                    target,
                    f"{target}-word-{index}",
                    word,
                    raw_evidence,
                    normalized_evidence,
                    action,
                )
        for rule in self.config.rules:
            if rule.target != target:
                continue
            if rule.kind == "keyword":
                needle = normalize(rule.value)
                position = normalized.find(needle) if needle else -1
                matched = position >= 0
            else:
                if self.regex_remaining <= 0:
                    if not self._budget_logged:
                        logger.warning("Sakura Guard 正则预算已耗尽 target=%s", target)
                        self._budget_logged = True
                    continue
                started = time.perf_counter()
                try:
                    result = compiled(rule.value).search(
                        normalized, timeout=min(0.005, self.regex_remaining)
                    )
                    matched = result is not None
                    position = result.start() if result else -1
                except TimeoutError:
                    logger.warning("Sakura Guard 正则超时，已跳过规则 %s", rule.id)
                    continue
                finally:
                    self.regex_remaining -= time.perf_counter() - started
            if matched:
                raw_evidence, normalized_evidence = evidence_window(original, normalized, position)
                return Match(
                    target,
                    rule.id,
                    rule.value,
                    raw_evidence,
                    normalized_evidence,
                    self.config.nickname_action
                    if target in {"nickname", "username"} and rule.action == "delete"
                    else rule.action,
                )
        return None

    def nickname(self, user):
        if not self.config.nickname_filter:
            return None
        name = " ".join(
            value
            for value in (getattr(user, "first_name", None), getattr(user, "last_name", None))
            if value
        )
        username = getattr(user, "username", None) or ""
        for target, value in (("nickname", name), ("username", username)):
            matched = self._match(
                target, value, self.nickname_keywords, self.config.nickname_action
            )
            if matched:
                return matched
            if self.config.ban_any_traditional and _converter.convert(value) != value:
                return Match(
                    target,
                    "traditional",
                    "ban_any_traditional",
                    value[:2048],
                    normalize(value)[:2048],
                    self.config.nickname_action,
                )
        for alias in getattr(user, "usernames", None) or []:
            if getattr(alias, "active", False):
                matched = self._match(
                    "username", alias.username, self.nickname_keywords, self.config.nickname_action
                )
                if matched:
                    return matched
        return None

    def message(self, message):
        # 结构过滤独立于正文关键词开关；隐藏转发来源也保留 fwd_from。
        has_buttons = any(
            getattr(row, "buttons", None)
            for row in getattr(getattr(message, "reply_markup", None), "rows", None) or []
        )
        for blocked, target, action in (
            (
                self.config.block_contact_messages
                and isinstance(getattr(message, "media", None), MessageMediaContact),
                "contact_message",
                self.config.contact_message_action,
            ),
            (
                self.config.block_button_messages and has_buttons,
                "button_message",
                self.config.button_message_action,
            ),
            (
                self.config.block_forwarded_messages
                and getattr(message, "fwd_from", None) is not None,
                "forwarded_message",
                self.config.forwarded_message_action,
            ),
        ):
            if blocked:
                from core.i18n.i18n import t

                original = (getattr(message, "message", "") or "")[:2048]
                if target == "contact_message":
                    contact = message.media
                    name = " ".join(
                        value[:256] for value in (contact.first_name, contact.last_name) if value
                    )
                    original = t("guard.contact_message_evidence", name=name or "—")
                return Match(
                    target,
                    target.replace("_", "-"),
                    t(f"guard.{target}_reason"),
                    original,
                    normalize(original),
                    action,
                )
        if not self.config.message_filter:
            return None
        matched = self._match(
            "message",
            getattr(message, "message", "") or "",
            self.config.message_keywords,
            self.config.message_action,
        )
        if matched:
            return matched
        for link in extract_links(message):
            matched = self._match(
                "link", link, self.config.message_keywords, self.config.message_action
            )
            if matched:
                return matched
        return None
