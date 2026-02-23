"""测试 Exceptions 模块

Copyright 2026 Sakura-Bot
本项目采用 AGPL-3.0 许可
"""

import pytest

from core.exceptions import (
    AIServiceError,
    BotError,
    ChannelNotFoundError,
    ConfigurationError,
    DatabaseError,
    InvalidScheduleError,
    PollGenerationError,
    TelegramAPIError,
    ValidationError,
)


@pytest.mark.unit
class TestBotError:
    """测试基础异常类"""

    def test_bot_error_is_exception(self):
        """测试BotError是Exception的子类"""
        assert issubclass(BotError, Exception)

    def test_bot_error_can_be_raised(self):
        """测试BotError可以被抛出"""
        with pytest.raises(BotError):
            raise BotError("Test error")

    def test_bot_error_stores_message(self):
        """测试BotError存储错误消息"""
        error = BotError("Test message")
        assert str(error) == "Test message"

    def test_bot_error_can_be_caught(self):
        """测试BotError可以被捕获"""
        try:
            raise BotError("Test")
        except BotError as e:
            assert str(e) == "Test"


@pytest.mark.unit
class TestConfigurationError:
    """测试配置错误"""

    def test_configuration_error_inherits_bot_error(self):
        """测试ConfigurationError继承自BotError"""
        assert issubclass(ConfigurationError, BotError)

    def test_configuration_error_can_be_raised(self):
        """测试ConfigurationError可以被抛出"""
        with pytest.raises(ConfigurationError):
            raise ConfigurationError("Invalid config")


@pytest.mark.unit
class TestAIServiceError:
    """测试AI服务错误"""

    def test_ai_service_error_inherits_bot_error(self):
        """测试AIServiceError继承自BotError"""
        assert issubclass(AIServiceError, BotError)

    def test_ai_service_error_can_be_raised(self):
        """测试AIServiceError可以被抛出"""
        with pytest.raises(AIServiceError):
            raise AIServiceError("API failed")


@pytest.mark.unit
class TestTelegramAPIError:
    """测试Telegram API错误"""

    def test_telegram_api_error_inherits_bot_error(self):
        """测试TelegramAPIError继承自BotError"""
        assert issubclass(TelegramAPIError, BotError)

    def test_telegram_api_error_can_be_raised(self):
        """测试TelegramAPIError可以被抛出"""
        with pytest.raises(TelegramAPIError):
            raise TelegramAPIError("Telegram failed")


@pytest.mark.unit
class TestChannelNotFoundError:
    """测试频道未找到错误"""

    def test_channel_not_found_error_inherits_bot_error(self):
        """测试ChannelNotFoundError继承自BotError"""
        assert issubclass(ChannelNotFoundError, BotError)

    def test_channel_not_found_error_can_be_raised(self):
        """测试ChannelNotFoundError可以被抛出"""
        with pytest.raises(ChannelNotFoundError):
            raise ChannelNotFoundError("Channel not found")


@pytest.mark.unit
class TestInvalidScheduleError:
    """测试无效调度错误"""

    def test_invalid_schedule_error_inherits_bot_error(self):
        """测试InvalidScheduleError继承自BotError"""
        assert issubclass(InvalidScheduleError, BotError)

    def test_invalid_schedule_error_can_be_raised(self):
        """测试InvalidScheduleError可以被抛出"""
        with pytest.raises(InvalidScheduleError):
            raise InvalidScheduleError("Invalid schedule")


@pytest.mark.unit
class TestPollGenerationError:
    """测试投票生成错误"""

    def test_poll_generation_error_inherits_bot_error(self):
        """测试PollGenerationError继承自BotError"""
        assert issubclass(PollGenerationError, BotError)

    def test_poll_generation_error_can_be_raised(self):
        """测试PollGenerationError可以被抛出"""
        with pytest.raises(PollGenerationError):
            raise PollGenerationError("Poll generation failed")


@pytest.mark.unit
class TestDatabaseError:
    """测试数据库错误"""

    def test_database_error_inherits_bot_error(self):
        """测试DatabaseError继承自BotError"""
        assert issubclass(DatabaseError, BotError)

    def test_database_error_can_be_raised(self):
        """测试DatabaseError可以被抛出"""
        with pytest.raises(DatabaseError):
            raise DatabaseError("Database failed")


@pytest.mark.unit
class TestValidationError:
    """测试验证错误"""

    def test_validation_error_inherits_bot_error(self):
        """测试ValidationError继承自BotError"""
        assert issubclass(ValidationError, BotError)

    def test_validation_error_can_be_raised(self):
        """测试ValidationError可以被抛出"""
        with pytest.raises(ValidationError):
            raise ValidationError("Validation failed")


@pytest.mark.unit
class TestExceptionHierarchy:
    """测试异常层次结构"""

    def test_all_exceptions_inherit_from_bot_error(self):
        """测试所有异常都继承自BotError"""
        exceptions = [
            ConfigurationError,
            AIServiceError,
            TelegramAPIError,
            ChannelNotFoundError,
            InvalidScheduleError,
            PollGenerationError,
            DatabaseError,
            ValidationError,
        ]

        for exc in exceptions:
            assert issubclass(exc, BotError), f"{exc.__name__} should inherit from BotError"

    def test_all_exceptions_can_be_caught_as_bot_error(self):
        """测试所有异常都可以作为BotError被捕获"""
        exceptions = [
            ConfigurationError,
            AIServiceError,
            TelegramAPIError,
            ChannelNotFoundError,
            InvalidScheduleError,
            PollGenerationError,
            DatabaseError,
            ValidationError,
        ]

        for exc in exceptions:
            try:
                raise exc("Test")
            except BotError:
                pass  # 成功捕获
            else:
                pytest.fail(f"{exc.__name__} should be caught as BotError")


if __name__ == "__main__":
    pytest.main([__file__, "-v"])
