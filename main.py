import asyncio
import logging
import os
from telethon import TelegramClient
from telethon.events import NewMessage
from telethon.tl.functions.bots import SetBotCommandsRequest
from telethon.tl.types import BotCommand, BotCommandScopeDefault
from apscheduler.schedulers.asyncio import AsyncIOScheduler

from config import (
    API_ID, API_HASH, BOT_TOKEN, CHANNELS, LLM_API_KEY,
    RESTART_FLAG_FILE, logger
)
from scheduler import main_job
from command_handlers import (
    handle_manual_summary, handle_show_prompt, handle_set_prompt,
    handle_prompt_input, handle_show_ai_config, handle_set_ai_config,
    handle_ai_config_input, handle_show_log_level, handle_set_log_level,
    handle_restart, handle_show_channels, handle_add_channel,
    handle_delete_channel, handle_clear_summary_time, handle_set_send_to_source
)

async def main():
    logger.info("开始初始化机器人服务...")
    
    try:
        # 初始化调度器
        scheduler = AsyncIOScheduler()
        # 每周一早 9 点执行
        scheduler.add_job(main_job, 'cron', day_of_week='mon', hour=9, minute=0)
        logger.info("定时任务已配置：每周一早上9点执行")
        
        # 启动机器人客户端，处理命令
        logger.info("开始初始化Telegram机器人客户端...")
        client = TelegramClient('bot_session', int(API_ID), API_HASH)
        
        # 添加命令处理，支持中英文命令
        logger.debug("开始添加命令处理器...")
        client.add_event_handler(handle_manual_summary, NewMessage(pattern='/立即总结|/summary'))
        client.add_event_handler(handle_show_prompt, NewMessage(pattern='/showprompt|/show_prompt|/查看提示词'))
        client.add_event_handler(handle_set_prompt, NewMessage(pattern='/setprompt|/set_prompt|/设置提示词'))
        client.add_event_handler(handle_show_ai_config, NewMessage(pattern='/showaicfg|/show_aicfg|/查看AI配置'))
        client.add_event_handler(handle_set_ai_config, NewMessage(pattern='/setaicfg|/set_aicfg|/设置AI配置'))
        # 添加日志级别命令
        client.add_event_handler(handle_show_log_level, NewMessage(pattern='/showloglevel|/show_log_level|/查看日志级别'))
        client.add_event_handler(handle_set_log_level, NewMessage(pattern='/setloglevel|/set_log_level|/设置日志级别'))
        # 添加重启命令
        client.add_event_handler(handle_restart, NewMessage(pattern='/restart|/重启'))
        # 添加频道管理命令
        client.add_event_handler(handle_show_channels, NewMessage(pattern='/showchannels|/show_channels|/查看频道列表'))
        client.add_event_handler(handle_add_channel, NewMessage(pattern='/addchannel|/add_channel|/添加频道'))
        client.add_event_handler(handle_delete_channel, NewMessage(pattern='/deletechannel|/delete_channel|/删除频道'))
        # 添加清除总结时间命令
        client.add_event_handler(handle_clear_summary_time, NewMessage(pattern='/clearsummarytime|/clear_summary_time|/清除总结时间'))
        # 添加设置报告发送回源频道命令
        client.add_event_handler(handle_set_send_to_source, NewMessage(pattern='/setsendtosource|/set_send_to_source|/设置报告发送回源频道'))
        # 只处理非命令消息作为提示词或AI配置输入
        client.add_event_handler(handle_prompt_input, NewMessage(func=lambda e: not e.text.startswith('/')))
        client.add_event_handler(handle_ai_config_input, NewMessage(func=lambda e: True))
        logger.info("命令处理器添加完成")
        
        # 启动客户端
        logger.info("正在启动Telegram机器人客户端...")
        await client.start(bot_token=BOT_TOKEN)
        logger.info("Telegram机器人客户端启动成功")
        
        # 注册机器人命令
        logger.info("开始注册机器人命令...")
        
        commands = [
            BotCommand(command="summary", description="立即生成本周频道消息汇总"),
            BotCommand(command="showprompt", description="查看当前提示词"),
            BotCommand(command="setprompt", description="设置自定义提示词"),
            BotCommand(command="showaicfg", description="查看AI配置"),
            BotCommand(command="setaicfg", description="设置AI配置"),
            BotCommand(command="showloglevel", description="查看当前日志级别"),
            BotCommand(command="setloglevel", description="设置日志级别"),
            BotCommand(command="restart", description="重启机器人"),
            BotCommand(command="showchannels", description="查看当前频道列表"),
            BotCommand(command="addchannel", description="添加频道"),
            BotCommand(command="deletechannel", description="删除频道"),
            BotCommand(command="clearsummarytime", description="清除上次总结时间记录"),
            BotCommand(command="setsendtosource", description="设置是否将报告发送回源频道")
        ]
        
        await client(SetBotCommandsRequest(
            scope=BotCommandScopeDefault(),
            lang_code="zh",
            commands=commands
        ))
        logger.info("机器人命令注册完成")
        
        logger.info("定时监控已启动...")
        logger.info("机器人已启动，正在监听命令...")
        logger.info("机器人命令已注册完成...")
        
        # 启动调度器
        scheduler.start()
        logger.info("调度器已启动")
        
        # 检查是否是重启后的首次运行
        if os.path.exists(RESTART_FLAG_FILE):
            try:
                with open(RESTART_FLAG_FILE, 'r') as f:
                    restart_user_id = int(f.read().strip())
                
                # 发送重启成功消息
                logger.info(f"检测到重启标记，向用户 {restart_user_id} 发送重启成功消息")
                await client.send_message(restart_user_id, "机器人已成功重启！", link_preview=False)
                
                # 删除重启标记文件
                os.remove(RESTART_FLAG_FILE)
                logger.info("重启标记文件已删除")
            except Exception as e:
                logger.error(f"处理重启标记时出错: {type(e).__name__}: {e}", exc_info=True)
        
        # 保持客户端运行
        await client.run_until_disconnected()
    except Exception as e:
        logger.critical(f"机器人服务初始化或运行失败: {type(e).__name__}: {e}", exc_info=True)

if __name__ == "__main__":
    logger.info("===== 机器人服务启动 ====")
    
    # 检查必要变量是否存在
    required_vars = [API_ID, API_HASH, BOT_TOKEN, LLM_API_KEY]
    missing_vars = []
    if not API_ID:
        missing_vars.append("TELEGRAM_API_ID")
    if not API_HASH:
        missing_vars.append("TELEGRAM_API_HASH")
    if not BOT_TOKEN:
        missing_vars.append("TELEGRAM_BOT_TOKEN")
    if not LLM_API_KEY:
        missing_vars.append("LLM_API_KEY 或 DEEPSEEK_API_KEY")
    
    if missing_vars:
        logger.error(f"错误: 请确保 .env 文件中配置了所有必要的 API 凭证。缺少: {', '.join(missing_vars)}")
        print(f"错误: 请确保 .env 文件中配置了所有必要的 API 凭证。缺少: {', '.join(missing_vars)}")
    else:
        logger.info("所有必要的 API 凭证已配置完成")
        # 启动主函数
        try:
            logger.info("开始启动主函数...")
            asyncio.run(main())
        except KeyboardInterrupt:
            logger.info("机器人服务已通过键盘中断停止")
        except Exception as e:
            logger.critical(f"主函数执行失败: {type(e).__name__}: {e}", exc_info=True)
