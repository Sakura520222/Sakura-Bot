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
	"maps"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	defaultVectorDBDir       = "data/vectors"
	defaultMigrationMarker   = "data/.vector_migration_done"
	chromaSQLiteFileName     = "chroma.sqlite3"
	migrateSampleSize        = 10
	migrateCosineThreshold   = 0.999999
	chromaDocMetaKey         = "chroma:document"
	hnswHeaderVersionedBytes = 100
	hnswHeaderLegacyBytes    = 96
)

// MigrateOptions 配置纯 Go ChromaDB -> SQLite(WAL) 一次性迁移参数
// （行为对齐 scripts/migrate_vectors_to_sqlite.py）。
type MigrateOptions struct {
	// VectorDBPath 为源 chroma.sqlite3 所在目录，同时决定输出 lite_index.sqlite3 位置
	//（为空时默认 "data/vectors"）。
	VectorDBPath string

	// MarkerPath 为迁移完成幂等标记文件路径（为空时：若 VectorDBPath 为默认目录则取
	// "data/.vector_migration_done"，否则取 "{VectorDBPath}/.vector_migration_done"）。
	MarkerPath string

	// ExpectedDim 为目标向量维度（<=0 时默认 DefaultDimension = 1024）。
	ExpectedDim int

	// Force 为 true 时忽略已存在的幂等标记强制重跑。
	Force bool

	// AssumeStopped 为 true 时跳过停机窗口校验（chroma.sqlite3 伴生文件与写锁探测）。
	AssumeStopped bool

	// AllowEmpty 为 true 时允许源 Chroma 的 summaries 与 messages 均为空（0 行）时继续。
	AllowEmpty bool

	// VerifyOnly 为 true 时仅校验已存在的 lite_index.sqlite3 与源 Chroma 一致性，不执行写入。
	VerifyOnly bool

	// FallbackEmbed 可选兜底嵌入器：当某段仅存有文档正文而无可用队列/HNSW 向量时重算向量。
	FallbackEmbed EmbedFunc
}

// MigrateReport 汇总 ChromaDB -> SQLite(WAL) 迁移或校验报告。
type MigrateReport struct {
	Skipped           bool   `json:"skipped"`
	Verified          bool   `json:"verified"`
	SummariesMigrated int    `json:"summaries_migrated"`
	MessagesMigrated  int    `json:"messages_migrated"`
	FallbackEmbedded  int    `json:"fallback_embedded"`
	OutputDBPath      string `json:"output_db_path"`
	MarkerPath        string `json:"marker_path"`
}

type chromaMigratedRow struct {
	ID       string
	Vector   []float32
	Document string
	Metadata map[string]any
}

// MigrateChromaToSQLite 执行纯 Go ChromaDB -> SQLite(WAL) 迁移（防损坏顺序逐项对齐 Python 脚本）：
//  1. 检查幂等标记（Force=false 时已存在即直接跳过返回 Skipped=true）；
//  2. 硬校验停机窗口（chroma.sqlite3 的 -wal/-shm/-journal 伴生文件与写锁探测，AssumeStopped 可跳过）；
//  3. 只读打开 chroma.sqlite3，提取 summaries/messages 集合的文档、元数据与向量（合并 HNSW 段与 WAL 队列）；
//  4. 写入 lite_index.sqlite3.tmp（WAL + synchronous=FULL），提交后执行 PRAGMA wal_checkpoint(TRUNCATE)；
//  5. 校验目标行数、全量维度与随机抽样余弦相似度（> 0.999999）；
//  6. 清理并断言无 .tmp-wal/.tmp-shm 伴生文件残留，清理旧目标伴生文件后原子 Rename 为 lite_index.sqlite3；
//  7. 写入幂等标记文件。
func MigrateChromaToSQLite(ctx context.Context, opts MigrateOptions) (*MigrateReport, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	vectorDBPath := strings.TrimSpace(opts.VectorDBPath)
	defaultDirUsed := false
	if vectorDBPath == "" {
		vectorDBPath = defaultVectorDBDir
		defaultDirUsed = true
	} else if filepath.Clean(vectorDBPath) == filepath.Clean(defaultVectorDBDir) {
		defaultDirUsed = true
	}

	markerPath := strings.TrimSpace(opts.MarkerPath)
	if markerPath == "" {
		if defaultDirUsed {
			markerPath = filepath.FromSlash(defaultMigrationMarker)
		} else {
			markerPath = filepath.Join(vectorDBPath, ".vector_migration_done")
		}
	}

	expectedDim := opts.ExpectedDim
	if expectedDim <= 0 {
		expectedDim = DefaultDimension
	}

	finalDB := filepath.Join(vectorDBPath, dbFileName)
	report := &MigrateReport{
		OutputDBPath: finalDB,
		MarkerPath:   markerPath,
	}

	if !opts.VerifyOnly && !opts.Force {
		if fi, err := os.Stat(markerPath); err == nil && !fi.IsDir() {
			report.Skipped = true
			return report, nil
		}
	}

	chromaDBPath := filepath.Join(vectorDBPath, chromaSQLiteFileName)
	if fi, err := os.Stat(chromaDBPath); err != nil || fi.IsDir() {
		return nil, fmt.Errorf("未找到源 Chroma 数据库: %s", chromaDBPath)
	}

	if !opts.VerifyOnly {
		if err := assertChromaShutdownWindow(ctx, chromaDBPath, opts.AssumeStopped); err != nil {
			return nil, err
		}
	}

	summariesRows, sumFallback, err := extractChromaCollection(ctx, vectorDBPath, chromaDBPath, "summaries", expectedDim, opts.FallbackEmbed)
	if err != nil {
		return nil, fmt.Errorf("提取 Chroma summaries 集合失败: %w", err)
	}
	messagesRows, msgFallback, err := extractChromaCollection(ctx, vectorDBPath, chromaDBPath, "messages", expectedDim, opts.FallbackEmbed)
	if err != nil {
		return nil, fmt.Errorf("提取 Chroma messages 集合失败: %w", err)
	}
	report.FallbackEmbedded = sumFallback + msgFallback

	if opts.VerifyOnly {
		if fi, err := os.Stat(finalDB); err != nil || fi.IsDir() {
			return nil, fmt.Errorf("待校验的 lite 向量库不存在: %s", finalDB)
		}
		if err := verifyMigratedDB(ctx, finalDB, expectedDim, summariesRows, messagesRows); err != nil {
			return nil, err
		}
		report.Verified = true
		report.SummariesMigrated = len(summariesRows)
		report.MessagesMigrated = len(messagesRows)
		return report, nil
	}

	if len(summariesRows) == 0 && len(messagesRows) == 0 && !opts.AllowEmpty {
		return nil, errors.New("源 Chroma 的 summaries 与 messages 集合均为空（0 行）；若确需空库迁移请设置 AllowEmpty=true")
	}

	if err := os.MkdirAll(vectorDBPath, 0o755); err != nil {
		return nil, fmt.Errorf("创建目标向量库目录失败: %w", err)
	}

	tmpDB := finalDB + ".tmp"
	removeSQLiteFamily(tmpDB)

	if err := writeTempLiteDB(ctx, tmpDB, expectedDim, summariesRows, messagesRows); err != nil {
		removeSQLiteFamily(tmpDB)
		return nil, err
	}

	if err := verifyMigratedDB(ctx, tmpDB, expectedDim, summariesRows, messagesRows); err != nil {
		return nil, fmt.Errorf("迁移后数据一致性校验失败（保留临时文件 %s 供排查）: %w", tmpDB, err)
	}

	// 清除只读校验可能遗留的空会话伴生文件，随后硬断言无伴生文件残留。
	removeFileIgnoreMissing(tmpDB + "-wal")
	removeFileIgnoreMissing(tmpDB + "-shm")
	if err := assertNoCompanionFiles(tmpDB); err != nil {
		return nil, err
	}

	// 先删除旧目标库及其伴生文件（防止旧 WAL 帧重放进新库），再原子改名。
	removeSQLiteFamily(finalDB)
	if err := os.Rename(tmpDB, finalDB); err != nil {
		return nil, fmt.Errorf("原子替换目标向量库失败: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(markerPath), 0o755); err != nil {
		return nil, fmt.Errorf("创建幂等标记目录失败: %w", err)
	}
	markerContent := formatISOUTC(time.Now().UTC()) + "\n"
	if err := os.WriteFile(markerPath, []byte(markerContent), 0o644); err != nil {
		return nil, fmt.Errorf("写入迁移幂等标记失败: %w", err)
	}

	report.Verified = true
	report.SummariesMigrated = len(summariesRows)
	report.MessagesMigrated = len(messagesRows)
	return report, nil
}

// assertChromaShutdownWindow 硬校验停机窗口：检查伴生文件并探测写锁。
func assertChromaShutdownWindow(ctx context.Context, chromaDBPath string, assumeStopped bool) error {
	var companions []string
	for _, suffix := range []string{"-journal", "-wal", "-shm"} {
		if _, err := os.Stat(chromaDBPath + suffix); err == nil {
			companions = append(companions, suffix)
		}
	}
	if len(companions) > 0 && !assumeStopped {
		return fmt.Errorf(
			"检测到 %s 存在活动伴生文件 %v，疑似仍有进程持有 Chroma 库；请停机后重试或设置 AssumeStopped=true",
			chromaDBPath, companions,
		)
	}
	if len(companions) > 0 {
		return nil
	}

	probeDB, err := sql.Open("sqlite", buildRWDSN(chromaDBPath))
	if err != nil {
		if assumeStopped {
			return nil
		}
		return fmt.Errorf("探测 Chroma 写锁时打开连接失败: %w", err)
	}
	probeDB.SetMaxOpenConns(1)
	defer probeDB.Close()

	conn, err := probeDB.Conn(ctx)
	if err != nil {
		if assumeStopped {
			return nil
		}
		return fmt.Errorf("探测 Chroma 写锁获取连接失败: %w", err)
	}
	defer conn.Close()

	_, _ = conn.ExecContext(ctx, "PRAGMA busy_timeout=1000")
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		if assumeStopped {
			return nil
		}
		return fmt.Errorf("Chroma 主库写锁探测失败（疑似仍有进程活跃）: %w", err)
	}
	_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
	return nil
}

// assertNoCompanionFiles 断言指定 SQLite 主文件不存在 -wal/-shm 伴生文件残留。
func assertNoCompanionFiles(dbPath string) error {
	var leftovers []string
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(dbPath + suffix); err == nil {
			leftovers = append(leftovers, suffix)
		}
	}
	if len(leftovers) > 0 {
		return fmt.Errorf("%s 换名前仍存在未合并伴生文件 %v，拒绝替换", dbPath, leftovers)
	}
	return nil
}

func removeFileIgnoreMissing(path string) {
	err := os.Remove(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		// 尽力删除，后续断言或 Rename 会捕获真正的占用错误
	}
}

func removeSQLiteFamily(dbPath string) {
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		removeFileIgnoreMissing(dbPath + suffix)
	}
}

// ────────────────────────── Chroma SQLite + HNSW 提取 ──────────────────────────

type chromaLiveEmbedding struct {
	rowID    int64
	docID    string
	seqID    int64
	document string
	metadata map[string]any
}

type chromaQueueState struct {
	seqID    int64
	deleted  bool
	vector   []float32
	document string
	metadata map[string]any
}

// extractChromaCollection 从只读 chroma.sqlite3 与对应 HNSW 段目录提取单个集合的全部记录。
func extractChromaCollection(
	ctx context.Context,
	vectorDBPath, chromaDBPath, collectionName string,
	expectedDim int,
	fallbackEmbed EmbedFunc,
) (map[string]chromaMigratedRow, int, error) {
	db, err := sql.Open("sqlite", buildDSN(chromaDBPath))
	if err != nil {
		return nil, 0, err
	}
	db.SetMaxOpenConns(1)
	defer db.Close()

	hasCollections, err := sqliteTableExists(ctx, db, "collections")
	if err != nil {
		return nil, 0, err
	}
	if !hasCollections {
		return map[string]chromaMigratedRow{}, 0, nil
	}

	collCols, err := sqliteTableColumns(ctx, db, "collections")
	if err != nil {
		return nil, 0, err
	}
	var collectionID, collectionTopic string
	if collCols["topic"] {
		err = db.QueryRowContext(ctx,
			"SELECT id, COALESCE(topic, '') FROM collections WHERE name = ? LIMIT 1", collectionName,
		).Scan(&collectionID, &collectionTopic)
	} else {
		err = db.QueryRowContext(ctx,
			"SELECT id FROM collections WHERE name = ? LIMIT 1", collectionName,
		).Scan(&collectionID)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return map[string]chromaMigratedRow{}, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}

	metadataSegID, vectorSegID, segTopic, err := findCollectionSegments(ctx, db, collectionID)
	if err != nil {
		return nil, 0, err
	}
	if collectionTopic == "" {
		collectionTopic = segTopic
	}

	liveList, metaMaxSeq, err := readChromaLiveMetadata(ctx, db, metadataSegID, collectionID)
	if err != nil {
		return nil, 0, err
	}

	vecMaxSeq, _ := readChromaMaxSeqID(ctx, db, vectorSegID)
	queueMap, queueOrder, err := readChromaEmbeddingsQueue(ctx, db, collectionID, collectionTopic)
	if err != nil {
		return nil, 0, err
	}

	// 若 Chroma >= 0.5 存在尚未合并进 embeddings 元数据表的 WAL 队列记录，按 seq_id 叠加。
	liveByID := make(map[string]*chromaLiveEmbedding, len(liveList)+len(queueOrder))
	orderedIDs := make([]string, 0, len(liveList)+len(queueOrder))
	for i := range liveList {
		item := liveList[i]
		liveByID[item.docID] = &item
		orderedIDs = append(orderedIDs, item.docID)
	}
	for _, docID := range queueOrder {
		qs := queueMap[docID]
		existing, exists := liveByID[docID]
		if qs.deleted {
			if exists && (metaMaxSeq == 0 || qs.seqID > metaMaxSeq) {
				delete(liveByID, docID)
			}
			continue
		}
		if !exists && (len(liveList) == 0 || qs.seqID > metaMaxSeq) {
			metaCopy := map[string]any{}
			if qs.metadata != nil {
				maps.Copy(metaCopy, qs.metadata)
			}
			liveByID[docID] = &chromaLiveEmbedding{
				docID:    docID,
				seqID:    qs.seqID,
				document: qs.document,
				metadata: metaCopy,
			}
			orderedIDs = append(orderedIDs, docID)
		} else if exists && qs.seqID > metaMaxSeq && qs.seqID > existing.seqID {
			if qs.document != "" {
				existing.document = qs.document
			}
			if len(qs.metadata) > 0 {
				maps.Copy(existing.metadata, qs.metadata)
			}
			existing.seqID = qs.seqID
		}
	}

	if len(liveByID) == 0 {
		return map[string]chromaMigratedRow{}, 0, nil
	}

	// 解析 HNSW 段文件（若存在）。
	var hnswSeg *parsedHNSWSegment
	if vectorSegID != "" {
		segDir := filepath.Join(vectorDBPath, vectorSegID)
		if fi, statErr := os.Stat(segDir); statErr == nil && fi.IsDir() {
			parsed, parseErr := readHNSWSegmentDir(segDir)
			if parseErr == nil {
				hnswSeg = parsed
			}
		}
	}

	result := make(map[string]chromaMigratedRow, len(liveByID))
	var missingIDs []string
	var missingTexts []string

	activeOrdinal := 0
	for _, docID := range orderedIDs {
		rec, ok := liveByID[docID]
		if !ok {
			continue
		}

		var vec []float32
		// 1) WAL 队列中尚未刷入 HNSW（或更新于 HNSW max_seq_id）的向量优先。
		if qs, hasQ := queueMap[docID]; hasQ && !qs.deleted && len(qs.vector) > 0 {
			if hnswSeg == nil || vecMaxSeq == 0 || qs.seqID > vecMaxSeq {
				vec = qs.vector
			}
		}
		// 2) HNSW data_level0.bin 中的持久化向量。
		if len(vec) == 0 && hnswSeg != nil {
			vec = hnswSeg.lookupVector(rec.docID, rec.seqID, rec.rowID, activeOrdinal, len(liveByID))
		}
		// 3) 兜底：队列中虽已落盘但仍保留的同 ID 向量。
		if len(vec) == 0 {
			if qs, hasQ := queueMap[docID]; hasQ && !qs.deleted && len(qs.vector) > 0 {
				vec = qs.vector
			}
		}

		meta := rec.metadata
		if meta == nil {
			meta = map[string]any{}
		}
		row := chromaMigratedRow{
			ID:       rec.docID,
			Vector:   vec,
			Document: rec.document,
			Metadata: meta,
		}
		result[rec.docID] = row
		if len(vec) == 0 {
			missingIDs = append(missingIDs, rec.docID)
			missingTexts = append(missingTexts, rec.document)
		}
		activeOrdinal++
	}

	fallbackCount := 0
	if len(missingIDs) > 0 {
		if fallbackEmbed == nil {
			return nil, 0, fmt.Errorf(
				"集合 %s 中有 %d 条文档（如 %q）缺失可用向量且未提供 FallbackEmbed",
				collectionName, len(missingIDs), missingIDs[0],
			)
		}
		computed, err := fallbackEmbed(ctx, missingTexts)
		if err != nil {
			return nil, 0, fmt.Errorf("FallbackEmbed 重算集合 %s 向量失败: %w", collectionName, err)
		}
		if len(computed) != len(missingIDs) {
			return nil, 0, fmt.Errorf(
				"FallbackEmbed 返回向量数不匹配: 期望 %d，实际 %d", len(missingIDs), len(computed))
		}
		for i, id := range missingIDs {
			r := result[id]
			r.Vector = computed[i]
			result[id] = r
		}
		fallbackCount = len(missingIDs)
	}

	for id, r := range result {
		if len(r.Vector) != expectedDim {
			return nil, 0, fmt.Errorf(
				"%w: 集合 %s 文档 %q 向量维度 %d != 期望维度 %d",
				ErrDimensionMismatch, collectionName, id, len(r.Vector), expectedDim,
			)
		}
	}
	return result, fallbackCount, nil
}

// findCollectionSegments 查找指定 collection 关联的 METADATA 段与 VECTOR 段 ID。
func findCollectionSegments(ctx context.Context, db *sql.DB, collectionID string) (metaSegID, vecSegID, topic string, err error) {
	hasSegments, err := sqliteTableExists(ctx, db, "segments")
	if err != nil || !hasSegments {
		return "", "", "", err
	}
	segCols, err := sqliteTableColumns(ctx, db, "segments")
	if err != nil {
		return "", "", "", err
	}

	query := "SELECT id, type, scope, '' FROM segments WHERE collection = ?"
	if segCols["topic"] {
		query = "SELECT id, type, scope, COALESCE(topic, '') FROM segments WHERE collection = ?"
	}
	rows, err := db.QueryContext(ctx, query, collectionID)
	if err != nil {
		return "", "", "", err
	}
	defer rows.Close()

	for rows.Next() {
		var id, segType, scope, segTopic string
		if err := rows.Scan(&id, &segType, &scope, &segTopic); err != nil {
			return "", "", "", err
		}
		if topic == "" && segTopic != "" {
			topic = segTopic
		}
		upperScope := strings.ToUpper(scope)
		lowerType := strings.ToLower(segType)
		switch {
		case upperScope == "METADATA" || strings.Contains(lowerType, "metadata") || strings.Contains(lowerType, "sqlite"):
			metaSegID = id
		case upperScope == "VECTOR" || strings.Contains(lowerType, "vector") || strings.Contains(lowerType, "hnsw"):
			vecSegID = id
		}
	}
	return metaSegID, vecSegID, topic, rows.Err()
}

// readChromaLiveMetadata 读取 embeddings 表与 embedding_metadata 表中的活动文档及全类型元数据。
func readChromaLiveMetadata(ctx context.Context, db *sql.DB, metaSegID, collectionID string) ([]chromaLiveEmbedding, int64, error) {
	hasEmbeddings, err := sqliteTableExists(ctx, db, "embeddings")
	if err != nil || !hasEmbeddings {
		return nil, 0, err
	}

	targetSeg := metaSegID
	if targetSeg == "" {
		targetSeg = collectionID
	}
	maxSeq, _ := readChromaMaxSeqID(ctx, db, targetSeg)

	rows, err := db.QueryContext(ctx,
		"SELECT id, embedding_id, seq_id FROM embeddings WHERE segment_id = ? ORDER BY id ASC", targetSeg)
	if err != nil {
		return nil, 0, err
	}

	var list []chromaLiveEmbedding
	byRowID := map[int64]int{}
	for rows.Next() {
		var (
			rowID  int64
			docID  string
			rawSeq any
		)
		if err := rows.Scan(&rowID, &docID, &rawSeq); err != nil {
			rows.Close()
			return nil, 0, err
		}
		seq := decodeChromaSeqID(rawSeq)
		if seq > maxSeq {
			maxSeq = seq
		}
		byRowID[rowID] = len(list)
		list = append(list, chromaLiveEmbedding{
			rowID:    rowID,
			docID:    docID,
			seqID:    seq,
			metadata: map[string]any{},
		})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, 0, err
	}
	rows.Close()

	if len(list) == 0 {
		return nil, maxSeq, nil
	}

	hasMeta, err := sqliteTableExists(ctx, db, "embedding_metadata")
	if err != nil || !hasMeta {
		return list, maxSeq, err
	}
	metaCols, err := sqliteTableColumns(ctx, db, "embedding_metadata")
	if err != nil {
		return nil, 0, err
	}
	hasBool := metaCols["bool_value"]

	metaSQL := `
		SELECT em.id, em.key, em.string_value, em.int_value, em.float_value
		FROM embedding_metadata em
		JOIN embeddings e ON em.id = e.id
		WHERE e.segment_id = ?`
	if hasBool {
		metaSQL = `
		SELECT em.id, em.key, em.string_value, em.int_value, em.float_value, em.bool_value
		FROM embedding_metadata em
		JOIN embeddings e ON em.id = e.id
		WHERE e.segment_id = ?`
	}

	mRows, err := db.QueryContext(ctx, metaSQL, targetSeg)
	if err != nil {
		return nil, 0, err
	}
	defer mRows.Close()

	for mRows.Next() {
		var (
			rowID   int64
			key     string
			strVal  sql.NullString
			intVal  sql.NullInt64
			fltVal  sql.NullFloat64
			boolVal sql.NullInt64
		)
		if hasBool {
			err = mRows.Scan(&rowID, &key, &strVal, &intVal, &fltVal, &boolVal)
		} else {
			err = mRows.Scan(&rowID, &key, &strVal, &intVal, &fltVal)
		}
		if err != nil {
			return nil, 0, err
		}
		idx, ok := byRowID[rowID]
		if !ok {
			continue
		}
		if key == chromaDocMetaKey {
			if strVal.Valid {
				list[idx].document = strVal.String
			}
			continue
		}
		if strings.HasPrefix(key, "chroma:") {
			continue
		}
		switch {
		case hasBool && boolVal.Valid:
			list[idx].metadata[key] = (boolVal.Int64 != 0)
		case strVal.Valid:
			list[idx].metadata[key] = strVal.String
		case intVal.Valid:
			list[idx].metadata[key] = intVal.Int64
		case fltVal.Valid:
			list[idx].metadata[key] = fltVal.Float64
		}
	}
	if err := mRows.Err(); err != nil {
		return nil, 0, err
	}
	return list, maxSeq, nil
}

// readChromaMaxSeqID 查询 max_seq_id 表中某 segment 已消费的最大序号。
func readChromaMaxSeqID(ctx context.Context, db *sql.DB, segmentID string) (int64, error) {
	if segmentID == "" {
		return 0, nil
	}
	ok, err := sqliteTableExists(ctx, db, "max_seq_id")
	if err != nil || !ok {
		return 0, err
	}
	var raw any
	if err := db.QueryRowContext(ctx, "SELECT seq_id FROM max_seq_id WHERE segment_id = ?", segmentID).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, nil
		}
		return 0, err
	}
	return decodeChromaSeqID(raw), nil
}

// readChromaEmbeddingsQueue 读取 Chroma >= 0.5 的 embeddings_queue WAL 表并折叠为每文档最新状态。
func readChromaEmbeddingsQueue(ctx context.Context, db *sql.DB, collectionID, collectionTopic string) (map[string]chromaQueueState, []string, error) {
	out := map[string]chromaQueueState{}
	hasQueue, err := sqliteTableExists(ctx, db, "embeddings_queue")
	if err != nil || !hasQueue {
		return out, nil, err
	}

	rows, err := db.QueryContext(ctx,
		"SELECT seq_id, operation, topic, id, vector, encoding, metadata FROM embeddings_queue ORDER BY seq_id ASC")
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	var order []string
	seen := map[string]bool{}

	for rows.Next() {
		var (
			rawSeq   any
			op       int
			topic    string
			docID    string
			vecBlob  []byte
			encoding sql.NullString
			metaRaw  sql.NullString
		)
		if err := rows.Scan(&rawSeq, &op, &topic, &docID, &vecBlob, &encoding, &metaRaw); err != nil {
			return nil, nil, err
		}
		if !queueTopicMatches(topic, collectionID, collectionTopic) {
			continue
		}
		seq := decodeChromaSeqID(rawSeq)
		if !seen[docID] {
			seen[docID] = true
			order = append(order, docID)
		}

		// Chroma Operation 枚举：0=ADD, 1=UPDATE, 2=UPSERT, 3=DELETE
		if op == 3 {
			out[docID] = chromaQueueState{seqID: seq, deleted: true}
			continue
		}

		prev := out[docID]
		if prev.deleted {
			prev = chromaQueueState{}
		}
		prev.seqID = seq
		prev.deleted = false

		if len(vecBlob) > 0 {
			vec, decErr := decodeQueueVector(vecBlob, encoding.String)
			if decErr != nil {
				return nil, nil, fmt.Errorf("解码 embeddings_queue 向量失败 (id=%s): %w", docID, decErr)
			}
			prev.vector = vec
		}
		if metaRaw.Valid && metaRaw.String != "" {
			var parsed map[string]any
			if json.Unmarshal([]byte(metaRaw.String), &parsed) == nil && parsed != nil {
				if prev.metadata == nil {
					prev.metadata = map[string]any{}
				}
				for k, v := range parsed {
					if k == chromaDocMetaKey {
						if s, ok := v.(string); ok {
							prev.document = s
						}
						continue
					}
					if strings.HasPrefix(k, "chroma:") {
						continue
					}
					if v == nil {
						delete(prev.metadata, k)
					} else {
						prev.metadata[k] = v
					}
				}
			}
		}
		out[docID] = prev
	}
	return out, order, rows.Err()
}

func queueTopicMatches(topic, collectionID, collectionTopic string) bool {
	if collectionTopic != "" && topic == collectionTopic {
		return true
	}
	if topic == collectionID || strings.HasSuffix(topic, "/"+collectionID) {
		return true
	}
	return false
}

func decodeQueueVector(blob []byte, encoding string) ([]float32, error) {
	enc := strings.ToUpper(strings.TrimSpace(encoding))
	switch enc {
	case "", "FLOAT32":
		return decodeFloat32LE(blob)
	case "FLOAT64":
		if len(blob)%8 != 0 {
			return nil, fmt.Errorf("非法 FLOAT64 向量 BLOB 长度: %d", len(blob))
		}
		n := len(blob) / 8
		out := make([]float32, n)
		for i := 0; i < n; i++ {
			out[i] = float32(math.Float64frombits(binary.LittleEndian.Uint64(blob[i*8:])))
		}
		return out, nil
	default:
		return nil, fmt.Errorf("不支持的队列向量编码格式: %q", encoding)
	}
}

// decodeChromaSeqID 兼容整数与旧版 Chroma 大端字节序 BLOB 两种 seq_id 表示。
func decodeChromaSeqID(raw any) int64 {
	switch v := raw.(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case string:
		n, _ := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		return n
	case []byte:
		if len(v) == 8 {
			return int64(binary.BigEndian.Uint64(v))
		}
		if len(v) == 24 {
			// Chroma 0.4 Python int pickle/big-endian 24-byte seq_id 取低 8 字节。
			return int64(binary.BigEndian.Uint64(v[16:24]))
		}
		var acc int64
		for _, b := range v {
			acc = (acc << 8) | int64(b)
		}
		return acc
	default:
		return 0
	}
}

func sqliteTableExists(ctx context.Context, db *sql.DB, tableName string) (bool, error) {
	var name string
	err := db.QueryRowContext(ctx,
		"SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?", tableName).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func sqliteTableColumns(ctx context.Context, db *sql.DB, tableName string) (map[string]bool, error) {
	rows, err := db.QueryContext(ctx, "PRAGMA table_info("+tableName+")")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cols := map[string]bool{}
	for rows.Next() {
		var (
			cid     int
			name    string
			colType string
			notnull int
			dfltVal any
			pk      int
		)
		if err := rows.Scan(&cid, &name, &colType, &notnull, &dfltVal, &pk); err != nil {
			return nil, err
		}
		cols[name] = true
	}
	return cols, rows.Err()
}

// ────────────────────────── 纯 Go HNSW 段与 Pickle 元数据解析 ──────────────────────────

type hnswHeader struct {
	maxElements        uint64
	curElementCount    uint64
	sizeDataPerElement uint64
	labelOffset        uint64
	offsetData         uint64
}

func (h hnswHeader) valid(dataLen int) bool {
	if h.sizeDataPerElement < 12 || h.labelOffset <= h.offsetData {
		return false
	}
	if (h.labelOffset-h.offsetData)%4 != 0 {
		return false
	}
	if h.labelOffset+8 > h.sizeDataPerElement {
		return false
	}
	if h.curElementCount > h.maxElements {
		return false
	}
	if h.curElementCount > 0 && uint64(dataLen) < h.curElementCount*h.sizeDataPerElement {
		return false
	}
	return true
}

func parseHNSWHeader(hdr []byte, dataLen int) (hnswHeader, error) {
	parseAt := func(base int) hnswHeader {
		return hnswHeader{
			maxElements:        binary.LittleEndian.Uint64(hdr[base+8 : base+16]),
			curElementCount:    binary.LittleEndian.Uint64(hdr[base+16 : base+24]),
			sizeDataPerElement: binary.LittleEndian.Uint64(hdr[base+24 : base+32]),
			labelOffset:        binary.LittleEndian.Uint64(hdr[base+32 : base+40]),
			offsetData:         binary.LittleEndian.Uint64(hdr[base+40 : base+48]),
		}
	}
	// chroma-hnswlib 持久化头在起始处多 4 字节 uint32 版本号（共 100 字节）。
	if len(hdr) >= hnswHeaderVersionedBytes {
		if h := parseAt(4); h.valid(dataLen) {
			return h, nil
		}
	}
	if len(hdr) >= hnswHeaderLegacyBytes {
		if h := parseAt(0); h.valid(dataLen) {
			return h, nil
		}
	}
	return hnswHeader{}, fmt.Errorf("无法解析 HNSW header.bin (len=%d, data_level0=%d)", len(hdr), dataLen)
}

type parsedHNSWSegment struct {
	dim        int
	labelToVec map[uint64][]float32
	elemVecs   [][]float32
	idToLabel  map[string]uint64
	hasIDMap   bool
}

func (s *parsedHNSWSegment) lookupVector(docID string, seqID, rowID int64, ordinal, totalLive int) []float32 {
	if s.hasIDMap {
		if lbl, ok := s.idToLabel[docID]; ok {
			return s.labelToVec[lbl]
		}
		return nil
	}
	if seqID > 0 {
		if v, ok := s.labelToVec[uint64(seqID)]; ok {
			return v
		}
	}
	if rowID > 0 {
		if v, ok := s.labelToVec[uint64(rowID)]; ok {
			return v
		}
	}
	if totalLive == len(s.elemVecs) && ordinal >= 0 && ordinal < len(s.elemVecs) {
		if v, ok := s.labelToVec[uint64(ordinal)]; ok {
			return v
		}
		return s.elemVecs[ordinal]
	}
	return nil
}

func readHNSWSegmentDir(segDir string) (*parsedHNSWSegment, error) {
	hdrBytes, err := os.ReadFile(filepath.Join(segDir, "header.bin"))
	if err != nil {
		return nil, err
	}
	dataBytes, err := os.ReadFile(filepath.Join(segDir, "data_level0.bin"))
	if err != nil {
		return nil, err
	}
	hdr, err := parseHNSWHeader(hdrBytes, len(dataBytes))
	if err != nil {
		return nil, err
	}
	lengthBytes, _ := os.ReadFile(filepath.Join(segDir, "length.bin"))

	dim := int((hdr.labelOffset - hdr.offsetData) / 4)
	seg := &parsedHNSWSegment{
		dim:        dim,
		labelToVec: make(map[uint64][]float32, hdr.curElementCount),
	}

	if pklBytes, err := os.ReadFile(filepath.Join(segDir, "index_metadata.pickle")); err == nil && len(pklBytes) > 0 {
		if idMap, pklErr := parseChromaPickleIDToLabel(pklBytes); pklErr == nil && len(idMap) > 0 {
			seg.idToLabel = idMap
			seg.hasIDMap = true
		}
	}

	for i := uint64(0); i < hdr.curElementCount; i++ {
		base := i * hdr.sizeDataPerElement
		linkWord := binary.LittleEndian.Uint32(dataBytes[base : base+4])
		isDeleted := ((linkWord >> 16) & 0x01) != 0
		if isDeleted {
			continue
		}
		vecSlice := dataBytes[base+hdr.offsetData : base+hdr.labelOffset]
		vec, err := decodeFloat32LE(vecSlice)
		if err != nil {
			return nil, err
		}
		// chroma-hnswlib 在 cosine 空间将原向量范数存于 length.bin[i]，乘回即可还原精确原向量。
		if len(lengthBytes) >= int((i+1)*4) {
			norm := math.Float32frombits(binary.LittleEndian.Uint32(lengthBytes[i*4:]))
			if norm > 0 && !math.IsNaN(float64(norm)) && !math.IsInf(float64(norm), 0) && math.Abs(float64(norm)-1.0) > 1e-7 {
				for k := range vec {
					vec[k] *= norm
				}
			}
		}
		label := binary.LittleEndian.Uint64(dataBytes[base+hdr.labelOffset : base+hdr.labelOffset+8])
		seg.labelToVec[label] = vec
		seg.elemVecs = append(seg.elemVecs, vec)
	}
	return seg, nil
}

// ────────────────────────── 只读受限 Pickle 解码器（仅提取 id_to_label / label_to_id） ──────────────────────────

type pickleMark struct{}

type pickleObject struct {
	state any
}

type pickleList struct {
	items []any
}

// parseChromaPickleIDToLabel 纯数据解析 Chroma index_metadata.pickle（兼容 Python PersistentData 实例与 Rust dict 两种形态）。
func parseChromaPickleIDToLabel(data []byte) (map[string]uint64, error) {
	root, err := decodeSafePickle(data)
	if err != nil {
		return nil, err
	}
	var topMap map[any]any
	switch v := root.(type) {
	case map[any]any:
		topMap = v
	case *pickleObject:
		if m, ok := v.state.(map[any]any); ok {
			topMap = m
		}
	}
	if topMap == nil {
		return nil, errors.New("pickle 顶层不包含字典状态")
	}

	out := map[string]uint64{}
	if rawMap, ok := topMap["id_to_label"].(map[any]any); ok {
		for k, v := range rawMap {
			id, okK := k.(string)
			lbl, okV := pickleAsUint64(v)
			if okK && okV {
				out[id] = lbl
			}
		}
	}
	if len(out) == 0 {
		if revMap, ok := topMap["label_to_id"].(map[any]any); ok {
			for k, v := range revMap {
				lbl, okK := pickleAsUint64(k)
				id, okV := v.(string)
				if okK && okV {
					out[id] = lbl
				}
			}
		}
	}
	return out, nil
}

func pickleAsUint64(v any) (uint64, bool) {
	switch n := v.(type) {
	case int64:
		if n < 0 {
			return 0, false
		}
		return uint64(n), true
	case uint64:
		return n, true
	default:
		return 0, false
	}
}

// decodeSafePickle 执行无副作用的只读栈式 Pickle 状态机（支持协议 0-5 常用数据操作码）。
func decodeSafePickle(data []byte) (any, error) {
	var (
		stack []any
		memo  = map[int]any{}
		pos   = 0
	)
	pop := func() (any, error) {
		if len(stack) == 0 {
			return nil, errors.New("pickle 栈下溢")
		}
		v := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		return v, nil
	}
	popToMark := func() ([]any, error) {
		for i := len(stack) - 1; i >= 0; i-- {
			if _, isMark := stack[i].(pickleMark); isMark {
				items := make([]any, len(stack)-1-i)
				copy(items, stack[i+1:])
				stack = stack[:i]
				return items, nil
			}
		}
		return nil, errors.New("pickle 未找到 MARK")
	}
	readLine := func() (string, error) {
		idx := bytes.IndexByte(data[pos:], '\n')
		if idx < 0 {
			return "", errors.New("pickle 行未闭合")
		}
		s := strings.TrimRight(string(data[pos:pos+idx]), "\r")
		pos += idx + 1
		return s, nil
	}
	readBytes := func(n int) ([]byte, error) {
		if n < 0 || pos+n > len(data) {
			return nil, errors.New("pickle 数据意外截断")
		}
		b := data[pos : pos+n]
		pos += n
		return b, nil
	}

	for pos < len(data) {
		op := data[pos]
		pos++
		switch op {
		case 0x80: // PROTO
			if _, err := readBytes(1); err != nil {
				return nil, err
			}
		case 0x95: // FRAME
			if _, err := readBytes(8); err != nil {
				return nil, err
			}
		case '.': // STOP
			return pop()
		case '(': // MARK
			stack = append(stack, pickleMark{})
		case 'N': // NONE
			stack = append(stack, nil)
		case 0x88: // NEWTRUE
			stack = append(stack, true)
		case 0x89: // NEWFALSE
			stack = append(stack, false)
		case 'K': // BININT1
			b, err := readBytes(1)
			if err != nil {
				return nil, err
			}
			stack = append(stack, int64(b[0]))
		case 'M': // BININT2
			b, err := readBytes(2)
			if err != nil {
				return nil, err
			}
			stack = append(stack, int64(binary.LittleEndian.Uint16(b)))
		case 'J': // BININT
			b, err := readBytes(4)
			if err != nil {
				return nil, err
			}
			stack = append(stack, int64(int32(binary.LittleEndian.Uint32(b))))
		case 'I', 'L': // INT / LONG
			line, err := readLine()
			if err != nil {
				return nil, err
			}
			line = strings.TrimSuffix(line, "L")
			switch line {
			case "00":
				stack = append(stack, false)
			case "01":
				stack = append(stack, true)
			default:
				v, _ := strconv.ParseInt(line, 10, 64)
				stack = append(stack, v)
			}
		case 0x8a: // LONG1
			nb, err := readBytes(1)
			if err != nil {
				return nil, err
			}
			raw, err := readBytes(int(nb[0]))
			if err != nil {
				return nil, err
			}
			stack = append(stack, decodeSignedLEInt64(raw))
		case 0x8b: // LONG4
			nb, err := readBytes(4)
			if err != nil {
				return nil, err
			}
			raw, err := readBytes(int(int32(binary.LittleEndian.Uint32(nb))))
			if err != nil {
				return nil, err
			}
			stack = append(stack, decodeSignedLEInt64(raw))
		case 'G': // BINFLOAT
			b, err := readBytes(8)
			if err != nil {
				return nil, err
			}
			stack = append(stack, math.Float64frombits(binary.BigEndian.Uint64(b)))
		case 0x8c, 'U': // SHORT_BINUNICODE / SHORT_BINSTRING
			nb, err := readBytes(1)
			if err != nil {
				return nil, err
			}
			b, err := readBytes(int(nb[0]))
			if err != nil {
				return nil, err
			}
			stack = append(stack, string(b))
		case 'X', 'T': // BINUNICODE / BINSTRING
			nb, err := readBytes(4)
			if err != nil {
				return nil, err
			}
			b, err := readBytes(int(binary.LittleEndian.Uint32(nb)))
			if err != nil {
				return nil, err
			}
			stack = append(stack, string(b))
		case 0x8d: // BINUNICODE8
			nb, err := readBytes(8)
			if err != nil {
				return nil, err
			}
			u := binary.LittleEndian.Uint64(nb)
			if u > uint64(len(data)-pos) {
				return nil, errors.New("pickle 字符串长度越界")
			}
			b, err := readBytes(int(u))
			if err != nil {
				return nil, err
			}
			stack = append(stack, string(b))
		case 'V', 'S': // UNICODE / STRING
			line, err := readLine()
			if err != nil {
				return nil, err
			}
			stack = append(stack, strings.Trim(line, "'\""))
		case 'C': // SHORT_BINBYTES
			nb, err := readBytes(1)
			if err != nil {
				return nil, err
			}
			b, err := readBytes(int(nb[0]))
			if err != nil {
				return nil, err
			}
			cp := append([]byte(nil), b...)
			stack = append(stack, cp)
		case 'B': // BINBYTES
			nb, err := readBytes(4)
			if err != nil {
				return nil, err
			}
			b, err := readBytes(int(binary.LittleEndian.Uint32(nb)))
			if err != nil {
				return nil, err
			}
			cp := append([]byte(nil), b...)
			stack = append(stack, cp)
		case 0x8e: // BINBYTES8
			nb, err := readBytes(8)
			if err != nil {
				return nil, err
			}
			u := binary.LittleEndian.Uint64(nb)
			if u > uint64(len(data)-pos) {
				return nil, errors.New("pickle 字节串长度越界")
			}
			b, err := readBytes(int(u))
			if err != nil {
				return nil, err
			}
			cp := append([]byte(nil), b...)
			stack = append(stack, cp)
		case ')': // EMPTY_TUPLE
			stack = append(stack, []any{})
		case 't': // TUPLE
			items, err := popToMark()
			if err != nil {
				return nil, err
			}
			stack = append(stack, items)
		case 0x85: // TUPLE1
			a, err := pop()
			if err != nil {
				return nil, err
			}
			stack = append(stack, []any{a})
		case 0x86: // TUPLE2
			b, err := pop()
			if err != nil {
				return nil, err
			}
			a, err := pop()
			if err != nil {
				return nil, err
			}
			stack = append(stack, []any{a, b})
		case 0x87: // TUPLE3
			c, err := pop()
			if err != nil {
				return nil, err
			}
			b, err := pop()
			if err != nil {
				return nil, err
			}
			a, err := pop()
			if err != nil {
				return nil, err
			}
			stack = append(stack, []any{a, b, c})
		case ']': // EMPTY_LIST
			stack = append(stack, &pickleList{})
		case 'a': // APPEND
			v, err := pop()
			if err != nil {
				return nil, err
			}
			if len(stack) == 0 {
				return nil, errors.New("pickle APPEND 栈空")
			}
			if pl, ok := stack[len(stack)-1].(*pickleList); ok {
				pl.items = append(pl.items, v)
			}
		case 'e': // APPENDS
			items, err := popToMark()
			if err != nil {
				return nil, err
			}
			if len(stack) == 0 {
				return nil, errors.New("pickle APPENDS 栈空")
			}
			if pl, ok := stack[len(stack)-1].(*pickleList); ok {
				pl.items = append(pl.items, items...)
			}
		case '}': // EMPTY_DICT
			stack = append(stack, map[any]any{})
		case 'd': // DICT
			items, err := popToMark()
			if err != nil {
				return nil, err
			}
			m := make(map[any]any, len(items)/2)
			for i := 0; i+1 < len(items); i += 2 {
				if isHashablePickleKey(items[i]) {
					m[items[i]] = items[i+1]
				}
			}
			stack = append(stack, m)
		case 's': // SETITEM
			val, err := pop()
			if err != nil {
				return nil, err
			}
			key, err := pop()
			if err != nil {
				return nil, err
			}
			if len(stack) == 0 {
				return nil, errors.New("pickle SETITEM 栈空")
			}
			if m, ok := stack[len(stack)-1].(map[any]any); ok && isHashablePickleKey(key) {
				m[key] = val
			}
		case 'u': // SETITEMS
			items, err := popToMark()
			if err != nil {
				return nil, err
			}
			if len(stack) == 0 {
				return nil, errors.New("pickle SETITEMS 栈空")
			}
			if m, ok := stack[len(stack)-1].(map[any]any); ok {
				for i := 0; i+1 < len(items); i += 2 {
					if isHashablePickleKey(items[i]) {
						m[items[i]] = items[i+1]
					}
				}
			}
		case 0x8f: // EMPTY_SET
			stack = append(stack, &pickleList{})
		case 0x90, 0x91: // ADDITEMS / FROZENSET
			items, err := popToMark()
			if err != nil {
				return nil, err
			}
			if op == 0x91 {
				stack = append(stack, &pickleList{items: items})
			}
		case 0x94: // MEMOIZE
			if len(stack) == 0 {
				return nil, errors.New("pickle MEMOIZE 栈空")
			}
			memo[len(memo)] = stack[len(stack)-1]
		case 'q': // BINPUT
			b, err := readBytes(1)
			if err != nil || len(stack) == 0 {
				return nil, errors.New("pickle BINPUT 错误")
			}
			memo[int(b[0])] = stack[len(stack)-1]
		case 'r': // LONG_BINPUT
			b, err := readBytes(4)
			if err != nil || len(stack) == 0 {
				return nil, errors.New("pickle LONG_BINPUT 错误")
			}
			memo[int(binary.LittleEndian.Uint32(b))] = stack[len(stack)-1]
		case 'p': // PUT
			line, err := readLine()
			if err != nil || len(stack) == 0 {
				return nil, errors.New("pickle PUT 错误")
			}
			idx, _ := strconv.Atoi(line)
			memo[idx] = stack[len(stack)-1]
		case 'h': // BINGET
			b, err := readBytes(1)
			if err != nil {
				return nil, err
			}
			stack = append(stack, memo[int(b[0])])
		case 'j': // LONG_BINGET
			b, err := readBytes(4)
			if err != nil {
				return nil, err
			}
			stack = append(stack, memo[int(binary.LittleEndian.Uint32(b))])
		case 'g': // GET
			line, err := readLine()
			if err != nil {
				return nil, err
			}
			idx, _ := strconv.Atoi(line)
			stack = append(stack, memo[idx])
		case 'c': // GLOBAL
			mod, err := readLine()
			if err != nil {
				return nil, err
			}
			name, err := readLine()
			if err != nil {
				return nil, err
			}
			stack = append(stack, mod+"."+name)
		case 0x93: // STACK_GLOBAL
			name, err := pop()
			if err != nil {
				return nil, err
			}
			mod, err := pop()
			if err != nil {
				return nil, err
			}
			stack = append(stack, fmt.Sprintf("%v.%v", mod, name))
		case 0x81, 'R': // NEWOBJ / REDUCE
			if _, err := pop(); err != nil {
				return nil, err
			}
			if _, err := pop(); err != nil {
				return nil, err
			}
			stack = append(stack, &pickleObject{})
		case 0x92: // NEWOBJ_EX
			for range 3 {
				if _, err := pop(); err != nil {
					return nil, err
				}
			}
			stack = append(stack, &pickleObject{})
		case 'i', 'o': // INST / OBJ
			if op == 'i' {
				_, _ = readLine()
				_, _ = readLine()
			}
			if _, err := popToMark(); err != nil {
				return nil, err
			}
			stack = append(stack, &pickleObject{})
		case 'b': // BUILD
			state, err := pop()
			if err != nil || len(stack) == 0 {
				return nil, errors.New("pickle BUILD 错误")
			}
			if obj, ok := stack[len(stack)-1].(*pickleObject); ok {
				obj.state = state
			}
		case '0': // POP
			if _, err := pop(); err != nil {
				return nil, err
			}
		case '1': // POP_MARK
			if _, err := popToMark(); err != nil {
				return nil, err
			}
		case '2': // DUP
			if len(stack) == 0 {
				return nil, errors.New("pickle DUP 栈空")
			}
			stack = append(stack, stack[len(stack)-1])
		default:
			return nil, fmt.Errorf("不支持的 pickle 操作码: 0x%02x", op)
		}
	}
	return nil, errors.New("pickle 缺少 STOP 结束符")
}

func isHashablePickleKey(k any) bool {
	switch k.(type) {
	case string, int64, uint64, float64, bool, nil:
		return true
	default:
		return false
	}
}

func decodeSignedLEInt64(raw []byte) int64 {
	if len(raw) == 0 {
		return 0
	}
	var u uint64
	limit := len(raw)
	if limit > 8 {
		limit = 8
	}
	for i := 0; i < limit; i++ {
		u |= uint64(raw[i]) << (8 * i)
	}
	if len(raw) < 8 && (raw[len(raw)-1]&0x80) != 0 {
		for i := len(raw); i < 8; i++ {
			u |= 0xff << (8 * i)
		}
	}
	return int64(u)
}

// ────────────────────────── 目标临时库写入与一致性校验 ──────────────────────────

func writeTempLiteDB(
	ctx context.Context,
	tmpPath string,
	expectedDim int,
	summariesRows, messagesRows map[string]chromaMigratedRow,
) error {
	db, err := sql.Open("sqlite", buildRWDSN(tmpPath))
	if err != nil {
		return fmt.Errorf("创建临时目标库失败: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	defer db.Close()

	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=FULL",
		"PRAGMA foreign_keys=OFF",
	} {
		if _, err := conn.ExecContext(ctx, pragma); err != nil {
			return fmt.Errorf("临时库执行 %s 失败: %w", pragma, err)
		}
	}

	if _, err := conn.ExecContext(ctx, schemaDDL); err != nil {
		return fmt.Errorf("临时库建表失败: %w", err)
	}

	storedAt := formatISOUTC(time.Now().UTC())
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return err
	}

	writeTable := func(table string, data map[string]chromaMigratedRow) error {
		if len(data) == 0 {
			return nil
		}
		ids := make([]string, 0, len(data))
		for id := range data {
			ids = append(ids, id)
		}
		sort.Strings(ids)

		stmt, err := conn.PrepareContext(ctx,
			"INSERT OR REPLACE INTO "+table+" (id, embedding, document, metadata_json, dim, stored_at) VALUES (?, ?, ?, ?, ?, ?)")
		if err != nil {
			return err
		}
		defer stmt.Close()

		for _, id := range ids {
			item := data[id]
			if len(item.Vector) != expectedDim {
				return fmt.Errorf("%w: %s/%s 维度 %d != %d", ErrDimensionMismatch, table, id, len(item.Vector), expectedDim)
			}
			metaJSON, _, err := marshalMetadataJSON(item.Metadata)
			if err != nil {
				return err
			}
			if _, err := stmt.ExecContext(ctx,
				item.ID, encodeFloat32LE(item.Vector), item.Document, metaJSON, expectedDim, storedAt,
			); err != nil {
				return err
			}
		}
		return nil
	}

	if err := writeTable(TableSummaries, summariesRows); err != nil {
		safeRollback(conn)
		return err
	}
	if err := writeTable(TableMessages, messagesRows); err != nil {
		safeRollback(conn)
		return err
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		safeRollback(conn)
		return err
	}

	// 显式 TRUNCATE checkpoint：确保全部已提交帧合并进主文件并清空 .tmp-wal。
	var busy, logPages, checkpointed int
	if err := conn.QueryRowContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &logPages, &checkpointed); err != nil {
		return fmt.Errorf("执行 wal_checkpoint(TRUNCATE) 失败: %w", err)
	}
	if busy != 0 {
		return fmt.Errorf("wal_checkpoint(TRUNCATE) 处于忙碌状态 (busy=%d)", busy)
	}
	return nil
}

// buildImmutableVerifyDSN 构造校验期只读 immutable URI（对齐 Python 脚本 ?mode=ro&immutable=1，
// 避免在已合并关闭的临时库上重新生成伴生文件）。
func buildImmutableVerifyDSN(file string) string {
	p := strings.ReplaceAll(file, "\\", "/")
	p = strings.ReplaceAll(p, "%", "%25")
	p = strings.ReplaceAll(p, "?", "%3F")
	p = strings.ReplaceAll(p, "#", "%23")
	return "file:" + p + "?mode=ro&immutable=1"
}

func verifyMigratedDB(
	ctx context.Context,
	dbPath string,
	expectedDim int,
	summariesRows, messagesRows map[string]chromaMigratedRow,
) error {
	db, err := sql.Open("sqlite", buildImmutableVerifyDSN(dbPath))
	if err != nil {
		return err
	}
	db.SetMaxOpenConns(1)
	defer db.Close()

	var schemaVer int
	if err := db.QueryRowContext(ctx,
		"SELECT value FROM store_meta WHERE key = 'schema_version'").Scan(&schemaVer); err != nil || schemaVer != requiredVersion {
		return fmt.Errorf("校验失败：schema_version 异常 (%v, ver=%d)", err, schemaVer)
	}

	checkCollection := func(label, table string, expected map[string]chromaMigratedRow) error {
		var actualCount int
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&actualCount); err != nil {
			return err
		}
		if actualCount != len(expected) {
			return fmt.Errorf("校验失败：%s 行数不一致（SQLite=%d, Chroma=%d）", label, actualCount, len(expected))
		}

		var badDim int
		if err := db.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM "+table+" WHERE dim != ? OR LENGTH(embedding) != ?",
			expectedDim, expectedDim*4,
		).Scan(&badDim); err != nil {
			return err
		}
		if badDim > 0 {
			return fmt.Errorf("校验失败：%s 存在 %d 行维度异常（期望 %d）", label, badDim, expectedDim)
		}

		rows, err := db.QueryContext(ctx,
			"SELECT id, embedding FROM "+table+" ORDER BY RANDOM() LIMIT ?", migrateSampleSize)
		if err != nil {
			return err
		}
		defer rows.Close()

		for rows.Next() {
			var (
				id   string
				blob []byte
			)
			if err := rows.Scan(&id, &blob); err != nil {
				return err
			}
			src, ok := expected[id]
			if !ok {
				return fmt.Errorf("校验失败：%s/%s 在源 Chroma 中不存在", label, id)
			}
			localVec, err := decodeFloat32LE(blob)
			if err != nil {
				return err
			}
			cos := cosineSimilarity(localVec, src.Vector)
			if cos <= migrateCosineThreshold {
				return fmt.Errorf("校验失败：%s/%s 余弦相似度 %.8f <= %.6f", label, id, cos, migrateCosineThreshold)
			}
		}
		return rows.Err()
	}

	if err := checkCollection("summaries", TableSummaries, summariesRows); err != nil {
		return err
	}
	if err := checkCollection("messages", TableMessages, messagesRows); err != nil {
		return err
	}
	return nil
}

// cosineSimilarity 计算两个原始向量的余弦相似度（两零向量完全相等时返回 1.0）。
func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dotProd, normA, normB float64
	for i := range a {
		fa, fb := float64(a[i]), float64(b[i])
		dotProd += fa * fb
		normA += fa * fa
		normB += fb * fb
	}
	if normA == 0 && normB == 0 {
		return 1.0
	}
	denom := math.Max(math.Sqrt(normA)*math.Sqrt(normB), 1e-12)
	return dotProd / denom
}
