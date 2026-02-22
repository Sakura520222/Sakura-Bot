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
SQLite数据库管理器模块

继承原有的DatabaseManagerLegacy，保持所有功能的兼容性
"""

import os

from .database import DatabaseManagerLegacy


class SQLiteManager(DatabaseManagerLegacy):
    """
    SQLite数据库管理器

    继承原有的DatabaseManagerLegacy类，保持所有功能的完全兼容
    所有方法实现都在DatabaseManagerLegacy中，这里只是提供SQLite专用包装
    """

    def __init__(self, db_path=None):
        """
        初始化SQLite数据库管理器

        Args:
            db_path: 数据库文件路径，默认为 data/summaries.db
        """
        if db_path is None:
            db_path = os.path.join("data", "summaries.db")

        # 调用父类构造函数
        super().__init__(db_path)

        # 标记数据库类型
        self._db_type = "sqlite"
        self._db_version = 4

    def get_database_type(self) -> str:
        """获取数据库类型"""
        return "sqlite"

    def get_database_version(self) -> int:
        """获取数据库版本号"""
        return 4

    def get_connection(self):
        """
        获取数据库连接（供迁移器使用）

        Returns:
            sqlite3.Connection
        """
        import sqlite3

        return sqlite3.connect(self.db_path)
