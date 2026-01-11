# Copyright 2026 Sakura-频道总结助手
# 
# 本项目采用 CC BY-NC-SA 4.0 许可证
# 您可以自由地共享、修改本作品，但必须遵守以下条件：
# - 署名：必须提供本项目的原始来源链接
# - 非商业：禁止任何商业用途和分发
# - 相同方式共享：衍生作品必须采用相同的许可证
# 
# 本项目源代码：https://github.com/Sakura520222/Sakura-Channel-Summary-Assistant
# 许可证全文：https://creativecommons.org/licenses/by-nc-sa/4.0/deed.zh

import logging
from datetime import datetime, timezone, timedelta
from apscheduler.schedulers.asyncio import AsyncIOScheduler

from config import CHANNELS, SEND_REPORT_TO_SOURCE, logger
from prompt_manager import load_prompt
from summary_time_manager import load_last_summary_time, save_last_summary_time
from ai_client import analyze_with_ai
from telegram_client import fetch_last_week_messages, send_report, get_active_client

async def main_job(channel=None):
    """定时任务主函数
    
    Args:
        channel: 可选，指定要处理的频道。如果为None，则处理所有频道
    
    Returns:
        dict: 包含任务执行结果的字典，格式为:
            {
                "success": bool,  # 是否成功
                "channel": str,   # 处理的频道
                "message_count": int,  # 处理的消息数量
                "summary_length": int,  # 总结长度（字符数）
                "processing_time": float,  # 处理时间（秒）
                "error": str or None,  # 错误信息（如果有）
                "details": str  # 详细结果描述
            }
    """
    start_time = datetime.now()
    
    if channel:
        logger.info(f"定时任务启动（单频道模式）: {start_time}，频道: {channel}")
        channels_to_process = [channel]
    else:
        logger.info(f"定时任务启动（全频道模式）: {start_time}")
        channels_to_process = CHANNELS
    
    try:
        results = []
        # 按频道分别处理
        for channel in channels_to_process:
            channel_start_time = datetime.now()
            logger.info(f"开始处理频道: {channel}")
            
            # 读取该频道的上次总结时间和报告消息ID
            channel_summary_data = load_last_summary_time(channel, include_report_ids=True)
            if channel_summary_data:
                channel_last_summary_time = channel_summary_data["time"]
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
            
            # 检查频道是否存在（如果频道不存在，messages_by_channel可能不包含该频道）
            if channel not in messages_by_channel:
                # 频道不存在或无法访问
                channel_end_time = datetime.now()
                channel_processing_time = (channel_end_time - channel_start_time).total_seconds()
                
                result = {
                    "success": False,
                    "channel": channel,
                    "message_count": 0,
                    "summary_length": 0,
                    "processing_time": channel_processing_time,
                    "error": f"频道 {channel} 不存在或无法访问",
                    "details": f"频道 {channel} 不存在或无法访问，处理时间 {channel_processing_time:.2f}秒"
                }
                results.append(result)
                logger.error(f"频道 {channel} 不存在或无法访问")
                continue
            if messages:
                logger.info(f"开始处理频道 {channel} 的消息，共 {len(messages)} 条消息")
                current_prompt = load_prompt()
                summary = analyze_with_ai(messages, current_prompt)
                # 获取活动的客户端实例和频道的实际名称用于报告标题
                active_client = get_active_client()
                try:
                    channel_entity = await active_client.get_entity(channel)
                    channel_name = channel_entity.title
                    logger.info(f"获取到频道实际名称: {channel_name}")
                except Exception as e:
                    logger.warning(f"获取频道实体失败，使用链接后缀作为回退: {e}")
                    # 如果获取失败，使用链接后缀作为回退
                    channel_name = channel.split('/')[-1]
                # 计算起始日期和终止日期
                end_date = datetime.now(timezone.utc)
                if channel_last_summary_time:
                    start_date = channel_last_summary_time
                else:
                    start_date = end_date - timedelta(days=7)
                # 格式化日期为 月.日 格式
                start_date_str = f"{start_date.month}.{start_date.day}"
                end_date_str = f"{end_date.month}.{end_date.day}"
                # 生成报告标题
                report_text = f"**{channel_name} 周报 {start_date_str}-{end_date_str}**\n\n{summary}"
                # 发送报告给管理员，并根据配置决定是否发送回源频道
                sent_report_ids = []
                
                if SEND_REPORT_TO_SOURCE:
                    sent_report_ids = await send_report(report_text, channel, client=active_client)
                else:
                    await send_report(report_text, client=active_client)
                
                # 保存该频道的本次总结时间和报告消息ID
                save_last_summary_time(channel, datetime.now(timezone.utc), sent_report_ids)
                
                channel_end_time = datetime.now()
                channel_processing_time = (channel_end_time - channel_start_time).total_seconds()
                
                # 构建结果信息
                result = {
                    "success": True,
                    "channel": channel,
                    "message_count": len(messages),
                    "summary_length": len(summary),
                    "processing_time": channel_processing_time,
                    "error": None,
                    "details": f"成功处理频道 {channel}，共 {len(messages)} 条消息，生成 {len(summary)} 字符的总结，处理时间 {channel_processing_time:.2f}秒"
                }
                results.append(result)
                
                logger.info(f"频道 {channel} 处理完成: {result['details']}")
            else:
                logger.info(f"频道 {channel} 没有新消息需要总结")
                channel_end_time = datetime.now()
                channel_processing_time = (channel_end_time - channel_start_time).total_seconds()
                
                result = {
                    "success": True,
                    "channel": channel,
                    "message_count": 0,
                    "summary_length": 0,
                    "processing_time": channel_processing_time,
                    "error": None,
                    "details": f"频道 {channel} 没有新消息需要总结，处理时间 {channel_processing_time:.2f}秒"
                }
                results.append(result)
        
        end_time = datetime.now()
        processing_time = (end_time - start_time).total_seconds()
        
        if channel:
            logger.info(f"定时任务完成（单频道模式）: {end_time}，频道: {channel}，处理时间: {processing_time:.2f}秒")
        else:
            logger.info(f"定时任务完成（全频道模式）: {end_time}，总处理时间: {processing_time:.2f}秒")
        
        # 返回结果
        if len(results) == 1:
            return results[0]
        else:
            return {
                "success": True,
                "channel": "all" if not channel else channel,
                "message_count": sum(r["message_count"] for r in results),
                "summary_length": sum(r["summary_length"] for r in results),
                "processing_time": processing_time,
                "error": None,
                "details": f"成功处理 {len(results)} 个频道，共 {sum(r['message_count'] for r in results)} 条消息，总处理时间 {processing_time:.2f}秒"
            }
            
    except Exception as e:
        end_time = datetime.now()
        processing_time = (end_time - start_time).total_seconds()
        
        error_msg = f"{type(e).__name__}: {e}"
        if channel:
            logger.error(f"定时任务执行失败（单频道模式）: {error_msg}，频道: {channel}，开始时间: {start_time}，结束时间: {end_time}，处理时间: {processing_time:.2f}秒", exc_info=True)
        else:
            logger.error(f"定时任务执行失败（全频道模式）: {error_msg}，开始时间: {start_time}，结束时间: {end_time}，处理时间: {processing_time:.2f}秒", exc_info=True)
        
        # 返回错误结果
        return {
            "success": False,
            "channel": channel if channel else "all",
            "message_count": 0,
            "summary_length": 0,
            "processing_time": processing_time,
            "error": error_msg,
            "details": f"任务执行失败: {error_msg}，处理时间 {processing_time:.2f}秒"
        }
