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
问答引擎 - 处理自然语言查询并生成回答
"""

import logging
from typing import Dict, Any, List, Optional
from .database import get_db_manager
from .intent_parser import get_intent_parser
from .memory_manager import get_memory_manager
from .ai_client import client_llm
from .settings import get_llm_model

logger = logging.getLogger(__name__)


class QAEngine:
    """问答引擎"""

    def __init__(self):
        """初始化问答引擎"""
        self.db = get_db_manager()
        self.intent_parser = get_intent_parser()
        self.memory_manager = get_memory_manager()
        logger.info("问答引擎初始化完成")

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
                return await self._handle_content_query(parsed)

        except Exception as e:
            logger.error(f"处理查询失败: {type(e).__name__}: {e}", exc_info=True)
            return "❌ 处理查询时出错，请稍后重试。"

    async def _handle_status_query(self) -> str:
        """处理状态查询"""
        from .quota_manager import get_quota_manager
        quota_mgr = get_quota_manager()
        status = quota_mgr.get_system_status()

        return f"""📊 系统状态

• 每日总限额: {status['daily_limit']} 次
• 今日剩余: {status['remaining']} 次

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

    async def _handle_content_query(self, parsed: Dict[str, Any]) -> str:
        """处理内容查询"""
        try:
            # 提取查询参数
            keywords = parsed.get("keywords", [])
            time_range = parsed.get("time_range", 7)

            # 搜索相关总结
            summaries = self.memory_manager.search_summaries(
                keywords=keywords,
                time_range_days=time_range,
                limit=10
            )

            if not summaries:
                return f"🔍 未找到相关总结。\n\n💡 提示：尝试调整关键词或时间范围。"

            # 使用AI生成回答
            answer = await self._generate_answer(
                query=parsed["original_query"],
                summaries=summaries,
                keywords=keywords
            )

            return answer

        except Exception as e:
            logger.error(f"处理内容查询失败: {type(e).__name__}: {e}", exc_info=True)
            return "❌ 查询失败，请稍后重试。"

    async def _generate_answer(self, query: str, summaries: List[Dict[str, Any]],
                              keywords: List[str] = None) -> str:
        """
        使用AI生成回答

        Args:
            query: 原始查询
            summaries: 相关总结列表
            keywords: 关键词

        Returns:
            生成的回答
        """
        try:
            # 准备上下文
            context = self._prepare_context(summaries)

            # 获取频道画像
            channel_ids = list(set(s.get('channel_id') for s in summaries))
            channel_context = ""
            if len(channel_ids) == 1:
                channel_context = self.memory_manager.get_channel_context(channel_ids[0])
            elif len(channel_ids) > 1:
                channel_context = "多频道综合查询"

            # 构建提示词
            prompt = f"""你是一个专业的资讯助手，负责根据历史总结回答用户问题。

{channel_context}

用户查询：{query}

相关历史总结（共{len(summaries)}条）：
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

            logger.info(f"调用AI生成回答，总结数: {len(summaries)}")

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
            source_info = self._format_source_info(summaries)
            return f"{answer}\n\n{source_info}"

        except Exception as e:
            logger.error(f"AI生成回答失败: {type(e).__name__}: {e}", exc_info=True)
            # 降级方案：直接返回总结摘要
            return self._fallback_answer(summaries)

    def _prepare_context(self, summaries: List[Dict[str, Any]]) -> str:
        """准备上下文信息"""
        context_parts = []

        for i, summary in enumerate(summaries[:5], 1):  # 最多5条
            channel_name = summary.get('channel_name', '未知频道')
            created_at = summary.get('created_at', '')
            summary_text = summary.get('summary_text', '')

            # 提取摘要（前500字符）
            text_preview = summary_text[:500] + "..." if len(summary_text) > 500 else summary_text

            context_parts.append(
                f"[{i}] {channel_name} ({created_at})\n{text_preview}"
            )

        return "\n\n".join(context_parts)

    def _format_source_info(self, summaries: List[Dict[str, Any]]) -> str:
        """格式化来源信息"""
        channels = {}
        for s in summaries:
            channel_id = s.get('channel_id', '')
            channel_name = s.get('channel_name', '未知频道')
            if channel_id not in channels:
                channels[channel_id] = {
                    'name': channel_name,
                    'count': 0
                }
            channels[channel_id]['count'] += 1

        sources = [f"• {info['name']}: {info['count']}条"
                  for info in channels.values()]

        return f"📚 数据来源: {len(sources)}个频道\n" + "\n".join(sources)

    def _fallback_answer(self, summaries: List[Dict[str, Any]]) -> str:
        """降级方案：直接返回总结摘要"""
        result = "📋 相关总结摘要：\n\n"

        for i, summary in enumerate(summaries[:3], 1):
            channel_name = summary.get('channel_name', '未知频道')
            created_at = summary.get('created_at', '')[:10]
            text = summary.get('summary_text', '')[:200]

            result += f"{i}. **{channel_name}** ({created_at})\n{text}...\n\n"

        return result


# 创建全局问答引擎实例
qa_engine = None

def get_qa_engine():
    """获取全局问答引擎实例"""
    global qa_engine
    if qa_engine is None:
        qa_engine = QAEngine()
    return qa_engine