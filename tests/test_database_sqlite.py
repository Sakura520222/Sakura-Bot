"""测试 SQLite Database 模块

Copyright 2026 Sakura-Bot
本项目采用 AGPL-3.0 许可
"""

import os
import sqlite3

import pytest

from core.database_sqlite import SQLiteManager


@pytest.mark.unit
class TestSQLiteManager:
    """SQLite Manager 测试"""

    def test_init_with_default_path(self):
        """测试使用默认路径初始化"""
        manager = SQLiteManager()
        assert manager.db_path == os.path.join("data", "summaries.db")
        assert manager._db_type == "sqlite"
        assert manager._db_version == 4

    def test_init_with_custom_path(self, temp_dir):
        """测试使用自定义路径初始化"""
        custom_path = temp_dir / "custom.db"
        manager = SQLiteManager(str(custom_path))
        assert manager.db_path == str(custom_path)
        assert manager._db_type == "sqlite"
        assert manager._db_version == 4

    def test_get_database_type(self):
        """测试获取数据库类型"""
        manager = SQLiteManager()
        assert manager.get_database_type() == "sqlite"

    def test_get_database_version(self):
        """测试获取数据库版本"""
        manager = SQLiteManager()
        assert manager.get_database_version() == 4

    def test_get_connection(self, temp_dir):
        """测试获取数据库连接"""
        db_path = temp_dir / "test.db"
        manager = SQLiteManager(str(db_path))

        conn = manager.get_connection()
        assert isinstance(conn, sqlite3.Connection)

        # 验证连接是可用的
        cursor = conn.cursor()
        cursor.execute("SELECT 1")
        result = cursor.fetchone()
        assert result[0] == 1

        conn.close()

    def test_database_file_creation(self, temp_dir):
        """测试数据库文件创建"""
        db_path = temp_dir / "test.db"
        assert not db_path.exists()

        manager = SQLiteManager(str(db_path))
        # 创建连接时会创建数据库文件
        conn = manager.get_connection()
        conn.close()

        assert db_path.exists()


if __name__ == "__main__":
    pytest.main([__file__, "-v"])
