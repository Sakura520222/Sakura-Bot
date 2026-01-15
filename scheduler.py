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

from config import CHANNELS, SEND_REPORT_TO_SOURCE, logger, LLM_MODEL
from prompt_manager import load_prompt
from summary_time_manager import load_last_summary_time, save_last_summary_time
from ai_client import analyze_with_ai
from telegram_client import fetch_last_week_messages, send_report, get_active_client, extract_date_range_from_summary

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

                # 获取频道的调度配置，用于生成报告标题
                from config import get_channel_schedule
                schedule_config = get_channel_schedule(channel)
                frequency = schedule_config.get('frequency', 'weekly')

                # 计算起始日期和终止日期
                end_date = datetime.now(timezone.utc)
                if channel_last_summary_time:
                    start_date = channel_last_summary_time
                else:
                    start_date = end_date - timedelta(days=7)

                # 格式化日期为 月.日 格式
                start_date_str = f"{start_date.month}.{start_date.day}"
                end_date_str = f"{end_date.month}.{end_date.day}"

                # 根据频率生成报告标题
                if frequency == 'daily':
                    report_title = f"{channel_name} 日报 {end_date_str}"
                else:  # weekly
                    report_title = f"{channel_name} 周报 {start_date_str}-{end_date_str}"

                # 生成报告文本
                report_text = f"**{report_title}**\n\n{summary}"
                # 发送报告给管理员，并根据配置决定是否发送回源频道
                report_result = None

                if SEND_REPORT_TO_SOURCE:
                    report_result = await send_report(report_text, channel, client=active_client, message_count=len(messages))
                else:
                    report_result = await send_report(report_text, client=active_client, message_count=len(messages))

                # 保存该频道的本次总结时间和所有相关消息ID
                if report_result:
                    summary_ids = report_result.get("summary_message_ids", [])
                    poll_id = report_result.get("poll_message_id")
                    button_id = report_result.get("button_message_id")

                    # 转换单个ID为列表格式
                    poll_ids = [poll_id] if poll_id else []
                    button_ids = [button_id] if button_id else []

                    # ✅ 新增：保存到数据库
                    try:
                        from database import get_db_manager

                        # 提取时间范围
                        start_time_db, end_time_db = extract_date_range_from_summary(report_text)

                        # 保存到数据库
                        db = get_db_manager()
                        summary_id = db.save_summary(
                            channel_id=channel,
                            channel_name=channel_name,
                            summary_text=report_text,
                            message_count=len(messages),
                            start_time=start_time_db,
                            end_time=end_time_db,
                            summary_message_ids=summary_ids,
                            poll_message_id=poll_id,
                            button_message_id=button_id,
                            ai_model=LLM_MODEL,
                            summary_type=frequency  # 'daily' 或 'weekly'
                        )

                        if summary_id:
                            logger.info(f"定时任务总结已保存到数据库，记录ID: {summary_id}")
                        else:
                            logger.warning("保存到数据库失败，但不影响定时任务执行")

                    except Exception as e:
                        logger.error(f"保存定时任务总结到数据库时出错: {type(e).__name__}: {e}", exc_info=True)
                        # 数据库保存失败不影响定时任务，只记录日志

                    save_last_summary_time(
                        channel,
                        datetime.now(timezone.utc),
                        summary_message_ids=summary_ids,
                        poll_message_ids=poll_ids,
                        button_message_ids=button_ids
                    )
                
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


async def cleanup_old_poll_regenerations():
    """定期清理超过30天的投票重新生成数据"""
    from config import cleanup_old_regenerations

    try:
        deleted_count = cleanup_old_regenerations(days=30)
        if deleted_count > 0:
            logger.info(f"已清理 {deleted_count} 条过期的投票重新生成数据")
    except Exception as e:
        logger.error(f"清理投票重新生成数据失败: {e}")
