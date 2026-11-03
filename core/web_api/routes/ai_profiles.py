# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot

"""服务档案 API：脱敏读取、版本冲突检测与独立测试。"""

from copy import deepcopy

from fastapi import APIRouter, HTTPException, Request
from openai import AsyncOpenAI

from core.config.ai_profiles import (
    PURPOSES,
    AIProfilesConfig,
    parse_ai_profiles,
    profiles_revision,
)
from core.config.ai_runtime import effective_ai_config
from core.config.persistence import ConfigConflictError, ConfigPersistenceError
from core.i18n.i18n import get_text
from core.web_api.deps import get_config, write_config

router = APIRouter()


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
        return {
            "success": True,
            "data": public_profiles(config),
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


@router.post("/profiles/{profile_id}/models")
async def list_profile_models(profile_id: str):
    profile = saved_profile(profile_id)
    try:
        async with AsyncOpenAI(
            api_key=profile.api_key, base_url=profile.base_url, timeout=15, max_retries=0
        ) as client:
            result = await client.models.list()
            models = sorted({item.id for item in result.data if isinstance(item.id, str)})
        return {"success": True, "data": models}
    except Exception:
        # SDK 异常可能包含请求参数、密钥或上游响应正文，不得回传。
        raise HTTPException(502, get_text("ai_profiles.models_failed")) from None


@router.post("/profiles/{profile_id}/test")
async def test_profile(profile_id: str, request: Request):
    profile = saved_profile(profile_id)
    try:
        body = await request.json()
        if not isinstance(body, dict) or body.get("kind") not in {"connection", "call"}:
            raise ValueError()
        model = body.get("model") or profile.model
        if not isinstance(model, str) or len(model) > 256 or any(ord(c) < 32 for c in model):
            raise ValueError()
    except (ValueError, TypeError):
        raise HTTPException(400, get_text("ai_profiles.test_kind")) from None
    try:
        async with AsyncOpenAI(
            api_key=profile.api_key, base_url=profile.base_url, timeout=20, max_retries=0
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
