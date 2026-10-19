# Copyright 2026 Sakura-Bot
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
"""有超时的 Telegram 操作，精确保留验证前的成员限制。"""

import asyncio
from datetime import UTC, datetime

from telethon import utils
from telethon.tl.functions.channels import EditBannedRequest
from telethon.tl.functions.messages import HideChatJoinRequestRequest, SendMessageRequest
from telethon.tl.types import ChatBannedRights, PeerUser

VERIFICATION_RESTRICTIONS = (
    "send_messages",
    "send_media",
    "send_plain",
    "send_stickers",
    "send_gifs",
    "send_games",
    "send_inline",
    "embed_links",
    "send_polls",
    "send_photos",
    "send_videos",
    "send_roundvideos",
    "send_audios",
    "send_voices",
    "send_docs",
)


def verification_rights(original):
    """明确保存本次验证请求的全部发言限制，不能被一次滞后的读取覆盖。"""
    values = dict(original)
    values.update({key: True for key in VERIFICATION_RESTRICTIONS})
    values["until_date"] = 0
    return values


def mute_rights(original, mute_until):
    values = dict(original)
    indefinite = not values.get("until_date") and any(
        value for key, value in values.items() if key != "until_date"
    )
    values.update(send_messages=True, send_media=True, send_plain=True)
    values["until_date"] = 0 if indefinite else max(original.get("until_date", 0), mute_until)
    return values


def comparable_rights(values):
    """归一化 Telegram 的永久日期、聚合禁言位和无实际限制的日期。"""
    result = rights_dict(None)
    result.update(values)
    until = result.get("until_date") or 0
    result["until_date"] = until
    now = datetime.now(UTC).timestamp()
    if until and until < now:
        return rights_dict(None)
    if until > now + 366 * 86400:
        result["until_date"] = 0
    if result.get("send_messages"):
        result.update({key: True for key in VERIFICATION_RESTRICTIONS})
    if not any(value for key, value in result.items() if key != "until_date"):
        result["until_date"] = 0
    return result


def same_rights(left, right):
    return comparable_rights(left) == comparable_rights(right)


async def call(awaitable):
    return await asyncio.wait_for(awaitable, timeout=25)


class TelegramActionError(RuntimeError):
    """仅表示 Telegram 副作用调用失败，与数据库提交失败区分。"""


async def mutate(awaitable):
    try:
        return await call(awaitable)
    except Exception as error:
        raise TelegramActionError(type(error).__name__) from error


def rights_dict(rights):
    if rights is None:
        rights = ChatBannedRights(until_date=None)
    values = rights.to_dict()
    values.pop("_", None)
    until = values.get("until_date")
    if isinstance(until, datetime):
        values["until_date"] = int(until.timestamp())
    # Telegram 永久权限的 until_date 可能是 None、0 或 epoch。
    values["until_date"] = values.get("until_date") or 0
    return {key: value if key == "until_date" else bool(value) for key, value in values.items()}


def rights_object(values):
    values = dict(values)
    until = values.pop("until_date", 0)
    if until and until < datetime.now(UTC).timestamp():
        return ChatBannedRights(until_date=None)
    return ChatBannedRights(
        until_date=datetime.fromtimestamp(until, UTC) if until else None, **values
    )


class TelegramActions:
    def __init__(self, client):
        self.client = client

    async def permissions(self, chat_id, user_id):
        return await call(self.client.get_permissions(chat_id, user_id))

    async def rights(self, chat_id, user_id):
        permissions = await self.permissions(chat_id, user_id)
        return rights_dict(getattr(permissions.participant, "banned_rights", None))

    async def set_rights(self, chat_id, user_id, values):
        await mutate(self.client(EditBannedRequest(chat_id, user_id, rights_object(values))))

    async def ban(self, chat_id, user_id):
        await mutate(self.client.edit_permissions(chat_id, user_id, view_messages=False))

    async def unban(self, chat_id, user_id):
        await mutate(self.client.edit_permissions(chat_id, user_id, view_messages=True))

    async def join_request(self, chat_id, user_id, approved):
        await mutate(self.client(HideChatJoinRequestRequest(chat_id, user_id, approved=approved)))

    async def input_user(self, user_id):
        return utils.get_input_user(await call(self.client.get_input_entity(PeerUser(user_id))))

    async def send_once(self, target_id, text, buttons, random_id, *, entities=None):
        # 固定 random_id 让同一投递在提交失败后重试时仍由 Telegram 去重。
        result = await mutate(
            self.client(
                SendMessageRequest(
                    peer=await call(self.client.get_input_entity(target_id)),
                    message=text,
                    random_id=random_id,
                    reply_markup=self.client.build_reply_markup(buttons),
                    entities=entities,
                    no_webpage=True,
                )
            )
        )
        if getattr(result, "id", None):
            return result.id
        for update in getattr(result, "updates", []):
            if getattr(update, "random_id", None) == random_id and getattr(update, "id", None):
                return update.id
            message = getattr(update, "message", None)
            if message is not None and getattr(message, "id", None):
                return message.id
        raise RuntimeError("Telegram did not return a notification message ID")
