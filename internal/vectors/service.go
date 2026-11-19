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
	"errors"
	"fmt"
	"maps"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	// DefaultChunkMaxChars 为长消息向量化分块的默认单块最大字符（rune）数。
	DefaultChunkMaxChars = 800

	// DefaultChunkOverlap 为长消息向量化分块的默认相邻块重叠字符（rune）数。
	DefaultChunkOverlap = 100
)

// EmbedFunc 批量把文本编码为 float32 向量；返回向量切片须与 texts 等长且一一对应。
type EmbedFunc func(ctx context.Context, texts []string) ([][]float32, error)

// StoreStats 汇总向量存储整体就绪状态与各集合统计（对齐 WebUI /vector-store/stats）。
type StoreStats struct {
	Available      bool   `json:"available"`
	SummariesCount int    `json:"summaries_count"`
	MessagesCount  int    `json:"messages_count"`
	TotalVectors   int    `json:"total_vectors"`
	DBPath         string `json:"db_path"`
	ExpectedDim    int    `json:"expected_dim"`
}

// GetFilter 控制 GetRows 的过滤条件、分页与返回字段投影
// （对齐 core/ai/vector_lite.py::LiteEngine.get_rows 与 CollectionAdapter.get）。
type GetFilter struct {
	IDs         []string // 非空时按 id IN (...) 精确过滤
	IDPrefix    string   // 非空时按 id >= prefix AND id < prefix+"\uffff" 前缀过滤
	Limit       int      // >0 时限制返回条数；<=0 表示不限
	Offset      int      // >0 时跳过前 Offset 条
	IncludeDocs bool     // 是否加载并返回 Document 正文
	IncludeMeta bool     // 是否解析并返回 Metadata 字典
	IncludeVecs bool     // 是否解码并返回 Vector ([]float32)
}

// StoredRow 表示从 SQLite 向量表读取的单条原始记录。
type StoredRow struct {
	ID       string         `json:"id"`
	Document string         `json:"document,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
	Vector   []float32      `json:"vector,omitempty"`
	Dim      int            `json:"dim"`
	StoredAt string         `json:"stored_at,omitempty"`
}

// MessageItem 表示一条待写入或更新的频道原始消息（由 UpsertMessages 自动分块并清理陈旧块）。
type MessageItem struct {
	ChannelID       string         // 频道 ID（必填）
	ChannelName     string         // 频道名称
	ChannelUsername string         // 频道公开用户名（可选）
	MessageID       int64          // Telegram 消息 ID（必填，>0）
	SenderID        int64          // 发送者 ID（可选）
	Text            string         // 消息正文
	CreatedAt       time.Time      // 发布时间（零值时优先保留 Metadata["created_at"]，否则取当前 UTC）
	Metadata        map[string]any // 附加扩展元数据（可选）
	Vector          []float32      // 可选预计算向量（仅当消息不分块且非空时免调 embedFn）
	ChunkMaxChars   int            // 可选自定义分块最大字符数（<=0 取 DefaultChunkMaxChars）
	ChunkOverlap    int            // 可选自定义分块重叠字符数（ChunkMaxChars<=0 且 ChunkOverlap==0 时取 DefaultChunkOverlap）
}

// withQueryReadyLocked 在持有 s.mu 的前提下确保底层连接可读（RW 自动建表，RO 仅核查契约）并带单次连接重试。
func (s *Store) withQueryReadyLocked(ctx context.Context, op func(context.Context) error) error {
	if !s.readOnly {
		if s.db != nil {
			if fi, err := os.Stat(s.dbFile); err != nil || fi.IsDir() {
				_ = s.resetDBLocked()
			}
		}
		return s.runWithRetryLocked(ctx, func(ctx context.Context) error {
			if err := s.ensureRWReadyLocked(ctx); err != nil {
				return err
			}
			return op(ctx)
		})
	}

	if fi, err := os.Stat(s.dbFile); err != nil || fi.IsDir() {
		_ = s.resetDBLocked()
		return fmt.Errorf("%w: 向量库文件缺失: %s", ErrNotReady, s.dbFile)
	}
	return s.runWithRetryLocked(ctx, func(ctx context.Context) error {
		if _, err := s.readVersions(ctx); err != nil {
			return fmt.Errorf("%w: %w", ErrNotReady, err)
		}
		return op(ctx)
	})
}

// Count 返回指定集合在磁盘上的权威行数（SELECT COUNT(*)）。
func (s *Store) Count(ctx context.Context, collection string) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	table, _, _, err := resolveCollection(collection)
	if err != nil {
		return 0, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	var count int
	err = s.withQueryReadyLocked(ctx, func(ctx context.Context) error {
		return s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count)
	})
	if err != nil {
		return 0, err
	}
	return count, nil
}

// Stats 返回向量库整体可用性与两集合行数统计。
// 当底层库尚未就绪或目录不可写（但 ctx 未取消）时，返回 Available=false 且 error=nil，
// 对齐 core/ai/vector_store.py::get_stats 的降级语义。
func (s *Store) Stats(ctx context.Context) (StoreStats, error) {
	stats := StoreStats{
		DBPath:      s.dbFile,
		ExpectedDim: s.ExpectedDim(),
	}
	if err := ctx.Err(); err != nil {
		return stats, err
	}
	if err := s.Ready(ctx); err != nil {
		if ctx.Err() != nil {
			return stats, ctx.Err()
		}
		return stats, nil
	}

	sumCount, err := s.Count(ctx, "summaries")
	if err != nil {
		return stats, err
	}
	msgCount, err := s.Count(ctx, "messages")
	if err != nil {
		return stats, err
	}
	stats.Available = true
	stats.SummariesCount = sumCount
	stats.MessagesCount = msgCount
	stats.TotalVectors = sumCount + msgCount
	stats.ExpectedDim = s.probeEffectiveDim(ctx)
	return stats, nil
}

// probeEffectiveDim 返回有效维度：RW 模式直接返回固定 expectedDim，RO 模式尝试从表内首行探测，回退 1024。
func (s *Store) probeEffectiveDim(ctx context.Context) int {
	if s.expectedDim > 0 {
		return s.expectedDim
	}
	if d := s.ExpectedDim(); d != DefaultDimension {
		return d
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return DefaultDimension
	}
	for _, table := range []string{TableSummaries, TableMessages} {
		var dim int
		err := s.db.QueryRowContext(ctx, "SELECT dim FROM "+table+" LIMIT 1").Scan(&dim)
		if err == nil && dim > 0 {
			return dim
		}
	}
	return DefaultDimension
}

// GetRows 按 ID 集合、ID 前缀与分页条件按 ORDER BY id 升序读取向量表记录。
func (s *Store) GetRows(ctx context.Context, collection string, filter GetFilter) ([]StoredRow, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	table, _, _, err := resolveCollection(collection)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	var out []StoredRow
	err = s.withQueryReadyLocked(ctx, func(ctx context.Context) error {
		// 若 IDs 超过单语句参数块上限，分块查询后在内存合并排序并分页。
		if len(filter.IDs) > deleteChunkSize {
			merged := make([]StoredRow, 0, len(filter.IDs))
			seen := make(map[string]struct{}, len(filter.IDs))
			for start := 0; start < len(filter.IDs); start += deleteChunkSize {
				end := start + deleteChunkSize
				if end > len(filter.IDs) {
					end = len(filter.IDs)
				}
				sub := filter
				sub.IDs = filter.IDs[start:end]
				sub.Limit = 0
				sub.Offset = 0
				part, err := queryStoredRows(ctx, s.db, table, sub)
				if err != nil {
					return err
				}
				for _, r := range part {
					if _, dup := seen[r.ID]; dup {
						continue
					}
					seen[r.ID] = struct{}{}
					merged = append(merged, r)
				}
			}
			sort.Slice(merged, func(i, j int) bool { return merged[i].ID < merged[j].ID })
			if filter.Offset > 0 {
				if filter.Offset >= len(merged) {
					out = []StoredRow{}
					return nil
				}
				merged = merged[filter.Offset:]
			}
			if filter.Limit > 0 && filter.Limit < len(merged) {
				merged = merged[:filter.Limit]
			}
			out = merged
			return nil
		}

		rows, err := queryStoredRows(ctx, s.db, table, filter)
		if err != nil {
			return err
		}
		out = rows
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// queryStoredRows 执行单次 SQL 查询并按 filter 投影解析结果。
func queryStoredRows(ctx context.Context, db *sql.DB, table string, filter GetFilter) ([]StoredRow, error) {
	cols := []string{"id", "dim", "stored_at"}
	if filter.IncludeDocs {
		cols = append(cols, "document")
	}
	if filter.IncludeMeta {
		cols = append(cols, "metadata_json")
	}
	if filter.IncludeVecs {
		cols = append(cols, "embedding")
	}

	var sqlBuf strings.Builder
	sqlBuf.WriteString("SELECT ")
	sqlBuf.WriteString(strings.Join(cols, ", "))
	sqlBuf.WriteString(" FROM ")
	sqlBuf.WriteString(table)

	var args []any
	hasWhere := false
	if len(filter.IDs) > 0 {
		sqlBuf.WriteString(" WHERE id IN (")
		sqlBuf.WriteString(buildPlaceholders(len(filter.IDs)))
		sqlBuf.WriteString(")")
		for _, id := range filter.IDs {
			args = append(args, id)
		}
		hasWhere = true
	}
	if filter.IDPrefix != "" {
		if hasWhere {
			sqlBuf.WriteString(" AND ")
		} else {
			sqlBuf.WriteString(" WHERE ")
		}
		sqlBuf.WriteString("id >= ? AND id < ?")
		args = append(args, filter.IDPrefix, filter.IDPrefix+"\uffff")
	}
	sqlBuf.WriteString(" ORDER BY id")
	if filter.Limit > 0 {
		sqlBuf.WriteString(" LIMIT ?")
		args = append(args, filter.Limit)
		if filter.Offset > 0 {
			sqlBuf.WriteString(" OFFSET ?")
			args = append(args, filter.Offset)
		}
	} else if filter.Offset > 0 {
		sqlBuf.WriteString(" LIMIT -1 OFFSET ?")
		args = append(args, filter.Offset)
	}

	rows, err := db.QueryContext(ctx, sqlBuf.String(), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []StoredRow
	for rows.Next() {
		var (
			id, storedAt string
			dim          int
			doc          string
			metaJSON     string
			blob         []byte
		)
		dests := []any{&id, &dim, &storedAt}
		if filter.IncludeDocs {
			dests = append(dests, &doc)
		}
		if filter.IncludeMeta {
			dests = append(dests, &metaJSON)
		}
		if filter.IncludeVecs {
			dests = append(dests, &blob)
		}
		if err := rows.Scan(dests...); err != nil {
			return nil, err
		}

		item := StoredRow{
			ID:       id,
			Dim:      dim,
			StoredAt: storedAt,
		}
		if filter.IncludeDocs {
			item.Document = doc
		}
		if filter.IncludeMeta {
			meta, err := decodeMetadata(metaJSON)
			if err != nil {
				meta = map[string]any{}
			}
			item.Metadata = meta
		}
		if filter.IncludeVecs {
			vec, err := decodeFloat32LE(blob)
			if err != nil {
				return nil, err
			}
			item.Vector = vec
			if item.Dim == 0 {
				item.Dim = len(vec)
			}
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if result == nil {
		result = []StoredRow{}
	}
	return result, nil
}

// GetDocument 查询单条文档详情（含正文、元数据、向量与 Dim）；未命中时返回 (nil, nil)。
func (s *Store) GetDocument(ctx context.Context, collection, docID string) (*StoredRow, error) {
	rows, err := s.GetRows(ctx, collection, GetFilter{
		IDs:         []string{docID},
		Limit:       1,
		IncludeDocs: true,
		IncludeMeta: true,
		IncludeVecs: true,
	})
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	row := rows[0]
	return &row, nil
}

// SplitMessageChunks 按字符（Unicode rune）窗口与重叠量无损切分消息文本：
//   - maxChars <= 0 时默认取 DefaultChunkMaxChars (800)；
//   - overlap < 0 时归零；overlap >= maxChars 时钳制为 maxChars-1（保证窗口严格前进）；
//   - 绝不拆坏多字节 UTF-8 字符（中文、Emoji、代码），绝不丢失首尾空白或尾部内容。
func SplitMessageChunks(text string, maxChars, overlap int) []string {
	if text == "" {
		return nil
	}
	if maxChars <= 0 {
		maxChars = DefaultChunkMaxChars
	}
	if overlap < 0 {
		overlap = 0
	}
	if overlap >= maxChars {
		if maxChars <= 1 {
			overlap = 0
		} else {
			overlap = maxChars - 1
		}
	}

	runes := []rune(text)
	if len(runes) <= maxChars {
		return []string{text}
	}

	step := maxChars - overlap
	estChunks := (len(runes)-overlap+step-1)/step + 1
	chunks := make([]string, 0, estChunks)
	for start := 0; start < len(runes); {
		end := start + maxChars
		if end >= len(runes) {
			chunks = append(chunks, string(runes[start:]))
			break
		}
		chunks = append(chunks, string(runes[start:end]))
		start = end - overlap
	}
	return chunks
}

// AddSummary 生成总结向量并插入 summaries 集合（对齐 core/ai/vector_store.py::add_summary）：
//   - embedding 网络调用在 Store 互斥锁外执行；
//   - docID 固定为 strconv.FormatInt(summaryID, 10)；
//   - 重复 summaryID 返回 ErrDuplicateID。
func (s *Store) AddSummary(
	ctx context.Context,
	embedFn EmbedFunc,
	summaryID int64,
	channelID, channelName, summaryText string,
	startTime, endTime, createdAt time.Time,
	messageCount int,
) error {
	return s.writeSummary(ctx, embedFn, summaryID, channelID, channelName, summaryText, startTime, endTime, createdAt, messageCount, false)
}

// UpsertSummary updates the derived index from the authoritative MySQL summary.
func (s *Store) UpsertSummary(
	ctx context.Context,
	embedFn EmbedFunc,
	summaryID int64,
	channelID, channelName, summaryText string,
	startTime, endTime, createdAt time.Time,
	messageCount int,
) error {
	return s.writeSummary(ctx, embedFn, summaryID, channelID, channelName, summaryText, startTime, endTime, createdAt, messageCount, true)
}

func (s *Store) writeSummary(
	ctx context.Context,
	embedFn EmbedFunc,
	summaryID int64,
	channelID, channelName, summaryText string,
	startTime, endTime, createdAt time.Time,
	messageCount int,
	upsert bool,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.readOnly {
		return ErrReadOnly
	}
	if embedFn == nil {
		return errors.New("embedFn 不能为空")
	}
	if strings.TrimSpace(summaryText) == "" {
		return errors.New("summaryText 不能为空")
	}

	vecs, err := embedFn(ctx, []string{summaryText})
	if err != nil {
		return fmt.Errorf("生成总结向量失败: %w", err)
	}
	if len(vecs) != 1 || len(vecs[0]) == 0 {
		return fmt.Errorf("生成总结向量返回数量异常: got %d", len(vecs))
	}

	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	meta := map[string]any{
		"summary_id":    summaryID,
		"channel_id":    channelID,
		"channel_name":  channelName,
		"created_at":    formatISOUTC(createdAt),
		"message_count": messageCount,
	}
	if !startTime.IsZero() {
		meta["start_time"] = formatISOUTC(startTime)
	}
	if !endTime.IsZero() {
		meta["end_time"] = formatISOUTC(endTime)
	}

	id := strconv.FormatInt(summaryID, 10)
	if upsert {
		_, err := s.UpsertMany(ctx, "summaries", []WriteRow{{ID: id, Vector: vecs[0], Document: summaryText, Metadata: meta}})
		return err
	}
	return s.Insert(ctx, "summaries", id, vecs[0], summaryText, meta)
}

// isMessageChunkID 报告 id 是否为指定 parentID 的分块文档 ID（兼容 "#chunk_" 与历史 Python "#chunk="）。
func isMessageChunkID(id, parentID string) bool {
	return strings.HasPrefix(id, parentID+"#chunk_") || strings.HasPrefix(id, parentID+"#chunk=")
}

// findExistingMessageDocIDs 查询某批 parentID 当前在 messages 表中已存在的单条 ID 与全部分块 ID。
func (s *Store) findExistingMessageDocIDs(ctx context.Context, parentIDs []string) ([]string, error) {
	if len(parentIDs) == 0 {
		return nil, nil
	}
	seen := make(map[string]struct{}, len(parentIDs)*2)
	var result []string
	addID := func(id string) {
		if _, ok := seen[id]; !ok {
			seen[id] = struct{}{}
			result = append(result, id)
		}
	}

	exactRows, err := s.GetRows(ctx, "messages", GetFilter{IDs: parentIDs})
	if err != nil {
		return nil, err
	}
	for _, r := range exactRows {
		addID(r.ID)
	}

	for _, parentID := range parentIDs {
		chunkRows, err := s.GetRows(ctx, "messages", GetFilter{IDPrefix: parentID + "#chunk"})
		if err != nil {
			return nil, err
		}
		for _, r := range chunkRows {
			if isMessageChunkID(r.ID, parentID) {
				addID(r.ID)
			}
		}
	}
	return result, nil
}

// UpsertMessages 批量切分、嵌入并幂等写入频道消息向量（对齐 RealtimeRAGHandler + replace_message_chunks）：
//   - 单块消息使用 "{channel_id}:{message_id}" 作为文档 ID；
//   - 多块消息使用 "{channel_id}:{message_id}#chunk_{i}" 作为文档 ID，并在元数据中记录
//     parent_id、chunk_index、total_chunks（同时附带 chunk_count 兼容旧版读取）；
//   - 消息编辑导致分块数缩减（或长文改短文 / 短文改长文）时，自动清理残余旧分块或旧单条 ID；
//   - 嵌入网络调用在锁外一次批量完成，任一向量失败或维度不符绝不覆盖既有可用版本。
func (s *Store) UpsertMessages(ctx context.Context, embedFn EmbedFunc, items []MessageItem) (int, error) {
	commit, err := s.PrepareMessageUpsert(ctx, embedFn, items)
	if err != nil || commit == nil {
		return 0, err
	}
	return commit(nil)
}

// MessageUpsert commits an embedded batch. A non-nil keep predicate selects
// which parent messages to write and remove obsolete chunks for.
type MessageUpsert func(keep func(channelID string, messageID int64) bool) (int, error)

// PrepareMessageUpsert performs embedding before returning the database write.
// Callers can synchronize that write with message edits and deletions.
func (s *Store) PrepareMessageUpsert(ctx context.Context, embedFn EmbedFunc, items []MessageItem) (MessageUpsert, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.readOnly {
		return nil, ErrReadOnly
	}
	if len(items) == 0 {
		return nil, nil
	}

	type itemPlan struct {
		item     MessageItem
		parentID string
		chunks   []string
		embStart int // -1 表示使用预计算的 item.Vector
	}

	plans := make([]itemPlan, 0, len(items))
	var textsToEmbed []string
	for _, item := range items {
		if strings.TrimSpace(item.ChannelID) == "" || item.MessageID <= 0 {
			return nil, fmt.Errorf("无效的消息标识: channel_id=%q message_id=%d", item.ChannelID, item.MessageID)
		}
		if strings.TrimSpace(item.Text) == "" {
			continue
		}
		maxChars := item.ChunkMaxChars
		overlap := item.ChunkOverlap
		if maxChars <= 0 {
			maxChars = DefaultChunkMaxChars
			if overlap == 0 {
				overlap = DefaultChunkOverlap
			}
		}
		chunks := SplitMessageChunks(item.Text, maxChars, overlap)
		if len(chunks) == 0 {
			continue
		}

		parentID := fmt.Sprintf("%s:%d", item.ChannelID, item.MessageID)
		plan := itemPlan{
			item:     item,
			parentID: parentID,
			chunks:   chunks,
			embStart: -1,
		}
		if len(chunks) == 1 && len(item.Vector) > 0 {
			if len(item.Vector) != s.expectedDim {
				return nil, fmt.Errorf("%w: 期望 %d，实际 %d", ErrDimensionMismatch, s.expectedDim, len(item.Vector))
			}
		} else {
			plan.embStart = len(textsToEmbed)
			textsToEmbed = append(textsToEmbed, chunks...)
		}
		plans = append(plans, plan)
	}

	if len(plans) == 0 {
		return nil, nil
	}

	var computedVecs [][]float32
	if len(textsToEmbed) > 0 {
		if embedFn == nil {
			return nil, errors.New("embedFn 不能为空")
		}
		var err error
		computedVecs, err = embedFn(ctx, textsToEmbed)
		if err != nil {
			return nil, fmt.Errorf("批量生成消息向量失败: %w", err)
		}
		if len(computedVecs) != len(textsToEmbed) {
			return nil, fmt.Errorf("消息向量返回数量不匹配: 期望 %d，实际 %d", len(textsToEmbed), len(computedVecs))
		}
		for i, v := range computedVecs {
			if len(v) != s.expectedDim {
				return nil, fmt.Errorf("%w: 第 %d 块向量期望 %d，实际 %d", ErrDimensionMismatch, i, s.expectedDim, len(v))
			}
		}
	}

	// 汇总待写入行（若同批 items 中同一 parentID 出现多次，以最后一次为准）。
	latestPlanIdx := make(map[string]int, len(plans))
	for idx, p := range plans {
		latestPlanIdx[p.parentID] = idx
	}

	parentRows := make(map[string][]WriteRow, len(latestPlanIdx))
	nowISO := formatISOUTC(time.Now().UTC())

	for idx, p := range plans {
		if latestPlanIdx[p.parentID] != idx {
			continue
		}
		totalChunks := len(p.chunks)
		channelName := p.item.ChannelName
		if channelName == "" {
			channelName = p.item.ChannelID
		}

		createdAtStr := nowISO
		if !p.item.CreatedAt.IsZero() {
			createdAtStr = formatISOUTC(p.item.CreatedAt)
		} else if existing, ok := p.item.Metadata["created_at"].(string); ok && existing != "" {
			createdAtStr = existing
		}

		senderStr := ""
		if p.item.SenderID != 0 {
			senderStr = strconv.FormatInt(p.item.SenderID, 10)
		} else if existing, ok := p.item.Metadata["sender_id"].(string); ok {
			senderStr = existing
		}

		for i, chunkText := range p.chunks {
			docID := p.parentID
			if totalChunks > 1 {
				docID = fmt.Sprintf("%s#chunk_%d", p.parentID, i)
			}
			var vec []float32
			if p.embStart < 0 {
				vec = p.item.Vector
			} else {
				vec = computedVecs[p.embStart+i]
			}

			meta := make(map[string]any, len(p.item.Metadata)+9)
			if p.item.Metadata != nil {
				maps.Copy(meta, p.item.Metadata)
			}
			meta["channel_id"] = p.item.ChannelID
			meta["channel_name"] = channelName
			meta["message_id"] = strconv.FormatInt(p.item.MessageID, 10)
			meta["created_at"] = createdAtStr
			meta["sender_id"] = senderStr
			if p.item.ChannelUsername != "" {
				meta["channel_username"] = p.item.ChannelUsername
			}
			meta["parent_id"] = p.parentID
			meta["chunk_index"] = i
			meta["total_chunks"] = totalChunks
			meta["chunk_count"] = totalChunks

			parentRows[p.parentID] = append(parentRows[p.parentID], WriteRow{
				ID:       docID,
				Vector:   vec,
				Document: chunkText,
				Metadata: meta,
			})
		}
	}

	return func(keep func(string, int64) bool) (int, error) {
		var writeRows []WriteRow
		parentList := make([]string, 0, len(parentRows))
		for idx, p := range plans {
			if latestPlanIdx[p.parentID] != idx || (keep != nil && !keep(p.item.ChannelID, p.item.MessageID)) {
				continue
			}
			parentList = append(parentList, p.parentID)
			writeRows = append(writeRows, parentRows[p.parentID]...)
		}
		return s.commitMessageUpsert(ctx, parentList, writeRows)
	}, nil
}

func (s *Store) commitMessageUpsert(ctx context.Context, parentList []string, writeRows []WriteRow) (int, error) {
	if len(writeRows) == 0 {
		return 0, nil
	}
	newIDSet := make(map[string]struct{}, len(writeRows))
	for _, row := range writeRows {
		newIDSet[row.ID] = struct{}{}
	}
	// 先查出已存在的旧 ID，完整写入新块后再清理陈旧尾块/旧单条 ID（防中断半空窗）。
	existingIDs, err := s.findExistingMessageDocIDs(ctx, parentList)
	if err != nil {
		return 0, err
	}
	written, err := s.UpsertMany(ctx, "messages", writeRows)
	if err != nil {
		return 0, err
	}

	var staleIDs []string
	for _, oldID := range existingIDs {
		if _, keep := newIDSet[oldID]; !keep {
			staleIDs = append(staleIDs, oldID)
		}
	}
	if len(staleIDs) > 0 {
		if _, err := s.Delete(ctx, "messages", staleIDs); err != nil {
			return written, err
		}
	}
	return written, nil
}

// DeleteMessages 删除指定频道下的一组消息向量，同时清除精确 "{channel_id}:{message_id}"
// 与全部分块 "{channel_id}:{message_id}#chunk_*"（及历史 "#chunk=*"）。
func (s *Store) DeleteMessages(ctx context.Context, channelID string, messageIDs []int64) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if s.readOnly {
		return 0, ErrReadOnly
	}
	if len(messageIDs) == 0 {
		return 0, nil
	}

	parents := make([]string, 0, len(messageIDs))
	seen := make(map[string]struct{}, len(messageIDs))
	for _, msgID := range messageIDs {
		p := fmt.Sprintf("%s:%d", channelID, msgID)
		if _, ok := seen[p]; !ok {
			seen[p] = struct{}{}
			parents = append(parents, p)
		}
	}

	targetIDs, err := s.findExistingMessageDocIDs(ctx, parents)
	if err != nil {
		return 0, err
	}
	if len(targetIDs) == 0 {
		return 0, nil
	}
	return s.Delete(ctx, "messages", targetIDs)
}
