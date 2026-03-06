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
机器人命令定义和注册模块

将所有 Telegram Bot 命令集中管理，提供统一的注册接口。
"""

from telethon.tl.functions.bots import SetBotCommandsRequest
from telethon.tl.types import BotCommand, BotCommandScopeDefault

# ==================== 命令定义 ====================

# 命令按功能分类定义
# 每个分类包含：
#   - category: 分类名称（中文显示）
#   - i18n_key: 国际化键名后缀（用于生成 help.section_xxx）
#   - commands: 命令列表
# 每个命令是一个元组：(command, description)
BOT_COMMANDS = [
    # ========== 1. 基础与核心 ==========
    {
        "category": "基础核心",
        "i18n_key": "basic_core",
        "commands": [
            ("start", "查看欢迎消息和帮助"),
            ("help", "查看完整命令列表"),
            ("summary", "立即生成本周频道消息汇总"),
        ],
    },
    # ========== 2. 频道管理 ==========
    {
        "category": "频道管理",
        "i18n_key": "channel",
        "commands": [
            ("showchannels", "查看当前频道列表"),
            ("addchannel", "添加频道"),
            ("deletechannel", "删除频道"),
        ],
    },
    # ========== 3. 定时与推送 ==========
    {
        "category": "定时推送",
        "i18n_key": "schedule_push",
        "commands": [
            ("showchannelschedule", "查看频道自动总结时间配置"),
            ("setchannelschedule", "设置频道自动总结时间"),
            ("deletechannelschedule", "删除频道自动总结时间配置"),
            ("clearsummarytime", "清除上次总结时间记录"),
            ("setsendtosource", "设置是否将报告发送回源频道"),
        ],
    },
    # ========== 4. AI 配置 ==========
    {
        "category": "AI配置",
        "i18n_key": "ai",
        "commands": [
            ("showprompt", "查看当前提示词"),
            ("setprompt", "设置自定义提示词"),
            ("showpollprompt", "查看当前投票提示词"),
            ("setpollprompt", "设置投票提示词"),
            ("showaicfg", "查看AI配置"),
            ("setaicfg", "设置AI配置"),
        ],
    },
    # ========== 5. 频道互动 ==========
    {
        "category": "频道互动",
        "i18n_key": "channel_interaction",
        "commands": [
            ("channelpoll", "查看频道投票配置"),
            ("setchannelpoll", "设置频道投票配置"),
            ("deletechannelpoll", "删除频道投票配置"),
            ("showcommentwelcome", "查看频道评论区欢迎配置"),
            ("setcommentwelcome", "设置频道评论区欢迎配置"),
            ("deletecommentwelcome", "删除频道评论区欢迎配置"),
        ],
    },
    # ========== 6. 统计与历史 ==========
    {
        "category": "统计历史",
        "i18n_key": "stats_history",
        "commands": [
            ("history", "查看历史总结"),
            ("export", "导出历史记录"),
            ("stats", "查看统计数据"),
        ],
    },
    # ========== 7. 系统运维 ==========
    {
        "category": "系统运维",
        "i18n_key": "system_ops",
        "commands": [
            # 系统控制
            ("pause", "暂停所有定时任务"),
            ("resume", "恢复所有定时任务"),
            ("restart", "重启机器人"),
            ("shutdown", "彻底停止机器人"),
            # 日志与缓存
            ("showloglevel", "查看当前日志级别"),
            ("setloglevel", "设置日志级别"),
            ("clearcache", "清除讨论组ID缓存"),
            # 更新维护
            ("changelog", "查看更新日志"),
            ("update", "一键更新机器人"),
            # 问答Bot控制
            ("qa_status", "查看问答Bot运行状态"),
            ("qa_start", "启动问答Bot"),
            ("qa_stop", "停止问答Bot"),
            ("qa_restart", "重启问答Bot"),
            ("qa_stats", "查看问答Bot详细统计"),
        ],
    },
    # ========== 8. 数据库管理（高危） ==========
    {
        "category": "数据库管理",
        "i18n_key": "database",
        "commands": [
            ("migrate_check", "检查数据库迁移准备状态"),
            ("migrate_start", "开始数据库迁移"),
            ("migrate_status", "查看数据库迁移进度"),
            ("db_clear", "清空MySQL数据库（危险操作）"),
        ],
    },
    # ========== 9. 偏好设置 ==========
    {
        "category": "偏好设置",
        "i18n_key": "preferences",
        "commands": [
            ("language", "切换界面语言"),
        ],
    },
    # ========== 10. 频道消息转发 ==========
    {
        "category": "频道转发",
        "i18n_key": "forwarding",
        "commands": [
            ("forwarding", "查看转发功能状态"),
            ("forwarding_enable", "启用转发功能"),
            ("forwarding_disable", "禁用转发功能"),
            ("forwarding_stats", "查看转发统计"),
            ("forwarding_footer", "设置转发底栏"),
            ("forwarding_default_footer", "启用/禁用默认底栏"),
        ],
    },
    # ========== 11. UserBot 管理 ==========
    {
        "category": "UserBot管理",
        "i18n_key": "userbot",
        "commands": [
            ("userbot_status", "查看 UserBot 状态"),
            ("userbot_join", "UserBot 加入频道"),
            ("userbot_leave", "UserBot 离开频道"),
            ("userbot_list", "列出已加入频道"),
        ],
    },
]


# ==================== 辅助函数 ====================


def get_bot_commands() -> list[BotCommand]:
    """
    获取 BotCommand 对象列表

    Returns:
        List[BotCommand]: Telegram Bot 命令对象列表
    """
    commands = []

    for category_group in BOT_COMMANDS:
        for cmd, desc in category_group["commands"]:
            commands.append(BotCommand(command=cmd, description=desc))

    return commands


def get_command_categories() -> list[dict]:
    """
    获取命令分类定义（用于生成帮助文档等）

    Returns:
        List[dict]: 命令分类列表，每个分类包含 category 和 commands
    """
    return BOT_COMMANDS


def get_command_count() -> int:
    """
    获取命令总数

    Returns:
        int: 命令总数
    """
    return sum(len(category["commands"]) for category in BOT_COMMANDS)


# ==================== 注册函数 ====================


async def register_commands(client, lang_code: str = "zh") -> None:
    """
    注册机器人命令到 Telegram

    Args:
        client: Telegram 客户端实例
        lang_code: 语言代码（默认 "zh"）
    """
    commands = get_bot_commands()

    await client(
        SetBotCommandsRequest(
            scope=BotCommandScopeDefault(), lang_code=lang_code, commands=commands
        )
    )


# ==================== 帮助文档生成 ====================


def generate_help_text(get_text_func) -> str:
    """
    生成格式化的帮助文档

    Args:
        get_text_func: 国际化文本获取函数（core.i18n.get_text）

    Returns:
        str: 格式化的帮助文档
    """
    help_text = f"{get_text_func('help.title')}\n\n"

    for category_group in BOT_COMMANDS:
        # 使用 i18n_key 字段生成正确的国际化键
        i18n_key = category_group.get("i18n_key", "")
        category = category_group["category"]
        commands = category_group["commands"]

        # 生成章节标题键名
        section_key = f"help.section_{i18n_key}" if i18n_key else ""
        section_title = get_text_func(section_key, default=category) if section_key else category

        help_text += f"{section_title}\n"

        for cmd, _ in commands:
            cmd_key = f"cmd.{cmd}"
            help_text += f"{get_text_func(cmd_key)}\n"

        help_text += "\n"

    help_text += get_text_func("help.tip")

    return help_text
