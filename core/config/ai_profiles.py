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
    provider_id: str = ""
    model_id: str = Field(default="", max_length=256)
    supports_vision: bool | None = None

    @field_validator("model", "model_id")
    @classmethod
    def safe_model(cls, value):
        return AIProfile.safe_string(value)


class VisionSettings(StrictModel):
    enabled: bool = False
    ocr: bool = True
    description: bool = True


class AIModel(StrictModel):
    name: str = Field(default="", max_length=100)
    supports_vision: bool = False

    @field_validator("name", mode="before")
    @classmethod
    def safe_name(cls, value):
        return AIProfile.safe_string(value)


class AIProvider(StrictModel):
    name: str = Field(min_length=1, max_length=100)
    provider: Literal["openai"] = "openai"
    base_url: str = Field(min_length=1, max_length=2048)
    api_key: str = Field(default="", repr=False, max_length=8192)
    models: dict[str, AIModel] = Field(default_factory=dict)

    @field_validator("name", "base_url", "api_key", mode="before")
    @classmethod
    def safe_string(cls, value):
        return AIProfile.safe_string(value)

    @field_validator("base_url")
    @classmethod
    def valid_url(cls, value):
        return AIProfile.valid_url(value)

    @field_validator("models")
    @classmethod
    def valid_models(cls, value):
        if len(value) > 500:
            raise ValueError("too many models")
        for model_id in value:
            if not model_id or len(model_id) > 256 or AIProfile.safe_string(model_id) != model_id:
                raise ValueError("invalid model ID")
        return value


class AIProfilesConfig(StrictModel):
    profiles: dict[str, AIProfile] = Field(default_factory=dict)
    providers: dict[str, AIProvider] = Field(default_factory=dict)
    version: Literal[1, 2] = 1
    routing: dict[str, AIRoute] = Field(default_factory=dict)
    vision: VisionSettings = Field(default_factory=VisionSettings)

    @field_validator("version", mode="before")
    @classmethod
    def valid_version(cls, value):
        if type(value) is not int or value not in (1, 2):
            raise ValueError("invalid configuration version")
        return value

    @model_validator(mode="after")
    def references(self):
        if self.version == 1 and self.providers:
            raise ValueError("providers require version 2")
        if len(self.profiles) > 100 or len(self.providers) > 100:
            raise ValueError("too many profiles")
        if any(
            not re.fullmatch(r"[a-zA-Z0-9_-]{1,64}", key)
            for key in (*self.profiles, *self.providers)
        ):
            raise ValueError("invalid profile ID")
        for purpose, route in self.routing.items():
            if purpose not in PURPOSES:
                raise ValueError("unknown purpose")
            if route.profile_id and route.profile_id not in self.profiles:
                raise ValueError("profile missing or still assigned")
            if route.provider_id:
                if route.profile_id or route.model or not route.model_id:
                    raise ValueError("ambiguous route")
                provider = self.providers.get(route.provider_id)
                if provider is None or route.model_id not in provider.models:
                    raise ValueError("provider or model missing or still assigned")
            elif route.model_id:
                raise ValueError("model requires provider")
            if self.version == 2 and (route.profile_id or route.model):
                raise ValueError("legacy route in v2 configuration")
        return self


def parse_ai_profiles(config: dict) -> AIProfilesConfig:
    try:
        return AIProfilesConfig.model_validate(
            {
                "profiles": config.get("ai_profiles", {}),
                "providers": config.get("ai_providers", {}),
                "version": config.get("ai_config_version", 1),
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


def providers_revision(config: dict) -> str:
    fields = (
        "ai_profiles",
        "ai_providers",
        "ai_routing",
        "ai_vision",
        "ai_config_version",
        "api_key",
        "base_url",
        "model",
    )
    content = {key: config.get(key) for key in fields}
    return hashlib.sha256(json.dumps(content, sort_keys=True).encode()).hexdigest()


def migrate_providers(config: dict) -> AIProfilesConfig:
    """Read legacy profiles without writing. The first v2 save commits the migration.

    Keep legacy fields on disk as recovery evidence. Credentials never pass through
    the browser; omitted keys on save are restored from this same migration.
    """
    parsed = parse_ai_profiles(config)
    if parsed.version == 2:
        return parsed
    from core.settings import get_settings

    providers = {}
    for key, profile in parsed.profiles.items():
        providers[key] = AIProvider(
            **profile.model_dump(exclude={"model", "supports_vision"}),
            models={profile.model: AIModel(supports_vision=profile.supports_vision)},
        )
    default_id = "legacy_default"
    while default_id in providers:
        default_id += "_"
    defaults = get_settings().ai
    routing = {}
    for purpose in PURPOSES:
        route = parsed.routing.get(purpose, AIRoute())
        if not route.profile_id and not route.model:
            routing[purpose] = AIRoute(supports_vision=route.supports_vision)
            continue
        if route.profile_id:
            provider_id = route.profile_id
            profile = parsed.profiles[provider_id]
            model_id = route.model or profile.model
            vision = profile.supports_vision
        else:
            provider_id = default_id
            model_id = route.model or config.get("model") or defaults.model
            vision = False
            if default_id not in providers:
                providers[default_id] = AIProvider(
                    name="默认服务（迁移）",
                    base_url=config.get("base_url") or defaults.base_url,
                    api_key=config.get("api_key") or defaults.effective_api_key or "",
                )
        providers[provider_id].models.setdefault(model_id, AIModel(supports_vision=vision))
        routing[purpose] = AIRoute(
            provider_id=provider_id, model_id=model_id, supports_vision=route.supports_vision
        )
    return AIProfilesConfig(providers=providers, routing=routing, vision=parsed.vision, version=2)
