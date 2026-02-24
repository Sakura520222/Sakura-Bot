# 👨‍💻 开发者文档

欢迎来到 Sakura-Bot 开发者文档中心！这里提供了完整的技术文档，帮助开发者了解系统架构、参与贡献代码。

[← 返回 Wiki 首页](Home.md)

---

## 📚 文档目录

### 🚀 开发入门

| 文档 | 说明 | 难度 |
|------|------|------|
| **[开发快速开始](Developer-Getting-Started.md)** | 开发环境配置和本地运行 | ⭐ |
| **[架构设计](Developer-Architecture.md)** | 系统架构和模块设计 | ⭐⭐ |

### 🔧 代码质量

| 文档 | 说明 | 难度 |
|------|------|------|
| **[Ruff 脚本指南](Developer-Ruff-Script-Guide.md)** | 代码检查和格式化工具 | ⭐ |

### 🏗️ 架构与重构

| 文档 | 说明 | 难度 |
|------|------|------|
| **[模块拆分总结](Developer-Module-Split-Summary.md)** | 代码模块化重构 | ⭐⭐ |
| **[代码重构总结](Developer-Refactoring-Summary.md)** | 重构改进说明 | ⭐⭐ |
| **[错误处理增强](Developer-Error-Handling-Improvements.md)** | 错误处理机制 | ⭐⭐ |

### 🤖 AI 功能实现

| 文档 | 说明 | 难度 |
|------|------|------|
| **[问答 Bot 实施](Developer-QA-Bot-Implementation.md)** | AI 功能实施文档 | ⭐⭐⭐ |
| **[RAG 技术文档](Developer-RAG-Feature-V3.md)** | RAG 技术实现详解 | ⭐⭐⭐ |

### 🚀 部署与运维

| 文档 | 说明 | 难度 |
|------|------|------|
| **[CI/CD 工作流](Developer-Workflow-Improvements.md)** | 持续集成配置 | ⭐⭐ |

---

## 🎯 按开发场景查找

### 我是新贡献者

推荐阅读顺序：

1. 📖 [开发快速开始](Developer-Getting-Started.md) - 配置开发环境
2. 🏗️ [架构设计](Developer-Architecture.md) - 了解系统架构
3. 🔧 [Ruff 脚本指南](Developer-Ruff-Script-Guide.md) - 学习代码规范

### 我想了解 AI 实现

推荐阅读顺序：

1. 🤖 [问答 Bot 实施](Developer-QA-Bot-Implementation.md) - 整体架构
2. 🔍 [RAG 技术文档](Developer-RAG-Feature-V3.md) - 技术细节
3. 🏗️ [架构设计](Developer-Architecture.md) - 系统设计

### 我想参与代码重构

推荐阅读：

1. 🏗️ [模块拆分总结](Developer-Module-Split-Summary.md) - 模块化设计
2. 🔧 [代码重构总结](Developer-Refactoring-Summary.md) - 重构原则
3. 🛡️ [错误处理增强](Developer-Error-Handling-Improvements.md) - 错误处理

### 我想配置 CI/CD

推荐阅读：

- 🚀 [CI/CD 工作流](Developer-Workflow-Improvements.md) - 工作流配置

---

## 📖 技术栈概览

Sakura-Bot 使用以下技术栈：

### 核心框架

- **Python 3.11+** - 主要编程语言
- **Telethon** - Telegram MTProto API 客户端
- **python-telegram-bot** - Telegram Bot API 封装

### 数据存储

- **SQLite** - 轻量级关系数据库（WAL 模式）
- **aiosqlite** - 异步 SQLite 操作
- **ChromaDB** - 向量数据库（RAG 功能）

### AI/ML

- **OpenAI API** - LLM 推理
- **SiliconFlow** - Embedding 和 Reranker
- **BGE 模型** - 中文语义理解

### 开发工具

- **Ruff** - 代码检查和格式化
- **pytest** - 单元测试
- **pre-commit** - Git 钩子
- **Docker** - 容器化部署

### CI/CD

- **GitHub Actions** - 持续集成
- **Docker Compose** - 多容器编排

---

## 🏗️ 项目结构

```
Sakura-Bot/
├── core/                    # 核心模块
│   ├── ai_client.py        # AI 客户端
│   ├── config.py           # 配置管理
│   ├── database.py         # 数据库操作
│   ├── conversation_manager.py  # 会话管理
│   ├── intent_parser.py    # 意图解析
│   ├── memory_manager.py   # 记忆管理
│   ├── qa_engine_v3.py     # 问答引擎
│   ├── vector_store.py     # 向量存储
│   ├── reranker.py         # 重排序
│   └── telegram/           # Telegram 相关
│       ├── client.py       # 客户端
│       ├── messaging.py    # 消息处理
│       └── poll_handlers.py # 投票处理
├── tests/                   # 测试代码
├── data/                    # 数据文件
├── wiki/                    # 项目文档
├── main.py                  # 主程序入口
├── qa_bot.py               # 问答 Bot 入口
└── requirements.txt        # 依赖列表
```

---

## 🧪 测试

运行测试：

```bash
# 运行所有测试
pytest

# 运行特定测试
pytest tests/test_ai_client.py

# 查看覆盖率
pytest --cov=core --cov-report=html
```

测试覆盖率目标：**≥ 90%**

---

## 🔍 代码规范

### 使用 Ruff

```bash
# 检查代码
python run_ruff.py

# 自动修复
python run_ruff.py --fix

# 格式化代码
python run_ruff.py --format

# 一键完成
python run_ruff.py --all
```

### Pre-commit 钩子

```bash
# 安装钩子
pre-commit install

# 手动运行
pre-commit run --all-files
```

---

## 🤝 贡献流程

1. **Fork 项目** - 创建您的分支
2. **克隆代码** - `git clone https://github.com/YOUR_USERNAME/Sakura-Bot.git`
3. **创建分支** - `git checkout -b feature/your-feature`
4. **编写代码** - 遵循代码规范
5. **运行测试** - 确保测试通过
6. **提交代码** - `git commit -m "feat: 添加功能"`
7. **推送分支** - `git push origin feature/your-feature`
8. **创建 PR** - 在 GitHub 上创建 Pull Request

详细指南请参考 [CONTRIBUTING.md](../CONTRIBUTING.md)

---

## 📝 开发最佳实践

### 1. 代码风格

- 遵循 PEP 8 规范
- 使用类型提示
- 添加文档字符串（Google 风格）
- 保持函数简洁（< 50 行）

### 2. 错误处理

- 使用自定义异常类
- 记录详细错误信息
- 提供有意义的错误消息
- 使用重试机制处理临时错误

### 3. 测试

- 为新功能编写单元测试
- 测试覆盖率 ≥ 90%
- 使用 pytest fixtures
- 模拟外部依赖

### 4. 文档

- 更新相关文档
- 添加代码注释
- 更新 CHANGELOG.md
- 保持文档同步

---

## 🔗 相关资源

- **[Wiki 首页](Home.md)** - 返回文档中心
- **[用户文档](User-Guide.md)** - 用户相关文档
- **[项目 README](../README.md)** - 项目概述
- **[贡献指南](../CONTRIBUTING.md)** - 如何贡献
- **[GitHub Issues](https://github.com/Sakura520222/Sakura-Bot/issues)** - 问题跟踪

---

## 💬 获取帮助

如果您在开发过程中遇到问题：

1. 📖 查阅上述文档
2. 🔍 搜索 [GitHub Issues](https://github.com/Sakura520222/Sakura-Bot/issues)
3. 💬 参与 [GitHub Discussions](https://github.com/Sakura520222/Sakura-Bot/discussions)
4. 📧 发送邮件至 [sakura520222@outlook.com](mailto:sakura520222@outlook.com)

---

<div align="center">

**🌸 Sakura-Bot 开发者文档**

共建智能频道管理系统

[⭐ Star](https://github.com/Sakura520222/Sakura-Bot) · [🍴 Fork](https://github.com/Sakura520222/Sakura-Bot/fork)

</div>