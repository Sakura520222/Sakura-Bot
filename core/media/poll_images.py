"""Bounded, transient media collection for one channel fun poll."""

import asyncio
import io

from core.ai.poll_vision import MAX_POLL_IMAGES
from core.ai.qa_images import MAX_IMAGE_BYTES, image_data_url

MAX_TOTAL_BYTES = 20 * 1024 * 1024
ALBUM_SETTLE_SECONDS = 0.9
ALBUM_SCAN_RADIUS = 20


class PollImageError(ValueError):
    """Safe to log: never contains image bytes or upstream exception bodies."""


def is_poll_image(message) -> bool:
    photo = getattr(message, "photo", None)
    if isinstance(getattr(photo, "id", None), int):
        return True
    document = getattr(message, "document", None)
    return isinstance(getattr(document, "id", None), int) and getattr(
        document, "mime_type", None
    ) in {"image/png", "image/jpeg"}


def has_poll_media(message) -> bool:
    return is_poll_image(message) or isinstance(getattr(message, "grouped_id", None), int)


class ImageBuffer(io.BytesIO):
    def write(self, data):
        if self.tell() + len(data) > MAX_IMAGE_BYTES:
            raise PollImageError("趣味投票单张图片超过 8 MiB，已跳过")
        return super().write(data)


async def collect_poll_images(client, message, discussion_id: int, caption: str):
    """Queue references stay small; image bytes exist only while processing."""
    if message is None or not has_poll_media(message):
        return caption, []
    if isinstance(getattr(message, "grouped_id", None), int):
        await asyncio.sleep(ALBUM_SETTLE_SECONDS)
    try:
        message = await asyncio.wait_for(
            client.get_messages(discussion_id, ids=message.id), timeout=30
        )
    except Exception:
        raise PollImageError("无法读取趣味投票原消息，已跳过") from None
    if not message:
        raise PollImageError("趣味投票原消息已删除，已跳过")
    messages = {message.id: message}
    grouped_id = getattr(message, "grouped_id", None)
    if isinstance(grouped_id, int):
        try:
            nearby = await asyncio.wait_for(
                client.get_messages(
                    discussion_id,
                    ids=list(
                        range(
                            max(1, message.id - ALBUM_SCAN_RADIUS),
                            message.id + ALBUM_SCAN_RADIUS + 1,
                        )
                    ),
                ),
                timeout=30,
            )
            for item in nearby or []:
                if item and getattr(item, "grouped_id", None) == grouped_id:
                    messages[item.id] = item
        except Exception:
            raise PollImageError("无法完整读取趣味投票相册，已跳过") from None

    ordered = [messages[key] for key in sorted(messages)]
    texts = list(
        dict.fromkeys(
            text.strip()
            for item in ordered
            if isinstance(text := getattr(item, "text", None), str) and text.strip()
        )
    )
    caption = "\n\n".join(texts)
    images = [item for item in ordered if is_poll_image(item)]
    if len(images) > MAX_POLL_IMAGES:
        raise PollImageError("趣味投票一次最多支持 10 张图片，已跳过")
    urls = []
    total = 0
    for item in images:
        size = getattr(getattr(item, "file", None), "size", None)
        if isinstance(size, int) and size > MAX_IMAGE_BYTES:
            raise PollImageError("趣味投票单张图片超过 8 MiB，已跳过")
        try:
            with ImageBuffer() as stream:
                await asyncio.wait_for(client.download_media(item, file=stream), timeout=45)
                data = stream.getvalue()
            total += len(data)
            if total > MAX_TOTAL_BYTES:
                raise PollImageError("趣味投票图片总量超过 20 MiB，已跳过")
            urls.append(await asyncio.to_thread(image_data_url, data))
        except PollImageError:
            raise
        except Exception:
            raise PollImageError("趣味投票图片下载或校验失败，已跳过") from None
    return caption, urls
