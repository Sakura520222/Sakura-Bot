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
记忆管理器 - 提取和管理总结元数据
"""

import json
import logging
from datetime import UTC
from typing import Any

from .ai_client import client_llm
from .database import get_db_manager
from .settings import get_llm_model

logger = logging.getLogger(__name__)


class MemoryManager:
    """记忆管理器"""

    def __init__(self):
        """初始化记忆管理器"""
        self.db = get_db_manager()
        logger.info("记忆管理器初始化完成")

    def extract_metadata(self, summary_text: str) -> dict[str, Any]:
        """
        从总结中提取元数据（关键词、主题、情感、实体）

        Args:
            summary_text: 总结文本

        Returns:
            {
                "keywords": List[str],
                "topics": List[str],
                "sentiment": str,
                "entities": List[str]
            }
        """
        try:
            logger.info("开始提取总结元数据")

            # 使用AI提取元数据
            prompt = f"""请分析以下总结文本，提取关键信息，以JSON格式返回：

总结内容：
{summary_text[:1000]}

请提取：
1. keywords: 3-5个关键词
2. topics: 1-3个主题标签
3. sentiment: 情感倾向（positive/neutral/negative）
4. entities: 提及的重要实体（人名、组织、产品等）

返回JSON格式：
{{
    "keywords": ["关键词1", "关键词2", ...],
    "topics": ["主题1", "主题2", ...],
    "sentiment": "neutral",
    "entities": ["实体1", "实体2", ...]
}}

只返回JSON，不要其他内容。"""

            response = client_llm.chat.completions.create(
                model=get_llm_model(),
                messages=[
                    {"role": "system", "content": "你是一个专业的文本分析助手，擅长提取关键信息。"},
                    {"role": "user", "content": prompt},
                ],
                temperature=0.3,
            )

            result_text = response.choices[0].message.content.strip()

            # 解析JSON
            try:
                # 提取JSON部分
                import re

                json_match = re.search(r"\{.*\}", result_text, re.DOTALL)
                if json_match:
                    metadata = json.loads(json_match.group())
                else:
                    metadata = json.loads(result_text)

                logger.info(f"成功提取元数据: {metadata}")
                return metadata

            except json.JSONDecodeError:
                logger.warning(f"AI返回的JSON格式无效: {result_text}")
                # 返回默认值
                return self._get_default_metadata(summary_text)

        except Exception as e:
            logger.error(f"提取元数据失败: {type(e).__name__}: {e}", exc_info=True)
            return self._get_default_metadata(summary_text)

    def _get_default_metadata(self, summary_text: str) -> dict[str, Any]:
        """获取默认元数据"""
        return {"keywords": [], "topics": [], "sentiment": "neutral", "entities": []}

    def update_channel_profile(
        self, channel_id: str, channel_name: str, summary_text: str, metadata: dict[str, Any]
    ) -> None:
        """
        更新频道画像

        Args:
            channel_id: 频道URL
            channel_name: 频道名称
            summary_text: 总结文本
            metadata: 提取的元数据
        """
        try:
            keywords = metadata.get("keywords", [])
            topics = metadata.get("topics", [])
            sentiment = metadata.get("sentiment", "neutral")
            entities = metadata.get("entities", [])

            self.db.update_channel_profile(
                channel_id=channel_id,
                channel_name=channel_name,
                keywords=keywords,
                topics=topics,
                sentiment=sentiment,
                entities=entities,
            )

            logger.info(f"已更新频道画像: {channel_name}")

        except Exception as e:
            logger.error(f"更新频道画像失败: {type(e).__name__}: {e}", exc_info=True)

    def get_channel_context(self, channel_id: str | None = None) -> str:
        """
        获取频道上下文信息（用于AI生成回答）

        Args:
            channel_id: 频道URL，None表示所有频道

        Returns:
            上下文描述字符串
        """
        try:
            if not channel_id:
                return "这是一个多频道总结系统。"

            profile = self.db.get_channel_profile(channel_id)
            if not profile:
                return f"频道: {channel_id.split('/')[-1]}"

            style = profile.get("style", "neutral")
            topics = profile.get("topics", [])
            profile.get("tone", "neutral")
            total_summaries = profile.get("total_summaries", 0)

            style_map = {"tech": "技术专业", "casual": "轻松闲聊", "neutral": "中立客观"}

            context = f"频道特点: {style_map.get(style, '中立')}\n"
            if topics:
                context += f"常讨论主题: {', '.join(topics)}\n"
            context += f"已总结次数: {total_summaries}"

            return context

        except Exception as e:
            logger.error(f"获取频道上下文失败: {type(e).__name__}: {e}", exc_info=True)
            return ""

    def search_summaries(
        self,
        keywords: list[str] = None,
        topics: list[str] = None,
        time_range_days: int = 7,
        channel_id: str | None = None,
        limit: int = 10,
    ) -> list[dict[str, Any]]:
        """
        搜索相关总结

        Args:
            keywords: 关键词列表
            topics: 主题列表
            time_range_days: 时间范围（天数）
            channel_id: 频道URL
            limit: 返回数量限制

        Returns:
            匹配的总结列表
        """
        try:
            from datetime import datetime, timedelta

            # 计算时间范围
            end_date = datetime.now(UTC)
            start_date = end_date - timedelta(days=time_range_days)

            # 获取基础总结
            summaries = self.db.get_summaries(
                channel_id=channel_id, limit=limit, start_date=start_date, end_date=end_date
            )

            # 如果没有关键词，直接返回
            if not keywords and not topics:
                return summaries

            # 过滤匹配的总结
            filtered = []
            for summary in summaries:
                summary_text = summary.get("summary_text", "").lower()
                summary_topics = summary.get("topics", "[]")
                summary_keywords = summary.get("keywords", "[]")

                # 解析JSON字段
                try:
                    if summary_topics:
                        summary_topics = json.loads(summary_topics)
                    else:
                        summary_topics = []
                except:
                    summary_topics = []

                try:
                    if summary_keywords:
                        summary_keywords = json.loads(summary_keywords)
                    else:
                        summary_keywords = []
                except:
                    summary_keywords = []

                # 检查关键词匹配
                keyword_match = any(
                    kw.lower() in summary_text or kw.lower() in str(summary_keywords).lower()
                    for kw in keywords
                )

                # 检查主题匹配
                topic_match = (
                    any(topic.lower() in str(summary_topics).lower() for topic in topics)
                    if topics
                    else True
                )

                if keyword_match or topic_match:
                    filtered.append(summary)

            logger.info(f"搜索完成: 找到 {len(filtered)} 条匹配总结")
            return filtered[:limit]

        except Exception as e:
            logger.error(f"搜索总结失败: {type(e).__name__}: {e}", exc_info=True)
            return []


# 创建全局记忆管理器实例
memory_manager = None


def get_memory_manager():
    """获取全局记忆管理器实例"""
    global memory_manager
    if memory_manager is None:
        memory_manager = MemoryManager()
    return memory_manager
