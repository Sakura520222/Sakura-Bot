"""
消息处理工具函数
"""

import logging

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
    day_map = {
        'mon': '周一', 'tue': '周二', 'wed': '周三', 'thu': '周四',
        'fri': '周五', 'sat': '周六', 'sun': '周日'
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
        return f"{prefix}{channel_name}: 每天 {hour:02d}:{minute:02d}\n"
    elif frequency == 'weekly':
        days_cn = '、'.join([day_map.get(d, d) for d in schedule.get('days', [])])
        return f"{prefix}{channel_name}: 每周{days_cn} {hour:02d}:{minute:02d}\n"
    else:
        return f"{prefix}{channel_name}: 未知频率 {frequency} {hour:02d}:{minute:02d}\n"