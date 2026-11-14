import difflib
import html
import re
import uuid
from typing import Literal

from pydantic import Field, field_validator

from .models import Strict
from .reader import VISIBLE
from .schema import REVISION_VISIBLE
from .store import decode, encode
from .web_research import public_url

PROPOSAL_LABELS = {
    "edit": "正文修订",
    "correction": "纠错",
    "annotation": "注解",
    "source": "补充来源",
    "dispute": "争议标注",
    "outdated": "过期标注",
    "fact_check": "事实核查",
}


class Proposal(Strict):
    revision: str = Field(pattern=r"^[a-f0-9]{32}$")
    kind: Literal["edit", "correction", "annotation", "source", "dispute", "outdated", "fact_check"]
    claim: int = Field(ge=0, le=10000, strict=True)
    text: str = Field(min_length=1, max_length=10000)
    sources: list[str] = Field(default_factory=list, max_length=5)
    excerpt: str = Field(default="", max_length=500)
    excerpt_occurrence: int | None = Field(default=None, ge=0, le=10000, strict=True)

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


def selection_text(value):
    value = re.sub(
        r"!?\[([^\]\n]*)\]\([^\n)]*\)|\[\[([^\]\n]+)\]\]",
        lambda match: match[1] or match[2] or "",
        value,
    )
    value = re.sub(r"[`*_~]", "", html.unescape(value))
    return " ".join(value.split())


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
            if proposal.excerpt:
                excerpt = selection_text(proposal.excerpt)
                if not excerpt or excerpt not in selection_text(original):
                    raise ValueError("所选正文与当前版本不一致，请重新划选")
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
                if not ch["enabled"]:
                    raise ValueError("请先启用频道 Wiki")
                await self.validate_approval(cur, ch, row)
                revision, job = await self.build_approved(cur, row, actor)
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
        return {
            "status": action,
            "revision_id": revision,
            "job_id": job,
            "queued": action == "approved" and not job,
        }

    async def validate_approval(self, cur, channel, proposal):
        await cur.execute(
            "SELECT blocked FROM wiki_users WHERE id=%s FOR UPDATE", (proposal["user_id"],)
        )
        author = await cur.fetchone()
        if not author or author["blocked"]:
            raise ValueError("投稿账户不可用")
        await cur.execute(
            f"""SELECT r.id FROM wiki_publication_pages pp
            JOIN wiki_page_revisions r ON r.id=pp.revision_id JOIN wiki_pages p ON p.id=pp.page_id
            WHERE pp.publication_id=%s AND pp.page_id=%s AND r.id=%s AND {REVISION_VISIBLE}
            AND NOT EXISTS (SELECT 1 FROM wiki_edit_proposals other WHERE other.page_id=p.id
                AND other.base_revision=r.id AND other.status='approved' AND other.id<>%s)""",
            (
                channel["published_revision"],
                proposal["page_id"],
                proposal["base_revision"],
                proposal["id"],
            ),
        )
        if not await cur.fetchone():
            raise ValueError("发布基线已变化，或同一版本已有其他建议获批，请重新投稿")

    async def build_approved(self, cur, proposal, actor):
        data = decode(proposal["payload"])
        annotation = None
        replace = proposal["kind"] in {"edit", "correction"}
        if not replace or data["sources"]:
            annotation = {
                "proposal_id": proposal["id"],
                "kind": proposal["kind"],
                "text": data["text"],
                "sources": data["sources"],
            }
            if data.get("excerpt"):
                annotation["excerpt"] = data["excerpt"]
                if data.get("excerpt_occurrence") is not None:
                    annotation["excerpt_occurrence"] = data["excerpt_occurrence"]
        revision = await self.knowledge.create_correction(
            cur,
            proposal["channel_id"],
            proposal["base_revision"],
            data["claim"],
            data["text"],
            actor,
            annotation=annotation,
            proposal_id=proposal["id"],
            replace_text=replace,
            standalone=True,
        )
        job = await self.store.enqueue_page_edit(
            cur,
            proposal["channel_id"],
            proposal["page_id"],
            revision,
            proposal["base_revision"],
            actor,
        )
        return revision, job

    async def dispatch_approved(self, channel):
        async with self.store.transaction() as cur:
            await cur.execute("SELECT * FROM wiki_channels WHERE id=%s FOR UPDATE", (channel,))
            ch = await cur.fetchone()
            if not ch or not ch["enabled"]:
                return False
            await cur.execute(
                """SELECT * FROM wiki_edit_proposals WHERE channel_id=%s AND status='approved' AND revision_id IS NULL
                ORDER BY updated_at,id LIMIT 1 FOR UPDATE""",
                (channel,),
            )
            proposal = await cur.fetchone()
            if not proposal:
                return False
            try:
                await self.validate_approval(cur, ch, proposal)
            except ValueError as error:
                await cur.execute(
                    "UPDATE wiki_edit_proposals SET status='returned',note=%s,updated_at=UTC_TIMESTAMP(6) WHERE id=%s",
                    (str(error), proposal["id"]),
                )
                await self.store.audit(
                    cur, channel, "proposal_returned", proposal["id"], "wiki", {"note": str(error)}
                )
                return True
            revision, _ = await self.build_approved(cur, proposal, proposal["actor"] or "wiki")
            await cur.execute(
                "UPDATE wiki_edit_proposals SET revision_id=%s,updated_at=UTC_TIMESTAMP(6) WHERE id=%s",
                (revision, proposal["id"]),
            )
            return True

    @staticmethod
    async def _expired(cur, identifier):
        await cur.execute(
            "SELECT created_at<UTC_TIMESTAMP(6)-INTERVAL 1 DAY expired FROM wiki_edit_proposals WHERE id=%s",
            (identifier,),
        )
        return (await cur.fetchone())["expired"]

    async def proposals(
        self,
        *,
        user=None,
        status=None,
        page=None,
        identifier=None,
        offset=0,
        review=False,
        queued=False,
    ):
        args, where = [], ["1=1"]
        if user:
            where.append("e.user_id=%s")
            args.append(user)
        if status:
            where.append("e.status=%s")
            args.append(status)
        if queued:
            where.append("e.status='approved' AND e.revision_id IS NULL")
        if page:
            where.append("e.page_id=%s")
            args.append(page)
        if identifier:
            where.append("e.id=%s")
            args.append(identifier)
        review_fields = (
            """,c.title channel_title,j.id review_job_id,j.status review_job_status,
            JSON_EXTRACT(j.progress,'$.review_complete')=TRUE review_complete,
            JSON_EXTRACT(j.progress,'$.review_skipped')=TRUE review_skipped,base.document base_document"""
            if review
            else ""
        )
        review_joins = (
            """JOIN wiki_channels c ON c.id=e.channel_id
            LEFT JOIN wiki_page_revisions base ON base.id=e.base_revision AND base.page_id=e.page_id
            LEFT JOIN wiki_page_revisions edit ON edit.id=e.revision_id
            LEFT JOIN wiki_jobs j ON j.id=edit.job_id AND j.kind='page_edit'"""
            if review
            else ""
        )
        rows = await self.store.query(
            f"""SELECT e.id,e.channel_id,e.page_id,e.base_revision,e.revision_id,e.kind,e.payload,e.status,e.note,
            e.created_at,p.title,EXISTS(SELECT 1 FROM wiki_contributions x WHERE x.proposal_id=e.id) published{review_fields}
            FROM wiki_edit_proposals e JOIN wiki_pages p ON p.id=e.page_id
            {review_joins}
            WHERE {" AND ".join(where)} ORDER BY e.created_at DESC,e.id LIMIT 30 OFFSET %s""",
            (*args, offset),
            fetch=True,
        )
        for row in rows:
            row["payload"] = decode(row["payload"])
            if review:
                claims = decode(row.pop("base_document")).get("claims", [])
                claim = row["payload"]["claim"]
                row["original"] = claims[claim]["text"] if 0 <= claim < len(claims) else None
        return rows

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
