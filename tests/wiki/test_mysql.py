# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot

"""Run against a disposable MySQL: WIKI_TEST_PORT. No production configuration is read."""

import asyncio
import json
import os
import subprocess
import uuid
from datetime import datetime, timedelta
from io import BytesIO
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import AsyncMock
from urllib.parse import parse_qs, urlsplit

import aiomysql
import pytest
import pytest_asyncio
from fastapi import FastAPI
from httpx import ASGITransport, AsyncClient
from PIL import Image

from core.web_api.auth import WebUIUser, generate_token
from core.web_api.middleware import AuthMiddleware, _wiki_buckets
from core.web_api.routes import auth, wiki, wiki_account
from core.wiki.collector import SourceCollector
from core.wiki.community import Community, Proposal
from core.wiki.engine import WikiEngine
from core.wiki.knowledge import Knowledge
from core.wiki.models import WikiConfig, chunks, digest, name_key
from core.wiki.reader import PublicReader
from core.wiki.runtime import WikiRuntime
from core.wiki.store import LeaseLost, WikiStore, decode
from core.wiki.telegram_login import LoginConfig
from tests.wiki.test_contracts import extraction

TEST_PORT = int(os.environ.get("WIKI_TEST_PORT", "0"))
GO_TEST_ENV = {
    key: os.environ[key]
    for key in ("LOCALAPPDATA", "GOCACHE", "GOPATH", "GOROOT")
    if key in os.environ
}
pytestmark = [
    pytest.mark.integration,
    pytest.mark.skipif(not TEST_PORT, reason="Set WIKI_TEST_PORT to an isolated test MySQL"),
]


@pytest_asyncio.fixture
async def store():
    database = "wiki_test_" + uuid.uuid4().hex
    connection = await aiomysql.connect(
        host="127.0.0.1", port=TEST_PORT, user="root", password="", autocommit=True
    )
    async with connection.cursor() as cur:
        await cur.execute(
            f"CREATE DATABASE `{database}` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci"
        )
    pool = await aiomysql.create_pool(
        host="127.0.0.1",
        port=TEST_PORT,
        user="root",
        password="",
        db=database,
        autocommit=True,
        maxsize=8,
    )
    result = WikiStore(SimpleNamespace(pool=pool))
    result.test_database = database
    try:
        await result.initialize()
        await result.initialize()  # additive migration is safe to repeat
        # collector.prepare_summary_refs 会读应用侧 summaries 表（生产库由
        # DatabaseInitializer 建表）；测试库只有 wiki_* 表，这里补最小镜像，
        # 列与 core/wiki/collector.py 的 SELECT 保持一致。
        await result.query(
            """CREATE TABLE IF NOT EXISTS summaries (
            id INT AUTO_INCREMENT PRIMARY KEY,
            channel_id VARCHAR(255) NOT NULL,
            summary_text TEXT NOT NULL,
            created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
            summary_message_ids JSON
            ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4"""
        )
        yield result
    finally:
        pool.close()
        await pool.wait_closed()
        async with connection.cursor() as cur:
            await cur.execute(f"DROP DATABASE `{database}`")
        connection.close()


async def channel(store, channel_id=12345, public=True):
    entity = SimpleNamespace(
        id=channel_id,
        username=f"channel_{channel_id}",
        title="知识频道",
        broadcast=True,
        restricted=False,
        noforwards=False,
    )
    ch = await store.register_channel(entity, f"https://t.me/{entity.username}")
    await store.configure(
        ch["id"],
        WikiConfig(
            enabled=True,
            public=public,
            token_budget=10000000,
            public_base_url="https://wiki.example.com",
        ),
        "test",
    )
    return await store.channel(ch["id"])


async def test_oidc_cookie_flow_isolated_from_webui_and_replay(store, monkeypatch):
    origin = "https://wiki.example.test"
    headers = {"Origin": origin, "X-Wiki-Request": "1"}
    account = wiki_account.ACCOUNT_PATH
    service = WikiRuntime(store, None, None)
    service.ready = True
    monkeypatch.setattr(wiki, "get_wiki_runtime", lambda: service)
    monkeypatch.setattr(
        wiki_account.telegram_login, "login_config", lambda: LoginConfig(origin, "123456", "secret")
    )
    exchange = AsyncMock(
        return_value=(987654321, b"opaque-subject", "第一位用户", "private_name", None)
    )
    monkeypatch.setattr(wiki_account.telegram_login, "exchange_code", exchange)
    monkeypatch.setattr(
        wiki_account.telegram_login,
        "load_profile_avatar",
        AsyncMock(return_value=(b"avatar", "image/png")),
    )
    monkeypatch.setattr("core.web_api.auth._is_dev_mode_enabled", lambda: True)
    app = FastAPI()
    app.add_middleware(AuthMiddleware)
    app.include_router(wiki.public, prefix="/api/public/wiki")
    app.include_router(wiki_account.router, prefix=account)
    app.include_router(wiki_account.admin, prefix="/api/wiki/admin")
    transport = ASGITransport(app=app)

    async with (
        AsyncClient(transport=transport, base_url=origin) as client,
        AsyncClient(transport=transport, base_url=origin) as other,
    ):
        assert (await client.get("/api/public/wiki/channels")).status_code == 200
        assert (await client.get(f"{account}/session")).json()["user"] is None
        assert (
            await client.post(
                f"{account}/start", json={}, headers={**headers, "Origin": "https://evil.test"}
            )
        ).status_code == 403
        assert (
            await client.post(f"{account}/start", json={}, headers={**headers, "Host": "evil.test"})
        ).status_code == 404
        start = await client.post(
            f"{account}/start", headers=headers, json={"return_to": "/wiki/123#claim-1"}
        )
        assert start.status_code == 200, start.text
        params = parse_qs(urlsplit(start.json()["authorization_url"]).query)
        assert "HttpOnly" in start.headers["set-cookie"] and "Secure" in start.headers["set-cookie"]
        callback_url = f"{account}/callback"
        expired = await client.post(f"{account}/start", headers=headers, json={})
        expired_state = parse_qs(urlsplit(expired.json()["authorization_url"]).query)["state"][0]
        await store.query(
            "UPDATE wiki_login_flows SET expires_at=UTC_TIMESTAMP(6) WHERE state_hash=UNHEX(%s)",
            (wiki_account.token_hash(expired_state),),
        )
        expired_result = await client.get(
            callback_url, params={"state": expired_state, "code": "expired-code"}
        )
        assert expired_result.headers["location"].endswith("login_error=expired")
        callback_params = {"state": params["state"][0], "code": "single-use-code"}
        rejected = await other.get(callback_url, params=callback_params)
        assert (
            rejected.headers["location"].endswith("login_error=expired")
            and not exchange.await_count
        )
        accepted = await client.get(callback_url, params=callback_params)
        assert (
            accepted.status_code == 303
            and accepted.headers["location"] == origin + "/wiki/123#claim-1"
        )
        assert (
            accepted.headers["cache-control"] == "no-store"
            and accepted.headers["referrer-policy"] == "no-referrer"
        )
        assert all(
            "HttpOnly" in cookie and "SameSite=lax" in cookie
            for cookie in accepted.headers.get_list("set-cookie")
        )
        replay = await client.get(callback_url, params=callback_params)
        assert (
            replay.headers["location"].endswith("login_error=expired") and exchange.await_count == 1
        )
        assert exchange.await_args.args[3] == params["nonce"][0]
        assert exchange.await_args.args[2] != params["code_challenge"][0]

        state = await client.get(f"{account}/session")
        user = state.json()["user"]
        assert user["display_name"] == "第一位用户" and user["username"] == "private_name"
        assert "987654321" not in state.text and "opaque-subject" not in state.text
        assert user["privacy"] == {
            "show_avatar": False,
            "show_username": False,
            "anonymous_contributions": True,
        }
        assert (await client.get(f"{account}/avatar")).content == b"avatar"
        assert (await other.get(f"{account}/avatar", params={"id": user["id"]})).status_code == 401
        admin_url = "/api/wiki/admin/status"
        assert (await client.get(admin_url)).status_code == 401
        assert (
            await client.get(
                admin_url,
                headers={"Authorization": f"Bearer {client.cookies.get(wiki_account.ACCESS)}"},
            )
        ).status_code == 401
        assert (
            await client.get(
                admin_url, headers={"Authorization": f"Bearer {generate_token('999999')}"}
            )
        ).status_code == 403
        assert (
            await client.get(
                admin_url, headers={"Authorization": f"Bearer {generate_token('admin')}"}
            )
        ).json() == {"admin": True}
        assert (
            await other.get(f"{account}/avatar", headers={"Authorization": "Bearer dev"})
        ).status_code == 401

        preferences = {
            "show_avatar": True,
            "show_username": False,
            "anonymous_contributions": False,
        }
        assert (await client.put(f"{account}/privacy", json=preferences)).status_code == 403
        assert (
            await client.put(
                f"{account}/privacy", headers=headers, json={**preferences, "user_id": "someone"}
            )
        ).status_code == 422
        assert (
            await client.put(f"{account}/privacy", headers=headers, json=preferences)
        ).json() == preferences
        bookmark = "a" * 32
        bookmarks = f"{account}/bookmarks"
        assert (await other.get(bookmarks)).status_code == 401
        assert (await client.post(bookmarks, json={"add": [bookmark]})).status_code == 403
        for _ in range(2):
            assert (
                await client.post(bookmarks, headers=headers, json={"add": [bookmark]})
            ).status_code == 204
        assert (await client.get(bookmarks)).json() == {"ids": [bookmark]}
        assert (
            await client.post(bookmarks, headers=headers, json={"remove": bookmark})
        ).status_code == 204
        assert (await client.get(bookmarks)).json() == {"ids": []}
        old_refresh = client.cookies.get(wiki_account.REFRESH)
        old_access = client.cookies.get(wiki_account.ACCESS)
        session = await service.accounts.session(old_access)
        await store.query(
            "UPDATE wiki_sessions SET access_until=UTC_TIMESTAMP(6) WHERE id=%s",
            (session["session_id"],),
        )
        assert (await client.get(f"{account}/session")).json()["can_refresh"] is True
        assert (await client.post(f"{account}/refresh", headers=headers)).status_code == 204
        assert client.cookies.get(wiki_account.REFRESH) != old_refresh
        assert await service.accounts.rotate(old_refresh) is None
        assert (await client.get(f"{account}/session")).json()["user"]["privacy"] == preferences
        new_refresh = client.cookies.get(wiki_account.REFRESH)
        rotations = await asyncio.gather(
            service.accounts.rotate(new_refresh), service.accounts.rotate(new_refresh)
        )
        rotated = [result for result in rotations if result]
        assert len(rotated) == 1
        client.cookies.set(wiki_account.ACCESS, rotated[0][0], domain="wiki.example.test", path="/")
        client.cookies.set(
            wiki_account.REFRESH, rotated[0][1], domain="wiki.example.test", path="/"
        )

        profile = await client.post(
            f"{account}/start", headers=headers, json={"refresh_profile": True}
        )
        state = parse_qs(urlsplit(profile.json()["authorization_url"]).query)["state"][0]
        exchange.return_value = (999999, b"different-account", "其他用户", None, None)
        wrong_account = await client.get(
            callback_url, params={"state": state, "code": "wrong-account"}
        )
        assert wrong_account.headers["location"].endswith("login_error=unavailable")
        assert (await client.get(f"{account}/session")).json()["user"]["id"] == user["id"]
        profile = await client.post(
            f"{account}/start", headers=headers, json={"refresh_profile": True}
        )
        state = parse_qs(urlsplit(profile.json()["authorization_url"]).query)["state"][0]
        exchange.return_value = (987654321, b"opaque-subject", "新昵称", None, None)
        await client.get(callback_url, params={"state": state, "code": "profile"})
        updated = (await client.get(f"{account}/session")).json()["user"]
        assert (
            updated["id"] == user["id"]
            and updated["display_name"] == "新昵称"
            and updated["privacy"] == preferences
        )

        admin_headers = {"Authorization": f"Bearer {generate_token('admin')}"}
        block = f"/api/wiki/admin/accounts/{user['id']}/blocked"
        assert (await client.put(block, json={"blocked": True})).status_code == 401
        assert (
            await client.put(block, headers=admin_headers, json={"blocked": True})
        ).status_code == 200
        assert (await client.post(f"{account}/refresh", headers=headers)).status_code == 401
        assert (await client.get(f"{account}/session")).json()["user"] is None
        assert not await store.one("SELECT id FROM wiki_sessions")
        assert (
            await client.put(block, headers=admin_headers, json={"blocked": False})
        ).status_code == 200
        start = await client.post(f"{account}/start", headers=headers, json={})
        state = parse_qs(urlsplit(start.json()["authorization_url"]).query)["state"][0]
        assert (
            await client.get(callback_url, params={"state": state, "code": "login-again"})
        ).status_code == 303
        refresh = client.cookies.get(wiki_account.REFRESH)
        assert refresh
        assert (await client.post(f"{account}/logout", headers=headers)).status_code == 204
        assert not client.cookies.get(wiki_account.REFRESH) and not await service.accounts.rotate(
            refresh
        )


async def test_legacy_admin_widget_preserves_login_with_expiry_and_replay_checks(
    store, monkeypatch
):
    import hashlib
    import hmac
    import time

    from core.settings import get_settings
    from core.web_api import deps

    service = WikiRuntime(store, None, None)
    service.ready = True
    monkeypatch.setattr(wiki, "get_wiki_runtime", lambda: service)
    monkeypatch.setattr(deps, "check_admin_permission", lambda user_id: user_id == "123456")
    app = FastAPI()
    app.add_middleware(AuthMiddleware)
    app.include_router(auth.router, prefix="/api/auth")
    key = hashlib.sha256(get_settings().telegram.bot_token.encode()).digest()

    def signed(when):
        data = {"id": 123456, "first_name": "Admin", "auth_date": when}
        message = "\n".join(f"{name}={value}" for name, value in sorted(data.items()))
        return {**data, "hash": hmac.new(key, message.encode(), hashlib.sha256).hexdigest()}

    async with AsyncClient(
        transport=ASGITransport(app=app), base_url="https://wiki.example.test"
    ) as client:
        endpoint = "/api/auth/telegram-callback"
        now = int(time.time())
        assert not (await client.post(endpoint, json=signed(now - 301))).json()["success"]
        response = await client.post(endpoint, json=signed(now))
        assert response.json()["success"]
        assert not (await client.post(endpoint, json=signed(now))).json()["success"]


async def published(store, *, image=False, channel_id=12345):
    ch = await channel(store, channel_id)
    if image:
        await store.put_source(
            ch["id"],
            "message",
            1,
            "图文",
            datetime(2020, 1, 1),
            verified=True,
            message_id=1,
            media_key="photo:1",
            category="image",
        )
    source_id = await store.put_source(
        ch["id"],
        "image" if image else "message",
        1,
        "Sakura 支持中文检索。",
        datetime(2020, 1, 1),
        verified=True,
        message_id=1,
        original_length=14,
        media_key="photo:1" if image else None,
    )
    source = await store.one("SELECT * FROM wiki_sources WHERE id=%s", (source_id,))
    chunk = next(chunks(source_id, source["body"], source["content_hash"]))
    job_id = await store.enqueue(ch["id"], "backfill", "test")
    job = await store.claim_job()
    knowledge = Knowledge(store)
    await knowledge.apply_extraction(job, chunk, extraction(chunk))
    page = await store.one("SELECT * FROM wiki_pages WHERE channel_id=%s", (ch["id"],))
    revision = await knowledge.build_page(job, page)
    job["cursor"]["revisions"] = {page["id"]: revision}
    fresh = await store.channel(ch["id"])
    job["progress"].update(
        generation_complete=True, pages=1, source_generation=fresh["source_generation"]
    )
    await WikiEngine(store, runtime=lambda _: (FakeAI(), "fixture-model")).review(job, fresh)
    await store.finish(job)
    publication = await knowledge.publish(ch["id"], job_id, "test")
    return ch, source, page, revision, publication


async def test_atomic_publication_visibility_revocation_and_rollback(store):
    ch, source, page, revision, publication = await published(store)
    reader = PublicReader(store)
    article = await reader.page(ch["id"], page["slug"])
    assert article["publication_id"] == publication and article["revision_id"] == revision
    assert article["sources"][0]["url"] == "https://t.me/channel_12345/1"
    assert article["claims"][0]["evidence"][0]["quote"] is None
    # 增量总量字段与截断后的列表必须同时存在且一致
    assert article["sources_total"] == 1 and article["history_total"] == 1
    assert (await reader.search("中文"))["total"] == 1
    bookmarks = await reader.bookmarks(["f" * 32, page["id"], page["id"]])
    assert [item["id"] for item in bookmarks] == [page["id"]]
    assert bookmarks[0]["url"] == article["url"]
    published_job = await store.one("SELECT * FROM wiki_jobs WHERE channel_id=%s", (ch["id"],))
    assert published_job["status"] == "published" and decode(published_job["progress"])["published"]
    assert await Knowledge(store).publish(ch["id"], published_job["id"], "retry") == publication
    await store.revoke(ch["id"], source_id=source["id"], actor="test")
    assert await reader.page(ch["id"], page["slug"]) is None
    assert not (await reader.search("中文"))["items"]
    assert not await reader.bookmarks([page["id"]])
    with pytest.raises(ValueError, match="revoked"):
        await Knowledge(store).publish(ch["id"], None, "test", rollback=publication)
    assert (await store.channel(ch["id"]))["published_revision"] == publication


async def test_community_contributions_follow_publication_and_privacy(store, monkeypatch):
    ch, source, page, revision, original = await published(store)
    await store.configure(
        ch["id"],
        ch["config"].model_copy(update={"proposals_enabled": True, "annotations_enabled": True}),
        "test",
    )
    service = WikiRuntime(store, None, None)
    fake = FakeAI()
    service.engine = WikiEngine(store, runtime=lambda _: (fake, "fixture-model"))
    service.community = Community(store, service.engine.knowledge)
    await service.accounts.login(
        (987654321, b"test-subject", "测试贡献者", "hidden_name", None), (None, None), None, None
    )
    user = (await store.one("SELECT id FROM wiki_users"))["id"]
    proposal = Proposal(
        revision=revision,
        kind="annotation",
        claim=0,
        text="社区补充说明",
        sources=["https://example.org/reference"],
        excerpt="中文检索",
        excerpt_occurrence=0,
    )
    identifier, notify = await service.community.submit(user, ch["id"], page["id"], proposal)
    assert not notify and not await service.community.contributions()
    competing, _ = await service.community.submit(user, ch["id"], page["id"], proposal)
    current_job = await store.enqueue(ch["id"], "update")
    await store.control_job(ch["id"], current_job, "pause", "test")
    before = await store.one("SELECT * FROM wiki_jobs WHERE id=%s", (current_job,))
    approved = await service.community.decide(identifier, "approved", "test")
    assert not approved["queued"] and approved["job_id"] != current_job
    assert not await service.community.dispatch_approved(ch["id"])
    assert (await store.channel(ch["id"]))["active_job"] == current_job
    [detail] = await service.community.proposals(identifier=identifier, review=True)
    assert detail["original"] and detail["review_job_id"] == approved["job_id"]
    assert detail["review_job_status"] == "pending"
    assert not await service.community.contributions()
    job = await store.claim_job()
    assert job["kind"] == "page_edit" and job["id"] == approved["job_id"]
    await service.run_job(job)
    assert job["progress"]["reviewed_pages"] == job["progress"]["review_total"] == 1
    await service.engine.knowledge.publish(ch["id"], job["id"], "test")
    assert await store.one("SELECT * FROM wiki_jobs WHERE id=%s", (current_job,)) == before
    assert (await store.channel(ch["id"]))["active_job"] == current_job
    assert (await store.one("SELECT COUNT(*) total FROM wiki_contributions"))["total"] == 1
    assert not await service.community.contributions()  # Anonymous by default.
    await store.query("UPDATE wiki_users SET anonymous_contributions=FALSE WHERE id=%s", (user,))
    contributors = await service.community.contributions(channel=ch["id"])
    assert contributors[0]["total"] == 1 and contributors[0]["username"] is None
    article = await service.reader.page(ch["id"], page["slug"])
    assert article["claims"][0]["community_notes"][0]["text"] == "社区补充说明"
    assert article["claims"][0]["community_notes"][0]["excerpt_occurrence"] == 0
    assert article["claims"][0]["community_notes"][0]["contributor"] == {
        "id": user,
        "display_name": "测试贡献者",
        "username": None,
        "avatar": False,
    }
    assert "proposal_id" not in article["claims"][0]["community_notes"][0]
    await store.query("UPDATE wiki_users SET anonymous_contributions=TRUE WHERE id=%s", (user,))
    anonymous = await service.reader.page(ch["id"], page["slug"], part="article")
    assert "contributor" not in anonymous["claims"][0]["community_notes"][0]
    await store.query("UPDATE wiki_users SET anonymous_contributions=FALSE WHERE id=%s", (user,))
    comparison = await service.reader.page(
        ch["id"], page["slug"], compare=(revision, article["revision_id"])
    )
    assert comparison["changes"] == [
        {"before": None, "after": "注解「中文检索」　社区补充说明\nhttps://example.org/reference"}
    ]
    with pytest.raises(ValueError, match="基线"):
        await service.community.decide(competing, "approved", "test")
    # Simulate an approval queued by the previous version, while the channel job is still paused.
    legacy, _ = await service.community.submit(
        user, ch["id"], page["id"], proposal.model_copy(update={"revision": article["revision_id"]})
    )
    await store.query("UPDATE wiki_edit_proposals SET status='approved' WHERE id=%s", (legacy,))
    assert await service.community.dispatch_approved(ch["id"])
    [detail] = await service.community.proposals(identifier=legacy, review=True)
    assert detail["review_job_status"] == "pending"
    assert (await store.claim_job())["id"] == detail["review_job_id"]
    service.ready = True
    monkeypatch.setattr(wiki, "get_wiki_runtime", lambda: service)
    app = FastAPI()
    app.add_middleware(AuthMiddleware)
    app.include_router(wiki.public, prefix="/api/public/wiki")
    compare_path = f"/api/public/wiki/channels/{ch['id']}/pages/{page['slug']}/compare"
    article_path = compare_path.removesuffix("/compare")
    async with AsyncClient(transport=ASGITransport(app=app), base_url="http://test") as client:
        response = await client.get(
            compare_path, params={"before": revision, "after": article["revision_id"]}
        )
        assert response.status_code == 200 and response.json()["changes"] == comparison["changes"]
        response = await client.get(
            compare_path, params={"before": revision, "after": detail["revision_id"]}
        )
        assert response.status_code == 404
        response = await client.get(article_path, params={"part": "article", "revision": revision})
        assert response.status_code == 200
        previous = response.json()
        assert previous["revision_id"] == revision
        assert previous["current_revision_id"] == article["revision_id"]
        assert previous["claims"][0]["community_notes"] == []
        assert not previous["proposals_enabled"] and not previous["annotations_enabled"]
        response = await client.get(
            article_path, params={"part": "references", "revision": revision}
        )
        assert response.status_code == 200 and response.json()["revision_id"] == revision
        response = await client.get(article_path, params={"revision": detail["revision_id"]})
        assert response.status_code == 404
    await service.engine.knowledge.publish(ch["id"], None, "test", rollback=original)
    assert not await service.community.contributions()
    assert (await store.one("SELECT COUNT(*) total FROM wiki_contributions"))["total"] == 1
    assert await store.one("SELECT * FROM wiki_jobs WHERE id=%s", (current_job,)) == before


async def test_page_edit_publishes_while_channel_paused_and_survives_stale_manifest(store):
    ch, _, page, revision, _ = await published(store)
    channel_id = ch["id"]
    service = WikiRuntime(store, None, None)
    service.engine = WikiEngine(store, runtime=lambda _: (FakeAI(), "fixture-model"))
    knowledge = service.engine.knowledge
    channel_job_id = await store.enqueue(channel_id, "reindex")
    channel_job = await store.claim_job()
    channel_job["cursor"]["revisions"] = {page["id"]: revision}
    fresh = await store.channel(channel_id)
    channel_job["progress"].update(
        generation_complete=True, pages=1, source_generation=fresh["source_generation"]
    )
    await service.engine.review(channel_job, fresh)
    await store.configure(
        channel_id,
        fresh["config"].model_copy(update={"paused": True, "publication_mode": "auto"}),
        "test",
    )
    before = await store.one("SELECT * FROM wiki_jobs WHERE id=%s", (channel_job_id,))
    assert before["status"] == "paused"
    corrected, edit_id = await knowledge.correct_page(channel_id, revision, 0, "人工核对。", "test")
    _, stale_id = await knowledge.correct_page(channel_id, revision, 0, "另一版改动。", "test")
    edit = await store.claim_job()
    assert edit["id"] == edit_id and await store.claim_job() is None
    await service.run_job(edit)
    assert edit["progress"]["review_total"] == 1 and edit["progress"]["published"]
    await service.run_job(await store.claim_job())
    assert (await store.one("SELECT status FROM wiki_jobs WHERE id=%s", (stale_id,)))[
        "status"
    ] == "failed"
    assert await store.one("SELECT * FROM wiki_jobs WHERE id=%s", (channel_job_id,)) == before
    article = await service.reader.page(channel_id, page["slug"])
    assert article["revision_id"] == corrected and article["claims"][0]["text"] == "人工核对。"
    await store.configure(channel_id, fresh["config"], "test")
    await store.control_job(channel_id, channel_job_id, "resume", "test")
    await service.run_job(await store.claim_job())
    await knowledge.publish(channel_id, channel_job_id, "test")
    assert (await service.reader.page(channel_id, page["slug"]))["revision_id"] == corrected


async def test_fact_check_requires_real_evidence_and_reuses_cached_claims(store, monkeypatch):
    from core.wiki import web_research

    ch = await channel(store)
    await store.configure(
        ch["id"],
        ch["config"].model_copy(update={"fact_check": True, "fact_check_required": True}),
        "test",
    )
    await store.put_source(
        ch["id"],
        "message",
        1,
        "Sakura 支持中文检索。",
        datetime(2020, 1, 1),
        verified=True,
        message_id=1,
    )
    settings = web_research.SearchSettings(enabled=True)
    monkeypatch.setattr(web_research, "search_settings", lambda: settings)
    results = web_research.normalized_results(
        "deepseek",
        {
            "results": [
                {
                    "url": "https://example.org/project",
                    "title": "Project docs",
                    "content": "Supports Chinese",
                },
                {
                    "url": "https://example.net/release",
                    "title": "Release notes",
                    "content": "Supports Chinese",
                },
            ]
        },
        3,
    )
    service = WikiRuntime(store, None, None)
    research = AsyncMock(return_value={"results": results})

    class Reviewer(FakeAI):
        async def create(self, **request):
            response = await super().create(**request)
            payload = json.loads(request["messages"][1]["content"])
            if "external_evidence" in payload:
                message = response.choices[0].message
                verdict = json.loads(message.content)
                for fact in verdict["claims"]:
                    fact.update(fact_status="verified", citations=[0, 1, 999])
                message.content = json.dumps(verdict)
            return response

    fake = Reviewer()
    service.engine = WikiEngine(store, runtime=lambda _: (fake, "fixture-model"))
    service.engine.research = SimpleNamespace(search=research)
    job = await store.enqueue(ch["id"], "reindex")
    await service.run_job(await store.claim_job())
    await service.engine.knowledge.publish(ch["id"], job, "test")
    page = await store.one("SELECT id FROM wiki_pages WHERE title='Sakura'")
    article = await service.reader.page(ch["id"], page["id"])
    assert article["claims"][0]["fact_check"]["status"] == "verified"
    assert len(article["claims"][0]["fact_check"]["sources"]) == 2
    charged = research.await_count
    await store.enqueue(ch["id"], "reindex")
    await service.run_job(await store.claim_job())
    assert research.await_count == charged


async def test_page_additive_counts_match_true_totals(store):
    ch, _, page, _, _ = await published(store)
    article = await PublicReader(store).page(ch["id"], page["slug"])
    # 夹具只有一条来源与一次发布：500/50 截断不会触发，但总量键必须与真实值一致
    assert article["sources_total"] == len(article["sources"]) == 1
    assert article["history_total"] == len(article["history"]) == 1


async def test_uploaded_cover_replaces_and_withdraws_with_channel(store, monkeypatch):
    ch, _, _, _, _ = await published(store)
    channel_id = ch["id"]
    reader = PublicReader(store)
    config = ch["config"].model_copy(update={"cover_url": "https://example.com/cover.png"})
    await store.configure(channel_id, config, "test")
    versions = await reader.visibility()
    monkeypatch.setattr(wiki, "get_wiki_runtime", lambda: SimpleNamespace(ready=True, store=store))
    monkeypatch.setattr(
        "core.web_api.middleware.verify_token", lambda _: WebUIUser(user_id="admin")
    )
    admin_path = f"/api/wiki/admin/channels/{channel_id}/cover"
    public_path = f"/api/public/wiki/channels/{channel_id}/cover"
    auth = {"Authorization": "Bearer test"}
    _wiki_buckets.clear()
    app = FastAPI()
    app.add_middleware(AuthMiddleware)
    app.include_router(wiki.public, prefix="/api/public/wiki")
    app.include_router(wiki.admin, prefix="/api/wiki/admin")
    async with AsyncClient(transport=ASGITransport(app=app), base_url="http://test") as client:
        assert (await client.get(public_path)).status_code == 404
        for color in ("red", "blue"):
            payload = BytesIO()
            Image.new("RGB", (16, 8), color).save(payload, "PNG")
            image = payload.getvalue()
            uploaded = await client.put(admin_path, content=image, headers=auth)
            assert uploaded.status_code == 200
            cover_digest = uploaded.json()["cover_digest"]
            summary = await reader.channel(channel_id, summary=True)
            assert summary["cover_url"] == f"{public_path}?v={cover_digest}"
            result = await client.get(summary["cover_url"])
            assert result.content == image
            assert result.headers["content-type"] == "image/png"
            assert "no-cache" in result.headers["cache-control"]
            tag = {"If-None-Match": result.headers["etag"]}
            assert (await client.get(public_path, headers=tag)).status_code == 304
            head = await client.head(public_path)
            assert head.content == b"" and int(head.headers["content-length"]) == len(image)
            if color == "red":
                previous_url = summary["cover_url"]
        assert (await client.get(previous_url)).status_code == 404
        assert (await reader.visibility())[channel_id] != versions[channel_id]
        await store.configure(channel_id, config.model_copy(update={"public": False}), "test")
        denied = await client.get(public_path, headers=tag)
        assert denied.status_code == 404
        assert denied.headers["cache-control"] == "no-store"
        assert (await client.delete(admin_path, headers=auth)).json() == {"cover_digest": None}
        await store.configure(channel_id, config, "test")
        assert (await reader.channel(channel_id, summary=True))["cover_url"] == config.cover_url


@pytest.mark.parametrize("mode,q", [("latest", ""), ("recommended", ""), ("search", "中文")])
async def test_feed_cursor_only_returns_visible_published_pages(store, mode, q):
    first = await published(store, channel_id=12345)
    second = await published(store, channel_id=12346)
    third = await published(store, channel_id=12347)
    records = {record[2]["id"]: record for record in (first, second, third)}
    reader = PublicReader(store)
    batch = await reader.feed(mode=mode, q=q, limit=1)
    assert batch["has_more"] and batch["items"][0]["evidence_count"] == 1
    cursor = batch["next_cursor"]
    following = await reader.feed(mode=mode, q=q, cursor=cursor, limit=2)
    assert len(following["items"]) == 2
    assert not following["has_more"]
    assert {item["id"] for item in batch["items"] + following["items"]} == records.keys()
    assert following == await reader.feed(mode=mode, q=q, cursor=cursor, limit=2)
    with pytest.raises(ValueError, match="游标"):
        await reader.feed(mode=mode, q=q, topic="其他范围", cursor=cursor)
    revoked = following["items"][0]["id"]
    channel_row, source, *_ = records[revoked]
    await store.revoke(channel_row["id"], source_id=source["id"], actor="test")
    after = await reader.feed(mode=mode, q=q, cursor=cursor, limit=2)
    assert len(after["items"]) == 1 and after["items"][0]["id"] != revoked


async def test_article_parts_and_timeline_cursor_share_publication_gate(store):
    ch, source, page, revision, publication = await published(store)
    reader = PublicReader(store)
    article = await reader.page(ch["id"], page["slug"], part="article")
    refs = await reader.page(ch["id"], page["slug"], part="references")
    assert article["claims"] and "history" not in article and article["history_total"] == 1
    assert refs["publication_id"] == publication and refs["revision_id"] == revision
    assert refs["sources_total"] == 1 and "claims" not in refs
    assert await reader.related(ch["id"], page["slug"]) == {"next": None, "previous": None}
    before = await reader.visibility()
    await store.query(
        """INSERT INTO wiki_revision_claims (revision_id,ordinal,occurred_at,source_created_at,payload)
        SELECT revision_id,ordinal+1,occurred_at,source_created_at,payload
        FROM wiki_revision_claims WHERE revision_id=%s""",
        (revision,),
    )
    first = await reader.timeline(ch["id"], cursor_mode=True, page_size=1)
    assert first["has_more"]
    second = await reader.timeline(
        ch["id"], cursor_mode=True, page_size=1, cursor=first["next_cursor"]
    )
    assert (
        not second["has_more"] and second["items"][0]["event_id"] != first["items"][0]["event_id"]
    )
    await store.revoke(ch["id"], source_id=source["id"], actor="test")
    assert (await reader.visibility())[ch["id"]] != before[ch["id"]]
    assert await reader.page(ch["id"], page["slug"], part="references") is None
    assert await reader.page(ch["id"], page["slug"], compare=(revision, revision)) is None
    assert await reader.page(ch["id"], page["slug"], revision=revision) is None
    assert await reader.related(ch["id"], page["slug"]) is None
    assert not (await reader.timeline(ch["id"], cursor_mode=True))["items"]


async def test_topic_trends_window_is_bounded_but_topics_remain_all_time(store):
    ch, source, _, _, _ = await published(store)
    knowledge = Knowledge(store)
    # 夹具来源创建于 2020-01-01：全量主题统计到它，24 个月趋势窗口不包含它
    profile = await knowledge.profile(ch["id"])
    assert [t["message_count"] for t in profile["topics"]] == [1]
    assert profile["topic_trends"] == []
    await store.query(
        "UPDATE wiki_sources SET created_at=UTC_TIMESTAMP(6) WHERE id=%s", (source["id"],)
    )
    fresh = await knowledge.profile(ch["id"])
    # 移入窗口后趋势恢复，且与按月统计的最新月份一致
    assert fresh["topic_trends"][0]["message_count"] == 1
    assert fresh["topic_trends"][0]["month"] == fresh["months"][-1]["month"]


async def test_source_dedup_stale_edits_and_delete_tombstone(store):
    ch = await channel(store)
    sid = await store.put_source(
        ch["id"],
        "message",
        7,
        "新编辑",
        datetime(2020, 1, 1),
        edited_at=datetime(2022, 1, 1),
        verified=True,
        message_id=7,
    )
    await store.put_source(
        ch["id"], "message", 7, "旧原文", datetime(2020, 1, 1), verified=True, message_id=7
    )
    await store.put_source(
        ch["id"],
        "message",
        7,
        "新编辑",
        datetime(2020, 1, 1),
        edited_at=datetime(2022, 1, 1),
        verified=True,
        message_id=7,
    )
    row = await store.one("SELECT * FROM wiki_sources WHERE id=%s", (sid,))
    assert row["body"] == "新编辑" and row["revision"] == 1
    await store.revoke(ch["id"], message_ids=[7, 8], deleted=True)
    await store.put_source(
        ch["id"], "message", 8, "扫描得到旧原文", datetime(2020, 1, 1), verified=True, message_id=8
    )
    row = await store.one(
        "SELECT * FROM wiki_sources WHERE id=%s", (digest(ch["id"], "message", "8"),)
    )
    assert row["deleted"] and row["body"] == ""


async def test_alias_identity_private_channel_and_job_mutex(store):
    ch = await channel(store, public=False)
    assert (await store.channel("-10012345"))["id"] == ch["id"]
    assert (await store.channel("@CHANNEL_12345"))["id"] == ch["id"]
    ids = await asyncio.gather(*(store.enqueue(ch["id"], "backfill") for _ in range(4)))
    assert len(set(ids)) == 1
    first = await store.claim_job()
    assert first and not await store.claim_job()
    await store.control_job(ch["id"], first["id"], "pause", "test")
    with pytest.raises(LeaseLost):
        await store.checkpoint(first)
    assert not await store.claim_job()
    await store.control_job(ch["id"], first["id"], "resume", "test")
    resumed = await store.claim_job()
    assert resumed["lease_token"] != first["lease_token"]
    assert await PublicReader(store).channels() == []


async def test_expired_worker_is_fenced_and_recovered(store):
    ch = await channel(store)
    await store.enqueue(ch["id"])
    old = await store.claim_job()
    await store.query(
        "UPDATE wiki_jobs SET lease_until=DATE_SUB(UTC_TIMESTAMP(6),INTERVAL 1 SECOND) WHERE id=%s",
        (old["id"],),
    )
    new = await store.claim_job()
    assert new["id"] == old["id"] and new["lease_token"] != old["lease_token"]
    with pytest.raises(LeaseLost):
        await store.finish(old)


async def test_full_history_more_than_week_and_resume(store, monkeypatch):
    ch = await channel(store)
    messages = [
        SimpleNamespace(
            id=i,
            message=f"消息{i}",
            date=datetime(2019, 1, 1) + timedelta(days=i),
            edit_date=None,
            photo=None,
            document=None,
            action=None,
        )
        for i in range(1, 206)
    ]
    messages[-1].message = "超长消息🙂" * 4000 + "末尾事实"

    class Client:
        fail = True

        def is_connected(self):
            return True

        async def get_entity(self, _):
            return SimpleNamespace(
                id=12345, username="channel_12345", title="知识频道", broadcast=True
            )

        async def get_messages(self, _, **kwargs):
            return SimpleNamespace(total=205)

        async def iter_messages(self, _, min_id, reverse, limit, wait_time):
            assert reverse and limit == 100
            for message in [m for m in messages if m.id > min_id][:limit]:
                if self.fail and message.id == 110:
                    self.fail = False
                    raise ConnectionError("offline")
                yield message

    collector = SourceCollector(store, Client())
    await store.enqueue(ch["id"], "backfill")
    job = await store.claim_job()
    with pytest.raises(ConnectionError):
        await collector.history(job, ch)
    assert job["cursor"]["history_id"] == 109
    await collector.history(job, ch)
    assert job["progress"]["enumerated"] == 205
    last = await store.one("SELECT body FROM wiki_sources WHERE message_id=205")
    assert last["body"] == messages[-1].message
    assert (await store.one("SELECT COUNT(*) n FROM wiki_sources"))["n"] == 205


async def test_public_profile_counts_real_text_and_config_revocation(store):
    ch, _, page, _, _ = await published(store)
    await store.put_source(
        ch["id"],
        "message",
        2,
        "四个文字",
        datetime(2021, 1, 1),
        verified=True,
        message_id=2,
        original_length=4,
    )
    await store.put_source(
        ch["id"], "summary", 999, "derived summary", datetime(2021, 1, 1), verified=False
    )
    profile = await PublicReader(store).profile(ch["id"])
    assert profile["statistics"]["message_count"] == 2
    assert float(profile["statistics"]["avg_message_length"]) == 9
    await store.configure(ch["id"], WikiConfig(enabled=True, public=False), "test")
    assert await PublicReader(store).page(ch["id"], page["slug"]) is None
    assert await PublicReader(store).profile(ch["id"]) is None


async def test_go_reads_identical_publication_and_revocation(store):
    ch, source, page, _, pub = await published(store)
    env = dict(
        os.environ,
        **GO_TEST_ENV,
        WIKI_MYSQL_DSN=f"root@tcp(127.0.0.1:{TEST_PORT})/{store.test_database}?parseTime=true",
        WIKI_EXPECTED_PUBLICATION=pub,
        WIKI_EXPECTED_PAGE=page["slug"],
    )
    result = await asyncio.to_thread(
        subprocess.run,
        ["go", "test", "./internal/db", "-run", "TestWikiSharedMySQL", "-count=1"],
        cwd=Path(__file__).parents[2] / "qa-bot-go",
        env=env,
        capture_output=True,
        text=True,
    )
    assert result.returncode == 0, result.stdout + result.stderr

    await store.revoke(ch["id"], source_id=source["id"])
    env["WIKI_EXPECT_REVOKED"] = "1"
    result = await asyncio.to_thread(
        subprocess.run,
        ["go", "test", "./internal/db", "-run", "TestWikiSharedMySQL", "-count=1"],
        cwd=Path(__file__).parents[2] / "qa-bot-go",
        env=env,
        capture_output=True,
        text=True,
    )
    assert result.returncode == 0, result.stdout + result.stderr


class FakeAI:
    """模拟生成模型与来源审校模型的响应。"""

    def __init__(self, *, incomplete=False, stable=False, review_status="supported"):
        self.calls = 0
        self.review_calls = 0
        self.incomplete = incomplete
        self.review_status = review_status
        # stable：按消息内容前缀决定词条归属。默认按批次下标奇偶（既有测试契
        # 约）；source id 是哈希，批次构成在编辑/删除后会变，增量测试必须用
        # stable 才能得到确定的页面归属。
        self.stable = stable
        self.chat = SimpleNamespace(completions=self)
        self.base_url = "https://fixture.invalid"

    def with_options(self, **_):
        return self

    async def create(self, **request):
        payload = json.loads(request["messages"][1]["content"])
        if "claims" in payload:
            self.review_calls += 1
            content = json.dumps(
                {
                    "approved": self.review_status == "supported",
                    "reason": "审校结论",
                    "claims": [
                        {"index": item["index"], "status": self.review_status, "reason": "来源审校"}
                        for item in payload["claims"]
                    ]
                    if self.review_status != "missing"
                    else [],
                }
            )
        elif "summaries" in payload:
            content = "中文检索\nSakura"
        elif "sources" in payload or "source" in payload:
            sources = payload.get("sources") or [{"source": 0, "text": payload["source"]}]
            sections = []
            for item in sources:
                index, text = item["source"], item["text"]
                if self.stable:
                    title = (
                        "软件工具 / 第一条词条"
                        if text.startswith("第一条")
                        else "频道资料 / 第二条词条"
                    )
                else:
                    title = "频道资料 / 中文搜索" if index % 2 else "软件工具 / Sakura"
                body = f"根据原文整理：{text[:80]}"
                if index == 0:
                    body += "\n\n相关条目：[[中文搜索]]"
                sections.append(f"## [{index}] {title}\n\n{body}")
            content = "\n\n".join(sections)
        elif "statistics" in payload:
            content = "频道以中文检索与技术说明为主，长期保持稳定的更新节奏。"
        else:
            raise AssertionError(f"未知的生成请求负载：{sorted(payload)}")
        self.calls += int("claims" not in payload)
        return SimpleNamespace(
            choices=[
                SimpleNamespace(
                    finish_reason="length" if self.incomplete else "stop",
                    message=SimpleNamespace(content=content),
                )
            ],
            usage=SimpleNamespace(prompt_tokens=100, completion_tokens=100),
        )


class FlakyAI(FakeAI):
    """前 fail_on 次调用抛连接错误，用于验证任务中断恢复。"""

    def __init__(self, fail_on=1):
        super().__init__()
        self.fail_on = fail_on

    async def create(self, **request):
        if self.calls < self.fail_on:
            self.calls += 1
            raise ConnectionError("model offline")
        return await super().create(**request)


def fake_message(id, text, edited=None):
    return SimpleNamespace(
        id=id,
        message=text,
        date=datetime(2020, 1, 1) + timedelta(days=id),
        edit_date=edited,
        photo=None,
        document=None,
        action=None,
    )


class UpdateClient:
    """模拟在线 UserBot：记录 update 任务的历史扫描起点，按需回放新消息。"""

    def __init__(self, messages=()):
        self.messages = list(messages)
        self.scan_starts = []

    def is_connected(self):
        return True

    async def get_entity(self, _):
        return SimpleNamespace(id=12345, username="channel_12345", title="知识频道", broadcast=True)

    async def get_messages(self, _, **kwargs):
        return SimpleNamespace(total=len(self.messages))

    async def iter_messages(self, _, min_id, reverse, limit, wait_time):
        assert reverse and limit == 100
        self.scan_starts.append(min_id)
        for message in [m for m in self.messages if m.id > min_id][:limit]:
            yield message


async def build_and_publish(
    store, *, texts=("Sakura 支持中文检索。",), model="fixture-model", stable=False
):
    """用 fixture 模型构建并自动发布一个小型知识库，供增量断言使用。"""
    ch = await channel(store)
    await store.configure(
        ch["id"], ch["config"].model_copy(update={"publication_mode": "auto"}), "test"
    )
    for index, text in enumerate(texts, start=1):
        await store.put_source(
            ch["id"],
            "message",
            index,
            text,
            datetime(2020, 1, index),
            verified=True,
            message_id=index,
            original_length=len(text),
        )
    fake = FakeAI(stable=stable)
    runtime = WikiRuntime(store, None, None)
    runtime.engine = WikiEngine(store, runtime=lambda _: (fake, model))
    await store.enqueue(ch["id"], "reindex")
    await runtime.run_job(await store.claim_job())
    assert (await store.channel(ch["id"]))["published_revision"]
    return ch, fake, runtime


async def fact_ids(store, sql, args=()):
    return [row["id"] for row in await store.query(sql, args, fetch=True)]


async def page_of_message(store, channel_id, message_id):
    """消息的知识归属页面。source id 是哈希，批次内顺序不保证与消息号一致，
    页面归属须从事实表动态查询而非按 Fixture 的奇偶下标假设。"""
    row = await store.one(
        """SELECT f.page_id FROM wiki_facts f
        JOIN wiki_chunks ch ON ch.id=f.chunk_id JOIN wiki_sources s ON s.id=ch.source_id
        WHERE s.channel_id=%s AND s.message_id=%s""",
        (channel_id, message_id),
    )
    assert row, "fixture 消息应已产生知识事实"
    return row["page_id"]


async def test_grouped_telegram_album_replaces_split_pages_and_preserves_sources(store):
    ch, fake, runtime = await build_and_publish(
        store, texts=("第一条相册配文", "第二条图片识别"), stable=True
    )
    channel_id = ch["id"]
    caption_page = await page_of_message(store, channel_id, 1)
    image_page = await page_of_message(store, channel_id, 2)
    assert caption_page != image_page
    date = datetime(2020, 1, 2)
    await store.query(
        """UPDATE wiki_sources SET media_group='album-1',category='image',media_key='photo:2'
        WHERE channel_id=%s AND message_id=2""",
        (channel_id,),
    )
    await store.query(
        "UPDATE wiki_sources SET media_group='album-1',category='image' WHERE channel_id=%s AND message_id=1",
        (channel_id,),
    )
    # The image data already exists on disk; no Telegram backfill is needed for grouping.
    await store.put_source(
        channel_id,
        "image",
        2,
        "第二张图中出现二维码",
        date,
        verified=True,
        media_group="album-1",
        message_id=2,
        category="image",
        media_key="photo:2",
    )
    member = await store.put_source(
        channel_id,
        "message",
        3,
        "",
        date,
        verified=True,
        media_group="album-1",
        message_id=3,
        category="image",
        media_key="photo:3",
    )
    album_inputs = []

    async def request(job, config, generator, system, payload, **options):
        if "album" in payload:
            album_inputs.append(payload["album"])
            return "## [0] 频道资料 / 测试相册\n\n相册包含配文和二维码。"
        return await original_request(job, config, generator, system, payload, **options)

    original_request = runtime.engine.request
    runtime.engine.request = request
    await store.enqueue(channel_id, "albums")
    await runtime.run_job(await store.claim_job())
    reader = runtime.reader
    article = await reader.page(channel_id, caption_page)
    assert article and await reader.page(channel_id, image_page) is None
    assert len(album_inputs) == 1
    assert any(x["text"] == "第一条相册配文" for x in album_inputs[0])
    assert any(
        x["text"] == "第二张图中出现二维码" and x["type"] == "图片识别" for x in album_inputs[0]
    )
    assert {link["message_id"] for link in article["album_links"]} == {1, 2, 3}
    assert {item["type"] for item in article["sources"]} == {"image", "message"}
    await store.revoke(channel_id, source_id=member)
    assert {
        item["message_id"] for item in (await reader.page(channel_id, caption_page))["album_links"]
    } == {1, 2}
    await store.revoke(channel_id, message_ids=[2], deleted=True)
    assert await reader.page(channel_id, caption_page) is None
    await store.enqueue(channel_id, "albums")
    await runtime.run_job(await store.claim_job())
    repaired = await reader.page(channel_id, caption_page)
    assert repaired and [item["message_id"] for item in repaired["album_links"]] == [1]


async def test_generation_review_graph_cache_and_incremental_publication(store):
    ch = await channel(store)
    await store.configure(
        ch["id"], ch["config"].model_copy(update={"publication_mode": "auto"}), "test"
    )
    await store.put_source(
        ch["id"],
        "message",
        1,
        "项目支持中文检索。" * 1500,
        datetime(2020, 1, 1),
        verified=True,
        message_id=1,
    )
    fake = FakeAI()
    reviewer = FakeAI()
    runtime = WikiRuntime(store, None, None)
    runtime.engine = WikiEngine(
        store,
        runtime=lambda purpose: (
            (reviewer, "fixture-review") if purpose == "wiki_review" else (fake, "fixture-model")
        ),
    )
    first_id = await store.enqueue(ch["id"], "reindex")
    await runtime.run_job(await store.claim_job())
    first = await store.one("SELECT * FROM wiki_jobs WHERE id=%s", (first_id,))
    assert first["status"] == "published", first
    assert fake.calls == 3  # 两个提取批次 + 一次频道简介
    assert reviewer.review_calls > 0 and reviewer.calls == fake.review_calls == 0
    reviews = await store.query("SELECT * FROM wiki_revision_reviews", fetch=True)
    assert len(reviews) == 2 and all(row["approved"] for row in reviews)
    assert {decode(row["result"])["model"] for row in reviews} == {"fixture-review"}
    publication = (await store.channel(ch["id"]))["published_revision"]
    graph = await runtime.reader.graph(ch["id"], limit=3)
    assert len(graph["nodes"]) == 2 and graph["edges"]
    assert all(edge["evidence_count"] > 0 for edge in graph["edges"])
    browse = await runtime.reader.browse_graph(ch["id"], limit=2)
    assert len(browse["nodes"]) == 2 and browse["edges"]
    assert browse["next_cursor"] is None and not browse["truncated"]
    first_page = await runtime.reader.browse_graph(ch["id"], limit=1)
    assert first_page["next_cursor"]
    second_page = await runtime.reader.browse_graph(
        ch["id"], limit=1, cursor=first_page["next_cursor"]
    )
    assert len(second_page["nodes"]) == 1 and second_page["next_cursor"] is None
    root = first_page["nodes"][0]["id"]
    neighbour = second_page["nodes"][0]["id"]
    assert root != neighbour
    neighbourhood = await runtime.reader.browse_graph(ch["id"], root, limit=1)
    assert {node["id"] for node in neighbourhood["nodes"]} == {root, neighbour}
    assert neighbourhood["edges"]
    assert not (await runtime.reader.browse_graph(ch["id"], relation="unknown"))["nodes"]
    timeline = await runtime.reader.timeline(ch["id"], page_size=1)
    assert len(timeline["items"]) == 1 and timeline["total_pages"] >= 2
    charged = fake.calls
    reviewed = reviewer.review_calls
    await store.enqueue(ch["id"], "reindex")
    await runtime.run_job(await store.claim_job())
    assert fake.calls == charged
    assert reviewer.review_calls == reviewed
    # 内容未变化：不创建重复 publication，沿用当前发布快照
    assert (await store.channel(ch["id"]))["published_revision"] == publication
    assert (await store.one("SELECT COUNT(*) n FROM wiki_publications"))["n"] == 1
    # Current visibility is always checked even after a successful prior graph request.
    await store.revoke(ch["id"], message_ids=[1], deleted=True)
    assert not (await runtime.reader.graph(ch["id"]))["nodes"]
    assert not (await runtime.reader.browse_graph(ch["id"]))["nodes"]
    assert await runtime.reader.browse_graph(ch["id"], root) is None


@pytest.mark.parametrize("review_status", ["unsupported", "missing"])
async def test_failed_review_preserves_existing_publication(store, monkeypatch, review_status):
    ch, _, runtime = await build_and_publish(store)
    publication = (await store.channel(ch["id"]))["published_revision"]
    await store.put_source(
        ch["id"], "message", 2, "新来源", datetime(2020, 1, 2), verified=True, message_id=2
    )
    reviewer = FakeAI(review_status=review_status)
    generator = FakeAI()
    runtime.engine.runtime = lambda purpose: (
        (reviewer, "review-rejection") if purpose == "wiki_review" else (generator, "fixture-model")
    )
    job_id = await store.enqueue(ch["id"], "reindex")
    await runtime.run_job(await store.claim_job())
    saved = await store.one("SELECT * FROM wiki_jobs WHERE id=%s", (job_id,))
    progress = decode(saved["progress"])
    assert not progress["review_complete"]
    assert reviewer.review_calls > 0
    assert (await store.channel(ch["id"]))["published_revision"] == publication
    if review_status == "unsupported":
        assert saved["status"] == "completed" and progress["review_rejections"] > 0
        rejected = await store.one(
            "SELECT revision_id FROM wiki_revision_reviews WHERE approved=FALSE"
        )
        assert rejected
        monkeypatch.setattr(wiki, "get_wiki_runtime", lambda: runtime)
        runtime.ready = True
        retry = await wiki.retry_revision_review(ch["id"], rejected["revision_id"], "test")
        audit = await store.one(
            "SELECT details FROM wiki_review_actions WHERE action='retry_revision_review'"
        )
        assert decode(audit["details"])["previous_reviews"]
        reviewer.review_status = "supported"
        await runtime.run_job(await store.claim_job())
        saved = await store.one("SELECT status FROM wiki_jobs WHERE id=%s", (retry["job_id"],))
        assert saved["status"] == "published"
    else:
        assert saved["status"] == "partial" and saved["phase"] == "review"
        assert saved["error_code"] == "WikiResponseError"
        await store.control_job(ch["id"], job_id, "retry", "test")
        reviewer.review_status = "supported"
        generation_calls = generator.calls
        await runtime.run_job(await store.claim_job())
        saved = await store.one("SELECT * FROM wiki_jobs WHERE id=%s", (job_id,))
        assert saved["status"] == "published", saved
        assert generator.calls == generation_calls + 1  # Only the channel introduction.


async def test_disabled_ai_review_allows_page_edit_without_model_and_rechecks_policy(store):
    ch, _, page, revision, _ = await published(store)
    channel_id = ch["id"]
    config = (await store.channel(channel_id))["config"]
    await store.configure(
        channel_id, config.model_copy(update={"ai_review_enabled": False}), "test"
    )
    service = WikiRuntime(store, None, None)
    service.engine.client = lambda _: pytest.fail("Skipped review called a model")
    replacement, job_id = await service.engine.knowledge.correct_page(
        channel_id, revision, 0, "人工修订。", "test"
    )
    job = await store.claim_job()
    await service.run_job(job)
    assert job["progress"]["review_skipped"] and job["progress"]["review_complete"]
    await store.configure(channel_id, config, "test")
    with pytest.raises(ValueError, match="重新启用"):
        await service.engine.knowledge.publish(channel_id, job_id, "test")
    await store.configure(
        channel_id, config.model_copy(update={"ai_review_enabled": False}), "test"
    )
    await service.engine.knowledge.publish(channel_id, job_id, "test")
    assert (await service.reader.page(channel_id, page["slug"]))["revision_id"] == replacement


async def test_publish_requires_review_of_exact_document(store):
    ch = await channel(store)
    await store.put_source(
        ch["id"], "message", 1, "原始消息", datetime(2020, 1, 1), verified=True, message_id=1
    )
    runtime = WikiRuntime(store, None, None)
    ai = FakeAI()
    runtime.engine = WikiEngine(store, runtime=lambda _: (ai, "fixture-model"))
    job_id = await store.enqueue(ch["id"], "reindex")
    await runtime.run_job(await store.claim_job())
    row = await store.one("SELECT * FROM wiki_jobs WHERE id=%s", (job_id,))
    cursor = decode(row["cursor"])
    revision_id = next(iter(cursor["revisions"].values()))
    assert decode(row["progress"])["review_complete"]
    assert not (
        await store.one("SELECT approved FROM wiki_page_revisions WHERE id=%s", (revision_id,))
    )["approved"]
    await store.query(
        "UPDATE wiki_page_revisions SET document=JSON_SET(document,'$.title','审校后更改') WHERE id=%s",
        (revision_id,),
    )
    with pytest.raises(ValueError, match="independent review"):
        await runtime.engine.knowledge.publish(ch["id"], job_id, "test")
    await store.query(
        "UPDATE wiki_jobs SET `cursor`=JSON_REMOVE(`cursor`,'$.review_policy') WHERE id=%s",
        (job_id,),
    )
    with pytest.raises(ValueError, match="predates independent review"):
        await runtime.engine.knowledge.publish(ch["id"], job_id, "test")
    assert not (await store.channel(ch["id"]))["published_revision"]


async def test_manual_mode_and_budget_pause_do_not_publish(store):
    ch = await channel(store)
    await store.put_source(
        ch["id"],
        "message",
        1,
        "包含尚待核实的断言",
        datetime(2020, 1, 1),
        verified=True,
        message_id=1,
    )
    runtime = WikiRuntime(store, None, None)
    runtime.engine = WikiEngine(store, runtime=lambda _: (FakeAI(), "fixture-model"))
    job_id = await store.enqueue(ch["id"], "reindex")
    await runtime.run_job(await store.claim_job())
    job = await store.one("SELECT * FROM wiki_jobs WHERE id=%s", (job_id,))
    # 手动发布模式先完成独立审校，再等待管理员发布。
    assert job["status"] == "completed", job
    assert decode(job["progress"])["review_complete"]
    assert (await store.channel(ch["id"]))["published_revision"] is None
    ch = await store.channel(ch["id"])
    await store.configure(ch["id"], ch["config"].model_copy(update={"token_budget": 1000}), "test")
    fake = FakeAI()
    runtime.engine = WikiEngine(store, runtime=lambda _: (fake, "different-model"))
    job_id = await store.enqueue(ch["id"], "reindex")
    await runtime.run_job(await store.claim_job())
    job = await store.one("SELECT * FROM wiki_jobs WHERE id=%s", (job_id,))
    assert job["status"] == "paused" and job["error_code"] == "token_budget_reached"
    assert fake.calls == 0


async def test_manual_correction_lock_and_source_revocation(store):
    ch, source, page, revision, _ = await published(store)
    knowledge = Knowledge(store)
    corrected = await knowledge.correct(
        ch["id"], revision, 0, "经管理员复核的中文检索说明", "admin"
    )
    job_id = await store.enqueue(ch["id"], "reindex")
    job = await store.claim_job()
    page = await store.one("SELECT * FROM wiki_pages WHERE id=%s", (page["id"],))
    assert await knowledge.build_page(job, page) == corrected
    assert job_id == job["id"]
    await store.revoke(ch["id"], source_id=source["id"])
    with pytest.raises(ValueError):
        await knowledge.correct(ch["id"], corrected, 0, "重新公开", "admin")


async def test_replaced_image_revokes_unchanged_ocr_before_new_recognition(store):
    ch, source, page, _, _ = await published(store, image=True)
    reader = PublicReader(store)
    assert await reader.page(ch["id"], page["slug"])
    await store.put_source(
        ch["id"],
        "message",
        1,
        "图文",
        datetime(2020, 1, 1),
        edited_at=datetime(2021, 1, 1),
        verified=True,
        message_id=1,
        media_key="photo:2",
        category="image",
    )
    assert (await store.one("SELECT content_hash FROM wiki_sources WHERE id=%s", (source["id"],)))[
        "content_hash"
    ] == source["content_hash"]
    assert await reader.page(ch["id"], page["slug"]) is None
    assert not (await reader.search("中文"))["items"]


async def test_delete_outbox_recovers_database_outage_and_duplicate_is_free(
    store, tmp_path, monkeypatch
):
    from unittest.mock import AsyncMock

    from core.wiki.outbox import EventOutbox

    ch, source, page, _, _ = await published(store)
    runtime = WikiRuntime(store, None, None)
    runtime.known_channels = {ch["id"]}
    runtime.outbox = EventOutbox(tmp_path / "events")
    revoke = store.revoke
    monkeypatch.setattr(store, "revoke", AsyncMock(side_effect=ConnectionError("offline")))
    await runtime.on_delete(SimpleNamespace(chat_id=-10012345, deleted_ids=[1]))
    assert len(runtime.outbox.pending()) == 1
    monkeypatch.setattr(store, "revoke", revoke)
    assert await runtime.flush_outbox()
    assert not runtime.outbox.pending()
    generation = (await store.channel(ch["id"]))["source_generation"]
    await store.revoke(ch["id"], message_ids=[1], deleted=True)
    assert (await store.channel(ch["id"]))["source_generation"] == generation
    assert await runtime.reader.page(ch["id"], page["slug"]) is None


async def test_incomplete_ai_response_retains_stable_publication(store):
    ch, _, page, _, pub = await published(store)
    await store.configure(
        ch["id"], ch["config"].model_copy(update={"retries": 0, "publication_mode": "auto"}), "test"
    )
    runtime = WikiRuntime(store, None, None)
    runtime.engine = WikiEngine(
        store, runtime=lambda _: (FakeAI(incomplete=True), "incomplete-model")
    )
    job_id = await store.enqueue(ch["id"], "reindex")
    await runtime.run_job(await store.claim_job())
    job = await store.one("SELECT * FROM wiki_jobs WHERE id=%s", (job_id,))
    assert job["status"] == "failed"
    assert (await runtime.reader.page(ch["id"], page["slug"]))["publication_id"] == pub


async def test_unverified_channel_expiry_denies_all_public_views(store):
    ch, _, page, _, _ = await published(store)
    await store.query(
        "UPDATE wiki_channels SET verified_until=DATE_SUB(UTC_TIMESTAMP(6),INTERVAL 1 SECOND) WHERE id=%s",
        (ch["id"],),
    )
    reader = PublicReader(store)
    assert not await reader.channels()
    assert await reader.channel(ch["id"]) is None
    assert await reader.page(ch["id"], page["slug"]) is None
    assert await reader.profile(ch["id"]) is None
    assert not (await reader.search("中文"))["items"]


@pytest.mark.parametrize(
    "policy",
    [
        "public=FALSE",
        "enabled=FALSE",
        "access_verified=FALSE",
        "username=NULL",
        "published_revision=NULL",
        "verified_until=DATE_SUB(UTC_TIMESTAMP(6),INTERVAL 1 SECOND)",
    ],
)
async def test_graph_denies_channel_metadata_when_publication_is_not_public(store, policy):
    ch, _, _, _, _ = await published(store)
    await store.query(f"UPDATE wiki_channels SET {policy} WHERE id=%s", (ch["id"],))
    assert await PublicReader(store).graph(ch["id"]) is None


async def test_summary_chunks_inform_planning_but_never_become_original_evidence(store):
    ch = await channel(store)
    await store.put_source(
        ch["id"], "summary", 99, "既有总结的辅助主题。" * 800, datetime(2020, 1, 2), verified=False
    )
    await store.put_source(
        ch["id"],
        "message",
        1,
        "Sakura 支持中文检索。",
        datetime(2020, 1, 1),
        verified=True,
        message_id=1,
    )
    runtime = WikiRuntime(store, None, None)
    fake = FakeAI()
    runtime.engine = WikiEngine(store, runtime=lambda _: (fake, "fixture-model"))
    await store.enqueue(ch["id"], "reindex")
    job = await store.claim_job()
    await runtime.run_job(job)
    saved = await store.one("SELECT * FROM wiki_jobs WHERE id=%s", (job["id"],))
    assert saved["status"] == "completed", saved
    assert decode(saved["cursor"])["summary_topics"]
    # 新管线中 summary 来源只进入规划上下文，不产生提取行，更不会成为原始证据
    contexts = await store.one(
        "SELECT COUNT(*) n FROM wiki_extractions e JOIN wiki_chunks c ON c.id=e.chunk_id JOIN wiki_sources s ON s.id=c.source_id WHERE s.source_type='summary'"
    )
    assert contexts["n"] == 0
    evidence = await store.query(
        "SELECT DISTINCT s.source_type FROM wiki_evidence e JOIN wiki_sources s ON s.id=e.source_id",
        fetch=True,
    )
    assert [e["source_type"] for e in evidence] == ["message"]


async def test_multistep_reads_pin_publication_and_next_request_observes_revocation(store):
    from contextvars import Context

    ch, _, page, _, publication = await published(store)
    reader = PublicReader(store)
    async with store.snapshot():
        assert (await reader.page(ch["id"], page["slug"]))["publication_id"] == publication
        await asyncio.create_task(
            store.configure(ch["id"], WikiConfig(enabled=True, public=False), "test"),
            context=Context(),
        )
        assert (await reader.page(ch["id"], page["slug"]))["publication_id"] == publication
    assert await reader.page(ch["id"], page["slug"]) is None


async def test_partial_manifest_cannot_publish_even_with_successful_extraction(store):
    ch, _, page, revision, publication = await published(store)
    await store.enqueue(ch["id"], "reindex")
    job = await store.claim_job()
    job["cursor"]["revisions"] = {page["id"]: revision}
    job["progress"].update(
        generation_complete=True,
        review_complete=True,
        source_generation=(await store.channel(ch["id"]))["source_generation"],
    )
    await store.checkpoint(job, "assemble")
    with pytest.raises(ValueError):
        await Knowledge(store).publish(ch["id"], job["id"], "test", running_job=job)
    assert (await store.channel(ch["id"]))["published_revision"] == publication


async def test_manual_alias_override_supports_mysql_reserved_identifiers(store):
    ch, _, page, _, _ = await published(store)
    knowledge = Knowledge(store)
    await knowledge.manage_page(ch["id"], page["id"], "merge", "test", alias="人工别名")
    row = await store.one(
        "SELECT page_id,`manual` FROM wiki_entity_aliases WHERE channel_id=%s AND alias_key=%s",
        (ch["id"], digest(name_key("人工别名"))),
    )
    assert row["page_id"] == page["id"] and row["manual"]


@pytest.mark.parametrize("target", ["current", "historical", "page_id"])
async def test_rename_rejects_other_page_links_without_partial_writes(store, target):
    ch, _, first, _, _ = await published(store)
    knowledge = Knowledge(store)
    async with store.transaction() as cur:
        other_id = await knowledge.entity(cur, ch["id"], "另一个项目")
    await knowledge.manage_page(
        ch["id"], other_id, "rename", "test", title="其他", slug="historical"
    )
    await knowledge.manage_page(ch["id"], other_id, "rename", "test", title="其他", slug="current")
    slug = other_id if target == "page_id" else target
    before = await store.channel(ch["id"])
    with pytest.raises(ValueError, match="占用"):
        await knowledge.manage_page(
            ch["id"], first["id"], "rename", "test", title="冲突", slug=slug
        )
    current = await store.one("SELECT title,slug FROM wiki_pages WHERE id=%s", (first["id"],))
    assert current == {"title": first["title"], "slug": first["slug"]}
    assert (await store.channel(ch["id"]))["source_generation"] == before["source_generation"]
    await knowledge.manage_page(
        ch["id"], first["id"], "rename", "test", title="新标题", slug="available"
    )
    reader = PublicReader(store)
    assert (await reader.page(ch["id"], first["slug"]))["id"] == first["id"]
    assert (await reader.page(ch["id"], "available"))["id"] == first["id"]


async def test_healthy_inflight_event_does_not_revoke_public_channel(store, tmp_path, monkeypatch):
    ch, _, page, _, _ = await published(store)
    import core.config as config_module
    from core.wiki.outbox import EventOutbox

    runtime = WikiRuntime(store, None, None)
    # 配置已加入但频道缓存尚未刷新的正面路径仍应立即采集。
    monkeypatch.setattr(config_module, "CHANNELS", ["@channel_12345"])
    runtime.outbox = EventOutbox(tmp_path / "events")
    entered, release = asyncio.Event(), asyncio.Event()

    async def hold_capture(*args):
        entered.set()
        await release.wait()

    monkeypatch.setattr(runtime.collector, "capture", hold_capture)
    event = SimpleNamespace(
        is_channel=True,
        chat=SimpleNamespace(id=12345, username="channel_12345", title="知识频道", broadcast=True),
        message=SimpleNamespace(
            id=2,
            message="新消息",
            date=datetime(2020, 1, 2),
            edit_date=None,
            photo=None,
            document=None,
        ),
    )
    task = asyncio.create_task(runtime.on_message(event))
    try:
        await asyncio.wait_for(entered.wait(), timeout=5)
        assert runtime.outbox.pending()
        assert await runtime.flush_outbox()
        assert (await store.channel(ch["id"]))["access_verified"]
        assert await runtime.reader.page(ch["id"], page["slug"])
    finally:
        release.set()
        await task
    assert not runtime.outbox.pending()


async def test_restart_recovers_unacknowledged_outbox_files(store, tmp_path):
    ch, _, page, _, _ = await published(store)
    from core.wiki.outbox import EventOutbox

    runtime = WikiRuntime(store, None, None)
    runtime.outbox = EventOutbox(tmp_path / "events")
    runtime.outbox.put({"kind": "delete", "channel_id": ch["id"], "ids": [1]})
    await runtime.start()
    assert not runtime.recovery_keys and not runtime.outbox.pending()
    assert await runtime.reader.page(ch["id"], page["slug"]) is None


async def test_unconfigured_channel_does_not_query_mysql_on_each_message(store, monkeypatch):
    from unittest.mock import AsyncMock

    import core.config as config_module

    runtime = WikiRuntime(store, None, None)
    monkeypatch.setattr(config_module, "CHANNELS", ["@configured_channel"])
    lookup = AsyncMock()
    monkeypatch.setattr(store, "channel", lookup)
    await runtime.on_message(
        SimpleNamespace(is_channel=True, chat=SimpleNamespace(id=77777, username="unrelated_group"))
    )
    lookup.assert_not_awaited()


async def test_timeline_batches_sources_without_loading_whole_articles(store, monkeypatch):
    from unittest.mock import AsyncMock

    ch, _, page, _, publication = await published(store)
    query = AsyncMock(wraps=store.query)
    monkeypatch.setattr(store, "query", query)
    timeline = await PublicReader(store).timeline(ch["id"])
    assert query.await_count == 4
    item = timeline["items"][0]
    assert item["page_id"] == page["id"] and item["publication_id"] == publication
    assert item["evidence"][0]["quote"] is None
    assert item["evidence"][0]["url"] == "https://t.me/channel_12345/1"


# ---------- Issue #153：增量索引、写入放大与快照治理的回归测试 ----------


async def test_empty_incremental_update_skips_unchanged_sources(store):
    """空增量：重复执行 update 不重新处理历史 Chunk，不产生 facts/edges 写入。"""
    ch, fake, runtime = await build_and_publish(store, stable=True)
    facts = await fact_ids(store, "SELECT id FROM wiki_facts ORDER BY id")
    publication = (await store.channel(ch["id"]))["published_revision"]
    charged = fake.calls
    runtime.collector.client = UpdateClient()
    await store.enqueue(ch["id"], "update")
    await runtime.run_job(await store.claim_job())
    job = await store.one(
        "SELECT * FROM wiki_jobs WHERE kind='update' ORDER BY created_at DESC LIMIT 1"
    )
    progress = decode(job["progress"])
    assert job["status"] == "published", job
    assert progress["processed_chunks"] == 0 and progress["cache_hits"] == 0
    assert fake.calls == charged  # 简介等全部命中缓存
    assert await fact_ids(store, "SELECT id FROM wiki_facts ORDER BY id") == facts
    assert (await store.channel(ch["id"]))["published_revision"] == publication
    assert (await store.one("SELECT COUNT(*) n FROM wiki_publications"))["n"] == 1
    cursor = decode(job["cursor"])
    assert "source_manifest" not in cursor and "completed_sources" not in cursor
    assert "source_after" not in cursor  # 终态游标已清理临时窗口状态


async def test_incremental_update_processes_only_new_sources(store):
    """少量新增：Telegram 请求量与提取量都只与新消息成正比。"""
    ch, fake, runtime = await build_and_publish(store, texts=("第一条消息",), stable=True)
    before = await fact_ids(store, "SELECT id FROM wiki_facts ORDER BY id")
    client = UpdateClient(messages=[fake_message(2, "第二条消息：新增功能说明")])
    runtime.collector.client = client
    charged = fake.calls
    await store.enqueue(ch["id"], "update")
    await runtime.run_job(await store.claim_job())
    # 采集只从已保存的最大 message_id 之后继续，不回退为全量扫描
    # （扫到新消息后按新游标再确认一轮是分页循环的正常行为）
    assert client.scan_starts[0] == 1 and 0 not in client.scan_starts
    # 只有新来源触发提取（1 批）+ 一次新简介；历史来源不再发请求
    assert fake.calls == charged + 2
    job = await store.one(
        "SELECT * FROM wiki_jobs WHERE kind='update' ORDER BY created_at DESC LIMIT 1"
    )
    progress = decode(job["progress"])
    assert progress["processed_chunks"] == 1 and progress["source_total"] == 1
    # 新事实写入且旧事实原样保留
    after = await fact_ids(store, "SELECT id FROM wiki_facts ORDER BY id")
    assert set(before) < set(after)
    # 内容变化产生新的发布快照
    assert (await store.channel(ch["id"]))["published_revision"] is not None


async def test_edited_source_reruns_and_rebuilds_only_affected_page(store):
    """编辑：仅受影响来源重新提取，其页面重建，无关页面修订保持不变。"""
    ch, fake, runtime = await build_and_publish(
        store, texts=("第一条消息", "第二条消息"), stable=True
    )
    edited_page = await page_of_message(store, ch["id"], 1)
    untouched_page = await page_of_message(store, ch["id"], 2)
    assert edited_page != untouched_page
    untouched = await fact_ids(
        store, "SELECT id FROM wiki_facts WHERE page_id=%s ORDER BY id", (untouched_page,)
    )
    edited_before = (
        await store.one(
            "SELECT COUNT(*) n FROM wiki_page_revisions WHERE page_id=%s", (edited_page,)
        )
    )["n"]
    old_chunk = await store.one(
        """SELECT ch.id FROM wiki_chunks ch JOIN wiki_sources s ON s.id=ch.source_id
        WHERE s.channel_id=%s AND s.message_id=1""",
        (ch["id"],),
    )
    await store.put_source(
        ch["id"],
        "message",
        1,
        "第一条消息（编辑后的内容）",
        datetime(2020, 1, 1),
        edited_at=datetime(2021, 1, 1),
        verified=True,
        message_id=1,
    )
    runtime.collector.client = UpdateClient()
    charged = fake.calls
    await store.enqueue(ch["id"], "update")
    await runtime.run_job(await store.claim_job())
    progress = decode(
        (
            await store.one(
                "SELECT progress FROM wiki_jobs WHERE kind='update' ORDER BY created_at DESC LIMIT 1"
            )
        )["progress"]
    )
    assert progress["processed_chunks"] == 1  # 只有被编辑的来源重新提取
    assert fake.calls == charged + 2  # 1 次提取 + 1 次新简介
    # 无关页面的事实与修订完全未动
    assert (
        await fact_ids(
            store, "SELECT id FROM wiki_facts WHERE page_id=%s ORDER BY id", (untouched_page,)
        )
        == untouched
    )
    assert (
        await store.one(
            "SELECT COUNT(*) n FROM wiki_page_revisions WHERE page_id=%s", (untouched_page,)
        )
    )["n"] == 1
    # 受影响页面产生新修订（事实内容变化）
    assert (
        await store.one(
            "SELECT COUNT(*) n FROM wiki_page_revisions WHERE page_id=%s", (edited_page,)
        )
    )["n"] > edited_before
    # 旧版本 chunk 的事实仍在：首次发布的证据链引用旧 chunk，回滚能力优先保留
    assert (
        await store.one("SELECT COUNT(*) n FROM wiki_facts WHERE chunk_id=%s", (old_chunk["id"],))
    )["n"] == 1
    assert (await store.one("SELECT COUNT(*) n FROM wiki_dirty_pages"))["n"] == 0


async def test_deleted_message_drops_page_and_publishes_smaller_snapshot(store):
    """删除：tombstone 保留，受影响页面从发布清单剔除，其余页面不受影响。"""
    ch, _, runtime = await build_and_publish(store, texts=("第一条消息", "第二条消息"), stable=True)
    dropped_page = await page_of_message(store, ch["id"], 2)
    kept_page = await page_of_message(store, ch["id"], 1)
    assert dropped_page != kept_page
    slugs = {
        row["id"]: row["slug"]
        for row in await store.query("SELECT id,slug FROM wiki_pages", fetch=True)
    }
    await store.revoke(ch["id"], message_ids=[2], deleted=True)
    tombstone = await store.one(
        "SELECT deleted,body FROM wiki_sources WHERE channel_id=%s AND message_id=2 AND source_type='message'",
        (ch["id"],),
    )
    assert tombstone["deleted"] and tombstone["body"] == ""
    runtime.collector.client = UpdateClient()
    await store.enqueue(ch["id"], "update")
    await runtime.run_job(await store.claim_job())
    # 旧归档不会复活已删除内容
    await runtime.collector.capture(
        await store.channel(ch["id"]), fake_message(2, "扫描得到的旧原文")
    )
    row = await store.one(
        "SELECT deleted FROM wiki_sources WHERE channel_id=%s AND message_id=2 AND source_type='message'",
        (ch["id"],),
    )
    assert row["deleted"]
    # 新发布快照只保留未受影响页面
    publication = (await store.channel(ch["id"]))["published_revision"]
    remaining = await store.query(
        """SELECT pp.page_id FROM wiki_publication_pages pp WHERE pp.publication_id=%s""",
        (publication,),
        fetch=True,
    )
    assert [row["page_id"] for row in remaining] == [kept_page]
    assert await PublicReader(store).page(ch["id"], slugs[dropped_page]) is None
    assert await PublicReader(store).page(ch["id"], slugs[kept_page])


async def test_apply_extraction_cache_hit_is_write_free(store):
    """缓存命中：来源内容与提取结果未变时跳过 facts 删除重建（零写入）。"""
    ch = await channel(store)
    sid = await store.put_source(
        ch["id"],
        "message",
        1,
        "Sakura 支持中文检索。",
        datetime(2020, 1, 1),
        verified=True,
        message_id=1,
    )
    source = await store.one("SELECT * FROM wiki_sources WHERE id=%s", (sid,))
    chunk = next(chunks(sid, source["body"], source["content_hash"]))
    await store.enqueue(ch["id"], "backfill")
    job = await store.claim_job()
    knowledge = Knowledge(store)
    await knowledge.apply_extraction(job, chunk, extraction(chunk))
    assert (await store.one("SELECT applied_key FROM wiki_chunks WHERE id=%s", (chunk["id"],)))[
        "applied_key"
    ]
    # 在既有事实上打标记：若重放仍执行删除重建，标记会丢失
    await store.query(
        "UPDATE wiki_facts SET fact=JSON_SET(fact,'$.marker','kept') WHERE chunk_id=%s",
        (chunk["id"],),
    )
    await knowledge.apply_extraction(job, chunk, extraction(chunk))
    row = await store.one("SELECT fact FROM wiki_facts WHERE chunk_id=%s", (chunk["id"],))
    assert decode(row["fact"]).get("marker") == "kept"


async def test_reconcile_watermark_advances_after_full_enumeration(store, monkeypatch, tmp_path):
    """定期核对：全量枚举完成即推进水位，后续失败不再把调度带回全量扫描。"""
    from unittest.mock import AsyncMock

    monkeypatch.setenv("MESSAGE_SOURCE_DIR", str(tmp_path / "sources"))
    monkeypatch.setenv("VECTOR_DB_PATH", str(tmp_path / "vectors"))
    ch, _, runtime = await build_and_publish(store, stable=True)
    client = UpdateClient()
    runtime.collector.client = client
    # 核对循环阶段失败（如 UserBot 掉线）：水位已推进
    monkeypatch.setattr(
        runtime.collector, "reconcile", AsyncMock(side_effect=ConnectionError("offline"))
    )
    await store.enqueue(ch["id"], "reconcile")
    await runtime.run_job(await store.claim_job())
    row = await store.one("SELECT reconciled_at FROM wiki_channels WHERE id=%s", (ch["id"],))
    assert row["reconciled_at"] is not None
    # 水位新鲜时，日常调度逻辑选择 update 而非 reconcile
    from core.wiki.models import utc as wiki_utc

    stale = not row["reconciled_at"] or wiki_utc(row["reconciled_at"]) - wiki_utc() >= timedelta(
        days=7
    )
    assert not stale
    # 恢复后核对任务可续跑完成，不影响最终状态
    monkeypatch.setattr(
        runtime.collector,
        "reconcile",
        AsyncMock(return_value=None),
    )
    await store.control_job(
        ch["id"],
        (
            await store.one(
                "SELECT id FROM wiki_jobs WHERE kind='reconcile' ORDER BY created_at DESC LIMIT 1"
            )
        )["id"],
        "retry",
        "test",
    )
    await runtime.run_job(await store.claim_job())
    saved = await store.one(
        "SELECT status FROM wiki_jobs WHERE kind='reconcile' ORDER BY created_at DESC LIMIT 1"
    )
    assert saved["status"] == "published"


async def test_generation_resumes_after_model_failure(store):
    """任务中断恢复：失败的批次不丢不重，续跑后知识完整。"""
    ch = await channel(store)
    await store.configure(
        ch["id"], ch["config"].model_copy(update={"publication_mode": "auto"}), "test"
    )
    for index in range(1, 4):
        await store.put_source(
            ch["id"],
            "message",
            index,
            f"第{index}条消息的内容",
            datetime(2020, 1, index),
            verified=True,
            message_id=index,
        )
    flaky = FlakyAI(fail_on=1)
    runtime = WikiRuntime(store, None, None)
    runtime.engine = WikiEngine(store, runtime=lambda _: (flaky, "flaky-model"))
    await store.enqueue(ch["id"], "reindex")
    job = await store.claim_job()
    await runtime.run_job(job)
    saved = await store.one("SELECT status FROM wiki_jobs WHERE id=%s", (job["id"],))
    assert saved["status"] == "partial"
    await store.control_job(ch["id"], job["id"], "retry", "test")
    await runtime.run_job(await store.claim_job())
    saved = await store.one("SELECT status FROM wiki_jobs WHERE id=%s", (job["id"],))
    assert saved["status"] == "published"
    # 每个来源恰好应用一次：无重复事实，两个词条各一个修订
    assert (await store.one("SELECT COUNT(*) n FROM wiki_facts"))["n"] == 3
    assert (await store.one("SELECT COUNT(*) n FROM wiki_page_revisions"))["n"] == 2


async def test_image_description_update_rebuilds_knowledge(store):
    """图片 OCR/描述更新：对应 image 来源失效并局部重建。"""
    ch = await channel(store)
    await store.configure(
        ch["id"], ch["config"].model_copy(update={"publication_mode": "auto"}), "test"
    )
    await store.put_source(
        ch["id"],
        "message",
        1,
        "图文",
        datetime(2020, 1, 1),
        verified=True,
        message_id=1,
        media_key="photo:1",
        category="image",
    )
    await store.put_source(
        ch["id"],
        "image",
        1,
        "识别文字 A",
        datetime(2020, 1, 1),
        verified=True,
        message_id=1,
        media_key="photo:1",
        category="image",
    )
    fake = FakeAI(stable=True)
    runtime = WikiRuntime(store, None, None)
    runtime.engine = WikiEngine(store, runtime=lambda _: (fake, "fixture-model"))
    await store.enqueue(ch["id"], "reindex")
    await runtime.run_job(await store.claim_job())
    first = (await store.channel(ch["id"]))["published_revision"]
    # OCR 结果更新：image 来源内容变化
    await store.put_source(
        ch["id"],
        "image",
        1,
        "识别文字 B（重新识别）",
        datetime(2020, 1, 1),
        edited_at=datetime(2021, 1, 1),
        verified=True,
        message_id=1,
        media_key="photo:1",
        category="image",
    )
    runtime.collector.client = UpdateClient()
    charged = fake.calls
    await store.enqueue(ch["id"], "update")
    await runtime.run_job(await store.claim_job())
    progress = decode(
        (
            await store.one(
                "SELECT progress FROM wiki_jobs WHERE kind='update' ORDER BY created_at DESC LIMIT 1"
            )
        )["progress"]
    )
    assert progress["processed_chunks"] == 1  # 只有 image 来源需要重新提取
    assert fake.calls == charged + 2
    fact = await store.one(
        """SELECT f.fact FROM wiki_facts f JOIN wiki_chunks ch2 ON ch2.id=f.chunk_id
        JOIN wiki_sources s ON s.id=ch2.source_id
        WHERE s.channel_id=%s AND s.source_type='image' AND s.message_id=1
        AND s.content_hash=ch2.content_hash""",
        (ch["id"],),
    )
    assert "B" in decode(fact["fact"])["text"]
    assert (await store.channel(ch["id"]))["published_revision"] != first


async def test_prune_keeps_evidence_referenced_chunks(store):
    """清理保护：无引用的旧版本 chunk 被删除；发布证据引用的必须保留。"""
    ch = await channel(store)
    sid = await store.put_source(
        ch["id"],
        "message",
        1,
        "初始内容",
        datetime(2020, 1, 1),
        verified=True,
        message_id=1,
    )
    await store.enqueue(ch["id"], "backfill")
    job = await store.claim_job()
    knowledge = Knowledge(store)
    source = await store.one("SELECT * FROM wiki_sources WHERE id=%s", (sid,))
    chunk_v1 = next(chunks(sid, source["body"], source["content_hash"]))
    await knowledge.apply_extraction(job, chunk_v1, extraction(chunk_v1))
    # 编辑产生新版本并应用：未发布的旧版本无任何引用
    await store.put_source(
        ch["id"],
        "message",
        1,
        "编辑后的内容",
        datetime(2020, 1, 1),
        edited_at=datetime(2021, 1, 1),
        verified=True,
        message_id=1,
    )
    source = await store.one("SELECT * FROM wiki_sources WHERE id=%s", (sid,))
    chunk_v2 = next(chunks(sid, source["body"], source["content_hash"]))
    await knowledge.apply_extraction(job, chunk_v2, extraction(chunk_v2))
    await knowledge.prune(ch["id"], 20)
    assert not await store.one("SELECT id FROM wiki_chunks WHERE id=%s", (chunk_v1["id"],))
    assert await store.one("SELECT id FROM wiki_chunks WHERE id=%s", (chunk_v2["id"],))
    # 发布后证据链引用 v2：再次编辑清理时 v2 必须保留（回滚能力）
    page = await store.one("SELECT * FROM wiki_pages WHERE channel_id=%s", (ch["id"],))
    revision = await knowledge.build_page(job, page)
    job["cursor"]["revisions"] = {page["id"]: revision}
    fresh = await store.channel(ch["id"])
    job["progress"].update(
        generation_complete=True, pages=1, source_generation=fresh["source_generation"]
    )
    await WikiEngine(store, runtime=lambda _: (FakeAI(), "fixture-model")).review(job, fresh)
    await store.finish(job)
    await knowledge.publish(ch["id"], job["id"], "test")
    await store.put_source(
        ch["id"],
        "message",
        1,
        "再次编辑的内容",
        datetime(2020, 1, 1),
        edited_at=datetime(2022, 1, 1),
        verified=True,
        message_id=1,
    )
    source = await store.one("SELECT * FROM wiki_sources WHERE id=%s", (sid,))
    chunk_v3 = next(chunks(sid, source["body"], source["content_hash"]))
    # 终态任务的租约已释放：v3 的应用需要一个新的运行中任务
    await store.enqueue(ch["id"], "backfill")
    job2 = await store.claim_job()
    await knowledge.apply_extraction(job2, chunk_v3, extraction(chunk_v3))
    removed = await knowledge.prune(ch["id"], 20)
    assert removed["chunks"] == 0  # v2 仍被发布证据引用
    assert await store.one("SELECT id FROM wiki_chunks WHERE id=%s", (chunk_v2["id"],))
