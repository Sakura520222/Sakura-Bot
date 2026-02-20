"""
Telegram客户端功能模块
"""

from .client_management import (
    extract_date_range_from_summary,
    get_active_client,
    set_active_client,
)
from .messaging import fetch_last_week_messages, send_long_message, send_report
from .poll_handlers import (
    send_poll,
    send_poll_to_channel,
    send_poll_to_discussion_group,
)

__all__ = [
    "send_long_message",
    "send_report",
    "fetch_last_week_messages",
    "send_poll",
    "send_poll_to_channel",
    "send_poll_to_discussion_group",
    "set_active_client",
    "get_active_client",
    "extract_date_range_from_summary",
]
