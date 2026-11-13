import hashlib
import hmac

from fastapi import APIRouter, HTTPException, Request
from pydantic import Field

from core.settings import get_settings
from core.web_api.deps import get_config, write_config
from core.wiki.models import Strict
from core.wiki.web_research import (
    DEFAULTS,
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
            return await service.search(body.value)
        if operation == "fetch":
            return await service.fetch(body.value)
    except SearchUnavailable as error:
        return {"error": str(error)}
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
