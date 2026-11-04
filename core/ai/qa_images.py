"""Bounded, ephemeral Telegram image references for private QA (#146).

Only file IDs live between turns. Binary/data URLs exist during a request only.
"""

import asyncio
import base64
import io
import os
import re
import threading
import time
from dataclasses import dataclass, field
from urllib.parse import urlsplit

import aiohttp
from PIL import Image

MAX_IMAGE_BYTES = 8 * 1024 * 1024
MAX_IMAGES = 4
MAX_PIXELS = 12_000_000
MAX_SIDE = 8192
IMAGE_SLOTS = threading.BoundedSemaphore(2)
IMAGE_ACK = "🖼️ 图片已暂存，可继续发图（最多 4 张），然后发送问题。5 分钟内可用「它 / 上图」追问；新图片替换上一批，/clear 清除。"
VISION_DISABLED = "当前「助手问答」模型未开启图片输入。请在 WebUI 的 AI 配置中选择支持图片的助手模型，并开启该模型的图片能力。"
IMAGE_FAILURE = "图片问答失败：上游未接受图片或请求超时，请检查助手模型的图片能力与服务配置后重试。"
_FOLLOWUP = re.compile(r"它|这张|那张|上图|图中|图片|照片|\b(?:it|its|image|photo|picture)\b", re.I)


class ImageInputError(ValueError):
    """Safe, user-facing image failure; never wraps URLs, bytes or SDK errors."""


def context_ttl():
    try:
        return max(30, min(600, int(os.getenv("QA_IMAGE_CONTEXT_TTL_SECONDS", "300"))))
    except ValueError:
        return 300


@dataclass(frozen=True)
class ImageRef:
    file_id: str = field(repr=False)
    message_id: int
    created: float = field(default_factory=time.monotonic)


@dataclass
class ImageState:
    pending: list[ImageRef] = field(default_factory=list)
    active: list[ImageRef] = field(default_factory=list)
    session: str = ""
    busy: bool = False
    seen: set[int] = field(default_factory=set)


@dataclass
class ImageLease:
    scope: tuple[int, int]
    state: ImageState
    refs: tuple[ImageRef, ...]
    consumed: bool


class ImageContext:
    def __init__(self, ttl=None, now=time.monotonic):
        self.ttl = ttl if ttl is not None else context_ttl()
        self.now = now
        self.states: dict[tuple[int, int], ImageState] = {}
        self.expired = {}
        self.lock = threading.RLock()

    def cleanup(self):
        with self.lock:
            cutoff = self.now() - self.ttl
            for key, state in list(self.states.items()):
                had_pending = bool(state.pending)
                had_images = bool(state.pending or state.active)
                state.pending = [r for r in state.pending if r.created > cutoff]
                state.active = [r for r in state.active if r.created > cutoff]
                state.seen.intersection_update(
                    r.message_id for r in (*state.pending, *state.active)
                )
                if not state.pending and not state.active and not state.busy:
                    if had_images:
                        self.expired[key] = (self.now(), had_pending)
                    del self.states[key]
            self.expired = {
                key: value for key, value in self.expired.items() if value[0] > self.now() - 60
            }
            while len(self.expired) > 256:
                self.expired.pop(next(iter(self.expired)))

    def add(self, scope, ref):
        with self.lock:
            self.cleanup()
            if scope not in self.states and len(self.states) >= 256:
                raise ImageInputError("图片暂存已满，请稍后再试。")
            state = self.states.setdefault(scope, ImageState())
            self.expired.pop(scope, None)
            if ref.message_id in state.seen:
                return False, False
            if len(state.pending) >= MAX_IMAGES:
                raise ImageInputError("一次最多 4 张图片，请先提问或用 /clear 清除。")
            first = not state.pending
            state.pending.append(ref)
            state.pending.sort(key=lambda r: r.message_id)
            state.seen.add(ref.message_id)
            return True, first

    def has_context(self, scope):
        self.cleanup()
        with self.lock:
            return scope in self.states or scope in self.expired

    def begin(self, scope, session, query, reply=False):
        with self.lock:
            self.cleanup()
            state = self.states.get(scope)
            if state is None:
                expired = self.expired.get(scope)
                if expired and (expired[1] or reply or _FOLLOWUP.search(query)):
                    self.expired.pop(scope, None)
                    raise ImageInputError("图片已过期，请重新发送图片后提问。")
                return None
            if state.busy:
                raise ImageInputError("上一条图片问题仍在处理，请稍后再发送问题。")
            if state.session != session:
                state.active.clear()
            consumed = bool(state.pending)
            if consumed:
                state.active, state.pending = state.pending, []
            refs = tuple(state.active) if consumed or reply or _FOLLOWUP.search(query) else ()
            state.session = session
            if not refs:
                return None
            state.busy = True
            return ImageLease(scope, state, refs, consumed)

    def finish(self, lease, success):
        if lease is None:
            return
        with self.lock:
            if self.states.get(lease.scope) is not lease.state:
                return
            state = lease.state
            state.busy = False
            if not success and lease.consumed and not state.pending:
                state.pending = list(lease.refs)
            self.cleanup()

    def clear_user(self, user_id):
        with self.lock:
            for scope in list(self.states):
                if scope[0] == user_id:
                    del self.states[scope]
            for scope in list(self.expired):
                if scope[0] == user_id:
                    del self.expired[scope]


def telegram_image_ref(message):
    photos = getattr(message, "photo", None)
    doc = getattr(message, "document", None)
    if photos:
        media = max(photos, key=lambda p: p.width * p.height)
        if media.width * media.height > MAX_PIXELS or max(media.width, media.height) > MAX_SIDE:
            raise ImageInputError("图片像素过大，最多 1200 万像素、单边 8192 像素。")
    elif doc and (
        (doc.mime_type or "").startswith("image/")
        or (doc.file_name or "").lower().endswith((".png", ".jpg", ".jpeg"))
    ):
        if doc.mime_type not in {"image/png", "image/jpeg", "application/octet-stream", None, ""}:
            raise ImageInputError("目前只支持 PNG / JPEG 图片。")
        media = doc
    else:
        return None
    if (media.file_size or 0) > MAX_IMAGE_BYTES:
        raise ImageInputError("单张图片不能超过 8 MiB。")
    return ImageRef(media.file_id, message.message_id)


def image_data_url(data):
    if not data or len(data) > MAX_IMAGE_BYTES:
        raise ImageInputError("单张图片不能超过 8 MiB，且不能为空。")
    try:
        with Image.open(io.BytesIO(data)) as img:
            fmt = img.format
            if fmt not in {"PNG", "JPEG"}:
                raise ImageInputError("目前只支持 PNG / JPEG 图片。")
            if (
                img.width * img.height > MAX_PIXELS
                or max(img.size) > MAX_SIDE
                or getattr(img, "n_frames", 1) != 1
            ):
                raise ImageInputError("图片像素过大或为动画，请发送静态 PNG / JPEG。")
            img.verify()
        with Image.open(io.BytesIO(data)) as img:
            img.load()
    except ImageInputError:
        raise
    except Exception:
        raise ImageInputError("图片已损坏或格式不受支持，请重新发送 PNG / JPEG。") from None
    mime = "image/png" if fmt == "PNG" else "image/jpeg"
    return f"data:{mime};base64," + base64.b64encode(data).decode("ascii")


async def download_image(bot, ref):
    try:
        remote = await bot.get_file(ref.file_id, read_timeout=15, connect_timeout=15)
        if (remote.file_size or 0) > MAX_IMAGE_BYTES:
            raise ImageInputError("单张图片不能超过 8 MiB。")
        url = urlsplit(remote.file_path or "")
        if (
            url.scheme != "https"
            or url.hostname != "api.telegram.org"
            or url.username
            or url.password
            or url.port not in (None, 443)
            or not url.path.startswith("/file/bot")
        ):
            raise ImageInputError("Telegram 图片文件地址无效，请重新发送。")
        data = bytearray()
        async with (
            aiohttp.ClientSession(
                timeout=aiohttp.ClientTimeout(total=30), trust_env=True
            ) as client,
            client.get(remote.file_path, allow_redirects=False) as response,
        ):
            if response.status != 200:
                raise ImageInputError("Telegram 图片文件不存在或下载失败，请重新发送。")
            response.raise_for_status()
            async for chunk in response.content.iter_chunked(64 * 1024):
                if len(data) + len(chunk) > MAX_IMAGE_BYTES:
                    raise ImageInputError("单张图片不能超过 8 MiB。")
                data.extend(chunk)
        return await asyncio.to_thread(image_data_url, data)
    except ImageInputError:
        raise
    except Exception:
        raise ImageInputError("下载 Telegram 图片失败或超时，请重新发送后重试。") from None


@dataclass(repr=False)
class ImageRequest:
    store: ImageContext
    scope: tuple[int, int]
    bot: object
    reply: bool = False
    require_images: bool = False
    download: object = download_image


def user_content(query, images):
    if not images:
        return query
    return [
        {"type": "text", "text": query},
        *({"type": "image_url", "image_url": {"url": url}} for url in images),
    ]
