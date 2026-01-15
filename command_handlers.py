# Copyright 2026 Sakura-频道总结助手
# 
# 本项目采用 CC BY-NC-SA 4.0 许可证
# 您可以自由地共享、修改本作品，但必须遵守以下条件：
# - 署名：必须提供本项目的原始来源链接
# - 非商业：禁止任何商业用途和分发
# - 相同方式共享：衍生作品必须采用相同的许可证
# 
# 本项目源代码：https://github.com/Sakura520222/Sakura-Channel-Summary-Assistant
# 许可证全文：https://creativecommons.org/licenses/by-nc-sa/4.0/deed.zh

import logging
import sys
import subprocess
import os
from datetime import datetime, timezone, timedelta
from telethon.events import NewMessage

from config import (
    CHANNELS, ADMIN_LIST, SEND_REPORT_TO_SOURCE,
    RESTART_FLAG_FILE, load_config, save_config, logger,
    get_channel_schedule, set_channel_schedule, set_channel_schedule_v2,
    delete_channel_schedule, validate_schedule,
    get_channel_poll_config, set_channel_poll_config, delete_channel_poll_config
)
from prompt_manager import load_prompt, save_prompt
from summary_time_manager import load_last_summary_time, save_last_summary_time
from ai_client import analyze_with_ai, client_llm
from telegram_client import fetch_last_week_messages, send_long_message, send_report

# 全局变量，用于跟踪正在设置提示词的用户
setting_prompt_users = set()
# 全局变量，用于跟踪正在设置AI配置的用户
setting_ai_config_users = set()
# 全局变量，用于存储正在配置中的AI参数
current_ai_config = {}

async def handle_manual_summary(event):
    """处理/立即总结命令"""
    sender_id = event.sender_id
    command = event.text
    logger.info(f"收到命令: {command}，发送者: {sender_id}")
    
    # 检查发送者是否为管理员
    if sender_id not in ADMIN_LIST and ADMIN_LIST != ['me']:
        logger.warning(f"发送者 {sender_id} 没有权限执行命令 {command}")
        await event.reply("您没有权限执行此命令")
        return
    
    # 发送正在处理的消息
    await event.reply("正在为您生成总结...")
    logger.info(f"开始执行 {command} 命令")
    
    # 解析命令参数，支持指定频道
    try:
        # 分割命令和参数
        parts = command.split()
        if len(parts) > 1:
            # 有指定频道参数
            specified_channels = []
            for part in parts[1:]:
                if part.startswith('http'):
                    # 完整的频道URL
                    specified_channels.append(part)
                else:
                    # 频道名称，需要转换为完整URL
                    specified_channels.append(f"https://t.me/{part}")
            
            # 验证指定的频道是否在配置中
            valid_channels = []
            for channel in specified_channels:
                if channel in CHANNELS:
                    valid_channels.append(channel)
                else:
                    await event.reply(f"频道 {channel} 不在配置列表中，将跳过")
            
            if not valid_channels:
                await event.reply("没有找到有效的指定频道")
                return
            
            channels_to_process = valid_channels
        else:
            # 没有指定频道，处理所有配置的频道
            channels_to_process = CHANNELS
        
        # 按频道分别处理
        for channel in channels_to_process:
            # 读取该频道的上次总结时间和报告消息ID
            channel_summary_data = load_last_summary_time(channel, include_report_ids=True)
            if channel_summary_data:
                channel_last_summary_time = channel_summary_data["time"]
                # 使用新的键名: summary_message_ids
                # 为了向后兼容,同时支持旧格式
                if "summary_message_ids" in channel_summary_data:
                    # 新格式
                    summary_ids = channel_summary_data["summary_message_ids"]
                    # 类型检查: 如果summary_ids是字典,说明数据格式错误,需要修复
                    if isinstance(summary_ids, dict):
                        logger.warning(f"检测到summary_ids是字典格式,正在修复数据结构: {summary_ids}")
                        summary_ids = summary_ids.get("summary_message_ids", [])
                    # 确保是列表
                    if not isinstance(summary_ids, list):
                        logger.error(f"summary_ids类型错误: {type(summary_ids)}, 值: {summary_ids}, 使用空列表")
                        summary_ids = []

                    poll_ids = channel_summary_data.get("poll_message_ids", [])
                    button_ids = channel_summary_data.get("button_message_ids", [])
                    # 确保都是列表
                    if not isinstance(poll_ids, list):
                        poll_ids = []
                    if not isinstance(button_ids, list):
                        button_ids = []

                    # 合并所有消息ID用于排除
                    report_message_ids_to_exclude = summary_ids + poll_ids + button_ids
                else:
                    # 旧格式,使用report_message_ids
                    report_message_ids_to_exclude = channel_summary_data["report_message_ids"]
            else:
                channel_last_summary_time = None
                report_message_ids_to_exclude = []
            
            # 抓取该频道从上次总结时间开始的消息，排除已发送的报告消息
            messages_by_channel = await fetch_last_week_messages(
                [channel], 
                start_time=channel_last_summary_time,
                report_message_ids={channel: report_message_ids_to_exclude}
            )
            
            # 获取该频道的消息
            messages = messages_by_channel.get(channel, [])
            if messages:
                logger.info(f"开始处理频道 {channel} 的消息")
                current_prompt = load_prompt()
                summary = analyze_with_ai(messages, current_prompt)
                # 获取频道实际名称
                try:
                    channel_entity = await event.client.get_entity(channel)
                    channel_actual_name = channel_entity.title
                    logger.info(f"获取到频道实际名称: {channel_actual_name}")
                except Exception as e:
                    logger.warning(f"获取频道实体失败，使用默认名称: {e}")
                    # 使用频道链接的最后部分作为回退
                    channel_actual_name = channel.split('/')[-1]
                # 计算起始日期和终止日期
                end_date = datetime.now(timezone.utc)
                if channel_last_summary_time:
                    start_date = channel_last_summary_time
                else:
                    start_date = end_date - timedelta(days=7)
                # 格式化日期为 月.日 格式
                start_date_str = f"{start_date.month}.{start_date.day}"
                end_date_str = f"{end_date.month}.{end_date.day}"

                # 获取频道的调度配置，用于生成报告标题
                schedule_config = get_channel_schedule(channel)
                frequency = schedule_config.get('frequency', 'weekly')

                # 根据频率生成报告标题
                if frequency == 'daily':
                    report_title = f"{channel_actual_name} 日报 {end_date_str}"
                else:  # weekly
                    report_title = f"{channel_actual_name} 周报 {start_date_str}-{end_date_str}"

                # 生成报告文本
                report_text = f"**{report_title}**\n\n{summary}"
                # 向请求者发送总结
                await send_long_message(event.client, sender_id, report_text)
                # 根据配置决定是否向源频道发送总结，传递现有客户端实例避免数据库锁定
                # 如果请求者是管理员，跳过向管理员发送报告，避免重复发送
                skip_admins = sender_id in ADMIN_LIST or ADMIN_LIST == ['me']
                sent_report_ids = []
                if SEND_REPORT_TO_SOURCE:
                    sent_report_ids = await send_report(report_text, channel, event.client, skip_admins=skip_admins, message_count=len(messages))
                else:
                    await send_report(report_text, None, event.client, skip_admins=skip_admins, message_count=len(messages))
                
                # 保存该频道的本次总结时间和报告消息ID
                save_last_summary_time(channel, datetime.now(timezone.utc), sent_report_ids)
            else:
                logger.info(f"频道 {channel} 没有新消息需要总结")
                # 获取频道实际名称用于无消息提示
                try:
                    channel_entity = await event.client.get_entity(channel)
                    channel_actual_name = channel_entity.title
                except Exception as e:
                    channel_actual_name = channel.split('/')[-1]
                await send_long_message(event.client, sender_id, f"📋 **{channel_actual_name} 频道汇总**\n\n该频道自上次总结以来没有新消息。")
        
        logger.info(f"命令 {command} 执行成功")
    except Exception as e:
        logger.error(f"执行命令 {command} 时出错: {type(e).__name__}: {e}", exc_info=True)
        await event.reply(f"生成总结时出错: {e}")

async def handle_show_prompt(event):
    """处理/showprompt命令，显示当前提示词"""
    sender_id = event.sender_id
    command = event.text
    logger.info(f"收到命令: {command}，发送者: {sender_id}")
    
    # 检查发送者是否为管理员
    if sender_id not in ADMIN_LIST and ADMIN_LIST != ['me']:
        logger.warning(f"发送者 {sender_id} 没有权限执行命令 {command}")
        await event.reply("您没有权限执行此命令")
        return
    
    logger.info(f"执行命令 {command} 成功")
    current_prompt = load_prompt()
    await event.reply(f"当前提示词：\n\n{current_prompt}")

async def handle_set_prompt(event):
    """处理/setprompt命令，触发提示词设置流程"""
    sender_id = event.sender_id
    command = event.text
    logger.info(f"收到命令: {command}，发送者: {sender_id}")
    
    # 检查发送者是否为管理员
    if sender_id not in ADMIN_LIST and ADMIN_LIST != ['me']:
        logger.warning(f"发送者 {sender_id} 没有权限执行命令 {command}")
        await event.reply("您没有权限执行此命令")
        return
    
    # 添加用户到正在设置提示词的集合中
    setting_prompt_users.add(sender_id)
    logger.info(f"添加用户 {sender_id} 到提示词设置集合")
    current_prompt = load_prompt()
    await event.reply("请发送新的提示词，我将使用它来生成总结。\n\n当前提示词：\n" + current_prompt)

async def handle_prompt_input(event):
    """处理用户输入的新提示词"""
    sender_id = event.sender_id
    input_text = event.text
    
    # 检查发送者是否在设置提示词的集合中
    if sender_id not in setting_prompt_users:
        return
    
    logger.info(f"收到用户 {sender_id} 的提示词输入")
    
    # 检查是否是命令消息，如果是则不处理
    if input_text.startswith('/'):
        logger.warning(f"用户 {sender_id} 发送了命令而非提示词内容: {input_text}")
        await event.reply("请发送提示词内容，不要发送命令。如果要取消设置，请重新发送命令。")
        return
    
    # 获取新提示词
    new_prompt = input_text.strip()
    logger.debug(f"用户 {sender_id} 设置的新提示词: {new_prompt[:100]}..." if len(new_prompt) > 100 else f"用户 {sender_id} 设置的新提示词: {new_prompt}")
    
    # 更新提示词
    save_prompt(new_prompt)
    logger.info(f"已更新提示词，长度: {len(new_prompt)}字符")
    
    # 从集合中移除用户
    setting_prompt_users.remove(sender_id)
    logger.info(f"从提示词设置集合中移除用户 {sender_id}")
    
    await event.reply(f"提示词已更新为：\n\n{new_prompt}")

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
    from config import LLM_API_KEY, LLM_BASE_URL, LLM_MODEL
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
    
    # 添加用户到正在设置AI配置的集合中
    setting_ai_config_users.add(sender_id)
    logger.info(f"添加用户 {sender_id} 到AI配置设置集合")
    
    # 初始化当前配置，使用None值来标识未处理的参数
    global current_ai_config
    current_ai_config = {
        'api_key': None,
        'base_url': None,
        'model': None
    }
    
    logger.info(f"开始执行 {command} 命令")
    await event.reply("请依次发送以下AI配置参数，或发送/skip跳过：\n\n1. API Key\n2. Base URL\n3. Model\n\n发送/cancel取消设置")

async def handle_ai_config_input(event):
    """处理用户输入的AI配置参数"""
    # 声明全局变量
    from config import LLM_API_KEY, LLM_BASE_URL, LLM_MODEL
    
    # 检查发送者是否在设置AI配置的集合中
    sender_id = event.sender_id
    input_text = event.text
    
    if sender_id not in setting_ai_config_users:
        return
    
    logger.info(f"收到用户 {sender_id} 的AI配置输入: {input_text}")
    
    # 检查命令
    if input_text == '/cancel':
        # 取消设置
        setting_ai_config_users.remove(sender_id)
        logger.info(f"用户 {sender_id} 取消了AI配置设置")
        await event.reply("已取消AI配置设置")
        return
    
    # 检查是否是其他命令
    if input_text.startswith('/') and input_text != '/skip':
        # 如果是其他命令，提示用户先完成当前配置或取消
        await event.reply("您正在设置AI配置中，请先完成当前配置或发送/cancel取消设置，然后再执行其他命令")
        return
    
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
            current_ai_config['api_key'] = input_text.strip()
            logger.debug(f"用户 {sender_id} 设置了新的API Key: {'***' if input_text.strip() else '未设置'}")
        else:
            # 使用当前值
            current_ai_config['api_key'] = LLM_API_KEY
        await event.reply(f"API Key已设置为：{current_ai_config['api_key'][:10]}...{current_ai_config['api_key'][-10:] if len(current_ai_config['api_key']) > 20 else current_ai_config['api_key']}\n\n请发送Base URL，或发送/skip跳过")
    elif config_step == 2:
        # 处理Base URL
        if input_text != '/skip':
            current_ai_config['base_url'] = input_text.strip()
            logger.debug(f"用户 {sender_id} 设置了新的Base URL: {input_text.strip()}")
        else:
            # 使用当前值
            current_ai_config['base_url'] = LLM_BASE_URL
        await event.reply(f"Base URL已设置为：{current_ai_config['base_url']}\n\n请发送Model，或发送/skip跳过")
    elif config_step == 3:
        # 处理Model
        if input_text != '/skip':
            current_ai_config['model'] = input_text.strip()
            logger.debug(f"用户 {sender_id} 设置了新的Model: {input_text.strip()}")
        else:
            # 使用当前值
            current_ai_config['model'] = LLM_MODEL
        
        # 保存配置
        save_config(current_ai_config)
        logger.info("已保存AI配置到文件")
        
        # 从集合中移除用户
        setting_ai_config_users.remove(sender_id)
        logger.info(f"从AI配置设置集合中移除用户 {sender_id}")
        
        # 显示最终配置
        config_info = f"AI配置已更新：\n\n"
        config_info += f"API Key：{current_ai_config['api_key'][:10]}...{current_ai_config['api_key'][-10:] if len(current_ai_config['api_key']) > 20 else current_ai_config['api_key']}\n"
        config_info += f"Base URL：{current_ai_config['base_url']}\n"
        config_info += f"Model：{current_ai_config['model']}\n"
        
        logger.info(f"用户 {sender_id} 完成了AI配置设置")
        await event.reply(config_info)
    elif config_step == 4:
        # 所有参数都已设置，可能是重复输入，返回最终配置
        await event.reply("AI配置已完成设置，当前配置：\n\n" + 
                        f"API Key：{current_ai_config['api_key'][:10]}...{current_ai_config['api_key'][-10:] if len(current_ai_config['api_key']) > 20 else current_ai_config['api_key']}\n" +
                        f"Base URL：{current_ai_config['base_url']}\n" +
                        f"Model：{current_ai_config['model']}\n")

async def handle_show_log_level(event):
    """处理/showloglevel命令，显示当前日志级别"""
    sender_id = event.sender_id
    command = event.text
    logger.info(f"收到命令: {command}，发送者: {sender_id}")
    
    # 检查发送者是否为管理员
    if sender_id not in ADMIN_LIST and ADMIN_LIST != ['me']:
        logger.warning(f"发送者 {sender_id} 没有权限执行命令 {command}")
        await event.reply("您没有权限执行此命令")
        return
    
    # 获取当前日志级别
    import logging
    root_logger = logging.getLogger()
    current_level = root_logger.getEffectiveLevel()
    level_name = logging.getLevelName(current_level)
    
    logger.info(f"执行命令 {command} 成功")
    await event.reply(f"当前日志级别：{level_name}\n\n可用日志级别：DEBUG, INFO, WARNING, ERROR, CRITICAL")

async def handle_set_log_level(event):
    """处理/setloglevel命令，设置日志级别"""
    sender_id = event.sender_id
    command = event.text
    logger.info(f"收到命令: {command}，发送者: {sender_id}")
    
    # 检查发送者是否为管理员
    if sender_id not in ADMIN_LIST and ADMIN_LIST != ['me']:
        logger.warning(f"发送者 {sender_id} 没有权限执行命令 {command}")
        await event.reply("您没有权限执行此命令")
        return
    
    # 解析命令参数
    try:
        _, level_str = command.split(maxsplit=1)
        level_str = level_str.strip().upper()
        
        # 检查日志级别是否有效
        from config import LOG_LEVEL_MAP
        if level_str not in LOG_LEVEL_MAP:
            await event.reply(f"无效的日志级别: {level_str}\n\n可用日志级别：DEBUG, INFO, WARNING, ERROR, CRITICAL")
            return
        
        # 设置日志级别
        import logging
        root_logger = logging.getLogger()
        old_level = root_logger.getEffectiveLevel()
        new_level = LOG_LEVEL_MAP[level_str]
        root_logger.setLevel(new_level)
        
        # 更新配置文件
        config = load_config()
        config['log_level'] = level_str
        save_config(config)
        
        logger.info(f"日志级别已从 {logging.getLevelName(old_level)} 更改为 {logging.getLevelName(new_level)}")
        await event.reply(f"日志级别已成功更改为：{level_str}\n\n之前的级别：{logging.getLevelName(old_level)}")
        
    except ValueError:
        # 没有提供日志级别参数
        await event.reply("请提供有效的日志级别，例如：/setloglevel INFO\n\n可用日志级别：DEBUG, INFO, WARNING, ERROR, CRITICAL")
    except Exception as e:
        logger.error(f"设置日志级别时出错: {type(e).__name__}: {e}", exc_info=True)
        await event.reply(f"设置日志级别时出错: {e}")

async def handle_restart(event):
    """处理/restart命令，重启机器人"""
    sender_id = event.sender_id
    command = event.text
    logger.info(f"收到命令: {command}，发送者: {sender_id}")
    
    # 检查发送者是否为管理员
    if sender_id not in ADMIN_LIST and ADMIN_LIST != ['me']:
        logger.warning(f"发送者 {sender_id} 没有权限执行命令 {command}")
        await event.reply("您没有权限执行此命令")
        return
    
    logger.info(f"开始执行 {command} 命令")
    
    # 发送重启确认消息
    await event.reply("正在重启机器人...")
    
    # 记录重启日志
    logger.info("机器人重启命令已执行，正在重启...")
    
    # 实现重启逻辑
    import sys
    import subprocess
    import os
    
    # 创建重启标记文件，用于新进程识别重启操作
    with open(RESTART_FLAG_FILE, 'w') as f:
        f.write(str(sender_id))  # 写入发起重启的用户ID
    
    # 关闭当前进程，启动新进程
    python = sys.executable
    subprocess.Popen([python] + sys.argv)
    
    # 退出当前进程
    sys.exit(0)

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

async def handle_clear_summary_time(event):
    """处理/clearsummarytime命令，清除上次总结时间记录
    支持清除所有频道或特定频道的时间记录
    """
    sender_id = event.sender_id
    command = event.text
    logger.info(f"收到命令: {command}，发送者: {sender_id}")
    
    # 检查发送者是否为管理员
    if sender_id not in ADMIN_LIST and ADMIN_LIST != ['me']:
        logger.warning(f"发送者 {sender_id} 没有权限执行命令 {command}")
        await event.reply("您没有权限执行此命令")
        return
    
    try:
        # 解析命令参数
        parts = command.split()
        specific_channel = None
        if len(parts) > 1:
            # 有指定频道参数
            channel_part = parts[1]
            if channel_part.startswith('http'):
                specific_channel = channel_part
            else:
                specific_channel = f"https://t.me/{channel_part}"
        
        import json
        from config import LAST_SUMMARY_FILE
        if os.path.exists(LAST_SUMMARY_FILE):
            if specific_channel:
                # 清除特定频道的时间记录
                with open(LAST_SUMMARY_FILE, "r", encoding="utf-8") as f:
                    content = f.read().strip()
                    if content:
                        existing_data = json.loads(content)
                        if specific_channel in existing_data:
                            del existing_data[specific_channel]
                            # 写回文件
                            with open(LAST_SUMMARY_FILE, "w", encoding="utf-8") as f_write:
                                json.dump(existing_data, f_write, ensure_ascii=False, indent=2)
                            logger.info(f"已清除频道 {specific_channel} 的上次总结时间记录")
                            await event.reply(f"已成功清除频道 {specific_channel} 的上次总结时间记录。")
                        else:
                            logger.info(f"频道 {specific_channel} 的上次总结时间记录不存在，无需清除")
                            await event.reply(f"频道 {specific_channel} 的上次总结时间记录不存在，无需清除。")
                    else:
                        logger.info(f"上次总结时间记录文件 {LAST_SUMMARY_FILE} 内容为空，无需清除")
                        await event.reply("上次总结时间记录文件内容为空，无需清除。")
            else:
                # 清除所有频道的时间记录
                os.remove(LAST_SUMMARY_FILE)
                logger.info(f"已清除所有频道的上次总结时间记录，文件 {LAST_SUMMARY_FILE} 已删除")
                await event.reply("已成功清除所有频道的上次总结时间记录。下次总结将重新抓取过去一周的消息。")
        else:
            logger.info(f"上次总结时间记录文件 {LAST_SUMMARY_FILE} 不存在，无需清除")
            await event.reply("上次总结时间记录文件不存在，无需清除。")
    except Exception as e:
        logger.error(f"清除上次总结时间记录时出错: {type(e).__name__}: {e}", exc_info=True)
        await event.reply(f"清除上次总结时间记录时出错: {e}")

async def handle_set_send_to_source(event):
    """处理/setsendtosource命令，设置是否将报告发送回源频道"""
    sender_id = event.sender_id
    command = event.text
    logger.info(f"收到命令: {command}，发送者: {sender_id}")
    
    # 检查发送者是否为管理员
    if sender_id not in ADMIN_LIST and ADMIN_LIST != ['me']:
        logger.warning(f"发送者 {sender_id} 没有权限执行命令 {command}")
        await event.reply("您没有权限执行此命令")
        return
    
    # 解析命令参数
    try:
        _, value = command.split(maxsplit=1)
        value = value.strip().lower()
        
        # 检查值是否有效
        if value not in ['true', 'false', '1', '0', 'yes', 'no']:
            await event.reply(f"无效的值: {value}\n\n可用值：true, false, 1, 0, yes, no")
            return
        
        # 转换为布尔值
        from config import SEND_REPORT_TO_SOURCE
        SEND_REPORT_TO_SOURCE = value in ['true', '1', 'yes']
        
        # 更新配置文件
        config = load_config()
        config['send_report_to_source'] = SEND_REPORT_TO_SOURCE
        save_config(config)
        
        logger.info(f"已将send_report_to_source设置为: {SEND_REPORT_TO_SOURCE}")
        await event.reply(f"已成功将报告发送回源频道的设置更改为：{SEND_REPORT_TO_SOURCE}\n\n当前状态：{'开启' if SEND_REPORT_TO_SOURCE else '关闭'}")
        
    except ValueError:
        # 没有提供值，显示当前设置
        from config import SEND_REPORT_TO_SOURCE
        await event.reply(f"当前报告发送回源频道的设置：{SEND_REPORT_TO_SOURCE}\n\n当前状态：{'开启' if SEND_REPORT_TO_SOURCE else '关闭'}\n\n使用格式：/setsendtosource true|false")
    except Exception as e:
        logger.error(f"设置报告发送回源频道选项时出错: {type(e).__name__}: {e}", exc_info=True)
        await event.reply(f"设置报告发送回源频道选项时出错: {e}")


def format_schedule_info(channel, schedule, index=None):
    """格式化调度配置信息

    Args:
        channel: 频道URL
        schedule: 标准化的调度配置字典
        index: 可选的索引编号

    Returns:
        str: 格式化的配置信息字符串
    """
    day_map = {
        'mon': '周一', 'tue': '周二', 'wed': '周三', 'thu': '周四',
        'fri': '周五', 'sat': '周六', 'sun': '周日'
    }

    channel_name = channel.split('/')[-1]
    frequency = schedule.get('frequency', 'weekly')
    hour = schedule['hour']
    minute = schedule['minute']

    if index is not None:
        prefix = f"{index}. "
    else:
        prefix = ""

    if frequency == 'daily':
        return f"{prefix}{channel_name}: 每天 {hour:02d}:{minute:02d}\n"
    elif frequency == 'weekly':
        days_cn = '、'.join([day_map.get(d, d) for d in schedule.get('days', [])])
        return f"{prefix}{channel_name}: 每周{days_cn} {hour:02d}:{minute:02d}\n"
    else:
        return f"{prefix}{channel_name}: 未知频率 {frequency} {hour:02d}:{minute:02d}\n"


async def handle_show_channel_schedule(event):
    """处理/showchannelschedule命令，查看指定频道的自动总结时间配置"""
    sender_id = event.sender_id
    command = event.text
    logger.info(f"收到命令: {command}，发送者: {sender_id}")
    
    # 检查发送者是否为管理员
    if sender_id not in ADMIN_LIST and ADMIN_LIST != ['me']:
        logger.warning(f"发送者 {sender_id} 没有权限执行命令 {command}")
        await event.reply("您没有权限执行此命令")
        return
    
    try:
        # 解析命令参数
        parts = command.split()
        if len(parts) > 1:
            # 有指定频道参数
            channel_part = parts[1]
            if channel_part.startswith('http'):
                channel = channel_part
            else:
                channel = f"https://t.me/{channel_part}"
            
            # 检查频道是否存在
            if channel not in CHANNELS:
                await event.reply(f"频道 {channel} 不在配置列表中")
                return
        else:
            # 没有指定频道，显示所有频道的配置
            if not CHANNELS:
                await event.reply("当前没有配置任何频道")
                return
            
            # 构建所有频道的配置信息
            schedule_msg = "所有频道的自动总结时间配置：\n\n"
            for i, ch in enumerate(CHANNELS, 1):
                schedule = get_channel_schedule(ch)
                schedule_msg += format_schedule_info(ch, schedule, i)

            await event.reply(schedule_msg)
            return
        
        # 获取指定频道的配置
        schedule = get_channel_schedule(channel)

        schedule_info = format_schedule_info(channel, schedule)
        schedule_info += f"\n使用格式：\n"
        schedule_info += f"每天模式：/setchannelschedule {channel.split('/')[-1]} daily 23 0\n"
        schedule_info += f"每周模式：/setchannelschedule {channel.split('/')[-1]} weekly mon,thu 14 30\n"
        schedule_info += f"旧格式：/setchannelschedule {channel.split('/')[-1]} mon 9 0"

        logger.info(f"执行命令 {command} 成功")
        await event.reply(schedule_info)
        
    except Exception as e:
        logger.error(f"查看频道时间配置时出错: {type(e).__name__}: {e}", exc_info=True)
        await event.reply(f"查看频道时间配置时出错: {e}")

async def handle_set_channel_schedule(event):
    """处理/setchannelschedule命令，设置指定频道的自动总结时间（支持新格式）"""
    sender_id = event.sender_id
    command = event.text
    logger.info(f"收到命令: {command}，发送者: {sender_id}")

    # 检查发送者是否为管理员
    if sender_id not in ADMIN_LIST and ADMIN_LIST != ['me']:
        logger.warning(f"发送者 {sender_id} 没有权限执行命令 {command}")
        await event.reply("您没有权限执行此命令")
        return

    try:
        # 解析命令参数
        parts = command.split()
        if len(parts) < 4:
            await event.reply(
                "请提供完整的参数。可用格式：\n\n"
                "每天模式：/setchannelschedule <频道> daily <小时> <分钟>\n"
                "  例如：/setchannelschedule channel daily 23 0\n\n"
                "每周模式：/setchannelschedule <频道> weekly <星期> <小时> <分钟>\n"
                "  例如：/setchannelschedule channel weekly mon,thu 23 0\n"
                "  例如：/setchannelschedule channel weekly sun 9 0\n\n"
                "旧格式（向后兼容）：/setchannelschedule <频道> <星期> <小时> <分钟>\n"
                "  例如：/setchannelschedule channel mon 9 0"
            )
            return

        # 解析频道参数
        channel_part = parts[1]
        if channel_part.startswith('http'):
            channel = channel_part
        else:
            channel = f"https://t.me/{channel_part}"

        # 检查频道是否存在
        if channel not in CHANNELS:
            await event.reply(f"频道 {channel} 不在配置列表中，请先使用/addchannel命令添加频道")
            return

        # 判断是新格式还是旧格式
        frequency_or_day = parts[2].lower()

        if frequency_or_day in ['daily', 'weekly']:
            # 新格式
            frequency = frequency_or_day

            if frequency == 'daily':
                # 每天模式：/setchannelschedule channel daily hour minute
                if len(parts) < 5:
                    await event.reply("每天模式需要提供小时和分钟：/setchannelschedule channel daily 23 0")
                    return

                try:
                    hour = int(parts[3])
                    minute = int(parts[4])
                except ValueError:
                    await event.reply("小时和分钟必须是数字")
                    return

                success = set_channel_schedule_v2(channel, frequency='daily', hour=hour, minute=minute)

                if success:
                    success_msg = f"已成功设置频道 {channel.split('/')[-1]} 的自动总结时间：\n\n"
                    success_msg += f"• 频率：每天\n"
                    success_msg += f"• 时间：{hour:02d}:{minute:02d}\n"
                    success_msg += f"\n下次自动总结将在每天 {hour:02d}:{minute:02d} 执行。"
                    await event.reply(success_msg)
                else:
                    await event.reply("设置失败，请检查日志")

            elif frequency == 'weekly':
                # 每周模式：/setchannelschedule channel weekly mon,thu hour minute
                if len(parts) < 6:
                    await event.reply("每周模式需要提供星期、小时和分钟：/setchannelschedule channel weekly mon,thu 23 0")
                    return

                days_str = parts[3]
                try:
                    hour = int(parts[4])
                    minute = int(parts[5])
                except ValueError:
                    await event.reply("小时和分钟必须是数字")
                    return

                # 解析星期几
                days = [d.strip() for d in days_str.split(',')]

                success = set_channel_schedule_v2(channel, frequency='weekly', days=days, hour=hour, minute=minute)

                if success:
                    day_map = {
                        'mon': '周一', 'tue': '周二', 'wed': '周三', 'thu': '周四',
                        'fri': '周五', 'sat': '周六', 'sun': '周日'
                    }
                    days_cn = '、'.join([day_map.get(d, d) for d in days])

                    success_msg = f"已成功设置频道 {channel.split('/')[-1]} 的自动总结时间：\n\n"
                    success_msg += f"• 频率：每周\n"
                    success_msg += f"• 星期：{days_cn}\n"
                    success_msg += f"• 时间：{hour:02d}:{minute:02d}\n"
                    success_msg += f"\n下次自动总结将在每周{days_cn} {hour:02d}:{minute:02d} 执行。"
                    await event.reply(success_msg)
                else:
                    await event.reply("设置失败，请检查日志")
        else:
            # 旧格式（向后兼容）：/setchannelschedule channel day hour minute
            day = frequency_or_day
            try:
                hour = int(parts[3])
                minute = int(parts[4]) if len(parts) > 4 else 0
            except ValueError:
                await event.reply("小时和分钟必须是数字")
                return

            # 验证时间配置
            is_valid, error_msg = validate_schedule(day, hour, minute)
            if not is_valid:
                await event.reply(error_msg)
                return

            # 使用旧函数设置（内部转换为新格式）
            success = set_channel_schedule(channel, day=day, hour=hour, minute=minute)

            if success:
                day_map = {
                    'mon': '周一', 'tue': '周二', 'wed': '周三', 'thu': '周四',
                    'fri': '周五', 'sat': '周六', 'sun': '周日'
                }
                day_cn = day_map.get(day, day)

                success_msg = f"已成功设置频道 {channel.split('/')[-1]} 的自动总结时间：\n\n"
                success_msg += f"• 星期几：{day_cn} ({day})\n"
                success_msg += f"• 时间：{hour:02d}:{minute:02d}\n"
                success_msg += f"\n下次自动总结将在每周{day_cn} {hour:02d}:{minute:02d}执行。"
                await event.reply(success_msg)
            else:
                await event.reply("设置频道时间配置失败，请检查日志")

    except Exception as e:
        logger.error(f"设置频道时间配置时出错: {type(e).__name__}: {e}", exc_info=True)
        await event.reply(f"设置频道时间配置时出错: {e}")

async def handle_delete_channel_schedule(event):
    """处理/deletechannelschedule命令，删除指定频道的自动总结时间配置"""
    sender_id = event.sender_id
    command = event.text
    logger.info(f"收到命令: {command}，发送者: {sender_id}")
    
    # 检查发送者是否为管理员
    if sender_id not in ADMIN_LIST and ADMIN_LIST != ['me']:
        logger.warning(f"发送者 {sender_id} 没有权限执行命令 {command}")
        await event.reply("您没有权限执行此命令")
        return
    
    try:
        # 解析命令参数
        parts = command.split()
        if len(parts) < 2:
            await event.reply("请提供频道参数：/deletechannelschedule 频道\n\n例如：/deletechannelschedule examplechannel")
            return
        
        # 解析频道参数
        channel_part = parts[1]
        if channel_part.startswith('http'):
            channel = channel_part
        else:
            channel = f"https://t.me/{channel_part}"
        
        # 检查频道是否存在
        if channel not in CHANNELS:
            await event.reply(f"频道 {channel} 不在配置列表中")
            return
        
        # 删除频道时间配置
        success = delete_channel_schedule(channel)
        
        if success:
            success_msg = f"已成功删除频道 {channel.split('/')[-1]} 的自动总结时间配置。\n"
            success_msg += f"该频道将使用默认时间配置：每周一 09:00"
            
            logger.info(f"已删除频道 {channel} 的时间配置")
            await event.reply(success_msg)
        else:
            await event.reply("删除频道时间配置失败，请检查日志")
            
    except Exception as e:
        logger.error(f"删除频道时间配置时出错: {type(e).__name__}: {e}", exc_info=True)
        await event.reply(f"删除频道时间配置时出错: {e}")

async def handle_changelog(event):
    """处理/changelog命令，直接发送变更日志文件"""
    sender_id = event.sender_id
    command = event.text
    logger.info(f"收到命令: {command}，发送者: {sender_id}")
    
    # 检查发送者是否为管理员
    if sender_id not in ADMIN_LIST and ADMIN_LIST != ['me']:
        logger.warning(f"发送者 {sender_id} 没有权限执行命令 {command}")
        await event.reply("您没有权限执行此命令")
        return
    
    try:
        import os
        changelog_file = "CHANGELOG.md"
        
        # 检查文件是否存在
        if not os.path.exists(changelog_file):
            logger.error(f"更新日志文件 {changelog_file} 不存在")
            await event.reply(f"更新日志文件 {changelog_file} 不存在")
            return
        
        # 直接发送文件
        await event.client.send_file(
            sender_id,
            changelog_file,
            caption="📄 项目的完整变更日志文件",
            file_name="CHANGELOG.md"
        )
        
        logger.info(f"已向用户 {sender_id} 发送变更日志文件")
        
    except Exception as e:
        logger.error(f"发送变更日志文件时出错: {type(e).__name__}: {e}", exc_info=True)
        await event.reply(f"发送变更日志文件时出错: {e}")

async def handle_shutdown(event):
    """处理/shutdown命令，彻底停止机器人"""
    sender_id = event.sender_id
    command = event.text
    logger.info(f"收到命令: {command}，发送者: {sender_id}")
    
    # 检查发送者是否为管理员
    if sender_id not in ADMIN_LIST and ADMIN_LIST != ['me']:
        logger.warning(f"发送者 {sender_id} 没有权限执行命令 {command}")
        await event.reply("您没有权限执行此命令")
        return
    
    logger.info(f"开始执行 {command} 命令")
    
    # 发送关机确认消息
    await event.reply("正在关闭机器人...")
    
    # 设置关机状态
    from config import set_bot_state, BOT_STATE_SHUTTING_DOWN
    set_bot_state(BOT_STATE_SHUTTING_DOWN)
    
    # 停止调度器
    from config import get_scheduler_instance
    scheduler = get_scheduler_instance()
    if scheduler:
        scheduler.shutdown(wait=False)
        logger.info("调度器已停止")
    
    # 记录关机日志
    logger.info("机器人关机命令已执行，正在关闭...")
    
    # 向管理员发送关机通知
    try:
        for admin_id in ADMIN_LIST:
            if admin_id != 'me':
                await event.client.send_message(admin_id, "机器人已执行关机命令，正在停止运行...", link_preview=False)
    except Exception as e:
        logger.error(f"发送关机通知失败: {e}")
    
    # 关闭当前进程
    import sys
    import time
    time.sleep(1)  # 等待消息发送完成
    sys.exit(0)

async def handle_pause(event):
    """处理/pause命令，暂停所有定时任务"""
    sender_id = event.sender_id
    command = event.text
    logger.info(f"收到命令: {command}，发送者: {sender_id}")
    
    # 检查发送者是否为管理员
    if sender_id not in ADMIN_LIST and ADMIN_LIST != ['me']:
        logger.warning(f"发送者 {sender_id} 没有权限执行命令 {command}")
        await event.reply("您没有权限执行此命令")
        return
    
    # 检查当前状态
    from config import get_bot_state, set_bot_state, BOT_STATE_RUNNING, BOT_STATE_PAUSED
    current_state = get_bot_state()
    
    if current_state == BOT_STATE_PAUSED:
        await event.reply("机器人已经处于暂停状态")
        return
    
    if current_state != BOT_STATE_RUNNING:
        await event.reply(f"机器人当前状态为 {current_state}，无法暂停")
        return
    
    # 暂停调度器
    from config import get_scheduler_instance
    scheduler = get_scheduler_instance()
    if scheduler:
        scheduler.pause()
        logger.info("调度器已暂停")
    
    # 更新状态
    set_bot_state(BOT_STATE_PAUSED)
    
    logger.info(f"执行命令 {command} 成功")
    await event.reply("机器人已暂停。定时任务已停止，但手动命令仍可执行。\n使用 /resume 或 /恢复 恢复运行。")

async def handle_resume(event):
    """处理/resume命令，恢复所有定时任务"""
    sender_id = event.sender_id
    command = event.text
    logger.info(f"收到命令: {command}，发送者: {sender_id}")

    # 检查发送者是否为管理员
    if sender_id not in ADMIN_LIST and ADMIN_LIST != ['me']:
        logger.warning(f"发送者 {sender_id} 没有权限执行命令 {command}")
        await event.reply("您没有权限执行此命令")
        return

    # 检查当前状态
    from config import get_bot_state, set_bot_state, BOT_STATE_RUNNING, BOT_STATE_PAUSED
    current_state = get_bot_state()

    if current_state == BOT_STATE_RUNNING:
        await event.reply("机器人已经在运行状态")
        return

    if current_state != BOT_STATE_PAUSED:
        await event.reply(f"机器人当前状态为 {current_state}，无法恢复")
        return

    # 恢复调度器
    from config import get_scheduler_instance
    scheduler = get_scheduler_instance()
    if scheduler:
        scheduler.resume()
        logger.info("调度器已恢复")

    # 更新状态
    set_bot_state(BOT_STATE_RUNNING)

    logger.info(f"执行命令 {command} 成功")
    await event.reply("机器人已恢复运行。定时任务将继续执行。")

async def handle_show_channel_poll(event):
    """处理/channelpoll命令，查看指定频道的投票配置"""
    sender_id = event.sender_id
    command = event.text
    logger.info(f"收到命令: {command}，发送者: {sender_id}")

    # 检查发送者是否为管理员
    if sender_id not in ADMIN_LIST and ADMIN_LIST != ['me']:
        logger.warning(f"发送者 {sender_id} 没有权限执行命令 {command}")
        await event.reply("您没有权限执行此命令")
        return

    try:
        # 解析命令参数
        parts = command.split()
        if len(parts) > 1:
            # 有指定频道参数
            channel_part = parts[1]
            if channel_part.startswith('http'):
                channel = channel_part
            else:
                channel = f"https://t.me/{channel_part}"

            # 检查频道是否存在
            if channel not in CHANNELS:
                await event.reply(f"频道 {channel} 不在配置列表中")
                return

            # 获取指定频道的配置
            poll_config = get_channel_poll_config(channel)

            channel_name = channel.split('/')[-1]
            enabled = poll_config['enabled']
            send_to_channel = poll_config['send_to_channel']

            # 格式化启用状态
            if enabled is None:
                enabled_text = "使用全局配置"
            else:
                enabled_text = "启用" if enabled else "禁用"

            # 格式化发送位置
            location_text = "频道" if send_to_channel else "讨论组"

            poll_info = f"频道 {channel_name} 的投票配置：\n\n"
            poll_info += f"• 状态：{enabled_text}\n"
            poll_info += f"• 发送位置：{location_text}\n\n"

            poll_info += f"使用格式：\n"
            poll_info += f"/setchannelpoll {channel_name} true|false channel|discussion\n"
            poll_info += f"/deletechannelpoll {channel_name}"

            logger.info(f"执行命令 {command} 成功")
            await event.reply(poll_info)
        else:
            # 没有指定频道，显示所有频道的配置
            if not CHANNELS:
                await event.reply("当前没有配置任何频道")
                return

            # 构建所有频道的配置信息
            poll_info = "所有频道的投票配置：\n\n"
            for i, ch in enumerate(CHANNELS, 1):
                poll_config = get_channel_poll_config(ch)
                channel_name = ch.split('/')[-1]

                enabled = poll_config['enabled']
                send_to_channel = poll_config['send_to_channel']

                # 格式化启用状态
                if enabled is None:
                    enabled_text = "全局"
                else:
                    enabled_text = "启用" if enabled else "禁用"

                # 格式化发送位置
                location_text = "频道" if send_to_channel else "讨论组"

                poll_info += f"{i}. {channel_name}: {enabled_text} / {location_text}\n"

            await event.reply(poll_info)

    except Exception as e:
        logger.error(f"查看频道投票配置时出错: {type(e).__name__}: {e}", exc_info=True)
        await event.reply(f"查看频道投票配置时出错: {e}")

async def handle_set_channel_poll(event):
    """处理/setchannelpoll命令，设置指定频道的投票配置"""
    sender_id = event.sender_id
    command = event.text
    logger.info(f"收到命令: {command}，发送者: {sender_id}")

    # 检查发送者是否为管理员
    if sender_id not in ADMIN_LIST and ADMIN_LIST != ['me']:
        logger.warning(f"发送者 {sender_id} 没有权限执行命令 {command}")
        await event.reply("您没有权限执行此命令")
        return

    try:
        # 解析命令参数
        parts = command.split()
        if len(parts) < 4:
            await event.reply(
                "请提供完整的参数。可用格式：\n\n"
                "/setchannelpoll <频道> <enabled> <location>\n\n"
                "参数说明：\n"
                "• 频道：频道URL或名称\n"
                "• enabled：true（启用）或 false（禁用）\n"
                "• location：channel（频道）或 discussion（讨论组）\n\n"
                "示例：\n"
                "/setchannelpoll channel1 true channel\n"
                "/setchannelpoll channel1 false discussion\n"
                "/setchannelpoll channel1 false channel"
            )
            return

        # 解析频道参数
        channel_part = parts[1]
        if channel_part.startswith('http'):
            channel = channel_part
        else:
            channel = f"https://t.me/{channel_part}"

        # 检查频道是否存在
        if channel not in CHANNELS:
            await event.reply(f"频道 {channel} 不在配置列表中，请先使用/addchannel命令添加频道")
            return

        # 解析enabled参数
        enabled_str = parts[2].lower()
        if enabled_str in ['true', '1', 'yes']:
            enabled = True
        elif enabled_str in ['false', '0', 'no']:
            enabled = False
        else:
            await event.reply(f"无效的enabled参数: {enabled_str}\n\n有效值：true, false, 1, 0, yes, no")
            return

        # 解析location参数
        location_str = parts[3].lower()
        if location_str in ['channel', 'c']:
            send_to_channel = True
        elif location_str in ['discussion', 'd', 'discuss']:
            send_to_channel = False
        else:
            await event.reply(f"无效的location参数: {location_str}\n\n有效值：channel, discussion")
            return

        # 设置配置
        success = set_channel_poll_config(channel, enabled=enabled, send_to_channel=send_to_channel)

        if success:
            channel_name = channel.split('/')[-1]
            enabled_text = "启用" if enabled else "禁用"
            location_text = "频道" if send_to_channel else "讨论组"

            success_msg = f"已成功设置频道 {channel_name} 的投票配置：\n\n"
            success_msg += f"• 状态：{enabled_text}\n"
            success_msg += f"• 发送位置：{location_text}\n"

            if not enabled:
                success_msg += f"\n注意：投票功能已禁用，不会发送投票。"
            elif send_to_channel:
                success_msg += f"\n注意：投票将直接发送到频道，回复总结消息。"
            else:
                success_msg += f"\n注意：投票将发送到讨论组，回复转发消息。"

            await event.reply(success_msg)
        else:
            await event.reply("设置失败，请检查日志")

    except Exception as e:
        logger.error(f"设置频道投票配置时出错: {type(e).__name__}: {e}", exc_info=True)
        await event.reply(f"设置频道投票配置时出错: {e}")

async def handle_delete_channel_poll(event):
    """处理/deletechannelpoll命令，删除指定频道的投票配置"""
    sender_id = event.sender_id
    command = event.text
    logger.info(f"收到命令: {command}，发送者: {sender_id}")

    # 检查发送者是否为管理员
    if sender_id not in ADMIN_LIST and ADMIN_LIST != ['me']:
        logger.warning(f"发送者 {sender_id} 没有权限执行命令 {command}")
        await event.reply("您没有权限执行此命令")
        return

    try:
        # 解析命令参数
        parts = command.split()
        if len(parts) < 2:
            await event.reply("请提供频道参数：/deletechannelpoll 频道\n\n例如：/deletechannelpoll examplechannel")
            return

        # 解析频道参数
        channel_part = parts[1]
        if channel_part.startswith('http'):
            channel = channel_part
        else:
            channel = f"https://t.me/{channel_part}"

        # 检查频道是否存在
        if channel not in CHANNELS:
            await event.reply(f"频道 {channel} 不在配置列表中")
            return

        # 删除频道投票配置
        success = delete_channel_poll_config(channel)

        if success:
            channel_name = channel.split('/')[-1]
            success_msg = f"已成功删除频道 {channel_name} 的投票配置。\n\n"
            success_msg += f"该频道将使用全局投票配置："

            # 获取全局配置状态
            from config import ENABLE_POLL
            global_enabled = "启用" if ENABLE_POLL else "禁用"
            success_msg += f"\n• 状态：{global_enabled}\n"
            success_msg += f"• 发送位置：讨论组（默认）"

            logger.info(f"已删除频道 {channel} 的投票配置")
            await event.reply(success_msg)
        else:
            await event.reply("删除频道投票配置失败，请检查日志")

    except Exception as e:
        logger.error(f"删除频道投票配置时出错: {type(e).__name__}: {e}", exc_info=True)
        await event.reply(f"删除频道投票配置时出错: {e}")

async def handle_start(event):
    """处理/start命令，显示欢迎消息和帮助信息"""
    sender_id = event.sender_id
    command = event.text
    logger.info(f"收到命令: {command}，发送者: {sender_id}")

    # 不检查管理员权限，所有用户都可以使用 /start 命令

    try:
        # 构建欢迎消息
        welcome_message = """🌸 **欢迎使用 Sakura-频道总结助手**

🤖 我是Telegram智能频道管理助手，专门帮助频道主自动化管理 Telegram 频道内容。

✨ **主要功能**
• 📊 AI智能总结频道消息
• ⏰ 支持每天/每周自动总结
• 🎯 自定义总结风格和频率
• 📝 自动生成投票互动
• 👥 多频道同时管理
• 📜 历史总结记录与查询

📚 **常用命令**

**基础命令**
/start - 查看此欢迎消息
/summary - 立即生成本周汇总

**配置命令**
/showchannels - 查看频道列表
/addchannel - 添加监控频道
/setchannelschedule - 设置自动总结时间

**历史记录** (新功能)
/history - 查看历史总结
/export - 导出历史记录
/stats - 查看统计数据

**管理命令**
/pause - 暂停定时任务
/resume - 恢复定时任务
/changelog - 查看更新日志

💡 **提示**
• 发送 /help 查看完整命令列表
• 更多信息请访问项目[开源仓库](https://github.com/Sakura520222/Sakura-Channel-Summary-Assistant)"""

        await event.reply(welcome_message, link_preview=False)
        logger.info(f"已向用户 {sender_id} 发送欢迎消息")

    except Exception as e:
        logger.error(f"发送欢迎消息时出错: {type(e).__name__}: {e}", exc_info=True)
        await event.reply(f"发送欢迎消息时出错: {e}")


async def handle_clear_cache(event):
    """处理/clearcache命令，清除讨论组ID缓存"""
    sender_id = event.sender_id
    command = event.text

    # 检查管理员权限
    if sender_id not in ADMIN_LIST and ADMIN_LIST != ['me']:
        logger.warning(f"用户 {sender_id} 尝试使用 /clearcache 命令，但没有管理员权限")
        await event.reply("❌ 只有管理员可以清除缓存")
        return

    logger.info(f"收到 /clearcache 命令，发送者: {sender_id}")

    try:
        # 解析命令参数
        parts = command.split()
        if len(parts) > 1:
            # 清除指定频道的缓存
            channel = parts[1]
            from config import clear_discussion_group_cache
            clear_discussion_group_cache(channel)
            await event.reply(f"✅ 已清除频道 {channel} 的讨论组ID缓存")
            logger.info(f"管理员 {sender_id} 清除了频道 {channel} 的讨论组ID缓存")
        else:
            # 清除所有缓存
            from config import clear_discussion_group_cache, LINKED_CHAT_CACHE
            cache_size = len(LINKED_CHAT_CACHE)
            clear_discussion_group_cache()
            await event.reply(f"✅ 已清除所有讨论组ID缓存（共 {cache_size} 条）")
            logger.info(f"管理员 {sender_id} 清除了所有讨论组ID缓存（共 {cache_size} 条）")

    except Exception as e:
        logger.error(f"清除缓存时出错: {type(e).__name__}: {e}", exc_info=True)
        await event.reply(f"❌ 清除缓存时出错: {e}")


async def handle_help(event):
    """处理/help命令，显示完整命令列表和使用说明"""
    sender_id = event.sender_id
    command = event.text
    logger.info(f"收到命令: {command}，发送者: {sender_id}")

    # 不检查管理员权限，所有用户都可以使用 /help 命令

    try:
        # 构建完整帮助消息
        help_message = """📚 **Sakura-频道总结助手 - 完整命令列表**

**🤖 基础命令**
/start - 查看欢迎消息和基本介绍
/help - 查看此完整命令列表
/summary - 立即生成本周频道消息汇总
/changelog - 查看项目更新日志

**⚙️ 提示词管理**
/showprompt - 查看当前使用的提示词
/setprompt - 设置自定义提示词

**🤖 AI 配置**
/showaicfg - 查看当前 AI 配置信息
/setaicfg - 设置自定义 AI 配置（API Key、Base URL、Model）

**📊 日志管理**
/showloglevel - 查看当前日志级别
/setloglevel - 设置日志级别（DEBUG/INFO/WARNING/ERROR/CRITICAL）

**🔄 机器人控制**
/restart - 重启机器人
/shutdown - 彻底停止机器人
/pause - 暂停所有定时任务
/resume - 恢复所有定时任务

**📺 频道管理**
/showchannels - 查看当前监控的频道列表
/addchannel - 添加新频道到监控列表
• 示例：/addchannel https://t.me/examplechannel
/deletechannel - 从监控列表中删除频道
• 示例：/deletechannel https://t.me/examplechannel

**⏰ 时间配置**
/showchannelschedule - 查看频道自动总结时间配置
/setchannelschedule - 设置频道自动总结时间
• 每天：/setchannelschedule 频道 daily 小时 分钟
• 每周：/setchannelschedule 频道 weekly 星期,星期 小时 分钟
/deletechannelschedule - 删除频道自动总结时间配置

**🗑️ 数据管理**
/clearsummarytime - 清除上次总结时间记录

**📤 报告设置**
/setsendtosource - 设置是否将报告发送回源频道

**🗳️ 投票配置**
/channelpoll - 查看频道投票配置
/setchannelpoll - 设置频道投票配置
• 格式：/setchannelpoll 频道 true/false channel/discussion
/deletechannelpoll - 删除频道投票配置

**💾 缓存管理**
/clearcache - 清除讨论组ID缓存
• /clearcache - 清除所有缓存
• /clearcache 频道URL - 清除指定频道缓存

**📜 历史记录** (新功能)
/history - 查看历史总结
• /history - 查看所有频道最近10条
• /history channel1 - 查看指定频道
• /history channel1 30 - 查看最近30天

/export - 导出历史记录
• /export - 导出所有记录为JSON
• /export channel1 csv - 导出为CSV
• /export channel1 md - 导出为md

/stats - 查看统计数据
• /stats - 查看所有频道统计
• /stats channel1 - 查看指定频道统计

---
💡 **提示**
• 大多数命令支持中英文别名
• 配置类命令需要管理员权限
• 使用 /start 查看快速入门指南"""

        await event.reply(help_message, link_preview=False)
        logger.info(f"已向用户 {sender_id} 发送完整帮助信息")

    except Exception as e:
        logger.error(f"发送帮助信息时出错: {type(e).__name__}: {e}", exc_info=True)
        await event.reply(f"发送帮助信息时出错: {e}")
