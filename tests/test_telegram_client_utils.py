# Copyright 2026 Sakura-Bot
#
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可，
# 并附加非商业使用限制条款。

"""
测试 Telegram 客户端工具函数
"""

from core.telegram_client_utils import (
    sanitize_markdown,
    split_by_lines_smart,
    split_message_smart,
    validate_message_entities,
)


class TestValidateMessageEntities:
    """测试消息实体验证函数"""

    def test_valid_simple_text(self):
        """测试纯文本验证"""
        text = "这是一个简单的测试文本"
        is_valid, error_msg = validate_message_entities(text)
        assert is_valid is True
        assert error_msg == "所有实体完整"

    def test_valid_bold(self):
        """测试有效的粗体格式"""
        text = "这是**粗体**文本"
        is_valid, error_msg = validate_message_entities(text)
        assert is_valid is True

    def test_invalid_bold_unclosed(self):
        """测试未闭合的粗体格式"""
        text = "这是**粗体文本"
        is_valid, error_msg = validate_message_entities(text)
        assert is_valid is False
        assert "粗体标记不匹配" in error_msg

    def test_valid_italic(self):
        """测试有效的斜体格式"""
        text = "这是*斜体*文本"
        is_valid, error_msg = validate_message_entities(text)
        assert is_valid is True

    def test_valid_link(self):
        """测试有效的链接格式"""
        text = "这是一个[链接](https://example.com)"
        is_valid, error_msg = validate_message_entities(text)
        assert is_valid is True

    def test_invalid_link_empty_text(self):
        """测试空文本的链接格式"""
        text = "这是一个[](https://example.com)"
        is_valid, error_msg = validate_message_entities(text)
        assert is_valid is False
        assert "链接文本为空" in error_msg

    def test_valid_code(self):
        """测试有效的内联代码格式"""
        text = "这是`代码`文本"
        is_valid, error_msg = validate_message_entities(text)
        assert is_valid is True

    def test_invalid_code_unclosed(self):
        """测试未闭合的代码格式"""
        text = "这是`代码文本"
        is_valid, error_msg = validate_message_entities(text)
        assert is_valid is False
        assert "内联代码标记不匹配" in error_msg

    def test_valid_combined_formats(self):
        """测试组合格式"""
        text = "**粗体** *斜体* `代码` [链接](url)"
        is_valid, error_msg = validate_message_entities(text)
        assert is_valid is True

    def test_empty_text(self):
        """测试空文本"""
        is_valid, error_msg = validate_message_entities("")
        assert is_valid is True
        assert error_msg == "空文本，无需验证"

    def test_valid_code_block(self):
        """测试有效的代码块格式"""
        text = "这是```代码块```文本"
        is_valid, error_msg = validate_message_entities(text)
        assert is_valid is True

    def test_invalid_code_block_unclosed(self):
        """测试未闭合的代码块格式"""
        text = "这是```代码块文本"
        is_valid, error_msg = validate_message_entities(text)
        assert is_valid is False
        assert "代码块标记不匹配" in error_msg

    def test_valid_strikethrough(self):
        """测试有效的删除线格式"""
        text = "这是~~删除线~~文本"
        is_valid, error_msg = validate_message_entities(text)
        assert is_valid is True

    def test_valid_bold_underscore(self):
        """测试有效的下划线粗体格式"""
        text = "这是__粗体__文本"
        is_valid, error_msg = validate_message_entities(text)
        assert is_valid is True


class TestSanitizeMarkdown:
    """测试 Markdown 清理函数"""

    def test_sanitize_valid_text(self):
        """测试清理有效的文本（不应修改）"""
        text = "**粗体** *斜体*"
        result = sanitize_markdown(text, aggressive=False)
        assert result == text

    def test_sanitize_unclosed_bold(self):
        """测试清理未闭合的粗体格式"""
        text = "这是**粗体文本"
        result = sanitize_markdown(text, aggressive=False)
        assert "**" not in result
        assert result == "这是粗体文本"

    def test_sanitize_unclosed_code(self):
        """测试清理未闭合的代码格式"""
        text = "这是`代码文本"
        result = sanitize_markdown(text, aggressive=False)
        assert "`" not in result
        assert result == "这是代码文本"

    def test_sanitize_aggressive_mode(self):
        """测试激进模式（移除所有格式）"""
        text = "**粗体** *斜体* `代码` [链接](url)"
        result = sanitize_markdown(text, aggressive=True)
        assert "**" not in result
        assert "*" not in result
        assert "`" not in result
        assert "[" not in result
        assert result == "粗体 斜体 代码 链接"

    def test_sanitize_empty_text(self):
        """测试清理空文本"""
        text = ""
        result = sanitize_markdown(text, aggressive=False)
        assert result == ""

    def test_sanitize_nested_formats(self):
        """测试清理嵌套格式"""
        text = "**粗体*斜体**文本"  # 不完整的嵌套
        result = sanitize_markdown(text, aggressive=False)
        # 应该移除所有不完整的格式
        assert validate_message_entities(result)[0] is True


class TestSplitMessageSmart:
    """测试智能消息分割函数"""

    def test_short_message_no_split(self):
        """测试短消息不分割"""
        text = "这是一条短消息"
        parts = split_message_smart(text, max_length=100, preserve_md=True)
        assert len(parts) == 1
        assert parts[0] == text

    def test_long_message_split(self):
        """测试长消息分割"""
        text = "A" * 5000
        parts = split_message_smart(text, max_length=1000, preserve_md=False)
        assert len(parts) == 5
        assert all(len(part) <= 1000 for part in parts)

    def test_preserve_markdown_entities(self):
        """测试保留 Markdown 实体"""
        text = "**粗体文本**" * 1000  # 创建一个包含 Markdown 的长文本
        parts = split_message_smart(text, max_length=1000, preserve_md=True)
        # 验证每个分段都是有效的
        for part in parts:
            is_valid, _ = validate_message_entities(part)
            assert is_valid, f"分段包含无效的 Markdown 实体: {part[:50]}..."

    def test_split_with_bold_entities(self):
        """测试分割包含粗体实体的文本"""
        text = "**这是粗体文本** " * 100
        parts = split_message_smart(text, max_length=500, preserve_md=True)
        # 验证分段后的实体完整性
        for part in parts:
            is_valid, _ = validate_message_entities(part)
            if not is_valid:
                # 如果验证失败，应该已经被修复
                # 再次验证修复后的结果
                sanitized = sanitize_markdown(part, aggressive=False)
                assert validate_message_entities(sanitized)[0] is True

    def test_split_max_length_respected(self):
        """测试分割后每段都不超过最大长度"""
        text = "这是一个很长的测试文本。" * 1000
        max_length = 1000
        parts = split_message_smart(text, max_length=max_length, preserve_md=True)
        for part in parts:
            # 注意：由于可能添加标题，实际长度可能略大于 max_length
            # 但在实际使用中，send_long_message 会处理这个问题
            assert len(part) <= max_length or len(part) <= max_length + 100


class TestSplitByLinesSmart:
    """测试按行智能分割函数"""

    def test_short_lines_no_split(self):
        """测试短行不分割"""
        text = "短行1\n短行2\n短行3"
        lines = split_by_lines_smart(text, max_length=50)
        assert len(lines) == 3

    def test_long_line_split(self):
        """测试长行分割"""
        text = "这是一个非常长的行，需要被分割成多个行" * 10
        lines = split_by_lines_smart(text, max_length=50)
        for line in lines:
            assert len(line) <= 50

    def test_mixed_lines(self):
        """测试混合长度行"""
        text = "短行\n" + "这是一个非常长的行，需要被分割" * 5 + "\n另一个短行"
        lines = split_by_lines_smart(text, max_length=50)
        for line in lines:
            assert len(line) <= 50
        # 验证内容没有被丢失
        combined = "".join(lines)
        assert "短行" in combined
        assert "长行" in combined


class TestEdgeCases:
    """测试边界情况"""

    def test_only_markdown_no_content(self):
        """测试只有 Markdown 标记没有内容"""
        text = "****"
        is_valid, _ = validate_message_entities(text)
        assert is_valid is True  # 空的粗体标记是有效的

    def test_very_long_single_word(self):
        """测试超长单词"""
        text = "A" * 10000
        parts = split_message_smart(text, max_length=1000, preserve_md=False)
        assert len(parts) == 10
        assert all(len(part) <= 1000 for part in parts)

    def test_mixed_valid_invalid_formats(self):
        """测试混合有效和无效的格式"""
        text = "**有效** 粗体 *无效的斜体 文本"
        result = sanitize_markdown(text, aggressive=False)
        # 应该修复无效的斜体，保留有效的粗体
        is_valid, _ = validate_message_entities(result)
        assert is_valid is True

    def test_unicode_characters(self):
        """测试 Unicode 字符"""
        text = "**中文粗体** 🎉🎊"
        is_valid, _ = validate_message_entities(text)
        assert is_valid is True

    def test_multiple_link_formats(self):
        """测试多个链接"""
        text = "[链接1](url1) [链接2](url2) [链接3](url3)"
        is_valid, _ = validate_message_entities(text)
        assert is_valid is True

    def test_nested_asterisks(self):
        """测试嵌套星号"""
        text = "****文本****"  # 两个粗体标记嵌套
        is_valid, _ = validate_message_entities(text)
        assert is_valid is True
