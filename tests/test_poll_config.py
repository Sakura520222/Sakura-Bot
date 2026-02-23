"""测试 Poll Config 模块

Copyright 2026 Sakura-Bot
本项目采用 AGPL-3.0 许可
"""

import json
from unittest.mock import patch

import pytest

from core.exceptions import ConfigurationError
from core.poll_config import (
    ChannelPollConfigManager,
    delete_channel_poll_config,
    get_channel_poll_config,
    get_poll_config_manager,
    set_channel_poll_config,
)


@pytest.mark.unit
class TestChannelPollConfigManagerInit:
    """配置管理器初始化测试"""

    def test_init_with_default_config_file(self, tmp_path):
        """测试使用默认配置文件初始化"""
        config_file = tmp_path / "config.json"

        with patch("core.poll_config.CONFIG_FILE", str(config_file)):
            manager = ChannelPollConfigManager()

            assert manager._poll_settings == {}

    def test_init_with_custom_config_file(self, tmp_path):
        """测试使用自定义配置文件初始化"""
        config_file = tmp_path / "custom_config.json"

        manager = ChannelPollConfigManager(str(config_file))

        assert str(manager._config_file) == str(config_file)

    def test_init_loads_existing_settings(self, tmp_path):
        """测试加载现有配置"""
        config_file = tmp_path / "config.json"
        settings = {
            "channel_poll_settings": {
                "https://t.me/channel1": {"enabled": True, "send_to_channel": True}
            }
        }
        config_file.write_text(json.dumps(settings), encoding="utf-8")

        manager = ChannelPollConfigManager(str(config_file))

        assert len(manager._poll_settings) == 1
        assert "https://t.me/channel1" in manager._poll_settings

    def test_init_with_invalid_json(self, tmp_path):
        """测试加载无效JSON"""
        config_file = tmp_path / "config.json"
        config_file.write_text("invalid json", encoding="utf-8")

        with pytest.raises(ConfigurationError):
            ChannelPollConfigManager(str(config_file))

    def test_init_with_missing_settings_key(self, tmp_path):
        """测试配置文件缺少channel_poll_settings键"""
        config_file = tmp_path / "config.json"
        config_file.write_text('{"other_key": {}}', encoding="utf-8")

        manager = ChannelPollConfigManager(str(config_file))

        assert manager._poll_settings == {}

    def test_init_with_invalid_settings_type(self, tmp_path):
        """测试channel_poll_settings类型错误"""
        config_file = tmp_path / "config.json"
        config_file.write_text('{"channel_poll_settings": "not_a_dict"}', encoding="utf-8")

        manager = ChannelPollConfigManager(str(config_file))

        assert manager._poll_settings == {}


@pytest.mark.unit
class TestGetConfig:
    """获取配置测试"""

    def test_get_existing_config(self, tmp_path):
        """测试获取存在的配置"""
        config_file = tmp_path / "config.json"
        settings = {
            "channel_poll_settings": {
                "https://t.me/channel1": {"enabled": True, "send_to_channel": True}
            }
        }
        config_file.write_text(json.dumps(settings), encoding="utf-8")

        manager = ChannelPollConfigManager(str(config_file))
        config = manager.get_config("https://t.me/channel1")

        assert config["enabled"] is True
        assert config["send_to_channel"] is True

    def test_get_nonexistent_config(self):
        """测试获取不存在的配置"""
        manager = ChannelPollConfigManager()
        config = manager.get_config("https://t.me/channel1")

        assert config["enabled"] is None  # 使用全局配置
        assert config["send_to_channel"] is False  # 默认讨论组模式

    def test_get_config_with_partial_settings(self, tmp_path):
        """测试获取部分配置"""
        config_file = tmp_path / "config.json"
        settings = {
            "channel_poll_settings": {
                "https://t.me/channel1": {
                    "enabled": True
                    # 缺少send_to_channel
                }
            }
        }
        config_file.write_text(json.dumps(settings), encoding="utf-8")

        manager = ChannelPollConfigManager(str(config_file))
        config = manager.get_config("https://t.me/channel1")

        assert config["enabled"] is True
        assert config["send_to_channel"] is False  # 默认值


@pytest.mark.unit
class TestSetConfig:
    """设置配置测试"""

    def test_set_enabled(self, tmp_path):
        """测试设置启用状态"""
        config_file = tmp_path / "config.json"
        config_file.write_text('{"channel_poll_settings": {}}', encoding="utf-8")

        manager = ChannelPollConfigManager(str(config_file))
        manager.set_config("https://t.me/channel1", enabled=True)

        config = manager.get_config("https://t.me/channel1")
        assert config["enabled"] is True

    def test_set_send_to_channel(self, tmp_path):
        """测试设置发送位置"""
        config_file = tmp_path / "config.json"
        config_file.write_text('{"channel_poll_settings": {}}', encoding="utf-8")

        manager = ChannelPollConfigManager(str(config_file))
        manager.set_config("https://t.me/channel1", send_to_channel=True)

        config = manager.get_config("https://t.me/channel1")
        assert config["send_to_channel"] is True

    def test_set_both_parameters(self, tmp_path):
        """测试同时设置两个参数"""
        config_file = tmp_path / "config.json"
        config_file.write_text('{"channel_poll_settings": {}}', encoding="utf-8")

        manager = ChannelPollConfigManager(str(config_file))
        manager.set_config("https://t.me/channel1", enabled=True, send_to_channel=False)

        config = manager.get_config("https://t.me/channel1")
        assert config["enabled"] is True
        assert config["send_to_channel"] is False

    def test_update_existing_config(self, tmp_path):
        """测试更新现有配置"""
        config_file = tmp_path / "config.json"
        settings = {
            "channel_poll_settings": {
                "https://t.me/channel1": {"enabled": True, "send_to_channel": False}
            }
        }
        config_file.write_text(json.dumps(settings), encoding="utf-8")

        manager = ChannelPollConfigManager(str(config_file))
        manager.set_config("https://t.me/channel1", enabled=False)

        config = manager.get_config("https://t.me/channel1")
        assert config["enabled"] is False
        assert config["send_to_channel"] is False  # 保持不变

    def test_set_none_does_not_modify(self, tmp_path):
        """测试设置None不修改现有值"""
        config_file = tmp_path / "config.json"
        settings = {
            "channel_poll_settings": {
                "https://t.me/channel1": {"enabled": True, "send_to_channel": True}
            }
        }
        config_file.write_text(json.dumps(settings), encoding="utf-8")

        manager = ChannelPollConfigManager(str(config_file))
        manager.set_config("https://t.me/channel1", enabled=None, send_to_channel=None)

        config = manager.get_config("https://t.me/channel1")
        assert config["enabled"] is True
        assert config["send_to_channel"] is True


@pytest.mark.unit
class TestDeleteConfig:
    """删除配置测试"""

    def test_delete_existing_config(self, tmp_path):
        """测试删除存在的配置"""
        config_file = tmp_path / "config.json"
        settings = {
            "channel_poll_settings": {
                "https://t.me/channel1": {"enabled": True, "send_to_channel": False}
            }
        }
        config_file.write_text(json.dumps(settings), encoding="utf-8")

        manager = ChannelPollConfigManager(str(config_file))
        result = manager.delete_config("https://t.me/channel1")

        assert result is True
        config = manager.get_config("https://t.me/channel1")
        assert config["enabled"] is None  # 使用全局配置

    def test_delete_nonexistent_config(self):
        """测试删除不存在的配置"""
        manager = ChannelPollConfigManager()
        result = manager.delete_config("https://t.me/channel1")

        assert result is False


@pytest.mark.unit
class TestGetAllConfigs:
    """获取所有配置测试"""

    def test_get_all_configs_returns_copy(self, tmp_path):
        """测试返回配置的副本"""
        config_file = tmp_path / "config.json"
        settings = {
            "channel_poll_settings": {
                "https://t.me/channel1": {"enabled": True, "send_to_channel": False}
            }
        }
        config_file.write_text(json.dumps(settings), encoding="utf-8")

        manager = ChannelPollConfigManager(str(config_file))
        all_configs = manager.get_all_configs()

        # 修改返回的字典不应该影响原配置
        all_configs["new_channel"] = {"enabled": False}

        assert "new_channel" not in manager._poll_settings


@pytest.mark.unit
class TestFormatConfigText:
    """格式化配置文本测试"""

    def test_format_with_custom_enabled(self):
        """测试格式化自定义启用状态"""
        manager = ChannelPollConfigManager()
        manager._poll_settings = {
            "https://t.me/channel1": {"enabled": True, "send_to_channel": False}
        }

        text = manager.format_config_text("https://t.me/channel1", global_enabled=False)

        assert "启用" in text or "enabled" in text.lower()

    def test_format_with_global_enabled(self):
        """测试格式化全局启用状态"""
        manager = ChannelPollConfigManager()
        manager._poll_settings = {
            "https://t.me/channel1": {"enabled": None, "send_to_channel": False}
        }

        text = manager.format_config_text("https://t.me/channel1", global_enabled=True)

        assert "全局" in text or "global" in text.lower()

    def test_format_channel_location(self):
        """测试格式化频道位置"""
        manager = ChannelPollConfigManager()
        manager._poll_settings = {
            "https://t.me/channel1": {"enabled": True, "send_to_channel": True}
        }

        text = manager.format_config_text("https://t.me/channel1", global_enabled=True)

        assert "频道" in text or "channel" in text.lower()

    def test_format_discussion_location(self):
        """测试格式化讨论组位置"""
        manager = ChannelPollConfigManager()
        manager._poll_settings = {
            "https://t.me/channel1": {"enabled": True, "send_to_channel": False}
        }

        text = manager.format_config_text("https://t.me/channel1", global_enabled=True)

        assert "讨论组" in text or "discussion" in text.lower()


@pytest.mark.unit
class TestGetPollConfigManager:
    """获取配置管理器实例测试"""

    def test_singleton_pattern(self):
        """测试单例模式"""
        import core.poll_config

        core.poll_config._poll_config_manager = None

        manager1 = get_poll_config_manager()
        manager2 = get_poll_config_manager()

        assert manager1 is manager2

    def test_creates_instance(self):
        """测试创建实例"""
        import core.poll_config

        core.poll_config._poll_config_manager = None

        manager = get_poll_config_manager()

        assert isinstance(manager, ChannelPollConfigManager)


@pytest.mark.unit
class TestConvenienceFunctions:
    """便捷函数测试"""

    def test_get_channel_poll_config(self):
        """测试获取频道配置便捷函数"""
        import core.poll_config

        core.poll_config._poll_config_manager = None

        config = get_channel_poll_config("https://t.me/channel1")

        assert isinstance(config, dict)
        assert "enabled" in config

    def test_set_channel_poll_config(self):
        """测试设置频道配置便捷函数"""
        import core.poll_config

        core.poll_config._poll_config_manager = None

        set_channel_poll_config("https://t.me/channel1", enabled=True)

        config = get_channel_poll_config("https://t.me/channel1")
        assert config["enabled"] is True

    def test_delete_channel_poll_config(self):
        """测试删除频道配置便捷函数"""
        import core.poll_config

        core.poll_config._poll_config_manager = None

        set_channel_poll_config("https://t.me/channel1", enabled=True)
        result = delete_channel_poll_config("https://t.me/channel1")

        assert result is True


if __name__ == "__main__":
    pytest.main([__file__, "-v"])
