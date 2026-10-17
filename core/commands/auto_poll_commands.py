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
自动趣味投票配置命令处理
"""

import core.config as config_module
from core.config import (
    ADMIN_LIST,
    delete_channel_auto_poll_config,
    get_channel_auto_poll_config,
    load_config,
    logger,
    save_config_changes,
    set_channel_auto_poll_config,
)
from core.i18n.i18n import get_text


async def handle_show_auto_poll(event):
    """处理 /showautopoll 命令，查看自动趣味投票配置

    用法：
    /showautopoll                       # 查看所有频道配置
    /showautopoll <channel>             # 查看指定频道配置
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
            await _show_single_channel_auto_poll(event, channel)
        else:
            await _show_all_channels_auto_poll(event)

    except Exception as e:
        logger.error(f"查看自动趣味投票配置时出错: {type(e).__name__}: {e}", exc_info=True)
        await event.reply(get_text("auto_poll.query_failed", error=str(e)))


async def _show_single_channel_auto_poll(event, channel: str):
    """显示单个频道的自动趣味投票配置"""
    if channel not in config_module.CHANNELS:
        await event.reply(get_text("error.channel_not_found", channel=channel))
        return

    auto_poll_config = get_channel_auto_poll_config(channel)
    channel_name = channel.split("/")[-1]
    enabled = auto_poll_config["enabled"]

    if not config_module.ENABLE_AUTO_POLL:
        status_text = get_text("auto_poll.status_global_disabled")
    elif enabled is None:
        # 使用全局配置
        status_text = get_text("auto_poll.status_global_enabled")
    else:
        status_text = (
            get_text("auto_poll.status_enabled")
            if enabled
            else get_text("auto_poll.status_disabled")
        )

    info = get_text("auto_poll.channel_title", channel=channel_name) + "\n\n"
    info += get_text("auto_poll.info", status=status_text) + "\n\n"
    info += get_text("auto_poll.usage_on", channel=channel_name) + "\n"
    info += get_text("auto_poll.usage_off", channel=channel_name) + "\n"
    info += get_text("auto_poll.usage_delete", channel=channel_name) + "\n"
    info += get_text("auto_poll.usage_global")

    await event.reply(info)


async def _show_all_channels_auto_poll(event):
    """显示所有频道的自动趣味投票配置"""
    if not config_module.CHANNELS:
        await event.reply(get_text("error.no_channels"))
        return

    global_status = (
        get_text("auto_poll.status_enabled")
        if config_module.ENABLE_AUTO_POLL
        else get_text("auto_poll.status_disabled")
    )

    info = get_text("auto_poll.all_title", global_status=global_status) + "\n\n"

    for i, ch in enumerate(config_module.CHANNELS, 1):
        auto_poll_config = get_channel_auto_poll_config(ch)
        channel_name = ch.split("/")[-1]
        enabled = auto_poll_config["enabled"]

        if not config_module.ENABLE_AUTO_POLL:
            status_text = get_text("auto_poll.status_global_disabled")
        elif enabled is None:
            status_text = get_text("auto_poll.status_global_enabled")
        else:
            status_text = (
                get_text("auto_poll.status_enabled")
                if enabled
                else get_text("auto_poll.status_disabled")
            )

        info += f"{i}. {channel_name}: {status_text}\n"

    await event.reply(info)


async def handle_set_auto_poll(event):
    """处理 /setautopoll 命令，设置自动趣味投票配置

    用法：
    /setautopoll on                     # 开启全局自动趣味投票
    /setautopoll off                    # 关闭全局自动趣味投票
    /setautopoll <channel> on           # 开启频道的自动趣味投票
    /setautopoll <channel> off          # 关闭频道的自动趣味投票
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
        parts = command.split()

        if len(parts) < 2:
            await event.reply(get_text("auto_poll.invalid_params"))
            return

        action = parts[1].lower()

        # /setautopoll on|off（无频道参数）→ 切换全局开关
        if action in ("on", "off") and len(parts) == 2:
            new_value = action == "on"
            config = load_config()
            config["enable_auto_poll"] = new_value
            save_config_changes(config)

            status = (
                get_text("auto_poll.status_enabled")
                if new_value
                else get_text("auto_poll.status_disabled")
            )
            await event.reply(get_text("auto_poll.global_set_success", status=status))
            logger.info(f"管理员 {sender_id} 已将全局自动趣味投票设置为: {new_value}")
            return

        # /setautopoll <channel> on|off → 切换频道级开关
        if len(parts) < 3:
            await event.reply(get_text("auto_poll.invalid_params"))
            return

        channel_part = parts[1]
        if channel_part.startswith("http"):
            channel = channel_part
        else:
            channel = f"https://t.me/{channel_part}"

        if channel not in config_module.CHANNELS:
            await event.reply(get_text("error.channel_not_found", channel=channel))
            return

        if not config_module.ENABLE_AUTO_POLL:
            await event.reply(get_text("auto_poll.global_required"))
            return

        action = parts[2].lower()

        if action == "on":
            success = set_channel_auto_poll_config(channel, enabled=True)
            if success:
                await event.reply(
                    get_text("auto_poll.enabled_channel", channel=channel.split("/")[-1])
                )
            else:
                await event.reply(get_text("auto_poll.set_failed"))
        elif action == "off":
            success = set_channel_auto_poll_config(channel, enabled=False)
            if success:
                await event.reply(
                    get_text("auto_poll.disabled_channel", channel=channel.split("/")[-1])
                )
            else:
                await event.reply(get_text("auto_poll.set_failed"))
        else:
            await event.reply(get_text("auto_poll.invalid_action"))

    except Exception as e:
        logger.error(f"设置自动趣味投票配置时出错: {type(e).__name__}: {e}", exc_info=True)
        await event.reply(get_text("auto_poll.set_failed"))


async def handle_delete_auto_poll(event):
    """处理 /deleteautopoll 命令，删除指定频道的自动趣味投票配置

    用法：
    /deleteautopoll <channel>          # 删除频道配置，恢复使用全局配置
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
        parts = command.split()
        if len(parts) < 2:
            await event.reply(get_text("auto_poll.invalid_params_delete"))
            return

        channel_part = parts[1]
        if channel_part.startswith("http"):
            channel = channel_part
        else:
            channel = f"https://t.me/{channel_part}"

        if channel not in config_module.CHANNELS:
            await event.reply(get_text("error.channel_not_found", channel=channel))
            return

        if not config_module.ENABLE_AUTO_POLL:
            await event.reply(get_text("auto_poll.global_required"))
            return

        success = delete_channel_auto_poll_config(channel)
        if success:
            await event.reply(get_text("auto_poll.deleted_channel", channel=channel.split("/")[-1]))
        else:
            await event.reply(get_text("auto_poll.set_failed"))

    except Exception as e:
        logger.error(f"删除自动趣味投票配置时出错: {type(e).__name__}: {e}", exc_info=True)
        await event.reply(get_text("auto_poll.set_failed"))
