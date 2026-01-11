# 🌸 Sakura-频道总结助手 v1.2.3

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
- [🌐 Web管理界面](#-web管理界面)
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
| ⏰ **定时周报推送** | 每周一早上9点自动生成并发送频道周报 | ✅ |
| ⚡ **手动触发总结** | 支持管理员通过命令随时生成总结 | ✅ |
| 🔧 **自定义AI配置** | 支持多种OpenAI兼容API服务（DeepSeek、OpenAI等） | ✅ |
| 🎯 **自定义提示词** | 允许管理员设置专属提示词，灵活调整总结风格 | ✅ |
| 👥 **多管理员支持** | 可配置多个管理员ID，报告同时发送给所有管理员 | ✅ |
| 📝 **长消息分段发送** | 智能处理超长总结内容，自动分段发送 | ✅ |
| 🌐 **多频道支持** | 同时监控和总结多个频道的内容 | ✅ |
| ⏱️ **智能时间记录** | 自动记录总结时间，仅获取新消息提高效率 | ✅ |
| ⏰ **频道级时间配置** | 为每个频道单独配置自动总结时间 | ✅ |
| 🛡️ **错误处理与恢复** | 智能重试机制、健康检查和优雅关闭 | ✅ |
| 🌐 **完整Web管理界面** | 直观的浏览器界面管理所有功能 | ✅ |
| 📊 **投票消息功能** | 总结消息发送后自动生成投票并发送到讨论组评论区 | ✅ |

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

# ===== Web管理界面配置 =====
WEB_PORT=8000  # Web管理界面端口，可自定义（如9000、8080等）
WEB_ADMIN_PASSWORD=your_admin_password_here  # Web管理界面登录密码

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
│   ├── bot_session_web.session # Web界面会话
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

## 🌐 Web管理界面

Sakura-频道总结助手提供了完整的Web管理界面，可以通过浏览器直观地管理机器人配置和功能。

### 访问方式

1. **启动服务**：`python main.py` 或 `docker-compose up -d`
2. **访问地址**：
   - **默认端口**：http://localhost:8000
   - **自定义端口**：在`.env`文件中设置`WEB_PORT=9000`，然后访问 http://localhost:9000
   - **局域网访问**：启动时会显示局域网IP地址，如 http://192.168.1.100:8000
3. **登录密码**：通过`.env`文件中的`WEB_ADMIN_PASSWORD`配置（默认：`Sakura`）

### 多地址访问
Web管理界面启动时会显示所有可访问地址：
- 本地访问: http://127.0.0.1:{PORT} 或 http://localhost:{PORT}
- 所有接口: http://0.0.0.0:{PORT}
- 局域网访问: http://{LAN_IP}:{PORT}（自动获取本地IP地址）

### 功能页面

| 页面 | 功能 | 访问路径 |
|------|------|----------|
| 📊 **仪表板** | 系统概览、健康状态、错误统计 | `/` |
| 📺 **频道管理** | 添加/删除频道，设置自动总结时间 | `/channels` |
| ⚙️ **配置管理** | 查看和修改AI配置 | `/config` |
| ⏰ **任务管理** | 手动触发总结，查看定时任务 | `/tasks` |
| 📝 **日志查看** | 查看系统日志，支持搜索和清空 | `/logs` |
| 🖥️ **系统信息** | API凭证、管理员列表、错误统计 | `/system` |

### 技术特性

- ✅ **响应式设计**：适配桌面和移动设备
- ✅ **实时同步**：所有配置更改实时保存到`config.json`
- ✅ **会话管理**：安全的登录认证系统
- ✅ **API端点**：支持频道管理、配置更新、任务触发等操作
- ✅ **错误处理**：集成现有的错误处理系统
- ✅ **与Telegram命令兼容**：通过相同的配置文件同步

## 📋 使用说明

### 命令列表

| 命令 | 别名 | 功能 | 示例 |
|------|------|------|------|
| `/summary` | `/立即总结` | 立即生成本周频道消息汇总 | `/summary` |
| `/setprompt` | `/设置提示词` | 设置自定义提示词 | `/setprompt` |
| `/showaicfg` | `/查看AI配置` | 查看当前AI配置信息 | `/showaicfg` |
| `/setaicfg` | `/设置AI配置` | 设置自定义AI配置 | `/setaicfg` |
| `/showchannels` | `/查看频道列表` | 查看所有配置的频道 | `/showchannels` |
| `/addchannel` | `/添加频道` | 添加新的频道到监控列表 | `/addchannel https://t.me/example` |
| `/deletechannel` | `/删除频道` | 从监控列表中删除频道 | `/deletechannel https://t.me/example` |
| `/restart` | `/重启` | 重启机器人 | `/restart` |
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

# 设置频道自动总结时间
/setchannelschedule FireflyLeak tue 10 30
# 含义：FireflyLeak频道每周二10:30执行自动总结

# 删除频道时间配置
/deletechannelschedule FireflyLeak
```

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
│   ├── web_app.py                 # Web管理界面应用
│   ├── restart_bot.py             # 机器人重启脚本
│   └── restart_bot_improved.py    # 改进版重启脚本
│
├── 📁 templates/                  # Web界面模板
│   ├── base.html                  # 基础模板
│   ├── dashboard.html             # 仪表板
│   ├── channels.html              # 频道管理
│   ├── config.html                # 配置管理
│   ├── tasks.html                 # 任务管理
│   ├── logs.html                  # 日志查看
│   ├── system.html                # 系统信息
│   └── login.html                 # 登录页面
│
├── 📁 static/                     # 静态资源
│   ├── css/
│   │   └── style.css              # 样式表
│   └── js/
│       └── script.js              # JavaScript
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
│   ├── bot_session_web.session    # Web界面会话
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
- `data/` 目录在旧版本中使用，新版本已改为使用根目录下的配置文件
- 带 `*` 的文件为运行时自动生成的文件
- 配置文件（`.env`、`config.json`、`prompt.txt`）需要根据实际环境进行配置

## 🔧 技术栈

| 技术 | 用途 | 版本 |
|------|------|------|
| **Python** | 主编程语言 | 3.13+ |
| **Telethon** | Telegram API客户端 | 1.34+ |
| **OpenAI SDK** | AI API调用 | 1.0+ |
| **APScheduler** | 定时任务调度 | 3.10+ |
| **Flask** | Web服务器框架 | 3.0+ |
| **Docker** | 容器化部署 | 20.10+ |
| **Docker Compose** | 容器编排 | 2.0+ |

## 📝 更新日志

## [1.2.3] - 2026-01-11 （最新）

### 修复
- **/changelog命令过度分段问题**：修复了/changelog命令导致消息被分割成几百条的问题
  - 修改默认行为，只显示最近1个版本的更新日志
  - 支持参数控制显示的版本数量：/changelog 5 显示最近5个版本
  - 优化send_long_message函数，新增show_pagination参数
  - 当show_pagination=False时，只在第一条消息显示标题，后续消息直接发送内容
  - 大幅减少消息数量，提供更好的用户体验

### 改进
- **消息分段优化**：改进长消息的分段策略
  - 避免每条消息都显示分页标题浪费字符空间
  - 第一条消息显示完整标题，后续消息无标题
  - 充分利用Telegram 4000字符的消息限制

### 使用方式
- `/changelog` - 显示最近1个版本（默认）
- `/changelog 5` - 显示最近5个版本
- `/changelog 14` - 显示全部14个版本

## [1.2.2] - 2026-01-11

### 新增
- **自动置顶总结消息**：当在频道中发送了总结消息后，自动将其置顶
  - 在`send_report`函数中添加自动置顶功能
  - 使用`client.pin_message()`方法置顶第一条消息
  - 添加错误处理，即使置顶失败（如权限不足）也不会影响主要功能
  - 在两个发送路径（使用现有客户端和创建新客户端）中都应用置顶功能

### 修复
- **总结消息标题优化**：修正总结消息的标题不使用从频道链接中提取，而使用频道的实际名称
  - 在`command_handlers.py`的`handle_manual_summary`函数中，使用`client.get_entity(channel).title`获取频道实际名称
  - 在`telegram_client.py`的`send_report`函数中，添加智能标题检查逻辑，避免重复标题
  - 统一管理员和源频道接收的总结标题，确保两者都使用频道实际名称
  - 改进标题替换逻辑，避免内容被截断

### 技术实现
- **频道实体获取**：使用`client.get_entity(source_channel)`获取频道实体，提取实际名称
- **智能标题检查**：检查总结文本是否已有标题，避免重复添加
- **条件性更新**：只有当标题与频道实际名称不匹配时才更新标题
- **错误回退**：获取频道实体失败时，使用频道链接的最后部分作为回退名称
- **日期范围提取**：从原总结文本中提取日期范围，保持格式一致

### 影响范围
- 所有总结消息发送功能：
  - `/summary`命令的手动总结
  - 自动周报的定时总结
  - 所有发送到源频道的总结消息都会自动置顶
  - 所有总结消息都使用频道实际名称作为标题

## [1.2.1] - 2026-01-11

### 修复
- **EntityBoundsInvalidError错误**：修复了发送长消息分段时出现的`EntityBoundsInvalidError: Some of provided entities have invalid bounds`错误
  - 创建智能消息分割算法 (`telegram_client_utils.py`)，保护Markdown格式实体不被破坏
  - 更新`send_long_message()`函数使用智能分割，确保**粗体**、`内联代码`、[链接]等实体完整性
  - 添加实体完整性验证和自动修复机制，当格式被破坏时自动移除格式重试
  - 优先在段落、句子、换行等自然边界分割，避免破坏消息结构
  - 提供优雅的回退机制：智能分割失败时自动使用简单字符分割

### 技术实现
- **智能分割算法**：实现`split_message_smart()`函数，能够识别和保护Markdown实体
- **实体保护**：确保**粗体**、`代码`、[链接]等Markdown实体不被分割破坏
- **边界检测**：优先在自然边界（段落、句子）分割，其次在单词边界分割
- **验证机制**：每个分段都验证实体完整性和长度限制
- **错误恢复**：发送失败时自动尝试移除格式重试，确保消息能够送达

### 影响范围
- 修复影响所有长消息发送功能：
  - `/summary`命令的长消息发送
  - 自动周报的长消息发送
  - `/changelog`命令的更新日志发送
  - 所有使用`send_long_message()`或`send_report()`的地方

## [1.2.0] - 2026-01-10

### 新增
- **投票消息功能**：当总结消息发送至源频道时，自动生成投票消息并发送到讨论组评论区
  - AI根据总结内容智能生成相关投票问题
  - 自动检测频道绑定的讨论组（linked_chat_id）
  - 支持Telegram投票长度限制（问题255字符，选项100字符）
  - 自动清理投票文本，移除不可见字符
  - 支持被动监听转发消息机制，避免机器人权限限制
  - 提供配置开关，可通过`ENABLE_POLL`环境变量控制功能开关

### 技术实现
- **底层RPC调用**：直接使用Telethon底层`SendMediaRequest`，绕过高级函数转换问题
- **正确对象包装**：使用`TextWithEntities`包装投票文本，`InputReplyToMessage`包装回复ID
- **智能ID转换**：自动将讨论组ID转换为Telethon超级群组格式（-100xxxxxxxxx）
- **多级错误处理**：投票失败不影响主要总结流程，记录详细错误日志
- **容错机制**：转发消息超时（10秒）后发送独立投票消息作为备选方案

### 配置
- 在`config.py`中添加`ENABLE_POLL`配置项，默认值为`True`
- 在`.env.example`中添加`ENABLE_POLL=True`配置示例
- 支持通过环境变量或配置文件控制投票功能开关

### 使用方式
1. **自动触发**：执行`/summary`命令时，如果频道绑定了讨论组且启用了投票功能，会自动发送投票
2. **手动控制**：通过设置`ENABLE_POLL=False`可禁用投票功能
3. **权限要求**：机器人需要加入频道的讨论组（私人群组）才能发送投票

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

如果您发现任何问题或有功能建议，请通过 [GitHub Issues](https://github.com/Sakura520222/Sakura-Channel-Summary-Assistant/issues) 报告。

---

<div align="center">
  
**🌸 Sakura-频道总结助手** · 让频道管理更智能

[快速开始](#-快速开始) · [使用说明](#-使用说明) · [Docker部署](#-docker部署)

</div>
