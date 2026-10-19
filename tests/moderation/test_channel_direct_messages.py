# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
"""频道私信不作为独立加入群组，不能用其普通成员身份生成退出名单。"""

import time
from datetime import UTC, datetime
from types import SimpleNamespace
from unittest.mock import AsyncMock

import pytest
from telethon import utils
from telethon.tl.types import Channel

from core.moderation.chat_cleanup import ChatCleanup
from core.moderation.config import GuardGroup
from core.moderation.groups import GroupDirectory
from core.moderation.moderator import GuardError

from .conftest import ADMIN
from .test_chat_cleanup import CleanupClient

PARENT_ID = 123456789
PARENT = utils.get_peer_id(Channel(PARENT_ID, "频道", None, datetime.now(UTC)))
DM_ID = 1072000000123
DM = -(1000000000000 + DM_ID)


def entities(*, parent_link=True):
    parent = Channel(
        id=PARENT_ID,
        title="频道",
        photo=None,
        date=datetime.now(UTC),
        broadcast=True,
        broadcast_messages_allowed=True,
        linked_monoforum_id=DM_ID,
    )
    direct = Channel(
        id=DM_ID,
        title="频道 Messages",
        photo=None,
        date=datetime.now(UTC),
        megagroup=True,
        monoforum=True,
        linked_monoforum_id=PARENT_ID if parent_link else None,
    )
    return parent, direct


async def test_channel_admin_and_its_member_direct_messages_never_become_cleanup_targets():
    parent, direct = entities()
    client = CleanupClient()
    client.chats.update({PARENT: parent, DM: direct})
    client.members.update({PARENT, DM})
    client.admins.add(PARENT)
    permissions = AsyncMock(wraps=client.get_permissions)
    client.get_permissions = permissions
    directory = GroupDirectory(client, lambda: [PARENT, DM], lambda: 999)
    directory.refresh(force=True)
    await directory.task
    service = ChatCleanup(directory, lambda actor: actor == ADMIN)
    try:
        snapshot = directory.snapshot()
        assert [row["chat_id"] for row in snapshot["chats"]] == [PARENT]
        assert snapshot["chats"][0]["is_admin"] is True
        assert snapshot["chats"][0]["chat_type"] == "channel"
        assert not snapshot["groups"]
        assert snapshot["related_chat_count"] == 1
        row = snapshot["metadata"][str(DM)]
        assert row["chat_type"] == "channel_dm"
        assert row["parent_chat_id"] == PARENT
        assert row["identity_verified"] is True
        assert row["is_standalone"] is False
        assert not directory.is_group(direct)
        assert not directory.can_leave(row)
        permissions.assert_awaited_once_with(PARENT, 999)
        preview = service.preview(ADMIN)
        assert preview["chats"] == []
        assert preview["total"] == preview["unknown_count"] == 0
        assert preview["admin_count"] == 1
        assert not client.requests
    finally:
        await service.stop()
        await directory.stop()


@pytest.mark.parametrize("parent_link", [True, False])
async def test_direct_messages_are_never_left_even_when_parent_is_unknown(parent_link):
    _, direct = entities(parent_link=parent_link)
    client = CleanupClient()
    client.chats[DM] = direct
    client.members.add(DM)
    client.get_permissions = AsyncMock()
    directory = GroupDirectory(client, lambda: [], lambda: 999)
    service = ChatCleanup(directory, lambda actor: True)
    result = await service._process(DM)
    assert result[0] == "skipped" and "频道私信" in result[1]
    assert not client.requests
    client.get_permissions.assert_not_awaited()
    assert not directory.snapshot()["chats"]
    assert str(PARENT) in directory.snapshot()["metadata"] if parent_link else True
    await service.stop()
    await directory.stop()


async def test_old_preview_with_misclassified_direct_messages_is_rechecked_and_skipped():
    _, direct = entities()
    client = CleanupClient()
    client.chats[DM] = direct
    client.members.add(DM)
    directory = GroupDirectory(client, lambda: [], lambda: 999)
    directory.remember(DM, direct.title)
    directory.entries[DM].update(
        chat_type="supergroup",
        joined=True,
        is_admin=False,
        role="member",
        identity_verified=True,
        is_standalone=True,
        checked_at=time.monotonic(),
    )
    directory.last_scan = time.monotonic()
    service = ChatCleanup(directory, lambda actor: True)
    preview = service.preview(ADMIN)
    assert preview["total"] == 1
    service.start(preview["token"], ADMIN)
    await service.task
    assert service.status()["results"][0]["status"] == "skipped"
    assert "频道私信" in service.status()["results"][0]["message"]
    assert not client.requests
    await service.stop()
    await directory.stop()


@pytest.mark.parametrize("invalid", ["wrong_id", "missing_id", "user"])
async def test_entity_identity_mismatch_never_checks_permissions_or_leaves(invalid):
    parent, _ = entities()
    client = CleanupClient()
    wrong = Channel(PARENT_ID + 1, "其他频道", None, datetime.now(UTC), broadcast=True)
    if invalid == "missing_id":
        wrong = SimpleNamespace(title="看似正常的群", megagroup=True)
    elif invalid == "user":
        from telethon.tl.types import User

        wrong = User(PARENT_ID)
    client.get_entity = AsyncMock(return_value=wrong)
    client.get_permissions = AsyncMock()
    directory = GroupDirectory(client, lambda: [], lambda: 999)
    service = ChatCleanup(directory, lambda actor: True)
    result = await service._process(utils.get_peer_id(parent))
    row = directory.entries[PARENT]
    assert result[0] == "skipped"
    assert row["error"] == "ChatIdentityMismatch"
    assert row["identity_verified"] is False
    assert row["is_admin"] is None and row["joined"] is None
    client.get_permissions.assert_not_awaited()
    assert not client.requests
    assert not directory.snapshot()["chats"]
    await service.stop()
    await directory.stop()


async def test_group_configuration_rejects_channel_direct_messages(guard):
    _, direct = entities()
    guard.client.get_entity = AsyncMock(return_value=direct)
    guard.client.get_permissions = AsyncMock()
    with pytest.raises(GuardError, match="supergroup_required"):
        await guard.validate_group(DM, GuardGroup(enabled=True))
    guard.client.get_permissions.assert_not_awaited()
