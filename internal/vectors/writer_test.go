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
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const rwTestDim = 8

func rwBasis(indices ...int) []float32 {
	v := make([]float32, rwTestDim)
	for _, idx := range indices {
		v[idx%rwTestDim] += 1.0
	}
	return v
}

func deterministicEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := make([][]float32, len(texts))
	for i, t := range texts {
		v := make([]float32, rwTestDim)
		var h uint32 = 2166136261
		for j := 0; j < len(t); j++ {
			h ^= uint32(t[j])
			h *= 16777619
		}
		v[int(h%rwTestDim)] = 1.0
		if strings.Contains(t, "TAIL_MARKER") {
			v = make([]float32, rwTestDim)
			v[rwTestDim-1] = 1.0
		}
		out[i] = v
	}
	return out, nil
}

func TestReadWriteInsertDuplicateAndDimensionValidation(t *testing.T) {
	ctx := context.Background()

	// 默认维度回退 1024，且构造期不触碰磁盘。
	lazyDir := filepath.Join(t.TempDir(), "nested", "vectors")
	defStore := NewReadWrite(lazyDir, 0)
	t.Cleanup(func() { _ = defStore.Close() })
	if defStore.ExpectedDim() != DefaultDimension {
		t.Fatalf("ExpectedDim = %d, want %d", defStore.ExpectedDim(), DefaultDimension)
	}
	if defStore.ReadOnly() {
		t.Fatal("NewReadWrite 实例 ReadOnly() 应为 false")
	}
	if _, err := os.Stat(lazyDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("构造期不应提前创建目录")
	}

	// 只读实例对缺失目录绝不建目录/建表，且拒绝所有写操作。
	roEmptyDir := filepath.Join(t.TempDir(), "ro_missing")
	roEmpty := New(roEmptyDir)
	t.Cleanup(func() { _ = roEmpty.Close() })
	if !roEmpty.ReadOnly() {
		t.Fatal("New 实例 ReadOnly() 应为 true")
	}
	if err := roEmpty.EnsureSchema(ctx); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("RO EnsureSchema 期望 ErrReadOnly, got %v", err)
	}
	if err := roEmpty.Insert(ctx, "summaries", "1", rwBasis(0), "doc", nil); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("RO Insert 期望 ErrReadOnly, got %v", err)
	}
	if _, err := os.Stat(roEmptyDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("RO 实例被调用写方法后仍不得创建目录")
	}

	// 读写实例正常建库、写入、主键冲突拦截、维度校验。
	dir := filepath.Join(t.TempDir(), "rw_vectors")
	store := NewReadWrite(dir, rwTestDim)
	t.Cleanup(func() { _ = store.Close() })

	if err := store.EnsureSchema(ctx); err != nil {
		t.Fatalf("EnsureSchema 失败: %v", err)
	}
	// 幂等二次调用。
	if err := store.EnsureSchema(ctx); err != nil {
		t.Fatalf("二次 EnsureSchema 失败: %v", err)
	}

	stats, err := store.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats 失败: %v", err)
	}
	if !stats.Available || stats.SummariesCount != 0 || stats.MessagesCount != 0 || stats.ExpectedDim != rwTestDim {
		t.Fatalf("空库 Stats 异常: %+v", stats)
	}

	if err := store.Insert(ctx, "summaries", "101", rwBasis(2), "总结正文", map[string]any{
		"channel_id": "-10088",
		"created_at": "2026-05-01T00:00:00+00:00",
	}); err != nil {
		t.Fatalf("Insert 失败: %v", err)
	}

	// 重复主键冲突。
	if err := store.Insert(ctx, "summaries", "101", rwBasis(3), "覆盖尝试", nil); !errors.Is(err, ErrDuplicateID) {
		t.Fatalf("重复 ID Insert 期望 ErrDuplicateID, got %v", err)
	}

	// 维度不匹配。
	if err := store.Insert(ctx, "summaries", "102", []float32{1, 2, 3}, "坏维度", nil); !errors.Is(err, ErrDimensionMismatch) {
		t.Fatalf("坏维度 Insert 期望 ErrDimensionMismatch, got %v", err)
	}

	doc, err := store.GetDocument(ctx, "summaries", "101")
	if err != nil {
		t.Fatalf("GetDocument 失败: %v", err)
	}
	if doc == nil || doc.Document != "总结正文" || doc.Dim != rwTestDim || len(doc.Vector) != rwTestDim {
		t.Fatalf("GetDocument 返回内容不符: %+v", doc)
	}
	missing, err := store.GetDocument(ctx, "summaries", "999999")
	if err != nil || missing != nil {
		t.Fatalf("不存在的文档应返回 (nil, nil), got (%+v, %v)", missing, err)
	}
}

func TestUpsertManyAllOrNothingDimensionValidation(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store := NewReadWrite(dir, rwTestDim)
	t.Cleanup(func() { _ = store.Close() })

	if err := store.Insert(ctx, "messages", "10:1", rwBasis(0), "initial", map[string]any{"v": 1}); err != nil {
		t.Fatalf("Insert 失败: %v", err)
	}

	// 批次末尾含坏维度行：整批必须原子拒绝，不覆盖 "10:1"，不新增 "10:2"。
	_, err := store.UpsertMany(ctx, "messages", []WriteRow{
		{ID: "10:1", Vector: rwBasis(1), Document: "mutated", Metadata: map[string]any{"v": 2}},
		{ID: "10:2", Vector: rwBasis(2), Document: "new", Metadata: map[string]any{"v": 2}},
		{ID: "10:3", Vector: []float32{1.0, 0.0}, Document: "bad-dim", Metadata: nil},
	})
	if !errors.Is(err, ErrDimensionMismatch) {
		t.Fatalf("期望 ErrDimensionMismatch, got %v", err)
	}

	cnt, err := store.Count(ctx, "messages")
	if err != nil || cnt != 1 {
		t.Fatalf("回滚后行数应仍为 1, got %d (err=%v)", cnt, err)
	}
	doc, err := store.GetDocument(ctx, "messages", "10:1")
	if err != nil || doc.Document != "initial" {
		t.Fatalf("前置校验失败不得污染已有记录: %+v (err=%v)", doc, err)
	}

	// 合法批量幂等 Upsert。
	n, err := store.UpsertMany(ctx, "messages", []WriteRow{
		{ID: "10:1", Vector: rwBasis(1), Document: "updated-1", Metadata: map[string]any{"v": 2}},
		{ID: "10:2", Vector: rwBasis(2), Document: "inserted-2", Metadata: map[string]any{"v": 2}},
	})
	if err != nil || n != 2 {
		t.Fatalf("UpsertMany 期望写入 2 行, got %d (err=%v)", n, err)
	}
	doc, _ = store.GetDocument(ctx, "messages", "10:1")
	if doc.Document != "updated-1" {
		t.Fatalf("UpsertMany 未覆盖已有文档: %+v", doc)
	}
}

func TestChunkedDeleteOver500GetRowsPaginationPrefixAndClear(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store := NewReadWrite(dir, rwTestDim)
	t.Cleanup(func() { _ = store.Close() })

	const totalRows = 1250
	rows := make([]WriteRow, 0, totalRows)
	allIDs := make([]string, 0, totalRows)
	for i := 0; i < totalRows; i++ {
		prefix := "chanA"
		if i >= 1000 {
			prefix = "chanB"
		}
		id := fmt.Sprintf("%s:%04d", prefix, i)
		allIDs = append(allIDs, id)
		rows = append(rows, WriteRow{
			ID:       id,
			Vector:   rwBasis(i % rwTestDim),
			Document: fmt.Sprintf("doc-%04d", i),
			Metadata: map[string]any{"idx": i},
		})
	}
	if n, err := store.UpsertMany(ctx, "messages", rows); err != nil || n != totalRows {
		t.Fatalf("批量写入 %d 行失败: n=%d err=%v", totalRows, n, err)
	}

	// 测试前缀扫描：chanB: 应恰好命中 250 条且按 id 升序。
	prefixRows, err := store.GetRows(ctx, "messages", GetFilter{
		IDPrefix:    "chanB:",
		IncludeDocs: true,
	})
	if err != nil || len(prefixRows) != 250 {
		t.Fatalf("IDPrefix 期望 250 条, got %d (err=%v)", len(prefixRows), err)
	}
	if prefixRows[0].ID != "chanB:1000" || prefixRows[249].ID != "chanB:1249" {
		t.Fatalf("IDPrefix 排序边界异常: first=%s last=%s", prefixRows[0].ID, prefixRows[249].ID)
	}

	// 测试分页（ORDER BY id）。
	page, err := store.GetRows(ctx, "messages", GetFilter{
		IDPrefix:    "chanA:",
		Limit:       3,
		Offset:      10,
		IncludeDocs: true,
		IncludeMeta: true,
	})
	if err != nil || len(page) != 3 {
		t.Fatalf("分页查询失败: len=%d err=%v", len(page), err)
	}
	if page[0].ID != "chanA:0010" || page[2].ID != "chanA:0012" || page[0].Document != "doc-0010" {
		t.Fatalf("分页内容不符: %+v", page)
	}

	// 测试超过 500 个 ID 的 GetRows 查询。
	bigLookup, err := store.GetRows(ctx, "messages", GetFilter{IDs: allIDs[:620]})
	if err != nil || len(bigLookup) != 620 {
		t.Fatalf(">500 IDs GetRows 失败: len=%d err=%v", len(bigLookup), err)
	}

	// 测试超过 500 个 ID 的分块删除（1100 个真实 ID + 30 个不存在 ID）。
	delIDs := append([]string{}, allIDs[:1100]...)
	for i := 0; i < 30; i++ {
		delIDs = append(delIDs, fmt.Sprintf("missing:%d", i))
	}
	deleted, err := store.Delete(ctx, "messages", delIDs)
	if err != nil || deleted != 1100 {
		t.Fatalf("分块 Delete 期望删除 1100 条, got %d (err=%v)", deleted, err)
	}

	// 再次删除已不存在的 ID：deleted == 0，且版本号不再自增。
	verBefore, err := store.readVersions(ctx)
	if err != nil {
		t.Fatalf("读取版本失败: %v", err)
	}
	deletedAgain, err := store.Delete(ctx, "messages", []string{"missing:1", "chanA:0001"})
	if err != nil || deletedAgain != 0 {
		t.Fatalf("幂等空删期望 0, got %d (err=%v)", deletedAgain, err)
	}
	verAfter, err := store.readVersions(ctx)
	if err != nil || verAfter != verBefore {
		t.Fatalf("删除 0 行不应自增版本号: before=%+v after=%+v", verBefore, verAfter)
	}

	// ClearCollection 清空剩余 150 条。
	cleared, err := store.ClearCollection(ctx, "messages")
	if err != nil || cleared != 150 {
		t.Fatalf("ClearCollection 期望清空 150 条, got %d (err=%v)", cleared, err)
	}
	if cnt, _ := store.Count(ctx, "messages"); cnt != 0 {
		t.Fatalf("ClearCollection 后行数应为 0, got %d", cnt)
	}
	// 空集合再次 ClearCollection 不自增版本号。
	verBefore, _ = store.readVersions(ctx)
	if cleared2, err := store.ClearCollection(ctx, "messages"); err != nil || cleared2 != 0 {
		t.Fatalf("空集合 ClearCollection 期望 0, got %d (err=%v)", cleared2, err)
	}
	verAfter, _ = store.readVersions(ctx)
	if verAfter != verBefore {
		t.Fatalf("空集合 ClearCollection 不应自增版本号: before=%+v after=%+v", verBefore, verAfter)
	}
}

func TestIncrementalSnapshotAndCrossHandleReadOnlySync(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	rw := NewReadWrite(dir, rwTestDim)
	t.Cleanup(func() { _ = rw.Close() })
	ro := New(dir)
	t.Cleanup(func() { _ = ro.Close() })

	// 建库前只读句柄报 ErrNotReady。
	if err := ro.Ready(ctx); !errors.Is(err, ErrNotReady) {
		t.Fatalf("建库前 RO Ready 应返回 ErrNotReady, got %v", err)
	}

	// RW 首次写入触发建库，先触发 RW 与 RO 各自加载初始内存快照。
	if err := rw.Insert(ctx, "summaries", "2", rwBasis(0), "doc-2", map[string]any{"channel_id": "c1"}); err != nil {
		t.Fatalf("Insert 2 失败: %v", err)
	}
	if hits, err := rw.Search(ctx, rwBasis(0), Options{Collection: "summaries"}); err != nil || len(hits) != 1 {
		t.Fatalf("RW 初始 Search 失败: hits=%d err=%v", len(hits), err)
	}
	if hits, err := ro.Search(ctx, rwBasis(0), Options{Collection: "summaries"}); err != nil || len(hits) != 1 {
		t.Fatalf("RO 初始 Search 失败: hits=%d err=%v", len(hits), err)
	}

	// RW 继续插入 ID="1"（字典序在 "2" 之前，同相似度并列时应排在 "2" 前面）。
	if err := rw.Insert(ctx, "summaries", "1", rwBasis(0), "doc-1", map[string]any{"channel_id": "c1"}); err != nil {
		t.Fatalf("Insert 1 失败: %v", err)
	}

	// 校验 RW 的内存快照已在事务提交时直接增量更新到最新版本号（无需 Search 整表重载）。
	snapPtr := rw.current.Load()
	dbVers, err := rw.readVersions(ctx)
	if err != nil {
		t.Fatalf("readVersions 失败: %v", err)
	}
	if snapPtr == nil || *snapPtr == nil || (*snapPtr).versionsChanged(dbVers) {
		t.Fatalf("RW 写入后内存快照版本号未增量对齐磁盘: snap=%+v db=%+v", *snapPtr, dbVers)
	}

	// RW 进程内 Search 立即命中且保持 ORDER BY id 并列序 ("1" 在 "2" 前)。
	rwHits, err := rw.Search(ctx, rwBasis(0), Options{Collection: "summaries", TopK: 5})
	if err != nil || len(rwHits) != 2 || rwHits[0].DocID != "1" || rwHits[1].DocID != "2" {
		t.Fatalf("RW 增量快照 Search 结果异常: %+v (err=%v)", rwHits, err)
	}

	// 跨句柄 RO 实例通过 store_meta 版本号感知变化并重载看到最新结果。
	roHits, err := ro.Search(ctx, rwBasis(0), Options{Collection: "summaries", TopK: 5})
	if err != nil || len(roHits) != 2 || roHits[0].DocID != "1" || roHits[1].DocID != "2" {
		t.Fatalf("RO 跨句柄版本感知 Search 结果异常: %+v (err=%v)", roHits, err)
	}

	// 模拟底层 SQLite 连接意外关闭后的瞬态重连自愈。
	rw.mu.Lock()
	_ = rw.db.Close() // 保留非 nil 的已关闭 *sql.DB，触发首轮操作报错并自动重连重试
	rw.mu.Unlock()

	if err := rw.Insert(ctx, "summaries", "3", rwBasis(1), "doc-3-after-reconnect", nil); err != nil {
		t.Fatalf("连接异常后自动重建重试失败: %v", err)
	}
	roHits, err = ro.Search(ctx, rwBasis(1), Options{Collection: "summaries", TopK: 1})
	if err != nil || len(roHits) != 1 || roHits[0].DocID != "3" {
		t.Fatalf("重连写入后 RO 未检索到新记录: %+v (err=%v)", roHits, err)
	}
}

func TestHighLevelSummaryAndMultiChunkMessageLifecycle(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store := NewReadWrite(dir, rwTestDim)
	t.Cleanup(func() { _ = store.Close() })

	// 1) SplitMessageChunks Unicode 无损与重叠校验。
	sample := "首段中文🥳 " + strings.Repeat("代码块 def f(): return 1\n", 60) + "TAIL_MARKER"
	chunks := SplitMessageChunks(sample, 120, 20)
	if len(chunks) < 5 {
		t.Fatalf("期望切出多块, got %d", len(chunks))
	}
	if !strings.HasSuffix(chunks[len(chunks)-1], "TAIL_MARKER") {
		t.Fatalf("尾块丢失尾部标记: %q", chunks[len(chunks)-1])
	}
	noOverlap := SplitMessageChunks(sample, 120, 0)
	if strings.Join(noOverlap, "") != sample {
		t.Fatal("overlap=0 分块拼接不等于原文")
	}

	// 2) AddSummary 写入与重复拦截。
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	if err := store.AddSummary(ctx, deterministicEmbed, 77, "-1001", "测试频道", "今日总结内容", now.Add(-time.Hour), now, now, 42); err != nil {
		t.Fatalf("AddSummary 失败: %v", err)
	}
	if err := store.AddSummary(ctx, deterministicEmbed, 77, "-1001", "测试频道", "重复总结", time.Time{}, time.Time{}, now, 1); !errors.Is(err, ErrDuplicateID) {
		t.Fatalf("重复 AddSummary 期望 ErrDuplicateID, got %v", err)
	}

	// 3) UpsertMessages：长消息分 4+ 块 -> 编辑缩减为 2 块 -> 再编辑缩减为单块短消息，验证旧尾块被彻底删除。
	longText := strings.Repeat("第一版长文内容段落测试 ", 180) + "TAIL_MARKER"
	written1, err := store.UpsertMessages(ctx, deterministicEmbed, []MessageItem{
		{
			ChannelID:       "-10099",
			ChannelName:     "资讯频道",
			ChannelUsername: "news_chan",
			MessageID:       55,
			SenderID:        888,
			Text:            longText,
			CreatedAt:       now,
			ChunkMaxChars:   400,
			ChunkOverlap:    50,
		},
		// 邻居消息 ID=550，用于确认前缀清理绝不误伤 "-10099:550"。
		{
			ChannelID: "-10099",
			MessageID: 550,
			Text:      "邻居短消息",
			CreatedAt: now,
		},
	})
	if err != nil || written1 < 4 {
		t.Fatalf("首次长消息 UpsertMessages 失败: written=%d err=%v", written1, err)
	}

	// 检索尾部标记应精准命中尾块。
	tailVec := make([]float32, rwTestDim)
	tailVec[rwTestDim-1] = 1.0
	hits, err := store.Search(ctx, tailVec, Options{Collection: "messages", TopK: 1})
	if err != nil || len(hits) != 1 || !strings.Contains(hits[0].Document, "TAIL_MARKER") {
		t.Fatalf("长消息尾块检索失败: %+v (err=%v)", hits, err)
	}
	if hits[0].Metadata["parent_id"] != "-10099:55" {
		t.Fatalf("parent_id 元数据错误: %+v", hits[0].Metadata)
	}

	// 编辑消息：缩减为仅 2 块的中等长度文本（不含 TAIL_MARKER），旧 #chunk_2..N 必须全部被清理。
	midText := strings.Repeat("编辑后中等长度文本 ", 45)
	written2, err := store.UpsertMessages(ctx, deterministicEmbed, []MessageItem{
		{
			ChannelID:     "-10099",
			ChannelName:   "资讯频道",
			MessageID:     55,
			Text:          midText,
			CreatedAt:     now,
			ChunkMaxChars: 400,
			ChunkOverlap:  50,
		},
	})
	if err != nil || written2 != 2 {
		t.Fatalf("编辑为 2 块期望写入 2 行, got %d (err=%v)", written2, err)
	}
	msg55Rows, err := store.GetRows(ctx, "messages", GetFilter{IDPrefix: "-10099:55#chunk_", IncludeMeta: true})
	if err != nil || len(msg55Rows) != 2 {
		t.Fatalf("缩减分块后应只剩 2 个 chunk，实际 %d: %+v (err=%v)", len(msg55Rows), msg55Rows, err)
	}
	if msg55Rows[0].ID != "-10099:55#chunk_0" || msg55Rows[1].ID != "-10099:55#chunk_1" {
		t.Fatalf("剩余分块 ID 不符: %+v", msg55Rows)
	}
	if int(msg55Rows[0].Metadata["total_chunks"].(float64)) != 2 {
		t.Fatalf("total_chunks 未更新为 2: %+v", msg55Rows[0].Metadata)
	}

	// 再次编辑消息：缩减为单块短文本 -> 所有 #chunk_* 被删除，只保留 "-10099:55"。
	written3, err := store.UpsertMessages(ctx, deterministicEmbed, []MessageItem{
		{
			ChannelID: "-10099",
			MessageID: 55,
			Text:      "最终极短版本",
			CreatedAt: now,
		},
	})
	if err != nil || written3 != 1 {
		t.Fatalf("编辑为单块期望写入 1 行, got %d (err=%v)", written3, err)
	}
	leftChunks, _ := store.GetRows(ctx, "messages", GetFilter{IDPrefix: "-10099:55#chunk_"})
	if len(leftChunks) != 0 {
		t.Fatalf("长文改短文后仍有残留分块: %+v", leftChunks)
	}
	singleDoc, err := store.GetDocument(ctx, "messages", "-10099:55")
	if err != nil || singleDoc == nil || singleDoc.Document != "最终极短版本" {
		t.Fatalf("单块文档读取异常: %+v (err=%v)", singleDoc, err)
	}

	// DeleteMessages：删除 55（不误删邻居 550）。
	delCnt, err := store.DeleteMessages(ctx, "-10099", []int64{55})
	if err != nil || delCnt != 1 {
		t.Fatalf("DeleteMessages(55) 期望删除 1 条, got %d (err=%v)", delCnt, err)
	}
	neighbor, err := store.GetDocument(ctx, "messages", "-10099:550")
	if err != nil || neighbor == nil {
		t.Fatalf("邻居消息 -10099:550 被误删: %+v (err=%v)", neighbor, err)
	}
}
