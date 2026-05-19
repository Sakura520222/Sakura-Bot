# Copyright 2026 Sakura-Bot
#
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可

"""
投稿审核处理器 (MainBot 侧)

处理管理员审核投稿的按钮回调，支持 AI 优化、同意、拒绝三个操作。
"""

import base64
import logging
from io import BytesIO
from typing import Any

from telethon import Button

from core.config import ADMIN_LIST
from core.i18n.i18n import get_text
from core.infrastructure.database.submission_repo import get_submission_repo
from core.services.submission_service import get_submission_service

logger = logging.getLogger(__name__)


class SubmissionReviewHandler:
    """主 Bot 投稿审核处理器"""

    def __init__(self):
        self.service = get_submission_service()
        self.repo = get_submission_repo()

    async def check_pending_submissions(self, context=None, telethon_client=None) -> None:
        """定期检查并处理待审核的投稿（由主 Bot 调度器调用）

        Args:
            context: PTB context
            telethon_client: Telethon 客户端
        """
        try:
            pending = await self.repo.get_pending_submissions()
            if not pending:
                return

            logger.info(f"发现 {len(pending)} 个待审核投稿")
            for submission in pending:
                await self._notify_admin(submission, telethon_client)

        except Exception as e:
            logger.error(f"检查待审核投稿失败: {type(e).__name__}: {e}", exc_info=True)

    def _build_review_text(self, submission: dict[str, Any]) -> str:
        """构建审核通知文本（不含媒体信息）"""
        submission_id = submission["id"]
        title = submission["title"]
        submitter_name = submission["submitter_name"]
        anonymous_text = "是" if submission.get("is_anonymous") else "否"
        content = submission.get("content") or "(无)"

        target_channel = submission.get("target_channel") or ""
        channel_text = ""
        if target_channel:
            channel_name = target_channel.rstrip("/").split("/")[-1]
            channel_text = f"\n目标频道: {channel_name}"

        return f"""📝 新投稿审核

投稿ID: {submission_id}
投稿者: {submitter_name}
匿名投稿: {anonymous_text}
标题: {title}
正文: {content[:300]}{"..." if len(content) > 300 else ""}{channel_text}

请审核此投稿："""

    def _decode_media(self, media: dict[str, Any], index: int) -> BytesIO | None:
        """解码数据库中存储的媒体文件为 BytesIO 对象"""
        file_data_b64 = media.get("file_data")
        if not file_data_b64:
            return None

        try:
            file_bytes = base64.b64decode(file_data_b64)
        except Exception as e:
            logger.error(f"媒体文件 base64 解码失败 (index={index}): {type(e).__name__}: {e}")
            return None

        file_buf = BytesIO(file_bytes)
        media_type = media.get("type", "document")
        file_name = media.get("file_name")

        if file_name:
            file_buf.name = file_name
        elif media_type == "photo":
            file_buf.name = "photo.jpg"
        elif media_type == "video":
            ext = self._mime_to_ext(media.get("mime_type", "video/mp4"))
            file_buf.name = f"video{ext}"
        elif media_type == "audio":
            ext = self._mime_to_ext(media.get("mime_type", "audio/mpeg"))
            file_buf.name = f"audio{ext}"
        else:
            file_buf.name = f"file_{index}"

        return file_buf

    async def _notify_admin(self, submission: dict[str, Any], client) -> None:
        """通知管理员有新投稿需要审核（含媒体文件预览）"""
        try:
            submission_id = submission["id"]
            media_files = submission.get("media_files") or []

            # 构建审核文本
            review_text = self._build_review_text(submission)

            # 构建审核按钮
            buttons = self._build_review_buttons(
                submission_id,
                can_force_signature=self._can_force_signature(submission),
            )

            # 通知所有管理员
            sent_msg_ids = []
            for admin_id in ADMIN_LIST:
                try:
                    first_msg_id = await self._send_review_to_admin(
                        client, admin_id, review_text, media_files, buttons
                    )
                    if first_msg_id:
                        sent_msg_ids.append(first_msg_id)
                except Exception as e:
                    logger.error(f"通知管理员失败 admin_id={admin_id}: {e}")

            # 保存审核消息 ID，用于后续更新按钮
            if sent_msg_ids:
                await self.repo.update_submission_status(
                    submission_id=submission_id,
                    status="pending",
                    review_message_id=sent_msg_ids[0],
                )

        except Exception as e:
            logger.error(f"通知管理员投稿失败: {type(e).__name__}: {e}", exc_info=True)

    async def _send_review_to_admin(
        self,
        client,
        admin_id: int,
        review_text: str,
        media_files: list[dict],
        buttons: list,
    ) -> int | None:
        """向单个管理员发送审核通知（含媒体）

        策略：先发送审核文本（带按钮），再发送媒体文件预览

        Returns:
            第一条消息的 ID（即带按钮的审核消息）
        """
        # 先发送带按钮的审核文本
        sent_msg = await client.send_message(admin_id, review_text, buttons=buttons)
        first_msg_id = sent_msg.id

        # 再在下方发送媒体文件预览（作为媒体组）
        if media_files:
            file_bufs = []
            for i, media in enumerate(media_files):
                file_buf = self._decode_media(media, i)
                if file_buf:
                    file_bufs.append(file_buf)

            if file_bufs:
                try:
                    await self._send_media_group(
                        client,
                        admin_id,
                        file_bufs,
                        caption="",
                    )
                except Exception as e:
                    logger.error(f"发送媒体预览给管理员 {admin_id} 失败: {e}")

        return first_msg_id

    async def handle_callback(self, event, client) -> None:
        """处理管理员按钮点击（Telethon 事件）

        Args:
            event: Telethon 回调事件
            client: Telethon 客户端
        """
        try:
            await event.answer()

            data = event.data.decode() if isinstance(event.data, bytes) else event.data
            if not data.startswith("submission_"):
                return

            parts = data.split("_")
            if len(parts) < 3:
                return

            action = parts[1]  # aiopt / approve / signapprove / reject
            submission_id = int(parts[2])

            if action == "aiopt":
                await self._handle_ai_optimize(event, submission_id)
            elif action == "approve":
                await self._handle_approve(event, submission_id, client)
            elif action == "signapprove":
                await self._handle_approve(event, submission_id, client, signature_forced=True)
            elif action == "reject":
                await self._handle_reject(event, submission_id)
            elif action == "restore":
                await self._handle_restore(event, submission_id)

        except Exception as e:
            logger.error(f"处理投稿审核回调查询失败: {type(e).__name__}: {e}", exc_info=True)

    @staticmethod
    def _mime_to_ext(mime_type: str | None) -> str:
        """将 MIME 类型转换为文件扩展名"""
        if not mime_type:
            return ""
        mapping = {
            "image/jpeg": ".jpg",
            "image/png": ".png",
            "image/gif": ".gif",
            "image/webp": ".webp",
            "image/bmp": ".bmp",
            "image/tiff": ".tiff",
            "video/mp4": ".mp4",
            "video/mpeg": ".mpeg",
            "video/webm": ".webm",
            "video/quicktime": ".mov",
            "video/x-msvideo": ".avi",
            "audio/mpeg": ".mp3",
            "audio/ogg": ".ogg",
            "audio/wav": ".wav",
            "audio/flac": ".flac",
            "audio/aac": ".aac",
            "audio/x-m4a": ".m4a",
            "application/pdf": ".pdf",
            "application/zip": ".zip",
            "application/x-rar-compressed": ".rar",
        }
        return mapping.get(mime_type.lower(), "")

    async def _send_media_group(
        self,
        client,
        entity,
        file_bufs: list[BytesIO],
        caption: str,
    ) -> int | None:
        """发送媒体组（album），失败时回退逐个发送

        用于频道发布场景，不需要按钮。

        Args:
            client: Telethon 客户端
            entity: 目标频道
            file_bufs: 媒体 BytesIO 列表
            caption: 第一条消息的 caption

        Returns:
            第一条消息的 ID，失败返回 None
        """
        if not file_bufs:
            return None

        # 单个文件直接发送
        if len(file_bufs) == 1:
            try:
                msg = await client.send_file(
                    entity,
                    file=file_bufs[0],
                    caption=caption,
                    link_preview=False,
                )
                return msg.id
            except Exception as e:
                logger.error(f"发送单个媒体失败: {type(e).__name__}: {e}")
                return None

        # 多个文件：尝试作为媒体组发送
        try:
            messages = await client.send_file(
                entity,
                file=file_bufs,
                caption=caption,
                link_preview=False,
            )
            # send_file 返回列表或单个消息
            if isinstance(messages, list):
                return messages[0].id
            else:
                return messages.id
        except Exception as e:
            logger.warning(f"媒体组发送失败 ({type(e).__name__}: {e})，回退逐个发送")

        # 回退：逐个发送
        first_msg_id = None
        for i, file_buf in enumerate(file_bufs):
            try:
                msg = await client.send_file(
                    entity,
                    file=file_buf,
                    caption=caption if i == 0 else None,
                    link_preview=False,
                )
                if first_msg_id is None:
                    first_msg_id = msg.id
            except Exception as e:
                logger.error(f"逐个发送媒体 {i} 失败: {type(e).__name__}: {e}")
                continue

        return first_msg_id

    @staticmethod
    def _can_force_signature(submission: dict[str, Any] | None) -> bool:
        """判断审核按钮是否允许强制署名。"""
        if not submission:
            return False
        return bool(submission.get("is_anonymous") and submission.get("submitter_name"))

    def _build_review_buttons(
        self,
        submission_id: int,
        show_restore: bool = False,
        can_force_signature: bool = False,
    ) -> list:
        """构建审核按钮

        Args:
            submission_id: 投稿ID
            show_restore: 是否显示恢复原文按钮（AI优化后显示）
            can_force_signature: 是否显示署名通过按钮
        """
        decision_buttons = [
            Button.inline("✅ 同意", data=f"submission_approve_{submission_id}".encode()),
        ]
        if can_force_signature:
            decision_buttons.append(
                Button.inline(
                    get_text("submission.review_approve_signed"),
                    data=f"submission_signapprove_{submission_id}".encode(),
                )
            )
        decision_buttons.append(
            Button.inline("❌ 拒绝", data=f"submission_reject_{submission_id}".encode())
        )

        if show_restore:
            action_buttons = [
                Button.inline("🔄 恢复原文", data=f"submission_restore_{submission_id}".encode())
            ]
        else:
            action_buttons = [
                Button.inline("🤖 AI 优化", data=f"submission_aiopt_{submission_id}".encode())
            ]
        return [decision_buttons, action_buttons]

    async def _handle_ai_optimize(self, event, submission_id: int) -> None:
        """处理 AI 优化按钮"""
        can_force_signature = False
        try:
            submission = await self.repo.get_submission(submission_id)
            can_force_signature = self._can_force_signature(submission)
            await event.edit("🤖 正在使用 AI 优化投稿内容，请稍候...")

            result = await self.service.ai_optimize(submission_id)

            if result["success"]:
                optimized_title = result.get("optimized_title", "")
                optimized = result["optimized_content"]
                title_preview = f"标题：{optimized_title}\n\n" if optimized_title else ""
                content_preview = optimized[:500] + "..." if len(optimized) > 500 else optimized
                buttons = self._build_review_buttons(
                    submission_id,
                    show_restore=True,
                    can_force_signature=can_force_signature,
                )
                await event.edit(
                    f"🤖 AI 优化完成\n\n{title_preview}正文：\n{content_preview}\n\n请使用同意或拒绝按钮继续审核。",
                    buttons=buttons,
                )
            else:
                buttons = self._build_review_buttons(
                    submission_id,
                    can_force_signature=can_force_signature,
                )
                await event.edit(f"❌ AI 优化失败: {result['message']}", buttons=buttons)
        except Exception as e:
            logger.error(f"AI 优化投稿失败: {type(e).__name__}: {e}", exc_info=True)
            try:
                buttons = self._build_review_buttons(
                    submission_id,
                    can_force_signature=can_force_signature,
                )
                await event.edit(f"❌ AI 优化失败: {str(e)}", buttons=buttons)
            except Exception:
                await event.edit(f"❌ AI 优化失败: {str(e)}")

    async def _handle_restore(self, event, submission_id: int) -> None:
        """处理恢复原文按钮"""
        try:
            submission = await self.repo.get_submission(submission_id)
            if not submission:
                await event.edit("❌ 投稿不存在")
                return

            # 清除 AI 优化内容，恢复原始内容
            original_content = submission.get("content") or "(无)"
            preview = (
                original_content[:500] + "..." if len(original_content) > 500 else original_content
            )

            # 清除 DB 中的 AI 优化内容
            await self.repo.update_submission_status(
                submission_id=submission_id,
                status="pending",
                clear_ai_content=True,
            )

            buttons = self._build_review_buttons(
                submission_id,
                show_restore=False,
                can_force_signature=self._can_force_signature(submission),
            )
            await event.edit(
                f"🔄 已恢复原文\n\n{submission['title']}\n\n{preview}\n\n请使用同意或拒绝按钮继续审核。",
                buttons=buttons,
            )
        except Exception as e:
            logger.error(f"恢复原文失败: {type(e).__name__}: {e}", exc_info=True)
            await event.edit(f"❌ 恢复失败: {str(e)}")

    async def _handle_approve(
        self, event, submission_id: int, client, signature_forced: bool = False
    ) -> None:
        """处理同意按钮"""
        try:
            admin_id = event.sender_id
            result = await self.service.approve_submission(
                submission_id,
                reviewed_by=admin_id,
                signature_forced=signature_forced,
            )

            if result["success"]:
                submission = result["submission"]
                # 发布到目标频道
                publish_result = await self._publish_to_channel(submission, client)
                # 通知投稿者
                await self._notify_submitter(
                    submission,
                    "approved",
                    publish_result,
                    signature_forced=bool(submission.get("signature_forced")),
                )
                status_msg = f"✅ 投稿已通过\n\n投稿ID: {submission_id}\n审核人: {admin_id}"
                if submission.get("signature_forced"):
                    status_msg += f"\n{get_text('submission.review_signature_forced')}"
                if publish_result.get("success"):
                    status_msg += f"\n\n📢 已发布到频道: {publish_result.get('channel_name', '')}"
                else:
                    status_msg += f"\n\n⚠️ 发布到频道失败: {publish_result.get('error', '未知错误')}"
                await event.edit(status_msg)
            else:
                await event.edit(f"❌ 操作失败: {result['message']}")
        except Exception as e:
            logger.error(f"同意投稿失败: {type(e).__name__}: {e}", exc_info=True)
            await event.edit(f"❌ 操作失败: {str(e)}")

    async def _handle_reject(self, event, submission_id: int) -> None:
        """处理拒绝按钮"""
        try:
            admin_id = event.sender_id
            result = await self.service.reject_submission(submission_id, reviewed_by=admin_id)

            if result["success"]:
                await event.edit(f"❌ 投稿已拒绝\n\n投稿ID: {submission_id}\n审核人: {admin_id}")
                # 通知投稿者通过 notification_queue
                submission = await self.repo.get_submission(submission_id)
                if submission:
                    await self._notify_submitter(submission, "rejected")
            else:
                await event.edit(f"❌ 操作失败: {result['message']}")
        except Exception as e:
            logger.error(f"拒绝投稿失败: {type(e).__name__}: {e}", exc_info=True)
            await event.edit(f"❌ 操作失败: {str(e)}")

    async def _publish_to_channel(self, submission: dict[str, Any], client) -> dict[str, Any]:
        """将已通过的投稿发布到目标频道

        Args:
            submission: 投稿记录
            client: Telethon 客户端

        Returns:
            {"success": bool, "channel_name": str, "message_url": str|None, "error": str|None}
        """
        try:
            target_channel = submission.get("target_channel")
            if not target_channel:
                logger.warning(f"投稿 {submission['id']} 未指定目标频道，跳过发布")
                return {"success": False, "error": "未指定目标频道"}

            # 构建发布消息（优先使用 AI 优化后的标题和内容）
            caption = self._build_publish_caption(submission)

            media_files = submission.get("media_files") or []
            first_message_id = None

            if media_files:
                # 解码所有媒体文件
                file_bufs = []
                for i, media in enumerate(media_files):
                    file_buf = self._decode_media(media, i)
                    if file_buf:
                        file_bufs.append(file_buf)

                if file_bufs:
                    # 尝试作为媒体组（album）发送
                    first_message_id = await self._send_media_group(
                        client,
                        target_channel,
                        file_bufs,
                        caption,
                    )

                # 如果所有媒体上传失败，回退到纯文本
                if first_message_id is None:
                    msg = await client.send_message(target_channel, caption, link_preview=False)
                    first_message_id = msg.id
            else:
                # 纯文本消息
                msg = await client.send_message(target_channel, caption, link_preview=False)
                first_message_id = msg.id

            channel_name = target_channel.rstrip("/").split("/")[-1]
            logger.info(
                f"投稿 {submission['id']} 已发布到频道 {channel_name}, 消息ID={first_message_id}"
            )

            # 构建消息链接
            message_url = None
            try:
                entity = await client.get_entity(target_channel)
                if hasattr(entity, "username") and entity.username:
                    message_url = f"https://t.me/{entity.username}/{first_message_id}"
            except Exception:
                pass

            return {
                "success": True,
                "channel_name": channel_name,
                "message_url": message_url,
                "message_id": first_message_id,
            }

        except Exception as e:
            logger.error(f"发布投稿到频道失败: {type(e).__name__}: {e}", exc_info=True)
            return {"success": False, "error": str(e)}

    def _build_publish_caption(self, submission: dict[str, Any]) -> str:
        """构建投稿发布文本。"""
        title = submission.get("ai_optimized_title") or submission["title"]
        content = submission.get("ai_optimized_content") or submission.get("content") or ""

        caption = f"**{title}**\n"
        if content:
            caption += f"\n{content}\n"
        caption += f"\n{self._format_submitter_signature(submission)}"
        return caption

    @staticmethod
    def _format_submitter_signature(submission: dict[str, Any]) -> str:
        """格式化投稿者署名。"""
        is_anonymous = bool(submission.get("is_anonymous"))
        signature_forced = bool(submission.get("signature_forced"))
        submitter_name = submission.get("submitter_name")

        if signature_forced and not submitter_name:
            logger.warning(f"投稿 {submission.get('id')} 已强制署名但投稿者名称为空")

        if (is_anonymous and not signature_forced) or not submitter_name:
            return "—— 投稿者: 匿名"
        return f"—— 投稿者: @{submitter_name}"

    async def _notify_submitter(
        self,
        submission: dict[str, Any],
        status: str,
        publish_result=None,
        signature_forced: bool = False,
    ) -> None:
        """通过 notification_queue 通知投稿者审核结果

        注意：已移除历史遗留的未使用 client 参数。通知统一通过数据库
        notification_queue 实现，无需直接调用 Telegram client 发送消息。
        """
        try:
            from core.infrastructure.database.manager import get_db_manager

            db = get_db_manager()
            status_text = "通过" if status == "approved" else "拒绝"
            extra_msg = ""
            signature_notice = ""
            if status == "approved" and signature_forced:
                signature_notice = get_text("submission.signature_forced_notice")
            if status == "approved" and publish_result and publish_result.get("success"):
                channel_name = publish_result.get("channel_name", "")
                message_url = publish_result.get("message_url")
                extra_msg = f"\n已发布到频道: {channel_name}"
                if message_url:
                    extra_msg += f"\n查看: {message_url}"

            await db.create_notification(
                user_id=submission["submitter_id"],
                notification_type=f"submission_{status}",
                content={
                    "submission_id": submission["id"],
                    "title": submission["title"],
                    "message": (
                        f"您的投稿「{submission['title']}」已被管理员{status_text}。"
                        f"{signature_notice}{extra_msg}"
                    ),
                },
            )
            logger.info(f"已通知投稿者 {submission['submitter_id']} 审核结果: {status}")
        except Exception as e:
            logger.error(f"通知投稿者失败: {type(e).__name__}: {e}", exc_info=True)


# 全局实例
_submission_review_handler: SubmissionReviewHandler | None = None


def get_submission_review_handler() -> SubmissionReviewHandler:
    """获取全局投稿审核处理器实例"""
    global _submission_review_handler
    if _submission_review_handler is None:
        _submission_review_handler = SubmissionReviewHandler()
    return _submission_review_handler
