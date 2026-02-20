"""
消息处理工具函数
"""

import logging

from ..i18n import get_text

logger = logging.getLogger(__name__)


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
        'mon': get_text('date.weekday.monday'),
        'tue': get_text('date.weekday.tuesday'),
        'wed': get_text('date.weekday.wednesday'),
        'thu': get_text('date.weekday.thursday'),
        'fri': get_text('date.weekday.friday'),
        'sat': get_text('date.weekday.saturday'),
        'sun': get_text('date.weekday.sunday'),
    }

    channel_name = channel.split('/')[-1]
    frequency = schedule.get('frequency', 'weekly')
    hour = schedule['hour']
    minute = schedule['minute']

    if index is not None:
        prefix = f"{index}. "
    else:
        prefix = ""

    if frequency == 'daily':
        daily_text = get_text('date.frequency.daily')
        return f"{prefix}{channel_name}: {daily_text} {hour:02d}:{minute:02d}\n"
    elif frequency == 'weekly':
        weekly_text = get_text('date.frequency.weekly')
        days_localized = '、'.join([weekday_map.get(d, d) for d in schedule.get('days', [])])
        return f"{prefix}{channel_name}: {weekly_text}{days_localized} {hour:02d}:{minute:02d}\n"
    else:
        # 未知频率，回退到显示原始值
        return f"{prefix}{channel_name}: {frequency} {hour:02d}:{minute:02d}\n"
