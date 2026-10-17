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
频道管理命令处理
"""

import core.config as config_module
from core.config import ADMIN_LIST, load_config, logger, save_config_changes
from core.i18n.i18n import get_text


async def handle_show_channels(event):
    """处理/showchannels命令，查看当前频道列表"""
    sender_id = event.sender_id
    command = event.text
    logger.info(f"收到命令: {command}，发送者: {sender_id}")

    # 检查发送者是否为管理员
    if sender_id not in ADMIN_LIST and ADMIN_LIST != ["me"]:
        logger.warning(f"发送者 {sender_id} 没有权限执行命令 {command}")
        await event.reply(get_text("error.permission_denied"))
        return

    logger.info(f"执行命令 {command} 成功")

    if not config_module.CHANNELS:
        await event.reply(get_text("error.no_channels"))
        return

    # 构建频道列表消息
    channels_msg = f"{get_text('channel.list_title')}\n\n"
    for i, channel in enumerate(config_module.CHANNELS, 1):
        channels_msg += f"{i}. {channel}\n"

    await event.reply(channels_msg)


async def handle_add_channel(event):
    """处理/addchannel命令，添加频道"""
    sender_id = event.sender_id
    command = event.text
    logger.info(f"收到命令: {command}，发送者: {sender_id}")

    # 检查发送者是否为管理员
    if sender_id not in ADMIN_LIST and ADMIN_LIST != ["me"]:
        logger.warning(f"发送者 {sender_id} 没有权限执行命令 {command}")
        await event.reply(get_text("error.permission_denied"))
        return

    try:
        _, channel_url = command.split(maxsplit=1)
        channel_url = channel_url.strip()

        if not channel_url:
            await event.reply(get_text("channel.add_invalid_url"))
            return

        # 检查频道是否已存在
        if channel_url in config_module.CHANNELS:
            await event.reply(get_text("error.channel_exists", channel=channel_url))
            return

        # 更新配置文件
        config = load_config()
        channels = list(config.get("channels", config_module.CHANNELS))
        if channel_url not in channels:
            channels.append(channel_url)
        config["channels"] = channels
        save_config_changes(config)

        logger.info(f"已添加频道 {channel_url} 到列表")
        await event.reply(
            get_text("channel.add_success", channel=channel_url, count=len(config_module.CHANNELS))
        )

    except ValueError:
        # 没有提供频道URL
        await event.reply(get_text("channel.add_invalid_url"))
    except Exception as e:
        logger.error(f"添加频道时出错: {type(e).__name__}: {e}", exc_info=True)
        await event.reply(get_text("channel.add_failed", error=e))


async def handle_delete_channel(event):
    """处理/deletechannel命令，删除频道"""
    sender_id = event.sender_id
    command = event.text
    logger.info(f"收到命令: {command}，发送者: {sender_id}")

    # 检查发送者是否为管理员
    if sender_id not in ADMIN_LIST and ADMIN_LIST != ["me"]:
        logger.warning(f"发送者 {sender_id} 没有权限执行命令 {command}")
        await event.reply(get_text("error.permission_denied"))
        return

    try:
        _, channel_url = command.split(maxsplit=1)
        channel_url = channel_url.strip()

        if not channel_url:
            await event.reply(get_text("channel.add_invalid_url"))
            return

        # 检查频道是否存在
        if channel_url not in config_module.CHANNELS:
            await event.reply(get_text("error.channel_not_in_list", channel=channel_url))
            return

        # 更新配置文件
        config = load_config()
        channels = list(config.get("channels", config_module.CHANNELS))
        if channel_url in channels:
            channels.remove(channel_url)
        config["channels"] = channels
        save_config_changes(config)

        logger.info(f"已从列表中删除频道 {channel_url}")
        await event.reply(
            get_text(
                "channel.delete_success", channel=channel_url, count=len(config_module.CHANNELS)
            )
        )

    except ValueError:
        # 没有提供频道URL或频道不存在
        await event.reply(get_text("channel.add_invalid_url"))
    except Exception as e:
        logger.error(f"删除频道时出错: {type(e).__name__}: {e}", exc_info=True)
        await event.reply(get_text("channel.delete_failed", error=e))
