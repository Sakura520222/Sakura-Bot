# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
from types import SimpleNamespace

import pytest

from .conftest import ADMIN, CHAT, USER
from .test_verification import begin, callback
from .test_verification_admin import admin_button


def start_case(store):
    return next(case for case in store.cases.values() if case["action"] == "verify_start")


@pytest.mark.parametrize("username", [None, "FireflyChat520"])
async def test_start_notification_links_to_the_actual_public_or_private_challenge(
    guard, store, monkeypatch, username
):
    get_entity = guard.client.get_entity

    async def entity(identifier):
        if identifier == CHAT:
            return SimpleNamespace(id=abs(CHAT), title="测试群", username=username, megagroup=True)
        return await get_entity(identifier)

    monkeypatch.setattr(guard.client, "get_entity", entity)
    session = await begin(guard, store)
    base = "https://t.me/FireflyChat520" if username else "https://t.me/c/1234567890"
    link = f"{base}/{session['message_id']}"
    case = start_case(store)
    assert case["payload"]["message_id"] == session["message_id"]
    assert case["payload"]["verification_session_id"] == session["id"]
    assert case["payload"]["evidence_link"] == link
    for notice in await store.notices(case["id"]):
        text = guard.client.sent[(notice["target_id"], notice["message_id"])]["text"]
        assert f"验证消息链接：{link}" in text
        assert f"证据消息 ID：{session['message_id']}" in text


async def test_replacement_challenge_updates_existing_notices_without_duplicates(guard, store):
    old = await begin(guard, store)
    case = start_case(store)
    notices = await store.notices(case["id"])
    await guard.client.delete_messages(CHAT, [old["message_id"]])
    await guard.recover_once()
    current = await store.verification(CHAT, USER)
    assert old["message_id"] != current["message_id"]
    assert len([row for row in store.cases.values() if row["action"] == "verify_start"]) == 1
    assert current["payload"]["start_message_id"] == current["message_id"]
    for old_notice in notices:
        new_notice = next(
            row
            for row in await store.notices(case["id"])
            if row["target_id"] == old_notice["target_id"]
        )
        assert new_notice["message_id"] == old_notice["message_id"]
        text = guard.client.sent[(new_notice["target_id"], new_notice["message_id"])]["text"]
        assert f"验证消息链接：https://t.me/c/1234567890/{current['message_id']}\n" in text
        assert f"验证消息链接：https://t.me/c/1234567890/{old['message_id']}\n" not in text


async def test_old_active_start_notice_is_backfilled_once(guard, store):
    session = await begin(guard, store)
    session["payload"].pop("start_message_id")
    await store.save_verification(session)
    case = start_case(store)
    case["payload"].update(message_id=None, evidence_link="")
    await store.update_case(case["id"], case["status"], payload=case["payload"])
    before = {row["target_id"]: row["message_id"] for row in await store.notices(case["id"])}
    await guard.recover_once()
    assert start_case(store)["payload"]["message_id"] == session["message_id"]
    assert {
        row["target_id"]: row["message_id"] for row in await store.notices(case["id"])
    } == before
    after = list(guard.client.calls)
    await guard.recover_once()
    assert guard.client.calls == after


@pytest.mark.parametrize("operation", ["answer", "ban", "skip"])
async def test_completion_and_admin_notices_keep_the_challenge_reference(guard, store, operation):
    session = await begin(guard, store)
    event = (
        callback(session)
        if operation == "answer"
        else admin_button(session, "b" if operation == "ban" else "s")
    )
    await guard.verification.callback(event)
    cases = [row for row in store.cases.values() if row["action"] != "verify_start"]
    assert cases
    for case in cases:
        assert case["payload"]["message_id"] == session["message_id"]
        for notice in await store.notices(case["id"]):
            text = guard.client.sent[(notice["target_id"], notice["message_id"])]["text"]
            assert f"验证消息链接：https://t.me/c/1234567890/{session['message_id']}" in text


async def test_failed_verification_notice_points_to_the_challenge(guard, store):
    session = await begin(guard, store)
    for _ in range(3):
        await guard.verification.callback(callback(session, correct=False))
    case = next(row for row in store.cases.values() if row["payload"].get("verification_failure"))
    assert case["payload"]["message_id"] == session["message_id"]
    notice = next(row for row in await store.notices(case["id"]) if row["target_id"] == ADMIN)
    assert f"/{session['message_id']}" in guard.client.sent[(ADMIN, notice["message_id"])]["text"]
