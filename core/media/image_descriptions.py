"""Durable image results shared by polls, channel summaries and retrieval."""

import base64
import hashlib
import logging

logger = logging.getLogger(__name__)


def image_data(url):
    header, separator, encoded = url.partition(",")
    if not separator or header not in {"data:image/png;base64", "data:image/jpeg;base64"}:
        raise ValueError("invalid_image_data_url")
    return header[5:].split(";", 1)[0], base64.b64decode(encoded, validate=True)


def description_text(asset):
    parts = []
    for key, label in (("description", "图片描述"), ("ocr", "图中文字")):
        value = asset.get(key)
        if isinstance(value, str) and value.strip():
            parts.append(f"{label}：{value.strip()}")
    if not parts:
        raise ValueError("empty_saved_image_description")
    return "\n".join(parts)


async def register_poll_image(data, mime, message, client):
    from core.infrastructure.database.manager import get_db_manager
    from core.media.image_pipeline import channel_key, get_image_pipeline, image_media

    # Standalone callers may have no application DB; the running bot always does.
    if get_db_manager().pool is None:
        return
    pipeline = get_image_pipeline()
    forward = getattr(message, "fwd_from", None)
    peer = getattr(forward, "from_id", None)
    post_id = getattr(forward, "channel_post", None)
    if getattr(peer, "channel_id", None) and isinstance(post_id, int):
        record = await pipeline.store.get(channel_key(peer.channel_id), post_id)
        if not record or record["deleted"] or record["media_id"] != image_media(message):
            # The discussion forward can arrive before the channel listener. Fetch
            # the original rather than indexing a discussion ID or today's date.
            source_client = pipeline.client or client
            original = await source_client.get_messages(peer, ids=post_id)
            if not original or image_media(original) != image_media(message):
                raise ValueError("poll_image_source_changed")
            chat = getattr(original, "chat", None) or await source_client.get_entity(peer)
            await pipeline.capture(original, chat)
    await pipeline.store_poll_image(data, mime, image_media(message))


async def saved_poll_descriptions(images, config):
    from core.infrastructure.database.manager import get_db_manager
    from core.media.image_pipeline import get_image_pipeline

    if get_db_manager().pool is None:
        raise RuntimeError("image_store_unavailable")
    pipeline = get_image_pipeline()
    descriptions = []
    for index, url in enumerate(images, 1):
        _, data = image_data(url)
        digest = hashlib.sha256(data).hexdigest()
        asset = await pipeline.describe_for_poll(digest, config=config)
        descriptions.append(f"图{index}：\n{description_text(asset)}")
    logger.info("趣味投票使用已持久化图片描述: %d 张", len(descriptions))
    return descriptions
