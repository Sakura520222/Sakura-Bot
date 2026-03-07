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
数据库初始化器

负责数据库连接初始化和迁移执行。
"""

import logging
import os

from core.database import get_db_manager


class DatabaseInitializer:
    """数据库初始化器"""

    def __init__(self):
        self.logger = logging.getLogger(__name__)

    async def initialize(self) -> None:
        """初始化数据库连接并执行迁移"""
        self.logger.info("初始化数据库连接...")

        db_manager = get_db_manager()

        # 检查是否需要初始化
        if (
            hasattr(db_manager, "init_database")
            and hasattr(db_manager, "pool")
            and db_manager.pool is None
        ):
            try:
                # 尝试初始化数据库（MySQL）
                await db_manager.init_database()
                self.logger.info("数据库连接池初始化完成")
            except Exception as e:
                # MySQL初始化失败，回退到SQLite
                self.logger.warning(f"MySQL初始化失败: {e}")
                self.logger.info("回退到 SQLite...")

                # 强制重新创建
                import core.database as db_module

                db_module.db_manager = None

                # 更新配置
                os.environ["DATABASE_TYPE"] = "sqlite"

                db_manager = get_db_manager()
                if hasattr(db_manager, "init_database"):
                    await db_manager.init_database()
                self.logger.info("SQLite数据库初始化完成")
        else:
            self.logger.info("数据库连接已存在或不需要初始化")

        # 执行数据库迁移
        await self._run_migrations(db_manager)

    async def _run_migrations(self, db_manager) -> None:
        """执行数据库迁移

        Args:
            db_manager: 数据库管理器实例
        """
        self.logger.info("检查并执行数据库迁移...")

        try:
            from core.migrations.migrate_forwarding_table_v1 import ensure_forwarding_table_updated

            await ensure_forwarding_table_updated(db_manager)
        except Exception as e:
            self.logger.warning(f"数据库迁移执行失败（可忽略）: {type(e).__name__}: {e}")
