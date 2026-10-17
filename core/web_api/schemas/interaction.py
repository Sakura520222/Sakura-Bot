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
互动设置相关的请求/响应模型
"""

from pydantic import BaseModel, Field


class PollSettingsUpdate(BaseModel):
    """投票设置更新请求（频道级）"""

    enabled: bool | None = Field(default=None, description="是否启用投票，null 恢复全局配置")
    send_to_channel: bool | None = Field(
        default=None, description="是否发送到频道，null 恢复讨论组"
    )
    public_voters: bool | None = Field(
        default=None, description="是否公开投票者，null 恢复全局配置"
    )


class GlobalPollSettingsUpdate(BaseModel):
    """全局投票设置更新请求"""

    enable_poll: bool | None = Field(default=None, description="全局启用/禁用投票")
    poll_regen_threshold: int | None = Field(default=None, description="重新生成阈值")
    public_voters: bool | None = Field(default=None, description="是否公开投票者")
    enable_vote_regen_request: bool | None = Field(default=None, description="启用投票重新生成请求")


class AutoPollSettingsUpdate(BaseModel):
    """自动投票设置更新请求"""

    enabled: bool = Field(..., description="是否启用自动投票")


class CommentWelcomeUpdate(BaseModel):
    """评论区欢迎设置更新请求"""

    enabled: bool | None = Field(default=None, description="是否启用评论区欢迎")
    welcome_message: str | None = Field(default=None, description="欢迎消息文本")
    button_text: str | None = Field(default=None, description="按钮文本")
    button_action: str | None = Field(default=None, description="按钮动作: request_summary")


class CommentWelcomeConfigResponse(BaseModel):
    """评论区欢迎配置响应"""

    default: dict = Field(default_factory=dict, description="默认欢迎配置")
    channel_overrides: dict = Field(default_factory=dict, description="频道级别覆盖配置")
