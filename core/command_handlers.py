"""
命令处理模块 - 重新导出所有子模块功能
此文件保持向后兼容，所有功能已拆分到 core/command_handlers/ 子目录中
"""

# 从子模块重新导出所有功能
from .command_handlers import (
    # 总结相关命令
    handle_manual_summary,
    handle_clear_summary_time,
    # 提示词管理命令
    handle_show_prompt,
    handle_set_prompt,
    handle_prompt_input,
    handle_show_poll_prompt,
    handle_set_poll_prompt,
    handle_poll_prompt_input,
    # AI配置命令
    handle_show_ai_config,
    handle_set_ai_config,
    handle_ai_config_input,
    # 频道管理命令
    handle_show_channels,
    handle_add_channel,
    handle_delete_channel,
    # 其他命令
    handle_show_log_level,
    handle_set_log_level,
    handle_restart,
    handle_shutdown,
    handle_pause,
    handle_resume,
    handle_show_channel_schedule,
    handle_set_channel_schedule,
    handle_delete_channel_schedule,
    handle_show_channel_poll,
    handle_set_channel_poll,
    handle_delete_channel_poll,
    handle_set_send_to_source,
    handle_clear_cache,
    handle_start,
    handle_help,
    handle_changelog,
)

__all__ = [
    # 总结相关命令
    'handle_manual_summary',
    'handle_clear_summary_time',
    # 提示词管理命令
    'handle_show_prompt',
    'handle_set_prompt',
    'handle_prompt_input',
    'handle_show_poll_prompt',
    'handle_set_poll_prompt',
    'handle_poll_prompt_input',
    # AI配置命令
    'handle_show_ai_config',
    'handle_set_ai_config',
    'handle_ai_config_input',
    # 频道管理命令
    'handle_show_channels',
    'handle_add_channel',
    'handle_delete_channel',
    # 其他命令
    'handle_show_log_level',
    'handle_set_log_level',
    'handle_restart',
    'handle_shutdown',
    'handle_pause',
    'handle_resume',
    'handle_show_channel_schedule',
    'handle_set_channel_schedule',
    'handle_delete_channel_schedule',
    'handle_show_channel_poll',
    'handle_set_channel_poll',
    'handle_delete_channel_poll',
    'handle_set_send_to_source',
    'handle_clear_cache',
    'handle_start',
    'handle_help',
    'handle_changelog',
]