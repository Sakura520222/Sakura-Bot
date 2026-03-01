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
转发处理器模块

提供Telegram频道消息转发的核心功能
"""

import hashlib
import logging
from datetime import UTC, datetime
from typing import TYPE_CHECKING, Any

from .filters import should_forward_by_keywords, should_forward_by_regex

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

    def __init__(self, db: "DatabaseManagerBase", client: "TelegramClient"):
        """
        初始化转发处理器

        Args:
            db: 数据库管理器（异步）
            client: Telegram客户端
        """
        self.db = db
        self.client = client
        self._enabled = False
        self._config = {}

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

            # 执行转发
            if rule.get("copy_mode", False):
                # 使用复制模式（不显示转发来源）
                await self.client.send_message(
                    entity=target_channel,
                    message=message.message,
                    file=message.media if message.media else None,
                )
            else:
                # 使用转发模式（显示转发来源）
                await self.client.forward_messages(
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
            logger.error(f"转发消息失败: {type(e).__name__}: {e}", exc_info=True)
            return False

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
