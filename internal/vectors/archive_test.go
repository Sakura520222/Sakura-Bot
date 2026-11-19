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
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSourcePathMatchesPythonSHA256Contract(t *testing.T) {
	// 对照 Python hashlib.sha256(b"123:9").hexdigest() 真实产物校验。
	got := filepath.Base(SourcePath("data/message_sources", "123", 9))
	want := "c0c24d088f80e15850156ff523af3d10b75a3242580f65a676fe03b7c3cf9b15.json"
	if got != want {
		t.Fatalf("SourcePath 文件名 = %q, 期望 %q", got, want)
	}
}

func TestArchiveReadOverwriteAndDeleteSource(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "message_sources")

	originalText := "  完整🥳\n" + strings.Repeat("x", 900) + "<TAIL>&end  "
	rec := SourceRecord{
		ChannelID:       "123",
		MessageID:       7,
		Text:            originalText,
		CreatedAt:       "2020-01-02T03:04:05+08:00",
		ChannelName:     "测试频道",
		ChannelUsername: "example",
		PostURL:         "https://t.me/example/7",
		Extra: map[string]any{
			"extra": map[string]any{"preserve": true},
		},
	}

	if err := ArchiveSource(dir, rec); err != nil {
		t.Fatalf("ArchiveSource 失败: %v", err)
	}

	// 目录内不应残留临时文件。
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("归档目录应仅有 1 个正式文件, got %d (err=%v)", len(entries), err)
	}

	loaded, err := ReadSource(dir, "123", 7)
	if err != nil {
		t.Fatalf("ReadSource 失败: %v", err)
	}
	if loaded.Text != originalText || loaded.CreatedAt != rec.CreatedAt || loaded.PostURL != rec.PostURL {
		t.Fatalf("ReadSource 核心字段失真: %+v", loaded)
	}
	nested, ok := loaded.Extra["extra"].(map[string]any)
	if !ok || nested["preserve"] != true {
		t.Fatalf("ReadSource 扩展元数据丢失: %+v", loaded.Extra)
	}

	// 覆盖更新同一消息归档。
	rec.Text = "编辑后的完整正文"
	if err := ArchiveSource(dir, rec); err != nil {
		t.Fatalf("二次覆盖 ArchiveSource 失败: %v", err)
	}
	loaded2, err := ReadSource(dir, "123", 7)
	if err != nil || loaded2.Text != "编辑后的完整正文" {
		t.Fatalf("覆盖后读取不符: %+v (err=%v)", loaded2, err)
	}

	// 删除归档（含重复幂等删除）。
	if err := DeleteSource(dir, "123", 7); err != nil {
		t.Fatalf("DeleteSource 失败: %v", err)
	}
	if err := DeleteSource(dir, "123", 7); err != nil {
		t.Fatalf("幂等二次 DeleteSource 失败: %v", err)
	}
	if _, err := ReadSource(dir, "123", 7); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("删除后 ReadSource 应返回 os.ErrNotExist, got %v", err)
	}
}

func TestWalkSourcesAndReindexFromArchiveWithFaultIsolation(t *testing.T) {
	ctx := context.Background()
	archiveDir := filepath.Join(t.TempDir(), "sources")
	vecDir := filepath.Join(t.TempDir(), "vectors")

	shortRec := SourceRecord{
		ChannelID:   "-1001",
		MessageID:   10,
		Text:        "短消息归档正文",
		CreatedAt:   "2026-02-03T04:05:06+08:00",
		ChannelName: "一频道",
	}
	longRec := SourceRecord{
		ChannelID:       "-1001",
		MessageID:       11,
		Text:            strings.Repeat("长消息归档片段内容 ", 140) + "TAIL_MARKER",
		CreatedAt:       "2026-02-04T04:05:06+08:00",
		ChannelName:     "一频道",
		ChannelUsername: "chan_one",
		PostURL:         "https://t.me/chan_one/11",
		Extra:           map[string]any{"custom_tag": "keep-verbatim"},
	}

	if err := ArchiveSource(archiveDir, shortRec); err != nil {
		t.Fatalf("ArchiveSource short 失败: %v", err)
	}
	if err := ArchiveSource(archiveDir, longRec); err != nil {
		t.Fatalf("ArchiveSource long 失败: %v", err)
	}

	longPath := SourcePath(archiveDir, longRec.ChannelID, longRec.MessageID)
	rawLongBefore, err := os.ReadFile(longPath)
	if err != nil {
		t.Fatalf("读取原始归档字节失败: %v", err)
	}

	// 混入损坏 JSON 文件，验证 WalkSources 与 ReindexFromArchive 的单条故障隔离。
	brokenPath := filepath.Join(archiveDir, "0000_broken.json")
	if err := os.WriteFile(brokenPath, []byte("{broken-json"), 0o644); err != nil {
		t.Fatalf("写入损坏文件失败: %v", err)
	}

	var walked []int64
	if err := WalkSources(archiveDir, func(r SourceRecord) error {
		walked = append(walked, r.MessageID)
		return nil
	}); err != nil {
		t.Fatalf("WalkSources 失败: %v", err)
	}
	if len(walked) != 2 {
		t.Fatalf("WalkSources 应遍历 2 条有效记录, got %v", walked)
	}

	store := NewReadWrite(vecDir, rwTestDim)
	t.Cleanup(func() { _ = store.Close() })

	processed, reindexErr := ReindexFromArchive(ctx, archiveDir, store, deterministicEmbed, 1)
	if processed != 2 {
		t.Fatalf("ReindexFromArchive 应成功重建 2 条有效归档, got %d (err=%v)", processed, reindexErr)
	}
	if reindexErr == nil {
		t.Fatal("存在损坏 JSON 时 ReindexFromArchive 应同时报告聚合错误")
	}

	// 原归档文件内容必须逐字节未变。
	rawLongAfter, err := os.ReadFile(longPath)
	if err != nil || !bytes.Equal(rawLongBefore, rawLongAfter) {
		t.Fatal("ReindexFromArchive 不得修改原归档目录中的文件")
	}

	// 校验短消息单条入库且保留原始 +08:00 时间戳字符串。
	doc10, err := store.GetDocument(ctx, "messages", "-1001:10")
	if err != nil || doc10 == nil {
		t.Fatalf("未找到重建的短消息文档: %+v (err=%v)", doc10, err)
	}
	if doc10.Metadata["created_at"] != "2026-02-03T04:05:06+08:00" {
		t.Fatalf("重建后原始时区字符串丢失: %v", doc10.Metadata["created_at"])
	}

	// 校验长消息已按多块写入且尾部可被检索命中。
	tailVec := make([]float32, rwTestDim)
	tailVec[rwTestDim-1] = 1.0
	hits, err := store.Search(ctx, tailVec, Options{Collection: "messages", TopK: 1})
	if err != nil || len(hits) != 1 || !strings.Contains(hits[0].Document, "TAIL_MARKER") {
		t.Fatalf("重建后长消息尾块不可检索: %+v (err=%v)", hits, err)
	}

	// 移除损坏文件后重跑应零错误幂等收敛。
	_ = os.Remove(brokenPath)
	processed2, err := ReindexFromArchive(ctx, archiveDir, store, deterministicEmbed, 10)
	if err != nil || processed2 != 2 {
		t.Fatalf("清理坏文件后重跑失败: processed=%d err=%v", processed2, err)
	}
}
