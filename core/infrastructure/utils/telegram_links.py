# Copyright 2026 Sakura-Bot
#
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
#
# - 署名：必须提供本项目的原始来源链接
# - 相同方式共享：衍生作品必须采用相同的许可证
#
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
# 许可证全文：参见 LICENSE 文件

"""Telegram 帖子链接构造工具。

统一处理公开频道与私有频道的消息链接格式，避免把数字频道 ID 错误拼接成
公开频道 URL：

- 公开频道（有 username）：``https://t.me/<username>/<message_id>``
- 私有频道 / 无 username 的频道：``https://t.me/c/<channel_id>/<message_id>``

无法验证出合法链接时返回 None，调用方不得把内部 ID、数据库主键或
总结报告消息 ID 冒充为频道帖子链接。
"""

import re

# t.me 链接的已知 host 前缀
_LINK_PREFIXES = (
    "https://t.me/",
    "http://t.me/",
    "https://telegram.me/",
    "http://telegram.me/",
    "t.me/",
    "telegram.me/",
)

# Telegram username 规则：4-32 位，字母开头，仅字母/数字/下划线
# （官方实际仅允许小写，这里放宽大小写以兼容历史数据）
_USERNAME_PATTERN = re.compile(r"[A-Za-z][A-Za-z0-9_]{3,31}")


def _normalize_channel(channel_id: str) -> str | None:
    """把各种频道标识归一化为纯 username 或纯数字 ID。

    支持：t.me/telegram.me 链接、@username、``c/<id>`` 路径、
    Bot API 的 ``-100`` 前缀标记 ID、裸 username、裸数字 ID。
    邀请链接（+开头）与无法识别的标识返回 None。
    """
    channel = str(channel_id or "").strip()
    if not channel:
        return None

    for prefix in _LINK_PREFIXES:
        if channel.startswith(prefix):
            channel = channel[len(prefix) :]
            break

    channel = channel.strip("/")

    # 邀请链接（如 +AbCdEf）无法定位具体帖子
    if channel.startswith("+") or channel.split("/")[0].startswith("+"):
        return None

    # 多段路径：取第一段（如 c/12345 → c/12345 交由后续分支处理）
    first_segment = channel.split("/")[0]
    if len(channel.split("/")) > 1:
        # 仅允许 c/<id> 形式的多段路径，其余（如 username/123 帖子路径）取首段
        if first_segment != "c":
            channel = first_segment

    if channel.startswith("c/"):
        # c/<id> 或 c/<id>/<message_id>，只保留数字频道 ID 段
        channel = channel[2:].split("/")[0]
        # c/ 后必须是数字 ID
        return channel if channel.isdigit() else None

    if channel.startswith("@"):
        channel = channel[1:]

    # Bot API 标记 ID：-100<channel_id> → 去掉 -100 前缀
    if channel.startswith("-100") and channel[4:].isdigit():
        channel = channel[4:]

    if not channel:
        return None

    return channel


def build_telegram_post_link(
    channel_id: str | int | None, message_id: int | str | None
) -> str | None:
    """构造 Telegram 频道帖子链接。

    Args:
        channel_id: 频道标识，支持 username、@username、t.me 链接、
            数字内部 ID、Bot API ``-100`` 标记 ID、``c/<id>`` 路径
        message_id: 频道内的真实消息 ID（必须是正整数）

    Returns:
        合法的帖子链接；频道标识无法识别、消息 ID 非法时返回 None。
        私有频道链接（/c/ 形式）要求访问者拥有频道权限才能打开。
    """
    if message_id is None:
        return None

    try:
        mid = int(message_id)
    except (TypeError, ValueError):
        return None

    if mid <= 0:
        return None

    channel = _normalize_channel(str(channel_id) if channel_id is not None else "")
    if not channel:
        return None

    # 数字频道 ID：私有频道格式
    if channel.isdigit():
        return f"https://t.me/c/{channel}/{mid}"

    # 公开频道 username
    if _USERNAME_PATTERN.fullmatch(channel):
        return f"https://t.me/{channel}/{mid}"

    return None
