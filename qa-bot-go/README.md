# sakura-qa（qa_bot.py 的 Go 移植）

目标：以单静态二进制替代 Python 子进程 `qa_bot.py`，稳态 RSS ≤30MB，
行为与 Python 版逐条对齐，`QA_BOT_RUNTIME=go|python` 一键回退。

- 行为规格唯一事实来源：`qa_bot.py` 及其闭包（设计文档见会话记录）。
- module：`github.com/Sakura520222/Sakura-Bot/qa-bot-go`，Go 1.27，`CGO_ENABLED=0` 全静态。
- 依赖白名单：`go-telegram/bot`、`go-sql-driver/mysql`、`modernc.org/sqlite`，其余一律 stdlib。

## 评审裁决的硬规则（违反即返工）

1. **串行分发**：update 处理链单 goroutine 顺序执行（对齐 PTB 默认，绝不做 goroutine-per-update）；
   每次 handleUpdate 顶层 `defer recover()`，panic 记日志不杀进程。两个 ticker job 独立 goroutine。
2. **向量新鲜度**：每次 search 前 `SELECT value FROM store_meta WHERE key IN ('summaries_version','messages_version')`
   判断重载（WAL 下主文件 mtime 不可靠，禁用 stat 方案）。
3. **rune 计数**：一切长度判定用 `utf8.RuneCountInString`（80 字符阈值 / 4096 限长 / 2000/1000/500 字限）。
4. **TG API 调用统一 5s 超时**（申诉卡片 25s 例外）；429 仅当 `RetryAfter <= 60s` 才睡眠重试一次，否则按错误返回；
   `message is not modified` 静默。
5. **GOMEMLIMIT** 默认 96MiB（env 可调），在 main.go 里 `debug.SetMemoryLimit`。
6. **GET_LOCK**：取锁/工作/释放必须在同一 `db.Conn` 上，defer 双保险，NULL/Scan 错误按获取失败处理（fail-closed）。
7. **配额**：config.json 越界 → 配额检查整体失败拒绝（复刻 `model_validate` raise 语义），env 才是 clamp；
   预留保持非原子 SELECT+UPDATE（对齐 Python 现状，串行分发下等价）。
8. 通知未知类型 → 发"您有新的通知"（fallback 分支不可漏）。
9. 控制标记协议 `__DONE__` / `__NEW_SESSION__` / `__ERROR__:` 与 Python 字符串逐字一致。
10. 向量 BLOB = 小端 float32×1024；`similarity = 1 - distance`；`round(x,3)` = `math.Round(x*1000)/1000`。

## SQLite 向量存储契约（与 core/ai/vector_lite.py 共享，冻结）

文件：`{VECTOR_DB_PATH}/lite_index.sqlite3`（默认 `data/vectors/lite_index.sqlite3`），WAL。

```sql
CREATE TABLE store_meta (key TEXT PRIMARY KEY, value INTEGER NOT NULL);
-- 'schema_version'=1, 'summaries_version', 'messages_version'
CREATE TABLE summaries_vectors (
  id TEXT PRIMARY KEY, embedding BLOB NOT NULL, document TEXT NOT NULL,
  metadata_json TEXT NOT NULL, dim INTEGER NOT NULL, stored_at TEXT NOT NULL) WITHOUT ROWID;
CREATE TABLE messages_vectors (同 summaries_vectors);
```

- summaries.id = `str(MySQL summaries.id)`；messages.id = `"{channel_id}:{message_id}"`。
- metadata_json 含 `channel_id/channel_name/created_at`(aware ISO UTC)/`message_id`(messages)/`sender_id`(messages) 等。
- 只读打开：`file:...lite_index.sqlite3?mode=ro&_busy_timeout=5000`（禁 immutable=1）。
- 检索：全表加载归一化 float32 矩阵 → 暴力余弦 → date 过滤按 metadata.created_at（naive 视为 UTC）→ 按 similarity 降序 top_k。
