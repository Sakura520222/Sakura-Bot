import pytest

from core.wiki.web_research import normalized_results, provider_request, public_url


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
    method, url, options = provider_request(
        name, "https://search.example.org", "secret", "question", 3
    )
    assert url.endswith(path) and method == ("GET" if name == "serpapi" else "POST")
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
