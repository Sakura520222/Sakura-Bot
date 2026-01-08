import logging
from datetime import datetime, timezone, timedelta
from apscheduler.schedulers.asyncio import AsyncIOScheduler

from config import CHANNELS, SEND_REPORT_TO_SOURCE, logger
from prompt_manager import load_prompt
from summary_time_manager import load_last_summary_time, save_last_summary_time
from ai_client import analyze_with_ai
from telegram_client import fetch_last_week_messages, send_report

async def main_job():
    start_time = datetime.now()
    logger.info(f"定时任务启动: {start_time}")
    
    try:
        # 按频道分别处理
        for channel in CHANNELS:
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
            if messages:
                logger.info(f"开始处理频道 {channel} 的消息")
                current_prompt = load_prompt()
                summary = analyze_with_ai(messages, current_prompt)
                # 获取频道名称用于报告标题
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
                    sent_report_ids = await send_report(report_text, channel)
                else:
                    await send_report(report_text)
                
                # 保存该频道的本次总结时间和报告消息ID
                save_last_summary_time(channel, datetime.now(timezone.utc), sent_report_ids)
            else:
                logger.info(f"频道 {channel} 没有新消息需要总结")
        
        end_time = datetime.now()
        processing_time = (end_time - start_time).total_seconds()
        logger.info(f"定时任务完成: {end_time}，总处理时间: {processing_time:.2f}秒")
    except Exception as e:
        end_time = datetime.now()
        processing_time = (end_time - start_time).total_seconds()
        logger.error(f"定时任务执行失败: {type(e).__name__}: {e}，开始时间: {start_time}，结束时间: {end_time}，处理时间: {processing_time:.2f}秒", exc_info=True)