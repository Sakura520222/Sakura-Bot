"""管理员权限判定回归测试（Issue #112：fail-closed）

Copyright 2026 Sakura-Bot
本项目采用 AGPL-3.0 许可
"""

from types import SimpleNamespace
from unittest.mock import AsyncMock, patch

import pytest

import core.infrastructure.utils.auth as auth_module
from core.infrastructure.utils.auth import check_admin_permission, is_admin


@pytest.mark.unit
class TestIsAdminStrict:
    """is_admin 严格判定测试"""

    def test_explicit_admin_id_matches(self):
        """显式配置的数字用户 ID 通过检查"""
        with patch.object(auth_module, "ADMIN_LIST", [123456]):
            assert is_admin(123456) is True
            assert is_admin("123456") is True  # 数字字符串可安全转换

    def test_non_admin_denied(self):
        """未配置的用户被拒绝"""
        with patch.object(auth_module, "ADMIN_LIST", [123456]):
            assert is_admin(999) is False

    def test_empty_list_fail_closed(self):
        """未配置管理员时（空列表）所有用户被拒绝（fail-closed）"""
        with patch.object(auth_module, "ADMIN_LIST", []):
            assert is_admin(123456) is False
            assert is_admin(0) is False

    def test_me_wildcard_removed(self):
        """回归测试（Issue #112）：['me'] 不再是全员管理员通配值"""
        with patch.object(auth_module, "ADMIN_LIST", ["me"]):
            assert is_admin(123456) is False
            assert is_admin("me") is False

    def test_none_and_invalid_input(self):
        """非法输入一律拒绝，不抛异常"""
        with patch.object(auth_module, "ADMIN_LIST", [123]):
            assert is_admin(None) is False
            assert is_admin("abc") is False
            assert is_admin("12.5") is False


@pytest.mark.unit
class TestCheckAdminPermissionEvent:
    """事件级权限检查测试"""

    @pytest.mark.asyncio
    async def test_denies_non_admin_and_replies(self):
        """非管理员被拒绝并收到拒绝回复"""
        event = SimpleNamespace(sender_id=999, text="/stats", reply=AsyncMock())

        with patch.object(auth_module, "ADMIN_LIST", [123]):
            assert await check_admin_permission(event) is False

        event.reply.assert_awaited_once()

    @pytest.mark.asyncio
    async def test_allows_admin_without_reply(self):
        """管理员通过检查，不发送拒绝回复"""
        event = SimpleNamespace(sender_id=123, text="/stats", reply=AsyncMock())

        with patch.object(auth_module, "ADMIN_LIST", [123]):
            assert await check_admin_permission(event) is True

        event.reply.assert_not_awaited()

    @pytest.mark.asyncio
    async def test_event_without_sender_id_denied(self):
        """缺少 sender_id 的事件被拒绝"""
        event = SimpleNamespace(text="/stats", reply=AsyncMock())

        assert await check_admin_permission(event) is False

    @pytest.mark.asyncio
    async def test_me_wildcard_removed_at_event_level(self):
        """回归测试（Issue #112）：['me'] 兜底值下事件级检查同样拒绝所有人"""
        event = SimpleNamespace(sender_id=123, text="/stats", reply=AsyncMock())

        with patch.object(auth_module, "ADMIN_LIST", ["me"]):
            assert await check_admin_permission(event) is False


@pytest.mark.unit
class TestWebDepsAdminCheck:
    """WebUI 管理员校验测试（core.web_api.deps.check_admin_permission）"""

    def test_strict_membership_check(self):
        from core.web_api import deps

        with patch.object(auth_module, "ADMIN_LIST", [123456]):
            assert deps.check_admin_permission(123456) is True
            assert deps.check_admin_permission("123456") is True
            assert deps.check_admin_permission(999) is False

    def test_empty_list_fail_closed(self):
        """未配置管理员时 WebUI 管理权限对所有人关闭"""
        from core.web_api import deps

        with patch.object(auth_module, "ADMIN_LIST", []):
            assert deps.check_admin_permission(123456) is False

    def test_me_wildcard_removed(self):
        """回归测试（Issue #112）：['me'] 不再让所有人通过 WebUI 管理校验"""
        from core.web_api import deps

        with patch.object(auth_module, "ADMIN_LIST", ["me"]):
            assert deps.check_admin_permission(123456) is False


if __name__ == "__main__":
    pytest.main([__file__, "-v"])
