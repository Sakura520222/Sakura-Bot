# Copyright 2026 Sakura-Bot
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
"""在主 Bot 和数据库就绪后注册群管，关闭时取消全部处理器和后台任务。"""

import logging

from core.config import ConfigChangedEvent, read_config
from core.config.persistence import ConfigPersistenceError
from core.moderation import set_guard
from core.moderation.appeals import AppealWorkflow
from core.moderation.moderator import Moderator
from core.moderation.repository import GuardRepository

logger = logging.getLogger(__name__)


class ModerationInitializer:
    def __init__(self, event_bus=None):
        self.event_bus = event_bus
        self.service = None

    async def initialize(self, client, database):
        try:
            repository = GuardRepository(database.pool)
            await repository.initialize()
            self.service = Moderator(client, repository)
            self.service.appeals = AppealWorkflow(self.service)
            try:
                config = read_config()
            except ConfigPersistenceError:
                logger.warning("Guard 未读取到有效配置，保持全部群组关闭")
                config = {}
            self.service.reload(config)
            await self.service.start()
        except Exception:
            logger.exception("Sakura Guard 初始化失败，群管未启用；主 Bot 其他功能继续启动")
            if self.service:
                await self.service.stop()
            self.service = None
            set_guard(None)
            return
        set_guard(self.service)
        if self.event_bus:
            await self.event_bus.subscribe(ConfigChangedEvent, self.on_config_changed)

    async def on_config_changed(self, event):
        if self.service:
            self.service.reload(event.config)

    async def shutdown(self):
        if self.event_bus:
            await self.event_bus.unsubscribe(ConfigChangedEvent, self.on_config_changed)
        if self.service:
            await self.service.stop()
        set_guard(None)
