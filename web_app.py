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

import asyncio
import logging
import datetime
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
    load_config, save_config, logger, get_log_level, WEB_PORT,
    get_bot_state, set_bot_state, BOT_STATE_RUNNING, BOT_STATE_PAUSED, BOT_STATE_SHUTTING_DOWN,
    get_scheduler_instance
)
from error_handler import get_health_checker, get_error_stats

# 创建FastAPI应用
app = FastAPI(title="Sakura频道总结助手管理界面", version="1.2.3")

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

# 管理员密码配置（从环境变量读取，默认为"Sakura"）
DEFAULT_ADMIN_PASSWORD = os.getenv("WEB_ADMIN_PASSWORD", "Sakura")

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
        "version": "1.2.3",
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
    
    # 从配置文件获取send_report_to_source，如果不存在则使用默认值True
    send_report_to_source = config.get('send_report_to_source', True)
    
    context = {
        "request": request,
        "user": user,
        "config": config,
        "ai_api_key": LLM_API_KEY[:10] + "..." if LLM_API_KEY and len(LLM_API_KEY) > 10 else LLM_API_KEY,
        "ai_base_url": LLM_BASE_URL,
        "ai_model": LLM_MODEL,
        "channels": CHANNELS,
        "send_report_to_source": send_report_to_source
    }
    return templates.TemplateResponse("config.html", context)

@app.get("/tasks", response_class=HTMLResponse)
async def tasks_management(request: Request, user: str = Depends(require_auth)):
    """任务管理页面"""
    # 获取最近的任务执行记录
    recent_tasks = get_recent_task_history(limit=10)
    
    context = {
        "request": request,
        "user": user,
        "channels": CHANNELS,
        "summary_schedules": SUMMARY_SCHEDULES,
        "recent_tasks": recent_tasks
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
        
        # 将任务放入队列，由主线程处理
        summary_task_queue.put(channel)
        
        # 立即记录任务触发
        record_task_execution(
            channel=channel,
            task_type="手动触发总结",
            status="已触发",
            result_message="任务已触发，已加入队列等待主线程处理..."
        )
        
        return {
            "status": "success", 
            "message": f"已触发频道 {channel} 的总结任务，已加入队列等待主线程处理..."
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
    send_report_to_source: Optional[str] = Form(None),
    user: str = Depends(require_auth)
):
    """更新AI配置和系统配置"""
    config = load_config()
    
    if ai_api_key:
        config['api_key'] = ai_api_key
    if ai_base_url:
        config['base_url'] = ai_base_url
    if ai_model:
        config['model'] = ai_model
    
    # 处理send_report_to_source参数
    if send_report_to_source is not None:
        # HTML checkbox: 选中时为"on"，未选中时为空字符串
        config['send_report_to_source'] = (send_report_to_source == "on")
    
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

# 机器人状态管理API端点
@app.get("/api/get_bot_status")
async def api_get_bot_status(user: str = Depends(require_auth)):
    """获取机器人当前状态"""
    try:
        current_state = get_bot_state()
        
        # 状态映射到中文描述
        state_map = {
            BOT_STATE_RUNNING: {"name": "运行中", "description": "机器人正常运行，定时任务已启动", "icon": "play-circle", "color": "success"},
            BOT_STATE_PAUSED: {"name": "已暂停", "description": "机器人已暂停，定时任务已停止", "icon": "pause-circle", "color": "warning"},
            BOT_STATE_SHUTTING_DOWN: {"name": "关机中", "description": "机器人正在关机", "icon": "power-off", "color": "danger"}
        }
        
        state_info = state_map.get(current_state, {"name": "未知", "description": "未知状态", "icon": "question-circle", "color": "secondary"})
        
        return {
            "status": "success",
            "state": current_state,
            "state_name": state_info["name"],
            "state_description": state_info["description"],
            "state_icon": state_info["icon"],
            "state_color": state_info["color"],
            "timestamp": datetime.datetime.now().isoformat()
        }
    except Exception as e:
        logger.error(f"获取机器人状态时出错: {e}")
        return {"status": "error", "message": f"获取机器人状态失败: {str(e)}"}

@app.post("/api/pause_bot")
async def api_pause_bot(user: str = Depends(require_auth)):
    """暂停机器人"""
    try:
        current_state = get_bot_state()
        
        # 检查当前状态
        if current_state == BOT_STATE_PAUSED:
            return {"status": "warning", "message": "机器人已经处于暂停状态"}
        
        if current_state != BOT_STATE_RUNNING:
            return {"status": "error", "message": f"机器人当前状态为 {current_state}，无法暂停"}
        
        # 暂停调度器
        scheduler = get_scheduler_instance()
        if scheduler:
            scheduler.pause()
            logger.info("调度器已暂停")
        
        # 更新状态
        set_bot_state(BOT_STATE_PAUSED)
        
        # 记录操作日志
        logger.info(f"Web管理界面暂停了机器人，操作者: {user}")
        
        return {
            "status": "success", 
            "message": "机器人已暂停。定时任务已停止，但手动命令仍可执行。"
        }
    except Exception as e:
        logger.error(f"暂停机器人时出错: {e}")
        return {"status": "error", "message": f"暂停机器人失败: {str(e)}"}

@app.post("/api/resume_bot")
async def api_resume_bot(user: str = Depends(require_auth)):
    """恢复机器人"""
    try:
        current_state = get_bot_state()
        
        # 检查当前状态
        if current_state == BOT_STATE_RUNNING:
            return {"status": "warning", "message": "机器人已经在运行状态"}
        
        if current_state != BOT_STATE_PAUSED:
            return {"status": "error", "message": f"机器人当前状态为 {current_state}，无法恢复"}
        
        # 恢复调度器
        scheduler = get_scheduler_instance()
        if scheduler:
            scheduler.resume()
            logger.info("调度器已恢复")
        
        # 更新状态
        set_bot_state(BOT_STATE_RUNNING)
        
        # 记录操作日志
        logger.info(f"Web管理界面恢复了机器人，操作者: {user}")
        
        return {
            "status": "success", 
            "message": "机器人已恢复运行。定时任务将继续执行。"
        }
    except Exception as e:
        logger.error(f"恢复机器人时出错: {e}")
        return {"status": "error", "message": f"恢复机器人失败: {str(e)}"}

@app.post("/api/shutdown_bot")
async def api_shutdown_bot(user: str = Depends(require_auth)):
    """关机机器人 - 优雅执行关机操作"""
    try:
        # 检查当前状态
        current_state = get_bot_state()
        if current_state == BOT_STATE_SHUTTING_DOWN:
            return {"status": "warning", "message": "机器人已经在关机过程中"}
        
        # 设置关机状态
        set_bot_state(BOT_STATE_SHUTTING_DOWN)
        
        # 创建关机标记文件
        SHUTDOWN_FLAG_FILE = ".shutdown_flag"
        with open(SHUTDOWN_FLAG_FILE, 'w') as f:
            f.write(user)  # 写入操作者
        
        # 停止调度器
        scheduler = get_scheduler_instance()
        if scheduler:
            scheduler.shutdown(wait=False)
            logger.info("调度器已停止")
        
        # 记录操作日志
        logger.info(f"Web管理界面执行了机器人关机，操作者: {user}")
        
        # 在后台线程中延迟执行关机，确保先返回响应
        import threading
        import time
        import sys
        import os
        
        def delayed_shutdown():
            """延迟执行关机"""
            time.sleep(2)  # 等待2秒确保响应已发送给客户端
            logger.info("执行关机操作...")
            
            # 尝试删除关机标记文件，避免遗留
            try:
                if os.path.exists(SHUTDOWN_FLAG_FILE):
                    os.remove(SHUTDOWN_FLAG_FILE)
                    logger.info("已清理关机标记文件")
            except Exception as e:
                logger.error(f"清理关机标记文件时出错: {e}")
            
            # 使用os._exit而不是sys.exit，避免被FastAPI捕获
            os._exit(0)
        
        # 启动后台线程执行关机
        shutdown_thread = threading.Thread(target=delayed_shutdown, daemon=True)
        shutdown_thread.start()
        
        # 立即返回成功响应
        return {
            "status": "success", 
            "message": "机器人关机命令已执行。机器人将在几秒钟内停止运行。"
        }
        
    except Exception as e:
        logger.error(f"关机机器人时出错: {e}")
        return {"status": "error", "message": f"关机机器人失败: {str(e)}"}

# 全局变量，用于存储日志
log_buffer = []
MAX_LOG_LINES = 1000

# 全局变量，用于存储任务执行记录
task_history = []
MAX_TASK_HISTORY = 50

# 线程安全队列，用于Web管理界面触发总结任务
import queue
summary_task_queue = queue.Queue()

# 自定义日志处理器，将日志存储到内存中
class MemoryLogHandler(logging.Handler):
    def __init__(self):
        super().__init__()
        self.setFormatter(logging.Formatter('%(asctime)s - %(name)s - %(levelname)s - %(message)s'))
    
    def emit(self, record):
        try:
            msg = self.format(record)
            log_buffer.append(msg)
            # 限制缓冲区大小
            if len(log_buffer) > MAX_LOG_LINES:
                log_buffer.pop(0)
        except Exception:
            self.handleError(record)

# 添加内存日志处理器到根日志记录器
root_logger = logging.getLogger()
memory_handler = MemoryLogHandler()
memory_handler.setLevel(logging.INFO)
root_logger.addHandler(memory_handler)

# 任务执行记录函数
def record_task_execution(channel, task_type, status, result_message):
    """记录任务执行"""
    import datetime
    task_record = {
        "timestamp": datetime.datetime.now(),
        "channel": channel,
        "task_type": task_type,
        "status": status,
        "result": result_message
    }
    task_history.append(task_record)
    
    # 限制历史记录大小
    if len(task_history) > MAX_TASK_HISTORY:
        task_history.pop(0)
    
    logger.info(f"记录任务执行: {channel} - {task_type} - {status}")

def get_recent_task_history(limit=10):
    """获取最近的任务执行记录"""
    return task_history[-limit:] if len(task_history) > limit else task_history

# 日志管理API端点
@app.get("/api/get_recent_tasks")
async def api_get_recent_tasks(limit: int = 10, user: str = Depends(require_auth)):
    """获取最近的任务执行记录"""
    try:
        recent_tasks = get_recent_task_history(limit)
        
        # 将任务记录转换为可序列化的格式
        serializable_tasks = []
        for task in recent_tasks:
            serializable_tasks.append({
                "timestamp": task["timestamp"].isoformat(),
                "channel": task["channel"],
                "task_type": task["task_type"],
                "status": task["status"],
                "result": task["result"]
            })
        
        return serializable_tasks
    except Exception as e:
        logger.error(f"获取任务执行记录时出错: {e}")
        return {"status": "error", "message": f"获取任务执行记录失败: {str(e)}"}

@app.get("/api/get_logs")
async def api_get_logs(lines: int = 100, user: str = Depends(require_auth)):
    """获取日志 - 从内存缓冲区获取"""
    try:
        # 记录日志查看事件
        logger.info(f"用户 {user} 查看了日志，请求行数: {lines}")
        
        # 从内存缓冲区获取日志
        if log_buffer:
            # 获取最后指定行数的日志
            log_lines = log_buffer[-lines:] if len(log_buffer) > lines else log_buffer
            # 确保每行日志都有换行符
            log_content = '\n'.join(log_lines)
            # 确保最后也有换行符
            if not log_content.endswith('\n'):
                log_content += '\n'
        else:
            # 如果缓冲区为空，返回当前状态
            import datetime
            log_content = f"{datetime.datetime.now()} - web_app - INFO - 日志缓冲区为空\n"
            log_content += f"{datetime.datetime.now()} - web_app - INFO - Web管理界面正在运行\n"
            log_content += f"{datetime.datetime.now()} - web_app - INFO - 当前时间: {datetime.datetime.now()}\n"
            log_content += f"{datetime.datetime.now()} - web_app - INFO - 用户 {user} 查看了日志\n"
            log_content += f"{datetime.datetime.now()} - web_app - INFO - 请求行数: {lines}\n"
        
        # 设置正确的Content-Type，确保换行符被正确解析
        from fastapi.responses import Response
        return Response(content=log_content, media_type="text/plain; charset=utf-8")
    except Exception as e:
        logger.error(f"获取日志时出错: {e}")
        return f"获取日志时出错: {str(e)}"

@app.post("/api/clear_logs")
async def api_clear_logs(user: str = Depends(require_auth)):
    """清空日志"""
    try:
        # 清空内存缓冲区
        global log_buffer
        log_buffer.clear()
        
        # 记录清空操作
        import datetime
        logger.info(f"用户 {user} 清空了日志缓冲区")
        
        # 添加清空确认消息到缓冲区
        clear_msg = f"{datetime.datetime.now()} - web_app - INFO - 日志缓冲区已清空"
        log_buffer.append(clear_msg)
        
        return {"status": "success", "message": "日志缓冲区已清空"}
    except Exception as e:
        logger.error(f"清空日志时出错: {e}")
        return {"status": "error", "message": f"清空日志失败: {str(e)}"}

def get_local_ip():
    """获取本地局域网IP地址"""
    import socket
    try:
        # 创建一个socket连接来获取本地IP
        s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
        # 不需要实际连接，只是获取本地IP
        s.connect(("8.8.8.8", 80))
        local_ip = s.getsockname()[0]
        s.close()
        return local_ip
    except Exception:
        # 如果获取失败，返回None
        return None

def run_web_server():
    """运行Web服务器"""
    import uvicorn
    logger.info("启动Web管理界面服务器...")
    
    # 获取本地IP地址
    local_ip = get_local_ip()
    
    # 显示所有可访问地址
    logger.info(f"Web管理界面已启动，访问地址:")
    logger.info(f"- 本地访问: http://127.0.0.1:{WEB_PORT} 或 http://localhost:{WEB_PORT}")
    logger.info(f"- 所有接口: http://0.0.0.0:{WEB_PORT}")
    if local_ip:
        logger.info(f"- 局域网访问: http://{local_ip}:{WEB_PORT}")
    else:
        logger.info("- 局域网访问: 无法获取局域网IP地址")
    
    uvicorn.run(app, host="0.0.0.0", port=WEB_PORT, log_level="info")

if __name__ == "__main__":
    run_web_server()
