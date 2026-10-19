# Copyright 2026 Sakura-Bot
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
"""只清理管理页无法访问的历史记录；保留 Telegram 会话缓存与全部成员关系。"""

from .chat_cleanup import ChatCleanup, ChatCleanupError


class HistoryCleanup(ChatCleanup):
    operation = "清理历史记录"
    success_status = "cleaned"
    audit_action = "guard.chat.history.clear"

    def _candidates(self, snapshot):
        if not self.directory.history_store or not self.directory.bot_id():
            raise ChatCleanupError("历史记录存储尚未就绪，请稍后重试")
        return sorted(
            (row for row in snapshot["metadata"].values() if self.directory.can_clean_history(row)),
            key=lambda row: (row["title"].casefold(), row["chat_id"]),
        )

    async def _process(self, chat_id):
        directory = self.directory
        entry = directory.entries.get(chat_id, {})
        if not directory.can_clean_history(entry):
            return "skipped", "当前记录已变化或受保护，已保留"
        version = directory.observed_versions.get(chat_id, 0)
        bot_id = directory.bot_id()
        await directory.check(chat_id)
        if (
            not directory.can_clean_history(entry)
            or directory.observed_versions.get(chat_id, 0) != version
        ):
            return "skipped", "已加入、已恢复访问或无法确认不可访问，已保留"
        actor = self.job["actor"]
        if not self.authorized(actor) or directory.bot_id() != bot_id:
            self.job["error"] = "操作者权限或机器人身份已变化，剩余记录未处理"
            return "skipped", "操作者权限或机器人身份已变化，已保留"
        # 独立标记只影响目录展示，从不删除 Session 实体、群管设置、消息或成员关系。
        await directory.history_store.mark_chat_history_cleaned(bot_id, chat_id, actor)
        if (
            directory.observed_versions.get(chat_id, 0) != version
            or not directory.can_clean_history(entry)
            or directory.bot_id() != bot_id
            or not self.authorized(actor)
        ):
            await directory.history_store.restore_chat_history(bot_id, {chat_id})
            return "skipped", "清理期间收到会话更新，已保留"
        directory.cleaned_history.add(chat_id)
        return "cleaned", "已清理历史记录；重新收到活动或确认加入后会自动显示"
