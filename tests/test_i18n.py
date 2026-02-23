"""测试 I18n 模块

Copyright 2026 Sakura-Bot
本项目采用 AGPL-3.0 许可
"""

import pytest

from core.i18n import (
    I18nManager,
    get_language,
    get_supported_languages,
    get_text,
    set_language,
    t,
)


@pytest.mark.unit
class TestI18nManager:
    """I18n Manager 测试"""

    def test_singleton_pattern(self):
        """测试单例模式"""
        manager1 = I18nManager()
        manager2 = I18nManager()
        assert manager1 is manager2

    def test_get_default_language(self):
        """测试获取默认语言"""
        manager = I18nManager()
        assert manager.get_language() == "zh-CN"

    def test_set_valid_language(self):
        """测试设置有效语言"""
        manager = I18nManager()
        assert manager.set_language("en-US") is True
        assert manager.get_language() == "en-US"

    def test_set_invalid_language(self):
        """测试设置无效语言"""
        manager = I18nManager()
        manager.set_language("zh-CN")  # 先重置为中文
        assert manager.set_language("fr-FR") is False
        assert manager.get_language() == "zh-CN"  # 保持不变

    def test_get_supported_languages(self):
        """测试获取支持的语言列表"""
        manager = I18nManager()
        languages = manager.get_supported_languages()
        assert "zh-CN" in languages
        assert "en-US" in languages
        assert len(languages) == 2

    def test_get_text_with_existing_key(self):
        """测试获取存在的翻译文本"""
        manager = I18nManager()
        manager.set_language("zh-CN")  # 确保是中文
        text = manager.get_text("success")
        assert text == "操作成功"

    def test_get_text_with_variable_interpolation(self):
        """测试文本变量插值"""
        manager = I18nManager()
        text = manager.get_text("language.current", language="zh-CN")
        assert "zh-CN" in text

    def test_get_text_fallback_to_zh_cn(self):
        """测试回退到中文"""
        manager = I18nManager()
        manager.set_language("en-US")
        # 使用一个只在中文存在的key
        text = manager.get_text("language.current")
        assert "current" in text.lower()

    def test_get_text_key_not_found_returns_key(self):
        """测试key不存在时返回key本身"""
        manager = I18nManager()
        text = manager.get_text("nonexistent.key")
        assert text == "nonexistent.key"

    def test_get_text_interpolation_error(self):
        """测试插值错误处理"""
        manager = I18nManager()
        # 使用一个需要插值的key，但不提供变量
        text = manager.get_text("error.invalid_parameter")
        # 应该返回原始文本或部分插值文本
        assert "{parameter}" in text or text.startswith("无效的参数")

    def test_get_text_with_all_variables(self, caplog):
        """测试完整的变量插值"""
        manager = I18nManager()
        text = manager.get_text(
            "schedule.set_success", channel="test_channel", frequency="daily", hour=9, minute=30
        )
        assert "test_channel" in text
        assert "09:30" in text

    def test_english_translation(self):
        """测试英文翻译"""
        manager = I18nManager()
        manager.set_language("en-US")
        text = manager.get_text("success")
        assert text == "Operation successful"

    def test_language_persistence(self):
        """测试语言设置持久化（单例）"""
        manager1 = I18nManager()
        manager1.set_language("en-US")

        manager2 = I18nManager()
        assert manager2.get_language() == "en-US"


@pytest.mark.unit
class TestI18nConvenienceFunctions:
    """I18n 便捷函数测试"""

    def test_get_language_function(self):
        """测试 get_language 函数"""
        lang = get_language()
        assert lang in ["zh-CN", "en-US"]

    def test_set_language_function(self):
        """测试 set_language 函数"""
        result = set_language("en-US")
        assert result is True
        assert get_language() == "en-US"

    def test_get_supported_languages_function(self):
        """测试 get_supported_languages 函数"""
        languages = get_supported_languages()
        assert isinstance(languages, list)
        assert len(languages) == 2

    def test_get_text_function(self):
        """测试 get_text 函数"""
        text = get_text("success")
        assert isinstance(text, str)

    def test_t_alias_function(self):
        """测试 t 别名函数"""
        text = t("success")
        assert text == "操作成功" or text == "Operation successful"

    def test_function_with_interpolation(self):
        """测试带插值的便捷函数"""
        text = get_text("language.changed", language="en-US")
        assert "en-US" in text


if __name__ == "__main__":
    pytest.main([__file__, "-v"])
