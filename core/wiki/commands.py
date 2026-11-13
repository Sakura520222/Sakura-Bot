# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot

"""Main Bot Wiki commands; assistant tools belong exclusively to Go."""

import asyncio
import logging

from telethon import Button, events

logger = logging.getLogger(__name__)


async def notify_proposal(runtime, identifier, channel, kind):
    from .telegram_login import wiki_login_settings

    if not runtime.bot_client:
        return
    base = wiki_login_settings().origin.rstrip("/")
    buttons = [
        Button.inline("批准", f"wpr:a:{identifier}"),
        Button.inline("拒绝", f"wpr:r:{identifier}"),
    ]
    if base.startswith("https://"):
        buttons.append(Button.url("查看详情", base + "/wiki/admin"))
    from core.config import ADMIN_LIST

    for admin in (value for value in ADMIN_LIST if isinstance(value, int) and value > 0):
        try:
            await asyncio.wait_for(
                runtime.bot_client.send_message(
                    admin,
                    f"Wiki 待审投稿\n频道 {channel} · {kind}\n{identifier}",
                    buttons=buttons,
                    parse_mode=None,
                ),
                timeout=4,
            )
        except Exception as error:
            logger.info("Wiki 投稿通知发送失败: %s", type(error).__name__)


def register_wiki_commands(client, runtime):
    @client.on(events.CallbackQuery(pattern=rb"^wpr:([ar]):([a-f0-9]{32})$"))
    async def review_proposal(event):
        from core.infrastructure.utils.auth import is_admin

        if not is_admin(event.sender_id):
            await event.answer("仅管理员可审核", alert=True)
            return
        action, identifier = (part.decode("ascii") for part in event.pattern_match.groups())
        try:
            await runtime.community.decide(
                identifier,
                "approved" if action == "a" else "rejected",
                event.sender_id,
                telegram=True,
            )
        except ValueError as error:
            await event.answer(str(error), alert=True)
            return
        runtime.wake()
        await event.answer("投稿已处理")

    @client.on(events.NewMessage(pattern=r"^/wiki_(status|build|pause)(?:@\w+)?(?:\s+(\S+))?\s*$"))
    async def handle(event):
        import core.config as config_module

        action, channel_key = event.pattern_match.groups()
        admin = str(event.sender_id) in {str(uid) for uid in config_module.ADMIN_LIST}
        if not channel_key:
            await event.reply(
                "用法：/wiki_status 频道；管理员可用 /wiki_build 频道、/wiki_pause 频道"
            )
            return
        if action == "status" and not admin:
            channel = await runtime.reader.channel(channel_key)
            await event.reply(
                f"Sakura Wiki：{channel['title']}，已发布 {channel['page_count']} 个条目"
                if channel
                else "该频道暂无公开 Wiki。",
                parse_mode=None,
            )
            return
        if not admin:
            await event.reply("此命令仅限管理员。")
            return
        channel = await runtime.store.channel(channel_key)
        if not channel:
            await event.reply("频道未完成身份核实，请检查 WebUI / UserBot。")
            return
        try:
            if action == "build":
                job = await runtime.store.enqueue(channel["id"], "backfill", event.sender_id)
                await event.reply(
                    f"Wiki 后台任务已排队：{job}。可在 WebUI 查看进度。", parse_mode=None
                )
            elif action == "pause":
                config = channel["config"].model_copy(update={"paused": True})
                await runtime.store.configure(channel["id"], config, event.sender_id)
                if channel["active_job"]:
                    await runtime.store.control_job(
                        channel["id"], channel["active_job"], "pause", event.sender_id
                    )
                await event.reply("Wiki 更新已暂停，可在 WebUI 恢复。")
            else:
                await event.reply(
                    f"Wiki：启用={channel['enabled']}，公开={channel['public']}，覆盖={channel['coverage'].get('status')}，任务={channel['active_job'] or '无'}",
                    parse_mode=None,
                )
        except ValueError as error:
            await event.reply(str(error), parse_mode=None)
