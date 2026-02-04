"""
Telegram客户端模块 - 重新导出所有子模块功能
此文件保持向后兼容，所有功能已拆分到 core/telegram/ 子目录中
"""

# 从子模块重新导出所有功能
from .telegram import (
    # 消息发送功能
    send_long_message,
    send_report,
    fetch_last_week_messages,
    # 投票处理功能
    send_poll,
    send_poll_to_channel,
    send_poll_to_discussion_group,
    # 客户端管理功能
    set_active_client,
    get_active_client,
    extract_date_range_from_summary,
)

__all__ = [
    'send_long_message',
    'send_report',
    'fetch_last_week_messages',
    'send_poll',
    'send_poll_to_channel',
    'send_poll_to_discussion_group',
    'set_active_client',
    'get_active_client',
    'extract_date_range_from_summary',
]