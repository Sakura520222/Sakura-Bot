# 🚀 快速开始

欢迎来到 Sakura-Bot！本指南将帮助您在 5 分钟内完成部署和配置。

> **分类**: 用户文档  
> **类型**: 教程  
> **难度**: ⭐  
> **更新时间**: 2026-10-31

[← 返回用户文档](User-Guide.md) | [Wiki 首页](Home.md)

---

## 📋 前置要求

在开始之前，请确保您具备以下条件：

- ✅ **Go 1.27+ 与 Node.js 22+**，或 **Docker Engine 与 Compose v2**
- ✅ **Telegram Bot Token**（从 [@BotFather](https://t.me/BotFather) 获取）
- ✅ **Telegram API 凭证**（从 [my.telegram.org](https://my.telegram.org) 获取）
- ✅ **AI API Key**（推荐使用 [DeepSeek](https://platform.deepseek.com/) 或 [OpenAI](https://platform.openai.com/)）
- ✅ **MySQL 数据库**（应用数据仅支持 MySQL，需要自行部署，Docker Compose 不包含 MySQL 服务）

---

## 🎯 方案选择

我们提供两种部署方式：

| 方式 | 优点 | 缺点 | 推荐场景 |
|------|------|------|----------|
| **🐳 Docker** | 环境隔离、一键部署、易于维护 | 需要安装 Docker | ✅ 推荐用于生产环境 |
| **💻 本地安装** | 灵活调试、适合开发 | 需要手动配置环境 | ✅ 推荐用于开发测试 |

---

## 🐳 方案一：Docker 部署（推荐）

使用 GitHub Release 自制 ZIP 或 tar.gz 时，参见[发布附件安装与更新](https://github.com/Sakura520222/Sakura-Bot/blob/main/docs/release-archives.md)。

### 步骤 1：克隆项目

```bash
git clone https://github.com/Sakura520222/Sakura-Bot.git
cd Sakura-Bot
```

### 步骤 2：配置环境变量

```bash
# 复制配置模板
cp data/.env.example data/.env

# 编辑配置文件
nano data/.env  # 或使用其他编辑器
```

### 步骤 3：填写必要配置

在 `data/.env` 文件中填写以下必要配置：

```env
# ===== Telegram 配置 =====
TELEGRAM_API_ID=your_api_id_here
TELEGRAM_API_HASH=your_api_hash_here
TELEGRAM_BOT_TOKEN=your_bot_token_here

# ===== AI 配置 =====
# 使用 DeepSeek（推荐）
LLM_API_KEY=your_deepseek_api_key
LLM_BASE_URL=https://api.deepseek.com
LLM_MODEL=deepseek-chat

# ===== 管理员配置 =====
# 未配置时所有管理命令与 WebUI 管理权限将被拒绝，请务必配置
REPORT_ADMIN_IDS=your_telegram_user_id_here

# ===== 数据库配置（MySQL 必需） =====
MYSQL_HOST=localhost
MYSQL_PORT=3306
MYSQL_USER=sakura_bot
MYSQL_PASSWORD=your_secure_password
MYSQL_DATABASE=sakura_bot_db
```

**获取方式：**
- `TELEGRAM_API_ID` 和 `TELEGRAM_API_HASH`：访问 [my.telegram.org](https://my.telegram.org)
- `TELEGRAM_BOT_TOKEN`：在 Telegram 中搜索 [@BotFather](https://t.me/BotFather)，使用 `/newbot` 创建
- `LLM_API_KEY`：访问 [DeepSeek](https://platform.deepseek.com/) 或 [OpenAI](https://platform.openai.com/)
- `REPORT_ADMIN_IDS`：您的 Telegram 用户 ID（可使用 [@userinfobot](https://t.me/userinfobot) 获取）
- `MYSQL_*`：先在 MySQL 中建库：`CREATE DATABASE sakura_bot_db CHARACTER SET utf8mb4;`，建表和迁移由程序启动时自动完成

### 步骤 4：启动服务

```bash
# 一键部署：更新源码 + 拉取完整镜像并启动
bash scripts/deploy.sh

# 查看日志
docker compose logs -f
```

镜像由 GitHub Actions 发布，包含主 Bot、助手和 WebUI。`scripts/deploy.sh` 拉取与 Git 提交匹配的镜像。`data/` 和 `logs/` 保存于宿主机。

**镜像拉取被拒（401/403）时**，任选其一处理：

- 到 GitHub 仓库的 **Packages** 设置，将 `sakura-bot` 包改为 public；
- 或在服务器上执行一次 `docker login ghcr.io`。

**Fork 或本地源码部署**需要自行构建镜像：

```bash
docker compose build
docker compose up -d
```

### 步骤 5：首次登录

启用 UserBot 且尚无可用会话时，在终端运行授权命令。已有 Telethon 会话可从原路径导入。

```bash
docker compose stop sakura-summary-bot
docker compose run --rm --no-deps sakura-summary-bot sakura-main --login-userbot
docker compose up -d --no-build
```

若部署固定了 `SAKURA_BOT_IMAGE`，这些命令也要使用同一镜像。主 Bot 使用 Bot Token，UserBot 使用手机号、验证码及已启用的两步验证密码。

### 步骤 6：添加频道

1. 在 Telegram 中找到您的 Bot
2. 发送 `/添加频道 https://t.me/your_channel`
3. Bot 将开始监控该频道


### ✅ 完成！

您已成功部署 Sakura-Bot！Bot 将按配置自动总结频道内容。

### 更新与重建

手动更新使用 `bash scripts/deploy.sh`。运行 systemd 的服务器可执行一次 `bash deploy/install-deployer.sh`，让管理员使用 `/update` 提交更新请求。部署器快进更新源码并拉取对应提交的镜像。fork 或本地源码构建使用 `docker compose build` 后执行 `docker compose up -d --no-build`。

[升级、备份与回滚](../docs/go-deployment.md)
---

## 💻 方案二：本地安装

### 步骤 1：克隆项目

```bash
git clone https://github.com/Sakura520222/Sakura-Bot.git
cd Sakura-Bot
```

### 步骤 2：配置环境变量

复制 `data/.env.example` 为 `data/.env`，填入 Telegram、MySQL 和 AI 配置。

### 步骤 3：构建并运行

安装 Go 和 Node.js，执行 `bash start.sh` 或 Windows 下的 `start.bat`。脚本构建 WebUI 和两个 Go 二进制。在仓库根目录执行 `bin/sakura-main --login-userbot`（Windows 使用 `bin\sakura-main.exe --login-userbot`）完成首次 UserBot 授权。

### ✅ 完成！

---

## 🎮 基础使用

常用命令：

```
/help          # 查看帮助
/立即总结      # 立即生成总结
/查看频道列表  # 查看配置的频道
/暂停 /恢复    # 暂停/恢复定时任务
```

完整命令列表见[命令参考](User-Commands.md)。

---

## ⚙️ 进阶配置

### 启用 RAG 智能问答（可选）

RAG 功能可以让 Bot 支持语义检索和智能问答。

**配置步骤：**

1. 获取 SiliconFlow API 密钥：访问 [SiliconFlow](https://siliconflow.cn/)
2. 在 `data/.env` 中添加：

```env
# Embedding 模型配置
EMBEDDING_API_KEY=your_siliconflow_api_key
EMBEDDING_API_BASE=https://api.siliconflow.cn/v1
EMBEDDING_MODEL=BAAI/bge-m3
EMBEDDING_DIMENSION=1024

# Reranker 配置（可选）
RERANKER_API_KEY=your_siliconflow_api_key
RERANKER_API_BASE=https://api.siliconflow.cn/v1/rerank
RERANKER_MODEL=BAAI/bge-reranker-v2-m3
```

> 进阶：向量存储后端由 `VECTOR_BACKEND` 控制，缺省 `chroma`（ChromaDB）。资源受限环境可切换 `lite`（SQLite WAL + numpy），但切换前必须先在全停机窗口运行 `scripts/migrate_vectors_to_sqlite.py` 完成向量迁移。

详细说明请参考 [RAG 快速启动](User-RAG-Quickstart.md)。

### 启用问答 Bot（可选）

问答 Bot 提供自然语言查询频道历史的功能。

**配置步骤：**

1. 创建新的 Bot（使用 [@BotFather](https://t.me/BotFather)）
2. 在 `data/.env` 中添加：

```env
QA_BOT_ENABLED=True
QA_BOT_TOKEN=your_qa_bot_token_here
QA_BOT_USER_LIMIT=3
QA_BOT_DAILY_LIMIT=200
```

> 问答 Bot 默认以 Go 运行时启动（`QA_BOT_RUNTIME` 缺省 `go`，静态二进制由依赖镜像提供），资源占用远低于 Python 版；二进制缺失时自动回退 Python。管理员可通过 `/qa_runtime` 命令或 WebUI「系统」页在线切换。

详细说明请参考 [问答 Bot 指南](User-QA-Bot-Guide.md)。

### 启用 WebUI 管理面板（可选）

在浏览器中管理频道、转发规则、数据库和系统状态：

```env
WEBUI_ENABLED=true
WEBUI_HOST=0.0.0.0
WEBUI_PORT=8080
```

启动后访问 `http://localhost:8080`，使用管理员 Telegram ID 登录。详细说明请参考 [WebUI 管理面板](User-WebUI-Guide.md)。

### 启用 UserBot（可选）

UserBot 使用真实 Telegram 账号，可访问私有频道、抓取消息更稳定，首次启用需输入验证码登录：

```env
USERBOT_ENABLED=true
USERBOT_PHONE_NUMBER=+8613800138000
USERBOT_SESSION_PATH=data/sessions/user_session
```

---

## 🔍 验证部署

### 检查 Bot 状态

向 Bot 发送 `/start` 命令，应该收到欢迎消息。

### 生成测试总结

1. 添加一个有消息的测试频道
2. 发送 `/立即总结`
3. 检查是否收到总结报告

### 查看 Docker 日志

```bash
docker compose logs -f
```

---

## ❓ 常见问题

### Q1: 首次运行需要登录吗？

**A:** 主 Bot 使用配置好的 Bot Token。启用 UserBot 时，首次授权需输入手机号和验证码。后续启动会复用会话文件。

### Q2: Bot 没有响应怎么办？

**A:** 检查以下几点：
1. 确认 Bot Token 是否正确
2. 检查网络连接
3. 查看日志排查错误

### Q3: 如何获取 Telegram 用户 ID？

**A:** 在 Telegram 中搜索 [@userinfobot](https://t.me/userinfobot)，发送任意消息即可获取您的用户 ID。

### Q4: 支持哪些 AI 服务？

**A:** 支持所有 OpenAI 兼容的 API 服务，包括：
- DeepSeek（推荐，性价比高）
- OpenAI 官方 API
- 任何提供 OpenAI 兼容接口的第三方服务

### Q5: Docker 部署如何备份数据？

**A:** 暂停服务写入后备份 MySQL 数据库、宿主机的 `data/`、自定义数据目录和 Compose 配置。可用下列命令归档 `data/`。

```bash
tar -czf backup-$(date +%Y%m%d).tar.gz data/
```

---

## 📚 下一步

恭喜您完成基础部署！接下来您可以：

- 📖 阅读 [问答 Bot 指南](User-QA-Bot-Guide.md) 了解智能问答功能
- 🔧 阅读 [投票功能](User-Poll-Feature.md) 配置频道投票
- 🚀 阅读 [RAG 快速启动](User-RAG-Quickstart.md) 启用语义检索
- 🧠 阅读 [多轮对话功能](User-Multi-Turn-Conversation.md) 了解上下文对话

---

## 💡 提示

- **生产环境推荐**：使用 Docker 部署，更稳定且易于维护
- **安全性**：不要将 `.env` 文件提交到版本控制系统
- **定期备份**：定期备份 `data/` 目录，防止数据丢失
- **日志监控**：定期查看日志，及时发现和解决问题

---

## 🔗 相关资源

- **[用户文档](User-Guide.md)** - 更多用户指南
- **[项目 README](../README.md)** - 项目概述
- **[GitHub Issues](https://github.com/Sakura520222/Sakura-Bot/issues)** - 问题反馈

---

<div align="center">

**🌸 开始您的智能频道管理之旅！**

[⭐ Star](https://github.com/Sakura520222/Sakura-Bot) · [🍴 Fork](https://github.com/Sakura520222/Sakura-Bot/fork)

</div>
