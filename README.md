# Sakura-频道总结助手

Sakura-频道总结助手是一个基于Telegram API和AI技术的智能频道内容管理工具，专为Telegram频道管理员设计，提供自动化的消息汇总和报告生成服务。支持多种OpenAI兼容的AI服务，包括DeepSeek、OpenAI等。

## 功能特性

- 🔍 **自动消息抓取**：定期监控指定Telegram频道，自动抓取并整理消息内容
- 🤖 **AI智能总结**：利用AI大模型对消息进行深度分析，提取核心要点
- 🔧 **自定义AI配置**：支持通过指令灵活配置API Key、Base URL和Model，兼容多种OpenAI API服务
- ⏰ **定时周报推送**：每周一早上9点自动生成并发送频道周报
- ⚡ **手动触发总结**：支持管理员通过命令随时生成总结
- 🎯 **自定义提示词**：允许管理员设置专属提示词，灵活调整总结风格和重点
- 👥 **多管理员支持**：可配置多个管理员ID，报告将同时发送给所有管理员
- 📝 **长消息分段发送**：智能处理超长总结内容，自动分段发送，确保完整送达

## 技术栈

- Python 3.13
- Telethon（Telegram API客户端）
- OpenAI SDK（调用DeepSeek API）
- APScheduler（定时任务调度）
- python-dotenv（环境变量管理）

## 快速开始

### 环境要求

- Python 3.13或更高版本
- 一个Telegram Bot Token（从@BotFather获取）
- Telegram API ID和API Hash（从https://my.telegram.org 获取）
- 一个OpenAI兼容的API Key（如DeepSeek、OpenAI等）

### 安装依赖

```bash
pip install -r requirements.txt
```

### 配置文件

创建`.env`文件，并填写以下配置：

```env
# Telegram配置
TELEGRAM_API_ID=your_api_id
TELEGRAM_API_HASH=your_api_hash
TELEGRAM_BOT_TOKEN=your_bot_token

# AI配置（支持DeepSeek、OpenAI等OpenAI兼容API）
# 方式1：使用DeepSeek专属配置（兼容旧版本）
DEEPSEEK_API_KEY=your_deepseek_api_key

# 方式2：使用通用LLM配置（推荐，支持任意OpenAI兼容API）
# LLM_API_KEY=your_llm_api_key
# LLM_BASE_URL=https://api.deepseek.com  # 或 https://api.openai.com/v1 等
# LLM_MODEL=deepseek-chat  # 或 gpt-3.5-turbo 等

# 频道配置
TARGET_CHANNEL=https://t.me/your_channel

# 管理员配置（支持多个ID，用逗号分隔）
REPORT_ADMIN_IDS=admin_id1,admin_id2
```

### 运行项目

```bash
python main.py
```

## 使用说明

### 命令列表

| 命令 | 别名 | 功能 |
|------|------|------|
| `/summary` | `/立即总结` | 立即生成本周频道消息汇总 |
| `/showprompt` | `/查看提示词` | 显示当前使用的提示词 |
| `/setprompt` | `/设置提示词` | 设置自定义提示词 |
| `/showaicfg` | `/查看AI配置` | 查看当前AI配置信息 |
| `/setaicfg` | `/设置AI配置` | 设置自定义AI配置（API Key、Base URL、Model） |

### 自定义提示词

1. 发送`/setprompt`或`/设置提示词`命令
2. 机器人会回复当前提示词，并等待您发送新的提示词
3. 发送您想要设置的新提示词
4. 机器人会确认提示词已更新

### AI配置管理

#### 查看AI配置
1. 发送`/showaicfg`或`/查看AI配置`命令
2. 机器人会回复当前AI配置信息，包括API Key（部分隐藏）、Base URL和Model

#### 设置AI配置
1. 发送`/setaicfg`或`/设置AI配置`命令
2. 机器人会引导您依次设置以下参数：
   - API Key（输入新的API Key或发送`/skip`保留当前值）
   - Base URL（输入新的Base URL或发送`/skip`保留当前值）
   - Model（输入新的Model或发送`/skip`保留当前值）
3. 发送`/cancel`可以随时取消设置过程
4. 所有参数设置完成后，机器人会确认配置已更新

## 项目结构

```
tg-bot/
├── main.py          # 主程序文件
├── prompt.txt       # 提示词存储文件
├── config.json      # AI配置存储文件（自动生成）
├── .env             # 环境变量配置
├── .gitignore       # Git忽略规则
├── requirements.txt # 依赖列表
└── README.md        # 项目说明文档
```

## 注意事项

1. 确保您的Bot已被添加到目标频道，并且具有读取消息的权限
2. 首次运行时，需要手动授权登录Telegram账号
3. 请妥善保管您的API凭证，不要泄露给他人
4. 建议定期备份会话文件，避免重复登录
5. 总结内容的质量取决于提示词的设计和AI模型的选择，请根据实际需求调整
6. 支持的AI服务包括DeepSeek、OpenAI以及其他兼容OpenAI API的服务
7. 通过指令设置的AI配置会保存到config.json文件中，重启后依然有效
8. 环境变量和配置文件的优先级：config.json > 环境变量 > 默认值

## 许可证

Apache-2.0 许可证

## 贡献

欢迎提交Issue和Pull Request，一起完善这个项目！

## 联系方式

如有问题或建议，请通过GitHub Issues联系我们。