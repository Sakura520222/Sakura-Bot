# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot

"""通过现有 WebUI JWT 认证提供图片任务状态、重试和限量补录。"""

from datetime import datetime

from fastapi import APIRouter, HTTPException
from pydantic import BaseModel, Field

from core.i18n.i18n import get_text
from core.media.image_pipeline import channel_key, get_image_pipeline

router = APIRouter()


class BackfillRequest(BaseModel):
    channel: str
    start: datetime
    end: datetime
    limit: int = Field(default=1000, ge=1, le=100000)


class RetryRequest(BaseModel):
    force: bool = False


def active_pipeline():
    pipeline = get_image_pipeline()
    if not pipeline.tasks:
        raise HTTPException(503, get_text("images.unavailable"))
    return pipeline


@router.get("/stats")
async def image_stats():
    return {"success": True, "data": await active_pipeline().store.stats()}


@router.get("/{channel_id}/{message_id}")
async def image_status(channel_id: str, message_id: int):
    job = await active_pipeline().store.get(channel_key(channel_id), message_id)
    if not job:
        raise HTTPException(404, get_text("images.not_found"))
    # 不暴露内部租约 token 和服务端文件路径。
    return {
        "success": True,
        "data": {k: v for k, v in job.items() if k not in ("lease_token", "revision")},
    }


@router.post("/{channel_id}/{message_id}/retry")
async def retry_image(channel_id: str, message_id: int, request: RetryRequest):
    success = await active_pipeline().store.retry(
        channel_key(channel_id), message_id, request.force
    )
    if not success:
        raise HTTPException(404, get_text("images.not_found"))
    return {"success": True}


@router.post("/backfill")
async def backfill_images(request: BackfillRequest):
    import core.config as config_module

    # 不允许通过管理接口访问任意 Telegram 频道。
    if request.channel not in config_module.CHANNELS:
        raise HTTPException(400, get_text("images.channel_invalid"))
    if not request.start.tzinfo or not request.end.tzinfo or request.start >= request.end:
        raise HTTPException(400, get_text("images.range_invalid"))
    pipeline = active_pipeline()
    count = await pipeline.backfill(
        pipeline.client, request.channel, request.start, request.end, request.limit
    )
    return {"success": True, "data": {"scheduled": count, "limit": request.limit}}
