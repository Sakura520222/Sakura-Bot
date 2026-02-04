"""
Telegram客户端管理模块
"""

import logging
from datetime import datetime, timezone

logger = logging.getLogger(__name__)


def extract_date_range_from_summary(summary_text):
    """
    从总结文本中提取日期范围

    Args:
        summary_text: 总结文本

    Returns:
        (start_time, end_time): 起始时间和结束时间的datetime对象，解析失败返回(None, None)
    """
    try:
        import re

        # 匹配周报日期范围: "**xxx周报 1.8-1.15**" 或 "**xxx周报 1.8 - 1.15**"
        weekly_pattern = r"\*\*.*?周报\s*(\d{1,2})\.(\d{1,2})\s*[-—~]\s*(\d{1,2})\.(\d{1,2})\*\*"
        weekly_match = re.search(weekly_pattern, summary_text)

        if weekly_match:
            start_month = int(weekly_match.group(1))
            start_day = int(weekly_match.group(2))
            end_month = int(weekly_match.group(3))
            end_day = int(weekly_match.group(4))

            current_year = datetime.now(timezone.utc).year

            start_time = datetime(current_year, start_month, start_day, tzinfo=timezone.utc)
            end_time = datetime(current_year, end_month, end_day, 23, 59, 59, tzinfo=timezone.utc)

            # 如果结束时间早于开始时间，说明跨年了
            if end_time < start_time:
                end_time = datetime(current_year + 1, end_month, end_day, 23, 59, 59, tzinfo=timezone.utc)

            return start_time, end_time

        # 匹配日报日期: "**xxx日报 1.15**"
        daily_pattern = r"\*\*.*?日报\s*(\d{1,2})\.(\d{1,2})\*\*"
        daily_match = re.search(daily_pattern, summary_text)

        if daily_match:
            month = int(daily_match.group(1))
            day = int(daily_match.group(2))
            current_year = datetime.now(timezone.utc).year

            start_time = datetime(current_year, month, day, tzinfo=timezone.utc)
            end_time = datetime(current_year, month, day, 23, 59, 59, tzinfo=timezone.utc)

            return start_time, end_time

        # 没有匹配到日期模式
        logger.debug("未能从总结文本中提取日期范围")
        return None, None

    except Exception as e:
        logger.warning(f"提取日期范围时出错: {e}")
        return None, None


# 全局变量，用于存储活动的Telegram客户端实例
_active_client = None


def set_active_client(client):
    """设置活动的Telegram客户端实例"""
    global _active_client
    _active_client = client
    logger.info("已设置活动的Telegram客户端实例")


def get_active_client():
    """获取活动的Telegram客户端实例"""
    return _active_client