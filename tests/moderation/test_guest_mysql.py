# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
"""访客来源与历史扫描意图在真实 MySQL 中可恢复。"""

import uuid
from copy import deepcopy
from types import SimpleNamespace

import pytest

from core.moderation.appeals import AppealWorkflow, eligible, source_signature
from core.moderation.deleted_cleanup import DeletedUserCleanup
from core.moderation.guest_messages import GuestMessages
from core.moderation.matcher import Match
from core.moderation.repository import GuardStore

from .conftest import ADMIN, CHAT
from .test_deleted_cleanup import deleted as deleted
from .test_deleted_cleanup import drain
from .test_deleted_cleanup_mysql import attach
from .test_deleted_history import history as history
from .test_guest_blacklist import blacklist
from .test_guest_messages import CALLER, event, send
from .test_mysql import TEST_DSN
from .test_mysql import mysql_repository as mysql_repository

pytestmark = [
    pytest.mark.integration,
    pytest.mark.database,
    pytest.mark.skipif(not TEST_DSN, reason="GUARD_MYSQL_TEST_DSN not configured"),
]


async def test_mysql_guest_caller_intent_survives_restart_and_is_retained(guard, mysql_repository):
    guard.repository = mysql_repository
    await send(guard, event(guard))
    parent = (await mysql_repository.query("cases"))["rows"][0]
    assert parent["payload"]["guest_ban_pending"]
    work, _ = await mysql_repository.work()
    assert parent["id"] in {row["id"] for row in work}
    await guard.recover_once()
    parent = await mysql_repository.get_case(parent["id"])
    child = await mysql_repository.get_case(parent["payload"]["guest_ban"]["case_id"])
    assert child["user_id"] == CALLER and child["status"] == "pending"
    assert child["reviewer_actor"] == "system:guest_message"
    assert not parent["payload"]["guest_ban_pending"]
    await guard.recover_once()
    assert sum(row[0] == "ban" and row[2] == CALLER for row in guard.client.calls) == 1


async def test_mysql_deleted_history_phase_survives_restart(deleted, history, mysql_repository):
    attach(deleted, mysql_repository)
    await deleted.start(ADMIN, uuid.uuid4().hex, [CHAT])
    for _ in range(8):
        await deleted.step()
        job = (await deleted.status())["job"]
        if job["groups"][0]["status"] == "history_scanning":
            break
    assert job["counts"]["history_pending"] == 1
    await deleted.stop()
    restarted = DeletedUserCleanup(deleted.guard, deleted.repository)
    result = await drain(restarted)
    assert result["status"] == "completed"
    assert result["groups"][0]["history_scanned"] == 3
    assert result["groups"][0]["history_deleted"] == 2


async def blacklisted_source(guard, repository):
    guard.repository = repository
    await send(guard, event(guard))
    parent = (await repository.query("cases"))["rows"][0]
    return await blacklist(guard, parent)


async def test_mysql_global_guest_intent_commits_with_source_and_recovers(guard, mysql_repository):
    parent = await blacklisted_source(guard, mysql_repository)
    assert parent["payload"]["guest_ban"]["action"] == "blacklist"
    guard.guest_messages = GuestMessages(guard)
    await guard.recover_once()
    parent = await mysql_repository.get_case(parent["id"])
    audit = await mysql_repository.get_case(parent["payload"]["guest_ban"]["case_id"])
    async with mysql_repository.lock(CALLER) as store:
        row = await store.blacklist(CALLER)
    assert row["case_id"] == audit["id"] and audit["status"] == "done"
    assert audit["action"] == "guest_blacklist" and not eligible(audit)
    assert audit["payload"]["guest_blacklist_authorization"]["actor"] == f"telegram:{ADMIN}"
    assert row["reviewer_actor"] == "system:guest_message"
    assert not parent["payload"]["guest_ban_pending"]


async def test_mysql_global_guest_case_and_blacklist_roll_back_together(
    guard, mysql_repository, monkeypatch
):
    parent = await blacklisted_source(guard, mysql_repository)
    key = guard.guest_messages.blacklist_key(parent["id"], parent["payload"]["guest_ban"])
    update = GuardStore.update_case

    async def fail_after_update(self, case_id, status, **kwargs):
        await update(self, case_id, status, **kwargs)
        if case_id == key[:32] and status == "done":
            raise ConnectionError("after audit and blacklist writes")

    with monkeypatch.context() as context:
        context.setattr(GuardStore, "update_case", fail_after_update)
        await guard.guest_messages.resume(parent["id"])
    async with mysql_repository.lock(CALLER) as store:
        assert await store.blacklist(CALLER) is None
        assert await store.case_for_event(key) is None
    parent = await mysql_repository.get_case(parent["id"])
    assert parent["payload"]["guest_ban_pending"]
    assert parent["payload"]["guest_ban"]["error"] == "ConnectionError"
    async with mysql_repository.lock(parent["user_id"]) as store:
        parent["payload"]["guest_ban"].pop("retry_at", None)
        await store.update_case(parent["id"], parent["status"], payload=parent["payload"])
    guard.guest_messages = GuestMessages(guard)
    await guard.guest_messages.resume(parent["id"])
    assert (await mysql_repository.get_case(parent["id"]))["payload"]["guest_ban"][
        "status"
    ] == "blacklisted"


async def test_mysql_global_guest_transaction_rechecks_source_after_external_await(
    guard, mysql_repository, monkeypatch
):
    parent = await blacklisted_source(guard, mysql_repository)
    finish = GuardStore.finish_review

    async def change_before_commit(self, case, action, *, guest_parent=None):
        if guest_parent:
            async with mysql_repository.lock(parent["user_id"]) as source_store:
                latest = await source_store.case(parent["id"])
                await source_store.update_case(parent["id"], "unbanned", payload=latest["payload"])
        return await finish(self, case, action, guest_parent=guest_parent)

    monkeypatch.setattr(GuardStore, "finish_review", change_before_commit)
    await guard.guest_messages.resume(parent["id"])
    async with mysql_repository.lock(CALLER) as store:
        assert await store.blacklist(CALLER) is None
    audit_key = guard.guest_messages.blacklist_key(parent["id"], parent["payload"]["guest_ban"])
    assert await mysql_repository.get_case(audit_key[:32]) is None


async def test_mysql_guest_link_preserves_a_preexisting_global_decision(guard, mysql_repository):
    parent = await blacklisted_source(guard, mysql_repository)
    async with mysql_repository.lock(CALLER) as store:
        prior = await guard.punish(
            store,
            CHAT,
            guard.client.users[CALLER],
            Match("message", "prior", "prior human case", "prior", "prior"),
            "independent",
        )
    await guard.reviews.review(prior["id"], "blacklist", ADMIN)
    async with mysql_repository.lock(CALLER) as store:
        before = await store.blacklist(CALLER)
    await guard.guest_messages.resume(parent["id"])
    async with mysql_repository.lock(CALLER) as store:
        after = await store.blacklist(CALLER)
    assert {k: v for k, v in before.items() if k != "last_seen_at"} == {
        k: v for k, v in after.items() if k != "last_seen_at"
    }
    parent = await mysql_repository.get_case(parent["id"])
    assert parent["payload"]["guest_ban"]["status"] == "blacklisted"
    assert parent["payload"]["guest_ban"]["case_id"] != prior["id"]


async def test_mysql_guest_replay_cannot_restore_a_removed_global_blacklist(
    guard, mysql_repository
):
    parent = await blacklisted_source(guard, mysql_repository)
    intent = deepcopy(parent["payload"])
    await guard.guest_messages.resume(parent["id"])
    async with mysql_repository.lock(parent["user_id"]) as store:
        await store.update_case(parent["id"], "blacklisted", payload=intent)
    async with mysql_repository.lock(CALLER) as store:
        await store.execute("DELETE FROM moderation_blacklist WHERE user_id=%s", (CALLER,))
    calls = deepcopy(guard.client.calls)
    guard.guest_messages = GuestMessages(guard)
    await guard.guest_messages.resume(parent["id"])
    async with mysql_repository.lock(CALLER) as store:
        assert await store.blacklist(CALLER) is None
    assert guard.client.calls == calls
    parent = await mysql_repository.get_case(parent["id"])
    assert parent["payload"]["guest_ban"]["status"] == "skipped"


async def test_mysql_global_link_audit_does_not_block_review_of_the_actual_ban(
    guard, mysql_repository
):
    parent = await blacklisted_source(guard, mysql_repository)
    await guard.guest_messages.resume(parent["id"])
    parent = await mysql_repository.get_case(parent["id"])
    audit = await mysql_repository.get_case(parent["payload"]["guest_ban"]["case_id"])
    case = await mysql_repository.get_case(audit["payload"]["guest_ban_case"])
    appeal = {
        "case_id": case["id"],
        "chat_id": CHAT,
        "user_id": CALLER,
        "payload": {"source_signature": source_signature(case)},
    }
    async with mysql_repository.lock(CALLER) as store:
        assert (await AppealWorkflow(guard).check_source(SimpleNamespace(guard=store), appeal))[
            "id"
        ] == case["id"]
