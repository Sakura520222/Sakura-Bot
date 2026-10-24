# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
import asyncio
from contextlib import asynccontextmanager
from copy import deepcopy
from types import SimpleNamespace

from telethon import utils
from telethon.errors import UserNotParticipantError
from telethon.tl.functions.channels import EditBannedRequest, GetParticipantsRequest
from telethon.tl.functions.messages import DeleteChatUserRequest, GetFullChatRequest
from telethon.tl.functions.users import GetUsersRequest
from telethon.tl.types import (
    Channel,
    ChannelParticipant,
    ChannelParticipantBanned,
    Chat,
    ChatBannedRights,
    ChatParticipant,
    PeerUser,
    User,
    UserStatusEmpty,
)

from core.infrastructure.database.mysql import utc_now_naive

from .conftest import CHAT, FakeTelegram

BASIC = -42
CHANNEL = CHAT - 2


class DeletedClient(FakeTelegram):
    def __init__(self):
        super().__init__()
        self.users = {
            200: User(
                id=200,
                access_hash=100,
                first_name="Deleted Account",
                deleted=False,
                status=UserStatusEmpty(),
            ),
            201: User(id=201, first_name="已销号", deleted=True),
            202: User(id=202, access_hash=102, first_name="已销号管理员", deleted=True),
            203: User(id=203, access_hash=103, first_name="长期未登录", status=UserStatusEmpty()),
            999: User(id=999, access_hash=999, first_name="主 Bot", deleted=True, is_self=True),
        }
        self.chats = {
            CHAT: Channel(
                id=utils.resolve_id(CHAT)[0],
                title="管理超级群",
                photo=None,
                date=utc_now_naive(),
                megagroup=True,
                access_hash=90,
            ),
            BASIC: Chat(
                id=42,
                title="管理普通群",
                photo=None,
                participants_count=3,
                date=utc_now_naive(),
                version=1,
            ),
            CHANNEL: Channel(
                id=utils.resolve_id(CHANNEL)[0],
                title="频道",
                photo=None,
                date=utc_now_naive(),
                broadcast=True,
                access_hash=91,
            ),
        }
        self.members = {CHAT: set(self.users), BASIC: {200, 201, 999}, CHANNEL: {201}}
        self.admins = {202}
        self.bot_admin = True
        self.ban_users = True
        self.page_size = 200
        self.restricted = {201, 203}
        self.partial = False
        self.requests = []
        self.removals = []
        self.failures = {}
        self.fail_after_remove = False
        self.after_profile = None

    async def get_entity(self, identifier):
        if isinstance(identifier, int) and identifier < 0:
            return self.chats[identifier]
        return self.users[getattr(identifier, "id", identifier)]

    async def get_permissions(self, chat_id, user_id):
        chat_id = chat_id if isinstance(chat_id, int) else utils.get_peer_id(chat_id)
        user_id = (
            user_id
            if isinstance(user_id, int)
            else getattr(user_id, "user_id", None) or getattr(user_id, "id", None)
        )
        if user_id not in self.members[chat_id]:
            raise UserNotParticipantError(request=None)
        admin = self.bot_admin if user_id == 999 else user_id in self.admins
        return SimpleNamespace(
            is_admin=admin,
            is_creator=False,
            participant=SimpleNamespace(
                admin_rights=SimpleNamespace(ban_users=self.ban_users, delete_messages=True)
                if admin
                else None,
                left=False,
                banned_rights=None,
            ),
        )

    async def __call__(self, request, **kwargs):
        self.requests.append(request)
        failures = self.failures.get(type(request), [])
        if failures:
            raise failures.pop(0)
        if isinstance(request, GetParticipantsRequest):
            chat_id = utils.get_peer_id(request.channel)
            members = sorted(self.members[chat_id])
            ids = members[request.offset : request.offset + min(self.page_size, request.limit)]
            if self.partial and request.offset:
                ids = []
            return SimpleNamespace(
                participants=[
                    ChannelParticipantBanned(
                        PeerUser(user_id),
                        999,
                        utc_now_naive(),
                        ChatBannedRights(until_date=None, send_messages=True),
                    )
                    if user_id in self.restricted
                    else ChannelParticipant(user_id, utc_now_naive())
                    for user_id in ids
                ],
                users=[deepcopy(self.users[user_id]) for user_id in ids],
                count=len(members),
            )
        if isinstance(request, GetFullChatRequest):
            ids = sorted(self.members[-request.chat_id])
            return SimpleNamespace(
                full_chat=SimpleNamespace(
                    participants=SimpleNamespace(
                        participants=[
                            ChatParticipant(user_id, 999, utc_now_naive()) for user_id in ids
                        ]
                    )
                ),
                users=[deepcopy(self.users[user_id]) for user_id in ids],
            )
        if isinstance(request, GetUsersRequest):
            users = [deepcopy(self.users[value.user_id]) for value in request.id]
            if self.after_profile:
                self.after_profile()
            return users
        if isinstance(request, (EditBannedRequest, DeleteChatUserRequest)):
            if isinstance(request, EditBannedRequest):
                chat_id, user_id = utils.get_peer_id(request.channel), request.participant.user_id
                assert request.banned_rights.view_messages is True
            else:
                chat_id, user_id = -request.chat_id, request.user_id.user_id
                assert request.revoke_history is True
            self.members[chat_id].discard(user_id)
            self.removals.append((chat_id, user_id))
            if self.fail_after_remove:
                self.fail_after_remove = False
                raise TimeoutError("已执行但响应丢失")
            return True
        raise AssertionError(type(request).__name__)


class MemoryCleanupStore:
    def __init__(self):
        self.jobs = {}
        self.items = {}

    async def active(self, bot_id):
        return next(
            (
                deepcopy(row)
                for row in self.jobs.values()
                if row["bot_id"] == bot_id and row["status"] in {"running", "waiting"}
            ),
            None,
        )

    async def requested(self, bot_id, request_id):
        return next(
            (
                deepcopy(row)
                for row in self.jobs.values()
                if row["bot_id"] == bot_id and row["request_id"] == request_id
            ),
            None,
        )

    async def get(self, bot_id, job_id):
        row = self.jobs.get(job_id)
        return deepcopy(row) if row and row["bot_id"] == bot_id else None

    async def latest_scheduled(self, bot_id):
        rows = [
            row
            for row in self.jobs.values()
            if row["bot_id"] == bot_id and row["trigger_type"] == "scheduled"
        ]
        return deepcopy(max(rows, key=lambda row: row["created_at"])) if rows else None

    async def create(self, bot_id, actor, request_id, groups, requested_ids, trigger="manual"):
        import uuid

        now = utc_now_naive()
        row = {
            "id": uuid.uuid4().hex,
            "bot_id": bot_id,
            "actor": str(actor),
            "request_id": request_id,
            "trigger_type": trigger,
            "status": "running",
            "error": None,
            "retry_at": now,
            "created_at": now,
            "updated_at": now,
            "payload": {
                "groups": deepcopy(groups),
                "group_index": 0,
                "requested_chat_ids": requested_ids,
            },
        }
        self.jobs[row["id"]] = deepcopy(row)
        return row

    async def save(self, row):
        self.jobs[row["id"]] = deepcopy(row)

    async def candidates(self, job_id, chat_id, users):
        for user in users:
            self.items.setdefault(
                (job_id, chat_id, user.id),
                {
                    "job_id": job_id,
                    "chat_id": chat_id,
                    "user_id": user.id,
                    "access_hash": user.access_hash,
                    "status": "pending",
                    "attempts": 0,
                    "message": "",
                    "updated_at": utc_now_naive(),
                },
            )

    async def pending(self, job_id, chat_id):
        rows = [
            row
            for row in self.items.values()
            if row["job_id"] == job_id
            and row["chat_id"] == chat_id
            and row["status"] in {"pending", "removing"}
        ]
        return deepcopy(min(rows, key=lambda row: row["user_id"])) if rows else None

    async def item(self, row, status, message=""):
        self.items[row["job_id"], row["chat_id"], row["user_id"]].update(
            status=status, message=message
        )

    async def attempt(self, row, delta=1):
        current = self.items[row["job_id"], row["chat_id"], row["user_id"]]
        current["attempts"] = max(0, current["attempts"] + delta)

    async def history(self, bot_id, limit=10):
        return deepcopy(
            sorted(
                (row for row in self.jobs.values() if row["bot_id"] == bot_id),
                key=lambda row: row["created_at"],
                reverse=True,
            )[:limit]
        )

    async def view(self, job):
        rows = [deepcopy(row) for row in self.items.values() if row["job_id"] == job["id"]]
        counts = {
            status: sum(row["status"] == status for row in rows)
            for status in {row["status"] for row in rows}
        }
        return {
            "id": job["id"],
            "actor": job["actor"],
            "trigger_type": job["trigger_type"],
            "status": job["status"],
            "groups": deepcopy(job["payload"]["groups"]),
            "checked_groups": job["payload"]["group_index"],
            "error": job["error"],
            "counts": counts,
            "results": [
                {key: row[key] for key in ("chat_id", "user_id", "status", "message", "updated_at")}
                for row in rows
                if row["status"] not in {"pending", "removing"}
            ],
            **{key: job[key].isoformat() + "Z" for key in ("created_at", "updated_at", "retry_at")},
        }


class MemoryCleanupRepository:
    def __init__(self):
        self.store = MemoryCleanupStore()
        self.lock = asyncio.Lock()

    @asynccontextmanager
    async def locked(self, bot_id):
        if self.lock.locked():
            yield None
        else:
            async with self.lock:
                yield self.store

    @asynccontextmanager
    async def read(self):
        yield self.store
