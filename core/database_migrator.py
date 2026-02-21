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
数据库迁移工具模块

提供从SQLite到MySQL的数据库迁移功能，采用流式读取+分批插入策略
"""

import asyncio
import json
import logging
import os
import shutil
from datetime import UTC, datetime
from typing import Any

from .database_base import DatabaseManagerBase
from .database_sqlite import SQLiteManager
from .database_mysql import MySQLManager
from .i18n import get_text

logger = logging.getLogger(__name__)


class DatabaseMigrator:
    """数据库迁移器（SQLite -> MySQL）"""

    def __init__(self, sqlite_path: str = "data/summaries.db", mysql_config: dict = None):
        """
        初始化数据库迁移器

        Args:
            sqlite_path: SQLite数据库文件路径
            mysql_config: MySQL配置字典
        """
        self.sqlite_path = sqlite_path
        self.mysql_config = mysql_config or {}
        self.sqlite_db = None
        self.mysql_db = None
        self.migration_status = {"status": "idle", "progress": 0, "message": "", "table_stats": {}}

    async def check_migration_ready(self) -> dict[str, Any]:
        """
        检查是否准备好进行迁移

        Returns:
            包含检查结果的字典
        """
        result = {
            "ready": False,
            "sqlite_exists": False,
            "mysql_configured": False,
            "mysql_connectable": False,
            "sqlite_tables": {},
            "message": "",
        }

        try:
            # 检查SQLite数据库是否存在
            if os.path.exists(self.sqlite_path):
                result["sqlite_exists"] = True

                # 连接SQLite获取表信息
                self.sqlite_db = SQLiteManager(self.sqlite_path)
                # init_database() 在构造函数中已自动调用

                # 获取各表的记录数
                tables = [
                    "summaries",
                    "usage_quota",
                    "channel_profiles",
                    "conversation_history",
                    "users",
                    "subscriptions",
                    "request_queue",
                    "notification_queue",
                ]

                for table in tables:
                    try:
                        # SQLite 使用同步连接
                        conn = self.sqlite_db.get_connection()
                        cursor = conn.execute(f"SELECT COUNT(*) FROM {table}")
                        count = cursor.fetchone()[0]
                        conn.close()
                        result["sqlite_tables"][table] = count
                    except Exception:
                        result["sqlite_tables"][table] = 0

            # 检查MySQL配置
            if all(
                k in self.mysql_config
                for k in ["host", "port", "user", "password", "database"]
            ):
                result["mysql_configured"] = True

                # 尝试连接MySQL
                try:
                    self.mysql_db = MySQLManager(**self.mysql_config)
                    await self.mysql_db.init_database()
                    result["mysql_connectable"] = True

                    # 检查MySQL中是否已有数据
                    stats = await self.mysql_db.get_statistics()
                    if stats.get("total_count", 0) > 0:
                        result["message"] = get_text("database.migrate.mysql_has_data")
                        result["ready"] = False
                    else:
                        result["ready"] = True
                        result["message"] = get_text("database.migrate.check_passed")

                except Exception as e:
                    result["message"] = f"{get_text('database.migrate.mysql_connect_error')}: {str(e)}"
                    logger.error(f"MySQL连接检查失败: {e}", exc_info=True)

            else:
                result["message"] = get_text("database.migrate.mysql_incomplete")

            return result

        except Exception as e:
            logger.error(f"迁移准备检查失败: {e}", exc_info=True)
            result["message"] = f"{get_text('database.migrate.check_general_error')}: {str(e)}"
            return result

    async def migrate_data(self, chunk_size: int = 500) -> dict[str, Any]:
        """
        执行数据迁移（采用流式读取+分批插入策略）

        Args:
            chunk_size: 每批处理的数据量

        Returns:
            迁移结果字典
        """
        if not self.sqlite_db or not self.mysql_db:
            return {
                "success": False,
                "message": get_text("database.migrate.not_initialized"),
            }

        # 备份SQLite数据库
        backup_path = await self._backup_sqlite()
        if not backup_path:
            return {"success": False, "message": "SQLite数据库备份失败"}

        self.migration_status = {
            "status": "in_progress",
            "progress": 0,
            "message": get_text("database.migrate.start_migrating"),
            "start_time": datetime.now(UTC).isoformat(),
            "table_stats": {},
        }

        try:
            # 按表迁移（考虑外键依赖关系）
            migration_order = [
                ("users", self._migrate_users),
                ("subscriptions", self._migrate_subscriptions),
                ("usage_quota", self._migrate_usage_quota),
                ("summaries", self._migrate_summaries),
                ("channel_profiles", self._migrate_channel_profiles),
                ("conversation_history", self._migrate_conversation_history),
                ("request_queue", self._migrate_request_queue),
                ("notification_queue", self._migrate_notification_queue),
            ]

            total_tables = len(migration_order)

            for idx, (table_name, migrate_func) in enumerate(migration_order):
                try:
                    logger.info(f"开始迁移表: {table_name}")
                    self.migration_status["message"] = f"{get_text('database.migrate.migrating_table')}: {table_name}"

                    stats = await migrate_func(chunk_size)
                    self.migration_status["table_stats"][table_name] = stats

                    progress = int((idx + 1) / total_tables * 100)
                    self.migration_status["progress"] = progress

                    logger.info(f"表 {table_name} 迁移完成: {stats}")

                except Exception as e:
                    logger.error(f"迁移表 {table_name} 失败: {e}", exc_info=True)
                    raise

            self.migration_status["status"] = "completed"
            self.migration_status["message"] = get_text("database.migrate.migration_complete")
            self.migration_status["end_time"] = datetime.now(UTC).isoformat()

            # 验证数据完整性
            verification = await self._verify_migration()
            
            # ⚠️ 不在迁移过程中删除 SQLite 文件
            # 删除操作应在重启成功后、验证 MySQL 可用后再执行
            logger.info("✅ 迁移完成，但保留 SQLite 文件。请在重启并验证 MySQL 后手动删除。")

            return {
                "success": True,
                "message": get_text("database.migrate.migration_complete"),
                "stats": self.migration_status["table_stats"],
                "backup_path": backup_path,
                "verification": verification,
                "sqlite_deleted": False,  # 迁移过程中不删除
                "sqlite_path": self.sqlite_path,  # 返回 SQLite 路径供后续使用
            }

        except Exception as e:
            logger.error(f"迁移失败: {e}", exc_info=True)

            self.migration_status["status"] = "failed"
            self.migration_status["message"] = f"{get_text('database.migrate.migration_failed')}: {str(e)}"
            self.migration_status["end_time"] = datetime.now(UTC).isoformat()

            return {
                "success": False,
                "message": f"{get_text('database.migrate.migration_failed')}: {str(e)}",
                "backup_path": backup_path,
            }

    async def _backup_sqlite(self) -> str | None:
        """备份SQLite数据库"""
        try:
            timestamp = datetime.now(UTC).strftime("%Y%m%d_%H%M%S")
            backup_path = f"{self.sqlite_path}.backup_{timestamp}"

            shutil.copy2(self.sqlite_path, backup_path)
            logger.info(f"SQLite数据库已备份到: {backup_path}")
            return backup_path

        except Exception as e:
            logger.error(f"备份SQLite数据库失败: {e}", exc_info=True)
            return None

    def _get_sqlite_data(self, query: str) -> list:
        """
        从SQLite同步读取数据（辅助方法）

        Args:
            query: SQL查询语句

        Returns:
            查询结果列表
        """
        import sqlite3

        try:
            conn = sqlite3.connect(self.sqlite_path)
            conn.row_factory = sqlite3.Row
            cursor = conn.execute(query)
            rows = cursor.fetchall()
            conn.close()
            return rows
        except Exception as e:
            logger.error(f"SQLite查询失败: {e}", exc_info=True)
            return []

    async def _migrate_users(self, chunk_size: int) -> dict[str, int]:
        """迁移用户表"""
        migrated = 0
        failed = 0

        try:
            # 使用同步方式读取 SQLite
            rows = self._get_sqlite_data("SELECT * FROM users")
            
            for row in rows:
                try:
                    # SQLite row: (user_id, username, first_name, registered_at, last_active, is_admin, preferences)
                    user_id = row[0]
                    username = row[1]
                    first_name = row[2]
                    is_admin = bool(row[5]) if len(row) > 5 else False

                    await self.mysql_db.register_user(
                        user_id=user_id,
                        username=username,
                        first_name=first_name,
                        is_admin=is_admin,
                    )
                    migrated += 1

                except Exception as e:
                    logger.warning(f"迁移用户 {row[0]} 失败: {e}")
                    failed += 1

        except Exception as e:
            logger.error(f"迁移用户表失败: {e}", exc_info=True)

        return {"migrated": migrated, "failed": failed}

    async def _migrate_subscriptions(self, chunk_size: int) -> dict[str, int]:
        """迁移订阅表"""
        migrated = 0
        failed = 0

        try:
            rows = self._get_sqlite_data("SELECT * FROM subscriptions")
            
            for row in rows:
                try:
                    user_id = row[1]
                    channel_id = row[2]
                    channel_name = row[3] if len(row) > 3 else None
                    sub_type = row[4] if len(row) > 4 else "summary"

                    await self.mysql_db.add_subscription(
                        user_id=user_id,
                        channel_id=channel_id,
                        channel_name=channel_name,
                        sub_type=sub_type,
                    )
                    migrated += 1

                except Exception as e:
                    logger.warning(f"迁移订阅 {row[0]} 失败: {e}")
                    failed += 1

        except Exception as e:
            logger.error(f"迁移订阅表失败: {e}", exc_info=True)

        return {"migrated": migrated, "failed": failed}

    async def _migrate_usage_quota(self, chunk_size: int) -> dict[str, int]:
        """迁移配额表"""
        migrated = 0
        failed = 0

        try:
            rows = self._get_sqlite_data("SELECT * FROM usage_quota")
            
            for row in rows:
                try:
                    user_id = row[1]
                    query_date = row[2]
                    usage_count = row[3]

                    # 直接插入MySQL
                    async with self.mysql_db.pool.acquire() as conn:
                        async with conn.cursor() as mysql_cursor:
                            await mysql_cursor.execute(
                                """
                                INSERT IGNORE INTO usage_quota
                                (user_id, query_date, usage_count)
                                VALUES (%s, %s, %s)
                            """,
                                (user_id, query_date, usage_count),
                            )
                            await conn.commit()
                    migrated += 1

                except Exception as e:
                    logger.warning(f"迁移配额记录 {row[0]} 失败: {e}")
                    failed += 1

        except Exception as e:
            logger.error(f"迁移配额表失败: {e}", exc_info=True)

        return {"migrated": migrated, "failed": failed}

    async def _migrate_summaries(self, chunk_size: int) -> dict[str, int]:
        """迁移总结表（分批处理）"""
        migrated = 0
        failed = 0

        try:
            rows = self._get_sqlite_data("SELECT * FROM summaries")
            
            for row in rows:
                try:
                    # 解析数据
                    channel_id = row[1]
                    channel_name = row[2]
                    summary_text = row[3]
                    message_count = row[4]
                    
                    # 🔧 修复：将 SQLite 字符串时间转换为 datetime 对象
                    start_time_str = row[5] if len(row) > 5 else None
                    end_time_str = row[6] if len(row) > 6 else None
                    
                    # 转换时间字符串为 datetime 对象
                    from datetime import datetime
                    start_time = None
                    end_time = None
                    
                    if start_time_str:
                        try:
                            # 尝试解析 ISO 格式时间字符串
                            if isinstance(start_time_str, str):
                                start_time = datetime.fromisoformat(start_time_str.replace('Z', '+00:00'))
                            else:
                                start_time = start_time_str
                        except Exception as e:
                            logger.warning(f"解析 start_time 失败: {start_time_str}, {e}")
                    
                    if end_time_str:
                        try:
                            if isinstance(end_time_str, str):
                                end_time = datetime.fromisoformat(end_time_str.replace('Z', '+00:00'))
                            else:
                                end_time = end_time_str
                        except Exception as e:
                            logger.warning(f"解析 end_time 失败: {end_time_str}, {e}")
                    
                    ai_model = row[8] if len(row) > 8 else "unknown"
                    summary_type = row[9] if len(row) > 9 else "weekly"

                    # 解析JSON字段
                    summary_message_ids = None
                    if len(row) > 10 and row[10]:
                        try:
                            summary_message_ids = json.loads(row[10])
                        except:
                            pass

                    poll_message_id = row[11] if len(row) > 11 else None
                    button_message_id = row[12] if len(row) > 12 else None

                    # 插入MySQL
                    new_id = await self.mysql_db.save_summary(
                        channel_id=channel_id,
                        channel_name=channel_name,
                        summary_text=summary_text,
                        message_count=message_count,
                        start_time=start_time,
                        end_time=end_time,
                        summary_message_ids=summary_message_ids,
                        poll_message_id=poll_message_id,
                        button_message_id=button_message_id,
                        ai_model=ai_model,
                        summary_type=summary_type,
                    )

                    if new_id:
                        migrated += 1
                    else:
                        failed += 1

                except Exception as e:
                    logger.warning(f"迁移总结 {row[0]} 失败: {e}")
                    failed += 1

        except Exception as e:
            logger.error(f"迁移总结表失败: {e}", exc_info=True)

        return {"migrated": migrated, "failed": failed}

    async def _migrate_channel_profiles(self, chunk_size: int) -> dict[str, int]:
        """迁移频道画像表"""
        migrated = 0
        failed = 0

        try:
            rows = self._get_sqlite_data("SELECT * FROM channel_profiles")
            
            for row in rows:
                try:
                    channel_id = row[0]
                    channel_name = row[1] if len(row) > 1 else None

                    await self.mysql_db.update_channel_profile(
                        channel_id=channel_id,
                        channel_name=channel_name,
                    )
                    migrated += 1

                except Exception as e:
                    logger.warning(f"迁移频道画像 {row[0]} 失败: {e}")
                    failed += 1

        except Exception as e:
            logger.error(f"迁移频道画像表失败: {e}", exc_info=True)

        return {"migrated": migrated, "failed": failed}

    async def _migrate_conversation_history(self, chunk_size: int) -> dict[str, int]:
        """迁移对话历史表（分批处理）"""
        migrated = 0
        failed = 0

        try:
            rows = self._get_sqlite_data("SELECT * FROM conversation_history")
            
            for row in rows:
                try:
                    user_id = row[1]
                    session_id = row[2]
                    role = row[3]
                    content = row[4]
                    metadata = None

                    if len(row) > 6 and row[6]:
                        try:
                            metadata = json.loads(row[6])
                        except:
                            pass

                    await self.mysql_db.save_conversation(
                        user_id=user_id,
                        session_id=session_id,
                        role=role,
                        content=content,
                        metadata=metadata,
                    )
                    migrated += 1

                except Exception as e:
                    logger.warning(f"迁移对话记录 {row[0]} 失败: {e}")
                    failed += 1

        except Exception as e:
            logger.error(f"迁移对话历史表失败: {e}", exc_info=True)

        return {"migrated": migrated, "failed": failed}

    async def _migrate_request_queue(self, chunk_size: int) -> dict[str, int]:
        """迁移请求队列表"""
        migrated = 0
        failed = 0

        try:
            rows = self._get_sqlite_data("SELECT * FROM request_queue")
            
            for row in rows:
                try:
                    request_type = row[1]
                    requested_by = row[2]
                    target_channel = row[3] if len(row) > 3 else None
                    params = None

                    if len(row) > 4 and row[4]:
                        try:
                            params = json.loads(row[4])
                        except:
                            pass

                    await self.mysql_db.create_request(
                        request_type=request_type,
                        requested_by=requested_by,
                        target_channel=target_channel,
                        params=params,
                    )
                    migrated += 1

                except Exception as e:
                    logger.warning(f"迁移请求 {row[0]} 失败: {e}")
                    failed += 1

        except Exception as e:
            logger.error(f"迁移请求队列表失败: {e}", exc_info=True)

        return {"migrated": migrated, "failed": failed}

    async def _migrate_notification_queue(self, chunk_size: int) -> dict[str, int]:
        """迁移通知队列表"""
        migrated = 0
        failed = 0

        try:
            rows = self._get_sqlite_data("SELECT * FROM notification_queue")
            
            for row in rows:
                try:
                    user_id = row[1]
                    notification_type = row[2]
                    content = None

                    if len(row) > 3 and row[3]:
                        try:
                            content = json.loads(row[3])
                        except:
                            pass

                    await self.mysql_db.create_notification(
                        user_id=user_id,
                        notification_type=notification_type,
                        content=content,
                    )
                    migrated += 1

                except Exception as e:
                    logger.warning(f"迁移通知 {row[0]} 失败: {e}")
                    failed += 1

        except Exception as e:
            logger.error(f"迁移通知队列表失败: {e}", exc_info=True)

        return {"migrated": migrated, "failed": failed}

    async def _verify_migration(self) -> dict[str, Any]:
        """验证迁移结果"""
        try:
            sqlite_stats = {}
            mysql_stats = {}

            tables = [
                "users",
                "subscriptions",
                "usage_quota",
                "summaries",
                "channel_profiles",
                "conversation_history",
                "request_queue",
                "notification_queue",
            ]

            # 获取SQLite统计（同步方式）
            for table in tables:
                try:
                    rows = self._get_sqlite_data(f"SELECT COUNT(*) FROM {table}")
                    sqlite_stats[table] = rows[0][0] if rows else 0
                except:
                    sqlite_stats[table] = 0

            # 获取MySQL统计
            async with self.mysql_db.pool.acquire() as conn:
                async with conn.cursor() as cursor:
                    for table in tables:
                        try:
                            await cursor.execute(f"SELECT COUNT(*) FROM {table}")
                            count = (await cursor.fetchone())[0]
                            mysql_stats[table] = count
                        except:
                            mysql_stats[table] = 0

            # 比较结果
            verification = {"sqlite_stats": sqlite_stats, "mysql_stats": mysql_stats, "matched": True}

            for table in tables:
                if sqlite_stats[table] != mysql_stats[table]:
                    verification["matched"] = False
                    logger.warning(
                        f"表 {table} 数据不匹配: SQLite={sqlite_stats[table]}, MySQL={mysql_stats[table]}"
                    )

            return verification

        except Exception as e:
            logger.error(f"验证迁移失败: {e}", exc_info=True)
            return {"error": str(e)}

    def get_migration_status(self) -> dict[str, Any]:
        """获取迁移状态"""
        return self.migration_status.copy()

    async def cleanup(self):
        """清理资源"""
        if self.sqlite_db:
            await self.sqlite_db.close()
        if self.mysql_db:
            await self.mysql_db.close()