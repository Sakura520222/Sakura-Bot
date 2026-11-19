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
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	sqlite "modernc.org/sqlite"
)

const (
	// DefaultDimension 为默认向量维度（对齐 EMBEDDING_DIMENSION=1024）。
	DefaultDimension = 1024

	// TableSummaries 与 TableMessages 为 SQLite 契约冻结的集合表名。
	TableSummaries = "summaries_vectors"
	TableMessages  = "messages_vectors"

	// versionKeySummaries 与 versionKeyMessages 为 store_meta 中的集合版本号键。
	versionKeySummaries = "summaries_version"
	versionKeyMessages  = "messages_version"

	// deleteChunkSize 对齐 core/ai/vector_lite.py::_SQLITE_PARAM_CHUNK (500)，
	// 防止大批量删除突破 SQLite 单语句占位符上限。
	deleteChunkSize = 500

	// sqliteConstraintCode 对应 SQLite 主错误码 SQLITE_CONSTRAINT (19)。
	sqliteConstraintCode = 19
)

var (
	// ErrReadOnly 表示当前 Store 由 New() 以纯只读模式构造，拒绝任何写或建表操作。
	ErrReadOnly = errors.New("向量库处于只读模式")

	// ErrDuplicateID 表示 Insert 遇到主键冲突（对齐 LiteEngine.insert 的 IntegrityError）。
	ErrDuplicateID = errors.New("向量文档 ID 已存在")
)

const schemaDDL = `
CREATE TABLE IF NOT EXISTS store_meta (
    key   TEXT PRIMARY KEY,
    value INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS summaries_vectors (
    id            TEXT PRIMARY KEY,
    embedding     BLOB NOT NULL,
    document      TEXT NOT NULL,
    metadata_json TEXT NOT NULL,
    dim           INTEGER NOT NULL,
    stored_at     TEXT NOT NULL
) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS messages_vectors (
    id            TEXT PRIMARY KEY,
    embedding     BLOB NOT NULL,
    document      TEXT NOT NULL,
    metadata_json TEXT NOT NULL,
    dim           INTEGER NOT NULL,
    stored_at     TEXT NOT NULL
) WITHOUT ROWID;
INSERT OR IGNORE INTO store_meta (key, value) VALUES
    ('schema_version', 1),
    ('summaries_version', 1),
    ('messages_version', 1);
`

// WriteRow 表示批量写入（UpsertMany）的一行向量数据。
type WriteRow struct {
	ID       string         // 文档唯一 ID
	Vector   []float32      // float32 向量，长度必须等于 ExpectedDim
	Document string         // 原文内容
	Metadata map[string]any // 元数据字典（nil 视为空对象 {}）
}

// buildRWDSN 构造读写连接文件路径（斜杠归一化，连接级 PRAGMA 在建连时显式执行）。
func buildRWDSN(file string) string {
	return filepath.ToSlash(file)
}

// resolveCollection 将调用方传入的集合名映射为 (SQLite 表名, store_meta 版本键, 是否为总结表)。
func resolveCollection(collection string) (table, versionKey string, isSummaries bool, err error) {
	switch collection {
	case "summaries", TableSummaries:
		return TableSummaries, versionKeySummaries, true, nil
	case "messages", TableMessages:
		return TableMessages, versionKeyMessages, false, nil
	default:
		return "", "", false, fmt.Errorf("未知的 collection: %q", collection)
	}
}

// ensureRWConnLocked 确保读写 SQLite 单连接可用（须持有 s.mu）。
func (s *Store) ensureRWConnLocked(ctx context.Context) error {
	if s.readOnly {
		return ErrReadOnly
	}
	if s.db != nil {
		return nil
	}
	dir := filepath.Dir(s.dbFile)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建向量库目录失败 (%s): %w", dir, err)
	}
	db, err := sql.Open("sqlite", s.dsn)
	if err != nil {
		return fmt.Errorf("打开读写向量库失败: %w", err)
	}
	// 单写连接池：保证连接级 PRAGMA 与显式 BEGIN IMMEDIATE 事务始终落在同一底层连接上。
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)
	db.SetConnMaxIdleTime(0)

	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=5000",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA foreign_keys=OFF",
	}
	for _, p := range pragmas {
		if _, err := db.ExecContext(ctx, p); err != nil {
			_ = db.Close()
			return fmt.Errorf("设置 %s 失败: %w", p, err)
		}
	}
	s.db = db
	s.schemaReady = false
	return nil
}

// ensureRWReadyLocked 确保读写连接已建立且契约表与初始版本号已幂等就绪（须持有 s.mu）。
func (s *Store) ensureRWReadyLocked(ctx context.Context) error {
	if err := s.ensureRWConnLocked(ctx); err != nil {
		return err
	}
	if s.schemaReady {
		return nil
	}
	if _, err := s.db.ExecContext(ctx, schemaDDL); err != nil {
		return fmt.Errorf("初始化向量库表结构失败: %w", err)
	}
	s.schemaReady = true
	return nil
}

// EnsureSchema 幂等创建 store_meta、summaries_vectors、messages_vectors 并初始化版本号。
// 只读实例调用返回 ErrReadOnly。
func (s *Store) EnsureSchema(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.readOnly {
		return ErrReadOnly
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.runWithRetryLocked(ctx, func(ctx context.Context) error {
		return s.ensureRWReadyLocked(ctx)
	})
}

// isConstraintError 判断 err 是否为 SQLite 约束冲突（如主键重复），约束冲突属于确定性业务错误，不重试。
func isConstraintError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrDuplicateID) {
		return true
	}
	var sqErr *sqlite.Error
	if errors.As(err, &sqErr) && (sqErr.Code()&0xff) == sqliteConstraintCode {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "constraint")
}

// isNonRetryableError 过滤不应重建连接重试的确定性错误。
func isNonRetryableError(ctx context.Context, err error) bool {
	if err == nil {
		return true
	}
	if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	if errors.Is(err, ErrReadOnly) || errors.Is(err, ErrDimensionMismatch) {
		return true
	}
	return isConstraintError(err)
}

// runWithRetryLocked 在持有 s.mu 的前提下执行 SQLite 操作；遇到瞬态连接错误时关闭连接并重试一次
// （对齐 core/ai/vector_lite.py::LiteEngine._run_with_retry）。
func (s *Store) runWithRetryLocked(ctx context.Context, op func(context.Context) error) error {
	err := op(ctx)
	if err == nil || isNonRetryableError(ctx, err) {
		return err
	}
	slog.Warn("SQLite 操作失败，重建连接后重试一次", "error", err)
	_ = s.resetDBLocked()
	return op(ctx)
}

// encodeFloat32LE 将 float32 切片编码为小端字节序列（契约 BLOB 格式：<f4 × dim）。
func encodeFloat32LE(vec []float32) []byte {
	blob := make([]byte, len(vec)*4)
	for i, v := range vec {
		binary.LittleEndian.PutUint32(blob[i*4:], math.Float32bits(v))
	}
	return blob
}

// decodeFloat32LE 将小端 BLOB 解码为 float32 切片。
func decodeFloat32LE(blob []byte) ([]float32, error) {
	if len(blob)%4 != 0 {
		return nil, fmt.Errorf("非法向量 BLOB 长度: %d", len(blob))
	}
	n := len(blob) / 4
	out := make([]float32, n)
	for i := 0; i < n; i++ {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(blob[i*4:]))
	}
	return out, nil
}

// marshalMetadataJSON 将 metadata 序列化为不转义 HTML 字符的紧凑 JSON 对象文本。
func marshalMetadataJSON(metadata map[string]any) (string, map[string]any, error) {
	if metadata == nil {
		metadata = map[string]any{}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(metadata); err != nil {
		return "", nil, fmt.Errorf("序列化 metadata 失败: %w", err)
	}
	raw := strings.TrimRight(buf.String(), "\r\n")
	// 通过 decodeMetadata 归一类型（如 int -> float64），确保增量内存态与整表加载完全一致。
	decoded, err := decodeMetadata(raw)
	if err != nil {
		return "", nil, err
	}
	return raw, decoded, nil
}

// formatISOUTC 将时间格式化为与 Python datetime.now(UTC).isoformat() 一致的 UTC ISO 8601 字符串。
func formatISOUTC(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000000+00:00")
}

// safeRollback 在给定连接上尽力回滚活动事务。
func safeRollback(conn *sql.Conn) {
	_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
}

// readConnVersions 在指定连接上读取最新两集合版本号（事务提交后同步内存快照用）。
func readConnVersions(ctx context.Context, conn *sql.Conn) (versions, error) {
	var vers versions
	rows, err := conn.QueryContext(ctx,
		"SELECT key, value FROM store_meta WHERE key IN ('summaries_version','messages_version')")
	if err != nil {
		return vers, err
	}
	defer rows.Close()
	meta := map[string]int64{}
	for rows.Next() {
		var k string
		var v int64
		if err := rows.Scan(&k, &v); err != nil {
			return vers, err
		}
		meta[k] = v
	}
	if err := rows.Err(); err != nil {
		return vers, err
	}
	vers.sumVer = versionVal{set: metaKeyExists(meta, versionKeySummaries), val: meta[versionKeySummaries]}
	vers.msgVer = versionVal{set: metaKeyExists(meta, versionKeyMessages), val: meta[versionKeyMessages]}
	return vers, nil
}

// preparedWriteRow 保存已完成前置校验、JSON 编码与归一化的待写入行。
type preparedWriteRow struct {
	id           string
	blob         []byte
	normVec      []float32
	document     string
	metadataJSON string
	metadata     map[string]any
}

// prepareWriteRow 校验向量维度并编码单行。
func (s *Store) prepareWriteRow(row WriteRow) (preparedWriteRow, error) {
	if len(row.Vector) != s.expectedDim {
		return preparedWriteRow{}, fmt.Errorf(
			"%w: 期望 %d，实际 %d", ErrDimensionMismatch, s.expectedDim, len(row.Vector))
	}
	metaJSON, normMeta, err := marshalMetadataJSON(row.Metadata)
	if err != nil {
		return preparedWriteRow{}, err
	}
	return preparedWriteRow{
		id:           row.ID,
		blob:         encodeFloat32LE(row.Vector),
		normVec:      normalizeCopy(row.Vector),
		document:     row.Document,
		metadataJSON: metaJSON,
		metadata:     normMeta,
	}, nil
}

// Insert 插入单条向量记录（对齐 LiteEngine.insert）：
//   - 维度不符返回 ErrDimensionMismatch；
//   - 主键重复返回包装 ErrDuplicateID 的约束错误；
//   - BEGIN IMMEDIATE 事务内同步自增 store_meta 版本号，提交后增量更新内存快照。
func (s *Store) Insert(ctx context.Context, collection, docID string, vec []float32, document string, metadata map[string]any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.readOnly {
		return ErrReadOnly
	}
	table, versionKey, isSummaries, err := resolveCollection(collection)
	if err != nil {
		return err
	}
	prep, err := s.prepareWriteRow(WriteRow{
		ID:       docID,
		Vector:   vec,
		Document: document,
		Metadata: metadata,
	})
	if err != nil {
		return err
	}
	storedAt := formatISOUTC(time.Now())

	s.mu.Lock()
	defer s.mu.Unlock()

	return s.runWithRetryLocked(ctx, func(ctx context.Context) error {
		if err := s.ensureRWReadyLocked(ctx); err != nil {
			return err
		}
		conn, err := s.db.Conn(ctx)
		if err != nil {
			return err
		}
		defer conn.Close()

		if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
			return err
		}
		_, execErr := conn.ExecContext(ctx,
			"INSERT INTO "+table+" (id, embedding, document, metadata_json, dim, stored_at) VALUES (?, ?, ?, ?, ?, ?)",
			prep.id, prep.blob, prep.document, prep.metadataJSON, s.expectedDim, storedAt,
		)
		if execErr != nil {
			safeRollback(conn)
			if isConstraintError(execErr) {
				return fmt.Errorf("%w: %w", ErrDuplicateID, execErr)
			}
			return execErr
		}
		if _, err := conn.ExecContext(ctx,
			"UPDATE store_meta SET value = value + 1 WHERE key = ?", versionKey); err != nil {
			safeRollback(conn)
			return err
		}
		if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
			safeRollback(conn)
			return err
		}
		s.syncMemoryUpsertLocked(ctx, conn, table, isSummaries, []preparedWriteRow{prep})
		return nil
	})
}

// UpsertMany 单事务批量幂等写入向量记录（对齐 LiteEngine.upsert_many）：
//   - 开启事务前对全部行做全量维度校验，任一行不符立即返回 ErrDimensionMismatch，整批零写入；
//   - BEGIN IMMEDIATE 事务内 INSERT OR REPLACE 全部行并自增一次集合版本号；
//   - 提交后增量更新内存快照。
func (s *Store) UpsertMany(ctx context.Context, collection string, rows []WriteRow) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if s.readOnly {
		return 0, ErrReadOnly
	}
	table, versionKey, isSummaries, err := resolveCollection(collection)
	if err != nil {
		return 0, err
	}

	// 全量前置校验：任一行维度异常则整批拒绝，不触碰数据库事务。
	prepared := make([]preparedWriteRow, 0, len(rows))
	for _, r := range rows {
		prep, err := s.prepareWriteRow(r)
		if err != nil {
			return 0, err
		}
		prepared = append(prepared, prep)
	}
	if len(prepared) == 0 {
		s.mu.Lock()
		defer s.mu.Unlock()
		if err := s.runWithRetryLocked(ctx, func(ctx context.Context) error {
			return s.ensureRWReadyLocked(ctx)
		}); err != nil {
			return 0, err
		}
		return 0, nil
	}

	storedAt := formatISOUTC(time.Now())
	s.mu.Lock()
	defer s.mu.Unlock()

	err = s.runWithRetryLocked(ctx, func(ctx context.Context) error {
		if err := s.ensureRWReadyLocked(ctx); err != nil {
			return err
		}
		conn, err := s.db.Conn(ctx)
		if err != nil {
			return err
		}
		defer conn.Close()

		if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
			return err
		}
		stmt, err := conn.PrepareContext(ctx,
			"INSERT OR REPLACE INTO "+table+" (id, embedding, document, metadata_json, dim, stored_at) VALUES (?, ?, ?, ?, ?, ?)")
		if err != nil {
			safeRollback(conn)
			return err
		}
		for _, p := range prepared {
			if _, err := stmt.ExecContext(ctx,
				p.id, p.blob, p.document, p.metadataJSON, s.expectedDim, storedAt); err != nil {
				_ = stmt.Close()
				safeRollback(conn)
				return err
			}
		}
		if err := stmt.Close(); err != nil {
			safeRollback(conn)
			return err
		}
		if _, err := conn.ExecContext(ctx,
			"UPDATE store_meta SET value = value + 1 WHERE key = ?", versionKey); err != nil {
			safeRollback(conn)
			return err
		}
		if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
			safeRollback(conn)
			return err
		}
		s.syncMemoryUpsertLocked(ctx, conn, table, isSummaries, prepared)
		return nil
	})
	if err != nil {
		return 0, err
	}
	return len(prepared), nil
}

// Delete 幂等删除给定 ID 列表（对齐 LiteEngine.delete）：
//   - 按 500 个 ID 分块独立事务执行；
//   - 每块仅当实际删除行数 deleted > 0 时自增集合版本号并增量同步内存快照。
func (s *Store) Delete(ctx context.Context, collection string, ids []string) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if s.readOnly {
		return 0, ErrReadOnly
	}
	table, versionKey, isSummaries, err := resolveCollection(collection)
	if err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	totalDeleted := 0
	for start := 0; start < len(ids); start += deleteChunkSize {
		end := start + deleteChunkSize
		if end > len(ids) {
			end = len(ids)
		}
		chunk := ids[start:end]

		var chunkDeleted int
		err := s.runWithRetryLocked(ctx, func(ctx context.Context) error {
			chunkDeleted = 0
			if err := s.ensureRWReadyLocked(ctx); err != nil {
				return err
			}
			conn, err := s.db.Conn(ctx)
			if err != nil {
				return err
			}
			defer conn.Close()

			placeholders := buildPlaceholders(len(chunk))
			args := make([]any, len(chunk))
			for i, id := range chunk {
				args[i] = id
			}

			if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
				return err
			}
			res, err := conn.ExecContext(ctx,
				"DELETE FROM "+table+" WHERE id IN ("+placeholders+")", args...)
			if err != nil {
				safeRollback(conn)
				return err
			}
			affected, err := res.RowsAffected()
			if err != nil {
				safeRollback(conn)
				return err
			}
			chunkDeleted = int(affected)
			if chunkDeleted > 0 {
				if _, err := conn.ExecContext(ctx,
					"UPDATE store_meta SET value = value + 1 WHERE key = ?", versionKey); err != nil {
					safeRollback(conn)
					return err
				}
			}
			if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
				safeRollback(conn)
				return err
			}
			if chunkDeleted > 0 {
				s.syncMemoryRemoveLocked(ctx, conn, isSummaries, chunk)
			}
			return nil
		})
		if err != nil {
			return totalDeleted, err
		}
		totalDeleted += chunkDeleted
	}
	return totalDeleted, nil
}

// ClearCollection 清空指定集合中的所有向量记录，当实际删除行数 > 0 时自增版本号并清空内存快照。
func (s *Store) ClearCollection(ctx context.Context, collection string) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if s.readOnly {
		return 0, ErrReadOnly
	}
	table, versionKey, isSummaries, err := resolveCollection(collection)
	if err != nil {
		return 0, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	var deleted int
	err = s.runWithRetryLocked(ctx, func(ctx context.Context) error {
		deleted = 0
		if err := s.ensureRWReadyLocked(ctx); err != nil {
			return err
		}
		conn, err := s.db.Conn(ctx)
		if err != nil {
			return err
		}
		defer conn.Close()

		if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
			return err
		}
		res, err := conn.ExecContext(ctx, "DELETE FROM "+table)
		if err != nil {
			safeRollback(conn)
			return err
		}
		affected, err := res.RowsAffected()
		if err != nil {
			safeRollback(conn)
			return err
		}
		deleted = int(affected)
		if deleted > 0 {
			if _, err := conn.ExecContext(ctx,
				"UPDATE store_meta SET value = value + 1 WHERE key = ?", versionKey); err != nil {
				safeRollback(conn)
				return err
			}
		}
		if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
			safeRollback(conn)
			return err
		}
		if deleted > 0 {
			s.syncMemoryClearLocked(ctx, conn, table, isSummaries)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return deleted, nil
}

// buildPlaceholders 生成 n 个以逗号分隔的 "?" 占位符（n >= 1）。
func buildPlaceholders(n int) string {
	if n <= 0 {
		return ""
	}
	if n == 1 {
		return "?"
	}
	var b strings.Builder
	b.Grow(n*2 - 1)
	b.WriteByte('?')
	for i := 1; i < n; i++ {
		b.WriteString(",?")
	}
	return b.String()
}

// ────────────────────────── 写后内存快照增量同步（Copy-On-Write） ──────────────────────────

// invalidateSnapshotLocked 作废内存快照，使下次 Search 触发全量重载。
func (s *Store) invalidateSnapshotLocked() {
	empty := (*snapshot)(nil)
	s.current.Store(&empty)
}

// canApplyIncrementalLocked 校验当前快照是否恰好落后本次单步写事务一个版本号；
// 若期间有外部进程并发修改了库，则回退为作废快照由下次 Search 全量重载。
func canApplyIncrementalLocked(oldVers, newVers versions, isSummaries bool) bool {
	if !newVers.sumVer.set || !newVers.msgVer.set {
		return false
	}
	if isSummaries {
		return oldVers.msgVer == newVers.msgVer && newVers.sumVer.val == oldVers.sumVer.val+1
	}
	return oldVers.sumVer == newVers.sumVer && newVers.msgVer.val == oldVers.msgVer.val+1
}

// syncMemoryUpsertLocked 在写事务提交后以 Copy-On-Write 方式按 id 升序增量更新内存快照。
func (s *Store) syncMemoryUpsertLocked(ctx context.Context, conn *sql.Conn, table string, isSummaries bool, rows []preparedWriteRow) {
	cur := s.current.Load()
	if cur == nil || *cur == nil {
		// 尚未加载过内存矩阵：保持 nil，首次 Search 按需一次性加载。
		return
	}
	newVers, err := readConnVersions(ctx, conn)
	if err != nil || !canApplyIncrementalLocked((*cur).vers, newVers, isSummaries) {
		s.invalidateSnapshotLocked()
		return
	}
	prev := *cur
	var target *collectionData
	if isSummaries {
		target = prev.summaries
	} else {
		target = prev.messages
	}
	updated := cloneAndUpsertCollection(target, table, s.expectedDim, rows)
	next := &snapshot{
		vers:      newVers,
		summaries: prev.summaries,
		messages:  prev.messages,
	}
	if isSummaries {
		next.summaries = updated
	} else {
		next.messages = updated
	}
	s.current.Store(&next)
}

// syncMemoryRemoveLocked 在删除事务提交后以 Copy-On-Write 方式移除被删 ID。
func (s *Store) syncMemoryRemoveLocked(ctx context.Context, conn *sql.Conn, isSummaries bool, removedIDs []string) {
	cur := s.current.Load()
	if cur == nil || *cur == nil {
		return
	}
	newVers, err := readConnVersions(ctx, conn)
	if err != nil || !canApplyIncrementalLocked((*cur).vers, newVers, isSummaries) {
		s.invalidateSnapshotLocked()
		return
	}
	prev := *cur
	var target *collectionData
	if isSummaries {
		target = prev.summaries
	} else {
		target = prev.messages
	}
	updated := cloneAndRemoveCollection(target, removedIDs)
	next := &snapshot{
		vers:      newVers,
		summaries: prev.summaries,
		messages:  prev.messages,
	}
	if isSummaries {
		next.summaries = updated
	} else {
		next.messages = updated
	}
	s.current.Store(&next)
}

// syncMemoryClearLocked 在清空集合事务提交后将该集合替换为空矩阵。
func (s *Store) syncMemoryClearLocked(ctx context.Context, conn *sql.Conn, table string, isSummaries bool) {
	cur := s.current.Load()
	if cur == nil || *cur == nil {
		return
	}
	newVers, err := readConnVersions(ctx, conn)
	if err != nil || !canApplyIncrementalLocked((*cur).vers, newVers, isSummaries) {
		s.invalidateSnapshotLocked()
		return
	}
	prev := *cur
	cleared := &collectionData{name: table, dim: s.expectedDim}
	next := &snapshot{
		vers:      newVers,
		summaries: prev.summaries,
		messages:  prev.messages,
	}
	if isSummaries {
		next.summaries = cleared
	} else {
		next.messages = cleared
	}
	s.current.Store(&next)
}

// cloneAndUpsertCollection 返回应用 rows 后的新 collectionData（保持 ids 严格升序，与 ORDER BY id 等价）。
func cloneAndUpsertCollection(src *collectionData, table string, dim int, rows []preparedWriteRow) *collectionData {
	if src == nil {
		src = &collectionData{name: table, dim: dim}
	}
	effectiveDim := src.dim
	if effectiveDim == 0 {
		effectiveDim = dim
	}

	// 先对本批 rows 去重（同批内同 ID 以最后一次为准）并按 ID 升序排列。
	byID := make(map[string]preparedWriteRow, len(rows))
	for _, r := range rows {
		byID[r.id] = r
	}
	batchIDs := make([]string, 0, len(byID))
	for id := range byID {
		batchIDs = append(batchIDs, id)
	}
	sort.Strings(batchIDs)

	// 双指针有序归并 src（已有序）与 batchIDs（已有序），O(N + M) 一次分配完成。
	capHint := len(src.ids) + len(batchIDs)
	dst := &collectionData{
		name:      src.name,
		dim:       effectiveDim,
		vecs:      make([]float32, 0, capHint*effectiveDim),
		ids:       make([]string, 0, capHint),
		documents: make([]string, 0, capHint),
		metadatas: make([]map[string]any, 0, capHint),
		channels:  make([]string, 0, capHint),
		dates:     make([]time.Time, 0, capHint),
		hasDate:   make([]bool, 0, capHint),
	}

	appendPrepared := func(p preparedWriteRow) {
		dst.vecs = append(dst.vecs, p.normVec...)
		dst.ids = append(dst.ids, p.id)
		dst.documents = append(dst.documents, p.document)
		dst.metadatas = append(dst.metadatas, p.metadata)
		dst.channels = append(dst.channels, canonChannelID(p.metadata["channel_id"]))
		created, ok := parseISODateTime(metadataString(p.metadata, "created_at"))
		dst.dates = append(dst.dates, created)
		dst.hasDate = append(dst.hasDate, ok)
	}
	appendExisting := func(i int) {
		dst.vecs = append(dst.vecs, src.vecs[i*effectiveDim:(i+1)*effectiveDim]...)
		dst.ids = append(dst.ids, src.ids[i])
		dst.documents = append(dst.documents, src.documents[i])
		dst.metadatas = append(dst.metadatas, src.metadatas[i])
		dst.channels = append(dst.channels, src.channels[i])
		dst.dates = append(dst.dates, src.dates[i])
		dst.hasDate = append(dst.hasDate, src.hasDate[i])
	}

	i, j := 0, 0
	for i < len(src.ids) && j < len(batchIDs) {
		switch {
		case src.ids[i] < batchIDs[j]:
			appendExisting(i)
			i++
		case src.ids[i] > batchIDs[j]:
			appendPrepared(byID[batchIDs[j]])
			j++
		default: // 覆盖既有同 ID 行
			appendPrepared(byID[batchIDs[j]])
			i++
			j++
		}
	}
	for ; i < len(src.ids); i++ {
		appendExisting(i)
	}
	for ; j < len(batchIDs); j++ {
		appendPrepared(byID[batchIDs[j]])
	}
	return dst
}

// cloneAndRemoveCollection 返回剔除 removedIDs 后的新 collectionData（保持原升序）。
func cloneAndRemoveCollection(src *collectionData, removedIDs []string) *collectionData {
	if src == nil || len(src.ids) == 0 {
		if src == nil {
			return &collectionData{}
		}
		return &collectionData{name: src.name, dim: src.dim}
	}
	drop := make(map[string]struct{}, len(removedIDs))
	for _, id := range removedIDs {
		drop[id] = struct{}{}
	}
	dim := src.dim
	dst := &collectionData{
		name:      src.name,
		dim:       dim,
		vecs:      make([]float32, 0, len(src.ids)*dim),
		ids:       make([]string, 0, len(src.ids)),
		documents: make([]string, 0, len(src.ids)),
		metadatas: make([]map[string]any, 0, len(src.ids)),
		channels:  make([]string, 0, len(src.ids)),
		dates:     make([]time.Time, 0, len(src.ids)),
		hasDate:   make([]bool, 0, len(src.ids)),
	}
	for i, id := range src.ids {
		if _, ok := drop[id]; ok {
			continue
		}
		dst.vecs = append(dst.vecs, src.vecs[i*dim:(i+1)*dim]...)
		dst.ids = append(dst.ids, id)
		dst.documents = append(dst.documents, src.documents[i])
		dst.metadatas = append(dst.metadatas, src.metadatas[i])
		dst.channels = append(dst.channels, src.channels[i])
		dst.dates = append(dst.dates, src.dates[i])
		dst.hasDate = append(dst.hasDate, src.hasDate[i])
	}
	return dst
}
