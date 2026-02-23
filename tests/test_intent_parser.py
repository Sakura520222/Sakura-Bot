"""测试 Intent Parser 模块

Copyright 2026 Sakura-Bot
本项目采用 AGPL-3.0 许可
"""

import pytest

from core.intent_parser import IntentParser, get_intent_parser


@pytest.mark.unit
class TestIntentParserInit:
    """初始化测试"""

    def test_init(self):
        """测试初始化"""
        parser = IntentParser()

        assert parser.time_keywords is not None
        assert "今天" in parser.time_keywords


@pytest.mark.unit
class TestParseQuery:
    """解析查询测试"""

    def test_parse_simple_summary_query(self):
        """测试解析简单总结查询"""
        parser = IntentParser()
        result = parser.parse_query("今天有什么新闻")

        assert result["intent"] in ["summary", "keyword"]
        assert result["time_range"] == 0

    def test_parse_keyword_query(self):
        """测试解析关键词查询"""
        parser = IntentParser()
        result = parser.parse_query("AI相关的内容")

        assert result["intent"] == "keyword"
        assert "AI" in result["keywords"] or "ai" in [k.lower() for k in result["keywords"]]

    def test_parse_status_query(self):
        """测试解析状态查询"""
        parser = IntentParser()
        result = parser.parse_query("还剩多少配额")

        assert result["intent"] == "status"
        assert result["confidence"] >= 0.9

    def test_parse_stats_query(self):
        """测试解析统计查询"""
        parser = IntentParser()
        result = parser.parse_query("总共有多少条总结")

        assert result["intent"] == "stats"
        assert result["confidence"] >= 0.9

    def test_parse_with_time_range_days(self):
        """测试解析带天数的查询"""
        parser = IntentParser()
        result = parser.parse_query("最近7天的内容")

        assert result["time_range"] == 7

    def test_parse_with_time_range_weeks(self):
        """测试解析带周数的查询"""
        parser = IntentParser()
        result = parser.parse_query("最近2周的消息")

        assert result["time_range"] == 14

    def test_parse_with_time_range_months(self):
        """测试解析带月数的查询"""
        parser = IntentParser()
        result = parser.parse_query("最近3个月")

        assert result["time_range"] == 90


@pytest.mark.unit
class TestExtractTimeRange:
    """提取时间范围测试"""

    def test_extract_today(self):
        """测试提取今天"""
        parser = IntentParser()
        result = parser._extract_time_range("今天的内容")

        assert result == 0

    def test_extract_yesterday(self):
        """测试提取昨天"""
        parser = IntentParser()
        result = parser._extract_time_range("昨天的消息")

        assert result == 1

    def test_extract_days_pattern(self):
        """测试提取天数模式"""
        parser = IntentParser()
        result = parser._extract_time_range("最近5天")

        assert result == 5

    def test_extract_weeks_pattern(self):
        """测试提取周数模式"""
        parser = IntentParser()
        result = parser._extract_time_range("过去2周")

        assert result == 14

    def test_extract_months_pattern(self):
        """测试提取月数模式"""
        parser = IntentParser()
        result = parser._extract_time_range("近1个月")

        assert result == 30

    def test_extract_no_time(self):
        """测试没有时间信息"""
        parser = IntentParser()
        result = parser._extract_time_range("AI技术")

        assert result is None


@pytest.mark.unit
class TestExtractKeywords:
    """提取关键词测试"""

    def test_extract_chinese_keywords(self):
        """测试提取中文关键词"""
        parser = IntentParser()
        keywords = parser._extract_keywords("人工智能和机器学习")

        assert len(keywords) > 0
        assert any("人工智能" in kw or "机器学习" in kw for kw in keywords)

    def test_extract_english_keywords(self):
        """测试提取英文关键词"""
        parser = IntentParser()
        keywords = parser._extract_keywords("Python and Django")

        assert "Python" in keywords
        assert "Django" in keywords

    def test_extract_mixed_keywords(self):
        """测试提取混合关键词"""
        parser = IntentParser()
        keywords = parser._extract_keywords("学习Python编程语言")

        assert len(keywords) > 0

    def test_remove_stopwords(self):
        """测试移除停用词"""
        parser = IntentParser()
        keywords = parser._extract_keywords("怎么查看最新的内容")

        # "怎么"、"查看"、"最新的"等停用词应该被过滤
        assert "怎么" not in keywords

    def test_limit_keywords_count(self):
        """测试限制关键词数量"""
        parser = IntentParser()
        # 长文本，应该提取最多6个关键词
        keywords = parser._extract_keywords(
            "Python Django Flask TensorFlow PyTorch Kubernetes Docker Redis MySQL PostgreSQL MongoDB"
        )

        assert len(keywords) <= 6


@pytest.mark.unit
class TestExtractTopics:
    """提取主题测试"""

    def test_extract_tech_topic(self):
        """测试提取技术主题"""
        parser = IntentParser()
        topics = parser._extract_topics("关于API接口开发")

        assert "技术" in topics

    def test_extract_news_topic(self):
        """测试提取新闻主题"""
        parser = IntentParser()
        topics = parser._extract_topics("最新的公告和通知")

        assert "新闻" in topics

    def test_extract_ai_topic(self):
        """测试提取AI主题"""
        parser = IntentParser()
        topics = parser._extract_topics("大模型和GPT技术")

        assert "AI" in topics

    def test_extract_multiple_topics(self):
        """测试提取多个主题"""
        parser = IntentParser()
        topics = parser._extract_topics("AI技术的新版本发布")

        assert len(topics) >= 2


@pytest.mark.unit
class TestFormatQueryResult:
    """格式化查询结果测试"""

    def test_format_result(self):
        """测试格式化结果"""
        parser = IntentParser()
        parsed = {
            "intent": "keyword",
            "time_range": 7,
            "keywords": ["AI", "Python"],
            "channel_id": None,
            "original_query": "AI相关",
            "confidence": 0.8,
        }

        result = parser.format_query_result(parsed)

        assert "关键词" in result
        assert "7天" in result
        assert "80%" in result


@pytest.mark.unit
class TestGetIntentParser:
    """获取全局实例测试"""

    def test_singleton(self):
        """测试单例模式"""
        import core.intent_parser

        core.intent_parser.intent_parser = None

        parser1 = get_intent_parser()
        parser2 = get_intent_parser()

        assert parser1 is parser2

    def test_returns_intent_parser_instance(self):
        """测试返回IntentParser实例"""
        import core.intent_parser

        core.intent_parser.intent_parser = None

        parser = get_intent_parser()

        assert isinstance(parser, IntentParser)


if __name__ == "__main__":
    pytest.main([__file__, "-v"])
