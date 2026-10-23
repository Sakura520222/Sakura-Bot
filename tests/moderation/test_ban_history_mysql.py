# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
from datetime import timedelta
from unittest.mock import AsyncMock, patch

import pytest

from core.infrastructure.database.mysql import utc_now_naive
from core.moderation.moderator import Moderator

from .conftest import ADMIN, USER
from .test_mysql import TEST_DSN
from .test_mysql import mysql_repository as mysql_repository
from .test_reports import send

pytestmark = [
    pytest.mark.integration,
    pytest.mark.database,
    pytest.mark.skipif(not TEST_DSN, reason="GUARD_MYSQL_TEST_DSN not configured"),
]


async def ban(guard, repository, action="ban"):
    guard.repository = repository
    await send(guard)
    case = (await repository.query("cases"))["rows"][0]
    # 模拟提交终态后尚未来得及即时清理即退出，覆盖持久化恢复路径。
    with patch.object(guard.ban_history, "finish", AsyncMock()):
        return await guard.reviews.review(case["id"], action, ADMIN)


@pytest.mark.parametrize("action", ["ban", "blacklist"])
async def test_null_retry_deadline_is_selected_and_cleanup_survives_restart(
    guard, mysql_repository, action
):
    repository = mysql_repository
    case = await ban(guard, repository, action)
    assert case["payload"]["ban_message_cleanup"]["retry_at"] is None
    current = await repository.get_case(case["id"])
    assert not current["payload"]["ban_message_cleanup_dirty"]
    cases, _ = await repository.work()
    assert case["id"] in {row["id"] for row in cases}
    restarted = Moderator(
        guard.client, repository, guard.config, default_admins=guard.default_admins
    )
    restarted.bot_id = guard.bot_id
    restarted.ban_history.revoke = AsyncMock(return_value=True)
    await restarted.recover_once()
    saved = await repository.get_case(case["id"])
    assert saved["payload"]["ban_message_cleanup"]["status"] == "deleted"
    assert not saved["payload"]["ban_message_cleanup_dirty"]
    restarted.ban_history.revoke.assert_awaited_once()
    await restarted.recover_once()
    assert restarted.ban_history.revoke.await_count == 1


async def test_future_cleanup_deadline_waits_then_sql_recovery_retries(guard, mysql_repository):
    repository = mysql_repository
    case = await ban(guard, repository)
    guard.ban_history.revoke.side_effect = RuntimeError("API unavailable")
    await guard.recover_once()
    cases, _ = await repository.work()
    assert case["id"] not in {row["id"] for row in cases}
    async with repository.lock(USER) as store:
        current = await store.case(case["id"])
        current["payload"]["ban_message_cleanup"]["retry_at"] = (
            utc_now_naive() - timedelta(seconds=1)
        ).isoformat()
        await store.update_case(case["id"], current["status"], payload=current["payload"])
    cases, _ = await repository.work()
    assert case["id"] in {row["id"] for row in cases}
    guard.ban_history.revoke.side_effect = None
    await guard.recover_once()
    assert (await repository.get_case(case["id"]))["payload"]["ban_message_cleanup"][
        "status"
    ] == "deleted"


async def test_retention_preserves_pending_history_cleanup_until_finished(guard, mysql_repository):
    repository = mysql_repository
    case = await ban(guard, repository, "blacklist")
    async with repository.lock(USER) as store:
        await store.execute(
            "UPDATE moderation_cases SET created_at=%s WHERE id=%s",
            (utc_now_naive() - timedelta(days=100), case["id"]),
        )
    await repository.cleanup(90)
    assert await repository.get_case(case["id"])
    await guard.recover_once()
    await repository.cleanup(90)
    assert await repository.get_case(case["id"]) is None
