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
实时 RAG 处理器 - 异步队列批量处理频道消息并写入向量库

监听频道新消息，通过 asyncio.Queue 异步批量生成 embedding 后写入 ChromaDB messages collection。
"""

import asyncio
import logging
from datetime import UTC, datetime

logger = logging.getLogger(__name__)

# 批量处理配置
BATCH_SIZE = 5  # 每批处理的消息数量
BATCH_INTERVAL = 5.0  # 批量处理间隔（秒）
QUEUE_MAX_SIZE = 1000  # 队列最大容量，防止内存无限增长


class RealtimeRAGHandler:
    """实时 RAG 处理器 - 异步队列批量处理频道消息"""

    def __init__(self):
        """初始化处理器"""
        self._queue: asyncio.Queue[dict] = asyncio.Queue(maxsize=QUEUE_MAX_SIZE)
        self._running = False
        self._worker_task: asyncio.Task | None = None
        self._processed_count = 0
        self._failed_count = 0
        logger.info("实时RAG处理器已创建")

    async def start(self) -> None:
        """启动后台 worker 任务"""
        if self._running:
            logger.warning("实时RAG处理器已在运行")
            return

        self._running = True
        self._worker_task = asyncio.create_task(self._message_worker())
        logger.info("实时RAG处理器已启动")

    async def stop(self) -> None:
        """停止后台 worker 任务"""
        if not self._running:
            return

        self._running = False

        # 等待队列清空
        remaining = self._queue.qsize()
        if remaining > 0:
            logger.info(f"实时RAG处理器停止前处理剩余 {remaining} 条消息...")
            try:
                await asyncio.wait_for(self._queue.join(), timeout=30.0)
            except TimeoutError:
                logger.warning(f"实时RAG处理器停止超时，丢弃 {self._queue.qsize()} 条消息")

        if self._worker_task and not self._worker_task.done():
            self._worker_task.cancel()
            try:
                await self._worker_task
            except asyncio.CancelledError:
                pass

        logger.info(
            f"实时RAG处理器已停止，共处理 {self._processed_count} 条，失败 {self._failed_count} 条"
        )

    def enqueue_message(
        self,
        channel_id: str,
        channel_name: str,
        message_id: int,
        text: str,
        sender_id: int | None = None,
        channel_username: str | None = None,
    ) -> bool:
        """
        将频道消息加入处理队列

        Args:
            channel_id: 频道ID（数字内部 ID）
            channel_name: 频道名称
            message_id: 消息ID
            text: 消息文本内容
            sender_id: 发送者ID（可选）
            channel_username: 频道公开 username（可选，用于生成公开帖子链接）

        Returns:
            是否成功入队
        """
        if not text or not text.strip():
            return False

        if not self._running:
            logger.warning("实时RAG处理器未运行，消息将无法处理")
            return False

        # 使用 channel_id:message_id 作为向量库唯一标识
        vector_id = f"{channel_id}:{message_id}"

        try:
            self._queue.put_nowait(
                {
                    "vector_id": vector_id,
                    "message_id": message_id,
                    "channel_id": channel_id,
                    "channel_name": channel_name,
                    "text": text.strip(),
                    "sender_id": sender_id,
                    "channel_username": channel_username,
                    "created_at": datetime.now(UTC).isoformat(),
                }
            )
        except asyncio.QueueFull:
            logger.warning(
                f"实时RAG队列已满({self._queue.maxsize})，丢弃消息: "
                f"channel={channel_id}, msg_id={message_id}"
            )
            return False

        logger.debug(f"消息已入队: channel={channel_id}, msg_id={message_id}")
        return True

    def enqueue_message_update(
        self,
        channel_id: str,
        channel_name: str,
        message_id: int,
        text: str,
        sender_id: int | None = None,
        channel_username: str | None = None,
    ) -> bool:
        """
        将消息更新加入处理队列（编辑消息时调用）

        Args:
            channel_id: 频道ID（数字内部 ID）
            channel_name: 频道名称
            message_id: 消息ID
            text: 新的文本内容
            sender_id: 发送者ID（可选）
            channel_username: 频道公开 username（可选，用于生成公开帖子链接）

        Returns:
            是否成功入队
        """
        if not text or not text.strip():
            return False

        if not self._running:
            logger.warning("实时RAG处理器未运行，消息更新将无法处理")
            return False

        vector_id = f"{channel_id}:{message_id}"

        try:
            self._queue.put_nowait(
                {
                    "vector_id": vector_id,
                    "message_id": message_id,
                    "channel_id": channel_id,
                    "channel_name": channel_name,
                    "text": text.strip(),
                    "sender_id": sender_id,
                    "channel_username": channel_username,
                    "created_at": datetime.now(UTC).isoformat(),
                    "is_update": True,
                }
            )
        except asyncio.QueueFull:
            logger.warning(
                f"实时RAG队列已满({self._queue.maxsize})，丢弃更新: "
                f"channel={channel_id}, msg_id={message_id}"
            )
            return False

        logger.debug(f"消息更新已入队: channel={channel_id}, msg_id={message_id}")
        return True

    async def handle_message_delete(self, channel_id: str, message_id: int) -> bool:
        """
        处理消息删除事件，从向量库中移除对应条目

        Args:
            channel_id: 频道ID
            message_id: 消息ID

        Returns:
            是否成功删除
        """
        try:
            from core.ai.vector_store import get_vector_store

            vector_store = get_vector_store()
            if not vector_store.is_messages_available():
                return False

            vector_id = f"{channel_id}:{message_id}"
            return vector_store.delete_message(vector_id)

        except Exception as e:
            logger.error(f"删除消息向量失败: {type(e).__name__}: {e}")
            return False

    def get_stats(self) -> dict:
        """
        获取处理器统计信息

        Returns:
            统计信息字典
        """
        return {
            "running": self._running,
            "queue_size": self._queue.qsize(),
            "processed_count": self._processed_count,
            "failed_count": self._failed_count,
        }

    async def _message_worker(self) -> None:
        """后台 worker 循环，定期批量处理队列中的消息"""
        logger.info("实时RAG worker 已启动")

        while self._running:
            try:
                batch = []

                # 尝试收集一批消息
                try:
                    # 等待第一条消息（最多等待 BATCH_INTERVAL 秒）
                    first_item = await asyncio.wait_for(self._queue.get(), timeout=BATCH_INTERVAL)
                    batch.append(first_item)

                    # 尝试收集更多消息（非阻塞）
                    while len(batch) < BATCH_SIZE:
                        try:
                            item = self._queue.get_nowait()
                            batch.append(item)
                        except asyncio.QueueEmpty:
                            break

                except TimeoutError:
                    # 超时无新消息，继续等待
                    continue

                # 处理这一批消息
                if batch:
                    await self._process_batch(batch)
                    # 标记任务完成
                    for _ in batch:
                        self._queue.task_done()

            except asyncio.CancelledError:
                logger.info("实时RAG worker 收到取消信号")
                break
            except Exception as e:
                logger.error(f"实时RAG worker 异常: {type(e).__name__}: {e}", exc_info=True)
                await asyncio.sleep(1.0)  # 防止错误循环

        logger.info("实时RAG worker 已退出")

    async def _process_batch(self, batch: list[dict]) -> None:
        """
        批量处理消息：生成 embedding → 写入 ChromaDB

        Args:
            batch: 消息列表，每条包含 vector_id, text, metadata 等
        """
        try:
            from core.ai.embedding_generator import get_embedding_generator
            from core.ai.vector_store import get_vector_store

            vector_store = get_vector_store()
            emb_gen = get_embedding_generator()

            if not vector_store.is_messages_available():
                logger.warning("消息向量存储不可用，跳过批次处理")
                self._failed_count += len(batch)
                return

            if not emb_gen.is_available():
                logger.warning("Embedding服务不可用，跳过批次处理")
                self._failed_count += len(batch)
                return

            # 提取文本列表
            texts = [item["text"] for item in batch]

            # 批量生成 embedding
            loop = asyncio.get_running_loop()

            def _batch_embed():
                return emb_gen.batch_generate(texts)

            embeddings = await loop.run_in_executor(None, _batch_embed)

            if embeddings is None or len(embeddings) != len(batch):
                logger.error(
                    f"批量embedding生成失败: 期望 {len(batch)} 个，实际 {len(embeddings) if embeddings else 0} 个"
                )
                self._failed_count += len(batch)
                return

            # 分离新增和更新操作
            add_items = []
            update_items = []

            for item, embedding in zip(batch, embeddings, strict=True):
                if embedding is None:
                    logger.warning(f"跳过embedding为空的消息: vector_id={item['vector_id']}")
                    self._failed_count += 1
                    continue

                metadata = {
                    "channel_id": item["channel_id"],
                    "channel_name": item["channel_name"],
                    "message_id": str(item["message_id"]),
                    "created_at": item["created_at"],
                    "sender_id": str(item["sender_id"]) if item.get("sender_id") else "",
                }
                # 公开频道 username：生成帖子链接时优先于数字 ID（更友好可读）
                if item.get("channel_username"):
                    metadata["channel_username"] = str(item["channel_username"])

                entry = {
                    "id": item["vector_id"],
                    "text": item["text"],
                    "metadata": metadata,
                    "embedding": embedding,
                }

                if item.get("is_update"):
                    update_items.append(entry)
                else:
                    add_items.append(entry)

            # 批量新增
            if add_items:
                success_count = vector_store.add_messages_batch(
                    ids=[it["id"] for it in add_items],
                    texts=[it["text"] for it in add_items],
                    metadatas=[it["metadata"] for it in add_items],
                    embeddings=[it["embedding"] for it in add_items],
                )
                self._processed_count += success_count
                self._failed_count += len(add_items) - success_count

            # 逐条更新
            for item in update_items:
                success = vector_store.update_message(
                    message_id=item["id"],
                    text=item["text"],
                    metadata=item["metadata"],
                    embedding=item["embedding"],
                )
                if success:
                    self._processed_count += 1
                else:
                    self._failed_count += 1

            logger.info(
                f"批次处理完成: 新增 {len(add_items)} 条, 更新 {len(update_items)} 条, "
                f"队列剩余 {self._queue.qsize()} 条"
            )

        except Exception as e:
            logger.error(f"批次处理失败: {type(e).__name__}: {e}", exc_info=True)
            self._failed_count += len(batch)


# ── 模块级单例 ──────────────────────────────────────────────────────────

_realtime_rag_handler: RealtimeRAGHandler | None = None


def get_realtime_rag_handler() -> RealtimeRAGHandler:
    """获取全局实时RAG处理器实例"""
    global _realtime_rag_handler
    if _realtime_rag_handler is None:
        _realtime_rag_handler = RealtimeRAGHandler()
    return _realtime_rag_handler
