# tests/core/config/test_event_bus.py
import asyncio

import pytest

from core.config.event_bus import AsyncIOEventBus


class TestEvent:
    pass


@pytest.mark.asyncio
async def test_event_bus_subscribe_and_publish():
    """测试事件总线的基本订阅和发布功能"""
    bus = AsyncIOEventBus()
    received = []

    def callback(event):
        received.append(event)

    await bus.subscribe(TestEvent, callback)
    await bus.publish(TestEvent())

    assert len(received) == 1


@pytest.mark.asyncio
async def test_event_bus_priority_order():
    """测试优先级顺序执行"""
    bus = AsyncIOEventBus(sequential_mode=True)
    execution_order = []

    async def callback_low(event):
        execution_order.append("low")

    async def callback_high(event):
        execution_order.append("high")

    # 高优先级应该先执行
    await bus.subscribe(TestEvent, callback_low, priority=AsyncIOEventBus.PRIORITY_LOW)
    await bus.subscribe(TestEvent, callback_high, priority=AsyncIOEventBus.PRIORITY_HIGH)

    await bus.publish(TestEvent())

    assert execution_order == ["high", "low"]


@pytest.mark.asyncio
async def test_event_bus_timeout_protection():
    """测试超时保护机制"""
    bus = AsyncIOEventBus()

    async def blocking_callback(event):
        await asyncio.sleep(100)  # 超过超时时间

    await bus.subscribe(TestEvent, blocking_callback, timeout=0.1)

    # 应该超时但不抛出异常
    await bus.publish(TestEvent())


@pytest.mark.asyncio
async def test_event_bus_unsubscribe():
    """测试取消订阅功能"""
    bus = AsyncIOEventBus()
    received = []

    def callback(event):
        received.append(event)

    # 订阅事件
    await bus.subscribe(TestEvent, callback)

    # 发布事件，应该被接收
    await bus.publish(TestEvent())
    assert len(received) == 1

    # 取消订阅
    result = await bus.unsubscribe(TestEvent, callback)
    assert result is True

    # 发布事件，不应该被接收
    await bus.publish(TestEvent())
    assert len(received) == 1  # 仍然是1，没有增加


@pytest.mark.asyncio
async def test_event_bus_unsubscribe_nonexistent():
    """测试取消不存在的订阅"""
    bus = AsyncIOEventBus()

    def callback(event):
        pass

    # 尝试取消不存在的订阅
    result = await bus.unsubscribe(TestEvent, callback)
    assert result is False
