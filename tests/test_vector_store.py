"""测试 Vector Store 门面模块（占位）

旧测试针对 chroma patch 点（CHROMADB_AVAILABLE / chromadb.PersistentClient）编写，
lite 后端落地后失效；完整测试套件由下一阶段按新设计重写。
本文件当前仅保证模块层面契约：可正常 import、VECTOR_BACKEND 路由正确、
lite 模式不加载 chromadb、单例与模块属性置空语义不变。

Copyright 2026 Sakura-Bot
本项目采用 AGPL-3.0 许可
"""

import sys

import pytest

import core.ai.vector_store as vector_store_module
from core.ai.vector_store import VectorStore, get_vector_store


@pytest.mark.unit
def test_module_imports():
    """模块导入层面不炸：门面符号齐全"""
    assert hasattr(vector_store_module, "VectorStore")
    assert hasattr(vector_store_module, "get_vector_store")
    assert vector_store_module.vector_store is None or isinstance(
        vector_store_module.vector_store, VectorStore
    )


@pytest.mark.unit
def test_lite_backend_does_not_import_chromadb(monkeypatch, tmp_path):
    """VECTOR_BACKEND=lite 时 chromadb 不得出现在 sys.modules"""
    monkeypatch.setenv("VECTOR_BACKEND", "lite")
    monkeypatch.setenv("VECTOR_DB_PATH", str(tmp_path))

    store = VectorStore()

    assert store.backend == "lite"
    assert store.collection is not None
    assert store.messages_collection is not None
    assert "chromadb" not in sys.modules


@pytest.mark.unit
def test_unknown_backend_falls_back_to_chroma(monkeypatch, tmp_path):
    """未知 VECTOR_BACKEND 告警后回退 chroma"""
    monkeypatch.setenv("VECTOR_BACKEND", "dual")
    monkeypatch.setenv("VECTOR_DB_PATH", str(tmp_path))

    store = VectorStore()

    assert store.backend == "chroma"


@pytest.mark.unit
def test_get_vector_store_singleton(monkeypatch, tmp_path):
    """懒加载单例与模块属性置空语义保持原样"""
    monkeypatch.setenv("VECTOR_BACKEND", "lite")
    monkeypatch.setenv("VECTOR_DB_PATH", str(tmp_path))
    monkeypatch.setattr(vector_store_module, "vector_store", None)

    store1 = get_vector_store()
    store2 = get_vector_store()

    assert store1 is store2
    assert isinstance(store1, VectorStore)


if __name__ == "__main__":
    pytest.main([__file__, "-v"])
