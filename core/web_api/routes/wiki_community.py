import re
from typing import Literal

from fastapi import APIRouter, Depends, HTTPException, Query
from fastapi.responses import Response
from pydantic import Field

from core.wiki.community import Proposal
from core.wiki.models import Strict

from .wiki import ADMIN_AUTH, runtime
from .wiki_account import WikiUser, same_origin

PATH = "/api/wiki/community"
IDENTIFIER = re.compile(r"[0-9a-f]{32}")
router = APIRouter()
admin = APIRouter(dependencies=[ADMIN_AUTH])


def is_community_request(method, path):
    return (method, path) in {
        ("GET", PATH + "/proposals"),
        ("POST", PATH + "/proposals"),
        ("GET", PATH + "/contributors"),
    } or (
        method in {"GET", "DELETE"}
        and re.fullmatch(
            PATH + r"/(?:contributors/[a-f0-9]{32}(?:/avatar)?|proposals/[a-f0-9]{32})", path
        )
    )


class Submission(Proposal):
    channel: str = Field(pattern=r"^[1-9][0-9]{0,18}$")
    page: str = Field(pattern=r"^[a-f0-9]{32}$")


class Decision(Strict):
    action: Literal["approved", "rejected", "returned"]
    note: str = Field(default="", max_length=1000)


@router.post("/proposals", status_code=201, dependencies=[Depends(same_origin)])
async def submit(body: Submission, user: WikiUser):
    service = runtime()
    try:
        proposal = Proposal.model_validate(body.model_dump(exclude={"channel", "page"}))
        identifier, notify = await service.community.submit(
            user["id"], body.channel, body.page, proposal
        )
    except ValueError as error:
        raise HTTPException(409, str(error)) from None
    if notify:
        from core.wiki.commands import notify_proposal

        await notify_proposal(service, identifier, body.channel, body.kind)
    return {"id": identifier, "status": "pending"}


@router.get("/proposals")
async def own_proposals(user: WikiUser, offset: int = Query(0, ge=0, le=100000)):
    return await runtime().community.proposals(user=user["id"], offset=offset)


@router.delete("/proposals/{identifier}", status_code=204, dependencies=[Depends(same_origin)])
async def withdraw_proposal(identifier: str, user: WikiUser):
    count = await runtime().store.query(
        """UPDATE wiki_edit_proposals SET status='withdrawn',updated_at=UTC_TIMESTAMP(6)
        WHERE id=%s AND user_id=%s AND status IN ('pending','returned')""",
        (identifier, user["id"]),
    )
    if not count:
        raise HTTPException(404)


@router.get("/contributors")
async def contributors(
    channel: str | None = None, page: str | None = None, offset: int = Query(0, ge=0, le=100000)
):
    if (channel and not re.fullmatch(r"[1-9][0-9]{0,18}", channel)) or (
        page and not IDENTIFIER.fullmatch(page)
    ):
        raise HTTPException(404)
    return await runtime().community.contributions(channel=channel, page=page, offset=offset)


@router.get("/contributors/{identifier}")
async def contributor(identifier: str, offset: int = Query(0, ge=0, le=100000)):
    rows = await runtime().community.contributions(user=identifier, offset=offset, history=True)
    if not rows and not offset:
        raise HTTPException(404)
    return rows


@router.get("/contributors/{identifier}/avatar")
async def contributor_avatar(identifier: str):
    service = runtime()
    entries = await service.community.contributions(user=identifier)
    if not entries or not entries[0]["avatar"]:
        raise HTTPException(404)
    row = await service.store.one(
        """SELECT avatar,avatar_type FROM wiki_users WHERE id=%s AND show_avatar=TRUE
        AND anonymous_contributions=FALSE AND blocked=FALSE""",
        (identifier,),
    )
    if not row or not row["avatar"]:
        raise HTTPException(404)
    return Response(row["avatar"], media_type=row["avatar_type"])


@admin.get("/proposals")
async def queue(
    status: Literal["pending", "approved", "rejected", "returned", "withdrawn"] = "pending",
    offset: int = Query(0, ge=0, le=100000),
):
    return await runtime(admin=True).community.proposals(status=status, offset=offset)


@admin.get("/pages/{page}/proposals")
async def page_queue(page: str, offset: int = Query(0, ge=0, le=100000)):
    if not IDENTIFIER.fullmatch(page):
        raise HTTPException(404)
    return await runtime(admin=True).community.proposals(status="pending", page=page, offset=offset)


@admin.post("/proposals/{identifier}")
async def decide(identifier: str, body: Decision, actor=ADMIN_AUTH):
    service = runtime(admin=True)
    try:
        result = await service.community.decide(identifier, body.action, actor, body.note)
    except ValueError as error:
        raise HTTPException(409, str(error)) from None
    service.wake()
    return result
