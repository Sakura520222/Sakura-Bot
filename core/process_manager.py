# -*- coding: utf-8 -*-
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
import sys
import subprocess

logger = logging.getLogger(__name__)

# 问答Bot进程引用
_qa_bot_process = None


def start_qa_bot():
    """在后台启动问答Bot"""
    global _qa_bot_process
    try:
        # 检查是否启用问答Bot
        qa_bot_enabled = os.getenv("QA_BOT_ENABLED", "True").lower() == "true"
        qa_bot_token = os.getenv("QA_BOT_TOKEN", "")

        if not qa_bot_enabled:
            logger.info("问答Bot未启用 (QA_BOT_ENABLED=False)")
            return

        if not qa_bot_token:
            logger.warning("未配置QA_BOT_TOKEN，跳过启动问答Bot")
            return

        logger.info("正在启动问答Bot...")
        # 使用subprocess在后台运行qa_bot.py
        _qa_bot_process = subprocess.Popen(
            [sys.executable, "qa_bot.py"],
            cwd=os.path.dirname(os.path.abspath(sys.argv[0]))
        )
        logger.info(f"问答Bot已启动 (PID: {_qa_bot_process.pid})")

    except Exception as e:
        logger.error(f"启动问答Bot失败: {type(e).__name__}: {e}", exc_info=True)


def stop_qa_bot():
    """停止问答Bot"""
    global _qa_bot_process
    if _qa_bot_process:
        try:
            logger.info("正在停止问答Bot...")
            _qa_bot_process.terminate()
            _qa_bot_process.wait(timeout=5)
            logger.info("问答Bot已停止")
        except Exception as e:
            logger.error(f"停止问答Bot失败: {type(e).__name__}: {e}")
            try:
                _qa_bot_process.kill()
            except Exception:
                pass
        finally:
            _qa_bot_process = None
    else:
        logger.debug("问答Bot未运行，无需停止")


def get_qa_bot_process():
    """获取当前问答Bot进程引用"""
    return _qa_bot_process
