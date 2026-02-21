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

"""基于 Pydantic 的配置管理

此模块使用 Pydantic Settings 来管理环境变量配置，提供类型验证和默认值。
"""

import logging
from pathlib import Path

from dotenv import load_dotenv
from pydantic import Field, field_validator
from pydantic_settings import BaseSettings

from .constants import (
    BOT_STATE_RUNNING,
    DEFAULT_LLM_BASE_URL,
    DEFAULT_LLM_MODEL,
    DEFAULT_LOG_LEVEL,
    POLL_REGEN_THRESHOLD_DEFAULT,
    VALID_BOT_STATES,
)

# 加载 .env 文件
env_path = Path(__file__).parent.parent / "data" / ".env"
load_dotenv(env_path, override=True)  # 使用 override=True 确保 .env 文件的值会覆盖系统环境变量

logger = logging.getLogger(__name__)


class TelegramSettings(BaseSettings):
    """Telegram 相关配置"""

    api_id: int | None = Field(default=None, alias="TELEGRAM_API_ID")
    api_hash: str | None = Field(default=None, alias="TELEGRAM_API_HASH")
    bot_token: str | None = Field(default=None, alias="TELEGRAM_BOT_TOKEN")

    @field_validator("api_id")
    @classmethod
    def validate_api_id(cls, v: int | None) -> int | None:
        if v is not None and v <= 0:
            raise ValueError("API_ID 必须为正整数")
        return v

    class Config:
        # 移除 env_file 配置，使用全局的 load_dotenv
        extra = "ignore"


class AISettings(BaseSettings):
    """AI 服务相关配置"""

    api_key: str | None = Field(default=None, alias="LLM_API_KEY")
    deepseek_api_key: str | None = Field(default=None, alias="DEEPSEEK_API_KEY")
    base_url: str = Field(default=DEFAULT_LLM_BASE_URL, alias="LLM_BASE_URL")
    model: str = Field(default=DEFAULT_LLM_MODEL, alias="LLM_MODEL")

    @property
    def effective_api_key(self) -> str | None:
        """获取有效的 API Key（优先使用 LLM_API_KEY）"""
        return self.api_key or self.deepseek_api_key

    class Config:
        # 移除 env_file 配置，使用全局的 load_dotenv
        extra = "ignore"


class ChannelSettings(BaseSettings):
    """频道相关配置"""

    target_channel: str | None = Field(default=None, alias="TARGET_CHANNEL")

    @property
    def channels(self) -> list[str]:
        """获取频道列表"""
        if self.target_channel:
            return [ch.strip() for ch in self.target_channel.split(",") if ch.strip()]
        return []

    class Config:
        # 移除 env_file 配置，使用全局的 load_dotenv
        extra = "ignore"


class AdminSettings(BaseSettings):
    """管理员相关配置"""

    report_admin_ids: str = Field(default="", alias="REPORT_ADMIN_IDS")

    @property
    def admin_list(self) -> list[int | str]:
        """获取管理员 ID 列表"""
        if self.report_admin_ids:
            try:
                return [int(admin_id.strip()) for admin_id in self.report_admin_ids.split(",")]
            except ValueError:
                logger.warning("管理员 ID 格式错误，使用默认值 'me'")
        return ["me"]

    class Config:
        # 移除 env_file 配置，使用全局的 load_dotenv
        extra = "ignore"


class LogSettings(BaseSettings):
    """日志相关配置"""

    log_level: str = Field(default=DEFAULT_LOG_LEVEL, alias="LOG_LEVEL")

    @field_validator("log_level")
    @classmethod
    def validate_log_level(cls, v: str) -> str:
        valid_levels = ["DEBUG", "INFO", "WARNING", "ERROR", "CRITICAL"]
        v_upper = v.upper()
        if v_upper not in valid_levels:
            logger.warning(f"无效的日志级别: {v}，使用默认值 {DEFAULT_LOG_LEVEL}")
            return DEFAULT_LOG_LEVEL
        return v_upper

    @property
    def logging_level(self) -> int:
        """获取 logging 模块的日志级别"""
        level_map = {"DEBUG": 10, "INFO": 20, "WARNING": 30, "ERROR": 40, "CRITICAL": 50}
        return level_map.get(self.log_level, 10)

    class Config:
        # 移除 env_file 配置，使用全局的 load_dotenv
        extra = "ignore"


class PollSettings(BaseSettings):
    """投票相关配置"""

    enable_poll: bool = Field(default=True, alias="ENABLE_POLL")
    poll_regen_threshold: int = Field(
        default=POLL_REGEN_THRESHOLD_DEFAULT, alias="POLL_REGEN_THRESHOLD"
    )
    enable_vote_regen_request: bool = Field(default=True, alias="ENABLE_VOTE_REGEN_REQUEST")

    @field_validator("poll_regen_threshold")
    @classmethod
    def validate_threshold(cls, v: int) -> int:
        if v < 1:
            raise ValueError("投票重新生成阈值必须 >= 1")
        return v

    class Config:
        # 移除 env_file 配置，使用全局的 load_dotenv
        extra = "ignore"


class DatabaseSettings(BaseSettings):
    """数据库相关配置"""

    database_type: str = Field(default="sqlite", alias="DATABASE_TYPE")

    # MySQL 配置
    mysql_host: str = Field(default="localhost", alias="MYSQL_HOST")
    mysql_port: int = Field(default=3306, alias="MYSQL_PORT")
    mysql_user: str = Field(default="root", alias="MYSQL_USER")
    mysql_password: str = Field(default="", alias="MYSQL_PASSWORD")
    mysql_database: str = Field(default="sakura_bot_db", alias="MYSQL_DATABASE")
    mysql_charset: str = Field(default="utf8mb4", alias="MYSQL_CHARSET")

    # 连接池配置
    mysql_pool_size: int = Field(default=5, alias="MYSQL_POOL_SIZE")
    mysql_max_overflow: int = Field(default=10, alias="MYSQL_MAX_OVERFLOW")
    mysql_pool_timeout: int = Field(default=30, alias="MYSQL_POOL_TIMEOUT")

    @field_validator("database_type")
    @classmethod
    def validate_database_type(cls, v: str) -> str:
        valid_types = ["sqlite", "mysql"]
        v_lower = v.lower()
        if v_lower not in valid_types:
            logger.warning(f"无效的数据库类型: {v}，使用默认值 sqlite")
            return "sqlite"
        return v_lower

    class Config:
        # 移除 env_file 配置，使用全局的 load_dotenv
        extra = "ignore"


class Settings:
    """主配置类，聚合所有子配置"""

    def __init__(self):
        """初始化所有子配置"""
        self.telegram = TelegramSettings()
        self.ai = AISettings()
        self.channel = ChannelSettings()
        self.admin = AdminSettings()
        self.log = LogSettings()
        self.poll = PollSettings()
        self.database = DatabaseSettings()

        # 其他配置
        self.send_report_to_source = True
        self.bot_state = BOT_STATE_RUNNING


# 全局配置实例
_settings: Settings | None = None


def get_settings() -> Settings:
    """获取全局配置实例（单例模式）"""
    global _settings
    if _settings is None:
        _settings = Settings()
        logger.info("配置已初始化")
    return _settings


def reload_settings() -> Settings:
    """重新加载配置"""
    global _settings
    _settings = Settings()
    logger.info("配置已重新加载")
    return _settings


# 便捷属性访问函数
def get_api_id() -> int | None:
    """获取 Telegram API ID"""
    return get_settings().telegram.api_id


def get_api_hash() -> str | None:
    """获取 Telegram API Hash"""
    return get_settings().telegram.api_hash


def get_bot_token() -> str | None:
    """获取 Bot Token"""
    return get_settings().telegram.bot_token


def get_llm_api_key() -> str | None:
    """获取 LLM API Key"""
    return get_settings().ai.effective_api_key


def get_llm_base_url() -> str:
    """获取 LLM Base URL"""
    return get_settings().ai.base_url


def get_llm_model() -> str:
    """获取 LLM 模型名称"""
    return get_settings().ai.model


def get_channels() -> list[str]:
    """获取频道列表"""
    return get_settings().channel.channels


def get_admin_list() -> list[int | str]:
    """获取管理员列表"""
    return get_settings().admin.admin_list


def get_log_level() -> str:
    """获取日志级别"""
    return get_settings().log.log_level


def is_poll_enabled() -> bool:
    """检查是否启用投票功能"""
    return get_settings().poll.enable_poll


def get_poll_regen_threshold() -> int:
    """获取投票重新生成阈值"""
    return get_settings().poll.poll_regen_threshold


def is_vote_regen_request_enabled() -> bool:
    """检查是否启用投票重新生成请求功能"""
    return get_settings().poll.enable_vote_regen_request


def get_bot_state() -> str:
    """获取机器人状态"""
    return get_settings().bot_state


def set_bot_state(state: str) -> None:
    """设置机器人状态"""
    if state not in VALID_BOT_STATES:
        raise ValueError(f"无效的机器人状态: {state}，有效值: {VALID_BOT_STATES}")
    settings = get_settings()
    settings.bot_state = state
    logger.info(f"机器人状态已更新为: {state}")


def get_send_report_to_source() -> bool:
    """获取是否将报告发送回源频道"""
    return get_settings().send_report_to_source


def set_send_report_to_source(value: bool) -> None:
    """设置是否将报告发送回源频道"""
    settings = get_settings()
    settings.send_report_to_source = value
    logger.info(f"发送报告到源频道的配置已更新为: {value}")


# 验证必要配置
def validate_required_settings() -> tuple[bool, list[str]]:
    """验证必要的配置项是否存在

    Returns:
        tuple: (是否所有配置都存在, 缺失的配置项列表)
    """
    settings = get_settings()
    missing = []

    if not settings.telegram.api_id:
        missing.append("TELEGRAM_API_ID")
    if not settings.telegram.api_hash:
        missing.append("TELEGRAM_API_HASH")
    if not settings.telegram.bot_token:
        missing.append("TELEGRAM_BOT_TOKEN")
    if not settings.ai.effective_api_key:
        missing.append("LLM_API_KEY 或 DEEPSEEK_API_KEY")

    return len(missing) == 0, missing
