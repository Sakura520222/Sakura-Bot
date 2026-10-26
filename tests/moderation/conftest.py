# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
"""只模拟 Telegram 和存储边界，测试真实的群管、审核与验证状态机。"""

import asyncio
from collections import defaultdict
from contextlib import asynccontextmanager
from copy import deepcopy
from types import SimpleNamespace
from unittest.mock import AsyncMock, MagicMock

import pytest
from telethon import utils
from telethon.tl.functions.channels import EditBannedRequest
from telethon.tl.functions.messages import (
    DeleteChatUserRequest,
    ForwardMessagesRequest,
    HideChatJoinRequestRequest,
    SendMessageRequest,
)
from telethon.tl.types import Channel, Chat, ChatBannedRights, PeerChat

from core.infrastructure.database.mysql import utc_now_naive
from core.moderation.config import GuardConfig, GuardGroup
from core.moderation.guest_messages import blacklist_task, current_blacklist_task
from core.moderation.moderator import Moderator
from core.moderation.telegram import rights_dict, rights_object

CHAT = -1001234567890
USER = 200
ADMIN = 10


class MemoryStore:
    def __init__(self):
        self.cases = {}
        self.sessions = {}
        self.white = set()
        self.black = {}
        self.notifications = {}
        self.fail_finish = 0
        self.fail_pending = 0

    async def case(self, case_id):
        return deepcopy(self.cases.get(case_id))

    async def case_for_event(self, event_key):
        return next(
            (deepcopy(row) for row in self.cases.values() if row["event_key"] == event_key), None
        )

    async def later_mute(self, case):
        start = case["payload"].get("restriction_started_at") or case["created_at"].isoformat()
        return next(
            (
                deepcopy(row)
                for row in self.cases.values()
                if row["id"] != case["id"]
                and (row["chat_id"], row["user_id"]) == (case["chat_id"], case["user_id"])
                and row["action"] == "mute"
                and row["status"]
                in {
                    "executing",
                    "done",
                    "reviewing_allow",
                    "reviewing_unban",
                    "reviewing_ban",
                    "reviewing_blacklist",
                }
                and (row["payload"].get("restriction_started_at") or row["created_at"].isoformat())
                > start
            ),
            None,
        )

    async def active_ban(self, chat_id, user_id, *, exclude_case_id=None):
        return next(
            (
                deepcopy(row)
                for row in self.cases.values()
                if row["chat_id"] == chat_id
                and row["user_id"] == user_id
                and row["id"] != exclude_case_id
                and (
                    row["action"] == "ban"
                    or row["payload"].get("review_ban_confirmed")
                    or row["status"] in {"reviewing_ban", "reviewing_blacklist"}
                )
                and row["status"]
                in {
                    "executing",
                    "pending",
                    "reviewing_allow",
                    "reviewing_blacklist",
                    "reviewing_unban",
                    "reviewing_ban",
                }
            ),
            None,
        )

    async def create_case(self, case):
        assert not await self.case_for_event(case["event_key"])
        self.cases[case["id"]] = deepcopy(
            {
                **case,
                "created_at": utc_now_naive(),
                "updated_at": utc_now_naive(),
                "reviewer_id": case.get("reviewer_id"),
                "reviewer_actor": case.get("reviewer_actor"),
                "reviewed_at": case.get("reviewed_at"),
                "error": None,
            }
        )

    async def update_case(self, case_id, status, *, error=None, payload=None):
        if status == "pending" and self.fail_pending:
            self.fail_pending -= 1
            raise ConnectionError("injected commit failure")
        self.cases[case_id].update(status=status, error=error, updated_at=utc_now_naive())
        if payload is not None:
            self.cases[case_id]["payload"] = deepcopy(payload)

    async def blacklist(self, user_id):
        return deepcopy(self.black.get(user_id))

    async def touch_blacklist(self, user_id):
        self.black[user_id]["last_seen_at"] = utc_now_naive()

    async def whitelisted(self, chat_id, user_id):
        return (chat_id, user_id) in self.white

    async def set_whitelist(
        self, chat_id, user_id, reviewer_id, enabled=True, *, reviewer_actor=None
    ):
        if enabled:
            self.white.add((chat_id, user_id))
        else:
            self.white.discard((chat_id, user_id))

    async def claim_review(self, case_id, action, reviewer_id, *, reviewer_actor=None):
        case = self.cases[case_id]
        if case["status"] not in {"pending", "done"} and not (
            action == "kick"
            and case["action"] == "report"
            and case["status"] in {"allowed", "blacklisted", "unbanned", "unmuted", "ignored"}
        ):
            return False
        case.update(
            status=f"reviewing_{action}",
            reviewer_id=reviewer_id,
            reviewer_actor=reviewer_actor,
            reviewed_at=utc_now_naive(),
        )
        return True

    async def finish_review(self, case, action, *, guest_parent=None):
        if self.fail_finish:
            self.fail_finish -= 1
            raise ConnectionError("injected transaction failure")
        if guest_parent is not None:
            parent = await self.case(guest_parent[0])
            if not current_blacklist_task(parent, guest_parent[1]) or not await self.blacklist(
                parent["user_id"]
            ):
                raise ValueError("guest_source_decision_changed")
            await self.create_case(case)
        if action == "allow":
            if case["user_id"] in self.black:
                raise ValueError("global_blacklist_conflict")
            self.white.add((case["chat_id"], case["user_id"]))
        elif action == "blacklist" and not (guest_parent and case["user_id"] in self.black):
            self.black[case["user_id"]] = deepcopy(case)
            self.white.discard((case["chat_id"], case["user_id"]))
        payload = deepcopy(case["payload"])
        payload["review_history"] = [
            *payload.get("review_history", []),
            {
                "action": action,
                "actor": case.get("reviewer_actor"),
                "at": utc_now_naive().isoformat(),
            },
        ]
        if action == "blacklist":
            payload = blacklist_task(payload, case.get("reviewer_id"), case.get("reviewer_actor"))
        unban_status = (
            "unbanned"
            if case["action"] == "ban" or payload.get("review_ban_confirmed")
            else "unmuted"
            if case["action"] == "mute"
            else "ignored"
        )
        if action == "ban":
            payload["review_ban_confirmed"] = True
            payload["review_generation"] = payload.get("review_generation", 0) + 1
        await self.update_case(
            case["id"],
            "done"
            if guest_parent
            else {
                "allow": "allowed",
                "blacklist": "blacklisted",
                "unban": unban_status,
                "ban": "pending",
                "kick": "kicked",
            }[action],
            payload=payload,
        )

    async def verification(self, chat_id, user_id):
        return deepcopy(self.sessions.get((chat_id, user_id)))

    async def verification_by_id(self, session_id):
        return next(
            (deepcopy(row) for row in self.sessions.values() if row["id"] == session_id), None
        )

    async def save_verification(self, session):
        self.sessions[(session["chat_id"], session["user_id"])] = deepcopy(session)

    async def notices(self, case_id):
        return [deepcopy(row) for (key, _), row in self.notifications.items() if key == case_id]

    async def notice(self, case_id, target_id, message_id, status, error=None, attempts=0):
        from datetime import timedelta

        previous = self.notifications.get((case_id, target_id), {})
        if previous.get("synced_status") == "removed" or (
            previous.get("synced_status") == "deleting" and status not in {"deleting", "removed"}
        ):
            return
        self.notifications[(case_id, target_id)] = {
            "case_id": case_id,
            "target_id": target_id,
            "message_id": message_id,
            "synced_status": status,
            "error": error,
            "attempts": attempts,
            "retry_at": utc_now_naive() + timedelta(seconds=30),
        }


class MemoryRepository:
    def __init__(self):
        self.store = MemoryStore()
        self.locks = defaultdict(asyncio.Lock)
        self.cleaned_history = {}

    async def cleaned_chat_history(self, bot_id):
        return {chat_id for (bot, chat_id) in self.cleaned_history if bot == bot_id}

    async def mark_chat_history_cleaned(self, bot_id, chat_id, actor):
        self.cleaned_history[bot_id, chat_id] = str(actor)

    async def restore_chat_history(self, bot_id, chat_ids):
        for chat_id in chat_ids:
            self.cleaned_history.pop((bot_id, chat_id), None)

    @asynccontextmanager
    async def lock(self, user_id):
        async with self.locks[user_id]:
            yield self.store

    async def get_case(self, case_id):
        return await self.store.case(case_id)

    async def get_verification(self, session_id):
        return await self.store.verification_by_id(session_id)

    async def work(self, *, control_policy=None):
        return deepcopy(list(self.store.cases.values())), deepcopy(
            list(self.store.sessions.values())
        )

    async def cleanup(self, days):
        pass


class FakeTelegram:
    def __init__(self):
        self.users = {
            USER: SimpleNamespace(
                id=USER, first_name="正常成员", last_name=None, username="alice", bot=False
            )
        }
        self.member_rights = defaultdict(lambda: rights_dict(None))
        self.admins = set()
        self.ban_actors = {}
        self.sent = {}
        self.source_messages = {}
        self.forwarded = {}
        self.forward_attempts = []
        self.random_ids = {}
        self.deleted = set()
        self.calls = []
        self.fail = {}
        self.add_event_handler = MagicMock()
        self.remove_event_handler = MagicMock()

    async def get_entity(self, identifier):
        identifier = getattr(identifier, "user_id", identifier)
        if identifier < 0:
            bare_id, peer_type = utils.resolve_id(identifier)
            if peer_type is PeerChat:
                return Chat(
                    id=bare_id,
                    title="测试群",
                    photo=None,
                    participants_count=2,
                    date=utc_now_naive(),
                    version=1,
                )
            return Channel(
                id=bare_id,
                title="测试群",
                photo=None,
                date=utc_now_naive(),
                megagroup=True,
            )
        return self.users.get(
            identifier,
            SimpleNamespace(
                id=identifier, first_name="user", last_name=None, username=None, bot=False
            ),
        )

    async def get_me(self):
        return SimpleNamespace(id=999)

    async def get_permissions(self, chat_id, user_id):
        if "permissions" in self.fail:
            raise self.fail["permissions"]
        return SimpleNamespace(
            is_admin=user_id in self.admins,
            is_creator=False,
            participant=SimpleNamespace(
                kicked_by=self.ban_actors.get((chat_id, user_id)),
                banned_rights=rights_object(self.member_rights[(chat_id, user_id)]),
                admin_rights=SimpleNamespace(
                    ban_users=True, delete_messages=True, invite_users=True
                ),
            ),
        )

    async def edit_permissions(self, chat_id, user_id, *, view_messages=True):
        action = "unban" if view_messages else "ban"
        self.calls.append((action, chat_id, user_id))
        if action in self.fail:
            raise self.fail[action]
        self.ban_actors[chat_id, user_id] = None if view_messages else 999
        self.member_rights[(chat_id, user_id)] = rights_dict(
            ChatBannedRights(until_date=None, view_messages=not view_messages)
        )

    async def get_input_entity(self, identifier):
        from telethon.tl.types import InputPeerUser, PeerUser

        if isinstance(identifier, PeerUser):
            if "profile" in self.fail:
                raise self.fail["profile"]
            return InputPeerUser(identifier.user_id, 12345)
        return identifier

    def build_reply_markup(self, buttons):
        return buttons

    async def __call__(self, request):
        if isinstance(request, DeleteChatUserRequest):
            self.calls.append(("kick", -request.chat_id, request.user_id.user_id))
            if "kick" in self.fail:
                raise self.fail["kick"]
            return SimpleNamespace(updates=[])
        if isinstance(request, ForwardMessagesRequest):
            self.forward_attempts.append(request)
            if request.to_peer in self.fail:
                raise self.fail[request.to_peer]
            if "forward" in self.fail:
                raise self.fail["forward"]
            self.forwarded.setdefault(request.random_id[0], request)
            return SimpleNamespace(updates=[])
        if isinstance(request, EditBannedRequest):
            self.calls.append(("restrict", request.channel, request.participant))
            if "restrict" in self.fail:
                raise self.fail["restrict"]
            self.member_rights[(request.channel, request.participant)] = rights_dict(
                request.banned_rights
            )
            return True
        if isinstance(request, HideChatJoinRequestRequest):
            self.calls.append(
                ("approve" if request.approved else "reject", request.peer, request.user_id)
            )
            if "request" in self.fail:
                raise self.fail["request"]
            return True
        assert isinstance(request, SendMessageRequest)
        if request.peer in self.fail:
            raise self.fail[request.peer]
        if request.random_id in self.random_ids:
            message_id = self.random_ids[request.random_id]
        else:
            message_id = len(self.random_ids) + 1
            self.random_ids[request.random_id] = message_id
        self.sent[(request.peer, message_id)] = {
            "text": request.message,
            "buttons": request.reply_markup,
            "entities": request.entities or [],
        }
        return SimpleNamespace(id=message_id)

    async def edit_message(self, chat_id, message_id, text, **kwargs):
        if chat_id in self.fail:
            raise self.fail[chat_id]
        self.sent[(chat_id, message_id)] = {
            "text": text,
            "buttons": kwargs.get("buttons"),
            "entities": kwargs.get("formatting_entities") or [],
        }

    async def delete_messages(self, chat_id, ids):
        self.calls.append(("delete", chat_id, tuple(ids)))
        if "delete" in self.fail:
            raise self.fail["delete"]
        self.deleted.update((chat_id, identifier) for identifier in ids)
        for identifier in ids:
            self.sent.pop((chat_id, identifier), None)

    async def get_messages(self, chat_id, *, ids):
        if (chat_id, ids) in self.deleted:
            return None
        if (chat_id, ids) in self.source_messages:
            return self.source_messages[chat_id, ids]
        return SimpleNamespace(id=ids) if (chat_id, ids) in self.sent else None


@pytest.fixture
def guard():
    repository = MemoryRepository()
    client = FakeTelegram()
    config = GuardConfig(groups={str(CHAT): GuardGroup(enabled=True, verify_join=False)})
    service = Moderator(client, repository, config, default_admins=lambda: [ADMIN, 11])
    service.bot_id = 999
    service.ban_history.revoke = AsyncMock(return_value=True)
    return service


@pytest.fixture
def store(guard):
    return guard.repository.store
