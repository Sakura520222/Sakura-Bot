"""测试 Poll Prompt Manager 模块

Copyright 2026 Sakura-Bot
本项目采用 AGPL-3.0 许可
"""

from unittest.mock import patch

import pytest

from core.poll_prompt_manager import load_poll_prompt, save_poll_prompt


@pytest.mark.unit
class TestPollPromptManager:
    """Poll Prompt Manager 测试"""

    def test_save_and_load_poll_prompt(self, temp_dir):
        """测试保存和加载投票提示词"""
        prompt_file = temp_dir / "test_poll_prompt.txt"
        test_prompt = "这是一个测试投票提示词"

        with patch("core.poll_prompt_manager.POLL_PROMPT_FILE", str(prompt_file)):
            # 保存提示词
            save_poll_prompt(test_prompt)
            assert prompt_file.exists()
            assert prompt_file.read_text(encoding="utf-8") == test_prompt

            # 加载提示词
            loaded_prompt = load_poll_prompt()
            assert loaded_prompt == test_prompt

    def test_load_poll_prompt_file_not_exists(self, temp_dir):
        """测试加载不存在的投票提示词文件"""
        nonexistent_file = temp_dir / "nonexistent_poll_prompt.txt"

        with patch("core.poll_prompt_manager.POLL_PROMPT_FILE", str(nonexistent_file)):
            from core.config import DEFAULT_POLL_PROMPT

            prompt = load_poll_prompt()
            assert prompt == DEFAULT_POLL_PROMPT
            # 应该创建文件
            assert nonexistent_file.exists()

    def test_load_poll_prompt_with_encoding(self, temp_dir):
        """测试加载包含特殊字符的投票提示词"""
        prompt_file = temp_dir / "test_poll_prompt.txt"
        test_prompt = "测试投票提示词 with special chars: 🌸 中文 ñoño"

        prompt_file.write_text(test_prompt, encoding="utf-8")

        with patch("core.poll_prompt_manager.POLL_PROMPT_FILE", str(prompt_file)):
            loaded_prompt = load_poll_prompt()
            assert loaded_prompt == test_prompt

    def test_save_poll_prompt_overwrite(self, temp_dir):
        """测试覆盖已存在的投票提示词文件"""
        prompt_file = temp_dir / "test_poll_prompt.txt"

        with patch("core.poll_prompt_manager.POLL_PROMPT_FILE", str(prompt_file)):
            # 保存第一个提示词
            save_poll_prompt("第一个投票提示词")
            assert prompt_file.read_text(encoding="utf-8") == "第一个投票提示词"

            # 覆盖保存
            save_poll_prompt("第二个投票提示词")
            assert prompt_file.read_text(encoding="utf-8") == "第二个投票提示词"

    def test_save_poll_prompt_empty_string(self, temp_dir):
        """测试保存空字符串"""
        prompt_file = temp_dir / "test_poll_prompt.txt"

        with patch("core.poll_prompt_manager.POLL_PROMPT_FILE", str(prompt_file)):
            save_poll_prompt("")
            assert prompt_file.read_text(encoding="utf-8") == ""

    def test_load_poll_prompt_trims_whitespace(self, temp_dir):
        """测试加载投票提示词时会去除首尾空白"""
        prompt_file = temp_dir / "test_poll_prompt.txt"
        test_prompt = "  测试投票提示词  \n\n  "

        prompt_file.write_text(test_prompt, encoding="utf-8")

        with patch("core.poll_prompt_manager.POLL_PROMPT_FILE", str(prompt_file)):
            loaded_prompt = load_poll_prompt()
            assert loaded_prompt == "测试投票提示词"


if __name__ == "__main__":
    pytest.main([__file__, "-v"])
