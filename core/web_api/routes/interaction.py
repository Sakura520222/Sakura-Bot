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
互动设置 API 路由

提供投票设置、自动投票、评论区欢迎配置等功能。
"""

import logging

from fastapi import APIRouter, HTTPException

from core.config import normalize_channel_id
from core.web_api.deps import get_config, write_config
from core.web_api.schemas.interaction import (
    AutoPollSettingsUpdate,
    CommentWelcomeUpdate,
    GlobalPollSettingsUpdate,
    PollSettingsUpdate,
)

logger = logging.getLogger(__name__)

router = APIRouter()


# ==================== 投票设置 ====================


@router.get("/poll-settings")
async def get_poll_settings():
    """获取所有频道的投票设置"""
    try:
        config = get_config()
        poll_settings = config.get("channel_poll_settings", {})

        return {
            "success": True,
            "data": {
                "settings": poll_settings,
                "enable_poll": config.get("enable_poll", True),
                "poll_regen_threshold": config.get("poll_regen_threshold", 5),
                "enable_vote_regen_request": config.get("enable_vote_regen_request", True),
                "public_voters": config.get("public_voters", True),
            },
        }

    except Exception as e:
        logger.error(f"获取投票设置失败: {type(e).__name__}: {e}", exc_info=True)
        raise HTTPException(status_code=500, detail=str(e)) from e


@router.put("/poll-settings/global")
async def update_global_poll_settings(request: GlobalPollSettingsUpdate):
    """更新全局投票设置"""
    try:
        config = get_config()

        if request.enable_poll is not None:
            config["enable_poll"] = request.enable_poll
        if request.poll_regen_threshold is not None:
            if request.poll_regen_threshold < 1:
                raise HTTPException(status_code=400, detail="重新生成阈值必须 >= 1")
            config["poll_regen_threshold"] = request.poll_regen_threshold
        if request.public_voters is not None:
            config["public_voters"] = request.public_voters
        if request.enable_vote_regen_request is not None:
            config["enable_vote_regen_request"] = request.enable_vote_regen_request

        write_config(config)

        logger.info("已通过 WebUI 更新全局投票设置")
        return {"success": True, "message": "全局投票设置已更新"}

    except HTTPException:
        raise
    except Exception as e:
        logger.error(f"更新全局投票设置失败: {type(e).__name__}: {e}", exc_info=True)
        raise HTTPException(status_code=500, detail=str(e)) from e


@router.put("/poll-settings/{channel:path}")
async def update_poll_settings(channel: str, request: PollSettingsUpdate):
    """更新指定频道的投票设置"""
    try:
        channel = normalize_channel_id(channel)
        config = get_config()

        poll_settings = config.get("channel_poll_settings", {})
        current = poll_settings.get(channel, {})

        # 未提交的字段保持不变；显式 null 删除覆盖，恢复全局配置或默认发送位置。
        for field, value in request.model_dump(exclude_unset=True).items():
            if value is None:
                current.pop(field, None)
            else:
                current[field] = value

        poll_settings[channel] = current
        config["channel_poll_settings"] = poll_settings
        write_config(config)

        logger.info(f"已通过 WebUI 更新投票设置: {channel}")
        return {"success": True, "message": f"投票设置已更新: {channel}"}

    except Exception as e:
        logger.error(f"更新投票设置失败: {type(e).__name__}: {e}", exc_info=True)
        raise HTTPException(status_code=500, detail=str(e)) from e


@router.delete("/poll-settings/{channel:path}")
async def delete_channel_poll_settings(channel: str):
    """删除指定频道的投票配置（回退到全局默认）"""
    try:
        channel = normalize_channel_id(channel)
        config = get_config()

        poll_settings = config.get("channel_poll_settings", {})
        if channel in poll_settings:
            del poll_settings[channel]
            config["channel_poll_settings"] = poll_settings
            write_config(config)
            logger.info(f"已通过 WebUI 删除频道投票配置: {channel}")
            return {"success": True, "message": f"频道投票配置已删除: {channel}"}
        return {"success": True, "message": "该频道无投票配置"}

    except Exception as e:
        logger.error(f"删除频道投票配置失败: {type(e).__name__}: {e}", exc_info=True)
        raise HTTPException(status_code=500, detail=str(e)) from e


# ==================== 自动投票 ====================


@router.get("/auto-poll")
async def get_auto_poll_settings():
    """获取自动投票配置"""
    try:
        config = get_config()
        return {
            "success": True,
            "data": {
                "enabled": config.get("enable_auto_poll", False),
                "channel_settings": config.get("channel_auto_poll_settings", {}),
            },
        }

    except Exception as e:
        logger.error(f"获取自动投票配置失败: {type(e).__name__}: {e}", exc_info=True)
        raise HTTPException(status_code=500, detail=str(e)) from e


@router.put("/auto-poll")
async def update_auto_poll(request: AutoPollSettingsUpdate):
    """更新自动投票全局开关"""
    try:
        config = get_config()
        config["enable_auto_poll"] = request.enabled
        write_config(config)

        status_text = "启用" if request.enabled else "禁用"
        logger.info(f"已通过 WebUI {status_text}自动投票")
        return {"success": True, "message": f"自动投票已{status_text}"}

    except Exception as e:
        logger.error(f"更新自动投票配置失败: {type(e).__name__}: {e}", exc_info=True)
        raise HTTPException(status_code=500, detail=str(e)) from e


@router.put("/auto-poll/{channel:path}")
async def update_channel_auto_poll(channel: str, request: AutoPollSettingsUpdate):
    """更新指定频道的自动投票设置"""
    try:
        channel = normalize_channel_id(channel)
        config = get_config()

        if not config.get("enable_auto_poll", False):
            raise HTTPException(status_code=400, detail="请先开启全局自动趣味投票")

        auto_poll_settings = config.get("channel_auto_poll_settings", {})
        auto_poll_settings[channel] = {"enabled": request.enabled}
        config["channel_auto_poll_settings"] = auto_poll_settings
        write_config(config)

        logger.info(f"已通过 WebUI 更新频道自动投票设置: {channel}")
        return {"success": True, "message": f"频道自动投票设置已更新: {channel}"}

    except HTTPException:
        raise
    except Exception as e:
        logger.error(f"更新频道自动投票设置失败: {type(e).__name__}: {e}", exc_info=True)
        raise HTTPException(status_code=500, detail=str(e)) from e


@router.delete("/auto-poll/{channel:path}")
async def delete_channel_auto_poll(channel: str):
    """删除指定频道的自动投票配置"""
    try:
        channel = normalize_channel_id(channel)
        config = get_config()

        if not config.get("enable_auto_poll", False):
            raise HTTPException(status_code=400, detail="请先开启全局自动趣味投票")

        auto_poll_settings = config.get("channel_auto_poll_settings", {})
        if channel in auto_poll_settings:
            del auto_poll_settings[channel]
            config["channel_auto_poll_settings"] = auto_poll_settings
            write_config(config)
            logger.info(f"已通过 WebUI 删除频道自动投票配置: {channel}")
            return {"success": True, "message": f"频道自动投票配置已删除: {channel}"}
        return {"success": True, "message": "该频道无自动投票配置"}

    except HTTPException:
        raise
    except Exception as e:
        logger.error(f"删除频道自动投票配置失败: {type(e).__name__}: {e}", exc_info=True)
        raise HTTPException(status_code=500, detail=str(e)) from e


@router.get("/channels")
async def get_channels_for_interaction():
    """获取所有频道列表（用于互动设置配置）"""
    try:
        config = get_config()
        channels = config.get("channels", [])
        return {"success": True, "data": {"channels": channels}}

    except Exception as e:
        logger.error(f"获取频道列表失败: {type(e).__name__}: {e}", exc_info=True)
        raise HTTPException(status_code=500, detail=str(e)) from e


# ==================== 评论区欢迎 ====================


@router.get("/comment-welcome")
async def get_comment_welcome_config():
    """获取评论区欢迎配置"""
    try:
        config = get_config()
        return {
            "success": True,
            "data": {
                "default": config.get("comment_welcome", {}).get("default", {}),
                "channel_overrides": config.get("channel_comment_welcome", {}),
            },
        }

    except Exception as e:
        logger.error(f"获取评论区欢迎配置失败: {type(e).__name__}: {e}", exc_info=True)
        raise HTTPException(status_code=500, detail=str(e)) from e


@router.put("/comment-welcome/default")
async def update_default_comment_welcome(request: CommentWelcomeUpdate):
    """更新默认评论区欢迎配置"""
    try:
        config = get_config()

        if "comment_welcome" not in config:
            config["comment_welcome"] = {}
        if "default" not in config["comment_welcome"]:
            config["comment_welcome"]["default"] = {}

        default_config = config["comment_welcome"]["default"]

        if request.enabled is not None:
            default_config["enabled"] = request.enabled
        if request.welcome_message is not None:
            default_config["welcome_message"] = request.welcome_message
        if request.button_text is not None:
            default_config["button_text"] = request.button_text
        if request.button_action is not None:
            default_config["button_action"] = request.button_action

        config["comment_welcome"]["default"] = default_config
        write_config(config)

        logger.info("已通过 WebUI 更新默认评论区欢迎配置")
        return {"success": True, "message": "默认评论区欢迎配置已更新"}

    except Exception as e:
        logger.error(f"更新默认评论区欢迎配置失败: {type(e).__name__}: {e}", exc_info=True)
        raise HTTPException(status_code=500, detail=str(e)) from e


@router.put("/comment-welcome/{channel:path}")
async def update_channel_comment_welcome(channel: str, request: CommentWelcomeUpdate):
    """更新指定频道的评论区欢迎配置"""
    try:
        channel = normalize_channel_id(channel)
        config = get_config()

        channel_welcome = config.get("channel_comment_welcome", {})
        current = channel_welcome.get(channel, {})

        if request.enabled is not None:
            current["enabled"] = request.enabled

        channel_welcome[channel] = current
        config["channel_comment_welcome"] = channel_welcome
        write_config(config)

        logger.info(f"已通过 WebUI 更新频道评论区欢迎配置: {channel}")
        return {"success": True, "message": f"频道评论区欢迎配置已更新: {channel}"}

    except Exception as e:
        logger.error(f"更新频道评论区欢迎配置失败: {type(e).__name__}: {e}", exc_info=True)
        raise HTTPException(status_code=500, detail=str(e)) from e
