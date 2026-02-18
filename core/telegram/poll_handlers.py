"""
Telegram投票处理模块
包含发送投票到频道和讨论组的功能
"""

import logging
import asyncio
from telethon import events, Button
from telethon.tl.types import InputMediaPoll, Poll, PollAnswer, TextWithEntities

from ..config import ENABLE_POLL, get_channel_poll_config, POLL_REGEN_THRESHOLD, ENABLE_VOTE_REGEN_REQUEST
from ..ai_client import generate_poll_from_summary
from ..error_handler import record_error
from ..i18n import get_text

logger = logging.getLogger(__name__)


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
        poll_data = generate_poll_from_summary(summary_text)

        if not poll_data or 'question' not in poll_data or 'options' not in poll_data:
            logger.error("生成投票内容失败，使用默认投票")
            poll_data = {
                "question": get_text('poll.default_question'),
                "options": [
                    get_text('poll.default_options.0'),
                    get_text('poll.default_options.1'),
                    get_text('poll.default_options.2'),
                    get_text('poll.default_options.3')
                ]
            }

        # 发送投票，使用 reply_to 参数回复总结消息
        logger.info(f"发送投票到频道: {poll_data['question']}")

        # 使用高层 API 发送投票并附加按钮
        try:
            # 清洗并截断问题文本
            question_text = str(poll_data.get('question', get_text('poll_regen.default_question'))).strip()[:250]

            # 构造选项
            poll_answers = []
            for i, opt in enumerate(poll_data.get('options', [])[:10]):
                opt_clean = str(opt).strip()[:100]
                # 选项的text也必须是TextWithEntities类型
                poll_answers.append(PollAnswer(
                    text=TextWithEntities(opt_clean, entities=[]),
                    option=bytes([i])
                ))

            # 构造投票对象
            # 注意：Telethon的Poll构造函数要求question必须是TextWithEntities类型
            poll_obj = Poll(
                id=0,
                question=TextWithEntities(question_text, entities=[]),  # 必须包装为TextWithEntities，并传入空的entities列表
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
                    get_text('poll_regen.request_button', count=0, threshold=POLL_REGEN_THRESHOLD),
                    data=f"request_regen_{summary_message_id}".encode('utf-8')
                )])
            # 添加管理员重新生成按钮
            button_markup.append([Button.inline(
                get_text('poll_regen.admin_button'),
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
            from ..config import add_poll_regeneration
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
        from ..config import get_discussion_group_id_cached
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
        poll_data = generate_poll_from_summary(summary_text)

        if not poll_data or 'question' not in poll_data or 'options' not in poll_data:
            logger.error("生成投票内容失败，使用默认投票")
            poll_data = {
                "question": get_text('poll.default_question'),
                "options": [
                    get_text('poll.default_options.0'),
                    get_text('poll.default_options.1'),
                    get_text('poll.default_options.2'),
                    get_text('poll.default_options.3')
                ]
            }

        # 使用事件监听方式等待转发消息
        logger.info(f"等待频道消息转发到讨论组...")

        # 创建事件Future来等待转发消息
        from asyncio import Future
        forward_message_future = Future()

        # 定义事件处理器
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

            try:
                # 1. 严格清洗并截断
                question_text = str(poll_data.get('question', get_text('poll_regen.default_question'))).strip()[:250]

                # 2. 构造选项，选项的text也必须是TextWithEntities类型
                poll_answers = []
                for i, opt in enumerate(poll_data.get('options', [])[:10]):
                    opt_clean = str(opt).strip()[:100]
                    poll_answers.append(PollAnswer(
                        text=TextWithEntities(opt_clean, entities=[]),  # 必须包装为TextWithEntities
                        option=bytes([i])
                    ))

                # 3. 手动构造Poll对象（question必须包装为TextWithEntities）
                poll_obj = Poll(
                    id=0,
                    question=TextWithEntities(question_text, entities=[]),  # 必须包装为TextWithEntities，并传入空的entities列表
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
                        get_text('poll_regen.request_button', count=0, threshold=POLL_REGEN_THRESHOLD),
                        data=f"request_regen_{summary_message_id}".encode('utf-8')
                    )])
                # 添加管理员重新生成按钮
                button_markup.append([Button.inline(
                    get_text('poll_regen.admin_button'),
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
                from ..config import add_poll_regeneration
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
                options_text = "\n".join([f"• {opt}" for opt in poll_data['options']])
                await client.send_message(
                    discussion_group_id,
                    get_text('poll.timeout_fallback', question=poll_data['question'], options=options_text)
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