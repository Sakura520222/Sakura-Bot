# Copyright 2026 Sakura-频道总结助手
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

"""项目核心常量定义

此模块集中管理项目中的所有常量，避免魔法数字和硬编码字符串。
"""

# ==================== 文件路径常量 ====================
DATA_DIR = "data"
SESSIONS_DIR = "data/sessions"
PROMPT_FILE = "data/prompt.txt"
POLL_PROMPT_FILE = "data/poll_prompt.txt"
CONFIG_FILE = "data/config.json"
RESTART_FLAG_FILE = "data/.restart_flag"
LAST_SUMMARY_FILE = "data/.last_summary_time.json"
POLL_REGENERATIONS_FILE = "data/.poll_regenerations.json"
DATABASE_FILE = "data/bot.db"

# ==================== 投票相关常量 ====================
# Telegram 投票限制
POLL_QUESTION_MAX_LENGTH = 255
POLL_OPTION_MAX_LENGTH = 100
POLL_MIN_OPTIONS = 2
POLL_MAX_OPTIONS = 10

# 投票重新生成配置
POLL_REGEN_THRESHOLD_DEFAULT = 5
CLEANUP_DAYS_DEFAULT = 30

# ==================== 时间配置常量 ====================
# 默认自动总结时间
DEFAULT_SUMMARY_DAY = 'mon'  # 星期几：mon, tue, wed, thu, fri, sat, sun
DEFAULT_SUMMARY_HOUR = 9  # 小时：0-23
DEFAULT_SUMMARY_MINUTE = 0  # 分钟：0-59

# 有效的星期几
VALID_DAYS = ['mon', 'tue', 'wed', 'thu', 'fri', 'sat', 'sun']

# 有效的频率类型
VALID_FREQUENCIES = ['daily', 'weekly']

# ==================== 机器人状态常量 ====================
BOT_STATE_RUNNING = "running"
BOT_STATE_PAUSED = "paused"
BOT_STATE_SHUTTING_DOWN = "shutting_down"
VALID_BOT_STATES = [BOT_STATE_RUNNING, BOT_STATE_PAUSED, BOT_STATE_SHUTTING_DOWN]

# ==================== 日志配置常量 ====================
DEFAULT_LOG_LEVEL = "DEBUG"
LOG_LEVEL_MAP = {
    'DEBUG': 'DEBUG',
    'INFO': 'INFO',
    'WARNING': 'WARNING',
    'ERROR': 'ERROR',
    'CRITICAL': 'CRITICAL'
}

# ==================== 默认提示词 ====================
DEFAULT_PROMPT = "请总结以下 Telegram 消息，提取核心要点并列出重要消息的链接：\n\n"

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

# ==================== 默认投票配置 ====================
# 注意：默认投票文本已移至 i18n 模块，使用 get_text() 获取
# get_text('poll.default_question')
# get_text('poll.default_options.0') - get_text('poll.default_options.3')

# ==================== AI 配置默认值 ====================
DEFAULT_LLM_BASE_URL = 'https://api.deepseek.com'
DEFAULT_LLM_MODEL = 'deepseek-chat'

# ==================== 重试配置 ====================
RETRY_MAX_ATTEMPTS = 3
RETRY_BASE_DELAY = 1.0
RETRY_MAX_DELAY = 30.0