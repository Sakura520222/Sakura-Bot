# -*- coding: utf-8 -*-
# Copyright 2026 Sakura-频道总结助手
#
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可，
# 并附加非商业使用限制条款。
#
# - 署名：必须提供本项目的原始来源链接
# - 非商业：禁止任何商业用途和分发
# - 相同方式共享：衍生作品必须采用相同的许可证
#
# 本项目源代码：https://github.com/Sakura520222/Sakura-Channel-Summary-Assistant
# 许可证全文：参见 LICENSE 文件

"""
问答引擎 v3.0.0 - 集成向量搜索和重排序
实现语义检索 + RAG架构
"""

import logging
from typing import Dict, Any, List, Optional
from .database import get_db_manager
from .intent_parser import get_intent_parser
from .memory_manager import get_memory_manager
from .vector_store import get_vector_store
from .reranker import get_reranker
from .ai_client import client_llm
from .settings import get_llm_model

logger = logging.getLogger(__name__)


class QAEngineV3:
    """问答引擎 v3.0.0 - 向量搜索版本"""

    def __init__(self):
        """初始化问答引擎"""
        self.db = get_db_manager()
        self.intent_parser = get_intent_parser()
        self.memory_manager = get_memory_manager()
        self.vector_store = get_vector_store()
        self.reranker = get_reranker()
        logger.info("问答引擎v3.0.0初始化完成")

    async def process_query(self, query: str, user_id: int) -> str:
        """
        处理用户查询

        Args:
            query: 用户查询
            user_id: 用户ID

        Returns:
            回答文本
        """
        try:
            logger.info(f"处理查询: user_id={user_id}, query={query}")

            # 1. 解析查询意图
            parsed = self.intent_parser.parse_query(query)
            logger.info(f"查询意图: {parsed['intent']}, 置信度: {parsed['confidence']}")

            # 2. 根据意图处理
            intent = parsed["intent"]

            if intent == "status":
                return await self._handle_status_query()
            elif intent == "stats":
                return await self._handle_stats_query(parsed)
            else:
                return await self._handle_content_query_v3(parsed)

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
• 今日已使用: {status['used_today']} 次
• 今日剩余: {status['remaining']} 次
• 使用率: {status['utilization']}{vector_info}

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

    async def _handle_content_query_v3(self, parsed: Dict[str, Any]) -> str:
        """
        处理内容查询（v3.0.0向量搜索版本）

        实现混合检索策略：
        1. 语义检索（Dense）
        2. 关键词检索（Sparse）作为备选
        3. RRF融合
        4. 重排序
        """
        try:
            query = parsed["original_query"]
            keywords = parsed.get("keywords", [])
            time_range = parsed.get("time_range", 7)

            # 步骤1: 语义检索（召回Top-20）
            semantic_results = []
            if self.vector_store.is_available():
                try:
                    semantic_results = self.vector_store.search_similar(
                        query=query,
                        top_k=20
                    )
                    logger.info(f"语义检索: 找到 {len(semantic_results)} 条结果")
                except Exception as e:
                    logger.error(f"语义检索失败: {e}")

            # 步骤2: 关键词检索（备选方案）
            keyword_results = []
            if keywords or not semantic_results:
                try:
                    from datetime import datetime, timezone, timedelta
                    # 确保time_range不为None
                    search_days = time_range if time_range is not None else 7
                    end_date = datetime.now(timezone.utc)
                    start_date = end_date - timedelta(days=search_days)

                    keyword_results = self.memory_manager.search_summaries(
                        keywords=keywords,
                        time_range_days=search_days,
                        limit=10
                    )
                    logger.info(f"关键词检索: 找到 {len(keyword_results)} 条结果")
                except Exception as e:
                    logger.error(f"关键词检索失败: {e}")

            # 步骤3: 融合结果
            if semantic_results and keyword_results:
                # 使用RRF融合
                final_candidates = self._rrf_fusion(semantic_results, keyword_results)
                logger.info(f"RRF融合: {len(final_candidates)} 条结果")
            elif semantic_results:
                # 只使用语义检索结果
                final_candidates = semantic_results
            elif keyword_results:
                # 只使用关键词检索结果
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
                # 都没有结果
                return f"🔍 未找到相关总结。\n\n💡 提示：尝试调整关键词或时间范围。"

            # 步骤4: 重排序（Top-20 → Top-5）
            if self.reranker.is_available() and len(final_candidates) > 5:
                try:
                    final_candidates = self.reranker.rerank(query, final_candidates, top_k=5)
                    logger.info(f"重排序完成: 保留 {len(final_candidates)} 条结果")
                except Exception as e:
                    logger.error(f"重排序失败: {e}")
                    final_candidates = final_candidates[:5]
            else:
                final_candidates = final_candidates[:5]

            # 步骤5: AI生成回答（RAG）
            answer = await self._generate_answer_with_rag(
                query=query,
                summaries=final_candidates,
                keywords=keywords
            )

            return answer

        except Exception as e:
            logger.error(f"处理内容查询失败: {type(e).__name__}: {e}", exc_info=True)
            return "❌ 查询失败，请稍后重试。"

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
        # 构建ID到结果的映射
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
                # 合并分数
                result_map[summary_id]['score'] += score
                result_map[summary_id]['source'] = 'hybrid'
            else:
                # 添加新结果
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

        # 按分数排序
        sorted_results = sorted(
            result_map.values(),
            key=lambda x: x['score'],
            reverse=True
        )

        # 返回融合后的结果列表
        return [item['summary'] for item in sorted_results]

    async def _generate_answer_with_rag(self, query: str,
                                        summaries: List[Dict[str, Any]],
                                        keywords: List[str] = None) -> str:
        """
        使用RAG生成回答

        Args:
            query: 用户查询
            summaries: 相关总结列表
            keywords: 关键词

        Returns:
            生成的回答
        """
        try:
            # 准备上下文
            context = self._prepare_rag_context(summaries)

            # 获取频道画像
            channel_ids = list(set(
                s.get('metadata', {}).get('channel_id') or s.get('channel_id', '')
                for s in summaries
            ))
            channel_context = ""
            if len(channel_ids) == 1 and channel_ids[0]:
                channel_context = self.memory_manager.get_channel_context(channel_ids[0])
            elif len(channel_ids) > 1:
                channel_context = "多频道综合查询"

            # 构建提示词
            prompt = f"""你是一个专业的资讯助手，负责根据历史总结回答用户问题。

{channel_context}

用户查询：{query}

相关历史总结（共{len(summaries)}条，已通过语义搜索和重排序精选）：
{context}

要求（严格遵循）：
1. 基于上述总结内容回答问题，不要编造信息
2. 如果总结中没有相关信息，明确说明
3. 使用清晰的结构和要点
4. 语言简洁专业
5. **Markdown格式要求**：
   - 粗体：使用 **文本** （注意两边各两个星号）
   - 斜体：使用 *文本* （注意两边各一个星号）
   - 代码：使用 `代码` （反引号）
   - **禁止使用 # 标题格式**
   - 列表：使用 - 或 • 开头
   - 链接：使用 [文本](URL) 格式
   - **禁止使用未配对的星号、下划线或反引号**
   - **所有特殊字符必须成对出现**

请用严格的Markdown格式回答（不使用#标题）："""

            logger.info(f"调用AI生成回答（RAG），总结数: {len(summaries)}")

            response = client_llm.chat.completions.create(
                model=get_llm_model(),
                messages=[
                    {
                        "role": "system",
                        "content": "你是一个专业的资讯助手，擅长从历史记录中提取关键信息并回答用户问题。"
                    },
                    {"role": "user", "content": prompt}
                ],
                temperature=0.7
            )

            answer = response.choices[0].message.content.strip()
            logger.info(f"AI回答生成成功，长度: {len(answer)}字符")

            # 添加来源信息
            source_info = self._format_source_info_v3(summaries)
            return f"{answer}\n\n{source_info}"

        except Exception as e:
            error_type = type(e).__name__
            error_msg = str(e)
            
            logger.error(f"AI生成回答失败: {error_type}: {error_msg}", exc_info=True)
            
            # 检查是否是内容审核拦截
            if 'Moderation Block' in error_msg or 'content_filter' in error_msg:
                return """🍀 **知识的殿堂不应被迷雾笼罩。**

在这个世界上，有些思绪会像枯萎的枝叶一样被世界树排斥。你的查询触碰了不被允许的领域，所以我无法为你解读这部分信息。

💡 **建议你换个角度思考：**
• 询问频道里最近盛开的"知识果实"（近期总结）
• 查询特定主题在世界树中的"根系"（历史记录）
• 看看大家都在为了什么而努力奋斗？

"去吧，让智慧重新流淌在清澈的溪流中。\""""
            
            # 其他错误：降级方案，直接返回总结摘要
            return self._fallback_answer_v3(summaries)

    def _prepare_rag_context(self, summaries: List[Dict[str, Any]]) -> str:
        """准备RAG上下文信息"""
        context_parts = []

        for i, summary in enumerate(summaries[:5], 1):
            # 从metadata或直接字段获取信息
            metadata = summary.get('metadata', {})
            channel_name = metadata.get('channel_name') or summary.get('channel_name', '未知频道')
            created_at = metadata.get('created_at') or summary.get('created_at', '')
            summary_text = summary.get('summary_text', '')

            # 提取摘要（前500字符）
            text_preview = summary_text[:500] + "..." if len(summary_text) > 500 else summary_text

            # 添加相似度分数（如果有）
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
            text = summary.get('summary_text', '')[:200]

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