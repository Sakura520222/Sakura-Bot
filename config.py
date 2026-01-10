import os
import logging
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
PROMPT_FILE = "prompt.txt"
CONFIG_FILE = "config.json"
RESTART_FLAG_FILE = ".restart_flag"
LAST_SUMMARY_FILE = ".last_summary_time.json"

# 默认提示词
DEFAULT_PROMPT = "请总结以下 Telegram 消息，提取核心要点并列出重要消息的链接：\n\n"

# 加载 .env 文件中的变量
load_dotenv()
logger.info("已加载 .env 文件中的环境变量")

# 从环境变量中读取配置
logger.info("开始从环境变量加载配置...")
API_ID = os.getenv('TELEGRAM_API_ID')
API_HASH = os.getenv('TELEGRAM_API_HASH')
BOT_TOKEN = os.getenv('TELEGRAM_BOT_TOKEN')
# AI 配置 - 从环境变量获取默认值
LLM_API_KEY = os.getenv('LLM_API_KEY', os.getenv('DEEPSEEK_API_KEY'))
LLM_BASE_URL = os.getenv('LLM_BASE_URL', 'https://api.deepseek.com')
LLM_MODEL = os.getenv('LLM_MODEL', 'deepseek-chat')
# Web管理界面配置 - 从环境变量获取默认值
WEB_PORT = int(os.getenv('WEB_PORT', '8000'))

# 频道配置 - 从环境变量获取默认值
TARGET_CHANNEL = os.getenv('TARGET_CHANNEL')
CHANNELS = []
if TARGET_CHANNEL:
    # 支持多个频道，用逗号分隔
    CHANNELS = [channel.strip() for channel in TARGET_CHANNEL.split(',')]
    logger.info(f"已从环境变量加载频道配置: {CHANNELS}")

# 是否将报告发送回源频道的配置，默认为True
SEND_REPORT_TO_SOURCE = True

# 日志级别 - 从环境变量获取默认值
LOG_LEVEL_FROM_ENV = os.getenv('LOG_LEVEL')
logger.debug(f"从环境变量读取的日志级别: {LOG_LEVEL_FROM_ENV}")

logger.debug(f"从环境变量加载的配置: API_ID={'***' if API_ID else '未设置'}, API_HASH={'***' if API_HASH else '未设置'}, BOT_TOKEN={'***' if BOT_TOKEN else '未设置'}")
logger.debug(f"AI配置 - 环境变量默认值: Base URL={LLM_BASE_URL}, Model={LLM_MODEL}")
logger.debug(f"Web配置 - 环境变量默认值: Port={WEB_PORT}")
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
    except Exception as e:
        logger.error(f"保存配置到文件 {CONFIG_FILE} 时出错: {type(e).__name__}: {e}", exc_info=True)

# 加载配置文件，覆盖环境变量默认值
logger.info("开始加载配置文件...")
config = load_config()
if config:
    logger.debug(f"从配置文件读取的配置: {config}")
    LLM_API_KEY = config.get('api_key', LLM_API_KEY)
    LLM_BASE_URL = config.get('base_url', LLM_BASE_URL)
    LLM_MODEL = config.get('model', LLM_MODEL)
    
    # 从配置文件读取频道列表
    config_channels = config.get('channels')
    if config_channels and isinstance(config_channels, list):
        CHANNELS = config_channels
        logger.info(f"已从配置文件加载频道列表: {CHANNELS}")
    
    # 从配置文件读取是否将报告发送回源频道的配置
    SEND_REPORT_TO_SOURCE = config.get('send_report_to_source', SEND_REPORT_TO_SOURCE)
    logger.info(f"已从配置文件加载发送报告到源频道的配置: {SEND_REPORT_TO_SOURCE}")
    
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

# 获取频道的时间配置
def get_channel_schedule(channel):
    """获取指定频道的自动总结时间配置
    
    Args:
        channel: 频道URL
        
    Returns:
        dict: 包含day, hour, minute的配置字典
    """
    if channel in SUMMARY_SCHEDULES:
        schedule = SUMMARY_SCHEDULES[channel]
        return {
            'day': schedule.get('day', DEFAULT_SUMMARY_DAY),
            'hour': schedule.get('hour', DEFAULT_SUMMARY_HOUR),
            'minute': schedule.get('minute', DEFAULT_SUMMARY_MINUTE)
        }
    else:
        # 返回默认配置
        return {
            'day': DEFAULT_SUMMARY_DAY,
            'hour': DEFAULT_SUMMARY_HOUR,
            'minute': DEFAULT_SUMMARY_MINUTE
        }

# 设置频道的时间配置
def set_channel_schedule(channel, day=None, hour=None, minute=None):
    """设置指定频道的自动总结时间配置
    
    Args:
        channel: 频道URL
        day: 星期几（mon, tue, wed, thu, fri, sat, sun）
        hour: 小时（0-23）
        minute: 分钟（0-59）
        
    Returns:
        bool: 是否成功保存配置
    """
    try:
        # 加载当前配置
        current_config = load_config()
        
        # 确保summary_schedules字段存在
        if 'summary_schedules' not in current_config:
            current_config['summary_schedules'] = {}
        
        # 获取或创建频道的配置
        if channel not in current_config['summary_schedules']:
            current_config['summary_schedules'][channel] = {}
        
        # 更新配置
        if day is not None:
            current_config['summary_schedules'][channel]['day'] = day
        if hour is not None:
            current_config['summary_schedules'][channel]['hour'] = hour
        if minute is not None:
            current_config['summary_schedules'][channel]['minute'] = minute
        
        # 保存配置
        save_config(current_config)
        
        # 更新内存中的配置
        global SUMMARY_SCHEDULES
        SUMMARY_SCHEDULES = current_config.get('summary_schedules', {})
        
        logger.info(f"已更新频道 {channel} 的时间配置: day={day}, hour={hour}, minute={minute}")
        return True
    except Exception as e:
        logger.error(f"设置频道时间配置时出错: {type(e).__name__}: {e}", exc_info=True)
        return False

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
            
            # 保存配置
            save_config(current_config)
            
            # 更新内存中的配置
            global SUMMARY_SCHEDULES
            SUMMARY_SCHEDULES = current_config.get('summary_schedules', {})
            
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
