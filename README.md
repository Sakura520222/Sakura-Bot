# 🌸 Sakura-Bot

[![Release](https://img.shields.io/github/v/release/Sakura520222/Sakura-Bot?style=flat-square)](https://github.com/Sakura520222/Sakura-Bot/releases)
[![License](https://img.shields.io/badge/License-AGPL--3.0-blue?style=flat-square)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.27+-blue?style=flat-square&logo=go)](https://go.dev/dl/)
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
- **多 AI 用途路由**：独立服务档案分配总结、助手、图片识别及投票模型，支持模型列表与手动输入、脱敏密钥、连接/调用测试；[配置说明](wiki/AI_PROFILES.md)
- **Sakura Guard**：按群启用昵称与消息规则、联系人卡片过滤、入群验证、`@admin 举报`、管理员审核、黑白名单和自助封禁申诉
- **Sakura Wiki**：现有 WebUI 的 `/wiki` 提供免登录频道百科、证据文章、交互式知识地图、时间轴与真实消息画像；AI 自动维护、管理员审核发布，Go 助手共享同一 MySQL 快照。默认关闭；[使用与管理手册](docs/sakura-wiki.md)。仓库 `wiki/` 仍为项目文档。
- **基础设施**：多频道多管理员、中英双语界面、配置热重载与原子回滚、独立问答进程、优雅关闭

完整命令列表见[命令参考](wiki/User-Commands.md)，各功能的使用细节见[用户文档](wiki/User-Guide.md)。

## 快速开始

从 GitHub Release 下载自制 ZIP 或 tar.gz 的用户，请按[发布附件安装与更新](docs/release-archives.md)部署。

需要 **Docker Engine 和 Compose v2**，或 **Go 1.27+ 和 Node.js 22+**。请准备 Telegram Bot Token、Telegram API 凭证、OpenAI 兼容服务凭证和 MySQL 数据库。[获取凭证及配置说明](wiki/User-Getting-Started.md)

```bash
git clone https://github.com/Sakura520222/Sakura-Bot.git
cd Sakura-Bot
cp data/.env.example data/.env   # 编辑 Telegram、MySQL 和 AI 配置
bash scripts/deploy.sh
```

GitHub Actions 发布包含主 Bot、助手 Bot 和 WebUI 的完整镜像。部署脚本快进更新 Git 工作区，拉取对应提交的镜像并重建容器。容器持久化 `data/` 和 `logs/`。[部署、升级及回滚](docs/go-deployment.md)

在 Linux 或 macOS 本地开发时运行 `bash start.sh`，Windows 运行 `start.bat`。脚本会构建两个 Go 程序和 WebUI，然后启动主 Bot。若启用了 UserBot 且尚无授权会话，按部署手册完成首次登录。

## 目录结构

```text
cmd/sakura-main/   主 Bot 入口
cmd/sakura-qa/     助手 Bot 入口
internal/          业务领域与共享基础设施
web/               Vue 3、Vite 和 Naive UI
data/              运行配置、提示词、会话和向量库
wiki/              项目文档站
```

主 Bot 使用 gotd/td 处理 MTProto，与助手共用 Go Telegram Bot API 客户端。应用数据保存在 MySQL，向量索引使用 SQLite WAL 和 float32。两个进程使用同一套 OpenAI 兼容客户端、Embedding、Reranker、配置、日志和媒体处理实现。

## 文档

文档站：<https://sakura-bot.pyabunny.com/>（源文件在 [wiki/](wiki/Home.md)，推送 `main` 后自动发布）。

- 用户：[快速开始](wiki/User-Getting-Started.md)、[命令参考](wiki/User-Commands.md)、[WebUI 管理面板](wiki/User-WebUI-Guide.md)、[Sakura Guard 智能群管](wiki/User-Sakura-Guard.md)、[问答 Bot](wiki/User-QA-Bot-Guide.md)、[RAG 快速启动](wiki/User-RAG-Quickstart.md)
- 开发：[开发快速开始](wiki/Developer-Getting-Started.md)、[系统架构](wiki/Developer-Architecture.md)、[文档站维护](wiki/Developer-Documentation-Site.md)

## 贡献

欢迎 Issue 和 PR。PR 请提交到 `main` 分支，提交信息遵循 [Conventional Commits](https://www.conventionalcommits.org/)，Go 代码使用 gofmt。详见[贡献指南](CONTRIBUTING.md)和[行为准则](wiki/Code-of-Conduct.md)。

## 许可证

[AGPL-3.0](LICENSE)。修改和网络服务形式的使用需以相同许可证提供源代码，衍生作品请保留本仓库来源：https://github.com/Sakura520222/Sakura-Bot 。本项目仅供个人学习使用，API 费用与合规责任由使用者自行承担。

## 致谢

- [gotd/td](https://github.com/gotd/td) - Telegram MTProto 客户端
- [go-telegram/bot](https://github.com/go-telegram/bot) - Telegram Bot API 库
- [DeepSeek](https://www.deepseek.com/) / [OpenAI](https://openai.com/) - 大模型 API
- [SiliconFlow](https://siliconflow.cn/) - Embedding 与 Reranker API
- 所有[贡献者](https://github.com/Sakura520222/Sakura-Bot/graphs/contributors)

## 支持

- 问题反馈：[GitHub Issues](https://github.com/Sakura520222/Sakura-Bot/issues)
- 讨论交流：[GitHub Discussions](https://github.com/Sakura520222/Sakura-Bot/discussions)
- 邮件：[sakura520222@outlook.com](mailto:sakura520222@outlook.com)
