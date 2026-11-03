# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot

"""多服务档案校验，各处共用；异常和 repr 中不暴露凭据。"""

import hashlib
import json
import re
from typing import Literal
from urllib.parse import urlsplit

from pydantic import BaseModel, ConfigDict, Field, field_validator, model_validator

PURPOSES = ("summary", "assistant", "vision", "poll")


class StrictModel(BaseModel):
    model_config = ConfigDict(strict=True, extra="forbid")


class AIProfile(StrictModel):
    name: str = Field(min_length=1, max_length=100)
    provider: Literal["openai"] = "openai"
    base_url: str = Field(min_length=1, max_length=2048)
    api_key: str = Field(default="", repr=False, max_length=8192)
    model: str = Field(min_length=1, max_length=256)
    supports_vision: bool = False

    @field_validator("name", "base_url", "api_key", "model", mode="before")
    @classmethod
    def safe_string(cls, value):
        if not isinstance(value, str):
            raise ValueError("expected string")
        if any(ord(char) < 32 or ord(char) == 127 for char in value):
            raise ValueError("invalid control character")
        return value.strip()

    @field_validator("base_url")
    @classmethod
    def valid_url(cls, value):
        url = urlsplit(value)
        if (
            url.scheme not in {"http", "https"}
            or not url.hostname
            or url.username is not None
            or url.password is not None
            or any(char.isspace() for char in url.hostname)
            or url.query
            or url.fragment
        ):
            raise ValueError("invalid HTTP(S) URL")
        _ = url.port
        return value


class AIRoute(StrictModel):
    profile_id: str = ""
    model: str = Field(default="", max_length=256)
    supports_vision: bool | None = None

    @field_validator("model")
    @classmethod
    def safe_model(cls, value):
        return AIProfile.safe_string(value)


class VisionSettings(StrictModel):
    enabled: bool = False
    ocr: bool = True
    description: bool = True


class AIProfilesConfig(StrictModel):
    profiles: dict[str, AIProfile] = Field(default_factory=dict)
    routing: dict[str, AIRoute] = Field(default_factory=dict)
    vision: VisionSettings = Field(default_factory=VisionSettings)

    @model_validator(mode="after")
    def references(self):
        if len(self.profiles) > 100:
            raise ValueError("too many profiles")
        if any(not re.fullmatch(r"[a-zA-Z0-9_-]{1,64}", key) for key in self.profiles):
            raise ValueError("invalid profile ID")
        for purpose, route in self.routing.items():
            if purpose not in PURPOSES:
                raise ValueError("unknown purpose")
            if route.profile_id and route.profile_id not in self.profiles:
                raise ValueError("profile missing or still assigned")
        return self


def parse_ai_profiles(config: dict) -> AIProfilesConfig:
    try:
        return AIProfilesConfig.model_validate(
            {
                "profiles": config.get("ai_profiles", {}),
                "routing": config.get("ai_routing", {}),
                "vision": config.get("ai_vision", {}),
            }
        )
    except (ValueError, TypeError):
        # Pydantic 的错误 input 可能包含密钥，不允许向运行时调用方传播。
        raise ValueError("Invalid AI profiles or routing configuration") from None


def profiles_revision(config: dict) -> str:
    # 包含密钥的摘要只用作乐观锁；API 从不返回原始凭据。
    content = {key: config.get(key, {}) for key in ("ai_profiles", "ai_routing", "ai_vision")}
    return hashlib.sha256(json.dumps(content, sort_keys=True).encode()).hexdigest()
