# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot

"""频道头像的 Telethon 拉取与磁盘缓存；所有失败都以 None 表达，绝不外抛到调用方。"""

import asyncio
import io
import logging
import os
import tempfile
import time
from pathlib import Path

logger = logging.getLogger(__name__)

# 与趣味投票图片共用 8 MiB 上限；头像通常远小于该值，仅用于拦截异常下载。
MAX_AVATAR_BYTES = 8 * 1024 * 1024
NEGATIVE_TTL_SECONDS = 300
STALE_AFTER_SECONDS = 7 * 86400
FETCH_TIMEOUT_SECONDS = 30
ENTITY_TIMEOUT_SECONDS = 15


def sniff_media_type(data):
    """仅凭魔数判断图片类型；识别失败一律拒绝入库与出站。"""
    if data.startswith(b"\xff\xd8\xff"):
        return "image/jpeg"
    if data.startswith(b"\x89PNG"):
        return "image/png"
    return None


class AvatarBuffer(io.BytesIO):
    """有界下载缓冲：超限时抛错中断 Telethon 写入，避免异常响应耗尽内存。"""

    def write(self, data):
        if self.tell() + len(data) > MAX_AVATAR_BYTES:
            raise ValueError("头像超过大小上限，已中止下载")
        return super().write(data)


class WikiAvatarService:
    """以 photo_id 命名磁盘文件；内存索引 + 负缓存 + 每频道单飞锁。"""

    def __init__(self, store, client_provider, root="data/wiki/avatars"):
        self.store = store
        self.client_provider = client_provider
        self.root = Path(root)
        self._index = {}  # channel_id -> photo_id
        self._refreshed_at = {}  # channel_id -> monotonic 时间，用于过期后台刷新
        self._negative = {}  # channel_id -> monotonic 时间，短负缓存避免高频回源
        self._locks = {}
        self._revalidating = {}
        self._tasks = set()

    async def get(self, channel_id):
        """磁盘缓存优先；未命中或负缓存过期才回源，任何异常折叠为 None。"""
        try:
            photo_id = self._index.get(channel_id)
            if photo_id is not None:
                cached = await self._cached(photo_id)
                if cached is not None:
                    fetched_at = self._refreshed_at.get(channel_id)
                    now = time.monotonic()
                    if fetched_at is None:
                        # 索引存在而时间戳丢失的异常状态：视为刚刚写入。
                        fetched_at = self._refreshed_at[channel_id] = now
                    if now - fetched_at > STALE_AFTER_SECONDS:
                        self._schedule_revalidate(channel_id)
                    return cached
            expired = self._negative.get(channel_id)
            if expired is not None and time.monotonic() - expired < NEGATIVE_TTL_SECONDS:
                return None
            return await asyncio.wait_for(self.refresh(channel_id), FETCH_TIMEOUT_SECONDS)
        except Exception as error:
            logger.warning("频道头像获取失败 (%s): %s", channel_id, type(error).__name__)
            self._negative[channel_id] = time.monotonic()
            return None

    async def refresh(self, channel_id):
        """回源刷新单个频道头像；内部持锁单飞，成功返回 (bytes, media_type, photo_id)。"""
        lock = self._locks.setdefault(channel_id, asyncio.Lock())
        async with lock:
            client = self._select_client()
            from telethon.tl.types import PeerChannel

            target = PeerChannel(int(channel_id)) if str(channel_id).isdecimal() else channel_id
            async with asyncio.timeout(ENTITY_TIMEOUT_SECONDS):
                entity = await client.get_entity(target)
            photo_id = getattr(getattr(entity, "photo", None), "photo_id", None)
            if not isinstance(photo_id, int):
                # 无头像的频道/实体进入短负缓存，防止匿名流量反复回源。
                self._negative[channel_id] = time.monotonic()
                return None
            if self._index.get(channel_id) == photo_id:
                # photo_id 未变且磁盘完好：直接复用，发布钩子的重复刷新零下载。
                cached = await self._cached(photo_id)
                if cached is not None:
                    self._refreshed_at[channel_id] = time.monotonic()
                    self._negative.pop(channel_id, None)
                    return cached
            stream = AvatarBuffer()
            try:
                await asyncio.wait_for(
                    client.download_profile_photo(entity, file=stream, download_big=True),
                    FETCH_TIMEOUT_SECONDS,
                )
                data = stream.getvalue()
            finally:
                stream.close()
            media_type = sniff_media_type(data)
            if not media_type:
                raise ValueError("下载内容不是可识别的图片格式")
            await self._persist(photo_id, data)
            self._index[channel_id] = photo_id
            self._refreshed_at[channel_id] = time.monotonic()
            self._negative.pop(channel_id, None)
            return data, media_type, photo_id

    def _select_client(self):
        """主客户端在线优先，否则退回 Bot 客户端；两者皆不可用视为失败。"""
        client, bot_client = self.client_provider()
        if client is not None and client.is_connected():
            return client
        if bot_client is not None:
            return bot_client
        raise LookupError("wiki_avatar_client_unavailable")

    async def _cached(self, photo_id):
        path = self.root / f"{photo_id}.jpg"
        try:
            data = await asyncio.to_thread(path.read_bytes)
        except OSError:
            return None
        media_type = sniff_media_type(data)
        if not media_type:
            return None
        return data, media_type, photo_id

    async def _persist(self, photo_id, data):
        """临时文件 + 原子替换落盘；目录懒创建，与 data/ 的相对路径约定一致。"""
        destination = self.root / f"{photo_id}.jpg"

        def persist():
            self.root.mkdir(parents=True, exist_ok=True)
            fd, temporary = tempfile.mkstemp(prefix="avatar-", dir=self.root)
            try:
                with os.fdopen(fd, "wb") as stream:
                    stream.write(data)
                    stream.flush()
                    os.fsync(stream.fileno())
                os.replace(temporary, destination)
            finally:
                Path(temporary).unlink(missing_ok=True)

        await asyncio.to_thread(persist)

    def _schedule_revalidate(self, channel_id):
        """过期缓存仍先返回旧图，后台刷新一次；失败仅记录，不影响本次响应。

        同一频道同时只允许一个排队中的后台刷新，防止高并发请求把
        Telegram 回源任务在锁上排队堆积。"""

        async def revalidate():
            try:
                await asyncio.wait_for(self.refresh(channel_id), FETCH_TIMEOUT_SECONDS)
            except Exception as error:
                logger.warning("频道头像后台刷新失败 (%s): %s", channel_id, type(error).__name__)
            finally:
                self._revalidating.discard(channel_id)

        if channel_id in self._revalidating:
            return
        self._revalidating.add(channel_id)
        task = asyncio.create_task(revalidate())
        self._tasks.add(task)
        task.add_done_callback(self._tasks.discard)
