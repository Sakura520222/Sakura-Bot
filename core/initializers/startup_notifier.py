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
启动通知器

负责向管理员发送启动消息、数据库迁移建议等通知。
"""

import asyncio
import logging
import os
from typing import TYPE_CHECKING

import aiofiles

if TYPE_CHECKING:
    from telethon import TelegramClient

from core.config import ADMIN_LIST, RESTART_FLAG_FILE
from core.i18n.i18n import get_text
from core.infrastructure.config.system_config import SystemConfigManager


class StartupNotifier:
    """启动通知器"""

    def __init__(self, version: str = "1.7.4", system_config_manager: SystemConfigManager = None):
        self.logger = logging.getLogger(__name__)
        self.version = version
        self.system_config_manager = system_config_manager

    async def send_startup_message(self, client: "TelegramClient") -> None:
        """向所有管理员发送启动消息

        Args:
            client: Telegram客户端实例
        """
        self.logger.info("开始向管理员发送启动消息...")

        try:
            from core.bot_commands import get_command_categories

            # 使用 bot_commands 模块动态生成命令列表
            categories = get_command_categories()

            # 只显示常用命令（每个分类的前3个）
            common_commands = []
            for category_group in categories[:5]:  # 前5个分类
                for cmd, _ in category_group["commands"][:3]:  # 每个分类前3个命令
                    common_commands.append(get_text(f"cmd.{cmd}"))

            # 构建帮助信息（使用 i18n，支持多语言）
            help_text = f"""🤖 **Sakura-Bot v{self.version} 已启动**

**核心功能**
• 自动总结频道消息
• 多频道管理
• 自定义提示词
• AI配置调整
• 定时任务调度

**可用命令**
{chr(10).join(common_commands)}

**版本信息**
当前版本: v{self.version}

💡 使用 /help 查看完整命令列表

机器人运行正常，随时为您服务！"""

            # 向所有管理员发送消息
            for admin_id in ADMIN_LIST:
                try:
                    await client.send_message(
                        admin_id, help_text, parse_mode="md", link_preview=False
                    )
                    self.logger.info(f"已向管理员 {admin_id} 发送启动消息")
                except Exception as e:
                    self.logger.error(
                        f"向管理员 {admin_id} 发送启动消息失败: {type(e).__name__}: {e}"
                    )

        except Exception as e:
            self.logger.error(f"发送启动消息时出错: {type(e).__name__}: {e}", exc_info=True)

    async def check_database_migration(self, client: "TelegramClient") -> None:
        """检查数据库类型，如果使用SQLite则建议迁移

        Args:
            client: Telegram客户端实例
        """
        self.logger.info("检查数据库类型...")

        # 读取环境变量判断当前使用的数据库类型
        current_db_type = os.getenv("DATABASE_TYPE", "sqlite").lower()

        # 只在当前使用 SQLite 时才提示迁移
        if current_db_type == "sqlite":
            # 检查SQLite数据库文件是否存在且有数据
            sqlite_db_path = "data/summaries.db"

            if await asyncio.to_thread(os.path.exists, sqlite_db_path):
                # 检查数据库大小，如果有数据则提示迁移
                db_size = await asyncio.to_thread(os.path.getsize, sqlite_db_path)
                if db_size > 0:
                    self.logger.info(f"检测到SQLite数据库，大小: {db_size} 字节")

                    for admin_id in ADMIN_LIST:
                        try:
                            await client.send_message(
                                admin_id,
                                get_text("database.startup_old_database"),
                                parse_mode="md",
                                link_preview=False,
                            )
                            self.logger.info(f"已向管理员 {admin_id} 发送数据库迁移建议")
                        except Exception as e:
                            self.logger.error(f"向管理员 {admin_id} 发送迁移建议失败: {e}")
                else:
                    self.logger.info("SQLite数据库为空，跳过迁移提示")
            else:
                self.logger.info("未检测到SQLite数据库文件，跳过迁移提示")
        else:
            self.logger.info(f"使用 {current_db_type.upper()} 数据库，无需迁移")

    async def check_restart_flag(self, client: "TelegramClient") -> None:
        """检查是否是重启后的首次运行

        Args:
            client: Telegram客户端实例
        """
        if await asyncio.to_thread(os.path.exists, RESTART_FLAG_FILE):
            try:
                async with aiofiles.open(RESTART_FLAG_FILE) as f:
                    content = (await f.read()).strip()

                # 尝试解析为用户ID
                try:
                    restart_user_id = int(content)
                    # 发送重启成功消息给特定用户
                    self.logger.info(f"检测到重启标记，向用户 {restart_user_id} 发送重启成功消息")
                    await client.send_message(
                        restart_user_id, "机器人已成功重启！", link_preview=False
                    )
                except ValueError:
                    # 如果不是整数，忽略
                    self.logger.info(f"检测到重启标记，但内容不是有效的用户ID: {content}")

                # 删除重启标记文件
                await asyncio.to_thread(os.remove, RESTART_FLAG_FILE)
                self.logger.info("重启标记文件已删除")

            except Exception as e:
                self.logger.error(f"处理重启标记时出错: {type(e).__name__}: {e}", exc_info=True)
