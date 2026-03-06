# Copyright 2026 Sakura-Bot
#
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
#
# - 署名：必须提供本项目的原始来源链接
# - 相同方式共享：衍生作品必须采用相同的许可证
#
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
# 许可证全文：参见 LICENSE 文件

import asyncio
import logging
import os
import signal
import sys

import aiofiles
from apscheduler.schedulers.asyncio import AsyncIOScheduler
from telethon import TelegramClient
from telethon.events import CallbackQuery, NewMessage
from telethon.tl.functions.bots import SetBotCommandsRequest
from telethon.tl.types import BotCommand, BotCommandScopeDefault

# 添加项目根目录到 Python 路径
sys.path.append(os.path.dirname(os.path.abspath(__file__)))

from core.channel_comment_welcome import (
    get_comment_welcome_handler,
    handle_summary_request_callback,
    initialize_comment_welcome,
)
from core.command_handlers import (
    handle_add_channel,
    handle_ai_config_input,
    handle_changelog,
    handle_clear_cache,
    handle_clear_summary_time,
    handle_delete_channel,
    handle_delete_channel_poll,
    handle_delete_channel_schedule,
    handle_help,
    handle_language,
    handle_manual_summary,
    handle_pause,
    handle_poll_prompt_input,
    handle_prompt_input,
    handle_restart,
    handle_resume,
    handle_set_ai_config,
    handle_set_channel_poll,
    handle_set_channel_schedule,
    handle_set_log_level,
    handle_set_poll_prompt,
    handle_set_prompt,
    handle_set_send_to_source,
    handle_show_ai_config,
    handle_show_channel_poll,
    handle_show_channel_schedule,
    handle_show_channels,
    handle_show_log_level,
    handle_show_poll_prompt,
    handle_show_prompt,
    handle_shutdown,
    handle_start,
)
from core.command_handlers.comment_welcome_commands import (
    handle_delete_comment_welcome,
    handle_set_comment_welcome,
    handle_show_comment_welcome,
)
from core.command_handlers.forwarding_commands import (
    cmd_forwarding_default_footer,
    cmd_forwarding_disable,
    cmd_forwarding_enable,
    cmd_forwarding_footer,
    cmd_forwarding_stats,
    cmd_forwarding_status,
)
from core.command_handlers.other_commands import handle_update
from core.command_handlers.qa_control_commands import (
    handle_qa_restart,
    handle_qa_start,
    handle_qa_stats,
    handle_qa_status,
    handle_qa_stop,
)
from core.command_handlers.userbot_commands import handle_userbot_status
from core.config import (
    ADMIN_LIST,
    CHANNELS,
    RESTART_FLAG_FILE,
    build_cron_trigger,
    get_channel_schedule,
    logger,
)
from core.error_handler import initialize_error_handling
from core.history_handlers import handle_export, handle_history, handle_stats
from core.mainbot_push_handler import get_mainbot_push_handler
from core.mainbot_request_handler import get_mainbot_request_handler
from core.poll_regeneration_handlers import (
    handle_poll_regeneration_callback,
    handle_vote_regen_request_callback,
)
from core.scheduler import main_job
from core.settings import (
    get_api_hash,
    get_api_id,
    get_bot_token,
    validate_required_settings,
)

# 版本信息
__version__ = "1.7.1"

from core.command_handlers.database_migration_commands import (
    handle_db_clear,
    handle_db_clear_cancel,
    handle_db_clear_confirm,
    handle_migrate_check,
    handle_migrate_start,
    handle_migrate_status,
)
from core.database import get_db_manager
from core.process_manager import start_qa_bot, stop_qa_bot


async def graceful_shutdown_resources():
    """优雅关闭所有资源（已废弃，请使用 core.shutdown_manager.ShutdownManager）

    为了向后兼容，保留此函数但重定向到新的关机管理器
    """
    from core.shutdown_manager import get_shutdown_manager
    from core.telegram_client import get_active_client

    shutdown_manager = get_shutdown_manager()
    client = get_active_client()
    await shutdown_manager.graceful_shutdown(client=client)


async def send_startup_message(client):
    """向所有管理员发送启动消息"""
    from core.i18n import get_text

    try:
        # 构建帮助信息（使用 i18n，支持多语言）
        help_text = f"""🤖 **Sakura-Bot v{__version__} 已启动**

**核心功能**
• 自动总结频道消息
• 多频道管理
• 自定义提示词
• AI配置调整
• 定时任务调度

**可用命令**
{get_text("cmd.summary")}
{get_text("cmd.showprompt")}
{get_text("cmd.setprompt")}
{get_text("cmd.showpollprompt")}
{get_text("cmd.setpollprompt")}
{get_text("cmd.showaicfg")}
{get_text("cmd.setaicfg")}
{get_text("cmd.showloglevel")}
{get_text("cmd.setloglevel")}
{get_text("cmd.restart")}
{get_text("cmd.shutdown")}
{get_text("cmd.pause")}
{get_text("cmd.resume")}
{get_text("cmd.showchannels")}
{get_text("cmd.addchannel")}
{get_text("cmd.deletechannel")}
{get_text("cmd.clearsummarytime")}
{get_text("cmd.setsendtosource")}
{get_text("cmd.showchannelschedule")}
{get_text("cmd.setchannelschedule")}
{get_text("cmd.deletechannelschedule")}
{get_text("cmd.channelpoll")}
{get_text("cmd.setchannelpoll")}
{get_text("cmd.deletechannelpoll")}
{get_text("cmd.clearcache")}
{get_text("cmd.history")}
{get_text("cmd.export")}
{get_text("cmd.stats")}
{get_text("cmd.language")}
{get_text("cmd.changelog")}
{get_text("cmd.update")}

**版本信息**
当前版本: v{__version__}

机器人运行正常，随时为您服务！"""

        # 向所有管理员发送消息
        for admin_id in ADMIN_LIST:
            try:
                await client.send_message(admin_id, help_text, parse_mode="md", link_preview=False)
                logger.info(f"已向管理员 {admin_id} 发送启动消息")
            except Exception as e:
                logger.error(f"向管理员 {admin_id} 发送启动消息失败: {type(e).__name__}: {e}")
    except Exception as e:
        logger.error(f"发送启动消息时出错: {type(e).__name__}: {e}", exc_info=True)


async def main():
    # 抑制第三方库的 INFO 日志输出
    logging.getLogger("telethon").setLevel(logging.WARNING)
    logging.getLogger("telethon.client.updates").setLevel(logging.WARNING)

    logger.info(f"开始初始化机器人服务 v{__version__}...")

    try:
        # 初始化错误处理系统
        logger.info("初始化错误处理系统...")
        initialize_error_handling()
        logger.info("错误处理系统初始化完成")

        # 初始化数据库连接（如果使用MySQL）
        logger.info("初始化数据库连接...")
        db_manager = get_db_manager()
        if (
            hasattr(db_manager, "init_database")
            and hasattr(db_manager, "pool")
            and db_manager.pool is None
        ):
            try:
                await db_manager.init_database()
                logger.info("数据库连接池初始化完成")
            except Exception as e:
                # MySQL初始化失败，回退到SQLite
                logger.warning(f"MySQL初始化失败: {e}")
                logger.info("回退到 SQLite...")

                # 强制重新创建
                import core.database as db_module

                db_module.db_manager = None

                # 更新配置
                os.environ["DATABASE_TYPE"] = "sqlite"

                db_manager = get_db_manager()
                if hasattr(db_manager, "init_database"):
                    await db_manager.init_database()
                logger.info("SQLite数据库初始化完成")
        else:
            logger.info("数据库连接已存在或不需要初始化")

        # 执行数据库迁移（转发功能表结构优化）
        logger.info("检查并执行数据库迁移...")
        try:
            from core.migrations.migrate_forwarding_table_v1 import ensure_forwarding_table_updated

            await ensure_forwarding_table_updated(db_manager)
        except Exception as e:
            logger.warning(f"数据库迁移执行失败（可忽略）: {type(e).__name__}: {e}")

        # 初始化调度器
        scheduler = AsyncIOScheduler()

        # 为每个频道配置独立的定时任务
        logger.info(f"开始为 {len(CHANNELS)} 个频道配置定时任务...")
        for channel in CHANNELS:
            # 获取频道的自动总结时间配置（已标准化）
            schedule = get_channel_schedule(channel)

            # 构建 cron 触发器参数
            trigger_params = build_cron_trigger(schedule)

            # 创建定时任务
            scheduler.add_job(
                main_job,
                "cron",
                **trigger_params,  # 解包触发器参数
                args=[channel],  # 传入频道参数
                id=f"summary_job_{channel}",  # 唯一ID，便于管理
                replace_existing=True,
            )

            # 格式化输出信息
            frequency = schedule.get("frequency", "weekly")
            if frequency == "daily":
                frequency_text = "每天"
            elif frequency == "weekly":
                day_map = {
                    "mon": "周一",
                    "tue": "周二",
                    "wed": "周三",
                    "thu": "周四",
                    "fri": "周五",
                    "sat": "周六",
                    "sun": "周日",
                }
                days_cn = "、".join([day_map.get(d, d) for d in schedule.get("days", [])])
                frequency_text = f"每周{days_cn}"
            else:
                frequency_text = "未知"

            logger.info(
                f"频道 {channel} 的定时任务已配置：{frequency_text} {schedule['hour']:02d}:{schedule['minute']:02d}"
            )

        logger.info(f"定时任务配置完成：共 {len(CHANNELS)} 个频道")

        # 添加定期清理任务
        from core.scheduler import cleanup_old_poll_regenerations

        scheduler.add_job(
            cleanup_old_poll_regenerations,
            "cron",
            hour=3,
            minute=0,
            id="cleanup_poll_regenerations",
        )
        logger.info("投票重新生成数据清理任务已配置：每天凌晨3点执行")

        # 添加定期检查请求任务（跨Bot通信）
        # 注意：需要在 client 启动后才能使用，所以这里使用闭包捕获 client
        async def check_requests_job():
            """定期检查并处理来自问答Bot的请求"""
            try:
                # 传递 Telethon client 给请求处理器
                await request_handler.check_requests(telethon_client=client)
            except Exception as e:
                logger.error(f"检查请求任务失败: {type(e).__name__}: {e}")

        scheduler.add_job(
            check_requests_job,
            "interval",
            seconds=30,  # 每30秒检查一次
            id="check_requests",
        )
        logger.info("跨Bot请求检查任务已配置：每30秒执行一次")

        # 添加问答Bot健康检查任务（自动重启）
        async def qa_bot_health_check_job():
            """定期检查问答Bot健康状态，必要时自动重启"""
            try:
                from core.i18n import get_text
                from core.process_manager import check_qa_bot_health, restart_qa_bot

                is_healthy, should_restart, message = check_qa_bot_health()

                if should_restart:
                    logger.warning(f"问答Bot需要自动重启: {message}")

                    # 通知管理员
                    for admin_id in ADMIN_LIST:
                        if admin_id != "me":
                            try:
                                await client.send_message(
                                    admin_id,
                                    f"{get_text('qabot.auto_restart')}\n\n{message}\n\n{get_text('qabot.attempting_recovery')}",
                                    parse_mode="markdown",
                                    link_preview=False,
                                )
                            except Exception as e:
                                logger.error(f"通知管理员失败: {e}")

                    # 执行自动重启
                    result = restart_qa_bot()

                    if result["success"]:
                        logger.info(f"问答Bot自动重启成功: {result['message']}")

                        # 通知管理员恢复成功
                        for admin_id in ADMIN_LIST:
                            if admin_id != "me":
                                try:
                                    await client.send_message(
                                        admin_id,
                                        f"{get_text('qabot.recovered', pid=result['pid'])}",
                                        parse_mode="markdown",
                                        link_preview=False,
                                    )
                                except Exception as e:
                                    logger.error(f"通知管理员失败: {e}")
                    else:
                        logger.error(f"问答Bot自动重启失败: {result['message']}")

                        # 通知管理员恢复失败
                        for admin_id in ADMIN_LIST:
                            if admin_id != "me":
                                try:
                                    await client.send_message(
                                        admin_id,
                                        f"{get_text('qabot.recovery_failed', message=result['message'])}",
                                        parse_mode="markdown",
                                        link_preview=False,
                                    )
                                except Exception as e:
                                    logger.error(f"通知管理员失败: {e}")

            except Exception as e:
                logger.error(f"问答Bot健康检查任务失败: {type(e).__name__}: {e}")

        scheduler.add_job(
            qa_bot_health_check_job,
            "interval",
            seconds=60,  # 每分钟检查一次
            id="qa_bot_health_check",
        )
        logger.info("问答Bot健康检查任务已配置：每60秒执行一次")

        # 确保 sessions 目录存在
        sessions_dir = "data/sessions"
        os.makedirs(sessions_dir, exist_ok=True)
        logger.debug(f"会话目录已准备: {sessions_dir}")

        # 启动机器人客户端，处理命令
        logger.info("开始初始化Telegram机器人客户端...")

        # 从 settings 获取配置
        api_id = get_api_id()
        api_hash = get_api_hash()
        bot_token = get_bot_token()

        client = TelegramClient("data/sessions/bot_session", int(api_id), api_hash)

        # 设置活动的客户端实例，供其他模块使用
        from core.telegram_client import set_active_client

        set_active_client(client)

        # 添加命令处理，支持中英文命令
        logger.debug("开始添加命令处理器...")

        # 1. 基础命令
        client.add_event_handler(handle_start, NewMessage(pattern="/start|/开始"))
        client.add_event_handler(handle_help, NewMessage(pattern="/help|/帮助"))

        # 2. 核心功能命令
        client.add_event_handler(handle_manual_summary, NewMessage(pattern="/立即总结|/summary"))

        # 3. AI 配置命令
        client.add_event_handler(
            handle_show_prompt, NewMessage(pattern="/showprompt|/show_prompt|/查看提示词")
        )
        client.add_event_handler(
            handle_set_prompt, NewMessage(pattern="/setprompt|/set_prompt|/设置提示词")
        )
        client.add_event_handler(
            handle_show_poll_prompt,
            NewMessage(pattern="/showpollprompt|/show_poll_prompt|/查看投票提示词"),
        )
        client.add_event_handler(
            handle_set_poll_prompt,
            NewMessage(pattern="/setpollprompt|/set_poll_prompt|/设置投票提示词"),
        )
        client.add_event_handler(
            handle_show_ai_config, NewMessage(pattern="/showaicfg|/show_aicfg|/查看AI配置")
        )
        client.add_event_handler(
            handle_set_ai_config, NewMessage(pattern="/setaicfg|/set_aicfg|/设置AI配置")
        )

        # 4. 频道管理命令
        client.add_event_handler(
            handle_show_channels, NewMessage(pattern="/showchannels|/show_channels|/查看频道列表")
        )
        client.add_event_handler(
            handle_add_channel, NewMessage(pattern="/addchannel|/add_channel|/添加频道")
        )
        client.add_event_handler(
            handle_delete_channel, NewMessage(pattern="/deletechannel|/delete_channel|/删除频道")
        )

        # 5. 自动化配置命令
        client.add_event_handler(
            handle_show_channel_schedule,
            NewMessage(pattern="/showchannelschedule|/show_channel_schedule|/查看频道时间配置"),
        )
        client.add_event_handler(
            handle_set_channel_schedule,
            NewMessage(pattern="/setchannelschedule|/set_channel_schedule|/设置频道时间配置"),
        )
        client.add_event_handler(
            handle_delete_channel_schedule,
            NewMessage(pattern="/deletechannelschedule|/delete_channel_schedule|/删除频道时间配置"),
        )
        client.add_event_handler(
            handle_clear_summary_time,
            NewMessage(pattern="/clearsummarytime|/clear_summary_time|/清除总结时间"),
        )
        client.add_event_handler(
            handle_set_send_to_source,
            NewMessage(pattern="/setsendtosource|/set_send_to_source|/设置报告发送回源频道"),
        )

        # 6. 投票配置命令
        client.add_event_handler(
            handle_show_channel_poll,
            NewMessage(pattern="/channelpoll|/channel_poll|/查看频道投票配置"),
        )
        client.add_event_handler(
            handle_set_channel_poll,
            NewMessage(pattern="/setchannelpoll|/set_channel_poll|/设置频道投票配置"),
        )
        client.add_event_handler(
            handle_delete_channel_poll,
            NewMessage(pattern="/deletechannelpoll|/delete_channel_poll|/删除频道投票配置"),
        )

        # 7. 系统控制命令
        client.add_event_handler(handle_pause, NewMessage(pattern="/pause|/暂停"))
        client.add_event_handler(handle_resume, NewMessage(pattern="/resume|/恢复"))
        client.add_event_handler(handle_restart, NewMessage(pattern="/restart|/重启"))
        client.add_event_handler(handle_shutdown, NewMessage(pattern="/shutdown|/关机"))

        # 8. 日志与调试命令
        client.add_event_handler(
            handle_show_log_level, NewMessage(pattern="/showloglevel|/show_log_level|/查看日志级别")
        )
        client.add_event_handler(
            handle_set_log_level, NewMessage(pattern="/setloglevel|/set_log_level|/设置日志级别")
        )
        client.add_event_handler(
            handle_clear_cache, NewMessage(pattern="/clearcache|/clear_cache|/清除缓存")
        )
        client.add_event_handler(handle_changelog, NewMessage(pattern="/changelog|/更新日志"))
        client.add_event_handler(handle_update, NewMessage(pattern="/update|/更新"))

        # 9. 历史记录命令
        client.add_event_handler(handle_history, NewMessage(pattern="/history|/历史"))
        client.add_event_handler(handle_export, NewMessage(pattern="/export|/导出"))
        client.add_event_handler(handle_stats, NewMessage(pattern="/stats|/统计"))

        # 10. 语言设置命令
        client.add_event_handler(handle_language, NewMessage(pattern="/language|/语言"))

        # 14. 评论区欢迎配置命令
        client.add_event_handler(
            handle_show_comment_welcome,
            NewMessage(pattern="/showcommentwelcome|/show_comment_welcome|/查看评论区欢迎"),
        )
        client.add_event_handler(
            handle_set_comment_welcome,
            NewMessage(pattern="/setcommentwelcome|/set_comment_welcome|/设置评论区欢迎"),
        )
        client.add_event_handler(
            handle_delete_comment_welcome,
            NewMessage(pattern="/deletecommentwelcome|/delete_comment_welcome|/删除评论区欢迎"),
        )

        # 11. 数据库迁移命令
        client.add_event_handler(
            handle_migrate_check, NewMessage(pattern="/migrate_check|/迁移检查")
        )
        client.add_event_handler(
            handle_migrate_start, NewMessage(pattern="/migrate_start|/开始迁移")
        )
        client.add_event_handler(
            handle_migrate_status, NewMessage(pattern="/migrate_status|/迁移状态")
        )
        # 12. 数据库清空命令
        client.add_event_handler(handle_db_clear, NewMessage(pattern="/db_clear|/清空数据库"))
        client.add_event_handler(
            handle_db_clear_confirm, NewMessage(pattern="/db_clear_confirm|/确认清空数据库")
        )
        client.add_event_handler(
            handle_db_clear_cancel, NewMessage(pattern="/db_clear_cancel|/取消清空数据库")
        )

        # 13. 问答Bot控制命令
        client.add_event_handler(handle_qa_status, NewMessage(pattern="/qa_status|/qa_状态"))
        client.add_event_handler(handle_qa_start, NewMessage(pattern="/qa_start|/qa_启动"))
        client.add_event_handler(handle_qa_stop, NewMessage(pattern="/qa_stop|/qa_停止"))
        client.add_event_handler(handle_qa_restart, NewMessage(pattern="/qa_restart|/qa_重启"))
        client.add_event_handler(handle_qa_stats, NewMessage(pattern="/qa_stats|/qa_统计"))

        # 15. 频道消息转发命令
        async def handle_forwarding_status(event):
            from core.forwarding import get_forwarding_handler

            handler = get_forwarding_handler()
            if handler:
                await cmd_forwarding_status(client, event.message, handler)
            else:
                await event.message.reply("转发功能未初始化")

        async def handle_forwarding_enable(event):
            from core.forwarding import get_forwarding_handler

            handler = get_forwarding_handler()
            if handler:
                await cmd_forwarding_enable(client, event.message, handler)
            else:
                await event.message.reply("转发功能未初始化")

        async def handle_forwarding_disable(event):
            from core.forwarding import get_forwarding_handler

            handler = get_forwarding_handler()
            if handler:
                await cmd_forwarding_disable(client, event.message, handler)
            else:
                await event.message.reply("转发功能未初始化")

        async def handle_forwarding_stats(event):
            from core.forwarding import get_forwarding_handler

            handler = get_forwarding_handler()
            if handler:
                await cmd_forwarding_stats(client, event.message, handler)
            else:
                await event.message.reply("转发功能未初始化")

        async def handle_forwarding_footer(event):
            from core.forwarding import get_forwarding_handler

            handler = get_forwarding_handler()
            if handler:
                await cmd_forwarding_footer(client, event.message, handler)
            else:
                await event.message.reply("转发功能未初始化")

        async def handle_forwarding_default_footer(event):
            from core.forwarding import get_forwarding_handler

            handler = get_forwarding_handler()
            if handler:
                await cmd_forwarding_default_footer(client, event.message, handler)
            else:
                await event.message.reply("转发功能未初始化")

        client.add_event_handler(
            handle_forwarding_status, NewMessage(pattern="/forwarding|/转发状态")
        )
        client.add_event_handler(
            handle_forwarding_enable, NewMessage(pattern="/forwarding_enable|/启用转发")
        )
        client.add_event_handler(
            handle_forwarding_disable, NewMessage(pattern="/forwarding_disable|/禁用转发")
        )
        client.add_event_handler(
            handle_forwarding_stats, NewMessage(pattern="/forwarding_stats|/转发统计")
        )
        client.add_event_handler(
            handle_forwarding_footer, NewMessage(pattern="/forwarding_footer|/转发底栏")
        )
        client.add_event_handler(
            handle_forwarding_default_footer,
            NewMessage(pattern="/forwarding_default_footer|/默认底栏"),
        )

        # 16. UserBot 状态命令
        async def handle_userbot_status_cmd(event):
            """UserBot 状态查询命令"""
            await handle_userbot_status(client, event.message)

        client.add_event_handler(
            handle_userbot_status_cmd, NewMessage(pattern="/userbot_status|/userbot_状态")
        )

        # 17. UserBot 频道管理命令
        from core.command_handlers.userbot_commands import (
            handle_userbot_join,
            handle_userbot_leave,
            handle_userbot_list,
        )

        async def handle_userbot_join_cmd(event):
            """UserBot 加入频道命令"""
            await handle_userbot_join(client, event.message)

        async def handle_userbot_leave_cmd(event):
            """UserBot 离开频道命令"""
            await handle_userbot_leave(client, event.message)

        async def handle_userbot_list_cmd(event):
            """UserBot 列出已加入频道命令"""
            await handle_userbot_list(client, event.message)

        client.add_event_handler(
            handle_userbot_join_cmd, NewMessage(pattern="/userbot_join|/userbot_加入")
        )
        client.add_event_handler(
            handle_userbot_leave_cmd, NewMessage(pattern="/userbot_leave|/userbot_离开")
        )
        client.add_event_handler(
            handle_userbot_list_cmd, NewMessage(pattern="/userbot_list|/userbot_列表")
        )

        # 只处理非命令消息作为提示词或AI配置输入
        client.add_event_handler(
            handle_prompt_input, NewMessage(func=lambda e: not e.text.startswith("/"))
        )
        client.add_event_handler(
            handle_poll_prompt_input, NewMessage(func=lambda e: not e.text.startswith("/"))
        )
        client.add_event_handler(handle_ai_config_input, NewMessage(func=lambda e: True))

        # 添加投票重新生成回调查询处理器
        logger.debug("添加投票重新生成回调处理器...")
        client.add_event_handler(
            handle_poll_regeneration_callback,
            CallbackQuery(func=lambda e: e.data.startswith(b"regen_poll_")),
        )
        logger.info("投票重新生成回调处理器已注册")

        # 添加投票重新生成请求回调查询处理器
        client.add_event_handler(
            handle_vote_regen_request_callback,
            CallbackQuery(func=lambda e: e.data.startswith(b"request_regen_")),
        )
        logger.info("投票重新生成请求回调处理器已注册")

        # 初始化跨Bot通信处理器
        logger.info("初始化跨Bot通信处理器...")
        request_handler = get_mainbot_request_handler()
        get_mainbot_push_handler()

        # 添加请求处理回调查询处理器
        async def handle_request_callback(event):
            """处理总结请求的回调查询（Telethon Event）"""
            await request_handler.handle_callback_query(event, client)

        client.add_event_handler(
            handle_request_callback,
            CallbackQuery(
                func=lambda e: (
                    e.data
                    and (
                        e.data.startswith(b"confirm_summary_")
                        or e.data.startswith(b"reject_summary_")
                    )
                )
            ),
        )
        logger.info("请求处理回调处理器已注册")

        logger.info("命令处理器添加完成")

        # 启动客户端
        logger.info("正在启动Telegram机器人客户端...")
        await client.start(bot_token=bot_token)
        logger.info("Telegram机器人客户端启动成功")

        # 初始化 UserBot 客户端（必须在转发功能之前初始化）
        logger.info("初始化 UserBot 客户端...")
        userbot_client = None
        try:
            from core.userbot_client import init_userbot_client

            userbot = await init_userbot_client()
            if userbot:
                success = await userbot.start()
                if success:
                    userbot_client = userbot.get_client()
                    logger.info("UserBot 客户端启动成功")
                else:
                    logger.warning("UserBot 客户端启动失败")
            else:
                logger.info("UserBot 未启用或配置缺失")
        except Exception as e:
            logger.error(f"初始化 UserBot 客户端失败: {type(e).__name__}: {e}", exc_info=True)

        # 初始化频道评论区欢迎消息功能
        logger.info("初始化频道评论区欢迎消息功能...")
        try:
            await initialize_comment_welcome(client, db_manager)

            # 添加评论欢迎消息监听器
            comment_welcome_handler = get_comment_welcome_handler()
            client.add_event_handler(
                comment_welcome_handler.handle_discussion_message,
                NewMessage(func=lambda e: e.is_channel and not e.out),
            )
            logger.info("频道评论区欢迎消息监听器已注册")

            # 添加申请周报总结按钮回调处理器
            client.add_event_handler(
                handle_summary_request_callback,
                CallbackQuery(func=lambda e: e.data and e.data.startswith(b"req_summary:")),
            )
            logger.info("申请周报总结按钮回调处理器已注册")
        except Exception as e:
            logger.error(f"初始化频道评论区欢迎消息功能失败: {type(e).__name__}: {e}")

        # 初始化频道消息转发功能（需要 userbot_client，所以在 UserBot 初始化之后）
        logger.info("初始化频道消息转发功能...")
        try:
            from core.config import get_forwarding_config
            from core.forwarding import ForwardingHandler, set_forwarding_handler

            # 创建转发处理器（传递两个客户端）
            # - monitoring_client: 用于监听消息（优先 UserBot，否则 Bot）
            # - sending_client: 总是使用 Bot 发送消息
            if userbot_client:
                # UserBot 可用，优先使用 UserBot 进行消息监听
                monitoring_client = userbot_client
                logger.info("转发功能监听使用 UserBot 客户端（更高权限）")
            else:
                # UserBot 不可用，使用 Bot 客户端监听
                monitoring_client = client
                logger.info("转发功能监听使用 Bot 客户端（UserBot 不可用）")

            # 发送消息总是使用 Bot 客户端
            sending_client = client
            logger.info("转发功能发送使用 Bot 客户端")

            # 创建转发处理器
            forwarding_handler = ForwardingHandler(db_manager, monitoring_client, sending_client)
            set_forwarding_handler(forwarding_handler)

            # 从config.json读取转发配置
            forwarding_config = get_forwarding_config()
            if forwarding_config.get("enabled", False):
                forwarding_handler.enabled = True
                forwarding_handler.set_config(forwarding_config)
                logger.info(f"转发功能已启用，共 {len(forwarding_config.get('rules', []))} 条规则")

                # UserBot 自动加入源频道
                if userbot:
                    from core.i18n import get_text

                    logger.info("UserBot 开始自动加入转发配置的源频道...")

                    # 通知管理员开始自动加入
                    for admin_id in ADMIN_LIST:
                        if admin_id != "me":
                            try:
                                await client.send_message(
                                    admin_id,
                                    get_text("userbot.join_all_start"),
                                    parse_mode="md",
                                    link_preview=False,
                                )
                            except Exception as e:
                                logger.error(f"通知管理员失败: {e}")

                    # 执行自动加入
                    result = await userbot.join_all_forwarding_channels(forwarding_config)

                    # 构建结果消息
                    if result.get("success"):
                        success_count = result.get("success_count", 0)
                        failed_count = result.get("failed_count", 0)
                        failed_list = result.get("failed_list", [])

                        # 如果有失败的频道，构建失败列表
                        failed_list_text = ""
                        if failed_list:
                            failed_items = []
                            for item in failed_list:
                                channel = item.get("channel", "未知")
                                reason = item.get("reason", "未知原因")
                                failed_items.append(f"• {channel}: {reason}")
                            failed_list_text = "\n".join(failed_items)

                        # 构建总结消息
                        summary_message = get_text(
                            "userbot.join_all_summary",
                            success_count=success_count,
                            failed_count=failed_count,
                        )

                        if failed_list_text:
                            summary_message += get_text(
                                "userbot.join_all_failed_list",
                                failed_list=failed_list_text,
                            )

                        # 发送结果给管理员
                        for admin_id in ADMIN_LIST:
                            if admin_id != "me":
                                try:
                                    await client.send_message(
                                        admin_id,
                                        summary_message,
                                        parse_mode="md",
                                        link_preview=False,
                                    )
                                except Exception as e:
                                    logger.error(f"发送结果给管理员失败: {e}")

                        # 如果有失败的频道，发送警告
                        if failed_count > 0:
                            warning_message = get_text(
                                "userbot.auto_join_warning",
                                failed_list=failed_list_text,
                            )
                            for admin_id in ADMIN_LIST:
                                if admin_id != "me":
                                    try:
                                        await client.send_message(
                                            admin_id,
                                            warning_message,
                                            parse_mode="md",
                                            link_preview=False,
                                        )
                                    except Exception as e:
                                        logger.error(f"发送警告给管理员失败: {e}")
                    else:
                        # 自动加入失败
                        error_message = result.get("message", "未知错误")
                        logger.error(f"UserBot 自动加入源频道失败: {error_message}")
                else:
                    logger.info("UserBot 不可用，跳过自动加入源频道")

                # 提取所有源频道ID
                source_channels = forwarding_config.get("rules", [])
                source_channel_ids = set()
                for rule in source_channels:
                    source_url = rule.get("source_channel", "")
                    # 从URL中提取频道ID
                    if source_url:
                        channel_id = source_url.rstrip("/").split("/")[-1]
                        source_channel_ids.add(channel_id)

                logger.info(f"转发功能监听的源频道: {source_channel_ids}")

                # 媒体组缓存字典（在闭包中共享）
                media_group_cache: dict = {}

                # 添加频道消息监听器（只监听配置的源频道）
                async def handle_channel_message(event):
                    """处理频道消息，触发转发"""
                    try:
                        # 获取当前频道的用户名或ID
                        chat_entity = await event.get_chat()
                        chat_username = getattr(chat_entity, "username", None)
                        chat_id = str(getattr(chat_entity, "id", ""))

                        logger.debug(
                            f"转发监听器被触发: chat_username={chat_username}, chat_id={chat_id}"
                        )

                        # 检查是否是配置的源频道
                        is_source_channel = (
                            chat_username and chat_username in source_channel_ids
                        ) or (chat_id in source_channel_ids)

                        if not is_source_channel:
                            # 不是源频道，跳过处理
                            logger.debug(f"频道 {chat_username or chat_id} 不是源频道，跳过")
                            return

                        logger.info(f"检测到源频道 {chat_username or chat_id} 的消息，开始处理转发")

                        # 检查是否是媒体组消息
                        grouped_id = getattr(event.message, "grouped_id", None)

                        if grouped_id:
                            # 媒体组消息：检查第一条消息是否有文本说明
                            # 通常媒体组的第一条消息会带有文本说明
                            # 如果第一条消息没有文本，跳过收集以节省资源
                            group_key = f"{chat_username or chat_id}_{grouped_id}"

                            if group_key not in media_group_cache:
                                # 这是媒体组的第一条消息
                                if not event.message.message:
                                    # 第一条消息没有文本，跳过整个媒体组
                                    logger.debug(
                                        f"媒体组 {grouped_id} 第一条消息没有文本说明，跳过收集"
                                    )
                                    return

                                # 有文本，开始收集
                                media_group_cache[group_key] = []
                                # 设置延迟处理任务（只创建一次）
                                asyncio.create_task(
                                    _delayed_forward_media_group(
                                        group_key,
                                        grouped_id,
                                        event.message,
                                        chat_username or chat_id,
                                    )
                                )

                            # 将当前消息添加到缓存
                            if event.message.id not in [
                                msg.id for msg in media_group_cache[group_key]
                            ]:
                                media_group_cache[group_key].append(event.message)
                                logger.debug(
                                    f"媒体组收集: grouped_id={grouped_id}, "
                                    f"已收集 {len(media_group_cache[group_key])} 条消息"
                                )
                        else:
                            # 普通消息：直接处理
                            await forwarding_handler.process_message(event.message)

                    except Exception as e:
                        logger.error(
                            f"处理频道消息转发失败: {type(e).__name__}: {e}", exc_info=True
                        )

                async def _delayed_forward_media_group(
                    group_key: str, grouped_id: int, first_message, channel_id: str
                ):
                    """延迟处理媒体组消息，等待所有消息到达"""
                    try:
                        # 等待1秒，让同组的其他消息到达
                        await asyncio.sleep(1)

                        # 获取缓存的消息列表
                        messages = media_group_cache.get(group_key, []).copy()

                        # 清理缓存
                        if group_key in media_group_cache:
                            del media_group_cache[group_key]

                        if not messages:
                            logger.warning(f"媒体组 {grouped_id} 缓存为空")
                            return

                        logger.info(
                            f"媒体组收集完成: grouped_id={grouped_id}, 共 {len(messages)} 条消息"
                        )

                        # 按消息ID排序，确保顺序正确
                        messages.sort(key=lambda m: m.id)

                        # 将收集的消息传递给转发处理器
                        forwarding_handler.set_external_media_group(grouped_id, messages)

                        # 只处理第一条消息（转发处理器会使用外部收集的消息）
                        await forwarding_handler.process_message(messages[0])

                    except Exception as e:
                        logger.error(f"延迟处理媒体组失败: {type(e).__name__}: {e}", exc_info=True)

                # 根据客户端类型选择注册监听器
                if userbot_client:
                    # 使用 UserBot 客户端注册监听器
                    userbot_client.add_event_handler(
                        handle_channel_message,
                        NewMessage(func=lambda e: e.is_channel),
                    )
                    logger.info("频道消息转发监听器已注册到 UserBot 客户端（仅监听配置的源频道）")
                else:
                    # 使用 Bot 客户端注册监听器
                    client.add_event_handler(
                        handle_channel_message,
                        NewMessage(func=lambda e: e.is_channel),
                    )
                    logger.info("频道消息转发监听器已注册到 Bot 客户端（仅监听配置的源频道）")
            else:
                logger.info("转发功能未启用（在config.json中设置enabled=true启用）")
        except Exception as e:
            logger.error(f"初始化频道消息转发功能失败: {type(e).__name__}: {e}")

        # 注册机器人命令
        logger.info("开始注册机器人命令...")

        commands = [
            # ========== 1. 基础与核心 ==========
            BotCommand(command="start", description="查看欢迎消息和帮助"),
            BotCommand(command="help", description="查看完整命令列表"),
            BotCommand(command="summary", description="立即生成本周频道消息汇总"),
            # ========== 2. 频道管理 ==========
            BotCommand(command="showchannels", description="查看当前频道列表"),
            BotCommand(command="addchannel", description="添加频道"),
            BotCommand(command="deletechannel", description="删除频道"),
            # ========== 3. 定时与推送 ==========
            BotCommand(command="showchannelschedule", description="查看频道自动总结时间配置"),
            BotCommand(command="setchannelschedule", description="设置频道自动总结时间"),
            BotCommand(command="deletechannelschedule", description="删除频道自动总结时间配置"),
            BotCommand(command="clearsummarytime", description="清除上次总结时间记录"),
            BotCommand(command="setsendtosource", description="设置是否将报告发送回源频道"),
            # ========== 4. AI 配置 ==========
            BotCommand(command="showprompt", description="查看当前提示词"),
            BotCommand(command="setprompt", description="设置自定义提示词"),
            BotCommand(command="showpollprompt", description="查看当前投票提示词"),
            BotCommand(command="setpollprompt", description="设置投票提示词"),
            BotCommand(command="showaicfg", description="查看AI配置"),
            BotCommand(command="setaicfg", description="设置AI配置"),
            # ========== 5. 频道互动 ==========
            BotCommand(command="channelpoll", description="查看频道投票配置"),
            BotCommand(command="setchannelpoll", description="设置频道投票配置"),
            BotCommand(command="deletechannelpoll", description="删除频道投票配置"),
            BotCommand(command="showcommentwelcome", description="查看频道评论区欢迎配置"),
            BotCommand(command="setcommentwelcome", description="设置频道评论区欢迎配置"),
            BotCommand(command="deletecommentwelcome", description="删除频道评论区欢迎配置"),
            # ========== 6. 统计与历史 ==========
            BotCommand(command="history", description="查看历史总结"),
            BotCommand(command="export", description="导出历史记录"),
            BotCommand(command="stats", description="查看统计数据"),
            # ========== 7. 系统运维 ==========
            # 系统控制
            BotCommand(command="pause", description="暂停所有定时任务"),
            BotCommand(command="resume", description="恢复所有定时任务"),
            BotCommand(command="restart", description="重启机器人"),
            BotCommand(command="shutdown", description="彻底停止机器人"),
            # 日志与缓存
            BotCommand(command="showloglevel", description="查看当前日志级别"),
            BotCommand(command="setloglevel", description="设置日志级别"),
            BotCommand(command="clearcache", description="清除讨论组ID缓存"),
            # 更新维护
            BotCommand(command="changelog", description="查看更新日志"),
            BotCommand(command="update", description="一键更新机器人"),
            # 问答Bot控制
            BotCommand(command="qa_status", description="查看问答Bot运行状态"),
            BotCommand(command="qa_start", description="启动问答Bot"),
            BotCommand(command="qa_stop", description="停止问答Bot"),
            BotCommand(command="qa_restart", description="重启问答Bot"),
            BotCommand(command="qa_stats", description="查看问答Bot详细统计"),
            # ========== 8. 数据库管理（高危） ==========
            BotCommand(command="migrate_check", description="检查数据库迁移准备状态"),
            BotCommand(command="migrate_start", description="开始数据库迁移"),
            BotCommand(command="migrate_status", description="查看数据库迁移进度"),
            BotCommand(command="db_clear", description="清空MySQL数据库（危险操作）"),
            # ========== 9. 偏好设置 ==========
            BotCommand(command="language", description="切换界面语言"),
            # ========== 10. 频道消息转发 ==========
            BotCommand(command="forwarding", description="查看转发功能状态"),
            BotCommand(command="forwarding_enable", description="启用转发功能"),
            BotCommand(command="forwarding_disable", description="禁用转发功能"),
            BotCommand(command="forwarding_stats", description="查看转发统计"),
            BotCommand(command="forwarding_footer", description="设置转发底栏"),
            BotCommand(command="forwarding_default_footer", description="启用/禁用默认底栏"),
            # ========== 11. UserBot 管理 ==========
            BotCommand(command="userbot_status", description="查看 UserBot 状态"),
            BotCommand(command="userbot_join", description="UserBot 加入频道"),
            BotCommand(command="userbot_leave", description="UserBot 离开频道"),
            BotCommand(command="userbot_list", description="列出已加入频道"),
        ]

        await client(
            SetBotCommandsRequest(scope=BotCommandScopeDefault(), lang_code="zh", commands=commands)
        )
        logger.info("机器人命令注册完成")

        logger.info("定时监控已启动...")
        logger.info("机器人已启动，正在监听命令...")
        logger.info("机器人命令已注册完成...")

        # 启动调度器
        scheduler.start()
        logger.info("调度器已启动")

        # 存储调度器实例到config模块，供其他模块访问
        from core.config import set_scheduler_instance

        set_scheduler_instance(scheduler)
        logger.info("调度器实例已存储到config模块")

        # 向管理员发送启动消息
        logger.info("开始向管理员发送启动消息...")
        await send_startup_message(client)
        logger.info("启动消息发送完成")

        # 检查数据库类型，如果使用SQLite则建议迁移
        logger.info("检查数据库类型...")

        # 读取环境变量判断当前使用的数据库类型
        current_db_type = os.getenv("DATABASE_TYPE", "sqlite").lower()

        # 只在当前使用 SQLite 时才提示迁移
        if current_db_type == "sqlite":
            db_manager = get_db_manager()
            from core.i18n import get_text

            # 检查SQLite数据库文件是否存在且有数据
            sqlite_db_path = "data/summaries.db"

            if await asyncio.to_thread(os.path.exists, sqlite_db_path):
                # 检查数据库大小，如果有数据则提示迁移
                db_size = await asyncio.to_thread(os.path.getsize, sqlite_db_path)
                if db_size > 0:
                    logger.info(f"检测到SQLite数据库，大小: {db_size} 字节")

                    for admin_id in ADMIN_LIST:
                        try:
                            await client.send_message(
                                admin_id,
                                get_text("database.startup_old_database"),
                                parse_mode="md",
                                link_preview=False,
                            )
                            logger.info(f"已向管理员 {admin_id} 发送数据库迁移建议")
                        except Exception as e:
                            logger.error(f"向管理员 {admin_id} 发送迁移建议失败: {e}")
                else:
                    logger.info("SQLite数据库为空，跳过迁移提示")
            else:
                logger.info("未检测到SQLite数据库文件，跳过迁移提示")
        else:
            logger.info(f"使用 {current_db_type.upper()} 数据库，无需迁移")

        # 检查是否是重启后的首次运行
        if await asyncio.to_thread(os.path.exists, RESTART_FLAG_FILE):
            try:
                async with aiofiles.open(RESTART_FLAG_FILE) as f:
                    content = (await f.read()).strip()

                # 尝试解析为用户ID
                try:
                    restart_user_id = int(content)
                    # 发送重启成功消息给特定用户
                    logger.info(f"检测到重启标记，向用户 {restart_user_id} 发送重启成功消息")
                    await client.send_message(
                        restart_user_id, "机器人已成功重启！", link_preview=False
                    )
                except ValueError:
                    # 如果不是整数，忽略
                    logger.info(f"检测到重启标记，但内容不是有效的用户ID: {content}")

                # 删除重启标记文件
                await asyncio.to_thread(os.remove, RESTART_FLAG_FILE)
                logger.info("重启标记文件已删除")
            except Exception as e:
                logger.error(f"处理重启标记时出错: {type(e).__name__}: {e}", exc_info=True)

        # 保持 Bot 客户端运行
        # 如果有 UserBot 客户端，也需要让它持续运行
        if userbot_client and userbot_client.is_connected():
            # 创建一个任务来保持 UserBot 客户端运行
            async def keep_userbot_alive():
                """保持 UserBot 客户端运行，接收更新"""
                try:
                    logger.info("UserBot 客户端开始接收更新...")
                    await userbot_client.run_until_disconnected()
                except Exception as e:
                    logger.error(f"UserBot 客户端运行出错: {type(e).__name__}: {e}", exc_info=True)

            # 启动 UserBot 保持运行任务
            asyncio.create_task(keep_userbot_alive())
            logger.info("UserBot 客户端已启动后台任务")

        # 保持 Bot 客户端运行
        await client.run_until_disconnected()
    except KeyboardInterrupt:
        logger.info("收到键盘中断，正在优雅关闭...")
        raise  # 重新抛出以便在上层处理
    except Exception as e:
        logger.critical(f"机器人服务初始化或运行失败: {type(e).__name__}: {e}", exc_info=True)
    finally:
        # 在主事件循环关闭前清理资源
        logger.info("正在清理资源...")

        # 1. 停止调度器
        from core.config import get_scheduler_instance

        scheduler = get_scheduler_instance()
        if scheduler and scheduler.running:
            logger.info("停止调度器...")
            scheduler.shutdown(wait=False)

        # 2. 关闭数据库连接池
        db_manager = get_db_manager()
        if hasattr(db_manager, "close") and asyncio.iscoroutinefunction(db_manager.close):
            try:
                await db_manager.close()
                logger.info("数据库连接池已关闭")
            except Exception as e:
                logger.error(f"关闭数据库连接池时出错: {type(e).__name__}: {e}")

        # 3. 断开 Telegram 客户端
        if client.is_connected():
            try:
                await client.disconnect()
                logger.info("Telegram客户端已断开")
            except Exception as e:
                logger.error(f"断开Telegram客户端时出错: {type(e).__name__}: {e}")


if __name__ == "__main__":
    logger.info(f"===== Sakura-Bot v{__version__} 启动 ======")

    # 验证必要配置
    is_valid, missing = validate_required_settings()

    if not is_valid:
        logger.error(
            f"错误: 请确保 .env 文件中配置了所有必要的 API 凭证。缺少: {', '.join(missing)}"
        )
        print(f"错误: 请确保 .env 文件中配置了所有必要的 API 凭证。缺少: {', '.join(missing)}")
    else:
        logger.info("所有必要的 API 凭证已配置完成")

        # 创建一个事件循环和用于优雅关闭的 threading.Event
        loop = asyncio.new_event_loop()
        asyncio.set_event_loop(loop)

        shutdown_event = asyncio.Event()

        # 定义信号处理函数
        def signal_handler(signum, frame):
            """处理 SIGTERM 和 SIGINT 信号"""
            signal_name = signal.Signals(signum).name
            logger.info(f"收到信号 {signal_name} ({signum})，开始优雅关闭...")
            # 设置事件以通知主循环开始关闭（使用 call_soon_threadsafe 因为 set() 是同步方法）
            loop.call_soon_threadsafe(shutdown_event.set)

        # 注册信号处理器（在 Windows 上只支持 SIGINT，SIGTERM 可能不可用）
        try:
            signal.signal(signal.SIGTERM, signal_handler)
            logger.info("已注册 SIGTERM 信号处理器")
        except (AttributeError, ValueError) as e:
            logger.warning(f"无法注册 SIGTERM 信号处理器: {e}")

        try:
            signal.signal(signal.SIGINT, signal_handler)
            logger.info("已注册 SIGINT 信号处理器")
        except (AttributeError, ValueError) as e:
            logger.warning(f"无法注册 SIGINT 信号处理器: {e}")

        # 设置全局关机事件
        from core.config import set_shutdown_event

        set_shutdown_event(shutdown_event)
        logger.info("全局关机事件已设置到 config 模块")

        # 启动问答Bot
        start_qa_bot()

        # 启动主函数
        try:
            logger.info("开始启动主函数...")

            # 创建主任务和等待关闭信号的任务
            async def run_with_shutdown():
                """运行主任务，同时监听关闭信号"""
                main_task = asyncio.create_task(main())

                # 等待关闭信号或主任务完成
                await shutdown_event.wait()

                # 如果收到关闭信号，取消主任务并执行优雅关闭
                if not main_task.done():
                    logger.info("取消主任务...")
                    main_task.cancel()

                    try:
                        await main_task
                    except asyncio.CancelledError:
                        logger.info("主任务已取消")
                    except Exception as e:
                        logger.error(f"主任务取消时出错: {type(e).__name__}: {e}")

                # 执行优雅关闭
                logger.info("执行优雅关闭...")
                await graceful_shutdown_resources()

                # 退出程序（使用 os._exit 确保立即退出并关闭控制台）
                from core.shutdown_manager import get_shutdown_manager

                shutdown_manager = get_shutdown_manager()
                shutdown_manager.perform_exit(0)

                # 这行代码不会被执行到，因为 perform_exit 会立即终止进程
                loop.stop()

            loop.run_until_complete(run_with_shutdown())

        except KeyboardInterrupt:
            logger.info("机器人服务已通过键盘中断停止")
        except Exception as e:
            logger.critical(f"主函数执行失败: {type(e).__name__}: {e}", exc_info=True)
        finally:
            # 确保问答Bot被停止（已在main()的finally中处理了其他资源）
            logger.info("正在停止问答Bot...")
            stop_qa_bot()
            logger.info("程序已退出")
            # 关闭事件循环
            try:
                loop.run_until_complete(loop.shutdown_asyncgens())
            except Exception as e:
                logger.error(f"关闭异步生成器时出错: {e}")
            loop.close()
