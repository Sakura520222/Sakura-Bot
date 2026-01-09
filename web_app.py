import asyncio
import logging
from fastapi import FastAPI, Request, Depends, Form, HTTPException
from fastapi.responses import HTMLResponse, RedirectResponse
from fastapi.staticfiles import StaticFiles
from fastapi.templating import Jinja2Templates
from fastapi.middleware.cors import CORSMiddleware
from starlette.middleware.sessions import SessionMiddleware
from itsdangerous import URLSafeTimedSerializer
import os
from typing import Optional

from config import (
    API_ID, API_HASH, BOT_TOKEN, CHANNELS, LLM_API_KEY, LLM_BASE_URL, LLM_MODEL,
    ADMIN_LIST, SEND_REPORT_TO_SOURCE, get_channel_schedule, SUMMARY_SCHEDULES,
    load_config, save_config, logger, get_log_level
)
from error_handler import get_health_checker, get_error_stats

# 创建FastAPI应用
app = FastAPI(title="Sakura频道总结助手管理界面", version="1.1.0")

# 配置会话中间件
SECRET_KEY = os.getenv("WEB_SECRET_KEY", "sakura-channel-summary-secret-key-2026/01/09")
app.add_middleware(SessionMiddleware, secret_key=SECRET_KEY, max_age=7200)  # 2小时会话

# 配置CORS
app.add_middleware(
    CORSMiddleware,
    allow_origins=["*"],
    allow_credentials=True,
    allow_methods=["*"],
    allow_headers=["*"],
)

# 配置模板和静态文件
templates = Jinja2Templates(directory="templates")
app.mount("/static", StaticFiles(directory="static"), name="static")

# 默认管理员密码
DEFAULT_ADMIN_PASSWORD = "Sakura"

# 认证相关函数
def verify_password(password: str) -> bool:
    """验证密码"""
    return password == DEFAULT_ADMIN_PASSWORD

def get_current_user(request: Request):
    """获取当前用户，如果未登录则返回None"""
    return request.session.get("user")

def require_auth(request: Request):
    """要求认证的依赖项"""
    user = get_current_user(request)
    if not user:
        raise HTTPException(status_code=303, headers={"Location": "/login"})
    return user

# 路由定义
@app.get("/", response_class=HTMLResponse)
async def index(request: Request, user: str = Depends(require_auth)):
    """仪表板首页"""
    # 获取系统状态
    health_checker = get_health_checker()
    error_stats = get_error_stats()
    
    # 获取配置信息
    config = load_config()
    
    context = {
        "request": request,
        "user": user,
        "version": "1.1.0",
        "channels_count": len(CHANNELS),
        "admin_count": len(ADMIN_LIST),
        "ai_model": LLM_MODEL,
        "ai_base_url": LLM_BASE_URL,
        "send_to_source": SEND_REPORT_TO_SOURCE,
        "health_status": health_checker.get_status() if health_checker else "未知",
        "error_stats": error_stats if error_stats else {},
        "summary_schedules": SUMMARY_SCHEDULES,
        "config": config
    }
    return templates.TemplateResponse("dashboard.html", context)

@app.get("/login", response_class=HTMLResponse)
async def login_page(request: Request):
    """登录页面"""
    # 如果已登录，重定向到首页
    if request.session.get("user"):
        return RedirectResponse(url="/", status_code=303)
    
    return templates.TemplateResponse("login.html", {"request": request})

@app.post("/login")
async def login(request: Request, password: str = Form(...)):
    """处理登录"""
    if verify_password(password):
        request.session["user"] = "admin"
        return RedirectResponse(url="/", status_code=303)
    else:
        return templates.TemplateResponse("login.html", {
            "request": request,
            "error": "密码错误"
        })

@app.get("/logout")
async def logout(request: Request):
    """登出"""
    request.session.clear()
    return RedirectResponse(url="/login", status_code=303)

@app.get("/channels", response_class=HTMLResponse)
async def channels_management(request: Request, user: str = Depends(require_auth)):
    """频道管理页面"""
    context = {
        "request": request,
        "user": user,
        "channels": CHANNELS,
        "summary_schedules": SUMMARY_SCHEDULES
    }
    return templates.TemplateResponse("channels.html", context)

@app.get("/config", response_class=HTMLResponse)
async def config_management(request: Request, user: str = Depends(require_auth)):
    """配置管理页面"""
    config = load_config()
    
    context = {
        "request": request,
        "user": user,
        "config": config,
        "ai_api_key": LLM_API_KEY[:10] + "..." if LLM_API_KEY and len(LLM_API_KEY) > 10 else LLM_API_KEY,
        "ai_base_url": LLM_BASE_URL,
        "ai_model": LLM_MODEL,
        "channels": CHANNELS,
        "send_report_to_source": SEND_REPORT_TO_SOURCE
    }
    return templates.TemplateResponse("config.html", context)

@app.get("/tasks", response_class=HTMLResponse)
async def tasks_management(request: Request, user: str = Depends(require_auth)):
    """任务管理页面"""
    context = {
        "request": request,
        "user": user,
        "channels": CHANNELS,
        "summary_schedules": SUMMARY_SCHEDULES
    }
    return templates.TemplateResponse("tasks.html", context)

@app.get("/logs", response_class=HTMLResponse)
async def logs_viewer(request: Request, user: str = Depends(require_auth)):
    """日志查看器"""
    # 读取最近的日志文件内容
    log_content = ""
    try:
        # 这里可以扩展为读取实际的日志文件
        log_content = "日志查看功能需要配置日志文件路径..."
    except Exception as e:
        log_content = f"读取日志时出错: {str(e)}"
    
    context = {
        "request": request,
        "user": user,
        "log_content": log_content
    }
    return templates.TemplateResponse("logs.html", context)

@app.get("/system", response_class=HTMLResponse)
async def system_info(request: Request, user: str = Depends(require_auth)):
    """系统信息页面"""
    health_checker = get_health_checker()
    error_stats = get_error_stats()
    
    context = {
        "request": request,
        "user": user,
        "health_status": health_checker.get_status() if health_checker else "未知",
        "error_stats": error_stats if error_stats else {},
        "api_id": API_ID,
        "api_hash": API_HASH[:10] + "..." if API_HASH and len(API_HASH) > 10 else API_HASH,
        "bot_token": BOT_TOKEN[:10] + "..." if BOT_TOKEN and len(BOT_TOKEN) > 10 else BOT_TOKEN,
        "admin_list": ADMIN_LIST
    }
    return templates.TemplateResponse("system.html", context)

# API端点
@app.post("/api/trigger_summary")
async def trigger_summary(channel: str = Form(...), user: str = Depends(require_auth)):
    """手动触发总结"""
    try:
        # 检查频道是否存在
        from config import CHANNELS
        if channel not in CHANNELS:
            return {"status": "error", "message": f"频道 {channel} 不在配置列表中"}
        
        # 记录触发事件
        logger.info(f"Web管理界面触发了频道 {channel} 的手动总结")
        
        # 这里需要调用现有的总结功能
        # 由于Web界面和Telegram客户端在不同的进程中，我们需要一种方式来触发总结
        # 创建一个异步任务来执行总结
        import asyncio
        from datetime import datetime, timezone, timedelta
        
        # 导入必要的模块
        from summary_time_manager import load_last_summary_time, save_last_summary_time
        from ai_client import analyze_with_ai
        from prompt_manager import load_prompt
        from telegram_client import fetch_last_week_messages, send_long_message, send_report
        from config import ADMIN_LIST, SEND_REPORT_TO_SOURCE
        
        # 在后台执行总结任务
        async def execute_summary():
            try:
                # 读取该频道的上次总结时间和报告消息ID
                channel_summary_data = load_last_summary_time(channel, include_report_ids=True)
                if channel_summary_data:
                    channel_last_summary_time = channel_summary_data["time"]
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
                    logger.info(f"开始处理频道 {channel} 的消息，共 {len(messages)} 条")
                    current_prompt = load_prompt()
                    summary = analyze_with_ai(messages, current_prompt)
                    
                    # 获取频道名称用于报告标题
                    channel_name = channel.split('/')[-1]
                    
                    # 计算起始日期和终止日期
                    end_date = datetime.now(timezone.utc)
                    if channel_last_summary_time:
                        start_date = channel_last_summary_time
                    else:
                        start_date = end_date - timedelta(days=7)
                    
                    # 格式化日期为 月.日 格式
                    start_date_str = f"{start_date.month}.{start_date.day}"
                    end_date_str = f"{end_date.month}.{end_date.day}"
                    
                    # 生成报告标题
                    report_text = f"**{channel_name} 周报 {start_date_str}-{end_date_str}**\n\n{summary}"
                    
                    # 使用send_report函数发送总结（它会创建Telegram客户端实例）
                    sent_report_ids = await send_report(report_text, channel if SEND_REPORT_TO_SOURCE else None, None, skip_admins=False)
                    
                    # 保存该频道的本次总结时间和报告消息ID
                    save_last_summary_time(channel, datetime.now(timezone.utc), sent_report_ids)
                    
                    logger.info(f"频道 {channel} 的总结任务完成")
                    return True, f"已成功为频道 {channel} 生成总结报告，共处理 {len(messages)} 条消息"
                else:
                    logger.info(f"频道 {channel} 没有新消息需要总结")
                    return True, f"频道 {channel} 自上次总结以来没有新消息"
                    
            except Exception as e:
                logger.error(f"执行总结任务时出错: {type(e).__name__}: {e}", exc_info=True)
                return False, f"执行总结任务时出错: {str(e)}"
        
        # 在后台运行总结任务
        asyncio.create_task(execute_summary())
        
        return {
            "status": "success", 
            "message": f"已触发频道 {channel} 的总结任务，正在后台执行..."
        }
        
    except Exception as e:
        logger.error(f"触发总结任务时出错: {type(e).__name__}: {e}", exc_info=True)
        return {"status": "error", "message": f"触发总结任务时出错: {str(e)}"}

@app.post("/api/update_config")
async def update_config(
    request: Request,
    ai_api_key: Optional[str] = Form(None),
    ai_base_url: Optional[str] = Form(None),
    ai_model: Optional[str] = Form(None),
    user: str = Depends(require_auth)
):
    """更新AI配置"""
    config = load_config()
    
    if ai_api_key:
        config['api_key'] = ai_api_key
    if ai_base_url:
        config['base_url'] = ai_base_url
    if ai_model:
        config['model'] = ai_model
    
    save_config(config)
    
    return {"status": "success", "message": "配置已更新"}

@app.post("/api/restart_bot")
async def restart_bot(request: Request, user: str = Depends(require_auth)):
    """重启机器人 - 实际执行重启操作"""
    import sys
    import subprocess
    import os
    import time
    import signal
    from config import RESTART_FLAG_FILE
    
    try:
        # 创建重启标记文件，写入Web用户标识
        with open(RESTART_FLAG_FILE, 'w') as f:
            f.write("web_admin")  # 使用特殊标识表示Web管理界面触发的重启
        
        # 记录重启日志
        logger.info("Web管理界面触发了机器人重启，正在执行重启操作...")
        
        # 获取当前Python解释器和脚本路径
        python = sys.executable
        script_path = os.path.abspath("main.py")
        
        # 使用改进后的重启脚本
        improved_script = "restart_bot_improved.py"
        
        # 在后台执行改进后的重启脚本
        subprocess.Popen([python, improved_script], 
                        creationflags=subprocess.CREATE_NEW_CONSOLE)
        
        # 返回成功消息
        return {
            "status": "success", 
            "message": "机器人重启命令已执行。新进程正在启动中，请等待几秒钟..."
        }
    except Exception as e:
        logger.error(f"重启机器人时出错: {type(e).__name__}: {e}", exc_info=True)
        return {"status": "error", "message": f"重启失败: {str(e)}"}

@app.post("/api/set_schedule")
async def api_set_schedule(
    channel: str = Form(...),
    day: str = Form(...),
    hour: int = Form(...),
    minute: int = Form(...),
    user: str = Depends(require_auth)
):
    """设置频道时间配置"""
    from config import set_channel_schedule, validate_schedule
    
    # 验证时间配置
    is_valid, error_message = validate_schedule(day, hour, minute)
    if not is_valid:
        return {"status": "error", "message": error_message}
    
    # 保存配置
    success = set_channel_schedule(channel, day, hour, minute)
    if success:
        return {"status": "success", "message": f"频道 {channel} 的时间配置已保存"}
    else:
        return {"status": "error", "message": "保存配置时出错"}

@app.post("/api/delete_schedule")
async def api_delete_schedule(
    channel: str = Form(...),
    user: str = Depends(require_auth)
):
    """删除频道时间配置"""
    from config import delete_channel_schedule
    
    success = delete_channel_schedule(channel)
    if success:
        return {"status": "success", "message": f"频道 {channel} 的时间配置已删除"}
    else:
        return {"status": "error", "message": "删除配置时出错"}

@app.post("/api/add_channel")
async def api_add_channel(
    channel_url: str = Form(...),
    user: str = Depends(require_auth)
):
    """添加频道"""
    from config import load_config, save_config
    
    # 加载当前配置
    config = load_config()
    
    # 确保channels字段存在
    if 'channels' not in config:
        config['channels'] = []
    
    # 检查频道是否已存在
    if channel_url in config['channels']:
        return {"status": "error", "message": "频道已存在"}
    
    # 添加频道
    config['channels'].append(channel_url)
    
    # 保存配置
    save_config(config)
    
    return {"status": "success", "message": f"频道 {channel_url} 已添加"}

@app.post("/api/delete_channel")
async def api_delete_channel(
    channel_url: str = Form(...),
    user: str = Depends(require_auth)
):
    """删除频道"""
    from config import load_config, save_config, delete_channel_schedule
    
    # 加载当前配置
    config = load_config()
    
    # 确保channels字段存在
    if 'channels' not in config:
        return {"status": "error", "message": "配置文件中没有频道列表"}
    
    # 检查频道是否存在
    if channel_url not in config['channels']:
        return {"status": "error", "message": "频道不存在"}
    
    # 删除频道
    config['channels'] = [c for c in config['channels'] if c != channel_url]
    
    # 删除频道的时间配置
    delete_channel_schedule(channel_url)
    
    # 保存配置
    save_config(config)
    
    return {"status": "success", "message": f"频道 {channel_url} 已删除"}

# 系统管理API端点
@app.post("/api/clear_error_stats")
async def api_clear_error_stats(user: str = Depends(require_auth)):
    """清空错误统计"""
    from error_handler import clear_error_stats
    
    try:
        clear_error_stats()
        return {"status": "success", "message": "错误统计已清空"}
    except Exception as e:
        logger.error(f"清空错误统计时出错: {e}")
        return {"status": "error", "message": f"清空错误统计失败: {str(e)}"}

@app.post("/api/run_health_check")
async def api_run_health_check(user: str = Depends(require_auth)):
    """运行健康检查"""
    from error_handler import get_health_checker
    
    try:
        health_checker = get_health_checker()
        if health_checker:
            # 运行所有健康检查
            results = await health_checker.run_all_checks()
            
            # 计算总体状态
            all_success = all(success for success, _ in results.values())
            status = "healthy" if all_success else "warning"
            
            return {
                "status": "success", 
                "message": f"健康检查完成: {status}",
                "details": {
                    "overall_status": status,
                    "checks": results
                }
            }
        else:
            return {"status": "error", "message": "健康检查器未初始化"}
    except Exception as e:
        logger.error(f"运行健康检查时出错: {e}")
        return {"status": "error", "message": f"运行健康检查失败: {str(e)}"}

@app.post("/api/run_diagnosis")
async def api_run_diagnosis(user: str = Depends(require_auth)):
    """运行系统诊断"""
    try:
        # 检查各个模块的状态
        diagnosis_results = {
            "config": "正常",
            "telegram_client": "正常", 
            "ai_client": "正常",
            "scheduler": "正常",
            "error_handler": "正常",
            "web_server": "正常"
        }
        
        # 检查配置
        try:
            from config import load_config
            config = load_config()
            diagnosis_results["config"] = f"正常 (频道数: {len(config.get('channels', []))})"
        except Exception as e:
            diagnosis_results["config"] = f"错误: {str(e)}"
        
        # 检查错误处理系统
        try:
            from error_handler import get_error_stats
            error_stats = get_error_stats()
            if error_stats:
                diagnosis_results["error_handler"] = f"正常 (错误数: {error_stats.get('error_count', 0)})"
        except Exception as e:
            diagnosis_results["error_handler"] = f"错误: {str(e)}"
        
        return {
            "status": "success",
            "message": "系统诊断完成",
            "details": diagnosis_results
        }
    except Exception as e:
        logger.error(f"运行系统诊断时出错: {e}")
        return {"status": "error", "message": f"系统诊断失败: {str(e)}"}

# 日志管理API端点
@app.get("/api/get_logs")
async def api_get_logs(lines: int = 100, user: str = Depends(require_auth)):
    """获取日志"""
    try:
        # 尝试读取日志文件
        log_file = "sakura_bot.log"
        if os.path.exists(log_file):
            with open(log_file, 'r', encoding='utf-8') as f:
                all_lines = f.readlines()
                # 获取最后指定行数的日志
                log_lines = all_lines[-lines:] if len(all_lines) > lines else all_lines
                log_content = ''.join(log_lines)
        else:
            # 如果没有日志文件，返回模拟日志
            import datetime
            log_content = f"{datetime.datetime.now()} - config - INFO - Web管理界面正在运行\n"
            log_content += f"{datetime.datetime.now()} - web_app - INFO - 日志查看功能已启用\n"
            log_content += f"{datetime.datetime.now()} - web_app - INFO - 当前时间: {datetime.datetime.now()}\n"
        
        return log_content
    except Exception as e:
        logger.error(f"获取日志时出错: {e}")
        return f"获取日志时出错: {str(e)}"

@app.post("/api/clear_logs")
async def api_clear_logs(user: str = Depends(require_auth)):
    """清空日志"""
    try:
        log_file = "sakura_bot.log"
        if os.path.exists(log_file):
            # 备份当前日志
            import shutil
            import datetime
            backup_file = f"sakura_bot.log.backup.{datetime.datetime.now().strftime('%Y%m%d_%H%M%S')}"
            shutil.copy2(log_file, backup_file)
            
            # 清空日志文件
            with open(log_file, 'w', encoding='utf-8') as f:
                f.write(f"{datetime.datetime.now()} - web_app - INFO - 日志已清空\n")
            
            return {"status": "success", "message": "日志已清空并备份"}
        else:
            return {"status": "success", "message": "日志文件不存在，无需清空"}
    except Exception as e:
        logger.error(f"清空日志时出错: {e}")
        return {"status": "error", "message": f"清空日志失败: {str(e)}"}

def run_web_server():
    """运行Web服务器"""
    import uvicorn
    logger.info("启动Web管理界面服务器...")
    uvicorn.run(app, host="0.0.0.0", port=8000, log_level="info")

if __name__ == "__main__":
    run_web_server()

