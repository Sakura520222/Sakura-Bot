# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
from datetime import timedelta

import pytest

from core.infrastructure.database.mysql import utc_now_naive
from core.moderation.moderator import Moderator

from .conftest import ADMIN, CHAT, USER
from .test_mysql import TEST_DSN
from .test_mysql import mysql_repository as mysql_repository
from .test_notice_cleanup import legacy_result
from .test_verification import callback

pytestmark = [
    pytest.mark.integration,
    pytest.mark.database,
    pytest.mark.skipif(not TEST_DSN, reason="GUARD_MYSQL_TEST_DSN not configured"),
]


async def start(guard, repository):
    guard.repository = repository
    guard.group(CHAT).verify_join = True
    await guard.process_join(CHAT, guard.client.users[USER], "mysql-notice-cleanup")
    async with repository.lock(USER) as store:
        return await store.verification(CHAT, USER)


async def event_cases(repository):
    rows = (await repository.query("cases"))["rows"]
    return next(row for row in rows if row["action"] == "verify_start"), next(
        row for row in rows if row["action"] == "verify_pass"
    )


async def test_mysql_removed_notice_tombstone_is_sticky_and_queryable(guard, mysql_repository):
    session = await start(guard, mysql_repository)
    await guard.verification.callback(callback(session))
    parent, result = await event_cases(mysql_repository)
    async with mysql_repository.lock(USER) as store:
        rows = await store.notices(parent["id"])
        assert all(row["synced_status"] == "removed" for row in rows)
        original = next(row for row in rows if row["target_id"] == ADMIN)
        await store.notice(parent["id"], ADMIN, 999999, None, "should not overwrite")
        saved = next(row for row in await store.notices(parent["id"]) if row["target_id"] == ADMIN)
        assert (
            saved["message_id"] == original["message_id"]
            and saved["synced_status"] == "removed"
            and not saved["error"]
        )
    removed = await mysql_repository.query("notifications", user_id=USER, status="removed")
    assert removed["total"] == 2
    cases, _ = await mysql_repository.work()
    assert not {parent["id"], result["id"]} & {row["id"] for row in cases}


async def test_mysql_cleanup_job_is_recovered_after_result_is_already_synced(
    guard, mysql_repository, monkeypatch
):
    session = await start(guard, mysql_repository)
    delete = guard.client.delete_messages

    async def fail_private(target, identifiers):
        if target == ADMIN:
            raise RuntimeError("private delete unavailable")
        await delete(target, identifiers)

    monkeypatch.setattr(guard.client, "delete_messages", fail_private)
    await guard.verification.callback(callback(session))
    parent, result = await event_cases(mysql_repository)
    assert result["payload"]["notification_cleanup_pending"]
    cases, _ = await mysql_repository.work()
    assert result["id"] not in {row["id"] for row in cases}
    async with mysql_repository.lock(USER) as store:
        rows = await store.notices(parent["id"])
        original = next(row for row in rows if row["target_id"] == ADMIN)
        assert original["synced_status"] == "deleting"
        await store.notice(parent["id"], ADMIN, original["message_id"], None, None)
        assert (
            next(row for row in await store.notices(parent["id"]) if row["target_id"] == ADMIN)[
                "synced_status"
            ]
            == "deleting"
        )
        past = utc_now_naive() - timedelta(seconds=1)
        await store.execute(
            "UPDATE moderation_notifications SET retry_at=%s WHERE case_id=%s AND target_id=%s",
            (past, parent["id"], ADMIN),
        )
        payload = {
            **result["payload"],
            "notification_cleanup_retry_at": past.isoformat(timespec="seconds"),
        }
        await store.update_case(result["id"], result["status"], payload=payload)
    cases, _ = await mysql_repository.work()
    assert result["id"] in {row["id"] for row in cases} and parent["id"] not in {
        row["id"] for row in cases
    }
    monkeypatch.setattr(guard.client, "delete_messages", delete)
    restarted = Moderator(
        guard.client, mysql_repository, guard.config, default_admins=guard.default_admins
    )
    await restarted.recover_once()
    async with mysql_repository.lock(USER) as store:
        assert all(row["synced_status"] == "removed" for row in await store.notices(parent["id"]))
    assert not (await mysql_repository.get_case(result["id"]))["payload"][
        "notification_cleanup_pending"
    ]


async def test_mysql_retention_preserves_both_sides_of_pending_notification_cleanup(
    guard, mysql_repository, monkeypatch
):
    session = await start(guard, mysql_repository)
    delete = guard.client.delete_messages

    async def fail_private(target, identifiers):
        if target in {ADMIN, 11}:
            raise RuntimeError("delete unavailable")
        await delete(target, identifiers)

    monkeypatch.setattr(guard.client, "delete_messages", fail_private)
    await guard.verification.callback(callback(session))
    parent, result = await event_cases(mysql_repository)
    async with mysql_repository.lock(USER) as store:
        await store.execute(
            "UPDATE moderation_cases SET created_at=%s WHERE id IN (%s,%s)",
            (utc_now_naive() - timedelta(days=30), parent["id"], result["id"]),
        )
    await mysql_repository.cleanup(7)
    assert await mysql_repository.get_case(parent["id"]) is not None
    assert await mysql_repository.get_case(result["id"]) is not None


async def test_mysql_partial_delivery_keeps_unsent_recipients_original(guard, mysql_repository):
    session = await start(guard, mysql_repository)
    guard.client.fail[ADMIN] = RuntimeError("recipient unavailable")
    await guard.verification.callback(callback(session))
    parent, result = await event_cases(mysql_repository)
    async with mysql_repository.lock(USER) as store:
        rows = {row["target_id"]: row for row in await store.notices(parent["id"])}
        assert rows[ADMIN]["synced_status"] not in {"deleting", "removed"}
        assert rows[11]["synced_status"] == "removed"
        assert (ADMIN, rows[ADMIN]["message_id"]) in guard.client.sent
    assert result["payload"]["notification_cleanup_pending"]


@pytest.mark.parametrize("field", ["verification_failure", "verification_admin"])
async def test_mysql_legacy_nested_session_reference_can_retire_original(
    guard, mysql_repository, field
):
    session = await start(guard, mysql_repository)
    async with mysql_repository.lock(USER) as store:
        result = await legacy_result(guard, store, session, field)
        await guard.notifier.sync(store, result, guard.group(CHAT))
        saved = await store.case(result["id"])
        assert saved["payload"]["verification_session_id"] == session["id"]
        assert not saved["payload"]["notification_cleanup_pending"]
        originals = await store.notices(saved["payload"]["notification_cleanup_source"])
        assert all(row["synced_status"] == "removed" for row in originals)
