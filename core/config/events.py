# core/config/events.py
from datetime import datetime
from typing import Literal

from pydantic import BaseModel, Field


class ConfigChangedEvent(BaseModel):
    """配置变更成功事件"""

    config: dict = Field(repr=False)  # 内部消费者需要完整配置，禁止在事件 repr 中暴露凭据
    version: int
    old_config: dict | None = Field(default=None, repr=False)
    changed_fields: set[str] = Field(default_factory=set)
    timestamp: float = Field(default_factory=lambda: datetime.now().timestamp())

    @staticmethod
    def calculate_changed_fields(old_config: dict | None, new_config: dict) -> set[str]:
        """计算新旧配置的差异字段

        Args:
            old_config: 旧配置字典
            new_config: 新配置字典

        Returns:
            发生变化的字段名集合
        """
        if not old_config:
            return set()

        changed_fields = set()
        all_keys = set(old_config.keys()) | set(new_config.keys())

        for key in all_keys:
            old_val = old_config.get(key)
            new_val = new_config.get(key)

            # 类型不同
            if type(old_val) is not type(new_val):
                changed_fields.add(key)
            # 嵌套字典比较
            elif isinstance(old_val, dict) and isinstance(new_val, dict):
                nested_changes = ConfigChangedEvent._compare_nested_dicts(
                    f"{key}.", old_val, new_val
                )
                changed_fields.update(nested_changes)
            # 列表比较
            elif isinstance(old_val, list) and isinstance(new_val, list):
                if old_val != new_val:
                    changed_fields.add(key)
            # 值比较
            elif old_val != new_val:
                changed_fields.add(key)

        return changed_fields

    @staticmethod
    def _compare_nested_dicts(prefix: str, old_dict: dict, new_dict: dict) -> set[str]:
        """比较嵌套字典的差异

        Args:
            prefix: 字段前缀（用于嵌套字段）
            old_dict: 旧字典
            new_dict: 新字典

        Returns:
            发生变化的嵌套字段名集合
        """
        changed = set()
        all_keys = set(old_dict.keys()) | set(new_dict.keys())

        for key in all_keys:
            old_val = old_dict.get(key)
            new_val = new_dict.get(key)

            if type(old_val) is not type(new_val):
                changed.add(f"{prefix}{key}")
            elif isinstance(old_val, dict) and isinstance(new_val, dict):
                changed.update(
                    ConfigChangedEvent._compare_nested_dicts(f"{prefix}{key}.", old_val, new_val)
                )
            elif isinstance(old_val, list) and isinstance(new_val, list):
                if old_val != new_val:
                    changed.add(f"{prefix}{key}")
            elif old_val != new_val:
                changed.add(f"{prefix}{key}")

        return changed


class PromptChangedEvent(BaseModel):
    """提示词变更成功事件"""

    prompt_type: Literal["summary", "poll", "qa_persona"]  # 提示词类型
    file_path: str  # 文件路径
    content: str | None = None  # 新内容（可选）
    timestamp: float = Field(default_factory=lambda: datetime.now().timestamp())


class ConfigValidationErrorEvent(BaseModel):
    """配置验证失败事件"""

    error: str
    config_path: str
    error_line: int | None = None
    error_column: int | None = None
    timestamp: float = Field(default_factory=lambda: datetime.now().timestamp())
    rolled_back: bool = False  # 是否已自动回滚

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
            f"📝 修复后保存文件，系统将自动重新加载"
        )
