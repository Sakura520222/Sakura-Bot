import logging
from datetime import datetime, timedelta, timezone
from telethon import TelegramClient
from config import API_ID, API_HASH, BOT_TOKEN, CHANNELS, ADMIN_LIST, SEND_REPORT_TO_SOURCE

logger = logging.getLogger(__name__)

async def fetch_last_week_messages(channels_to_fetch=None, start_time=None, report_message_ids=None):
    """抓取指定时间范围的频道消息
    
    Args:
        channels_to_fetch: 可选，要抓取的频道列表。如果为None，则抓取所有配置的频道。
        start_time: 可选，开始抓取的时间。如果为None，则默认抓取过去一周的消息。
        report_message_ids: 可选，要排除的报告消息ID列表，按频道分组。
    """
    # 确保 API_ID 是整数
    logger.info("开始抓取指定时间范围的频道消息")
    
    async with TelegramClient('session_name', int(API_ID), API_HASH) as client:
        # 如果没有提供开始时间，则默认抓取过去一周的消息
        if start_time is None:
            start_time = datetime.now(timezone.utc) - timedelta(days=7)
            logger.info(f"未提供开始时间，默认抓取过去一周的消息")
        
        messages_by_channel = {}  # 按频道分组的消息字典
        report_message_ids = report_message_ids or {}
        
        # 确定要抓取的频道
        if channels_to_fetch and isinstance(channels_to_fetch, list):
            # 只抓取指定的频道
            channels = channels_to_fetch
            logger.info(f"正在抓取指定的 {len(channels)} 个频道的消息，时间范围: {start_time} 至今")
        else:
            # 抓取所有配置的频道
            if not CHANNELS:
                logger.warning("没有配置任何频道，无法抓取消息")
                return messages_by_channel
            channels = CHANNELS
            logger.info(f"正在抓取所有 {len(channels)} 个频道的消息，时间范围: {start_time} 至今")
        
        total_message_count = 0
        
        # 遍历所有要抓取的频道
        for channel in channels:
            channel_messages = []
            channel_message_count = 0
            skipped_report_count = 0
            logger.info(f"开始抓取频道: {channel}")
            
            # 获取当前频道要排除的报告消息ID列表
            exclude_ids = report_message_ids.get(channel, [])
            logger.info(f"频道 {channel} 要排除的报告消息ID列表: {exclude_ids}")
            
            async for message in client.iter_messages(channel, offset_date=start_time, reverse=True):
                total_message_count += 1
                channel_message_count += 1
                
                # 跳过报告消息
                if message.id in exclude_ids:
                    skipped_report_count += 1
                    logger.debug(f"跳过报告消息，ID: {message.id}")
                    continue
                
                if message.text:
                    # 动态获取频道名用于生成链接
                    channel_part = channel.split('/')[-1]
                    msg_link = f"https://t.me/{channel_part}/{message.id}"
                    channel_messages.append(f"内容: {message.text[:500]}\n链接: {msg_link}")
                    
                    # 每抓取10条消息记录一次日志
                    if len(channel_messages) % 10 == 0:
                        logger.debug(f"频道 {channel} 已抓取 {len(channel_messages)} 条有效消息")
            
            # 将当前频道的消息添加到字典中
            messages_by_channel[channel] = channel_messages
            logger.info(f"频道 {channel} 抓取完成，共处理 {channel_message_count} 条消息，其中 {len(channel_messages)} 条包含文本内容，跳过了 {skipped_report_count} 条报告消息")
        
        logger.info(f"所有指定频道消息抓取完成，共处理 {total_message_count} 条消息")
        return messages_by_channel

async def send_long_message(client, chat_id, text, max_length=4000):
    """分段发送长消息"""
    logger.info(f"开始发送长消息，接收者: {chat_id}，消息总长度: {len(text)}字符，最大分段长度: {max_length}字符")
    
    if len(text) <= max_length:
        logger.info(f"消息长度未超过限制，直接发送")
        await client.send_message(chat_id, text, link_preview=False)
        return
    
    # 提取频道名称用于分段消息标题
    channel_title = "频道周报汇总"
    if "**" in text and "** " in text:
        # 提取 ** 之间的频道名称
        start_idx = text.index("**") + 2
        end_idx = text.index("** ", start_idx)
        channel_title = text[start_idx:end_idx]
    
    # 分段发送
    parts = []
    current_part = ""
    
    logger.info(f"消息需要分段发送，开始分段处理")
    for line in text.split('\n'):
        # 检查添加当前行是否超过限制
        if len(current_part) + len(line) + 1 <= max_length:
            current_part += line + '\n'
        else:
            # 如果当前部分不为空，添加到列表
            if current_part:
                parts.append(current_part.strip())
            # 检查当前行是否超过限制
            if len(line) > max_length:
                # 对超长行进行进一步分割
                logger.warning(f"发现超长行，长度: {len(line)}字符，将进一步分割")
                for i in range(0, len(line), max_length):
                    parts.append(line[i:i+max_length])
            else:
                current_part = line + '\n'
    
    # 添加最后一部分
    if current_part:
        parts.append(current_part.strip())
    
    logger.info(f"消息分段完成，共分成 {len(parts)} 段")
    
    # 发送所有部分
    for i, part in enumerate(parts):
        logger.info(f"正在发送第 {i+1}/{len(parts)} 段，长度: {len(part)}字符")
        await client.send_message(chat_id, f"📋 **{channel_title} ({i+1}/{len(parts)})**\n\n{part}", link_preview=False)
        logger.debug(f"成功发送第 {i+1}/{len(parts)} 段")

async def send_report(summary_text, source_channel=None, client=None):
    """发送报告
    
    Args:
        summary_text: 报告内容
        source_channel: 源频道，可选。如果提供，将向该频道发送报告
        client: 可选。已存在的Telegram客户端实例，如果不提供，将创建一个新实例
    
    Returns:
        list: 发送到源频道的消息ID列表
    """
    logger.info("开始发送报告")
    logger.debug(f"报告长度: {len(summary_text)}字符")
    
    # 存储发送到源频道的消息ID
    report_message_ids = []
    
    # 如果提供了客户端实例，直接使用它；否则创建新实例
    if client:
        logger.info("使用现有客户端实例发送报告")
        # 向所有管理员发送消息
        for admin_id in ADMIN_LIST:
            try:
                logger.info(f"正在向管理员 {admin_id} 发送报告")
                await send_long_message(client, admin_id, summary_text)
                logger.info(f"成功向管理员 {admin_id} 发送报告")
            except Exception as e:
                logger.error(f"向管理员 {admin_id} 发送报告失败: {type(e).__name__}: {e}", exc_info=True)
        
        # 如果提供了源频道且配置允许，向源频道发送报告
        if source_channel and SEND_REPORT_TO_SOURCE:
            try:
                logger.info(f"正在向源频道 {source_channel} 发送报告")
                # 直接调用client.send_message并收集消息ID，因为send_long_message不返回消息ID
                if len(summary_text) <= 4000:
                    # 短消息直接发送
                    msg = await client.send_message(source_channel, summary_text, link_preview=False)
                    report_message_ids.append(msg.id)
                else:
                    # 长消息分段发送，收集每个分段的消息ID
                    # 提取频道名称用于分段消息标题
                    channel_title = "频道周报汇总"
                    if "**" in summary_text and "** " in summary_text:
                        start_idx = summary_text.index("**") + 2
                        end_idx = summary_text.index("** ", start_idx)
                        channel_title = summary_text[start_idx:end_idx]
                    
                    # 分段发送
                    parts = []
                    current_part = ""
                    
                    for line in summary_text.split('\n'):
                        if len(current_part) + len(line) + 1 <= 4000:
                            current_part += line + '\n'
                        else:
                            if current_part:
                                parts.append(current_part.strip())
                            if len(line) > 4000:
                                # 对超长行进行进一步分割
                                for i in range(0, len(line), 4000):
                                    parts.append(line[i:i+4000])
                            else:
                                current_part = line + '\n'
                    
                    if current_part:
                        parts.append(current_part.strip())
                    
                    # 发送所有部分并收集消息ID
                    for i, part in enumerate(parts):
                        part_text = f"📋 **{channel_title} ({i+1}/{len(parts)})**\n\n{part}"
                        msg = await client.send_message(source_channel, part_text, link_preview=False)
                        report_message_ids.append(msg.id)
                
                logger.info(f"成功向源频道 {source_channel} 发送报告，消息ID: {report_message_ids}")
            except Exception as e:
                logger.error(f"向源频道 {source_channel} 发送报告失败: {type(e).__name__}: {e}", exc_info=True)
    else:
        logger.info("创建新客户端实例发送报告")
        client = TelegramClient('bot_session', int(API_ID), API_HASH)
        async with client:
            await client.start(bot_token=BOT_TOKEN)
            logger.info("Telegram机器人客户端已启动")
            
            # 向所有管理员发送消息
            for admin_id in ADMIN_LIST:
                try:
                    logger.info(f"正在向管理员 {admin_id} 发送报告")
                    await send_long_message(client, admin_id, summary_text)
                    logger.info(f"成功向管理员 {admin_id} 发送报告")
                except Exception as e:
                    logger.error(f"向管理员 {admin_id} 发送报告失败: {type(e).__name__}: {e}", exc_info=True)
            
            # 如果提供了源频道且配置允许，向源频道发送报告
            if source_channel and SEND_REPORT_TO_SOURCE:
                try:
                    logger.info(f"正在向源频道 {source_channel} 发送报告")
                    # 直接调用client.send_message并收集消息ID
                    if len(summary_text) <= 4000:
                        # 短消息直接发送
                        msg = await client.send_message(source_channel, summary_text, link_preview=False)
                        report_message_ids.append(msg.id)
                    else:
                        # 长消息分段发送，收集每个分段的消息ID
                        # 提取频道名称用于分段消息标题
                        channel_title = "频道周报汇总"
                        if "**" in summary_text and "** " in summary_text:
                            start_idx = summary_text.index("**") + 2
                            end_idx = summary_text.index("** ", start_idx)
                            channel_title = summary_text[start_idx:end_idx]
                        
                        # 分段发送
                        parts = []
                        current_part = ""
                        
                        for line in summary_text.split('\n'):
                            if len(current_part) + len(line) + 1 <= 4000:
                                current_part += line + '\n'
                            else:
                                if current_part:
                                    parts.append(current_part.strip())
                                if len(line) > 4000:
                                    for i in range(0, len(line), 4000):
                                        parts.append(line[i:i+4000])
                                else:
                                    current_part = line + '\n'
                        
                        if current_part:
                            parts.append(current_part.strip())
                        
                        # 发送所有部分并收集消息ID
                        for i, part in enumerate(parts):
                            part_text = f"📋 **{channel_title} ({i+1}/{len(parts)})**\n\n{part}"
                            msg = await client.send_message(source_channel, part_text, link_preview=False)
                            report_message_ids.append(msg.id)
                    
                    logger.info(f"成功向源频道 {source_channel} 发送报告，消息ID: {report_message_ids}")
                except Exception as e:
                    logger.error(f"向源频道 {source_channel} 发送报告失败: {type(e).__name__}: {e}", exc_info=True)
    
    return report_message_ids