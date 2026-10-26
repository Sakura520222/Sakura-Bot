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
AI 配置 API 路由

提供 AI 模型配置和提示词管理功能。
"""

import logging
from urllib.parse import urlsplit

import aiofiles
from fastapi import APIRouter, HTTPException

from core.config import DEFAULT_POLL_PROMPT, DEFAULT_PROMPT, DEFAULT_QA_PERSONA
from core.config.ai_runtime import effective_ai_config
from core.config.persistence import ConfigConflictError, ConfigPersistenceError
from core.config.qa_quota import effective_qa_quota
from core.web_api.deps import get_config, write_config
from core.web_api.schemas.config import (
    AIConfigResponse,
    AIConfigUpdateRequest,
    PromptResponse,
    PromptUpdateRequest,
    QAQuotaUpdateRequest,
)

logger = logging.getLogger(__name__)

router = APIRouter()

# 提示词文件路径映射
PROMPT_FILES = {
    "summary": "data/prompt.txt",
    "poll": "data/poll_prompt.txt",
    "qa": "data/qa_persona.txt",
}

# 默认提示词
DEFAULT_PROMPTS = {
    "summary": DEFAULT_PROMPT,
    "poll": DEFAULT_POLL_PROMPT,
    "qa": DEFAULT_QA_PERSONA,
}


@router.get("/config")
async def get_ai_config():
    """获取 AI 配置信息"""
    try:
        settings = effective_ai_config(get_config())
        api_key = settings.api_key
        api_key_preview = ""
        if api_key:
            api_key_preview = f"{api_key[:4]}...{api_key[-4:]}" if len(api_key) > 8 else "***"

        return {
            "success": True,
            "data": AIConfigResponse(
                base_url=settings.base_url,
                model=settings.model,
                api_key_set=bool(api_key),
                api_key_preview=api_key_preview,
            ).model_dump(),
        }
    except Exception as e:
        logger.error(f"获取 AI 配置失败: {type(e).__name__}: {e}", exc_info=True)
        raise HTTPException(status_code=500, detail=str(e)) from e


@router.put("/config")
async def update_ai_config(request: AIConfigUpdateRequest):
    """更新 AI 配置"""
    try:
        updates = request.model_dump(exclude_none=True)
        if any(any(ord(char) < 32 for char in value) for value in updates.values()):
            raise HTTPException(status_code=400, detail="AI 配置不能包含换行或控制字符")
        if "base_url" in updates:
            try:
                url = urlsplit(updates["base_url"])
                valid = (
                    url.scheme in {"http", "https"}
                    and url.hostname
                    and not (url.username or url.password or url.query or url.fragment)
                )
                _ = url.port
            except ValueError:
                valid = False
            if not valid:
                raise HTTPException(status_code=400, detail="API 地址必须为有效的 HTTP(S) 地址")
        config = get_config()
        config.update(updates)
        write_config(config)

        # 主进程立即切换；助手进程在下一次请求时读取同一配置文件。
        from core.ai.ai_client import get_async_ai_runtime

        get_async_ai_runtime()
        logger.info("已通过 WebUI 更新 AI 配置并立即生效")
        return {"success": True, "message": "AI 配置已保存并立即生效，后续请求使用新配置"}
    except HTTPException:
        raise
    except ConfigConflictError as error:
        raise HTTPException(status_code=409, detail=str(error)) from None
    except ConfigPersistenceError as error:
        raise HTTPException(status_code=400, detail=str(error)) from None
    except Exception:
        # 不输出 SDK 异常原文，避免凭据或连接参数进入响应和日志。
        logger.error("更新 AI 配置失败")
        raise HTTPException(status_code=500, detail="AI 配置未能应用，请重试") from None


@router.get("/qa-quota")
async def get_qa_quota():
    try:
        return {"success": True, "data": effective_qa_quota(get_config()).model_dump()}
    except ConfigPersistenceError as error:
        raise HTTPException(status_code=400, detail=str(error)) from None


@router.put("/qa-quota")
async def update_qa_quota(request: QAQuotaUpdateRequest):
    updates = request.model_dump(exclude_none=True)
    if not updates:
        raise HTTPException(status_code=400, detail="请填写需要修改的每日次数")
    try:
        config = get_config()
        config.setdefault("qa_bot_quota", {}).update(updates)
        write_config(config)
        return {
            "success": True,
            "data": effective_qa_quota(get_config()).model_dump(),
            "message": "助手 Bot 每日次数已保存并立即生效，当天已用次数保持不变",
        }
    except ConfigConflictError as error:
        raise HTTPException(status_code=409, detail=str(error)) from None
    except ConfigPersistenceError as error:
        raise HTTPException(status_code=400, detail=str(error)) from None


@router.get("/prompts")
async def list_prompts():
    """获取所有提示词"""
    try:
        prompts = []
        for prompt_type, file_path in PROMPT_FILES.items():
            content = await _read_prompt_file(file_path)
            default_content = DEFAULT_PROMPTS.get(prompt_type, "")
            is_default = content == default_content if content else False

            prompts.append(
                PromptResponse(
                    prompt_type=prompt_type,
                    content=content,
                    is_default=is_default,
                ).model_dump()
            )

        return {"success": True, "data": prompts}

    except Exception as e:
        logger.error(f"获取提示词列表失败: {type(e).__name__}: {e}", exc_info=True)
        raise HTTPException(status_code=500, detail=str(e)) from e


@router.get("/prompts/{prompt_type}")
async def get_prompt(prompt_type: str):
    """获取指定类型的提示词"""
    if prompt_type not in PROMPT_FILES:
        raise HTTPException(status_code=400, detail=f"无效的提示词类型: {prompt_type}")

    try:
        file_path = PROMPT_FILES[prompt_type]
        content = await _read_prompt_file(file_path)
        default_content = DEFAULT_PROMPTS.get(prompt_type, "")
        is_default = content == default_content if content else False

        return {
            "success": True,
            "data": PromptResponse(
                prompt_type=prompt_type,
                content=content,
                is_default=is_default,
            ).model_dump(),
        }

    except Exception as e:
        logger.error(f"获取提示词失败: {type(e).__name__}: {e}", exc_info=True)
        raise HTTPException(status_code=500, detail=str(e)) from e


@router.put("/prompts/{prompt_type}")
async def update_prompt(prompt_type: str, request: PromptUpdateRequest):
    """更新指定类型的提示词"""
    if prompt_type not in PROMPT_FILES:
        raise HTTPException(status_code=400, detail=f"无效的提示词类型: {prompt_type}")

    try:
        file_path = PROMPT_FILES[prompt_type]
        async with aiofiles.open(file_path, "w", encoding="utf-8") as f:
            await f.write(request.content)

        logger.info(f"已通过 WebUI 更新提示词: {prompt_type}")
        return {"success": True, "message": f"提示词已更新: {prompt_type}"}

    except Exception as e:
        logger.error(f"更新提示词失败: {type(e).__name__}: {e}", exc_info=True)
        raise HTTPException(status_code=500, detail=str(e)) from e


@router.post("/prompts/{prompt_type}/reset")
async def reset_prompt(prompt_type: str):
    """重置提示词为默认值"""
    if prompt_type not in PROMPT_FILES:
        raise HTTPException(status_code=400, detail=f"无效的提示词类型: {prompt_type}")

    try:
        file_path = PROMPT_FILES[prompt_type]
        default_content = DEFAULT_PROMPTS.get(prompt_type, "")

        async with aiofiles.open(file_path, "w", encoding="utf-8") as f:
            await f.write(default_content)

        logger.info(f"已通过 WebUI 重置提示词: {prompt_type}")
        return {"success": True, "message": f"提示词已重置为默认值: {prompt_type}"}

    except Exception as e:
        logger.error(f"重置提示词失败: {type(e).__name__}: {e}", exc_info=True)
        raise HTTPException(status_code=500, detail=str(e)) from e


async def _read_prompt_file(file_path: str) -> str:
    """读取提示词文件内容"""
    try:
        async with aiofiles.open(file_path, encoding="utf-8") as f:
            return (await f.read()).strip()
    except FileNotFoundError:
        return ""
