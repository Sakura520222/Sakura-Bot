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
频道评论区欢迎消息模块
当频道发布新消息时，自动在讨论组发送欢迎消息和内联按钮
"""

import asyncio
import logging
import random
from datetime import UTC, datetime, timedelta

from telethon import Button, events
from telethon.errors import FloodWaitError

from .channel_comment_welcome_config import (
    get_channel_comment_welcome_config,
    validate_callback_data_length,
)
from .database import get_db_manager
from .error_handler import record_error
from .i18n import get_text

logger = logging.getLogger(__name__)

# 去重缓存：消息ID -> 时间戳
# 使用deque + TTL机制，防止内存无限增长
_message_cache: dict[str, datetime] = {}
_cache_lock = asyncio.Lock()

# TTL设置：消息去重缓存保留1小时
_CACHE_TTL = timedelta(hours=1)


class CommentWelcomeHandler:
    """频道评论区欢迎消息处理器"""

    def __init__(self, client, rate_limit_delay=(1, 3), worker_count=1):
        """
        初始化处理器

        Args:
            client: Telegram客户端实例
            rate_limit_delay: 限流延迟范围（秒），默认1-3秒随机
            worker_count: Worker数量，默认1个（普通频道规模足够）
        """
        self.client = client
        self.rate_limit_delay = rate_limit_delay
        self.worker_count = worker_count
        self.task_queue: asyncio.Queue = asyncio.Queue()
        self.workers: list[asyncio.Task] = []
        self.is_running = False

        # 定期清理缓存的任务
        self._cleanup_task: asyncio.Task | None = None

    async def start(self):
        """启动处理器（创建Workers和缓存清理任务）"""
        if self.is_running:
            logger.warning("CommentWelcomeHandler已在运行中")
            return

        logger.info("启动CommentWelcomeHandler...")

        # 启动Workers
        for i in range(self.worker_count):
            worker_task = asyncio.create_task(self._worker(worker_id=i))
            self.workers.append(worker_task)
            logger.info(f"已启动Worker-{i}")

        # 启动缓存清理任务（每10分钟清理一次）
        self._cleanup_task = asyncio.create_task(self._periodic_cache_cleanup())
        logger.info("已启动缓存清理任务")

        self.is_running = True
        logger.info(f"CommentWelcomeHandler启动完成，Worker数量: {self.worker_count}")

    async def stop(self):
        """停止处理器"""
        if not self.is_running:
            return

        logger.info("正在停止CommentWelcomeHandler...")
        self.is_running = False

        # 停止Workers
        for worker in self.workers:
            worker.cancel()
        await asyncio.gather(*self.workers, return_exceptions=True)
        self.workers.clear()

        # 停止缓存清理任务
        if self._cleanup_task:
            self._cleanup_task.cancel()
            try:
                await self._cleanup_task
            except asyncio.CancelledError:
                pass

        logger.info("CommentWelcomeHandler已停止")

    async def _worker(self, worker_id: int):
        """
        异步Worker，处理发送队列（带限流）

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

                # 执行发送任务
                await self._send_welcome_message(
                    discussion_id=task_data["discussion_id"],
                    forward_msg_id=task_data["forward_msg_id"],
                    channel_id=task_data["channel_id"],
                    channel_msg_id=task_data["channel_msg_id"],
                )

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

    async def _send_welcome_message(
        self,
        discussion_id: int,
        forward_msg_id: int,
        channel_id: str,
        channel_msg_id: int,
    ):
        """
        发送欢迎消息到讨论组（异步）

        Args:
            discussion_id: 讨论组ID
            forward_msg_id: 转发消息ID（用于reply_to）
            channel_id: 频道ID（用于按钮回调数据和配置读取）
            channel_msg_id: 频道消息ID（用于按钮回调数据）
        """
        try:
            # 获取频道配置
            from .config import CHANNELS

            # 尝试匹配频道URL（处理数字ID情况）
            channel_url = None
            for ch in CHANNELS:
                if ch.endswith(channel_id) or str(channel_id) in ch:
                    channel_url = ch
                    break

            # 获取配置
            if channel_url:
                config = await get_channel_comment_welcome_config(channel_url)
            else:
                # 使用默认配置
                from .channel_comment_welcome_config import get_default_comment_welcome_config

                config = get_default_comment_welcome_config()

            # 检查是否启用
            if not config.get("enabled", True):
                logger.debug(f"频道 {channel_id} 的评论区欢迎功能已禁用，跳过")
                return

            # 获取消息文本（支持 i18n key 或直接文本）
            welcome_message_key = config.get("welcome_message", "comment_welcome.message")
            if welcome_message_key.startswith("comment_welcome."):
                # 使用 i18n
                message = get_text(welcome_message_key)
            else:
                # 直接使用配置的文本
                message = welcome_message_key

            # 获取按钮文本（支持 i18n key 或直接文本）
            button_text_key = config.get("button_text", "comment_welcome.button")
            if button_text_key.startswith("comment_welcome."):
                button_text = get_text(button_text_key)
            else:
                button_text = button_text_key

            # 获取按钮行为
            button_action = config.get("button_action", "request_summary")

            # 策略模式：根据 button_action 生成不同的按钮
            # 当前仅支持 request_summary，预留扩展其他行为
            if button_action == "request_summary":
                # 验证 Callback Data 长度
                if not validate_callback_data_length(channel_id, channel_msg_id):
                    logger.warning(
                        f"Callback Data 长度超限，跳过发送按钮：{channel_id}:{channel_msg_id}"
                    )
                    # 发送无按钮的欢迎消息
                    await self.client.send_message(
                        discussion_id,
                        message,
                        reply_to=forward_msg_id,
                    )
                    return

                button = Button.inline(
                    button_text,
                    data=f"req_summary:{channel_id}:{channel_msg_id}".encode(),
                )
            else:
                # 未知行为，使用默认
                logger.warning(f"未知的按钮行为: {button_action}，使用默认行为")
                button = Button.inline(
                    button_text,
                    data=f"req_summary:{channel_id}:{channel_msg_id}".encode(),
                )

            # 发送消息，精准回复转发消息
            await self.client.send_message(
                discussion_id,
                message,
                buttons=button,
                reply_to=forward_msg_id,
            )

            logger.info(f"✅ 已在讨论组 {discussion_id} 发送欢迎消息，回复消息 {forward_msg_id}")

        except FloodWaitError as e:
            # 捕获FloodWait错误，等待指定时间后重试
            wait_seconds = e.seconds
            logger.warning(f"触发FloodWait，需要等待 {wait_seconds} 秒后将任务重新加入队列")
            await asyncio.sleep(wait_seconds)

            # 重新加入队列
            await self.task_queue.put(
                {
                    "discussion_id": discussion_id,
                    "forward_msg_id": forward_msg_id,
                    "channel_id": channel_id,
                    "channel_msg_id": channel_msg_id,
                }
            )

        except Exception as e:
            record_error(e, "send_welcome_message")
            logger.error(
                f"发送欢迎消息失败: {type(e).__name__}: {e}",
                exc_info=True,
            )

    async def handle_discussion_message(self, event: events.NewMessage.Event):
        """
        处理讨论组新消息事件（异步）
        检测是否为频道转发消息，如果是则发送欢迎消息

        Args:
            event: Telethon NewMessage事件
        """
        try:
            msg = event.message

            # 检查是否为转发消息
            if not (hasattr(msg, "fwd_from") and msg.fwd_from):
                return

            fwd_from = msg.fwd_from

            # 检查转发来源是否为频道
            if not (
                hasattr(fwd_from, "from_id")
                and fwd_from.from_id
                and hasattr(fwd_from.from_id, "channel_id")
            ):
                return

            channel_id_num = fwd_from.from_id.channel_id
            channel_post_id = fwd_from.channel_post

            if not channel_post_id:
                return

            # 检查是否在监控的频道列表中
            # 注意：channel_id_num是数字ID，需要与CHANNELS中的URL匹配
            # 这里我们简化处理：直接处理所有有讨论组的频道
            # 如果需要限制特定频道，可以在config中添加配置

            # 去重检查：使用消息ID和grouped_id
            cache_key = f"{msg.chat_id}_{channel_post_id}"
            if hasattr(msg, "grouped_id") and msg.grouped_id:
                # 如果是媒体组，使用grouped_id作为去重键
                cache_key = f"{msg.chat_id}_{msg.grouped_id}"

            async with _cache_lock:
                # 检查缓存中是否存在
                if cache_key in _message_cache:
                    # 检查是否过期
                    if datetime.now(UTC) - _message_cache[cache_key] < _CACHE_TTL:
                        logger.debug(f"消息 {cache_key} 已在缓存中，跳过")
                        return
                    else:
                        # 过期，删除旧记录
                        del _message_cache[cache_key]

                # 添加到缓存
                _message_cache[cache_key] = datetime.now(UTC)

            # 获取频道标识（用于按钮回调）
            # 注意：移除@符号，避免与下划线分隔符冲突
            try:
                channel_entity = await self.client.get_entity(channel_id_num)
                # 优先使用username（不带@），否则使用数字ID
                channel_identifier = (
                    channel_entity.username
                    if hasattr(channel_entity, "username") and channel_entity.username
                    else str(channel_id_num)
                )
            except Exception as e:
                logger.warning(f"获取频道实体失败，使用ID作为标识: {e}")
                channel_identifier = str(channel_id_num)

            # 添加到任务队列（异步处理，避免阻塞）
            await self.task_queue.put(
                {
                    "discussion_id": msg.chat_id,
                    "forward_msg_id": msg.id,
                    "channel_id": channel_identifier,
                    "channel_msg_id": channel_post_id,
                }
            )

            logger.info(
                f"📥 检测到频道 {channel_identifier} 的新消息 {channel_post_id}，已加入发送队列"
            )

        except Exception as e:
            record_error(e, "handle_discussion_message")
            logger.error(
                f"处理讨论组消息时出错: {type(e).__name__}: {e}",
                exc_info=True,
            )

    async def _periodic_cache_cleanup(self):
        """定期清理过期的去重缓存（每10分钟执行一次）"""
        while self.is_running:
            try:
                await asyncio.sleep(600)  # 10分钟

                async with _cache_lock:
                    now = datetime.now(UTC)
                    expired_keys = [
                        key
                        for key, timestamp in _message_cache.items()
                        if now - timestamp >= _CACHE_TTL
                    ]

                    for key in expired_keys:
                        del _message_cache[key]

                    if expired_keys:
                        logger.info(f"清理了 {len(expired_keys)} 条过期的去重缓存")

            except asyncio.CancelledError:
                break
            except Exception as e:
                logger.error(f"清理缓存时出错: {type(e).__name__}: {e}", exc_info=True)


# 全局实例
_comment_welcome_handler: CommentWelcomeHandler | None = None


def get_comment_welcome_handler():
    """获取全局CommentWelcomeHandler实例"""
    return _comment_welcome_handler


async def initialize_comment_welcome(
    client, db_manager=None, rate_limit_delay=(1, 3), worker_count=1
):
    """
    初始化频道评论区欢迎消息功能

    Args:
        client: Telegram客户端实例
        db_manager: 数据库管理器实例（可选，用于兼容性）
        rate_limit_delay: 限流延迟范围（秒），默认1-3秒随机
        worker_count: Worker数量，默认1个

    Returns:
        CommentWelcomeHandler实例
    """
    global _comment_welcome_handler

    if _comment_welcome_handler is not None:
        logger.warning("CommentWelcomeHandler已存在，跳过初始化")
        return _comment_welcome_handler

    logger.info("初始化频道评论区欢迎消息功能...")

    # 创建处理器
    handler = CommentWelcomeHandler(
        client=client,
        rate_limit_delay=rate_limit_delay,
        worker_count=worker_count,
    )

    # 启动处理器
    await handler.start()

    # 保存全局实例
    _comment_welcome_handler = handler

    logger.info("频道评论区欢迎消息功能初始化完成")
    return handler


async def shutdown_comment_welcome():
    """关闭频道评论区欢迎消息功能"""
    global _comment_welcome_handler

    if _comment_welcome_handler is None:
        return

    logger.info("正在关闭频道评论区欢迎消息功能...")
    await _comment_welcome_handler.stop()
    _comment_welcome_handler = None
    logger.info("频道评论区欢迎消息功能已关闭")


async def handle_summary_request_callback(event: events.CallbackQuery.Event):
    """
    处理"申请周报总结"按钮回调

    Args:
        event: Telethon CallbackQuery事件
    """
    try:
        # 解析callback_data: req_summary:<channel_id>:<msg_id>
        data_str = event.data.decode()
        parts = data_str.split(":")

        if len(parts) != 3 or parts[0] != "req_summary":
            logger.warning(f"无效的callback_data格式: {data_str}")
            await event.answer("无效的请求")
            return

        channel_id = parts[1]
        msg_id = int(parts[2])

        logger.info(f"收到周报申请请求: 频道={channel_id}, 消息ID={msg_id}")

        # 检查数据库中是否已有处理中的请求
        db = get_db_manager()
        if hasattr(db, "check_pending_summary_request"):
            has_pending = await db.check_pending_summary_request(channel_id)
            if has_pending:
                # 已有处理中的请求
                await event.answer(get_text("comment_welcome.already_requested"), alert=True)
                logger.info(f"频道 {channel_id} 已有处理中的周报请求")
                return

        # 记录请求到数据库
        if hasattr(db, "add_summary_request"):
            await db.add_summary_request(
                channel_id=channel_id,
                message_id=msg_id,
                request_type="manual",
                requested_by=event.sender_id,
            )
            logger.info(f"已记录周报请求到数据库: 频道={channel_id}")

        # 发送通知给管理员
        from .config import ADMIN_LIST
        from .telegram_client import get_active_client

        client = get_active_client()
        if client:
            notification = (
                f"📝 <b>新的周报申请</b>\n\n"
                f"频道: {channel_id}\n"
                f"消息ID: {msg_id}\n"
                f"申请用户: {event.sender_id}\n\n"
                f"请使用 /summary 命令处理该请求"
            )

            for admin_id in ADMIN_LIST:
                if admin_id != "me":
                    try:
                        await client.send_message(admin_id, notification, parse_mode="HTML")
                        logger.info(f"已通知管理员 {admin_id}")
                    except Exception as e:
                        logger.error(f"通知管理员 {admin_id} 失败: {e}")

        # 发送callback响应
        await event.answer(get_text("comment_welcome.request_sent"), alert=True)
        logger.info(f"周报申请处理完成: 频道={channel_id}")

    except Exception as e:
        record_error(e, "handle_summary_request_callback")
        logger.error(
            f"处理周报申请回调时出错: {type(e).__name__}: {e}",
            exc_info=True,
        )
        try:
            await event.answer("处理请求时出错", alert=True)
        except Exception:
            pass
