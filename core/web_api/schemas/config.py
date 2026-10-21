# Copyright 2026 Sakura-Bot
#
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
#
# - 署名：必须提供本项目的原始来源链接
# - 相同方式共享：衍生作品必须采用相同的许可证
#
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
# 许可证全文：参见 LICENSE 文件

"""
AI 配置相关的请求/响应模型
"""

from pydantic import BaseModel, ConfigDict, Field, field_validator


class AIConfigResponse(BaseModel):
    """AI 配置响应"""

    base_url: str = Field(default="", description="LLM API 基础 URL")
    model: str = Field(default="", description="LLM 模型名称")
    api_key_set: bool = Field(default=False, description="是否已配置 API Key")
    api_key_preview: str = Field(default="", description="API Key 脱敏预览")


class PromptResponse(BaseModel):
    """提示词响应"""

    prompt_type: str = Field(..., description="提示词类型: summary / poll / qa")
    content: str = Field(default="", description="提示词内容")
    is_default: bool = Field(default=False, description="是否为默认提示词")


class PromptUpdateRequest(BaseModel):
    """更新提示词请求"""

    content: str = Field(..., description="新的提示词内容")


class AIConfigUpdateRequest(BaseModel):
    """更新 AI 配置请求"""

    base_url: str | None = Field(default=None, description="LLM API 基础 URL")
    model: str | None = Field(default=None, description="LLM 模型名称")
    api_key: str | None = Field(default=None, description="LLM API Key")

    @field_validator("base_url", "model", "api_key", mode="before")
    @classmethod
    def trim_optional_value(cls, value):
        return value.strip() or None if isinstance(value, str) else value


class QAQuotaUpdateRequest(BaseModel):
    model_config = ConfigDict(strict=True, extra="forbid")

    user_daily_limit: int | None = Field(default=None, ge=1, le=2147483647)
    total_daily_limit: int | None = Field(default=None, ge=10, le=2147483647)
