# Copyright 2026 Sakura-Bot
#
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
#
# - 署名：必须提供本项目的原始来源链接
# - 相同方式共享：衍生作品必须采用相同的许可证
#
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
# 许可证全文：参见 LICENSE 文件

"""极简 Telegram Bot API HTTP 客户端（httpx 直调）

主进程对 python-telegram-bot 的全部用途只是「Bot API HTTP 客户端 + 异常分类」，
用本模块替代以避免主进程加载 PTB（实测 +10MB RSS）。QA 子进程仍是完整 PTB 应用。

异常语义与旧代码的用法保持对齐：
- ``str(error)`` 携带 API description 原文（去掉 "Bad Request: " 前缀），
  供既有 ``"bot was blocked by the user" in str(e)`` 等子串匹配继续工作；
- 429 限流抛 :class:`RetryAfterError`，``retry_after`` 为秒数（float）；
- 网络层错误（超时/连接失败）抛 :class:`NetworkAPIError`。
"""

import asyncio
import logging

import httpx

logger = logging.getLogger(__name__)

_API_BASE = "https://api.telegram.org"

# 与 PTB 旧调用点的超时对齐（ban_history 曾用 read=15s）
_DEFAULT_TIMEOUT = 15.0


class BotAPIError(Exception):
    """Bot API 返回 ok=false 的基础异常"""

    def __init__(self, description: str, error_code: int = 0):
        super().__init__(description)
        self.description = description
        self.error_code = error_code


class RetryAfterError(BotAPIError):
    """429 限流；retry_after 为应等待的秒数"""

    def __init__(self, description: str, retry_after: float):
        super().__init__(description, 429)
        self.retry_after = float(retry_after)


class BadRequestError(BotAPIError):
    """400 类客户端错误（description 去掉 "Bad Request: " 前缀，便于匹配）"""


class NetworkAPIError(BotAPIError):
    """网络/传输层错误（含超时）"""


_client: httpx.AsyncClient | None = None
_client_lock: asyncio.Lock | None = None


async def _get_client() -> httpx.AsyncClient:
    """共享一个 AsyncClient，避免每次调用重建连接池"""
    global _client, _client_lock
    if _client is None or _client.is_closed:
        if _client_lock is None:
            _client_lock = asyncio.Lock()
        async with _client_lock:
            if _client is None or _client.is_closed:
                _client = httpx.AsyncClient(timeout=_DEFAULT_TIMEOUT)
    return _client


async def aclose() -> None:
    """关闭共享客户端（进程退出前调用，未调用也由 OS 回收）"""
    global _client
    if _client is not None and not _client.is_closed:
        await _client.aclose()
    _client = None


def bot_id_from_token(token: str) -> int:
    """从 token 前缀解析 bot ID（与 PTB Bot.id 同源，无网络请求）"""
    return int(token.split(":", 1)[0])


def _clean_description(description: str) -> str:
    """去掉 "Bad Request: " 前缀，保持与 PTB 异常消息一致的可匹配形态"""
    lower = description.lower()
    if lower.startswith("bad request:"):
        return description[len("bad request:") :].strip()
    return description


async def call_method(
    token: str,
    method: str,
    payload: dict | None = None,
    *,
    timeout: float | None = None,
) -> dict:
    """调用任意 Bot API 方法，返回 result 字段

    Raises:
        RetryAfterError: 429 限流
        BadRequestError: 400 类错误
        NetworkAPIError: 网络/传输错误
        BotAPIError: 其他 ok=false 响应
    """
    url = f"{_API_BASE}/bot{token}/{method}"
    client = await _get_client()
    try:
        response = await client.post(url, json=payload or {}, timeout=timeout)
    except httpx.TimeoutException as e:
        raise NetworkAPIError(f"Timeout: {type(e).__name__}") from e
    except httpx.TransportError as e:
        raise NetworkAPIError(f"Network: {type(e).__name__}") from e

    try:
        body = response.json()
    except ValueError as e:
        raise NetworkAPIError(f"Invalid JSON (HTTP {response.status_code})") from e

    if body.get("ok"):
        return body.get("result", {})

    description = str(body.get("description", f"HTTP {response.status_code}"))
    error_code = int(body.get("error_code", response.status_code))
    if response.status_code == 429 or error_code == 429:
        parameters = body.get("parameters") or {}
        raise RetryAfterError(description, parameters.get("retry_after", 1))
    if response.status_code == 400 or error_code == 400:
        raise BadRequestError(_clean_description(description), error_code)
    raise BotAPIError(description, error_code)


async def send_message(
    token: str,
    chat_id: int | str,
    text: str,
    *,
    parse_mode: str | None = None,
    timeout: float | None = None,
) -> dict:
    """发送消息（sendMessage），成功返回 result（含 message_id）"""
    payload: dict = {"chat_id": chat_id, "text": text}
    if parse_mode:
        payload["parse_mode"] = parse_mode
    return await call_method(token, "sendMessage", payload, timeout=timeout)


async def ban_chat_member(
    token: str,
    chat_id: int | str,
    user_id: int | str,
    *,
    revoke_messages: bool = False,
    timeout: float | None = None,
) -> bool:
    """封禁成员（banChatMember），成功返回 True（与 PTB 返回语义一致）"""
    payload: dict = {
        "chat_id": chat_id,
        "user_id": user_id,
        "revoke_messages": revoke_messages,
    }
    await call_method(token, "banChatMember", payload, timeout=timeout)
    return True
