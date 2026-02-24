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
主Bot推送处理器
当有新总结生成时，通知订阅用户
"""

import logging
import os
from typing import Any

from telegram import Bot
from telegram.error import TelegramError

from core.database import get_db_manager

logger = logging.getLogger(__name__)


class MainBotPushHandler:
    """主Bot推送处理器"""

    def __init__(self, qa_bot_token: str = None):
        """
        初始化推送处理器

        Args:
            qa_bot_token: 问答Bot的Token（用于发送通知）
        """
        self.db = get_db_manager()
        self.qa_bot_token = qa_bot_token or os.getenv("QA_BOT_TOKEN")
        self.qa_bot = None

        if self.qa_bot_token:
            self.qa_bot = Bot(token=self.qa_bot_token)
            logger.info("主Bot推送处理器初始化完成")
        else:
            logger.warning("未配置QA_BOT_TOKEN，无法发送通知")

    async def notify_summary_subscribers(
        self, channel_id: str, channel_name: str, summary_text: str
    ) -> int:
        """
        通知订阅了该频道的用户

        Args:
            channel_id: 频道URL
            channel_name: 频道名称
            summary_text: 总结内容

        Returns:
            成功通知的用户数
        """
        try:
            logger.info(f"开始处理频道 {channel_name} ({channel_id}) 的订阅推送")

            if not self.qa_bot:
                logger.warning("问答Bot未初始化，无法发送通知")
                return 0

            # SQLite 不使用连接池，MySQL 才需要检查
            # 检查数据库是否已初始化
            if not hasattr(self.db, "_db_type"):
                logger.warning("数据库管理器未初始化，无法获取订阅者")
                return 0

            # 获取订阅用户
            subscribers = await self.db.get_channel_subscribers(channel_id, "summary")

            if not subscribers:
                logger.info(f"频道 {channel_name} ({channel_id}) 没有订阅用户")
                return 0

            logger.info(f"频道 {channel_name} 有 {len(subscribers)} 个订阅用户，准备发送通知")

            # 截取总结内容（避免消息过长）
            summary_preview = (
                summary_text[:300] + "..." if len(summary_text) > 300 else summary_text
            )

            success_count = 0

            for user_id in subscribers:
                try:
                    # 发送通知 (使用HTML格式，更稳定)
                    message = f"""📬 <b>新总结通知</b>

频道 {channel_name} 有新的总结发布了！

<b>总结预览</b>:
{summary_preview}

💡 使用 <code>/mysubscriptions</code> 查看您的订阅
💡 使用 <code>/unsubscribe <频道链接></code> 取消订阅"""

                    await self.qa_bot.send_message(chat_id=user_id, text=message, parse_mode="HTML")

                    success_count += 1
                    logger.info(f"成功通知用户 {user_id}")

                except TelegramError as e:
                    if "bot was blocked by the user" in str(e):
                        logger.warning(f"用户 {user_id} 已阻止Bot，取消订阅")
                        # 自动取消该用户的订阅
                        await self.db.remove_subscription(user_id, channel_id)
                    elif "user is deactivated" in str(e):
                        logger.warning(f"用户 {user_id} 账号已停用，取消订阅")
                        await self.db.remove_subscription(user_id, channel_id)
                    else:
                        logger.error(f"通知用户 {user_id} 失败: {e}")
                except Exception as e:
                    logger.error(f"通知用户 {user_id} 时发生错误: {type(e).__name__}: {e}")

            logger.info(f"推送完成: 成功 {success_count}/{len(subscribers)}")
            return success_count

        except Exception as e:
            logger.error(f"通知订阅用户失败: {type(e).__name__}: {e}", exc_info=True)
            return 0

    async def process_pending_notifications(self) -> int:
        """
        处理待发送的通知队列（由问答Bot轮询调用）

        Returns:
            成功处理的通知数
        """
        try:
            if not self.qa_bot:
                return 0

            # 获取待发送通知
            notifications = await self.db.get_pending_notifications(limit=50)

            if not notifications:
                return 0

            logger.info(f"发现 {len(notifications)} 个待发送通知")

            success_count = 0

            for notification in notifications:
                try:
                    notification_id = notification["id"]
                    user_id = notification["user_id"]
                    notification_type = notification["notification_type"]
                    content = notification.get("content", {})

                    # 根据类型构建消息
                    if notification_type == "request_result":
                        message = self._format_request_result(content)
                    elif notification_type == "summary_push":
                        message = self._format_summary_push(content)
                    else:
                        message = "您有新的通知"

                    # 发送消息 (使用HTML格式)
                    await self.qa_bot.send_message(chat_id=user_id, text=message, parse_mode="HTML")

                    # 更新状态
                    await self.db.update_notification_status(notification_id, "sent")
                    success_count += 1

                except TelegramError as e:
                    if "bot was blocked by the user" in str(e):
                        logger.warning(f"用户 {user_id} 已阻止Bot")
                        await self.db.update_notification_status(notification["id"], "failed")
                    else:
                        logger.error(f"发送通知失败: {e}")
                        await self.db.update_notification_status(notification["id"], "failed")
                except Exception as e:
                    logger.error(f"处理通知失败: {type(e).__name__}: {e}")
                    await self.db.update_notification_status(notification["id"], "failed")

            return success_count

        except Exception as e:
            logger.error(f"处理通知队列失败: {type(e).__name__}: {e}", exc_info=True)
            return 0

    def _format_request_result(self, content: dict[str, Any]) -> str:
        """格式化请求结果通知"""
        request_id = content.get("request_id", "未知")
        channel_id = content.get("channel_id", "未知频道")
        message = content.get("message", "")

        return f"""📝 **请求结果通知**

您的总结请求已处理完成！

**请求ID**: {request_id}
**频道**: {channel_id}

**结果**: {message}

💡 感谢使用！"""

    def _format_summary_push(self, content: dict[str, Any]) -> str:
        """格式化总结推送通知"""
        channel_name = content.get("channel_name", "未知频道")
        summary_preview = content.get("summary_preview", "")

        return f"""📬 **新总结通知**

频道 {channel_name} 有新的总结发布了！

**总结预览**:
{summary_preview}

💡 查看完整总结，请访问频道。"""


# 创建全局实例
mainbot_push_handler = None


def get_mainbot_push_handler():
    """获取全局推送处理器实例"""
    global mainbot_push_handler
    if mainbot_push_handler is None:
        mainbot_push_handler = MainBotPushHandler()
    return mainbot_push_handler
