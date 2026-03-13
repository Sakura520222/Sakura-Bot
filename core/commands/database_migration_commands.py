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
数据库迁移命令处理器模块

处理从SQLite到MySQL的数据库迁移相关命令
"""

import asyncio
import os
from pathlib import Path

import aiofiles
from telethon.events import NewMessage

from core.config import ADMIN_LIST, logger
from core.i18n.i18n import get_text
from core.infrastructure.database.migrator import DatabaseMigrator
from core.infrastructure.database.mysql import MySQLManager

# 全局迁移器实例
migrator = None
migration_in_progress = False

# 等待确认清空数据库的用户集合
pending_clear_confirmations = set()


async def handle_migrate_check(event: NewMessage.Event):
    """
    处理 /migrate_check 命令
    检查数据库迁移准备状态
    """
    user_id = event.sender_id

    # 检查权限
    if user_id not in ADMIN_LIST and user_id != "me":
        await event.reply(get_text("error.no_permission"))
        return

    try:
        await event.reply(get_text("database.migrate.checking"))

        # 创建迁移器
        global migrator
        mysql_config = {
            "host": os.getenv("MYSQL_HOST"),
            "port": int(os.getenv("MYSQL_PORT", "3306")),
            "user": os.getenv("MYSQL_USER"),
            "password": os.getenv("MYSQL_PASSWORD"),
            "database": os.getenv("MYSQL_DATABASE"),
        }

        migrator = DatabaseMigrator(mysql_config=mysql_config)

        # 检查迁移准备状态
        result = await migrator.check_migration_ready()

        # 构建响应消息
        message = f"🗄️ {get_text('database.migrate.check_result')}\n\n"

        # SQLite状态
        if result.get("sqlite_exists"):
            message += f"✅ {get_text('database.migrate.sqlite_found')}\n"
            tables = result.get("sqlite_tables", {})
            if tables:
                total_records = sum(tables.values())
                message += f"📊 {get_text('database.migrate.total_records')}: {total_records}\n\n"
                message += f"{get_text('database.migrate.table_details')}:\n"
                for table, count in tables.items():
                    message += f"  • {table}: {count}\n"
        else:
            message += f"❌ {get_text('database.migrate.sqlite_not_found')}\n"

        message += "\n"

        # MySQL状态
        if result.get("mysql_configured"):
            message += f"✅ {get_text('database.migrate.mysql_configured')}\n"
            if result.get("mysql_connectable"):
                message += f"✅ {get_text('database.migrate.mysql_connected')}\n"
            else:
                message += f"❌ {get_text('database.migrate.mysql_connect_failed')}\n"
        else:
            message += f"❌ {get_text('database.migrate.mysql_not_configured')}\n"

        message += f"\n{get_text('database.migrate.status')}: "
        if result.get("ready"):
            message += f"✅ {get_text('database.migrate.ready')}\n\n"
            message += f"💡 {get_text('database.migrate.can_start')}: /migrate_start"
        else:
            message += f"❌ {get_text('database.migrate.not_ready')}\n"
            message += (
                f"📝 {get_text('database.migrate.reason')}: {result.get('message', 'Unknown')}\n"
            )

        await event.reply(message)

    except Exception as e:
        logger.error(f"检查迁移状态失败: {e}", exc_info=True)
        await event.reply(f"❌ {get_text('database.migrate.check_failed')}: {str(e)}")


async def handle_migrate_start(event: NewMessage.Event):
    """
    处理 /migrate_start 命令
    开始数据库迁移
    """
    user_id = event.sender_id

    # 检查权限
    if user_id not in ADMIN_LIST and user_id != "me":
        await event.reply(get_text("error.no_permission"))
        return

    global migration_in_progress

    if migration_in_progress:
        await event.reply(get_text("database.migrate.already_in_progress"))
        return

    try:
        # 创建迁移器（如果还没创建）
        global migrator
        if migrator is None:
            mysql_config = {
                "host": os.getenv("MYSQL_HOST"),
                "port": int(os.getenv("MYSQL_PORT", "3306")),
                "user": os.getenv("MYSQL_USER"),
                "password": os.getenv("MYSQL_PASSWORD"),
                "database": os.getenv("MYSQL_DATABASE"),
            }
            migrator = DatabaseMigrator(mysql_config=mysql_config)

        # 检查是否准备好
        check_result = await migrator.check_migration_ready()
        if not check_result.get("ready"):
            await event.reply(
                f"❌ {get_text('database.migrate.not_ready')}\n\n"
                f"{get_text('database.migrate.reason')}: {check_result.get('message')}"
            )
            return

        # 开始迁移
        migration_in_progress = True
        await event.reply(
            f"🚀 {get_text('database.migrate.starting')}\n\n"
            f"⏳ {get_text('database.migrate.please_wait')}\n\n"
            f"📊 {get_text('database.migrate.check_progress')}: /migrate_status"
        )

        # 执行迁移
        result = await migrator.migrate_data()

        # 发送结果
        if result.get("success"):
            message = f"✅ {get_text('database.migrate.success')}\n\n"

            # 迁移统计
            stats = result.get("stats", {})
            total_migrated = 0
            total_failed = 0
            for table, table_stats in stats.items():
                migrated = table_stats.get("migrated", 0)
                failed = table_stats.get("failed", 0)
                total_migrated += migrated
                total_failed += failed
                message += f"📊 {table}: {get_text('database.migrate.migrated')} {migrated}"
                if failed > 0:
                    message += f", {get_text('database.migrate.failed')} {failed}"
                message += "\n"

            # 验证结果
            verification = result.get("verification", {})
            if verification.get("matched"):
                message += f"\n✅ {get_text('database.migrate.verification_passed')}"
            else:
                message += f"\n⚠️ {get_text('database.migrate.verification_warning')}"
                # 显示不匹配的表
                if "sqlite_stats" in verification:
                    sqlite_stats = verification["sqlite_stats"]
                    mysql_stats = verification["mysql_stats"]
                    message += "\n\n📊 数据不匹配详情:\n"
                    for table in sqlite_stats:
                        if sqlite_stats[table] != mysql_stats.get(table, 0):
                            message += f"  • {table}: SQLite={sqlite_stats[table]}, MySQL={mysql_stats.get(table, 0)}\n"

            # 备份位置
            backup = result.get("backup_path")
            if backup:
                message += f"\n\n💾 {get_text('database.migrate.backup_location')}: {backup}"

            # 🔧 自动化流程：修改配置、删除旧文件、重启
            message += f"\n\n🔄 {get_text('database.migrate.auto_switch')}\n"

            try:
                # 1. 修改 .env 配置
                env_path = Path("data/.env")
                async with aiofiles.open(env_path, encoding="utf-8") as f:
                    env_content = await f.read()

                # 替换 DATABASE_TYPE
                new_env_content = env_content
                for line in env_content.split("\n"):
                    if line.strip().startswith("DATABASE_TYPE="):
                        new_env_content = new_env_content.replace(line, "DATABASE_TYPE=mysql")
                        break

                async with aiofiles.open(env_path, "w", encoding="utf-8") as f:
                    await f.write(new_env_content)

                message += f"✅ {get_text('database.migrate.auto_switch')}\n"
                logger.info("✅ 已自动切换 .env 配置为 MySQL")

                # 2. 删除旧 SQLite 文件（已有备份）
                sqlite_db_path = "data/summaries.db"
                if await asyncio.to_thread(os.path.exists, sqlite_db_path):
                    await asyncio.to_thread(os.remove, sqlite_db_path)
                    message += f"✅ {get_text('database.migrate.sqlite_deleted')}\n"
                    logger.info(f"✅ 已删除旧 SQLite 数据库文件: {sqlite_db_path}")
                else:
                    message += f"ℹ️ {get_text('database.migrate.sqlite_not_found')}\n"

                # 3. 准备重启
                message += f"\n🔄 {get_text('database.migrate.restart_in_3s')}\n\n"
                message += f"💡 {get_text('database.migrate.warning_keep_backup')}\n"
                message += f"📁 备份文件: {backup}"

                # 延迟3秒后重启
                await asyncio.sleep(3)

                # 调用重启命令
                from core.commands.other_commands import handle_restart

                await handle_restart(event)

            except Exception as e:
                message += f"\n\n❌ {get_text('database.migrate.manual_switch_required')}\n"
                message += f"错误: {str(e)}\n\n"
                message += f"📝 {get_text('database.migrate.manual_switch_steps')}\n"
                message += "1️⃣ 在 data/.env 中设置: DATABASE_TYPE=mysql\n"
                message += "2️⃣ 重启机器人: /restart\n"
                message += "3️⃣ 重启成功后，使用 /migrate_cleanup 删除旧 SQLite 文件"
                logger.error(f"自动切换失败: {e}", exc_info=True)

        else:
            message = f"❌ {get_text('database.migrate.failed')}\n\n"
            message += f"📝 {get_text('database.migrate.error')}: {result.get('message')}\n"
            backup = result.get("backup_path")
            if backup:
                message += f"\n💾 {get_text('database.migrate.backup_location')}: {backup}"

        await event.reply(message)

    except Exception as e:
        logger.error(f"数据迁移失败: {e}", exc_info=True)
        await event.reply(f"❌ {get_text('database.migrate.failed')}: {str(e)}")

    finally:
        migration_in_progress = False


async def handle_migrate_status(event: NewMessage.Event):
    """
    处理 /migrate_status 命令
    查看迁移进度
    """
    user_id = event.sender_id

    # 检查权限
    if user_id not in ADMIN_LIST and user_id != "me":
        await event.reply(get_text("error.no_permission"))
        return

    try:
        global migrator
        if migrator is None:
            await event.reply(get_text("database.migrate.no_migration_in_progress"))
            return

        status = migrator.get_migration_status()

        message = f"📊 {get_text('database.migrate.current_status')}\n\n"
        message += f"{get_text('database.migrate.status')}: {status.get('status', 'unknown')}\n"

        if status.get("progress"):
            progress = status["progress"]
            bar_length = 20
            filled = int(bar_length * progress / 100)
            bar = "█" * filled + "░" * (bar_length - filled)
            message += f"{get_text('database.migrate.progress')}: {bar} {progress}%\n"

        if status.get("message"):
            message += f"{get_text('database.migrate.message')}: {status['message']}\n"

        # 表统计
        table_stats = status.get("table_stats", {})
        if table_stats:
            message += f"\n{get_text('database.migrate.table_stats')}:\n"
            for table, stats in table_stats.items():
                message += f"  • {table}: {get_text('database.migrate.migrated')} {stats.get('migrated', 0)}"
                if stats.get("failed", 0) > 0:
                    message += f", {get_text('database.migrate.failed')} {stats['failed']}"
                message += "\n"

        await event.reply(message)

    except Exception as e:
        logger.error(f"获取迁移状态失败: {e}", exc_info=True)
        await event.reply(f"❌ {get_text('database.migrate.status_failed')}: {str(e)}")


async def handle_db_clear(event: NewMessage.Event):
    """
    处理 /db_clear 命令
    清空 MySQL 数据库（需要二次确认）
    """
    user_id = event.sender_id

    # 检查权限
    if user_id not in ADMIN_LIST and user_id != "me":
        await event.reply(get_text("error.no_permission"))
        return

    try:
        # 检查当前是否使用 MySQL
        db_type = os.getenv("DATABASE_TYPE", "sqlite").lower()
        if db_type != "mysql":
            await event.reply(
                f"❌ {get_text('database.clear.not_mysql')}\n\n"
                f"{get_text('database.clear.current_type')}: {db_type}\n"
                f"{get_text('database.clear.only_mysql')}"
            )
            return

        # 创建 MySQL 管理器
        db_manager = MySQLManager(
            host=os.getenv("MYSQL_HOST"),
            port=int(os.getenv("MYSQL_PORT", "3306")),
            user=os.getenv("MYSQL_USER"),
            password=os.getenv("MYSQL_PASSWORD"),
            database=os.getenv("MYSQL_DATABASE"),
        )

        # 初始化数据库连接
        await db_manager.init_database()

        # 获取当前数据统计
        stats = await db_manager.get_statistics()

        # 构建确认消息
        message = f"⚠️ {get_text('database.clear.warning')}\n\n"
        message += f"📊 {get_text('database.clear.current_data')}:\n"
        message += (
            f"• {get_text('database.migrate.total_records')}: {stats.get('total_messages', 0)}\n"
        )
        message += (
            f"• {get_text('database.clear.total_summaries')}: {stats.get('total_count', 0)}\n\n"
        )
        message += f"🚨 {get_text('database.clear.irreversible')}\n\n"
        message += f"💡 {get_text('database.clear.confirm_instruction')}\n"
        message += f"📝 {get_text('database.clear.confirm_command')}: /db_clear_confirm"
        message += f"\n📝 {get_text('database.clear.cancel_command')}: /db_clear_cancel"

        # 添加到待确认列表
        pending_clear_confirmations.add(user_id)

        await event.reply(message)

    except Exception as e:
        logger.error(f"准备清空数据库失败: {e}", exc_info=True)
        await event.reply(f"❌ {get_text('database.clear.prepare_failed')}: {str(e)}")


async def handle_db_clear_confirm(event: NewMessage.Event):
    """
    处理 /db_clear_confirm 命令
    确认清空 MySQL 数据库
    """
    user_id = event.sender_id

    # 检查权限
    if user_id not in ADMIN_LIST and user_id != "me":
        await event.reply(get_text("error.no_permission"))
        return

    # 检查是否在待确认列表中
    if user_id not in pending_clear_confirmations:
        await event.reply(get_text("database.clear.no_pending"))
        return

    try:
        # 从待确认列表中移除
        pending_clear_confirmations.discard(user_id)

        await event.reply(f"🗑️ {get_text('database.clear.clearing')}...")

        # 创建 MySQL 管理器
        db_manager = MySQLManager(
            host=os.getenv("MYSQL_HOST"),
            port=int(os.getenv("MYSQL_PORT", "3306")),
            user=os.getenv("MYSQL_USER"),
            password=os.getenv("MYSQL_PASSWORD"),
            database=os.getenv("MYSQL_DATABASE"),
        )

        # 初始化数据库连接
        await db_manager.init_database()

        # 清空所有表（注意：由于外键约束，需按依赖顺序删除）
        tables = [
            "comment_messages",  # 依赖 comment_sessions
            "comment_sessions",  # 被依赖
            "comment_cache",  # 评论区缓存表
            "forwarded_messages",
            "forwarding_stats",
            "summaries",
            "conversation_history",
            "users",
            "subscriptions",
            "usage_quota",
            "request_queue",
            "notification_queue",
            "channel_profiles",
            "db_version",
        ]

        cleared_tables = []
        failed_tables = []

        for table in tables:
            try:
                await db_manager.execute_query(f"DELETE FROM {table}")
                cleared_tables.append(table)
                logger.info(f"已清空表: {table}")
            except Exception as e:
                failed_tables.append((table, str(e)))
                logger.error(f"清空表 {table} 失败: {e}")

        # 构建结果消息
        message = f"✅ {get_text('database.clear.completed')}\n\n"

        if cleared_tables:
            message += f"✅ {get_text('database.clear.cleared_tables')} ({len(cleared_tables)}):\n"
            for table in cleared_tables:
                message += f"  • {table}\n"

        if failed_tables:
            message += f"\n❌ {get_text('database.clear.failed_tables')} ({len(failed_tables)}):\n"
            for table, error in failed_tables:
                message += f"  • {table}: {error}\n"

        message += f"\n💡 {get_text('database.clear.note')}"

        await event.reply(message)

    except Exception as e:
        logger.error(f"清空数据库失败: {e}", exc_info=True)
        await event.reply(f"❌ {get_text('database.clear.failed')}: {str(e)}")


async def handle_db_clear_cancel(event: NewMessage.Event):
    """
    处理 /db_clear_cancel 命令
    取消清空数据库操作
    """
    user_id = event.sender_id

    # 检查权限
    if user_id not in ADMIN_LIST and user_id != "me":
        await event.reply(get_text("error.no_permission"))
        return

    # 从待确认列表中移除
    if user_id in pending_clear_confirmations:
        pending_clear_confirmations.discard(user_id)
        await event.reply(f"✅ {get_text('database.clear.cancelled')}")
    else:
        await event.reply(get_text("database.clear.no_pending"))


async def handle_migrate_cleanup(event: NewMessage.Event):
    """
    处理 /migrate_cleanup 命令
    删除旧的 SQLite 数据库文件（仅在确认 MySQL 工作正常后使用）
    """
    user_id = event.sender_id

    # 检查权限
    if user_id not in ADMIN_LIST and user_id != "me":
        await event.reply(get_text("error.no_permission"))
        return

    try:
        # 检查当前是否使用 MySQL
        db_type = os.getenv("DATABASE_TYPE", "sqlite").lower()
        if db_type != "mysql":
            await event.reply(
                f"❌ 当前未使用 MySQL 数据库\n\n"
                f"当前数据库类型: {db_type}\n\n"
                f"⚠️ 请确保已切换到 MySQL 并验证工作正常后，再执行清理"
            )
            return

        sqlite_db_path = "data/summaries.db"

        # 检查 SQLite 文件是否存在
        if not await asyncio.to_thread(os.path.exists, sqlite_db_path):
            await event.reply(
                f"✅ SQLite 数据库文件不存在\n\n路径: {sqlite_db_path}\n\n可能已被删除或从未存在"
            )
            return

        # 获取文件大小
        file_size = await asyncio.to_thread(os.path.getsize, sqlite_db_path)
        file_size_mb = file_size / (1024 * 1024)

        # 构建确认消息
        message = (
            f"⚠️ 即将删除 SQLite 数据库文件\n\n"
            f"📁 文件路径: {sqlite_db_path}\n"
            f"📊 文件大小: {file_size_mb:.2f} MB\n\n"
            f"🚨 此操作不可逆！\n\n"
            f"请确认:\n"
            f"1. ✅ 已成功迁移到 MySQL\n"
            f"2. ✅ MySQL 数据库验证正常\n"
            f"3. ✅ 机器人使用 MySQL 运行正常\n\n"
            f"💡 如确认无误，请执行: /migrate_cleanup_confirm"
        )

        await event.reply(message)

    except Exception as e:
        logger.error(f"准备清理 SQLite 失败: {e}", exc_info=True)
        await event.reply(f"❌ 准备清理失败: {str(e)}")


async def handle_migrate_cleanup_confirm(event: NewMessage.Event):
    """
    处理 /migrate_cleanup_confirm 命令
    确认删除旧的 SQLite 数据库文件
    """
    user_id = event.sender_id

    # 检查权限
    if user_id not in ADMIN_LIST and user_id != "me":
        await event.reply(get_text("error.no_permission"))
        return

    try:
        sqlite_db_path = "data/summaries.db"

        # 检查文件是否存在
        if not await asyncio.to_thread(os.path.exists, sqlite_db_path):
            await event.reply(f"✅ SQLite 数据库文件不存在\n\n路径: {sqlite_db_path}")
            return

        # 删除文件
        await asyncio.to_thread(os.remove, sqlite_db_path)
        logger.info(f"✅ SQLite 数据库文件已删除: {sqlite_db_path}")

        # 检查是否有备份文件
        backup_dir = os.path.dirname(sqlite_db_path)
        backup_prefix = os.path.basename(sqlite_db_path) + ".backup_"
        backup_files = (
            [
                os.path.join(backup_dir, f)
                for f in os.listdir(backup_dir)
                if f.startswith(backup_prefix)
            ]
            if await asyncio.to_thread(os.path.exists, backup_dir)
            else []
        )

        message = "✅ SQLite 数据库文件已删除\n\n"
        message += f"📁 已删除: {sqlite_db_path}\n\n"

        if backup_files:
            message += f"💾 发现 {len(backup_files)} 个备份文件:\n"
            for backup in backup_files[:3]:  # 只显示前3个
                message += f"  • {os.path.basename(backup)}\n"
            if len(backup_files) > 3:
                message += f"  • ... 还有 {len(backup_files) - 3} 个\n"
            message += "\n💡 建议保留备份文件一段时间，确认 MySQL 工作正常后再删除"

        await event.reply(message)

    except Exception as e:
        logger.error(f"删除 SQLite 数据库文件失败: {e}", exc_info=True)
        await event.reply(f"❌ 删除失败: {str(e)}")
