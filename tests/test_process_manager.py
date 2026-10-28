"""测试 Process Manager 模块

Copyright 2026 Sakura-Bot
本项目采用 AGPL-3.0 许可
"""

import os
import time
from unittest.mock import MagicMock, patch

import pytest

import core.system.process_manager as process_manager
from core.system.process_manager import (
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
    @patch("core.system.process_manager.subprocess.Popen")
    def test_start_qa_bot_success(self, mock_popen):
        """测试成功启动QA Bot"""
        mock_process = MagicMock()
        mock_process.pid = 12345
        mock_popen.return_value = mock_process

        result = start_qa_bot()

        assert result["success"] is True
        assert result["pid"] == 12345
        mock_popen.assert_called_once()
        _, kwargs = mock_popen.call_args
        assert kwargs["env"]["SAKURA_LOG_COMPONENT"] == "qa"
        assert kwargs["env"]["SAKURA_COMPONENT_LOG_FILE"] == "qa-bot.log"
        assert kwargs["env"]["QA_BOT_TOKEN"] == "test_token"

    @patch.dict(os.environ, {"QA_BOT_ENABLED": "False"})
    def test_start_qa_bot_disabled(self):
        """测试QA Bot未启用"""
        # 重置全局变量
        process_manager._qa_bot_process = None

        result = start_qa_bot()

        assert result["success"] is False
        assert result["pid"] is None

    @patch.dict(os.environ, {"QA_BOT_ENABLED": "True", "QA_BOT_TOKEN": ""})
    def test_start_qa_bot_no_token(self):
        """测试未配置token"""
        process_manager._qa_bot_process = None

        result = start_qa_bot()

        assert result["success"] is False
        assert "token" in result["message"].lower()

    @patch.dict(os.environ, {"QA_BOT_ENABLED": "True", "QA_BOT_TOKEN": "test"})
    def test_start_qa_bot_already_running(self):
        """测试QA Bot已在运行"""
        mock_process = MagicMock()
        mock_process.pid = 12345
        process_manager._qa_bot_process = mock_process

        result = start_qa_bot()

        assert result["success"] is False
        assert result["pid"] == 12345


@pytest.mark.unit
class TestStopQABot:
    """停止QA Bot测试"""

    def test_stop_qa_bot_success(self):
        """测试成功停止QA Bot"""
        mock_process = MagicMock()
        mock_process.pid = 12345
        mock_process.wait.return_value = None
        process_manager._qa_bot_process = mock_process

        result = stop_qa_bot()

        assert result["success"] is True
        mock_process.terminate.assert_called_once()
        assert process_manager._qa_bot_process is None

    def test_stop_qa_bot_not_running(self):
        """测试停止未运行的QA Bot"""
        process_manager._qa_bot_process = None
        process_manager._qa_bot_start_time = None

        result = stop_qa_bot()

        assert result["success"] is True


@pytest.mark.unit
class TestGetQABotProcess:
    """获取QA Bot进程测试"""

    def test_get_qa_bot_process(self):
        """测试获取进程引用"""
        mock_process = MagicMock()
        process_manager._qa_bot_process = mock_process

        result = get_qa_bot_process()

        assert result is mock_process


@pytest.mark.unit
class TestRestartQABot:
    """重启QA Bot测试"""

    @patch.dict(os.environ, {"QA_BOT_ENABLED": "True", "QA_BOT_TOKEN": "test"})
    @patch("core.system.process_manager.subprocess.Popen")
    @patch("core.system.process_manager.time.sleep")
    def test_restart_qa_bot_success(self, mock_sleep, mock_popen):
        """测试成功重启QA Bot"""
        mock_process = MagicMock()
        mock_process.pid = 12345
        mock_process.wait.return_value = None
        process_manager._qa_bot_process = mock_process
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
        mock_process = MagicMock()
        mock_process.pid = 12345
        mock_process.poll.return_value = None  # 进程运行中
        process_manager._qa_bot_process = mock_process
        process_manager._qa_bot_start_time = time.time() - 100

        status = get_qa_bot_status()

        assert status["running"] is True
        assert status["pid"] == 12345
        assert status["enabled"] is True
        assert status["token_configured"] is True
        assert status["uptime_seconds"] >= 100

    @patch.dict(os.environ, {"QA_BOT_ENABLED": "True", "QA_BOT_TOKEN": "test_token"})
    def test_get_status_not_running(self):
        """测试获取未运行状态"""
        process_manager._qa_bot_process = None
        process_manager._qa_bot_start_time = None

        status = get_qa_bot_status()

        assert status["running"] is False
        assert status["pid"] is None
        assert status["enabled"] is True

    @patch.dict(os.environ, {"QA_BOT_ENABLED": "False"})
    def test_get_status_disabled(self):
        """测试获取禁用状态"""
        process_manager._qa_bot_process = None

        status = get_qa_bot_status()

        assert status["enabled"] is False

    @patch.dict(os.environ, {"QA_BOT_ENABLED": "True", "QA_BOT_TOKEN": ""})
    def test_get_status_no_token(self):
        """测试未配置token状态"""
        process_manager._qa_bot_process = None

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
        process_manager._qa_bot_process = None

        is_healthy, should_restart, message = check_qa_bot_health()

        assert is_healthy is False
        assert should_restart is False

    @patch.dict(os.environ, {"QA_BOT_ENABLED": "True", "QA_BOT_TOKEN": "test"})
    def test_check_health_running(self):
        """测试运行中的健康检查"""
        mock_process = MagicMock()
        mock_process.pid = 12345
        mock_process.poll.return_value = None
        process_manager._qa_bot_process = mock_process
        process_manager._qa_bot_start_time = time.time()

        is_healthy, should_restart, message = check_qa_bot_health()

        assert is_healthy is True
        assert should_restart is False

    @patch.dict(os.environ, {"QA_BOT_ENABLED": "True", "QA_BOT_TOKEN": "test"})
    def test_check_health_not_running_should_restart(self):
        """测试未运行时的健康检查"""
        process_manager._qa_bot_process = None

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


@pytest.mark.unit
class TestQARuntimeResolution:
    """问答Bot运行时解析与切换测试"""

    def setup_method(self):
        process_manager._qa_bot_process = None
        process_manager._qa_bot_runtime = "python"

    @patch.dict(os.environ, {"QA_BOT_RUNTIME": ""}, clear=False)
    def test_effective_runtime_default_python(self):
        with patch("core.config.runtime.runtime_config") as rc:
            rc.read.return_value = {}
            assert process_manager.effective_qa_runtime() == "python"

    @patch.dict(os.environ, {"QA_BOT_RUNTIME": "go"})
    def test_effective_runtime_from_env(self):
        with patch("core.config.runtime.runtime_config") as rc:
            rc.read.return_value = {}
            assert process_manager.effective_qa_runtime() == "go"

    @patch.dict(os.environ, {"QA_BOT_RUNTIME": "python"})
    def test_effective_runtime_config_overrides_env(self):
        with patch("core.config.runtime.runtime_config") as rc:
            rc.read.return_value = {"qa_bot_runtime": "go"}
            assert process_manager.effective_qa_runtime() == "go"

    @patch.dict(os.environ, {"QA_BOT_RUNTIME": "python"})
    def test_effective_runtime_invalid_values_fall_back(self):
        with patch("core.config.runtime.runtime_config") as rc:
            rc.read.return_value = {"qa_bot_runtime": "rust"}
            assert process_manager.effective_qa_runtime() == "python"

    @patch.dict(os.environ, {"QA_BOT_TOKEN": "t", "QA_BOT_ENABLED": "True", "QA_BOT_RUNTIME": "go"})
    @patch("core.system.process_manager.subprocess.Popen")
    def test_start_qa_bot_go_uses_binary(self, mock_popen):
        with patch("core.config.runtime.runtime_config") as rc:
            rc.read.return_value = {}
            mock_process = MagicMock()
            mock_process.pid = 777
            mock_popen.return_value = mock_process
            with patch.object(process_manager, "_resolve_qa_binary", return_value="/bin/sakura-qa"):
                result = process_manager.start_qa_bot()
            assert result["success"] is True
            args, kwargs = mock_popen.call_args
            assert args[0] == ["/bin/sakura-qa"]
            assert kwargs["env"].get("GOMEMLIMIT") == "96MiB"
            assert process_manager._qa_bot_runtime == "go"

    def test_status_includes_runtime_fields(self):
        with patch("core.config.runtime.runtime_config") as rc:
            rc.read.return_value = {"qa_bot_runtime": "go"}
            status = process_manager.get_qa_bot_status()
            assert status["configured_runtime"] == "go"
            assert "runtime" in status

    @patch.dict(os.environ, {"QA_BOT_RUNTIME": "python"})
    def test_set_runtime_invalid_rejected(self):
        with patch("core.config.runtime.runtime_config") as rc:
            rc.read.return_value = {}
            result = process_manager.set_qa_bot_runtime("rust")
            assert result["success"] is False
            assert result["runtime"] == "python"

    @patch.dict(os.environ, {"QA_BOT_RUNTIME": "python"})
    def test_set_runtime_writes_config_and_skips_restart_when_stopped(self):
        with patch("core.config.runtime.runtime_config") as rc:
            rc.read.return_value = {}
            with patch("core.config.update_config_fields") as mock_write:
                with patch.object(process_manager, "restart_qa_bot") as mock_restart:
                    result = process_manager.set_qa_bot_runtime("go")
            assert result["success"] is True
            mock_write.assert_called_once_with({"qa_bot_runtime": "go"})
            # 未运行时不触发重启，下次启动自然生效
            mock_restart.assert_not_called()
