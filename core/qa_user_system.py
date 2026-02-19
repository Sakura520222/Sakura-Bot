# -*- coding: utf-8 -*-
# Copyright 2026 Sakura-Bot
#
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可，
# 并附加非商业使用限制条款。
#
# - 署名：必须提供本项目的原始来源链接
# - 非商业：禁止任何商业用途和分发
# - 相同方式共享：衍生作品必须采用相同的许可证
#
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
# 许可证全文：参见 LICENSE 文件

"""
问答Bot用户系统管理
处理用户注册、订阅管理和跨Bot请求
"""

import logging
from typing import Dict, Any, List, Optional
from .database import get_db_manager

logger = logging.getLogger(__name__)


class QAUserSystem:
    """问答Bot用户系统管理类"""

    def __init__(self):
        """初始化用户系统"""
        self.db = get_db_manager()
        logger.info("问答Bot用户系统初始化完成")

    def register_user(self, user_id: int, username: str = None,
                     first_name: str = None) -> Dict[str, Any]:
        """
        注册用户

        Args:
            user_id: Telegram用户ID
            username: 用户名
            first_name: 名字

        Returns:
            注册结果字典
        """
        try:
            # 检查是否已注册
            if self.db.is_user_registered(user_id):
                # 更新活跃时间
                self.db.update_user_activity(user_id)
                return {
                    "success": True,
                    "new_user": False,
                    "message": "欢迎回来！"
                }

            # 注册新用户
            success = self.db.register_user(user_id, username, first_name)

            if success:
                return {
                    "success": True,
                    "new_user": True,
                    "message": "注册成功！欢迎使用智能资讯助手。"
                }
            else:
                return {
                    "success": False,
                    "message": "注册失败，请稍后重试。"
                }

        except Exception as e:
            logger.error(f"用户注册失败: {type(e).__name__}: {e}", exc_info=True)
            return {
                "success": False,
                "message": "注册时发生错误。"
            }

    def get_available_channels(self) -> List[Dict[str, Any]]:
        """
        获取可订阅的频道列表

        Returns:
            频道列表
        """
        try:
            channels = self.db.get_all_channels()
            return channels
        except Exception as e:
            logger.error(f"获取频道列表失败: {type(e).__name__}: {e}", exc_info=True)
            return []

    def add_subscription(self, user_id: int, channel_id: str,
                        channel_name: str = None) -> Dict[str, Any]:
        """
        添加订阅

        Args:
            user_id: 用户ID
            channel_id: 频道URL
            channel_name: 频道名称

        Returns:
            操作结果字典
        """
        try:
            # 检查是否已订阅
            if self.db.is_subscribed(user_id, channel_id):
                return {
                    "success": False,
                    "message": f"您已经订阅了此频道的总结推送。"
                }

            # 添加订阅
            success = self.db.add_subscription(user_id, channel_id, channel_name, 'summary')

            if success:
                return {
                    "success": True,
                    "message": f"✅ 已订阅 {channel_name} 的总结推送\n\n当该频道有新总结时，我会及时通知您！"
                }
            else:
                return {
                    "success": False,
                    "message": "订阅失败，请稍后重试。"
                }

        except Exception as e:
            logger.error(f"添加订阅失败: {type(e).__name__}: {e}", exc_info=True)
            return {
                "success": False,
                "message": "订阅时发生错误。"
            }

    def remove_subscription(self, user_id: int, channel_id: str) -> Dict[str, Any]:
        """
        移除订阅

        Args:
            user_id: 用户ID
            channel_id: 频道URL

        Returns:
            操作结果字典
        """
        try:
            deleted_count = self.db.remove_subscription(user_id, channel_id, 'summary')

            if deleted_count > 0:
                return {
                    "success": True,
                    "message": "✅ 已取消该频道的订阅"
                }
            else:
                return {
                    "success": False,
                    "message": "未找到该频道的订阅记录。"
                }

        except Exception as e:
            logger.error(f"移除订阅失败: {type(e).__name__}: {e}", exc_info=True)
            return {
                "success": False,
                "message": "取消订阅时发生错误。"
            }

    def get_user_subscriptions(self, user_id: int) -> List[Dict[str, Any]]:
        """
        获取用户订阅列表

        Args:
            user_id: 用户ID

        Returns:
            订阅列表
        """
        try:
            subscriptions = self.db.get_user_subscriptions(user_id, 'summary')
            return subscriptions
        except Exception as e:
            logger.error(f"获取用户订阅失败: {type(e).__name__}: {e}", exc_info=True)
            return []

    def create_summary_request(self, user_id: int, channel_id: str,
                              channel_name: str = None) -> Dict[str, Any]:
        """
        创建总结请求（发送给主Bot）

        Args:
            user_id: 请求者用户ID
            channel_id: 频道URL
            channel_name: 频道名称

        Returns:
            请求结果字典
        """
        try:
            # 创建请求记录
            request_id = self.db.create_request(
                request_type='summary',
                requested_by=user_id,
                target_channel=channel_id,
                params={'channel_name': channel_name}
            )

            if request_id:
                return {
                    "success": True,
                    "request_id": request_id,
                    "message": f"📝 已向管理员提交总结请求\n\n频道: {channel_name}\n请求ID: {request_id}\n\n请等待管理员确认并生成总结..."
                }
            else:
                return {
                    "success": False,
                    "message": "创建请求失败，请稍后重试。"
                }

        except Exception as e:
            logger.error(f"创建总结请求失败: {type(e).__name__}: {e}", exc_info=True)
            return {
                "success": False,
                "message": "创建请求时发生错误。"
            }

    def get_request_status(self, request_id: int) -> Optional[Dict[str, Any]]:
        """
        获取请求状态

        Args:
            request_id: 请求ID

        Returns:
            请求状态字典，不存在返回None
        """
        try:
            return self.db.get_request_status(request_id)
        except Exception as e:
            logger.error(f"获取请求状态失败: {type(e).__name__}: {e}", exc_info=True)
            return None

    def format_channels_list(self, channels: List[Dict[str, Any]]) -> str:
        """
        格式化频道列表为可读文本

        Args:
            channels: 频道列表

        Returns:
            格式化文本
        """
        if not channels:
            return "暂无可订阅的频道。"

        lines = ["📋 **可订阅频道列表**\n"]
        
        for i, channel in enumerate(channels, 1):
            channel_name = channel.get('channel_name', '未知频道')
            channel_id = channel.get('channel_id', '')
            last_time = channel.get('last_summary_time', '')[:10]
            lines.append(f"{i}. **{channel_name}**")
            lines.append(f"   链接: `{channel_id}`")
            lines.append(f"   最后更新: {last_time}")
            lines.append("")

        lines.append("💡 使用方法:")
        lines.append("`/subscribe <频道链接>`")
        lines.append("")
        lines.append("示例:")
        lines.append(f"`/subscribe {channels[0].get('channel_id', 'https://t.me/channel_name')}`")

        return "\n".join(lines)

    def format_subscriptions_list(self, subscriptions: List[Dict[str, Any]]) -> str:
        """
        格式化订阅列表为可读文本

        Args:
            subscriptions: 订阅列表

        Returns:
            格式化文本
        """
        if not subscriptions:
            return "您还没有订阅任何频道。\n\n💡 使用 `/listchannels` 查看可订阅频道"

        lines = ["📚 **我的订阅**\n"]
        
        for i, sub in enumerate(subscriptions, 1):
            channel_name = sub.get('channel_name', sub.get('channel_id', '未知频道'))
            channel_id = sub.get('channel_id', '')
            created_at = sub.get('created_at', '')[:10]
            lines.append(f"{i}. **{channel_name}**")
            lines.append(f"   链接: `{channel_id}`")
            lines.append(f"   订阅时间: {created_at}")
            lines.append("")

        lines.append("💡 使用方法:")
        lines.append("`/unsubscribe <频道链接>`")
        lines.append("")
        lines.append("示例:")
        if subscriptions:
            lines.append(f"`/unsubscribe {subscriptions[0].get('channel_id', 'https://t.me/channel_name')}`")

        return "\n".join(lines)


# 创建全局用户系统实例
qa_user_system = None


def get_qa_user_system():
    """获取全局用户系统实例"""
    global qa_user_system
    if qa_user_system is None:
        qa_user_system = QAUserSystem()
    return qa_user_system