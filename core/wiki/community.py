import difflib
import uuid
from typing import Literal

from pydantic import Field, field_validator

from .models import Strict
from .reader import VISIBLE
from .schema import REVISION_VISIBLE
from .store import decode, encode
from .web_research import public_url


class Proposal(Strict):
    revision: str = Field(pattern=r"^[a-f0-9]{32}$")
    kind: Literal["edit", "correction", "annotation", "source", "dispute", "outdated", "fact_check"]
    claim: int = Field(ge=0, le=10000, strict=True)
    text: str = Field(min_length=1, max_length=10000)
    sources: list[str] = Field(default_factory=list, max_length=5)

    @field_validator("text")
    @classmethod
    def nonempty(cls, value):
        value = value.strip()
        if not value or "\x00" in value:
            raise ValueError("内容不能为空")
        return value

    @field_validator("sources")
    @classmethod
    def urls(cls, values):
        return list(dict.fromkeys(public_url(value) for value in values))


class Community:
    def __init__(self, store, knowledge):
        self.store, self.knowledge = store, knowledge

    async def submit(self, user, channel, page, proposal):
        if proposal.kind in {"source", "fact_check"} and not proposal.sources:
            raise ValueError("请提供来源链接")
        async with self.store.transaction() as cur:
            await cur.execute("SELECT config FROM wiki_channels WHERE id=%s FOR UPDATE", (channel,))
            row = await cur.fetchone()
            config = decode(row["config"]) if row else {}
            if not config.get("proposals_enabled") or (
                proposal.kind not in {"edit", "correction"}
                and not config.get("annotations_enabled")
            ):
                raise ValueError("此频道尚未开放该类投稿")
            await cur.execute("SELECT blocked FROM wiki_users WHERE id=%s FOR UPDATE", (user,))
            account = await cur.fetchone()
            if not account or account["blocked"]:
                raise ValueError("账户不可投稿")
            await cur.execute(
                "SELECT COUNT(*) total FROM wiki_edit_proposals WHERE user_id=%s AND created_at>UTC_TIMESTAMP(6)-INTERVAL 1 DAY",
                (user,),
            )
            if (await cur.fetchone())["total"] >= 10:
                raise ValueError("今天的投稿次数已用完")
            await cur.execute(
                f"""SELECT r.document FROM wiki_channels c
                JOIN wiki_publication_pages pp ON pp.publication_id=c.published_revision
                JOIN wiki_pages p ON p.id=pp.page_id JOIN wiki_page_revisions r ON r.id=pp.revision_id
                WHERE c.id=%s AND p.id=%s AND r.id=%s AND {VISIBLE}""",
                (channel, page, proposal.revision),
            )
            current = await cur.fetchone()
            if not current:
                raise ValueError("当前版本已变化或不可读取")
            claims = decode(current["document"])["claims"]
            if proposal.claim >= len(claims):
                raise ValueError("找不到所选段落")
            original = claims[proposal.claim]["text"]
            if proposal.kind in {"edit", "correction"} and proposal.text == original:
                raise ValueError("修订内容没有变化")
            changes = (
                list(
                    difflib.unified_diff(
                        original.splitlines(),
                        proposal.text.splitlines(),
                        fromfile="published",
                        tofile="proposed",
                        lineterm="",
                    )
                )
                if proposal.kind in {"edit", "correction"}
                else []
            )
            identifier = uuid.uuid4().hex
            await cur.execute(
                """INSERT INTO wiki_edit_proposals
                (id,user_id,channel_id,page_id,base_revision,kind,payload,status,created_at,updated_at)
                VALUES (%s,%s,%s,%s,%s,%s,%s,'pending',UTC_TIMESTAMP(6),UTC_TIMESTAMP(6))""",
                (
                    identifier,
                    user,
                    channel,
                    page,
                    proposal.revision,
                    proposal.kind,
                    encode({**proposal.model_dump(), "diff": "\n".join(changes)}),
                ),
            )
            return identifier, config.get("proposal_notifications", False)

    async def decide(self, identifier, action, actor, note="", *, telegram=False):
        if action not in {"approved", "rejected", "returned"}:
            raise ValueError("审核操作无效")
        proposal = await self.store.one(
            "SELECT channel_id FROM wiki_edit_proposals WHERE id=%s", (identifier,)
        )
        if not proposal:
            raise ValueError("投稿不存在")
        channel = proposal["channel_id"]
        revision = None
        job = None
        async with self.store.transaction() as cur:
            await cur.execute("SELECT * FROM wiki_channels WHERE id=%s FOR UPDATE", (channel,))
            ch = await cur.fetchone()
            await cur.execute(
                "SELECT * FROM wiki_edit_proposals WHERE id=%s FOR UPDATE", (identifier,)
            )
            row = await cur.fetchone()
            if not ch or not row or row["status"] != "pending":
                raise ValueError("投稿已处理")
            if telegram and (await self._expired(cur, identifier)):
                raise ValueError("按钮已过期，请在 WebUI 处理")
            if action == "approved":
                if not ch["enabled"] or ch["active_job"]:
                    raise ValueError("请等待当前 Wiki 任务结束后再批准")
                await cur.execute(
                    "SELECT blocked FROM wiki_users WHERE id=%s FOR UPDATE", (row["user_id"],)
                )
                author = await cur.fetchone()
                if not author or author["blocked"]:
                    raise ValueError("投稿账户不可用")
                await cur.execute(
                    f"""SELECT r.id FROM wiki_publication_pages pp
                    JOIN wiki_page_revisions r ON r.id=pp.revision_id JOIN wiki_pages p ON p.id=pp.page_id
                    WHERE pp.publication_id=%s AND pp.page_id=%s AND {REVISION_VISIBLE}
                    AND NOT EXISTS (SELECT 1 FROM wiki_edit_proposals other WHERE other.page_id=p.id
                        AND other.base_revision=r.id AND other.status='approved')""",
                    (ch["published_revision"], row["page_id"]),
                )
                base = await cur.fetchone()
                if not base or base["id"] != row["base_revision"]:
                    raise ValueError("发布基线已变化，请退回后重新投稿")
                data = decode(row["payload"])
                annotation = None
                if row["kind"] not in {"edit", "correction"} or data["sources"]:
                    annotation = {
                        "kind": row["kind"],
                        "text": data["text"],
                        "sources": data["sources"],
                    }
                revision = await self.knowledge.create_correction(
                    cur,
                    channel,
                    base["id"],
                    data["claim"],
                    data["text"],
                    actor,
                    annotation=annotation,
                    proposal_id=identifier,
                    replace_text=row["kind"] in {"edit", "correction"},
                )
                job = await self.store.enqueue(channel, "proposal", actor, cur=cur)
            await cur.execute(
                "UPDATE wiki_edit_proposals SET status=%s,revision_id=%s,actor=%s,note=%s,updated_at=UTC_TIMESTAMP(6) WHERE id=%s",
                (action, revision, str(actor), note, identifier),
            )
            await self.store.audit(
                cur,
                channel,
                "proposal_" + action,
                identifier,
                actor,
                {"revision_id": revision, "note": note},
            )
        return {"status": action, "revision_id": revision, "job_id": job}

    @staticmethod
    async def _expired(cur, identifier):
        await cur.execute(
            "SELECT created_at<UTC_TIMESTAMP(6)-INTERVAL 1 DAY expired FROM wiki_edit_proposals WHERE id=%s",
            (identifier,),
        )
        return (await cur.fetchone())["expired"]

    async def proposals(self, *, user=None, status=None, page=None, offset=0):
        args, where = [], ["1=1"]
        if user:
            where.append("e.user_id=%s")
            args.append(user)
        if status:
            where.append("e.status=%s")
            args.append(status)
        if page:
            where.append("e.page_id=%s")
            args.append(page)
        rows = await self.store.query(
            f"""SELECT e.id,e.channel_id,e.page_id,e.base_revision,e.revision_id,e.kind,e.payload,e.status,e.note,
            e.created_at,p.title,EXISTS(SELECT 1 FROM wiki_contributions x WHERE x.proposal_id=e.id) published
            FROM wiki_edit_proposals e JOIN wiki_pages p ON p.id=e.page_id
            WHERE {" AND ".join(where)} ORDER BY e.created_at DESC,e.id LIMIT 30 OFFSET %s""",
            (*args, offset),
            fetch=True,
        )
        return [{**row, "payload": decode(row["payload"])} for row in rows]

    async def contributions(self, *, channel=None, page=None, user=None, offset=0, history=False):
        args, conditions = [], [VISIBLE, "u.blocked=FALSE", "u.anonymous_contributions=FALSE"]
        for field, value in (("e.channel_id", channel), ("e.page_id", page), ("u.id", user)):
            if value:
                args.append(value)
                conditions.append(f"{field}=%s")
        select = "u.id,u.display_name,IF(u.show_username,u.username,NULL) username,u.show_avatar AND u.avatar IS NOT NULL avatar"
        if history:
            select += ",e.kind,p.title,p.slug,e.channel_id,e.revision_id,x.created_at"
        else:
            select += """,COUNT(*) total,MAX(x.created_at) latest,
                SUM(e.kind IN ('edit','correction')) edits,
                SUM(e.kind IN ('annotation','dispute','outdated','fact_check')) annotations,
                SUM(e.kind='source') sources"""
        group = "" if history else "GROUP BY u.id,u.display_name,username,avatar"
        order = "x.created_at DESC,x.proposal_id" if history else "total DESC,latest DESC,u.id"
        return await self.store.query(
            f"""SELECT {select} FROM wiki_contributions x
            JOIN wiki_edit_proposals e ON e.id=x.proposal_id JOIN wiki_users u ON u.id=e.user_id
            JOIN wiki_channels c ON c.id=e.channel_id JOIN wiki_pages p ON p.id=e.page_id
            JOIN wiki_publication_pages pp ON pp.publication_id=c.published_revision AND pp.page_id=p.id
            JOIN wiki_page_revisions r ON r.id=pp.revision_id
                AND (r.id=x.revision_id OR JSON_CONTAINS(JSON_EXTRACT(r.document,'$.community_contributions'),JSON_QUOTE(e.id)))
            WHERE {" AND ".join(conditions)} {group} ORDER BY {order} LIMIT 30 OFFSET %s""",
            (*args, offset),
            fetch=True,
        )
