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
频道评论区AI聊天引擎
负责处理评论区AI查询，复用RAG检索能力
"""

import logging
import os
from datetime import datetime
from typing import Any

from core.ai.ai_client import client_llm
from core.ai.comment_context import CommentContext, format_comment_context_for_ai
from core.ai.comment_conversation_manager import get_comment_conversation_manager
from core.ai.intent_parser import get_intent_parser
from core.ai.memory_manager import get_memory_manager
from core.ai.reranker import get_reranker
from core.ai.vector_store import get_vector_store
from core.settings import get_llm_model

logger = logging.getLogger(__name__)

# 评论区系统提示词模板
COMMENT_SYSTEM_TEMPLATE = """{persona_description}

---

## 核心任务
你是小樱，一个智能的频道评论区助手。你的任务是参与频道消息的评论区讨论。

## 核心约束（必须严格遵守）
1. **基于事实**：严格基于提供的历史总结内容回答，不要编造信息
2. **理解上下文**：优先利用评论区的对话上下文理解用户问题
3. **简洁有力**：评论区环境要求回答简洁，避免长篇大论
4. **友好亲切**：使用口语化的表达，可以适度使用emoji
5. **明确回答**：总结中没有相关信息时，明确说明
6. **Markdown规范**：
   - 粗体：使用 **文本** （两边各两个星号）
   - 斜体：使用 *文本* （两边各一个星号）
   - 代码：使用 `代码` （反引号）
   - **禁止使用 # 标题格式**
   - 列表：使用 - 或 • 开头
   - **禁止使用未配对的星号、下划线或反引号**

## 当前评论区上下文
{comment_context}

## AI对话历史
{conversation_context}
"""


class CommentChatEngine:
    """评论区AI聊天引擎 - 处理评论区AI查询"""

    def __init__(self):
        """初始化评论区AI引擎"""
        self.intent_parser = get_intent_parser()
        self.memory_manager = get_memory_manager()
        self.vector_store = get_vector_store()
        self.reranker = get_reranker()
        self.conversation_mgr = get_comment_conversation_manager()
        self._persona_cache = None
        logger.info("评论区AI引擎初始化完成")

    def _get_persona_description(self) -> str:
        """获取AI人格描述（带缓存）"""
        if self._persona_cache is not None:
            return self._persona_cache

        try:
            persona_file = os.path.join("data", "comment_chat_persona.txt")
            if os.path.exists(persona_file):
                with open(persona_file, encoding="utf-8") as f:
                    self._persona_cache = f.read().strip()
                    return self._persona_cache
        except Exception as e:
            logger.warning(f"读取评论区AI人格文件失败: {e}")

        # 默认人格
        self._persona_cache = (
            "你是小樱，一个智能的频道评论区助手。"
            "你的任务是参与频道消息的评论区讨论，为用户提供有价值的信息和见解。"
        )
        return self._persona_cache

    async def process_comment_query(
        self,
        comment_context: CommentContext,
        user_query: str,
        user_id: int,
        username: str,
    ) -> str:
        """
        处理评论区查询

        核心流程：
        1. 获取/创建评论区会话
        2. 构建包含评论上下文的用户查询
        3. 调用 QAEngineV3 的检索能力进行检索
        4. 生成回答（使用评论区专用prompt）
        5. 保存对话历史

        Args:
            comment_context: 评论上下文
            user_query: 用户查询
            user_id: 用户ID
            username: 用户名

        Returns:
            AI回答
        """
        try:
            logger.info(
                f"处理评论区查询: channel={comment_context.channel_id}, "
                f"msg_id={comment_context.channel_msg_id}, user={username}, query={user_query}"
            )

            # 1. 获取或创建会话
            session_id, is_new_session = await self.conversation_mgr.get_or_create_comment_session(
                channel_id=comment_context.channel_id,
                channel_msg_id=comment_context.channel_msg_id,
                discussion_id=comment_context.discussion_id,
            )

            # 2. 保存用户消息
            await self.conversation_mgr.save_comment_message(
                session_id=session_id,
                user_id=user_id,
                username=username,
                role="user",
                content=user_query,
            )

            # 3. 解析查询意图
            parsed = self.intent_parser.parse_query(user_query)
            logger.info(f"查询意图: {parsed['intent']}, 置信度: {parsed['confidence']}")

            # 4. 执行检索（复用QAEngineV3的检索能力）
            search_results = []
            keywords = parsed.get("keywords", [])

            # 语义检索
            if self.vector_store.is_available():
                try:
                    search_query = parsed["original_query"]
                    semantic_results = self.vector_store.search_similar(
                        query=search_query, top_k=10
                    )
                    logger.info(f"语义检索: 找到 {len(semantic_results)} 条结果")

                    if semantic_results:
                        search_results.extend(
                            [
                                {
                                    "summary_id": r["summary_id"],
                                    "summary_text": r["summary_text"],
                                    "metadata": r.get("metadata", {}),
                                }
                                for r in semantic_results
                            ]
                        )
                except Exception as e:
                    logger.error(f"语义检索失败: {e}")

            # 关键词检索（补充）
            if keywords or len(search_results) < 5:
                try:
                    keyword_results = await self.memory_manager.search_summaries(
                        keywords=keywords, time_range_days=90, limit=10
                    )
                    logger.info(f"关键词检索: 找到 {len(keyword_results)} 条结果")

                    for r in keyword_results:
                        # 避免重复
                        if not any(s["summary_id"] == r["id"] for s in search_results):
                            search_results.append(
                                {
                                    "summary_id": r["id"],
                                    "summary_text": r["summary_text"],
                                    "metadata": {
                                        "channel_id": r.get("channel_id"),
                                        "channel_name": r.get("channel_name"),
                                        "created_at": r.get("created_at"),
                                    },
                                }
                            )
                except Exception as e:
                    logger.error(f"关键词检索失败: {e}")

            # 重排序（可选）
            if self.reranker.is_available() and len(search_results) > 5:
                try:
                    search_results = self.reranker.rerank(user_query, search_results, top_k=5)
                    logger.info(f"重排序完成: 保留 {len(search_results)} 条结果")
                except Exception as e:
                    logger.error(f"重排序失败: {e}")
                    search_results = search_results[:5]
            else:
                search_results = search_results[:5]

            # 5. 获取历史上下文
            conversation_history = await self.conversation_mgr.get_comment_conversation_history(
                session_id
            )

            # 6. 生成回答
            answer = await self._generate_comment_answer(
                query=user_query,
                summaries=search_results,
                comment_context=comment_context,
                conversation_history=conversation_history,
            )

            # 7. 保存助手回复
            await self.conversation_mgr.save_comment_message(
                session_id=session_id,
                user_id=0,  # Bot使用user_id=0
                username="小樱",
                role="assistant",
                content=answer,
            )

            return answer

        except Exception as e:
            logger.error(f"处理评论区查询失败: {e}", exc_info=True)
            return "抱歉，处理你的问题时出错了，请稍后再试。"

    async def _generate_comment_answer(
        self,
        query: str,
        summaries: list[dict[str, Any]],
        comment_context: CommentContext,
        conversation_history: list[dict] = None,
    ) -> str:
        """
        使用RAG生成回答（评论区专用）

        Args:
            query: 用户查询
            summaries: 相关总结列表
            comment_context: 评论上下文
            conversation_history: 对话历史

        Returns:
            生成的回答
        """
        try:
            # 构建评论上下文
            comment_context_text = format_comment_context_for_ai(comment_context)

            # 构建对话上下文
            conversation_context_text = ""
            if conversation_history and len(conversation_history) > 1:
                # 排除最后一条（就是当前的用户查询）
                history_excluding_current = conversation_history[:-1]
                if history_excluding_current:
                    formatted_history = self.conversation_mgr.format_comment_conversation_context(
                        history_excluding_current, comment_context
                    )
                    conversation_context_text = f"\n【之前的对话】\n{formatted_history}\n"

            # 构建RAG上下文
            rag_context = self._prepare_rag_context(summaries)

            # 获取人格描述
            persona_description = self._get_persona_description()

            # 构建系统提示词
            system_prompt = COMMENT_SYSTEM_TEMPLATE.format(
                persona_description=persona_description,
                comment_context=comment_context_text,
                conversation_context=conversation_context_text,
            )

            # 构建用户提示词
            user_prompt = f"""【用户问题】
{query}

【相关频道历史】
{rag_context}

请根据上述信息回答用户的问题。"""

            logger.info(
                f"调用AI生成评论区回答，总结数: {len(summaries)}, "
                f"历史消息: {len(conversation_history) if conversation_history else 0}"
            )

            response = client_llm.chat.completions.create(
                model=get_llm_model(),
                messages=[
                    {"role": "system", "content": system_prompt},
                    {"role": "user", "content": user_prompt},
                ],
                temperature=0.7,
            )

            answer = response.choices[0].message.content.strip()
            logger.info(f"AI回答生成成功，长度: {len(answer)}字符")

            return answer

        except Exception as e:
            logger.error(f"AI生成回答失败: {e}", exc_info=True)

            # 降级方案：直接返回总结摘要
            if summaries:
                return self._fallback_answer(summaries, comment_context)
            return "抱歉，我暂时无法回答这个问题。"

    def _prepare_rag_context(self, summaries: list[dict[str, Any]]) -> str:
        """
        准备RAG上下文信息

        Args:
            summaries: 总结列表

        Returns:
            格式化的上下文文本
        """
        if not summaries:
            return "（暂无相关历史记录）"

        context_parts = []
        for i, summary in enumerate(summaries[:5], 1):
            metadata = summary.get("metadata", {})
            channel_name = metadata.get("channel_name") or "未知频道"

            # 处理created_at可能是datetime对象的情况
            created_at_raw = metadata.get("created_at", "")
            if isinstance(created_at_raw, datetime):
                created_at = created_at_raw.strftime("%Y-%m-%d")
            elif isinstance(created_at_raw, str):
                created_at = created_at_raw[:10]
            else:
                created_at = str(created_at_raw)[:10] if created_at_raw else ""

            summary_text = summary.get("summary_text", "")

            # 截断过长的总结
            if len(summary_text) > 500:
                summary_text = summary_text[:500] + "..."

            context_parts.append(f"[{i}] {channel_name} ({created_at})\n{summary_text}")

        return "\n\n".join(context_parts)

    def _fallback_answer(
        self, summaries: list[dict[str, Any]], comment_context: CommentContext
    ) -> str:
        """
        降级方案：直接返回总结摘要

        Args:
            summaries: 总结列表
            comment_context: 评论上下文

        Returns:
            简单的回答
        """
        result = "根据频道历史记录：\n\n"

        for i, summary in enumerate(summaries[:3], 1):
            metadata = summary.get("metadata", {})
            channel_name = metadata.get("channel_name") or "未知频道"
            text = summary.get("summary_text", "")[:300]

            result += f"{i}. {channel_name}\n{text}...\n\n"

        return result


# 创建全局评论区AI引擎实例
comment_chat_engine = None


def get_comment_chat_engine():
    """获取全局评论区AI引擎实例"""
    global comment_chat_engine
    if comment_chat_engine is None:
        comment_chat_engine = CommentChatEngine()
    return comment_chat_engine
