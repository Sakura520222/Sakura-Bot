# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot

"""Source integrity and anonymous/admin routing contracts."""

import asyncio
import re
import time
from datetime import UTC, datetime, timedelta
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import AsyncMock

import pytest
from fastapi import FastAPI
from fastapi.testclient import TestClient

from core.web_api.auth import WebUIUser
from core.web_api.middleware import AuthMiddleware, _wiki_buckets
from core.web_api.routes import wiki
from core.wiki.avatars import STALE_AFTER_SECONDS, WikiAvatarService, sniff_media_type
from core.wiki.knowledge import merge_facts
from core.wiki.models import (
    Extraction,
    WikiConfig,
    alias_key,
    canonical_id,
    chunks,
    digest,
    validate_extraction,
)
from core.wiki.runtime import schedule_due
from core.wiki.schema import CHANNEL_VISIBLE, REVISION_VISIBLE
from core.wiki.store import WikiSchemaError, WikiStore


def extraction(chunk, text="项目支持中文检索"):
    quote = chunk["text"][:20]
    return Extraction.model_validate(
        {
            "facts": [
                {
                    "entity": "Sakura",
                    "kind": "Entity",
                    "topic": "搜索",
                    "key": "检索",
                    "text": text,
                    "confidence": 0.9,
                    "evidence": [
                        {
                            "source_id": chunk["source_id"],
                            "chunk_id": chunk["id"],
                            "quote": quote,
                            "start": chunk["start"],
                            "end": chunk["start"] + len(quote),
                        }
                    ],
                }
            ],
            "relations": [],
        }
    )


def test_identity_and_complete_chunk_coverage():
    assert canonical_id(-100123456) == "123456"
    assert alias_key("https://t.me/Sakura_Test/") == alias_key("@SAKURA_TEST")
    with pytest.raises(ValueError):
        canonical_id("../../private")
    body = "正文🙂\n" * 4000 + "最后一条重要事实"
    parts = list(chunks(digest("source"), body, digest(body)))
    reconstructed = "".join(
        part["text"] if i == 0 else part["text"][300:] for i, part in enumerate(parts)
    )
    assert reconstructed == body
    assert parts[-1]["end"] == len(body)
    assert parts == list(chunks(digest("source"), body, digest(body)))


@pytest.mark.parametrize("change", ["wrong_source", "wrong_chunk", "outside", "wrong_quote"])
def test_citation_ids_are_not_trusted(change):
    chunk = next(chunks(digest("source"), "Sakura 项目支持中文检索。", digest("body")))
    result = extraction(chunk)
    evidence = result.facts[0].evidence[0]
    if change == "wrong_source":
        evidence.source_id = digest("other")
    if change == "wrong_chunk":
        evidence.chunk_id = digest("other")
    if change == "outside":
        evidence.end += 1
    if change == "wrong_quote":
        evidence.quote = "伪造原文"
    with pytest.raises(ValueError):
        validate_extraction(result, chunk)


def test_relation_endpoints_resolve_without_local_fact_assertions():
    # 关系端点与其边在同一事务内解析/创建，实体不必在本 chunk 另有事实断言；
    # 但关系自带引用的引文仍按同一规则校验
    chunk = next(chunks(digest("source"), "Sakura 项目支持中文检索。", digest("body")))
    result = extraction(chunk)
    from core.wiki.models import Relation

    result.relations.append(
        Relation(
            source="Sakura",
            target="missing",
            kind="supports",
            evidence=[result.facts[0].evidence[0]],
            confidence=0.9,
        )
    )
    assert validate_extraction(result, chunk) is result

    forged = extraction(chunk)
    forged.relations.append(
        Relation(
            source="Sakura",
            target="missing",
            kind="supports",
            evidence=[forged.facts[0].evidence[0].model_copy(update={"quote": "伪造原文"})],
            confidence=0.9,
        )
    )
    with pytest.raises(ValueError):
        validate_extraction(forged, chunk)


def test_fact_dedup_conflicts_and_expiry_are_explicit():
    chunk = next(chunks(digest("source"), "可引用的真实原文", digest("body")))
    original = extraction(chunk).facts[0].model_dump(mode="json")
    original["source_created_at"] = "2020-01-01T00:00:00"
    changed = {**original, "text": "项目停止中文检索"}
    facts = merge_facts([original, original, changed])
    assert len(facts) == 2
    assert all(f["status"] == "disputed" for f in facts)
    assert len(facts[0]["evidence"]) == 1
    expired = {**original, "valid_until": "2020-02-01T00:00:00"}
    assert merge_facts([expired])[0]["status"] == "superseded"


def test_configuration_defaults_and_schedule_validation():
    config = WikiConfig()
    assert not config.enabled and not config.public and config.frequency == "manual"
    with pytest.raises(ValueError):
        WikiConfig(public=True)
    with pytest.raises(ValueError):
        WikiConfig(cron="not cron")
    with pytest.raises(ValueError):
        WikiConfig(public_base_url="https://user:secret@example.com")
    now = datetime(2026, 11, 5, 4, tzinfo=UTC)
    assert not schedule_due(config, None, now)
    assert schedule_due(
        WikiConfig(frequency="daily", timezone="UTC"),
        (now - timedelta(days=1)).replace(tzinfo=None),
        now,
    )
    assert not schedule_due(
        WikiConfig(frequency="daily", timezone="UTC"), now.replace(tzinfo=None), now
    )


def test_python_go_visibility_predicates_match():
    source = (Path(__file__).parents[2] / "qa-bot-go/internal/db/wiki.go").read_text(
        encoding="utf-8"
    )
    for name, expected in [
        ("wikiChannelVisible", CHANNEL_VISIBLE),
        ("wikiRevisionVisible", REVISION_VISIBLE),
    ]:
        actual = re.search(r"const " + name + r" = `([^`]+)`", source).group(1)
        assert " ".join(actual.split()) == " ".join(expected.split())


@pytest.fixture
def api(monkeypatch):
    _wiki_buckets.clear()
    reader = SimpleNamespace(
        channels=AsyncMock(return_value=[]),
        channel=AsyncMock(return_value=None),
        page=AsyncMock(return_value=None),
        search=AsyncMock(return_value={"items": [], "total": 0}),
    )
    monkeypatch.setattr(
        wiki, "get_wiki_runtime", lambda: SimpleNamespace(ready=True, reader=reader)
    )
    app = FastAPI()
    app.add_middleware(AuthMiddleware)
    app.include_router(wiki.public, prefix="/api/public/wiki")
    app.include_router(wiki.admin, prefix="/api/wiki/admin")
    return TestClient(app)


def test_public_reads_without_token_revalidate_cached_responses(api):
    for method in ("GET", "HEAD"):
        response = api.request(method, "/api/public/wiki/channels")
        assert response.status_code == 200
        assert response.headers["cache-control"] == "private, no-cache, must-revalidate"
        again = api.request(
            method, "/api/public/wiki/channels", headers={"If-None-Match": response.headers["etag"]}
        )
        assert again.status_code == 304 and not again.content
    assert api.get("/api/public/wiki/channels/private/pages/hidden").status_code == 404


def test_avatar_route_regex_contract():
    assert wiki.is_public_wiki_request("/api/public/wiki/channels/123/avatar", "GET")
    assert wiki.is_public_wiki_request("/api/public/wiki/channels/123/avatar", "HEAD")
    assert wiki.WIKI_AVATAR_ROUTE.fullmatch("/api/public/wiki/channels/123/avatar")
    for method, path in [
        ("POST", "/api/public/wiki/channels/123/avatar"),
        ("GET", "/api/public/wiki/channels/123/avatar/x"),
        ("GET", "/api/public/wiki/channels/123/publish"),
        ("GET", "/api/public/wiki/channels/123/../admin/config"),
    ]:
        assert not wiki.is_public_wiki_request(path, method)


def test_avatar_sniffing_rejects_unknown_magic_bytes():
    assert sniff_media_type(b"\xff\xd8\xff\xe0rest") == "image/jpeg"
    assert sniff_media_type(b"\x89PNG\r\n\x1a\n") == "image/png"
    assert sniff_media_type(b"GIF89a") is None
    assert sniff_media_type(b"") is None


async def test_avatar_stale_disk_cache_survives_background_refresh_failure(tmp_path):
    """回归：stale 再校验调度曾因容器类型错误抛 AttributeError，被 get() 兜底
    except 吞掉后把完好的磁盘缓存折叠成 404 并反复写负缓存；修复后必须始终
    返回磁盘缓存，后台刷新失败仅记录。"""
    payload = bytes([0xFF, 0xD8, 0xFF, 0xE0]) + b"jpeg-bytes"
    (tmp_path / "42.jpg").write_bytes(payload)
    service = WikiAvatarService(None, lambda: (None, None), root=str(tmp_path))
    service._index["123"] = 42
    service._refreshed_at["123"] = time.monotonic() - STALE_AFTER_SECONDS - 1

    first = await service.get("123")
    assert first == (payload, "image/jpeg", 42)
    await asyncio.sleep(0)  # 排空后台刷新任务（client 不可用会失败并记录，不影响缓存）
    assert "123" not in service._revalidating

    second = await service.get("123")
    assert second == (payload, "image/jpeg", 42)


def test_avatar_conditional_request_returns_304(api, monkeypatch):
    payload = b"\xff\xd8\xff\xe0" + b"jpeg" * 8
    service = SimpleNamespace(
        ready=True,
        reader=SimpleNamespace(channels=AsyncMock(return_value=[])),
        store=SimpleNamespace(
            channel=AsyncMock(return_value={"id": "123"}),
            one=AsyncMock(return_value={"id": "123"}),
        ),
        avatar=SimpleNamespace(get=AsyncMock(return_value=(payload, "image/jpeg", 42))),
    )
    monkeypatch.setattr(wiki, "get_wiki_runtime", lambda: service)
    first = api.get("/api/public/wiki/channels/123/avatar")
    assert first.status_code == 200
    assert first.headers["etag"] == '"av-42"'
    second = api.get(
        "/api/public/wiki/channels/123/avatar",
        headers={"If-None-Match": '"av-42"'},
    )
    assert second.status_code == 304
    assert second.headers["cache-control"] == "private, no-cache, must-revalidate"
    assert second.headers["etag"] == '"av-42"'
    assert second.content == b""
    # 304 免去响应体传输；get() 仍按请求执行（磁盘缓存读取，代价可忽略）
    assert service.avatar.get.await_count == 2
    service.avatar.get.assert_awaited_with("123")


def test_avatar_response_carries_public_cache_headers(api, monkeypatch):
    payload = b"\xff\xd8\xff\xe0" + b"jpeg" * 8
    service = SimpleNamespace(
        ready=True,
        reader=SimpleNamespace(channels=AsyncMock(return_value=[])),
        store=SimpleNamespace(
            channel=AsyncMock(return_value={"id": "123"}),
            one=AsyncMock(return_value={"id": "123"}),
        ),
        avatar=SimpleNamespace(get=AsyncMock(return_value=(payload, "image/jpeg", 42))),
    )
    monkeypatch.setattr(wiki, "get_wiki_runtime", lambda: service)
    response = api.get("/api/public/wiki/channels/123/avatar")
    assert response.status_code == 200
    assert response.headers["cache-control"] == "private, no-cache, must-revalidate"
    assert response.headers["etag"] == '"av-42"'
    assert response.headers["content-type"] == "image/jpeg"
    assert response.content == payload
    service.avatar.get.assert_awaited_once_with("123")
    assert "no-cache" in api.get("/api/public/wiki/channels").headers["cache-control"]


def test_public_article_rechecks_visibility_before_returning_304(api, monkeypatch):
    reader = SimpleNamespace(page=AsyncMock(return_value={"title": "公开文章"}))
    monkeypatch.setattr(
        wiki, "get_wiki_runtime", lambda: SimpleNamespace(ready=True, reader=reader)
    )
    path = "/api/public/wiki/channels/123/pages/article"
    first = api.get(path)
    validators = {"If-None-Match": first.headers["etag"]}
    assert api.get(path, headers=validators).status_code == 304
    reader.page.return_value = None
    denied = api.get(path, headers=validators)
    assert denied.status_code == 404
    assert denied.headers["cache-control"] == "no-store"
    assert "etag" not in denied.headers


def test_avatar_failure_keeps_no_store(api, monkeypatch):
    """缓存白名单只对 200 生效：404/503 的头像响应必须保持 no-store。"""
    service = SimpleNamespace(
        ready=True,
        reader=SimpleNamespace(channels=AsyncMock(return_value=[])),
        store=SimpleNamespace(
            channel=AsyncMock(return_value={"id": "123"}),
            one=AsyncMock(return_value=None),  # 可见性门禁不通过
        ),
        avatar=SimpleNamespace(get=AsyncMock(return_value=(b"\xff\xd8\xff", "image/jpeg", 42))),
    )
    monkeypatch.setattr(wiki, "get_wiki_runtime", lambda: service)
    response = api.get("/api/public/wiki/channels/123/avatar")
    assert response.status_code == 404
    assert response.headers["cache-control"] == "no-store"

    invisible = SimpleNamespace(
        ready=True,
        reader=SimpleNamespace(channels=AsyncMock(return_value=[])),
        store=SimpleNamespace(
            channel=AsyncMock(return_value={"id": "123"}),
            one=AsyncMock(return_value={"id": "123"}),
        ),
        avatar=SimpleNamespace(get=AsyncMock(return_value=None)),
    )
    monkeypatch.setattr(wiki, "get_wiki_runtime", lambda: invisible)
    response = api.get("/api/public/wiki/channels/123/avatar")
    assert response.status_code == 404
    assert response.headers["cache-control"] == "no-store"


def test_avatar_missing_or_invisible_channel_is_404(api, monkeypatch):
    store = SimpleNamespace(
        channel=AsyncMock(return_value={"id": "123"}),
        one=AsyncMock(return_value={"id": "123"}),
    )

    def runtime_with(avatar):
        return SimpleNamespace(ready=True, store=store, avatar=avatar)

    avatar = SimpleNamespace(get=AsyncMock(return_value=None))
    monkeypatch.setattr(wiki, "get_wiki_runtime", lambda: runtime_with(avatar))
    # 头像服务返回 None：仍以 404 表达，绝不向匿名调用方暴露内部差异。
    assert api.get("/api/public/wiki/channels/123/avatar").status_code == 404
    store.one = AsyncMock(return_value=None)  # CHANNEL_VISIBLE 门禁未通过
    assert api.get("/api/public/wiki/channels/123/avatar").status_code == 404
    store.channel = AsyncMock(return_value=None)  # 频道不存在
    assert api.get("/api/public/wiki/channels/123/avatar").status_code == 404
    # 头像服务未装配（avatar 属性缺失）同样是 404。
    monkeypatch.setattr(wiki, "get_wiki_runtime", lambda: SimpleNamespace(ready=True, store=store))
    store.channel = AsyncMock(return_value={"id": "123"})
    store.one = AsyncMock(return_value={"id": "123"})
    assert api.get("/api/public/wiki/channels/123/avatar").status_code == 404


@pytest.mark.parametrize("method", ["GET", "POST", "PUT", "PATCH", "DELETE", "HEAD"])
@pytest.mark.parametrize(
    "path",
    [
        "/api/wiki/admin/channels",
        "/api/wiki/admin/channels/123/config",
        "/api/wiki/admin/channels/123/publish",
        "/api/public/wiki/admin/channels",
        "/api/public/wiki/channels/123/publish",
        "/api/public/wiki/channels/123/avatar/x",
        "/api/public/wiki/channels/123/../admin/config",
    ],
)
def test_no_anonymous_admin_or_prefix_bypass(api, method, path):
    assert api.request(method, path).status_code == 401


@pytest.mark.parametrize("method", ["POST", "PUT", "PATCH", "DELETE"])
@pytest.mark.parametrize(
    "path",
    [
        "/api/public/wiki/channels",
        "/api/public/wiki/channels/123/avatar",
        "/api/public/wiki/channels/123/cover",
    ],
)
def test_public_read_paths_never_authorize_writes(api, method, path):
    assert api.request(method, path).status_code == 401


def test_signed_non_admin_denied(api, monkeypatch):
    monkeypatch.setattr(
        "core.web_api.middleware.verify_token", lambda _: WebUIUser(user_id="not-admin")
    )
    assert (
        api.get("/api/wiki/admin/channels", headers={"Authorization": "Bearer signed"}).status_code
        == 403
    )
    assert (
        api.put(
            "/api/wiki/admin/channels/123/cover", headers={"Authorization": "Bearer signed"}
        ).status_code
        == 403
    )


def test_upload_cover_rejects_anonymous_invalid_and_oversized_bodies(api, monkeypatch):
    path = "/api/wiki/admin/channels/123/cover"
    assert api.put(path, content=b"image").status_code == 401
    assert api.delete(path).status_code == 401
    store = SimpleNamespace(
        channel=AsyncMock(return_value={"id": "123"}),
        set_cover=AsyncMock(return_value=True),
    )
    monkeypatch.setattr(wiki, "get_wiki_runtime", lambda: SimpleNamespace(ready=True, store=store))
    monkeypatch.setattr(
        "core.web_api.middleware.verify_token", lambda _: WebUIUser(user_id="admin")
    )
    monkeypatch.setattr(wiki, "MAX_IMAGE_BYTES", 12)
    headers = {"Authorization": "Bearer test"}
    invalid = api.put(path, content=b"not an image", headers=headers)
    assert invalid.status_code == 422
    assert invalid.headers["cache-control"] == "no-store"
    assert api.put(path, content=b"x" * 13, headers=headers).status_code == 413
    store.set_cover.assert_not_called()


def test_bad_graph_bounds_are_rejected_before_db(api):
    assert api.get("/api/public/wiki/channels/123/graph?limit=99999").status_code == 422
    assert api.get("/api/public/wiki/channels/123/graph?depth=9").status_code == 422
    assert api.get("/api/public/wiki/channels/123/graph?node_id=../../private").status_code == 422


def test_public_rate_limit_has_bounded_window(api):
    for _ in range(120):
        assert api.get("/api/public/wiki/channels").status_code == 200
    response = api.get("/api/public/wiki/channels")
    assert response.status_code == 429
    assert response.headers["retry-after"] == "60"


async def test_schema_failure_names_table_and_does_not_mark_initialization_complete():
    import aiomysql

    store = WikiStore(SimpleNamespace(pool=None))
    store.one = AsyncMock(return_value=None)
    executed = []

    async def query(sql, *args, **kwargs):
        executed.append(sql)
        if "CREATE TABLE IF NOT EXISTS wiki_entity_aliases" in sql:
            raise aiomysql.ProgrammingError(1064, "syntax failure near reserved identifier")

    store.query = query
    with pytest.raises(WikiSchemaError, match="wiki_entity_aliases.*1064"):
        await store.initialize()
    assert not any("INSERT IGNORE INTO wiki_schema" in sql for sql in executed)


def test_admin_sees_startup_failure_without_false_channel_empty_state(api, monkeypatch):
    service = SimpleNamespace(
        ready=False, startup_error="Wiki 建表失败：wiki_entity_aliases（MySQL 1064）"
    )
    monkeypatch.setattr(wiki, "get_wiki_runtime", lambda: service)
    monkeypatch.setattr(
        "core.web_api.middleware.verify_token", lambda _: WebUIUser(user_id="admin")
    )
    response = api.get("/api/wiki/admin/channels", headers={"Authorization": "Bearer test"})
    assert response.status_code == 503
    assert "wiki_entity_aliases" in response.json()["detail"]
    assert "1064" in response.json()["detail"]
    assert api.get("/api/public/wiki/channels").json()["detail"] == "Wiki 服务尚未启动"
