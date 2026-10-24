# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
import asyncio
import uuid
from datetime import timedelta

import pytest

from core.infrastructure.database.mysql import utc_now_naive
from core.moderation.deleted_cleanup import DeletedUserCleanup
from core.moderation.deleted_cleanup_repository import DeletedCleanupRepository
from core.moderation.repository import GuardStore

from .conftest import ADMIN, CHAT
from .test_deleted_cleanup import deleted as deleted
from .test_deleted_cleanup import drain
from .test_mysql import TEST_DSN
from .test_mysql import mysql_repository as mysql_repository

pytestmark = [
    pytest.mark.integration,
    pytest.mark.database,
    pytest.mark.skipif(not TEST_DSN, reason="GUARD_MYSQL_TEST_DSN not configured"),
]


def attach(deleted, mysql_repository):
    deleted.guard.repository = mysql_repository
    deleted.repository = DeletedCleanupRepository(mysql_repository.pool)


async def test_mysql_next_job_rechecks_removed_deleted_users_and_clears_legacy_messages(
    deleted, mysql_repository
):
    attach(deleted, mysql_repository)
    await deleted.start(ADMIN, uuid.uuid4().hex, [CHAT])
    first = await drain(deleted)
    assert first["counts"]["removed"] == 1
    deleted.guard.ban_history.revoke.reset_mock()
    await deleted.start(ADMIN, uuid.uuid4().hex, [CHAT])
    second = await drain(deleted)
    assert second["counts"]["removed"] == 1
    assert deleted.guard.client.removals == [(CHAT, 201)]
    deleted.guard.ban_history.revoke.assert_awaited_once_with(CHAT, 201, 999)


async def test_mysql_schema_idempotence_pagination_and_persisted_audit(deleted, mysql_repository):
    attach(deleted, mysql_repository)
    await mysql_repository.initialize()
    deleted.guard.client.page_size = 2
    request = uuid.uuid4().hex
    first = await deleted.start(ADMIN, request, [CHAT])
    assert (await deleted.start(ADMIN, request, [CHAT]))["id"] == first["id"]
    result = await drain(deleted)
    assert result["status"] == "completed" and result["counts"]["removed"] == 1
    async with mysql_repository.pool.acquire() as connection:
        store = GuardStore(connection)
        assert (await store.one("SELECT COUNT(*) AS total FROM moderation_deleted_cleanup_jobs"))[
            "total"
        ] == 1
        assert (await store.one("SELECT COUNT(*) AS total FROM moderation_deleted_cleanup_items"))[
            "total"
        ] == 2
        assert (await store.one("SELECT active_key FROM moderation_deleted_cleanup_jobs"))[
            "active_key"
        ] is None
    for table in ("cases", "blacklist", "whitelist"):
        assert (await mysql_repository.query(table))["total"] == 0


async def test_mysql_recovery_after_telegram_success_and_lost_reply(deleted, mysql_repository):
    attach(deleted, mysql_repository)
    await deleted.start(ADMIN, uuid.uuid4().hex, [CHAT])
    await deleted.step()
    deleted.guard.client.fail_after_remove = True
    await deleted.step()
    assert (await deleted.status())["job"]["status"] == "waiting"
    await deleted.stop()
    recovered = DeletedUserCleanup(deleted.guard, DeletedCleanupRepository(mysql_repository.pool))
    result = await drain(recovered)
    assert result["status"] == "completed" and result["counts"]["removed"] == 1
    assert deleted.guard.client.removals == [(CHAT, 201)]


async def test_mysql_cross_instance_lock_and_job_create_are_serialized(deleted, mysql_repository):
    attach(deleted, mysql_repository)
    other = DeletedCleanupRepository(mysql_repository.pool)
    async with deleted.repository.locked(999) as first:
        assert first is not None
        async with other.locked(999) as second:
            assert second is None
    request = uuid.uuid4().hex
    results = await asyncio.gather(
        deleted.start(ADMIN, request, [CHAT]),
        deleted.start(ADMIN, request, [CHAT]),
        return_exceptions=True,
    )
    assert any(isinstance(row, dict) for row in results)
    async with other.read() as store:
        assert len(await store.history(999)) == 1


async def test_mysql_schedule_survives_restart_and_retention_preserves_active_jobs(
    deleted, mysql_repository
):
    attach(deleted, mysql_repository)
    deleted.settings.enabled = True
    await deleted.step()
    first = await drain(deleted)
    restarted = DeletedUserCleanup(deleted.guard, DeletedCleanupRepository(mysql_repository.pool))
    await restarted.step()
    assert len((await restarted.status())["history"]) == 1
    second = await restarted.start(ADMIN, uuid.uuid4().hex, [CHAT])
    old = utc_now_naive() - timedelta(days=100)
    async with mysql_repository.pool.acquire() as connection:
        await GuardStore(connection).execute(
            "UPDATE moderation_deleted_cleanup_jobs SET created_at=%s,updated_at=%s", (old, old)
        )
    await mysql_repository.cleanup(90)
    async with restarted.repository.read() as store:
        assert await store.get(999, first["id"]) is None
        assert (await store.active(999))["id"] == second["id"]
        assert await store.store.one(
            "SELECT COUNT(*) AS total FROM moderation_deleted_cleanup_items WHERE job_id=%s",
            (first["id"],),
        ) == {"total": 0}
