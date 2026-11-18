// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package wiki

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hash/crc32"
	"strings"
	"time"
)

// DefaultCursorTTL 是公开列表延续游标的有效期（2 小时，对齐 core/wiki/cursor.py）。
const DefaultCursorTTL = 2 * time.Hour

// ErrInvalidCursor 表示延续游标已过期、被篡改或与当前查询作用域不匹配。
var ErrInvalidCursor = errors.New("游标已失效，请刷新列表。")

// CursorPosition 表示标准三元组游标锚点 (Order, Date, ID)。
type CursorPosition struct {
	Order int
	Date  time.Time
	ID    string
}

// CursorData 表示解码并校验通过后的延续游标状态。
type CursorData struct {
	Scope    string
	Start    time.Time
	Seed     string
	Key      []any
	Position *CursorPosition
}

// ScopeKey 计算查询过滤条件绑定的 20 字符作用域摘要（对齐 cursor.py:scope_key）。
func ScopeKey(values ...any) string {
	return Digest(values...)[:20]
}

// NewFeedSeed 生成 32 位小写十六进制随机推荐流种子。
func NewFeedSeed() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// EncodeCursor 将 [scope, start_unix, seed, offsetKey] 编码为无填充 URL-safe Base64 JSON 令牌。
func EncodeCursor(scope string, offsetKey []any, start time.Time, seed string) (string, error) {
	if scope == "" || len(offsetKey) == 0 {
		return "", ErrInvalidCursor
	}
	if start.IsZero() {
		start = NowUTC()
	}
	payload := []any{scope, start.UTC().Unix(), seed, offsetKey}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(payload); err != nil {
		return "", ErrInvalidCursor
	}
	compact := bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
	return base64.RawURLEncoding.EncodeToString(compact), nil
}

// DecodeCursor 解码并严格校验 URL-safe Base64 游标的作用域、签名完整性、时间窗口与键结构。
func DecodeCursor(token, expectedScope string, ttl time.Duration) (*CursorData, error) {
	return decodeCursorAt(token, expectedScope, ttl, NowUTC())
}

func decodeCursorAt(token, expectedScope string, ttl time.Duration, now time.Time) (*CursorData, error) {
	if ttl <= 0 {
		ttl = DefaultCursorTTL
	}
	trimmed := strings.TrimRight(strings.TrimSpace(token), "=")
	if trimmed == "" || len(trimmed) > 1024 {
		return nil, ErrInvalidCursor
	}
	raw, err := base64.RawURLEncoding.DecodeString(trimmed)
	if err != nil {
		return nil, ErrInvalidCursor
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var arr []any
	if err := dec.Decode(&arr); err != nil || len(arr) != 4 {
		return nil, ErrInvalidCursor
	}
	var extra any
	if dec.Decode(&extra) == nil {
		return nil, ErrInvalidCursor
	}

	scope, ok := arr[0].(string)
	if !ok || scope == "" || scope != expectedScope {
		return nil, ErrInvalidCursor
	}

	startTime, ok := parseCursorStart(arr[1])
	if !ok {
		return nil, ErrInvalidCursor
	}
	if startTime.Before(now.Add(-ttl)) || startTime.After(now.Add(5*time.Second)) {
		return nil, ErrInvalidCursor
	}

	seed, ok := arr[2].(string)
	if !ok {
		return nil, ErrInvalidCursor
	}
	if seed != "" && !reHex32.MatchString(seed) {
		return nil, ErrInvalidCursor
	}

	keySlice, ok := arr[3].([]any)
	if !ok || len(keySlice) == 0 {
		return nil, ErrInvalidCursor
	}

	data := &CursorData{
		Scope: scope,
		Start: startTime,
		Seed:  seed,
		Key:   keySlice,
	}

	// 若 offsetKey 为标准 [order, date, id] 三元组，则解析并强校验 CursorPosition
	if len(keySlice) == 3 {
		pos, err := parseCursorPosition(keySlice)
		if err != nil {
			return nil, ErrInvalidCursor
		}
		data.Position = pos
	}

	return data, nil
}

func parseCursorStart(v any) (time.Time, bool) {
	switch val := v.(type) {
	case json.Number:
		if sec, err := val.Int64(); err == nil && sec > 0 {
			return time.Unix(sec, 0).UTC(), true
		}
		if f, err := val.Float64(); err == nil && f > 0 {
			sec := int64(f)
			nsec := int64((f - float64(sec)) * 1e9)
			return time.Unix(sec, nsec).UTC().Truncate(time.Microsecond), true
		}
	case string:
		if t, err := ParseUTCTime(val); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func parseCursorPosition(items []any) (*CursorPosition, error) {
	num, ok := items[0].(json.Number)
	if !ok {
		return nil, ErrInvalidCursor
	}
	order64, err := num.Int64()
	if err != nil || order64 < 0 || order64 > 100000 {
		return nil, ErrInvalidCursor
	}
	dateStr, ok := items[1].(string)
	if !ok || dateStr == "" {
		return nil, ErrInvalidCursor
	}
	dt, err := ParseUTCTime(dateStr)
	if err != nil {
		return nil, ErrInvalidCursor
	}
	idStr, ok := items[2].(string)
	if !ok || !reHex32.MatchString(idStr) {
		return nil, ErrInvalidCursor
	}
	return &CursorPosition{
		Order: int(order64),
		Date:  dt,
		ID:    idStr,
	}, nil
}

// ReadOrInitCursor 辅助方法：无 token 时初始化新会话时间戳与推荐种子，有 token 时严格解码校验。
func ReadOrInitCursor(token, scope string, shuffle bool) (time.Time, string, *CursorPosition, error) {
	if strings.TrimSpace(token) == "" {
		seed := ""
		if shuffle {
			seed = NewFeedSeed()
		}
		return NowUTC(), seed, nil, nil
	}
	decoded, err := DecodeCursor(token, scope, DefaultCursorTTL)
	if err != nil {
		return time.Time{}, "", nil, err
	}
	if shuffle && len(decoded.Seed) != 32 {
		return time.Time{}, "", nil, ErrInvalidCursor
	}
	if !shuffle && decoded.Seed != "" {
		return time.Time{}, "", nil, ErrInvalidCursor
	}
	if decoded.Position == nil {
		return time.Time{}, "", nil, ErrInvalidCursor
	}
	return decoded.Start, decoded.Seed, decoded.Position, nil
}

// FeedOrder 使用 IEEE CRC32 计算确定性伪随机排序哈希（对齐 zlib.crc32 / MySQL CRC32）。
func FeedOrder(seed, channelID, pageID string) uint32 {
	return crc32.ChecksumIEEE([]byte(seed + ":" + channelID + ":" + pageID))
}

// FeedRecommendedRank 复刻推荐流两级频道主题轮转加权得分（对齐 core/wiki/reader.py:300-303）。
func FeedRecommendedRank(seed, channelID, topic, pageID string, isRecent bool) int {
	bonus := 0
	if isRecent {
		bonus = 200
	}
	groupCRC := crc32.ChecksumIEEE([]byte(seed + ":" + channelID + ":" + topic))
	pageCRC := crc32.ChecksumIEEE([]byte(seed + ":" + pageID))
	mixed := (uint64(groupCRC) + uint64(pageCRC%97)) % 127
	return bonus + int(mixed)
}
