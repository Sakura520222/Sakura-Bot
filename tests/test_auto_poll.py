"""测试自动趣味投票模块

Copyright 2026 Sakura-Bot
本项目采用 AGPL-3.0 许可
"""

import asyncio
import sys
from unittest.mock import AsyncMock, MagicMock, patch

import pytest
from fastapi import HTTPException

from core.handlers.auto_poll_handler import (
    AutoPollHandler,
    get_auto_poll_handler,
    initialize_auto_poll,
    shutdown_auto_poll,
)

# ==================== 辅助 Fixtures ====================


@pytest.fixture
def mock_client():
    """创建模拟的 Telegram 客户端"""
    client = MagicMock()
    client.send_message = AsyncMock(return_value=MagicMock(id=999))
    return client


@pytest.fixture
def handler(mock_client):
    """创建 AutoPollHandler 实例（不启动 worker）"""
    return AutoPollHandler(mock_client)


@pytest.fixture
def running_handler(mock_client):
    """创建并启动 AutoPollHandler 实例"""
    with patch(
        "core.handlers.auto_poll_handler.is_auto_poll_enabled_for_channel",
        return_value=True,
    ):
        h = AutoPollHandler(mock_client)
        h._running = True
        yield h


# ==================== 初始化测试 ====================


@pytest.mark.unit
class TestAutoPollHandlerInit:
    """AutoPollHandler 初始化测试"""

    def test_init_default_state(self, mock_client):
        """测试初始化默认状态"""
        h = AutoPollHandler(mock_client)
        assert h._running is False
        assert h._processed_count == 0
        assert h._failed_count == 0
        assert h._queue.maxsize == 500
        assert h.client is mock_client

    def test_get_stats_initial(self, handler):
        """测试初始统计信息"""
        stats = handler.get_stats()
        assert stats["running"] is False
        assert stats["queue_size"] == 0
        assert stats["processed_count"] == 0
        assert stats["failed_count"] == 0


# ==================== 入队测试 ====================


@pytest.mark.unit
class TestEnqueuePollTask:
    """入队逻辑测试"""

    def test_enqueue_success(self, running_handler):
        """测试成功入队"""
        result = running_handler.enqueue_poll_task(
            channel_id="@test_channel",
            discussion_id=123,
            forward_msg_id=456,
            message_text="这是一条测试消息内容",
        )
        assert result is True
        assert running_handler._queue.qsize() == 1

    def test_enqueue_empty_text_skipped(self, running_handler):
        """测试空文本消息不入队"""
        result = running_handler.enqueue_poll_task(
            channel_id="@test_channel",
            discussion_id=123,
            forward_msg_id=456,
            message_text="",
        )
        assert result is False
        assert running_handler._queue.qsize() == 0

    def test_enqueue_whitespace_only_skipped(self, running_handler):
        """测试仅空白字符的消息不入队"""
        result = running_handler.enqueue_poll_task(
            channel_id="@test_channel",
            discussion_id=123,
            forward_msg_id=456,
            message_text="   ",
        )
        assert result is False
        assert running_handler._queue.qsize() == 0

    def test_enqueue_not_running_skipped(self, handler):
        """测试未运行时入队被拒绝"""
        result = handler.enqueue_poll_task(
            channel_id="@test_channel",
            discussion_id=123,
            forward_msg_id=456,
            message_text="测试消息",
        )
        assert result is False

    def test_enqueue_disabled_channel_skipped(self, mock_client):
        """测试全局或频道禁用时不入队"""
        h = AutoPollHandler(mock_client)
        h._running = True

        with patch(
            "core.handlers.auto_poll_handler.is_auto_poll_enabled_for_channel",
            return_value=False,
        ):
            result = h.enqueue_poll_task(
                channel_id="@disabled_channel",
                discussion_id=123,
                forward_msg_id=456,
                message_text="这是一条测试消息内容",
            )

        assert result is False
        assert h._queue.qsize() == 0

    def test_enqueue_duplicate_skipped(self, running_handler):
        """测试重复消息被去重"""
        running_handler.enqueue_poll_task(
            channel_id="@test_channel",
            discussion_id=123,
            forward_msg_id=456,
            message_text="测试消息",
        )
        # 第二次相同消息应被去重
        result = running_handler.enqueue_poll_task(
            channel_id="@test_channel",
            discussion_id=123,
            forward_msg_id=456,
            message_text="另一条消息",
        )
        assert result is False
        assert running_handler._queue.qsize() == 1

    def test_enqueue_queue_full_backpressure(self, mock_client):
        """测试队列满时背压丢弃"""
        h = AutoPollHandler(mock_client)
        h._running = True
        # 使用小队列测试背压
        h._queue = asyncio.Queue(maxsize=2)

        with patch(
            "core.handlers.auto_poll_handler.is_auto_poll_enabled_for_channel",
            return_value=True,
        ):
            # 填满队列（去重缓存也需不同的 msg_id）
            h.enqueue_poll_task("@ch", 1, 1, "消息一")
            h.enqueue_poll_task("@ch", 1, 2, "消息二")

            # 第三条应该被丢弃
            result = h.enqueue_poll_task("@ch", 1, 3, "消息三")
        assert result is False
        assert h._queue.qsize() == 2


# ==================== 任务处理测试 ====================


@pytest.mark.unit
class TestProcessPollTask:
    """单任务处理测试"""

    @pytest.mark.asyncio
    async def test_process_disabled_channel(self, handler):
        """测试频道禁用时跳过处理"""
        task = {
            "channel_id": "@disabled_channel",
            "discussion_id": 123,
            "forward_msg_id": 456,
            "message_text": "这是一条较长的测试消息内容",
        }

        with patch(
            "core.handlers.auto_poll_handler.is_auto_poll_enabled_for_channel",
            return_value=False,
        ):
            await handler._process_poll_task(task)

        # 未调用 send_message
        handler.client.send_message.assert_not_called()
        assert handler._processed_count == 0

    @pytest.mark.asyncio
    async def test_process_short_text_skipped(self, handler):
        """测试短文本消息跳过"""
        task = {
            "channel_id": "@test_channel",
            "discussion_id": 123,
            "forward_msg_id": 456,
            "message_text": "短文本",
        }

        with patch(
            "core.handlers.auto_poll_handler.is_auto_poll_enabled_for_channel",
            return_value=True,
        ):
            await handler._process_poll_task(task)

        handler.client.send_message.assert_not_called()

    @pytest.mark.asyncio
    async def test_process_success(self, handler):
        """测试成功处理投票任务"""
        task = {
            "channel_id": "@test_channel",
            "discussion_id": 123,
            "forward_msg_id": 456,
            "message_text": "这是一条较长的测试消息内容，用于生成趣味投票",
        }

        mock_poll_data = {
            "question": "你觉得这条消息怎么样？",
            "options": ["选项A", "选项B", "选项C", "选项D"],
        }

        with (
            patch(
                "core.handlers.auto_poll_handler.is_auto_poll_enabled_for_channel",
                return_value=True,
            ),
            patch(
                "core.handlers.auto_poll_handler.generate_poll_from_summary",
                return_value=mock_poll_data,
            ),
        ):
            await handler._process_poll_task(task)

        handler.client.send_message.assert_called_once()
        assert handler._processed_count == 1
        assert handler._failed_count == 0

        # 验证 send_message 参数
        call_kwargs = handler.client.send_message.call_args
        assert call_kwargs[0][0] == 123  # discussion_id
        assert call_kwargs[1]["reply_to"] == 456  # forward_msg_id

    @pytest.mark.asyncio
    async def test_process_ai_failure_uses_default(self, handler):
        """测试 AI 生成失败时使用默认投票"""
        task = {
            "channel_id": "@test_channel",
            "discussion_id": 123,
            "forward_msg_id": 456,
            "message_text": "这是一条较长的测试消息内容，用于生成趣味投票",
        }

        with (
            patch(
                "core.handlers.auto_poll_handler.is_auto_poll_enabled_for_channel",
                return_value=True,
            ),
            patch(
                "core.handlers.auto_poll_handler.generate_poll_from_summary",
                return_value=None,
            ),
            patch(
                "core.handlers.auto_poll_handler.get_text",
                side_effect=lambda key, **kwargs: key,
            ),
        ):
            await handler._process_poll_task(task)

        handler.client.send_message.assert_called_once()
        assert handler._processed_count == 1

    @pytest.mark.asyncio
    async def test_process_send_failure_counted(self, handler):
        """测试发送失败计入失败计数"""
        task = {
            "channel_id": "@test_channel",
            "discussion_id": 123,
            "forward_msg_id": 456,
            "message_text": "这是一条较长的测试消息内容",
        }

        handler.client.send_message.side_effect = Exception("发送失败")

        with (
            patch(
                "core.handlers.auto_poll_handler.is_auto_poll_enabled_for_channel",
                return_value=True,
            ),
            patch(
                "core.handlers.auto_poll_handler.generate_poll_from_summary",
                return_value={"question": "测试", "options": ["A", "B"]},
            ),
        ):
            await handler._process_poll_task(task)

        assert handler._processed_count == 0
        assert handler._failed_count == 1


# ==================== Web API 配置测试 ====================


@pytest.mark.unit
class TestAutoPollWebApi:
    """自动趣味投票 Web API 配置测试"""

    @pytest.mark.asyncio
    async def test_update_channel_auto_poll_rejects_when_global_disabled(self):
        """测试全局关闭时拒绝更新频道级自动趣味投票"""
        from core.web_api.routes import interaction
        from core.web_api.schemas.interaction import AutoPollSettingsUpdate

        with (
            patch.object(interaction, "get_config", return_value={"enable_auto_poll": False}),
            patch.object(interaction, "write_config") as mock_write,
        ):
            with pytest.raises(HTTPException) as exc_info:
                await interaction.update_channel_auto_poll(
                    "https://t.me/example_channel",
                    AutoPollSettingsUpdate(enabled=True),
                )

        assert exc_info.value.status_code == 400
        assert "全局自动趣味投票" in exc_info.value.detail
        mock_write.assert_not_called()

    @pytest.mark.asyncio
    async def test_update_channel_auto_poll_saves_when_global_enabled(self):
        """测试全局开启时允许更新频道级自动趣味投票"""
        from core.web_api.routes import interaction
        from core.web_api.schemas.interaction import AutoPollSettingsUpdate

        config = {"enable_auto_poll": True, "channel_auto_poll_settings": {}}

        with (
            patch.object(interaction, "get_config", return_value=config),
            patch.object(interaction, "write_config") as mock_write,
        ):
            result = await interaction.update_channel_auto_poll(
                "https://t.me/example_channel",
                AutoPollSettingsUpdate(enabled=False),
            )

        assert result["success"] is True
        mock_write.assert_called_once()
        saved_config = mock_write.call_args.args[0]
        assert saved_config["channel_auto_poll_settings"]["https://t.me/example_channel"] == {
            "enabled": False
        }


# ==================== 生命周期测试 ====================


@pytest.mark.unit
class TestLifecycle:
    """start/stop 生命周期测试"""

    @pytest.mark.asyncio
    async def test_start_creates_worker(self, handler):
        """测试 start 创建 worker 任务"""
        await handler.start()
        assert handler._running is True
        assert handler._worker_task is not None
        assert not handler._worker_task.done()
        await handler.stop()

    @pytest.mark.asyncio
    async def test_stop_waits_for_queue(self, mock_client, monkeypatch):
        """测试 stop 等待队列清空"""
        monkeypatch.setattr(
            "core.handlers.auto_poll_handler.is_auto_poll_enabled_for_channel",
            lambda _channel: True,
        )
        h = AutoPollHandler(mock_client)
        h._process_poll_task = AsyncMock()
        await h.start()

        # 入队一个任务
        h.enqueue_poll_task("@ch", 123, 456, "测试消息")
        assert h._queue.qsize() == 1

        # stop 应等待队列处理完成
        await asyncio.wait_for(h.stop(), timeout=1)
        assert h._running is False
        assert h._queue.empty()
        h._process_poll_task.assert_awaited_once()

    @pytest.mark.asyncio
    async def test_stop_waits_for_in_flight_task(self, mock_client, monkeypatch):
        """队列已取空时，stop 仍应等待正在处理的任务完成。"""
        monkeypatch.setattr(
            "core.handlers.auto_poll_handler.is_auto_poll_enabled_for_channel",
            lambda _channel: True,
        )
        started = asyncio.Event()
        finish = asyncio.Event()
        completed = asyncio.Event()

        async def process(_task):
            started.set()
            await finish.wait()
            completed.set()

        h = AutoPollHandler(mock_client)
        h._process_poll_task = AsyncMock(side_effect=process)
        await h.start()
        h.enqueue_poll_task("@ch", 123, 456, "测试消息")
        await asyncio.wait_for(started.wait(), timeout=1)
        assert h._queue.empty()

        stopping = asyncio.create_task(h.stop())
        try:
            await asyncio.sleep(0)
            assert not stopping.done()
        finally:
            finish.set()
            await asyncio.wait_for(stopping, timeout=1)

        h._process_poll_task.assert_awaited_once()
        assert completed.is_set()
        await asyncio.wait_for(h._queue.join(), timeout=1)

    @pytest.mark.asyncio
    async def test_double_start_warning(self, handler):
        """测试重复 start 不报错"""
        await handler.start()
        await handler.start()  # 应该不报错
        assert handler._running is True
        await handler.stop()

    @pytest.mark.asyncio
    async def test_stop_when_not_running(self, handler):
        """测试未运行时 stop 不报错"""
        await handler.stop()  # 应该不报错


# ==================== 单例管理测试 ====================


@pytest.mark.unit
class TestSingleton:
    """模块级单例管理测试"""

    @pytest.mark.asyncio
    async def test_initialize_creates_instance(self, mock_client):
        """测试 initialize 创建全局实例"""
        # 清理可能存在的旧实例
        import core.handlers.auto_poll_handler as mod

        mod._auto_poll_handler = None

        h = await initialize_auto_poll(mock_client)
        assert h is not None
        assert h.client is mock_client
        assert get_auto_poll_handler() is h

        # 清理
        await shutdown_auto_poll()
        mod._auto_poll_handler = None

    @pytest.mark.asyncio
    async def test_initialize_idempotent(self, mock_client):
        """测试重复初始化返回同一实例"""
        import core.handlers.auto_poll_handler as mod

        mod._auto_poll_handler = None

        h1 = await initialize_auto_poll(mock_client)
        h2 = await initialize_auto_poll(mock_client)
        assert h1 is h2

        # 清理
        await shutdown_auto_poll()
        mod._auto_poll_handler = None

    @pytest.mark.asyncio
    async def test_shutdown_clears_instance(self, mock_client):
        """测试 shutdown 清除全局实例"""
        import core.handlers.auto_poll_handler as mod

        mod._auto_poll_handler = None

        await initialize_auto_poll(mock_client)
        assert get_auto_poll_handler() is not None

        await shutdown_auto_poll()
        assert get_auto_poll_handler() is None

        mod._auto_poll_handler = None


# ==================== 去重缓存测试 ====================


@pytest.mark.unit
class TestDeduplicationCache:
    """去重缓存测试"""

    def test_different_messages_different_channels(self, running_handler):
        """测试不同频道/不同消息可以入队"""
        r1 = running_handler.enqueue_poll_task("@ch1", 111, 1, "消息一")
        r2 = running_handler.enqueue_poll_task("@ch2", 222, 2, "消息二")
        assert r1 is True
        assert r2 is True
        assert running_handler._queue.qsize() == 2


# ==================== 频道标识归一化与配置生效测试（Issue #115） ====================


def _old_config_module():
    """获取自动趣味投票判定函数实际读取全局变量的模块（core._old_config）"""
    import core.config  # noqa: F401 确保旧配置模块已加载

    return sys.modules["core._old_config"]


@pytest.mark.unit
class TestAutoPollChannelKeyResolution:
    """频道标识归一化测试

    WebUI 以完整 URL 写入配置键，后台以 username 查询，
    必须命中同一频道的配置，否则显式禁用会被误判为"无独立配置"。
    使用真实配置字典与真实判定函数，不 Mock is_auto_poll_enabled_for_channel。
    """

    @pytest.fixture(autouse=True)
    def setup_real_config(self, monkeypatch):
        """让真实判定函数读取测试配置字典"""
        self.settings = {
            "https://t.me/Nahida_Leak": {"enabled": False},
            "https://t.me/FireflyLeak": {"enabled": True},
        }
        monkeypatch.setattr(_old_config_module(), "CHANNEL_AUTO_POLL_SETTINGS", self.settings)
        monkeypatch.setattr(_old_config_module(), "ENABLE_AUTO_POLL", True)

    def test_disabled_channel_matched_by_all_key_variants(self):
        """WebUI 存 URL 键，后台传 username/@/大小写混合/尾斜杠都解析到 disabled"""
        from core.config import get_channel_auto_poll_config, is_auto_poll_enabled_for_channel

        for channel in (
            "Nahida_Leak",
            "@Nahida_Leak",
            "https://t.me/Nahida_Leak",
            "https://t.me/nahida_leak",
            "https://t.me/NAHIDA_LEAK/",
            "t.me/Nahida_Leak",
        ):
            config = get_channel_auto_poll_config(channel)
            assert config["enabled"] is False, channel
            assert config["matched_key"] == "https://t.me/Nahida_Leak", channel
            assert is_auto_poll_enabled_for_channel(channel) is False, channel

    def test_explicitly_enabled_channel_still_enabled(self):
        """显式启用的频道保持启用"""
        from core.config import is_auto_poll_enabled_for_channel

        assert is_auto_poll_enabled_for_channel("FireflyLeak") is True
        assert is_auto_poll_enabled_for_channel("@FireflyLeak") is True

    def test_unconfigured_channel_inherits_global(self):
        """无独立配置的频道继承全局开关（全局开启时为 True）"""
        from core.config import is_auto_poll_enabled_for_channel

        assert is_auto_poll_enabled_for_channel("SomeOtherChannel") is True

    def test_global_off_disables_all_channels(self, monkeypatch):
        """全局关闭时一律关闭，显式 true 也不生效"""
        from core.config import is_auto_poll_enabled_for_channel

        monkeypatch.setattr(_old_config_module(), "ENABLE_AUTO_POLL", False)
        assert is_auto_poll_enabled_for_channel("@FireflyLeak") is False
        assert is_auto_poll_enabled_for_channel("SomeOtherChannel") is False

    def test_numeric_id_does_not_mismatch_other_channels(self):
        """数字 ID 仅严格匹配，不与 username 或 c/<id> 链接互相猜测"""
        from core.config import is_auto_poll_enabled_for_channel

        self.settings["https://t.me/c/1234567890"] = {"enabled": False}
        # 裸数字 ID 不等于 c/ 链接键，也不能命中任何 username 键 → 继承全局
        assert is_auto_poll_enabled_for_channel("1234567890") is True
        # c/<id> 链接形式与存储键一致 → 命中禁用配置
        assert is_auto_poll_enabled_for_channel("https://t.me/c/1234567890") is False
        # 其它频道不受影响
        assert is_auto_poll_enabled_for_channel("Nahida_Leak") is False
        assert is_auto_poll_enabled_for_channel("FireflyLeak") is True


@pytest.mark.unit
class TestAutoPollHandlerWithRealConfig:
    """基于真实配置字典的处理器行为测试（不 Mock 判定函数本身）"""

    @pytest.fixture(autouse=True)
    def setup_real_config(self, monkeypatch):
        self.settings = {
            "https://t.me/Nahida_Leak": {"enabled": False},
            "https://t.me/FireflyLeak": {"enabled": True},
        }
        monkeypatch.setattr(_old_config_module(), "CHANNEL_AUTO_POLL_SETTINGS", self.settings)
        monkeypatch.setattr(_old_config_module(), "ENABLE_AUTO_POLL", True)

    def test_enqueue_rejected_for_disabled_channel_variant_keys(self, mock_client):
        """入队前检查：显式禁用频道的各种键写法都不入队（不调用 AI）"""
        h = AutoPollHandler(mock_client)
        h._running = True

        for channel in ("Nahida_Leak", "@Nahida_Leak", "https://t.me/nahida_leak"):
            assert h.enqueue_poll_task(channel, 123, 1, "这是一条测试消息内容") is False
        assert h._queue.qsize() == 0

    def test_enqueue_accepted_for_enabled_channel(self, mock_client):
        """显式启用的频道正常入队"""
        h = AutoPollHandler(mock_client)
        h._running = True

        assert h.enqueue_poll_task("FireflyLeak", 123, 1, "这是一条测试消息内容") is True
        assert h._queue.qsize() == 1

    @pytest.mark.asyncio
    async def test_queued_task_skipped_after_disable(self, handler):
        """已入队但未处理的任务，禁用后不再生成与发送"""
        task = {
            "channel_id": "https://t.me/Nahida_Leak",
            "discussion_id": 123,
            "forward_msg_id": 456,
            "message_text": "这是一条较长的测试消息内容，用于生成趣味投票",
        }

        with patch(
            "core.handlers.auto_poll_handler.generate_poll_from_summary",
            new_callable=AsyncMock,
        ) as mock_generate:
            await handler._process_poll_task(task)

        mock_generate.assert_not_called()
        handler.client.send_message.assert_not_called()
        assert handler._processed_count == 0

    @pytest.mark.asyncio
    async def test_send_cancelled_when_disabled_during_generation(self, mock_client):
        """AI 生成过程中关闭频道开关：生成完成但最终不发送"""
        h = AutoPollHandler(mock_client)

        async def disable_during_generation(_text):
            # 模拟 AI 生成期间管理员通过 WebUI 禁用该频道（热重载即时生效）
            self.settings["https://t.me/Nahida_Leak"]["enabled"] = False
            return {"question": "生成期间被禁用", "options": ["选项A", "选项B"]}

        task = {
            "channel_id": "https://t.me/Nahida_Leak",
            "discussion_id": 123,
            "forward_msg_id": 456,
            "message_text": "这是一条较长的测试消息内容，用于生成趣味投票",
        }

        self.settings["https://t.me/Nahida_Leak"]["enabled"] = True
        with patch(
            "core.handlers.auto_poll_handler.generate_poll_from_summary",
            side_effect=disable_during_generation,
        ):
            await h._process_poll_task(task)

        h.client.send_message.assert_not_called()
        assert h._processed_count == 0
        assert h._failed_count == 0

    @pytest.mark.asyncio
    async def test_send_succeeds_for_enabled_channel_with_real_config(self, mock_client):
        """显式启用频道（真实配置判定）全流程生成并发送成功"""
        h = AutoPollHandler(mock_client)

        task = {
            "channel_id": "https://t.me/FireflyLeak",
            "discussion_id": 123,
            "forward_msg_id": 456,
            "message_text": "这是一条较长的测试消息内容，用于生成趣味投票",
        }

        with patch(
            "core.handlers.auto_poll_handler.generate_poll_from_summary",
            return_value={"question": "测试问题", "options": ["选项A", "选项B"]},
        ):
            await h._process_poll_task(task)

        h.client.send_message.assert_called_once()
        assert h._processed_count == 1


@pytest.mark.unit
class TestLinkedChannelCanonicalKeyResolution:
    """讨论组事件 linked_chat_id → CHANNELS 规范配置键解析测试"""

    def _make_handler(self, mock_client, username):
        entity = MagicMock()
        entity.username = username
        mock_client.get_entity = AsyncMock(return_value=entity)
        from core.handlers.channel_comment_welcome import CommentWelcomeHandler

        return CommentWelcomeHandler(client=mock_client)

    @pytest.mark.asyncio
    async def test_resolve_returns_channels_entry(self, mock_client, monkeypatch):
        """实体解析出的 username 能命中 CHANNELS 中的原始条目（规范键）"""
        monkeypatch.setattr(
            _old_config_module(),
            "CHANNELS",
            ["https://t.me/Nahida_Leak", "https://t.me/FireflyLeak"],
        )
        handler = self._make_handler(mock_client, "Nahida_Leak")

        identifier, canonical_key = await handler._resolve_linked_channel(123456)

        assert identifier == "Nahida_Leak"
        assert canonical_key == "https://t.me/Nahida_Leak"

    @pytest.mark.asyncio
    async def test_resolve_username_case_insensitive(self, mock_client, monkeypatch):
        """username 大小写差异仍命中同一白名单条目"""
        monkeypatch.setattr(_old_config_module(), "CHANNELS", ["https://t.me/Nahida_Leak"])
        handler = self._make_handler(mock_client, "nahida_leak")

        identifier, canonical_key = await handler._resolve_linked_channel(123456)

        assert identifier == "nahida_leak"
        assert canonical_key == "https://t.me/Nahida_Leak"

    @pytest.mark.asyncio
    async def test_resolve_private_channel_by_numeric_id(self, mock_client, monkeypatch):
        """无 username 的私有频道按数字 ID / c/<id> 链接匹配，不猜测 username"""
        monkeypatch.setattr(_old_config_module(), "CHANNELS", ["https://t.me/c/1234567890"])
        handler = self._make_handler(mock_client, None)

        identifier, canonical_key = await handler._resolve_linked_channel(1234567890)

        assert identifier == "1234567890"
        assert canonical_key == "https://t.me/c/1234567890"

    @pytest.mark.asyncio
    async def test_resolve_not_in_whitelist_returns_none(self, mock_client, monkeypatch):
        """不在白名单的频道返回 None，且不误配其它频道"""
        monkeypatch.setattr(_old_config_module(), "CHANNELS", ["https://t.me/OtherChannel"])
        handler = self._make_handler(mock_client, "Nahida_Leak")

        identifier, canonical_key = await handler._resolve_linked_channel(123456)

        assert identifier == "Nahida_Leak"
        assert canonical_key is None

    def test_is_channel_in_whitelist_case_insensitive(self):
        """白名单匹配忽略 URL 前缀、@ 与大小写差异"""
        from core.handlers.channel_comment_welcome import is_channel_in_whitelist

        channels = ["https://t.me/Nahida_Leak", "@FireflyLeak"]
        assert is_channel_in_whitelist("Nahida_Leak", channels) is True
        assert is_channel_in_whitelist("nahida_leak", channels) is True
        assert is_channel_in_whitelist("@fireflyleak", channels) is True
        assert is_channel_in_whitelist("https://t.me/NAHIDA_LEAK/", channels) is True
        assert is_channel_in_whitelist("OtherChannel", channels) is False
