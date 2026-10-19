# tests/integration/test_config_hot_reload.py
import asyncio
import json
import tempfile
from pathlib import Path

import pytest

from core.config import AsyncIOEventBus, ConfigManager, FileWatcher
from core.config.events import ConfigChangedEvent


@pytest.mark.asyncio
@pytest.mark.parametrize("reload_delay", [0, 0.75], ids=["normal", "delayed"])
async def test_end_to_end_config_hot_reload(monkeypatch, reload_delay):
    """端到端测试：配置热重载完整流程"""
    loop = asyncio.get_running_loop()

    # 1. 创建测试配置文件
    with tempfile.TemporaryDirectory() as tmpdir:
        config_file = Path(tmpdir) / "config.json"
        config_file.write_text(json.dumps({"version": 1, "channels": ["test1"]}))

        # 2. 初始化ConfigManager
        config_manager = ConfigManager(loop)
        success, error = config_manager.initialize_sync(config_file)
        assert success

        # 3. 初始化事件总线
        event_bus = AsyncIOEventBus(sequential_mode=True)
        config_manager.event_bus = event_bus
        reloaded = asyncio.Event()
        received_events = []

        async def on_config_changed(event):
            received_events.append(event)
            reloaded.set()

        await event_bus.subscribe(ConfigChangedEvent, on_config_changed)

        if reload_delay:
            reload_config = config_manager.reload_config

            async def delayed_reload():
                # 模拟慢磁盘/繁忙 CI：重载完成时间明确超过原来的固定等待。
                await asyncio.sleep(reload_delay)
                return await reload_config()

            monkeypatch.setattr(config_manager, "reload_config", delayed_reload)

        # 4. 启动文件监控
        file_watcher = FileWatcher(config_manager, loop)
        file_watcher.start(tmpdir)

        try:
            # 5. 修改配置文件
            await asyncio.sleep(0.1)
            expected = {"version": 2, "channels": ["test1", "test2"]}
            config_file.write_text(json.dumps(expected))

            # 6. 等待真实重载事件；超时仍会明确暴露监控或重载故障。
            await asyncio.wait_for(reloaded.wait(), timeout=5)

            # 7. 同时验证内存快照和事件中的完整配置。
            assert await config_manager.get_config() == expected
            assert len(received_events) == 1
            assert received_events[0].config == expected
        finally:
            # 断言失败时也必须停止监控，避免线程和任务干扰后续用例。
            file_watcher.stop()


@pytest.mark.asyncio
async def test_config_validation_error_sends_event():
    """测试配置验证失败时发送事件"""

    loop = asyncio.get_event_loop()

    with tempfile.TemporaryDirectory() as tmpdir:
        config_file = Path(tmpdir) / "config.json"
        config_file.write_text('{"invalid": json}')  # 无效JSON

        config_manager = ConfigManager(loop)
        config_manager.initialize_sync(config_file)

        event_bus = AsyncIOEventBus()
        config_manager.event_bus = event_bus

        received_errors = []

        async def on_validation_error(event):
            received_errors.append(event)

        from core.config.events import ConfigValidationErrorEvent

        await event_bus.subscribe(ConfigValidationErrorEvent, on_validation_error)

        # 触发重载
        success, error = await config_manager.reload_config()

        # 验证失败并发送了错误事件
        assert not success
        assert len(received_errors) == 1
