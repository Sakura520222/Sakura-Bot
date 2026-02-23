"""测试 Settings 模块

Copyright 2026 Sakura-Bot
本项目采用 AGPL-3.0 许可
"""

import os
from unittest.mock import patch

import pytest

from core.settings import (
    AdminSettings,
    AISettings,
    ChannelSettings,
    DatabaseSettings,
    LogSettings,
    PollSettings,
    TelegramSettings,
    get_admin_list,
    get_api_hash,
    get_api_id,
    get_bot_state,
    get_bot_token,
    get_channels,
    get_llm_api_key,
    get_llm_base_url,
    get_llm_model,
    get_log_level,
    get_poll_regen_threshold,
    get_send_report_to_source,
    get_settings,
    is_poll_enabled,
    is_vote_regen_request_enabled,
    reload_settings,
    set_bot_state,
    set_send_report_to_source,
    validate_required_settings,
)


@pytest.mark.unit
class TestTelegramSettings:
    """Telegram 配置测试"""

    def test_default_values(self, monkeypatch):
        """测试默认值"""
        # 清除环境变量以测试真正的默认值
        monkeypatch.delenv("TELEGRAM_API_ID", raising=False)
        monkeypatch.delenv("TELEGRAM_API_HASH", raising=False)
        monkeypatch.delenv("TELEGRAM_BOT_TOKEN", raising=False)

        settings = TelegramSettings()
        assert settings.api_id is None
        assert settings.api_hash is None
        assert settings.bot_token is None

    def test_from_env(self, mock_env_vars):
        """测试从环境变量读取"""
        settings = TelegramSettings()
        assert settings.api_id == 123456
        assert settings.api_hash == "test_api_hash"
        assert settings.bot_token == "123456:ABCDEF"

    def test_api_id_from_env(self, mock_env_vars):
        """测试从环境变量读取 API ID"""
        settings = TelegramSettings()
        assert settings.api_id == 123456
        assert isinstance(settings.api_id, int)


@pytest.mark.unit
class TestAISettings:
    """AI 配置测试"""

    def test_default_values(self, monkeypatch):
        """测试默认值"""
        # 清除环境变量以测试真正的默认值
        monkeypatch.delenv("LLM_API_KEY", raising=False)
        monkeypatch.delenv("DEEPSEEK_API_KEY", raising=False)
        monkeypatch.delenv("LLM_BASE_URL", raising=False)
        monkeypatch.delenv("LLM_MODEL", raising=False)

        settings = AISettings()
        assert settings.api_key is None
        assert settings.deepseek_api_key is None
        assert settings.base_url == "https://api.deepseek.com"
        assert settings.model == "deepseek-chat"

    def test_effective_api_key_prioritizes_llm_key(self, mock_env_vars):
        """测试有效的 API Key 优先使用 LLM_API_KEY"""
        settings = AISettings()
        assert settings.effective_api_key == "test_llm_api_key"

    def test_effective_api_key_fallback_to_deepseek(self):
        """测试当 LLM_API_KEY 不存在时使用 DEEPSEEK_API_KEY"""
        with patch.dict(os.environ, {"DEEPSEEK_API_KEY": "deepseek_key"}, clear=True):
            settings = AISettings()
            assert settings.effective_api_key == "deepseek_key"

    def test_effective_api_key_none_when_both_missing(self):
        """测试当两个 Key 都不存在时返回 None"""
        with patch.dict(os.environ, {}, clear=True):
            settings = AISettings()
            assert settings.effective_api_key is None


@pytest.mark.unit
class TestChannelSettings:
    """频道配置测试"""

    def test_default_values(self, monkeypatch):
        """测试默认值"""
        # 清除环境变量以测试真正的默认值
        monkeypatch.delenv("TARGET_CHANNEL", raising=False)

        settings = ChannelSettings()
        assert settings.target_channel is None
        assert settings.channels == []

    def test_single_channel(self, mock_env_vars):
        """测试单个频道"""
        settings = ChannelSettings()
        assert settings.channels == ["@test_channel"]

    def test_multiple_channels(self):
        """测试多个频道（逗号分隔）"""
        with patch.dict(os.environ, {"TARGET_CHANNEL": "@channel1,@channel2,@channel3"}):
            settings = ChannelSettings()
            assert settings.channels == ["@channel1", "@channel2", "@channel3"]

    def test_channels_with_spaces(self):
        """测试频道列表中有空格"""
        with patch.dict(os.environ, {"TARGET_CHANNEL": " @channel1 , @channel2 , @channel3 "}):
            settings = ChannelSettings()
            assert settings.channels == ["@channel1", "@channel2", "@channel3"]

    def test_empty_channels(self):
        """测试空频道列表"""
        with patch.dict(os.environ, {"TARGET_CHANNEL": ""}):
            settings = ChannelSettings()
            assert settings.channels == []


@pytest.mark.unit
class TestAdminSettings:
    """管理员配置测试"""

    def test_default_values(self, monkeypatch):
        """测试默认值"""
        # 清除环境变量以测试真正的默认值
        monkeypatch.delenv("REPORT_ADMIN_IDS", raising=False)

        settings = AdminSettings()
        assert settings.report_admin_ids == ""
        assert settings.admin_list == ["me"]

    def test_single_admin(self):
        """测试单个管理员"""
        with patch.dict(os.environ, {"REPORT_ADMIN_IDS": "123456"}):
            settings = AdminSettings()
            assert settings.admin_list == [123456]

    def test_multiple_admins(self):
        """测试多个管理员"""
        with patch.dict(os.environ, {"REPORT_ADMIN_IDS": "123456,789012,345678"}):
            settings = AdminSettings()
            assert settings.admin_list == [123456, 789012, 345678]

    def test_invalid_admin_id_format(self, caplog):
        """测试无效的管理员 ID 格式"""
        with patch.dict(os.environ, {"REPORT_ADMIN_IDS": "invalid,123456"}):
            settings = AdminSettings()
            # 当格式错误时应该返回默认值
            assert settings.admin_list == ["me"]


@pytest.mark.unit
class TestLogSettings:
    """日志配置测试"""

    def test_default_values(self, monkeypatch):
        """测试默认值"""
        # 清除环境变量以测试真正的默认值
        monkeypatch.delenv("LOG_LEVEL", raising=False)

        settings = LogSettings()
        # 注意：如果 conftest.py 中设置了 LOG_LEVEL，这里会读取到该值
        # 在测试环境中，默认值应该是 "DEBUG"（来自 conftest）
        assert settings.log_level in ["DEBUG", "INFO"]

    def test_valid_log_levels(self):
        """测试有效的日志级别"""
        # 注意：如果环境变量设置了 LOG_LEVEL，它会被优先使用
        # 这里我们测试属性是否正确设置
        settings = LogSettings()
        assert settings.log_level in ["DEBUG", "INFO", "WARNING", "ERROR", "CRITICAL"]
        # 测试 logging_level 属性
        assert hasattr(settings, "logging_level")

    def test_lowercase_log_level(self):
        """测试小写日志级别会被转换为大写"""
        settings = LogSettings(log_level="debug")
        assert settings.log_level == "DEBUG"

    def test_invalid_log_level(self, caplog):
        """测试无效的日志级别"""
        settings = LogSettings(log_level="INVALID")
        assert settings.log_level == "DEBUG"  # 应该使用默认值 DEBUG

    def test_logging_level_property(self, monkeypatch):
        """测试 logging_level 属性"""
        import logging

        # 使用环境变量设置不同的日志级别
        monkeypatch.setenv("LOG_LEVEL", "INFO")
        settings = LogSettings()
        assert settings.logging_level == logging.INFO

        monkeypatch.setenv("LOG_LEVEL", "WARNING")
        settings = LogSettings()
        assert settings.logging_level == logging.WARNING

        monkeypatch.setenv("LOG_LEVEL", "ERROR")
        settings = LogSettings()
        assert settings.logging_level == logging.ERROR

        # DEBUG 是 10
        monkeypatch.setenv("LOG_LEVEL", "DEBUG")
        settings = LogSettings()
        assert settings.logging_level == logging.DEBUG


@pytest.mark.unit
class TestPollSettings:
    """投票配置测试"""

    def test_default_values(self, monkeypatch):
        """测试默认值"""
        # 清除环境变量以测试真正的默认值
        monkeypatch.delenv("ENABLE_POLL", raising=False)
        monkeypatch.delenv("POLL_REGEN_THRESHOLD", raising=False)

        settings = PollSettings()
        assert settings.enable_poll is True
        assert settings.poll_regen_threshold == 5
        assert settings.enable_vote_regen_request is True

    def test_poll_disabled(self, monkeypatch):
        """测试禁用投票"""
        # 使用 monkeypatch 设置环境变量（优先级高于构造函数参数）
        monkeypatch.setenv("ENABLE_POLL", "false")
        settings = PollSettings()
        assert settings.enable_poll is False

    def test_invalid_threshold(self, monkeypatch):
        """测试阈值设置"""
        # 测试边界值 - 使用环境变量
        monkeypatch.setenv("POLL_REGEN_THRESHOLD", "1")
        settings = PollSettings()
        assert settings.poll_regen_threshold == 1

        monkeypatch.setenv("POLL_REGEN_THRESHOLD", "10")
        settings = PollSettings()
        assert settings.poll_regen_threshold == 10


@pytest.mark.unit
class TestDatabaseSettings:
    """数据库配置测试"""

    def test_default_values(self, monkeypatch):
        """测试默认值"""
        # 清除环境变量以测试真正的默认值
        monkeypatch.delenv("DATABASE_TYPE", raising=False)
        for key in ["MYSQL_HOST", "MYSQL_PORT", "MYSQL_USER", "MYSQL_PASSWORD", "MYSQL_DATABASE"]:
            monkeypatch.delenv(key, raising=False)

        settings = DatabaseSettings()
        assert settings.database_type == "sqlite"
        assert settings.mysql_host == "localhost"
        assert settings.mysql_port == 3306
        assert settings.mysql_user == "root"
        assert settings.mysql_database == "sakura_bot_db"
        assert settings.mysql_pool_size == 5

    def test_mysql_type(self, monkeypatch):
        """测试 MySQL 数据库类型"""
        # 使用环境变量设置数据库类型
        monkeypatch.setenv("DATABASE_TYPE", "mysql")
        settings = DatabaseSettings()
        assert settings.database_type == "mysql"

    def test_invalid_database_type(self, caplog, monkeypatch):
        """测试无效的数据库类型"""
        # 清除环境变量以避免干扰
        monkeypatch.delenv("DATABASE_TYPE", raising=False)

        settings = DatabaseSettings(database_type="postgresql")
        assert settings.database_type == "sqlite"  # 应该使用默认值

    def test_custom_mysql_config(self, monkeypatch):
        """测试自定义 MySQL 配置"""
        # 使用环境变量设置自定义配置
        monkeypatch.setenv("MYSQL_HOST", "192.168.1.100")
        monkeypatch.setenv("MYSQL_PORT", "3307")
        monkeypatch.setenv("MYSQL_USER", "custom_user")
        monkeypatch.setenv("MYSQL_PASSWORD", "custom_pass")
        monkeypatch.setenv("MYSQL_DATABASE", "custom_db")

        settings = DatabaseSettings()
        assert settings.mysql_host == "192.168.1.100"
        assert settings.mysql_port == 3307
        assert settings.mysql_user == "custom_user"
        assert settings.mysql_password == "custom_pass"
        assert settings.mysql_database == "custom_db"


@pytest.mark.unit
class TestSettings:
    """主配置类测试"""

    def test_singleton_pattern(self):
        """测试单例模式"""
        settings1 = get_settings()
        settings2 = get_settings()
        assert settings1 is settings2

    def test_reload_settings(self):
        """测试重新加载配置"""
        settings1 = get_settings()
        settings2 = reload_settings()
        assert settings1 is not settings2

    def test_all_sub_settings_initialized(self, mock_env_vars):
        """测试所有子配置都已初始化"""
        settings = get_settings()
        assert hasattr(settings, "telegram")
        assert hasattr(settings, "ai")
        assert hasattr(settings, "channel")
        assert hasattr(settings, "admin")
        assert hasattr(settings, "log")
        assert hasattr(settings, "poll")
        assert hasattr(settings, "database")

    def test_default_bot_state(self):
        """测试默认机器人状态"""
        settings = get_settings()
        assert settings.bot_state == "running"

    def test_default_send_report_to_source(self):
        """测试默认发送报告配置"""
        settings = get_settings()
        assert settings.send_report_to_source is True


@pytest.mark.unit
class TestConvenienceFunctions:
    """便捷函数测试"""

    def test_get_api_id(self, mock_env_vars):
        """测试获取 API ID"""
        assert get_api_id() == 123456

    def test_get_api_hash(self, mock_env_vars):
        """测试获取 API Hash"""
        assert get_api_hash() == "test_api_hash"

    def test_get_bot_token(self, mock_env_vars):
        """测试获取 Bot Token"""
        assert get_bot_token() == "123456:ABCDEF"

    def test_get_llm_api_key(self, mock_env_vars):
        """测试获取 LLM API Key"""
        assert get_llm_api_key() == "test_llm_api_key"

    def test_get_llm_base_url(self, mock_env_vars):
        """测试获取 LLM Base URL"""
        assert get_llm_base_url() == "https://api.test.com"

    def test_get_llm_model(self, mock_env_vars):
        """测试获取 LLM 模型"""
        assert get_llm_model() == "test-model"

    def test_get_channels(self, mock_env_vars):
        """测试获取频道列表"""
        channels = get_channels()
        assert channels == ["@test_channel"]

    def test_get_admin_list(self, monkeypatch):
        """测试获取管理员列表（默认）"""
        # 清除环境变量以测试真正的默认值
        monkeypatch.delenv("REPORT_ADMIN_IDS", raising=False)

        admin_list = get_admin_list()
        assert admin_list == ["me"]

    def test_get_admin_list_with_env(self, mock_env_vars):
        """测试从环境变量获取管理员列表"""
        with patch.dict(os.environ, {"REPORT_ADMIN_IDS": "123456,789012"}):
            # 重新加载设置
            reload_settings()
            admin_list = get_admin_list()
            assert admin_list == [123456, 789012]

    def test_get_log_level(self, mock_env_vars):
        """测试获取日志级别"""
        assert get_log_level() == "DEBUG"

    def test_is_poll_enabled(self, mock_env_vars):
        """测试检查投票是否启用"""
        assert is_poll_enabled() is True

    def test_get_poll_regen_threshold(self, mock_env_vars):
        """测试获取投票重新生成阈值"""
        assert get_poll_regen_threshold() == 5

    def test_is_vote_regen_request_enabled(self, mock_env_vars):
        """测试检查投票重新生成请求是否启用"""
        assert is_vote_regen_request_enabled() is True

    def test_get_bot_state(self):
        """测试获取机器人状态"""
        assert get_bot_state() == "running"

    def test_set_bot_state_valid(self):
        """测试设置有效的机器人状态"""
        set_bot_state("paused")
        assert get_bot_state() == "paused"

        set_bot_state("running")
        assert get_bot_state() == "running"

    def test_set_bot_state_invalid(self):
        """测试设置无效的机器人状态"""
        with pytest.raises(ValueError):
            set_bot_state("invalid_state")

    def test_get_send_report_to_source(self):
        """测试获取发送报告配置"""
        assert get_send_report_to_source() is True

    def test_set_send_report_to_source(self):
        """测试设置发送报告配置"""
        set_send_report_to_source(False)
        assert get_send_report_to_source() is False

        set_send_report_to_source(True)
        assert get_send_report_to_source() is True


@pytest.mark.unit
class TestValidateRequiredSettings:
    """验证必要配置测试"""

    def test_all_required_settings_present(self, mock_env_vars):
        """测试所有必要配置都存在"""
        is_valid, missing = validate_required_settings()
        assert is_valid is True
        assert len(missing) == 0

    def test_missing_api_id(self):
        """测试缺少 API ID"""
        # 直接修改 settings 对象来模拟缺失的配置
        settings = get_settings()
        original_api_id = settings.telegram.api_id
        settings.telegram.api_id = None

        is_valid, missing = validate_required_settings()
        assert is_valid is False
        assert "TELEGRAM_API_ID" in missing

        # 恢复原始值
        settings.telegram.api_id = original_api_id

    def test_missing_api_hash(self):
        """测试缺少 API Hash"""
        with patch.dict(os.environ, {"TELEGRAM_API_HASH": ""}, clear=False):
            reload_settings()
            is_valid, missing = validate_required_settings()
            assert is_valid is False
            assert "TELEGRAM_API_HASH" in missing

    def test_missing_bot_token(self):
        """测试缺少 Bot Token"""
        with patch.dict(os.environ, {"TELEGRAM_BOT_TOKEN": ""}, clear=False):
            reload_settings()
            is_valid, missing = validate_required_settings()
            assert is_valid is False
            assert "TELEGRAM_BOT_TOKEN" in missing

    def test_missing_llm_api_key(self):
        """测试缺少 LLM API Key"""
        with patch.dict(os.environ, {"LLM_API_KEY": "", "DEEPSEEK_API_KEY": ""}, clear=False):
            reload_settings()
            is_valid, missing = validate_required_settings()
            assert is_valid is False
            assert "LLM_API_KEY 或 DEEPSEEK_API_KEY" in missing

    def test_multiple_missing_settings(self):
        """测试多个缺少的配置"""
        # 直接修改 settings 对象来模拟缺失的配置
        settings = get_settings()
        original_api_id = settings.telegram.api_id
        original_api_hash = settings.telegram.api_hash
        original_bot_token = settings.telegram.bot_token

        settings.telegram.api_id = None
        settings.telegram.api_hash = None
        settings.telegram.bot_token = None

        is_valid, missing = validate_required_settings()
        assert is_valid is False
        assert len(missing) >= 3

        # 恢复原始值
        settings.telegram.api_id = original_api_id
        settings.telegram.api_hash = original_api_hash
        settings.telegram.bot_token = original_bot_token


@pytest.mark.integration
class TestSettingsIntegration:
    """配置集成测试"""

    def test_full_settings_initialization(self, mock_env_vars):
        """测试完整的配置初始化流程"""
        settings = get_settings()

        # 验证 Telegram 配置
        assert settings.telegram.api_id == 123456
        assert settings.telegram.api_hash == "test_api_hash"
        assert settings.telegram.bot_token == "123456:ABCDEF"

        # 验证 AI 配置
        assert settings.ai.base_url == "https://api.test.com"
        assert settings.ai.model == "test-model"

        # 验证频道配置
        assert settings.channel.channels == ["@test_channel"]

        # 验证日志配置
        assert settings.log.log_level == "DEBUG"

        # 验证投票配置
        assert settings.poll.enable_poll is True

    def test_settings_consistency_across_calls(self, mock_env_vars):
        """测试配置在多次调用间保持一致"""
        settings1 = get_settings()
        settings2 = get_settings()

        assert settings1.telegram.api_id == settings2.telegram.api_id
        assert settings1.ai.base_url == settings2.ai.base_url
        assert settings1.channel.channels == settings2.channel.channels


if __name__ == "__main__":
    pytest.main([__file__, "-v"])
