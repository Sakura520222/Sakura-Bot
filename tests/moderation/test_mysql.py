# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
"""真实 MySQL 集成测试。仅在显式设置 GUARD_MYSQL_TEST_DSN 时运行。

每个测试创建并删除一个独立、随机命名的 schema，不访问应用配置或现有表。
"""

import asyncio
import os
import uuid
from datetime import timedelta
from urllib.parse import unquote, urlparse

import aiomysql
import pytest

from core.infrastructure.database.mysql import utc_now_naive
from core.moderation.matcher import Match
from core.moderation.moderator import GuardError, Moderator
from core.moderation.repository import GuardRepository, GuardStore

from .conftest import ADMIN, CHAT, USER

# 在根 conftest 的环境清理 fixture 执行前捕获专用测试连接；从不复用 MYSQL_*。
TEST_DSN = os.environ.get("GUARD_MYSQL_TEST_DSN")
pytestmark = [
    pytest.mark.integration,
    pytest.mark.database,
    pytest.mark.skipif(not TEST_DSN, reason="GUARD_MYSQL_TEST_DSN not configured"),
]


@pytest.fixture
async def mysql_repository():
    parsed = urlparse(TEST_DSN)
    parameters = {
        "host": parsed.hostname or "127.0.0.1",
        "port": parsed.port or 3306,
        "user": unquote(parsed.username or "root"),
        "password": unquote(parsed.password or ""),
        "autocommit": True,
        "charset": "utf8mb4",
    }
    database = "sakura_guard_test_" + uuid.uuid4().hex
    admin = await aiomysql.connect(**parameters)
    pool = None
    try:
        async with admin.cursor() as cursor:
            await cursor.execute(f"CREATE DATABASE `{database}` CHARACTER SET utf8mb4")
        pool = await aiomysql.create_pool(**parameters, db=database, minsize=1, maxsize=5)
        repository = GuardRepository(pool)
        await repository.initialize()
        yield repository
    finally:
        if pool is not None:
            pool.close()
            await pool.wait_closed()
        async with admin.cursor() as cursor:
            await cursor.execute(f"DROP DATABASE IF EXISTS `{database}`")
        admin.close()


async def create_ban(guard, repository):
    guard.repository = repository
    async with repository.lock(USER) as store:
        return await guard.punish(
            store,
            CHAT,
            guard.client.users[USER],
            Match("nickname", "nick", "主页", "主頁🌸", "主页🌸"),
            "mysql-event",
        )


async def test_schema_case_and_whitelist_survive_repository_restart(guard, mysql_repository):
    repository = mysql_repository
    await repository.initialize()
    case = await create_ban(guard, repository)
    assert case["status"] == "pending" and case["payload"]["evidence"]["original"] == "主頁🌸"
    await guard.reviews.review(case["id"], "allow", ADMIN)
    restarted = GuardRepository(repository.pool)
    assert (await restarted.get_case(case["id"]))["status"] == "allowed"
    async with restarted.lock(USER) as store:
        assert await store.whitelisted(CHAT, USER)
        assert not await store.whitelisted(CHAT - 1, USER)
        with pytest.raises(aiomysql.IntegrityError):
            await store.create_case({**case, "id": uuid.uuid4().hex})


@pytest.mark.parametrize(
    "action,status", [("unban", "ignored"), ("ban", "pending"), ("blacklist", "blacklisted")]
)
async def test_mysql_user_report_is_deduplicated_and_reviewed(
    guard, mysql_repository, action, status
):
    from .test_reports import send

    guard.repository = mysql_repository
    await send(guard)
    rows = await mysql_repository.query("cases", chat_id=CHAT, status="pending")
    assert rows["total"] == 1
    case = rows["rows"][0]
    assert case["action"] == "report" and not guard.client.calls
    # 新 Repository 实例仍通过数据库原消息键去重，并可使用真实 SQL 认领举报审核。
    guard.repository = GuardRepository(mysql_repository.pool)
    await send(guard)
    assert (await guard.repository.query("cases"))["total"] == 1
    result = await guard.reviews.review(case["id"], action, ADMIN)
    assert result["status"] == status
    async with guard.repository.lock(USER) as store:
        assert bool(await store.blacklist(USER)) == (action == "blacklist")
        assert not await store.whitelisted(CHAT, USER)


async def test_history_cleanup_persists_per_bot_without_touching_guard_data(mysql_repository):
    repository = mysql_repository
    await repository.initialize()
    await repository.mark_chat_history_cleaned(999, CHAT, ADMIN)
    await repository.mark_chat_history_cleaned(1000, CHAT, "admin")
    await repository.mark_chat_history_cleaned(999, CHAT, "admin")
    restarted = GuardRepository(repository.pool)
    assert await restarted.cleaned_chat_history(999) == {CHAT}
    assert await restarted.cleaned_chat_history(1000) == {CHAT}
    await restarted.restore_chat_history(999, {CHAT, CHAT - 1})
    assert not await repository.cleaned_chat_history(999)
    assert await repository.cleaned_chat_history(1000) == {CHAT}
    assert (await repository.query("cases"))["total"] == 0


async def test_mysql_multi_instance_opposing_reviews_and_permanent_identity(
    guard, mysql_repository
):
    case = await create_ban(guard, mysql_repository)
    other = Moderator(
        guard.client,
        GuardRepository(mysql_repository.pool),
        guard.config,
        default_admins=guard.default_admins,
    )
    other.bot_id = guard.bot_id
    results = await asyncio.gather(
        guard.reviews.review(case["id"], "blacklist", ADMIN),
        other.reviews.review(case["id"], "allow", 11),
    )
    assert len({result["status"] for result in results}) == 1
    assert results[0]["status"] in {"allowed", "blacklisted"}
    async with mysql_repository.lock(USER) as store:
        black, white = await store.blacklist(USER), await store.whitelisted(CHAT, USER)
        assert bool(black) != white
        if black:
            guard.client.users[USER].first_name = "renamed"
            assert (await store.blacklist(USER))["user_id"] == USER


async def test_finish_review_transaction_rolls_back_whitelist_when_case_commit_fails(
    guard, mysql_repository, monkeypatch
):
    case = await create_ban(guard, mysql_repository)
    async with mysql_repository.lock(USER) as store:
        await store.claim_review(case["id"], "allow", ADMIN)
        original = store.update_case

        async def fail(*args, **kwargs):
            raise aiomysql.OperationalError(2013, "injected commit failure")

        monkeypatch.setattr(store, "update_case", fail)
        with pytest.raises(aiomysql.OperationalError):
            await store.finish_review(await store.case(case["id"]), "allow")
        assert not await store.whitelisted(CHAT, USER)
        assert (await store.case(case["id"]))["status"] == "reviewing_allow"
        monkeypatch.setattr(store, "update_case", original)
        await store.finish_review(await store.case(case["id"]), "allow")
        assert await store.whitelisted(CHAT, USER)


async def test_ban_commit_failure_is_recovered_without_new_case(
    guard, mysql_repository, monkeypatch
):
    original = GuardStore.update_case
    failures = 1

    async def fail_once(self, case_id, status, **kwargs):
        nonlocal failures
        if status == "pending" and failures:
            failures -= 1
            raise aiomysql.OperationalError(2013, "injected failure after Telegram success")
        await original(self, case_id, status, **kwargs)

    monkeypatch.setattr(GuardStore, "update_case", fail_once)
    with pytest.raises(aiomysql.OperationalError):
        await create_ban(guard, mysql_repository)
    assert (await mysql_repository.query("cases"))["rows"][0]["status"] == "executing"
    await guard.recover_once()
    result = await mysql_repository.query("cases")
    assert result["total"] == 1 and result["rows"][0]["status"] == "pending"


async def test_verification_persistence_restart_timeout_and_notification_filters(
    guard, mysql_repository
):
    guard.repository = mysql_repository
    guard.config.groups[str(CHAT)].verify_join = True
    await guard.process_join(CHAT, guard.client.users[USER], "join-mysql")
    async with mysql_repository.lock(USER) as store:
        session = await store.verification(CHAT, USER)
        assert session["status"] == "waiting"
        session["expires_at"] = utc_now_naive() - timedelta(seconds=1)
        await store.save_verification(session)
    restarted = Moderator(
        guard.client,
        GuardRepository(mysql_repository.pool),
        guard.config,
        default_admins=guard.default_admins,
    )
    restarted.bot_id = guard.bot_id
    await restarted.recover_once()
    assert (await mysql_repository.get_verification(session["id"]))["status"] == "failed_banned"
    await create_ban(guard, mysql_repository)
    assert (
        await mysql_repository.query("notifications", chat_id=CHAT, user_id=USER, status="pending")
    )["total"] == 2
    assert (await mysql_repository.query("notifications", chat_id=CHAT - 1))["total"] == 0


async def test_user_lock_releases_on_exception_across_connections(mysql_repository):
    with pytest.raises(RuntimeError):
        async with mysql_repository.lock(USER):
            raise RuntimeError("cancelled operation")
    async with asyncio.timeout(1):
        async with mysql_repository.lock(USER) as store:
            assert await store.one("SELECT 1 AS healthy") == {"healthy": 1}


async def test_webui_actor_is_persisted_without_fabricating_a_telegram_user(
    guard, mysql_repository
):
    case = await create_ban(guard, mysql_repository)
    result = await guard.reviews.review(case["id"], "blacklist", "admin", web=True)
    assert result["reviewer_id"] is None and result["reviewer_actor"] == "webui:admin"
    async with mysql_repository.lock(USER) as store:
        entry = await store.blacklist(USER)
        assert entry["reviewer_id"] is None and entry["reviewer_actor"] == "webui:admin"


async def test_shared_global_match_updates_last_seen_and_overrides_local_whitelist(
    guard, mysql_repository
):
    case = await create_ban(guard, mysql_repository)
    await guard.reviews.review(case["id"], "blacklist", ADMIN)
    guard.config.groups[str(CHAT)].share_global_blacklist = True
    before = utc_now_naive() - timedelta(days=1)
    async with mysql_repository.lock(USER) as store:
        await store.set_whitelist(CHAT, USER, ADMIN)
        await store.execute(
            "UPDATE moderation_blacklist SET last_seen_at=%s WHERE user_id=%s", (before, USER)
        )
        matched = await guard.match(store, guard.group(CHAT), CHAT, guard.client.users[USER])
        assert matched.target == "global_blacklist"
        assert (await store.blacklist(USER))["last_seen_at"] > before


async def test_retention_preserves_pending_cases_and_permanent_blacklist(guard, mysql_repository):
    case = await create_ban(guard, mysql_repository)
    await guard.reviews.review(case["id"], "blacklist", ADMIN)
    pending_id = uuid.uuid4().hex
    async with mysql_repository.lock(USER) as store:
        await store.create_case(
            {**case, "id": pending_id, "event_key": pending_id.ljust(64, "0"), "status": "pending"}
        )
        await store.execute(
            "UPDATE moderation_cases SET created_at=%s", (utc_now_naive() - timedelta(days=30),)
        )
    await mysql_repository.cleanup(7)
    assert await mysql_repository.get_case(case["id"]) is None
    assert (await mysql_repository.get_case(pending_id))["status"] == "pending"
    async with mysql_repository.lock(USER) as store:
        assert await store.blacklist(USER)


@pytest.mark.parametrize(
    ("button", "result"),
    [("b", "admin_banned"), ("x", "admin_blacklisted"), ("s", "admin_skipped")],
)
async def test_mysql_verification_admin_actions_record_actor_and_survive_restart(
    guard, mysql_repository, button, result
):
    from .test_verification_admin import admin_button

    guard.repository = mysql_repository
    guard.config.groups[str(CHAT)].verify_join = True
    await guard.process_join(CHAT, guard.client.users[USER], "mysql-admin-join")
    async with mysql_repository.lock(USER) as store:
        session = await store.verification(CHAT, USER)
    await guard.verification.callback(admin_button(session, button))
    assert (await mysql_repository.get_verification(session["id"]))["status"] == result
    rows = (await mysql_repository.query("cases", chat_id=CHAT, user_id=USER))["rows"]
    manual = [case for case in rows if case["payload"].get("verification_admin")]
    assert len(manual) == 1
    assert manual[0]["reviewer_id"] == ADMIN and manual[0]["reviewer_actor"] == f"telegram:{ADMIN}"
    assert manual[0]["reviewed_at"] is not None
    async with mysql_repository.lock(USER) as store:
        assert bool(await store.blacklist(USER)) is (button == "x")
        assert not await store.whitelisted(CHAT, USER)
    await guard.recover_once()
    assert (await mysql_repository.get_verification(session["id"]))["status"] == result


async def test_mysql_terminal_message_cleanup_is_selected_and_retained_until_success(
    guard, mysql_repository, monkeypatch
):
    from unittest.mock import AsyncMock

    guard.repository = mysql_repository
    guard.config.groups[str(CHAT)].verify_join = True
    await guard.process_join(CHAT, guard.client.users[USER], "mysql-cleanup-join")
    async with mysql_repository.lock(USER) as store:
        session = await store.verification(CHAT, USER)
        session["expires_at"] = utc_now_naive() - timedelta(seconds=1)
        await store.save_verification(session)
    delete = guard.client.delete_messages
    monkeypatch.setattr(
        guard.client, "delete_messages", AsyncMock(side_effect=RuntimeError("offline"))
    )
    await guard.recover_once()
    saved = await mysql_repository.get_verification(session["id"])
    assert saved["status"] == "failed_banned" and saved["payload"]["cleanup_pending"]
    _, work = await mysql_repository.work()
    assert session["id"] in {row["id"] for row in work}
    async with mysql_repository.lock(USER) as store:
        await store.execute(
            "UPDATE moderation_verification_sessions SET updated_at=%s WHERE id=%s",
            (utc_now_naive() - timedelta(days=30), session["id"]),
        )
    await mysql_repository.cleanup(7)
    assert await mysql_repository.get_verification(session["id"])
    monkeypatch.setattr(guard.client, "delete_messages", delete)
    await guard.recover_once()
    assert (await mysql_repository.get_verification(session["id"]))["payload"][
        "cleanup_pending"
    ] is False
    assert (CHAT, session["message_id"]) not in guard.client.sent
    _, work = await mysql_repository.work()
    assert session["id"] not in {row["id"] for row in work}


async def test_mysql_unrecorded_completion_event_is_selected_after_message_cleanup(
    guard, mysql_repository
):
    from .test_verification import callback

    guard.repository = mysql_repository
    guard.config.groups[str(CHAT)].verify_join = True
    await guard.process_join(CHAT, guard.client.users[USER], "mysql-event-join")
    async with mysql_repository.lock(USER) as store:
        session = await store.verification(CHAT, USER)
    await guard.verification.callback(callback(session))
    async with mysql_repository.lock(USER) as store:
        saved = await store.verification_by_id(session["id"])
        saved["payload"].pop("lifecycle_recorded")
        await store.save_verification(saved)
    _, work = await mysql_repository.work()
    assert session["id"] in {row["id"] for row in work}
    await guard.recover_once()
    saved = await mysql_repository.get_verification(session["id"])
    assert saved["payload"]["lifecycle_recorded"] is True
    events = (await mysql_repository.query("cases", chat_id=CHAT, user_id=USER))["rows"]
    assert sum(case["action"] == "verify_pass" for case in events) == 1


async def test_mysql_unproven_passing_never_releases_after_repository_restart(
    guard, mysql_repository
):
    guard.repository = mysql_repository
    guard.config.groups[str(CHAT)].verify_join = True
    await guard.process_join(CHAT, guard.client.users[USER], "mysql-unproven-passing")
    async with mysql_repository.lock(USER) as store:
        session = await store.verification(CHAT, USER)
        session["status"] = "passing"
        await store.save_verification(session)
    await guard.recover_once()
    saved = await mysql_repository.get_verification(session["id"])
    assert saved["status"] == "waiting" and saved["expires_at"] == session["expires_at"]
    assert guard.client.member_rights[(CHAT, USER)]["send_messages"]


async def test_mysql_answer_receipt_survives_json_roundtrip_and_restart(guard, mysql_repository):
    from core.moderation.verification import verified_answer

    from .test_verification import callback

    guard.repository = mysql_repository
    guard.config.groups[str(CHAT)].verify_join = True
    await guard.process_join(CHAT, guard.client.users[USER], "mysql-answer-receipt")
    async with mysql_repository.lock(USER) as store:
        session = await store.verification(CHAT, USER)
    guard.client.fail["restrict"] = RuntimeError("injected recovery failure")
    await guard.verification.callback(callback(session))
    saved = await mysql_repository.get_verification(session["id"])
    assert saved["status"] == "passing" and verified_answer(saved)
    guard.client.fail.clear()
    restarted = Moderator(
        guard.client, mysql_repository, guard.config, default_admins=guard.default_admins
    )
    await restarted.recover_once()
    assert (await mysql_repository.get_verification(session["id"]))["status"] == "passed"


async def test_mysql_held_verification_is_recovered_and_not_deleted_by_retention(
    guard, mysql_repository
):
    from .test_verification_admin import admin_button

    guard.repository = mysql_repository
    guard.config.groups[str(CHAT)].verify_join = True
    guard.config.groups[str(CHAT)].verification_timeout_action = "allow"
    await guard.process_join(CHAT, guard.client.users[USER], "mysql-held")
    async with mysql_repository.lock(USER) as store:
        session = await store.verification(CHAT, USER)
        session["expires_at"] = utc_now_naive() - timedelta(seconds=1)
        await store.save_verification(session)
    await guard.recover_once()
    saved = await mysql_repository.get_verification(session["id"])
    assert saved["status"] == "held"
    async with mysql_repository.lock(USER) as store:
        await store.execute(
            "UPDATE moderation_verification_sessions SET updated_at=%s WHERE id=%s",
            (utc_now_naive() - timedelta(days=30), session["id"]),
        )
    await mysql_repository.cleanup(7)
    _, work = await mysql_repository.work()
    assert session["id"] in {row["id"] for row in work}
    assert guard.client.member_rights[(CHAT, USER)]["send_messages"]
    await guard.verification.callback(admin_button(saved, "s"))
    assert (await mysql_repository.get_verification(session["id"]))["status"] == "admin_skipped"


async def test_mysql_legacy_skip_can_be_reauthorized_without_unique_event_conflict(
    guard, mysql_repository
):
    from .test_verification_admin import admin_button

    guard.repository = mysql_repository
    guard.config.groups[str(CHAT)].verify_join = True
    await guard.process_join(CHAT, guard.client.users[USER], "mysql-skip-reauthorize")
    async with mysql_repository.lock(USER) as store:
        session = await store.verification(CHAT, USER)
    guard.client.fail["restrict"] = RuntimeError("injected failure")
    await guard.verification.callback(admin_button(session, "s"))
    guard.client.fail.clear()
    async with mysql_repository.lock(USER) as store:
        saved = await store.verification(CHAT, USER)
        saved["payload"]["admin_action"].pop("authorized_at")
        case_id = saved["payload"]["admin_action"]["case_id"]
        case = await store.case(case_id)
        case["payload"]["verification_admin"].pop("authorized_at")
        await store.update_case(case_id, "executing", payload=case["payload"])
        await store.save_verification(saved)
    await guard.recover_once()
    assert (await mysql_repository.get_verification(session["id"]))["status"] == "admin_pending"
    assert guard.client.member_rights[(CHAT, USER)]["send_messages"]
    await guard.verification.callback(admin_button(saved, "s"))
    assert (await mysql_repository.get_verification(session["id"]))["status"] == "admin_skipped"


async def test_mysql_replacement_updates_the_same_start_notification_links(guard, mysql_repository):
    guard.repository = mysql_repository
    guard.config.groups[str(CHAT)].verify_join = True
    await guard.process_join(CHAT, guard.client.users[USER], "mysql-notice-link")
    async with mysql_repository.lock(USER) as store:
        session = await store.verification(CHAT, USER)
    cases = (await mysql_repository.query("cases"))["rows"]
    initial = next(case for case in cases if case["action"] == "verify_start")
    async with mysql_repository.lock(USER) as store:
        notices = await store.notices(initial["id"])
    await guard.client.delete_messages(CHAT, [session["message_id"]])
    await guard.recover_once()
    saved = await mysql_repository.get_verification(session["id"])
    case = await mysql_repository.get_case(initial["id"])
    assert case["payload"]["message_id"] == saved["message_id"] != session["message_id"]
    async with mysql_repository.lock(USER) as store:
        current_notices = await store.notices(initial["id"])
    assert {row["target_id"]: row["message_id"] for row in notices} == {
        row["target_id"]: row["message_id"] for row in current_notices
    }
    for notice in current_notices:
        text = guard.client.sent[(notice["target_id"], notice["message_id"])]["text"]
        assert f"验证消息链接：https://t.me/c/1234567890/{saved['message_id']}" in text


async def test_mysql_only_unban_does_not_write_whitelist_and_survives_restart(
    guard, mysql_repository
):
    case = await create_ban(guard, mysql_repository)
    result = await guard.reviews.review(case["id"], "unban", ADMIN)
    assert result["status"] == "unbanned"
    async with mysql_repository.lock(USER) as store:
        assert not await store.whitelisted(CHAT, USER)
        assert not await store.blacklist(USER)
    restarted = GuardRepository(mysql_repository.pool)
    saved = await restarted.get_case(case["id"])
    assert saved["payload"]["review_history"][-1]["action"] == "unban"
    assert saved["reviewer_actor"] == f"telegram:{ADMIN}"


@pytest.mark.parametrize("kind", ["mute", "warn"])
@pytest.mark.parametrize(
    ("action", "expected"),
    [("unban", None), ("allow", "allowed"), ("ban", "pending"), ("blacklist", "blacklisted")],
)
async def test_mysql_mute_and_warning_notice_reviews(
    guard, mysql_repository, kind, action, expected
):
    guard.repository = mysql_repository
    async with mysql_repository.lock(USER) as store:
        case = await guard.punish(
            store,
            CHAT,
            guard.client.users[USER],
            Match("message", "test-word", "test", "1", "1", kind),
            "mysql-notice-controls",
        )
    result = await guard.reviews.review(case["id"], action, ADMIN, expected_generation=0)
    assert result["status"] == (expected or ("unmuted" if kind == "mute" else "ignored"))
    async with mysql_repository.lock(USER) as store:
        assert bool(await store.whitelisted(CHAT, USER)) is (action == "allow")
        assert bool(await store.blacklist(USER)) is (action == "blacklist")
        if action == "ban":
            assert (await store.active_ban(CHAT, USER))["id"] == case["id"]
    if action == "ban":
        with pytest.raises(GuardError, match="invalid_callback"):
            await guard.reviews.review(case["id"], "allow", ADMIN, expected_generation=0)
        assert (await guard.reviews.review(case["id"], "unban", ADMIN, expected_generation=1))[
            "status"
        ] == "unbanned"


async def test_mysql_notification_can_ban_after_verification_passes(guard, mysql_repository):
    from .test_notification_controls import notice_event
    from .test_verification import callback

    guard.repository = mysql_repository
    guard.group(CHAT).verify_join = True
    await guard.process_join(CHAT, guard.client.users[USER], "mysql-notice-pass")
    async with mysql_repository.lock(USER) as store:
        session = await store.verification(CHAT, USER)
    await guard.verification.callback(callback(session))
    case = next(
        row
        for row in (await mysql_repository.query("cases"))["rows"]
        if row["action"] == "verify_pass"
    )
    async with mysql_repository.lock(USER) as store:
        event = await notice_event(store, case, "b", prefix="n")
    await guard.verification.notification_callback(event)
    assert (await mysql_repository.get_verification(session["id"]))["status"] == "admin_banned"


async def test_mysql_old_mute_detects_later_indefinite_mute_within_the_same_second(
    guard, mysql_repository
):
    guard.repository = mysql_repository
    guard.client.member_rights[(CHAT, USER)]["send_polls"] = True
    async with mysql_repository.lock(USER) as store:
        first = await guard.punish(
            store,
            CHAT,
            guard.client.users[USER],
            Match("message", "word", "test", "1", "1", "mute"),
            "mysql-old-mute",
        )
        second = await guard.punish(
            store,
            CHAT,
            guard.client.users[USER],
            Match("message", "word", "test", "1", "1", "mute"),
            "mysql-new-mute",
        )
        assert (await store.later_mute(first))["id"] == second["id"]
    with pytest.raises(GuardError):
        await guard.reviews.review(first["id"], "unban", ADMIN)
    assert guard.client.member_rights[(CHAT, USER)]["send_messages"]
