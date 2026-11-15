import hashlib
import secrets
from types import SimpleNamespace
from unittest.mock import AsyncMock

import pytest
from aiohttp import web
from fastapi import FastAPI
from fastapi.middleware.cors import CORSMiddleware
from httpx import ASGITransport, AsyncClient
from pydantic import SecretStr

from core.web_api.middleware import AuthMiddleware
from core.web_api.routes import wiki_account, wiki_assistant
from core.wiki.telegram_login import LoginConfig


@pytest.fixture
async def bridge_client(monkeypatch):
    token = "123:fixture"
    digest = hashlib.sha256(f"sakura-wiki-assistant:v1:{token}".encode()).hexdigest()
    access, session = secrets.token_urlsafe(32), secrets.token_urlsafe(32)
    origin = "https://wiki.example.test"
    calls = []

    async def upstream(request):
        assert request.headers["Authorization"] == f"Bearer {digest}"
        assert request.headers["X-Wiki-User"] == "12345"
        assert request.headers["X-Wiki-Session"] == session
        body = await request.json() if request.can_read_body else None
        calls.append((request.method, request.path, body))
        if request.path == "/status":
            return web.json_response({"point_units": 25, "free_used": 1})
        if request.path == "/checkin":
            return web.json_response({"added": True, "units": 10})
        if request.method == "DELETE":
            return web.Response(status=204)
        response = web.StreamResponse(headers={"Content-Type": "text/event-stream"})
        await response.prepare(request)
        await response.write(b"event: accepted\ndata: true\n\n")
        await response.write(b'event: done\ndata: {"refunded":false}\n\n')
        return response

    upstream_app = web.Application()
    upstream_app.router.add_route("*", "/{tail:.*}", upstream)
    runner = web.AppRunner(upstream_app)
    await runner.setup()
    try:
        site = web.TCPSite(runner, "127.0.0.1", 0)
        await site.start()
        port = runner.addresses[0][1]
        config = SimpleNamespace(
            assistant_enabled=True, assistant_port=port, assistant_checkin=True
        )
        monkeypatch.setattr(
            wiki_assistant,
            "get_settings",
            lambda: SimpleNamespace(
                wiki_login=config,
                telegram=SimpleNamespace(qa_bot_token=SecretStr(token)),
            ),
        )
        monkeypatch.setattr(
            wiki_account.telegram_login,
            "login_config",
            lambda: LoginConfig(origin, "123", "secret"),
        )
        accounts = SimpleNamespace(
            session=AsyncMock(
                side_effect=lambda value: {"telegram_id": 12345} if value == access else None
            )
        )
        monkeypatch.setattr(wiki_account, "runtime", lambda: SimpleNamespace(accounts=accounts))
        app = FastAPI()
        app.add_middleware(CORSMiddleware, allow_origins=["*"], allow_credentials=True)
        app.add_middleware(AuthMiddleware)
        app.include_router(wiki_assistant.router, prefix=wiki_assistant.PATH)
        async with AsyncClient(transport=ASGITransport(app=app), base_url=origin) as client:
            yield client, origin, access, session, config, calls
    finally:
        await runner.cleanup()


async def test_assistant_bridge_uses_session_and_streams_private_events(bridge_client):
    client, origin, access, session, config, calls = bridge_client
    path = wiki_assistant.PATH
    assert (await client.get(f"{path}/status")).status_code == 401
    client.cookies.set(wiki_account.ACCESS, access)
    client.cookies.set(wiki_account.LOGOUT, session)
    status = await client.get(f"{path}/status", headers={"Origin": "https://evil.test"})
    assert status.json() == {"point_units": 25, "free_used": 1}
    assert status.headers["cache-control"] == "no-store"
    assert "access-control-allow-origin" not in status.headers
    assert (await client.post(f"{path}/checkin")).status_code == 403
    headers = {"Origin": origin, "X-Wiki-Request": "1"}
    assert (await client.post(f"{path}/checkin", headers=headers)).json()["added"] is True
    config.assistant_checkin = False
    assert (await client.post(f"{path}/checkin", headers=headers)).status_code == 403
    turn = "a" * 32
    question = {"id": turn, "question": "What changed?"}
    assert (
        await client.post(f"{path}/chat", headers=headers, json={**question, "user_id": 999})
    ).status_code == 422
    response = await client.post(f"{path}/chat", headers=headers, json=question)
    assert response.headers["content-type"].startswith("text/event-stream")
    assert response.headers["x-accel-buffering"] == "no"
    assert "event: accepted" in response.text and "event: done" in response.text
    assert (await client.get(f"{path}/chat/{turn}")).text == response.text
    assert (await client.delete(f"{path}/chat/{turn}", headers=headers)).status_code == 204
    assert calls == [
        ("GET", "/status", None),
        ("POST", "/checkin", None),
        ("POST", "/chat", question),
        ("GET", f"/chat/{turn}", None),
        ("DELETE", f"/chat/{turn}", None),
    ]
