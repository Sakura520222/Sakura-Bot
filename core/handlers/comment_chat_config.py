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
频道评论区AI聊天配置管理模块
"""

import asyncio
from typing import Any

from core.config import load_config, logger, save_config

# 默认配置
DEFAULT_COMMENT_CHAT_CONFIG = {
    "enabled": True,
    "wake_keyword": "小樱",
    "debounce_seconds": 3,
    "session_timeout": 30,
    "max_context_messages": 50,
}


async def get_comment_chat_config(channel_url: str) -> dict[str, Any]:
    """
    获取指定频道的评论区聊天配置（异步）

    Args:
        channel_url: 频道URL

    Returns:
        dict: 评论区聊天配置，如果未配置则返回默认配置
    """
    config = await asyncio.to_thread(load_config)
    channels = config.get("channels", {})

    if channel_url in channels:
        channel_config = channels[channel_url]
        if isinstance(channel_config, dict) and "comment_chat" in channel_config:
            # 合并配置和默认值（优先使用配置文件）
            result = DEFAULT_COMMENT_CHAT_CONFIG.copy()
            result.update(channel_config["comment_chat"])
            return result

    # 返回默认配置
    return DEFAULT_COMMENT_CHAT_CONFIG.copy()


async def set_comment_chat_config(
    channel_url: str,
    enabled: bool | None = None,
    wake_keyword: str | None = None,
    debounce_seconds: int | None = None,
    session_timeout: int | None = None,
    max_context_messages: int | None = None,
) -> dict[str, Any]:
    """
    设置指定频道的评论区聊天配置（异步）

    Args:
        channel_url: 频道URL
        enabled: 是否启用（可选）
        wake_keyword: 唤醒关键词（可选）
        debounce_seconds: 防抖时间（秒）（可选）
        session_timeout: 会话超时时间（分钟）（可选）
        max_context_messages: 最大上下文消息数（可选）

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
        if "comment_chat" in channel_config:
            current_config = channel_config["comment_chat"].copy()
        else:
            current_config = DEFAULT_COMMENT_CHAT_CONFIG.copy()
    else:
        channels[channel_url] = {}
        current_config = DEFAULT_COMMENT_CHAT_CONFIG.copy()

    # 更新配置
    if enabled is not None:
        current_config["enabled"] = enabled

    if wake_keyword is not None:
        if not wake_keyword.strip():
            raise ValueError("唤醒关键词不能为空")
        current_config["wake_keyword"] = wake_keyword.strip()

    if debounce_seconds is not None:
        if debounce_seconds < 0:
            raise ValueError("防抖时间不能为负数")
        current_config["debounce_seconds"] = debounce_seconds

    if session_timeout is not None:
        if session_timeout < 1:
            raise ValueError("会话超时时间不能小于1分钟")
        current_config["session_timeout"] = session_timeout

    if max_context_messages is not None:
        if max_context_messages < 10:
            raise ValueError("最大上下文消息数不能小于10")
        current_config["max_context_messages"] = max_context_messages

    # 保存配置
    channels[channel_url]["comment_chat"] = current_config
    await asyncio.to_thread(save_config, config)

    logger.info(f"已更新频道 {channel_url} 的评论区聊天配置")
    return current_config


async def get_all_comment_chat_configs() -> dict[str, dict[str, Any]]:
    """
    获取所有频道的评论区聊天配置（异步）

    Returns:
        dict: 所有频道配置的字典
    """
    config = await asyncio.to_thread(load_config)
    channels = config.get("channels", {})

    result = {}
    for channel_url, channel_config in channels.items():
        if isinstance(channel_config, dict) and "comment_chat" in channel_config:
            result[channel_url] = channel_config["comment_chat"]

    return result


async def delete_comment_chat_config(channel_url: str) -> bool:
    """
    删除指定频道的评论区聊天配置（异步）

    Args:
        channel_url: 频道URL

    Returns:
        bool: 是否成功删除
    """
    config = await asyncio.to_thread(load_config)
    channels = config.get("channels", {})

    if channel_url in channels and isinstance(channels[channel_url], dict):
        if "comment_chat" in channels[channel_url]:
            del channels[channel_url]["comment_chat"]
            await asyncio.to_thread(save_config, config)
            logger.info(f"已删除频道 {channel_url} 的评论区聊天配置")
            return True

    return False


def get_default_comment_chat_config() -> dict[str, Any]:
    """
    获取默认的评论区聊天配置

    Returns:
        dict: 默认配置
    """
    return DEFAULT_COMMENT_CHAT_CONFIG.copy()


def get_comment_session_id(channel_id: str, channel_msg_id: int) -> str:
    """
    生成评论区会话ID

    Args:
        channel_id: 频道ID
        channel_msg_id: 频道消息ID

    Returns:
        str: 会话ID，格式: comment_{channel_id}_{channel_msg_id}
    """
    return f"comment_{channel_id}_{channel_msg_id}"
