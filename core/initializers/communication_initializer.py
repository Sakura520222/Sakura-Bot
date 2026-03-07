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
跨Bot通信初始化器

负责初始化主Bot与QA Bot之间的通信机制。
"""

import logging
from typing import TYPE_CHECKING

if TYPE_CHECKING:
    from telethon import TelegramClient

from core.mainbot_push_handler import get_mainbot_push_handler
from core.mainbot_request_handler import get_mainbot_request_handler


class CommunicationInitializer:
    """跨Bot通信初始化器"""

    def __init__(self):
        self.logger = logging.getLogger(__name__)

    async def initialize(self, client: "TelegramClient") -> None:
        """初始化跨Bot通信处理器

        Args:
            client: Telegram客户端实例
        """
        self.logger.info("初始化跨Bot通信处理器...")

        try:
            # 初始化请求处理器
            get_mainbot_request_handler()

            # 初始化推送处理器
            get_mainbot_push_handler()

            self.logger.info("跨Bot通信处理器初始化完成")

        except Exception as e:
            self.logger.error(f"初始化跨Bot通信处理器失败: {type(e).__name__}: {e}")
