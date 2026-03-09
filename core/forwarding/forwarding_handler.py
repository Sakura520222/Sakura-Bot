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
转发处理器模块

提供Telegram频道消息转发的核心功能
"""

import asyncio
import hashlib
import logging
from datetime import UTC, datetime
from typing import TYPE_CHECKING, Any

# Import event system for config hot-reload
from ..config import AsyncIOEventBus, ConfigChangedEvent
from .download_manager import DownloadManager
from .filters import (
    should_forward_by_keywords,
    should_forward_by_regex,
    should_forward_original_only,
)
from .media_utils import ForwardStrategy, decide_forward_strategy

if TYPE_CHECKING:
    from telethon import TelegramClient
    from telethon.tl.types import Message

    from ..database import DatabaseManagerBase

logger = logging.getLogger(__name__)


class ForwardingHandler:
    """
    频道消息转发处理器

    负责处理频道消息的转发逻辑，包括：
    - 消息去重
    - 关键词过滤
    - 转发操作
    - 统计信息更新
    """

    def __init__(
        self,
        db: "DatabaseManagerBase",
        monitoring_client: "TelegramClient",
        sending_client: "TelegramClient",
        event_bus: "AsyncIOEventBus" = None,
    ):
        """
        初始化转发处理器

        Args:
            db: 数据库管理器（异步）
            monitoring_client: 用于监听消息的客户端（UserBot 或 Bot）
            sending_client: 用于发送消息的客户端（总是 Bot）
            event_bus: 事件总线，用于配置热重载（可选）
        """
        self.db = db
        self.monitoring_client = monitoring_client
        self.sending_client = sending_client
        self._event_bus = event_bus
        self._enabled = False
        self._config = {}
        # 媒体组缓存：{grouped_id: [messages]}
        # 用于收集媒体组的所有消息（Bot无法访问频道历史）
        self._media_group_cache: dict[int, list[Message]] = {}
        # 媒体组缓存锁，防止并发问题
        self._media_group_lock = asyncio.Lock()
        # 外部收集的消息（由事件处理器设置）
        self._external_media_group: dict[int, list[Message]] = {}
        # 下载管理器
        self._download_manager = DownloadManager()

        # 订阅配置变更事件
        if event_bus:
            asyncio.create_task(
                event_bus.subscribe(
                    ConfigChangedEvent,
                    self.on_config_updated,
                    priority=AsyncIOEventBus.PRIORITY_HIGH,
                )
            )
            logger.info("✅ 转发处理器已订阅配置变更事件")

    @property
    def enabled(self) -> bool:
        """转发功能是否启用"""
        return self._enabled

    @enabled.setter
    def enabled(self, value: bool):
        """设置转发功能启用状态"""
        self._enabled = value
        logger.info(f"转发功能状态变更: {'启用' if value else '禁用'}")

    @property
    def config(self) -> dict[str, Any]:
        """获取转发配置"""
        return self._config

    def set_config(self, config: dict[str, Any]):
        """
        设置转发配置

        Args:
            config: 配置字典
        """
        self._config = config
        logger.info(f"转发配置已更新: {len(config.get('rules', []))} 条规则")

    async def on_config_updated(self, event: ConfigChangedEvent):
        """配置更新处理

        当config.json发生变化时，自动更新转发配置

        Args:
            event: 配置变更事件，包含完整的配置字典
        """
        try:
            # 从完整配置中提取转发配置
            forwarding_config = event.config.get("forwarding", {})

            if not forwarding_config:
                logger.warning("⚠️ 配置更新事件中未找到forwarding配置，保持现有配置")
                return

            # 记录配置更新详情
            old_rules_count = len(self._config.get("rules", [])) if self._config else 0
            new_rules_count = len(forwarding_config.get("rules", []))

            # 使用现有的配置更新机制
            self._config = forwarding_config

            logger.info(
                f"✅ 转发配置已热重载: "
                f"版本={event.version}, "
                f"规则数量: {old_rules_count} → {new_rules_count}, "
                f"变更字段: {event.changed_fields if event.changed_fields else '全部'}"
            )

            # 如果有关键词变更，记录详细信息
            if event.changed_fields and "forwarding" in str(event.changed_fields):
                logger.info("🔄 转发规则（关键词）已更新并生效")

        except Exception as e:
            logger.error(f"❌ 处理配置更新失败: {type(e).__name__}: {e}", exc_info=True)
            # 保持现有配置不变

    async def process_message(self, message: "Message") -> bool:
        """
        处理单条消息，决定是否转发

        Args:
            message: Telegram消息对象

        Returns:
            是否成功处理（包括决定不转发的情况）
        """
        if not self._enabled:
            return False

        try:
            # 获取频道信息
            channel_id = None
            if hasattr(message, "chat") and message.chat:
                channel_id = message.chat.username or str(message.chat.id)
            elif hasattr(message, "peer_id") and message.peer_id:
                channel_id = str(message.peer_id.channel_id)

            if not channel_id:
                logger.debug("无法获取频道ID，跳过消息")
                return False

            logger.debug(f"处理转发消息: channel_id={channel_id}, message_id={message.id}")

            # 查找匹配的转发规则
            matched_rules = []
            for rule in self._config.get("rules", []):
                source_url = rule.get("source_channel", "")
                # 从URL中提取频道ID（如 https://t.me/jffnekjdnfn -> jffnekjdnfn）
                rule_channel_id = source_url.rstrip("/").split("/")[-1] if source_url else ""

                # 支持username或数字ID匹配
                if rule_channel_id and (
                    rule_channel_id == channel_id or rule_channel_id in str(channel_id)
                ):
                    matched_rules.append(rule)
                    logger.debug(f"匹配转发规则: {source_url} -> {rule.get('target_channel')}")

            if not matched_rules:
                logger.debug(f"频道 {channel_id} 无匹配的转发规则")
                return False

            # 处理每条匹配的规则
            success_count = 0
            for rule in matched_rules:
                target_channel = rule.get("target_channel")
                if not target_channel:
                    continue

                # 检查是否已转发（使用三字段主键）
                message_id = str(message.id)
                if await self.db.is_message_forwarded(message_id, target_channel, channel_id):
                    logger.debug(f"消息 {message_id} 已转发到 {target_channel}，跳过")
                    continue

                # 应用过滤器
                if not self._should_forward(message, rule):
                    logger.debug(f"消息被规则过滤，不转发到 {target_channel}")
                    continue

                # 执行转发
                if await self._forward_message(message, target_channel, rule):
                    success_count += 1

            return success_count > 0

        except Exception as e:
            logger.error(f"处理消息时出错: {type(e).__name__}: {e}", exc_info=True)
            return False

    def _should_forward(self, message: "Message", rule: dict[str, Any]) -> bool:
        """
        判断是否应该转发消息

        Args:
            message: Telegram消息对象
            rule: 转发规则

        Returns:
            是否应该转发
        """
        # 检查是否只转发原创消息
        forward_original_only = rule.get("forward_original_only", False)
        if not should_forward_original_only(message, forward_original_only):
            from ..i18n import t

            logger.debug(t("forwarding.filter.forward_skipped"))
            return False

        # 检查关键词过滤
        keywords = rule.get("keywords")
        blacklist = rule.get("blacklist")
        if keywords or blacklist:
            if not should_forward_by_keywords(message, keywords, blacklist):
                return False

        # 检查正则表达式过滤
        patterns = rule.get("patterns")
        blacklist_patterns = rule.get("blacklist_patterns")
        if patterns or blacklist_patterns:
            if not should_forward_by_regex(message, patterns, blacklist_patterns):
                return False

        return True

    async def _forward_message(
        self, message: "Message", target_channel: str, rule: dict[str, Any]
    ) -> bool:
        """
        执行消息转发

        Args:
            message: Telegram消息对象
            target_channel: 目标频道
            rule: 转发规则

        Returns:
            是否成功转发
        """
        try:
            # 获取源频道ID
            source_channel = None
            if hasattr(message, "chat") and message.chat:
                source_channel = message.chat.username or str(message.chat.id)
            elif hasattr(message, "peer_id") and message.peer_id:
                source_channel = str(message.peer_id.channel_id)

            # 检查是否是媒体组（相册）的一部分
            grouped_id = getattr(message, "grouped_id", None)

            if grouped_id:
                # 处理媒体组（相册）
                return await self._forward_media_group(
                    message, grouped_id, target_channel, source_channel, rule
                )
            else:
                # 处理单个消息
                return await self._forward_single_message(
                    message, target_channel, source_channel, rule
                )

        except Exception as e:
            logger.error(f"转发消息失败: {type(e).__name__}: {e}", exc_info=True)
            return False

    async def _forward_single_message(
        self, message: "Message", target_channel: str, source_channel: str, rule: dict[str, Any]
    ) -> bool:
        """
        转发单个消息（非媒体组）

        Args:
            message: Telegram消息对象
            target_channel: 目标频道
            source_channel: 源频道ID
            rule: 转发规则

        Returns:
            是否成功转发
        """
        try:
            # 如果有媒体文件，决定转发策略
            if message.media:
                strategy_decision = await decide_forward_strategy([message])
                logger.info(
                    f"单个消息转发策略: {strategy_decision['strategy'].value} - {strategy_decision['reason']}"
                )

                if strategy_decision["strategy"] == ForwardStrategy.DOWNLOAD:
                    # 下载后转发（大文件）
                    return await self._forward_single_with_download(
                        message, target_channel, source_channel, rule
                    )

            # 生成底栏
            footer = await self._generate_footer(message, source_channel, target_channel, rule)

            # 内存转发（小文件或无媒体）- 使用 Bot 发送
            if rule.get("copy_mode", False):
                # 使用复制模式（不显示转发来源）
                caption = message.message or ""
                if footer:
                    caption = f"{caption}\n\n{footer}" if caption else footer

                await self.sending_client.send_message(
                    entity=target_channel,
                    message=caption,
                    file=message.media if message.media else None,
                    link_preview=False,  # 禁用链接预览
                )
            else:
                # 使用转发模式（显示转发来源）
                await self.sending_client.forward_messages(
                    entity=target_channel,
                    messages=message,
                    from_peer=message.chat_id,
                )

            # 记录已转发
            message_id = str(message.id)
            content_hash = self._generate_content_hash(message)
            timestamp = (
                int(message.date.timestamp())
                if message.date
                else int(datetime.now(UTC).timestamp())
            )

            await self.db.add_forwarded_message(
                message_id=message_id,
                source_channel=source_channel,
                target_channel=target_channel,
                content_hash=content_hash,
                timestamp=timestamp,
            )

            # 更新统计
            await self.db.update_forwarding_stats(source_channel, 1)

            logger.info(f"成功转发消息 {message_id} from {source_channel} to {target_channel}")
            return True

        except Exception as e:
            logger.error(f"转发单个消息失败: {type(e).__name__}: {e}", exc_info=True)
            return False

    async def _forward_single_with_download(
        self,
        message: "Message",
        target_channel: str,
        source_channel: str,
        rule: dict[str, Any],
    ) -> bool:
        """
        下载后转换单个消息（用于大文件）

        Args:
            message: Telegram消息对象
            target_channel: 目标频道
            source_channel: 源频道ID
            rule: 转发规则

        Returns:
            是否成功转发
        """
        try:
            message_id_str = str(message.id)
            logger.info(f"开始下载单个文件: message_id={message_id_str}")

            # 下载文件（使用 UserBot/Bot 下载）
            file_path = await self._download_manager.download_media(
                self.monitoring_client, message, "000_media", message_id_str
            )

            if not file_path:
                logger.error(f"文件下载失败: message_id={message_id_str}")
                return False

            logger.info(f"文件下载完成: {file_path}")

            # 生成底栏
            footer = await self._generate_footer(message, source_channel, target_channel, rule)

            # 转发文件（下载管理器已返回绝对路径）- 使用 Bot 发送
            if rule.get("copy_mode", False):
                # 复制模式：发送文件
                caption = message.message or ""
                if footer:
                    caption = f"{caption}\n\n{footer}" if caption else footer

                await self.sending_client.send_file(
                    entity=target_channel,
                    file=file_path,
                    caption=caption,
                    link_preview=False,  # 禁用链接预览
                )
            else:
                # 转发模式：由于已经下载，使用 send_file 并说明来源
                caption = message.message or ""
                if footer:
                    caption = f"{caption}\n\n{footer}" if caption else footer
                else:
                    caption = f"📎 来自: {source_channel}"

                await self.sending_client.send_file(
                    entity=target_channel,
                    file=file_path,
                    caption=caption,
                    link_preview=False,  # 禁用链接预览
                )

            # 清理缓存
            await self._download_manager.cleanup_cache(message_id_str)

            # 记录已转发
            content_hash = self._generate_content_hash(message)
            timestamp = (
                int(message.date.timestamp())
                if message.date
                else int(datetime.now(UTC).timestamp())
            )

            await self.db.add_forwarded_message(
                message_id=message_id_str,
                source_channel=source_channel,
                target_channel=target_channel,
                content_hash=content_hash,
                timestamp=timestamp,
            )

            # 更新统计
            await self.db.update_forwarding_stats(source_channel, 1)

            logger.info(
                f"成功转发单个文件 {message_id_str} from {source_channel} to {target_channel}"
            )
            return True

        except Exception as e:
            logger.error(
                f"下载并转发单个消息失败 {message.id}: {type(e).__name__}: {e}", exc_info=True
            )
            # 清理缓存（即使失败也清理）
            try:
                await self._download_manager.cleanup_cache(str(message.id))
            except Exception:
                pass
            return False

    async def _forward_media_group(
        self,
        message: "Message",
        grouped_id: int,
        target_channel: str,
        source_channel: str,
        rule: dict[str, Any],
    ) -> bool:
        """
        转发媒体组（相册）

        Args:
            message: Telegram消息对象（媒体组的第一条消息）
            grouped_id: 媒体组ID
            target_channel: 目标频道
            source_channel: 源频道ID
            rule: 转发规则

        Returns:
            是否成功转发
        """
        try:
            # 使用grouped_id作为去重键，避免同一组消息被多次转发
            group_key = f"group_{grouped_id}"

            # 检查媒体组是否已转发
            if await self.db.is_message_forwarded(group_key, target_channel, source_channel):
                logger.debug(f"媒体组 {group_key} 已转发到 {target_channel}，跳过")
                return False

            # 获取媒体组的所有消息
            # 注意：我们需要获取这个组内的所有消息
            # 由于Telegram API的限制，我们需要通过聊天历史来获取
            media_group_messages = await self._get_media_group_messages(message, grouped_id)

            if not media_group_messages:
                logger.warning(f"无法获取媒体组 {grouped_id} 的消息")
                return False

            logger.info(f"获取到媒体组 {grouped_id} 的 {len(media_group_messages)} 条消息")

            # 按消息ID排序，确保顺序正确
            media_group_messages.sort(key=lambda m: m.id)

            # 决定转发策略（内存转发 vs 下载后转发）
            strategy_decision = await decide_forward_strategy(media_group_messages)
            logger.info(
                f"媒体组转发策略: {strategy_decision['strategy'].value} - {strategy_decision['reason']}"
            )

            # 执行转发
            if strategy_decision["strategy"] == ForwardStrategy.DOWNLOAD:
                # 下载后转发（大文件）
                return await self._forward_media_group_with_download(
                    media_group_messages, group_key, target_channel, source_channel, rule
                )

            # 内存转发（小文件或纯图片）- 使用 Bot 发送
            if rule.get("copy_mode", False):
                # 复制模式：逐条发送（保持原始顺序）
                captions = []
                files = []

                for msg in media_group_messages:
                    if msg.media:
                        files.append(msg.media)
                        # 收集所有文本，第一张图带文本，其他不带
                        if not captions and msg.message:
                            captions.append(msg.message)
                        else:
                            captions.append("")

                # 生成底栏
                footer = await self._generate_footer(
                    media_group_messages[0], source_channel, target_channel, rule
                )

                # 组合 caption 和 footer
                caption = captions[0] if captions else ""
                if footer:
                    caption = f"{caption}\n\n{footer}" if caption else footer

                # 批量发送媒体组
                await self.sending_client.send_file(
                    entity=target_channel,
                    file=files,
                    caption=caption if caption else None,
                    link_preview=False,  # 禁用链接预览
                )
            else:
                # 转发模式：使用forward_messages批量转发
                await self.sending_client.forward_messages(
                    entity=target_channel,
                    messages=media_group_messages,
                    from_peer=message.chat_id,
                )

            # 记录已转发（使用grouped_id作为消息ID）
            content_hash = self._generate_group_hash(media_group_messages)
            timestamp = (
                int(message.date.timestamp())
                if message.date
                else int(datetime.now(UTC).timestamp())
            )

            await self.db.add_forwarded_message(
                message_id=group_key,
                source_channel=source_channel,
                target_channel=target_channel,
                content_hash=content_hash,
                timestamp=timestamp,
            )

            # 更新统计（媒体组算一次转发）
            await self.db.update_forwarding_stats(source_channel, 1)

            logger.info(
                f"成功转发媒体组 {group_key} ({len(media_group_messages)} 条消息) "
                f"from {source_channel} to {target_channel}"
            )
            return True

        except Exception as e:
            logger.error(f"转发媒体组失败: {type(e).__name__}: {e}", exc_info=True)
            return False

    async def _forward_media_group_with_download(
        self,
        messages: list["Message"],
        group_key: str,
        target_channel: str,
        source_channel: str,
        rule: dict[str, Any],
    ) -> bool:
        """
        下载后转发媒体组（用于大文件）

        Args:
            messages: 媒体组的消息列表
            group_key: 媒体组键
            target_channel: 目标频道
            source_channel: 源频道ID
            rule: 转发规则

        Returns:
            是否成功转发
        """
        try:
            logger.info(f"开始下载媒体组: {group_key}")

            # 下载所有媒体文件（使用 UserBot/Bot 下载）
            file_paths = await self._download_manager.download_media_group(
                self.monitoring_client, messages, group_key
            )

            if not file_paths:
                logger.error(f"媒体组下载失败: {group_key}")
                return False

            logger.info(f"媒体组下载完成: {len(file_paths)} 个文件")

            # 生成底栏
            footer = await self._generate_footer(messages[0], source_channel, target_channel, rule)

            # 转发文件（下载管理器已返回绝对路径）- 使用 Bot 发送
            if rule.get("copy_mode", False):
                # 复制模式：发送文件
                caption = messages[0].message if messages and messages[0].message else ""
                if footer:
                    caption = f"{caption}\n\n{footer}" if caption else footer

                await self.sending_client.send_file(
                    entity=target_channel,
                    file=file_paths,
                    caption=caption,
                    link_preview=False,  # 禁用链接预览
                )
            else:
                # 转发模式：由于已经下载，使用 send_file 并说明来源
                caption = messages[0].message if messages and messages[0].message else ""
                if footer:
                    caption = f"{caption}\n\n{footer}" if caption else footer
                else:
                    caption = f"📎 来自: {source_channel}"

                await self.sending_client.send_file(
                    entity=target_channel,
                    file=file_paths,
                    caption=caption,
                    link_preview=False,  # 禁用链接预览
                )

            # 清理缓存
            await self._download_manager.cleanup_cache(group_key)

            # 记录已转发
            content_hash = self._generate_group_hash(messages)
            timestamp = (
                int(messages[0].date.timestamp())
                if messages[0].date
                else int(datetime.now(UTC).timestamp())
            )

            await self.db.add_forwarded_message(
                message_id=group_key,
                source_channel=source_channel,
                target_channel=target_channel,
                content_hash=content_hash,
                timestamp=timestamp,
            )

            # 更新统计
            await self.db.update_forwarding_stats(source_channel, 1)

            logger.info(
                f"成功转发媒体组 {group_key} ({len(file_paths)} 个文件) "
                f"from {source_channel} to {target_channel}"
            )
            return True

        except Exception as e:
            logger.error(
                f"下载并转发媒体组失败 {group_key}: {type(e).__name__}: {e}", exc_info=True
            )
            # 清理缓存（即使失败也清理）
            try:
                await self._download_manager.cleanup_cache(group_key)
            except Exception:
                pass
            return False

    def set_external_media_group(self, grouped_id: int, messages: list["Message"]):
        """
        设置外部收集的媒体组消息（由事件处理器调用）

        Args:
            grouped_id: 媒体组ID
            messages: 收集的消息列表
        """
        self._external_media_group[grouped_id] = messages
        logger.debug(f"设置外部媒体组: grouped_id={grouped_id}, 消息数={len(messages)}")

    async def _get_media_group_messages(
        self, message: "Message", grouped_id: int
    ) -> list["Message"]:
        """
        获取媒体组的所有消息（优先使用外部收集的消息）

        由于Bot账户无法访问频道历史（GetHistoryRequest受限），
        优先使用事件处理器收集的消息，如果没有则使用内部缓存：
        1. 检查外部收集的消息
        2. 如果没有，使用内部缓存策略
        3. 清理已使用的缓存

        Args:
            message: 媒体组中的一条消息
            grouped_id: 媒体组ID

        Returns:
            媒体组的所有消息列表
        """
        try:
            # 优先使用外部收集的消息
            if grouped_id in self._external_media_group:
                media_group_messages = self._external_media_group[grouped_id].copy()
                # 清理外部缓存
                del self._external_media_group[grouped_id]
                logger.info(
                    f"使用外部收集的媒体组: grouped_id={grouped_id}, "
                    f"消息数={len(media_group_messages)}"
                )
                return media_group_messages

            # 如果没有外部消息，使用内部缓存（备用方案）
            async with self._media_group_lock:
                # 将当前消息添加到缓存
                if grouped_id not in self._media_group_cache:
                    self._media_group_cache[grouped_id] = []

                # 检查消息是否已在缓存中（防止重复添加）
                message_ids = [msg.id for msg in self._media_group_cache[grouped_id]]
                if message.id not in message_ids:
                    self._media_group_cache[grouped_id].append(message)
                    logger.debug(
                        f"添加消息到媒体组缓存: grouped_id={grouped_id}, "
                        f"message_id={message.id}, 缓存大小={len(self._media_group_cache[grouped_id])}"
                    )

            # 等待一小段时间，让同组的其他消息到达
            # 媒体组的消息通常会在很短时间内连续到达
            await asyncio.sleep(0.5)

            # 再次检查缓存，看是否有更多消息到达
            async with self._media_group_lock:
                media_group_messages = self._media_group_cache.get(grouped_id, []).copy()

                # 清理缓存（避免内存泄漏）
                if grouped_id in self._media_group_cache:
                    del self._media_group_cache[grouped_id]
                    logger.debug(f"清理媒体组缓存: grouped_id={grouped_id}")

            if media_group_messages:
                logger.info(
                    f"从缓存获取媒体组: grouped_id={grouped_id}, 消息数={len(media_group_messages)}"
                )

            return media_group_messages

        except Exception as e:
            logger.error(f"获取媒体组消息失败: {type(e).__name__}: {e}", exc_info=True)
            # 发生异常时，返回至少当前消息
            return [message]

    def _generate_group_hash(self, messages: list["Message"]) -> str:
        """
        生成媒体组内容哈希（用于去重）

        Args:
            messages: 媒体组的消息列表

        Returns:
            内容哈希字符串
        """
        # 合并所有消息的文本
        content = "".join([msg.message or "" for msg in messages])
        # 生成哈希
        return hashlib.md5(content.encode("utf-8")).hexdigest()[:16]

    def _generate_content_hash(self, message: "Message") -> str:
        """
        生成消息内容哈希（用于去重）

        Args:
            message: Telegram消息对象

        Returns:
            内容哈希字符串
        """
        content = message.message or ""
        # 简单的哈希算法
        return hashlib.md5(content.encode("utf-8")).hexdigest()[:16]

    async def _generate_footer(
        self,
        message: "Message",
        source_channel: str,
        target_channel: str,
        rule: dict[str, Any],
    ) -> str:
        """
        生成转发消息底栏

        支持三种模式：
        1. 自定义底栏模式：规则设置了 custom_footer 时，使用自定义模板
        2. 默认底栏模式：未设置自定义底栏时，使用格式 [Source](链接) @频道
        3. 隐藏底栏模式：全局配置 show_default_footer=False 时，不添加任何底栏

        支持的占位符：
        - {source_link}: 源消息链接
        - {source_title}: 源频道名称
        - {target_title}: 目标频道名称
        - {source_channel}: 源频道ID
        - {target_channel}: 目标频道ID
        - {message_id}: 消息ID

        Args:
            message: Telegram消息对象
            source_channel: 源频道ID
            target_channel: 目标频道ID
            rule: 转发规则

        Returns:
            格式化的底栏文本
        """
        try:
            # 获取源频道信息（使用监听客户端）
            source_entity = await self.monitoring_client.get_entity(message.chat_id)
            source_username = getattr(source_entity, "username", None)
            source_title = getattr(source_entity, "title", "Unknown")

            # 生成源消息链接
            if source_username:
                source_link = f"https://t.me/{source_username}/{message.id}"
            else:
                # 私有频道使用数字ID（去掉负号）
                source_link = f"https://t.me/c/{abs(message.chat_id)}/{message.id}"

            # 获取目标频道信息（使用监听客户端）
            try:
                target_entity = await self.monitoring_client.get_entity(target_channel)
                target_title = getattr(target_entity, "title", target_channel)
                target_username = getattr(target_entity, "username", None)
            except Exception as e:
                logger.warning(f"获取目标频道信息失败: {e}")
                target_title = target_channel
                target_username = None

            # 模式1：自定义底栏（优先级最高）
            custom_footer = rule.get("custom_footer", "").strip()
            if custom_footer:
                # 安全替换占位符（使用 .replace() 避免 str.format() 的大括号冲突）
                mapping = {
                    "{source_link}": source_link,
                    "{source_title}": source_title,
                    "{target_title}": target_title,
                    "{source_channel}": source_channel,
                    "{target_channel}": target_username if target_username else target_channel,
                    "{message_id}": str(message.id),
                }

                footer_text = custom_footer
                for placeholder, value in mapping.items():
                    footer_text = footer_text.replace(placeholder, str(value))

                return footer_text

            # 模式2：检查全局配置是否禁用默认底栏
            if not self._config.get("show_default_footer", True):
                return ""

            # 模式3：使用默认格式
            if target_username:
                return f"[Source]({source_link}) @{target_username}"
            else:
                return f"[Source]({source_link}) {target_channel}"

        except Exception as e:
            logger.error(f"生成底栏失败: {type(e).__name__}: {e}", exc_info=True)
            return ""

    async def get_statistics(self, channel_id: str = None) -> dict[str, Any]:
        """
        获取转发统计信息

        Args:
            channel_id: 可选，频道URL

        Returns:
            统计信息字典
        """
        try:
            return await self.db.get_forwarding_stats(channel_id)
        except Exception as e:
            logger.error(f"获取转发统计失败: {type(e).__name__}: {e}", exc_info=True)
            return {}

    async def cleanup_old_records(self, days: int = 30) -> int:
        """
        清理旧的转发记录

        Args:
            days: 保留天数

        Returns:
            删除的记录数
        """
        try:
            return await self.db.cleanup_old_forwarded_messages(days)
        except Exception as e:
            logger.error(f"清理旧记录失败: {type(e).__name__}: {e}", exc_info=True)
            return 0


# 全局转发处理器实例
_forwarding_handler: ForwardingHandler | None = None


def get_forwarding_handler() -> ForwardingHandler | None:
    """
    获取全局转发处理器实例

    Returns:
        ForwardingHandler实例，如果未初始化则返回None
    """
    return _forwarding_handler


def set_forwarding_handler(handler: ForwardingHandler):
    """
    设置全局转发处理器实例

    Args:
        handler: ForwardingHandler实例
    """
    global _forwarding_handler
    _forwarding_handler = handler
    logger.info("全局转发处理器已设置")
