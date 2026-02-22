"""测试 AI Client 模块

Copyright 2026 Sakura-Bot
本项目采用 AGPL-3.0 许可
"""

from unittest.mock import MagicMock, patch

import pytest
from openai import OpenAI

from core.ai_client import analyze_with_ai, client_llm, generate_poll_from_summary


@pytest.mark.unit
class TestAIClientInitialization:
    """AI 客户端初始化测试"""

    def test_client_exists(self):
        """测试客户端实例存在"""
        assert client_llm is not None
        assert isinstance(client_llm, OpenAI)

    def test_client_configuration(self, mock_env_vars):
        """测试客户端配置"""
        # 重新导入以获取配置的客户端
        import importlib

        core_ai_client = importlib.import_module("core.ai_client")
        assert core_ai_client.client_llm is not None


@pytest.mark.unit
class TestAnalyzeWithAI:
    """AI 分析功能测试"""

    @pytest.fixture
    def mock_chat_completion(self):
        """Mock chat completion 响应"""
        response = MagicMock()
        response.choices = [MagicMock()]
        response.choices[0].message.content = "这是 AI 生成的测试总结"
        response.choices[0].index = 0
        response.choices[0].finish_reason = "stop"
        return response

    def test_analyze_empty_messages(self):
        """测试分析空消息列表"""
        result = analyze_with_ai([], "测试提示词")
        assert result == "本周无新动态。"

    def test_analyze_with_messages(self, mock_chat_completion):
        """测试分析有效消息"""
        with patch.object(client_llm.chat.completions, "create", return_value=mock_chat_completion):
            messages = ["消息1", "消息2", "消息3"]
            prompt = "请总结以下消息："
            result = analyze_with_ai(messages, prompt)

            assert result == "这是 AI 生成的测试总结"
            # 验证 API 被调用
            client_llm.chat.completions.create.assert_called_once()

    def test_analyze_with_custom_model(self, mock_chat_completion):
        """测试使用自定义模型"""
        with patch.object(client_llm.chat.completions, "create", return_value=mock_chat_completion):
            messages = ["测试消息"]
            prompt = "总结："

            with patch("core.ai_client.get_llm_model", return_value="custom-model"):
                result = analyze_with_ai(messages, prompt)

                assert result == "这是 AI 生成的测试总结"
                # 验证使用了自定义模型
                call_args = client_llm.chat.completions.create.call_args
                assert call_args[1]["model"] == "custom-model"

    def test_analyze_handles_long_messages(self, mock_chat_completion):
        """测试处理长消息"""
        long_message = "这是一条很长的消息。" * 100
        messages = [long_message] * 10

        with patch.object(client_llm.chat.completions, "create", return_value=mock_chat_completion):
            result = analyze_with_ai(messages, "总结：")
            assert result == "这是 AI 生成的测试总结"

    def test_analyze_with_api_error(self):
        """测试 API 错误处理"""
        from core.error_handler import RetryExhaustedError

        with patch.object(
            client_llm.chat.completions, "create", side_effect=ConnectionError("API 连接失败")
        ):
            messages = ["测试消息"]
            # 现在重试机制会抛出 RetryExhaustedError
            with pytest.raises(RetryExhaustedError):
                analyze_with_ai(messages, "总结：")

    def test_analyze_with_timeout(self):
        """测试超时处理"""
        from core.error_handler import RetryExhaustedError

        with patch.object(
            client_llm.chat.completions, "create", side_effect=TimeoutError("请求超时")
        ):
            messages = ["测试消息"]
            # 现在重试机制会抛出 RetryExhaustedError
            with pytest.raises(RetryExhaustedError):
                analyze_with_ai(messages, "总结：")

    def test_analyze_system_message_structure(self, mock_chat_completion):
        """测试系统消息结构"""
        with patch.object(client_llm.chat.completions, "create", return_value=mock_chat_completion):
            messages = ["测试消息"]
            analyze_with_ai(messages, "总结：")

            # 验证消息结构
            call_args = client_llm.chat.completions.create.call_args
            messages_sent = call_args[1]["messages"]

            assert len(messages_sent) == 2
            assert messages_sent[0]["role"] == "system"
            assert "资讯摘要助手" in messages_sent[0]["content"]
            assert messages_sent[1]["role"] == "user"


@pytest.mark.unit
class TestGeneratePollFromSummary:
    """投票生成功能测试"""

    @pytest.fixture
    def mock_poll_response(self):
        """Mock 投票生成响应"""
        response = MagicMock()
        response.choices = [MagicMock()]
        response.choices[0].message.content = """```json
{
  "question": "测试投票问题 / Test Poll Question",
  "options": [
    "选项1 / Option 1",
    "选项2 / Option 2",
    "选项3 / Option 3",
    "选项4 / Option 4"
  ]
}
```"""
        return response

    def test_generate_poll_with_valid_summary(self, mock_poll_response):
        """测试从有效总结生成投票"""
        with patch.object(client_llm.chat.completions, "create", return_value=mock_poll_response):
            summary = "这是一条测试总结，包含了频道的核心要点。"
            result = generate_poll_from_summary(summary)

            assert "question" in result
            assert "options" in result
            assert isinstance(result["options"], list)
            assert len(result["options"]) == 4

    def test_generate_poll_with_short_summary(self):
        """测试从短总结生成投票（使用默认）"""
        short_summary = "短"
        result = generate_poll_from_summary(short_summary)

        assert "question" in result
        assert "options" in result
        assert isinstance(result["options"], list)

    def test_generate_poll_with_empty_summary(self):
        """测试从空总结生成投票"""
        result = generate_poll_from_summary("")

        assert "question" in result
        assert "options" in result

    def test_generate_poll_with_whitespace_only(self):
        """测试从仅包含空格的总结生成投票"""
        result = generate_poll_from_summary("   ")

        assert "question" in result
        assert "options" in result

    def test_generate_poll_with_malformed_json(self):
        """测试处理格式错误的 JSON 响应"""
        bad_response = MagicMock()
        bad_response.choices = [MagicMock()]
        bad_response.choices[0].message.content = "这不是 JSON 格式的响应"

        with patch.object(client_llm.chat.completions, "create", return_value=bad_response):
            summary = "测试总结"
            result = generate_poll_from_summary(summary)

            # 应该返回默认投票
            assert "question" in result
            assert "options" in result

    def test_generate_poll_with_incomplete_json(self):
        """测试处理不完整的 JSON 响应"""
        incomplete_response = MagicMock()
        incomplete_response.choices = [MagicMock()]
        incomplete_response.choices[0].message.content = '{"question": "问题"}'

        with patch.object(client_llm.chat.completions, "create", return_value=incomplete_response):
            summary = "测试总结"
            result = generate_poll_from_summary(summary)

            # 应该返回默认投票
            assert "question" in result
            assert "options" in result

    def test_generate_poll_with_too_long_question(self):
        """测试处理超长问题（>255字符）"""
        long_question_response = MagicMock()
        long_question_response.choices = [MagicMock()]
        long_question_response.choices[
            0
        ].message.content = (
            f'''{{"question": "{"测试问题" * 100}", "options": ["选项1", "选项2"]}}'''
        )

        with patch.object(
            client_llm.chat.completions, "create", return_value=long_question_response
        ):
            summary = "测试总结"
            result = generate_poll_from_summary(summary)

            assert "question" in result
            assert len(result["question"]) <= 255

    def test_generate_poll_with_too_long_options(self):
        """测试处理超长选项（>100字符）"""
        long_options_response = MagicMock()
        long_options_response.choices = [MagicMock()]
        long_options_response.choices[
            0
        ].message.content = f'''{{"question": "测试问题", "options": ["{"长选项" * 50}", "选项2", "选项3", "选项4"]}}'''

        with patch.object(
            client_llm.chat.completions, "create", return_value=long_options_response
        ):
            summary = "测试总结"
            result = generate_poll_from_summary(summary)

            assert "options" in result
            # 所有选项都应该被截断
            for option in result["options"]:
                assert len(option) <= 103  # 100 + "..."

    def test_generate_poll_with_less_than_two_options(self):
        """测试处理选项少于2个的情况"""
        single_option_response = MagicMock()
        single_option_response.choices = [MagicMock()]
        single_option_response.choices[
            0
        ].message.content = '{"question": "问题", "options": ["选项1"]}'

        with patch.object(
            client_llm.chat.completions, "create", return_value=single_option_response
        ):
            summary = "测试总结"
            result = generate_poll_from_summary(summary)

            # 应该返回默认投票
            assert "question" in result
            assert "options" in result
            assert len(result["options"]) >= 2

    def test_generate_poll_system_message(self, mock_poll_response):
        """测试系统消息内容"""
        with patch.object(client_llm.chat.completions, "create", return_value=mock_poll_response):
            # 提供足够长的 summary 以通过长度检查
            summary = (
                "这是一条足够长的测试总结，包含了频道的核心要点，确保不会因为长度过短而被拦截。"
            )
            generate_poll_from_summary(summary)

            # 验证系统消息
            call_args = client_llm.chat.completions.create.call_args
            messages_sent = call_args[1]["messages"]

            assert messages_sent[0]["role"] == "system"
            assert (
                "互动策划专家" in messages_sent[0]["content"]
                or "humorous" in messages_sent[0]["content"]
            )

    def test_generate_poll_with_api_error(self):
        """测试 API 错误处理"""
        with patch.object(client_llm.chat.completions, "create", side_effect=Exception("API 错误")):
            summary = "测试总结"
            result = generate_poll_from_summary(summary)

            # 应该返回默认投票
            assert "question" in result
            assert "options" in result


@pytest.mark.unit
class TestAIClientErrorHandling:
    """AI 客户端错误处理测试"""

    def test_analyze_retry_mechanism(self):
        """测试重试机制"""
        # 前两次失败，第三次成功
        mock_response = MagicMock()
        mock_response.choices = [MagicMock()]
        mock_response.choices[0].message.content = "成功"

        with patch.object(
            client_llm.chat.completions,
            "create",
            side_effect=[ConnectionError("失败1"), ConnectionError("失败2"), mock_response],
        ):
            messages = ["测试消息"]
            result = analyze_with_ai(messages, "总结：")

            # 应该在重试后成功
            assert result == "成功"

    def test_analyze_max_retries_exceeded(self):
        """测试超过最大重试次数"""
        from core.error_handler import RetryExhaustedError

        with patch.object(
            client_llm.chat.completions, "create", side_effect=ConnectionError("持续失败")
        ):
            messages = ["测试消息"]
            # 现在重试机制会抛出 RetryExhaustedError
            with pytest.raises(RetryExhaustedError):
                analyze_with_ai(messages, "总结：")


@pytest.mark.integration
class TestAIClientIntegration:
    """AI 客户端集成测试"""

    def test_full_analyze_workflow(self, mock_chat_completion):
        """测试完整的分析工作流"""
        with patch.object(client_llm.chat.completions, "create", return_value=mock_chat_completion):
            messages = [
                "2026-01-01 10:00 - 用户A: 项目启动",
                "2026-01-01 11:00 - 用户B: 完成开发",
                "2026-01-01 12:00 - 用户C: 提交测试",
            ]
            prompt = "请总结以上消息："

            result = analyze_with_ai(messages, prompt)

            assert result == "这是 AI 生成的测试总结"
            assert client_llm.chat.completions.create.call_count == 1

    def test_full_poll_generation_workflow(self, mock_poll_response):
        """测试完整的投票生成工作流"""
        with patch.object(client_llm.chat.completions, "create", return_value=mock_poll_response):
            summary = """
# 频道总结
本周完成了以下工作：
1. 完成了测试框架的搭建
2. 添加了单元测试和集成测试
3. 改进了 CI/CD 流程
"""

            result = generate_poll_from_summary(summary)

            assert "question" in result
            assert "options" in result
            assert isinstance(result["options"], list)
            assert len(result["options"]) >= 2

    def test_error_recovery_workflow(self):
        """测试错误恢复工作流"""
        from core.error_handler import RetryExhaustedError

        # 第一次调用失败，现在会抛出异常
        with patch.object(
            client_llm.chat.completions, "create", side_effect=ConnectionError("连接失败")
        ):
            with pytest.raises(RetryExhaustedError):
                analyze_with_ai(["消息"], "提示")

        # 第二次调用成功
        mock_response = MagicMock()
        mock_response.choices = [MagicMock()]
        mock_response.choices[0].message.content = "成功"

        with patch.object(client_llm.chat.completions, "create", return_value=mock_response):
            result = analyze_with_ai(["消息"], "提示")
            assert result == "成功"


@pytest.mark.slow
class TestAIClientPerformance:
    """AI 客户端性能测试"""

    def test_large_message_batch(self, mock_chat_completion):
        """测试大批量消息处理"""
        # 生成100条消息
        messages = [f"消息 {i}: 内容" for i in range(100)]

        with patch.object(client_llm.chat.completions, "create", return_value=mock_chat_completion):
            import time

            start_time = time.time()
            result = analyze_with_ai(messages, "总结：")
            elapsed_time = time.time() - start_time

            assert result == "这是 AI 生成的测试总结"
            # 应该在合理时间内完成（模拟测试应该很快）
            assert elapsed_time < 5.0

    def test_concurrent_analyze_calls(self, mock_chat_completion):
        """测试并发分析调用"""
        import asyncio

        async def async_analyze():
            # 在异步上下文中调用同步函数
            return analyze_with_ai(["测试消息"], "总结：")

        with patch.object(client_llm.chat.completions, "create", return_value=mock_chat_completion):
            # 运行多个并发调用
            results = asyncio.run(asyncio.gather(*[async_analyze() for _ in range(5)]))

            assert all(r == "这是 AI 生成的测试总结" for r in results)


if __name__ == "__main__":
    pytest.main([__file__, "-v"])
