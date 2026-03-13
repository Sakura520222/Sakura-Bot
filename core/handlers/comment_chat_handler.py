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
频道评论区AI聊天事件处理模块
处理评论区消息的AI唤醒和响应
"""

import asyncio
import logging
import random
from collections import defaultdict
from datetime import UTC, datetime, timedelta
from typing import Any

from telethon import events
from telethon.errors import FloodWaitError

from core.ai.comment_chat_engine import get_comment_chat_engine
from core.ai.comment_context import build_comment_context
from core.config import CHANNELS
from core.handlers.channel_comment_welcome import is_channel_in_whitelist
from core.handlers.comment_chat_config import get_comment_chat_config
from core.system.error_handler import record_error

logger = logging.getLogger(__name__)

# 防抖缓存：{discussion_id: {user_id: {"messages": [], "timer": task}}}
_debounce_cache: dict[int, dict[int, dict]] = {}
_debounce_lock = asyncio.Lock()

# 转发消息锚点缓存：{discussion_id: {forward_msg_id: channel_id}}
_forward_anchor_cache: dict[int, dict[int, str]] = {}

# 评论消息缓存：{discussion_id: {forward_msg_id: [comment_data, ...]}
# 用于存储Bot实时接收到的评论区消息（Bot无法获取历史消息）
_comment_cache: dict[int, dict[int, list]] = defaultdict(lambda: defaultdict(list))
_comment_cache_lock = asyncio.Lock()


def get_cached_comments(discussion_id: int, forward_msg_id: int) -> list[dict]:
    """获取缓存的评论消息"""
    return list(_comment_cache.get(discussion_id, {}).get(forward_msg_id, []))


async def add_cached_comment(
    discussion_id: int,
    forward_msg_id: int,
    user_id: int,
    username: str,
    text: str,
):
    """添加评论到缓存"""
    async with _comment_cache_lock:
        _comment_cache[discussion_id][forward_msg_id].append(
            {
                "user_id": user_id,
                "username": username,
                "text": text,
                "timestamp": datetime.now(UTC),
            }
        )


async def cleanup_comment_cache(discussion_id: int = None, max_age_hours: int = 24):
    """清理过期的评论缓存"""
    async with _comment_cache_lock:
        cutoff = datetime.now(UTC) - timedelta(hours=max_age_hours)

        if discussion_id is not None:
            # 清理特定讨论组的缓存
            if discussion_id in _comment_cache:
                for forward_msg_id, comments in list(_comment_cache[discussion_id].items()):
                    # 过滤掉过期消息
                    _comment_cache[discussion_id][forward_msg_id] = [
                        c for c in comments if c.get("timestamp", datetime.min) > cutoff
                    ]
                    # 移除空列表
                    if not _comment_cache[discussion_id][forward_msg_id]:
                        del _comment_cache[discussion_id][forward_msg_id]
                # 移除空的讨论组
                if not _comment_cache[discussion_id]:
                    del _comment_cache[discussion_id]
        else:
            # 清理所有缓存
            for did in list(_comment_cache.keys()):
                for fid in list(_comment_cache[did].keys()):
                    _comment_cache[did][fid] = [
                        c
                        for c in _comment_cache[did][fid]
                        if c.get("timestamp", datetime.min) > cutoff
                    ]
                    if not _comment_cache[did][fid]:
                        del _comment_cache[did][fid]
                if not _comment_cache[did]:
                    del _comment_cache[did]


class CommentChatHandler:
    """评论区AI聊天处理器"""

    def __init__(self, client, rate_limit_delay=(1, 3), worker_count=1):
        """
        初始化处理器

        Args:
            client: Telegram客户端实例
            rate_limit_delay: 限流延迟范围（秒），默认1-3秒随机
            worker_count: Worker数量，默认1个
        """
        self.client = client
        self.rate_limit_delay = rate_limit_delay
        self.worker_count = worker_count
        self.task_queue: asyncio.Queue = asyncio.Queue()
        self.workers: list[asyncio.Task] = []
        self.is_running = False
        self.engine = None

    async def start(self):
        """启动处理器（创建Workers）"""
        if self.is_running:
            logger.warning("CommentChatHandler已在运行中")
            return

        logger.info("启动CommentChatHandler...")

        # 初始化AI引擎
        self.engine = get_comment_chat_engine()

        # 启动Workers
        for i in range(self.worker_count):
            worker_task = asyncio.create_task(self._worker(worker_id=i))
            self.workers.append(worker_task)
            logger.info(f"已启动Worker-{i}")

        self.is_running = True
        logger.info(f"CommentChatHandler启动完成，Worker数量: {self.worker_count}")

    async def stop(self):
        """停止处理器"""
        if not self.is_running:
            return

        logger.info("正在停止CommentChatHandler...")
        self.is_running = False

        # 停止Workers
        for worker in self.workers:
            worker.cancel()
        await asyncio.gather(*self.workers, return_exceptions=True)
        self.workers.clear()

        # 清理防抖缓存
        async with _debounce_lock:
            for discussion in _debounce_cache.values():
                for user_data in discussion.values():
                    if "timer" in user_data and user_data["timer"]:
                        user_data["timer"].cancel()
            _debounce_cache.clear()

        logger.info("CommentChatHandler已停止")

    async def _worker(self, worker_id: int):
        """
        异步Worker，处理AI回复（带限流）

        Args:
            worker_id: Worker标识ID
        """
        logger.info(f"Worker-{worker_id}启动")

        while self.is_running:
            task_data = None
            try:
                # 从队列获取任务（阻塞等待，带超时避免无法退出）
                task_data = await asyncio.wait_for(self.task_queue.get(), timeout=5.0)

                if task_data is None:
                    # None表示停止信号
                    break

                # 随机延迟，模拟人类操作，防止FloodWait
                delay = random.uniform(*self.rate_limit_delay)
                await asyncio.sleep(delay)

                # 执行AI回复
                await self._process_ai_reply(task_data)

            except TimeoutError:
                # 超时是正常的，继续下一次循环
                continue
            except Exception as e:
                logger.error(
                    f"Worker-{worker_id}处理任务时出错: {type(e).__name__}: {e}",
                    exc_info=True,
                )
            finally:
                # 确保即使出错也要标记任务完成，防止队列挂起
                if task_data is not None:
                    self.task_queue.task_done()

        logger.info(f"Worker-{worker_id}已停止")

    async def _process_ai_reply(self, task_data: dict[str, Any]):
        """
        处理AI回复

        Args:
            task_data: 任务数据
        """
        try:
            discussion_id = task_data["discussion_id"]
            forward_msg_id = task_data["forward_msg_id"]
            comment_msg_ids = task_data["comment_msg_ids"]
            user_id = task_data["user_id"]
            username = task_data["username"]
            aggregated_messages = task_data["aggregated_messages"]

            logger.info(
                f"处理AI回复: discussion={discussion_id}, user={username}, "
                f"messages={len(aggregated_messages)}"
            )

            # 构建评论上下文
            comment_context = await build_comment_context(
                self.client,
                discussion_id,
                forward_msg_id,
            )

            if not comment_context.channel_id:
                logger.warning("无法构建评论上下文，跳过")
                return

            # 处理查询（使用聚合的消息）
            # 如果有多条消息，用换行连接
            user_query = "\n".join(aggregated_messages)

            # 调用AI引擎
            answer = await self.engine.process_comment_query(
                comment_context=comment_context,
                user_query=user_query,
                user_id=user_id,
                username=username,
            )

            # 发送回复（回复最后一条评论消息）
            last_msg_id = comment_msg_ids[-1]
            await self.client.send_message(
                discussion_id,
                answer,
                reply_to=last_msg_id,
            )

            logger.info(f"✅ 已在讨论组 {discussion_id} 发送AI回复")

        except FloodWaitError as e:
            # 捕获FloodWait错误，等待指定时间后重试
            wait_seconds = e.seconds
            logger.warning(f"触发FloodWait，需要等待 {wait_seconds} 秒")
            await asyncio.sleep(wait_seconds)

            # 重新加入队列
            await self.task_queue.put(task_data)

        except Exception as e:
            record_error(e, "process_ai_reply")
            logger.error(
                f"处理AI回复失败: {type(e).__name__}: {e}",
                exc_info=True,
            )

    async def handle_new_message(self, event: events.NewMessage.Event):
        """
        处理讨论组新消息

        判断流程：
        1. 检查是否在监控的频道列表
        2. 检查消息是否为转发消息（记录锚点）
        3. 如果是普通消息，检查是否为回复转发消息
        4. 检查是否包含唤醒关键词
        5. 使用防抖机制聚合用户消息

        Args:
            event: Telethon NewMessage事件
        """
        try:
            msg = event.message

            # 1. 检查是否为转发消息（记录锚点）
            if hasattr(msg, "fwd_from") and msg.fwd_from:
                await self._register_forward_anchor(msg)
                return

            # 2. 检查是否为回复消息
            if not msg.reply_to:
                return

            reply_to_msg_id = msg.reply_to.reply_to_msg_id
            if not reply_to_msg_id:
                return

            # 3. 检查是否为回复转发消息
            channel_id = await self._get_channel_for_forward_anchor(msg.chat_id, reply_to_msg_id)
            if not channel_id:
                return

            # 4. 检查是否在白名单中
            if not is_channel_in_whitelist(channel_id, CHANNELS):
                logger.debug(f"频道 {channel_id} 不在白名单中，忽略")
                return

            # 4.5. 记录评论到缓存（无论是否包含唤醒关键词）
            username = await self._get_username(msg)
            await add_cached_comment(
                discussion_id=msg.chat_id,
                forward_msg_id=reply_to_msg_id,
                user_id=msg.sender_id or 0,
                username=username,
                text=msg.message or "",
            )

            # 5. 检查是否包含唤醒关键词
            config = await get_comment_chat_config()
            if not config.get("enabled", True):
                return

            wake_keyword = config.get("wake_keyword", "小樱")
            message_text = msg.message or ""

            if wake_keyword.lower() not in message_text.lower():
                return

            logger.info(
                f"检测到唤醒关键词 '{wake_keyword}': discussion={msg.chat_id}, user={msg.sender_id}"
            )

            # 6. 使用防抖机制聚合用户消息
            await self._handle_debounced_message(
                discussion_id=msg.chat_id,
                forward_msg_id=reply_to_msg_id,
                comment_msg_id=msg.id,
                user_id=msg.sender_id or 0,  # 处理匿名用户
                username=await self._get_username(msg),
                message_text=message_text,
                debounce_seconds=config.get("debounce_seconds", 3),
            )

        except Exception as e:
            record_error(e, "handle_new_message")
            logger.error(
                f"处理讨论组消息时出错: {type(e).__name__}: {e}",
                exc_info=True,
            )

    async def _register_forward_anchor(self, msg):
        """
        注册转发消息锚点

        Args:
            msg: 转发消息
        """
        try:
            fwd_from = msg.fwd_from

            if not (
                hasattr(fwd_from, "from_id")
                and fwd_from.from_id
                and hasattr(fwd_from.from_id, "channel_id")
            ):
                return

            channel_id_num = fwd_from.from_id.channel_id

            # 获取频道标识
            channel_identifier = str(channel_id_num)
            try:
                channel_entity = await self.client.get_entity(channel_id_num)
                if hasattr(channel_entity, "username") and channel_entity.username:
                    channel_identifier = channel_entity.username
            except Exception as e:
                logger.debug(f"获取频道实体失败: {e}")

            # 注册锚点
            if msg.chat_id not in _forward_anchor_cache:
                _forward_anchor_cache[msg.chat_id] = {}

            _forward_anchor_cache[msg.chat_id][msg.id] = channel_identifier

            # 设置过期清理（1小时后删除）
            asyncio.create_task(self._cleanup_forward_anchor(msg.chat_id, msg.id, 3600))

        except Exception as e:
            logger.error(f"注册转发消息锚点失败: {e}")

    async def _get_channel_for_forward_anchor(
        self, discussion_id: int, forward_msg_id: int
    ) -> str | None:
        """
        获取转发消息对应的频道ID

        Args:
            discussion_id: 讨论组ID
            forward_msg_id: 转发消息ID

        Returns:
            频道ID，如果不是转发消息则返回None
        """
        if discussion_id not in _forward_anchor_cache:
            return None

        return _forward_anchor_cache[discussion_id].get(forward_msg_id)

    async def _cleanup_forward_anchor(self, discussion_id: int, forward_msg_id: int, delay: int):
        """
        清理过期的转发消息锚点

        Args:
            discussion_id: 讨论组ID
            forward_msg_id: 转发消息ID
            delay: 延迟秒数
        """
        await asyncio.sleep(delay)

        if discussion_id in _forward_anchor_cache:
            if forward_msg_id in _forward_anchor_cache[discussion_id]:
                del _forward_anchor_cache[discussion_id][forward_msg_id]

            if not _forward_anchor_cache[discussion_id]:
                del _forward_anchor_cache[discussion_id]

    async def _handle_debounced_message(
        self,
        discussion_id: int,
        forward_msg_id: int,
        comment_msg_id: int,
        user_id: int,
        username: str,
        message_text: str,
        debounce_seconds: int,
    ):
        """
        处理带防抖的消息

        Args:
            discussion_id: 讨论组ID
            forward_msg_id: 转发消息ID
            comment_msg_id: 评论消息ID
            user_id: 用户ID
            username: 用户名
            message_text: 消息文本
            debounce_seconds: 防抖秒数
        """
        async with _debounce_lock:
            # 确保讨论组和用户在缓存中
            if discussion_id not in _debounce_cache:
                _debounce_cache[discussion_id] = {}

            if user_id not in _debounce_cache[discussion_id]:
                _debounce_cache[discussion_id][user_id] = {
                    "messages": [],
                    "msg_ids": [],
                    "timer": None,
                }

            user_data = _debounce_cache[discussion_id][user_id]

            # 添加消息
            user_data["messages"].append(message_text)
            user_data["msg_ids"].append(comment_msg_id)

            # 取消之前的定时器
            if user_data["timer"] and not user_data["timer"].done():
                user_data["timer"].cancel()

            # 创建新的定时器
            async def process_aggregated():
                await asyncio.sleep(debounce_seconds)
                await self._flush_debounced_messages(
                    discussion_id, forward_msg_id, user_id, username
                )

            user_data["timer"] = asyncio.create_task(process_aggregated())

    async def _flush_debounced_messages(
        self,
        discussion_id: int,
        forward_msg_id: int,
        user_id: int,
        username: str,
    ):
        """
        刷新防抖缓存中的消息到处理队列

        Args:
            discussion_id: 讨论组ID
            forward_msg_id: 转发消息ID
            user_id: 用户ID
            username: 用户名
        """
        async with _debounce_lock:
            if discussion_id not in _debounce_cache:
                return

            if user_id not in _debounce_cache[discussion_id]:
                return

            user_data = _debounce_cache[discussion_id][user_id]

            if not user_data["messages"]:
                return

            # 取出消息
            messages = user_data["messages"].copy()
            msg_ids = user_data["msg_ids"].copy()

            # 清空缓存
            user_data["messages"] = []
            user_data["msg_ids"] = []

        # 加入处理队列（在锁外执行，避免阻塞）
        await self.task_queue.put(
            {
                "discussion_id": discussion_id,
                "forward_msg_id": forward_msg_id,
                "comment_msg_ids": msg_ids,
                "user_id": user_id,
                "username": username,
                "aggregated_messages": messages,
            }
        )

        logger.info(
            f"已加入处理队列: discussion={discussion_id}, user={username}, "
            f"aggregated={len(messages)}条消息"
        )

    async def _get_username(self, msg) -> str:
        """
        获取用户名

        Args:
            msg: 消息对象

        Returns:
            用户名
        """
        try:
            user_id = msg.sender_id or 0  # 处理匿名用户
            sender = await msg.get_sender()

            if sender:
                if hasattr(sender, "username") and sender.username:
                    return sender.username
                elif hasattr(sender, "first_name") and sender.first_name:
                    if hasattr(sender, "last_name") and sender.last_name:
                        return f"{sender.first_name} {sender.last_name}"
                    return sender.first_name

            return f"User_{user_id}" if user_id else "匿名"

        except Exception:
            return "匿名"


# 全局实例
_comment_chat_handler: CommentChatHandler | None = None


def get_comment_chat_handler():
    """获取全局CommentChatHandler实例"""
    return _comment_chat_handler


async def initialize_comment_chat(client, rate_limit_delay=(1, 3), worker_count=1):
    """
    初始化频道评论区AI聊天功能

    Args:
        client: Telegram客户端实例
        rate_limit_delay: 限流延迟范围（秒），默认1-3秒随机
        worker_count: Worker数量，默认1个

    Returns:
        CommentChatHandler实例
    """
    global _comment_chat_handler

    if _comment_chat_handler is not None:
        logger.warning("CommentChatHandler已存在，跳过初始化")
        return _comment_chat_handler

    logger.info("初始化频道评论区AI聊天功能...")

    # 创建处理器
    handler = CommentChatHandler(
        client=client,
        rate_limit_delay=rate_limit_delay,
        worker_count=worker_count,
    )

    # 启动处理器
    await handler.start()

    # 保存全局实例
    _comment_chat_handler = handler

    logger.info("频道评论区AI聊天功能初始化完成")
    return handler


async def shutdown_comment_chat():
    """关闭频道评论区AI聊天功能"""
    global _comment_chat_handler

    if _comment_chat_handler is None:
        return

    logger.info("正在关闭频道评论区AI聊天功能...")
    await _comment_chat_handler.stop()
    _comment_chat_handler = None
    logger.info("频道评论区AI聊天功能已关闭")
