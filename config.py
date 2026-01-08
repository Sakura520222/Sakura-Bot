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