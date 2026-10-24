# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
"""访客来源与历史扫描意图在真实 MySQL 中可恢复。"""

import uuid

import pytest

from core.moderation.deleted_cleanup import DeletedUserCleanup

from .conftest import ADMIN, CHAT
from .test_deleted_cleanup import deleted as deleted
from .test_deleted_cleanup import drain
from .test_deleted_cleanup_mysql import attach
from .test_deleted_history import history as history
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
