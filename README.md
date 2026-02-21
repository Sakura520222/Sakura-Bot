# 🌸 Sakura-Bot

[![Release](https://img.shields.io/github/v/release/Sakura520222/Sakura-Bot?style=flat-square)](https://github.com/Sakura520222/Sakura-Bot/releases)
[![License](https://img.shields.io/badge/License-AGPL--3.0%20%2B%20Non--Commercial-blue?style=flat-square)](LICENSE)
[![Python](https://img.shields.io/badge/python-3.13+-blue?style=flat-square&logo=python)](https://www.python.org/downloads/)
[![Code style: black](https://img.shields.io/badge/code%20style-black-000000?style=flat-square)](https://github.com/psf/black)
[![Docker](https://img.shields.io/badge/docker-20.10%2B-blue?style=flat-square&logo=docker)](https://www.docker.com/)
[![PRs Welcome](https://img.shields.io/badge/PRs-welcome-brightgreen?style=flat-square)](https://github.com/Sakura520222/Sakura-Bot/pulls)
[![Stars](https://img.shields.io/github/stars/Sakura520222/Sakura-Bot?style=flat-square)](https://github.com/Sakura520222/Sakura-Bot/stargazers)

> **基于AI技术的智能Telegram频道管理工具** 🤖✨

[English](README_EN.md) | [文档](wiki) | [报告问题](https://github.com/Sakura520222/Sakura-Bot/issues) | [功能建议](https://github.com/Sakura520222/Sakura-Bot/issues)

---

## 📖 项目简介

Sakura-Bot是一款基于Telegram API和AI技术的智能频道内容管理工具，专为Telegram频道管理员设计。它利用先进的人工智能技术自动监控、分析和总结频道内容，为频道管理员提供高效的内容管理解决方案。

### ✨ 核心亮点

- 🎯 **AI智能总结** - 先进的语言模型深度分析对话，提取核心要点
- 🔍 **智能问答系统** - 基于RAG技术的自然语言问答，支持语义检索和历史查询
- ⏰ **灵活调度** - 支持每天、每周或多天自动总结
- 🌐 **多频道支持** - 同时管理多个频道的内容
- 🤖 **自定义AI配置** - 支持多种OpenAI兼容API（DeepSeek、OpenAI等）
- 📊 **互动投票** - 通过AI生成的投票增强社区互动
- 📝 **历史管理** - 追踪、导出和分析所有总结记录

---

## 🚀 快速开始

### 环境要求

- **Python 3.13+** 或 **Docker 20.10+**
- **Telegram Bot Token**（从 [@BotFather](https://t.me/BotFather) 获取）
- **Telegram API凭证**（从 [my.telegram.org](https://my.telegram.org) 获取）
- **OpenAI兼容API Key**（如 [DeepSeek](https://platform.deepseek.com/)、[OpenAI](https://platform.openai.com/) 等）

### 🐳 Docker部署（推荐）

```bash
# 克隆项目
git clone https://github.com/Sakura520222/Sakura-Bot.git
cd Sakura-Bot

# 配置环境变量
cp data/.env.example data/.env
# 编辑 data/.env 文件，填写您的配置

# 启动服务
docker-compose up -d

# 查看日志
docker-compose logs -f
```

### 💻 本地安装

```bash
# 克隆项目
git clone https://github.com/Sakura520222/Sakura-Bot.git
cd Sakura-Bot

# 安装依赖
pip install -r requirements.txt

# 配置环境
cp data/.env.example data/.env
# 编辑 data/.env 文件，填写您的配置

# 运行程序
python main.py
```

---

## 🎨 功能特性

### 核心能力

| 功能 | 描述 | 状态 |
|------|------|------|
| **🤖 AI智能总结** | 利用先进AI模型深度分析并提取频道消息核心要点 | ✅ |
| **🔍 自动监控** | 定期自动抓取并整理监控频道的内容 | ✅ |
| **⏰ 多频率模式** | 支持每天、每周、每周多天等多种自动总结频率 | ✅ |
| **⚡ 手动触发** | 支持管理员通过命令随时生成总结 | ✅ |

### AI与配置

| 功能 | 描述 | 状态 |
|------|------|------|
| **🔧 自定义AI配置** | 支持多种OpenAI兼容API服务 | ✅ |
| **🎯 自定义提示词** | 灵活调整AI总结风格和内容 | ✅ |
| **🎯 投票提示词** | 独立配置AI生成投票内容的提示词 | ✅ |
| **🤖 问答Bot人格** | 自定义问答Bot的人格设定和回答风格 | ✅ |

### 频道管理

| 功能 | 描述 | 状态 |
|------|------|------|
| **👥 多管理员支持** | 可配置多个管理员ID，报告同时发送给所有管理员 | ✅ |
| **🌐 多频道支持** | 同时监控和总结多个频道的内容 | ✅ |
| **📝 长消息分段** | 智能处理超长总结内容，自动分段发送 | ✅ |
| **⏱️ 智能时间记录** | 自动记录总结时间，仅获取新消息提高效率 | ✅ |
| **🕐 频道级时间配置** | 为每个频道单独配置自动总结时间 | ✅ |

### 高级功能

| 功能 | 描述 | 状态 |
|------|------|------|
| **🔍 智能问答系统** | 基于RAG技术的自然语言问答，支持语义检索历史总结 | ✅ |
| **🧠 向量存储** | 使用ChromaDB存储总结向量，支持语义搜索 | ✅ |
| **🎯 重排序优化** | 使用BGE-Reranker精排检索结果，提升准确率 | ✅ |
| **💬 多轮对话** | 支持上下文感知的对话，AI能理解代词指代和历史对话 | ✅ |
| **🛡️ 错误恢复** | 智能重试机制、健康检查和优雅关闭 | ✅ |
| **📊 互动投票** | 总结后自动在讨论组生成投票消息 | ✅ |
| **🎯 频道级投票配置** | 为每个频道单独配置投票发送位置和启用状态 | ✅ |
| **🔄 投票重新生成** | 管理员可通过一键按钮重新生成投票 | ✅ |
| **📜 历史记录** | 自动保存所有总结到数据库，支持查询、导出和统计 | ✅ |
| **🌍 国际化支持** | 完整的多语言支持系统，所有模块已国际化 | ✅ |
| **📢 用户订阅系统** | 用户可订阅感兴趣的频道，收到新总结时自动推送通知 | ✅ |
| **📝 总结请求功能** | 用户可主动请求生成频道总结，由主Bot管理员审核处理 | ✅ |
| **🤖 跨Bot通信** | 问答Bot和主Bot通过数据库队列实现进程间通信 | ✅ |
| **🔧 代码优化** | 总结生成流程模块化，支持代码复用和统一管理 | ✅ |
| **📱 命令菜单** | QA Bot自动注册命令菜单，用户可直接查看所有可用命令 | ✅ |
| **🗄️ MySQL数据库支持** | 新增MySQL数据库支持，提升性能和并发能力 | ✅ |
| **🔄 数据库迁移** | 一键从SQLite迁移到MySQL，自动备份和数据验证 | ✅ |
| **⚡ 启动时检查** | 自动检测旧数据库并通知管理员迁移建议 | ✅ |

---

## 📋 使用指南

### 命令列表

#### 基础命令

| 命令 | 别名 | 功能 | 示例 |
|------|------|------|------|
| `/start` | `/开始` | 查看欢迎消息和基本介绍 | `/start` |
| `/help` | `/帮助` | 查看完整命令列表和使用说明 | `/help` |

#### 核心功能

| 命令 | 别名 | 功能 | 示例 |
|------|------|------|------|
| `/summary` | `/立即总结` | 立即生成本周频道消息汇总 | `/summary` |

#### AI配置

| 命令 | 别名 | 功能 | 示例 |
|------|------|------|------|
| `/showprompt` | `/查看提示词` | 查看当前AI提示词 | `/showprompt` |
| `/setprompt` | `/设置提示词` | 设置自定义AI提示词 | `/setprompt` |
| `/showaicfg` | `/查看AI配置` | 查看当前AI配置信息 | `/showaicfg` |
| `/setaicfg` | `/设置AI配置` | 设置自定义AI配置 | `/setaicfg` |

#### 频道管理

| 命令 | 别名 | 功能 | 示例 |
|------|------|------|------|
| `/showchannels` | `/查看频道列表` | 查看所有配置的频道 | `/showchannels` |
| `/addchannel` | `/添加频道` | 添加新频道到监控列表 | `/addchannel https://t.me/example` |
| `/deletechannel` | `/删除频道` | 从监控列表中删除频道 | `/deletechannel https://t.me/example` |

#### 调度配置

| 命令 | 别名 | 功能 | 示例 |
|------|------|------|------|
| `/showchannelschedule` | `/查看频道时间配置` | 查看频道自动总结时间配置 | `/showchannelschedule` |
| `/setchannelschedule` | `/设置频道时间配置` | 设置频道自动总结时间 | `/setchannelschedule` |
| `/deletechannelschedule` | `/删除频道时间配置` | 删除频道自动总结时间配置 | `/deletechannelschedule` |
| `/clearsummarytime` | `/清除总结时间` | 清除上次总结时间记录 | `/clearsummarytime` |
| `/setsendtosource` | `/设置报告发送回源频道` | 设置是否将报告发送回源频道 | `/setsendtosource` |

#### 投票配置

| 命令 | 别名 | 功能 | 示例 |
|------|------|------|------|
| `/channelpoll` | `/查看频道投票配置` | 查看频道投票配置 | `/channelpoll` |
| `/setchannelpoll` | `/设置频道投票配置` | 设置频道投票配置 | `/setchannelpoll` |
| `/deletechannelpoll` | `/删除频道投票配置` | 删除频道投票配置 | `/deletechannelpoll` |

#### 系统控制

| 命令 | 别名 | 功能 | 示例 |
|------|------|------|------|
| `/pause` | `/暂停` | 暂停所有定时任务 | `/pause` |
| `/resume` | `/恢复` | 恢复所有定时任务 | `/resume` |
| `/restart` | `/重启` | 重启机器人 | `/restart` |
| `/shutdown` | `/关机` | 彻底停止机器人 | `/shutdown` |

#### 问答Bot控制

| 命令 | 别名 | 功能 | 示例 |
|------|------|------|------|
| `/qa_status` | `/qa_状态` | 查看问答Bot运行状态 | `/qa_status` |
| `/qa_start` | `/qa_启动` | 启动问答Bot | `/qa_start` |
| `/qa_stop` | `/qa_停止` | 停止问答Bot | `/qa_stop` |
| `/qa_restart` | `/qa_重启` | 重启问答Bot | `/qa_restart` |
| `/qa_stats` | `/qa_统计` | 查看问答Bot详细统计 | `/qa_stats` |

#### 调试与日志

| 命令 | 别名 | 功能 | 示例 |
|------|------|------|------|
| `/showloglevel` | `/查看日志级别` | 查看当前日志级别 | `/showloglevel` |
| `/setloglevel` | `/设置日志级别` | 设置日志级别 | `/setloglevel` |
| `/clearcache` | `/清除缓存` | 清除讨论组ID缓存 | `/clearcache` |
| `/changelog` | `/更新日志` | 查看更新日志 | `/changelog` |

#### 历史记录

| 命令 | 别名 | 功能 | 示例 |
|------|------|------|------|
| `/history` | `/历史` | 查看历史总结 | `/history` |
| `/export` | `/导出` | 导出历史记录 | `/export channel1 csv` |
| `/stats` | `/统计` | 查看统计数据 | `/stats` |

#### 语言设置

| 命令 | 别名 | 功能 | 示例 |
|------|------|------|------|
| `/language` | `/语言` | 查看或切换界面语言 | `/language` / `/language zh-CN` |

#### 数据库迁移

| 命令 | 别名 | 功能 | 示例 |
|------|------|------|------|
| `/migrate_check` | `/迁移检查` | 检查迁移准备状态 | `/migrate_check` |
| `/migrate_start` | `/开始迁移` | 开始数据库迁移 | `/migrate_start` |
| `/migrate_status` | `/迁移状态` | 查看迁移进度 | `/migrate_status` |

**迁移说明**：
- 支持从SQLite一键迁移到MySQL数据库
- 迁移前自动备份，安全可靠
- 适合生产环境和高并发场景

**MySQL配置示例**：
```env
# ===== 数据库配置 =====
DATABASE_TYPE=mysql  # sqlite 或 mysql

# MySQL配置（使用MySQL时必需）
MYSQL_HOST=localhost
MYSQL_PORT=3306
MYSQL_USER=your_mysql_user
MYSQL_PASSWORD=your_mysql_password
MYSQL_DATABASE=sakura_bot_db
MYSQL_CHARSET=utf8mb4
MYSQL_POOL_SIZE=5
MYSQL_MAX_OVERFLOW=10
MYSQL_POOL_TIMEOUT=30
```

**迁移步骤**：
1. 创建MySQL数据库：`CREATE DATABASE sakura_bot_db CHARACTER SET utf8mb4;`
2. 在 `.env` 中配置MySQL连接信息
3. 使用 `/migrate_check` 检查准备状态
4. 使用 `/migrate_start` 开始迁移

### QA Bot 命令

QA Bot 是独立运行的问答机器人（需单独配置 `QA_BOT_TOKEN`），支持以下命令：

#### 基础命令

| 命令 | 功能 |
|------|------|
| `/start` | 查看欢迎消息和功能介绍 |
| `/help` | 查看完整帮助文档 |
| `/status` | 查看当前配额使用情况和会话状态 |
| `/clear` | 清除当前对话历史，开始新会话 |
| `/view_persona` | 查看当前问答Bot的人格设定 |

#### 订阅管理

| 命令 | 功能 | 示例 |
|------|------|------|
| `/listchannels` | 列出可订阅的频道（自动注册） | `/listchannels` |
| `/subscribe <频道链接>` | 订阅频道总结推送（自动注册） | `/subscribe https://t.me/channel` |
| `/unsubscribe <频道链接>` | 取消频道订阅 | `/unsubscribe https://t.me/channel` |
| `/mysubscriptions` | 查看我的订阅列表 | `/mysubscriptions` |
| `/request_summary <频道链接>` | 请求生成频道总结（自动注册） | `/request_summary https://t.me/channel` |

**说明**：首次使用订阅功能时会自动注册用户，无需单独注册。

### 配置示例

创建或编辑 `data/.env` 文件：

```env
# ===== Telegram配置 =====
TELEGRAM_API_ID=your_api_id_here
TELEGRAM_API_HASH=your_api_hash_here
TELEGRAM_BOT_TOKEN=your_bot_token_here

# ===== 语言配置 =====
LANGUAGE=zh-CN  # 界面语言：zh-CN（简体中文）或 en-US（英语）

# ===== AI配置（支持任意OpenAI兼容API） =====
# 方式1：使用DeepSeek（推荐）
LLM_API_KEY=your_deepseek_api_key
LLM_BASE_URL=https://api.deepseek.com
LLM_MODEL=deepseek-chat

# 方式2：使用OpenAI
# LLM_API_KEY=your_openai_api_key
# LLM_BASE_URL=https://api.openai.com/v1
# LLM_MODEL=gpt-4o

# ===== 管理员配置 =====
REPORT_ADMIN_IDS=your_admin_id_here,another_admin_id_here

# ===== 日志级别 =====
LOG_LEVEL=INFO

# ===== 投票功能 =====
ENABLE_POLL=True

# ===== 问答Bot配置（可选） =====
QA_BOT_ENABLED=True
QA_BOT_TOKEN=your_qa_bot_token_here  # 从@BotFather获取，必须与主Bot不同
QA_BOT_USER_LIMIT=3                  # 每个用户每日限额（默认3次）
QA_BOT_DAILY_LIMIT=200               # 每日总限额（默认200次）

# 问答Bot人格配置（三选一，优先级从高到低）
# 方式1：直接填写人格描述（最高优先级）
# QA_BOT_PERSONA=你是一个专业的技术顾问...
# 方式2：指向自定义人格文件
# QA_BOT_PERSONA=data/custom_persona.txt
# 方式3：不填则自动读取 data/qa_persona.txt（默认）

# ===== RAG智能问答系统配置（可选） =====
# Embedding模型配置（必需，用于向量搜索）
EMBEDDING_API_KEY=your_siliconflow_api_key
EMBEDDING_API_BASE=https://api.siliconflow.cn/v1
EMBEDDING_MODEL=BAAI/bge-m3
EMBEDDING_DIMENSION=1024

# Reranker配置（可选，用于提升检索准确率）
RERANKER_API_KEY=your_siliconflow_api_key
RERANKER_API_BASE=https://api.siliconflow.cn/v1/rerank
RERANKER_MODEL=BAAI/bge-reranker-v2-m3
RERANKER_TOP_K=20
RERANKER_FINAL=5

# 向量数据库配置
VECTOR_DB_PATH=data/vectors
```

> **提示**：RAG系统需要额外的API密钥，推荐使用 [SiliconFlow](https://siliconflow.cn/) 获取Embedding和Reranker服务的API密钥。详细配置请参考 [RAG快速启动指南](wiki/RAG_QUICKSTART.md)。

---

## 🏗️ 项目结构

```
Sakura-Bot/
│
├── 📁 core/                          # 核心模块目录
│   ├── ai_client.py                  # AI客户端模块
│   ├── process_manager.py            # 进程管理模块（QA Bot子进程）
│   ├── command_handlers/             # 命令处理子模块
│   ├── telegram/                     # Telegram客户端子模块
│   └── utils/                        # 工具模块
│
├── 📁 data/                          # 数据目录
│   ├── .env                          # 环境变量配置
│   ├── config.json                   # AI配置文件
│   ├── prompt.txt                    # 总结提示词
│   ├── poll_prompt.txt               # 投票提示词
│   ├── qa_persona.txt                # 问答Bot人格配置
│   ├── summaries.db                  # SQLite数据库
│   └── sessions/                     # Telegram会话目录
│
├── 📁 wiki/                          # 文档目录
├── 📁 .github/                       # GitHub工作流
│
├── 📄 main.py                        # 主程序入口
├── 📄 qa_bot.py                      # 问答Bot入口
├── 📄 requirements.txt               # Python依赖
├── 📄 docker-compose.yml             # Docker Compose配置
├── 📄 Dockerfile                     # Docker镜像构建
└── 📄 README.md                      # 本文件
```

---

## 🔧 技术栈

| 技术 | 用途 | 版本 |
|------|------|------|
| **Python** | 主编程语言 | 3.13+ |
| **Telethon** | Telegram MTProto API客户端（主Bot） | 1.34+ |
| **python-telegram-bot** | Telegram Bot API客户端（QA Bot） | 20.0+ |
| **OpenAI SDK** | AI API集成 | 1.0+ |
| **APScheduler** | 定时任务调度 | 3.10+ |
| **ChromaDB** | 向量数据库（RAG系统） | 0.4+ |
| **aiosqlite** | 异步SQLite数据库 | 0.20+ |
| **Pydantic** | 配置管理与数据验证 | 2.0+ |
| **httpx** | HTTP客户端（Reranker调用） | 0.27+ |
| **python-dotenv** | 环境变量管理 | 1.0+ |
| **Docker** | 容器化部署 | 20.10+ |

---

## ❓ 常见问题

### 首次运行需要登录吗？

是的，首次运行需要输入手机号和验证码完成Telegram登录。登录后会生成会话文件，后续运行无需重新登录。

### 如何获取Telegram API凭证？

1. 访问 [my.telegram.org](https://my.telegram.org)
2. 登录你的Telegram账号
3. 点击 "API development tools"
4. 创建应用获取 `api_id` 和 `api_hash`

### 支持哪些AI服务？

支持所有OpenAI兼容的API服务，包括：
- **DeepSeek**（推荐，性价比高）
- **OpenAI**官方API
- 任何提供OpenAI兼容接口的第三方服务

### 如何自定义问答Bot的人格？

有三种方式（优先级从高到低）：

1. **环境变量**：在 `.env` 中设置 `QA_BOT_PERSONA=你是一个专业的...`
2. **人格文件**：编辑 `data/qa_persona.txt`（首次运行自动创建）
3. **配置文件**：在 `data/config.json` 中设置 `qa_bot_persona` 字段

修改后重启Bot生效。可通过 `/view_persona` 命令查看当前生效的人格。

### 如何备份数据？

```bash
# 备份data目录
tar -czf backup-$(date +%Y%m%d).tar.gz data/
```

---

## 🤝 贡献

我们欢迎各种形式的贡献！请查看我们的[贡献指南](CONTRIBUTING.md)和[行为准则](wiki/CODE_OF_CONDUCT.md)。

### 如何贡献

#### 📝 报告问题

如果您发现了 bug 或有功能建议：

1. 检查 [现有的 issues](https://github.com/Sakura520222/Sakura-Bot/issues) 确保问题未被报告
2. 使用合适的 [Issue 模板](.github/ISSUE_TEMPLATE/) 创建新 issue
3. 提供尽可能详细的信息（环境、日志、复现步骤等）

#### 💻 提交代码

1. **Fork 项目** - 在 GitHub 上点击 Fork 按钮
2. **克隆到本地**
   ```bash
   git clone https://github.com/YOUR_USERNAME/Sakura-Bot.git
   cd Sakura-Bot
   ```
3. **创建功能分支**
   ```bash
   git checkout main
   git pull origin main
   git checkout -b feature/your-feature-name
   ```
4. **进行更改**
   - 遵循[代码规范](CONTRIBUTING.md#代码规范)
   - 编写有意义的提交信息
   - 添加必要的测试
   - 更新相关文档

5. **本地预检查**（推荐）
   ```bash
   # 代码风格检查
   ruff check .
   
   # 运行测试
   pytest tests/ -v
   ```

6. **提交更改**
   ```bash
   git add .
   git commit -m "feat(scope): 描述您的更改"
   ```

7. **推送到您的 Fork**
   ```bash
   git push origin feature/your-feature-name
   ```

8. **创建 Pull Request**
   - 在 GitHub 上创建 PR 到 `main` 分支
   - 使用 [PR 模板](.github/PULL_REQUEST_TEMPLATE.md)
   - 添加合适的 PR 标签（如 `enhancement`、`bug` 等）
   - 等待 CI 检查通过和代码审查

> **重要说明**：所有 PR 应该提交到 `main` 分支。main 分支的更改会自动同步到 dev 分支。

#### 📚 文档贡献

文档同样重要！您可以：

1. 修正错别字和错误
2. 改进现有文档的清晰度
3. 添加使用示例
4. 翻译文档

### 开发规范

- **提交信息**：遵循 [Conventional Commits](https://www.conventionalcommits.org/) 规范
- **代码风格**：使用 Ruff 进行代码检查
- **测试要求**：确保所有测试通过
- **CI 检查**：PR 必须通过 CI 检查（代码质量、安全扫描、Docker 构建、单元测试）

详细说明请参阅[贡献指南](CONTRIBUTING.md)。

---

## 📄 许可证

本项目采用 **GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可，并附加非商业使用限制条款**。

### 许可证要点

- **AGPL-3.0**：要求修改后的代码必须开源，通过网络提供服务时也需提供源代码
- **非商业限制**：禁止将本软件用于任何商业用途、有偿订阅服务或付费 SaaS 产品
- **署名要求**：所有衍生作品必须保留原作者的项目链接和作者署名信息
- **API 责任**：使用者需自行承担 API 费用及相关法律责任

### 重要说明

- 本项目仅供**个人学习使用**，禁止任何商业用途
- 使用本项目的代码或衍生作品时，必须标注本仓库的原始来源地址
- 基于 AGPL-3.0 的网络交互条款，通过服务器提供服务的必须提供源代码
- 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
- 完整许可证内容请参阅 [LICENSE](LICENSE) 文件

---

## 🙏 致谢

- [Telethon](https://github.com/LonamiWebs/Telethon) - 强大的Telegram MTProto API框架
- [python-telegram-bot](https://github.com/python-telegram-bot/python-telegram-bot) - 功能完善的Telegram Bot API库
- [OpenAI](https://openai.com/) - 领先的AI研究和API服务
- [DeepSeek](https://www.deepseek.com/) - 高性价比的AI API提供商
- [SiliconFlow](https://siliconflow.cn/) - Embedding和Reranker API服务
- 所有为改进本项目做出贡献的[贡献者](https://github.com/Sakura520222/Sakura-Bot/graphs/contributors)

---

## 📞 支持

- 📧 电子邮件：[sakura520222@outlook.com](mailto:sakura520222@outlook.com)
- 🐛 问题反馈：[GitHub Issues](https://github.com/Sakura520222/Sakura-Bot/issues)
- 💬 讨论交流：[GitHub Discussions](https://github.com/Sakura520222/Sakura-Bot/discussions)

---

<div align="center">

**🌸 Sakura-Bot** · 让频道管理更智能

Made with ❤️ by [Sakura520222](https://github.com/Sakura520222)

[⭐ Star](https://github.com/Sakura520222/Sakura-Bot) · [🍴 Fork](https://github.com/Sakura520222/Sakura-Bot/fork) · [📖 文档](wiki) · [🐛 报告问题](https://github.com/Sakura520222/Sakura-Bot/issues)

</div>
