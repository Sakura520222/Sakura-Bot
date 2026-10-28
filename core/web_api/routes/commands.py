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
命令中心 API 路由

将 Telegram Bot 命令映射为 WebUI 可执行的结构化操作。
"""

import asyncio
import logging
import time
from collections.abc import Awaitable, Callable
from typing import Any

from fastapi import APIRouter, HTTPException, Request

from core.bot_commands import BOT_COMMANDS
from core.config import normalize_channel_id
from core.web_api.deps import get_config, write_config
from core.web_api.routes.system import _actor_from_request, _audit_duration, record_system_audit
from core.web_api.schemas.commands import (
    CommandCategory,
    CommandExecuteRequest,
    CommandExecuteResponse,
    CommandItem,
    CommandParameter,
    ParameterType,
)

logger = logging.getLogger(__name__)

router = APIRouter()

OperationHandler = Callable[[dict[str, Any], Request], Awaitable[dict[str, Any]]]

DANGER_CONFIRM_TEXT = "CONFIRM"
LOG_LEVEL_OPTIONS = ["DEBUG", "INFO", "WARNING", "ERROR", "CRITICAL"]
SENSITIVE_PARAM_NAMES = {"api_key", "secret_key", "token", "secret", "password"}


def _param(
    name: str,
    label: str,
    parameter_type: ParameterType = "string",
    *,
    required: bool = False,
    description: str = "",
    placeholder: str = "",
    default: Any | None = None,
    options: list[dict[str, Any]] | None = None,
) -> CommandParameter:
    """创建命令参数定义。"""
    return CommandParameter(
        name=name,
        label=label,
        type=parameter_type,
        required=required,
        description=description,
        placeholder=placeholder,
        default=default,
        options=options or [],
    )


COMMAND_METADATA: dict[str, dict[str, Any]] = {
    "summary": {
        "risk": "normal",
        "covered_by_page": "/stats",
        "parameters": [
            _param(
                "channel", "频道", required=True, placeholder="https://t.me/example 或 @example"
            ),
        ],
    },
    "showchannels": {"risk": "safe", "covered_by_page": "/channels"},
    "addchannel": {
        "risk": "normal",
        "covered_by_page": "/channels",
        "parameters": [
            _param("channel", "频道", required=True, placeholder="https://t.me/example")
        ],
    },
    "deletechannel": {
        "risk": "danger",
        "covered_by_page": "/channels",
        "parameters": [
            _param("channel", "频道", required=True, placeholder="https://t.me/example")
        ],
    },
    "setloglevel": {
        "risk": "normal",
        "covered_by_page": "/system",
        "parameters": [
            _param(
                "level",
                "日志级别",
                "select",
                required=True,
                default="INFO",
                options=[{"label": level, "value": level} for level in LOG_LEVEL_OPTIONS],
            ),
        ],
    },
    "pause": {"risk": "normal", "covered_by_page": "/system"},
    "resume": {"risk": "normal", "covered_by_page": "/system"},
    "restart": {"risk": "danger", "covered_by_page": "/system"},
    "shutdown": {"risk": "danger", "covered_by_page": "/system"},
    "clearcache": {
        "risk": "danger",
        "covered_by_page": "/system",
        "parameters": [_param("channel", "频道", required=False, placeholder="留空清理全部")],
    },
    "db_clear": {"risk": "danger", "covered_by_page": "/system"},
    "update": {"risk": "danger", "covered_by_page": "/system"},
    "qa_status": {"risk": "safe", "covered_by_page": "/system"},
    "qa_start": {"risk": "normal", "covered_by_page": "/system"},
    "qa_stop": {"risk": "danger", "covered_by_page": "/system"},
    "qa_restart": {"risk": "danger", "covered_by_page": "/system"},
    "qa_stats": {"risk": "safe", "covered_by_page": "/system"},
    "qa_runtime": {
        "risk": "danger",
        "covered_by_page": "/system",
        "parameters": [
            _param(
                "runtime",
                "目标运行时",
                required=False,
                placeholder="go 或 python，留空仅查看当前运行时",
            )
        ],
    },
    "forwarding": {"risk": "safe", "covered_by_page": "/forwarding"},
    "forwarding_enable": {"risk": "normal", "covered_by_page": "/forwarding"},
    "forwarding_disable": {"risk": "danger", "covered_by_page": "/forwarding"},
    "forwarding_add_rule": {
        "risk": "normal",
        "covered_by_page": "/forwarding",
        "parameters": [
            _param("source_channel", "源频道", required=True, placeholder="https://t.me/source"),
            _param("target_channel", "目标频道", required=True, placeholder="https://t.me/target"),
            _param("keywords", "关键词白名单", "tags"),
            _param("blacklist", "关键词黑名单", "tags"),
            _param("copy_mode", "复制模式", "boolean", default=False),
            _param("forward_original_only", "只转发原创", "boolean", default=False),
            _param("custom_footer", "自定义底栏", "textarea"),
        ],
    },
    "forwarding_remove_rule": {
        "risk": "danger",
        "covered_by_page": "/forwarding",
        "parameters": [
            _param(
                "source_channel",
                "源频道",
                required=False,
                placeholder="https://t.me/source",
                description="优先按源频道和目标频道删除，与 Telegram 命令保持一致",
            ),
            _param(
                "target_channel",
                "目标频道",
                required=False,
                placeholder="https://t.me/target",
                description="与源频道一起填写时按频道对删除规则",
            ),
            _param(
                "rule_index",
                "规则序号",
                "number",
                required=False,
                description="兼容旧界面：从 1 开始；规则列表会展示序号与频道对应关系",
            ),
        ],
    },
    "forwarding_stats": {
        "risk": "safe",
        "covered_by_page": "/forwarding",
        "description_note": "当前复用转发状态处理器，返回规则数量与启用状态。",
    },
    "userbot_status": {"risk": "safe", "covered_by_page": "/userbot"},
    "userbot_join": {
        "risk": "normal",
        "covered_by_page": "/userbot",
        "parameters": [
            _param("channel", "频道", required=True, placeholder="https://t.me/example")
        ],
    },
    "userbot_leave": {
        "risk": "danger",
        "covered_by_page": "/userbot",
        "parameters": [
            _param("channel", "频道", required=True, placeholder="https://t.me/example")
        ],
    },
    "userbot_list": {"risk": "safe", "covered_by_page": "/userbot"},
}


def _default_metadata(command: str) -> dict[str, Any]:
    """为未显式配置的命令生成默认元数据。"""
    return {
        "risk": "normal",
        "covered_by_page": _infer_covered_page(command),
        "parameters": [],
    }


def _infer_covered_page(command: str) -> str | None:
    """根据命令名推断已覆盖页面。"""
    if "prompt" in command or "aicfg" in command:
        return "/ai-config"
    if "schedule" in command or command in {"clearsummarytime", "setsendtosource"}:
        return "/schedules"
    if "poll" in command or "commentwelcome" in command or "autopoll" in command:
        return "/interaction"
    if command in {"history", "export", "stats"}:
        return "/stats"
    return None


def _build_command_item(command: str, description: str, category: str) -> CommandItem:
    """构建命令中心命令项。"""
    metadata = COMMAND_METADATA.get(command, _default_metadata(command))
    executable = command in OPERATION_HANDLERS
    return CommandItem(
        command=command,
        description=description,
        operation_id=command,
        category=category,
        risk=metadata.get("risk", "normal"),
        executable=executable,
        covered_by_page=metadata.get("covered_by_page"),
        parameters=metadata.get("parameters", []),
        aliases=metadata.get("aliases", []),
    )


def _require_param(params: dict[str, Any], name: str) -> Any:
    """读取必填参数。"""
    value = params.get(name)
    if value is None or value == "":
        raise HTTPException(status_code=400, detail=f"缺少必填参数: {name}")
    return value


def _ensure_forwarding(config: dict[str, Any]) -> dict[str, Any]:
    """确保转发配置存在。"""
    if "forwarding" not in config or not isinstance(config["forwarding"], dict):
        config["forwarding"] = {}
    config["forwarding"].setdefault("rules", [])
    return config["forwarding"]


async def _op_summary(params: dict[str, Any], _request: Request) -> dict[str, Any]:
    from core.commands.summary_commands import generate_channel_summary
    from core.telegram.client import get_active_client

    channel = str(_require_param(params, "channel"))
    client = get_active_client()
    if not client or not client.is_connected():
        raise HTTPException(status_code=503, detail="Telegram 客户端未连接，无法生成总结")

    result = await generate_channel_summary(channel, client, skip_admins=True)
    if result.get("success"):
        summary_text = result.get("summary_text", "")
        return {
            "success": True,
            "message": f"总结生成成功（{result.get('message_count', 0)} 条消息）",
            "data": {
                "channel_name": result.get("channel_name", channel),
                "message_count": result.get("message_count", 0),
                "summary_preview": summary_text[:200] + ("..." if len(summary_text) > 200 else ""),
            },
        }
    return {"success": False, "message": result.get("error", "总结生成失败"), "data": {}}


async def _op_show_channels(_params: dict[str, Any], _request: Request) -> dict[str, Any]:
    config = get_config()
    channels = config.get("channels", [])
    return {
        "success": True,
        "message": f"共 {len(channels)} 个频道",
        "data": {"channels": channels},
    }


async def _op_add_channel(params: dict[str, Any], _request: Request) -> dict[str, Any]:
    channel = normalize_channel_id(str(_require_param(params, "channel")))
    config = get_config()
    channels = config.get("channels", [])
    if channel in channels:
        return {"success": False, "message": f"频道已存在: {channel}", "data": {}}
    channels.append(channel)
    config["channels"] = channels
    write_config(config)
    return {"success": True, "message": f"频道添加成功: {channel}", "data": {"channel": channel}}


async def _op_delete_channel(params: dict[str, Any], _request: Request) -> dict[str, Any]:
    channel = normalize_channel_id(str(_require_param(params, "channel")))
    config = get_config()
    channels = config.get("channels", [])
    if channel not in channels:
        return {"success": False, "message": f"频道不存在: {channel}", "data": {}}
    channels.remove(channel)
    config["channels"] = channels
    for key in ["summary_schedules", "channel_poll_settings", "channel_auto_poll_settings"]:
        settings = config.get(key, {})
        if channel in settings:
            del settings[channel]
            config[key] = settings
    write_config(config)
    return {"success": True, "message": f"频道删除成功: {channel}", "data": {"channel": channel}}


async def _op_pause(_params: dict[str, Any], _request: Request) -> dict[str, Any]:
    from core.config import BOT_STATE_PAUSED, get_bot_state, set_bot_state

    if get_bot_state() == BOT_STATE_PAUSED:
        return {"success": False, "message": "Bot 已处于暂停状态", "data": {}}
    set_bot_state(BOT_STATE_PAUSED)
    return {"success": True, "message": "Bot 已暂停", "data": {}}


async def _op_resume(_params: dict[str, Any], _request: Request) -> dict[str, Any]:
    from core.config import BOT_STATE_RUNNING, get_bot_state, set_bot_state

    if get_bot_state() == BOT_STATE_RUNNING:
        return {"success": False, "message": "Bot 已处于运行状态", "data": {}}
    set_bot_state(BOT_STATE_RUNNING)
    return {"success": True, "message": "Bot 已恢复", "data": {}}


async def _op_restart(_params: dict[str, Any], _request: Request) -> dict[str, Any]:
    import aiofiles

    from core.config import RESTART_FLAG_FILE, trigger_shutdown

    async with aiofiles.open(RESTART_FLAG_FILE, "w") as f:
        await f.write("webui_command_restart")
    trigger_shutdown()
    return {"success": True, "message": "正在重启 Bot...", "data": {}}


async def _op_shutdown(_params: dict[str, Any], _request: Request) -> dict[str, Any]:
    from core.config import trigger_shutdown

    trigger_shutdown()
    return {"success": True, "message": "正在关闭 Bot...", "data": {}}


async def _op_set_log_level(params: dict[str, Any], _request: Request) -> dict[str, Any]:
    import logging as logging_module

    level = str(_require_param(params, "level")).upper()
    if level not in LOG_LEVEL_OPTIONS:
        raise HTTPException(status_code=400, detail=f"无效的日志级别: {level}")
    root_logger = logging_module.getLogger()
    level_value = getattr(logging_module, level)
    root_logger.setLevel(level_value)
    for handler in root_logger.handlers:
        handler.setLevel(level_value)
    config = get_config()
    config["log_level"] = level
    write_config(config)
    return {"success": True, "message": f"日志级别已修改为: {level}", "data": {"level": level}}


async def _op_clear_cache(params: dict[str, Any], _request: Request) -> dict[str, Any]:
    from core.services.cache_manager import get_discussion_cache

    cache = get_discussion_cache()
    channel = params.get("channel")
    if channel:
        deleted = cache.delete(str(channel))
        message = f"已清理频道缓存: {channel}" if deleted else f"频道缓存不存在: {channel}"
        return {"success": True, "message": message, "data": {"cleared": 1 if deleted else 0}}
    cleared = cache.size()
    cache.clear()
    return {
        "success": True,
        "message": f"已清理全部讨论组缓存: {cleared} 条",
        "data": {"cleared": cleared},
    }


async def _op_db_clear(_params: dict[str, Any], _request: Request) -> dict[str, Any]:
    from core.infrastructure.database import get_db_manager

    db = get_db_manager()
    if not hasattr(db, "clear_all_data"):
        raise HTTPException(status_code=503, detail="当前数据库管理器不支持清空操作")
    results = await db.clear_all_data()
    total_deleted = sum(v for v in results.values() if v > 0)
    failed_tables = [table for table, count in results.items() if count < 0]
    return {
        "success": not failed_tables,
        "message": f"数据库清空完成，总删除 {total_deleted} 行",
        "data": {
            "results": results,
            "total_deleted": total_deleted,
            "failed_tables": failed_tables,
        },
    }


async def _op_update(_params: dict[str, Any], _request: Request) -> dict[str, Any]:
    import sys

    from core.config import RESTART_FLAG_FILE, trigger_shutdown
    from core.infrastructure.utils.version_utils import (
        build_frontend,
        git_pull_latest,
        install_dependencies,
    )

    success, message = await git_pull_latest()
    if not success:
        return {"success": False, "message": f"代码更新失败: {message}", "data": {}}

    deps_success, deps_message = await install_dependencies()
    if not deps_success:
        return {
            "success": False,
            "message": f"依赖安装失败: {deps_message}",
            "data": {"git": message},
        }

    frontend_success, frontend_message = await build_frontend()
    if not frontend_success:
        return {"success": False, "message": frontend_message, "data": {"git": message}}

    if sys.platform != "win32":
        import subprocess

        await asyncio.to_thread(
            subprocess.run, ["chmod", "+x", "start.sh"], capture_output=True, timeout=5
        )

    import aiofiles

    async with aiofiles.open(RESTART_FLAG_FILE, "w") as f:
        await f.write("webui_command_update")
    trigger_shutdown()
    return {
        "success": True,
        "message": "更新完成，正在重启 Bot...",
        "data": {"git": message, "deps": deps_message, "frontend": frontend_message},
    }


async def _op_qa_status(_params: dict[str, Any], _request: Request) -> dict[str, Any]:
    from core.system.process_manager import get_qa_bot_process, get_qa_bot_status

    proc = get_qa_bot_process()
    running = proc is not None and proc.poll() is None
    return {
        "success": True,
        "message": "QA Bot 状态已获取",
        "data": {"running": running, "status": get_qa_bot_status()},
    }


async def _op_qa_start(_params: dict[str, Any], _request: Request) -> dict[str, Any]:
    from core.system.process_manager import start_qa_bot

    result = start_qa_bot()
    return {
        "success": bool(result.get("success")),
        "message": result.get("message", ""),
        "data": {"pid": result.get("pid")},
    }


async def _op_qa_stop(_params: dict[str, Any], _request: Request) -> dict[str, Any]:
    from core.system.process_manager import stop_qa_bot

    result = stop_qa_bot()
    return {
        "success": bool(result.get("success")),
        "message": result.get("message", ""),
        "data": {},
    }


async def _op_qa_restart(_params: dict[str, Any], _request: Request) -> dict[str, Any]:
    from core.system.process_manager import restart_qa_bot

    result = restart_qa_bot()
    return {
        "success": bool(result.get("success")),
        "message": result.get("message", ""),
        "data": {"pid": result.get("pid")},
    }


async def _op_qa_runtime(params: dict[str, Any], _request: Request) -> dict[str, Any]:
    from core.system.process_manager import (
        effective_qa_runtime,
        get_qa_bot_status,
        set_qa_bot_runtime,
    )

    target = str(params.get("runtime") or "").strip()
    if not target:
        status = get_qa_bot_status()
        current = status.get("configured_runtime", effective_qa_runtime())
        return {
            "success": True,
            "message": f"当前运行时: {current}（实际运行: {status.get('runtime', '-')}）",
            "data": {"runtime": current, "actual": status.get("runtime")},
        }

    result = set_qa_bot_runtime(target)
    return {
        "success": bool(result.get("success")),
        "message": result.get("message", ""),
        "data": {
            "runtime": result.get("runtime"),
            "restart": result.get("restart"),
        },
    }


async def _op_forwarding_status(_params: dict[str, Any], _request: Request) -> dict[str, Any]:
    config = get_config()
    forwarding = config.get("forwarding", {})
    rules = forwarding.get("rules", [])
    return {
        "success": True,
        "message": f"转发功能{'已启用' if forwarding.get('enabled', False) else '已禁用'}，共 {len(rules)} 条规则",
        "data": {
            "enabled": forwarding.get("enabled", False),
            "rules": rules,
            "rule_count": len(rules),
        },
    }


def _op_forwarding_toggle(enabled: bool) -> OperationHandler:
    async def handler(_params: dict[str, Any], _request: Request) -> dict[str, Any]:
        config = get_config()
        forwarding = _ensure_forwarding(config)
        forwarding["enabled"] = enabled
        write_config(config)
        status_text = "启用" if enabled else "禁用"
        return {
            "success": True,
            "message": f"转发功能已{status_text}",
            "data": {"enabled": enabled},
        }

    return handler


async def _op_forwarding_add_rule(params: dict[str, Any], _request: Request) -> dict[str, Any]:
    source = normalize_channel_id(str(_require_param(params, "source_channel")))
    target = normalize_channel_id(str(_require_param(params, "target_channel")))
    config = get_config()
    forwarding = _ensure_forwarding(config)
    rules = forwarding.get("rules", [])
    for rule in rules:
        if rule.get("source_channel") == source and rule.get("target_channel") == target:
            return {"success": False, "message": "该转发规则已存在", "data": {}}
    rule = {
        "source_channel": source,
        "target_channel": target,
        "keywords": params.get("keywords") or [],
        "blacklist": params.get("blacklist") or [],
        "patterns": params.get("patterns") or [],
        "blacklist_patterns": params.get("blacklist_patterns") or [],
        "copy_mode": bool(params.get("copy_mode", False)),
        "forward_original_only": bool(params.get("forward_original_only", False)),
    }
    if params.get("custom_footer"):
        rule["custom_footer"] = str(params["custom_footer"])
    rules.append(rule)
    forwarding["rules"] = rules
    write_config(config)
    return {
        "success": True,
        "message": f"转发规则已添加: {source} -> {target}",
        "data": {"rule": rule},
    }


async def _op_forwarding_remove_rule(params: dict[str, Any], _request: Request) -> dict[str, Any]:
    config = get_config()
    forwarding = _ensure_forwarding(config)
    rules = forwarding.get("rules", [])

    source = params.get("source_channel") or None
    target = params.get("target_channel") or None
    if source and target:
        normalized_source = normalize_channel_id(str(source))
        normalized_target = normalize_channel_id(str(target))
        for index, rule in enumerate(rules):
            if (
                rule.get("source_channel") == normalized_source
                and rule.get("target_channel") == normalized_target
            ):
                removed = rules.pop(index)
                forwarding["rules"] = rules
                write_config(config)
                return {
                    "success": True,
                    "message": f"转发规则已删除: {normalized_source} -> {normalized_target}",
                    "data": {"removed": removed, "rule_index": index + 1},
                }
        return {
            "success": False,
            "message": f"转发规则不存在: {normalized_source} -> {normalized_target}",
            "data": {},
        }
    # 只提供了源频道或目标频道其中一个，无法唯一定位转发规则。
    if source or target:
        raise HTTPException(status_code=400, detail="按频道删除规则时需同时提供源频道和目标频道")

    try:
        rule_index = int(_require_param(params, "rule_index")) - 1
    except ValueError as e:
        raise HTTPException(status_code=400, detail="规则序号必须是整数") from e

    if rule_index < 0 or rule_index >= len(rules):
        return {"success": False, "message": f"无效的规则序号: {rule_index + 1}", "data": {}}
    removed = rules.pop(rule_index)
    forwarding["rules"] = rules
    write_config(config)
    return {"success": True, "message": "转发规则已删除", "data": {"removed": removed}}


async def _op_userbot_status(_params: dict[str, Any], _request: Request) -> dict[str, Any]:
    from core.settings import get_settings

    settings = get_settings()
    connected = False
    try:
        from core.telegram.client import get_active_client

        client = get_active_client()
        connected = client is not None and client.is_connected()
    except Exception:
        logger.debug("获取 active client 状态失败", exc_info=True)
    return {
        "success": True,
        "message": "UserBot 状态已获取",
        "data": {"enabled": settings.userbot.userbot_enabled, "connected": connected},
    }


async def _op_userbot_join(params: dict[str, Any], _request: Request) -> dict[str, Any]:
    channel = normalize_channel_id(str(_require_param(params, "channel")))
    from core.handlers.userbot_client import get_userbot_client

    userbot = get_userbot_client()
    if not userbot:
        return {"success": False, "message": "UserBot 客户端未初始化", "data": {}}
    result = await userbot.join_channel(channel)
    return {
        "success": bool(result.get("success")),
        "message": result.get("message", ""),
        "data": result,
    }


async def _op_userbot_leave(params: dict[str, Any], _request: Request) -> dict[str, Any]:
    channel = normalize_channel_id(str(_require_param(params, "channel")))
    from core.handlers.userbot_client import get_userbot_client

    userbot = get_userbot_client()
    if not userbot:
        return {"success": False, "message": "UserBot 客户端未初始化", "data": {}}
    result = await userbot.leave_channel(channel)
    return {
        "success": bool(result.get("success")),
        "message": result.get("message", ""),
        "data": result,
    }


async def _op_userbot_list(_params: dict[str, Any], _request: Request) -> dict[str, Any]:
    from core.handlers.userbot_client import get_userbot_client

    userbot = get_userbot_client()
    if not userbot:
        return {"success": False, "message": "UserBot 客户端未初始化", "data": {}}
    if not hasattr(userbot, "list_joined_channels"):
        return {"success": False, "message": "UserBot 不支持频道列表查询", "data": {}}
    result = await userbot.list_joined_channels()
    if result.get("success"):
        channels = result.get("channels", [])
        return {
            "success": True,
            "message": f"共 {result.get('count', len(channels))} 个频道",
            "data": {"channels": channels, "count": result.get("count", len(channels))},
        }
    return {"success": False, "message": result.get("message", "列出频道失败"), "data": result}


OPERATION_HANDLERS: dict[str, OperationHandler] = {
    "summary": _op_summary,
    "showchannels": _op_show_channels,
    "addchannel": _op_add_channel,
    "deletechannel": _op_delete_channel,
    "pause": _op_pause,
    "resume": _op_resume,
    "restart": _op_restart,
    "shutdown": _op_shutdown,
    "setloglevel": _op_set_log_level,
    "clearcache": _op_clear_cache,
    "db_clear": _op_db_clear,
    "update": _op_update,
    "qa_status": _op_qa_status,
    "qa_start": _op_qa_start,
    "qa_stop": _op_qa_stop,
    "qa_restart": _op_qa_restart,
    "qa_stats": _op_qa_status,
    "qa_runtime": _op_qa_runtime,
    "forwarding": _op_forwarding_status,
    "forwarding_enable": _op_forwarding_toggle(True),
    "forwarding_disable": _op_forwarding_toggle(False),
    "forwarding_stats": _op_forwarding_status,
    "forwarding_add_rule": _op_forwarding_add_rule,
    "forwarding_remove_rule": _op_forwarding_remove_rule,
    "userbot_status": _op_userbot_status,
    "userbot_join": _op_userbot_join,
    "userbot_leave": _op_userbot_leave,
    "userbot_list": _op_userbot_list,
}


@router.get("")
async def list_commands():
    """获取 WebUI 命令中心目录。"""
    categories = []
    executable_count = 0
    danger_count = 0
    for group in BOT_COMMANDS:
        items = []
        category = group["category"]
        for command, description in group["commands"]:
            item = _build_command_item(command, description, category)
            if item.executable:
                executable_count += 1
            if item.risk == "danger":
                danger_count += 1
            items.append(item.model_dump())
        categories.append(
            CommandCategory(
                category=category,
                i18n_key=group.get("i18n_key", ""),
                commands=items,
            ).model_dump()
        )

    return {
        "success": True,
        "data": {
            "categories": categories,
            "total": sum(len(group["commands"]) for group in BOT_COMMANDS),
            "executable_count": executable_count,
            "danger_count": danger_count,
            "danger_confirm_text": DANGER_CONFIRM_TEXT,
        },
    }


@router.post("/{operation_id}/execute", response_model=CommandExecuteResponse)
async def execute_command(operation_id: str, request_data: CommandExecuteRequest, request: Request):
    """执行结构化命令操作。"""
    command_meta = COMMAND_METADATA.get(operation_id, _default_metadata(operation_id))
    risk = command_meta.get("risk", "normal")
    if risk == "danger" and (
        not request_data.confirm or request_data.confirm_text != DANGER_CONFIRM_TEXT
    ):
        return {
            "success": False,
            "message": f"危险操作需要二次确认，请输入 {DANGER_CONFIRM_TEXT}",
            "data": {"confirm_required": True, "confirm_text": DANGER_CONFIRM_TEXT},
        }

    handler = OPERATION_HANDLERS.get(operation_id)
    if handler is None:
        return {"success": False, "message": f"命令 {operation_id} 暂未接入结构化执行", "data": {}}

    started_at = time.perf_counter()
    actor = _actor_from_request(request)
    try:
        result = await handler(request_data.params, request)
        success = bool(result.get("success"))
        message = str(result.get("message", ""))
        await record_system_audit(
            action=f"command.{operation_id}",
            actor=actor,
            target=str(
                request_data.params.get("channel")
                or request_data.params.get("source_channel")
                or ""
            ),
            params_summary=_summarize_params(request_data.params),
            success=success,
            message=message,
            duration_ms=_audit_duration(started_at),
        )
        return result
    except HTTPException as e:
        await record_system_audit(
            action=f"command.{operation_id}",
            actor=actor,
            params_summary=_summarize_params(request_data.params),
            success=False,
            message=str(e.detail),
            duration_ms=_audit_duration(started_at),
        )
        raise
    except Exception as e:
        message = f"执行命令失败: {type(e).__name__}: {e}"
        logger.error(message, exc_info=True)
        await record_system_audit(
            action=f"command.{operation_id}",
            actor=actor,
            params_summary=_summarize_params(request_data.params),
            success=False,
            message=message,
            duration_ms=_audit_duration(started_at),
        )
        raise HTTPException(status_code=500, detail=message) from e


def _summarize_params(params: dict[str, Any]) -> str:
    """生成参数摘要，避免记录敏感长文本。"""
    safe_params = {}
    for key, value in params.items():
        normalized_key = key.lower()
        if normalized_key in SENSITIVE_PARAM_NAMES or normalized_key.endswith(
            ("_token", "_secret", "_password")
        ):
            safe_params[key] = "***"
        elif isinstance(value, str) and len(value) > 120:
            safe_params[key] = value[:120] + "..."
        else:
            safe_params[key] = value
    return str(safe_params)
