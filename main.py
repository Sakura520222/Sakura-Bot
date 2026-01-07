import os
import asyncio
import logging
from datetime import datetime, timedelta, timezone
from dotenv import load_dotenv
from telethon import TelegramClient
from telethon.events import NewMessage
from openai import OpenAI
from apscheduler.schedulers.asyncio import AsyncIOScheduler

# 配置日志
logging.basicConfig(
    level=logging.DEBUG,
    format='%(asctime)s - %(name)s - %(levelname)s - %(message)s',
    datefmt='%Y-%m-%d %H:%M:%S'
)
logger = logging.getLogger(__name__)

# 加载 .env 文件中的变量
load_dotenv()
logger.info("已加载 .env 文件中的环境变量")

# 配置文件
PROMPT_FILE = "prompt.txt"
CONFIG_FILE = "config.json"
logger.debug(f"配置文件路径: 提示词文件={PROMPT_FILE}, 配置文件={CONFIG_FILE}")

# 默认提示词
DEFAULT_PROMPT = "请总结以下 Telegram 消息，提取核心要点并列出重要消息的链接：\n\n"

# 读取提示词函数
def load_prompt():
    """从文件中读取提示词，如果文件不存在则使用默认提示词"""
    logger.info(f"开始读取提示词文件: {PROMPT_FILE}")
    try:
        with open(PROMPT_FILE, "r", encoding="utf-8") as f:
            content = f.read().strip()
            logger.info(f"成功读取提示词文件，长度: {len(content)}字符")
            return content
    except FileNotFoundError:
        logger.warning(f"提示词文件 {PROMPT_FILE} 不存在，将使用默认提示词并创建文件")
        # 如果文件不存在，使用默认提示词并创建文件
        save_prompt(DEFAULT_PROMPT)
        return DEFAULT_PROMPT
    except Exception as e:
        logger.error(f"读取提示词文件 {PROMPT_FILE} 时出错: {type(e).__name__}: {e}", exc_info=True)
        # 如果读取失败，使用默认提示词
        return DEFAULT_PROMPT

# 保存提示词函数
def save_prompt(prompt):
    """将提示词保存到文件中"""
    logger.info(f"开始保存提示词到文件: {PROMPT_FILE}")
    try:
        with open(PROMPT_FILE, "w", encoding="utf-8") as f:
            f.write(prompt)
        logger.info(f"成功保存提示词到文件，长度: {len(prompt)}字符")
    except Exception as e:
        logger.error(f"保存提示词到文件 {PROMPT_FILE} 时出错: {type(e).__name__}: {e}", exc_info=True)

# 读取配置文件
def load_config():
    """从配置文件读取AI配置"""
    import json
    logger.info(f"开始读取配置文件: {CONFIG_FILE}")
    try:
        with open(CONFIG_FILE, "r", encoding="utf-8") as f:
            config = json.load(f)
            logger.info(f"成功读取配置文件，配置项数量: {len(config)}")
            return config
    except FileNotFoundError:
        logger.warning(f"配置文件 {CONFIG_FILE} 不存在，返回空配置")
        return {}
    except json.JSONDecodeError as e:
        logger.error(f"配置文件 {CONFIG_FILE} 格式错误: {e}", exc_info=True)
        return {}
    except Exception as e:
        logger.error(f"读取配置文件 {CONFIG_FILE} 时出错: {type(e).__name__}: {e}", exc_info=True)
        return {}

# 保存配置文件
def save_config(config):
    """保存AI配置到文件"""
    import json
    logger.info(f"开始保存配置到文件: {CONFIG_FILE}")
    try:
        with open(CONFIG_FILE, "w", encoding="utf-8") as f:
            json.dump(config, f, ensure_ascii=False, indent=2)
        logger.info(f"成功保存配置到文件，配置项数量: {len(config)}")
    except Exception as e:
        logger.error(f"保存配置到文件 {CONFIG_FILE} 时出错: {type(e).__name__}: {e}", exc_info=True)

# 初始化提示词
CURRENT_PROMPT = load_prompt()
logger.info("已加载提示词配置")
logger.debug(f"当前提示词: {CURRENT_PROMPT[:100]}..." if len(CURRENT_PROMPT) > 100 else f"当前提示词: {CURRENT_PROMPT}")

# 从环境变量中读取配置
logger.info("开始从环境变量加载配置...")
API_ID = os.getenv('TELEGRAM_API_ID')
API_HASH = os.getenv('TELEGRAM_API_HASH')
BOT_TOKEN = os.getenv('TELEGRAM_BOT_TOKEN')
# AI 配置 - 从环境变量获取默认值
LLM_API_KEY = os.getenv('LLM_API_KEY', os.getenv('DEEPSEEK_API_KEY'))
LLM_BASE_URL = os.getenv('LLM_BASE_URL', 'https://api.deepseek.com')
LLM_MODEL = os.getenv('LLM_MODEL', 'deepseek-chat')
TARGET_CHANNEL = os.getenv('TARGET_CHANNEL')

logger.debug(f"从环境变量加载的配置: API_ID={'***' if API_ID else '未设置'}, API_HASH={'***' if API_HASH else '未设置'}, BOT_TOKEN={'***' if BOT_TOKEN else '未设置'}")
logger.debug(f"AI配置 - 环境变量默认值: Base URL={LLM_BASE_URL}, Model={LLM_MODEL}")
logger.debug(f"目标频道: {TARGET_CHANNEL}")

# 管理员 ID 列表，从环境变量读取后转为整数列表
REPORT_ADMIN_IDS = os.getenv('REPORT_ADMIN_IDS', '')
logger.debug(f"从环境变量读取的管理员ID: {REPORT_ADMIN_IDS}")

# 处理管理员ID列表
ADMIN_LIST = []
if REPORT_ADMIN_IDS:
    # 支持多个管理员ID，用逗号分隔
    ADMIN_LIST = [int(admin_id.strip()) for admin_id in REPORT_ADMIN_IDS.split(',')]
    logger.info(f"已从环境变量加载管理员ID列表: {ADMIN_LIST}")
else:
    # 如果没有配置管理员ID，默认发送给自己
    ADMIN_LIST = ['me']
    logger.info("未配置管理员ID，默认发送给机器人所有者")

# 加载配置文件，覆盖环境变量默认值
logger.info("开始加载配置文件...")
config = load_config()
if config:
    logger.debug(f"从配置文件读取的配置: {config}")
    LLM_API_KEY = config.get('api_key', LLM_API_KEY)
    LLM_BASE_URL = config.get('base_url', LLM_BASE_URL)
    LLM_MODEL = config.get('model', LLM_MODEL)
    logger.info("已使用配置文件覆盖AI配置默认值")
else:
    logger.info("未找到配置文件或配置文件为空，使用默认配置")

# 初始化 AI 客户端
logger.info("开始初始化AI客户端...")
logger.debug(f"AI客户端配置: Base URL={LLM_BASE_URL}, Model={LLM_MODEL}, API Key={'***' if LLM_API_KEY else '未设置'}")

client_llm = OpenAI(
    api_key=LLM_API_KEY, 
    base_url=LLM_BASE_URL
)

logger.info("AI客户端初始化完成")

async def fetch_last_week_messages():
    """抓取过去一周的频道消息"""
    # 确保 API_ID 是整数
    logger.info("开始抓取过去一周的频道消息")
    
    async with TelegramClient('session_name', int(API_ID), API_HASH) as client:
        last_week = datetime.now(timezone.utc) - timedelta(days=7)
        messages_list = []
        
        logger.info(f"正在抓取频道: {TARGET_CHANNEL}，时间范围: {last_week} 至今")
        
        message_count = 0
        async for message in client.iter_messages(TARGET_CHANNEL, offset_date=last_week, reverse=True):
            message_count += 1
            if message.text:
                # 动态获取频道名用于生成链接
                channel_part = TARGET_CHANNEL.split('/')[-1]
                msg_link = f"https://t.me/{channel_part}/{message.id}"
                messages_list.append(f"内容: {message.text[:500]}\n链接: {msg_link}")
                
                # 每抓取10条消息记录一次日志
                if len(messages_list) % 10 == 0:
                    logger.debug(f"已抓取 {len(messages_list)} 条有效消息")
        
        logger.info(f"消息抓取完成，共处理 {message_count} 条消息，其中 {len(messages_list)} 条包含文本内容")
        return messages_list

def analyze_with_ai(messages):
    """调用 AI 进行汇总"""
    logger.info("开始调用AI进行消息汇总")
    
    if not messages:
        logger.info("没有需要分析的消息，返回空结果")
        return "本周无新动态。"

    context_text = "\n\n---\n\n".join(messages)
    prompt = f"{CURRENT_PROMPT}{context_text}"
    
    logger.debug(f"AI请求配置: 模型={LLM_MODEL}, 提示词长度={len(CURRENT_PROMPT)}字符, 上下文长度={len(context_text)}字符")
    logger.debug(f"AI请求总长度: {len(prompt)}字符")
    
    try:
        start_time = datetime.now()
        response = client_llm.chat.completions.create(
            model=LLM_MODEL,
            messages=[
                {"role": "system", "content": "你是一个专业的资讯摘要助手，擅长提取重点并保持客观。"},
                {"role": "user", "content": prompt},
            ]
        )
        end_time = datetime.now()
        
        processing_time = (end_time - start_time).total_seconds()
        logger.info(f"AI分析完成，处理时间: {processing_time:.2f}秒")
        logger.debug(f"AI响应状态: 成功，选择索引={response.choices[0].index}, 完成原因={response.choices[0].finish_reason}")
        logger.debug(f"AI响应长度: {len(response.choices[0].message.content)}字符")
        
        return response.choices[0].message.content
    except Exception as e:
        logger.error(f"AI分析失败: {type(e).__name__}: {e}", exc_info=True)
        return f"AI 分析失败: {e}"

async def send_report(summary_text):
    """发送报告"""
    logger.info("开始发送报告")
    logger.debug(f"报告长度: {len(summary_text)}字符")
    
    client = TelegramClient('bot_session', int(API_ID), API_HASH)
    async with client:
        await client.start(bot_token=BOT_TOKEN)
        logger.info("Telegram机器人客户端已启动")
        
        # 向所有管理员发送消息
        for admin_id in ADMIN_LIST:
            try:
                logger.info(f"正在向管理员 {admin_id} 发送报告")
                await send_long_message(client, admin_id, summary_text)
                logger.info(f"成功向管理员 {admin_id} 发送报告")
            except Exception as e:
                logger.error(f"向管理员 {admin_id} 发送报告失败: {type(e).__name__}: {e}", exc_info=True)

async def main_job():
    start_time = datetime.now()
    logger.info(f"定时任务启动: {start_time}")
    
    try:
        messages = await fetch_last_week_messages()
        summary = analyze_with_ai(messages)
        await send_report(f"📋 **频道周报汇总**\n\n{summary}")
        
        end_time = datetime.now()
        processing_time = (end_time - start_time).total_seconds()
        logger.info(f"定时任务完成: {end_time}，总处理时间: {processing_time:.2f}秒")
    except Exception as e:
        end_time = datetime.now()
        processing_time = (end_time - start_time).total_seconds()
        logger.error(f"定时任务执行失败: {type(e).__name__}: {e}，开始时间: {start_time}，结束时间: {end_time}，处理时间: {processing_time:.2f}秒", exc_info=True)

# 全局变量，用于跟踪正在设置提示词的用户
setting_prompt_users = set()
# 全局变量，用于跟踪正在设置AI配置的用户
setting_ai_config_users = set()
# 全局变量，用于存储正在配置中的AI参数
current_ai_config = {}

async def send_long_message(client, chat_id, text, max_length=4000):
    """分段发送长消息"""
    logger.info(f"开始发送长消息，接收者: {chat_id}，消息总长度: {len(text)}字符，最大分段长度: {max_length}字符")
    
    if len(text) <= max_length:
        logger.info(f"消息长度未超过限制，直接发送")
        await client.send_message(chat_id, text)
        return
    
    # 分段发送
    parts = []
    current_part = ""
    
    logger.info(f"消息需要分段发送，开始分段处理")
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
                logger.warning(f"发现超长行，长度: {len(line)}字符，将进一步分割")
                for i in range(0, len(line), max_length):
                    parts.append(line[i:i+max_length])
            else:
                current_part = line + '\n'
    
    # 添加最后一部分
    if current_part:
        parts.append(current_part.strip())
    
    logger.info(f"消息分段完成，共分成 {len(parts)} 段")
    
    # 发送所有部分
    for i, part in enumerate(parts):
        logger.info(f"正在发送第 {i+1}/{len(parts)} 段，长度: {len(part)}字符")
        await client.send_message(chat_id, f"📋 **频道周报汇总 ({i+1}/{len(parts)})**\n\n{part}")
        logger.debug(f"成功发送第 {i+1}/{len(parts)} 段")

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
    await event.reply("正在为您生成本周总结...")
    logger.info(f"开始执行 {command} 命令")
    
    # 执行总结任务
    try:
        messages = await fetch_last_week_messages()
        summary = analyze_with_ai(messages)
        await send_long_message(event.client, sender_id, summary)
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
    await event.reply(f"当前提示词：\n\n{CURRENT_PROMPT}")

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
    await event.reply("请发送新的提示词，我将使用它来生成总结。\n\n当前提示词：\n" + CURRENT_PROMPT)

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
    
    # 更新全局变量和文件
    global CURRENT_PROMPT
    CURRENT_PROMPT = new_prompt
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
    
    # 初始化当前配置
    global current_ai_config
    current_ai_config = {
        'api_key': LLM_API_KEY,
        'base_url': LLM_BASE_URL,
        'model': LLM_MODEL
    }
    
    logger.info(f"开始执行 {command} 命令")
    await event.reply("请依次发送以下AI配置参数，或发送/skip跳过：\n\n1. API Key\n2. Base URL\n3. Model\n\n发送/cancel取消设置")

async def handle_ai_config_input(event):
    """处理用户输入的AI配置参数"""
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
    
    # 获取当前配置状态
    global current_ai_config
    config_step = len([k for k, v in current_ai_config.items() if v is not None]) + 1
    logger.debug(f"当前AI配置步骤: {config_step}")
    
    # 根据当前步骤处理输入
    if config_step == 1:
        # 处理API Key
        if input_text != '/skip':
            current_ai_config['api_key'] = input_text.strip()
            logger.debug(f"用户 {sender_id} 设置了新的API Key: {'***' if input_text.strip() else '未设置'}")
        await event.reply(f"API Key已设置为：{current_ai_config['api_key'][:10]}...{current_ai_config['api_key'][-10:] if len(current_ai_config['api_key']) > 20 else current_ai_config['api_key']}\n\n请发送Base URL，或发送/skip跳过")
    elif config_step == 2:
        # 处理Base URL
        if input_text != '/skip':
            current_ai_config['base_url'] = input_text.strip()
            logger.debug(f"用户 {sender_id} 设置了新的Base URL: {input_text.strip()}")
        await event.reply(f"Base URL已设置为：{current_ai_config['base_url']}\n\n请发送Model，或发送/skip跳过")
    elif config_step == 3:
        # 处理Model
        if input_text != '/skip':
            current_ai_config['model'] = input_text.strip()
            logger.debug(f"用户 {sender_id} 设置了新的Model: {input_text.strip()}")
        
        # 保存配置
        save_config(current_ai_config)
        logger.info("已保存AI配置到文件")
        
        # 更新全局变量
        global LLM_API_KEY, LLM_BASE_URL, LLM_MODEL, client_llm
        LLM_API_KEY = current_ai_config['api_key']
        LLM_BASE_URL = current_ai_config['base_url']
        LLM_MODEL = current_ai_config['model']
        
        # 重新初始化AI客户端
        logger.info("开始重新初始化AI客户端...")
        client_llm = OpenAI(
            api_key=LLM_API_KEY, 
            base_url=LLM_BASE_URL
        )
        logger.info("AI客户端重新初始化完成")
        
        # 从集合中移除用户
        setting_ai_config_users.remove(sender_id)
        logger.info(f"从AI配置设置集合中移除用户 {sender_id}")
        
        # 显示最终配置
        config_info = f"AI配置已更新：\n\n"
        config_info += f"API Key：{LLM_API_KEY[:10]}...{LLM_API_KEY[-10:] if len(LLM_API_KEY) > 20 else LLM_API_KEY}\n"
        config_info += f"Base URL：{LLM_BASE_URL}\n"
        config_info += f"Model：{LLM_MODEL}\n"
        
        logger.info(f"用户 {sender_id} 完成了AI配置设置")
        await event.reply(config_info)

async def main():
    logger.info("开始初始化机器人服务...")
    
    try:
        # 初始化调度器
        scheduler = AsyncIOScheduler()
        # 每周一早 9 点执行
        scheduler.add_job(main_job, 'cron', day_of_week='mon', hour=9, minute=0)
        logger.info("定时任务已配置：每周一早上9点执行")
        
        # 测试运行：启动即运行一次
        # await main_job()
        
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
        logger.info("机器人命令注册完成")
        
        logger.info("定时监控已启动...")
        logger.info("机器人已启动，正在监听命令...")
        logger.info("机器人命令已注册完成...")
        
        # 启动调度器
        scheduler.start()
        logger.info("调度器已启动")
        
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