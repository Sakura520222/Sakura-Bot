"""
AI配置管理命令处理
"""

import logging

from ..config import ADMIN_LIST, logger, LLM_API_KEY, LLM_BASE_URL, LLM_MODEL
from ..config import load_config, save_config
from ..states import get_user_context

logger = logging.getLogger(__name__)


async def handle_show_ai_config(event):
    """处理/showaicfg命令，显示当前AI配置"""
    sender_id = event.sender_id
    command = event.text
    logger.info(f"收到命令: {command}，发送者: {sender_id}")
    
    # 检查发送者是否为管理员
    if sender_id not in ADMIN_LIST and ADMIN_LIST != ['me']:
        logger.warning(f"发送者 {sender_id} 没有权限执行命令 {command}")
        await event.reply("您没有权限执行此命令")
        return
    
    # 显示当前配置
    config_info = f"当前AI配置：\n\n"
    config_info += f"API Key：{LLM_API_KEY[:10]}...{LLM_API_KEY[-10:] if len(LLM_API_KEY) > 20 else LLM_API_KEY}\n"
    config_info += f"Base URL：{LLM_BASE_URL}\n"
    config_info += f"Model：{LLM_MODEL}\n"
    
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
        await event.reply("您没有权限执行此命令")
        return
    
    # 使用状态管理器
    user_context = get_user_context()
    user_context.start_setting_ai_config(sender_id)
    logger.info(f"添加用户 {sender_id} 到AI配置设置集合")
    
    logger.info(f"开始执行 {command} 命令")
    await event.reply("请依次发送以下AI配置参数，或发送/skip跳过：\n\n1. API Key\n2. Base URL\n3. Model\n\n发送/cancel取消设置")


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
        await event.reply("已取消AI配置设置")
        return
    
    # 检查是否是其他命令
    if input_text.startswith('/') and input_text != '/skip':
        # 如果是其他命令，提示用户先完成当前配置或取消
        await event.reply("您正在设置AI配置中，请先完成当前配置或发送/cancel取消设置，然后再执行其他命令")
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
        await event.reply(f"API Key已设置为：{current_ai_config['api_key'][:10]}...{current_ai_config['api_key'][-10:] if len(current_ai_config['api_key']) > 20 else current_ai_config['api_key']}\n\n请发送Base URL，或发送/skip跳过")
    
    elif config_step == 2:
        # 处理Base URL
        if input_text != '/skip':
            user_context.update_ai_config(sender_id, 'base_url', input_text.strip())
            logger.debug(f"用户 {sender_id} 设置了新的Base URL: {input_text.strip()}")
        else:
            # 使用当前值
            user_context.update_ai_config(sender_id, 'base_url', LLM_BASE_URL)
        
        current_ai_config = user_context.get_ai_config(sender_id)
        await event.reply(f"Base URL已设置为：{current_ai_config['base_url']}\n\n请发送Model，或发送/skip跳过")
    
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
        logger.info("已保存AI配置到文件")
        
        # 从集合中移除用户
        user_context.end_setting_ai_config(sender_id)
        logger.info(f"从AI配置设置集合中移除用户 {sender_id}")
        
        # 显示最终配置
        config_info = f"AI配置已更新：\n\n"
        config_info += f"API Key：{final_config['api_key'][:10]}...{final_config['api_key'][-10:] if len(final_config['api_key']) > 20 else final_config['api_key']}\n"
        config_info += f"Base URL：{final_config['base_url']}\n"
        config_info += f"Model：{final_config['model']}\n"
        
        logger.info(f"用户 {sender_id} 完成了AI配置设置")
        await event.reply(config_info)
    
    elif config_step == 4:
        # 所有参数都已设置，可能是重复输入，返回最终配置
        final_config = user_context.get_ai_config(sender_id)
        await event.reply("AI配置已完成设置，当前配置：\n\n" + 
                        f"API Key：{final_config['api_key'][:10]}...{final_config['api_key'][-10:] if len(final_config['api_key']) > 20 else final_config['api_key']}\n" +
                        f"Base URL：{final_config['base_url']}\n" +
                        f"Model：{final_config['model']}\n")