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
- 📊 **灵活日志级别控制**：支持通过环境变量、配置文件或Telegram指令动态调整日志级别，便于调试和生产环境使用
- 🌐 **多频道支持**：支持配置多个Telegram频道，同时监控和总结多个频道的内容
- 📋 **频道管理命令**：提供便捷的指令用于查看、添加和删除频道，方便管理员管理频道列表
- ⏱️ **智能总结时间记录**：自动记录每次总结的时间，下次总结时仅获取自上次总结以来的新消息，提高总结效率和准确性

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

# 日志级别配置（可选，默认DEBUG）
# 可用值：DEBUG, INFO, WARNING, ERROR, CRITICAL
LOG_LEVEL=INFO
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
| `/showloglevel` | `/查看日志级别` | 查看当前日志级别 |
| `/setloglevel <级别>` | `/设置日志级别 <级别>` | 设置日志级别（支持DEBUG、INFO、WARNING、ERROR、CRITICAL） |
| `/restart` | `/重启` | 重启机器人 |
| `/showchannels` | `/查看频道列表` | 查看当前配置的所有频道 |
| `/addchannel <频道URL>` | `/添加频道 <频道URL>` | 添加新的频道到监控列表 |
| `/deletechannel <频道URL>` | `/删除频道 <频道URL>` | 从监控列表中删除指定频道 |

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

### 日志级别管理

#### 查看日志级别
1. 发送`/showloglevel`或`/查看日志级别`命令
2. 机器人会回复当前日志级别以及可用的日志级别选项

#### 设置日志级别
1. 发送`/setloglevel <级别>`或`/设置日志级别 <级别>`命令，其中`<级别>`为以下值之一：
   - `DEBUG`：最详细的日志，包含所有调试信息
   - `INFO`：普通信息日志，记录主要操作
   - `WARNING`：警告日志，仅记录警告和错误信息
   - `ERROR`：错误日志，仅记录错误和严重错误信息
   - `CRITICAL`：严重错误日志，仅记录最严重的错误信息
2. 机器人会确认日志级别已更新，并显示之前的日志级别
3. 设置的日志级别会永久保存到配置文件中，重启后依然有效

#### 日志级别优先级
- 配置文件（config.json）中的`log_level`字段优先级最高
- 环境变量（.env）中的`LOG_LEVEL`次之
- 默认为`DEBUG`级别

#### 日志级别应用场景
- **开发调试**：使用`DEBUG`级别，查看所有详细日志
- **生产环境**：建议使用`INFO`或`WARNING`级别，减少日志输出
- **故障排查**：临时调整为`DEBUG`级别，获取详细调试信息

### 频道管理

#### 查看频道列表
1. 发送`/showchannels`或`/查看频道列表`命令
2. 机器人会回复当前配置的所有频道列表
3. 如果没有配置任何频道，会显示"当前没有配置任何频道"

#### 添加频道
1. 发送`/addchannel <频道URL>`或`/添加频道 <频道URL>`命令，其中`<频道URL>`为Telegram频道的完整URL
2. 例如：`/addchannel https://t.me/examplechannel`
3. 机器人会确认频道是否添加成功，并显示当前频道数量
4. 支持同时添加多个频道，每个频道需要单独发送命令

#### 删除频道
1. 发送`/deletechannel <频道URL>`或`/删除频道 <频道URL>`命令，其中`<频道URL>`为已添加的频道完整URL
2. 例如：`/deletechannel https://t.me/examplechannel`
3. 机器人会确认频道是否删除成功，并显示当前频道数量
4. 如果尝试删除不存在的频道，机器人会给出提示

#### 频道配置说明
- 支持从环境变量`TARGET_CHANNEL`加载单个默认频道
- 支持通过命令动态添加和删除频道
- 所有频道配置会永久保存到`config.json`文件中
- 重启机器人后，所有配置的频道会自动加载
- 消息抓取时会遍历所有配置的频道，汇总所有消息

### 智能总结时间记录

#### 功能说明
机器人会自动记录每次总结的时间（包括定时总结和手动触发的总结），并将时间存储在`.last_summary_time`文件中。下次总结时，机器人会从该时间点开始获取消息，只处理自上次总结以来的新消息。

#### 工作原理
1. 每次执行总结任务时，机器人会先读取`.last_summary_time`文件中的时间
2. 从该时间点开始抓取所有新消息
3. 生成并发送总结报告
4. 将当前时间保存到`.last_summary_time`文件中，覆盖原有时间
5. 下次总结时重复上述步骤

#### 首次使用
- 首次运行机器人时，`.last_summary_time`文件不存在
- 机器人会默认抓取过去一周的消息进行总结
- 总结完成后，会将当前时间保存到`.last_summary_time`文件中
- 后续总结将从该时间点开始

#### 注意事项
- `.last_summary_time`文件会自动生成，无需手动创建
- 该文件存储的是UTC时间，确保跨时区使用时的准确性
- 删除该文件会重置总结时间，机器人将重新抓取过去一周的消息
- 支持同时为所有配置的频道记录统一的总结时间

## 项目结构

```
tg-bot/
├── main.py               # 主程序文件
├── prompt.txt            # 提示词存储文件
├── config.json           # AI配置存储文件（自动生成）
├── .env                  # 环境变量配置
├── .last_summary_time    # 上次总结时间存储文件（自动生成）
├── .restart_flag         # 重启标记文件（临时生成）
├── .gitignore            # Git忽略规则
├── requirements.txt      # 依赖列表
└── README.md             # 项目说明文档
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