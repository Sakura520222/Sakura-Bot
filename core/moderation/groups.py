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

from telethon import utils
from telethon.errors import FloodWaitError, UserNotParticipantError
from telethon.tl.types import Channel, ChannelParticipantLeft, Chat, PeerChannel

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
    def __init__(self, client, configured_ids, bot_id, history_store=None):
        self.client = client
        self.configured_ids = configured_ids
        self.bot_id = bot_id
        self.history_store = history_store
        self.cleaned_history = set()
        self._history_bot_id = None
        self._restore_history = set()
        self.observed_versions = {}
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
                "is_standalone": True,
                "parent_chat_id": None,
                "identity_verified": False,
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
        self.remember(chat_id)
        entry = self.entries.get(chat_id)
        if entry is None:
            return
        self.observed_versions[chat_id] = self.observed_versions.get(chat_id, 0) + 1
        self.restore_history(chat_id)
        if chat is not None:
            if not self._identify(chat_id, chat):
                return
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

    def _identify(self, chat_id, chat):
        entry = self.entries[chat_id]
        try:
            matches = utils.get_peer_id(chat) == chat_id
        except (TypeError, ValueError):
            matches = False
        if not matches:
            entry.update(
                joined=None,
                is_admin=None,
                role="unknown",
                identity_verified=False,
                checked_at=time.monotonic(),
                error="ChatIdentityMismatch",
            )
            return False
        self.remember(chat_id, getattr(chat, "title", ""))
        kind = self.chat_type(chat)
        entry.update(
            chat_type=kind,
            username=getattr(chat, "username", None),
            identity_verified=True,
            is_standalone=kind != "channel_dm",
            parent_chat_id=None,
        )
        if kind == "channel_dm":
            # 频道私信不是独立加入的群组；其成员身份不能代替所属频道的管理员身份。
            # linked_monoforum_id 在私信会话上指向原频道，在原频道上则指向私信会话。
            parent_id = getattr(chat, "linked_monoforum_id", None)
            if isinstance(parent_id, int) and not isinstance(parent_id, bool) and parent_id > 0:
                entry["parent_chat_id"] = utils.get_peer_id(PeerChannel(parent_id))
                self.remember(entry["parent_chat_id"])
            entry.update(joined=None, is_admin=None, role="unknown", error=None)
            return False
        return True

    def invalidate(self, chat_id):
        """成员或权限变化后先清除旧判断，后台重新核实。"""
        self.remember(chat_id)
        self.observed_versions[chat_id] = self.observed_versions.get(chat_id, 0) + 1
        self.restore_history(chat_id)
        if chat_id in self.entries:
            self.entries[chat_id].update(
                joined=None,
                is_admin=None,
                role="unknown",
                identity_verified=False,
                checked_at=0.0,
                error=None,
            )

    @staticmethod
    def is_group(chat):
        return GroupDirectory.chat_type(chat) in {"group", "supergroup"}

    @staticmethod
    def chat_type(chat):
        if (
            getattr(chat, "monoforum", False) is True
            or getattr(chat, "is_direct_messages", False) is True
        ):
            return "channel_dm"
        if isinstance(chat, Chat):
            return "group"
        if getattr(chat, "megagroup", False) or getattr(chat, "gigagroup", False):
            return "supergroup"
        if isinstance(chat, Channel) or getattr(chat, "broadcast", False):
            return "channel"
        return "unknown"

    @staticmethod
    def can_leave(row):
        return (
            row.get("is_standalone") is True
            and row.get("identity_verified") is True
            and row.get("chat_type") in {"group", "supergroup", "channel"}
            and row.get("joined") is True
            and row.get("is_admin") is False
            and row.get("error") is None
        )

    def can_clean_history(self, row):
        # 只接受明确的访问拒绝；超时、限流、身份不一致等不能当作失效记录。
        return (
            row.get("chat_id") not in self.cleaned_history
            and str(row.get("chat_id")) not in {str(key) for key in self.configured_ids()}
            and row.get("is_standalone") is True
            and row.get("joined") is None
            and row.get("is_admin") is not True
            and row.get("error") == "ChannelPrivateError"
        )

    def restore_history(self, chat_id):
        if chat_id in self.cleaned_history:
            self.cleaned_history.discard(chat_id)
            self._restore_history.add(chat_id)

    async def _sync_history(self, *, load=False):
        if not self.history_store or not self.bot_id():
            return
        bot_id = self.bot_id()
        if self._history_bot_id != bot_id:
            self.cleaned_history.clear()
            self._restore_history.clear()
            self._history_bot_id = bot_id
        try:
            restoring = set(self._restore_history)
            if restoring:
                await self.history_store.restore_chat_history(bot_id, restoring)
                self._restore_history.difference_update(restoring)
            if load:
                cleaned = await self.history_store.cleaned_chat_history(bot_id)
                self.cleaned_history = set(cleaned) - self._restore_history
        except Exception:
            logger.warning("Guard 历史清理状态暂时无法同步，将在刷新时重试")

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
        version = self.observed_versions.get(chat_id, 0)
        entry["checked_at"] = time.monotonic()
        if count:
            self.checked += 1
        entry.update(is_admin=None, role="unknown", identity_verified=False)
        if not self._identify(chat_id, chat):
            return
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
            if self.observed_versions.get(chat_id, 0) != version:
                return
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
                self.restore_history(chat_id)
                return chat
        except UserNotParticipantError:
            if self.observed_versions.get(chat_id, 0) == version:
                entry.update(joined=False, error=None)
        except FloodWaitError:
            if self.observed_versions.get(chat_id, 0) == version:
                entry.update(joined=None, error="FloodWaitError")
            raise
        except Exception as error:
            if self.observed_versions.get(chat_id, 0) == version:
                entry.update(joined=None, error=type(error).__name__)

    async def check(self, chat_id, *, count=False):
        """重新向 Telegram 核实一个会话；返回刚查到的实体，失败时清除旧权限。"""
        self.remember(chat_id)
        version = self.observed_versions.get(chat_id, 0)
        try:
            chat = await asyncio.wait_for(self.client.get_entity(chat_id), timeout=8)
            if self.observed_versions.get(chat_id, 0) != version:
                if count:
                    self.checked += 1
                return
            return await self._resolve(chat_id, chat, count=count)
        except FloodWaitError:
            if self.observed_versions.get(chat_id, 0) == version:
                self.entries[chat_id].update(
                    joined=None,
                    is_admin=None,
                    role="unknown",
                    identity_verified=False,
                    error="FloodWaitError",
                )
            raise
        except Exception as error:
            if self.observed_versions.get(chat_id, 0) != version:
                if count:
                    self.checked += 1
                return
            self.entries[chat_id].update(
                joined=None,
                is_admin=None,
                role="unknown",
                identity_verified=False,
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
            await self._sync_history(load=True)
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
            await self._sync_history()
            self.last_scan = time.monotonic()

    def snapshot(self):
        configured = {str(key) for key in self.configured_ids()}
        metadata = {
            str(chat_id): {key: value for key, value in row.items() if key != "checked_at"}
            for chat_id, row in self.entries.items()
            if not (
                chat_id in self.cleaned_history
                and row["joined"] is not True
                and row["is_standalone"]
                and str(chat_id) not in configured
            )
        }
        joined = [
            row
            for row in metadata.values()
            if row["joined"] is True and row["is_standalone"] and row["identity_verified"]
        ]
        joined.sort(key=lambda row: (row["title"].casefold(), row["chat_id"]))
        return {
            "groups": [row for row in joined if row["chat_type"] in {"group", "supergroup"}],
            "chats": joined,
            "related_chat_count": sum(not row["is_standalone"] for row in metadata.values()),
            "history_cleanable_count": sum(
                self.can_clean_history(row) for row in metadata.values()
            ),
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
