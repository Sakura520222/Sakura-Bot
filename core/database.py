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

import sqlite3
import json
import logging
import os
from datetime import datetime, timezone, timedelta
from typing import Optional, List, Dict, Any

logger = logging.getLogger(__name__)


class DatabaseManager:
    """总结历史记录数据库管理器"""

    def __init__(self, db_path=None):
        """
        初始化数据库管理器

        Args:
            db_path: 数据库文件路径，默认为 data/summaries.db
        """
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

            # 为新表创建索引
            cursor.execute("""
                CREATE INDEX IF NOT EXISTS idx_quota_user_date
                ON usage_quota(user_id, query_date)
            """)
            
            cursor.execute("""
                CREATE INDEX IF NOT EXISTS idx_quota_date
                ON usage_quota(query_date)
            """)

            # 插入或更新版本号
            cursor.execute("""
                INSERT OR REPLACE INTO db_version (version, upgraded_at)
                VALUES (2, CURRENT_TIMESTAMP)
            """)

            conn.commit()
            conn.close()

            logger.info("数据库表结构初始化成功")

        except Exception as e:
            logger.error(f"初始化数据库失败: {type(e).__name__}: {e}", exc_info=True)
            raise

    def save_summary(self, channel_id: str, channel_name: str, summary_text: str,
                     message_count: int, start_time: Optional[datetime] = None,
                     end_time: Optional[datetime] = None,
                     summary_message_ids: Optional[List[int]] = None,
                     poll_message_id: Optional[int] = None,
                     button_message_id: Optional[int] = None,
                     ai_model: str = "unknown", summary_type: str = "weekly"):
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

            cursor.execute("""
                INSERT INTO summaries (
                    channel_id, channel_name, summary_text, message_count,
                    start_time, end_time, ai_model, summary_type,
                    summary_message_ids, poll_message_id, button_message_id
                ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            """, (
                channel_id, channel_name, summary_text, message_count,
                start_time_str, end_time_str, ai_model, summary_type,
                summary_ids_json, poll_message_id, button_message_id
            ))

            conn.commit()
            summary_id = cursor.lastrowid
            conn.close()

            logger.info(f"成功保存总结记录到数据库, ID: {summary_id}, 频道: {channel_name}")
            return summary_id

        except Exception as e:
            logger.error(f"保存总结记录失败: {type(e).__name__}: {e}", exc_info=True)
            return None

    def get_summaries(self, channel_id: Optional[str] = None, limit: int = 10,
                      start_date: Optional[datetime] = None,
                      end_date: Optional[datetime] = None) -> List[Dict[str, Any]]:
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
                if summary['summary_message_ids']:
                    try:
                        summary['summary_message_ids'] = json.loads(summary['summary_message_ids'])
                    except:
                        summary['summary_message_ids'] = []
                else:
                    summary['summary_message_ids'] = []

                summaries.append(summary)

            logger.info(f"查询到 {len(summaries)} 条总结记录")
            return summaries

        except Exception as e:
            logger.error(f"查询总结记录失败: {type(e).__name__}: {e}", exc_info=True)
            return []

    def get_summary_by_id(self, summary_id: int) -> Optional[Dict[str, Any]]:
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
                if summary['summary_message_ids']:
                    try:
                        summary['summary_message_ids'] = json.loads(summary['summary_message_ids'])
                    except:
                        summary['summary_message_ids'] = []
                else:
                    summary['summary_message_ids'] = []

                return summary
            return None

        except Exception as e:
            logger.error(f"查询总结记录失败 (ID={summary_id}): {type(e).__name__}: {e}", exc_info=True)
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

            cutoff_date = datetime.now(timezone.utc) - timedelta(days=days)

            cursor.execute("""
                DELETE FROM summaries
                WHERE created_at < ?
            """, (cutoff_date.isoformat(),))

            deleted_count = cursor.rowcount
            conn.commit()
            conn.close()

            logger.info(f"已删除 {deleted_count} 条旧总结记录 (超过 {days} 天)")
            return deleted_count

        except Exception as e:
            logger.error(f"删除旧总结记录失败: {type(e).__name__}: {e}", exc_info=True)
            return 0

    def get_statistics(self, channel_id: Optional[str] = None) -> Dict[str, Any]:
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
            cursor.execute(f"""
                SELECT COUNT(*) FROM summaries
                {channel_condition}
            """, params)
            total_count = cursor.fetchone()[0]

            # 按类型统计
            cursor.execute(f"""
                SELECT summary_type, COUNT(*) as count
                FROM summaries
                {channel_condition}
                GROUP BY summary_type
            """, params)
            type_stats = dict(cursor.fetchall())

            # 总消息数
            cursor.execute(f"""
                SELECT SUM(message_count) FROM summaries
                {channel_condition}
            """, params)
            total_messages = cursor.fetchone()[0] or 0

            # 平均消息数
            avg_messages = total_messages / total_count if total_count > 0 else 0

            # 最近总结时间
            cursor.execute(f"""
                SELECT MAX(created_at) FROM summaries
                {channel_condition}
            """, params)
            last_summary_time = cursor.fetchone()[0]

            # 本周统计
            week_ago = (datetime.now(timezone.utc) - timedelta(days=7)).isoformat()
            if channel_id:
                cursor.execute("""
                    SELECT COUNT(*) FROM summaries
                    WHERE channel_id = ? AND created_at >= ?
                """, [channel_id, week_ago])
            else:
                cursor.execute("""
                    SELECT COUNT(*) FROM summaries
                    WHERE created_at >= ?
                """, [week_ago])
            week_count = cursor.fetchone()[0]

            # 本月统计
            month_ago = (datetime.now(timezone.utc) - timedelta(days=30)).isoformat()
            if channel_id:
                cursor.execute("""
                    SELECT COUNT(*) FROM summaries
                    WHERE channel_id = ? AND created_at >= ?
                """, [channel_id, month_ago])
            else:
                cursor.execute("""
                    SELECT COUNT(*) FROM summaries
                    WHERE created_at >= ?
                """, [month_ago])
            month_count = cursor.fetchone()[0]

            conn.close()

            stats = {
                "total_count": total_count,
                "type_stats": type_stats,
                "total_messages": total_messages,
                "avg_messages": round(avg_messages, 1),
                "last_summary_time": last_summary_time,
                "week_count": week_count,
                "month_count": month_count
            }

            logger.info(f"统计数据获取成功: {stats}")
            return stats

        except Exception as e:
            logger.error(f"获取统计信息失败: {type(e).__name__}: {e}", exc_info=True)
            return {}

    def get_channel_ranking(self, limit: int = 10) -> List[Dict[str, Any]]:
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

            cursor.execute("""
                SELECT
                    channel_id,
                    channel_name,
                    COUNT(*) as summary_count,
                    SUM(message_count) as total_messages
                FROM summaries
                GROUP BY channel_id, channel_name
                ORDER BY summary_count DESC
                LIMIT ?
            """, (limit,))

            rows = cursor.fetchall()
            conn.close()

            ranking = [dict(row) for row in rows]
            logger.info(f"频道排行获取成功: {len(ranking)} 个频道")
            return ranking

        except Exception as e:
            logger.error(f"获取频道排行失败: {type(e).__name__}: {e}", exc_info=True)
            return []

    def export_summaries(self, output_format: str = "json",
                         channel_id: Optional[str] = None) -> Optional[str]:
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
            timestamp = datetime.now(timezone.utc).strftime("%Y%m%d_%H%M%S")
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

    def _export_json(self, summaries: List[Dict], filename: str):
        """导出为JSON格式"""
        with open(filename, 'w', encoding='utf-8') as f:
            json.dump(summaries, f, ensure_ascii=False, indent=2)

    def _export_csv(self, summaries: List[Dict], filename: str):
        """导出为CSV格式"""
        import csv

        if not summaries:
            return

        # 获取所有字段名
        fieldnames = list(summaries[0].keys())

        with open(filename, 'w', encoding='utf-8', newline='') as f:
            writer = csv.DictWriter(f, fieldnames=fieldnames)
            writer.writeheader()

            for summary in summaries:
                # 将列表字段转换为字符串
                row = summary.copy()
                if isinstance(row.get('summary_message_ids'), list):
                    row['summary_message_ids'] = json.dumps(row['summary_message_ids'])
                writer.writerow(row)

    def _export_md(self, summaries: List[Dict], filename: str):
        """导出为md格式"""
        with open(filename, 'w', encoding='utf-8') as f:
            f.write("# 频道总结历史记录\n\n")
            f.write(f"导出时间: {datetime.now(timezone.utc).strftime('%Y-%m-%d %H:%M:%S UTC')}\n\n")
            f.write(f"总记录数: {len(summaries)}\n\n")
            f.write("---\n\n")

            for summary in summaries:
                channel_name = summary.get('channel_name', summary.get('channel_id', '未知频道'))
                created_at = summary.get('created_at', '未知时间')
                summary_type = summary.get('summary_type', 'unknown')
                message_count = summary.get('message_count', 0)
                summary_text = summary.get('summary_text', '')

                # 类型中文映射
                type_map = {'daily': '日报', 'weekly': '周报', 'manual': '手动总结'}
                type_cn = type_map.get(summary_type, summary_type)

                f.write(f"## {channel_name} - {created_at} ({type_cn})\n\n")
                f.write(f"**消息数量**: {message_count}\n\n")
                f.write(f"**总结内容**:\n\n{summary_text}\n\n")
                f.write("---\n\n")

    # ============ 配额管理方法 ============

    def get_quota_usage(self, user_id: int, date: Optional[str] = None) -> Dict[str, Any]:
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
                date = datetime.now(timezone.utc).strftime('%Y-%m-%d')

            conn = sqlite3.connect(self.db_path)
            cursor = conn.cursor()

            cursor.execute("""
                SELECT usage_count, last_reset
                FROM usage_quota
                WHERE user_id = ? AND query_date = ?
            """, (user_id, date))

            row = cursor.fetchone()
            conn.close()

            if row:
                return {
                    "user_id": user_id,
                    "date": date,
                    "usage_count": row[0],
                    "last_reset": row[1]
                }
            else:
                return {
                    "user_id": user_id,
                    "date": date,
                    "usage_count": 0,
                    "last_reset": None
                }

        except Exception as e:
            logger.error(f"获取配额使用失败: {type(e).__name__}: {e}", exc_info=True)
            return {"user_id": user_id, "date": date, "usage_count": 0, "last_reset": None}

    def check_and_increment_quota(self, user_id: int, daily_limit: int,
                                 is_admin: bool = False) -> Dict[str, Any]:
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

            date = datetime.now(timezone.utc).strftime('%Y-%m-%d')
            conn = sqlite3.connect(self.db_path)
            cursor = conn.cursor()

            # 获取当前使用次数
            cursor.execute("""
                SELECT usage_count FROM usage_quota
                WHERE user_id = ? AND query_date = ?
            """, (user_id, date))

            row = cursor.fetchone()
            current_usage = row[0] if row else 0

            # 检查是否超限
            if current_usage >= daily_limit:
                conn.close()
                return {
                    "allowed": False,
                    "remaining": 0,
                    "used": current_usage,
                    "daily_limit": daily_limit
                }

            # 增加使用次数
            new_usage = current_usage + 1
            if row:
                cursor.execute("""
                    UPDATE usage_quota
                    SET usage_count = ?
                    WHERE user_id = ? AND query_date = ?
                """, (new_usage, user_id, date))
            else:
                cursor.execute("""
                    INSERT INTO usage_quota (user_id, query_date, usage_count)
                    VALUES (?, ?, ?)
                """, (user_id, date, new_usage))

            conn.commit()
            conn.close()

            logger.info(f"用户 {user_id} 配额使用: {new_usage}/{daily_limit}")
            return {
                "allowed": True,
                "remaining": daily_limit - new_usage,
                "used": new_usage,
                "daily_limit": daily_limit
            }

        except Exception as e:
            logger.error(f"配额检查失败: {type(e).__name__}: {e}", exc_info=True)
            return {"allowed": False, "error": str(e)}

    def get_total_daily_usage(self, date: Optional[str] = None) -> int:
        """
        获取指定日期的总使用次数

        Args:
            date: 查询日期，格式YYYY-MM-DD，默认为今天

        Returns:
            总使用次数
        """
        try:
            if date is None:
                date = datetime.now(timezone.utc).strftime('%Y-%m-%d')

            conn = sqlite3.connect(self.db_path)
            cursor = conn.cursor()

            cursor.execute("""
                SELECT SUM(usage_count) FROM usage_quota
                WHERE query_date = ?
            """, (date,))

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
            today = datetime.now(timezone.utc).strftime('%Y-%m-%d')
            conn = sqlite3.connect(self.db_path)
            cursor = conn.cursor()

            # 检查最后记录日期
            cursor.execute("""
                SELECT query_date FROM usage_quota
                WHERE user_id = ?
                ORDER BY query_date DESC
                LIMIT 1
            """, (user_id,))

            row = cursor.fetchone()
            if row and row[0] != today:
                # 最后记录不是今天，自动重置
                logger.info(f"检测到新的一天，重置用户 {user_id} 配额")

            conn.close()

        except Exception as e:
            logger.error(f"重置配额失败: {type(e).__name__}: {e}", exc_info=True)

    # ============ 频道画像方法 ============

    def get_channel_profile(self, channel_id: str) -> Optional[Dict[str, Any]]:
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
                    if profile.get('topics'):
                        profile['topics'] = json.loads(profile['topics'])
                    if profile.get('keywords_freq'):
                        profile['keywords_freq'] = json.loads(profile['keywords_freq'])
                except:
                    pass
                return profile
            return None

        except Exception as e:
            logger.error(f"获取频道画像失败: {type(e).__name__}: {e}", exc_info=True)
            return None

    def update_channel_profile(self, channel_id: str, channel_name: str,
                              keywords: List[str] = None,
                              topics: List[str] = None,
                              sentiment: str = None,
                              entities: List[str] = None) -> None:
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
            cursor.execute("""
                SELECT COUNT(*) as count, AVG(message_count) as avg_len
                FROM summaries
                WHERE channel_id = ?
            """, (channel_id,))
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
            entities_json = json.dumps(entities, ensure_ascii=False) if entities else None

            # 推断频道风格（简单规则）
            if topics:
                tech_keywords = ['AI', '编程', '技术', '开发', 'Python', 'GPT', 'API']
                if any(kw in ' '.join(topics) for kw in tech_keywords):
                    style = 'tech'
                else:
                    style = 'casual'
            else:
                style = 'neutral'

            now = datetime.now(timezone.utc).isoformat()

            if existing:
                cursor.execute("""
                    UPDATE channel_profiles
                    SET channel_name = ?, style = ?, topics = ?,
                        keywords_freq = ?, tone = ?, avg_message_length = ?,
                        total_summaries = ?, last_updated = ?
                    WHERE channel_id = ?
                """, (channel_name, style, topics_json, keywords_json,
                       sentiment or 'neutral', avg_message_length,
                       total_summaries, now, channel_id))
            else:
                cursor.execute("""
                    INSERT INTO channel_profiles (
                        channel_id, channel_name, style, topics, keywords_freq,
                        tone, avg_message_length, total_summaries, last_updated
                    ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
                """, (channel_id, channel_name, style, topics_json, keywords_json,
                       sentiment or 'neutral', avg_message_length, total_summaries, now))

            conn.commit()
            conn.close()

            logger.info(f"更新频道画像: {channel_name} ({channel_id})")

        except Exception as e:
            logger.error(f"更新频道画像失败: {type(e).__name__}: {e}", exc_info=True)

    def update_summary_metadata(self, summary_id: int, keywords: List[str] = None,
                               topics: List[str] = None, sentiment: str = None,
                               entities: List[str] = None) -> None:
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

            cursor.execute("""
                UPDATE summaries
                SET keywords = ?, topics = ?, sentiment = ?, entities = ?
                WHERE id = ?
            """, (keywords_json, topics_json, sentiment, entities_json, summary_id))

            conn.commit()
            conn.close()

            logger.info(f"更新总结元数据: ID={summary_id}")

        except Exception as e:
            logger.error(f"更新总结元数据失败: {type(e).__name__}: {e}", exc_info=True)

    # ============ 对话历史管理方法 ============

    def save_conversation(self, user_id: int, session_id: str, 
                         role: str, content: str, 
                         metadata: Optional[Dict[str, Any]] = None) -> bool:
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

            cursor.execute("""
                INSERT INTO conversation_history 
                (user_id, session_id, role, content, metadata)
                VALUES (?, ?, ?, ?, ?)
            """, (user_id, session_id, role, content, metadata_json))

            conn.commit()
            conn.close()

            logger.debug(f"保存对话记录: user_id={user_id}, session={session_id}, role={role}")
            return True

        except Exception as e:
            logger.error(f"保存对话记录失败: {type(e).__name__}: {e}", exc_info=True)
            return False

    def get_conversation_history(self, user_id: int, session_id: str, 
                                limit: int = 20) -> List[Dict[str, Any]]:
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

            cursor.execute("""
                SELECT role, content, timestamp, metadata
                FROM conversation_history
                WHERE user_id = ? AND session_id = ?
                ORDER BY timestamp ASC
                LIMIT ?
            """, (user_id, session_id, limit))

            rows = cursor.fetchall()
            conn.close()

            history = []
            for row in rows:
                item = {
                    'role': row['role'],
                    'content': row['content'],
                    'timestamp': row['timestamp']
                }
                # 解析元数据
                if row['metadata']:
                    try:
                        item['metadata'] = json.loads(row['metadata'])
                    except:
                        pass
                history.append(item)

            logger.debug(f"获取对话历史: user_id={user_id}, session={session_id}, 条数={len(history)}")
            return history

        except Exception as e:
            logger.error(f"获取对话历史失败: {type(e).__name__}: {e}", exc_info=True)
            return []

    def get_last_session_time(self, user_id: int) -> Optional[str]:
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

            cursor.execute("""
                SELECT timestamp
                FROM conversation_history
                WHERE user_id = ?
                ORDER BY timestamp DESC
                LIMIT 1
            """, (user_id,))

            result = cursor.fetchone()
            conn.close()

            return result[0] if result else None

        except Exception as e:
            logger.error(f"获取最后会话时间失败: {type(e).__name__}: {e}", exc_info=True)
            return None

    def clear_user_conversations(self, user_id: int, 
                                session_id: Optional[str] = None) -> int:
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
                cursor.execute("""
                    DELETE FROM conversation_history
                    WHERE user_id = ? AND session_id = ?
                """, (user_id, session_id))
            else:
                cursor.execute("""
                    DELETE FROM conversation_history
                    WHERE user_id = ?
                """, (user_id,))

            deleted_count = cursor.rowcount
            conn.commit()
            conn.close()

            logger.info(f"清除对话历史: user_id={user_id}, session={session_id}, 删除{deleted_count}条")
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

            cursor.execute("""
                SELECT COUNT(DISTINCT session_id)
                FROM conversation_history
                WHERE user_id = ?
            """, (user_id,))

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

            cutoff_date = datetime.now(timezone.utc) - timedelta(days=days)

            cursor.execute("""
                DELETE FROM conversation_history
                WHERE timestamp < ?
            """, (cutoff_date.isoformat(),))

            deleted_count = cursor.rowcount
            conn.commit()
            conn.close()

            logger.info(f"已删除 {deleted_count} 条旧对话记录 (超过 {days} 天)")
            return deleted_count

        except Exception as e:
            logger.error(f"删除旧对话记录失败: {type(e).__name__}: {e}", exc_info=True)
            return 0


# 创建全局数据库管理器实例
db_manager = None

def get_db_manager():
    """获取全局数据库管理器实例"""
    global db_manager
    if db_manager is None:
        # 使用 data/summaries.db 作为默认路径
        db_manager = DatabaseManager(os.path.join("data", "summaries.db"))
    return db_manager
