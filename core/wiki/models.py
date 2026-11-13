# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot

"""Validated Wiki configuration and extraction contracts. Source text is always data."""

import hashlib
import json
import re
import unicodedata
from datetime import UTC, datetime
from typing import Literal
from zoneinfo import ZoneInfo

from pydantic import BaseModel, ConfigDict, Field, field_validator, model_validator


def digest(*parts) -> str:
    return hashlib.sha256(
        json.dumps(parts, ensure_ascii=False, sort_keys=True, default=str).encode()
    ).hexdigest()


def canonical_id(value) -> str:
    value = str(value).strip()
    if value.startswith("-100"):
        value = value[4:]
    if not re.fullmatch(r"[1-9][0-9]{0,18}", value):
        raise ValueError("Wiki channel identity must be a Telegram channel ID")
    return value


def alias_key(value) -> str:
    value = (
        str(value)
        .strip()
        .lower()
        .removeprefix("https://t.me/")
        .removeprefix("http://t.me/")
        .removeprefix("t.me/")
        .lstrip("@")
        .rstrip("/")
    )
    try:
        return canonical_id(value)
    except ValueError:
        if not re.fullmatch(r"[a-z][a-z0-9_]{3,63}", value):
            raise ValueError("Invalid channel alias") from None
        return value


def name_key(value) -> str:
    return " ".join(unicodedata.normalize("NFKC", value).casefold().split())


def utc(value=None):
    if value is None:
        return datetime.now(UTC).replace(tzinfo=None)
    if isinstance(value, str):
        value = datetime.fromisoformat(value.replace("Z", "+00:00"))
    return value.astimezone(UTC).replace(tzinfo=None) if value.tzinfo else value


class Strict(BaseModel):
    model_config = ConfigDict(extra="forbid")


class WikiConfig(Strict):
    enabled: bool = False
    public: bool = False
    paused: bool = False
    publication_mode: Literal["review", "auto"] = "review"
    fact_check: bool = False
    fact_check_required: bool = False
    proposals_enabled: bool = False
    annotations_enabled: bool = False
    proposal_notifications: bool = False
    frequency: Literal["manual", "daily", "weekly", "cron"] = "manual"
    cron: str = "0 3 * * *"
    timezone: str = "Asia/Shanghai"
    history_since: datetime | None = None
    include_summaries: bool = True
    include_images: bool = True
    message_types: list[Literal["text", "image", "other"]] = Field(
        default_factory=lambda: ["text", "image", "other"]
    )
    language: str = Field(default="zh-CN", min_length=2, max_length=32)
    columns: list[Literal["pages", "graph", "timeline", "profile"]] = Field(
        default_factory=lambda: ["pages", "graph", "timeline", "profile"]
    )
    show_quotes: bool = False
    show_telegram_links: bool = True
    public_base_url: str = Field(default="", max_length=2048)
    description: str = Field(default="", max_length=280)
    cover_url: str = Field(default="", max_length=2048)
    hidden_topics: list[str] = Field(default_factory=list, max_length=100)
    temperature: float = Field(default=0.2, ge=0, le=1)
    timeout_seconds: int = Field(default=180, ge=10, le=1800)
    retries: int = Field(default=3, ge=0, le=8)
    concurrency: int = Field(default=1, ge=1, le=4)
    token_budget: int = Field(default=10000000, ge=1000)
    max_output_tokens: int = Field(default=65536, ge=1024, le=262144)
    # 发布快照保留数量（含当前发布）：超出部分在发布成功后清理，回滚仍可用
    retention_publications: int = Field(default=20, ge=2, le=200)
    input_cost_per_million: float | None = Field(default=None, ge=0)
    output_cost_per_million: float | None = Field(default=None, ge=0)
    debounce_seconds: int = Field(default=120, ge=10, le=86400)

    @field_validator("timezone")
    @classmethod
    def valid_zone(cls, value):
        ZoneInfo(value)
        return value

    @field_validator("public_base_url")
    @classmethod
    def valid_public_url(cls, value):
        from urllib.parse import urlsplit

        if not value:
            return value
        parsed = urlsplit(value)
        if (
            parsed.scheme not in {"http", "https"}
            or not parsed.hostname
            or parsed.username
            or parsed.password
            or parsed.query
            or parsed.fragment
            or parsed.path not in {"", "/"}
        ):
            raise ValueError("Use the public WebUI origin without a path or credentials")
        return value.rstrip("/")

    @field_validator("cover_url")
    @classmethod
    def valid_cover_url(cls, value):
        from urllib.parse import urlsplit

        if value:
            parsed = urlsplit(value)
            if (
                parsed.scheme != "https"
                or not parsed.hostname
                or parsed.username
                or parsed.password
            ):
                raise ValueError("Use an HTTPS image URL without credentials")
        return value

    @field_validator("cron")
    @classmethod
    def valid_cron(cls, value):
        from apscheduler.triggers.cron import CronTrigger

        CronTrigger.from_crontab(value)
        return value

    @model_validator(mode="after")
    def public_requires_enabled(self):
        if self.public and not self.enabled:
            raise ValueError("Enable the Wiki before making it public")
        return self


class Citation(Strict):
    source_id: str = Field(pattern=r"^[a-f0-9]{64}$")
    chunk_id: str = Field(pattern=r"^[a-f0-9]{64}$")
    quote: str = Field(min_length=1, max_length=6000)
    start: int = Field(ge=0, strict=True)
    end: int = Field(gt=0, strict=True)


class Fact(Strict):
    entity: str = Field(min_length=1, max_length=160)
    kind: Literal["Topic", "Entity", "Event", "Claim", "FAQ", "Procedure"]
    aliases: list[str] = Field(default_factory=list, max_length=20)
    topic: str = Field(default="其他", min_length=1, max_length=160)
    key: str = Field(min_length=1, max_length=160)
    text: str = Field(min_length=1, max_length=16000)
    occurred_at: datetime | None = None
    valid_until: datetime | None = None
    status: Literal["current", "uncertain", "disputed", "superseded"] = "current"
    confidence: float = Field(ge=0, le=1, strict=True)
    evidence: list[Citation] = Field(min_length=1, max_length=20)


class Relation(Strict):
    source: str = Field(min_length=1, max_length=160)
    target: str = Field(min_length=1, max_length=160)
    kind: Literal[
        "belongs_to",
        "depends_on",
        "discusses",
        "solves",
        "replaces",
        "supports",
        "contradicts",
        "evolves",
    ]
    evidence: list[Citation] = Field(min_length=1, max_length=20)
    confidence: float = Field(ge=0, le=1)


class Extraction(Strict):
    facts: list[Fact] = Field(default_factory=list, max_length=80)
    relations: list[Relation] = Field(default_factory=list, max_length=80)


class ClaimReview(Strict):
    index: int = Field(ge=0, strict=True)
    status: Literal["supported", "unsupported", "sensitive"]
    reason: str = Field(min_length=1, max_length=500)
    fact_status: Literal[
        "verified", "partial", "disputed", "unverified", "outdated", "rejected"
    ] = "unverified"
    citations: list[int] = Field(default_factory=list, max_length=10)


class Review(Strict):
    approved: bool = Field(strict=True)
    reason: str = Field(min_length=1, max_length=500)
    claims: list[ClaimReview]


class ProfileNarrative(Strict):
    description: str = Field(max_length=2000)
    style: str = Field(max_length=1000)
    limitations: str = Field(max_length=1000)


def chunks(source_id, body, content_hash, size=5000, overlap=300):
    """Every code point is covered; offsets refer to the complete persisted source."""
    if not 0 <= overlap < size:
        raise ValueError("Invalid chunk overlap")
    start = 0
    while start < len(body):
        end = min(len(body), start + size)
        yield {
            "id": digest(source_id, content_hash, start, end),
            "source_id": source_id,
            "content_hash": content_hash,
            "start": start,
            "end": end,
            "text": body[start:end],
        }
        if end == len(body):
            break
        start = end - overlap


def validate_extraction(result: Extraction, chunk: dict):
    """JSON IDs are not trusted: require exact, bounded, current source quotations."""
    for item in [*result.facts, *result.relations]:
        for evidence in item.evidence:
            if (
                evidence.source_id != chunk["source_id"]
                or evidence.chunk_id != chunk["id"]
                or evidence.start < chunk["start"]
                or evidence.end > chunk["end"]
                or evidence.end <= evidence.start
                or chunk["text"][evidence.start - chunk["start"] : evidence.end - chunk["start"]]
                != evidence.quote
            ):
                raise ValueError("Citation does not match its source and offsets")
        # Named relation endpoints are resolved/created in the same transaction as
        # their edges; an entity need not have a separate assertion in this chunk.
    return result
