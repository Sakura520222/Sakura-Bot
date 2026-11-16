import asyncio
import hashlib
import io
from datetime import UTC, timedelta
from types import SimpleNamespace
from unittest.mock import AsyncMock

import aiomysql
import pytest
from telethon import events

from core.infrastructure.database.mysql import utc_now_naive
from core.moderation import observation_media
from core.moderation.moderator import GuardError, Moderator
from core.moderation.observation import profile_snapshot
from core.moderation.observation_media import scan_qr

from .conftest import CHAT, USER
from .test_verification import callback


@pytest.fixture(autouse=True)
def configured_assistant(monkeypatch):
    monkeypatch.setenv("QA_BOT_ENABLED", "true")
    monkeypatch.setenv("QA_BOT_TOKEN", "123:synthetic-fixture")


async def test_permission_failure_is_recovered_after_restart(guard, store):
    guard.group(CHAT).verify_join = True
    guard.client.fail["permissions"] = TimeoutError()
    await asyncio.gather(
        *[guard.process_join(CHAT, guard.client.users[USER], "join-1") for _ in range(2)]
    )
    deferred = await store.verification(CHAT, USER)
    assert deferred["status"] == "checking"
    assert not deferred["message_id"] and not guard.client.calls

    guard.client.fail.clear()
    restarted = Moderator(guard.client, guard.repository, guard.config, guard.default_admins)
    restarted.bot_id = guard.bot_id
    await restarted.recover_once()
    waiting = await store.verification(CHAT, USER)
    assert waiting["id"] == deferred["id"]
    assert (
        waiting["status"] == "waiting" and guard.client.member_rights[CHAT, USER]["send_messages"]
    )


async def test_permission_failure_does_not_mute_confirmed_admin(guard, store):
    guard.group(CHAT).verify_join = True
    guard.client.fail["permissions"] = TimeoutError()
    await guard.process_join(CHAT, guard.client.users[USER], "join-1")
    guard.client.fail.clear()
    guard.client.admins.add(USER)
    await guard.recover_once()
    assert (await store.verification(CHAT, USER))["status"] == "cancelled"
    assert not guard.client.calls


async def test_join_is_retried_after_initial_database_outage(guard, store, monkeypatch):
    guard.group(CHAT).verify_join = True
    original = guard.repository.lock

    def unavailable(user_id):
        raise aiomysql.OperationalError(2006, "test database outage")

    monkeypatch.setattr(guard.repository, "lock", unavailable)
    await guard.process_join(CHAT, guard.client.users[USER], "db-outage-join")
    assert await store.verification(CHAT, USER) is None

    monkeypatch.setattr(guard.repository, "lock", original)
    await guard.recover_once()
    assert (await store.verification(CHAT, USER))["status"] == "waiting"
    assert guard.client.member_rights[CHAT, USER]["send_messages"]


async def assistant_session(guard, store, *, online=True):
    group = guard.group(CHAT)
    group.verify_join = True
    group.verification_mode = "assistant"
    group.verification_assistant_username = "old_preview_value"
    store.verification_assistant = AsyncMock(
        return_value={"username": "sakura_test_bot", "bot_id": 123} if online else None
    )
    store.previous_verification_enforcement = AsyncMock(return_value=False)
    await guard.process_join(CHAT, guard.client.users[USER], "join-1")
    return await store.verification(CHAT, USER)


def completed(session, receipts):
    now = int(utc_now_naive().replace(tzinfo=UTC).timestamp())
    session["payload"]["assistant"].update(
        state="completed",
        receipts=[hashlib.sha256(receipt.encode()).hexdigest()[:24] for receipt in receipts],
        actor_id=USER,
        chat_id=CHAT,
        session_id=session["id"],
        started_at=now,
        completed_at=now,
    )


async def test_assistant_releases_only_after_both_steps_and_observes_edits(guard, store):
    session = await assistant_session(guard, store)
    assert session["status"] == "waiting" and session["payload"]["restriction_confirmed"]
    with pytest.raises(GuardError, match="invalid_callback"):
        await guard.verification.callback(callback(session))

    completed(session, ["step-1"])
    await store.save_verification(session)
    await guard.recover_once()
    assert guard.client.member_rights[CHAT, USER]["send_messages"]
    completed(session, ["step-1", "step-2"])
    await store.save_verification(session)
    await guard.recover_once()
    row = await store.verification(CHAT, USER)
    assert row["status"] == "passed" and not guard.client.member_rights[CHAT, USER]["send_messages"]
    assert row["payload"]["observation"]["hits"] == 0

    msg = SimpleNamespace(
        id=1234, message="想问一下怎么设置，附上截图", media=object(), edit_date=None
    )
    event = SimpleNamespace(chat_id=CHAT, is_group=True, sender_id=USER, id=msg.id, message=msg)
    await guard.handle_message(event)
    msg.message, msg.edit_date = "点击 https://example.com/", utc_now_naive()
    with pytest.raises(events.StopPropagation):
        await guard.handle_message(event)
    assert (await store.verification(CHAT, USER))["payload"]["observation"]["hits"] == 1
    assert (CHAT, msg.id) in guard.client.deleted


async def test_observation_detects_short_paid_recruitment_ad_locally(guard, store):
    session = await assistant_session(guard, store)
    completed(session, ["step-1", "step-2"])
    await store.save_verification(session)
    await guard.recover_once()

    normal = SimpleNamespace(id=2608, message="有人說接代簽一天能賺很多，這種廣告可信嗎？")
    await guard.handle_message(
        SimpleNamespace(chat_id=CHAT, is_group=True, sender_id=USER, id=normal.id, message=normal)
    )
    assert (CHAT, normal.id) not in guard.client.deleted

    ad = SimpleNamespace(id=2609, message="接收代签，一天能赚很多，至少1w，兴趣来")
    with pytest.raises(events.StopPropagation):
        await guard.handle_message(
            SimpleNamespace(chat_id=CHAT, is_group=True, sender_id=USER, id=ad.id, message=ad)
        )
    assert (CHAT, ad.id) in guard.client.deleted
    assert (await store.verification(CHAT, USER))["payload"]["observation"]["hits"] == 1


async def test_observation_waits_for_ai_slot_instead_of_dropping_message(guard, store, monkeypatch):
    session = await assistant_session(guard, store)
    completed(session, ["step-1", "step-2"])
    await store.save_verification(session)
    await guard.recover_once()
    guard.group(CHAT).observation_ai_review = True

    message = SimpleNamespace(
        id=2610,
        sender_id=USER,
        message="这是一个只能通过语义识别出来的待审核文本示例",
        edit_date=None,
    )
    guard.client.source_messages[(CHAT, message.id)] = message
    classify = AsyncMock(return_value={"verdict": {"reason": "test", "confidence": 0.99}})
    sleep = AsyncMock()
    monkeypatch.setattr(observation_media, "classify_observation", classify)
    monkeypatch.setattr(observation_media.asyncio, "sleep", sleep)
    guard.observation_media.next_model_at = asyncio.get_running_loop().time() + 2
    await guard.observation_media.inspect(
        (CHAT, USER, session["id"], message.id, None, None), message
    )

    sleep.assert_awaited_once()
    classify.assert_awaited_once_with(message.message, guard.group(CHAT).observation_ai_confidence)
    assert (CHAT, message.id) in guard.client.deleted


async def test_observation_classifier_uses_the_configured_client_directly(monkeypatch):
    from unittest.mock import Mock

    from core.ai import ai_client

    text = "请私聊咨询广告合作"
    completion = SimpleNamespace(
        choices=[
            SimpleNamespace(
                finish_reason="stop",
                message=SimpleNamespace(
                    content=(
                        '{"verdict":"violation","confidence":0.99,"category":"spam",'
                        '"reason":"广告招揽","evidence":[{"source":"message","quote":"请私聊咨询广告合作"}]}'
                    )
                ),
            )
        ]
    )
    create = AsyncMock(return_value=completion)
    client = SimpleNamespace(
        chat=SimpleNamespace(completions=SimpleNamespace(create=create)), with_options=Mock()
    )
    runtime = Mock(return_value=(client, "configured-model"))
    monkeypatch.setattr(ai_client, "get_async_ai_runtime", runtime)

    assert (await observation_media.classify_observation(text))["model"] == "configured-model"
    client.with_options.assert_not_called()
    runtime.assert_called_once_with(purpose="guard")
    assert "timeout" not in create.await_args.kwargs


async def test_guard_ocr_uses_its_own_route_without_channel_ocr(monkeypatch):
    from unittest.mock import Mock

    from core.ai import ai_client
    from core.config import ai_runtime

    config = SimpleNamespace(supports_vision=True, vision_enabled=False, vision_ocr=False)
    effective = Mock(return_value=config)
    create = AsyncMock(
        return_value=SimpleNamespace(
            choices=[SimpleNamespace(message=SimpleNamespace(content="OCR result"))]
        )
    )
    client = SimpleNamespace(chat=SimpleNamespace(completions=SimpleNamespace(create=create)))
    runtime = Mock(return_value=(client, "guard-ocr-model"))
    monkeypatch.setattr(ai_runtime, "effective_ai_config", effective)
    monkeypatch.setattr(ai_client, "get_async_ai_runtime", runtime)

    assert await observation_media.recognize_image(b"fixture", "image/png") == "OCR result"
    effective.assert_called_once_with(purpose="guard_vision")
    runtime.assert_called_once_with(purpose="guard_vision", config=config)
    assert create.await_args.kwargs["model"] == "guard-ocr-model"


async def test_short_observation_message_reaches_ai_and_can_be_deleted(guard, store, monkeypatch):
    session = await assistant_session(guard, store)
    completed(session, ["step-1", "step-2"])
    await store.save_verification(session)
    await guard.recover_once()
    guard.group(CHAT).observation_ai_review = True

    message = SimpleNamespace(
        id=2613,
        sender_id=USER,
        message="一天可抓紧1w，速来，很简单不麻烦",
        edit_date=None,
    )
    guard.client.source_messages[(CHAT, message.id)] = message
    guard.group(CHAT).observation_ai_confidence = 0.90
    classify = AsyncMock(return_value={"verdict": {"reason": "test", "confidence": 0.90}})
    monkeypatch.setattr(observation_media, "classify_observation", classify)
    guard.observation_media.start()
    try:
        event = SimpleNamespace(
            chat_id=CHAT, is_group=True, sender_id=USER, id=message.id, message=message
        )
        await guard.handle_message(event)
        await guard.observation_media.queue.join()
    finally:
        await guard.observation_media.stop()

    classify.assert_awaited_once_with(message.message, 0.90)
    assert (CHAT, message.id) in guard.client.deleted


@pytest.mark.parametrize(
    "conflict", ["permissions", "same_rights_other_admin", "blacklist", "expiry", "actor"]
)
async def test_assistant_does_not_release_when_state_changed(guard, store, conflict):
    session = await assistant_session(guard, store)
    completed(session, ["step-1", "step-2"])
    if conflict == "permissions":
        guard.client.member_rights[CHAT, USER]["change_info"] = True
    elif conflict == "same_rights_other_admin":
        guard.client.ban_actors[CHAT, USER] = 1001
    elif conflict == "blacklist":
        guard.group(CHAT).share_global_blacklist = True
        store.black[USER] = {"user_id": USER}
    elif conflict == "expiry":
        session["expires_at"] = utc_now_naive() - timedelta(seconds=1)
        guard.group(CHAT).verification_timeout_action = "allow"
    elif conflict == "actor":
        session["payload"]["assistant"]["actor_id"] = USER + 1
    await store.save_verification(session)
    await guard.recover_once()
    assert (await store.verification(CHAT, USER))["status"] != "passed"
    assert guard.client.member_rights[CHAT, USER]["send_messages"]


async def test_unavailable_assistant_keeps_the_restriction_until_it_recovers(guard, store):
    session = await assistant_session(guard, store, online=False)
    assert session["status"] == "waiting" and not session["payload"]["assistant"].get("bot_id")
    assert guard.client.member_rights[CHAT, USER]["send_messages"]
    store.verification_assistant.return_value = {"username": "sakura_test_bot", "bot_id": 123}
    await guard.recover_once()
    assert (await store.verification(CHAT, USER))["payload"]["assistant"]["bot_id"] == 123
    store.verification_assistant.assert_awaited_with(123)


async def test_assistant_username_comes_from_configured_bot_and_tracks_renames(guard, store):
    session = await assistant_session(guard, store)
    assert session["payload"]["assistant"]["username"] == "sakura_test_bot"
    store.verification_assistant.assert_awaited_with(123)
    store.verification_assistant.return_value = {"username": "renamed_test_bot", "bot_id": 123}
    await guard.recover_once()
    refreshed = await store.verification(CHAT, USER)
    assert refreshed["payload"]["assistant"]["username"] == "renamed_test_bot"


async def test_other_assistant_heartbeat_does_not_bind_session(guard, store):
    await assistant_session(guard, store, online=False)
    store.verification_assistant.return_value = {"username": "other_bot", "bot_id": 987}
    await guard.recover_once()
    session = await store.verification(CHAT, USER)
    assert "bot_id" not in session["payload"]["assistant"]


@pytest.mark.parametrize("value", ["", "malformed", "999:main-bot", "-123:invalid"])
def test_assistant_id_rejects_missing_invalid_or_main_bot_tokens(guard, monkeypatch, value):
    monkeypatch.setenv("QA_BOT_TOKEN", value)
    assert guard.verification.configured_assistant_id() is None


def test_premium_does_not_change_risk(guard):
    user = guard.client.users[USER]
    user.premium = False
    before = profile_snapshot(user, guard.group(CHAT), False)
    user.premium = True
    after = profile_snapshot(user, guard.group(CHAT), False)
    assert before["score"] == after["score"] and before["signals"] == after["signals"]


def test_qr_scan_reads_links_from_pixels():
    import zxingcpp
    from PIL import Image

    uri = "https://example.com/test-qr"
    barcode = zxingcpp.create_barcode(uri, zxingcpp.BarcodeFormat.QRCode)
    output = io.BytesIO()
    Image.fromarray(barcode.to_image(scale=6)).save(output, format="PNG")
    assert scan_qr(output.getvalue()) == uri


async def test_release_rechecks_rights_after_durable_intent(guard, store, monkeypatch):
    session = await assistant_session(guard, store)
    completed(session, ["step-1", "step-2"])
    await store.save_verification(session)
    original = store.save_verification

    async def save_then_external_change(row):
        await original(row)
        if row["payload"].get("release_intent"):
            guard.client.member_rights[CHAT, USER]["change_info"] = True
            guard.client.ban_actors[CHAT, USER] = 1001

    monkeypatch.setattr(store, "save_verification", save_then_external_change)
    await guard.recover_once()
    assert guard.client.member_rights[CHAT, USER]["change_info"]
    assert guard.client.member_rights[CHAT, USER]["send_messages"]


async def test_channel_rejoin_in_same_second_does_not_reuse_left_session(guard, store):
    from telethon.tl.types import ChannelParticipant, UpdateChannelParticipant

    guard.group(CHAT).verify_join = True
    now = utc_now_naive().replace(tzinfo=UTC)
    participant = ChannelParticipant(USER, now)

    def update(qts, previous, new):
        return UpdateChannelParticipant(
            channel_id=1234567890,
            date=now,
            actor_id=USER,
            user_id=USER,
            prev_participant=previous,
            new_participant=new,
            qts=qts,
        )

    await guard.handle_membership_update(update(10, None, participant))
    first = await store.verification(CHAT, USER)
    await guard.handle_membership_update(update(11, participant, None))
    assert (await store.verification(CHAT, USER))["status"] == "left"
    await guard.handle_membership_update(update(12, None, participant))
    second = await store.verification(CHAT, USER)
    assert second["id"] != first["id"] and second["status"] == "waiting"
    await guard.handle_membership_update(update(11, participant, None))
    assert (await store.verification(CHAT, USER))["status"] == "waiting"
