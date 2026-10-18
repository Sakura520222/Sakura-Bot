# Copyright 2026 Sakura-Bot
#
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
#
# - 署名：必须提供本项目的原始来源链接
# - 相同方式共享：衍生作品必须采用相同的许可证
#
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
# 许可证全文：参见 LICENSE 文件

import asyncio
import logging
import os
from pathlib import Path

from dotenv import load_dotenv

from . import i18n

# 配置日志
# 默认日志级别
DEFAULT_LOG_LEVEL = logging.DEBUG

# 日志级别映射
LOG_LEVEL_MAP = {
    "DEBUG": logging.DEBUG,
    "INFO": logging.INFO,
    "WARNING": logging.WARNING,
    "ERROR": logging.ERROR,
    "CRITICAL": logging.CRITICAL,
}


# 日志级别转换函数
def get_log_level(level_str):
    """将字符串日志级别转换为logging模块对应的级别常量"""
    if not level_str:
        return DEFAULT_LOG_LEVEL
    return LOG_LEVEL_MAP.get(level_str.upper(), DEFAULT_LOG_LEVEL)


# 日志初始化移至 main.py，由 setup_logging() 统一管理
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


def normalize_channel_id(channel_id: str) -> str:
    """
    标准化频道ID格式，确保所有频道ID使用统一格式

    Args:
        channel_id: 频道ID（可能是各种格式）

    Returns:
        str: 标准化后的频道ID（完整URL格式）

    Examples:
        >>> normalize_channel_id('jffnekjdnfn')
        'https://t.me/jffnekjdnfn'
        >>> normalize_channel_id('@jffnekjdnfn')
        'https://t.me/jffnekjdnfn'
        >>> normalize_channel_id('https://t.me/jffnekjdnfn')
        'https://t.me/jffnekjdnfn'
        >>> normalize_channel_id('https://t.me/jffnekjdnfn/')
        'https://t.me/jffnekjdnfn'
        >>> normalize_channel_id('t.me/jffnekjdnfn')
        'https://t.me/jffnekjdnfn'
    """
    if not channel_id:
        return channel_id

    # 去除首尾空格
    channel_id = channel_id.strip()

    # 支持完整或省略协议的 Telegram 链接（www. 前缀需优先匹配，避免被短前缀截断）
    for prefix in (
        "https://www.t.me/",
        "https://t.me/",
        "http://www.t.me/",
        "http://t.me/",
        "https://www.telegram.me/",
        "https://telegram.me/",
        "http://www.telegram.me/",
        "http://telegram.me/",
        "www.t.me/",
        "t.me/",
        "www.telegram.me/",
        "telegram.me/",
    ):
        if channel_id.startswith(prefix):
            return f"https://t.me/{channel_id.removeprefix(prefix).rstrip('/')}"

    # 去除@前缀
    if channel_id.startswith("@"):
        channel_id = channel_id[1:]

    # 返回标准化格式
    return f"https://t.me/{channel_id}"


def comparable_channel_key(channel_id: str) -> str:
    """提取频道键的可比较形式，用于频道配置键的等价匹配

    在 normalize_channel_id 的基础上去除 URL 前缀并统一小写：
    username 在 Telegram 中不区分大小写，数字 ID 不受小写影响。
    仅用于比较，不作为持久化的配置键。

    Examples:
        >>> comparable_channel_key('@Nahida_Leak')
        'nahida_leak'
        >>> comparable_channel_key('https://t.me/Nahida_Leak/')
        'nahida_leak'
    """
    return normalize_channel_id(channel_id).removeprefix("https://t.me/").lower()


def find_equivalent_channel_key(settings: dict, channel: str) -> str | None:
    """在频道配置字典中查找与 channel 指向同一频道的既有键

    兼容 @username、username、完整 URL、大小写差异与末尾斜杠等写法，
    使读取、写入、删除使用同一匹配规则；无匹配时返回 None。
    仅做键的等价匹配，不猜测数字 ID 与 username 的对应关系。
    """
    if not isinstance(channel, str) or not channel.strip():
        return None
    target = comparable_channel_key(channel)
    for key in settings:
        if isinstance(key, str) and comparable_channel_key(key) == target:
            return key
    return None


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
DEFAULT_QA_PERSONA = """你是一个友好、专业的通用智能助手。

【角色特点】
- [Helpful] 乐于助人，可以回答各类问题：日常聊天、知识问答、编程、写作、分析等
- [Concise] 回答清晰有条理，直击要点
- [Trustworthy] 基于事实回答，不确定时如实说明

【你的能力】
- 你接入了一个 Telegram 频道管理系统的数据（频道历史总结、消息记录、统计数据等）。
  当用户的问题涉及这些频道内容时，你可以按需调用提供的检索工具查询后再回答。
- 普通聊天、通用知识、编程与创作类问题无需检索，直接回答即可。
- 是否需要检索由你根据问题性质自主判断，不要把每条消息都当成频道查询。

【回答风格】
- 使用清晰的结构和要点，语言自然、专业但不失亲和力
- 引用频道检索结果时保留相关帖子链接作为证据来源
- 检索不到相关信息时如实说明，不要编造"""

# 加载 .env 文件中的变量
# 显式指定 .env 文件路径（data/.env）
env_path = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "data", ".env")
load_dotenv(env_path, override=True)  # 使用 override=True 确保 .env 文件的值会覆盖系统环境变量
logger.info(f"已加载 .env 文件中的环境变量 (路径: {env_path})")

# 从环境变量中读取配置
logger.info("开始从环境变量加载配置...")
# 语言配置 - 从环境变量获取默认值
LANGUAGE_FROM_ENV = os.getenv("LANGUAGE", "zh-CN")
API_ID = os.getenv("TELEGRAM_API_ID")
API_HASH = os.getenv("TELEGRAM_API_HASH")
BOT_TOKEN = os.getenv("TELEGRAM_BOT_TOKEN")
QA_BOT_USERNAME = os.getenv("QA_BOT_USERNAME", "").lstrip("@")  # 移除@前缀（如果有）
# AI 配置 - 从环境变量获取默认值
LLM_API_KEY = os.getenv("LLM_API_KEY", os.getenv("DEEPSEEK_API_KEY"))
LLM_BASE_URL = os.getenv("LLM_BASE_URL", "https://api.deepseek.com")
LLM_MODEL = os.getenv("LLM_MODEL", "deepseek-chat")

# 频道配置 - 从环境变量获取默认值
TARGET_CHANNEL = os.getenv("TARGET_CHANNEL")
CHANNELS = []
if TARGET_CHANNEL:
    # 支持多个频道，用逗号分隔
    CHANNELS = [channel.strip() for channel in TARGET_CHANNEL.split(",")]
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

# 投票是否公开投票者，默认为True（公开）
POLL_PUBLIC_VOTERS = os.getenv("POLL_PUBLIC_VOTERS", "true").lower() in ("true", "1", "yes")

# 是否启用自动趣味投票功能，默认为False
ENABLE_AUTO_POLL = False

# 投票重新生成数据文件锁，用于并发控制
_poll_regenerations_lock = asyncio.Lock()

# 日志级别 - 从环境变量获取默认值
LOG_LEVEL_FROM_ENV = os.getenv("LOG_LEVEL")
logger.debug(f"从环境变量读取的日志级别: {LOG_LEVEL_FROM_ENV}")

logger.debug(
    f"从环境变量加载的配置: API_ID={'***' if API_ID else '未设置'}, API_HASH={'***' if API_HASH else '未设置'}, BOT_TOKEN={'***' if BOT_TOKEN else '未设置'}"
)
logger.debug(f"AI配置 - 环境变量默认值: Base URL={LLM_BASE_URL}, Model={LLM_MODEL}")
logger.debug(f"目标频道列表: {CHANNELS}")

# 管理员 ID 列表，从环境变量读取后转为整数列表
REPORT_ADMIN_IDS = os.getenv("REPORT_ADMIN_IDS", "")
logger.debug(f"从环境变量读取的管理员ID: {REPORT_ADMIN_IDS}")

# 处理管理员ID列表
# 安全策略（fail-closed）：只有明确配置且格式合法的 Telegram 数字用户 ID 才会被
# 授予管理员权限；未配置或配置无效时管理员列表为空，所有管理命令与 WebUI 管理
# 权限一律拒绝，绝不能默认放行所有用户。
ADMIN_LIST: list[int] = []
if REPORT_ADMIN_IDS:
    # 支持多个管理员ID，用逗号分隔；非法条目跳过并告警
    for admin_id in REPORT_ADMIN_IDS.split(","):
        admin_id = admin_id.strip()
        if not admin_id:
            continue
        try:
            ADMIN_LIST.append(int(admin_id))
        except ValueError:
            logger.warning(f"忽略非法的管理员ID配置项: {admin_id!r}（必须是 Telegram 数字用户 ID）")
    if ADMIN_LIST:
        logger.info(f"已从环境变量加载管理员ID列表: {ADMIN_LIST}")
    else:
        logger.warning(
            "REPORT_ADMIN_IDS 未包含任何合法的数字用户 ID，管理员列表为空，"
            "所有管理命令与 WebUI 管理权限将被拒绝"
        )
else:
    logger.warning(
        "未配置 REPORT_ADMIN_IDS：管理员列表为空（fail-closed），"
        "所有管理命令与 WebUI 管理权限将被拒绝。"
        "请在 data/.env 中配置 REPORT_ADMIN_IDS=<Telegram数字用户ID>（多个用逗号分隔）"
    )


# 读取配置文件
def load_config():
    """兼容只读调用方；读取失败的空结果不能作为写入基线。"""
    from core.config.persistence import ConfigPersistenceError, ConfigSnapshot

    try:
        return read_config()
    except ConfigPersistenceError as error:
        logger.warning("无法读取完整配置: %s", error)
        return ConfigSnapshot({}, Path(CONFIG_FILE), valid=False)


def read_config():
    """严格读取完整配置，缺失、损坏或校验失败时向调用方报错。"""
    from core.config.persistence import ConfigStore

    return ConfigStore(CONFIG_FILE).read()


# 保存配置文件
def save_config(config):
    """明确的完整替换接口，保留删除语义；失败会抛出异常。"""
    from core.config.persistence import ConfigStore

    saved = ConfigStore(CONFIG_FILE).replace(config)
    update_module_variables(saved)
    return saved


def update_config_fields(fields):
    """原子更新指定顶层字段，始终从最新且有效的完整配置开始。"""
    from core.config.persistence import ConfigStore

    saved = ConfigStore(CONFIG_FILE).update_fields(fields)
    update_module_variables(saved)
    return saved


def save_config_changes(snapshot):
    """提交 load_config/read_config 的显式差异，保护并发更新及字段删除。"""
    from core.config.persistence import ConfigStore

    saved = ConfigStore(CONFIG_FILE).save_changes(snapshot)
    update_module_variables(saved)
    return saved


def _parse_bool(value) -> bool:
    """将配置值安全解析为布尔类型

    支持 bool、str（"true"/"1"/"yes"）和 int 类型输入。
    用于热重载时防止 JSON 配置文件中字符串形式的布尔值导致类型不一致。
    """
    if isinstance(value, bool):
        return value
    if isinstance(value, str):
        return value.lower() in ("true", "1", "yes")
    return bool(value)


def update_module_variables(config):
    """更新模块变量以匹配配置文件

    注意：所有布尔型配置项均通过 _parse_bool() 解析，确保无论配置文件
    传入 bool 还是 str 类型（如 "true"/"false"），都能正确转换为 Python bool。
    """
    global \
        LLM_API_KEY, \
        LLM_BASE_URL, \
        LLM_MODEL, \
        CHANNELS, \
        ENABLE_POLL, \
        SEND_REPORT_TO_SOURCE, \
        SUMMARY_SCHEDULES, \
        CHANNEL_POLL_SETTINGS, \
        POLL_REGEN_THRESHOLD, \
        ENABLE_VOTE_REGEN_REQUEST, \
        POLL_PUBLIC_VOTERS, \
        ENABLE_AUTO_POLL, \
        CHANNEL_AUTO_POLL_SETTINGS

    # 更新AI配置
    LLM_API_KEY = config.get("api_key", LLM_API_KEY)
    LLM_BASE_URL = config.get("base_url", LLM_BASE_URL)
    LLM_MODEL = config.get("model", LLM_MODEL)

    # 更新频道列表
    config_channels = config.get("channels")
    if isinstance(config_channels, list):
        CHANNELS = config_channels
        logger.info(f"已更新内存中的频道列表: {CHANNELS}")

    # 更新是否将报告发送回源频道的配置
    if "send_report_to_source" in config:
        SEND_REPORT_TO_SOURCE = _parse_bool(config["send_report_to_source"])
        logger.info(f"已更新内存中的发送报告到源频道的配置: {SEND_REPORT_TO_SOURCE}")

    # 更新是否启用投票功能的配置
    if "enable_poll" in config:
        ENABLE_POLL = _parse_bool(config["enable_poll"])
        logger.info(f"已更新内存中的投票功能配置: {ENABLE_POLL}")

    # 更新频道级时间配置
    summary_schedules_config = config.get("summary_schedules", {})
    if isinstance(summary_schedules_config, dict):
        SUMMARY_SCHEDULES = summary_schedules_config
        logger.info(f"已更新内存中的频道级时间配置: {len(SUMMARY_SCHEDULES)} 个频道")

    # 更新频道级投票配置
    channel_poll_config = config.get("channel_poll_settings", {})
    if isinstance(channel_poll_config, dict):
        CHANNEL_POLL_SETTINGS = channel_poll_config
        logger.info(f"已更新内存中的频道级投票配置: {len(CHANNEL_POLL_SETTINGS)} 个频道")

    # 更新投票重新生成请求配置
    if "poll_regen_threshold" in config:
        POLL_REGEN_THRESHOLD = config["poll_regen_threshold"]
        logger.info(f"已更新内存中的投票重新生成阈值: {POLL_REGEN_THRESHOLD}")

    if "enable_vote_regen_request" in config:
        ENABLE_VOTE_REGEN_REQUEST = _parse_bool(config["enable_vote_regen_request"])
        logger.info(f"已更新内存中的投票重新生成请求功能配置: {ENABLE_VOTE_REGEN_REQUEST}")

    if "public_voters" in config:
        POLL_PUBLIC_VOTERS = _parse_bool(config["public_voters"])
        logger.info(f"已更新内存中的投票公开配置: {POLL_PUBLIC_VOTERS}")

    # 更新自动趣味投票配置
    if "enable_auto_poll" in config:
        ENABLE_AUTO_POLL = _parse_bool(config["enable_auto_poll"])
        logger.info(f"已更新内存中的自动趣味投票配置: {ENABLE_AUTO_POLL}")

    # 更新频道级自动趣味投票配置
    auto_poll_config = config.get("channel_auto_poll_settings", {})
    if isinstance(auto_poll_config, dict):
        CHANNEL_AUTO_POLL_SETTINGS = auto_poll_config
        logger.info(
            f"已更新内存中的频道级自动趣味投票配置: {len(CHANNEL_AUTO_POLL_SETTINGS)} 个频道"
        )


# 加载配置文件，覆盖环境变量默认值
logger.info("开始加载配置文件...")
config = load_config()

# 语言配置初始化
# 优先级：config.json > .env > 默认值(zh-CN)
LANGUAGE_FROM_CONFIG = None
if config:
    LANGUAGE_FROM_CONFIG = config.get("language")
    logger.debug(f"从配置文件读取的语言配置: {LANGUAGE_FROM_CONFIG}")

# 确定最终语言配置
final_language = LANGUAGE_FROM_CONFIG or LANGUAGE_FROM_ENV
logger.info(
    f"最终语言配置: {final_language} (来源: {'配置文件' if LANGUAGE_FROM_CONFIG else '环境变量或默认值'})"
)

# 初始化国际化模块的语言设置
i18n.set_language(final_language)
logger.info(f"国际化模块语言已设置为: {i18n.get_language()}")

if config:
    logger.debug("已读取配置文件，字段数量: %s", len(config))
    # 只有当配置文件中存在且不为None时才覆盖环境变量的值
    if "api_key" in config and config["api_key"]:
        LLM_API_KEY = config["api_key"]
        logger.debug("从配置文件覆盖 LLM_API_KEY")
    else:
        logger.debug("使用环境变量中的 LLM_API_KEY")

    if "base_url" in config and config["base_url"]:
        LLM_BASE_URL = config["base_url"]
        logger.debug("从配置文件覆盖 LLM_BASE_URL")
    else:
        logger.debug("使用环境变量中的 LLM_BASE_URL")

    if "model" in config and config["model"]:
        LLM_MODEL = config["model"]
        logger.debug("从配置文件覆盖 LLM_MODEL")
    else:
        logger.debug("使用环境变量中的 LLM_MODEL")

    # 从配置文件读取频道列表
    config_channels = config.get("channels")
    if config_channels and isinstance(config_channels, list):
        CHANNELS = config_channels
        logger.info(f"已从配置文件加载频道列表: {CHANNELS}")

    # 使用统一的模块变量更新函数（避免重复逻辑）
    update_module_variables(config)

    # 从配置文件读取日志级别
    LOG_LEVEL_FROM_CONFIG = config.get("log_level")
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
    logger.info(
        f"日志级别已从 {logging.getLevelName(current_level)} 更改为 {logging.getLevelName(final_log_level)}"
    )
else:
    logger.info(f"当前日志级别: {logging.getLevelName(current_level)}")

# 抑制第三方库的 INFO 日志输出（与 QA Bot 保持一致）
logging.getLogger("httpx").setLevel(logging.WARNING)
logging.getLogger("apscheduler").setLevel(logging.WARNING)
# 降低 Telethon 网络连接日志级别（重连是正常机制，不需要警告）
logging.getLogger("telethon.network.connection.connection").setLevel(logging.ERROR)
# 抑制 Telethon mtprotosender 的重连日志
logging.getLogger("telethon.network.mtprotosender").setLevel(logging.WARNING)

# 机器人状态管理
BOT_STATE_RUNNING = "running"
BOT_STATE_PAUSED = "paused"
BOT_STATE_SHUTTING_DOWN = "shutting_down"

# 全局状态变量
_bot_state = BOT_STATE_RUNNING
_scheduler_instance = None
_shutdown_event = None  # 全局关机事件，用于触发与 Ctrl+C 相同的关闭流程


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


def set_shutdown_event(event):
    """设置全局关机事件"""
    global _shutdown_event
    _shutdown_event = event
    logger.info("全局关机事件已设置")


def trigger_shutdown():
    """触发关机事件（与 Ctrl+C 相同的效果）"""
    global _shutdown_event
    if _shutdown_event is not None:
        logger.info("🚪 触发关机事件（与 Ctrl+C 相同的效果）")
        _shutdown_event.set()
    else:
        logger.warning("⚠️ 关机事件未设置，无法触发")


# 自动总结时间配置
# 默认时间：每周一早上9点
DEFAULT_SUMMARY_DAY = "mon"  # 星期几：mon, tue, wed, thu, fri, sat, sun
DEFAULT_SUMMARY_HOUR = 9  # 小时：0-23
DEFAULT_SUMMARY_MINUTE = 0  # 分钟：0-59

# 从配置文件读取频道级时间配置
SUMMARY_SCHEDULES = {}
if config:
    summary_schedules_config = config.get("summary_schedules", {})
    if isinstance(summary_schedules_config, dict):
        SUMMARY_SCHEDULES = summary_schedules_config
        logger.info(f"已从配置文件加载频道级时间配置: {len(SUMMARY_SCHEDULES)} 个频道")
    else:
        logger.warning("配置文件中的summary_schedules格式不正确，使用默认配置")

# 频道级投票配置
CHANNEL_POLL_SETTINGS = {}
if config:
    channel_poll_config = config.get("channel_poll_settings", {})
    if isinstance(channel_poll_config, dict):
        CHANNEL_POLL_SETTINGS = channel_poll_config
        logger.info(f"已从配置文件加载频道级投票配置: {len(CHANNEL_POLL_SETTINGS)} 个频道")
    else:
        logger.warning("配置文件中的channel_poll_settings格式不正确，使用默认配置")

# 频道级自动趣味投票配置
CHANNEL_AUTO_POLL_SETTINGS = {}
if config:
    auto_poll_config = config.get("channel_auto_poll_settings", {})
    if isinstance(auto_poll_config, dict):
        CHANNEL_AUTO_POLL_SETTINGS = auto_poll_config
        logger.info(
            f"已从配置文件加载频道级自动趣味投票配置: {len(CHANNEL_AUTO_POLL_SETTINGS)} 个频道"
        )
    else:
        logger.warning("配置文件中的channel_auto_poll_settings格式不正确，使用默认配置")


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
            "frequency": "weekly",
            "days": [DEFAULT_SUMMARY_DAY],
            "hour": DEFAULT_SUMMARY_HOUR,
            "minute": DEFAULT_SUMMARY_MINUTE,
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
        return set_channel_schedule_v2(channel, "weekly", days=[day], hour=hour, minute=minute)
    else:
        # 仅更新时间，保持默认星期
        return set_channel_schedule_v2(
            channel, "weekly", days=[DEFAULT_SUMMARY_DAY], hour=hour, minute=minute
        )


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
        if "summary_schedules" in current_config and channel in current_config["summary_schedules"]:
            # 删除频道配置
            del current_config["summary_schedules"][channel]

            # 保存配置（save_config会自动更新模块变量）
            save_config_changes(current_config)

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
    valid_days = ["mon", "tue", "wed", "thu", "fri", "sat", "sun"]

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
    if "frequency" in schedule_dict:
        # 确保 days 字段存在（weekly 模式）
        if schedule_dict["frequency"] == "weekly" and "days" not in schedule_dict:
            # 如果没有 days 字段但有 day 字段，转换它
            if "day" in schedule_dict:
                schedule_dict["days"] = [schedule_dict["day"]]
            else:
                schedule_dict["days"] = [DEFAULT_SUMMARY_DAY]
        return schedule_dict

    # 向后兼容：旧格式 (day 字段)
    if "day" in schedule_dict:
        return {
            "frequency": "weekly",
            "days": [schedule_dict["day"]],
            "hour": schedule_dict.get("hour", DEFAULT_SUMMARY_HOUR),
            "minute": schedule_dict.get("minute", DEFAULT_SUMMARY_MINUTE),
        }

    # 默认配置
    return {
        "frequency": "weekly",
        "days": [DEFAULT_SUMMARY_DAY],
        "hour": schedule_dict.get("hour", DEFAULT_SUMMARY_HOUR),
        "minute": schedule_dict.get("minute", DEFAULT_SUMMARY_MINUTE),
    }


def validate_schedule_v2(config_dict):
    """验证新的时间配置格式

    Args:
        config_dict: 包含 frequency, days, hour, minute 的字典

    Returns:
        tuple: (是否有效, 错误信息)
    """
    valid_days = ["mon", "tue", "wed", "thu", "fri", "sat", "sun"]
    valid_frequencies = ["daily", "weekly"]

    # 验证 frequency
    frequency = config_dict.get("frequency")
    if frequency not in valid_frequencies:
        return False, f"无效的频率: {frequency}，有效值: {', '.join(valid_frequencies)}"

    # 验证 days（仅 weekly 模式需要）
    if frequency == "weekly":
        days = config_dict.get("days", [])
        if not isinstance(days, list) or not days:
            return False, "weekly 模式必须提供 days 字段（非空数组）"

        for day in days:
            if day not in valid_days:
                return False, f"无效的星期几: {day}，有效值: {', '.join(valid_days)}"

    # 验证时间
    hour = config_dict.get("hour")
    minute = config_dict.get("minute")

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
            "frequency": frequency,
            "hour": hour if hour is not None else DEFAULT_SUMMARY_HOUR,
            "minute": minute if minute is not None else DEFAULT_SUMMARY_MINUTE,
        }

        # weekly 模式需要 days 字段
        if frequency == "weekly":
            if days is None:
                days = [DEFAULT_SUMMARY_DAY]
            config_dict["days"] = days

        # 验证配置
        is_valid, error_msg = validate_schedule_v2(config_dict)
        if not is_valid:
            logger.error(f"配置验证失败: {error_msg}")
            return False

        # 加载当前配置
        current_config = load_config()

        # 确保summary_schedules字段存在
        if "summary_schedules" not in current_config:
            current_config["summary_schedules"] = {}

        # 更新配置
        current_config["summary_schedules"][channel] = config_dict

        # 保存配置
        save_config_changes(current_config)

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
    frequency = schedule_config.get("frequency", "weekly")

    if frequency == "daily":
        # 每天模式
        return {
            "day_of_week": "*",  # 每天
            "hour": schedule_config["hour"],
            "minute": schedule_config["minute"],
        }
    elif frequency == "weekly":
        # 每周模式（支持多天）
        days_str = ",".join(schedule_config["days"])
        return {
            "day_of_week": days_str,
            "hour": schedule_config["hour"],
            "minute": schedule_config["minute"],
        }
    else:
        # 默认：每周一
        return {
            "day_of_week": "mon",
            "hour": schedule_config.get("hour", DEFAULT_SUMMARY_HOUR),
            "minute": schedule_config.get("minute", DEFAULT_SUMMARY_MINUTE),
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
            "enabled": config.get("enabled", None),  # None 表示使用全局配置
            "send_to_channel": config.get("send_to_channel", False),  # 默认讨论组模式
            "public_voters": config.get("public_voters", None),  # None 表示使用全局配置
        }
    else:
        # 没有独立配置，返回默认配置
        return {
            "enabled": None,  # 使用全局 ENABLE_POLL
            "send_to_channel": False,  # 默认讨论组模式
            "public_voters": None,  # 使用全局配置
        }


def set_channel_poll_config(channel, enabled=None, send_to_channel=None, public_voters=None):
    """设置指定频道的投票配置

    Args:
        channel: 频道URL
        enabled: 是否启用投票（None 表示不修改）
        send_to_channel: 投票发送位置（None 表示不修改）
            True - 频道模式（直接发送到频道）
            False - 讨论组模式（发送到讨论组）
        public_voters: 投票是否公开投票者（None 表示不修改）
            True - 公开投票
            False - 匿名投票

    Returns:
        bool: 是否成功保存配置
    """
    try:
        # 加载当前配置
        current_config = load_config()

        # 确保channel_poll_settings字段存在
        if "channel_poll_settings" not in current_config:
            current_config["channel_poll_settings"] = {}

        # 获取频道当前配置
        if channel not in current_config["channel_poll_settings"]:
            current_config["channel_poll_settings"][channel] = {}

        channel_config = current_config["channel_poll_settings"][channel]

        # 更新配置（只更新提供的参数）
        if enabled is not None:
            channel_config["enabled"] = enabled
            logger.info(f"设置频道 {channel} 的投票启用状态: {enabled}")

        if send_to_channel is not None:
            channel_config["send_to_channel"] = send_to_channel
            logger.info(
                f"设置频道 {channel} 的投票发送位置: {'频道' if send_to_channel else '讨论组'}"
            )

        if public_voters is not None:
            channel_config["public_voters"] = public_voters
            mode = "公开" if public_voters else "匿名"
            logger.info(f"设置频道 {channel} 的投票模式: {mode}")

        # 保存配置
        save_config_changes(current_config)

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
        if (
            "channel_poll_settings" in current_config
            and channel in current_config["channel_poll_settings"]
        ):
            # 删除频道配置
            del current_config["channel_poll_settings"][channel]

            # 保存配置
            save_config_changes(current_config)

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


# ==================== 频道级自动趣味投票配置管理函数 ====================


def get_channel_auto_poll_config(channel):
    """获取指定频道的自动趣味投票配置

    Args:
        channel: 频道URL、@username、username 或数字ID

    Returns:
        dict: 包含 enabled 的配置字典
            - enabled: 是否启用自动趣味投票（None 表示使用全局配置）
            - matched_key: 命中的配置键（None 表示无独立配置）
    """
    matched_key = find_equivalent_channel_key(CHANNEL_AUTO_POLL_SETTINGS, channel)
    if matched_key is not None:
        if matched_key != channel:
            logger.info(f"频道自动趣味投票配置键已归一化: '{channel}' -> '{matched_key}'")
        config = CHANNEL_AUTO_POLL_SETTINGS[matched_key]
        return {
            "enabled": config.get("enabled", None),  # None 表示使用全局配置
            "matched_key": matched_key,
        }
    else:
        return {
            "enabled": None,  # 使用全局 ENABLE_AUTO_POLL
            "matched_key": None,
        }


def is_auto_poll_enabled_for_channel(channel):
    """判断指定频道的自动趣味投票最终是否启用。

    enable_auto_poll 是主开关：全局关闭时任何频道配置都不生效。
    全局开启时，未配置频道默认启用，频道配置可显式启用或禁用。
    """
    if not ENABLE_AUTO_POLL:
        logger.debug(f"[自动趣味投票] 全局开关关闭: channel={channel} -> enabled=False")
        return False

    channel_config = get_channel_auto_poll_config(channel)
    enabled = channel_config.get("enabled")
    if enabled is None:
        logger.debug(
            f"[自动趣味投票] 频道无独立配置，继承全局开关: "
            f"channel={channel}, matched_key=None -> enabled=True"
        )
        return True
    result = _parse_bool(enabled)
    logger.debug(
        f"[自动趣味投票] 频道独立配置生效: "
        f"channel={channel}, matched_key={channel_config.get('matched_key')} -> enabled={result}"
    )
    return result


def set_channel_auto_poll_config(channel, enabled=None):
    """设置指定频道的自动趣味投票配置

    Args:
        channel: 频道URL
        enabled: 是否启用自动趣味投票（None 表示不修改）

    Returns:
        bool: 是否成功保存配置
    """
    try:
        current_config = load_config()

        if "channel_auto_poll_settings" not in current_config:
            current_config["channel_auto_poll_settings"] = {}

        settings = current_config["channel_auto_poll_settings"]
        # 命中既有等价键时原地更新，避免同一频道产生多个配置条目
        channel_key = find_equivalent_channel_key(settings, channel)
        if channel_key is None:
            channel_key = channel
        elif channel_key != channel:
            logger.info(f"频道自动趣味投票配置键已归一化: '{channel}' -> '{channel_key}'")

        if channel_key not in settings:
            settings[channel_key] = {}

        channel_config = settings[channel_key]

        if enabled is not None:
            channel_config["enabled"] = enabled
            logger.info(f"设置频道 {channel} 的自动趣味投票启用状态: {enabled}")

        save_config_changes(current_config)

        logger.info(f"已更新频道 {channel} 的自动趣味投票配置")
        return True
    except Exception as e:
        logger.error(f"设置频道自动趣味投票配置时出错: {type(e).__name__}: {e}", exc_info=True)
        return False


def delete_channel_auto_poll_config(channel):
    """删除指定频道的自动趣味投票配置

    删除后，该频道将使用全局自动趣味投票配置

    Args:
        channel: 频道URL

    Returns:
        bool: 是否成功删除配置
    """
    try:
        current_config = load_config()

        if "channel_auto_poll_settings" in current_config:
            settings = current_config["channel_auto_poll_settings"]
            # 按等价规则命中既有键（兼容 @username、username、URL、大小写差异）
            channel_key = find_equivalent_channel_key(settings, channel)
            if channel_key is not None:
                del settings[channel_key]
                save_config_changes(current_config)
                logger.info(f"已删除频道 {channel_key} 的自动趣味投票配置，将使用全局配置")
                return True

        logger.info(f"频道 {channel} 没有独立的自动趣味投票配置，无需删除")
        return True
    except Exception as e:
        logger.error(f"删除频道自动趣味投票配置时出错: {type(e).__name__}: {e}", exc_info=True)
        return False


def load_poll_regenerations():
    """加载投票重新生成数据

    Returns:
        dict: 投票重新生成数据字典
    """
    import json

    if os.path.exists(POLL_REGENERATIONS_FILE):
        try:
            with open(POLL_REGENERATIONS_FILE, encoding="utf-8") as f:
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


def add_poll_regeneration(
    channel,
    summary_msg_id,
    poll_msg_id,
    button_msg_id,
    summary_text,
    channel_name,
    send_to_channel,
    discussion_forward_msg_id=None,
):
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
        "voters": [],  # 初始化已投票用户列表
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
            logger.warning(
                f"投票重新生成记录不存在: channel={channel}, summary_id={summary_msg_id}"
            )
            return False, 0, False

        record = data[channel][str(summary_msg_id)]

        # 检查用户是否已投票
        if user_id in record.get("voters", []):
            logger.info(f"用户 {user_id} 已经投票过了")
            return False, record.get("vote_count", 0), True

        # 增加投票计数
        record["vote_count"] = record.get("vote_count", 0) + 1
        record.setdefault("voters", []).append(user_id)

        # 保存数据
        save_poll_regenerations(data)

        logger.info(
            f"投票计数已更新: channel={channel}, summary_id={summary_msg_id}, count={record['vote_count']}, user_id={user_id}"
        )
        return True, record["vote_count"], False


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
    data[channel][str(summary_msg_id)]["vote_count"] = 0
    data[channel][str(summary_msg_id)]["voters"] = []

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

    return data[channel][str(summary_msg_id)].get("vote_count", 0)


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
        if hasattr(full_channel, "linked_chat_id") and full_channel.linked_chat_id:
            discussion_group_id = full_channel.linked_chat_id
        else:
            # 尝试使用GetFullChannelRequest
            logger.debug(f"频道 {channel_url} 没有linked_chat_id属性,尝试GetFullChannelRequest")
            full_info = await client(GetFullChannelRequest(full_channel))
            if (
                hasattr(full_info.full_chat, "linked_chat_id")
                and full_info.full_chat.linked_chat_id
            ):
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


# ==================== 频道消息转发配置管理 ====================


def get_forwarding_config():
    """获取转发功能配置

    Returns:
        dict: 转发配置字典，包含 enabled 和 rules
            - enabled: 是否启用转发功能
            - rules: 转发规则列表
    """
    config = load_config()
    forwarding_config = config.get("forwarding", {})

    # 确保返回完整的配置结构
    if not isinstance(forwarding_config, dict):
        logger.warning("转发配置格式不正确，使用默认配置")
        return {"enabled": False, "rules": []}

    # 确保必要字段存在
    if "enabled" not in forwarding_config:
        forwarding_config["enabled"] = False
    if "rules" not in forwarding_config:
        # 尝试从旧的 forwarding_rules 迁移
        old_rules = config.get("forwarding_rules", [])
        if old_rules:
            logger.info("检测到旧的 forwarding_rules 配置，自动迁移到 forwarding.rules")
            # 自动迁移配置
            config["forwarding"] = {
                "enabled": forwarding_config.get("enabled", False),
                "rules": old_rules,
            }
            save_config_changes(config)
            forwarding_config["rules"] = old_rules
        else:
            forwarding_config["rules"] = []

    return forwarding_config


def get_forwarding_rules():
    """获取所有转发规则配置

    Returns:
        list: 转发规则列表，每个规则为字典格式
    """
    config = load_config()
    return config.get("forwarding_rules", [])


def get_forwarding_rules_by_source(source_channel):
    """获取指定源频道的转发规则

    Args:
        source_channel: 源频道URL或ID

    Returns:
        list: 匹配的转发规则列表
    """
    rules = get_forwarding_rules()
    return [
        rule
        for rule in rules
        if rule.get("source_channel") == source_channel and rule.get("enabled", True)
    ]


def add_forwarding_rule(
    source_channel, target_channel, keywords=None, ai_prompt="", custom_footer=""
):
    """添加转发规则

    Args:
        source_channel: 源频道URL或ID
        target_channel: 目标频道URL或ID
        keywords: 关键词列表
        ai_prompt: AI提示词
        custom_footer: 自定义底栏

    Returns:
        bool: 是否成功添加
    """
    try:
        # 加载当前配置
        config = load_config()

        # 标准化频道ID
        source_channel = normalize_channel_id(source_channel)
        target_channel = normalize_channel_id(target_channel)

        # 检查规则是否已存在
        existing_rules = config.get("forwarding_rules", [])
        for rule in existing_rules:
            if (
                rule.get("source_channel") == source_channel
                and rule.get("target_channel") == target_channel
            ):
                logger.warning(f"转发规则已存在: {source_channel} -> {target_channel}")
                return False

        # 添加新规则
        new_rule = {
            "source_channel": source_channel,
            "target_channel": target_channel,
            "keywords": keywords or [],
            "ai_prompt": ai_prompt,
            "custom_footer": custom_footer,
            "enabled": True,
        }

        config.setdefault("forwarding_rules", []).append(new_rule)

        # 保存配置
        save_config_changes(config)

        logger.info(f"已添加转发规则: {source_channel} -> {target_channel}")
        return True
    except Exception as e:
        logger.error(f"添加转发规则时出错: {type(e).__name__}: {e}", exc_info=True)
        return False


def remove_forwarding_rule(source_channel, target_channel):
    """移除转发规则

    Args:
        source_channel: 源频道URL或ID
        target_channel: 目标频道URL或ID

    Returns:
        bool: 是否成功移除
    """
    try:
        # 加载当前配置
        config = load_config()

        # 标准化频道ID
        source_channel = normalize_channel_id(source_channel)
        target_channel = normalize_channel_id(target_channel)

        # 查找并删除规则
        rules = config.get("forwarding_rules", [])
        original_count = len(rules)

        rules = [
            rule
            for rule in rules
            if not (
                rule.get("source_channel") == source_channel
                and rule.get("target_channel") == target_channel
            )
        ]

        if len(rules) == original_count:
            logger.warning(f"转发规则不存在: {source_channel} -> {target_channel}")
            return False

        config["forwarding_rules"] = rules

        # 保存配置
        save_config_changes(config)

        logger.info(f"已移除转发规则: {source_channel} -> {target_channel}")
        return True
    except Exception as e:
        logger.error(f"移除转发规则时出错: {type(e).__name__}: {e}", exc_info=True)
        return False


def update_forwarding_rule(source_channel, target_channel, **kwargs):
    """更新转发规则

    Args:
        source_channel: 源频道URL或ID
        target_channel: 目标频道URL或ID
        **kwargs: 要更新的字段（keywords, ai_prompt, custom_footer, enabled等）

    Returns:
        bool: 是否成功更新
    """
    try:
        # 加载当前配置
        config = load_config()

        # 标准化频道ID
        source_channel = normalize_channel_id(source_channel)
        target_channel = normalize_channel_id(target_channel)

        # 查找规则
        rules = config.get("forwarding_rules", [])
        for rule in rules:
            if (
                rule.get("source_channel") == source_channel
                and rule.get("target_channel") == target_channel
            ):
                # 更新字段
                for key, value in kwargs.items():
                    if key in ["keywords", "ai_prompt", "custom_footer", "enabled"]:
                        rule[key] = value
                        logger.info(f"更新转发规则字段 {key}: {source_channel} -> {target_channel}")

                # 保存配置
                save_config_changes(config)
                return True

        logger.warning(f"转发规则不存在: {source_channel} -> {target_channel}")
        return False
    except Exception as e:
        logger.error(f"更新转发规则时出错: {type(e).__name__}: {e}", exc_info=True)
        return False


def toggle_forwarding_rule(source_channel, target_channel):
    """切换转发规则的启用状态

    Args:
        source_channel: 源频道URL或ID
        target_channel: 目标频道URL或ID

    Returns:
        bool: 新的启用状态，如果规则不存在返回None
    """
    try:
        # 加载当前配置
        config = load_config()

        # 标准化频道ID
        source_channel = normalize_channel_id(source_channel)
        target_channel = normalize_channel_id(target_channel)

        # 查找规则
        rules = config.get("forwarding_rules", [])
        for rule in rules:
            if (
                rule.get("source_channel") == source_channel
                and rule.get("target_channel") == target_channel
            ):
                # 切换状态
                current_state = rule.get("enabled", True)
                new_state = not current_state
                rule["enabled"] = new_state

                # 保存配置
                save_config_changes(config)

                logger.info(
                    f"转发规则状态已切换: {source_channel} -> {target_channel}, enabled={new_state}"
                )
                return new_state

        logger.warning(f"转发规则不存在: {source_channel} -> {target_channel}")
        return None
    except Exception as e:
        logger.error(f"切换转发规则状态时出错: {type(e).__name__}: {e}", exc_info=True)
        return None


def get_forwarding_enabled_sources():
    """获取所有已启用转发的源频道列表

    Returns:
        set: 源频道集合
    """
    rules = get_forwarding_rules()
    return {rule.get("source_channel") for rule in rules if rule.get("enabled", True)}


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
    persona_from_config = config.get("qa_bot_persona")
    if persona_from_config:
        logger.debug("使用配置文件中的问答Bot人格")
        return persona_from_config

    # 2. 再尝试从环境变量读取
    persona_from_env = os.getenv("QA_BOT_PERSONA")
    if persona_from_env:
        # 检查是否是文件路径（如果以.txt结尾或路径包含文件分隔符）
        if persona_from_env.endswith(".txt") or "\\" in persona_from_env or "/" in persona_from_env:
            try:
                with open(persona_from_env, encoding="utf-8") as f:
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
        with open(QA_PERSONA_FILE, encoding="utf-8") as f:
            persona = f.read().strip()
            logger.debug(f"从默认文件加载问答Bot人格: {QA_PERSONA_FILE}")
            return persona
    except FileNotFoundError:
        # 4. 文件不存在，返回默认人格并创建文件
        logger.info(f"人格文件 {QA_PERSONA_FILE} 不存在，创建默认文件")
        try:
            with open(QA_PERSONA_FILE, "w", encoding="utf-8") as f:
                f.write(DEFAULT_QA_PERSONA)
            logger.info(f"已创建默认人格文件: {QA_PERSONA_FILE}")
        except Exception as e:
            logger.warning(f"创建默认人格文件失败: {e}")
        return DEFAULT_QA_PERSONA
    except Exception as e:
        logger.error(f"读取人格文件失败: {e}，使用默认人格")
        return DEFAULT_QA_PERSONA


# ==================== 配置热重载支持 ====================


# 调度器热重载辅助函数
async def _reschedule_summary_jobs(event):
    """重新调度所有频道总结任务（热重载调度时间）

    当 summary_schedules 配置变更时，移除旧的调度任务并用新配置重新创建。

    Args:
        event: ConfigChangedEvent 实例
    """
    try:
        scheduler = get_scheduler_instance()
        if not scheduler:
            logger.warning("调度器实例未设置，跳过调度任务热重载")
            return

        # 获取当前频道列表
        channels = event.config.get("channels", [])
        if not channels:
            logger.info("频道列表为空，移除所有调度任务")
            return

        # 延迟导入，避免循环依赖
        from core.system.scheduler import main_job

        rescheduled = 0
        for channel in channels:
            schedule = get_channel_schedule(channel)
            trigger_params = build_cron_trigger(schedule)
            job_id = f"summary_job_{channel}"

            try:
                # 使用 replace_existing=True 原子性更新任务
                scheduler.add_job(
                    main_job,
                    "cron",
                    **trigger_params,
                    args=[channel],
                    id=job_id,
                    replace_existing=True,
                )
                rescheduled += 1
            except Exception as e:
                logger.error(f"重新调度频道 {channel} 任务失败: {type(e).__name__}: {e}")

        # 移除已不在频道列表中的旧任务
        try:
            existing_jobs = scheduler.get_jobs()
            removed = 0
            for job in existing_jobs:
                if job.id.startswith("summary_job_"):
                    # 提取频道 URL
                    job_channel = job.id[len("summary_job_") :]
                    if job_channel not in channels:
                        scheduler.remove_job(job.id)
                        removed += 1
            if removed > 0:
                logger.info(f"已移除 {removed} 个无效频道调度任务")
        except Exception as e:
            logger.error(f"清理无效调度任务失败: {type(e).__name__}: {e}")

        logger.info(f"✅ 调度任务已热重载: 共 {rescheduled} 个频道")
    except Exception as e:
        logger.error(f"❌ 调度任务热重载失败: {type(e).__name__}: {e}", exc_info=True)


_config_reload_subscribed = False


async def setup_config_reload(event_bus):
    """设置配置热重载，订阅 ConfigChangedEvent

    Args:
        event_bus: AsyncIOEventBus 实例
    """
    global _config_reload_subscribed

    if _config_reload_subscribed:
        logger.debug("配置热重载已设置，跳过重复订阅")
        return

    from core.config.events import ConfigChangedEvent

    async def on_config_changed(event: ConfigChangedEvent):
        """配置变更处理 - 全量配置热重载"""
        try:
            config = event.config
            global \
                CHANNELS, \
                SEND_REPORT_TO_SOURCE, \
                ENABLE_POLL, \
                POLL_REGEN_THRESHOLD, \
                ENABLE_VOTE_REGEN_REQUEST, \
                SUMMARY_SCHEDULES, \
                CHANNEL_POLL_SETTINGS, \
                ENABLE_AUTO_POLL, \
                CHANNEL_AUTO_POLL_SETTINGS, \
                POLL_PUBLIC_VOTERS, \
                LLM_API_KEY, \
                LLM_BASE_URL, \
                LLM_MODEL

            changes = []

            # ==================== 基础配置 ====================

            # 更新频道列表
            if "channels" in event.changed_fields or not event.changed_fields:
                config_channels = config.get("channels")
                if config_channels and isinstance(config_channels, list):
                    old_channels = CHANNELS.copy()
                    CHANNELS = config_channels
                    changes.append(f"channels: {len(old_channels)} → {len(CHANNELS)} 个频道")

            # 更新是否将报告发送回源频道的配置
            if "send_report_to_source" in event.changed_fields or not event.changed_fields:
                if "send_report_to_source" in config:
                    old_value = SEND_REPORT_TO_SOURCE
                    SEND_REPORT_TO_SOURCE = _parse_bool(config["send_report_to_source"])
                    changes.append(f"send_report_to_source: {old_value} → {SEND_REPORT_TO_SOURCE}")

            # ==================== 投票配置 ====================

            # 更新是否启用投票功能的配置
            if "enable_poll" in event.changed_fields or not event.changed_fields:
                if "enable_poll" in config:
                    old_value = ENABLE_POLL
                    ENABLE_POLL = _parse_bool(config["enable_poll"])
                    changes.append(f"enable_poll: {old_value} → {ENABLE_POLL}")

            # 更新投票重新生成请求配置
            if "poll_regen_threshold" in event.changed_fields or not event.changed_fields:
                if "poll_regen_threshold" in config:
                    old_value = POLL_REGEN_THRESHOLD
                    POLL_REGEN_THRESHOLD = config["poll_regen_threshold"]
                    changes.append(f"poll_regen_threshold: {old_value} → {POLL_REGEN_THRESHOLD}")

            if "enable_vote_regen_request" in event.changed_fields or not event.changed_fields:
                if "enable_vote_regen_request" in config:
                    old_value = ENABLE_VOTE_REGEN_REQUEST
                    ENABLE_VOTE_REGEN_REQUEST = _parse_bool(config["enable_vote_regen_request"])
                    changes.append(
                        f"enable_vote_regen_request: {old_value} → {ENABLE_VOTE_REGEN_REQUEST}"
                    )

            # 更新投票公开配置
            if "public_voters" in event.changed_fields or not event.changed_fields:
                if "public_voters" in config:
                    old_value = POLL_PUBLIC_VOTERS
                    POLL_PUBLIC_VOTERS = _parse_bool(config["public_voters"])
                    changes.append(f"public_voters: {old_value} → {POLL_PUBLIC_VOTERS}")

            # ==================== 调度配置 ====================

            # 更新频道级时间配置
            if "summary_schedules" in str(event.changed_fields) or not event.changed_fields:
                summary_schedules_config = config.get("summary_schedules", {})
                if isinstance(summary_schedules_config, dict):
                    old_count = len(SUMMARY_SCHEDULES)
                    SUMMARY_SCHEDULES = summary_schedules_config
                    changes.append(
                        f"summary_schedules: {old_count} → {len(SUMMARY_SCHEDULES)} 个频道"
                    )

                    # 重新初始化调度器任务（热重载调度时间）
                    await _reschedule_summary_jobs(event)

            # ==================== 投票频道配置 ====================

            # 更新频道级投票配置
            if "channel_poll_settings" in str(event.changed_fields) or not event.changed_fields:
                channel_poll_config = config.get("channel_poll_settings", {})
                if isinstance(channel_poll_config, dict):
                    old_count = len(CHANNEL_POLL_SETTINGS)
                    CHANNEL_POLL_SETTINGS = channel_poll_config
                    changes.append(
                        f"channel_poll_settings: {old_count} → {len(CHANNEL_POLL_SETTINGS)} 个频道"
                    )

            # ==================== 自动趣味投票配置 ====================

            # 更新自动趣味投票全局开关
            if "enable_auto_poll" in event.changed_fields or not event.changed_fields:
                if "enable_auto_poll" in config:
                    old_value = ENABLE_AUTO_POLL
                    ENABLE_AUTO_POLL = _parse_bool(config["enable_auto_poll"])
                    changes.append(f"enable_auto_poll: {old_value} → {ENABLE_AUTO_POLL}")

            # 更新频道级自动趣味投票配置
            if (
                "channel_auto_poll_settings" in str(event.changed_fields)
                or not event.changed_fields
            ):
                auto_poll_config = config.get("channel_auto_poll_settings", {})
                if isinstance(auto_poll_config, dict):
                    old_count = len(CHANNEL_AUTO_POLL_SETTINGS)
                    CHANNEL_AUTO_POLL_SETTINGS = auto_poll_config
                    changes.append(
                        f"channel_auto_poll_settings: {old_count} → "
                        f"{len(CHANNEL_AUTO_POLL_SETTINGS)} 个频道"
                    )

            # ==================== 日志级别 ====================
            # 日志级别热重载由 SystemConfigManager.on_config_updated() 统一处理
            # （更新根 logger + 恢复第三方库抑制），此处仅记录变更

            if "log_level" in event.changed_fields or not event.changed_fields:
                if "log_level" in config:
                    new_level_str = config["log_level"]
                    new_level = get_log_level(new_level_str)
                    old_level = logging.getLogger().level
                    changes.append(
                        f"log_level: {logging.getLevelName(old_level)} → "
                        f"{logging.getLevelName(new_level)}"
                    )

            # ==================== 语言配置 ====================

            if "language" in event.changed_fields or not event.changed_fields:
                if "language" in config:
                    new_lang = config["language"]
                    old_lang = i18n.get_language()
                    if new_lang != old_lang:
                        i18n.set_language(new_lang)
                        changes.append(f"language: {old_lang} → {new_lang}")

            # ==================== AI 配置 ====================

            if "api_key" in event.changed_fields or not event.changed_fields:
                new_key = config.get("api_key")
                if new_key:
                    LLM_API_KEY = new_key
                    changes.append("api_key: 已更新")

            if "base_url" in event.changed_fields or not event.changed_fields:
                new_url = config.get("base_url")
                if new_url:
                    LLM_BASE_URL = new_url
                    changes.append(f"base_url: → {new_url}")

            if "model" in event.changed_fields or not event.changed_fields:
                new_model = config.get("model")
                if new_model:
                    LLM_MODEL = new_model
                    changes.append(f"model: → {new_model}")

            # AI 配置变更时，重建 AI 客户端实例
            ai_fields = {"api_key", "base_url"}
            if ai_fields & (event.changed_fields or set()) or not event.changed_fields:
                current_key = config.get("api_key") or LLM_API_KEY
                current_url = config.get("base_url") or LLM_BASE_URL
                if current_key and current_url:
                    try:
                        from core.ai.ai_client import reinitialize_ai_clients

                        reinitialize_ai_clients(current_key, current_url)
                        changes.append("ai_client: 已重新初始化")
                    except Exception as e:
                        logger.error(f"AI 客户端热重载失败: {type(e).__name__}: {e}")

            # ==================== 评论区欢迎配置 ====================
            # comment_welcome 和 channel_comment_welcome 配置通过
            # channel_comment_welcome_config.py 的 get_channel_comment_welcome_config()
            # 每次调用时从 load_config() 读取最新配置，无需额外处理。
            # 仅记录变更日志。
            if any(
                f.startswith("comment_welcome") or f.startswith("channel_comment_welcome")
                for f in (event.changed_fields or [])
            ):
                changes.append("comment_welcome: 配置已变更，将在下次触发时生效")

            if changes:
                logger.info(f"✅ 全局配置变量已热重载: 版本={event.version}, {', '.join(changes)}")
            else:
                logger.debug("全局配置变量已检查，无相关变更")

        except Exception as e:
            logger.error(f"❌ 处理全局配置变量更新失败: {type(e).__name__}: {e}", exc_info=True)

    await event_bus.subscribe(
        ConfigChangedEvent,
        on_config_changed,
        priority=event_bus.PRIORITY_HIGH,
    )
    _config_reload_subscribed = True
    logger.info("✅ 全局配置变量热重载已订阅")
