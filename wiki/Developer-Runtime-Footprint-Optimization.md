# 运行时占用优化与 Rust/Go 迁移评估

> 分支 `feature/resource-optimization` · 2026-10 · 结论：**Python 主进程瘦身 + QA 子进程 Go 化**，
> 容器总占用从 **≈400MB（RSS+swap）降至 ≈200MB 量级**（最终数字见文末实测表）。

## 1. 动机与基线

部署机为 2 核 / 4GB / swap 2GB 的单机，长期 swap 用量 >1GB。实测基线（2026-10-26，容器 sakura-bot）：

| 进程 | RSS | Swap |
|---|---|---|
| main.py（主进程） | 202 MB | 23 MB |
| qa_bot.py（QA 子进程） | 137 MB | 37 MB |
| 合计 | **339 MB** | **60 MB** |

容器内逐模块 `import` 的 RSS 归因（增量）：

| 模块 | RSS 增量 | 说明 |
|---|---|---|
| telethon | **+59 MB** | MTProto 客户端，结构性大头 |
| chromadb（含 numpy） | +24~32 MB | 进程内向量库，实际仅 **3,649 条向量（1024 维）** |
| fastapi + uvicorn | +17 MB | WebUI 后端 |
| openai | +12 MB | |
| python-telegram-bot | +10 MB | 主进程只把它当 HTTP 客户端用 |
| numpy | +8 MB | |
| aiomysql | +8 MB | |
| pydantic-settings | +6 MB | |
| 解释器基线 | 9 MB | |

结论：**"重"不在业务代码，而在依赖形态** —— 三类可砍：进程内 ChromaDB（超配）、
主进程里可有可无的 PTB、以及 QA 子进程因传递 import 白白加载的 Telethon。

## 2. Rust vs Go 评估结论（摘要）

完整调研（2026-10 快照，来源标注 A/B/C 可信度）见本文件历史版本，要点：

- **Bot API（QA 试点）**：Go `go-telegram/bot`（Bot API 10.3、零第三方依赖、MIT）最合适；
  Rust 对应 teloxide 可用但上手成本高。
- **MTProto（主进程未来整体迁移）**：Rust **grammers** 是当前唯一贴 Telegram 最新 layer
  的库（已迁 Codeberg，持续发版）；Go gotd/td 可用但发布节奏慢。主进程若迁，Rust 优先。
- **MySQL/SSE/向量**：两侧生态均成熟；本项目规模（4k 向量）暴力余弦即可，无需任何 ANN 库。
- **评分**（生态/体积/开发速度/长期维护加权）：Go-first 4.4 vs Rust-first 4.0。
- **决策**：QA 子进程用 **Go**（快出成果、依赖面最小、CGO_ENABLED=0 单命令交叉编译）；
  主进程暂留 Python（telethon +59MB 是结构性地板，整体迁移是独立立项，见 §6 路线图）。

## 3. 落地改动

### 3.1 主进程去 PTB（`d3eab68`）

主进程对 PTB 的全部用途是「Bot API HTTP 客户端 + 异常分类」。新增
`core/infrastructure/telegram_bot_api.py`（httpx 直调、共享连接池、异常 description
保留原文以兼容既有子串匹配）替换 5 个触点：telegram_notifier、mainbot_push_handler、
mainbot_request_handler（删除 PTB 死代码分支）、moderation/ban_history（身份校验改为
token 前缀解析，省一次 getMe）、deleted_cleanup（异常类型体系替换）。
主进程 import 链不再加载 PTB（**-10MB**）。

### 3.2 轻量向量库替换 chromadb（`92dbf43`）

`VECTOR_BACKEND=chroma|lite` 门面（缺省 chroma，未知值告警回退）；lite 后端 =
SQLite（WAL）BLOB（小端 float32）+ numpy 归一化内存矩阵暴力余弦：

- 3,649×1024 矩阵 ≈15MB 常驻；单次查询全量点积 <1ms（此前 HNSW 属严重超配）。
- 跨进程一致性：`store_meta` 版本号表，读侧每次查询前检查（WAL 下文件 mtime 不可靠），
  他进程写入后下一次查询即重载；写侧同事务原子 bump。
- WebUI 零改动：提供 chroma 兼容 collection 适配器（count/get/delete）。
- 迁移：`scripts/migrate_vectors_to_sqlite.py`（停机窗口：tmp → verify 行数/维度/抽样
  余弦 → 清 -wal/-shm → 原子替换；幂等标记 `data/.vector_migration_done`）；
  反向 `scripts/backfill_sqlite_to_chroma.py` 保障任意时点回滚数据完整。
- lite 模式主/QA 两进程都不再 import chromadb（**-24~32MB/进程**）。
- 顺带修复两个既有缺陷：naive 日期边界从「整体返回 []」改为按 UTC 归一化命中；
  日期过滤从「超采 top_k*3」改为全量精确过滤。

### 3.3 其他主进程瘦身（`d3eab68` 内）

- matcher lru_cache 上限 2048→256（长文 key 内存上限收紧）；
- version_utils aiohttp 惰性导入；
- Dockerfile `MALLOC_ARENA_MAX=2`（glibc 碎片抑制，利好 swap）。

### 3.4 QA 子进程 Go 化（`8ea98ea` 起系列提交）

`qa-bot-go/`（module `github.com/Sakura520222/Sakura-Bot/qa-bot-go`，Go 1.27）：
行为逐条对齐 qa_bot.py 及其闭包（约 6-7k 行有效逻辑），依赖白名单
`go-telegram/bot` + `go-sql-driver/mysql` + `modernc.org/sqlite`，其余全 stdlib。

关键行为保真（对抗评审裁决）：

- **串行分发**：单 goroutine 顺序处理 update（对齐 PTB 默认，配额非原子性等行为依赖此），
  每条 update 顶层 recover；
- 流式 editMessageText 节流 80 rune / 1.5s 双阈值、4096 限长、markdown 修复器逐 rune；
- TG API 统一 5s 超时；429 仅 `RetryAfter ≤ 60s` 才睡眠重试；
- GET_LOCK 同连接取放 + defer 双保险 + NULL fail-closed；
- 配额/通知/申诉状态机 SQL 逐字移植；i18n 双语字典内嵌 + `t()` 容错插值；
- 向量检索只读共享同一 SQLite 文件（跨语言契约冻结在 `qa-bot-go/README.md`）；
- `GOMEMLIMIT` 默认 96MiB（env 可调）。

接入与回退：`QA_BOT_RUNTIME=go|python`（`process_manager` 唯一控制点；二进制缺失自动
回退 python 并告警；`get_qa_bot_status().runtime` 可见实际实现）；Dockerfile 新增
golang 构建层，产物装 `/usr/local/bin/sakura-qa`（避开 `/app` 挂载遮蔽）。
回退 = 改回 `python` + `docker compose restart`，无需数据回迁。

## 4. 已知行为差异（有意为之）

| 差异 | 理由 |
|---|---|
| lite 日期边界 naive 按 UTC 归一化 | 原实现遇 naive 边界整体返回 []，真实可触发（LLM 透传） |
| 日期过滤全量精确而非超采 top_k*3 | 纯改善，结果只会更准/更多 |
| WebUI 向量分页顺序改为 ORDER BY id | 无调用方依赖插入序，仅需分页稳定 |
| Go 版配额保持非原子 SELECT+UPDATE | 串行分发下与 Python 等价；原子化属行为收紧，不混入 |
| float32 舍入致 similarity 差 <1e-6 | 消费方 round(,3)，无感 |

## 5. 实测对比

（部署后填充：main.py / QA 进程 RSS、docker stats、swap 变化，及与基线表的对照。）

## 6. 后续路线图（若要继续压）

1. **主进程整体迁移 Rust（grammers）**：MTProto+UserBot+转发+总结调度，预估月级工作量；
   参考本评估 §2，grammers 是唯一贴最新 layer 的选择。
2. **WebUI 后端进程外置**（fastapi+uvicorn -17MB；零代码替代=WEBUI_ENABLED=false）。
3. **openai 惰性化**：AI 功能关闭的部署可全程不加载（活跃部署稳态无收益）。
4. **删除 chromadb 依赖行**：lite 陪跑稳定一个总结周期+realtime 写入周期后，
   从 requirements 移除（镜像再瘦身），保留反向回填脚本作为回滚通道。
