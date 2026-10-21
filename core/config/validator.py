# core/config/validator.py
import json
import logging
from pathlib import Path

from core.config.events import ConfigValidationErrorEvent

logger = logging.getLogger(__name__)


class ConfigValidator:
    """配置文件验证器（增强版）"""

    # 有效的日志级别
    VALID_LOG_LEVELS = {"DEBUG", "INFO", "WARNING", "ERROR", "CRITICAL"}

    # 有效的频率值
    VALID_FREQUENCIES = {"daily", "weekly"}

    # 有效的星期几
    VALID_DAYS = {"mon", "tue", "wed", "thu", "fri", "sat", "sun"}

    @staticmethod
    def validate_config_file(
        file_path: Path,
    ) -> tuple[bool, ConfigValidationErrorEvent | None, dict | None]:
        """
        验证配置文件（JSON格式 + 业务逻辑）

        Returns:
            (成功, 错误事件, 配置字典)
        """
        try:
            if not file_path.exists():
                error_event = ConfigValidationErrorEvent(
                    error=f"配置文件不存在: {file_path}", config_path=str(file_path)
                )
                return False, error_event, None

            with open(file_path, encoding="utf-8") as f:
                config_dict = json.load(f)

            # 业务逻辑验证
            validation_error = ConfigValidator.validate_config(config_dict, file_path)
            if validation_error:
                return False, validation_error, None

            return True, None, config_dict

        except json.JSONDecodeError as e:
            # JSON解析错误，提取行号和列号
            error_event = ConfigValidationErrorEvent(
                error=f"JSON格式错误: {e.msg}",
                config_path=str(file_path),
                error_line=e.lineno,
                error_column=e.colno,
            )
            return False, error_event, None

        except Exception:
            error_event = ConfigValidationErrorEvent(
                error="读取配置文件失败", config_path=str(file_path)
            )
            return False, error_event, None

    @staticmethod
    def validate_config(config: dict, file_path: Path) -> ConfigValidationErrorEvent | None:
        """文件读取与写入候选共用的校验，不在错误中包含配置值。"""
        if not isinstance(config, dict):
            return ConfigValidationErrorEvent(
                error="配置根节点必须是对象", config_path=str(file_path)
            )
        for field in ("api_key", "base_url", "model"):
            if field in config and config[field] is not None and not isinstance(config[field], str):
                return ConfigValidationErrorEvent(
                    error=f"{field} 必须是字符串或 null", config_path=str(file_path)
                )
        if "channels" in config and (
            not isinstance(config["channels"], list)
            or any(not isinstance(channel, str) for channel in config["channels"])
        ):
            return ConfigValidationErrorEvent(
                error="channels 必须是字符串数组", config_path=str(file_path)
            )
        if "sakura_guard" in config:
            from pydantic import ValidationError

            from core.moderation.config import GuardConfig

            try:
                GuardConfig.model_validate(config["sakura_guard"])
            except (ValidationError, ValueError, TypeError):
                return ConfigValidationErrorEvent(
                    error="Sakura Guard 配置无效，请检查群组 ID、规则、动作及超时范围",
                    config_path=str(file_path),
                )
        if "qa_bot_quota" in config:
            from pydantic import ValidationError

            from core.config.qa_quota import QAQuotaSettings

            try:
                QAQuotaSettings.model_validate(config["qa_bot_quota"])
            except (ValidationError, ValueError, TypeError):
                return ConfigValidationErrorEvent(
                    error="助手 Bot 配额必须为整数，每人每日不少于 1 次、全站每日不少于 10 次",
                    config_path=str(file_path),
                )
        return ConfigValidator._validate_business_logic(config, file_path)

    @staticmethod
    def validate_transition(
        previous: dict, config: dict, file_path: Path
    ) -> ConfigValidationErrorEvent | None:
        """阻断 AI 草稿覆盖和异常大量顶层字段删除，允许普通的显式字段删除。"""
        removed = previous.keys() - config.keys()
        ai_fields = {"api_key", "base_url", "model"}
        if (removed and config.keys() <= ai_fields and previous.keys() - ai_fields) or (
            len(removed) >= 3 and len(removed) * 2 >= len(previous)
        ):
            return ConfigValidationErrorEvent(
                error="检测到异常大量配置字段删除，已拒绝更新；请从完整有效备份恢复",
                config_path=str(file_path),
            )
        return None

    @staticmethod
    def _validate_business_logic(
        config: dict, file_path: Path
    ) -> ConfigValidationErrorEvent | None:
        """验证配置的业务逻辑"""

        # 1. 验证 log_level
        if "log_level" in config:
            log_level = config["log_level"]
            if (
                not isinstance(log_level, str)
                or log_level.upper() not in ConfigValidator.VALID_LOG_LEVELS
            ):
                return ConfigValidationErrorEvent(
                    error="log_level 必须是有效的日志级别",
                    config_path=str(file_path),
                )

        # 2. 验证 summary_schedules
        if "summary_schedules" in config:
            schedules = config["summary_schedules"]
            if not isinstance(schedules, dict):
                return ConfigValidationErrorEvent(
                    error="summary_schedules 必须是对象类型", config_path=str(file_path)
                )

            for channel, schedule in schedules.items():
                error = ConfigValidator._validate_schedule(schedule, channel, file_path)
                if error:
                    return error

        # 3. 验证 channel_poll_settings
        if "channel_poll_settings" in config:
            poll_settings = config["channel_poll_settings"]
            if not isinstance(poll_settings, dict):
                return ConfigValidationErrorEvent(
                    error="channel_poll_settings 必须是对象类型", config_path=str(file_path)
                )

        # 4. 验证 forwarding 配置
        if "forwarding" in config:
            forwarding = config["forwarding"]
            if not isinstance(forwarding, dict):
                return ConfigValidationErrorEvent(
                    error="forwarding 必须是对象类型", config_path=str(file_path)
                )

        # 5. 验证 channel_auto_poll_settings
        if "channel_auto_poll_settings" in config:
            auto_poll_settings = config["channel_auto_poll_settings"]
            if not isinstance(auto_poll_settings, dict):
                return ConfigValidationErrorEvent(
                    error="channel_auto_poll_settings 必须是对象类型", config_path=str(file_path)
                )

        return None

    @staticmethod
    def _validate_schedule(
        schedule: dict, channel: str, file_path: Path
    ) -> ConfigValidationErrorEvent | None:
        """验证单个时间配置"""
        if not isinstance(schedule, dict):
            return ConfigValidationErrorEvent(
                error="频道时间配置必须是对象类型", config_path=str(file_path)
            )

        # 验证 frequency
        frequency = schedule.get("frequency")
        if frequency and frequency not in ConfigValidator.VALID_FREQUENCIES:
            return ConfigValidationErrorEvent(
                error="时间配置的 frequency 必须是 daily 或 weekly",
                config_path=str(file_path),
            )

        # 验证时间范围
        hour = schedule.get("hour")
        if hour is not None and (not isinstance(hour, int) or hour < 0 or hour > 23):
            return ConfigValidationErrorEvent(
                error="时间配置的 hour 必须是 0-23 的整数", config_path=str(file_path)
            )

        minute = schedule.get("minute")
        if minute is not None and (not isinstance(minute, int) or minute < 0 or minute > 59):
            return ConfigValidationErrorEvent(
                error="时间配置的 minute 必须是 0-59 的整数", config_path=str(file_path)
            )

        return None
