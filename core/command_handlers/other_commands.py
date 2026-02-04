"""
其他命令处理（系统、调度、投票、数据管理、UI命令）
"""

import logging
import sys
import subprocess
import os
from datetime import datetime, timezone, timedelta

from ..config import (
    ADMIN_LIST, CHANNELS, SEND_REPORT_TO_SOURCE,
    RESTART_FLAG_FILE, load_config, save_config, logger,
    get_channel_schedule, set_channel_schedule, set_channel_schedule_v2,
    delete_channel_schedule, validate_schedule,
    get_channel_poll_config, set_channel_poll_config, delete_channel_poll_config,
    get_bot_state, set_bot_state, BOT_STATE_RUNNING, BOT_STATE_PAUSED,
    BOT_STATE_SHUTTING_DOWN, LOG_LEVEL_MAP, get_scheduler_instance,
    clear_discussion_group_cache, LINKED_CHAT_CACHE
)
from ..utils.message_utils import format_schedule_info

logger = logging.getLogger(__name__)


# ==================== 系统控制命令 ====================

async def handle_show_log_level(event):
    """处理/showloglevel命令，显示当前日志级别"""
    sender_id = event.sender_id
    command = event.text
    logger.info(f"收到命令: {command}，发送者: {sender_id}")
    
    if sender_id not in ADMIN_LIST and ADMIN_LIST != ['me']:
        logger.warning(f"发送者 {sender_id} 没有权限执行命令 {command}")
        await event.reply("您没有权限执行此命令")
        return
    
    import logging
    root_logger = logging.getLogger()
    current_level = root_logger.getEffectiveLevel()
    level_name = logging.getLevelName(current_level)
    
    logger.info(f"执行命令 {command} 成功")
    await event.reply(f"当前日志级别：{level_name}\n\n可用日志级别：DEBUG, INFO, WARNING, ERROR, CRITICAL")


async def handle_set_log_level(event):
    """处理/setloglevel命令，设置日志级别"""
    sender_id = event.sender_id
    command = event.text
    logger.info(f"收到命令: {command}，发送者: {sender_id}")
    
    if sender_id not in ADMIN_LIST and ADMIN_LIST != ['me']:
        logger.warning(f"发送者 {sender_id} 没有权限执行命令 {command}")
        await event.reply("您没有权限执行此命令")
        return
    
    try:
        _, level_str = command.split(maxsplit=1)
        level_str = level_str.strip().upper()
        
        if level_str not in LOG_LEVEL_MAP:
            await event.reply(f"无效的日志级别: {level_str}\n\n可用日志级别：DEBUG, INFO, WARNING, ERROR, CRITICAL")
            return
        
        import logging
        root_logger = logging.getLogger()
        old_level = root_logger.getEffectiveLevel()
        new_level = LOG_LEVEL_MAP[level_str]
        root_logger.setLevel(new_level)
        
        config = load_config()
        config['log_level'] = level_str
        save_config(config)
        
        logger.info(f"日志级别已从 {logging.getLevelName(old_level)} 更改为 {logging.getLevelName(new_level)}")
        await event.reply(f"日志级别已成功更改为：{level_str}\n\n之前的级别：{logging.getLevelName(old_level)}")
        
    except ValueError:
        await event.reply("请提供有效的日志级别，例如：/setloglevel INFO\n\n可用日志级别：DEBUG, INFO, WARNING, ERROR, CRITICAL")
    except Exception as e:
        logger.error(f"设置日志级别时出错: {type(e).__name__}: {e}", exc_info=True)
        await event.reply(f"设置日志级别时出错: {e}")


async def handle_restart(event):
    """处理/restart命令，重启机器人"""
    sender_id = event.sender_id
    command = event.text
    logger.info(f"收到命令: {command}，发送者: {sender_id}")
    
    if sender_id not in ADMIN_LIST and ADMIN_LIST != ['me']:
        logger.warning(f"发送者 {sender_id} 没有权限执行命令 {command}")
        await event.reply("您没有权限执行此命令")
        return
    
    logger.info(f"开始执行 {command} 命令")
    await event.reply("正在重启机器人...")
    logger.info("机器人重启命令已执行，正在重启...")
    
    with open(RESTART_FLAG_FILE, 'w') as f:
        f.write(str(sender_id))
    
    python = sys.executable
    subprocess.Popen([python] + sys.argv)
    sys.exit(0)


async def handle_shutdown(event):
    """处理/shutdown命令，彻底停止机器人"""
    sender_id = event.sender_id
    command = event.text
    logger.info(f"收到命令: {command}，发送者: {sender_id}")
    
    if sender_id not in ADMIN_LIST and ADMIN_LIST != ['me']:
        logger.warning(f"发送者 {sender_id} 没有权限执行命令 {command}")
        await event.reply("您没有权限执行此命令")
        return
    
    logger.info(f"开始执行 {command} 命令")
    await event.reply("正在关闭机器人...")
    
    set_bot_state(BOT_STATE_SHUTTING_DOWN)
    
    scheduler = get_scheduler_instance()
    if scheduler:
        scheduler.shutdown(wait=False)
        logger.info("调度器已停止")
    
    logger.info("机器人关机命令已执行，正在关闭...")
    
    try:
        for admin_id in ADMIN_LIST:
            if admin_id != 'me':
                await event.client.send_message(admin_id, "机器人已执行关机命令，正在停止运行...", link_preview=False)
    except Exception as e:
        logger.error(f"发送关机通知失败: {e}")
    
    import time
    time.sleep(1)
    sys.exit(0)


async def handle_pause(event):
    """处理/pause命令，暂停所有定时任务"""
    sender_id = event.sender_id
    command = event.text
    logger.info(f"收到命令: {command}，发送者: {sender_id}")
    
    if sender_id not in ADMIN_LIST and ADMIN_LIST != ['me']:
        logger.warning(f"发送者 {sender_id} 没有权限执行命令 {command}")
        await event.reply("您没有权限执行此命令")
        return
    
    current_state = get_bot_state()
    
    if current_state == BOT_STATE_PAUSED:
        await event.reply("机器人已经处于暂停状态")
        return
    
    if current_state != BOT_STATE_RUNNING:
        await event.reply(f"机器人当前状态为 {current_state}，无法暂停")
        return
    
    scheduler = get_scheduler_instance()
    if scheduler:
        scheduler.pause()
        logger.info("调度器已暂停")
    
    set_bot_state(BOT_STATE_PAUSED)
    
    logger.info(f"执行命令 {command} 成功")
    await event.reply("机器人已暂停。定时任务已停止，但手动命令仍可执行。\n使用 /resume 或 /恢复 恢复运行。")


async def handle_resume(event):
    """处理/resume命令，恢复所有定时任务"""
    sender_id = event.sender_id
    command = event.text
    logger.info(f"收到命令: {command}，发送者: {sender_id}")

    if sender_id not in ADMIN_LIST and ADMIN_LIST != ['me']:
        logger.warning(f"发送者 {sender_id} 没有权限执行命令 {command}")
        await event.reply("您没有权限执行此命令")
        return

    current_state = get_bot_state()

    if current_state == BOT_STATE_RUNNING:
        await event.reply("机器人已经在运行状态")
        return

    if current_state != BOT_STATE_PAUSED:
        await event.reply(f"机器人当前状态为 {current_state}，无法恢复")
        return

    scheduler = get_scheduler_instance()
    if scheduler:
        scheduler.resume()
        logger.info("调度器已恢复")

    set_bot_state(BOT_STATE_RUNNING)

    logger.info(f"执行命令 {command} 成功")
    await event.reply("机器人已恢复运行。定时任务将继续执行。")


# ==================== 调度配置命令 ====================

async def handle_show_channel_schedule(event):
    """处理/showchannelschedule命令，查看指定频道的自动总结时间配置"""
    sender_id = event.sender_id
    command = event.text
    logger.info(f"收到命令: {command}，发送者: {sender_id}")
    
    if sender_id not in ADMIN_LIST and ADMIN_LIST != ['me']:
        logger.warning(f"发送者 {sender_id} 没有权限执行命令 {command}")
        await event.reply("您没有权限执行此命令")
        return
    
    try:
        parts = command.split()
        if len(parts) > 1:
            channel_part = parts[1]
            if channel_part.startswith('http'):
                channel = channel_part
            else:
                channel = f"https://t.me/{channel_part}"
            
            if channel not in CHANNELS:
                await event.reply(f"频道 {channel} 不在配置列表中")
                return
        else:
            if not CHANNELS:
                await event.reply("当前没有配置任何频道")
                return
            
            schedule_msg = "所有频道的自动总结时间配置：\n\n"
            for i, ch in enumerate(CHANNELS, 1):
                schedule = get_channel_schedule(ch)
                schedule_msg += format_schedule_info(ch, schedule, i)

            await event.reply(schedule_msg)
            return
        
        schedule = get_channel_schedule(channel)

        schedule_info = format_schedule_info(channel, schedule)
        schedule_info += f"\n使用格式：\n"
        schedule_info += f"每天模式：/setchannelschedule {channel.split('/')[-1]} daily 23 0\n"
        schedule_info += f"每周模式：/setchannelschedule {channel.split('/')[-1]} weekly mon,thu 14 30\n"
        schedule_info += f"旧格式：/setchannelschedule {channel.split('/')[-1]} mon 9 0"

        logger.info(f"执行命令 {command} 成功")
        await event.reply(schedule_info)
        
    except Exception as e:
        logger.error(f"查看频道时间配置时出错: {type(e).__name__}: {e}", exc_info=True)
        await event.reply(f"查看频道时间配置时出错: {e}")


async def handle_set_channel_schedule(event):
    """处理/setchannelschedule命令，设置指定频道的自动总结时间（支持新格式）"""
    sender_id = event.sender_id
    command = event.text
    logger.info(f"收到命令: {command}，发送者: {sender_id}")

    if sender_id not in ADMIN_LIST and ADMIN_LIST != ['me']:
        logger.warning(f"发送者 {sender_id} 没有权限执行命令 {command}")
        await event.reply("您没有权限执行此命令")
        return

    try:
        parts = command.split()
        if len(parts) < 4:
            await event.reply(
                "请提供完整的参数。可用格式：\n\n"
                "每天模式：/setchannelschedule <频道> daily <小时> <分钟>\n"
                "  例如：/setchannelschedule channel daily 23 0\n\n"
                "每周模式：/setchannelschedule <频道> weekly <星期> <小时> <分钟>\n"
                "  例如：/setchannelschedule channel weekly mon,thu 23 0\n"
                "  例如：/setchannelschedule channel weekly sun 9 0\n\n"
                "旧格式（向后兼容）：/setchannelschedule <频道> <星期> <小时> <分钟>\n"
                "  例如：/setchannelschedule channel mon 9 0"
            )
            return

        channel_part = parts[1]
        if channel_part.startswith('http'):
            channel = channel_part
        else:
            channel = f"https://t.me/{channel_part}"

        if channel not in CHANNELS:
            await event.reply(f"频道 {channel} 不在配置列表中，请先使用/addchannel命令添加频道")
            return

        frequency_or_day = parts[2].lower()

        if frequency_or_day in ['daily', 'weekly']:
            frequency = frequency_or_day

            if frequency == 'daily':
                if len(parts) < 5:
                    await event.reply("每天模式需要提供小时和分钟：/setchannelschedule channel daily 23 0")
                    return

                try:
                    hour = int(parts[3])
                    minute = int(parts[4])
                except ValueError:
                    await event.reply("小时和分钟必须是数字")
                    return

                success = set_channel_schedule_v2(channel, frequency='daily', hour=hour, minute=minute)

                if success:
                    success_msg = f"已成功设置频道 {channel.split('/')[-1]} 的自动总结时间：\n\n"
                    success_msg += f"• 频率：每天\n"
                    success_msg += f"• 时间：{hour:02d}:{minute:02d}\n"
                    success_msg += f"\n下次自动总结将在每天 {hour:02d}:{minute:02d} 执行。"
                    await event.reply(success_msg)
                else:
                    await event.reply("设置失败，请检查日志")

            elif frequency == 'weekly':
                if len(parts) < 6:
                    await event.reply("每周模式需要提供星期、小时和分钟：/setchannelschedule channel weekly mon,thu 23 0")
                    return

                days_str = parts[3]
                try:
                    hour = int(parts[4])
                    minute = int(parts[5])
                except ValueError:
                    await event.reply("小时和分钟必须是数字")
                    return

                days = [d.strip() for d in days_str.split(',')]

                success = set_channel_schedule_v2(channel, frequency='weekly', days=days, hour=hour, minute=minute)

                if success:
                    day_map = {
                        'mon': '周一', 'tue': '周二', 'wed': '周三', 'thu': '周四',
                        'fri': '周五', 'sat': '周六', 'sun': '周日'
                    }
                    days_cn = '、'.join([day_map.get(d, d) for d in days])

                    success_msg = f"已成功设置频道 {channel.split('/')[-1]} 的自动总结时间：\n\n"
                    success_msg += f"• 频率：每周\n"
                    success_msg += f"• 星期：{days_cn}\n"
                    success_msg += f"• 时间：{hour:02d}:{minute:02d}\n"
                    success_msg += f"\n下次自动总结将在每周{days_cn} {hour:02d}:{minute:02d} 执行。"
                    await event.reply(success_msg)
                else:
                    await event.reply("设置失败，请检查日志")
        else:
            day = frequency_or_day
            try:
                hour = int(parts[3])
                minute = int(parts[4]) if len(parts) > 4 else 0
            except ValueError:
                await event.reply("小时和分钟必须是数字")
                return

            is_valid, error_msg = validate_schedule(day, hour, minute)
            if not is_valid:
                await event.reply(error_msg)
                return

            success = set_channel_schedule(channel, day=day, hour=hour, minute=minute)

            if success:
                day_map = {
                    'mon': '周一', 'tue': '周二', 'wed': '周三', 'thu': '周四',
                    'fri': '周五', 'sat': '周六', 'sun': '周日'
                }
                day_cn = day_map.get(day, day)

                success_msg = f"已成功设置频道 {channel.split('/')[-1]} 的自动总结时间：\n\n"
                success_msg += f"• 星期几：{day_cn} ({day})\n"
                success_msg += f"• 时间：{hour:02d}:{minute:02d}\n"
                success_msg += f"\n下次自动总结将在每周{day_cn} {hour:02d}:{minute:02d}执行。"
                await event.reply(success_msg)
            else:
                await event.reply("设置频道时间配置失败，请检查日志")

    except Exception as e:
        logger.error(f"设置频道时间配置时出错: {type(e).__name__}: {e}", exc_info=True)
        await event.reply(f"设置频道时间配置时出错: {e}")


async def handle_delete_channel_schedule(event):
    """处理/deletechannelschedule命令，删除指定频道的自动总结时间配置"""
    sender_id = event.sender_id
    command = event.text
    logger.info(f"收到命令: {command}，发送者: {sender_id}")
    
    if sender_id not in ADMIN_LIST and ADMIN_LIST != ['me']:
        logger.warning(f"发送者 {sender_id} 没有权限执行命令 {command}")
        await event.reply("您没有权限执行此命令")
        return
    
    try:
        parts = command.split()
        if len(parts) < 2:
            await event.reply("请提供频道参数：/deletechannelschedule 频道\n\n例如：/deletechannelschedule examplechannel")
            return
        
        channel_part = parts[1]
        if channel_part.startswith('http'):
            channel = channel_part
        else:
            channel = f"https://t.me/{channel_part}"
        
        if channel not in CHANNELS:
            await event.reply(f"频道 {channel} 不在配置列表中")
            return
        
        success = delete_channel_schedule(channel)
        
        if success:
            success_msg = f"已成功删除频道 {channel.split('/')[-1]} 的自动总结时间配置。\n"
            success_msg += f"该频道将使用默认时间配置：每周一 09:00"
            
            logger.info(f"已删除频道 {channel} 的时间配置")
            await event.reply(success_msg)
        else:
            await event.reply("删除频道时间配置失败，请检查日志")
            
    except Exception as e:
        logger.error(f"删除频道时间配置时出错: {type(e).__name__}: {e}", exc_info=True)
        await event.reply(f"删除频道时间配置时出错: {e}")


# ==================== 投票配置命令 ====================

async def handle_show_channel_poll(event):
    """处理/channelpoll命令，查看指定频道的投票配置"""
    sender_id = event.sender_id
    command = event.text
    logger.info(f"收到命令: {command}，发送者: {sender_id}")

    if sender_id not in ADMIN_LIST and ADMIN_LIST != ['me']:
        logger.warning(f"发送者 {sender_id} 没有权限执行命令 {command}")
        await event.reply("您没有权限执行此命令")
        return

    try:
        parts = command.split()
        if len(parts) > 1:
            channel_part = parts[1]
            if channel_part.startswith('http'):
                channel = channel_part
            else:
                channel = f"https://t.me/{channel_part}"

            if channel not in CHANNELS:
                await event.reply(f"频道 {channel} 不在配置列表中")
                return

            poll_config = get_channel_poll_config(channel)

            channel_name = channel.split('/')[-1]
            enabled = poll_config['enabled']
            send_to_channel = poll_config['send_to_channel']

            if enabled is None:
                enabled_text = "使用全局配置"
            else:
                enabled_text = "启用" if enabled else "禁用"

            location_text = "频道" if send_to_channel else "讨论组"

            poll_info = f"频道 {channel_name} 的投票配置：\n\n"
            poll_info += f"• 状态：{enabled_text}\n"
            poll_info += f"• 发送位置：{location_text}\n\n"

            poll_info += f"使用格式：\n"
            poll_info += f"/setchannelpoll {channel_name} true|false channel|discussion\n"
            poll_info += f"/deletechannelpoll {channel_name}"

            logger.info(f"执行命令 {command} 成功")
            await event.reply(poll_info)
        else:
            if not CHANNELS:
                await event.reply("当前没有配置任何频道")
                return

            poll_info = "所有频道的投票配置：\n\n"
            for i, ch in enumerate(CHANNELS, 1):
                poll_config = get_channel_poll_config(ch)
                channel_name = ch.split('/')[-1]

                enabled = poll_config['enabled']
                send_to_channel = poll_config['send_to_channel']

                if enabled is None:
                    enabled_text = "全局"
                else:
                    enabled_text = "启用" if enabled else "禁用"

                location_text = "频道" if send_to_channel else "讨论组"

                poll_info += f"{i}. {channel_name}: {enabled_text} / {location_text}\n"

            await event.reply(poll_info)

    except Exception as e:
        logger.error(f"查看频道投票配置时出错: {type(e).__name__}: {e}", exc_info=True)
        await event.reply(f"查看频道投票配置时出错: {e}")


async def handle_set_channel_poll(event):
    """处理/setchannelpoll命令，设置指定频道的投票配置"""
    sender_id = event.sender_id
    command = event.text
    logger.info(f"收到命令: {command}，发送者: {sender_id}")

    if sender_id not in ADMIN_LIST and ADMIN_LIST != ['me']:
        logger.warning(f"发送者 {sender_id} 没有权限执行命令 {command}")
        await event.reply("您没有权限执行此命令")
        return

    try:
        parts = command.split()
        if len(parts) < 4:
            await event.reply(
                "请提供完整的参数。可用格式：\n\n"
                "/setchannelpoll <频道> <enabled> <location>\n\n"
                "参数说明：\n"
                "• 频道：频道URL或名称\n"
                "• enabled：true（启用）或 false（禁用）\n"
                "• location：channel（频道）或 discussion（讨论组）\n\n"
                "示例：\n"
                "/setchannelpoll channel1 true channel\n"
                "/setchannelpoll channel1 false discussion\n"
                "/setchannelpoll channel1 false channel"
            )
            return

        channel_part = parts[1]
        if channel_part.startswith('http'):
            channel = channel_part
        else:
            channel = f"https://t.me/{channel_part}"

        if channel not in CHANNELS:
            await event.reply(f"频道 {channel} 不在配置列表中，请先使用/addchannel命令添加频道")
            return

        enabled_str = parts[2].lower()
        if enabled_str in ['true', '1', 'yes']:
            enabled = True
        elif enabled_str in ['false', '0', 'no']:
            enabled = False
        else:
            await event.reply(f"无效的enabled参数: {enabled_str}\n\n有效值：true, false, 1, 0, yes, no")
            return

        location_str = parts[3].lower()
        if location_str in ['channel', 'c']:
            send_to_channel = True
        elif location_str in ['discussion', 'd', 'discuss']:
            send_to_channel = False
        else:
            await event.reply(f"无效的location参数: {location_str}\n\n有效值：channel, discussion")
            return

        success = set_channel_poll_config(channel, enabled=enabled, send_to_channel=send_to_channel)

        if success:
            channel_name = channel.split('/')[-1]
            enabled_text = "启用" if enabled else "禁用"
            location_text = "频道" if send_to_channel else "讨论组"

            success_msg = f"已成功设置频道 {channel_name} 的投票配置：\n\n"
            success_msg += f"• 状态：{enabled_text}\n"
            success_msg += f"• 发送位置：{location_text}\n"

            if not enabled:
                success_msg += f"\n注意：投票功能已禁用，不会发送投票。"
            elif send_to_channel:
                success_msg += f"\n注意：投票将直接发送到频道，回复总结消息。"
            else:
                success_msg += f"\n注意：投票将发送到讨论组，回复转发消息。"

            await event.reply(success_msg)
        else:
            await event.reply("设置失败，请检查日志")

    except Exception as e:
        logger.error(f"设置频道投票配置时出错: {type(e).__name__}: {e}", exc_info=True)
        await event.reply(f"设置频道投票配置时出错: {e}")


async def handle_delete_channel_poll(event):
    """处理/deletechannelpoll命令，删除指定频道的投票配置"""
    sender_id = event.sender_id
    command = event.text
    logger.info(f"收到命令: {command}，发送者: {sender_id}")

    if sender_id not in ADMIN_LIST and ADMIN_LIST != ['me']:
        logger.warning(f"发送者 {sender_id} 没有权限执行命令 {command}")
        await event.reply("您没有权限执行此命令")
        return

    try:
        parts = command.split()
        if len(parts) < 2:
            await event.reply("请提供频道参数：/deletechannelpoll 频道\n\n例如：/deletechannelpoll examplechannel")
            return

        channel_part = parts[1]
        if channel_part.startswith('http'):
            channel = channel_part
        else:
            channel = f"https://t.me/{channel_part}"

        if channel not in CHANNELS:
            await event.reply(f"频道 {channel} 不在配置列表中")
            return

        success = delete_channel_poll_config(channel)

        if success:
            channel_name = channel.split('/')[-1]
            from .config import ENABLE_POLL
            global_enabled = "启用" if ENABLE_POLL else "禁用"
            
            success_msg = f"已成功删除频道 {channel_name} 的投票配置。\n\n"
            success_msg += f"该频道将使用全局投票配置：\n"
            success_msg += f"• 状态：{global_enabled}\n"
            success_msg += f"• 发送位置：讨论组（默认）"

            logger.info(f"已删除频道 {channel} 的投票配置")
            await event.reply(success_msg)
        else:
            await event.reply("删除频道投票配置失败，请检查日志")

    except Exception as e:
        logger.error(f"删除频道投票配置时出错: {type(e).__name__}: {e}", exc_info=True)
        await event.reply(f"删除频道投票配置时出错: {e}")


# ==================== 数据管理命令 ====================

async def handle_set_send_to_source(event):
    """处理/setsendtosource命令，设置是否将报告发送回源频道"""
    sender_id = event.sender_id
    command = event.text
    logger.info(f"收到命令: {command}，发送者: {sender_id}")
    
    if sender_id not in ADMIN_LIST and ADMIN_LIST != ['me']:
        logger.warning(f"发送者 {sender_id} 没有权限执行命令 {command}")
        await event.reply("您没有权限执行此命令")
        return
    
    try:
        _, value = command.split(maxsplit=1)
        value = value.strip().lower()
        
        if value not in ['true', 'false', '1', '0', 'yes', 'no']:
            await event.reply(f"无效的值: {value}\n\n可用值：true, false, 1, 0, yes, no")
            return
        
        new_value = value in ['true', '1', 'yes']
        
        config = load_config()
        config['send_report_to_source'] = new_value
        save_config(config)
        
        logger.info(f"已将send_report_to_source设置为: {new_value}")
        await event.reply(f"已成功将报告发送回源频道的设置更改为：{new_value}\n\n当前状态：{'开启' if new_value else '关闭'}")
        
    except ValueError:
        current_value = load_config().get('send_report_to_source', SEND_REPORT_TO_SOURCE)
        await event.reply(f"当前报告发送回源频道的设置：{current_value}\n\n当前状态：{'开启' if current_value else '关闭'}\n\n使用格式：/setsendtosource true|false")
    except Exception as e:
        logger.error(f"设置报告发送回源频道选项时出错: {type(e).__name__}: {e}", exc_info=True)
        await event.reply(f"设置报告发送回源频道选项时出错: {e}")


async def handle_clear_cache(event):
    """处理/clearcache命令，清除讨论组ID缓存"""
    sender_id = event.sender_id
    command = event.text

    if sender_id not in ADMIN_LIST and ADMIN_LIST != ['me']:
        logger.warning(f"用户 {sender_id} 尝试使用 /clearcache 命令，但没有管理员权限")
        await event.reply("❌ 只有管理员可以清除缓存")
        return

    logger.info(f"收到 /clearcache 命令，发送者: {sender_id}")

    try:
        parts = command.split()
        if len(parts) > 1:
            channel = parts[1]
            clear_discussion_group_cache(channel)
            await event.reply(f"✅ 已清除频道 {channel} 的讨论组ID缓存")
            logger.info(f"管理员 {sender_id} 清除了频道 {channel} 的讨论组ID缓存")
        else:
            cache_size = len(LINKED_CHAT_CACHE)
            clear_discussion_group_cache()
            await event.reply(f"✅ 已清除所有讨论组ID缓存（共 {cache_size} 条）")
            logger.info(f"管理员 {sender_id} 清除了所有讨论组ID缓存（共 {cache_size} 条）")

    except Exception as e:
        logger.error(f"清除缓存时出错: {type(e).__name__}: {e}", exc_info=True)
        await event.reply(f"❌ 清除缓存时出错: {e}")


# ==================== UI命令 ====================

async def handle_start(event):
    """处理/start命令，显示欢迎消息和帮助信息"""
    sender_id = event.sender_id
    command = event.text
    logger.info(f"收到命令: {command}，发送者: {sender_id}")

    try:
        welcome_message = """🌸 **欢迎使用 Sakura-频道总结助手**

🤖 我是Telegram智能频道管理助手，专门帮助频道主自动化管理 Telegram 频道内容。

✨ **主要功能**
• 📊 AI智能总结频道消息
• ⏰ 支持每天/每周自动总结
• 🎯 自定义总结风格和频率
• 📝 自动生成投票互动
• 👥 多频道同时管理
• 📜 历史总结记录与查询

📚 **常用命令**

**基础命令**
/start - 查看此欢迎消息
/summary - 立即生成本周汇总

**配置命令**
/showchannels - 查看频道列表
/addchannel - 添加监控频道
/setchannelschedule - 设置自动总结时间

**历史记录** (新功能)
/history - 查看历史总结
/export - 导出历史记录
/stats - 查看统计数据

**管理命令**
/pause - 暂停定时任务
/resume - 恢复定时任务
/changelog - 查看更新日志

💡 **提示**
• 发送 /help 查看完整命令列表
• 更多信息请访问项目[开源仓库](https://github.com/Sakura520222/Sakura-Channel-Summary-Assistant)"""

        await event.reply(welcome_message, link_preview=False)
        logger.info(f"已向用户 {sender_id} 发送欢迎消息")

    except Exception as e:
        logger.error(f"发送欢迎消息时出错: {type(e).__name__}: {e}", exc_info=True)
        await event.reply(f"发送欢迎消息时出错: {e}")


async def handle_help(event):
    """处理/help命令，显示完整命令列表和使用说明"""
    sender_id = event.sender_id
    command = event.text
    logger.info(f"收到命令: {command}，发送者: {sender_id}")

    try:
        help_message = """📚 **Sakura-频道总结助手 - 完整命令列表**

**🤖 基础命令**
/start - 查看欢迎消息和基本介绍
/help - 查看此完整命令列表
/summary - 立即生成本周频道消息汇总
/changelog - 查看项目更新日志

**⚙️ 提示词管理**
/showprompt - 查看当前使用的提示词
/setprompt - 设置自定义提示词
/showpollprompt - 查看当前投票提示词
/setpollprompt - 设置自定义投票提示词

**🤖 AI 配置**
/showaicfg - 查看当前 AI 配置信息
/setaicfg - 设置自定义 AI 配置（API Key、Base URL、Model）

**📊 日志管理**
/showloglevel - 查看当前日志级别
/setloglevel - 设置日志级别（DEBUG/INFO/WARNING/ERROR/CRITICAL）

**🔄 机器人控制**
/restart - 重启机器人
/shutdown - 彻底停止机器人
/pause - 暂停所有定时任务
/resume - 恢复所有定时任务

**📺 频道管理**
/showchannels - 查看当前监控的频道列表
/addchannel - 添加新频道到监控列表
• 示例：/addchannel https://t.me/examplechannel
/deletechannel - 从监控列表中删除频道
• 示例：/deletechannel https://t.me/examplechannel

**⏰ 时间配置**
/showchannelschedule - 查看频道自动总结时间配置
/setchannelschedule - 设置频道自动总结时间
• 每天：/setchannelschedule 频道 daily 小时 分钟
• 每周：/setchannelschedule 频道 weekly 星期,星期 小时 分钟
/deletechannelschedule - 删除频道自动总结时间配置

**🗑️ 数据管理**
/clearsummarytime - 清除上次总结时间记录

**📤 报告设置**
/setsendtosource - 设置是否将报告发送回源频道

**🗳️ 投票配置**
/channelpoll - 查看频道投票配置
/setchannelpoll - 设置频道投票配置
• 格式：/setchannelpoll 频道 true/false channel/discussion
/deletechannelpoll - 删除频道投票配置

**💾 缓存管理**
/clearcache - 清除讨论组ID缓存
• /clearcache - 清除所有缓存
• /clearcache 频道URL - 清除指定频道缓存

**📜 历史记录** (新功能)
/history - 查看历史总结
• /history - 查看所有频道最近10条
• /history channel1 - 查看指定频道
• /history channel1 30 - 查看最近30天

/export - 导出历史记录
• /export - 导出所有记录为JSON
• /export channel1 csv - 导出为CSV
• /export channel1 md - 导出为md

/stats - 查看统计数据
• /stats - 查看所有频道统计
• /stats channel1 - 查看指定频道统计

---
💡 **提示**
• 大多数命令支持中英文别名
• 配置类命令需要管理员权限
• 使用 /start 查看快速入门指南"""

        await event.reply(help_message, link_preview=False)
        logger.info(f"已向用户 {sender_id} 发送完整帮助信息")

    except Exception as e:
        logger.error(f"发送帮助信息时出错: {type(e).__name__}: {e}", exc_info=True)
        await event.reply(f"发送帮助信息时出错: {e}")


async def handle_changelog(event):
    """处理/changelog命令，直接发送变更日志文件"""
    sender_id = event.sender_id
    command = event.text
    logger.info(f"收到命令: {command}，发送者: {sender_id}")
    
    if sender_id not in ADMIN_LIST and ADMIN_LIST != ['me']:
        logger.warning(f"发送者 {sender_id} 没有权限执行命令 {command}")
        await event.reply("您没有权限执行此命令")
        return
    
    try:
        changelog_file = "CHANGELOG.md"
        
        if not os.path.exists(changelog_file):
            logger.error(f"更新日志文件 {changelog_file} 不存在")
            await event.reply(f"更新日志文件 {changelog_file} 不存在")
            return
        
        await event.client.send_file(
            sender_id,
            changelog_file,
            caption="📄 项目的完整变更日志文件",
            file_name="CHANGELOG.md"
        )
        
        logger.info(f"已向用户 {sender_id} 发送变更日志文件")
        
    except Exception as e:
        logger.error(f"发送变更日志文件时出错: {type(e).__name__}: {e}", exc_info=True)
        await event.reply(f"发送变更日志文件时出错: {e}")