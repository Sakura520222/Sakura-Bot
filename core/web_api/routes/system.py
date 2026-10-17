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
系统运维 API 路由

提供 Bot 状态查看、暂停/恢复、日志级别调整等功能。
"""

import asyncio
import collections
import logging
import time
from datetime import datetime
from pathlib import Path
from typing import Any

import aiofiles
from fastapi import APIRouter, HTTPException, Query, Request

from core import __version__
from core.config import RESTART_FLAG_FILE, get_bot_state, set_bot_state
from core.web_api.deps import (
    actor_from_request as _actor_from_request,
)
from core.web_api.deps import (
    audit_duration as _audit_duration,
)
from core.web_api.deps import (
    get_database_or_none as _get_db,
)
from core.web_api.deps import (
    maybe_await as _maybe_await,
)
from core.web_api.deps import (
    record_system_audit,
)
from core.web_api.schemas.system import BotStatusResponse, CleanupRequest, LogLevelUpdate

logger = logging.getLogger(__name__)

router = APIRouter()


def _format_datetime(value) -> str:
    """格式化数据库时间字段，兼容字符串和 datetime。"""
    if isinstance(value, datetime):
        return value.isoformat(sep=" ", timespec="seconds")
    return str(value) if value is not None else ""


@router.get("/status")
async def get_system_status():
    """获取 Bot 运行状态"""
    try:
        from core.config import load_config

        config = load_config()
        channels = config.get("channels", [])
        forwarding = config.get("forwarding", {})

        # 检查各组件状态
        userbot_connected = False
        try:
            from core.telegram.client import get_active_client

            client = get_active_client()
            userbot_connected = client is not None and client.is_connected()
        except Exception:
            pass

        qa_bot_running = False
        qa_bot_status = {}
        try:
            from core.system.process_manager import get_qa_bot_process, get_qa_bot_status

            proc = get_qa_bot_process()
            qa_bot_running = proc is not None and proc.poll() is None
            qa_bot_status = get_qa_bot_status()
        except Exception:
            pass

        cache_status = {"size": 0}
        try:
            from core.services.cache_manager import get_discussion_cache

            cache = get_discussion_cache()
            cache_status = {"size": cache.size()}
        except Exception:
            pass

        database_status = await _collect_database_status()
        log_status = _collect_log_status()
        audit_summary = await _collect_audit_summary()

        return {
            "success": True,
            "data": BotStatusResponse(
                status=get_bot_state(),
                version=__version__,
                log_level=logging.getLevelName(logging.getLogger().level),
                channel_count=len(channels),
                forwarding_enabled=forwarding.get("enabled", False),
                qa_bot_running=qa_bot_running,
                userbot_connected=userbot_connected,
                uptime_seconds=0,  # TODO: 从 bootstrap 获取
            ).model_dump()
            | {
                "qa_bot": qa_bot_status,
                "database": database_status,
                "cache": cache_status,
                "logs": log_status,
                "audit": audit_summary,
            },
        }

    except Exception as e:
        logger.error(f"获取系统状态失败: {type(e).__name__}: {e}", exc_info=True)
        raise HTTPException(status_code=500, detail=str(e)) from e


@router.post("/pause")
async def pause_bot():
    """暂停 Bot 定时任务"""
    try:
        from core.config import BOT_STATE_PAUSED

        current = get_bot_state()
        if current == BOT_STATE_PAUSED:
            return {"success": False, "message": "Bot 已处于暂停状态"}

        set_bot_state(BOT_STATE_PAUSED)
        logger.info("已通过 WebUI 暂停 Bot")
        return {"success": True, "message": "Bot 已暂停"}

    except Exception as e:
        logger.error(f"暂停 Bot 失败: {type(e).__name__}: {e}", exc_info=True)
        raise HTTPException(status_code=500, detail=str(e)) from e


@router.post("/resume")
async def resume_bot():
    """恢复 Bot 定时任务"""
    try:
        from core.config import BOT_STATE_RUNNING

        current = get_bot_state()
        if current == BOT_STATE_RUNNING:
            return {"success": False, "message": "Bot 已处于运行状态"}

        set_bot_state(BOT_STATE_RUNNING)
        logger.info("已通过 WebUI 恢复 Bot")
        return {"success": True, "message": "Bot 已恢复"}

    except Exception as e:
        logger.error(f"恢复 Bot 失败: {type(e).__name__}: {e}", exc_info=True)
        raise HTTPException(status_code=500, detail=str(e)) from e


@router.put("/log-level")
async def update_log_level(request: LogLevelUpdate):
    """修改日志级别"""
    try:
        import logging as logging_module

        # 更新根 logger 和所有 handler 的级别
        root_logger = logging_module.getLogger()
        level = getattr(logging_module, request.level)
        root_logger.setLevel(level)
        for handler in root_logger.handlers:
            handler.setLevel(level)

        # 更新配置
        from core.config import load_config, save_config_changes

        config = load_config()
        config["log_level"] = request.level
        save_config_changes(config)

        logger.info(f"已通过 WebUI 修改日志级别: {request.level}")
        return {"success": True, "message": f"日志级别已修改为: {request.level}"}

    except Exception as e:
        logger.error(f"修改日志级别失败: {type(e).__name__}: {e}", exc_info=True)
        raise HTTPException(status_code=500, detail=str(e)) from e


@router.post("/restart")
async def restart_bot():
    """立即重启 Bot（优雅关闭后用 os.execv 替换当前进程）"""
    try:
        # 写入重启标记（webui_restart 区别于 Telegram 命令的标记）
        async with aiofiles.open(RESTART_FLAG_FILE, "w") as f:
            await f.write("webui_restart")

        logger.info("已通过 WebUI 请求重启 Bot")

        # 立即触发优雅关闭流程
        from core.config import trigger_shutdown

        trigger_shutdown()

        return {"success": True, "message": "正在重启 Bot..."}

    except Exception as e:
        logger.error(f"请求重启失败: {type(e).__name__}: {e}", exc_info=True)
        raise HTTPException(status_code=500, detail=str(e)) from e


@router.post("/database/clear-and-restart")
async def clear_database_and_restart(request: Request):
    """一键清空数据库并重启 Bot。"""
    started_at = time.perf_counter()
    db = _get_db()
    if not db or not hasattr(db, "clear_all_data"):
        raise HTTPException(status_code=503, detail="数据库管理器不可用")

    try:
        actor = _actor_from_request(request)
        results = await _maybe_await(db.clear_all_data())
        failed_tables = [table for table, count in results.items() if count < 0]
        if failed_tables:
            message = f"清空数据库部分失败: {', '.join(failed_tables)}"
            await record_system_audit(
                action="database.clear_and_restart",
                actor=actor,
                params_summary="{}",
                success=False,
                message=message,
                duration_ms=_audit_duration(started_at),
            )
            raise HTTPException(status_code=500, detail=message)

        # 写入重启标记并触发优雅关闭；清库后不再写审计，避免重新产生一条审计记录。
        async with aiofiles.open(RESTART_FLAG_FILE, "w") as f:
            await f.write("webui_clear_database_restart")

        logger.warning(
            "已通过 WebUI 清空数据库并请求重启 Bot，操作者=%s，清理结果=%s",
            actor,
            results,
        )

        from core.config import trigger_shutdown

        trigger_shutdown()
        deleted_total = sum(count for count in results.values() if count > 0)
        return {
            "success": True,
            "message": f"已清空数据库（共 {deleted_total} 条记录），正在重启 Bot...",
            "data": {"deleted_total": deleted_total, "tables": results},
        }

    except HTTPException:
        raise
    except Exception as e:
        message = f"清空数据库并重启失败: {type(e).__name__}: {e}"
        await record_system_audit(
            action="database.clear_and_restart",
            actor=_actor_from_request(request),
            params_summary="{}",
            success=False,
            message=message,
            duration_ms=_audit_duration(started_at),
        )
        logger.error(message, exc_info=True)
        raise HTTPException(status_code=500, detail=message) from e


@router.post("/qa-bot/start")
async def start_qa_bot_endpoint(request: Request):
    """启动 QA Bot 子进程。"""
    started_at = time.perf_counter()
    from core.system.process_manager import start_qa_bot

    result = start_qa_bot()
    success = bool(result.get("success"))
    await record_system_audit(
        action="qa_bot.start",
        actor=_actor_from_request(request),
        success=success,
        message=result.get("message", ""),
        duration_ms=_audit_duration(started_at),
    )
    return {
        "success": success,
        "message": result.get("message", ""),
        "data": {"pid": result.get("pid")},
    }


@router.post("/qa-bot/stop")
async def stop_qa_bot_endpoint(request: Request):
    """停止 QA Bot 子进程。"""
    started_at = time.perf_counter()
    from core.system.process_manager import stop_qa_bot

    result = stop_qa_bot()
    success = bool(result.get("success"))
    await record_system_audit(
        action="qa_bot.stop",
        actor=_actor_from_request(request),
        success=success,
        message=result.get("message", ""),
        duration_ms=_audit_duration(started_at),
    )
    return {"success": success, "message": result.get("message", "")}


@router.post("/qa-bot/restart")
async def restart_qa_bot_endpoint(request: Request):
    """重启 QA Bot 子进程。"""
    started_at = time.perf_counter()
    from core.system.process_manager import restart_qa_bot

    result = restart_qa_bot()
    success = bool(result.get("success"))
    await record_system_audit(
        action="qa_bot.restart",
        actor=_actor_from_request(request),
        success=success,
        message=result.get("message", ""),
        duration_ms=_audit_duration(started_at),
    )
    return {
        "success": success,
        "message": result.get("message", ""),
        "data": {"pid": result.get("pid")},
    }


@router.post("/qa-bot/health")
async def check_qa_bot_health_endpoint(request: Request):
    """检查 QA Bot 健康状态。"""
    started_at = time.perf_counter()
    from core.system.process_manager import check_qa_bot_health

    healthy, should_restart, message = check_qa_bot_health()
    await record_system_audit(
        action="qa_bot.health",
        actor=_actor_from_request(request),
        success=healthy,
        message=message,
        duration_ms=_audit_duration(started_at),
    )
    return {
        "success": True,
        "data": {
            "healthy": healthy,
            "should_restart": should_restart,
            "message": message,
        },
    }


@router.post("/config/reload")
async def reload_config_endpoint(request: Request):
    """重新读取配置文件并刷新模块变量。"""
    started_at = time.perf_counter()
    try:
        import core.config as config_module

        config = config_module.load_config()
        config_module.update_module_variables(config)
        message = "配置已重载"
        await record_system_audit(
            action="config.reload",
            actor=_actor_from_request(request),
            success=True,
            message=message,
            duration_ms=_audit_duration(started_at),
        )
        return {
            "success": True,
            "message": message,
            "data": {"config_keys": len(config)},
        }
    except Exception as e:
        message = f"配置重载失败: {e}"
        await record_system_audit(
            action="config.reload",
            actor=_actor_from_request(request),
            success=False,
            message=message,
            duration_ms=_audit_duration(started_at),
        )
        raise HTTPException(status_code=500, detail=message) from e


@router.get("/cache/discussion")
async def get_discussion_cache_status():
    """获取讨论组缓存状态。"""
    from core.services.cache_manager import get_discussion_cache

    cache = get_discussion_cache()
    return {
        "success": True,
        "data": {
            "size": cache.size(),
            "channels": list(cache.get_all().keys()),
        },
    }


@router.delete("/cache/discussion")
async def clear_discussion_cache_endpoint(
    request: Request,
    channel: str | None = Query(default=None, description="可选，指定频道"),
):
    """清理讨论组缓存。"""
    started_at = time.perf_counter()
    from core.services.cache_manager import get_discussion_cache

    cache = get_discussion_cache()
    if channel:
        deleted = cache.delete(channel)
        cleared = 1 if deleted else 0
        message = f"已清理频道缓存: {channel}" if deleted else f"频道缓存不存在: {channel}"
    else:
        cleared = cache.size()
        cache.clear()
        message = f"已清理全部讨论组缓存: {cleared} 条"

    await record_system_audit(
        action="cache.discussion.clear",
        actor=_actor_from_request(request),
        target=channel or "all",
        success=True,
        message=message,
        duration_ms=_audit_duration(started_at),
    )
    return {"success": True, "message": message, "data": {"cleared": cleared}}


@router.get("/database/status")
async def get_database_status():
    """获取数据库状态。"""
    return {"success": True, "data": await _collect_database_status()}


@router.post("/database/cleanup/forwarded-messages")
async def cleanup_forwarded_messages_endpoint(request_data: CleanupRequest, request: Request):
    """清理旧转发记录。"""
    started_at = time.perf_counter()
    db = _get_db()
    if not db or not hasattr(db, "cleanup_old_forwarded_messages"):
        raise HTTPException(status_code=503, detail="数据库管理器不可用")

    try:
        deleted = await _maybe_await(db.cleanup_old_forwarded_messages(days=request_data.days))
    except Exception as e:
        message = f"清理转发记录失败: {type(e).__name__}: {e}"
        await record_system_audit(
            action="database.cleanup.forwarded_messages",
            actor=_actor_from_request(request),
            params_summary=f'{{"days": {request_data.days}}}',
            success=False,
            message=message,
            duration_ms=_audit_duration(started_at),
        )
        raise HTTPException(status_code=500, detail=message) from e

    message = f"已清理 {deleted} 条旧转发记录"
    await record_system_audit(
        action="database.cleanup.forwarded_messages",
        actor=_actor_from_request(request),
        params_summary=f'{{"days": {request_data.days}}}',
        success=True,
        message=message,
        duration_ms=_audit_duration(started_at),
    )
    return {
        "success": True,
        "message": message,
        "data": {"deleted_count": deleted, "days": request_data.days},
    }


@router.post("/database/cleanup/poll-regenerations")
async def cleanup_poll_regenerations_endpoint(request_data: CleanupRequest, request: Request):
    """清理旧投票重生成记录。"""
    started_at = time.perf_counter()
    db = _get_db()
    if not db or not hasattr(db, "cleanup_old_poll_regenerations"):
        raise HTTPException(status_code=503, detail="数据库管理器不可用")

    try:
        deleted = await _maybe_await(db.cleanup_old_poll_regenerations(days=request_data.days))
    except Exception as e:
        message = f"清理投票重生成记录失败: {type(e).__name__}: {e}"
        await record_system_audit(
            action="database.cleanup.poll_regenerations",
            actor=_actor_from_request(request),
            params_summary=f'{{"days": {request_data.days}}}',
            success=False,
            message=message,
            duration_ms=_audit_duration(started_at),
        )
        raise HTTPException(status_code=500, detail=message) from e

    message = f"已清理 {deleted} 条旧投票重生成记录"
    await record_system_audit(
        action="database.cleanup.poll_regenerations",
        actor=_actor_from_request(request),
        params_summary=f'{{"days": {request_data.days}}}',
        success=True,
        message=message,
        duration_ms=_audit_duration(started_at),
    )
    return {
        "success": True,
        "message": message,
        "data": {"deleted_count": deleted, "days": request_data.days},
    }


@router.post("/database/cleanup/audit-logs")
async def cleanup_audit_logs_endpoint(
    request: Request,
    request_data: CleanupRequest,
):
    """清理旧审计记录。"""
    started_at = time.perf_counter()
    db = _get_db()
    if not db or not hasattr(db, "cleanup_old_system_audit_logs"):
        raise HTTPException(status_code=503, detail="数据库管理器不可用")

    try:
        deleted = await _maybe_await(db.cleanup_old_system_audit_logs(days=request_data.days))
    except Exception as e:
        message = f"清理审计记录失败: {type(e).__name__}: {e}"
        await record_system_audit(
            action="database.cleanup.audit_logs",
            actor=_actor_from_request(request),
            params_summary=f'{{"days": {request_data.days}}}',
            success=False,
            message=message,
            duration_ms=_audit_duration(started_at),
        )
        raise HTTPException(status_code=500, detail=message) from e

    message = f"已清理 {deleted} 条旧审计记录"
    await record_system_audit(
        action="database.cleanup.audit_logs",
        actor=_actor_from_request(request),
        params_summary=f'{{"days": {request_data.days}}}',
        success=True,
        message=message,
        duration_ms=_audit_duration(started_at),
    )
    return {
        "success": True,
        "message": message,
        "data": {"deleted_count": deleted, "days": request_data.days},
    }


@router.get("/logs/recent")
async def get_recent_logs(
    lines: int = Query(default=100, ge=1, le=1000),
    level: str | None = Query(default=None),
    keyword: str | None = Query(default=None, max_length=100),
):
    """读取最近日志内容，不提供文件下载。"""
    try:
        from core.settings import get_settings

        log_path = Path(get_settings().log.log_file_path)
        log_exists, log_is_file = await asyncio.to_thread(
            lambda: (log_path.exists(), log_path.is_file())
        )
        if not log_exists or not log_is_file:
            return {
                "success": True,
                "data": {"lines": [], "total_returned": 0, "path": str(log_path)},
                "message": "日志文件不存在",
            }

        # 使用 deque 限制内存占用，只保留尾部 N*3 行作为过滤候选
        tail_lines: collections.deque[str] = collections.deque(maxlen=lines * 3)
        async with aiofiles.open(log_path, encoding="utf-8", errors="replace") as f:
            async for raw_line in f:
                tail_lines.append(raw_line.rstrip("\n"))

        filtered = list(tail_lines)
        if level:
            level_upper = level.upper()
            filtered = [line for line in filtered if level_upper in line.upper()]
        if keyword:
            keyword_lower = keyword.lower()
            filtered = [line for line in filtered if keyword_lower in line.lower()]

        selected = filtered[-lines:]
        return {
            "success": True,
            "data": {
                "lines": selected,
                "total_returned": len(selected),
                "path": log_path.name,
            },
        }
    except Exception as e:
        logger.error(f"读取最近日志失败: {type(e).__name__}: {e}", exc_info=True)
        raise HTTPException(status_code=500, detail=str(e)) from e


@router.get("/audit-logs")
async def get_audit_logs(limit: int = Query(default=50, ge=1, le=200)):
    """获取最近 WebUI 运维审计记录。"""
    db = _get_db()
    if not db or not hasattr(db, "get_system_audit_logs"):
        return {"success": True, "data": {"items": [], "total": 0}}

    rows = await _maybe_await(db.get_system_audit_logs(limit=limit))
    items = [_normalize_audit_row(row) for row in rows]
    return {"success": True, "data": {"items": items, "total": len(items)}}


async def _collect_database_status() -> dict[str, Any]:
    """汇总数据库状态。"""
    db = _get_db()
    if not db:
        return {"available": False, "message": "数据库管理器不可用"}

    data: dict[str, Any] = {"available": True}
    try:
        if hasattr(db, "get_database_type"):
            data["type"] = db.get_database_type()
        else:
            data["type"] = db.__class__.__name__
        if hasattr(db, "get_database_version"):
            data["version"] = db.get_database_version()
        if hasattr(db, "get_statistics"):
            data["statistics"] = await _maybe_await(db.get_statistics())
    except Exception as e:
        data.update({"available": False, "message": str(e)})
    return data


def _collect_log_status() -> dict[str, Any]:
    """汇总日志文件状态。"""
    try:
        from core.settings import get_settings

        log_path = Path(get_settings().log.log_file_path)
        return {
            "path": str(log_path),
            "exists": log_path.exists(),
            "size_bytes": log_path.stat().st_size if log_path.exists() else 0,
        }
    except Exception as e:
        return {"exists": False, "message": str(e)}


async def _collect_audit_summary() -> dict[str, Any]:
    """汇总最近审计记录摘要。"""
    db = _get_db()
    if not db or not hasattr(db, "get_system_audit_logs"):
        return {"recent_count": 0}
    try:
        rows = await _maybe_await(db.get_system_audit_logs(limit=5))
        return {"recent_count": len(rows), "recent": [_normalize_audit_row(row) for row in rows]}
    except Exception as e:
        return {"recent_count": 0, "message": str(e)}


def _normalize_audit_row(row: dict[str, Any]) -> dict[str, Any]:
    """统一审计记录返回形态。"""
    return {
        "id": row.get("id"),
        "action": row.get("action", ""),
        "actor": row.get("actor", ""),
        "target": row.get("target", ""),
        "params_summary": row.get("params_summary", ""),
        "success": bool(row.get("success")),
        "message": row.get("message", ""),
        "duration_ms": row.get("duration_ms", 0),
        "created_at": _format_datetime(row.get("created_at")),
    }
