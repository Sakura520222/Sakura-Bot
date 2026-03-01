# Copyright 2026 Sakura-Bot
#
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可，
# 并附加非商业使用限制条款。
#
# - 署名：必须提供本项目的原始来源链接
# - 非商业：禁止任何商业用途和分发
# - 相同方式共享：衍生作品必须采用相同的许可证
#
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
# 许可证全文：参见 LICENSE 文件

"""
消息过滤器模块

提供基于关键词的消息过滤功能
"""

import logging
import re
from typing import TYPE_CHECKING

if TYPE_CHECKING:
    from telethon.tl.types import Message

logger = logging.getLogger(__name__)


def should_forward_by_keywords(
    message: "Message", keywords: list[str] = None, blacklist: list[str] = None
) -> bool:
    """
    根据关键词判断是否应该转发消息

    Args:
        message: Telegram消息对象
        keywords: 白名单关键词列表，任一匹配即转发
        blacklist: 黑名单关键词列表，任一匹配即不转发

    Returns:
        是否应该转发
    """
    # 提取消息文本
    text = message.message or ""
    if not text:
        return False

    # 先检查黑名单（优先级更高）
    if blacklist:
        for keyword in blacklist:
            if keyword.lower() in text.lower():
                logger.debug(f"消息匹配黑名单关键词: {keyword}")
                return False

    # 检查白名单
    if keywords:
        for keyword in keywords:
            if keyword.lower() in text.lower():
                logger.debug(f"消息匹配白名单关键词: {keyword}")
                return True
        # 如果设置了白名单但都不匹配，则不转发
        logger.debug("消息不匹配任何白名单关键词")
        return False

    # 如果没有设置白名单，则转发所有消息（除非被黑名单过滤）
    return True


def should_forward_by_regex(
    message: "Message", patterns: list[str] = None, blacklist_patterns: list[str] = None
) -> bool:
    """
    根据正则表达式判断是否应该转发消息

    Args:
        message: Telegram消息对象
        patterns: 白名单正则表达式列表
        blacklist_patterns: 黑名单正则表达式列表

    Returns:
        是否应该转发
    """
    # 提取消息文本
    text = message.message or ""
    if not text:
        return False

    # 先检查黑名单（优先级更高）
    if blacklist_patterns:
        for pattern in blacklist_patterns:
            try:
                if re.search(pattern, text, re.IGNORECASE):
                    logger.debug(f"消息匹配黑名单正则: {pattern}")
                    return False
            except re.error as e:
                logger.warning(f"黑名单正则表达式无效: {pattern}, 错误: {e}")

    # 检查白名单
    if patterns:
        for pattern in patterns:
            try:
                if re.search(pattern, text, re.IGNORECASE):
                    logger.debug(f"消息匹配白名单正则: {pattern}")
                    return True
            except re.error as e:
                logger.warning(f"白名单正则表达式无效: {pattern}, 错误: {e}")
        # 如果设置了白名单但都不匹配，则不转发
        logger.debug("消息不匹配任何白名单正则")
        return False

    # 如果没有设置白名单，则转发所有消息（除非被黑名单过滤）
    return True
