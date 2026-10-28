// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package tools

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/links"
)

// 在 tool message 中截断 summary_text 的最大字符数
// （agent_tools.py:280；Python 码点计数 → Go rune，硬规则 3）。
const summaryTruncateLen = 500

// messageIDSuffix 等价 Python re.search(r":(\d+)$", doc_id)
// （agent_tools.py:591）：从 doc_id 尾段解析真实 Telegram 消息 ID。
var messageIDSuffix = regexp.MustCompile(`:(\d+)$`)

// ---------------------------------------------------------------------------
// Python 语义的标量转换辅助
// ---------------------------------------------------------------------------

// pyErr 带语义化 Python 异常类名的错误，用于把工具异常文本对齐到
// Python 的 "工具执行失败: {type(e).__name__}: {e}" 形态。
type pyErr struct {
	typeName string
	msg      string
}

// Error 实现 error。
func (e *pyErr) Error() string { return e.typeName + ": " + e.msg }

// TypeName 返回对齐 Python 的异常类名（供日志与 error JSON 复用）。
func (e *pyErr) TypeName() string { return e.typeName }

// errTypeName 提取错误的 Python 风格类型名：实现 TypeName() 接口者优先
// （pyErr 及装配层可自定义的类型化错误），否则回退 "Exception"
// （对齐"任意异常"兜底；Go error 缺少统一的 Python 类名概念）。
func errTypeName(err error) string {
	if err == nil {
		return ""
	}
	if namer, ok := err.(interface{ TypeName() string }); ok {
		return namer.TypeName()
	}
	return "Exception"
}

// keyErr 构造 KeyError（Python str(KeyError('q')) 的形态是 "'q'"）。
func keyErr(key string) error { return &pyErr{"KeyError", "'" + key + "'"} }

// pyTypeName 返回 Go 值对应的 Python 类型名（错误文案用；覆盖工具参数
// 实际会出现的 JSON 解码类型）。
func pyTypeName(v any) string {
	switch v.(type) {
	case nil:
		return "NoneType"
	case bool:
		return "bool"
	case string:
		return "str"
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return "int"
	case float32, float64:
		return "float"
	case []any:
		return "list"
	case map[string]any:
		return "dict"
	default:
		return "object"
	}
}

// pyInt 等价 Python int(v)（agent_tools.py 对 result_id / limit / top_k 的
// 转换）：整型直通；float 截断取整（int(1.9)==1）；bool 按 1/0；字符串
// 允许首尾空白与正负号、要求全为十进制整数（int(" 3 ")==3，"3.5" 抛
// ValueError）；nil 与容器抛 TypeError。NaN/Inf 对齐 Python 的
// ValueError/OverflowError。
func pyInt(v any) (int, error) {
	switch t := v.(type) {
	case nil:
		return 0, &pyErr{"TypeError", "int() argument must be a string, a bytes-like object or a real number, not 'NoneType'"}
	case bool:
		if t {
			return 1, nil
		}
		return 0, nil
	case int:
		return t, nil
	case int8:
		return int(t), nil
	case int16:
		return int(t), nil
	case int32:
		return int(t), nil
	case int64:
		return int(t), nil
	case uint:
		return int(t), nil
	case uint8:
		return int(t), nil
	case uint16:
		return int(t), nil
	case uint32:
		return int(t), nil
	case uint64:
		return int(t), nil
	case float32:
		return pyInt(float64(t))
	case float64:
		if math.IsNaN(t) {
			return 0, &pyErr{"ValueError", "cannot convert float NaN to integer"}
		}
		if math.IsInf(t, 0) {
			return 0, &pyErr{"OverflowError", "cannot convert float infinity to integer"}
		}
		return int(math.Trunc(t)), nil
	case string:
		s := strings.TrimSpace(t)
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return 0, &pyErr{"ValueError", fmt.Sprintf("invalid literal for int() with base 10: %q", t)}
		}
		return int(n), nil
	case json.Number:
		n, err := strconv.ParseInt(string(t), 10, 64)
		if err != nil {
			return 0, &pyErr{"ValueError", fmt.Sprintf("invalid literal for int() with base 10: %q", string(t))}
		}
		return int(n), nil
	default:
		return 0, &pyErr{"TypeError", fmt.Sprintf("int() argument must be a string, a bytes-like object or a real number, not '%s'", pyTypeName(v))}
	}
}

// pyFloat 等价 Python float(v)：数值直通、字符串十进制解析，其余 TypeError。
func pyFloat(v any) (float64, error) {
	switch t := v.(type) {
	case nil:
		return 0, &pyErr{"TypeError", "float() argument must be a string or a real number, not 'NoneType'"}
	case bool:
		if t {
			return 1, nil
		}
		return 0, nil
	case int:
		return float64(t), nil
	case int64:
		return float64(t), nil
	case float64:
		return t, nil
	case float32:
		return float64(t), nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
		if err != nil {
			return 0, &pyErr{"ValueError", fmt.Sprintf("could not convert string to float: %q", t)}
		}
		return f, nil
	default:
		return 0, &pyErr{"TypeError", fmt.Sprintf("float() argument must be a string or a real number, not '%s'", pyTypeName(v))}
	}
}

// pythonStr 等价 Python str() 对 JSON 解码标量的形态：字符串直通；整数
// 十进制；float 为整数值时输出无小数点形式（JSON 全数值解码为 float64，
// 而 Python json 会得到 int，str(123)=="123"）；bool 为 True/False；
// nil 返回空串（调用点均有 `or ""` 或真值守卫，等价 Python 语义）。
func pythonStr(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool:
		if t {
			return "True"
		}
		return "False"
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	case float64:
		if t == math.Trunc(t) && !math.IsInf(t, 0) && math.Abs(t) < 1e15 {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'g', -1, 64)
	case float32:
		return pythonStr(float64(t))
	default:
		return fmt.Sprint(v)
	}
}

// truthy 等价 Python 真值判定：nil/false/零值数值/空串/空列表/空字典为假。
func truthy(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case string:
		return t != ""
	case int:
		return t != 0
	case int64:
		return t != 0
	case float64:
		return t != 0
	case float32:
		return t != 0
	case []any:
		return len(t) > 0
	case map[string]any:
		return len(t) > 0
	default:
		return true
	}
}

// pyIterableValues 复刻 Python `for x in obj` 对 JSON 解码值的可迭代展开
// （rerank 的 result_ids，agent_tools.py:440）：list 逐元素、str 逐 Unicode
// 码点、dict 逐键迭代；元素均可哈希，`rid in store` 全 miss。其余类型
// 不可迭代，返回 false（调用方按 "'<type>' object is not iterable" 收敛）。
func pyIterableValues(v any) ([]any, bool) {
	switch t := v.(type) {
	case []any:
		return t, true
	case string:
		out := make([]any, 0, utf8.RuneCountInString(t))
		for _, r := range t {
			out = append(out, string(r))
		}
		return out, true
	case map[string]any:
		out := make([]any, 0, len(t))
		for k := range t {
			out = append(out, k)
		}
		return out, true
	default:
		return nil, false
	}
}

// pyIsoformat 等价 Python datetime.isoformat()（无时区、无参数）：
// 微秒为 0 时 "YYYY-MM-DDTHH:MM:SS"，否则补零到 6 位微秒。
// 与 internal/db.PythonISOFormat 同语义；本包不 import db 以免引入驱动依赖。
func pyIsoformat(t time.Time) string {
	base := t.Format("2006-01-02T15:04:05")
	micros := t.Nanosecond() / 1000
	if micros != 0 {
		return fmt.Sprintf("%s.%06d", base, micros)
	}
	return base
}

// round3 等价工具层的 round(x, 3)（README 硬规则 10 冻结实现）。
// 注：math.Round 为 half-away-from-zero，与 Python round 的 half-even 在精确
// .5 边界存在理论差异（二进制浮点下几乎不可表示），已按硬规则 10 冻结接受。
func round3(x float64) float64 { return math.Round(x*1000) / 1000 }

// normalizeDecimalFields 保证原始 DB 行经 JSON 序列化后的数值合法（R21）：
// DECIMAL 列（如 channel_profiles.avg_message_length DECIMAL(10,2)）经
// go-sql-driver 扫描为 []byte/字符串，直接序列化会得到字符串/中码；此处
// 经 strconv.ParseFloat 转为 JSON 数值。解析失败保留原值（仍是合法 JSON）。
func normalizeDecimalFields(m map[string]any) {
	if m == nil {
		return
	}
	v, ok := m["avg_message_length"]
	if !ok {
		return
	}
	switch t := v.(type) {
	case string:
		if f, err := strconv.ParseFloat(strings.TrimSpace(t), 64); err == nil {
			m["avg_message_length"] = f
		}
	case []byte:
		if f, err := strconv.ParseFloat(strings.TrimSpace(string(t)), 64); err == nil {
			m["avg_message_length"] = f
		}
	}
}

// ---------------------------------------------------------------------------
// 结果存储（agent_tools.py:292-303, 680-692）
// ---------------------------------------------------------------------------

// resultStore 是一次请求内的结果缓存：内部引用一律使用请求内自增的
// result_id，不依赖 summary_id / doc_id 的巧合唯一性。非并发安全：
// 与 Python 一致，由单请求串行执行循环独占使用。
type resultStore struct {
	store    map[int]map[string]any    // result_id → 原始结果
	bySumID  map[any]map[string]any    // 真实总结 ID（保留原始键类型）→ 结果
	byDoc    map[string]map[string]any // str(doc_id) → 结果
	docOrder []string                  // byDoc 插入序（get_all_results 确定性输出）
	next     int                       // 最近分配的 result_id
}

// newResultStore 创建空存储。
func newResultStore() *resultStore {
	return &resultStore{
		store:   make(map[int]map[string]any),
		bySumID: make(map[any]map[string]any),
		byDoc:   make(map[string]map[string]any),
	}
}

// reset 清空全部缓存（对齐 ToolExecutor.reset，agent_tools.py:298-303）。
func (s *resultStore) reset() {
	s.store = make(map[int]map[string]any)
	s.bySumID = make(map[any]map[string]any)
	s.byDoc = make(map[string]map[string]any)
	s.docOrder = s.docOrder[:0]
	s.next = 0
}

// put 缓存一条结果并分配自增 result_id（对齐 _store_result）：
// summary_id 非空时按原始键类型入 bySumID（Python dict 键类型敏感，
// int(5) 与 "5" 是不同的键）；doc_id 真值时按 str(doc_id) 入 byDoc。
func (s *resultStore) put(r map[string]any) {
	s.next++
	r["result_id"] = s.next
	s.store[s.next] = r
	if sid := r["summary_id"]; sid != nil {
		s.bySumID[canonicalSummaryKey(sid)] = r
	}
	if docID := r["doc_id"]; truthy(docID) {
		key := pythonStr(docID)
		if _, exists := s.byDoc[key]; !exists {
			s.docOrder = append(s.docOrder, key)
		}
		s.byDoc[key] = r
	}
}

// canonicalSummaryKey 把 summary_id 归一为 Python dict 键的相等语义：
// Python 中 int 5 与 float 5.0 相等且同哈希（5 == 5.0 → 同一键），
// JSON 解码的整数在 Go 侧是 float64，必须归一到 int64 才能命中
// 适配层写入的 int64 键；bool True == 1 同理；字符串键保持原类型。
func canonicalSummaryKey(v any) any {
	switch t := v.(type) {
	case bool:
		if t {
			return int64(1)
		}
		return int64(0)
	case int:
		return int64(t)
	case int32:
		return int64(t)
	case int64:
		return t
	case uint64:
		return int64(t)
	case json.Number:
		if n, err := strconv.ParseInt(t.String(), 10, 64); err == nil {
			return n
		}
		if f, err := t.Float64(); err == nil && f == math.Trunc(f) {
			return int64(f)
		}
		return t
	case float64:
		if t == math.Trunc(t) && !math.IsInf(t, 0) {
			return int64(t)
		}
		return t
	default:
		return v
	}
}

// dictKeyID 等价 Python 把 tool 参数值作为 dict[int] 键做 `in` 判定
// （rerank 的 result_ids 过滤，agent_tools.py:440）：
//   - 整型 / 整值浮点 / bool（True==1）→ 可哈希命中；
//   - 字符串与非整值浮点的哈希与整型键不同 → 键不存在（跳过，非错误）；
//   - 列表/字典不可哈希 → Python 抛 TypeError。
func dictKeyID(v any) (id int, ok bool, err error) {
	switch t := v.(type) {
	case []any, map[string]any:
		return 0, false, &pyErr{"TypeError", fmt.Sprintf("unhashable type: '%s'", pyTypeName(v))}
	case bool:
		if t {
			return 1, true, nil
		}
		return 0, true, nil
	case int:
		return t, true, nil
	case int64:
		return int(t), true, nil
	case float64:
		if t == math.Trunc(t) && !math.IsInf(t, 0) {
			return int(t), true, nil
		}
		return 0, false, nil
	case json.Number:
		if n, perr := strconv.ParseInt(t.String(), 10, 64); perr == nil {
			return int(n), true, nil
		}
		if f, perr := t.Float64(); perr == nil && f == math.Trunc(f) {
			return int(f), true, nil
		}
		return 0, false, nil
	default:
		// 字符串等其他可哈希类型：与整型键不相等
		return 0, false, nil
	}
}

// get 按 result_id 取回（不存在第二返回值为 false）。
func (s *resultStore) get(id int) (map[string]any, bool) {
	r, ok := s.store[id]
	return r, ok
}

// getBySummaryID 按 summary_id 取回：数值键经 canonicalSummaryKey 归一
// （Python 的 int 5 / float 5.0 / True 是同一 dict 键；字符串键类型敏感）。
func (s *resultStore) getBySummaryID(sid any) (map[string]any, bool) {
	r, ok := s.bySumID[canonicalSummaryKey(sid)]
	return r, ok
}

// getByDocID 按 str(doc_id) 取回。
func (s *resultStore) getByDocID(docID string) (map[string]any, bool) {
	r, ok := s.byDoc[docID]
	return r, ok
}

// getAllResults 获取全部累积结果（完整文本、不截断），按 result_id 升序、
// doc 索引插入序去重追加（对齐 get_all_results，agent_tools.py:703-712；
// 去重键为 (summary_id, doc_id) 二元组）。
func (s *resultStore) getAllResults() []map[string]any {
	var out []map[string]any
	seen := make(map[string]bool)
	ids := make([]int, 0, len(s.store))
	for id := range s.store {
		ids = append(ids, id)
	}
	sortInts(ids)
	for _, id := range ids {
		r := s.store[id]
		key := dedupKey(r)
		if !seen[key] {
			out = append(out, r)
			seen[key] = true
		}
	}
	for _, key := range s.docOrder {
		r, ok := s.byDoc[key]
		if !ok {
			continue
		}
		k := dedupKey(r)
		if !seen[k] {
			out = append(out, r)
			seen[k] = true
		}
	}
	return out
}

// getResultsByIDs 按给定 ID 列表取回结果，跳过不存在的 ID，
// 保持入参顺序（对齐 get_results_by_ids，agent_tools.py:714-716）。
func (s *resultStore) getResultsByIDs(ids []int) []map[string]any {
	out := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		if r, ok := s.store[id]; ok {
			out = append(out, r)
		}
	}
	return out
}

// dedupKey 构造 (summary_id, doc_id) 二元组的字符串化键。
func dedupKey(r map[string]any) string {
	return fmt.Sprintf("%v|%v", r["summary_id"], r["doc_id"])
}

// sortInts 升序排序（避开 sort 包依赖的最小实现）。
func sortInts(a []int) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j] < a[j-1]; j-- {
			a[j], a[j-1] = a[j-1], a[j]
		}
	}
}

// ---------------------------------------------------------------------------
// 来源类型 / 消息 ID / 帖子链接解析（agent_tools.py:560-633，R9 红线）
// ---------------------------------------------------------------------------

// resolveSourceType 判定结果来源类型："message"（频道原始消息）或
// "summary"（总结记录）。显式 source 标记优先；缺省时推断：无真实总结 ID
// 且 doc_id 为 "channel:msg" 格式 → 消息（agent_tools.py:560-571）。
func resolveSourceType(result map[string]any) string {
	if s, ok := result["source"].(string); ok && (s == "message" || s == "summary") {
		return s
	}
	if result["summary_id"] == nil {
		if docID, ok := result["doc_id"].(string); ok && strings.Contains(docID, ":") {
			return "message"
		}
	}
	return "summary"
}

// resolveMessageID 提取结果对应的真实 Telegram 消息 ID（仅消息类结果有效）。
// 优先级：显式 message_id 字段 / metadata.message_id → doc_id 末段
// （"channel_id:message_id" 格式）。内部哈希、数据库主键不会被返回。
// 第二返回值 false 等价 Python 返回 None（agent_tools.py:573-596）。
func resolveMessageID(result map[string]any) (int64, bool) {
	rawMid := result["message_id"]
	if rawMid == nil {
		if meta, ok := result["metadata"].(map[string]any); ok && meta != nil {
			rawMid = meta["message_id"]
		}
	}
	if rawMid != nil {
		mid, err := pyInt(rawMid)
		if err == nil {
			if mid > 0 {
				return int64(mid), true
			}
			return 0, false // mid <= 0 直接判 None，不再尝试 doc_id
		}
		// 转换异常（TypeError/ValueError）→ 对齐 except: pass，继续 doc_id
	}
	docID := pythonStr(result["doc_id"])
	if m := messageIDSuffix.FindStringSubmatch(docID); m != nil {
		mid, err := strconv.ParseInt(m[1], 10, 64)
		if err == nil && mid > 0 {
			return mid, true
		}
	}
	return 0, false
}

// resolvePostURL 为消息类结果构造经过验证的帖子链接（agent_tools.py:598-633）。
//
//   - 仅 source_type=message 的结果才可能有帖子链接；总结报告消息 ID
//     不是频道原帖，绝不能拼接为链接（R9 历史修复红线）
//   - 频道标识优先公开 username（更友好），其次数字 ID（/c/ 私有格式）
//   - 无法验证时返回空串（对齐 Python None），由模型不引用任何链接
func resolvePostURL(result map[string]any) string {
	if resolveSourceType(result) != "message" {
		return ""
	}
	messageID, ok := resolveMessageID(result)
	if !ok {
		return ""
	}

	var meta map[string]any
	if m, ok := result["metadata"].(map[string]any); ok {
		meta = m
	}
	var candidates []any
	if meta != nil {
		candidates = append(candidates, meta["channel_username"], meta["channel_id"])
	}
	candidates = append(candidates, result["channel_id"])
	for _, channel := range candidates {
		if !truthy(channel) {
			continue
		}
		if link := links.BuildTelegramPostLink(pythonStr(channel), messageID); link != "" {
			return link
		}
	}

	// 兜底：从 doc_id 前缀提取频道标识（"channel_id:message_id" 格式）
	docID := pythonStr(result["doc_id"])
	if i := strings.LastIndex(docID, ":"); i >= 0 {
		return links.BuildTelegramPostLink(docID[:i], messageID)
	}
	return ""
}

// ---------------------------------------------------------------------------
// 序列化（agent_tools.py:635-678）
// ---------------------------------------------------------------------------

// serializedResult 是 tool message 中单条结果的序列化形态。字段顺序与
// Python dict 字面量一致（result_id → post_links，条件性 similarity /
// rerank_score 收尾）；nil 序列化为 null。
//
// 字段语义（重要，勿混淆三种 ID）：
//   - result_id: 请求内自增的内部引用 ID，供 rerank/get_source_detail 使用
//   - summary_id: 仅总结类结果携带的 MySQL 总结记录 ID；消息类结果为 null
//   - message_id: 仅消息类结果携带的真实 Telegram 频道消息 ID
//   - post_url:   后端验证过的帖子链接；总结类结果为 null
type serializedResult struct {
	ResultID    any      `json:"result_id"`
	SourceType  string   `json:"source_type"`
	SummaryID   any      `json:"summary_id"`
	SummaryText string   `json:"summary_text"`
	ChannelID   any      `json:"channel_id"`
	ChannelName any      `json:"channel_name"`
	CreatedAt   any      `json:"created_at"`
	MessageID   any      `json:"message_id"`
	DocID       any      `json:"doc_id"`
	PostURL     any      `json:"post_url"`
	PostLinks   []string `json:"post_links"`
	Similarity  *float64 `json:"similarity,omitempty"`
	RerankScore *float64 `json:"rerank_score,omitempty"`
}

// truncateSummary 按 rune 截断正文：超过 summaryTruncateLen（500）个码点
// 时截断并追加 "..."（对齐 agent_tools.py:646-648 的码点切片语义）。
func truncateSummary(text string) string {
	if utf8.RuneCountInString(text) <= summaryTruncateLen {
		return text
	}
	n := 0
	for i := range text {
		if n == summaryTruncateLen {
			return text[:i] + "..."
		}
		n++
	}
	return text // 不可达：长度已在上面保证
}

// serializeResult 序列化单条结果用于 tool message（截断长文本）。
// 调用前结果必须已经 resultStore.put（result_id 已分配）。
func serializeResult(result map[string]any) serializedResult {
	text, _ := result["summary_text"].(string)
	truncated := truncateSummary(text)

	var meta map[string]any
	if m, ok := result["metadata"].(map[string]any); ok {
		meta = m
	}
	var createdAt any
	if meta != nil {
		createdAt = meta["created_at"]
	}
	if t, ok := createdAt.(time.Time); ok {
		createdAt = pyIsoformat(t)
	}

	sourceType := resolveSourceType(result)
	postURL := resolvePostURL(result)

	out := serializedResult{
		ResultID:    result["result_id"],
		SourceType:  sourceType,
		SummaryText: truncated,
		CreatedAt:   createdAt,
		DocID:       result["doc_id"],
		PostLinks:   []string{},
	}
	if sourceType == "summary" {
		out.SummaryID = result["summary_id"]
	} else {
		if mid, ok := resolveMessageID(result); ok {
			out.MessageID = mid
		}
	}
	// metadata.get("channel_id") or result.get("channel_id")：真值优先
	if v := metaVal(meta, "channel_id"); truthy(v) {
		out.ChannelID = v
	} else {
		out.ChannelID = result["channel_id"]
	}
	if v := metaVal(meta, "channel_name"); truthy(v) {
		out.ChannelName = v
	} else {
		out.ChannelName = result["channel_name"]
	}
	if postURL != "" {
		out.PostURL = postURL
		out.PostLinks = []string{postURL}
	}
	// 条件字段：键存在即输出（值非法经 pyFloat 转 error 的路径由执行器
	// 统一捕获；此处数值一定来自检索组件）
	if v, ok := result["similarity"]; ok {
		if f, err := pyFloat(v); err == nil {
			r := round3(f)
			out.Similarity = &r
		}
	}
	if v, ok := result["rerank_score"]; ok {
		if f, err := pyFloat(v); err == nil {
			r := round3(f)
			out.RerankScore = &r
		}
	}
	return out
}

// metaVal 安全读取 metadata 键（meta 为 nil 时返回 nil）。
func metaVal(meta map[string]any, key string) any {
	if meta == nil {
		return nil
	}
	return meta[key]
}
