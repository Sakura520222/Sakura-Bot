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

import json
import logging
import os
import sqlite3
from datetime import UTC, datetime, timedelta
from typing import Any

from .i18n import get_text

logger = logging.getLogger(__name__)


# ⚠️ 警告：此类已废弃，请使用 get_db_manager() 函数获取数据库管理器
# 保留此类仅用于向后兼容，将来的版本可能会移除
class DatabaseManagerLegacy:
    """总结历史记录数据库管理器（已废弃）"""

    def __init__(self, db_path=None):
        """
        初始化数据库管理器

        Args:
            db_path: 数据库文件路径，默认为 data/summaries.db
        """
        import warnings
        warnings.warn(
            "DatabaseManager 类已废弃，请使用 get_db_manager() 函数获取数据库管理器实例",
            DeprecationWarning,
            stacklevel=2
        )
        
        if db_path is None:
            db_path = os.path.join("data", "summaries.db")
        self.db_path = db_path
        self.init_database()
        logger.info(f"数据库管理器初始化完成: {db_path}")

    def init_database(self):
        """初始化数据库和表结构"""
        try:
            conn = sqlite3.connect(self.db_path)
            cursor = conn.cursor()

            # 启用WAL模式以支持多进程并发读写
            cursor.execute("PRAGMA journal_mode=WAL")
            cursor.execute("PRAGMA synchronous=NORMAL")
            cursor.execute("PRAGMA busy_timeout=5000")  # 5秒超时
            logger.info("已启用SQLite WAL模式以支持多进程并发")

            # 创建总结记录主表（扩展元数据字段）
            cursor.execute("""
                CREATE TABLE IF NOT EXISTS summaries (
                    id INTEGER PRIMARY KEY AUTOINCREMENT,
                    channel_id TEXT NOT NULL,
                    channel_name TEXT,
                    summary_text TEXT NOT NULL,
                    message_count INTEGER DEFAULT 0,
                    start_time TIMESTAMP,
                    end_time TIMESTAMP,
                    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
                    ai_model TEXT,
                    summary_type TEXT DEFAULT 'weekly',
                    summary_message_ids TEXT,
                    poll_message_id INTEGER,
                    button_message_id INTEGER,
                    keywords TEXT,
                    topics TEXT,
                    sentiment TEXT,
                    entities TEXT
                )
            """)

            # 创建索引以提升查询性能
            cursor.execute("""
                CREATE INDEX IF NOT EXISTS idx_channel_created
                ON summaries(channel_id, created_at DESC)
            """)

            cursor.execute("""
                CREATE INDEX IF NOT EXISTS idx_created
                ON summaries(created_at DESC)
            """)

            cursor.execute("""
                CREATE INDEX IF NOT EXISTS idx_channel
                ON summaries(channel_id)
            """)

            # 创建数据库版本管理表
            cursor.execute("""
                CREATE TABLE IF NOT EXISTS db_version (
                    version INTEGER PRIMARY KEY,
                    upgraded_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
                )
            """)

            # 创建配额管理表
            cursor.execute("""
                CREATE TABLE IF NOT EXISTS usage_quota (
                    id INTEGER PRIMARY KEY AUTOINCREMENT,
                    user_id INTEGER NOT NULL,
                    query_date TEXT NOT NULL,
                    usage_count INTEGER DEFAULT 0,
                    last_reset TEXT DEFAULT CURRENT_TIMESTAMP,
                    UNIQUE(user_id, query_date)
                )
            """)

            # 创建频道画像表
            cursor.execute("""
                CREATE TABLE IF NOT EXISTS channel_profiles (
                    channel_id TEXT PRIMARY KEY,
                    channel_name TEXT,
                    style TEXT DEFAULT 'neutral',
                    topics TEXT,
                    keywords_freq TEXT,
                    tone TEXT,
                    avg_message_length REAL DEFAULT 0,
                    total_summaries INTEGER DEFAULT 0,
                    last_updated TEXT DEFAULT CURRENT_TIMESTAMP
                )
            """)

            # 创建对话历史表（多轮对话支持）
            cursor.execute("""
                CREATE TABLE IF NOT EXISTS conversation_history (
                    id INTEGER PRIMARY KEY AUTOINCREMENT,
                    user_id INTEGER NOT NULL,
                    session_id TEXT NOT NULL,
                    role TEXT NOT NULL,
                    content TEXT NOT NULL,
                    timestamp TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
                    metadata TEXT
                )
            """)

            # 创建用户注册表
            cursor.execute("""
                CREATE TABLE IF NOT EXISTS users (
                    user_id INTEGER PRIMARY KEY,
                    username TEXT,
                    first_name TEXT,
                    registered_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
                    last_active TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
                    is_admin BOOLEAN DEFAULT 0,
                    preferences TEXT
                )
            """)

            # 创建订阅配置表
            cursor.execute("""
                CREATE TABLE IF NOT EXISTS subscriptions (
                    id INTEGER PRIMARY KEY AUTOINCREMENT,
                    user_id INTEGER NOT NULL,
                    channel_id TEXT NOT NULL,
                    channel_name TEXT,
                    sub_type TEXT DEFAULT 'summary',
                    enabled BOOLEAN DEFAULT 1,
                    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
                    UNIQUE(user_id, channel_id, sub_type)
                )
            """)

            # 创建请求队列表（用于IPC）
            cursor.execute("""
                CREATE TABLE IF NOT EXISTS request_queue (
                    id INTEGER PRIMARY KEY AUTOINCREMENT,
                    request_type TEXT NOT NULL,
                    requested_by INTEGER NOT NULL,
                    target_channel TEXT,
                    params TEXT,
                    status TEXT DEFAULT 'pending',
                    result TEXT,
                    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
                    processed_at TIMESTAMP
                )
            """)

            # 创建通知队列表（用于跨Bot通知）
            cursor.execute("""
                CREATE TABLE IF NOT EXISTS notification_queue (
                    id INTEGER PRIMARY KEY AUTOINCREMENT,
                    user_id INTEGER NOT NULL,
                    notification_type TEXT NOT NULL,
                    content TEXT,
                    status TEXT DEFAULT 'pending',
                    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
                    sent_at TIMESTAMP
                )
            """)

            # 为新表创建索引
            cursor.execute("""
                CREATE INDEX IF NOT EXISTS idx_quota_user_date
                ON usage_quota(user_id, query_date)
            """)

            cursor.execute("""
                CREATE INDEX IF NOT EXISTS idx_quota_date
                ON usage_quota(query_date)
            """)

            # 用户表索引
            cursor.execute("""
                CREATE INDEX IF NOT EXISTS idx_users_last_active
                ON users(last_active DESC)
            """)

            # 订阅表索引
            cursor.execute("""
                CREATE INDEX IF NOT EXISTS idx_subscriptions_user
                ON subscriptions(user_id)
            """)

            cursor.execute("""
                CREATE INDEX IF NOT EXISTS idx_subscriptions_channel
                ON subscriptions(channel_id)
            """)

            # 请求队列索引
            cursor.execute("""
                CREATE INDEX IF NOT EXISTS idx_request_queue_status
                ON request_queue(status, created_at)
            """)

            cursor.execute("""
                CREATE INDEX IF NOT EXISTS idx_request_queue_user
                ON request_queue(requested_by)
            """)

            # 插入或更新版本号
            cursor.execute("""
                INSERT OR REPLACE INTO db_version (version, upgraded_at)
                VALUES (3, CURRENT_TIMESTAMP)
            """)

            conn.commit()
            conn.close()

            logger.info("数据库表结构初始化成功")

        except Exception as e:
            logger.error(f"初始化数据库失败: {type(e).__name__}: {e}", exc_info=True)
            raise

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
    ):
        """
        保存总结记录到数据库

        Args:
            channel_id: 频道URL
            channel_name: 频道名称
            summary_text: 总结内容
            message_count: 消息数量
            start_time: 总结起始时间
            end_time: 总结结束时间
            summary_message_ids: 总结消息ID列表
            poll_message_id: 投票消息ID
            button_message_id: 按钮消息ID
            ai_model: AI模型名称
            summary_type: 总结类型 (daily/weekly/manual)
        """
        try:
            conn = sqlite3.connect(self.db_path)
            cursor = conn.cursor()

            # 将列表转换为JSON字符串存储
            summary_ids_json = json.dumps(summary_message_ids) if summary_message_ids else None

            # 格式化时间
            start_time_str = start_time.isoformat() if start_time else None
            end_time_str = end_time.isoformat() if end_time else None

            cursor.execute(
                """
                INSERT INTO summaries (
                    channel_id, channel_name, summary_text, message_count,
                    start_time, end_time, ai_model, summary_type,
                    summary_message_ids, poll_message_id, button_message_id
                ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            """,
                (
                    channel_id,
                    channel_name,
                    summary_text,
                    message_count,
                    start_time_str,
                    end_time_str,
                    ai_model,
                    summary_type,
                    summary_ids_json,
                    poll_message_id,
                    button_message_id,
                ),
            )

            conn.commit()
            summary_id = cursor.lastrowid
            conn.close()

            logger.info(f"成功保存总结记录到数据库, ID: {summary_id}, 频道: {channel_name}")
            return summary_id

        except Exception as e:
            logger.error(f"保存总结记录失败: {type(e).__name__}: {e}", exc_info=True)
            return None

    def get_summaries(
        self,
        channel_id: str | None = None,
        limit: int = 10,
        start_date: datetime | None = None,
        end_date: datetime | None = None,
    ) -> list[dict[str, Any]]:
        """
        查询历史总结

        Args:
            channel_id: 可选，频道URL，不指定则查询所有频道
            limit: 返回记录数量，默认10条
            start_date: 可选，起始日期
            end_date: 可选，结束日期

        Returns:
            总结记录列表
        """
        try:
            conn = sqlite3.connect(self.db_path)
            conn.row_factory = sqlite3.Row  # 使用字典格式返回结果
            cursor = conn.cursor()

            # 构建查询条件
            conditions = []
            params = []

            if channel_id:
                conditions.append("channel_id = ?")
                params.append(channel_id)

            if start_date:
                conditions.append("created_at >= ?")
                params.append(start_date.isoformat())

            if end_date:
                conditions.append("created_at <= ?")
                params.append(end_date.isoformat())

            where_clause = " AND ".join(conditions) if conditions else "1=1"

            query = f"""
                SELECT * FROM summaries
                WHERE {where_clause}
                ORDER BY created_at DESC
                LIMIT ?
            """
            params.append(limit)

            cursor.execute(query, params)
            rows = cursor.fetchall()
            conn.close()

            # 转换为字典列表
            summaries = []
            for row in rows:
                summary = dict(row)

                # 解析JSON字段
                if summary["summary_message_ids"]:
                    try:
                        summary["summary_message_ids"] = json.loads(summary["summary_message_ids"])
                    except:
                        summary["summary_message_ids"] = []
                else:
                    summary["summary_message_ids"] = []

                summaries.append(summary)

            logger.info(f"查询到 {len(summaries)} 条总结记录")
            return summaries

        except Exception as e:
            logger.error(f"查询总结记录失败: {type(e).__name__}: {e}", exc_info=True)
            return []

    def get_summary_by_id(self, summary_id: int) -> dict[str, Any] | None:
        """
        根据ID获取单条总结

        Args:
            summary_id: 总结记录ID

        Returns:
            总结记录字典，不存在则返回None
        """
        try:
            conn = sqlite3.connect(self.db_path)
            conn.row_factory = sqlite3.Row
            cursor = conn.cursor()

            cursor.execute("SELECT * FROM summaries WHERE id = ?", (summary_id,))
            row = cursor.fetchone()
            conn.close()

            if row:
                summary = dict(row)
                # 解析JSON字段
                if summary["summary_message_ids"]:
                    try:
                        summary["summary_message_ids"] = json.loads(summary["summary_message_ids"])
                    except:
                        summary["summary_message_ids"] = []
                else:
                    summary["summary_message_ids"] = []

                return summary
            return None

        except Exception as e:
            logger.error(
                f"查询总结记录失败 (ID={summary_id}): {type(e).__name__}: {e}", exc_info=True
            )
            return None

    def delete_old_summaries(self, days: int = 90) -> int:
        """
        删除旧总结记录

        Args:
            days: 保留天数，默认90天

        Returns:
            删除的记录数
        """
        try:
            conn = sqlite3.connect(self.db_path)
            cursor = conn.cursor()

            cutoff_date = datetime.now(UTC) - timedelta(days=days)

            cursor.execute(
                """
                DELETE FROM summaries
                WHERE created_at < ?
            """,
                (cutoff_date.isoformat(),),
            )

            deleted_count = cursor.rowcount
            conn.commit()
            conn.close()

            logger.info(f"已删除 {deleted_count} 条旧总结记录 (超过 {days} 天)")
            return deleted_count

        except Exception as e:
            logger.error(f"删除旧总结记录失败: {type(e).__name__}: {e}", exc_info=True)
            return 0

    def get_statistics(self, channel_id: str | None = None) -> dict[str, Any]:
        """
        获取统计信息

        Args:
            channel_id: 可选，频道URL，不指定则统计所有频道

        Returns:
            统计信息字典
        """
        try:
            conn = sqlite3.connect(self.db_path)
            cursor = conn.cursor()

            channel_condition = "WHERE channel_id = ?" if channel_id else ""
            params = [channel_id] if channel_id else []

            # 总总结次数
            cursor.execute(
                f"""
                SELECT COUNT(*) FROM summaries
                {channel_condition}
            """,
                params,
            )
            total_count = cursor.fetchone()[0]

            # 按类型统计
            cursor.execute(
                f"""
                SELECT summary_type, COUNT(*) as count
                FROM summaries
                {channel_condition}
                GROUP BY summary_type
            """,
                params,
            )
            type_stats = dict(cursor.fetchall())

            # 总消息数
            cursor.execute(
                f"""
                SELECT SUM(message_count) FROM summaries
                {channel_condition}
            """,
                params,
            )
            total_messages = cursor.fetchone()[0] or 0

            # 平均消息数
            avg_messages = total_messages / total_count if total_count > 0 else 0

            # 最近总结时间
            cursor.execute(
                f"""
                SELECT MAX(created_at) FROM summaries
                {channel_condition}
            """,
                params,
            )
            last_summary_time = cursor.fetchone()[0]

            # 本周统计
            week_ago = (datetime.now(UTC) - timedelta(days=7)).isoformat()
            if channel_id:
                cursor.execute(
                    """
                    SELECT COUNT(*) FROM summaries
                    WHERE channel_id = ? AND created_at >= ?
                """,
                    [channel_id, week_ago],
                )
            else:
                cursor.execute(
                    """
                    SELECT COUNT(*) FROM summaries
                    WHERE created_at >= ?
                """,
                    [week_ago],
                )
            week_count = cursor.fetchone()[0]

            # 本月统计
            month_ago = (datetime.now(UTC) - timedelta(days=30)).isoformat()
            if channel_id:
                cursor.execute(
                    """
                    SELECT COUNT(*) FROM summaries
                    WHERE channel_id = ? AND created_at >= ?
                """,
                    [channel_id, month_ago],
                )
            else:
                cursor.execute(
                    """
                    SELECT COUNT(*) FROM summaries
                    WHERE created_at >= ?
                """,
                    [month_ago],
                )
            month_count = cursor.fetchone()[0]

            conn.close()

            stats = {
                "total_count": total_count,
                "type_stats": type_stats,
                "total_messages": total_messages,
                "avg_messages": round(avg_messages, 1),
                "last_summary_time": last_summary_time,
                "week_count": week_count,
                "month_count": month_count,
            }

            logger.info(f"统计数据获取成功: {stats}")
            return stats

        except Exception as e:
            logger.error(f"获取统计信息失败: {type(e).__name__}: {e}", exc_info=True)
            return {}

    def get_channel_ranking(self, limit: int = 10) -> list[dict[str, Any]]:
        """
        获取频道排行(按总结次数)

        Args:
            limit: 返回记录数量

        Returns:
            频道排行列表
        """
        try:
            conn = sqlite3.connect(self.db_path)
            conn.row_factory = sqlite3.Row
            cursor = conn.cursor()

            cursor.execute(
                """
                SELECT
                    channel_id,
                    channel_name,
                    COUNT(*) as summary_count,
                    SUM(message_count) as total_messages
                FROM summaries
                GROUP BY channel_id, channel_name
                ORDER BY summary_count DESC
                LIMIT ?
            """,
                (limit,),
            )

            rows = cursor.fetchall()
            conn.close()

            ranking = [dict(row) for row in rows]
            logger.info(f"频道排行获取成功: {len(ranking)} 个频道")
            return ranking

        except Exception as e:
            logger.error(f"获取频道排行失败: {type(e).__name__}: {e}", exc_info=True)
            return []

    def export_summaries(
        self, output_format: str = "json", channel_id: str | None = None
    ) -> str | None:
        """
        导出历史记录

        Args:
            output_format: 输出格式 (json/csv/md)
            channel_id: 可选，频道URL，不指定则导出所有频道

        Returns:
            导出文件的路径，失败返回None
        """
        try:
            # 查询数据
            summaries = self.get_summaries(channel_id=channel_id, limit=10000)

            if not summaries:
                logger.warning("没有数据可导出")
                return None

            # 生成文件名
            timestamp = datetime.now(UTC).strftime("%Y%m%d_%H%M%S")
            channel_suffix = f"_{channel_id.split('/')[-1]}" if channel_id else ""
            filename = f"summaries_export{channel_suffix}_{timestamp}.{output_format}"

            if output_format == "json":
                self._export_json(summaries, filename)
            elif output_format == "csv":
                self._export_csv(summaries, filename)
            elif output_format == "md":
                self._export_md(summaries, filename)
            else:
                logger.error(f"不支持的导出格式: {output_format}")
                return None

            logger.info(f"成功导出 {len(summaries)} 条记录到 {filename}")
            return filename

        except Exception as e:
            logger.error(f"导出历史记录失败: {type(e).__name__}: {e}", exc_info=True)
            return None

    def _export_json(self, summaries: list[dict], filename: str):
        """导出为JSON格式"""
        with open(filename, "w", encoding="utf-8") as f:
            json.dump(summaries, f, ensure_ascii=False, indent=2)

    def _export_csv(self, summaries: list[dict], filename: str):
        """导出为CSV格式"""
        import csv

        if not summaries:
            return

        # 获取所有字段名
        fieldnames = list(summaries[0].keys())

        with open(filename, "w", encoding="utf-8", newline="") as f:
            writer = csv.DictWriter(f, fieldnames=fieldnames)
            writer.writeheader()

            for summary in summaries:
                # 将列表字段转换为字符串
                row = summary.copy()
                if isinstance(row.get("summary_message_ids"), list):
                    row["summary_message_ids"] = json.dumps(row["summary_message_ids"])
                writer.writerow(row)

    def _export_md(self, summaries: list[dict], filename: str):
        """导出为md格式"""
        with open(filename, "w", encoding="utf-8") as f:
            f.write("# 频道总结历史记录\n\n")
            f.write(f"导出时间: {datetime.now(UTC).strftime('%Y-%m-%d %H:%M:%S UTC')}\n\n")
            f.write(f"总记录数: {len(summaries)}\n\n")
            f.write("---\n\n")

            for summary in summaries:
                channel_name = summary.get("channel_name", summary.get("channel_id", "未知频道"))
                created_at = summary.get("created_at", "未知时间")
                summary_type = summary.get("summary_type", "unknown")
                message_count = summary.get("message_count", 0)
                summary_text = summary.get("summary_text", "")

                # 类型国际化映射
                type_cn = get_text(f"summary_type.{summary_type}")

                f.write(f"## {channel_name} - {created_at} ({type_cn})\n\n")
                f.write(f"**消息数量**: {message_count}\n\n")
                f.write(f"**总结内容**:\n\n{summary_text}\n\n")
                f.write("---\n\n")

    # ============ 配额管理方法 ============

    def get_quota_usage(self, user_id: int, date: str | None = None) -> dict[str, Any]:
        """
        获取用户配额使用情况

        Args:
            user_id: 用户ID
            date: 查询日期，格式YYYY-MM-DD，默认为今天

        Returns:
            配额使用信息字典
        """
        try:
            if date is None:
                date = datetime.now(UTC).strftime("%Y-%m-%d")

            conn = sqlite3.connect(self.db_path)
            cursor = conn.cursor()

            cursor.execute(
                """
                SELECT usage_count, last_reset
                FROM usage_quota
                WHERE user_id = ? AND query_date = ?
            """,
                (user_id, date),
            )

            row = cursor.fetchone()
            conn.close()

            if row:
                return {
                    "user_id": user_id,
                    "date": date,
                    "usage_count": row[0],
                    "last_reset": row[1],
                }
            else:
                return {"user_id": user_id, "date": date, "usage_count": 0, "last_reset": None}

        except Exception as e:
            logger.error(f"获取配额使用失败: {type(e).__name__}: {e}", exc_info=True)
            return {"user_id": user_id, "date": date, "usage_count": 0, "last_reset": None}

    def check_and_increment_quota(
        self, user_id: int, daily_limit: int, is_admin: bool = False
    ) -> dict[str, Any]:
        """
        检查并增加配额使用

        Args:
            user_id: 用户ID
            daily_limit: 每日限额
            is_admin: 是否为管理员（管理员无限制）

        Returns:
            {"allowed": bool, "remaining": int, "used": int}
        """
        try:
            if is_admin:
                # 管理员无限制
                return {"allowed": True, "remaining": -1, "used": 0, "is_admin": True}

            date = datetime.now(UTC).strftime("%Y-%m-%d")
            conn = sqlite3.connect(self.db_path)
            cursor = conn.cursor()

            # 获取当前使用次数
            cursor.execute(
                """
                SELECT usage_count FROM usage_quota
                WHERE user_id = ? AND query_date = ?
            """,
                (user_id, date),
            )

            row = cursor.fetchone()
            current_usage = row[0] if row else 0

            # 检查是否超限
            if current_usage >= daily_limit:
                conn.close()
                return {
                    "allowed": False,
                    "remaining": 0,
                    "used": current_usage,
                    "daily_limit": daily_limit,
                }

            # 增加使用次数
            new_usage = current_usage + 1
            if row:
                cursor.execute(
                    """
                    UPDATE usage_quota
                    SET usage_count = ?
                    WHERE user_id = ? AND query_date = ?
                """,
                    (new_usage, user_id, date),
                )
            else:
                cursor.execute(
                    """
                    INSERT INTO usage_quota (user_id, query_date, usage_count)
                    VALUES (?, ?, ?)
                """,
                    (user_id, date, new_usage),
                )

            conn.commit()
            conn.close()

            logger.info(f"用户 {user_id} 配额使用: {new_usage}/{daily_limit}")
            return {
                "allowed": True,
                "remaining": daily_limit - new_usage,
                "used": new_usage,
                "daily_limit": daily_limit,
            }

        except Exception as e:
            logger.error(f"配额检查失败: {type(e).__name__}: {e}", exc_info=True)
            return {"allowed": False, "error": str(e)}

    def get_total_daily_usage(self, date: str | None = None) -> int:
        """
        获取指定日期的总使用次数

        Args:
            date: 查询日期，格式YYYY-MM-DD，默认为今天

        Returns:
            总使用次数
        """
        try:
            if date is None:
                date = datetime.now(UTC).strftime("%Y-%m-%d")

            conn = sqlite3.connect(self.db_path)
            cursor = conn.cursor()

            cursor.execute(
                """
                SELECT SUM(usage_count) FROM usage_quota
                WHERE query_date = ?
            """,
                (date,),
            )

            result = cursor.fetchone()
            conn.close()

            return result[0] if result and result[0] else 0

        except Exception as e:
            logger.error(f"获取总使用次数失败: {type(e).__name__}: {e}", exc_info=True)
            return 0

    def reset_quota_if_new_day(self, user_id: int) -> None:
        """
        如果是新的一天，重置用户配额

        Args:
            user_id: 用户ID
        """
        try:
            today = datetime.now(UTC).strftime("%Y-%m-%d")
            conn = sqlite3.connect(self.db_path)
            cursor = conn.cursor()

            # 检查最后记录日期
            cursor.execute(
                """
                SELECT query_date FROM usage_quota
                WHERE user_id = ?
                ORDER BY query_date DESC
                LIMIT 1
            """,
                (user_id,),
            )

            row = cursor.fetchone()
            if row and row[0] != today:
                # 最后记录不是今天，自动重置
                logger.info(f"检测到新的一天，重置用户 {user_id} 配额")

            conn.close()

        except Exception as e:
            logger.error(f"重置配额失败: {type(e).__name__}: {e}", exc_info=True)

    # ============ 频道画像方法 ============

    def get_channel_profile(self, channel_id: str) -> dict[str, Any] | None:
        """
        获取频道画像

        Args:
            channel_id: 频道URL

        Returns:
            频道画像字典，不存在返回None
        """
        try:
            conn = sqlite3.connect(self.db_path)
            conn.row_factory = sqlite3.Row
            cursor = conn.cursor()

            cursor.execute("SELECT * FROM channel_profiles WHERE channel_id = ?", (channel_id,))
            row = cursor.fetchone()
            conn.close()

            if row:
                profile = dict(row)
                # 解析JSON字段
                try:
                    if profile.get("topics"):
                        profile["topics"] = json.loads(profile["topics"])
                    if profile.get("keywords_freq"):
                        profile["keywords_freq"] = json.loads(profile["keywords_freq"])
                except:
                    pass
                return profile
            return None

        except Exception as e:
            logger.error(f"获取频道画像失败: {type(e).__name__}: {e}", exc_info=True)
            return None

    def update_channel_profile(
        self,
        channel_id: str,
        channel_name: str,
        keywords: list[str] = None,
        topics: list[str] = None,
        sentiment: str = None,
        entities: list[str] = None,
    ) -> None:
        """
        更新频道画像

        Args:
            channel_id: 频道URL
            channel_name: 频道名称
            keywords: 关键词列表
            topics: 主题列表
            sentiment: 情感倾向
            entities: 实体列表
        """
        try:
            conn = sqlite3.connect(self.db_path)
            cursor = conn.cursor()

            # 获取现有画像
            cursor.execute("SELECT * FROM channel_profiles WHERE channel_id = ?", (channel_id,))
            existing = cursor.fetchone()

            # 统计该频道的总结数和平均消息长度
            cursor.execute(
                """
                SELECT COUNT(*) as count, AVG(message_count) as avg_len
                FROM summaries
                WHERE channel_id = ?
            """,
                (channel_id,),
            )
            stats = cursor.fetchone()

            total_summaries = stats[0] if stats else 0
            avg_message_length = stats[1] if stats and stats[1] else 0

            # 获取当前画像或创建新的
            if existing:
                # 更新关键词频率
                try:
                    keywords_freq = json.loads(existing[4]) if existing[4] else {}
                except:
                    keywords_freq = {}

                if keywords:
                    for kw in keywords:
                        keywords_freq[kw] = keywords_freq.get(kw, 0) + 1
            else:
                keywords_freq = {}
                if keywords:
                    for kw in keywords:
                        keywords_freq[kw] = 1

            # 转换为JSON存储
            topics_json = json.dumps(topics, ensure_ascii=False) if topics else None
            keywords_json = json.dumps(keywords_freq, ensure_ascii=False) if keywords_freq else None
            json.dumps(entities, ensure_ascii=False) if entities else None

            # 推断频道风格（简单规则）
            if topics:
                tech_keywords = ["AI", "编程", "技术", "开发", "Python", "GPT", "API"]
                if any(kw in " ".join(topics) for kw in tech_keywords):
                    style = "tech"
                else:
                    style = "casual"
            else:
                style = "neutral"

            now = datetime.now(UTC).isoformat()

            if existing:
                cursor.execute(
                    """
                    UPDATE channel_profiles
                    SET channel_name = ?, style = ?, topics = ?,
                        keywords_freq = ?, tone = ?, avg_message_length = ?,
                        total_summaries = ?, last_updated = ?
                    WHERE channel_id = ?
                """,
                    (
                        channel_name,
                        style,
                        topics_json,
                        keywords_json,
                        sentiment or "neutral",
                        avg_message_length,
                        total_summaries,
                        now,
                        channel_id,
                    ),
                )
            else:
                cursor.execute(
                    """
                    INSERT INTO channel_profiles (
                        channel_id, channel_name, style, topics, keywords_freq,
                        tone, avg_message_length, total_summaries, last_updated
                    ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
                """,
                    (
                        channel_id,
                        channel_name,
                        style,
                        topics_json,
                        keywords_json,
                        sentiment or "neutral",
                        avg_message_length,
                        total_summaries,
                        now,
                    ),
                )

            conn.commit()
            conn.close()

            logger.info(f"更新频道画像: {channel_name} ({channel_id})")

        except Exception as e:
            logger.error(f"更新频道画像失败: {type(e).__name__}: {e}", exc_info=True)

    def update_summary_metadata(
        self,
        summary_id: int,
        keywords: list[str] = None,
        topics: list[str] = None,
        sentiment: str = None,
        entities: list[str] = None,
    ) -> None:
        """
        更新总结的元数据

        Args:
            summary_id: 总结ID
            keywords: 关键词列表
            topics: 主题列表
            sentiment: 情感倾向
            entities: 实体列表
        """
        try:
            conn = sqlite3.connect(self.db_path)
            cursor = conn.cursor()

            keywords_json = json.dumps(keywords, ensure_ascii=False) if keywords else None
            topics_json = json.dumps(topics, ensure_ascii=False) if topics else None
            entities_json = json.dumps(entities, ensure_ascii=False) if entities else None

            cursor.execute(
                """
                UPDATE summaries
                SET keywords = ?, topics = ?, sentiment = ?, entities = ?
                WHERE id = ?
            """,
                (keywords_json, topics_json, sentiment, entities_json, summary_id),
            )

            conn.commit()
            conn.close()

            logger.info(f"更新总结元数据: ID={summary_id}")

        except Exception as e:
            logger.error(f"更新总结元数据失败: {type(e).__name__}: {e}", exc_info=True)

    # ============ 对话历史管理方法 ============

    def save_conversation(
        self,
        user_id: int,
        session_id: str,
        role: str,
        content: str,
        metadata: dict[str, Any] | None = None,
    ) -> bool:
        """
        保存对话记录

        Args:
            user_id: 用户ID
            session_id: 会话ID
            role: 角色 ('user' 或 'assistant')
            content: 内容
            metadata: 可选的元数据字典

        Returns:
            是否成功
        """
        try:
            conn = sqlite3.connect(self.db_path)
            cursor = conn.cursor()

            metadata_json = json.dumps(metadata, ensure_ascii=False) if metadata else None

            cursor.execute(
                """
                INSERT INTO conversation_history
                (user_id, session_id, role, content, metadata)
                VALUES (?, ?, ?, ?, ?)
            """,
                (user_id, session_id, role, content, metadata_json),
            )

            conn.commit()
            conn.close()

            logger.debug(f"保存对话记录: user_id={user_id}, session={session_id}, role={role}")
            return True

        except Exception as e:
            logger.error(f"保存对话记录失败: {type(e).__name__}: {e}", exc_info=True)
            return False

    def get_conversation_history(
        self, user_id: int, session_id: str, limit: int = 20
    ) -> list[dict[str, Any]]:
        """
        获取用户的对话历史

        Args:
            user_id: 用户ID
            session_id: 会话ID
            limit: 返回记录数量（默认20条，即10轮对话）

        Returns:
            对话历史列表，按时间升序排列
        """
        try:
            conn = sqlite3.connect(self.db_path)
            conn.row_factory = sqlite3.Row
            cursor = conn.cursor()

            cursor.execute(
                """
                SELECT role, content, timestamp, metadata
                FROM conversation_history
                WHERE user_id = ? AND session_id = ?
                ORDER BY timestamp ASC
                LIMIT ?
            """,
                (user_id, session_id, limit),
            )

            rows = cursor.fetchall()
            conn.close()

            history = []
            for row in rows:
                item = {
                    "role": row["role"],
                    "content": row["content"],
                    "timestamp": row["timestamp"],
                }
                # 解析元数据
                if row["metadata"]:
                    try:
                        item["metadata"] = json.loads(row["metadata"])
                    except:
                        pass
                history.append(item)

            logger.debug(
                f"获取对话历史: user_id={user_id}, session={session_id}, 条数={len(history)}"
            )
            return history

        except Exception as e:
            logger.error(f"获取对话历史失败: {type(e).__name__}: {e}", exc_info=True)
            return []

    def get_last_session_time(self, user_id: int) -> str | None:
        """
        获取用户最后一次对话时间

        Args:
            user_id: 用户ID

        Returns:
            最后一次对话时间（ISO格式），不存在返回None
        """
        try:
            conn = sqlite3.connect(self.db_path)
            cursor = conn.cursor()

            cursor.execute(
                """
                SELECT timestamp
                FROM conversation_history
                WHERE user_id = ?
                ORDER BY timestamp DESC
                LIMIT 1
            """,
                (user_id,),
            )

            result = cursor.fetchone()
            conn.close()

            return result[0] if result else None

        except Exception as e:
            logger.error(f"获取最后会话时间失败: {type(e).__name__}: {e}", exc_info=True)
            return None

    def clear_user_conversations(self, user_id: int, session_id: str | None = None) -> int:
        """
        清除用户的对话历史

        Args:
            user_id: 用户ID
            session_id: 可选，指定会话ID，不指定则清除所有会话

        Returns:
            删除的记录数
        """
        try:
            conn = sqlite3.connect(self.db_path)
            cursor = conn.cursor()

            if session_id:
                cursor.execute(
                    """
                    DELETE FROM conversation_history
                    WHERE user_id = ? AND session_id = ?
                """,
                    (user_id, session_id),
                )
            else:
                cursor.execute(
                    """
                    DELETE FROM conversation_history
                    WHERE user_id = ?
                """,
                    (user_id,),
                )

            deleted_count = cursor.rowcount
            conn.commit()
            conn.close()

            logger.info(
                f"清除对话历史: user_id={user_id}, session={session_id}, 删除{deleted_count}条"
            )
            return deleted_count

        except Exception as e:
            logger.error(f"清除对话历史失败: {type(e).__name__}: {e}", exc_info=True)
            return 0

    def get_session_count(self, user_id: int) -> int:
        """
        获取用户的会话总数

        Args:
            user_id: 用户ID

        Returns:
            会话数量
        """
        try:
            conn = sqlite3.connect(self.db_path)
            cursor = conn.cursor()

            cursor.execute(
                """
                SELECT COUNT(DISTINCT session_id)
                FROM conversation_history
                WHERE user_id = ?
            """,
                (user_id,),
            )

            result = cursor.fetchone()
            conn.close()

            return result[0] if result else 0

        except Exception as e:
            logger.error(f"获取会话数量失败: {type(e).__name__}: {e}", exc_info=True)
            return 0

    def delete_old_conversations(self, days: int = 7) -> int:
        """
        删除旧的对话记录（定期清理）

        Args:
            days: 保留天数，默认7天

        Returns:
            删除的记录数
        """
        try:
            conn = sqlite3.connect(self.db_path)
            cursor = conn.cursor()

            cutoff_date = datetime.now(UTC) - timedelta(days=days)

            cursor.execute(
                """
                DELETE FROM conversation_history
                WHERE timestamp < ?
            """,
                (cutoff_date.isoformat(),),
            )

            deleted_count = cursor.rowcount
            conn.commit()
            conn.close()

            logger.info(f"已删除 {deleted_count} 条旧对话记录 (超过 {days} 天)")
            return deleted_count

        except Exception as e:
            logger.error(f"删除旧对话记录失败: {type(e).__name__}: {e}", exc_info=True)
            return 0

    # ============ 用户管理方法 ============

    def register_user(
        self, user_id: int, username: str = None, first_name: str = None, is_admin: bool = False
    ) -> bool:
        """
        注册新用户

        Args:
            user_id: Telegram用户ID
            username: 用户名
            first_name: 名字
            is_admin: 是否为管理员

        Returns:
            是否成功
        """
        try:
            conn = sqlite3.connect(self.db_path)
            cursor = conn.cursor()

            now = datetime.now(UTC).isoformat()

            cursor.execute(
                """
                INSERT OR REPLACE INTO users
                (user_id, username, first_name, registered_at, last_active, is_admin)
                VALUES (?, ?, ?,
                        COALESCE((SELECT registered_at FROM users WHERE user_id = ?), ?),
                        ?, ?)
            """,
                (user_id, username, first_name, user_id, now, now, 1 if is_admin else 0),
            )

            conn.commit()
            conn.close()

            logger.info(f"用户注册成功: user_id={user_id}, username={username}")
            return True

        except Exception as e:
            logger.error(f"用户注册失败: {type(e).__name__}: {e}", exc_info=True)
            return False

    def get_user(self, user_id: int) -> dict[str, Any] | None:
        """
        获取用户信息

        Args:
            user_id: 用户ID

        Returns:
            用户信息字典，不存在返回None
        """
        try:
            conn = sqlite3.connect(self.db_path)
            conn.row_factory = sqlite3.Row
            cursor = conn.cursor()

            cursor.execute("SELECT * FROM users WHERE user_id = ?", (user_id,))
            row = cursor.fetchone()
            conn.close()

            if row:
                user = dict(row)
                # 解析preferences JSON
                if user.get("preferences"):
                    try:
                        user["preferences"] = json.loads(user["preferences"])
                    except:
                        pass
                return user
            return None

        except Exception as e:
            logger.error(f"获取用户信息失败: {type(e).__name__}: {e}", exc_info=True)
            return None

    def update_user_activity(self, user_id: int) -> bool:
        """
        更新用户最后活跃时间

        Args:
            user_id: 用户ID

        Returns:
            是否成功
        """
        try:
            conn = sqlite3.connect(self.db_path)
            cursor = conn.cursor()

            now = datetime.now(UTC).isoformat()

            cursor.execute(
                """
                UPDATE users SET last_active = ?
                WHERE user_id = ?
            """,
                (now, user_id),
            )

            conn.commit()
            conn.close()

            return True

        except Exception as e:
            logger.error(f"更新用户活跃时间失败: {type(e).__name__}: {e}", exc_info=True)
            return False

    def set_user_admin(self, user_id: int, is_admin: bool) -> bool:
        """
        设置用户管理员权限

        Args:
            user_id: 用户ID
            is_admin: 是否为管理员

        Returns:
            是否成功
        """
        try:
            conn = sqlite3.connect(self.db_path)
            cursor = conn.cursor()

            cursor.execute(
                """
                UPDATE users SET is_admin = ?
                WHERE user_id = ?
            """,
                (1 if is_admin else 0, user_id),
            )

            conn.commit()
            conn.close()

            logger.info(f"更新用户权限: user_id={user_id}, is_admin={is_admin}")
            return True

        except Exception as e:
            logger.error(f"设置用户权限失败: {type(e).__name__}: {e}", exc_info=True)
            return False

    def get_registered_users(self, active_days: int = 30, limit: int = 100) -> list[dict[str, Any]]:
        """
        获取注册用户列表

        Args:
            active_days: 活跃天数限制，默认30天
            limit: 返回记录数量

        Returns:
            用户列表
        """
        try:
            conn = sqlite3.connect(self.db_path)
            conn.row_factory = sqlite3.Row
            cursor = conn.cursor()

            cutoff = (datetime.now(UTC) - timedelta(days=active_days)).isoformat()

            cursor.execute(
                """
                SELECT * FROM users
                WHERE last_active >= ?
                ORDER BY last_active DESC
                LIMIT ?
            """,
                (cutoff, limit),
            )

            rows = cursor.fetchall()
            conn.close()

            return [dict(row) for row in rows]

        except Exception as e:
            logger.error(f"获取用户列表失败: {type(e).__name__}: {e}", exc_info=True)
            return []

    def is_user_registered(self, user_id: int) -> bool:
        """
        检查用户是否已注册

        Args:
            user_id: 用户ID

        Returns:
            是否已注册
        """
        try:
            conn = sqlite3.connect(self.db_path)
            cursor = conn.cursor()

            cursor.execute("SELECT 1 FROM users WHERE user_id = ?", (user_id,))
            result = cursor.fetchone()
            conn.close()

            return result is not None

        except Exception as e:
            logger.error(f"检查用户注册状态失败: {type(e).__name__}: {e}", exc_info=True)
            return False

    # ============ 订阅管理方法 ============

    def add_subscription(
        self, user_id: int, channel_id: str, channel_name: str = None, sub_type: str = "summary"
    ) -> bool:
        """
        添加订阅

        Args:
            user_id: 用户ID
            channel_id: 频道URL
            channel_name: 频道名称
            sub_type: 订阅类型 ('summary' 或 'poll')

        Returns:
            是否成功
        """
        try:
            conn = sqlite3.connect(self.db_path)
            cursor = conn.cursor()

            cursor.execute(
                """
                INSERT OR IGNORE INTO subscriptions
                (user_id, channel_id, channel_name, sub_type)
                VALUES (?, ?, ?, ?)
            """,
                (user_id, channel_id, channel_name, sub_type),
            )

            conn.commit()
            affected = cursor.rowcount
            conn.close()

            if affected > 0:
                logger.info(
                    f"添加订阅成功: user_id={user_id}, channel={channel_name}, type={sub_type}"
                )
                return True
            else:
                logger.info(f"订阅已存在: user_id={user_id}, channel={channel_name}")
                return False

        except Exception as e:
            logger.error(f"添加订阅失败: {type(e).__name__}: {e}", exc_info=True)
            return False

    def remove_subscription(
        self, user_id: int, channel_id: str = None, sub_type: str = None
    ) -> int:
        """
        移除订阅

        Args:
            user_id: 用户ID
            channel_id: 可选，频道ID，不指定则移除所有频道
            sub_type: 可选，订阅类型，不指定则移除所有类型

        Returns:
            删除的订阅数
        """
        try:
            conn = sqlite3.connect(self.db_path)
            cursor = conn.cursor()

            conditions = ["user_id = ?"]
            params = [user_id]

            if channel_id:
                conditions.append("channel_id = ?")
                params.append(channel_id)

            if sub_type:
                conditions.append("sub_type = ?")
                params.append(sub_type)

            query = f"DELETE FROM subscriptions WHERE {' AND '.join(conditions)}"
            cursor.execute(query, params)

            deleted_count = cursor.rowcount
            conn.commit()
            conn.close()

            logger.info(f"移除订阅: user_id={user_id}, 删除{deleted_count}条")
            return deleted_count

        except Exception as e:
            logger.error(f"移除订阅失败: {type(e).__name__}: {e}", exc_info=True)
            return 0

    def get_user_subscriptions(self, user_id: int, sub_type: str = None) -> list[dict[str, Any]]:
        """
        获取用户的订阅列表

        Args:
            user_id: 用户ID
            sub_type: 可选，订阅类型过滤

        Returns:
            订阅列表
        """
        try:
            conn = sqlite3.connect(self.db_path)
            conn.row_factory = sqlite3.Row
            cursor = conn.cursor()

            if sub_type:
                cursor.execute(
                    """
                    SELECT * FROM subscriptions
                    WHERE user_id = ? AND sub_type = ? AND enabled = 1
                    ORDER BY created_at DESC
                """,
                    (user_id, sub_type),
                )
            else:
                cursor.execute(
                    """
                    SELECT * FROM subscriptions
                    WHERE user_id = ? AND enabled = 1
                    ORDER BY created_at DESC
                """,
                    (user_id,),
                )

            rows = cursor.fetchall()
            conn.close()

            return [dict(row) for row in rows]

        except Exception as e:
            logger.error(f"获取用户订阅失败: {type(e).__name__}: {e}", exc_info=True)
            return []

    def get_channel_subscribers(self, channel_id: str, sub_type: str = "summary") -> list[int]:
        """
        获取频道的订阅用户ID列表

        Args:
            channel_id: 频道URL
            sub_type: 订阅类型

        Returns:
            用户ID列表
        """
        try:
            conn = sqlite3.connect(self.db_path)
            cursor = conn.cursor()

            cursor.execute(
                """
                SELECT user_id FROM subscriptions
                WHERE channel_id = ? AND sub_type = ? AND enabled = 1
            """,
                (channel_id, sub_type),
            )

            rows = cursor.fetchall()
            conn.close()

            return [row[0] for row in rows]

        except Exception as e:
            logger.error(f"获取频道订阅者失败: {type(e).__name__}: {e}", exc_info=True)
            return []

    def get_all_channels(self) -> list[dict[str, Any]]:
        """
        获取所有可用频道（从summaries表中提取）

        Returns:
            频道列表，包含channel_id和channel_name
        """
        try:
            conn = sqlite3.connect(self.db_path)
            conn.row_factory = sqlite3.Row
            cursor = conn.cursor()

            cursor.execute("""
                SELECT DISTINCT channel_id, channel_name,
                       MAX(created_at) as last_summary_time
                FROM summaries
                GROUP BY channel_id, channel_name
                ORDER BY last_summary_time DESC
            """)

            rows = cursor.fetchall()
            conn.close()

            return [dict(row) for row in rows]

        except Exception as e:
            logger.error(f"获取频道列表失败: {type(e).__name__}: {e}", exc_info=True)
            return []

    def is_subscribed(self, user_id: int, channel_id: str, sub_type: str = "summary") -> bool:
        """
        检查用户是否已订阅某频道

        Args:
            user_id: 用户ID
            channel_id: 频道URL
            sub_type: 订阅类型

        Returns:
            是否已订阅
        """
        try:
            conn = sqlite3.connect(self.db_path)
            cursor = conn.cursor()

            cursor.execute(
                """
                SELECT 1 FROM subscriptions
                WHERE user_id = ? AND channel_id = ?
                      AND sub_type = ? AND enabled = 1
            """,
                (user_id, channel_id, sub_type),
            )

            result = cursor.fetchone()
            conn.close()

            return result is not None

        except Exception as e:
            logger.error(f"检查订阅状态失败: {type(e).__name__}: {e}", exc_info=True)
            return False

    # ============ 请求队列方法 ============

    def create_request(
        self,
        request_type: str,
        requested_by: int,
        target_channel: str = None,
        params: dict[str, Any] = None,
    ) -> int | None:
        """
        创建请求

        Args:
            request_type: 请求类型 ('summary', 'poll', etc.)
            requested_by: 请求者用户ID
            target_channel: 目标频道URL
            params: 其他参数（JSON格式）

        Returns:
            请求ID，失败返回None
        """
        try:
            conn = sqlite3.connect(self.db_path)
            cursor = conn.cursor()

            params_json = json.dumps(params, ensure_ascii=False) if params else None

            cursor.execute(
                """
                INSERT INTO request_queue
                (request_type, requested_by, target_channel, params)
                VALUES (?, ?, ?, ?)
            """,
                (request_type, requested_by, target_channel, params_json),
            )

            conn.commit()
            request_id = cursor.lastrowid
            conn.close()

            logger.info(f"创建请求: id={request_id}, type={request_type}, user={requested_by}")
            return request_id

        except Exception as e:
            logger.error(f"创建请求失败: {type(e).__name__}: {e}", exc_info=True)
            return None

    def get_pending_requests(self, limit: int = 10) -> list[dict[str, Any]]:
        """
        获取待处理的请求列表

        Args:
            limit: 返回记录数量

        Returns:
            请求列表
        """
        try:
            conn = sqlite3.connect(self.db_path)
            conn.row_factory = sqlite3.Row
            cursor = conn.cursor()

            cursor.execute(
                """
                SELECT * FROM request_queue
                WHERE status = 'pending'
                ORDER BY created_at ASC
                LIMIT ?
            """,
                (limit,),
            )

            rows = cursor.fetchall()
            conn.close()

            requests = []
            for row in rows:
                req = dict(row)
                # 解析JSON字段
                if req.get("params"):
                    try:
                        req["params"] = json.loads(req["params"])
                    except:
                        pass
                if req.get("result"):
                    try:
                        req["result"] = json.loads(req["result"])
                    except:
                        pass
                requests.append(req)

            return requests

        except Exception as e:
            logger.error(f"获取待处理请求失败: {type(e).__name__}: {e}", exc_info=True)
            return []

    def update_request_status(self, request_id: int, status: str, result: Any = None) -> bool:
        """
        更新请求状态

        Args:
            request_id: 请求ID
            status: 新状态 ('processing', 'completed', 'failed')
            result: 结果数据（JSON可序列化）

        Returns:
            是否成功
        """
        try:
            conn = sqlite3.connect(self.db_path)
            cursor = conn.cursor()

            now = datetime.now(UTC).isoformat()
            result_json = json.dumps(result, ensure_ascii=False) if result else None

            cursor.execute(
                """
                UPDATE request_queue
                SET status = ?, result = ?, processed_at = ?
                WHERE id = ?
            """,
                (status, result_json, now, request_id),
            )

            conn.commit()
            conn.close()

            logger.info(f"更新请求状态: id={request_id}, status={status}")
            return True

        except Exception as e:
            logger.error(f"更新请求状态失败: {type(e).__name__}: {e}", exc_info=True)
            return False

    def get_request_status(self, request_id: int) -> dict[str, Any] | None:
        """
        获取请求状态

        Args:
            request_id: 请求ID

        Returns:
            请求信息字典，不存在返回None
        """
        try:
            conn = sqlite3.connect(self.db_path)
            conn.row_factory = sqlite3.Row
            cursor = conn.cursor()

            cursor.execute("SELECT * FROM request_queue WHERE id = ?", (request_id,))
            row = cursor.fetchone()
            conn.close()

            if row:
                req = dict(row)
                # 解析JSON字段
                if req.get("params"):
                    try:
                        req["params"] = json.loads(req["params"])
                    except:
                        pass
                if req.get("result"):
                    try:
                        req["result"] = json.loads(req["result"])
                    except:
                        pass
                return req
            return None

        except Exception as e:
            logger.error(f"获取请求状态失败: {type(e).__name__}: {e}", exc_info=True)
            return None

    def cleanup_old_requests(self, days: int = 7) -> int:
        """
        清理旧请求记录

        Args:
            days: 保留天数，默认7天

        Returns:
            删除的记录数
        """
        try:
            conn = sqlite3.connect(self.db_path)
            cursor = conn.cursor()

            cutoff_date = datetime.now(UTC) - timedelta(days=days)

            cursor.execute(
                """
                DELETE FROM request_queue
                WHERE created_at < ? AND status IN ('completed', 'failed')
            """,
                (cutoff_date.isoformat(),),
            )

            deleted_count = cursor.rowcount
            conn.commit()
            conn.close()

            logger.info(f"清理旧请求: 删除 {deleted_count} 条记录")
            return deleted_count

        except Exception as e:
            logger.error(f"清理旧请求失败: {type(e).__name__}: {e}", exc_info=True)
            return 0

    # ============ 通知队列方法 ============

    def create_notification(
        self, user_id: int, notification_type: str, content: dict[str, Any]
    ) -> int | None:
        """
        创建通知

        Args:
            user_id: 目标用户ID
            notification_type: 通知类型 ('summary_push', 'request_result', etc.)
            content: 通知内容（JSON格式）

        Returns:
            通知ID，失败返回None
        """
        try:
            conn = sqlite3.connect(self.db_path)
            cursor = conn.cursor()

            content_json = json.dumps(content, ensure_ascii=False) if content else None

            cursor.execute(
                """
                INSERT INTO notification_queue
                (user_id, notification_type, content)
                VALUES (?, ?, ?)
            """,
                (user_id, notification_type, content_json),
            )

            conn.commit()
            notification_id = cursor.lastrowid
            conn.close()

            logger.info(f"创建通知: id={notification_id}, type={notification_type}, user={user_id}")
            return notification_id

        except Exception as e:
            logger.error(f"创建通知失败: {type(e).__name__}: {e}", exc_info=True)
            return None

    def get_pending_notifications(self, limit: int = 50) -> list[dict[str, Any]]:
        """
        获取待发送的通知列表

        Args:
            limit: 返回记录数量

        Returns:
            通知列表
        """
        try:
            conn = sqlite3.connect(self.db_path)
            conn.row_factory = sqlite3.Row
            cursor = conn.cursor()

            cursor.execute(
                """
                SELECT * FROM notification_queue
                WHERE status = 'pending'
                ORDER BY created_at ASC
                LIMIT ?
            """,
                (limit,),
            )

            rows = cursor.fetchall()
            conn.close()

            notifications = []
            for row in rows:
                notif = dict(row)
                # 解析JSON字段
                if notif.get("content"):
                    try:
                        notif["content"] = json.loads(notif["content"])
                    except:
                        pass
                notifications.append(notif)

            return notifications

        except Exception as e:
            logger.error(f"获取待发送通知失败: {type(e).__name__}: {e}", exc_info=True)
            return []

    def update_notification_status(self, notification_id: int, status: str) -> bool:
        """
        更新通知状态

        Args:
            notification_id: 通知ID
            status: 新状态 ('sent', 'failed')

        Returns:
            是否成功
        """
        try:
            conn = sqlite3.connect(self.db_path)
            cursor = conn.cursor()

            now = datetime.now(UTC).isoformat()

            cursor.execute(
                """
                UPDATE notification_queue
                SET status = ?, sent_at = ?
                WHERE id = ?
            """,
                (status, now, notification_id),
            )

            conn.commit()
            conn.close()

            logger.info(f"更新通知状态: id={notification_id}, status={status}")
            return True

        except Exception as e:
            logger.error(f"更新通知状态失败: {type(e).__name__}: {e}", exc_info=True)
            return False

    def cleanup_old_notifications(self, days: int = 7) -> int:
        """
        清理旧通知记录

        Args:
            days: 保留天数，默认7天

        Returns:
            删除的记录数
        """
        try:
            conn = sqlite3.connect(self.db_path)
            cursor = conn.cursor()

            cutoff_date = datetime.now(UTC) - timedelta(days=days)

            cursor.execute(
                """
                DELETE FROM notification_queue
                WHERE created_at < ? AND status IN ('sent', 'failed')
            """,
                (cutoff_date.isoformat(),),
            )

            deleted_count = cursor.rowcount
            conn.commit()
            conn.close()

            logger.info(f"清理旧通知: 删除 {deleted_count} 条记录")
            return deleted_count

        except Exception as e:
            logger.error(f"清理旧通知失败: {type(e).__name__}: {e}", exc_info=True)
            return 0

    def get_user_info(self, user_id: int) -> dict[str, Any] | None:
        """
        获取用户信息（别名方法，兼容请求处理器）

        Args:
            user_id: 用户ID

        Returns:
            用户信息字典，不存在返回None
        """
        return self.get_user(user_id)

    def get_qa_bot_statistics(self) -> dict[str, Any]:
        """
        获取问答Bot的详细统计信息

        Returns:
            统计信息字典
        """
        try:
            conn = sqlite3.connect(self.db_path)
            cursor = conn.cursor()

            stats = {}

            # 用户统计
            cursor.execute("SELECT COUNT(*) FROM users")
            stats["total_users"] = cursor.fetchone()[0]

            # 活跃用户数（7天内有查询）
            week_ago = (datetime.now(UTC) - timedelta(days=7)).isoformat()
            cursor.execute(
                """
                SELECT COUNT(DISTINCT user_id) FROM usage_quota
                WHERE query_date >= ?
            """,
                (week_ago[:10],),
            )
            stats["active_users"] = cursor.fetchone()[0] or 0

            # 今日新增用户
            today = datetime.now(UTC).strftime("%Y-%m-%d")
            cursor.execute(
                """
                SELECT COUNT(*) FROM users
                WHERE DATE(registered_at) = ?
            """,
                (today,),
            )
            stats["new_users_today"] = cursor.fetchone()[0] or 0

            # 查询统计
            cursor.execute(
                """
                SELECT SUM(usage_count) FROM usage_quota
                WHERE query_date = ?
            """,
                (today,),
            )
            stats["queries_today"] = cursor.fetchone()[0] or 0

            # 本周查询次数
            week_start = (datetime.now(UTC) - timedelta(days=7)).strftime("%Y-%m-%d")
            cursor.execute(
                """
                SELECT SUM(usage_count) FROM usage_quota
                WHERE query_date >= ?
            """,
                (week_start,),
            )
            stats["queries_week"] = cursor.fetchone()[0] or 0

            # 总查询次数
            cursor.execute("SELECT SUM(usage_count) FROM usage_quota")
            stats["total_queries"] = cursor.fetchone()[0] or 0

            # 订阅统计
            cursor.execute("SELECT COUNT(*) FROM subscriptions WHERE enabled = 1")
            stats["total_subscriptions"] = cursor.fetchone()[0] or 0

            # 活跃订阅数（用户7天内活跃）
            cursor.execute(
                """
                SELECT COUNT(DISTINCT s.user_id) FROM subscriptions s
                INNER JOIN usage_quota q ON s.user_id = q.user_id
                WHERE s.enabled = 1 AND q.query_date >= ?
            """,
                (week_ago[:10],),
            )
            stats["active_subscriptions"] = cursor.fetchone()[0] or 0

            # 请求统计
            cursor.execute("SELECT COUNT(*) FROM request_queue WHERE status = 'pending'")
            stats["pending_requests"] = cursor.fetchone()[0] or 0

            # 今日完成的请求数
            cursor.execute(
                """
                SELECT COUNT(*) FROM request_queue
                WHERE status IN ('completed', 'failed')
                AND DATE(processed_at) = ?
            """,
                (today,),
            )
            stats["completed_requests_today"] = cursor.fetchone()[0] or 0

            # 总请求数
            cursor.execute("SELECT COUNT(*) FROM request_queue")
            stats["total_requests"] = cursor.fetchone()[0] or 0

            # 活跃用户排行（前10）
            cursor.execute("""
                SELECT u.user_id, u.username, u.first_name,
                       SUM(q.usage_count) as query_count
                FROM users u
                INNER JOIN usage_quota q ON u.user_id = q.user_id
                GROUP BY u.user_id, u.username, u.first_name
                ORDER BY query_count DESC
                LIMIT 10
            """)
            top_users = []
            for row in cursor.fetchall():
                top_users.append(
                    {
                        "user_id": row[0],
                        "username": row[1],
                        "first_name": row[2],
                        "query_count": row[3],
                    }
                )
            stats["top_users"] = top_users

            # 频道订阅分布
            cursor.execute("""
                SELECT channel_name, COUNT(*) as count
                FROM subscriptions
                WHERE enabled = 1 AND channel_name IS NOT NULL
                GROUP BY channel_name
                ORDER BY count DESC
            """)
            channel_subs = {}
            for row in cursor.fetchall():
                channel_subs[row[0]] = row[1]
            stats["channel_subscriptions"] = channel_subs

            conn.close()

            logger.info(f"问答Bot统计信息获取成功: {stats}")
            return stats

        except Exception as e:
            logger.error(f"获取问答Bot统计信息失败: {type(e).__name__}: {e}", exc_info=True)
            return {}


# 创建全局数据库管理器实例
db_manager = None


def reload_db_manager():
    """
    强制重新加载数据库管理器实例
    
    重新读取环境变量并创建新的数据库管理器实例
    用于迁移后切换数据库类型
    
    ⚠️ 注意：此函数为同步函数，但可能需要关闭异步数据库连接
    """
    global db_manager
    
    # 关闭旧连接（如果有 close 方法）
    if db_manager and hasattr(db_manager, 'close'):
        try:
            # 检查是否是异步数据库管理器
            import asyncio
            import inspect
            
            # 尝试创建事件循环来关闭异步连接
            try:
                loop = asyncio.get_event_loop()
                if loop.is_running():
                    # 事件循环正在运行，创建任务
                    if asyncio.iscoroutinefunction(db_manager.close):
                        asyncio.create_task(db_manager.close())
                        logger.info("✅ 已调度异步数据库连接关闭任务")
                    else:
                        db_manager.close()
                        logger.info("✅ 已关闭旧的数据库连接")
                else:
                    # 事件循环未运行，直接运行
                    if asyncio.iscoroutinefunction(db_manager.close):
                        loop.run_until_complete(db_manager.close())
                        logger.info("✅ 已关闭旧的异步数据库连接")
                    else:
                        db_manager.close()
                        logger.info("✅ 已关闭旧的数据库连接")
            except RuntimeError:
                # 没有事件循环，尝试创建新的
                if asyncio.iscoroutinefunction(db_manager.close):
                    asyncio.run(db_manager.close())
                    logger.info("✅ 已关闭旧的异步数据库连接")
                else:
                    db_manager.close()
                    logger.info("✅ 已关闭旧的数据库连接")
                    
        except Exception as e:
            logger.error(f"❌ 关闭旧数据库连接失败: {e}")
            # 即使关闭失败也继续，允许旧连接被垃圾回收
    
    # 重置为 None
    db_manager = None
    
    # 重新加载环境变量
    from .settings import get_settings
    import os
    
    # 强制重新读取 .env 文件
    env_path = os.path.join("data", ".env")
    if os.path.exists(env_path):
        from dotenv import load_dotenv
        load_dotenv(env_path, override=True)
        logger.info(f"✅ 已重新加载环境变量: {env_path}")
    
    # 创建新实例
    settings = get_settings()
    db_type = getattr(settings, 'DATABASE_TYPE', 'sqlite').lower()
    
    logger.info(f"🔄 正在切换数据库管理器到: {db_type.upper()}")
    
    if db_type == 'mysql':
        from .database_mysql import MySQLManager
        db_manager = MySQLManager()
        logger.info(f"✅ 数据库管理器已切换到: MySQL")
    else:
        from .database_sqlite import SQLiteManager
        db_manager = SQLiteManager()
        logger.info(f"✅ 数据库管理器已切换到: SQLite")
    
    return db_manager


def get_db_manager():
    """
    获取全局数据库管理器实例
    
    根据环境变量 DATABASE_TYPE 选择数据库管理器:
    - 'sqlite': 使用 SQLiteManager
    - 'mysql': 使用 MySQLManager
    """
    global db_manager
    if db_manager is None:
        from .settings import get_settings
        
        settings = get_settings()
        # 正确的属性访问方式：settings.database.database_type
        db_type = settings.database.database_type
        
        if db_type == 'mysql':
            from .database_mysql import MySQLManager
            logger.info("使用 MySQL 数据库管理器")
            db_manager = MySQLManager()
        else:
            from .database_sqlite import SQLiteManager
            logger.info("使用 SQLite 数据库管理器")
            db_manager = SQLiteManager()
    
    return db_manager
