"""测试 Channel Config 模块

Copyright 2026 Sakura-Bot
本项目采用 AGPL-3.0 许可
"""

import json

import pytest

from core.channel_config import (
    ChannelScheduleManager,
    build_cron_trigger,
    delete_channel_schedule,
    get_channel_schedule,
    get_schedule_manager,
    set_channel_schedule,
)
from core.exceptions import ConfigurationError, InvalidScheduleError


@pytest.mark.unit
class TestChannelScheduleManagerInit:
    """配置管理器初始化测试"""

    def test_init_with_default_config(self, tmp_path):
        """测试使用默认配置初始化"""
        config_file = tmp_path / "config.json"

        manager = ChannelScheduleManager(str(config_file))

        assert manager._schedules == {}

    def test_init_loads_existing_schedules(self, tmp_path):
        """测试加载现有配置"""
        config_file = tmp_path / "config.json"
        schedules = {
            "summary_schedules": {
                "https://t.me/channel1": {"frequency": "daily", "hour": 9, "minute": 0}
            }
        }
        config_file.write_text(json.dumps(schedules), encoding="utf-8")

        manager = ChannelScheduleManager(str(config_file))

        assert len(manager._schedules) == 1
        assert "https://t.me/channel1" in manager._schedules

    def test_init_with_invalid_json(self, tmp_path):
        """测试加载无效JSON"""
        config_file = tmp_path / "config.json"
        config_file.write_text("invalid json", encoding="utf-8")

        with pytest.raises(ConfigurationError):
            ChannelScheduleManager(str(config_file))


@pytest.mark.unit
class TestGetSchedule:
    """获取配置测试"""

    def test_get_existing_schedule(self, tmp_path):
        """测试获取存在的配置"""
        config_file = tmp_path / "config.json"
        schedules = {
            "summary_schedules": {
                "https://t.me/channel1": {"frequency": "daily", "hour": 10, "minute": 30}
            }
        }
        config_file.write_text(json.dumps(schedules), encoding="utf-8")

        manager = ChannelScheduleManager(str(config_file))
        schedule = manager.get_schedule("https://t.me/channel1")

        assert schedule["frequency"] == "daily"
        assert schedule["hour"] == 10
        assert schedule["minute"] == 30

    def test_get_nonexistent_schedule(self):
        """测试获取不存在的配置返回默认值"""
        manager = ChannelScheduleManager()
        schedule = manager.get_schedule("https://t.me/channel1")

        assert schedule["frequency"] == "weekly"
        assert schedule["hour"] >= 0
        assert schedule["minute"] >= 0


@pytest.mark.unit
class TestSetSchedule:
    """设置配置测试"""

    def test_set_daily_schedule(self, tmp_path):
        """测试设置每日配置"""
        config_file = tmp_path / "config.json"
        config_file.write_text('{"summary_schedules": {}}', encoding="utf-8")

        manager = ChannelScheduleManager(str(config_file))
        manager.set_schedule("https://t.me/channel1", "daily", hour=9, minute=30)

        schedule = manager.get_schedule("https://t.me/channel1")
        assert schedule["frequency"] == "daily"
        assert schedule["hour"] == 9

    def test_set_weekly_schedule(self, tmp_path):
        """测试设置每周配置"""
        config_file = tmp_path / "config.json"
        config_file.write_text('{"summary_schedules": {}}', encoding="utf-8")

        manager = ChannelScheduleManager(str(config_file))
        manager.set_schedule(
            "https://t.me/channel1", "weekly", days=["mon", "wed"], hour=10, minute=0
        )

        schedule = manager.get_schedule("https://t.me/channel1")
        assert schedule["frequency"] == "weekly"
        assert schedule["days"] == ["mon", "wed"]

    def test_set_invalid_frequency(self, tmp_path):
        """测试设置无效频率"""
        config_file = tmp_path / "config.json"
        config_file.write_text('{"summary_schedules": {}}', encoding="utf-8")

        manager = ChannelScheduleManager(str(config_file))

        with pytest.raises(InvalidScheduleError):
            manager.set_schedule("https://t.me/channel1", "invalid", hour=9)

    def test_set_invalid_hour(self, tmp_path):
        """测试设置无效小时"""
        config_file = tmp_path / "config.json"
        config_file.write_text('{"summary_schedules": {}}', encoding="utf-8")

        manager = ChannelScheduleManager(str(config_file))

        with pytest.raises(InvalidScheduleError):
            manager.set_schedule("https://t.me/channel1", "daily", hour=25)

    def test_set_invalid_minute(self, tmp_path):
        """测试设置无效分钟"""
        config_file = tmp_path / "config.json"
        config_file.write_text('{"summary_schedules": {}}', encoding="utf-8")

        manager = ChannelScheduleManager(str(config_file))

        with pytest.raises(InvalidScheduleError):
            manager.set_schedule("https://t.me/channel1", "daily", minute=60)


@pytest.mark.unit
class TestDeleteSchedule:
    """删除配置测试"""

    def test_delete_existing_schedule(self, tmp_path):
        """测试删除存在的配置"""
        config_file = tmp_path / "config.json"
        schedules = {
            "summary_schedules": {
                "https://t.me/channel1": {"frequency": "daily", "hour": 9, "minute": 0}
            }
        }
        config_file.write_text(json.dumps(schedules), encoding="utf-8")

        manager = ChannelScheduleManager(str(config_file))
        result = manager.delete_schedule("https://t.me/channel1")

        assert result is True
        assert "https://t.me/channel1" not in manager._schedules

    def test_delete_nonexistent_schedule(self):
        """测试删除不存在的配置"""
        manager = ChannelScheduleManager()
        result = manager.delete_schedule("https://t.me/channel1")

        assert result is False


@pytest.mark.unit
class TestFormatScheduleText:
    """格式化配置文本测试"""

    def test_format_daily_schedule(self):
        """测试格式化每日配置"""
        manager = ChannelScheduleManager()
        manager._schedules = {
            "https://t.me/channel1": {"frequency": "daily", "hour": 9, "minute": 30}
        }

        text = manager.format_schedule_text("https://t.me/channel1")

        assert "每天" in text
        assert "09:30" in text

    def test_format_weekly_schedule(self):
        """测试格式化每周配置"""
        manager = ChannelScheduleManager()
        manager._schedules = {
            "https://t.me/channel1": {
                "frequency": "weekly",
                "days": ["mon", "wed"],
                "hour": 10,
                "minute": 0,
            }
        }

        text = manager.format_schedule_text("https://t.me/channel1")

        assert "每周" in text
        assert "10:00" in text


@pytest.mark.unit
class TestBuildCronTrigger:
    """构建cron触发器测试"""

    def test_build_daily_trigger(self):
        """测试构建每日触发器"""
        manager = ChannelScheduleManager()
        manager._schedules = {
            "https://t.me/channel1": {"frequency": "daily", "hour": 9, "minute": 30}
        }

        trigger = manager.build_cron_trigger("https://t.me/channel1")

        assert trigger["day_of_week"] == "*"
        assert trigger["hour"] == 9
        assert trigger["minute"] == 30

    def test_build_weekly_trigger(self):
        """测试构建每周触发器"""
        manager = ChannelScheduleManager()
        manager._schedules = {
            "https://t.me/channel1": {
                "frequency": "weekly",
                "days": ["mon", "wed"],
                "hour": 10,
                "minute": 0,
            }
        }

        trigger = manager.build_cron_trigger("https://t.me/channel1")

        assert "mon" in trigger["day_of_week"]
        assert trigger["hour"] == 10


@pytest.mark.unit
class TestGetScheduleManager:
    """获取配置管理器实例测试"""

    def test_singleton(self):
        """测试单例模式"""
        import core.channel_config

        core.channel_config._schedule_manager = None

        manager1 = get_schedule_manager()
        manager2 = get_schedule_manager()

        assert manager1 is manager2


@pytest.mark.unit
class TestConvenienceFunctions:
    """便捷函数测试"""

    def test_get_channel_schedule(self):
        """测试获取频道配置便捷函数"""
        import core.channel_config

        core.channel_config._schedule_manager = None

        schedule = get_channel_schedule("https://t.me/channel1")

        assert isinstance(schedule, dict)

    def test_set_channel_schedule(self, tmp_path):
        """测试设置频道配置便捷函数"""
        import core.channel_config

        core.channel_config._schedule_manager = None

        config_file = tmp_path / "config.json"
        with open(config_file, "w", encoding="utf-8") as f:
            json.dump({"summary_schedules": {}}, f)

        set_channel_schedule("https://t.me/channel1", "daily", hour=9)

        schedule = get_channel_schedule("https://t.me/channel1")
        assert schedule["frequency"] == "daily"

    def test_delete_channel_schedule(self, tmp_path):
        """测试删除频道配置便捷函数"""
        import core.channel_config

        core.channel_config._schedule_manager = None

        config_file = tmp_path / "config.json"
        schedules = {"summary_schedules": {"https://t.me/channel1": {"frequency": "daily"}}}
        with open(config_file, "w", encoding="utf-8") as f:
            json.dump(schedules, f)

        result = delete_channel_schedule("https://t.me/channel1")

        assert result is True

    def test_build_cron_trigger_function(self):
        """测试构建cron触发器便捷函数"""
        import core.channel_config

        core.channel_config._schedule_manager = None

        trigger = build_cron_trigger("https://t.me/channel1")

        assert isinstance(trigger, dict)


if __name__ == "__main__":
    pytest.main([__file__, "-v"])
