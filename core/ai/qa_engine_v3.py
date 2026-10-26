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
问答引擎 v4 - 通用 AI 助手 + Agentic RAG + 真实多轮对话

设计原则（对应 Issue #112 Phase 2）：
1. 通用助手是默认路径：普通对话、知识问答、编程创作直接回答；
   频道历史/统计等数据能力以 Function Tools 形式供模型自主调用。
2. 用户消息原样传递，不做关键词/时间范围的扩写改写；
   显式命令（如 /ask）的频道约束通过系统级可信上下文注入。
3. 对话历史使用真实 user/assistant 角色序列，不再扁平化为提示词文本。
4. 原生流式 tool-call 循环：无工具调用时只生成一次，避免重复调用。
5. 每个请求使用独立的 ToolExecutor，多用户并发互不污染。
6. 失败降级为"无工具直连对话"，普通对话不会被误导为频道检索。
"""

import asyncio
import json
import logging
from datetime import UTC, datetime
from typing import Any

from core.ai.agent_tools import TOOL_SCHEMAS, ToolExecutor
from core.ai.ai_client import get_ai_runtime
from core.ai.memory_manager import get_memory_manager
from core.ai.reranker import get_reranker
from core.ai.vector_store import get_vector_store
from core.config import get_qa_bot_persona
from core.infrastructure.database import get_db_manager

from .conversation_manager import get_conversation_manager

logger = logging.getLogger(__name__)

# 系统提示词模板（使用占位符，人格描述会动态注入）
BASE_SYSTEM_TEMPLATE = """{persona_description}

---

## 核心任务
你是一个通用智能助手：可以陪用户聊天、回答知识问题、协助编程与写作。
同时你接入了一个 Telegram 频道管理系统的数据（频道历史总结、消息记录、统计信息），
当问题涉及这些频道内容时，按需调用检索工具查询后再回答。

## 核心约束（必须严格遵守）
1. **自主判断**：是否检索由你根据问题性质决定。闲聊、通用知识、编程与创作等
   直接回答，不要调用任何工具；只有涉及频道历史、消息或统计时才检索。
2. **基于事实**：引用频道数据时严格基于工具返回的内容回答，不要编造信息
3. **上下文理解**：优先利用多轮对话历史理解代词指代（如"它"、"那个"、"这个"等）
4. **按需引用**：工具结果包含 post_links 时，在相关要点后保留帖子链接作为证据；
   与频道内容无关的回答无需附加任何来源信息
5. **Markdown规范**：
   - 粗体：使用 **文本** （两边各两个星号）
   - 斜体：使用 *文本* （两边各一个星号）
   - 代码：使用 `代码` （反引号）
   - **禁止使用 # 标题格式**
   - 列表：使用 - 或 • 开头
   - 链接：使用 [文本](URL) 格式
   - **禁止使用未配对的星号、下划线或反引号**

## 当前上下文
{channel_context}{command_context}
当前日期时间：{current_time}
"""

# 追加到系统提示词的工具使用说明（仅在支持工具调用时注入）
AGENT_TOOL_INSTRUCTIONS = """

## 检索工具
你可以调用以下工具查询频道数据（是否使用完全由你自主判断）：
- **semantic_search**: 语义搜索，适合模糊/概念性查询
- **keyword_search**: 关键词搜索，适合精确匹配特定术语
- **rerank_results**: 对搜索结果重排序（结果>5条时考虑使用）
- **list_channels**: 获取所有可查询频道链接
- **resolve_channel**: 将频道名、@username 或链接解析为标准频道链接
- **get_recent_summaries**: 获取最近总结，适合回答最新动态
- **get_channel_stats**: 获取频道统计信息
- **get_source_detail**: 展开之前搜索结果的完整来源内容

### 使用策略
1. 普通聊天、通用知识、编程与创作类问题：直接回答，不要调用任何工具
2. 涉及频道历史/消息/统计时才检索；用户指定频道名、@username 或链接时，
   先调用 resolve_channel，再把 channel_id 传给搜索工具
3. 可以同时调用多个搜索工具（如语义+关键词并行检索）
4. 检索结果充足则回答，不足则调整参数再次检索
5. 引用规则：只有消息类结果（source_type=message）带有后端验证过的
   post_url 帖子链接，在相关要点后引用该链接作为证据；总结类结果
   （source_type=summary）没有帖子链接。严禁根据 channel_id、message_id
   或其他 ID 自行拼接 t.me 链接
6. 最多进行{max_iterations}轮工具调用，之后必须基于已收集的信息给出最终回答
"""

# Agentic RAG 最大工具调用迭代次数
AGENT_MAX_ITERATIONS = 10

# 提供给模型的历史消息条数上限
MAX_HISTORY_MESSAGES = 20

# 单条历史消息内容截断长度（控制 token 消耗）
HISTORY_MESSAGE_MAX_CHARS = 1500


class QAEngineV3:
    """问答引擎 v4 - 通用 AI 助手 + Agentic RAG + 真实多轮对话"""

    def __init__(self):
        """初始化问答引擎"""
        self.db = get_db_manager()
        self.memory_manager = get_memory_manager()
        self.vector_store = get_vector_store()
        self.reranker = get_reranker()
        self.conversation_mgr = get_conversation_manager()
        logger.info("问答引擎v4.0.0初始化完成（通用AI助手 + Agentic RAG + 真实多轮对话）")

    async def process_query(self, query: str, user_id: int) -> str:
        """
        处理用户查询（非流式便捷入口：收集流式输出为完整文本）

        Args:
            query: 用户查询
            user_id: 用户ID

        Returns:
            回答文本
        """
        parts: list[str] = []
        async for chunk in self.process_query_stream(query, user_id):
            if chunk in ("__DONE__", "__NEW_SESSION__"):
                continue
            if chunk.startswith("__ERROR__:"):
                return chunk[len("__ERROR__:") :]
            parts.append(chunk)
        return "".join(parts)

    async def process_query_stream(self, query: str, user_id: int, channel_hint: str | None = None):
        """
        流式处理用户查询（异步生成器版本）

        所有消息统一走通用助手路径：模型自主决定是否调用检索工具。
        系统状态等固定能力由显式命令（/status 等）在命令层处理，
        不再对普通消息做关键词意图拦截。

        Yields:
            str: 文本片段，以 "__DONE__" 结尾表示完成，
                 以 "__ERROR__:<msg>" 表示出错，
                 以 "__NEW_SESSION__" 表示开始了新会话。
        """
        try:
            logger.info(f"[stream] 处理查询: user_id={user_id}, query={query}")

            # 1. 获取或创建会话
            session_id, is_new_session = self.conversation_mgr.get_or_create_session(user_id)

            # 2. 先取历史（此时还不含当前消息），再保存当前用户消息，
            #    避免当前消息在历史中重复出现
            conversation_history = await self.conversation_mgr.get_conversation_history(
                user_id, session_id
            )
            await self.conversation_mgr.save_message(
                user_id=user_id, session_id=session_id, role="user", content=query
            )

            if is_new_session:
                yield "__NEW_SESSION__"

            # 3. Agentic 处理：模型自主决定是否检索
            full_answer = ""
            try:
                async for chunk in self._agentic_stream(
                    query=query,
                    conversation_history=conversation_history,
                    channel_hint=channel_hint,
                ):
                    full_answer += chunk
                    yield chunk

            except Exception as e:
                # 降级策略：不再强制进入固定检索流水线（那会把普通对话
                # 误导为频道查询）。改为一次无工具的直连对话；若仍失败
                # 则向调用方返回可观测的错误。
                logger.error(
                    f"[stream] Agentic 处理异常，降级为无工具直连回答: {type(e).__name__}: {e}",
                    exc_info=True,
                )
                if not full_answer:
                    async for chunk in self._plain_chat_stream(
                        query=query, conversation_history=conversation_history
                    ):
                        full_answer += chunk
                        yield chunk
                else:
                    # 已输出部分内容后中断：保留已生成内容并附加可观测提示
                    notice = "\n\n⚠️ 回答生成中断，请稍后重试。"
                    full_answer += notice
                    yield notice

            # 空回答兜底（可观测，避免占位消息永远停留在"思考中"）
            if not full_answer.strip():
                full_answer = "⚠️ 未能生成回答，请稍后重试。"
                yield full_answer

            # 4. 保存完整回答到对话历史
            if is_new_session:
                full_answer = "🍃 *开始新的对话。*\n\n" + full_answer
            await self.conversation_mgr.save_message(
                user_id=user_id, session_id=session_id, role="assistant", content=full_answer
            )

            yield "__DONE__"

        except Exception as e:
            logger.error(f"[stream] 处理查询失败: {type(e).__name__}: {e}", exc_info=True)
            yield "__ERROR__:❌ 处理查询时出错，请稍后重试。"

    # ------------------------------------------------------------------
    # 系统提示词构建
    # ------------------------------------------------------------------

    async def _build_system_prompt(
        self, channel_hint: str | None = None, include_tools: bool = True
    ) -> str:
        """构建系统提示词。

        Args:
            channel_hint: 显式命令（/ask）指定的频道约束，作为可信系统级
                上下文注入，绝不改写用户消息本身
            include_tools: 是否附加检索工具说明（降级直连时不附加）
        """
        channel_context = await self.memory_manager.get_channel_context()

        command_context = ""
        if channel_hint:
            command_context = (
                f"\n【命令上下文】\n用户通过 /ask 命令显式限定了查询频道：{channel_hint}。"
                "涉及频道内容时请先调用 resolve_channel 解析该频道，"
                "再以解析出的 channel_id 限定检索。\n"
            )

        persona_description = get_qa_bot_persona()
        current_time = datetime.now(UTC).strftime("%Y-%m-%d %H:%M") + " (UTC)"

        system_prompt = BASE_SYSTEM_TEMPLATE.format(
            persona_description=persona_description,
            channel_context=channel_context,
            command_context=command_context,
            current_time=current_time,
        )

        if include_tools:
            system_prompt += AGENT_TOOL_INSTRUCTIONS.format(max_iterations=AGENT_MAX_ITERATIONS)

        return system_prompt

    @staticmethod
    def _history_to_messages(conversation_history: list[dict]) -> list[dict[str, str]]:
        """把数据库中的对话历史转换为真实的 user/assistant 消息序列。

        - 只保留 user/assistant 角色，跳过异常数据
        - 单条内容超长时截断，控制 token 消耗
        - 只保留最近 MAX_HISTORY_MESSAGES 条
        """
        messages: list[dict[str, str]] = []
        for item in conversation_history or []:
            role = item.get("role")
            content = item.get("content") or ""
            if role not in ("user", "assistant") or not content.strip():
                continue
            if len(content) > HISTORY_MESSAGE_MAX_CHARS:
                content = content[:HISTORY_MESSAGE_MAX_CHARS] + "…"
            messages.append({"role": role, "content": content})

        return messages[-MAX_HISTORY_MESSAGES:]

    # ------------------------------------------------------------------
    # Agentic 流式循环
    # ------------------------------------------------------------------

    async def _agentic_stream(
        self,
        query: str,
        conversation_history: list[dict],
        channel_hint: str | None = None,
    ):
        """原生流式 Agentic 循环。

        每一轮都是一次流式请求，同时接收文本增量与 tool_call 增量：
        - 模型不调用工具 → 内容已按流式输出，直接结束（无二次生成）
        - 模型调用工具 → 执行工具、追加 tool 消息后进入下一轮
        每个请求使用独立的 ToolExecutor，多用户并发互不污染。
        """
        # 每请求隔离的工具执行上下文
        tool_executor = ToolExecutor(self.vector_store, self.memory_manager, self.reranker)
        client, model = get_ai_runtime()
        loop = asyncio.get_running_loop()

        system_prompt = await self._build_system_prompt(channel_hint=channel_hint)

        # 真实角色序列：system + 历史 + 当前用户消息（原样传递）
        messages: list[dict[str, Any]] = [{"role": "system", "content": system_prompt}]
        messages.extend(self._history_to_messages(conversation_history))
        messages.append({"role": "user", "content": query})

        for iteration in range(AGENT_MAX_ITERATIONS):
            logger.info(f"[agent] 迭代 {iteration + 1}/{AGENT_MAX_ITERATIONS}")

            stream = await loop.run_in_executor(
                None,
                lambda: client.chat.completions.create(
                    model=model,
                    messages=messages,
                    tools=TOOL_SCHEMAS,
                    tool_choice="auto",
                    temperature=0.7,
                    stream=True,
                ),
            )

            content_parts: list[str] = []
            # index -> {"id": str, "name": str, "arguments": str}
            tool_calls_acc: dict[int, dict[str, str]] = {}

            async for chunk in self._iter_stream_chunks(stream):
                delta = self._get_stream_delta(chunk)
                if delta is None:
                    continue

                delta_content = getattr(delta, "content", None)
                if delta_content:
                    content_parts.append(delta_content)
                    # 文本一到达即向外流式输出
                    yield delta_content

                for tc in getattr(delta, "tool_calls", None) or []:
                    self._accumulate_tool_call_delta(tool_calls_acc, tc)

            content = "".join(content_parts)

            # 无工具调用 → 最终回答已在上面流式输出完毕
            if not tool_calls_acc:
                logger.info(f"[agent] LLM 未调用工具（迭代 {iteration + 1}），回答完成")
                return

            # 有工具调用：记录 assistant 消息（保留已输出内容与 tool_calls）
            assistant_message: dict[str, Any] = {"role": "assistant", "content": content}
            assistant_message["tool_calls"] = [
                {
                    "id": slot["id"] or f"call_{index}",
                    "type": "function",
                    "function": {"name": slot["name"], "arguments": slot["arguments"]},
                }
                for index, slot in sorted(tool_calls_acc.items())
            ]
            messages.append(assistant_message)

            # 执行所有工具调用并追加 tool 消息
            for index, slot in sorted(tool_calls_acc.items()):
                tool_name = slot["name"]
                try:
                    tool_args = json.loads(slot["arguments"]) if slot["arguments"] else {}
                except json.JSONDecodeError:
                    logger.warning(f"[agent] 工具参数解析失败: {slot['arguments']}")
                    tool_args = {}

                logger.info(f"[agent] 调用工具: {tool_name}")
                result_str = await tool_executor.execute(tool_name, tool_args)
                messages.append(
                    {
                        "role": "tool",
                        "tool_call_id": slot["id"] or f"call_{index}",
                        "content": result_str,
                    }
                )
        else:
            # 达到最大迭代：附加提示后强制生成最终回答（不带工具）
            logger.warning(f"[agent] 达到最大迭代 {AGENT_MAX_ITERATIONS}，强制生成回答")
            messages.append({"role": "user", "content": "请基于已收集的信息直接给出最终回答。"})

            stream = await loop.run_in_executor(
                None,
                lambda: client.chat.completions.create(
                    model=model,
                    messages=messages,
                    temperature=0.7,
                    stream=True,
                ),
            )

            async for chunk in self._iter_stream_chunks(stream):
                delta = self._get_stream_delta(chunk)
                if delta is None:
                    continue
                delta_content = getattr(delta, "content", None)
                if delta_content:
                    yield delta_content

    async def _plain_chat_stream(self, query: str, conversation_history: list[dict]):
        """无工具降级：直连对话流式生成。

        模型 API 不支持工具调用或工具链路异常时使用；保证普通对话仍能
        正常回答，不会误导为"未找到频道总结"。
        """
        client, model = get_ai_runtime()
        system_prompt = await self._build_system_prompt(include_tools=False)
        messages: list[dict[str, Any]] = [{"role": "system", "content": system_prompt}]
        messages.extend(self._history_to_messages(conversation_history))
        messages.append({"role": "user", "content": query})

        loop = asyncio.get_running_loop()
        logger.info("[fallback] 使用无工具直连对话降级")

        stream = await loop.run_in_executor(
            None,
            lambda: client.chat.completions.create(
                model=model,
                messages=messages,
                temperature=0.7,
                stream=True,
            ),
        )

        async for chunk in self._iter_stream_chunks(stream):
            delta = self._get_stream_delta(chunk)
            if delta is None:
                continue
            delta_content = getattr(delta, "content", None)
            if delta_content:
                yield delta_content

    # ------------------------------------------------------------------
    # 流式工具函数
    # ------------------------------------------------------------------

    @staticmethod
    async def _iter_stream_chunks(stream):
        """在线程池中迭代同步流对象，避免阻塞事件循环。"""
        loop = asyncio.get_running_loop()
        sentinel = object()
        while True:
            chunk = await loop.run_in_executor(None, next, stream, sentinel)
            if chunk is sentinel:
                return
            yield chunk

    @staticmethod
    def _get_stream_delta(chunk) -> Any:
        """安全提取流式分片的 delta 对象。"""
        choices = getattr(chunk, "choices", None)
        if not choices:
            return None
        return getattr(choices[0], "delta", None)

    @staticmethod
    def _accumulate_tool_call_delta(tool_calls_acc: dict[int, dict[str, str]], tc: Any) -> None:
        """累积流式分片中的 tool_call 增量。

        OpenAI 兼容流式协议中，tool_call 的 id/name 在首个分片完整给出，
        arguments 以字符串分片累加。
        """
        index = getattr(tc, "index", None)
        index = 0 if index is None else index

        slot = tool_calls_acc.setdefault(index, {"id": "", "name": "", "arguments": ""})

        tc_id = getattr(tc, "id", None)
        if tc_id:
            slot["id"] = tc_id

        function = getattr(tc, "function", None)
        if function is not None:
            name = getattr(function, "name", None)
            if name:
                # name 按协议在首个分片完整到达，采用覆盖语义
                slot["name"] = name
            arguments = getattr(function, "arguments", None)
            if arguments:
                slot["arguments"] += arguments


# 创建全局问答引擎实例
qa_engine_v3 = None


def get_qa_engine_v3():
    """获取全局问答引擎实例"""
    global qa_engine_v3
    if qa_engine_v3 is None:
        qa_engine_v3 = QAEngineV3()
    return qa_engine_v3
