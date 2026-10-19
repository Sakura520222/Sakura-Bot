# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
"""主 Bot 会话清理：权限核实、预览约束、退出协议与管理 API。"""

import asyncio
import sqlite3
import time
from datetime import UTC, datetime
from types import SimpleNamespace
from unittest.mock import AsyncMock

import pytest
from fastapi import FastAPI
from fastapi.testclient import TestClient
from httpx import ASGITransport, AsyncClient
from telethon.errors import FloodWaitError
from telethon.tl.functions.channels import LeaveChannelRequest
from telethon.tl.functions.messages import DeleteChatUserRequest
from telethon.tl.types import Chat, InputUserSelf, UpdateChannel

from core.moderation.chat_cleanup import ChatCleanup, ChatCleanupError
from core.moderation.groups import GroupDirectory, cached_chats
from core.moderation.history_cleanup import HistoryCleanup
from core.web_api.auth import WebUIUser
from core.web_api.routes import guard as routes

from .conftest import ADMIN, CHAT, USER
from .test_groups import DirectoryClient

BASIC_CHAT = -123


class CleanupClient(DirectoryClient):
    def __init__(self):
        super().__init__()
        self.chats[BASIC_CHAT] = Chat(
            id=123,
            title="普通群",
            photo=None,
            participants_count=2,
            date=datetime.now(UTC),
            version=1,
        )
        self.members.update({CHAT - 1, BASIC_CHAT})
        self.admins = {CHAT - 1}
        self.unknown = set()
        self.errors = {}
        self.requests = []
        self.get_permissions_calls = []

    async def get_permissions(self, chat_id, user_id):
        self.get_permissions_calls.append((chat_id, user_id))
        if chat_id in self.errors:
            raise self.errors[chat_id]
        result = await super().get_permissions(chat_id, user_id)
        result.is_admin = None if chat_id in self.unknown else chat_id in self.admins
        return result

    async def __call__(self, request):
        self.requests.append(request)
        return True


@pytest.fixture
async def cleanup():
    client = CleanupClient()
    directory = GroupDirectory(client, lambda: [str(key) for key in client.chats], lambda: 999)
    directory.refresh(force=True)
    await directory.task
    service = ChatCleanup(directory, lambda actor: actor in {ADMIN, "admin"})
    yield service
    await service.stop()
    await directory.stop()


def test_cache_does_not_silently_truncate_after_one_thousand_chats(tmp_path):
    path = tmp_path / "bot.session"
    connection = sqlite3.connect(path)
    connection.execute("CREATE TABLE entities (id INTEGER, name TEXT, date INTEGER)")
    connection.executemany(
        "INSERT INTO entities VALUES (?,?,?)",
        [(-value, str(value), value) for value in range(1, 1010)],
    )
    connection.commit()
    connection.close()
    assert len(cached_chats(str(path))) == 1009


async def test_list_includes_channels_but_guard_group_picker_stays_group_only(cleanup):
    snapshot = cleanup.directory.snapshot()
    assert len(snapshot["chats"]) == 4
    assert len(snapshot["groups"]) == 3
    assert snapshot["metadata"][str(CHAT - 1)]["is_admin"] is True
    assert snapshot["metadata"][str(CHAT - 2)]["chat_type"] == "channel"
    assert snapshot["metadata"][str(BASIC_CHAT)]["chat_type"] == "group"


async def test_preview_excludes_admin_unknown_and_departed_without_leaving(cleanup):
    directory = cleanup.directory
    directory.client.unknown.add(CHAT - 2)
    await directory.check(CHAT - 2)
    directory.observe(BASIC_CHAT, left=True)
    preview = cleanup.preview(ADMIN)
    assert [row["chat_id"] for row in preview["chats"]] == [CHAT]
    assert preview["admin_count"] == 1
    assert preview["unknown_count"] == 1
    assert not directory.client.requests
    # 预览返回值与内部待执行名单不共享可变对象。
    preview["chats"][0]["chat_id"] = USER
    assert cleanup.previews[preview["token"]]["chats"][0]["chat_id"] == CHAT


async def test_confirm_uses_separate_channel_and_basic_group_self_leave_requests(cleanup):
    audit = AsyncMock()
    preview = cleanup.preview(ADMIN)
    cleanup.start(preview["token"], ADMIN, audit=audit)
    await cleanup.task
    job = cleanup.status()
    assert job["status"] == "completed"
    assert job["checked"] == job["total"] == 3
    assert all(row["status"] == "left" for row in job["results"])
    requests = cleanup.directory.client.requests
    assert sum(isinstance(request, LeaveChannelRequest) for request in requests) == 2
    basic = next(request for request in requests if isinstance(request, DeleteChatUserRequest))
    assert basic.chat_id == 123
    assert isinstance(basic.user_id, InputUserSelf)
    assert not basic.revoke_history
    assert [row["chat_id"] for row in cleanup.directory.snapshot()["chats"]] == [CHAT - 1]
    assert audit.await_count == 3


@pytest.mark.parametrize("change", ["promoted", "unknown", "offline", "left"])
async def test_confirm_rechecks_permissions_and_skips_changes_since_preview(cleanup, change):
    preview = cleanup.preview(ADMIN)
    client = cleanup.directory.client
    if change == "promoted":
        client.admins.add(CHAT)
    elif change == "unknown":
        client.unknown.add(CHAT)
    elif change == "offline":
        client.errors[CHAT] = ConnectionError("offline")
    else:
        client.members.remove(CHAT)
    cleanup.start(preview["token"], ADMIN)
    await cleanup.task
    result = next(row for row in cleanup.status()["results"] if row["chat_id"] == CHAT)
    assert result["status"] == "skipped"
    assert len(client.requests) == 2


async def test_confirm_never_adds_new_members_to_the_preview(cleanup):
    preview = cleanup.preview(ADMIN)
    # 原先的管理员降为普通成员，仍然不在确认名单里。
    cleanup.directory.client.admins.clear()
    await cleanup.directory.check(CHAT - 1)
    cleanup.start(preview["token"], ADMIN)
    await cleanup.task
    assert CHAT - 1 not in {row["chat_id"] for row in cleanup.status()["results"]}
    assert cleanup.directory.entries[CHAT - 1]["joined"] is True


async def test_invalid_expired_and_other_actor_tokens_cannot_leave(cleanup):
    preview = cleanup.preview(ADMIN)
    with pytest.raises(ChatCleanupError):
        cleanup.start(preview["token"], "admin")
    with pytest.raises(ChatCleanupError):
        cleanup.start("invalid", ADMIN)
    cleanup.previews[preview["token"]]["expires"] = time.monotonic() - 1
    with pytest.raises(ChatCleanupError):
        cleanup.start(preview["token"], ADMIN)
    assert not cleanup.directory.client.requests


async def test_duplicate_confirmation_returns_existing_job_without_repeating_requests(cleanup):
    preview = cleanup.preview(ADMIN)
    first = cleanup.start(preview["token"], ADMIN)
    assert cleanup.start(preview["token"], ADMIN) == first
    await cleanup.task
    finished = cleanup.status()
    assert cleanup.start(preview["token"], ADMIN) == finished
    assert len(cleanup.directory.client.requests) == 3


async def test_preview_waits_for_discovery_and_refreshes_stale_results(cleanup):
    directory = cleanup.directory
    directory.task = asyncio.create_task(asyncio.Event().wait())
    with pytest.raises(ChatCleanupError):
        cleanup.preview(ADMIN)
    directory.task.cancel()
    await asyncio.gather(directory.task, return_exceptions=True)
    directory.last_scan = time.monotonic() - 301
    with pytest.raises(ChatCleanupError):
        cleanup.preview(ADMIN)
    assert directory.task and not directory.task.done()
    await directory.task


async def test_preview_rejects_a_scan_interrupted_before_all_candidates_were_checked(cleanup):
    cleanup.directory.checked -= 1
    with pytest.raises(ChatCleanupError, match="尚未完成"):
        cleanup.preview(ADMIN)
    assert not cleanup.directory.client.requests


async def test_job_pauses_discovery_and_stops_on_revoked_global_admin(cleanup):
    preview = cleanup.preview(ADMIN)
    cleanup.authorized = lambda actor: False
    cleanup.start(preview["token"], ADMIN)
    previous_task = cleanup.directory.task
    cleanup.directory.refresh(force=True)
    assert cleanup.directory.task is previous_task
    await cleanup.task
    assert cleanup.status()["status"] == "interrupted"
    assert cleanup.status()["checked"] == 0
    assert not cleanup.directory.mutating
    assert not cleanup.directory.client.requests


async def test_flood_wait_stops_the_batch_without_trying_remaining_chats(cleanup):
    preview = cleanup.preview(ADMIN)
    # 列表按标题排序；让第一项触发限流。
    first = preview["chats"][0]["chat_id"]
    cleanup.directory.client.errors[first] = FloodWaitError(request=None, capture=60)
    cleanup.start(preview["token"], ADMIN)
    await cleanup.task
    assert cleanup.status()["status"] == "interrupted"
    assert cleanup.status()["checked"] == 1
    assert cleanup.status()["results"][0]["status"] == "failed"
    assert not cleanup.directory.client.requests


async def test_failed_leave_does_not_mark_chat_left_and_other_chats_continue(cleanup):
    preview = cleanup.preview(ADMIN)
    original = cleanup.directory.client.__class__.__call__
    calls = []

    async def fail_once(client, request):
        calls.append(request)
        if len(calls) == 1:
            raise RuntimeError("injected failure")
        return await original(client, request)

    from unittest.mock import patch

    with patch.object(CleanupClient, "__call__", fail_once):
        cleanup.start(preview["token"], ADMIN)
        await cleanup.task
    job = cleanup.status()
    assert job["checked"] == 3
    assert [row["status"] for row in job["results"]].count("failed") == 1
    assert cleanup.directory.entries[preview["chats"][0]["chat_id"]]["joined"] is True


async def test_channel_update_invalidates_cached_admin_and_requests_discovery(guard):
    guard.directory.observe(CHAT, SimpleNamespace(title="群", megagroup=True, left=False))
    guard.directory.entries[CHAT]["is_admin"] = False
    await guard.handle_membership_update(UpdateChannel(channel_id=1234567890))
    assert guard.directory.entries[CHAT]["is_admin"] is None
    await guard.directory.task
    await guard.directory.stop()


async def test_stop_cancels_a_confirmed_job_before_any_leave(cleanup):
    preview = cleanup.preview(ADMIN)
    cleanup.start(preview["token"], ADMIN)
    await cleanup.stop()
    assert cleanup.status()["status"] == "interrupted"
    assert not cleanup.directory.mutating
    assert not cleanup.directory.client.requests


async def test_api_preview_confirm_progress_and_final_list_use_the_real_service(
    cleanup, monkeypatch
):
    service = SimpleNamespace(
        directory=cleanup.directory,
        chat_cleanup=cleanup,
        history_cleanup=HistoryCleanup(cleanup.directory, cleanup.authorized),
    )
    app = FastAPI()
    app.dependency_overrides[routes.guard_admin] = lambda: (service, ADMIN)
    app.include_router(routes.router, prefix="/api/guard")
    audit = AsyncMock()
    monkeypatch.setattr(routes, "record_system_audit", audit)
    async with AsyncClient(transport=ASGITransport(app), base_url="http://test") as client:
        preview = (await client.post("/api/guard/chats/leave-preview")).json()
        response = await client.post(
            "/api/guard/chats/leave", json={"token": preview["token"], "confirmed": True}
        )
        assert response.status_code == 200
        assert response.json()["status"] == "running"
        await cleanup.task
        final = (await client.get("/api/guard/chats")).json()
    assert final["cleanup"]["status"] == "completed"
    assert final["cleanup"]["checked"] == 3
    assert [row["chat_id"] for row in final["chats"]] == [CHAT - 1]
    assert audit.await_count == 3


@pytest.fixture
def api(guard, monkeypatch):
    monkeypatch.setattr(routes, "get_guard", lambda: guard)
    monkeypatch.setattr(routes, "record_system_audit", AsyncMock())
    guard.directory.last_scan = time.monotonic()
    guard.directory.observe(CHAT, SimpleNamespace(title="测试群", megagroup=True, left=False))
    guard.directory.entries[CHAT].update(is_admin=False, role="member")
    app = FastAPI()

    @app.middleware("http")
    async def authenticated(request, call_next):
        request.state.user = WebUIUser(user_id=request.headers.get("x-test-user", str(ADMIN)))
        return await call_next(request)

    app.include_router(routes.router, prefix="/api/guard")
    with TestClient(app) as client:
        yield client


def test_api_requires_current_global_admin_for_listing_preview_and_confirm(api, guard):
    for path, method in [
        ("chats", "get"),
        ("chats/leave-preview", "post"),
        ("chats/leave", "post"),
        ("chats/history-cleanup-preview", "post"),
        ("chats/history-cleanup", "post"),
    ]:
        call = getattr(api, method)
        assert call(f"/api/guard/{path}", headers={"x-test-user": str(USER)}).status_code == 403
    assert api.get("/api/guard/chats").status_code == 200
    assert api.post("/api/guard/chats/leave-preview").status_code == 200
    guard.default_admins = lambda: []
    assert api.post("/api/guard/chats/leave-preview").status_code == 403


@pytest.mark.parametrize(
    "body",
    [
        {},
        {"token": "x" * 32},
        {"token": "x" * 32, "confirmed": False},
        {"token": "x" * 32, "confirmed": 1},
        {"token": "x" * 32, "confirmed": True, "chat_ids": [CHAT]},
    ],
)
def test_api_rejects_unconfirmed_requests_and_client_supplied_chat_lists(api, body):
    assert api.post("/api/guard/chats/leave", json=body).status_code == 422


def test_api_cannot_confirm_without_own_valid_preview(api):
    assert (
        api.post("/api/guard/chats/leave", json={"token": "x" * 32, "confirmed": True}).status_code
        == 409
    )
    preview = api.post("/api/guard/chats/leave-preview").json()
    response = api.post(
        "/api/guard/chats/leave",
        json={"token": preview["token"], "confirmed": True},
        headers={"x-test-user": "admin"},
    )
    assert response.status_code == 409
