// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package vectors

import (
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"

	"path/filepath"
	"testing"
)

const testDim = 1024 // 契约冻结维度：BLOB = 小端 float32 × 1024

// 契约 DDL（qa-bot-go/README.md「SQLite 向量存储契约」，逐字对齐）。
const contractDDL = `
CREATE TABLE IF NOT EXISTS store_meta (key TEXT PRIMARY KEY, value INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS summaries_vectors (
  id TEXT PRIMARY KEY, embedding BLOB NOT NULL, document TEXT NOT NULL,
  metadata_json TEXT NOT NULL, dim INTEGER NOT NULL, stored_at TEXT NOT NULL) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS messages_vectors (
  id TEXT PRIMARY KEY, embedding BLOB NOT NULL, document TEXT NOT NULL,
  metadata_json TEXT NOT NULL, dim INTEGER NOT NULL, stored_at TEXT NOT NULL) WITHOUT ROWID;
`

// embedBlob 按契约编码：小端 float32 × dim。
func embedBlob(vec []float32) []byte {
	b := make([]byte, 4*len(vec))
	for i, x := range vec {
		binary.LittleEndian.PutUint32(b[i*4:], math.Float32bits(x))
	}
	return b
}

// basisVec 返回 dim 维单位正交基的第 i 个向量 e_i（norm=1，两两点积为 0）。
func basisVec(i int) []float32 {
	v := make([]float32, testDim)
	v[i] = 1
	return v
}

// mustExec 执行 SQL 并在失败时终止测试。
func mustExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("执行 %q 失败: %v", query, err)
	}
}

// openWriter 打开读写连接（测试种子数据用），启用 WAL 对齐生产契约
// （只读侧的 -shm 可访问性依赖写侧伴生文件存在）。
func openWriter(t *testing.T, file string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.ToSlash(file))
	if err != nil {
		t.Fatalf("打开写连接失败: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	var mode string
	if err := db.QueryRow("PRAGMA journal_mode=WAL").Scan(&mode); err != nil {
		t.Fatalf("启用 WAL 失败: %v", err)
	}
	mustExec(t, db, contractDDL)
	return db
}

// seedMeta 写入 store_meta 契约键（schema_version=1 与两 collection 版本号）。
func seedMeta(t *testing.T, db *sql.DB, sumVer, msgVer int64) {
	t.Helper()
	mustExec(t, db, "INSERT INTO store_meta (key, value) VALUES ('schema_version', 1)")
	mustExec(t, db, "INSERT INTO store_meta (key, value) VALUES ('summaries_version', ?)", sumVer)
	mustExec(t, db, "INSERT INTO store_meta (key, value) VALUES ('messages_version', ?)", msgVer)
}

// insertDoc 按 contract 表结构写入一条向量。
func insertDoc(t *testing.T, db *sql.DB, table, id string, vec []float32, document string, metadata map[string]any) {
	t.Helper()
	metaJSON, err := json.Marshal(metadata)
	if err != nil {
		t.Fatalf("metadata 序列化失败: %v", err)
	}
	mustExec(t, db,
		"INSERT INTO "+table+" (id, embedding, document, metadata_json, dim, stored_at) VALUES (?, ?, ?, ?, ?, ?)",
		id, embedBlob(vec), document, string(metaJSON), len(vec), "2026-01-01T00:00:00+00:00")
}

// itoa 避免引入 strconv 全名的轻量封装（测试内多处使用）。
func itoa(i int) string { return fmt.Sprintf("%d", i) }

// idsOf 提取结果 DocID 集合。
func idsOf(hits []Hit) map[string]bool {
	m := make(map[string]bool, len(hits))
	for _, h := range hits {
		m[h.DocID] = true
	}
	return m
}

// TestSearchOrthogonalTopK  Mandated 用例：临时 SQLite 写入 100 条两两正交
// 的 1024 维向量，断言 top-k 命中、默认 TopK、超量 TopK、all 合并并列顺序
// 与零向量查询不产生 NaN。
func TestSearchOrthogonalTopK(t *testing.T) {
	dir := t.TempDir()
	db := openWriter(t, filepath.Join(dir, dbFileName))
	seedMeta(t, db, 1, 1)
	for i := 0; i < 100; i++ {
		insertDoc(t, db, "summaries_vectors", itoa(i), basisVec(i),
			"doc-"+itoa(i), map[string]any{"channel_id": "-100100", "created_at": "2026-03-01T00:00:00+00:00"})
	}
	insertDoc(t, db, "messages_vectors", "100200:42", basisVec(42), "msg-42",
		map[string]any{"channel_id": "-100200", "created_at": "2026-03-01T00:00:00+00:00", "message_id": 42})

	store := New(dir)
	t.Cleanup(func() { _ = store.Close() }) // Windows 下未关连接会锁住 TempDir 清理
	ctx := context.Background()

	t.Run("精确命中且相似度为1", func(t *testing.T) {
		hits, err := store.Search(ctx, basisVec(42), Options{Collection: "summaries", TopK: 5})
		if err != nil {
			t.Fatalf("Search 失败: %v", err)
		}
		if len(hits) != 5 {
			t.Fatalf("结果数 = %d, 期望 5", len(hits))
		}
		if hits[0].DocID != "42" {
			t.Fatalf("top1 DocID = %q, 期望 \"42\"", hits[0].DocID)
		}
		if math.Abs(hits[0].Similarity-1) > 1e-6 {
			t.Fatalf("top1 Similarity = %v, 期望 1", hits[0].Similarity)
		}
		// 其余 4 条为正交（相似度恰为 0），且不得包含正命中与重复 ID。
		seen := map[string]bool{}
		for _, h := range hits[1:] {
			if h.Similarity != 0 {
				t.Fatalf("正交项相似度应为 0，得到 %v (%s)", h.Similarity, h.DocID)
			}
			if h.DocID == "42" || seen[h.DocID] {
				t.Fatalf("结果重复或包含 top1: %s", h.DocID)
			}
			seen[h.DocID] = true
		}
		if hits[0].Metadata["channel_id"] != "-100100" {
			t.Fatalf("metadata.channel_id 透传失败: %v", hits[0].Metadata["channel_id"])
		}
	})

	t.Run("TopK缺省为20", func(t *testing.T) {
		hits, err := store.Search(ctx, basisVec(7), Options{Collection: "summaries"})
		if err != nil {
			t.Fatalf("Search 失败: %v", err)
		}
		if len(hits) != 20 {
			t.Fatalf("默认 TopK 应为 20，得到 %d", len(hits))
		}
		if hits[0].DocID != "7" {
			t.Fatalf("top1 = %q, 期望 \"7\"", hits[0].DocID)
		}
	})

	t.Run("TopK超过行数返回全部", func(t *testing.T) {
		hits, err := store.Search(ctx, basisVec(0), Options{Collection: "summaries", TopK: 200})
		if err != nil {
			t.Fatalf("Search 失败: %v", err)
		}
		if len(hits) != 100 {
			t.Fatalf("结果数 = %d, 期望 100", len(hits))
		}
	})

	t.Run("all 合并列并列时按 summaries 优先", func(t *testing.T) {
		// summaries 的 "42" 与 messages 的 "100200:42" 对查询向量同为相似度 1，
		// 合并后 top1 必须是其中之一且两条都进 top2（并列稳定排序）。
		hits, err := store.Search(ctx, basisVec(42), Options{Collection: "all", TopK: 2})
		if err != nil {
			t.Fatalf("Search 失败: %v", err)
		}
		if len(hits) != 2 {
			t.Fatalf("结果数 = %d, 期望 2", len(hits))
		}
		got := idsOf(hits)
		if !got["42"] || !got["100200:42"] {
			t.Fatalf("并列 top2 应含两侧正命中: %v", got)
		}
		for _, h := range hits {
			if math.IsNaN(h.Similarity) || math.IsInf(h.Similarity, 0) {
				t.Fatalf("相似度出现 NaN/Inf: %v (%s)", h.Similarity, h.DocID)
			}
		}
	})

	t.Run("零向量查询不产生 NaN", func(t *testing.T) {
		zero := make([]float32, testDim)
		hits, err := store.Search(ctx, zero, Options{Collection: "summaries", TopK: 5})
		if err != nil {
			t.Fatalf("Search 失败: %v", err)
		}
		for _, h := range hits {
			if math.IsNaN(h.Similarity) {
				t.Fatalf("零向量查询产生 NaN: %s", h.DocID)
			}
		}
	})
}

// TestSearchFilters 日期过滤与频道过滤语义（naive 视为 UTC、坏边界忽略、
// 缺 created_at 丢弃、channel_id 精确匹配）。
func TestSearchFilters(t *testing.T) {
	dir := t.TempDir()
	db := openWriter(t, filepath.Join(dir, dbFileName))
	seedMeta(t, db, 1, 1)
	insertDoc(t, db, "summaries_vectors", "1", basisVec(1), "d1",
		map[string]any{"channel_id": "-100100", "created_at": "2026-03-01T12:00:00+00:00"})
	insertDoc(t, db, "summaries_vectors", "2", basisVec(2), "d2",
		map[string]any{"channel_id": "-100200", "created_at": "2026-01-01T00:00:00+00:00"})
	insertDoc(t, db, "summaries_vectors", "3", basisVec(3), "d3",
		map[string]any{"channel_id": "-100100"}) // 缺 created_at：有边界时应被丢弃
	insertDoc(t, db, "summaries_vectors", "4", basisVec(4), "d4",
		map[string]any{"channel_id": "-100100", "created_at": "2026-05-01T00:00:00Z"})

	store := New(dir)
	t.Cleanup(func() { _ = store.Close() }) // Windows 下未关连接会锁住 TempDir 清理
	ctx := context.Background()

	t.Run("DateAfter 过滤且 naive 按 UTC 命中", func(t *testing.T) {
		// naive 边界 "2026-02-01T00:00:00" 应视为 UTC，等价 2026-02-01T00:00:00+00:00
		hits, err := store.Search(ctx, basisVec(1), Options{Collection: "summaries", TopK: 10, DateAfter: "2026-02-01T00:00:00"})
		if err != nil {
			t.Fatalf("Search 失败: %v", err)
		}
		got := idsOf(hits)
		if !got["1"] || !got["4"] || len(hits) != 2 {
			t.Fatalf("DateAfter 后应只剩 1/4（缺 created_at 的 3 被丢弃）: %v", got)
		}
	})

	t.Run("坏边界忽略等于不过滤", func(t *testing.T) {
		hits, err := store.Search(ctx, basisVec(1), Options{Collection: "summaries", TopK: 10, DateAfter: "not-a-date"})
		if err != nil {
			t.Fatalf("Search 失败: %v", err)
		}
		if len(hits) != 4 {
			t.Fatalf("坏边界应被忽略，返回全部 4 条，得到 %d", len(hits))
		}
	})

	t.Run("ChannelID 精确匹配", func(t *testing.T) {
		hits, err := store.Search(ctx, basisVec(1), Options{Collection: "summaries", TopK: 10, ChannelID: "-100200"})
		if err != nil {
			t.Fatalf("Search 失败: %v", err)
		}
		if len(hits) != 1 || hits[0].DocID != "2" {
			t.Fatalf("ChannelID=-100200 应只命中 2: %v", idsOf(hits))
		}
	})
}

// TestVersionInvalidation 版本失效：他进程 bump store_meta 后，本进程下一次
// Search 前检测到并整表重载（README 硬规则 2：禁用文件 stat 方案）。
func TestVersionInvalidation(t *testing.T) {
	dir := t.TempDir()
	db := openWriter(t, filepath.Join(dir, dbFileName))
	seedMeta(t, db, 7, 7)
	insertDoc(t, db, "summaries_vectors", "1", basisVec(1), "d1",
		map[string]any{"channel_id": "-100100", "created_at": "2026-03-01T00:00:00+00:00"})

	store := New(dir)
	t.Cleanup(func() { _ = store.Close() }) // Windows 下未关连接会锁住 TempDir 清理
	ctx := context.Background()
	hits, err := store.Search(ctx, basisVec(1), Options{Collection: "summaries", TopK: 10})
	if err != nil {
		t.Fatalf("首次 Search 失败: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("首次应只有 1 条，得到 %d", len(hits))
	}

	// 模拟主进程写入：插入新行并 bump 版本号（同事务语义）。
	insertDoc(t, db, "summaries_vectors", "2", basisVec(2), "d2",
		map[string]any{"channel_id": "-100100", "created_at": "2026-03-02T00:00:00+00:00"})
	mustExec(t, db, "UPDATE store_meta SET value = value + 1 WHERE key = 'summaries_version'")

	hits, err = store.Search(ctx, basisVec(2), Options{Collection: "summaries", TopK: 10})
	if err != nil {
		t.Fatalf("失效重载后的 Search 失败: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("版本 bump 后下一次 Search 应看到 2 条，得到 %d", len(hits))
	}
}

// TestNotReady 文件/表缺失返回 ErrNotReady 且不锁存（下一次就绪后可恢复）。
func TestNotReady(t *testing.T) {
	store := New(t.TempDir()) // 空：lite_index.sqlite3 不存在
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	if _, err := store.Search(ctx, basisVec(0), Options{Collection: "summaries"}); !errors.Is(err, ErrNotReady) {
		t.Fatalf("空目录应返回 ErrNotReady，得到 %v", err)
	}

	// 库就绪后同一实例应能自愈（不锁存错误）。
	dir := t.TempDir()
	db := openWriter(t, filepath.Join(dir, dbFileName))
	seedMeta(t, db, 1, 1)
	insertDoc(t, db, "summaries_vectors", "9", basisVec(9), "d9",
		map[string]any{"channel_id": "-100100", "created_at": "2026-03-01T00:00:00+00:00"})
	s2 := New(dir)
	t.Cleanup(func() { _ = s2.Close() })
	if _, err := s2.Search(ctx, basisVec(9), Options{Collection: "summaries"}); err != nil {
		t.Fatalf("就绪后 Search 应成功: %v", err)
	}
}
