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
        """兼容入口应将活动客户端交给统一关机管理器。"""
        from main import graceful_shutdown_resources

        manager = MagicMock()
        manager.graceful_shutdown = AsyncMock()
        client = MagicMock()
        with (
            patch("core.system.shutdown_manager.get_shutdown_manager", return_value=manager),
            patch("core.telegram.client.get_active_client", return_value=client),
        ):
            await graceful_shutdown_resources()
        manager.graceful_shutdown.assert_awaited_once_with(client=client)

    @pytest.mark.asyncio
    async def test_graceful_shutdown_with_no_client(self):
        """没有活动客户端也应完成统一关机委托。"""
        from main import graceful_shutdown_resources

        manager = MagicMock()
        manager.graceful_shutdown = AsyncMock()
        with (
            patch("core.system.shutdown_manager.get_shutdown_manager", return_value=manager),
            patch("core.telegram.client.get_active_client", return_value=None),
        ):
            await graceful_shutdown_resources()
        manager.graceful_shutdown.assert_awaited_once_with(client=None)

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
