# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot

"""所有 AI 请求使用同一份运行时配置，环境变量作为兼容默认值。"""

from dataclasses import dataclass, field

from .ai_profiles import PURPOSES, parse_ai_profiles
from .runtime import runtime_config


@dataclass(frozen=True)
class AIConnectionSettings:
    api_key: str | None = field(repr=False)
    base_url: str
    model: str
    profile_id: str = ""
    supports_vision: bool = False
    vision_enabled: bool = False
    vision_ocr: bool = True
    vision_description: bool = True


def effective_ai_config(
    config: dict | None = None, purpose: str = "summary"
) -> AIConnectionSettings:
    from core.settings import get_settings

    if config is None:
        config = runtime_config.read()
    if purpose not in PURPOSES:
        raise ValueError("Unknown AI purpose")
    profiles = parse_ai_profiles(config)
    route = profiles.routing.get(purpose)
    vision = dict(
        vision_enabled=profiles.vision.enabled,
        vision_ocr=profiles.vision.ocr,
        vision_description=profiles.vision.description,
    )
    if route and route.profile_id:
        profile = profiles.profiles[route.profile_id]
        return AIConnectionSettings(
            api_key=profile.api_key,
            base_url=profile.base_url,
            model=route.model or profile.model,
            profile_id=route.profile_id,
            supports_vision=(
                profile.supports_vision if route.supports_vision is None else route.supports_vision
            ),
            **vision,
        )
    defaults = get_settings().ai
    # 未绑定档案的路由同样按用途生效：路由覆盖只改变模型与图片能力，
    # 凭据与地址仍来自旧配置/env，绝不与其他档案混用。
    return AIConnectionSettings(
        api_key=config.get("api_key") or defaults.effective_api_key,
        base_url=config.get("base_url") or defaults.base_url,
        model=(route.model if route and route.model else None)
        or config.get("model")
        or defaults.model,
        supports_vision=(
            route.supports_vision if route and route.supports_vision is not None else False
        ),
        **vision,
    )
