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
自然语言意图解析器
理解用户的查询意图
"""

import logging
import re
from typing import Any

logger = logging.getLogger(__name__)

# 停用词列表（不作为关键词提取）
STOPWORDS = {
    # 疑问/连接词
    "什么",
    "什么样",
    "怎么",
    "怎样",
    "如何",
    "为什么",
    "为何",
    "哪里",
    "哪个",
    "哪些",
    "是否",
    "有没有",
    "有无",
    "能否",
    "可以",
    "是不是",
    # 动词
    "发生",
    "发布",
    "更新",
    "讨论",
    "介绍",
    "讲",
    "说",
    "看",
    "查",
    "找",
    "帮",
    "告诉",
    "知道",
    "了解",
    "查看",
    "分析",
    "总结",
    "回答",
    "回复",
    # 代词
    "我",
    "你",
    "他",
    "她",
    "它",
    "我们",
    "你们",
    "他们",
    "这",
    "那",
    "这个",
    "那个",
    "这些",
    "那些",
    "这里",
    "那里",
    # 副词/介词/助词
    "最近",
    "近期",
    "今天",
    "昨天",
    "前天",
    "本周",
    "上周",
    "这周",
    "本月",
    "上月",
    "这月",
    "最新",
    "新",
    "旧",
    "大",
    "小",
    "多",
    "少",
    "很",
    "非常",
    "比较",
    "一些",
    "一下",
    "关于",
    "有关",
    "的",
    "了",
    "吗",
    "呢",
    "啊",
    "吧",
    "嗯",
    "和",
    "与",
    "或",
    "但",
    "而",
    "所以",
    "因此",
    "因为",
    # 频道相关通用词
    "频道",
    "内容",
    "信息",
    "消息",
    "新闻",
    "资讯",
    "动态",
    "记录",
    "历史",
    "过去",
    "情况",
    "进展",
}


class IntentParser:
    """意图解析器"""

    def __init__(self):
        """初始化意图解析器"""
        # 时间关键词映射：关键词 -> (天数偏移, 是否精确当天)
        # 天数表示往前多少天（0 = 今天）
        self.time_keywords = {
            "今天": 0,
            "今日": 0,
            "当天": 0,
            "昨天": 1,
            "昨日": 1,
            "前天": 2,
            "大前天": 3,
            "本周": 7,
            "这周": 7,
            "本星期": 7,
            "上周": 14,
            "上星期": 14,
            "这个星期": 7,
            "这两天": 2,
            "这几天": 3,
            "这段时间": 7,
            "这月": 30,
            "本月": 30,
            "这个月": 30,
            "上月": 60,
            "上个月": 60,
            "最近": 7,
            "近期": 7,
            "近来": 7,
            "最新": 3,
            "新近": 3,
            "今年": 365,
            "今年以来": 365,
        }

    def parse_query(self, query: str) -> dict[str, Any]:
        """
        解析用户查询

        Args:
            query: 用户查询文本

        Returns:
            {
                "intent": str,         # 意图类型: summary, topic, keyword, stats, status
                "time_range": Optional[int],  # 时间范围（天数），None 表示不限制
                "keywords": List[str], # 关键词
                "channel_id": Optional[str],  # 频道ID
                "original_query": str, # 原始查询
                "confidence": float    # 置信度
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
            "confidence": 0.0,
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

        # 4. 提取关键词（智能提取，不依赖硬编码词典）
        keywords = self._extract_keywords(query)
        if keywords:
            result["keywords"] = keywords
            result["intent"] = "keyword"
            logger.info(f"提取关键词: {keywords}")

        # 5. 检测主题查询（主题会追加到关键词中）
        topics = self._extract_topics(query)
        if topics:
            # 只添加不在已有关键词中的主题词
            for t in topics:
                if t not in result["keywords"]:
                    result["keywords"].append(t)
            if result["intent"] == "summary":
                result["intent"] = "topic"
            logger.info(f"提取主题: {topics}")

        # 6. 计算置信度
        if time_range is not None or result["keywords"]:
            result["confidence"] = 0.8
        else:
            result["confidence"] = 0.5
            result["intent"] = "summary"
            # 没有时间约束时不默认限制7天，让向量检索决定
            # result["time_range"] 保持 None

        return result

    def _is_status_query(self, query: str) -> bool:
        """检查是否为状态查询"""
        status_keywords = [
            "配额",
            "剩余",
            "还能",
            "几次",
            "限额",
            "quota",
            "remaining",
            "limit",
            "次数",
        ]
        query_lower = query.lower()
        return any(kw in query_lower for kw in status_keywords)

    def _is_stats_query(self, query: str) -> bool:
        """检查是否为统计查询"""
        stats_keywords = [
            "统计",
            "总数",
            "多少条",
            "有多少条",
            "数量",
            "排名",
            "排行",
            "top",
            "总共有多少",
        ]
        query_lower = query.lower()
        return any(kw in query_lower for kw in stats_keywords)

    def _extract_time_range(self, query: str) -> int | None:
        """
        提取时间范围（天数）

        支持的模式：
        - 今天/昨天/前天 等关键词
        - 最近N天/N日/N周/N个月 等数量表达
        - 过去N天 等表达
        """
        # 1. 精确数字+单位模式（最高优先级）
        # "最近3天" / "过去7天" / "最近3日"
        match = re.search(r"(?:最近|过去|近|前)\s*(\d+)\s*[天日]", query)
        if match:
            return int(match.group(1))

        # "最近N周"
        match = re.search(r"(?:最近|过去|近|前)\s*(\d+)\s*周", query)
        if match:
            return int(match.group(1)) * 7

        # "最近N个月" / "最近N月"
        match = re.search(r"(?:最近|过去|近|前)\s*(\d+)\s*(?:个月|月)", query)
        if match:
            return int(match.group(1)) * 30

        # 裸数字+天（如"7天"、"30日"）
        match = re.search(r"(\d+)\s*[天日]", query)
        if match:
            days = int(match.group(1))
            if 1 <= days <= 365:  # 合理范围内
                return days

        # 2. 关键词匹配（按关键词长度降序，优先匹配长词）
        sorted_keywords = sorted(self.time_keywords.keys(), key=len, reverse=True)
        for keyword in sorted_keywords:
            if keyword in query:
                return self.time_keywords[keyword]

        return None

    def _extract_keywords(self, query: str) -> list[str]:
        """
        从查询中提取关键词

        策略：
        1. 移除时间词、停用词等无意义词汇
        2. 提取长度 >= 2 的中文词段和英文词
        3. 返回最多 5 个关键词
        """
        # 移除时间关键词（避免时间词成为关键词）
        filtered = query
        for keyword in self.time_keywords.keys():
            filtered = filtered.replace(keyword, " ")

        # 移除疑问句尾（"吗"、"呢"、"？"等）
        filtered = re.sub(r"[？?！!。，,、；;：:]", " ", filtered)

        keywords = []

        # 提取英文词（大小写不敏感，长度>=2）
        eng_words = re.findall(r"[A-Za-z][A-Za-z0-9\-_.]{1,}", filtered)
        for w in eng_words:
            if w.lower() not in STOPWORDS and w not in keywords:
                keywords.append(w)

        # 提取中文词段：使用正则分割后过滤停用词
        # 按空格/标点分割，保留长度>=2的中文词组
        segments = re.split(r"[\s\u0000-\u00ff]+", filtered)
        for seg in segments:
            # 只处理中文字符组成的词段（含中文的片段）
            if len(seg) >= 2 and re.search(r"[\u4e00-\u9fff]", seg):
                # 尝试提取子词段（2-8字符的连续中文片段）
                cn_words = re.findall(r"[\u4e00-\u9fff]{2,8}", seg)
                for w in cn_words:
                    if w not in STOPWORDS and w not in keywords:
                        keywords.append(w)

        # 去重并限制数量
        seen = set()
        unique_kw = []
        for kw in keywords:
            lower = kw.lower()
            if lower not in seen:
                seen.add(lower)
                unique_kw.append(kw)

        return unique_kw[:6]

    def _extract_topics(self, query: str) -> list[str]:
        """提取主题（基于语义模式）"""
        topics = []

        topic_patterns = {
            "技术": [
                r"技术",
                r"开发",
                r"编程",
                r"代码",
                r"工程",
                r"算法",
                r"框架",
                r"接口",
                r"API",
            ],
            "新闻": [r"新闻", r"资讯", r"消息", r"公告", r"通知", r"发布", r"声明"],
            "讨论": [r"讨论", r"看法", r"观点", r"评论", r"意见", r"分析", r"评价"],
            "更新": [r"更新", r"升级", r"新版本", r"版本", r"改进", r"迭代", r"发布"],
            "问题": [r"问题", r"bug", r"错误", r"故障", r"异常", r"报错", r"失败"],
            "市场": [r"市场", r"价格", r"行情", r"涨", r"跌", r"交易", r"投资"],
            "AI": [r"AI", r"人工智能", r"模型", r"大模型", r"GPT", r"LLM", r"机器学习"],
        }

        query_lower = query.lower()
        for topic, patterns in topic_patterns.items():
            if any(re.search(p, query_lower, re.IGNORECASE) for p in patterns):
                topics.append(topic)

        return topics

    def format_query_result(self, parsed: dict[str, Any]) -> str:
        """
        格式化解析结果（用于调试）
        """
        intent_map = {
            "summary": "总结查询",
            "keyword": "关键词查询",
            "topic": "主题查询",
            "stats": "统计查询",
            "status": "状态查询",
        }

        result = "🔍 查询解析:\n"
        result += f"意图: {intent_map.get(parsed['intent'], parsed['intent'])}\n"

        if parsed.get("time_range") is not None:
            result += f"时间范围: 最近{parsed['time_range']}天\n"
        else:
            result += "时间范围: 不限\n"

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
