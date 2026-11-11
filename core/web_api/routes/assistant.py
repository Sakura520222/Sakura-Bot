"""Go assistant settings and assistant-token capability diagnostics."""

import os
from zoneinfo import ZoneInfo, ZoneInfoNotFoundError

from fastapi import APIRouter, HTTPException
from pydantic import BaseModel, ConfigDict, Field, field_validator

from core.infrastructure.telegram_bot_api import BotAPIError, call_method
from core.web_api.deps import get_config, write_config

router = APIRouter()


class StrictModel(BaseModel):
    model_config = ConfigDict(extra="forbid", strict=True)


class CommentSettings(StrictModel):
    channel: str
    enabled: bool = False
    wake_word: str = Field(default="小妲", min_length=1, max_length=32)

    @field_validator("wake_word")
    @classmethod
    def wake_nonempty(cls, value):
        if not value.strip():
            raise ValueError("唤醒词不能只有空白")
        return value


class AssistantSettings(StrictModel):
    timezone: str = "UTC"
    checkin_enabled: bool = True
    require_subscription: bool = False
    required_channels: list[str] = Field(default_factory=list, max_length=100)
    comments: list[CommentSettings] = Field(default_factory=list, max_length=100)
    max_messages: int = Field(default=8, ge=1, le=64)
    inbox_capacity: int = Field(default=8, ge=1, le=64)
    per_user_concurrency: int = Field(default=4, ge=1, le=16)

    @field_validator("timezone")
    @classmethod
    def timezone_exists(cls, value):
        try:
            ZoneInfo(value)
        except (ZoneInfoNotFoundError, ValueError) as exc:
            raise ValueError("无效业务时区") from exc
        return value


class PoolSettings(StrictModel):
    workers: int = Field(default=4, ge=1, le=32)
    queue_capacity: int = Field(default=64, ge=1, le=1024)
    timeout_seconds: int = Field(default=180, ge=1, le=1800)


class AssistantRequest(StrictModel):
    assistant: AssistantSettings
    pool: PoolSettings


def validate_channels(settings, channels):
    selected = settings.required_channels + [r.channel for r in settings.comments]
    if any(ch not in channels for ch in selected):
        raise HTTPException(422, "只能选择系统已配置频道")
    if len(set(settings.required_channels)) != len(settings.required_channels) or len(
        {r.channel for r in settings.comments}
    ) != len(settings.comments):
        raise HTTPException(422, "频道不能重复")
    if settings.require_subscription and not settings.required_channels:
        raise HTTPException(422, "请至少选择一个必订阅频道")


async def diagnose(settings):
    token = os.getenv("QA_BOT_TOKEN", "")
    selected = set(settings.required_channels if settings.require_subscription else []) | {
        r.channel for r in settings.comments if r.enabled
    }
    if not selected:
        return []
    if not token:
        return [
            {"channel": ch, "ok": False, "message": "未配置助手 Bot Token"}
            for ch in sorted(selected)
        ]
    results = []
    try:
        me = await call_method(token, "getMe", timeout=5)
    except BotAPIError:
        return [
            {"channel": ch, "ok": False, "message": "助手 Bot 身份查询失败"}
            for ch in sorted(selected)
        ]
    for ch in sorted(selected):
        try:
            info = await call_method(
                token, "getChat", {"chat_id": ch.replace("https://t.me/", "@")}, timeout=5
            )
            if settings.require_subscription and ch in settings.required_channels:
                membership = await call_method(
                    token, "getChatMember", {"chat_id": info["id"], "user_id": me["id"]}, timeout=5
                )
                if membership.get("status") not in ("administrator", "creator"):
                    raise ValueError("助手 Bot 必须是必订阅频道管理员")
            if any(r.channel == ch and r.enabled for r in settings.comments):
                discussion = info.get("linked_chat_id")
                if not discussion:
                    raise ValueError("频道没有关联讨论群组")
                membership = await call_method(
                    token, "getChatMember", {"chat_id": discussion, "user_id": me["id"]}, timeout=5
                )
                if membership.get("status") not in ("member", "administrator", "creator"):
                    raise ValueError("助手 Bot 尚未加入讨论群组")
                if membership.get("status") == "member" and not me.get(
                    "can_read_all_group_messages"
                ):
                    raise ValueError(
                        "请关闭助手 Bot 隐私模式或将其设为群管理员，以收集全部可见评论"
                    )
            results.append({"channel": ch, "ok": True, "message": "助手 Bot 权限检查通过"})
        except ValueError as exc:
            results.append({"channel": ch, "ok": False, "message": str(exc)})
        except BotAPIError:
            results.append(
                {
                    "channel": ch,
                    "ok": False,
                    "message": "Telegram 查询失败，请检查助手 Bot 权限并重试",
                }
            )
    return results


@router.get("/assistant")
async def get_assistant():
    cfg = get_config()
    settings = AssistantSettings.model_validate(cfg.get("qa_assistant", {}))
    return {
        "success": True,
        "data": {
            "assistant": settings.model_dump(),
            "pool": PoolSettings.model_validate(cfg.get("qa_ai_pool", {})).model_dump(),
            "channels": cfg.get("channels", []),
        },
    }


@router.post("/assistant/diagnostics")
async def assistant_diagnostics(request: AssistantRequest):
    validate_channels(request.assistant, get_config().get("channels", []))
    return {"success": True, "data": await diagnose(request.assistant)}


@router.put("/assistant")
async def update_assistant(request: AssistantRequest):
    cfg = get_config()
    validate_channels(request.assistant, cfg.get("channels", []))
    diagnostics = await diagnose(request.assistant)
    if any(not item["ok"] for item in diagnostics):
        raise HTTPException(422, {"message": "助手权限检查未通过", "diagnostics": diagnostics})
    cfg["qa_assistant"] = request.assistant.model_dump()
    cfg["qa_ai_pool"] = request.pool.model_dump()
    write_config(cfg)
    return {
        "success": True,
        "message": "已保存；频道、订阅与签到配置立即生效，并发容量与回合限制重启助手后生效",
    }


class BackfillRequest(StrictModel):
    channel: str
    post_id: int = Field(ge=1)
    limit: int = Field(default=5000, ge=1, le=20000)


@router.post("/assistant/backfill")
async def backfill_comments(request: BackfillRequest):
    import asyncio

    from core.handlers.userbot_client import get_userbot_client
    from core.services.qa_comment_backfill import backfill_post
    from core.web_api.deps import get_database

    settings = AssistantSettings.model_validate(get_config().get("qa_assistant", {}))
    if not any(r.channel == request.channel and r.enabled for r in settings.comments):
        raise HTTPException(422, "请先启用该频道的评论助手")
    manager = get_userbot_client()
    client = getattr(manager, "client", None)
    if not client or not client.is_connected():
        raise HTTPException(409, "UserBot 未连接")
    me = await client.get_me()
    if not me or me.bot:
        raise HTTPException(409, "历史回溯需要已登录的 UserBot 用户账号")
    database = get_database()
    if not getattr(database, "pool", None):
        raise HTTPException(409, "数据库连接不可用")
    try:
        async with asyncio.timeout(180):
            result = await backfill_post(
                client, database.pool, request.channel, request.post_id, request.limit
            )
    except TimeoutError:
        raise HTTPException(504, "回溯超时；已保存部分可见评论，可幂等重试") from None
    except Exception:
        raise HTTPException(
            502, "历史回溯失败，请确认帖子、UserBot 权限以及 Go 助手表结构已初始化"
        ) from None
    return {"success": True, "data": result}
