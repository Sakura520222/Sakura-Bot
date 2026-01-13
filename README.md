# 🌸 Sakura-频道总结助手 v1.2.9

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
| 🔄 **投票重新生成** | 投票消息附带重新生成按钮，支持管理员重新生成投票 | ✅ |

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

| 命令 | 别名 | 功能 | 示例 |
|------|------|------|------|
| `/start` | `/开始` | 查看欢迎消息和基本介绍 | `/start` |
| `/help` | `/帮助` | 查看完整命令列表和使用说明 | `/help` |
| `/summary` | `/立即总结` | 立即生成本周频道消息汇总 | `/summary` |
| `/showprompt` | `/查看提示词` | 查看当前提示词 | `/showprompt` |
| `/setprompt` | `/设置提示词` | 设置自定义提示词 | `/setprompt` |
| `/showaicfg` | `/查看AI配置` | 查看当前AI配置信息 | `/showaicfg` |
| `/setaicfg` | `/设置AI配置` | 设置自定义AI配置 | `/setaicfg` |
| `/showloglevel` | `/查看日志级别` | 查看当前日志级别 | `/showloglevel` |
| `/setloglevel` | `/设置日志级别` | 设置日志级别 | `/setloglevel` |
| `/restart` | `/重启` | 重启机器人 | `/restart` |
| `/shutdown` | `/关机` | 彻底停止机器人 | `/shutdown` |
| `/pause` | `/暂停` | 暂停所有定时任务 | `/pause` |
| `/resume` | `/恢复` | 恢复所有定时任务 | `/resume` |
| `/showchannels` | `/查看频道列表` | 查看所有配置的频道 | `/showchannels` |
| `/addchannel` | `/添加频道` | 添加新的频道到监控列表 | `/addchannel https://t.me/example` |
| `/deletechannel` | `/删除频道` | 从监控列表中删除频道 | `/deletechannel https://t.me/example` |
| `/clearsummarytime` | `/清除总结时间` | 清除上次总结时间记录 | `/clearsummarytime` |
| `/setsendtosource` | `/设置报告发送回源频道` | 设置是否将报告发送回源频道 | `/setsendtosource` |
| `/showchannelschedule` | `/查看频道时间配置` | 查看频道自动总结时间配置 | `/showchannelschedule` |
| `/setchannelschedule` | `/设置频道时间配置` | 设置频道自动总结时间 | `/setchannelschedule` |
| `/deletechannelschedule` | `/删除频道时间配置` | 删除频道自动总结时间配置 | `/deletechannelschedule` |
| `/channelpoll` | `/查看频道投票配置` | 查看频道投票配置 | `/channelpoll` |
| `/setchannelpoll` | `/设置频道投票配置` | 设置频道投票配置 | `/setchannelpoll` |
| `/deletechannelpoll` | `/删除频道投票配置` | 删除频道投票配置 | `/deletechannelpoll` |
| `/changelog` | `/更新日志` | 查看更新日志 | `/changelog` |

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

### [1.2.9] - 2026-01-13 （最新）

#### 新增
- **投票重新生成功能**：投票消息附带"重新生成投票"按钮，管理员可重新生成投票内容
  - 点击按钮后自动删除旧投票和按钮，生成新的投票内容
  - 支持频道模式和讨论组模式的投票重新生成
  - 自动记录并使用讨论组转发消息ID，确保讨论组模式重新生成成功
  - 新投票将发送到与原投票相同的位置（频道或讨论组）
  - 权限控制：仅管理员可以重新生成投票

#### 技术改进
- **投票重新生成数据存储**：
  - 新增 `.poll_regenerations.json` 文件，存储投票重新生成所需的数据
  - 记录投票消息ID、按钮消息ID、总结文本、频道信息等
  - 讨论组模式额外记录转发消息ID，确保重新生成时准确定位
  - 支持定期清理超过30天的旧记录
- **消息ID追踪优化**：
  - 扩展 `.last_summary_time.json` 数据结构，分别记录总结、投票、按钮三类消息ID
  - 避免投票和按钮消息被重复分析和总结
  - 向后兼容旧格式的数据结构
- **投票发送逻辑重构**：
  - 重构投票发送函数返回值，统一返回投票和按钮消息ID
  - 优化讨论组模式的转发消息等待机制
  - 使用存储的转发消息ID，避免等待超时问题

#### 修复
- **讨论组模式投票重新生成**：
  - 修复讨论组模式下投票重新生成时等待转发消息超时的问题
  - 通过存储转发消息ID，直接使用原始转发消息，无需等待新转发
  - 修复删除旧投票时从错误位置删除的问题
  - 修复 GetHistoryRequest 权限限制导致的搜索失败问题

#### 文档更新
- 更新 README.md，添加投票重新生成功能说明
- 更新版本号至 v1.2.9

### [1.2.8] - 2026-01-13

#### 新增
- **用户帮助系统**：添加 `/start` 和 `/help` 命令，提供完整的命令帮助体系
  - `/start` 命令：显示欢迎消息和快速入门指南，列出精选常用命令
  - `/help` 命令：显示完整的命令列表和使用说明，按功能分类展示
  - 两个命令都支持中英文别名
  - 所有用户都可以使用（无需管理员权限）

#### 新增命令
- `/start` 或 `/开始` - 查看欢迎消息和基本介绍
- `/help` 或 `/帮助` - 查看完整命令列表和使用说明

#### 用户体验改进
- 新用户首次使用时发送 `/start` 即可快速了解机器人功能
- 通过 `/help` 命令轻松查找所有可用命令
- 帮助体系完整，两个命令互相引用，便于导航

### [1.2.7] - 2026-01-13

#### 新增
- **频道级投票配置功能**：为每个频道单独配置投票的发送位置和行为
  - **频道模式**：投票直接发送到频道，回复总结消息
  - **讨论组模式**：投票发送到频道讨论组，回复转发消息（默认，保持向后兼容）
  - 支持为每个频道单独启用或禁用投票功能
  - 支持通过配置文件或命令进行配置

#### 新增命令
- `/channelpoll <频道>` - 查看指定频道或所有频道的投票配置
- `/setchannelpoll <频道> <enabled> <location>` - 设置频道投票配置
  - `enabled`: true/false（启用/禁用投票）
  - `location`: channel/discussion（频道/讨论组）
- `/deletechannelpoll <频道>` - 删除频道投票配置，恢复全局配置

#### 配置文件增强
- 在 `config.json` 中新增 `channel_poll_settings` 字段
- 支持为每个频道配置独立的投票行为
- 未配置的频道自动使用全局配置，保持向后兼容

#### 文档更新
- 新增 `POLL_FEATURE.md` 文档，详细说明投票配置功能
- 新增 `config.example.json` 配置示例文件
- 更新 README.md，添加投票配置说明

#### 向后兼容
- 未配置的频道默认使用讨论组模式（原有行为）
- 未配置启用状态的频道使用全局 `ENABLE_POLL` 配置
- 现有配置无需修改即可继续使用

### [1.2.6] - 2026-01-12

#### 新增
- **多频率模式支持**：新增"每天"和"每周N天"两种自动总结频率模式
  - **每天模式 (daily)**：每天在固定时间执行总结，如每天23:00
  - **每周N天模式 (weekly)**：每周在指定的多天执行，如每周一和周四
  - 支持为每个频道单独配置不同的总结频率
  - 报告标题根据频率自动显示"日报"或"周报"

#### 命令增强
- `/setchannelschedule` 命令支持新格式：
  - `/setchannelschedule <频道> daily <小时> <分钟>` - 设置每天总结
  - `/setchannelschedule <频道> weekly <星期,星期> <小时> <分钟>` - 设置每周多天总结
- `/showchannelschedule` 命令优化显示格式，清晰展示每天/每周模式

#### 向后兼容
- 保留旧命令格式 `/setchannelschedule <频道> <星期> <小时> <分钟>`
- 旧格式配置自动识别并转换为新格式
- 无需修改现有配置即可继续使用

### [1.2.5] - 2026-01-12

#### 移除
- **完全移除Web管理界面**：移除了整个Web管理界面及相关功能
  - 删除了`web_app.py`文件及其所有FastAPI路由和API端点
  - 删除了`templates/`目录（所有HTML模板文件）
  - 删除了`static/`目录（CSS和JavaScript文件）
  - 从`main.py`中移除了Web服务器启动代码和相关线程
  - 移除了Web管理界面触发的总结任务队列处理逻辑
  - 从`requirements.txt`中移除了Web相关依赖（fastapi、uvicorn、jinja2、python-multipart、itsdangerous）
  - 从`config.py`中移除了`WEB_PORT`配置项和相关日志
  - 从`.env.example`中移除了Web管理界面配置示例
  - 从`docker-compose.yml`中移除了Web端口映射、健康检查和资源限制相关配置

#### 简化
- **简化架构**：项目现在完全依赖Telegram命令进行管理，架构更加简洁
  - 所有功能通过Telegram机器人命令操作
  - 减少了依赖项和代码复杂度
  - 降低了资源使用（内存限制从768M降至512M）
  - 简化了部署流程

#### 文档更新
- 更新了README.md，移除了所有Web管理界面相关的说明和配置示例
- 更新了项目结构说明，移除了Web相关文件和目录的描述

### [1.2.4] - 2026-01-11

#### 修复
- **消息过度分割问题**：修复了智能分割算法导致消息被过度分割的问题
  - 7554字符的列表式内容从94段（平均80字符/段）优化为2-3段（接近4000字符/段）
  - 移除在每个换行符处分割的逻辑
  - 只在段落边界（连续两个或更多换行符）和句子边界（句号、问号、感叹号后）分割
  - 添加最小分段长度检查（100字符），避免产生过短的段落

#### 修改
- **完全移除分段标题**：优化了长消息分段的显示方式
  - 移除所有分段消息的标题显示，包括第一条消息
  - 不再显示"📋 **频道名称**"或"1/3"、"2/3"等分页信息
  - 直接发送原始内容，提供更简洁的阅读体验

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
