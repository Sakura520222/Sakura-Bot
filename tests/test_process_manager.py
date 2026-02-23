"""测试 Process Manager 模块

Copyright 2026 Sakura-Bot
本项目采用 AGPL-3.0 许可
"""

import os
import time
from unittest.mock import MagicMock, patch

import pytest

from core.process_manager import (
    check_qa_bot_health,
    format_uptime,
    get_qa_bot_process,
    get_qa_bot_status,
    restart_qa_bot,
    start_qa_bot,
    stop_qa_bot,
)


@pytest.mark.unit
class TestStartQABot:
    """启动QA Bot测试"""

    @patch.dict(os.environ, {"QA_BOT_ENABLED": "True", "QA_BOT_TOKEN": "test_token"})
    @patch("core.process_manager.subprocess.Popen")
    def test_start_qa_bot_success(self, mock_popen):
        """测试成功启动QA Bot"""
        mock_process = MagicMock()
        mock_process.pid = 12345
        mock_popen.return_value = mock_process

        result = start_qa_bot()

        assert result["success"] is True
        assert result["pid"] == 12345
        mock_popen.assert_called_once()

    @patch.dict(os.environ, {"QA_BOT_ENABLED": "False"})
    def test_start_qa_bot_disabled(self):
        """测试QA Bot未启用"""
        # 重置全局变量
        import core.process_manager

        core.process_manager._qa_bot_process = None

        result = start_qa_bot()

        assert result["success"] is False
        assert result["pid"] is None

    @patch.dict(os.environ, {"QA_BOT_ENABLED": "True", "QA_BOT_TOKEN": ""})
    def test_start_qa_bot_no_token(self):
        """测试未配置token"""
        import core.process_manager

        core.process_manager._qa_bot_process = None

        result = start_qa_bot()

        assert result["success"] is False
        assert "token" in result["message"].lower()

    @patch.dict(os.environ, {"QA_BOT_ENABLED": "True", "QA_BOT_TOKEN": "test"})
    def test_start_qa_bot_already_running(self):
        """测试QA Bot已在运行"""
        import core.process_manager

        mock_process = MagicMock()
        mock_process.pid = 12345
        core.process_manager._qa_bot_process = mock_process

        result = start_qa_bot()

        assert result["success"] is False
        assert result["pid"] == 12345


@pytest.mark.unit
class TestStopQABot:
    """停止QA Bot测试"""

    def test_stop_qa_bot_success(self):
        """测试成功停止QA Bot"""
        import core.process_manager

        mock_process = MagicMock()
        mock_process.pid = 12345
        mock_process.wait.return_value = None
        core.process_manager._qa_bot_process = mock_process

        result = stop_qa_bot()

        assert result["success"] is True
        mock_process.terminate.assert_called_once()
        assert core.process_manager._qa_bot_process is None

    def test_stop_qa_bot_not_running(self):
        """测试停止未运行的QA Bot"""
        import core.process_manager

        core.process_manager._qa_bot_process = None
        core.process_manager._qa_bot_start_time = None

        result = stop_qa_bot()

        assert result["success"] is True


@pytest.mark.unit
class TestGetQABotProcess:
    """获取QA Bot进程测试"""

    def test_get_qa_bot_process(self):
        """测试获取进程引用"""
        import core.process_manager

        mock_process = MagicMock()
        core.process_manager._qa_bot_process = mock_process

        result = get_qa_bot_process()

        assert result is mock_process


@pytest.mark.unit
class TestRestartQABot:
    """重启QA Bot测试"""

    @patch.dict(os.environ, {"QA_BOT_ENABLED": "True", "QA_BOT_TOKEN": "test"})
    @patch("core.process_manager.subprocess.Popen")
    @patch("core.process_manager.time.sleep")
    def test_restart_qa_bot_success(self, mock_sleep, mock_popen):
        """测试成功重启QA Bot"""
        import core.process_manager

        mock_process = MagicMock()
        mock_process.pid = 12345
        mock_process.wait.return_value = None
        core.process_manager._qa_bot_process = mock_process
        mock_popen.return_value = mock_process

        result = restart_qa_bot()

        assert result["success"] is True
        assert result["pid"] == 12345
        mock_sleep.assert_called_once()


@pytest.mark.unit
class TestGetQABotStatus:
    """获取QA Bot状态测试"""

    @patch.dict(os.environ, {"QA_BOT_ENABLED": "True", "QA_BOT_TOKEN": "test_token"})
    def test_get_status_running(self):
        """测试获取运行中状态"""
        import core.process_manager

        mock_process = MagicMock()
        mock_process.pid = 12345
        mock_process.poll.return_value = None  # 进程运行中
        core.process_manager._qa_bot_process = mock_process
        core.process_manager._qa_bot_start_time = time.time() - 100

        status = get_qa_bot_status()

        assert status["running"] is True
        assert status["pid"] == 12345
        assert status["enabled"] is True
        assert status["token_configured"] is True
        assert status["uptime_seconds"] >= 100

    @patch.dict(os.environ, {"QA_BOT_ENABLED": "True", "QA_BOT_TOKEN": "test_token"})
    def test_get_status_not_running(self):
        """测试获取未运行状态"""
        import core.process_manager

        core.process_manager._qa_bot_process = None
        core.process_manager._qa_bot_start_time = None

        status = get_qa_bot_status()

        assert status["running"] is False
        assert status["pid"] is None
        assert status["enabled"] is True

    @patch.dict(os.environ, {"QA_BOT_ENABLED": "False"})
    def test_get_status_disabled(self):
        """测试获取禁用状态"""
        import core.process_manager

        core.process_manager._qa_bot_process = None

        status = get_qa_bot_status()

        assert status["enabled"] is False

    @patch.dict(os.environ, {"QA_BOT_ENABLED": "True", "QA_BOT_TOKEN": ""})
    def test_get_status_no_token(self):
        """测试未配置token状态"""
        import core.process_manager

        core.process_manager._qa_bot_process = None

        status = get_qa_bot_status()

        assert status["token_configured"] is False


@pytest.mark.unit
class TestCheckQABotHealth:
    """检查QA Bot健康状态测试"""

    @patch.dict(os.environ, {"QA_BOT_ENABLED": "False"})
    def test_check_health_disabled(self):
        """测试禁用时的健康检查"""
        is_healthy, should_restart, message = check_qa_bot_health()

        assert is_healthy is True
        assert should_restart is False

    @patch.dict(os.environ, {"QA_BOT_ENABLED": "True", "QA_BOT_TOKEN": ""})
    def test_check_health_no_token(self):
        """测试无token时的健康检查"""
        import core.process_manager

        core.process_manager._qa_bot_process = None

        is_healthy, should_restart, message = check_qa_bot_health()

        assert is_healthy is False
        assert should_restart is False

    @patch.dict(os.environ, {"QA_BOT_ENABLED": "True", "QA_BOT_TOKEN": "test"})
    def test_check_health_running(self):
        """测试运行中的健康检查"""
        import core.process_manager

        mock_process = MagicMock()
        mock_process.pid = 12345
        mock_process.poll.return_value = None
        core.process_manager._qa_bot_process = mock_process
        core.process_manager._qa_bot_start_time = time.time()

        is_healthy, should_restart, message = check_qa_bot_health()

        assert is_healthy is True
        assert should_restart is False

    @patch.dict(os.environ, {"QA_BOT_ENABLED": "True", "QA_BOT_TOKEN": "test"})
    def test_check_health_not_running_should_restart(self):
        """测试未运行时的健康检查"""
        import core.process_manager

        core.process_manager._qa_bot_process = None

        is_healthy, should_restart, message = check_qa_bot_health()

        assert is_healthy is False
        assert should_restart is True


@pytest.mark.unit
class TestFormatUptime:
    """格式化运行时间测试"""

    def test_format_uptime_hours(self):
        """测试格式化小时"""
        result = format_uptime(3665)  # 1小时1分5秒

        assert "1小时" in result
        assert "1分钟" in result

    def test_format_uptime_minutes(self):
        """测试格式化分钟"""
        result = format_uptime(125)  # 2分5秒

        assert "2分钟" in result
        assert "5秒" in result

    def test_format_uptime_seconds(self):
        """测试格式化秒"""
        result = format_uptime(45)

        assert "45秒" in result

    def test_format_uptime_none(self):
        """测试None输入"""
        result = format_uptime(None)

        assert result == "未知"


if __name__ == "__main__":
    pytest.main([__file__, "-v"])
