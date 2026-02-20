# -*- coding: utf-8 -*-
# Copyright 2026 Sakura-Bot
#
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可，
# 并附加非商业使用限制条款。
#
# - 署名：必须提供本项目的原始来源链接
# - 非商业：禁止任何商业用途和分发
# - 相同方式共享：衍生作品必须采用相同的许可证
#
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
# 许可证全文：参见 LICENSE 文件

"""
主Bot请求处理器
处理来自问答Bot的总结请求
"""

import logging
from typing import Any, Dict

from telegram import InlineKeyboardButton, InlineKeyboardMarkup
from telegram.ext import ContextTypes
from telethon import Button

from core.command_handlers.summary_commands import generate_channel_summary
from core.config import ADMIN_LIST
from core.database import get_db_manager

logger = logging.getLogger(__name__)


class MainBotRequestHandler:
    """主Bot请求处理器"""

    def __init__(self):
        """初始化请求处理器"""
        self.db = get_db_manager()
        self.pending_requests_cache = {}  # 缓存pending请求
        logger.info("主Bot请求处理器初始化完成")

    async def check_requests(self, context: ContextTypes.DEFAULT_TYPE = None,
                            telethon_client=None) -> None:
        """
        定期检查并处理新的总结请求

        Args:
            context: Telegram Bot上下文（可选，用于 PTB）
            telethon_client: Telethon 客户端实例（可选，用于 Telethon）
        """
        try:
            # 获取所有pending状态的请求
            pending_requests = self.db.get_pending_requests()

            if not pending_requests:
                return

            logger.info(f"发现 {len(pending_requests)} 个待处理请求")

            for request in pending_requests:
                # 优先使用 Telethon 客户端
                if telethon_client:
                    await self._notify_admin_with_telethon(request, telethon_client)
                else:
                    await self._notify_admin_request(request, context)

        except Exception as e:
            logger.error(f"检查请求失败: {type(e).__name__}: {e}", exc_info=True)

    def _build_admin_message(self, request: Dict[str, Any]) -> str:
        """
        构建管理员通知消息

        Args:
            request: 请求信息字典

        Returns:
            str: 格式化的通知消息
        """
        request_id = request.get('id')
        channel_id = request.get('target_channel')
        requested_by = request.get('requested_by')
        created_at = request.get('created_at', '')

        # 获取请求者信息
        user_info = self.db.get_user_info(requested_by)
        if not user_info:
            user_name = f"用户_{requested_by}"
        else:
            user_name = user_info.get('username') or user_info.get('first_name', f"用户_{requested_by}")

        # 构建通知消息
        message = f"""📝 **新的总结请求**

**请求ID**: {request_id}
**频道**: {channel_id}
**请求者**: {user_name} (ID: {requested_by})
**时间**: {created_at}

请确认是否为该频道生成总结？"""

        return message

    async def _notify_admin_request(self, request: Dict[str, Any],
                                    context: ContextTypes.DEFAULT_TYPE = None) -> None:
        """
        通知管理员有新的总结请求（使用 PTB context）

        Args:
            request: 请求信息字典
            context: Telegram Bot上下文
        """
        try:
            request_id = request.get('id')
            request.get('target_channel')
            # 更新状态为processing
            self.db.update_request_status(request_id, 'processing')

            # 构建消息
            message = self._build_admin_message(request)

            # 创建确认按钮
            keyboard = [
                [
                    InlineKeyboardButton("✅ 确认生成", callback_data=f"confirm_summary_{request_id}"),
                    InlineKeyboardButton("❌ 拒绝", callback_data=f"reject_summary_{request_id}")
                ]
            ]
            reply_markup = InlineKeyboardMarkup(keyboard)

            # 通知所有管理员
            for admin_id in ADMIN_LIST:
                if context:
                    try:
                        await context.bot.send_message(
                            chat_id=admin_id,
                            text=message,
                            parse_mode='Markdown',
                            reply_markup=reply_markup
                        )
                    except Exception as e:
                        logger.error(f"通知管理员失败 admin_id={admin_id}: {e}")

        except Exception as e:
            logger.error(f"通知管理员失败: {type(e).__name__}: {e}", exc_info=True)
            # 恢复请求状态
            self.db.update_request_status(request['id'], 'pending')

    async def _notify_admin_with_telethon(self, request: Dict[str, Any],
                                         client) -> None:
        """
        使用 Telethon 客户端通知管理员有新的总结请求

        Args:
            request: 请求信息字典
            client: Telethon 客户端实例
        """
        try:
            request_id = request.get('id')
            request.get('target_channel')

            # 更新状态为processing
            self.db.update_request_status(request_id, 'processing')

            # 构建消息
            message = self._build_admin_message(request)

            # 构建 Telethon 风格的按钮
            buttons = [
                [
                    Button.inline("✅ 确认生成", data=f"confirm_summary_{request_id}".encode()),
                    Button.inline("❌ 拒绝", data=f"reject_summary_{request_id}".encode())
                ]
            ]

            # 通知所有管理员
            for admin_id in ADMIN_LIST:
                try:
                    await client.send_message(
                        admin_id,
                        message,
                        parse_mode='markdown',
                        buttons=buttons
                    )
                    logger.info(f"已通过 Telethon 向管理员 {admin_id} 发送总结请求通知")
                except Exception as e:
                    logger.error(f"Telethon 通知管理员失败 admin_id={admin_id}: {e}")

        except Exception as e:
            logger.error(f"Telethon 通知管理员失败: {type(e).__name__}: {e}", exc_info=True)
            # 恢复请求状态
            self.db.update_request_status(request['id'], 'pending')

    async def handle_callback_query(self, event, client) -> None:
        """
        处理管理员按钮点击（Telethon Event）

        Args:
            event: Telethon 回调事件对象
            client: Telethon 客户端实例
        """
        try:
            # 应答回调查询
            await event.answer()

            # 解析callback_data
            data = event.data.decode() if isinstance(event.data, bytes) else event.data
            if not data.startswith(('confirm_summary_', 'reject_summary_')):
                return

            parts = data.split('_')
            action = parts[0]
            request_id = int(parts[2])

            # 获取请求信息
            request = self.db.get_request_status(request_id)
            if not request:
                await event.edit("❌ 请求不存在或已过期")
                return

            if action == 'confirm':
                await self._process_summary_request_telethon(event, request, client)
            elif action == 'reject':
                await self._reject_summary_request_telethon(event, request)

        except Exception as e:
            logger.error(f"处理回调查询失败: {type(e).__name__}: {e}", exc_info=True)

    async def _process_summary_request_telethon(self, event, request: Dict[str, Any],
                                               client) -> None:
        """
        处理确认的总结请求（Telethon 版本）

        Args:
            event: Telethon 回调事件对象
            request: 请求信息
            client: Telethon 客户端实例
        """
        try:
            request_id = request['id']
            channel_id = request['target_channel']

            # 更新消息
            await event.edit(f"⏳ 正在为频道生成总结...\n\n频道: {channel_id}\n请求ID: {request_id}")

            # 调用真实的总结生成函数
            # skip_admins=True 因为管理员已经看到请求通知了
            result = await generate_channel_summary(
                channel_id=channel_id,
                client=client,
                skip_admins=True
            )

            # 检查结果
            if result['success']:
                # 更新数据库状态
                self.db.update_request_status(
                    request_id,
                    'completed',
                    result={
                        'message': '总结生成成功',
                        'summary_text': result['summary_text'],
                        'message_count': result['message_count'],
                        'channel_name': result['channel_name']
                    }
                )

                # 构建成功消息
                channel_name = result['channel_name']
                message_count = result['message_count']
                summary_preview = result['summary_text'][:200] + "..." if len(result['summary_text']) > 200 else result['summary_text']

                success_message = f"""✅ 总结生成完成！

📢 频道: {channel_name}
📊 处理消息数: {message_count}
📝 请求ID: {request_id}

📋 总结预览:
{summary_preview}"""

                await event.edit(success_message)

                # 通知请求者
                await self._notify_requester(
                    request_id,
                    channel_id,
                    f"✅ 总结已成功生成！\n\n频道: {channel_name}\n处理消息数: {message_count}"
                )
            else:
                # 生成失败
                error_msg = result.get('error', '未知错误')
                self.db.update_request_status(request_id, 'failed', result={'error': error_msg})
                await event.edit(f"❌ 生成总结失败: {error_msg}")

                # 通知请求者
                await self._notify_requester(request_id, channel_id, f"❌ 总结生成失败: {error_msg}")

        except Exception as e:
            logger.error(f"处理总结请求失败: {type(e).__name__}: {e}", exc_info=True)
            self.db.update_request_status(request_id, 'failed', result={'error': str(e)})
            await event.edit(f"❌ 生成总结失败: {str(e)}")

            # 通知请求者
            await self._notify_requester(request_id, request['target_channel'], f"❌ 总结生成失败: {str(e)}")

    async def _reject_summary_request_telethon(self, event, request: Dict[str, Any]) -> None:
        """
        拒绝总结请求（Telethon 版本）

        Args:
            event: Telethon 回调事件对象
            request: 请求信息
        """
        try:
            request_id = request['id']
            channel_id = request['target_channel']

            # 更新状态
            self.db.update_request_status(request_id, 'failed',
                                         result={'error': '管理员拒绝了请求'})

            await event.edit(
                f"❌ 已拒绝总结请求\n\n频道: {channel_id}\n请求ID: {request_id}"
            )

            # 通知请求者
            await self._notify_requester(request_id, channel_id, "您的总结请求被管理员拒绝。")

        except Exception as e:
            logger.error(f"拒绝请求失败: {type(e).__name__}: {e}", exc_info=True)

    async def _notify_requester(self, request_id: int, channel_id: str,
                               message: str) -> None:
        """
        通过问答Bot通知请求者

        Args:
            request_id: 请求ID
            channel_id: 频道ID
            message: 通知消息
        """
        try:
            # 获取请求信息
            request = self.db.get_request_status(request_id)
            if not request:
                return

            requested_by = request.get('requested_by')
            if not requested_by:
                return

            # 这里需要通过问答Bot发送消息
            # 由于两个Bot是独立的进程，我们需要使用共享的数据库或API
            # 方案1：将通知写入notification_queue表，由问答Bot轮询
            # 方案2：使用HTTP API调用问答Bot
            # 方案3：使用Telegram的Bot API直接发送（需要问答Bot的token）

            # 当前使用方案1：写入通知队列
            self.db.create_notification(
                user_id=requested_by,
                notification_type='request_result',
                content={
                    'request_id': request_id,
                    'channel_id': channel_id,
                    'message': message
                }
            )

            logger.info(f"已为用户 {requested_by} 创建通知")

        except Exception as e:
            logger.error(f"通知请求者失败: {type(e).__name__}: {e}", exc_info=True)


# 创建全局实例
mainbot_request_handler = None


def get_mainbot_request_handler():
    """获取全局请求处理器实例"""
    global mainbot_request_handler
    if mainbot_request_handler is None:
        mainbot_request_handler = MainBotRequestHandler()
    return mainbot_request_handler
