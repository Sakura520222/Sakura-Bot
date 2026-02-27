#!/usr/bin/env python3
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
Sakura 问答Bot - 独立的智能问答助手
基于历史总结回答自然语言查询
"""

import logging
import os
import sys
import time

from telegram import BotCommand, Update
from telegram.ext import (
    Application,
    CommandHandler,
    ContextTypes,
    MessageHandler,
    filters,
)

# 添加项目根目录到Python路径
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

from core.config import get_qa_bot_persona
from core.conversation_manager import get_conversation_manager
from core.qa_engine_v3 import get_qa_engine_v3
from core.qa_user_system import get_qa_user_system
from core.quota_manager import get_quota_manager


# 配置日志 - 添加[QA]前缀以便区分
class QAFormatter(logging.Formatter):
    """自定义日志格式器，添加[QA]前缀"""

    def format(self, record):
        # 在消息前添加 [QA] 前缀
        if record.msg and isinstance(record.msg, str):
            record.msg = f"[QA] {record.msg}"
        return super().format(record)


# 配置基础日志 - 使用 WARNING 作为全局级别，避免第三方库输出过多日志
logging.basicConfig(
    level=logging.WARNING, format="%(asctime)s - %(name)s - %(levelname)s - %(message)s"
)

# 为 QABot 创建独立的 logger 并设置 INFO 级别
logger = logging.getLogger(__name__)
logger.setLevel(logging.INFO)

# 为 QABot 的 logger 添加专用 handler
if not logger.handlers:
    handler = logging.StreamHandler()
    handler.setFormatter(QAFormatter("%(asctime)s - %(name)s - %(levelname)s - %(message)s"))
    logger.addHandler(handler)

# 确保日志不传播到 root logger（避免重复输出）
logger.propagate = False

# 抑制第三方库的 INFO 日志输出
logging.getLogger("httpx").setLevel(logging.WARNING)
logging.getLogger("apscheduler").setLevel(logging.WARNING)


# 获取配置
QA_BOT_TOKEN = os.getenv("QA_BOT_TOKEN")
QA_BOT_ENABLED = os.getenv("QA_BOT_ENABLED", "True").lower() == "true"

if not QA_BOT_TOKEN:
    logger.error("未设置QA_BOT_TOKEN环境变量")
    logger.error("请在.env文件中配置: QA_BOT_TOKEN=your_bot_token")
    sys.exit(1)

if not QA_BOT_ENABLED:
    logger.warning("问答Bot未启用 (QA_BOT_ENABLED=False)")
    sys.exit(0)


class QABot:
    """问答Bot主类"""

    def __init__(self):
        """初始化Bot"""
        self.quota_manager = get_quota_manager()
        self.qa_engine = get_qa_engine_v3()
        self.conversation_mgr = get_conversation_manager()
        self.user_system = get_qa_user_system()
        self.application = None

        logger.info("问答Bot初始化完成（v3.0.0向量搜索版本 + 多轮对话支持 + 用户系统）")

    async def initialize_database(self):
        """初始化数据库连接"""
        try:
            from core.database import get_db_manager

            db = get_db_manager()

            # 如果是MySQL数据库，需要初始化连接池
            if hasattr(db, "init_database") and hasattr(db, "pool") and db.pool is None:
                logger.info("正在初始化MySQL数据库连接池...")
                await db.init_database()

                # 检查连接池是否成功创建
                if db.pool is None:
                    logger.error("数据库连接池初始化失败，pool 仍为 None")
                    raise RuntimeError("MySQL连接池创建失败")

                logger.info("MySQL数据库连接池初始化完成")
        except Exception as e:
            logger.error(f"初始化数据库失败: {type(e).__name__}: {e}", exc_info=True)
            raise

    async def start(self, update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
        """处理/start命令"""
        # 自动注册用户
        await self.user_system.register_user(
            update.effective_user.id,
            update.effective_user.username,
            update.effective_user.first_name,
        )

        welcome_message = """🤖 **你好！我是智能资讯助手。**

我可以帮你从频道的历史记录中快速查找信息和知识。
无论是最近的讨论，还是过去的精华总结，只要你提问，我就能为你找到答案。

🌟 **你可以试着对我提问：**
• "最近频道里发生了什么新鲜事？"
• "帮我分析一下关于某个关键词的讨论。"
• "查看本周的精华总结。"
• "今天有什么更新？"

💡 **小提示：**
我会记住我们的对话上下文（30分钟内），所以你可以用代词追问，比如"那它呢？"、"这个怎么样？"。"""

        await update.message.reply_text(welcome_message, parse_mode="Markdown")

    async def help_command(self, update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
        """处理/help命令"""
        help_text = """📚 **使用帮助**

*基础命令*
• `/start` - 查看欢迎信息
• `/help` - 显示这份帮助文档
• `/status` - 查看使用配额和会话状态
• `/clear` - 清除对话记忆，重新开始
• `/view_persona` - 查看当前助手人格设定

*订阅管理*
• `/listchannels` - 列出可订阅的频道
• `/subscribe` - 订阅频道总结推送
• `/unsubscribe` - 取消频道订阅
• `/mysubscriptions` - 查看我的订阅列表
• `/request_summary` - 请求生成频道总结

*自然语言查询*
直接发送问题，例如：
• "上周发生了什么？"
• "最近有什么技术讨论？"
• "今天有什么更新？"
• "关于特定关键词的内容"

*多轮对话*
• 我会记住我们的对话上下文（30分钟内）
• 你可以使用代词追问："那它呢？"、"这个怎么样？"
• 对话超时后会自动开始新会话

*时间关键词*
• 今天、昨天、前天
• 本周、上周
• 本月、上月
• 最近7天、最近30天

*功能特点*
✅ 智能意图识别
✅ 上下文感知（多轮对话）
✅ 频道画像注入
✅ 多频道综合查询
✅ 频道订阅推送
✅ 总结请求功能

⚠️ *注意*
请尽量提出与频道总结相关的问题。过度偏离的查询可能会被拦截。"""

        await update.message.reply_text(help_text, parse_mode="Markdown")

    async def status_command(self, update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
        """处理/status命令"""
        user_id = update.effective_user.id
        status_info = await self.quota_manager.get_usage_status(user_id)

        # 构建配额状态文本
        if status_info.get("is_admin"):
            quota_text = """🌟 <b>管理员状态</b>

你拥有无限制访问的特权。

📊 今日总使用：{}次""".format(status_info.get("total_used_today", 0))
        else:
            quota_text = """📊 <b>配额状态</b>

• 今日已使用: {used} 次
• 剩余次数: {remaining} 次""".format(
                used=status_info.get("used_today", 0), remaining=status_info.get("remaining", 50)
            )

        # 获取会话信息
        session_info = await self.conversation_mgr.get_session_info(user_id)

        session_text = ""
        if session_info:
            is_active = session_info.get("is_active", False)
            status_emoji = "🟢 活跃中" if is_active else "⚪ 已超时"
            # 使用代码块显示会话ID，避免Markdown解析问题
            session_id_preview = session_info["session_id"][:8]
            session_text = f"""

🧠 <b>当前会话状态</b>
• 会话ID: <code>{session_id_preview}...</code>
• 消息数: {session_info["message_count"]} 条
• 状态: {status_emoji}"""

        message = f"""📊 <b>系统状态</b>

{quota_text}{session_text}

📅 重置时间：每日 00:00 (UTC)"""

        # 使用HTML模式以避免Markdown解析错误
        await update.message.reply_text(message, parse_mode="HTML")

    async def clear_command(self, update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
        """处理/clear命令 - 清除对话历史"""
        user_id = update.effective_user.id

        # 清除所有对话历史
        deleted_count = await self.conversation_mgr.clear_user_history(user_id)

        message = f"""🗑️ **对话记忆已清除**

已清除 **{deleted_count}** 条对话记录。

现在，我们的对话是全新的开始。有什么可以帮你的吗？"""

        await update.message.reply_text(message, parse_mode="Markdown")

    async def view_persona_command(
        self, update: Update, context: ContextTypes.DEFAULT_TYPE
    ) -> None:
        """处理/view_persona命令 - 查看当前人格设定"""
        persona = get_qa_bot_persona()

        # 限制显示长度，避免消息过长
        max_length = 3500
        if len(persona) > max_length:
            persona_preview = persona[:max_length] + "\n\n... (内容过长，已截断)"
        else:
            persona_preview = persona

        message = f"""📋 **当前助手人格设定**

```
{persona_preview}
```

💡 **提示**：
人格设定可通过以下方式修改：
1. 修改 `data/qa_persona.txt` 文件
2. 在 `data/config.json` 中设置 `qa_bot_persona` 字段
3. 在 `.env` 文件中设置 `QA_BOT_PERSONA` 环境变量

修改后需重启Bot生效。"""

        await update.message.reply_text(message, parse_mode="Markdown")

    async def list_channels_command(
        self, update: Update, context: ContextTypes.DEFAULT_TYPE
    ) -> None:
        """处理/listchannels命令 - 列出可订阅频道"""
        user_id = update.effective_user.id

        # 自动注册用户
        await self.user_system.register_user(
            user_id, update.effective_user.username, update.effective_user.first_name
        )

        # 获取频道列表
        channels = await self.user_system.get_available_channels()
        message = self.user_system.format_channels_list(channels)

        await update.message.reply_text(message, parse_mode="Markdown")

    async def subscribe_command(self, update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
        """处理/subscribe命令 - 订阅频道"""
        user_id = update.effective_user.id

        # 自动注册用户
        await self.user_system.register_user(
            user_id, update.effective_user.username, update.effective_user.first_name
        )

        # 检查参数
        if not context.args or len(context.args) < 1:
            message = """📖 **订阅频道**

使用方法:
`/subscribe <频道链接>`

示例:
`/subscribe https://t.me/channel_name`

💡 使用 `/listchannels` 查看可订阅频道"""
            await update.message.reply_text(message, parse_mode="Markdown")
            return

        channel_url = context.args[0]

        # 获取频道列表，查找频道名称
        channels = await self.user_system.get_available_channels()
        channel_name = None
        for ch in channels:
            if ch.get("channel_id") == channel_url:
                channel_name = ch.get("channel_name")
                break

        if not channel_name:
            # 从URL中提取频道名作为备用
            channel_name = channel_url.split("/")[-1]

        # 添加订阅
        result = await self.user_system.add_subscription(user_id, channel_url, channel_name)
        await update.message.reply_text(result["message"], parse_mode="Markdown")

    async def unsubscribe_command(self, update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
        """处理/unsubscribe命令 - 取消订阅"""
        user_id = update.effective_user.id

        # 检查参数
        if not context.args or len(context.args) < 1:
            # 如果没有参数，显示订阅列表让用户选择
            subscriptions = await self.user_system.get_user_subscriptions(user_id)
            if not subscriptions:
                message = "您还没有订阅任何频道。"
            else:
                lines = ["📚 **取消订阅**\n\n请使用频道链接取消订阅：\n"]
                for sub in subscriptions:
                    lines.append(f"• {sub.get('channel_name', sub.get('channel_id'))}")
                    lines.append(f"  `{sub.get('channel_id')}`")
                    lines.append("")
                lines.append("使用方法: `/unsubscribe <频道链接>`")
                message = "\n".join(lines)

            await update.message.reply_text(message, parse_mode="Markdown")
            return

        channel_url = context.args[0]
        result = await self.user_system.remove_subscription(user_id, channel_url)
        await update.message.reply_text(result["message"], parse_mode="Markdown")

    async def my_subscriptions_command(
        self, update: Update, context: ContextTypes.DEFAULT_TYPE
    ) -> None:
        """处理/mysubscriptions命令 - 查看我的订阅"""
        user_id = update.effective_user.id

        subscriptions = await self.user_system.get_user_subscriptions(user_id)
        message = self.user_system.format_subscriptions_list(subscriptions)

        await update.message.reply_text(message, parse_mode="Markdown")

    async def request_summary_command(
        self, update: Update, context: ContextTypes.DEFAULT_TYPE
    ) -> None:
        """处理/request_summary命令 - 请求生成总结"""
        user_id = update.effective_user.id

        # 自动注册用户
        await self.user_system.register_user(
            user_id, update.effective_user.username, update.effective_user.first_name
        )

        # 检查参数
        if not context.args or len(context.args) < 1:
            message = """📝 <b>请求生成总结</b>

使用方法:
<code>/request_summary <频道链接></code>

此命令会向管理员提交请求，请管理员为指定频道生成总结。

💡 使用 <code>/listchannels</code> 查看可用的频道。"""
            await update.message.reply_text(message, parse_mode="HTML")
            return

        channel_url = context.args[0]

        # 获取频道名称
        channels = await self.user_system.get_available_channels()
        channel_name = None
        for ch in channels:
            if ch.get("channel_id") == channel_url:
                channel_name = ch.get("channel_name")
                break

        if not channel_name:
            channel_name = channel_url.split("/")[-1]

        # 创建请求（异步调用）
        result = await self.user_system.create_summary_request(user_id, channel_url, channel_name)
        await update.message.reply_text(result["message"], parse_mode="HTML")

    async def handle_message(self, update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
        """处理用户消息（流式输出 - 单条消息动态编辑）"""
        # 防御性检查：忽略非用户消息（如频道事件、系统消息）
        if not update.effective_user or not update.message:
            return

        user_id = update.effective_user.id
        query = update.message.text

        if not query or not query.strip():
            return

        # 自动注册用户（确保数据库中有用户信息）
        await self.user_system.register_user(
            user_id, update.effective_user.username, update.effective_user.first_name
        )

        logger.info(f"收到查询: user_id={user_id}, query={query}")

        try:
            # 1. 检查配额
            quota_check = await self.quota_manager.check_quota(user_id)
            if not quota_check.get("allowed", False):
                await update.message.reply_text(quota_check.get("message", "配额不足"))
                return

            # 2. 发送初始占位消息
            placeholder = await update.message.reply_text("🔍 正在检索相关记录...")

            # 3. 流式处理并实时编辑消息
            await self._stream_and_edit(
                placeholder=placeholder, query=query, user_id=user_id, quota_check=quota_check
            )

        except Exception as e:
            logger.error(f"处理消息失败: {type(e).__name__}: {e}", exc_info=True)
            try:
                await update.message.reply_text("❌ 抱歉，处理查询时出错。请稍后再试。")
            except Exception:
                pass

    async def _stream_and_edit(
        self, placeholder, query: str, user_id: int, quota_check: dict
    ) -> None:
        """
        流式接收 QA 引擎输出，并实时编辑 Telegram 消息。

        策略：
        - 流式阶段：以纯文本实时更新（避免不完整 Markdown 报错），
          每积累 STREAM_EDIT_THRESHOLD 字符或超过 STREAM_EDIT_INTERVAL 秒则编辑一次。
        - 完成阶段：用完整文本做最终编辑，并尝试启用 Markdown 格式。
        - 如果单条消息超过 4096 字符，则继续追加新消息。
        """
        # ── 可调参数 ─────────────────────────────────────────────────────────
        # 每积累多少字符触发一次编辑
        STREAM_EDIT_THRESHOLD = 80
        # 距离上次编辑超过多少秒也触发一次编辑（防止长时间无更新）
        STREAM_EDIT_INTERVAL = 1.5
        # Telegram 单条消息最大字符数
        MAX_MSG_LEN = 4096
        # ─────────────────────────────────────────────────────────────────────

        accumulated = ""  # 当前消息已累积的完整文本
        last_edit_len = 0  # 上次编辑时的文本长度
        last_edit_time = time.monotonic()
        is_new_session = False
        current_msg = placeholder  # 当前正在编辑的消息对象
        extra_msgs = []  # 超长时追加的额外消息

        async def _safe_edit(msg, text: str, use_markdown: bool = False):
            """安全地编辑消息，失败时静默处理。"""
            if not text.strip():
                return
            try:
                if use_markdown:
                    await msg.edit_text(text, parse_mode="Markdown")
                else:
                    await msg.edit_text(text)
            except Exception as e:
                err = str(e)
                # 内容与当前内容相同时忽略
                if "Message is not modified" in err:
                    return
                if use_markdown:
                    # Markdown 失败，尝试修复
                    try:
                        fixed = self._fix_markdown(text)
                        await msg.edit_text(fixed, parse_mode="Markdown")
                    except Exception:
                        try:
                            await msg.edit_text(text)
                        except Exception:
                            pass
                else:
                    logger.debug(f"编辑消息失败（已忽略）: {e}")

        async def _flush_edit(final: bool = False):
            """将 accumulated 写入当前消息（或追加新消息）。"""
            nonlocal last_edit_len, last_edit_time, current_msg

            text = accumulated
            if not text.strip():
                return

            # 超过单条消息长度限制时，把超出部分作为新消息追加
            if len(text) > MAX_MSG_LEN:
                # 保留当前消息不变（已是完整内容），超出部分作为新消息
                # 此处只在 final 阶段处理，避免流式中频繁拆分
                if final:
                    parts = self._split_long_message(text, MAX_MSG_LEN)
                    # 编辑当前消息为第一部分
                    await _safe_edit(current_msg, parts[0], use_markdown=True)
                    # 追加其余部分
                    for part in parts[1:]:
                        try:
                            new_msg = await current_msg.reply_text(part, parse_mode="Markdown")
                            extra_msgs.append(new_msg)
                            current_msg = new_msg
                        except Exception:
                            try:
                                new_msg = await current_msg.reply_text(
                                    self._fix_markdown(part), parse_mode="Markdown"
                                )
                                extra_msgs.append(new_msg)
                                current_msg = new_msg
                            except Exception:
                                pass
                else:
                    # 流式阶段：截断显示，末尾加省略号
                    truncated = text[: MAX_MSG_LEN - 30] + "\n\n_（内容生成中…）_"
                    await _safe_edit(current_msg, truncated)
            else:
                await _safe_edit(current_msg, text, use_markdown=final)

            last_edit_len = len(accumulated)
            last_edit_time = time.monotonic()

        try:
            async for chunk in self.qa_engine.process_query_stream(query, user_id):
                # ── 处理特殊控制标记 ─────────────────────────────────────────
                if chunk == "__DONE__":
                    break

                if chunk == "__NEW_SESSION__":
                    is_new_session = True
                    continue

                if chunk.startswith("__ERROR__:"):
                    error_msg = chunk[len("__ERROR__:") :]
                    await _safe_edit(current_msg, error_msg)
                    return

                # ── 首个真实文本块：更新占位消息状态 ────────────────────────
                if not accumulated:
                    # 将占位符从"检索中"更新为新会话提示或开始生成
                    if is_new_session:
                        prefix = "🍃 _开始新的对话。_\n\n"
                        accumulated = prefix + chunk
                    else:
                        accumulated += chunk
                    await _safe_edit(current_msg, accumulated + " ✍️")
                    last_edit_len = len(accumulated)
                    last_edit_time = time.monotonic()
                    continue

                # ── 累积文本 ─────────────────────────────────────────────────
                accumulated += chunk

                # ── 判断是否需要触发编辑 ─────────────────────────────────────
                chars_since_edit = len(accumulated) - last_edit_len
                time_since_edit = time.monotonic() - last_edit_time
                should_edit = (
                    chars_since_edit >= STREAM_EDIT_THRESHOLD
                    or time_since_edit >= STREAM_EDIT_INTERVAL
                )

                if should_edit:
                    # 流式阶段：纯文本 + 光标提示
                    display = accumulated + " ✍️"
                    await _safe_edit(current_msg, display)
                    last_edit_len = len(accumulated)
                    last_edit_time = time.monotonic()

        except Exception as e:
            logger.error(f"流式接收失败: {type(e).__name__}: {e}", exc_info=True)
            if not accumulated:
                await _safe_edit(current_msg, "❌ 抱歉，处理查询时出错。请稍后再试。")
                return

        # ── 生成完成：最终编辑，追加配额提示并启用 Markdown ─────────────────
        if not accumulated.strip():
            await _safe_edit(current_msg, "❌ 未能获取回答，请稍后再试。")
            return

        # 追加配额提示（剩余次数不足时）
        if not quota_check.get("is_admin", False):
            remaining = quota_check.get("remaining", 99)
            if remaining <= 1:
                accumulated += f"\n\n_💡 提示：今日剩余查询次数：{remaining} 次_"

        # 最终写入（启用 Markdown）
        await _flush_edit(final=True)
        logger.info(f"流式回答完成，总长度: {len(accumulated)} 字符")

    def _split_long_message(self, text: str, max_length: int = 4096) -> list:
        """将长消息分割为多个部分"""
        if len(text) <= max_length:
            return [text]

        parts = []
        current_part = ""
        paragraphs = text.split("\n\n")

        for para in paragraphs:
            if len(current_part) + len(para) + 2 <= max_length:
                current_part += para + "\n\n"
            else:
                if current_part:
                    parts.append(current_part.strip())
                current_part = para + "\n\n"

        if current_part:
            parts.append(current_part.strip())

        return parts

    async def _send_with_fallback(self, message, text: str):
        """发送消息，强制使用Markdown格式

        如果AI生成的Markdown有语法错误，进行简单修复
        """
        # 直接尝试发送Markdown
        try:
            await message.reply_text(text, parse_mode="Markdown")
        except Exception as e:
            logger.warning(f"Markdown发送失败: {e}, 尝试修复格式")
            # 尝试修复常见的Markdown格式错误
            fixed_text = self._fix_markdown(text)
            try:
                await message.reply_text(fixed_text, parse_mode="Markdown")
            except Exception as e2:
                logger.error(f"Markdown修复后仍然失败: {e2}, 使用纯文本")
                # 最后的保底方案
                await message.reply_text(text)

    def _fix_markdown(self, text: str) -> str:
        """修复常见的Markdown格式错误

        策略：通过统计各标记符号出现次数，如为奇数则在末尾补全一个，
        避免暴力正则替换导致的文本错误。
        """
        import re

        lines = text.split("\n")
        fixed_lines = []
        for line in lines:
            # 统计行内未在代码块中的 ** 对数（粗体）
            # 用简单方法：计算 ** 的出现次数，若为奇数则补全
            bold_count = len(re.findall(r"\*\*", line))
            if bold_count % 2 == 1:
                line = line + "**"

            # 统计行内单个 * 的数量（斜体，排除 **）
            # 替换掉 ** 后再统计剩余 *
            stripped = re.sub(r"\*\*", "", line)
            italic_count = stripped.count("*")
            if italic_count % 2 == 1:
                line = line + "*"

            # 统计反引号（代码）
            backtick_count = line.count("`")
            if backtick_count % 2 == 1:
                line = line + "`"

            fixed_lines.append(line)

        return "\n".join(fixed_lines)

    def run(self):
        """运行Bot"""
        logger.info("启动问答Bot...")

        # 创建应用
        self.application = Application.builder().token(QA_BOT_TOKEN).build()

        # 设置命令菜单注册回调
        async def register_commands(application):
            """注册命令菜单和初始化数据库"""
            # 初始化数据库连接
            await self.initialize_database()

            logger.info("注册问答Bot命令菜单...")
            commands = [
                BotCommand("start", "查看欢迎信息"),
                BotCommand("help", "显示使用帮助"),
                BotCommand("status", "查看使用配额和会话状态"),
                BotCommand("clear", "清除对话记忆"),
                BotCommand("view_persona", "查看当前助手人格设定"),
                BotCommand("listchannels", "列出可订阅的频道"),
                BotCommand("subscribe", "订阅频道总结推送"),
                BotCommand("unsubscribe", "取消频道订阅"),
                BotCommand("mysubscriptions", "查看我的订阅列表"),
                BotCommand("request_summary", "请求生成频道总结"),
            ]

            try:
                await application.bot.set_my_commands(commands)
                logger.info(f"问答Bot命令菜单注册完成，共 {len(commands)} 个命令")
            except Exception as e:
                logger.error(f"注册命令菜单失败: {type(e).__name__}: {e}")

        # 将命令注册添加到post_init回调
        self.application.post_init = register_commands

        # 注册处理器
        self.application.add_handler(CommandHandler("start", self.start))
        self.application.add_handler(CommandHandler("help", self.help_command))
        self.application.add_handler(CommandHandler("status", self.status_command))
        self.application.add_handler(CommandHandler("clear", self.clear_command))
        self.application.add_handler(CommandHandler("view_persona", self.view_persona_command))

        # 订阅管理命令
        self.application.add_handler(CommandHandler("listchannels", self.list_channels_command))
        self.application.add_handler(CommandHandler("subscribe", self.subscribe_command))
        self.application.add_handler(CommandHandler("unsubscribe", self.unsubscribe_command))
        self.application.add_handler(
            CommandHandler("mysubscriptions", self.my_subscriptions_command)
        )
        self.application.add_handler(
            CommandHandler("request_summary", self.request_summary_command)
        )

        # 消息处理器
        self.application.add_handler(
            MessageHandler(filters.TEXT & ~filters.COMMAND, self.handle_message)
        )

        # 添加定期检查通知任务（跨Bot通信）
        async def check_notifications_job(context=None):
            """定期检查并发送待处理的通知"""
            try:
                from core.mainbot_push_handler import get_mainbot_push_handler

                push_handler = get_mainbot_push_handler()

                # 检查推送处理器是否初始化
                if push_handler.qa_bot is None:
                    logger.warning("⚠️ 问答Bot推送处理器未初始化，跳过通知检查")
                    return 0

                count = await push_handler.process_pending_notifications()

                if count > 0:
                    logger.info(f"✅ 通知检查任务完成：已处理 {count} 条通知")
                else:
                    logger.debug("📭 通知检查任务完成：无待处理通知")

            except Exception as e:
                logger.error(f"❌ 检查通知任务失败: {type(e).__name__}: {e}", exc_info=True)
                return 0

        # 每30秒检查一次通知队列
        self.application.job_queue.run_repeating(check_notifications_job, interval=30, first=10)
        logger.info("✅ 跨Bot通知检查任务已启动：每30秒执行一次，首次执行延迟10秒")

        # 启动Bot
        logger.info("问答Bot已启动，等待消息...")
        self.application.run_polling(allowed_updates=Update.ALL_TYPES)


def main():
    """主函数"""
    try:
        # 创建并运行Bot
        bot = QABot()
        bot.run()

    except KeyboardInterrupt:
        logger.info("收到中断信号，正在关闭...")
    except Exception as e:
        logger.error(f"Bot运行出错: {type(e).__name__}: {e}", exc_info=True)
        sys.exit(1)


if __name__ == "__main__":
    main()
