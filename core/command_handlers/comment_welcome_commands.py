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
评论区欢迎配置命令处理
"""

from ..channel_comment_welcome_config import (
    delete_channel_comment_welcome_config,
    get_all_comment_welcome_configs,
    get_channel_comment_welcome_config,
    get_default_comment_welcome_config,
    set_channel_comment_welcome_config,
)
from ..config import ADMIN_LIST, CHANNELS, logger
from ..i18n import get_text


async def handle_show_comment_welcome(event):
    """
    处理/showcommentwelcome命令，查看评论区欢迎配置

    用法：
    /showcommentwelcome                    # 查看所有频道配置
    /showcommentwelcome <channel>          # 查看指定频道配置
    """
    sender_id = event.sender_id
    command = event.text
    logger.info(f"收到命令: {command}，发送者: {sender_id}")

    # 权限检查
    if sender_id not in ADMIN_LIST and ADMIN_LIST != ["me"]:
        logger.warning(f"发送者 {sender_id} 没有权限执行命令 {command}")
        await event.reply(get_text("error.permission_denied"))
        return

    try:
        parts = command.split(maxsplit=1)
        channel = parts[1].strip() if len(parts) > 1 else None

        if channel:
            # 查看指定频道配置
            await _show_single_channel_config(event, channel)
        else:
            # 查看所有频道配置
            await _show_all_channels_config(event)

    except Exception as e:
        logger.error(f"查看评论区欢迎配置时出错: {type(e).__name__}: {e}", exc_info=True)
        await event.reply(get_text("comment_welcome.config.set_failed", error=str(e)))


async def _show_single_channel_config(event, channel: str):
    """显示单个频道的配置"""
    # 检查频道是否在监控列表中
    if channel not in CHANNELS:
        await event.reply(get_text("error.channel_not_found", channel=channel))
        return

    # 获取配置
    config = await get_channel_comment_welcome_config(channel)
    all_configs = await get_all_comment_welcome_configs()
    is_custom = channel in all_configs

    # 构建显示文本
    status_text = (
        get_text("comment_welcome.config.enabled")
        if config["enabled"]
        else get_text("comment_welcome.config.disabled")
    )

    # 判断是否使用默认值
    if not is_custom:
        note = f"\n💡 {get_text('comment_welcome.config.no_config')}"
    else:
        note = ""

    message = (
        f"{get_text('comment_welcome.config.title')}\n\n"
        f"{get_text('comment_welcome.config.channel', channel=channel)}\n"
        f"{get_text('comment_welcome.config.status')} {status_text}\n"
        f"{get_text('comment_welcome.config.message')} {config['welcome_message']}\n"
        f"{get_text('comment_welcome.config.button')} {config['button_text']}\n"
        f"{get_text('comment_welcome.config.action')} {config['button_action']}"
        f"{note}"
    )

    await event.reply(message)


async def _show_all_channels_config(event):
    """显示所有频道的配置"""
    all_configs = await get_all_comment_welcome_configs()

    if not CHANNELS:
        await event.reply(get_text("error.no_channels"))
        return

    message = f"{get_text('comment_welcome.config.all_title')}\n\n"

    for channel in CHANNELS:
        config = await get_channel_comment_welcome_config(channel)
        is_custom = channel in all_configs

        status_text = (
            get_text("comment_welcome.config.enabled")
            if config["enabled"]
            else get_text("comment_welcome.config.disabled")
        )

        message += f"📌 {channel}\n  {get_text('comment_welcome.config.status')} {status_text}\n"

        if is_custom:
            message += (
                f"  {get_text('comment_welcome.config.message')} {config['welcome_message']}\n"
                f"  {get_text('comment_welcome.config.button')} {config['button_text']}\n"
                f"  {get_text('comment_welcome.config.action')} {config['button_action']}\n"
            )
        else:
            message += f"  💡 {get_text('comment_welcome.config.no_config')}\n"

        message += "\n"

    await event.reply(message.strip())


async def handle_set_comment_welcome(event):
    """
    处理/setcommentwelcome命令，设置评论区欢迎配置

    用法：
    /setcommentwelcome <channel> <enabled> [welcome_message] [button_text]

    示例：
    /setcommentwelcome channel1 true
    /setcommentwelcome channel1 true "欢迎来到评论区" "申请总结"
    /setcommentwelcome channel1 false
    """
    sender_id = event.sender_id
    command = event.text
    logger.info(f"收到命令: {command}，发送者: {sender_id}")

    # 权限检查
    if sender_id not in ADMIN_LIST and ADMIN_LIST != ["me"]:
        logger.warning(f"发送者 {sender_id} 没有权限执行命令 {command}")
        await event.reply(get_text("error.permission_denied"))
        return

    try:
        import shlex

        # 使用 shlex.split() 正确解析带引号的参数
        try:
            tokens = shlex.split(command)
        except ValueError as e:
            await event.reply(
                f"❌ 参数解析错误：{str(e)}\n\n"
                f"💡 提示：如果参数包含空格，请使用双引号包裹\n"
                f'示例：/setcommentwelcome channel true "欢迎消息" "按钮文本"'
            )
            return

        # 检查参数数量
        if len(tokens) < 2:
            # 没有提供任何参数，显示完整帮助信息
            help_text = (
                f"{get_text('comment_welcome.config.title')}\n\n"
                f"📋 **{get_text('comment_welcome.config.usage')}**\n\n"
                f"**基本格式：**\n"
                f"`/setcommentwelcome <频道> <true|false>`\n\n"
                f"**完整格式：**\n"
                f"`/setcommentwelcome <频道> <true|false> [欢迎消息] [按钮文本]`\n\n"
                f"**示例：**\n"
            )

            # 为每个已配置的频道生成示例
            if CHANNELS:
                help_text += "• 启用（使用默认消息）：\n"
                for channel in CHANNELS[:3]:  # 最多显示3个频道
                    help_text += f"  `/setcommentwelcome {channel} true`\n"

                help_text += "\n• 禁用：\n"
                for channel in CHANNELS[:2]:  # 最多显示2个频道
                    help_text += f"  `/setcommentwelcome {channel} false`\n"

                help_text += "\n• 自定义消息和按钮：\n"
                if CHANNELS:
                    help_text += (
                        f'  `/setcommentwelcome {CHANNELS[0]} true "欢迎来到评论区" "申请总结"`\n'
                    )
            else:
                help_text += "  `/setcommentwelcome your_channel true`\n"
                help_text += '  `/setcommentwelcome your_channel true "欢迎" "申请总结"`\n'

            help_text += "\n💡 **提示：** 使用 `/showcommentwelcome` 查看当前配置"

            await event.reply(help_text)
            return

        if len(tokens) < 3:
            # 只有频道，没有启用状态
            await event.reply(
                f"❌ 请提供启用状态参数\n\n"
                f"用法：`/setcommentwelcome <频道> <true|false>`\n\n"
                f"示例：\n"
                f"`/setcommentwelcome {tokens[1]} true`  启用\n"
                f"`/setcommentwelcome {tokens[1]} false` 禁用"
            )
            return

        channel = tokens[1].strip()
        enabled_str = tokens[2].strip()

        # 检查频道是否在监控列表中
        if channel not in CHANNELS:
            await event.reply(get_text("error.channel_not_found", channel=channel))
            return

        # 解析 enabled 参数
        if enabled_str.lower() in ["true", "1", "yes", "on", "是", "启用"]:
            enabled = True
        elif enabled_str.lower() in ["false", "0", "no", "off", "否", "禁用"]:
            enabled = False
        else:
            await event.reply(
                get_text(
                    "comment_welcome.config.invalid_params",
                    usage=get_text("comment_welcome.config.usage"),
                )
            )
            return

        # 解析可选参数（欢迎消息和按钮文本）
        welcome_message = None
        button_text = None

        if len(tokens) >= 4:
            welcome_message = tokens[3]
        if len(tokens) >= 5:
            button_text = tokens[4]

        # 设置配置
        config = await set_channel_comment_welcome_config(
            channel_url=channel,
            enabled=enabled,
            welcome_message=welcome_message,
            button_text=button_text,
        )

        # 构建成功消息
        status_text = (
            get_text("comment_welcome.config.enabled")
            if config["enabled"]
            else get_text("comment_welcome.config.disabled")
        )

        message = (
            f"{get_text('comment_welcome.config.set_success', channel=channel)}\n\n"
            f"{get_text('comment_welcome.config.status')} {status_text}\n"
            f"{get_text('comment_welcome.config.message')} {config['welcome_message']}\n"
            f"{get_text('comment_welcome.config.button')} {config['button_text']}\n"
            f"{get_text('comment_welcome.config.action')} {config['button_action']}"
        )

        await event.reply(message)
        logger.info(f"已更新频道 {channel} 的评论区欢迎配置")

    except ValueError as e:
        # 参数验证失败
        await event.reply(f"❌ {str(e)}\n\n{get_text('comment_welcome.config.usage')}")
    except Exception as e:
        logger.error(f"设置评论区欢迎配置时出错: {type(e).__name__}: {e}", exc_info=True)
        await event.reply(get_text("comment_welcome.config.set_failed", error=str(e)))


async def handle_delete_comment_welcome(event):
    """
    处理/deletecommentwelcome命令，删除评论区欢迎配置

    用法：
    /deletecommentwelcome <channel>

    说明：删除配置后，该频道将使用默认配置
    """
    sender_id = event.sender_id
    command = event.text
    logger.info(f"收到命令: {command}，发送者: {sender_id}")

    # 权限检查
    if sender_id not in ADMIN_LIST and ADMIN_LIST != ["me"]:
        logger.warning(f"发送者 {sender_id} 没有权限执行命令 {command}")
        await event.reply(get_text("error.permission_denied"))
        return

    try:
        parts = command.split(maxsplit=1)

        if len(parts) < 2:
            await event.reply(
                "❌ 请提供频道参数\n\n用法：/deletecommentwelcome <channel>\n\n示例：/deletecommentwelcome channel1"
            )
            return

        channel = parts[1].strip()

        # 检查频道是否在监控列表中
        if channel not in CHANNELS:
            await event.reply(get_text("error.channel_not_found", channel=channel))
            return

        # 删除配置
        deleted = await delete_channel_comment_welcome_config(channel)

        if deleted:
            # 获取默认配置
            default_config = get_default_comment_welcome_config()
            status_text = (
                get_text("comment_welcome.config.enabled")
                if default_config["enabled"]
                else get_text("comment_welcome.config.disabled")
            )

            message = (
                f"{get_text('comment_welcome.config.delete_success', channel=channel)}\n\n"
                f"{get_text('comment_welcome.config.status')} {status_text}\n"
                f"{get_text('comment_welcome.config.message')} {default_config['welcome_message']}\n"
                f"{get_text('comment_welcome.config.button')} {default_config['button_text']}"
            )

            await event.reply(message)
            logger.info(f"已删除频道 {channel} 的评论区欢迎配置")
        else:
            # 没有找到配置
            await event.reply(f"ℹ️ 频道 {channel} 没有自定义配置，无需删除")

    except Exception as e:
        logger.error(f"删除评论区欢迎配置时出错: {type(e).__name__}: {e}", exc_info=True)
        await event.reply(get_text("comment_welcome.config.delete_failed", error=str(e)))
