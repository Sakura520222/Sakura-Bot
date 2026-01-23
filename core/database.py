# Copyright 2026 Sakura-频道总结助手
#
# 本项目采用 CC BY-NC-SA 4.0 许可证
# 您可以自由地共享、修改本作品，但必须遵守以下条件：
# - 署名：必须提供本项目的原始来源链接
# - 非商业：禁止任何商业用途和分发
# - 相同方式共享：衍生作品必须采用相同的许可证
#
# 本项目源代码：https://github.com/Sakura520222/Sakura-Channel-Summary-Assistant
# 许可证全文：https://creativecommons.org/licenses/by-nc-sa/4.0/deed.zh

import sqlite3
import json
import logging
import os
from datetime import datetime, timedelta
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

            # 创建总结记录主表
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
                    button_message_id INTEGER
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

            # 插入或更新版本号
            cursor.execute("""
                INSERT OR REPLACE INTO db_version (version, upgraded_at)
                VALUES (1, CURRENT_TIMESTAMP)
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

            cutoff_date = datetime.now() - timedelta(days=days)

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
            week_ago = (datetime.now() - timedelta(days=7)).isoformat()
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
            month_ago = (datetime.now() - timedelta(days=30)).isoformat()
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
            timestamp = datetime.now().strftime("%Y%m%d_%H%M%S")
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
            f.write(f"导出时间: {datetime.now().strftime('%Y-%m-%d %H:%M:%S')}\n\n")
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


# 创建全局数据库管理器实例
db_manager = None

def get_db_manager():
    """获取全局数据库管理器实例"""
    global db_manager
    if db_manager is None:
        # 使用 data/summaries.db 作为默认路径
        db_manager = DatabaseManager(os.path.join("data", "summaries.db"))
    return db_manager
