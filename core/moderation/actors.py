# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
"""Telegram 消息身份与举报审计。群组身份始终与真实用户分开。"""

from dataclasses import dataclass

from telethon import utils
from telethon.tl.types import Message, PeerChannel, PeerUser

ANONYMOUS_ADMIN_NAME = "本群匿名管理员（真实身份不可见）"


@dataclass(frozen=True)
class TelegramActor:
    actor_type: str
    chat_id: int
    message_id: int | None
    user_id: int | None
    source: str

    @property
    def key(self):
        if self.actor_type == "anonymous_group_admin":
            return f"anonymous_group_admin:{self.chat_id}"
        return f"telegram:{self.user_id}"


def message_actor(message, chat_id, *, allow_edited=False):
    """原始 from_id 优先于兼容 sender_id，拒绝频道马甲和自动转发。"""
    if (
        message is None
        or getattr(message, "action", None)
        or getattr(message, "post", False)
        or (not allow_edited and getattr(message, "edit_date", None))
    ):
        return None
    peer = getattr(message, "from_id", None)
    destination = getattr(message, "peer_id", None)
    if destination is not None and utils.get_peer_id(destination) != chat_id:
        return None
    sender_id = getattr(message, "sender_id", None)
    implicit_sender = (
        peer is None
        and isinstance(message, Message)
        and isinstance(destination, PeerChannel)
        and sender_id in (None, chat_id)
    )
    if implicit_sender:
        # MTProto 省略 from_id 时，TDLib 使用消息所在 peer 作为发件人。
        peer = destination
    if isinstance(peer, PeerChannel):
        if (
            utils.get_peer_id(peer) != chat_id
            or not isinstance(destination, PeerChannel)
            or getattr(message, "fwd_from", None)
        ):
            return None
        return TelegramActor(
            "anonymous_group_admin",
            chat_id,
            getattr(message, "id", None),
            None,
            "telegram:peer_id" if implicit_sender else "telegram:peer_channel",
        )
    if isinstance(peer, PeerUser):
        if sender_id is not None and sender_id != peer.user_id:
            return None
        sender_id = peer.user_id
    elif peer is not None:
        return None
    if not isinstance(sender_id, int) or sender_id <= 0:
        return None
    return TelegramActor(
        "real_user", chat_id, getattr(message, "id", None), sender_id, "telegram:peer_user"
    )


def event_actor(event):
    message = event.message
    # Telethon 消息自带 sender_id；兼容只在事件层暴露 sender_id 的调用者。
    if getattr(message, "from_id", None) is None and not hasattr(message, "sender_id"):
        from types import SimpleNamespace

        message = SimpleNamespace(**vars(message), sender_id=event.sender_id)
    return message_actor(message, event.chat_id)


def reporter_snapshot(reporter, actor=None):
    anonymous = actor is not None and actor.actor_type == "anonymous_group_admin"
    payload = {
        "user_id": None if anonymous else reporter.id,
        "name": ANONYMOUS_ADMIN_NAME
        if anonymous
        else " ".join(
            value
            for value in (
                getattr(reporter, "first_name", None),
                getattr(reporter, "last_name", None),
            )
            if value
        )[:256],
        "username": None if anonymous else getattr(reporter, "username", None),
    }
    if actor is not None:
        payload.update(
            actor_type=actor.actor_type,
            actor_key=actor.key,
            chat_id=actor.chat_id,
            message_id=actor.message_id,
            source=actor.source,
        )
    return payload


def reporter_key(reporter):
    """旧样本通过真实 user_id 去重，新匿名样本通过共享群身份去重。"""
    return reporter.get("actor_key") or f"telegram:{reporter.get('user_id')}"
