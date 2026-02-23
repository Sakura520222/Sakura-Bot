"""测试 States 模块

Copyright 2026 Sakura-Bot
本项目采用 AGPL-3.0 许可
"""

import pytest

from core.states import UserContext, get_user_context


@pytest.mark.unit
class TestUserContext:
    """用户上下文管理器测试"""

    def test_is_waiting_for_prompt_initially_false(self):
        """测试初始状态下用户不在等待提示词"""
        context = UserContext()

        assert not context.is_waiting_for_prompt(123)

    def test_start_and_end_setting_prompt(self):
        """测试开始和结束设置提示词"""
        context = UserContext()

        context.start_setting_prompt(123)
        assert context.is_waiting_for_prompt(123)

        context.end_setting_prompt(123)
        assert not context.is_waiting_for_prompt(123)

    def test_is_waiting_for_poll_prompt_initially_false(self):
        """测试初始状态下用户不在等待投票提示词"""
        context = UserContext()

        assert not context.is_waiting_for_poll_prompt(123)

    def test_start_and_end_setting_poll_prompt(self):
        """测试开始和结束设置投票提示词"""
        context = UserContext()

        context.start_setting_poll_prompt(123)
        assert context.is_waiting_for_poll_prompt(123)

        context.end_setting_poll_prompt(123)
        assert not context.is_waiting_for_poll_prompt(123)

    def test_is_setting_ai_config_initially_false(self):
        """测试初始状态下用户不在设置AI配置"""
        context = UserContext()

        assert not context.is_setting_ai_config(123)

    def test_start_and_end_setting_ai_config(self):
        """测试开始和结束设置AI配置"""
        context = UserContext()

        context.start_setting_ai_config(123)
        assert context.is_setting_ai_config(123)

        context.end_setting_ai_config(123)
        assert not context.is_setting_ai_config(123)

    def test_get_ai_config_initially_none(self):
        """测试初始AI配置为空"""
        context = UserContext()

        config = context.get_ai_config(123)

        assert config == {"api_key": None, "base_url": None, "model": None}

    def test_update_ai_config(self):
        """测试更新AI配置"""
        context = UserContext()

        context.update_ai_config(123, "api_key", "test_key")
        config = context.get_ai_config(123)

        assert config["api_key"] == "test_key"
        assert config["base_url"] is None
        assert config["model"] is None

    def test_update_multiple_ai_config_fields(self):
        """测试更新多个AI配置字段"""
        context = UserContext()

        context.update_ai_config(123, "api_key", "key1")
        context.update_ai_config(123, "base_url", "url1")
        context.update_ai_config(123, "model", "model1")

        config = context.get_ai_config(123)

        assert config == {"api_key": "key1", "base_url": "url1", "model": "model1"}

    def test_start_setting_ai_config_initializes_config(self):
        """测试开始设置AI配置时初始化配置"""
        context = UserContext()

        context.start_setting_ai_config(123)
        config = context.get_ai_config(123)

        assert config == {"api_key": None, "base_url": None, "model": None}

    def test_end_setting_ai_config_clears_config(self):
        """测试结束设置AI配置时清理配置"""
        context = UserContext()

        context.start_setting_ai_config(123)
        context.update_ai_config(123, "api_key", "test_key")
        context.end_setting_ai_config(123)

        config = context.get_ai_config(123)

        assert config == {"api_key": None, "base_url": None, "model": None}

    def test_multiple_users_independent_states(self):
        """测试多个用户状态独立"""
        context = UserContext()

        context.start_setting_prompt(123)
        context.start_setting_prompt(456)

        context.end_setting_prompt(123)

        assert not context.is_waiting_for_prompt(123)
        assert context.is_waiting_for_prompt(456)

    def test_prompt_and_poll_prompt_independent(self):
        """测试提示词和投票提示词状态独立"""
        context = UserContext()

        context.start_setting_prompt(123)
        context.start_setting_poll_prompt(123)

        assert context.is_waiting_for_prompt(123)
        assert context.is_waiting_for_poll_prompt(123)

        context.end_setting_prompt(123)

        assert not context.is_waiting_for_prompt(123)
        assert context.is_waiting_for_poll_prompt(123)

    def test_get_ai_config_for_nonexistent_user(self):
        """测试获取不存在用户的AI配置"""
        context = UserContext()

        config = context.get_ai_config(123)

        assert config["api_key"] is None
        assert config["base_url"] is None
        assert config["model"] is None


@pytest.mark.unit
class TestGetUserContext:
    """获取用户上下文实例测试"""

    def test_singleton(self):
        """测试单例模式"""
        context1 = get_user_context()
        context2 = get_user_context()

        assert context1 is context2

    def test_returns_user_context_instance(self):
        """测试返回UserContext实例"""
        context = get_user_context()

        assert isinstance(context, UserContext)


if __name__ == "__main__":
    pytest.main([__file__, "-v"])
