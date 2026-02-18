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

"""缓存管理模块

此模块提供讨论组 ID 缓存功能，避免频繁调用 Telegram API。
"""

import logging
import json
from pathlib import Path
from typing import dict as dict_type

from .constants import DATA_DIR

logger = logging.getLogger(__name__)

# 缓存文件路径
CACHE_FILE = Path(DATA_DIR) / "discussion_cache.json"


class DiscussionCache:
    """讨论组 ID 缓存管理类
    
    提供内存缓存和持久化存储功能。
    """
    
    def __init__(self):
        """初始化缓存"""
        self._memory_cache: dict_type[str, int] = {}
        self._load_from_disk()
    
    def _load_from_disk(self) -> None:
        """从磁盘加载缓存数据"""
        if CACHE_FILE.exists():
            try:
                with open(CACHE_FILE, 'r', encoding='utf-8') as f:
                    self._memory_cache = json.load(f)
                logger.info(f"已从磁盘加载讨论组缓存: {len(self._memory_cache)} 条记录")
            except json.JSONDecodeError as e:
                logger.error(f"缓存文件格式错误: {e}，将使用空缓存")
                self._memory_cache = {}
            except Exception as e:
                logger.error(f"加载缓存文件失败: {e}，将使用空缓存")
                self._memory_cache = {}
        else:
            logger.info("缓存文件不存在，使用空缓存")
            self._memory_cache = {}
    
    def _save_to_disk(self) -> None:
        """将缓存数据保存到磁盘"""
        try:
            # 确保目录存在
            CACHE_FILE.parent.mkdir(parents=True, exist_ok=True)
            
            with open(CACHE_FILE, 'w', encoding='utf-8') as f:
                json.dump(self._memory_cache, f, ensure_ascii=False, indent=2)
            logger.debug("缓存数据已保存到磁盘")
        except Exception as e:
            logger.error(f"保存缓存数据到磁盘失败: {e}")
    
    def get(self, channel_url: str) -> int | None:
        """获取缓存的讨论组 ID
        
        Args:
            channel_url: 频道 URL
        
        Returns:
            讨论组 ID，如果不存在则返回 None
        """
        return self._memory_cache.get(channel_url)
    
    def set(self, channel_url: str, discussion_group_id: int) -> None:
        """缓存讨论组 ID
        
        Args:
            channel_url: 频道 URL
            discussion_group_id: 讨论组 ID（已转换为超级群组格式）
        """
        self._memory_cache[channel_url] = discussion_group_id
        logger.debug(f"已缓存讨论组 ID: {channel_url} -> {discussion_group_id}")
        # 异步保存到磁盘（避免频繁 I/O）
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
    
    def clear(self) -> None:
        """清除所有缓存"""
        count = len(self._memory_cache)
        self._memory_cache.clear()
        logger.info(f"已清除所有讨论组缓存: {count} 条记录")
        self._save_to_disk()
    
    def get_all(self) -> dict_type[str, int]:
        """获取所有缓存数据
        
        Returns:
            所有缓存的字典
        """
        return self._memory_cache.copy()
    
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