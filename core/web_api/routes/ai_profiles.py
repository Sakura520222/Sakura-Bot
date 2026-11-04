# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot

"""服务档案 API：脱敏读取、版本冲突检测、连接与调用测试。"""

import logging
from copy import deepcopy

from fastapi import APIRouter, HTTPException, Request
from openai import AsyncOpenAI

from core.config.ai_profiles import (
    PURPOSES,
    AIProfile,
    AIProfilesConfig,
    migrate_providers,
    parse_ai_profiles,
    profiles_revision,
    providers_revision,
)
from core.config.ai_runtime import effective_ai_config
from core.config.persistence import ConfigConflictError, ConfigPersistenceError
from core.i18n.i18n import get_text
from core.web_api.deps import get_config, write_config

router = APIRouter()
logger = logging.getLogger(__name__)


def public_profiles(config):
    parsed = parse_ai_profiles(config)
    profiles = {}
    for key, profile in parsed.profiles.items():
        data = profile.model_dump(exclude={"api_key"})
        data.update(
            api_key_set=bool(profile.api_key), api_key_preview="********" if profile.api_key else ""
        )
        profiles[key] = data
    effective = {}
    for purpose in PURPOSES:
        resolved = effective_ai_config(config, purpose)
        effective[purpose] = {
            "profile_id": resolved.profile_id,
            "model": resolved.model,
            "supports_vision": resolved.supports_vision,
        }
    return {
        "profiles": profiles,
        "routing": parsed.model_dump()["routing"],
        "vision": parsed.vision.model_dump(),
        "effective": effective,
        "revision": profiles_revision(config),
    }


@router.get("/profiles")
async def get_profiles():
    try:
        return {"success": True, "data": public_profiles(get_config())}
    except (ConfigPersistenceError, ValueError, TypeError):
        raise HTTPException(400, get_text("ai_profiles.invalid")) from None


@router.put("/profiles")
async def save_profiles(request: Request):
    # 手动处理校验，避免 FastAPI 默认 422 在 input 字段回显 API Key。
    try:
        body = await request.json()
        if not isinstance(body, dict) or set(body) != {"profiles", "routing", "vision", "revision"}:
            raise ValueError()
        config = get_config()
        if config.get("ai_config_version", 1) != 1:
            raise ConfigConflictError("AI configuration upgraded; reload the page")
        if body["revision"] != profiles_revision(config):
            raise ConfigConflictError(get_text("ai_profiles.conflict"))
        candidate = deepcopy(body)
        candidate.pop("revision")
        for key, profile in candidate["profiles"].items():
            if "api_key" not in profile:
                profile["api_key"] = config.get("ai_profiles", {}).get(key, {}).get("api_key", "")
        parsed = AIProfilesConfig.model_validate(candidate)
        data = parsed.model_dump()
        config.update(
            ai_profiles=data["profiles"], ai_routing=data["routing"], ai_vision=data["vision"]
        )
        write_config(config)
        persisted = get_config()
        if profiles_revision(persisted) != profiles_revision(config):
            raise ConfigConflictError("AI configuration readback mismatch")
        return {
            "success": True,
            "data": public_profiles(persisted),
            "message": get_text("ai_profiles.saved"),
        }
    except ConfigConflictError:
        raise HTTPException(409, get_text("ai_profiles.conflict")) from None
    except (ValueError, TypeError, KeyError, AttributeError, ConfigPersistenceError):
        raise HTTPException(400, get_text("ai_profiles.validation")) from None


def saved_profile(profile_id):
    try:
        profiles = parse_ai_profiles(get_config()).profiles
    except (ValueError, TypeError, ConfigPersistenceError):
        raise HTTPException(400, get_text("ai_profiles.invalid")) from None
    if profile_id not in profiles:
        raise HTTPException(404, get_text("ai_profiles.not_found"))
    return profiles[profile_id]


async def _json_body(request: Request) -> dict:
    """读取 JSON 请求体；空请求体或非对象一律按空字典处理。"""
    try:
        body = await request.json()
    except Exception:
        return {}
    return body if isinstance(body, dict) else {}


def _draft_connection(body: dict) -> tuple[str, str] | None:
    """提取请求体中的草稿连接参数；未提供 base_url 时返回 None 走已保存档案。

    值直接来自请求，校验失败以 400 结束且错误信息不回显任何内容。
    """
    if not body.get("base_url"):
        return None
    try:
        base_url = AIProfile.valid_url(AIProfile.safe_string(body["base_url"]))
        api_key = AIProfile.safe_string(body.get("api_key") or "")
        if not base_url or len(base_url) > 2048 or len(api_key) > 8192:
            raise ValueError()
    except (ValueError, TypeError):
        raise HTTPException(400, get_text("ai_profiles.validation")) from None
    return base_url, api_key


def _connection_for(profile_id: str, body: dict) -> tuple[str, str]:
    """解析列模型/测试所用的连接参数：草稿优先，缺密钥时沿用已保存档案。"""
    draft = _draft_connection(body)
    if not draft:
        profile = saved_profile(profile_id)
        return profile.base_url, profile.api_key
    base_url, api_key = draft
    if not api_key:
        try:
            api_key = saved_profile(profile_id).api_key
        except HTTPException:
            api_key = ""
    return base_url, api_key


@router.post("/profiles/{profile_id}/models")
async def list_profile_models(profile_id: str, request: Request):
    base_url, api_key = _connection_for(profile_id, await _json_body(request))
    try:
        async with AsyncOpenAI(
            api_key=api_key, base_url=base_url, timeout=15, max_retries=0
        ) as client:
            result = await client.models.list()
        models = sorted({item.id for item in result.data if isinstance(item.id, str)})
        return {"success": True, "data": models}
    except Exception:
        # SDK 异常可能包含请求参数、密钥或上游响应正文，不得回传。
        raise HTTPException(502, get_text("ai_profiles.models_failed")) from None


@router.post("/profiles/{profile_id}/test")
async def test_profile(profile_id: str, request: Request):
    body = await _json_body(request)
    if body.get("kind") not in {"connection", "call"}:
        raise HTTPException(400, get_text("ai_profiles.test_kind"))
    model = body.get("model")
    if model is not None and (
        not isinstance(model, str) or len(model) > 256 or any(ord(c) < 32 for c in model)
    ):
        raise HTTPException(400, get_text("ai_profiles.test_kind"))
    if not model:
        try:
            model = saved_profile(profile_id).model
        except HTTPException:
            model = ""
    if body["kind"] == "call" and not model:
        raise HTTPException(400, get_text("ai_profiles.test_kind"))
    base_url, api_key = _connection_for(profile_id, body)
    try:
        async with AsyncOpenAI(
            api_key=api_key, base_url=base_url, timeout=20, max_retries=0
        ) as client:
            if body["kind"] == "connection":
                await client.models.list()
            else:
                await client.chat.completions.create(
                    model=model, messages=[{"role": "user", "content": "Reply with only OK."}]
                )
        return {"success": True, "message": get_text("ai_profiles.success")}
    except Exception:
        raise HTTPException(502, get_text("ai_profiles.failed")) from None


def public_providers(config):
    parsed = migrate_providers(config)
    providers = {}
    for key, provider in parsed.providers.items():
        data = provider.model_dump(exclude={"api_key"})
        data.update(
            api_key_set=bool(provider.api_key),
            api_key_preview="********" if provider.api_key else "",
        )
        providers[key] = data
    resolved_config = dict(
        config,
        ai_providers=parsed.model_dump()["providers"],
        ai_routing=parsed.model_dump()["routing"],
        ai_config_version=2,
    )
    effective = {}
    for purpose in PURPOSES:
        resolved = effective_ai_config(resolved_config, purpose)
        effective[purpose] = {
            "provider_id": resolved.profile_id,
            "model_id": resolved.model,
            "supports_vision": resolved.supports_vision,
        }
    return {
        "version": 2,
        "providers": providers,
        "routing": {
            key: route.model_dump(include={"provider_id", "model_id", "supports_vision"})
            for key, route in parsed.routing.items()
        },
        "vision": parsed.vision.model_dump(),
        "effective": effective,
        "revision": providers_revision(config),
    }


@router.get("/providers")
async def get_providers():
    try:
        return {"success": True, "data": public_providers(get_config())}
    except (ConfigPersistenceError, ValueError, TypeError):
        raise HTTPException(400, get_text("ai_profiles.invalid")) from None


@router.put("/providers")
async def save_providers(request: Request):
    try:
        body = await request.json()
        if (
            not isinstance(body, dict)
            or set(body) != {"version", "providers", "routing", "vision", "revision"}
            or body["version"] != 2
        ):
            raise ValueError()
        config = get_config()
        if body["revision"] != providers_revision(config):
            raise ConfigConflictError("AI configuration changed")
        previous = migrate_providers(config)
        candidate = deepcopy(body)
        candidate.pop("revision")
        for key, provider in candidate["providers"].items():
            if "api_key" not in provider:
                provider["api_key"] = (
                    previous.providers[key].api_key if key in previous.providers else ""
                )
        parsed = AIProfilesConfig.model_validate(candidate)
        data = parsed.model_dump()
        config.update(
            ai_config_version=2,
            ai_providers=data["providers"],
            ai_routing=data["routing"],
            ai_vision=data["vision"],
        )
        write_config(config)
        persisted = get_config()
        if providers_revision(persisted) != providers_revision(config):
            raise ConfigConflictError("AI configuration readback mismatch")
        result = public_providers(persisted)
        logger.info(
            "AI configuration persisted and verified: revision=%s providers=%d models=%d",
            result["revision"][:12],
            len(parsed.providers),
            sum(len(p.models) for p in parsed.providers.values()),
        )
        return {
            "success": True,
            "data": result,
            "message": "厂商、已添加模型和用途配置已保存并校验",
        }
    except ConfigConflictError:
        raise HTTPException(409, get_text("ai_profiles.conflict")) from None
    except (ValueError, TypeError, KeyError, AttributeError, ConfigPersistenceError):
        raise HTTPException(400, get_text("ai_profiles.validation")) from None


def provider_connection(provider_id, body):
    try:
        saved = migrate_providers(get_config()).providers.get(provider_id)
    except (ValueError, TypeError, ConfigPersistenceError):
        raise HTTPException(400, get_text("ai_profiles.invalid")) from None
    draft = _draft_connection(body)
    if not draft and saved is None:
        raise HTTPException(404, get_text("ai_profiles.not_found"))
    base_url, key = draft if draft else (saved.base_url, saved.api_key)
    # Omission/blank preserves the key; explicit clearing also applies to tests.
    if body.get("clear_api_key") is True:
        key = ""
    elif not key and saved:
        key = saved.api_key
    return base_url, key


@router.post("/providers/{provider_id}/models")
async def list_provider_models(provider_id: str, request: Request):
    base_url, key = provider_connection(provider_id, await _json_body(request))
    try:
        async with AsyncOpenAI(api_key=key, base_url=base_url, timeout=15, max_retries=0) as client:
            result = await client.models.list()
        models = sorted(
            {
                item.id
                for item in result.data
                if isinstance(item.id, str)
                and 0 < len(item.id) <= 256
                and AIProfile.safe_string(item.id) == item.id
            }
        )
        return {"success": True, "data": models}
    except Exception:
        raise HTTPException(502, get_text("ai_profiles.models_failed")) from None


@router.post("/providers/{provider_id}/test")
async def test_provider(provider_id: str, request: Request):
    body = await _json_body(request)
    if body.get("kind") not in {"connection", "call"}:
        raise HTTPException(400, get_text("ai_profiles.test_kind"))
    model = body.get("model", "")
    if (
        not isinstance(model, str)
        or len(model) > 256
        or (body["kind"] == "call" and not model.strip())
        or any(ord(c) < 32 or ord(c) == 127 for c in model)
    ):
        raise HTTPException(400, get_text("ai_profiles.test_kind"))
    base_url, key = provider_connection(provider_id, body)
    try:
        async with AsyncOpenAI(api_key=key, base_url=base_url, timeout=20, max_retries=0) as client:
            if body["kind"] == "connection":
                await client.models.list()
            else:
                await client.chat.completions.create(
                    model=model.strip(),
                    messages=[{"role": "user", "content": "Reply with only OK."}],
                )
        return {"success": True, "message": get_text("ai_profiles.success")}
    except Exception:
        raise HTTPException(502, get_text("ai_profiles.failed")) from None
