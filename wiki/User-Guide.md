# 👥 用户文档

欢迎来到 Sakura-Bot 用户文档中心！这里提供了完整的使用指南，帮助您快速上手并充分利用 Sakura-Bot 的各项功能。

[← 返回 Wiki 首页](Home.md)

---

## 📚 文档目录

### 🚀 快速入门

| 文档 | 说明 | 难度 |
|------|------|------|
| **[快速开始](User-Getting-Started.md)** | 5 分钟快速部署 Sakura-Bot | ⭐ |
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
- 深色/浅色主题切换
- JWT 安全认证
- 高准确率回答

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
