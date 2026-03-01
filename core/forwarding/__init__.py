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
频道消息转发功能模块

提供Telegram频道消息转发功能，支持：
- 关键词过滤
- AI处理（改写、翻译等）
- 消息去重
- 统计信息
"""

from .filters import should_forward_by_keywords
from .forwarding_handler import ForwardingHandler, get_forwarding_handler, set_forwarding_handler

__all__ = [
    "should_forward_by_keywords",
    "ForwardingHandler",
    "get_forwarding_handler",
    "set_forwarding_handler",
]
