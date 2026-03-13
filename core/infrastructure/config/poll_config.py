# Copyright 2026 Sakura-Bot
#
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
#
# - 署名：必须提供本项目的原始来源链接
# - 相同方式共享：衍生作品必须采用相同的许可证
#
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
# 许可证全文：参见 LICENSE 文件

"""投票配置管理模块

此模块管理频道级的投票配置，包括启用状态和发送位置。
"""

import asyncio
import json
import logging
from pathlib import Path
from typing import Literal

from core.config import AsyncIOEventBus, ConfigChangedEvent
from core.i18n.i18n import get_text
from core.infrastructure.utils.constants import CONFIG_FILE
from core.infrastructure.utils.exceptions import ConfigurationError

logger = logging.getLogger(__name__)


# 投票发送位置类型
PollSendLocation = Literal["channel", "discussion"]


# 投票配置类型定义
ChannelPollConfig = dict[
    str,  # 'enabled', 'send_to_channel', 'regen_threshold', 'enable_vote_regen_request'
    bool | int | None,
]


class ChannelPollConfigManager:
    """频道投票配置管理器

    管理频道级的投票配置，包括启用状态和发送位置。
    """

    def __init__(self, config_file: str | None = None, event_bus: AsyncIOEventBus = None):
        """初始化配置管理器

        Args:
            config_file: 配置文件路径，默认为 config.json
            event_bus: 事件总线，用于配置热重载（可选）
        """
        self._config_file = Path(config_file or CONFIG_FILE)
        self._event_bus = event_bus
        self._poll_settings: dict[str, ChannelPollConfig] = {}

        # 订阅配置变更事件
        if event_bus:
            asyncio.create_task(
                event_bus.subscribe(
                    ConfigChangedEvent,
                    self.on_config_updated,
                    priority=AsyncIOEventBus.PRIORITY_HIGH,
                )
            )
            logger.info("✅ 投票配置管理器已订阅配置变更事件")

        self._load_settings()

    def _load_settings(self) -> None:
        """从配置文件加载投票配置"""
        if not self._config_file.exists():
            logger.info("配置文件不存在，使用空配置")
            self._poll_settings = {}
            return

        try:
            with open(self._config_file, encoding="utf-8") as f:
                config = json.load(f)

            # 从新的频道中心化配置中提取投票设置
            channels = config.get("channels", {})
            poll_settings = {}
            for channel_url, channel_config in channels.items():
                if isinstance(channel_config, dict) and "poll" in channel_config:
                    poll_settings[channel_url] = channel_config["poll"]

            self._poll_settings = poll_settings
            logger.info(f"已加载 {len(self._poll_settings)} 个频道的投票配置")
        except json.JSONDecodeError as e:
            raise ConfigurationError(f"配置文件格式错误: {e}") from e
        except Exception as e:
            raise ConfigurationError(f"加载配置文件失败: {e}") from e

    def _save_settings(self) -> None:
        """保存投票配置到文件"""
        try:
            # 读取完整配置
            full_config = {}
            if self._config_file.exists():
                with open(self._config_file, encoding="utf-8") as f:
                    full_config = json.load(f)

            # 确保 channels 存在
            if "channels" not in full_config:
                full_config["channels"] = {}

            # 更新每个频道的投票配置
            for channel_url, poll_config in self._poll_settings.items():
                if channel_url not in full_config["channels"]:
                    full_config["channels"][channel_url] = {}
                full_config["channels"][channel_url]["poll"] = poll_config

            # 确保目录存在
            self._config_file.parent.mkdir(parents=True, exist_ok=True)

            # 保存配置
            with open(self._config_file, "w", encoding="utf-8") as f:
                json.dump(full_config, f, ensure_ascii=False, indent=2)

            logger.info(f"已保存 {len(self._poll_settings)} 个频道的投票配置")
        except Exception as e:
            raise ConfigurationError(f"保存配置文件失败: {e}") from e

    async def on_config_updated(self, event: ConfigChangedEvent):
        """配置更新处理

        当config.json发生变化时，自动更新投票配置

        Args:
            event: 配置变更事件，包含完整的配置字典
        """
        try:
            # 从新的频道中心化配置中提取投票设置
            channels = event.config.get("channels", {})
            poll_settings = {}
            for channel_url, channel_config in channels.items():
                if isinstance(channel_config, dict) and "poll" in channel_config:
                    poll_settings[channel_url] = channel_config["poll"]

            # 记录配置更新详情
            old_count = len(self._poll_settings)
            self._poll_settings = poll_settings
            new_count = len(self._poll_settings)

            logger.info(
                f"✅ 投票配置已热重载: "
                f"版本={event.version}, "
                f"频道数量: {old_count} → {new_count}, "
                f"变更字段: {event.changed_fields if event.changed_fields else '全部'}"
            )

            # 如果有投票配置变更，记录详细信息
            if event.changed_fields and "channels" in str(event.changed_fields):
                logger.info("🔄 投票配置已更新并生效")

        except Exception as e:
            logger.error(f"❌ 处理投票配置更新失败: {type(e).__name__}: {e}", exc_info=True)
            # 保持现有配置不变

    def get_config(self, channel: str) -> ChannelPollConfig:
        """获取指定频道的投票配置

        Args:
            channel: 频道 URL

        Returns:
            包含以下键的字典：
            - enabled (bool | None): 是否启用投票（None 表示使用全局配置）
            - send_to_channel (bool): true=频道模式, false=讨论组模式
            - regen_threshold (int): 重新生成阈值
            - enable_vote_regen_request (bool): 是否启用投票重新生成请求
        """
        if channel in self._poll_settings:
            config = self._poll_settings[channel]
            return {
                "enabled": config.get("enabled", None),
                "send_to_channel": config.get("send_to_channel", False),
                "regen_threshold": config.get("regen_threshold", 5),
                "enable_vote_regen_request": config.get("enable_vote_regen_request", True),
            }

        # 没有独立配置，返回默认配置
        return {
            "enabled": None,  # 使用全局配置
            "send_to_channel": False,  # 默认讨论组模式
            "regen_threshold": 5,  # 默认阈值
            "enable_vote_regen_request": True,  # 默认启用
        }

    def set_config(
        self, channel: str, enabled: bool | None = None, send_to_channel: bool | None = None
    ) -> None:
        """设置指定频道的投票配置

        Args:
            channel: 频道 URL
            enabled: 是否启用投票（None 表示不修改）
            send_to_channel: 投票发送位置（None 表示不修改）
                True - 频道模式（直接发送到频道）
                False - 讨论组模式（发送到讨论组）

        Raises:
            ConfigurationError: 保存配置失败时
        """
        # 获取频道当前配置
        if channel not in self._poll_settings:
            self._poll_settings[channel] = {}

        channel_config = self._poll_settings[channel]

        # 更新配置（只更新提供的参数）
        if enabled is not None:
            channel_config["enabled"] = enabled
            logger.info(f"设置频道 {channel} 的投票启用状态: {enabled}")

        if send_to_channel is not None:
            channel_config["send_to_channel"] = send_to_channel
            location = "频道" if send_to_channel else "讨论组"
            logger.info(f"设置频道 {channel} 的投票发送位置: {location}")

        # 保存配置
        self._save_settings()
        logger.info(f"已更新频道 {channel} 的投票配置")

    def delete_config(self, channel: str) -> bool:
        """删除指定频道的投票配置

        删除后，该频道将使用全局投票配置。

        Args:
            channel: 频道 URL

        Returns:
            是否成功删除配置
        """
        if channel in self._poll_settings:
            del self._poll_settings[channel]
            self._save_settings()
            logger.info(f"已删除频道 {channel} 的投票配置，将使用全局配置")
            return True

        logger.info(f"频道 {channel} 没有独立的投票配置，无需删除")
        return False

    def get_all_configs(self) -> dict[str, ChannelPollConfig]:
        """获取所有频道的投票配置

        Returns:
            所有频道配置的字典
        """
        return self._poll_settings.copy()

    def format_config_text(self, channel: str, global_enabled: bool = True) -> str:
        """格式化频道投票配置为可读文本

        Args:
            channel: 频道 URL
            global_enabled: 全局投票启用状态

        Returns:
            格式化后的文本
        """
        config = self.get_config(channel)

        # 确定实际启用状态
        enabled = config["enabled"]
        if enabled is None:
            enabled = global_enabled
            enabled_text = get_text("poll.status_global").format(
                status=get_text("poll.status_enabled" if enabled else "poll.status_disabled")
            )
        else:
            enabled_text = get_text("poll.status_enabled" if enabled else "poll.status_disabled")

        # 确定发送位置
        send_to_channel = config["send_to_channel"]
        location_text = get_text(
            "poll.location_channel" if send_to_channel else "poll.location_discussion"
        )

        return f"{enabled_text} | {location_text}"


# 全局配置管理器实例
_poll_config_manager: ChannelPollConfigManager | None = None


def get_poll_config_manager(event_bus: AsyncIOEventBus = None) -> ChannelPollConfigManager:
    """获取全局频道投票配置管理器（单例模式）

    Args:
        event_bus: 事件总线，用于配置热重载（可选）

    Returns:
        ChannelPollConfigManager 实例
    """
    global _poll_config_manager
    if _poll_config_manager is None:
        _poll_config_manager = ChannelPollConfigManager(event_bus=event_bus)
        logger.info("频道投票配置管理器已初始化")
    return _poll_config_manager


# 便捷函数（保持向后兼容）
def get_channel_poll_config(channel: str) -> ChannelPollConfig:
    """获取指定频道的投票配置"""
    return get_poll_config_manager().get_config(channel)


def set_channel_poll_config(
    channel: str, enabled: bool | None = None, send_to_channel: bool | None = None
) -> None:
    """设置指定频道的投票配置"""
    get_poll_config_manager().set_config(channel, enabled, send_to_channel)


def delete_channel_poll_config(channel: str) -> bool:
    """删除指定频道的投票配置"""
    return get_poll_config_manager().delete_config(channel)
