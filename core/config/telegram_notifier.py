# core/config/telegram_notifier.py
import logging
from datetime import datetime
from typing import TYPE_CHECKING

from telegram import Bot, InlineKeyboardButton, InlineKeyboardMarkup

from core.config.events import ConfigValidationErrorEvent

if TYPE_CHECKING:
    from .manager import ConfigManager

logger = logging.getLogger(__name__)


class ConfigErrorNotifier:
    """配置错误Telegram通知器"""

    def __init__(self, bot_token: str, admin_chat_id: str):
        self._bot_token = bot_token
        self._admin_chat_id = admin_chat_id
        self._config_manager: ConfigManager | None = None

    def set_config_manager(self, config_manager: ConfigManager):
        """设置配置管理器引用（用于重试功能）"""
        self._config_manager = config_manager

    async def on_config_validation_error(self, event: ConfigValidationErrorEvent):
        """处理配置验证失败事件"""
        try:
            bot = Bot(token=self._bot_token)

            # 发送错误消息
            message = event.format_error_message()

            # 创建重试按钮
            keyboard = InlineKeyboardMarkup(
                [
                    [
                        InlineKeyboardButton(
                            "🔄 重试配置加载", callback_data=f"retry_config:{event.timestamp}"
                        )
                    ]
                ]
            )

            await bot.send_message(
                chat_id=self._admin_chat_id,
                text=message,
                reply_markup=keyboard,
                parse_mode="Markdown",
            )

            logger.info("已发送配置错误通知到Telegram")

        except Exception as e:
            logger.error(f"发送Telegram错误通知失败: {e}", exc_info=True)

    async def handle_retry_callback(self, callback_query):
        """处理重试按钮回调"""
        await callback_query.answer("正在重新加载配置...")

        try:
            callback_data = callback_query.data
            if callback_data and callback_data.startswith("retry_config:"):
                # 触发配置重载
                if self._config_manager:
                    success, error = await self._config_manager.reload_config()

                    if success:
                        await callback_query.edit_message_text(
                            "✅ 配置重载成功！\n\n"
                            f"🕐 {datetime.now().strftime('%Y-%m-%d %H:%M:%S')}"
                        )
                    else:
                        await callback_query.edit_message_text(
                            f"❌ 配置重载仍然失败\n\n错误: {error}\n\n请继续修复配置文件后重试"
                        )
        except Exception as e:
            logger.error(f"处理重试回调失败: {e}", exc_info=True)
