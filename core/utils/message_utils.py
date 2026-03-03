"""
消息处理工具函数
"""

import logging

from ..i18n import get_text

logger = logging.getLogger(__name__)


def split_long_message(text: str, max_length: int = 4096) -> list:
    """将长消息分割为多个部分

    Args:
        text: 要分割的文本
        max_length: 每部分的最大长度（默认4096，Telegram消息限制）

    Returns:
        list: 分割后的文本列表
    """
    if len(text) <= max_length:
        return [text]

    parts = []
    current_part = ""
    lines = text.split("\n")

    for line in lines:
        # 如果单行就超过了最大长度，需要强制分割
        if len(line) > max_length:
            # 先保存当前部分
            if current_part:
                parts.append(current_part.rstrip())
                current_part = ""

            # 按字符强制分割超长行
            for i in range(0, len(line), max_length):
                chunk = line[i : i + max_length]
                parts.append(chunk)
        elif len(current_part) + len(line) + 1 <= max_length:
            current_part += line + "\n"
        else:
            # 当前部分已满，保存并开始新部分
            if current_part:
                parts.append(current_part.rstrip())
            current_part = line + "\n"

    # 添加最后一部分
    if current_part:
        parts.append(current_part.rstrip())

    return parts


def format_schedule_info(channel, schedule, index=None):
    """格式化调度配置信息

    Args:
        channel: 频道URL
        schedule: 标准化的调度配置字典
        index: 可选的索引编号

    Returns:
        str: 格式化的配置信息字符串
    """
    # 使用 i18n 获取星期映射
    weekday_map = {
        "mon": get_text("date.weekday.monday"),
        "tue": get_text("date.weekday.tuesday"),
        "wed": get_text("date.weekday.wednesday"),
        "thu": get_text("date.weekday.thursday"),
        "fri": get_text("date.weekday.friday"),
        "sat": get_text("date.weekday.saturday"),
        "sun": get_text("date.weekday.sunday"),
    }

    channel_name = channel.split("/")[-1]
    frequency = schedule.get("frequency", "weekly")
    hour = schedule["hour"]
    minute = schedule["minute"]

    if index is not None:
        prefix = f"{index}. "
    else:
        prefix = ""

    if frequency == "daily":
        daily_text = get_text("date.frequency.daily")
        return f"{prefix}{channel_name}: {daily_text} {hour:02d}:{minute:02d}\n"
    elif frequency == "weekly":
        weekly_text = get_text("date.frequency.weekly")
        days_localized = "、".join([weekday_map.get(d, d) for d in schedule.get("days", [])])
        return f"{prefix}{channel_name}: {weekly_text}{days_localized} {hour:02d}:{minute:02d}\n"
    else:
        # 未知频率，回退到显示原始值
        return f"{prefix}{channel_name}: {frequency} {hour:02d}:{minute:02d}\n"
