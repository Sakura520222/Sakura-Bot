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
    get_all_comment_welcome_configs,
    get_channel_comment_welcome_config,
    get_default_comment_welcome_config,
    set_channel_comment_welcome_config,
    validate_callback_data_length,
)


@pytest.fixture
def mock_config(monkeypatch):
    """
    使用 monkeypatch 模拟配置文件操作
    避免读写真实的 data/config.json 文件
    """
    # 使用可变 dict 作为"配置文件"的单一真相源
    config_store = {
        "channel_comment_welcome_settings": {},
    }

    def fake_load_config():
        """模拟加载配置，返回配置的副本"""
        return config_store.copy()

    def fake_save_config(new_config):
        """模拟保存配置"""
        config_store.clear()
        config_store.update(new_config)

    # 将核心模块中的 load/save 函数打桩到内存实现
    monkeypatch.setattr(
        "core.config.load_config",
        fake_load_config,
        raising=False,
    )
    monkeypatch.setattr(
        "core.config.save_config",
        fake_save_config,
        raising=False,
    )

    # 同时也需要打桩 channel_comment_welcome_config 模块中的引用
    monkeypatch.setattr(
        "core.channel_comment_welcome_config.load_config",
        fake_load_config,
        raising=False,
    )
    monkeypatch.setattr(
        "core.channel_comment_welcome_config.save_config",
        fake_save_config,
        raising=False,
    )

    return config_store


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
    async def test_set_basic_config(self, mock_config):
        """测试设置基本配置"""
        channel = "https://t.me/test_channel"
        config = {"enabled": True}

        await set_channel_comment_welcome_config(channel, config)

        result = await get_channel_comment_welcome_config(channel)
        assert result.get("enabled") is True

    @pytest.mark.asyncio
    async def test_set_custom_message(self, mock_config):
        """测试设置自定义消息"""
        channel = "https://t.me/test_channel"
        custom_message = "欢迎来到我们的频道！"
        config = {
            "enabled": True,
            "welcome_message": custom_message,
            "button_text": "申请总结",
        }

        await set_channel_comment_welcome_config(channel, config)

        result = await get_channel_comment_welcome_config(channel)
        assert result.get("welcome_message") == custom_message

    @pytest.mark.asyncio
    async def test_set_disabled(self, mock_config):
        """测试禁用频道"""
        channel = "https://t.me/test_channel"
        config = {"enabled": False}

        await set_channel_comment_welcome_config(channel, config)

        result = await get_channel_comment_welcome_config(channel)
        assert result.get("enabled") is False

    @pytest.mark.asyncio
    async def test_button_text_too_long(self, mock_config):
        """测试按钮文本过长应抛出异常"""
        channel = "https://t.me/test_channel"
        long_text = "a" * 100  # 超过 MAX_BUTTON_TEXT_LENGTH (20)

        with pytest.raises(ValueError, match="按钮文本过长"):
            await set_channel_comment_welcome_config(channel, {"button_text": long_text})

    @pytest.mark.asyncio
    async def test_invalid_button_action(self, mock_config):
        """测试无效的按钮行为应抛出异常"""
        channel = "https://t.me/test_channel"

        with pytest.raises(ValueError, match="无效的按钮行为"):
            await set_channel_comment_welcome_config(channel, {"button_action": "invalid_action"})


class TestGetChannelCommentWelcomeConfig:
    """测试获取频道评论区欢迎配置"""

    @pytest.mark.asyncio
    async def test_get_existing_config(self, mock_config):
        """测试获取已存在的配置"""
        channel = "https://t.me/test_channel"

        # 先设置配置
        await set_channel_comment_welcome_config(
            channel, {"enabled": True, "welcome_message": "test"}
        )

        result = await get_channel_comment_welcome_config(channel)
        assert result is not None
        assert result.get("welcome_message") == "test"

    @pytest.mark.asyncio
    async def test_get_nonexistent_config(self, mock_config):
        """测试获取不存在的配置（返回默认）"""
        channel = "https://t.me/nonexistent_channel"

        result = await get_channel_comment_welcome_config(channel)
        # 应该返回默认配置
        assert result is not None
        assert "enabled" in result
        assert result == get_default_comment_welcome_config()


class TestGetAllCommentWelcomeConfigs:
    """测试获取所有频道评论区欢迎配置"""

    @pytest.mark.asyncio
    async def test_get_all_configs(self, mock_config):
        """测试获取所有频道的配置"""
        channel1 = "https://t.me/test_channel1"
        channel2 = "https://t.me/test_channel2"

        config1 = {"enabled": True, "welcome_message": "Welcome 1"}
        config2 = {"enabled": False, "welcome_message": "Welcome 2"}

        # 设置多个频道的配置
        await set_channel_comment_welcome_config(channel1, config1)
        await set_channel_comment_welcome_config(channel2, config2)

        # 获取所有配置
        all_configs = await get_all_comment_welcome_configs()

        # 断言返回的字典包含期望的频道和配置
        assert channel1 in all_configs
        assert channel2 in all_configs

        assert all_configs[channel1]["enabled"] is True
        assert all_configs[channel1]["welcome_message"] == "Welcome 1"

        assert all_configs[channel2]["enabled"] is False
        assert all_configs[channel2]["welcome_message"] == "Welcome 2"

    @pytest.mark.asyncio
    async def test_get_all_configs_empty(self, mock_config):
        """测试获取所有配置（空）"""
        # 不设置任何配置
        all_configs = await get_all_comment_welcome_configs()

        # 应该返回空字典
        assert all_configs == {}


class TestDeleteChannelCommentWelcomeConfig:
    """测试删除频道评论区欢迎配置"""

    @pytest.mark.asyncio
    async def test_delete_existing_config(self, mock_config):
        """测试删除已存在的配置"""
        channel = "https://t.me/test_channel"

        # 先设置配置
        await set_channel_comment_welcome_config(channel, {"enabled": True})

        # 删除配置
        result = await delete_channel_comment_welcome_config(channel)
        assert result is True

        # 验证已删除（应该返回默认配置）
        config = await get_channel_comment_welcome_config(channel)
        assert config == get_default_comment_welcome_config()

    @pytest.mark.asyncio
    async def test_delete_nonexistent_config(self, mock_config):
        """测试删除不存在的配置（不应报错）"""
        channel = "https://t.me/nonexistent_channel"

        # 不应该抛出异常
        result = await delete_channel_comment_welcome_config(channel)
        assert result is False

        # 应该仍然返回默认配置
        config = await get_channel_comment_welcome_config(channel)
        assert config == get_default_comment_welcome_config()
