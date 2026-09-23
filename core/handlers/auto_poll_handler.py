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
自动趣味投票处理器 - 异步队列处理频道消息并生成趣味投票

监听讨论组中的频道转发消息，通过 asyncio.Queue 异步调用 AI 生成投票并发送到讨论组。
复用 RealtimeRAGHandler 的队列模式：有界队列 + 后台 worker + 背压丢弃。
"""

import asyncio
import logging
from collections import OrderedDict
from datetime import UTC, datetime, timedelta
from typing import Any

from telethon.tl.types import InputMediaPoll, Poll, PollAnswer, TextWithEntities

import core.config as config_module
from core.ai.ai_client import generate_poll_from_summary
from core.config import (
    is_auto_poll_enabled_for_channel,
)
from core.i18n.i18n import get_text
from core.system.error_handler import record_error

logger = logging.getLogger(__name__)

# 队列最大容量，防止内存无限增长
QUEUE_MAX_SIZE = 500

# 去重缓存 TTL
_CACHE_TTL = timedelta(hours=1)
# 去重缓存最大条目数（防止内存无限增长）
_CACHE_MAX_SIZE = 10000


class AutoPollHandler:
    """自动趣味投票处理器 - 异步队列处理频道消息并生成趣味投票"""

    def __init__(self, client):
        """初始化处理器

        Args:
            client: Telegram客户端实例
        """
        self._queue: asyncio.Queue[dict[str, Any]] = asyncio.Queue(maxsize=QUEUE_MAX_SIZE)
        self._running = False
        self._start_stop_lock = asyncio.Lock()  # 保护 start/stop 的并发安全
        self._worker_task: asyncio.Task | None = None
        self._processed_count = 0
        self._failed_count = 0
        self.client = client

        # 去重缓存（OrderedDict 实现 LRU 淘汰）
        self._poll_cache: OrderedDict[str, datetime] = OrderedDict()
        self._cache_lock = asyncio.Lock()
        self._cleanup_task: asyncio.Task | None = None

        logger.info("自动趣味投票处理器已创建")

    async def start(self) -> None:
        """启动后台 worker 任务"""
        async with self._start_stop_lock:
            if self._running:
                logger.warning("自动趣味投票处理器已在运行")
                return

            self._running = True
            self._worker_task = asyncio.create_task(self._poll_worker())
            self._cleanup_task = asyncio.create_task(self._periodic_cache_cleanup())
            logger.info("自动趣味投票处理器已启动")

    async def stop(self) -> None:
        """停止后台 worker 任务"""
        async with self._start_stop_lock:
            if not self._running:
                return

            self._running = False

        # 等待队列清空
        remaining = self._queue.qsize()
        if remaining > 0:
            logger.info(f"自动趣味投票处理器停止前处理剩余 {remaining} 个任务...")
            try:
                await asyncio.wait_for(self._queue.join(), timeout=30.0)
            except TimeoutError:
                logger.warning(f"自动趣味投票处理器停止超时，丢弃 {self._queue.qsize()} 个任务")

        if self._worker_task and not self._worker_task.done():
            self._worker_task.cancel()
            try:
                await self._worker_task
            except asyncio.CancelledError:
                pass

        if self._cleanup_task and not self._cleanup_task.done():
            self._cleanup_task.cancel()
            try:
                await self._cleanup_task
            except asyncio.CancelledError:
                pass

        logger.info(
            f"自动趣味投票处理器已停止，共处理 {self._processed_count} 个，"
            f"失败 {self._failed_count} 个"
        )

    def enqueue_poll_task(
        self,
        channel_id: str,
        discussion_id: int,
        forward_msg_id: int,
        message_text: str,
    ) -> bool:
        """将趣味投票任务加入处理队列

        Args:
            channel_id: 频道标识符
            discussion_id: 讨论组ID
            forward_msg_id: 转发消息ID（用于 reply_to）
            message_text: 消息文本内容（用于 AI 生成投票）

        Returns:
            是否成功入队
        """
        if not message_text or not message_text.strip():
            return False

        if not self._running:
            logger.warning("自动趣味投票处理器未运行，无法入队")
            return False

        if not is_auto_poll_enabled_for_channel(channel_id):
            logger.info(f"频道 {channel_id} 的自动趣味投票未启用，跳过入队")
            return False

        # 去重检查（同时清理过期条目）
        cache_key = f"{discussion_id}_{forward_msg_id}"
        if cache_key in self._poll_cache:
            cached_time = self._poll_cache[cache_key]
            # 实时检查是否过期，过期则移除并允许重新入队
            if datetime.now(UTC) - cached_time < _CACHE_TTL:
                logger.info(f"趣味投票任务 {cache_key} 已在缓存中，跳过")
                return False
            # 过期条目，移除
            del self._poll_cache[cache_key]

        try:
            self._queue.put_nowait(
                {
                    "channel_id": channel_id,
                    "discussion_id": discussion_id,
                    "forward_msg_id": forward_msg_id,
                    "message_text": message_text.strip(),
                    "created_at": datetime.now(UTC).isoformat(),
                }
            )
            # 记入去重缓存（LRU 淘汰：超过上限时移除最旧条目）
            self._poll_cache[cache_key] = datetime.now(UTC)
            if len(self._poll_cache) > _CACHE_MAX_SIZE:
                self._poll_cache.popitem(last=False)  # FIFO 淘汰最旧
        except asyncio.QueueFull:
            logger.warning(
                f"趣味投票队列已满({self._queue.maxsize})，丢弃任务: "
                f"channel={channel_id}, forward_msg_id={forward_msg_id}"
            )
            return False

        logger.info(f"📊 趣味投票任务已入队: channel={channel_id}, forward_msg_id={forward_msg_id}")
        return True

    def get_stats(self) -> dict:
        """获取处理器统计信息

        Returns:
            统计信息字典
        """
        return {
            "running": self._running,
            "queue_size": self._queue.qsize(),
            "processed_count": self._processed_count,
            "failed_count": self._failed_count,
        }

    async def _poll_worker(self) -> None:
        """后台 worker 循环，逐个处理队列中的投票任务"""
        logger.info("自动趣味投票 worker 已启动")

        while self._running:
            try:
                task = None
                try:
                    task = await asyncio.wait_for(self._queue.get(), timeout=5.0)
                except TimeoutError:
                    continue

                await self._process_poll_task(task)
                self._queue.task_done()

            except asyncio.CancelledError:
                logger.info("自动趣味投票 worker 收到取消信号")
                break
            except Exception as e:
                logger.error(f"自动趣味投票 worker 异常: {type(e).__name__}: {e}", exc_info=True)
                await asyncio.sleep(1.0)  # 防止错误循环

        logger.info("自动趣味投票 worker 已退出")

    async def _process_poll_task(self, task: dict) -> None:
        """处理单个趣味投票任务

        Args:
            task: 任务字典，包含 channel_id, discussion_id, forward_msg_id, message_text
        """
        channel_id = task["channel_id"]
        discussion_id = task["discussion_id"]
        forward_msg_id = task["forward_msg_id"]
        message_text = task["message_text"]

        try:
            if not is_auto_poll_enabled_for_channel(channel_id):
                logger.info(f"频道 {channel_id} 的自动趣味投票已禁用，跳过")
                return

            # 消息文本太短，无法生成有意义的投票
            if len(message_text.strip()) < 10:
                logger.info(f"消息文本太短（{len(message_text)}字符），跳过投票生成")
                return

            # 调用 AI 生成投票
            logger.info(f"开始为频道 {channel_id} 的消息生成趣味投票...")
            poll_data = await generate_poll_from_summary(message_text)

            if not poll_data or "question" not in poll_data or "options" not in poll_data:
                logger.error("AI 生成趣味投票内容失败，使用默认投票")
                poll_data = {
                    "question": get_text("poll.default_question"),
                    "options": [
                        get_text("poll.default_options.0"),
                        get_text("poll.default_options.1"),
                        get_text("poll.default_options.2"),
                        get_text("poll.default_options.3"),
                    ],
                }

            # 构造 Poll 对象（无 inline 按钮）
            question_text = str(
                poll_data.get("question", get_text("poll.default_question"))
            ).strip()[:250]

            poll_answers = []
            for i, opt in enumerate(poll_data.get("options", [])[:10]):
                opt_clean = str(opt).strip()[:100]
                poll_answers.append(
                    PollAnswer(
                        text=TextWithEntities(opt_clean, entities=[]),
                        option=bytes([i]),
                    )
                )

            poll_obj = Poll(
                id=0,
                question=TextWithEntities(question_text, entities=[]),
                answers=poll_answers,
                hash=0,
                closed=False,
                public_voters=config_module.POLL_PUBLIC_VOTERS,
                multiple_choice=False,
                quiz=False,
            )

            # 发送到讨论组，回复转发消息
            poll_msg = await self.client.send_message(
                discussion_id,
                file=InputMediaPoll(poll=poll_obj),
                reply_to=forward_msg_id,
            )

            self._processed_count += 1
            logger.info(
                f"✅ 趣味投票发送成功: channel={channel_id}, "
                f"discussion={discussion_id}, poll_msg_id={poll_msg.id}"
            )

        except Exception as e:
            self._failed_count += 1
            record_error(e, "_process_poll_task")
            logger.error(
                f"处理趣味投票任务失败: channel={channel_id}, {type(e).__name__}: {e}",
                exc_info=True,
            )

    async def _periodic_cache_cleanup(self) -> None:
        """定期清理过期的去重缓存（每10分钟执行一次）"""
        while self._running:
            try:
                await asyncio.sleep(600)  # 10分钟

                async with self._cache_lock:
                    now = datetime.now(UTC)
                    expired_keys = [
                        key
                        for key, timestamp in self._poll_cache.items()
                        if now - timestamp >= _CACHE_TTL
                    ]

                    for key in expired_keys:
                        del self._poll_cache[key]

                    if expired_keys:
                        logger.info(f"清理了 {len(expired_keys)} 条过期的趣味投票去重缓存")

            except asyncio.CancelledError:
                break
            except Exception as e:
                logger.error(f"清理趣味投票缓存时出错: {type(e).__name__}: {e}", exc_info=True)


# 全局实例
_auto_poll_handler: AutoPollHandler | None = None


def get_auto_poll_handler() -> AutoPollHandler | None:
    """获取全局 AutoPollHandler 实例"""
    return _auto_poll_handler


async def initialize_auto_poll(client) -> AutoPollHandler:
    """初始化自动趣味投票处理器

    Args:
        client: Telegram客户端实例

    Returns:
        AutoPollHandler 实例
    """
    global _auto_poll_handler

    if _auto_poll_handler is not None:
        logger.warning("AutoPollHandler 已存在，跳过初始化")
        return _auto_poll_handler

    logger.info("初始化自动趣味投票处理器...")

    handler = AutoPollHandler(client=client)
    await handler.start()

    _auto_poll_handler = handler

    logger.info("自动趣味投票处理器初始化完成")
    return handler


async def shutdown_auto_poll() -> None:
    """关闭自动趣味投票处理器"""
    global _auto_poll_handler

    if _auto_poll_handler is None:
        return

    logger.info("正在关闭自动趣味投票处理器...")
    await _auto_poll_handler.stop()
    _auto_poll_handler = None
    logger.info("自动趣味投票处理器已关闭")
