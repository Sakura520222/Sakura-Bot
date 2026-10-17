# Copyright 2026 Sakura-Bot
#
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
#
# - 署名：必须提供本项目的原始来源链接
# - 相同方式共享：衍生作品必须采用相同的许可证
#
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
# 许可证全文：参见 LICENSE 文件

"""缓存管理模块

此模块提供讨论组 ID 缓存功能，避免频繁调用 Telegram API。
支持 LRU 淘汰策略和 TTL 过期机制。
"""

import json
import logging
import time
from collections import OrderedDict
from pathlib import Path

from core.infrastructure.utils.constants import DATA_DIR

logger = logging.getLogger(__name__)

# 缓存文件路径
CACHE_FILE = Path(DATA_DIR) / "discussion_cache.json"

# 缓存配置
DEFAULT_MAX_SIZE = 1000  # 最大缓存条目数
DEFAULT_TTL = 86400  # 默认 TTL（24小时，单位：秒）


class DiscussionCache:
    """讨论组 ID 缓存管理类

    提供内存缓存和持久化存储功能。
    支持 LRU 淘汰策略和 TTL 过期机制。
    """

    def __init__(self, max_size: int = DEFAULT_MAX_SIZE, ttl: int = DEFAULT_TTL):
        """初始化缓存

        Args:
            max_size: 最大缓存条目数（LRU淘汰阈值）
            ttl: 缓存条目生存时间（秒），0 表示永不过期
        """
        self._max_size = max_size
        self._ttl = ttl
        # OrderedDict 实现 LRU：key -> (value, timestamp)
        self._memory_cache: OrderedDict[str, tuple[int, float]] = OrderedDict()
        self._load_from_disk()

    def _is_expired(self, timestamp: float) -> bool:
        """检查缓存条目是否过期

        Args:
            timestamp: 缓存时间戳

        Returns:
            是否过期
        """
        if self._ttl == 0:
            return False
        return time.time() - timestamp > self._ttl

    def _evict_if_needed(self) -> None:
        """如果缓存超过容量，淘汰最旧的条目（LRU）"""
        # 先清理过期条目
        if self._ttl > 0:
            expired_keys = [
                key
                for key, (_, timestamp) in self._memory_cache.items()
                if self._is_expired(timestamp)
            ]
            for key in expired_keys:
                del self._memory_cache[key]
            if expired_keys:
                logger.debug(f"清理过期缓存: {len(expired_keys)} 条")

        # 如果仍然超过容量，淘汰最旧的条目
        while len(self._memory_cache) >= self._max_size:
            key, _ = self._memory_cache.popitem(last=False)
            logger.debug(f"LRU淘汰缓存条目: {key}")

    def _move_to_end(self, key: str) -> None:
        """将访问的条目移到末尾（标记为最近使用）"""
        if key in self._memory_cache:
            self._memory_cache.move_to_end(key)

    def _load_from_disk(self) -> None:
        """从磁盘加载缓存数据（支持新旧格式兼容）"""
        if CACHE_FILE.exists():
            try:
                with open(CACHE_FILE, encoding="utf-8") as f:
                    data = json.load(f)

                # 转换旧格式 {key: value} 为新格式 {key: (value, timestamp)}
                for key, value in data.items():
                    if isinstance(value, (list, tuple)) and len(value) == 2:
                        # 新格式: (value, timestamp)
                        self._memory_cache[key] = tuple(value)
                    else:
                        # 旧格式: value -> 转换为新格式，使用当前时间戳
                        self._memory_cache[key] = (value, time.time())

                logger.info(f"已从磁盘加载讨论组缓存: {len(self._memory_cache)} 条记录")
            except json.JSONDecodeError as e:
                logger.error(f"缓存文件格式错误: {e}，将使用空缓存")
                self._memory_cache = OrderedDict()
            except Exception as e:
                logger.error(f"加载缓存文件失败: {e}，将使用空缓存")
                self._memory_cache = OrderedDict()
        else:
            logger.info("缓存文件不存在，使用空缓存")
            self._memory_cache = OrderedDict()

    def _save_to_disk(self) -> None:
        """将缓存数据保存到磁盘（只保存值，不保存时间戳）"""
        try:
            # 确保目录存在
            CACHE_FILE.parent.mkdir(parents=True, exist_ok=True)

            # 只保存值，不保存时间戳（重启后会重新计算 TTL）
            data = {key: value for key, (value, _) in self._memory_cache.items()}

            with open(CACHE_FILE, "w", encoding="utf-8") as f:
                json.dump(data, f, ensure_ascii=False, indent=2)
            logger.debug("缓存数据已保存到磁盘")
        except Exception as e:
            logger.error(f"保存缓存数据到磁盘失败: {e}")

    def get(self, channel_url: str) -> int | None:
        """获取缓存的讨论组 ID

        Args:
            channel_url: 频道 URL

        Returns:
            讨论组 ID，如果不存在或已过期则返回 None
        """
        entry = self._memory_cache.get(channel_url)
        if entry is None:
            return None

        value, timestamp = entry
        # 检查是否过期
        if self._is_expired(timestamp):
            del self._memory_cache[channel_url]
            logger.debug(f"缓存已过期: {channel_url}")
            return None

        # 标记为最近使用
        self._move_to_end(channel_url)
        return value

    def set(self, channel_url: str, discussion_group_id: int) -> None:
        """缓存讨论组 ID

        Args:
            channel_url: 频道 URL
            discussion_group_id: 讨论组 ID（已转换为超级群组格式）
        """
        # 检查容量，必要时淘汰
        self._evict_if_needed()

        # 存储值和时间戳
        self._memory_cache[channel_url] = (discussion_group_id, time.time())
        self._move_to_end(channel_url)
        logger.debug(f"已缓存讨论组 ID: {channel_url} -> {discussion_group_id}")
        # 保存到磁盘
        self._save_to_disk()

    def delete(self, channel_url: str) -> bool:
        """删除指定频道的缓存

        Args:
            channel_url: 频道 URL

        Returns:
            是否成功删除
        """
        if channel_url in self._memory_cache:
            del self._memory_cache[channel_url]
            logger.info(f"已删除频道 {channel_url} 的讨论组缓存")
            self._save_to_disk()
            return True
        return False

    def clean_expired(self) -> int:
        """清理所有过期的缓存条目

        Returns:
            清理的条目数量
        """
        if self._ttl == 0:
            return 0

        expired_keys = [
            key for key, (_, timestamp) in self._memory_cache.items() if self._is_expired(timestamp)
        ]

        for key in expired_keys:
            del self._memory_cache[key]

        if expired_keys:
            logger.info(f"清理过期缓存: {len(expired_keys)} 条")
            self._save_to_disk()

        return len(expired_keys)

    def clear(self) -> None:
        """清除所有缓存"""
        count = len(self._memory_cache)
        self._memory_cache.clear()
        logger.info(f"已清除所有讨论组缓存: {count} 条记录")
        self._save_to_disk()

    def get_all(self) -> dict[str, int]:
        """获取所有缓存数据

        Returns:
            所有缓存的字典
        """
        return {key: value for key, (value, _) in self._memory_cache.items()}

    def size(self) -> int:
        """获取缓存条目数量

        Returns:
            缓存条目数量
        """
        return len(self._memory_cache)


# 全局缓存实例
_discussion_cache: DiscussionCache | None = None


def get_discussion_cache() -> DiscussionCache:
    """获取全局讨论组缓存实例（单例模式）

    Returns:
        DiscussionCache 实例
    """
    global _discussion_cache
    if _discussion_cache is None:
        _discussion_cache = DiscussionCache()
        logger.info("讨论组缓存已初始化")
    return _discussion_cache


# 便捷函数（保持向后兼容）
def get_cached_discussion_group_id(channel_url: str) -> int | None:
    """获取缓存的讨论组 ID

    Args:
        channel_url: 频道 URL

    Returns:
        讨论组 ID，如果不存在则返回 None
    """
    return get_discussion_cache().get(channel_url)


def cache_discussion_group_id(channel_url: str, discussion_group_id: int) -> None:
    """缓存讨论组 ID

    Args:
        channel_url: 频道 URL
        discussion_group_id: 讨论组 ID（已转换为超级群组格式）
    """
    get_discussion_cache().set(channel_url, discussion_group_id)


def clear_discussion_group_cache(channel_url: str | None = None) -> None:
    """清除讨论组 ID 缓存

    Args:
        channel_url: 可选，指定要清除的频道 URL。如果为 None 则清除所有缓存
    """
    cache = get_discussion_cache()
    if channel_url:
        cache.delete(channel_url)
    else:
        cache.clear()
