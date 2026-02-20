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

"""自定义异常类

此模块定义了项目中使用的所有自定义异常类，以便更好地处理和分类错误。
"""


class BotError(Exception):
    """机器人基础异常类

    所有项目特定的异常都应继承自此类。
    """
    pass


class ConfigurationError(BotError):
    """配置错误

    当配置文件缺失、格式错误或包含无效值时抛出。
    """
    pass


class AIServiceError(BotError):
    """AI 服务错误

    当 AI API 调用失败时抛出。
    """
    pass


class TelegramAPIError(BotError):
    """Telegram API 错误

    当 Telegram API 调用失败时抛出。
    """
    pass


class ChannelNotFoundError(BotError):
    """频道未找到错误

    当指定的频道不存在或无法访问时抛出。
    """
    pass


class InvalidScheduleError(BotError):
    """无效的时间配置错误

    当时间调度配置无效时抛出。
    """
    pass


class PollGenerationError(BotError):
    """投票生成错误

    当投票生成失败时抛出。
    """
    pass


class DatabaseError(BotError):
    """数据库错误

    当数据库操作失败时抛出。
    """
    pass


class ValidationError(BotError):
    """验证错误

    当输入数据验证失败时抛出。
    """
    pass
