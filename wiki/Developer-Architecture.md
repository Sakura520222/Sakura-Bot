# 🏗️ 系统架构设计

本文档详细介绍 Sakura-Bot 的系统架构、模块设计和核心技术。

> **分类**: 开发者文档  
> **类型**: 参考资料  
> **难度**: ⭐⭐  
> **更新时间**: 2026-10-31

[← 返回开发者文档](Developer-Guide.md) | [Wiki 首页](Home.md)

---

## 📐 整体架构

### 系统概览

Sakura-Bot 采用**模块化微服务架构**，将功能拆分为多个独立模块，提高可维护性和可扩展性。

```
┌─────────────────────────────────────────────────────────────┐
│                         用户层                               │
│  Telegram 频道用户 · 管理员 · 问答Bot用户 · WebUI 管理员     │
└────────────────────┬────────────────────────────────────────┘
                     │
┌────────────────────┴────────────────────────────────────────┐
│                      Bot 层                                  │
│  ┌──────────────┐              ┌──────────────┐            │
│  │  主Bot       │              │  问答Bot     │            │
│  │ (main.py)   │              │ (Go/Python) │            │
│  │             │              │              │            │
│  │ - 频道监控   │              │ - 自然对话   │            │
│  │ - 消息总结   │              │ - 语义检索   │            │
│  │ - 投票功能   │              │ - 配额管理   │            │
│  └──────────────┘              └──────────────┘            │
│  ┌──────────────┐                                          │
│  │  WebUI API   │  (FastAPI + Vue.js)                      │
│  │ - 仪表盘统计  │                                          │
│  │ - 频道管理    │                                          │
│  │ - 总结触发    │                                          │
│  │ - 转发管理    │                                          │
│  │ - 数据库管理  │                                          │
│  └──────────────┘                                          │
└────────────────────┬────────────────────────────────────────┘
                     │
┌────────────────────┴────────────────────────────────────────┐
│                     核心层 (core/)                           │
│  ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌──────────┐      │
│  │ AI客户端 │ │ 数据库   │ │ 向量存储 │ │ 配置管理 │      │
│  └──────────┘ └──────────┘ └──────────┘ └──────────┘      │
│  ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌──────────┐      │
│  │会话管理  │ │ 意图解析 │ │ 记忆管理 │ │ 问答引擎 │      │
│  └──────────┘ └──────────┘ └──────────┘ └──────────┘      │
└────────────────────┬────────────────────────────────────────┘
                     │
┌────────────────────┴────────────────────────────────────────┐
│                     数据层                                   │
│  MySQL · 向量库（SQLite/ChromaDB）· 配置文件 · 会话文件     │
└─────────────────────────────────────────────────────────────┘
```

**问答 Bot 双运行时**：问答 Bot 缺省运行 Go 编译的静态二进制（`qa-bot-go/`，稳态内存约 17-20MB，Python 版约 137MB，均为 v2.2.0 生产实测），由主进程 `core/system/process_manager.py` 作为子进程管理；Go 二进制缺失时自动回退 Python 版 `qa_bot.py`。运行时解析优先级为 `config.json` 的 `qa_bot_runtime` > `QA_BOT_RUNTIME` 环境变量 > 缺省 `go`，管理员可通过 `/qa_runtime` 命令或 WebUI「系统」页热切换并持久化。

---

## 🎯 设计原则

### 1. 模块化设计

- **单一职责**: 每个模块只负责一个功能域
- **松耦合**: 模块间通过清晰接口通信
- **高内聚**: 相关功能组织在一起

### 2. 异步优先

- 使用 `async/await` 处理 I/O 操作
- 支持 Telegram 并发请求
- 提高系统响应速度

### 3. 错误处理

- 自定义异常类
- 智能重试机制
- 优雅降级策略

### 4. 可扩展性

- 插件化命令处理
- 可配置的 AI 提供商
- 灵活的存储后端

---

## 📦 核心模块

### 1. AI 模块 (`core/ai/`)

**职责**: 封装 AI API 调用，支持多种 AI 提供商。

**核心功能**:
- LLM 推理（总结生成、问答）
- Embedding 生成（向量化）
- Reranker 调用（结果重排）
- 错误处理和重试

**关键 API**:
```python
# core/ai/ai_client.py —— 模块级异步函数
async def analyze_with_ai(messages, current_prompt) -> str
async def generate_poll_from_summary(summary_text) -> str

# core/ai/embedding_generator.py
class EmbeddingGenerator:
    def generate(self, text: str) -> list[float] | None
    def batch_generate(self, texts: list[str]) -> list[list[float] | None]

# core/ai/reranker.py
class Reranker:
    def rerank(self, query: str, candidates: list[dict], top_k: int | None = None) -> list[dict]
```

AI 客户端支持热重载：`get_ai_runtime()` / `get_async_ai_runtime()` 在每次请求时按 `effective_ai_config()` 校验连接信息，配置变更后自动重新初始化客户端。

**支持的 AI 提供商**:
- OpenAI (GPT-4, GPT-3.5)
- DeepSeek (deepseek-chat)
- SiliconFlow (Embedding, Reranker)
- 任何 OpenAI 兼容的 API

### 2. 数据库模块 (`core/infrastructure/database/`)

**职责**: 管理所有数据持久化操作。

**数据表**（仅列主要表）:

| 表名 | 用途 |
|------|------|
| `summaries` | 存储频道总结记录 |
| `users` / `subscriptions` | 用户与订阅 |
| `usage_quota` | 问答Bot配额管理 |
| `channel_profiles` | 频道画像数据 |
| `conversation_history` | 多轮对话历史 |
| `poll_regenerations` | 投票重新生成记录 |
| `poll_voters` | 投票者记录 |
| `forwarded_messages` / `forwarding_stats` | 转发记录与统计信息 |
| `request_queue` / `notification_queue` | 主 Bot 与 QA Bot 跨进程通信队列 |
| `submissions` | 投稿审核记录 |
| `system_audit_logs` | 系统操作审计日志 |
| `db_version` | 数据库迁移版本 |

此外，Sakura Guard 群管与封禁申诉使用独立的 `moderation_*` 系列表（案件、黑白名单、验证会话、通知及五张申诉表等，定义见 `core/moderation/schema.sql`）。

**支持的数据库**:
- MySQL（当前唯一支持的关系数据库）

**关键特性**:
- aiomysql 连接池管理
- 自动初始化和迁移
- 表名/列名白名单校验
- WebUI 数据库管理接口支持分页查询和受控写操作
- 主 Bot 与 QA Bot 通过 MySQL 队列进行跨进程通信

### 3. 向量存储模块 (`core/ai/vector_store.py`)

**职责**: 双后端向量存储门面，实现语义检索。通过环境变量 `VECTOR_BACKEND` 选择后端：

- **`chroma`**（代码缺省）: ChromaDB 持久化向量库，`chromadb` 延迟到后端初始化时才导入
- **`lite`**: SQLite(WAL) 单文件 + numpy 内存矩阵暴力余弦（实现见 `core/ai/vector_lite.py`），数据存于 `store_meta` / `summaries_vectors` / `messages_vectors` 表；lite 模式绝不加载 chromadb

两个后端接口逐字段兼容（`VectorStore` 类、搜索结果形状、`collection`/`messages_collection` 属性等），WebUI 与调用方零改动。v2.2.0 引入 lite 后端用于替换进程内 ChromaDB 以降低内存占用（生产环境已启用，回滚可切回 chroma）。

**核心功能**:
- 添加总结/消息向量
- 语义搜索（精确元数据过滤，日期按 UTC 归一化）
- 向量更新和删除
- 纯读者模式：QA 进程设置 `VECTOR_BACKEND_READER=1`，lite 后端以只读连接打开、绝不执行 DDL/写事务，库缺失时降级为不可用

**使用的技术**:
- **向量库**: ChromaDB 或 SQLite+numpy（按 `VECTOR_BACKEND` 选择）
- **BGE 模型**: 中文 Embedding (bge-m3, bge-large-zh-v1.5)
- **余弦相似度**: 相似度计算

**工作流程**:
```
总结生成 → 生成 Embedding → 存储到向量库（按 VECTOR_BACKEND 选择后端）
用户查询 → 生成查询向量 → 语义检索 → 返回 Top-K 结果
```

### 4. 会话管理模块 (`core/ai/conversation_manager.py`)

**职责**: 管理问答Bot的多轮对话上下文。

**核心功能**:
- 会话创建和超时管理
- 对话历史存储
- 上下文格式化
- 会话清理

**会话生命周期**:
```
用户首次提问 → 创建新会话
30分钟内新消息 → 继续当前会话
超过30分钟 → 创建新会话
用户发送 /clear → 手动清除会话
```

### 5. 意图解析模块 (`core/ai/intent_parser.py`)

**职责**: 从用户自然语言查询中提取检索辅助信息（频道提示、时间范围、关键词），不做硬编码意图拦截。

**识别能力**:
- **时间范围**: 今天、昨天、本周、上周、最近N天
- **关键词**: 智能提取，不依赖硬编码词典
- **主题**: 主题词会追加到关键词中
- **频道提示**: @用户名、t.me 链接或「在 xxx 频道」等自然语言片段

**返回结果**:
```python
{
    "intent": "summary",  # 意图类型: summary / topic / keyword
    "time_range": 7,  # 时间范围（天），None 表示不限制
    "keywords": ["AI", "GPT"],  # 关键词
    "channel_id": None,  # 频道ID
    "channel_hint": "@mychannel",  # 用户提到的频道提示
    "original_query": "最近7天 AI 相关的总结",  # 原始查询
    "confidence": 0.8,  # 置信度
}
```

> 注：问答 v3 引擎的流式路径已改为 agentic 工具调用，本模块作为检索辅助信息提取实现保留；系统状态类查询请使用显式命令（如 `/status`）。

### 6. 记忆管理模块 (`core/ai/memory_manager.py`)

**职责**: 管理频道画像和记忆数据。

**核心功能**:
- 频道画像生成和更新
- 关键词提取
- 主题分类
- 智能搜索

**频道画像维度**:
- **风格**: tech/casual/neutral
- **主题**: 常讨论的主题列表
- **关键词频率**: 关键词出现频率
- **情感倾向**: positive/neutral/negative
- **统计信息**: 平均消息长度、总结总数

### 7. 问答引擎模块 (`core/ai/qa_engine_v3.py`)

**职责**: 实现 RAG（检索增强生成）问答流程，流式输出。

**工作流程**:
```
用户查询
  ↓
获取/创建会话，加载对话历史
  ↓
Agentic 工具调用循环（优先路径）
  │  模型自主决定是否检索及调用哪些工具：
  │  semantic_search / keyword_search / rerank_results /
  │  resolve_channel / get_recent_summaries 等多轮迭代
  ↓ 失败时降级
无工具直连对话（保留普通闲聊能力，不强制进入检索流水线）
  ↓
流式返回回答
```

**核心机制**:
- **Agentic 工具调用**: 每轮流式请求同时接收文本增量与 tool_call 增量，模型按需检索；工具内部使用向量语义检索、关键词检索与可选的 Reranker 精排
- **Reranker**: BGE-reranker-v2-m3 精排（以 `rerank_results` 工具形式提供，未配置 API Key 时自动不可用）
- **降级直连**: agentic 路径异常时回退为无工具的普通对话，避免把日常闲聊误导向频道检索

### 8. 重排序模块 (`core/ai/reranker.py`)

**职责**: 使用 Reranker 模型对检索结果精排。

**工作原理**:
```
输入: 查询 + 20个候选文档
  ↓
Reranker 模型计算相关性得分
  ↓
按得分排序
  ↓
输出: Top-5 最相关文档
```

**使用模型**:
- BGE-reranker-v2-m3 (中文优化)
- 通过交叉注意力计算相关性

### 9. Telegram 与 UserBot 模块 (`core/telegram/`, `core/handlers/`)

**职责**: 封装 Telegram MTProto API 操作。

**核心功能**:
- 用户客户端（频道消息抓取）
- Bot 客户端（命令处理）
- 消息发送
- 频道管理

### 10. 命令处理模块 (`core/commands/`)

**职责**: 处理所有 Telegram Bot 命令。

**命令分类**:
- **AI 配置命令**: 提示词、AI 配置
- **频道命令**: 添加/删除/查看频道
- **调度命令**: 设置自动总结时间
- **投票命令**: 投票配置
- **系统控制**: 暂停/恢复/重启
- **历史记录**: 查看/导出/统计
- **转发管理**: 规则、过滤、底栏和统计
- **UserBot 管理**: 状态、加入、离开和频道列表

**实现方式**:
- 命令在 `core/initializers/command_registrar.py` 中通过 `client.add_event_handler(...)` 集中注册（命令模块本身不使用注册装饰器）
- 新增命令通常需同时更新注册器与 `core/bot_commands.py` 菜单元数据
- 支持中英文别名
- 状态管理（多步骤交互）
- Main Bot 欢迎和帮助消息提供内联键盘入口，复用既有命令处理器

### 11. WebUI API 模块 (`core/web_api/` + `web/`)

**职责**: 提供浏览器端管理面板和 REST API。

**后端能力**:
- FastAPI 路由与中间件，按功能域拆分路由：认证、仪表盘、频道、调度、转发、互动、群管（guard）、AI 配置、命令中心、总结、UserBot、系统、统计、向量存储和数据库管理
- JWT 认证与管理员权限校验
- 配置保存后显式触发热重载

**前端能力**:
- Vue 3 + Vite + TypeScript + Naive UI
- v2.2.1 起全页面（14 个路由页 + 登录页 + 布局）按平面设计系统重建（`--sb-*` 令牌：浅色对齐 Google 控制台、深色对齐 X），侧栏菜单按信息架构分组，统一「页头 + 工具栏 + 数据区」骨架
- 异步三态（骨架屏 / 空态引导 / 错误重试）与危险操作三层确认
- 深色/浅色主题持久化
- 转发规则可视化编辑和底栏占位符插入
- 数据库表分页浏览和受控维护操作
- 「系统」页支持问答 Bot 运行时（Go/Python）热切换

**部署方式**:
- Docker 多阶段构建（Node 构建 `web/` 前端、Go 构建 `qa-bot-go/` 二进制、Python 运行镜像）；GitHub Actions 将「依赖镜像」（Python 依赖 + Go 二进制 + 前端产物）推送 GHCR，`docker-compose.yml` 缺省拉取 `ghcr.io/sakura520222/sakura-bot:main`，源码仍整体挂载
- `/update` 命令更新代码时若检测到镜像层变更（`qa-bot-go/`、`web/`、`Dockerfile`、`docker-compose.yml`、`requirements.txt`、构建工作流），会写入更新请求标记，由宿主机 deployer（`deploy/install-deployer.sh` 一次性安装的 systemd 单元）自动拉取新镜像并重建容器；未安装 deployer 时优雅退回容器内自愈路径（pip/npm 重建）
- 本地开发可在 `web/` 中运行 Vite 开发服务，并代理到后端 API；fork/本地源码部署使用 `docker compose build` 构建

---

## 🔄 数据流

### 总结生成流程

```
定时任务触发
  ↓
获取频道消息
  ↓
AI 生成总结
  ↓
生成 Embedding
  ↓
存储到数据库
  ↓
存储向量到向量库（按 VECTOR_BACKEND 选择后端）
  ↓
发送到管理员/频道
  ↓
生成投票（可选）
  ↓
完成
```

### 问答流程

```
用户发送问题
  ↓
配额检查
  ↓
Agentic 工具调用循环（模型自主决定检索与重排；失败时降级为无工具直连对话）
  ↓
AI 流式生成回答
  ↓
更新配额
  ↓
返回回答
```

---

## 🔐 安全设计

### 1. 数据隔离

- 不同用户的对话历史完全隔离
- 不同频道的配置独立存储
- 管理员权限验证

### 2. 配额管理

- 用户级配额（每日限制）
- 系统总配额（防滥用）
- 管理员豁免
- 自动重置机制

### 3. 错误处理

- 自定义异常类
- 详细错误日志
- 用户友好的错误消息
- 降级策略

---

## 🚀 性能优化

### 1. 数据库优化

- **MySQL 连接池**: 减少连接开销并提升并发能力
- **索引优化**: 关键字段建立索引
- **分页查询**: WebUI 和统计接口使用分页/偏移量避免一次加载大量数据
- **行数统计优化**: 数据库管理页面复用优化后的表行数查询
- **查询缓存**: 热点数据缓存

### 2. AI 调用优化

- **提示词优化**: 精简高效
- **温度控制**: 平衡创造性和准确性
- **重试机制**: 自动重试临时错误
- **降级方案**: AI 失败时的备选方案

### 3. 并发处理

- **异步 I/O**: 所有 I/O 操作异步化
- **任务队列**: 后台任务处理
- **进程隔离**: 问答Bot为独立子进程，缺省运行 Go 版静态二进制（`qa-bot-go/`），可热切换为 Python 版 `qa_bot.py`

---

## 📊 监控与日志

### 日志级别

- **DEBUG**: 详细调试信息
- **INFO**: 一般信息
- **WARNING**: 警告信息
- **ERROR**: 错误信息
- **CRITICAL**: 严重错误

### 日志位置

- **应用日志**: `logs/sakura-bot.log`
- **QA Bot 日志**: `logs/qa-bot.log`

错误信息按级别写入上述应用日志，没有独立的错误日志文件。

### 监控指标

- 总结生成成功率
- AI 调用延迟
- 查询响应时间
- 配额使用情况

---

## 🔧 配置管理

### 环境变量 (`data/.env`)

主要配置项：
- Telegram 凭证
- AI API 配置
- 数据库配置
- 日志级别
- 向量后端（`VECTOR_BACKEND`，缺省 `chroma`）
- 问答 Bot 运行时（`QA_BOT_RUNTIME`，缺省 `go`，`config.json` 优先）
- 功能开关

### 配置文件 (`data/config.json`)

运行时配置：
- 频道列表
- 投票配置
- 提示词配置
- 问答Bot人格

---

## 🧪 测试策略

### 单元测试

- 每个模块独立测试
- 使用 pytest fixtures
- Mock 外部依赖
- 覆盖率目标与当前进展见[技术债务追踪](Developer-Technical-Debt.md)

### 集成测试

- 模块间交互测试
- 数据库集成测试
- AI API 集成测试

### 端到端测试

- 完整工作流测试
- 用户场景测试

---

## 📚 相关文档

- **[模块拆分总结](Developer-Module-Split-Summary.md)** - 代码模块化详情
- **[错误处理增强](Developer-Error-Handling-Improvements.md)** - 错误处理机制
- **[RAG 技术文档](Developer-RAG-Feature-V3.md)** - RAG 实现细节
- **[问答 Bot 实施](Developer-QA-Bot-Implementation.md)** - AI 功能实施

---

## 🔗 相关资源

- **[开发者文档](Developer-Guide.md)** - 开发者指南
- **[贡献指南](../CONTRIBUTING.md)** - 如何贡献
- **[GitHub Issues](https://github.com/Sakura520222/Sakura-Bot/issues)** - 问题跟踪

---

<div align="center">

**🌸 Sakura-Bot 架构设计**

模块化 · 可扩展 · 高性能

</div>