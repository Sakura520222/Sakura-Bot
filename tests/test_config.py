"""测试配置模块

Copyright 2026 Sakura-Bot
本项目采用 AGPL-3.0 许可
"""

import os
from unittest.mock import patch

import pytest


@pytest.mark.unit
class TestConfigModule:
    """配置模块单元测试"""

    def test_import_config(self):
        """测试配置模块导入"""
        from core import config

        assert config is not None

    def test_logger_exists(self):
        """测试日志记录器存在"""
        from core.config import logger

        assert logger is not None

    def test_channels_list_exists(self):
        """测试频道配置列表存在"""
        from core.config import CHANNELS

        assert isinstance(CHANNELS, list)

    def test_api_id_from_env(self):
        """测试从环境变量获取 API ID"""
        from core.settings import get_api_id

        api_id = get_api_id()
        assert api_id is not None
        assert isinstance(api_id, int)

    def test_get_channel_schedule_with_valid_channel(self):
        """测试获取有效频道的调度配置"""
        from core.config import get_channel_schedule

        # 使用默认频道进行测试
        if len(__import__("core.config", fromlist=["CHANNELS"]).CHANNELS) > 0:
            from core.config import CHANNELS

            channel = CHANNELS[0]
            schedule = get_channel_schedule(channel)

            assert isinstance(schedule, dict)
            assert "hour" in schedule
            assert "minute" in schedule
            assert 0 <= schedule["hour"] <= 23
            assert 0 <= schedule["minute"] <= 59

    def test_build_cron_trigger(self):
        """测试构建 cron 触发器"""
        from core.config import build_cron_trigger

        schedule = {"hour": 10, "minute": 30, "frequency": "weekly", "days": ["mon", "wed", "fri"]}

        trigger = build_cron_trigger(schedule)

        assert "hour" in trigger
        assert trigger["hour"] == 10
        assert "minute" in trigger
        assert trigger["minute"] == 30


@pytest.mark.unit
class TestSettingsModule:
    """设置模块单元测试"""

    @pytest.mark.skipif(
        os.getenv("TELEGRAM_API_ID") is None, reason="需要 TELEGRAM_API_ID 环境变量"
    )
    def test_get_api_id(self):
        """测试获取 API ID"""
        from core.settings import get_api_id

        api_id = get_api_id()
        assert api_id is not None

    @pytest.mark.skipif(
        os.getenv("TELEGRAM_API_HASH") is None, reason="需要 TELEGRAM_API_HASH 环境变量"
    )
    def test_get_api_hash(self):
        """测试获取 API Hash"""
        from core.settings import get_api_hash

        api_hash = get_api_hash()
        assert api_hash is not None

    @pytest.mark.skipif(
        os.getenv("TELEGRAM_BOT_TOKEN") is None, reason="需要 TELEGRAM_BOT_TOKEN 环境变量"
    )
    def test_get_bot_token(self):
        """测试获取 Bot Token"""
        from core.settings import get_bot_token

        token = get_bot_token()
        assert token is not None
        assert ":" in token  # Telegram token 格式:数字:字符串

    def test_validate_required_settings_with_missing(self):
        """测试验证必要配置（缺失情况）"""
        with patch.dict(os.environ, {}, clear=True):
            from core.settings import validate_required_settings

            is_valid, missing = validate_required_settings()
            assert is_valid is False
            assert len(missing) > 0


if __name__ == "__main__":
    pytest.main([__file__, "-v"])
