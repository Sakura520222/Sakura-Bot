# core/config/__init__.py
# New modular config system
# Backward compatibility: import everything from the old config.py
# The old config.py is a sibling file to this directory, so we need to import it directly
import importlib.util
import sys
from pathlib import Path

from core.config.event_bus import AsyncIOEventBus
from core.config.events import ConfigChangedEvent, ConfigValidationErrorEvent
from core.config.file_watcher import FileWatcher
from core.config.manager import ConfigManager
from core.config.telegram_notifier import ConfigErrorNotifier
from core.config.validator import ConfigValidator

# Load the old config.py file directly to avoid circular import
_old_config_path = Path(__file__).parent.parent / "config.py"
spec = importlib.util.spec_from_file_location("core._old_config", _old_config_path)
_old_config = importlib.util.module_from_spec(spec)
sys.modules["core._old_config"] = _old_config
spec.loader.exec_module(_old_config)

# Export everything from the old config module for backward compatibility
ADMIN_LIST = _old_config.ADMIN_LIST
API_HASH = _old_config.API_HASH
API_ID = _old_config.API_ID
BOT_STATE_PAUSED = _old_config.BOT_STATE_PAUSED
BOT_STATE_RUNNING = _old_config.BOT_STATE_RUNNING
BOT_STATE_SHUTTING_DOWN = _old_config.BOT_STATE_SHUTTING_DOWN
BOT_TOKEN = _old_config.BOT_TOKEN
CHANNELS = _old_config.CHANNELS
CHANNEL_POLL_SETTINGS = _old_config.CHANNEL_POLL_SETTINGS
CONFIG_FILE = _old_config.CONFIG_FILE
DEFAULT_LOG_LEVEL = _old_config.DEFAULT_LOG_LEVEL
DEFAULT_POLL_PROMPT = _old_config.DEFAULT_POLL_PROMPT
DEFAULT_PROMPT = _old_config.DEFAULT_PROMPT
DEFAULT_QA_PERSONA = _old_config.DEFAULT_QA_PERSONA
DEFAULT_SUMMARY_DAY = _old_config.DEFAULT_SUMMARY_DAY
DEFAULT_SUMMARY_HOUR = _old_config.DEFAULT_SUMMARY_HOUR
DEFAULT_SUMMARY_MINUTE = _old_config.DEFAULT_SUMMARY_MINUTE
ENABLE_POLL = _old_config.ENABLE_POLL
# ENABLE_VOTE_REGEN_REQUEST 已移除，现使用频道级别配置
ENABLE_VOTE_REGEN_REQUEST = getattr(_old_config, "ENABLE_VOTE_REGEN_REQUEST", True)
LANGUAGE_FROM_CONFIG = _old_config.LANGUAGE_FROM_CONFIG
LANGUAGE_FROM_ENV = _old_config.LANGUAGE_FROM_ENV
LAST_SUMMARY_FILE = _old_config.LAST_SUMMARY_FILE
LINKED_CHAT_CACHE = _old_config.LINKED_CHAT_CACHE
LLM_API_KEY = _old_config.LLM_API_KEY
LLM_BASE_URL = _old_config.LLM_BASE_URL
LLM_MODEL = _old_config.LLM_MODEL
LOG_LEVEL_FROM_ENV = _old_config.LOG_LEVEL_FROM_ENV
LOG_LEVEL_MAP = _old_config.LOG_LEVEL_MAP
POLL_PROMPT_FILE = _old_config.POLL_PROMPT_FILE
# POLL_REGEN_THRESHOLD 已移除，现使用频道级别配置
POLL_REGEN_THRESHOLD = getattr(_old_config, "POLL_REGEN_THRESHOLD", 5)
PROMPT_FILE = _old_config.PROMPT_FILE
QA_BOT_USERNAME = _old_config.QA_BOT_USERNAME
QA_PERSONA_FILE = _old_config.QA_PERSONA_FILE
RESTART_FLAG_FILE = _old_config.RESTART_FLAG_FILE
REPORT_ADMIN_IDS = _old_config.REPORT_ADMIN_IDS
SEND_REPORT_TO_SOURCE = _old_config.SEND_REPORT_TO_SOURCE
SUMMARY_SCHEDULES = _old_config.SUMMARY_SCHEDULES
TARGET_CHANNEL = _old_config.TARGET_CHANNEL
add_forwarding_rule = _old_config.add_forwarding_rule
add_poll_regeneration = _old_config.add_poll_regeneration
build_cron_trigger = _old_config.build_cron_trigger
cache_discussion_group_id = _old_config.cache_discussion_group_id
cleanup_old_regenerations = _old_config.cleanup_old_regenerations
clear_discussion_group_cache = _old_config.clear_discussion_group_cache
delete_channel_poll_config = _old_config.delete_channel_poll_config
delete_channel_schedule = _old_config.delete_channel_schedule
delete_poll_regeneration = _old_config.delete_poll_regeneration
get_bot_state = _old_config.get_bot_state
get_cached_discussion_group_id = _old_config.get_cached_discussion_group_id
get_channel_poll_config = _old_config.get_channel_poll_config
get_channel_schedule = _old_config.get_channel_schedule
get_discussion_group_id_cached = _old_config.get_discussion_group_id_cached
get_forwarding_config = _old_config.get_forwarding_config
get_forwarding_enabled_sources = _old_config.get_forwarding_enabled_sources
get_forwarding_rules = _old_config.get_forwarding_rules
get_forwarding_rules_by_source = _old_config.get_forwarding_rules_by_source
get_log_level = _old_config.get_log_level
get_poll_regeneration = _old_config.get_poll_regeneration
get_qa_bot_persona = _old_config.get_qa_bot_persona
get_scheduler_instance = _old_config.get_scheduler_instance
load_config = _old_config.load_config
load_poll_regenerations = _old_config.load_poll_regenerations
logger = _old_config.logger
normalize_channel_id = _old_config.normalize_channel_id
normalize_schedule_config = _old_config.normalize_schedule_config
remove_forwarding_rule = _old_config.remove_forwarding_rule
reset_vote_count = _old_config.reset_vote_count
save_config = _old_config.save_config
save_poll_regenerations = _old_config.save_poll_regenerations
set_bot_state = _old_config.set_bot_state
set_channel_poll_config = _old_config.set_channel_poll_config
set_channel_schedule = _old_config.set_channel_schedule
set_channel_schedule_v2 = _old_config.set_channel_schedule_v2
set_scheduler_instance = _old_config.set_scheduler_instance
set_shutdown_event = _old_config.set_shutdown_event
toggle_forwarding_rule = _old_config.toggle_forwarding_rule
trigger_shutdown = _old_config.trigger_shutdown
update_forwarding_rule = _old_config.update_forwarding_rule
update_module_variables = _old_config.update_module_variables
update_poll_regeneration = _old_config.update_poll_regeneration
validate_schedule = _old_config.validate_schedule
validate_schedule_v2 = _old_config.validate_schedule_v2
increment_vote_count = _old_config.increment_vote_count
get_vote_count = _old_config.get_vote_count

__all__ = [
    # New modular system exports
    "ConfigChangedEvent",
    "ConfigValidationErrorEvent",
    "AsyncIOEventBus",
    "ConfigValidator",
    "FileWatcher",
    "ConfigManager",
    "ConfigErrorNotifier",
    # Backward compatibility exports (from old config.py)
    "ADMIN_LIST",
    "API_HASH",
    "API_ID",
    "BOT_STATE_PAUSED",
    "BOT_STATE_RUNNING",
    "BOT_STATE_SHUTTING_DOWN",
    "BOT_TOKEN",
    "CHANNELS",
    "CHANNEL_POLL_SETTINGS",
    "CONFIG_FILE",
    "DEFAULT_LOG_LEVEL",
    "DEFAULT_POLL_PROMPT",
    "DEFAULT_PROMPT",
    "DEFAULT_QA_PERSONA",
    "DEFAULT_SUMMARY_DAY",
    "DEFAULT_SUMMARY_HOUR",
    "DEFAULT_SUMMARY_MINUTE",
    "ENABLE_POLL",
    # "ENABLE_VOTE_REGEN_REQUEST" - 已废弃，使用频道级别配置
    "LANGUAGE_FROM_CONFIG",
    "LANGUAGE_FROM_ENV",
    "LAST_SUMMARY_FILE",
    "LINKED_CHAT_CACHE",
    "LLM_API_KEY",
    "LLM_BASE_URL",
    "LLM_MODEL",
    "LOG_LEVEL_FROM_ENV",
    "LOG_LEVEL_MAP",
    "POLL_PROMPT_FILE",
    # "POLL_REGEN_THRESHOLD" - 已废弃，使用频道级别配置
    "PROMPT_FILE",
    "QA_BOT_USERNAME",
    "QA_PERSONA_FILE",
    "RESTART_FLAG_FILE",
    "REPORT_ADMIN_IDS",
    "SEND_REPORT_TO_SOURCE",
    "SUMMARY_SCHEDULES",
    "TARGET_CHANNEL",
    "add_forwarding_rule",
    "add_poll_regeneration",
    "build_cron_trigger",
    "cache_discussion_group_id",
    "cleanup_old_regenerations",
    "clear_discussion_group_cache",
    "delete_channel_poll_config",
    "delete_channel_schedule",
    "delete_poll_regeneration",
    "get_bot_state",
    "get_cached_discussion_group_id",
    "get_channel_poll_config",
    "get_channel_schedule",
    "get_discussion_group_id_cached",
    "get_forwarding_config",
    "get_forwarding_enabled_sources",
    "get_forwarding_rules",
    "get_forwarding_rules_by_source",
    "get_log_level",
    "get_poll_regeneration",
    "get_qa_bot_persona",
    "get_scheduler_instance",
    "get_vote_count",
    "increment_vote_count",
    "load_config",
    "load_poll_regenerations",
    "logger",
    "normalize_channel_id",
    "normalize_schedule_config",
    "remove_forwarding_rule",
    "reset_vote_count",
    "save_config",
    "save_poll_regenerations",
    "set_bot_state",
    "set_channel_poll_config",
    "set_channel_schedule",
    "set_channel_schedule_v2",
    "set_scheduler_instance",
    "set_shutdown_event",
    "toggle_forwarding_rule",
    "trigger_shutdown",
    "update_forwarding_rule",
    "update_module_variables",
    "update_poll_regeneration",
    "validate_schedule",
    "validate_schedule_v2",
]
