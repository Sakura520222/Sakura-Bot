"""
用户上下文状态管理模块
集中管理所有用户交互状态，避免循环导入
"""


class UserContext:
    """用户上下文管理器，用于跟踪用户的交互状态"""

    def __init__(self):
        # 正在设置提示词的用户
        self._setting_prompt_users: set[int] = set()
        # 正在设置投票提示词的用户
        self._setting_poll_prompt_users: set[int] = set()
        # 正在设置AI配置的用户
        self._setting_ai_config_users: set[int] = set()
        # 配置中的AI参数
        self._current_ai_config: dict[int, dict[str, str | None]] = {}
        self._skipped_ai_config: dict[int, set[str]] = {}

    def is_waiting_for_prompt(self, user_id: int) -> bool:
        """检查用户是否在等待提示词输入"""
        return user_id in self._setting_prompt_users

    def start_setting_prompt(self, user_id: int):
        """开始设置提示词"""
        self._setting_prompt_users.add(user_id)

    def end_setting_prompt(self, user_id: int):
        """结束设置提示词"""
        self._setting_prompt_users.discard(user_id)

    def is_waiting_for_poll_prompt(self, user_id: int) -> bool:
        """检查用户是否在等待投票提示词输入"""
        return user_id in self._setting_poll_prompt_users

    def start_setting_poll_prompt(self, user_id: int):
        """开始设置投票提示词"""
        self._setting_poll_prompt_users.add(user_id)

    def end_setting_poll_prompt(self, user_id: int):
        """结束设置投票提示词"""
        self._setting_poll_prompt_users.discard(user_id)

    def is_setting_ai_config(self, user_id: int) -> bool:
        """检查用户是否正在设置AI配置"""
        return user_id in self._setting_ai_config_users

    def start_setting_ai_config(self, user_id: int):
        """开始设置AI配置"""
        self.end_setting_prompt(user_id)
        self.end_setting_poll_prompt(user_id)
        self._setting_ai_config_users.add(user_id)
        # 初始化当前配置
        self._current_ai_config[user_id] = {"api_key": None, "base_url": None, "model": None}
        self._skipped_ai_config[user_id] = set()

    def end_setting_ai_config(self, user_id: int):
        """结束设置AI配置"""
        self._setting_ai_config_users.discard(user_id)
        self._skipped_ai_config.pop(user_id, None)
        # 清理配置数据
        if user_id in self._current_ai_config:
            del self._current_ai_config[user_id]

    def get_ai_config(self, user_id: int) -> dict[str, str | None]:
        """获取用户的AI配置"""
        return self._current_ai_config.get(
            user_id, {"api_key": None, "base_url": None, "model": None}
        )

    def update_ai_config(self, user_id: int, key: str, value: str | None):
        """更新用户的AI配置"""
        if user_id not in self._current_ai_config:
            self._current_ai_config[user_id] = {"api_key": None, "base_url": None, "model": None}
        self._current_ai_config[user_id][key] = value
        self._skipped_ai_config.setdefault(user_id, set()).discard(key)

    def skip_ai_config(self, user_id: int, key: str, current_value: str | None):
        """推进会话步骤，但跳过字段不参与最终写入，避免覆盖并发更新。"""
        self.update_ai_config(user_id, key, current_value or "")
        self._skipped_ai_config[user_id].add(key)

    def get_ai_config_updates(self, user_id: int) -> dict[str, str]:
        """只返回明确输入的字段。"""
        skipped = self._skipped_ai_config.get(user_id, set())
        return {
            key: value
            for key, value in self.get_ai_config(user_id).items()
            if value is not None and key not in skipped
        }


# 全局单例
_user_context = UserContext()


def get_user_context() -> UserContext:
    """
    获取用户上下文实例

    Returns:
        UserContext: 全局唯一的用户上下文实例
    """
    return _user_context
