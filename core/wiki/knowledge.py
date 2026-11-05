"""Incremental entity resolution, revisions, review and atomic publication."""

import uuid
from collections import defaultdict

from .models import Extraction, digest, name_key, utc
from .schema import REVISION_VISIBLE
from .store import decode, encode


class Knowledge:
    def __init__(self, store):
        self.store = store

    async def entity(self, cur, channel, name, kind="Entity", topic="其他", aliases=()):
        key = digest(name_key(name))
        await cur.execute(
            "SELECT page_id FROM wiki_entity_aliases WHERE channel_id=%s AND alias_key=%s",
            (channel, key),
        )
        row = await cur.fetchone()
        if row:
            return row["page_id"]
        # Only unambiguous aliases are merged automatically; administrators can override.
        for alias in aliases:
            await cur.execute(
                "SELECT page_id FROM wiki_entity_aliases WHERE channel_id=%s AND alias_key=%s",
                (channel, digest(name_key(alias))),
            )
            row = await cur.fetchone()
            if row:
                await cur.execute(
                    "INSERT IGNORE INTO wiki_entity_aliases VALUES (%s,%s,%s,%s,FALSE)",
                    (channel, key, name[:160], row["page_id"]),
                )
                return row["page_id"]
        page_id = digest(channel, key)[:32]
        # ID-based slug remains stable when names/aliases change and works for CJK.
        await cur.execute(
            """INSERT IGNORE INTO wiki_pages
            (id,channel_id,entity_key,title,slug,kind,topic,updated_at)
            VALUES (%s,%s,%s,%s,%s,%s,%s,UTC_TIMESTAMP(6))""",
            (page_id, channel, key, name, page_id, kind, topic),
        )
        for alias in {name, *aliases}:
            await cur.execute(
                "INSERT IGNORE INTO wiki_entity_aliases VALUES (%s,%s,%s,%s,FALSE)",
                (channel, digest(name_key(alias)), alias[:160], page_id),
            )
        return page_id

    async def apply_extraction(self, job, chunk, result: Extraction):
        async with self.store.transaction() as cur:
            await cur.execute(
                "SELECT id FROM wiki_channels WHERE id=%s FOR UPDATE", (job["channel_id"],)
            )
            await self.store.fence(cur, job)
            await cur.execute("SELECT * FROM wiki_sources WHERE id=%s", (chunk["source_id"],))
            source = await cur.fetchone()
            if source and not source["verified"]:
                # Derived summary extraction informs planning, never creates public entities
                # or assertions until independently grounded in an original source.
                return
            if (
                not source
                or source["deleted"]
                or source["hidden"]
                or source["content_hash"] != chunk["content_hash"]
            ):
                return
            entities = {}
            await cur.execute(
                "DELETE FROM wiki_facts WHERE chunk_id=%s AND channel_id=%s",
                (chunk["id"], job["channel_id"]),
            )
            await cur.execute(
                "UPDATE wiki_edges SET active=FALSE WHERE chunk_id=%s AND channel_id=%s",
                (chunk["id"], job["channel_id"]),
            )
            for fact in result.facts:
                page_id = await self.entity(
                    cur, job["channel_id"], fact.entity, fact.kind, fact.topic, fact.aliases
                )
                entities[name_key(fact.entity)] = page_id
                payload = fact.model_dump(mode="json")
                payload["source_created_at"] = source["created_at"].isoformat()
                fact_id = digest(chunk["id"], page_id, fact.key, fact.text)
                await cur.execute(
                    """INSERT INTO wiki_facts (id,channel_id,page_id,chunk_id,source_hash,fact)
                    VALUES (%s,%s,%s,%s,%s,%s) ON DUPLICATE KEY UPDATE fact=VALUES(fact),page_id=VALUES(page_id)""",
                    (
                        fact_id,
                        job["channel_id"],
                        page_id,
                        chunk["id"],
                        chunk["content_hash"],
                        encode(payload),
                    ),
                )
            for relation in result.relations:
                source_page, target_page = (
                    entities[name_key(relation.source)],
                    entities[name_key(relation.target)],
                )
                await cur.execute(
                    """INSERT INTO wiki_edges
                    (id,channel_id,chunk_id,source_hash,source_page,target_page,kind,relation_json)
                    VALUES (%s,%s,%s,%s,%s,%s,%s,%s) ON DUPLICATE KEY UPDATE active=TRUE""",
                    (
                        digest(
                            chunk["id"], source_page, target_page, relation.model_dump(mode="json")
                        ),
                        job["channel_id"],
                        chunk["id"],
                        chunk["content_hash"],
                        source_page,
                        target_page,
                        relation.kind,
                        encode(relation.model_dump(mode="json")),
                    ),
                )

    async def build_page(self, job, page):
        rows = await self.store.query(
            """SELECT f.fact FROM wiki_facts f
            JOIN wiki_chunks ch ON ch.id=f.chunk_id JOIN wiki_sources s ON s.id=ch.source_id
            WHERE f.page_id=%s AND s.deleted=FALSE AND s.hidden=FALSE AND s.excluded=FALSE
            AND s.verified=TRUE AND s.content_hash=f.source_hash ORDER BY s.created_at,f.id""",
            (page["id"],),
            fetch=True,
        )
        facts = merge_facts([decode(row["fact"]) for row in rows])
        if not facts:
            return None
        old = await self.store.one(
            "SELECT * FROM wiki_page_revisions WHERE page_id=%s ORDER BY created_at DESC,id DESC LIMIT 1",
            (page["id"],),
        )
        if page["locked"]:
            if not old:
                return None
            valid = await self.store.one(
                f"SELECT r.id FROM wiki_page_revisions r JOIN wiki_pages p ON p.id=r.page_id WHERE r.id=%s AND {REVISION_VISIBLE}",
                (old["id"],),
            )
            return old["id"] if valid else None
        override = decode(page["override_json"])
        document = {
            "title": override.get("title", page["title"]),
            "kind": page["kind"],
            "topic": page["topic"],
            "claims": facts,
            "verified_at": utc().isoformat(),
        }
        content_hash = digest(
            {key: value for key, value in document.items() if key != "verified_at"}
        )
        if old and old["content_hash"] == content_hash and not old["rejected"]:
            return old["id"]
        revision_id = uuid.uuid4().hex
        async with self.store.transaction() as cur:
            await cur.execute(
                "SELECT id FROM wiki_channels WHERE id=%s FOR UPDATE", (job["channel_id"],)
            )
            await self.store.fence(cur, job)
            await cur.execute(
                """INSERT INTO wiki_page_revisions
                (id,channel_id,page_id,job_id,document,content_hash,search_text,approved,created_at)
                VALUES (%s,%s,%s,%s,%s,%s,%s,TRUE,UTC_TIMESTAMP(6))""",
                (
                    revision_id,
                    job["channel_id"],
                    page["id"],
                    job["id"],
                    encode(document),
                    content_hash,
                    "\n".join([document["title"], document["topic"], *[f["text"] for f in facts]]),
                ),
            )
            for ordinal, fact in enumerate(facts):
                await cur.execute(
                    "INSERT INTO wiki_revision_claims VALUES (%s,%s,%s,%s,%s)",
                    (
                        revision_id,
                        ordinal,
                        utc(fact["occurred_at"]) if fact.get("occurred_at") else None,
                        utc(fact["source_created_at"]),
                        encode(fact),
                    ),
                )
                for evidence in fact["evidence"]:
                    await cur.execute(
                        """INSERT IGNORE INTO wiki_evidence
                        SELECT %s,s.id,ch.content_hash,ch.id,%s,%s FROM wiki_chunks ch
                        JOIN wiki_sources s ON s.id=ch.source_id WHERE ch.id=%s AND s.channel_id=%s""",
                        (
                            revision_id,
                            evidence["start"],
                            evidence["end"],
                            evidence["chunk_id"],
                            job["channel_id"],
                        ),
                    )
        return revision_id

    async def profile(self, channel, cur=None, publication=None):
        async def rows(sql, args=()):
            if cur:
                await cur.execute(sql, args)
                return list(await cur.fetchall())
            return await self.store.query(sql, args, fetch=True)

        scope = "channel_id=%s AND source_type='message' AND deleted=FALSE AND hidden=FALSE AND excluded=FALSE AND verified=TRUE"
        stats = (
            await rows(
                f"""SELECT COUNT(*) message_count,COALESCE(AVG(original_length),0) avg_message_length,
            MIN(created_at) earliest,MAX(created_at) latest FROM wiki_sources WHERE {scope}""",
                (channel,),
            )
        )[0]
        hours = await rows(
            f"SELECT HOUR(created_at) hour,COUNT(*) count FROM wiki_sources WHERE {scope} GROUP BY HOUR(created_at) ORDER BY hour",
            (channel,),
        )
        months = await rows(
            f"SELECT DATE_FORMAT(created_at,'%%Y-%%m') month,COUNT(*) count FROM wiki_sources WHERE {scope} GROUP BY month ORDER BY month",
            (channel,),
        )
        if publication is None:
            current = await rows(
                "SELECT published_revision FROM wiki_channels WHERE id=%s", (channel,)
            )
            publication = current[0]["published_revision"] if current else None
        topic_scope = f"""FROM wiki_publication_pages pp JOIN wiki_pages p ON p.id=pp.page_id
            JOIN wiki_page_revisions r ON r.id=pp.revision_id JOIN wiki_evidence ev ON ev.revision_id=r.id
            JOIN wiki_sources s ON s.id=ev.source_id JOIN wiki_channels c ON c.id=p.channel_id
            WHERE pp.publication_id=%s AND p.channel_id=%s AND s.source_type='message' AND {REVISION_VISIBLE}
            AND NOT JSON_CONTAINS(JSON_EXTRACT(c.config,'$.hidden_topics'),JSON_QUOTE(p.topic))"""
        topics = await rows(
            f"SELECT p.topic,COUNT(DISTINCT s.id) message_count {topic_scope} GROUP BY p.topic ORDER BY message_count DESC LIMIT 100",
            (publication, channel),
        )
        trends = await rows(
            f"SELECT p.topic,DATE_FORMAT(s.created_at,'%%Y-%%m') month,COUNT(DISTINCT s.id) message_count {topic_scope} GROUP BY p.topic,month ORDER BY month,p.topic",
            (publication, channel),
        )
        return {
            "statistics": stats,
            "hours_utc": hours,
            "months": months,
            "topics": topics,
            "topic_trends": trends,
            "method": "Actual verified messages; topics may overlap. No user profiling.",
            "as_of": utc().isoformat(),
        }

    async def publish(self, channel, job_id, actor, *, rollback=None, running_job=None):
        publication_id = uuid.uuid4().hex
        async with self.store.transaction() as cur:
            await cur.execute("SELECT * FROM wiki_channels WHERE id=%s FOR UPDATE", (channel,))
            ch = await cur.fetchone()
            if (
                not ch
                or not ch["enabled"]
                or not ch["access_verified"]
                or not ch["verified_until"]
                or ch["verified_until"] < utc()
            ):
                raise ValueError("Channel is disabled or its public access is unverified")
            if running_job:
                await self.store.fence(cur, running_job)
            if job_id:
                await cur.execute(
                    "SELECT id FROM wiki_publications WHERE channel_id=%s AND job_id=%s ORDER BY created_at DESC LIMIT 1",
                    (channel, job_id),
                )
                existing = await cur.fetchone()
                if existing:
                    if existing["id"] != ch["published_revision"]:
                        raise ValueError(
                            "This job was already published; use explicit rollback for an older snapshot"
                        )
                    return existing["id"]
            if rollback:
                await cur.execute(
                    "SELECT * FROM wiki_publications WHERE id=%s AND channel_id=%s",
                    (rollback, channel),
                )
                if not await cur.fetchone():
                    raise ValueError("Unknown publication")
                await cur.execute(
                    """SELECT pp.page_id,pp.revision_id FROM wiki_publication_pages pp
                    WHERE pp.publication_id=%s""",
                    (rollback,),
                )
                candidates = list(await cur.fetchall())
            else:
                await cur.execute(
                    "SELECT * FROM wiki_jobs WHERE id=%s AND channel_id=%s", (job_id, channel)
                )
                job = await cur.fetchone()
                progress = decode(job["progress"]) if job else {}
                if (
                    not job
                    or job["status"] not in {"completed", "running"}
                    or job["phase"] != "ready"
                    or (job["status"] == "running" and running_job is None)
                    or not progress.get("generation_complete")
                    or not progress.get("review_complete")
                    or progress.get("source_generation") != ch["source_generation"]
                ):
                    raise ValueError(
                        "Job has not completed validation, or sources changed; update before publishing"
                    )
                # Revisions are prepared as an explicit manifest, including unchanged pages.
                candidates = [
                    {"page_id": key, "revision_id": value}
                    for key, value in decode(job["cursor"]).get("revisions", {}).items()
                ]
            if not candidates:
                raise ValueError("No reviewed knowledge to publish")
            for candidate in candidates:
                await cur.execute(
                    f"""SELECT r.id FROM wiki_page_revisions r JOIN wiki_pages p ON p.id=r.page_id
                    WHERE r.id=%s AND r.channel_id=%s AND p.id=%s AND {REVISION_VISIBLE}""",
                    (candidate["revision_id"], channel, candidate["page_id"]),
                )
                if not await cur.fetchone():
                    raise ValueError("Publication includes revoked, rejected or hidden evidence")
            await cur.execute(
                "INSERT INTO wiki_publications VALUES (%s,%s,%s,%s,%s,UTC_TIMESTAMP(6))",
                (publication_id, channel, job_id, encode({}), ch["coverage"]),
            )
            for candidate in candidates:
                await cur.execute(
                    "INSERT INTO wiki_publication_pages VALUES (%s,%s,%s)",
                    (publication_id, candidate["page_id"], candidate["revision_id"]),
                )
            profile = await self.profile(channel, cur, publication_id)
            if not rollback:
                manifest = decode(job["cursor"])
                profile["narrative"] = manifest.get("profile_narrative", {})
                profile["source_generation"] = manifest.get("profile_generation")
            await cur.execute(
                "UPDATE wiki_publications SET profile=%s WHERE id=%s",
                (encode(profile), publication_id),
            )
            if rollback:
                await cur.execute(
                    "INSERT INTO wiki_publication_edges SELECT %s,edge_id FROM wiki_publication_edges WHERE publication_id=%s",
                    (publication_id, rollback),
                )
            else:
                await cur.execute(
                    """INSERT INTO wiki_publication_edges SELECT %s,e.id FROM wiki_edges e
                    JOIN wiki_chunks ch ON ch.id=e.chunk_id JOIN wiki_sources s ON s.id=ch.source_id
                    JOIN wiki_publication_pages a ON a.page_id=e.source_page AND a.publication_id=%s
                    JOIN wiki_publication_pages b ON b.page_id=e.target_page AND b.publication_id=%s
                    WHERE e.channel_id=%s AND e.active=TRUE AND s.content_hash=e.source_hash AND s.deleted=FALSE
                    AND s.hidden=FALSE AND s.excluded=FALSE AND s.verified=TRUE""",
                    (publication_id, publication_id, publication_id, channel),
                )
            await cur.execute(
                "UPDATE wiki_channels SET published_revision=%s,updated_at=UTC_TIMESTAMP(6) WHERE id=%s",
                (publication_id, channel),
            )
            if job_id and running_job is None:
                progress.update(published=True, publication_id=publication_id)
                await cur.execute(
                    "UPDATE wiki_jobs SET status='published',progress=%s,updated_at=UTC_TIMESTAMP(6) WHERE id=%s",
                    (encode(progress), job_id),
                )
                await cur.execute(
                    "UPDATE wiki_channels SET active_job=NULL WHERE id=%s AND active_job=%s",
                    (channel, job_id),
                )
            await self.store.audit(
                cur,
                channel,
                "rollback" if rollback else "publish",
                publication_id,
                actor,
                {"previous": ch["published_revision"], "rollback": rollback},
            )
        return publication_id

    async def correct(self, channel, revision_id, ordinal, text, actor):
        """An explicit administrator correction is a new audited, locked draft.

        It may change an assertion, but cannot invent sources or restore revoked evidence.
        Publication still requires the normal background manifest and explicit publish step.
        """
        new_id = uuid.uuid4().hex
        async with self.store.transaction() as cur:
            await cur.execute("SELECT id FROM wiki_channels WHERE id=%s FOR UPDATE", (channel,))
            await cur.execute(
                f"""SELECT r.* FROM wiki_page_revisions r JOIN wiki_pages p ON p.id=r.page_id
                WHERE r.id=%s AND r.channel_id=%s AND {REVISION_VISIBLE}""",
                (revision_id, channel),
            )
            old = await cur.fetchone()
            if not old:
                raise ValueError("Correction requires current valid evidence")
            document = decode(old["document"])
            if not 0 <= ordinal < len(document["claims"]):
                raise ValueError("Unknown claim")
            document["claims"][ordinal]["text"] = text
            document["verified_at"] = utc().isoformat()
            await cur.execute(
                """INSERT INTO wiki_page_revisions
                (id,channel_id,page_id,document,content_hash,search_text,approved,created_at)
                VALUES (%s,%s,%s,%s,%s,%s,TRUE,UTC_TIMESTAMP(6))""",
                (
                    new_id,
                    channel,
                    old["page_id"],
                    encode(document),
                    digest(document),
                    "\n".join(
                        [
                            document["title"],
                            document["topic"],
                            *[f["text"] for f in document["claims"]],
                        ]
                    ),
                ),
            )
            await cur.execute(
                "INSERT INTO wiki_evidence SELECT %s,source_id,source_hash,chunk_id,start_offset,end_offset FROM wiki_evidence WHERE revision_id=%s",
                (new_id, revision_id),
            )
            for index, fact in enumerate(document["claims"]):
                await cur.execute(
                    "INSERT INTO wiki_revision_claims VALUES (%s,%s,%s,%s,%s)",
                    (
                        new_id,
                        index,
                        utc(fact["occurred_at"]) if fact.get("occurred_at") else None,
                        utc(fact["source_created_at"]),
                        encode(fact),
                    ),
                )
            await cur.execute("UPDATE wiki_pages SET locked=TRUE WHERE id=%s", (old["page_id"],))
            await cur.execute(
                "UPDATE wiki_channels SET source_generation=source_generation+1,dirty_at=UTC_TIMESTAMP(6) WHERE id=%s",
                (channel,),
            )
            await self.store.audit(
                cur, channel, "correct", new_id, actor, {"previous": revision_id, "claim": ordinal}
            )
        return new_id

    async def manage_page(self, channel, page_id, action, actor, **options):
        async with self.store.transaction() as cur:
            await cur.execute("SELECT id FROM wiki_channels WHERE id=%s FOR UPDATE", (channel,))
            await cur.execute(
                "SELECT * FROM wiki_pages WHERE id=%s AND channel_id=%s FOR UPDATE",
                (page_id, channel),
            )
            page = await cur.fetchone()
            if not page:
                raise ValueError("Unknown page")
            if action in {"lock", "unlock", "hide", "show"}:
                column = "locked" if action in {"lock", "unlock"} else "hidden"
                await cur.execute(
                    f"UPDATE wiki_pages SET {column}=%s WHERE id=%s",
                    (action in {"lock", "hide"}, page_id),
                )
            elif action == "rename":
                title, slug = options["title"], options["slug"]
                await cur.execute(
                    "INSERT IGNORE INTO wiki_slug_aliases VALUES (%s,%s,%s)",
                    (channel, page["slug"], page_id),
                )
                await cur.execute(
                    "UPDATE wiki_pages SET title=%s,slug=%s,override_json=%s WHERE id=%s",
                    (title, slug, encode({"title": title}), page_id),
                )
            elif action in {"merge", "split"}:
                alias = options["alias"]
                # Explicitly rebind one alias. Move its facts only, not other entities.
                await cur.execute(
                    "INSERT INTO wiki_entity_aliases VALUES (%s,%s,%s,%s,TRUE) ON DUPLICATE KEY UPDATE page_id=VALUES(page_id),manual=TRUE",
                    (channel, digest(name_key(alias)), alias, page_id),
                )
                await cur.execute("SELECT id,fact FROM wiki_facts WHERE channel_id=%s", (channel,))
                for fact_row in await cur.fetchall():
                    if name_key(decode(fact_row["fact"])["entity"]) == name_key(alias):
                        await cur.execute(
                            "UPDATE wiki_facts SET page_id=%s WHERE id=%s",
                            (page_id, fact_row["id"]),
                        )
            else:
                raise ValueError("Unknown page action")
            await cur.execute(
                "UPDATE wiki_channels SET source_generation=source_generation+1,dirty_at=UTC_TIMESTAMP(6) WHERE id=%s",
                (channel,),
            )
            await self.store.audit(cur, channel, action, page_id, actor, options)


def merge_facts(facts):
    """Deduplicate overlapping chunks, retain evidence and surface conflicting claims."""
    merged = {}
    for fact in facts:
        key = (name_key(fact["key"]), name_key(fact["text"]))
        if key not in merged:
            merged[key] = dict(fact, evidence=list(fact["evidence"]))
        else:
            known = {(e["source_id"], e["start"], e["end"]) for e in merged[key]["evidence"]}
            merged[key]["evidence"].extend(
                e for e in fact["evidence"] if (e["source_id"], e["start"], e["end"]) not in known
            )
    groups = defaultdict(list)
    for (key, _), fact in merged.items():
        groups[key].append(fact)
    for group in groups.values():
        if len(group) > 1:
            for fact in group:
                if fact["status"] == "current":
                    # A new statement alone is insufficient proof that the old one is false.
                    fact["status"] = "disputed"
        for fact in group:
            if fact.get("valid_until") and utc(fact["valid_until"]) < utc():
                fact["status"] = "superseded"
    return sorted(
        merged.values(),
        key=lambda fact: (
            fact.get("occurred_at") or fact["source_created_at"],
            fact["key"],
            fact["text"],
        ),
    )
