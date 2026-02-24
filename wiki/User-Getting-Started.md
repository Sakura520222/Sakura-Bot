# 🚀 快速开始

欢迎来到 Sakura-Bot！本指南将帮助您在 5 分钟内完成部署和配置。

> **分类**: 用户文档  
> **类型**: 教程  
> **难度**: ⭐  
> **更新时间**: 2026-02-25

[← 返回用户文档](User-Guide.md) | [Wiki 首页](Home.md)

---

## 📋 前置要求

在开始之前，请确保您具备以下条件：

- ✅ **Python 3.13+** 或 **Docker 20.10+**
- ✅ **Telegram Bot Token**（从 [@BotFather](https://t.me/BotFather) 获取）
- ✅ **Telegram API 凭证**（从 [my.telegram.org](https://my.telegram.org) 获取）
- ✅ **AI API Key**（推荐使用 [DeepSeek](https://platform.deepseek.com/) 或 [OpenAI](https://platform.openai.com/)）

---

## 🎯 方案选择

我们提供两种部署方式：

| 方式 | 优点 | 缺点 | 推荐场景 |
|------|------|------|----------|
| **🐳 Docker** | 环境隔离、一键部署、易于维护 | 需要安装 Docker | ✅ 推荐用于生产环境 |
| **💻 本地安装** | 灵活调试、适合开发 | 需要手动配置环境 | ✅ 推荐用于开发测试 |

---

## 🐳 方案一：Docker 部署（推荐）

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
REPORT_ADMIN_IDS=your_telegram_user_id_here
```

**获取方式：**
- `TELEGRAM_API_ID` 和 `TELEGRAM_API_HASH`：访问 [my.telegram.org](https://my.telegram.org)
- `TELEGRAM_BOT_TOKEN`：在 Telegram 中搜索 [@BotFather](https://t.me/BotFather)，使用 `/newbot` 创建
- `LLM_API_KEY`：访问 [DeepSeek](https://platform.deepseek.com/) 或 [OpenAI](https://platform.openai.com/)
- `REPORT_ADMIN_IDS`：您的 Telegram 用户 ID（可使用 [@userinfobot](https://t.me/userinfobot) 获取）

### 步骤 4：启动服务

```bash
# 启动容器
docker-compose up -d

# 查看日志
docker-compose logs -f
```

### 步骤 5：首次登录

首次运行时，您需要在日志中找到登录提示，然后在 Telegram 中完成登录：

1. 查看日志中的手机号输入提示
2. 在 Telegram 中输入手机号
3. 输入收到的验证码
4. （如果有两步验证）输入密码

登录成功后，Bot 将自动运行。

### 步骤 6：添加频道

1. 在 Telegram 中找到您的 Bot
2. 发送 `/添加频道 https://t.me/your_channel`
3. Bot 将开始监控该频道

### ✅ 完成！

您已成功部署 Sakura-Bot！Bot 将按配置自动总结频道内容。

---

## 💻 方案二：本地安装

### 步骤 1：克隆项目

```bash
git clone https://github.com/Sakura520222/Sakura-Bot.git
cd Sakura-Bot
```

### 步骤 2：创建虚拟环境（推荐）

```bash
# 创建虚拟环境
python -m venv venv

# 激活虚拟环境
# Windows:
venv\Scripts\activate
# Linux/macOS:
source venv/bin/activate
```

### 步骤 3：安装依赖

```bash
pip install -r requirements.txt
```

### 步骤 4：配置环境变量

```bash
# 复制配置模板
cp data/.env.example data/.env

# 编辑配置文件
nano data/.env  # 或使用其他编辑器
```

填写与 Docker 方案相同的必要配置（见上文）。

### 步骤 5：运行程序

```bash
python main.py
```

### 步骤 6：首次登录

与 Docker 方案相同，完成 Telegram 登录。

### ✅ 完成！

---

## 🎮 基础使用

### 查看帮助

```
/help
```

### 立即生成总结

```
/立即总结
```

### 查看配置的频道

```
/查看频道列表
```

### 暂停/恢复定时任务

```
/暂停
/恢复
```

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

详细说明请参考 [问答 Bot 指南](User-QA-Bot-Guide.md)。

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
docker-compose logs -f
```

---

## ❓ 常见问题

### Q1: 首次运行需要登录吗？

**A:** 是的。首次运行需要输入手机号和验证码完成 Telegram 登录。登录后会生成会话文件，后续运行无需重新登录。

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

**A:**
```bash
# 备份 data 目录
tar -czf backup-$(date +%Y%m%d).tar.gz data/

# 或使用 Docker 卷备份
docker run --rm -v sakura_bot_data:/data -v $(pwd):/backup alpine tar czf /backup/backup.tar.gz /data
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