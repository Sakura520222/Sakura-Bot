# Copyright 2026 Sakura-Bot
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
"""群组配置与输入校验；历史配置缺省时不启用任何处罚。"""

from typing import Literal

import regex
from pydantic import BaseModel, ConfigDict, Field, field_validator, model_validator

Action = Literal["delete", "warn", "mute", "kick", "ban"]
MemberAction = Literal["warn", "mute", "kick", "ban", "allow"]
NotificationEvent = Literal[
    "moderation",
    "verification_start",
    "verification_end",
    "verification_fail",
    "admin_action",
    "join_request",
    "whitelist",
    "blacklist",
]
DEFAULT_NOTIFICATION_EVENTS = [
    "moderation",
    "verification_start",
    "verification_end",
    "verification_fail",
    "admin_action",
    "join_request",
    "whitelist",
    "blacklist",
]
DEFAULT_WORDS = ["主页", "看我", "头像", "小学生"]


class GuardRule(BaseModel):
    model_config = ConfigDict(extra="forbid", strict=True)
    id: str = Field(min_length=1, max_length=64, pattern=r"^[a-zA-Z0-9_-]+$")
    target: Literal["nickname", "username", "message", "link"] = "message"
    kind: Literal["keyword", "regex"] = "keyword"
    value: str = Field(min_length=1, max_length=256)
    action: Action = "delete"

    @model_validator(mode="after")
    def validate_pattern(self):
        if self.kind == "regex":
            try:
                regex.compile(self.value, regex.IGNORECASE | regex.VERSION1)
            except regex.error as error:
                raise ValueError("invalid guard regex") from error
        elif not self.value.strip():
            raise ValueError("empty guard keyword")
        return self


class GuardGroup(BaseModel):
    model_config = ConfigDict(extra="forbid", strict=True)
    enabled: bool = False
    kick_button_enabled: bool = True
    verify_join: bool = True
    verification_timeout: int = Field(default=120, ge=30, le=600)
    verification_max_attempts: int = Field(default=3, ge=1, le=10)
    verification_timeout_action: MemberAction = "ban"
    verification_wrong_action: MemberAction = "ban"
    verification_message_mode: Literal["delete", "result"] = "delete"
    verification_admin_actions: list[Literal["ban", "blacklist", "skip"]] = Field(
        default_factory=lambda: ["ban", "blacklist", "skip"], max_length=3
    )
    join_requests: Literal["manual", "approve"] = "manual"
    join_request_match_action: Literal["reject", "manual", "approve"] = "reject"
    nickname_filter: bool = True
    nickname_action: Literal["warn", "mute", "kick", "ban"] = "ban"
    message_filter: bool = False
    block_bot_messages: bool = False
    bot_message_action: Action = "delete"
    nickname_keywords: list[str] = Field(
        default_factory=lambda: DEFAULT_WORDS.copy(), max_length=100
    )
    message_keywords: list[str] = Field(default_factory=list, max_length=100)
    message_action: Action = "delete"
    mute_seconds: int = Field(default=600, ge=30, le=86400 * 30)
    rules: list[GuardRule] = Field(default_factory=list, max_length=100)
    ban_any_traditional: bool = False
    share_global_blacklist: bool = False
    trusted_bot_ids: list[int] = Field(default_factory=list, max_length=100)
    reviewer_ids: list[int] = Field(default_factory=list, max_length=100)
    notification_ids: list[int] = Field(default_factory=list, max_length=100)
    notification_enabled: bool = True
    verification_notice_cleanup: bool = True
    notification_events: list[NotificationEvent] = Field(
        default_factory=lambda: DEFAULT_NOTIFICATION_EVENTS.copy(), max_length=8
    )
    review_chat_id: int | None = None
    allow_group_admin_review: bool = False

    @field_validator("nickname_keywords", "message_keywords")
    @classmethod
    def validate_words(cls, values):
        if any(not value.strip() or len(value) > 128 for value in values):
            raise ValueError("invalid guard keyword")
        return list(dict.fromkeys(values))

    @field_validator("trusted_bot_ids", "reviewer_ids", "notification_ids")
    @classmethod
    def validate_users(cls, values):
        if any(value <= 0 or value > 2**63 - 1 for value in values):
            raise ValueError("Telegram user IDs must be positive integers")
        return list(dict.fromkeys(values))

    @field_validator("verification_admin_actions", "notification_events")
    @classmethod
    def unique_options(cls, values):
        return list(dict.fromkeys(values))

    @field_validator("review_chat_id")
    @classmethod
    def validate_review_chat(cls, value):
        if value is not None and (value >= 0 or value < -(2**63) + 1):
            raise ValueError("review chat ID must be negative")
        return value

    @model_validator(mode="after")
    def unique_rules(self):
        if len({rule.id for rule in self.rules}) != len(self.rules):
            raise ValueError("duplicate guard rule ID")
        return self


class GuardConfig(BaseModel):
    model_config = ConfigDict(extra="forbid", strict=True)
    groups: dict[str, GuardGroup] = Field(default_factory=dict, max_length=500)
    retention_days: int = Field(default=90, ge=7, le=3650)

    @field_validator("groups")
    @classmethod
    def validate_groups(cls, groups):
        for key in groups:
            try:
                chat_id = int(key)
            except ValueError as error:
                raise ValueError("group keys must be Telegram chat IDs") from error
            if str(chat_id) != key or chat_id >= 0 or chat_id < -(2**63) + 1:
                raise ValueError("group keys must be canonical negative Telegram chat IDs")
        return groups
