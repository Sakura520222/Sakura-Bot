# Copyright 2026 Sakura-Bot
#
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
#
# - 署名：必须提供本项目的原始来源链接
# - 相同方式共享：衍生作品必须采用相同的许可证
#
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
# 许可证全文：参见 LICENSE 文件

"""
问答Bot配额管理器
"""

import logging
from typing import Any

from core.config.qa_quota import QAQuotaSettings, effective_qa_quota
from core.config.runtime import runtime_config
from core.infrastructure.database import get_db_manager
from core.infrastructure.utils.auth import is_admin as _strict_is_admin

logger = logging.getLogger(__name__)


class QuotaManager:
    """配额管理器"""

    def __init__(self):
        """初始化配额管理器"""
        self.db = get_db_manager()
        self._refresh_limits()
        logger.info(
            f"配额管理器初始化完成: 用户限额={self.daily_limit}, 总限额={self.total_daily_limit}"
        )

    def _refresh_limits(self) -> QAQuotaSettings:
        settings = effective_qa_quota(runtime_config.read())
        self.daily_limit = settings.user_daily_limit
        self.total_daily_limit = settings.total_daily_limit
        return settings

    def is_admin(self, user_id: int) -> bool:
        """检查用户是否为管理员（严格模式，fail-closed）。

        委托 core.infrastructure.utils.auth.is_admin：仅当用户 ID 与
        REPORT_ADMIN_IDS 中显式配置的数字 ID 一致时为 True。
        """
        return _strict_is_admin(user_id)

    async def check_quota(self, user_id: int) -> dict[str, Any]:
        """
        检查用户配额

        Args:
            user_id: 用户ID

        Returns:
            {
                "allowed": bool,  # 是否允许查询
                "remaining": int,  # 剩余次数
                "used": int,  # 已用次数
                "daily_limit": int,  # 用户限额
                "is_admin": bool,  # 是否管理员
                "message": str  # 提示消息
            }
        """
        try:
            limits = self._refresh_limits()
            # 检查是否为管理员
            is_admin = self.is_admin(user_id)

            # 检查每日总限额
            total_used_today = await self.db.get_total_daily_usage()
            if total_used_today >= limits.total_daily_limit and not is_admin:
                logger.warning(f"今日总配额已用尽: {total_used_today}/{limits.total_daily_limit}")
                return {
                    "allowed": False,
                    "remaining": 0,
                    "used": 0,
                    "daily_limit": limits.user_daily_limit,
                    "is_admin": False,
                    "message": f"⏰ **今日配额已用完**\n\n系统今日已处理 {total_used_today} 次查询。\n请在明日配额重置后继续使用。\n\n🌙 **重置时间：每日00:00**",
                }

            # 检查并增加用户配额
            result = await self.db.check_and_increment_quota(
                user_id=user_id, daily_limit=limits.user_daily_limit, is_admin=is_admin
            )

            if not result.get("allowed", False):
                used = result.get("used", 0)
                daily_limit = result.get("daily_limit", limits.user_daily_limit)
                logger.info(f"用户 {user_id} 配额已用尽: {used}/{daily_limit}")
                return {
                    "allowed": False,
                    "remaining": 0,
                    "used": used,
                    "daily_limit": daily_limit,
                    "is_admin": False,
                    "message": f"⏰ **今日配额已用完**\n\n你今天已经使用了 {used} 次查询。\n休息一下，明天配额重置后再来吧。\n\n🌙 **重置时间：每日00:00**",
                }

            # 配额允许
            remaining = result.get("remaining", 0)
            used = result.get("used", 0)

            logger.info(
                f"用户 {user_id} 配额检查通过: {used}/{limits.user_daily_limit} (剩余{remaining})"
            )

            if is_admin:
                message = "🌟 **管理员权限**\n\n你拥有无限制访问的特权。"
            else:
                total_remaining = limits.total_daily_limit - total_used_today - 1
                message = f"✅ **查询成功**\n\n💡 今日剩余次数：{remaining}/{limits.user_daily_limit}\n📊 系统总剩余：{total_remaining}次"

            return {
                "allowed": True,
                "remaining": remaining,
                "used": used,
                "daily_limit": limits.user_daily_limit,
                "is_admin": is_admin,
                "message": message,
            }

        except Exception as e:
            logger.error(f"配额检查失败: {type(e).__name__}: {e}", exc_info=True)
            return {
                "allowed": False,
                "remaining": 0,
                "used": 0,
                "daily_limit": self.daily_limit,
                "is_admin": False,
                "message": "⚠️ **系统错误**\n\n配额检查失败，请稍后再试。",
            }

    async def refund_quota(self, user_id: int) -> bool:
        """回退一次配额使用（内部错误时不扣费）。

        check_quota() 通过"检查并原子递增"完成配额预留；当请求后续处理因内部
        错误失败（而非用户配额不足）时，调用本方法把这一次预留退回，保证普通
        用户不会为失败的请求消耗每日额度。管理员本身不消耗额度，调用无副作用。

        Args:
            user_id: 用户ID

        Returns:
            bool: 是否成功回退
        """
        try:
            if self.is_admin(user_id):
                return True

            refunded = await self.db.refund_quota(user_id)
            if refunded:
                logger.info(f"用户 {user_id} 的请求处理失败，已回退本次配额占用")
            else:
                logger.info(f"用户 {user_id} 无需回退配额（当日无占用记录）")
            return refunded
        except Exception as e:
            logger.error(f"回退配额失败: {type(e).__name__}: {e}", exc_info=True)
            return False

    async def get_usage_status(self, user_id: int) -> dict[str, Any]:
        """
        获取用户使用状态（不消耗配额）

        Args:
            user_id: 用户ID

        Returns:
            使用状态信息
        """
        try:
            limits = self._refresh_limits()
            is_admin = self.is_admin(user_id)
            quota = await self.db.get_quota_usage(user_id)
            total_used = await self.db.get_total_daily_usage()

            if is_admin:
                return {
                    "user_id": user_id,
                    "is_admin": True,
                    "used_today": 0,
                    "remaining": -1,  # -1表示无限制
                    "total_used_today": total_used,
                    "total_limit": limits.total_daily_limit,
                    "message": f"🌟 **管理员状态**\n\n你拥有无限制访问的特权。\n\n📊 今日总使用：{total_used}次",
                }

            used = quota.get("usage_count", 0)
            remaining = max(0, limits.user_daily_limit - used)
            total_remaining = max(0, limits.total_daily_limit - total_used)

            return {
                "user_id": user_id,
                "is_admin": False,
                "used_today": used,
                "remaining": remaining,
                "total_used_today": total_used,
                "total_limit": limits.total_daily_limit,
                "message": f"📊 **使用状态**\n\n📚 今日已使用：{used}次\n💡 今日剩余：{remaining}次\n📊 系统总剩余：{total_remaining}次",
            }

        except Exception as e:
            logger.error(f"获取使用状态失败: {type(e).__name__}: {e}", exc_info=True)
            return {"user_id": user_id, "error": str(e)}

    async def get_system_status(self) -> dict[str, Any]:
        """
        获取系统配额状态

        Returns:
            系统状态信息
        """
        try:
            limits = self._refresh_limits()
            total_used = await self.db.get_total_daily_usage()
            total_remaining = max(0, limits.total_daily_limit - total_used)

            return {
                "daily_limit": limits.total_daily_limit,
                "used_today": total_used,
                "remaining": total_remaining,
                "user_limit": limits.user_daily_limit,
                "utilization": f"{total_used / limits.total_daily_limit * 100:.1f}%",
            }

        except Exception as e:
            logger.error(f"获取系统状态失败: {type(e).__name__}: {e}", exc_info=True)
            return {"error": str(e)}


# 创建全局配额管理器实例
quota_manager = None


def get_quota_manager():
    """获取全局配额管理器实例"""
    global quota_manager
    if quota_manager is None:
        quota_manager = QuotaManager()
    return quota_manager
