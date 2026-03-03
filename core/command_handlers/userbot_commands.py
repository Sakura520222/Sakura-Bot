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
UserBot 相关命令处理器

提供 UserBot 状态检查和管理功能
"""

import logging

from telethon.tl.types import Message

from ..i18n import t
from ..userbot_client import get_userbot_client

logger = logging.getLogger(__name__)


async def handle_userbot_status(client, message: Message):
    """
    处理 UserBot 状态查询命令

    Args:
        client: Telegram 客户端实例
        message: 消息对象
    """
    try:
        userbot = get_userbot_client()

        if not userbot:
            # UserBot 未启用
            await message.reply(
                "❌ **UserBot 未启用**\n\n"
                "请在 `.env` 文件中配置：\n"
                "• `USERBOT_ENABLED=true`\n"
                "• `USERBOT_PHONE_NUMBER=+8613800138000`\n\n"
                "配置后重启机器人即可。",
                parse_mode="md",
            )
            return

        # 获取 UserBot 状态
        status = await userbot.get_status()

        if not status.get("available", False):
            # UserBot 初始化失败
            error = status.get("error", "未知错误")
            await message.reply(
                f"⚠️ **UserBot 不可用**\n\n错误: {error}\n\n请检查配置并重启机器人。",
                parse_mode="md",
            )
            return

        # UserBot 可用，显示简化信息（保护隐私）
        is_connected = status.get("connected", False)
        is_initialized = status.get("initialized", False)

        # 构建状态消息（简化版，不包含敏感信息）
        status_text = f"""✅ **UserBot 状态：运行中**

**连接状态**
• 已连接: {"✅ 是" if is_connected else "❌ 否"}
• 已初始化: {"✅ 是" if is_initialized else "❌ 否"}

**功能说明**
UserBot 使用您的真实 Telegram 账号，具有以下优势：
• ✅ 可访问私有频道
• ✅ 更高的消息抓取权限
• ✅ 更稳定的连接
• ✅ 支持更多高级功能"""

        await message.reply(status_text, parse_mode="md")

    except Exception as e:
        logger.error(f"处理 UserBot 状态查询失败: {type(e).__name__}: {e}", exc_info=True)
        await message.reply(
            f"❌ 查询 UserBot 状态失败: {type(e).__name__}: {e}",
            parse_mode="md",
        )


async def handle_userbot_join(client, message: Message):
    """
    处理 UserBot 加入频道命令

    Args:
        client: Telegram 客户端实例
        message: 消息对象
    """
    try:
        userbot = get_userbot_client()

        if not userbot or not userbot.is_available():
            await message.reply(t("userbot.not_available"))
            return

        # 解析命令参数
        args = message.message.text.split(maxsplit=1)
        if len(args) < 2:
            await message.reply(t("userbot.join_usage"))
            return

        channel_link = args[1].strip()

        # 调用 UserBot 加入频道
        result = await userbot.join_channel(channel_link)

        if result.get("success"):
            if result.get("already_joined"):
                await message.reply(t("userbot.join_already_joined", channel=channel_link))
            else:
                await message.reply(t("userbot.join_success", channel=channel_link))
        else:
            error_code = result.get("error", "")
            error_message = result.get("message", "未知错误")

            # 根据错误类型选择合适的翻译消息
            if error_code == "channel_private":
                await message.reply(t("userbot.join_failed_private", channel=channel_link))
            elif error_code == "channel_invalid":
                await message.reply(t("userbot.join_failed_not_found", channel=channel_link))
            elif error_code == "invalid_channel_format":
                await message.reply(t("userbot.join_failed_invalid", channel=channel_link))
            else:
                await message.reply(
                    t("userbot.join_failed_error", channel=channel_link, error=error_message)
                )

    except Exception as e:
        logger.error(f"处理 UserBot 加入频道命令失败: {type(e).__name__}: {e}", exc_info=True)
        await message.reply(t("userbot.join_failed_error", channel="未知", error=str(e)))


async def handle_userbot_leave(client, message: Message):
    """
    处理 UserBot 离开频道命令

    Args:
        client: Telegram 客户端实例
        message: 消息对象
    """
    try:
        userbot = get_userbot_client()

        if not userbot or not userbot.is_available():
            await message.reply(t("userbot.not_available"))
            return

        # 解析命令参数
        args = message.message.text.split(maxsplit=1)
        if len(args) < 2:
            await message.reply(t("userbot.leave_usage"))
            return

        channel_link = args[1].strip()

        # 调用 UserBot 离开频道
        result = await userbot.leave_channel(channel_link)

        if result.get("success"):
            if result.get("not_joined"):
                await message.reply(t("userbot.leave_not_joined", channel=channel_link))
            else:
                await message.reply(t("userbot.leave_success", channel=channel_link))
        else:
            error_message = result.get("message", "未知错误")
            await message.reply(
                t("userbot.leave_failed", channel=channel_link, error=error_message)
            )

    except Exception as e:
        logger.error(f"处理 UserBot 离开频道命令失败: {type(e).__name__}: {e}", exc_info=True)
        await message.reply(t("userbot.leave_failed", channel="未知", error=str(e)))


async def handle_userbot_list(client, message: Message):
    """
    处理 UserBot 列出已加入频道命令

    Args:
        client: Telegram 客户端实例
        message: 消息对象
    """
    try:
        userbot = get_userbot_client()

        if not userbot or not userbot.is_available():
            await message.reply(t("userbot.not_available"))
            return

        # 调用 UserBot 列出频道
        result = await userbot.list_joined_channels()

        if not result.get("success"):
            error_message = result.get("message", "未知错误")
            await message.reply(f"❌ 列出频道失败: {error_message}")
            return

        channels = result.get("channels", [])
        count = result.get("count", 0)

        if count == 0:
            await message.reply(t("userbot.list_empty"))
            return

        # 每条消息的最大长度（保留一些余量）
        MAX_LENGTH = 3800

        # 分批发送频道列表，使用引用格式
        batch = []
        current_length = 0
        batch_number = 1
        total_batches = (count + 99) // 100  # 估算批次数量

        for index, channel in enumerate(channels, start=1):
            title = channel.get("title", "未知频道")
            username = channel.get("username")
            channel_id = channel.get("id", "未知")

            if username:
                channel_info = f"@{username}"
            else:
                channel_info = f"ID: {channel_id}"

            line = t(
                "userbot.list_item", index=index, title=title, channel=channel_info, id=channel_id
            )

            # 检查是否需要开始新的批次
            if current_length + len(line) + 10 > MAX_LENGTH:  # 10 是余量
                # 发送当前批次
                batch_text = "\n".join(batch)
                if batch_number == 1:
                    await message.reply(
                        f"{t('userbot.list_title')}\n\n{batch_text}", parse_mode="md"
                    )
                else:
                    await message.reply(
                        f"📋 **继续 ({batch_number}/{total_batches})**\n\n{batch_text}",
                        parse_mode="md",
                    )

                # 开始新批次
                batch = [line]
                current_length = len(line)
                batch_number += 1
            else:
                batch.append(line)
                current_length += len(line)

        # 发送最后一批
        if batch:
            batch_text = "\n".join(batch)
            if batch_number == 1:
                await message.reply(f"{t('userbot.list_title')}\n\n{batch_text}", parse_mode="md")
            else:
                await message.reply(
                    f"📋 **继续 ({batch_number}/{total_batches}) - 完成**\n\n{batch_text}",
                    parse_mode="md",
                )

    except Exception as e:
        logger.error(f"处理 UserBot 列出频道命令失败: {type(e).__name__}: {e}", exc_info=True)
        await message.reply(f"❌ 列出频道失败: {type(e).__name__}: {e}")
