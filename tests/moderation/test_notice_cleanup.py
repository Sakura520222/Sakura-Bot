# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
from copy import deepcopy
from datetime import timedelta
from unittest.mock import AsyncMock

import pytest

from core.infrastructure.database.mysql import utc_now_naive
from core.moderation.moderator import GuardError, Moderator
from core.moderation.notification import review_buttons, stable_random_id

from .conftest import ADMIN, CHAT, USER
from .test_notification_controls import lifecycle_case, notice_event
from .test_verification import begin, callback


async def receipts(store, case):
    return {row["target_id"]: row for row in await store.notices(case["id"])}


async def legacy_result(guard, store, session, field):
    case = {
        "id": "f" * 32,
        "event_key": "e" * 64,
        "chat_id": CHAT,
        "user_id": USER,
        "action": "ban" if field == "verification_failure" else "verify_skip",
        "status": "pending" if field == "verification_failure" else "done",
        "payload": {
            field: {"session_id": session["id"]},
            "notification_cleanup_pending": True,
            "notification_cleanup_retry_at": "1970-01-01T00:00:00",
        },
    }
    await store.create_case(case)
    for target in [ADMIN, 11]:
        message_id = await guard.actions.send_once(
            target, "result", None, stable_random_id(f"legacy-result:{target}")
        )
        await store.notice(case["id"], target, message_id, case["status"], None)
    return await store.case(case["id"])


async def test_pass_sends_and_records_result_before_deleting_start_per_recipient(
    guard, store, monkeypatch
):
    session = await begin(guard, store)
    start = lifecycle_case(store, "verify_start")
    original = await receipts(store, start)
    delete = guard.client.delete_messages

    async def checked_delete(target, identifiers):
        if target in original:
            result = lifecycle_case(store, "verify_pass")
            sent = (await receipts(store, result))[target]
            assert sent["synced_status"] == "done" and not sent["error"]
            assert (target, sent["message_id"]) in guard.client.sent
        await delete(target, identifiers)

    monkeypatch.setattr(guard.client, "delete_messages", checked_delete)
    await guard.verification.callback(callback(session))
    for target, row in (await receipts(store, start)).items():
        assert (
            row["synced_status"] == "removed"
            and row["message_id"] == original[target]["message_id"]
        )
        assert (target, row["message_id"]) not in guard.client.sent
    result = lifecycle_case(store, "verify_pass")
    assert len(review_buttons(result, guard.group(CHAT))) == 2
    assert not (await store.case(result["id"]))["payload"]["notification_cleanup_pending"]
    assert await store.case(start["id"]) is not None


@pytest.mark.parametrize("action", ["ban", "kick", "mute", "warn", "allow"])
async def test_timeout_result_replaces_start_for_each_policy(guard, store, action):
    guard.group(CHAT).verification_timeout_action = action
    session = await begin(guard, store)
    start = lifecycle_case(store, "verify_start")
    session["expires_at"] = utc_now_naive() - timedelta(seconds=1)
    await store.save_verification(session)
    await guard.recover_once()
    result = next(row for row in store.cases.values() if row["payload"].get("verification_failure"))
    assert result["status"] != "executing"
    assert all(row["synced_status"] == "removed" for row in await store.notices(start["id"]))
    assert all(
        (row["target_id"], row["message_id"]) in guard.client.sent
        for row in await store.notices(result["id"])
    )


async def test_failed_result_delivery_keeps_only_that_admins_original_notice(guard, store):
    session = await begin(guard, store)
    start = lifecycle_case(store, "verify_start")
    initial = await receipts(store, start)
    guard.client.fail[ADMIN] = RuntimeError("recipient unavailable")
    await guard.verification.callback(callback(session))
    result = lifecycle_case(store, "verify_pass")
    assert (ADMIN, initial[ADMIN]["message_id"]) in guard.client.sent
    assert (11, initial[11]["message_id"]) not in guard.client.sent
    assert (await store.case(result["id"]))["payload"]["notification_cleanup_pending"]
    guard.client.fail.clear()
    store.notifications[(result["id"], ADMIN)]["retry_at"] = utc_now_naive() - timedelta(seconds=1)
    await guard.recover_once()
    assert (ADMIN, initial[ADMIN]["message_id"]) not in guard.client.sent
    assert all(row["synced_status"] == "removed" for row in await store.notices(start["id"]))


async def test_delete_failure_retries_after_restart_without_resending_result(
    guard, store, monkeypatch
):
    session = await begin(guard, store)
    start = lifecycle_case(store, "verify_start")
    delete = guard.client.delete_messages

    async def fail_one(target, identifiers):
        if target == ADMIN:
            raise RuntimeError("delete unavailable")
        await delete(target, identifiers)

    monkeypatch.setattr(guard.client, "delete_messages", fail_one)
    await guard.verification.callback(callback(session))
    result = lifecycle_case(store, "verify_pass")
    old = (await receipts(store, start))[ADMIN]
    assert old["synced_status"] == "deleting" and old["error"] == "cleanup:RuntimeError"
    final_receipts = await receipts(store, result)
    assert all(row["synced_status"] == "done" for row in final_receipts.values())
    send = AsyncMock(wraps=guard.actions.send_once)
    monkeypatch.setattr(guard.actions, "send_once", send)
    monkeypatch.setattr(guard.client, "delete_messages", delete)
    store.notifications[(start["id"], ADMIN)]["retry_at"] = utc_now_naive() - timedelta(seconds=1)
    restarted = Moderator(
        guard.client, guard.repository, guard.config, default_admins=guard.default_admins
    )
    restarted.actions.send_once = send
    await restarted.recover_once()
    assert (await receipts(store, start))[ADMIN]["synced_status"] == "removed"
    assert {target: row["message_id"] for target, row in final_receipts.items()} == {
        target: row["message_id"] for target, row in (await receipts(store, result)).items()
    }
    send.assert_not_awaited()


async def test_crash_after_delete_before_tombstone_is_recovered(guard, store, monkeypatch):
    session = await begin(guard, store)
    start = lifecycle_case(store, "verify_start")
    record = store.notice
    failed = False

    async def fail_once(case_id, target, message_id, status, error=None, attempts=0):
        nonlocal failed
        if case_id == start["id"] and target == ADMIN and status == "removed" and not failed:
            failed = True
            raise ConnectionError("commit failed after delete")
        await record(case_id, target, message_id, status, error, attempts)

    monkeypatch.setattr(store, "notice", fail_once)
    await guard.verification.callback(callback(session))
    assert failed
    assert (await receipts(store, start))[ADMIN]["synced_status"] == "deleting"
    await guard.recover_once()
    assert all(row["synced_status"] == "removed" for row in await store.notices(start["id"]))
    assert (await store.verification(CHAT, USER))["status"] == "passed"


async def test_tombstones_reject_old_callbacks_and_prevent_start_resurrection(guard, store):
    session = await begin(guard, store)
    start = deepcopy(lifecycle_case(store, "verify_start"))
    old_event = await notice_event(store, start, "b", prefix="n")
    await guard.verification.callback(callback(session))
    for row in await store.notices(start["id"]):
        await store.notice(start["id"], row["target_id"], row["message_id"], None, None)
    guard.group(CHAT).notification_ids = [ADMIN, 11, 13]
    before = deepcopy(guard.client.sent)
    await guard.notifier.sync(store, start, guard.group(CHAT))
    assert guard.client.sent == before
    assert all(row["synced_status"] == "removed" for row in await store.notices(start["id"]))
    with pytest.raises(GuardError, match="invalid_callback"):
        await guard.verification.notification_callback(old_event)


async def test_changed_recipients_keep_original_until_the_same_recipient_gets_result(guard, store):
    session = await begin(guard, store)
    start = lifecycle_case(store, "verify_start")
    initial = await receipts(store, start)
    guard.group(CHAT).notification_ids = [13]
    await guard.verification.callback(callback(session))
    assert all((target, row["message_id"]) in guard.client.sent for target, row in initial.items())
    result = lifecycle_case(store, "verify_pass")
    guard.group(CHAT).notification_ids = [13, ADMIN]
    await guard.notifier.sync(store, await store.case(result["id"]), guard.group(CHAT))
    assert (ADMIN, initial[ADMIN]["message_id"]) not in guard.client.sent
    assert (11, initial[11]["message_id"]) in guard.client.sent


async def test_disabled_cleanup_keeps_both_notices(guard, store):
    guard.group(CHAT).verification_notice_cleanup = False
    session = await begin(guard, store)
    start = lifecycle_case(store, "verify_start")
    await guard.verification.callback(callback(session))
    result = lifecycle_case(store, "verify_pass")
    assert not result["payload"]["notification_cleanup_enabled"]
    for case in [start, result]:
        assert all(
            (row["target_id"], row["message_id"]) in guard.client.sent
            for row in await store.notices(case["id"])
        )


async def test_disabled_result_event_never_deletes_the_only_start_notice(guard, store):
    guard.group(CHAT).notification_events = ["verification_start"]
    session = await begin(guard, store)
    start = lifecycle_case(store, "verify_start")
    await guard.verification.callback(callback(session))
    assert all(
        (row["target_id"], row["message_id"]) in guard.client.sent
        for row in await store.notices(start["id"])
    )
    assert not (await store.case(start["id"]))["payload"].get("notification_replaced_by")


async def test_unconfirmed_timeout_action_does_not_retire_start(guard, store):
    session = await begin(guard, store)
    start = lifecycle_case(store, "verify_start")
    session["expires_at"] = utc_now_naive() - timedelta(seconds=1)
    await store.save_verification(session)
    guard.client.fail["ban"] = RuntimeError("ban unavailable")
    await guard.recover_once()
    assert (await store.verification(CHAT, USER))["status"] == "kicking"
    assert all(
        row["synced_status"] not in {"deleting", "removed"}
        for row in await store.notices(start["id"])
    )
    guard.client.fail.clear()
    await guard.recover_once()
    assert all(row["synced_status"] == "removed" for row in await store.notices(start["id"]))


async def test_same_message_id_is_treated_as_replacement_without_deleting_result(guard, store):
    session = await begin(guard, store)
    start = lifecycle_case(store, "verify_start")
    source = (await receipts(store, start))[ADMIN]
    guard.client.fail[ADMIN] = RuntimeError("initial result delivery unavailable")
    await guard.verification.callback(callback(session))
    guard.client.fail.clear()
    result = lifecycle_case(store, "verify_pass")
    await store.notice(result["id"], ADMIN, source["message_id"], "done", None)
    guard.client.sent[(ADMIN, source["message_id"])] = {"text": "result", "buttons": None}
    await guard.notifier.sync(store, await store.case(result["id"]), guard.group(CHAT))
    assert (ADMIN, source["message_id"]) in guard.client.sent
    assert (await receipts(store, start))[ADMIN]["synced_status"] == "removed"


async def test_admin_skip_result_keeps_its_management_buttons_after_start_cleanup(guard, store):
    await begin(guard, store)
    start = lifecycle_case(store, "verify_start")
    await guard.verification.notification_callback(
        await notice_event(store, start, "s", prefix="n")
    )
    result = lifecycle_case(store, "verify_skip")
    assert len(review_buttons(result, guard.group(CHAT))) == 2
    assert all(row["synced_status"] == "removed" for row in await store.notices(start["id"]))
    await guard.verification.notification_callback(
        await notice_event(store, result, "b", prefix="n")
    )
    assert (await store.verification(CHAT, USER))["status"] == "admin_banned"


@pytest.mark.parametrize("field", ["verification_failure", "verification_admin"])
async def test_legacy_result_resolves_nested_session_id_and_completes_cleanup(guard, store, field):
    session = await begin(guard, store)
    parent = lifecycle_case(store, "verify_start")
    result = await legacy_result(guard, store, session, field)
    await guard.notifier.sync(store, result, guard.group(CHAT))
    saved = await store.case(result["id"])
    assert saved["payload"]["verification_session_id"] == session["id"]
    assert saved["payload"]["notification_cleanup_source"] == parent["id"]
    assert not saved["payload"]["notification_cleanup_pending"]
    assert all(row["synced_status"] == "removed" for row in await store.notices(parent["id"]))


async def test_unknown_legacy_reference_keeps_original_without_an_endless_job(guard, store):
    session = await begin(guard, store)
    parent = lifecycle_case(store, "verify_start")
    result = await legacy_result(guard, store, session, "verification_failure")
    result["payload"]["verification_failure"].pop("session_id")
    await store.update_case(result["id"], result["status"], payload=result["payload"])
    await guard.notifier.sync(store, result, guard.group(CHAT))
    assert not (await store.case(result["id"]))["payload"]["notification_cleanup_pending"]
    assert all(
        (row["target_id"], row["message_id"]) in guard.client.sent
        for row in await store.notices(parent["id"])
    )
