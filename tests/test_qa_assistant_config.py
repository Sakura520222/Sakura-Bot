import pytest
from fastapi import HTTPException
from pydantic import ValidationError

from core.web_api.routes import assistant


def test_defaults_and_strict_limits():
    value = assistant.AssistantSettings()
    assert value.timezone == "UTC" and value.max_messages == 8
    for data in (
        {"max_messages": 0},
        {"max_messages": True},
        {"timezone": "not/a/timezone"},
        {"unknown": 1},
    ):
        with pytest.raises(ValidationError):
            assistant.AssistantSettings.model_validate(data)
    with pytest.raises(HTTPException):
        assistant.validate_channels(assistant.AssistantSettings(require_subscription=True), [])
    with pytest.raises(HTTPException):
        assistant.validate_channels(
            assistant.AssistantSettings(required_channels=["foreign"]), ["known"]
        )


@pytest.mark.asyncio
async def test_diagnostics_uses_assistant_token_and_fail_closed(monkeypatch):
    monkeypatch.setenv("QA_BOT_TOKEN", "123:test")
    calls = []

    async def call(token, method, payload=None, **kwargs):
        calls.append((token, method, payload))
        if method == "getMe":
            return {"id": 123}
        if method == "getChat":
            return {"id": -1001, "linked_chat_id": -1002}
        return {"status": "member"}

    monkeypatch.setattr(assistant, "call_method", call)
    settings = assistant.AssistantSettings(
        require_subscription=True, required_channels=["https://t.me/test"]
    )
    result = await assistant.diagnose(settings)
    assert not result[0]["ok"]
    assert all(c[0] == "123:test" for c in calls)
    assert calls[-1][2]["user_id"] == 123


@pytest.mark.asyncio
async def test_disable_subscription_does_not_require_telegram(monkeypatch):
    async def forbidden(*args, **kwargs):
        raise AssertionError("unexpected API call")

    monkeypatch.setattr(assistant, "call_method", forbidden)
    assert await assistant.diagnose(assistant.AssistantSettings()) == []


@pytest.mark.asyncio
async def test_userbot_backfill_maps_post_and_does_not_overwrite_live_records():
    import json
    from datetime import UTC, datetime
    from types import SimpleNamespace

    from telethon.tl.types import PeerChannel

    from core.services.qa_comment_backfill import backfill_post

    writes = []

    class Cursor:
        rowcount = 1

        async def __aenter__(self):
            return self

        async def __aexit__(self, *args):
            pass

        async def execute(self, sql, args):
            writes.append((sql, args))

    class Connection:
        async def __aenter__(self):
            return self

        async def __aexit__(self, *args):
            pass

        def cursor(self):
            return Cursor()

        async def commit(self):
            pass

    class Pool:
        def acquire(self):
            return Connection()

    def message(id):
        return SimpleNamespace(
            id=id,
            peer_id=PeerChannel(200),
            fwd_from=SimpleNamespace(channel_post=7),
            sender_id=1,
            message="原文",
            date=datetime.now(UTC),
            reply_to_msg_id=10,
            edit_date=None,
            media=None,
        )

    class Client:
        async def get_entity(self, channel):
            return PeerChannel(100)

        async def __call__(self, request):
            return SimpleNamespace(messages=[message(10)])

        async def iter_messages(self, entity, **kwargs):
            assert kwargs["reply_to"] == 7
            yield message(11)
            yield message(12)

    result = await backfill_post(Client(), Pool(), "https://t.me/test", 7, limit=1)
    assert result["truncated"]
    assert any("qa_posts" in sql for sql, _ in writes)
    comments = [args for sql, args in writes if "qa_comments" in sql]
    assert len(comments) == 2
    assert all(json.loads(args[4])["root_id"] == 10 for args in comments)
    assert all("INSERT IGNORE" in sql for sql, _ in writes)
