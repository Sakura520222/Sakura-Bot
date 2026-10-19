# Copyright 2026 Sakura-Bot
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
"""Sakura Guard：仅复用主 Bot 的可选群组管理服务。"""

_service = None


def get_guard():
    """返回已初始化的主进程服务；不会创建另一套客户端。"""
    return _service


def set_guard(service):
    global _service
    _service = service
