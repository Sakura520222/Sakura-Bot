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
错误处理与恢复机制增强模块
提供优雅的错误处理、重试机制和健康检查功能
"""

import asyncio
import inspect
import logging
import time
from collections.abc import Callable
from datetime import datetime, timedelta
from functools import wraps
from typing import Any

logger = logging.getLogger(__name__)

# 全局错误统计
_error_stats = {
    "total_errors": 0,
    "recent_errors": [],  # 最近错误记录
    "error_types": {},  # 错误类型统计
    "last_error_time": None,
}

# 重试配置
DEFAULT_RETRY_CONFIG = {
    "max_retries": 3,
    "base_delay": 1.0,  # 基础延迟秒数
    "max_delay": 30.0,  # 最大延迟秒数
    "exponential_backoff": True,
    "retry_on_exceptions": (Exception,),  # 默认重试所有异常
    "skip_retry_on_exceptions": (KeyboardInterrupt, SystemExit),  # 不重试的异常
}


class RetryExhaustedError(Exception):
    """重试耗尽异常"""

    def __init__(self, message: str, last_exception: Exception):
        super().__init__(message)
        self.last_exception = last_exception


def record_error(error: Exception, context: str = ""):
    """记录错误统计信息"""
    global _error_stats

    _error_stats["total_errors"] += 1
    _error_stats["last_error_time"] = datetime.now()

    error_type = type(error).__name__
    _error_stats["error_types"][error_type] = _error_stats["error_types"].get(error_type, 0) + 1

    # 记录最近错误（最多保留10个）
    error_record = {
        "time": datetime.now().isoformat(),
        "type": error_type,
        "message": str(error),
        "context": context,
    }
    _error_stats["recent_errors"].append(error_record)
    if len(_error_stats["recent_errors"]) > 10:
        _error_stats["recent_errors"].pop(0)

    logger.error(f"错误记录: {context} - {error_type}: {error}")


def get_error_stats() -> dict:
    """获取错误统计信息"""
    return _error_stats.copy()


def reset_error_stats():
    """重置错误统计"""
    global _error_stats
    _error_stats = {
        "total_errors": 0,
        "recent_errors": [],
        "error_types": {},
        "last_error_time": None,
    }


def retry_with_backoff(
    max_retries: int = None,
    base_delay: float = None,
    max_delay: float = None,
    exponential_backoff: bool = None,
    retry_on_exceptions: tuple = None,
    skip_retry_on_exceptions: tuple = None,
):
    """
    重试装饰器，支持指数退避

    Args:
        max_retries: 最大重试次数
        base_delay: 基础延迟秒数
        max_delay: 最大延迟秒数
        exponential_backoff: 是否使用指数退避
        retry_on_exceptions: 需要重试的异常类型
        skip_retry_on_exceptions: 跳过重试的异常类型
    """

    def decorator(func: Callable):
        # 使用函数签名检查是否为异步函数
        is_async = inspect.iscoroutinefunction(func)

        @wraps(func)
        async def async_wrapper(*args, **kwargs):
            config = DEFAULT_RETRY_CONFIG.copy()
            if max_retries is not None:
                config["max_retries"] = max_retries
            if base_delay is not None:
                config["base_delay"] = base_delay
            if max_delay is not None:
                config["max_delay"] = max_delay
            if exponential_backoff is not None:
                config["exponential_backoff"] = exponential_backoff
            if retry_on_exceptions is not None:
                config["retry_on_exceptions"] = retry_on_exceptions
            if skip_retry_on_exceptions is not None:
                config["skip_retry_on_exceptions"] = skip_retry_on_exceptions

            last_exception = None
            for attempt in range(config["max_retries"] + 1):
                try:
                    if attempt > 0:
                        logger.info(f"重试 {func.__name__}，第 {attempt} 次尝试")

                    return await func(*args, **kwargs)

                except config["skip_retry_on_exceptions"] as e:
                    # 不重试的异常直接抛出
                    record_error(e, f"skip_retry: {func.__name__}")
                    raise

                except config["retry_on_exceptions"] as e:
                    last_exception = e
                    record_error(e, f"retry_attempt_{attempt}: {func.__name__}")

                    if attempt == config["max_retries"]:
                        # 重试次数耗尽
                        logger.error(f"{func.__name__} 重试次数耗尽 ({config['max_retries']} 次)")
                        raise RetryExhaustedError(
                            f"{func.__name__} 重试次数耗尽", last_exception
                        ) from last_exception

                    # 计算延迟时间
                    if config["exponential_backoff"]:
                        delay = min(config["base_delay"] * (2**attempt), config["max_delay"])
                    else:
                        delay = config["base_delay"]

                    logger.warning(
                        f"{func.__name__} 第 {attempt + 1} 次失败，"
                        f"{delay:.1f}秒后重试: {type(e).__name__}: {e}"
                    )

                    await asyncio.sleep(delay)

            # 理论上不会执行到这里
            raise RetryExhaustedError(f"{func.__name__} 重试次数耗尽", last_exception)

        @wraps(func)
        def sync_wrapper(*args, **kwargs):
            config = DEFAULT_RETRY_CONFIG.copy()
            if max_retries is not None:
                config["max_retries"] = max_retries
            if base_delay is not None:
                config["base_delay"] = base_delay
            if max_delay is not None:
                config["max_delay"] = max_delay
            if exponential_backoff is not None:
                config["exponential_backoff"] = exponential_backoff
            if retry_on_exceptions is not None:
                config["retry_on_exceptions"] = retry_on_exceptions
            if skip_retry_on_exceptions is not None:
                config["skip_retry_on_exceptions"] = skip_retry_on_exceptions

            last_exception = None
            for attempt in range(config["max_retries"] + 1):
                try:
                    if attempt > 0:
                        logger.info(f"重试 {func.__name__}，第 {attempt} 次尝试")

                    return func(*args, **kwargs)

                except config["skip_retry_on_exceptions"] as e:
                    # 不重试的异常直接抛出
                    record_error(e, f"skip_retry: {func.__name__}")
                    raise

                except config["retry_on_exceptions"] as e:
                    last_exception = e
                    record_error(e, f"retry_attempt_{attempt}: {func.__name__}")

                    if attempt == config["max_retries"]:
                        # 重试次数耗尽
                        logger.error(f"{func.__name__} 重试次数耗尽 ({config['max_retries']} 次)")
                        raise RetryExhaustedError(
                            f"{func.__name__} 重试次数耗尽", last_exception
                        ) from last_exception

                    # 计算延迟时间
                    if config["exponential_backoff"]:
                        delay = min(config["base_delay"] * (2**attempt), config["max_delay"])
                    else:
                        delay = config["base_delay"]

                    logger.warning(
                        f"{func.__name__} 第 {attempt + 1} 次失败，"
                        f"{delay:.1f}秒后重试: {type(e).__name__}: {e}"
                    )

                    time.sleep(delay)

            # 理论上不会执行到这里
            raise RetryExhaustedError(f"{func.__name__} 重试次数耗尽", last_exception)

        return async_wrapper if is_async else sync_wrapper

    return decorator


class HealthChecker:
    """健康检查器"""

    def __init__(self):
        self.checks = {}
        self.last_check_time = {}

    def register_check(self, name: str, check_func: Callable, interval_seconds: int = 60):
        """注册健康检查函数"""
        self.checks[name] = {
            "func": check_func,
            "interval": interval_seconds,
            "last_result": None,
            "last_success": None,
        }
        self.last_check_time[name] = datetime.now() - timedelta(seconds=interval_seconds + 1)

    async def run_check(self, name: str) -> tuple[bool, Any]:
        """运行指定的健康检查"""
        if name not in self.checks:
            return False, f"未找到健康检查: {name}"

        check = self.checks[name]
        now = datetime.now()

        # 检查是否需要运行
        if (now - self.last_check_time[name]).total_seconds() < check["interval"]:
            return check["last_result"], check["last_success"]

        try:
            result = (
                await check["func"]()
                if inspect.iscoroutinefunction(check["func"])
                else check["func"]()
            )
            self.last_check_time[name] = now
            check["last_result"] = True
            check["last_success"] = result
            return True, result
        except Exception as e:
            self.last_check_time[name] = now
            check["last_result"] = False
            check["last_success"] = str(e)
            record_error(e, f"health_check: {name}")
            return False, str(e)

    async def run_all_checks(self) -> dict[str, tuple[bool, Any]]:
        """运行所有健康检查"""
        results = {}
        for name in self.checks:
            success, detail = await self.run_check(name)
            results[name] = (success, detail)
        return results

    def get_status(self) -> dict[str, Any]:
        """获取健康检查状态"""
        status = {}
        for name, check in self.checks.items():
            status[name] = {
                "last_result": check["last_result"],
                "last_success": check["last_success"],
                "interval": check["interval"],
                "last_check": self.last_check_time.get(name),
            }
        return status


# 全局健康检查器实例
_health_checker = HealthChecker()


def get_health_checker() -> HealthChecker:
    """获取全局健康检查器实例"""
    return _health_checker


async def graceful_shutdown(signame: str, loop: asyncio.AbstractEventLoop):
    """优雅关闭处理"""
    logger.info(f"收到信号 {signame}，开始优雅关闭...")

    # 记录关闭事件
    logger.info("保存当前状态...")

    # 等待进行中的任务完成（最多10秒）
    tasks = [t for t in asyncio.all_tasks() if t is not asyncio.current_task()]
    if tasks:
        logger.info(f"等待 {len(tasks)} 个任务完成...")
        await asyncio.wait(tasks, timeout=10.0)

    logger.info("优雅关闭完成")
    loop.stop()


def setup_graceful_shutdown():
    """设置优雅关闭信号处理（Unix系统）"""
    try:
        import signal

        loop = asyncio.get_event_loop()

        for signame in ("SIGINT", "SIGTERM"):
            loop.add_signal_handler(
                getattr(signal, signame),
                lambda s=signame: asyncio.create_task(graceful_shutdown(s, loop)),
            )

        logger.info("优雅关闭信号处理已设置")
    except (ImportError, NotImplementedError):
        logger.warning("当前系统不支持信号处理，优雅关闭功能受限")


# 预定义的健康检查函数
async def check_telegram_connection():
    """检查Telegram连接"""
    from telethon import TelegramClient

    from core.config import API_HASH, API_ID, BOT_TOKEN

    try:
        async with TelegramClient("health_check", int(API_ID), API_HASH) as client:
            await client.start(bot_token=BOT_TOKEN)
            me = await client.get_me()
            return f"Telegram连接正常，机器人: @{me.username}"
    except Exception as e:
        raise Exception(f"Telegram连接失败: {e}") from e


async def check_ai_api():
    """检查AI API连接"""
    from openai import OpenAI

    from core.config import LLM_API_KEY, LLM_BASE_URL, LLM_MODEL

    try:
        client = OpenAI(api_key=LLM_API_KEY, base_url=LLM_BASE_URL)
        # 发送一个简单的测试请求
        client.chat.completions.create(
            model=LLM_MODEL, messages=[{"role": "user", "content": "Hello"}], max_tokens=5
        )
        return f"AI API连接正常，模型: {LLM_MODEL}"
    except Exception as e:
        raise Exception(f"AI API连接失败: {e}") from e


async def check_database_connection():
    """检查数据库连接（如果有的话）"""
    # 这里可以添加数据库连接检查
    # 目前项目没有数据库，返回成功
    return "无数据库配置"


def initialize_error_handling():
    """初始化错误处理系统"""
    logger.info("初始化错误处理系统...")

    # 设置优雅关闭
    setup_graceful_shutdown()

    # 注册默认健康检查
    health_checker = get_health_checker()
    health_checker.register_check("telegram", check_telegram_connection, interval_seconds=300)
    health_checker.register_check("ai_api", check_ai_api, interval_seconds=300)
    health_checker.register_check("database", check_database_connection, interval_seconds=300)

    logger.info("错误处理系统初始化完成")
    return health_checker
