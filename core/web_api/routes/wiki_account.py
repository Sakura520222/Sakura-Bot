# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot

"""First-party Wiki account endpoints. WebUI administration uses its own credentials."""

import re
import secrets
from typing import Annotated
from urllib.parse import unquote, urlencode, urlsplit

import aiohttp
from fastapi import APIRouter, Depends, HTTPException, Request, Response
from fastapi.responses import RedirectResponse
from jwt import PyJWTError
from pydantic import BaseModel, ConfigDict, Field

from core.settings import get_settings
from core.wiki import telegram_login
from core.wiki.accounts import ACCESS_SECONDS, FLOW_SECONDS, AccountDenied, token_hash

from .wiki import ADMIN_AUTH, runtime

ACCOUNT_PATH = "/api/wiki/account"
ACCESS = "__Host-sakura-wiki-access"
REFRESH = "__Host-sakura-wiki-refresh"
LOGOUT = "__Host-sakura-wiki-logout"
BROWSER = "__Host-sakura-wiki-login"
METHODS = {
    ("GET", f"{ACCOUNT_PATH}/session"),
    ("POST", f"{ACCOUNT_PATH}/start"),
    ("GET", telegram_login.CALLBACK),
    ("POST", f"{ACCOUNT_PATH}/refresh"),
    ("POST", f"{ACCOUNT_PATH}/logout"),
    ("PUT", f"{ACCOUNT_PATH}/privacy"),
    ("GET", f"{ACCOUNT_PATH}/avatar"),
}
router = APIRouter()
admin = APIRouter(dependencies=[ADMIN_AUTH])


class LoginStart(BaseModel):
    model_config = ConfigDict(extra="forbid", strict=True)
    return_to: str = Field(default="/wiki", max_length=2048)
    refresh_profile: bool = False


class Privacy(BaseModel):
    model_config = ConfigDict(extra="forbid", strict=True)
    show_avatar: bool
    show_username: bool
    anonymous_contributions: bool


class AccountBlock(BaseModel):
    model_config = ConfigDict(extra="forbid", strict=True)
    blocked: bool


def safe_wiki_path(path):
    try:
        parsed = urlsplit(path)
    except ValueError:
        return "/wiki"
    decoded = unquote(parsed.path)
    if (
        parsed.scheme
        or parsed.netloc
        or not re.match(r"^/wiki(?:/|$)", decoded)
        or any(segment in {".", ".."} for segment in decoded.split("/"))
        or not re.fullmatch(r"/wiki(?:/[A-Za-z0-9_%/-]*)?", parsed.path)
        or decoded.startswith("/wiki/admin")
        or "%" in decoded
        or any(ord(char) < 32 or char == "\\" for char in unquote(path))
    ):
        return "/wiki"
    return path


def on_origin(request, config):
    # The public authority is fixed by configuration. The reverse proxy preserves Host.
    return config and request.headers.get("host", "").lower() == urlsplit(config.origin).netloc


def same_origin(request: Request):
    config = telegram_login.login_config()
    if not on_origin(request, config):
        raise HTTPException(404, "Wiki 登录未启用")
    if (
        request.headers.get("origin") != config.origin
        or request.headers.get("X-Wiki-Request") != "1"
    ):
        raise HTTPException(403, "Wiki 请求来源无效")
    return config


async def require_wiki_user(request: Request):
    config = telegram_login.login_config()
    if not on_origin(request, config):
        raise HTTPException(401, "请先登录 Wiki")
    session = await runtime().accounts.session(request.cookies.get(ACCESS))
    if not session:
        raise HTTPException(401, "请先登录 Wiki")
    return session


SameOrigin = Annotated[telegram_login.LoginConfig, Depends(same_origin)]
WikiUser = Annotated[dict, Depends(require_wiki_user)]


def set_cookie(response, name, value, seconds):
    response.set_cookie(
        name, value, max_age=seconds, path="/", secure=True, httponly=True, samesite="lax"
    )


def clear_cookie(response, name):
    response.delete_cookie(name, path="/", secure=True, httponly=True, samesite="lax")


def set_session(response, access, refresh, remaining, logout_token=None):
    set_cookie(response, ACCESS, access, min(ACCESS_SECONDS, remaining))
    set_cookie(response, REFRESH, refresh, remaining)
    if logout_token:
        set_cookie(response, LOGOUT, logout_token, remaining)


def profile(session):
    return {
        "id": session["id"],
        "display_name": session["display_name"],
        "username": session["username"],
        "avatar": bool(session["has_avatar"]),
        "privacy": {name: bool(session[name]) for name in Privacy.model_fields},
    }


@router.get("/session")
async def session_status(request: Request):
    config = telegram_login.login_config()
    if not on_origin(request, config):
        return {"enabled": False, "user": None, "can_refresh": False}
    accounts = runtime().accounts
    user = await accounts.session(request.cookies.get(ACCESS))
    return {
        "enabled": True,
        "assistant_enabled": get_settings().wiki_login.assistant_enabled,
        "user": profile(user) if user else None,
        "can_refresh": not user and token_hash(request.cookies.get(REFRESH)) is not None,
    }


@admin.get("/status")
async def admin_status():
    return {"admin": True}


@router.post("/start")
async def start(body: LoginStart, request: Request, response: Response, config: SameOrigin):
    accounts = runtime().accounts
    previous = None
    if body.refresh_profile:
        previous = await require_wiki_user(request)
    browser = request.cookies.get(BROWSER)
    if not token_hash(browser):
        browser = secrets.token_urlsafe(32)
    state, nonce, verifier = await accounts.begin(
        browser,
        safe_wiki_path(body.return_to),
        config,
        previous["session_id"] if previous else None,
    )
    set_cookie(response, BROWSER, browser, FLOW_SECONDS)
    return {"authorization_url": telegram_login.authorization_url(config, state, nonce, verifier)}


@router.get("/callback", include_in_schema=False)
async def callback(request: Request):
    config = telegram_login.login_config()
    if not on_origin(request, config):
        raise HTTPException(404, "Wiki 登录未启用")
    accounts = runtime().accounts
    params = request.query_params
    flow = None
    if len(params.getlist("state")) == 1:
        flow = await accounts.consume(params["state"], request.cookies.get(BROWSER), config)
    error = "expired"
    if flow:
        if params.get("error") == "access_denied":
            error = "cancelled"
        elif (
            len(params.getlist("code")) == 1
            and 1 <= len(params["code"]) <= 2048
            and "error" not in params
        ):
            try:
                identity = await telegram_login.exchange_code(
                    config, params["code"], flow["verifier"], flow["nonce"]
                )
                avatar = await telegram_login.download_avatar(identity[4])
                tokens = await accounts.login(
                    identity, avatar, flow["refresh_session"], request.cookies.get(LOGOUT)
                )
            except AccountDenied:
                error = "unavailable"
            except (aiohttp.ClientError, TimeoutError, PyJWTError, ValueError, TypeError, KeyError):
                error = "failed"
            else:
                response = RedirectResponse(config.origin + flow["redirect_path"], status_code=303)
                set_session(response, *tokens)
                return response
    return RedirectResponse(
        config.origin + "/wiki?" + urlencode({"login_error": error}), status_code=303
    )


@router.post("/refresh", status_code=204, dependencies=[Depends(same_origin)])
async def refresh(request: Request, response: Response):
    try:
        tokens = await runtime().accounts.rotate(request.cookies.get(REFRESH))
    except AccountDenied:
        tokens = None
    if not tokens:
        # A concurrent tab may already have rotated the browser's cookies.
        raise HTTPException(401, "Wiki 会话已失效")
    set_session(response, *tokens)


@router.post("/logout", status_code=204, dependencies=[Depends(same_origin)])
async def logout(request: Request, response: Response):
    await runtime().accounts.logout(request.cookies.get(LOGOUT), request.cookies.get(BROWSER))
    for name in (ACCESS, REFRESH, LOGOUT, BROWSER):
        clear_cookie(response, name)


@router.put("/privacy", dependencies=[Depends(same_origin)])
async def update_privacy(body: Privacy, request: Request, user: WikiUser):
    accounts = runtime().accounts
    try:
        await accounts.privacy(user, request.cookies.get(ACCESS), body)
    except AccountDenied as error:
        raise HTTPException(401, "Wiki 会话已失效") from error
    return body


@router.get("/avatar")
async def avatar(user: WikiUser):
    row = await runtime().store.one(
        "SELECT avatar,avatar_type FROM wiki_users WHERE id=%s AND blocked=FALSE", (user["id"],)
    )
    if not row or not row["avatar"]:
        raise HTTPException(404)
    return Response(row["avatar"], media_type=row["avatar_type"])


@admin.get("/accounts")
async def accounts(offset: int = 0):
    if not 0 <= offset <= 100000:
        raise HTTPException(422, "无效页码")
    return await runtime(admin=True).store.query(
        """SELECT id,CAST(telegram_id AS CHAR) telegram_id,display_name,username,blocked,profile_at
        FROM wiki_users ORDER BY profile_at DESC,id LIMIT 50 OFFSET %s""",
        (offset,),
        fetch=True,
    )


@admin.put("/accounts/{user_id}/blocked")
async def block_account(user_id: str, body: AccountBlock):
    if not re.fullmatch(r"[a-f0-9]{32}", user_id) or not await runtime(
        admin=True
    ).accounts.set_blocked(user_id, body.blocked):
        raise HTTPException(404, "Wiki 用户不存在")
    return {"blocked": body.blocked}
