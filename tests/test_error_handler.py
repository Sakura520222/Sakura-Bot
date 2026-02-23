"""测试 Error Handler 模块

Copyright 2026 Sakura-Bot
本项目采用 AGPL-3.0 许可
"""

import asyncio
import time

import pytest

from core.error_handler import (
    RetryExhaustedError,
    check_database_connection,
    get_error_stats,
    get_health_checker,
    graceful_shutdown,
    initialize_error_handling,
    record_error,
    reset_error_stats,
    retry_with_backoff,
    setup_graceful_shutdown,
)


@pytest.mark.unit
class TestErrorStats:
    """错误统计测试"""

    def test_record_error(self):
        """测试记录错误"""
        reset_error_stats()
        error = ValueError("测试错误")
        record_error(error, "测试上下文")

        stats = get_error_stats()
        assert stats["total_errors"] == 1
        assert stats["error_types"]["ValueError"] == 1
        assert stats["last_error_time"] is not None
        assert len(stats["recent_errors"]) == 1

    def test_record_multiple_errors(self):
        """测试记录多个错误"""
        reset_error_stats()

        error1 = ValueError("错误1")
        error2 = TypeError("错误2")
        error3 = ValueError("错误3")

        record_error(error1, "上下文1")
        record_error(error2, "上下文2")
        record_error(error3, "上下文3")

        stats = get_error_stats()
        assert stats["total_errors"] == 3
        assert stats["error_types"]["ValueError"] == 2
        assert stats["error_types"]["TypeError"] == 1
        assert len(stats["recent_errors"]) == 3

    def test_recent_errors_limit(self):
        """测试最近错误记录限制（最多10个）"""
        reset_error_stats()

        for i in range(15):
            error = ValueError(f"错误{i}")
            record_error(error, f"上下文{i}")

        stats = get_error_stats()
        assert stats["total_errors"] == 15
        assert len(stats["recent_errors"]) == 10  # 最多保留10个

    def test_reset_error_stats(self):
        """测试重置错误统计"""
        reset_error_stats()
        error = ValueError("测试错误")
        record_error(error, "测试")

        assert get_error_stats()["total_errors"] == 1

        reset_error_stats()
        stats = get_error_stats()
        assert stats["total_errors"] == 0
        assert len(stats["recent_errors"]) == 0
        assert stats["error_types"] == {}
        assert stats["last_error_time"] is None


@pytest.mark.unit
class TestRetryWithBackoff:
    """重试装饰器测试"""

    def test_sync_function_success_on_first_try(self):
        """测试同步函数首次成功"""
        call_count = 0

        @retry_with_backoff(max_retries=3)
        def test_func():
            nonlocal call_count
            call_count += 1
            return "成功"

        result = test_func()
        assert result == "成功"
        assert call_count == 1

    def test_sync_function_retry_then_success(self):
        """测试同步函数重试后成功"""
        call_count = 0

        @retry_with_backoff(max_retries=3, base_delay=0.1)
        def test_func():
            nonlocal call_count
            call_count += 1
            if call_count < 3:
                raise ConnectionError("连接失败")
            return "成功"

        result = test_func()
        assert result == "成功"
        assert call_count == 3

    def test_sync_function_max_retries_exceeded(self):
        """测试同步函数超过最大重试次数"""

        @retry_with_backoff(max_retries=2, base_delay=0.1)
        def test_func():
            raise ConnectionError("持续失败")

        with pytest.raises(RetryExhaustedError) as exc_info:
            test_func()

        assert isinstance(exc_info.value.last_exception, ConnectionError)

    def test_sync_function_skip_retry_on_exception(self):
        """测试同步函数跳过特定异常的重试"""
        call_count = 0

        @retry_with_backoff(max_retries=3, skip_retry_on_exceptions=(ValueError,), base_delay=0.1)
        def test_func():
            nonlocal call_count
            call_count += 1
            raise ValueError("不应重试的错误")

        with pytest.raises(ValueError):
            test_func()

        assert call_count == 1  # 不应该重试

    def test_sync_function_custom_retry_exceptions(self):
        """测试同步函数只重试特定异常"""
        call_count = 0

        @retry_with_backoff(max_retries=3, retry_on_exceptions=(ConnectionError,), base_delay=0.1)
        def test_func():
            nonlocal call_count
            call_count += 1
            if call_count < 2:
                raise ConnectionError("连接失败")
            raise ValueError("值错误")

        with pytest.raises(ValueError):  # ValueError不重试
            test_func()

        assert call_count == 2  # ConnectionError重试了一次

    def test_exponential_backoff(self):
        """测试指数退避"""
        call_times = []

        @retry_with_backoff(max_retries=3, base_delay=0.1, exponential_backoff=True)
        def test_func():
            call_times.append(time.time())
            if len(call_times) < 2:
                raise ConnectionError("失败")
            return "成功"

        test_func()

        # 验证延迟时间呈指数增长
        if len(call_times) >= 2:
            delay = call_times[1] - call_times[0]
            assert delay >= 0.1  # 至少是基础延迟

    def test_linear_backoff(self):
        """测试线性退避"""
        call_times = []

        @retry_with_backoff(max_retries=2, base_delay=0.1, exponential_backoff=False)
        def test_func():
            call_times.append(time.time())
            if len(call_times) < 2:
                raise ConnectionError("失败")
            return "成功"

        test_func()

        # 验证延迟时间是固定的
        if len(call_times) >= 2:
            delay = call_times[1] - call_times[0]
            assert 0.1 <= delay <= 0.2

    @pytest.mark.asyncio
    async def test_async_function_success_on_first_try(self):
        """测试异步函数首次成功"""
        call_count = 0

        @retry_with_backoff(max_retries=3)
        async def test_func():
            nonlocal call_count
            call_count += 1
            return "成功"

        result = await test_func()
        assert result == "成功"
        assert call_count == 1

    @pytest.mark.asyncio
    async def test_async_function_retry_then_success(self):
        """测试异步函数重试后成功"""
        call_count = 0

        @retry_with_backoff(max_retries=3, base_delay=0.1)
        async def test_func():
            nonlocal call_count
            call_count += 1
            if call_count < 2:
                raise ConnectionError("连接失败")
            return "成功"

        result = await test_func()
        assert result == "成功"
        assert call_count == 2

    @pytest.mark.asyncio
    async def test_async_function_max_retries_exceeded(self):
        """测试异步函数超过最大重试次数"""

        @retry_with_backoff(max_retries=2, base_delay=0.1)
        async def test_func():
            raise ConnectionError("持续失败")

        with pytest.raises(RetryExhaustedError):
            await test_func()

    @pytest.mark.asyncio
    async def test_async_function_skip_retry(self):
        """测试异步函数跳过特定异常的重试"""
        call_count = 0

        @retry_with_backoff(max_retries=3, skip_retry_on_exceptions=(ValueError,), base_delay=0.1)
        async def test_func():
            nonlocal call_count
            call_count += 1
            raise ValueError("不应重试的错误")

        with pytest.raises(ValueError):
            await test_func()

        assert call_count == 1


@pytest.mark.unit
class TestHealthChecker:
    """健康检查器测试"""

    def test_register_check(self):
        """测试注册健康检查"""
        checker = get_health_checker()

        def simple_check():
            return "OK"

        checker.register_check("test_check", simple_check, interval_seconds=60)

        status = checker.get_status()
        assert "test_check" in status
        assert status["test_check"]["interval"] == 60

    def test_register_async_check(self):
        """测试注册异步健康检查"""
        checker = get_health_checker()

        async def async_check():
            return "OK"

        checker.register_check("async_check", async_check, interval_seconds=60)

        status = checker.get_status()
        assert "async_check" in status

    @pytest.mark.asyncio
    async def test_run_check_success(self):
        """测试运行成功的健康检查"""
        checker = get_health_checker()

        def success_check():
            return "检查成功"

        checker.register_check("success", success_check, interval_seconds=0)

        success, result = await checker.run_check("success")
        assert success is True
        assert result == "检查成功"

    @pytest.mark.asyncio
    async def test_run_check_failure(self):
        """测试运行失败的健康检查"""
        checker = get_health_checker()

        def failing_check():
            raise Exception("检查失败")

        checker.register_check("failing", failing_check, interval_seconds=0)

        success, result = await checker.run_check("failing")
        assert success is False
        assert "检查失败" in result

    @pytest.mark.asyncio
    async def test_run_check_not_found(self):
        """测试运行不存在的健康检查"""
        checker = get_health_checker()

        success, result = await checker.run_check("nonexistent")
        assert success is False
        assert "未找到健康检查" in result

    @pytest.mark.asyncio
    async def test_run_check_interval_skip(self):
        """测试间隔时间未到时跳过检查"""
        checker = get_health_checker()

        def slow_check():
            return "结果"

        checker.register_check("slow", slow_check, interval_seconds=10)

        # 第一次运行
        success1, result1 = await checker.run_check("slow")
        assert success1 is True

        # 立即再次运行，应该跳过
        success2, result2 = await checker.run_check("slow")
        assert success2 is True  # 返回上次结果
        assert result2 == "结果"

    @pytest.mark.asyncio
    async def test_run_all_checks(self):
        """测试运行所有健康检查"""
        # 使用唯一的检查名称避免与其他测试冲突
        checker = get_health_checker()

        def check1():
            return "检查1"

        def check2():
            return "检查2"

        async def async_check():
            return "异步检查"

        checker.register_check("test_run_all_check1", check1, interval_seconds=0)
        checker.register_check("test_run_all_check2", check2, interval_seconds=0)
        checker.register_check("test_run_all_async_check", async_check, interval_seconds=0)

        results = await checker.run_all_checks()

        # 验证我们的检查都在结果中
        assert "test_run_all_check1" in results
        assert "test_run_all_check2" in results
        assert "test_run_all_async_check" in results
        assert results["test_run_all_check1"][0] is True
        assert results["test_run_all_check2"][0] is True
        assert results["test_run_all_async_check"][0] is True

    def test_get_status(self):
        """测试获取健康检查状态"""
        checker = get_health_checker()

        def test_check():
            return "OK"

        checker.register_check("test", test_check, interval_seconds=60)

        status = checker.get_status()
        assert "test" in status
        assert "last_result" in status["test"]
        assert "last_success" in status["test"]
        assert "interval" in status["test"]
        assert "last_check" in status["test"]


@pytest.mark.unit
class TestGracefulShutdown:
    """优雅关闭测试"""

    @pytest.mark.asyncio
    async def test_graceful_shutdown_logs(self, caplog):
        """测试优雅关闭日志"""
        loop = asyncio.get_event_loop()

        await graceful_shutdown("SIGTERM", loop)

        # 验证日志记录
        assert "收到信号 SIGTERM" in caplog.text or "优雅关闭" in caplog.text

    def test_setup_graceful_shutdown(self):
        """测试设置优雅关闭"""
        # 这个函数在Windows上可能不支持，但不应该抛出异常
        try:
            setup_graceful_shutdown()
        except Exception as e:
            # Windows上可能会失败，这是预期的
            assert not isinstance(e, AttributeError)


@pytest.mark.unit
class TestHealthCheckFunctions:
    """预定义健康检查函数测试"""

    @pytest.mark.asyncio
    async def test_check_database_connection(self):
        """测试数据库连接检查"""
        result = await check_database_connection()
        assert "无数据库配置" in result or result == "无数据库配置"


@pytest.mark.unit
class TestInitializeErrorHandling:
    """错误处理初始化测试"""

    def test_initialize_error_handling(self):
        """测试初始化错误处理系统"""
        reset_error_stats()  # 先重置状态

        health_checker = initialize_error_handling()

        assert health_checker is not None
        assert isinstance(health_checker, type(get_health_checker()))

        # 验证健康检查已注册
        status = health_checker.get_status()
        assert "telegram" in status or "ai_api" in status or "database" in status


if __name__ == "__main__":
    pytest.main([__file__, "-v"])
