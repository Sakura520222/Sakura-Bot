# Copyright 2026 Sakura-Bot
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
"""用现有 UserBot 补检 Bot API 不投递的其他机器人消息；处罚始终由主 Bot 执行。"""

import logging

from telethon import events
from telethon.tl.types import InputPeerUser

from .guest_messages import guest_actor
from .telegram import call

logger = logging.getLogger(__name__)


class BotMessageObserver:
    def __init__(self, guard):
        self.guard = guard
        self.client = None
        self.handlers = []

    def attach(self, client):
        self.detach()
        if client is None or client is self.guard.client:
            return
        self.client = client
        for builder in (events.NewMessage(incoming=True), events.MessageEdited(incoming=True)):
            client.add_event_handler(self.handle, builder)
            self.handlers.append(builder)
        logger.info("Guard 已启用 UserBot 机器人消息补检；仅覆盖 UserBot 可接收消息的受管群")

    def detach(self):
        if self.client:
            for builder in self.handlers:
                self.client.remove_event_handler(self.handle, builder)
        self.handlers.clear()
        self.client = None

    async def handle(self, event):
        guard = self.guard
        if (
            not event.is_group
            or not guard.group(event.chat_id).enabled
            or not event.sender_id
            or event.sender_id <= 0
            or event.sender_id == guard.bot_id
        ):
            return
        try:
            if guest_actor(event.message):
                await guard.handle_message(event)
                return
            sender = await call(event.get_sender())
            if not sender or sender.id != event.sender_id or not getattr(sender, "bot", False):
                return
            # 通过主 Bot 的 Telegram 查询获取自身可用的实体和权限，不能复用 UserBot access_hash。
            resolved = await call(guard.client.get_entity(InputPeerUser(sender.id, 0)))
            if resolved.id != sender.id or not getattr(resolved, "bot", False):
                return
            await guard.handle_message(event)
        except events.StopPropagation:
            # 只消费主 Bot 的审核结果，不影响 UserBot 其余监听器。
            pass
        except Exception as error:
            logger.warning(
                "Guard 机器人补检失败 chat=%s message=%s error=%s",
                event.chat_id,
                event.id,
                type(error).__name__,
            )
