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
AI配置管理命令处理
"""

import logging

from ..config import ADMIN_LIST, logger, LLM_API_KEY, LLM_BASE_URL, LLM_MODEL
from ..config import load_config, save_config
from ..states import get_user_context
from ..i18n import get_text

logger = logging.getLogger(__name__)


async def handle_show_ai_config(event):
    """处理/showaicfg命令，显示当前AI配置"""
    sender_id = event.sender_id
    command = event.text
    logger.info(f"收到命令: {command}，发送者: {sender_id}")
    
    # 检查发送者是否为管理员
    if sender_id not in ADMIN_LIST and ADMIN_LIST != ['me']:
        logger.warning(f"发送者 {sender_id} 没有权限执行命令 {command}")
        await event.reply(get_text('error.permission_denied'))
        return
    
    # 显示当前配置
    config_info = f"{get_text('aicfg.title')}\n\n"
    if LLM_API_KEY:
        api_key_display = f"{LLM_API_KEY[:10]}...{LLM_API_KEY[-10:] if len(LLM_API_KEY) > 20 else LLM_API_KEY}"
        config_info += get_text('aicfg.api_key', value=api_key_display) + "\n"
    else:
        config_info += get_text('aicfg.api_key', value=get_text('aicfg.not_set')) + "\n"
    config_info += get_text('aicfg.base_url', value=LLM_BASE_URL) + "\n"
    config_info += get_text('aicfg.model', value=LLM_MODEL) + "\n"
    
    logger.info(f"执行命令 {command} 成功")
    await event.reply(config_info)


async def handle_set_ai_config(event):
    """处理/setaicfg命令，触发AI配置设置流程"""
    sender_id = event.sender_id
    command = event.text
    logger.info(f"收到命令: {command}，发送者: {sender_id}")
    
    # 检查发送者是否为管理员
    if sender_id not in ADMIN_LIST and ADMIN_LIST != ['me']:
        logger.warning(f"发送者 {sender_id} 没有权限执行命令 {command}")
        await event.reply(get_text('error.permission_denied'))
        return
    
    # 使用状态管理器
    user_context = get_user_context()
    user_context.start_setting_ai_config(sender_id)
    logger.info(f"添加用户 {sender_id} 到AI配置设置集合")
    
    logger.info(f"开始执行 {command} 命令")
    await event.reply(get_text('aicfg.set_prompt'))


async def handle_ai_config_input(event):
    """处理用户输入的AI配置参数"""
    # 检查发送者是否在设置AI配置的集合中
    sender_id = event.sender_id
    input_text = event.text
    
    user_context = get_user_context()
    if not user_context.is_setting_ai_config(sender_id):
        return
    
    logger.info(f"收到用户 {sender_id} 的AI配置输入: {input_text}")
    
    # 检查命令
    if input_text == '/cancel':
        # 取消设置
        user_context.end_setting_ai_config(sender_id)
        logger.info(f"用户 {sender_id} 取消了AI配置设置")
        await event.reply(get_text('aicfg.cancelled'))
        return
    
    # 检查是否是其他命令
    if input_text.startswith('/') and input_text != '/skip':
        # 如果是其他命令，提示用户先完成当前配置或取消
        await event.reply(get_text('aicfg.in_progress'))
        return
    
    # 获取用户当前配置
    current_ai_config = user_context.get_ai_config(sender_id)
    
    # 重新计算配置步骤：找到第一个为None的参数
    config_order = ['api_key', 'base_url', 'model']
    config_step = None
    
    for i, param in enumerate(config_order):
        if current_ai_config[param] is None:
            config_step = i + 1
            break
    
    if config_step is None:
        config_step = 4  # 所有参数都已设置
    
    logger.debug(f"当前AI配置步骤: {config_step}")
    
    # 根据当前步骤处理输入
    if config_step == 1:
        # 处理API Key
        if input_text != '/skip':
            user_context.update_ai_config(sender_id, 'api_key', input_text.strip())
            logger.debug(f"用户 {sender_id} 设置了新的API Key: {'***' if input_text.strip() else '未设置'}")
        else:
            # 使用当前值
            user_context.update_ai_config(sender_id, 'api_key', LLM_API_KEY)
        
        current_ai_config = user_context.get_ai_config(sender_id)
        api_key_display = current_ai_config['api_key']
        if api_key_display:
            api_key_display = f"{api_key_display[:10]}...{api_key_display[-10:] if len(api_key_display) > 20 else api_key_display}"
        else:
            api_key_display = get_text('aicfg.not_set')
        await event.reply(get_text('aicfg.api_key_set', key=api_key_display))
    
    elif config_step == 2:
        # 处理Base URL
        if input_text != '/skip':
            user_context.update_ai_config(sender_id, 'base_url', input_text.strip())
            logger.debug(f"用户 {sender_id} 设置了新的Base URL: {input_text.strip()}")
        else:
            # 使用当前值
            user_context.update_ai_config(sender_id, 'base_url', LLM_BASE_URL)
        
        current_ai_config = user_context.get_ai_config(sender_id)
        await event.reply(get_text('aicfg.base_url_set', value=current_ai_config['base_url']))
    
    elif config_step == 3:
        # 处理Model
        if input_text != '/skip':
            user_context.update_ai_config(sender_id, 'model', input_text.strip())
            logger.debug(f"用户 {sender_id} 设置了新的Model: {input_text.strip()}")
        else:
            # 使用当前值
            user_context.update_ai_config(sender_id, 'model', LLM_MODEL)
        
        # 获取最终配置
        final_config = user_context.get_ai_config(sender_id)
        
        # 保存配置
        save_config(final_config)
        logger.info(get_text('aicfg.saved'))
        
        # 从集合中移除用户
        user_context.end_setting_ai_config(sender_id)
        logger.info(f"从AI配置设置集合中移除用户 {sender_id}")
        
        # 显示最终配置
        api_key_display = final_config['api_key']
        if api_key_display:
            api_key_display = f"{api_key_display[:10]}...{api_key_display[-10:] if len(api_key_display) > 20 else api_key_display}"
        else:
            api_key_display = get_text('aicfg.not_set')
        
        config_info = get_text('aicfg.updated')
        config_info += get_text('aicfg.api_key', value=api_key_display) + "\n"
        config_info += get_text('aicfg.base_url', value=final_config['base_url']) + "\n"
        config_info += get_text('aicfg.model', value=final_config['model']) + "\n"
        
        logger.info(f"用户 {sender_id} 完成了AI配置设置")
        await event.reply(config_info)
    
    elif config_step == 4:
        # 所有参数都已设置，可能是重复输入，返回最终配置
        final_config = user_context.get_ai_config(sender_id)
        api_key_display = final_config['api_key']
        if api_key_display:
            api_key_display = f"{api_key_display[:10]}...{api_key_display[-10:] if len(api_key_display) > 20 else api_key_display}"
        else:
            api_key_display = get_text('aicfg.not_set')
        
        config_info = get_text('aicfg.completed')
        config_info += get_text('aicfg.api_key', value=api_key_display) + "\n"
        config_info += get_text('aicfg.base_url', value=final_config['base_url']) + "\n"
        config_info += get_text('aicfg.model', value=final_config['model']) + "\n"
        await event.reply(config_info)