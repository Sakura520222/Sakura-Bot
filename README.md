# 🌸 Sakura-频道总结助手 v1.3.0

[![License](https://img.shields.io/badge/License-CC%20BY--NC--SA%204.0-blue.svg)](https://creativecommons.org/licenses/by-nc-sa/4.0/deed.zh)
[![Python Version](https://img.shields.io/badge/python-3.13+-blue.svg)](https://www.python.org/downloads/)
[![Telethon](https://img.shields.io/badge/Telethon-1.34+-blue.svg)](https://docs.telethon.dev/)
[![Docker](https://img.shields.io/badge/Docker-20.10%2B-blue.svg)](https://www.docker.com/)
[![PRs Welcome](https://img.shields.io/badge/PRs-welcome-brightgreen.svg)](https://github.com/Sakura520222/Sakura-Channel-Summary-Assistant/pulls)

> 一个基于Telegram API和AI技术的智能频道内容管理工具，专为Telegram频道管理员设计，提供自动化的消息汇总、报告生成和投票互动服务。

## 📖 目录

- [✨ 功能特性](#-功能特性)
- [🚀 快速开始](#-快速开始)
  - [环境要求](#环境要求)
  - [安装部署](#安装部署)
- [🐳 Docker部署](#-docker部署)
- [📋 使用说明](#-使用说明)
  - [命令列表](#命令列表)
  - [AI配置管理](#ai配置管理)
  - [频道管理](#频道管理)
- [🏗️ 项目结构](#️-项目结构)
- [🔧 技术栈](#-技术栈)
- [📝 更新日志](#-更新日志)
- [📄 许可证](#-许可证)
- [🤝 贡献](#-贡献)

## ✨ 功能特性

| 功能 | 描述 | 状态 |
|------|------|------|
| 🤖 **AI智能总结** | 利用AI大模型对消息进行深度分析，提取核心要点 | ✅ |
| 🔍 **自动消息抓取** | 定期监控指定Telegram频道，自动抓取并整理消息内容 | ✅ |
| ⏰ **多频率模式** | 支持每天、每周、每周多天等多种自动总结频率 | ✅ |
| ⚡ **手动触发总结** | 支持管理员通过命令随时生成总结 | ✅ |
| 🔧 **自定义AI配置** | 支持多种OpenAI兼容API服务（DeepSeek、OpenAI等） | ✅ |
| 🎯 **自定义提示词** | 允许管理员设置专属提示词，灵活调整总结风格 | ✅ |
| 👥 **多管理员支持** | 可配置多个管理员ID，报告同时发送给所有管理员 | ✅ |
| 📝 **长消息分段发送** | 智能处理超长总结内容，自动分段发送 | ✅ |
| 🌐 **多频道支持** | 同时监控和总结多个频道的内容 | ✅ |
| ⏱️ **智能时间记录** | 自动记录总结时间，仅获取新消息提高效率 | ✅ |
| ⏰ **频道级时间配置** | 为每个频道单独配置自动总结时间 | ✅ |
| 🛡️ **错误处理与恢复** | 智能重试机制、健康检查和优雅关闭 | ✅ |
| 📊 **投票消息功能** | 总结消息发送后自动生成投票并发送到讨论组评论区 | ✅ |
| 🎯 **频道级投票配置** | 为每个频道单独配置投票发送位置和启用状态 | ✅ |
| 🔄 **投票重新生成** | 投票消息附带重新生成按钮,支持管理员重新生成投票 | ✅ |
| 📜 **历史记录功能** | 自动保存所有总结到数据库,支持查询、导出和统计 | ✅ |

## 🚀 快速开始

### 环境要求

- **Python 3.13+** 或 **Docker 20.10+**
- **Telegram Bot Token**（从 [@BotFather](https://t.me/BotFather) 获取）
- **Telegram API ID 和 API Hash**（从 [my.telegram.org](https://my.telegram.org) 获取）
- **OpenAI兼容API Key**（如 [DeepSeek](https://platform.deepseek.com/)、[OpenAI](https://platform.openai.com/) 等）

### 安装部署

#### 方法一：本地运行（推荐开发者）

```bash
# 1. 克隆项目
git clone https://github.com/Sakura520222/Sakura-Channel-Summary-Assistant.git
cd Sakura-Channel-Summary-Assistant

# 2. 安装依赖
pip install -r requirements.txt

# 3. 配置环境变量
cp .env.example .env
# 编辑 .env 文件，填写您的配置

# 4. 运行项目
python main.py
```

#### 方法二：Docker一键部署（推荐生产环境）

```bash
# 1. 克隆项目
git clone https://github.com/Sakura520222/Sakura-Channel-Summary-Assistant.git
cd Sakura-Channel-Summary-Assistant

# 2. 配置环境变量
cp .env.example .env
# 编辑 .env 文件，填写您的配置

# 3. 启动服务
docker-compose up -d

# 4. 查看日志（首次运行需要Telegram登录）
docker-compose logs -f
```

#### 配置文件示例 (`.env`)

```env
# ===== Telegram配置 =====
TELEGRAM_API_ID=your_api_id
TELEGRAM_API_HASH=your_api_hash
TELEGRAM_BOT_TOKEN=your_bot_token

# ===== AI配置（支持任意OpenAI兼容API） =====
# 方式1：使用DeepSeek
LLM_API_KEY=your_deepseek_api_key
LLM_BASE_URL=https://api.deepseek.com
LLM_MODEL=deepseek-chat

# 方式2：使用OpenAI
# LLM_API_KEY=your_openai_api_key
# LLM_BASE_URL=https://api.openai.com/v1
# LLM_MODEL=gpt-3.5-turbo

# ===== 频道配置 =====
TARGET_CHANNEL=https://t.me/your_channel

# ===== 管理员配置（支持多个ID，用逗号分隔） =====
REPORT_ADMIN_IDS=admin_id1,admin_id2

# ===== 日志级别配置 =====
LOG_LEVEL=INFO  # DEBUG, INFO, WARNING, ERROR, CRITICAL

# ===== 投票功能配置 =====
ENABLE_POLL=True  # 是否启用投票功能，默认开启
```

## 🐳 Docker部署

### 一键部署

```bash
# 启动所有服务
docker-compose up -d

# 查看服务状态
docker-compose ps

# 查看实时日志
docker-compose logs -f

# 停止服务
docker-compose down
```

### 数据持久化

容器使用本地卷进行数据持久化，所有重要数据都保存在本地文件系统中：

```
Sakura-Channel-Summary-Assistant/
├── 📄 配置文件（持久化保存）
│   ├── .env                    # 环境变量配置
│   ├── config.json             # AI配置文件
│   ├── prompt.txt              # 提示词文件
│   └── .last_summary_time.json # 总结时间记录文件
│
├── 📄 会话文件（持久化保存）
│   ├── bot_session.session     # Telegram主会话
│   ├── health_check.session    # 健康检查会话
│   └── *.session-journal       # 会话日志文件
│
└── 📄 Docker配置
    ├── docker-compose.yml      # Docker Compose配置
    └── Dockerfile              # Docker镜像构建
```

**重要说明：**
- 所有配置文件（`.env`、`config.json`、`prompt.txt`）都保存在项目根目录，确保容器重启后配置不丢失
- Telegram会话文件（`*.session`）持久化保存，避免重复登录
- 数据目录结构已简化，不再使用单独的`data/`目录，所有文件直接保存在项目根目录

### 管理命令

```bash
# 进入容器
docker-compose exec sakura-summary-bot bash

# 重启服务
docker-compose restart

# 更新服务（重新构建镜像）
docker-compose up -d --build

# 查看健康状态
docker inspect --format='{{json .State.Health}}' sakura-summary-bot
```

## 📋 使用说明

### 命令列表

#### 1. 基础命令

| 命令 | 别名 | 功能 | 示例 |
|------|------|------|------|
| `/start` | `/开始` | 查看欢迎消息和基本介绍 | `/start` |
| `/help` | `/帮助` | 查看完整命令列表和使用说明 | `/help` |

#### 2. 核心功能命令

| 命令 | 别名 | 功能 | 示例 |
|------|------|------|------|
| `/summary` | `/立即总结` | 立即生成本周频道消息汇总 | `/summary` |

#### 3. AI 配置命令

| 命令 | 别名 | 功能 | 示例 |
|------|------|------|------|
| `/showprompt` | `/查看提示词` | 查看当前提示词 | `/showprompt` |
| `/setprompt` | `/设置提示词` | 设置自定义提示词 | `/setprompt` |
| `/showaicfg` | `/查看AI配置` | 查看当前AI配置信息 | `/showaicfg` |
| `/setaicfg` | `/设置AI配置` | 设置自定义AI配置 | `/setaicfg` |

#### 4. 频道管理命令

| 命令 | 别名 | 功能 | 示例 |
|------|------|------|------|
| `/showchannels` | `/查看频道列表` | 查看所有配置的频道 | `/showchannels` |
| `/addchannel` | `/添加频道` | 添加新的频道到监控列表 | `/addchannel https://t.me/example` |
| `/deletechannel` | `/删除频道` | 从监控列表中删除频道 | `/deletechannel https://t.me/example` |

#### 5. 自动化配置命令

| 命令 | 别名 | 功能 | 示例 |
|------|------|------|------|
| `/showchannelschedule` | `/查看频道时间配置` | 查看频道自动总结时间配置 | `/showchannelschedule` |
| `/setchannelschedule` | `/设置频道时间配置` | 设置频道自动总结时间 | `/setchannelschedule` |
| `/deletechannelschedule` | `/删除频道时间配置` | 删除频道自动总结时间配置 | `/deletechannelschedule` |
| `/clearsummarytime` | `/清除总结时间` | 清除上次总结时间记录 | `/clearsummarytime` |
| `/setsendtosource` | `/设置报告发送回源频道` | 设置是否将报告发送回源频道 | `/setsendtosource` |

#### 6. 投票配置命令

| 命令 | 别名 | 功能 | 示例 |
|------|------|------|------|
| `/channelpoll` | `/查看频道投票配置` | 查看频道投票配置 | `/channelpoll` |
| `/setchannelpoll` | `/设置频道投票配置` | 设置频道投票配置 | `/setchannelpoll` |
| `/deletechannelpoll` | `/删除频道投票配置` | 删除频道投票配置 | `/deletechannelpoll` |

#### 7. 系统控制命令

| 命令 | 别名 | 功能 | 示例 |
|------|------|------|------|
| `/pause` | `/暂停` | 暂停所有定时任务 | `/pause` |
| `/resume` | `/恢复` | 恢复所有定时任务 | `/resume` |
| `/restart` | `/重启` | 重启机器人 | `/restart` |
| `/shutdown` | `/关机` | 彻底停止机器人 | `/shutdown` |

#### 8. 日志与调试命令

| 命令 | 别名 | 功能 | 示例 |
|------|------|------|------|
| `/showloglevel` | `/查看日志级别` | 查看当前日志级别 | `/showloglevel` |
| `/setloglevel` | `/设置日志级别` | 设置日志级别 | `/setloglevel` |
| `/clearcache` | `/清除缓存` | 清除讨论组ID缓存 | `/clearcache` |
| `/changelog` | `/更新日志` | 查看更新日志 | `/changelog` |

#### 9. 历史记录命令 🆕

| 命令 | 别名 | 功能 | 示例 |
|------|------|------|------|
| `/history` | `/历史` | 查看历史总结 | `/history` |
| `/export` | `/导出` | 导出历史记录 | `/export channel1 csv` |
| `/stats` | `/统计` | 查看统计数据 | `/stats` |

##### 历史记录功能说明

**查看历史总结 (`/history`)**
```bash
# 查看所有频道最近10条总结
/history

# 查看指定频道最近10条总结
/history channel1

# 查看指定频道最近30天的总结
/history channel1 30

# 支持完整URL格式
/history https://t.me/channel1 7
```

**导出历史记录 (`/export`)**
```bash
# 导出所有记录为JSON（默认格式）
/export

# 导出指定频道为JSON
/export channel1

# 导出为CSV格式（适合Excel）
/export channel1 csv

# 导出为Markdown格式（适合阅读）
/export channel1 markdown
```

**查看统计数据 (`/stats`)**
```bash
# 查看所有频道的统计数据
/stats

# 查看指定频道的统计
/stats channel1
```

**统计信息包括**：
- 总总结次数、总处理消息数、平均消息数
- 按类型统计（日报/周报/手动总结）
- 时间分布（本周、本月总结次数）
- 最近总结时间
- 频道排行榜（按总结次数排序）

**数据存储**：
- 所有总结自动保存到 SQLite 数据库 (`summaries.db`)
- 数据库文件本地存储，不上传云端
- 支持导出多种格式：JSON、CSV、Markdown
- 定期清理旧数据，防止数据库过大

### AI配置管理

#### 设置AI配置流程

```bash
# 1. 发送设置命令
/setaicfg

# 2. 按提示依次设置（发送/skip跳过，/cancel取消）
#    - API Key
#    - Base URL
#    - Model

# 3. 配置完成，立即生效
```

#### 支持的AI服务

| 服务 | Base URL | 模型示例 | 备注 |
|------|----------|----------|------|
| **DeepSeek** | `https://api.deepseek.com` | `deepseek-chat` | 推荐，性价比高 |
| **OpenAI** | `https://api.openai.com/v1` | `gpt-3.5-turbo` | 兼容性好 |
| **其他兼容API** | 您的API地址 | 您的模型名称 | 任意OpenAI兼容API |

### 频道管理

#### 添加频道

```bash
# 添加单个频道
/addchannel https://t.me/examplechannel

# 添加多个频道（分别执行）
/addchannel https://t.me/channel1
/addchannel https://t.me/channel2
```

#### 频道时间配置

```bash
# 查看所有频道配置
/showchannelschedule

# 每天模式：设置每天固定时间总结
/setchannelschedule FireflyLeak daily 23 0
# 含义：FireflyLeak频道每天23:00执行自动总结

# 每周模式：设置每周多天总结
/setchannelschedule Nahida_Leak weekly mon,thu 14 30
# 含义：Nahida_Leak频道每周一和周四14:30执行自动总结

# 旧格式（仍然支持）：设置每周单天总结
/setchannelschedule FireflyLeak sun 9 0
# 含义：FireflyLeak频道每周日09:00执行自动总结

# 删除频道时间配置
/deletechannelschedule FireflyLeak
```

**支持的频率模式：**
- **每天模式 (daily)**：每天在固定时间执行总结
- **每周模式 (weekly)**：每周在指定的多天执行总结
- **旧格式（向后兼容）**：每周单天执行总结

#### 频道投票配置

```bash
# 查看所有频道的投票配置
/channelpoll

# 查看指定频道的投票配置
/channelpoll channel1

# 设置频道投票：启用并发送到频道（回复总结消息）
/setchannelpoll channel1 true channel

# 设置频道投票：启用并发送到讨论组（回复转发消息）
/setchannelpoll channel1 true discussion

# 禁用频道投票功能
/setchannelpoll channel1 false channel

# 删除频道投票配置（恢复全局配置）
/deletechannelpoll channel1
```

**投票配置说明：**
- **频道模式 (channel)**：投票直接发送到频道，回复总结消息
- **讨论组模式 (discussion)**：投票发送到频道讨论组，回复转发消息（默认）
- **启用状态**：可以为每个频道单独启用或禁用投票功能
- **向后兼容**：未配置的频道使用全局 `ENABLE_POLL` 配置，默认讨论组模式

## 🏗️ 项目结构

```
Sakura-Channel-Summary-Assistant/
│
├── 📄 核心源代码文件
│   ├── main.py                    # 主程序入口
│   ├── config.py                  # 配置管理模块
│   ├── ai_client.py               # AI客户端模块
│   ├── telegram_client.py         # Telegram客户端模块
│   ├── telegram_client_utils.py   # Telegram客户端工具模块（智能消息分割）
│   ├── command_handlers.py        # 命令处理模块
│   ├── scheduler.py               # 调度器模块
│   ├── error_handler.py           # 错误处理模块
│   ├── prompt_manager.py          # 提示词管理模块
│   ├── summary_time_manager.py    # 时间管理模块
│   ├── restart_bot.py             # 机器人重启脚本
│   └── restart_bot_improved.py    # 改进版重启脚本
│
├── 📄 配置文件
│   ├── .env                       # 环境变量配置（从.env.example复制）
│   ├── .env.example               # 环境变量示例
│   ├── config.json                # AI配置文件（运行时生成）
│   ├── prompt.txt                 # 提示词文件（运行时生成）
│   ├── .last_summary_time.json    # 时间记录文件（运行时生成）
│   └── .gitignore                 # Git忽略文件
│
├── 📄 会话文件（运行时生成）
│   ├── bot_session.session        # Telegram主会话
│   ├── health_check.session       # 健康检查会话
│   ├── session_name.session       # 会话名称文件
│   └── *.session-journal          # 会话日志文件
│
├── 📄 部署与构建文件
│   ├── Dockerfile                 # Docker镜像构建
│   ├── docker-compose.yml         # Docker Compose配置
│   ├── docker-entrypoint.sh       # Docker入口点脚本
│   ├── requirements.txt           # Python依赖
│   ├── start.bat                  # Windows启动脚本
│   └── sakura-bot.spec            # PyInstaller打包配置
│
├── 📄 文档文件
│   ├── README.md                  # 项目说明文档
│   ├── CHANGELOG.md               # 更新日志
│   ├── LICENSE                    # 许可证文件
│   ├── ERROR_HANDLING_IMPROVEMENTS.md # 错误处理改进文档
│   └── MODULE_SPLIT_SUMMARY.md    # 模块拆分总结文档
│
└── 📁 __pycache__/                # Python缓存目录（运行时生成）
```

**重要说明：**
- 配置文件（`.env`、`config.json`、`prompt.txt`）需要根据实际环境进行配置
- 带 `*` 的文件为运行时自动生成的文件
- Telegram会话文件需要保密，不要提交到版本控制系统

## 🔧 技术栈

| 技术 | 用途 | 版本 |
|------|------|------|
| **Python** | 主编程语言 | 3.13+ |
| **Telethon** | Telegram API客户端 | 1.34+ |
| **OpenAI SDK** | AI API调用 | 1.0+ |
| **APScheduler** | 定时任务调度 | 3.10+ |
| **Docker** | 容器化部署 | 20.10+ |
| **Docker Compose** | 容器编排 | 2.0+ |

## 📝 更新日志

> 查看完整更新日志：`/changelog` 或 [CHANGELOG.md](CHANGELOG.md)

## 📄 许可证

本项目采用 **Creative Commons Attribution-NonCommercial-ShareAlike 4.0 (CC BY-NC-SA 4.0)** 许可证。

### 许可证条款

您可以自由地：
- **共享** — 在任何媒介以任何形式复制、发行本作品
- **演绎** — 修改、转换或以本作品为基础进行创作

惟须遵守下列条件：
- **署名** — 您必须提供本项目的原始来源链接，并注明是否对原始作品作了修改
- **非商业性使用** — 您不得将本作品用于商业目的或进行商业分发
- **相同方式共享** — 如果您再混合、转换或者基于本作品进行创作，您必须基于与原先许可协议相同的许可协议分发您贡献的作品

### 重要说明

- 本项目仅供**个人学习使用**，禁止任何商业用途
- 使用本项目的代码或衍生作品时，必须标注本仓库的原始来源地址
- 本项目源代码：https://github.com/Sakura520222/Sakura-Channel-Summary-Assistant
- 许可证全文：https://creativecommons.org/licenses/by-nc-sa/4.0/deed.zh

## 🤝 贡献

欢迎贡献代码、报告问题或提出建议！

### 贡献流程

1. **Fork 仓库**
2. **创建功能分支** (`git checkout -b feature/amazing-feature`)
3. **提交更改** (`git commit -m 'Add some amazing feature'`)
4. **推送到分支** (`git push origin feature/amazing-feature`)
5. **开启 Pull Request**

### 行为准则

请阅读我们的 [行为准则](CODE_OF_CONDUCT.md) 以确保社区友好和包容。

### 报告问题

如果您发现任何问题或有功能建议，请通过以下方式联系：

- 📧 **电子邮件**：[sakura520222@outlook.com](mailto:sakura520222@outlook.com)
- 🐛 **GitHub Issues**：[提交问题](https://github.com/Sakura520222/Sakura-Channel-Summary-Assistant/issues)

---

<div align="center">

**🌸 Sakura-频道总结助手** · 让频道管理更智能

[快速开始](#-快速开始) · [使用说明](#-使用说明) · [Docker部署](#-docker部署)

</div>
