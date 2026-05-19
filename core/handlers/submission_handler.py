# Copyright 2026 Sakura-Bot
#
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可

"""
投稿状态机处理器 (QABot 侧)

使用 python-telegram-bot 的 ConversationHandler 实现多步投稿流程。
"""

import base64
import logging
import warnings
from io import BytesIO
from typing import Any

from telegram import InlineKeyboardButton, InlineKeyboardMarkup, Update
from telegram.ext import (
    CallbackQueryHandler,
    CommandHandler,
    ContextTypes,
    ConversationHandler,
    MessageHandler,
    filters,
)
from telegram.warnings import PTBUserWarning

from core.i18n.i18n import get_text
from core.infrastructure.database.submission_repo import get_submission_repo
from core.services.submission_service import get_submission_service
from core.telegram.keyboards import (
    QA_MENU_SUBMIT,
    SUBMIT_MENU_ANONYMOUS_NO,
    SUBMIT_MENU_ANONYMOUS_YES,
    SUBMIT_MENU_CANCEL,
    SUBMIT_MENU_CONFIRM,
    SUBMIT_MENU_DONE_MEDIA,
    SUBMIT_MENU_SKIP_CONTENT,
    SUBMIT_MENU_SKIP_MEDIA,
    build_qa_main_menu_keyboard,
    build_submission_anonymous_keyboard,
    build_submission_confirm_keyboard,
    build_submission_content_keyboard,
    build_submission_media_keyboard,
    build_submission_title_keyboard,
)

logger = logging.getLogger(__name__)

# 投稿状态常量
WAITING_TITLE, WAITING_CONTENT, WAITING_ANONYMOUS, WAITING_MEDIA, SELECT_CHANNEL, CONFIRM = range(6)

# 媒体文件大小限制 (20MB)
MAX_MEDIA_SIZE = 20 * 1024 * 1024


class SubmissionHandler:
    """QABot 投稿流程处理器"""

    def __init__(self):
        self.service = get_submission_service()
        self.repo = get_submission_repo()
        self._user_states: dict[int, dict[str, Any]] = {}  # 用户投稿会话数据

    def get_user_state(self, user_id: int) -> dict[str, Any] | None:
        """获取用户的投稿会话状态"""
        return self._user_states.get(user_id)

    def clear_user_state(self, user_id: int) -> None:
        """清除用户的投稿会话状态"""
        self._user_states.pop(user_id, None)

    async def start_submission(
        self, update: Update, context: ContextTypes.DEFAULT_TYPE, from_deep_link: bool = False
    ) -> int:
        """处理 /submit 命令，开始投稿流程"""
        user_id = update.effective_user.id
        user_name = (
            update.effective_user.username or update.effective_user.first_name or str(user_id)
        )

        # 检查是否有进行中的投稿
        if user_id in self._user_states:
            self.clear_user_state(user_id)
            logger.info(f"用户 {user_name} 重新开始投稿流程，已清理旧状态")

        # 初始化投稿会话
        self._user_states[user_id] = {
            "title": None,
            "content": None,
            "is_anonymous": False,
            "media_files": [],
        }

        message = """📝 投稿流程

请发送您的投稿标题（必填）。

💡 提示：
• 发送 /cancel_submit 可随时取消投稿
• 标题将作为您投稿的摘要展示"""
        await update.message.reply_text(message, reply_markup=build_submission_title_keyboard())
        logger.info(f"用户 {user_name} 开始投稿流程")
        return WAITING_TITLE

    async def receive_title(self, update: Update, context: ContextTypes.DEFAULT_TYPE) -> int:
        """接收投稿标题"""
        user_id = update.effective_user.id
        state = self._user_states.get(user_id)
        if not state:
            self.clear_user_state(user_id)
            await update.message.reply_text(
                get_text("submission.session_expired"),
                reply_markup=build_qa_main_menu_keyboard(),
            )
            return ConversationHandler.END

        title = update.message.text.strip()
        if len(title) > 500:
            await update.message.reply_text(
                "❌ 标题不能超过 500 个字符，请重新输入。",
                reply_markup=build_submission_title_keyboard(),
            )
            return WAITING_TITLE

        state["title"] = title

        message = f"""📝 标题已记录：{title}

现在请发送投稿正文（可选），或使用以下命令：
• /skip — 跳过正文，直接进入下一步
• /cancel_submit — 取消投稿"""
        await update.message.reply_text(message, reply_markup=build_submission_content_keyboard())
        return WAITING_CONTENT

    async def receive_content(self, update: Update, context: ContextTypes.DEFAULT_TYPE) -> int:
        """接收投稿正文"""
        user_id = update.effective_user.id
        state = self._user_states.get(user_id)
        if not state:
            self.clear_user_state(user_id)
            await update.message.reply_text(
                get_text("submission.session_expired"),
                reply_markup=build_qa_main_menu_keyboard(),
            )
            return ConversationHandler.END

        state["content"] = update.message.text.strip()

        return await self._ask_anonymous(update, "📝 正文已记录。")

    async def skip_content(self, update: Update, context: ContextTypes.DEFAULT_TYPE) -> int:
        """跳过正文输入"""
        user_id = update.effective_user.id
        state = self._user_states.get(user_id)
        if not state:
            self.clear_user_state(user_id)
            await update.message.reply_text(
                get_text("submission.session_expired"),
                reply_markup=build_qa_main_menu_keyboard(),
            )
            return ConversationHandler.END

        return await self._ask_anonymous(update, "📝 已跳过正文。")

    async def _ask_anonymous(self, update: Update, prefix: str) -> int:
        """询问投稿者是否匿名。"""
        message = f"""{prefix}

请选择是否匿名投稿：
• 匿名投稿 — 发布到频道时隐藏您的用户名
• 署名投稿 — 发布到频道时显示您的用户名
• /cancel_submit — 取消投稿"""
        await update.message.reply_text(message, reply_markup=build_submission_anonymous_keyboard())
        return WAITING_ANONYMOUS

    async def choose_anonymous(self, update: Update, context: ContextTypes.DEFAULT_TYPE) -> int:
        """处理是否匿名投稿选择。"""
        if not update.message or not update.message.text:
            return WAITING_ANONYMOUS

        user_id = update.effective_user.id
        state = self._user_states.get(user_id)
        if not state:
            self.clear_user_state(user_id)
            await update.message.reply_text(
                get_text("submission.session_expired"),
                reply_markup=build_qa_main_menu_keyboard(),
            )
            return ConversationHandler.END

        text = update.message.text.strip()
        state["is_anonymous"] = text == SUBMIT_MENU_ANONYMOUS_YES
        anonymous_text = "匿名投稿" if state["is_anonymous"] else "署名投稿"

        message = f"""✅ 已选择：{anonymous_text}

现在请发送媒体文件（图片、视频、文件等），或使用以下命令：
• /skip — 跳过媒体文件
• /done — 完成投稿，进入确认
• /cancel_submit — 取消投稿"""
        await update.message.reply_text(message, reply_markup=build_submission_media_keyboard())
        return WAITING_MEDIA

    async def receive_media(self, update: Update, context: ContextTypes.DEFAULT_TYPE) -> int:
        """接收媒体文件"""
        user_id = update.effective_user.id
        state = self._user_states.get(user_id)
        if not state:
            self.clear_user_state(user_id)
            await update.message.reply_text(
                get_text("submission.session_expired"),
                reply_markup=build_qa_main_menu_keyboard(),
            )
            return ConversationHandler.END

        media_info = self._extract_media_info(update.message)
        if not media_info:
            await update.message.reply_text(
                "❌ 无法识别该媒体类型，请发送图片、视频或文件。\n或使用 /done 完成投稿。",
                reply_markup=build_submission_media_keyboard(),
            )
            return WAITING_MEDIA

        # 检查文件大小
        file_size = self._get_file_size(update.message)
        if file_size and file_size > MAX_MEDIA_SIZE:
            await update.message.reply_text(
                f"❌ 文件过大（{file_size / 1024 / 1024:.1f}MB），最大支持 20MB。\n请发送更小的文件。",
                reply_markup=build_submission_media_keyboard(),
            )
            return WAITING_MEDIA

        # 下载文件到内存
        try:
            file_bytes = await self._download_media_to_bytes(update.message, context)
            if file_bytes:
                media_info["file_data"] = base64.b64encode(file_bytes).decode("utf-8")
                media_info["file_size"] = len(file_bytes)
                state["media_files"].append(media_info)
                count = len(state["media_files"])
                await update.message.reply_text(
                    f"✅ 已接收第 {count} 个媒体文件。\n\n"
                    "继续发送更多文件，或使用以下命令：\n"
                    "• /done — 完成投稿\n"
                    "• /skip — 跳过（如果没有更多媒体）\n"
                    "• /cancel_submit — 取消投稿",
                    reply_markup=build_submission_media_keyboard(),
                )
            else:
                await update.message.reply_text(
                    "❌ 文件下载失败，请重新发送或跳过。",
                    reply_markup=build_submission_media_keyboard(),
                )
        except Exception as e:
            logger.error(f"下载媒体文件失败: {type(e).__name__}: {e}")
            await update.message.reply_text(
                f"❌ 文件下载失败: {str(e)[:100]}\n请重新发送或使用 /skip 跳过。",
                reply_markup=build_submission_media_keyboard(),
            )

        return WAITING_MEDIA

    async def skip_media(self, update: Update, context: ContextTypes.DEFAULT_TYPE) -> int:
        """跳过媒体输入"""
        return await self._go_to_confirm(update)

    async def done_media(self, update: Update, context: ContextTypes.DEFAULT_TYPE) -> int:
        """完成媒体输入"""
        return await self._go_to_confirm(update)

    def _get_channels(self) -> list[str]:
        """获取可用频道列表"""
        import core.config as config_module

        return config_module.CHANNELS if config_module.CHANNELS else []

    async def _go_to_confirm(self, update: Update) -> int:
        """进入频道选择阶段（媒体完成后先选频道）"""
        user_id = update.effective_user.id
        state = self._user_states.get(user_id)
        if not state:
            self.clear_user_state(user_id)
            await update.message.reply_text(
                get_text("submission.session_expired"),
                reply_markup=build_qa_main_menu_keyboard(),
            )
            return ConversationHandler.END

        channels = self._get_channels()
        if not channels:
            # 没有配置频道，直接进入确认
            return await self._show_confirm(update)

        # 构建频道选择按钮
        keyboard = []
        for i, ch in enumerate(channels):
            # 提取频道名称（从URL中提取）
            channel_name = ch.rstrip("/").split("/")[-1]
            keyboard.append([InlineKeyboardButton(f"📢 {channel_name}", callback_data=f"ch_{i}")])

        reply_markup = InlineKeyboardMarkup(keyboard)
        await update.message.reply_text("请选择投稿目标频道：", reply_markup=reply_markup)
        return SELECT_CHANNEL

    async def select_channel(self, update: Update, context: ContextTypes.DEFAULT_TYPE) -> int:
        """处理频道选择回调"""
        query = update.callback_query
        await query.answer()

        user_id = query.from_user.id
        state = self._user_states.get(user_id)
        if not state:
            self.clear_user_state(user_id)
            await query.edit_message_text(get_text("submission.session_expired"))
            return ConversationHandler.END

        data = query.data
        if data and data.startswith("ch_"):
            channel_idx = int(data[3:])
            channels = self._get_channels()
            if 0 <= channel_idx < len(channels):
                state["target_channel"] = channels[channel_idx]
                channel_name = channels[channel_idx].rstrip("/").split("/")[-1]
                await query.edit_message_text(f"✅ 已选择频道: {channel_name}")
                # 进入确认
                return await self._show_confirm_text(query)
            else:
                self.clear_user_state(user_id)
                await query.edit_message_text("❌ 频道选择无效，请重新发送 /submit。")
                return ConversationHandler.END

        return SELECT_CHANNEL

    async def _show_confirm_text(self, query) -> int:
        """通过 callback query 显示确认信息"""
        user_id = query.from_user.id
        state = self._user_states.get(user_id)
        if not state:
            self.clear_user_state(user_id)
            await query.message.reply_text(
                get_text("submission.session_expired"),
                reply_markup=build_qa_main_menu_keyboard(),
            )
            return ConversationHandler.END

        title = state["title"] or "(未设置)"
        content = state["content"] or "(无)"
        media_count = len(state["media_files"])
        target_channel = state.get("target_channel", "")
        channel_name = target_channel.rstrip("/").split("/")[-1] if target_channel else "(未选择)"
        anonymous_text = "是" if state.get("is_anonymous") else "否"

        preview = (
            f"📝 投稿预览确认\n\n"
            f"标题：{title}\n"
            f"正文：{content[:200]}{'...' if len(content) > 200 else ''}\n"
            f"媒体文件：{media_count} 个\n"
            f"匿名投稿：{anonymous_text}\n"
            f"目标频道：{channel_name}\n\n"
            f"请确认是否提交：\n"
            f"• /confirm — 确认提交\n"
            f"• /cancel_submit — 取消投稿"
        )
        await query.message.reply_text(preview, reply_markup=build_submission_confirm_keyboard())
        return CONFIRM

    async def _show_confirm(self, update: Update) -> int:
        """通过 update message 显示确认信息（无频道选择时使用）"""
        user_id = update.effective_user.id
        state = self._user_states.get(user_id)
        if not state:
            self.clear_user_state(user_id)
            await update.message.reply_text(
                get_text("submission.session_expired"),
                reply_markup=build_qa_main_menu_keyboard(),
            )
            return ConversationHandler.END

        title = state["title"] or "(未设置)"
        content = state["content"] or "(无)"
        media_count = len(state["media_files"])
        target_channel = state.get("target_channel", "")
        channel_name = target_channel.rstrip("/").split("/")[-1] if target_channel else "(未选择)"
        anonymous_text = "是" if state.get("is_anonymous") else "否"

        preview = (
            f"📝 投稿预览确认\n\n"
            f"标题：{title}\n"
            f"正文：{content[:200]}{'...' if len(content) > 200 else ''}\n"
            f"媒体文件：{media_count} 个\n"
            f"匿名投稿：{anonymous_text}\n"
            f"目标频道：{channel_name}\n\n"
            f"请确认是否提交：\n"
            f"• /confirm — 确认提交\n"
            f"• /cancel_submit — 取消投稿"
        )
        await update.message.reply_text(preview, reply_markup=build_submission_confirm_keyboard())
        return CONFIRM

    async def confirm_submission(self, update: Update, context: ContextTypes.DEFAULT_TYPE) -> int:
        """确认并提交投稿"""
        user_id = update.effective_user.id
        state = self._user_states.get(user_id)
        if not state:
            self.clear_user_state(user_id)
            await update.message.reply_text(
                get_text("submission.session_expired"),
                reply_markup=build_qa_main_menu_keyboard(),
            )
            return ConversationHandler.END

        user_name = (
            update.effective_user.username or update.effective_user.first_name or str(user_id)
        )

        # 提交投稿
        result = await self.service.submit(
            submitter_id=user_id,
            submitter_name=user_name,
            title=state["title"],
            content=state["content"],
            media_files=state["media_files"] if state["media_files"] else None,
            target_channel=state.get("target_channel"),
            is_anonymous=bool(state.get("is_anonymous")),
        )

        if result["success"]:
            # MainBot 的定时任务会自动检测 pending 投稿并通知管理员
            await update.message.reply_text(
                f"✅ 投稿已成功提交，请等待管理员审核。\n\n您的投稿ID: {result['submission_id']}",
                reply_markup=build_qa_main_menu_keyboard(),
            )
            logger.info(f"用户 {user_name} 投稿提交成功: ID={result['submission_id']}")
        else:
            await update.message.reply_text(
                f"❌ {result['message']}",
                reply_markup=build_qa_main_menu_keyboard(),
            )

        self.clear_user_state(user_id)
        return ConversationHandler.END

    async def cancel_submission(self, update: Update, context: ContextTypes.DEFAULT_TYPE) -> int:
        """取消投稿"""
        user_id = update.effective_user.id
        self.clear_user_state(user_id)
        await update.message.reply_text(
            "✅ 投稿已取消。",
            reply_markup=build_qa_main_menu_keyboard(),
        )
        logger.info(f"用户 {update.effective_user.username or user_id} 取消了投稿")
        return ConversationHandler.END

    def _extract_media_info(self, message) -> dict[str, Any] | None:
        """从消息中提取媒体信息"""
        if message.photo:
            # 取最大尺寸的照片
            photo = message.photo[-1]
            return {
                "file_id": photo.file_id,
                "type": "photo",
                "file_unique_id": photo.file_unique_id,
            }
        elif message.video:
            return {
                "file_id": message.video.file_id,
                "type": "video",
                "file_unique_id": message.video.file_unique_id,
                "mime_type": message.video.mime_type,
                "file_name": message.video.file_name,
            }
        elif message.document:
            return {
                "file_id": message.document.file_id,
                "type": "document",
                "file_unique_id": message.document.file_unique_id,
                "file_name": message.document.file_name,
                "mime_type": message.document.mime_type,
            }
        elif message.audio:
            return {
                "file_id": message.audio.file_id,
                "type": "audio",
                "file_unique_id": message.audio.file_unique_id,
                "file_name": message.audio.file_name,
                "mime_type": message.audio.mime_type,
            }
        return None

    def _get_file_size(self, message) -> int | None:
        """获取消息中媒体文件的大小"""
        if message.photo:
            return message.photo[-1].file_size
        elif message.video:
            return message.video.file_size
        elif message.document:
            return message.document.file_size
        elif message.audio:
            return message.audio.file_size
        return None

    async def _download_media_to_bytes(
        self, message, context: ContextTypes.DEFAULT_TYPE
    ) -> bytes | None:
        """下载媒体文件到内存

        Args:
            message: Telegram 消息对象
            context: Bot 上下文

        Returns:
            文件字节数据，失败返回 None
        """
        try:
            # 获取文件对象
            if message.photo:
                file_id = message.photo[-1].file_id
            elif message.video:
                file_id = message.video.file_id
            elif message.document:
                file_id = message.document.file_id
            elif message.audio:
                file_id = message.audio.file_id
            else:
                return None

            file = await context.bot.get_file(file_id)
            buf = BytesIO()
            await file.download_to_memory(buf)
            return buf.getvalue()

        except Exception as e:
            logger.error(f"下载媒体文件到内存失败: {type(e).__name__}: {e}")
            return None

    async def start_submission_deep_link(
        self, update: Update, context: ContextTypes.DEFAULT_TYPE
    ) -> int:
        """处理深链接 /start submit，启动投稿流程"""
        if context.args and context.args[0] == "submit":
            # 深链接入口，先注册用户
            from core.qa_user_system import get_qa_user_system

            user_system = get_qa_user_system()
            if user_system:
                await user_system.register_user(
                    update.effective_user.id,
                    update.effective_user.username,
                    update.effective_user.first_name,
                )
            return await self.start_submission(update, context, from_deep_link=True)
        return ConversationHandler.END

    def build_conversation_handler(self) -> ConversationHandler:
        """构建 ConversationHandler 实例"""
        with warnings.catch_warnings():
            warnings.filterwarnings(
                "ignore",
                message="If 'per_message=False', 'CallbackQueryHandler' will not be tracked.*",
                category=PTBUserWarning,
            )
            return ConversationHandler(
                entry_points=[
                    CommandHandler("submit", self.start_submission),
                    CommandHandler("start", self.start_submission_deep_link),
                    MessageHandler(filters.Regex(f"^{QA_MENU_SUBMIT}$"), self.start_submission),
                ],
                states={
                    WAITING_TITLE: [
                        MessageHandler(
                            filters.Regex(f"^{SUBMIT_MENU_CANCEL}$"), self.cancel_submission
                        ),
                        MessageHandler(filters.TEXT & ~filters.COMMAND, self.receive_title),
                        CommandHandler("cancel_submit", self.cancel_submission),
                    ],
                    WAITING_CONTENT: [
                        MessageHandler(
                            filters.Regex(f"^{SUBMIT_MENU_SKIP_CONTENT}$"), self.skip_content
                        ),
                        MessageHandler(
                            filters.Regex(f"^{SUBMIT_MENU_CANCEL}$"), self.cancel_submission
                        ),
                        MessageHandler(filters.TEXT & ~filters.COMMAND, self.receive_content),
                        CommandHandler("skip", self.skip_content),
                        CommandHandler("cancel_submit", self.cancel_submission),
                    ],
                    WAITING_ANONYMOUS: [
                        MessageHandler(
                            filters.Regex(
                                f"^({SUBMIT_MENU_ANONYMOUS_YES}|{SUBMIT_MENU_ANONYMOUS_NO})$"
                            ),
                            self.choose_anonymous,
                        ),
                        MessageHandler(
                            filters.Regex(f"^{SUBMIT_MENU_CANCEL}$"), self.cancel_submission
                        ),
                        CommandHandler("cancel_submit", self.cancel_submission),
                    ],
                    WAITING_MEDIA: [
                        MessageHandler(
                            filters.Regex(f"^{SUBMIT_MENU_SKIP_MEDIA}$"), self.skip_media
                        ),
                        MessageHandler(
                            filters.Regex(f"^{SUBMIT_MENU_DONE_MEDIA}$"), self.done_media
                        ),
                        MessageHandler(
                            filters.Regex(f"^{SUBMIT_MENU_CANCEL}$"), self.cancel_submission
                        ),
                        MessageHandler(
                            filters.PHOTO | filters.VIDEO | filters.Document.ALL | filters.AUDIO,
                            self.receive_media,
                        ),
                        CommandHandler("skip", self.skip_media),
                        CommandHandler("done", self.done_media),
                        CommandHandler("cancel_submit", self.cancel_submission),
                    ],
                    SELECT_CHANNEL: [
                        CallbackQueryHandler(self.select_channel, pattern=r"^ch_\d+$"),
                        MessageHandler(
                            filters.Regex(f"^{SUBMIT_MENU_CANCEL}$"), self.cancel_submission
                        ),
                        CommandHandler("cancel_submit", self.cancel_submission),
                    ],
                    CONFIRM: [
                        MessageHandler(
                            filters.Regex(f"^{SUBMIT_MENU_CONFIRM}$"), self.confirm_submission
                        ),
                        MessageHandler(
                            filters.Regex(f"^{SUBMIT_MENU_CANCEL}$"), self.cancel_submission
                        ),
                        CommandHandler("confirm", self.confirm_submission),
                        CommandHandler("cancel_submit", self.cancel_submission),
                    ],
                },
                fallbacks=[
                    CommandHandler("cancel_submit", self.cancel_submission),
                    MessageHandler(
                        filters.Regex(f"^{SUBMIT_MENU_CANCEL}$"), self.cancel_submission
                    ),
                ],
                # 投稿流程同时使用文本/命令消息和频道选择按钮；必须保持 per_message=False。
                # PTB 会对混用 CallbackQueryHandler 给出提示，但本流程按用户维度跟踪即可。
                per_message=False,
            )


# 全局实例
_submission_handler: SubmissionHandler | None = None


def get_submission_handler() -> SubmissionHandler:
    """获取全局投稿处理器实例"""
    global _submission_handler
    if _submission_handler is None:
        _submission_handler = SubmissionHandler()
    return _submission_handler
