# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
"""分页扫描已销号成员，保存处理意图，逐项复核；手动与定期任务共用执行器。"""

import asyncio
import logging
import time
import uuid
from datetime import timedelta

from telethon import utils
from telethon.errors import FloodWaitError, UserNotParticipantError
from telethon.tl.functions.channels import EditBannedRequest, GetParticipantsRequest
from telethon.tl.functions.messages import DeleteChatUserRequest, GetFullChatRequest
from telethon.tl.functions.users import GetUsersRequest
from telethon.tl.types import (
    ChannelParticipantLeft,
    ChannelParticipantsSearch,
    Chat,
    ChatBannedRights,
    InputPeerUser,
    InputUser,
    User,
)

from core.infrastructure.database.mysql import utc_now_naive

from .chat_cleanup import ChatCleanupError
from .deleted_cleanup_repository import DeletedCleanupRepository

logger = logging.getLogger(__name__)


class DeletedUserCleanup:
    def __init__(self, guard, repository=None):
        self.guard = guard
        self.directory = guard.directory
        pool = getattr(guard.repository, "pool", None)
        self.repository = repository or (DeletedCleanupRepository(pool) if pool else None)
        self.task = None
        self._wake = asyncio.Event()
        self._stopped = False
        self._owns_directory = False

    @property
    def settings(self):
        return self.guard.config.deleted_user_cleanup

    def authorized(self, actor):
        return actor == "admin" or (
            str(actor).isdigit() and int(actor) in self.guard.default_admins()
        )

    def wake(self):
        self._wake.set()

    def _available(self):
        if self._stopped or not self.repository or not self.guard.bot_id:
            raise ChatCleanupError("已销号清理服务尚未就绪")

    def _groups(self, chat_ids=None):
        directory = self.directory
        snapshot = directory.snapshot()
        if (
            directory.mutating
            or snapshot["discovering"]
            or not directory.last_scan
            or time.monotonic() - directory.last_scan > 300
        ):
            directory.refresh(force=True)
            raise ChatCleanupError("请等待群组列表核实完成后再清理")
        eligible = {
            row["chat_id"]: row
            for row in snapshot["chats"]
            if row["joined"] is True
            and row["is_admin"] is True
            and row["identity_verified"] is True
            and row["is_standalone"] is True
            and row["chat_type"] in {"group", "supergroup"}
            and row["error"] is None
        }
        if chat_ids is not None and any(chat_id not in eligible for chat_id in chat_ids):
            raise ChatCleanupError("只能清理已确认机器人为管理员的群组，请刷新列表")
        chosen = sorted(eligible if chat_ids is None else set(chat_ids))
        return [
            {
                "chat_id": chat_id,
                "title": eligible[chat_id]["title"],
                "status": "scanning",
                "offset": 0,
                "scanned": 0,
                "error": None,
            }
            for chat_id in chosen
        ]

    async def start(self, actor, request_id, chat_ids=None):
        self._available()
        if not self.authorized(actor):
            raise ChatCleanupError("只有系统管理员可以清理已销号成员")
        requested = sorted(set(chat_ids)) if chat_ids is not None else None
        async with self.repository.locked(self.guard.bot_id) as store:
            if store is None:
                raise ChatCleanupError("正在处理清理任务，请稍后重试")
            previous = await store.requested(self.guard.bot_id, request_id)
            if previous:
                if (
                    previous["actor"] != str(actor)
                    or previous["payload"]["requested_chat_ids"] != requested
                ):
                    raise ChatCleanupError("请求标识已用于其他清理操作")
                return await store.view(previous)
            if await store.active(self.guard.bot_id):
                raise ChatCleanupError("已有已销号清理任务，请等待完成")
            groups = self._groups(requested)
            if not groups:
                raise ChatCleanupError("当前没有可清理的管理群组")
            job = await store.create(self.guard.bot_id, actor, request_id, groups, requested)
            self.directory.mutating = self._owns_directory = True
            self.wake()
            return await store.view(job)

    async def status(self):
        result = {
            "available": bool(self.repository and self.guard.bot_id and not self._stopped),
            "settings": self.settings.model_dump(),
            "job": None,
            "history": [],
            "next_run_at": None,
        }
        if not result["available"]:
            return result
        async with self.repository.read() as store:
            history = await store.history(self.guard.bot_id)
            if history:
                result["job"] = await store.view(history[0])
                result["history"] = [
                    {
                        "id": row["id"],
                        "status": row["status"],
                        "trigger_type": row["trigger_type"],
                        "created_at": row["created_at"].isoformat() + "Z",
                    }
                    for row in history
                ]
            last = await store.latest_scheduled(self.guard.bot_id)
            if self.settings.enabled:
                due = (
                    last["created_at"] + timedelta(hours=self.settings.interval_hours)
                    if last
                    else utc_now_naive()
                )
                result["next_run_at"] = due.isoformat() + "Z"
        return result

    async def job(self, job_id):
        self._available()
        async with self.repository.read() as store:
            row = await store.get(self.guard.bot_id, job_id)
            return await store.view(row) if row else None

    async def start_worker(self):
        if self.repository and self.task is None:
            self.task = asyncio.create_task(self._run(), name="guard-deleted-user-cleanup")

    async def _run(self):
        while True:
            self._wake.clear()
            try:
                await self.step()
            except asyncio.CancelledError:
                raise
            except Exception:
                logger.exception("已销号成员清理暂未完成，将从持久记录恢复")
            try:
                await asyncio.wait_for(self._wake.wait(), timeout=1 if self._owns_directory else 5)
            except TimeoutError:
                pass

    async def step(self):
        self._available()
        async with self.repository.locked(self.guard.bot_id) as store:
            if store is None:
                return
            job = await store.active(self.guard.bot_id)
            if job is None:
                self._release_directory()
                if not self.settings.enabled or self.directory.mutating:
                    return
                last = await store.latest_scheduled(self.guard.bot_id)
                if (
                    last
                    and last["created_at"] + timedelta(hours=self.settings.interval_hours)
                    > utc_now_naive()
                ):
                    return
                try:
                    groups = self._groups()
                except ChatCleanupError:
                    return
                if not groups:
                    return
                job = await store.create(
                    self.guard.bot_id, "schedule", uuid.uuid4().hex, groups, None, "scheduled"
                )
            if self.directory.mutating and not self._owns_directory:
                return
            self.directory.mutating = self._owns_directory = True
            permitted = (
                self.settings.enabled
                if job["trigger_type"] == "scheduled"
                else self.authorized(job["actor"])
            )
            if not permitted:
                job.update(
                    status="cancelled", error="定期清理已关闭或操作者权限已撤销，剩余成员未处理"
                )
                await store.save(job)
                self._release_directory()
                return
            if job["retry_at"] > utc_now_naive():
                return
            groups = job["payload"]["groups"]
            index = job["payload"]["group_index"]
            if index >= len(groups):
                job["status"] = "partial" if any(row["error"] for row in groups) else "completed"
                await store.save(job)
                self._release_directory()
                return
            group = groups[index]
            job.update(status="running", error=None)
            item = None
            try:
                chat = await self._group(group["chat_id"])
                if group["status"] == "scanning":
                    await self._scan(store, job, group, chat)
                else:
                    item = await store.pending(job["id"], group["chat_id"])
                    if item:
                        await self._remove(store, job, item, chat)
                    else:
                        group["status"] = "partial" if group["error"] else "completed"
                        job["payload"]["group_index"] += 1
                group.pop("failures", None)
            except FloodWaitError as error:
                job.update(
                    status="waiting",
                    error=f"Telegram 限流，等待 {error.seconds} 秒后继续",
                    retry_at=utc_now_naive() + timedelta(seconds=max(1, error.seconds)),
                )
            except (TimeoutError, ConnectionError, OSError) as error:
                group["failures"] = group.get("failures", 0) + 1
                job.update(
                    status="waiting",
                    error=f"{type(error).__name__}，稍后重新核实并继续",
                    retry_at=utc_now_naive() + timedelta(seconds=15),
                )
                if group["failures"] >= 3 and item is None:
                    group.update(status="failed", error="多次读取成员或群权限失败，请重新清理")
                    job["payload"]["group_index"] += 1
            except Exception as error:
                group.update(
                    status="failed",
                    error=str(error)
                    if isinstance(error, ChatCleanupError)
                    else f"{type(error).__name__}，无法继续核实本群",
                )
                job["payload"]["group_index"] += 1
                logger.warning(
                    "已销号清理跳过群组 chat=%s error=%s", group["chat_id"], type(error).__name__
                )
            await store.save(job)

    async def _group(self, chat_id):
        chat = await self.directory.check(chat_id)
        row = self.directory.entries.get(chat_id, {})
        if chat is None and row.get("error") == "TimeoutError":
            raise TimeoutError("群组权限核实超时")
        if (
            chat is None
            or not self.directory.is_group(chat)
            or row.get("joined") is not True
            or row.get("is_admin") is not True
            or row.get("identity_verified") is not True
        ):
            raise ChatCleanupError("机器人已不在群内、不是管理员或权限无法确认，本群已跳过")
        permissions = await asyncio.wait_for(
            self.guard.client.get_permissions(chat, self.guard.bot_id), 15
        )
        rights = getattr(getattr(permissions, "participant", None), "admin_rights", None)
        if not (
            permissions.is_creator is True
            or (isinstance(chat, Chat) and permissions.is_admin is True)
            or (permissions.is_admin is True and getattr(rights, "ban_users", None) is True)
        ):
            raise ChatCleanupError("机器人缺少移除成员权限，本群已跳过")
        return chat

    async def _rpc(self, request):
        return await asyncio.wait_for(self.guard.client(request, flood_sleep_threshold=0), 20)

    async def _scan(self, store, job, group, chat):
        if isinstance(chat, Chat):
            response = await self._rpc(GetFullChatRequest(chat.id))
            participants = getattr(response.full_chat.participants, "participants", None)
            if participants is None:
                raise ChatCleanupError("Telegram 未提供成员列表，本群未完成扫描")
            total = len(participants)
        else:
            response = await self._rpc(
                GetParticipantsRequest(chat, ChannelParticipantsSearch(""), group["offset"], 200, 0)
            )
            participants, total = response.participants, response.count
        ids = {row.user_id for row in participants if not getattr(row, "left", False)}
        candidates = [
            user
            for user in response.users
            if isinstance(user, User)
            and user.deleted is True
            and user.id in ids
            and user.id != self.guard.bot_id
            and user.id > 0
        ]
        await store.candidates(job["id"], group["chat_id"], candidates)
        group["scanned"] += len(participants)
        group["offset"] += len(participants)
        if isinstance(chat, Chat) or not participants or group["offset"] >= total:
            if group["offset"] < total:
                group["error"] = "Telegram 仅返回部分成员，已处理可核实的账号，请稍后重新扫描"
            group["status"] = "removing"

    async def _remove(self, store, job, item, chat):
        if item["attempts"] >= 3:
            await store.item(item, "failed", "多次核实失败，未确认移除，请重新清理")
            job["payload"]["groups"][job["payload"]["group_index"]]["error"] = (
                "部分成员核实或移除失败"
            )
            return
        await store.attempt(item)
        try:
            async with self.guard.repository.lock(item["user_id"]):
                users = await self._rpc(
                    GetUsersRequest([InputUser(item["user_id"], item["access_hash"] or 0)])
                )
                user = next(
                    (
                        value
                        for value in users
                        if isinstance(value, User) and value.id == item["user_id"]
                    ),
                    None,
                )
                if (
                    user is None
                    or user.deleted is not True
                    or user.min is True
                    or user.id == self.guard.bot_id
                ):
                    await store.item(item, "skipped", "无法确认当前已销号，已保留")
                    return
                try:
                    permissions = await asyncio.wait_for(
                        self.guard.client.get_permissions(chat, user), 15
                    )
                except UserNotParticipantError:
                    await store.item(
                        item,
                        "removed" if item["status"] == "removing" else "skipped",
                        "已确认不在群内",
                    )
                    return
                participant = getattr(permissions, "participant", None)
                if (
                    isinstance(participant, ChannelParticipantLeft)
                    or getattr(participant, "left", False)
                    or getattr(getattr(participant, "banned_rights", None), "view_messages", False)
                ):
                    await store.item(
                        item,
                        "removed" if item["status"] == "removing" else "skipped",
                        "已确认不在群内",
                    )
                    return
                if permissions.is_admin is not False or permissions.is_creator is not False:
                    await store.item(item, "skipped", "管理员或无法确认成员权限，已保留")
                    return
                # 再次复核操作者、机器人身份和群权限，状态变更发生在这里之前时不再移除。
                permitted = (
                    self.settings.enabled
                    if job["trigger_type"] == "scheduled"
                    else self.authorized(job["actor"])
                )
                if not permitted or self.guard.bot_id != job["bot_id"]:
                    raise ChatCleanupError("操作者权限或机器人身份已变化，剩余成员未处理")
                await self._group(item["chat_id"])
                await store.item(item, "removing")
                permitted = (
                    self.settings.enabled
                    if job["trigger_type"] == "scheduled"
                    else self.authorized(job["actor"])
                )
                if not permitted or self.guard.bot_id != job["bot_id"]:
                    raise ChatCleanupError("操作者权限或机器人身份已变化，剩余成员未处理")
                request = (
                    DeleteChatUserRequest(chat.id, utils.get_input_user(user), revoke_history=False)
                    if isinstance(chat, Chat)
                    else EditBannedRequest(
                        chat,
                        InputPeerUser(user.id, user.access_hash or item["access_hash"] or 0),
                        ChatBannedRights(until_date=None, view_messages=True),
                    )
                )
                # 只移除已销号账号，不进行 ban/unban 补偿，不解除原有封禁，不删除消息历史。
                await self._rpc(request)
                await store.item(item, "removed", "已移除已销号成员")
        except FloodWaitError:
            await store.attempt(item, -1)
            raise
        except (TimeoutError, ConnectionError, OSError):
            raise
        except ChatCleanupError:
            raise
        except Exception as error:
            await store.item(item, "failed", f"{type(error).__name__}，未确认移除")
            job["payload"]["groups"][job["payload"]["group_index"]]["error"] = (
                "部分成员核实或移除失败"
            )

    def _release_directory(self):
        if self._owns_directory:
            self.directory.mutating = False
            self._owns_directory = False

    async def stop(self):
        self._stopped = True
        if self.task and not self.task.done():
            self.task.cancel()
            try:
                await self.task
            except asyncio.CancelledError:
                pass
        self._release_directory()
