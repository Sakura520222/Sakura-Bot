"""Main Bot Wiki commands; assistant tools belong exclusively to Go."""

from telethon import events


def register_wiki_commands(client, runtime):
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
