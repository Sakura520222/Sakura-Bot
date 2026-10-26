# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
"""Participant_id_invalid 后逐页扫描，逐项核对作者，不信任搜索过滤。"""

from types import SimpleNamespace
from unittest.mock import AsyncMock

import pytest
from telethon.tl.types import PeerChannel, PeerUser

from core.infrastructure.telegram_bot_api import BadRequestError

from .conftest import ADMIN, CHAT
from .test_deleted_cleanup import deleted as deleted
from .test_deleted_cleanup import drain


def message(mid, user, guest=None, chat=CHAT):
    return SimpleNamespace(
        id=mid,
        peer_id=PeerChannel(abs(chat) - 1000000000000),
        from_id=PeerUser(user),
        guestchat_via_from=PeerUser(guest) if guest else None,
    )


@pytest.fixture
def history(deleted):
    rows = [message(100, 201), message(99, 200), message(98, 777, guest=201)]
    pages = [rows, []]

    async def rpc(request, **kwargs):
        return SimpleNamespace(messages=pages.pop(0))

    deleted.guard.bot_observer.client = SimpleNamespace(
        get_input_entity=AsyncMock(return_value=PeerChannel(abs(CHAT) - 1000000000000)),
        __call__=rpc,
    )

    class Reader:
        async def get_input_entity(self, source):
            return PeerChannel(abs(CHAT) - 1000000000000)

        async def __call__(self, request, **kwargs):
            return await rpc(request, **kwargs)

    deleted.guard.bot_observer.client = Reader()
    deleted.guard.ban_history.revoke.side_effect = BadRequestError("Participant_id_invalid")
    originals = {m.id: m for m in rows}

    async def get_messages(chat_id, *, ids):
        if isinstance(ids, list):
            return [
                None if (chat_id, mid) in deleted.guard.client.deleted else originals.get(mid)
                for mid in ids
            ]
        return None if (chat_id, ids) in deleted.guard.client.deleted else originals.get(ids)

    deleted.guard.client.get_messages = get_messages
    return rows


async def test_unresolvable_deleted_account_falls_back_to_verified_full_history(deleted, history):
    import uuid

    await deleted.start(ADMIN, uuid.uuid4().hex, [CHAT])
    result = await drain(deleted)
    assert result["status"] == "completed"
    assert (CHAT, 100) in deleted.guard.client.deleted
    assert (CHAT, 98) in deleted.guard.client.deleted
    assert (CHAT, 99) not in deleted.guard.client.deleted
    assert result["groups"][0]["history_scanned"] == 3
    assert result["groups"][0]["history_deleted"] == 2


async def test_history_author_mismatch_is_not_deleted(deleted, history):
    import uuid

    original = deleted.guard.client.get_messages

    async def changed(chat_id, *, ids):
        return (
            [message(mid, 200) for mid in ids]
            if isinstance(ids, list)
            else await original(chat_id, ids=ids)
        )

    deleted.guard.client.get_messages = changed
    await deleted.start(ADMIN, uuid.uuid4().hex, [CHAT])
    result = await drain(deleted)
    assert result["status"] == "partial"
    assert not deleted.guard.client.deleted
