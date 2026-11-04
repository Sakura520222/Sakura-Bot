"""问答引擎 v4 通用助手流式循环测试（Issue #112 Phase 2）

Copyright 2026 Sakura-Bot
本项目采用 AGPL-3.0 许可
"""

import json
from types import SimpleNamespace
from unittest.mock import AsyncMock, MagicMock

import pytest

import core.ai.qa_engine_v3 as qae
from core.ai.qa_engine_v3 import QAEngineV3

# ---------------------------------------------------------------------------
# 测试工具
# ---------------------------------------------------------------------------


def content_chunk(text: str) -> SimpleNamespace:
    """构造包含文本增量的流式分片"""
    delta = SimpleNamespace(content=text, tool_calls=None)
    return SimpleNamespace(choices=[SimpleNamespace(delta=delta)])


def tool_call_chunk(index: int, call_id: str, name: str, arguments: str) -> SimpleNamespace:
    """构造包含 tool_call 增量的流式分片"""
    tc = SimpleNamespace(
        index=index, id=call_id, function=SimpleNamespace(name=name, arguments=arguments)
    )
    delta = SimpleNamespace(content=None, tool_calls=[tc])
    return SimpleNamespace(choices=[SimpleNamespace(delta=delta)])


def empty_chunk() -> SimpleNamespace:
    """构造无有效内容的流式分片"""
    delta = SimpleNamespace(content=None, tool_calls=None)
    return SimpleNamespace(choices=[SimpleNamespace(delta=delta)])


@pytest.fixture
def engine(monkeypatch):
    """构造依赖全部 mock 的引擎实例"""
    memory = MagicMock()
    memory.get_channel_context = AsyncMock(return_value="【频道】测试频道")
    conv = MagicMock()
    conv.get_or_create_session = MagicMock(return_value=("session-1", False))
    conv.get_conversation_history = AsyncMock(return_value=[])
    conv.save_message = AsyncMock(return_value=True)

    monkeypatch.setattr(qae, "get_db_manager", lambda: MagicMock())
    monkeypatch.setattr(qae, "get_memory_manager", lambda: memory)
    monkeypatch.setattr(qae, "get_vector_store", lambda: MagicMock())
    monkeypatch.setattr(qae, "get_reranker", lambda: MagicMock())
    monkeypatch.setattr(qae, "get_conversation_manager", lambda: conv)
    monkeypatch.setattr(qae, "get_qa_bot_persona", lambda: "测试人格")

    return QAEngineV3()


def install_llm(monkeypatch, create):
    """替换引擎使用的 LLM 客户端"""
    llm = MagicMock()
    llm.chat.completions.create = create
    monkeypatch.setattr(qae, "get_ai_runtime", lambda purpose="assistant": (llm, "test-model"))
    return llm


def install_tool_executor(monkeypatch, result='{"ok": true}'):
    """替换 ToolExecutor，返回执行器实例 mock 便于断言"""
    executor = MagicMock()
    executor.execute = AsyncMock(return_value=result)
    executor_cls = MagicMock(return_value=executor)
    monkeypatch.setattr(qae, "ToolExecutor", executor_cls)
    return executor_cls, executor


async def collect(agen):
    """收集异步生成器全部输出"""
    return [chunk async for chunk in agen]


# ---------------------------------------------------------------------------
# 无工具调用：单次流式生成
# ---------------------------------------------------------------------------


@pytest.mark.unit
class TestAgenticNoToolSinglePass:
    """LLM 不调用工具时只生成一次（不发生无意义的第二次调用）"""

    @pytest.mark.asyncio
    async def test_plain_reply_single_llm_call(self, engine, monkeypatch):
        create = MagicMock(return_value=iter([content_chunk("你好"), content_chunk("！")]))
        install_llm(monkeypatch, create)

        chunks = await collect(engine._agentic_stream("你好", []))

        assert "".join(chunks) == "你好！"
        # 关键验收：无工具调用时只有一次模型请求
        assert create.call_count == 1
        # 请求带上了工具定义且 tool_choice 为 auto
        _, kwargs = create.call_args
        assert kwargs.get("tools") is qae.TOOL_SCHEMAS
        assert kwargs.get("tool_choice") == "auto"
        assert kwargs.get("stream") is True

    @pytest.mark.asyncio
    async def test_user_message_passed_verbatim(self, engine, monkeypatch):
        """用户消息原样传递，不被关键词/时间范围扩写"""
        create = MagicMock(return_value=iter([content_chunk("好的")]))
        install_llm(monkeypatch, create)

        await collect(engine._agentic_stream("我还能学习 Python 吗？", []))

        _, kwargs = create.call_args
        user_messages = [m for m in kwargs["messages"] if m["role"] == "user"]
        assert user_messages == [{"role": "user", "content": "我还能学习 Python 吗？"}]


# ---------------------------------------------------------------------------
# 工具调用循环
# ---------------------------------------------------------------------------


@pytest.mark.unit
class TestAgenticToolLoop:
    """原生流式 tool-call 循环"""

    @pytest.mark.asyncio
    async def test_tool_call_then_final_answer(self, engine, monkeypatch):
        executor_cls, executor = install_tool_executor(monkeypatch)
        stream1 = iter(
            [
                tool_call_chunk(0, "call_1", "list_channels", '{"lim'),
                tool_call_chunk(0, "call_1", "list_channels", 'it": 5}'),
            ]
        )
        stream2 = iter([content_chunk("当前共有 2 个频道。")])
        create = MagicMock(side_effect=[stream1, stream2])
        install_llm(monkeypatch, create)

        chunks = await collect(engine._agentic_stream("有哪些频道？", []))

        assert "".join(chunks) == "当前共有 2 个频道。"
        assert create.call_count == 2

        # 工具参数由分片正确拼接
        executor.execute.assert_awaited_once_with("list_channels", {"limit": 5})

        # 第二轮请求包含 assistant(tool_calls) 与 tool 消息
        _, kwargs = create.call_args
        roles = [m["role"] for m in kwargs["messages"]]
        assert "tool" in roles
        assistant_with_tools = [
            m for m in kwargs["messages"] if m.get("role") == "assistant" and "tool_calls" in m
        ]
        assert assistant_with_tools, "应保留带 tool_calls 的 assistant 消息"
        assert assistant_with_tools[0]["tool_calls"][0]["function"]["name"] == "list_channels"

    @pytest.mark.asyncio
    async def test_tool_executor_isolated_per_request(self, engine, monkeypatch):
        """每个请求创建独立的 ToolExecutor（多用户并发隔离）"""
        executor_cls, _ = install_tool_executor(monkeypatch)
        create = MagicMock(return_value=iter([content_chunk("好")]))
        install_llm(monkeypatch, create)

        await collect(engine._agentic_stream("问题一", []))
        await collect(engine._agentic_stream("问题二", []))

        assert executor_cls.call_count == 2

    @pytest.mark.asyncio
    async def test_history_as_real_role_messages(self, engine, monkeypatch):
        """对话历史以真实 user/assistant 角色序列进入请求"""
        create = MagicMock(return_value=iter([content_chunk("好的")]))
        install_llm(monkeypatch, create)
        history = [
            {"role": "user", "content": "第一个问题"},
            {"role": "assistant", "content": "第一个回答"},
        ]

        await collect(engine._agentic_stream("刚才第二个问题呢？", history))

        _, kwargs = create.call_args
        messages = kwargs["messages"]
        assert messages[0]["role"] == "system"
        assert {"role": "user", "content": "第一个问题"} in messages
        assert {"role": "assistant", "content": "第一个回答"} in messages
        # 当前用户消息只在末尾出现一次
        assert messages[-1] == {"role": "user", "content": "刚才第二个问题呢？"}
        assert sum(1 for m in messages if m["content"] == "刚才第二个问题呢？") == 1


# ---------------------------------------------------------------------------
# 降级路径
# ---------------------------------------------------------------------------


@pytest.mark.unit
class TestFallbackPlainChat:
    """Agentic 失败时降级为无工具直连对话（不再强制固定 RAG）"""

    @pytest.mark.asyncio
    async def test_tools_unsupported_falls_back_to_plain_chat(self, engine, monkeypatch):
        def create(**kwargs):
            if kwargs.get("tools") is not None:
                raise TypeError("tools is not supported")
            return iter([content_chunk("这是直连回答。")])

        create_mock = MagicMock(side_effect=create)
        install_llm(monkeypatch, create_mock)

        chunks = await collect(engine.process_query_stream("你好", 1))

        assert "这是直连回答。" in "".join(chunks)
        assert "__DONE__" in chunks
        assert not any(str(c).startswith("__ERROR__") for c in chunks)
        # 降级请求不携带 tools 参数
        _, fallback_kwargs = create_mock.call_args
        assert fallback_kwargs.get("tools") is None

    @pytest.mark.asyncio
    async def test_history_fetched_before_saving_current_message(self, engine, monkeypatch):
        """先取历史再保存当前消息，避免当前消息在历史中重复出现"""
        create = MagicMock(return_value=iter([content_chunk("回答")]))
        install_llm(monkeypatch, create)

        chunks = await collect(engine.process_query_stream("你好", 1))
        assert "__DONE__" in chunks

        engine.conversation_mgr.get_conversation_history.assert_awaited_once()
        # save_message 第一次调用必须是保存 user 消息
        first_save = engine.conversation_mgr.save_message.await_args_list[0]
        assert first_save.kwargs["role"] == "user"
        # 历史读取发生在用户消息保存之前
        conv = engine.conversation_mgr
        assert conv.get_conversation_history.await_count == 1


# ---------------------------------------------------------------------------
# 辅助函数
# ---------------------------------------------------------------------------


@pytest.mark.unit
class TestStreamHelpers:
    """流式工具函数测试"""

    @pytest.mark.asyncio
    async def test_history_to_messages_preserves_content_and_filters(self):
        long_content = "x" * 3000
        history = [
            {"role": "tool", "content": "应被过滤"},
            {"role": "user", "content": ""},
            {"role": "assistant", "content": long_content},
            {"role": "user", "content": "问题"},
        ]
        messages = QAEngineV3._history_to_messages(history)

        assert len(messages) == 2
        assert messages[0]["role"] == "assistant"
        assert messages[0]["content"] == long_content
        assert messages[1] == {"role": "user", "content": "问题"}

    def test_accumulate_tool_call_delta_assembles_fragments(self):
        acc = {}
        qae.QAEngineV3._accumulate_tool_call_delta(
            acc,
            SimpleNamespace(
                index=0, id="call_1", function=SimpleNamespace(name="search", arguments='{"q')
            ),
        )
        qae.QAEngineV3._accumulate_tool_call_delta(
            acc,
            SimpleNamespace(
                index=0, id=None, function=SimpleNamespace(name=None, arguments='": "x"}')
            ),
        )
        assert acc[0]["id"] == "call_1"
        assert acc[0]["name"] == "search"
        assert json.loads(acc[0]["arguments"]) == {"q": "x"}

    def test_get_stream_delta_handles_empty_choices(self):
        assert QAEngineV3._get_stream_delta(SimpleNamespace(choices=[])) is None
        assert QAEngineV3._get_stream_delta(SimpleNamespace(choices=None)) is None


@pytest.mark.parametrize("mode", ["tools", "fallback", "forced"])
async def test_multimodal_requests_preserve_images_runtime_and_text_only_history(
    engine, monkeypatch, caplog, mode
):
    from copy import deepcopy

    from core.ai.qa_images import ImageContext, ImageRef, ImageRequest
    from core.config.ai_runtime import AIConnectionSettings

    store = ImageContext()
    store.add((1, 10), ImageRef("telegram-one", 1))
    store.add((1, 10), ImageRef("telegram-two", 2))
    images = ["data:image/png;base64,PRIVATE_ONE", "data:image/jpeg;base64,PRIVATE_TWO"]
    downloader = AsyncMock(side_effect=images)
    request = ImageRequest(store, (1, 10), object(), download=downloader)
    connection = AIConnectionSettings(
        "provider-secret", "https://assistant.example/v1", "assistant-image", supports_vision=True
    )
    sent = []

    def create(**kwargs):
        sent.append(deepcopy(kwargs))
        if len(sent) == 1:
            if mode == "fallback":
                raise ValueError("PRIVATE_ONE provider-secret")
            return iter([tool_call_chunk(0, "call", "list_channels", "{}")])
        return iter([content_chunk("红色的物体")])

    client = MagicMock()
    client.chat.completions.create = create
    runtime_calls = []

    def runtime(purpose="assistant", *, config=None):
        runtime_calls.append((purpose, config))
        return client, config.model

    monkeypatch.setattr(qae, "get_ai_runtime", runtime)
    install_tool_executor(monkeypatch)
    if mode == "forced":
        monkeypatch.setattr(qae, "AGENT_MAX_ITERATIONS", 1)
    chunks = await collect(
        engine.process_query_stream("这是什么", 1, image_request=request, ai_config=connection)
    )
    assert "__DONE__" in chunks and "红色的物体" in chunks
    assert len(sent) == 2 and runtime_calls == [("assistant", connection)]
    for call in sent:
        current = [m for m in call["messages"] if isinstance(m["content"], list)]
        assert len(current) == 1 and current[0]["role"] == "user"
        assert current[0]["content"][0] == {"type": "text", "text": "这是什么"}
        assert [p["image_url"]["url"] for p in current[0]["content"][1:]] == images
        assert call["model"] == "assistant-image"
    for call in engine.conversation_mgr.save_message.await_args_list:
        assert isinstance(call.kwargs["content"], str)
        assert "PRIVATE" not in call.kwargs["content"]
    assert "PRIVATE" not in caplog.text and "provider-secret" not in caplog.text
    followup = store.begin((1, 10), "session-1", "它是什么颜色？")
    assert len(followup.refs) == 2
    store.finish(followup, True)


async def test_images_disabled_or_download_failure_never_sends_text_only(engine, monkeypatch):
    from core.ai.qa_images import ImageContext, ImageInputError, ImageRef, ImageRequest
    from core.config.ai_runtime import AIConnectionSettings

    store = ImageContext()
    store.add((1, 1), ImageRef("file", 1))
    download = AsyncMock(side_effect=ImageInputError("下载失败"))
    req = ImageRequest(store, (1, 1), object(), download=download)
    runtime = MagicMock(return_value=(MagicMock(), "image"))
    monkeypatch.setattr(qae, "get_ai_runtime", runtime)
    config = AIConnectionSettings("key", "https://a.example/v1", "image", supports_vision=False)
    chunks = await collect(
        engine.process_query_stream("这是什么", 1, image_request=req, ai_config=config)
    )
    assert any("未开启图片输入" in c for c in chunks)
    runtime.assert_not_called()
    download.assert_not_called()
    config = AIConnectionSettings("key", "https://a.example/v1", "image", supports_vision=True)
    chunks = await collect(
        engine.process_query_stream("再试一次", 1, image_request=req, ai_config=config)
    )
    assert chunks == ["__ERROR__:下载失败"]
    engine.conversation_mgr.save_message.assert_not_awaited()


if __name__ == "__main__":
    pytest.main([__file__, "-v"])
