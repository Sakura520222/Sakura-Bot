from types import SimpleNamespace
from unittest.mock import AsyncMock

import pytest

from core.wiki.web_research import (
    DEFAULTS,
    PageText,
    Provider,
    SearchSettings,
    SearchUnavailable,
    WebResearch,
    normalized_results,
    provider_request,
    public_url,
    stored_search_settings,
)


@pytest.mark.parametrize(
    "url",
    [
        "file:///etc/passwd",
        "http://127.0.0.1",
        "https://[::1]",
        "http://169.254.169.254",
        "http://127.1",
        "http://0x7f.0x1",
        "http://localhost.localdomain",
        "https://foo.internal",
        "https://user@public.example.com",
        "https://example.org:8080",
    ],
)
def test_only_public_urls(url):
    with pytest.raises(ValueError):
        public_url(url)


@pytest.mark.parametrize(
    ("name", "path", "collection", "field"),
    [
        ("deepseek", "/v1/tools/search", "results", "url"),
        ("tavily", "/search", "results", "url"),
        ("serpapi", "/search.json", "organic_results", "link"),
    ],
)
def test_provider_requests_and_normalization(name, path, collection, field):
    method, url, options = provider_request(name, "secret", "question", 3)
    assert url == DEFAULTS[name] + path and method == ("GET" if name == "serpapi" else "POST")
    assert "secret" not in url and options
    records = normalized_results(
        name,
        {
            collection: [
                {field: "https://example.org/result#one", "title": "Source"},
                {field: "https://example.org/result#two", "title": "Duplicate"},
                {field: "http://10.0.0.1", "title": "Private"},
            ]
        },
        3,
    )
    assert len(records) == 1 and records[0]["url"] == "https://example.org/result"
    assert records[0]["provider"] == name


def test_legacy_search_settings_keep_keys_without_custom_endpoints_or_limits():
    result = stored_search_settings(
        {
            "daily_calls": 100,
            "daily_cost_units": 1,
            "providers": {
                "tavily": {
                    "enabled": True,
                    "api_key": "fixture-key",
                    "base_url": "https://old.example",
                    "cost_units": 50,
                }
            },
        }
    )
    assert result.providers["tavily"].model_dump() == {"enabled": True, "api_key": "fixture-key"}
    assert "daily_calls" not in result.model_dump()


@pytest.mark.parametrize(
    ("name", "body", "expected"),
    [
        (
            "wikipedia",
            {
                "pages": [
                    {
                        "key": "崩壞：星穹鐵道",
                        "title": "星穹铁道",
                        "excerpt": "<span>星穹铁道</span>",
                    }
                ]
            },
            "https://zh.wikipedia.org/wiki/",
        ),
        (
            "crossref",
            {"message": {"items": [{"URL": "https://doi.org/10.1/abc", "title": ["Paper"]}]}},
            "https://doi.org/",
        ),
        (
            "github",
            {
                "items": [
                    {
                        "html_url": "https://github.com/example/project",
                        "full_name": "example/project",
                        "description": "Project",
                    }
                ]
            },
            "https://github.com/",
        ),
    ],
)
def test_free_provider_uses_official_endpoint_without_key(name, body, expected):
    method, url, options = provider_request(name, "", "星穹铁道", 3)
    assert method == "GET" and "Authorization" not in options["headers"]
    [result] = normalized_results(name, body, 3, endpoint=url)
    assert result["url"].startswith(expected)
    assert "<span>" not in result["snippet"]


@pytest.mark.asyncio
async def test_reachable_accepts_success_after_public_redirect():
    class Response:
        def __init__(self, status, headers):
            self.status = status
            self.headers = headers
            self.content = SimpleNamespace(read=AsyncMock(return_value=b"<"))

        async def __aenter__(self):
            return self

        async def __aexit__(self, *args):
            pass

    class Client:
        async def __aenter__(self):
            return self

        async def __aexit__(self, *args):
            pass

        def get(self, url, *, allow_redirects):
            assert not allow_redirects
            return responses[url]

    responses = {
        "https://example.org/": Response(302, {"Location": "/article"}),
        "https://example.org/article": Response(200, {}),
        "https://example.org/missing": Response(404, {}),
        "https://example.org/private": Response(302, {"Location": "http://127.0.0.1/"}),
    }
    service = WebResearch(None)
    service.client = lambda _: Client()
    assert await service.reachable("https://example.org/")
    assert not await service.reachable("https://example.org/missing")
    assert not await service.reachable("https://example.org/private")


@pytest.mark.asyncio
async def test_assistant_keeps_index_snippets_and_prefers_general_web_search():
    class Research(WebResearch):
        async def cached(self, key, settings, perform):
            return await perform()

    service = Research(None)
    service.request_provider = AsyncMock(
        return_value={"results": [{"url": "https://example.org/", "content": "Indexed text"}]}
    )
    service.reachable = AsyncMock(return_value=False)
    settings = SearchSettings(
        enabled=True,
        order=["wikipedia", "tavily"],
        providers={
            "wikipedia": Provider(enabled=True),
            "tavily": Provider(enabled=True, api_key="test"),
        },
    )
    answer = await service.search("千恋万花", settings, assistant=True)
    assert answer["scope"] == "web" and answer["provider"] == "tavily"
    assert answer["results"][0]["snippet"] == "Indexed text"
    assert answer["results"][0]["page_reachable"] is False
    assert service.request_provider.await_count == 1
    with pytest.raises(SearchUnavailable, match="no_search_evidence"):
        await service.search("千恋万花", settings)


def test_page_text_extracts_title_and_article_without_navigation():
    parser = PageText()
    parser.feed(
        "<title>示例 &amp; 更新</title><nav>导航</nav><main><article>"
        "<h1>标题</h1><p>游戏<strong>动画</strong>消息</p><script>隐藏文本</script>"
        "<p>第二段</p></article></main><footer>页脚</footer>"
    )
    parser.close()
    assert "".join(parser.title) == "示例 & 更新"
    assert parser.content() == "标题\n游戏动画消息\n第二段"
