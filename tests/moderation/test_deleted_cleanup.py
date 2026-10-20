# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
import time
import uuid
from datetime import timedelta

import pytest
from telethon.errors import FloodWaitError
from telethon.tl.functions.channels import EditBannedRequest, GetParticipantsRequest
from telethon.tl.functions.users import GetUsersRequest
from telethon.tl.types import (
    ChannelParticipant,
    ChannelParticipantBanned,
    ChannelParticipantCreator,
    ChannelParticipantLeft,
    ChatAdminRights,
    ChatBannedRights,
    InputPeerUser,
    PeerChannel,
    PeerUser,
)

from core.infrastructure.database.mysql import utc_now_naive
from core.moderation.chat_cleanup import ChatCleanupError
from core.moderation.deleted_cleanup import DeletedUserCleanup, participant_user_id

from .conftest import ADMIN, CHAT, USER
from .deleted_cleanup_fakes import BASIC, CHANNEL, DeletedClient, MemoryCleanupRepository


@pytest.fixture
async def deleted(guard):
    guard.client = DeletedClient()
    guard.directory.client = guard.client
    guard.bot_id = 999
    for chat_id in guard.client.chats:
        await guard.directory.check(chat_id)
    guard.directory.last_scan = time.monotonic()
    guard.directory.total = guard.directory.checked = len(guard.client.chats)
    service = DeletedUserCleanup(guard, MemoryCleanupRepository())
    guard.deleted_cleanup = service
    return service


async def drain(service, maximum=50):
    for _ in range(maximum):
        async with service.repository.read() as store:
            active = await store.active(service.guard.bot_id)
            if not active:
                return (await service.status())["job"]
            active["retry_at"] = utc_now_naive()
            await store.save(active)
        await service.step()
    raise AssertionError("Cleanup failed to finish within bounded steps")


async def test_one_click_cleans_only_deleted_members_in_managed_groups(deleted, store):
    store.white.add((CHAT, 201))
    store.black[201] = {"user_id": 201}
    before_white, before_black = store.white.copy(), store.black.copy()
    job = await deleted.start(ADMIN, uuid.uuid4().hex)
    assert {row["chat_id"] for row in job["groups"]} == {CHAT, BASIC}
    assert not deleted.guard.client.removals
    result = await drain(deleted)
    assert result["status"] == "completed"
    assert set(deleted.guard.client.removals) == {(CHAT, 201), (BASIC, 201)}
    assert result["counts"]["removed"] == 2
    assert {200, 202, 203, 999} <= deleted.guard.client.members[CHAT]
    assert deleted.guard.client.members[CHANNEL] == {201}
    assert store.white == before_white and store.black == before_black
    assert not deleted.directory.mutating
    assert all("access_hash" not in row for row in result["results"])


async def test_pagination_finishes_before_mutating_members(deleted):
    deleted.guard.client.page_size = 2
    await deleted.start(ADMIN, uuid.uuid4().hex, [CHAT])
    await deleted.step()
    await deleted.step()
    assert not deleted.guard.client.removals
    await deleted.step()
    assert not deleted.guard.client.removals
    result = await drain(deleted)
    assert result["groups"][0]["scanned"] == 5
    assert deleted.guard.client.removals == [(CHAT, 201)]


async def test_fresh_deleted_flag_is_required_even_when_list_snapshot_says_deleted(deleted):
    await deleted.start(ADMIN, uuid.uuid4().hex, [CHAT])
    await deleted.step()
    deleted.guard.client.users[201].deleted = False
    result = await drain(deleted)
    assert result["counts"]["skipped"] == 2
    assert not deleted.guard.client.removals


@pytest.mark.parametrize("change", ["actor", "bot", "identity"])
async def test_permissions_are_rechecked_before_each_removal(deleted, change):
    await deleted.start(ADMIN, uuid.uuid4().hex, [CHAT])
    await deleted.step()
    if change == "actor":
        deleted.guard.client.after_profile = lambda: setattr(
            deleted.guard, "default_admins", lambda: []
        )
    elif change == "bot":
        deleted.guard.client.after_profile = lambda: setattr(
            deleted.guard.client, "ban_users", False
        )
    else:
        deleted.guard.client.after_profile = lambda: setattr(deleted.guard, "bot_id", 998)
    await deleted.step()
    assert not deleted.guard.client.removals


@pytest.mark.parametrize(
    "request_type", [GetParticipantsRequest, GetUsersRequest, EditBannedRequest]
)
async def test_flood_wait_is_durable_and_does_not_repeat_successful_removals(deleted, request_type):
    deleted.guard.client.failures[request_type] = [FloodWaitError(request=None, capture=30)]
    await deleted.start(ADMIN, uuid.uuid4().hex, [CHAT])
    for _ in range(4):
        await deleted.step()
        if (await deleted.status())["job"]["status"] == "waiting":
            break
    result = (await deleted.status())["job"]
    assert result["status"] == "waiting"
    assert result["retry_at"] > result["created_at"]
    restarted = DeletedUserCleanup(deleted.guard, deleted.repository)
    await deleted.stop()
    result = await drain(restarted)
    assert result["status"] == "completed"
    assert deleted.guard.client.removals == [(CHAT, 201)]


async def test_restart_after_removal_with_lost_response_reconciles_without_second_ban(deleted):
    await deleted.start(ADMIN, uuid.uuid4().hex, [CHAT])
    await deleted.step()
    deleted.guard.client.fail_after_remove = True
    await deleted.step()
    assert (await deleted.status())["job"]["status"] == "waiting"
    await deleted.stop()
    restarted = DeletedUserCleanup(deleted.guard, deleted.repository)
    result = await drain(restarted)
    assert result["counts"]["removed"] == 1
    assert deleted.guard.client.removals == [(CHAT, 201)]


async def test_only_one_job_and_repeated_request_id_is_idempotent(deleted):
    request = uuid.uuid4().hex
    first = await deleted.start(ADMIN, request, [CHAT])
    assert (await deleted.start(ADMIN, request, [CHAT]))["id"] == first["id"]
    with pytest.raises(ChatCleanupError):
        await deleted.start(ADMIN, uuid.uuid4().hex, [BASIC])
    await drain(deleted)
    assert (await deleted.start(ADMIN, request, [CHAT]))["id"] == first["id"]
    with pytest.raises(ChatCleanupError):
        await deleted.start(ADMIN, request, [BASIC])


async def test_normal_member_and_group_reviewer_cannot_start(deleted):
    deleted.guard.group(CHAT).reviewer_ids = [USER]
    with pytest.raises(ChatCleanupError):
        await deleted.start(USER, uuid.uuid4().hex, [CHAT])
    assert not deleted.repository.store.jobs


@pytest.mark.parametrize("scope", [[CHANNEL], [CHAT - 100]])
async def test_channels_and_unknown_groups_are_rejected(deleted, scope):
    with pytest.raises(ChatCleanupError):
        await deleted.start(ADMIN, uuid.uuid4().hex, scope)
    assert not deleted.repository.store.jobs


async def test_other_cleanup_and_stale_directory_block_new_jobs(deleted):
    deleted.directory.mutating = True
    with pytest.raises(ChatCleanupError):
        await deleted.start(ADMIN, uuid.uuid4().hex)
    deleted.directory.mutating = False
    deleted.directory.last_scan = time.monotonic() - 301
    with pytest.raises(ChatCleanupError):
        await deleted.start(ADMIN, uuid.uuid4().hex)
    await deleted.directory.stop()


async def test_periodic_default_off_then_enable_run_once_and_respect_interval_and_restart(deleted):
    await deleted.step()
    assert not deleted.repository.store.jobs
    deleted.settings.enabled = True
    await deleted.step()
    result = await drain(deleted)
    assert result["trigger_type"] == "scheduled"
    await deleted.step()
    assert len(deleted.repository.store.jobs) == 1
    restarted = DeletedUserCleanup(deleted.guard, deleted.repository)
    await restarted.step()
    assert len(deleted.repository.store.jobs) == 1
    previous = deleted.repository.store.jobs[result["id"]]
    previous["created_at"] -= timedelta(hours=25)
    await restarted.step()
    assert len(deleted.repository.store.jobs) == 2
    deleted.settings.enabled = False
    await restarted.step()
    assert (await restarted.status())["job"]["status"] == "cancelled"
    assert not deleted.directory.mutating


async def test_incomplete_participant_list_is_reported_as_partial(deleted):
    deleted.guard.client.page_size = 2
    deleted.guard.client.partial = True
    await deleted.start(ADMIN, uuid.uuid4().hex, [CHAT])
    result = await drain(deleted)
    assert result["status"] == "partial"
    assert "部分成员" in result["groups"][0]["error"]


async def test_repeated_profile_timeouts_are_bounded_and_reported(deleted):
    deleted.guard.client.failures[GetUsersRequest] = [TimeoutError()] * 6
    await deleted.start(ADMIN, uuid.uuid4().hex, [CHAT])
    result = await drain(deleted)
    assert result["status"] == "partial"
    assert result["counts"]["failed"] == 2
    assert not deleted.guard.client.removals


@pytest.mark.parametrize(
    "participant,expected",
    [
        (ChannelParticipant(201, None), 201),
        (ChannelParticipantCreator(202, ChatAdminRights()), 202),
        (
            ChannelParticipantBanned(
                PeerUser(201), 999, None, ChatBannedRights(until_date=None, send_messages=True)
            ),
            201,
        ),
        (
            ChannelParticipantBanned(
                PeerUser(201), 999, None, ChatBannedRights(until_date=None, view_messages=True)
            ),
            None,
        ),
        (
            ChannelParticipantBanned(
                PeerUser(201), 999, None, ChatBannedRights(until_date=None), left=True
            ),
            None,
        ),
        (
            ChannelParticipantBanned(
                PeerChannel(201), 999, None, ChatBannedRights(until_date=None, send_messages=True)
            ),
            None,
        ),
        (ChannelParticipantLeft(PeerUser(201)), None),
    ],
)
def test_telegram_participant_constructors_and_restricted_member_peers(participant, expected):
    assert participant_user_id(participant) == expected


async def test_deleted_user_without_hash_can_be_checked_without_entity_cache(deleted, monkeypatch):
    original = deleted.guard.client.get_permissions
    checked = []

    async def permissions(chat, user):
        if isinstance(user, InputPeerUser):
            checked.append(user)
        return await original(chat, user)

    monkeypatch.setattr(deleted.guard.client, "get_permissions", permissions)
    await deleted.start(ADMIN, uuid.uuid4().hex, [CHAT])
    result = await drain(deleted)
    assert result["status"] == "completed" and result["counts"]["removed"] == 1
    assert any(row.user_id == 201 and row.access_hash == 0 for row in checked)
