# Copyright 2026 Sakura-Bot
#
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
#
# - 署名：必须提供本项目的原始来源链接
# - 相同方式共享：衍生作品必须采用相同的许可证
#
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
# 许可证全文：参见 LICENSE 文件

"""频道时间配置管理模块

此模块管理频道级的时间配置，支持每天和每周两种模式。
"""

import json
import logging
from pathlib import Path
from typing import Literal

from core.config import AsyncIOEventBus, ConfigChangedEvent
from core.infrastructure.utils.constants import (
    CONFIG_FILE,
    DAY_NAMES_CN,
    DEFAULT_SUMMARY_DAY,
    DEFAULT_SUMMARY_HOUR,
    DEFAULT_SUMMARY_MINUTE,
    VALID_DAYS,
    VALID_FREQUENCIES,
)
from core.infrastructure.utils.exceptions import ConfigurationError, InvalidScheduleError

logger = logging.getLogger(__name__)


# 时间配置类型定义
ScheduleFrequency = Literal["daily", "weekly"]
ScheduleConfig = dict[
    str,  # 'frequency', 'days', 'hour', 'minute'
    str | int | list[str],
]


class ChannelScheduleManager:
    """频道时间配置管理器

    管理频道级的时间配置，支持每天和每周两种模式。
    """

    def __init__(
        self, config_manager=None, event_bus: AsyncIOEventBus = None, config_file: str | None = None
    ):
        """初始化配置管理器

        Args:
            config_manager: 配置管理器实例
            event_bus: 事件总线实例
            config_file: 配置文件路径，默认为 config.json
        """
        self._config_file = Path(config_file or CONFIG_FILE)
        self._schedules: dict[str, ScheduleConfig] = {}
        self._config_manager = config_manager
        self._event_bus = event_bus

        # 订阅配置变更事件
        if event_bus:
            event_bus.subscribe(
                ConfigChangedEvent, self.on_config_updated, priority=AsyncIOEventBus.PRIORITY_HIGH
            )

        # 如果没有传入config_manager，使用传统的文件加载方式
        if not config_manager:
            self._load_schedules()

    def _load_schedules(self) -> None:
        """从配置文件加载时间配置"""
        if not self._config_file.exists():
            logger.info("配置文件不存在，使用空配置")
            self._schedules = {}
            return

        try:
            with open(self._config_file, encoding="utf-8") as f:
                config = json.load(f)

            # 从新的频道中心化配置中提取总结时间设置
            channels = config.get("channels", {})
            schedules = {}
            for channel_url, channel_config in channels.items():
                if isinstance(channel_config, dict):
                    summary_config = channel_config.get("summary", {})
                    if "schedule" in summary_config:
                        schedules[channel_url] = summary_config["schedule"]

            # 标准化所有配置
            self._schedules = {
                channel: self._normalize_schedule(schedule)
                for channel, schedule in schedules.items()
            }
            logger.info(f"已加载 {len(self._schedules)} 个频道的时间配置")
        except json.JSONDecodeError as e:
            raise ConfigurationError(f"配置文件格式错误: {e}") from e
        except Exception as e:
            raise ConfigurationError(f"加载配置文件失败: {e}") from e

    def _save_schedules(self) -> None:
        """保存时间配置到文件"""
        try:
            # 读取完整配置
            full_config = {}
            if self._config_file.exists():
                with open(self._config_file, encoding="utf-8") as f:
                    full_config = json.load(f)

            # 确保 channels 存在
            if "channels" not in full_config:
                full_config["channels"] = {}

            # 更新每个频道的时间配置
            for channel_url, schedule in self._schedules.items():
                if channel_url not in full_config["channels"]:
                    full_config["channels"][channel_url] = {}
                if "summary" not in full_config["channels"][channel_url]:
                    full_config["channels"][channel_url]["summary"] = {}
                full_config["channels"][channel_url]["summary"]["schedule"] = schedule

            # 确保目录存在
            self._config_file.parent.mkdir(parents=True, exist_ok=True)

            # 保存配置
            with open(self._config_file, "w", encoding="utf-8") as f:
                json.dump(full_config, f, ensure_ascii=False, indent=2)

            logger.info(f"已保存 {len(self._schedules)} 个频道的时间配置")
        except Exception as e:
            raise ConfigurationError(f"保存配置文件失败: {e}") from e

    def _normalize_schedule(self, schedule: ScheduleConfig) -> ScheduleConfig:
        """标准化时间配置，处理向后兼容

        Args:
            schedule: 原始配置字典

        Returns:
            标准化后的配置字典
        """
        # 如果包含 frequency 字段，已经是新格式
        if "frequency" in schedule:
            frequency = schedule["frequency"]
            if frequency == "weekly" and "days" not in schedule:
                # 如果没有 days 字段但有 day 字段，转换它
                if "day" in schedule:
                    schedule["days"] = [schedule["day"]]
                else:
                    schedule["days"] = [DEFAULT_SUMMARY_DAY]
            return schedule

        # 向后兼容：旧格式 (day 字段)
        if "day" in schedule:
            return {
                "frequency": "weekly",
                "days": [schedule["day"]],
                "hour": schedule.get("hour", DEFAULT_SUMMARY_HOUR),
                "minute": schedule.get("minute", DEFAULT_SUMMARY_MINUTE),
            }

        # 默认配置
        return {
            "frequency": "weekly",
            "days": [DEFAULT_SUMMARY_DAY],
            "hour": schedule.get("hour", DEFAULT_SUMMARY_HOUR),
            "minute": schedule.get("minute", DEFAULT_SUMMARY_MINUTE),
        }

    def _validate_schedule(self, config: ScheduleConfig) -> None:
        """验证时间配置

        Args:
            config: 配置字典

        Raises:
            InvalidScheduleError: 配置无效时
        """
        frequency = config.get("frequency")
        if frequency not in VALID_FREQUENCIES:
            raise InvalidScheduleError(
                f"无效的频率: {frequency}，有效值: {', '.join(VALID_FREQUENCIES)}"
            )

        # 验证 days（仅 weekly 模式需要）
        if frequency == "weekly":
            days = config.get("days", [])
            if not isinstance(days, list) or not days:
                raise InvalidScheduleError("weekly 模式必须提供 days 字段（非空数组）")

            for day in days:
                if day not in VALID_DAYS:
                    raise InvalidScheduleError(
                        f"无效的星期几: {day}，有效值: {', '.join(VALID_DAYS)}"
                    )

        # 验证时间
        hour = config.get("hour")
        minute = config.get("minute")

        if not isinstance(hour, int) or hour < 0 or hour > 23:
            raise InvalidScheduleError(f"无效的小时: {hour}，有效范围: 0-23")

        if not isinstance(minute, int) or minute < 0 or minute > 59:
            raise InvalidScheduleError(f"无效的分钟: {minute}，有效范围: 0-59")

    def get_schedule(self, channel: str) -> ScheduleConfig:
        """获取指定频道的自动总结时间配置

        Args:
            channel: 频道 URL

        Returns:
            标准化的配置字典，包含 frequency, days, hour, minute
        """
        if channel in self._schedules:
            return self._schedules[channel].copy()

        # 返回默认配置
        return {
            "frequency": "weekly",
            "days": [DEFAULT_SUMMARY_DAY],
            "hour": DEFAULT_SUMMARY_HOUR,
            "minute": DEFAULT_SUMMARY_MINUTE,
        }

    def set_schedule(
        self,
        channel: str,
        frequency: ScheduleFrequency,
        days: list[str] | None = None,
        hour: int | None = None,
        minute: int | None = None,
    ) -> None:
        """设置指定频道的自动总结时间配置

        Args:
            channel: 频道 URL
            frequency: 频率类型（'daily' 或 'weekly'）
            days: 星期几列表（weekly 模式必需）
            hour: 小时（0-23）
            minute: 分钟（0-59）

        Raises:
            InvalidScheduleError: 配置无效时
            ConfigurationError: 保存配置失败时
        """
        # 构建配置字典
        config: ScheduleConfig = {
            "frequency": frequency,
            "hour": hour if hour is not None else DEFAULT_SUMMARY_HOUR,
            "minute": minute if minute is not None else DEFAULT_SUMMARY_MINUTE,
        }

        # weekly 模式需要 days 字段
        if frequency == "weekly":
            if days is None:
                days = [DEFAULT_SUMMARY_DAY]
            config["days"] = days

        # 验证配置
        self._validate_schedule(config)

        # 更新配置
        self._schedules[channel] = config
        self._save_schedules()

        logger.info(f"已更新频道 {channel} 的时间配置: {config}")

    def delete_schedule(self, channel: str) -> bool:
        """删除指定频道的自动总结时间配置

        Args:
            channel: 频道 URL

        Returns:
            是否成功删除配置
        """
        if channel in self._schedules:
            del self._schedules[channel]
            self._save_schedules()
            logger.info(f"已删除频道 {channel} 的时间配置")
            return True

        logger.info(f"频道 {channel} 没有时间配置，无需删除")
        return False

    def get_all_schedules(self) -> dict[str, ScheduleConfig]:
        """获取所有频道的时间配置

        Returns:
            所有频道配置的字典
        """
        return self._schedules.copy()

    def build_cron_trigger(self, channel: str) -> dict[str, str | int]:
        """根据配置构建 APScheduler cron 触发器参数

        Args:
            channel: 频道 URL

        Returns:
            包含 cron 触发器参数的字典
        """
        schedule = self.get_schedule(channel)
        frequency = schedule.get("frequency", "weekly")

        if frequency == "daily":
            return {"day_of_week": "*", "hour": schedule["hour"], "minute": schedule["minute"]}
        elif frequency == "weekly":
            days_str = ",".join(schedule["days"])  # type: ignore
            return {"day_of_week": days_str, "hour": schedule["hour"], "minute": schedule["minute"]}
        else:
            return {
                "day_of_week": "mon",
                "hour": schedule.get("hour", DEFAULT_SUMMARY_HOUR),
                "minute": schedule.get("minute", DEFAULT_SUMMARY_MINUTE),
            }

    def format_schedule_text(self, channel: str) -> str:
        """格式化频道时间配置为可读文本

        Args:
            channel: 频道 URL

        Returns:
            格式化后的文本
        """
        schedule = self.get_schedule(channel)
        frequency = schedule.get("frequency", "weekly")
        hour = schedule["hour"]
        minute = schedule["minute"]

        if frequency == "daily":
            return f"每天 {hour:02d}:{minute:02d}"
        elif frequency == "weekly":
            days_cn = "、".join([DAY_NAMES_CN.get(d, d) for d in schedule.get("days", [])])  # type: ignore
            return f"每周{days_cn} {hour:02d}:{minute:02d}"
        else:
            return "未知配置"

    async def initialize(self):
        """初始化（从config_manager加载配置）"""
        if self._config_manager:
            config = await self._config_manager.get_config()
            self._load_schedules_from_config(config)
        else:
            self._load_schedules()

    async def on_config_updated(self, event: ConfigChangedEvent):
        """配置更新处理

        Args:
            event: 配置变更事件
        """
        logger.info("收到配置更新事件，重新加载频道配置")
        config = event.config
        self._load_schedules_from_config(config)

    def _load_schedules_from_config(self, config: dict):
        """从配置字典加载时间配置

        Args:
            config: 配置字典
        """
        if not config:
            logger.info("配置为空，使用空配置")
            self._schedules = {}
            return

        try:
            # 从新的频道中心化配置中提取总结时间设置
            channels = config.get("channels", {})
            schedules = {}
            for channel_url, channel_config in channels.items():
                if isinstance(channel_config, dict):
                    summary_config = channel_config.get("summary", {})
                    if "schedule" in summary_config:
                        schedules[channel_url] = summary_config["schedule"]

            # 标准化所有配置
            self._schedules = {
                channel: self._normalize_schedule(schedule)
                for channel, schedule in schedules.items()
            }
            logger.info(f"已加载 {len(self._schedules)} 个频道的时间配置")
        except Exception as e:
            logger.error(f"加载时间配置失败: {e}")
            self._schedules = {}


# 全局配置管理器实例
_schedule_manager: ChannelScheduleManager | None = None


def get_schedule_manager(
    config_manager=None, event_bus: AsyncIOEventBus = None
) -> ChannelScheduleManager:
    """获取全局频道时间配置管理器（单例模式）

    Args:
        config_manager: 配置管理器实例
        event_bus: 事件总线实例

    Returns:
        ChannelScheduleManager 实例
    """
    global _schedule_manager
    if _schedule_manager is None:
        _schedule_manager = ChannelScheduleManager(config_manager, event_bus)
        logger.info("频道时间配置管理器已初始化")
    return _schedule_manager


# 便捷函数（保持向后兼容）
def get_channel_schedule(channel: str) -> ScheduleConfig:
    """获取指定频道的自动总结时间配置"""
    return get_schedule_manager().get_schedule(channel)


def set_channel_schedule(
    channel: str,
    frequency: ScheduleFrequency,
    days: list[str] | None = None,
    hour: int | None = None,
    minute: int | None = None,
) -> None:
    """设置指定频道的自动总结时间配置"""
    get_schedule_manager().set_schedule(channel, frequency, days, hour, minute)


def delete_channel_schedule(channel: str) -> bool:
    """删除指定频道的自动总结时间配置"""
    return get_schedule_manager().delete_schedule(channel)


def build_cron_trigger(channel: str) -> dict[str, str | int]:
    """根据配置构建 APScheduler cron 触发器参数"""
    return get_schedule_manager().build_cron_trigger(channel)
