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
关机管理器单元测试
"""

import os
from unittest.mock import AsyncMock, MagicMock, patch

import pytest

from core.shutdown_manager import ShutdownManager, get_shutdown_manager


class TestShutdownManager:
    """关机管理器测试类"""

    @pytest.fixture
    def shutdown_manager(self):
        """创建关机管理器实例"""
        # 每次测试前重置单例
        import core.shutdown_manager

        core.shutdown_manager._shutdown_manager = ShutdownManager()
        return core.shutdown_manager._shutdown_manager

    def test_get_shutdown_manager_singleton(self, shutdown_manager):
        """测试关机管理器单例模式"""
        manager1 = get_shutdown_manager()
        manager2 = get_shutdown_manager()
        assert manager1 is manager2

    def test_detect_pm2_with_env_var(self, shutdown_manager, monkeypatch):
        """测试PM2环境检测（通过环境变量）"""
        # 设置PM2环境变量
        monkeypatch.setenv("PM2_HOME", "/home/user/.pm2")
        assert shutdown_manager.detect_pm2() is True

        # 再次调用应该使用缓存
        assert shutdown_manager.detect_pm2() is True

    def test_detect_pm2_without_pm2(self, shutdown_manager, monkeypatch):
        """测试非PM2环境检测"""
        # 移除PM2环境变量
        monkeypatch.delenv("PM2_HOME", raising=False)
        monkeypatch.delenv("PM2_JSON_PROCESSING", raising=False)
        assert shutdown_manager.detect_pm2() is False

    @pytest.mark.asyncio
    async def test_graceful_shutdown_basic(self, shutdown_manager):
        """测试基本关机流程"""
        # Mock各个组件
        with (
            patch("core.shutdown_manager.stop_qa_bot") as mock_stop_qa,
            patch("core.shutdown_manager.get_scheduler_instance") as mock_get_scheduler,
            patch("core.shutdown_manager.get_db_manager") as mock_get_db,
        ):
            # 设置Mock返回值
            mock_stop_qa.return_value = {"success": True, "message": "stopped"}
            mock_scheduler = MagicMock()
            mock_scheduler.running = False
            mock_get_scheduler.return_value = mock_scheduler
            mock_get_db.return_value = MagicMock()

            # 执行关机
            await shutdown_manager.graceful_shutdown()

            # 验证调用
            mock_stop_qa.assert_called_once()

    @pytest.mark.asyncio
    async def test_stop_qa_bot_with_timeout(self, shutdown_manager):
        """测试停止问答Bot（带超时）"""
        with patch("core.shutdown_manager.stop_qa_bot") as mock_stop_qa:
            mock_stop_qa.return_value = {"success": True, "message": "stopped"}

            await shutdown_manager._stop_qa_bot_with_timeout(timeout=2)

            mock_stop_qa.assert_called_once()

    @pytest.mark.asyncio
    async def test_stop_scheduler_with_timeout(self, shutdown_manager):
        """测试停止调度器（带超时）"""
        with patch("core.shutdown_manager.get_scheduler_instance") as mock_get_scheduler:
            mock_scheduler = MagicMock()
            mock_scheduler.running = True
            mock_get_scheduler.return_value = mock_scheduler

            await shutdown_manager._stop_scheduler_with_timeout(timeout=2)

            mock_scheduler.shutdown.assert_called_once_with(wait=False)

    @pytest.mark.asyncio
    async def test_close_database_with_timeout(self, shutdown_manager):
        """测试关闭数据库（带超时）"""
        mock_db = MagicMock()
        mock_close = AsyncMock()
        mock_db.close = mock_close

        with (
            patch("core.shutdown_manager.get_db_manager") as mock_get_db,
            patch("core.shutdown_manager.asyncio.iscoroutinefunction") as mock_is_coro,
        ):
            mock_is_coro.return_value = True
            mock_get_db.return_value = mock_db

            await shutdown_manager._close_database_with_timeout(timeout=2)

            mock_close.assert_called_once()

    @pytest.mark.asyncio
    async def test_disconnect_client_with_timeout(self, shutdown_manager):
        """测试断开客户端（带超时）"""
        mock_client = MagicMock()
        mock_client.is_connected.return_value = True
        mock_disconnect = AsyncMock()
        mock_client.disconnect = mock_disconnect

        with patch("core.shutdown_manager.asyncio.wait_for"):
            await shutdown_manager._disconnect_client_with_timeout(mock_client, timeout=3)

            mock_disconnect.assert_called_once()

    @pytest.mark.asyncio
    async def test_wait_for_tasks(self, shutdown_manager):
        """测试等待任务完成"""
        with (
            patch("core.shutdown_manager.asyncio.all_tasks") as mock_all_tasks,
            patch("core.shutdown_manager.asyncio.wait_for") as mock_wait,
        ):
            # 模拟没有其他任务
            mock_all_tasks.return_value = []

            await shutdown_manager._wait_for_tasks(timeout=2)

            # 不应该调用wait_for
            mock_wait.assert_not_called()

    def test_perform_exit_normal(self, shutdown_manager, caplog):
        """测试正常退出"""
        with patch("core.shutdown_manager.sys.exit") as mock_exit:
            shutdown_manager.perform_exit(0)

            # 验证sys.exit被调用
            mock_exit.assert_called_once_with(0)

    def test_perform_exit_pm2_mode(self, shutdown_manager, monkeypatch, caplog):
        """测试PM2模式退出"""
        # 设置PM2环境
        monkeypatch.setenv("PM2_HOME", "/home/user/.pm2")

        with patch("core.shutdown_manager.sys.exit") as mock_exit:
            shutdown_manager.perform_exit(0)

            # 验证sys.exit被调用
            mock_exit.assert_called_once_with(0)

    @pytest.mark.asyncio
    async def test_graceful_shutdown_already_in_progress(self, shutdown_manager):
        """测试关机已在进行中的情况"""
        # 设置关机标志
        shutdown_manager._shutdown_in_progress = True

        # 执行关机应该立即返回
        await shutdown_manager.graceful_shutdown()

        # 验证关机标志仍然为True
        assert shutdown_manager._shutdown_in_progress is True

    @pytest.mark.asyncio
    async def test_graceful_shutdown_with_client(self, shutdown_manager):
        """测试带客户端的关机流程"""
        mock_client = MagicMock()
        mock_client.is_connected.return_value = False

        with (
            patch("core.shutdown_manager.stop_qa_bot"),
            patch("core.shutdown_manager.get_scheduler_instance") as mock_get_scheduler,
            patch("core.shutdown_manager.get_db_manager"),
        ):
            mock_scheduler = MagicMock()
            mock_scheduler.running = False
            mock_get_scheduler.return_value = mock_scheduler

            # 执行关机
            await shutdown_manager.graceful_shutdown(client=mock_client)

            # 验证完成
            assert shutdown_manager._shutdown_in_progress is True

    def test_timeout_config_from_env(self, monkeypatch):
        """测试从环境变量读取超时配置"""
        # 设置环境变量
        monkeypatch.setenv("SHUTDOWN_TIMEOUT_QA_BOT", "3")
        monkeypatch.setenv("SHUTDOWN_TIMEOUT_SCHEDULER", "4")

        # 创建新实例以读取环境变量
        manager = ShutdownManager()

        assert manager.TIMEOUT_QA_BOT == 3
        assert manager.TIMEOUT_SCHEDULER == 4

    def test_timeout_config_defaults(self):
        """测试默认超时配置"""
        # 确保没有环境变量覆盖
        env_vars = [
            "SHUTDOWN_TIMEOUT_QA_BOT",
            "SHUTDOWN_TIMEOUT_SCHEDULER",
            "SHUTDOWN_TIMEOUT_DATABASE",
            "SHUTDOWN_TIMEOUT_CLIENT",
            "SHUTDOWN_TIMEOUT_TASKS",
            "SHUTDOWN_TOTAL_TIMEOUT",
        ]
        for var in env_vars:
            os.environ.pop(var, None)

        manager = ShutdownManager()

        assert manager.TIMEOUT_QA_BOT == 2
        assert manager.TIMEOUT_SCHEDULER == 2
        assert manager.TIMEOUT_DATABASE == 2
        assert manager.TIMEOUT_CLIENT == 3
        assert manager.TIMEOUT_TASKS == 2
        assert manager.TOTAL_TIMEOUT == 10
