"""
总结相关命令处理
"""

import logging
import json
import os
from datetime import datetime, timezone, timedelta

from ..config import (
    CHANNELS, ADMIN_LIST, SEND_REPORT_TO_SOURCE,
    LAST_SUMMARY_FILE, logger,
    get_channel_schedule
)
from ..summary_time_manager import load_last_summary_time, save_last_summary_time
from ..prompt_manager import load_prompt
from ..ai_client import analyze_with_ai
from ..telegram_client import fetch_last_week_messages, send_long_message, send_report

logger = logging.getLogger(__name__)


async def handle_manual_summary(event):
    """处理/立即总结命令"""
    sender_id = event.sender_id
    command = event.text
    logger.info(f"收到命令: {command}，发送者: {sender_id}")
    
    # 检查发送者是否为管理员
    if sender_id not in ADMIN_LIST and ADMIN_LIST != ['me']:
        logger.warning(f"发送者 {sender_id} 没有权限执行命令 {command}")
        await event.reply("您没有权限执行此命令")
        return
    
    # 发送正在处理的消息
    await event.reply("正在为您生成总结...")
    logger.info(f"开始执行 {command} 命令")
    
    # 解析命令参数，支持指定频道
    try:
        # 分割命令和参数
        parts = command.split()
        if len(parts) > 1:
            # 有指定频道参数
            specified_channels = []
            for part in parts[1:]:
                if part.startswith('http'):
                    # 完整的频道URL
                    specified_channels.append(part)
                else:
                    # 频道名称，需要转换为完整URL
                    specified_channels.append(f"https://t.me/{part}")
            
            # 验证指定的频道是否在配置中
            valid_channels = []
            for channel in specified_channels:
                if channel in CHANNELS:
                    valid_channels.append(channel)
                else:
                    await event.reply(f"频道 {channel} 不在配置列表中，将跳过")
            
            if not valid_channels:
                await event.reply("没有找到有效的指定频道")
                return
            
            channels_to_process = valid_channels
        else:
            # 没有指定频道，处理所有配置的频道
            channels_to_process = CHANNELS
        
        # 按频道分别处理
        for channel in channels_to_process:
            # 读取该频道的上次总结时间和报告消息ID
            channel_summary_data = load_last_summary_time(channel, include_report_ids=True)
            if channel_summary_data:
                channel_last_summary_time = channel_summary_data["time"]
                # 使用新的键名: summary_message_ids
                # 为了向后兼容,同时支持旧格式
                if "summary_message_ids" in channel_summary_data:
                    # 新格式
                    summary_ids = channel_summary_data["summary_message_ids"]
                    # 类型检查: 如果summary_ids是字典,说明数据格式错误,需要修复
                    if isinstance(summary_ids, dict):
                        logger.warning(f"检测到summary_ids是字典格式,正在修复数据结构: {summary_ids}")
                        summary_ids = summary_ids.get("summary_message_ids", [])
                    # 确保是列表
                    if not isinstance(summary_ids, list):
                        logger.error(f"summary_ids类型错误: {type(summary_ids)}, 值: {summary_ids}, 使用空列表")
                        summary_ids = []

                    poll_ids = channel_summary_data.get("poll_message_ids", [])
                    button_ids = channel_summary_data.get("button_message_ids", [])
                    # 确保都是列表
                    if not isinstance(poll_ids, list):
                        poll_ids = []
                    if not isinstance(button_ids, list):
                        button_ids = []

                    # 合并所有消息ID用于排除
                    report_message_ids_to_exclude = summary_ids + poll_ids + button_ids
                else:
                    # 旧格式,使用report_message_ids
                    report_message_ids_to_exclude = channel_summary_data["report_message_ids"]
            else:
                channel_last_summary_time = None
                report_message_ids_to_exclude = []
            
            # 抓取该频道从上次总结时间开始的消息，排除已发送的报告消息
            messages_by_channel = await fetch_last_week_messages(
                [channel], 
                start_time=channel_last_summary_time,
                report_message_ids={channel: report_message_ids_to_exclude}
            )
            
            # 获取该频道的消息
            messages = messages_by_channel.get(channel, [])
            if messages:
                logger.info(f"开始处理频道 {channel} 的消息")
                current_prompt = load_prompt()
                summary = analyze_with_ai(messages, current_prompt)
                # 获取频道实际名称
                try:
                    channel_entity = await event.client.get_entity(channel)
                    channel_actual_name = channel_entity.title
                    logger.info(f"获取到频道实际名称: {channel_actual_name}")
                except Exception as e:
                    logger.warning(f"获取频道实体失败，使用默认名称: {e}")
                    # 使用频道链接的最后部分作为回退
                    channel_actual_name = channel.split('/')[-1]
                # 计算起始日期和终止日期
                end_date = datetime.now(timezone.utc)
                if channel_last_summary_time:
                    start_date = channel_last_summary_time
                else:
                    start_date = end_date - timedelta(days=7)
                # 格式化日期为 月.日 格式
                start_date_str = f"{start_date.month}.{start_date.day}"
                end_date_str = f"{end_date.month}.{end_date.day}"

                # 获取频道的调度配置，用于生成报告标题
                schedule_config = get_channel_schedule(channel)
                frequency = schedule_config.get('frequency', 'weekly')

                # 根据频率生成报告标题
                if frequency == 'daily':
                    report_title = f"{channel_actual_name} 日报 {end_date_str}"
                else:  # weekly
                    report_title = f"{channel_actual_name} 周报 {start_date_str}-{end_date_str}"

                # 生成报告文本
                report_text = f"**{report_title}**\n\n{summary}"
                # 向请求者发送总结
                await send_long_message(event.client, sender_id, report_text)
                # 根据配置决定是否向源频道发送总结，传递现有客户端实例避免数据库锁定
                # 如果请求者是管理员，跳过向管理员发送报告，避免重复发送
                skip_admins = sender_id in ADMIN_LIST or ADMIN_LIST == ['me']
                sent_report_ids = []
                if SEND_REPORT_TO_SOURCE:
                    sent_report_ids = await send_report(report_text, channel, event.client, skip_admins=skip_admins, message_count=len(messages))
                else:
                    await send_report(report_text, None, event.client, skip_admins=skip_admins, message_count=len(messages))
                
                # 保存该频道的本次总结时间和所有相关消息ID
                if sent_report_ids:
                    summary_ids = sent_report_ids.get("summary_message_ids", [])
                    poll_id = sent_report_ids.get("poll_message_id")
                    button_id = sent_report_ids.get("button_message_id")

                    # 转换单个ID为列表格式
                    poll_ids = [poll_id] if poll_id else []
                    button_ids = [button_id] if button_id else []

                    save_last_summary_time(
                        channel,
                        datetime.now(timezone.utc),
                        summary_message_ids=summary_ids,
                        poll_message_ids=poll_ids,
                        button_message_ids=button_ids
                    )
                else:
                    save_last_summary_time(channel, datetime.now(timezone.utc))
            else:
                logger.info(f"频道 {channel} 没有新消息需要总结")
                # 获取频道实际名称用于无消息提示
                try:
                    channel_entity = await event.client.get_entity(channel)
                    channel_actual_name = channel_entity.title
                except Exception as e:
                    channel_actual_name = channel.split('/')[-1]
                await send_long_message(event.client, sender_id, f"📋 **{channel_actual_name} 频道汇总**\n\n该频道自上次总结以来没有新消息。")
        
        logger.info(f"命令 {command} 执行成功")
    except Exception as e:
        logger.error(f"执行命令 {command} 时出错: {type(e).__name__}: {e}", exc_info=True)
        await event.reply(f"生成总结时出错: {e}")


async def handle_clear_summary_time(event):
    """处理/clearsummarytime命令，清除上次总结时间记录
    支持清除所有频道或特定频道的时间记录
    """
    sender_id = event.sender_id
    command = event.text
    logger.info(f"收到命令: {command}，发送者: {sender_id}")
    
    # 检查发送者是否为管理员
    if sender_id not in ADMIN_LIST and ADMIN_LIST != ['me']:
        logger.warning(f"发送者 {sender_id} 没有权限执行命令 {command}")
        await event.reply("您没有权限执行此命令")
        return
    
    try:
        # 解析命令参数
        parts = command.split()
        specific_channel = None
        if len(parts) > 1:
            # 有指定频道参数
            channel_part = parts[1]
            if channel_part.startswith('http'):
                specific_channel = channel_part
            else:
                specific_channel = f"https://t.me/{channel_part}"
        
        if os.path.exists(LAST_SUMMARY_FILE):
            if specific_channel:
                # 清除特定频道的时间记录
                with open(LAST_SUMMARY_FILE, "r", encoding="utf-8") as f:
                    content = f.read().strip()
                    if content:
                        existing_data = json.loads(content)
                        if specific_channel in existing_data:
                            del existing_data[specific_channel]
                            # 写回文件
                            with open(LAST_SUMMARY_FILE, "w", encoding="utf-8") as f_write:
                                json.dump(existing_data, f_write, ensure_ascii=False, indent=2)
                            logger.info(f"已清除频道 {specific_channel} 的上次总结时间记录")
                            await event.reply(f"已成功清除频道 {specific_channel} 的上次总结时间记录。")
                        else:
                            logger.info(f"频道 {specific_channel} 的上次总结时间记录不存在，无需清除")
                            await event.reply(f"频道 {specific_channel} 的上次总结时间记录不存在，无需清除。")
                    else:
                        logger.info(f"上次总结时间记录文件 {LAST_SUMMARY_FILE} 内容为空，无需清除")
                        await event.reply("上次总结时间记录文件内容为空，无需清除。")
            else:
                # 清除所有频道的时间记录
                os.remove(LAST_SUMMARY_FILE)
                logger.info(f"已清除所有频道的上次总结时间记录，文件 {LAST_SUMMARY_FILE} 已删除")
                await event.reply("已成功清除所有频道的上次总结时间记录。下次总结将重新抓取过去一周的消息。")
        else:
            logger.info(f"上次总结时间记录文件 {LAST_SUMMARY_FILE} 不存在，无需清除")
            await event.reply("上次总结时间记录文件不存在，无需清除。")
    except Exception as e:
        logger.error(f"清除上次总结时间记录时出错: {type(e).__name__}: {e}", exc_info=True)
        await event.reply(f"清除上次总结时间记录时出错: {e}")