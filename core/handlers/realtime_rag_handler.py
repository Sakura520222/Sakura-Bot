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
from itertools import count

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
        self._generations: dict[str, int] = {}
        self._generation_sequence = count(1)
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
        created_at: datetime | str | None = None,
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
        generation = next(self._generation_sequence)

        from core.ai.source_archive import archive_source

        created_at = (
            created_at.isoformat()
            if isinstance(created_at, datetime)
            else created_at or datetime.now(UTC).isoformat()
        )
        archive_source(
            channel_id,
            message_id,
            text,
            created_at,
            channel_name=channel_name,
            channel_username=channel_username,
        )
        self._generations[vector_id] = generation
        try:
            self._queue.put_nowait(
                {
                    "vector_id": vector_id,
                    "generation": generation,
                    "message_id": message_id,
                    "channel_id": channel_id,
                    "channel_name": channel_name,
                    "text": text,
                    "sender_id": sender_id,
                    "channel_username": channel_username,
                    "created_at": (
                        created_at.isoformat()
                        if isinstance(created_at, datetime)
                        else created_at or datetime.now(UTC).isoformat()
                    ),
                }
            )
        except asyncio.QueueFull:
            self._generations.pop(vector_id, None)
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
        created_at: datetime | str | None = None,
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
        generation = next(self._generation_sequence)

        from core.ai.source_archive import archive_source

        created_at = (
            created_at.isoformat()
            if isinstance(created_at, datetime)
            else created_at or datetime.now(UTC).isoformat()
        )
        archive_source(
            channel_id,
            message_id,
            text,
            created_at,
            channel_name=channel_name,
            channel_username=channel_username,
        )
        self._generations[vector_id] = generation
        try:
            self._queue.put_nowait(
                {
                    "vector_id": vector_id,
                    "generation": generation,
                    "message_id": message_id,
                    "channel_id": channel_id,
                    "channel_name": channel_name,
                    "text": text,
                    "sender_id": sender_id,
                    "channel_username": channel_username,
                    "created_at": (
                        created_at.isoformat()
                        if isinstance(created_at, datetime)
                        else created_at or datetime.now(UTC).isoformat()
                    ),
                    "is_update": True,
                }
            )
        except asyncio.QueueFull:
            self._generations.pop(vector_id, None)
            logger.warning(
                f"实时RAG队列已满({self._queue.maxsize})，丢弃更新: "
                f"channel={channel_id}, msg_id={message_id}"
            )
            return False

        logger.debug(f"消息更新已入队: channel={channel_id}, msg_id={message_id}")
        return True

    async def index_message(
        self,
        channel_id: str,
        channel_name: str,
        message_id: int,
        text: str,
        channel_username: str | None = None,
        created_at: datetime | str | None = None,
    ) -> bool:
        """同步等待所有分块写入完成后再返回，供带持久化重试的图片任务调用。"""
        from core.ai.source_archive import archive_source

        if not text or not text.strip():
            return False
        vector_id = f"{channel_id}:{message_id}"
        generation = next(self._generation_sequence)
        timestamp = (
            created_at.isoformat()
            if isinstance(created_at, datetime)
            else created_at or datetime.now(UTC).isoformat()
        )
        archive_source(
            channel_id,
            message_id,
            text,
            timestamp,
            channel_name=channel_name,
            channel_username=channel_username,
        )
        self._generations[vector_id] = generation
        return await self._process_batch(
            [
                {
                    "vector_id": vector_id,
                    "generation": generation,
                    "message_id": message_id,
                    "channel_id": channel_id,
                    "channel_name": channel_name,
                    "text": text,
                    "channel_username": channel_username,
                    "created_at": timestamp,
                }
            ]
        )

    async def handle_message_delete(self, channel_id: str, message_id: int) -> bool:
        """
        处理消息删除事件，从向量库中移除对应条目

        Args:
            channel_id: 频道ID
            message_id: 消息ID

        Returns:
            是否成功删除
        """
        vector_id = f"{channel_id}:{message_id}"
        self._generations.pop(vector_id, None)
        from core.ai.source_archive import delete_source

        delete_source(channel_id, message_id)
        try:
            from core.ai.vector_store import get_vector_store

            vector_store = get_vector_store()
            if not vector_store.is_messages_available():
                return False

            vector_id = f"{channel_id}:{message_id}"
            success = vector_store.delete_message(vector_id)
            if success:
                from core.ai.source_archive import delete_source

                delete_source(channel_id, message_id)
            return success

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

    async def _process_batch(self, batch: list[dict]) -> bool:
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
                return False

            if not emb_gen.is_available():
                logger.warning("Embedding服务不可用，跳过批次处理")
                self._failed_count += len(batch)
                return False

            import os

            from core.ai.text_budget import split_text

            budget = int(os.getenv("EMBEDDING_INPUT_TOKENS", "4096"))
            loop = asyncio.get_running_loop()
            # 一条消息的所有分块都写入成功才算处理成功；不分块的消息沿用旧文档 ID。
            all_success = True
            for item in batch:
                try:
                    parts = split_text(item["text"], budget)
                    embeddings = await loop.run_in_executor(None, emb_gen.batch_generate, parts)
                    if (
                        embeddings is None
                        or len(embeddings) != len(parts)
                        or any(embedding is None for embedding in embeddings)
                    ):
                        raise RuntimeError("Incomplete embeddings for message " + item["vector_id"])
                    if item.get("generation") is not None and item[
                        "generation"
                    ] != self._generations.get(item["vector_id"]):
                        raise RuntimeError("Message was changed or deleted while embedding")
                    metadata = {
                        "channel_id": item["channel_id"],
                        "channel_name": item["channel_name"],
                        "message_id": str(item["message_id"]),
                        "created_at": item["created_at"],
                        "sender_id": str(item.get("sender_id") or ""),
                        "parent_id": item["vector_id"],
                    }
                    if item.get("channel_username"):
                        metadata["channel_username"] = str(item["channel_username"])
                    ids = (
                        [item["vector_id"]]
                        if len(parts) == 1
                        else [f"{item['vector_id']}#chunk={n}" for n in range(len(parts))]
                    )
                    metadatas = [
                        dict(metadata, chunk_index=n, chunk_count=len(parts))
                        for n in range(len(parts))
                    ]
                    # 先完成 embedding，失败不会覆盖现有可用版本。
                    success = vector_store.replace_message_chunks(
                        item["vector_id"], ids, parts, metadatas, embeddings
                    )
                    if not success:
                        raise RuntimeError(
                            "Failed to persist complete message " + item["vector_id"]
                        )
                    self._processed_count += 1
                except Exception as exc:
                    all_success = False
                    self._failed_count += 1
                    logger.error("RAG 消息处理失败 %s: %s", item["vector_id"], exc)

            return all_success

        except Exception as e:
            logger.error(f"批次处理失败: {type(e).__name__}: {e}", exc_info=True)
            self._failed_count += len(batch)

            return False
        finally:
            # 序号只增不复用；清理时跳过已被更新编辑覆盖的条目，不影响后来的修改。
            for item in batch:
                if (
                    item.get("generation") is not None
                    and self._generations.get(item["vector_id"]) == item["generation"]
                ):
                    self._generations.pop(item["vector_id"], None)


# ── 模块级单例 ──────────────────────────────────────────────────────────

_realtime_rag_handler: RealtimeRAGHandler | None = None


def get_realtime_rag_handler() -> RealtimeRAGHandler:
    """获取全局实时RAG处理器实例"""
    global _realtime_rag_handler
    if _realtime_rag_handler is None:
        _realtime_rag_handler = RealtimeRAGHandler()
    return _realtime_rag_handler
