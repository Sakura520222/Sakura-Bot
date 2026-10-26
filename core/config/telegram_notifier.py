# core/config/telegram_notifier.py
import asyncio
import logging
from datetime import datetime

from core.config.events import ConfigChangedEvent, ConfigValidationErrorEvent
from core.infrastructure.telegram_bot_api import send_message as bot_api_send_message

logger = logging.getLogger(__name__)

# Telegram 发送超时（秒），防止通知发送阻塞事件链
_SEND_TIMEOUT = 10.0


class ConfigErrorNotifier:
    """配置通知器 - 支持错误和变更通知（非阻塞）"""

    def __init__(self, bot_token: str, admin_chat_id: str):
        self._bot_token = bot_token
        self._admin_chat_id = admin_chat_id

    async def on_config_validation_error(self, event: ConfigValidationErrorEvent):
        """处理配置验证失败事件（非阻塞发送通知）"""
        try:
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

            # 非阻塞发送：作为后台任务启动，不阻塞事件链
            asyncio.create_task(self._safe_send(self._bot_token, self._admin_chat_id, message))

        except Exception as e:
            logger.error(f"创建配置回滚通知任务失败: {e}", exc_info=True)

    async def on_config_changed(self, event: ConfigChangedEvent):
        """处理配置变更成功事件 - 非阻塞发送 Telegram 通知"""
        try:
            # 按配置分组
            grouped = self._group_changes(event.changed_fields) if event.changed_fields else {}

            # 构建消息
            message = f"""✅ *配置重载成功*

📊 版本: `{event.version}`
🕐 时间: {datetime.fromtimestamp(event.timestamp).strftime("%Y-%m-%d %H:%M:%S")}
📝 变更字段: {len(event.changed_fields) if event.changed_fields else 0}个

{self._format_grouped_changes(grouped)}

💡 提示: 配置已即时生效，无需重启"""

            # 非阻塞发送：作为后台任务启动，不阻塞事件链
            asyncio.create_task(self._safe_send(self._bot_token, self._admin_chat_id, message))

        except Exception as e:
            logger.error(f"创建配置变更通知任务失败: {e}", exc_info=True)

    async def _safe_send(self, bot_token: str, chat_id: str, text: str):
        """安全发送消息（带超时保护，不阻塞调用者）

        Args:
            bot_token: Bot 令牌
            chat_id: 目标聊天 ID
            text: 消息文本
        """
        try:
            await asyncio.wait_for(
                bot_api_send_message(
                    token=bot_token,
                    chat_id=chat_id,
                    text=text,
                    parse_mode="Markdown",
                ),
                timeout=_SEND_TIMEOUT,
            )
            logger.info("已发送配置通知到Telegram")
        except TimeoutError:
            logger.warning(f"发送配置通知超时（{_SEND_TIMEOUT}s），跳过通知")
        except Exception as e:
            logger.error(f"发送配置通知失败: {type(e).__name__}: {e}")

    def _group_changes(self, changed_fields: set[str]) -> dict:
        """将变更字段按配置区域分组"""
        sections = {
            "系统": {"channels", "log_level", "language", "send_report_to_source"},
            "投票": {
                "enable_poll",
                "poll_regen_threshold",
                "enable_vote_regen_request",
                "channel_poll_settings",
            },
            "转发": {"forwarding"},
            "总结": {"summary_schedules"},
            "评论欢迎": {"comment_welcome", "channel_comment_welcome"},
        }

        grouped = {}
        for field in changed_fields:
            top_level = field.split(".")[0]
            found = False
            for section, fields in sections.items():
                if top_level in fields:
                    grouped.setdefault(section, []).append(field)
                    found = True
                    break
            if not found:
                grouped.setdefault("其他", []).append(field)

        return grouped

    def _format_grouped_changes(self, grouped: dict) -> str:
        """格式化分组变更信息"""
        if not grouped:
            return "📋 无具体变更信息"

        lines = []
        for section, fields in grouped.items():
            lines.append(f"\n📌 *{section}配置*:")
            for field in fields[:5]:  # 最多显示5个字段
                lines.append(f"  • `{field}`")
            if len(fields) > 5:
                lines.append(f"  • ... 等 {len(fields)} 个字段")

        return "\n".join(lines)
