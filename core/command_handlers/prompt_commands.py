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
提示词管理命令处理
"""

from ..config import ADMIN_LIST, logger
from ..i18n import get_text
from ..poll_prompt_manager import load_poll_prompt, save_poll_prompt
from ..prompt_manager import load_prompt, save_prompt
from ..states import get_user_context


async def handle_show_prompt(event):
    """处理/showprompt命令，显示当前提示词"""
    sender_id = event.sender_id
    command = event.text
    logger.info(f"收到命令: {command}，发送者: {sender_id}")

    # 检查发送者是否为管理员
    if sender_id not in ADMIN_LIST and ADMIN_LIST != ["me"]:
        logger.warning(f"发送者 {sender_id} 没有权限执行命令 {command}")
        await event.reply(get_text("error.permission_denied"))
        return

    logger.info(f"执行命令 {command} 成功")
    current_prompt = load_prompt()
    prompt_message = f"{get_text('prompt.current_title')}{get_text('prompt.current_content', content=current_prompt)}"
    await event.reply(prompt_message)


async def handle_set_prompt(event):
    """处理/setprompt命令，触发提示词设置流程"""
    sender_id = event.sender_id
    command = event.text
    logger.info(f"收到命令: {command}，发送者: {sender_id}")

    # 检查发送者是否为管理员
    if sender_id not in ADMIN_LIST and ADMIN_LIST != ["me"]:
        logger.warning(f"发送者 {sender_id} 没有权限执行命令 {command}")
        await event.reply(get_text("error.permission_denied"))
        return

    # 使用状态管理器
    user_context = get_user_context()
    user_context.start_setting_prompt(sender_id)
    logger.info(f"添加用户 {sender_id} 到提示词设置集合")
    current_prompt = load_prompt()
    prompt_message = f"{get_text('prompt.setting')}\n\n{get_text('prompt.current_content', content=current_prompt)}"
    await event.reply(prompt_message)


async def handle_prompt_input(event):
    """处理用户输入的新提示词"""
    sender_id = event.sender_id
    input_text = event.text

    # 使用状态管理器检查
    user_context = get_user_context()
    if not user_context.is_waiting_for_prompt(sender_id):
        return

    logger.info(f"收到用户 {sender_id} 的提示词输入")

    # 检查是否是命令消息，如果是则不处理
    if input_text.startswith("/"):
        logger.warning(f"用户 {sender_id} 发送了命令而非提示词内容: {input_text}")
        await event.reply(get_text("prompt.error_command"))
        return

    # 获取新提示词
    new_prompt = input_text.strip()
    logger.debug(
        f"用户 {sender_id} 设置的新提示词: {new_prompt[:100]}..."
        if len(new_prompt) > 100
        else f"用户 {sender_id} 设置的新提示词: {new_prompt}"
    )

    # 更新提示词
    save_prompt(new_prompt)
    logger.info(f"已更新提示词，长度: {len(new_prompt)}字符")

    # 从状态管理器中移除用户
    user_context.end_setting_prompt(sender_id)
    logger.info(f"从提示词设置集合中移除用户 {sender_id}")

    prompt_message = f"{get_text('prompt.set_success')}\n\n{get_text('prompt.current_content', content=new_prompt)}"
    await event.reply(prompt_message)


async def handle_show_poll_prompt(event):
    """处理/showpollprompt命令，显示当前投票提示词"""
    sender_id = event.sender_id
    command = event.text
    logger.info(f"收到命令: {command}，发送者: {sender_id}")

    # 检查发送者是否为管理员
    if sender_id not in ADMIN_LIST and ADMIN_LIST != ["me"]:
        logger.warning(f"发送者 {sender_id} 没有权限执行命令 {command}")
        await event.reply(get_text("error.permission_denied"))
        return

    logger.info(f"执行命令 {command} 成功")
    current_poll_prompt = load_poll_prompt()
    prompt_message = f"{get_text('prompt.poll_current_title')}{get_text('prompt.current_content', content=current_poll_prompt)}"
    await event.reply(prompt_message)


async def handle_set_poll_prompt(event):
    """处理/setpollprompt命令，触发投票提示词设置流程"""
    sender_id = event.sender_id
    command = event.text
    logger.info(f"收到命令: {command}，发送者: {sender_id}")

    # 检查发送者是否为管理员
    if sender_id not in ADMIN_LIST and ADMIN_LIST != ["me"]:
        logger.warning(f"发送者 {sender_id} 没有权限执行命令 {command}")
        await event.reply(get_text("error.permission_denied"))
        return

    # 使用状态管理器
    user_context = get_user_context()
    user_context.start_setting_poll_prompt(sender_id)
    logger.info(f"添加用户 {sender_id} 到投票提示词设置集合")
    current_poll_prompt = load_poll_prompt()
    prompt_message = f"{get_text('prompt.poll_setting')}\n\n{get_text('prompt.current_content', content=current_poll_prompt)}"
    await event.reply(prompt_message)


async def handle_poll_prompt_input(event):
    """处理用户输入的新投票提示词"""
    sender_id = event.sender_id
    input_text = event.text

    # 使用状态管理器检查
    user_context = get_user_context()
    if not user_context.is_waiting_for_poll_prompt(sender_id):
        return

    logger.info(f"收到用户 {sender_id} 的投票提示词输入")

    # 检查是否是命令消息，如果是则不处理
    if input_text.startswith("/"):
        logger.warning(f"用户 {sender_id} 发送了命令而非提示词内容: {input_text}")
        await event.reply(get_text("prompt.error_command"))
        return

    # 获取新提示词
    new_poll_prompt = input_text.strip()
    logger.debug(
        f"用户 {sender_id} 设置的新投票提示词: {new_poll_prompt[:100]}..."
        if len(new_poll_prompt) > 100
        else f"用户 {sender_id} 设置的新投票提示词: {new_poll_prompt}"
    )

    # 更新投票提示词
    save_poll_prompt(new_poll_prompt)
    logger.info(f"已更新投票提示词，长度: {len(new_poll_prompt)}字符")

    # 从状态管理器中移除用户
    user_context.end_setting_poll_prompt(sender_id)
    logger.info(f"从投票提示词设置集合中移除用户 {sender_id}")

    prompt_message = f"{get_text('prompt.poll_set_success')}\n\n{get_text('prompt.current_content', content=new_poll_prompt)}"
    await event.reply(prompt_message)
