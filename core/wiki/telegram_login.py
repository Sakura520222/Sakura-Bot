# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot

"""Telegram's OIDC code flow and its private profile payload."""

import asyncio
import base64
import hashlib
import hmac
import ipaddress
import json
import re
import time
from dataclasses import dataclass, field
from urllib.parse import urlencode, urlsplit

import aiohttp
import jwt

from core.config.runtime import runtime_config
from core.media.image_validation import validate_image
from core.settings import get_settings

ISSUER = "https://oauth.telegram.org"
CALLBACK = "/api/wiki/account/callback"
JWKS = f"{ISSUER}/.well-known/jwks.json"
MAX_AVATAR_BYTES = 512 * 1024
KEY_ALGORITHMS = {"RS256": "RSA", "ES256": "EC"}
_keys = []
_keys_at = 0.0
_keys_lock = asyncio.Lock()


class TelegramLoginError(ValueError):
    def __init__(self, stage, reason, status=None):
        super().__init__(stage, reason, status)
        self.stage = stage
        self.reason = reason
        self.status = status


class PublicAvatarResolver(aiohttp.ThreadedResolver):
    async def resolve(self, host, port=0, family=0):
        addresses = await super().resolve(host, port, family)
        if not addresses or any(
            not ipaddress.ip_address(item["host"]).is_global
            or ipaddress.ip_address(item["host"]).is_multicast
            for item in addresses
        ):
            raise OSError("Invalid avatar address")
        return addresses


@dataclass(frozen=True)
class LoginConfig:
    origin: str
    client_id: str
    client_secret: str = field(repr=False)

    @property
    def callback(self):
        return self.origin + CALLBACK


def wiki_login_settings(settings=None):
    settings = settings or get_settings()
    defaults = settings.wiki_login
    saved = runtime_config.read().get("wiki_login", {})
    if not isinstance(saved, dict) or not saved:
        return defaults
    overrides = {
        type(defaults).model_fields[key].alias: saved[key]
        for key in ("enabled", "origin", "client_secret", "assistant_enabled", "assistant_checkin")
        if key in saved
    }
    return type(defaults).model_validate({**defaults.model_dump(by_alias=True), **overrides})


def login_config(settings=None, config=None):
    settings = settings or get_settings()
    config = config or wiki_login_settings(settings)
    token = settings.telegram.qa_bot_token
    if not config.enabled or not token or not config.client_secret.get_secret_value():
        return None
    origin = config.origin.rstrip("/")
    if len(origin) > 255 or not re.fullmatch(
        r"https://[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?(?::[1-9][0-9]{0,4})?", origin
    ):
        return None
    try:
        port = urlsplit(origin).port
    except ValueError:
        return None
    if port == 443:
        origin = origin.removesuffix(":443")
    client_id, separator, secret = token.get_secret_value().partition(":")
    if not separator or not secret or not re.fullmatch(r"[1-9][0-9]{0,18}", client_id):
        return None
    return LoginConfig(origin, client_id, config.client_secret.get_secret_value())


def authorization_url(config, state, nonce, verifier):
    challenge = (
        base64.urlsafe_b64encode(hashlib.sha256(verifier.encode()).digest()).decode().rstrip("=")
    )
    return f"{ISSUER}/auth?" + urlencode(
        {
            "client_id": config.client_id,
            "redirect_uri": config.callback,
            "response_type": "code",
            "scope": "openid profile",
            "state": state,
            "nonce": nonce,
            "code_challenge": challenge,
            "code_challenge_method": "S256",
        }
    )


async def bounded_body(response, limit):
    data = bytearray()
    async for chunk in response.content.iter_chunked(16384):
        data.extend(chunk)
        if len(data) > limit:
            raise ValueError("Response too large")
    return bytes(data)


async def signing_key(client, header):
    global _keys, _keys_at
    algorithm, kid = header.get("alg"), header.get("kid")
    if (
        not isinstance(algorithm, str)
        or algorithm not in KEY_ALGORITHMS
        or not isinstance(kid, str)
    ):
        raise ValueError("Invalid signing header")
    if not 1 <= len(kid) <= 200:
        raise ValueError("Invalid key ID")

    def matching_key():
        return next(
            (
                key
                for key in _keys
                if key.get("kid") == kid
                and key.get("kty") == KEY_ALGORITHMS[algorithm]
                and key.get("use", "sig") == "sig"
                and key.get("alg", algorithm) == algorithm
            ),
            None,
        )

    async with _keys_lock:
        age = time.monotonic() - _keys_at if _keys_at else float("inf")
        if age >= 3600 or (age >= 30 and not matching_key()):
            async with client.get(JWKS, allow_redirects=False) as response:
                if response.status != 200:
                    raise ValueError("JWKS unavailable")
                keys = json.loads(await bounded_body(response, 64 * 1024))["keys"]
                if (
                    not isinstance(keys, list)
                    or not keys
                    or not all(isinstance(key, dict) for key in keys)
                ):
                    raise ValueError("Invalid JWKS")
                _keys, _keys_at = keys, time.monotonic()
        key = matching_key()
    if not key:
        raise ValueError("Unknown signing key")
    return jwt.PyJWK.from_dict(key, algorithm=algorithm)


async def exchange_code(config, code, verifier, nonce):
    async with aiohttp.ClientSession(timeout=aiohttp.ClientTimeout(total=10)) as client:
        try:
            async with client.post(
                f"{ISSUER}/token",
                allow_redirects=False,
                auth=aiohttp.BasicAuth(config.client_id, config.client_secret),
                data={
                    "grant_type": "authorization_code",
                    "code": code,
                    "redirect_uri": config.callback,
                    "client_id": config.client_id,
                    "code_verifier": verifier,
                },
            ) as response:
                token = token_from_response(
                    response.status, await bounded_body(response, 32 * 1024)
                )
        except TelegramLoginError:
            raise
        except (aiohttp.ClientError, TimeoutError, ValueError) as error:
            raise TelegramLoginError("exchange", type(error).__name__) from None

        try:
            key = await signing_key(client, jwt.get_unverified_header(token))
        except (
            aiohttp.ClientError,
            TimeoutError,
            jwt.PyJWTError,
            ValueError,
            KeyError,
            TypeError,
        ) as error:
            raise TelegramLoginError("signing_key", type(error).__name__) from None

    try:
        return verify_identity(token, key, config.client_id, nonce)
    except jwt.MissingRequiredClaimError as error:
        reason = f"missing_{error.claim}"
    except TelegramLoginError:
        raise
    except (jwt.PyJWTError, ValueError, TypeError) as error:
        reason = type(error).__name__
    raise TelegramLoginError("identity", reason) from None


def token_from_response(status, body):
    try:
        payload = json.loads(body)
    except ValueError:
        raise TelegramLoginError("exchange", "invalid_json", status) from None
    if not isinstance(payload, dict):
        raise TelegramLoginError("exchange", "invalid_payload", status)
    if status != 200 or "error" in payload:
        error = payload.get("error")
        reason = (
            error
            if isinstance(error, str)
            and error
            in {
                "invalid_client",
                "invalid_grant",
                "invalid_request",
                "unauthorized_client",
                "unsupported_grant_type",
                "access_denied",
                "server_error",
                "temporarily_unavailable",
            }
            else "provider_error"
        )
        raise TelegramLoginError("exchange", reason, status)
    token = payload.get("id_token")
    if not isinstance(token, str) or not 1 <= len(token) <= 16384:
        raise TelegramLoginError("exchange", "invalid_id_token", status)
    return token


def verify_identity(token, key, audience, nonce):
    claims = jwt.decode(
        token,
        key.key,
        algorithms=[key.algorithm_name],
        issuer=ISSUER,
        audience=audience,
        options={"require": ["iss", "aud", "sub", "exp", "iat", "nonce", "id"], "strict_aud": True},
        leeway=30,
    )
    subject, user_id, signed_nonce = claims["sub"], claims["id"], claims["nonce"]
    if (
        not isinstance(signed_nonce, str)
        or not signed_nonce.isascii()
        or not hmac.compare_digest(nonce, signed_nonce)
    ):
        raise TelegramLoginError("identity", "nonce_mismatch")
    now = time.time()
    if type(claims["iat"]) is not int or not now - 630 <= claims["iat"] <= now + 30:
        raise TelegramLoginError("identity", "invalid_issued_at")
    if type(claims["exp"]) is not int or claims["exp"] <= claims["iat"]:
        raise TelegramLoginError("identity", "invalid_expiration")
    if not isinstance(subject, str) or not re.fullmatch(r"[\x21-\x7e]{1,255}", subject):
        raise TelegramLoginError("identity", "invalid_subject")
    # Telegram may serialize the signed numeric profile ID as a decimal string.
    if isinstance(user_id, str) and re.fullmatch(r"[1-9][0-9]{0,18}", user_id):
        user_id = int(user_id)
    if type(user_id) is not int or not 0 < user_id < 2**63:
        raise TelegramLoginError("identity", f"invalid_user_id_{type(user_id).__name__}")
    name, username = claims.get("name"), claims.get("preferred_username")
    name = name.replace("\x00", "").strip()[:160] if isinstance(name, str) else ""
    username = (
        username
        if isinstance(username, str) and re.fullmatch(r"[a-zA-Z0-9_]{1,64}", username)
        else None
    )
    return (
        user_id,
        subject.encode("ascii"),
        name or "Telegram 用户",
        username,
        claims.get("picture"),
    )


async def download_avatar(url):
    # Only the official photo CDN, without redirects or ambient proxies.
    if not isinstance(url, str) or not re.fullmatch(
        r"https://cdn[0-9]{1,2}\.telesco\.pe/file/[A-Za-z0-9_/-]{1,1600}", url
    ):
        return None, None
    try:
        resolver = PublicAvatarResolver()
        async with aiohttp.ClientSession(
            timeout=aiohttp.ClientTimeout(total=4),
            connector=aiohttp.TCPConnector(resolver=resolver),
        ) as client:
            async with client.get(url, allow_redirects=False) as response:
                if response.status != 200 or response.content_type not in {
                    "image/jpeg",
                    "image/png",
                }:
                    return None, None
                data = await bounded_body(response, MAX_AVATAR_BYTES)
        media_type = await asyncio.to_thread(validate_image, data)
        return data, media_type
    except (aiohttp.ClientError, TimeoutError, ValueError):
        return None, None
