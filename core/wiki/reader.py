# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot

"""Public read model. Never serialize database rows directly to anonymous callers."""

from functools import wraps

from .models import utc
from .schema import CHANNEL_VISIBLE, REVISION_VISIBLE
from .store import decode

VISIBLE = f"""{CHANNEL_VISIBLE} AND {REVISION_VISIBLE}
    AND NOT JSON_CONTAINS(JSON_EXTRACT(c.config,'$.hidden_topics'),JSON_QUOTE(p.topic))"""
PAGE_JOIN = """FROM wiki_channels c JOIN wiki_publication_pages pp ON pp.publication_id=c.published_revision
    JOIN wiki_pages p ON p.id=pp.page_id AND p.channel_id=c.id
    JOIN wiki_page_revisions r ON r.id=pp.revision_id AND r.channel_id=c.id"""


def like(value):
    return "%" + value.replace("=", "==").replace("%", "=%").replace("_", "=_") + "%"


def consistent_read(method):
    @wraps(method)
    async def read(self, *args, **kwargs):
        async with self.store.snapshot():
            return await method(self, *args, **kwargs)

    return read


class PublicReader:
    def __init__(self, store):
        self.store = store

    @consistent_read
    async def channels(self):
        rows = await self.store.query(
            f"""SELECT c.id,c.title,c.username,c.coverage,c.updated_at,
            c.published_revision,COUNT(p.id) page_count {PAGE_JOIN} WHERE {VISIBLE}
            GROUP BY c.id ORDER BY c.updated_at DESC LIMIT 200""",
            fetch=True,
        )
        return [
            {
                "id": r["id"],
                "title": r["title"],
                "username": r["username"],
                "coverage": public_coverage(r["coverage"]),
                "updated_at": r["updated_at"],
                "publication_id": r["published_revision"],
                "page_count": r["page_count"],
                "url": f"/wiki/{r['id']}",
            }
            for r in rows
        ]

    @consistent_read
    async def channel(self, channel):
        ch = await self.store.channel(channel)
        if not ch:
            return None
        row = await self.store.one(
            f"SELECT c.id FROM wiki_channels c WHERE c.id=%s AND {CHANNEL_VISIBLE}", (ch["id"],)
        )
        if not row:
            return None
        pages = await self.search(channel=ch["id"], page_size=12)
        return {
            "id": ch["id"],
            "title": ch["title"],
            "username": ch["username"],
            "publication_id": ch["published_revision"],
            "updated_at": ch["updated_at"],
            "coverage": public_coverage(ch["coverage"]),
            "columns": ch["config"].columns,
            "pages": pages["items"],
            "page_count": pages["total"],
            "source_policy": {
                "quotes": ch["config"].show_quotes,
                "telegram_links": ch["config"].show_telegram_links,
            },
        }

    @consistent_read
    async def search(
        self,
        q="",
        channel=None,
        page=1,
        page_size=20,
        kind=None,
        topic=None,
        after=None,
        before=None,
    ):
        page, page_size = max(1, min(page, 10000)), max(1, min(page_size, 50))
        after, before = utc(after) if after else None, utc(before) if before else None
        conditions, params = [VISIBLE], []
        if channel:
            resolved = await self.store.channel(channel)
            if not resolved:
                return {"items": [], "total": 0, "page": page, "page_size": page_size}
            conditions.append("c.id=%s")
            params.append(resolved["id"])
        if q:
            conditions.append(
                "(r.search_text LIKE %s ESCAPE '=' OR EXISTS (SELECT 1 FROM wiki_entity_aliases a WHERE a.page_id=p.id AND a.name LIKE %s ESCAPE '='))"
            )
            params.extend([like(q), like(q)])
        for column, value, operator in [
            ("p.kind", kind, "="),
            ("p.topic", topic, "="),
            ("r.created_at", after, ">="),
            ("r.created_at", before, "<="),
        ]:
            if value:
                conditions.append(f"{column}{operator}%s")
                params.append(value)
        where = " AND ".join(conditions)
        count = await self.store.one(f"SELECT COUNT(*) total {PAGE_JOIN} WHERE {where}", params)
        rows = await self.store.query(
            f"""SELECT p.id,p.slug,p.title,p.kind,p.topic,c.id channel_id,c.title channel_title,
            c.published_revision,r.id revision_id,r.created_at,r.document {PAGE_JOIN} WHERE {where}
            ORDER BY r.created_at DESC,p.id LIMIT %s OFFSET %s""",
            [*params, page_size, (page - 1) * page_size],
            fetch=True,
        )
        return {
            "items": [page_card(row) for row in rows],
            "total": count["total"],
            "page": page,
            "page_size": page_size,
        }

    @consistent_read
    async def page(self, channel, slug):
        ch = await self.store.channel(channel)
        if not ch:
            return None
        row = await self.store.one(
            f"""SELECT p.id,p.slug,p.title,p.kind,p.topic,c.id channel_id,c.title channel_title,
            c.username,c.config,c.published_revision,r.id revision_id,r.created_at,r.document {PAGE_JOIN}
            WHERE c.id=%s AND (p.slug=%s OR p.id=%s OR EXISTS (SELECT 1 FROM wiki_slug_aliases sa
            WHERE sa.channel_id=c.id AND sa.page_id=p.id AND sa.slug=%s)) AND {VISIBLE} LIMIT 1""",
            (ch["id"], slug, slug, slug),
        )
        if not row:
            return None
        config = decode(row["config"])
        evidence_rows = await self.store.query(
            """SELECT DISTINCT s.id,s.source_type,s.message_id,s.created_at,s.edited_at
            FROM wiki_evidence e JOIN wiki_sources s ON s.id=e.source_id WHERE e.revision_id=%s""",
            (row["revision_id"],),
            fetch=True,
        )
        evidence = {r["id"]: r for r in evidence_rows}
        result = page_card(row)
        doc = decode(row["document"])
        result["verified_at"] = doc["verified_at"]
        result["claims"] = [
            public_claim(fact, evidence, row["username"], config) for fact in doc["claims"]
        ]
        result["sources"] = [
            source_card(source, row["username"], config) for source in evidence_rows
        ]
        # Historical metadata is restricted to revisions that were actually published.
        history = await self.store.query(
            f"""SELECT DISTINCT r.id,r.created_at FROM wiki_page_revisions r
            JOIN wiki_pages p ON p.id=r.page_id JOIN wiki_publication_pages pp ON pp.revision_id=r.id
            WHERE p.id=%s AND {REVISION_VISIBLE} ORDER BY r.created_at DESC LIMIT 50""",
            (row["id"],),
            fetch=True,
        )
        result["history"] = [
            {"revision_id": r["id"], "created_at": r["created_at"]} for r in history
        ]
        # Recheck the same snapshot and visibility after multi-query assembly.
        valid = await self.store.one(
            f"""SELECT r.id {PAGE_JOIN} WHERE c.id=%s AND c.published_revision=%s
            AND r.id=%s AND {VISIBLE}""",
            (ch["id"], row["published_revision"], row["revision_id"]),
        )
        return result if valid else None

    @consistent_read
    async def profile(self, channel):
        ch = await self.store.channel(channel)
        if not ch:
            return None
        row = await self.store.one(
            f"""SELECT pub.profile,pub.coverage,pub.id,pub.created_at FROM wiki_channels c
            JOIN wiki_publications pub ON pub.id=c.published_revision WHERE c.id=%s AND {CHANNEL_VISIBLE}""",
            (ch["id"],),
        )
        if not row:
            return None
        # Topic labels can be hidden after publication. Counts are recomputed from live allowed
        # sources so deleting/withdrawing data cannot expose an old profile through rollback.
        from .knowledge import Knowledge

        live = await Knowledge(self.store).profile(ch["id"])
        live["topics"] = [t for t in live["topics"] if t["topic"] not in ch["config"].hidden_topics]
        snapshot = decode(row["profile"])
        live["narrative"] = (
            snapshot.get("narrative", {})
            if not ch["config"].hidden_topics
            and snapshot.get("source_generation") == ch["source_generation"]
            else {}
        )
        return {
            **live,
            "publication_id": row["id"],
            "published_at": row["created_at"],
            "coverage": public_coverage(ch["coverage"]),
        }

    @consistent_read
    async def changes(self, channel, page=1, page_size=20):
        # Published, still-authorized page changes only. Audit logs and drafts are separate.
        return await self.search(channel=channel, page=page, page_size=page_size)

    @consistent_read
    async def timeline(self, channel, page=1, page_size=20):
        ch = await self.store.channel(channel)
        if not ch:
            return {"items": [], "page": page, "page_size": page_size, "total_pages": 0}
        join = PAGE_JOIN + " JOIN wiki_revision_claims wc ON wc.revision_id=r.id "
        total = await self.store.one(
            f"SELECT COUNT(*) n {join} WHERE c.id=%s AND {VISIBLE}", (ch["id"],)
        )
        rows = await self.store.query(
            f"""SELECT p.slug,wc.ordinal,r.id revision_id {join}
            WHERE c.id=%s AND {VISIBLE} ORDER BY COALESCE(wc.occurred_at,wc.source_created_at) DESC,r.id,wc.ordinal
            LIMIT %s OFFSET %s""",
            (ch["id"], page_size, (page - 1) * page_size),
            fetch=True,
        )
        events = []
        pages = {}
        for row in rows:
            if row["slug"] not in pages:
                pages[row["slug"]] = await self.page(channel, row["slug"])
            doc = pages[row["slug"]]
            if doc and doc["revision_id"] == row["revision_id"]:
                events.append(
                    {
                        "page_id": doc["id"],
                        "title": doc["title"],
                        "url": doc["url"],
                        "publication_id": doc["publication_id"],
                        **doc["claims"][row["ordinal"]],
                    }
                )
        return {
            "items": events,
            "page": page,
            "page_size": page_size,
            "total_pages": (total["n"] + page_size - 1) // page_size,
        }

    @consistent_read
    async def graph(
        self,
        channel,
        node_id=None,
        depth=1,
        limit=60,
        kind=None,
        topic=None,
        relation=None,
        q="",
        after=None,
        before=None,
    ):
        ch = await self.store.channel(channel)
        if not ch:
            return None
        limit, depth = max(1, min(limit, 120)), max(1, min(depth, 2))
        after, before = utc(after) if after else None, utc(before) if before else None
        if node_id:
            root = await self.page(ch["id"], node_id)
            if not root:
                return None
            nodes = {
                root["id"]: {
                    key: root[key]
                    for key in (
                        "id",
                        "title",
                        "kind",
                        "topic",
                        "url",
                        "revision_id",
                        "publication_id",
                    )
                }
            }
        else:
            initial = await self.search(
                q=q,
                channel=ch["id"],
                kind=kind,
                topic=topic,
                page_size=min(20, limit),
                after=after,
                before=before,
            )
            nodes = {n["id"]: n for n in initial["items"]}
        edges = {}
        frontier = list(nodes)
        for _ in range(depth):
            if not frontier or len(nodes) >= limit:
                break
            placeholders = ",".join(["%s"] * len(frontier))
            rows = await self.store.query(
                f"""SELECT e.id,e.source_page,e.target_page,e.kind,e.relation_json,
                s.created_at FROM wiki_edges e JOIN wiki_publication_edges pe ON pe.edge_id=e.id
                JOIN wiki_chunks chunk ON chunk.id=e.chunk_id JOIN wiki_sources s ON s.id=chunk.source_id
                WHERE pe.publication_id=%s AND e.channel_id=%s AND s.deleted=FALSE AND s.hidden=FALSE
                AND s.excluded=FALSE AND s.verified=TRUE AND s.content_hash=e.source_hash
                AND (e.source_page IN ({placeholders}) OR e.target_page IN ({placeholders}))
                ORDER BY e.id LIMIT %s""",
                [ch["published_revision"], ch["id"], *frontier, *frontier, limit * 3],
                fetch=True,
            )
            frontier = []
            for row in rows:
                if relation and row["kind"] != relation:
                    continue
                if (after and row["created_at"] < after) or (before and row["created_at"] > before):
                    continue
                valid = True
                for page_id in (row["source_page"], row["target_page"]):
                    if page_id not in nodes:
                        if len(nodes) >= limit:
                            valid = False
                            break
                        page_row = await self.page(ch["id"], page_id)
                        if (
                            not page_row
                            or page_row["publication_id"] != ch["published_revision"]
                            or (kind and page_row["kind"] != kind)
                            or (topic and page_row["topic"] != topic)
                        ):
                            valid = False
                            break
                        nodes[page_id] = {
                            key: page_row[key]
                            for key in (
                                "id",
                                "title",
                                "kind",
                                "topic",
                                "url",
                                "revision_id",
                                "publication_id",
                            )
                        }
                        frontier.append(page_id)
                if valid:
                    payload = decode(row["relation_json"])
                    edges[row["id"]] = {
                        "id": row["id"],
                        "source": row["source_page"],
                        "target": row["target_page"],
                        "kind": row["kind"],
                        "confidence": payload["confidence"],
                        "evidence_count": len(payload["evidence"]),
                    }
        return {
            "nodes": list(nodes.values()),
            "edges": list(edges.values()),
            "publication_id": ch["published_revision"],
            "limit": limit,
            "truncated": len(nodes) >= limit,
        }


def page_card(row):
    doc = decode(row["document"])
    claims = doc.get("claims", [])
    return {
        "id": row["id"],
        "slug": row["slug"],
        "title": doc["title"],
        "kind": row["kind"],
        "topic": row["topic"],
        "channel_id": row["channel_id"],
        "channel_title": row["channel_title"],
        "publication_id": row["published_revision"],
        "revision_id": row["revision_id"],
        "updated_at": row["created_at"],
        "excerpt": claims[0]["text"][:240] if claims else "",
        "evidence_count": len({e["source_id"] for f in claims for e in f["evidence"]}),
        "url": f"/wiki/{row['channel_id']}/page/{row['slug']}",
    }


def source_card(source, username, config):
    return {
        "id": source["id"],
        "type": source["source_type"],
        "created_at": source["created_at"],
        "edited_at": source["edited_at"],
        "url": f"https://t.me/{username}/{source['message_id']}"
        if username
        and source["message_id"]
        and config.get("show_telegram_links")
        and source["source_type"] != "summary"
        else None,
    }


def public_claim(fact, sources, username, config):
    return {
        **{
            key: fact.get(key)
            for key in (
                "key",
                "text",
                "status",
                "confidence",
                "occurred_at",
                "valid_until",
                "source_created_at",
            )
        },
        "evidence": [
            {
                "source_id": e["source_id"],
                "start": e["start"],
                "end": e["end"],
                "quote": e["quote"] if config.get("show_quotes") else None,
                "url": source_card(sources[e["source_id"]], username, config)["url"]
                if e["source_id"] in sources
                else None,
            }
            for e in fact["evidence"]
        ],
    }


def public_coverage(value):
    value = decode(value)
    return {
        key: value.get(key)
        for key in (
            "status",
            "reason",
            "enumerated",
            "indexed",
            "excluded",
            "estimated_total",
            "earliest",
            "latest",
            "missing_ranges",
            "failures",
            "updated_at",
        )
    }
