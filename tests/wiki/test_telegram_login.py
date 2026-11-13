import base64
import hashlib
import time
from types import SimpleNamespace
from urllib.parse import parse_qs, urlsplit

import jwt
import pytest
from cryptography.hazmat.primitives.asymmetric import rsa
from fastapi import HTTPException
from pydantic import SecretStr

from core.settings import WikiLoginSettings
from core.web_api.routes import wiki_account
from core.web_api.routes.wiki_account import safe_wiki_path
from core.wiki import telegram_login
from core.wiki.community import selection_text
from core.wiki.telegram_login import (
    ISSUER,
    LoginConfig,
    authorization_url,
    download_avatar,
    verify_identity,
)


def test_selection_matches_rendered_markdown():
    assert (
        selection_text("**Sakura** 支持 [[中文检索]] 与 [使用指南](https://example.org)。")
        == "Sakura 支持 中文检索 与 使用指南。"
    )


async def test_webui_settings_hot_apply_and_keep_secret_on_omission(monkeypatch):
    raw = {}
    settings = SimpleNamespace(
        wiki_login=WikiLoginSettings(),
        telegram=SimpleNamespace(qa_bot_token=SecretStr("123456:fixture")),
    )
    monkeypatch.setattr(wiki_account, "get_settings", lambda: settings)
    monkeypatch.setattr(wiki_account, "get_config", lambda: dict(raw))
    monkeypatch.setattr(wiki_account, "write_config", raw.update)
    monkeypatch.setattr(telegram_login.runtime_config, "read", lambda: raw)
    data = {
        "enabled": True,
        "origin": "https://wiki.example.test",
        "assistant_enabled": True,
        "assistant_checkin": True,
    }

    class Update:
        def __init__(self, body):
            self.body = body

        async def json(self):
            return self.body

    assert not (await wiki_account.read_account_settings())["enabled"]
    with pytest.raises(HTTPException, match="HTTPS"):
        await wiki_account.update_account_settings(Update(data))
    view = await wiki_account.update_account_settings(
        Update({**data, "client_secret": "fixture-secret"})
    )
    assert view["enabled"] and view["assistant_enabled"] and view["has_secret"]
    assert "fixture-secret" not in str(view)
    assert (
        telegram_login.login_config(settings).callback
        == "https://wiki.example.test/api/wiki/account/callback"
    )
    await wiki_account.update_account_settings(Update({**data, "assistant_enabled": False}))
    assert not telegram_login.wiki_login_settings(settings).assistant_enabled
    assert (
        telegram_login.wiki_login_settings(settings).client_secret.get_secret_value()
        == "fixture-secret"
    )


def test_authorization_is_bound_to_configured_bot_and_pkce():
    config = LoginConfig("https://wiki.example.test", "123456", "secret")
    url = urlsplit(authorization_url(config, "state", "nonce", "verifier"))
    params = parse_qs(url.query)
    assert url.scheme == "https" and url.netloc == "oauth.telegram.org" and url.path == "/auth"
    assert params["redirect_uri"] == [config.callback]
    assert params["client_id"] == [config.client_id]
    assert params["scope"] == ["openid profile"]
    assert params["state"] == ["state"] and params["nonce"] == ["nonce"]
    assert params["code_challenge_method"] == ["S256"]
    challenge = base64.urlsafe_b64encode(hashlib.sha256(b"verifier").digest()).decode().rstrip("=")
    assert params["code_challenge"] == [challenge] and "secret" not in url.geturl()


@pytest.fixture(scope="module")
def signing_keys():
    private = rsa.generate_private_key(public_exponent=65537, key_size=2048)
    jwk = jwt.PyJWK.from_json(
        jwt.algorithms.RSAAlgorithm.to_jwk(private.public_key()), algorithm="RS256"
    )
    return private, jwk


def id_claims():
    now = int(time.time())
    return {
        "iss": ISSUER,
        "sub": "opaque-123",
        "aud": "123456",
        "iat": now,
        "exp": now + 300,
        "nonce": "nonce",
        "id": 987654321,
        "name": "测试用户",
        "preferred_username": "test_user",
    }


def test_verified_identity_uses_numeric_id_and_opaque_subject(signing_keys):
    private, public = signing_keys
    token = jwt.encode(id_claims(), private, algorithm="RS256")
    assert verify_identity(token, public, "123456", "nonce") == (
        987654321,
        b"opaque-123",
        "测试用户",
        "test_user",
        None,
    )


@pytest.mark.parametrize(
    "override",
    [
        {"iss": "https://example.test"},
        {"aud": "321"},
        {"aud": ["123456", "321"]},
        {"exp": 0},
        {"iat": 1},
        {"nonce": None},
        {"nonce": "wrong"},
        {"nonce": "非 ASCII"},
        {"id": None},
        {"id": "admin"},
        {"id": True},
        {"id": -1},
    ],
)
def test_invalid_id_token_claims_fail_closed(signing_keys, override):
    private, public = signing_keys
    token = jwt.encode({**id_claims(), **override}, private, algorithm="RS256")
    with pytest.raises((ValueError, jwt.InvalidTokenError)):
        verify_identity(token, public, "123456", "nonce")


def test_invalid_id_token_signature_and_algorithm_fail_closed(signing_keys):
    _, public = signing_keys
    other = rsa.generate_private_key(public_exponent=65537, key_size=2048)
    for token in (
        jwt.encode(id_claims(), other, algorithm="RS256"),
        jwt.encode(id_claims(), "x" * 32, algorithm="HS256"),
    ):
        with pytest.raises(jwt.InvalidTokenError):
            verify_identity(token, public, "123456", "nonce")


@pytest.mark.parametrize(
    "path",
    [
        "//evil.test/wiki",
        "/wiki/../login",
        "/wiki/%2e%2e/login",
        "/wiki%2fadmin",
        "/wiki/admin",
        "/wiki/%2561dmin",
        "/wiki?x=%0d%0a",
        "/wiki\\evil",
    ],
)
def test_return_path_cannot_leave_public_wiki(path):
    assert safe_wiki_path(path) == "/wiki"


def test_return_path_preserves_article_query_and_anchor():
    path = "/wiki/123/page/demo?q=%E4%B8%AD%E6%96%87#claim-2"
    assert safe_wiki_path(path) == path


@pytest.mark.parametrize(
    "url",
    [
        "http://cdn4.telesco.pe/file/id",
        "https://cdn4.telesco.pe.evil.test/file/id",
        "https://cdn4.telesco.pe@127.0.0.1/file/id",
        "https://cdn4.telesco.pe:443/file/id",
        "https://cdn4.telesco.pe/file/id?token=example",
        "https://[::1]/avatar",
    ],
)
async def test_avatar_rejects_urls_outside_fixed_cdn(url):
    assert await download_avatar(url) == (None, None)
