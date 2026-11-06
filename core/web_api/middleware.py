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
认证中间件

拦截所有 /api 请求，验证 JWT Token，放行公开路径。
"""

import logging
import time
from collections import OrderedDict

from fastapi import Request
from starlette.middleware.base import BaseHTTPMiddleware, RequestResponseEndpoint
from starlette.responses import JSONResponse, Response

from .auth import verify_token

logger = logging.getLogger(__name__)
access_logger = logging.getLogger("core.web_api.access")
_wiki_buckets = OrderedDict()

# 不需要认证的路径
PUBLIC_PATHS = {
    "/api/auth/login",
    "/api/auth/telegram-callback",
    "/api/auth/status",
    "/api/health",
    "/api/docs",
    "/api/redoc",
    "/api/openapi.json",
}


class AuthMiddleware(BaseHTTPMiddleware):
    """JWT 认证中间件。

    注意：当前继承 BaseHTTPMiddleware，适用于现有 WebUI JSON API。
    BaseHTTPMiddleware 对流式响应和 WebSocket 场景存在已知限制，后续如引入
    相关能力应迁移为纯 ASGI 中间件。
    """

    async def dispatch(self, request: Request, call_next: RequestResponseEndpoint) -> Response:
        path = request.url.path

        from .routes.wiki import WIKI_AVATAR_ROUTE, is_public_wiki_request

        if is_public_wiki_request(path, request.method):
            # Trust the socket peer, never an arbitrary forwarded header. Bound memory.
            peer = request.client.host if request.client else "unknown"
            window = int(time.monotonic() // 60)
            old_window, count = _wiki_buckets.pop(peer, (window, 0))
            count = count + 1 if old_window == window else 1
            _wiki_buckets[peer] = (window, count)
            while len(_wiki_buckets) > 10000:
                _wiki_buckets.popitem(last=False)
            if count > 120:
                return JSONResponse(
                    {"detail": "Wiki 请求过于频繁"}, status_code=429, headers={"Retry-After": "60"}
                )
            response = await call_next(request)
            # 仅成功的头像响应允许浏览器短缓存（404/503 保持 no-store）；
            # 其余匿名 Wiki 响应一律 no-store。
            if response.status_code == 200 and WIKI_AVATAR_ROUTE.fullmatch(path):
                response.headers["Cache-Control"] = "public, max-age=3600"
            else:
                response.headers["Cache-Control"] = "no-store"
            response.headers["X-Content-Type-Options"] = "nosniff"
            response.headers["Content-Security-Policy"] = (
                "default-src 'none'; frame-ancestors 'none'"
            )
            return response

        # 非 API 路径直接放行（静态文件等）
        if not path.startswith("/api"):
            return await call_next(request)

        # 公开路径放行
        if path in PUBLIC_PATHS:
            return await call_next(request)

        # OPTIONS 请求放行（CORS 预检）
        if request.method == "OPTIONS":
            return await call_next(request)

        # 提取 Token
        token = _extract_token(request)
        if not token:
            return JSONResponse(
                status_code=401,
                content={"detail": "未提供认证 Token"},
            )

        # 验证 Token
        user = verify_token(token)
        if not user:
            return JSONResponse(
                status_code=401,
                content={"detail": "Token 无效或已过期"},
            )

        # 将用户信息存入 request.state
        request.state.user = user

        return await call_next(request)


def _extract_token(request: Request) -> str | None:
    """从请求中提取 Token"""
    # 查询参数
    token = request.query_params.get("token")
    if token:
        return token

    # Authorization Header
    auth_header = request.headers.get("Authorization", "")
    if auth_header.startswith("Bearer "):
        return auth_header[7:]

    return None


class AccessLogMiddleware(BaseHTTPMiddleware):
    """WebUI 精简访问日志中间件。

    注意：当前继承 BaseHTTPMiddleware，适用于现有 WebUI JSON API。
    BaseHTTPMiddleware 对流式响应和 WebSocket 场景存在已知限制，后续如引入
    相关能力应迁移为纯 ASGI 中间件以降低额外开销。
    """

    async def dispatch(self, request: Request, call_next: RequestResponseEndpoint) -> Response:
        start_time = time.perf_counter()
        path = request.url.path

        try:
            response = await call_next(request)
        except Exception as e:
            elapsed_ms = (time.perf_counter() - start_time) * 1000
            client_host = request.client.host if request.client else "unknown"
            access_logger.error(
                "WebUI 请求异常: %s %s client=%s elapsed=%.2fms error=%s: %s",
                request.method,
                path,
                client_host,
                elapsed_ms,
                type(e).__name__,
                e,
                exc_info=True,
            )
            raise

        elapsed_ms = (time.perf_counter() - start_time) * 1000
        client_host = request.client.host if request.client else "unknown"

        log_method = access_logger.debug if path == "/api/health" else access_logger.info
        log_method(
            "WebUI 请求: %s %s status=%s client=%s elapsed=%.2fms",
            request.method,
            path,
            response.status_code,
            client_host,
            elapsed_ms,
        )

        return response
