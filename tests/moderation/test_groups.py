# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
"""群组自动发现的成员校验、名称刷新与只读缓存边界。"""

import hashlib
import sqlite3
from types import SimpleNamespace

from telethon.errors import UserNotParticipantError

from core.moderation.groups import GroupDirectory, cached_chats

from .conftest import CHAT


class DirectoryClient:
    def __init__(self, filename=None):
        self.session = SimpleNamespace(filename=filename)
        self.chats = {
            CHAT: SimpleNamespace(title="真实群名", megagroup=True, left=False),
            CHAT - 1: SimpleNamespace(title="已退出群", megagroup=True, left=False),
            CHAT - 2: SimpleNamespace(
                title="广播频道", megagroup=False, broadcast=True, left=False
            ),
        }
        self.members = {CHAT, CHAT - 2}
        self.fail_lookup = False

    async def get_entity(self, identifiers):
        if self.fail_lookup:
            raise RuntimeError("offline")
        if isinstance(identifiers, list):
            return [self.chats[identifier] for identifier in identifiers]
        return self.chats[identifiers]

    async def get_permissions(self, chat_id, user_id):
        if chat_id not in self.members:
            raise UserNotParticipantError(request=None)
        return SimpleNamespace(
            is_admin=False,
            is_creator=False,
            participant=SimpleNamespace(left=False, banned_rights=None),
        )


def session_file(tmp_path):
    path = tmp_path / "main-bot.session"
    connection = sqlite3.connect(path)
    connection.execute("CREATE TABLE entities (id INTEGER PRIMARY KEY, name TEXT, date INTEGER)")
    connection.executemany(
        "INSERT INTO entities VALUES (?,?,?)",
        [
            (CHAT, "旧群名", 3),
            (CHAT - 1, "旧退出群", 2),
            (CHAT - 2, "频道", 1),
            (100, "私人联系人", 4),
        ],
    )
    connection.commit()
    connection.close()
    return path


def test_entity_cache_is_read_only_and_excludes_users(tmp_path):
    path = session_file(tmp_path)
    before = hashlib.sha256(path.read_bytes()).hexdigest()
    rows = cached_chats(str(path))
    assert len(rows) == 3 and all(row[0] < 0 for row in rows)
    assert hashlib.sha256(path.read_bytes()).hexdigest() == before
    assert not cached_chats(str(tmp_path / "missing.session"))
    assert not (tmp_path / "missing.session").exists()


async def test_discovery_resolves_current_names_and_confirms_membership(tmp_path):
    client = DirectoryClient(str(session_file(tmp_path)))
    directory = GroupDirectory(client, lambda: [str(CHAT)], lambda: 999)
    directory.refresh(force=True)
    await directory.task
    snapshot = directory.snapshot()
    assert [row["chat_id"] for row in snapshot["groups"]] == [CHAT]
    assert snapshot["groups"][0]["title"] == "真实群名"
    assert snapshot["metadata"][str(CHAT - 1)]["joined"] is False
    assert snapshot["metadata"][str(CHAT - 2)]["joined"] is True
    assert snapshot["metadata"][str(CHAT - 2)]["chat_type"] == "channel"
    assert len(snapshot["chats"]) == 2
    assert snapshot["checked"] == snapshot["total"] == 3
    assert not snapshot["complete_history_available"]
    await directory.stop()


async def test_configured_ids_work_without_sqlite_and_refresh_names():
    client = DirectoryClient()
    directory = GroupDirectory(client, lambda: [str(CHAT)], lambda: 999)
    directory.refresh(force=True)
    await directory.task
    client.chats[CHAT].title = "改名后的群"
    directory.refresh(force=True)
    await directory.task
    assert directory.snapshot()["groups"][0]["title"] == "改名后的群"
    client.members.clear()
    directory.refresh(force=True)
    await directory.task
    assert not directory.snapshot()["groups"]
    await directory.stop()


async def test_lookup_failure_never_claims_cached_group_is_still_joined(tmp_path):
    client = DirectoryClient(str(session_file(tmp_path)))
    client.fail_lookup = True
    directory = GroupDirectory(client, lambda: [str(CHAT)], lambda: 999)
    directory.refresh(force=True)
    await directory.task
    assert not directory.snapshot()["groups"]
    assert directory.snapshot()["metadata"][str(CHAT)]["title"] == "旧群名"
    assert directory.snapshot()["metadata"][str(CHAT)]["error"] == "RuntimeError"
    await directory.stop()


def test_join_message_and_leave_events_keep_directory_current():
    directory = GroupDirectory(DirectoryClient(), lambda: [], lambda: 999)
    directory.observe(CHAT, SimpleNamespace(title="新加入的群", megagroup=True, left=False))
    assert directory.snapshot()["groups"][0]["title"] == "新加入的群"
    directory.observe(CHAT, left=True)
    assert not directory.snapshot()["groups"]
