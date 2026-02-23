"""测试 Message Utils 模块

Copyright 2026 Sakura-Bot
本项目采用 AGPL-3.0 许可
"""

import pytest

from core.utils.message_utils import format_schedule_info


@pytest.mark.unit
class TestFormatScheduleInfo:
    """测试格式化调度配置信息"""

    def test_format_daily_schedule(self):
        """测试格式化每日调度"""
        channel = "https://t.me/test_channel"
        schedule = {"frequency": "daily", "hour": 9, "minute": 30}

        result = format_schedule_info(channel, schedule)

        assert "test_channel" in result
        assert "09:30" in result

    def test_format_weekly_schedule(self):
        """测试格式化每周调度"""
        channel = "https://t.me/test_channel"
        schedule = {"frequency": "weekly", "hour": 14, "minute": 0, "days": ["mon", "wed", "fri"]}

        result = format_schedule_info(channel, schedule)

        assert "test_channel" in result
        assert "14:00" in result
        # 检查星期几被翻译
        assert "mon" not in result or "星期" in result or "周一" in result

    def test_format_with_index(self):
        """测试带索引的格式化"""
        channel = "https://t.me/test_channel"
        schedule = {"frequency": "daily", "hour": 10, "minute": 0}

        result = format_schedule_info(channel, schedule, index=1)

        assert result.startswith("1.")
        assert "test_channel" in result

    def test_format_without_index(self):
        """测试不带索引的格式化"""
        channel = "https://t.me/test_channel"
        schedule = {"frequency": "daily", "hour": 10, "minute": 0}

        result = format_schedule_info(channel, schedule)

        # 不应该以数字加点开头
        assert not result[0].isdigit()

    def test_format_unknown_frequency(self):
        """测试未知频率格式化"""
        channel = "https://t.me/test_channel"
        schedule = {"frequency": "monthly", "hour": 10, "minute": 0}

        result = format_schedule_info(channel, schedule)

        assert "test_channel" in result
        assert "monthly" in result

    def test_format_with_missing_days(self):
        """测试缺少days字段的weekly调度"""
        channel = "https://t.me/test_channel"
        schedule = {
            "frequency": "weekly",
            "hour": 10,
            "minute": 0,
            # 缺少days字段
        }

        result = format_schedule_info(channel, schedule)

        assert "test_channel" in result
        assert "10:00" in result

    def test_format_channel_from_url(self):
        """测试从URL提取频道名"""
        channel = "https://t.me/some_long_channel_name"
        schedule = {"frequency": "daily", "hour": 8, "minute": 0}

        result = format_schedule_info(channel, schedule)

        assert "some_long_channel_name" in result

    def test_format_with_full_url(self):
        """测试完整URL格式化"""
        channel = "https://t.me/channel123"
        schedule = {"frequency": "weekly", "hour": 18, "minute": 30, "days": ["sun"]}

        result = format_schedule_info(channel, schedule, index=5)

        assert "5." in result
        assert "channel123" in result
        assert "18:30" in result


if __name__ == "__main__":
    pytest.main([__file__, "-v"])
