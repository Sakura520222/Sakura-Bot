# tests/core/config/test_file_watcher.py
import asyncio
import tempfile
from pathlib import Path

import pytest

from core.config.file_watcher import FileWatcher


class MockConfigManager:
    def __init__(self):
        self.reload_count = 0

    async def reload_config(self):
        self.reload_count += 1


@pytest.mark.asyncio
async def test_file_watcher_debounce():
    """测试500ms防抖机制"""
    loop = asyncio.get_event_loop()
    mock_manager = MockConfigManager()

    with tempfile.TemporaryDirectory() as tmpdir:
        config_file = Path(tmpdir) / "config.json"
        config_file.write_text("{}")

        watcher = FileWatcher(mock_manager, loop)
        watcher.start(tmpdir)

        # 快速修改文件3次
        for i in range(3):
            config_file.write_text(f'{{"version": {i}}}')
            await asyncio.sleep(0.1)  # 短于防抖时间

        # 等待防抖结束
        await asyncio.sleep(0.6)

        # 应该只触发一次重载
        assert mock_manager.reload_count == 1

        watcher.stop()


@pytest.mark.asyncio
async def test_file_watcher_ignores_non_config_files():
    """测试只监控配置文件"""
    loop = asyncio.get_event_loop()
    mock_manager = MockConfigManager()

    with tempfile.TemporaryDirectory() as tmpdir:
        other_file = Path(tmpdir) / "other.txt"
        other_file.write_text("test")

        watcher = FileWatcher(mock_manager, loop)
        watcher.start(tmpdir)

        # 修改非配置文件
        other_file.write_text("modified")
        await asyncio.sleep(0.6)

        # 不应该触发重载
        assert mock_manager.reload_count == 0

        watcher.stop()


@pytest.mark.asyncio
async def test_file_watcher_stop_during_debounce():
    """测试在防抖期间停止监控器"""
    loop = asyncio.get_event_loop()
    mock_manager = MockConfigManager()

    with tempfile.TemporaryDirectory() as tmpdir:
        config_file = Path(tmpdir) / "config.json"
        config_file.write_text("{}")

        watcher = FileWatcher(mock_manager, loop)
        watcher.start(tmpdir)

        # 修改文件触发防抖
        config_file.write_text('{"version": 1}')

        # 在防抖延迟结束前停止监控器
        await asyncio.sleep(0.2)  # 短于防抖时间
        watcher.stop()

        # 等待超过防抖时间
        await asyncio.sleep(0.5)

        # 不应该触发重载（因为监控器已停止）
        assert mock_manager.reload_count == 0
