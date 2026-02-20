# Copyright 2026 Sakura-Bot
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
总结相关命令处理
"""

import json
import os
from datetime import UTC, datetime, timedelta
from typing import Any

from ..ai_client import analyze_with_ai
from ..config import (
    ADMIN_LIST,
    CHANNELS,
    LAST_SUMMARY_FILE,
    SEND_REPORT_TO_SOURCE,
    get_channel_schedule,
    logger,
)
from ..i18n import get_text
from ..prompt_manager import load_prompt
from ..summary_time_manager import load_last_summary_time, save_last_summary_time
from ..telegram_client import fetch_last_week_messages, send_long_message, send_report
from ..vector_store import get_vector_store


async def generate_channel_summary(
    channel_id: str, client, skip_admins: bool = False
) -> dict[str, Any]:
    """
    为指定频道生成总结并发送的核心函数

    此函数提取自 handle_manual_summary，用于复用总结生成逻辑
    可被手动命令和请求处理器共同调用

    Args:
        channel_id: 频道ID或URL
        client: Telethon客户端实例
        skip_admins: 是否跳过向管理员发送（默认False）

    Returns:
        dict: {
            'success': bool,              # 是否成功
            'summary_text': str,          # 总结文本（成功时）
            'message_count': int,         # 消息数量
            'channel_name': str,          # 频道名称
            'message_ids': dict,          # 发送的消息ID
            'error': str                  # 错误信息（失败时）
        }
    """
    try:
        logger.info(f"开始为频道生成总结: {channel_id}")

        # 1. 读取该频道的上次总结时间和报告消息ID
        channel_summary_data = load_last_summary_time(channel_id, include_report_ids=True)
        if channel_summary_data:
            channel_last_summary_time = channel_summary_data["time"]
            # 使用新的键名: summary_message_ids
            if "summary_message_ids" in channel_summary_data:
                summary_ids = channel_summary_data["summary_message_ids"]
                # 类型检查和修复
                if isinstance(summary_ids, dict):
                    logger.warning(f"检测到summary_ids是字典格式,正在修复数据结构: {summary_ids}")
                    summary_ids = summary_ids.get("summary_message_ids", [])
                if not isinstance(summary_ids, list):
                    logger.error(f"summary_ids类型错误: {type(summary_ids)}, 使用空列表")
                    summary_ids = []

                poll_ids = channel_summary_data.get("poll_message_ids", [])
                button_ids = channel_summary_data.get("button_message_ids", [])
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

        # 2. 抓取该频道从上次总结时间开始的消息
        messages_by_channel = await fetch_last_week_messages(
            [channel_id],
            start_time=channel_last_summary_time,
            report_message_ids={channel_id: report_message_ids_to_exclude},
        )

        # 3. 获取该频道的消息
        messages = messages_by_channel.get(channel_id, [])
        if not messages:
            logger.info(f"频道 {channel_id} 没有新消息需要总结")
            return {"success": False, "error": f"频道 {channel_id} 没有新消息需要总结"}

        logger.info(f"频道 {channel_id} 发现 {len(messages)} 条新消息，开始生成总结")

        # 4. 获取频道实际名称
        try:
            channel_entity = await client.get_entity(channel_id)
            channel_actual_name = channel_entity.title
            logger.info(f"获取到频道实际名称: {channel_actual_name}")
        except Exception as e:
            logger.warning(f"获取频道实体失败，使用默认名称: {e}")
            channel_actual_name = channel_id.split("/")[-1]

        # 5. AI生成总结
        current_prompt = load_prompt()
        summary = analyze_with_ai(messages, current_prompt)

        # 6. 计算日期范围和生成报告标题
        end_date = datetime.now(UTC)
        if channel_last_summary_time:
            start_date = channel_last_summary_time
        else:
            start_date = end_date - timedelta(days=7)

        start_date_str = f"{start_date.month}.{start_date.day}"
        end_date_str = f"{end_date.month}.{end_date.day}"

        # 获取频道的调度配置，用于生成报告标题
        schedule_config = get_channel_schedule(channel_id)
        frequency = schedule_config.get("frequency", "weekly")

        # 根据频率生成报告标题
        if frequency == "daily":
            report_title = get_text(
                "summary.daily_title", channel=channel_actual_name, date=end_date_str
            )
        else:
            report_title = get_text(
                "summary.weekly_title",
                channel=channel_actual_name,
                start_date=start_date_str,
                end_date=end_date_str,
            )

        # 7. 生成报告文本
        report_text = f"**{report_title}**\n\n{summary}"

        # 8. 保存到数据库和向量存储
        try:
            from ..database import get_db_manager
            from ..telegram_client import extract_date_range_from_summary

            # 提取时间范围
            start_time, end_time = extract_date_range_from_summary(report_text)

            # 保存到数据库
            db = get_db_manager()
            summary_id = db.save_summary(
                channel_id=channel_id,
                channel_name=channel_actual_name,
                summary_text=report_text,
                message_count=len(messages),
                start_time=start_time,
                end_time=end_time,
                summary_message_ids=[],
                poll_message_id=None,
                button_message_id=None,
                ai_model=None,
                summary_type="manual",
            )

            if summary_id:
                logger.info(f"总结已保存到数据库，记录ID: {summary_id}")

                # 生成并保存向量
                vector_store = get_vector_store()
                if vector_store.is_available():
                    success = vector_store.add_summary(
                        summary_id=summary_id,
                        text=report_text,
                        metadata={
                            "channel_id": channel_id,
                            "channel_name": channel_actual_name,
                            "created_at": datetime.now(UTC).isoformat(),
                            "summary_type": "manual",
                            "message_count": len(messages),
                        },
                    )

                    if success:
                        logger.info(f"向量已成功保存，summary_id: {summary_id}")
                    else:
                        logger.warning(
                            f"向量保存失败，但数据库记录已保存，summary_id: {summary_id}"
                        )
                else:
                    logger.debug("向量存储不可用，跳过向量化")
            else:
                logger.warning("保存到数据库失败")

        except Exception as save_error:
            logger.error(
                f"保存总结到数据库/向量存储时出错: {type(save_error).__name__}: {save_error}",
                exc_info=True,
            )
            # 保存失败不影响报告发送

        # 9. 根据配置决定是否向源频道发送总结
        if SEND_REPORT_TO_SOURCE:
            sent_report_ids = await send_report(
                report_text,
                channel_id,
                client,
                skip_admins=skip_admins,
                message_count=len(messages),
            )
        else:
            sent_report_ids = await send_report(
                report_text, None, client, skip_admins=skip_admins, message_count=len(messages)
            )

        # 10. 通知订阅用户（跨Bot推送）
        try:
            from ..mainbot_push_handler import get_mainbot_push_handler

            push_handler = get_mainbot_push_handler()

            notified_count = await push_handler.notify_summary_subscribers(
                channel_id=channel_id, channel_name=channel_actual_name, summary_text=report_text
            )

            if notified_count > 0:
                logger.info(f"已成功通知 {notified_count} 个订阅用户")
        except Exception as e:
            logger.error(f"通知订阅用户失败: {type(e).__name__}: {e}", exc_info=True)

        # 11. 保存该频道的本次总结时间和所有相关消息ID
        if sent_report_ids:
            summary_ids = sent_report_ids.get("summary_message_ids", [])
            poll_id = sent_report_ids.get("poll_message_id")
            button_id = sent_report_ids.get("button_message_id")

            # 转换单个ID为列表格式
            poll_ids = [poll_id] if poll_id else []
            button_ids = [button_id] if button_id else []

            save_last_summary_time(
                channel_id,
                datetime.now(UTC),
                summary_message_ids=summary_ids,
                poll_message_ids=poll_ids,
                button_message_ids=button_ids,
            )
        else:
            save_last_summary_time(channel_id, datetime.now(UTC))

        logger.info(f"频道 {channel_id} 总结生成完成")

        # 返回成功结果
        return {
            "success": True,
            "summary_text": report_text,
            "message_count": len(messages),
            "channel_name": channel_actual_name,
            "message_ids": sent_report_ids or {},
        }

    except Exception as e:
        logger.error(f"为频道 {channel_id} 生成总结时出错: {type(e).__name__}: {e}", exc_info=True)
        return {"success": False, "error": f"{type(e).__name__}: {str(e)}"}


async def handle_manual_summary(event):
    """处理/立即总结命令"""
    sender_id = event.sender_id
    command = event.text
    logger.info(f"收到命令: {command}，发送者: {sender_id}")

    # 检查发送者是否为管理员
    if sender_id not in ADMIN_LIST and ADMIN_LIST != ["me"]:
        logger.warning(f"发送者 {sender_id} 没有权限执行命令 {command}")
        await event.reply(get_text("error.permission_denied"))
        return

    # 发送正在处理的消息
    await event.reply(get_text("summary.generating"))
    logger.info(f"开始执行 {command} 命令")

    # 解析命令参数，支持指定频道
    try:
        # 分割命令和参数
        parts = command.split()
        if len(parts) > 1:
            # 有指定频道参数
            specified_channels = []
            for part in parts[1:]:
                if part.startswith("http"):
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
                    await event.reply(get_text("channel.will_skip", channel=channel))

            if not valid_channels:
                await event.reply(get_text("channel.no_valid"))
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
                        logger.warning(
                            f"检测到summary_ids是字典格式,正在修复数据结构: {summary_ids}"
                        )
                        summary_ids = summary_ids.get("summary_message_ids", [])
                    # 确保是列表
                    if not isinstance(summary_ids, list):
                        logger.error(
                            f"summary_ids类型错误: {type(summary_ids)}, 值: {summary_ids}, 使用空列表"
                        )
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
                report_message_ids={channel: report_message_ids_to_exclude},
            )

            # 获取该频道的消息
            messages = messages_by_channel.get(channel, [])
            if messages:
                logger.info(
                    get_text("summary.start_processing", channel=channel, count=len(messages))
                )
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
                    channel_actual_name = channel.split("/")[-1]
                # 计算起始日期和终止日期
                end_date = datetime.now(UTC)
                if channel_last_summary_time:
                    start_date = channel_last_summary_time
                else:
                    start_date = end_date - timedelta(days=7)
                # 格式化日期为 月.日 格式
                start_date_str = f"{start_date.month}.{start_date.day}"
                end_date_str = f"{end_date.month}.{end_date.day}"

                # 获取频道的调度配置，用于生成报告标题
                schedule_config = get_channel_schedule(channel)
                frequency = schedule_config.get("frequency", "weekly")

                # 根据频率生成报告标题
                if frequency == "daily":
                    report_title = get_text(
                        "summary.daily_title", channel=channel_actual_name, date=end_date_str
                    )
                else:  # weekly
                    report_title = get_text(
                        "summary.weekly_title",
                        channel=channel_actual_name,
                        start_date=start_date_str,
                        end_date=end_date_str,
                    )

                # 生成报告文本
                report_text = f"**{report_title}**\n\n{summary}"

                # ✅ v3.0.0新增：先保存到数据库和向量存储
                try:
                    from ..database import get_db_manager
                    from ..telegram_client import extract_date_range_from_summary

                    # 提取时间范围
                    start_time, end_time = extract_date_range_from_summary(report_text)

                    # 保存到数据库
                    db = get_db_manager()
                    summary_id = db.save_summary(
                        channel_id=channel,
                        channel_name=channel_actual_name,
                        summary_text=report_text,
                        message_count=len(messages),
                        start_time=start_time,
                        end_time=end_time,
                        summary_message_ids=[],
                        poll_message_id=None,
                        button_message_id=None,
                        ai_model=None,  # 使用默认配置
                        summary_type="manual",
                    )

                    if summary_id:
                        logger.info(f"总结已保存到数据库，记录ID: {summary_id}")

                        # 生成并保存向量
                        vector_store = get_vector_store()
                        if vector_store.is_available():
                            success = vector_store.add_summary(
                                summary_id=summary_id,
                                text=report_text,
                                metadata={
                                    "channel_id": channel,
                                    "channel_name": channel_actual_name,
                                    "created_at": datetime.now(UTC).isoformat(),
                                    "summary_type": "manual",
                                    "message_count": len(messages),
                                },
                            )

                            if success:
                                logger.info(f"向量已成功保存，summary_id: {summary_id}")
                            else:
                                logger.warning(
                                    f"向量保存失败，但数据库记录已保存，summary_id: {summary_id}"
                                )
                        else:
                            logger.debug("向量存储不可用，跳过向量化")
                    else:
                        logger.warning("保存到数据库失败")

                except Exception as save_error:
                    logger.error(
                        f"保存总结到数据库/向量存储时出错: {type(save_error).__name__}: {save_error}",
                        exc_info=True,
                    )
                    # 保存失败不影响报告发送

                # 向请求者发送总结
                await send_long_message(event.client, sender_id, report_text)
                # 根据配置决定是否向源频道发送总结，传递现有客户端实例避免数据库锁定
                # 如果请求者是管理员，跳过向管理员发送报告，避免重复发送
                skip_admins = sender_id in ADMIN_LIST or ADMIN_LIST == ["me"]
                if SEND_REPORT_TO_SOURCE:
                    sent_report_ids = await send_report(
                        report_text,
                        channel,
                        event.client,
                        skip_admins=skip_admins,
                        message_count=len(messages),
                    )
                else:
                    sent_report_ids = await send_report(
                        report_text,
                        None,
                        event.client,
                        skip_admins=skip_admins,
                        message_count=len(messages),
                    )

                # 通知订阅用户（跨Bot推送）
                try:
                    from ..mainbot_push_handler import get_mainbot_push_handler

                    push_handler = get_mainbot_push_handler()

                    notified_count = await push_handler.notify_summary_subscribers(
                        channel_id=channel,
                        channel_name=channel_actual_name,
                        summary_text=report_text,
                    )

                    if notified_count > 0:
                        logger.info(f"已成功通知 {notified_count} 个订阅用户")
                except Exception as e:
                    logger.error(f"通知订阅用户失败: {type(e).__name__}: {e}", exc_info=True)

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
                        datetime.now(UTC),
                        summary_message_ids=summary_ids,
                        poll_message_ids=poll_ids,
                        button_message_ids=button_ids,
                    )
                else:
                    save_last_summary_time(channel, datetime.now(UTC))
            else:
                logger.info(f"频道 {channel} 没有新消息需要总结")
                # 获取频道实际名称用于无消息提示
                try:
                    channel_entity = await event.client.get_entity(channel)
                    channel_actual_name = channel_entity.title
                except Exception:
                    channel_actual_name = channel.split("/")[-1]
                await send_long_message(
                    event.client,
                    sender_id,
                    get_text("summary.no_messages", channel=channel_actual_name),
                )

        logger.info(f"命令 {command} 执行成功")
    except Exception as e:
        logger.error(f"执行命令 {command} 时出错: {type(e).__name__}: {e}", exc_info=True)
        await event.reply(get_text("summary.error", error=e))


async def handle_clear_summary_time(event):
    """处理/clearsummarytime命令，清除上次总结时间记录
    支持清除所有频道或特定频道的时间记录
    """
    sender_id = event.sender_id
    command = event.text
    logger.info(f"收到命令: {command}，发送者: {sender_id}")

    # 检查发送者是否为管理员
    if sender_id not in ADMIN_LIST and ADMIN_LIST != ["me"]:
        logger.warning(f"发送者 {sender_id} 没有权限执行命令 {command}")
        await event.reply(get_text("error.permission_denied"))
        return

    try:
        # 解析命令参数
        parts = command.split()
        specific_channel = None
        if len(parts) > 1:
            # 有指定频道参数
            channel_part = parts[1]
            if channel_part.startswith("http"):
                specific_channel = channel_part
            else:
                specific_channel = f"https://t.me/{channel_part}"

        if os.path.exists(LAST_SUMMARY_FILE):
            if specific_channel:
                # 清除特定频道的时间记录
                with open(LAST_SUMMARY_FILE, encoding="utf-8") as f:
                    content = f.read().strip()
                    if content:
                        existing_data = json.loads(content)
                        if specific_channel in existing_data:
                            del existing_data[specific_channel]
                            # 写回文件
                            with open(LAST_SUMMARY_FILE, "w", encoding="utf-8") as f_write:
                                json.dump(existing_data, f_write, ensure_ascii=False, indent=2)
                            logger.info(f"已清除频道 {specific_channel} 的上次总结时间记录")
                            await event.reply(
                                get_text(
                                    "summarytime.clear_channel_success", channel=specific_channel
                                )
                            )
                        else:
                            logger.info(
                                f"频道 {specific_channel} 的上次总结时间记录不存在，无需清除"
                            )
                            await event.reply(
                                get_text(
                                    "summarytime.clear_channel_not_exist", channel=specific_channel
                                )
                            )
                    else:
                        logger.info(f"上次总结时间记录文件 {LAST_SUMMARY_FILE} 内容为空，无需清除")
                        await event.reply(get_text("summarytime.clear_empty_file"))
            else:
                # 清除所有频道的时间记录
                os.remove(LAST_SUMMARY_FILE)
                logger.info(f"已清除所有频道的上次总结时间记录，文件 {LAST_SUMMARY_FILE} 已删除")
                await event.reply(get_text("summarytime.clear_all_success"))
        else:
            logger.info(f"上次总结时间记录文件 {LAST_SUMMARY_FILE} 不存在，无需清除")
            await event.reply(get_text("summarytime.clear_all_failed"))
    except Exception as e:
        logger.error(f"清除上次总结时间记录时出错: {type(e).__name__}: {e}", exc_info=True)
        await event.reply(get_text("summarytime.clear_error", error=e))
