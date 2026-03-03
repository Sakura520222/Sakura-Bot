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
媒体工具模块

提供媒体文件检测、大小计算、类型识别等工具函数
"""

import logging
from enum import Enum
from typing import TYPE_CHECKING

if TYPE_CHECKING:
    from telethon.tl.types import Message

logger = logging.getLogger(__name__)


class MediaType(str, Enum):  # noqa: UP042 (兼容 Python 3.10)
    """媒体类型枚举"""

    IMAGE = "image"
    VIDEO = "video"
    DOCUMENT = "document"
    NONE = "none"
    UNKNOWN = "unknown"


class ForwardStrategy(str, Enum):  # noqa: UP042 (兼容 Python 3.10)
    """转发策略枚举"""

    MEMORY = "memory"  # 内存转发
    DOWNLOAD = "download"  # 下载后转发


def get_media_type(message: "Message") -> MediaType:
    """
    检测消息的媒体类型

    Args:
        message: Telegram消息对象

    Returns:
        MediaType枚举值
    """
    if not message.media:
        return MediaType.NONE

    # 检查是否是图片
    if hasattr(message.media, "photo"):
        return MediaType.IMAGE

    # 检查是否是文档（视频或其他文件）
    if hasattr(message.media, "document"):
        mime_type = message.media.document.mime_type or ""
        if mime_type.startswith("video/"):
            return MediaType.VIDEO
        else:
            return MediaType.DOCUMENT

    return MediaType.UNKNOWN


def get_media_size(message: "Message") -> int:
    """
    获取消息中媒体文件的大小（字节）

    Args:
        message: Telegram消息对象

    Returns:
        文件大小（字节），无法获取时返回0
    """
    if not message.media:
        return 0

    try:
        # 文档类型（视频、文件等）
        if hasattr(message.media, "document"):
            return message.media.document.size

        # 图片类型（取最大尺寸的大小估算）
        if hasattr(message.media, "photo"):
            sizes = message.media.photo.sizes
            if sizes:
                # 取最大的尺寸大小
                return max(getattr(s, "size", 0) for s in sizes)
    except Exception as e:
        logger.debug(f"获取媒体大小失败: {type(e).__name__}: {e}")

    return 0


async def get_media_group_total_size(messages: list["Message"]) -> int:
    """
    计算媒体组的总大小

    Args:
        messages: 媒体组的消息列表

    Returns:
        总大小（字节）
    """
    total = 0
    for msg in messages:
        total += get_media_size(msg)
    return total


async def decide_forward_strategy(
    messages: list["Message"],
    small_file_threshold: int = 10 * 1024 * 1024,  # 10MB
    media_group_threshold: int = 50 * 1024 * 1024,  # 50MB
) -> dict:
    """
    决定转发策略

    Args:
        messages: 消息列表
        small_file_threshold: 小文件阈值（字节），默认10MB
        media_group_threshold: 媒体组阈值（字节），默认50MB

    Returns:
        策略字典，包含 strategy 和 reason
    """
    # 计算总大小
    total_size = await get_media_group_total_size(messages)

    # 检测媒体类型
    media_types = set(get_media_type(msg) for msg in messages)
    media_types.discard(MediaType.NONE)  # 移除none类型

    logger.debug(
        f"策略决策: 消息数={len(messages)}, "
        f"总大小={total_size / 1024 / 1024:.2f}MB, "
        f"媒体类型={media_types}"
    )

    # 决策逻辑
    if len(messages) > 1:  # 媒体组
        if media_types == {MediaType.IMAGE}:  # 纯图片
            return {
                "strategy": ForwardStrategy.MEMORY,
                "reason": "纯图片媒体组",
                "total_size": total_size,
            }
        elif MediaType.VIDEO in media_types or MediaType.DOCUMENT in media_types:
            # 包含视频或文档的混合媒体组
            if total_size < media_group_threshold:
                return {
                    "strategy": ForwardStrategy.MEMORY,
                    "reason": f"小文件混合媒体组（{total_size / 1024 / 1024:.2f}MB < 50MB）",
                    "total_size": total_size,
                }
            else:
                return {
                    "strategy": ForwardStrategy.DOWNLOAD,
                    "reason": f"大文件媒体组（{total_size / 1024 / 1024:.2f}MB >= 50MB）",
                    "total_size": total_size,
                }
    else:  # 单个文件
        if total_size < small_file_threshold:
            return {
                "strategy": ForwardStrategy.MEMORY,
                "reason": f"小文件（{total_size / 1024 / 1024:.2f}MB < 10MB）",
                "total_size": total_size,
            }
        else:
            return {
                "strategy": ForwardStrategy.DOWNLOAD,
                "reason": f"大文件（{total_size / 1024 / 1024:.2f}MB >= 10MB）",
                "total_size": total_size,
            }

    # 默认内存转发
    return {
        "strategy": ForwardStrategy.MEMORY,
        "reason": "默认策略",
        "total_size": total_size,
    }
