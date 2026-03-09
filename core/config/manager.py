# core/config/manager.py
import asyncio
import json
import logging
from pathlib import Path

from core.config.event_bus import AsyncIOEventBus
from core.config.events import ConfigChangedEvent
from core.config.validator import ConfigValidator

logger = logging.getLogger(__name__)

# 配置文件路径
CONFIG_FILE = Path("data/config.json")


class ConfigManager:
    """配置管理器（原子性更新 + 热重载）"""

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
        """
        同步初始化配置（纯同步方法，解决启动顺序问题）

        Returns:
            (成功, 错误信息)
        """
        if config_file:
            self._config_file = config_file

        try:
            # 同步读取并解析配置文件
            if not self._config_file.exists():
                return False, f"配置文件不存在: {self._config_file}"

            with open(self._config_file, encoding="utf-8") as f:
                config_dict = json.load(f)

            self._config_snapshot = config_dict
            logger.info(f"✅ 配置已同步加载: {self._config_file}")
            return True, ""

        except json.JSONDecodeError as e:
            return False, f"JSON格式错误: {e}"
        except Exception as e:
            return False, f"初始化失败: {e}"

    async def get_config(self) -> dict:
        """获取当前配置（异步方法）"""
        return self._config_snapshot

    async def reload_config(self) -> tuple[bool, str]:
        """原子性配置重载"""
        try:
            # 1. 验证配置文件
            success, error_event, config_dict = ConfigValidator.validate_config_file(
                self._config_file
            )

            if not success:
                # 验证失败：发布错误事件
                if self._event_bus:
                    await self._event_bus.publish(error_event)

                logger.error(f"配置验证失败: {error_event.error}")
                return False, error_event.error

            # 2. 原子更新
            old_config = self._config_snapshot
            async with self._write_lock:
                self._config_snapshot = config_dict
                self._config_version += 1

            # 3. 发布成功事件
            if self._event_bus:
                await self._event_bus.publish(
                    ConfigChangedEvent(
                        config=config_dict, version=self._config_version, old_config=old_config
                    )
                )

            logger.info(f"✅ 配置已更新到版本 {self._config_version}")
            return True, ""

        except Exception as e:
            error_msg = f"配置重载异常: {str(e)}"
            logger.error(error_msg, exc_info=True)
            return False, error_msg
