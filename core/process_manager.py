# Copyright 2026 Sakura-Bot
#
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可，
# 并附加非商业使用限制条款。
#
# - 署名：必须提供本项目的原始来源链接
# - 非商业：禁止任何商业用途和分发
# - 相同方式共享：衍生作品必须采用相同的许可证
#
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
# 许可证全文：参见 LICENSE 文件

"""
进程管理模块 - 负责管理问答Bot子进程的生命周期
"""

import logging
import os
import subprocess
import sys
import time

from .i18n import get_text

logger = logging.getLogger(__name__)

# 问答Bot进程引用
_qa_bot_process = None
_qa_bot_start_time = None


def start_qa_bot():
    """在后台启动问答Bot

    Returns:
        dict: 启动结果 {'success': bool, 'message': str, 'pid': int or None}
    """
    global _qa_bot_process, _qa_bot_start_time
    try:
        # 检查是否已经运行
        if _qa_bot_process:
            return {
                "success": False,
                "message": get_text("qabot.already_running", pid=_qa_bot_process.pid),
                "pid": _qa_bot_process.pid,
            }

        # 检查是否启用问答Bot
        qa_bot_enabled = os.getenv("QA_BOT_ENABLED", "True").lower() == "true"
        qa_bot_token = os.getenv("QA_BOT_TOKEN", "")

        if not qa_bot_enabled:
            logger.info("问答Bot未启用 (QA_BOT_ENABLED=False)")
            return {"success": False, "message": get_text("qabot.not_enabled"), "pid": None}

        if not qa_bot_token:
            logger.warning("未配置QA_BOT_TOKEN，跳过启动问答Bot")
            return {
                "success": False,
                "message": get_text("qabot.token_not_configured"),
                "pid": None,
            }

        logger.info("正在启动问答Bot...")
        # 使用subprocess在后台运行qa_bot.py
        _qa_bot_process = subprocess.Popen(
            [sys.executable, "qa_bot.py"], cwd=os.path.dirname(os.path.abspath(sys.argv[0]))
        )
        _qa_bot_start_time = time.time()

        logger.info(f"问答Bot已启动 (PID: {_qa_bot_process.pid})")
        return {
            "success": True,
            "message": get_text("qabot.started", pid=_qa_bot_process.pid),
            "pid": _qa_bot_process.pid,
        }

    except Exception as e:
        logger.error(f"启动问答Bot失败: {type(e).__name__}: {e}", exc_info=True)
        return {
            "success": False,
            "message": get_text(
                "qabot.start_success", message=f"Startup failed: {type(e).__name__}: {e}"
            ),
            "pid": None,
        }


def stop_qa_bot():
    """停止问答Bot

    Returns:
        dict: 停止结果 {'success': bool, 'message': str}
    """
    global _qa_bot_process, _qa_bot_start_time
    if _qa_bot_process:
        try:
            logger.info("正在停止问答Bot...")
            _qa_bot_process.terminate()
            _qa_bot_process.wait(timeout=5)
            logger.info("问答Bot已停止")
            _qa_bot_start_time = None
            return {"success": True, "message": get_text("qabot.stopped", pid=_qa_bot_process.pid)}
        except Exception as e:
            logger.error(f"停止问答Bot失败: {type(e).__name__}: {e}")
            try:
                _qa_bot_process.kill()
                _qa_bot_start_time = None
                return {"success": True, "message": get_text("qabot.force_stopped")}
            except Exception:
                return {"success": False, "message": f"Stop failed: {type(e).__name__}: {e}"}
        finally:
            _qa_bot_process = None
    else:
        logger.debug("问答Bot未运行，无需停止")
        return {"success": True, "message": get_text("qabot.not_running_short")}


def get_qa_bot_process():
    """获取当前问答Bot进程引用"""
    return _qa_bot_process


def restart_qa_bot():
    """重启问答Bot

    Returns:
        dict: 重启结果 {'success': bool, 'message': str, 'pid': int or None}
    """
    logger.info("正在重启问答Bot...")

    # 先停止
    stop_result = stop_qa_bot()
    if not stop_result["success"]:
        return {"success": False, "message": f"Stop failed: {stop_result['message']}", "pid": None}

    # 等待一小段时间确保进程完全停止
    time.sleep(1)

    # 再启动
    start_result = start_qa_bot()
    if start_result["success"]:
        return {
            "success": True,
            "message": get_text("qabot.restarted", pid=start_result["pid"]),
            "pid": start_result["pid"],
        }
    else:
        return {
            "success": False,
            "message": f"Restart failed: {start_result['message']}",
            "pid": None,
        }


def get_qa_bot_status():
    """获取问答Bot运行状态

    Returns:
        dict: 状态信息 {
            'running': bool,
            'pid': int or None,
            'start_time': float or None,
            'uptime_seconds': float or None,
            'enabled': bool,
            'token_configured': bool
        }
    """
    global _qa_bot_process, _qa_bot_start_time

    # 检查配置
    qa_bot_enabled = os.getenv("QA_BOT_ENABLED", "True").lower() == "true"
    qa_bot_token = os.getenv("QA_BOT_TOKEN", "")

    # 检查进程是否存活
    is_running = False
    pid = None
    uptime = None

    if _qa_bot_process:
        # 检查进程是否仍在运行
        poll_result = _qa_bot_process.poll()
        if poll_result is None:  # None表示进程仍在运行
            is_running = True
            pid = _qa_bot_process.pid
            if _qa_bot_start_time:
                uptime = time.time() - _qa_bot_start_time
        else:
            # 进程已退出，清理引用
            logger.warning(f"问答Bot进程已退出 (退出码: {poll_result})")
            _qa_bot_process = None
            _qa_bot_start_time = None

    return {
        "running": is_running,
        "pid": pid,
        "start_time": _qa_bot_start_time,
        "uptime_seconds": uptime,
        "enabled": qa_bot_enabled,
        "token_configured": bool(qa_bot_token),
    }


def check_qa_bot_health():
    """检查问答Bot健康状态（用于自动重启）

    Returns:
        tuple: (is_healthy: bool, should_restart: bool, message: str)
    """
    status = get_qa_bot_status()

    # 如果未启用，不需要检查
    if not status["enabled"]:
        return True, False, get_text("qabot.not_enabled")

    # 如果没有配置token，无法启动
    if not status["token_configured"]:
        return False, False, get_text("qabot.token_not_configured")

    # 如果进程正在运行，健康
    if status["running"]:
        uptime_str = f"{status['uptime_seconds']:.0f}" if status["uptime_seconds"] else "0"
        return True, False, get_text("qabot.running_normal", pid=status["pid"], uptime=uptime_str)

    # 进程未运行但应该运行，需要重启
    return False, True, get_text("qabot.process_not_running")


def format_uptime(seconds):
    """格式化运行时间

    Args:
        seconds: 秒数

    Returns:
        str: 格式化的时间字符串
    """
    if seconds is None:
        return "未知"

    hours = int(seconds // 3600)
    minutes = int((seconds % 3600) // 60)
    secs = int(seconds % 60)

    if hours > 0:
        return f"{hours}小时{minutes}分钟{secs}秒"
    elif minutes > 0:
        return f"{minutes}分钟{secs}秒"
    else:
        return f"{secs}秒"
