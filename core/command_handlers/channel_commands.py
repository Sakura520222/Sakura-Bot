"""
频道管理命令处理
"""

import logging

from ..config import ADMIN_LIST, CHANNELS, logger, load_config, save_config

logger = logging.getLogger(__name__)


async def handle_show_channels(event):
    """处理/showchannels命令，查看当前频道列表"""
    sender_id = event.sender_id
    command = event.text
    logger.info(f"收到命令: {command}，发送者: {sender_id}")
    
    # 检查发送者是否为管理员
    if sender_id not in ADMIN_LIST and ADMIN_LIST != ['me']:
        logger.warning(f"发送者 {sender_id} 没有权限执行命令 {command}")
        await event.reply("您没有权限执行此命令")
        return
    
    logger.info(f"执行命令 {command} 成功")
    
    if not CHANNELS:
        await event.reply("当前没有配置任何频道")
        return
    
    # 构建频道列表消息
    channels_msg = "当前配置的频道列表：\n\n"
    for i, channel in enumerate(CHANNELS, 1):
        channels_msg += f"{i}. {channel}\n"
    
    await event.reply(channels_msg)


async def handle_add_channel(event):
    """处理/addchannel命令，添加频道"""
    sender_id = event.sender_id
    command = event.text
    logger.info(f"收到命令: {command}，发送者: {sender_id}")
    
    # 检查发送者是否为管理员
    if sender_id not in ADMIN_LIST and ADMIN_LIST != ['me']:
        logger.warning(f"发送者 {sender_id} 没有权限执行命令 {command}")
        await event.reply("您没有权限执行此命令")
        return
    
    try:
        _, channel_url = command.split(maxsplit=1)
        channel_url = channel_url.strip()
        
        if not channel_url:
            await event.reply("请提供有效的频道URL")
            return
        
        # 检查频道是否已存在
        if channel_url in CHANNELS:
            await event.reply(f"频道 {channel_url} 已存在于列表中")
            return
        
        # 添加频道到列表
        CHANNELS.append(channel_url)
        
        # 更新配置文件
        config = load_config()
        config['channels'] = CHANNELS
        save_config(config)
        
        logger.info(f"已添加频道 {channel_url} 到列表")
        await event.reply(f"频道 {channel_url} 已成功添加到列表中\n\n当前频道数量：{len(CHANNELS)}")
        
    except ValueError:
        # 没有提供频道URL
        await event.reply("请提供有效的频道URL，例如：/addchannel https://t.me/examplechannel")
    except Exception as e:
        logger.error(f"添加频道时出错: {type(e).__name__}: {e}", exc_info=True)
        await event.reply(f"添加频道时出错: {e}")


async def handle_delete_channel(event):
    """处理/deletechannel命令，删除频道"""
    sender_id = event.sender_id
    command = event.text
    logger.info(f"收到命令: {command}，发送者: {sender_id}")
    
    # 检查发送者是否为管理员
    if sender_id not in ADMIN_LIST and ADMIN_LIST != ['me']:
        logger.warning(f"发送者 {sender_id} 没有权限执行命令 {command}")
        await event.reply("您没有权限执行此命令")
        return
    
    try:
        _, channel_url = command.split(maxsplit=1)
        channel_url = channel_url.strip()
        
        if not channel_url:
            await event.reply("请提供有效的频道URL")
            return
        
        # 检查频道是否存在
        if channel_url not in CHANNELS:
            await event.reply(f"频道 {channel_url} 不在列表中")
            return
        
        # 从列表中删除频道
        CHANNELS.remove(channel_url)
        
        # 更新配置文件
        config = load_config()
        config['channels'] = CHANNELS
        save_config(config)
        
        logger.info(f"已从列表中删除频道 {channel_url}")
        await event.reply(f"频道 {channel_url} 已成功从列表中删除\n\n当前频道数量：{len(CHANNELS)}")
        
    except ValueError:
        # 没有提供频道URL或频道不存在
        await event.reply("请提供有效的频道URL，例如：/deletechannel https://t.me/examplechannel")
    except Exception as e:
        logger.error(f"删除频道时出错: {type(e).__name__}: {e}", exc_info=True)
        await event.reply(f"删除频道时出错: {e}")