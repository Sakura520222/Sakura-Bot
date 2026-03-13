# Copyright 2026 Sakura-Bot
#
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
#
# - 署名：必须提供本项目的原始来源链接
# - 相同方式共享：衍生作品必须采用相同的许可证
#
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
# 许可证全文：参见 LICENSE 文件

"""系统配置管理模块

此模块管理系统级别的配置，包括频道列表、日志级别、语言等。
支持配置热重载，当配置文件发生变化时自动更新。
"""

import asyncio
import logging
from pathlib import Path

from core.config import AsyncIOEventBus, ConfigChangedEvent
from core.infrastructure.utils.constants import CONFIG_FILE

logger = logging.getLogger(__name__)


class SystemConfigManager:
    """系统配置管理器

    管理系统级别的配置，包括频道列表、日志级别、语言等。
    支持配置热重载，当config.json发生变化时自动更新。
    """

    def __init__(self, event_bus: AsyncIOEventBus = None, config_file: str = None):
        """初始化系统配置管理器

        Args:
            event_bus: 事件总线，用于配置热重载（可选）
            config_file: 配置文件路径，默认为 config.json
        """
        self._event_bus = event_bus
        self._config_file = Path(config_file or CONFIG_FILE)
        self._config = {}

        # 系统配置项
        self._channels: dict[str, dict] = {}
        self._log_level = "INFO"
        self._language = "zh-CN"
        self._send_report_to_source = True

        # 订阅配置变更事件
        if event_bus:
            asyncio.create_task(
                event_bus.subscribe(
                    ConfigChangedEvent,
                    self.on_config_updated,
                    priority=AsyncIOEventBus.PRIORITY_HIGH,
                )
            )
            logger.info("✅ 系统配置管理器已订阅配置变更事件")

        # 加载初始配置
        self._load_config()

    def _load_config(self):
        """从配置文件加载系统配置"""
        try:
            import json

            # 直接读取配置文件
            if not self._config_file.exists():
                logger.warning(f"配置文件 {self._config_file} 不存在，使用空配置")
                self._config = {}
            else:
                with open(self._config_file, encoding="utf-8") as f:
                    self._config = json.load(f)

            # 提取系统配置项
            self._channels = self._config.get("channels", {})
            self._log_level = self._config.get("log_level", "INFO")
            self._language = self._config.get("language", "zh-CN")
            self._send_report_to_source = self._config.get("send_report_to_source", True)

            # 应用初始配置
            self._apply_log_level()

            logger.info(
                f"系统配置已加载: "
                f"{len(self._channels)} 个频道, "
                f"日志级别={self._log_level}, "
                f"语言={self._language}"
            )
        except Exception as e:
            logger.error(f"加载系统配置失败: {e}", exc_info=True)
            # 使用默认值
            self._channels = []
            self._log_level = "INFO"
            self._language = "zh-CN"

    async def on_config_updated(self, event: ConfigChangedEvent):
        """配置更新处理

        当config.json发生变化时，自动更新系统配置

        Args:
            event: 配置变更事件，包含完整的配置字典
        """
        try:
            # 记录旧配置
            old_channels = self._channels.copy()
            old_log_level = self._log_level
            old_language = self._language
            old_send_report = self._send_report_to_source

            # 更新配置
            self._config = event.config
            self._channels = self._config.get("channels", {})
            self._log_level = self._config.get("log_level", "INFO")
            self._language = self._config.get("language", "zh-CN")
            self._send_report_to_source = self._config.get("send_report_to_source", True)

            # 检测具体变更
            changes = []
            if old_channels != self._channels:
                changes.append(f"频道列表: {len(old_channels)} → {len(self._channels)}")
            if old_log_level != self._log_level:
                changes.append(f"日志级别: {old_log_level} → {self._log_level}")
                self._apply_log_level()
            if old_language != self._language:
                changes.append(f"语言: {old_language} → {self._language}")
            if old_send_report != self._send_report_to_source:
                changes.append(f"报告来源: {old_send_report} → {self._send_report_to_source}")

            if changes:
                logger.info(
                    f"✅ 系统配置已热重载: "
                    f"版本={event.version}, "
                    f"{', '.join(changes)}, "
                    f"变更字段: {event.changed_fields if event.changed_fields else '全部'}"
                )
            else:
                logger.debug("系统配置已更新，但没有检测到系统级别配置变更")

        except Exception as e:
            logger.error(f"❌ 处理系统配置更新失败: {type(e).__name__}: {e}", exc_info=True)
            # 保持现有配置不变

    def _apply_log_level(self):
        """应用日志级别配置"""
        try:
            import logging

            level = getattr(logging, self._log_level.upper(), logging.INFO)
            logging.getLogger().setLevel(level)
            logger.info(f"日志级别已设置为: {self._log_level}")
        except Exception as e:
            logger.error(f"设置日志级别失败: {e}")

    @property
    def channels(self) -> dict[str, dict]:
        """获取频道配置字典"""
        return self._channels

    @property
    def log_level(self) -> str:
        """获取日志级别"""
        return self._log_level

    @property
    def language(self) -> str:
        """获取语言配置"""
        return self._language

    @property
    def send_report_to_source(self) -> bool:
        """是否发送报告到源频道"""
        return self._send_report_to_source

    @property
    def config(self) -> dict:
        """获取完整配置字典"""
        return self._config.copy()


# 全局配置管理器实例
_system_config_manager: SystemConfigManager | None = None


def get_system_config_manager(event_bus: AsyncIOEventBus = None) -> SystemConfigManager:
    """获取全局系统配置管理器（单例模式）

    Args:
        event_bus: 事件总线，用于配置热重载（可选）

    Returns:
        SystemConfigManager 实例
    """
    global _system_config_manager
    if _system_config_manager is None:
        _system_config_manager = SystemConfigManager(event_bus=event_bus)
        logger.info("系统配置管理器已初始化")
    return _system_config_manager
