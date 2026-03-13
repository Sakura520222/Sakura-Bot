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
频道评论区上下文构建模块
负责获取和格式化评论区的对话上下文
"""

import logging
from dataclasses import dataclass, field
from datetime import datetime

logger = logging.getLogger(__name__)


@dataclass
class CommentContext:
    """评论区上下文数据类"""

    channel_id: str  # 频道ID
    channel_msg_id: int  # 频道消息ID
    discussion_id: int  # 讨论组ID
    forward_msg_id: int  # 转发消息ID（讨论组中的锚点消息）

    # 母频道消息内容
    original_post: str = ""  # 原始频道消息文本

    # 评论列表
    comments: list[dict] = field(default_factory=list)
    # 格式: [{"user_id": int, "username": str, "text": str, "timestamp": datetime}, ...]

    # 频道名称（可选）
    channel_name: str = ""


async def build_comment_context(
    client,
    discussion_id: int,
    forward_msg_id: int,
    max_messages: int = 50,
) -> CommentContext:
    """
    构建评论区的完整对话上下文

    核心逻辑：
    1. 从转发消息获取频道ID和原始消息ID
    2. 获取原始频道消息内容
    3. 向上爬取讨论组中该转发消息下方的所有回复
    4. 区分母频道消息（通过fwd_from识别）和用户评论
    5. 提取用户昵称

    Args:
        client: Telegram客户端实例
        discussion_id: 讨论组ID
        forward_msg_id: 转发消息ID
        max_messages: 最多获取的消息数量

    Returns:
        CommentContext: 评论上下文对象
    """
    try:
        # 1. 获取转发消息详情
        forward_msg = await client.get_messages(discussion_id, ids=forward_msg_id)
        if not forward_msg:
            logger.warning(
                f"未找到转发消息: discussion_id={discussion_id}, msg_id={forward_msg_id}"
            )
            return CommentContext(
                channel_id="",
                channel_msg_id=0,
                discussion_id=discussion_id,
                forward_msg_id=forward_msg_id,
            )

        # 检查是否为转发消息
        if not hasattr(forward_msg, "fwd_from") or not forward_msg.fwd_from:
            logger.warning(f"消息 {forward_msg_id} 不是转发消息")
            return CommentContext(
                channel_id="",
                channel_msg_id=0,
                discussion_id=discussion_id,
                forward_msg_id=forward_msg_id,
            )

        fwd_from = forward_msg.fwd_from

        # 检查转发来源是否为频道
        if not (
            hasattr(fwd_from, "from_id")
            and fwd_from.from_id
            and hasattr(fwd_from.from_id, "channel_id")
        ):
            logger.warning(f"消息 {forward_msg_id} 不是从频道转发的")
            return CommentContext(
                channel_id="",
                channel_msg_id=0,
                discussion_id=discussion_id,
                forward_msg_id=forward_msg_id,
            )

        channel_id_num = fwd_from.from_id.channel_id
        channel_post_id = fwd_from.channel_post

        if not channel_post_id:
            logger.warning("转发消息缺少 channel_post_id")
            return CommentContext(
                channel_id="",
                channel_msg_id=0,
                discussion_id=discussion_id,
                forward_msg_id=forward_msg_id,
            )

        # 获取频道实体以获取username和频道名称
        channel_identifier = str(channel_id_num)
        channel_name = ""
        try:
            channel_entity = await client.get_entity(channel_id_num)
            if hasattr(channel_entity, "username") and channel_entity.username:
                channel_identifier = channel_entity.username
            if hasattr(channel_entity, "title") and channel_entity.title:
                channel_name = channel_entity.title
        except Exception as e:
            logger.warning(f"获取频道实体失败: {e}")

        # 2. 获取原始频道消息内容
        original_post = ""
        try:
            if channel_entity:
                original_channel_msg = await client.get_messages(
                    channel_entity, ids=channel_post_id
                )
                if original_channel_msg:
                    original_post = original_channel_msg.message or ""
        except Exception as e:
            logger.warning(f"获取原始频道消息失败: {e}")

        # 3. 获取讨论组中转发消息下方的所有回复
        # Bot用户无法获取历史消息，使用内存缓存
        comments = []

        # 从全局缓存中获取评论（需要预先记录）
        from core.handlers.comment_chat_handler import get_cached_comments

        cached_comments = get_cached_comments(discussion_id, forward_msg_id)
        for cached in cached_comments:
            comments.append(
                {
                    "user_id": cached.get("user_id", 0),
                    "username": cached.get("username", "Unknown"),
                    "text": cached.get("text", ""),
                    "timestamp": cached.get("timestamp", datetime.now()),
                }
            )

        return CommentContext(
            channel_id=channel_identifier,
            channel_msg_id=channel_post_id,
            discussion_id=discussion_id,
            forward_msg_id=forward_msg_id,
            original_post=original_post,
            comments=comments,
            channel_name=channel_name,
        )

    except Exception as e:
        logger.error(f"构建评论上下文失败: {e}", exc_info=True)
        return CommentContext(
            channel_id="",
            channel_msg_id=0,
            discussion_id=discussion_id,
            forward_msg_id=forward_msg_id,
        )


def format_comment_context_for_ai(context: CommentContext, include_wakeup: bool = True) -> str:
    """
    将评论上下文格式化为AI可理解的对话流格式

    输出格式：
    【频道消息】
    xxxxx

    【评论区】
    用户A: xxxxx
    用户B: xxxxx
    ...

    Args:
        context: 评论上下文对象
        include_wakeup: 是否包含唤醒提示（暂未使用）

    Returns:
        str: 格式化的对话流文本
    """
    lines = []

    # 母频道消息
    lines.append("【频道消息】")
    if context.channel_name:
        lines.append(f"频道: {context.channel_name}")
    if context.original_post:
        lines.append(context.original_post)
    else:
        lines.append("(无文本内容)")
    lines.append("")

    # 评论区
    if context.comments:
        lines.append("【评论区】")
        for comment in context.comments:
            text = comment["text"] or "(媒体或空消息)"
            lines.append(f"{comment['username']}: {text}")
    else:
        lines.append("【评论区】(暂无评论)")

    return "\n".join(lines)


def get_comment_summary(context: CommentContext) -> str:
    """
    获取评论区的简要摘要（用于日志或调试）

    Args:
        context: 评论上下文对象

    Returns:
        str: 简要摘要
    """
    return (
        f"频道: {context.channel_id}, "
        f"消息ID: {context.channel_msg_id}, "
        f"讨论组: {context.discussion_id}, "
        f"评论数: {len(context.comments)}"
    )
