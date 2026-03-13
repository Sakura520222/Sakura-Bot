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
SQLite数据库管理器模块

继承原有的DatabaseManagerLegacy，保持所有功能的兼容性
同时提供异步方法包装，以支持统一的异步接口
"""

import asyncio
import os
from typing import Any

from .manager import DatabaseManagerLegacy


class SQLiteManager(DatabaseManagerLegacy):
    """
    SQLite数据库管理器

    继承原有的DatabaseManagerLegacy类，保持所有功能的完全兼容
    所有方法实现都在DatabaseManagerLegacy中，这里只是提供SQLite专用包装
    """

    def __init__(self, db_path=None):
        """
        初始化SQLite数据库管理器

        Args:
            db_path: 数据库文件路径，默认为 data/summaries.db
        """
        if db_path is None:
            db_path = os.path.join("data", "summaries.db")

        # 调用父类构造函数
        super().__init__(db_path)

        # 标记数据库类型
        self._db_type = "sqlite"
        self._db_version = 4

    def get_database_type(self) -> str:
        """获取数据库类型"""
        return "sqlite"

    def get_database_version(self) -> int:
        """获取数据库版本号"""
        return 4

    def get_connection(self):
        """
        获取数据库连接（供迁移器使用）

        Returns:
            sqlite3.Connection
        """
        import sqlite3

        return sqlite3.connect(self.db_path)

    # ============ 异步方法包装层 ============
    # 以下方法将父类的同步方法包装为异步方法，以支持统一的异步接口
    # 这样 SQLite 和 MySQL 数据库管理器可以提供统一的异步 API

    # 通知队列方法
    async def get_pending_notifications(self, limit: int = 50) -> list[dict[str, Any]]:
        """异步包装：获取待发送的通知列表"""
        return await asyncio.to_thread(super().get_pending_notifications, limit)

    async def update_notification_status(self, notification_id: int, status: str) -> bool:
        """更新通知状态（异步包装）"""
        return await asyncio.to_thread(super().update_notification_status, notification_id, status)

    async def cleanup_old_notifications(self, days: int = 7) -> int:
        """清理旧通知记录（异步包装）"""
        return await asyncio.to_thread(super().cleanup_old_notifications, days)

    async def get_qa_bot_statistics(self) -> dict[str, Any]:
        """获取问答Bot统计信息（异步包装）"""
        return await asyncio.to_thread(super().get_qa_bot_statistics)

    # 订阅管理方法
    async def get_channel_subscribers(
        self, channel_id: str, sub_type: str = "summary"
    ) -> list[int]:
        """异步包装：获取频道的订阅用户ID列表"""
        return await asyncio.to_thread(super().get_channel_subscribers, channel_id, sub_type)

    async def remove_subscription(
        self, user_id: int, channel_id: str = None, sub_type: str = None
    ) -> int:
        """异步包装：移除订阅"""
        return await asyncio.to_thread(super().remove_subscription, user_id, channel_id, sub_type)

    async def add_subscription(
        self, user_id: int, channel_id: str, channel_name: str = None, sub_type: str = "summary"
    ) -> bool:
        """异步包装：添加订阅"""
        return await asyncio.to_thread(
            super().add_subscription, user_id, channel_id, channel_name, sub_type
        )

    async def get_user_subscriptions(
        self, user_id: int, sub_type: str = None
    ) -> list[dict[str, Any]]:
        """异步包装：获取用户的订阅列表"""
        return await asyncio.to_thread(super().get_user_subscriptions, user_id, sub_type)

    async def is_subscribed(self, user_id: int, channel_id: str, sub_type: str = "summary") -> bool:
        """异步包装：检查用户是否已订阅某频道"""
        return await asyncio.to_thread(super().is_subscribed, user_id, channel_id, sub_type)

    # 用户管理方法
    async def is_user_registered(self, user_id: int) -> bool:
        """异步包装：检查用户是否已注册"""
        return await asyncio.to_thread(super().is_user_registered, user_id)

    async def register_user(
        self, user_id: int, username: str = None, first_name: str = None, is_admin: bool = False
    ) -> bool:
        """异步包装：注册新用户"""
        return await asyncio.to_thread(
            super().register_user, user_id, username, first_name, is_admin
        )

    async def update_user_activity(self, user_id: int) -> bool:
        """异步包装：更新用户最后活跃时间"""
        return await asyncio.to_thread(super().update_user_activity, user_id)

    async def get_user(self, user_id: int) -> dict[str, Any] | None:
        """异步包装：获取用户信息"""
        return await asyncio.to_thread(super().get_user, user_id)

    async def get_user_info(self, user_id: int) -> dict[str, Any] | None:
        """异步包装：获取用户信息（别名方法，指向 get_user）"""
        # SQLite 的 get_user_info 是 get_user 的别名，需要异步包装
        return await self.get_user(user_id)

    async def get_all_channels(self) -> list[dict[str, Any]]:
        """异步包装：获取所有可用频道"""
        return await asyncio.to_thread(super().get_all_channels)

    # 配额管理方法
    async def get_quota_usage(self, user_id: int, date: str | None = None) -> dict[str, Any]:
        """异步包装：获取用户配额使用情况"""
        return await asyncio.to_thread(super().get_quota_usage, user_id, date)

    async def check_and_increment_quota(
        self, user_id: int, daily_limit: int, is_admin: bool = False
    ) -> dict[str, Any]:
        """异步包装：检查并增加配额使用"""
        return await asyncio.to_thread(
            super().check_and_increment_quota, user_id, daily_limit, is_admin
        )

    async def get_total_daily_usage(self, date: str | None = None) -> int:
        """异步包装：获取指定日期的总使用次数"""
        return await asyncio.to_thread(super().get_total_daily_usage, date)

    # 频道画像方法
    async def get_channel_profile(self, channel_id: str) -> dict[str, Any] | None:
        """异步包装：获取频道画像"""
        return await asyncio.to_thread(super().get_channel_profile, channel_id)

    async def update_channel_profile(
        self,
        channel_id: str,
        channel_name: str,
        keywords: list[str] = None,
        topics: list[str] = None,
        sentiment: str = None,
        entities: list[str] = None,
    ) -> None:
        """异步包装：更新频道画像"""
        return await asyncio.to_thread(
            super().update_channel_profile,
            channel_id,
            channel_name,
            keywords,
            topics,
            sentiment,
            entities,
        )

    # 总结查询方法
    async def get_summaries(
        self,
        channel_id: str | None = None,
        limit: int = 10,
        start_date: Any = None,
        end_date: Any = None,
    ) -> list[dict[str, Any]]:
        """异步包装：查询历史总结"""
        return await asyncio.to_thread(
            super().get_summaries, channel_id, limit, start_date, end_date
        )

    async def save_summary(
        self,
        channel_id: str,
        channel_name: str,
        summary_text: str,
        message_count: int,
        start_time: Any = None,
        end_time: Any = None,
        summary_message_ids: list[int] | None = None,
        poll_message_id: int | None = None,
        button_message_id: int | None = None,
        ai_model: str = "unknown",
        summary_type: str = "weekly",
    ) -> int | None:
        """异步包装：保存总结记录到数据库"""
        return await asyncio.to_thread(
            super().save_summary,
            channel_id,
            channel_name,
            summary_text,
            message_count,
            start_time,
            end_time,
            summary_message_ids,
            poll_message_id,
            button_message_id,
            ai_model,
            summary_type,
        )

    async def get_summary_by_id(self, summary_id: int) -> dict[str, Any] | None:
        """异步包装：根据ID获取单条总结"""
        return await asyncio.to_thread(super().get_summary_by_id, summary_id)

    # 请求队列方法
    async def get_pending_requests(self, limit: int = 10) -> list[dict[str, Any]]:
        """异步包装：获取待处理的请求列表"""
        return await asyncio.to_thread(super().get_pending_requests, limit)

    async def update_request_status(self, request_id: int, status: str, result: Any = None) -> bool:
        """异步包装：更新请求状态"""
        return await asyncio.to_thread(super().update_request_status, request_id, status, result)

    async def get_request_status(self, request_id: int) -> dict[str, Any] | None:
        """异步包装：获取请求状态"""
        return await asyncio.to_thread(super().get_request_status, request_id)

    # 对话历史方法
    async def save_conversation(
        self,
        user_id: int,
        session_id: str,
        role: str,
        content: str,
        metadata: dict[str, Any] | None = None,
    ) -> bool:
        """异步包装：保存对话记录"""
        return await asyncio.to_thread(
            super().save_conversation, user_id, session_id, role, content, metadata
        )

    async def get_conversation_history(
        self, user_id: int, session_id: str, limit: int = 20
    ) -> list[dict[str, Any]]:
        """异步包装：获取用户的对话历史"""
        return await asyncio.to_thread(super().get_conversation_history, user_id, session_id, limit)

    async def clear_user_conversations(self, user_id: int, session_id: str | None = None) -> int:
        """异步包装：清除用户的对话历史"""
        return await asyncio.to_thread(super().clear_user_conversations, user_id, session_id)

    async def delete_old_conversations(self, days: int = 7) -> int:
        """异步包装：删除旧的对话记录"""
        return await asyncio.to_thread(super().delete_old_conversations, days)

    # ============ 周报请求管理方法（异步包装） ============

    async def add_summary_request(
        self,
        channel_id: str,
        message_id: int,
        request_type: str = "manual",
        requested_by: int = None,
    ) -> int | None:
        """异步包装：添加周报请求记录"""
        return await asyncio.to_thread(
            super().add_summary_request, channel_id, message_id, request_type, requested_by
        )

    async def check_pending_summary_request(self, channel_id: str) -> bool:
        """异步包装：检查指定频道是否有待处理的周报请求"""
        return await asyncio.to_thread(super().check_pending_summary_request, channel_id)

    async def get_summary_requests(
        self, channel_id: str = None, status: str = None, limit: int = 20
    ) -> list[dict[str, Any]]:
        """异步包装：获取周报请求列表"""
        return await asyncio.to_thread(super().get_summary_requests, channel_id, status, limit)

    async def update_summary_request_status(self, request_id: int, status: str) -> bool:
        """异步包装：更新周报请求状态"""
        return await asyncio.to_thread(super().update_summary_request_status, request_id, status)

    # ============ 通知队列方法（异步包装） ============

    async def create_notification(
        self, user_id: int, notification_type: str, content: dict[str, Any]
    ) -> int | None:
        """异步包装：创建通知"""
        return await asyncio.to_thread(
            super().create_notification, user_id, notification_type, content
        )

    # ============ 频道消息转发功能方法（异步包装） ============

    async def is_message_forwarded(self, message_id: str, target_channel: str) -> bool:
        """异步包装：检查消息是否已转发"""
        return await asyncio.to_thread(super().is_message_forwarded, message_id, target_channel)

    async def add_forwarded_message(
        self,
        message_id: str,
        source_channel: str,
        target_channel: str,
        content_hash: str = None,
        timestamp: int = None,
    ) -> bool:
        """异步包装：添加已转发消息记录"""
        return await asyncio.to_thread(
            super().add_forwarded_message,
            message_id,
            source_channel,
            target_channel,
            content_hash,
            timestamp,
        )

    async def get_forwarding_stats(self, channel_id: str = None) -> dict[str, Any]:
        """异步包装：获取转发统计信息"""
        return await asyncio.to_thread(super().get_forwarding_stats, channel_id)

    async def update_forwarding_stats(self, channel_id: str, increment: int = 1) -> bool:
        """异步包装：更新频道转发统计"""
        return await asyncio.to_thread(super().update_forwarding_stats, channel_id, increment)

    async def cleanup_old_forwarded_messages(self, days: int = 30) -> int:
        """异步包装：清理旧的转发消息记录"""
        return await asyncio.to_thread(super().cleanup_old_forwarded_messages, days)

    # ============ 评论区会话管理方法（异步包装） ============

    async def save_comment_session(
        self,
        channel_id: str,
        channel_msg_id: int,
        session_id: str,
        discussion_id: int,
    ) -> bool:
        """异步包装：保存或更新评论区会话"""
        return await asyncio.to_thread(
            super().save_comment_session, channel_id, channel_msg_id, session_id, discussion_id
        )

    async def get_comment_session(
        self, channel_id: str, channel_msg_id: int
    ) -> dict[str, Any] | None:
        """异步包装：获取评论区会话"""
        return await asyncio.to_thread(super().get_comment_session, channel_id, channel_msg_id)

    async def update_comment_session_activity(self, session_id: str) -> bool:
        """异步包装：更新评论区会话活动时间"""
        return await asyncio.to_thread(super().update_comment_session_activity, session_id)

    async def save_comment_message(
        self,
        session_id: str,
        user_id: int,
        username: str,
        role: str,
        content: str,
    ) -> bool:
        """异步包装：保存评论区消息"""
        return await asyncio.to_thread(
            super().save_comment_message, session_id, user_id, username, role, content
        )

    async def get_comment_messages(self, session_id: str, limit: int = 50) -> list[dict[str, Any]]:
        """异步包装：获取评论区消息历史"""
        return await asyncio.to_thread(super().get_comment_messages, session_id, limit)

    async def delete_old_comment_sessions(self, days: int = 7) -> int:
        """异步包装：删除旧的评论区会话"""
        return await asyncio.to_thread(super().delete_old_comment_sessions, days)
