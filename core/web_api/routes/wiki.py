# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot

"""Read-only anonymous Wiki endpoints and separately authorized administration."""

import asyncio
import difflib
import re
from datetime import datetime
from hashlib import sha256
from typing import Annotated, Literal

from fastapi import APIRouter, Depends, HTTPException, Query, Request, Response
from fastapi.responses import HTMLResponse
from fastapi.routing import APIRoute
from pydantic import Field

from core.media.image_validation import MAX_IMAGE_BYTES, ImageInputError, validate_image
from core.wiki.avatars import sniff_media_type
from core.wiki.cursor import InvalidCursor
from core.wiki.models import Strict, WikiConfig
from core.wiki.reader import revision_changes
from core.wiki.runtime import get_wiki_runtime
from core.wiki.schema import CHANNEL_VISIBLE
from core.wiki.store import decode, encode

CHANNEL = r"[A-Za-z0-9_-]{1,64}"
PUBLIC_WIKI_ROUTE = re.compile(
    rf"/api/public/wiki/(?:channels|visibility|feed|search|sitemap|bookmarks|channels/{CHANNEL}"
    r"(?:/(?:profile|timeline|graph|changes|avatar|cover|pages/[A-Za-z0-9_-]{1,180}(?:/compare)?))?)"
)
# avatar 是固定字面量；publish 等动态段与 ../admin/config 探测都不会命中。
WIKI_AVATAR_ROUTE = re.compile(rf"/api/public/wiki/channels/{CHANNEL}/avatar")


def is_public_wiki_request(path, method):
    return method in {"GET", "HEAD"} and (
        PUBLIC_WIKI_ROUTE.fullmatch(path) is not None
        or re.fullmatch(rf"/wiki-preview/{CHANNEL}(?:/[A-Za-z0-9_-]{{1,180}})?", path) is not None
    )


def runtime(*, admin=False):
    service = get_wiki_runtime()
    if not service or not service.ready:
        detail = getattr(service, "startup_error", "") if admin else ""
        raise HTTPException(503, detail or "Wiki 服务尚未启动")
    return service


def administrator(request: Request):
    import core.config as config_module

    user = getattr(request.state, "user", None)
    if not user:
        raise HTTPException(401, "需要管理员登录")
    if not (
        user.is_dev
        or user.user_id == "admin"
        or str(user.user_id) in {str(uid) for uid in config_module.ADMIN_LIST}
    ):
        raise HTTPException(403, "需要管理员权限")
    return user.user_id


class PublicReadRoute(APIRoute):
    def get_route_handler(self):
        handler = super().get_route_handler()

        async def conditional(request):
            try:
                response = await handler(request)
            except InvalidCursor as error:
                raise HTTPException(400, str(error)) from error
            if response.status_code != 200 or not response.headers.get(
                "Content-Type", ""
            ).startswith("application/json"):
                return response
            # Compute the validator only after the current read passed visibility checks.
            # A browser can save bytes on revalidation, including after policy changes.
            tag = f'W/"{sha256(response.body).hexdigest()}"'
            headers = {"Cache-Control": "private, no-cache, must-revalidate", "ETag": tag}
            validators = request.headers.get("If-None-Match", "").split(",")
            if any(value.strip().removeprefix("W/") in {"*", tag[2:]} for value in validators):
                return Response(status_code=304, headers=headers)
            response.headers.update(headers)
            return response

        return conditional


public = APIRouter(route_class=PublicReadRoute)
ADMIN_AUTH = Depends(administrator)
admin = APIRouter(dependencies=[ADMIN_AUTH])


def found(value):
    if value is None:
        raise HTTPException(404, "Wiki 内容不存在或尚未公开")
    return value


@public.api_route("/channels", methods=["GET", "HEAD"])
async def channels():
    return await runtime().reader.channels()


@public.api_route("/visibility", methods=["GET", "HEAD"])
async def visibility():
    return await runtime().reader.visibility()


@public.api_route("/bookmarks", methods=["GET", "HEAD"])
async def bookmarks(
    id: Annotated[
        list[Annotated[str, Field(pattern=r"^[a-f0-9]{32}$")]], Query(min_length=1, max_length=50)
    ],
):
    return {"items": await runtime().reader.bookmarks(id)}


@public.api_route("/channels/{channel}", methods=["GET", "HEAD"])
async def channel_home(channel: str, summary: bool = False):
    reader = runtime().reader
    return found(
        await (reader.channel(channel, summary=True) if summary else reader.channel(channel))
    )


@public.api_route("/channels/{channel}/pages/{slug}", methods=["GET", "HEAD"])
async def page(
    channel: str,
    slug: str,
    part: Literal["full", "article", "references", "related"] = "full",
    revision: str | None = Query(default=None, pattern=r"^[a-f0-9]{32}$"),
):
    reader = runtime().reader
    if part == "related":
        return found(await reader.related(channel, slug))
    if revision:
        return found(await reader.page(channel, slug, part=part, revision=revision))
    return found(
        await (
            reader.page(channel, slug) if part == "full" else reader.page(channel, slug, part=part)
        )
    )


@public.api_route("/channels/{channel}/pages/{slug}/compare", methods=["GET", "HEAD"])
async def compare_page_revisions(
    channel: str,
    slug: str,
    before: str = Query(pattern=r"^[a-f0-9]{32}$"),
    after: str = Query(pattern=r"^[a-f0-9]{32}$"),
):
    return found(await runtime().reader.page(channel, slug, compare=(before, after)))


@public.api_route("/channels/{channel}/avatar", methods=["GET", "HEAD"])
async def avatar(channel: str, request: Request):
    """公开频道头像：可见性门禁 + 磁盘缓存；未就绪/不可见/缺失一律 404。"""
    service = runtime()
    ch = found(await service.store.channel(channel))
    found(
        await service.store.one(
            f"SELECT c.id FROM wiki_channels c WHERE c.id=%s AND {CHANNEL_VISIBLE}", (ch["id"],)
        )
    )
    avatar_service = getattr(service, "avatar", None)
    if not avatar_service:
        raise HTTPException(404, "Wiki 内容不存在或尚未公开")
    result = await avatar_service.get(ch["id"])
    if not result:
        raise HTTPException(404, "Wiki 内容不存在或尚未公开")
    data, media_type, photo_id = result
    etag = f'"av-{photo_id}"'
    # 条件请求：ETag 匹配时返回 304，浏览器在 max-age 过期后不必整包重传；
    # 304 与 200 同样由中间件下发缓存头（RFC 9110 要求缓存语义一致）。
    if request.headers.get("if-none-match") == etag:
        return Response(status_code=304, headers={"ETag": etag})
    # Cache-Control 由中间件统一改写（单一写者）；路由只负责内容与 ETag。
    return Response(content=data, media_type=media_type, headers={"ETag": etag})


@public.api_route("/channels/{channel}/cover", methods=["GET", "HEAD"])
async def cover(channel: str, request: Request, v: str | None = Query(None, max_length=64)):
    store = runtime().store
    ch = found(await store.channel(channel))
    row = found(
        await store.one(
            f"""SELECT cover.digest,cover.image FROM wiki_channels c
            JOIN wiki_channel_covers cover ON cover.channel_id=c.id
            WHERE c.id=%s AND (%s IS NULL OR cover.digest=%s) AND {CHANNEL_VISIBLE}""",
            (ch["id"], v, v),
        )
    )
    media_type = found(sniff_media_type(row["image"]))
    etag = f'"{row["digest"]}"'
    headers = {"ETag": etag, "Cache-Control": "private, no-cache, must-revalidate"}
    if request.headers.get("if-none-match") == etag:
        return Response(status_code=304, headers=headers)
    if request.method == "HEAD":
        headers["Content-Length"] = str(len(row["image"]))
        return Response(media_type=media_type, headers=headers)
    return Response(row["image"], media_type=media_type, headers=headers)


@public.api_route("/feed", methods=["GET", "HEAD"])
async def feed(
    mode: Literal["latest", "recommended", "search"] = "latest",
    q: str = Query("", max_length=200),
    channel: str | None = Query(None, max_length=64),
    kind: str | None = Query(None, max_length=16),
    topic: str | None = Query(None, max_length=160),
    after: datetime | None = None,
    before: datetime | None = None,
    cursor: str | None = Query(None, max_length=512),
    limit: int = Query(20, ge=1, le=50),
):
    return found(
        await runtime().reader.feed(
            mode=mode,
            q=q,
            channel=channel,
            kind=kind,
            topic=topic,
            after=after,
            before=before,
            cursor=cursor,
            limit=limit,
        )
    )


@public.api_route("/search", methods=["GET", "HEAD"])
async def search(
    q: str = Query("", max_length=200),
    channel: str | None = None,
    page: int = Query(1, ge=1, le=10000),
    page_size: int = Query(20, ge=1, le=50),
    kind: str | None = Query(None, max_length=16),
    topic: str | None = Query(None, max_length=160),
    after: datetime | None = None,
    before: datetime | None = None,
):
    return await runtime().reader.search(q, channel, page, page_size, kind, topic, after, before)


@public.api_route("/channels/{channel}/graph", methods=["GET", "HEAD"])
async def graph(
    channel: str,
    node_id: str | None = Query(None, pattern=r"^[a-f0-9]{32}$"),
    depth: int = Query(1, ge=1, le=2),
    limit: int = Query(60, ge=1, le=120),
    kind: str | None = Query(None, max_length=16),
    topic: str | None = Query(None, max_length=160),
    relation: str | None = Query(None, max_length=32),
    q: str = Query("", max_length=200),
    after: datetime | None = None,
    before: datetime | None = None,
    pagination: Literal["legacy", "cursor"] = "legacy",
    cursor: str | None = Query(None, max_length=512),
):
    reader = runtime().reader
    if pagination == "cursor":
        return found(
            await reader.browse_graph(
                channel,
                node_id,
                cursor=cursor,
                limit=limit,
                kind=kind,
                topic=topic,
                relation=relation,
                q=q,
                after=after,
                before=before,
            )
        )
    return found(
        await reader.graph(channel, node_id, depth, limit, kind, topic, relation, q, after, before)
    )


@public.api_route("/channels/{channel}/timeline", methods=["GET", "HEAD"])
async def timeline(
    channel: str,
    page: int = Query(1, ge=1, le=10000),
    page_size: int = Query(20, ge=1, le=50),
    pagination: Literal["page", "cursor"] = "page",
    cursor: str | None = Query(None, max_length=512),
    before: datetime | None = None,
):
    reader = runtime().reader
    if pagination == "cursor":
        return found(
            await reader.timeline(
                channel, page, page_size, cursor_mode=True, cursor=cursor, before=before
            )
        )
    found(await reader.channel(channel))
    return await reader.timeline(channel, page, page_size)


@public.api_route("/channels/{channel}/profile", methods=["GET", "HEAD"])
async def profile(channel: str):
    return found(await runtime().reader.profile(channel))


@public.api_route("/channels/{channel}/changes", methods=["GET", "HEAD"])
async def changes(
    channel: str, page: int = Query(1, ge=1, le=10000), page_size: int = Query(20, ge=1, le=50)
):
    found(await runtime().reader.channel(channel))
    return await runtime().reader.changes(channel, page, page_size)


@public.api_route("/sitemap", methods=["GET", "HEAD"])
async def sitemap(page: int = Query(1, ge=1, le=10000)):
    # Relative URLs intentionally avoid trusting a caller-controlled Host header.
    result = await runtime().reader.search(page=page, page_size=50)
    return {"paths": [p["url"] for p in result["items"]], "page": page, "total": result["total"]}


class JobRequest(Strict):
    kind: Literal["backfill", "update", "reconcile", "reindex", "page", "albums"] = "update"
    page_id: str | None = Field(default=None, pattern=r"^[a-f0-9]{32}$")


class PublishRequest(Strict):
    job_id: str = Field(pattern=r"^[a-f0-9]{32}$")


class RollbackRequest(Strict):
    publication_id: str = Field(pattern=r"^[a-f0-9]{32}$")


class SourceRequest(Strict):
    source_id: str = Field(pattern=r"^[a-f0-9]{64}$")


class PageAction(Strict):
    action: Literal["lock", "unlock", "hide", "show", "rename", "merge", "split"]
    title: str = Field(default="", max_length=160)
    slug: str = Field(default="", pattern=r"^(?:[A-Za-z0-9_-]{1,180})?$")
    alias: str = Field(default="", max_length=160)


class PurgeRequest(Strict):
    confirm_channel: str


class CorrectionRequest(Strict):
    claim: int = Field(ge=0)
    text: str = Field(min_length=1, max_length=4000)


async def managed(channel):
    service = runtime(admin=True)
    result = await service.store.channel(channel)
    if not result:
        raise HTTPException(404, "频道尚未完成身份核实，请检查 UserBot 和频道配置")
    return service, result


@admin.get("/channels")
async def admin_channels():
    service = runtime(admin=True)
    rows = await service.store.query("SELECT id FROM wiki_channels ORDER BY title", fetch=True)
    return [await service.store.channel(r["id"]) for r in rows]


@admin.get("/channels/{channel}/config")
async def config(channel: str):
    _, ch = await managed(channel)
    return ch


EXCLUSION_LABELS = {
    "service": "频道系统事件",
    "generated_summary": "机器人已生成的重复总结",
    "before_history_since": "早于设置的历史起点",
    "message_type": "未选择的消息类型",
    "unavailable": "原消息暂不可读取",
    "": "旧记录，等待重新核对",
}


@admin.get("/channels/{channel}/excluded")
async def excluded_sources(channel: str, page: int = Query(1, ge=1), reason: str | None = None):
    service, ch = await managed(channel)
    where = "channel_id=%s AND source_type='message' AND excluded=TRUE AND deleted=FALSE"
    groups = await service.store.query(
        f"SELECT exclusion_reason,COUNT(*) total FROM wiki_sources WHERE {where} GROUP BY exclusion_reason",
        (ch["id"],),
        fetch=True,
    )
    args = [ch["id"]]
    if reason is not None:
        where += " AND exclusion_reason=%s"
        args.append(reason)
    count = await service.store.one(f"SELECT COUNT(*) total FROM wiki_sources WHERE {where}", args)
    pending = await service.store.one(
        "SELECT COUNT(*) total FROM wiki_sources WHERE channel_id=%s AND source_type='message' AND verified=FALSE AND excluded=FALSE AND deleted=FALSE",
        (ch["id"],),
    )
    rows = await service.store.query(
        f"SELECT message_id,created_at,body,category,exclusion_reason FROM wiki_sources WHERE {where} ORDER BY message_id LIMIT 20 OFFSET %s",
        [*args, (page - 1) * 20],
        fetch=True,
    )
    return {
        "total": count["total"],
        "page": page,
        "page_size": 20,
        "unverified_total": pending["total"],
        "reasons": [
            {
                "key": row["exclusion_reason"],
                "label": EXCLUSION_LABELS.get(row["exclusion_reason"], row["exclusion_reason"]),
                "count": row["total"],
            }
            for row in groups
        ],
        "items": [
            {
                **row,
                "reason": EXCLUSION_LABELS.get(row["exclusion_reason"], row["exclusion_reason"]),
                "url": f"https://t.me/{ch['username']}/{row['message_id']}"
                if ch["username"]
                else None,
            }
            for row in rows
        ],
    }


@admin.post("/channels/{channel}/excluded/refresh")
async def refresh_exclusions(channel: str, actor=ADMIN_AUTH):
    service, ch = await managed(channel)
    active = (
        await service.store.one("SELECT * FROM wiki_jobs WHERE id=%s", (ch["active_job"],))
        if ch["active_job"]
        else None
    )
    resume = bool(active and active["status"] in {"running", "pending", "partial"})
    if resume:
        await service.store.control_job(ch["id"], active["id"], "pause", actor)
    try:
        restored = await service.collector.repair_exclusions(ch)
        if restored and active:
            async with service.store.transaction() as cur:
                await cur.execute(
                    "SELECT id FROM wiki_channels WHERE id=%s FOR UPDATE", (ch["id"],)
                )
                await cur.execute("SELECT * FROM wiki_jobs WHERE id=%s FOR UPDATE", (active["id"],))
                job = await cur.fetchone()
                cursor, progress = decode(job["cursor"]), decode(job["progress"])
                if "batch_pipeline" in cursor:
                    # 恢复的来源重新进入待处理集合：重置提取游标与装配状态即可，
                    # 已应用的来源会通过 chunk 应用标记快速跳过，不会重复写入。
                    cursor["source_after"] = ""
                    cursor["processed_count"] = 0
                    cursor.pop("page_after", None)
                    cursor["revisions"] = {}
                    cursor.pop("dropped_pages", None)
                    progress.update(
                        processed=0,
                        processed_chunks=0,
                        generation_complete=False,
                        review_complete=False,
                    )
                    await cur.execute(
                        "UPDATE wiki_jobs SET phase='extract',`cursor`=%s,progress=%s WHERE id=%s",
                        (encode(cursor), encode(progress), job["id"]),
                    )
        elif restored and ch["enabled"]:
            await service.store.enqueue(ch["id"], "reindex", actor)
        async with service.store.transaction() as cur:
            await service.store.audit(
                cur,
                ch["id"],
                "repair_exclusions",
                ch["id"],
                actor,
                {"restored_message_ids": restored},
            )
        return {"restored": len(restored), "message_ids": restored}
    finally:
        if resume:
            await service.store.control_job(ch["id"], active["id"], "resume", actor)
        service.wake()


@admin.put("/channels/{channel}/config")
async def set_config(channel: str, body: WikiConfig, actor=ADMIN_AUTH):
    service, ch = await managed(channel)
    if body.public and not ch["access_verified"]:
        raise HTTPException(409, "频道不是已核实的公开频道")
    await service.store.configure(ch["id"], body, actor)
    return await service.store.channel(ch["id"])


@admin.put("/channels/{channel}/cover")
async def upload_cover(channel: str, request: Request, actor=ADMIN_AUTH):
    service, ch = await managed(channel)
    data = bytearray()
    async for chunk in request.stream():
        if len(data) + len(chunk) > MAX_IMAGE_BYTES:
            raise HTTPException(413, "频道封面不能超过 8 MiB")
        data.extend(chunk)
    image = bytes(data)
    try:
        await asyncio.to_thread(validate_image, image)
    except ImageInputError as error:
        raise HTTPException(422, str(error)) from None
    digest = sha256(image).hexdigest()
    if not await service.store.set_cover(ch["id"], digest, image, actor):
        raise HTTPException(404, "频道不存在")
    return {"cover_digest": digest}


@admin.delete("/channels/{channel}/cover")
async def delete_cover(channel: str, actor=ADMIN_AUTH):
    service, ch = await managed(channel)
    await service.store.set_cover(ch["id"], None, None, actor)
    return {"cover_digest": None}


@admin.post("/channels/{channel}/jobs", status_code=202)
async def create_job(channel: str, body: JobRequest, actor=ADMIN_AUTH):
    service, ch = await managed(channel)
    try:
        job_id = await service.store.enqueue(ch["id"], body.kind, actor, body.page_id)
    except ValueError as error:
        raise HTTPException(409, str(error)) from None
    service.wake()
    return {"job_id": job_id}


@admin.get("/channels/{channel}/jobs")
async def jobs(channel: str, page: int = Query(1, ge=1, le=10000)):
    service, ch = await managed(channel)
    rows = await service.store.query(
        """SELECT id,kind,status,phase,progress,attempts,error_code,created_at,updated_at,
        next_attempt,lease_until,UTC_TIMESTAMP(6) server_time
        FROM wiki_jobs WHERE channel_id=%s ORDER BY created_at DESC LIMIT 50 OFFSET %s""",
        (ch["id"], (page - 1) * 50),
        fetch=True,
    )
    return [{**row, "progress": decode(row["progress"])} for row in rows]


@admin.post("/channels/{channel}/jobs/{job_id}/{action}")
async def control_job(
    channel: str, job_id: str, action: Literal["pause", "resume", "retry"], actor=ADMIN_AUTH
):
    service, ch = await managed(channel)
    try:
        await service.store.control_job(ch["id"], job_id, action, actor)
        if action != "pause":
            service.wake()
    except ValueError as error:
        raise HTTPException(409, str(error)) from None
    return {"success": True}


@admin.get("/channels/{channel}/revisions")
async def revisions(
    channel: str,
    page: int = Query(1, ge=1, le=10000),
    q: str = Query("", max_length=160),
    status: Literal["all", "pending", "failed", "passed", "rejected"] = "all",
    job_id: str | None = Query(None, pattern=r"^[a-f0-9]{32}$"),
):
    service, ch = await managed(channel)
    conditions = ["r.channel_id=%s"]
    args = [ch["id"]]
    policy = None
    if job_id:
        job = await service.store.one(
            "SELECT kind,`cursor` FROM wiki_jobs WHERE channel_id=%s AND id=%s", (ch["id"], job_id)
        )
        if not job:
            raise HTTPException(404, "找不到该频道的任务")
        manifest = decode(job["cursor"])
        policy = manifest.get("review_policy")
        candidates = manifest.get("review_revisions", manifest.get("revisions"))
        if candidates is not None:
            if (
                "review_revisions" not in manifest
                and policy
                and not ch["published_revision"]
                and job["kind"] in service.engine.INCREMENTAL_KINDS
            ):
                # Older running jobs did not persist the expanded review list for the first publication.
                candidates = await service.engine.knowledge.expand_revisions(None, ch, manifest)
            if candidates:
                conditions.append(
                    "r.id IN (SELECT candidate.id FROM JSON_TABLE(%s, '$[*]' "
                    "COLUMNS(id CHAR(32) CHARACTER SET ascii PATH '$')) AS candidate)"
                )
                args.append(encode(list(candidates.values())))
            else:
                conditions.append("FALSE")
        else:
            conditions.append("r.job_id=%s")
            args.append(job_id)
    if q.strip():
        term = "%" + q.strip().replace("!", "!!").replace("%", "!%").replace("_", "!_") + "%"
        conditions.append("(p.title LIKE %s ESCAPE '!' OR r.search_text LIKE %s ESCAPE '!')")
        args.extend((term, term))
    review_filter = "AND rr.policy_key=%s" if policy else ""
    status_filters = {
        "pending": "rejected=FALSE AND approved=FALSE AND review_result IS NULL",
        "failed": "rejected=FALSE AND review_result=FALSE",
        "passed": "rejected=FALSE AND (review_result=TRUE OR (review_result IS NULL AND approved=TRUE))",
        "rejected": "rejected=TRUE",
    }
    status_filter = status_filters.get(status, "1=1")
    return await service.store.query(
        f"""SELECT * FROM (
        SELECT r.id,r.page_id,r.job_id,r.approved,r.rejected,r.created_at,
        p.title,p.slug,p.locked,p.hidden,
        LEFT(JSON_UNQUOTE(JSON_EXTRACT(r.document,'$.claims[0].text')),200) excerpt,
        (SELECT rr.approved FROM wiki_revision_reviews rr WHERE rr.revision_id=r.id {review_filter}
        ORDER BY rr.created_at DESC,rr.policy_key,rr.subject_hash LIMIT 1) review_result
        FROM wiki_page_revisions r JOIN wiki_pages p ON p.id=r.page_id
        WHERE {" AND ".join(conditions)}) listed
        WHERE {status_filter} ORDER BY created_at DESC,id DESC LIMIT 50 OFFSET %s""",
        (*([policy] if policy else []), *args, (page - 1) * 50),
        fetch=True,
    )


@admin.get("/channels/{channel}/revisions/{revision_id}")
async def revision_detail(
    channel: str,
    revision_id: str,
    job_id: str | None = Query(None, pattern=r"^[a-f0-9]{32}$"),
):
    service, ch = await managed(channel)
    row = await service.store.one(
        "SELECT * FROM wiki_page_revisions WHERE id=%s AND channel_id=%s", (revision_id, ch["id"])
    )
    if not row:
        raise HTTPException(404)
    old = await service.store.one(
        "SELECT document FROM wiki_page_revisions WHERE page_id=%s AND created_at<%s ORDER BY created_at DESC LIMIT 1",
        (row["page_id"], row["created_at"]),
    )
    document = decode(row["document"])
    previous = decode(old["document"]) if old else {}
    changes, truncated = revision_changes(previous, document)
    diff = "\n".join(
        difflib.unified_diff(
            encode(previous).splitlines(),
            encode(document).splitlines(),
            fromfile="previous",
            tofile="draft",
            lineterm="",
        )
    )
    evidence = await service.store.query(
        """SELECT e.source_id,e.start_offset,e.end_offset,s.source_type,s.verified,s.deleted,s.hidden,s.body
        FROM wiki_evidence e JOIN wiki_sources s ON s.id=e.source_id WHERE e.revision_id=%s""",
        (revision_id,),
        fetch=True,
    )
    review = await service.store.one(
        """SELECT rr.approved,rr.result,rr.created_at FROM wiki_revision_reviews rr
        WHERE rr.revision_id=%s AND (%s IS NULL OR rr.policy_key=(
            SELECT CAST(JSON_UNQUOTE(JSON_EXTRACT(j.`cursor`,'$.review_policy')) AS CHAR CHARACTER SET ascii)
            FROM wiki_jobs j WHERE j.channel_id=%s AND j.id=%s))
        ORDER BY rr.created_at DESC,rr.policy_key,rr.subject_hash LIMIT 1""",
        (revision_id, job_id, ch["id"], job_id),
    )
    if review:
        review["result"] = decode(review["result"])
    return {
        "id": revision_id,
        "document": document,
        "diff": diff,
        "changes": changes,
        "changes_truncated": truncated,
        "evidence": evidence,
        "review": review,
    }


@admin.post("/channels/{channel}/revisions/{revision_id}/reject")
async def reject(channel: str, revision_id: str, actor=ADMIN_AUTH):
    service, ch = await managed(channel)
    async with service.store.transaction() as cur:
        await cur.execute("SELECT id FROM wiki_channels WHERE id=%s FOR UPDATE", (ch["id"],))
        await cur.execute(
            "UPDATE wiki_page_revisions SET rejected=TRUE WHERE id=%s AND channel_id=%s",
            (revision_id, ch["id"]),
        )
        await cur.execute(
            """UPDATE wiki_pages p JOIN wiki_page_revisions r ON r.page_id=p.id
            SET p.locked=TRUE WHERE r.id=%s AND r.channel_id=%s""",
            (revision_id, ch["id"]),
        )
        await cur.execute(
            "UPDATE wiki_channels SET source_generation=source_generation+1 WHERE id=%s",
            (ch["id"],),
        )
        await service.store.audit(cur, ch["id"], "reject", revision_id, actor)
    return {"success": True}


@admin.post("/channels/{channel}/revisions/{revision_id}/correct")
async def correct(channel: str, revision_id: str, body: CorrectionRequest, actor=ADMIN_AUTH):
    service, ch = await managed(channel)
    try:
        new_id, job_id = await service.engine.knowledge.correct_page(
            ch["id"], revision_id, body.claim, body.text, actor
        )
    except ValueError as error:
        raise HTTPException(409, str(error)) from None
    service.wake()
    return {"revision_id": new_id, "job_id": job_id}


@admin.post("/channels/{channel}/revisions/{revision_id}/review/retry", status_code=202)
async def retry_revision_review(channel: str, revision_id: str, actor=ADMIN_AUTH):
    service, ch = await managed(channel)
    async with service.store.transaction() as cur:
        await cur.execute(
            "SELECT active_job FROM wiki_channels WHERE id=%s FOR UPDATE", (ch["id"],)
        )
        channel_row = await cur.fetchone()
        await cur.execute(
            """SELECT j.id,j.`cursor`,j.status,j.phase FROM wiki_jobs j
            JOIN wiki_page_revisions r ON r.job_id=j.id
            WHERE r.id=%s AND r.channel_id=%s AND j.kind='page_edit' FOR UPDATE""",
            (revision_id, ch["id"]),
        )
        edit_job = await cur.fetchone()
        if edit_job:
            if edit_job["status"] not in {"completed", "failed"}:
                raise HTTPException(409, "该词条修订仍在处理中")
        elif channel_row["active_job"]:
            raise HTTPException(409, "请等待当前 Wiki 任务结束")
        await cur.execute(
            """SELECT rv.policy_key,rv.subject_hash,rv.result,rv.created_at FROM wiki_revision_reviews rv
            JOIN wiki_page_revisions r ON r.id=rv.revision_id
            WHERE r.id=%s AND r.channel_id=%s AND r.rejected=FALSE AND rv.approved=FALSE""",
            (revision_id, ch["id"]),
        )
        previous = await cur.fetchall()
        if not previous:
            raise HTTPException(404, "没有可重试的审校结果")
        await cur.execute(
            "DELETE FROM wiki_revision_reviews WHERE revision_id=%s AND approved=FALSE",
            (revision_id,),
        )
        await service.store.audit(
            cur,
            ch["id"],
            "retry_revision_review",
            revision_id,
            actor,
            {"previous_reviews": [{**row, "result": decode(row["result"])} for row in previous]},
        )
        if edit_job:
            manifest = decode(edit_job["cursor"])
            job_id = await service.store.enqueue_page_edit(
                cur, ch["id"], manifest["page_id"], revision_id, manifest["base_revision"], actor
            )
        else:
            job_id = await service.store.enqueue(ch["id"], "reindex", actor, cur=cur)
    service.wake()
    return {"job_id": job_id}


@admin.post("/channels/{channel}/publish")
async def publish(channel: str, body: PublishRequest, actor=ADMIN_AUTH):
    service, ch = await managed(channel)
    try:
        pub = await service.engine.knowledge.publish(ch["id"], body.job_id, actor)
    except ValueError as error:
        raise HTTPException(409, str(error)) from None
    return {"publication_id": pub}


@admin.post("/channels/{channel}/rollback")
async def rollback(channel: str, body: RollbackRequest, actor=ADMIN_AUTH):
    service, ch = await managed(channel)
    try:
        pub = await service.engine.knowledge.publish(
            ch["id"], None, actor, rollback=body.publication_id
        )
    except ValueError as error:
        raise HTTPException(409, str(error)) from None
    return {"publication_id": pub}


@admin.get("/channels/{channel}/publications")
async def publications(channel: str, page: int = Query(1, ge=1, le=10000)):
    service, ch = await managed(channel)
    return await service.store.query(
        "SELECT id,job_id,created_at FROM wiki_publications WHERE channel_id=%s ORDER BY created_at DESC LIMIT 50 OFFSET %s",
        (ch["id"], (page - 1) * 50),
        fetch=True,
    )


@admin.get("/channels/{channel}/extractions")
async def quarantined_extractions(channel: str, page: int = Query(1, ge=1, le=10000)):
    service, ch = await managed(channel)
    rows = await service.store.query(
        """SELECT e.cache_key,e.review,e.result,e.created_at,s.source_type
        FROM wiki_extractions e JOIN wiki_chunks chunk ON chunk.id=e.chunk_id
        JOIN wiki_sources s ON s.id=chunk.source_id WHERE e.channel_id=%s AND e.approved=FALSE
        AND NOT (JSON_EXTRACT(e.review,'$.mode') <=> 'draft')
        AND s.content_hash=chunk.content_hash AND s.hidden=FALSE AND s.deleted=FALSE
        ORDER BY e.created_at DESC LIMIT 50 OFFSET %s""",
        (ch["id"], (page - 1) * 50),
        fetch=True,
    )
    return [
        {**row, "review": decode(row["review"]), "result": decode(row["result"])} for row in rows
    ]


@admin.post("/channels/{channel}/extractions/{cache_key}/retry", status_code=202)
async def retry_review(channel: str, cache_key: str, actor=ADMIN_AUTH):
    service, ch = await managed(channel)
    async with service.store.transaction() as cur:
        await cur.execute("SELECT id FROM wiki_channels WHERE id=%s FOR UPDATE", (ch["id"],))
        await cur.execute(
            "UPDATE wiki_extractions SET review=%s WHERE cache_key=%s AND channel_id=%s AND approved=FALSE",
            (encode({"pending": True}), cache_key, ch["id"]),
        )
        if not cur.rowcount:
            raise HTTPException(404, "没有可重试的待审候选")
        await cur.execute(
            "UPDATE wiki_channels SET source_generation=source_generation+1,dirty_at=UTC_TIMESTAMP(6) WHERE id=%s",
            (ch["id"],),
        )
        await service.store.audit(cur, ch["id"], "retry_review", cache_key, actor)
    return {"job_id": await service.store.enqueue(ch["id"], "reindex", actor)}


@admin.post("/channels/{channel}/withdraw")
async def withdraw(channel: str, actor=ADMIN_AUTH):
    service, ch = await managed(channel)
    async with service.store.transaction() as cur:
        await cur.execute(
            "UPDATE wiki_channels SET published_revision=NULL WHERE id=%s", (ch["id"],)
        )
        await service.store.audit(cur, ch["id"], "withdraw", ch["published_revision"], actor)
    return {"success": True}


@admin.post("/channels/{channel}/hide-source")
async def hide_source(channel: str, body: SourceRequest, actor=ADMIN_AUTH):
    service, ch = await managed(channel)
    await service.store.revoke(ch["id"], source_id=body.source_id, actor=actor)
    return {"success": True}


@admin.get("/channels/{channel}/pages/{page_id}")
async def page_state(channel: str, page_id: str):
    service, ch = await managed(channel)
    row = await service.store.one(
        """SELECT p.id,p.title,p.locked,p.hidden FROM wiki_pages p WHERE p.channel_id=%s
        AND (p.id=%s OR p.slug=%s OR EXISTS (SELECT 1 FROM wiki_slug_aliases a
            WHERE a.channel_id=p.channel_id AND a.page_id=p.id AND a.slug=%s))""",
        (ch["id"], page_id, page_id, page_id),
    )
    if row is None:
        raise HTTPException(404, "词条不存在")
    return {**row, "locked": bool(row["locked"]), "hidden": bool(row["hidden"])}


@admin.post("/channels/{channel}/pages/{page_id}")
async def page_action(channel: str, page_id: str, body: PageAction, actor=ADMIN_AUTH):
    service, ch = await managed(channel)
    try:
        if body.action == "rename" and not (body.title and body.slug):
            raise ValueError("Title and slug are required")
        if body.action in {"merge", "split"} and not body.alias:
            raise ValueError("Alias is required")
        await service.engine.knowledge.manage_page(
            ch["id"],
            page_id,
            body.action,
            actor,
            title=body.title,
            slug=body.slug,
            alias=body.alias,
        )
    except ValueError as error:
        raise HTTPException(409, str(error)) from None
    return {"success": True}


@admin.get("/channels/{channel}/audit")
async def audit(channel: str, page: int = Query(1, ge=1, le=10000)):
    service, ch = await managed(channel)
    return await service.store.query(
        "SELECT action,target_id,actor,details,created_at FROM wiki_review_actions WHERE channel_id=%s ORDER BY id DESC LIMIT 50 OFFSET %s",
        (ch["id"], (page - 1) * 50),
        fetch=True,
    )


@admin.post("/channels/{channel}/purge")
async def purge(channel: str, body: PurgeRequest, actor=ADMIN_AUTH):
    service, ch = await managed(channel)
    if body.confirm_channel != ch["id"]:
        raise HTTPException(409, "请输入频道数字 ID 确认清除")
    async with service.store.transaction() as cur:
        await cur.execute(
            "UPDATE wiki_channels SET enabled=FALSE,public=FALSE,published_revision=NULL,active_job=NULL,config=%s WHERE id=%s",
            (encode(WikiConfig().model_dump(mode="json")), ch["id"]),
        )
        for table in (
            "wiki_channel_covers",
            "wiki_publications",
            "wiki_jobs",
            "wiki_pages",
            "wiki_extractions",
            "wiki_profile_cache",
        ):
            await cur.execute(f"DELETE FROM {table} WHERE channel_id=%s", (ch["id"],))
        # Retain only revocation tombstones so archives cannot resurrect forbidden content.
        await cur.execute(
            "DELETE FROM wiki_sources WHERE channel_id=%s AND deleted=FALSE AND hidden=FALSE",
            (ch["id"],),
        )
        await cur.execute("DELETE FROM wiki_albums WHERE channel_id=%s", (ch["id"],))
        await cur.execute("UPDATE wiki_sources SET body='' WHERE channel_id=%s", (ch["id"],))
        await service.store.audit(cur, ch["id"], "purge", ch["id"], actor)
    return {"success": True}


async def crawler_html(channel, slug=None):
    """Small same-process HTML view for crawlers/link previews; text is always escaped."""
    import html

    service = runtime()
    data = (
        await service.reader.page(channel, slug) if slug else await service.reader.channel(channel)
    )
    found(data)
    # og:image 供分享卡使用：只在配置了公开基础 URL 时输出，绝不回退到请求头 Host
    og_image = ""
    try:
        ch_row = await service.store.channel(channel)
        base = str(getattr((ch_row or {}).get("config"), "public_base_url", "") or "")
    except Exception:
        base = ""
    if base.startswith(("http://", "https://")):
        channel_id = data.get("channel_id") or data.get("id")
        avatar = f"{base.rstrip('/')}/api/public/wiki/channels/{channel_id}/avatar"
        og_image = f'<meta property="og:image" content="{html.escape(avatar, quote=True)}">'
    title = html.escape(data["title"])
    body = "".join(f"<p>{html.escape(claim['text'])}</p>" for claim in data.get("claims", []))
    body += "".join(
        f'<p><a href="{html.escape(card["url"], quote=True)}">{html.escape(card["title"])}</a></p>'
        for card in data.get("pages", [])
    )
    path = data.get("url", f"/wiki/{data['id']}")
    return HTMLResponse(
        f'<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><title>{title} · Sakura Wiki</title><meta property="og:title" content="{title}">{og_image}<link rel="canonical" href="{html.escape(path, quote=True)}"></head><body><h1>{title}</h1>{body}<a href="{html.escape(path, quote=True)}">打开交互页面</a></body></html>',
        headers={"Cache-Control": "no-store", "X-Content-Type-Options": "nosniff"},
    )
