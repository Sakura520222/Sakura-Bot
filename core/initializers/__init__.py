# Copyright 2026 Sakura-Bot
#
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
#
# - 署名：必须提供本项目的原始来源链接
# - 相同方式共享：衍生作品必须采用相同的许可证
#
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
# 许可证全文：参见 LICENSE 文件

"""
初始化器模块

提供各个功能模块的初始化逻辑，从 main.py 中分离出来，提高代码可维护性。
"""

from .command_registrar import CommandRegistrar
from .comment_chat_initializer import CommentChatInitializer
from .comment_welcome_initializer import CommentWelcomeInitializer
from .communication_initializer import CommunicationInitializer
from .database_initializer import DatabaseInitializer
from .forwarding_initializer import ForwardingInitializer
from .scheduler_initializer import SchedulerInitializer
from .startup_notifier import StartupNotifier
from .userbot_initializer import UserBotInitializer

__all__ = [
    "CommandRegistrar",
    "DatabaseInitializer",
    "SchedulerInitializer",
    "UserBotInitializer",
    "ForwardingInitializer",
    "CommentWelcomeInitializer",
    "CommentChatInitializer",
    "CommunicationInitializer",
    "StartupNotifier",
]
