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
频道评论区欢迎消息配置管理模块
"""

import asyncio
from typing import Any

from core.config import load_config, logger, save_config

# 默认配置（用于新创建的频道）
DEFAULT_COMMENT_WELCOME_CONFIG = {
    "enabled": True,
    "welcome_message": "comment_welcome.message",  # i18n key
    "button_text": "comment_welcome.button",  # i18n key
    "button_action": "request_summary",
}

# Telegram 限制
MAX_BUTTON_TEXT_LENGTH = 20  # 按钮文本最大字符数（美观考虑）
MAX_CALLBACK_DATA_LENGTH = 64  # Callback Data 最大字节数（Telegram 硬限制）


async def get_channel_comment_welcome_config(channel_url: str) -> dict[str, Any]:
    """
    获取指定频道的评论区欢迎配置（异步）

    Args:
        channel_url: 频道URL

    Returns:
        dict: 频道配置，如果未配置则返回默认配置
    """
    config = await asyncio.to_thread(load_config)
    channels = config.get("channels", {})

    if channel_url in channels:
        channel_config = channels[channel_url]
        if isinstance(channel_config, dict) and "comment_welcome" in channel_config:
            # 合并配置和默认值（优先使用频道配置）
            result = DEFAULT_COMMENT_WELCOME_CONFIG.copy()
            result.update(channel_config["comment_welcome"])
            return result

    # 返回默认配置
    return DEFAULT_COMMENT_WELCOME_CONFIG.copy()


async def get_all_comment_welcome_configs() -> dict[str, dict[str, Any]]:
    """
    获取所有频道的评论区欢迎配置（异步）

    Returns:
        dict: 所有频道配置的字典
    """
    config = await asyncio.to_thread(load_config)
    channels = config.get("channels", {})

    result = {}
    for channel_url, channel_config in channels.items():
        if isinstance(channel_config, dict) and "comment_welcome" in channel_config:
            result[channel_url] = channel_config["comment_welcome"]

    return result


async def set_channel_comment_welcome_config(
    channel_url: str,
    enabled: bool | None = None,
    welcome_message: str | None = None,
    button_text: str | None = None,
    button_action: str | None = None,
) -> dict[str, Any]:
    """
    设置指定频道的评论区欢迎配置（异步）

    Args:
        channel_url: 频道URL
        enabled: 是否启用（可选）
        welcome_message: 欢迎消息（可选）
        button_text: 按钮文本（可选）
        button_action: 按钮行为（可选）

    Returns:
        dict: 更新后的配置

    Raises:
        ValueError: 参数验证失败
    """
    config = await asyncio.to_thread(load_config)

    # 确保 channels 存在
    if "channels" not in config:
        config["channels"] = {}

    channels = config["channels"]

    # 获取现有配置或使用默认值
    if channel_url in channels and isinstance(channels[channel_url], dict):
        channel_config = channels[channel_url]
        if "comment_welcome" in channel_config:
            current_config = channel_config["comment_welcome"].copy()
        else:
            current_config = DEFAULT_COMMENT_WELCOME_CONFIG.copy()
    else:
        channels[channel_url] = {}
        current_config = DEFAULT_COMMENT_WELCOME_CONFIG.copy()

    # 更新配置
    if enabled is not None:
        current_config["enabled"] = enabled

    if welcome_message is not None:
        current_config["welcome_message"] = welcome_message

    if button_text is not None:
        # 验证按钮文本长度
        if len(button_text) > MAX_BUTTON_TEXT_LENGTH:
            raise ValueError(
                f"按钮文本过长（{len(button_text)}字符），最大允许{MAX_BUTTON_TEXT_LENGTH}字符"
            )
        current_config["button_text"] = button_text

    if button_action is not None:
        # 验证按钮行为
        valid_actions = ["request_summary"]  # 预留扩展
        if button_action not in valid_actions:
            raise ValueError(f"无效的按钮行为：{button_action}，支持的行为：{valid_actions}")
        current_config["button_action"] = button_action

    # 保存配置
    channels[channel_url]["comment_welcome"] = current_config
    await asyncio.to_thread(save_config, config)

    logger.info(f"已更新频道 {channel_url} 的评论区欢迎配置")
    return current_config


async def delete_channel_comment_welcome_config(channel_url: str) -> bool:
    """
    删除指定频道的评论区欢迎配置（异步）

    Args:
        channel_url: 频道URL

    Returns:
        bool: 是否成功删除
    """
    config = await asyncio.to_thread(load_config)
    channels = config.get("channels", {})

    if channel_url in channels and isinstance(channels[channel_url], dict):
        if "comment_welcome" in channels[channel_url]:
            del channels[channel_url]["comment_welcome"]
            await asyncio.to_thread(save_config, config)
            logger.info(f"已删除频道 {channel_url} 的评论区欢迎配置")
            return True

    return False


def validate_callback_data_length(channel_id: str, msg_id: int) -> bool:
    """
    验证 Callback Data 长度是否超过 Telegram 限制

    Args:
        channel_id: 频道ID
        msg_id: 消息ID

    Returns:
        bool: 是否在限制内
    """
    # 生成 Callback Data（使用冒号分隔）
    callback_data = f"req_summary:{channel_id}:{msg_id}".encode()

    if len(callback_data) > MAX_CALLBACK_DATA_LENGTH:
        logger.warning(
            f"Callback Data 过长（{len(callback_data)}字节），"
            f"超过 Telegram 限制（{MAX_CALLBACK_DATA_LENGTH}字节）：{callback_data}"
        )
        return False

    return True


def get_default_comment_welcome_config() -> dict[str, Any]:
    """
    获取默认的评论区欢迎配置

    Returns:
        dict: 默认配置
    """
    return DEFAULT_COMMENT_WELCOME_CONFIG.copy()
