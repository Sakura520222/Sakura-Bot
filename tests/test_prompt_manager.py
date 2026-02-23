"""测试 Prompt Manager 模块

Copyright 2026 Sakura-Bot
本项目采用 AGPL-3.0 许可
"""

from unittest.mock import patch

import pytest

from core.prompt_manager import load_prompt, save_prompt


@pytest.mark.unit
class TestPromptManager:
    """Prompt Manager 测试"""

    def test_save_and_load_prompt(self, temp_dir):
        """测试保存和加载提示词"""
        prompt_file = temp_dir / "test_prompt.txt"
        test_prompt = "这是一个测试提示词"

        # 模拟 core.config 中的 PROMPT_FILE
        with patch("core.prompt_manager.PROMPT_FILE", str(prompt_file)):
            # 保存提示词
            save_prompt(test_prompt)
            assert prompt_file.exists()
            assert prompt_file.read_text(encoding="utf-8") == test_prompt

            # 加载提示词
            loaded_prompt = load_prompt()
            assert loaded_prompt == test_prompt

    def test_load_prompt_file_not_exists(self, temp_dir):
        """测试加载不存在的提示词文件"""
        nonexistent_file = temp_dir / "nonexistent_prompt.txt"

        with patch("core.prompt_manager.PROMPT_FILE", str(nonexistent_file)):
            from core.config import DEFAULT_PROMPT

            prompt = load_prompt()
            assert prompt == DEFAULT_PROMPT
            # 应该创建文件
            assert nonexistent_file.exists()

    def test_load_prompt_with_encoding(self, temp_dir):
        """测试加载包含特殊字符的提示词"""
        prompt_file = temp_dir / "test_prompt.txt"
        test_prompt = "测试提示词 with special chars: 🌸 中文 ñoño"

        prompt_file.write_text(test_prompt, encoding="utf-8")

        with patch("core.prompt_manager.PROMPT_FILE", str(prompt_file)):
            loaded_prompt = load_prompt()
            assert loaded_prompt == test_prompt

    def test_save_prompt_overwrite(self, temp_dir):
        """测试覆盖已存在的提示词文件"""
        prompt_file = temp_dir / "test_prompt.txt"

        with patch("core.prompt_manager.PROMPT_FILE", str(prompt_file)):
            # 保存第一个提示词
            save_prompt("第一个提示词")
            assert prompt_file.read_text(encoding="utf-8") == "第一个提示词"

            # 覆盖保存
            save_prompt("第二个提示词")
            assert prompt_file.read_text(encoding="utf-8") == "第二个提示词"

    def test_save_prompt_empty_string(self, temp_dir):
        """测试保存空字符串"""
        prompt_file = temp_dir / "test_prompt.txt"

        with patch("core.prompt_manager.PROMPT_FILE", str(prompt_file)):
            save_prompt("")
            assert prompt_file.read_text(encoding="utf-8") == ""

    def test_load_prompt_trims_whitespace(self, temp_dir):
        """测试加载提示词时会去除首尾空白"""
        prompt_file = temp_dir / "test_prompt.txt"
        test_prompt = "  测试提示词  \n\n  "

        prompt_file.write_text(test_prompt, encoding="utf-8")

        with patch("core.prompt_manager.PROMPT_FILE", str(prompt_file)):
            loaded_prompt = load_prompt()
            assert loaded_prompt == "测试提示词"


if __name__ == "__main__":
    pytest.main([__file__, "-v"])
