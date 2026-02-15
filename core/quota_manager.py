# -*- coding: utf-8 -*-
# Copyright 2026 Sakura-频道总结助手
#
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可，
# 并附加非商业使用限制条款。
#
# - 署名：必须提供本项目的原始来源链接
# - 非商业：禁止任何商业用途和分发
# - 相同方式共享：衍生作品必须采用相同的许可证
#
# 本项目源代码：https://github.com/Sakura520222/Sakura-Channel-Summary-Assistant
# 许可证全文：参见 LICENSE 文件

"""
问答Bot配额管理器
"""

import logging
import os
from typing import Dict, Any, Optional
from .database import get_db_manager
from .config import ADMIN_LIST

logger = logging.getLogger(__name__)


class QuotaManager:
    """配额管理器"""

    def __init__(self):
        """初始化配额管理器"""
        self.db = get_db_manager()
        self.daily_limit = self._get_daily_limit()
        self.total_daily_limit = self._get_total_daily_limit()
        logger.info(f"配额管理器初始化完成: 用户限额={self.daily_limit}, 总限额={self.total_daily_limit}")

    def _get_daily_limit(self) -> int:
        """获取每用户每日限额"""
        try:
            limit = int(os.getenv("QA_BOT_USER_LIMIT", "3"))
            return max(1, limit)  # 至少1次
        except ValueError:
            logger.warning("QA_BOT_USER_LIMIT 配置无效，使用默认值3")
            return 3

    def _get_total_daily_limit(self) -> int:
        """获取每日总限额"""
        try:
            limit = int(os.getenv("QA_BOT_DAILY_LIMIT", "200"))
            return max(10, limit)  # 至少10次
        except ValueError:
            logger.warning("QA_BOT_DAILY_LIMIT 配置无效，使用默认值200")
            return 200

    def is_admin(self, user_id: int) -> bool:
        """检查用户是否为管理员"""
        return user_id in ADMIN_LIST or ADMIN_LIST == ['me']

    def check_quota(self, user_id: int) -> Dict[str, Any]:
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
            # 检查是否为管理员
            is_admin = self.is_admin(user_id)
            
            # 检查每日总限额
            total_used_today = self.db.get_total_daily_usage()
            if total_used_today >= self.total_daily_limit and not is_admin:
                logger.warning(f"今日总配额已用尽: {total_used_today}/{self.total_daily_limit}")
                return {
                    "allowed": False,
                    "remaining": 0,
                    "used": 0,
                    "daily_limit": self.daily_limit,
                    "is_admin": False,
                    "message": f"🍃 **世界树的智慧今日已经分享完毕。**\n\n旅者们的求知热情让世界树绽放了{total_used_today}次智慧的火花。\n请在梦境中休息，明日太阳升起时，新的知识将再次绽放。\n\n🌙 **重置时间：每日00:00**"
                }

            # 检查并增加用户配额
            result = self.db.check_and_increment_quota(
                user_id=user_id,
                daily_limit=self.daily_limit,
                is_admin=is_admin
            )

            if not result.get("allowed", False):
                used = result.get("used", 0)
                daily_limit = result.get("daily_limit", self.daily_limit)
                logger.info(f"用户 {user_id} 配额已用尽: {used}/{daily_limit}")
                return {
                    "allowed": False,
                    "remaining": 0,
                    "used": used,
                    "daily_limit": daily_limit,
                    "is_admin": False,
                    "message": f"🍃 **今天的思绪已经有些疲惫了。**\n\n你已经与世界树对话了{used}次，收获了不少智慧吧？\n休息一下，让知识在心中沉淀，明天再来寻找新的答案吧。\n\n🌙 **重置时间：每日00:00**"
                }

            # 配额允许
            remaining = result.get("remaining", 0)
            used = result.get("used", 0)
            
            logger.info(f"用户 {user_id} 配额检查通过: {used}/{self.daily_limit} (剩余{remaining})")
            
            if is_admin:
                message = "🌟 **守护者权限：智慧的大门永远为你敞开**"
            else:
                total_remaining = self.total_daily_limit - total_used_today - 1
                message = f"🍃 **智慧的微风拂过**\n\n✨ 本次对话成功\n💡 今日剩余对话次数：{remaining}/{self.daily_limit}\n🌳 世界树总剩余能量：{total_remaining}次"

            return {
                "allowed": True,
                "remaining": remaining,
                "used": used,
                "daily_limit": self.daily_limit,
                "is_admin": is_admin,
                "message": message
            }

        except Exception as e:
            logger.error(f"配额检查失败: {type(e).__name__}: {e}", exc_info=True)
            return {
                "allowed": False,
                "remaining": 0,
                "used": 0,
                "daily_limit": self.daily_limit,
                "is_admin": False,
                "message": "🌫️ **迷雾暂时遮蔽了世界树的感知**\n\n请稍后再试，让迷雾散去..."
            }

    def get_usage_status(self, user_id: int) -> Dict[str, Any]:
        """
        获取用户使用状态（不消耗配额）
        
        Args:
            user_id: 用户ID
            
        Returns:
            使用状态信息
        """
        try:
            is_admin = self.is_admin(user_id)
            quota = self.db.get_quota_usage(user_id)
            total_used = self.db.get_total_daily_usage()

            if is_admin:
                return {
                    "user_id": user_id,
                    "is_admin": True,
                    "used_today": 0,
                    "remaining": -1,  # -1表示无限制
                    "total_used_today": total_used,
                    "total_limit": self.total_daily_limit,
                    "message": "🌟 **守护者状态**\n\n你拥有访问世界树根系的特权，智慧的大门永远为你敞开。\n\n📊 今日总使用：{total_used}次"
                }

            used = quota.get("usage_count", 0)
            remaining = max(0, self.daily_limit - used)
            total_remaining = max(0, self.total_daily_limit - total_used)

            return {
                "user_id": user_id,
                "is_admin": False,
                "used_today": used,
                "remaining": remaining,
                "total_used_today": total_used,
                "total_limit": self.total_daily_limit,
                "message": f"🍃 **与世界树的连接状态**\n\n📚 今日已获取智慧：{used}次\n💡 今日剩余机会：{remaining}次\n🌳 世界树总能量：{total_remaining}次\n\n珍惜每次对话，让思考更有深度。"
            }

        except Exception as e:
            logger.error(f"获取使用状态失败: {type(e).__name__}: {e}", exc_info=True)
            return {
                "user_id": user_id,
                "error": str(e)
            }

    def get_system_status(self) -> Dict[str, Any]:
        """
        获取系统配额状态
        
        Returns:
            系统状态信息
        """
        try:
            total_used = self.db.get_total_daily_usage()
            total_remaining = max(0, self.total_daily_limit - total_used)
            
            return {
                "daily_limit": self.total_daily_limit,
                "used_today": total_used,
                "remaining": total_remaining,
                "user_limit": self.daily_limit,
                "utilization": f"{total_used / self.total_daily_limit * 100:.1f}%"
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