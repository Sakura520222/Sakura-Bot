import os
import asyncio
from datetime import datetime, timedelta, timezone
from dotenv import load_dotenv
from telethon import TelegramClient
from telethon.events import NewMessage
from openai import OpenAI
from apscheduler.schedulers.asyncio import AsyncIOScheduler

# 加载 .env 文件中的变量
load_dotenv()

# 配置文件
PROMPT_FILE = "prompt.txt"
CONFIG_FILE = "config.json"

# 默认提示词
DEFAULT_PROMPT = "请总结以下 Telegram 消息，提取核心要点并列出重要消息的链接：\n\n"

# 读取提示词函数
def load_prompt():
    """从文件中读取提示词，如果文件不存在则使用默认提示词"""
    try:
        with open(PROMPT_FILE, "r", encoding="utf-8") as f:
            return f.read().strip()
    except FileNotFoundError:
        # 如果文件不存在，使用默认提示词并创建文件
        save_prompt(DEFAULT_PROMPT)
        return DEFAULT_PROMPT

# 保存提示词函数
def save_prompt(prompt):
    """将提示词保存到文件中"""
    with open(PROMPT_FILE, "w", encoding="utf-8") as f:
        f.write(prompt)

# 读取配置文件
def load_config():
    """从配置文件读取AI配置"""
    import json
    try:
        with open(CONFIG_FILE, "r", encoding="utf-8") as f:
            return json.load(f)
    except FileNotFoundError:
        return {}
    except json.JSONDecodeError:
        return {}

# 保存配置文件
def save_config(config):
    """保存AI配置到文件"""
    import json
    with open(CONFIG_FILE, "w", encoding="utf-8") as f:
        json.dump(config, f, ensure_ascii=False, indent=2)

# 初始化提示词
CURRENT_PROMPT = load_prompt()

# 从环境变量中读取配置
API_ID = os.getenv('TELEGRAM_API_ID')
API_HASH = os.getenv('TELEGRAM_API_HASH')
BOT_TOKEN = os.getenv('TELEGRAM_BOT_TOKEN')
# AI 配置 - 从环境变量获取默认值
LLM_API_KEY = os.getenv('LLM_API_KEY', os.getenv('DEEPSEEK_API_KEY'))
LLM_BASE_URL = os.getenv('LLM_BASE_URL', 'https://api.deepseek.com')
LLM_MODEL = os.getenv('LLM_MODEL', 'deepseek-chat')
TARGET_CHANNEL = os.getenv('TARGET_CHANNEL')
# 管理员 ID 列表，从环境变量读取后转为整数列表
REPORT_ADMIN_IDS = os.getenv('REPORT_ADMIN_IDS', '')
# 处理管理员ID列表
ADMIN_LIST = []
if REPORT_ADMIN_IDS:
    # 支持多个管理员ID，用逗号分隔
    ADMIN_LIST = [int(admin_id.strip()) for admin_id in REPORT_ADMIN_IDS.split(',')]
else:
    # 如果没有配置管理员ID，默认发送给自己
    ADMIN_LIST = ['me']

# 加载配置文件，覆盖环境变量默认值
config = load_config()
if config:
    LLM_API_KEY = config.get('api_key', LLM_API_KEY)
    LLM_BASE_URL = config.get('base_url', LLM_BASE_URL)
    LLM_MODEL = config.get('model', LLM_MODEL)

# 初始化 AI 客户端
client_llm = OpenAI(
    api_key=LLM_API_KEY, 
    base_url=LLM_BASE_URL
)

async def fetch_last_week_messages():
    """抓取过去一周的频道消息"""
    # 确保 API_ID 是整数
    async with TelegramClient('session_name', int(API_ID), API_HASH) as client:
        last_week = datetime.now(timezone.utc) - timedelta(days=7)
        messages_list = []
        
        print(f"正在抓取频道: {TARGET_CHANNEL}...")
        
        async for message in client.iter_messages(TARGET_CHANNEL, offset_date=last_week, reverse=True):
            if message.text:
                # 动态获取频道名用于生成链接
                channel_part = TARGET_CHANNEL.split('/')[-1]
                msg_link = f"https://t.me/{channel_part}/{message.id}"
                messages_list.append(f"内容: {message.text[:500]}\n链接: {msg_link}")
        
        return messages_list

def analyze_with_ai(messages):
    """调用 AI 进行汇总"""
    if not messages:
        return "本周无新动态。"

    context_text = "\n\n---\n\n".join(messages)
    
    prompt = f"{CURRENT_PROMPT}{context_text}"

    try:
        response = client_llm.chat.completions.create(
            model=LLM_MODEL,
            messages=[
                {"role": "system", "content": "你是一个专业的资讯摘要助手，擅长提取重点并保持客观。"},
                {"role": "user", "content": prompt},
            ]
        )
        return response.choices[0].message.content
    except Exception as e:
        return f"AI 分析失败: {e}"

async def send_report(summary_text):
    """发送报告"""
    client = TelegramClient('bot_session', int(API_ID), API_HASH)
    async with client:
        await client.start(bot_token=BOT_TOKEN)
        # 向所有管理员发送消息
        for admin_id in ADMIN_LIST:
            try:
                await send_long_message(client, admin_id, summary_text)
                print(f"成功向管理员 {admin_id} 发送报告")
            except Exception as e:
                print(f"向管理员 {admin_id} 发送报告失败: {e}")

async def main_job():
    print(f"任务启动: {datetime.now()}")
    messages = await fetch_last_week_messages()
    summary = analyze_with_ai(messages)
    await send_report(f"📋 **频道周报汇总**\n\n{summary}")

# 全局变量，用于跟踪正在设置提示词的用户
setting_prompt_users = set()
# 全局变量，用于跟踪正在设置AI配置的用户
setting_ai_config_users = set()
# 全局变量，用于存储正在配置中的AI参数
current_ai_config = {}

async def send_long_message(client, chat_id, text, max_length=4000):
    """分段发送长消息"""
    if len(text) <= max_length:
        await client.send_message(chat_id, text)
        return
    
    # 分段发送
    parts = []
    current_part = ""
    for line in text.split('\n'):
        # 检查添加当前行是否超过限制
        if len(current_part) + len(line) + 1 <= max_length:
            current_part += line + '\n'
        else:
            # 如果当前部分不为空，添加到列表
            if current_part:
                parts.append(current_part.strip())
            # 检查当前行是否超过限制
            if len(line) > max_length:
                # 对超长行进行进一步分割
                for i in range(0, len(line), max_length):
                    parts.append(line[i:i+max_length])
            else:
                current_part = line + '\n'
    
    # 添加最后一部分
    if current_part:
        parts.append(current_part.strip())
    
    # 发送所有部分
    for i, part in enumerate(parts):
        await client.send_message(chat_id, f"📋 **频道周报汇总 ({i+1}/{len(parts)})**\n\n{part}")

async def handle_manual_summary(event):
    """处理/立即总结命令"""
    # 检查发送者是否为管理员
    sender_id = event.sender_id
    if sender_id not in ADMIN_LIST and ADMIN_LIST != ['me']:
        await event.reply("您没有权限执行此命令")
        return
    
    # 发送正在处理的消息
    await event.reply("正在为您生成本周总结...")
    
    # 执行总结任务
    try:
        messages = await fetch_last_week_messages()
        summary = analyze_with_ai(messages)
        await send_long_message(event.client, sender_id, summary)
    except Exception as e:
        await event.reply(f"生成总结时出错: {e}")

async def handle_show_prompt(event):
    """处理/showprompt命令，显示当前提示词"""
    # 检查发送者是否为管理员
    sender_id = event.sender_id
    if sender_id not in ADMIN_LIST and ADMIN_LIST != ['me']:
        await event.reply("您没有权限执行此命令")
        return
    
    await event.reply(f"当前提示词：\n\n{CURRENT_PROMPT}")

async def handle_set_prompt(event):
    """处理/setprompt命令，触发提示词设置流程"""
    # 检查发送者是否为管理员
    sender_id = event.sender_id
    if sender_id not in ADMIN_LIST and ADMIN_LIST != ['me']:
        await event.reply("您没有权限执行此命令")
        return
    
    # 添加用户到正在设置提示词的集合中
    setting_prompt_users.add(sender_id)
    await event.reply("请发送新的提示词，我将使用它来生成总结。\n\n当前提示词：\n" + CURRENT_PROMPT)

async def handle_prompt_input(event):
    """处理用户输入的新提示词"""
    # 检查发送者是否在设置提示词的集合中
    sender_id = event.sender_id
    if sender_id not in setting_prompt_users:
        return
    
    # 检查是否是命令消息，如果是则不处理
    if event.text.startswith('/'):
        await event.reply("请发送提示词内容，不要发送命令。如果要取消设置，请重新发送命令。")
        return
    
    # 获取新提示词
    new_prompt = event.text.strip()
    
    # 更新全局变量和文件
    global CURRENT_PROMPT
    CURRENT_PROMPT = new_prompt
    save_prompt(new_prompt)
    
    # 从集合中移除用户
    setting_prompt_users.remove(sender_id)
    
    await event.reply(f"提示词已更新为：\n\n{new_prompt}")

async def handle_show_ai_config(event):
    """处理/showaicfg命令，显示当前AI配置"""
    # 检查发送者是否为管理员
    sender_id = event.sender_id
    if sender_id not in ADMIN_LIST and ADMIN_LIST != ['me']:
        await event.reply("您没有权限执行此命令")
        return
    
    # 显示当前配置
    config_info = f"当前AI配置：\n\n"
    config_info += f"API Key：{LLM_API_KEY[:10]}...{LLM_API_KEY[-10:] if len(LLM_API_KEY) > 20 else LLM_API_KEY}\n"
    config_info += f"Base URL：{LLM_BASE_URL}\n"
    config_info += f"Model：{LLM_MODEL}\n"
    
    await event.reply(config_info)

async def handle_set_ai_config(event):
    """处理/setaicfg命令，触发AI配置设置流程"""
    # 检查发送者是否为管理员
    sender_id = event.sender_id
    if sender_id not in ADMIN_LIST and ADMIN_LIST != ['me']:
        await event.reply("您没有权限执行此命令")
        return
    
    # 添加用户到正在设置AI配置的集合中
    setting_ai_config_users.add(sender_id)
    # 初始化当前配置
    global current_ai_config
    current_ai_config = {
        'api_key': LLM_API_KEY,
        'base_url': LLM_BASE_URL,
        'model': LLM_MODEL
    }
    
    await event.reply("请依次发送以下AI配置参数，或发送/skip跳过：\n\n1. API Key\n2. Base URL\n3. Model\n\n发送/cancel取消设置")

async def handle_ai_config_input(event):
    """处理用户输入的AI配置参数"""
    # 检查发送者是否在设置AI配置的集合中
    sender_id = event.sender_id
    if sender_id not in setting_ai_config_users:
        return
    
    # 检查命令
    if event.text == '/cancel':
        # 取消设置
        setting_ai_config_users.remove(sender_id)
        await event.reply("已取消AI配置设置")
        return
    
    # 获取当前配置状态
    global current_ai_config
    config_step = len([k for k, v in current_ai_config.items() if v is not None]) + 1
    
    # 根据当前步骤处理输入
    if config_step == 1:
        # 处理API Key
        if event.text != '/skip':
            current_ai_config['api_key'] = event.text.strip()
        await event.reply(f"API Key已设置为：{current_ai_config['api_key'][:10]}...{current_ai_config['api_key'][-10:] if len(current_ai_config['api_key']) > 20 else current_ai_config['api_key']}\n\n请发送Base URL，或发送/skip跳过")
    elif config_step == 2:
        # 处理Base URL
        if event.text != '/skip':
            current_ai_config['base_url'] = event.text.strip()
        await event.reply(f"Base URL已设置为：{current_ai_config['base_url']}\n\n请发送Model，或发送/skip跳过")
    elif config_step == 3:
        # 处理Model
        if event.text != '/skip':
            current_ai_config['model'] = event.text.strip()
        
        # 保存配置
        save_config(current_ai_config)
        
        # 更新全局变量
        global LLM_API_KEY, LLM_BASE_URL, LLM_MODEL, client_llm
        LLM_API_KEY = current_ai_config['api_key']
        LLM_BASE_URL = current_ai_config['base_url']
        LLM_MODEL = current_ai_config['model']
        
        # 重新初始化AI客户端
        client_llm = OpenAI(
            api_key=LLM_API_KEY, 
            base_url=LLM_BASE_URL
        )
        
        # 从集合中移除用户
        setting_ai_config_users.remove(sender_id)
        
        # 显示最终配置
        config_info = f"AI配置已更新：\n\n"
        config_info += f"API Key：{LLM_API_KEY[:10]}...{LLM_API_KEY[-10:] if len(LLM_API_KEY) > 20 else LLM_API_KEY}\n"
        config_info += f"Base URL：{LLM_BASE_URL}\n"
        config_info += f"Model：{LLM_MODEL}\n"
        
        await event.reply(config_info)

async def main():
    # 初始化调度器
    scheduler = AsyncIOScheduler()
    # 每周一早 9 点执行
    scheduler.add_job(main_job, 'cron', day_of_week='mon', hour=9, minute=0)
    
    # 测试运行：启动即运行一次
    # await main_job()
    
    # 启动机器人客户端，处理命令
    client = TelegramClient('bot_session', int(API_ID), API_HASH)
    
    # 添加命令处理，支持中英文命令
    client.add_event_handler(handle_manual_summary, NewMessage(pattern='/立即总结|/summary'))
    client.add_event_handler(handle_show_prompt, NewMessage(pattern='/showprompt|/show_prompt|/查看提示词'))
    client.add_event_handler(handle_set_prompt, NewMessage(pattern='/setprompt|/set_prompt|/设置提示词'))
    client.add_event_handler(handle_show_ai_config, NewMessage(pattern='/showaicfg|/show_aicfg|/查看AI配置'))
    client.add_event_handler(handle_set_ai_config, NewMessage(pattern='/setaicfg|/set_aicfg|/设置AI配置'))
    # 只处理非命令消息作为提示词或AI配置输入
    client.add_event_handler(handle_prompt_input, NewMessage(func=lambda e: not e.text.startswith('/')))
    client.add_event_handler(handle_ai_config_input, NewMessage(func=lambda e: True))
    
    # 启动客户端
    await client.start(bot_token=BOT_TOKEN)
    
    # 注册机器人命令
    from telethon.tl.functions.bots import SetBotCommandsRequest
    from telethon.tl.types import BotCommand, BotCommandScopeDefault
    
    commands = [
        BotCommand(command="summary", description="立即生成本周频道消息汇总"),
        BotCommand(command="showprompt", description="查看当前提示词"),
        BotCommand(command="setprompt", description="设置自定义提示词"),
        BotCommand(command="showaicfg", description="查看AI配置"),
        BotCommand(command="setaicfg", description="设置AI配置")
    ]
    
    await client(SetBotCommandsRequest(
        scope=BotCommandScopeDefault(),
        lang_code="zh",
        commands=commands
    ))
    
    print("定时监控已启动...")
    print("机器人已启动，正在监听命令...")
    print("机器人命令已注册完成...")
    
    # 启动调度器
    scheduler.start()
    
    # 保持客户端运行
    await client.run_until_disconnected()

if __name__ == "__main__":
    # 检查必要变量是否存在
    required_vars = [API_ID, API_HASH, BOT_TOKEN, LLM_API_KEY]
    if not all(required_vars):
        print("错误: 请确保 .env 文件中配置了所有必要的 API 凭证。")
    else:
        # 启动主函数
        asyncio.run(main())