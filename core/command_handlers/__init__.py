"""
命令处理器聚合模块
保持向后兼容，所有导入无需修改
"""

from .summary_commands import (
    handle_manual_summary,
    handle_clear_summary_time
)
from .prompt_commands import (
    handle_show_prompt,
    handle_set_prompt,
    handle_prompt_input,
    handle_show_poll_prompt,
    handle_set_poll_prompt,
    handle_poll_prompt_input
)
from .ai_config_commands import (
    handle_show_ai_config,
    handle_set_ai_config,
    handle_ai_config_input
)
from .channel_commands import (
    handle_show_channels,
    handle_add_channel,
    handle_delete_channel
)
from .other_commands import (
    handle_show_channel_schedule,
    handle_set_channel_schedule,
    handle_delete_channel_schedule,
    handle_show_channel_poll,
    handle_set_channel_poll,
    handle_delete_channel_poll,
    handle_show_log_level,
    handle_set_log_level,
    handle_restart,
    handle_shutdown,
    handle_pause,
    handle_resume,
    handle_set_send_to_source,
    handle_clear_cache,
    handle_start,
    handle_help,
    handle_changelog
)

__all__ = [
    'handle_manual_summary',
    'handle_clear_summary_time',
    'handle_show_prompt',
    'handle_set_prompt',
    'handle_prompt_input',
    'handle_show_poll_prompt',
    'handle_set_poll_prompt',
    'handle_poll_prompt_input',
    'handle_show_ai_config',
    'handle_set_ai_config',
    'handle_ai_config_input',
    'handle_show_channels',
    'handle_add_channel',
    'handle_delete_channel',
    'handle_show_channel_schedule',
    'handle_set_channel_schedule',
    'handle_delete_channel_schedule',
    'handle_show_channel_poll',
    'handle_set_channel_poll',
    'handle_delete_channel_poll',
    'handle_show_log_level',
    'handle_set_log_level',
    'handle_restart',
    'handle_shutdown',
    'handle_pause',
    'handle_resume',
    'handle_set_send_to_source',
    'handle_clear_cache',
    'handle_start',
    'handle_help',
    'handle_changelog',
]