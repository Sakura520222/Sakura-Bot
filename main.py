import asyncio
import logging
import os
import sys
import threading
from telethon import TelegramClient
from telethon.events import NewMessage
from telethon.tl.functions.bots import SetBotCommandsRequest
from telethon.tl.types import BotCommand, BotCommandScopeDefault
from apscheduler.schedulers.asyncio import AsyncIOScheduler

from config import (
    API_ID, API_HASH, BOT_TOKEN, CHANNELS, LLM_API_KEY,
    RESTART_FLAG_FILE, logger, get_channel_schedule, ADMIN_LIST, WEB_PORT
)
from scheduler import main_job
from command_handlers import (
    handle_manual_summary, handle_show_prompt, handle_set_prompt,
    handle_prompt_input, handle_show_ai_config, handle_set_ai_config,
    handle_ai_config_input, handle_show_log_level, handle_set_log_level,
    handle_restart, handle_show_channels, handle_add_channel,
    handle_delete_channel, handle_clear_summary_time, handle_set_send_to_source,
    handle_show_channel_schedule, handle_set_channel_schedule, handle_delete_channel_schedule,
    handle_changelog, handle_shutdown, handle_pause, handle_resume
)
from error_handler import initialize_error_handling, get_health_checker, get_error_stats
from web_app import run_web_server

# 版本信息
__version__ = "1.1.6"

async def send_startup_message(client):
    """向所有管理员发送启动消息"""
    try:
        # 构建帮助信息
        help_text = f"""🤖 **Sakura频道总结助手 v{__version__} 已启动**

**核心功能**
• 自动总结频道消息
• 多频道管理
• 自定义提示词
• AI配置调整
• 定时任务调度

**可用命令**
/summary - 立即生成本周频道消息汇总
/showprompt - 查看当前提示词
/setprompt - 设置自定义提示词
/showaicfg - 查看AI配置
/setaicfg - 设置AI配置
/showloglevel - 查看当前日志级别
/setloglevel - 设置日志级别
/restart - 重启机器人
/shutdown - 彻底停止机器人
/pause - 暂停所有定时任务
/resume - 恢复所有定时任务
/showchannels - 查看当前频道列表
/addchannel - 添加频道
/deletechannel - 删除频道
/clearsummarytime - 清除上次总结时间记录
/setsendtosource - 设置是否将报告发送回源频道
/showchannelschedule - 查看频道自动总结时间配置
/setchannelschedule - 设置频道自动总结时间
/deletechannelschedule - 删除频道自动总结时间配置

**版本信息**
当前版本: v{__version__}
查看更新日志: /changelog

机器人运行正常，随时为您服务！"""

        # 向所有管理员发送消息
        for admin_id in ADMIN_LIST:
            try:
                await client.send_message(
                    admin_id,
                    help_text,
                    parse_mode='markdown',
                    link_preview=False
                )
                logger.info(f"已向管理员 {admin_id} 发送启动消息")
            except Exception as e:
                logger.error(f"向管理员 {admin_id} 发送启动消息失败: {type(e).__name__}: {e}")
    except Exception as e:
        logger.error(f"发送启动消息时出错: {type(e).__name__}: {e}", exc_info=True)

async def main():
    logger.info(f"开始初始化机器人服务 v{__version__}...")
    
    try:
        # 启动Web管理界面（在独立线程中）
        logger.info("启动Web管理界面...")
        web_thread = threading.Thread(target=run_web_server, daemon=True)
        web_thread.start()
        
        # 获取本地IP地址用于显示
        import socket
        local_ip = None
        try:
            s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
            s.connect(("8.8.8.8", 80))
            local_ip = s.getsockname()[0]
            s.close()
        except Exception:
            pass
        
        # 显示所有可访问地址
        logger.info(f"Web管理界面已启动，访问地址:")
        logger.info(f"- 本地访问: http://127.0.0.1:{WEB_PORT} 或 http://localhost:{WEB_PORT}")
        logger.info(f"- 所有接口: http://0.0.0.0:{WEB_PORT}")
        if local_ip:
            logger.info(f"- 局域网访问: http://{local_ip}:{WEB_PORT}")
        else:
            logger.info("- 局域网访问: 无法获取局域网IP地址")
        
        # 初始化错误处理系统
        logger.info("初始化错误处理系统...")
        health_checker = initialize_error_handling()
        logger.info("错误处理系统初始化完成")
        
        # 初始化调度器
        scheduler = AsyncIOScheduler()
        
        # 为每个频道配置独立的定时任务
        logger.info(f"开始为 {len(CHANNELS)} 个频道配置定时任务...")
        for channel in CHANNELS:
            # 获取频道的自动总结时间配置
            schedule = get_channel_schedule(channel)
            day = schedule['day']
            hour = schedule['hour']
            minute = schedule['minute']
            
            # 创建定时任务，传入频道参数
            scheduler.add_job(
                main_job,
                'cron',
                day_of_week=day,
                hour=hour,
                minute=minute,
                args=[channel],  # 传入频道参数
                id=f"summary_job_{channel}",  # 唯一ID，便于管理
                replace_existing=True
            )
            
            logger.info(f"频道 {channel} 的定时任务已配置：每周{day} {hour:02d}:{minute:02d}")
        
        logger.info(f"定时任务配置完成：共 {len(CHANNELS)} 个频道")
        
        # 启动机器人客户端，处理命令
        logger.info("开始初始化Telegram机器人客户端...")
        client = TelegramClient('bot_session', int(API_ID), API_HASH)
        
        # 设置活动的客户端实例，供其他模块使用
        from telegram_client import set_active_client
        set_active_client(client)
        
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
        # 添加关机命令
        client.add_event_handler(handle_shutdown, NewMessage(pattern='/shutdown|/关机'))
        # 添加暂停命令
        client.add_event_handler(handle_pause, NewMessage(pattern='/pause|/暂停'))
        # 添加恢复命令
        client.add_event_handler(handle_resume, NewMessage(pattern='/resume|/恢复'))
        # 添加频道管理命令
        client.add_event_handler(handle_show_channels, NewMessage(pattern='/showchannels|/show_channels|/查看频道列表'))
        client.add_event_handler(handle_add_channel, NewMessage(pattern='/addchannel|/add_channel|/添加频道'))
        client.add_event_handler(handle_delete_channel, NewMessage(pattern='/deletechannel|/delete_channel|/删除频道'))
        # 添加清除总结时间命令
        client.add_event_handler(handle_clear_summary_time, NewMessage(pattern='/clearsummarytime|/clear_summary_time|/清除总结时间'))
        # 添加设置报告发送回源频道命令
        client.add_event_handler(handle_set_send_to_source, NewMessage(pattern='/setsendtosource|/set_send_to_source|/设置报告发送回源频道'))
        # 添加频道时间配置命令
        client.add_event_handler(handle_show_channel_schedule, NewMessage(pattern='/showchannelschedule|/show_channel_schedule|/查看频道时间配置'))
        client.add_event_handler(handle_set_channel_schedule, NewMessage(pattern='/setchannelschedule|/set_channel_schedule|/设置频道时间配置'))
        client.add_event_handler(handle_delete_channel_schedule, NewMessage(pattern='/deletechannelschedule|/delete_channel_schedule|/删除频道时间配置'))
        # 添加更新日志命令
        client.add_event_handler(handle_changelog, NewMessage(pattern='/changelog|/更新日志'))
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
            BotCommand(command="shutdown", description="彻底停止机器人"),
            BotCommand(command="pause", description="暂停所有定时任务"),
            BotCommand(command="resume", description="恢复所有定时任务"),
            BotCommand(command="showchannels", description="查看当前频道列表"),
            BotCommand(command="addchannel", description="添加频道"),
            BotCommand(command="deletechannel", description="删除频道"),
            BotCommand(command="clearsummarytime", description="清除上次总结时间记录"),
            BotCommand(command="setsendtosource", description="设置是否将报告发送回源频道"),
            BotCommand(command="showchannelschedule", description="查看频道自动总结时间配置"),
            BotCommand(command="setchannelschedule", description="设置频道自动总结时间"),
            BotCommand(command="deletechannelschedule", description="删除频道自动总结时间配置"),
            BotCommand(command="changelog", description="查看更新日志")
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
        
        # 存储调度器实例到config模块，供其他模块访问
        from config import set_scheduler_instance
        set_scheduler_instance(scheduler)
        logger.info("调度器实例已存储到config模块")
        
        # 向管理员发送启动消息
        logger.info("开始向管理员发送启动消息...")
        await send_startup_message(client)
        logger.info("启动消息发送完成")
        
        # 检查是否是重启后的首次运行
        if os.path.exists(RESTART_FLAG_FILE):
            try:
                with open(RESTART_FLAG_FILE, 'r') as f:
                    content = f.read().strip()
                
                # 尝试解析为用户ID
                try:
                    restart_user_id = int(content)
                    # 发送重启成功消息给特定用户
                    logger.info(f"检测到重启标记，向用户 {restart_user_id} 发送重启成功消息")
                    await client.send_message(restart_user_id, "机器人已成功重启！", link_preview=False)
                except ValueError:
                    # 如果不是整数，可能是特殊标识（如web_admin）
                    logger.info(f"检测到重启标记，特殊标识: {content}")
                    if content == "web_admin":
                        # Web管理界面触发的重启，向所有管理员发送通知
                        for admin_id in ADMIN_LIST:
                            try:
                                await client.send_message(
                                    admin_id, 
                                    "🤖 机器人已通过Web管理界面成功重启！", 
                                    link_preview=False
                                )
                                logger.info(f"已向管理员 {admin_id} 发送Web重启通知")
                            except Exception as e:
                                logger.error(f"向管理员 {admin_id} 发送重启通知失败: {e}")
                
                # 删除重启标记文件
                os.remove(RESTART_FLAG_FILE)
                logger.info("重启标记文件已删除")
            except Exception as e:
                logger.error(f"处理重启标记时出错: {type(e).__name__}: {e}", exc_info=True)
        
        # 检查关机标记文件
        SHUTDOWN_FLAG_FILE = ".shutdown_flag"
        if os.path.exists(SHUTDOWN_FLAG_FILE):
            try:
                with open(SHUTDOWN_FLAG_FILE, 'r') as f:
                    shutdown_user = f.read().strip()
                
                logger.info(f"检测到关机标记，操作者: {shutdown_user}")
                
                # 向所有管理员发送关机通知
                for admin_id in ADMIN_LIST:
                    try:
                        await client.send_message(
                            admin_id,
                            "🤖 机器人已通过Web管理界面执行关机命令，正在停止运行...",
                            link_preview=False
                        )
                        logger.info(f"已向管理员 {admin_id} 发送关机通知")
                    except Exception as e:
                        logger.error(f"向管理员 {admin_id} 发送关机通知失败: {e}")
                
                # 删除关机标记文件
                os.remove(SHUTDOWN_FLAG_FILE)
                logger.info("关机标记文件已删除")
                
                # 等待消息发送完成
                import time
                time.sleep(2)
                
                # 执行关机
                logger.info("执行关机操作...")
                sys.exit(0)
                
            except Exception as e:
                logger.error(f"处理关机标记时出错: {type(e).__name__}: {e}", exc_info=True)
                # 即使出错也尝试删除关机标记文件，避免遗留
                try:
                    if os.path.exists(SHUTDOWN_FLAG_FILE):
                        os.remove(SHUTDOWN_FLAG_FILE)
                        logger.info("出错后已清理关机标记文件")
                except Exception as cleanup_error:
                    logger.error(f"清理关机标记文件时出错: {cleanup_error}")
        
        # 启动一个后台任务来检查Web管理界面触发的总结任务
        async def check_web_summary_tasks():
            """检查Web管理界面触发的总结任务"""
            from web_app import summary_task_queue
            import asyncio
            
            while True:
                try:
                    # 检查队列中是否有任务
                    if not summary_task_queue.empty():
                        channel = summary_task_queue.get()
                        logger.info(f"从Web管理界面接收到总结任务: {channel}")
                        
                        try:
                            # 执行总结任务并获取详细结果
                            from scheduler import main_job
                            result = await main_job(channel)
                            
                            # 根据结果更新任务执行记录
                            from web_app import record_task_execution
                            
                            if result["success"]:
                                # 任务执行成功，使用详细结果信息
                                status = "成功"
                                if result["message_count"] > 0:
                                    result_message = f"✅ 总结任务成功完成\n"
                                    result_message += f"• 频道: {result['channel']}\n"
                                    result_message += f"• 处理消息: {result['message_count']} 条\n"
                                    result_message += f"• 总结长度: {result['summary_length']} 字符\n"
                                    result_message += f"• 处理时间: {result['processing_time']:.2f} 秒\n"
                                    result_message += f"• 详情: {result['details']}"
                                else:
                                    result_message = f"ℹ️ 没有新消息需要总结\n"
                                    result_message += f"• 频道: {result['channel']}\n"
                                    result_message += f"• 处理时间: {result['processing_time']:.2f} 秒\n"
                                    result_message += f"• 详情: {result['details']}"
                                
                                logger.info(f"Web管理界面触发的总结任务成功: {result['details']}")
                            else:
                                # 任务执行失败
                                status = "失败"
                                result_message = f"❌ 总结任务执行失败\n"
                                result_message += f"• 频道: {result['channel']}\n"
                                result_message += f"• 错误: {result['error']}\n"
                                result_message += f"• 处理时间: {result['processing_time']:.2f} 秒\n"
                                result_message += f"• 详情: {result['details']}"
                                
                                logger.error(f"Web管理界面触发的总结任务失败: {result['error']}")
                            
                            # 更新任务执行记录
                            record_task_execution(
                                channel=channel,
                                task_type="手动触发总结",
                                status=status,
                                result_message=result_message
                            )
                            
                        except Exception as e:
                            logger.error(f"执行总结任务时出错: {e}")
                            
                            # 任务执行失败，更新任务执行记录
                            from web_app import record_task_execution
                            record_task_execution(
                                channel=channel,
                                task_type="手动触发总结",
                                status="失败",
                                result_message=f"❌ 总结任务执行失败\n• 频道: {channel}\n• 错误: {str(e)}\n• 详情: 执行过程中发生未预期的错误"
                            )
                except Exception as e:
                    logger.error(f"处理Web管理界面总结任务时出错: {e}")
                
                # 每秒检查一次
                await asyncio.sleep(1)
        
        # 启动后台任务
        asyncio.create_task(check_web_summary_tasks())
        
        # 保持客户端运行
        await client.run_until_disconnected()
    except Exception as e:
        logger.critical(f"机器人服务初始化或运行失败: {type(e).__name__}: {e}", exc_info=True)

if __name__ == "__main__":
    logger.info(f"===== Sakura频道总结助手 v{__version__} 启动 ====")
    
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
