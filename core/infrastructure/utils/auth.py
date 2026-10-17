# Copyright 2026 Sakura-Bot
#
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
#
# - 署名：必须提供本项目的原始来源链接
# - 相同方式共享：衍生作品必须采用相同的许可证
#
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
# 许可证全文：参见 LICENSE 文件

"""
命令权限检查工具。

集中处理管理员权限校验，目标是逐步替代所有命令模块中的内联权限检查，
避免重复实现并便于后续统一增加审计日志或调整权限逻辑。
"""

import logging
from typing import TYPE_CHECKING, Any

from core.config import ADMIN_LIST
from core.i18n.i18n import get_text

if TYPE_CHECKING:
    from collections.abc import Awaitable, Callable

logger = logging.getLogger(__name__)


def is_admin(user_id: int | str | None) -> bool:
    """严格判定用户是否为管理员（fail-closed）。

    只有与 ``REPORT_ADMIN_IDS`` 中显式配置、格式合法的 Telegram 数字用户 ID
    完全一致时才返回 True；未配置管理员、ID 非法或类型不匹配一律返回 False。
    历史上的 ``ADMIN_LIST == ["me"]`` 通配语义已废除——绝不能因未配置而放行
    所有用户。

    Args:
        user_id: Telegram 用户 ID（int 或数字字符串）

    Returns:
        bool: 是否为管理员
    """
    if user_id is None:
        return False

    try:
        uid = int(user_id)
    except (TypeError, ValueError):
        logger.warning(f"管理员身份检查收到非法用户ID: {user_id!r}")
        return False

    return uid in ADMIN_LIST


async def check_admin_permission(
    event: Any,
    command_name: str | None = None,
    *,
    reply_func: "Callable[[str], Awaitable[Any]] | None" = None,
) -> bool:
    """检查命令发送者是否为管理员。

    Args:
        event: Telegram 消息或事件对象，需包含 sender_id 与 reply 方法
        command_name: 命令名称，用于日志记录；未提供时会从 event.text/message 推断
        reply_func: 可选自定义回复函数，默认使用 event.reply

    Returns:
        bool: 具有管理员权限返回 True，否则返回 False
    """
    sender_id = getattr(event, "sender_id", None)
    command = command_name or _get_event_command(event)

    if sender_id is None:
        logger.warning(f"事件缺少 sender_id，无法进行权限检查，命令: {command}")
        return False

    if not is_admin(sender_id):
        logger.warning(f"发送者 {sender_id} 没有权限执行命令 {command}")
        reply = reply_func or getattr(event, "reply", None)
        if reply:
            await reply(get_text("error.permission_denied"))
        return False

    return True


def _get_event_command(event: Any) -> str:
    """从事件对象中提取命令文本。"""
    command = getattr(event, "text", None) or getattr(event, "message", None)
    if isinstance(command, str):
        return command
    return "<unknown>"
