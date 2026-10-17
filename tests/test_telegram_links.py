"""Telegram 帖子链接构造测试（Issue 来源链接修复）

Copyright 2026 Sakura-Bot
本项目采用 AGPL-3.0 许可
"""

import pytest

from core.infrastructure.utils.telegram_links import build_telegram_post_link


@pytest.mark.unit
class TestPublicChannelLinks:
    """公开频道（username）链接构造"""

    @pytest.mark.parametrize(
        "channel",
        [
            "test_channel",
            "@test_channel",
            "https://t.me/test_channel",
            "http://t.me/test_channel",
            "https://t.me/test_channel/",
            "t.me/test_channel",
            "https://telegram.me/test_channel",
        ],
    )
    def test_username_variants(self, channel):
        """各种 username 写法都归一化为公开频道链接"""
        assert build_telegram_post_link(channel, 123) == "https://t.me/test_channel/123"

    def test_short_username_rejected(self):
        """过短的 username（<4 位）不生成链接"""
        assert build_telegram_post_link("abc", 1) is None

    def test_invalid_username_rejected(self):
        """非法字符的 username 不生成链接"""
        assert build_telegram_post_link("1abc", 1) is None
        assert build_telegram_post_link("not a channel", 1) is None


@pytest.mark.unit
class TestPrivateChannelLinks:
    """私有/数字 ID 频道链接构造（/c/ 格式）"""

    @pytest.mark.parametrize(
        "channel",
        [
            "2039687763",
            "https://t.me/c/2039687763",
            "https://t.me/c/2039687763/9973",  # 已带帖子的路径
            "-1002039687763",  # Bot API 标记 ID
        ],
    )
    def test_numeric_channel_variants(self, channel):
        """数字频道 ID 一律使用 /c/ 私有格式（回归：曾错误拼成公开链接）"""
        assert build_telegram_post_link(channel, 9973) == "https://t.me/c/2039687763/9973"

    def test_numeric_channel_id_as_int(self):
        """int 类型的频道 ID 同样处理"""
        assert build_telegram_post_link(2039687763, 42) == "https://t.me/c/2039687763/42"


@pytest.mark.unit
class TestInvalidInputs:
    """非法输入返回 None"""

    def test_invite_link_rejected(self):
        """邀请链接（+开头）无法定位具体帖子"""
        assert build_telegram_post_link("https://t.me/+AbCdEfGh", 1) is None
        assert build_telegram_post_link("+AbCdEfGh", 1) is None

    def test_invalid_message_id(self):
        """非法消息 ID 返回 None"""
        assert build_telegram_post_link("test_channel", "abc") is None
        assert build_telegram_post_link("test_channel", 0) is None
        assert build_telegram_post_link("test_channel", -5) is None
        assert build_telegram_post_link("test_channel", None) is None

    def test_empty_channel(self):
        """空频道标识返回 None"""
        assert build_telegram_post_link(None, 1) is None
        assert build_telegram_post_link("", 1) is None
        assert build_telegram_post_link("   ", 1) is None

    def test_c_path_with_non_numeric_rejected(self):
        """c/ 后跟非数字的路径不生成链接"""
        assert build_telegram_post_link("https://t.me/c/abc", 1) is None


@pytest.mark.unit
class TestIssueRegression:
    """实际事故场景回归（v2.0.0 部署中发现的错误链接）"""

    def test_reported_broken_link_now_private_format(self):
        """t.me/2039687763/9973 → t.me/c/2039687763/9973"""
        assert build_telegram_post_link("2039687763", 9973) == "https://t.me/c/2039687763/9973"

    def test_hash_id_never_used_as_message_id(self):
        """内部哈希（如 1119685268）不会被链接函数特殊处理：
        它只能作为普通数字消息 ID 传入；修复后该哈希根本不会再出现在
        检索结果中（见 test_vector_store / test_agent_tools）"""
        # 即使误传，也只是按普通消息 ID 构造，无哈希语义
        assert build_telegram_post_link("2039687763", 1119685268) == (
            "https://t.me/c/2039687763/1119685268"
        )


if __name__ == "__main__":
    pytest.main([__file__, "-v"])
