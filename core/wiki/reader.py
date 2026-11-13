# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot

"""Public read model. Never serialize database rows directly to anonymous callers."""

from datetime import timedelta
from difflib import SequenceMatcher
from functools import wraps
from itertools import zip_longest

from .cursor import read_cursor, scope_key, write_cursor
from .models import utc
from .schema import CHANNEL_VISIBLE, REVISION_VISIBLE
from .store import decode

VISIBLE = f"""{CHANNEL_VISIBLE} AND {REVISION_VISIBLE}
    AND NOT JSON_CONTAINS(JSON_EXTRACT(c.config,'$.hidden_topics'),JSON_QUOTE(p.topic))"""
PAGE_JOIN = """FROM wiki_channels c JOIN wiki_publication_pages pp ON pp.publication_id=c.published_revision
    JOIN wiki_pages p ON p.id=pp.page_id AND p.channel_id=c.id
    JOIN wiki_page_revisions r ON r.id=pp.revision_id AND r.channel_id=c.id"""
CARD_FIELDS = """p.id,p.slug,p.kind,p.topic,c.id channel_id,c.title channel_title,
    c.published_revision,r.id revision_id,r.created_at,
    JSON_UNQUOTE(JSON_EXTRACT(r.document,'$.title')) title,
    LEFT(JSON_UNQUOTE(JSON_EXTRACT(r.document,'$.claims[0].text')),240) excerpt"""
NODE_FIELDS = """p.id,p.slug,p.kind,p.topic,c.id channel_id,
    JSON_UNQUOTE(JSON_EXTRACT(r.document,'$.title')) title,
    r.id revision_id,c.published_revision publication_id"""
EVIDENCE_COUNT = """(SELECT COUNT(DISTINCT ev.source_id) FROM wiki_evidence ev
    WHERE ev.revision_id=r.id) evidence_count"""
GRAPH_EDGE_JOIN = """FROM wiki_publication_edges pe JOIN wiki_edges e ON e.id=pe.edge_id
    JOIN wiki_chunks chunk ON chunk.id=e.chunk_id JOIN wiki_sources s ON s.id=chunk.source_id"""
GRAPH_EDGE_VISIBLE = """s.deleted=FALSE AND s.hidden=FALSE AND s.excluded=FALSE
    AND s.verified=TRUE AND s.content_hash=e.source_hash"""
LATEST_JOIN = """FROM wiki_page_revisions r FORCE INDEX (wiki_feed_latest)
    STRAIGHT_JOIN wiki_pages p ON p.id=r.page_id AND p.channel_id=r.channel_id
    STRAIGHT_JOIN wiki_channels c ON c.id=r.channel_id
    STRAIGHT_JOIN wiki_publication_pages pp ON pp.publication_id=c.published_revision
        AND pp.page_id=p.id AND pp.revision_id=r.id"""
PUBLICATION_FEED_JOIN = """FROM wiki_channels c
    STRAIGHT_JOIN wiki_publications pub ON pub.id=c.published_revision
    STRAIGHT_JOIN wiki_publication_pages pp ON pp.publication_id=pub.id
    STRAIGHT_JOIN wiki_pages p ON p.id=pp.page_id AND p.channel_id=c.id
    STRAIGHT_JOIN wiki_page_revisions r ON r.id=pp.revision_id AND r.channel_id=c.id"""
PUBLISHED_HISTORY = f"""FROM wiki_page_revisions r JOIN wiki_pages p ON p.id=r.page_id
    WHERE p.id=%s AND r.channel_id=p.channel_id AND {REVISION_VISIBLE}
    AND EXISTS (SELECT 1 FROM wiki_publication_pages pp
        JOIN wiki_publications pub ON pub.id=pp.publication_id
        WHERE pp.page_id=p.id AND pp.revision_id=r.id AND pub.channel_id=p.channel_id)"""


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

    async def visibility(self):
        rows = await self.store.query(
            f"""SELECT c.id,c.published_revision,c.source_generation,cover.digest cover_digest
            FROM wiki_channels c LEFT JOIN wiki_channel_covers cover ON cover.channel_id=c.id
            WHERE {CHANNEL_VISIBLE} ORDER BY c.id""",
            fetch=True,
        )
        return {
            row[
                "id"
            ]: f"{row['published_revision']}:{row['source_generation']}:{row['cover_digest'] or ''}"
            for row in rows
        }

    async def bookmarks(self, page_ids):
        ordered = list(dict.fromkeys(page_ids))
        if not ordered:
            return []
        placeholders = ",".join(["%s"] * len(ordered))
        rows = await self.store.query(
            f"""SELECT {CARD_FIELDS}, {EVIDENCE_COUNT} {PAGE_JOIN}
            WHERE p.id IN ({placeholders}) AND {VISIBLE}""",
            ordered,
            fetch=True,
        )
        cards = {row["id"]: page_card(row) for row in rows}
        return [cards[page_id] for page_id in ordered if page_id in cards]

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
    async def channel(self, channel, *, summary=False):
        ch = await self.store.channel(channel)
        if not ch:
            return None
        row = await self.store.one(
            f"SELECT c.id FROM wiki_channels c WHERE c.id=%s AND {CHANNEL_VISIBLE}", (ch["id"],)
        )
        if not row:
            return None
        result = {
            "id": ch["id"],
            "title": ch["title"],
            "username": ch["username"],
            "publication_id": ch["published_revision"],
            "updated_at": ch["updated_at"],
            "coverage": public_coverage(ch["coverage"]),
            "columns": ch["config"].columns,
            "cover_url": (
                f"/api/public/wiki/channels/{ch['id']}/cover?v={ch['cover_digest']}"
                if ch.get("cover_digest")
                else ch["config"].cover_url or None
            ),
            "description": ch["config"].description,
            "source_policy": {
                "quotes": ch["config"].show_quotes,
                "telegram_links": ch["config"].show_telegram_links,
            },
        }
        if summary:
            count = await self.store.one(
                f"SELECT COUNT(*) total {PUBLICATION_FEED_JOIN} WHERE c.id=%s AND {VISIBLE}",
                (ch["id"],),
            )
            topics = await self.store.query(
                f"""SELECT p.topic,COUNT(*) total {PUBLICATION_FEED_JOIN}
                WHERE c.id=%s AND {VISIBLE} GROUP BY p.topic ORDER BY total DESC,p.topic LIMIT 8""",
                (ch["id"],),
                fetch=True,
            )
            return {
                **result,
                "page_count": count["total"],
                "topics": [row["topic"] for row in topics],
            }
        pages = await self.search(channel=ch["id"], page_size=12)
        return {**result, "pages": pages["items"], "page_count": pages["total"]}

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
        conditions, params, terms, ranking, rank_params = await self._filters(
            q, channel, kind, topic, after, before
        )
        where = " AND ".join(conditions)
        order = (f"({ranking}) DESC," if ranking else "") + "r.created_at DESC,p.id"
        count = await self.store.one(f"SELECT COUNT(*) total {PAGE_JOIN} WHERE {where}", params)
        rows = await self.store.query(
            f"""SELECT p.id,p.slug,p.title,p.kind,p.topic,c.id channel_id,c.title channel_title,
            c.published_revision,r.id revision_id,r.created_at,r.document {PAGE_JOIN} WHERE {where}
            ORDER BY {order} LIMIT %s OFFSET %s""",
            [*params, *rank_params, page_size, (page - 1) * page_size],
            fetch=True,
        )
        return {
            "items": [page_card(row) for row in rows],
            "total": count["total"],
            "page": page,
            "page_size": page_size,
            "terms": terms,
        }

    async def _filters(self, q, channel, kind, topic, after, before):
        resolved = await self.store.channel(channel) if channel else None
        return self._page_filters(
            q,
            resolved["id"] if resolved else None,
            kind,
            topic,
            after,
            before,
            has_channel=bool(channel),
        )

    @staticmethod
    def _page_filters(q, channel, kind, topic, after, before, *, has_channel=False):
        after, before = utc(after) if after else None, utc(before) if before else None
        conditions, params = [VISIBLE], []
        if has_channel:
            conditions.append("c.id=%s")
            params.append(channel)
        terms = list(dict.fromkeys((q or "").split()))
        ranking, rank_params = [], []
        if terms:
            matches = []
            for term in terms:
                matches.append(
                    "(r.search_text LIKE %s ESCAPE '=' OR EXISTS (SELECT 1 FROM wiki_entity_aliases a WHERE a.page_id=p.id AND a.name LIKE %s ESCAPE '='))"
                )
                params.extend([like(term), like(term)])
                ranking.append(
                    "(CASE WHEN p.title LIKE %s ESCAPE '=' THEN 4 ELSE 0 END + CASE WHEN r.search_text LIKE %s ESCAPE '=' THEN 1 ELSE 0 END)"
                )
                rank_params.extend([like(term), like(term)])
            conditions.append("(" + " OR ".join(matches) + ")")
        for column, value, operator in [
            ("p.kind", kind, "="),
            ("p.topic", topic, "="),
            ("r.created_at", after, ">="),
            ("r.created_at", before, "<="),
        ]:
            if value:
                conditions.append(f"{column}{operator}%s")
                params.append(value)
        return conditions, params, terms, "+".join(ranking), rank_params

    @consistent_read
    async def feed(
        self,
        *,
        mode="latest",
        channel=None,
        topic=None,
        kind=None,
        q="",
        after=None,
        before=None,
        cursor=None,
        limit=20,
    ):
        if channel:
            visible = await self.visible_channel(channel)
            if not visible:
                return None
            channel = visible["id"]
        scope = scope_key("feed", mode, channel, topic, kind, q, after, before)
        shuffle = mode == "recommended"
        start, seed, position = read_cursor(cursor, scope, shuffle=shuffle)
        conditions, params, terms, ranking, rank_params = self._page_filters(
            q, channel, kind, topic, after, before, has_channel=bool(channel)
        )
        if shuffle:
            # Rotate the order of channel/topic groups, then the order inside each group.
            # A session has a fixed order, while a new session gets a fresh seed.
            ranking = """(CASE WHEN r.created_at >= %s THEN 200 ELSE 0 END
                + MOD(CRC32(CONCAT(%s, ':', c.id, ':', p.topic)) +
                  MOD(CRC32(CONCAT(%s, ':', p.id)), 97), 127))"""
            rank_params = [start - timedelta(days=7), seed, seed]
        sort = f"CAST(({ranking}) AS SIGNED)" if ranking else "0"
        order = "sort_rank DESC," if ranking else ""
        indexed = not (ranking or channel or topic or kind)
        join = LATEST_JOIN if indexed else PUBLICATION_FEED_JOIN
        # Keep publications created after the first page out of its continuation.
        if indexed:
            conditions.append("""(SELECT pub.created_at FROM wiki_publications pub
                WHERE pub.id=c.published_revision)<=%s""")
        else:
            conditions.append("pub.created_at<=%s")
        params.append(start)
        keyset = ""
        if position:
            rank, date, identity = position
            if ranking:
                keyset = "HAVING sort_rank<%s OR (sort_rank=%s AND (created_at,id)<(%s,%s))"
                params.extend([rank, rank, date, identity])
            else:
                conditions.append("(r.created_at<%s OR (r.created_at=%s AND r.page_id<%s))")
                params.extend([date, date, identity])
        where = " AND ".join(conditions)
        rows = await self.store.query(
            f"""SELECT {CARD_FIELDS}, batch.sort_rank, {EVIDENCE_COUNT}
            FROM (
              SELECT c.id channel_id,p.id,r.id revision_id,r.created_at,{sort} sort_rank
              {join}
              WHERE {where} {keyset} ORDER BY {order}r.created_at DESC,r.page_id DESC LIMIT %s
            ) batch
            JOIN wiki_channels c ON c.id=batch.channel_id
            JOIN wiki_pages p ON p.id=batch.id
            JOIN wiki_page_revisions r ON r.id=batch.revision_id
            ORDER BY batch.sort_rank DESC,r.created_at DESC,p.id DESC""",
            [*rank_params, *params, limit + 1],
            fetch=True,
        )
        has_more = len(rows) > limit
        rows = rows[:limit]
        next_cursor = None
        if has_more:
            last = rows[-1]
            next_cursor = write_cursor(
                scope, start, seed, (last["sort_rank"], last["created_at"], last["id"])
            )
        return {
            "items": [page_card(row) for row in rows],
            "has_more": has_more,
            "next_cursor": next_cursor,
            "terms": terms,
        }

    async def visible_channel(self, channel):
        ch = await self.store.channel(channel)
        return ch and await self.store.one(
            f"SELECT c.id FROM wiki_channels c WHERE c.id=%s AND {CHANNEL_VISIBLE}", (ch["id"],)
        )

    @consistent_read
    async def page(self, channel, slug, *, part="full", compare=None, revision=None):
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
        if compare:
            return await self.compare_revisions(row["id"], *compare)
        current_revision = row["revision_id"]
        historical = bool(revision and revision != current_revision)
        if historical:
            version = await self.store.one(
                f"SELECT r.id revision_id,r.created_at,r.document {PUBLISHED_HISTORY} AND r.id=%s",
                (row["id"], revision),
            )
            if not version:
                return None
            row.update(version)
        config = decode(row["config"])
        evidence_rows = await self.store.query(
            """SELECT DISTINCT s.id,s.source_type,s.message_id,s.created_at,s.edited_at
            FROM wiki_evidence e JOIN wiki_sources s ON s.id=e.source_id WHERE e.revision_id=%s
            ORDER BY s.created_at DESC,s.id LIMIT 500""",
            (row["revision_id"],),
            fetch=True,
        )
        evidence = {r["id"]: r for r in evidence_rows}
        result = page_card(row)
        doc = decode(row["document"])
        result["verified_at"] = doc["verified_at"]
        result["current_revision_id"] = current_revision
        result["proposals_enabled"] = not historical and bool(config.get("proposals_enabled"))
        result["annotations_enabled"] = not historical and bool(config.get("annotations_enabled"))
        if part == "article":
            total = await self.store.one(f"SELECT COUNT(*) total {PUBLISHED_HISTORY}", (row["id"],))
            result["history_total"] = total["total"]
        result["claims"] = [
            public_claim(fact, evidence, row["username"], config) for fact in doc["claims"]
        ]
        if part != "references" and doc.get("community_contributions"):
            await self.credit_annotations(
                row["id"], doc["community_contributions"], doc["claims"], result["claims"]
            )
        review = await self.store.one(
            """SELECT result FROM wiki_revision_reviews WHERE revision_id=%s AND approved=TRUE
            ORDER BY created_at DESC LIMIT 1""",
            (row["revision_id"],),
        )
        if review:
            for check in decode(review["result"]).get("fact_checks", []):
                if 0 <= check["index"] < len(result["claims"]):
                    result["claims"][check["index"]]["fact_check"] = check
        if part == "article":
            return result
        sources_total = len(evidence_rows)
        if len(evidence_rows) == 500:
            total = await self.store.one(
                """SELECT COUNT(DISTINCT s.id) total FROM wiki_evidence e
                JOIN wiki_sources s ON s.id=e.source_id WHERE e.revision_id=%s""",
                (row["revision_id"],),
            )
            sources_total = total["total"]
        result["sources"] = [
            source_card(source, row["username"], config) for source in evidence_rows
        ]
        # 总量独立于上方 500 条截断，前端据此提示"未全部展示"。
        result["sources_total"] = sources_total
        # Historical metadata is restricted to revisions that were actually published.
        history = await self.store.query(
            f"""SELECT r.id,r.created_at {PUBLISHED_HISTORY}
            ORDER BY r.id=%s DESC,r.created_at DESC,r.id DESC LIMIT 50""",
            (row["id"], current_revision),
            fetch=True,
        )
        history_total = len(history)
        if len(history) == 50:
            total = await self.store.one(
                f"SELECT COUNT(*) total {PUBLISHED_HISTORY}",
                (row["id"],),
            )
            history_total = total["total"]
        result["history"] = [
            {"revision_id": r["id"], "created_at": r["created_at"]} for r in history
        ]
        result["history_total"] = history_total
        # Recheck the same snapshot and visibility after multi-query assembly.
        valid = await self.store.one(
            f"""SELECT r.id {PAGE_JOIN} WHERE c.id=%s AND c.published_revision=%s
            AND r.id=%s AND {VISIBLE}""",
            (ch["id"], row["published_revision"], current_revision),
        )
        if not valid:
            return None
        if part == "references":
            return {
                key: result[key]
                for key in (
                    "revision_id",
                    "current_revision_id",
                    "publication_id",
                    "sources",
                    "sources_total",
                    "history",
                    "history_total",
                )
            }
        return result

    async def credit_annotations(self, page_id, proposal_ids, raw_claims, claims):
        if not any(claim["community_notes"] for claim in claims):
            return
        marks = ",".join(["%s"] * len(proposal_ids))
        rows = await self.store.query(
            f"""SELECT e.id,u.id contributor_id,u.display_name,
            IF(u.show_username,u.username,NULL) username,
            (u.show_avatar AND u.avatar IS NOT NULL) avatar
            FROM wiki_contributions x JOIN wiki_edit_proposals e ON e.id=x.proposal_id
            JOIN wiki_users u ON u.id=e.user_id
            WHERE e.page_id=%s AND e.id IN ({marks}) AND e.status='approved'
            AND u.blocked=FALSE AND u.anonymous_contributions=FALSE""",
            (page_id, *proposal_ids),
            fetch=True,
        )
        by_id = {row["id"]: row for row in rows}
        # Older annotations recorded only their text. Resolve a legacy match only if it is unique,
        # including anonymous proposals in the match so a public author cannot inherit their credit.
        legacy = []
        if any(
            not note.get("proposal_id")
            for claim in raw_claims
            for note in claim.get("community_notes", [])
        ):
            legacy = await self.store.query(
                f"""SELECT id,kind,payload FROM wiki_edit_proposals
                WHERE page_id=%s AND id IN ({marks}) AND status='approved'""",
                (page_id, *proposal_ids),
                fetch=True,
            )
            for entry in legacy:
                entry["payload"] = decode(entry["payload"])
        for ordinal, claim in enumerate(claims):
            for note, public_note in zip(
                raw_claims[ordinal].get("community_notes", []),
                claim["community_notes"],
                strict=True,
            ):
                proposal = note.get("proposal_id")
                if not proposal:
                    matches = [
                        entry["id"]
                        for entry in legacy
                        if entry["kind"] == note["kind"]
                        and entry["payload"].get("claim") == ordinal
                        and all(
                            entry["payload"].get(key) == note.get(key)
                            for key in ("text", "sources")
                        )
                        and (entry["payload"].get("excerpt") or "") == (note.get("excerpt") or "")
                    ]
                    proposal = matches[0] if len(matches) == 1 else None
                row = by_id.get(proposal)
                if row:
                    public_note["contributor"] = {
                        "id": row["contributor_id"],
                        "display_name": row["display_name"],
                        "username": row["username"],
                        "avatar": bool(row["avatar"]),
                    }

    async def compare_revisions(self, page_id, before, after):
        rows = await self.store.query(
            f"SELECT r.id,r.created_at,r.document {PUBLISHED_HISTORY} AND r.id IN (%s,%s)",
            (page_id, before, after),
            fetch=True,
        )
        versions = {row["id"]: row for row in rows}
        if before not in versions or after not in versions:
            return None

        def blocks(row):
            document = decode(row["document"])
            result = [document["title"], document["topic"]]
            for claim in document["claims"]:
                result.append(claim["text"])
                for note in claim.get("community_notes", []):
                    excerpt = f"「{note['excerpt']}」" if note.get("excerpt") else ""
                    result.append("\n".join([f"注解{excerpt}　{note['text']}", *note["sources"]]))
            return result

        old, new = blocks(versions[before]), blocks(versions[after])
        changes = []
        truncated = False
        for tag, start, end, new_start, new_end in SequenceMatcher(None, old, new).get_opcodes():
            if tag == "equal":
                continue
            for removed, added in zip_longest(old[start:end], new[new_start:new_end]):
                if len(changes) == 100:
                    truncated = True
                    break
                changes.append({"before": removed, "after": added})
            if truncated:
                break
        return {
            "before": {key: versions[before][key] for key in ("id", "created_at")},
            "after": {key: versions[after][key] for key in ("id", "created_at")},
            "changes": changes,
            "truncated": truncated,
        }

    @consistent_read
    async def related(self, channel, slug):
        ch = await self.store.channel(channel)
        if not ch:
            return None
        current = await self.store.one(
            f"""SELECT p.id,p.topic,r.created_at {PAGE_JOIN}
            WHERE c.id=%s AND (p.slug=%s OR p.id=%s OR EXISTS (
              SELECT 1 FROM wiki_slug_aliases a WHERE a.channel_id=c.id AND a.page_id=p.id
              AND a.slug=%s)) AND {VISIBLE}""",
            (ch["id"], slug, slug, slug),
        )
        if not current:
            return None
        topic = current["topic"]
        output = {}
        for label, operator, sort in [("previous", "<", "DESC"), ("next", ">", "ASC")]:
            row = await self.store.one(
                f"""SELECT {NODE_FIELDS} {PAGE_JOIN}
                WHERE c.id=%s AND p.topic=%s AND {VISIBLE}
                AND (r.created_at,p.id){operator}(%s,%s)
                ORDER BY r.created_at {sort},p.id {sort} LIMIT 1""",
                (ch["id"], topic, current["created_at"], current["id"]),
            )
            output[label] = graph_node(row) if row else None
        return output

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
            "title": ch["title"],
            "publication_id": row["id"],
            "published_at": row["created_at"],
            "coverage": public_coverage(ch["coverage"]),
        }

    @consistent_read
    async def changes(self, channel, page=1, page_size=20):
        # Published, still-authorized page changes only. Audit logs and drafts are separate.
        return await self.search(channel=channel, page=page, page_size=page_size)

    @consistent_read
    async def timeline(
        self, channel, page=1, page_size=20, *, cursor_mode=False, cursor=None, before=None
    ):
        ch = await self.store.channel(channel)
        empty = {"items": [], "page": page, "page_size": page_size, "total_pages": 0}
        if not ch:
            return None if cursor_mode else empty
        if cursor_mode and not await self.store.one(
            f"SELECT c.id FROM wiki_channels c WHERE c.id=%s AND {CHANNEL_VISIBLE}", (ch["id"],)
        ):
            return None
        join = PAGE_JOIN + " JOIN wiki_revision_claims wc ON wc.revision_id=r.id "
        event_time = "COALESCE(wc.occurred_at,wc.source_created_at)"
        where = f"c.id=%s AND {VISIBLE}"
        params = [ch["id"]]
        if cursor_mode:
            scope = scope_key("timeline", ch["id"], ch["published_revision"], before)
            start, seed, position = read_cursor(cursor, scope)
            if before:
                where += f" AND {event_time}<=%s"
                params.append(utc(before))
            if position:
                ordinal, date, revision = position
                where += f""" AND ({event_time}<%s OR
                    ({event_time}=%s AND (r.id,wc.ordinal)>(%s,%s)))"""
                params.extend([date, date, revision, ordinal])
            pagination = "LIMIT %s"
            params.append(page_size + 1)
        else:
            total = await self.store.one(f"SELECT COUNT(*) n {join} WHERE {where}", params)
            pagination = "LIMIT %s OFFSET %s"
            params.extend([page_size, (page - 1) * page_size])
        rows = await self.store.query(
            f"""SELECT p.id,p.slug,c.id channel_id,c.username,c.config,c.published_revision,
            JSON_UNQUOTE(JSON_EXTRACT(r.document,'$.title')) title,wc.payload,
            r.id revision_id,wc.ordinal,{event_time} event_time {join}
            WHERE {where} ORDER BY event_time DESC,r.id,wc.ordinal {pagination}""",
            params,
            fetch=True,
        )
        has_more = cursor_mode and len(rows) > page_size
        rows = rows[:page_size]
        # 按断言合并引用，再分批读取来源；避免每个条目反复读取整页及历史。
        source_ids = sorted(
            {e["source_id"] for row in rows for e in decode(row["payload"])["evidence"]}
        )
        sources = {}
        for offset in range(0, len(source_ids), 500):
            batch = source_ids[offset : offset + 500]
            placeholders = ",".join(["%s"] * len(batch))
            sources.update(
                {
                    source["id"]: source
                    for source in await self.store.query(
                        f"""SELECT id,source_type,message_id,created_at,edited_at FROM wiki_sources
                    WHERE channel_id=%s AND id IN ({placeholders})""",
                        [ch["id"], *batch],
                        fetch=True,
                    )
                }
            )
        events = []
        for row in rows:
            events.append(
                {
                    "page_id": row["id"],
                    "title": row["title"],
                    "url": f"/wiki/{row['channel_id']}/page/{row['slug']}",
                    "publication_id": row["published_revision"],
                    "event_id": f"{row['revision_id']}:{row['ordinal']}",
                    **public_claim(
                        decode(row["payload"]), sources, row["username"], decode(row["config"])
                    ),
                }
            )
        if cursor_mode:
            next_cursor = None
            if has_more:
                last = rows[-1]
                next_cursor = write_cursor(
                    scope, start, seed, (last["ordinal"], last["event_time"], last["revision_id"])
                )
            return {
                "items": events,
                "has_more": has_more,
                "next_cursor": next_cursor,
                "publication_id": ch["published_revision"],
            }
        return {
            "items": events,
            "page": page,
            "page_size": page_size,
            "total_pages": (total["n"] + page_size - 1) // page_size,
        }

    @consistent_read
    async def browse_graph(
        self,
        channel,
        node_id=None,
        *,
        cursor=None,
        limit=40,
        kind=None,
        topic=None,
        relation=None,
        q="",
        after=None,
        before=None,
    ):
        ch = await self.store.channel(channel)
        if not ch or not await self.visible_channel(ch["id"]):
            return None
        publication = ch["published_revision"]
        limit = max(1, min(limit, 50))
        after, before = utc(after) if after else None, utc(before) if before else None
        scope = scope_key(
            "graph", ch["id"], publication, node_id, limit, kind, topic, relation, q, after, before
        )
        start, _, position = read_cursor(cursor, scope)
        edge_conditions, edge_params = (
            ["pe.publication_id=%s", "e.channel_id=%s", GRAPH_EDGE_VISIBLE],
            [publication, ch["id"]],
        )
        for clause, value in [
            ("e.kind=%s", relation),
            ("s.created_at>=%s", after),
            ("s.created_at<=%s", before),
        ]:
            if value is not None:
                edge_conditions.append(clause)
                edge_params.append(value)
        edge_where = " AND ".join(edge_conditions)
        conditions, params, _, ranking, rank_params = self._page_filters(
            q,
            ch["id"],
            kind,
            topic,
            after if not node_id else None,
            before if not node_id else None,
            has_channel=True,
        )
        root = None
        if node_id:
            root = await self.store.one(
                f"SELECT {NODE_FIELDS} {PAGE_JOIN} WHERE c.id=%s AND p.id=%s AND {VISIBLE}",
                (ch["id"], node_id),
            )
            if not root:
                return None
        if node_id or relation:
            conditions.append(
                f"""EXISTS (SELECT 1 {GRAPH_EDGE_JOIN}
                WHERE {edge_where} AND ((e.source_page=p.id AND (%s IS NULL OR e.target_page=%s))
                    OR (e.target_page=p.id AND (%s IS NULL OR e.source_page=%s))))"""
            )
            params.extend([*edge_params, node_id, node_id, node_id, node_id])
        if node_id:
            conditions.append("p.id<>%s")
            params.append(node_id)
        sort = f"CAST(({ranking}) AS SIGNED)" if ranking else "0"
        continuation = ""
        if position:
            rank, date, identity = position
            continuation = "HAVING sort_rank<%s OR (sort_rank=%s AND (created_at,id)<(%s,%s))"
            params.extend([rank, rank, date, identity])
        where = " AND ".join(conditions)
        rows = await self.store.query(
            f"""SELECT {NODE_FIELDS},r.created_at,{sort} sort_rank
            {PAGE_JOIN} WHERE {where} {continuation}
            ORDER BY sort_rank DESC,r.created_at DESC,p.id DESC LIMIT %s""",
            [*rank_params, *params, limit + 1],
            fetch=True,
        )
        has_more = len(rows) > limit
        rows = rows[:limit]
        next_cursor = None
        if has_more:
            last = rows[-1]
            next_cursor = write_cursor(
                scope, start, "", (last["sort_rank"], last["created_at"], last["id"])
            )
        nodes = [graph_node(row) for row in ([root] if root else []) + rows]
        edges = []
        if nodes:
            ids = [node["id"] for node in nodes]
            marks = ",".join(["%s"] * len(ids))
            # Combine parallel evidence for the same directed relation so it draws one edge.
            edges = await self.store.query(
                f"""SELECT MIN(e.id) id,e.source_page source,e.target_page target,e.kind,
                MAX(JSON_EXTRACT(e.relation_json,'$.confidence')+0) confidence,
                SUM(JSON_LENGTH(e.relation_json,'$.evidence')) evidence_count
                {GRAPH_EDGE_JOIN} WHERE {edge_where}
                AND e.source_page IN ({marks}) AND e.target_page IN ({marks})
                GROUP BY e.source_page,e.target_page,e.kind ORDER BY id""",
                [*edge_params, *ids, *ids],
                fetch=True,
            )
        return {
            "nodes": nodes,
            "edges": edges,
            "publication_id": publication,
            "limit": limit,
            "truncated": has_more,
            "next_cursor": next_cursor,
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
        visible = await self.store.one(
            f"SELECT c.id FROM wiki_channels c WHERE c.id=%s AND {CHANNEL_VISIBLE}", (ch["id"],)
        )
        if not visible:
            return None
        limit, depth = max(1, min(limit, 120)), max(1, min(depth, 2))
        after, before = utc(after) if after else None, utc(before) if before else None
        if node_id:
            root = await self.store.one(
                f"""SELECT {NODE_FIELDS} {PAGE_JOIN}
                WHERE c.id=%s AND (p.id=%s OR p.slug=%s OR EXISTS (
                  SELECT 1 FROM wiki_slug_aliases a WHERE a.channel_id=c.id
                    AND a.page_id=p.id AND a.slug=%s)) AND {VISIBLE} LIMIT 1""",
                (ch["id"], node_id, node_id, node_id),
            )
            if not root:
                return None
            nodes = {root["id"]: graph_node(root)}
        else:
            initial = await self.feed(
                q=q,
                channel=ch["id"],
                kind=kind,
                topic=topic,
                limit=min(20, limit),
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
            candidates = sorted(
                {row[key] for row in rows for key in ("source_page", "target_page")} - nodes.keys()
            )
            neighbours = {}
            if candidates:
                marks = ",".join(["%s"] * len(candidates))
                conditions, params, *_ = self._page_filters("", None, kind, topic, None, None)
                where = " AND ".join(conditions)
                valid_nodes = await self.store.query(
                    f"""SELECT {NODE_FIELDS} {PAGE_JOIN}
                    WHERE c.id=%s AND p.id IN ({marks}) AND {where}""",
                    [ch["id"], *candidates, *params],
                    fetch=True,
                )
                neighbours = {row["id"]: graph_node(row) for row in valid_nodes}
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
                        page_row = neighbours.get(page_id)
                        if not page_row:
                            valid = False
                            break
                        nodes[page_id] = page_row
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


def graph_node(row):
    return {
        **{
            key: row[key]
            for key in ("id", "title", "kind", "topic", "revision_id", "publication_id")
        },
        "url": f"/wiki/{row['channel_id']}/page/{row['slug']}",
    }


def page_card(row):
    if "document" in row:
        doc = decode(row["document"])
        claims = doc.get("claims", [])
        title = doc["title"]
        excerpt = claims[0]["text"][:240] if claims else ""
        evidence_count = len({e["source_id"] for f in claims for e in f["evidence"]})
    else:
        title, excerpt, evidence_count = row["title"], row["excerpt"] or "", row["evidence_count"]
    return {
        "id": row["id"],
        "slug": row["slug"],
        "title": title,
        "kind": row["kind"],
        "topic": row["topic"],
        "channel_id": row["channel_id"],
        "channel_title": row["channel_title"],
        "publication_id": row["published_revision"],
        "revision_id": row["revision_id"],
        "updated_at": row["created_at"],
        "excerpt": excerpt,
        "evidence_count": evidence_count,
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
        "community_notes": [
            {
                key: note[key]
                for key in ("kind", "text", "sources", "excerpt", "excerpt_occurrence")
                if key in note
            }
            for note in fact.get("community_notes", [])
        ],
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
