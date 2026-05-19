# Copyright 2026 Sakura-Bot
#
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可

"""
投稿数据访问层

提供投稿记录的数据库操作接口。
"""

import json
import logging
from typing import Any

import aiomysql

from core.infrastructure.database.mysql import utc_now_naive

logger = logging.getLogger(__name__)


class SubmissionRepository:
    """投稿数据访问层"""

    def __init__(self, pool=None):
        """初始化投稿仓库

        Args:
            pool: aiomysql 连接池（可选，支持延迟获取）。
                未传入时，首次访问 pool 属性将自动从全局数据库管理器获取。
                调用前提：数据库管理器必须已完成初始化。
        """
        self._pool = pool

    @property
    def pool(self):
        """延迟获取数据库连接池"""
        if self._pool is not None:
            return self._pool
        from core.infrastructure.database.manager import get_db_manager

        db = get_db_manager()
        if db is not None and hasattr(db, "pool") and db.pool is not None:
            self._pool = db.pool
            return self._pool
        raise RuntimeError("数据库连接池尚未初始化")

    async def create_submission(
        self,
        submitter_id: int,
        submitter_name: str,
        title: str,
        content: str | None = None,
        media_files: list[dict[str, Any]] | None = None,
        target_channel: str | None = None,
        is_anonymous: bool = False,
    ) -> int | None:
        """创建投稿记录

        Args:
            submitter_id: 投稿者ID
            submitter_name: 投稿者用户名
            title: 投稿标题
            content: 投稿正文
            media_files: 媒体文件列表
            target_channel: 目标频道
            is_anonymous: 是否匿名投稿

        Returns:
            投稿ID，失败返回None
        """
        try:
            media_json = json.dumps(media_files, ensure_ascii=False) if media_files else None
            async with self.pool.acquire() as conn:
                async with conn.cursor() as cursor:
                    await cursor.execute(
                        """
                        INSERT INTO submissions
                        (submitter_id, submitter_name, title, content, media_files,
                         target_channel, is_anonymous, status)
                        VALUES (%s, %s, %s, %s, %s, %s, %s, 'pending')
                        """,
                        (
                            submitter_id,
                            submitter_name,
                            title,
                            content,
                            media_json,
                            target_channel,
                            is_anonymous,
                        ),
                    )
                    await conn.commit()
                    submission_id = cursor.lastrowid
                    logger.info(f"创建投稿记录成功: ID={submission_id}, 投稿者={submitter_name}")
                    return submission_id
        except Exception as e:
            logger.error(f"创建投稿记录失败: {type(e).__name__}: {e}", exc_info=True)
            return None

    async def get_submission(self, submission_id: int) -> dict[str, Any] | None:
        """获取单条投稿记录

        Args:
            submission_id: 投稿ID

        Returns:
            投稿记录字典，不存在返回None
        """
        try:
            async with self.pool.acquire() as conn:
                async with conn.cursor(aiomysql.DictCursor) as cursor:
                    await cursor.execute(
                        "SELECT * FROM submissions WHERE id = %s", (submission_id,)
                    )
                    row = await cursor.fetchone()
                    if row and row.get("media_files"):
                        try:
                            row["media_files"] = json.loads(row["media_files"])
                        except Exception:
                            row["media_files"] = []
                    return row
        except Exception as e:
            logger.error(f"获取投稿记录失败: {type(e).__name__}: {e}", exc_info=True)
            return None

    async def get_pending_submissions(self) -> list[dict[str, Any]]:
        """获取待审核且尚未通知管理员的投稿

        仅返回 review_message_id 为 NULL 的 pending 投稿，
        避免重复通知管理员。

        Returns:
            待审核投稿列表
        """
        try:
            async with self.pool.acquire() as conn:
                async with conn.cursor(aiomysql.DictCursor) as cursor:
                    await cursor.execute(
                        """SELECT * FROM submissions
                        WHERE status = 'pending' AND review_message_id IS NULL
                        ORDER BY created_at ASC"""
                    )
                    rows = await cursor.fetchall()
                    for row in rows:
                        if row.get("media_files"):
                            try:
                                row["media_files"] = json.loads(row["media_files"])
                            except Exception:
                                row["media_files"] = []
                    return rows
        except Exception as e:
            logger.error(f"获取待审核投稿失败: {type(e).__name__}: {e}", exc_info=True)
            return []

    async def get_user_submissions(self, user_id: int, limit: int = 20) -> list[dict[str, Any]]:
        """获取用户的投稿历史

        Args:
            user_id: 用户ID
            limit: 返回数量限制

        Returns:
            投稿记录列表
        """
        try:
            async with self.pool.acquire() as conn:
                async with conn.cursor(aiomysql.DictCursor) as cursor:
                    await cursor.execute(
                        """SELECT * FROM submissions
                        WHERE submitter_id = %s
                        ORDER BY created_at DESC
                        LIMIT %s""",
                        (user_id, limit),
                    )
                    rows = await cursor.fetchall()
                    for row in rows:
                        if row.get("media_files"):
                            try:
                                row["media_files"] = json.loads(row["media_files"])
                            except Exception:
                                row["media_files"] = []
                    return rows
        except Exception as e:
            logger.error(f"获取用户投稿历史失败: {type(e).__name__}: {e}", exc_info=True)
            return []

    async def update_submission_status(
        self,
        submission_id: int,
        status: str,
        reviewed_by: int | None = None,
        ai_optimized_content: str | None = None,
        ai_optimized_title: str | None = None,
        review_message_id: int | None = None,
        signature_forced: bool | None = None,
        clear_ai_content: bool = False,
    ) -> bool:
        """更新投稿状态

        Args:
            submission_id: 投稿ID
            status: 新状态 (pending/approved/rejected/cancelled)
            reviewed_by: 审核人ID
            ai_optimized_content: AI优化后的内容
            ai_optimized_title: AI优化后的标题
            review_message_id: 审核消息ID
            signature_forced: 是否由管理员强制署名
            clear_ai_content: 是否清除AI优化内容（恢复原文时使用）

        Returns:
            是否更新成功
        """
        try:
            async with self.pool.acquire() as conn:
                async with conn.cursor() as cursor:
                    update_fields = ["status = %s"]
                    params: list[Any] = [status]

                    if reviewed_by is not None:
                        update_fields.append("reviewed_by = %s")
                        params.append(reviewed_by)
                        update_fields.append("reviewed_at = %s")
                        params.append(utc_now_naive())

                    if ai_optimized_content is not None:
                        update_fields.append("ai_optimized_content = %s")
                        params.append(ai_optimized_content)

                    if ai_optimized_title is not None:
                        update_fields.append("ai_optimized_title = %s")
                        params.append(ai_optimized_title)

                    if clear_ai_content:
                        update_fields.append("ai_optimized_content = NULL")
                        update_fields.append("ai_optimized_title = NULL")

                    if review_message_id is not None:
                        update_fields.append("review_message_id = %s")
                        params.append(review_message_id)

                    if signature_forced is not None:
                        update_fields.append("signature_forced = %s")
                        params.append(signature_forced)

                    params.append(submission_id)
                    await cursor.execute(
                        f"UPDATE submissions SET {', '.join(update_fields)} WHERE id = %s",
                        params,
                    )
                    await conn.commit()
                    logger.info(f"更新投稿状态: ID={submission_id}, status={status}")
                    return True
        except Exception as e:
            logger.error(f"更新投稿状态失败: {type(e).__name__}: {e}", exc_info=True)
            return False

    async def get_submission_stats(self) -> dict[str, Any]:
        """获取投稿统计

        Returns:
            统计信息字典
        """
        try:
            async with self.pool.acquire() as conn:
                async with conn.cursor() as cursor:
                    await cursor.execute(
                        """SELECT status, COUNT(*) as count
                        FROM submissions
                        GROUP BY status"""
                    )
                    stats = dict(await cursor.fetchall())
                    return stats
        except Exception as e:
            logger.error(f"获取投稿统计失败: {type(e).__name__}: {e}", exc_info=True)
            return {}


# 全局实例
_submission_repo: SubmissionRepository | None = None


def get_submission_repo() -> SubmissionRepository:
    """获取全局投稿仓库实例"""
    global _submission_repo
    if _submission_repo is not None:
        return _submission_repo

    _submission_repo = SubmissionRepository()
    return _submission_repo
