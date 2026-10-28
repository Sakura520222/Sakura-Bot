# Copyright 2026 Sakura-Bot
#
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
#
# - 署名：必须提供本项目的原始来源链接
# - 相同方式共享：衍生作品必须采用相同的许可证
#
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
# 许可证全文：参见 LICENSE 文件

"""Vector Store 门面与 lite 后端契约测试

覆盖 core/ai/vector_store.py（双后端门面）与 core/ai/vector_lite.py（SQLite+numpy 引擎）：
doc_id 解析三红线、写入语义（add 重复拒绝 / upsert 幂等 / 覆盖 / 幂等删除 / 维度校验）、
搜索与过滤语义（降序、top_k、similarity=1-distance、metadata 等值与 AND、日期边界）、
search_all 合并注入、get_stats 形状、chroma 兼容适配器契约、跨实例版本失效、
读者只读模式（mode=ro、不执行 DDL、写请求降级），以及构造失败降级不抛异常。

所有用例运行于 VECTOR_BACKEND=lite + tmp_path SQLite；embedding 由确定性正交向量桩提供。
"""

import math
import os
import subprocess
import sys
import zlib
from pathlib import Path

import pytest

import core.ai.vector_store as vector_store_module
from core.ai.vector_store import VectorStore, get_vector_store

_DIM = 8
_REPO_ROOT = Path(__file__).resolve().parent.parent


def _basis(*indices: int) -> list[float]:
    """构造可预测正交向量（单位基向量之和）"""
    vec = [0.0] * _DIM
    for index in indices:
        vec[index] = 1.0
    return vec


class _StubEmbeddingGenerator:
    """确定性 embedding 桩：预登记文本返回登记向量，未登记文本按 crc32 落到基向量"""

    def __init__(self) -> None:
        self._vectors: dict[str, list[float]] = {}

    def register(self, text: str, vector: list[float]) -> None:
        """预登记 文本 -> 向量 映射，保证搜索测试的相似度可精确预测"""
        self._vectors[text] = list(vector)

    def is_available(self) -> bool:
        return True

    def generate(self, text: str) -> list[float]:
        vector = self._vectors.get(text)
        if vector is None:
            vector = _basis(zlib.crc32(text.encode("utf-8")) % _DIM)
            self._vectors[text] = vector
        return list(vector)


@pytest.fixture
def stub_embeddings(monkeypatch):
    """以桩替换 core.ai.embedding_generator.get_embedding_generator（门面为方法内懒加载）"""
    import core.ai.embedding_generator as embedding_module

    stub = _StubEmbeddingGenerator()
    monkeypatch.setattr(embedding_module, "get_embedding_generator", lambda: stub)
    return stub


@pytest.fixture
def lite_store(monkeypatch, tmp_path, stub_embeddings):
    """lite 后端 VectorStore：独立 SQLite 文件 + 8 维向量；每测试前重置模块单例"""
    monkeypatch.setenv("VECTOR_BACKEND", "lite")
    monkeypatch.setenv("VECTOR_DB_PATH", str(tmp_path / "vectors"))
    monkeypatch.setenv("EMBEDDING_DIMENSION", str(_DIM))
    monkeypatch.delenv("VECTOR_BACKEND_READER", raising=False)
    monkeypatch.setattr(vector_store_module, "vector_store", None)
    return VectorStore()


def _seed_similarity_ladder(store, stub) -> None:
    """写入相似度 1.0 / sqrt(0.5) / 0.0 的三条总结与查询文本"""
    stub.register("ladder query", _basis(0))
    stub.register("ladder exact", _basis(0))
    stub.register("ladder partial", _basis(0, 1))
    stub.register("ladder orthogonal", _basis(1))
    store.add_summary(1, "ladder exact", {"channel_id": "100"})
    store.add_summary(2, "ladder partial", {"channel_id": "100"})
    store.add_summary(3, "ladder orthogonal", {"channel_id": "100"})


def _seed_filter_rows(store) -> None:
    """写入 channel_id / summary_type 组合不同的两条总结（同文本同向量）"""
    store.add_summary(11, "filter target text", {"channel_id": "100", "summary_type": "daily"})
    store.add_summary(12, "filter target text", {"channel_id": "200", "summary_type": "manual"})


def _seed_dated_rows(store) -> None:
    """写入 created_at 相差一个月的两条总结"""
    store.add_summary(31, "dated may text", {"created_at": "2024-05-01T00:00:00+00:00"})
    store.add_summary(32, "dated june text", {"created_at": "2024-06-15T00:00:00+00:00"})


def _seed_search_all_rows(store, stub) -> None:
    """双边各写一条：summary 相似度 1.0，message 相似度 sqrt(0.5)"""
    stub.register("search all query", _basis(0))
    stub.register("search all summary", _basis(0))
    stub.register("search all message", _basis(0, 2))
    store.add_summary(41, "search all summary", {"channel_id": "100"})
    store.add_message(
        "60:7", "search all message", {"channel_id": "60"}, stub.generate("search all message")
    )


# ==================== 模块层面契约 ====================


@pytest.mark.unit
def test_module_imports():
    """模块导入层面不炸：门面符号齐全"""
    assert hasattr(vector_store_module, "VectorStore")
    assert hasattr(vector_store_module, "get_vector_store")
    assert vector_store_module.vector_store is None or isinstance(
        vector_store_module.vector_store, VectorStore
    )


# lite 进程内探针：构造 VectorStore 后以退出码断言 chromadb 未被加载
_SUBPROCESS_NO_CHROMADB_PROBE = (
    "import sys\n"
    "import core.ai.vector_store as vector_store_module\n"
    "store = vector_store_module.VectorStore()\n"
    "assert store.backend == 'lite', store.backend\n"
    "assert store.collection is not None\n"
    "assert store.messages_collection is not None\n"
    "sys.exit(1 if 'chromadb' in sys.modules else 0)\n"
)


@pytest.mark.unit
def test_lite_backend_does_not_import_chromadb(monkeypatch, tmp_path):
    """VECTOR_BACKEND=lite 时 chromadb 不得被加载（子进程断言，免受会话导入顺序影响）"""
    env = os.environ.copy()
    env["VECTOR_BACKEND"] = "lite"
    env["VECTOR_DB_PATH"] = str(tmp_path)
    env["EMBEDDING_DIMENSION"] = str(_DIM)
    env.pop("VECTOR_BACKEND_READER", None)
    env["PYTHONPATH"] = str(_REPO_ROOT) + os.pathsep + env.get("PYTHONPATH", "")

    proc = subprocess.run(
        [sys.executable, "-c", _SUBPROCESS_NO_CHROMADB_PROBE],
        cwd=str(_REPO_ROOT),
        env=env,
        capture_output=True,
        text=True,
        timeout=120,
        check=False,
    )

    assert proc.returncode == 0, (
        f"lite 进程内加载了 chromadb 或构造失败\nstdout: {proc.stdout}\nstderr: {proc.stderr[-2000:]}"
    )


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


# ==================== doc_id 解析三红线 ====================


@pytest.mark.unit
def test_numeric_doc_id_resolves_summary_id_only(lite_store, stub_embeddings):
    """纯数字 doc_id：summary_id 填充、message_id 必须为 None"""
    stub_embeddings.register("numeric id text", _basis(0))
    assert lite_store.add_summary(77, "numeric id text", {"channel_id": "100"}) is True

    row = lite_store.search_similar("numeric id text", top_k=5)[0]

    assert row["doc_id"] == "77"
    assert row["summary_id"] == 77
    assert row["message_id"] is None


@pytest.mark.unit
def test_channel_message_doc_id_resolves_message_id(lite_store, stub_embeddings):
    """字符串 "channel:msg" doc_id：summary_id 为 None，尾段解析出真实消息 ID"""
    embedding = stub_embeddings.generate("channel msg text")
    assert (
        lite_store.add_message(
            "100:42", "channel msg text", {"channel_id": "100", "message_id": "42"}, embedding
        )
        is True
    )

    row = lite_store.search_messages("channel msg text", top_k=5)[0]

    assert row["doc_id"] == "100:42"
    assert row["summary_id"] is None
    assert row["message_id"] == 42


@pytest.mark.unit
def test_metadata_message_id_fallback_for_non_numeric_doc_id(lite_store, stub_embeddings):
    """doc_id 尾段无数字时，metadata.message_id（字符串）兜底转 int"""
    embedding = stub_embeddings.generate("legacy doc text")
    assert (
        lite_store.add_message(
            "legacy-broadcast", "legacy doc text", {"message_id": "555"}, embedding
        )
        is True
    )

    row = lite_store.search_messages("legacy doc text", top_k=5)[0]

    assert row["doc_id"] == "legacy-broadcast"
    assert row["summary_id"] is None
    assert row["message_id"] == 555


# ==================== 写入语义 ====================


@pytest.mark.unit
def test_add_summary_duplicate_id_returns_false(lite_store, stub_embeddings):
    """add 语义：重复 id 抛异常被捕获并返回 False，行数不增加"""
    stub_embeddings.register("dup text", _basis(0))
    assert lite_store.add_summary(1, "dup text", {"channel_id": "100"}) is True
    assert lite_store.add_summary(1, "dup text", {"channel_id": "100"}) is False
    assert lite_store.collection.count() == 1


@pytest.mark.unit
def test_add_messages_batch_is_idempotent_upsert(lite_store, stub_embeddings):
    """批量 upsert 幂等：重复提交返回 len(ids) 且不产生重复行，覆盖旧内容"""
    ids = ["10:1", "10:2"]
    metas = [{"channel_id": "10"}, {"channel_id": "10"}]
    first_texts = ["batch alpha", "batch beta"]
    assert (
        lite_store.add_messages_batch(
            ids, first_texts, metas, [stub_embeddings.generate(t) for t in first_texts]
        )
        == 2
    )

    second_texts = ["batch alpha v2", "batch beta v2"]
    assert (
        lite_store.add_messages_batch(
            ids, second_texts, metas, [stub_embeddings.generate(t) for t in second_texts]
        )
        == 2
    )

    assert lite_store.messages_collection.count() == 2
    got = lite_store.messages_collection.get(ids=["10:1"], include=["documents"])
    assert got["documents"] == ["batch alpha v2"]


@pytest.mark.unit
def test_update_message_overwrites_row(lite_store, stub_embeddings):
    """update_message 单条 upsert：文本与元数据均被覆盖，不新增行"""
    lite_store.add_message(
        "10:1", "before edit", {"version": 1}, stub_embeddings.generate("before edit")
    )

    assert (
        lite_store.update_message(
            "10:1", "after edit", {"version": 2}, stub_embeddings.generate("after edit")
        )
        is True
    )

    got = lite_store.messages_collection.get(ids=["10:1"], include=["documents", "metadatas"])
    assert got["documents"] == ["after edit"]
    assert got["metadatas"] == [{"version": 2}]
    assert lite_store.messages_collection.count() == 1


@pytest.mark.unit
def test_delete_missing_id_returns_true(lite_store):
    """幂等删除：删除不存在的 id 同样返回 True"""
    assert lite_store.delete_summary(424242) is True
    assert lite_store.delete_message("999:1") is True


@pytest.mark.unit
def test_dimension_mismatch_returns_false(lite_store, stub_embeddings, monkeypatch):
    """维度校验：向量维度不符的写入一律失败（False / 0），不落库"""
    assert lite_store.add_message("10:9", "bad dim", {}, [0.1] * (_DIM + 3)) is False
    assert (
        lite_store.add_messages_batch(["10:10"], ["bad dim batch"], [{}], [[0.1] * (_DIM - 1)]) == 0
    )

    monkeypatch.setattr(stub_embeddings, "generate", lambda _text: [0.1] * (_DIM * 2))
    assert lite_store.add_summary(9, "bad dim summary", {}) is False


# ==================== 搜索语义 ====================


@pytest.mark.unit
def test_search_orders_by_similarity_descending(lite_store, stub_embeddings):
    """搜索结果按相似度严格降序，数值与构造的正交梯度一致"""
    _seed_similarity_ladder(lite_store, stub_embeddings)

    results = lite_store.search_similar("ladder query", top_k=10)

    assert [row["doc_id"] for row in results] == ["1", "2", "3"]
    similarities = [row["similarity"] for row in results]
    assert similarities == sorted(similarities, reverse=True)
    assert similarities[0] == pytest.approx(1.0)
    assert similarities[1] == pytest.approx(math.sqrt(0.5))
    assert similarities[2] == pytest.approx(0.0, abs=1e-6)


@pytest.mark.unit
def test_search_truncates_to_top_k(lite_store, stub_embeddings):
    """top_k 截断：仅保留相似度最高的前 k 条"""
    _seed_similarity_ladder(lite_store, stub_embeddings)

    results = lite_store.search_similar("ladder query", top_k=2)

    assert [row["doc_id"] for row in results] == ["1", "2"]


@pytest.mark.unit
def test_search_empty_collection_returns_empty_list(lite_store):
    """空库搜索返回 [] 而非异常"""
    assert lite_store.search_similar("anything", top_k=5) == []
    assert lite_store.search_messages("anything", top_k=5) == []


@pytest.mark.unit
def test_search_similarity_is_one_minus_distance(lite_store, stub_embeddings):
    """结果形状契约：similarity == 1 - distance"""
    _seed_similarity_ladder(lite_store, stub_embeddings)

    for row in lite_store.search_similar("ladder query", top_k=3):
        assert row["similarity"] == pytest.approx(1.0 - row["distance"])
        assert row["summary_text"]
        assert isinstance(row["metadata"], dict)


@pytest.mark.unit
def test_filter_metadata_string_match_and_int_miss(lite_store):
    """等值过滤：字符串命中；类型严格，int 不命中"""
    _seed_filter_rows(lite_store)

    hits = lite_store.search_similar(
        "filter target text", top_k=10, filter_metadata={"channel_id": "100"}
    )
    assert [row["doc_id"] for row in hits] == ["11"]

    misses = lite_store.search_similar(
        "filter target text", top_k=10, filter_metadata={"channel_id": 100}
    )
    assert misses == []


@pytest.mark.unit
def test_filter_metadata_multi_key_and_semantics(lite_store):
    """多键过滤：所有键同时满足（AND）才命中"""
    _seed_filter_rows(lite_store)

    hits = lite_store.search_similar(
        "filter target text",
        top_k=10,
        filter_metadata={"channel_id": "100", "summary_type": "daily"},
    )
    assert [row["doc_id"] for row in hits] == ["11"]

    conflicts = lite_store.search_similar(
        "filter target text",
        top_k=10,
        filter_metadata={"channel_id": "100", "summary_type": "manual"},
    )
    assert conflicts == []


@pytest.mark.unit
def test_date_filter_normalizes_naive_boundaries_as_utc(lite_store):
    """naive 日期边界按 UTC 归一化后与 aware created_at 正常比较（不触发整体 [] 陷阱）"""
    _seed_dated_rows(lite_store)

    after = lite_store.search_similar("dated", top_k=10, date_after="2024-06-01T00:00:00")
    assert [row["doc_id"] for row in after] == ["32"]

    before = lite_store.search_similar("dated", top_k=10, date_before="2024-06-01T00:00:00")
    assert [row["doc_id"] for row in before] == ["31"]

    window = lite_store.search_similar(
        "dated", top_k=10, date_after="2024-06-01T00:00:00", date_before="2024-06-30T00:00:00"
    )
    assert [row["doc_id"] for row in window] == ["32"]


@pytest.mark.unit
def test_date_filter_ignores_unparseable_boundary(lite_store):
    """不可解析的 date 边界被静默忽略，不缩小结果集"""
    _seed_dated_rows(lite_store)

    results = lite_store.search_similar(
        "dated", top_k=10, date_after="not-a-date", date_before="2024/13/99"
    )

    assert {row["doc_id"] for row in results} == {"31", "32"}


@pytest.mark.unit
def test_date_filter_drops_entries_without_created_at(lite_store, stub_embeddings):
    """任一日期边界存在时，缺 created_at 的条目被丢弃；无日期条件时正常返回"""
    stub_embeddings.register("undated text", _basis(0))
    lite_store.add_summary(33, "undated text", {"channel_id": "100"})

    unfiltered = lite_store.search_similar("undated text", top_k=10)
    assert [row["doc_id"] for row in unfiltered] == ["33"]

    filtered = lite_store.search_similar("undated text", top_k=10, date_after="2024-01-01T00:00:00")
    assert filtered == []


# ==================== search_all 合并语义 ====================


@pytest.mark.unit
def test_search_all_injects_source_and_merges_descending(lite_store, stub_embeddings):
    """search_all 注入 source 并按相似度全局降序合并"""
    _seed_search_all_rows(lite_store, stub_embeddings)

    results = lite_store.search_all("search all query", top_k=10)

    assert {row["doc_id"] for row in results} == {"41", "60:7"}
    assert [row["source"] for row in results] == ["summary", "message"]
    similarities = [row["similarity"] for row in results]
    assert similarities == sorted(similarities, reverse=True)


@pytest.mark.unit
def test_search_all_truncates_merged_results_to_top_k(lite_store, stub_embeddings):
    """search_all 对合并后的全集截取 top_k，保留相似度最高者"""
    _seed_search_all_rows(lite_store, stub_embeddings)

    results = lite_store.search_all("search all query", top_k=1)

    assert [row["source"] for row in results] == ["summary"]


@pytest.mark.unit
def test_search_all_returns_partial_results_when_one_side_fails(lite_store, stub_embeddings):
    """单边异常只记日志继续：另一侧结果照常返回"""
    _seed_search_all_rows(lite_store, stub_embeddings)

    class _BrokenCollection:
        """engine.search 必抛的 summaries 侧替身"""

        table = "summaries_vectors"

        class engine:
            @staticmethod
            def search(*_args, **_kwargs):
                raise RuntimeError("summaries side down")

    lite_store.collection = _BrokenCollection()

    results = lite_store.search_all("search all query", top_k=10)

    assert results
    assert {row["source"] for row in results} == {"message"}


# ==================== get_stats 形状 ====================


@pytest.mark.unit
def test_get_stats_shape_field_by_field(lite_store, stub_embeddings):
    """get_stats 逐字段形状：空库与写入后均与契约一致，顶层 total_vectors 跟随 summaries"""
    stats = lite_store.get_stats()
    assert stats == {
        "available": True,
        "summaries": {"available": True, "total_vectors": 0},
        "messages": {"available": True, "total_vectors": 0},
        "total_vectors": 0,
    }

    stub_embeddings.register("stats summary text", _basis(0))
    assert lite_store.add_summary(51, "stats summary text", {"channel_id": "100"}) is True
    lite_store.add_message(
        "80:1", "stats message text", {}, stub_embeddings.generate("stats message text")
    )

    stats = lite_store.get_stats()
    assert stats["available"] is True
    assert stats["summaries"] == {"available": True, "total_vectors": 1}
    assert stats["messages"] == {"available": True, "total_vectors": 1}
    assert stats["total_vectors"] == 1


# ==================== chroma 兼容适配器契约 ====================


@pytest.mark.unit
def test_adapter_count_reflects_rows(lite_store, stub_embeddings):
    """collection.count() 为权威行数，与磁盘一致"""
    adapter = lite_store.messages_collection
    assert adapter.count() == 0

    ids = ["1:1", "1:2", "1:3"]
    texts = ["count alpha", "count beta", "count gamma"]
    assert (
        lite_store.add_messages_batch(
            ids,
            texts,
            [{"channel_id": "1"} for _ in ids],
            [stub_embeddings.generate(t) for t in texts],
        )
        == 3
    )
    assert adapter.count() == 3


@pytest.mark.unit
def test_adapter_get_pagination_with_offset(lite_store, stub_embeddings):
    """get 分页：ORDER BY id 下 limit/offset 稳定，未请求键为 None"""
    ids = ["d1", "d2", "d3", "d4", "d5"]
    texts = [f"page doc {i}" for i in range(1, 6)]
    lite_store.add_messages_batch(
        ids, texts, [{} for _ in ids], [stub_embeddings.generate(t) for t in texts]
    )

    page = lite_store.messages_collection.get(limit=2, offset=1, include=["documents"])

    assert page["ids"] == ["d2", "d3"]
    assert page["documents"] == ["page doc 2", "page doc 3"]
    assert page["metadatas"] is None
    assert page["embeddings"] is None


@pytest.mark.unit
def test_adapter_get_by_ids_embeddings_and_empty_include(lite_store, stub_embeddings):
    """get(ids) 过滤与 embeddings 维度；include=[] 仅回 ids；未命中返回空 ids"""
    adapter = lite_store.messages_collection
    texts = ["dim doc one", "dim doc two"]
    lite_store.add_messages_batch(
        ["d1", "d2"], texts, [{"n": 1}, {"n": 2}], [stub_embeddings.generate(t) for t in texts]
    )

    got = adapter.get(ids=["d1"], include=["metadatas", "documents", "embeddings"])
    assert got["ids"] == ["d1"]
    assert got["documents"] == ["dim doc one"]
    assert got["metadatas"] == [{"n": 1}]
    assert len(got["embeddings"][0]) == _DIM

    assert adapter.get(ids=["missing"])["ids"] == []

    only_ids = adapter.get(include=[], limit=1000)
    assert sorted(only_ids["ids"]) == ["d1", "d2"]
    assert only_ids["documents"] is None
    assert only_ids["metadatas"] is None
    assert only_ids["embeddings"] is None


@pytest.mark.unit
def test_adapter_delete_accepts_1000_ids(lite_store, stub_embeddings):
    """delete 单次接受 1000 个 id（WebUI 清空循环批量），幂等清空"""
    adapter = lite_store.messages_collection
    ids = [f"9:{i}" for i in range(1000)]
    texts = [f"bulk doc {i}" for i in range(1000)]
    lite_store.add_messages_batch(
        ids, texts, [{} for _ in ids], [stub_embeddings.generate(t) for t in texts]
    )
    assert adapter.count() == 1000

    adapter.delete(ids)

    assert adapter.count() == 0
    assert adapter.get(include=[], limit=10)["ids"] == []


# ==================== 跨实例版本失效 ====================


@pytest.mark.unit
def test_second_instance_sees_writes_on_next_search(monkeypatch, tmp_path, stub_embeddings):
    """同一 SQLite 文件的两个实例：A 写入后，B 下一次 search 立即可见（版本号失效重载）"""
    monkeypatch.setenv("VECTOR_BACKEND", "lite")
    monkeypatch.setenv("VECTOR_DB_PATH", str(tmp_path / "vectors"))
    monkeypatch.setenv("EMBEDDING_DIMENSION", str(_DIM))
    monkeypatch.delenv("VECTOR_BACKEND_READER", raising=False)
    monkeypatch.setattr(vector_store_module, "vector_store", None)

    writer = VectorStore()
    reader = VectorStore()
    assert writer is not reader
    assert reader.search_messages("late write text") == []

    assert (
        writer.add_message(
            "70:1",
            "late write text",
            {"channel_id": "70"},
            stub_embeddings.generate("late write text"),
        )
        is True
    )

    hits = reader.search_messages("late write text", top_k=5)
    assert [row["doc_id"] for row in hits] == ["70:1"]


# ==================== 读者只读模式（VECTOR_BACKEND_READER=1） ====================


@pytest.mark.unit
def test_reader_mode_missing_db_degrades_and_creates_nothing(monkeypatch, tmp_path):
    """读者模式：库缺失时绝不建目录/建库（无 DDL），可用性降级为 False"""
    monkeypatch.setenv("VECTOR_BACKEND", "lite")
    monkeypatch.setenv("VECTOR_DB_PATH", str(tmp_path / "vectors"))
    monkeypatch.setenv("EMBEDDING_DIMENSION", str(_DIM))
    monkeypatch.setenv("VECTOR_BACKEND_READER", "1")
    monkeypatch.setattr(vector_store_module, "vector_store", None)

    store = VectorStore()

    assert store.backend == "lite"
    assert store.readonly is True
    assert store.is_available() is False
    assert store.is_messages_available() is False
    assert not (tmp_path / "vectors").exists()


@pytest.mark.unit
def test_reader_mode_reads_writer_data_and_rejects_writes(monkeypatch, tmp_path, stub_embeddings):
    """读者模式：读到 writer 已提交数据；写请求一律降级失败且不产生任何变更"""
    monkeypatch.setenv("VECTOR_BACKEND", "lite")
    monkeypatch.setenv("VECTOR_DB_PATH", str(tmp_path / "vectors"))
    monkeypatch.setenv("EMBEDDING_DIMENSION", str(_DIM))
    monkeypatch.delenv("VECTOR_BACKEND_READER", raising=False)
    monkeypatch.setattr(vector_store_module, "vector_store", None)

    writer = VectorStore()
    assert (
        writer.add_message(
            "70:1",
            "reader visible text",
            {"channel_id": "70"},
            stub_embeddings.generate("reader visible text"),
        )
        is True
    )

    monkeypatch.setenv("VECTOR_BACKEND_READER", "1")
    reader = VectorStore()

    assert reader.readonly is True
    assert reader.is_messages_available() is True
    hits = reader.search_messages("reader visible text", top_k=5)
    assert [row["doc_id"] for row in hits] == ["70:1"]

    assert (
        reader.add_message("70:2", "blocked text", {}, stub_embeddings.generate("blocked text"))
        is False
    )
    assert reader.delete_message("70:1") is False
    assert reader.messages_collection.count() == 1


# ==================== 构造失败降级 ====================


@pytest.mark.unit
def test_unwritable_db_path_degrades_without_raising(monkeypatch, tmp_path, stub_embeddings):
    """VECTOR_DB_PATH 不可写：构造不抛、is_available False、get_stats available False"""
    blocker = tmp_path / "blocker.txt"
    blocker.write_text("not a directory", encoding="utf-8")
    monkeypatch.setenv("VECTOR_BACKEND", "lite")
    monkeypatch.setenv("VECTOR_DB_PATH", str(blocker / "vectors"))
    monkeypatch.setenv("EMBEDDING_DIMENSION", str(_DIM))
    monkeypatch.setattr(vector_store_module, "vector_store", None)

    store = VectorStore()

    assert store.backend == "lite"
    assert store.is_available() is False
    assert store.is_messages_available() is False

    stats = store.get_stats()
    assert stats["available"] is False
    assert "error" in stats["summaries"]
    assert "error" in stats["messages"]
    assert stats["total_vectors"] == 0


if __name__ == "__main__":
    pytest.main([__file__, "-v"])
