"""测试 Cache Manager 模块

Copyright 2026 Sakura-Bot
本项目采用 AGPL-3.0 许可
"""

import json
from pathlib import Path
from unittest.mock import mock_open, patch

import pytest

from core.cache_manager import (
    DiscussionCache,
    cache_discussion_group_id,
    clear_discussion_group_cache,
    get_cached_discussion_group_id,
    get_discussion_cache,
)


@pytest.mark.unit
class TestDiscussionCache:
    """讨论组缓存测试"""

    def setup_method(self):
        """每个测试前重置全局缓存"""
        import core.cache_manager

        core.cache_manager._discussion_cache = None

    def test_init_loads_from_disk(self):
        """测试初始化从磁盘加载"""
        mock_data = {"channel1": -100123, "channel2": -100456}

        with patch("core.cache_manager.CACHE_FILE") as mock_file:
            mock_file.exists.return_value = True
            with patch("builtins.open", mock_open(read_data=json.dumps(mock_data))):
                cache = DiscussionCache()

                assert cache.get_all() == mock_data

    def test_init_with_no_file(self):
        """测试文件不存在时初始化"""
        with patch("core.cache_manager.CACHE_FILE") as mock_file:
            mock_file.exists.return_value = False
            cache = DiscussionCache()

            assert cache.size() == 0

    def test_init_with_invalid_json(self):
        """测试加载无效JSON"""
        with patch("core.cache_manager.CACHE_FILE") as mock_file:
            mock_file.exists.return_value = True
            with patch("builtins.open", mock_open(read_data="invalid json")):
                cache = DiscussionCache()

                assert cache.size() == 0

    def test_init_with_read_error(self):
        """测试读取错误"""
        with patch("core.cache_manager.CACHE_FILE") as mock_file:
            mock_file.exists.return_value = True
            with patch("builtins.open", side_effect=PermissionError("No permission")):
                cache = DiscussionCache()

                assert cache.size() == 0

    def test_get_existing_channel(self):
        """测试获取存在的频道"""
        cache = DiscussionCache()
        cache.set("channel1", -100123)

        result = cache.get("channel1")

        assert result == -100123

    def test_get_nonexistent_channel(self):
        """测试获取不存在的频道"""
        cache = DiscussionCache()

        result = cache.get("nonexistent")

        assert result is None

    def test_set_discussion_id(self):
        """测试设置讨论组ID"""
        cache = DiscussionCache()

        cache.set("channel1", -100123)

        assert cache.get("channel1") == -100123
        assert cache.size() == 1

    def test_delete_existing_channel(self):
        """测试删除存在的频道"""
        cache = DiscussionCache()
        cache.set("channel1", -100123)

        result = cache.delete("channel1")

        assert result is True
        assert cache.get("channel1") is None
        assert cache.size() == 0

    def test_delete_nonexistent_channel(self):
        """测试删除不存在的频道"""
        cache = DiscussionCache()

        result = cache.delete("nonexistent")

        assert result is False

    def test_clear_all_cache(self):
        """测试清除所有缓存"""
        cache = DiscussionCache()
        cache.set("channel1", -100123)
        cache.set("channel2", -100456)

        cache.clear()

        assert cache.size() == 0

    def test_get_all(self):
        """测试获取所有缓存"""
        cache = DiscussionCache()
        cache.set("channel1", -100123)
        cache.set("channel2", -100456)

        result = cache.get_all()

        assert result == {"channel1": -100123, "channel2": -100456}
        # 确保返回的是副本
        result["channel3"] = -100789
        assert cache.get("channel3") is None

    def test_size(self, tmp_path):
        """测试获取缓存大小"""
        # 使用临时文件避免数据污染
        cache_file = tmp_path / "test_size_cache.json"

        with patch("core.cache_manager.CACHE_FILE", cache_file):
            cache = DiscussionCache()

            assert cache.size() == 0

            cache.set("channel1", -100123)
            assert cache.size() == 1

            cache.set("channel2", -100456)
            assert cache.size() == 2

    def test_save_to_disk(self, tmp_path):
        """测试保存到磁盘"""
        cache_file = tmp_path / "test_cache.json"

        with patch("core.cache_manager.CACHE_FILE", cache_file):
            cache = DiscussionCache()
            cache.set("channel1", -100123)

            # 文件应该存在并包含数据
            assert cache_file.exists()
            data = json.loads(cache_file.read_text())
            assert data == {"channel1": -100123}

    def test_save_to_disk_error(self):
        """测试保存失败（使用无效路径）"""
        # 使用无效路径模拟保存失败
        invalid_path = Path("/invalid/nonexistent/path/cache.json")

        with patch("core.cache_manager.CACHE_FILE", invalid_path):
            cache = DiscussionCache()
            # 不应该在初始化时抛出异常
            cache.set("channel1", -100123)
            # 保存失败时不应该抛出异常
            cache._save_to_disk()


@pytest.mark.unit
class TestGetDiscussionCache:
    """获取缓存实例测试"""

    def test_get_discussion_cache_singleton(self):
        """测试单例模式"""
        # 重置全局变量
        import core.cache_manager

        core.cache_manager._discussion_cache = None

        cache1 = get_discussion_cache()
        cache2 = get_discussion_cache()

        assert cache1 is cache2

    def test_get_discussion_cache_creates_instance(self):
        """测试创建实例"""
        # 重置全局变量
        import core.cache_manager

        core.cache_manager._discussion_cache = None

        cache = get_discussion_cache()

        assert isinstance(cache, DiscussionCache)


@pytest.mark.unit
class TestConvenienceFunctions:
    """便捷函数测试"""

    def test_get_cached_discussion_group_id(self):
        """测试获取缓存的讨论组ID"""
        # 重置全局变量
        import core.cache_manager

        core.cache_manager._discussion_cache = None

        cache = get_discussion_cache()
        cache.set("channel1", -100123)

        result = get_cached_discussion_group_id("channel1")

        assert result == -100123

    def test_cache_discussion_group_id(self):
        """测试缓存讨论组ID"""
        # 重置全局变量
        import core.cache_manager

        core.cache_manager._discussion_cache = None

        cache_discussion_group_id("channel1", -100123)

        result = get_cached_discussion_group_id("channel1")

        assert result == -100123

    def test_clear_discussion_group_cache_specific_channel(self):
        """测试清除特定频道缓存"""
        # 重置全局变量
        import core.cache_manager

        core.cache_manager._discussion_cache = None

        cache = get_discussion_cache()
        cache.set("channel1", -100123)
        cache.set("channel2", -100456)

        clear_discussion_group_cache("channel1")

        assert cache.get("channel1") is None
        assert cache.get("channel2") == -100456

    def test_clear_discussion_group_cache_all(self):
        """测试清除所有缓存"""
        # 重置全局变量
        import core.cache_manager

        core.cache_manager._discussion_cache = None

        cache = get_discussion_cache()
        cache.set("channel1", -100123)
        cache.set("channel2", -100456)

        clear_discussion_group_cache()

        assert cache.size() == 0


@pytest.mark.unit
class TestCachePersistence:
    """缓存持久化测试"""

    def test_save_and_load(self, tmp_path):
        """测试保存和加载"""
        # 使用临时文件
        cache_file = tmp_path / "test_cache.json"

        with patch("core.cache_manager.CACHE_FILE", cache_file):
            # 创建并保存缓存
            cache1 = DiscussionCache()
            cache1.set("channel1", -100123)
            cache1.set("channel2", -100456)

            # 创建新实例并加载
            cache2 = DiscussionCache()

            assert cache2.get("channel1") == -100123
            assert cache2.get("channel2") == -100456

    def test_save_creates_directory(self, tmp_path):
        """测试保存时创建目录"""
        cache_file = tmp_path / "subdir" / "cache.json"

        with patch("core.cache_manager.CACHE_FILE", cache_file):
            cache = DiscussionCache()
            cache.set("channel1", -100123)

            assert cache_file.exists()


if __name__ == "__main__":
    pytest.main([__file__, "-v"])
