# Copyright 2026 Sakura-Bot
#
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
#
# - 署名：必须提供本项目的原始来源链接
# - 相同方式共享：衍生作品必须采用相同的许可证
#
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
# 许可证全文：参见 LICENSE 文件

"""测试 Agentic RAG 工具执行器"""

import json
from unittest.mock import AsyncMock, MagicMock

import pytest

from core.ai.agent_tools import TOOL_SCHEMAS, ToolExecutor


@pytest.fixture
def executor():
    """创建工具执行器"""
    vector_store = MagicMock()
    vector_store.is_available.return_value = True
    vector_store.is_messages_available.return_value = True
    memory_manager = MagicMock()
    reranker = MagicMock()
    reranker.is_available.return_value = False
    return ToolExecutor(vector_store, memory_manager, reranker)


def test_tool_schemas_include_channel_tools():
    """测试工具定义包含频道相关工具"""
    names = {tool["function"]["name"] for tool in TOOL_SCHEMAS}

    assert "list_channels" in names
    assert "resolve_channel" in names
    assert "get_recent_summaries" in names
    assert "get_channel_stats" in names
    assert "get_source_detail" in names


def test_serialize_result_builds_message_post_link_public_channel():
    """测试消息向量结果（公开频道）生成帖子链接"""
    result = {
        "summary_text": "消息内容",
        "doc_id": "12345:777",
        "source": "message",
        "metadata": {
            "channel_id": "12345",
            "channel_username": "test",
            "channel_name": "Test",
            "message_id": "777",
        },
    }

    serialized = ToolExecutor._serialize_result(result)

    # 公开 username 优先于数字 ID
    assert serialized["post_url"] == "https://t.me/test/777"
    assert serialized["post_links"] == ["https://t.me/test/777"]
    assert serialized["message_id"] == 777
    assert serialized["source_type"] == "message"
    # 消息类结果不携带总结ID
    assert serialized["summary_id"] is None


def test_serialize_result_numeric_channel_uses_private_format():
    """回归测试：数字频道 ID 必须生成 /c/ 私有格式链接。

    历史 Bug 把内部数字频道 ID 直接拼成公开频道链接
    （如 https://t.me/2039687763/9973），该地址实际不存在。
    """
    result = {
        "summary_id": None,
        "summary_text": "频道原始消息",
        "doc_id": "2039687763:9457",
        "source": "message",
        "metadata": {"channel_id": "2039687763", "message_id": "9457"},
    }

    serialized = ToolExecutor._serialize_result(result)

    assert serialized["post_url"] == "https://t.me/c/2039687763/9457"
    assert serialized["message_id"] == 9457
    assert serialized["summary_id"] is None


def test_serialize_result_message_id_from_doc_id_tail():
    """doc_id 末段（channel:message 格式）解析出真实消息 ID"""
    result = {
        "summary_text": "消息",
        "doc_id": "2039687763:9457",
        "source": "message",
        "metadata": {"channel_id": "2039687763"},
    }

    serialized = ToolExecutor._serialize_result(result)

    assert serialized["message_id"] == 9457
    assert serialized["post_url"] == "https://t.me/c/2039687763/9457"


def test_serialize_result_summary_has_no_fabricated_links():
    """回归测试：总结类结果不再根据 summary_message_ids 伪造帖子链接。

    summary_message_ids 是机器人发布的总结报告消息 ID（甚至可能来自
    管理员私聊），不是频道原帖 ID，绝不能拼成频道链接。
    """
    result = {
        "summary_id": 1,
        "summary_text": "总结内容",
        "source": "summary",
        "metadata": {
            "channel_id": "https://t.me/test",
            "channel_name": "Test",
            "summary_message_ids": "[10, 11]",
        },
    }

    serialized = ToolExecutor._serialize_result(result)

    assert serialized["source_type"] == "summary"
    assert serialized["post_url"] is None
    assert serialized["post_links"] == []
    assert serialized["message_id"] is None
    # 真实数据库总结 ID 保留
    assert serialized["summary_id"] == 1


def test_serialize_result_excludes_internal_hash_from_summary_id():
    """回归测试：消息结果不会被塞入内部哈希伪造成 summary_id。"""
    result = {
        "summary_id": None,
        "summary_text": "消息",
        "doc_id": "2039687763:9457",
        "source": "message",
        "metadata": {"channel_id": "2039687763"},
    }

    serialized = ToolExecutor._serialize_result(result)

    assert serialized["summary_id"] is None
    assert serialized["message_id"] == 9457


def test_build_post_link_delegates_to_shared_builder():
    """测试 _build_post_link 委托统一链接构造并拒绝无效来源"""
    assert ToolExecutor._build_post_link(None, 1) is None
    assert ToolExecutor._build_post_link("https://t.me/+private", 1) is None
    assert ToolExecutor._build_post_link("https://t.me/test", "abc") is None
    assert ToolExecutor._build_post_link("@test_channel", 123) == "https://t.me/test_channel/123"
    # 数字频道 → /c/ 私有格式
    assert ToolExecutor._build_post_link("2039687763", 9973) == ("https://t.me/c/2039687763/9973")
    # Bot API -100 前缀
    assert ToolExecutor._build_post_link("-1002039687763", 9973) == (
        "https://t.me/c/2039687763/9973"
    )


def test_get_all_results_includes_doc_results_without_duplicates(executor):
    """测试获取全部结果时包含仅 doc_id 索引的消息结果并去重"""
    summary_result = {"summary_id": 1, "summary_text": "总结"}
    message_result = {"summary_text": "消息", "doc_id": "https://t.me/test:2"}
    duplicated_result = {
        "summary_id": 3,
        "summary_text": "同时有两个索引",
        "doc_id": "https://t.me/test:3",
    }

    executor._store_result(summary_result)
    executor._store_result(message_result)
    executor._store_result(duplicated_result)
    executor._doc_result_store[duplicated_result["doc_id"]] = dict(duplicated_result)

    results = executor.get_all_results()

    assert summary_result in results
    assert message_result in results
    assert results.count(duplicated_result) == 1


@pytest.mark.asyncio
async def test_execute_list_channels_returns_channels(executor):
    """测试列出频道工具"""
    executor.memory_manager.list_channels = AsyncMock(
        return_value=[
            {"channel_id": "https://t.me/test", "channel_name": "Test", "summary_count": 1}
        ]
    )

    result = json.loads(await executor.execute("list_channels", {"limit": 10}))

    assert result["count"] == 1
    assert result["channels"][0]["channel_id"] == "https://t.me/test"


@pytest.mark.asyncio
async def test_execute_resolve_channel_returns_match(executor):
    """测试解析频道工具"""
    executor.memory_manager.resolve_channel = AsyncMock(
        return_value={"success": True, "channel_id": "https://t.me/test"}
    )

    result = json.loads(await executor.execute("resolve_channel", {"channel_hint": "@test"}))

    assert result["success"] is True
    assert result["channel_id"] == "https://t.me/test"


@pytest.mark.asyncio
async def test_execute_semantic_search_uses_channel_filter(executor):
    """测试语义检索传递频道过滤"""
    executor.vector_store.search_all.return_value = [
        {
            "summary_id": 1,
            "summary_text": "完整内容",
            "metadata": {"channel_id": "https://t.me/test", "channel_name": "Test"},
            "doc_id": "https://t.me/test:1",
            "source": "message",
        }
    ]

    result = json.loads(
        await executor.execute(
            "semantic_search",
            {"query": "AI", "channel_id": "https://t.me/test", "collection": "all"},
        )
    )

    assert result["count"] == 1
    assert result["results"][0]["post_links"] == ["https://t.me/test/1"]
    executor.vector_store.search_all.assert_called_once()
    call_kwargs = executor.vector_store.search_all.call_args.kwargs
    assert call_kwargs["filter_metadata"] == {"channel_id": "https://t.me/test"}


@pytest.mark.asyncio
async def test_execute_source_detail_returns_full_text(executor):
    """测试来源详情返回完整文本"""
    executor.vector_store.search_all.return_value = [
        {
            "summary_id": 1,
            "summary_text": "很长的完整内容" * 100,
            "metadata": {"channel_id": "https://t.me/test", "channel_name": "Test"},
            "doc_id": "https://t.me/test:1",
        }
    ]
    await executor.execute("semantic_search", {"query": "AI"})

    result = json.loads(await executor.execute("get_source_detail", {"summary_id": 1}))

    assert "detail" in result
    assert len(result["detail"]["summary_text"]) > 500


@pytest.mark.asyncio
async def test_keyword_search_does_not_fabricate_post_links(executor):
    """回归测试：keyword_search 的总结结果不携带伪造的帖子链接"""
    executor.memory_manager.search_summaries = AsyncMock(
        return_value=[
            {
                "id": 5,
                "summary_text": "总结",
                "channel_id": "https://t.me/test",
                "channel_name": "Test",
                "created_at": "2026-10-17T00:00:00",
                "summary_message_ids": [10, 11],  # 报告消息 ID，不应变成链接
            }
        ]
    )

    result = json.loads(await executor.execute("keyword_search", {"keywords": ["AI"]}))

    serialized = result["results"][0]
    assert serialized["source_type"] == "summary"
    assert serialized["summary_id"] == 5
    assert serialized["post_url"] is None
    assert serialized["post_links"] == []
    assert serialized["result_id"] == 1


@pytest.mark.asyncio
async def test_rerank_uses_result_id_reference(executor):
    """rerank 通过请求内 result_id 引用结果，而非 summary_id/doc_id"""
    executor.reranker.is_available.return_value = True
    executor.vector_store.search_all.return_value = [
        {
            "summary_id": None,
            "message_id": 9457,
            "summary_text": "消息",
            "doc_id": "2039687763:9457",
            "source": "message",
            "metadata": {"channel_id": "2039687763"},
        }
    ]
    await executor.execute("semantic_search", {"query": "AI"})

    executor.reranker.rerank.return_value = [
        {
            "summary_id": None,
            "message_id": 9457,
            "summary_text": "消息",
            "doc_id": "2039687763:9457",
            "source": "message",
            "metadata": {"channel_id": "2039687763"},
            "rerank_score": 0.9,
        }
    ]

    result = json.loads(
        await executor.execute("rerank_results", {"query": "AI", "result_ids": [1]})
    )

    assert result["count"] == 1
    assert result["results"][0]["rerank_score"] == 0.9


@pytest.mark.asyncio
async def test_source_detail_supports_result_id(executor):
    """get_source_detail 优先支持 result_id 引用"""
    executor.vector_store.search_all.return_value = [
        {
            "summary_id": None,
            "message_id": 9457,
            "summary_text": "完整内容" * 100,
            "doc_id": "2039687763:9457",
            "source": "message",
            "metadata": {"channel_id": "2039687763"},
        }
    ]
    await executor.execute("semantic_search", {"query": "AI"})

    result = json.loads(await executor.execute("get_source_detail", {"result_id": 1}))

    assert "detail" in result
    assert result["detail"]["post_url"] == "https://t.me/c/2039687763/9457"
