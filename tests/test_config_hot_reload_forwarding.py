# Copyright 2026 Sakura-Bot
#
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可

import asyncio
import tempfile
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import AsyncMock, Mock, patch

import pytest

from core.config.events import ConfigChangedEvent
from core.config.manager import ConfigManager
from core.forwarding.forwarding_handler import ForwardingHandler
from core.initializers.forwarding_initializer import ForwardingInitializer
from core.web_api import deps


@pytest.mark.asyncio
async def test_webui_write_config_schedules_config_reload():
    """WebUI 保存配置后应显式安排配置热重载。"""
    config_manager = SimpleNamespace(reload_config=AsyncMock(return_value=(True, "")))
    deps.configure_config_manager(config_manager)

    with patch.object(deps, "save_config_changes") as save_config_changes:
        deps.write_config({"forwarding": {"enabled": True, "rules": []}})
        await asyncio.sleep(0)

    save_config_changes.assert_called_once()
    config_manager.reload_config.assert_awaited_once()
    deps.configure_config_manager(None)


@pytest.mark.asyncio
async def test_webui_reload_task_uses_bound_config_manager():
    """热重载任务应使用调度时绑定的配置管理器。"""
    first_manager = SimpleNamespace(reload_config=AsyncMock(return_value=(True, "")))
    second_manager = SimpleNamespace(reload_config=AsyncMock(return_value=(True, "")))
    deps.configure_config_manager(first_manager)

    with patch.object(deps, "save_config_changes"):
        deps.write_config({"forwarding": {"enabled": True, "rules": []}})
        deps.configure_config_manager(second_manager)
        await asyncio.sleep(0)

    first_manager.reload_config.assert_awaited_once()
    second_manager.reload_config.assert_not_awaited()
    deps.configure_config_manager(None)


@pytest.mark.asyncio
async def test_forwarding_handler_on_config_updated_updates_runtime_config():
    """配置事件应更新转发处理器运行时配置。"""
    handler = ForwardingHandler(Mock(), Mock(), Mock())
    handler.enabled = False
    handler.set_config({"enabled": False, "rules": []})

    event = ConfigChangedEvent(
        config={
            "forwarding": {
                "enabled": True,
                "rules": [
                    {
                        "source_channel": "https://t.me/source_channel",
                        "target_channel": "https://t.me/target_channel",
                    }
                ],
            }
        },
        version=1,
        changed_fields={"forwarding.rules"},
    )

    await handler.on_config_updated(event)

    assert handler.enabled is True
    assert len(handler.config["rules"]) == 1
    assert handler.config["rules"][0]["source_channel"] == "https://t.me/source_channel"
    assert handler.source_channel_ids == {"source_channel"}


def test_forwarding_initializer_reads_current_source_channels_from_handler_cache():
    """转发监听源频道应从处理器缓存动态读取。"""
    initializer = ForwardingInitializer()
    handler = ForwardingHandler(Mock(), Mock(), Mock())
    handler.set_config(
        {
            "rules": [
                {
                    "source_channel": "https://t.me/new_source",
                    "target_channel": "https://t.me/target",
                }
            ]
        }
    )
    initializer.forwarding_handler = handler

    assert initializer._get_current_source_channels() == {"new_source"}

    handler.set_config(
        {
            "rules": [
                {
                    "source_channel": "https://t.me/another_source",
                    "target_channel": "https://t.me/target",
                }
            ]
        }
    )

    assert initializer._get_current_source_channels() == {"another_source"}


@pytest.mark.asyncio
async def test_config_manager_reload_skips_unchanged_snapshot():
    """配置内容未变化时应跳过重复事件发布。"""
    with tempfile.TemporaryDirectory() as tmpdir:
        config_file = Path(tmpdir) / "config.json"
        await asyncio.to_thread(config_file.write_text, '{"version": 1}', encoding="utf-8")

        manager = ConfigManager(asyncio.get_running_loop())
        assert manager.initialize_sync(config_file)[0] is True

        event_bus = SimpleNamespace(publish=AsyncMock())
        manager.event_bus = event_bus

        success, error = await manager.reload_config()

        assert success is True
        assert error == ""
        event_bus.publish.assert_not_awaited()
