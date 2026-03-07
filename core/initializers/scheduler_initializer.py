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
调度器初始化器

负责配置和启动APScheduler调度器，包括定时总结任务、健康检查任务等。
"""

import logging
from typing import TYPE_CHECKING

from apscheduler.schedulers.asyncio import AsyncIOScheduler

if TYPE_CHECKING:
    from telethon import TelegramClient

from core.config import CHANNELS, build_cron_trigger, get_channel_schedule, set_scheduler_instance
from core.scheduler import cleanup_old_poll_regenerations, main_job


class SchedulerInitializer:
    """调度器初始化器"""

    def __init__(self):
        self.logger = logging.getLogger(__name__)
        self.scheduler: AsyncIOScheduler | None = None

    async def initialize(self, client: "TelegramClient") -> AsyncIOScheduler:
        """初始化调度器

        Args:
            client: Telegram客户端实例

        Returns:
            初始化好的调度器实例
        """
        self.logger.info("开始初始化调度器...")

        # 创建调度器实例
        self.scheduler = AsyncIOScheduler()

        # 添加频道定时总结任务
        await self._add_channel_summary_jobs(client)

        # 添加定期清理任务
        self._add_cleanup_jobs()

        # 添加跨Bot通信检查任务
        self._add_communication_jobs(client)

        # 添加问答Bot健康检查任务
        self._add_qabot_health_check_jobs(client)

        # 启动调度器
        self.scheduler.start()
        self.logger.info("调度器已启动")

        # 存储调度器实例到config模块
        set_scheduler_instance(self.scheduler)
        self.logger.info("调度器实例已存储到config模块")

        return self.scheduler

    async def _add_channel_summary_jobs(self, client: "TelegramClient") -> None:
        """添加频道定时总结任务

        Args:
            client: Telegram客户端实例
        """
        self.logger.info(f"开始为 {len(CHANNELS)} 个频道配置定时任务...")

        for channel in CHANNELS:
            # 获取频道的自动总结时间配置
            schedule = get_channel_schedule(channel)

            # 构建 cron 触发器参数
            trigger_params = build_cron_trigger(schedule)

            # 创建定时任务
            self.scheduler.add_job(
                main_job,
                "cron",
                **trigger_params,
                args=[channel],
                id=f"summary_job_{channel}",
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

            self.logger.info(
                f"频道 {channel} 的定时任务已配置：{frequency_text} {schedule['hour']:02d}:{schedule['minute']:02d}"
            )

        self.logger.info(f"定时任务配置完成：共 {len(CHANNELS)} 个频道")

    def _add_cleanup_jobs(self) -> None:
        """添加定期清理任务"""
        # 投票重新生成数据清理任务
        self.scheduler.add_job(
            cleanup_old_poll_regenerations,
            "cron",
            hour=3,
            minute=0,
            id="cleanup_poll_regenerations",
        )
        self.logger.info("投票重新生成数据清理任务已配置：每天凌晨3点执行")

    def _add_communication_jobs(self, client: "TelegramClient") -> None:
        """添加跨Bot通信检查任务

        Args:
            client: Telegram客户端实例
        """
        from core.mainbot_request_handler import get_mainbot_request_handler

        request_handler = get_mainbot_request_handler()

        async def check_requests_job():
            """定期检查并处理来自问答Bot的请求"""
            try:
                await request_handler.check_requests(telethon_client=client)
            except Exception as e:
                self.logger.error(f"检查请求任务失败: {type(e).__name__}: {e}")

        self.scheduler.add_job(
            check_requests_job,
            "interval",
            seconds=30,
            id="check_requests",
        )
        self.logger.info("跨Bot请求检查任务已配置：每30秒执行一次")

    def _add_qabot_health_check_jobs(self, client: "TelegramClient") -> None:
        """添加问答Bot健康检查任务

        Args:
            client: Telegram客户端实例
        """
        from core.config import ADMIN_LIST
        from core.i18n import get_text
        from core.process_manager import check_qa_bot_health, restart_qa_bot

        async def qa_bot_health_check_job():
            """定期检查问答Bot健康状态，必要时自动重启"""
            try:
                is_healthy, should_restart, message = check_qa_bot_health()

                if should_restart:
                    self.logger.warning(f"问答Bot需要自动重启: {message}")

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
                                self.logger.error(f"通知管理员失败: {e}")

                    # 执行自动重启
                    result = restart_qa_bot()

                    if result["success"]:
                        self.logger.info(f"问答Bot自动重启成功: {result['message']}")

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
                                    self.logger.error(f"通知管理员失败: {e}")
                    else:
                        self.logger.error(f"问答Bot自动重启失败: {result['message']}")

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
                                    self.logger.error(f"通知管理员失败: {e}")

            except Exception as e:
                self.logger.error(f"问答Bot健康检查任务失败: {type(e).__name__}: {e}")

        self.scheduler.add_job(
            qa_bot_health_check_job,
            "interval",
            seconds=60,
            id="qa_bot_health_check",
        )
        self.logger.info("问答Bot健康检查任务已配置：每60秒执行一次")
