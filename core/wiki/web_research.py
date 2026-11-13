"""Bounded, shared search for the assistant and Wiki review."""

import asyncio
import ipaddress
import json
import re
import socket
from html.parser import HTMLParser
from urllib.parse import urljoin, urlsplit, urlunsplit

import aiohttp
from pydantic import Field, field_validator

from core.config.runtime import runtime_config

from .models import Strict, digest, utc
from .store import decode, encode
from .telegram_login import bounded_body

DEFAULTS = {
    "deepseek": "https://api.deepseek.com",
    "tavily": "https://api.tavily.com",
    "serpapi": "https://serpapi.com",
}


class SearchUnavailable(ValueError):
    pass


def public_url(value):
    if not isinstance(value, str) or len(value) > 2048 or re.search(r"[\s\\\x00-\x20\x7f]", value):
        raise ValueError("Invalid public URL")
    parsed = urlsplit(value)
    host = parsed.hostname
    if (
        parsed.scheme not in {"https", "http"}
        or not host
        or parsed.username is not None
        or parsed.port not in {None, 443 if parsed.scheme == "https" else 80}
    ):
        raise ValueError("Invalid public URL")
    if host.lower().rstrip(".") in {"localhost", "localhost.localdomain"}:
        raise ValueError("Invalid public hostname")
    try:
        ip = ipaddress.ip_address(host)
    except ValueError:
        if (
            "." not in host
            or not re.search(r"[a-zA-Z]", host.rstrip(".").rsplit(".", 1)[-1])
            or re.fullmatch(r"(?:0x[0-9a-f]+|[0-9]+)(?:\.(?:0x[0-9a-f]+|[0-9]+))*\.?", host, re.I)
            or host.rstrip(".").endswith((".local", ".internal", ".localhost"))
        ):
            raise ValueError("Invalid public hostname") from None
    else:
        if not ip.is_global or ip.is_multicast:
            raise ValueError("Invalid public address")
    return urlunsplit(parsed._replace(fragment=""))


class Provider(Strict):
    enabled: bool = False
    base_url: str = ""
    api_key: str = Field(default="", max_length=1024)
    cost_units: int = Field(default=1, ge=1, le=1000000)

    @field_validator("base_url")
    @classmethod
    def validate_base(cls, value):
        if not value:
            return value
        url = urlsplit(public_url(value))
        if url.scheme != "https" or url.query or url.fragment or "#" in value:
            raise ValueError("Search provider requires an HTTPS base URL")
        return url.geturl().rstrip("/")


class SearchSettings(Strict):
    enabled: bool = False
    order: list[str] = Field(default_factory=lambda: list(DEFAULTS), max_length=3)
    providers: dict[str, Provider] = Field(default_factory=dict)
    daily_calls: int = Field(default=100, ge=1, le=10000)
    daily_cost_units: int = Field(default=100, ge=1, le=100000000)
    result_limit: int = Field(default=5, ge=1, le=10)
    timeout: int = Field(default=10, ge=2, le=30)
    concurrency: int = Field(default=3, ge=1, le=12)
    ttl_hours: int = Field(default=24, ge=1, le=168)

    @field_validator("providers", "order")
    @classmethod
    def supported(cls, value):
        if set(value) - DEFAULTS.keys() or len(set(value)) != len(value):
            raise ValueError("Unknown or duplicate search provider")
        return value


def search_settings():
    return SearchSettings.model_validate(runtime_config.read().get("wiki_search", {}))


class PublicResolver(aiohttp.ThreadedResolver):
    async def resolve(self, host, port=0, family=socket.AF_UNSPEC):
        rows = await super().resolve(host, port, family)
        if not rows or any(
            not (ip := ipaddress.ip_address(row["host"])).is_global or ip.is_multicast
            for row in rows
        ):
            raise OSError("Non-public address")
        return rows


class PageText(HTMLParser):
    def __init__(self):
        super().__init__(convert_charrefs=True)
        self.skip = 0
        self.parts = []

    def handle_starttag(self, tag, attrs):
        if tag in {"script", "style", "template", "noscript"}:
            self.skip += 1

    def handle_endtag(self, tag):
        if self.skip and tag in {"script", "style", "template", "noscript"}:
            self.skip -= 1

    def handle_data(self, text):
        if not self.skip:
            self.parts.append(text)


def provider_request(name, base, key, query, limit):
    if name == "deepseek":
        return (
            "POST",
            base + "/v1/tools/search",
            {
                "headers": {"Authorization": f"Bearer {key}"},
                "json": {"query": query, "count": limit, "need_content": True},
            },
        )
    if name == "tavily":
        return (
            "POST",
            base + "/search",
            {
                "headers": {"Authorization": f"Bearer {key}"},
                "json": {
                    "query": query,
                    "max_results": limit,
                    "include_raw_content": "text",
                    "include_published_date": True,
                    "search_depth": "basic",
                },
            },
        )
    return (
        "GET",
        base + "/search.json",
        {"params": {"engine": "google", "q": query, "api_key": key}},
    )


def normalized_results(name, payload, limit):
    if not isinstance(payload, dict) or payload.get("error"):
        raise SearchUnavailable("provider_error")
    rows = payload.get("organic_results", []) if name == "serpapi" else payload.get("results")
    if not isinstance(rows, list):
        raise SearchUnavailable("provider_format")
    results = {}
    for row in rows[:50]:
        if not isinstance(row, dict):
            continue
        try:
            url = public_url(row.get("link" if name == "serpapi" else "url"))
        except ValueError:
            continue
        results.setdefault(
            url,
            {
                "url": url,
                "title": str(row.get("title") or "")[:300],
                "provider": name,
                "snippet": str(row.get("content" if name == "tavily" else "snippet") or "")[:800],
                "content": str(row.get("raw_content" if name == "tavily" else "content") or "")[
                    :4500
                ],
                "published_at": str(row.get("published_date") or row.get("date") or "")[:80],
                "fetched_at": utc().isoformat() + "Z",
            },
        )
        if len(results) >= limit:
            break
    return list(results.values())


class WebResearch:
    def __init__(self, store):
        self.store = store
        self.limit = asyncio.Condition()
        self.active = 0
        self.locks = [asyncio.Lock() for _ in range(32)]

    async def reserve(self, provider, cost, settings):
        async with self.store.transaction() as cur:
            await cur.execute(
                "INSERT IGNORE INTO wiki_search_usage(day,provider) VALUES (UTC_DATE(),'total')"
            )
            await cur.execute(
                "SELECT calls,cost_units FROM wiki_search_usage WHERE day=UTC_DATE() AND provider='total' FOR UPDATE"
            )
            row = await cur.fetchone()
            if (
                row["calls"] >= settings.daily_calls
                or row["cost_units"] + cost > settings.daily_cost_units
            ):
                raise SearchUnavailable("budget_exhausted")
            await cur.execute(
                "UPDATE wiki_search_usage SET calls=calls+1,cost_units=cost_units+%s WHERE day=UTC_DATE() AND provider='total'",
                (cost,),
            )
            await cur.execute(
                """INSERT INTO wiki_search_usage VALUES (UTC_DATE(),%s,1,%s)
                AS attempt ON DUPLICATE KEY UPDATE calls=wiki_search_usage.calls+1,cost_units=wiki_search_usage.cost_units+attempt.cost_units""",
                (provider, cost),
            )

    async def cached(self, key, settings, perform):
        async with self.locks[int(key[:4], 16) % len(self.locks)]:
            row = await self.store.one(
                "SELECT result FROM wiki_search_cache WHERE cache_key=%s AND expires_at>UTC_TIMESTAMP(6)",
                (key,),
            )
            if row:
                return decode(row["result"])
            async with self.limit:
                await self.limit.wait_for(lambda: self.active < settings.concurrency)
                self.active += 1
            try:
                result = await perform()
            finally:
                async with self.limit:
                    self.active -= 1
                    self.limit.notify_all()
            await self.store.query(
                """INSERT INTO wiki_search_cache VALUES (%s,%s,DATE_ADD(UTC_TIMESTAMP(6),INTERVAL %s HOUR)) AS entry
                ON DUPLICATE KEY UPDATE result=entry.result,expires_at=entry.expires_at""",
                (key, encode(result), settings.ttl_hours),
            )
            return result

    async def search(self, query, settings=None):
        settings = settings or search_settings()
        query = " ".join(query.split())[:128]
        if not settings.enabled or not query:
            raise SearchUnavailable("search_disabled_or_empty")
        key = digest("search-v2", query, settings.model_dump())

        async def perform():
            for name in settings.order:
                provider = settings.providers.get(name)
                if not provider or not provider.enabled or not provider.api_key:
                    continue
                await self.reserve(name, provider.cost_units, settings)
                method, url, options = provider_request(
                    name,
                    provider.base_url or DEFAULTS[name],
                    provider.api_key,
                    query,
                    settings.result_limit,
                )
                try:
                    async with (
                        self.client(settings.timeout) as client,
                        client.request(method, url, allow_redirects=False, **options) as response,
                    ):
                        if response.status != 200:
                            continue
                        results = normalized_results(
                            name,
                            json.loads(await bounded_body(response, 1024 * 1024)),
                            settings.result_limit,
                        )
                        reachable = await asyncio.gather(
                            *(self.reachable(result["url"]) for result in results)
                        )
                        results = [
                            result
                            for result, available in zip(results, reachable, strict=True)
                            if available
                        ]
                        if results:
                            return {"results": results, "provider": name}
                except (aiohttp.ClientError, TimeoutError, ValueError):
                    continue
            raise SearchUnavailable("no_search_evidence")

        return await self.cached(key, settings, perform)

    @staticmethod
    def client(timeout):
        return aiohttp.ClientSession(
            timeout=aiohttp.ClientTimeout(total=timeout),
            connector=aiohttp.TCPConnector(resolver=PublicResolver()),
        )

    async def fetch(self, url):
        settings = search_settings()
        if not settings.enabled:
            raise SearchUnavailable("fetch_disabled")
        url = public_url(url)

        async def perform():
            await self.reserve("fetch", 1, settings)
            target = url
            try:
                async with (
                    asyncio.timeout(settings.timeout),
                    self.client(settings.timeout) as client,
                ):
                    for _ in range(4):
                        async with client.get(target, allow_redirects=False) as response:
                            if response.status in {
                                301,
                                302,
                                303,
                                307,
                                308,
                            } and response.headers.get("Location"):
                                target = public_url(urljoin(target, response.headers["Location"]))
                                continue
                            if response.status != 200 or response.content_type not in {
                                "text/html",
                                "text/plain",
                                "text/markdown",
                            }:
                                break
                            text = (await bounded_body(response, 512 * 1024)).decode(
                                response.charset or "utf-8", errors="replace"
                            )
                            if response.content_type == "text/html":
                                parser = PageText()
                                parser.feed(text)
                                text = " ".join(parser.parts)
                            return {
                                "url": target,
                                "provider": "fetch",
                                "content": " ".join(text.split())[:12000],
                                "fetched_at": utc().isoformat() + "Z",
                            }
            except (aiohttp.ClientError, TimeoutError, ValueError, LookupError):
                pass
            raise SearchUnavailable("fetch_unavailable")

        return await self.cached(digest("fetch-v1", url), settings, perform)

    async def reachable(self, url):
        try:
            target = public_url(url)
            async with asyncio.timeout(5), self.client(5) as client:
                for _ in range(4):
                    async with client.get(
                        target, allow_redirects=False, headers={"Range": "bytes=0-511"}
                    ) as response:
                        if response.status in {301, 302, 303, 307, 308} and response.headers.get(
                            "Location"
                        ):
                            target = public_url(urljoin(target, response.headers["Location"]))
                            continue
                            return response.status in {200, 206} and bool(
                                await response.content.read(1)
                            )
        except (ValueError, aiohttp.ClientError, TimeoutError):
            pass
        return False

    async def prune(self):
        for table in ("wiki_search_cache", "wiki_fact_checks"):
            await self.store.query(
                f"DELETE FROM {table} WHERE expires_at<=UTC_TIMESTAMP(6) LIMIT 1000"
            )
        await self.store.query(
            "DELETE FROM wiki_search_usage WHERE day<UTC_DATE()-INTERVAL 90 DAY LIMIT 1000"
        )
