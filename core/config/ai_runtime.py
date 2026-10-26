# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot

"""所有 AI 请求使用同一份运行时配置，环境变量作为兼容默认值。"""

from dataclasses import dataclass, field

from .runtime import runtime_config


@dataclass(frozen=True)
class AIConnectionSettings:
    api_key: str | None = field(repr=False)
    base_url: str
    model: str


def effective_ai_config(config: dict | None = None) -> AIConnectionSettings:
    from core.settings import get_settings

    if config is None:
        config = runtime_config.read()
    defaults = get_settings().ai
    return AIConnectionSettings(
        api_key=config.get("api_key") or defaults.effective_api_key,
        base_url=config.get("base_url") or defaults.base_url,
        model=config.get("model") or defaults.model,
    )
