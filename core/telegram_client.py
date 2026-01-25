# Copyright 2026 Sakura-频道总结助手
#
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可，
# 并附加非商业使用限制条款。
#
# - 署名：必须提供本项目的原始来源链接
# - 非商业：禁止任何商业用途和分发
# - 相同方式共享：衍生作品必须采用相同的许可证
#
# 本项目源代码：https://github.com/Sakura520222/Sakura-Channel-Summary-Assistant
# 许可证全文：参见 LICENSE 文件

import logging
import asyncio
from datetime import datetime, timedelta, timezone
from telethon import TelegramClient, Button
from telethon.tl.types import PeerChannel
from .config import API_ID, API_HASH, BOT_TOKEN, CHANNELS, ADMIN_LIST, SEND_REPORT_TO_SOURCE, ENABLE_POLL, get_channel_poll_config, LLM_MODEL
from .error_handler import retry_with_backoff, record_error
from .telegram_client_utils import split_message_smart, validate_message_entities

logger = logging.getLogger(__name__)


def extract_date_range_from_summary(summary_text):
    """
    从总结文本中提取日期范围

    Args:
        summary_text: 总结文本

    Returns:
        (start_time, end_time): 起始时间和结束时间的datetime对象，解析失败返回(None, None)
    """
    try:
        import re

        # 匹配周报日期范围: "**xxx周报 1.8-1.15**" 或 "**xxx周报 1.8 - 1.15**"
        weekly_pattern = r"\*\*.*?周报\s*(\d{1,2})\.(\d{1,2})\s*[-—~]\s*(\d{1,2})\.(\d{1,2})\*\*"
        weekly_match = re.search(weekly_pattern, summary_text)

        if weekly_match:
            start_month = int(weekly_match.group(1))
            start_day = int(weekly_match.group(2))
            end_month = int(weekly_match.group(3))
            end_day = int(weekly_match.group(4))

            current_year = datetime.now().year

            start_time = datetime(current_year, start_month, start_day, tzinfo=timezone.utc)
            end_time = datetime(current_year, end_month, end_day, 23, 59, 59, tzinfo=timezone.utc)

            # 如果结束时间早于开始时间，说明跨年了
            if end_time < start_time:
                end_time = datetime(current_year + 1, end_month, end_day, 23, 59, 59, tzinfo=timezone.utc)

            return start_time, end_time

        # 匹配日报日期: "**xxx日报 1.15**"
        daily_pattern = r"\*\*.*?日报\s*(\d{1,2})\.(\d{1,2})\*\*"
        daily_match = re.search(daily_pattern, summary_text)

        if daily_match:
            month = int(daily_match.group(1))
            day = int(daily_match.group(2))
            current_year = datetime.now().year

            start_time = datetime(current_year, month, day, tzinfo=timezone.utc)
            end_time = datetime(current_year, month, day, 23, 59, 59, tzinfo=timezone.utc)

            return start_time, end_time

        # 没有匹配到日期模式
        logger.debug("未能从总结文本中提取日期范围")
        return None, None

    except Exception as e:
        logger.warning(f"提取日期范围时出错: {e}")
        return None, None

# 全局变量，用于存储活动的Telegram客户端实例
_active_client = None

def set_active_client(client):
    """设置活动的Telegram客户端实例"""
    global _active_client
    _active_client = client
    logger.info("已设置活动的Telegram客户端实例")

def get_active_client():
    """获取活动的Telegram客户端实例"""
    return _active_client

@retry_with_backoff(
    max_retries=3,
    base_delay=2.0,
    max_delay=60.0,
    exponential_backoff=True,
    retry_on_exceptions=(ConnectionError, TimeoutError, Exception)
)
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
            start_time = datetime.now() - timedelta(days=7)
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
            
            try:
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
            except Exception as e:
                record_error(e, f"fetch_messages_channel_{channel}")
                logger.error(f"抓取频道 {channel} 消息时出错: {e}")
                # 继续处理其他频道
                continue
            
            # 将当前频道的消息添加到字典中
            messages_by_channel[channel] = channel_messages
            logger.info(f"频道 {channel} 抓取完成，共处理 {channel_message_count} 条消息，其中 {len(channel_messages)} 条包含文本内容，跳过了 {skipped_report_count} 条报告消息")
        
        logger.info(f"所有指定频道消息抓取完成，共处理 {total_message_count} 条消息")
        return messages_by_channel

async def send_long_message(client, chat_id, text, max_length=4000, channel_title=None, show_pagination=True):
    """分段发送长消息
    
    Args:
        client: Telegram客户端实例
        chat_id: 接收者聊天ID
        text: 要发送的文本
        max_length: 最大分段长度，默认4000字符
        channel_title: 频道标题，用于分段消息的标题。如果为None，则使用"更新日志"
        show_pagination: 是否在每条消息显示分页标题（如"1/3"），默认为True。设为False时只在第一条显示标题
    """
    logger.info(f"开始发送长消息，接收者: {chat_id}，消息总长度: {len(text)}字符，最大分段长度: {max_length}字符")
    
    if len(text) <= max_length:
        logger.info(f"消息长度未超过限制，直接发送")
        # 如果消息不超过限制但提供了标题，可以添加标题
        if channel_title and show_pagination:
            text = f"📋 **{channel_title}**\n\n{text}"
        await client.send_message(chat_id, text, link_preview=False)
        return
    
    # 确定标题
    if channel_title is None:
        channel_title = "更新日志"
    
    # 计算标题长度
    if show_pagination:
        # 标题格式：📋 **{channel_title} ({i+1}/{len(parts)})**\n\n
        # 计算最大可能标题长度
        max_title_length = len(f"📋 **{channel_title} (99/99)**\n\n")
    else:
        # 只在第一条消息显示标题，其他条不显示
        # 第一条：📋 **{channel_title}**\n\n
        # 其他：无标题
        max_title_length = len(f"📋 **{channel_title}**\n\n")
    
    # 实际可用于内容的最大长度
    content_max_length = max_length - max_title_length
    
    logger.info(f"消息需要分段发送，开始分段处理，标题长度: {max_title_length}字符，内容最大长度: {content_max_length}字符")
    
    # 使用智能分割算法
    try:
        parts = split_message_smart(text, content_max_length, preserve_md=True)
        logger.info(f"智能分割完成，共分成 {len(parts)} 段")
        
        # 验证每个分段的实体完整性
        for i, part in enumerate(parts):
            is_valid, error_msg = validate_message_entities(part)
            if not is_valid:
                logger.warning(f"第 {i+1} 段实体验证失败: {error_msg}")
                # 尝试修复：移除有问题的格式
                parts[i] = part.replace('**', '').replace('`', '')
                logger.info(f"已修复第 {i+1} 段的格式问题")
    except Exception as e:
        logger.error(f"智能分割失败，使用简单分割: {e}")
        # 回退到简单分割
        parts = []
        text_length = len(text)
        for i in range(0, text_length, content_max_length):
            part = text[i:i+content_max_length]
            if part:
                parts.append(part)
        logger.info(f"简单分割完成，共分成 {len(parts)} 段")
    
    # 验证分段结果
    total_content_length = sum(len(part) for part in parts)
    logger.debug(f"分段后总内容长度: {total_content_length}字符，原始长度: {len(text)}字符")
    
    # 发送所有部分
    for i, part in enumerate(parts):
        # 根据 show_pagination 参数决定标题格式
        if show_pagination:
            # 在每条消息显示分页标题
            full_message = f"📋 **{channel_title} ({i+1}/{len(parts)})**\n\n{part}"
        else:
            # 不显示任何标题，直接发送内容
            full_message = part
        
        full_message_length = len(full_message)
        logger.info(f"正在发送第 {i+1}/{len(parts)} 段，长度: {full_message_length}字符")
        
        # 验证消息长度不超过限制
        if full_message_length > max_length:
            logger.error(f"第 {i+1} 段消息长度 {full_message_length} 超过限制 {max_length}，将进行紧急分割")
            # 紧急分割：直接按字符分割
            for j in range(0, full_message_length, max_length):
                emergency_part = full_message[j:j+max_length]
                await client.send_message(chat_id, emergency_part, link_preview=False)
                logger.warning(f"发送紧急分割段 {j//max_length + 1}")
        else:
            try:
                await client.send_message(chat_id, full_message, link_preview=False)
                logger.debug(f"成功发送第 {i+1}/{len(parts)} 段")
            except Exception as e:
                logger.error(f"发送第 {i+1} 段失败: {e}")
                # 尝试移除格式后重试
                try:
                    plain_message = full_message.replace('**', '').replace('`', '')
                    await client.send_message(chat_id, plain_message, link_preview=False)
                    logger.info(f"已成功发送第 {i+1} 段（移除格式后）")
                except Exception as e2:
                    logger.error(f"即使移除格式后发送第 {i+1} 段仍然失败: {e2}")

async def send_report(summary_text, source_channel=None, client=None, skip_admins=False, message_count=0):
    """发送报告

    Args:
        summary_text: 报告内容
        source_channel: 源频道，可选。如果提供，将向该频道发送报告
        client: 可选。已存在的Telegram客户端实例，如果不提供，将尝试使用活动的客户端实例或创建新实例
        skip_admins: 是否跳过向管理员发送报告，默认为False
        message_count: 消息数量，用于数据库记录，默认为0

    Returns:
        dict: 包含所有消息ID的字典
            {
                "summary_message_ids": [12345, 12346],  # 总结消息ID列表
                "poll_message_id": 12347,                # 投票消息ID(单个)
                "button_message_id": 12348               # 按钮消息ID(单个)
            }
    """
    logger.info("开始发送报告")
    logger.debug(f"报告长度: {len(summary_text)}字符")

    # 存储发送到源频道的消息ID
    report_message_ids = []
    poll_message_id = None
    button_message_id = None
    
    try:
        # 确定使用哪个客户端实例
        # 1. 如果提供了客户端实例，直接使用它
        # 2. 否则，尝试使用活动的客户端实例
        # 3. 否则，创建新实例
        if client:
            logger.info("使用提供的客户端实例发送报告")
            use_client = client
            # 如果提供了客户端实例，我们假设它已经启动并连接
            use_existing_client = True
        else:
            # 尝试获取活动的客户端实例
            active_client = get_active_client()
            if active_client:
                logger.info("使用活动的客户端实例发送报告")
                use_client = active_client
                use_existing_client = True
            else:
                logger.info("没有活动的客户端实例，创建新客户端实例发送报告")
                use_client = TelegramClient('bot_session', int(API_ID), API_HASH)
                use_existing_client = False
        
        if use_existing_client:
            # 使用现有的客户端实例（已经启动并连接）
            
            # 获取频道实际名称（如果提供了源频道）
            channel_actual_name = None
            if source_channel:
                try:
                    channel_entity = await use_client.get_entity(source_channel)
                    channel_actual_name = channel_entity.title
                    logger.info(f"获取到频道实际名称: {channel_actual_name}")
                except Exception as e:
                    logger.warning(f"获取频道实体失败，使用默认名称: {e}")
                    # 使用频道链接的最后部分作为回退
                    channel_actual_name = source_channel.split('/')[-1]
            
            # 提取日期范围和报告类型（从原总结文本中提取）
            date_range = ""
            report_type = ""
            if ("周报" in summary_text or "日报" in summary_text) and "**" in summary_text:
                # 尝试从原总结文本中提取日期范围和类型
                import re
                # 匹配周报或日报的日期范围
                weekly_pattern = r"\*\*.*周报\s*([0-9.]+-[0-9.]+)\*\*"
                daily_pattern = r"\*\*.*日报\s*([0-9.]+)\*\*"
                weekly_match = re.search(weekly_pattern, summary_text)
                daily_match = re.search(daily_pattern, summary_text)
                if weekly_match:
                    date_range = weekly_match.group(1)
                    report_type = "周报"
                elif daily_match:
                    date_range = daily_match.group(1)
                    report_type = "日报"

            # 检查总结文本是否已经有正确的标题格式
            # 如果总结文本已经以 "**" 开头，说明已经有标题，不需要重复添加
            summary_text_for_admins = summary_text  # 管理员接收的文本
            summary_text_for_source = summary_text  # 源频道接收的文本

            # 如果提供了源频道，检查是否需要更新标题
            if source_channel and channel_actual_name:
                # 检查总结文本是否已经有标题（周报或日报）
                if summary_text.startswith("**") and ("周报" in summary_text or "日报" in summary_text):
                    # 已经有标题，检查是否需要更新为频道实际名称
                    # 提取日期范围
                    if date_range:
                        expected_title = f"**{channel_actual_name} {report_type} {date_range}**"
                    else:
                        expected_title = f"**{channel_actual_name} {report_type}**"

                    # 如果当前标题与预期标题不同，则更新
                    if not summary_text.startswith(expected_title):
                        # 找到原标题的结束位置
                        if "** " in summary_text:
                            start_idx = summary_text.index("**")
                            end_idx = summary_text.index("** ", start_idx) + 2
                            # 替换标题
                            summary_text_for_source = expected_title + summary_text[end_idx:]
                            summary_text_for_admins = summary_text_for_source
                else:
                    # 没有标题，添加标题（默认为周报）
                    if not report_type:
                        report_type = "周报"
                    if date_range:
                        new_title = f"**{channel_actual_name} {report_type} {date_range}**"
                    else:
                        new_title = f"**{channel_actual_name} {report_type}**"
                    summary_text_for_source = new_title + "\n\n" + summary_text
                    summary_text_for_admins = summary_text_for_source
            
            # 向所有管理员发送消息（除非跳过）
            if not skip_admins:
                for admin_id in ADMIN_LIST:
                    try:
                        logger.info(f"正在向管理员 {admin_id} 发送报告")
                        await send_long_message(use_client, admin_id, summary_text_for_admins, show_pagination=False)
                        logger.info(f"成功向管理员 {admin_id} 发送报告")
                    except Exception as e:
                        logger.error(f"向管理员 {admin_id} 发送报告失败: {type(e).__name__}: {e}", exc_info=True)
            else:
                logger.info("跳过向管理员发送报告")
            
            # 如果提供了源频道且配置允许，向源频道发送报告
            if source_channel and SEND_REPORT_TO_SOURCE:
                try:
                    logger.info(f"正在向源频道 {source_channel} 发送报告")
                    
                    # 检查频道是否为仅讨论组模式（无写入权限）
                    try:
                        # 尝试获取频道实体和权限
                        channel_entity = await use_client.get_entity(source_channel)
                        logger.info(f"成功获取频道实体: {channel_entity.title if hasattr(channel_entity, 'title') else source_channel}")
                        
                        # 检查是否可以发送消息（发送测试）
                        # 注意：某些频道只允许讨论组，不允许直接发送消息
                        # 如果发送失败，跳过频道发送但继续执行管理员通知
                    
                    except Exception as e:
                        logger.warning(f"获取频道实体失败: {e}，将尝试直接发送")
                    
                    # 直接调用use_client.send_message并收集消息ID
                    if len(summary_text_for_source) <= 4000:
                        # 短消息直接发送
                        msg = await use_client.send_message(source_channel, summary_text_for_source, link_preview=False)
                        report_message_ids.append(msg.id)
                    else:
                        # 长消息分段发送，收集每个分段的消息ID
                        # 使用频道实际名称作为分段消息标题
                        channel_title = channel_actual_name if channel_actual_name else "频道周报汇总"
                        
                        # 使用send_long_message函数进行智能分割和发送
                        # 但需要收集消息ID，所以需要自定义实现
                        max_length = 4000
                        max_title_length = len(f"📋 **{channel_title} (99/99)**\n\n")
                        content_max_length = max_length - max_title_length
                        
                        # 使用智能分割算法
                        try:
                            parts = split_message_smart(summary_text_for_source, content_max_length, preserve_md=True)
                            logger.info(f"智能分割完成，共分成 {len(parts)} 段")
                            
                            # 验证每个分段的实体完整性
                            for i, part in enumerate(parts):
                                is_valid, error_msg = validate_message_entities(part)
                                if not is_valid:
                                    logger.warning(f"第 {i+1} 段实体验证失败: {error_msg}")
                                    # 尝试修复：移除有问题的格式
                                    parts[i] = part.replace('**', '').replace('`', '')
                                    logger.info(f"已修复第 {i+1} 段的格式问题")
                        except Exception as e:
                            logger.error(f"智能分割失败，使用简单分割: {e}")
                            # 回退到简单分割
                            parts = []
                            text_length = len(summary_text_for_source)
                            for i in range(0, text_length, content_max_length):
                                part = summary_text_for_source[i:i+content_max_length]
                                if part:
                                    parts.append(part)
                            logger.info(f"简单分割完成，共分成 {len(parts)} 段")
                        
                        # 发送所有部分并收集消息ID
                        for i, part in enumerate(parts):
                            # 不显示任何标题，直接发送内容
                            part_text = part
                            try:
                                msg = await use_client.send_message(source_channel, part_text, link_preview=False)
                                report_message_ids.append(msg.id)
                                logger.debug(f"成功发送第 {i+1}/{len(parts)} 段，消息ID: {msg.id}")
                            except Exception as e:
                                logger.error(f"发送第 {i+1} 段失败: {e}")
                                # 尝试移除格式后重试
                                try:
                                    plain_text = part_text.replace('**', '').replace('`', '')
                                    msg = await use_client.send_message(source_channel, plain_text, link_preview=False)
                                    report_message_ids.append(msg.id)
                                    logger.info(f"已成功发送第 {i+1} 段（移除格式后），消息ID: {msg.id}")
                                except Exception as e2:
                                    logger.error(f"即使移除格式后发送第 {i+1} 段仍然失败: {e2}")
                    
                    logger.info(f"成功向源频道 {source_channel} 发送报告，消息ID: {report_message_ids}")
                    
                    # 向管理员发送频道发送成功的通知
                    if not skip_admins:
                        for admin_id in ADMIN_LIST:
                            try:
                                await use_client.send_message(
                                    admin_id,
                                    f"✅ 总结已成功发送到频道 {channel_actual_name or source_channel}",
                                    link_preview=False
                                )
                            except Exception as e:
                                logger.debug(f"发送频道成功通知到管理员失败: {e}")
                    
                    # 自动置顶第一条消息
                    if report_message_ids:
                        try:
                            first_message_id = report_message_ids[0]
                            await use_client.pin_message(source_channel, first_message_id)
                            logger.info(f"已成功置顶消息ID: {first_message_id}")
                        except Exception as e:
                            logger.warning(f"置顶消息失败，可能需要管理员权限: {e}")
                    
                    # 如果启用了投票功能，根据频道配置发送投票
                    if report_message_ids:
                        logger.info(f"开始处理投票发送，总结消息ID: {report_message_ids[0]}")
                        # 使用第一个消息ID作为投票回复目标
                        poll_result = await send_poll(
                            use_client, source_channel, report_message_ids[0], summary_text_for_source
                        )
                        if poll_result and poll_result.get("poll_msg_id"):
                            poll_message_id = poll_result.get("poll_msg_id")
                            button_message_id = poll_result.get("button_msg_id")
                            logger.info(f"投票成功发送, poll_msg_id={poll_message_id}, button_msg_id={button_message_id}")
                        else:
                            logger.warning("投票发送失败，但总结消息已成功发送")
                except Exception as e:
                    logger.error(f"向源频道 {source_channel} 发送报告失败: {type(e).__name__}: {e}", exc_info=True)
                    
                    # 特殊处理：频道无写入权限错误
                    if "ChatWriteForbiddenError" in type(e).__name__ or "You can't write in this chat" in str(e):
                        logger.warning(f"⚠️ 频道 {source_channel} ({channel_actual_name}) 不允许机器人发送消息")
                        logger.warning(f"可能的原因：")
                        logger.warning(f"  1. 频道设置为仅讨论组模式")
                        logger.warning(f"  2. 机器人没有在该频道发送消息的权限")
                        logger.warning(f"  3. 频道未启用机器人功能")
                        logger.warning(f"建议：检查频道设置，或仅使用管理员通知功能")
                        
                        # 向管理员发送详细的失败通知
                        if not skip_admins:
                            for admin_id in ADMIN_LIST:
                                try:
                                    notification = (
                                        f"⚠️ **频道发送失败**\n\n"
                                        f"频道：{channel_actual_name or source_channel}\n"
                                        f"原因：机器人没有在该频道发送消息的权限\n\n"
                                        f"可能原因：\n"
                                        f"• 频道设置为仅讨论组模式\n"
                                        f"• 机器人未获得发送消息的权限\n"
                                        f"• 频道未启用机器人功能\n\n"
                                        f"建议：检查频道管理员权限设置\n\n"
                                        f"📊 **总结内容如下：**\n\n"
                                        f"{summary_text_for_source}"
                                    )
                                    await use_client.send_message(admin_id, notification, link_preview=False)
                                except Exception as notify_error:
                                    logger.error(f"发送失败通知到管理员失败: {notify_error}")
                    else:
                        # 其他错误，也向管理员发送通知
                        if not skip_admins:
                            for admin_id in ADMIN_LIST:
                                try:
                                    await use_client.send_message(
                                        admin_id,
                                        f"❌ 向频道 {channel_actual_name or source_channel} 发送报告失败：\n{type(e).__name__}: {e}",
                                        link_preview=False
                                    )
                                except Exception as notify_error:
                                    logger.error(f"发送失败通知到管理员失败: {notify_error}")
        else:
            # 创建新的客户端实例
            async with use_client:
                await use_client.start(bot_token=BOT_TOKEN)
                logger.info("Telegram机器人客户端已启动")
                
                # 获取频道实际名称（如果提供了源频道）
                channel_actual_name = None
                if source_channel:
                    try:
                        channel_entity = await use_client.get_entity(source_channel)
                        channel_actual_name = channel_entity.title
                        logger.info(f"获取到频道实际名称: {channel_actual_name}")
                    except Exception as e:
                        logger.warning(f"获取频道实体失败，使用默认名称: {e}")
                        # 使用频道链接的最后部分作为回退
                        channel_actual_name = source_channel.split('/')[-1]
                
                # 提取日期范围（从原总结文本中提取）
                date_range = ""
                if "周报" in summary_text and "**" in summary_text:
                    # 尝试从原总结文本中提取日期范围
                    import re
                    date_pattern = r"\*\*.*周报\s*([0-9.]+-[0-9.]+)\*\*"
                    match = re.search(date_pattern, summary_text)
                    if match:
                        date_range = match.group(1)
                
                # 检查总结文本是否已经有正确的标题格式
                # 如果总结文本已经以 "**" 开头，说明已经有标题，不需要重复添加
                summary_text_for_admins = summary_text  # 管理员接收的文本
                summary_text_for_source = summary_text  # 源频道接收的文本
                
                # 如果提供了源频道，检查是否需要更新标题
                if source_channel and channel_actual_name:
                    # 检查总结文本是否已经有标题
                    if summary_text.startswith("**") and "周报" in summary_text:
                        # 已经有标题，检查是否需要更新为频道实际名称
                        # 提取日期范围
                        if date_range:
                            expected_title = f"**{channel_actual_name} 周报 {date_range}**"
                        else:
                            expected_title = f"**{channel_actual_name} 周报**"
                        
                        # 如果当前标题与预期标题不同，则更新
                        if not summary_text.startswith(expected_title):
                            # 找到原标题的结束位置
                            if "** " in summary_text:
                                start_idx = summary_text.index("**")
                                end_idx = summary_text.index("** ", start_idx) + 2
                                # 替换标题
                                summary_text_for_source = expected_title + summary_text[end_idx:]
                                summary_text_for_admins = summary_text_for_source
                    else:
                        # 没有标题，添加标题
                        if date_range:
                            new_title = f"**{channel_actual_name} 周报 {date_range}**"
                        else:
                            new_title = f"**{channel_actual_name} 周报**"
                        summary_text_for_source = new_title + "\n\n" + summary_text
                        summary_text_for_admins = summary_text_for_source
                
                # 向所有管理员发送消息（除非跳过）
                if not skip_admins:
                    for admin_id in ADMIN_LIST:
                        try:
                            logger.info(f"正在向管理员 {admin_id} 发送报告")
                            await send_long_message(use_client, admin_id, summary_text_for_admins, show_pagination=False)
                            logger.info(f"成功向管理员 {admin_id} 发送报告")
                        except Exception as e:
                            logger.error(f"向管理员 {admin_id} 发送报告失败: {type(e).__name__}: {e}", exc_info=True)
                else:
                    logger.info("跳过向管理员发送报告")
                
                # 如果提供了源频道且配置允许，向源频道发送报告
                if source_channel and SEND_REPORT_TO_SOURCE:
                    try:
                        logger.info(f"正在向源频道 {source_channel} 发送报告")
                        
                        # 直接调用use_client.send_message并收集消息ID
                        if len(summary_text_for_source) <= 4000:
                            # 短消息直接发送
                            msg = await use_client.send_message(source_channel, summary_text_for_source, link_preview=False)
                            report_message_ids.append(msg.id)
                        else:
                            # 长消息分段发送，收集每个分段的消息ID
                            # 使用频道实际名称作为分段消息标题
                            channel_title = channel_actual_name if channel_actual_name else "频道周报汇总"
                            
                            # 使用智能分割算法
                            max_length = 4000
                            max_title_length = len(f"📋 **{channel_title} (99/99)**\n\n")
                            content_max_length = max_length - max_title_length
                            
                            # 使用智能分割算法
                            try:
                                parts = split_message_smart(summary_text_for_source, content_max_length, preserve_md=True)
                                logger.info(f"智能分割完成，共分成 {len(parts)} 段")
                                
                                # 验证每个分段的实体完整性
                                for i, part in enumerate(parts):
                                    is_valid, error_msg = validate_message_entities(part)
                                    if not is_valid:
                                        logger.warning(f"第 {i+1} 段实体验证失败: {error_msg}")
                                        # 尝试修复：移除有问题的格式
                                        parts[i] = part.replace('**', '').replace('`', '')
                                        logger.info(f"已修复第 {i+1} 段的格式问题")
                            except Exception as e:
                                logger.error(f"智能分割失败，使用简单分割: {e}")
                                # 回退到简单分割
                                parts = []
                                text_length = len(summary_text_for_source)
                                for i in range(0, text_length, content_max_length):
                                    part = summary_text_for_source[i:i+content_max_length]
                                    if part:
                                        parts.append(part)
                                logger.info(f"简单分割完成，共分成 {len(parts)} 段")
                            
                            # 发送所有部分并收集消息ID
                            for i, part in enumerate(parts):
                                # 不显示任何标题，直接发送内容
                                part_text = part
                                try:
                                    msg = await use_client.send_message(source_channel, part_text, link_preview=False)
                                    report_message_ids.append(msg.id)
                                    logger.debug(f"成功发送第 {i+1}/{len(parts)} 段，消息ID: {msg.id}")
                                except Exception as e:
                                    logger.error(f"发送第 {i+1} 段失败: {e}")
                                    # 尝试移除格式后重试
                                    try:
                                        plain_text = part_text.replace('**', '').replace('`', '')
                                        msg = await use_client.send_message(source_channel, plain_text, link_preview=False)
                                        report_message_ids.append(msg.id)
                                        logger.info(f"已成功发送第 {i+1} 段（移除格式后），消息ID: {msg.id}")
                                    except Exception as e2:
                                        logger.error(f"即使移除格式后发送第 {i+1} 段仍然失败: {e2}")
                        
                        logger.info(f"成功向源频道 {source_channel} 发送报告，消息ID: {report_message_ids}")
                        
                        # 自动置顶第一条消息
                        if report_message_ids:
                            try:
                                first_message_id = report_message_ids[0]
                                await use_client.pin_message(source_channel, first_message_id)
                                logger.info(f"已成功置顶消息ID: {first_message_id}")
                            except Exception as e:
                                logger.warning(f"置顶消息失败，可能需要管理员权限: {e}")
                        
                        # 如果启用了投票功能，根据频道配置发送投票
                        if report_message_ids:
                            logger.info(f"开始处理投票发送，总结消息ID: {report_message_ids[0]}")
                            # 使用第一个消息ID作为投票回复目标
                            poll_success = await send_poll(
                                use_client, source_channel, report_message_ids[0], summary_text_for_source
                            )
                            if poll_success:
                                logger.info("投票成功发送")
                            else:
                                logger.warning("投票发送失败，但总结消息已成功发送")
                    except Exception as e:
                        logger.error(f"向源频道 {source_channel} 发送报告失败: {type(e).__name__}: {e}", exc_info=True)
        
        # ✅ 新增：保存到数据库
        # 如果成功发送总结到频道，保存到数据库
        if source_channel and report_message_ids:
            try:
                from .database import get_db_manager

                # 提取时间范围
                start_time, end_time = extract_date_range_from_summary(summary_text_for_source)

                # 保存到数据库
                db = get_db_manager()
                summary_id = db.save_summary(
                    channel_id=source_channel,
                    channel_name=channel_actual_name,
                    summary_text=summary_text_for_source,
                    message_count=message_count,
                    start_time=start_time,
                    end_time=end_time,
                    summary_message_ids=report_message_ids,
                    poll_message_id=poll_message_id,
                    button_message_id=button_message_id,
                    ai_model=LLM_MODEL,
                    summary_type='manual'  # 手动触发的总结
                )

                if summary_id:
                    logger.info(f"总结已保存到数据库，记录ID: {summary_id}")
                else:
                    logger.warning("保存到数据库失败，但不影响总结发送")

            except Exception as e:
                logger.error(f"保存总结到数据库时出错: {type(e).__name__}: {e}", exc_info=True)
                # 数据库保存失败不影响总结发送，只记录日志

        # 返回包含所有消息ID的字典
        return {
            "summary_message_ids": report_message_ids,
            "poll_message_id": poll_message_id,
            "button_message_id": button_message_id
        }

    except Exception as e:
        logger.error(f"发送报告时发生严重错误: {type(e).__name__}: {e}", exc_info=True)
        # 返回空字典，而不是让程序崩溃
        return {
            "summary_message_ids": [],
            "poll_message_id": None,
            "button_message_id": None
        }


async def send_poll_to_channel(client, channel, summary_message_id, summary_text):
    """发送投票到源频道，直接回复总结消息

    Args:
        client: Telegram客户端实例
        channel: 频道URL或ID
        summary_message_id: 总结消息在频道中的ID
        summary_text: 总结文本，用于生成投票内容

    Returns:
        dict: {"poll_msg_id": 12347, "button_msg_id": 12348} 或 None
    """
    logger.info(f"开始处理投票发送到频道: 频道={channel}, 消息ID={summary_message_id}")

    try:
        # 获取频道实体
        logger.info(f"获取频道实体: {channel}")
        channel_entity = await client.get_entity(channel)
        logger.info(f"成功获取频道实体: {channel_entity.title if hasattr(channel_entity, 'title') else channel}")

        # 生成投票内容
        logger.info("开始生成投票内容")
        from .ai_client import generate_poll_from_summary
        poll_data = generate_poll_from_summary(summary_text)

        if not poll_data or 'question' not in poll_data or 'options' not in poll_data:
            logger.error("生成投票内容失败，使用默认投票")
            poll_data = {
                "question": "你对本周总结有什么看法？",
                "options": ["非常满意", "比较满意", "一般", "有待改进"]
            }

        # 发送投票，使用 reply_to 参数回复总结消息
        logger.info(f"发送投票到频道: {poll_data['question']}")

        # 使用高层 API 发送投票并附加按钮
        try:
            from telethon.tl.types import (
                InputMediaPoll, Poll, PollAnswer, TextWithEntities
            )
            from .config import POLL_REGEN_THRESHOLD, ENABLE_VOTE_REGEN_REQUEST

            # 清洗并截断问题文本
            question_text = str(poll_data.get('question', '频道调研')).strip()[:250]

            # 构造选项
            poll_answers = []
            for i, opt in enumerate(poll_data.get('options', [])[:10]):
                opt_clean = str(opt).strip()[:100]
                poll_answers.append(PollAnswer(
                    text=TextWithEntities(text=opt_clean, entities=[]),
                    option=bytes([i])
                ))

            # 构造投票对象
            poll_obj = Poll(
                id=0,
                question=TextWithEntities(text=question_text, entities=[]),
                answers=poll_answers,
                closed=False,
                public_voters=False,
                multiple_choice=False,
                quiz=False
            )

            # 构造内联按钮
            button_markup = []
            # 如果启用投票重新生成请求功能，添加请求按钮
            if ENABLE_VOTE_REGEN_REQUEST:
                button_markup.append([Button.inline(
                    f"👍 请求重新生成 (0/{POLL_REGEN_THRESHOLD})",
                    data=f"request_regen_{summary_message_id}".encode('utf-8')
                )])
            # 添加管理员重新生成按钮
            button_markup.append([Button.inline(
                "🔄 重新生成投票 (管理员)",
                data=f"regen_poll_{summary_message_id}".encode('utf-8')
            )])

            # 使用 send_message 发送投票并附加按钮
            poll_msg = await client.send_message(
                channel,
                file=InputMediaPoll(poll=poll_obj),
                buttons=button_markup,
                reply_to=int(summary_message_id)
            )

            logger.info(f"✅ 成功发送投票到频道并回复消息 {summary_message_id}, 投票消息ID: {poll_msg.id}")

            # 保存映射关系到存储
            from .config import add_poll_regeneration
            channel_name = channel_entity.title if hasattr(channel_entity, 'title') else channel
            add_poll_regeneration(
                channel=channel,
                summary_msg_id=summary_message_id,
                poll_msg_id=poll_msg.id,
                button_msg_id=None,  # 按钮直接附加在投票消息上，无需单独存储
                summary_text=summary_text,
                channel_name=channel_name,
                send_to_channel=True
            )

            # 返回消息ID
            return {
                "poll_msg_id": poll_msg.id,
                "button_msg_id": None  # 按钮直接附加在投票消息上
            }

        except Exception as e:
            logger.error(f"发送投票到频道失败: {e}")
            import traceback
            logger.error(traceback.format_exc())
            return None

    except Exception as e:
        record_error(e, "send_poll_to_channel")
        logger.error(f"发送投票到频道时发生错误: {type(e).__name__}: {e}", exc_info=True)
        return None


async def send_poll(client, channel, summary_message_id, summary_text):
    """根据频道配置发送投票到频道或讨论组

    Args:
        client: Telegram客户端实例
        channel: 频道URL或ID
        summary_message_id: 总结消息在频道中的ID
        summary_text: 总结文本，用于生成投票内容

    Returns:
        dict: {"poll_msg_id": 12347, "button_msg_id": 12348} 或 None
    """
    # 获取频道投票配置
    poll_config = get_channel_poll_config(channel)

    # 检查是否启用投票
    enabled = poll_config['enabled']
    if enabled is None:
        # 没有独立配置，使用全局配置
        enabled = ENABLE_POLL

    if not enabled:
        logger.info(f"频道 {channel} 的投票功能已禁用，跳过投票发送")
        return None

    # 根据配置决定发送位置
    if poll_config['send_to_channel']:
        # 频道模式：直接回复总结消息
        logger.info(f"频道 {channel} 配置为频道模式，投票将发送到频道")
        return await send_poll_to_channel(client, channel, summary_message_id, summary_text)
    else:
        # 讨论组模式：发送到讨论组，回复转发消息
        logger.info(f"频道 {channel} 配置为讨论组模式，投票将发送到讨论组")
        return await send_poll_to_discussion_group(client, channel, summary_message_id, summary_text)


async def send_poll_to_discussion_group(client, channel, summary_message_id, summary_text):
    """发送投票到频道的讨论组（评论区）

    Args:
        client: Telegram客户端实例
        channel: 频道URL或ID
        summary_message_id: 总结消息在频道中的ID
        summary_text: 总结文本，用于生成投票内容

    Returns:
        dict: {"poll_msg_id": 12347, "button_msg_id": 12348} 或 None
    """
    logger.info(f"开始处理投票发送到讨论组: 频道={channel}, 消息ID={summary_message_id}")

    if not ENABLE_POLL:
        logger.info("投票功能已禁用，跳过投票发送")
        return False

    try:
        # 获取频道实体
        logger.info(f"获取频道实体: {channel}")
        channel_entity = await client.get_entity(channel)
        channel_id = channel_entity.id
        channel_name = channel_entity.title if hasattr(channel_entity, 'title') else channel

        # 检查频道是否有绑定的讨论组(使用缓存版本)
        from .config import get_discussion_group_id_cached
        discussion_group_id = await get_discussion_group_id_cached(client, channel)

        if not discussion_group_id:
            logger.warning(f"频道 {channel} 没有绑定讨论组，无法发送投票到评论区")
            return False

        logger.info(f"频道 {channel} 绑定的讨论组ID: {discussion_group_id}")

        # 检查机器人是否在讨论组中
        try:
            await client.get_permissions(discussion_group_id)
            logger.info(f"机器人已在讨论组 {discussion_group_id} 中")
        except Exception as e:
            logger.warning(f"机器人未加入讨论组 {discussion_group_id} 或没有权限: {e}")
            logger.warning("请将机器人添加到频道的讨论组（私人群组）中")
            return False

        # 生成投票内容
        logger.info("开始生成投票内容")
        from .ai_client import generate_poll_from_summary
        poll_data = generate_poll_from_summary(summary_text)

        if not poll_data or 'question' not in poll_data or 'options' not in poll_data:
            logger.error("生成投票内容失败，使用默认投票")
            poll_data = {
                "question": "你对本周总结有什么看法？",
                "options": ["非常满意", "比较满意", "一般", "有待改进"]
            }

        # 使用事件监听方式等待转发消息
        logger.info(f"等待频道消息转发到讨论组...")

        # 创建事件Future来等待转发消息
        from asyncio import Future
        forward_message_future = Future()

        # 定义事件处理器
        from telethon import events

        @client.on(events.NewMessage(chats=discussion_group_id))
        async def on_discussion_message(event):
            msg = event.message

            # 检查是否是转发消息
            if (hasattr(msg, 'fwd_from') and msg.fwd_from and
                hasattr(msg.fwd_from, 'from_id') and msg.fwd_from.from_id and
                hasattr(msg.fwd_from.from_id, 'channel_id') and
                msg.fwd_from.from_id.channel_id == channel_id and
                msg.fwd_from.channel_post == summary_message_id):

                logger.info(f"收到转发消息，讨论组消息ID: {msg.id}")
                forward_message_future.set_result(msg)

                # 移除事件处理器
                client.remove_event_handler(on_discussion_message)

        # 等待转发消息（最多10秒）
        try:
            forward_message = await asyncio.wait_for(forward_message_future, timeout=10)
            logger.info(f"成功收到转发消息，ID: {forward_message.id}")

            # 发送投票作为回复
            logger.info(f"发送投票到讨论组: {poll_data['question']}")

            # 使用高层 API 发送投票并附加按钮
            from telethon.tl.types import InputMediaPoll, Poll, PollAnswer, TextWithEntities
            from .config import POLL_REGEN_THRESHOLD, ENABLE_VOTE_REGEN_REQUEST

            try:
                # 1. 严格清洗并截断
                question_text = str(poll_data.get('question', '频道调研')).strip()[:250]

                # 2. 构造选项，确保text字段被显式包装为TextWithEntities
                poll_answers = []
                for i, opt in enumerate(poll_data.get('options', [])[:10]):
                    opt_clean = str(opt).strip()[:100]
                    poll_answers.append(PollAnswer(
                        text=TextWithEntities(text=opt_clean, entities=[]),
                        option=bytes([i])
                    ))

                # 3. 手动构造Poll对象
                poll_obj = Poll(
                    id=0,
                    question=TextWithEntities(text=question_text, entities=[]),
                    answers=poll_answers,
                    closed=False,
                    public_voters=False,
                    multiple_choice=False,
                    quiz=False
                )

                # 4. 构造内联按钮
                button_markup = []
                # 如果启用投票重新生成请求功能，添加请求按钮
                if ENABLE_VOTE_REGEN_REQUEST:
                    button_markup.append([Button.inline(
                        f"👍 请求重新生成 (0/{POLL_REGEN_THRESHOLD})",
                        data=f"request_regen_{summary_message_id}".encode('utf-8')
                    )])
                # 添加管理员重新生成按钮
                button_markup.append([Button.inline(
                    "🔄 重新生成投票 (管理员)",
                    data=f"regen_poll_{summary_message_id}".encode('utf-8')
                )])

                # 5. 使用 send_message 发送投票并附加按钮
                poll_msg = await client.send_message(
                    discussion_group_id,
                    file=InputMediaPoll(poll=poll_obj),
                    buttons=button_markup,
                    reply_to=forward_message.id
                )

                logger.info(f"✅ [高层API模式] 投票发送成功: {question_text}, 消息ID: {poll_msg.id}")

                # 保存映射关系到存储
                from .config import add_poll_regeneration
                add_poll_regeneration(
                    channel=channel,
                    summary_msg_id=summary_message_id,
                    poll_msg_id=poll_msg.id,
                    button_msg_id=None,  # 按钮直接附加在投票消息上，无需单独存储
                    summary_text=summary_text,
                    channel_name=channel_name,
                    send_to_channel=False,
                    discussion_forward_msg_id=forward_message.id
                )

                # 返回消息ID
                return {
                    "poll_msg_id": poll_msg.id,
                    "button_msg_id": None  # 按钮直接附加在投票消息上
                }

            except Exception as e:
                logger.error(f"❌ 发送投票失败: {e}")
                import traceback
                logger.error(traceback.format_exc())
                return None

        except asyncio.TimeoutError:
            logger.warning(f"等待转发消息超时（10秒），可能转发延迟或未成功")
            # 移除事件处理器
            client.remove_event_handler(on_discussion_message)

            # 尝试发送独立消息
            try:
                logger.info(f"尝试发送独立投票消息")
                await client.send_message(
                    discussion_group_id,
                    f"📊 **投票：{poll_data['question']}**\n\n" +
                    "\n".join([f"• {opt}" for opt in poll_data['options']])
                )
                logger.info("成功发送独立投票消息")
                return None
            except Exception as e:
                logger.error(f"发送独立投票消息失败: {e}")
                return None

    except Exception as e:
        record_error(e, "send_poll_to_discussion_group")
        logger.error(f"发送投票到讨论组失败: {type(e).__name__}: {e}", exc_info=True)
        return None
