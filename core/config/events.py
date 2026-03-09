# core/config/events.py
from datetime import datetime
from typing import TYPE_CHECKING, Optional

from pydantic import BaseModel, Field

if TYPE_CHECKING:
    from .config import BusinessConfig


class ConfigChangedEvent(BaseModel):
    """配置变更成功事件"""

    config: "BusinessConfig"  # 前向引用
    version: int
    old_config: Optional["BusinessConfig"] = None
    changed_fields: set[str] = Field(default_factory=set)
    timestamp: float = Field(default_factory=lambda: datetime.now().timestamp())


class ConfigValidationErrorEvent(BaseModel):
    """配置验证失败事件"""

    error: str
    config_path: str
    error_line: int | None = None
    error_column: int | None = None
    timestamp: float = Field(default_factory=lambda: datetime.now().timestamp())

    def format_error_message(self) -> str:
        """格式化Telegram错误消息"""
        from pathlib import Path

        location = f"第{self.error_line}行" if self.error_line else "未知位置"
        filename = Path(self.config_path).name

        return (
            f"⚠️ *配置重载失败*\n\n"
            f"📁 文件: `{filename}`\n"
            f"📍 位置: {location}\n"
            f"❌ 错误: {self.error}\n"
            f"🕐 时间: {datetime.fromtimestamp(self.timestamp).strftime('%Y-%m-%d %H:%M:%S')}\n\n"
            f"请修复配置文件后点击下方按钮重试"
        )
