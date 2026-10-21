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
记忆管理器 - 提取和管理总结元数据
"""

import json
import logging
from datetime import UTC, datetime, timedelta
from typing import Any

from core.ai.ai_client import get_ai_runtime
from core.config import normalize_channel_id
from core.infrastructure.database import get_db_manager

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

            client, model = get_ai_runtime()
            response = client.chat.completions.create(
                model=model,
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

    async def update_channel_profile(
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

            await self.db.update_channel_profile(
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

    async def get_channel_context(self, channel_id: str | None = None) -> str:
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

            profile = await self.db.get_channel_profile(channel_id)
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

    async def list_channels(self) -> list[dict[str, Any]]:
        """
        获取所有可查询频道。

        Returns:
            频道列表，包含频道链接、名称、更新时间和统计信息
        """
        try:
            channels = await self.db.get_all_channels()
            return [self._normalize_channel_row(channel) for channel in channels]
        except Exception as e:
            logger.error(f"获取频道列表失败: {type(e).__name__}: {e}", exc_info=True)
            return []

    async def resolve_channel(self, channel_hint: str) -> dict[str, Any]:
        """
        将用户输入的频道提示解析为标准频道链接。

        Args:
            channel_hint: 频道链接、@username、频道名或链接尾段

        Returns:
            解析结果。唯一匹配时 success=True；多候选时返回 candidates。
        """
        try:
            hint = (channel_hint or "").strip()
            if not hint:
                return {"success": False, "error": "频道提示为空", "candidates": []}

            channels = await self.list_channels()
            normalized_hint = normalize_channel_id(hint)
            hint_lower = hint.lower().lstrip("@")
            normalized_lower = normalized_hint.lower()
            hint_tail_lower = normalized_hint.rstrip("/").split("/")[-1].lower().lstrip("@")

            exact_matches = []
            fuzzy_matches = []

            for channel in channels:
                channel_id = channel.get("channel_id", "")
                channel_name = channel.get("channel_name", "") or ""
                channel_tail = channel_id.rstrip("/").split("/")[-1]
                candidates = {
                    channel_id.lower(),
                    normalize_channel_id(channel_id).lower(),
                    channel_tail.lower(),
                    channel_name.lower(),
                }

                if (
                    normalized_lower in candidates
                    or hint_lower in candidates
                    or hint_tail_lower in candidates
                ):
                    exact_matches.append(channel)
                    continue

                if (
                    hint_lower in channel_name.lower()
                    or hint_lower in channel_tail.lower()
                    or hint_tail_lower in channel_tail.lower()
                ):
                    fuzzy_matches.append(channel)

            matches = exact_matches or fuzzy_matches
            if len(matches) == 1:
                match = matches[0]
                return {
                    "success": True,
                    "channel_id": match.get("channel_id"),
                    "channel_name": match.get("channel_name"),
                    "match_type": "exact" if exact_matches else "fuzzy",
                    "channel": match,
                }

            if len(matches) > 1:
                return {
                    "success": False,
                    "error": "找到多个匹配频道，请提供更完整的频道链接或名称",
                    "candidates": matches[:10],
                }

            # 没有目录匹配时，若输入看起来像 Telegram 标识，返回标准化链接供后续检索尝试。
            if hint.startswith(("@", "http://", "https://", "t.me/", "telegram.me/")):
                return {
                    "success": True,
                    "channel_id": normalized_hint,
                    "channel_name": normalized_hint.split("/")[-1],
                    "match_type": "normalized",
                    "channel": {
                        "channel_id": normalized_hint,
                        "channel_name": normalized_hint.split("/")[-1],
                    },
                }

            return {"success": False, "error": "未找到匹配频道", "candidates": []}

        except Exception as e:
            logger.error(f"解析频道失败: {type(e).__name__}: {e}", exc_info=True)
            return {"success": False, "error": f"解析频道失败: {e}", "candidates": []}

    async def get_channel_stats(self, channel_id: str) -> dict[str, Any]:
        """
        获取频道统计信息。

        Args:
            channel_id: 标准频道链接

        Returns:
            频道统计字典
        """
        try:
            stats = await self.db.get_channel_summary_stats(channel_id)
            return self._normalize_channel_row(stats) if stats else {}
        except Exception as e:
            logger.error(f"获取频道统计失败: {type(e).__name__}: {e}", exc_info=True)
            return {}

    async def get_recent_summaries(
        self,
        channel_id: str | None = None,
        limit: int = 5,
        time_range_days: int | None = None,
    ) -> list[dict[str, Any]]:
        """
        获取最近总结。

        Args:
            channel_id: 可选频道链接
            limit: 返回数量
            time_range_days: 可选时间范围；不传时返回不限时间范围的最近 N 条

        Returns:
            最近总结列表
        """
        try:
            end_date = datetime.now(UTC)
            start_date = end_date - timedelta(days=time_range_days) if time_range_days else None
            return await self.db.get_recent_summaries(
                channel_id=channel_id,
                limit=limit,
                start_date=start_date,
                end_date=end_date if time_range_days else None,
            )
        except Exception as e:
            logger.error(f"获取最近总结失败: {type(e).__name__}: {e}", exc_info=True)
            return []

    async def search_summaries(
        self,
        keywords: list[str] = None,
        topics: list[str] = None,
        time_range_days: int = 7,
        channel_id: str | None = None,
        limit: int = 10,
        date_before: datetime | None = None,
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
            end_date = date_before or datetime.now(UTC)
            start_date = end_date - timedelta(days=time_range_days)

            # 获取基础总结
            summaries = await self.db.get_summaries(
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
                except Exception:
                    summary_topics = []

                try:
                    if summary_keywords:
                        summary_keywords = json.loads(summary_keywords)
                    else:
                        summary_keywords = []
                except Exception:
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

    @staticmethod
    def _normalize_channel_row(channel: dict[str, Any]) -> dict[str, Any]:
        """标准化频道行中的日期和数值字段。"""
        normalized = dict(channel or {})
        for field in ("last_summary_time", "first_summary_time"):
            value = normalized.get(field)
            if hasattr(value, "isoformat"):
                normalized[field] = value.isoformat()
        normalized["summary_count"] = int(normalized.get("summary_count") or 0)
        normalized["message_count"] = int(normalized.get("message_count") or 0)
        return normalized


# 创建全局记忆管理器实例
memory_manager = None


def get_memory_manager():
    """获取全局记忆管理器实例"""
    global memory_manager
    if memory_manager is None:
        memory_manager = MemoryManager()
    return memory_manager
