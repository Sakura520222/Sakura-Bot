"""
数据库迁移脚本：转发消息表结构优化

修改内容：
1. 修改 forwarded_messages 表主键为三字段组合
2. 添加 forward_status, error_message, message_type 字段
3. 添加必要的索引

版本：v1.7.4
"""

import asyncio
import logging

logger = logging.getLogger(__name__)


async def migrate_forwarded_messages_table(db):
    """
    迁移 forwarded_messages 表结构

    Args:
        db: 数据库管理器实例（SQLiteManager 或 MySQLManager）

    Returns:
        dict: 迁移结果
    """
    result = {"success": False, "message": "", "details": {}}

    try:
        # 检查数据库类型
        db_type = db.__class__.__name__
        logger.info(f"开始迁移 forwarded_messages 表 (数据库类型: {db_type})")

        if db_type == "SQLiteManager":
            result = await _migrate_sqlite(db)
        elif db_type == "MySQLManager":
            result = await _migrate_mysql(db)
        else:
            result["message"] = f"不支持的数据库类型: {db_type}"

        return result

    except Exception as e:
        logger.error(f"迁移转发消息表失败: {type(e).__name__}: {e}", exc_info=True)
        result["message"] = f"迁移失败: {str(e)}"
        return result


async def _migrate_sqlite(db):
    """迁移 SQLite 数据库"""
    try:
        conn = db.get_connection()
        cursor = conn.cursor()

        # 检查表是否存在
        cursor.execute("""
            SELECT name FROM sqlite_master
            WHERE type='table' AND name='forwarded_messages'
        """)
        table_exists = cursor.fetchone() is not None

        if not table_exists:
            logger.info("forwarded_messages 表不存在，无需迁移")
            conn.close()
            return {"success": True, "message": "表不存在，无需迁移", "details": {"skipped": True}}

        # 检查是否已经有 source_channel 字段
        cursor.execute("PRAGMA table_info(forwarded_messages)")
        columns = {row[1] for row in cursor.fetchall()}

        if "source_channel" in columns and "forward_status" in columns:
            logger.info("表结构已是最新的，无需迁移")
            conn.close()
            return {
                "success": True,
                "message": "表结构已是最新的",
                "details": {"already_updated": True},
            }

        logger.info("开始迁移表结构...")

        # 1. 备份现有数据
        cursor.execute("""
            SELECT message_id, target_channel, content_hash, timestamp, created_at
            FROM forwarded_messages
        """)
        existing_data = cursor.fetchall()
        logger.info(f"备份了 {len(existing_data)} 条现有记录")

        # 2. 删除旧表
        cursor.execute("DROP TABLE IF EXISTS forwarded_messages")

        # 3. 创建新表（三字段主键 + 新字段）
        cursor.execute("""
            CREATE TABLE forwarded_messages (
                message_id TEXT NOT NULL,
                source_channel TEXT NOT NULL,
                target_channel TEXT NOT NULL,
                content_hash TEXT,
                timestamp INTEGER NOT NULL,
                forward_status TEXT DEFAULT 'success',
                error_message TEXT,
                message_type TEXT DEFAULT 'unknown',
                created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
                PRIMARY KEY (message_id, source_channel, target_channel)
            )
        """)

        # 4. 创建索引
        cursor.execute("""
            CREATE INDEX IF NOT EXISTS idx_forwarded_source
            ON forwarded_messages(source_channel)
        """)

        cursor.execute("""
            CREATE INDEX IF NOT EXISTS idx_forwarded_target
            ON forwarded_messages(target_channel)
        """)

        cursor.execute("""
            CREATE INDEX IF NOT EXISTS idx_forwarded_timestamp
            ON forwarded_messages(timestamp)
        """)

        # 5. 恢复数据（注意：旧数据没有 source_channel，设置为 'unknown'）
        recovered_count = 0
        for row in existing_data:
            try:
                cursor.execute(
                    """
                    INSERT INTO forwarded_messages
                    (message_id, source_channel, target_channel, content_hash, timestamp, forward_status)
                    VALUES (?, ?, ?, ?, ?, ?)
                """,
                    (row[0], "unknown", row[1], row[2], row[3], "success"),
                )
                recovered_count += 1
            except Exception as e:
                logger.warning(f"恢复记录失败: {e}")

        conn.commit()
        conn.close()

        logger.info(f"迁移完成: 恢复了 {recovered_count}/{len(existing_data)} 条记录")

        return {
            "success": True,
            "message": "SQLite 表迁移成功",
            "details": {
                "total_records": len(existing_data),
                "recovered_records": recovered_count,
                "lost_records": len(existing_data) - recovered_count,
            },
        }

    except Exception as e:
        logger.error(f"SQLite 迁移失败: {type(e).__name__}: {e}", exc_info=True)
        raise


async def _migrate_mysql(db):
    """迁移 MySQL 数据库"""
    try:
        async with db.pool.acquire() as conn:
            async with conn.cursor() as cursor:
                # 检查表是否存在
                await cursor.execute("""
                    SELECT table_name FROM information_schema.tables
                    WHERE table_schema = DATABASE() AND table_name = 'forwarded_messages'
                """)
                table_exists = await cursor.fetchone()

                if not table_exists:
                    logger.info("forwarded_messages 表不存在，无需迁移")
                    return {
                        "success": True,
                        "message": "表不存在，无需迁移",
                        "details": {"skipped": True},
                    }

                # 检查是否已经有新字段
                await cursor.execute("""
                    SELECT column_name FROM information_schema.columns
                    WHERE table_schema = DATABASE() AND table_name = 'forwarded_messages'
                """)
                columns = {row[0] for row in await cursor.fetchall()}

                if "source_channel" in columns and "forward_status" in columns:
                    logger.info("表结构已是最新的，无需迁移")
                    return {
                        "success": True,
                        "message": "表结构已是最新的",
                        "details": {"already_updated": True},
                    }

                logger.info("开始迁移表结构...")

                # 1. 备份现有数据
                await cursor.execute("""
                    SELECT message_id, target_channel, content_hash, timestamp, created_at
                    FROM forwarded_messages
                """)
                existing_data = await cursor.fetchall()
                logger.info(f"备份了 {len(existing_data)} 条现有记录")

                # 2. 删除旧表
                await cursor.execute("DROP TABLE IF EXISTS forwarded_messages")

                # 3. 创建新表（缩短字段长度，避免索引过长）
                await cursor.execute("""
                    CREATE TABLE forwarded_messages (
                        id BIGINT AUTO_INCREMENT PRIMARY KEY,
                        message_id VARCHAR(255) NOT NULL,
                        source_channel VARCHAR(255) NOT NULL,
                        target_channel VARCHAR(255) NOT NULL,
                        content_hash VARCHAR(64),
                        timestamp BIGINT NOT NULL,
                        forward_status VARCHAR(20) DEFAULT 'success',
                        error_message TEXT,
                        message_type VARCHAR(50) DEFAULT 'unknown',
                        created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
                        UNIQUE KEY unique_message (message_id, source_channel(191), target_channel(191)),
                        INDEX idx_source (source_channel(191)),
                        INDEX idx_target (target_channel(191)),
                        INDEX idx_timestamp (timestamp)
                    ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
                """)

                # 4. 恢复数据
                recovered_count = 0
                for row in existing_data:
                    try:
                        await cursor.execute(
                            """
                            INSERT INTO forwarded_messages
                            (message_id, source_channel, target_channel, content_hash, timestamp, forward_status)
                            VALUES (%s, %s, %s, %s, %s, %s)
                        """,
                            (row[0], "unknown", row[1], row[2], row[3], "success"),
                        )
                        recovered_count += 1
                    except Exception as e:
                        logger.warning(f"恢复记录失败: {e}")

                await conn.commit()

        logger.info(f"迁移完成: 恢复了 {recovered_count}/{len(existing_data)} 条记录")

        return {
            "success": True,
            "message": "MySQL 表迁移成功",
            "details": {
                "total_records": len(existing_data),
                "recovered_records": recovered_count,
                "lost_records": len(existing_data) - recovered_count,
            },
        }

    except Exception as e:
        logger.error(f"MySQL 迁移失败: {type(e).__name__}: {e}", exc_info=True)
        raise


# 便捷函数：在应用启动时自动运行迁移
async def ensure_forwarding_table_updated(db):
    """
    确保 forwarded_messages 表结构已更新

    在应用启动时调用，自动检查并执行迁移

    Args:
        db: 数据库管理器实例
    """
    try:
        result = await migrate_forwarded_messages_table(db)
        if result["success"]:
            logger.info(f"✅ 转发消息表迁移完成: {result['message']}")
        else:
            logger.error(f"❌ 转发消息表迁移失败: {result['message']}")
    except Exception as e:
        logger.error(f"❌ 转发消息表迁移异常: {type(e).__name__}: {e}")


if __name__ == "__main__":
    # 测试迁移脚本
    import sys

    sys.path.append("..")

    from database import get_db_manager

    async def test():
        db = get_db_manager()
        result = await migrate_forwarded_messages_table(db)
        print(f"迁移结果: {result}")

    asyncio.run(test())
