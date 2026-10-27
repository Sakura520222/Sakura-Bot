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
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"time"
)

// snapshot 是一次整表加载的不可变内存镜像，通过原子指针交换发布。
type snapshot struct {
	vers      versions
	summaries *collectionData
	messages  *collectionData
}

// versionsChanged 报告磁盘 store_meta 版本与快照是否不一致
// （任一 collection 版本变化即触发两表整表重载）。
func (s *snapshot) versionsChanged(v versions) bool {
	return s.vers.sumVer != v.sumVer || s.vers.msgVer != v.msgVer
}

// collectionData 是单个 collection 的只读内存矩阵：向量按行主序扁平存放
// （n × dim，float32 小端解码后归一化），行 i 的向量切片为 vecs[i*dim:(i+1)*dim]。
// channels/dates/hasDate 为 Search 热路径预提取字段，避免扫描期反复解析 JSON。
type collectionData struct {
	name      string
	dim       int
	vecs      []float32
	ids       []string
	documents []string
	metadatas []map[string]any
	channels  []string    // 预提取的 metadata.channel_id 规范化字符串；空串表示缺失
	dates     []time.Time // 预解析的 metadata.created_at（统一转 UTC）
	hasDate   []bool      // created_at 存在且可解析
}

// loadSnapshot 整表加载两 collection，构成新快照（调用方在成功后原子交换）。
func (s *Store) loadSnapshot(ctx context.Context, vers versions) (*snapshot, error) {
	summaries, err := loadCollection(ctx, s.db, "summaries_vectors")
	if err != nil {
		return nil, err
	}
	messages, err := loadCollection(ctx, s.db, "messages_vectors")
	if err != nil {
		return nil, err
	}
	return &snapshot{vers: vers, summaries: summaries, messages: messages}, nil
}

// loadCollection 整表加载一个向量表（README SQLite 契约冻结的表结构：
// id/embedding/document/metadata_json/dim/stored_at，WITHOUT ROWID）。
// 行按 id 排序，保证并列相似度时的稳定顺序。契约违规的行（BLOB 长度与
// dim 不符、行间维度不一致）跳过并告警，不使整表加载失败——与 Python 版
// 「单条坏数据不拖垮检索」的容错方向一致。
func loadCollection(ctx context.Context, db *sql.DB, name string) (*collectionData, error) {
	data := &collectionData{name: name}
	rows, err := db.QueryContext(ctx,
		"SELECT id, embedding, document, metadata_json, dim FROM "+name+" ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	skipped, badMetadata := 0, 0
	for rows.Next() {
		var (
			id, document, metadataJSON string
			embedding                  []byte
			dim                        int64
		)
		if err := rows.Scan(&id, &embedding, &document, &metadataJSON, &dim); err != nil {
			return nil, err
		}
		// 契约：embedding BLOB = 小端 float32 × dim（README 硬规则 10）。
		if dim <= 0 || int64(len(embedding)) != dim*4 {
			skipped++
			continue
		}
		if data.dim == 0 {
			data.dim = int(dim)
		} else if int(dim) != data.dim {
			skipped++
			continue
		}

		vec := make([]float32, data.dim)
		for i := 0; i < data.dim; i++ {
			vec[i] = math.Float32frombits(binary.LittleEndian.Uint32(embedding[i*4:]))
		}
		normalizeInPlace(vec) // 契约：加载即归一化 → 检索期点积即余弦
		data.vecs = append(data.vecs, vec...)
		data.ids = append(data.ids, id)
		data.documents = append(data.documents, document)

		meta, err := decodeMetadata(metadataJSON)
		if err != nil {
			badMetadata++
			meta = map[string]any{}
		}
		data.metadatas = append(data.metadatas, meta)
		data.channels = append(data.channels, canonChannelID(meta["channel_id"]))
		created, ok := parseISODateTime(metadataString(meta, "created_at"))
		data.dates = append(data.dates, created)
		data.hasDate = append(data.hasDate, ok)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if skipped > 0 {
		slog.Warn("向量表存在契约违规行，已跳过", "table", name, "count", skipped)
	}
	if badMetadata > 0 {
		slog.Warn("向量表存在无法解析的 metadata_json，按空元数据处理", "table", name, "count", badMetadata)
	}
	return data, nil
}

// decodeMetadata 解析 metadata_json（契约要求非空 JSON 对象）；
// 解析失败返回错误，由调用方按空元数据兜底（缺 created_at 时日期过滤
// 行为不变：任一边界存在即丢弃）。
func decodeMetadata(raw string) (map[string]any, error) {
	if raw == "" {
		return map[string]any{}, nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return nil, fmt.Errorf("metadata_json 解析失败: %w", err)
	}
	if m == nil {
		m = map[string]any{}
	}
	return m, nil
}

// metadataString 取字符串型元数据字段；缺失或非字符串返回空串
// （对齐 Python metadata.get(key, "") 后 _parse_dt 对非字符串返回 None）。
func metadataString(meta map[string]any, key string) string {
	s, _ := meta[key].(string)
	return s
}

// canonChannelID 把 metadata.channel_id 规范化为与 Options.ChannelID 可比较
// 的字符串。Python 入库的 channel_id 恒为字符串；这里防御性兼容 JSON 数字
// （encoding/json 解码为 float64），整数值按十进制无小数点格式化。
func canonChannelID(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case json.Number:
		return t.String()
	case float64:
		if t == math.Trunc(t) && math.Abs(t) < 1e15 {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	default:
		return fmt.Sprintf("%v", t)
	}
}
