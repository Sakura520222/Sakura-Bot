"""
Telegram客户端功能模块
"""

from .messaging import send_long_message, send_report, fetch_last_week_messages
from .poll_handlers import send_poll, send_poll_to_channel, send_poll_to_discussion_group
from .client_management import set_active_client, get_active_client, extract_date_range_from_summary

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