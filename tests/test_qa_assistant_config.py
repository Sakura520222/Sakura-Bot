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
@pytest.mark.parametrize("forwarded", [False, True])
async def test_userbot_backfill_maps_post_and_does_not_overwrite_live_records(forwarded):
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
            fwd_from=(
                SimpleNamespace(
                    channel_post=13009,
                    from_id=PeerChannel(99),
                    saved_from_peer=PeerChannel(100),
                    saved_from_msg_id=7,
                )
                if forwarded
                else SimpleNamespace(channel_post=7, from_id=PeerChannel(100))
            ),
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


@pytest.mark.asyncio
@pytest.mark.parametrize(
    ("failure", "stage", "reason"),
    [
        ("channel_query", "channel_info", "频道信息（getChat）检查失败"),
        ("channel_permission", "channel_membership", "频道权限不足"),
        ("channel_api", "channel_membership", "频道管理员权限检查失败"),
        ("no_discussion", "discussion_link", "频道未关联讨论群"),
        ("discussion_kicked_api", "discussion_membership", "助手 Bot 已被移出或封禁"),
        ("discussion_missing_api", "discussion_membership", "助手 Bot 尚未加入关联讨论群"),
        ("discussion_left", "discussion_membership", "尚未加入，请将助手添加到讨论群"),
        ("discussion_kicked", "discussion_membership", "已被封禁，请解除封禁后重新添加"),
        ("discussion_restricted", "discussion_membership", "当前成员状态为 restricted"),
        ("privacy", "discussion_visibility", "群隐私模式限制评论读取"),
        ("timeout", "discussion_membership", "网络连接或请求超时"),
        ("rate_limit", "discussion_membership", "请在 12 秒后重试"),
    ],
)
async def test_diagnostics_distinguishes_failure_stages(monkeypatch, failure, stage, reason):
    from core.infrastructure.telegram_bot_api import BotAPIError, NetworkAPIError, RetryAfterError

    monkeypatch.setenv("QA_BOT_TOKEN", "123:test")
    calls = []

    async def call(token, method, payload=None, **kwargs):
        calls.append((method, payload))
        if method == "getMe":
            return {"id": 123, "can_read_all_group_messages": failure != "privacy"}
        if method == "getChat":
            if failure == "channel_query":
                raise BotAPIError("Bad Request: chat not found", 400)
            return {"id": -1001, "linked_chat_id": None if failure == "no_discussion" else -1002}
        if payload["chat_id"] == -1001:
            if failure == "channel_api":
                raise BotAPIError("Forbidden: not enough rights", 403)
            return {"status": "member" if failure == "channel_permission" else "administrator"}
        assert payload["chat_id"] == -1002
        if failure == "discussion_kicked_api":
            raise BotAPIError("Forbidden: bot was kicked from the supergroup chat", 403)
        if failure == "discussion_missing_api":
            raise BotAPIError("Forbidden: bot is not a member of the supergroup chat", 403)
        if failure == "timeout":
            raise NetworkAPIError("Timeout: ReadTimeout")
        if failure == "rate_limit":
            raise RetryAfterError("Too Many Requests", 12)
        return {
            "status": failure.removeprefix("discussion_")
            if failure in ("discussion_left", "discussion_kicked", "discussion_restricted")
            else "member"
        }

    monkeypatch.setattr(assistant, "call_method", call)
    settings = assistant.AssistantSettings(
        require_subscription=True,
        required_channels=["https://t.me/test"],
        comments=[assistant.CommentSettings(channel="https://t.me/test", enabled=True)],
    )
    result = (await assistant.diagnose(settings))[0]
    assert not result["ok"]
    assert result["stage"] == stage
    assert reason in result["message"]
    if stage.startswith("discussion"):
        assert "频道管理员权限已确认" in result["message"]
    else:
        assert all(not payload or payload.get("chat_id") != -1002 for _, payload in calls)
    if failure.startswith("discussion_") and failure.endswith("_api"):
        assert "Telegram 403" in result["message"]
        assert "supergroup chat" in result["message"]


@pytest.mark.asyncio
async def test_identity_failure_is_specific_and_redacts_token(monkeypatch):
    from core.infrastructure.telegram_bot_api import BotAPIError

    token = "123:test-secret"
    monkeypatch.setenv("QA_BOT_TOKEN", token)

    async def call(*args, **kwargs):
        raise BotAPIError(f"Unauthorized: https://api.telegram.org/bot{token}/getMe", 401)

    monkeypatch.setattr(assistant, "call_method", call)
    settings = assistant.AssistantSettings(
        require_subscription=True, required_channels=["https://t.me/test"]
    )
    result = (await assistant.diagnose(settings))[0]
    assert result["stage"] == "identity"
    assert "Token 无效或已失效" in result["message"]
    assert "Telegram 401" in result["message"]
    assert token not in result["message"]
    assert "[REDACTED]" in result["message"]


@pytest.mark.asyncio
async def test_comment_only_diagnostics_does_not_require_channel_admin(monkeypatch):
    monkeypatch.setenv("QA_BOT_TOKEN", "123:test")

    async def call(token, method, payload=None, **kwargs):
        if method == "getMe":
            return {"id": 123, "can_read_all_group_messages": True}
        if method == "getChat":
            return {"id": -1001, "linked_chat_id": -1002}
        assert payload["chat_id"] == -1002
        return {"status": "member"}

    monkeypatch.setattr(assistant, "call_method", call)
    result = (
        await assistant.diagnose(
            assistant.AssistantSettings(
                comments=[assistant.CommentSettings(channel="https://t.me/test", enabled=True)]
            )
        )
    )[0]
    assert result["ok"]
    assert "频道信息读取成功" in result["message"]
    assert "频道管理员权限已确认" not in result["message"]
    assert "关联讨论群（ID：-1002）成员身份及评论读取权限已确认" in result["message"]


@pytest.mark.asyncio
async def test_failed_diagnostic_still_blocks_save(monkeypatch):
    from core.infrastructure.telegram_bot_api import BotAPIError

    monkeypatch.setenv("QA_BOT_TOKEN", "123:test")
    monkeypatch.setattr(assistant, "get_config", lambda: {"channels": ["https://t.me/test"]})
    writes = []
    monkeypatch.setattr(assistant, "write_config", writes.append)

    async def call(token, method, payload=None, **kwargs):
        if method == "getMe":
            return {"id": 123, "can_read_all_group_messages": True}
        if method == "getChat":
            return {"id": -1001, "linked_chat_id": -1002}
        raise BotAPIError("Forbidden: bot was kicked from the supergroup chat", 403)

    monkeypatch.setattr(assistant, "call_method", call)
    request = assistant.AssistantRequest(
        assistant=assistant.AssistantSettings(
            comments=[assistant.CommentSettings(channel="https://t.me/test", enabled=True)]
        ),
        pool=assistant.PoolSettings(),
    )
    with pytest.raises(HTTPException) as error:
        await assistant.update_assistant(request)
    assert error.value.status_code == 422
    assert error.value.detail["diagnostics"][0]["stage"] == "discussion_membership"
    assert not writes


@pytest.mark.asyncio
async def test_backfill_does_not_confuse_original_source_with_current_channel():
    from types import SimpleNamespace

    from telethon.tl.types import PeerChannel

    from core.services.qa_comment_backfill import backfill_post

    class Client:
        async def get_entity(self, channel):
            return PeerChannel(100)

        async def __call__(self, request):
            return SimpleNamespace(
                messages=[
                    SimpleNamespace(
                        peer_id=PeerChannel(200),
                        fwd_from=SimpleNamespace(
                            from_id=PeerChannel(100),
                            channel_post=7,
                            saved_from_peer=PeerChannel(300),
                            saved_from_msg_id=7,
                        ),
                    )
                ]
            )

    with pytest.raises(ValueError, match="无法唯一识别"):
        await backfill_post(Client(), object(), "https://t.me/test", 7)
