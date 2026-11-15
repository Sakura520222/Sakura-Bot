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
认证 API 路由

提供 Token 登录、验证、Telegram OAuth 回调等认证功能。
"""

import hashlib
import logging
import time
from datetime import UTC, datetime

from fastapi import APIRouter, HTTPException, Request
from pydantic import BaseModel, Field

from ..auth import verify_token

logger = logging.getLogger(__name__)

router = APIRouter()


class TokenLoginRequest(BaseModel):
    """Token 登录请求"""

    token: str = Field(..., description="管理 Token（从启动日志获取）")


class TokenLoginResponse(BaseModel):
    """登录成功响应"""

    success: bool
    access_token: str = ""
    message: str = ""


class AuthStatusResponse(BaseModel):
    """认证状态响应"""

    authenticated: bool
    user_id: str = ""
    dev_mode: bool = False


@router.post("/login", response_model=TokenLoginResponse)
async def token_login(request: TokenLoginRequest):
    """通过管理 Token 登录

    管理 Token 是 Bot Token 的 SHA256 前16位，可在启动日志中查看。
    """
    try:
        expected = _compute_admin_token()
        if not expected:
            return TokenLoginResponse(success=False, message="Bot Token 未配置，无法登录")

        if request.token != expected and request.token != "dev":
            return TokenLoginResponse(success=False, message="Token 无效")

        # 开发模式登录需要 .env 中启用 WEBUI_DEV_MODE
        if request.token == "dev":
            from core.settings import get_settings

            if not get_settings().webui.dev_mode:
                return TokenLoginResponse(
                    success=False, message="开发模式未启用，请在 .env 中设置 WEBUI_DEV_MODE=true"
                )

        # 生成 JWT
        user_id = "dev" if request.token == "dev" else "admin"
        from ..auth import generate_token

        access_token = generate_token(user_id)

        logger.info(f"WebUI 登录成功: {user_id}")
        return TokenLoginResponse(success=True, access_token=access_token, message="登录成功")

    except Exception as e:
        logger.error(f"登录失败: {type(e).__name__}: {e}", exc_info=True)
        raise HTTPException(status_code=500, detail=str(e)) from e


@router.post("/telegram-callback", response_model=TokenLoginResponse)
async def telegram_oauth_callback(request: Request):
    """Telegram Login Widget OAuth 回调

    接收 Telegram Login Widget 的认证数据，验证签名后签发 JWT。
    """
    try:
        from ..deps import verify_telegram_auth

        data = dict(request.query_params)
        if not data.get("hash"):
            # 尝试从 body 获取
            body = await request.json()
            data = body

        if not verify_telegram_auth(data):
            return TokenLoginResponse(success=False, message="Telegram 认证验证失败")

        user_id = str(data.get("id", "telegram_user"))

        from ..deps import check_admin_permission

        if not check_admin_permission(user_id):
            return TokenLoginResponse(success=False, message="无管理员权限")

        from .wiki import runtime

        if not await runtime().accounts.consume_legacy(
            data["hash"], datetime.fromtimestamp(time.time(), UTC)
        ):
            return TokenLoginResponse(success=False, message="Telegram 登录请求已使用")

        from ..auth import generate_token

        access_token = generate_token(user_id)

        logger.info(f"Telegram OAuth 登录成功: user_id={user_id}")
        return TokenLoginResponse(success=True, access_token=access_token, message="登录成功")

    except HTTPException:
        raise
    except Exception as e:
        logger.warning("Telegram OAuth 回调失败: %s", type(e).__name__)
        raise HTTPException(status_code=500, detail="Telegram 登录暂时不可用") from None


@router.get("/status", response_model=AuthStatusResponse)
async def auth_status(request: Request):
    """检查当前认证状态"""
    from core.settings import get_settings

    dev_mode = get_settings().webui.dev_mode

    token = _extract_token(request)
    if not token:
        return AuthStatusResponse(authenticated=False, dev_mode=dev_mode)

    user = verify_token(token)
    if user:
        return AuthStatusResponse(authenticated=True, user_id=user.user_id, dev_mode=dev_mode)
    return AuthStatusResponse(authenticated=False, dev_mode=dev_mode)


@router.post("/refresh")
async def refresh_token(request: Request):
    """刷新 Token（需要有效 Token）"""
    token = _extract_token(request)
    if not token:
        raise HTTPException(status_code=401, detail="未提供 Token")

    user = verify_token(token)
    if not user:
        raise HTTPException(status_code=401, detail="Token 无效或已过期")

    from ..auth import generate_token

    new_token = generate_token(user.user_id)
    return {"success": True, "access_token": new_token, "message": "Token 已刷新"}


def _extract_token(request: Request) -> str | None:
    """从请求中提取 Token"""
    # 1. 查询参数
    token = request.query_params.get("token")
    if token:
        return token

    # 2. Authorization Header
    auth_header = request.headers.get("Authorization", "")
    if auth_header.startswith("Bearer "):
        return auth_header[7:]

    return None


def _compute_admin_token() -> str | None:
    """计算管理 Token（Bot Token 的 SHA256 前16位）"""
    from core.settings import get_settings

    settings = get_settings()
    bot_token = settings.telegram.bot_token
    if not bot_token:
        return None
    return hashlib.sha256(bot_token.encode()).hexdigest()[:16]
