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

"""投票数据管理模块

此模块使用 SQLite 数据库管理投票重新生成数据，提供高性能的异步访问。
"""

import asyncio
import logging
from datetime import datetime, timedelta
from pathlib import Path

import aiosqlite

from .constants import CLEANUP_DAYS_DEFAULT, DATABASE_FILE
from .exceptions import DatabaseError

logger = logging.getLogger(__name__)


# 投票重新生成记录类型
PollRegenerationRecord = dict[
    str,  # 'channel', 'summary_msg_id', 'poll_msg_id', 'button_msg_id',
    # 'summary_text', 'channel_name', 'timestamp', 'send_to_channel',
    # 'vote_count', 'voters', 'discussion_forward_msg_id'
    str | int | bool | list[int] | None,
]


class PollRegenerationManager:
    """投票重新生成数据管理器

    使用 SQLite 数据库管理投票重新生成数据，支持高并发访问。
    """

    def __init__(self, db_path: str | None = None):
        """初始化数据管理器

        Args:
            db_path: 数据库文件路径，默认为 data/bot.db
        """
        self._db_path = Path(db_path or DATABASE_FILE)
        self._lock = asyncio.Lock()
        self._initialized = False

    async def _ensure_initialized(self) -> None:
        """确保数据库已初始化"""
        if not self._initialized:
            await self._initialize_database()
            self._initialized = True

    async def _initialize_database(self) -> None:
        """初始化数据库表结构"""
        try:
            # 确保目录存在
            self._db_path.parent.mkdir(parents=True, exist_ok=True)

            async with aiosqlite.connect(self._db_path) as db:
                # 创建投票重新生成表
                await db.execute("""
                    CREATE TABLE IF NOT EXISTS poll_regenerations (
                        id INTEGER PRIMARY KEY AUTOINCREMENT,
                        channel TEXT NOT NULL,
                        summary_msg_id INTEGER NOT NULL,
                        poll_msg_id INTEGER,
                        button_msg_id INTEGER,
                        summary_text TEXT,
                        channel_name TEXT,
                        timestamp DATETIME DEFAULT CURRENT_TIMESTAMP,
                        send_to_channel BOOLEAN DEFAULT 0,
                        vote_count INTEGER DEFAULT 0,
                        discussion_forward_msg_id INTEGER,
                        UNIQUE(channel, summary_msg_id)
                    )
                """)

                # 创建投票者表
                await db.execute("""
                    CREATE TABLE IF NOT EXISTS poll_voters (
                        id INTEGER PRIMARY KEY AUTOINCREMENT,
                        channel TEXT NOT NULL,
                        summary_msg_id INTEGER NOT NULL,
                        user_id INTEGER NOT NULL,
                        voted_at DATETIME DEFAULT CURRENT_TIMESTAMP,
                        UNIQUE(channel, summary_msg_id, user_id),
                        FOREIGN KEY (channel, summary_msg_id)
                            REFERENCES poll_regenerations(channel, summary_msg_id)
                                ON DELETE CASCADE
                    )
                """)

                # 创建索引以提高查询性能
                await db.execute("""
                    CREATE INDEX IF NOT EXISTS idx_poll_regeneration
                    ON poll_regenerations(channel, summary_msg_id)
                """)

                await db.execute("""
                    CREATE INDEX IF NOT EXISTS idx_poll_voters
                    ON poll_voters(channel, summary_msg_id, user_id)
                """)

                await db.commit()

            logger.info("投票重新生成数据库已初始化")
        except Exception as e:
            raise DatabaseError(f"初始化数据库失败: {e}") from e

    async def add_record(
        self,
        channel: str,
        summary_msg_id: int,
        poll_msg_id: int,
        button_msg_id: int,
        summary_text: str,
        channel_name: str,
        send_to_channel: bool,
        discussion_forward_msg_id: int | None = None,
    ) -> None:
        """添加一条投票重新生成记录

        Args:
            channel: 频道 URL
            summary_msg_id: 总结消息 ID
            poll_msg_id: 投票消息 ID
            button_msg_id: 按钮消息 ID
            summary_text: 总结文本
            channel_name: 频道名称
            send_to_channel: 是否发送到频道
            discussion_forward_msg_id: 讨论组中的转发消息 ID（仅讨论组模式）
        """
        await self._ensure_initialized()

        async with self._lock:
            try:
                async with aiosqlite.connect(self._db_path) as db:
                    await db.execute(
                        """
                        INSERT OR REPLACE INTO poll_regenerations
                        (channel, summary_msg_id, poll_msg_id, button_msg_id,
                         summary_text, channel_name, send_to_channel,
                         discussion_forward_msg_id, vote_count)
                        VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0)
                    """,
                        (
                            channel,
                            summary_msg_id,
                            poll_msg_id,
                            button_msg_id,
                            summary_text,
                            channel_name,
                            int(send_to_channel),
                            discussion_forward_msg_id,
                        ),
                    )
                    await db.commit()

                logger.info(
                    f"已添加投票重新生成记录: channel={channel}, summary_id={summary_msg_id}"
                )
            except Exception as e:
                raise DatabaseError(f"添加记录失败: {e}") from e

    async def get_record(self, channel: str, summary_msg_id: int) -> PollRegenerationRecord | None:
        """获取指定的投票重新生成记录

        Args:
            channel: 频道 URL
            summary_msg_id: 总结消息 ID

        Returns:
            投票重新生成记录，如果不存在返回 None
        """
        await self._ensure_initialized()

        try:
            async with aiosqlite.connect(self._db_path) as db:
                db.row_factory = aiosqlite.Row
                async with db.execute(
                    """
                    SELECT * FROM poll_regenerations
                    WHERE channel = ? AND summary_msg_id = ?
                """,
                    (channel, summary_msg_id),
                ) as cursor:
                    row = await cursor.fetchone()

                    if row:
                        # 获取投票者列表
                        voters = []
                        async with db.execute(
                            """
                            SELECT user_id FROM poll_voters
                            WHERE channel = ? AND summary_msg_id = ?
                        """,
                            (channel, summary_msg_id),
                        ) as voter_cursor:
                            async for voter_row in voter_cursor:
                                voters.append(voter_row[0])

                        return dict(row) | {"voters": voters}

                    return None
        except Exception as e:
            raise DatabaseError(f"获取记录失败: {e}") from e

    async def update_message_ids(
        self, channel: str, summary_msg_id: int, poll_msg_id: int, button_msg_id: int
    ) -> None:
        """更新投票重新生成记录的消息 ID

        Args:
            channel: 频道 URL
            summary_msg_id: 总结消息 ID
            poll_msg_id: 新的投票消息 ID
            button_msg_id: 新的按钮消息 ID
        """
        await self._ensure_initialized()

        async with self._lock:
            try:
                async with aiosqlite.connect(self._db_path) as db:
                    await db.execute(
                        """
                        UPDATE poll_regenerations
                        SET poll_msg_id = ?, button_msg_id = ?
                        WHERE channel = ? AND summary_msg_id = ?
                    """,
                        (poll_msg_id, button_msg_id, channel, summary_msg_id),
                    )
                    await db.commit()

                logger.info(
                    f"已更新投票重新生成记录: channel={channel}, summary_id={summary_msg_id}"
                )
            except Exception as e:
                raise DatabaseError(f"更新记录失败: {e}") from e

    async def delete_record(self, channel: str, summary_msg_id: int) -> None:
        """删除指定的投票重新生成记录

        Args:
            channel: 频道 URL
            summary_msg_id: 总结消息 ID
        """
        await self._ensure_initialized()

        async with self._lock:
            try:
                async with aiosqlite.connect(self._db_path) as db:
                    # 删除投票者（CASCADE 会自动处理）
                    await db.execute(
                        """
                        DELETE FROM poll_regenerations
                        WHERE channel = ? AND summary_msg_id = ?
                    """,
                        (channel, summary_msg_id),
                    )
                    await db.commit()

                logger.info(
                    f"已删除投票重新生成记录: channel={channel}, summary_id={summary_msg_id}"
                )
            except Exception as e:
                raise DatabaseError(f"删除记录失败: {e}") from e

    async def increment_vote_count(
        self, channel: str, summary_msg_id: int, user_id: int
    ) -> tuple[bool, int, bool]:
        """增加投票计数

        Args:
            channel: 频道 URL
            summary_msg_id: 总结消息 ID
            user_id: 用户 ID

        Returns:
            tuple: (是否成功增加, 当前计数, 是否已投票)
        """
        await self._ensure_initialized()

        async with self._lock:
            try:
                async with aiosqlite.connect(self._db_path) as db:
                    # 检查记录是否存在
                    async with db.execute(
                        """
                        SELECT vote_count FROM poll_regenerations
                        WHERE channel = ? AND summary_msg_id = ?
                    """,
                        (channel, summary_msg_id),
                    ) as cursor:
                        row = await cursor.fetchone()
                        if not row:
                            logger.warning(
                                f"投票重新生成记录不存在: channel={channel}, "
                                f"summary_id={summary_msg_id}"
                            )
                            return False, 0, False

                    current_count = row[0]

                    # 检查用户是否已投票
                    async with db.execute(
                        """
                        SELECT 1 FROM poll_voters
                        WHERE channel = ? AND summary_msg_id = ? AND user_id = ?
                    """,
                        (channel, summary_msg_id, user_id),
                    ) as cursor:
                        if await cursor.fetchone():
                            logger.info(f"用户 {user_id} 已经投票过了")
                            return False, current_count, True

                    # 增加投票计数
                    await db.execute(
                        """
                        UPDATE poll_regenerations
                        SET vote_count = vote_count + 1
                        WHERE channel = ? AND summary_msg_id = ?
                    """,
                        (channel, summary_msg_id),
                    )

                    # 记录投票者
                    await db.execute(
                        """
                        INSERT INTO poll_voters (channel, summary_msg_id, user_id)
                        VALUES (?, ?, ?)
                    """,
                        (channel, summary_msg_id, user_id),
                    )

                    await db.commit()

                    # 获取更新后的计数
                    async with db.execute(
                        """
                        SELECT vote_count FROM poll_regenerations
                        WHERE channel = ? AND summary_msg_id = ?
                    """,
                        (channel, summary_msg_id),
                    ) as cursor:
                        row = await cursor.fetchone()
                        new_count = row[0]

                    logger.info(
                        f"投票计数已更新: channel={channel}, "
                        f"summary_id={summary_msg_id}, count={new_count}, "
                        f"user_id={user_id}"
                    )

                    return True, new_count, False

            except Exception as e:
                raise DatabaseError(f"增加投票计数失败: {e}") from e

    async def reset_vote_count(self, channel: str, summary_msg_id: int) -> bool:
        """重置投票计数

        Args:
            channel: 频道 URL
            summary_msg_id: 总结消息 ID

        Returns:
            是否成功重置
        """
        await self._ensure_initialized()

        async with self._lock:
            try:
                async with aiosqlite.connect(self._db_path) as db:
                    # 检查记录是否存在
                    async with db.execute(
                        """
                        SELECT 1 FROM poll_regenerations
                        WHERE channel = ? AND summary_msg_id = ?
                    """,
                        (channel, summary_msg_id),
                    ) as cursor:
                        if not await cursor.fetchone():
                            logger.warning(
                                f"投票重新生成记录不存在: channel={channel}, "
                                f"summary_id={summary_msg_id}"
                            )
                            return False

                    # 重置计数
                    await db.execute(
                        """
                        UPDATE poll_regenerations
                        SET vote_count = 0
                        WHERE channel = ? AND summary_msg_id = ?
                    """,
                        (channel, summary_msg_id),
                    )

                    # 删除投票者记录
                    await db.execute(
                        """
                        DELETE FROM poll_voters
                        WHERE channel = ? AND summary_msg_id = ?
                    """,
                        (channel, summary_msg_id),
                    )

                    await db.commit()

                    logger.info(f"投票计数已重置: channel={channel}, summary_id={summary_msg_id}")
                    return True

            except Exception as e:
                raise DatabaseError(f"重置投票计数失败: {e}") from e

    async def get_vote_count(self, channel: str, summary_msg_id: int) -> int:
        """获取投票计数

        Args:
            channel: 频道 URL
            summary_msg_id: 总结消息 ID

        Returns:
            投票计数，如果记录不存在返回 0
        """
        await self._ensure_initialized()

        try:
            async with aiosqlite.connect(self._db_path) as db:
                async with db.execute(
                    """
                    SELECT vote_count FROM poll_regenerations
                    WHERE channel = ? AND summary_msg_id = ?
                """,
                    (channel, summary_msg_id),
                ) as cursor:
                    row = await cursor.fetchone()
                    return row[0] if row else 0
        except Exception as e:
            raise DatabaseError(f"获取投票计数失败: {e}") from e

    async def cleanup_old_records(self, days: int = CLEANUP_DAYS_DEFAULT) -> int:
        """清理超过指定天数的旧记录

        Args:
            days: 保留天数，默认 30 天

        Returns:
            清理的记录数量
        """
        await self._ensure_initialized()

        async with self._lock:
            try:
                cutoff_time = datetime.now() - timedelta(days=days)

                async with aiosqlite.connect(self._db_path) as db:
                    cursor = await db.execute(
                        """
                        DELETE FROM poll_regenerations
                        WHERE timestamp < ?
                    """,
                        (cutoff_time.isoformat(),),
                    )

                    count = cursor.rowcount
                    await db.commit()

                if count > 0:
                    logger.info(f"已清理 {count} 条超过 {days} 天的投票重新生成记录")

                return count

            except Exception as e:
                raise DatabaseError(f"清理旧记录失败: {e}") from e


# 全局数据管理器实例
_poll_regeneration_manager: PollRegenerationManager | None = None


def get_poll_regeneration_manager() -> PollRegenerationManager:
    """获取全局投票重新生成数据管理器（单例模式）

    Returns:
        PollRegenerationManager 实例
    """
    global _poll_regeneration_manager
    if _poll_regeneration_manager is None:
        _poll_regeneration_manager = PollRegenerationManager()
        logger.info("投票重新生成数据管理器已初始化")
    return _poll_regeneration_manager
