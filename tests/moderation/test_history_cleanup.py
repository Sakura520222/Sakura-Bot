# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
"""历史清理只改目录显示，保护实时成员关系、配置与会话文件。"""

import asyncio
import hashlib
import time
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import AsyncMock

import pytest
from fastapi import FastAPI
from httpx import ASGITransport, AsyncClient
from telethon.errors import ChannelPrivateError, FloodWaitError

from core.moderation.chat_cleanup import ChatCleanup, ChatCleanupError
from core.moderation.groups import GroupDirectory
from core.moderation.history_cleanup import HistoryCleanup
from core.web_api.routes import guard as routes

from .conftest import ADMIN, CHAT, MemoryRepository
from .test_chat_cleanup import CleanupClient
from .test_groups import session_file

HISTORY = CHAT - 2


@pytest.fixture
async def history_cleanup(tmp_path):
    client = CleanupClient()
    client.session.filename = str(session_file(tmp_path))
    client.errors[HISTORY] = ChannelPrivateError(request=None)
    repository = MemoryRepository()
    configured = {str(CHAT)}
    directory = GroupDirectory(client, lambda: configured, lambda: 999, repository)
    for chat_id in client.chats:
        directory.remember(chat_id)
    directory.refresh(force=True)
    await directory.task
    cleanup = HistoryCleanup(directory, lambda actor: actor in {ADMIN, "admin"})
    yield cleanup
    await cleanup.stop()
    await directory.stop()


async def execute(cleanup):
    preview = cleanup.preview(ADMIN)
    cleanup.start(preview["token"], ADMIN)
    await cleanup.task
    return cleanup.status()


async def test_preview_is_immutable_and_never_writes_or_leaves(history_cleanup):
    cleanup = history_cleanup
    preview = cleanup.preview(ADMIN)
    assert [row["chat_id"] for row in preview["chats"]] == [HISTORY]
    assert not cleanup.directory.history_store.cleaned_history
    assert not cleanup.directory.client.requests
    preview["chats"][0]["chat_id"] = CHAT
    assert cleanup.previews[preview["token"]]["chats"][0]["chat_id"] == HISTORY


async def test_cleanup_preserves_members_settings_and_session_and_survives_restart(history_cleanup):
    cleanup = history_cleanup
    directory = cleanup.directory
    session = Path(directory.client.session.filename)
    before = hashlib.sha256(await asyncio.to_thread(session.read_bytes)).hexdigest()
    joined = directory.snapshot()["chats"]
    audit = AsyncMock()
    preview = cleanup.preview(ADMIN)
    cleanup.start(preview["token"], ADMIN, audit=audit)
    await cleanup.task
    assert cleanup.status()["results"][0]["status"] == "cleaned"
    assert directory.snapshot()["chats"] == joined
    assert str(HISTORY) not in directory.snapshot()["metadata"]
    assert set(directory.configured_ids()) == {str(CHAT)}
    assert not directory.client.requests
    assert hashlib.sha256(await asyncio.to_thread(session.read_bytes)).hexdigest() == before
    assert audit.call_args.kwargs["action"] == "guard.chat.history.clear"
    restarted = GroupDirectory(
        directory.client, directory.configured_ids, directory.bot_id, directory.history_store
    )
    restarted.refresh(force=True)
    await restarted.task
    assert str(HISTORY) not in restarted.snapshot()["metadata"]
    assert {row["chat_id"] for row in restarted.snapshot()["chats"]} == {CHAT, CHAT - 1}
    await restarted.stop()


@pytest.mark.parametrize("change", ["joined", "admin", "configured", "timeout", "identity", "dm"])
async def test_confirm_rechecks_and_preserves_changed_or_uncertain_records(history_cleanup, change):
    cleanup = history_cleanup
    directory = cleanup.directory
    preview = cleanup.preview(ADMIN)
    if change in {"joined", "admin", "dm"}:
        directory.client.errors.clear()
        if change == "admin":
            directory.client.admins.add(HISTORY)
        if change == "dm":
            directory.client.chats[HISTORY].monoforum = True
    elif change == "configured":
        directory.configured_ids().add(str(HISTORY))
    elif change == "timeout":
        directory.client.errors[HISTORY] = TimeoutError()
    else:
        directory.client.chats[HISTORY] = directory.client.chats[CHAT]
    cleanup.start(preview["token"], ADMIN)
    await cleanup.task
    assert cleanup.status()["results"][0]["status"] == "skipped"
    assert not directory.history_store.cleaned_history
    assert not directory.client.requests
    assert str(HISTORY) in directory.snapshot()["metadata"]


@pytest.mark.parametrize("error", [TimeoutError(), ConnectionError(), FloodWaitError(None, 10)])
async def test_transient_errors_are_never_preview_candidates(history_cleanup, error):
    directory = history_cleanup.directory
    directory.client.errors[HISTORY] = error
    try:
        await directory.check(HISTORY)
    except FloodWaitError:
        pass
    assert history_cleanup.preview(ADMIN)["total"] == 0


async def test_write_failure_keeps_history_visible_and_retryable(history_cleanup):
    directory = history_cleanup.directory
    directory.history_store.mark_chat_history_cleaned = AsyncMock(side_effect=OSError("disk"))
    result = await execute(history_cleanup)
    assert result["results"][0]["status"] == "failed"
    assert str(HISTORY) in directory.snapshot()["metadata"]
    assert not directory.cleaned_history


async def test_event_during_access_check_protects_newly_joined_chat(history_cleanup):
    directory = history_cleanup.directory
    original = directory.client.get_entity

    async def rejoin(chat_id):
        directory.observe(chat_id, directory.client.chats[chat_id])
        raise ChannelPrivateError(request=None)

    directory.client.get_entity = rejoin
    result = await execute(history_cleanup)
    directory.client.get_entity = original
    assert result["results"][0]["status"] == "skipped"
    assert directory.entries[HISTORY]["joined"] is True
    assert not directory.history_store.cleaned_history


async def test_event_during_database_commit_cannot_hide_current_chat(history_cleanup):
    directory = history_cleanup.directory
    original = directory.history_store.mark_chat_history_cleaned

    async def rejoin(bot_id, chat_id, actor):
        await original(bot_id, chat_id, actor)
        directory.observe(chat_id, directory.client.chats[chat_id])

    directory.history_store.mark_chat_history_cleaned = rejoin
    result = await execute(history_cleanup)
    assert result["results"][0]["status"] == "skipped"
    assert HISTORY in {row["chat_id"] for row in directory.snapshot()["chats"]}
    assert not directory.history_store.cleaned_history


async def test_activity_and_confirmed_membership_restore_cleaned_history(history_cleanup):
    directory = history_cleanup.directory
    await execute(history_cleanup)
    directory.observe(HISTORY, directory.client.chats[HISTORY])
    assert str(HISTORY) in directory.snapshot()["metadata"]
    directory.client.errors.clear()
    directory.refresh(force=True)
    await directory.task
    assert HISTORY in {row["chat_id"] for row in directory.snapshot()["chats"]}
    assert not directory.history_store.cleaned_history


async def test_cleanup_state_is_scoped_to_bot_identity(history_cleanup):
    directory = history_cleanup.directory
    await execute(history_cleanup)
    other = GroupDirectory(directory.client, lambda: [], lambda: 1000, directory.history_store)
    other.refresh(force=True)
    await other.task
    assert str(HISTORY) in other.snapshot()["metadata"]
    await other.stop()


async def test_cross_operation_tokens_and_expired_or_foreign_tokens_are_rejected(history_cleanup):
    directory = history_cleanup.directory
    leaving = ChatCleanup(directory, history_cleanup.authorized)
    preview = history_cleanup.preview(ADMIN)
    for cleanup, actor in [(leaving, ADMIN), (history_cleanup, "admin")]:
        with pytest.raises(ChatCleanupError):
            cleanup.start(preview["token"], actor)
    history_cleanup.previews[preview["token"]]["expires"] = time.monotonic() - 1
    with pytest.raises(ChatCleanupError):
        history_cleanup.start(preview["token"], ADMIN)
    assert not directory.history_store.cleaned_history


async def test_duplicate_confirmation_is_idempotent_and_operations_are_serialized(history_cleanup):
    directory = history_cleanup.directory
    leaving = ChatCleanup(directory, history_cleanup.authorized)
    leave_preview = leaving.preview(ADMIN)
    preview = history_cleanup.preview(ADMIN)
    first = history_cleanup.start(preview["token"], ADMIN)
    assert history_cleanup.start(preview["token"], ADMIN) == first
    with pytest.raises(ChatCleanupError):
        leaving.start(leave_preview["token"], ADMIN)
    await leaving.stop()
    assert directory.mutating
    await history_cleanup.task
    assert history_cleanup.start(preview["token"], ADMIN) == history_cleanup.status()
    assert len(directory.history_store.cleaned_history) == 1
    assert not directory.client.requests


async def test_revoked_admin_during_recheck_cannot_clean(history_cleanup):
    directory = history_cleanup.directory
    original = directory.client.get_entity

    async def revoke(chat_id):
        history_cleanup.authorized = lambda actor: False
        return await original(chat_id)

    directory.client.get_entity = revoke
    result = await execute(history_cleanup)
    assert result["status"] == "interrupted"
    assert not directory.history_store.cleaned_history


async def test_api_requires_preview_explicit_confirmation_and_returns_history_job(
    history_cleanup, monkeypatch
):
    directory = history_cleanup.directory
    service = SimpleNamespace(
        directory=directory,
        chat_cleanup=ChatCleanup(directory, history_cleanup.authorized),
        history_cleanup=history_cleanup,
    )
    app = FastAPI()
    app.dependency_overrides[routes.guard_admin] = lambda: (service, ADMIN)
    app.include_router(routes.router, prefix="/api/guard")
    monkeypatch.setattr(routes, "record_system_audit", AsyncMock())
    async with AsyncClient(transport=ASGITransport(app), base_url="http://test") as client:
        path = "/api/guard/chats/history-cleanup"
        assert (
            await client.post(path, json={"token": "x" * 32, "confirmed": True})
        ).status_code == 409
        preview = (await client.post(path + "-preview")).json()
        for body in [
            {"token": preview["token"]},
            {"token": preview["token"], "confirmed": False},
            {"token": preview["token"], "confirmed": "true"},
            {"token": preview["token"], "confirmed": True, "chat_ids": [CHAT]},
        ]:
            assert (await client.post(path, json=body)).status_code == 422
        assert not directory.history_store.cleaned_history
        response = await client.post(path, json={"token": preview["token"], "confirmed": True})
        assert response.status_code == 200 and response.json()["status"] == "running"
        await history_cleanup.task
        final = (await client.get("/api/guard/chats")).json()
        assert final["history_cleanup"]["results"][0]["status"] == "cleaned"
        assert final["history_cleanable_count"] == 0
        assert str(HISTORY) not in final["metadata"]
    assert not directory.client.requests
