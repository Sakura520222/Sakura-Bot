# 🌸 Sakura-Bot

[![Release](https://img.shields.io/github/v/release/Sakura520222/Sakura-Bot?style=flat-square)](https://github.com/Sakura520222/Sakura-Bot/releases)
[![License](https://img.shields.io/badge/License-AGPL--3.0-blue?style=flat-square)](LICENSE)
[![Python](https://img.shields.io/badge/python-3.13+-blue?style=flat-square&logo=python)](https://www.python.org/downloads/)
[![Code style: ruff](https://img.shields.io/endpoint?style=flat-square&url=https://raw.githubusercontent.com/astral-sh/ruff/main/assets/badge/v2.json)](https://github.com/astral-sh/ruff)
[![Docker](https://img.shields.io/badge/docker-20.10%2B-blue?style=flat-square&logo=docker)](https://www.docker.com/)
[![PRs Welcome](https://img.shields.io/badge/PRs-welcome-brightgreen?style=flat-square)](https://github.com/Sakura520222/Sakura-Bot/pulls)
[![Stars](https://img.shields.io/github/stars/Sakura520222/Sakura-Bot?style=flat-square)](https://github.com/Sakura520222/Sakura-Bot/stargazers)

> **基于AI技术的智能Telegram频道管理工具** 🤖✨

[English](README_EN.md) | [🌸 文档站](https://sakura-bot.pyabunny.com/) | [📖 Wiki](wiki/Home.md) | [📘 用户文档](wiki/User-Guide.md) | [👨‍💻 开发者文档](wiki/Developer-Guide.md) | [报告问题](https://github.com/Sakura520222/Sakura-Bot/issues) | [功能建议](https://github.com/Sakura520222/Sakura-Bot/issues)

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
- 🌐 **WebUI 管理面板** - 通过浏览器管理频道、转发、数据库和系统状态
- 📤 **可靠转发** - 支持过滤、底栏模板、UserBot 回退和热重载去重
- 🛡 **Sakura Guard 群管** - 按群启用昵称/消息规则、入群验证、管理员通知操作和永久黑白名单，详见 [使用说明](wiki/User-Sakura-Guard.md)

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

项目目录通过 `.:/app` 挂载到容器，`/update` 会直接更新宿主机仓库，然后安装 Python 依赖、构建 WebUI 并重启。镜像已包含 Git、Node.js 和 npm；GitHub SSH 远程地址在容器内通过 HTTPS 拉取，无需挂载宿主机 SSH 密钥。更新使用快进合并，保留本地配置；遇到冲突或分支分叉时会停止并提示处理。

旧部署遗留的 `web/package-lock.json` 修改和 `web/tsconfig.tsbuildinfo` 缓存会先备份到 Git 目录中的 `sakura-update-backups/generated-*`，再清理并更新；合并失败会恢复原内容。已暂存的生成文件或本地 `package.json` 修改需要手动处理，避免覆盖自定义依赖。配置、提示词和其他源码不会被自动清理。前端使用 `npm ci` 和不生成增量缓存的类型检查，避免构建再次污染仓库。

旧版 Docker 部署需先在宿主机更新上述部署文件，再重建一次：

```bash
docker compose up -d --build
```

源码、前端构建产物和 `data/` 均保存在宿主机。重新创建容器时会按源码指纹同步 Python 依赖和 WebUI；普通重启会复用已安装依赖和已有构建。涉及系统依赖或 Dockerfile 的更新仍需在宿主机执行上述重建命令。

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

| 功能             | 描述                                         | 状态 |
| ---------------- | -------------------------------------------- | ---- |
| **🤖 AI智能总结** | 利用先进AI模型深度分析并提取频道消息核心要点 | ✅    |
| **🔍 自动监控**   | 定期自动抓取并整理监控频道的内容             | ✅    |
| **⏰ 多频率模式** | 支持每天、每周、每周多天等多种自动总结频率   | ✅    |
| **⚡ 手动触发**   | 支持管理员通过命令随时生成总结               | ✅    |

### AI与配置

| 功能               | 描述                              | 状态 |
| ------------------ | --------------------------------- | ---- |
| **🔧 自定义AI配置** | 支持多种OpenAI兼容API服务         | ✅    |
| **🎯 自定义提示词** | 灵活调整AI总结风格和内容          | ✅    |
| **🎯 投票提示词**   | 独立配置AI生成投票内容的提示词    | ✅    |
| **🤖 问答Bot人格**  | 自定义问答Bot的人格设定和回答风格 | ✅    |

### 频道管理

| 功能                 | 描述                                         | 状态 |
| -------------------- | -------------------------------------------- | ---- |
| **👥 多管理员支持**   | 可配置多个管理员ID，报告同时发送给所有管理员 | ✅    |
| **🌐 多频道支持**     | 同时监控和总结多个频道的内容                 | ✅    |
| **📝 长消息分段**     | 智能处理超长总结内容，自动分段发送           | ✅    |
| **⏱️ 智能时间记录**   | 自动记录总结时间，仅获取新消息提高效率       | ✅    |
| **🕐 频道级时间配置** | 为每个频道单独配置自动总结时间               | ✅    |

### 高级功能

| 功能                  | 描述                                               | 状态 |
| --------------------- | -------------------------------------------------- | ---- |
| **🔍 智能问答系统**    | 基于RAG技术的自然语言问答，支持语义检索历史总结    | ✅    |
| **🧠 向量存储**        | 使用ChromaDB存储总结向量，支持语义搜索             | ✅    |
| **🎯 重排序优化**      | 使用BGE-Reranker精排检索结果，提升准确率           | ✅    |
| **💬 多轮对话**        | 支持上下文感知的对话，AI能理解代词指代和历史对话   | ✅    |
| **🛡️ 错误恢复**        | 智能重试机制、健康检查和优雅关闭                   | ✅    |
| **📊 互动投票**        | 总结后自动在讨论组生成投票消息                     | ✅    |
| **🎯 频道级投票配置**  | 为每个频道单独配置投票发送位置和启用状态           | ✅    |
| **🔄 投票重新生成**    | 管理员可通过一键按钮重新生成投票                   | ✅    |
| **📜 历史记录**        | 自动保存所有总结到数据库，支持查询、导出和统计     | ✅    |
| **🌍 国际化支持**      | 完整的多语言支持系统，所有模块已国际化             | ✅    |
| **📢 用户订阅系统**    | 用户可订阅感兴趣的频道，收到新总结时自动推送通知   | ✅    |
| **📝 总结请求功能**    | 用户可主动请求生成频道总结，由主Bot管理员审核处理  | ✅    |
| **🤖 跨Bot通信**       | 问答Bot和主Bot通过数据库队列实现进程间通信         | ✅    |
| **🔧 代码优化**        | 总结生成流程模块化，支持代码复用和统一管理         | ✅    |
| **📱 命令菜单**        | QA Bot自动注册命令菜单，用户可直接查看所有可用命令 | ✅    |
| **🗄️ MySQL数据库支持** | 使用MySQL数据库，提供高性能和并发支持            | ✅    |
| **📤 频道消息转发**    | 智能转发频道消息到目标频道，支持关键词和正则过滤   | ✅    |
| **⚡ 启动时检查**      | 自动检测旧数据库并通知管理员迁移建议               | ✅    |
| **📝 投稿审核系统**    | 用户通过QA Bot投稿，管理员审核后发布到频道         | ✅    |
| **🤖 Agentic RAG**     | 智能体RAG循环，支持工具调用与固定管线双模式        | ✅    |
| **📊 双数据源搜索**    | 语义搜索同时检索总结和原始消息，提升准确率         | ✅    |
| **🔄 配置热重载**      | 无需重启即可热更新配置，支持原子性回滚             | ✅    |
| **🗳️ 投票公开/匿名**   | 为每个频道单独配置投票为公开或匿名模式             | ✅    |
| **🎯 自动趣味投票**    | AI自动生成趣味投票，可按频道独立配置               | ✅    |
| **🌐 WebUI 管理面板**  | 基于 Vue.js 的 Web 管理界面，支持深色主题和 JWT 认证 | ✅    |
| **📱 帖子链接支持**    | RAG 工具支持帖子链接引用，答案可追溯至原始消息     | ✅    |
| **🔒 匿名投稿**        | 投稿审核系统支持匿名投稿模式，保护投稿者隐私         | ✅    |
| **📊 向量存储管理**    | WebUI 支持向量存储集合管理，包括清空和删除文档        | ✅    |
| **📈 统计与分页增强**  | 新增统计 API 接口，总结查询支持分页偏移量              | ✅    |
| **🗄️ WebUI 数据库管理** | WebUI 支持 MySQL 表浏览、分页查询和受控增删改操作        | ✅    |
| **🧭 主 Bot 内联菜单**  | 欢迎和帮助消息提供按钮式入口，快速访问常用管理功能      | ✅    |
| **⚡ WebUI 配置即时生效** | WebUI 保存配置后显式触发热重载，减少重启需求            | ✅    |
| **🛡️ 转发回退与去重**   | Bot 转发失败时可回退 UserBot，并去重配置热重载事件       | ✅    |

---

## 📋 使用指南

### 命令列表

#### 基础命令

| 命令     | 别名    | 功能                       | 示例     |
| -------- | ------- | -------------------------- | -------- |
| `/start` | `/开始` | 查看欢迎消息和基本介绍     | `/start` |
| `/help`  | `/帮助` | 查看完整命令列表和使用说明 | `/help`  |

> `/start` 和 `/help` 会提供内联菜单按钮，管理员可通过按钮快速进入频道、转发、系统和问答 Bot 管理功能。

#### 核心功能

| 命令       | 别名        | 功能                     | 示例       |
| ---------- | ----------- | ------------------------ | ---------- |
| `/summary` | `/立即总结` | 立即生成本周频道消息汇总 | `/summary` |

#### AI配置

| 命令          | 别名          | 功能               | 示例          |
| ------------- | ------------- | ------------------ | ------------- |
| `/showprompt` | `/查看提示词` | 查看当前AI提示词   | `/showprompt` |
| `/setprompt`  | `/设置提示词` | 设置自定义AI提示词 | `/setprompt`  |
| `/showaicfg`  | `/查看AI配置` | 查看当前AI配置信息 | `/showaicfg`  |
| `/setaicfg`   | `/设置AI配置` | 设置自定义AI配置   | `/setaicfg`   |

#### 频道管理

| 命令             | 别名            | 功能                 | 示例                                  |
| ---------------- | --------------- | -------------------- | ------------------------------------- |
| `/showchannels`  | `/查看频道列表` | 查看所有配置的频道   | `/showchannels`                       |
| `/addchannel`    | `/添加频道`     | 添加新频道到监控列表 | `/addchannel https://t.me/example`    |
| `/deletechannel` | `/删除频道`     | 从监控列表中删除频道 | `/deletechannel https://t.me/example` |

#### 调度配置

| 命令                     | 别名                    | 功能                       | 示例                     |
| ------------------------ | ----------------------- | -------------------------- | ------------------------ |
| `/showchannelschedule`   | `/查看频道时间配置`     | 查看频道自动总结时间配置   | `/showchannelschedule`   |
| `/setchannelschedule`    | `/设置频道时间配置`     | 设置频道自动总结时间       | `/setchannelschedule`    |
| `/deletechannelschedule` | `/删除频道时间配置`     | 删除频道自动总结时间配置   | `/deletechannelschedule` |
| `/clearsummarytime`      | `/清除总结时间`         | 清除上次总结时间记录       | `/clearsummarytime`      |
| `/setsendtosource`       | `/设置报告发送回源频道` | 设置是否将报告发送回源频道 | `/setsendtosource`       |

#### 投票配置

| 命令                 | 别名                | 功能             | 示例                 |
| -------------------- | ------------------- | ---------------- | -------------------- |
| `/channelpoll`       | `/查看频道投票配置` | 查看频道投票配置 | `/channelpoll`       |
| `/setchannelpoll`    | `/设置频道投票配置` | 设置频道投票配置 | `/setchannelpoll`    |
| `/deletechannelpoll` | `/删除频道投票配置` | 删除频道投票配置 | `/deletechannelpoll` |

#### 评论区欢迎配置

| 命令                    | 别名              | 功能                   | 示例                    |
| ----------------------- | ----------------- | ---------------------- | ----------------------- |
| `/showcommentwelcome`   | `/查看评论区欢迎` | 查看频道评论区欢迎配置 | `/showcommentwelcome`   |
| `/setcommentwelcome`    | `/设置评论区欢迎` | 设置频道评论区欢迎配置 | `/setcommentwelcome`    |
| `/deletecommentwelcome` | `/删除评论区欢迎` | 删除频道评论区欢迎配置 | `/deletecommentwelcome` |

**功能说明**：
- 为每个频道单独配置评论区欢迎消息
- 支持自定义欢迎消息内容和按钮文本
- 可为频道禁用欢迎消息功能
- 默认消息："🌸 评论区已开放，快来抢占沙发吧～"
- 默认按钮："申请周报总结"

#### 系统控制

| 命令        | 别名    | 功能             | 示例        |
| ----------- | ------- | ---------------- | ----------- |
| `/pause`    | `/暂停` | 暂停所有定时任务 | `/pause`    |
| `/resume`   | `/恢复` | 恢复所有定时任务 | `/resume`   |
| `/restart`  | `/重启` | 重启机器人       | `/restart`  |
| `/shutdown` | `/关机` | 彻底停止机器人   | `/shutdown` |

#### 问答Bot控制

| 命令          | 别名       | 功能                | 示例          |
| ------------- | ---------- | ------------------- | ------------- |
| `/qa_status`  | `/qa_状态` | 查看问答Bot运行状态 | `/qa_status`  |
| `/qa_start`   | `/qa_启动` | 启动问答Bot         | `/qa_start`   |
| `/qa_stop`    | `/qa_停止` | 停止问答Bot         | `/qa_stop`    |
| `/qa_restart` | `/qa_重启` | 重启问答Bot         | `/qa_restart` |
| `/qa_stats`   | `/qa_统计` | 查看问答Bot详细统计 | `/qa_stats`   |

#### 调试与日志

| 命令            | 别名            | 功能             | 示例            |
| --------------- | --------------- | ---------------- | --------------- |
| `/showloglevel` | `/查看日志级别` | 查看当前日志级别 | `/showloglevel` |
| `/setloglevel`  | `/设置日志级别` | 设置日志级别     | `/setloglevel`  |
| `/clearcache`   | `/清除缓存`     | 清除讨论组ID缓存 | `/clearcache`   |
| `/changelog`    | `/更新日志`     | 查看更新日志     | `/changelog`    |

#### 历史记录

| 命令       | 别名    | 功能         | 示例                   |
| ---------- | ------- | ------------ | ---------------------- |
| `/history` | `/历史` | 查看历史总结 | `/history`             |
| `/export`  | `/导出` | 导出历史记录 | `/export channel1 csv` |
| `/stats`   | `/统计` | 查看统计数据 | `/stats`               |

#### 语言设置

| 命令        | 别名    | 功能               | 示例                            |
| ----------- | ------- | ------------------ | ------------------------------- |
| `/language` | `/语言` | 查看或切换界面语言 | `/language` / `/language zh-CN` |

#### 数据库迁移

| 命令              | 别名        | 功能             | 示例              |
| ----------------- | ----------- | ---------------- | ----------------- |
| `/migrate_check`  | `/迁移检查` | 检查迁移准备状态 | `/migrate_check`  |
| `/migrate_start`  | `/开始迁移` | 开始数据库迁移   | `/migrate_start`  |
| `/migrate_status` | `/迁移状态` | 查看迁移进度     | `/migrate_status` |

#### 频道消息转发

| 命令                       | 别名        | 功能                       | 示例                                               |
| -------------------------- | ----------- | -------------------------- | -------------------------------------------------- |
| `/forwarding`              | `/转发状态` | 查看转发功能状态和规则列表 | `/forwarding`                                      |
| `/forwarding_enable`       | `/启用转发` | 启用转发功能               | `/forwarding_enable`                               |
| `/forwarding_disable`      | `/禁用转发` | 禁用转发功能               | `/forwarding_disable`                              |
| `/forwarding_stats [频道]` | `/转发统计` | 查看转发统计信息           | `/forwarding_stats` / `/forwarding_stats channel1` |

**功能说明**：
- 智能转发频道消息到目标频道
- 支持关键词白名单和黑名单过滤
- 支持正则表达式模式匹配
- 支持转发模式（显示来源）和复制模式（不显示来源）
- 自动记录已转发消息，避免重复转发
- 提供详细的转发统计信息
- 转发命令仅管理员可执行，防止非管理员修改规则
- 当 Bot 无法解析或转发目标实体时，可回退到 UserBot/监听客户端执行转发
- 配置保存或 WebUI 修改后支持热重载去重，避免重复刷新规则

**配置方式**：
在 `data/config.json` 中配置转发规则：

```json
{
  "forwarding": {
    "enabled": true,
    "rules": [
      {
        "source_channel": "https://t.me/source_channel",
        "target_channel": "https://t.me/my_channel",
        "keywords": ["重要", "新闻", "更新"],
        "blacklist": ["广告", "垃圾"],
        "patterns": [],
        "blacklist_patterns": [],
        "copy_mode": false
      }
    ]
  }
}
```

**配置说明**：
- `enabled`: 是否启用转发功能（true/false）
- `source_channel`: 源频道URL
- `target_channel`: 目标频道URL
- `keywords`: 白名单关键词列表（包含任一关键词即转发）
- `blacklist`: 黑名单关键词列表（包含任一关键词不转发）
- `patterns`: 正则表达式白名单（匹配任一模式即转发）
- `blacklist_patterns`: 正则表达式黑名单（匹配任一模式不转发）
- `copy_mode`: 复制模式（true=不显示来源，false=显示来源）
- `forward_original_only`: 只转发原创消息（true=只转发频道原创消息，不转发转发消息；false=转发所有消息，默认false）

**底栏自定义**：
- 支持自定义转发消息的底部信息
- 可用模板变量：`{source_link}`, `{source_title}`, `{target_title}`, `{source_channel}`, `{target_channel}`, `{message_id}`, `{assistant_bot}`, `{submission}`
- 示例：`/forwarding_footer https://t.me/source https://t.me/target "📢 来源: {source_title}"`
- 支持默认底栏的启用/禁用

### WebUI 管理面板

WebUI 提供浏览器端管理界面，可用于查看仪表盘、管理频道与调度、触发总结、管理转发规则、查看统计信息和执行受控系统操作。

**最新能力**：
- **数据库管理**：支持 MySQL 表浏览、分页查询和受控增删改，适合管理员排查数据问题
- **配置即时生效**：通过 WebUI 保存配置后会显式触发热重载，AI、调度和转发配置无需等待文件监听
- **转发规则管理**：支持底栏模板和占位符插入，减少手动编辑 `config.json` 的风险
- **系统操作**：支持查看状态、调整日志级别、暂停/恢复任务和触发优雅重启

> ⚠️ 数据库管理和系统操作属于高危功能，请仅在受信任网络中启用 WebUI，并确保 JWT 认证和管理员列表配置正确。

#### 转发规则管理

| 命令                      | 别名            | 功能                   | 示例                                      |
| ------------------------- | --------------- | ---------------------- | ----------------------------------------- |
| `/forwarding_add_rule`    | `/添加转发规则` | 添加转发规则（持久化） | `/forwarding_add_rule 源频道 目标频道`    |
| `/forwarding_remove_rule` | `/删除转发规则` | 删除转发规则           | `/forwarding_remove_rule 源频道 目标频道` |
| `/forwarding_rule_info`   | `/规则详情`     | 查看规则详情           | `/forwarding_rule_info`                   |

#### 关键词和正则过滤

| 命令                             | 别名            | 功能             | 示例                                              |
| -------------------------------- | --------------- | ---------------- | ------------------------------------------------- |
| `/forwarding_keywords`           | `/关键词白名单` | 设置关键词白名单 | `/forwarding_keywords add/remove/clear`           |
| `/forwarding_blacklist`          | `/关键词黑名单` | 设置关键词黑名单 | `/forwarding_blacklist add/remove/clear`          |
| `/forwarding_patterns`           | `/正则白名单`   | 设置正则白名单   | `/forwarding_patterns add/remove/clear`           |
| `/forwarding_blacklist_patterns` | `/正则黑名单`   | 设置正则黑名单   | `/forwarding_blacklist_patterns add/remove/clear` |

#### 转发模式设置

| 命令                        | 别名          | 功能                       | 示例                               |
| --------------------------- | ------------- | -------------------------- | ---------------------------------- |
| `/forwarding_copy_mode`     | `/复制模式`   | 设置复制模式（不显示来源） | `/forwarding_copy_mode on/off`     |
| `/forwarding_original_only` | `/只转发原创` | 设置只转发原创消息         | `/forwarding_original_only on/off` |

#### 底栏配置

| 命令                         | 别名        | 功能              | 示例                                            |
| ---------------------------- | ----------- | ----------------- | ----------------------------------------------- |
| `/forwarding_footer`         | `/转发底栏` | 设置自定义底栏    | `/forwarding_footer 源频道 目标频道 "底栏文本"` |
| `/forwarding_default_footer` | `/默认底栏` | 启用/禁用默认底栏 | `/forwarding_default_footer on/off`             |

#### 帮助命令

| 命令               | 别名        | 功能         | 示例               |
| ------------------ | ----------- | ------------ | ------------------ |
| `/forwarding_help` | `/转发帮助` | 转发命令帮助 | `/forwarding_help` |

#### 投稿功能（QA Bot）

| 命令             | 别名        | 功能                   | 示例             |
| ---------------- | ----------- | ---------------------- | ---------------- |
| `/submit`        | `/投稿`     | 通过QA Bot提交投稿内容 | `/submit`        |
| `/cancel_submit` | `/取消投稿` | 取消正在进行的投稿     | `/cancel_submit` |

**功能说明**：
- 用户通过QA Bot的深链接入口提交投稿
- 支持文本、图片、文件等多种类型投稿
- 管理员收到投稿通知后可审核通过或拒绝
- 投稿内容经审核后发布到指定频道

#### 自动趣味投票

| 命令             | 别名            | 功能                   | 示例             |
| ---------------- | --------------- | ---------------------- | ---------------- |
| `/set_auto_poll` | `/设置自动投票` | 为频道配置自动趣味投票 | `/set_auto_poll` |

**功能说明**：
- 为每个频道单独配置AI自动生成趣味投票
- 支持公开/匿名投票模式切换
- 投票内容基于频道消息自动生成

**说明**：仅支持 MySQL 数据库，请确保已配置 MySQL 环境。

#### UserBot 管理

| 命令                        | 别名            | 功能                                           | 示例                                  |
| --------------------------- | --------------- | ---------------------------------------------- | ------------------------------------- |
| `/userbot_status`           | `/userbot_状态` | 查看 UserBot 运行状态和用户信息                | `/userbot_status`                     |
| `/userbot_join <频道链接>`  | `/userbot_加入` | 手动加入频道（支持公开频道和私有频道邀请链接） | `/userbot_join https://t.me/channel`  |
| `/userbot_leave <频道链接>` | `/userbot_离开` | 手动离开频道                                   | `/userbot_leave https://t.me/channel` |
| `/userbot_list`             | `/userbot_列表` | 列出 UserBot 已加入的所有频道                  | `/userbot_list`                       |

**功能说明**：
- **UserBot 自动加入**：转发功能启用时，UserBot 会自动加入所有转发配置的源频道
- **支持的频道链接格式**：
  - 公开频道：`https://t.me/channelname` 或 `@channelname`
  - 私有频道：`https://t.me/+invitecode`（邀请链接）
- **智能错误处理**：
  - 私有频道无法自动加入时，提示需要手动邀请
  - 频道不存在或无权限时，显示详细错误信息
  - 无效链接格式时，提示正确用法
- **自动化通知**：
  - 自动加入开始时通知管理员
  - 自动加入完成后发送结果汇总
  - 失败的频道列表详细说明失败原因

**注意事项**：
- 私有频道无法自动加入，需要手动将 UserBot 添加为成员
- UserBot 需要有加入频道的权限
- 自动加入功能仅在转发功能启用时触发
- 可通过命令手动管理 UserBot 加入的频道

**MySQL配置示例**：
```env
# ===== 数据库配置 =====
DATABASE_TYPE=mysql

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

| 命令            | 功能                           |
| --------------- | ------------------------------ |
| `/start`        | 查看欢迎消息和功能介绍         |
| `/help`         | 查看完整帮助文档               |
| `/status`       | 查看当前配额使用情况和会话状态 |
| `/clear`        | 清除当前对话历史，开始新会话   |
| `/view_persona` | 查看当前问答Bot的人格设定      |

#### 订阅管理

| 命令                          | 功能                         | 示例                                    |
| ----------------------------- | ---------------------------- | --------------------------------------- |
| `/listchannels`               | 列出可订阅的频道（自动注册） | `/listchannels`                         |
| `/subscribe <频道链接>`       | 订阅频道总结推送（自动注册） | `/subscribe https://t.me/channel`       |
| `/unsubscribe <频道链接>`     | 取消频道订阅                 | `/unsubscribe https://t.me/channel`     |
| `/mysubscriptions`            | 查看我的订阅列表             | `/mysubscriptions`                      |
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
# 未配置时所有管理命令与 WebUI 管理权限将被拒绝（fail-closed），请务必配置
REPORT_ADMIN_IDS=your_admin_id_here,another_admin_id_here

# ===== 日志级别 =====
LOG_LEVEL=INFO

# ===== 日志详细配置 =====
# 是否启用文件日志（true/false）
LOG_TO_FILE=true
# 日志文件路径
LOG_FILE_PATH=logs/sakura-bot.log
# 组件日志默认与主日志同目录：logs/qa-bot.log、logs/webui.log
# 单个日志文件最大大小（字节，默认10485760 = 10MB）
LOG_FILE_MAX_SIZE=10485760
# 保留的日志备份文件数量（0-20）
LOG_FILE_BACKUP_COUNT=5
# 是否启用控制台日志（true/false）
LOG_TO_CONSOLE=true
# 是否启用彩色日志输出（仅控制台，true/false）
LOG_COLORIZE=true

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

# ===== UserBot 配置 =====
# UserBot 使用您的真实 Telegram 账号，具有更高的权限
# 可以访问私有频道，抓取消息更稳定

# 是否启用 UserBot（true/false）
USERBOT_ENABLED=true

# UserBot 手机号（国际格式，例如：+8613800138000）
# 首次启用时需要输入验证码进行登录
USERBOT_PHONE_NUMBER=+8613800138000

# UserBot Session 文件路径（自动保存，下次启动自动登录）
USERBOT_SESSION_PATH=data/sessions/user_session

# UserBot 降级策略（true/false）
# 如果 UserBot 不可用，是否降级到临时会话方式
USERBOT_FALLBACK_TO_BOT=false

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

# ===== WebUI 管理面板配置（可选） =====
# 启用 WebUI 管理界面（true/false）
WEBUI_ENABLED=true
# WebUI 监听地址
WEBUI_HOST=0.0.0.0
# WebUI 监听端口
WEBUI_PORT=8080
# WebUI 开发模式（免认证登录，仅用于开发环境）
WEBUI_DEV_MODE=false
```

> **提示**：RAG系统需要额外的API密钥，推荐使用 [SiliconFlow](https://siliconflow.cn/) 获取Embedding和Reranker服务的API密钥。详细配置请参考 [RAG快速启动指南](wiki/RAG_QUICKSTART.md)。

---

## 🏗️ 项目结构

```
Sakura-Bot/
│
├── 📁 core/                          # 核心模块目录
│   ├── ai/                           # AI引擎（Agentic RAG、向量存储、嵌入、重排）
│   ├── bootstrap/                    # 应用引导程序（13步初始化器）
│   ├── commands/                     # 命令处理器（按功能分组）
│   ├── config/                       # 配置热重载（事件总线、文件监控、原子回滚）
│   ├── forwarding/                   # 频道消息转发（关键词/正则过滤、底栏）
│   ├── handlers/                     # 事件处理器（UserBot、跨Bot通信、投稿审核）
│   ├── i18n/                         # 国际化系统（zh-CN, en-US）
│   ├── infrastructure/               # 基础设施（数据库、日志、异常、工具）
│   ├── integrations/                 # 第三方集成
│   ├── migrations/                   # 数据库迁移
│   ├── services/                     # 业务服务层
│   ├── system/                       # 系统管理（调度、错误处理、进程、关闭）
│   ├── telegram/                     # Telegram客户端
│   ├── utils/                        # 工具函数
│   └── web_api/                      # WebUI API 服务器（FastAPI、JWT 认证）
│
├── 📁 data/                          # 数据目录
│   ├── .env                          # 环境变量配置
│   ├── config.json                   # AI配置文件（支持热重载）
│   ├── prompt.txt                    # 总结提示词
│   ├── poll_prompt.txt               # 投票提示词
│   ├── qa_persona.txt                # 问答Bot人格配置
│   ├── cache/                        # 缓存目录
│   ├── sessions/                     # Telegram会话目录
│   └── vectors/                      # 向量数据库目录
│
├── 📁 tests/                         # 测试目录
├── 📁 web/                           # WebUI 前端源码（Vue.js + Vite）
├── 📁 wiki/                          # 文档目录
├── 📁 .github/                       # GitHub工作流
│
├── 📄 main.py                        # 主程序入口
├── 📄 qa_bot.py                      # 问答Bot入口
├── 📄 requirements.txt               # Python依赖
├── 📄 pyproject.toml                 # 项目配置（Ruff、覆盖率）
├── 📄 docker-compose.yml             # Docker Compose配置
├── 📄 Dockerfile                     # Docker镜像构建
└── 📄 README.md                      # 本文件
```

---

## 🔧 技术栈

| 技术                    | 用途                                | 版本   |
| ----------------------- | ----------------------------------- | ------ |
| **Python**              | 主编程语言                          | 3.13+  |
| **Telethon**            | Telegram MTProto API客户端（主Bot） | 1.34+  |
| **python-telegram-bot** | Telegram Bot API客户端（QA Bot）    | 20.0+  |
| **OpenAI SDK**          | AI API集成（支持DeepSeek/OpenAI等） | 1.0+   |
| **APScheduler**         | 定时任务调度                        | 3.10+  |
| **ChromaDB**            | 向量数据库（RAG系统）               | 0.4+   |
| **aiomysql**            | 异步MySQL数据库                     | 0.2+   |
| **Pydantic Settings**   | 配置管理与数据验证                  | 2.0+   |
| **httpx**               | HTTP客户端（Reranker调用）          | 0.27+  |
| **watchdog**            | 文件监控（配置热重载）              | 4.0+   |
| **python-dotenv**       | 环境变量管理                        | 1.0+   |
| **FastAPI**             | WebUI API 服务器框架                | 0.115+ |
| **Vue.js + Vite**       | WebUI 前端框架                      | 3.5+   |
| **Ruff**                | 代码检查与格式化                    | 0.8+   |
| **Docker**              | 容器化部署                          | 20.10+ | |

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

### 如何使用 WebUI 管理面板？

1. 在 `.env` 中设置 `WEBUI_ENABLED=true`
2. 启动 Bot 后访问 `http://localhost:8080`
3. 使用管理员 Telegram ID 登录（JWT 认证）
4. 支持深色/浅色主题切换，可在面板中管理频道、查看统计、触发总结等

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
   git clone https://github.com/Sakura520222/Sakura-Bot.git
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

本项目采用 **GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可**。

### 许可证要点

- **AGPL-3.0**：要求修改后的代码必须开源，通过网络提供服务时也需提供源代码
- **署名要求**：所有衍生作品必须保留原作者的项目链接和作者署名信息
- **API 责任**：使用者需自行承担 API 费用及相关法律责任

### 重要说明

- 本项目仅供**个人学习使用**，禁止任何非法用途
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
