# Copyright 2026 Sakura-Bot
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
"""从主 Bot 会话与事件发现群组、频道，使用 Telegram 核实成员和管理员身份。"""

import asyncio
import logging
import sqlite3
import time
from contextlib import closing
from pathlib import Path

from telethon.errors import FloodWaitError, UserNotParticipantError
from telethon.tl.types import Channel, ChannelParticipantLeft, Chat

logger = logging.getLogger(__name__)


def cached_chats(filename):
    """只读主 Bot 自己的实体缓存；不读取认证密钥或另一个账户的会话。"""
    if not isinstance(filename, str) or not filename:
        return []
    path = Path(filename).resolve()
    if not path.is_file():
        return []
    try:
        with closing(
            sqlite3.connect(path.as_uri() + "?mode=ro", uri=True, timeout=1)
        ) as connection:
            return connection.execute(
                "SELECT id,name FROM entities WHERE id<0 ORDER BY date DESC"
            ).fetchall()
    except sqlite3.Error:
        logger.warning("Guard 群组发现暂时无法读取主 Bot 实体缓存")
        return []


class GroupDirectory:
    def __init__(self, client, configured_ids, bot_id):
        self.client = client
        self.configured_ids = configured_ids
        self.bot_id = bot_id
        self.entries = {}
        self.task = None
        self.last_scan = 0.0
        self.checked = 0
        self.total = 0
        self.mutating = False
        self._stopped = False

    def remember(self, chat_id, title=""):
        if not isinstance(chat_id, int) or chat_id >= 0:
            return
        entry = self.entries.setdefault(
            chat_id,
            {
                "chat_id": chat_id,
                "title": "",
                "username": None,
                "chat_type": "unknown",
                "joined": None,
                "is_admin": None,
                "role": "unknown",
                "checked_at": 0.0,
                "error": None,
            },
        )
        if isinstance(title, str) and title:
            entry["title"] = title[:256]

    def observe(self, chat_id, chat=None, *, left=False):
        self.remember(chat_id, getattr(chat, "title", ""))
        entry = self.entries.get(chat_id)
        if entry is None:
            return
        if chat is not None:
            entry.update(chat_type=self.chat_type(chat), username=getattr(chat, "username", None))
        if left:
            entry.update(
                joined=False, is_admin=None, role="unknown", checked_at=time.monotonic(), error=None
            )
        elif (
            chat is not None
            and self.chat_type(chat) != "unknown"
            and not getattr(chat, "left", False)
        ):
            if entry["joined"] is not True:
                entry.update(is_admin=None, role="unknown")
            entry.update(joined=True, checked_at=time.monotonic(), error=None)

    def invalidate(self, chat_id):
        """成员或权限变化后先清除旧判断，后台重新核实。"""
        self.remember(chat_id)
        if chat_id in self.entries:
            self.entries[chat_id].update(
                joined=None, is_admin=None, role="unknown", checked_at=0.0, error=None
            )

    @staticmethod
    def is_group(chat):
        return bool(getattr(chat, "megagroup", False) or isinstance(chat, Chat))

    @staticmethod
    def chat_type(chat):
        if isinstance(chat, Chat):
            return "group"
        if getattr(chat, "megagroup", False) or getattr(chat, "gigagroup", False):
            return "supergroup"
        if isinstance(chat, Channel) or getattr(chat, "broadcast", False):
            return "channel"
        return "unknown"

    def refresh(self, *, force=False):
        if self._stopped or self.mutating:
            return
        for chat_id in self.configured_ids():
            self.remember(int(chat_id))
        if self.task is not None and not self.task.done():
            return
        if (
            force
            or time.monotonic() - self.last_scan > 300
            or any(not row["checked_at"] for row in self.entries.values())
        ):
            self.task = asyncio.create_task(self._scan(), name="guard-group-discovery")

    async def _resolve(self, chat_id, chat, *, count=True):
        entry = self.entries[chat_id]
        entry["checked_at"] = time.monotonic()
        if count:
            self.checked += 1
        entry.update(
            chat_type=self.chat_type(chat),
            username=getattr(chat, "username", None),
            is_admin=None,
            role="unknown",
        )
        if (
            entry["chat_type"] == "unknown"
            or getattr(chat, "left", False)
            or getattr(chat, "deactivated", False)
        ):
            entry.update(joined=False, error=None)
            return
        self.remember(chat_id, getattr(chat, "title", ""))
        try:
            permissions = await asyncio.wait_for(
                self.client.get_permissions(chat_id, self.bot_id()), timeout=8
            )
            participant = getattr(permissions, "participant", None)
            banned = getattr(participant, "banned_rights", None)
            if (
                isinstance(participant, ChannelParticipantLeft)
                or getattr(participant, "left", False)
                or getattr(banned, "view_messages", False)
            ):
                entry.update(joined=False, error=None)
            else:
                creator = getattr(permissions, "is_creator", None)
                admin = getattr(permissions, "is_admin", None)
                # 只有明确的布尔结果才能作为批量退出依据；缺失权限信息不能视作普通成员。
                if creator is True or getattr(participant, "admin_rights", None) is not None:
                    admin = True
                entry.update(
                    joined=True,
                    is_admin=admin if isinstance(admin, bool) else None,
                    role="creator"
                    if creator is True
                    else "admin"
                    if admin is True
                    else "member"
                    if admin is False
                    else "unknown",
                    error=None,
                )
                return chat
        except UserNotParticipantError:
            entry.update(joined=False, error=None)
        except FloodWaitError:
            entry.update(joined=None, error="FloodWaitError")
            raise
        except Exception as error:
            entry.update(joined=None, error=type(error).__name__)

    async def check(self, chat_id, *, count=False):
        """重新向 Telegram 核实一个会话；返回刚查到的实体，失败时清除旧权限。"""
        self.remember(chat_id)
        try:
            chat = await asyncio.wait_for(self.client.get_entity(chat_id), timeout=8)
            return await self._resolve(chat_id, chat, count=count)
        except FloodWaitError:
            self.entries[chat_id].update(
                joined=None, is_admin=None, role="unknown", error="FloodWaitError"
            )
            raise
        except Exception as error:
            self.entries[chat_id].update(
                joined=None,
                is_admin=None,
                role="unknown",
                checked_at=time.monotonic(),
                error=type(error).__name__,
            )
            if count:
                self.checked += 1

    async def _single(self, chat_id, semaphore):
        async with semaphore:
            await self.check(chat_id, count=True)

    async def _scan(self):
        try:
            filename = getattr(getattr(self.client, "session", None), "filename", None)
            for chat_id, name in await asyncio.to_thread(cached_chats, filename):
                # 缓存名称仅作失败时的显示回退，不能证明仍在群内。
                if chat_id not in self.entries or not self.entries[chat_id]["title"]:
                    self.remember(chat_id, name)
            configured = [int(chat_id) for chat_id in self.configured_ids()]
            candidates = list(dict.fromkeys([*configured, *self.entries]))
            self.total, self.checked = len(candidates), 0
            semaphore = asyncio.Semaphore(2)
            for offset in range(0, len(candidates), 40):
                batch = candidates[offset : offset + 40]
                try:
                    chats = await asyncio.wait_for(self.client.get_entity(batch), timeout=12)
                    if not isinstance(chats, (list, tuple)) or len(chats) != len(batch):
                        raise ValueError("incomplete group lookup")
                except Exception:
                    results = await asyncio.gather(
                        *(self._single(chat_id, semaphore) for chat_id in batch),
                        return_exceptions=True,
                    )
                    if any(isinstance(result, FloodWaitError) for result in results):
                        break
                    continue
                # Telethon 的批量 get_entity 保持输入顺序。
                for chat_id, chat in zip(batch, chats, strict=True):
                    async with semaphore:
                        await self._resolve(chat_id, chat)
        except Exception:
            logger.exception("Guard 群组自动发现失败，将在刷新时重试")
        finally:
            self.last_scan = time.monotonic()

    def snapshot(self):
        metadata = {
            str(chat_id): {key: value for key, value in row.items() if key != "checked_at"}
            for chat_id, row in self.entries.items()
        }
        joined = [row for row in metadata.values() if row["joined"] is True]
        joined.sort(key=lambda row: (row["title"].casefold(), row["chat_id"]))
        return {
            "groups": [row for row in joined if row["chat_type"] in {"group", "supergroup"}],
            "chats": joined,
            "metadata": metadata,
            "discovering": self.task is not None and not self.task.done(),
            "checked": self.checked,
            "total": self.total,
            "complete_history_available": False,
        }

    async def stop(self):
        self._stopped = True
        if self.task and not self.task.done():
            self.task.cancel()
            try:
                await self.task
            except asyncio.CancelledError:
                pass
