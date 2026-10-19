# Copyright 2026 Sakura-Bot
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
"""预览并退出主 Bot 非管理员会话；名单固定、逐项复核、后台执行。"""

import asyncio
import logging
import secrets
import time
from copy import deepcopy

from telethon.errors import FloodWaitError, UserNotParticipantError
from telethon.tl.functions.channels import LeaveChannelRequest
from telethon.tl.functions.messages import DeleteChatUserRequest
from telethon.tl.types import Chat, InputUserSelf

logger = logging.getLogger(__name__)


class ChatCleanupError(ValueError):
    pass


class ChatCleanup:
    def __init__(self, directory, authorized):
        self.directory = directory
        self.authorized = authorized
        self.previews = {}
        self.job = None
        self.task = None
        self._token = None
        self._stopped = False

    def preview(self, actor):
        if self._stopped or (self.task and not self.task.done()):
            raise ChatCleanupError("正在退出会话，请等待执行结束")
        snapshot = self.directory.snapshot()
        if snapshot["discovering"] or not self.directory.last_scan:
            raise ChatCleanupError("正在核实会话，请等待列表刷新完成后预览")
        if snapshot["checked"] < snapshot["total"]:
            raise ChatCleanupError("会话核实尚未完成，请稍后刷新列表再预览")
        if time.monotonic() - self.directory.last_scan > 300:
            self.directory.refresh(force=True)
            raise ChatCleanupError("会话列表已过期，请等待刷新完成后重新预览")
        candidates = [row for row in snapshot["chats"] if row["is_admin"] is False]
        token = secrets.token_urlsafe(32)
        now = time.monotonic()
        self.previews = {
            key: value
            for key, value in self.previews.items()
            if value["expires"] > now and value["actor"] != actor
        }
        if len(self.previews) >= 64:
            self.previews.pop(next(iter(self.previews)))
        self.previews[token] = {
            "actor": actor,
            "bot_id": self.directory.bot_id(),
            "expires": now + 300,
            "chats": deepcopy(candidates),
        }
        return {
            "token": token,
            "expires_in_seconds": 300,
            "chats": candidates,
            "total": len(candidates),
            "admin_count": sum(row["is_admin"] is True for row in snapshot["chats"]),
            "unknown_count": sum(
                row["joined"] is None or (row["joined"] is True and row["is_admin"] is None)
                for row in snapshot["metadata"].values()
            ),
        }

    def start(self, token, actor, *, audit=None):
        # 请求重试返回同一任务，不重复执行已经确认的退出操作。
        if token == self._token and self.job and actor == self.job["actor"]:
            return self.status()
        preview = self.previews.get(token)
        if (
            self._stopped
            or not preview
            or preview["actor"] != actor
            or preview["bot_id"] != self.directory.bot_id()
            or preview["expires"] <= time.monotonic()
        ):
            raise ChatCleanupError("预览已失效，请重新预览并确认")
        if self.task and not self.task.done():
            raise ChatCleanupError("已有退出任务正在执行，请等待完成")
        if self.directory.task and not self.directory.task.done():
            raise ChatCleanupError("正在刷新会话，请等待刷新完成后确认")
        self.previews.pop(token)
        self._token = token
        self.job = {
            "actor": actor,
            "status": "running",
            "total": len(preview["chats"]),
            "checked": 0,
            "results": [],
            "error": None,
        }
        self.directory.mutating = True
        self.task = asyncio.create_task(self._run(preview, audit), name="guard-chat-cleanup")
        return self.status()

    def status(self):
        return deepcopy(self.job)

    async def _leave(self, chat_id):
        chat = await self.directory.check(chat_id)
        entry = self.directory.entries[chat_id]
        if entry["joined"] is False:
            return "skipped", "已不在该会话中"
        if entry["is_admin"] is True:
            return "skipped", "机器人当前为管理员，已保留"
        if chat is None or entry["joined"] is not True or entry["is_admin"] is not False:
            return "skipped", "无法确认成员身份或权限，已保留"
        request = (
            DeleteChatUserRequest(chat.id, InputUserSelf())
            if isinstance(chat, Chat)
            else LeaveChannelRequest(chat)
        )
        try:
            await asyncio.wait_for(self.directory.client(request), timeout=15)
        except UserNotParticipantError:
            self.directory.observe(chat_id, left=True)
            return "skipped", "已不在该会话中"
        self.directory.observe(chat_id, left=True)
        return "left", "已退出"

    async def _run(self, preview, audit):
        try:
            for row in preview["chats"]:
                actor = preview["actor"]
                if not self.authorized(actor) or self.directory.bot_id() != preview["bot_id"]:
                    self.job["error"] = "操作者权限或机器人身份已变化，剩余会话未处理"
                    break
                try:
                    status, message = await self._leave(row["chat_id"])
                except FloodWaitError as error:
                    status, message = "failed", f"Telegram 限流，请 {error.seconds} 秒后重新预览"
                    self.job["error"] = message
                except Exception as error:
                    status, message = (
                        "failed",
                        f"退出失败：{type(error).__name__}，请刷新核实后重试",
                    )
                    logger.warning(
                        "Bot 退出会话失败 chat=%s error=%s", row["chat_id"], type(error).__name__
                    )
                result = {**row, "status": status, "message": message}
                self.job["results"].append(result)
                self.job["checked"] += 1
                if audit:
                    await audit(
                        action="guard.chat.leave",
                        actor=str(actor),
                        target=str(row["chat_id"]),
                        success=status == "left",
                        message=message,
                    )
                if self.job["error"]:
                    break
        except asyncio.CancelledError:
            self.job["error"] = "服务已停止，未完成的会话请刷新核实后重新预览"
            raise
        except Exception:
            self.job["error"] = "退出任务中断，请刷新会话核实结果后重新预览"
            logger.exception("Bot 会话退出任务中断")
        finally:
            self.directory.mutating = False
            self.job["status"] = "interrupted" if self.job["error"] else "completed"

    async def stop(self):
        self._stopped = True
        self.previews.clear()
        if self.task and not self.task.done():
            self.task.cancel()
            try:
                await self.task
            except asyncio.CancelledError:
                pass
        self.directory.mutating = False
        if self.job and self.job["status"] == "running":
            self.job.update(status="interrupted", error="服务已停止，请刷新核实后重新预览")
