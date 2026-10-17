# Copyright 2026 Sakura-Bot
#
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
# 许可证全文：参见 LICENSE 文件

"""AI 配置管理命令：会话只收集字段补丁，不保存完整配置。"""

import core.config as config_module
from core.config import logger, update_config_fields
from core.i18n.i18n import get_text
from core.infrastructure.utils.auth import is_admin
from core.infrastructure.utils.states import get_user_context


def _api_key_display(value):
    """所有长度的密钥均完全遮蔽，短密钥也不能回显原文。"""
    return "***" if value else get_text("aicfg.not_set")


def _format_config(config: dict) -> str:
    return (
        get_text(
            "aicfg.api_key",
            value=_api_key_display(config.get("api_key", config_module.LLM_API_KEY)),
        )
        + "\n"
        + get_text("aicfg.base_url", value=config.get("base_url", config_module.LLM_BASE_URL))
        + "\n"
        + get_text("aicfg.model", value=config.get("model", config_module.LLM_MODEL))
        + "\n"
    )


async def handle_show_ai_config(event):
    """处理 /showaicfg，不记录可能携带凭据的命令参数。"""
    sender_id = event.sender_id
    logger.info("收到 /showaicfg 命令，发送者: %s", sender_id)
    if not is_admin(sender_id):
        await event.reply(get_text("error.permission_denied"))
        return
    await event.reply(f"{get_text('aicfg.title')}\n\n" + _format_config({}))


async def handle_set_ai_config(event):
    """处理 /setaicfg，触发当前用户的 AI 配置会话。"""
    sender_id = event.sender_id
    logger.info("收到 /setaicfg 命令，发送者: %s", sender_id)
    if not is_admin(sender_id):
        await event.reply(get_text("error.permission_denied"))
        return
    get_user_context().start_setting_ai_config(sender_id)
    await event.reply(get_text("aicfg.set_prompt"))


async def handle_ai_config_input(event):
    """处理输入，/skip 保留提交时的最新值，/cancel 不触碰磁盘。"""
    sender_id = event.sender_id
    input_text = event.text
    context = get_user_context()
    if not context.is_setting_ai_config(sender_id) or not isinstance(input_text, str):
        return
    logger.info("收到用户 %s 的 AI 配置输入", sender_id)
    if input_text == "/cancel":
        context.end_setting_ai_config(sender_id)
        await event.reply(get_text("aicfg.cancelled"))
        return
    if input_text.startswith("/") and input_text != "/skip":
        await event.reply(get_text("aicfg.in_progress"))
        return

    draft = context.get_ai_config(sender_id)
    field = next((name for name in ("api_key", "base_url", "model") if draft[name] is None), None)
    if field is None:
        await event.reply(get_text("aicfg.completed") + _format_config(draft))
        return
    if input_text == "/skip":
        current_value = getattr(
            config_module,
            {"api_key": "LLM_API_KEY", "base_url": "LLM_BASE_URL", "model": "LLM_MODEL"}[field],
        )
        context.skip_ai_config(sender_id, field, current_value)
    else:
        context.update_ai_config(sender_id, field, input_text.strip())

    if field == "api_key":
        await event.reply(get_text("aicfg.api_key_set", value=_api_key_display(draft[field])))
    elif field == "base_url":
        await event.reply(get_text("aicfg.base_url_set", url=draft[field]))
    else:
        try:
            saved = update_config_fields(context.get_ai_config_updates(sender_id))
        except Exception as error:
            # 异常原文也可能包含输入值，日志和用户回复均使用安全固定文本。
            logger.error("保存 AI 配置失败，异常类型: %s", type(error).__name__)
            context.update_ai_config(sender_id, "model", None)
            await event.reply(get_text("aicfg.save_failed"))
            return
        context.end_setting_ai_config(sender_id)
        logger.info("用户 %s 完成了 AI 配置设置", sender_id)
        await event.reply(get_text("aicfg.updated") + _format_config(saved))
