# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot

"""Bridge Wiki sessions to the local Go assistant, without exposing its credentials."""

import hashlib
import re

import aiohttp
from fastapi import APIRouter, Depends, HTTPException, Request
from fastapi.responses import JSONResponse, Response, StreamingResponse
from pydantic import BaseModel, ConfigDict, Field

from core.settings import get_settings
from core.wiki.accounts import token_hash

from .wiki_account import LOGOUT, WikiUser, same_origin

PATH = "/api/wiki/assistant"
TURN = re.compile(r"[a-f0-9]{32}")
METHODS = {("GET", "/status"), ("POST", "/checkin"), ("POST", "/chat")}
router = APIRouter()


def is_assistant_request(method, path):
    if not path.startswith(PATH + "/"):
        return False
    route = path[len(PATH) :]
    return (method, route) in METHODS or (
        method in {"GET", "DELETE"}
        and route.startswith("/chat/")
        and TURN.fullmatch(route[6:]) is not None
    )


class Article(BaseModel):
    model_config = ConfigDict(extra="forbid", strict=True)
    channel: str = Field(pattern=r"^[1-9][0-9]{0,18}$")
    page: str = Field(pattern=r"^[a-f0-9]{32}$")
    revision: str = Field(pattern=r"^[a-f0-9]{32}$")
    selected: str = Field(default="", max_length=500)


class Question(BaseModel):
    model_config = ConfigDict(extra="forbid", strict=True, str_strip_whitespace=True)
    id: str = Field(pattern=r"^[a-f0-9]{32}$")
    question: str = Field(min_length=1, max_length=4000)
    parent: str | None = Field(default=None, pattern=r"^[a-f0-9]{32}$")
    article: Article | None = None


async def bridge(request, user, method, path, *, body=None, stream=False):
    settings = get_settings()
    config = settings.wiki_login
    token = settings.telegram.qa_bot_token
    if not config.assistant_enabled or not token:
        raise HTTPException(404, "Wiki 助手未启用")
    session = request.cookies.get(LOGOUT)
    if not token_hash(session):
        raise HTTPException(401, "请重新登录 Wiki")
    auth = hashlib.sha256(
        f"sakura-wiki-assistant:v1:{token.get_secret_value()}".encode()
    ).hexdigest()
    client = aiohttp.ClientSession(
        timeout=aiohttp.ClientTimeout(total=None, connect=4, sock_read=45 if stream else 20)
    )
    try:
        upstream = await client.request(
            method,
            f"http://127.0.0.1:{config.assistant_port}{path}",
            headers={
                "Authorization": f"Bearer {auth}",
                "X-Wiki-User": str(user["telegram_id"]),
                "X-Wiki-Session": session,
            },
            json=body,
            allow_redirects=False,
        )
    except (aiohttp.ClientError, TimeoutError):
        await client.close()
        raise HTTPException(503, "Go 助手暂不可用，请稍后重试") from None
    except BaseException:
        await client.close()
        raise

    if stream and upstream.status == 200 and upstream.content_type == "text/event-stream":

        async def events():
            try:
                async for chunk in upstream.content.iter_any():
                    yield chunk
            except (aiohttp.ClientError, TimeoutError):
                # EOF without a terminal event lets the browser reconnect to the same ID.
                return
            finally:
                upstream.close()
                await client.close()

        return StreamingResponse(
            events(), media_type="text/event-stream", headers={"X-Accel-Buffering": "no"}
        )

    try:
        if upstream.status == 204:
            return Response(status_code=204)
        if (
            upstream.status not in {200, 400, 401, 403, 404, 409, 410, 503}
            or upstream.content_type != "application/json"
        ):
            raise HTTPException(503, "Go 助手响应暂不可用")
        payload = await upstream.json()
        if (
            method == "GET"
            and path == "/status"
            and upstream.status == 200
            and not config.assistant_checkin
        ):
            payload["checkin_enabled"] = False
        return JSONResponse(payload, status_code=upstream.status)
    except (aiohttp.ClientError, TimeoutError, ValueError):
        raise HTTPException(503, "Go 助手响应暂不可用") from None
    finally:
        upstream.close()
        await client.close()


@router.get("/status")
async def status(request: Request, user: WikiUser):
    return await bridge(request, user, "GET", "/status")


@router.post("/checkin", dependencies=[Depends(same_origin)])
async def checkin(request: Request, user: WikiUser):
    if not get_settings().wiki_login.assistant_checkin:
        raise HTTPException(403, "Wiki 签到未启用")
    return await bridge(request, user, "POST", "/checkin")


@router.post("/chat", dependencies=[Depends(same_origin)])
async def chat(request: Request, body: Question, user: WikiUser):
    return await bridge(
        request, user, "POST", "/chat", body=body.model_dump(exclude_none=True), stream=True
    )


@router.get("/chat/{turn}")
async def resume(turn: str, request: Request, user: WikiUser):
    if not TURN.fullmatch(turn):
        raise HTTPException(404)
    return await bridge(request, user, "GET", f"/chat/{turn}", stream=True)


@router.delete("/chat/{turn}", dependencies=[Depends(same_origin)])
async def cancel(turn: str, request: Request, user: WikiUser):
    if not TURN.fullmatch(turn):
        raise HTTPException(404)
    return await bridge(request, user, "DELETE", f"/chat/{turn}")
