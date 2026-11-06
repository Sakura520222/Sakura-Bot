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
from pathlib import Path
from types import SimpleNamespace

import aiomysql
import pytest
import pytest_asyncio

from core.wiki.collector import SourceCollector
from core.wiki.engine import WikiEngine
from core.wiki.knowledge import Knowledge
from core.wiki.models import WikiConfig, chunks, digest, name_key
from core.wiki.reader import PublicReader
from core.wiki.runtime import WikiRuntime
from core.wiki.store import LeaseLost, WikiStore, decode
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


async def published(store, *, image=False):
    ch = await channel(store)
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
        generation_complete=True, review_complete=True, source_generation=fresh["source_generation"]
    )
    await store.checkpoint(job, "ready")
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
    published_job = await store.one("SELECT * FROM wiki_jobs WHERE channel_id=%s", (ch["id"],))
    assert published_job["status"] == "published" and decode(published_job["progress"])["published"]
    assert await Knowledge(store).publish(ch["id"], published_job["id"], "retry") == publication
    await store.revoke(ch["id"], source_id=source["id"], actor="test")
    assert await reader.page(ch["id"], page["slug"]) is None
    assert not (await reader.search("中文"))["items"]
    with pytest.raises(ValueError, match="revoked"):
        await Knowledge(store).publish(ch["id"], None, "test", rollback=publication)
    assert (await store.channel(ch["id"]))["published_revision"] == publication


async def test_page_additive_counts_match_true_totals(store):
    ch, _, page, _, _ = await published(store)
    article = await PublicReader(store).page(ch["id"], page["slug"])
    # 夹具只有一条来源与一次发布：500/50 截断不会触发，但总量键必须与真实值一致
    assert article["sources_total"] == len(article["sources"]) == 1
    assert article["history_total"] == len(article["history"]) == 1


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
    """按 wiki-2 markdown 管线契约返回：分批 Markdown、主题行、纯文本简介。

    1aafed9 起引擎不再发送 JSON schema / 独立审核请求，直接按负载形状分发。
    """

    def __init__(self, *, incomplete=False):
        self.calls = 0
        self.incomplete = incomplete
        self.chat = SimpleNamespace(completions=self)
        self.base_url = "https://fixture.invalid"

    def with_options(self, **_):
        return self

    async def create(self, **request):
        self.calls += 1
        payload = json.loads(request["messages"][1]["content"])
        if "summaries" in payload:
            content = "中文检索\nSakura"
        elif "sources" in payload or "source" in payload:
            sources = payload.get("sources") or [{"source": 0, "text": payload["source"]}]
            sections = []
            for item in sources:
                index, text = item["source"], item["text"]
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
        return SimpleNamespace(
            choices=[
                SimpleNamespace(
                    finish_reason="length" if self.incomplete else "stop",
                    message=SimpleNamespace(content=content),
                )
            ],
            usage=SimpleNamespace(prompt_tokens=100, completion_tokens=100),
        )


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
    runtime = WikiRuntime(store, None, None)
    runtime.engine = WikiEngine(store, runtime=lambda _: (fake, "fixture-model"))
    first_id = await store.enqueue(ch["id"], "reindex")
    await runtime.run_job(await store.claim_job())
    first = await store.one("SELECT * FROM wiki_jobs WHERE id=%s", (first_id,))
    assert first["status"] == "published", first
    assert fake.calls == 3  # 两个提取批次 + 一次频道简介；独立审核已随管线移除
    publication = (await store.channel(ch["id"]))["published_revision"]
    graph = await runtime.reader.graph(ch["id"], limit=3)
    assert len(graph["nodes"]) == 2 and graph["edges"]
    assert all(edge["evidence_count"] > 0 for edge in graph["edges"])
    timeline = await runtime.reader.timeline(ch["id"], page_size=1)
    assert len(timeline["items"]) == 1 and timeline["total_pages"] >= 2
    charged = fake.calls
    await store.enqueue(ch["id"], "reindex")
    await runtime.run_job(await store.claim_job())
    assert fake.calls == charged
    assert (await store.channel(ch["id"]))["published_revision"] != publication
    # Current visibility is always checked even after a successful prior graph request.
    await store.revoke(ch["id"], message_ids=[1], deleted=True)
    assert not (await runtime.reader.graph(ch["id"]))["nodes"]


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
    # 审核门已随 markdown 管线移除（review_complete 恒为 True）；非 auto 发布模式下仍不发布
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
