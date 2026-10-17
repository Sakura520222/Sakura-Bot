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
Agentic RAG 工具定义与执行器
将检索能力封装为 LLM 可调用的 Function Calling 工具
"""

import asyncio
import json
import logging
import re
from typing import Any

from core.infrastructure.utils.telegram_links import build_telegram_post_link

logger = logging.getLogger(__name__)

# ---------------------------------------------------------------------------
# OpenAI 兼容的工具 Schema 定义
# ---------------------------------------------------------------------------

TOOL_SCHEMAS = [
    {
        "type": "function",
        "function": {
            "name": "semantic_search",
            "description": (
                "搜索与查询语义相关的频道历史总结和消息。"
                "使用向量相似度检索，适合模糊匹配和概念性查询。"
                "当用户询问某个主题、概念或需要广泛召回时使用此工具。"
                "默认同时搜索总结和原始消息两个数据源。"
                "结果中的 post_url 是后端验证过的帖子链接，引用时只使用 post_url；"
                "总结类结果没有 post_url，不要根据任何 ID 自行拼接链接。"
            ),
            "parameters": {
                "type": "object",
                "properties": {
                    "query": {
                        "type": "string",
                        "description": "搜索查询文本，应使用改写后的完整独立查询。",
                    },
                    "top_k": {
                        "type": "integer",
                        "description": "返回结果数量，默认20",
                        "default": 20,
                    },
                    "date_after": {
                        "type": "string",
                        "description": "时间下限，ISO格式，如 '2026-04-19T00:00:00'",
                    },
                    "date_before": {
                        "type": "string",
                        "description": "时间上限，ISO格式",
                    },
                    "channel_id": {
                        "type": "string",
                        "description": "限定搜索的频道ID",
                    },
                    "collection": {
                        "type": "string",
                        "description": "搜索的数据源: 'all'(默认，同时搜summaries+messages)、'summaries'(仅总结)、'messages'(仅原始消息)",
                        "enum": ["all", "summaries", "messages"],
                        "default": "all",
                    },
                },
                "required": ["query"],
            },
        },
    },
    {
        "type": "function",
        "function": {
            "name": "keyword_search",
            "description": (
                "基于关键词搜索频道历史总结。"
                "使用SQL匹配，适合精确关键词查找和补充语义检索的结果。"
                "当语义搜索结果不足、需要查找特定术语，或需要按频道/时间获取最近总结时使用。"
                "keywords 可为空；为空时将按 channel_id/time_range_days/limit 做频道或时间过滤。"
                "返回的是总结记录（source_type=summary），没有帖子链接 post_url。"
            ),
            "parameters": {
                "type": "object",
                "properties": {
                    "keywords": {
                        "type": "array",
                        "items": {"type": "string"},
                        "description": "关键词列表",
                    },
                    "topics": {
                        "type": "array",
                        "items": {"type": "string"},
                        "description": "主题列表，如 ['技术', 'AI']",
                    },
                    "time_range_days": {
                        "type": "integer",
                        "description": "搜索最近N天的总结，默认90",
                        "default": 90,
                    },
                    "channel_id": {
                        "type": "string",
                        "description": "限定搜索的频道ID",
                    },
                    "limit": {
                        "type": "integer",
                        "description": "返回结果数量上限，默认10",
                        "default": 10,
                    },
                },
                "required": [],
            },
        },
    },
    {
        "type": "function",
        "function": {
            "name": "list_channels",
            "description": "获取所有可查询频道的名称、链接、总结数和最后更新时间。用户询问频道链接、有哪些频道、可查询范围时使用。",
            "parameters": {
                "type": "object",
                "properties": {
                    "limit": {
                        "type": "integer",
                        "description": "返回频道数量上限，默认50",
                        "default": 50,
                    },
                },
                "required": [],
            },
        },
    },
    {
        "type": "function",
        "function": {
            "name": "resolve_channel",
            "description": "根据频道名、@username、t.me链接或模糊名称解析为标准频道链接。限定频道查询前应先调用。",
            "parameters": {
                "type": "object",
                "properties": {
                    "channel_hint": {
                        "type": "string",
                        "description": "用户提到的频道名称、@username 或 Telegram 链接",
                    },
                },
                "required": ["channel_hint"],
            },
        },
    },
    {
        "type": "function",
        "function": {
            "name": "get_recent_summaries",
            "description": "获取指定频道或所有频道的最近总结，适合回答最近发生了什么、最新动态等问题。",
            "parameters": {
                "type": "object",
                "properties": {
                    "channel_id": {
                        "type": "string",
                        "description": "可选，限定频道链接。若用户给的是频道名，应先调用 resolve_channel。",
                    },
                    "limit": {
                        "type": "integer",
                        "description": "返回总结数量，默认5",
                        "default": 5,
                    },
                    "time_range_days": {
                        "type": "integer",
                        "description": "可选，最近N天",
                    },
                },
                "required": [],
            },
        },
    },
    {
        "type": "function",
        "function": {
            "name": "get_channel_stats",
            "description": "获取指定频道的总结数量、消息数量和更新时间等统计信息。",
            "parameters": {
                "type": "object",
                "properties": {
                    "channel_id": {
                        "type": "string",
                        "description": "标准频道链接。若用户给的是频道名，应先调用 resolve_channel。",
                    },
                },
                "required": ["channel_id"],
            },
        },
    },
    {
        "type": "function",
        "function": {
            "name": "get_source_detail",
            "description": (
                "按 result_id（推荐）或 doc_id 获取之前搜索结果中的完整来源内容。"
                "用户要求展开来源、查看详情时使用。"
            ),
            "parameters": {
                "type": "object",
                "properties": {
                    "result_id": {
                        "type": "integer",
                        "description": "搜索结果中的 result_id 字段，请求内唯一且稳定",
                    },
                    "summary_id": {
                        "type": "integer",
                        "description": "总结类结果中的数据库总结ID（仅 source_type=summary 时有效）",
                    },
                    "doc_id": {
                        "type": "string",
                        "description": "搜索结果中的原始 doc_id，适用于消息向量来源",
                    },
                },
                "required": [],
            },
        },
    },
    {
        "type": "function",
        "function": {
            "name": "rerank_results",
            "description": (
                "对搜索结果进行精排重排序。"
                "当搜索结果较多(>5条)且需要提高准确率时使用。"
                "基于query与文档的相关性重新排序并返回Top-K结果。"
            ),
            "parameters": {
                "type": "object",
                "properties": {
                    "query": {
                        "type": "string",
                        "description": "用于重排序的查询文本",
                    },
                    "result_ids": {
                        "type": "array",
                        "items": {"type": "integer"},
                        "description": "需要重排序的结果ID列表（取之前搜索结果中的 result_id 字段）",
                    },
                    "top_k": {
                        "type": "integer",
                        "description": "重排序后保留的结果数量，默认5",
                        "default": 5,
                    },
                },
                "required": ["query", "result_ids"],
            },
        },
    },
    {
        "type": "function",
        "function": {
            "name": "get_channel_info",
            "description": (
                "获取频道列表和频道画像信息。当用户提及特定频道或需要了解可用频道时使用。"
            ),
            "parameters": {
                "type": "object",
                "properties": {
                    "channel_id": {
                        "type": "string",
                        "description": "频道ID，不传则返回所有频道列表",
                    },
                },
                "required": [],
            },
        },
    },
]

# 在 tool message 中截断 summary_text 的最大字符数
_SUMMARY_TRUNCATE_LEN = 500


class ToolExecutor:
    """工具执行器：将 LLM 的 tool_call 请求路由到实际的检索组件。"""

    def __init__(self, vector_store, memory_manager, reranker):
        self.vector_store = vector_store
        self.memory_manager = memory_manager
        self.reranker = reranker
        # 结果存储：内部引用一律使用请求内自增的 result_id，
        # 不依赖 summary_id / doc_id 的巧合唯一性
        self._result_store: dict[int, dict[str, Any]] = {}
        # 真实数据库总结ID（source_type=summary 时才有）→ 结果
        self._result_by_summary_id: dict[int, dict[str, Any]] = {}
        self._doc_result_store: dict[str, dict[str, Any]] = {}
        self._next_result_id = 0

    def reset(self):
        """重置结果存储（每次 agent loop 开始时调用）"""
        self._result_store.clear()
        self._result_by_summary_id.clear()
        self._doc_result_store.clear()
        self._next_result_id = 0

    async def execute(self, tool_name: str, arguments: dict[str, Any]) -> str:
        """执行工具调用，返回 JSON 字符串格式的结果。

        所有异常都被捕获并作为错误结果返回，不会向上抛出。
        """
        try:
            if tool_name == "semantic_search":
                return await self._execute_semantic_search(arguments)
            elif tool_name == "keyword_search":
                return await self._execute_keyword_search(arguments)
            elif tool_name == "rerank_results":
                return await self._execute_rerank(arguments)
            elif tool_name == "list_channels":
                return await self._execute_list_channels(arguments)
            elif tool_name == "resolve_channel":
                return await self._execute_resolve_channel(arguments)
            elif tool_name == "get_recent_summaries":
                return await self._execute_recent_summaries(arguments)
            elif tool_name == "get_channel_stats":
                return await self._execute_channel_stats(arguments)
            elif tool_name == "get_source_detail":
                return await self._execute_source_detail(arguments)
            elif tool_name == "get_channel_info":
                return await self._execute_channel_info(arguments)
            else:
                return json.dumps({"error": f"未知工具: {tool_name}"}, ensure_ascii=False)
        except Exception as e:
            logger.error(f"工具执行失败 [{tool_name}]: {type(e).__name__}: {e}", exc_info=True)
            return json.dumps(
                {"error": f"工具执行失败: {type(e).__name__}: {e}"}, ensure_ascii=False
            )

    # ---- 各工具实现 ----

    async def _execute_semantic_search(self, args: dict) -> str:
        if not self.vector_store.is_available():
            return json.dumps(
                {"error": "向量存储不可用", "results": [], "count": 0}, ensure_ascii=False
            )

        filter_metadata = None
        if args.get("channel_id"):
            filter_metadata = {"channel_id": args["channel_id"]}

        # 根据 collection 参数选择数据源
        collection = args.get("collection", "all")

        if collection == "messages" and self.vector_store.is_messages_available():
            results = self.vector_store.search_messages(
                query=args["query"],
                top_k=args.get("top_k", 20),
                filter_metadata=filter_metadata,
                date_after=args.get("date_after"),
                date_before=args.get("date_before"),
            )
        elif collection == "summaries":
            results = self.vector_store.search_similar(
                query=args["query"],
                top_k=args.get("top_k", 20),
                filter_metadata=filter_metadata,
                date_after=args.get("date_after"),
                date_before=args.get("date_before"),
            )
        else:
            # 默认: 同时搜索 summaries + messages
            if self.vector_store.is_messages_available():
                results = self.vector_store.search_all(
                    query=args["query"],
                    top_k=args.get("top_k", 20),
                    filter_metadata=filter_metadata,
                    date_after=args.get("date_after"),
                    date_before=args.get("date_before"),
                )
            else:
                results = self.vector_store.search_similar(
                    query=args["query"],
                    top_k=args.get("top_k", 20),
                    filter_metadata=filter_metadata,
                    date_after=args.get("date_after"),
                    date_before=args.get("date_before"),
                )

        # 累积到 result_store 并序列化（截断长文本）
        serialized = []
        for r in results:
            self._store_result(r)
            serialized.append(self._serialize_result(r))

        return json.dumps(
            {"results": serialized, "count": len(serialized)},
            ensure_ascii=False,
        )

    async def _execute_keyword_search(self, args: dict) -> str:
        results = await self.memory_manager.search_summaries(
            keywords=args.get("keywords", []),
            topics=args.get("topics"),
            time_range_days=args.get("time_range_days", 90),
            channel_id=args.get("channel_id"),
            limit=args.get("limit", 10),
        )

        # keyword_search 返回原始 DB 行，需要归一化为统一格式。
        # 注意：DB 行的 summary_message_ids 是机器人发布的总结报告消息 ID，
        # 不是频道原帖 ID，不能用于构造帖子链接（历史 Bug）。
        serialized = []
        for r in results:
            sid = r.get("id")
            if sid is None:
                continue
            normalized = {
                "summary_id": sid,
                "summary_text": r.get("summary_text", ""),
                "source": "summary",
                "metadata": {
                    "channel_id": r.get("channel_id"),
                    "channel_name": r.get("channel_name"),
                    "created_at": r.get("created_at"),
                },
            }
            self._store_result(normalized)
            serialized.append(self._serialize_result(normalized))

        return json.dumps(
            {"results": serialized, "count": len(serialized)},
            ensure_ascii=False,
        )

    async def _execute_rerank(self, args: dict) -> str:
        if not self.reranker.is_available():
            return json.dumps(
                {"error": "重排序服务不可用", "results": [], "count": 0}, ensure_ascii=False
            )

        result_ids = args.get("result_ids", [])
        candidates = [self._result_store[rid] for rid in result_ids if rid in self._result_store]

        if not candidates:
            return json.dumps(
                {"error": "未找到有效的结果ID", "results": [], "count": 0}, ensure_ascii=False
            )

        loop = asyncio.get_running_loop()
        reranked = await loop.run_in_executor(
            None,
            lambda: self.reranker.rerank(
                query=args["query"],
                candidates=candidates,
                top_k=args.get("top_k", 5),
            ),
        )

        # 更新 result_store 中的结果（含 rerank_score）
        serialized = []
        for r in reranked:
            self._store_result(r)
            serialized.append(self._serialize_result(r))

        return json.dumps(
            {"results": serialized, "count": len(serialized)},
            ensure_ascii=False,
        )

    async def _execute_list_channels(self, args: dict) -> str:
        channels = await self.memory_manager.list_channels()
        limit = max(1, min(int(args.get("limit", 50)), 100))
        return json.dumps(
            {
                "channels": channels[:limit],
                "count": min(len(channels), limit),
                "total": len(channels),
            },
            ensure_ascii=False,
        )

    async def _execute_resolve_channel(self, args: dict) -> str:
        result = await self.memory_manager.resolve_channel(args.get("channel_hint", ""))
        return json.dumps(result, ensure_ascii=False)

    async def _execute_recent_summaries(self, args: dict) -> str:
        results = await self.memory_manager.get_recent_summaries(
            channel_id=args.get("channel_id"),
            limit=args.get("limit", 5),
            time_range_days=args.get("time_range_days"),
        )
        # 与 keyword_search 相同：总结记录不构造帖子链接
        serialized = []
        for r in results:
            sid = r.get("id") or r.get("summary_id")
            if sid is None:
                continue
            normalized = {
                "summary_id": sid,
                "summary_text": r.get("summary_text", ""),
                "source": "summary",
                "metadata": {
                    "channel_id": r.get("channel_id"),
                    "channel_name": r.get("channel_name"),
                    "created_at": r.get("created_at"),
                },
            }
            self._store_result(normalized)
            serialized.append(self._serialize_result(normalized))

        return json.dumps({"results": serialized, "count": len(serialized)}, ensure_ascii=False)

    async def _execute_channel_stats(self, args: dict) -> str:
        stats = await self.memory_manager.get_channel_stats(args.get("channel_id", ""))
        return json.dumps({"stats": stats}, ensure_ascii=False)

    async def _execute_source_detail(self, args: dict) -> str:
        result = None
        result_id = args.get("result_id")
        summary_id = args.get("summary_id")
        doc_id = args.get("doc_id")

        # 优先内部 result_id；兼容旧的 summary_id / doc_id 引用方式
        if result_id is not None:
            try:
                result = self._result_store.get(int(result_id))
            except (TypeError, ValueError):
                result = None

        if result is None and summary_id is not None:
            result = self._result_by_summary_id.get(summary_id)

        if result is None and doc_id:
            result = self._doc_result_store.get(str(doc_id))

        if result is None:
            return json.dumps(
                {"error": "未找到来源详情，请先执行搜索或提供有效的 summary_id/doc_id"},
                ensure_ascii=False,
            )

        detail = self._serialize_result(result)
        detail["summary_text"] = result.get("summary_text", "")
        return json.dumps({"detail": detail}, ensure_ascii=False)

    async def _execute_channel_info(self, args: dict) -> str:
        channel_id = args.get("channel_id")
        if not channel_id:
            channels = await self.memory_manager.list_channels()
            return json.dumps({"channels": channels, "count": len(channels)}, ensure_ascii=False)

        context = await self.memory_manager.get_channel_context(channel_id)

        if channel_id:
            return json.dumps({"channel_id": channel_id, "context": context}, ensure_ascii=False)

        # 无 channel_id 时返回通用信息
        return json.dumps({"context": context}, ensure_ascii=False)

    # ---- 辅助方法 ----

    @staticmethod
    def _resolve_source_type(result: dict[str, Any]) -> str:
        """判定结果来源类型："message"（频道原始消息）或 "summary"（总结记录）。"""
        source = result.get("source")
        if source in ("message", "summary"):
            return source

        # 无显式标记时推断：无真实总结ID且 doc_id 为 "channel:msg" 格式 → 消息
        doc_id = result.get("doc_id")
        if result.get("summary_id") is None and isinstance(doc_id, str) and ":" in doc_id:
            return "message"
        return "summary"

    @staticmethod
    def _resolve_message_id(result: dict[str, Any]) -> int | None:
        """提取结果对应的真实 Telegram 消息 ID（仅消息类结果有效）。

        优先级：显式 message_id 字段 / metadata.message_id → doc_id 末段
        （"channel_id:message_id" 格式）。内部哈希、数据库主键不会被返回。
        """
        raw_mid = result.get("message_id")
        if raw_mid is None:
            raw_mid = (result.get("metadata") or {}).get("message_id")

        if raw_mid is not None:
            try:
                mid = int(raw_mid)
                return mid if mid > 0 else None
            except (TypeError, ValueError):
                pass

        match = re.search(r":(\d+)$", str(result.get("doc_id") or ""))
        if match:
            mid = int(match.group(1))
            return mid if mid > 0 else None

        return None

    @classmethod
    def _resolve_post_url(cls, result: dict[str, Any]) -> str | None:
        """为消息类结果构造经过验证的帖子链接。

        - 仅 source_type=message 的结果才可能有帖子链接；总结报告消息 ID
          不是频道原帖，绝不能拼接为链接
        - 频道标识优先公开 username（更友好），其次数字 ID（/c/ 私有格式）
        - 无法验证时返回 None，由模型不引用任何链接
        """
        if cls._resolve_source_type(result) != "message":
            return None

        message_id = cls._resolve_message_id(result)
        if message_id is None:
            return None

        metadata = result.get("metadata") or {}
        candidates = (
            metadata.get("channel_username"),
            metadata.get("channel_id"),
            result.get("channel_id"),
        )
        for channel in candidates:
            if not channel:
                continue
            link = build_telegram_post_link(str(channel), message_id)
            if link:
                return link

        # 兜底：从 doc_id 前缀提取频道标识（"channel_id:message_id" 格式）
        doc_id = str(result.get("doc_id") or "")
        if ":" in doc_id:
            doc_channel = doc_id.rsplit(":", 1)[0]
            return build_telegram_post_link(doc_channel, message_id)

        return None

    @staticmethod
    def _serialize_result(result: dict) -> dict:
        """序列化单条结果用于 tool message（截断长文本）。

        字段语义（重要，勿混淆三种 ID）：
        - result_id: 请求内自增的内部引用 ID，供 rerank/get_source_detail 使用
        - summary_id: 仅总结类结果携带的 MySQL 总结记录 ID；消息类结果为 None
        - message_id: 仅消息类结果携带的真实 Telegram 频道消息 ID
        - post_url:   后端验证过的帖子链接；总结类结果为 None
        """
        text = result.get("summary_text", "")
        truncated = (
            text[:_SUMMARY_TRUNCATE_LEN] + "..." if len(text) > _SUMMARY_TRUNCATE_LEN else text
        )

        metadata = result.get("metadata", {})
        created_at = metadata.get("created_at")
        if hasattr(created_at, "isoformat"):
            created_at = created_at.isoformat()

        source_type = ToolExecutor._resolve_source_type(result)
        post_url = ToolExecutor._resolve_post_url(result)

        serialized = {
            "result_id": result.get("result_id"),
            "source_type": source_type,
            "summary_id": result.get("summary_id") if source_type == "summary" else None,
            "summary_text": truncated,
            "channel_id": metadata.get("channel_id") or result.get("channel_id"),
            "channel_name": metadata.get("channel_name") or result.get("channel_name"),
            "created_at": created_at,
            "message_id": ToolExecutor._resolve_message_id(result)
            if source_type == "message"
            else None,
            "doc_id": result.get("doc_id"),
            "post_url": post_url,
            # 兼容字段：仅包含已验证链接
            "post_links": [post_url] if post_url else [],
        }
        if "similarity" in result:
            serialized["similarity"] = round(result["similarity"], 3)
        if "rerank_score" in result:
            serialized["rerank_score"] = round(result["rerank_score"], 3)
        return serialized

    def _store_result(self, result: dict[str, Any]) -> None:
        """缓存搜索结果，供重排序和来源详情工具复用。"""
        self._next_result_id += 1
        result["result_id"] = self._next_result_id
        self._result_store[self._next_result_id] = result

        sid = result.get("summary_id")
        if sid is not None:
            self._result_by_summary_id[sid] = result

        doc_id = result.get("doc_id")
        if doc_id:
            self._doc_result_store[str(doc_id)] = result

    @staticmethod
    def _build_post_link(channel_id: str | None, message_id: Any) -> str | None:
        """构造 Telegram 帖子链接。

        统一委托 core.infrastructure.utils.telegram_links.build_telegram_post_link：
        数字频道 ID 使用 /c/ 私有格式，username 使用公开格式，无法验证返回 None。
        """
        return build_telegram_post_link(channel_id, message_id)

    def get_all_results(self) -> list[dict[str, Any]]:
        """获取所有累积的搜索结果（完整文本，不截断）。"""
        all_results = []
        seen_keys = set()
        for result in [*self._result_store.values(), *self._doc_result_store.values()]:
            key = (result.get("summary_id"), result.get("doc_id"))
            if key not in seen_keys:
                all_results.append(result)
                seen_keys.add(key)
        return all_results

    def get_results_by_ids(self, ids: list[int]) -> list[dict[str, Any]]:
        """根据 ID 列表获取结果，按 ID 出现顺序排列。"""
        return [self._result_store[i] for i in ids if i in self._result_store]
