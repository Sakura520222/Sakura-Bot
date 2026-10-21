# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot

"""助手 Bot 配额配置，兼容原有环境变量。"""

import os

from pydantic import BaseModel, ConfigDict, Field


class QAQuotaSettings(BaseModel):
    model_config = ConfigDict(strict=True, extra="forbid")

    user_daily_limit: int = Field(default=3, ge=1, le=2147483647)
    total_daily_limit: int = Field(default=200, ge=10, le=2147483647)


def effective_qa_quota(config: dict) -> QAQuotaSettings:
    values = {}
    for field, env, default, minimum in (
        ("user_daily_limit", "QA_BOT_USER_LIMIT", 3, 1),
        ("total_daily_limit", "QA_BOT_DAILY_LIMIT", 200, 10),
    ):
        try:
            values[field] = min(2147483647, max(minimum, int(os.getenv(env, str(default)))))
        except ValueError:
            values[field] = default
    values.update(config.get("qa_bot_quota", {}))
    return QAQuotaSettings.model_validate(values)
