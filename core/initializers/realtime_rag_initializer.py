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
实时 RAG 初始化器

注册频道消息监听器，将含文本的新消息、编辑消息、删除消息事件
路由到 RealtimeRAGHandler 进行向量入库/更新/删除。
"""

import logging
from typing import TYPE_CHECKING, Optional

from telethon import events

if TYPE_CHECKING:
    from telethon import TelegramClient

import core.config as config_module
from core.handlers.realtime_rag_handler import get_realtime_rag_handler

logger = logging.getLogger(__name__)


class RealtimeRAGInitializer:
    """实时 RAG 初始化器 - 注册频道消息监听器"""

    def __init__(self):
        self.logger = logging.getLogger(__name__)

    async def initialize(
        self,
        bot_client: "TelegramClient",
        userbot_client: Optional["TelegramClient"] = None,
    ) -> None:
        """
        初始化实时 RAG 功能

        Args:
            bot_client: Bot 客户端
            userbot_client: UserBot 客户端（可选，优先用于监听）
        """
        self.logger.info("初始化实时 RAG 功能...")

        try:
            # 获取实时 RAG 处理器
            rag_handler = get_realtime_rag_handler()

            # 确定监听客户端（优先 UserBot）
            monitoring_client = userbot_client if userbot_client else bot_client
            client_type = "UserBot" if userbot_client else "Bot"
            self.logger.info(f"实时RAG监听使用 {client_type} 客户端")

            # 启动处理器
            await rag_handler.start()

            # 注册事件监听器
            self._register_listeners(monitoring_client, rag_handler)

            self.logger.info(
                f"实时RAG功能初始化完成，监听 {len(config_module.CHANNELS)} 个频道的消息"
            )

        except Exception as e:
            self.logger.error(f"初始化实时RAG功能失败: {type(e).__name__}: {e}", exc_info=True)

    def _register_listeners(self, client: "TelegramClient", rag_handler) -> None:
        """
        注册消息事件监听器

        Args:
            client: Telethon 客户端
            rag_handler: RealtimeRAGHandler 实例
        """

        @client.on(events.NewMessage)
        async def on_new_message(event):
            """处理频道新消息"""
            try:
                # 仅处理频道消息
                if not event.is_channel:
                    return

                # 检查频道是否在白名单中（优先使用缓存的 chat 实体）
                chat = event.chat or await event.get_chat()
                if not self._is_whitelisted_channel(chat):
                    return

                # 提取文本（富媒体消息只取文本部分，无文本则跳过）
                text = event.message.message
                if not text or not text.strip():
                    return

                # 获取频道信息（保留数字内部 ID 与公开 username，前者用于
                # 向量唯一标识与 /c/ 私有链接，后者用于生成更友好的公开链接）
                channel_id = str(chat.id)
                channel_name = getattr(chat, "title", channel_id)
                channel_username = getattr(chat, "username", None)
                message_id = event.message.id
                sender_id = event.message.sender_id

                # 入队处理
                rag_handler.enqueue_message(
                    channel_id=channel_id,
                    channel_name=channel_name,
                    message_id=message_id,
                    text=text,
                    sender_id=sender_id,
                    channel_username=channel_username,
                )

            except Exception as e:
                logger.error(f"处理新消息事件失败: {type(e).__name__}: {e}")

        @client.on(events.MessageEdited)
        async def on_message_edited(event):
            """处理频道消息编辑"""
            try:
                if not event.is_channel:
                    return

                chat = event.chat or await event.get_chat()
                if not self._is_whitelisted_channel(chat):
                    return

                text = event.message.message
                if not text or not text.strip():
                    return

                channel_id = str(chat.id)
                channel_name = getattr(chat, "title", channel_id)
                channel_username = getattr(chat, "username", None)
                message_id = event.message.id
                sender_id = event.message.sender_id

                rag_handler.enqueue_message_update(
                    channel_id=channel_id,
                    channel_name=channel_name,
                    message_id=message_id,
                    text=text,
                    sender_id=sender_id,
                    channel_username=channel_username,
                )

            except Exception as e:
                logger.error(f"处理消息编辑事件失败: {type(e).__name__}: {e}")

        @client.on(events.MessageDeleted)
        async def on_message_deleted(event):
            """处理频道消息删除"""
            try:
                # MessageDeleted 事件没有 get_chat()，需要通过其他方式判断
                # 检查是否有频道相关的标签
                if not event.chat_id:
                    return

                chat_id = str(event.chat_id)
                if not self._is_whitelisted_chat_id(chat_id):
                    return

                # 批量删除
                for msg_id in event.deleted_ids:
                    await rag_handler.handle_message_delete(
                        channel_id=chat_id,
                        message_id=msg_id,
                    )

            except Exception as e:
                logger.error(f"处理消息删除事件失败: {type(e).__name__}: {e}")

        self.logger.info("实时RAG事件监听器注册完成（NewMessage + MessageEdited + MessageDeleted）")

    def _extract_channel_identifier(self, channel_str: str) -> str:
        """
        从频道 URL/username 中提取标识符

        支持格式: @username, https://t.me/username, t.me/username, username

        Args:
            channel_str: 频道字符串（URL、@username 或纯 username）

        Returns:
            归一化后的标识符（纯 username 部分）
        """
        normalized = channel_str.strip().lstrip("@")
        for prefix in ("https://t.me/", "http://t.me/", "t.me/"):
            if normalized.startswith(prefix):
                normalized = normalized.replace(prefix, "")
                break
        return normalized

    def _is_whitelisted_channel(self, chat) -> bool:
        """
        检查频道是否在白名单中

        支持通过 username 或数字 ID 匹配 CHANNELS 列表中的频道 URL。

        Args:
            chat: Telethon 频道实体

        Returns:
            是否在白名单中
        """
        chat_id = str(chat.id)
        username = getattr(chat, "username", None)

        for ch in config_module.CHANNELS:
            normalized = self._extract_channel_identifier(ch)
            if normalized == username or normalized == chat_id:
                return True

        return False

    def _is_whitelisted_chat_id(self, chat_id: str) -> bool:
        """
        检查 chat_id 是否在白名单中

        Args:
            chat_id: 频道ID字符串

        Returns:
            是否在白名单中
        """
        for ch in config_module.CHANNELS:
            normalized = self._extract_channel_identifier(ch)
            if normalized == chat_id:
                return True

        return False
