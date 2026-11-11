"""Explicit, bounded UserBot backfill. This module never handles live AI requests."""

import json

from telethon import functions, utils


async def backfill_post(client, pool, channel, post_id, limit=5000):
    entity = await client.get_entity(channel)
    discussion = await client(
        functions.messages.GetDiscussionMessageRequest(peer=entity, msg_id=post_id)
    )
    candidates = [
        m
        for m in discussion.messages
        if getattr(getattr(m, "fwd_from", None), "channel_post", None) == post_id
        and utils.get_peer_id(m.peer_id) != utils.get_peer_id(entity)
    ]
    if len(candidates) != 1:
        raise ValueError("无法唯一识别该帖子的讨论根消息")
    root = candidates[0]
    chat_id = utils.get_peer_id(root.peer_id)
    count = 0

    async def save(message):
        nonlocal count
        record = {
            "chat_id": chat_id,
            "message_id": message.id,
            "root_id": root.id,
            "channel": channel,
            "user_id": message.sender_id or 0,
            "text": message.message or "",
            "date": int(message.date.timestamp()),
            "reply_id": message.reply_to_msg_id or 0,
            "edited": bool(message.edit_date),
            "media": type(message.media).__name__ if message.media else "",
            "sync_boundary": "userbot_backfill",
        }
        async with pool.acquire() as conn:
            async with conn.cursor() as cursor:
                # Live edits always win over a historical snapshot.
                await cursor.execute(
                    "INSERT IGNORE INTO qa_comments(chat_id,message_id,root_id,channel,payload,message_date) VALUES(%s,%s,%s,%s,%s,%s)",
                    (
                        chat_id,
                        message.id,
                        root.id,
                        channel,
                        json.dumps(record, ensure_ascii=False),
                        record["date"],
                    ),
                )
                count += cursor.rowcount
            await conn.commit()

    await save(root)
    async with pool.acquire() as conn:
        async with conn.cursor() as cursor:
            await cursor.execute(
                "INSERT IGNORE INTO qa_posts(channel_id,post_id,chat_id,root_id) VALUES(%s,%s,%s,%s)",
                (utils.get_peer_id(entity), post_id, chat_id, root.id),
            )
        await conn.commit()
    seen = 0
    async for message in client.iter_messages(entity, reply_to=post_id, limit=limit + 1):
        if seen == limit:
            return {
                "inserted": count,
                "truncated": True,
                "visibility": "仅 UserBot 可见历史；已有实时记录保持不变",
            }
        await save(message)
        seen += 1
    return {
        "inserted": count,
        "truncated": False,
        "visibility": "仅 UserBot 可见历史，不证明已删除或不可见评论完整",
    }
