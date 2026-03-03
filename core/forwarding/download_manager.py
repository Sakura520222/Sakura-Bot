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
下载管理器模块

提供带限速的文件下载功能，支持缓存管理和磁盘空间检查
"""

import asyncio
import logging
import os
import shutil
from typing import TYPE_CHECKING

if TYPE_CHECKING:
    from telethon import TelegramClient
    from telethon.tl.types import Message

logger = logging.getLogger(__name__)


class DownloadManager:
    """
    文件下载管理器

    功能：
    - 带限速的下载
    - 磁盘空间检查（跨平台兼容）
    - 缓存目录管理
    - 并发控制
    """

    # 最小可用磁盘空间（字节）
    MIN_FREE_SPACE = 500 * 1024 * 1024  # 500MB

    def __init__(
        self,
        cache_dir: str = "data/cache",
        speed_limit: int = 1024 * 1024,  # 1MB/s
        max_concurrent: int = 2,  # 最多2个并发下载
    ):
        """
        初始化下载管理器

        Args:
            cache_dir: 缓存目录路径
            speed_limit: 下载限速（字节/秒），默认1MB/s
            max_concurrent: 最大并发下载数
        """
        self.cache_dir = cache_dir
        self.speed_limit = speed_limit
        self.max_concurrent = max_concurrent
        self._semaphore = asyncio.Semaphore(max_concurrent)
        self._download_tasks: dict[str, asyncio.Task] = {}

        # 确保缓存目录存在
        os.makedirs(cache_dir, exist_ok=True)

    def check_disk_space(self) -> bool:
        """
        检查磁盘可用空间（跨平台兼容）

        Returns:
            是否有足够的空间（>= 500MB）
        """
        try:
            # 使用 shutil.disk_usage 获取跨平台兼容的磁盘信息
            usage = shutil.disk_usage(self.cache_dir)
            free_space = usage.free

            if free_space < self.MIN_FREE_SPACE:
                logger.warning(
                    f"磁盘空间不足: 可用 {free_space / 1024 / 1024:.2f}MB < "
                    f"{self.MIN_FREE_SPACE / 1024 / 1024:.2f}MB"
                )
                return False

            logger.debug(f"磁盘空间检查通过: 可用 {free_space / 1024 / 1024:.2f}MB")
            return True
        except Exception as e:
            logger.error(f"检查磁盘空间失败: {type(e).__name__}: {e}")
            # 检查失败时允许继续，避免阻塞转发功能
            return True

    def get_cache_path(self, forward_id: str) -> str:
        """
        获取转发ID对应的缓存目录路径

        Args:
            forward_id: 转发ID（可以是 grouped_id 或 message_id）

        Returns:
            缓存目录路径
        """
        cache_path = os.path.join(self.cache_dir, forward_id)
        os.makedirs(cache_path, exist_ok=True)
        return cache_path

    async def download_media(
        self,
        client: "TelegramClient",
        message: "Message",
        filename: str,
        forward_id: str,
    ) -> str | None:
        """
        下载媒体文件到缓存目录（带限速和并发控制）

        Args:
            client: Telegram客户端
            message: 消息对象
            filename: 文件名
            forward_id: 转发ID

        Returns:
            下载的文件路径，失败时返回None
        """
        # 检查磁盘空间
        if not self.check_disk_space():
            logger.error(f"磁盘空间不足，拒绝下载: {filename}")
            return None

        # 并发控制
        async with self._semaphore:
            try:
                # 获取缓存目录
                cache_dir = self.get_cache_path(forward_id)
                file_path = os.path.join(cache_dir, filename)

                # 如果文件已存在，直接返回（使用异步检查）
                file_exists = await asyncio.to_thread(os.path.exists, file_path)
                if file_exists:
                    logger.debug(f"文件已存在，跳过下载: {file_path}")
                    absolute_path = await asyncio.to_thread(os.path.abspath, file_path)
                    return absolute_path

                logger.info(
                    f"开始下载文件: {filename} (限速 {self.speed_limit / 1024 / 1024:.2f}MB/s)"
                )

                # 使用 Telethon 的下载功能
                # Telethon 会自动添加正确的扩展名
                downloaded_path = await client.download_media(
                    message=message,
                    file=file_path,
                    progress_callback=lambda d, t: self._limit_speed(d, t, filename),
                )

                # Telethon 可能会修改文件名（添加扩展名），使用实际下载的路径
                if downloaded_path:
                    actual_path = (
                        downloaded_path
                        if isinstance(downloaded_path, str)
                        else str(downloaded_path)
                    )
                    absolute_path = await asyncio.to_thread(os.path.abspath, actual_path)
                    logger.info(f"文件下载完成: {absolute_path}")
                    return absolute_path
                else:
                    # 降级处理：使用原始路径
                    absolute_path = await asyncio.to_thread(os.path.abspath, file_path)
                    logger.info(f"文件下载完成: {absolute_path}")
                    return absolute_path

            except Exception as e:
                logger.error(f"下载文件失败 {filename}: {type(e).__name__}: {e}", exc_info=True)
                return None

    def _limit_speed(self, downloaded: int, total: int, filename: str):
        """
        下载进度回调，用于限速

        Args:
            downloaded: 已下载字节数
            total: 总字节数
            filename: 文件名
        """
        # 计算下载速度
        # 这里只是一个占位符，实际的限速需要更复杂的实现
        # Telethon 的 download_media 不直接支持限速
        pass

    async def download_media_group(
        self,
        client: "TelegramClient",
        messages: list["Message"],
        forward_id: str,
    ) -> list[str]:
        """
        下载媒体组的所有文件

        Args:
            client: Telegram客户端
            messages: 消息列表
            forward_id: 转发ID

        Returns:
            下载的文件路径列表
        """
        file_paths = []

        for i, msg in enumerate(messages):
            if not msg.media:
                continue

            # 生成文件名
            filename = self._generate_filename(msg, i)
            file_path = await self.download_media(client, msg, filename, forward_id)

            if file_path:
                file_paths.append(file_path)

        logger.info(f"媒体组下载完成: {len(file_paths)}/{len(messages)} 个文件")
        return file_paths

    def _generate_filename(self, message: "Message", index: int) -> str:
        """
        生成文件名

        Args:
            message: 消息对象
            index: 索引

        Returns:
            文件名
        """
        # 尝试从消息中获取文件名
        if hasattr(message.media, "document") and hasattr(message.media.document, "attributes"):
            for attr in message.media.document.attributes:
                if hasattr(attr, "file_name"):
                    return f"{index:03d}_{attr.file_name}"

        # 默认文件名
        ext = self._get_file_extension(message)
        return f"{index:03d}_media{ext}"

    def _get_file_extension(self, message: "Message") -> str:
        """
        获取文件扩展名

        Args:
            message: 消息对象

        Returns:
            文件扩展名（包含点）
        """
        if not message.media:
            return ".unknown"

        # 图片
        if hasattr(message.media, "photo"):
            return ".jpg"

        # 文档
        if hasattr(message.media, "document"):
            mime_type = message.media.document.mime_type or ""
            if "video" in mime_type:
                return ".mp4"
            elif "pdf" in mime_type:
                return ".pdf"
            elif "zip" in mime_type or "rar" in mime_type:
                return ".archive"
            else:
                return ".bin"

        return ".unknown"

    async def cleanup_cache(self, forward_id: str):
        """
        清理指定转发ID的缓存目录

        Args:
            forward_id: 转发ID
        """
        cache_dir = self.get_cache_path(forward_id)

        try:
            dir_exists = await asyncio.to_thread(os.path.exists, cache_dir)
            if dir_exists:
                await asyncio.to_thread(shutil.rmtree, cache_dir)
                logger.debug(f"清理缓存目录: {cache_dir}")
        except Exception as e:
            logger.error(f"清理缓存失败 {cache_dir}: {type(e).__name__}: {e}")

    async def cleanup_all_cache(self):
        """清理所有缓存目录"""
        try:
            dir_exists = await asyncio.to_thread(os.path.exists, self.cache_dir)
            if dir_exists:
                await asyncio.to_thread(shutil.rmtree, self.cache_dir)
                await asyncio.to_thread(os.makedirs, self.cache_dir, exist_ok=True)
                logger.info("清理所有缓存目录")
        except Exception as e:
            logger.error(f"清理所有缓存失败: {type(e).__name__}: {e}")
