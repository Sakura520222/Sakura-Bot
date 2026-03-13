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
频道评论区会话管理模块
管理评论区AI聊天的会话状态和历史记录
"""

import logging
from datetime import UTC, datetime, timedelta

from core.ai.comment_context import CommentContext
from core.handlers.comment_chat_config import get_comment_chat_config, get_comment_session_id
from core.infrastructure.database import get_db_manager

logger = logging.getLogger(__name__)


class CommentConversationManager:
    """评论区会话管理器 - 负责管理评论区的对话会话"""

    # 会话超时时间（分钟）- 从配置读取
    SESSION_TIMEOUT_MINUTES = 30

    # 每个会话最大保留的消息条数
    MAX_MESSAGES_PER_SESSION = 50

    def __init__(self):
        """初始化评论区会话管理器"""
        self.db = get_db_manager()
        # 内存缓存：session_id -> {session_info}
        self._session_cache: dict[str, dict] = {}
        logger.info("评论区会话管理器初始化完成")

    async def _get_session_timeout(self) -> int:
        """获取会话超时时间（从配置读取）"""
        try:
            config = await get_comment_chat_config()
            return config.get("session_timeout", 30)
        except Exception:
            return 30

    async def get_or_create_comment_session(
        self,
        channel_id: str,
        channel_msg_id: int,
        discussion_id: int,
    ) -> tuple[str, bool]:
        """
        获取或创建评论区会话

        注意：评论区会话是"频道消息级别"的共享会话，
        所有参与该评论区的用户共享同一个会话上下文

        Args:
            channel_id: 频道ID
            channel_msg_id: 频道消息ID
            discussion_id: 讨论组ID

        Returns:
            (session_id, is_new_session) - 会话ID和是否为新会话
        """
        try:
            session_id = get_comment_session_id(channel_id, channel_msg_id)
            now = datetime.now(UTC)
            timeout_minutes = await self._get_session_timeout()

            # 1. 检查内存缓存
            if session_id in self._session_cache:
                cached = self._session_cache[session_id]
                last_active = datetime.fromisoformat(cached["last_active"])

                # 检查是否超时
                if (now - last_active) < timedelta(minutes=timeout_minutes):
                    # 会话仍然有效，更新活动时间
                    cached["last_active"] = now.isoformat()
                    logger.debug(f"评论区会话 {session_id} 继续使用")
                    await self.db.update_comment_session_activity(session_id)
                    return session_id, False
                else:
                    # 会话超时，从缓存移除，后续从数据库重新加载
                    logger.info(f"评论区会话 {session_id} 内存缓存超时")
                    del self._session_cache[session_id]

            # 2. 检查数据库中是否存在会话
            db_session = await self.db.get_comment_session(channel_id, channel_msg_id)
            if db_session:
                last_active = datetime.fromisoformat(db_session["last_active"])

                # 检查数据库中的会话是否超时
                if (now - last_active) < timedelta(minutes=timeout_minutes):
                    # 会话仍然有效，恢复到内存缓存
                    self._session_cache[session_id] = {
                        "session_id": session_id,
                        "channel_id": channel_id,
                        "channel_msg_id": channel_msg_id,
                        "discussion_id": discussion_id,
                        "last_active": now.isoformat(),
                    }
                    await self.db.update_comment_session_activity(session_id)
                    logger.info(f"从数据库恢复评论区会话 {session_id}")
                    return session_id, False
                else:
                    # 会话超时，创建新会话
                    logger.info(f"评论区会话 {session_id} 数据库会话超时，创建新会话")

            # 3. 创建新会话
            await self.db.save_comment_session(
                channel_id=channel_id,
                channel_msg_id=channel_msg_id,
                session_id=session_id,
                discussion_id=discussion_id,
            )

            self._session_cache[session_id] = {
                "session_id": session_id,
                "channel_id": channel_id,
                "channel_msg_id": channel_msg_id,
                "discussion_id": discussion_id,
                "last_active": now.isoformat(),
            }

            logger.info(f"创建新评论区会话 {session_id}")
            return session_id, True

        except Exception as e:
            logger.error(f"获取/创建评论区会话失败: {e}", exc_info=True)
            # 降级：返回一个会话ID
            return get_comment_session_id(channel_id, channel_msg_id), True

    async def save_comment_message(
        self,
        session_id: str,
        user_id: int,
        username: str,
        role: str,
        content: str,
    ) -> bool:
        """
        保存评论区对话消息

        Args:
            session_id: 会话ID
            user_id: 用户ID
            username: 用户名
            role: 角色 ('user' 或 'assistant')
            content: 消息内容

        Returns:
            是否成功
        """
        try:
            # 保存到数据库
            success = await self.db.save_comment_message(
                session_id=session_id,
                user_id=user_id,
                username=username,
                role=role,
                content=content,
            )

            if success:
                # 更新缓存的活动时间
                if session_id in self._session_cache:
                    self._session_cache[session_id]["last_active"] = datetime.now(UTC).isoformat()

            return success

        except Exception as e:
            logger.error(f"保存评论区消息失败: {e}", exc_info=True)
            return False

    async def get_comment_conversation_history(self, session_id: str) -> list[dict]:
        """
        获取评论区对话历史（用于RAG上下文）

        Args:
            session_id: 会话ID

        Returns:
            对话历史列表，格式：[{'role': 'user', 'content': '...'}, ...]
        """
        try:
            history = await self.db.get_comment_messages(
                session_id=session_id, limit=self.MAX_MESSAGES_PER_SESSION
            )

            # 转换为标准格式
            formatted_history = []
            for msg in history:
                formatted_history.append(
                    {
                        "role": msg["role"],
                        "content": msg["content"],
                        "username": msg["username"],
                        "id": msg["id"],
                    }
                )

            return formatted_history

        except Exception as e:
            logger.error(f"获取评论区对话历史失败: {e}", exc_info=True)
            return []

    def format_comment_conversation_context(self, history: list, context: CommentContext) -> str:
        """
        格式化评论区对话历史为上下文字符串

        Args:
            history: 对话历史列表
            context: 评论上下文对象

        Returns:
            格式化的上下文字符串
        """
        if not history:
            return ""

        context_parts = []

        for item in history:
            role = item["role"]
            content = item["content"]
            username = item.get("username", "Unknown")

            # 角色名称映射
            if role == "user":
                role_name = username
            elif role == "assistant":
                role_name = "小樱"
            else:
                role_name = role

            # 截断过长的消息（避免token浪费）
            if len(content) > 500:
                content = content[:500] + "..."

            context_parts.append(f"{role_name}: {content}")

        return "\n".join(context_parts)

    async def cleanup_old_sessions(self, days: int = 7) -> int:
        """
        清理旧的评论区会话（定期维护任务）

        Args:
            days: 保留天数

        Returns:
            删除的记录数
        """
        try:
            deleted = await self.db.delete_old_comment_sessions(days)

            # 清理缓存中不活跃的会话
            now = datetime.now(UTC)
            timeout_minutes = await self._get_session_timeout()

            expired_sessions = []
            for session_id, cached in self._session_cache.items():
                last_active = datetime.fromisoformat(cached["last_active"])
                if (now - last_active) > timedelta(minutes=timeout_minutes):
                    expired_sessions.append(session_id)

            for session_id in expired_sessions:
                del self._session_cache[session_id]

            logger.info(f"清理旧评论区会话: 删除{deleted}条记录, 清理{len(expired_sessions)}个缓存")
            return deleted

        except Exception as e:
            logger.error(f"清理旧评论区会话失败: {e}", exc_info=True)
            return 0


# 创建全局评论区会话管理器实例
comment_conversation_manager = None


def get_comment_conversation_manager():
    """获取全局评论区会话管理器实例"""
    global comment_conversation_manager
    if comment_conversation_manager is None:
        comment_conversation_manager = CommentConversationManager()
    return comment_conversation_manager
