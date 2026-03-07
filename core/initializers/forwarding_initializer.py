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
转发功能初始化器

负责初始化频道消息转发功能，包括设置监听器和处理器。
"""

import asyncio
import logging
from typing import TYPE_CHECKING, Optional

from telethon.events import NewMessage

if TYPE_CHECKING:
    from telethon import TelegramClient

from core.config import get_forwarding_config
from core.forwarding import ForwardingHandler, set_forwarding_handler
from core.userbot_client import UserBotClient


class ForwardingInitializer:
    """转发功能初始化器"""

    def __init__(self):
        self.logger = logging.getLogger(__name__)
        self.forwarding_handler: ForwardingHandler | None = None
        self.media_group_cache: dict[str, list] = {}

    async def initialize(
        self,
        bot_client: "TelegramClient",
        userbot_client: Optional["TelegramClient"],
        userbot: Optional["UserBotClient"],
    ) -> None:
        """初始化转发功能

        Args:
            bot_client: Bot客户端（用于发送消息）
            userbot_client: UserBot客户端（用于监听消息，可选）
            userbot: UserBot客户端实例（可选）
        """
        self.logger.info("初始化频道消息转发功能...")

        try:
            # 获取转发配置
            forwarding_config = get_forwarding_config()

            if not forwarding_config.get("enabled", False):
                self.logger.info("转发功能未启用（在config.json中设置enabled=true启用）")
                return

            # 确定监听客户端
            if userbot_client:
                monitoring_client = userbot_client
                self.logger.info("转发功能监听使用 UserBot 客户端（更高权限）")
            else:
                monitoring_client = bot_client
                self.logger.info("转发功能监听使用 Bot 客户端（UserBot 不可用）")

            # 发送消息总是使用 Bot 客户端
            sending_client = bot_client
            self.logger.info("转发功能发送使用 Bot 客户端")

            # 获取数据库管理器
            from core.database import get_db_manager

            db_manager = get_db_manager()

            # 创建转发处理器
            self.forwarding_handler = ForwardingHandler(
                db_manager, monitoring_client, sending_client
            )
            set_forwarding_handler(self.forwarding_handler)

            # 配置转发规则
            self.forwarding_handler.enabled = True
            self.forwarding_handler.set_config(forwarding_config)
            self.logger.info(f"转发功能已启用，共 {len(forwarding_config.get('rules', []))} 条规则")

            # UserBot 自动加入源频道
            if userbot:
                await self._auto_join_channels(userbot, forwarding_config)

            # 提取源频道ID
            source_channel_ids = self._extract_source_channels(forwarding_config)
            self.logger.info(f"转发功能监听的源频道: {source_channel_ids}")

            # 注册消息监听器
            await self._register_message_listener(
                monitoring_client, source_channel_ids, userbot_client is not None
            )

        except Exception as e:
            self.logger.error(f"初始化频道消息转发功能失败: {type(e).__name__}: {e}")

    async def _auto_join_channels(self, userbot, forwarding_config: dict) -> None:
        """UserBot自动加入源频道

        Args:
            userbot: UserBot客户端实例
            forwarding_config: 转发配置
        """
        self.logger.info("UserBot 开始自动加入转发配置的源频道...")

        result = await userbot.join_all_forwarding_channels(forwarding_config)

        if result.get("success"):
            success_count = result.get("success_count", 0)
            failed_count = result.get("failed_count", 0)
            failed_list = result.get("failed_list", [])

            # 记录失败的频道
            if failed_list:
                failed_items = []
                for item in failed_list:
                    channel = item.get("channel", "未知")
                    reason = item.get("reason", "未知原因")
                    failed_items.append(f"• {channel}: {reason}")
                failed_list_text = "\n".join(failed_items)
                self.logger.warning(f"UserBot 自动加入频道失败列表:\n{failed_list_text}")

            self.logger.info(
                f"UserBot 自动加入频道完成: 成功 {success_count} 个, 失败 {failed_count} 个"
            )
        else:
            error_message = result.get("message", "未知错误")
            self.logger.error(f"UserBot 自动加入源频道失败: {error_message}")

    def _extract_source_channels(self, forwarding_config: dict) -> set:
        """提取所有源频道ID

        Args:
            forwarding_config: 转发配置

        Returns:
            源频道ID集合
        """
        source_channels = forwarding_config.get("rules", [])
        source_channel_ids = set()

        for rule in source_channels:
            source_url = rule.get("source_channel", "")
            if source_url:
                channel_id = source_url.rstrip("/").split("/")[-1]
                source_channel_ids.add(channel_id)

        return source_channel_ids

    async def _register_message_listener(
        self, monitoring_client: "TelegramClient", source_channel_ids: set, is_userbot: bool
    ) -> None:
        """注册频道消息监听器

        Args:
            monitoring_client: 用于监听的客户端
            source_channel_ids: 源频道ID集合
            is_userbot: 是否使用UserBot
        """
        client_type = "UserBot" if is_userbot else "Bot"

        async def handle_channel_message(event):
            """处理频道消息，触发转发"""
            try:
                # 获取当前频道的用户名或ID
                chat_entity = await event.get_chat()
                chat_username = getattr(chat_entity, "username", None)
                chat_id = str(getattr(chat_entity, "id", ""))

                self.logger.debug(
                    f"转发监听器被触发: chat_username={chat_username}, chat_id={chat_id}"
                )

                # 检查是否是配置的源频道
                is_source_channel = (chat_username and chat_username in source_channel_ids) or (
                    chat_id in source_channel_ids
                )

                if not is_source_channel:
                    self.logger.debug(f"频道 {chat_username or chat_id} 不是源频道，跳过")
                    return

                self.logger.info(f"检测到源频道 {chat_username or chat_id} 的消息，开始处理转发")

                # 检查是否是媒体组消息
                grouped_id = getattr(event.message, "grouped_id", None)

                if grouped_id:
                    # 媒体组消息处理
                    await self._handle_media_group_message(
                        event, grouped_id, chat_username or chat_id
                    )
                else:
                    # 普通消息：直接处理
                    await self.forwarding_handler.process_message(event.message)

            except Exception as e:
                self.logger.error(f"处理频道消息转发失败: {type(e).__name__}: {e}", exc_info=True)

        # 根据客户端类型注册监听器
        monitoring_client.add_event_handler(
            handle_channel_message, NewMessage(func=lambda e: e.is_channel)
        )
        self.logger.info(f"频道消息转发监听器已注册到 {client_type} 客户端（仅监听配置的源频道）")

    async def _handle_media_group_message(self, event, grouped_id: int, channel_id: str) -> None:
        """处理媒体组消息

        Args:
            event: 消息事件
            grouped_id: 媒体组ID
            channel_id: 频道ID
        """
        group_key = f"{channel_id}_{grouped_id}"

        if group_key not in self.media_group_cache:
            # 这是媒体组的第一条消息
            if not event.message.message:
                # 第一条消息没有文本，跳过整个媒体组
                self.logger.debug(f"媒体组 {grouped_id} 第一条消息没有文本说明，跳过收集")
                return

            # 有文本，开始收集
            self.media_group_cache[group_key] = []

            # 设置延迟处理任务
            asyncio.create_task(
                self._delayed_forward_media_group(group_key, grouped_id, event.message)
            )

        # 将当前消息添加到缓存
        if event.message.id not in [msg.id for msg in self.media_group_cache[group_key]]:
            self.media_group_cache[group_key].append(event.message)
            self.logger.debug(
                f"媒体组收集: grouped_id={grouped_id}, "
                f"已收集 {len(self.media_group_cache[group_key])} 条消息"
            )

    async def _delayed_forward_media_group(
        self, group_key: str, grouped_id: int, first_message
    ) -> None:
        """延迟处理媒体组消息

        Args:
            group_key: 缓存键
            grouped_id: 媒体组ID
            first_message: 第一条消息
        """
        try:
            # 等待1秒，让同组的其他消息到达
            await asyncio.sleep(1)

            # 获取缓存的消息列表
            messages = self.media_group_cache.get(group_key, []).copy()

            # 清理缓存
            if group_key in self.media_group_cache:
                del self.media_group_cache[group_key]

            if not messages:
                self.logger.warning(f"媒体组 {grouped_id} 缓存为空")
                return

            self.logger.info(f"媒体组收集完成: grouped_id={grouped_id}, 共 {len(messages)} 条消息")

            # 按消息ID排序
            messages.sort(key=lambda m: m.id)

            # 将收集的消息传递给转发处理器
            self.forwarding_handler.set_external_media_group(grouped_id, messages)

            # 只处理第一条消息
            await self.forwarding_handler.process_message(messages[0])

        except Exception as e:
            self.logger.error(f"延迟处理媒体组失败: {type(e).__name__}: {e}", exc_info=True)
