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
UserBot初始化器

负责初始化UserBot客户端（用于频道消息监听）。
"""

import logging
from typing import TYPE_CHECKING, Optional

if TYPE_CHECKING:
    from telethon import TelegramClient

from core.userbot_client import UserBotClient, init_userbot_client


class UserBotInitializer:
    """UserBot初始化器"""

    def __init__(self):
        self.logger = logging.getLogger(__name__)
        self.userbot_client: UserBotClient | None = None
        self.telegram_client: TelegramClient | None = None

    async def initialize(self) -> Optional["TelegramClient"]:
        """初始化UserBot客户端

        Returns:
            UserBot的Telegram客户端实例，如果初始化失败则返回None
        """
        self.logger.info("初始化 UserBot 客户端...")

        try:
            self.userbot_client = await init_userbot_client()

            if self.userbot_client:
                success = await self.userbot_client.start()
                if success:
                    self.telegram_client = self.userbot_client.get_client()
                    self.logger.info("UserBot 客户端启动成功")
                    return self.telegram_client
                else:
                    self.logger.warning("UserBot 客户端启动失败")
                    return None
            else:
                self.logger.info("UserBot 未启用或配置缺失")
                return None

        except Exception as e:
            self.logger.error(f"初始化 UserBot 客户端失败: {type(e).__name__}: {e}", exc_info=True)
            return None

    async def keep_alive(self) -> None:
        """保持UserBot客户端运行（后台任务）"""
        if self.telegram_client and self.telegram_client.is_connected():
            try:
                self.logger.info("UserBot 客户端开始接收更新...")
                await self.telegram_client.run_until_disconnected()
            except Exception as e:
                self.logger.error(f"UserBot 客户端运行出错: {type(e).__name__}: {e}", exc_info=True)

    def get_client(self) -> Optional["TelegramClient"]:
        """获取UserBot的Telegram客户端实例

        Returns:
            Telegram客户端实例，如果未初始化则返回None
        """
        return self.telegram_client

    def get_userbot(self) -> UserBotClient | None:
        """获取UserBot客户端实例

        Returns:
            UserBot客户端实例，如果未初始化则返回None
        """
        return self.userbot_client
