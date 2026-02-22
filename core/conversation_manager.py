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
会话管理器 - 管理用户的多轮对话会话
实现会话生命周期管理、超时检测和状态维护
"""

import logging
import uuid
from datetime import UTC, datetime, timedelta

from .database import get_db_manager

logger = logging.getLogger(__name__)


class ConversationManager:
    """会话管理器 - 负责管理用户的对话会话"""

    # 会话超时时间（分钟）
    SESSION_TIMEOUT_MINUTES = 30

    # 每个会话最大保留的消息条数（10轮对话 = 20条消息）
    MAX_MESSAGES_PER_SESSION = 20

    def __init__(self):
        """初始化会话管理器"""
        self.db = get_db_manager()
        # 内存缓存：user_id -> {session_id, last_active}
        self._session_cache: dict[int, dict[str, any]] = {}
        logger.info("会话管理器初始化完成")

    def get_or_create_session(self, user_id: int) -> tuple[str, bool]:
        """
        获取或创建用户会话

        Args:
            user_id: 用户ID

        Returns:
            (session_id, is_new_session) - 会话ID和是否为新会话
        """
        try:
            now = datetime.now(UTC)

            # 检查缓存中的会话
            if user_id in self._session_cache:
                cached = self._session_cache[user_id]
                session_id = cached["session_id"]
                last_active = cached["last_active"]

                # 检查是否超时
                if (now - last_active) < timedelta(minutes=self.SESSION_TIMEOUT_MINUTES):
                    # 会话仍然有效，更新活动时间
                    cached["last_active"] = now
                    logger.debug(f"用户 {user_id} 继续使用会话 {session_id}")
                    return session_id, False
                else:
                    # 会话超时，创建新会话
                    logger.info(f"用户 {user_id} 的会话 {session_id} 已超时")

            # 创建新会话
            session_id = str(uuid.uuid4())
            self._session_cache[user_id] = {"session_id": session_id, "last_active": now}

            logger.info(f"为用户 {user_id} 创建新会话 {session_id}")
            return session_id, True

        except Exception as e:
            logger.error(f"获取/创建会话失败: {type(e).__name__}: {e}", exc_info=True)
            # 降级：返回一个随机会话ID
            return str(uuid.uuid4()), True

    async def save_message(
        self, user_id: int, session_id: str, role: str, content: str, metadata: dict | None = None
    ) -> bool:
        """
        保存对话消息

        Args:
            user_id: 用户ID
            session_id: 会话ID
            role: 角色 ('user' 或 'assistant')
            content: 消息内容
            metadata: 可选的元数据

        Returns:
            是否成功
        """
        try:
            # 保存到数据库
            success = await self.db.save_conversation(
                user_id=user_id,
                session_id=session_id,
                role=role,
                content=content,
                metadata=metadata,
            )

            if success:
                # 更新缓存的活动时间
                if user_id in self._session_cache:
                    self._session_cache[user_id]["last_active"] = datetime.now(UTC)

            return success

        except Exception as e:
            logger.error(f"保存消息失败: {type(e).__name__}: {e}", exc_info=True)
            return False

    async def get_conversation_history(self, user_id: int, session_id: str) -> list:
        """
        获取对话历史（用于RAG上下文）

        Args:
            user_id: 用户ID
            session_id: 会话ID

        Returns:
            对话历史列表，格式：[{'role': 'user', 'content': '...'}, ...]
        """
        try:
            history = await self.db.get_conversation_history(
                user_id=user_id, session_id=session_id, limit=self.MAX_MESSAGES_PER_SESSION
            )
            return history

        except Exception as e:
            logger.error(f"获取对话历史失败: {type(e).__name__}: {e}", exc_info=True)
            return []

    async def clear_user_history(self, user_id: int, session_id: str | None = None) -> int:
        """
        清除用户对话历史

        Args:
            user_id: 用户ID
            session_id: 可选，指定会话ID

        Returns:
            删除的记录数
        """
        try:
            # 从数据库删除
            deleted = await self.db.clear_user_conversations(user_id, session_id)

            # 如果清除了所有会话，清除缓存
            if session_id is None:
                if user_id in self._session_cache:
                    del self._session_cache[user_id]
            else:
                # 如果清除了当前会话，也清除缓存
                if user_id in self._session_cache:
                    if self._session_cache[user_id]["session_id"] == session_id:
                        del self._session_cache[user_id]

            return deleted

        except Exception as e:
            logger.error(f"清除对话历史失败: {type(e).__name__}: {e}", exc_info=True)
            return 0

    def format_conversation_context(self, history: list) -> str:
        """
        格式化对话历史为上下文字符串

        Args:
            history: 对话历史列表

        Returns:
            格式化的上下文字符串
        """
        if not history:
            return ""

        context_parts = []

        for item in history:
            role = item["role"]
            content = item["content"]

            # 角色名称映射
            role_name = "用户" if role == "user" else "助手"

            # 截断过长的消息（避免token浪费）
            if len(content) > 500:
                content = content[:500] + "..."

            context_parts.append(f"{role_name}：{content}")

        return "\n".join(context_parts)

    async def get_session_info(self, user_id: int) -> dict | None:
        """
        获取用户的会话信息

        Args:
            user_id: 用户ID

        Returns:
            会话信息字典
        """
        try:
            if user_id not in self._session_cache:
                return None

            cached = self._session_cache[user_id]
            session_id = cached["session_id"]
            last_active = cached["last_active"]

            # 计算会话时长
            now = datetime.now(UTC)
            duration = now - last_active

            # 获取会话中的消息数
            history = await self.db.get_conversation_history(user_id, session_id, limit=1000)
            message_count = len(history)

            return {
                "session_id": session_id,
                "last_active": last_active.isoformat(),
                "duration_seconds": int(duration.total_seconds()),
                "message_count": message_count,
                "is_active": duration < timedelta(minutes=self.SESSION_TIMEOUT_MINUTES),
            }

        except Exception as e:
            logger.error(f"获取会话信息失败: {type(e).__name__}: {e}", exc_info=True)
            return None

    async def cleanup_old_sessions(self, days: int = 7) -> int:
        """
        清理旧的对话记录（定期维护任务）

        Args:
            days: 保留天数

        Returns:
            删除的记录数
        """
        try:
            deleted = await self.db.delete_old_conversations(days)

            # 清理缓存中不活跃的会话
            now = datetime.now(UTC)
            expired_users = []

            for user_id, cached in self._session_cache.items():
                if (now - cached["last_active"]) > timedelta(minutes=self.SESSION_TIMEOUT_MINUTES):
                    expired_users.append(user_id)

            for user_id in expired_users:
                del self._session_cache[user_id]

            logger.info(f"清理旧会话: 删除{deleted}条记录, 清理{len(expired_users)}个缓存")
            return deleted

        except Exception as e:
            logger.error(f"清理旧会话失败: {type(e).__name__}: {e}", exc_info=True)
            return 0


# 创建全局会话管理器实例
conversation_manager = None


def get_conversation_manager():
    """获取全局会话管理器实例"""
    global conversation_manager
    if conversation_manager is None:
        conversation_manager = ConversationManager()
    return conversation_manager
