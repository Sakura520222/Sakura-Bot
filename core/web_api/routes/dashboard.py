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
仪表板 API 路由

提供 Bot 状态概览、关键指标统计等。
"""

import logging
import time

from fastapi import APIRouter

from core import __version__
from core.config import get_bot_state
from core.config.ai_runtime import effective_ai_config
from core.settings import get_settings

logger = logging.getLogger(__name__)

router = APIRouter()

# 记录启动时间
_start_time: float = time.time()


@router.get("/dashboard")
async def get_dashboard():
    """获取仪表板概览数据"""
    try:
        from core.config import load_config

        config = load_config()
        settings = get_settings()

        channels = config.get("channels", [])
        forwarding_config = config.get("forwarding", {})
        schedules = config.get("summary_schedules", {})

        # 获取数据库统计
        total_summaries = 0
        try:
            db = _get_db()
            if db:
                stats_data = await db.get_statistics()
                total_summaries = stats_data.get("total_count", 0) if stats_data else 0
        except Exception as e:
            logger.debug(f"获取数据库统计失败: {e}")

        # 计算运行时长
        uptime_seconds = time.time() - _start_time

        return {
            "success": True,
            "data": {
                "bot_status": get_bot_state(),
                "version": __version__,
                "channel_count": len(channels),
                "schedule_count": len(schedules),
                "forwarding_enabled": forwarding_config.get("enabled", False),
                "forwarding_rule_count": len(forwarding_config.get("rules", [])),
                "total_summaries": total_summaries,
                "log_level": settings.log.log_level,
                "uptime_seconds": round(uptime_seconds, 1),
                "ai_model": effective_ai_config(config).model,
                "qa_bot_username": settings.telegram.bot_token and "configured",
            },
        }
    except Exception as e:
        logger.error(f"获取仪表板数据失败: {type(e).__name__}: {e}", exc_info=True)
        return {"success": False, "message": f"获取仪表板数据失败: {e}"}


def _get_db():
    """安全获取数据库管理器"""
    try:
        from core.infrastructure.database.manager import get_db_manager

        return get_db_manager()
    except Exception:
        return None
