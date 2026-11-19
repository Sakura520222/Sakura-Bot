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
	"math"
	"os"
	"path/filepath"
	"testing"
)

const chromaTestDim = 8

// syntheticChromaSchema 复刻 ChromaDB PersistentClient 的核心 SQLite 表结构（0.5/1.x 形态）。
const syntheticChromaSchema = `
CREATE TABLE collections (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    dimension INTEGER
);
CREATE TABLE segments (
    id TEXT PRIMARY KEY,
    type TEXT NOT NULL,
    scope TEXT NOT NULL,
    collection TEXT NOT NULL
);
CREATE TABLE embeddings (
    id INTEGER PRIMARY KEY,
    segment_id TEXT NOT NULL,
    embedding_id TEXT NOT NULL,
    seq_id BLOB NOT NULL
);
CREATE TABLE embedding_metadata (
    id INTEGER NOT NULL,
    key TEXT NOT NULL,
    string_value TEXT,
    int_value INTEGER,
    float_value REAL,
    bool_value INTEGER,
    PRIMARY KEY (id, key)
);
CREATE TABLE max_seq_id (
    segment_id TEXT PRIMARY KEY,
    seq_id INTEGER NOT NULL
);
CREATE TABLE embeddings_queue (
    seq_id INTEGER PRIMARY KEY,
    operation INTEGER NOT NULL,
    topic TEXT NOT NULL,
    id TEXT NOT NULL,
    vector BLOB,
    encoding TEXT,
    metadata TEXT
);
`

type syntheticHNSWElement struct {
	label   uint64
	vec     []float32 // 归一化向量（存入 data_level0.bin）
	norm    float32   // 原范数（存入 length.bin）
	deleted bool
}

// writeSyntheticHNSWSegment 按 chroma-hnswlib 100 字节 header.bin + data_level0.bin + length.bin
// + index_metadata.pickle 真实二进制布局写出 HNSW 向量段文件。
func writeSyntheticHNSWSegment(
	t *testing.T,
	segDir string,
	dim int,
	elements []syntheticHNSWElement,
	pickleBytes []byte,
) {
	t.Helper()
	if err := os.MkdirAll(segDir, 0o755); err != nil {
		t.Fatalf("创建 HNSW 段目录失败: %v", err)
	}

	const (
		offsetData = uint64(132) // 32 links * 4 + 4 linklistsizeint
	)
	labelOffset := offsetData + uint64(dim*4)
	sizePerElem := labelOffset + 8
	maxElements := uint64(len(elements) + 16)
	curCount := uint64(len(elements))

	// 100 字节 chroma-hnswlib header.bin（首 4 字节为 version=1）。
	hdr := make([]byte, hnswHeaderVersionedBytes)
	binary.LittleEndian.PutUint32(hdr[0:4], 1)
	binary.LittleEndian.PutUint64(hdr[4:12], 0) // offsetLevel0_
	binary.LittleEndian.PutUint64(hdr[12:20], maxElements)
	binary.LittleEndian.PutUint64(hdr[20:28], curCount)
	binary.LittleEndian.PutUint64(hdr[28:36], sizePerElem)
	binary.LittleEndian.PutUint64(hdr[36:44], labelOffset)
	binary.LittleEndian.PutUint64(hdr[44:52], offsetData)
	binary.LittleEndian.PutUint32(hdr[52:56], 0)  // maxlevel_
	binary.LittleEndian.PutUint32(hdr[56:60], 0)  // enterpoint_node_
	binary.LittleEndian.PutUint64(hdr[60:68], 16) // maxM_
	binary.LittleEndian.PutUint64(hdr[68:76], 32) // maxM0_
	binary.LittleEndian.PutUint64(hdr[76:84], 16) // M_
	binary.LittleEndian.PutUint64(hdr[84:92], math.Float64bits(1.0/math.Log(16)))
	binary.LittleEndian.PutUint64(hdr[92:100], 100) // ef_construction_

	dataLevel0 := make([]byte, maxElements*sizePerElem)
	lengths := make([]byte, maxElements*4)

	for i, el := range elements {
		base := uint64(i) * sizePerElem
		if el.deleted {
			// hnswlib linklistsizeint 第 16 位为删除标记。
			binary.LittleEndian.PutUint32(dataLevel0[base:base+4], 1<<16)
		}
		copy(dataLevel0[base+offsetData:base+labelOffset], encodeFloat32LE(el.vec))
		binary.LittleEndian.PutUint64(dataLevel0[base+labelOffset:base+labelOffset+8], el.label)

		norm := el.norm
		if norm <= 0 {
			norm = 1.0
		}
		binary.LittleEndian.PutUint32(lengths[i*4:(i+1)*4], math.Float32bits(norm))
	}

	if err := os.WriteFile(filepath.Join(segDir, "header.bin"), hdr, 0o644); err != nil {
		t.Fatalf("写 header.bin 失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(segDir, "data_level0.bin"), dataLevel0, 0o644); err != nil {
		t.Fatalf("写 data_level0.bin 失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(segDir, "length.bin"), lengths, 0o644); err != nil {
		t.Fatalf("写 length.bin 失败: %v", err)
	}
	if len(pickleBytes) > 0 {
		if err := os.WriteFile(filepath.Join(segDir, "index_metadata.pickle"), pickleBytes, 0o644); err != nil {
			t.Fatalf("写 index_metadata.pickle 失败: %v", err)
		}
	}
}

// buildChromaDictPickle 构造协议 3 的 index_metadata.pickle（复刻 Chroma 1.x Rust 持久层字节流）。
func buildChromaDictPickle(idToLabel map[string]int32) []byte {
	var b bytes.Buffer
	b.Write([]byte{0x80, 0x03}) // PROTO 3
	b.WriteByte('}')            // EMPTY_DICT (root)
	b.WriteByte('(')            // MARK
	writeStr := func(s string) {
		b.WriteByte('X')
		var n [4]byte
		binary.LittleEndian.PutUint32(n[:], uint32(len(s)))
		b.Write(n[:])
		b.WriteString(s)
	}
	writeInt := func(v int32) {
		b.WriteByte('J')
		var n [4]byte
		binary.LittleEndian.PutUint32(n[:], uint32(v))
		b.Write(n[:])
	}
	writeStr("dimensionality")
	b.WriteByte('N')
	writeStr("total_elements_added")
	writeInt(int32(len(idToLabel)))
	writeStr("id_to_label")
	b.WriteByte('}')
	b.WriteByte('(')
	for id, lbl := range idToLabel {
		writeStr(id)
		writeInt(lbl)
	}
	b.WriteByte('u') // SETITEMS on id_to_label
	b.WriteByte('u') // SETITEMS on root
	b.WriteByte('.') // STOP
	return b.Bytes()
}

// buildChromaPersistentDataPickle 构造协议 4 PersistentData 实例形态的 index_metadata.pickle（复刻 Chroma 0.4/0.5）。
func buildChromaPersistentDataPickle(idToLabel map[string]int32) []byte {
	var b bytes.Buffer
	b.Write([]byte{0x80, 0x04}) // PROTO 4
	writeShortStr := func(s string) {
		b.WriteByte(0x8c)
		b.WriteByte(byte(len(s)))
		b.WriteString(s)
	}
	writeShortStr("chromadb.segment.impl.vector.local_persistent_hnsw")
	writeShortStr("PersistentData")
	b.WriteByte(0x93) // STACK_GLOBAL
	b.WriteByte(')')  // EMPTY_TUPLE
	b.WriteByte(0x81) // NEWOBJ
	b.WriteByte('}')  // state dict
	b.WriteByte('(')
	writeShortStr("id_to_label")
	b.WriteByte('}')
	b.WriteByte('(')
	for id, lbl := range idToLabel {
		writeShortStr(id)
		b.WriteByte('K')
		b.WriteByte(byte(lbl))
	}
	b.WriteByte('u')
	b.WriteByte('u')
	b.WriteByte('b') // BUILD -> attaches state dict to PersistentData object
	b.WriteByte('.') // STOP
	return b.Bytes()
}

func TestMigrateChromaToSQLiteEndToEnd(t *testing.T) {
	ctx := context.Background()
	vecDir := filepath.Join(t.TempDir(), "vectors")
	markerPath := filepath.Join(t.TempDir(), ".vector_migration_done")
	if err := os.MkdirAll(vecDir, 0o755); err != nil {
		t.Fatal(err)
	}

	chromaFile := filepath.Join(vecDir, "chroma.sqlite3")
	cdb, err := sql.Open("sqlite", filepath.ToSlash(chromaFile))
	if err != nil {
		t.Fatalf("创建合成 chroma.sqlite3 失败: %v", err)
	}
	mustExec(t, cdb, syntheticChromaSchema)

	const (
		sumColID     = "col-summaries-uuid"
		sumMetaSegID = "seg-sum-meta"
		sumVecSegID  = "seg-sum-vec"

		msgColID     = "col-messages-uuid"
		msgMetaSegID = "seg-msg-meta"
		msgVecSegID  = "seg-msg-vec"
	)

	// 1. 注册 collections 与 segments。
	mustExec(t, cdb, "INSERT INTO collections (id, name, dimension) VALUES (?, 'summaries', ?), (?, 'messages', ?)",
		sumColID, chromaTestDim, msgColID, chromaTestDim)
	mustExec(t, cdb, `INSERT INTO segments (id, type, scope, collection) VALUES
		(?, 'urn:chroma:segment/metadata/sqlite', 'METADATA', ?),
		(?, 'urn:chroma:segment/vector/hnsw-local-persisted', 'VECTOR', ?),
		(?, 'urn:chroma:segment/metadata/sqlite', 'METADATA', ?),
		(?, 'urn:chroma:segment/vector/hnsw-local-persisted', 'VECTOR', ?)`,
		sumMetaSegID, sumColID,
		sumVecSegID, sumColID,
		msgMetaSegID, msgColID,
		msgVecSegID, msgColID,
	)

	// 2. summaries 集合：模拟未满 sync_threshold 仅存在于 embeddings_queue WAL 的场景，含 string/int/float/bool 元数据。
	sumVec1 := []float32{3.0, 4.0, 0, 0, 0, 0, 0, 0} // 非单位向量，检验余弦保真
	sumVec2 := rwBasis(2)
	mustExec(t, cdb, "INSERT INTO embeddings (id, segment_id, embedding_id, seq_id) VALUES (1, ?, '10', 1), (2, ?, '20', 2)",
		sumMetaSegID, sumMetaSegID)
	mustExec(t, cdb, `INSERT INTO embedding_metadata (id, key, string_value, int_value, float_value, bool_value) VALUES
		(1, 'chroma:document', '总结十号正文', NULL, NULL, NULL),
		(1, 'channel_id', '-10010', NULL, NULL, NULL),
		(1, 'message_count', NULL, 42, NULL, NULL),
		(1, 'quality_score', NULL, NULL, 0.95, NULL),
		(1, 'pinned', NULL, NULL, NULL, 1),
		(2, 'chroma:document', '总结二十号正文', NULL, NULL, NULL),
		(2, 'channel_id', '-10020', NULL, NULL, NULL),
		(2, 'pinned', NULL, NULL, NULL, 0)`)
	sumTopic := "persistent://default/default/" + sumColID
	mustExec(t, cdb, `INSERT INTO embeddings_queue (seq_id, operation, topic, id, vector, encoding, metadata) VALUES
		(1, 0, ?, '10', ?, 'FLOAT32', '{}'),
		(2, 0, ?, '20', ?, 'FLOAT32', '{}')`,
		sumTopic, encodeFloat32LE(sumVec1), sumTopic, encodeFloat32LE(sumVec2))

	// 3. messages 集合：HNSW 段存有已落盘向量（含已标记删除项 + length.bin 缩放 + 0.4/0.5 PersistentData pickle）
	//    同时在 embeddings_queue 中有一条 seq_id > max_seq_id 的最新 UPSERT 覆盖。
	mustExec(t, cdb, "INSERT INTO embeddings (id, segment_id, embedding_id, seq_id) VALUES (3, ?, '500:1', 3), (4, ?, '500:2', 5)",
		msgMetaSegID, msgMetaSegID)
	mustExec(t, cdb, `INSERT INTO embedding_metadata (id, key, string_value, int_value, float_value, bool_value) VALUES
		(3, 'chroma:document', '消息一原文', NULL, NULL, NULL),
		(3, 'channel_id', '500', NULL, NULL, NULL),
		(3, 'message_id', '1', NULL, NULL, NULL),
		(4, 'chroma:document', '消息二被队列更新后正文', NULL, NULL, NULL),
		(4, 'channel_id', '500', NULL, NULL, NULL),
		(4, 'message_id', '2', NULL, NULL, NULL)`)
	mustExec(t, cdb, "INSERT INTO max_seq_id (segment_id, seq_id) VALUES (?, 4), (?, 5)", msgVecSegID, msgMetaSegID)

	// HNSW 中存有：
	// - label=99（已删除的旧消息，不应被读取）
	// - label=11 -> '500:1'（归一化 [0.6, 0.8, 0...] * length.bin 5.0 = [3, 4, 0...] -> 这里用正交方向 [0,0,0,0.6,0.8,0,0,0] * 5.0）
	// - label=12 -> '500:2'（旧向量 rwBasis(5)，将被队列 seq_id=5 覆盖为 rwBasis(7)）
	msg1Normed := []float32{0, 0, 0, 0.6, 0.8, 0, 0, 0}
	msg1Expected := []float32{0, 0, 0, 3.0, 4.0, 0, 0, 0}
	msg2Updated := rwBasis(7)

	pkl := buildChromaPersistentDataPickle(map[string]int32{
		"500:1": 11,
		"500:2": 12,
	})
	writeSyntheticHNSWSegment(t, filepath.Join(vecDir, msgVecSegID), chromaTestDim, []syntheticHNSWElement{
		{label: 99, vec: rwBasis(0), norm: 1.0, deleted: true},
		{label: 11, vec: msg1Normed, norm: 5.0, deleted: false},
		{label: 12, vec: rwBasis(5), norm: 1.0, deleted: false},
	}, pkl)

	msgTopic := "persistent://default/default/" + msgColID
	mustExec(t, cdb, `INSERT INTO embeddings_queue (seq_id, operation, topic, id, vector, encoding, metadata) VALUES
		(5, 2, ?, '500:2', ?, 'FLOAT32', '{}')`,
		msgTopic, encodeFloat32LE(msg2Updated))

	// 关闭源库以确保干净停机窗口（无 -wal/-shm/-journal 伴生文件）。
	if err := cdb.Close(); err != nil {
		t.Fatalf("关闭合成 chroma.sqlite3 失败: %v", err)
	}

	// 4. 执行迁移！
	report, err := MigrateChromaToSQLite(ctx, MigrateOptions{
		VectorDBPath: vecDir,
		MarkerPath:   markerPath,
		ExpectedDim:  chromaTestDim,
	})
	if err != nil {
		t.Fatalf("MigrateChromaToSQLite 失败: %v", err)
	}
	if report.Skipped || !report.Verified || report.SummariesMigrated != 2 || report.MessagesMigrated != 2 {
		t.Fatalf("迁移报告不符预期: %+v", report)
	}
	if _, err := os.Stat(markerPath); err != nil {
		t.Fatalf("未生成幂等标记文件: %v", err)
	}
	for _, suffix := range []string{".tmp", ".tmp-wal", ".tmp-shm", "-wal", "-shm"} {
		if _, err := os.Stat(report.OutputDBPath + suffix); err == nil {
			t.Fatalf("迁移完成后不应残留临时或伴生文件: %s", report.OutputDBPath+suffix)
		}
	}

	// 5. 幂等重跑（无 Force）必须直接跳过。
	skippedReport, err := MigrateChromaToSQLite(ctx, MigrateOptions{
		VectorDBPath: vecDir,
		MarkerPath:   markerPath,
		ExpectedDim:  chromaTestDim,
	})
	if err != nil || !skippedReport.Skipped {
		t.Fatalf("已存在标记时重跑应 Skipped=true: report=%+v err=%v", skippedReport, err)
	}

	// 6. VerifyOnly 模式复查通过。
	verifyReport, err := MigrateChromaToSQLite(ctx, MigrateOptions{
		VectorDBPath: vecDir,
		MarkerPath:   markerPath,
		ExpectedDim:  chromaTestDim,
		VerifyOnly:   true,
	})
	if err != nil || !verifyReport.Verified {
		t.Fatalf("VerifyOnly 校验失败: report=%+v err=%v", verifyReport, err)
	}

	// 7. 用只读 Store（sakura-qa 同款契约）打开迁移产物并核验检索与全类型元数据。
	roStore := New(vecDir)
	t.Cleanup(func() { _ = roStore.Close() })

	sumDoc10, err := roStore.GetDocument(ctx, "summaries", "10")
	if err != nil || sumDoc10 == nil {
		t.Fatalf("读取迁移后 summaries/10 失败: %+v (err=%v)", sumDoc10, err)
	}
	if sumDoc10.Document != "总结十号正文" ||
		sumDoc10.Metadata["channel_id"] != "-10010" ||
		sumDoc10.Metadata["message_count"] != float64(42) ||
		sumDoc10.Metadata["pinned"] != true {
		t.Fatalf("summaries/10 文档或元数据失真: %+v", sumDoc10)
	}
	if _, leaked := sumDoc10.Metadata["chroma:document"]; leaked {
		t.Fatalf("内部键 chroma:document 不应泄漏进用户 metadata: %+v", sumDoc10.Metadata)
	}
	if cos := cosineSimilarity(sumDoc10.Vector, sumVec1); cos <= migrateCosineThreshold {
		t.Fatalf("summaries/10 余弦保真度不足: %.8f", cos)
	}

	msgDoc1, err := roStore.GetDocument(ctx, "messages", "500:1")
	if err != nil || msgDoc1 == nil {
		t.Fatalf("读取迁移后 messages/500:1 失败: %+v (err=%v)", msgDoc1, err)
	}
	if cos := cosineSimilarity(msgDoc1.Vector, msg1Expected); cos <= migrateCosineThreshold {
		t.Fatalf("HNSW + length.bin 还原向量余弦不足: got=%v want=%v cos=%.8f", msgDoc1.Vector, msg1Expected, cos)
	}

	// 校验队列新序号 UPSERT 已覆盖 HNSW 旧向量。
	hits, err := roStore.Search(ctx, msg2Updated, Options{Collection: "messages", TopK: 1})
	if err != nil || len(hits) != 1 || hits[0].DocID != "500:2" || math.Abs(hits[0].Similarity-1.0) > 1e-6 {
		t.Fatalf("messages/500:2 队列最新覆盖向量检索异常: %+v (err=%v)", hits, err)
	}
}

func TestMigrateChromaGuardsDictPickleAndFallbackEmbed(t *testing.T) {
	ctx := context.Background()
	vecDir := filepath.Join(t.TempDir(), "vectors")
	markerPath := filepath.Join(t.TempDir(), ".marker")
	if err := os.MkdirAll(vecDir, 0o755); err != nil {
		t.Fatal(err)
	}

	chromaFile := filepath.Join(vecDir, "chroma.sqlite3")
	cdb, err := sql.Open("sqlite", filepath.ToSlash(chromaFile))
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, cdb, syntheticChromaSchema)
	mustExec(t, cdb, "INSERT INTO collections (id, name, dimension) VALUES ('c-sum', 'summaries', ?), ('c-msg', 'messages', ?)",
		chromaTestDim, chromaTestDim)
	_ = cdb.Close()

	// 1. 空源守卫：AllowEmpty=false 时拒绝生成空库。
	if _, err := MigrateChromaToSQLite(ctx, MigrateOptions{
		VectorDBPath: vecDir,
		MarkerPath:   markerPath,
		ExpectedDim:  chromaTestDim,
	}); err == nil {
		t.Fatal("双集合全空且未设 AllowEmpty 时应拒绝迁移")
	}

	// AllowEmpty=true 时允许生成空库。
	emptyRep, err := MigrateChromaToSQLite(ctx, MigrateOptions{
		VectorDBPath: vecDir,
		MarkerPath:   markerPath,
		ExpectedDim:  chromaTestDim,
		AllowEmpty:   true,
	})
	if err != nil || !emptyRep.Verified || emptyRep.SummariesMigrated != 0 {
		t.Fatalf("AllowEmpty=true 迁移失败: rep=%+v err=%v", emptyRep, err)
	}

	// 2. 写入一条仅含文档无向量的记录 + 一条带 Chroma 1.x dict pickle HNSW 的记录，并验证停机伴生文件守卫与 FallbackEmbed。
	cdb, err = sql.Open("sqlite", filepath.ToSlash(chromaFile))
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, cdb, `INSERT INTO segments (id, type, scope, collection) VALUES
		('s-sum-meta', 'sqlite', 'METADATA', 'c-sum'),
		('s-msg-meta', 'sqlite', 'METADATA', 'c-msg'),
		('s-msg-vec', 'hnsw', 'VECTOR', 'c-msg')`)
	// summaries/1 只有元数据正文，无队列也无 HNSW 目录 -> 触发 FallbackEmbed 路径。
	mustExec(t, cdb, "INSERT INTO embeddings (id, segment_id, embedding_id, seq_id) VALUES (1, 's-sum-meta', '1', 1)")
	mustExec(t, cdb, "INSERT INTO embedding_metadata (id, key, string_value) VALUES (1, 'chroma:document', '需要兜底重嵌入的总结')")

	// messages/9:1 走 Chroma 1.x dict pickle + HNSW。
	mustExec(t, cdb, "INSERT INTO embeddings (id, segment_id, embedding_id, seq_id) VALUES (2, 's-msg-meta', '9:1', 2)")
	mustExec(t, cdb, "INSERT INTO embedding_metadata (id, key, string_value) VALUES (2, 'chroma:document', 'Rust dict pickle 消息')")
	_ = cdb.Close()

	dictPkl := buildChromaDictPickle(map[string]int32{"9:1": 7})
	writeSyntheticHNSWSegment(t, filepath.Join(vecDir, "s-msg-vec"), chromaTestDim, []syntheticHNSWElement{
		{label: 7, vec: rwBasis(3), norm: 1.0},
	}, dictPkl)

	// 伪造残留 chroma.sqlite3-wal 伴生文件：AssumeStopped=false 必须拦截。
	walPath := chromaFile + "-wal"
	if err := os.WriteFile(walPath, []byte("active-wal"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := MigrateChromaToSQLite(ctx, MigrateOptions{
		VectorDBPath: vecDir,
		MarkerPath:   markerPath,
		ExpectedDim:  chromaTestDim,
		Force:        true,
	}); err == nil {
		t.Fatal("存在 -wal 伴生文件且 AssumeStopped=false 时必须报错拦截")
	}

	// AssumeStopped=true 跳过停机检查，但未提供 FallbackEmbed 时因 summaries/1 缺向量报错。
	if _, err := MigrateChromaToSQLite(ctx, MigrateOptions{
		VectorDBPath:  vecDir,
		MarkerPath:    markerPath,
		ExpectedDim:   chromaTestDim,
		Force:         true,
		AssumeStopped: true,
	}); err == nil {
		t.Fatal("缺失向量且 FallbackEmbed=nil 时必须报错")
	}

	// 提供 FallbackEmbed + Force + AssumeStopped：完整迁移成功。
	metaPayload, _ := json.Marshal(map[string]any{"ok": true})
	_ = metaPayload
	rep, err := MigrateChromaToSQLite(ctx, MigrateOptions{
		VectorDBPath:  vecDir,
		MarkerPath:    markerPath,
		ExpectedDim:   chromaTestDim,
		Force:         true,
		AssumeStopped: true,
		FallbackEmbed: deterministicEmbed,
	})
	if err != nil {
		t.Fatalf("带 FallbackEmbed 的强制迁移失败: %v", err)
	}
	if rep.FallbackEmbedded != 1 || rep.SummariesMigrated != 1 || rep.MessagesMigrated != 1 {
		t.Fatalf("FallbackEmbed 迁移报告异常: %+v", rep)
	}
	_ = os.Remove(walPath)
}

func TestMigrateChromaLegacy96ByteHeaderAndSeqIDMapping(t *testing.T) {
	ctx := context.Background()
	vecDir := filepath.Join(t.TempDir(), "vectors")
	markerPath := filepath.Join(t.TempDir(), ".marker96")
	if err := os.MkdirAll(vecDir, 0o755); err != nil {
		t.Fatal(err)
	}

	chromaFile := filepath.Join(vecDir, "chroma.sqlite3")
	cdb, err := sql.Open("sqlite", filepath.ToSlash(chromaFile))
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, cdb, syntheticChromaSchema)
	mustExec(t, cdb, "INSERT INTO collections (id, name, dimension) VALUES ('c-sum', 'summaries', ?), ('c-msg', 'messages', ?)",
		chromaTestDim, chromaTestDim)
	mustExec(t, cdb, `INSERT INTO segments (id, type, scope, collection) VALUES
		('s-sum-meta', 'sqlite', 'METADATA', 'c-sum'),
		('s-sum-vec', 'hnsw', 'VECTOR', 'c-sum')`)

	// seq_id 以 8 字节大端 BLOB 存放（兼容旧版 Chroma 0.4 seq_id BLOB 形态）。
	var seqBlob [8]byte
	binary.BigEndian.PutUint64(seqBlob[:], 42)
	mustExec(t, cdb, "INSERT INTO embeddings (id, segment_id, embedding_id, seq_id) VALUES (1, 's-sum-meta', '88', ?)", seqBlob[:])
	mustExec(t, cdb, "INSERT INTO embedding_metadata (id, key, string_value) VALUES (1, 'chroma:document', '旧版96字节头与seq_id映射')")
	_ = cdb.Close()

	// 构造无 4 字节版本前缀的 96 字节标准 hnswlib header.bin，且不写 index_metadata.pickle（走 seq_id=42 映射）。
	segDir := filepath.Join(vecDir, "s-sum-vec")
	if err := os.MkdirAll(segDir, 0o755); err != nil {
		t.Fatal(err)
	}
	const offsetData = uint64(132)
	labelOffset := offsetData + uint64(chromaTestDim*4)
	sizePerElem := labelOffset + 8
	hdr96 := make([]byte, hnswHeaderLegacyBytes)
	binary.LittleEndian.PutUint64(hdr96[0:8], 0)
	binary.LittleEndian.PutUint64(hdr96[8:16], 10)
	binary.LittleEndian.PutUint64(hdr96[16:24], 1)
	binary.LittleEndian.PutUint64(hdr96[24:32], sizePerElem)
	binary.LittleEndian.PutUint64(hdr96[32:40], labelOffset)
	binary.LittleEndian.PutUint64(hdr96[40:48], offsetData)

	d0 := make([]byte, 10*sizePerElem)
	expectedVec := rwBasis(6)
	copy(d0[offsetData:labelOffset], encodeFloat32LE(expectedVec))
	binary.LittleEndian.PutUint64(d0[labelOffset:labelOffset+8], 42) // label == seq_id (42)

	if err := os.WriteFile(filepath.Join(segDir, "header.bin"), hdr96, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(segDir, "data_level0.bin"), d0, 0o644); err != nil {
		t.Fatal(err)
	}

	rep, err := MigrateChromaToSQLite(ctx, MigrateOptions{
		VectorDBPath: vecDir,
		MarkerPath:   markerPath,
		ExpectedDim:  chromaTestDim,
	})
	if err != nil || !rep.Verified || rep.SummariesMigrated != 1 {
		t.Fatalf("96字节 header + seq_id 映射迁移失败: rep=%+v err=%v", rep, err)
	}

	ro := New(vecDir)
	t.Cleanup(func() { _ = ro.Close() })
	hits, err := ro.Search(ctx, expectedVec, Options{Collection: "summaries", TopK: 1})
	if err != nil || len(hits) != 1 || hits[0].DocID != "88" || math.Abs(hits[0].Similarity-1.0) > 1e-6 {
		t.Fatalf("迁移后检索验证失败: %+v (err=%v)", hits, err)
	}
}
