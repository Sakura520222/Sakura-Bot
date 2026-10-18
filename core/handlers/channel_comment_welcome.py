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
频道评论区欢迎消息模块
当频道发布新消息时，自动在讨论组发送欢迎消息和内联按钮
"""

import asyncio
import logging
import random
from datetime import UTC, datetime, timedelta

from telethon import Button, events
from telethon.errors import FloodWaitError
from telethon.tl.functions.channels import GetFullChannelRequest

from core.config import QA_BOT_USERNAME, comparable_channel_key, normalize_channel_id
from core.handlers.channel_comment_welcome_config import (
    get_channel_comment_welcome_config,
    validate_callback_data_length,
)
from core.i18n.i18n import get_text
from core.infrastructure.database import get_db_manager
from core.system.error_handler import record_error

logger = logging.getLogger(__name__)


def _channel_entry_matches(entry: str, channel_id: int | str) -> bool:
    """判断白名单条目 entry 是否指向 channel_id 标识的频道

    使用与频道配置读取一致的等价规则（忽略 URL 前缀、@、大小写与末尾斜杠）。

    匹配逻辑：
    1. 如果 channel_id 是 username（含 @/URL/大小写差异），直接等价比较
    2. 如果 channel_id 是数字ID字符串，直接比较（用于无username的频道，
       兼容 t.me/c/<id> 私有频道链接形式）
    """
    # 查询侧与白名单条目侧使用同一归一化规则
    channel_key = comparable_channel_key(str(channel_id).strip())
    if not channel_key:
        return False

    # 标准化频道URL，提取标识符；支持 @username、https://t.me/username、username
    normalized = comparable_channel_key(str(entry))
    return normalized == channel_key or normalized.endswith(f"/{channel_key}")


def is_channel_in_whitelist(channel_id: int | str, channels: list[str]) -> bool:
    """
    检查频道是否在白名单中

    Args:
        channel_id: 频道ID（数字或字符串）
        channels: 频道白名单（URL格式）

    Returns:
        bool: 是否在白名单中
    """
    for ch in channels:
        if _channel_entry_matches(ch, channel_id):
            return True

    return False


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
            # 频道已在前面通过白名单检查，这里直接获取配置
            # 如果没有特定配置，将使用默认配置
            config = await get_channel_comment_welcome_config(channel_id)

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

    async def _get_channel_identifier(self, channel_id: int) -> str:
        """通过频道数字ID获取可读标识符（username 或字符串ID）。

        Args:
            channel_id: 频道数字ID

        Returns:
            频道 username（小写）或字符串化的数字ID
        """
        try:
            entity = await self.client.get_entity(channel_id)
            if hasattr(entity, "username") and entity.username:
                return entity.username
        except Exception as e:
            logger.warning(f"获取频道实体失败 (id={channel_id}): {e}")
        return str(channel_id)

    async def _resolve_linked_channel(self, channel_id: int) -> tuple[str, str | None]:
        """解析频道数字ID在 CHANNELS 白名单中的规范配置键。

        先通过 Telegram 实体解析 username（权威映射，不做字符串猜测），
        再用与配置读取一致的等价规则在 CHANNELS 中匹配；私有频道
        （无 username）按数字ID或 t.me/c/<id> 链接形式匹配。
        该规范键与 WebUI 写入 channel_auto_poll_settings 等配置的键一致，
        后续入队与配置读取都必须使用它，避免不同写法导致配置错配。

        Args:
            channel_id: 频道数字ID

        Returns:
            (identifier, canonical_key):
            - identifier: username 或字符串化数字ID（用于日志）
            - canonical_key: CHANNELS 中命中的原始条目；不在白名单时为 None
        """
        import core.config as config_module

        identifier = await self._get_channel_identifier(channel_id)

        for entry in config_module.CHANNELS:
            if not isinstance(entry, str):
                continue
            if _channel_entry_matches(entry, identifier) or _channel_entry_matches(
                entry, str(channel_id)
            ):
                return identifier, entry

        # 未命中白名单：identifier 仍返回，供日志排查
        return identifier, None

    async def _collect_media_group_text(self, source_channel_id: int, channel_post_id: int) -> str:
        """从源频道获取媒体组的完整文本。

        讨论组中的媒体组消息去重后仅处理一条，但该消息可能没有 caption。
        通过获取源频道原始帖子及其相邻消息，收集整个媒体组的文本内容。

        Args:
            source_channel_id: 源频道数字ID
            channel_post_id: 源频道帖子ID

        Returns:
            合并后的文本内容，失败时返回空字符串
        """
        try:
            # 获取原始帖子消息
            original_msg = await self.client.get_messages(source_channel_id, ids=channel_post_id)
            if not original_msg:
                return ""

            # 如果原始消息本身有文本，直接返回
            original_text = original_msg.text or ""
            if original_text.strip():
                return original_text

            # 原始消息无文本，尝试获取附近消息（同一媒体组）
            grouped_id = getattr(original_msg, "grouped_id", None)
            if not grouped_id:
                return ""

            # 获取前后各5条消息，寻找同组的有文本消息
            nearby_ids = list(range(max(1, channel_post_id - 5), channel_post_id + 6))
            nearby_msgs = await self.client.get_messages(source_channel_id, ids=nearby_ids)
            if not nearby_msgs:
                return ""

            # 确保 nearby_msgs 是列表（单个消息时 get_messages 可能返回单个对象）
            if not isinstance(nearby_msgs, list):
                nearby_msgs = [nearby_msgs]

            # 收集同组消息中的文本
            texts = []
            for m in nearby_msgs:
                if m and getattr(m, "grouped_id", None) == grouped_id:
                    m_text = m.text or ""
                    if m_text.strip():
                        texts.append(m_text.strip())

            return "\n".join(texts)

        except Exception as e:
            logger.debug(
                f"获取媒体组文本失败: channel={source_channel_id}, "
                f"post={channel_post_id}, error={type(e).__name__}: {e}"
            )
            return ""

    async def handle_discussion_message(self, event: events.NewMessage.Event):
        """
        处理讨论组新消息事件（异步）
        检测是否为频道转发消息，如果是则发送欢迎消息

        仅处理讨论组中的消息，跳过频道本身的消息，
        避免在频道中直接发送欢迎消息。

        Args:
            event: Telethon NewMessage事件
        """
        try:
            msg = event.message

            # 仅处理讨论组中的消息（跳过频道本身的消息）
            # Telethon 中：频道 = Channel(megagroup=False)，讨论组 = Channel(megagroup=True)
            chat = event.chat
            if chat is None or getattr(chat, "megagroup", None) is not True:
                return

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

            source_channel_id = fwd_from.from_id.channel_id
            channel_post_id = fwd_from.channel_post

            if not channel_post_id:
                return

            # 确定讨论组所属的链接频道（讨论组 linked_chat_id 即为关联的频道）
            # get_entity 返回的 Channel 对象可能没有 linked_chat_id，
            # 需要用 GetFullChannelRequest 获取完整的 full_chat 信息
            linked_chat_id = None
            try:
                full_info = await self.client(GetFullChannelRequest(msg.chat_id))
                linked_chat_id = getattr(full_info.full_chat, "linked_chat_id", None)
            except Exception as e:
                logger.warning(f"获取讨论组完整信息失败: {e}")

            logger.debug(
                f"[评论欢迎] 讨论组 {msg.chat_id} linked_chat_id={linked_chat_id}, "
                f"source_channel_id={source_channel_id}"
            )
            if not linked_chat_id:
                logger.debug(f"讨论组 {msg.chat_id} 没有关联频道，跳过")
                return

            # 跳过频道帖子自动同步到讨论组的消息，避免重复处理
            # 场景：频道 A 转发了频道 B 的消息 → 讨论组收到两条消息：
            #   1. 来自 B 的直接转发（source_channel_id = B）→ 先到达，正常处理
            #   2. 频道 A 帖子自动同步（source_channel_id = A = linked_chat_id）→ 后到达
            # 通过去重缓存判断：如果同一帖子的外部转发已被处理，则跳过同步消息
            if source_channel_id == linked_chat_id:
                # 检查是否已有来自其他频道的外部转发处理了同一帖子
                async with _cache_lock:
                    found_external = False
                    for key in _message_cache:
                        # 匹配 "讨论组ID_*_帖子ID" 的模式（外部转发已处理）
                        if (
                            hasattr(msg, "grouped_id")
                            and msg.grouped_id
                            and key == f"{msg.chat_id}_{msg.grouped_id}"
                        ):
                            found_external = True
                            break
                        parts = key.split("_", 2)
                        if (
                            len(parts) == 3
                            and parts[0] == str(msg.chat_id)
                            and parts[2] == str(channel_post_id)
                        ):
                            found_external = True
                            break
                if found_external:
                    logger.info(f"频道同步消息 (帖子 {channel_post_id}) 已由外部转发处理过，跳过")
                    return

            # 白名单检查：验证讨论组所属的频道是否在 CHANNELS 配置中
            # 这样可以确保只在配置的目标频道的讨论组中发送欢迎消息

            # linked_chat_id 已通过上方守卫保证非 None。
            # 解析 CHANNELS 中的规范配置键：与 WebUI 写入
            # channel_auto_poll_settings / channel_comment_welcome 的键一致，
            # 入队与后续配置读取都必须使用该键，避免 username 与 URL
            # 两种写法错配导致频道级禁用配置失效。
            linked_identifier, linked_channel_key = await self._resolve_linked_channel(
                linked_chat_id
            )

            if linked_channel_key is None:
                logger.debug(
                    f"关联频道 {linked_identifier} (id={linked_chat_id}) 不在白名单中，忽略"
                )
                return

            source_identifier = await self._get_channel_identifier(source_channel_id)

            # 去重检查：使用讨论组ID + 来源频道帖子ID
            cache_key = f"{msg.chat_id}_{source_channel_id}_{channel_post_id}"
            if hasattr(msg, "grouped_id") and msg.grouped_id:
                cache_key = f"{msg.chat_id}_{msg.grouped_id}"

            async with _cache_lock:
                if cache_key in _message_cache:
                    if datetime.now(UTC) - _message_cache[cache_key] < _CACHE_TTL:
                        logger.debug(f"消息 {cache_key} 已在缓存中，跳过")
                        return
                    else:
                        del _message_cache[cache_key]

                _message_cache[cache_key] = datetime.now(UTC)

            # 添加到任务队列（channel_id 使用规范配置键，与
            # channel_comment_welcome 配置读取的键一致）
            await self.task_queue.put(
                {
                    "discussion_id": msg.chat_id,
                    "forward_msg_id": msg.id,
                    "channel_id": linked_channel_key,
                    "channel_msg_id": channel_post_id,
                }
            )

            logger.info(
                f"📥 检测到来自频道 {source_identifier} 的转发消息 "
                f"(帖子 {channel_post_id}) 出现在讨论组 {msg.chat_id}，已加入发送队列 "
                f"(关联频道: {linked_channel_key})"
            )

            # 将趣味投票任务入队（独立队列，与欢迎消息互不干扰）
            try:
                from core.handlers.auto_poll_handler import get_auto_poll_handler

                auto_poll_handler = get_auto_poll_handler()
                if auto_poll_handler is None:
                    logger.warning("趣味投票处理器未初始化，跳过入队")
                elif not auto_poll_handler._running:
                    logger.warning("趣味投票处理器未运行，跳过入队")
                else:
                    message_text = msg.text or ""

                    # 媒体组消息：讨论组中仅处理一条（去重），但该消息可能没有 caption。
                    # 此时从源频道获取原始帖子的完整文本（遍历同组所有消息）。
                    if (
                        not message_text.strip()
                        and hasattr(msg, "grouped_id")
                        and msg.grouped_id
                        and source_channel_id
                        and channel_post_id
                    ):
                        try:
                            full_text = await self._collect_media_group_text(
                                source_channel_id, channel_post_id
                            )
                            if full_text:
                                message_text = full_text
                                logger.info(
                                    f"📝 媒体组文本从源频道补充获取: "
                                    f"channel={source_channel_id}, "
                                    f"post={channel_post_id}, text_len={len(message_text)}"
                                )
                        except Exception as fetch_err:
                            logger.debug(
                                f"从源频道获取媒体组文本失败: {type(fetch_err).__name__}: "
                                f"{fetch_err}"
                            )

                    enqueued = auto_poll_handler.enqueue_poll_task(
                        channel_id=linked_channel_key,
                        discussion_id=msg.chat_id,
                        forward_msg_id=msg.id,
                        message_text=message_text,
                    )
                    logger.info(
                        f"📊 趣味投票入队结果: {enqueued}, "
                        f"channel={linked_channel_key} (linked_id={linked_chat_id}), "
                        f"text_len={len(message_text)}"
                    )
            except Exception as e:
                logger.warning(f"趣味投票入队失败（不影响欢迎消息）: {type(e).__name__}: {e}")

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
        user_id = event.sender_id

        # 标准化频道ID
        normalized_channel_id = normalize_channel_id(channel_id)
        if normalized_channel_id != channel_id:
            logger.info(f"频道ID已标准化: '{channel_id}' -> '{normalized_channel_id}'")

        logger.info(
            f"收到周报申请请求: 频道={normalized_channel_id}, 消息ID={msg_id}, 用户={user_id}"
        )

        # 检查用户是否已注册 QA Bot
        db = get_db_manager()
        logger.debug(f"获取数据库管理器成功: {type(db).__name__}")

        is_registered = False
        try:
            if hasattr(db, "is_user_registered"):
                logger.debug(f"检查用户 {user_id} 注册状态...")
                is_registered = await db.is_user_registered(user_id)
                logger.debug(f"用户 {user_id} 注册状态: {is_registered}")
            else:
                logger.error(f"数据库管理器不支持 is_user_registered 方法: {type(db).__name__}")
                await event.answer("系统错误：数据库不支持用户注册检查", alert=True)
                return
        except Exception as db_error:
            logger.error(
                f"检查用户注册状态时出错: {type(db_error).__name__}: {db_error}", exc_info=True
            )
            await event.answer("系统错误：无法检查用户状态", alert=True)
            return

        if not is_registered:
            # 用户未注册，编辑消息显示注册引导按钮
            logger.info(f"用户 {user_id} 未注册 QA Bot，编辑消息显示注册引导")

            # 构建 QA Bot 链接
            qa_bot_link = f"https://t.me/{QA_BOT_USERNAME}" if QA_BOT_USERNAME else None

            if qa_bot_link:
                # 有配置用户名，编辑消息显示注册按钮
                register_button = Button.url(
                    get_text("comment_welcome.register_button"), url=qa_bot_link
                )

                # 编辑消息，替换为注册按钮
                await event.edit(
                    get_text("comment_welcome.not_registered"), buttons=register_button
                )

                # 启动异步任务，30秒后恢复原始按钮
                asyncio.create_task(
                    _restore_original_button(
                        event.client, event.chat_id, event.message_id, channel_id, msg_id, user_id
                    )
                )

                await event.answer()
            else:
                # 未配置用户名，发送提示消息
                await event.answer(get_text("comment_welcome.not_registered"), alert=True)
            return

        # 用户已注册，继续处理申请
        logger.info(f"用户 {user_id} 已注册，继续处理周报申请")

        # 检查数据库中是否已有处理中的请求（使用标准化后的ID）
        if hasattr(db, "check_pending_summary_request"):
            has_pending = await db.check_pending_summary_request(normalized_channel_id)
            if has_pending:
                # 已有处理中的请求
                await event.answer(get_text("comment_welcome.already_requested"), alert=True)
                logger.info(f"频道 {normalized_channel_id} 已有处理中的周报请求")
                return

        # 记录请求到数据库（使用标准化后的ID）
        # 注意：不需要单独发送通知给管理员，因为轮询任务会自动检查并发送通知
        if hasattr(db, "add_summary_request"):
            await db.add_summary_request(
                channel_id=normalized_channel_id,
                message_id=msg_id,
                request_type="manual",
                requested_by=user_id,
            )
            logger.info(f"已记录周报请求到数据库，等待轮询任务处理: 频道={normalized_channel_id}")

        # 发送callback响应（告知用户请求已提交）
        await event.answer(get_text("comment_welcome.request_sent"), alert=True)
        logger.info(f"周报申请已提交，等待轮询处理: 频道={normalized_channel_id}")

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


async def _restore_original_button(
    client,
    chat_id: int,
    message_id: int,
    channel_id: str,
    channel_msg_id: int,
    user_id: int,
    timeout: int = 30,
):
    """
    异步任务：智能恢复原始按钮
    - 用户注册成功后立即恢复
    - 超时后无条件恢复

    Args:
        client: Telegram客户端实例
        chat_id: 聊天ID
        message_id: 消息ID
        channel_id: 频道ID
        channel_msg_id: 频道消息ID
        user_id: 用户ID
        timeout: 超时时间（秒），默认30秒
    """
    try:
        db = get_db_manager()
        restore_immediately = False

        # 智能等待：降低 DB 压力，使用较长间隔并限制检查次数
        check_interval = 3  # 每 3 秒检查一次
        max_checks = min(max(1, timeout // check_interval), 5)  # 最多检查 5 次，避免高频轮询

        for i in range(max_checks):
            await asyncio.sleep(check_interval)

            # 检查用户是否已注册
            if hasattr(db, "is_user_registered"):
                try:
                    is_registered = await db.is_user_registered(user_id)
                    if is_registered:
                        elapsed = (i + 1) * check_interval
                        logger.info(f"✨ 用户 {user_id} 已注册，立即恢复按钮（等待 {elapsed} 秒）")
                        restore_immediately = True
                        break
                except Exception as e:
                    logger.warning(f"检查用户注册状态时出错: {type(e).__name__}: {e}")

        # 如果已注册，立即恢复；否则等待超时后恢复
        if not restore_immediately:
            logger.debug(
                f"超时恢复原始按钮: chat_id={chat_id}, msg_id={message_id}, user_id={user_id}"
            )

        # 获取按钮文本（使用 i18n）
        button_text = get_text("comment_welcome.button")

        # 恢复原始按钮
        original_button = Button.inline(
            button_text,
            data=f"req_summary:{channel_id}:{channel_msg_id}".encode(),
        )

        # 获取原始欢迎消息文本
        welcome_message = get_text("comment_welcome.message")

        try:
            # 编辑消息，恢复原始按钮
            await client.edit_message(chat_id, message_id, welcome_message, buttons=original_button)
            logger.info(f"✅ 已恢复原始按钮: chat_id={chat_id}, msg_id={message_id}")
        except Exception as e:
            logger.warning(f"恢复按钮失败: {type(e).__name__}: {e}")

    except asyncio.CancelledError:
        logger.debug("恢复按钮任务被取消")
    except Exception as e:
        logger.error(f"恢复按钮任务出错: {type(e).__name__}: {e}", exc_info=True)
