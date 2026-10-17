"""测试 Quota Manager 模块

Copyright 2026 Sakura-Bot
本项目采用 AGPL-3.0 许可
"""

from unittest.mock import AsyncMock, MagicMock, patch

import pytest

from core.ai.quota_manager import QuotaManager, get_quota_manager


@pytest.mark.unit
class TestQuotaManagerInit:
    """配额管理器初始化测试"""

    @patch("core.ai.quota_manager.get_db_manager")
    def test_init_with_default_limits(self, mock_get_db):
        """测试使用默认限额初始化"""
        mock_get_db.return_value = MagicMock()

        with patch.dict("os.environ", {}, clear=True):
            qm = QuotaManager()

            assert qm.daily_limit == 3
            assert qm.total_daily_limit == 200

    @patch("core.ai.quota_manager.get_db_manager")
    def test_init_with_custom_limits(self, mock_get_db):
        """测试使用自定义限额初始化"""
        mock_get_db.return_value = MagicMock()

        with patch.dict("os.environ", {"QA_BOT_USER_LIMIT": "10", "QA_BOT_DAILY_LIMIT": "500"}):
            qm = QuotaManager()

            assert qm.daily_limit == 10
            assert qm.total_daily_limit == 500

    @patch("core.ai.quota_manager.get_db_manager")
    def test_init_with_invalid_limits(self, mock_get_db):
        """测试无效限额使用默认值"""
        mock_get_db.return_value = MagicMock()

        with patch.dict(
            "os.environ", {"QA_BOT_USER_LIMIT": "invalid", "QA_BOT_DAILY_LIMIT": "abc"}
        ):
            qm = QuotaManager()

            assert qm.daily_limit == 3  # 默认值
            assert qm.total_daily_limit == 200  # 默认值

    @patch("core.ai.quota_manager.get_db_manager")
    def test_init_with_zero_limits(self, mock_get_db):
        """测试零限额使用最小值"""
        mock_get_db.return_value = MagicMock()

        with patch.dict("os.environ", {"QA_BOT_USER_LIMIT": "0", "QA_BOT_DAILY_LIMIT": "0"}):
            qm = QuotaManager()

            assert qm.daily_limit >= 1  # 至少1次
            assert qm.total_daily_limit >= 10  # 至少10次


@pytest.mark.unit
class TestIsAdmin:
    """管理员检查测试"""

    @patch("core.ai.quota_manager.get_db_manager")
    def test_is_admin_in_list(self, mock_get_db):
        """测试用户在管理员列表"""
        mock_get_db.return_value = MagicMock()

        with patch("core.infrastructure.utils.auth.ADMIN_LIST", [123, 456]):
            qm = QuotaManager()
            assert qm.is_admin(123) is True
            assert qm.is_admin(456) is True
            assert qm.is_admin(789) is False

    @patch("core.ai.quota_manager.get_db_manager")
    def test_is_admin_empty_list_fail_closed(self, mock_get_db):
        """测试未配置管理员时任何人都不具备管理员权限（fail-closed）"""
        mock_get_db.return_value = MagicMock()

        with patch("core.infrastructure.utils.auth.ADMIN_LIST", []):
            qm = QuotaManager()
            assert qm.is_admin(123) is False
            assert qm.is_admin(456) is False

    @patch("core.ai.quota_manager.get_db_manager")
    def test_is_admin_me_wildcard_removed(self, mock_get_db):
        """回归测试（Issue #112）：ADMIN_LIST=['me'] 不再是全员管理员通配。

        历史实现中 `user_id in ADMIN_LIST or ADMIN_LIST == ["me"]` 会在未配置
        REPORT_ADMIN_IDS 时把所有用户当作管理员，导致越权与无限配额。
        """
        mock_get_db.return_value = MagicMock()

        with patch("core.infrastructure.utils.auth.ADMIN_LIST", ["me"]):
            qm = QuotaManager()
            assert qm.is_admin(123) is False
            assert qm.is_admin(456) is False


@pytest.mark.unit
class TestCheckQuota:
    """配额检查测试"""

    @pytest.mark.asyncio
    @patch("core.ai.quota_manager.get_db_manager")
    async def test_check_quota_admin_allowed(self, mock_get_db):
        """测试管理员配额检查通过"""
        mock_db = MagicMock()
        mock_db.get_total_daily_usage = AsyncMock(return_value=50)
        mock_db.check_and_increment_quota = AsyncMock(
            return_value={"allowed": True, "remaining": -1, "used": 0}
        )
        mock_get_db.return_value = mock_db

        with patch("core.infrastructure.utils.auth.ADMIN_LIST", [123]):
            qm = QuotaManager()
            result = await qm.check_quota(123)

            assert result["allowed"] is True
            assert result["is_admin"] is True
            assert "管理员" in result["message"]

    @pytest.mark.asyncio
    @patch("core.ai.quota_manager.get_db_manager")
    async def test_check_quota_total_limit_exceeded(self, mock_get_db):
        """测试每日总限额超限"""
        mock_db = MagicMock()
        mock_db.get_total_daily_usage = AsyncMock(return_value=200)
        mock_get_db.return_value = mock_db

        with patch("core.infrastructure.utils.auth.ADMIN_LIST", []):
            qm = QuotaManager()
            result = await qm.check_quota(123)

            assert result["allowed"] is False
            assert "今日配额已用完" in result["message"]
            assert result["remaining"] == 0

    @pytest.mark.asyncio
    @patch("core.ai.quota_manager.get_db_manager")
    async def test_check_quota_user_limit_exceeded(self, mock_get_db):
        """测试用户限额超限"""
        mock_db = MagicMock()
        mock_db.get_total_daily_usage = AsyncMock(return_value=50)
        mock_db.check_and_increment_quota = AsyncMock(
            return_value={"allowed": False, "used": 3, "daily_limit": 3}
        )
        mock_get_db.return_value = mock_db

        with patch("core.infrastructure.utils.auth.ADMIN_LIST", []):
            qm = QuotaManager()
            result = await qm.check_quota(123)

            assert result["allowed"] is False
            assert "今日配额已用完" in result["message"]
            assert result["used"] == 3

    @pytest.mark.asyncio
    @patch("core.ai.quota_manager.get_db_manager")
    async def test_check_quota_success(self, mock_get_db):
        """测试配额检查成功"""
        mock_db = MagicMock()
        mock_db.get_total_daily_usage = AsyncMock(return_value=50)
        mock_db.check_and_increment_quota = AsyncMock(
            return_value={"allowed": True, "remaining": 2, "used": 1}
        )
        mock_get_db.return_value = mock_db

        with patch("core.infrastructure.utils.auth.ADMIN_LIST", []):
            qm = QuotaManager()
            result = await qm.check_quota(123)

            assert result["allowed"] is True
            assert result["remaining"] == 2
            assert result["used"] == 1
            assert "查询成功" in result["message"]

    @pytest.mark.asyncio
    @patch("core.ai.quota_manager.get_db_manager")
    async def test_check_quota_error(self, mock_get_db):
        """测试配额检查错误"""
        mock_db = MagicMock()
        mock_db.get_total_daily_usage = AsyncMock(side_effect=Exception("DB错误"))
        mock_get_db.return_value = mock_db

        with patch("core.infrastructure.utils.auth.ADMIN_LIST", []):
            qm = QuotaManager()
            result = await qm.check_quota(123)

            assert result["allowed"] is False
            assert "系统错误" in result["message"]


@pytest.mark.unit
class TestGetUsageStatus:
    """获取使用状态测试"""

    @pytest.mark.asyncio
    @patch("core.ai.quota_manager.get_db_manager")
    async def test_get_usage_status_admin(self, mock_get_db):
        """测试获取管理员使用状态"""
        mock_db = MagicMock()
        mock_db.get_quota_usage = AsyncMock(return_value={"usage_count": 0})
        mock_db.get_total_daily_usage = AsyncMock(return_value=100)
        mock_get_db.return_value = mock_db

        with patch("core.infrastructure.utils.auth.ADMIN_LIST", [123]):
            qm = QuotaManager()
            result = await qm.get_usage_status(123)

            assert result["is_admin"] is True
            assert result["remaining"] == -1  # 无限制
            assert "管理员" in result["message"]

    @pytest.mark.asyncio
    @patch("core.ai.quota_manager.get_db_manager")
    async def test_get_usage_status_normal_user(self, mock_get_db):
        """测试获取普通用户使用状态"""
        mock_db = MagicMock()
        mock_db.get_quota_usage = AsyncMock(return_value={"usage_count": 2})
        mock_db.get_total_daily_usage = AsyncMock(return_value=50)
        mock_get_db.return_value = mock_db

        with patch("core.infrastructure.utils.auth.ADMIN_LIST", []):
            qm = QuotaManager()
            result = await qm.get_usage_status(123)

            assert result["is_admin"] is False
            assert result["used_today"] == 2
            assert result["remaining"] == 1  # 3 - 2
            assert "使用状态" in result["message"]

    @pytest.mark.asyncio
    @patch("core.ai.quota_manager.get_db_manager")
    async def test_get_usage_status_error(self, mock_get_db):
        """测试获取使用状态错误"""
        mock_db = MagicMock()
        mock_db.get_quota_usage = AsyncMock(side_effect=Exception("DB错误"))
        mock_get_db.return_value = mock_db

        with patch("core.infrastructure.utils.auth.ADMIN_LIST", []):
            qm = QuotaManager()
            result = await qm.get_usage_status(123)

            assert "error" in result


@pytest.mark.unit
class TestGetSystemStatus:
    """获取系统状态测试"""

    @pytest.mark.asyncio
    @patch("core.ai.quota_manager.get_db_manager")
    async def test_get_system_status_success(self, mock_get_db):
        """测试获取系统状态成功"""
        mock_db = MagicMock()
        mock_db.get_total_daily_usage = AsyncMock(return_value=100)
        mock_get_db.return_value = mock_db

        qm = QuotaManager()
        result = await qm.get_system_status()

        assert result["daily_limit"] == 200
        assert result["used_today"] == 100
        assert result["remaining"] == 100
        assert result["user_limit"] == 3
        assert "50.0%" in result["utilization"]

    @pytest.mark.asyncio
    @patch("core.ai.quota_manager.get_db_manager")
    async def test_get_system_status_exhausted(self, mock_get_db):
        """测试系统配额耗尽"""
        mock_db = MagicMock()
        mock_db.get_total_daily_usage = AsyncMock(return_value=200)
        mock_get_db.return_value = mock_db

        qm = QuotaManager()
        result = await qm.get_system_status()

        assert result["remaining"] == 0
        assert "100.0%" in result["utilization"]

    @pytest.mark.asyncio
    @patch("core.ai.quota_manager.get_db_manager")
    async def test_get_system_status_error(self, mock_get_db):
        """测试获取系统状态错误"""
        mock_db = MagicMock()
        mock_db.get_total_daily_usage = AsyncMock(side_effect=Exception("DB错误"))
        mock_get_db.return_value = mock_db

        qm = QuotaManager()
        result = await qm.get_system_status()

        assert "error" in result


@pytest.mark.unit
class TestRefundQuota:
    """配额回退测试（Issue #112：内部失败不扣费）"""

    @pytest.mark.asyncio
    @patch("core.ai.quota_manager.get_db_manager")
    async def test_refund_quota_normal_user(self, mock_get_db):
        """测试普通用户回退配额"""
        mock_db = MagicMock()
        mock_db.refund_quota = AsyncMock(return_value=True)
        mock_get_db.return_value = mock_db

        with patch("core.infrastructure.utils.auth.ADMIN_LIST", []):
            qm = QuotaManager()
            result = await qm.refund_quota(123)

            assert result is True
            mock_db.refund_quota.assert_awaited_once_with(123)

    @pytest.mark.asyncio
    @patch("core.ai.quota_manager.get_db_manager")
    async def test_refund_quota_admin_noop(self, mock_get_db):
        """测试管理员回退配额为无操作（管理员不消耗额度）"""
        mock_db = MagicMock()
        mock_db.refund_quota = AsyncMock(return_value=True)
        mock_get_db.return_value = mock_db

        with patch("core.infrastructure.utils.auth.ADMIN_LIST", [123]):
            qm = QuotaManager()
            result = await qm.refund_quota(123)

            assert result is True
            mock_db.refund_quota.assert_not_awaited()

    @pytest.mark.asyncio
    @patch("core.ai.quota_manager.get_db_manager")
    async def test_refund_quota_no_usage(self, mock_get_db):
        """测试当日无占用记录时回退无副作用"""
        mock_db = MagicMock()
        mock_db.refund_quota = AsyncMock(return_value=False)
        mock_get_db.return_value = mock_db

        with patch("core.infrastructure.utils.auth.ADMIN_LIST", []):
            qm = QuotaManager()
            result = await qm.refund_quota(123)

            assert result is False

    @pytest.mark.asyncio
    @patch("core.ai.quota_manager.get_db_manager")
    async def test_refund_quota_error_fail_safe(self, mock_get_db):
        """测试回退配额异常时不向上抛出"""
        mock_db = MagicMock()
        mock_db.refund_quota = AsyncMock(side_effect=Exception("DB错误"))
        mock_get_db.return_value = mock_db

        with patch("core.infrastructure.utils.auth.ADMIN_LIST", []):
            qm = QuotaManager()
            result = await qm.refund_quota(123)

            assert result is False


@pytest.mark.unit
class TestGetQuotaManager:
    """获取配额管理器实例测试"""

    @patch("core.ai.quota_manager.get_db_manager")
    def test_get_quota_manager_singleton(self, mock_get_db):
        """测试单例模式"""
        mock_get_db.return_value = MagicMock()

        # 重置全局变量
        import core.ai.quota_manager

        core.ai.quota_manager.quota_manager = None

        qm1 = get_quota_manager()
        qm2 = get_quota_manager()

        assert qm1 is qm2

    @patch("core.ai.quota_manager.get_db_manager")
    def test_get_quota_manager_creates_instance(self, mock_get_db):
        """测试创建实例"""
        mock_get_db.return_value = MagicMock()

        # 重置全局变量
        import core.ai.quota_manager

        core.ai.quota_manager.quota_manager = None

        qm = get_quota_manager()

        assert isinstance(qm, QuotaManager)
        assert qm.daily_limit >= 1


if __name__ == "__main__":
    pytest.main([__file__, "-v"])
