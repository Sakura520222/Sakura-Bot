import base64
import io
from types import SimpleNamespace
from unittest.mock import AsyncMock

import pytest
from PIL import Image

from core.media import poll_images


def photo(message_id=1, text="", group=None, color="red"):
    return SimpleNamespace(
        id=message_id,
        text=text,
        grouped_id=group,
        photo=SimpleNamespace(id=message_id),
        document=None,
        file=SimpleNamespace(size=10),
        color=color,
    )


def png(color="red"):
    data = io.BytesIO()
    Image.new("RGB", (4, 4), color).save(data, format="PNG")
    return data.getvalue()


def client_for(anchor, nearby=None):
    async def get_messages(chat, ids):
        return nearby if isinstance(ids, list) else anchor

    async def download(message, file):
        file.write(png(getattr(message, "color", "red")))

    return SimpleNamespace(
        get_messages=AsyncMock(side_effect=get_messages),
        download_media=AsyncMock(side_effect=download),
    )


async def test_single_photo_and_image_document_are_validated_without_persisting_files():
    for media in [
        photo(),
        SimpleNamespace(
            id=2,
            text="文件说明",
            grouped_id=None,
            photo=None,
            document=SimpleNamespace(id=2, mime_type="image/png"),
            file=SimpleNamespace(size=10),
        ),
    ]:
        client = client_for(media)
        caption, urls = await poll_images.collect_poll_images(client, media, 100, "旧说明")
        assert caption == media.text
        assert len(urls) == 1 and urls[0].startswith("data:image/png;base64,")
        client.get_messages.assert_awaited_once_with(100, ids=media.id)


async def test_album_sorts_deduplicates_and_excludes_neighbouring_posts(monkeypatch):
    monkeypatch.setattr(poll_images, "ALBUM_SETTLE_SECONDS", 0)
    first, second = photo(1, "第一张", 123), photo(3, "第二张", 123, "blue")
    client = client_for(second, [second, photo(2, "别人的帖子", 456), first, first])
    caption, urls = await poll_images.collect_poll_images(client, second, 100, "旧说明")
    assert caption == "第一张\n\n第二张"
    assert client.download_media.await_count == 2
    pixels = []
    for url in urls:
        with Image.open(io.BytesIO(base64.b64decode(url.split(",", 1)[1]))) as image:
            pixels.append(image.getpixel((0, 0)))
    assert pixels == [(255, 0, 0), (0, 0, 255)]


async def test_deleted_message_and_album_failure_skip_instead_of_using_stale_media(monkeypatch):
    media = photo()
    with pytest.raises(poll_images.PollImageError, match="已删除"):
        await poll_images.collect_poll_images(client_for(None), media, 100, "旧说明")
    monkeypatch.setattr(poll_images, "ALBUM_SETTLE_SECONDS", 0)
    media.grouped_id = 123
    client = client_for(media)
    client.get_messages.side_effect = [media, ValueError("PRIVATE")]
    with pytest.raises(poll_images.PollImageError) as error:
        await poll_images.collect_poll_images(client, media, 100, "")
    assert "PRIVATE" not in str(error.value)
    client.download_media.assert_not_awaited()


async def test_download_size_type_and_total_limits(monkeypatch):
    media = photo()
    client = client_for(media)
    media.file.size = poll_images.MAX_IMAGE_BYTES + 1
    with pytest.raises(poll_images.PollImageError, match="8 MiB"):
        await poll_images.collect_poll_images(client, media, 100, "")
    client.download_media.assert_not_awaited()
    media.file.size = 1

    async def corrupt(message, file):
        file.write(b"not an image")

    client.download_media.side_effect = corrupt
    with pytest.raises(poll_images.PollImageError, match="校验失败"):
        await poll_images.collect_poll_images(client, media, 100, "")
    with poll_images.ImageBuffer() as stream, pytest.raises(poll_images.PollImageError):
        stream.write(b"x" * (poll_images.MAX_IMAGE_BYTES + 1))
    monkeypatch.setattr(poll_images, "ALBUM_SETTLE_SECONDS", 0)
    monkeypatch.setattr(poll_images, "MAX_TOTAL_BYTES", len(png()) + 1)
    first, second = photo(1, group=123), photo(2, group=123)
    with pytest.raises(poll_images.PollImageError, match="总量"):
        await poll_images.collect_poll_images(client_for(first, [first, second]), first, 100, "")


async def test_text_only_tasks_do_not_download_or_read_telegram():
    client = client_for(None)
    assert await poll_images.collect_poll_images(client, None, 100, "文字") == ("文字", [])
    client.get_messages.assert_not_awaited()
    client.download_media.assert_not_awaited()
