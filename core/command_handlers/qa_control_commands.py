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
问答Bot控制命令处理
允许管理员通过主Bot控制问答Bot的启停和状态查询
"""

import logging

from ..config import ADMIN_LIST
from ..database import get_db_manager
from ..i18n import get_text
from ..process_manager import (
    format_uptime,
    get_qa_bot_status,
    restart_qa_bot,
    start_qa_bot,
    stop_qa_bot,
)

logger = logging.getLogger(__name__)


async def handle_qa_status(event):
    """处理/qa_status命令，显示问答Bot运行状态"""
    sender_id = event.sender_id
    command = event.text
    logger.info(f"收到命令: {command}，发送者: {sender_id}")

    if sender_id not in ADMIN_LIST and ADMIN_LIST != ["me"]:
        logger.warning(f"发送者 {sender_id} 没有权限执行命令 {command}")
        await event.reply(get_text("error.admin_only"))
        return

    try:
        # 获取状态
        status = get_qa_bot_status()

        # 构建状态消息
        lines = [get_text("qabot.status_title") + "\n"]

        # 运行状态
        if status["running"]:
            status_emoji = get_text("qabot.status_running")
            uptime_str = format_uptime(status["uptime_seconds"])
            lines.append(f"**状态**: {status_emoji}")
            lines.append(f"**进程ID**: `{status['pid']}`")
            lines.append(f"**{get_text('qabot.status_uptime')}**: {uptime_str}")
        else:
            status_emoji = get_text("qabot.status_stopped")
            lines.append(f"**状态**: {status_emoji}")
            lines.append(f"**进程ID**: {get_text('qabot.status_not_running')}")
            lines.append(f"**{get_text('qabot.status_uptime')}**: -")

        lines.append("")

        # 配置状态
        enabled_status = (
            get_text("qabot.status_enabled")
            if status["enabled"]
            else get_text("qabot.status_disabled")
        )
        token_status = (
            get_text("qabot.status_token_configured_yes")
            if status["token_configured"]
            else get_text("qabot.status_token_configured_no")
        )

        lines.append(f"**{get_text('qabot.status_feature_enabled')}**: {enabled_status}")
        lines.append(f"**{get_text('qabot.status_token_configured')}**: {token_status}")

        # 如果启用但未运行，给出提示
        if status["enabled"] and status["token_configured"] and not status["running"]:
            lines.append("")
            lines.append(get_text("qabot.status_tip_start"))

        # 详细统计信息
        lines.append("")
        lines.append(get_text("qabot.stats_title"))

        try:
            db = get_db_manager()
            stats = db.get_qa_bot_statistics()

            lines.append(get_text("qabot.total_users", count=stats.get("total_users", 0)))
            lines.append(get_text("qabot.active_users", count=stats.get("active_users", 0)))
            lines.append(get_text("qabot.queries_today", count=stats.get("queries_today", 0)))
            lines.append(get_text("qabot.total_queries", count=stats.get("total_queries", 0)))
            lines.append(
                get_text("qabot.total_subscriptions", count=stats.get("total_subscriptions", 0))
            )
            lines.append(get_text("qabot.pending_requests", count=stats.get("pending_requests", 0)))

        except Exception as e:
            logger.error(f"获取统计数据失败: {type(e).__name__}: {e}")
            lines.append(get_text("qabot.stats_unavailable"))

        lines.append("")
        lines.append(get_text("qabot.management_commands"))
        lines.append("`/qa_start` - " + get_text("qabot.cmd.qa_start"))
        lines.append("`/qa_stop` - " + get_text("qabot.cmd.qa_stop"))
        lines.append("`/qa_restart` - " + get_text("qabot.cmd.qa_restart"))
        lines.append("`/qa_stats` - " + get_text("qabot.cmd.qa_stats"))

        message = "\n".join(lines)
        await event.reply(message, parse_mode="Markdown")

    except Exception as e:
        logger.error(f"处理命令失败: {type(e).__name__}: {e}", exc_info=True)
        await event.reply(f"❌ {get_text('error.unknown')}: {e}")


async def handle_qa_start(event):
    """处理/qa_start命令，启动问答Bot"""
    sender_id = event.sender_id
    command = event.text
    logger.info(f"收到命令: {command}，发送者: {sender_id}")

    if sender_id not in ADMIN_LIST and ADMIN_LIST != ["me"]:
        logger.warning(f"发送者 {sender_id} 没有权限执行命令 {command}")
        await event.reply(get_text("error.admin_only"))
        return

    try:
        # 先检查当前状态
        status = get_qa_bot_status()

        if status["running"]:
            await event.reply(get_text("qabot.already_running", pid=status["pid"]))
            return

        if not status["enabled"]:
            await event.reply(get_text("qabot.not_enabled"))
            return

        if not status["token_configured"]:
            await event.reply(get_text("qabot.token_not_configured"))
            return

        # 启动
        await event.reply(get_text("qabot.starting"))
        result = start_qa_bot()

        if result["success"]:
            message = f"✅ {result['message']}\n\n💡 {get_text('qabot.tip_view_status')}"
            await event.reply(message)
        else:
            await event.reply(f"❌ {result['message']}")

    except Exception as e:
        logger.error(f"处理命令失败: {type(e).__name__}: {e}", exc_info=True)
        await event.reply(f"❌ {get_text('error.unknown')}: {e}")


async def handle_qa_stop(event):
    """处理/qa_stop命令，停止问答Bot"""
    sender_id = event.sender_id
    command = event.text
    logger.info(f"收到命令: {command}，发送者: {sender_id}")

    if sender_id not in ADMIN_LIST and ADMIN_LIST != ["me"]:
        logger.warning(f"发送者 {sender_id} 没有权限执行命令 {command}")
        await event.reply(get_text("error.admin_only"))
        return

    try:
        # 先检查当前状态
        status = get_qa_bot_status()

        if not status["running"]:
            await event.reply(get_text("qabot.not_running"))
            return

        # 停止
        await event.reply(get_text("qabot.stopping"))
        result = stop_qa_bot()

        if result["success"]:
            await event.reply(f"✅ {result['message']}")
        else:
            await event.reply(f"❌ {result['message']}")

    except Exception as e:
        logger.error(f"处理命令失败: {type(e).__name__}: {e}", exc_info=True)
        await event.reply(f"❌ {get_text('error.unknown')}: {e}")


async def handle_qa_restart(event):
    """处理/qa_restart命令，重启问答Bot"""
    sender_id = event.sender_id
    command = event.text
    logger.info(f"收到命令: {command}，发送者: {sender_id}")

    if sender_id not in ADMIN_LIST and ADMIN_LIST != ["me"]:
        logger.warning(f"发送者 {sender_id} 没有权限执行命令 {command}")
        await event.reply(get_text("error.admin_only"))
        return

    try:
        # 先检查配置
        status = get_qa_bot_status()

        if not status["enabled"]:
            await event.reply(get_text("qabot.not_enabled"))
            return

        if not status["token_configured"]:
            await event.reply(get_text("qabot.token_not_configured"))
            return

        # 重启
        await event.reply(get_text("qabot.restarting"))
        result = restart_qa_bot()

        if result["success"]:
            message = f"✅ {result['message']}\n\n💡 {get_text('qabot.tip_view_status')}"
            await event.reply(message)
        else:
            await event.reply(f"❌ {result['message']}")

    except Exception as e:
        logger.error(f"处理命令失败: {type(e).__name__}: {e}", exc_info=True)
        await event.reply(f"❌ {get_text('error.unknown')}: {e}")


async def handle_qa_stats(event):
    """处理/qa_stats命令，显示问答Bot详细统计信息"""
    sender_id = event.sender_id
    command = event.text
    logger.info(f"收到命令: {command}，发送者: {sender_id}")

    if sender_id not in ADMIN_LIST and ADMIN_LIST != ["me"]:
        logger.warning(f"发送者 {sender_id} 没有权限执行命令 {command}")
        await event.reply(get_text("error.admin_only"))
        return

    try:
        lines = [get_text("qabot.detailed_stats_title") + "\n"]

        # 获取运行状态
        status = get_qa_bot_status()
        lines.append(get_text("qabot.running_status"))
        if status["running"]:
            lines.append(get_text("qabot.stats_running"))
            lines.append(f"• Process ID: `{status['pid']}`")
            lines.append(
                f"• {get_text('qabot.status_uptime')}: {format_uptime(status['uptime_seconds'])}"
            )
        else:
            lines.append(get_text("qabot.stats_stopped"))
        lines.append("")

        # 获取数据库统计
        try:
            db = get_db_manager()
            stats = db.get_qa_bot_statistics()

            lines.append(get_text("qabot.user_stats"))
            lines.append(get_text("qabot.total_users", count=stats.get("total_users", 0)))
            lines.append(get_text("qabot.active_users", count=stats.get("active_users", 0)))
            lines.append(get_text("qabot.new_users_today", count=stats.get("new_users_today", 0)))
            lines.append("")

            lines.append(get_text("qabot.query_stats"))
            lines.append(get_text("qabot.queries_today", count=stats.get("queries_today", 0)))
            lines.append(get_text("qabot.queries_week", count=stats.get("queries_week", 0)))
            lines.append(get_text("qabot.total_queries", count=stats.get("total_queries", 0)))
            lines.append("")

            lines.append(get_text("qabot.subscription_stats"))
            lines.append(
                get_text("qabot.total_subscriptions", count=stats.get("total_subscriptions", 0))
            )
            lines.append(
                get_text("qabot.active_subscriptions", count=stats.get("active_subscriptions", 0))
            )
            lines.append("")

            lines.append(get_text("qabot.request_stats"))
            lines.append(get_text("qabot.pending_requests", count=stats.get("pending_requests", 0)))
            lines.append(
                get_text(
                    "qabot.completed_requests_today", count=stats.get("completed_requests_today", 0)
                )
            )
            lines.append(get_text("qabot.total_requests", count=stats.get("total_requests", 0)))
            lines.append("")

            # 显示活跃用户列表（最多显示10个）
            top_users = stats.get("top_users", [])
            if top_users:
                lines.append(get_text("qabot.top_users"))
                for i, user in enumerate(top_users[:10], 1):
                    username = (
                        user.get("username")
                        or user.get("first_name")
                        or f"User{user.get('user_id')}"
                    )
                    query_count = user.get("query_count", 0)
                    lines.append(
                        get_text("qabot.user_rank_item", index=i, name=username, count=query_count)
                    )
                lines.append("")

            # 显示订阅频道分布
            channel_subs = stats.get("channel_subscriptions", {})
            if channel_subs:
                lines.append(get_text("qabot.channel_distribution"))
                for channel_name, count in sorted(
                    channel_subs.items(), key=lambda x: x[1], reverse=True
                ):
                    lines.append(
                        get_text("qabot.channel_sub_item", channel=channel_name, count=count)
                    )
                lines.append("")

        except Exception as e:
            logger.error(f"获取统计数据失败: {type(e).__name__}: {e}")
            lines.append(get_text("qabot.stats_error"))
            lines.append("")

        lines.append(get_text("qabot.tip_view_brief"))

        message = "\n".join(lines)
        await event.reply(message, parse_mode="Markdown")

    except Exception as e:
        logger.error(f"处理命令失败: {type(e).__name__}: {e}", exc_info=True)
        await event.reply(f"❌ {get_text('error.unknown')}: {e}")
