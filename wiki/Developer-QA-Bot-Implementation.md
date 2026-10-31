# AI能力增强 - 实施总结

## 📋 实施概述

本次更新实现了基于历史总结的智能问答系统，允许用户通过自然语言查询频道历史内容。

**实施日期**: 2026年2月14日
**版本**: v2.0.0
**状态**: ✅ 已完成

---

## 🎯 实现目标

### 核心功能

✅ **上下文记忆** - 记住频道特点，生成更精准的总结
✅ **基于历史总结的问答系统** - 智能检索和回答
✅ **自然语言查询** - 支持"询问频道上周发生了什么？"等自然问题
✅ **频道画像** - 自动提取和存储频道特征
✅ **配额管理** - 防止滥用的使用限制

---

## 🏗️ 技术架构

### 系统设计

采用**独立Bot架构**，问答Bot与主Bot分离，共享数据库：

```
┌─────────────────────────────────────────────────────┐
│  主Bot（main.py）                                     │
│  - 频道监控和总结                                      │
│  - 投票功能                                          │
│  - 管理员命令                                         │
└──────────┬──────────────────────────────────────────┘
           │
           │ 共享数据库（MySQL）
           │   - summaries（总结记录）
           │   - usage_quota（配额管理）
           │   - channel_profiles（频道画像）
           │
           ↓
┌─────────────────────────────────────────────────────┐
│  问答Bot（qa_bot.py）                                 │
│  - 自然语言对话接口                                    │
│  - 意图解析器                                         │
│  - 记忆管理器                                         │
│  - 问答引擎                                           │
│  - 配额管理器                                         │
└─────────────────────────────────────────────────────┘
```

### 技术选型

| 组件 | 技术方案 | 说明 |
|------|---------|------|
| 数据存储 | MySQL + aiomysql | 支持跨进程队列、连接池和并发读写 |
| 检索方式 | agentic 工具调用：语义向量检索 + 关键词检索 | QAEngineV3，可选重排序，支持无工具降级 |
| 意图识别 | LLM 工具调用 | 模型自主决定检索策略（v2.0 规则引擎已由 V3 取代） |
| 元数据提取 | AI (LLM) | 自动提取关键词、主题、情感 |
| 配额管理 | 数据库计数 | 每日自动重置 |

---

## 🔀 双运行时（Go / Python）

自 2026-10 起，问答 Bot 支持 Python 与 Go 两种运行时实现：

- **Python 版**（`qa_bot.py`）：本文档描述的原始实现，保留作为回退；
- **Go 版**（`qa-bot-go/`）：行为逐条对齐 Python 版的 Go 实现，进程内存占用从约 137MB 降至约 20MB（v2.2.0 生产实测）。

查看与切换（管理员专用）：

| 方式 | 入口 |
|------|------|
| 主 Bot 命令 | `/qa_runtime`（别名 `/qa_运行时`）查看当前运行时；`/qa_runtime go\|python` 切换 |
| WebUI | 「系统」页一键切换（`POST /api/system/qa-bot/runtime`） |
| 部署级覆盖 | 环境变量 `QA_BOT_RUNTIME=go\|python`（`data/.env`；缺省 go） |

优先级：`config.json` > `QA_BOT_RUNTIME` 环境变量 > `go`（缺省 go）。切换会写入 `config.json` 持久化并立即重启问答 Bot（进行中的问答会话将被中断）；Go 二进制缺失时自动回退 Python 并告警。

Go 版与 Python 版的行为对齐方式及已知的有意差异（如配额非原子语义、float32 舍入等），见 [运行时占用优化](Developer-Runtime-Footprint-Optimization.md)。

---

## 📦 新增文件

### 核心模块

> 以下模块 v2.0 时位于 `core/` 下，后随模块拆分迁至 `core/ai/`，此处为当前路径。

1. **core/ai/quota_manager.py** - 配额管理器
   - 用户配额检查和管理
   - 每日总限额控制
   - 管理员豁免机制

2. **core/ai/intent_parser.py** - 意图解析器
   - 自然语言意图识别
   - 时间范围提取
   - 关键词和主题提取
   - 注：v2.0 实现；V3 引擎改为由 LLM 通过工具调用自行决定检索策略

3. **core/ai/memory_manager.py** - 记忆管理器
   - 频道画像管理
   - 元数据提取（关键词、主题、情感）
   - 智能搜索功能

4. **core/ai/qa_engine_v3.py** - 问答引擎（已迭代至 V3）
   - agentic 工具调用（LLM 自主决定检索策略）
   - AI生成回答（流式输出）
   - 降级方案（无工具直连对话）

5. **qa_bot.py** - 问答Bot入口
   - Telegram Bot接口
   - 消息处理逻辑
   - 配额集成

### 文档

- **wiki/User-QA-Bot-Guide.md** - 问答Bot使用指南
- **wiki/Developer-QA-Bot-Implementation.md** - 本文档

---

## 🔄 修改文件

> 以下为 v2.0 当时的变更记录；`core/database.py` 已随模块拆分迁移，应用数据库也已由 SQLite 改为 MySQL（SQLite(WAL) 现仅用于 lite 向量索引文件）。

### 1. core/database.py

**变更内容**：

#### a) 启用WAL模式
```python
cursor.execute("PRAGMA journal_mode=WAL")
cursor.execute("PRAGMA synchronous=NORMAL")
cursor.execute("PRAGMA busy_timeout=5000")
```

#### b) 扩展summaries表
```sql
ALTER TABLE summaries ADD COLUMN keywords TEXT;
ALTER TABLE summaries ADD COLUMN topics TEXT;
ALTER TABLE summaries ADD COLUMN sentiment TEXT;
ALTER TABLE summaries ADD COLUMN entities TEXT;
```

#### c) 新增usage_quota表
```sql
CREATE TABLE IF NOT EXISTS usage_quota (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER NOT NULL,
    query_date TEXT NOT NULL,
    usage_count INTEGER DEFAULT 0,
    last_reset TEXT DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(user_id, query_date)
);
```

#### d) 新增channel_profiles表
```sql
CREATE TABLE IF NOT EXISTS channel_profiles (
    channel_id TEXT PRIMARY KEY,
    channel_name TEXT,
    style TEXT DEFAULT 'neutral',
    topics TEXT,
    keywords_freq TEXT,
    tone TEXT,
    avg_message_length REAL DEFAULT 0,
    total_summaries INTEGER DEFAULT 0,
    last_updated TEXT DEFAULT CURRENT_TIMESTAMP
);
```

#### e) 新增方法
- `get_quota_usage()` - 获取配额使用情况
- `check_and_increment_quota()` - 检查并增加配额
- `get_total_daily_usage()` - 获取总使用次数
- `get_channel_profile()` - 获取频道画像
- `update_channel_profile()` - 更新频道画像
- `update_summary_metadata()` - 更新总结元数据

### 2. data/.env.example

**新增配置**：
```env
# ===== 问答Bot配置 =====
QA_BOT_TOKEN=your_qa_bot_token_here
QA_BOT_ENABLED=True
QA_BOT_USER_LIMIT=3
QA_BOT_DAILY_LIMIT=200
```

---

## 🎨 功能特性

### 1. 自然语言查询

**支持的查询类型**：

- **总结查询**: "上周发生了什么？"
- **关键词搜索**: "关于GPT-4的讨论"
- **主题查询**: "有什么技术讨论？"
- **统计查询**: "系统有多少条总结？"
- **状态查询**: "我的配额还剩多少？"

### 2. 智能意图识别

**识别能力**：
- 时间范围（今天、昨天、本周、上周、最近N天等）
- 关键词（AI、GPT、Python等）
- 主题（技术、新闻、讨论等）
- 查询类型（总结、统计、状态等）

### 3. 频道画像

**画像维度**：
- **风格** (style): tech/casual/neutral
- **主题** (topics): 常讨论的主题列表
- **关键词频率** (keywords_freq): 关键词出现频率
- **情感倾向** (tone): positive/neutral/negative
- **平均消息长度**: 统计数据
- **总结总数**: 统计数据

### 4. 配额管理

**配额规则**：
- 普通用户: 每日3次
- 管理员: 无限制
- 系统总限额: 每日200次
- 自动重置: 每日00:00 (UTC)

---

## 🚀 部署指南

### 自动启动

**重要更新**：main.py 已集成自动启动问答Bot功能！

启动 main.py 时会自动：
1. 检查 `QA_BOT_ENABLED` 配置
2. 检查 `QA_BOT_TOKEN` 是否配置
3. 在后台自动启动问答Bot进程
4. 主Bot退出时自动停止问答Bot

**环境变量**：
```env
# 问答Bot会随main.py自动启动
QA_BOT_ENABLED=True  # True=自动启动，False=禁用
QA_BOT_TOKEN=your_qa_bot_token_here
```

**无需手动操作**：
```bash
# 只需启动主Bot即可
python main.py

# 或使用Docker
docker compose up -d sakura-summary-bot
```

### 1. 创建问答Bot

```bash
# 在Telegram中访问 @BotFather
/newbot
# 按提示创建新Bot，获取Token
```

### 2. 配置环境变量

编辑 `data/.env`：

```env
QA_BOT_TOKEN=your_qa_bot_token_here
QA_BOT_ENABLED=True
QA_BOT_USER_LIMIT=3
QA_BOT_DAILY_LIMIT=200
```

### 3. 启动服务

**方式1：随主 Bot 自动启动（推荐）**

问答 Bot 由主 Bot 进程作为子进程托管（缺省 Go 运行时，Go 二进制缺失时自动回退 Python）：

```bash
# 本地源码
python main.py

# Docker Compose（问答 Bot 随主容器自动启动）
docker compose up -d sakura-summary-bot
```

**方式2：独立运行（调试用）**
```bash
python qa_bot.py
```

> 注意不要与主 Bot 托管的问答 Bot 同时使用同一个 Token。`docker-compose.yml` 只有 `sakura-summary-bot` 一个服务，无需也不应另建独立的 `qa_bot` 服务。

### 4. 验证部署

在Telegram中向问答Bot发送 `/start` 命令，应该收到欢迎消息。

---

## 📊 性能指标

### 预期性能

- **查询响应时间**: 2-5秒（包含AI生成）
- **并发模型**: 问答消息串行分发（对齐 PTB 默认行为），数据库访问经 MySQL 连接池（`MYSQL_POOL_SIZE` 缺省 5）
- **数据库大小**: 预计每月增长10-50MB
- **AI成本**: 每次查询约100-500 tokens

### 优化措施

1. **数据库优化**
   - MySQL 连接池复用连接
   - 关键字段索引
   - 查询结果缓存

2. **AI优化**
   - 精简提示词
   - 温度控制（0.7）
   - 降级方案

3. **配额控制**
   - 防止滥用
   - 成本可控
   - 自动重置

---

## 🧪 测试建议

### 功能测试

```bash
# 1. 基础查询测试
"上周发生了什么？"
"最近有什么关于AI的讨论？"
"今天的更新"

# 2. 配额测试
/status  # 查看配额
# 连续查询直到配额用尽

# 3. 错误处理测试
# 测试无结果查询
# 测试AI失败降级
```

### 性能测试

```bash
# 1. 并发测试
# 同时发送10个查询

# 2. 长时间运行
# 运行24小时，监控稳定性

# 3. 数据库压力
# 插入1000条总结，测试查询性能
```

---

## 📝 使用说明

### 用户端

1. **启动对话**
   ```
   /start
   ```

2. **开始提问**
   ```
   频道上周发生了什么？
   ```

3. **查看配额**
   ```
   /status
   ```

### 管理员端

1. **查看系统状态**
   ```
   系统统计
   ```

2. **调整配额**
   - 在 WebUI「AI 配置」页的问答配额面板在线调整（热生效）
   - 或修改 `config.json` 的 `qa_bot_quota`；环境变量 `QA_BOT_USER_LIMIT` / `QA_BOT_DAILY_LIMIT` 仅作部署级兜底

3. **监控日志**
   ```bash
   # 主容器日志（含问答子进程输出）
   docker compose logs -f sakura-summary-bot

   # 问答 Bot 组件日志
   docker compose exec sakura-summary-bot tail -n 100 logs/qa-bot.log
   ```

---

## 🐛 已知问题

### 当前限制

1. **数据迁移**
   - 旧总结无元数据
   - 需要手动或自动提取
   - 新总结自动提取元数据

> 早期的「无语义搜索」「无多轮对话」两项限制已分别由 QAEngineV3 语义向量检索（`VECTOR_BACKEND=chroma|lite` 双后端）与 ConversationManager 多轮会话解决，不再列为已知问题。

---

## 🚧 未来规划

### Phase 5: 高级特性落地情况

- [x] **向量数据库集成**（已完成）
  - ChromaDB 集成已上线多个版本；v2.2.0 起新增 SQLite(WAL)+numpy 轻量后端，`VECTOR_BACKEND=chroma|lite` 切换，见 [运行时占用优化](Developer-Runtime-Footprint-Optimization.md)
  - 语义搜索与相似度排序（QAEngineV3 `semantic_search` 工具，可选重排序）

- [x] **多轮对话支持**（已完成）
  - 会话上下文记忆（ConversationManager，Python 与 Go 版均已实现）
  - 跟进问题与上下文引用，见 [多轮对话功能](User-Multi-Turn-Conversation.md)

- [ ] **个性化推荐**
  - 用户偏好学习
  - 智能推荐总结
  - 自定义查询模板

- [x] **Web Dashboard**（已完成）
  - 可视化统计与配额管理界面（WebUI 统计页与「AI 配置」页问答配额面板）

- [x] **API接口**（已完成）
  - WebUI 后端 RESTful API（`core/web_api/`）

---

## 📞 技术支持

### 文档资源

- **使用指南**: [问答 Bot 指南](User-QA-Bot-Guide.md)
- **主README**: [README.md](../README.md)
- **代码文档**: 代码内docstring

### 问题反馈

- **GitHub Issues**: [提交问题](https://github.com/Sakura520222/Sakura-Bot/issues)
- **Discussions**: [参与讨论](https://github.com/Sakura520222/Sakura-Bot/discussions)
- **Email**: [sakura520222@outlook.com](mailto:sakura520222@outlook.com)

---

## ✅ 总结

本次更新成功实现了基于历史总结的智能问答系统，具有以下特点：

### 优势

✅ **独立架构** - 子进程托管，不影响主Bot运行
✅ **简单高效** - agentic 工具调用：语义向量检索 + 关键词检索
✅ **易于维护** - 代码结构清晰
✅ **成本可控** - 配额管理防止滥用
✅ **扩展性强** - 工具调用架构易于扩展新的检索能力

### 建议

1. **监控运行** - 定期检查日志和性能
2. **收集反馈** - 了解用户真实需求
3. **迭代优化** - 根据使用数据优化
4. **文档更新** - 及时更新使用文档

---

**实施完成日期**: 2026-02-14
**文档版本**: v1.0
**作者**: Sakura520222

---

<div align="center">

**🌸 Sakura-Bot**

让频道管理更智能 · 让信息查询更便捷

[⭐ Star](https://github.com/Sakura520222/Sakura-Bot) · [🍴 Fork](https://github.com/Sakura520222/Sakura-Bot/fork) · [📖 文档](../wiki)

</div>
