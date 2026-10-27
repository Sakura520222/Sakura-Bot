// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

// Package vectors 提供 QA 进程的向量只读检索：以 modernc.org/sqlite 只读打开
// {VECTOR_DB_PATH}/lite_index.sqlite3（SQLite 契约与 qa-bot-go/README.md 冻结），
// 全表加载为归一化 float32 内存矩阵后暴力余弦检索。
//
// 行为对齐 core/ai/vector_store.py 的 search/search_all 语义：相似度 = 1 - 距离
// 即归一化点积本身；日期过滤按 metadata.created_at（fromisoformat 语义，naive
// 视为 UTC，全量过滤后再截断 top_k）。文件/表缺失返回 ErrNotReady：不 panic、
// 不锁存，下次 Search 自动重试。
package vectors

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"math"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite" // 注册 "sqlite" 驱动（纯 Go，支持 file: URI 的 mode=ro）
)

// 契约常量（qa-bot-go/README.md「SQLite 向量存储契约」，冻结）：
// 文件名、schema_version 期望值与默认 top_k。
const (
	dbFileName      = "lite_index.sqlite3"
	defaultTopK     = 20
	requiredVersion = 1
)

var (
	// ErrNotReady 表示向量库尚未就绪：文件缺失、无法打开、schema 不符或表缺失。
	// 该状态不锁存：调用方下次 Search 会自动重试。
	ErrNotReady = errors.New("向量库未就绪")

	// ErrDimensionMismatch 表示查询向量维度与库内矩阵维度不一致
	//（对齐 Python 版 chromadb 的维度校验错误路径，工具层据此返回 error JSON）。
	ErrDimensionMismatch = errors.New("查询向量维度不匹配")
)

// Options 是一次向量检索的参数，语义对齐 core/ai/vector_store.py 的
// search / search_all。
type Options struct {
	// Collection 取 "summaries"、"messages" 或 "all"（空值视为 "all"）。
	Collection string

	// TopK 为返回结果上限；<=0 时取默认值 20（对齐 Python 签名默认 top_k=20）。
	TopK int

	// ChannelID 非空时按 metadata.channel_id 精确匹配过滤。
	ChannelID string

	// DateAfter / DateBefore 为 ISO 格式时间边界（含边界，对齐 Python 的
	// created_dt < after 丢弃 / created_dt > before 丢弃）；解析失败时忽略该边界。
	DateAfter  string
	DateBefore string
}

// Hit 是一条检索结果。
type Hit struct {
	DocID      string         // 原始文档 ID：summaries 为数字字符串，messages 为 "channel_id:message_id"
	Document   string         // 文档正文
	Metadata   map[string]any // 元数据浅拷贝（调用方可安全修改，不影响内部快照）
	Similarity float64        // 相似度 = 1 - distance，即归一化点积本身
}

// Store 是只读向量存储：首次 Search 惰性加载两表为归一化矩阵；每次 Search 前
// 查询 store_meta 两版本号（README 硬规则 2：WAL 下主文件 mtime 不可靠，禁用
// stat 方案），任一变化则整表重载并原子指针交换。
type Store struct {
	dsn    string // file:...?mode=ro&_busy_timeout=5000（冻结）
	dbFile string // {VECTOR_DB_PATH}/lite_index.sqlite3

	mu      sync.Mutex                // 串行化「版本检查 + 整表重载」
	db      *sql.DB                   // 只读连接池；失败即重置为 nil，保证下次重试
	current atomic.Pointer[*snapshot] // 当前内存矩阵快照，原子交换
}

// New 创建向量存储。vectorDBPath 对应环境变量 VECTOR_DB_PATH（Python 默认
// data/vectors），由调用方（main 装配）传入，本包不读取任何环境变量。
// 创建时不触碰磁盘，首次 Search 才惰性加载。
func New(vectorDBPath string) *Store {
	file := filepath.Join(vectorDBPath, dbFileName)
	return &Store{dbFile: file, dsn: buildDSN(file)}
}

// buildDSN 构造契约冻结的只读 DSN：file:...?mode=ro&_busy_timeout=5000。
// mode=ro 使缺失文件直接 SQLITE_CANTOPEN、写操作全部失败；禁 immutable=1
// （WAL 库不可作快照）。URI 路径中 %、?、# 需百分号转义，Windows 反斜杠
// 归一为 '/'（先转义 %，避免二次解释）。
func buildDSN(file string) string {
	p := strings.ReplaceAll(file, "\\", "/")
	p = strings.ReplaceAll(p, "%", "%25")
	p = strings.ReplaceAll(p, "?", "%3F")
	p = strings.ReplaceAll(p, "#", "%23")
	return "file:" + p + "?mode=ro&_busy_timeout=5000"
}

// Close 释放只读连接池（幂等）。已加载的内存快照保留，进程退出前由 main 调用。
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.resetDBLocked()
}

// resetDBLocked 关闭并丢弃连接池：任何打开/查询失败后都经由此处重置，
// 下一次 Search 重新打开（失败不锁存）。
func (s *Store) resetDBLocked() error {
	if s.db == nil {
		return nil
	}
	db := s.db
	s.db = nil
	return db.Close()
}

// Search 以归一化点积（余弦）检索，返回按相似度降序的最多 top_k 条结果。
// 对齐 Python 版语义：
//   - Collection="all"：两表各取 top_k，合并后按相似度降序稳定排序（并列时
//     summaries 在前，对齐 Python 先 extend summaries 的插入序）截取 top_k；
//   - 日期过滤按 metadata.created_at：边界不可解析则忽略该边界；任一边界存在
//     时，缺失/不可解析 created_at 的文档被丢弃（对齐 vector_store.py:526-547）；
//   - ChannelID 非空时按 metadata.channel_id 精确匹配；
//   - 文件/表缺失返回包装 ErrNotReady 的错误，不 panic、不锁存。
func (s *Store) Search(ctx context.Context, queryVec []float32, opts Options) ([]Hit, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	collection := opts.Collection
	if collection == "" {
		collection = "all"
	}
	switch collection {
	case "summaries", "messages", "all":
	default:
		return nil, fmt.Errorf("未知的 collection: %q", collection)
	}
	if err := s.ensureFresh(ctx); err != nil {
		return nil, err
	}
	snap := s.current.Load()
	if snap == nil || *snap == nil {
		return nil, ErrNotReady
	}

	topK := opts.TopK
	if topK <= 0 {
		topK = defaultTopK
	}
	after := parseISODateTimePtr(opts.DateAfter)   // 不可解析边界忽略 → nil
	before := parseISODateTimePtr(opts.DateBefore) // 同上
	q := normalizeCopy(queryVec)                   // 归一化查询向量，点积即余弦

	switch collection {
	case "summaries":
		return searchCollection((*snap).summaries, q, topK, opts.ChannelID, after, before)
	case "messages":
		return searchCollection((*snap).messages, q, topK, opts.ChannelID, after, before)
	default: // "all"
		// 先 summaries 后 messages 追加（稳定排序保持该插入序，对齐 search_all）。
		sumHits, err := searchCollection((*snap).summaries, q, topK, opts.ChannelID, after, before)
		if err != nil {
			return nil, err
		}
		msgHits, err := searchCollection((*snap).messages, q, topK, opts.ChannelID, after, before)
		if err != nil {
			return nil, err
		}
		hits := make([]Hit, 0, len(sumHits)+len(msgHits))
		hits = append(hits, sumHits...)
		hits = append(hits, msgHits...)
		sort.SliceStable(hits, func(i, j int) bool { return hits[i].Similarity > hits[j].Similarity })
		if len(hits) > topK {
			hits = hits[:topK]
		}
		return hits, nil
	}
}

// ensureFresh 保证 s.current 指向与磁盘 store_meta 版本一致的快照：
// 版本未变直接复用；任一版本变化（或尚未加载）则两表整表重载并原子交换，
// 重载成功后调用 debug.FreeOSMemory 归还内存（重载瞬态新旧矩阵并存约
// 2×15MB，见 README 硬规则 5/GOMEMLIMIT 与设计 §17-9）。任何打开/查询失败
// 都重置连接池（不锁存）；已有快照时短暂降级为陈旧数据并告警，无快照时
// 返回包装 ErrNotReady 的错误。
func (s *Store) ensureFresh(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 文件已被外部删除/重建（如迁移窗口重建库）→ 立即按未就绪处理：丢弃
	// 旧句柄与旧快照，避免对已被 unlink 的旧 inode 永久提供陈旧结果。
	if fi, err := os.Stat(s.dbFile); err != nil || fi.IsDir() {
		_ = s.resetDBLocked()
		empty := (*snapshot)(nil)
		s.current.Store(&empty)
		return fmt.Errorf("%w: 向量库文件缺失: %s", ErrNotReady, s.dbFile)
	}

	vers, err := s.readVersions(ctx)
	if err != nil {
		_ = s.resetDBLocked()
		if cur := s.current.Load(); cur != nil && *cur != nil {
			slog.Warn("查询向量库版本失败，沿用已加载矩阵", "error", err)
			return nil
		}
		return fmt.Errorf("%w: %w", ErrNotReady, err)
	}
	if cur := s.current.Load(); cur != nil && *cur != nil && !(*cur).versionsChanged(vers) {
		return nil
	}
	snap, err := s.loadSnapshot(ctx, vers)
	if err != nil {
		_ = s.resetDBLocked()
		if cur := s.current.Load(); cur != nil && *cur != nil {
			slog.Warn("重载向量矩阵失败，沿用已加载矩阵", "error", err)
			return nil
		}
		return fmt.Errorf("%w: %w", ErrNotReady, err)
	}
	s.current.Store(&snap)
	debug.FreeOSMemory()
	return nil
}

// ensureDB 打开（如未打开）只读连接池。sql.Open 是惰性的，open 错误在首次
// 查询时暴露并由调用方重置句柄重试。
func (s *Store) ensureDB() {
	if s.db == nil {
		db, err := sql.Open("sqlite", s.dsn)
		if err != nil {
			return // 仅未知驱动会在此失败；保持 nil 由调用方按未就绪处理
		}
		db.SetMaxOpenConns(2) // 只读检索串行使用，小池即可
		s.db = db
	}
}

// versionVal 是 store_meta 中一个版本号键的读取结果；set=false 表示键缺失。
type versionVal struct {
	set bool
	val int64
}

// versions 汇总两 collection 的版本号（README 契约键名冻结）。
type versions struct {
	sumVer versionVal // 'summaries_version'
	msgVer versionVal // 'messages_version'
}

// readVersions 校验契约元数据并读取两版本号：store_meta 缺失（查询报错）、
// schema_version 不为 1、任一向量表缺失均返回错误（由调用方映射为
// ErrNotReady）。每次 Search 前仅这两条微秒级 SELECT（README 硬规则 2）。
func (s *Store) readVersions(ctx context.Context) (versions, error) {
	var vers versions
	s.ensureDB()
	if s.db == nil {
		return vers, errors.New("无法打开只读连接")
	}

	meta := map[string]int64{}
	rows, err := s.db.QueryContext(ctx,
		"SELECT key, value FROM store_meta WHERE key IN ('schema_version','summaries_version','messages_version')")
	if err != nil {
		return vers, err // store_meta 表缺失在此暴露
	}
	for rows.Next() {
		var k string
		var v int64
		if err := rows.Scan(&k, &v); err != nil {
			rows.Close()
			return vers, err
		}
		meta[k] = v
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return vers, err
	}
	rows.Close()

	sv, ok := meta["schema_version"]
	if !ok || sv != requiredVersion {
		return vers, fmt.Errorf("store_meta schema_version 不符: 期望 %d", requiredVersion)
	}
	vers.sumVer = versionVal{set: metaKeyExists(meta, "summaries_version"), val: meta["summaries_version"]}
	vers.msgVer = versionVal{set: metaKeyExists(meta, "messages_version"), val: meta["messages_version"]}

	// 表存在性核查：契约两表同时创建，任一缺失即整体未就绪。
	missing, err := s.missingTables(ctx, "summaries_vectors", "messages_vectors")
	if err != nil {
		return vers, err
	}
	if len(missing) > 0 {
		return vers, fmt.Errorf("向量库缺少表: %s", strings.Join(missing, ", "))
	}
	return vers, nil
}

// missingTables 返回给定表中不存在于 sqlite_master 的表名（保持传入顺序）。
func (s *Store) missingTables(ctx context.Context, names ...string) ([]string, error) {
	var missing []string
	for _, name := range names {
		var one string
		err := s.db.QueryRowContext(ctx,
			"SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?", name).Scan(&one)
		if errors.Is(err, sql.ErrNoRows) {
			missing = append(missing, name)
			continue
		}
		if err != nil {
			return nil, err
		}
	}
	return missing, nil
}

// metaKeyExists 报告键是否存在于 store_meta 读取结果中。
func metaKeyExists(meta map[string]int64, key string) bool {
	_, ok := meta[key]
	return ok
}

// searchCollection 在单个 collection 的归一化矩阵上做暴力余弦检索：
// 通道/日期过滤在全量扫描时进行（评审结论：全量过滤后再截断，优于 chroma
// 时代的 fetch_k=top_k*3 近似），随后按相似度降序稳定排序截取 top_k
// （并列时保持行序；行序即 id 序，ORDER BY id 保证确定性）。
func searchCollection(coll *collectionData, q []float32, topK int, channelID string, after, before *time.Time) ([]Hit, error) {
	if coll == nil || len(coll.ids) == 0 {
		return nil, nil // 空表：对齐 Python collection.count()==0 → 返回 []
	}
	if len(q) != coll.dim {
		return nil, ErrDimensionMismatch
	}

	type scored struct {
		idx int
		sim float64
	}
	var matches []scored
	for i := range coll.ids {
		if channelID != "" && coll.channels[i] != channelID {
			continue
		}
		// 对齐 vector_store.py:542-545：任一边界存在时，created_at 缺失或
		// 不可解析的文档一律丢弃；边界比较含边界（< after 丢弃 / > before 丢弃）。
		if after != nil && (!coll.hasDate[i] || coll.dates[i].Before(*after)) {
			continue
		}
		if before != nil && (!coll.hasDate[i] || coll.dates[i].After(*before)) {
			continue
		}
		matches = append(matches, scored{i, dot(coll.vecs[i*coll.dim:(i+1)*coll.dim], q)})
	}
	sort.SliceStable(matches, func(a, b int) bool { return matches[a].sim > matches[b].sim })
	if len(matches) > topK {
		matches = matches[:topK]
	}

	hits := make([]Hit, 0, len(matches))
	for _, m := range matches {
		meta := make(map[string]any, len(coll.metadatas[m.idx]))
		maps.Copy(meta, coll.metadatas[m.idx]) // 浅拷贝，隔离调用方修改与内部快照
		hits = append(hits, Hit{
			DocID:      coll.ids[m.idx],
			Document:   coll.documents[m.idx],
			Metadata:   meta,
			Similarity: m.sim,
		})
	}
	return hits, nil
}

// dot 计算点积（float64 累加）；入参均为归一化向量时即余弦相似度
// （= 1 - cosine distance，README 硬规则 10）。与 Python float32 实现的
// 误差 <1e-6，消费方 round(x,3) 无感（评审结论记为可接受）。
func dot(a, b []float32) float64 {
	var sum float64
	for i := range a {
		sum += float64(a[i]) * float64(b[i])
	}
	return sum
}

// normalizeCopy 返回查询向量的 L2 归一化副本（不修改调用方切片）；
// 零向量保持原样（点积为 0，不产生 NaN）。
func normalizeCopy(v []float32) []float32 {
	out := make([]float32, len(v))
	copy(out, v)
	normalizeInPlace(out)
	return out
}

// normalizeInPlace 原地 L2 归一化；零向量保持原样。
func normalizeInPlace(v []float32) {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	if sum <= 0 {
		return
	}
	inv := float32(1 / math.Sqrt(sum))
	for i := range v {
		v[i] *= inv
	}
}
