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
评论区AI聊天初始化器

负责初始化频道评论区AI聊天功能。
"""

import logging
from typing import TYPE_CHECKING

from telethon.events import NewMessage

if TYPE_CHECKING:
    from telethon import TelegramClient

from core.handlers.comment_chat_handler import (
    get_comment_chat_handler,
    initialize_comment_chat,
)


class CommentChatInitializer:
    """评论区AI聊天初始化器"""

    def __init__(self):
        self.logger = logging.getLogger(__name__)

    async def initialize(self, client: "TelegramClient", db_manager=None) -> None:
        """初始化评论区AI聊天功能

        Args:
            client: Telegram客户端实例
            db_manager: 数据库管理器实例（可选，未使用）
        """
        self.logger.info("初始化频道评论区AI聊天功能...")

        try:
            # 初始化评论区聊天处理器
            await initialize_comment_chat(client)

            # 添加评论区聊天监听器
            comment_chat_handler = get_comment_chat_handler()
            client.add_event_handler(
                comment_chat_handler.handle_new_message,
                NewMessage(func=lambda e: e.is_group and not e.out),
            )
            self.logger.info("频道评论区AI聊天监听器已注册")

        except Exception as e:
            self.logger.error(f"初始化频道评论区AI聊天功能失败: {type(e).__name__}: {e}")

    async def shutdown(self) -> None:
        """关闭评论区AI聊天功能"""
        self.logger.info("正在关闭频道评论区AI聊天功能...")

        try:
            from core.handlers.comment_chat_handler import shutdown_comment_chat

            await shutdown_comment_chat()
            self.logger.info("频道评论区AI聊天功能已关闭")

        except Exception as e:
            self.logger.error(f"关闭频道评论区AI聊天功能失败: {type(e).__name__}: {e}")
