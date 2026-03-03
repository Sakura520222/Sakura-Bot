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
关机管理模块 - 统一处理关机和重启逻辑
"""

import asyncio
import logging
import os
import sys
import time

logger = logging.getLogger(__name__)


class ShutdownManager:
    """关机管理器 - 负责优雅关闭所有资源

    特性:
    - PM2 环境自动检测
    - 异步资源清理（调度器 → 数据库 → 客户端）
    - 超时保护机制
    - 可配置的超时时间
    - 强制退出保底机制
    """

    # 超时配置（可从环境变量覆盖）
    TIMEOUT_QA_BOT = int(os.getenv("SHUTDOWN_TIMEOUT_QA_BOT", "2"))
    TIMEOUT_COMMENT_WELCOME = int(os.getenv("SHUTDOWN_TIMEOUT_COMMENT_WELCOME", "2"))
    TIMEOUT_SCHEDULER = int(os.getenv("SHUTDOWN_TIMEOUT_SCHEDULER", "2"))
    TIMEOUT_DATABASE = int(os.getenv("SHUTDOWN_TIMEOUT_DATABASE", "2"))
    TIMEOUT_CLIENT = int(os.getenv("SHUTDOWN_TIMEOUT_CLIENT", "3"))
    TIMEOUT_TASKS = int(os.getenv("SHUTDOWN_TIMEOUT_TASKS", "2"))
    TOTAL_TIMEOUT = int(os.getenv("SHUTDOWN_TOTAL_TIMEOUT", "10"))

    def __init__(self):
        self._shutdown_in_progress = False
        self._pm2_detected: bool | None = None  # None=未检测, True=是PM2, False=不是PM2

    def detect_pm2(self) -> bool:
        """检测是否在PM2环境下运行

        检测逻辑:
        1. 检查 PM2_HOME 环境变量
        2. 检查 PM2_JSON_PROCESSING 环境变量
        3. 检查父进程命令行（Linux/Unix）

        Returns:
            bool: True表示在PM2环境下
        """
        if self._pm2_detected is not None:
            return self._pm2_detected

        # 方法1: 检查环境变量
        if "PM2_HOME" in os.environ or "PM2_JSON_PROCESSING" in os.environ:
            logger.info("✅ 检测到 PM2 环境变量")
            self._pm2_detected = True
            return True

        # 方法2: 检查父进程命令行（Linux/Unix）
        if sys.platform != "win32" and hasattr(os, "getppid"):
            try:
                cmdline_path = f"/proc/{os.getppid()}/cmdline"
                if os.path.exists(cmdline_path):
                    with open(cmdline_path) as f:
                        if "PM2" in f.read():
                            logger.info("✅ 通过父进程检测到 PM2 环境")
                            self._pm2_detected = True
                            return True
            except Exception as e:
                logger.debug(f"检查父进程命令行失败: {e}")

        logger.info("ℹ️ 未检测到 PM2 环境")
        self._pm2_detected = False
        return False

    async def graceful_shutdown(self, client=None, timeout: int | None = None):
        """优雅关闭所有资源

        关闭顺序（拓扑排序）:
        1. 停止问答Bot（停止子进程）
        2. 停止评论区欢迎消息处理器（停止Workers）
        3. 停止调度器（停止任务源）
        4. 关闭数据库连接池（断开存储层）
        5. 断开Telegram客户端（断开通信层）
        6. 等待所有待处理任务完成

        Args:
            client: Telethon客户端实例
            timeout: 总超时时间（秒），None使用默认值
        """
        if self._shutdown_in_progress:
            logger.warning("⚠️ 关机流程已在进行中，跳过重复调用")
            return

        self._shutdown_in_progress = True
        timeout = timeout or self.TOTAL_TIMEOUT
        logger.info(f"🚪 开始优雅关机流程（总超时: {timeout}秒）...")
        start_time = time.time()

        try:
            # 步骤1: 停止问答Bot
            await self._stop_qa_bot_with_timeout(self.TIMEOUT_QA_BOT)

            # 步骤2: 停止评论区欢迎消息处理器
            await self._stop_comment_welcome_with_timeout(self.TIMEOUT_COMMENT_WELCOME)

            # 步骤3: 停止调度器
            await self._stop_scheduler_with_timeout(self.TIMEOUT_SCHEDULER)

            # 步骤4: 关闭数据库连接池
            await self._close_database_with_timeout(self.TIMEOUT_DATABASE)

            # 步骤5: 断开Telegram客户端
            if client:
                await self._disconnect_client_with_timeout(client, self.TIMEOUT_CLIENT)

            # 步骤6: 等待所有待处理任务完成
            await self._wait_for_tasks(self.TIMEOUT_TASKS)

        except Exception as e:
            logger.error(f"❌ 关机过程中出错: {type(e).__name__}: {e}", exc_info=True)

        finally:
            elapsed = time.time() - start_time
            logger.info(f"✅ 关机流程完成，耗时 {elapsed:.2f} 秒")

    async def _stop_qa_bot_with_timeout(self, timeout: int):
        """停止问答Bot（带超时）

        Args:
            timeout: 超时时间（秒）
        """
        logger.info(f"⏳ 停止问答Bot（超时: {timeout}秒）...")
        try:
            from core.process_manager import stop_qa_bot

            result = stop_qa_bot()
            if result.get("success"):
                logger.info("✅ 问答Bot已停止")
            else:
                logger.warning(f"⚠️ 停止问答Bot: {result.get('message')}")
        except Exception as e:
            logger.error(f"❌ 停止问答Bot失败: {type(e).__name__}: {e}")

    async def _stop_comment_welcome_with_timeout(self, timeout: int):
        """停止评论区欢迎消息处理器（带超时）

        Args:
            timeout: 超时时间（秒）
        """
        logger.info(f"⏳ 停止评论区欢迎消息处理器（超时: {timeout}秒）...")
        try:
            from core.channel_comment_welcome import shutdown_comment_welcome

            await asyncio.wait_for(shutdown_comment_welcome(), timeout=timeout)
            logger.info("✅ 评论区欢迎消息处理器已停止")
        except TimeoutError:
            logger.warning(f"⚠️ 停止评论区欢迎消息处理器超时（{timeout}秒），强制继续")
        except Exception as e:
            logger.error(f"❌ 停止评论区欢迎消息处理器失败: {type(e).__name__}: {e}")

    async def _stop_scheduler_with_timeout(self, timeout: int):
        """停止调度器（带超时）

        Args:
            timeout: 超时时间（秒）
        """
        logger.info(f"⏳ 停止调度器（超时: {timeout}秒）...")
        try:
            from core.config import get_scheduler_instance

            scheduler = get_scheduler_instance()
            if scheduler and scheduler.running:
                scheduler.shutdown(wait=False)
                logger.info("✅ 调度器已停止")
            else:
                logger.debug("ℹ️ 调度器未运行，跳过")
        except Exception as e:
            logger.error(f"❌ 停止调度器失败: {type(e).__name__}: {e}")

    async def _close_database_with_timeout(self, timeout: int):
        """关闭数据库连接池（带超时）

        Args:
            timeout: 超时时间（秒）
        """
        logger.info(f"⏳ 关闭数据库连接池（超时: {timeout}秒）...")
        try:
            from core.database import get_db_manager

            db_manager = get_db_manager()
            if hasattr(db_manager, "close") and asyncio.iscoroutinefunction(db_manager.close):
                await asyncio.wait_for(db_manager.close(), timeout=timeout)
                logger.info("✅ 数据库连接池已关闭")
            else:
                logger.debug("ℹ️ 数据库没有close方法或不是异步的，跳过")
        except TimeoutError:
            logger.warning(f"⚠️ 关闭数据库超时（{timeout}秒），强制继续")
        except Exception as e:
            logger.error(f"❌ 关闭数据库失败: {type(e).__name__}: {e}")

    async def _disconnect_client_with_timeout(self, client, timeout: int):
        """断开Telegram客户端（带超时）

        Args:
            client: Telethon客户端实例
            timeout: 超时时间（秒）
        """
        logger.info(f"⏳ 断开Telegram客户端（超时: {timeout}秒）...")
        try:
            if client.is_connected():
                await asyncio.wait_for(client.disconnect(), timeout=timeout)
                logger.info("✅ Telegram客户端已断开")
            else:
                logger.debug("ℹ️ 客户端未连接，跳过")
        except TimeoutError:
            logger.warning(f"⚠️ 断开客户端超时（{timeout}秒），强制继续")
        except Exception as e:
            logger.error(f"❌ 断开客户端失败: {type(e).__name__}: {e}")

    async def _wait_for_tasks(self, timeout: int):
        """等待所有待处理任务完成

        Args:
            timeout: 超时时间（秒）
        """
        try:
            tasks = [t for t in asyncio.all_tasks() if t is not asyncio.current_task()]
            if tasks:
                logger.info(f"⏳ 等待 {len(tasks)} 个待处理任务完成（超时: {timeout}秒）...")
                await asyncio.wait_for(
                    asyncio.gather(*tasks, return_exceptions=True), timeout=timeout
                )
                logger.info("✅ 所有待处理任务已完成")
            else:
                logger.debug("ℹ️ 没有待处理任务")
        except TimeoutError:
            remaining = len([t for t in asyncio.all_tasks() if t is not asyncio.current_task()])
            logger.warning(f"⚠️ 部分任务未完成（剩余 {remaining} 个），强制继续")

    def perform_exit(self, exit_code: int = 0):
        """执行退出操作

        退出策略:
        1. 记录退出日志
        2. 如果是PM2环境，特别标注
        3. 使用 os._exit() 立即终止进程并关闭控制台

        Args:
            exit_code: 退出码
        """
        is_pm2 = self.detect_pm2()

        if is_pm2:
            logger.info("🔄 [PM2 Mode] 进程即将退出，交由 PM2 监控器处理重启...")
        else:
            logger.info(f"🚪 进程即将退出（退出码: {exit_code}）")

        # 立即终止进程并关闭控制台
        os._exit(exit_code)


# 全局单例
_shutdown_manager = ShutdownManager()


def get_shutdown_manager() -> ShutdownManager:
    """获取关机管理器实例（单例模式）

    Returns:
        ShutdownManager: 关机管理器实例
    """
    return _shutdown_manager
