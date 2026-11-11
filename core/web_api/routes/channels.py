# Copyright 2026 Sakura-Bot
#
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
#
# - 署名：必须提供本项目的原始来源链接
# - 相同方式共享：衍生作品必须采用相同的许可证
#
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
# 许可证全文：参见 LICENSE 文件

"""
频道管理 API 路由

提供频道的增删查功能。
"""

import logging

from fastapi import APIRouter, HTTPException

from core.config import normalize_channel_id
from core.web_api.deps import get_config, write_config
from core.web_api.schemas.channel import (
    ApiResponse,
    ChannelAddRequest,
    ChannelDeleteRequest,
    ChannelInfo,
)

logger = logging.getLogger(__name__)

router = APIRouter()


@router.get("")
async def list_channels():
    """获取所有频道列表"""
    try:
        config = get_config()
        channels = config.get("channels", [])
        schedules = config.get("summary_schedules", {})
        poll_settings = config.get("channel_poll_settings", {})

        channel_infos = []
        for ch in channels:
            channel_infos.append(
                ChannelInfo(
                    url=ch,
                    has_schedule=ch in schedules,
                    has_poll_settings=ch in poll_settings,
                )
            )

        return {
            "success": True,
            "data": {
                "channels": [c.model_dump() for c in channel_infos],
                "total": len(channel_infos),
            },
        }
    except Exception as e:
        logger.error(f"获取频道列表失败: {type(e).__name__}: {e}", exc_info=True)
        raise HTTPException(status_code=500, detail=str(e)) from e


@router.post("")
async def add_channel(request: ChannelAddRequest):
    """添加频道"""
    try:
        channel_url = normalize_channel_id(request.channel_url)

        if not channel_url:
            return ApiResponse(success=False, message="无效的频道 URL")

        config = get_config()
        channels = config.get("channels", [])

        if channel_url in channels:
            return ApiResponse(success=False, message=f"频道已存在: {channel_url}")

        channels.append(channel_url)
        config["channels"] = channels
        write_config(config)

        logger.info(f"已通过 WebUI 添加频道: {channel_url}")
        return ApiResponse(success=True, message=f"频道添加成功: {channel_url}")

    except Exception as e:
        logger.error(f"添加频道失败: {type(e).__name__}: {e}", exc_info=True)
        raise HTTPException(status_code=500, detail=str(e)) from e


@router.delete("")
async def delete_channel(request: ChannelDeleteRequest):
    """删除频道"""
    try:
        channel_url = normalize_channel_id(request.channel_url)

        config = get_config()
        channels = config.get("channels", [])

        if channel_url not in channels:
            return ApiResponse(success=False, message=f"频道不存在: {channel_url}")

        channels.remove(channel_url)
        config["channels"] = channels

        # 同时清理相关的定时任务和投票设置
        schedules = config.get("summary_schedules", {})
        if channel_url in schedules:
            del schedules[channel_url]
            config["summary_schedules"] = schedules

        poll_settings = config.get("channel_poll_settings", {})
        if channel_url in poll_settings:
            del poll_settings[channel_url]
            config["channel_poll_settings"] = poll_settings

        assistant = config.get("qa_assistant")
        if isinstance(assistant, dict):
            assistant["required_channels"] = [
                ch for ch in assistant.get("required_channels", []) if ch != channel_url
            ]
            assistant["comments"] = [
                rule for rule in assistant.get("comments", []) if rule.get("channel") != channel_url
            ]
            if not assistant["required_channels"]:
                assistant["require_subscription"] = False
            config["qa_assistant"] = assistant

        write_config(config)

        logger.info(f"已通过 WebUI 删除频道: {channel_url}")
        return ApiResponse(success=True, message=f"频道删除成功: {channel_url}")

    except Exception as e:
        logger.error(f"删除频道失败: {type(e).__name__}: {e}", exc_info=True)
        raise HTTPException(status_code=500, detail=str(e)) from e
