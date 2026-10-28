# 🌸 Sakura-Bot

[![Release](https://img.shields.io/github/v/release/Sakura520222/Sakura-Bot?style=flat-square)](https://github.com/Sakura520222/Sakura-Bot/releases)
[![License](https://img.shields.io/badge/License-AGPL--3.0-blue?style=flat-square)](LICENSE)
[![Python](https://img.shields.io/badge/python-3.13+-blue?style=flat-square&logo=python)](https://www.python.org/downloads/)
[![Code style: ruff](https://img.shields.io/endpoint?style=flat-square&url=https://raw.githubusercontent.com/astral-sh/ruff/main/assets/badge/v2.json)](https://github.com/astral-sh/ruff)
[![Docker](https://img.shields.io/badge/docker-20.10%2B-blue?style=flat-square&logo=docker)](https://www.docker.com/)
[![PRs Welcome](https://img.shields.io/badge/PRs-welcome-brightgreen?style=flat-square)](https://github.com/Sakura520222/Sakura-Bot/pulls)
[![Stars](https://img.shields.io/github/stars/Sakura520222/Sakura-Bot?style=flat-square)](https://github.com/Sakura520222/Sakura-Bot/stargazers)

Sakura-Bot 是一个 Telegram 频道管理工具：用 AI 总结频道内容、生成投票，支持消息转发、RAG 智能问答、订阅推送、投稿审核和浏览器端管理面板；群聊场景下提供 Sakura Guard 群管模块。

[English](README_EN.md) | [文档站](https://sakura-bot.pyabunny.com/) | [快速开始](wiki/User-Getting-Started.md) | [命令参考](wiki/User-Commands.md) | [Wiki](wiki/Home.md) | [报告问题](https://github.com/Sakura520222/Sakura-Bot/issues)

---

## 功能

- **AI 总结**：按频道配置每天、每周或多天定时总结，也可随时手动触发；总结自动存入 MySQL 并写入向量库
- **RAG 问答**：Agentic RAG，语义检索总结与原始消息双数据源，支持重排序、多轮对话和帖子链接引用，配额可控
- **消息转发**：关键词与正则过滤、自定义底栏模板、复制模式，Bot 无法转发时回退 UserBot
- **订阅与投稿**：用户可订阅频道总结推送、主动请求生成总结，或通过问答 Bot 投稿（支持匿名），由管理员审核发布
- **投票**：AI 根据频道内容生成互动投票，按频道配置公开/匿名模式和发送位置，支持一键重新生成
- **WebUI**：浏览器中管理频道、调度、转发规则、AI 配置、MySQL 数据、向量库和系统状态，JWT 认证，保存配置即时热重载
- **Sakura Guard**：按群启用昵称与消息规则、联系人卡片过滤、入群验证、`@admin 举报`、管理员审核、黑白名单和自助封禁申诉
- **基础设施**：多频道多管理员、中英双语界面、配置热重载与原子回滚、问答 Bot 双运行时热切换、优雅关闭

完整命令列表见[命令参考](wiki/User-Commands.md)，各功能的使用细节见[用户文档](wiki/User-Guide.md)。

## 快速开始

需要 **Python 3.13+** 或 **Docker 20.10+**，以及：

- Telegram Bot Token（从 [@BotFather](https://t.me/BotFather) 获取）
- Telegram API 凭证（从 [my.telegram.org](https://my.telegram.org) 获取）
- OpenAI 兼容 API Key（如 [DeepSeek](https://platform.deepseek.com/)、[OpenAI](https://platform.openai.com/)）
- MySQL 数据库（应用数据仅支持 MySQL，需自行部署，Compose 不包含 MySQL）

Docker 部署（推荐，一条命令更新）：

```bash
git clone https://github.com/Sakura520222/Sakura-Bot.git
cd Sakura-Bot
cp data/.env.example data/.env   # 编辑 .env，填入上述配置
bash scripts/deploy.sh           # 更新源码 + 拉取 GHCR 依赖镜像并启动
```

依赖镜像（Python 依赖、Go 二进制、前端产物）由 [GitHub Actions](.github/workflows/docker-build.yml) 构建并推送到 GHCR，服务器不再本地构建；业务代码仍从挂载的源码加载，日常更新同样只需 `bash scripts/deploy.sh`（纯 Python 改动甚至只需 `git pull` 后重启）。若镜像拉取被拒（401/403），到 GitHub 仓库 Packages 设置将 `sakura-bot` 包改为 public，或在服务器执行一次 `docker login ghcr.io`。fork 或源码部署仍可 `docker compose build && docker compose up -d`。

本地运行：

```bash
git clone https://github.com/Sakura520222/Sakura-Bot.git
cd Sakura-Bot
python -m venv .venv
source .venv/bin/activate        # Windows: .venv\Scripts\activate
pip install -r requirements.txt
cp data/.env.example data/.env   # 编辑 .env，填入上述配置
python main.py
```

首次运行需要输入手机号和验证码完成 Telegram 登录，之后会话文件会自动复用。各配置项说明、问答 Bot / UserBot / WebUI / Sakura Guard 的启用方式和常见问题，见[快速开始](wiki/User-Getting-Started.md)。

Docker 部署中，向 Bot 发送 `/update` 会更新宿主机仓库、重装依赖、重建 WebUI 并重启；更新使用快进合并、保留本地配置，遇到冲突会停止并提示。纯 Python 源码改动即时生效；涉及依赖、前端或 Go 二进制的变更，在宿主机执行 `bash scripts/deploy.sh` 拉取新依赖镜像即可。

### 问答 Bot 运行时

问答 Bot 默认使用 Go 实现（`qa-bot-go/`，资源占用约为 Python 版的 1/7），Python 版保留为回退。生效优先级为 `config.json` > 环境变量 `QA_BOT_RUNTIME`（部署级覆盖，见 `data/.env.example`）> `go`。管理员可用主 Bot 命令 `/qa_runtime`（别名 `/qa_运行时`）查看或切换运行时，也可在 WebUI「系统」页一键切换；切换会写入 `config.json` 持久化并立即重启问答 Bot（进行中的问答会话会中断），Go 二进制缺失时自动回退 Python。详见[运行时占用优化](wiki/Developer-Runtime-Footprint-Optimization.md)。

## 目录结构

```
Sakura-Bot/
├── core/               # 核心代码
│   ├── ai/             # AI 引擎（Agentic RAG、向量存储、嵌入、重排）
│   ├── bootstrap/      # 应用引导（初始化器编排）
│   ├── commands/       # 命令处理器
│   ├── config/         # 配置热重载（事件总线、文件监控、原子回滚）
│   ├── forwarding/     # 频道消息转发
│   ├── handlers/       # 事件处理器（UserBot、跨 Bot 通信、投稿审核）
│   ├── i18n/           # 国际化（zh-CN、en-US）
│   ├── infrastructure/ # 数据库、日志、异常、工具
│   ├── moderation/     # Sakura Guard 群管
│   ├── services/       # 业务服务层
│   ├── system/         # 调度、错误处理、进程、关闭
│   ├── telegram/       # Telegram 客户端
│   └── web_api/        # WebUI API（FastAPI、JWT 认证）
├── data/               # 配置（.env、config.json）、提示词、会话、向量库
├── web/                # WebUI 前端（Vue 3 + Vite + Naive UI）
├── wiki/               # 文档站源文件（VitePress）
├── tests/              # 测试
├── main.py             # 主程序入口
└── qa_bot.py           # 问答 Bot 入口
```

## 技术栈

| 技术 | 用途 |
| --- | --- |
| Python 3.13+ | 主语言 |
| Telethon / python-telegram-bot | Telegram 客户端（主 Bot / 问答 Bot） |
| OpenAI SDK | LLM 调用（任意 OpenAI 兼容服务） |
| FastAPI + Vue 3 | WebUI 后端与前端 |
| MySQL (aiomysql) / ChromaDB | 应用数据库 / 向量存储 |
| APScheduler / watchdog | 定时任务 / 配置热重载 |
| Ruff / Docker / GitHub Actions | 代码质量 / 部署 / CI |

## 文档

文档站：<https://sakura-bot.pyabunny.com/>（源文件在 [wiki/](wiki/Home.md)，推送 `main` 后自动发布）。

- 用户：[快速开始](wiki/User-Getting-Started.md)、[命令参考](wiki/User-Commands.md)、[WebUI 管理面板](wiki/User-WebUI-Guide.md)、[Sakura Guard 智能群管](wiki/User-Sakura-Guard.md)、[问答 Bot](wiki/User-QA-Bot-Guide.md)、[RAG 快速启动](wiki/User-RAG-Quickstart.md)
- 开发：[开发快速开始](wiki/Developer-Getting-Started.md)、[系统架构](wiki/Developer-Architecture.md)、[文档站维护](wiki/Developer-Documentation-Site.md)

## 贡献

欢迎 Issue 和 PR。PR 请提交到 `main` 分支（`dev` 由自动化维护），提交信息遵循 [Conventional Commits](https://www.conventionalcommits.org/)，代码风格使用 Ruff。详见[贡献指南](CONTRIBUTING.md)和[行为准则](wiki/Code-of-Conduct.md)。

## 许可证

[AGPL-3.0](LICENSE)。修改和网络服务形式的使用需以相同许可证提供源代码，衍生作品请保留本仓库来源：https://github.com/Sakura520222/Sakura-Bot 。本项目仅供个人学习使用，API 费用与合规责任由使用者自行承担。

## 致谢

- [Telethon](https://github.com/LonamiWebs/Telethon) - Telegram MTProto 客户端
- [python-telegram-bot](https://github.com/python-telegram-bot/python-telegram-bot) - Telegram Bot API 库
- [DeepSeek](https://www.deepseek.com/) / [OpenAI](https://openai.com/) - 大模型 API
- [SiliconFlow](https://siliconflow.cn/) - Embedding 与 Reranker API
- 所有[贡献者](https://github.com/Sakura520222/Sakura-Bot/graphs/contributors)

## 支持

- 问题反馈：[GitHub Issues](https://github.com/Sakura520222/Sakura-Bot/issues)
- 讨论交流：[GitHub Discussions](https://github.com/Sakura520222/Sakura-Bot/discussions)
- 邮件：[sakura520222@outlook.com](mailto:sakura520222@outlook.com)
