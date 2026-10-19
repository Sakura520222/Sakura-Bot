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
主 Bot 内联菜单键盘构建工具。
"""

from telethon import Button

CALLBACK_PREFIX = "mainbot"
MAX_CALLBACK_DATA_BYTES = 64

MENU_MAIN = "main"
MENU_BASIC = "basic"
MENU_QA = "qa"
MENU_FORWARDING = "forwarding"
MENU_CONFIG = "config"

CMD_HELP = "help"
CMD_SUMMARY = "summary"
CMD_CHANNELS = "channels"
CMD_HISTORY = "history"
CMD_STATS = "stats"
CMD_QA_STATUS = "qa_status"
CMD_QA_START = "qa_start"
CMD_QA_STOP = "qa_stop"
CMD_QA_RESTART = "qa_restart"
CMD_QA_STATS = "qa_stats"
CMD_FORWARDING_STATUS = "fwd_status"
CMD_FORWARDING_STATS = "fwd_stats"
CMD_FORWARDING_HELP = "fwd_help"
CMD_SHOW_PROMPT = "show_prompt"
CMD_SHOW_AI_CONFIG = "show_ai"
CMD_SHOW_SCHEDULE = "show_schedule"
CMD_SHOW_POLL = "show_poll"
CMD_CONFIG_HELP = "config_help"
CMD_GUARD = "guard"

CONFIG_HELP_AI_COMMANDS = (
    ("/showprompt", "查看当前总结提示词"),
    ("/setprompt", "设置总结提示词"),
    ("/showpollprompt", "查看投票提示词"),
    ("/setpollprompt", "设置投票提示词"),
    ("/showaicfg", "查看 AI 配置"),
    ("/setaicfg", "设置 AI 配置"),
)

CONFIG_HELP_CHANNEL_COMMANDS = (
    ("/showchannelschedule", "查看频道时间配置"),
    ("/setchannelschedule", "设置频道时间配置"),
    ("/deletechannelschedule", "删除频道时间配置"),
    ("/channelpoll", "查看频道投票配置"),
    ("/setchannelpoll", "设置频道投票配置"),
    ("/deletechannelpoll", "删除频道投票配置"),
)

MENU_TITLES = {
    MENU_MAIN: "🌸 Sakura Bot 主菜单",
    MENU_BASIC: "📌 基础高频功能",
    MENU_QA: "🤖 QA Bot 控制",
    MENU_FORWARDING: "🔁 频道转发功能",
    MENU_CONFIG: "⚙️ 配置入口",
}

MENU_DESCRIPTIONS = {
    MENU_MAIN: "请选择要使用的功能分类：",
    MENU_BASIC: "选择常用功能。部分功能可能需要管理员权限。",
    MENU_QA: "管理独立运行的 QA Bot。仅管理员可用。",
    MENU_FORWARDING: "查看频道转发状态、统计和帮助。",
    MENU_CONFIG: "配置类功能入口。为避免误操作，写入类配置只展示用法提示。",
}


def _format_config_help_commands(commands: tuple[tuple[str, str], ...]) -> str:
    """格式化配置帮助命令列表。"""
    return "\n".join(f"{command} - {description}" for command, description in commands)


CONFIG_HELP_TEXT = f"""⚙️ **配置入口说明**

以下配置类命令通常需要参数或多步输入，按钮不会直接执行危险操作。

**AI 与提示词**
{_format_config_help_commands(CONFIG_HELP_AI_COMMANDS)}

**频道与定时**
{_format_config_help_commands(CONFIG_HELP_CHANNEL_COMMANDS)}

**提示**：如需修改配置，请发送对应命令并按提示操作。"""


def build_callback(action: str, value: str) -> bytes:
    """构建主 Bot 内联按钮回调数据。

    Telegram callback_data 限制为 64 字节，新增菜单项时需保持 action/value 简短。
    长度校验以 UTF-8 编码后的字节数为准，不能使用字符串字符数代替。
    """
    callback_data = f"{CALLBACK_PREFIX}:{action}:{value}".encode()
    if len(callback_data) > MAX_CALLBACK_DATA_BYTES:
        raise ValueError("Telegram callback_data 不能超过 64 字节")
    return callback_data


def build_menu_text(menu: str = MENU_MAIN) -> str:
    """构建菜单说明文本。"""
    title = MENU_TITLES.get(menu, MENU_TITLES[MENU_MAIN])
    description = MENU_DESCRIPTIONS.get(menu, MENU_DESCRIPTIONS[MENU_MAIN])
    return f"{title}\n\n{description}"


def build_mainbot_menu_keyboard(menu: str = MENU_MAIN) -> list[list[Button]]:
    """构建主 Bot 内联菜单按钮。"""
    if menu == MENU_BASIC:
        return [
            [
                Button.inline("📝 立即总结", data=build_callback("cmd", CMD_SUMMARY)),
                Button.inline("📚 频道列表", data=build_callback("cmd", CMD_CHANNELS)),
            ],
            [
                Button.inline("📋 历史总结", data=build_callback("cmd", CMD_HISTORY)),
                Button.inline("📊 统计数据", data=build_callback("cmd", CMD_STATS)),
            ],
            [Button.inline("❓ 帮助", data=build_callback("cmd", CMD_HELP))],
            [Button.inline("⬅️ 返回主菜单", data=build_callback("menu", MENU_MAIN))],
        ]

    if menu == MENU_QA:
        return [
            [
                Button.inline("📊 QA 状态", data=build_callback("cmd", CMD_QA_STATUS)),
                Button.inline("📈 QA 统计", data=build_callback("cmd", CMD_QA_STATS)),
            ],
            [
                Button.inline("▶️ 启动 QA", data=build_callback("cmd", CMD_QA_START)),
                Button.inline("⏹ 停止 QA", data=build_callback("cmd", CMD_QA_STOP)),
            ],
            [Button.inline("🔄 重启 QA", data=build_callback("cmd", CMD_QA_RESTART))],
            [Button.inline("⬅️ 返回主菜单", data=build_callback("menu", MENU_MAIN))],
        ]

    if menu == MENU_FORWARDING:
        return [
            [
                Button.inline("📡 转发状态", data=build_callback("cmd", CMD_FORWARDING_STATUS)),
                Button.inline("📊 转发统计", data=build_callback("cmd", CMD_FORWARDING_STATS)),
            ],
            [Button.inline("📖 转发帮助", data=build_callback("cmd", CMD_FORWARDING_HELP))],
            [Button.inline("⬅️ 返回主菜单", data=build_callback("menu", MENU_MAIN))],
        ]

    if menu == MENU_CONFIG:
        return [
            [
                Button.inline("🧠 查看提示词", data=build_callback("cmd", CMD_SHOW_PROMPT)),
                Button.inline("🔧 AI 配置", data=build_callback("cmd", CMD_SHOW_AI_CONFIG)),
            ],
            [
                Button.inline("⏰ 定时配置", data=build_callback("cmd", CMD_SHOW_SCHEDULE)),
                Button.inline("🗳 投票配置", data=build_callback("cmd", CMD_SHOW_POLL)),
            ],
            [Button.inline("📖 配置用法", data=build_callback("cmd", CMD_CONFIG_HELP))],
            [Button.inline("⬅️ 返回主菜单", data=build_callback("menu", MENU_MAIN))],
        ]

    return [
        [
            Button.inline("📌 基础功能", data=build_callback("menu", MENU_BASIC)),
            Button.inline("🤖 QA 控制", data=build_callback("menu", MENU_QA)),
        ],
        [
            Button.inline("🔁 频道转发", data=build_callback("menu", MENU_FORWARDING)),
            Button.inline("⚙️ 配置入口", data=build_callback("menu", MENU_CONFIG)),
        ],
        [Button.inline("❓ 完整帮助", data=build_callback("cmd", CMD_HELP))],
        [Button.inline("🛡 Sakura Guard", data=build_callback("cmd", CMD_GUARD))],
    ]
