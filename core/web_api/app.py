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
FastAPI 应用工厂

创建和配置 FastAPI 应用实例，注册路由和中间件。
"""

import logging
from pathlib import Path

from fastapi import FastAPI, Request
from fastapi.middleware.cors import CORSMiddleware
from fastapi.responses import FileResponse
from fastapi.staticfiles import StaticFiles

from core import __version__

from .middleware import AccessLogMiddleware, AuthMiddleware
from .routes import (
    ai_config,
    auth,
    channels,
    commands,
    dashboard,
    database,
    forwarding,
    guard,
    interaction,
    schedules,
    stats,
    summaries,
    system,
    userbot,
    vector_store,
)

logger = logging.getLogger(__name__)


def create_app() -> FastAPI:
    """创建并配置 FastAPI 应用实例"""

    app = FastAPI(
        title="Sakura-Bot WebUI API",
        description="Sakura-Bot Telegram 频道管理助手 Web 管理界面 API",
        version=__version__,
        docs_url="/api/docs",
        redoc_url="/api/redoc",
    )

    # 配置 CORS
    app.add_middleware(
        CORSMiddleware,
        allow_origins=["*"],  # 生产环境应限制具体域名
        allow_credentials=True,
        allow_methods=["*"],
        allow_headers=["*"],
    )

    # JWT 认证中间件（在 CORS 之后，路由之前）
    app.add_middleware(AuthMiddleware)

    # 精简访问日志中间件。
    # FastAPI add_middleware 按 LIFO 顺序执行；这里最后注册，使访问日志位于最外层，
    # 以便记录所有 API 请求（包括被认证中间件拒绝的 401 响应）的状态与耗时。
    app.add_middleware(AccessLogMiddleware)

    # 注册 API 路由
    app.include_router(auth.router, prefix="/api/auth", tags=["认证"])
    app.include_router(dashboard.router, prefix="/api", tags=["仪表板"])
    app.include_router(channels.router, prefix="/api/channels", tags=["频道管理"])
    app.include_router(commands.router, prefix="/api/commands", tags=["命令中心"])
    app.include_router(ai_config.router, prefix="/api/ai", tags=["AI 配置"])
    app.include_router(schedules.router, prefix="/api/schedules", tags=["定时任务"])
    app.include_router(forwarding.router, prefix="/api/forwarding", tags=["转发规则"])
    app.include_router(system.router, prefix="/api/system", tags=["系统运维"])
    app.include_router(stats.router, prefix="/api/stats", tags=["统计数据"])
    app.include_router(interaction.router, prefix="/api/interaction", tags=["互动设置"])
    app.include_router(summaries.router, prefix="/api/summaries", tags=["总结生成"])
    app.include_router(userbot.router, prefix="/api/userbot", tags=["UserBot 管理"])
    app.include_router(vector_store.router, prefix="/api/vector-store", tags=["向量存储"])
    app.include_router(database.router, prefix="/api/tables", tags=["数据库管理"])
    app.include_router(guard.router, prefix="/api/guard", tags=["Sakura Guard"])

    # 注册健康检查端点
    @app.get("/api/health", tags=["健康检查"])
    async def health_check():
        return {"status": "ok", "service": "sakura-bot-webui"}

    # ==================== 前端静态文件 + SPA 路由回退 ====================
    static_dir = Path(__file__).parent.parent.parent / "web" / "dist"
    if static_dir.exists():
        # 1. 挂载 /assets 等静态资源（精确匹配）
        assets_dir = static_dir / "assets"
        if assets_dir.exists():
            app.mount("/assets", StaticFiles(directory=str(assets_dir)), name="assets")

        # 2. SPA 路由回退：所有非 /api、非静态资源的 GET 请求返回 index.html
        index_html = static_dir / "index.html"

        @app.get("/favicon.svg", include_in_schema=False)
        async def favicon():
            favicon_path = static_dir / "favicon.svg"
            if favicon_path.exists():
                return FileResponse(str(favicon_path))
            return FileResponse(str(index_html))

        @app.get("/{path:path}", include_in_schema=False)
        async def spa_fallback(request: Request, path: str):
            """SPA 路由回退处理

            对于前端路由（如 /login、/channels 等），返回 index.html
            让 Vue Router 处理客户端路由。
            """
            # 如果请求的是静态文件（有扩展名且文件存在），直接返回文件
            if path and "." in path.split("/")[-1]:
                file_path = static_dir / path
                if file_path.exists() and file_path.is_file():
                    return FileResponse(str(file_path))

            # 其余所有路径回退到 index.html（SPA 路由）
            return FileResponse(str(index_html))

        logger.info(f"已挂载前端静态文件: {static_dir}")
    else:
        logger.debug(f"前端静态文件目录不存在: {static_dir}，跳过挂载")

    logger.info("FastAPI 应用创建完成")
    return app
