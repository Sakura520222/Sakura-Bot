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
评论区欢迎初始化器

负责初始化频道评论区欢迎消息功能。
"""

import logging
from typing import TYPE_CHECKING

from telethon.events import CallbackQuery, NewMessage

if TYPE_CHECKING:
    from telethon import TelegramClient

from core.channel_comment_welcome import (
    get_comment_welcome_handler,
    handle_summary_request_callback,
    initialize_comment_welcome,
)


class CommentWelcomeInitializer:
    """评论区欢迎初始化器"""

    def __init__(self):
        self.logger = logging.getLogger(__name__)

    async def initialize(self, client: "TelegramClient", db_manager=None) -> None:
        """初始化评论区欢迎功能

        Args:
            client: Telegram客户端实例
            db_manager: 数据库管理器实例（可选）
        """
        self.logger.info("初始化频道评论区欢迎消息功能...")

        try:
            # 初始化评论区欢迎处理器
            await initialize_comment_welcome(client, db_manager)

            # 添加评论欢迎消息监听器
            comment_welcome_handler = get_comment_welcome_handler()
            client.add_event_handler(
                comment_welcome_handler.handle_discussion_message,
                NewMessage(func=lambda e: e.is_channel and not e.out),
            )
            self.logger.info("频道评论区欢迎消息监听器已注册")

            # 添加申请周报总结按钮回调处理器
            client.add_event_handler(
                handle_summary_request_callback,
                CallbackQuery(func=lambda e: e.data and e.data.startswith(b"req_summary:")),
            )
            self.logger.info("申请周报总结按钮回调处理器已注册")

        except Exception as e:
            self.logger.error(f"初始化频道评论区欢迎消息功能失败: {type(e).__name__}: {e}")
