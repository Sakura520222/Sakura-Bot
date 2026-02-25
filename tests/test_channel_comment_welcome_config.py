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

"""
测试频道评论区欢迎配置模块
"""

import pytest

from core.channel_comment_welcome_config import (
    delete_channel_comment_welcome_config,
    get_channel_comment_welcome_config,
    get_default_comment_welcome_config,
    set_channel_comment_welcome_config,
    validate_callback_data_length,
)


@pytest.fixture
def reset_config():
    """测试前后重置配置"""
    import json

    test_file = "data/config.json"

    # 备份原配置
    try:
        with open(test_file, encoding="utf-8") as f:
            original_config = json.load(f)
    except (FileNotFoundError, json.JSONDecodeError):
        original_config = None

    yield

    # 恢复原配置
    if original_config is not None:
        with open(test_file, "w", encoding="utf-8") as f:
            json.dump(original_config, f, indent=2, ensure_ascii=False)
    else:
        import os

        if os.path.exists(test_file):
            os.remove(test_file)


class TestValidateCallbackDataLength:
    """测试 Callback Data 长度验证"""

    def test_short_channel_id(self):
        """测试短频道ID"""
        assert validate_callback_data_length("test", 123) is True

    def test_long_channel_id(self):
        """测试长频道ID（会超限）"""
        long_id = "a" * 100
        assert validate_callback_data_length(long_id, 123) is False

    def test_large_message_id(self):
        """测试大消息ID"""
        assert validate_callback_data_length("channel", 999999999999) is True

    def test_both_long(self):
        """测试两者都较长"""
        long_channel = "a" * 50
        large_msg = 999999999999
        assert validate_callback_data_length(long_channel, large_msg) is False


class TestGetDefaultCommentWelcomeConfig:
    """测试获取默认评论区欢迎配置"""

    def test_returns_valid_config(self):
        """测试返回有效的配置"""
        config = get_default_comment_welcome_config()
        assert isinstance(config, dict)
        assert "enabled" in config
        assert "welcome_message" in config
        assert "button_text" in config
        assert "button_action" in config

    def test_default_enabled(self):
        """测试默认启用"""
        config = get_default_comment_welcome_config()
        assert config.get("enabled") is True

    def test_default_action(self):
        """测试默认动作"""
        config = get_default_comment_welcome_config()
        assert config.get("button_action") == "request_summary"


class TestSetChannelCommentWelcomeConfig:
    """测试设置频道评论区欢迎配置"""

    @pytest.mark.asyncio
    async def test_set_basic_config(self, reset_config):
        """测试设置基本配置"""
        channel = "https://t.me/test_channel"
        config = {"enabled": True}

        await set_channel_comment_welcome_config(channel, config)

        result = get_channel_comment_welcome_config(channel)
        assert result.get("enabled") is True

    @pytest.mark.asyncio
    async def test_set_custom_message(self, reset_config):
        """测试设置自定义消息"""
        channel = "https://t.me/test_channel"
        custom_message = "欢迎来到我们的频道！"
        config = {
            "enabled": True,
            "welcome_message": custom_message,
            "button_text": "申请总结",
        }

        await set_channel_comment_welcome_config(channel, config)

        result = get_channel_comment_welcome_config(channel)
        assert result.get("welcome_message") == custom_message

    @pytest.mark.asyncio
    async def test_set_disabled(self, reset_config):
        """测试禁用频道"""
        channel = "https://t.me/test_channel"
        config = {"enabled": False}

        await set_channel_comment_welcome_config(channel, config)

        result = get_channel_comment_welcome_config(channel)
        assert result.get("enabled") is False


class TestGetChannelCommentWelcomeConfig:
    """测试获取频道评论区欢迎配置"""

    def test_get_existing_config(self, reset_config):
        """测试获取已存在的配置"""
        channel = "https://t.me/test_channel"
        # 先设置配置
        import asyncio

        asyncio.run(
            set_channel_comment_welcome_config(
                channel, {"enabled": True, "welcome_message": "test"}
            )
        )

        result = get_channel_comment_welcome_config(channel)
        assert result is not None
        assert result.get("welcome_message") == "test"

    def test_get_nonexistent_config(self, reset_config):
        """测试获取不存在的配置（返回默认）"""
        channel = "https://t.me/nonexistent_channel"

        result = get_channel_comment_welcome_config(channel)
        # 应该返回默认配置
        assert result is not None
        assert "enabled" in result

    def test_get_all_configs(self, reset_config):
        """测试获取所有配置"""
        import asyncio

        # 设置多个频道配置
        asyncio.run(set_channel_comment_welcome_config("https://t.me/channel1", {"enabled": True}))
        asyncio.run(set_channel_comment_welcome_config("https://t.me/channel2", {"enabled": False}))

        # 获取所有配置（通过检查是否存在来验证）
        config1 = get_channel_comment_welcome_config("https://t.me/channel1")
        config2 = get_channel_comment_welcome_config("https://t.me/channel2")

        assert config1.get("enabled") is True
        assert config2.get("enabled") is False


class TestDeleteChannelCommentWelcomeConfig:
    """测试删除频道评论区欢迎配置"""

    @pytest.mark.asyncio
    async def test_delete_existing_config(self, reset_config):
        """测试删除已存在的配置"""
        channel = "https://t.me/test_channel"

        # 先设置配置
        await set_channel_comment_welcome_config(channel, {"enabled": True})

        # 删除配置
        await delete_channel_comment_welcome_config(channel)

        # 验证已删除（应该返回默认配置）
        result = get_channel_comment_welcome_config(channel)
        assert result == get_default_comment_welcome_config()

    @pytest.mark.asyncio
    async def test_delete_nonexistent_config(self, reset_config):
        """测试删除不存在的配置（不应报错）"""
        channel = "https://t.me/nonexistent_channel"

        # 不应该抛出异常
        await delete_channel_comment_welcome_config(channel)

        # 应该仍然返回默认配置
        result = get_channel_comment_welcome_config(channel)
        assert result == get_default_comment_welcome_config()
