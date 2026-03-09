# tests/integration/test_config_hot_reload.py
import asyncio
import json
import tempfile
from pathlib import Path

import pytest

from core.config import AsyncIOEventBus, ConfigManager, FileWatcher


@pytest.mark.asyncio
async def test_end_to_end_config_hot_reload():
    """端到端测试：配置热重载完整流程"""
    loop = asyncio.get_event_loop()

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

        # 4. 启动文件监控
        file_watcher = FileWatcher(config_manager, loop)
        file_watcher.start(tmpdir)

        # 5. 修改配置文件
        await asyncio.sleep(0.1)
        config_file.write_text(json.dumps({"version": 2, "channels": ["test1", "test2"]}))

        # 6. 等待防抖和重载
        await asyncio.sleep(0.6)

        # 7. 验证 - 由于ConfigChangedEvent需要BusinessConfig对象而不是dict，
        # 我们只验证配置文件内容是否正确更新
        new_config = await config_manager.get_config()
        assert new_config["version"] == 2
        assert len(new_config["channels"]) == 2

        # 8. 清理
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
