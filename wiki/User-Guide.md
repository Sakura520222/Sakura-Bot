# 👥 用户文档

欢迎来到 Sakura-Bot 用户文档中心！这里提供了完整的使用指南，帮助您快速上手并充分利用 Sakura-Bot 的各项功能。

[← 返回 Wiki 首页](Home.md)

---

## 📚 文档目录

### 🚀 快速入门

| 文档 | 说明 | 难度 |
|------|------|------|
| **[快速开始](User-Getting-Started.md)** | 5 分钟快速部署 Sakura-Bot | ⭐ |
| **[命令参考](User-Commands.md)** | 主 Bot 与问答 Bot 全部命令 | ⭐ |
| **[RAG 快速启动](User-RAG-Quickstart.md)** | 启用语义检索功能 | ⭐ |

### 🤖 AI 功能

| 文档 | 说明 | 难度 |
|------|------|------|
| **[问答 Bot 指南](User-QA-Bot-Guide.md)** | 智能问答功能完整指南 | ⭐⭐ |
| **[多轮对话功能](User-Multi-Turn-Conversation.md)** | 上下文感知对话说明 | ⭐⭐ |

### ⚙️ 功能配置

| 文档 | 说明 | 难度 |
|------|------|------|
| **[投票功能](User-Poll-Feature.md)** | 频道投票配置指南 | ⭐⭐ |
| **[WebUI 管理面板](User-WebUI-Guide.md)** | Web 管理界面使用指南 | ⭐⭐ |
| **[Sakura Guard 智能群管](User-Sakura-Guard.md)** | 群聊规则、入群验证、黑白名单与封禁申诉 | ⭐⭐ |

### 📜 社区规范

| 文档 | 说明 | 难度 |
|------|------|------|
| **[行为准则](Code-of-Conduct.md)** | 社区贡献行为准则 | ⭐ |

---

## 🎯 按使用场景查找

### 我是新用户

推荐阅读顺序：

1. 📖 [快速开始](User-Getting-Started.md) - 快速部署 Bot
2. 🤖 [问答 Bot 指南](User-QA-Bot-Guide.md) - 了解智能问答功能
3. 📊 [多轮对话功能](User-Multi-Turn-Conversation.md) - 使用上下文对话

### 我想启用 AI 功能

推荐阅读顺序：

1. 🚀 [RAG 快速启动](User-RAG-Quickstart.md) - 配置语义检索
2. 🤖 [问答 Bot 指南](User-QA-Bot-Guide.md) - 学习使用方法
3. 🧠 [多轮对话功能](User-Multi-Turn-Conversation.md) - 启用对话记忆

### 我想配置投票功能

推荐阅读：

- ⚙️ [投票功能](User-Poll-Feature.md) - 完整配置指南

### 我想参与社区

推荐阅读：

- 📜 [行为准则](Code-of-Conduct.md) - 了解社区规范

---

## 📖 功能特性概览

Sakura-Bot 提供以下核心功能：

### 🤖 智能问答

- 自然语言查询频道历史
- 语义检索和智能推荐
- 多轮对话上下文记忆
- 使用配额管理

> v2.2.0 起问答 Bot 缺省以轻量 Go 运行时运行，管理员可通过 `/qa_runtime` 命令或 WebUI「系统」页在线切换，详见 [命令参考](User-Commands.md)。

### 📊 自动总结

- 定时抓取频道消息
- AI 生成智能总结
- 支持手动触发总结
- 多频道独立配置

### 🗳️ 投票功能

- AI 生成投票选项
- 频道/讨论组模式
- 投票重新生成
- 按频道独立配置

### 🔍 RAG 语义检索

- 向量数据库支持
- 混合检索算法
- 智能重排序

### 🌐 WebUI 管理面板

- 可视化仪表盘统计
- 频道管理与调度配置
- 在线总结触发
- 转发规则管理与底栏模板配置
- MySQL 数据库浏览、分页查询与受控维护
- 配置保存后主动热重载，减少重启需求
- JWT 安全认证
- v2.2.1 起按平面设计系统全面重构：深色/浅色双主题、侧栏菜单按信息架构分 5 组、统一「页头 + 工具栏 + 数据区」页面骨架、异步加载三态（骨架屏/空态/错误重试）与危险操作多层确认

### 🚀 一键更新与自动部署

- 管理员发送 `/update`（别名 `/更新`）即可在线更新：自动比对 GitHub Release 最新版本并拉取代码
- 检测到镜像层变更（Go 源码、Dockerfile、依赖清单、前端、构建工作流）时，自动请求宿主机部署器拉取新镜像并重建容器，全程无需登录服务器
- 未安装宿主机部署器时优雅退回容器内依赖安装与前端构建的自愈路径，更新完成后自动重启

### 🧭 主 Bot 内联菜单

- `/start` 和 `/help` 提供按钮式入口
- 快速访问频道、转发、系统和问答 Bot 管理功能
- 适合移动端管理员日常操作

---

## 💡 常见问题

### 如何开始使用？

1. 阅读 [快速开始](User-Getting-Started.md) 部署 Bot
2. 配置必要的环境变量
3. 启动 Bot 并发送 `/start` 命令

### 如何启用 AI 功能？

参考 [RAG 快速启动](User-RAG-Quickstart.md) 配置 API 密钥和模型。

### 如何使用问答功能？

向问答 Bot 发送自然语言问题，如"频道上周发生了什么？"

### 如何配置投票？

参考 [投票功能](User-Poll-Feature.md) 文档进行配置。

### 如何更新 Bot？

管理员向主 Bot 发送 `/update` 即可在线更新；检测到依赖镜像层变更时会自动请求宿主机部署器拉取新镜像并重建容器，未安装部署器时自动走容器内自愈路径。

### 如何使用 WebUI 管理数据库或转发？

参考 [WebUI 管理面板](User-WebUI-Guide.md)。数据库管理和系统操作属于高危功能，请仅在可信网络和管理员账号下使用。

### 被群管封禁了，如何申诉？

私聊问答 Bot 发送 `/appeal` 或点击「🚫 封禁申诉」按钮提交申诉，完整流程见 [Sakura Guard 智能群管 › 自助封禁申诉](User-Sakura-Guard.md#自助封禁申诉)。

---

## 🔗 相关资源

- **[Wiki 首页](Home.md)** - 返回文档中心
- **[开发者文档](Developer-Guide.md)** - 开发者相关文档
- **[项目 README](../README.md)** - 项目概述
- **[GitHub Issues](https://github.com/Sakura520222/Sakura-Bot/issues)** - 问题反馈

---

## 💬 获取帮助

如果您在使用过程中遇到问题：

1. 📖 查阅上述文档
2. 🔍 搜索 [GitHub Issues](https://github.com/Sakura520222/Sakura-Bot/issues)
3. 💬 参与 [GitHub Discussions](https://github.com/Sakura520222/Sakura-Bot/discussions)
4. 📧 发送邮件至 [sakura520222@outlook.com](mailto:sakura520222@outlook.com)

---

<div align="center">

**🌸 Sakura-Bot 用户文档**

让频道管理更智能 · 让信息查询更便捷

[⭐ Star](https://github.com/Sakura520222/Sakura-Bot) · [🍴 Fork](https://github.com/Sakura520222/Sakura-Bot/fork)

</div>
