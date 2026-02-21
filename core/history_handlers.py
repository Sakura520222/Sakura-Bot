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
历史记录相关命令处理
"""

import inspect
import os
from datetime import UTC, datetime, timedelta

from .config import ADMIN_LIST, CHANNELS, logger
from .database import get_db_manager
from .i18n import get_text
from .telegram_client import send_long_message


async def handle_history(event):
    """处理 /history 命令，查看历史总结"""
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
        channel_id = None
        days = None

        if len(parts) > 1:
            # 有频道参数
            channel_part = parts[1]
            if channel_part.startswith("http"):
                channel_id = channel_part
            else:
                channel_id = f"https://t.me/{channel_part}"

            # 验证频道是否存在
            if channel_id not in CHANNELS:
                await event.reply(get_text("error.channel_not_found", channel=channel_id))
                return

        if len(parts) > 2:
            # 有天数参数
            try:
                days = int(parts[2])
            except ValueError:
                await event.reply(get_text("history.days_invalid"))
                return

        # 查询数据库
        db = get_db_manager()

        # 如果指定了天数，计算起始日期
        start_date = None
        if days:
            start_date = datetime.now(UTC) - timedelta(days=days)

        # 兼容同步和异步数据库管理器
        if inspect.iscoroutinefunction(db.get_summaries):
            summaries = await db.get_summaries(channel_id=channel_id, limit=10, start_date=start_date)
        else:
            summaries = db.get_summaries(channel_id=channel_id, limit=10, start_date=start_date)

        if not summaries:
            if channel_id:
                await event.reply(get_text("history.no_records", channel=channel_id.split("/")[-1]))
            else:
                await event.reply(get_text("history.all_no_records"))
            return

        # 格式化输出
        channel_name = (
            summaries[0].get("channel_name", get_text("channel.unknown"))
            if channel_id
            else get_text("channel.all")
        )
        total_count = len(summaries)

        result = f"📋 **{channel_name} {get_text('history.title_suffix')}**\n\n"
        result += get_text("history.found_count", count=total_count, display=min(total_count, 10))

        for i, summary in enumerate(summaries[:10], 1):
            created_at = summary.get("created_at", get_text("history.unknown_time"))
            summary_type = summary.get("summary_type", "weekly")
            message_count = summary.get("message_count", 0)
            summary_text = summary.get("summary_text", "")
            summary_message_ids = summary.get("summary_message_ids", [])

            # 类型中文映射
            type_map = {
                "daily": get_text("history.type_daily"),
                "weekly": get_text("history.type_weekly"),
                "manual": get_text("history.type_manual"),
            }
            type_cn = type_map.get(summary_type, summary_type)

            # 格式化时间
            try:
                dt = datetime.fromisoformat(created_at)
                time_str = dt.strftime("%Y-%m-%d %H:%M")
            except Exception:
                time_str = created_at

            # 提取摘要(前150字符)
            summary_preview = (
                summary_text[:150].replace("\n", " ") + "..."
                if len(summary_text) > 150
                else summary_text
            )

            # 生成链接(如果有消息ID)
            channel_link = summary.get("channel_id", "")
            msg_link = ""
            if summary_message_ids and channel_link:
                first_msg_id = summary_message_ids[0]
                channel_part = channel_link.split("/")[-1]
                msg_link = get_text("history.view_full", channel=channel_part, msg_id=first_msg_id)

            result += f"🔹 **{time_str}** ({type_cn})\n"
            result += f"   📊 {get_text('history.processing')}: {message_count} {get_text('history.messages')}\n"
            result += f"   💬 {get_text('history.key_points')}:\n   {summary_preview}{msg_link}\n\n"

        result += get_text("history.tip_export")

        logger.info(f"执行命令 {command} 成功，返回 {total_count} 条记录")
        await send_long_message(event.client, sender_id, result)

    except Exception as e:
        logger.error(f"执行命令 {command} 时出错: {type(e).__name__}: {e}", exc_info=True)
        await event.reply(get_text("history.query_error", error=e))


async def handle_export(event):
    """处理 /export 命令，导出历史记录"""
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
        channel_id = None
        output_format = "json"  # 默认格式

        if len(parts) > 1:
            # 有频道参数
            channel_part = parts[1]
            if channel_part.startswith("http"):
                channel_id = channel_part
            else:
                # 可能是频道名或格式
                if channel_part.lower() in ["json", "csv", "md"]:
                    output_format = channel_part.lower()
                else:
                    channel_id = f"https://t.me/{channel_part}"

        if len(parts) > 2:
            # 第二个参数可能是格式或频道
            second_param = parts[2].lower()
            if second_param in ["json", "csv", "md"]:
                output_format = second_param

        # 如果指定了频道，验证是否存在
        if channel_id and channel_id not in CHANNELS:
            await event.reply(get_text("error.channel_not_found", channel=channel_id))
            return

        await event.reply(get_text("history.exporting"))

        # 导出数据
        db = get_db_manager()
        # 兼容同步和异步数据库管理器
        if inspect.iscoroutinefunction(db.export_summaries):
            filename = await db.export_summaries(output_format=output_format, channel_id=channel_id)
        else:
            filename = db.export_summaries(output_format=output_format, channel_id=channel_id)

        if filename:
            # 发送文件
            await event.client.send_file(
                sender_id,
                filename,
                caption=get_text("history.export_done", format=output_format, filename=filename),
            )

            logger.info(f"成功导出历史记录: {filename}")

            # 删除临时文件
            try:
                os.remove(filename)
            except Exception:
                pass
        else:
            await event.reply(get_text("history.export_no_data"))

    except Exception as e:
        logger.error(f"执行命令 {command} 时出错: {type(e).__name__}: {e}", exc_info=True)
        await event.reply(get_text("history.export_error", error=e))


async def handle_stats(event):
    """处理 /stats 命令，查看统计数据"""
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
        channel_id = None

        if len(parts) > 1:
            # 有频道参数
            channel_part = parts[1]
            if channel_part.startswith("http"):
                channel_id = channel_part
            else:
                channel_id = f"https://t.me/{channel_part}"

            # 验证频道是否存在
            if channel_id not in CHANNELS:
                await event.reply(get_text("error.channel_not_found", channel=channel_id))
                return

        db = get_db_manager()

        if channel_id:
            # 显示指定频道的统计
            # 兼容同步和异步数据库管理器
            if inspect.iscoroutinefunction(db.get_statistics):
                stats = await db.get_statistics(channel_id=channel_id)
            else:
                stats = db.get_statistics(channel_id=channel_id)
            channel_name = channel_id.split("/")[-1]

            if not stats or stats.get("total_count", 0) == 0:
                await event.reply(get_text("history.stats_no_data", channel=channel_name))
                return

            result = get_text("history.stats_title", channel=channel_name) + "\n\n"

            # 总结统计
            result += get_text("history.stats_summary") + "\n"
            result += f"• {get_text('history.total_summaries')}: {stats['total_count']} {get_text('history.times')}\n"

            type_stats = stats.get("type_stats", {})
            type_map = {
                "daily": get_text("history.type_daily"),
                "weekly": get_text("history.type_weekly"),
                "manual": get_text("history.type_manual"),
            }
            for type_key, type_name in type_map.items():
                count = type_stats.get(type_key, 0)
                if count > 0:
                    result += f"  - {type_name}: {count} {get_text('history.times')}\n"

            result += f"• {get_text('history.total_messages')}: {stats['total_messages']:,} {get_text('history.messages')}\n"
            result += f"• {get_text('history.avg_per_summary')}: {stats['avg_messages']} {get_text('history.messages')}\n\n"

            # 时间分布
            result += get_text("history.time_distribution") + "\n"
            result += get_text("history.week_count", count=stats["week_count"]) + "\n"
            result += get_text("history.month_count", count=stats["month_count"]) + "\n"

            last_time = stats.get("last_summary_time")
            if last_time:
                try:
                    dt = datetime.fromisoformat(last_time)
                    if dt.tzinfo is None:
                        dt = dt.replace(tzinfo=UTC)
                    time_diff = datetime.now(UTC) - dt
                    hours = time_diff.total_seconds() / 3600
                    if hours < 1:
                        time_str = get_text("history.minutes_ago", minutes=int(hours * 60))
                    elif hours < 24:
                        time_str = get_text("history.hours_ago", hours=int(hours))
                    else:
                        time_str = get_text("history.days_ago", days=int(hours / 24))
                    result += f"• {get_text('history.last_summary')}: {time_str}\n\n"
                except Exception:
                    result += f"• {get_text('history.last_summary')}: {last_time}\n\n"

            # 数据库信息
            result += get_text("history.db_info") + "\n"
            result += get_text("history.db_records", count=stats["total_count"]) + "\n"

        else:
            # 显示所有频道的统计
            result = get_text("history.overview_title") + "\n\n"

            # 获取各频道统计
            # 兼容同步和异步数据库管理器
            if inspect.iscoroutinefunction(db.get_channel_ranking):
                channel_ranking = await db.get_channel_ranking(limit=10)
            else:
                channel_ranking = db.get_channel_ranking(limit=10)

            if not channel_ranking:
                await event.reply(get_text("history.all_no_records"))
                return

            result += get_text("history.ranking_title") + "\n\n"
            for i, channel_stats in enumerate(channel_ranking, 1):
                channel_name = channel_stats.get(
                    "channel_name", channel_stats.get("channel_id", get_text("channel.unknown"))
                )
                summary_count = channel_stats.get("summary_count", 0)
                total_messages = channel_stats.get("total_messages", 0)
                avg_messages = int(total_messages / summary_count) if summary_count > 0 else 0

                result += (
                    get_text(
                        "history.ranking_item",
                        index=i,
                        name=channel_name,
                        summary_count=summary_count,
                        total_messages=total_messages,
                        avg_messages=avg_messages,
                    )
                    + "\n\n"
                )

            # 总体统计
            # 兼容同步和异步数据库管理器
            if inspect.iscoroutinefunction(db.get_statistics):
                overall_stats = await db.get_statistics()
            else:
                overall_stats = db.get_statistics()
            result += "---\n\n"
            result += get_text("history.overall_stats") + "\n"
            result += (
                get_text(
                    "history.overall_summary",
                    total=overall_stats["total_count"],
                    messages=overall_stats["total_messages"],
                    channels=len(channel_ranking),
                )
                + "\n\n"
            )

        logger.info(f"执行命令 {command} 成功")
        await event.reply(result)

    except Exception as e:
        logger.error(f"执行命令 {command} 时出错: {type(e).__name__}: {e}", exc_info=True)
        await event.reply(get_text("history.stats_error", error=e))
