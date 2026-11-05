# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot

"""Source integrity and anonymous/admin routing contracts."""

import re
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


@pytest.mark.parametrize(
    "change", ["wrong_source", "wrong_chunk", "outside", "wrong_quote", "unknown_entity"]
)
def test_citation_ids_and_graph_entities_are_not_trusted(change):
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
    if change == "unknown_entity":
        from core.wiki.models import Relation

        result.relations.append(
            Relation(
                source="Sakura",
                target="missing",
                kind="supports",
                evidence=[evidence],
                confidence=0.9,
            )
        )
    with pytest.raises(ValueError):
        validate_extraction(result, chunk)


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


def test_public_reads_without_token_and_no_cache(api):
    for method in ("GET", "HEAD"):
        response = api.request(method, "/api/public/wiki/channels")
        assert response.status_code == 200
        assert response.headers["cache-control"] == "no-store"
    assert api.get("/api/public/wiki/channels/private/pages/hidden").status_code == 404


@pytest.mark.parametrize("method", ["GET", "POST", "PUT", "PATCH", "DELETE", "HEAD"])
@pytest.mark.parametrize(
    "path",
    [
        "/api/wiki/admin/channels",
        "/api/wiki/admin/channels/123/config",
        "/api/wiki/admin/channels/123/publish",
        "/api/public/wiki/admin/channels",
        "/api/public/wiki/channels/123/publish",
        "/api/public/wiki/channels/123/../admin/config",
    ],
)
def test_no_anonymous_admin_or_prefix_bypass(api, method, path):
    assert api.request(method, path).status_code == 401


@pytest.mark.parametrize("method", ["POST", "PUT", "PATCH", "DELETE"])
def test_public_read_paths_never_authorize_writes(api, method):
    assert api.request(method, "/api/public/wiki/channels").status_code == 401


def test_signed_non_admin_denied(api, monkeypatch):
    monkeypatch.setattr(
        "core.web_api.middleware.verify_token", lambda _: WebUIUser(user_id="not-admin")
    )
    assert (
        api.get("/api/wiki/admin/channels", headers={"Authorization": "Bearer signed"}).status_code
        == 403
    )


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
