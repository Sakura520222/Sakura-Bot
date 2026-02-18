# -*- coding: utf-8 -*-
# Copyright 2026 Sakura-频道总结助手
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
    clear_discussion_group_cache, LINKED_CHAT_CACHE, ENABLE_POLL
)
from ..i18n import get_text, set_language, get_language, get_supported_languages
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
        await event.reply(get_text('error.permission_denied'))
        return
    
    import logging
    root_logger = logging.getLogger()
    current_level = root_logger.getEffectiveLevel()
    level_name = logging.getLevelName(current_level)
    
    logger.info(f"执行命令 {command} 成功")
    await event.reply(get_text('loglevel.current', level=level_name))


async def handle_set_log_level(event):
    """处理/setloglevel命令，设置日志级别"""
    sender_id = event.sender_id
    command = event.text
    logger.info(f"收到命令: {command}，发送者: {sender_id}")
    
    if sender_id not in ADMIN_LIST and ADMIN_LIST != ['me']:
        logger.warning(f"发送者 {sender_id} 没有权限执行命令 {command}")
        await event.reply(get_text('error.permission_denied'))
        return
    
    try:
        _, level_str = command.split(maxsplit=1)
        level_str = level_str.strip().upper()
        
        if level_str not in LOG_LEVEL_MAP:
            await event.reply(get_text('loglevel.invalid', level=level_str))
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
        await event.reply(get_text('loglevel.set_success', level=level_str, old_level=logging.getLevelName(old_level)))
        
    except ValueError:
        # 无参数时，显示当前日志级别和用法提示
        import logging as _logging
        root_logger = _logging.getLogger()
        current_level = _logging.getLevelName(root_logger.getEffectiveLevel())
        await event.reply(get_text('loglevel.current', level=current_level))
    except Exception as e:
        logger.error(f"设置日志级别时出错: {type(e).__name__}: {e}", exc_info=True)
        await event.reply(get_text('loglevel.set_error', error=e))


async def handle_restart(event):
    """处理/restart命令，重启机器人"""
    sender_id = event.sender_id
    command = event.text
    logger.info(f"收到命令: {command}，发送者: {sender_id}")
    
    if sender_id not in ADMIN_LIST and ADMIN_LIST != ['me']:
        logger.warning(f"发送者 {sender_id} 没有权限执行命令 {command}")
        await event.reply(get_text('error.permission_denied'))
        return
    
    logger.info(f"开始执行 {command} 命令")
    await event.reply(get_text('bot.restarting'))
    logger.info("机器人重启命令已执行，正在重启...")
    
    with open(RESTART_FLAG_FILE, 'w') as f:
        f.write(str(sender_id))
    
    # 重启前先停止问答Bot，新进程启动后会重新创建
    from core.process_manager import stop_qa_bot
    stop_qa_bot()
    
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
        await event.reply(get_text('error.permission_denied'))
        return
    
    logger.info(f"开始执行 {command} 命令")
    await event.reply(get_text('bot.shutting_down'))
    
    set_bot_state(BOT_STATE_SHUTTING_DOWN)
    
    scheduler = get_scheduler_instance()
    if scheduler:
        scheduler.shutdown(wait=False)
        logger.info("调度器已停止")
    
    logger.info("机器人关机命令已执行，正在关闭...")
    
    try:
        for admin_id in ADMIN_LIST:
            if admin_id != 'me':
                await event.client.send_message(admin_id, get_text('bot.shutting_down'), link_preview=False)
    except Exception as e:
        logger.error(f"发送关机通知失败: {e}")
    
    # 停止问答Bot
    from core.process_manager import stop_qa_bot
    stop_qa_bot()
    
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
        await event.reply(get_text('error.permission_denied'))
        return
    
    current_state = get_bot_state()
    
    if current_state == BOT_STATE_PAUSED:
        await event.reply(get_text('bot.already_paused'))
        return
    
    if current_state != BOT_STATE_RUNNING:
        await event.reply(get_text('bot.invalid_state_pause', state=current_state))
        return
    
    scheduler = get_scheduler_instance()
    if scheduler:
        scheduler.pause()
        logger.info("调度器已暂停")
    
    set_bot_state(BOT_STATE_PAUSED)
    
    logger.info(f"执行命令 {command} 成功")
    await event.reply(get_text('bot.paused'))


async def handle_resume(event):
    """处理/resume命令，恢复所有定时任务"""
    sender_id = event.sender_id
    command = event.text
    logger.info(f"收到命令: {command}，发送者: {sender_id}")

    if sender_id not in ADMIN_LIST and ADMIN_LIST != ['me']:
        logger.warning(f"发送者 {sender_id} 没有权限执行命令 {command}")
        await event.reply(get_text('error.permission_denied'))
        return

    current_state = get_bot_state()

    if current_state == BOT_STATE_RUNNING:
        await event.reply(get_text('bot.already_running'))
        return

    if current_state != BOT_STATE_PAUSED:
        await event.reply(get_text('bot.invalid_state_resume', state=current_state))
        return

    scheduler = get_scheduler_instance()
    if scheduler:
        scheduler.resume()
        logger.info("调度器已恢复")

    set_bot_state(BOT_STATE_RUNNING)

    logger.info(f"执行命令 {command} 成功")
    await event.reply(get_text('bot.resumed'))


# ==================== 调度配置命令 ====================

async def handle_show_channel_schedule(event):
    """处理/showchannelschedule命令，查看指定频道的自动总结时间配置"""
    sender_id = event.sender_id
    command = event.text
    logger.info(f"收到命令: {command}，发送者: {sender_id}")
    
    if sender_id not in ADMIN_LIST and ADMIN_LIST != ['me']:
        logger.warning(f"发送者 {sender_id} 没有权限执行命令 {command}")
        await event.reply(get_text('error.permission_denied'))
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
                await event.reply(get_text('error.channel_not_found', channel=channel))
                return
        else:
            if not CHANNELS:
                await event.reply(get_text('error.no_channels'))
                return
            
            schedule_msg = get_text('schedule.all_title') + "\n\n"
            for i, ch in enumerate(CHANNELS, 1):
                schedule = get_channel_schedule(ch)
                schedule_msg += format_schedule_info(ch, schedule, i)

            await event.reply(schedule_msg)
            return
        
        schedule = get_channel_schedule(channel)

        channel_name = channel.split('/')[-1]
        schedule_info = format_schedule_info(channel, schedule)
        schedule_info += get_text('schedule.format_header')
        schedule_info += get_text('schedule.usage_daily', channel=channel_name) + "\n"
        schedule_info += get_text('schedule.usage_weekly', channel=channel_name) + "\n"
        schedule_info += get_text('schedule.usage_old', channel=channel_name)

        logger.info(f"执行命令 {command} 成功")
        await event.reply(schedule_info)
        
    except Exception as e:
        logger.error(f"查看频道时间配置时出错: {type(e).__name__}: {e}", exc_info=True)
        await event.reply(get_text('error.unknown'))


async def handle_set_channel_schedule(event):
    """处理/setchannelschedule命令，设置指定频道的自动总结时间（支持新格式）"""
    sender_id = event.sender_id
    command = event.text
    logger.info(f"收到命令: {command}，发送者: {sender_id}")

    if sender_id not in ADMIN_LIST and ADMIN_LIST != ['me']:
        logger.warning(f"发送者 {sender_id} 没有权限执行命令 {command}")
        await event.reply(get_text('error.permission_denied'))
        return

    try:
        parts = command.split()
        if len(parts) < 4:
            await event.reply(get_text('schedule.invalid_params'))
            return

        channel_part = parts[1]
        if channel_part.startswith('http'):
            channel = channel_part
        else:
            channel = f"https://t.me/{channel_part}"

        if channel not in CHANNELS:
            await event.reply(get_text('error.channel_not_found', channel=channel))
            return

        frequency_or_day = parts[2].lower()

        if frequency_or_day in ['daily', 'weekly']:
            frequency = frequency_or_day

            if frequency == 'daily':
                if len(parts) < 5:
                    await event.reply(get_text('schedule.daily_need_time', channel=channel.split('/')[-1]))
                    return

                try:
                    hour = int(parts[3])
                    minute = int(parts[4])
                except ValueError:
                    await event.reply(get_text('schedule.invalid_time'))
                    return

                success = set_channel_schedule_v2(channel, frequency='daily', hour=hour, minute=minute)

                if success:
                    await event.reply(get_text('schedule.set_success',
                        channel=channel.split('/')[-1],
                        frequency=get_text('date.frequency.daily'),
                        hour=hour,
                        minute=minute))
                else:
                    await event.reply(get_text('schedule.set_failed'))

            elif frequency == 'weekly':
                if len(parts) < 6:
                    await event.reply(get_text('schedule.weekly_need_params'))
                    return

                days_str = parts[3]
                try:
                    hour = int(parts[4])
                    minute = int(parts[5])
                except ValueError:
                    await event.reply(get_text('schedule.invalid_time'))
                    return

                days = [d.strip() for d in days_str.split(',')]

                success = set_channel_schedule_v2(channel, frequency='weekly', days=days, hour=hour, minute=minute)

                if success:
                    day_map = {
                        'mon': get_text('day.mon'), 'tue': get_text('day.tue'), 
                        'wed': get_text('day.wed'), 'thu': get_text('day.thu'),
                        'fri': get_text('day.fri'), 'sat': get_text('day.sat'), 
                        'sun': get_text('day.sun')
                    }
                    days_cn = '、'.join([day_map.get(d, d) for d in days])

                    await event.reply(get_text('schedule.set_success_weekly',
                        channel=channel.split('/')[-1],
                        days=days_cn,
                        hour=hour,
                        minute=minute))
                else:
                    await event.reply(get_text('schedule.set_failed'))
        else:
            day = frequency_or_day
            try:
                hour = int(parts[3])
                minute = int(parts[4]) if len(parts) > 4 else 0
            except ValueError:
                await event.reply(get_text('schedule.invalid_time'))
                return

            is_valid, error_msg = validate_schedule(day, hour, minute)
            if not is_valid:
                await event.reply(error_msg)
                return

            success = set_channel_schedule(channel, day=day, hour=hour, minute=minute)

            if success:
                day_map = {
                    'mon': get_text('day.mon'), 'tue': get_text('day.tue'), 
                    'wed': get_text('day.wed'), 'thu': get_text('day.thu'),
                    'fri': get_text('day.fri'), 'sat': get_text('day.sat'), 
                    'sun': get_text('day.sun')
                }
                day_cn = day_map.get(day, day)

                await event.reply(get_text('schedule.set_success_old',
                    channel=channel.split('/')[-1],
                    day_cn=day_cn,
                    day=day,
                    hour=hour,
                    minute=minute))
            else:
                await event.reply(get_text('schedule.set_failed'))

    except Exception as e:
        logger.error(f"设置频道时间配置时出错: {type(e).__name__}: {e}", exc_info=True)
        await event.reply(get_text('error.unknown'))


async def handle_delete_channel_schedule(event):
    """处理/deletechannelschedule命令，删除指定频道的自动总结时间配置"""
    sender_id = event.sender_id
    command = event.text
    logger.info(f"收到命令: {command}，发送者: {sender_id}")
    
    if sender_id not in ADMIN_LIST and ADMIN_LIST != ['me']:
        logger.warning(f"发送者 {sender_id} 没有权限执行命令 {command}")
        await event.reply(get_text('error.permission_denied'))
        return
    
    try:
        parts = command.split()
        if len(parts) < 2:
            await event.reply(get_text('schedule.delete_channel_param'))
            return
        
        channel_part = parts[1]
        if channel_part.startswith('http'):
            channel = channel_part
        else:
            channel = f"https://t.me/{channel_part}"
        
        if channel not in CHANNELS:
            await event.reply(get_text('error.channel_not_found', channel=channel))
            return
        
        success = delete_channel_schedule(channel)
        
        if success:
            logger.info(f"已删除频道 {channel} 的时间配置")
            await event.reply(get_text('schedule.delete_success', channel=channel.split('/')[-1]))
        else:
            await event.reply(get_text('schedule.set_failed'))
            
    except Exception as e:
        logger.error(f"删除频道时间配置时出错: {type(e).__name__}: {e}", exc_info=True)
        await event.reply(get_text('schedule.delete_error', error=e))


# ==================== 投票配置命令 ====================

async def handle_show_channel_poll(event):
    """处理/channelpoll命令，查看指定频道的投票配置"""
    sender_id = event.sender_id
    command = event.text
    logger.info(f"收到命令: {command}，发送者: {sender_id}")

    if sender_id not in ADMIN_LIST and ADMIN_LIST != ['me']:
        logger.warning(f"发送者 {sender_id} 没有权限执行命令 {command}")
        await event.reply(get_text('error.permission_denied'))
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
                await event.reply(get_text('error.channel_not_found', channel=channel))
                return

            poll_config = get_channel_poll_config(channel)

            channel_name = channel.split('/')[-1]
            enabled = poll_config['enabled']
            send_to_channel = poll_config['send_to_channel']

            if enabled is None:
                enabled_text = get_text('poll.status_global')
            else:
                enabled_text = get_text('poll.status_enabled') if enabled else get_text('poll.status_disabled')

            location_text = get_text('poll.location_channel') if send_to_channel else get_text('poll.location_discussion')

            poll_info = get_text('poll.channel_title', channel=channel_name) + "\n\n"
            poll_info += get_text('poll.info', status=enabled_text, location=location_text) + "\n\n"
            poll_info += get_text('poll.usage_set', channel=channel_name) + "\n"
            poll_info += get_text('poll.usage_delete', channel=channel_name)

            logger.info(f"执行命令 {command} 成功")
            await event.reply(poll_info)
        else:
            if not CHANNELS:
                await event.reply(get_text('error.no_channels'))
                return

            poll_info = get_text('poll.all_title') + "\n\n"
            for i, ch in enumerate(CHANNELS, 1):
                poll_config = get_channel_poll_config(ch)
                channel_name = ch.split('/')[-1]

                enabled = poll_config['enabled']
                send_to_channel = poll_config['send_to_channel']

                if enabled is None:
                    enabled_text = get_text('poll.status_global')
                else:
                    enabled_text = get_text('poll.status_enabled') if enabled else get_text('poll.status_disabled')

                location_text = get_text('poll.location_channel') if send_to_channel else get_text('poll.location_discussion')

                poll_info += f"{i}. {channel_name}: {enabled_text} / {location_text}\n"

            await event.reply(poll_info)

    except Exception as e:
        logger.error(f"查看频道投票配置时出错: {type(e).__name__}: {e}", exc_info=True)
        await event.reply(get_text('error.unknown'))


async def handle_set_channel_poll(event):
    """处理/setchannelpoll命令，设置指定频道的投票配置"""
    sender_id = event.sender_id
    command = event.text
    logger.info(f"收到命令: {command}，发送者: {sender_id}")

    if sender_id not in ADMIN_LIST and ADMIN_LIST != ['me']:
        logger.warning(f"发送者 {sender_id} 没有权限执行命令 {command}")
        await event.reply(get_text('error.permission_denied'))
        return

    try:
        parts = command.split()
        if len(parts) < 4:
            await event.reply(get_text('poll.invalid_params'))
            return

        channel_part = parts[1]
        if channel_part.startswith('http'):
            channel = channel_part
        else:
            channel = f"https://t.me/{channel_part}"

        if channel not in CHANNELS:
            await event.reply(get_text('error.channel_not_found', channel=channel))
            return

        enabled_str = parts[2].lower()
        if enabled_str in ['true', '1', 'yes']:
            enabled = True
        elif enabled_str in ['false', '0', 'no']:
            enabled = False
        else:
            await event.reply(get_text('poll.invalid_enabled', enabled=enabled_str))
            return

        location_str = parts[3].lower()
        if location_str in ['channel', 'c']:
            send_to_channel = True
        elif location_str in ['discussion', 'd', 'discuss']:
            send_to_channel = False
        else:
            await event.reply(get_text('poll.invalid_location', location=location_str))
            return

        success = set_channel_poll_config(channel, enabled=enabled, send_to_channel=send_to_channel)

        if success:
            channel_name = channel.split('/')[-1]
            enabled_text = get_text('poll.status_enabled') if enabled else get_text('poll.status_disabled')
            location_text = get_text('poll.location_channel') if send_to_channel else get_text('poll.location_discussion')

            success_msg = get_text('poll.set_success', channel=channel_name, status=enabled_text, location=location_text)

            if not enabled:
                success_msg += get_text('poll.set_note_disabled')
            elif send_to_channel:
                success_msg += get_text('poll.set_note_channel')
            else:
                success_msg += get_text('poll.set_note_discussion')

            await event.reply(success_msg)
        else:
            await event.reply(get_text('poll.set_failed'))

    except Exception as e:
        logger.error(f"设置频道投票配置时出错: {type(e).__name__}: {e}", exc_info=True)
        await event.reply(get_text('error.unknown'))


async def handle_delete_channel_poll(event):
    """处理/deletechannelpoll命令，删除指定频道的投票配置"""
    sender_id = event.sender_id
    command = event.text
    logger.info(f"收到命令: {command}，发送者: {sender_id}")

    if sender_id not in ADMIN_LIST and ADMIN_LIST != ['me']:
        logger.warning(f"发送者 {sender_id} 没有权限执行命令 {command}")
        await event.reply(get_text('error.permission_denied'))
        return

    try:
        parts = command.split()
        if len(parts) < 2:
            await event.reply(get_text('poll.delete_channel_param'))
            return

        channel_part = parts[1]
        if channel_part.startswith('http'):
            channel = channel_part
        else:
            channel = f"https://t.me/{channel_part}"

        if channel not in CHANNELS:
            await event.reply(get_text('error.channel_not_found', channel=channel))
            return

        success = delete_channel_poll_config(channel)

        if success:
            channel_name = channel.split('/')[-1]
            global_enabled = get_text('poll.status_enabled') if ENABLE_POLL else get_text('poll.status_disabled')
            
            logger.info(f"已删除频道 {channel} 的投票配置")
            await event.reply(get_text('poll.delete_success', 
                channel=channel_name, 
                status=global_enabled,
                location=get_text('poll.location_discussion')))
        else:
            await event.reply(get_text('poll.delete_failed'))

    except Exception as e:
        logger.error(f"删除频道投票配置时出错: {type(e).__name__}: {e}", exc_info=True)
        await event.reply(get_text('poll.delete_error', error=e))


# ==================== 数据管理命令 ====================

async def handle_set_send_to_source(event):
    """处理/setsendtosource命令，设置是否将报告发送回源频道"""
    sender_id = event.sender_id
    command = event.text
    logger.info(f"收到命令: {command}，发送者: {sender_id}")
    
    if sender_id not in ADMIN_LIST and ADMIN_LIST != ['me']:
        logger.warning(f"发送者 {sender_id} 没有权限执行命令 {command}")
        await event.reply(get_text('error.permission_denied'))
        return
    
    try:
        _, value = command.split(maxsplit=1)
        value = value.strip().lower()
        
        if value not in ['true', 'false', '1', '0', 'yes', 'no']:
            await event.reply(get_text('report.invalid_value', value=value))
            return
        
        new_value = value in ['true', '1', 'yes']
        
        config = load_config()
        config['send_report_to_source'] = new_value
        save_config(config)
        
        logger.info(f"已将send_report_to_source设置为: {new_value}")
        status = get_text('status.enabled') if new_value else get_text('status.disabled')
        await event.reply(get_text('report.set_success', value=new_value, status=status))
        
    except ValueError:
        current_value = load_config().get('send_report_to_source', SEND_REPORT_TO_SOURCE)
        status = get_text('status.enabled') if current_value else get_text('status.disabled')
        await event.reply(get_text('report.current_status', value=current_value, status=status))
    except Exception as e:
        logger.error(f"设置报告发送回源频道选项时出错: {type(e).__name__}: {e}", exc_info=True)
        await event.reply(get_text('report.set_error', error=e))


async def handle_clear_cache(event):
    """处理/clearcache命令，清除讨论组ID缓存"""
    sender_id = event.sender_id
    command = event.text

    if sender_id not in ADMIN_LIST and ADMIN_LIST != ['me']:
        logger.warning(f"用户 {sender_id} 尝试使用 /clearcache 命令，但没有管理员权限")
        await event.reply(get_text('cache.admin_only'))
        return

    logger.info(f"收到 /clearcache 命令，发送者: {sender_id}")

    try:
        parts = command.split()
        if len(parts) > 1:
            channel = parts[1]
            clear_discussion_group_cache(channel)
            await event.reply(get_text('cache.clear_channel_success', channel=channel))
            logger.info(f"管理员 {sender_id} 清除了频道 {channel} 的讨论组ID缓存")
        else:
            cache_size = len(LINKED_CHAT_CACHE)
            clear_discussion_group_cache()
            await event.reply(get_text('cache.clear_all_success', count=cache_size))
            logger.info(f"管理员 {sender_id} 清除了所有讨论组ID缓存（共 {cache_size} 条）")

    except Exception as e:
        logger.error(f"清除缓存时出错: {type(e).__name__}: {e}", exc_info=True)
        await event.reply(get_text('cache.clear_error', error=e))


# ==================== UI命令 ====================

async def handle_start(event):
    """处理/start命令，显示欢迎消息和帮助信息"""
    sender_id = event.sender_id
    command = event.text
    logger.info(f"收到命令: {command}，发送者: {sender_id}")

    try:
        welcome_message = f"""{get_text('welcome.title')}

{get_text('welcome.description')}

{get_text('welcome.features_title')}
{get_text('welcome.feature_summary')}
{get_text('welcome.feature_schedule')}
{get_text('welcome.feature_custom')}
{get_text('welcome.feature_poll')}
{get_text('welcome.feature_multi')}
{get_text('welcome.feature_history')}

{get_text('welcome.commands_title')}

{get_text('welcome.command_basic')}
{get_text('welcome.command_config')}
{get_text('welcome.command_history')}
{get_text('welcome.command_admin')}

{get_text('welcome.tip')}"""

        await event.reply(welcome_message, link_preview=False)
        logger.info(f"已向用户 {sender_id} 发送欢迎消息")

    except Exception as e:
        logger.error(f"发送欢迎消息时出错: {type(e).__name__}: {e}", exc_info=True)
        await event.reply(get_text('error.unknown'))


async def handle_help(event):
    """处理/help命令，显示完整命令列表和使用说明"""
    sender_id = event.sender_id
    command = event.text
    logger.info(f"收到命令: {command}，发送者: {sender_id}")

    try:
        help_message = f"""{get_text('help.title')}

{get_text('help.section_basic')}
{get_text('cmd.start')}
{get_text('cmd.help')}
{get_text('cmd.summary')}
{get_text('cmd.changelog')}

{get_text('help.section_prompt')}
{get_text('cmd.showprompt')}
{get_text('cmd.setprompt')}
{get_text('cmd.showpollprompt')}
{get_text('cmd.setpollprompt')}

{get_text('help.section_ai')}
{get_text('cmd.showaicfg')}
{get_text('cmd.setaicfg')}

{get_text('help.section_log')}
{get_text('cmd.showloglevel')}
{get_text('cmd.setloglevel')}

{get_text('help.section_control')}
{get_text('cmd.restart')}
{get_text('cmd.shutdown')}
{get_text('cmd.pause')}
{get_text('cmd.resume')}

{get_text('help.section_channel')}
{get_text('cmd.showchannels')}
{get_text('cmd.addchannel')}
{get_text('cmd.deletechannel')}

{get_text('help.section_schedule')}
{get_text('cmd.showchannelschedule')}
{get_text('cmd.setchannelschedule')}
{get_text('cmd.deletechannelschedule')}

{get_text('help.section_data')}
{get_text('cmd.clearsummarytime')}

{get_text('help.section_report')}
{get_text('cmd.setsendtosource')}

{get_text('help.section_poll')}
{get_text('cmd.channelpoll')}
{get_text('cmd.setchannelpoll')}
{get_text('cmd.deletechannelpoll')}

{get_text('help.section_cache')}
{get_text('cmd.clearcache')}

{get_text('help.section_history')}
{get_text('cmd.history')}
{get_text('cmd.export')}
{get_text('cmd.stats')}

{get_text('help.section_language')}
{get_text('cmd.language')}

{get_text('help.tip')}"""

        await event.reply(help_message, link_preview=False)
        logger.info(f"已向用户 {sender_id} 发送完整帮助信息")

    except Exception as e:
        logger.error(f"发送帮助信息时出错: {type(e).__name__}: {e}", exc_info=True)
        await event.reply(get_text('error.unknown'))


async def handle_changelog(event):
    """处理/changelog命令，直接发送变更日志文件"""
    sender_id = event.sender_id
    command = event.text
    logger.info(f"收到命令: {command}，发送者: {sender_id}")
    
    if sender_id not in ADMIN_LIST and ADMIN_LIST != ['me']:
        logger.warning(f"发送者 {sender_id} 没有权限执行命令 {command}")
        await event.reply(get_text('error.permission_denied'))
        return
    
    try:
        changelog_file = "CHANGELOG.md"
        
        if not os.path.exists(changelog_file):
            logger.error(f"更新日志文件 {changelog_file} 不存在")
            await event.reply(get_text('changelog.not_found', filename=changelog_file))
            return
        
        await event.client.send_file(
            sender_id,
            changelog_file,
            caption=get_text('changelog.caption'),
            file_name="CHANGELOG.md"
        )
        
        logger.info(f"已向用户 {sender_id} 发送变更日志文件")
        
    except Exception as e:
        logger.error(f"发送变更日志文件时出错: {type(e).__name__}: {e}", exc_info=True)
        await event.reply(get_text('changelog.send_error', error=e))


# ==================== 语言设置命令 ====================

async def handle_language(event):
    """处理/language命令，切换界面语言"""
    sender_id = event.sender_id
    command = event.text
    logger.info(f"收到命令: {command}，发送者: {sender_id}")

    try:
        parts = command.split()
        
        # 如果没有提供参数，显示当前语言和支持的语言列表
        if len(parts) < 2:
            current_lang = get_language()

            lang_names = {
                'zh-CN': '简体中文',
                'en-US': 'English'
            }

            language_info = get_text('language.supported') + "\n\n"
            language_info += get_text('language.current', language=f"{lang_names.get(current_lang, current_lang)} ({current_lang})") + "\n\n"
            language_info += get_text('language.usage')
            
            await event.reply(language_info)
            logger.info(f"执行命令 {command} 成功")
            return

        # 获取语言代码
        new_language = parts[1].strip()
        
        # 验证语言代码
        if new_language not in get_supported_languages():
            await event.reply(get_text('language.invalid', language=new_language))
            logger.warning(f"用户 {sender_id} 尝试设置不支持的语言: {new_language}")
            return

        # 设置语言
        success = set_language(new_language)
        
        if success:
            # 保存到配置文件
            config = load_config()
            config['language'] = new_language
            save_config(config)
            
            logger.info(f"用户 {sender_id} 将语言更改为: {new_language}")
            
            # 语言名称映射
            lang_names = {
                'zh-CN': '简体中文',
                'en-US': 'English'
            }
            
            lang_name = lang_names.get(new_language, new_language)
            success_msg = get_text('language.changed', language=f"{lang_name} ({new_language})")
            
            await event.reply(success_msg)
        else:
            await event.reply(get_text('language.invalid', language=new_language))

    except Exception as e:
        logger.error(f"设置语言时出错: {type(e).__name__}: {e}", exc_info=True)
        await event.reply(get_text('error.unknown'))