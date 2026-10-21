# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
from .conftest import USER
from .test_ban_history import reviewed_ban
from .test_config_api import api as api


async def test_admin_can_retry_only_history_cleanup_without_reexecuting_ban(api, guard, store):
    case = await reviewed_ban(guard, store)
    before = list(guard.client.calls)
    result = api.post(f"/api/guard/cases/{case['id']}/retry")
    assert result.status_code == 200
    assert result.json()["case"]["payload"]["ban_message_cleanup"]["status"] == "deleted"
    assert guard.client.calls == before


async def test_history_retry_route_requires_current_administrator(api, guard, store):
    case = await reviewed_ban(guard, store)
    assert (
        api.post(
            f"/api/guard/cases/{case['id']}/retry", headers={"x-test-user": str(USER)}
        ).status_code
        == 403
    )
    guard.ban_history.revoke.assert_not_awaited()


async def test_history_retry_after_unban_cancels_without_rebanning(api, guard, store):
    from .conftest import ADMIN

    case = await reviewed_ban(guard, store)
    await guard.reviews.review(case["id"], "unban", ADMIN)
    before = list(guard.client.calls)
    result = api.post(f"/api/guard/cases/{case['id']}/retry")
    assert result.status_code == 200
    assert result.json()["case"]["payload"]["ban_message_cleanup"]["status"] == "cancelled"
    assert guard.client.calls == before
    guard.ban_history.revoke.assert_not_awaited()
