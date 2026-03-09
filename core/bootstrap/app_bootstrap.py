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
应用引导程序

协调各个初始化器，按正确顺序启动应用的所有组件。
"""

import asyncio
import logging
import os
from typing import TYPE_CHECKING

from telethon import TelegramClient

if TYPE_CHECKING:
    from apscheduler.schedulers.asyncio import AsyncIOScheduler

from core.infrastructure.database.manager import get_db_manager
from core.initializers import (
    CommandRegistrar,
    CommentWelcomeInitializer,
    CommunicationInitializer,
    DatabaseInitializer,
    ForwardingInitializer,
    SchedulerInitializer,
    StartupNotifier,
    UserBotInitializer,
)
from core.settings import get_api_hash, get_api_id, get_bot_token, validate_required_settings
from core.system.error_handler import initialize_error_handling
from core.telegram.client import set_active_client


class AppBootstrap:
    """应用引导程序 - 协调所有初始化器的工作"""

    def __init__(self, version: str = "1.7.2", config_manager=None):
        self.logger = logging.getLogger(__name__)
        self.version = version
        self.client: TelegramClient | None = None
        self.scheduler: AsyncIOScheduler | None = None
        self.userbot_client: TelegramClient | None = None
        self.config_manager = config_manager

        # 从config_manager提取event_bus
        self._event_bus = config_manager.event_bus if config_manager else None

        # 初始化各个初始化器
        self.database_initializer = DatabaseInitializer()
        self.scheduler_initializer = SchedulerInitializer()
        self.userbot_initializer = UserBotInitializer()
        self.forwarding_initializer = ForwardingInitializer(event_bus=self._event_bus)
        self.comment_welcome_initializer = CommentWelcomeInitializer()
        self.communication_initializer = CommunicationInitializer()
        self.command_registrar = CommandRegistrar()
        self.startup_notifier = StartupNotifier(version=version)

    async def run(self) -> None:
        """运行应用引导流程

        这是应用的主入口点，按正确顺序初始化所有组件。
        """
        try:
            self.logger.info(f"开始初始化机器人服务 v{self.version}...")

            # 第1步：验证配置
            self._validate_settings()

            # 第2步：初始化错误处理
            self._initialize_error_handling()

            # 第3步：初始化数据库
            await self.database_initializer.initialize()

            # 第4步：启动Telegram Bot客户端
            await self._start_telegram_client()

            # 第5步：初始化UserBot（可选）
            await self._initialize_userbot()

            # 第6步：初始化调度器
            await self._initialize_scheduler()

            # 第7步：注册所有命令
            await self.command_registrar.register_all_commands(self.client)

            # 第8步：注册机器人命令菜单
            await self._register_bot_commands()

            # 第9步：初始化跨Bot通信
            await self.communication_initializer.initialize(self.client)

            # 第10步：初始化评论区欢迎功能
            await self._initialize_comment_welcome()

            # 第11步：初始化转发功能
            await self._initialize_forwarding()

            # 第12步：发送启动通知
            await self._send_startup_notifications()

            # 第13步：保持Bot运行
            await self._keep_running()

        except KeyboardInterrupt:
            self.logger.info("收到键盘中断，正在优雅关闭...")
            raise
        except Exception as e:
            self.logger.critical(f"应用引导失败: {type(e).__name__}: {e}", exc_info=True)
        finally:
            await self._cleanup()

    def _validate_settings(self) -> None:
        """验证必要的配置"""
        self.logger.info("验证配置...")
        is_valid, missing = validate_required_settings()

        if not is_valid:
            error_msg = (
                f"错误: 请确保 .env 文件中配置了所有必要的 API 凭证。缺少: {', '.join(missing)}"
            )
            self.logger.error(error_msg)
            raise RuntimeError(error_msg)

        self.logger.info("所有必要的 API 凭证已配置完成")

    def _initialize_error_handling(self) -> None:
        """初始化错误处理系统"""
        self.logger.info("初始化错误处理系统...")
        initialize_error_handling()
        self.logger.info("错误处理系统初始化完成")

    async def _start_telegram_client(self) -> None:
        """启动Telegram Bot客户端"""
        self.logger.info("开始初始化Telegram机器人客户端...")

        # 确保 sessions 目录存在
        sessions_dir = "data/sessions"
        os.makedirs(sessions_dir, exist_ok=True)
        self.logger.debug(f"会话目录已准备: {sessions_dir}")

        # 从 settings 获取配置
        api_id = get_api_id()
        api_hash = get_api_hash()
        bot_token = get_bot_token()

        # 创建客户端
        self.client = TelegramClient("data/sessions/bot_session", int(api_id), api_hash)

        # 设置活动的客户端实例
        set_active_client(self.client)

        # 启动客户端
        self.logger.info("正在启动Telegram机器人客户端...")
        await self.client.start(bot_token=bot_token)
        self.logger.info("Telegram机器人客户端启动成功")

    async def _initialize_userbot(self) -> None:
        """初始化UserBot（可选）"""
        self.userbot_client = await self.userbot_initializer.initialize()

    async def _initialize_scheduler(self) -> None:
        """初始化调度器"""
        self.scheduler = await self.scheduler_initializer.initialize(self.client)

    async def _initialize_comment_welcome(self) -> None:
        """初始化评论区欢迎功能"""
        from core.infrastructure.database.manager import get_db_manager

        db_manager = get_db_manager()
        await self.comment_welcome_initializer.initialize(self.client, db_manager)

    async def _initialize_forwarding(self) -> None:
        """初始化转发功能"""
        userbot = self.userbot_initializer.get_userbot()
        await self.forwarding_initializer.initialize(self.client, self.userbot_client, userbot)

    async def _register_bot_commands(self) -> None:
        """注册机器人命令菜单"""
        from core.bot_commands import register_commands

        self.logger.info("开始注册机器人命令...")
        await register_commands(self.client)
        self.logger.info("机器人命令注册完成")

    async def _send_startup_notifications(self) -> None:
        """发送启动通知"""
        # 发送启动消息
        await self.startup_notifier.send_startup_message(self.client)

        # 检查数据库迁移建议
        await self.startup_notifier.check_database_migration(self.client)

        # 检查重启标记
        await self.startup_notifier.check_restart_flag(self.client)

    async def _keep_running(self) -> None:
        """保持应用运行"""
        self.logger.info("机器人已启动，正在监听命令...")

        # 如果有 UserBot 客户端，也需要让它持续运行
        if self.userbot_client and self.userbot_client.is_connected():
            # 创建一个任务来保持 UserBot 客户端运行
            asyncio.create_task(self.userbot_initializer.keep_alive())
            self.logger.info("UserBot 客户端已启动后台任务")

        # 保持 Bot 客户端运行
        await self.client.run_until_disconnected()

    async def _cleanup(self) -> None:
        """清理资源"""
        self.logger.info("正在清理资源...")

        # 1. 停止调度器
        from core.config import get_scheduler_instance

        scheduler = get_scheduler_instance()
        if scheduler and scheduler.running:
            self.logger.info("停止调度器...")
            scheduler.shutdown(wait=False)

        # 2. 关闭数据库连接池
        db_manager = get_db_manager()
        if hasattr(db_manager, "close") and asyncio.iscoroutinefunction(db_manager.close):
            try:
                await db_manager.close()
                self.logger.info("数据库连接池已关闭")
            except Exception as e:
                self.logger.error(f"关闭数据库连接池时出错: {type(e).__name__}: {e}")

        # 3. 断开 Telegram 客户端
        if self.client and self.client.is_connected():
            try:
                await self.client.disconnect()
                self.logger.info("Telegram客户端已断开")
            except Exception as e:
                self.logger.error(f"断开Telegram客户端时出错: {type(e).__name__}: {e}")
