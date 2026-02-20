# Copyright 2026 Sakura-Bot
#
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可，
# 并附加非商业使用限制条款。
#
# - 署名：必须提供本项目的原始来源链接
# - 非商业：禁止任何商业用途和分发
# - 相同方式共享：衍生作品必须采用相同的许可证
#
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
# 许可证全文：参见 LICENSE 文件

import asyncio
import logging
import os

from dotenv import load_dotenv

# 配置日志
# 默认日志级别
DEFAULT_LOG_LEVEL = logging.DEBUG

# 日志级别映射
LOG_LEVEL_MAP = {
    'DEBUG': logging.DEBUG,
    'INFO': logging.INFO,
    'WARNING': logging.WARNING,
    'ERROR': logging.ERROR,
    'CRITICAL': logging.CRITICAL
}

# 日志级别转换函数
def get_log_level(level_str):
    """将字符串日志级别转换为logging模块对应的级别常量"""
    if not level_str:
        return DEFAULT_LOG_LEVEL
    return LOG_LEVEL_MAP.get(level_str.upper(), DEFAULT_LOG_LEVEL)

# 初始配置日志
logging.basicConfig(
    level=DEFAULT_LOG_LEVEL,
    format='%(asctime)s - %(name)s - %(levelname)s - %(message)s',
    datefmt='%Y-%m-%d %H:%M:%S'
)
logger = logging.getLogger(__name__)

# 配置文件路径
PROMPT_FILE = os.path.join("data", "prompt.txt")
POLL_PROMPT_FILE = os.path.join("data", "poll_prompt.txt")
QA_PERSONA_FILE = os.path.join("data", "qa_persona.txt")
CONFIG_FILE = os.path.join("data", "config.json")
RESTART_FLAG_FILE = os.path.join("data", ".restart_flag")
LAST_SUMMARY_FILE = os.path.join("data", ".last_summary_time.json")

# 讨论组ID缓存 (频道URL -> 讨论组ID)
# 避免频繁调用GetFullChannelRequest,提升性能
LINKED_CHAT_CACHE = {}

# 默认提示词
DEFAULT_PROMPT = "请总结以下 Telegram 消息，提取核心要点并列出重要消息的链接：\n\n"

# 默认投票生成提示词
DEFAULT_POLL_PROMPT = """根据以下内容生成一个有趣的单选投票。
1. **趣味性**：题目和选项要幽默、有梗，具有互动性，避免平铺直叙。
2. **双语要求**：整体内容中文在上，英文在下。在 JSON 字段内部，中文与英文之间使用 " / " 分隔。
3. **输出格式**：仅输出标准的 JSON 格式，严禁包含任何前言、解释或 Markdown 代码块标识符。
4. **JSON 结构**：
{{
  "question": "中文题目 / English Question",
  "options": [
    "中文选项1 / English Option 1",
    "中文选项2 / English Option 2",
    "中文选项3 / English Option 3",
    "中文选项4 / English Option 4"
  ]
}}

# Input Content
{summary_text}
"""

# 默认问答Bot人格提示词
DEFAULT_QA_PERSONA = """你是一个专业的智能资讯助手，擅长从历史记录中提取关键信息并回答用户问题。

【角色特点】
- [Friendly] 友好亲切，乐于助人
- [Concise] 回答简洁明了，直击要点
- [Expert] 专业可靠，基于事实回答

【你的任务】
基于频道历史总结为用户提供准确、有价值的信息，不要编造不存在的内容。

【回答风格】
- 使用清晰的结构和要点
- 语言简洁专业但不失亲和力
- 严格基于提供的历史总结回答
- 总结中没有相关信息时，明确说明"""

# 加载 .env 文件中的变量
# 显式指定 .env 文件路径（data/.env）
env_path = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "data", ".env")
load_dotenv(env_path)
logger.info(f"已加载 .env 文件中的环境变量 (路径: {env_path})")

# 从环境变量中读取配置
logger.info("开始从环境变量加载配置...")
# 语言配置 - 从环境变量获取默认值
LANGUAGE_FROM_ENV = os.getenv('LANGUAGE', 'zh-CN')
API_ID = os.getenv('TELEGRAM_API_ID')
API_HASH = os.getenv('TELEGRAM_API_HASH')
BOT_TOKEN = os.getenv('TELEGRAM_BOT_TOKEN')
# AI 配置 - 从环境变量获取默认值
LLM_API_KEY = os.getenv('LLM_API_KEY', os.getenv('DEEPSEEK_API_KEY'))
LLM_BASE_URL = os.getenv('LLM_BASE_URL', 'https://api.deepseek.com')
LLM_MODEL = os.getenv('LLM_MODEL', 'deepseek-chat')

# 频道配置 - 从环境变量获取默认值
TARGET_CHANNEL = os.getenv('TARGET_CHANNEL')
CHANNELS = []
if TARGET_CHANNEL:
    # 支持多个频道，用逗号分隔
    CHANNELS = [channel.strip() for channel in TARGET_CHANNEL.split(',')]
    logger.info(f"已从环境变量加载频道配置: {CHANNELS}")

# 是否将报告发送回源频道的配置，默认为True
SEND_REPORT_TO_SOURCE = True

# 是否启用投票功能，默认为True
ENABLE_POLL = True

# 投票重新生成请求配置
# 触发重新生成的投票数阈值，默认为5
POLL_REGEN_THRESHOLD = 5

# 是否启用投票重新生成请求功能，默认为True
ENABLE_VOTE_REGEN_REQUEST = True

# 投票重新生成数据文件锁，用于并发控制
_poll_regenerations_lock = asyncio.Lock()

# 日志级别 - 从环境变量获取默认值
LOG_LEVEL_FROM_ENV = os.getenv('LOG_LEVEL')
logger.debug(f"从环境变量读取的日志级别: {LOG_LEVEL_FROM_ENV}")

logger.debug(f"从环境变量加载的配置: API_ID={'***' if API_ID else '未设置'}, API_HASH={'***' if API_HASH else '未设置'}, BOT_TOKEN={'***' if BOT_TOKEN else '未设置'}")
logger.debug(f"AI配置 - 环境变量默认值: Base URL={LLM_BASE_URL}, Model={LLM_MODEL}")
logger.debug(f"目标频道列表: {CHANNELS}")

# 管理员 ID 列表，从环境变量读取后转为整数列表
REPORT_ADMIN_IDS = os.getenv('REPORT_ADMIN_IDS', '')
logger.debug(f"从环境变量读取的管理员ID: {REPORT_ADMIN_IDS}")

# 处理管理员ID列表
ADMIN_LIST = []
if REPORT_ADMIN_IDS:
    # 支持多个管理员ID，用逗号分隔
    ADMIN_LIST = [int(admin_id.strip()) for admin_id in REPORT_ADMIN_IDS.split(',')]
    logger.info(f"已从环境变量加载管理员ID列表: {ADMIN_LIST}")
else:
    # 如果没有配置管理员ID，默认发送给自己
    ADMIN_LIST = ['me']
    logger.info("未配置管理员ID，默认发送给机器人所有者")

# 读取配置文件
def load_config():
    """从配置文件读取AI配置"""
    import json
    logger.info(f"开始读取配置文件: {CONFIG_FILE}")
    try:
        with open(CONFIG_FILE, "r", encoding="utf-8") as f:
            config = json.load(f)
            logger.info(f"成功读取配置文件，配置项数量: {len(config)}")
            return config
    except FileNotFoundError:
        logger.warning(f"配置文件 {CONFIG_FILE} 不存在，返回空配置")
        return {}
    except json.JSONDecodeError as e:
        logger.error(f"配置文件 {CONFIG_FILE} 格式错误: {e}", exc_info=True)
        return {}
    except Exception as e:
        logger.error(f"读取配置文件 {CONFIG_FILE} 时出错: {type(e).__name__}: {e}", exc_info=True)
        return {}

# 保存配置文件
def save_config(config):
    """保存AI配置到文件"""
    import json
    logger.info(f"开始保存配置到文件: {CONFIG_FILE}")
    try:
        with open(CONFIG_FILE, "w", encoding="utf-8") as f:
            json.dump(config, f, ensure_ascii=False, indent=2)
        logger.info(f"成功保存配置到文件，配置项数量: {len(config)}")

        # 更新模块变量以保持一致性
        update_module_variables(config)

    except Exception as e:
        logger.error(f"保存配置到文件 {CONFIG_FILE} 时出错: {type(e).__name__}: {e}", exc_info=True)

def update_module_variables(config):
    """更新模块变量以匹配配置文件"""
    global LLM_API_KEY, LLM_BASE_URL, LLM_MODEL, CHANNELS, SEND_REPORT_TO_SOURCE, SUMMARY_SCHEDULES, CHANNEL_POLL_SETTINGS, POLL_REGEN_THRESHOLD, ENABLE_VOTE_REGEN_REQUEST

    # 更新AI配置
    LLM_API_KEY = config.get('api_key', LLM_API_KEY)
    LLM_BASE_URL = config.get('base_url', LLM_BASE_URL)
    LLM_MODEL = config.get('model', LLM_MODEL)

    # 更新频道列表
    config_channels = config.get('channels')
    if config_channels and isinstance(config_channels, list):
        CHANNELS = config_channels
        logger.info(f"已更新内存中的频道列表: {CHANNELS}")

    # 更新是否将报告发送回源频道的配置
    if 'send_report_to_source' in config:
        SEND_REPORT_TO_SOURCE = config['send_report_to_source']
        logger.info(f"已更新内存中的发送报告到源频道的配置: {SEND_REPORT_TO_SOURCE}")

    # 更新是否启用投票功能的配置
    if 'enable_poll' in config:
        ENABLE_POLL = config['enable_poll']
        logger.info(f"已更新内存中的投票功能配置: {ENABLE_POLL}")

    # 更新频道级时间配置
    summary_schedules_config = config.get('summary_schedules', {})
    if isinstance(summary_schedules_config, dict):
        SUMMARY_SCHEDULES = summary_schedules_config
        logger.info(f"已更新内存中的频道级时间配置: {len(SUMMARY_SCHEDULES)} 个频道")

    # 更新频道级投票配置
    channel_poll_config = config.get('channel_poll_settings', {})
    if isinstance(channel_poll_config, dict):
        CHANNEL_POLL_SETTINGS = channel_poll_config
        logger.info(f"已更新内存中的频道级投票配置: {len(CHANNEL_POLL_SETTINGS)} 个频道")

    # 更新投票重新生成请求配置
    if 'poll_regen_threshold' in config:
        POLL_REGEN_THRESHOLD = config['poll_regen_threshold']
        logger.info(f"已更新内存中的投票重新生成阈值: {POLL_REGEN_THRESHOLD}")

    if 'enable_vote_regen_request' in config:
        ENABLE_VOTE_REGEN_REQUEST = config['enable_vote_regen_request']
        logger.info(f"已更新内存中的投票重新生成请求功能配置: {ENABLE_VOTE_REGEN_REQUEST}")

# 加载配置文件，覆盖环境变量默认值
logger.info("开始加载配置文件...")
config = load_config()

# 语言配置初始化
# 优先级：config.json > .env > 默认值(zh-CN)
LANGUAGE_FROM_CONFIG = None
if config:
    LANGUAGE_FROM_CONFIG = config.get('language')
    logger.debug(f"从配置文件读取的语言配置: {LANGUAGE_FROM_CONFIG}")

# 确定最终语言配置
final_language = LANGUAGE_FROM_CONFIG or LANGUAGE_FROM_ENV
logger.info(f"最终语言配置: {final_language} (来源: {'配置文件' if LANGUAGE_FROM_CONFIG else '环境变量或默认值'})")

# 初始化国际化模块的语言设置
from . import i18n

i18n.set_language(final_language)
logger.info(f"国际化模块语言已设置为: {i18n.get_language()}")

if config:
    logger.debug(f"从配置文件读取的配置: {config}")
    # 只有当配置文件中存在且不为None时才覆盖环境变量的值
    if 'api_key' in config and config['api_key']:
        LLM_API_KEY = config['api_key']
        logger.debug("从配置文件覆盖 LLM_API_KEY")
    else:
        logger.debug("使用环境变量中的 LLM_API_KEY")

    if 'base_url' in config and config['base_url']:
        LLM_BASE_URL = config['base_url']
        logger.debug("从配置文件覆盖 LLM_BASE_URL")
    else:
        logger.debug("使用环境变量中的 LLM_BASE_URL")

    if 'model' in config and config['model']:
        LLM_MODEL = config['model']
        logger.debug("从配置文件覆盖 LLM_MODEL")
    else:
        logger.debug("使用环境变量中的 LLM_MODEL")

    # 从配置文件读取频道列表
    config_channels = config.get('channels')
    if config_channels and isinstance(config_channels, list):
        CHANNELS = config_channels
        logger.info(f"已从配置文件加载频道列表: {CHANNELS}")

    # 从配置文件读取是否将报告发送回源频道的配置
    SEND_REPORT_TO_SOURCE = config.get('send_report_to_source', SEND_REPORT_TO_SOURCE)
    logger.info(f"已从配置文件加载发送报告到源频道的配置: {SEND_REPORT_TO_SOURCE}")

    # 从配置文件读取是否启用投票功能的配置
    ENABLE_POLL = config.get('enable_poll', ENABLE_POLL)
    logger.info(f"已从配置文件加载投票功能配置: {ENABLE_POLL}")

    # 从配置文件读取投票重新生成请求配置
    POLL_REGEN_THRESHOLD = config.get('poll_regen_threshold', POLL_REGEN_THRESHOLD)
    logger.info(f"已从配置文件加载投票重新生成阈值: {POLL_REGEN_THRESHOLD}")

    ENABLE_VOTE_REGEN_REQUEST = config.get('enable_vote_regen_request', ENABLE_VOTE_REGEN_REQUEST)
    logger.info(f"已从配置文件加载投票重新生成请求功能配置: {ENABLE_VOTE_REGEN_REQUEST}")

    # 从配置文件读取日志级别
    LOG_LEVEL_FROM_CONFIG = config.get('log_level')
    logger.info("已使用配置文件覆盖AI配置默认值")
else:
    logger.info("未找到配置文件或配置文件为空，使用默认配置")
    LOG_LEVEL_FROM_CONFIG = None

# 确定最终日志级别（配置文件优先于环境变量）
final_log_level_str = LOG_LEVEL_FROM_CONFIG or LOG_LEVEL_FROM_ENV
final_log_level = get_log_level(final_log_level_str)

# 获取根日志记录器并设置级别
root_logger = logging.getLogger()
current_level = root_logger.getEffectiveLevel()
if current_level != final_log_level:
    root_logger.setLevel(final_log_level)
    logger.info(f"日志级别已从 {logging.getLevelName(current_level)} 更改为 {logging.getLevelName(final_log_level)}")
else:
    logger.info(f"当前日志级别: {logging.getLevelName(current_level)}")

# 机器人状态管理
BOT_STATE_RUNNING = "running"
BOT_STATE_PAUSED = "paused"
BOT_STATE_SHUTTING_DOWN = "shutting_down"

# 全局状态变量
_bot_state = BOT_STATE_RUNNING
_scheduler_instance = None

def get_bot_state():
    """获取当前机器人状态"""
    return _bot_state

def set_bot_state(state):
    """设置机器人状态"""
    global _bot_state
    valid_states = [BOT_STATE_RUNNING, BOT_STATE_PAUSED, BOT_STATE_SHUTTING_DOWN]
    if state in valid_states:
        _bot_state = state
        logger.info(f"机器人状态已更新为: {state}")
        return True
    else:
        logger.error(f"无效的机器人状态: {state}")
        return False

def set_scheduler_instance(scheduler):
    """设置调度器实例，供其他模块访问"""
    global _scheduler_instance
    _scheduler_instance = scheduler
    logger.info("调度器实例已设置")

def get_scheduler_instance():
    """获取调度器实例"""
    return _scheduler_instance

# 自动总结时间配置
# 默认时间：每周一早上9点
DEFAULT_SUMMARY_DAY = 'mon'  # 星期几：mon, tue, wed, thu, fri, sat, sun
DEFAULT_SUMMARY_HOUR = 9     # 小时：0-23
DEFAULT_SUMMARY_MINUTE = 0   # 分钟：0-59

# 从配置文件读取频道级时间配置
SUMMARY_SCHEDULES = {}
if config:
    summary_schedules_config = config.get('summary_schedules', {})
    if isinstance(summary_schedules_config, dict):
        SUMMARY_SCHEDULES = summary_schedules_config
        logger.info(f"已从配置文件加载频道级时间配置: {len(SUMMARY_SCHEDULES)} 个频道")
    else:
        logger.warning("配置文件中的summary_schedules格式不正确，使用默认配置")

# 频道级投票配置
CHANNEL_POLL_SETTINGS = {}
if config:
    channel_poll_config = config.get('channel_poll_settings', {})
    if isinstance(channel_poll_config, dict):
        CHANNEL_POLL_SETTINGS = channel_poll_config
        logger.info(f"已从配置文件加载频道级投票配置: {len(CHANNEL_POLL_SETTINGS)} 个频道")
    else:
        logger.warning("配置文件中的channel_poll_settings格式不正确，使用默认配置")

# 获取频道的时间配置
def get_channel_schedule(channel):
    """获取指定频道的自动总结时间配置（支持新格式）

    Args:
        channel: 频道URL

    Returns:
        dict: 标准化的配置字典，包含 frequency, days, hour, minute
    """
    if channel in SUMMARY_SCHEDULES:
        schedule = SUMMARY_SCHEDULES[channel]
        # 标准化配置（处理向后兼容）
        return normalize_schedule_config(schedule)
    else:
        # 返回默认配置
        return {
            'frequency': 'weekly',
            'days': [DEFAULT_SUMMARY_DAY],
            'hour': DEFAULT_SUMMARY_HOUR,
            'minute': DEFAULT_SUMMARY_MINUTE
        }

# 设置频道的时间配置
def set_channel_schedule(channel, day=None, hour=None, minute=None):
    """设置指定频道的自动总结时间配置（旧格式，保持向后兼容）

    此函数保留以支持旧代码，内部调用 set_channel_schedule_v2

    Args:
        channel: 频道URL
        day: 星期几（mon, tue, wed, thu, fri, sat, sun）
        hour: 小时（0-23）
        minute: 分钟（0-59）

    Returns:
        bool: 是否成功保存配置
    """
    if day is not None:
        # 旧格式，转换为 weekly 模式
        return set_channel_schedule_v2(channel, 'weekly', days=[day], hour=hour, minute=minute)
    else:
        # 仅更新时间，保持默认星期
        return set_channel_schedule_v2(channel, 'weekly', days=[DEFAULT_SUMMARY_DAY], hour=hour, minute=minute)

# 删除频道的时间配置
def delete_channel_schedule(channel):
    """删除指定频道的自动总结时间配置

    Args:
        channel: 频道URL

    Returns:
        bool: 是否成功删除配置
    """
    try:
        # 加载当前配置
        current_config = load_config()

        # 检查是否存在配置
        if 'summary_schedules' in current_config and channel in current_config['summary_schedules']:
            # 删除频道配置
            del current_config['summary_schedules'][channel]

            # 保存配置（save_config会自动更新模块变量）
            save_config(current_config)

            logger.info(f"已删除频道 {channel} 的时间配置")
            return True
        else:
            logger.info(f"频道 {channel} 没有时间配置，无需删除")
            return True
    except Exception as e:
        logger.error(f"删除频道时间配置时出错: {type(e).__name__}: {e}", exc_info=True)
        return False

# 验证时间配置
def validate_schedule(day, hour, minute):
    """验证时间配置是否有效

    Args:
        day: 星期几
        hour: 小时
        minute: 分钟

    Returns:
        tuple: (是否有效, 错误信息)
    """
    valid_days = ['mon', 'tue', 'wed', 'thu', 'fri', 'sat', 'sun']

    if day not in valid_days:
        return False, f"无效的星期几: {day}，有效值: {', '.join(valid_days)}"

    if not isinstance(hour, int) or hour < 0 or hour > 23:
        return False, f"无效的小时: {hour}，有效范围: 0-23"

    if not isinstance(minute, int) or minute < 0 or minute > 59:
        return False, f"无效的分钟: {minute}，有效范围: 0-59"

    return True, "配置有效"

# ==================== 多频率模式支持函数 ====================

def normalize_schedule_config(schedule_dict):
    """将配置标准化为新格式，处理向后兼容

    Args:
        schedule_dict: 原始配置字典

    Returns:
        dict: 标准化后的配置，包含 frequency, days, hour, minute
    """
    # 如果包含 frequency 字段，已经是新格式
    if 'frequency' in schedule_dict:
        # 确保 days 字段存在（weekly 模式）
        if schedule_dict['frequency'] == 'weekly' and 'days' not in schedule_dict:
            # 如果没有 days 字段但有 day 字段，转换它
            if 'day' in schedule_dict:
                schedule_dict['days'] = [schedule_dict['day']]
            else:
                schedule_dict['days'] = [DEFAULT_SUMMARY_DAY]
        return schedule_dict

    # 向后兼容：旧格式 (day 字段)
    if 'day' in schedule_dict:
        return {
            'frequency': 'weekly',
            'days': [schedule_dict['day']],
            'hour': schedule_dict.get('hour', DEFAULT_SUMMARY_HOUR),
            'minute': schedule_dict.get('minute', DEFAULT_SUMMARY_MINUTE)
        }

    # 默认配置
    return {
        'frequency': 'weekly',
        'days': [DEFAULT_SUMMARY_DAY],
        'hour': schedule_dict.get('hour', DEFAULT_SUMMARY_HOUR),
        'minute': schedule_dict.get('minute', DEFAULT_SUMMARY_MINUTE)
    }


def validate_schedule_v2(config_dict):
    """验证新的时间配置格式

    Args:
        config_dict: 包含 frequency, days, hour, minute 的字典

    Returns:
        tuple: (是否有效, 错误信息)
    """
    valid_days = ['mon', 'tue', 'wed', 'thu', 'fri', 'sat', 'sun']
    valid_frequencies = ['daily', 'weekly']

    # 验证 frequency
    frequency = config_dict.get('frequency')
    if frequency not in valid_frequencies:
        return False, f"无效的频率: {frequency}，有效值: {', '.join(valid_frequencies)}"

    # 验证 days（仅 weekly 模式需要）
    if frequency == 'weekly':
        days = config_dict.get('days', [])
        if not isinstance(days, list) or not days:
            return False, "weekly 模式必须提供 days 字段（非空数组）"

        for day in days:
            if day not in valid_days:
                return False, f"无效的星期几: {day}，有效值: {', '.join(valid_days)}"

    # 验证时间
    hour = config_dict.get('hour')
    minute = config_dict.get('minute')

    if not isinstance(hour, int) or hour < 0 or hour > 23:
        return False, f"无效的小时: {hour}，有效范围: 0-23"

    if not isinstance(minute, int) or minute < 0 or minute > 59:
        return False, f"无效的分钟: {minute}，有效范围: 0-59"

    return True, "配置有效"


def set_channel_schedule_v2(channel, frequency, days=None, hour=None, minute=None):
    """设置指定频道的自动总结时间配置（支持新格式）

    Args:
        channel: 频道URL
        frequency: 频率类型（'daily' 或 'weekly'）
        days: 星期几列表（weekly 模式必需）
        hour: 小时（0-23）
        minute: 分钟（0-59）

    Returns:
        bool: 是否成功保存配置
    """
    try:
        # 构建配置字典
        config_dict = {
            'frequency': frequency,
            'hour': hour if hour is not None else DEFAULT_SUMMARY_HOUR,
            'minute': minute if minute is not None else DEFAULT_SUMMARY_MINUTE
        }

        # weekly 模式需要 days 字段
        if frequency == 'weekly':
            if days is None:
                days = [DEFAULT_SUMMARY_DAY]
            config_dict['days'] = days

        # 验证配置
        is_valid, error_msg = validate_schedule_v2(config_dict)
        if not is_valid:
            logger.error(f"配置验证失败: {error_msg}")
            return False

        # 加载当前配置
        current_config = load_config()

        # 确保summary_schedules字段存在
        if 'summary_schedules' not in current_config:
            current_config['summary_schedules'] = {}

        # 更新配置
        current_config['summary_schedules'][channel] = config_dict

        # 保存配置
        save_config(current_config)

        logger.info(f"已更新频道 {channel} 的时间配置: {config_dict}")
        return True
    except Exception as e:
        logger.error(f"设置频道时间配置时出错: {type(e).__name__}: {e}", exc_info=True)
        return False


def build_cron_trigger(schedule_config):
    """根据配置构建 APScheduler cron 触发器参数

    Args:
        schedule_config: 标准化的调度配置字典

    Returns:
        dict: 包含 cron 触发器参数的字典
    """
    frequency = schedule_config.get('frequency', 'weekly')

    if frequency == 'daily':
        # 每天模式
        return {
            'day_of_week': '*',  # 每天
            'hour': schedule_config['hour'],
            'minute': schedule_config['minute']
        }
    elif frequency == 'weekly':
        # 每周模式（支持多天）
        days_str = ','.join(schedule_config['days'])
        return {
            'day_of_week': days_str,
            'hour': schedule_config['hour'],
            'minute': schedule_config['minute']
        }
    else:
        # 默认：每周一
        return {
            'day_of_week': 'mon',
            'hour': schedule_config.get('hour', DEFAULT_SUMMARY_HOUR),
            'minute': schedule_config.get('minute', DEFAULT_SUMMARY_MINUTE)
        }


# ==================== 频道级投票配置管理函数 ====================

def get_channel_poll_config(channel):
    """获取指定频道的投票配置

    Args:
        channel: 频道URL

    Returns:
        dict: 包含 enabled 和 send_to_channel 的配置字典
            - enabled: 是否启用投票（None 表示使用全局配置）
            - send_to_channel: true=频道模式, false=讨论组模式
    """
    if channel in CHANNEL_POLL_SETTINGS:
        config = CHANNEL_POLL_SETTINGS[channel]
        return {
            'enabled': config.get('enabled', None),  # None 表示使用全局配置
            'send_to_channel': config.get('send_to_channel', False)  # 默认讨论组模式
        }
    else:
        # 没有独立配置，返回默认配置
        return {
            'enabled': None,  # 使用全局 ENABLE_POLL
            'send_to_channel': False  # 默认讨论组模式
        }


def set_channel_poll_config(channel, enabled=None, send_to_channel=None):
    """设置指定频道的投票配置

    Args:
        channel: 频道URL
        enabled: 是否启用投票（None 表示不修改）
        send_to_channel: 投票发送位置（None 表示不修改）
            True - 频道模式（直接发送到频道）
            False - 讨论组模式（发送到讨论组）

    Returns:
        bool: 是否成功保存配置
    """
    try:
        # 加载当前配置
        current_config = load_config()

        # 确保channel_poll_settings字段存在
        if 'channel_poll_settings' not in current_config:
            current_config['channel_poll_settings'] = {}

        # 获取频道当前配置
        if channel not in current_config['channel_poll_settings']:
            current_config['channel_poll_settings'][channel] = {}

        channel_config = current_config['channel_poll_settings'][channel]

        # 更新配置（只更新提供的参数）
        if enabled is not None:
            channel_config['enabled'] = enabled
            logger.info(f"设置频道 {channel} 的投票启用状态: {enabled}")

        if send_to_channel is not None:
            channel_config['send_to_channel'] = send_to_channel
            logger.info(f"设置频道 {channel} 的投票发送位置: {'频道' if send_to_channel else '讨论组'}")

        # 保存配置
        save_config(current_config)

        logger.info(f"已更新频道 {channel} 的投票配置")
        return True
    except Exception as e:
        logger.error(f"设置频道投票配置时出错: {type(e).__name__}: {e}", exc_info=True)
        return False


def delete_channel_poll_config(channel):
    """删除指定频道的投票配置

    删除后，该频道将使用全局投票配置

    Args:
        channel: 频道URL

    Returns:
        bool: 是否成功删除配置
    """
    try:
        # 加载当前配置
        current_config = load_config()

        # 检查是否存在配置
        if 'channel_poll_settings' in current_config and channel in current_config['channel_poll_settings']:
            # 删除频道配置
            del current_config['channel_poll_settings'][channel]

            # 保存配置
            save_config(current_config)

            logger.info(f"已删除频道 {channel} 的投票配置，将使用全局配置")
            return True
        else:
            logger.info(f"频道 {channel} 没有独立的投票配置，无需删除")
            return True
    except Exception as e:
        logger.error(f"删除频道投票配置时出错: {type(e).__name__}: {e}", exc_info=True)
        return False


# 投票重新生成数据存储
POLL_REGENERATIONS_FILE = os.path.join("data", ".poll_regenerations.json")


def load_poll_regenerations():
    """加载投票重新生成数据

    Returns:
        dict: 投票重新生成数据字典
    """
    import json
    if os.path.exists(POLL_REGENERATIONS_FILE):
        try:
            with open(POLL_REGENERATIONS_FILE, "r", encoding="utf-8") as f:
                return json.load(f)
        except Exception as e:
            logger.error(f"加载投票重新生成数据失败: {e}")
            return {}
    return {}


def save_poll_regenerations(data):
    """保存投票重新生成数据

    Args:
        data: 要保存的数据字典
    """
    import json
    try:
        with open(POLL_REGENERATIONS_FILE, "w", encoding="utf-8") as f:
            json.dump(data, f, ensure_ascii=False, indent=2)
    except Exception as e:
        logger.error(f"保存投票重新生成数据失败: {e}")


def add_poll_regeneration(channel, summary_msg_id, poll_msg_id,
                         button_msg_id, summary_text, channel_name, send_to_channel,
                         discussion_forward_msg_id=None):
    """添加一条投票重新生成记录

    Args:
        channel: 频道URL
        summary_msg_id: 总结消息ID
        poll_msg_id: 投票消息ID
        button_msg_id: 按钮消息ID
        summary_text: 总结文本
        channel_name: 频道名称
        send_to_channel: 是否发送到频道(True=频道, False=讨论组)
        discussion_forward_msg_id: 讨论组中的转发消息ID(仅讨论组模式需要)
    """
    from datetime import datetime
    data = load_poll_regenerations()
    if channel not in data:
        data[channel] = {}
    record = {
        "poll_message_id": poll_msg_id,
        "button_message_id": button_msg_id,
        "summary_text": summary_text,
        "channel_name": channel_name,
        "timestamp": datetime.now().isoformat(),
        "send_to_channel": send_to_channel,
        "vote_count": 0,  # 初始化投票计数
        "voters": []  # 初始化已投票用户列表
    }
    # 如果是讨论组模式，保存转发消息ID
    if not send_to_channel and discussion_forward_msg_id is not None:
        record["discussion_forward_msg_id"] = discussion_forward_msg_id
    data[channel][str(summary_msg_id)] = record
    save_poll_regenerations(data)
    logger.info(f"已添加投票重新生成记录: channel={channel}, summary_id={summary_msg_id}")


def get_poll_regeneration(channel, summary_msg_id):
    """获取指定的投票重新生成记录

    Args:
        channel: 频道URL
        summary_msg_id: 总结消息ID

    Returns:
        dict: 投票重新生成记录,如果不存在返回None
    """
    data = load_poll_regenerations()
    return data.get(channel, {}).get(str(summary_msg_id))


def update_poll_regeneration(channel, summary_msg_id, poll_msg_id, button_msg_id):
    """更新投票重新生成记录的消息ID

    Args:
        channel: 频道URL
        summary_msg_id: 总结消息ID
        poll_msg_id: 新的投票消息ID
        button_msg_id: 新的按钮消息ID
    """
    data = load_poll_regenerations()
    if channel in data and str(summary_msg_id) in data[channel]:
        data[channel][str(summary_msg_id)]["poll_message_id"] = poll_msg_id
        data[channel][str(summary_msg_id)]["button_message_id"] = button_msg_id
        save_poll_regenerations(data)
        logger.info(f"已更新投票重新生成记录: channel={channel}, summary_id={summary_msg_id}")


def delete_poll_regeneration(channel, summary_msg_id):
    """删除指定的投票重新生成记录

    Args:
        channel: 频道URL
        summary_msg_id: 总结消息ID
    """
    data = load_poll_regenerations()
    if channel in data and str(summary_msg_id) in data[channel]:
        del data[channel][str(summary_msg_id)]
        save_poll_regenerations(data)
        logger.info(f"已删除投票重新生成记录: channel={channel}, summary_id={summary_msg_id}")


def cleanup_old_regenerations(days=30):
    """清理超过指定天数的旧记录

    Args:
        days: 保留天数,默认30天

    Returns:
        int: 清理的记录数量
    """
    from datetime import datetime, timedelta
    data = load_poll_regenerations()
    cutoff_time = datetime.now() - timedelta(days=days)
    count = 0
    for channel in list(data.keys()):
        for summary_id in list(data[channel].keys()):
            record = data[channel][summary_id]
            try:
                record_time = datetime.fromisoformat(record["timestamp"])
                if record_time < cutoff_time:
                    del data[channel][summary_id]
                    count += 1
            except Exception:
                pass
    save_poll_regenerations(data)
    if count > 0:
        logger.info(f"已清理 {count} 条超过 {days} 天的投票重新生成记录")
    return count


async def increment_vote_count(channel, summary_msg_id, user_id):
    """增加投票计数

    Args:
        channel: 频道URL
        summary_msg_id: 总结消息ID
        user_id: 用户ID

    Returns:
        tuple: (是否成功增加, 当前计数, 是否已投票)
    """
    global _poll_regenerations_lock

    async with _poll_regenerations_lock:
        data = load_poll_regenerations()

        # 检查记录是否存在
        if channel not in data or str(summary_msg_id) not in data[channel]:
            logger.warning(f"投票重新生成记录不存在: channel={channel}, summary_id={summary_msg_id}")
            return False, 0, False

        record = data[channel][str(summary_msg_id)]

        # 检查用户是否已投票
        if user_id in record.get('voters', []):
            logger.info(f"用户 {user_id} 已经投票过了")
            return False, record.get('vote_count', 0), True

        # 增加投票计数
        record['vote_count'] = record.get('vote_count', 0) + 1
        record.setdefault('voters', []).append(user_id)

        # 保存数据
        save_poll_regenerations(data)

        logger.info(f"投票计数已更新: channel={channel}, summary_id={summary_msg_id}, count={record['vote_count']}, user_id={user_id}")
        return True, record['vote_count'], False


def reset_vote_count(channel, summary_msg_id):
    """重置投票计数

    Args:
        channel: 频道URL
        summary_msg_id: 总结消息ID

    Returns:
        bool: 是否成功重置
    """
    global _poll_regenerations_lock

    data = load_poll_regenerations()

    # 检查记录是否存在
    if channel not in data or str(summary_msg_id) not in data[channel]:
        logger.warning(f"投票重新生成记录不存在: channel={channel}, summary_id={summary_msg_id}")
        return False

    # 重置计数和投票者列表
    data[channel][str(summary_msg_id)]['vote_count'] = 0
    data[channel][str(summary_msg_id)]['voters'] = []

    # 保存数据
    save_poll_regenerations(data)

    logger.info(f"投票计数已重置: channel={channel}, summary_id={summary_msg_id}")
    return True


def get_vote_count(channel, summary_msg_id):
    """获取投票计数

    Args:
        channel: 频道URL
        summary_msg_id: 总结消息ID

    Returns:
        int: 投票计数，如果记录不存在返回0
    """
    data = load_poll_regenerations()

    if channel not in data or str(summary_msg_id) not in data[channel]:
        return 0

    return data[channel][str(summary_msg_id)].get('vote_count', 0)


# ==================== 讨论组ID缓存管理 ====================

def get_cached_discussion_group_id(channel_url):
    """获取缓存的讨论组ID

    Args:
        channel_url: 频道URL

    Returns:
        int: 讨论组ID,如果不存在则返回None
    """
    return LINKED_CHAT_CACHE.get(channel_url)


def cache_discussion_group_id(channel_url, discussion_group_id):
    """缓存讨论组ID

    Args:
        channel_url: 频道URL
        discussion_group_id: 讨论组ID (已转换为超级群组格式)
    """
    LINKED_CHAT_CACHE[channel_url] = discussion_group_id
    logger.debug(f"已缓存讨论组ID: {channel_url} -> {discussion_group_id}")


def clear_discussion_group_cache(channel_url=None):
    """清除讨论组ID缓存

    Args:
        channel_url: 可选,指定要清除的频道URL。如果为None则清除所有缓存
    """
    if channel_url:
        if channel_url in LINKED_CHAT_CACHE:
            del LINKED_CHAT_CACHE[channel_url]
            logger.info(f"已清除频道 {channel_url} 的讨论组ID缓存")
    else:
        LINKED_CHAT_CACHE.clear()
        logger.info("已清除所有讨论组ID缓存")


async def get_discussion_group_id_cached(client, channel_url):
    """获取频道的讨论组ID(带缓存)

    首先尝试从缓存获取,如果缓存不存在则从Telegram获取并缓存结果

    Args:
        client: Telegram客户端实例
        channel_url: 频道URL

    Returns:
        int: 讨论组ID(已转换为超级群组格式),如果频道没有讨论组则返回None
    """
    # 1. 先尝试从缓存获取
    cached_id = get_cached_discussion_group_id(channel_url)
    if cached_id is not None:
        logger.debug(f"使用缓存的讨论组ID: {channel_url} -> {cached_id}")
        return cached_id

    # 2. 缓存未命中,从Telegram获取
    from telethon.tl.functions.channels import GetFullChannelRequest

    try:
        full_channel = await client.get_entity(channel_url)

        # 获取讨论组ID
        discussion_group_id = None
        if hasattr(full_channel, 'linked_chat_id') and full_channel.linked_chat_id:
            discussion_group_id = full_channel.linked_chat_id
        else:
            # 尝试使用GetFullChannelRequest
            logger.debug(f"频道 {channel_url} 没有linked_chat_id属性,尝试GetFullChannelRequest")
            full_info = await client(GetFullChannelRequest(full_channel))
            if hasattr(full_info.full_chat, 'linked_chat_id') and full_info.full_chat.linked_chat_id:
                discussion_group_id = full_info.full_chat.linked_chat_id

        if discussion_group_id:
            # 转换为超级群组格式
            if discussion_group_id > 0:
                discussion_group_id = -1000000000000 - discussion_group_id

            # 缓存结果
            cache_discussion_group_id(channel_url, discussion_group_id)
            logger.info(f"已获取并缓存讨论组ID: {channel_url} -> {discussion_group_id}")
            return discussion_group_id
        else:
            logger.warning(f"频道 {channel_url} 没有绑定讨论组")
            return None

    except Exception as e:
        logger.error(f"获取频道 {channel_url} 的讨论组ID失败: {e}")
        return None


# ==================== 问答Bot人格配置管理 ====================

def get_qa_bot_persona():
    """获取问答Bot的人格描述

    优先级（从高到低）：
    1. config.json 中的 qa_bot_persona 字段
    2. .env 中的 QA_BOT_PERSONA 环境变量
    3. data/qa_persona.txt 文件
    4. 内置默认人格 (DEFAULT_QA_PERSONA)

    Returns:
        str: 问答Bot的人格描述文本
    """
    # 1. 先尝试从配置文件读取
    config = load_config()
    persona_from_config = config.get('qa_bot_persona')
    if persona_from_config:
        logger.debug("使用配置文件中的问答Bot人格")
        return persona_from_config

    # 2. 再尝试从环境变量读取
    persona_from_env = os.getenv('QA_BOT_PERSONA')
    if persona_from_env:
        # 检查是否是文件路径（如果以.txt结尾或路径包含文件分隔符）
        if persona_from_env.endswith('.txt') or '\\' in persona_from_env or '/' in persona_from_env:
            try:
                with open(persona_from_env, 'r', encoding='utf-8') as f:
                    persona = f.read().strip()
                    logger.debug(f"从环境变量指定的文件加载问答Bot人格: {persona_from_env}")
                    return persona
            except Exception as e:
                logger.warning(f"从环境变量指定的文件加载人格失败: {e}，使用环境变量值作为人格文本")
                return persona_from_env
        else:
            logger.debug("使用环境变量中的问答Bot人格")
            return persona_from_env

    # 3. 尝试从默认文件读取
    try:
        with open(QA_PERSONA_FILE, 'r', encoding='utf-8') as f:
            persona = f.read().strip()
            logger.debug(f"从默认文件加载问答Bot人格: {QA_PERSONA_FILE}")
            return persona
    except FileNotFoundError:
        # 4. 文件不存在，返回默认人格并创建文件
        logger.info(f"人格文件 {QA_PERSONA_FILE} 不存在，创建默认文件")
        try:
            with open(QA_PERSONA_FILE, 'w', encoding='utf-8') as f:
                f.write(DEFAULT_QA_PERSONA)
            logger.info(f"已创建默认人格文件: {QA_PERSONA_FILE}")
        except Exception as e:
            logger.warning(f"创建默认人格文件失败: {e}")
        return DEFAULT_QA_PERSONA
    except Exception as e:
        logger.error(f"读取人格文件失败: {e}，使用默认人格")
        return DEFAULT_QA_PERSONA
