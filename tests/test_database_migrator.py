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
测试数据库迁移器模块

测试 DatabaseMigrator 类的功能，特别是异步文件存在性检查
"""

import asyncio
from unittest.mock import AsyncMock, MagicMock, patch

import pytest

from core.database_migrator import DatabaseMigrator


@pytest.mark.unit
class TestDatabaseMigrator:
    """DatabaseMigrator 测试"""

    def test_init_with_defaults(self):
        """测试使用默认参数初始化"""
        migrator = DatabaseMigrator()
        assert migrator.sqlite_path == "data/summaries.db"
        assert migrator.mysql_config == {}
        assert migrator.sqlite_db is None
        assert migrator.mysql_db is None
        assert migrator.migration_status["status"] == "idle"

    def test_init_with_custom_params(self):
        """测试使用自定义参数初始化"""
        custom_path = "custom/path/test.db"
        mysql_config = {"host": "localhost", "port": 3306}
        migrator = DatabaseMigrator(sqlite_path=custom_path, mysql_config=mysql_config)
        assert migrator.sqlite_path == custom_path
        assert migrator.mysql_config == mysql_config

    @pytest.mark.asyncio
    async def test_check_migration_ready_sqlite_not_exists(self, temp_dir):
        """测试 SQLite 文件不存在的情况"""
        # 使用临时目录中不存在的文件
        non_existent_path = str(temp_dir / "non_existent.db")
        migrator = DatabaseMigrator(sqlite_path=non_existent_path)

        result = await migrator.check_migration_ready()

        assert result["ready"] is False
        assert result["sqlite_exists"] is False
        assert result["mysql_configured"] is False
        assert result["mysql_connectable"] is False
        assert result["sqlite_tables"] == {}

    @pytest.mark.asyncio
    async def test_check_migration_ready_sqlite_exists(self, temp_dir):
        """测试 SQLite 文件存在但 MySQL 未配置的情况"""
        # 创建一个空的 SQLite 文件
        db_path = temp_dir / "test.db"
        db_path.touch()  # 创建空文件

        migrator = DatabaseMigrator(sqlite_path=str(db_path))

        # Mock SQLiteManager 以避免实际的数据库操作
        with patch("core.database_migrator.SQLiteManager") as mock_sqlite:
            mock_instance = MagicMock()
            mock_instance.get_connection.return_value = MagicMock()
            mock_conn = MagicMock()
            mock_cursor = MagicMock()
            mock_cursor.fetchone.return_value = [0]  # 表中没有数据
            mock_conn.execute.return_value = mock_cursor
            mock_conn.close = MagicMock()
            mock_instance.get_connection.return_value = mock_conn
            mock_sqlite.return_value = mock_instance

            result = await migrator.check_migration_ready()

            assert result["ready"] is False
            assert result["sqlite_exists"] is True
            assert "MySQL配置不完整" in result["message"] or "mysql_incomplete" in result["message"]

    @pytest.mark.asyncio
    async def test_check_migration_ready_mysql_configured(self, temp_dir):
        """测试 MySQL 已配置但连接失败的情况"""
        # 创建 SQLite 文件
        db_path = temp_dir / "test.db"
        db_path.touch()

        mysql_config = {
            "host": "localhost",
            "port": 3306,
            "user": "test_user",
            "password": "test_pass",
            "database": "test_db",
        }
        migrator = DatabaseMigrator(sqlite_path=str(db_path), mysql_config=mysql_config)

        # Mock SQLiteManager
        with patch("core.database_migrator.SQLiteManager") as mock_sqlite:
            mock_instance = MagicMock()
            mock_conn = MagicMock()
            mock_cursor = MagicMock()
            mock_cursor.fetchone.return_value = [0]
            mock_conn.execute.return_value = mock_cursor
            mock_conn.close = MagicMock()
            mock_instance.get_connection.return_value = mock_conn
            mock_sqlite.return_value = mock_instance

            # Mock MySQLManager 以抛出连接异常
            with patch("core.database_migrator.MySQLManager") as mock_mysql_class:
                mock_mysql = AsyncMock()
                mock_mysql.init_database.side_effect = Exception("Connection failed")
                mock_mysql_class.return_value = mock_mysql

                result = await migrator.check_migration_ready()

                assert result["ready"] is False
                assert result["sqlite_exists"] is True
                assert result["mysql_configured"] is True
                assert result["mysql_connectable"] is False

    @pytest.mark.asyncio
    async def test_check_migration_ready_success(self, temp_dir):
        """测试迁移准备检查成功的情况"""
        # 创建 SQLite 文件
        db_path = temp_dir / "test.db"
        db_path.touch()

        mysql_config = {
            "host": "localhost",
            "port": 3306,
            "user": "test_user",
            "password": "test_pass",
            "database": "test_db",
        }
        migrator = DatabaseMigrator(sqlite_path=str(db_path), mysql_config=mysql_config)

        # Mock SQLiteManager
        with patch("core.database_migrator.SQLiteManager") as mock_sqlite:
            mock_instance = MagicMock()
            mock_conn = MagicMock()
            mock_cursor = MagicMock()
            # 模拟每个表都有 10 条记录
            mock_cursor.fetchone.return_value = [10]
            mock_conn.execute.return_value = mock_cursor
            mock_conn.close = MagicMock()
            mock_instance.get_connection.return_value = mock_conn
            mock_sqlite.return_value = mock_instance

            # Mock MySQLManager
            with patch("core.database_migrator.MySQLManager") as mock_mysql_class:
                mock_mysql = AsyncMock()
                mock_mysql.get_statistics.return_value = {"total_count": 0}  # MySQL 中没有数据
                mock_mysql_class.return_value = mock_mysql

                result = await migrator.check_migration_ready()

                assert result["ready"] is True
                assert result["sqlite_exists"] is True
                assert result["mysql_configured"] is True
                assert result["mysql_connectable"] is True

    @pytest.mark.asyncio
    async def test_check_migration_ready_mysql_has_data(self, temp_dir):
        """测试 MySQL 中已有数据的情况"""
        # 创建 SQLite 文件
        db_path = temp_dir / "test.db"
        db_path.touch()

        mysql_config = {
            "host": "localhost",
            "port": 3306,
            "user": "test_user",
            "password": "test_pass",
            "database": "test_db",
        }
        migrator = DatabaseMigrator(sqlite_path=str(db_path), mysql_config=mysql_config)

        # Mock SQLiteManager
        with patch("core.database_migrator.SQLiteManager") as mock_sqlite:
            mock_instance = MagicMock()
            mock_conn = MagicMock()
            mock_cursor = MagicMock()
            mock_cursor.fetchone.return_value = [10]
            mock_conn.execute.return_value = mock_cursor
            mock_conn.close = MagicMock()
            mock_instance.get_connection.return_value = mock_conn
            mock_sqlite.return_value = mock_instance

            # Mock MySQLManager - MySQL 中已有数据
            with patch("core.database_migrator.MySQLManager") as mock_mysql_class:
                mock_mysql = AsyncMock()
                mock_mysql.get_statistics.return_value = {"total_count": 100}  # MySQL 有数据
                mock_mysql_class.return_value = mock_mysql

                result = await migrator.check_migration_ready()

                assert result["ready"] is False
                assert result["mysql_connectable"] is True
                # 检查消息中包含"已存在数据"或"覆盖"等关键词
                assert any(
                    keyword in result.get("message", "")
                    for keyword in ["已存在数据", "覆盖", "数据"]
                )

    @pytest.mark.asyncio
    async def test_check_migration_ready_exception_handling(self, temp_dir):
        """测试异常处理"""
        # 创建 SQLite 文件
        db_path = temp_dir / "test.db"
        db_path.touch()

        migrator = DatabaseMigrator(sqlite_path=str(db_path))

        # Mock os.path.exists 抛出异常
        with patch("asyncio.to_thread") as mock_to_thread:
            mock_to_thread.side_effect = Exception("File system error")

            result = await migrator.check_migration_ready()

            assert result["ready"] is False
            assert "error" in result.get("message", "").lower() or "general_error" in result.get(
                "message", ""
            )

    def test_get_migration_status(self):
        """测试获取迁移状态"""
        migrator = DatabaseMigrator()
        status = migrator.get_migration_status()

        assert status["status"] == "idle"
        assert status["progress"] == 0
        assert status["message"] == ""
        assert status["table_stats"] == {}

    @pytest.mark.asyncio
    async def test_migrate_data_not_initialized(self):
        """测试未初始化就执行迁移"""
        migrator = DatabaseMigrator()
        result = await migrator.migrate_data()

        assert result["success"] is False
        assert "not_initialized" in result["message"] or "未初始化" in result["message"]

    @pytest.mark.asyncio
    async def test_cleanup(self):
        """测试清理资源"""
        migrator = DatabaseMigrator()

        # Mock 数据库连接
        migrator.sqlite_db = AsyncMock()
        migrator.mysql_db = AsyncMock()

        await migrator.cleanup()

        migrator.sqlite_db.close.assert_called_once()
        migrator.mysql_db.close.assert_called_once()

    @pytest.mark.asyncio
    async def test_backup_sqlite_success(self, temp_dir):
        """测试 SQLite 备份成功"""
        # 创建测试数据库文件
        db_path = temp_dir / "test.db"
        db_path.write_text("test data")

        migrator = DatabaseMigrator(sqlite_path=str(db_path))

        backup_path = await migrator._backup_sqlite()

        assert backup_path is not None
        # 使用 asyncio.to_thread 包装 Path.exists() 调用
        from pathlib import Path

        assert await asyncio.to_thread(Path(backup_path).exists)
        assert ".backup_" in backup_path

    @pytest.mark.asyncio
    async def test_backup_sqlite_failure(self, temp_dir):
        """测试 SQLite 备份失败"""
        # 使用不存在的文件路径
        non_existent = temp_dir / "non_existent.db"
        migrator = DatabaseMigrator(sqlite_path=str(non_existent))

        backup_path = await migrator._backup_sqlite()

        assert backup_path is None

    def test_get_sqlite_data(self, temp_dir):
        """测试从 SQLite 读取数据"""
        # 创建一个真实的 SQLite 数据库并插入测试数据
        import sqlite3

        db_path = temp_dir / "test.db"
        conn = sqlite3.connect(str(db_path))
        conn.execute("CREATE TABLE test_table (id INTEGER, name TEXT)")
        conn.execute("INSERT INTO test_table VALUES (1, 'test')")
        conn.commit()
        conn.close()

        migrator = DatabaseMigrator(sqlite_path=str(db_path))

        rows = migrator._get_sqlite_data("SELECT * FROM test_table")

        assert len(rows) == 1
        assert rows[0][0] == 1
        assert rows[0][1] == "test"

    def test_get_sqlite_data_exception(self, temp_dir):
        """测试 SQLite 查询失败的情况"""
        # 使用无效的 SQL 查询
        db_path = temp_dir / "test.db"
        db_path.touch()

        migrator = DatabaseMigrator(sqlite_path=str(db_path))

        rows = migrator._get_sqlite_data("SELECT * FROM non_existent_table")

        assert rows == []

    @pytest.mark.asyncio
    async def test_migration_status_update(self, temp_dir):
        """测试迁移状态更新"""
        db_path = temp_dir / "test.db"
        db_path.touch()

        mysql_config = {
            "host": "localhost",
            "port": 3306,
            "user": "test_user",
            "password": "test_pass",
            "database": "test_db",
        }
        migrator = DatabaseMigrator(sqlite_path=str(db_path), mysql_config=mysql_config)

        # Mock 数据库
        migrator.sqlite_db = MagicMock()
        migrator.mysql_db = AsyncMock()

        # Mock 备份
        with patch.object(migrator, "_backup_sqlite", return_value=str(db_path) + ".backup"):
            # Mock 所有迁移方法
            with patch.object(
                migrator, "_migrate_users", return_value={"migrated": 1, "failed": 0}
            ):
                with patch.object(
                    migrator, "_migrate_subscriptions", return_value={"migrated": 1, "failed": 0}
                ):
                    with patch.object(
                        migrator, "_migrate_usage_quota", return_value={"migrated": 1, "failed": 0}
                    ):
                        with patch.object(
                            migrator,
                            "_migrate_summaries",
                            return_value={"migrated": 1, "failed": 0},
                        ):
                            with patch.object(
                                migrator,
                                "_migrate_channel_profiles",
                                return_value={"migrated": 1, "failed": 0},
                            ):
                                with patch.object(
                                    migrator,
                                    "_migrate_conversation_history",
                                    return_value={"migrated": 1, "failed": 0},
                                ):
                                    with patch.object(
                                        migrator,
                                        "_migrate_request_queue",
                                        return_value={"migrated": 1, "failed": 0},
                                    ):
                                        with patch.object(
                                            migrator,
                                            "_migrate_notification_queue",
                                            return_value={"migrated": 1, "failed": 0},
                                        ):
                                            with patch.object(
                                                migrator, "_verify_migration", return_value={}
                                            ):
                                                result = await migrator.migrate_data()

                                                assert result["success"] is True
                                                assert (
                                                    migrator.migration_status["status"]
                                                    == "completed"
                                                )

    @pytest.mark.asyncio
    async def test_verify_migration(self, temp_dir):
        """测试迁移验证"""
        import sqlite3

        # 创建 SQLite 数据库，只创建需要的表
        db_path = temp_dir / "test.db"
        conn = sqlite3.connect(str(db_path))
        # 创建所有需要的表
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
        for table in tables:
            conn.execute(f"CREATE TABLE {table} (id INTEGER PRIMARY KEY)")
            conn.execute(f"INSERT INTO {table} VALUES (1)")
            conn.execute(f"INSERT INTO {table} VALUES (2)")
        conn.commit()
        conn.close()

        migrator = DatabaseMigrator(sqlite_path=str(db_path))

        # Mock MySQL 数据库
        migrator.mysql_db = AsyncMock()

        # 创建一个正确的 mock 结构 - 直接返回异步上下文管理器
        class MockConnCtx:
            def __init__(self):
                self.mock_cursor = AsyncMock()
                self.mock_cursor.execute = AsyncMock()
                self.mock_cursor.fetchone = AsyncMock(return_value=[2])

                class MockCursorCtx:
                    async def __aenter__(self):
                        return self.mock_cursor

                    async def __aexit__(self, *args):
                        pass

                self.cursor = MagicMock(return_value=self.MockCursorCtx())

            async def __aenter__(self):
                return self

            async def __aexit__(self, *args):
                pass

        mock_pool = MagicMock()
        mock_pool.acquire = MagicMock(return_value=MockConnCtx())
        migrator.mysql_db.pool = mock_pool

        verification = await migrator._verify_migration()

        assert "sqlite_stats" in verification
        assert "mysql_stats" in verification
        assert verification["matched"] is True


if __name__ == "__main__":
    pytest.main([__file__, "-v", "--cov=core.database_migrator", "--cov-report=term-missing"])
