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
自然语言意图解析器
理解用户的查询意图
"""

import logging
import re
from datetime import datetime, timezone, timedelta
from typing import Dict, Any, Optional, List

logger = logging.getLogger(__name__)


class IntentParser:
    """意图解析器"""

    def __init__(self):
        """初始化意图解析器"""
        self.time_keywords = {
            "今天": 0,
            "昨天": 1,
            "前天": 2,
            "本周": 7,
            "上周": 14,
            "这周": 7,
            "这月": 30,
            "本月": 30,
            "上月": 60,
            "最近": 7,
            "近期": 7,
        }

    def parse_query(self, query: str) -> Dict[str, Any]:
        """
        解析用户查询

        Args:
            query: 用户查询文本

        Returns:
            {
                "intent": str,  # 意图类型: summary, topic, keyword, stats, status
                "time_range": Optional[int],  # 时间范围（天数）
                "keywords": List[str],  # 关键词
                "channel_id": Optional[str],  # 频道ID
                "original_query": str,  # 原始查询
                "confidence": float  # 置信度
            }
        """
        query = query.strip()
        logger.info(f"解析查询: {query}")

        result = {
            "original_query": query,
            "intent": "summary",
            "time_range": None,
            "keywords": [],
            "channel_id": None,
            "confidence": 0.0
        }

        # 1. 检测状态查询意图
        if self._is_status_query(query):
            result["intent"] = "status"
            result["confidence"] = 0.9
            logger.info("识别为状态查询意图")
            return result

        # 2. 检测统计查询意图
        if self._is_stats_query(query):
            result["intent"] = "stats"
            result["confidence"] = 0.9
            logger.info("识别为统计查询意图")
            return result

        # 3. 提取时间范围
        time_range = self._extract_time_range(query)
        if time_range is not None:
            result["time_range"] = time_range
            logger.info(f"提取时间范围: {time_range}天")

        # 4. 提取关键词
        keywords = self._extract_keywords(query)
        if keywords:
            result["keywords"] = keywords
            result["intent"] = "keyword" if keywords else "summary"
            logger.info(f"提取关键词: {keywords}")

        # 5. 检测主题查询
        topics = self._extract_topics(query)
        if topics:
            result["keywords"].extend(topics)
            result["intent"] = "topic"
            logger.info(f"提取主题: {topics}")

        # 6. 计算置信度
        if time_range or keywords or topics:
            result["confidence"] = 0.8
        else:
            result["confidence"] = 0.5
            result["intent"] = "summary"
            result["time_range"] = 7  # 默认查询最近7天

        return result

    def _is_status_query(self, query: str) -> bool:
        """检查是否为状态查询"""
        status_keywords = [
            "配额", "剩余", "还能", "几次", "限额",
            "quota", "remaining", "limit"
        ]
        query_lower = query.lower()
        return any(kw in query_lower for kw in status_keywords)

    def _is_stats_query(self, query: str) -> bool:
        """检查是否为统计查询"""
        stats_keywords = [
            "统计", "总数", "多少条", "有多少", "数量",
            "排名", "排行", "top"
        ]
        query_lower = query.lower()
        return any(kw in query_lower for kw in stats_keywords)

    def _extract_time_range(self, query: str) -> Optional[int]:
        """提取时间范围（天数）"""
        # 检查关键词
        for keyword, days in self.time_keywords.items():
            if keyword in query:
                return days

        # 检查数字+天模式
        pattern = r'(\d+)\s*[天日]'
        match = re.search(pattern, query)
        if match:
            return int(match.group(1))

        # 检查"最近N天"模式
        pattern = r'最近\s*(\d+)\s*[天日]'
        match = re.search(pattern, query)
        if match:
            return int(match.group(1))

        return None

    def _extract_keywords(self, query: str) -> List[str]:
        """提取关键词"""
        keywords = []

        # 移除时间关键词
        filtered_query = query
        for keyword in self.time_keywords.keys():
            filtered_query = filtered_query.replace(keyword, "")

        # 常见技术关键词
        tech_keywords = [
            "AI", "GPT", "ChatGPT", "人工智能", "机器学习",
            "Python", "JavaScript", "Java", "编程", "代码",
            "API", "开发", "框架", "库", "工具",
            "区块链", "Web3", "加密货币", "NFT"
        ]

        for kw in tech_keywords:
            if kw.lower() in filtered_query.lower():
                keywords.append(kw)

        return keywords

    def _extract_topics(self, query: str) -> List[str]:
        """提取主题"""
        topics = []

        # 主题映射
        topic_patterns = {
            "技术": ["技术", "开发", "编程", "代码", "AI"],
            "新闻": ["新闻", "资讯", "发布", "公告"],
            "讨论": ["讨论", "看法", "观点", "评论"],
            "更新": ["更新", "升级", "新版本", "发布"],
            "问题": ["问题", "bug", "错误", "故障"]
        }

        query_lower = query.lower()
        for topic, patterns in topic_patterns.items():
            if any(p in query_lower for p in patterns):
                topics.append(topic)

        return topics

    def format_query_result(self, parsed: Dict[str, Any]) -> str:
        """
        格式化解析结果（用于调试）

        Args:
            parsed: 解析结果

        Returns:
            格式化的字符串
        """
        intent_map = {
            "summary": "总结查询",
            "keyword": "关键词查询",
            "topic": "主题查询",
            "stats": "统计查询",
            "status": "状态查询"
        }

        result = f"🔍 查询解析:\n"
        result += f"意图: {intent_map.get(parsed['intent'], parsed['intent'])}\n"

        if parsed.get("time_range"):
            result += f"时间范围: 最近{parsed['time_range']}天\n"

        if parsed.get("keywords"):
            result += f"关键词: {', '.join(parsed['keywords'])}\n"

        result += f"置信度: {parsed['confidence']:.0%}\n"

        return result


# 创建全局意图解析器实例
intent_parser = None

def get_intent_parser():
    """获取全局意图解析器实例"""
    global intent_parser
    if intent_parser is None:
        intent_parser = IntentParser()
    return intent_parser