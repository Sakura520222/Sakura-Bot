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
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// 测试替身：窄接口的最小实现
// ---------------------------------------------------------------------------

type fakeVectorStore struct {
	available  bool
	msgAvail   bool
	results    []map[string]any
	err        error
	lastCall   string // 记录最近一次调用的方法名（路由断言用）
	lastQuery  string
	lastTopK   int
	lastFilter map[string]any
}

func (f *fakeVectorStore) IsAvailable() bool         { return f.available }
func (f *fakeVectorStore) IsMessagesAvailable() bool { return f.msgAvail }
func (f *fakeVectorStore) SearchSimilar(_ context.Context, query string, topK int, filter map[string]any, _, _ string) ([]map[string]any, error) {
	f.lastCall, f.lastQuery, f.lastTopK, f.lastFilter = "similar", query, topK, filter
	return f.results, f.err
}
func (f *fakeVectorStore) SearchMessages(_ context.Context, query string, topK int, filter map[string]any, _, _ string) ([]map[string]any, error) {
	f.lastCall, f.lastQuery, f.lastTopK, f.lastFilter = "messages", query, topK, filter
	return f.results, f.err
}
func (f *fakeVectorStore) SearchAll(_ context.Context, query string, topK int, filter map[string]any, _, _ string) ([]map[string]any, error) {
	f.lastCall, f.lastQuery, f.lastTopK, f.lastFilter = "all", query, topK, filter
	return f.results, f.err
}

type fakeMemory struct {
	searchRows  []map[string]any
	searchArgs  [5]any // keywords, topics, timeRangeDays, channelID, limit
	channels    []map[string]any
	resolve     map[string]any
	recentRows  []map[string]any
	recentLimit int
	recentRange int
	stats       map[string]any
	context     string
	err         error
}

func (f *fakeMemory) SearchSummaries(_ context.Context, keywords, topics []string, timeRangeDays int, channelID string, limit int) ([]map[string]any, error) {
	f.searchArgs = [5]any{keywords, topics, timeRangeDays, channelID, limit}
	return f.searchRows, f.err
}
func (f *fakeMemory) ListChannels(_ context.Context) ([]map[string]any, error) {
	return f.channels, f.err
}
func (f *fakeMemory) ResolveChannel(_ context.Context, _ string) (map[string]any, error) {
	return f.resolve, f.err
}
func (f *fakeMemory) GetRecentSummaries(_ context.Context, _ string, limit, timeRangeDays int) ([]map[string]any, error) {
	f.recentLimit, f.recentRange = limit, timeRangeDays
	return f.recentRows, f.err
}
func (f *fakeMemory) GetChannelStats(_ context.Context, _ string) (map[string]any, error) {
	return f.stats, f.err
}
func (f *fakeMemory) GetChannelContext(_ context.Context, _ string) (string, error) {
	return f.context, f.err
}

type fakeReranker struct {
	available  bool
	candidates []map[string]any
	out        []map[string]any
}

func (f *fakeReranker) IsAvailable() bool { return f.available }
func (f *fakeReranker) Rerank(_ context.Context, _ string, candidates []map[string]any, _ int) []map[string]any {
	f.candidates = candidates
	return f.out
}

// newExecutor 构造被测执行器（默认全部可用）。
func newExecutor(vs *fakeVectorStore, mm *fakeMemory, rr *fakeReranker) *ToolExecutor {
	if vs == nil {
		vs = &fakeVectorStore{available: true}
	}
	if mm == nil {
		mm = &fakeMemory{}
	}
	if rr == nil {
		rr = &fakeReranker{available: true}
	}
	return NewToolExecutor(vs, mm, rr)
}

// decodeJSON 解析执行结果为 map（并断言合法 JSON）。
func decodeJSON(t *testing.T, raw string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("结果非合法 JSON: %v: %s", err, raw)
	}
	return m
}

// mustResults 断言载荷形如 {"results": [...], "count": n} 并返回结果数组。
func mustResults(t *testing.T, raw string) []map[string]any {
	t.Helper()
	m := decodeJSON(t, raw)
	if _, hasErr := m["error"]; hasErr {
		t.Fatalf("不应为错误载荷: %s", raw)
	}
	count, _ := m["count"].(float64)
	results, _ := m["results"].([]any)
	if int(count) != len(results) {
		t.Fatalf("count(%v) 与 results 长度(%d) 不一致: %s", count, len(results), raw)
	}
	out := make([]map[string]any, 0, len(results))
	for _, r := range results {
		out = append(out, r.(map[string]any))
	}
	return out
}

// ---------------------------------------------------------------------------
// semantic_search
// ---------------------------------------------------------------------------

// TestExecuteSemanticSearch 路由与序列化（表驱动）。
func TestExecuteSemanticSearch(t *testing.T) {
	messageHit := map[string]any{
		"doc_id": "-100123:9", "summary_text": "消息正文",
		"metadata":   map[string]any{"channel_id": "-100123", "created_at": "2026-04-19T10:00:00"},
		"similarity": 0.8765432,
	}
	// 装配层（HitToResult）产出的总结行已带 summary_id（doc_id 数字解析）
	summaryHit := map[string]any{
		"summary_id": int64(77), "doc_id": "77", "summary_text": "总结正文", "similarity": 0.5,
		"metadata": map[string]any{"channel_id": "https://t.me/pub"},
	}

	cases := []struct {
		name     string
		vs       *fakeVectorStore
		args     string
		wantCall string
		wantErr  string // 非空时断言 error 前缀
	}{
		{
			name:     "默认all_消息库可用走search_all",
			vs:       &fakeVectorStore{available: true, msgAvail: true},
			args:     `{"query":"测试"}`,
			wantCall: "all",
		},
		{
			name:     "默认all_消息库不可用回退similar",
			vs:       &fakeVectorStore{available: true, msgAvail: false},
			args:     `{"query":"测试"}`,
			wantCall: "similar",
		},
		{
			name:     "collection=messages_可用",
			vs:       &fakeVectorStore{available: true, msgAvail: true},
			args:     `{"query":"测试","collection":"messages"}`,
			wantCall: "messages",
		},
		{
			name:     "collection=messages_不可用_链式落到similar",
			vs:       &fakeVectorStore{available: true, msgAvail: false},
			args:     `{"query":"测试","collection":"messages"}`,
			wantCall: "similar",
		},
		{
			name:     "collection=summaries_恒similar",
			vs:       &fakeVectorStore{available: true, msgAvail: true},
			args:     `{"query":"测试","collection":"summaries"}`,
			wantCall: "similar",
		},
		{
			name:     "向量库不可用",
			vs:       &fakeVectorStore{available: false},
			args:     `{"query":"测试"}`,
			wantCall: "",
			wantErr:  "向量存储不可用",
		},
		{
			name:     "缺query_按KeyError转错误",
			vs:       &fakeVectorStore{available: true, msgAvail: true},
			args:     `{}`,
			wantCall: "",
			wantErr:  "工具执行失败: KeyError: 'query'",
		},
		{
			name:     "组件异常转错误不上抛",
			vs:       &fakeVectorStore{available: true, err: context.DeadlineExceeded},
			args:     `{"query":"测试"}`,
			wantCall: "similar",
			wantErr:  "工具执行失败: ",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			c.vs.results = []map[string]any{messageHit, summaryHit}
			e := newExecutor(c.vs, nil, nil)
			raw := e.ExecuteJSON(context.Background(), "semantic_search", c.args)
			m := decodeJSON(t, raw)
			if c.wantErr != "" {
				if !strings.HasPrefix(m["error"].(string), c.wantErr) {
					t.Fatalf("error = %v, 期望前缀 %q", m["error"], c.wantErr)
				}
				if c.vs.lastCall != c.wantCall {
					t.Errorf("lastCall = %q, 期望 %q", c.vs.lastCall, c.wantCall)
				}
				return
			}
			results := mustResults(t, raw)
			if len(results) != 2 {
				t.Fatalf("结果数 = %d", len(results))
			}
			// 消息类：post_url 已构造、similarity 三位舍入
			r0 := results[0]
			if r0["post_url"] != "https://t.me/c/123/9" {
				t.Errorf("post_url = %v", r0["post_url"])
			}
			if r0["similarity"] != 0.877 {
				t.Errorf("similarity = %v, 期望 0.877", r0["similarity"])
			}
			if r0["source_type"] != "message" || r0["message_id"] != float64(9) {
				t.Errorf("消息类字段漂移: %v", r0)
			}
			// 总结类：R9 不变量
			r1 := results[1]
			if r1["post_url"] != nil || len(r1["post_links"].([]any)) != 0 || r1["message_id"] != nil {
				t.Errorf("总结类不得有链接: %v", r1)
			}
			if r1["summary_id"] != float64(77) {
				t.Errorf("summary_id = %v", r1["summary_id"])
			}
			// 参数透传
			if c.vs.lastQuery != "测试" || c.vs.lastTopK != 20 {
				t.Errorf("参数透传漂移: %q %d", c.vs.lastQuery, c.vs.lastTopK)
			}
		})
	}

	// channel_id 过滤参数与日期边界透传
	vs := &fakeVectorStore{available: true, msgAvail: true}
	e := newExecutor(vs, nil, nil)
	_ = e.ExecuteJSON(context.Background(), "semantic_search",
		`{"query":"q","channel_id":"https://t.me/foo","date_after":"2026-01-01T00:00:00","top_k":5}`)
	if vs.lastFilter == nil || vs.lastFilter["channel_id"] != "https://t.me/foo" {
		t.Errorf("filterMetadata 漂移: %v", vs.lastFilter)
	}
	if vs.lastTopK != 5 {
		t.Errorf("top_k = %d", vs.lastTopK)
	}
}

// TestSemanticSearchResultIDIncrementAcrossTools 校验请求内 result_id 自增与
// rerank/detail 的复用（agent_tools.py:680-692）。
func TestSemanticSearchResultIDIncrementAcrossTools(t *testing.T) {
	vs := &fakeVectorStore{available: true, msgAvail: true, results: []map[string]any{
		{"doc_id": "1", "summary_text": "一"},
		{"doc_id": "2", "summary_text": "二"},
	}}
	e := newExecutor(vs, nil, nil)
	first := mustResults(t, e.ExecuteJSON(context.Background(), "semantic_search", `{"query":"q"}`))
	if first[0]["result_id"] != float64(1) || first[1]["result_id"] != float64(2) {
		t.Fatalf("result_id 自增漂移: %v", first)
	}

	// get_source_detail 按 result_id 取回完整文本（不截断）
	vs.results = []map[string]any{{"doc_id": "2", "summary_text": strings.Repeat("长", 600)}}
	_ = e.ExecuteJSON(context.Background(), "semantic_search", `{"query":"q"}`)
	detailRaw := e.ExecuteJSON(context.Background(), "get_source_detail", `{"result_id":3}`)
	detail := decodeJSON(t, detailRaw)["detail"].(map[string]any)
	if len([]rune(detail["summary_text"].(string))) != 600 {
		t.Errorf("detail 应为完整文本, got %d rune", len([]rune(detail["summary_text"].(string))))
	}
}

// ---------------------------------------------------------------------------
// keyword_search / get_recent_summaries（总结行归一化 + R9）
// ---------------------------------------------------------------------------

func TestExecuteKeywordSearch(t *testing.T) {
	mm := &fakeMemory{searchRows: []map[string]any{
		{"id": int64(11), "summary_text": "总结A", "channel_id": "https://t.me/a",
			"channel_name": "A", "created_at": "2026-04-19T00:00:00",
			// summary_message_ids 是机器人发布的总结报告消息 ID —— 工具层
			// 不消费该字段，更不得用于拼链接（历史 Bug 语义锚点）
			"summary_message_ids": []any{float64(9001)}},
		{"summary_text": "无ID行被跳过"},
	}}
	e := newExecutor(nil, mm, nil)
	results := mustResults(t, e.ExecuteJSON(context.Background(), "keyword_search", `{}`))
	if len(results) != 1 {
		t.Fatalf("结果数 = %d, 期望 1（无 id 行跳过）", len(results))
	}
	r := results[0]
	if r["source_type"] != "summary" || r["summary_id"] != float64(11) {
		t.Errorf("归一化漂移: %v", r)
	}
	if r["post_url"] != nil || len(r["post_links"].([]any)) != 0 || r["message_id"] != nil {
		t.Errorf("R9 违例：总结行不得产生链接/消息ID: %v", r)
	}
	// 关键词参数与默认值（keywords=[] 缺省、time_range_days=90、limit=10）
	if kw := mm.searchArgs[0].([]string); len(kw) != 0 {
		t.Errorf("keywords 缺省应为空列表")
	}
	if mm.searchArgs[2] != 90 || mm.searchArgs[4] != 10 {
		t.Errorf("默认参数漂移: %v", mm.searchArgs)
	}

	// 显式参数透传
	_ = e.ExecuteJSON(context.Background(), "keyword_search",
		`{"keywords":["AI"],"topics":["技术"],"time_range_days":7,"channel_id":"https://t.me/a","limit":3}`)
	if kw := mm.searchArgs[0].([]string); len(kw) != 1 || kw[0] != "AI" {
		t.Errorf("keywords 透传漂移: %v", mm.searchArgs[0])
	}
	if tp := mm.searchArgs[1].([]string); len(tp) != 1 || tp[0] != "技术" {
		t.Errorf("topics 透传漂移: %v", mm.searchArgs[1])
	}
	if mm.searchArgs[2] != 7 || mm.searchArgs[3] != "https://t.me/a" || mm.searchArgs[4] != 3 {
		t.Errorf("参数透传漂移: %v", mm.searchArgs)
	}
}

func TestExecuteRecentSummaries(t *testing.T) {
	mm := &fakeMemory{recentRows: []map[string]any{
		{"id": int64(5), "summary_text": "最近", "channel_id": "c", "channel_name": "n", "created_at": "t"},
		{"id": 0, "summary_id": int64(8), "summary_text": "id为0回退summary_id"}, // Python or 语义
		{"summary_text": "全无ID跳过"},
	}}
	e := newExecutor(nil, mm, nil)
	results := mustResults(t, e.ExecuteJSON(context.Background(), "get_recent_summaries",
		`{"channel_id":"https://t.me/x","limit":9,"time_range_days":3}`))
	if len(results) != 2 {
		t.Fatalf("结果数 = %d, 期望 2", len(results))
	}
	if results[1]["summary_id"] != float64(8) {
		t.Errorf("id=0 的 or 回退漂移: %v", results[1])
	}
	if results[0]["post_url"] != nil {
		t.Error("R9 违例：recent_summaries 不得产生 post_url")
	}
	if mm.recentLimit != 9 || mm.recentRange != 3 {
		t.Errorf("参数透传漂移: limit=%d range=%d", mm.recentLimit, mm.recentRange)
	}
	// time_range_days 缺省 → 0（等价 Python None）
	_ = e.ExecuteJSON(context.Background(), "get_recent_summaries", `{}`)
	if mm.recentRange != 0 || mm.recentLimit != 5 {
		t.Errorf("缺省参数漂移: limit=%d range=%d", mm.recentLimit, mm.recentRange)
	}
}

// ---------------------------------------------------------------------------
// rerank_results
// ---------------------------------------------------------------------------

func TestExecuteRerank(t *testing.T) {
	t.Run("常规重排", func(t *testing.T) {
		vs := &fakeVectorStore{available: true, msgAvail: true}
		e := newExecutor(vs, nil, &fakeReranker{available: true})
		// 先搜索填入 result store
		vs.results = []map[string]any{
			{"doc_id": "1", "summary_text": "一"},
			{"doc_id": "ch:2", "summary_text": "二"},
		}
		_ = e.ExecuteJSON(context.Background(), "semantic_search", `{"query":"q"}`)

		rr := &fakeReranker{available: true, out: []map[string]any{
			{"doc_id": "ch:2", "summary_text": "二", "rerank_score": 0.987654},
			{"doc_id": "1", "summary_text": "一", "rerank_score": 0.1},
		}}
		e2 := NewToolExecutor(vs, &fakeMemory{}, rr)
		_ = e2.ExecuteJSON(context.Background(), "semantic_search", `{"query":"q"}`)
		results := mustResults(t, e2.ExecuteJSON(context.Background(), "rerank_results",
			`{"query":"q","result_ids":[2,1,999]}`))
		if len(results) != 2 {
			t.Fatalf("结果数 = %d, 期望 2（999 不存在被跳过）", len(results))
		}
		if results[0]["rerank_score"] != 0.988 {
			t.Errorf("rerank_score round 漂移: %v", results[0]["rerank_score"])
		}
		if results[0]["result_id"] != float64(3) {
			t.Errorf("重排结果应重新入存自增: %v", results[0]["result_id"])
		}
		if len(rr.candidates) != 2 || rr.candidates[0]["doc_id"] != "ch:2" {
			t.Errorf("候选顺序/内容漂移: %#v", rr.candidates)
		}
	})

	t.Run("错误分支", func(t *testing.T) {
		e := newExecutor(nil, nil, &fakeReranker{available: false})
		m := decodeJSON(t, e.ExecuteJSON(context.Background(), "rerank_results",
			`{"query":"q","result_ids":[1]}`))
		if m["error"] != "重排序服务不可用" {
			t.Errorf("不可用文案漂移: %v", m["error"])
		}

		e2 := newExecutor(nil, nil, &fakeReranker{available: true})
		m2 := decodeJSON(t, e2.ExecuteJSON(context.Background(), "rerank_results",
			`{"query":"q","result_ids":[42]}`))
		if m2["error"] != "未找到有效的结果ID" {
			t.Errorf("无有效ID文案漂移: %v", m2["error"])
		}

		// str 逐字符迭代（可哈希全 miss）→ "未找到有效的结果ID"（审计 A-3）。
		e4 := newExecutor(nil, nil, &fakeReranker{available: true})
		m4 := decodeJSON(t, e4.ExecuteJSON(context.Background(), "rerank_results",
			`{"query":"q","result_ids":"xyz"}`))
		if m4["error"] != "未找到有效的结果ID" {
			t.Errorf("str result_ids 应逐字符迭代后全 miss: %v", m4["error"])
		}

		// dict 逐键迭代同理全 miss（审计 A-3）。
		e5 := newExecutor(nil, nil, &fakeReranker{available: true})
		m5 := decodeJSON(t, e5.ExecuteJSON(context.Background(), "rerank_results",
			`{"query":"q","result_ids":{"a":1}}`))
		if m5["error"] != "未找到有效的结果ID" {
			t.Errorf("dict result_ids 应逐键迭代后全 miss: %v", m5["error"])
		}

		// None 不可迭代 → TypeError（两侧一致）。
		e6 := newExecutor(nil, nil, &fakeReranker{available: true})
		m6 := decodeJSON(t, e6.ExecuteJSON(context.Background(), "rerank_results",
			`{"query":"q","result_ids":null}`))
		if m6["error"] != "工具执行失败: TypeError: 'NoneType' object is not iterable" {
			t.Errorf("null result_ids 应 TypeError: %v", m6["error"])
		}

		// 候选检查先于 query 求值（Python：args["query"] 在候选非空后才触发
		// KeyError）——有有效候选且缺 query 时才报 KeyError
		vs := &fakeVectorStore{available: true, msgAvail: true, results: []map[string]any{
			{"doc_id": "1", "summary_text": "一"},
		}}
		e3 := NewToolExecutor(vs, &fakeMemory{}, &fakeReranker{available: true})
		_ = e3.ExecuteJSON(context.Background(), "semantic_search", `{"query":"q"}`)
		m3 := decodeJSON(t, e3.ExecuteJSON(context.Background(), "rerank_results", `{"result_ids":[1]}`))
		if m3["error"] != "工具执行失败: KeyError: 'query'" {
			t.Errorf("缺 query 错误漂移: %v", m3["error"])
		}
	})
}

// TestExecuteSourceDetailUnhashableSummaryID summary_id 传 list → Python
// dict.get 抛 TypeError: unhashable type: 'list'（agent_tools.py:529，审计 A-4）。
func TestExecuteSourceDetailUnhashableSummaryID(t *testing.T) {
	e := newExecutor(nil, nil, &fakeReranker{available: true})
	m := decodeJSON(t, e.ExecuteJSON(context.Background(), "get_source_detail",
		`{"summary_id":[1]}`))
	if m["error"] != "工具执行失败: TypeError: unhashable type: 'list'" {
		t.Fatalf("unhashable summary_id 错误文案漂移: %v", m["error"])
	}
}

// ---------------------------------------------------------------------------
// list_channels / resolve_channel / stats / channel_info
// ---------------------------------------------------------------------------

func TestExecuteListChannels(t *testing.T) {
	mm := &fakeMemory{channels: []map[string]any{
		{"channel_id": "a", "summary_count": 1},
		{"channel_id": "b", "summary_count": 2, "avg_message_length": "55.50"},
	}}
	e := newExecutor(nil, mm, nil)
	m := decodeJSON(t, e.ExecuteJSON(context.Background(), "list_channels", `{"limit":1}`))
	if m["count"] != float64(1) || m["total"] != float64(2) {
		t.Errorf("count/total 漂移: %v", m)
	}
	if len(m["channels"].([]any)) != 1 {
		t.Errorf("channels 截断漂移")
	}
	// 缺省 limit=50；越界夹取 [1,100]
	_ = e.ExecuteJSON(context.Background(), "list_channels", `{}`)
	if len(m["channels"].([]any)) < 1 {
		t.Error("channels 缺失")
	}
	mx := decodeJSON(t, e.ExecuteJSON(context.Background(), "list_channels", `{"limit":9999}`))
	if len(mx["channels"].([]any)) != 2 {
		t.Error("limit>100 应夹取为 100（不足则取全部）")
	}
	mn := decodeJSON(t, e.ExecuteJSON(context.Background(), "list_channels", `{"limit":0}`))
	if len(mn["channels"].([]any)) != 1 {
		t.Error("limit<1 应夹取为 1")
	}
	// R21：DECIMAL 文本转数值
	ch2 := mx["channels"].([]any)[1].(map[string]any)
	if v, ok := ch2["avg_message_length"].(float64); !ok || v != 55.5 {
		t.Errorf("avg_message_length 未转数值: %v (%T)", ch2["avg_message_length"], ch2["avg_message_length"])
	}
}

func TestExecuteResolveChannelAndInfo(t *testing.T) {
	mm := &fakeMemory{
		resolve:  map[string]any{"success": false, "error": "未找到匹配频道", "candidates": []any{}},
		context:  "这是一个多频道总结系统。",
		channels: []map[string]any{{"channel_id": "a"}},
		stats:    map[string]any{"summary_count": float64(3)},
	}
	e := newExecutor(nil, mm, nil)

	m := decodeJSON(t, e.ExecuteJSON(context.Background(), "resolve_channel", `{"channel_hint":"foo"}`))
	if m["success"] != false || m["error"] != "未找到匹配频道" {
		t.Errorf("resolve 透传漂移: %v", m)
	}

	// channel_info：无 channel_id → 列表
	mi := decodeJSON(t, e.ExecuteJSON(context.Background(), "get_channel_info", `{}`))
	if _, ok := mi["channels"]; !ok || mi["count"] != float64(1) {
		t.Errorf("channel_info 列表分支漂移: %v", mi)
	}
	// 有 channel_id → 上下文
	mc := decodeJSON(t, e.ExecuteJSON(context.Background(), "get_channel_info", `{"channel_id":"https://t.me/x"}`))
	if mc["channel_id"] != "https://t.me/x" || mc["context"] != "这是一个多频道总结系统。" {
		t.Errorf("channel_info 上下文分支漂移: %v", mc)
	}

	ms := decodeJSON(t, e.ExecuteJSON(context.Background(), "get_channel_stats", `{"channel_id":"https://t.me/x"}`))
	stats := ms["stats"].(map[string]any)
	if stats["summary_count"] != float64(3) {
		t.Errorf("stats 透传漂移: %v", ms)
	}
}

// TestExecuteSourceDetailRefs 三级引用回退（result_id → summary_id → doc_id）。
func TestExecuteSourceDetailRefs(t *testing.T) {
	vs := &fakeVectorStore{available: true, msgAvail: true}
	e := newExecutor(vs, nil, nil)
	vs.results = []map[string]any{
		{"doc_id": "300:11", "summary_text": "消息十一"},
	}
	_ = e.ExecuteJSON(context.Background(), "semantic_search", `{"query":"q"}`)

	// doc_id 尾段引用（兼容旧方式）
	m := decodeJSON(t, e.ExecuteJSON(context.Background(), "get_source_detail", `{"doc_id":"300:11"}`))
	if m["error"] != nil {
		t.Fatalf("doc_id 引用失败: %v", m)
	}
	// 非法 result_id 字符串 → 回退 doc_id（对齐 except → None 继续）
	m2 := decodeJSON(t, e.ExecuteJSON(context.Background(), "get_source_detail",
		`{"result_id":"abc","doc_id":"300:11"}`))
	if m2["error"] != nil {
		t.Errorf("非法 result_id 应回退 doc_id: %v", m2)
	}
	// 全部未命中 → 固定文案
	m3 := decodeJSON(t, e.ExecuteJSON(context.Background(), "get_source_detail", `{"doc_id":"nope:1"}`))
	if m3["error"] != "未找到来源详情，请先执行搜索或提供有效的 summary_id/doc_id" {
		t.Errorf("未命中文案漂移: %v", m3["error"])
	}
}

// TestExecuteUnknownTool 未知工具文案。
func TestExecuteUnknownTool(t *testing.T) {
	e := newExecutor(nil, nil, nil)
	m := decodeJSON(t, e.ExecuteJSON(context.Background(), "no_such_tool", `{}`))
	if m["error"] != "未知工具: no_such_tool" {
		t.Errorf("未知工具文案漂移: %v", m["error"])
	}
}

// TestExecuteArgsParseFallback 参数 JSON 非法时按空字典继续
// （qa_engine_v3.py:366-370）。
func TestExecuteArgsParseFallback(t *testing.T) {
	e := newExecutor(nil, nil, nil)
	m := decodeJSON(t, e.ExecuteJSON(context.Background(), "get_channel_stats", `{bad json`))
	if m["error"] != nil {
		t.Fatalf("解析失败应回退空字典继续: %v", m)
	}
}

// TestExecutePanicNotEscaping 工具层 panic 不逃逸（等价 Python 全量捕获）。
func TestExecutePanicNotEscaping(t *testing.T) {
	vs := panickyStore{available: true}
	e := NewToolExecutor(vs, &fakeMemory{}, &fakeReranker{available: true})
	raw := e.ExecuteJSON(context.Background(), "semantic_search", `{"query":"q"}`)
	m := decodeJSON(t, raw)
	if !strings.HasPrefix(m["error"].(string), "工具执行失败: ") {
		t.Errorf("panic 应转错误 JSON: %v", m["error"])
	}
}

type panickyStore struct{ available bool }

func (f panickyStore) IsAvailable() bool         { return f.available }
func (f panickyStore) IsMessagesAvailable() bool { return true }
func (f panickyStore) SearchSimilar(context.Context, string, int, map[string]any, string, string) ([]map[string]any, error) {
	panic("boom")
}
func (f panickyStore) SearchMessages(context.Context, string, int, map[string]any, string, string) ([]map[string]any, error) {
	panic("boom")
}
func (f panickyStore) SearchAll(context.Context, string, int, map[string]any, string, string) ([]map[string]any, error) {
	panic("boom")
}
