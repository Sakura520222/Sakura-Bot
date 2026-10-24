# Copyright 2026 Sakura-Bot
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
"""销号用户已无法由 banChatMember 解析时，按真实作者逐页核验并删除历史。"""

from telethon import utils
from telethon.tl.functions.messages import GetHistoryRequest
from telethon.tl.types import MessageEmpty, PeerUser

from core.infrastructure.database.mysql import utc_now_naive

from .chat_cleanup import ChatCleanupError
from .telegram import TelegramActionError, call, mutate


def belongs(message, targets):
    return any(
        isinstance(peer, PeerUser) and peer.user_id in targets
        for peer in (
            getattr(message, "from_id", None),
            getattr(message, "guestchat_via_from", None),
        )
    )


class DeletedHistory:
    def __init__(self, cleanup):
        self.cleanup = cleanup

    async def page(self, store, job, group, chat):
        cleanup = self.cleanup
        observer = getattr(cleanup.guard, "bot_observer", None)
        client = getattr(observer, "client", None)
        if not client or client is cleanup.guard.client:
            raise ChatCleanupError(
                "已销号账号已无法由 Telegram 封禁接口解析；完整历史清理需要可读取本群的 UserBot"
            )
        targets = {int(value) for value in group.get("history_targets", {})}
        if not targets:
            raise ChatCleanupError("缺少已核验销号身份，停止历史清理")
        source = getattr(chat, "username", None) or group["chat_id"]
        peer = await call(client.get_input_entity(source))
        # 每步处理至多五页，重启继续同一 offset；绝不信任可能被 Telegram 忽略的 from_user 搜索过滤。
        for _ in range(5):
            batch = group.get("history_batch")
            if not batch:
                result = await call(
                    client(
                        GetHistoryRequest(
                            peer=peer,
                            offset_id=group.get("history_offset", 0),
                            offset_date=None,
                            add_offset=0,
                            limit=100,
                            max_id=0,
                            min_id=0,
                            hash=0,
                        ),
                        flood_sleep_threshold=0,
                    )
                )
                rows = [m for m in result.messages if not isinstance(m, MessageEmpty)]
                if any(utils.get_peer_id(m.peer_id) != group["chat_id"] for m in rows):
                    raise ChatCleanupError("历史消息群组不匹配，未删除")
                if not rows:
                    for item in await store.history_pending(job["id"], group["chat_id"]):
                        await store.item(item, "removed", "已移除并逐页核实清理全部可见历史消息")
                    group.update(status="completed", error=None)
                    job["payload"]["group_index"] += 1
                    return
                offset = min(m.id for m in rows)
                if group.get("history_offset") and offset >= group["history_offset"]:
                    raise ChatCleanupError("历史消息分页未前进，停止清理")
                batch = {
                    "ids": [m.id for m in rows if belongs(m, targets)],
                    "offset": offset,
                    "scanned": len(rows),
                }
                group["history_batch"] = batch
                await store.save(job)
            if batch["ids"]:
                await cleanup._group(group["chat_id"])
                permitted = (
                    cleanup.settings.enabled
                    if job["trigger_type"] == "scheduled"
                    else cleanup.authorized(job["actor"])
                )
                if not permitted or cleanup.guard.bot_id != job["bot_id"]:
                    raise ChatCleanupError("清理已关闭或权限已撤销，未删除历史消息")
                current = await call(
                    cleanup.guard.client.get_messages(group["chat_id"], ids=batch["ids"])
                )
                current = current if isinstance(current, (list, tuple)) else [current]
                if any(
                    m is not None
                    and not isinstance(m, MessageEmpty)
                    and (
                        not belongs(m, targets)
                        or m.id not in batch["ids"]
                        or utils.get_peer_id(m.peer_id) != group["chat_id"]
                    )
                    for m in current
                ):
                    raise ChatCleanupError("历史消息作者不匹配，未删除")
                try:
                    live_ids = [
                        m.id for m in current if m is not None and not isinstance(m, MessageEmpty)
                    ]
                    if live_ids:
                        await mutate(
                            cleanup.guard.client.delete_messages(group["chat_id"], live_ids)
                        )
                except TelegramActionError as error:
                    from telethon.errors import FloodWaitError

                    if isinstance(error.__cause__, FloodWaitError):
                        raise error.__cause__ from None
                    raise ConnectionError("DeletedHistoryMutationRetry") from error
                remaining = await call(
                    cleanup.guard.client.get_messages(group["chat_id"], ids=batch["ids"])
                )
                remaining = remaining if isinstance(remaining, (list, tuple)) else [remaining]
                if any(m is not None and not isinstance(m, MessageEmpty) for m in remaining):
                    raise ConnectionError("DeletedHistoryUnconfirmed")
                group.setdefault("history_batches", []).append(
                    {"ids": batch["ids"], "at": utc_now_naive().isoformat()}
                )
                group["history_deleted"] = group.get("history_deleted", 0) + len(batch["ids"])
            group["history_offset"] = batch["offset"]
            group["history_scanned"] = group.get("history_scanned", 0) + batch["scanned"]
            group["failures"] = 0
            group.pop("history_batch", None)
            await store.save(job)
