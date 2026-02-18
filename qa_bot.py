#!/usr/bin/env python3
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
Sakura 问答Bot - 独立的智能问答助手
基于历史总结回答自然语言查询
"""

import asyncio
import logging
import os
import sys
from telegram import Update
from telegram.ext import Application, CommandHandler, MessageHandler, filters, ContextTypes

# 添加项目根目录到Python路径
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

from core.quota_manager import get_quota_manager
from core.qa_engine_v3 import get_qa_engine_v3
from core.conversation_manager import get_conversation_manager
from core.config import REPORT_ADMIN_IDS, get_qa_bot_persona

# 配置日志 - 添加[QA]前缀以便区分
class QAFormatter(logging.Formatter):
    """自定义日志格式器，添加[QA]前缀"""
    def format(self, record):
        # 在消息前添加 [QA] 前缀
        if record.msg and isinstance(record.msg, str):
            record.msg = f"[QA] {record.msg}"
        return super().format(record)

# 配置基础日志
logging.basicConfig(
    level=logging.INFO,
    format='%(asctime)s - %(name)s - %(levelname)s - %(message)s'
)

# 获取logger
logger = logging.getLogger(__name__)

# 为所有处理器应用自定义格式
for handler in logging.root.handlers:
    handler.setFormatter(QAFormatter(
        '%(asctime)s - %(name)s - %(levelname)s - %(message)s'
    ))


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
        self.application = None

        logger.info("问答Bot初始化完成（v3.0.0向量搜索版本 + 多轮对话支持）")

    async def start(self, update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
        """处理/start命令"""
        user_id = update.effective_user.id

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

        await update.message.reply_text(welcome_message, parse_mode='Markdown')

    async def help_command(self, update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
        """处理/help命令"""
        help_text = """📚 <b>使用帮助</b>

<b>基础命令：</b>
• /start - 查看欢迎信息
• /help - 显示这份帮助文档
• /status - 查看使用配额和会话状态
• /clear - 清除对话记忆，重新开始
• /view_persona - 查看当前助手人格设定

<b>自然语言查询：</b>
直接发送问题，例如：
• "上周发生了什么？"
• "最近有什么技术讨论？"
• "今天有什么更新？"
• "关于特定关键词的内容"

<b>多轮对话：</b>
• 我会记住你的对话上下文（30分钟内）
• 你可以使用代词追问："那它呢？"、"这个怎么样？"
• 对话超时后会自动开始新会话

<b>时间关键词：</b>
• 今天、昨天、前天
• 本周、上周
• 本月、上月
• 最近7天、最近30天

<b>功能特点：</b>
✅ 智能意图识别
✅ 上下文感知（多轮对话）
✅ 频道画像注入
✅ 多频道综合查询

⚠️ <b>注意：</b>
请尽量提出与频道总结相关的问题。过度偏离的查询可能会被拦截。"""

        await update.message.reply_text(help_text, parse_mode='HTML')

    async def status_command(self, update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
        """处理/status命令"""
        user_id = update.effective_user.id
        status_info = self.quota_manager.get_usage_status(user_id)

        # 构建配额状态文本
        if status_info.get('is_admin'):
            quota_text = """🌟 <b>管理员状态</b>

你拥有无限制访问的特权。

📊 今日总使用：{}次""".format(status_info.get('total_used_today', 0))
        else:
            quota_text = """📊 <b>配额状态</b>

• 剩余次数: {remaining} 次""".format(
                used=status_info.get('used_today', 0),
                remaining=status_info.get('remaining', 50)
            )

        # 获取会话信息
        session_info = self.conversation_mgr.get_session_info(user_id)
        
        session_text = ""
        if session_info:
            is_active = session_info.get('is_active', False)
            status_emoji = "🟢 活跃中" if is_active else "⚪ 已超时"
            # 使用代码块显示会话ID，避免Markdown解析问题
            session_id_preview = session_info['session_id'][:8]
            session_text = f"""

🧠 <b>当前会话状态</b>
• 会话ID: <code>{session_id_preview}...</code>
• 消息数: {session_info['message_count']} 条
• 状态: {status_emoji}"""

        message = f"""📊 <b>系统状态</b>

{quota_text}{session_text}

📅 重置时间：每日 00:00 (UTC)"""

        # 使用HTML模式以避免Markdown解析错误
        await update.message.reply_text(message, parse_mode='HTML')

    async def clear_command(self, update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
        """处理/clear命令 - 清除对话历史"""
        user_id = update.effective_user.id

        # 清除所有对话历史
        deleted_count = self.conversation_mgr.clear_user_history(user_id)

        message = f"""🗑️ **对话记忆已清除**

已清除 **{deleted_count}** 条对话记录。

现在，我们的对话是全新的开始。有什么可以帮你的吗？"""

        await update.message.reply_text(message, parse_mode='Markdown')

    async def view_persona_command(self, update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
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

        await update.message.reply_text(message, parse_mode='Markdown')

    async def handle_message(self, update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
        """处理用户消息（自然语言查询）"""
        # 防御性检查：忽略非用户消息（如频道事件、系统消息）
        if not update.effective_user or not update.message:
            return
        
        user_id = update.effective_user.id
        query = update.message.text

        if not query or not query.strip():
            return

        logger.info(f"收到查询: user_id={user_id}, query={query}")

        try:
            # 1. 检查配额
            quota_check = self.quota_manager.check_quota(user_id)

            if not quota_check.get("allowed", False):
                # 配额不足
                await update.message.reply_text(quota_check.get("message", "配额不足"))
                return

            # 2. 显示"正在思考"消息
            thinking_msg = await update.message.reply_text("🔍 正在检索相关记录...")

            # 3. 处理查询
            answer = await self.qa_engine.process_query(query, user_id)

            # 4. 删除"正在思考"消息
            try:
                await thinking_msg.delete()
            except:
                pass

            # 5. 发送回答
            # 检查消息长度，Telegram限制4096字符
            # 支持Markdown，如果失败则降级到HTML，最后降级到纯文本
            if len(answer) <= 4096:
                await self._send_with_fallback(update.message, answer)
            else:
                # 消息过长，分段发送
                parts = self._split_long_message(answer)
                for i, part in enumerate(parts):
                    await self._send_with_fallback(update.message, part)
                    if i > 0:
                        await asyncio.sleep(0.5)  # 避免发送过快

            # 6. 附加配额提示（如果不是管理员）
            if not quota_check.get("is_admin", False):
                quota_tip = f"\n\n{quota_check.get('message', '')}"
                try:
                    await update.message.reply_text(quota_tip)
                except:
                    pass

        except Exception as e:
            logger.error(f"处理消息失败: {type(e).__name__}: {e}", exc_info=True)
            await update.message.reply_text("❌ 抱歉，处理查询时出错。请稍后再试。")

    def _split_long_message(self, text: str, max_length: int = 4096) -> list:
        """将长消息分割为多个部分"""
        if len(text) <= max_length:
            return [text]

        parts = []
        current_part = ""
        paragraphs = text.split('\n\n')

        for para in paragraphs:
            if len(current_part) + len(para) + 2 <= max_length:
                current_part += para + '\n\n'
            else:
                if current_part:
                    parts.append(current_part.strip())
                current_part = para + '\n\n'

        if current_part:
            parts.append(current_part.strip())

        return parts

    async def _send_with_fallback(self, message, text: str):
        """发送消息，强制使用Markdown格式
        
        如果AI生成的Markdown有语法错误，进行简单修复
        """
        # 直接尝试发送Markdown
        try:
            await message.reply_text(text, parse_mode='Markdown')
        except Exception as e:
            logger.warning(f"Markdown发送失败: {e}, 尝试修复格式")
            # 尝试修复常见的Markdown格式错误
            fixed_text = self._fix_markdown(text)
            try:
                await message.reply_text(fixed_text, parse_mode='Markdown')
            except Exception as e2:
                logger.error(f"Markdown修复后仍然失败: {e2}, 使用纯文本")
                # 最后的保底方案
                await message.reply_text(text)
    
    def _fix_markdown(self, text: str) -> str:
        """修复常见的Markdown格式错误"""
        import re
        
        # 修复未配对的星号（粗体）
        text = re.sub(r'\*\*([^*]+)$', r'**\1**', text, flags=re.MULTILINE)
        text = re.sub(r'^([^*]+)\*\*', r'**\1**', text, flags=re.MULTILINE)
        
        # 修复未配对的星号（斜体）
        text = re.sub(r'\*([^*\n]+)$', r'*\1*', text, flags=re.MULTILINE)
        text = re.sub(r'^([^*\n]+)\*', r'*\1*', text, flags=re.MULTILINE)
        
        # 修复未配对的反引号
        text = re.sub(r'`([^`\n]+)$', r'`\1`', text, flags=re.MULTILINE)
        text = re.sub(r'^([^`\n]+)`', r'`\1`', text, flags=re.MULTILINE)
        
        # 修复未配对的下划线
        text = re.sub(r'__([^_]+)$', r'__\1__', text, flags=re.MULTILINE)
        text = re.sub(r'^([^_]+)__', r'__\1__', text, flags=re.MULTILINE)
        
        return text

    def run(self):
        """运行Bot"""
        logger.info("启动问答Bot...")

        # 创建应用
        self.application = Application.builder().token(QA_BOT_TOKEN).build()

        # 注册处理器
        self.application.add_handler(CommandHandler("start", self.start))
        self.application.add_handler(CommandHandler("help", self.help_command))
        self.application.add_handler(CommandHandler("status", self.status_command))
        self.application.add_handler(CommandHandler("clear", self.clear_command))
        self.application.add_handler(CommandHandler("view_persona", self.view_persona_command))
        self.application.add_handler(MessageHandler(filters.TEXT & ~filters.COMMAND, self.handle_message))

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