# core/config/validator.py
import json
import logging
from pathlib import Path

from core.config.events import ConfigValidationErrorEvent

logger = logging.getLogger(__name__)


class ConfigValidator:
    """配置文件验证器"""

    @staticmethod
    def validate_config_file(
        file_path: Path,
    ) -> tuple[bool, ConfigValidationErrorEvent | None, dict | None]:
        """
        验证配置文件

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

            # 基本的JSON格式验证成功
            # 注意：完整的Pydantic验证在ConfigManager中进行
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

        except Exception as e:
            error_event = ConfigValidationErrorEvent(
                error=f"未知错误: {str(e)}", config_path=str(file_path)
            )
            return False, error_event, None
