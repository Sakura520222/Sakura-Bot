# Copyright 2026 Sakura-Bot
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot

import asyncio
import logging
from copy import deepcopy
from pathlib import Path

from core.config.event_bus import AsyncIOEventBus
from core.config.events import ConfigChangedEvent, ConfigValidationErrorEvent
from core.config.persistence import ConfigPersistenceError, ConfigStore, configs_equal

logger = logging.getLogger(__name__)

CONFIG_FILE = Path("data/config.json")


class ConfigManager:
    """配置热重载管理器；与所有写入口共用事务存储和有效备份。"""

    def __init__(self, loop: asyncio.AbstractEventLoop):
        self._loop = loop
        self._write_lock = asyncio.Lock()
        self._config_snapshot: dict | None = None
        self._config_version = 0
        self._event_bus: AsyncIOEventBus | None = None
        self._config_file = CONFIG_FILE

    @property
    def event_bus(self) -> AsyncIOEventBus | None:
        return self._event_bus

    @event_bus.setter
    def event_bus(self, value: AsyncIOEventBus):
        self._event_bus = value

    def initialize_sync(self, config_file: Path = None) -> tuple[bool, str]:
        """同步校验并建立初始快照，不覆盖已有 last_valid。"""
        if config_file is not None:
            self._config_file = Path(config_file)
        try:
            self._config_snapshot = ConfigStore(self._config_file).initialize()
            logger.info("配置已同步加载: %s", self._config_file)
            return True, ""
        except ConfigPersistenceError as error:
            return False, str(error)
        except Exception:
            return False, "配置初始化失败"

    async def get_config(self) -> dict | None:
        """返回独立副本，避免调用方修改已校验的回滚快照。"""
        return deepcopy(self._config_snapshot)

    async def reload_config(self) -> tuple[bool, str]:
        """串行执行完整重载；验证失败时在文件锁内安全回滚。"""
        async with self._write_lock:
            try:
                old_config = self._config_snapshot
                config, error, rolled_back = await asyncio.to_thread(
                    ConfigStore(self._config_file).reload, old_config
                )
                if error:
                    event = ConfigValidationErrorEvent(
                        error=error,
                        config_path=str(self._config_file),
                        rolled_back=rolled_back,
                    )
                    if self._event_bus:
                        await self._event_bus.publish(event)
                    logger.error("配置验证失败: %s", error)
                    return False, error
                if configs_equal(config, old_config):
                    return True, ""

                self._config_snapshot = deepcopy(config)
                self._config_version += 1
                if self._event_bus:
                    await self._event_bus.publish(
                        ConfigChangedEvent(
                            config=deepcopy(config),
                            version=self._config_version,
                            old_config=deepcopy(old_config),
                            changed_fields=ConfigChangedEvent.calculate_changed_fields(
                                old_config, config
                            ),
                        )
                    )
                logger.info("配置已更新到版本 %s", self._config_version)
                return True, ""
            except ConfigPersistenceError as error:
                logger.error("配置重载失败: %s", error)
                return False, str(error)
            except Exception:
                logger.error("配置重载异常")
                return False, "配置重载异常"
