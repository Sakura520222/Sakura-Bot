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
MySQL数据库管理器模块

使用aiomysql实现异步MySQL数据库操作，支持连接池和事务处理
"""

import json
import logging
import os
import warnings
from datetime import UTC, datetime, timedelta
from typing import Any

import aiomysql

from core.i18n.i18n import get_text

from .base import DatabaseManagerBase

logger = logging.getLogger(__name__)


def utc_now_naive() -> datetime:
    """返回 naive UTC datetime，兼容 MySQL DATETIME 列

    MySQL 的 DATETIME 列不支持带时区信息的 ISO 格式字符串，
    统一使用此函数获取 UTC 时间，避免 aware datetime 写入问题。
    """
    return datetime.now(UTC).replace(tzinfo=None)


class MySQLManager(DatabaseManagerBase):
    """MySQL数据库管理器（异步）"""

    def __init__(
        self,
        host=None,
        port=None,
        user=None,
        password=None,
        database=None,
        charset="utf8mb4",
        pool_size=5,
        max_overflow=10,
        pool_timeout=30,
    ):
        """
        初始化MySQL数据库管理器

        Args:
            host: MySQL主机地址
            port: MySQL端口
            user: MySQL用户名
            password: MySQL密码
            database: 数据库名
            charset: 字符集（默认utf8mb4，支持Emoji）
            pool_size: 连接池大小
            max_overflow: 连接池最大溢出
            pool_timeout: 连接池超时时间
        """
        # 从环境变量或参数获取配置
        self.host = host or os.getenv("MYSQL_HOST", "localhost")
        self.port = int(port or os.getenv("MYSQL_PORT", "3306"))
        self.user = user or os.getenv("MYSQL_USER", "sakura_bot")
        self.password = password or os.getenv("MYSQL_PASSWORD", "")
        self.database = database or os.getenv("MYSQL_DATABASE", "sakura_bot_db")
        self.charset = charset or os.getenv("MYSQL_CHARSET", "utf8mb4")
        self.pool_size = int(pool_size or os.getenv("MYSQL_POOL_SIZE", "5"))
        self.max_overflow = int(max_overflow or os.getenv("MYSQL_MAX_OVERFLOW", "10"))
        self.pool_timeout = int(pool_timeout or os.getenv("MYSQL_POOL_TIMEOUT", "30"))

        self.pool = None
        self._db_type = "mysql"
        self._db_version = 6

        logger.info(
            f"MySQL管理器初始化: {self.user}@{self.host}:{self.port}/{self.database} "
            f"(charset={self.charset}, pool_size={self.pool_size})"
        )

    async def init_database(self):
        """初始化数据库连接和表结构"""
        try:
            # 创建连接池
            self.pool = await aiomysql.create_pool(
                host=self.host,
                port=self.port,
                user=self.user,
                password=self.password,
                db=self.database,
                charset=self.charset,
                autocommit=True,  # 改为 True，确保跨进程数据立即可见
                maxsize=self.pool_size,
                minsize=1,
                connect_timeout=self.pool_timeout,
                echo=False,
                # 设置时区为UTC，确保时间一致性
                init_command="SET time_zone='+00:00';",
            )
            logger.info("MySQL连接池创建成功（时区: UTC, autocommit=True）")

            # 初始化表结构
            async with self.pool.acquire() as conn:
                async with conn.cursor() as cursor:
                    await self._create_tables(cursor)
                    await conn.commit()
                    logger.info("MySQL数据库表结构初始化成功")

        except (aiomysql.OperationalError, OSError) as e:
            # MySQL 连接失败的特殊错误处理
            error_msg = str(e)
            logger.error(
                f"❌ MySQL 连接失败: {type(e).__name__}: {error_msg}",
                exc_info=True,
            )
            logger.error(f"   MySQL 主机: {self.host}:{self.port}")
            logger.error(f"   数据库: {self.database}")
            logger.error(f"   用户: {self.user}")

            # 重新抛出异常，供上层处理
            raise ConnectionError(
                f"MySQL 连接失败: {error_msg}\n请检查 MySQL 服务是否运行，确认配置是否正确"
            ) from e
        except Exception as e:
            logger.error(f"初始化MySQL数据库失败: {type(e).__name__}: {e}", exc_info=True)
            raise

    async def _create_tables(self, cursor):
        """创建所有数据库表"""
        # 保存当前的 sql_mode 设置，稍后恢复
        await cursor.execute("SELECT @@SESSION.sql_mode")
        original_sql_mode = (await cursor.fetchone())[0]

        try:
            # 临时禁用 MySQL 警告，避免表已存在时的警告信息
            await cursor.execute("SET SESSION sql_mode = ''")

            # 过滤 Python warnings 模块捕获的 aiomysql 警告
            with warnings.catch_warnings():
                warnings.filterwarnings("ignore", category=Warning)

                # 1. 创建总结记录主表（MySQL 5.7+支持JSON类型）
                await cursor.execute("""
                CREATE TABLE IF NOT EXISTS summaries (
                    id INT AUTO_INCREMENT PRIMARY KEY,
                    channel_id VARCHAR(255) NOT NULL,
                    channel_name VARCHAR(255),
                    summary_text TEXT NOT NULL,
                    message_count INT DEFAULT 0,
                    start_time DATETIME,
                    end_time DATETIME,
                    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
                    ai_model VARCHAR(100),
                    summary_type VARCHAR(20) DEFAULT 'weekly',
                    summary_message_ids JSON,
                    poll_message_id INT,
                    button_message_id INT,
                    keywords JSON,
                    topics JSON,
                    sentiment VARCHAR(50),
                    entities JSON,
                    INDEX idx_channel_created (channel_id, created_at DESC),
                    INDEX idx_created (created_at DESC),
                    INDEX idx_channel (channel_id)
                ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
            """)

                # 2. 创建数据库版本管理表
                await cursor.execute("""
                CREATE TABLE IF NOT EXISTS db_version (
                    version INT PRIMARY KEY,
                    upgraded_at DATETIME DEFAULT CURRENT_TIMESTAMP
                ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
            """)

                # 3. 创建配额管理表
                await cursor.execute("""
                CREATE TABLE IF NOT EXISTS usage_quota (
                    id INT AUTO_INCREMENT PRIMARY KEY,
                    user_id BIGINT NOT NULL,
                    query_date VARCHAR(10) NOT NULL,
                    usage_count INT DEFAULT 0,
                    last_reset DATETIME DEFAULT CURRENT_TIMESTAMP,
                    UNIQUE KEY unique_user_date (user_id, query_date),
                    INDEX idx_quota_user_date (user_id, query_date),
                    INDEX idx_quota_date (query_date)
                ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
            """)

                # 4. 创建频道画像表
                await cursor.execute("""
                CREATE TABLE IF NOT EXISTS channel_profiles (
                    channel_id VARCHAR(255) PRIMARY KEY,
                    channel_name VARCHAR(255),
                    style VARCHAR(50) DEFAULT 'neutral',
                    topics JSON,
                    keywords_freq JSON,
                    tone VARCHAR(50),
                    avg_message_length DECIMAL(10,2) DEFAULT 0,
                    total_summaries INT DEFAULT 0,
                    last_updated DATETIME DEFAULT CURRENT_TIMESTAMP
                ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
            """)

                # 5. 创建对话历史表
                await cursor.execute("""
                CREATE TABLE IF NOT EXISTS conversation_history (
                    id INT AUTO_INCREMENT PRIMARY KEY,
                    user_id BIGINT NOT NULL,
                    session_id VARCHAR(100) NOT NULL,
                    role VARCHAR(20) NOT NULL,
                    content TEXT NOT NULL,
                    timestamp DATETIME DEFAULT CURRENT_TIMESTAMP,
                    metadata JSON,
                    INDEX idx_user_session (user_id, session_id, timestamp ASC)
                ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
            """)

                # 6. 创建用户注册表
                await cursor.execute("""
                CREATE TABLE IF NOT EXISTS users (
                    user_id BIGINT PRIMARY KEY,
                    username VARCHAR(255),
                    first_name VARCHAR(255),
                    registered_at DATETIME DEFAULT CURRENT_TIMESTAMP,
                    last_active DATETIME DEFAULT CURRENT_TIMESTAMP,
                    is_admin BOOLEAN DEFAULT FALSE,
                    preferences JSON,
                    INDEX idx_users_last_active (last_active DESC)
                ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
            """)

                # 7. 创建订阅配置表
                await cursor.execute("""
                CREATE TABLE IF NOT EXISTS subscriptions (
                    id INT AUTO_INCREMENT PRIMARY KEY,
                    user_id BIGINT NOT NULL,
                    channel_id VARCHAR(255) NOT NULL,
                    channel_name VARCHAR(255),
                    sub_type VARCHAR(20) DEFAULT 'summary',
                    enabled BOOLEAN DEFAULT TRUE,
                    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
                    UNIQUE KEY unique_subscription (user_id, channel_id, sub_type),
                    INDEX idx_subscriptions_user (user_id),
                    INDEX idx_subscriptions_channel (channel_id)
                ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
            """)

                # 8. 创建请求队列表
                await cursor.execute("""
                CREATE TABLE IF NOT EXISTS request_queue (
                    id INT AUTO_INCREMENT PRIMARY KEY,
                    request_type VARCHAR(50) NOT NULL,
                    requested_by BIGINT NOT NULL,
                    target_channel VARCHAR(255),
                    params JSON,
                    status VARCHAR(20) DEFAULT 'pending',
                    result JSON,
                    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
                    processed_at DATETIME,
                    INDEX idx_request_queue_status (status, created_at),
                    INDEX idx_request_queue_user (requested_by)
                ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
            """)

                # 9. 创建通知队列表
                await cursor.execute("""
                CREATE TABLE IF NOT EXISTS notification_queue (
                    id INT AUTO_INCREMENT PRIMARY KEY,
                    user_id BIGINT NOT NULL,
                    notification_type VARCHAR(50) NOT NULL,
                    content JSON,
                    status VARCHAR(20) DEFAULT 'pending',
                    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
                    sent_at DATETIME,
                    INDEX idx_notification_queue_status (status, created_at)
                ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
            """)

                # 10. 创建转发消息记录表
                await cursor.execute("""
                CREATE TABLE IF NOT EXISTS forwarded_messages (
                    id INT AUTO_INCREMENT PRIMARY KEY,
                    message_id VARCHAR(100) NOT NULL,
                    source_channel VARCHAR(255) NOT NULL,
                    target_channel VARCHAR(255) NOT NULL,
                    content_hash VARCHAR(64),
                    timestamp BIGINT NOT NULL,
                    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
                    UNIQUE KEY unique_message_target (message_id, target_channel),
                    INDEX idx_forwarded_source (source_channel),
                    INDEX idx_forwarded_target (target_channel),
                    INDEX idx_forwarded_timestamp (timestamp)
                ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
            """)

                # 11. 创建转发统计表
                await cursor.execute("""
                CREATE TABLE IF NOT EXISTS forwarding_stats (
                    id INT AUTO_INCREMENT PRIMARY KEY,
                    channel_id VARCHAR(255) NOT NULL UNIQUE,
                    total_forwarded INT DEFAULT 0,
                    last_forwarded BIGINT,
                    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
                    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
                    INDEX idx_forwarding_stats_channel (channel_id)
                ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
            """)

                # 12. 创建投票重新生成表
                await cursor.execute("""
                CREATE TABLE IF NOT EXISTS poll_regenerations (
                    id BIGINT AUTO_INCREMENT PRIMARY KEY,
                    channel VARCHAR(512) NOT NULL,
                    summary_msg_id BIGINT NOT NULL,
                    poll_msg_id BIGINT,
                    button_msg_id BIGINT,
                    summary_text TEXT,
                    channel_name VARCHAR(255),
                    timestamp DATETIME DEFAULT CURRENT_TIMESTAMP,
                    send_to_channel BOOLEAN DEFAULT FALSE,
                    vote_count INT DEFAULT 0,
                    discussion_forward_msg_id BIGINT,
                    UNIQUE KEY unique_poll (channel, summary_msg_id),
                    INDEX idx_poll_lookup (channel, summary_msg_id),
                    INDEX idx_poll_timestamp (timestamp)
                ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
            """)

                # 13. 创建投票者表
                await cursor.execute("""
                CREATE TABLE IF NOT EXISTS poll_voters (
                    id BIGINT AUTO_INCREMENT PRIMARY KEY,
                    channel VARCHAR(512) NOT NULL,
                    summary_msg_id BIGINT NOT NULL,
                    user_id BIGINT NOT NULL,
                    voted_at DATETIME DEFAULT CURRENT_TIMESTAMP,
                    UNIQUE KEY unique_voter (channel, summary_msg_id, user_id),
                    INDEX idx_voter_lookup (channel, summary_msg_id),
                    INDEX idx_voter_user (user_id),
                    FOREIGN KEY (channel, summary_msg_id)
                        REFERENCES poll_regenerations(channel, summary_msg_id)
                            ON DELETE CASCADE
                ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
            """)

                # 14. 创建投稿表
                await cursor.execute("""
                CREATE TABLE IF NOT EXISTS submissions (
                    id INT AUTO_INCREMENT PRIMARY KEY,
                    submitter_id BIGINT NOT NULL,
                    submitter_name VARCHAR(255),
                    title VARCHAR(500) NOT NULL,
                    content TEXT,
                    media_files JSON,
                    target_channel VARCHAR(500),
                    is_anonymous BOOLEAN DEFAULT FALSE,
                    signature_forced BOOLEAN DEFAULT FALSE,
                    status VARCHAR(20) DEFAULT 'pending',
                    ai_optimized_content TEXT,
                    ai_optimized_title VARCHAR(500),
                    review_message_id INT,
                    reviewed_by BIGINT,
                    reviewed_at DATETIME,
                    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
                    INDEX idx_submissions_status (status, created_at),
                    INDEX idx_submissions_submitter (submitter_id)
                ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
            """)

                # 如果表已存在，尝试添加新列（幂等操作）
                try:
                    await cursor.execute(
                        "ALTER TABLE submissions ADD COLUMN target_channel VARCHAR(500) "
                        "AFTER media_files"
                    )
                    logger.info("投稿表新增 target_channel 列成功")
                except Exception as alter_err:
                    if "Duplicate column name" in str(alter_err):
                        logger.debug("target_channel 列已存在，跳过")
                    else:
                        logger.warning(f"添加 target_channel 列时出错: {alter_err}")

                try:
                    await cursor.execute(
                        "ALTER TABLE submissions ADD COLUMN ai_optimized_title VARCHAR(500) "
                        "AFTER ai_optimized_content"
                    )
                    logger.info("投稿表新增 ai_optimized_title 列成功")
                except Exception as alter_err:
                    if "Duplicate column name" in str(alter_err):
                        logger.debug("ai_optimized_title 列已存在，跳过")
                    else:
                        logger.warning(f"添加 ai_optimized_title 列时出错: {alter_err}")

                try:
                    await cursor.execute(
                        "ALTER TABLE submissions ADD COLUMN is_anonymous BOOLEAN DEFAULT FALSE "
                        "AFTER target_channel"
                    )
                    logger.info("投稿表新增 is_anonymous 列成功")
                except Exception as alter_err:
                    if "Duplicate column name" in str(alter_err):
                        logger.debug("is_anonymous 列已存在，跳过")
                    else:
                        logger.warning(f"添加 is_anonymous 列时出错: {alter_err}")

                try:
                    await cursor.execute(
                        "ALTER TABLE submissions ADD COLUMN signature_forced BOOLEAN DEFAULT FALSE "
                        "AFTER is_anonymous"
                    )
                    logger.info("投稿表新增 signature_forced 列成功")
                except Exception as alter_err:
                    if "Duplicate column name" in str(alter_err):
                        logger.debug("signature_forced 列已存在，跳过")
                    else:
                        logger.warning(f"添加 signature_forced 列时出错: {alter_err}")

                # 15. 创建 WebUI 系统运维审计表
                await cursor.execute("""
                CREATE TABLE IF NOT EXISTS system_audit_logs (
                    id BIGINT AUTO_INCREMENT PRIMARY KEY,
                    action VARCHAR(100) NOT NULL,
                    actor VARCHAR(100) NOT NULL,
                    target VARCHAR(500),
                    params_summary TEXT,
                    success BOOLEAN DEFAULT TRUE,
                    message TEXT,
                    duration_ms INT DEFAULT 0,
                    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
                    INDEX idx_system_audit_created (created_at DESC),
                    INDEX idx_system_audit_action (action),
                    INDEX idx_system_audit_actor (actor)
                ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
            """)

                # 插入或更新版本号
                await cursor.execute("""
                    INSERT INTO db_version (version, upgraded_at)
                    VALUES (6, NOW())
                    ON DUPLICATE KEY UPDATE version = 6, upgraded_at = NOW()
                """)

        finally:
            # 恢复原始的 sql_mode 设置，确保即使建表失败也能恢复
            # 使用参数化查询避免 SQL 注入风险
            if original_sql_mode:
                await cursor.execute("SET SESSION sql_mode = %s", (original_sql_mode,))
            else:
                # 如果原始值为空，恢复到空字符串
                await cursor.execute("SET SESSION sql_mode = %s", ("",))

    async def save_summary(
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
        try:
            async with self.pool.acquire() as conn:
                async with conn.cursor() as cursor:
                    # 将列表转换为JSON字符串存储（MySQL支持JSON类型）
                    summary_ids_json = (
                        json.dumps(summary_message_ids) if summary_message_ids else None
                    )

                    await cursor.execute(
                        """
                        INSERT INTO summaries (
                            channel_id, channel_name, summary_text, message_count,
                            start_time, end_time, ai_model, summary_type,
                            summary_message_ids, poll_message_id, button_message_id
                        ) VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s)
                    """,
                        (
                            channel_id,
                            channel_name,
                            summary_text,
                            message_count,
                            start_time,
                            end_time,
                            ai_model,
                            summary_type,
                            summary_ids_json,
                            poll_message_id,
                            button_message_id,
                        ),
                    )

                    await conn.commit()
                    summary_id = cursor.lastrowid
                    logger.info(f"成功保存总结记录到MySQL, ID: {summary_id}, 频道: {channel_name}")
                    return summary_id

        except Exception as e:
            logger.error(f"保存总结记录失败: {type(e).__name__}: {e}", exc_info=True)
            return None

    async def get_summaries(
        self,
        channel_id: str | None = None,
        limit: int = 10,
        offset: int = 0,
        start_date: datetime | None = None,
        end_date: datetime | None = None,
    ) -> list[dict[str, Any]]:
        """查询历史总结"""
        try:
            async with self.pool.acquire() as conn:
                async with conn.cursor(aiomysql.DictCursor) as cursor:
                    # 构建查询条件
                    conditions = []
                    params = []

                    if channel_id:
                        conditions.append("channel_id = %s")
                        params.append(channel_id)

                    if start_date:
                        conditions.append("created_at >= %s")
                        # 移除时区信息，只保留日期时间值
                        if start_date.tzinfo is not None:
                            start_date = start_date.replace(tzinfo=None)
                        params.append(start_date)

                    if end_date:
                        conditions.append("created_at <= %s")
                        # 移除时区信息，只保留日期时间值
                        if end_date.tzinfo is not None:
                            end_date = end_date.replace(tzinfo=None)
                        params.append(end_date)

                    where_clause = " AND ".join(conditions) if conditions else "1=1"

                    query = f"""
                        SELECT * FROM summaries
                        WHERE {where_clause}
                        ORDER BY created_at DESC
                        LIMIT %s OFFSET %s
                    """
                    params.append(limit)
                    params.append(offset)

                    await cursor.execute(query, params)
                    rows = await cursor.fetchall()

                    return self._parse_summary_rows(rows)

        except Exception as e:
            logger.error(f"查询总结记录失败: {type(e).__name__}: {e}", exc_info=True)
            return []

    async def count_summaries(
        self,
        channel_id: str | None = None,
        start_date: datetime | None = None,
        end_date: datetime | None = None,
    ) -> int:
        """统计历史总结总数"""
        try:
            async with self.pool.acquire() as conn:
                async with conn.cursor() as cursor:
                    conditions = []
                    params = []

                    if channel_id:
                        conditions.append("channel_id = %s")
                        params.append(channel_id)

                    if start_date:
                        conditions.append("created_at >= %s")
                        naive_start = (
                            start_date.replace(tzinfo=None)
                            if start_date.tzinfo is not None
                            else start_date
                        )
                        params.append(naive_start)

                    if end_date:
                        conditions.append("created_at <= %s")
                        naive_end = (
                            end_date.replace(tzinfo=None)
                            if end_date.tzinfo is not None
                            else end_date
                        )
                        params.append(naive_end)

                    where_clause = " AND ".join(conditions) if conditions else "1=1"
                    await cursor.execute(
                        f"SELECT COUNT(*) FROM summaries WHERE {where_clause}", params
                    )
                    row = await cursor.fetchone()
                    return int(row[0] or 0) if row else 0

        except Exception as e:
            logger.error(f"统计总结记录失败: {type(e).__name__}: {e}", exc_info=True)
            return 0

    @staticmethod
    def _parse_summary_rows(rows: list[dict[str, Any]]) -> list[dict[str, Any]]:
        """解析总结记录中的 JSON 字段"""
        summaries = []
        for row in rows:
            if row.get("summary_message_ids"):
                try:
                    row["summary_message_ids"] = json.loads(row["summary_message_ids"])
                except Exception:
                    row["summary_message_ids"] = []
            else:
                row["summary_message_ids"] = []
            summaries.append(row)

        logger.info(f"查询到 {len(summaries)} 条总结记录")
        return summaries

    async def get_summary_by_id(self, summary_id: int) -> dict[str, Any] | None:
        """根据ID获取单条总结"""
        try:
            async with self.pool.acquire() as conn:
                async with conn.cursor(aiomysql.DictCursor) as cursor:
                    await cursor.execute("SELECT * FROM summaries WHERE id = %s", (summary_id,))
                    row = await cursor.fetchone()

                    if row:
                        if row.get("summary_message_ids"):
                            try:
                                row["summary_message_ids"] = json.loads(row["summary_message_ids"])
                            except Exception:
                                row["summary_message_ids"] = []
                        else:
                            row["summary_message_ids"] = []
                        return row
                    return None

        except Exception as e:
            logger.error(
                f"查询总结记录失败 (ID={summary_id}): {type(e).__name__}: {e}", exc_info=True
            )
            return None

    async def delete_old_summaries(self, days: int = 90) -> int:
        """删除旧总结记录"""
        try:
            async with self.pool.acquire() as conn:
                async with conn.cursor() as cursor:
                    cutoff_date = datetime.now(UTC) - timedelta(days=days)

                    await cursor.execute(
                        """
                        DELETE FROM summaries
                        WHERE created_at < %s
                    """,
                        (cutoff_date,),
                    )

                    deleted_count = cursor.rowcount
                    await conn.commit()

                    logger.info(f"已删除 {deleted_count} 条旧总结记录 (超过 {days} 天)")
                    return deleted_count

        except Exception as e:
            logger.error(f"删除旧总结记录失败: {type(e).__name__}: {e}", exc_info=True)
            return 0

    async def get_statistics(self, channel_id: str | None = None) -> dict[str, Any]:
        """获取统计信息"""
        try:
            async with self.pool.acquire() as conn:
                async with conn.cursor() as cursor:
                    channel_condition = "WHERE channel_id = %s" if channel_id else ""
                    params = [channel_id] if channel_id else []

                    # 总总结次数
                    await cursor.execute(
                        f"""
                        SELECT COUNT(*) FROM summaries
                        {channel_condition}
                    """,
                        params,
                    )
                    total_count = (await cursor.fetchone())[0]

                    # 按类型统计
                    await cursor.execute(
                        f"""
                        SELECT summary_type, COUNT(*) as count
                        FROM summaries
                        {channel_condition}
                        GROUP BY summary_type
                    """,
                        params,
                    )
                    type_stats = dict(await cursor.fetchall())

                    # 总消息数
                    await cursor.execute(
                        f"""
                        SELECT SUM(message_count) FROM summaries
                        {channel_condition}
                    """,
                        params,
                    )
                    total_messages = (await cursor.fetchone())[0] or 0

                    # 平均消息数
                    avg_messages = total_messages / total_count if total_count > 0 else 0

                    # 最近总结时间
                    await cursor.execute(
                        f"""
                        SELECT MAX(created_at) FROM summaries
                        {channel_condition}
                    """,
                        params,
                    )
                    last_summary_time = (await cursor.fetchone())[0]

                    # 本周统计
                    week_ago = (datetime.now(UTC) - timedelta(days=7)).isoformat()
                    if channel_id:
                        await cursor.execute(
                            """
                            SELECT COUNT(*) FROM summaries
                            WHERE channel_id = %s AND created_at >= %s
                        """,
                            [channel_id, week_ago],
                        )
                    else:
                        await cursor.execute(
                            """
                            SELECT COUNT(*) FROM summaries
                            WHERE created_at >= %s
                        """,
                            [week_ago],
                        )
                    week_count = (await cursor.fetchone())[0] or 0

                    # 本月统计
                    month_ago = (datetime.now(UTC) - timedelta(days=30)).isoformat()
                    if channel_id:
                        await cursor.execute(
                            """
                            SELECT COUNT(*) FROM summaries
                            WHERE channel_id = %s AND created_at >= %s
                        """,
                            [channel_id, month_ago],
                        )
                    else:
                        await cursor.execute(
                            """
                            SELECT COUNT(*) FROM summaries
                            WHERE created_at >= %s
                        """,
                            [month_ago],
                        )
                    month_count = (await cursor.fetchone())[0] or 0

            stats = {
                "total_count": total_count,
                "type_stats": type_stats,
                "total_messages": total_messages,
                "avg_messages": round(avg_messages, 1),
                "last_summary_time": last_summary_time.isoformat() if last_summary_time else None,
                "week_count": week_count,
                "month_count": month_count,
            }

            logger.info(f"统计数据获取成功: {stats}")
            return stats

        except Exception as e:
            logger.error(f"获取统计信息失败: {type(e).__name__}: {e}", exc_info=True)
            return {}

    async def get_channel_ranking(self, limit: int = 10) -> list[dict[str, Any]]:
        """获取频道排行(按总结次数)"""
        try:
            async with self.pool.acquire() as conn:
                async with conn.cursor(aiomysql.DictCursor) as cursor:
                    await cursor.execute(
                        """
                        SELECT
                            channel_id,
                            channel_name,
                            COUNT(*) as summary_count,
                            SUM(message_count) as total_messages
                        FROM summaries
                        GROUP BY channel_id, channel_name
                        ORDER BY summary_count DESC
                        LIMIT %s
                    """,
                        (limit,),
                    )

                    ranking = await cursor.fetchall()
                    logger.info(f"频道排行获取成功: {len(ranking)} 个频道")
                    return ranking

        except Exception as e:
            logger.error(f"获取频道排行失败: {type(e).__name__}: {e}", exc_info=True)
            return []

    async def export_summaries(
        self, output_format: str = "json", channel_id: str | None = None
    ) -> str | None:
        """导出历史记录"""
        try:
            summaries = await self.get_summaries(channel_id=channel_id, limit=10000)

            if not summaries:
                logger.warning("没有数据可导出")
                return None

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
            json.dump(summaries, f, ensure_ascii=False, indent=2, default=str)

    def _export_csv(self, summaries: list[dict], filename: str):
        """导出为CSV格式"""
        import csv

        if not summaries:
            return

        fieldnames = list(summaries[0].keys())

        with open(filename, "w", encoding="utf-8", newline="") as f:
            writer = csv.DictWriter(f, fieldnames=fieldnames)
            writer.writeheader()

            for summary in summaries:
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

                type_cn = get_text(f"summary_type.{summary_type}")

                f.write(f"## {channel_name} - {created_at} ({type_cn})\n\n")
                f.write(f"**消息数量**: {message_count}\n\n")
                f.write(f"**总结内容**:\n\n{summary_text}\n\n")
                f.write("---\n\n")

    # ============ 配额管理方法 ============

    async def get_quota_usage(self, user_id: int, date: str | None = None) -> dict[str, Any]:
        """获取用户配额使用情况"""
        try:
            if date is None:
                date = datetime.now(UTC).strftime("%Y-%m-%d")

            async with self.pool.acquire() as conn:
                async with conn.cursor(aiomysql.DictCursor) as cursor:
                    await cursor.execute(
                        """
                        SELECT usage_count, last_reset
                        FROM usage_quota
                        WHERE user_id = %s AND query_date = %s
                    """,
                        (user_id, date),
                    )

                    row = await cursor.fetchone()

                    if row:
                        return {
                            "user_id": user_id,
                            "date": date,
                            "usage_count": row["usage_count"],
                            "last_reset": row["last_reset"].isoformat()
                            if row["last_reset"]
                            else None,
                        }
                    else:
                        return {
                            "user_id": user_id,
                            "date": date,
                            "usage_count": 0,
                            "last_reset": None,
                        }

        except Exception as e:
            logger.error(f"获取配额使用失败: {type(e).__name__}: {e}", exc_info=True)
            return {"user_id": user_id, "date": date, "usage_count": 0, "last_reset": None}

    async def check_and_increment_quota(
        self, user_id: int, daily_limit: int, is_admin: bool = False
    ) -> dict[str, Any]:
        """检查并增加配额使用"""
        try:
            if is_admin:
                return {"allowed": True, "remaining": -1, "used": 0, "is_admin": True}

            date = datetime.now(UTC).strftime("%Y-%m-%d")
            async with self.pool.acquire() as conn:
                async with conn.cursor() as cursor:
                    # 获取当前使用次数
                    await cursor.execute(
                        """
                        SELECT usage_count FROM usage_quota
                        WHERE user_id = %s AND query_date = %s
                    """,
                        (user_id, date),
                    )

                    row = await cursor.fetchone()
                    current_usage = row[0] if row else 0

                    # 检查是否超限
                    if current_usage >= daily_limit:
                        return {
                            "allowed": False,
                            "remaining": 0,
                            "used": current_usage,
                            "daily_limit": daily_limit,
                        }

                    # 增加使用次数
                    new_usage = current_usage + 1
                    if row:
                        await cursor.execute(
                            """
                            UPDATE usage_quota
                            SET usage_count = %s
                            WHERE user_id = %s AND query_date = %s
                        """,
                            (new_usage, user_id, date),
                        )
                    else:
                        await cursor.execute(
                            """
                            INSERT INTO usage_quota (user_id, query_date, usage_count)
                            VALUES (%s, %s, %s)
                        """,
                            (user_id, date, new_usage),
                        )

                    await conn.commit()

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

    async def get_total_daily_usage(self, date: str | None = None) -> int:
        """获取指定日期的总使用次数"""
        try:
            if date is None:
                date = datetime.now(UTC).strftime("%Y-%m-%d")

            async with self.pool.acquire() as conn:
                async with conn.cursor() as cursor:
                    await cursor.execute(
                        """
                        SELECT SUM(usage_count) FROM usage_quota
                        WHERE query_date = %s
                    """,
                        (date,),
                    )

                    result = await cursor.fetchone()
                    return result[0] if result and result[0] else 0

        except Exception as e:
            logger.error(f"获取总使用次数失败: {type(e).__name__}: {e}", exc_info=True)
            return 0

    async def reset_quota_if_new_day(self, user_id: int) -> None:
        """如果是新的一天，重置用户配额"""
        try:
            today = datetime.now(UTC).strftime("%Y-%m-%d")
            async with self.pool.acquire() as conn:
                async with conn.cursor() as cursor:
                    await cursor.execute(
                        """
                        SELECT query_date FROM usage_quota
                        WHERE user_id = %s
                        ORDER BY query_date DESC
                        LIMIT 1
                    """,
                        (user_id,),
                    )

                    row = await cursor.fetchone()
                    if row and row[0] != today:
                        logger.info(f"检测到新的一天，重置用户 {user_id} 配额")

        except Exception as e:
            logger.error(f"重置配额失败: {type(e).__name__}: {e}", exc_info=True)

    # ============ 频道画像方法 ============

    async def get_channel_profile(self, channel_id: str) -> dict[str, Any] | None:
        """获取频道画像"""
        try:
            async with self.pool.acquire() as conn:
                async with conn.cursor(aiomysql.DictCursor) as cursor:
                    await cursor.execute(
                        "SELECT * FROM channel_profiles WHERE channel_id = %s", (channel_id,)
                    )
                    row = await cursor.fetchone()

                    if row:
                        # 解析JSON字段
                        try:
                            if row.get("topics"):
                                row["topics"] = json.loads(row["topics"])
                            if row.get("keywords_freq"):
                                row["keywords_freq"] = json.loads(row["keywords_freq"])
                        except Exception:
                            pass
                        return row
                    return None

        except Exception as e:
            logger.error(f"获取频道画像失败: {type(e).__name__}: {e}", exc_info=True)
            return None

    async def update_channel_profile(
        self,
        channel_id: str,
        channel_name: str,
        keywords: list[str] = None,
        topics: list[str] = None,
        sentiment: str = None,
        entities: list[str] = None,
    ) -> None:
        """更新频道画像"""
        try:
            async with self.pool.acquire() as conn:
                async with conn.cursor() as cursor:
                    # 获取现有画像
                    await cursor.execute(
                        "SELECT * FROM channel_profiles WHERE channel_id = %s", (channel_id,)
                    )
                    existing = await cursor.fetchone()

                    # 统计该频道的总结数和平均消息长度
                    await cursor.execute(
                        """
                        SELECT COUNT(*) as count, AVG(message_count) as avg_len
                        FROM summaries
                        WHERE channel_id = %s
                    """,
                        (channel_id,),
                    )
                    stats = await cursor.fetchone()

                    total_summaries = stats[0] if stats else 0
                    avg_message_length = float(stats[1]) if stats and stats[1] else 0

                    # 获取当前画像或创建新的
                    if existing:
                        # 更新关键词频率
                        try:
                            keywords_freq = json.loads(existing[4]) if existing[4] else {}
                        except Exception:
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
                    keywords_json = (
                        json.dumps(keywords_freq, ensure_ascii=False) if keywords_freq else None
                    )

                    # 推断频道风格
                    if topics:
                        tech_keywords = ["AI", "编程", "技术", "开发", "Python", "GPT", "API"]
                        if any(kw in " ".join(topics) for kw in tech_keywords):
                            style = "tech"
                        else:
                            style = "casual"
                    else:
                        style = "neutral"

                    now = datetime.now(UTC)

                    if existing:
                        await cursor.execute(
                            """
                            UPDATE channel_profiles
                            SET channel_name = %s, style = %s, topics = %s,
                                keywords_freq = %s, tone = %s, avg_message_length = %s,
                                total_summaries = %s, last_updated = %s
                            WHERE channel_id = %s
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
                        await cursor.execute(
                            """
                            INSERT INTO channel_profiles (
                                channel_id, channel_name, style, topics, keywords_freq,
                                tone, avg_message_length, total_summaries, last_updated
                            ) VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s)
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

                    await conn.commit()
                    logger.info(f"更新频道画像: {channel_name} ({channel_id})")

        except Exception as e:
            logger.error(f"更新频道画像失败: {type(e).__name__}: {e}", exc_info=True)

    async def update_summary_metadata(
        self,
        summary_id: int,
        keywords: list[str] = None,
        topics: list[str] = None,
        sentiment: str = None,
        entities: list[str] = None,
    ) -> None:
        """更新总结的元数据"""
        try:
            async with self.pool.acquire() as conn:
                async with conn.cursor() as cursor:
                    keywords_json = json.dumps(keywords, ensure_ascii=False) if keywords else None
                    topics_json = json.dumps(topics, ensure_ascii=False) if topics else None
                    entities_json = json.dumps(entities, ensure_ascii=False) if entities else None

                    await cursor.execute(
                        """
                        UPDATE summaries
                        SET keywords = %s, topics = %s, sentiment = %s, entities = %s
                        WHERE id = %s
                    """,
                        (keywords_json, topics_json, sentiment, entities_json, summary_id),
                    )

                    await conn.commit()
                    logger.info(f"更新总结元数据: ID={summary_id}")

        except Exception as e:
            logger.error(f"更新总结元数据失败: {type(e).__name__}: {e}", exc_info=True)

    # ============ 对话历史管理方法 ============

    async def save_conversation(
        self,
        user_id: int,
        session_id: str,
        role: str,
        content: str,
        metadata: dict[str, Any] | None = None,
    ) -> bool:
        """保存对话记录"""
        try:
            async with self.pool.acquire() as conn:
                async with conn.cursor() as cursor:
                    metadata_json = json.dumps(metadata, ensure_ascii=False) if metadata else None

                    await cursor.execute(
                        """
                        INSERT INTO conversation_history
                        (user_id, session_id, role, content, metadata)
                        VALUES (%s, %s, %s, %s, %s)
                    """,
                        (user_id, session_id, role, content, metadata_json),
                    )

                    await conn.commit()

                    logger.debug(
                        f"保存对话记录: user_id={user_id}, session={session_id}, role={role}"
                    )
                    return True

        except Exception as e:
            logger.error(f"保存对话记录失败: {type(e).__name__}: {e}", exc_info=True)
            return False

    async def get_conversation_history(
        self, user_id: int, session_id: str, limit: int = 20
    ) -> list[dict[str, Any]]:
        """获取用户的对话历史"""
        try:
            async with self.pool.acquire() as conn:
                async with conn.cursor(aiomysql.DictCursor) as cursor:
                    await cursor.execute(
                        """
                        SELECT role, content, timestamp, metadata
                        FROM conversation_history
                        WHERE user_id = %s AND session_id = %s
                        ORDER BY timestamp ASC
                        LIMIT %s
                    """,
                        (user_id, session_id, limit),
                    )

                    rows = await cursor.fetchall()

                    history = []
                    for row in rows:
                        item = {
                            "role": row["role"],
                            "content": row["content"],
                            "timestamp": row["timestamp"].isoformat() if row["timestamp"] else None,
                        }
                        # 解析元数据
                        if row.get("metadata"):
                            try:
                                item["metadata"] = json.loads(row["metadata"])
                            except Exception:
                                pass
                        history.append(item)

                    logger.debug(
                        f"获取对话历史: user_id={user_id}, session={session_id}, 条数={len(history)}"
                    )
                    return history

        except Exception as e:
            logger.error(f"获取对话历史失败: {type(e).__name__}: {e}", exc_info=True)
            return []

    async def get_last_session_time(self, user_id: int) -> str | None:
        """获取用户最后一次对话时间"""
        try:
            async with self.pool.acquire() as conn:
                async with conn.cursor() as cursor:
                    await cursor.execute(
                        """
                        SELECT timestamp
                        FROM conversation_history
                        WHERE user_id = %s
                        ORDER BY timestamp DESC
                        LIMIT 1
                    """,
                        (user_id,),
                    )

                    result = await cursor.fetchone()
                    return result[0].isoformat() if result else None

        except Exception as e:
            logger.error(f"获取最后会话时间失败: {type(e).__name__}: {e}", exc_info=True)
            return None

    async def clear_user_conversations(self, user_id: int, session_id: str | None = None) -> int:
        """清除用户的对话历史"""
        try:
            async with self.pool.acquire() as conn:
                async with conn.cursor() as cursor:
                    if session_id:
                        await cursor.execute(
                            """
                            DELETE FROM conversation_history
                            WHERE user_id = %s AND session_id = %s
                        """,
                            (user_id, session_id),
                        )
                    else:
                        await cursor.execute(
                            """
                            DELETE FROM conversation_history
                            WHERE user_id = %s
                        """,
                            (user_id,),
                        )

                    deleted_count = cursor.rowcount
                    await conn.commit()

                    logger.info(
                        f"清除对话历史: user_id={user_id}, session={session_id}, 删除{deleted_count}条"
                    )
                    return deleted_count

        except Exception as e:
            logger.error(f"清除对话历史失败: {type(e).__name__}: {e}", exc_info=True)
            return 0

    async def get_session_count(self, user_id: int) -> int:
        """获取用户的会话总数"""
        try:
            async with self.pool.acquire() as conn:
                async with conn.cursor() as cursor:
                    await cursor.execute(
                        """
                        SELECT COUNT(DISTINCT session_id)
                        FROM conversation_history
                        WHERE user_id = %s
                    """,
                        (user_id,),
                    )

                    result = await cursor.fetchone()
                    return result[0] if result else 0

        except Exception as e:
            logger.error(f"获取会话数量失败: {type(e).__name__}: {e}", exc_info=True)
            return 0

    async def delete_old_conversations(self, days: int = 7) -> int:
        """删除旧的对话记录（定期清理）"""
        try:
            async with self.pool.acquire() as conn:
                async with conn.cursor() as cursor:
                    cutoff_date = datetime.now(UTC) - timedelta(days=days)

                    await cursor.execute(
                        """
                        DELETE FROM conversation_history
                        WHERE timestamp < %s
                    """,
                        (cutoff_date,),
                    )

                    deleted_count = cursor.rowcount
                    await conn.commit()

                    logger.info(f"已删除 {deleted_count} 条旧对话记录 (超过 {days} 天)")
                    return deleted_count

        except Exception as e:
            logger.error(f"删除旧对话记录失败: {type(e).__name__}: {e}", exc_info=True)
            return 0

    # ============ 用户管理方法 ============

    async def register_user(
        self, user_id: int, username: str = None, first_name: str = None, is_admin: bool = False
    ) -> bool:
        """注册新用户"""
        try:
            async with self.pool.acquire() as conn:
                async with conn.cursor() as cursor:
                    now = datetime.now(UTC)

                    # 先检查用户是否存在
                    await cursor.execute(
                        "SELECT registered_at FROM users WHERE user_id = %s", (user_id,)
                    )
                    existing = await cursor.fetchone()

                    if existing:
                        # 用户已存在，更新信息
                        await cursor.execute(
                            """
                            UPDATE users
                            SET username = %s, first_name = %s, last_active = %s, is_admin = %s
                            WHERE user_id = %s
                        """,
                            (username, first_name, now, 1 if is_admin else 0, user_id),
                        )
                    else:
                        # 新用户，插入记录
                        await cursor.execute(
                            """
                            INSERT INTO users
                            (user_id, username, first_name, registered_at, last_active, is_admin)
                            VALUES (%s, %s, %s, %s, %s, %s)
                        """,
                            (user_id, username, first_name, now, now, 1 if is_admin else 0),
                        )

                    await conn.commit()

                    logger.info(f"用户注册成功: user_id={user_id}, username={username}")
                    return True

        except Exception as e:
            logger.error(f"用户注册失败: {type(e).__name__}: {e}", exc_info=True)
            return False

    async def get_user(self, user_id: int) -> dict[str, Any] | None:
        """获取用户信息"""
        try:
            async with self.pool.acquire() as conn:
                async with conn.cursor(aiomysql.DictCursor) as cursor:
                    await cursor.execute("SELECT * FROM users WHERE user_id = %s", (user_id,))
                    row = await cursor.fetchone()

                    if row:
                        # 解析preferences JSON
                        if row.get("preferences"):
                            try:
                                row["preferences"] = json.loads(row["preferences"])
                            except Exception:
                                pass
                        return row
                    return None

        except Exception as e:
            logger.error(f"获取用户信息失败: {type(e).__name__}: {e}", exc_info=True)
            return None

    async def update_user_activity(self, user_id: int) -> bool:
        """更新用户最后活跃时间"""
        try:
            async with self.pool.acquire() as conn:
                async with conn.cursor() as cursor:
                    now = datetime.now(UTC)

                    await cursor.execute(
                        """
                        UPDATE users SET last_active = %s
                        WHERE user_id = %s
                    """,
                        (now, user_id),
                    )

                    await conn.commit()
                    return True

        except Exception as e:
            logger.error(f"更新用户活跃时间失败: {type(e).__name__}: {e}", exc_info=True)
            return False

    async def set_user_admin(self, user_id: int, is_admin: bool) -> bool:
        """设置用户管理员权限"""
        try:
            async with self.pool.acquire() as conn:
                async with conn.cursor() as cursor:
                    await cursor.execute(
                        """
                        UPDATE users SET is_admin = %s
                        WHERE user_id = %s
                    """,
                        (1 if is_admin else 0, user_id),
                    )

                    await conn.commit()

                    logger.info(f"更新用户权限: user_id={user_id}, is_admin={is_admin}")
                    return True

        except Exception as e:
            logger.error(f"设置用户权限失败: {type(e).__name__}: {e}", exc_info=True)
            return False

    async def get_registered_users(
        self, active_days: int = 30, limit: int = 100
    ) -> list[dict[str, Any]]:
        """获取注册用户列表"""
        try:
            async with self.pool.acquire() as conn:
                async with conn.cursor(aiomysql.DictCursor) as cursor:
                    cutoff = (datetime.now(UTC) - timedelta(days=active_days)).isoformat()

                    await cursor.execute(
                        """
                        SELECT * FROM users
                        WHERE last_active >= %s
                        ORDER BY last_active DESC
                        LIMIT %s
                    """,
                        (cutoff, limit),
                    )

                    return await cursor.fetchall()

        except Exception as e:
            logger.error(f"获取用户列表失败: {type(e).__name__}: {e}", exc_info=True)
            return []

    async def is_user_registered(self, user_id: int) -> bool:
        """检查用户是否已注册"""
        try:
            async with self.pool.acquire() as conn:
                async with conn.cursor() as cursor:
                    await cursor.execute("SELECT 1 FROM users WHERE user_id = %s", (user_id,))
                    result = await cursor.fetchone()
                    return result is not None

        except Exception as e:
            logger.error(f"检查用户注册状态失败: {type(e).__name__}: {e}", exc_info=True)
            return False

    # ============ 订阅管理方法 ============

    async def add_subscription(
        self, user_id: int, channel_id: str, channel_name: str = None, sub_type: str = "summary"
    ) -> bool:
        """添加订阅"""
        try:
            async with self.pool.acquire() as conn:
                async with conn.cursor() as cursor:
                    await cursor.execute(
                        """
                        INSERT IGNORE INTO subscriptions
                        (user_id, channel_id, channel_name, sub_type)
                        VALUES (%s, %s, %s, %s)
                    """,
                        (user_id, channel_id, channel_name, sub_type),
                    )

                    await conn.commit()
                    affected = cursor.rowcount

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

    async def remove_subscription(
        self, user_id: int, channel_id: str = None, sub_type: str = None
    ) -> int:
        """移除订阅"""
        try:
            async with self.pool.acquire() as conn:
                async with conn.cursor() as cursor:
                    conditions = ["user_id = %s"]
                    params = [user_id]

                    if channel_id:
                        conditions.append("channel_id = %s")
                        params.append(channel_id)

                    if sub_type:
                        conditions.append("sub_type = %s")
                        params.append(sub_type)

                    query = f"DELETE FROM subscriptions WHERE {' AND '.join(conditions)}"
                    await cursor.execute(query, params)

                    deleted_count = cursor.rowcount
                    await conn.commit()

                    logger.info(f"移除订阅: user_id={user_id}, 删除{deleted_count}条")
                    return deleted_count

        except Exception as e:
            logger.error(f"移除订阅失败: {type(e).__name__}: {e}", exc_info=True)
            return 0

    async def get_user_subscriptions(
        self, user_id: int, sub_type: str = None
    ) -> list[dict[str, Any]]:
        """获取用户的订阅列表"""
        try:
            async with self.pool.acquire() as conn:
                async with conn.cursor(aiomysql.DictCursor) as cursor:
                    if sub_type:
                        await cursor.execute(
                            """
                            SELECT * FROM subscriptions
                            WHERE user_id = %s AND sub_type = %s AND enabled = TRUE
                            ORDER BY created_at DESC
                        """,
                            (user_id, sub_type),
                        )
                    else:
                        await cursor.execute(
                            """
                            SELECT * FROM subscriptions
                            WHERE user_id = %s AND enabled = TRUE
                            ORDER BY created_at DESC
                        """,
                            (user_id,),
                        )

                    return await cursor.fetchall()

        except Exception as e:
            logger.error(f"获取用户订阅失败: {type(e).__name__}: {e}", exc_info=True)
            return []

    async def get_channel_subscribers(
        self, channel_id: str, sub_type: str = "summary"
    ) -> list[int]:
        """获取频道的订阅用户ID列表"""
        try:
            async with self.pool.acquire() as conn:
                async with conn.cursor() as cursor:
                    await cursor.execute(
                        """
                        SELECT user_id FROM subscriptions
                        WHERE channel_id = %s AND sub_type = %s AND enabled = TRUE
                    """,
                        (channel_id, sub_type),
                    )

                    rows = await cursor.fetchall()
                    return [row[0] for row in rows]

        except Exception as e:
            logger.error(f"获取频道订阅者失败: {type(e).__name__}: {e}", exc_info=True)
            return []

    async def get_all_channels(self) -> list[dict[str, Any]]:
        """获取所有可用频道（从 summaries 表中提取并合并配置频道）"""
        try:
            async with self.pool.acquire() as conn:
                async with conn.cursor(aiomysql.DictCursor) as cursor:
                    await cursor.execute("""
                        SELECT
                            channel_id,
                            COALESCE(MAX(NULLIF(channel_name, '')), channel_id) AS channel_name,
                            MAX(created_at) AS last_summary_time,
                            COUNT(*) AS summary_count,
                            COALESCE(SUM(message_count), 0) AS message_count
                        FROM summaries
                        GROUP BY channel_id
                        ORDER BY last_summary_time DESC
                    """)

                    rows = await cursor.fetchall()

            channels = {row["channel_id"]: row for row in rows if row.get("channel_id")}

            try:
                from core.config import CHANNELS, normalize_channel_id

                for configured_channel in CHANNELS:
                    normalized = normalize_channel_id(configured_channel)
                    if normalized in channels:
                        continue
                    channels[normalized] = {
                        "channel_id": normalized,
                        "channel_name": normalized.split("/")[-1],
                        "last_summary_time": None,
                        "summary_count": 0,
                        "message_count": 0,
                    }
            except Exception as e:
                logger.warning(f"合并配置频道失败: {type(e).__name__}: {e}")

            return sorted(
                channels.values(),
                key=lambda item: item.get("last_summary_time") or datetime.min,
                reverse=True,
            )

        except Exception as e:
            logger.error(f"获取频道列表失败: {type(e).__name__}: {e}", exc_info=True)
            return []

    async def get_channel_summary_stats(self, channel_id: str) -> dict[str, Any]:
        """获取指定频道的总结统计"""
        try:
            async with self.pool.acquire() as conn:
                async with conn.cursor(aiomysql.DictCursor) as cursor:
                    await cursor.execute(
                        """
                        SELECT
                            channel_id,
                            COALESCE(MAX(NULLIF(channel_name, '')), channel_id) AS channel_name,
                            COUNT(*) AS summary_count,
                            COALESCE(SUM(message_count), 0) AS message_count,
                            MAX(created_at) AS last_summary_time,
                            MIN(created_at) AS first_summary_time
                        FROM summaries
                        WHERE channel_id = %s
                        GROUP BY channel_id
                    """,
                        (channel_id,),
                    )
                    row = await cursor.fetchone()

                    if row:
                        return row

                    return {
                        "channel_id": channel_id,
                        "channel_name": channel_id.split("/")[-1],
                        "summary_count": 0,
                        "message_count": 0,
                        "last_summary_time": None,
                        "first_summary_time": None,
                    }

        except Exception as e:
            logger.error(f"获取频道统计失败: {type(e).__name__}: {e}", exc_info=True)
            return {}

    async def get_recent_summaries(
        self,
        channel_id: str | None = None,
        limit: int = 5,
        start_date: datetime | None = None,
        end_date: datetime | None = None,
    ) -> list[dict[str, Any]]:
        """获取最近总结列表"""
        return await self.get_summaries(
            channel_id=channel_id,
            limit=limit,
            start_date=start_date,
            end_date=end_date,
        )

    async def is_subscribed(self, user_id: int, channel_id: str, sub_type: str = "summary") -> bool:
        """检查用户是否已订阅某频道"""
        try:
            async with self.pool.acquire() as conn:
                async with conn.cursor() as cursor:
                    await cursor.execute(
                        """
                        SELECT 1 FROM subscriptions
                        WHERE user_id = %s AND channel_id = %s
                              AND sub_type = %s AND enabled = TRUE
                    """,
                        (user_id, channel_id, sub_type),
                    )

                    result = await cursor.fetchone()
                    return result is not None

        except Exception as e:
            logger.error(f"检查订阅状态失败: {type(e).__name__}: {e}", exc_info=True)
            return False

    # ============ 请求队列方法 ============

    async def create_request(
        self,
        request_type: str,
        requested_by: int,
        target_channel: str = None,
        params: dict[str, Any] = None,
    ) -> int | None:
        """创建请求"""
        try:
            async with self.pool.acquire() as conn:
                async with conn.cursor() as cursor:
                    params_json = json.dumps(params, ensure_ascii=False) if params else None

                    await cursor.execute(
                        """
                        INSERT INTO request_queue
                        (request_type, requested_by, target_channel, params)
                        VALUES (%s, %s, %s, %s)
                    """,
                        (request_type, requested_by, target_channel, params_json),
                    )

                    await conn.commit()
                    request_id = cursor.lastrowid

                    logger.info(
                        f"创建请求: id={request_id}, type={request_type}, user={requested_by}"
                    )
                    return request_id

        except Exception as e:
            logger.error(f"创建请求失败: {type(e).__name__}: {e}", exc_info=True)
            return None

    async def get_pending_requests(self, limit: int = 10) -> list[dict[str, Any]]:
        """获取待处理的请求列表"""
        try:
            async with self.pool.acquire() as conn:
                async with conn.cursor(aiomysql.DictCursor) as cursor:
                    await cursor.execute(
                        """
                        SELECT * FROM request_queue
                        WHERE status = 'pending'
                        ORDER BY created_at ASC
                        LIMIT %s
                    """,
                        (limit,),
                    )

                    requests = await cursor.fetchall()

                    # 解析JSON字段
                    for req in requests:
                        if req.get("params"):
                            try:
                                req["params"] = json.loads(req["params"])
                            except Exception:
                                pass
                        if req.get("result"):
                            try:
                                req["result"] = json.loads(req["result"])
                            except Exception:
                                pass

                    return requests

        except Exception as e:
            logger.error(f"获取待处理请求失败: {type(e).__name__}: {e}", exc_info=True)
            return []

    async def update_request_status(self, request_id: int, status: str, result: Any = None) -> bool:
        """更新请求状态"""
        try:
            async with self.pool.acquire() as conn:
                async with conn.cursor() as cursor:
                    result_json = json.dumps(result, ensure_ascii=False) if result else None

                    await cursor.execute(
                        """
                        UPDATE request_queue
                        SET status = %s, result = %s, processed_at = NOW()
                        WHERE id = %s
                    """,
                        (status, result_json, request_id),
                    )

                    await conn.commit()

                    logger.info(f"更新请求状态: id={request_id}, status={status}")
                    return True

        except Exception as e:
            logger.error(f"更新请求状态失败: {type(e).__name__}: {e}", exc_info=True)
            return False

    async def get_request_status(self, request_id: int) -> dict[str, Any] | None:
        """获取请求状态"""
        try:
            async with self.pool.acquire() as conn:
                async with conn.cursor(aiomysql.DictCursor) as cursor:
                    await cursor.execute("SELECT * FROM request_queue WHERE id = %s", (request_id,))
                    req = await cursor.fetchone()

                    if req:
                        # 解析JSON字段
                        if req.get("params"):
                            try:
                                req["params"] = json.loads(req["params"])
                            except Exception:
                                pass
                        if req.get("result"):
                            try:
                                req["result"] = json.loads(req["result"])
                            except Exception:
                                pass
                        return req
                    return None

        except Exception as e:
            logger.error(f"获取请求状态失败: {type(e).__name__}: {e}", exc_info=True)
            return None

    async def cleanup_old_requests(self, days: int = 7) -> int:
        """清理旧请求记录"""
        try:
            async with self.pool.acquire() as conn:
                async with conn.cursor() as cursor:
                    cutoff_date = datetime.now(UTC) - timedelta(days=days)

                    await cursor.execute(
                        """
                        DELETE FROM request_queue
                        WHERE created_at < %s AND status IN ('completed', 'failed')
                    """,
                        (cutoff_date,),
                    )

                    deleted_count = cursor.rowcount
                    await conn.commit()

                    logger.info(f"清理旧请求: 删除 {deleted_count} 条记录")
                    return deleted_count

        except Exception as e:
            logger.error(f"清理旧请求失败: {type(e).__name__}: {e}", exc_info=True)
            return 0

    # ============ 通知队列方法 ============

    async def create_notification(
        self, user_id: int, notification_type: str, content: dict[str, Any]
    ) -> int | None:
        """创建通知"""
        try:
            async with self.pool.acquire() as conn:
                async with conn.cursor() as cursor:
                    content_json = json.dumps(content, ensure_ascii=False) if content else None

                    await cursor.execute(
                        """
                        INSERT INTO notification_queue
                        (user_id, notification_type, content)
                        VALUES (%s, %s, %s)
                    """,
                        (user_id, notification_type, content_json),
                    )

                    await conn.commit()
                    notification_id = cursor.lastrowid

                    logger.info(
                        f"创建通知: id={notification_id}, type={notification_type}, user={user_id}"
                    )
                    return notification_id

        except Exception as e:
            logger.error(f"创建通知失败: {type(e).__name__}: {e}", exc_info=True)
            return None

    async def get_pending_notifications(self, limit: int = 50) -> list[dict[str, Any]]:
        """获取待发送的通知列表"""
        try:
            async with self.pool.acquire() as conn:
                async with conn.cursor(aiomysql.DictCursor) as cursor:
                    await cursor.execute(
                        """
                        SELECT * FROM notification_queue
                        WHERE status = 'pending'
                        ORDER BY created_at ASC
                        LIMIT %s
                    """,
                        (limit,),
                    )

                    notifications = await cursor.fetchall()

                    # 解析JSON字段
                    for notif in notifications:
                        if notif.get("content"):
                            try:
                                notif["content"] = json.loads(notif["content"])
                            except Exception:
                                pass

                    return notifications

        except Exception as e:
            logger.error(f"获取待发送通知失败: {type(e).__name__}: {e}", exc_info=True)
            return []

    async def update_notification_status(self, notification_id: int, status: str) -> bool:
        """更新通知状态"""
        try:
            async with self.pool.acquire() as conn:
                async with conn.cursor() as cursor:
                    await cursor.execute(
                        """
                        UPDATE notification_queue
                        SET status = %s, sent_at = NOW()
                        WHERE id = %s
                    """,
                        (status, notification_id),
                    )

                    await conn.commit()

                    logger.info(f"更新通知状态: id={notification_id}, status={status}")
                    return True

        except Exception as e:
            logger.error(f"更新通知状态失败: {type(e).__name__}: {e}", exc_info=True)
            return False

    async def cleanup_old_notifications(self, days: int = 7) -> int:
        """清理旧通知记录"""
        try:
            async with self.pool.acquire() as conn:
                async with conn.cursor() as cursor:
                    cutoff_date = datetime.now(UTC) - timedelta(days=days)

                    await cursor.execute(
                        """
                        DELETE FROM notification_queue
                        WHERE created_at < %s AND status IN ('sent', 'failed')
                    """,
                        (cutoff_date,),
                    )

                    deleted_count = cursor.rowcount
                    await conn.commit()

                    logger.info(f"清理旧通知: 删除 {deleted_count} 条记录")
                    return deleted_count

        except Exception as e:
            logger.error(f"清理旧通知失败: {type(e).__name__}: {e}", exc_info=True)
            return 0

    # ============ 数据库清空方法 ============

    async def clear_all_data(self) -> dict[str, int]:
        """清空所有数据表（保留表结构和 db_version）"""
        # 按外键依赖顺序清空表
        tables = [
            "poll_voters",  # 依赖 poll_regenerations
            "poll_regenerations",
            "submissions",
            "forwarded_messages",
            "forwarding_stats",
            "notification_queue",
            "request_queue",
            "subscriptions",
            "conversation_history",
            "users",
            "channel_profiles",
            "usage_quota",
            "summaries",
            "system_audit_logs",
        ]

        results = {}
        try:
            async with self.pool.acquire() as conn:
                async with conn.cursor() as cursor:
                    # 禁用外键检查（避免因外键约束导致删除失败）
                    await cursor.execute("SET FOREIGN_KEY_CHECKS = 0")

                    for table in tables:
                        try:
                            await cursor.execute(f"DELETE FROM {table}")
                            count = cursor.rowcount
                            results[table] = count
                            logger.info(f"已清空表 {table}，删除 {count} 行")
                        except Exception as e:
                            logger.error(f"清空表 {table} 失败: {e}")
                            results[table] = -1

                    # 恢复外键检查
                    await cursor.execute("SET FOREIGN_KEY_CHECKS = 1")
                    await conn.commit()

        except Exception as e:
            logger.error(f"清空数据库时出错: {type(e).__name__}: {e}", exc_info=True)
            # 确保外键检查被恢复
            try:
                async with self.pool.acquire() as conn:
                    async with conn.cursor() as cursor:
                        await cursor.execute("SET FOREIGN_KEY_CHECKS = 1")
            except Exception:
                pass

        return results

    # ============ 通用查询方法 ============

    async def get_user_info(self, user_id: int) -> dict[str, Any] | None:
        """获取用户信息（别名方法，兼容请求处理器）"""
        return await self.get_user(user_id)

    async def get_qa_bot_statistics(self) -> dict[str, Any]:
        """获取问答Bot的详细统计信息"""
        try:
            async with self.pool.acquire() as conn:
                async with conn.cursor() as cursor:
                    stats = {}

                    # 用户统计
                    await cursor.execute("SELECT COUNT(*) FROM users")
                    stats["total_users"] = (await cursor.fetchone())[0]

                    # 活跃用户数（7天内有查询）
                    week_ago = (datetime.now(UTC) - timedelta(days=7)).isoformat()
                    await cursor.execute(
                        """
                        SELECT COUNT(DISTINCT user_id) FROM usage_quota
                        WHERE query_date >= %s
                    """,
                        (week_ago[:10],),
                    )
                    stats["active_users"] = (await cursor.fetchone())[0] or 0

                    # 今日新增用户
                    today = datetime.now(UTC).strftime("%Y-%m-%d")
                    await cursor.execute(
                        """
                        SELECT COUNT(*) FROM users
                        WHERE DATE(registered_at) = %s
                    """,
                        (today,),
                    )
                    stats["new_users_today"] = (await cursor.fetchone())[0] or 0

                    # 查询统计
                    await cursor.execute(
                        """
                        SELECT SUM(usage_count) FROM usage_quota
                        WHERE query_date = %s
                    """,
                        (today,),
                    )
                    stats["queries_today"] = (await cursor.fetchone())[0] or 0

                    # 本周查询次数
                    week_start = (datetime.now(UTC) - timedelta(days=7)).strftime("%Y-%m-%d")
                    await cursor.execute(
                        """
                        SELECT SUM(usage_count) FROM usage_quota
                        WHERE query_date >= %s
                    """,
                        (week_start,),
                    )
                    stats["queries_week"] = (await cursor.fetchone())[0] or 0

                    # 总查询次数
                    await cursor.execute("SELECT SUM(usage_count) FROM usage_quota")
                    stats["total_queries"] = (await cursor.fetchone())[0] or 0

                    # 订阅统计
                    await cursor.execute("SELECT COUNT(*) FROM subscriptions WHERE enabled = TRUE")
                    stats["total_subscriptions"] = (await cursor.fetchone())[0] or 0

                    # 活跃订阅数（用户7天内活跃）
                    await cursor.execute(
                        """
                        SELECT COUNT(DISTINCT s.user_id) FROM subscriptions s
                        INNER JOIN usage_quota q ON s.user_id = q.user_id
                        WHERE s.enabled = TRUE AND q.query_date >= %s
                    """,
                        (week_ago[:10],),
                    )
                    stats["active_subscriptions"] = (await cursor.fetchone())[0] or 0

                    # 请求统计
                    await cursor.execute(
                        "SELECT COUNT(*) FROM request_queue WHERE status = 'pending'"
                    )
                    stats["pending_requests"] = (await cursor.fetchone())[0] or 0

                    # 今日完成的请求数
                    await cursor.execute(
                        """
                        SELECT COUNT(*) FROM request_queue
                        WHERE status IN ('completed', 'failed')
                        AND DATE(processed_at) = %s
                    """,
                        (today,),
                    )
                    stats["completed_requests_today"] = (await cursor.fetchone())[0] or 0

                    # 总请求数
                    await cursor.execute("SELECT COUNT(*) FROM request_queue")
                    stats["total_requests"] = (await cursor.fetchone())[0] or 0

                    # 活跃用户排行（前10）
                    await cursor.execute("""
                        SELECT u.user_id, u.username, u.first_name,
                               SUM(q.usage_count) as query_count
                        FROM users u
                        INNER JOIN usage_quota q ON u.user_id = q.user_id
                        GROUP BY u.user_id, u.username, u.first_name
                        ORDER BY query_count DESC
                        LIMIT 10
                    """)
                    top_users = []
                    async for row in cursor:
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
                    await cursor.execute("""
                        SELECT channel_name, COUNT(*) as count
                        FROM subscriptions
                        WHERE enabled = TRUE AND channel_name IS NOT NULL
                        GROUP BY channel_name
                        ORDER BY count DESC
                    """)
                    channel_subs = {}
                    async for row in cursor:
                        channel_subs[row[0]] = row[1]
                    stats["channel_subscriptions"] = channel_subs

            logger.info(f"问答Bot统计信息获取成功: {stats}")
            return stats

        except Exception as e:
            logger.error(f"获取问答Bot统计信息失败: {type(e).__name__}: {e}", exc_info=True)
            return {}

    # ============ 数据库类型和版本信息 ============

    def get_database_type(self) -> str:
        """获取数据库类型"""
        return self._db_type

    def get_database_version(self) -> int:
        """获取数据库版本号"""
        return self._db_version

    async def execute_query(self, query: str, params: tuple = None) -> int:
        """
        执行SQL查询并返回影响的行数

        Args:
            query: SQL查询语句
            params: 查询参数

        Returns:
            int: 影响的行数
        """
        try:
            async with self.pool.acquire() as conn:
                async with conn.cursor() as cursor:
                    await cursor.execute(query, params or ())
                    await conn.commit()
                    affected_rows = cursor.rowcount
                    logger.debug(f"执行查询成功: {query[:100]}..., 影响行数: {affected_rows}")
                    return affected_rows
        except Exception as e:
            logger.error(f"执行查询失败: {type(e).__name__}: {e}", exc_info=True)
            raise

    # ============ 周报请求管理方法 ============

    async def add_summary_request(
        self,
        channel_id: str,
        message_id: int,
        request_type: str = "manual",
        requested_by: int = None,
    ) -> int | None:
        """添加周报请求记录"""
        try:
            async with self.pool.acquire() as conn:
                async with conn.cursor() as cursor:
                    await cursor.execute(
                        """
                        INSERT INTO request_queue
                        (request_type, requested_by, target_channel, params, status)
                        VALUES (%s, %s, %s, %s, 'pending')
                    """,
                        (
                            "summary_request",
                            requested_by,
                            channel_id,
                            json.dumps(
                                {"message_id": message_id, "request_type": request_type},
                                ensure_ascii=False,
                            ),
                        ),
                    )

                    await conn.commit()
                    request_id = cursor.lastrowid

                    logger.info(
                        f"添加周报请求: id={request_id}, channel={channel_id}, msg_id={message_id}, user={requested_by}"
                    )
                    return request_id

        except Exception as e:
            logger.error(f"添加周报请求失败: {type(e).__name__}: {e}", exc_info=True)
            return None

    async def check_pending_summary_request(self, channel_id: str) -> bool:
        """检查指定频道是否有待处理的周报请求"""
        try:
            async with self.pool.acquire() as conn:
                async with conn.cursor() as cursor:
                    # 检查最近10分钟内是否有pending状态的请求
                    await cursor.execute(
                        """
                        SELECT COUNT(*) FROM request_queue
                        WHERE request_type = 'summary_request'
                        AND target_channel = %s
                        AND status = 'pending'
                        AND created_at > DATE_SUB(NOW(), INTERVAL 10 MINUTE)
                    """,
                        (channel_id,),
                    )

                    count = (await cursor.fetchone())[0]
                    return count > 0

        except Exception as e:
            logger.error(f"检查待处理周报请求失败: {type(e).__name__}: {e}", exc_info=True)
            return False

    async def get_summary_requests(
        self, channel_id: str = None, status: str = None, limit: int = 20
    ) -> list[dict[str, Any]]:
        """获取周报请求列表"""
        try:
            async with self.pool.acquire() as conn:
                async with conn.cursor(aiomysql.DictCursor) as cursor:
                    conditions = ["request_type = %s"]
                    params = ["summary_request"]

                    if channel_id:
                        conditions.append("target_channel = %s")
                        params.append(channel_id)

                    if status:
                        conditions.append("status = %s")
                        params.append(status)

                    where_clause = " AND ".join(conditions)

                    await cursor.execute(
                        f"""
                        SELECT * FROM request_queue
                        WHERE {where_clause}
                        ORDER BY created_at DESC
                        LIMIT %s
                    """,
                        params + [limit],
                    )

                    requests = await cursor.fetchall()

                    # 解析JSON字段
                    for req in requests:
                        if req.get("params"):
                            try:
                                req["params"] = json.loads(req["params"])
                            except Exception:
                                pass

                    return requests

        except Exception as e:
            logger.error(f"获取周报请求列表失败: {type(e).__name__}: {e}", exc_info=True)
            return []

    async def update_summary_request_status(self, request_id: int, status: str) -> bool:
        """更新周报请求状态"""
        try:
            async with self.pool.acquire() as conn:
                async with conn.cursor() as cursor:
                    await cursor.execute(
                        """
                        UPDATE request_queue
                        SET status = %s, processed_at = NOW()
                        WHERE id = %s AND request_type = 'summary_request'
                    """,
                        (status, request_id),
                    )

                    await conn.commit()

                    logger.info(f"更新周报请求状态: id={request_id}, status={status}")
                    return True

        except Exception as e:
            logger.error(f"更新周报请求状态失败: {type(e).__name__}: {e}", exc_info=True)
            return False

    async def close(self):
        """关闭数据库连接池"""
        if self.pool:
            self.pool.close()
            await self.pool.wait_closed()
            logger.info("MySQL连接池已关闭")

    # ============ 频道消息转发功能方法 ============

    async def is_message_forwarded(
        self, message_id: str, target_channel: str, source_channel: str = None
    ) -> bool:
        """检查消息是否已转发到指定频道"""
        try:
            async with self.pool.acquire() as conn:
                async with conn.cursor() as cursor:
                    if source_channel:
                        # 使用三字段主键精确匹配
                        await cursor.execute(
                            """
                            SELECT 1 FROM forwarded_messages
                            WHERE message_id = %s AND target_channel = %s AND source_channel = %s
                        """,
                            (str(message_id), target_channel, source_channel),
                        )
                    else:
                        # 兼容旧版本：仅使用 message_id 和 target_channel
                        await cursor.execute(
                            """
                            SELECT 1 FROM forwarded_messages
                            WHERE message_id = %s AND target_channel = %s
                        """,
                            (str(message_id), target_channel),
                        )

                    result = await cursor.fetchone()
                    return result is not None

        except Exception as e:
            logger.error(f"检查消息转发状态失败: {type(e).__name__}: {e}", exc_info=True)
            return False

    async def add_forwarded_message(
        self,
        message_id: str,
        source_channel: str,
        target_channel: str,
        content_hash: str = None,
        timestamp: int = None,
    ) -> bool:
        """添加已转发消息记录"""
        try:
            async with self.pool.acquire() as conn:
                async with conn.cursor() as cursor:
                    if timestamp is None:
                        timestamp = int(datetime.now(UTC).timestamp())

                    await cursor.execute(
                        """
                        INSERT IGNORE INTO forwarded_messages
                        (message_id, source_channel, target_channel, content_hash, timestamp)
                        VALUES (%s, %s, %s, %s, %s)
                    """,
                        (str(message_id), source_channel, target_channel, content_hash, timestamp),
                    )

                    await conn.commit()
                    affected = cursor.rowcount

                    if affected > 0:
                        logger.debug(
                            f"记录已转发消息: {message_id} from {source_channel} to {target_channel}"
                        )
                        return True
                    return False

        except Exception as e:
            logger.error(f"添加转发消息记录失败: {type(e).__name__}: {e}", exc_info=True)
            return False

    async def get_forwarding_stats(self, channel_id: str = None) -> dict[str, Any]:
        """获取转发统计信息"""
        try:
            async with self.pool.acquire() as conn:
                async with conn.cursor(aiomysql.DictCursor) as cursor:
                    if channel_id:
                        # 单个频道统计
                        await cursor.execute(
                            """
                            SELECT total_forwarded, last_forwarded
                            FROM forwarding_stats
                            WHERE channel_id = %s
                        """,
                            (channel_id,),
                        )
                        row = await cursor.fetchone()

                        if row:
                            return {
                                "channel_id": channel_id,
                                "total_forwarded": row["total_forwarded"] or 0,
                                "last_forwarded": row["last_forwarded"],
                            }
                        else:
                            return {
                                "channel_id": channel_id,
                                "total_forwarded": 0,
                                "last_forwarded": None,
                            }
                    else:
                        # 所有频道统计
                        await cursor.execute("""
                            SELECT channel_id, total_forwarded, last_forwarded
                            FROM forwarding_stats
                            ORDER BY total_forwarded DESC
                        """)
                        rows = await cursor.fetchall()

                        stats_list = []
                        total_all = 0
                        for row in rows:
                            stats_list.append(
                                {
                                    "channel_id": row["channel_id"],
                                    "total_forwarded": row["total_forwarded"] or 0,
                                    "last_forwarded": row["last_forwarded"],
                                }
                            )
                            total_all += row["total_forwarded"] or 0

                        return {
                            "total_all_channels": total_all,
                            "by_channel": stats_list,
                        }

        except Exception as e:
            logger.error(f"获取转发统计失败: {type(e).__name__}: {e}", exc_info=True)
            return {}

    async def update_forwarding_stats(self, channel_id: str, increment: int = 1) -> bool:
        """更新频道转发统计"""
        try:
            async with self.pool.acquire() as conn:
                async with conn.cursor() as cursor:
                    now = int(datetime.now(UTC).timestamp())

                    await cursor.execute(
                        """
                        INSERT INTO forwarding_stats (channel_id, total_forwarded, last_forwarded)
                        VALUES (%s, %s, %s)
                        ON DUPLICATE KEY UPDATE
                            total_forwarded = total_forwarded + %s,
                            last_forwarded = %s,
                            updated_at = NOW()
                    """,
                        (channel_id, increment, now, increment, now),
                    )

                    await conn.commit()

                    logger.debug(f"更新转发统计: {channel_id} +{increment}")
                    return True

        except Exception as e:
            logger.error(f"更新转发统计失败: {type(e).__name__}: {e}", exc_info=True)
            return False

    async def cleanup_old_forwarded_messages(self, days: int = 30) -> int:
        """清理旧的转发消息记录"""
        try:
            async with self.pool.acquire() as conn:
                async with conn.cursor() as cursor:
                    cutoff_timestamp = int((datetime.now(UTC) - timedelta(days=days)).timestamp())

                    await cursor.execute(
                        """
                        DELETE FROM forwarded_messages
                        WHERE timestamp < %s
                    """,
                        (cutoff_timestamp,),
                    )

                    deleted_count = cursor.rowcount
                    await conn.commit()

                    logger.info(f"清理旧转发消息记录: 删除 {deleted_count} 条")
                    return deleted_count

        except Exception as e:
            logger.error(f"清理旧转发消息记录失败: {type(e).__name__}: {e}", exc_info=True)
            return 0

    # ============ 投票重新生成管理方法 ============

    async def add_poll_regeneration(
        self,
        channel: str,
        summary_msg_id: int,
        poll_msg_id: int,
        button_msg_id: int,
        summary_text: str,
        channel_name: str,
        send_to_channel: bool,
        discussion_forward_msg_id: int | None = None,
    ) -> bool:
        """添加投票重新生成记录"""
        try:
            async with self.pool.acquire() as conn:
                async with conn.cursor() as cursor:
                    await cursor.execute(
                        """
                        INSERT INTO poll_regenerations
                        (channel, summary_msg_id, poll_msg_id, button_msg_id,
                         summary_text, channel_name, send_to_channel, discussion_forward_msg_id, vote_count)
                        VALUES (%s, %s, %s, %s, %s, %s, %s, %s, 0)
                        ON DUPLICATE KEY UPDATE
                            poll_msg_id = VALUES(poll_msg_id),
                            button_msg_id = VALUES(button_msg_id),
                            summary_text = VALUES(summary_text),
                            channel_name = VALUES(channel_name),
                            send_to_channel = VALUES(send_to_channel),
                            discussion_forward_msg_id = VALUES(discussion_forward_msg_id)
                    """,
                        (
                            channel,
                            summary_msg_id,
                            poll_msg_id,
                            button_msg_id,
                            summary_text,
                            channel_name,
                            send_to_channel,
                            discussion_forward_msg_id,
                        ),
                    )
                    await conn.commit()
                    logger.info(
                        f"已添加投票重新生成记录: channel={channel}, summary_id={summary_msg_id}"
                    )
                    return True

        except Exception as e:
            logger.error(f"添加投票重新生成记录失败: {type(e).__name__}: {e}", exc_info=True)
            return False

    async def get_poll_regeneration(
        self, channel: str, summary_msg_id: int
    ) -> dict[str, Any] | None:
        """获取投票重新生成记录"""
        try:
            async with self.pool.acquire() as conn:
                async with conn.cursor(aiomysql.DictCursor) as cursor:
                    await cursor.execute(
                        """
                        SELECT * FROM poll_regenerations
                        WHERE channel = %s AND summary_msg_id = %s
                    """,
                        (channel, summary_msg_id),
                    )
                    row = await cursor.fetchone()

                    if row:
                        # 获取投票者列表
                        await cursor.execute(
                            """
                            SELECT user_id FROM poll_voters
                            WHERE channel = %s AND summary_msg_id = %s
                        """,
                            (channel, summary_msg_id),
                        )
                        voter_rows = await cursor.fetchall()
                        row["voters"] = [v["user_id"] for v in voter_rows]
                        return row
                    return None

        except Exception as e:
            logger.error(f"获取投票重新生成记录失败: {type(e).__name__}: {e}", exc_info=True)
            return None

    async def update_poll_message_ids(
        self, channel: str, summary_msg_id: int, poll_msg_id: int, button_msg_id: int
    ) -> bool:
        """更新投票消息ID"""
        try:
            async with self.pool.acquire() as conn:
                async with conn.cursor() as cursor:
                    await cursor.execute(
                        """
                        UPDATE poll_regenerations
                        SET poll_msg_id = %s, button_msg_id = %s
                        WHERE channel = %s AND summary_msg_id = %s
                    """,
                        (poll_msg_id, button_msg_id, channel, summary_msg_id),
                    )
                    await conn.commit()
                    logger.info(f"已更新投票消息ID: channel={channel}, summary_id={summary_msg_id}")
                    return True

        except Exception as e:
            logger.error(f"更新投票消息ID失败: {type(e).__name__}: {e}", exc_info=True)
            return False

    async def delete_poll_regeneration(self, channel: str, summary_msg_id: int) -> bool:
        """删除投票重新生成记录"""
        try:
            async with self.pool.acquire() as conn:
                async with conn.cursor() as cursor:
                    await cursor.execute(
                        """
                        DELETE FROM poll_regenerations
                        WHERE channel = %s AND summary_msg_id = %s
                    """,
                        (channel, summary_msg_id),
                    )
                    await conn.commit()
                    logger.info(
                        f"已删除投票重新生成记录: channel={channel}, summary_id={summary_msg_id}"
                    )
                    return True

        except Exception as e:
            logger.error(f"删除投票重新生成记录失败: {type(e).__name__}: {e}", exc_info=True)
            return False

    async def increment_vote_count(
        self, channel: str, summary_msg_id: int, user_id: int
    ) -> tuple[bool, int, bool]:
        """增加投票计数"""
        try:
            async with self.pool.acquire() as conn:
                async with conn.cursor() as cursor:
                    # 检查记录是否存在
                    await cursor.execute(
                        """
                        SELECT vote_count FROM poll_regenerations
                        WHERE channel = %s AND summary_msg_id = %s
                    """,
                        (channel, summary_msg_id),
                    )
                    row = await cursor.fetchone()
                    if not row:
                        logger.warning(
                            f"投票重新生成记录不存在: channel={channel}, summary_id={summary_msg_id}"
                        )
                        return False, 0, False

                    current_count = row[0]

                    # 检查用户是否已投票
                    await cursor.execute(
                        """
                        SELECT 1 FROM poll_voters
                        WHERE channel = %s AND summary_msg_id = %s AND user_id = %s
                    """,
                        (channel, summary_msg_id, user_id),
                    )
                    if await cursor.fetchone():
                        logger.info(f"用户 {user_id} 已经投票过了")
                        return False, current_count, True

                    # 增加投票计数
                    await cursor.execute(
                        """
                        UPDATE poll_regenerations
                        SET vote_count = vote_count + 1
                        WHERE channel = %s AND summary_msg_id = %s
                    """,
                        (channel, summary_msg_id),
                    )

                    # 记录投票者
                    await cursor.execute(
                        """
                        INSERT INTO poll_voters (channel, summary_msg_id, user_id)
                        VALUES (%s, %s, %s)
                    """,
                        (channel, summary_msg_id, user_id),
                    )

                    await conn.commit()

                    # 获取更新后的计数
                    await cursor.execute(
                        """
                        SELECT vote_count FROM poll_regenerations
                        WHERE channel = %s AND summary_msg_id = %s
                    """,
                        (channel, summary_msg_id),
                    )
                    new_count = (await cursor.fetchone())[0]

                    logger.info(
                        f"投票计数已更新: channel={channel}, summary_id={summary_msg_id}, "
                        f"count={new_count}, user_id={user_id}"
                    )
                    return True, new_count, False

        except Exception as e:
            logger.error(f"增加投票计数失败: {type(e).__name__}: {e}", exc_info=True)
            return False, 0, False

    async def reset_vote_count(self, channel: str, summary_msg_id: int) -> bool:
        """重置投票计数"""
        try:
            async with self.pool.acquire() as conn:
                async with conn.cursor() as cursor:
                    # 检查记录是否存在
                    await cursor.execute(
                        """
                        SELECT 1 FROM poll_regenerations
                        WHERE channel = %s AND summary_msg_id = %s
                    """,
                        (channel, summary_msg_id),
                    )
                    if not await cursor.fetchone():
                        logger.warning(
                            f"投票重新生成记录不存在: channel={channel}, summary_id={summary_msg_id}"
                        )
                        return False

                    # 重置计数
                    await cursor.execute(
                        """
                        UPDATE poll_regenerations
                        SET vote_count = 0
                        WHERE channel = %s AND summary_msg_id = %s
                    """,
                        (channel, summary_msg_id),
                    )

                    # 删除投票者记录
                    await cursor.execute(
                        """
                        DELETE FROM poll_voters
                        WHERE channel = %s AND summary_msg_id = %s
                    """,
                        (channel, summary_msg_id),
                    )

                    await conn.commit()
                    logger.info(f"投票计数已重置: channel={channel}, summary_id={summary_msg_id}")
                    return True

        except Exception as e:
            logger.error(f"重置投票计数失败: {type(e).__name__}: {e}", exc_info=True)
            return False

    async def get_vote_count(self, channel: str, summary_msg_id: int) -> int:
        """获取投票计数"""
        try:
            async with self.pool.acquire() as conn:
                async with conn.cursor() as cursor:
                    await cursor.execute(
                        """
                        SELECT vote_count FROM poll_regenerations
                        WHERE channel = %s AND summary_msg_id = %s
                    """,
                        (channel, summary_msg_id),
                    )
                    row = await cursor.fetchone()
                    return row[0] if row else 0

        except Exception as e:
            logger.error(f"获取投票计数失败: {type(e).__name__}: {e}", exc_info=True)
            return 0

    async def cleanup_old_poll_regenerations(self, days: int = 30) -> int:
        """清理旧的投票重新生成记录"""
        try:
            async with self.pool.acquire() as conn:
                async with conn.cursor() as cursor:
                    cutoff_time = datetime.now(UTC) - timedelta(days=days)

                    await cursor.execute(
                        """
                        DELETE FROM poll_regenerations
                        WHERE timestamp < %s
                    """,
                        (cutoff_time,),
                    )

                    deleted_count = cursor.rowcount
                    await conn.commit()

                    if deleted_count > 0:
                        logger.info(f"已清理 {deleted_count} 条超过 {days} 天的投票重新生成记录")
                    return deleted_count

        except Exception as e:
            logger.error(f"清理旧投票重新生成记录失败: {type(e).__name__}: {e}", exc_info=True)
            return 0

    # ============ WebUI 系统运维审计 ============

    async def add_system_audit_log(
        self,
        action: str,
        actor: str,
        target: str = "",
        params_summary: str = "{}",
        success: bool = True,
        message: str = "",
        duration_ms: int = 0,
    ) -> bool:
        """添加 WebUI 系统运维审计记录"""
        try:
            async with self.pool.acquire() as conn:
                async with conn.cursor() as cursor:
                    await cursor.execute(
                        """
                        INSERT INTO system_audit_logs
                        (action, actor, target, params_summary, success, message, duration_ms)
                        VALUES (%s, %s, %s, %s, %s, %s, %s)
                    """,
                        (
                            action,
                            actor,
                            target,
                            params_summary,
                            success,
                            message,
                            duration_ms,
                        ),
                    )
                    await conn.commit()
                    return True
        except Exception as e:
            logger.error(f"添加系统审计记录失败: {type(e).__name__}: {e}", exc_info=True)
            return False

    async def get_system_audit_logs(self, limit: int = 50) -> list[dict[str, Any]]:
        """获取最近 WebUI 系统运维审计记录"""
        try:
            async with self.pool.acquire() as conn:
                async with conn.cursor(aiomysql.DictCursor) as cursor:
                    await cursor.execute(
                        """
                        SELECT id, action, actor, target, params_summary, success,
                               message, duration_ms, created_at
                        FROM system_audit_logs
                        ORDER BY created_at DESC, id DESC
                        LIMIT %s
                    """,
                        (limit,),
                    )
                    return list(await cursor.fetchall())
        except Exception as e:
            logger.error(f"获取系统审计记录失败: {type(e).__name__}: {e}", exc_info=True)
            return []

    async def cleanup_old_system_audit_logs(self, days: int = 90) -> int:
        """清理旧 WebUI 系统运维审计记录"""
        try:
            async with self.pool.acquire() as conn:
                async with conn.cursor() as cursor:
                    cutoff_time = datetime.now(UTC) - timedelta(days=days)
                    await cursor.execute(
                        """
                        DELETE FROM system_audit_logs
                        WHERE created_at < %s
                    """,
                        (cutoff_time,),
                    )
                    deleted_count = cursor.rowcount
                    await conn.commit()
                    if deleted_count > 0:
                        logger.info(f"已清理 {deleted_count} 条超过 {days} 天的系统审计记录")
                    return deleted_count
        except Exception as e:
            logger.error(f"清理系统审计记录失败: {type(e).__name__}: {e}", exc_info=True)
            return 0

    # ============ 数据库管理方法（WebUI） ============

    # 允许 WebUI 管理的表白名单
    _ALLOWED_TABLES: set[str] = {
        "summaries",
        "db_version",
        "usage_quota",
        "channel_profiles",
        "conversation_history",
        "users",
        "subscriptions",
        "request_queue",
        "notification_queue",
        "forwarded_messages",
        "forwarding_stats",
        "poll_regenerations",
        "poll_voters",
        "system_audit_logs",
        "submissions",
    }

    def table_exists(self, table: str) -> bool:
        """检查表是否在白名单中"""
        return table.lower() in self._ALLOWED_TABLES

    async def list_tables(self) -> list[dict[str, Any]]:
        """列出所有白名单表及基本信息"""
        try:
            async with self.pool.acquire() as conn:
                async with conn.cursor(aiomysql.DictCursor) as cursor:
                    placeholders = ", ".join(["%s"] * len(self._ALLOWED_TABLES))
                    await cursor.execute(
                        f"""
                        SELECT
                            TABLE_NAME as table_name,
                            TABLE_ROWS as table_rows,
                            ROUND(DATA_LENGTH / 1024 / 1024, 3) as data_size_mb,
                            ENGINE as engine
                        FROM INFORMATION_SCHEMA.TABLES
                        WHERE TABLE_SCHEMA = DATABASE()
                          AND TABLE_NAME IN ({placeholders})
                        ORDER BY TABLE_NAME
                    """,
                        list(self._ALLOWED_TABLES),
                    )
                    rows = await cursor.fetchall()
                    if not rows:
                        return []

                    # InnoDB 的 INFORMATION_SCHEMA.TABLES.TABLE_ROWS 是估算值；
                    # 使用 UNION ALL 批量获取精确 COUNT，避免逐表 N+1 查询。
                    count_sql = " UNION ALL ".join(
                        f"SELECT %s as table_name, COUNT(*) as cnt FROM `{row['table_name']}`"
                        for row in rows
                    )
                    await cursor.execute(count_sql, [row["table_name"] for row in rows])
                    counts = {
                        row["table_name"]: int(row["cnt"] or 0) for row in await cursor.fetchall()
                    }

                    for row in rows:
                        row["table_rows"] = counts.get(row["table_name"], 0)
                        row["data_size_mb"] = float(row.get("data_size_mb") or 0)
                    return list(rows)
        except Exception as e:
            logger.error(f"列出数据库表失败: {type(e).__name__}: {e}", exc_info=True)
            return []

    async def describe_table(self, table: str) -> list[dict[str, Any]]:
        """获取表的列信息"""
        try:
            async with self.pool.acquire() as conn:
                async with conn.cursor(aiomysql.DictCursor) as cursor:
                    await cursor.execute(
                        """
                        SELECT
                            COLUMN_NAME as `field`,
                            COLUMN_TYPE as `type`,
                            IS_NULLABLE as `null`,
                            COLUMN_KEY as `key`,
                            COLUMN_DEFAULT as `default`,
                            EXTRA as `extra`
                        FROM INFORMATION_SCHEMA.COLUMNS
                        WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = %s
                        ORDER BY ORDINAL_POSITION
                    """,
                        (table,),
                    )
                    return list(await cursor.fetchall())
        except Exception as e:
            logger.error(f"获取表结构失败 {table}: {type(e).__name__}: {e}", exc_info=True)
            return []

    async def get_primary_key(self, table: str) -> str | None:
        """获取表的主键列名"""
        try:
            async with self.pool.acquire() as conn:
                async with conn.cursor(aiomysql.DictCursor) as cursor:
                    await cursor.execute(
                        """
                        SELECT COLUMN_NAME
                        FROM INFORMATION_SCHEMA.KEY_COLUMN_USAGE
                        WHERE TABLE_SCHEMA = DATABASE()
                          AND TABLE_NAME = %s
                          AND CONSTRAINT_NAME = 'PRIMARY'
                        LIMIT 1
                    """,
                        (table,),
                    )
                    row = await cursor.fetchone()
                    return row["COLUMN_NAME"] if row else None
        except Exception as e:
            logger.error(f"获取主键失败 {table}: {type(e).__name__}: {e}", exc_info=True)
            return None

    async def query_table(
        self,
        table: str,
        page: int = 1,
        page_size: int = 20,
        search_column: str | None = None,
        search_keyword: str | None = None,
        order_by: str | None = None,
        order_dir: str = "ASC",
    ) -> dict[str, Any]:
        """通用分页查询表数据"""
        try:
            async with self.pool.acquire() as conn:
                async with conn.cursor(aiomysql.DictCursor) as cursor:
                    cols = await self.describe_table(table)
                    col_names = {c["field"] for c in cols}
                    where_clause = ""
                    params: list[Any] = []

                    if search_column and search_keyword:
                        # 安全校验列名（防止 SQL 注入）
                        if search_column not in col_names:
                            return {
                                "rows": [],
                                "total": 0,
                                "page": page,
                                "page_size": page_size,
                            }
                        where_clause = f"WHERE `{search_column}` LIKE %s"
                        params.append(f"%{search_keyword}%")

                    # 总数
                    await cursor.execute(
                        f"SELECT COUNT(*) as cnt FROM `{table}` {where_clause}",
                        params,
                    )
                    total_row = await cursor.fetchone()
                    total = int(total_row["cnt"]) if total_row else 0

                    # 排序
                    order_clause = ""
                    if order_by:
                        # 安全校验排序列
                        if order_by in col_names:
                            direction = "DESC" if order_dir.upper() == "DESC" else "ASC"
                            order_clause = f"ORDER BY `{order_by}` {direction}"

                    offset = (page - 1) * page_size
                    await cursor.execute(
                        f"SELECT * FROM `{table}` {where_clause} {order_clause} LIMIT %s OFFSET %s",
                        params + [page_size, offset],
                    )
                    rows = await cursor.fetchall()

                    return {
                        "rows": list(rows),
                        "total": total,
                        "page": page,
                        "page_size": page_size,
                    }
        except Exception as e:
            logger.error(f"查询表数据失败 {table}: {type(e).__name__}: {e}", exc_info=True)
            return {"rows": [], "total": 0, "page": page, "page_size": page_size}

    async def _validate_row_columns(self, table: str, data: dict[str, Any]) -> None:
        """校验行写入数据的列名是否合法，防止列名注入。"""
        cols = await self.describe_table(table)
        col_names = {c["field"] for c in cols}
        invalid_columns = [key for key in data if key not in col_names]
        if invalid_columns:
            raise ValueError(f"非法列名: {', '.join(invalid_columns)}")

    async def get_row(self, table: str, pk_column: str, pk_value: Any) -> dict[str, Any] | None:
        """获取单行数据"""
        try:
            async with self.pool.acquire() as conn:
                async with conn.cursor(aiomysql.DictCursor) as cursor:
                    await cursor.execute(
                        f"SELECT * FROM `{table}` WHERE `{pk_column}` = %s LIMIT 1",
                        (pk_value,),
                    )
                    return await cursor.fetchone()
        except Exception as e:
            logger.error(f"获取行数据失败 {table}: {type(e).__name__}: {e}", exc_info=True)
            return None

    async def insert_row(self, table: str, data: dict[str, Any]) -> int | None:
        """插入一行数据"""
        try:
            await self._validate_row_columns(table, data)
            async with self.pool.acquire() as conn:
                async with conn.cursor() as cursor:
                    columns = ", ".join(f"`{k}`" for k in data.keys())
                    placeholders = ", ".join(["%s"] * len(data))
                    values = list(data.values())
                    await cursor.execute(
                        f"INSERT INTO `{table}` ({columns}) VALUES ({placeholders})",
                        values,
                    )
                    await conn.commit()
                    return int(cursor.lastrowid) if cursor.lastrowid else None
        except ValueError:
            raise
        except Exception as e:
            logger.error(f"插入行失败 {table}: {type(e).__name__}: {e}", exc_info=True)
            raise

    async def update_row(
        self, table: str, pk_column: str, pk_value: Any, data: dict[str, Any]
    ) -> bool:
        """更新一行数据"""
        try:
            await self._validate_row_columns(table, data)
            async with self.pool.acquire() as conn:
                async with conn.cursor() as cursor:
                    set_clause = ", ".join(f"`{k}` = %s" for k in data.keys())
                    values = list(data.values()) + [pk_value]
                    await cursor.execute(
                        f"UPDATE `{table}` SET {set_clause} WHERE `{pk_column}` = %s LIMIT 1",
                        values,
                    )
                    await conn.commit()
                    return cursor.rowcount == 1
        except ValueError:
            raise
        except Exception as e:
            logger.error(f"更新行失败 {table}: {type(e).__name__}: {e}", exc_info=True)
            raise

    async def delete_row(self, table: str, pk_column: str, pk_value: Any) -> bool:
        """删除一行数据"""
        try:
            async with self.pool.acquire() as conn:
                async with conn.cursor() as cursor:
                    await cursor.execute(
                        f"DELETE FROM `{table}` WHERE `{pk_column}` = %s LIMIT 1",
                        (pk_value,),
                    )
                    await conn.commit()
                    return cursor.rowcount == 1
        except Exception as e:
            logger.error(f"删除行失败 {table}: {type(e).__name__}: {e}", exc_info=True)
            raise
