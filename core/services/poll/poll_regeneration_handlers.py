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
投票重新生成处理器
"""

import logging

from telethon import Button

from core.config import (
    ADMIN_LIST,
    get_poll_regeneration,
    increment_vote_count,
    load_poll_regenerations,
    reset_vote_count,
    update_poll_regeneration,
)
from core.i18n.i18n import get_text
from core.infrastructure.config.poll_config import get_channel_poll_config

logger = logging.getLogger(__name__)


async def handle_vote_regen_request_callback(event):
    """处理投票重新生成请求按钮的回调

    允许任何人点击并记录点击数，当达到阈值时自动重新生成投票
    """
    callback_data = event.data.decode("utf-8")
    sender_id = event.query.user_id

    logger.info(f"收到投票重新生成请求: {callback_data}, 来自用户: {sender_id}")

    # 解析callback数据
    # 格式: request_regen_{summary_message_id}
    parts = callback_data.split("_")
    if len(parts) < 3 or parts[0] != "request" or parts[1] != "regen":
        await event.answer(get_text("poll_regen.invalid_format"), alert=True)
        return

    summary_msg_id = int(parts[-1])

    # 先获取投票重新生成数据以找到频道和投票消息ID
    data = load_poll_regenerations()
    target_channel = None
    poll_msg_id = None

    for channel, records in data.items():
        if str(summary_msg_id) in records:
            target_channel = channel
            poll_msg_id = records[str(summary_msg_id)].get("poll_message_id")
            break

    if not target_channel:
        await event.answer(get_text("poll_regen.data_not_found"), alert=True)
        return

    # 获取频道投票配置
    poll_config = get_channel_poll_config(target_channel)
    regen_threshold = poll_config.get("regen_threshold", 5)
    enable_vote_regen = poll_config.get("enable_vote_regen_request", True)

    # 检查是否启用该功能
    if not enable_vote_regen:
        logger.info("投票重新生成请求功能已禁用")
        await event.answer(get_text("poll_regen.feature_disabled"), alert=True)
        return

    # 增加投票计数（传入正确的频道）
    success, count, already_voted = await increment_vote_count(
        target_channel, summary_msg_id, sender_id
    )

    if not success:
        logger.warning(f"投票重新生成记录不存在或更新失败: summary_msg_id={summary_msg_id}")
        await event.answer(get_text("poll_regen.data_not_found"), alert=True)
        return

    if already_voted:
        # 用户已经投过票了
        await event.answer(
            get_text("poll_regen.already_voted", count=count, threshold=regen_threshold),
            alert=True,
        )
        return

    # 更新按钮文本显示进度
    try:
        new_button_text = get_text(
            "poll_regen.request_button", count=count, threshold=regen_threshold
        )

        button_markup = []
        # 如果启用投票重新生成请求功能，添加请求按钮
        if enable_vote_regen:
            button_markup.append(
                [Button.inline(new_button_text, data=f"request_regen_{summary_msg_id}".encode())]
            )
        # 添加管理员重新生成按钮
        button_markup.append(
            [
                Button.inline(
                    get_text("poll_regen.admin_button"),
                    data=f"regen_poll_{summary_msg_id}".encode(),
                )
            ]
        )

        # 使用 edit_message 方法更新投票消息的按钮
        await event.client.edit_message(
            entity=event.chat_id, message=poll_msg_id, buttons=button_markup
        )
        logger.info(f"✅ 已更新投票消息按钮文本: {new_button_text}")
    except Exception as e:
        logger.error(f"更新按钮文本失败: {e}")
        # 继续执行，按钮更新失败不影响投票逻辑

    # 用户个人提示
    await event.answer(get_text("poll_regen.vote_success", count=count, threshold=regen_threshold))

    # 检查是否达到阈值
    if count >= regen_threshold:
        logger.info(f"🎉 投票数达到阈值: {count}/{regen_threshold}, 开始自动重新生成投票")

        # 自动触发投票重新生成
        regen_data = get_poll_regeneration(target_channel, summary_msg_id)
        if regen_data:
            success = await regenerate_poll(
                client=event.client,
                channel=target_channel,
                summary_msg_id=summary_msg_id,
                regen_data=regen_data,
            )
            if success:
                # 重置投票计数
                reset_success = reset_vote_count(target_channel, summary_msg_id)
                if reset_success:
                    logger.info(
                        f"✅ 投票计数已重置: channel={target_channel}, summary_id={summary_msg_id}"
                    )
            else:
                logger.warning(
                    f"⚠️ 重置投票计数失败: channel={target_channel}, summary_id={summary_msg_id}"
                )
        else:
            logger.error("❌ 未找到投票重新生成数据")
    else:
        logger.info(get_text("poll_regen.current_progress", count=count, threshold=regen_threshold))


async def handle_poll_regeneration_callback(event):
    """处理投票重新生成按钮的回调"""
    callback_data = event.data.decode("utf-8")
    sender_id = event.query.user_id

    logger.info(f"收到投票重新生成请求: {callback_data}, 来自用户: {sender_id}")

    # 1. 权限检查
    if sender_id not in ADMIN_LIST and ADMIN_LIST != ["me"]:
        logger.warning(f"用户 {sender_id} 没有权限重新生成投票")
        await event.answer(get_text("poll_regen.admin_only"), alert=True)
        return

    # 2. 解析callback_data
    # 格式: regen_poll_{summary_message_id}
    parts = callback_data.split("_")
    if len(parts) < 3 or parts[0] != "regen" or parts[1] != "poll":
        await event.answer(get_text("poll_regen.invalid_format"), alert=True)
        return

    summary_msg_id = int(parts[-1])

    # 3. 获取存储的重新生成数据
    # 需要遍历所有频道查找匹配的summary_msg_id
    regen_data = None
    target_channel = None

    data = load_poll_regenerations()
    for channel, records in data.items():
        if str(summary_msg_id) in records:
            regen_data = records[str(summary_msg_id)]
            target_channel = channel
            break

    if not regen_data:
        logger.warning(f"未找到投票重新生成数据: summary_msg_id={summary_msg_id}")
        await event.answer(get_text("poll_regen.data_not_found"), alert=True)
        return

    # 4. 确认操作
    await event.answer(get_text("poll_regen.regen_in_progress"))

    # 5. 执行重新生成逻辑
    # 注意:regen_data['send_to_channel']决定了原投票发送的位置
    # True = 频道模式, False = 讨论组模式
    # 重新生成的投票必须发送到相同的位置
    success = await regenerate_poll(
        client=event.client,
        channel=target_channel,
        summary_msg_id=summary_msg_id,
        regen_data=regen_data,
    )

    if success:
        # 管理员手动重新生成后，必须重置投票计数和用户列表
        reset_success = reset_vote_count(target_channel, summary_msg_id)
        if reset_success:
            logger.info(f"✅ 管理员手动重置，已清空投票计数和名单: {summary_msg_id}")
        else:
            logger.warning(f"⚠️ 重置投票计数失败: {summary_msg_id}")
        logger.info(f"✅ 投票重新生成成功: channel={target_channel}, summary_id={summary_msg_id}")
    else:
        logger.error(f"❌ 投票重新生成失败: channel={target_channel}, summary_id={summary_msg_id}")


async def regenerate_poll(client, channel, summary_msg_id, regen_data):
    """重新生成投票的核心逻辑

    重要: 保持与原投票相同的发送位置
    - 如果原投票在频道(send_to_channel=True),新投票也发到频道
    - 如果原投票在讨论组(send_to_channel=False),新投票也发到讨论组

    Args:
        client: Telegram客户端实例
        channel: 频道URL
        summary_msg_id: 总结消息ID
        regen_data: 投票重新生成数据

    Returns:
        bool: 是否成功
    """
    try:
        # 1. 删除旧的投票和按钮消息
        old_poll_id = regen_data["poll_message_id"]
        old_button_id = regen_data.get("button_message_id")  # 使用 .get() 兼容 None 值

        logger.info(f"删除旧投票和按钮: poll_id={old_poll_id}, button_id={old_button_id}")

        try:
            if regen_data["send_to_channel"]:
                # 频道模式：从频道删除
                if old_button_id:
                    await client.delete_messages(channel, [old_poll_id, old_button_id])
                    logger.info(
                        f"从频道删除旧投票和按钮: poll_id={old_poll_id}, button_id={old_button_id}"
                    )
                else:
                    await client.delete_messages(channel, [old_poll_id])
                    logger.info(f"从频道删除旧投票: poll_id={old_poll_id}")
            else:
                # 讨论组模式：需要先获取讨论组ID，然后从讨论组删除
                # 使用缓存版本避免频繁调用GetFullChannelRequest
                from core.config import get_discussion_group_id_cached

                discussion_group_id = await get_discussion_group_id_cached(client, channel)

                if discussion_group_id:
                    # 从讨论组删除消息
                    if old_button_id:
                        await client.delete_messages(
                            discussion_group_id, [old_poll_id, old_button_id]
                        )
                        logger.info(
                            f"从讨论组删除旧投票和按钮: discussion_group_id={discussion_group_id}, poll_id={old_poll_id}, button_id={old_button_id}"
                        )
                    else:
                        await client.delete_messages(discussion_group_id, [old_poll_id])
                        logger.info(
                            f"从讨论组删除旧投票: discussion_group_id={discussion_group_id}, poll_id={old_poll_id}"
                        )
                else:
                    # 回退到频道删除
                    logger.warning("无法获取讨论组ID，回退到从频道删除")
                    if old_button_id:
                        await client.delete_messages(channel, [old_poll_id, old_button_id])
                        logger.info(
                            f"回退：从频道删除旧投票和按钮: poll_id={old_poll_id}, button_id={old_button_id}"
                        )
                    else:
                        await client.delete_messages(channel, [old_poll_id])
                        logger.info(f"回退：从频道删除旧投票: poll_id={old_poll_id}")

            logger.info(get_text("poll_regen.poll_deleted"))
        except Exception as e:
            logger.warning(get_text("poll_regen.delete_warning") + f": {e}")

        # 2. 生成新的投票内容
        from core.ai.ai_client import generate_poll_from_summary

        summary_text = regen_data["summary_text"]
        logger.info(get_text("poll_regen.generating"))
        new_poll_data = await generate_poll_from_summary(summary_text)
        logger.info(f"✅ 新投票生成成功: {new_poll_data['question']}")

        # 3. 根据原投票的发送位置,发送新投票
        if regen_data["send_to_channel"]:
            # 原投票在频道,新投票也发到频道
            logger.info("原投票发送位置: 频道模式, 新投票也将发送到频道")
            success = await send_new_poll_to_channel(client, channel, summary_msg_id, new_poll_data)
        else:
            # 原投票在讨论组,新投票也发到讨论组
            logger.info("原投票发送位置: 讨论组模式, 新投票也将发送到讨论组")
            success = await send_new_poll_to_discussion_group(
                client, channel, summary_msg_id, new_poll_data, regen_data
            )

        return success

    except Exception as e:
        logger.error(f"重新生成投票时出错: {type(e).__name__}: {e}", exc_info=True)
        return False


async def send_new_poll_to_channel(client, channel, summary_msg_id, poll_data):
    """发送新投票到频道并更新按钮

    使用高层 API 发送投票并附加按钮

    Args:
        client: Telegram客户端实例
        channel: 频道URL
        summary_msg_id: 总结消息ID
        poll_data: 投票数据

    Returns:
        bool: 是否成功
    """
    try:
        from telethon.tl.types import InputMediaPoll, Poll, PollAnswer, TextWithEntities

        # 获取频道投票配置
        poll_config = get_channel_poll_config(channel)
        regen_threshold = poll_config.get("regen_threshold", 5)
        enable_vote_regen = poll_config.get("enable_vote_regen_request", True)

        # 1. 构造投票对象
        question_text = str(
            poll_data.get("question", get_text("poll_regen.default_question"))
        ).strip()[:250]

        poll_answers = []
        for i, opt in enumerate(poll_data.get("options", [])[:10]):
            opt_clean = str(opt).strip()[:100]
            poll_answers.append(
                PollAnswer(text=TextWithEntities(text=opt_clean, entities=[]), option=bytes([i]))
            )

        poll_obj = Poll(
            id=0,
            question=TextWithEntities(text=question_text, entities=[]),
            answers=poll_answers,
            closed=False,
            public_voters=False,
            multiple_choice=False,
            quiz=False,
        )

        # 2. 构造内联按钮
        button_markup = []
        # 如果启用投票重新生成请求功能，添加请求按钮
        if enable_vote_regen:
            button_markup.append(
                [
                    Button.inline(
                        get_text("poll_regen.request_button", count=0, threshold=regen_threshold),
                        data=f"request_regen_{summary_msg_id}".encode(),
                    )
                ]
            )
        # 添加管理员重新生成按钮
        button_markup.append(
            [
                Button.inline(
                    get_text("poll_regen.admin_button"),
                    data=f"regen_poll_{summary_msg_id}".encode(),
                )
            ]
        )

        # 3. 使用 send_message 发送投票并附加按钮
        poll_msg = await client.send_message(
            channel,
            file=InputMediaPoll(poll=poll_obj),
            buttons=button_markup,
            reply_to=int(summary_msg_id),
        )

        logger.info(get_text("poll_regen.sent_to_channel") + f", 消息ID: {poll_msg.id}")

        # 更新 poll_regenerations.json 存储
        update_poll_regeneration(
            channel=channel,
            summary_msg_id=summary_msg_id,
            poll_msg_id=poll_msg.id,
            button_msg_id=None,  # 按钮直接附加在投票消息上，无需单独存储
        )

        # 4. 更新 .last_summary_time.json 中的投票ID
        from .summary_time_manager import load_last_summary_time, save_last_summary_time

        channel_data = load_last_summary_time(channel, include_report_ids=True)
        if channel_data:
            # 保留原有的总结时间戳，只更新投票ID
            original_time = channel_data.get("time")
            summary_ids = channel_data.get("summary_message_ids", [])
            # 更新投票ID为新的，按钮ID为None，使用原有的时间戳
            save_last_summary_time(
                channel,
                original_time,
                summary_message_ids=summary_ids,
                poll_message_ids=[poll_msg.id],
                button_message_ids=None,
            )
            logger.info(get_text("poll_regen.updated_storage") + "（保留原有时间戳）")
        else:
            logger.warning(f"⚠️ 未找到频道 {channel} 的 .last_summary_time.json 记录")

        return True

    except Exception as e:
        logger.error(f"发送新投票到频道失败: {e}", exc_info=True)
        return False


async def send_new_poll_to_discussion_group(client, channel, summary_msg_id, poll_data, regen_data):
    """发送新投票到讨论组并更新按钮

    关键改进: 使用存储的转发消息ID,而不是等待新的转发消息

    Args:
        client: Telegram客户端实例
        channel: 频道URL
        summary_msg_id: 总结消息ID
        poll_data: 投票数据
        regen_data: 重新生成数据,包含存储的转发消息ID

    Returns:
        bool: 是否成功
    """
    try:
        from telethon.tl.types import InputMediaPoll, Poll, PollAnswer, TextWithEntities

        # 获取频道投票配置
        poll_config = get_channel_poll_config(channel)
        regen_threshold = poll_config.get("regen_threshold", 5)
        enable_vote_regen = poll_config.get("enable_vote_regen_request", True)

        logger.info("开始处理投票发送到讨论组(重新生成模式)")

        # 1. 检查是否有存储的转发消息ID
        if (
            "discussion_forward_msg_id" not in regen_data
            or not regen_data["discussion_forward_msg_id"]
        ):
            logger.error(get_text("poll_regen.no_forward_id"))
            return False

        forward_msg_id = regen_data["discussion_forward_msg_id"]
        logger.info(get_text("poll_regen.using_forward_id") + f": {forward_msg_id}")

        # 2. 获取频道实体和讨论组ID
        # 使用缓存版本避免频繁调用GetFullChannelRequest
        from core.config import get_discussion_group_id_cached

        discussion_group_id = await get_discussion_group_id_cached(client, channel)

        if not discussion_group_id:
            logger.error(get_text("poll_regen.no_discussion"))
            return False

        # 3. 直接使用存储的转发消息ID发送投票,无需等待
        logger.info(f"直接使用存储的转发消息ID {forward_msg_id} 发送投票")

        # 构造投票对象
        question_text = str(
            poll_data.get("question", get_text("poll_regen.default_question"))
        ).strip()[:250]
        poll_answers = []
        for i, opt in enumerate(poll_data.get("options", [])[:10]):
            opt_clean = str(opt).strip()[:100]
            poll_answers.append(
                PollAnswer(text=TextWithEntities(text=opt_clean, entities=[]), option=bytes([i]))
            )

        poll_obj = Poll(
            id=0,
            question=TextWithEntities(text=question_text, entities=[]),
            answers=poll_answers,
            closed=False,
            public_voters=False,
            multiple_choice=False,
            quiz=False,
        )

        # 4. 构造内联按钮
        button_markup = []
        # 如果启用投票重新生成请求功能，添加请求按钮
        if enable_vote_regen:
            button_markup.append(
                [
                    Button.inline(
                        get_text("poll_regen.request_button", count=0, threshold=regen_threshold),
                        data=f"request_regen_{summary_msg_id}".encode(),
                    )
                ]
            )
        # 添加管理员重新生成按钮
        button_markup.append(
            [
                Button.inline(
                    get_text("poll_regen.admin_button"),
                    data=f"regen_poll_{summary_msg_id}".encode(),
                )
            ]
        )

        # 5. 使用 send_message 发送投票并附加按钮
        poll_msg = await client.send_message(
            discussion_group_id,
            file=InputMediaPoll(poll=poll_obj),
            buttons=button_markup,
            reply_to=int(forward_msg_id),
        )

        logger.info(get_text("poll_regen.sent_to_discussion") + f", 消息ID: {poll_msg.id}")

        # 更新 poll_regenerations.json 存储
        update_poll_regeneration(
            channel=channel,
            summary_msg_id=summary_msg_id,
            poll_msg_id=poll_msg.id,
            button_msg_id=None,  # 按钮直接附加在投票消息上，无需单独存储
        )

        # 6. 更新 .last_summary_time.json 中的投票ID
        from .summary_time_manager import load_last_summary_time, save_last_summary_time

        channel_data = load_last_summary_time(channel, include_report_ids=True)
        if channel_data:
            # 保留原有的总结时间戳，只更新投票ID
            original_time = channel_data.get("time")
            summary_ids = channel_data.get("summary_message_ids", [])
            # 更新投票ID为新的，按钮ID为None，使用原有的时间戳
            save_last_summary_time(
                channel,
                original_time,
                summary_message_ids=summary_ids,
                poll_message_ids=[poll_msg.id],
                button_message_ids=None,
            )
            logger.info(get_text("poll_regen.updated_storage") + "（保留原有时间戳）")
        else:
            logger.warning(f"⚠️ 未找到频道 {channel} 的 .last_summary_time.json 记录")

        return True

    except Exception as e:
        logger.error(f"发送新投票到讨论组失败: {e}", exc_info=True)
        return False
