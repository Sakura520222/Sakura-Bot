"""测试工具模块

Copyright 2026 Sakura-Bot
本项目采用 AGPL-3.0 许可
"""

from datetime import UTC

import pytest

from core.utils.date_utils import extract_date_range_from_summary
from core.utils.message_utils import format_schedule_info


@pytest.mark.unit
class TestDateUtils:
    """日期工具测试"""

    def test_extract_date_range_from_summary_weekly(self):
        """测试从周报总结中提取日期范围"""
        summary_text = "**测试周报 1.8-1.15**"
        start_time, end_time = extract_date_range_from_summary(summary_text)

        assert start_time is not None
        assert end_time is not None
        assert start_time.month == 1
        assert start_time.day == 8
        assert end_time.month == 1
        assert end_time.day == 15

    def test_extract_date_range_from_summary_weekly_with_spaces(self):
        """测试从带空格的周报中提取日期范围"""
        summary_text = "**测试周报 1.8 - 1.15**"
        start_time, end_time = extract_date_range_from_summary(summary_text)

        assert start_time is not None
        assert end_time is not None
        assert start_time.month == 1
        assert start_time.day == 8
        assert end_time.month == 1
        assert end_time.day == 15

    def test_extract_date_range_from_summary_daily(self):
        """测试从日报总结中提取日期范围"""
        summary_text = "**测试日报 2.23**"
        start_time, end_time = extract_date_range_from_summary(summary_text)

        assert start_time is not None
        assert end_time is not None
        assert start_time.month == 2
        assert start_time.day == 23
        assert end_time.month == 2
        assert end_time.day == 23
        # 结束时间应该是当天 23:59:59
        assert end_time.hour == 23
        assert end_time.minute == 59
        assert end_time.second == 59

    def test_extract_date_range_from_summary_no_match(self):
        """测试从没有日期的总结中提取日期范围"""
        summary_text = "这是一条没有日期格式的总结文本"
        start_time, end_time = extract_date_range_from_summary(summary_text)

        assert start_time is None
        assert end_time is None

    def test_extract_date_range_from_summary_year_boundary(self):
        """测试跨年日期范围"""
        summary_text = "**测试周报 12.25-1.5**"
        start_time, end_time = extract_date_range_from_summary(summary_text)

        assert start_time is not None
        assert end_time is not None
        # 开始时间在12月
        assert start_time.month == 12
        assert start_time.day == 25
        # 结束时间在次年1月
        assert end_time.month == 1
        assert end_time.day == 5

    def test_extract_date_range_from_summary_timezone(self):
        """测试提取的日期包含时区信息"""
        summary_text = "**测试日报 2.23**"
        start_time, end_time = extract_date_range_from_summary(summary_text)

        assert start_time is not None
        assert end_time is not None
        # 验证时区信息
        assert start_time.tzinfo == UTC
        assert end_time.tzinfo == UTC


@pytest.mark.unit
class TestMessageUtils:
    """消息工具测试"""

    def test_format_schedule_info_daily(self):
        """测试格式化每日调度信息"""
        channel = "https://t.me/test_channel"
        schedule = {"frequency": "daily", "hour": 9, "minute": 30}

        result = format_schedule_info(channel, schedule)
        assert "test_channel" in result
        assert "09:30" in result
        assert "\n" in result

    def test_format_schedule_info_weekly(self):
        """测试格式化每周调度信息"""
        channel = "https://t.me/test_channel"
        schedule = {"frequency": "weekly", "hour": 14, "minute": 0, "days": ["mon", "wed", "fri"]}

        result = format_schedule_info(channel, schedule)
        assert "test_channel" in result
        assert "14:00" in result
        assert "\n" in result

    def test_format_schedule_info_with_index(self):
        """测试带索引的格式化"""
        channel = "https://t.me/test_channel"
        schedule = {"frequency": "daily", "hour": 8, "minute": 0}

        result = format_schedule_info(channel, schedule, index=1)
        assert "1. " in result
        assert "test_channel" in result

    def test_format_schedule_info_unknown_frequency(self):
        """测试未知频率的格式化"""
        channel = "https://t.me/test_channel"
        schedule = {"frequency": "monthly", "hour": 10, "minute": 15}

        result = format_schedule_info(channel, schedule)
        assert "test_channel" in result
        assert "monthly" in result
        assert "10:15" in result

    def test_format_schedule_info_no_days_in_weekly(self):
        """测试每周调度但没有指定天数"""
        channel = "https://t.me/test_channel"
        schedule = {"frequency": "weekly", "hour": 12, "minute": 0, "days": []}

        result = format_schedule_info(channel, schedule)
        assert "test_channel" in result
        assert "12:00" in result

    def test_format_schedule_info_hour_minute_formatting(self):
        """测试小时和分钟的格式化（补零）"""
        channel = "https://t.me/test_channel"
        schedule = {"frequency": "daily", "hour": 5, "minute": 5}

        result = format_schedule_info(channel, schedule)
        assert "05:05" in result


if __name__ == "__main__":
    pytest.main([__file__, "-v"])
