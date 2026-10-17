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
依赖注入模块

提供 FastAPI 路由的公共依赖项，如认证、数据库访问、配置管理等。
"""

import asyncio
import hashlib
import hmac
import inspect
import logging
import time
from collections.abc import Awaitable
from typing import Any, TypeVar

from fastapi import HTTPException, Query, status

from core.config import ADMIN_LIST, read_config, save_config_changes
from core.infrastructure.database.manager import get_db_manager
from core.settings import get_settings

logger = logging.getLogger(__name__)

T = TypeVar("T")

_config_manager: Any | None = None


def configure_config_manager(config_manager: Any | None) -> None:
    """配置 WebUI 使用的配置管理器

    Args:
        config_manager: 主进程 ConfigManager 实例，用于保存配置后显式触发热重载
    """
    global _config_manager
    _config_manager = config_manager
    if config_manager:
        logger.info("WebUI 配置热重载管理器已注入")


def get_config() -> dict:
    """获取当前配置（同步读取 config.json）"""
    return read_config()


def write_config(config: dict) -> None:
    """提交 get_config 基线后的修改和删除，成功后触发热更新。"""
    save_config_changes(config)
    _schedule_config_reload()


def _schedule_config_reload() -> None:
    """安排配置热重载任务

    WebUI 与 Bot 共享事件循环时，保存配置后直接调用 ConfigManager.reload_config()，
    避免仅依赖 watchdog 文件事件导致热重载延迟或遗漏。
    """
    config_manager = _config_manager
    if not config_manager:
        logger.debug("未注入配置管理器，跳过显式热重载触发")
        return

    async def _reload() -> None:
        try:
            success, error = await config_manager.reload_config()
            if success:
                logger.info("✅ WebUI 保存配置后已触发热重载")
            else:
                logger.error(f"WebUI 保存配置后触发热重载失败: {error}")
        except Exception as e:
            logger.error(f"WebUI 配置热重载任务异常: {type(e).__name__}: {e}", exc_info=True)

    try:
        loop = asyncio.get_running_loop()
    except RuntimeError:
        logger.warning("当前无线程运行中的事件循环，跳过 WebUI 显式热重载触发")
        return

    loop.create_task(_reload())


def get_database():
    """获取数据库管理器实例"""
    return get_db_manager()


def get_database_or_none():
    """安全获取数据库管理器，失败时返回 None。"""
    try:
        return get_db_manager()
    except Exception as e:
        logger.debug(f"获取数据库管理器失败: {e}")
        return None


async def maybe_await(value: T | Awaitable[T]) -> T:
    """兼容同步和异步返回值。"""
    if inspect.isawaitable(value):
        return await value
    return value


def actor_from_request(request: Any | None) -> str:
    """从请求状态中提取 WebUI 操作者。"""
    user = getattr(getattr(request, "state", None), "user", None)
    return str(getattr(user, "user_id", "unknown"))


def audit_duration(started_at: float) -> int:
    """计算操作耗时（毫秒）。"""
    return int((time.perf_counter() - started_at) * 1000)


async def record_system_audit(
    *,
    action: str,
    actor: str,
    target: str = "",
    params_summary: str = "{}",
    success: bool,
    message: str = "",
    duration_ms: int = 0,
) -> None:
    """写入 WebUI 系统运维审计记录，失败时仅记录日志。"""
    try:
        db = get_database_or_none()
        if db and hasattr(db, "add_system_audit_log"):
            await maybe_await(
                db.add_system_audit_log(
                    action=action,
                    actor=actor,
                    target=target,
                    params_summary=params_summary,
                    success=success,
                    message=message,
                    duration_ms=duration_ms,
                )
            )
    except Exception as e:
        logger.warning(f"写入系统审计记录失败: {e}")


def get_bot_state() -> str:
    """获取当前 Bot 运行状态"""
    from core.config import get_bot_state as _get_bot_state

    return _get_bot_state()


# ==================== Telegram OAuth 认证 ====================


def verify_telegram_auth(auth_data: dict[str, Any]) -> bool:
    """验证 Telegram Login Widget 的认证数据

    Args:
        auth_data: Telegram 回调数据（包含 hash 字段）

    Returns:
        bool: 验证是否通过
    """
    bot_token = get_settings().telegram.bot_token
    if not bot_token:
        logger.error("未配置 Bot Token，无法验证 Telegram 认证")
        return False

    # 提取 hash 并排序其余字段
    check_hash = auth_data.get("hash", "")
    if not check_hash:
        return False

    # 按 key 字母序排列其余字段，生成 check_string
    data_check = {k: v for k, v in auth_data.items() if k != "hash"}
    check_string = "\n".join(f"{k}={v}" for k, v in sorted(data_check.items()))

    # 使用 bot_token 的 SHA256 作为 key
    secret_key = hashlib.sha256(bot_token.encode()).digest()

    # 计算 HMAC-SHA256
    computed_hash = hmac.new(secret_key, check_string.encode(), hashlib.sha256).hexdigest()

    return hmac.compare_digest(computed_hash, check_hash)


def check_admin_permission(user_id: int | str) -> bool:
    """检查用户是否在管理员列表中

    Args:
        user_id: Telegram 用户 ID

    Returns:
        bool: 是否为管理员
    """
    if ADMIN_LIST == ["me"]:
        return True
    return user_id in ADMIN_LIST


# ==================== JWT 认证（简化版）====================

_jwt_secret: str = ""
_jwt_enabled: bool = False


def configure_jwt_auth(secret_key: str) -> None:
    """配置 JWT 认证密钥

    Args:
        secret_key: JWT 签名密钥
    """
    global _jwt_secret, _jwt_enabled
    _jwt_secret = secret_key
    _jwt_enabled = True
    logger.info("JWT 认证已配置")


def is_auth_enabled() -> bool:
    """检查是否启用了认证"""
    settings = get_settings()
    return getattr(settings, "webui", None) is not None


async def require_admin(
    token: str | None = Query(None, alias="token", description="认证 Token"),
) -> dict:
    """要求管理员权限的依赖项

    当前实现使用简单 Token 验证，后续可升级为完整 JWT。
    """
    settings = get_settings()
    webui_settings = getattr(settings, "webui", None)

    # 如果未配置 WebUI settings，跳过认证（开发模式）
    if not webui_settings:
        return {"user_id": "dev", "is_admin": True}

    # 检查 Token
    if not token:
        raise HTTPException(
            status_code=status.HTTP_401_UNAUTHORIZED,
            detail="缺少认证 Token",
        )

    # 简单 Token 验证（使用 admin_ids + secret 组合）
    expected_token = _generate_admin_token()
    if token != expected_token:
        raise HTTPException(
            status_code=status.HTTP_403_FORBIDDEN,
            detail="无效的认证 Token",
        )

    return {"user_id": "admin", "is_admin": True}


def _generate_admin_token() -> str:
    """生成管理员 Token（基于 bot_token 的稳定 hash）"""
    bot_token = get_settings().telegram.bot_token or ""
    return hashlib.sha256(f"sakura-bot-webui:{bot_token}".encode()).hexdigest()[:32]


def get_admin_token() -> str:
    """获取当前管理员 Token（供前端使用）"""
    return _generate_admin_token()
