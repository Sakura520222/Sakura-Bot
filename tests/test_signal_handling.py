# Copyright 2026 Sakura-Bot
#
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可，
# 并附加非商业使用限制条款。

"""
测试信号处理和优雅关闭功能
"""

import asyncio
import os
import signal
from unittest.mock import AsyncMock, MagicMock, patch

import pytest

from core.config import logger

# 设置必要的环境变量以避免 aiomysql 导入错误
os.environ.setdefault("USERNAME", "testuser")
os.environ.setdefault("USER", "testuser")


class TestSignalHandling:
    """测试信号处理功能"""

    @pytest.mark.asyncio
    async def test_signal_handler_sets_shutdown_event(self):
        """测试信号处理器正确设置关闭事件"""
        shutdown_event = asyncio.Event()

        def signal_handler(signum, frame):
            """模拟信号处理器"""
            signal_name = signal.Signals(signum).name
            logger.info(f"收到信号 {signal_name} ({signum})，开始优雅关闭...")
            shutdown_event.set()

        # 测试 SIGINT
        signal_handler(signal.SIGINT, None)
        await asyncio.sleep(0.1)
        assert shutdown_event.is_set()

    @pytest.mark.asyncio
    async def test_graceful_shutdown_resources(self):
        """测试优雅关闭资源功能"""
        with patch("main.stop_qa_bot") as mock_stop_qa_bot:
            with patch("core.config.get_scheduler_instance") as mock_get_scheduler:
                with patch("core.database.get_db_manager") as mock_get_db:
                    with patch("core.telegram_client.get_active_client") as mock_get_client:
                        # 配置模拟对象
                        mock_scheduler = MagicMock()
                        mock_scheduler.running = True
                        mock_get_scheduler.return_value = mock_scheduler

                        # 模拟没有 close 方法的数据库管理器
                        mock_db = MagicMock()
                        mock_get_db.return_value = mock_db

                        mock_client = MagicMock()
                        mock_client.is_connected.return_value = True
                        mock_client.disconnect = AsyncMock()
                        mock_get_client.return_value = mock_client

                        # 导入并执行优雅关闭函数
                        from main import graceful_shutdown_resources

                        await graceful_shutdown_resources()

                        # 验证关键组件被调用
                        mock_stop_qa_bot.assert_called_once()
                        mock_scheduler.shutdown.assert_called_once_with(wait=False)
                        mock_client.disconnect.assert_awaited_once()

    @pytest.mark.asyncio
    async def test_graceful_shutdown_with_no_client(self):
        """测试没有活动客户端时的优雅关闭"""
        with patch("main.stop_qa_bot") as mock_stop_qa_bot:
            with patch("core.config.get_scheduler_instance") as mock_get_scheduler:
                with patch("core.database.get_db_manager") as mock_get_db:
                    with patch("core.telegram_client.get_active_client") as mock_get_client:
                        # 配置模拟对象
                        mock_scheduler = MagicMock()
                        mock_scheduler.running = False
                        mock_get_scheduler.return_value = mock_scheduler

                        mock_db = MagicMock()
                        mock_get_db.return_value = mock_db

                        mock_get_client.return_value = None

                        # 导入并执行优雅关闭函数
                        from main import graceful_shutdown_resources

                        await graceful_shutdown_resources()

                        # 验证问答Bot被停止
                        mock_stop_qa_bot.assert_called_once()

    @pytest.mark.asyncio
    async def test_task_cancellation(self):
        """测试任务取消机制"""

        async def dummy_task():
            await asyncio.sleep(10)

        task = asyncio.create_task(dummy_task())
        await asyncio.sleep(0.1)

        # 取消任务
        task.cancel()

        try:
            await task
        except asyncio.CancelledError:
            pass

        assert task.done() or task.cancelled()

    @pytest.mark.asyncio
    async def test_signal_registration(self):
        """测试信号处理器注册"""

        # 验证信号可以被注册
        def handler(signum, frame):
            pass

        try:
            signal.signal(signal.SIGINT, handler)
            # 如果没有抛出异常，说明注册成功
            assert True
        except Exception as e:
            # 在某些环境中可能失败，这也是可以接受的
            raise AssertionError("信号注册失败") from e
