"""Bounded, shared search for the assistant and Wiki review."""

import asyncio
import html
import ipaddress
import json
import re
import socket
from html.parser import HTMLParser
from urllib.parse import quote, urljoin, urlsplit, urlunsplit

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
    "wikipedia": "https://www.wikipedia.org",
    "crossref": "https://api.crossref.org",
    "github": "https://api.github.com",
}
FREE_PROVIDERS = {"wikipedia": 1, "crossref": 1, "github": 6}
WEB_PROVIDERS = {"deepseek", "tavily", "serpapi"}
USER_AGENT = "Sakura-Bot/2.5 (https://github.com/Sakura520222/Sakura-Bot)"


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
    api_key: str = Field(default="", max_length=1024)


class SearchSettings(Strict):
    enabled: bool = False
    order: list[str] = Field(default_factory=lambda: list(DEFAULTS), max_length=len(DEFAULTS))
    providers: dict[str, Provider] = Field(default_factory=dict)
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


def stored_search_settings(raw):
    if not isinstance(raw, dict):
        return SearchSettings.model_validate(raw)
    data = {
        key: value for key, value in raw.items() if key not in {"daily_calls", "daily_cost_units"}
    }
    if isinstance(data.get("providers"), dict):
        data["providers"] = {
            name: {
                key: value
                for key, value in provider.items()
                if key not in {"base_url", "cost_units"}
            }
            if isinstance(provider, dict)
            else provider
            for name, provider in data["providers"].items()
        }
    return SearchSettings.model_validate(data)


def search_settings():
    return stored_search_settings(runtime_config.read().get("wiki_search", {}))


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
        self.skip = []
        self.parts = []
        self.main_parts = []
        self.title = []
        self.in_title = False
        self.in_main = 0

    def handle_starttag(self, tag, attrs):
        if tag in {"script", "style", "template", "noscript", "nav", "footer", "aside"}:
            self.skip.append(tag)
        if self.skip:
            return
        if tag == "title":
            self.in_title = True
        if tag in {"main", "article"}:
            self.in_main += 1
        if tag in {"p", "br", "li", "h1", "h2", "h3", "blockquote", "pre", "tr", "section"}:
            self.append("\n")

    def handle_endtag(self, tag):
        if self.skip:
            if self.skip[-1] == tag:
                self.skip.pop()
            return
        if tag == "title":
            self.in_title = False
        if tag in {"p", "li", "h1", "h2", "h3", "blockquote", "pre", "tr", "main", "article"}:
            self.append("\n")
        if self.in_main and tag in {"main", "article"}:
            self.in_main -= 1

    def handle_data(self, text):
        if self.skip:
            return
        if self.in_title:
            self.title.append(text)
        else:
            self.append(text)

    def append(self, text):
        self.parts.append(text)
        if self.in_main:
            self.main_parts.append(text)

    def content(self):
        parts = self.main_parts if any(part.strip() for part in self.main_parts) else self.parts
        return "\n".join(
            line for raw in "".join(parts).splitlines() if (line := " ".join(raw.split()))
        )


def provider_request(name, key, query, limit):
    base = DEFAULTS[name]
    headers = {"User-Agent": USER_AGENT}
    if name == "wikipedia":
        wiki = "zh" if re.search(r"[\u3400-\u9fff]", query) else "en"
        return (
            "GET",
            f"https://{wiki}.wikipedia.org/w/rest.php/v1/search/page",
            {"headers": headers, "params": {"q": query, "limit": limit}},
        )
    if name == "crossref":
        return (
            "GET",
            base + "/works",
            {
                "headers": headers,
                "params": {
                    "query.bibliographic": query,
                    "rows": limit,
                    "select": "title,URL,published,abstract",
                },
            },
        )
    if name == "github":
        return (
            "GET",
            base + "/search/repositories",
            {"headers": headers, "params": {"q": query, "per_page": limit}},
        )
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


def normalized_results(name, payload, limit, *, endpoint=""):
    if not isinstance(payload, dict) or payload.get("error"):
        raise SearchUnavailable("provider_error")
    if name == "wikipedia":
        rows = [
            {
                "url": f"https://{urlsplit(endpoint).hostname}/wiki/{quote(row['key'], safe='')}",
                "title": row.get("title"),
                "snippet": plain_snippet(row.get("excerpt")),
                "content": plain_snippet(row.get("description")),
            }
            for row in payload.get("pages", [])
            if isinstance(row, dict) and isinstance(row.get("key"), str)
        ]
    elif name == "crossref":
        rows = [
            {
                "url": row.get("URL"),
                "title": (row.get("title") or [""])[0],
                "snippet": plain_snippet(row.get("abstract") or (row.get("title") or [""])[0]),
                "date": "-".join(map(str, (row.get("published", {}).get("date-parts") or [[]])[0])),
            }
            for row in payload.get("message", {}).get("items", [])
            if isinstance(row, dict)
        ]
    elif name == "github":
        rows = [
            {
                "url": row.get("html_url"),
                "title": row.get("full_name"),
                "snippet": row.get("description"),
            }
            for row in payload.get("items", [])
            if isinstance(row, dict)
        ]
    else:
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


def plain_snippet(value):
    return html.unescape(re.sub(r"<[^>]*>", "", str(value or "")))


class WebResearch:
    def __init__(self, store):
        self.store = store
        self.limit = asyncio.Condition()
        self.active = 0
        self.locks = [asyncio.Lock() for _ in range(32)]
        self.provider_locks = {name: asyncio.Lock() for name in FREE_PROVIDERS}
        self.next_provider_call = dict.fromkeys(FREE_PROVIDERS, 0.0)

    async def record_attempt(self, provider):
        await self.store.query(
            """INSERT INTO wiki_search_usage(day,provider,calls) VALUES (UTC_DATE(),%s,1)
            ON DUPLICATE KEY UPDATE calls=wiki_search_usage.calls+1""",
            (provider,),
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

    async def search(self, query, settings=None, *, assistant=False):
        settings = settings or search_settings()
        query = " ".join(query.split())[:128]
        if not settings.enabled or not query:
            raise SearchUnavailable("search_disabled_or_empty")
        key = digest("search-v3", query, settings.model_dump(), assistant)

        async def perform():
            order = [
                name
                for name in settings.order
                if (provider := settings.providers.get(name))
                and provider.enabled
                and (name in FREE_PROVIDERS or provider.api_key)
            ]
            if not order:
                raise SearchUnavailable("no_search_provider_configured")
            if assistant:
                order = sorted(order, key=lambda name: name not in WEB_PROVIDERS)
            failures = 0
            for name in order:
                method, url, options = provider_request(
                    name,
                    settings.providers[name].api_key,
                    query,
                    settings.result_limit,
                )
                try:
                    payload = await self.request_provider(
                        name, method, url, options, settings.timeout
                    )
                    results = normalized_results(name, payload, settings.result_limit, endpoint=url)
                    reachable = await asyncio.gather(
                        *(self.reachable(result["url"]) for result in results)
                    )
                    if assistant:
                        usable = [
                            {**result, "page_reachable": available}
                            for result, available in zip(results, reachable, strict=True)
                            if available or result["snippet"] or result["content"]
                        ]
                        if usable:
                            return {
                                "results": usable,
                                "provider": name,
                                "scope": "web" if name in WEB_PROVIDERS else name,
                                "guidance": "按返回来源的覆盖范围作答。page_reachable=false 仅有搜索索引资料，不能声称读过原网页，也不能用空结果推断没有新闻。",
                            }
                    results = [
                        result
                        for result, available in zip(results, reachable, strict=True)
                        if available
                    ]
                    if results:
                        return {"results": results, "provider": name}
                except (
                    aiohttp.ClientError,
                    TimeoutError,
                    ValueError,
                    TypeError,
                    AttributeError,
                    IndexError,
                ):
                    failures += 1
            if failures == len(order):
                raise SearchUnavailable("search_providers_unavailable")
            if assistant and WEB_PROVIDERS.isdisjoint(order):
                raise SearchUnavailable("no_general_web_provider")
            raise SearchUnavailable("no_search_evidence")

        return await self.cached(key, settings, perform)

    async def request_provider(self, name, method, url, options, timeout):
        async def request():
            await self.record_attempt(name)
            async with (
                self.client(timeout) as client,
                client.request(method, url, allow_redirects=False, **options) as response,
            ):
                if response.status != 200:
                    if response.status in {403, 429} and name in FREE_PROVIDERS:
                        retry = response.headers.get("Retry-After", "")
                        delay = min(3600, int(retry)) if re.fullmatch(r"[0-9]{1,6}", retry) else 60
                        self.next_provider_call[name] = asyncio.get_running_loop().time() + max(
                            60, delay
                        )
                    raise SearchUnavailable("provider_unavailable")
                return json.loads(await bounded_body(response, 1024 * 1024))

        if name not in FREE_PROVIDERS:
            return await request()
        async with self.provider_locks[name]:
            clock = asyncio.get_running_loop()
            delay = self.next_provider_call[name] - clock.time()
            if delay > FREE_PROVIDERS[name] + 0.1:
                raise SearchUnavailable("provider_rate_limited")
            if delay > 0:
                await asyncio.sleep(delay)
            self.next_provider_call[name] = clock.time() + FREE_PROVIDERS[name]
            return await request()

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
            await self.record_attempt("fetch")
            target = url
            try:
                async with (
                    asyncio.timeout(settings.timeout),
                    self.client(settings.timeout) as client,
                ):
                    for _ in range(4):
                        async with client.get(
                            target,
                            allow_redirects=False,
                            headers={
                                "User-Agent": USER_AGENT,
                                "Accept": "text/html, text/plain;q=0.9, text/markdown;q=0.9, application/xhtml+xml;q=0.8",
                            },
                        ) as response:
                            if response.status in {
                                301,
                                302,
                                303,
                                307,
                                308,
                            } and response.headers.get("Location"):
                                target = public_url(urljoin(target, response.headers["Location"]))
                                continue
                            if response.status != 200:
                                raise SearchUnavailable(f"fetch_http_{response.status}")
                            if response.content_type not in {
                                "text/html",
                                "application/xhtml+xml",
                                "text/plain",
                                "text/markdown",
                            }:
                                raise SearchUnavailable("fetch_unsupported_type")
                            chunks = bytearray()
                            max_bytes = 512 * 1024
                            async for chunk in response.content.iter_chunked(16384):
                                chunks.extend(chunk[: max_bytes + 1 - len(chunks)])
                                if len(chunks) > max_bytes:
                                    break
                            text = chunks[:max_bytes].decode(
                                response.charset or "utf-8", errors="replace"
                            )
                            title = ""
                            if response.content_type in {"text/html", "application/xhtml+xml"}:
                                parser = PageText()
                                parser.feed(text)
                                parser.close()
                                text = parser.content()
                                title = " ".join("".join(parser.title).split())[:300]
                            text = text.strip()
                            if not text:
                                raise SearchUnavailable("fetch_no_text")
                            return {
                                "url": target,
                                "provider": "fetch",
                                "title": title,
                                "content": text[:12000],
                                "truncated": len(chunks) > max_bytes or len(text) > 12000,
                                "fetched_at": utc().isoformat() + "Z",
                            }
            except SearchUnavailable:
                raise
            except (aiohttp.ClientError, TimeoutError, ValueError, LookupError):
                pass
            raise SearchUnavailable("fetch_unavailable")

        return await self.cached(digest("fetch-v2", url), settings, perform)

    async def reachable(self, url):
        try:
            target = public_url(url)
            async with asyncio.timeout(5), self.client(5) as client:
                for _ in range(4):
                    async with client.get(target, allow_redirects=False) as response:
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
