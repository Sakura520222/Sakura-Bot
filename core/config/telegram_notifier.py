# core/config/telegram_notifier.py
import logging
from typing import TYPE_CHECKING

from telegram import Bot

from core.config.events import ConfigValidationErrorEvent

if TYPE_CHECKING:
    pass

logger = logging.getLogger(__name__)


class ConfigErrorNotifier:
    """配置错误Telegram通知器"""

    def __init__(self, bot_token: str, admin_chat_id: str):
        self._bot_token = bot_token
        self._admin_chat_id = admin_chat_id

    async def on_config_validation_error(self, event: ConfigValidationErrorEvent):
        """处理配置验证失败事件（支持回滚通知）"""
        try:
            bot = Bot(token=self._bot_token)

            # 构建增强的错误消息
            if event.rolled_back:
                message = f"""🚨 *配置验证失败并已自动回滚*

{event.format_error_message()}

🔄 *回滚操作*：配置已自动恢复到最后有效版本
📝 *建议*：请检查并修复配置文件中的 JSON 语法错误后保存，系统将自动重新加载"""
            else:
                message = f"""🚨 *配置验证失败*

{event.format_error_message()}

⚠️ *警告*：配置回滚失败，系统仍使用旧配置运行
📝 *建议*：请立即检查并手动修复配置文件"""

            await bot.send_message(
                chat_id=self._admin_chat_id,
                text=message,
                parse_mode="Markdown",
            )

            logger.info("已发送配置回滚通知到Telegram")

        except Exception as e:
            logger.error(f"发送Telegram回滚通知失败: {e}", exc_info=True)
