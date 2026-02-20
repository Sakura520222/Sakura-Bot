# -*- coding: utf-8 -*-
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

"""
问答引擎 v3.1.0 - 集成向量搜索和重排序
实现语义检索 + RAG架构 + 多轮对话查询改写
"""

import logging
import re
from datetime import datetime, timedelta, timezone
from typing import Any, Dict, List, Optional

from .ai_client import client_llm
from .config import get_qa_bot_persona
from .conversation_manager import get_conversation_manager
from .database import get_db_manager
from .intent_parser import get_intent_parser
from .memory_manager import get_memory_manager
from .reranker import get_reranker
from .settings import get_llm_model
from .vector_store import get_vector_store

logger = logging.getLogger(__name__)

# 判断查询是否含有代词/指代词，需要进行查询改写
PRONOUN_PATTERNS = re.compile(
    r'它|他|她|这个|那个|这些|那些|此|彼|前者|后者|上面|上述|刚才|之前|继续|还有|那么|那|这'
)

# 系统提示词模板（使用占位符，人格描述会动态注入）
BASE_SYSTEM_TEMPLATE = """{persona_description}

---

## 核心任务
你是一个智能问答助手，负责根据频道历史总结回答用户问题。

## 核心约束（必须严格遵守）
1. **基于事实**：严格基于提供的历史总结内容回答，不要编造信息
2. **上下文理解**：如果有对话历史，优先利用上下文理解代词指代（如"它"、"那个"、"这个"等）
3. **明确回答**：总结中没有相关信息时，明确说明
4. **结构清晰**：使用清晰的结构和要点，语言简洁专业
5. **Markdown规范**：
   - 粗体：使用 **文本** （两边各两个星号）
   - 斜体：使用 *文本* （两边各一个星号）
   - 代码：使用 `代码` （反引号）
   - **禁止使用 # 标题格式**
   - 列表：使用 - 或 • 开头
   - 链接：使用 [文本](URL) 格式
   - **禁止使用未配对的星号、下划线或反引号**

## 当前上下文
{channel_context}{conversation_context}
"""

# 查询改写专用系统提示
REWRITE_SYSTEM_PROMPT = """你是一个查询改写助手。
给定多轮对话历史和用户的最新问题，将用户的最新问题改写为一个**独立、完整、不依赖上下文的搜索查询**。
要求：
- 解析所有代词（"它"、"那个"、"这个"等），替换为明确的实体名称
- 保留原始意图和关键信息
- 仅输出改写后的查询文本，不要任何解释或前缀
- 如果最新问题已经足够独立清晰，直接原样返回"""


class QAEngineV3:
    """问答引擎 v3.1.0 - 向量搜索 + 多轮对话支持 + 查询改写"""

    def __init__(self):
        """初始化问答引擎"""
        self.db = get_db_manager()
        self.intent_parser = get_intent_parser()
        self.memory_manager = get_memory_manager()
        self.vector_store = get_vector_store()
        self.reranker = get_reranker()
        self.conversation_mgr = get_conversation_manager()
        logger.info("问答引擎v3.1.0初始化完成（支持多轮对话 + 查询改写）")

    async def process_query(self, query: str, user_id: int) -> str:
        """
        处理用户查询（支持多轮对话）

        Args:
            query: 用户查询
            user_id: 用户ID

        Returns:
            回答文本
        """
        try:
            logger.info(f"处理查询: user_id={user_id}, query={query}")

            # 1. 获取或创建会话
            session_id, is_new_session = self.conversation_mgr.get_or_create_session(user_id)

            # 2. 保存用户消息
            self.conversation_mgr.save_message(
                user_id=user_id,
                session_id=session_id,
                role='user',
                content=query
            )

            # 3. 解析查询意图
            parsed = self.intent_parser.parse_query(query)
            logger.info(f"查询意图: {parsed['intent']}, 置信度: {parsed['confidence']}")

            # 4. 根据意图处理
            intent = parsed["intent"]

            if intent == "status":
                answer = await self._handle_status_query()
            elif intent == "stats":
                answer = await self._handle_stats_query(parsed)
            else:
                answer = await self._handle_content_query_v3(parsed, user_id, session_id, is_new_session)

            # 5. 保存助手回复
            self.conversation_mgr.save_message(
                user_id=user_id,
                session_id=session_id,
                role='assistant',
                content=answer
            )

            return answer

        except Exception as e:
            logger.error(f"处理查询失败: {type(e).__name__}: {e}", exc_info=True)
            return "❌ 处理查询时出错，请稍后重试。"

    async def _handle_status_query(self) -> str:
        """处理状态查询"""
        from .quota_manager import get_quota_manager
        quota_mgr = get_quota_manager()
        status = quota_mgr.get_system_status()

        vector_stats = self.vector_store.get_stats() if self.vector_store.is_available() else {}

        vector_info = ""
        if vector_stats.get("available"):
            total_vectors = vector_stats.get("total_vectors", 0)
            vector_info = f"\n• 向量总结数: {total_vectors} 条"

        return f"""📊 系统状态

• 每日总限额: {status['daily_limit']} 次
• 今日剩余: {status['remaining']} 次{vector_info}

💡 每日00:00自动重置"""

    async def _handle_stats_query(self, parsed: Dict[str, Any]) -> str:
        """处理统计查询"""
        stats = self.db.get_statistics()

        return f"""📈 数据统计

• 总总结数: {stats['total_count']} 条
• 总消息数: {stats['total_messages']:,} 条
• 平均消息数: {stats['avg_messages']} 条/总结
• 本周总结: {stats['week_count']} 条
• 本月总结: {stats['month_count']} 条

📊 类型分布:""" + "\n".join(
            f"  • {t}: {c} 条" for t, c in stats.get('type_stats', {}).items()
        )

    async def _handle_content_query_v3(self, parsed: Dict[str, Any],
                                       user_id: int, session_id: str,
                                       is_new_session: bool = False) -> str:
        """
        处理内容查询（v3.1.0）

        实现混合检索策略：
        1. 查询改写（多轮对话中含代词时）
        2. 语义检索（Dense，含时间过滤）
        3. 关键词检索（Sparse）作为补充
        4. RRF融合
        5. Reranker精排
        6. RAG生成（含对话历史）
        """
        try:
            query = parsed["original_query"]
            keywords = parsed.get("keywords", [])
            time_range = parsed.get("time_range")  # 可能为 None

            # 获取对话历史
            conversation_history = self.conversation_mgr.get_conversation_history(user_id, session_id)
            logger.debug(f"用户 {user_id} 的对话历史: {len(conversation_history)} 条")

            # ── 步骤0: 查询改写（多轮对话 + 含代词时） ──────────────────────────
            search_query = query  # 用于检索的查询（可能被改写）
            query_rewritten = False

            if (not is_new_session
                    and len(conversation_history) >= 3  # 至少有1轮历史
                    and PRONOUN_PATTERNS.search(query)):
                try:
                    search_query = await self._rewrite_query(query, conversation_history)
                    if search_query != query:
                        query_rewritten = True
                        logger.info(f"查询改写: '{query}' → '{search_query}'")
                except Exception as e:
                    logger.warning(f"查询改写失败，使用原始查询: {e}")
                    search_query = query

            # ── 步骤1: 计算时间过滤范围 ─────────────────────────────────────────
            date_after: Optional[str] = None
            if time_range is not None:
                cutoff = datetime.now(timezone.utc) - timedelta(days=time_range)
                date_after = cutoff.isoformat()
                logger.info(f"时间过滤: date_after={date_after[:10]}")

            # ── 步骤2: 语义检索（召回Top-20，含时间过滤） ────────────────────────
            semantic_results = []
            if self.vector_store.is_available():
                try:
                    semantic_results = self.vector_store.search_similar(
                        query=search_query,
                        top_k=20,
                        date_after=date_after
                    )
                    logger.info(f"语义检索: 找到 {len(semantic_results)} 条结果")
                except Exception as e:
                    logger.error(f"语义检索失败: {e}")

            # ── 步骤3: 关键词检索（补充方案） ────────────────────────────────────
            keyword_results = []
            # 当语义检索结果不足或有明确关键词时，启用关键词检索
            if keywords or len(semantic_results) < 5:
                try:
                    search_days = time_range if time_range is not None else 90
                    keyword_results = self.memory_manager.search_summaries(
                        keywords=keywords,
                        time_range_days=search_days,
                        limit=10
                    )
                    logger.info(f"关键词检索: 找到 {len(keyword_results)} 条结果")
                except Exception as e:
                    logger.error(f"关键词检索失败: {e}")

            # ── 步骤4: 融合结果 ───────────────────────────────────────────────────
            if semantic_results and keyword_results:
                final_candidates = self._rrf_fusion(semantic_results, keyword_results)
                logger.info(f"RRF融合: {len(final_candidates)} 条结果")
            elif semantic_results:
                final_candidates = semantic_results
            elif keyword_results:
                final_candidates = [
                    {
                        'summary_id': r['id'],
                        'summary_text': r['summary_text'],
                        'metadata': {
                            'channel_id': r.get('channel_id'),
                            'channel_name': r.get('channel_name'),
                            'created_at': r.get('created_at')
                        }
                    }
                    for r in keyword_results
                ]
            else:
                if time_range is not None and time_range <= 7:
                    return (f"🔍 在最近 {time_range} 天内未找到相关总结。\n\n"
                            f"💡 提示：可以尝试扩大时间范围，例如'最近30天关于...'。")
                return "🔍 未找到相关总结。\n\n💡 提示：尝试调整关键词或时间范围。"

            # ── 步骤5: 重排序（Top-20 → Top-5） ─────────────────────────────────
            if self.reranker.is_available() and len(final_candidates) > 5:
                try:
                    final_candidates = self.reranker.rerank(search_query, final_candidates, top_k=5)
                    logger.info(f"重排序完成: 保留 {len(final_candidates)} 条结果")
                except Exception as e:
                    logger.error(f"重排序失败: {e}")
                    final_candidates = final_candidates[:5]
            else:
                final_candidates = final_candidates[:5]

            # ── 步骤6: AI生成回答（RAG + 对话历史） ──────────────────────────────
            answer = await self._generate_answer_with_rag(
                query=query,                        # 原始查询（用于AI理解用户意图）
                search_query=search_query,          # 改写后查询（用于说明检索依据）
                summaries=final_candidates,
                keywords=keywords,
                conversation_history=conversation_history,
                query_rewritten=query_rewritten
            )

            # 新会话时加上引导语
            if is_new_session:
                answer = "🍃 *开始新的对话。*\n\n" + answer

            return answer

        except Exception as e:
            logger.error(f"处理内容查询失败: {type(e).__name__}: {e}", exc_info=True)
            return "❌ 查询失败，请稍后重试。"

    async def _rewrite_query(self, query: str,
                             conversation_history: List[Dict]) -> str:
        """
        利用LLM将含代词的查询改写为独立完整的检索查询

        Args:
            query: 原始用户查询（可能含代词）
            conversation_history: 当前会话历史

        Returns:
            改写后的查询字符串
        """
        # 只取最近4条历史（避免token浪费）
        recent_history = conversation_history[-4:] if len(conversation_history) > 4 else conversation_history
        # 排除最后一条（就是当前的用户查询）
        context_history = recent_history[:-1] if len(recent_history) > 1 else []

        if not context_history:
            return query

        # 构建对话历史文本
        history_text = self.conversation_mgr.format_conversation_context(context_history)

        response = client_llm.chat.completions.create(
            model=get_llm_model(),
            messages=[
                {"role": "system", "content": REWRITE_SYSTEM_PROMPT},
                {"role": "user", "content": f"对话历史：\n{history_text}\n\n用户最新问题：{query}\n\n改写后的独立查询："}
            ],
            temperature=0.1,
            max_tokens=200
        )

        rewritten = response.choices[0].message.content.strip()
        # 安全检查：若改写结果过长或为空，回退到原始查询
        if not rewritten or len(rewritten) > 200:
            return query
        return rewritten

    def _rrf_fusion(self, semantic_results: List[Dict],
                   keyword_results: List[Dict], k: int = 60) -> List[Dict]:
        """
        Reciprocal Rank Fusion (RRF) 融合算法

        Args:
            semantic_results: 语义检索结果
            keyword_results: 关键词检索结果
            k: RRF常数，默认60

        Returns:
            融合后的结果列表
        """
        result_map = {}

        # 处理语义检索结果
        for rank, result in enumerate(semantic_results, 1):
            summary_id = result['summary_id']
            score = 1.0 / (k + rank)
            result_map[summary_id] = {
                'summary': result,
                'score': score,
                'source': 'semantic'
            }

        # 处理关键词检索结果
        for rank, result in enumerate(keyword_results, 1):
            summary_id = result['id']
            score = 1.0 / (k + rank)

            if summary_id in result_map:
                result_map[summary_id]['score'] += score
                result_map[summary_id]['source'] = 'hybrid'
            else:
                result_map[summary_id] = {
                    'summary': {
                        'summary_id': result['id'],
                        'summary_text': result['summary_text'],
                        'metadata': {
                            'channel_id': result.get('channel_id'),
                            'channel_name': result.get('channel_name'),
                            'created_at': result.get('created_at')
                        }
                    },
                    'score': score,
                    'source': 'keyword'
                }

        sorted_results = sorted(
            result_map.values(),
            key=lambda x: x['score'],
            reverse=True
        )

        return [item['summary'] for item in sorted_results]

    def _build_rag_prompts(self, query: str, summaries: List[Dict[str, Any]],
                           keywords: List[str] = None,
                           conversation_history: List[Dict] = None,
                           search_query: str = None,
                           query_rewritten: bool = False) -> tuple:
        """
        构建 RAG 所需的 system_prompt 和 user_prompt（供流式与非流式共用）

        Returns:
            (system_prompt, user_prompt)
        """
        context = self._prepare_rag_context(summaries)

        channel_ids = list(set(
            s.get('metadata', {}).get('channel_id') or s.get('channel_id', '')
            for s in summaries
        ))
        channel_context = ""
        if len(channel_ids) == 1 and channel_ids[0]:
            channel_context = self.memory_manager.get_channel_context(channel_ids[0])
        elif len(channel_ids) > 1:
            channel_context = "多频道综合查询"

        conversation_context = ""
        if conversation_history and len(conversation_history) > 0:
            history_excluding_current = (
                conversation_history[:-1] if len(conversation_history) > 1 else []
            )
            if history_excluding_current:
                conversation_context = self.conversation_mgr.format_conversation_context(
                    history_excluding_current
                )
                conversation_context = f"\n【对话历史】\n{conversation_context}\n"

        persona_description = get_qa_bot_persona()
        system_prompt = BASE_SYSTEM_TEMPLATE.format(
            persona_description=persona_description,
            channel_context=channel_context,
            conversation_context=conversation_context
        )

        rewrite_note = ""
        if query_rewritten and search_query and search_query != query:
            rewrite_note = f"\n（已根据对话上下文将查询理解为：「{search_query}」）"

        user_prompt = (
            f"用户当前查询：{query}{rewrite_note}\n\n"
            f"相关历史总结（共{len(summaries)}条，已通过语义搜索和重排序精选）：\n"
            f"{context}\n\n"
            f"请根据上述总结回答用户的问题。"
        )

        return system_prompt, user_prompt

    async def _generate_answer_with_rag(self, query: str,
                                        summaries: List[Dict[str, Any]],
                                        keywords: List[str] = None,
                                        conversation_history: List[Dict] = None,
                                        search_query: str = None,
                                        query_rewritten: bool = False) -> str:
        """
        使用RAG生成回答（支持多轮对话）

        Args:
            query: 用户原始查询
            summaries: 相关总结列表
            keywords: 关键词
            conversation_history: 对话历史列表
            search_query: 改写后的检索查询（可选）
            query_rewritten: 是否经过查询改写

        Returns:
            生成的回答
        """
        try:
            system_prompt, user_prompt = self._build_rag_prompts(
                query=query,
                summaries=summaries,
                keywords=keywords,
                conversation_history=conversation_history,
                search_query=search_query,
                query_rewritten=query_rewritten
            )

            logger.info(
                f"调用AI生成回答（RAG+对话历史），总结数: {len(summaries)}, "
                f"历史消息: {len(conversation_history) if conversation_history else 0}, "
                f"查询改写: {query_rewritten}"
            )

            response = client_llm.chat.completions.create(
                model=get_llm_model(),
                messages=[
                    {"role": "system", "content": system_prompt},
                    {"role": "user", "content": user_prompt}
                ],
                temperature=0.7
            )

            answer = response.choices[0].message.content.strip()
            logger.info(f"AI回答生成成功，长度: {len(answer)}字符")

            # 添加来源信息（若AI已自行生成来源块则不重复追加）
            if "📚 数据来源" not in answer:
                source_info = self._format_source_info_v3(summaries)
                return f"{answer}\n\n{source_info}"
            return answer

        except Exception as e:
            error_type = type(e).__name__
            error_msg = str(e)

            logger.error(f"AI生成回答失败: {error_type}: {error_msg}", exc_info=True)

            if 'Moderation Block' in error_msg or 'content_filter' in error_msg:
                return """⚠️ **查询内容受限**

抱歉，你的查询触发了内容过滤机制，无法提供相关信息。

💡 **建议：**
• 询问频道里最近的总结
• 查询特定主题的历史记录
• 使用不同的关键词重新表述问题

请重新组织你的问题再试。"""

            return self._fallback_answer_v3(summaries)

    async def generate_answer_stream(self, query: str,
                                     summaries: List[Dict[str, Any]],
                                     keywords: List[str] = None,
                                     conversation_history: List[Dict] = None,
                                     search_query: str = None,
                                     query_rewritten: bool = False):
        """
        使用RAG流式生成回答（异步生成器）

        Args:
            query: 用户原始查询
            summaries: 相关总结列表
            keywords: 关键词列表
            conversation_history: 对话历史
            search_query: 改写后的检索查询
            query_rewritten: 是否经过查询改写

        Yields:
            str: 逐步生成的文本片段
        """
        import asyncio

        system_prompt, user_prompt = self._build_rag_prompts(
            query=query,
            summaries=summaries,
            keywords=keywords,
            conversation_history=conversation_history,
            search_query=search_query,
            query_rewritten=query_rewritten
        )

        logger.info(
            f"调用AI流式生成回答（RAG），总结数: {len(summaries)}, "
            f"历史消息: {len(conversation_history) if conversation_history else 0}, "
            f"查询改写: {query_rewritten}"
        )

        loop = asyncio.get_event_loop()

        def _do_stream():
            return client_llm.chat.completions.create(
                model=get_llm_model(),
                messages=[
                    {"role": "system", "content": system_prompt},
                    {"role": "user", "content": user_prompt}
                ],
                temperature=0.7,
                stream=True
            )

        # 在线程池中调用同步 SDK，避免阻塞事件循环
        stream = await loop.run_in_executor(None, _do_stream)

        full_text = ""
        for chunk in stream:
            if not chunk.choices:
                continue
            delta = chunk.choices[0].delta.content if chunk.choices[0].delta.content else ""
            if delta:
                full_text += delta
                yield delta

        # 追加来源信息
        if "📚 数据来源" not in full_text:
            source_info = self._format_source_info_v3(summaries)
            suffix = f"\n\n{source_info}"
            yield suffix

    async def process_query_stream(self, query: str, user_id: int):
        """
        流式处理用户查询（异步生成器版本）

        先完成检索/改写等预处理阶段，然后流式 yield AI 生成的文本。

        Yields:
            str: 文本片段，以 "__DONE__" 结尾表示完成，
                 以 "__ERROR__:<msg>" 表示出错，
                 以 "__NEW_SESSION__" 表示开始了新会话。
        """
        try:
            logger.info(f"[stream] 处理查询: user_id={user_id}, query={query}")

            # 1. 获取或创建会话
            session_id, is_new_session = self.conversation_mgr.get_or_create_session(user_id)

            # 2. 保存用户消息
            self.conversation_mgr.save_message(
                user_id=user_id,
                session_id=session_id,
                role='user',
                content=query
            )

            # 3. 解析查询意图
            parsed = self.intent_parser.parse_query(query)
            intent = parsed["intent"]

            # 4. 非内容查询直接返回（不使用流式）
            if intent == "status":
                answer = await self._handle_status_query()
                yield answer
                self.conversation_mgr.save_message(
                    user_id=user_id, session_id=session_id,
                    role='assistant', content=answer
                )
                yield "__DONE__"
                return

            if intent == "stats":
                answer = await self._handle_stats_query(parsed)
                yield answer
                self.conversation_mgr.save_message(
                    user_id=user_id, session_id=session_id,
                    role='assistant', content=answer
                )
                yield "__DONE__"
                return

            # 5. 内容查询：先完成检索阶段，再流式生成
            if is_new_session:
                yield "__NEW_SESSION__"

            original_query = parsed["original_query"]
            keywords = parsed.get("keywords", [])
            time_range = parsed.get("time_range")

            conversation_history = self.conversation_mgr.get_conversation_history(
                user_id, session_id
            )

            # 查询改写
            search_query = original_query
            query_rewritten = False
            if (not is_new_session
                    and len(conversation_history) >= 3
                    and PRONOUN_PATTERNS.search(original_query)):
                try:
                    search_query = await self._rewrite_query(original_query, conversation_history)
                    if search_query != original_query:
                        query_rewritten = True
                        logger.info(f"[stream] 查询改写: '{original_query}' → '{search_query}'")
                except Exception as e:
                    logger.warning(f"[stream] 查询改写失败: {e}")
                    search_query = original_query

            # 时间过滤
            date_after: Optional[str] = None
            if time_range is not None:
                from datetime import datetime, timedelta, timezone
                cutoff = datetime.now(timezone.utc) - timedelta(days=time_range)
                date_after = cutoff.isoformat()

            # 语义检索
            semantic_results = []
            if self.vector_store.is_available():
                try:
                    semantic_results = self.vector_store.search_similar(
                        query=search_query, top_k=20, date_after=date_after
                    )
                except Exception as e:
                    logger.error(f"[stream] 语义检索失败: {e}")

            # 关键词检索
            keyword_results = []
            if keywords or len(semantic_results) < 5:
                try:
                    search_days = time_range if time_range is not None else 90
                    keyword_results = self.memory_manager.search_summaries(
                        keywords=keywords, time_range_days=search_days, limit=10
                    )
                except Exception as e:
                    logger.error(f"[stream] 关键词检索失败: {e}")

            # 融合
            if semantic_results and keyword_results:
                final_candidates = self._rrf_fusion(semantic_results, keyword_results)
            elif semantic_results:
                final_candidates = semantic_results
            elif keyword_results:
                final_candidates = [
                    {
                        'summary_id': r['id'],
                        'summary_text': r['summary_text'],
                        'metadata': {
                            'channel_id': r.get('channel_id'),
                            'channel_name': r.get('channel_name'),
                            'created_at': r.get('created_at')
                        }
                    }
                    for r in keyword_results
                ]
            else:
                if time_range is not None and time_range <= 7:
                    no_result = (
                        f"🔍 在最近 {time_range} 天内未找到相关总结。\n\n"
                        f"💡 提示：可以尝试扩大时间范围，例如'最近30天关于...'。"
                    )
                else:
                    no_result = "🔍 未找到相关总结。\n\n💡 提示：尝试调整关键词或时间范围。"
                yield no_result
                self.conversation_mgr.save_message(
                    user_id=user_id, session_id=session_id,
                    role='assistant', content=no_result
                )
                yield "__DONE__"
                return

            # 重排序
            if self.reranker.is_available() and len(final_candidates) > 5:
                try:
                    final_candidates = self.reranker.rerank(
                        search_query, final_candidates, top_k=5
                    )
                except Exception as e:
                    logger.error(f"[stream] 重排序失败: {e}")
                    final_candidates = final_candidates[:5]
            else:
                final_candidates = final_candidates[:5]

            # 流式生成回答
            full_answer = ""
            try:
                async for chunk in self.generate_answer_stream(
                    query=original_query,
                    summaries=final_candidates,
                    keywords=keywords,
                    conversation_history=conversation_history,
                    search_query=search_query,
                    query_rewritten=query_rewritten
                ):
                    full_answer += chunk
                    yield chunk
            except Exception as e:
                logger.error(f"[stream] AI生成失败，降级到非流式: {e}")
                fallback = self._fallback_answer_v3(final_candidates)
                yield fallback
                full_answer = fallback

            # 保存完整回答到对话历史
            if is_new_session:
                full_answer = "🍃 *开始新的对话。*\n\n" + full_answer
            self.conversation_mgr.save_message(
                user_id=user_id, session_id=session_id,
                role='assistant', content=full_answer
            )

            yield "__DONE__"

        except Exception as e:
            logger.error(f"[stream] 处理查询失败: {type(e).__name__}: {e}", exc_info=True)
            yield "__ERROR__:❌ 处理查询时出错，请稍后重试。"

    def _prepare_rag_context(self, summaries: List[Dict[str, Any]]) -> str:
        """
        准备RAG上下文信息

        动态分配每条总结的最大字符数：
        - 1条结果: 最多 2000 字符
        - 2条结果: 最多 1500 字符
        - 3-4条结果: 最多 1000 字符
        - 5条结果: 最多 800 字符
        """
        count = len(summaries[:5])
        if count <= 1:
            max_chars = 2000
        elif count == 2:
            max_chars = 1500
        elif count <= 4:
            max_chars = 1000
        else:
            max_chars = 800

        context_parts = []
        for i, summary in enumerate(summaries[:5], 1):
            metadata = summary.get('metadata', {})
            channel_name = metadata.get('channel_name') or summary.get('channel_name', '未知频道')
            created_at = metadata.get('created_at') or summary.get('created_at', '')
            summary_text = summary.get('summary_text', '')

            # 动态截断
            text_preview = summary_text[:max_chars] + "..." if len(summary_text) > max_chars else summary_text

            # 分数信息
            score_info = ""
            if 'similarity' in summary:
                score_info = f" [相似度: {summary['similarity']:.2f}]"
            if 'rerank_score' in summary:
                score_info += f" [重排分: {summary['rerank_score']:.2f}]"

            context_parts.append(
                f"[{i}] {channel_name} ({created_at}){score_info}\n{text_preview}"
            )

        return "\n\n".join(context_parts)

    def _format_source_info_v3(self, summaries: List[Dict[str, Any]]) -> str:
        """格式化来源信息（v3版本）"""
        channels = {}
        for s in summaries:
            metadata = s.get('metadata', {})
            channel_id = metadata.get('channel_id') or s.get('channel_id', '')
            channel_name = metadata.get('channel_name') or s.get('channel_name', '未知频道')

            if channel_id not in channels:
                channels[channel_id] = {
                    'name': channel_name,
                    'count': 0
                }
            channels[channel_id]['count'] += 1

        sources = [f"• {info['name']}: {info['count']}条"
                  for info in channels.values()]

        return f"📚 数据来源: {len(sources)}个频道\n" + "\n".join(sources)

    def _fallback_answer_v3(self, summaries: List[Dict[str, Any]]) -> str:
        """降级方案：直接返回总结摘要（v3版本）"""
        result = "📋 相关总结摘要：\n\n"

        for i, summary in enumerate(summaries[:3], 1):
            metadata = summary.get('metadata', {})
            channel_name = metadata.get('channel_name') or summary.get('channel_name', '未知频道')
            created_at = (metadata.get('created_at') or summary.get('created_at', ''))[:10]
            text = summary.get('summary_text', '')[:300]

            result += f"{i}. **{channel_name}** ({created_at})\n{text}...\n\n"

        return result


# 创建全局问答引擎v3实例
qa_engine_v3 = None

def get_qa_engine_v3():
    """获取全局问答引擎v3实例"""
    global qa_engine_v3
    if qa_engine_v3 is None:
        qa_engine_v3 = QAEngineV3()
    return qa_engine_v3
