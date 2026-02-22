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
数据库抽象基类模块

定义数据库管理器的统一接口，支持多种数据库实现（SQLite、MySQL等）
"""

from abc import ABC, abstractmethod
from datetime import datetime
from typing import Any


class DatabaseManagerBase(ABC):
    """数据库管理器抽象基类"""

    @abstractmethod
    def init_database(self):
        """初始化数据库和表结构"""
        pass

    # ============ 总结记录管理方法 ============

    @abstractmethod
    def save_summary(
        self,
        channel_id: str,
        channel_name: str,
        summary_text: str,
        message_count: int,
        start_time: datetime | None = None,
        end_time: datetime | None = None,
        summary_message_ids: list[int] | None = None,
        poll_message_id: int | None = None,
        button_message_id: int | None = None,
        ai_model: str = "unknown",
        summary_type: str = "weekly",
    ) -> int | None:
        """保存总结记录到数据库"""
        pass

    @abstractmethod
    def get_summaries(
        self,
        channel_id: str | None = None,
        limit: int = 10,
        start_date: datetime | None = None,
        end_date: datetime | None = None,
    ) -> list[dict[str, Any]]:
        """查询历史总结"""
        pass

    @abstractmethod
    def get_summary_by_id(self, summary_id: int) -> dict[str, Any] | None:
        """根据ID获取单条总结"""
        pass

    @abstractmethod
    def delete_old_summaries(self, days: int = 90) -> int:
        """删除旧总结记录"""
        pass

    @abstractmethod
    def get_statistics(self, channel_id: str | None = None) -> dict[str, Any]:
        """获取统计信息"""
        pass

    @abstractmethod
    def get_channel_ranking(self, limit: int = 10) -> list[dict[str, Any]]:
        """获取频道排行(按总结次数)"""
        pass

    @abstractmethod
    def export_summaries(
        self, output_format: str = "json", channel_id: str | None = None
    ) -> str | None:
        """导出历史记录"""
        pass

    # ============ 配额管理方法 ============

    @abstractmethod
    def get_quota_usage(self, user_id: int, date: str | None = None) -> dict[str, Any]:
        """获取用户配额使用情况"""
        pass

    @abstractmethod
    def check_and_increment_quota(
        self, user_id: int, daily_limit: int, is_admin: bool = False
    ) -> dict[str, Any]:
        """检查并增加配额使用"""
        pass

    @abstractmethod
    def get_total_daily_usage(self, date: str | None = None) -> int:
        """获取指定日期的总使用次数"""
        pass

    @abstractmethod
    def reset_quota_if_new_day(self, user_id: int) -> None:
        """如果是新的一天，重置用户配额"""
        pass

    # ============ 频道画像方法 ============

    @abstractmethod
    def get_channel_profile(self, channel_id: str) -> dict[str, Any] | None:
        """获取频道画像"""
        pass

    @abstractmethod
    def update_channel_profile(
        self,
        channel_id: str,
        channel_name: str,
        keywords: list[str] = None,
        topics: list[str] = None,
        sentiment: str = None,
        entities: list[str] = None,
    ) -> None:
        """更新频道画像"""
        pass

    @abstractmethod
    def update_summary_metadata(
        self,
        summary_id: int,
        keywords: list[str] = None,
        topics: list[str] = None,
        sentiment: str = None,
        entities: list[str] = None,
    ) -> None:
        """更新总结的元数据"""
        pass

    # ============ 对话历史管理方法 ============

    @abstractmethod
    def save_conversation(
        self,
        user_id: int,
        session_id: str,
        role: str,
        content: str,
        metadata: dict[str, Any] | None = None,
    ) -> bool:
        """保存对话记录"""
        pass

    @abstractmethod
    def get_conversation_history(
        self, user_id: int, session_id: str, limit: int = 20
    ) -> list[dict[str, Any]]:
        """获取用户的对话历史"""
        pass

    @abstractmethod
    def get_last_session_time(self, user_id: int) -> str | None:
        """获取用户最后一次对话时间"""
        pass

    @abstractmethod
    def clear_user_conversations(self, user_id: int, session_id: str | None = None) -> int:
        """清除用户的对话历史"""
        pass

    @abstractmethod
    def get_session_count(self, user_id: int) -> int:
        """获取用户的会话总数"""
        pass

    @abstractmethod
    def delete_old_conversations(self, days: int = 7) -> int:
        """删除旧的对话记录（定期清理）"""
        pass

    # ============ 用户管理方法 ============

    @abstractmethod
    def register_user(
        self, user_id: int, username: str = None, first_name: str = None, is_admin: bool = False
    ) -> bool:
        """注册新用户"""
        pass

    @abstractmethod
    def get_user(self, user_id: int) -> dict[str, Any] | None:
        """获取用户信息"""
        pass

    @abstractmethod
    def update_user_activity(self, user_id: int) -> bool:
        """更新用户最后活跃时间"""
        pass

    @abstractmethod
    def set_user_admin(self, user_id: int, is_admin: bool) -> bool:
        """设置用户管理员权限"""
        pass

    @abstractmethod
    def get_registered_users(self, active_days: int = 30, limit: int = 100) -> list[dict[str, Any]]:
        """获取注册用户列表"""
        pass

    @abstractmethod
    def is_user_registered(self, user_id: int) -> bool:
        """检查用户是否已注册"""
        pass

    # ============ 订阅管理方法 ============

    @abstractmethod
    def add_subscription(
        self, user_id: int, channel_id: str, channel_name: str = None, sub_type: str = "summary"
    ) -> bool:
        """添加订阅"""
        pass

    @abstractmethod
    def remove_subscription(
        self, user_id: int, channel_id: str = None, sub_type: str = None
    ) -> int:
        """移除订阅"""
        pass

    @abstractmethod
    def get_user_subscriptions(self, user_id: int, sub_type: str = None) -> list[dict[str, Any]]:
        """获取用户的订阅列表"""
        pass

    @abstractmethod
    def get_channel_subscribers(self, channel_id: str, sub_type: str = "summary") -> list[int]:
        """获取频道的订阅用户ID列表"""
        pass

    @abstractmethod
    def get_all_channels(self) -> list[dict[str, Any]]:
        """获取所有可用频道（从summaries表中提取）"""
        pass

    @abstractmethod
    def is_subscribed(self, user_id: int, channel_id: str, sub_type: str = "summary") -> bool:
        """检查用户是否已订阅某频道"""
        pass

    # ============ 请求队列方法 ============

    @abstractmethod
    def create_request(
        self,
        request_type: str,
        requested_by: int,
        target_channel: str = None,
        params: dict[str, Any] = None,
    ) -> int | None:
        """创建请求"""
        pass

    @abstractmethod
    def get_pending_requests(self, limit: int = 10) -> list[dict[str, Any]]:
        """获取待处理的请求列表"""
        pass

    @abstractmethod
    def update_request_status(self, request_id: int, status: str, result: Any = None) -> bool:
        """更新请求状态"""
        pass

    @abstractmethod
    def get_request_status(self, request_id: int) -> dict[str, Any] | None:
        """获取请求状态"""
        pass

    @abstractmethod
    def cleanup_old_requests(self, days: int = 7) -> int:
        """清理旧请求记录"""
        pass

    # ============ 通知队列方法 ============

    @abstractmethod
    def create_notification(
        self, user_id: int, notification_type: str, content: dict[str, Any]
    ) -> int | None:
        """创建通知"""
        pass

    @abstractmethod
    def get_pending_notifications(self, limit: int = 50) -> list[dict[str, Any]]:
        """获取待发送的通知列表"""
        pass

    @abstractmethod
    def update_notification_status(self, notification_id: int, status: str) -> bool:
        """更新通知状态"""
        pass

    @abstractmethod
    def cleanup_old_notifications(self, days: int = 7) -> int:
        """清理旧通知记录"""
        pass

    # ============ 通用查询方法 ============

    @abstractmethod
    def get_user_info(self, user_id: int) -> dict[str, Any] | None:
        """获取用户信息（别名方法，兼容请求处理器）"""
        pass

    @abstractmethod
    def get_qa_bot_statistics(self) -> dict[str, Any]:
        """获取问答Bot的详细统计信息"""
        pass

    # ============ 数据库类型和版本信息 ============

    @abstractmethod
    def get_database_type(self) -> str:
        """获取数据库类型（sqlite/mysql）"""
        pass

    @abstractmethod
    def get_database_version(self) -> int:
        """获取数据库版本号"""
        pass

    @abstractmethod
    def close(self):
        """关闭数据库连接"""
        pass
