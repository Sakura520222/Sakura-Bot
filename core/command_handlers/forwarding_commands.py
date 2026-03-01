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
频道消息转发命令处理器

提供转发功能的命令接口：
- /forwarding 查看转发状态
- /forwarding_enable 启用转发
- /forwarding_disable 禁用转发
- /forwarding_stats 查看转发统计
"""

import logging
from typing import TYPE_CHECKING

from core.i18n import get_text as t

if TYPE_CHECKING:
    from telethon import TelegramClient
    from telethon.tl.types import Message

    from core.forwarding.forwarding_handler import ForwardingHandler

logger = logging.getLogger(__name__)


async def cmd_forwarding_status(
    client: "TelegramClient", message: "Message", handler: "ForwardingHandler"
):
    """
    查看转发状态

    用法: /forwarding
    """
    try:
        status_text = (
            t("forwarding.status_enabled") if handler.enabled else t("forwarding.status_disabled")
        )
        rule_count = len(handler.config.get("rules", []))

        response = t(
            "forwarding.status_message",
            status=status_text,
            rule_count=rule_count,
        )

        # 如果有规则，列出规则
        if rule_count > 0:
            response += "\n\n" + t("forwarding.rules_header")
            for i, rule in enumerate(handler.config.get("rules", []), 1):
                source = rule.get("source_channel", "Unknown")
                target = rule.get("target_channel", "Unknown")
                response += f"\n{i}. {source} → {target}"

        await message.reply(response)
        logger.info(f"用户 {message.sender_id} 查询转发状态")

    except Exception as e:
        logger.error(f"查询转发状态失败: {type(e).__name__}: {e}", exc_info=True)
        await message.reply(t("forwarding.error.query_failed", error=str(e)))


async def cmd_forwarding_enable(
    client: "TelegramClient", message: "Message", handler: "ForwardingHandler"
):
    """
    启用转发功能

    用法: /forwarding_enable
    """
    try:
        if handler.enabled:
            await message.reply(t("forwarding.already_enabled"))
            return

        handler.enabled = True
        await message.reply(t("forwarding.enabled"))
        logger.info(f"用户 {message.sender_id} 启用了转发功能")

    except Exception as e:
        logger.error(f"启用转发功能失败: {type(e).__name__}: {e}", exc_info=True)
        await message.reply(t("forwarding.error.enable_failed", error=str(e)))


async def cmd_forwarding_disable(
    client: "TelegramClient", message: "Message", handler: "ForwardingHandler"
):
    """
    禁用转发功能

    用法: /forwarding_disable
    """
    try:
        if not handler.enabled:
            await message.reply(t("forwarding.already_disabled"))
            return

        handler.enabled = False
        await message.reply(t("forwarding.disabled"))
        logger.info(f"用户 {message.sender_id} 禁用了转发功能")

    except Exception as e:
        logger.error(f"禁用转发功能失败: {type(e).__name__}: {e}", exc_info=True)
        await message.reply(t("forwarding.error.disable_failed", error=str(e)))


async def cmd_forwarding_stats(
    client: "TelegramClient", message: "Message", handler: "ForwardingHandler"
):
    """
    查看转发统计

    用法: /forwarding_stats [频道URL]
    """
    try:
        # 解析参数
        args = message.message.split()[1:] if message.message else []
        channel_id = args[0] if args else None

        stats = await handler.get_statistics(channel_id)

        if not stats:
            await message.reply(t("forwarding.stats.no_data"))
            return

        # 格式化统计信息
        if channel_id:
            # 单个频道统计
            response = t(
                "forwarding.stats.single_channel",
                channel=channel_id,
                total=stats.get("total_forwarded", 0),
                last=stats.get("last_forwarded") or t("forwarding.stats.never"),
            )
        else:
            # 所有频道统计
            total_all = stats.get("total_all_channels", 0)
            response = t("forwarding.stats.total", total=total_all)

            # 列出各频道统计
            by_channel = stats.get("by_channel", [])
            if by_channel:
                response += "\n\n" + t("forwarding.stats.by_channel_header")
                for item in by_channel[:10]:  # 最多显示10个
                    channel = item.get("channel_id", "Unknown")
                    count = item.get("total_forwarded", 0)
                    response += f"\n• {channel}: {count}"

        await message.reply(response)
        logger.info(f"用户 {message.sender_id} 查询转发统计")

    except Exception as e:
        logger.error(f"查询转发统计失败: {type(e).__name__}: {e}", exc_info=True)
        await message.reply(t("forwarding.error.stats_failed", error=str(e)))
