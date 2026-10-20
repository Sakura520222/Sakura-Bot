# Copyright 2026 Sakura-Bot
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
"""申诉消息复用：输入、导航与异步通知共享持久化卡片。"""

import asyncio

from telegram.error import BadRequest

from core.i18n.i18n import t
from core.moderation.appeal_inbox import AppealInbox
from core.moderation.appeal_ui import buttons, card, input_buttons, input_prompt
from core.moderation.moderator import GuardError


async def edit_or_send(bot, chat_id, text, markup, message_id=None, *, fallback=True):
    kwargs = {
        "text": text,
        "parse_mode": "HTML",
        "reply_markup": markup,
        "disable_web_page_preview": True,
    }
    if message_id:
        try:
            await asyncio.wait_for(
                bot.edit_message_text(chat_id=chat_id, message_id=message_id, **kwargs), timeout=25
            )
            return message_id
        except BadRequest as error:
            reason = str(error).lower()
            if "not modified" in reason:
                return message_id
            if not any(
                value in reason
                for value in ("not found", "can't be edited", "no text in the message")
            ):
                raise
    if not fallback:
        return None
    message = await asyncio.wait_for(bot.send_message(chat_id=chat_id, **kwargs), timeout=25)
    return message.message_id


class AppealMessages:
    def __init__(self, repository):
        self.repository = repository

    async def user(self, bot, actor, appeal_id, origin=None):
        async with self.repository.lock(actor) as store:
            appeal = await store.get(appeal_id)
            AppealInbox.own(appeal, actor)
            entry = await store.input(actor)
            editing = (
                entry
                and entry["appeal_id"] == appeal_id
                and entry["mode"] in {"draft", "supplement"}
            )
            if editing:
                AppealInbox.validate_input(entry, appeal)
            notice = next(
                (row for row in await store.notices(appeal_id) if row["target_id"] == actor), None
            )
            message_id = (
                origin[1] if origin and origin[0] == actor else (notice or {}).get("message_id")
            )
            text = input_prompt(entry, appeal) if editing else card(appeal)
            markup = input_buttons(entry) if editing else buttons(appeal)
            message_id = await edit_or_send(bot, actor, text, markup, message_id)
            async with store.atomic():
                await store.bind_user_card(appeal, message_id)
                if editing:
                    await store.input_message(actor, entry["token"], message_id)

    async def input(self, bot, actor, entry, origin=None):
        if entry["mode"] != "review":
            await self.user(bot, actor, entry["appeal_id"], origin)
            return
        initial = await self.repository.get(entry["appeal_id"])
        if not initial:
            raise GuardError("appeal.stale")
        # 不同时持有审核人和申诉人的锁，避免跨群/多角色相互等待。
        async with self.repository.lock(initial["user_id"]) as store:
            current = await store.input(actor)
            if not current or current["token"] != entry["token"]:
                raise GuardError("appeal.stale")
            appeal = await store.get(entry["appeal_id"])
            AppealInbox.validate_input(current, appeal)
            source = current["payload"]["origin"]
            message_id = current["payload"].get("ui_message_id") or (
                source[1] if source[0] == actor else None
            )
            message_id = await edit_or_send(
                bot,
                actor,
                card(appeal, reviewer=True) + "\n\n" + input_prompt(current),
                input_buttons(current),
                message_id,
            )
            await store.input_message(actor, current["token"], message_id)

    async def review(
        self, bot, actor, appeal_id, revision, origin, *, expanded=False, confirm=None
    ):
        initial = await self.repository.get(appeal_id)
        if not initial:
            raise GuardError("appeal.stale")
        async with self.repository.lock(initial["user_id"]) as store:
            appeal = await store.get(appeal_id)
            await AppealInbox.check_origin(store, appeal, actor, revision, origin)
            if (expanded or confirm) and appeal["status"] != "pending":
                raise GuardError("appeal.stale")
            text = card(appeal, reviewer=True)
            if confirm:
                if confirm not in {"allow", "global"}:
                    raise GuardError("guard.invalid_callback")
                text += "\n\n" + t(f"appeal.confirm_{confirm}")
            message_id = await edit_or_send(
                bot,
                origin[0],
                text,
                buttons(appeal, reviewer=True, expanded=expanded, confirm=confirm),
                origin[1],
            )
            await store.save_notice(appeal, origin[0], message_id, "reviewer")

    async def finish_input(self, bot, actor, entry, *, cancelled=False):
        if entry["mode"] != "review":
            await self.user(bot, actor, entry["appeal_id"])
            return
        initial = await self.repository.get(entry["appeal_id"])
        if not initial:
            return
        async with self.repository.lock(initial["user_id"]) as store:
            appeal = await store.get(entry["appeal_id"])
            origin = entry["payload"]["origin"]
            if origin[0] == actor:
                notice = next(
                    (
                        row
                        for row in await store.notices(appeal["id"])
                        if row["kind"] == "reviewer"
                        and (row["target_id"], row["message_id"]) == tuple(origin)
                    ),
                    None,
                )
                if notice is None:
                    await edit_or_send(
                        bot,
                        actor,
                        t("appeal.review_cancelled" if cancelled else "appeal.review_submitted"),
                        None,
                        entry["payload"].get("ui_message_id"),
                        fallback=cancelled,
                    )
                    return
                text = card(appeal, reviewer=True)
                if not cancelled and appeal["status"] == "pending":
                    text += "\n\n" + t("appeal.review_submitted")
                message_id = await edit_or_send(
                    bot, actor, text, buttons(appeal, reviewer=True), origin[1], fallback=cancelled
                )
                if cancelled:
                    await store.save_notice(appeal, actor, message_id, "reviewer")
            else:
                await edit_or_send(
                    bot,
                    actor,
                    t("appeal.review_cancelled" if cancelled else "appeal.review_submitted"),
                    None,
                    entry["payload"].get("ui_message_id"),
                )
