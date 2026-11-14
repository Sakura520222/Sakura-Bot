import hashlib
import hmac

from fastapi import APIRouter, HTTPException, Request
from pydantic import Field

from core.settings import get_settings
from core.web_api.deps import get_config, write_config
from core.wiki.models import Strict
from core.wiki.web_research import (
    DEFAULTS,
    FREE_PROVIDERS,
    WEB_PROVIDERS,
    Provider,
    SearchSettings,
    SearchUnavailable,
    search_settings,
    stored_search_settings,
)

from .wiki import ADMIN_AUTH, runtime

PATH = "/api/internal/wiki-research"
router = APIRouter()
admin = APIRouter(dependencies=[ADMIN_AUTH])


class ResearchRequest(Strict):
    value: str = Field(min_length=1, max_length=2048)


@router.post("")
async def research(request: Request, body: ResearchRequest, operation: str):
    settings = get_settings().telegram
    token = settings.qa_bot_token
    expected = (
        hashlib.sha256(f"sakura-wiki-research:v1:{token.get_secret_value()}".encode()).hexdigest()
        if token
        else ""
    )
    provided = request.headers.get("Authorization", "")
    if (
        not expected
        or not provided.isascii()
        or not hmac.compare_digest(provided, f"Bearer {expected}")
    ):
        raise HTTPException(401)
    service = runtime().research
    try:
        if operation == "search":
            return await service.search(body.value, assistant=True)
        if operation == "fetch":
            return await service.fetch(body.value)
    except SearchUnavailable as error:
        code = str(error)
        if operation == "search":
            message = {
                "search_disabled_or_empty": "联网检索未启用或关键词为空。",
                "no_search_provider_configured": "没有启用可用的搜索源。",
                "search_providers_unavailable": "已启用的搜索源本次均未能响应。",
                "no_general_web_provider": "未启用通用网页搜索源，现有专题索引本次未找到可用资料。",
                "no_search_evidence": "已启用的搜索源本次没有返回可用资料，无法据此判断网上是否有新消息。",
            }.get(code, "联网检索暂时不可用。")
            return {"error": code, "message": message}
        message = {
            "fetch_disabled": "公开网页读取未启用。",
            "fetch_unsupported_type": "链接指向暂不支持读取的文件类型。",
            "fetch_no_text": "网页已打开，但没有可提取的正文。",
        }.get(code, "本次无法打开网页。")
        if code.startswith("fetch_http_"):
            message = f"目标网站返回 HTTP {code.removeprefix('fetch_http_')}。"
        return {"error": code, "message": message}
    except ValueError:
        return {"error": "invalid_public_url_or_configuration"}
    raise HTTPException(404)


def masked(settings):
    return {
        **settings.model_dump(exclude={"providers"}),
        "providers": {
            name: {
                **provider.model_dump(exclude={"api_key"}),
                "has_key": bool(provider.api_key),
                "official_url": DEFAULTS[name],
                "requires_key": name not in FREE_PROVIDERS,
                "general_web": name in WEB_PROVIDERS,
            }
            for name in DEFAULTS
            if (provider := settings.providers.get(name, Provider())) is not None
        },
    }


@admin.get("/research")
async def get_research():
    return masked(search_settings())


@admin.put("/research")
async def put_research(request: Request):
    try:
        body = SearchSettings.model_validate(await request.json())
    except ValueError:
        raise HTTPException(422, "搜索设置无效") from None
    config = get_config()
    previous = stored_search_settings(config.get("wiki_search", {}))
    for name, provider in body.providers.items():
        if "api_key" not in provider.model_fields_set and name in previous.providers:
            provider.api_key = previous.providers[name].api_key
    config["wiki_search"] = body.model_dump()
    write_config(config)
    return masked(body)
