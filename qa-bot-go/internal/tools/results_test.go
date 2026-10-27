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
	"strings"
	"testing"
	"time"
)

// TestRound3 校验 round(x, 3) 的冻结实现 math.Round(x*1000)/1000
// （README 硬规则 10）。
func TestRound3(t *testing.T) {
	cases := []struct {
		in, want float64
	}{
		{0.123456, 0.123},
		{0.1235, 0.124},
		{0.9994, 0.999},
		{0.9995, 1},
		{2.675, 2.675}, // 二进制近似：2.675*1000=2674.999…→2675→2.675
		{-0.123456, -0.123},
		{-1.23456, -1.235},
		{-1.5, -1.5}, // 千分位已对齐，原值不变
		{0, 0},
		{123.456789, 123.457},
	}
	for _, c := range cases {
		if got := round3(c.in); got != c.want {
			t.Errorf("round3(%v) = %v, 期望 %v", c.in, got, c.want)
		}
	}
}

// TestTruncateSummary 校验 500 码点截断（Python len/切片语义 = rune，硬规则 3）。
func TestTruncateSummary(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"恰500", strings.Repeat("字", 500), strings.Repeat("字", 500)},
		{"501截断", strings.Repeat("字", 501), strings.Repeat("字", 500) + "..."},
		{"多字节中英文混合", strings.Repeat("a你", 300), strings.Repeat("a你", 250) + "..."},
		{"空串", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := truncateSummary(c.in); got != c.want {
				t.Errorf("长度 %d rune, got 长度 %d", len([]rune(c.in)), len([]rune(got)))
			}
		})
	}
	// emoji 代理对按 rune（码点）计数，对齐 Python len
	emoji := strings.Repeat("\U0001F600", 600) // 600 个四字节 emoji
	got := truncateSummary(emoji)
	if n := len([]rune(got)); n != 503 {
		t.Errorf("emoji 截断后 rune 数 = %d, 期望 503", n)
	}
}

// TestResolveSourceType 校验来源类型判定（agent_tools.py:560-571）。
func TestResolveSourceType(t *testing.T) {
	cases := []struct {
		name   string
		result map[string]any
		want   string
	}{
		{"显式message", map[string]any{"source": "message"}, "message"},
		{"显式summary", map[string]any{"source": "summary"}, "summary"},
		{"显式未知值回退推断", map[string]any{"source": "other", "doc_id": "1:2"}, "message"},
		{"数字doc_id为summary", map[string]any{"doc_id": "123"}, "summary"},
		{"冒号doc_id推断message", map[string]any{"doc_id": "t.me/foo:45"}, "message"},
		{"有summary_id不推断", map[string]any{"summary_id": 7, "doc_id": "1:2"}, "summary"},
		{"无任何标记", map[string]any{}, "summary"},
		{"非字符串doc_id", map[string]any{"doc_id": 123}, "summary"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := resolveSourceType(c.result); got != c.want {
				t.Errorf("resolveSourceType = %q, 期望 %q", got, c.want)
			}
		})
	}
}

// TestPostURLInvariants R9 红线回归：post_url 只对 source_type=message 且
// 解析出真实 message_id 的结果构造；总结类一律无 post_url/message_id。
func TestPostURLInvariants(t *testing.T) {
	cases := []struct {
		name    string
		result  map[string]any
		wantURL string
		wantMID bool // message_id 是否非 nil
	}{
		{
			name: "消息_数字频道ID_doc解析",
			result: map[string]any{
				"doc_id": "-1001234567:42", "summary_text": "正文",
				"metadata": map[string]any{"channel_id": "-1001234567"},
			},
			wantURL: "https://t.me/c/1234567/42", wantMID: true,
		},
		{
			name: "消息_metadata_message_id优先",
			result: map[string]any{
				"doc_id": "-1001234567:42",
				"metadata": map[string]any{
					"channel_username": "pubchan", "message_id": float64(99),
				},
			},
			wantURL: "https://t.me/pubchan/99", wantMID: true,
		},
		{
			name: "消息_顶层channel_id",
			result: map[string]any{
				"message_id": 7, "doc_id": "https://t.me/foxy/:7", "channel_id": "https://t.me/foxy/",
			},
			wantURL: "https://t.me/foxy/7", wantMID: true,
		},
		{
			name:    "消息_无可用频道标识_仅doc兜底",
			result:  map[string]any{"doc_id": "888:5", "message_id": 5},
			wantURL: "https://t.me/c/888/5", wantMID: true,
		},
		{
			name:    "总结类_显式source_绝不拼链接",
			result:  map[string]any{"source": "summary", "summary_id": 12, "doc_id": "12"},
			wantURL: "", wantMID: false,
		},
		{
			name: "keyword_search行_发布消息ID不拼链接",
			result: map[string]any{
				"source": "summary", "summary_id": 33,
				"metadata": map[string]any{
					"channel_id": "https://t.me/foo", "message_id": 555, // 机器人发布消息 ID
				},
			},
			wantURL: "", wantMID: false, // serialized.message_id 对 summary 恒 null
		},
		{
			name:    "无message_id的消息不拼链接",
			result:  map[string]any{"doc_id": "abc-no-colon"},
			wantURL: "", wantMID: false,
		},
		{
			name:    "非法message_id_0",
			result:  map[string]any{"doc_id": "ch:0", "channel_id": "foo"},
			wantURL: "", wantMID: false,
		},
		{
			name:    "非法message_id_负数",
			result:  map[string]any{"doc_id": "ch:-5", "channel_id": "foo"},
			wantURL: "", wantMID: false,
		},
		{
			name: "邀请链接频道_无法验证",
			result: map[string]any{
				"message_id": 3, "doc_id": "+AbCdEf:3", "channel_id": "+AbCdEf",
			},
			wantURL: "", wantMID: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotURL := resolvePostURL(c.result)
			if gotURL != c.wantURL {
				t.Errorf("post_url = %q, 期望 %q", gotURL, c.wantURL)
			}
			s := serializeResult(c.result)
			if c.wantMID && s.MessageID == nil {
				t.Errorf("message_id 应非 null, got %v", s.MessageID)
			}
			if !c.wantMID && s.MessageID != nil {
				t.Errorf("message_id 应为 null, got %v", s.MessageID)
			}
			// post_links 兼容字段与 post_url 恒一致
			if c.wantURL == "" && (s.PostURL != nil || len(s.PostLinks) != 0) {
				t.Errorf("无链接时应为 post_url=null/post_links=[], got %v / %v", s.PostURL, s.PostLinks)
			}
			if c.wantURL != "" && (s.PostURL != c.wantURL || len(s.PostLinks) != 1 || s.PostLinks[0] != c.wantURL) {
				t.Errorf("post_links 漂移: %v / %v", s.PostURL, s.PostLinks)
			}
			// 消息类结果 summary_id 恒 null；总结类恒透传
			if c.wantURL != "" && s.SummaryID != nil {
				t.Errorf("消息类 summary_id 应为 null, got %v", s.SummaryID)
			}
		})
	}
}

// TestSerializeResult 键序、截断与条件字段。
func TestSerializeResult(t *testing.T) {
	result := map[string]any{
		"result_id":    1,
		"source":       "summary",
		"summary_id":   int64(9),
		"summary_text": strings.Repeat("长", 501),
		"metadata": map[string]any{
			"channel_id": "https://t.me/ch1", "channel_name": "频道一",
			"created_at": "2026-04-19T00:00:00",
		},
		"similarity": 0.123456,
	}
	s := serializeResult(result)
	if len([]rune(s.SummaryText)) != 503 {
		t.Errorf("summary_text 应截到 500+3, got %d rune", len([]rune(s.SummaryText)))
	}
	if s.SummaryID != int64(9) || s.MessageID != nil || s.PostURL != nil {
		t.Errorf("总结类 ID 字段漂移: %+v", s)
	}
	if s.Similarity == nil || *s.Similarity != 0.123 {
		t.Errorf("similarity round 漂移: %v", s.Similarity)
	}
	if s.RerankScore != nil {
		t.Error("无 rerank_score 时不得输出该字段")
	}
	if s.ChannelID != "https://t.me/ch1" || s.ChannelName != "频道一" {
		t.Errorf("元数据回退漂移: %v / %v", s.ChannelID, s.ChannelName)
	}
	if s.CreatedAt != "2026-04-19T00:00:00" {
		t.Errorf("created_at = %v", s.CreatedAt)
	}

	// 键序与 Python dict 字面量一致（result_id → post_links）
	order := []string{`"result_id":`, `"source_type":`, `"summary_id":`, `"summary_text":`,
		`"channel_id":`, `"channel_name":`, `"created_at":`, `"message_id":`,
		`"doc_id":`, `"post_url":`, `"post_links":`, `"similarity":`}
	js := marshalJSON(s)
	pos := -1
	for _, key := range order {
		next := strings.Index(js, key)
		if next <= pos {
			t.Errorf("键 %s 顺序漂移: %s", key, js)
			break
		}
		pos = next
	}

	// created_at 为 time.Time 时按 Python isoformat 输出
	result["metadata"].(map[string]any)["created_at"] = time.Date(2026, 4, 19, 1, 2, 3, 500000000, time.UTC)
	s2 := serializeResult(result)
	if s2.CreatedAt != "2026-04-19T01:02:03.500000" {
		t.Errorf("isoformat = %v", s2.CreatedAt)
	}

	// 元数据缺失时回退顶层键（metadata.get(x) or result.get(x)）
	fallback := map[string]any{
		"source": "summary", "summary_id": 1,
		"channel_id": "top", "channel_name": "顶层",
	}
	sf := serializeResult(fallback)
	if sf.ChannelID != "top" || sf.ChannelName != "顶层" {
		t.Errorf("顶层回退漂移: %v / %v", sf.ChannelID, sf.ChannelName)
	}
	// 元数据空串不遮蔽顶层（Python or 语义）
	fb2 := map[string]any{
		"source": "summary", "summary_id": 2,
		"metadata":   map[string]any{"channel_id": "", "channel_name": ""},
		"channel_id": "top2", "channel_name": "顶二",
	}
	sf2 := serializeResult(fb2)
	if sf2.ChannelID != "top2" || sf2.ChannelName != "顶二" {
		t.Errorf("or 回退漂移: %v / %v", sf2.ChannelID, sf2.ChannelName)
	}
}

// TestResultStoreBehavior 结果存储：自增 ID、三级索引、reset、批量读取。
func TestResultStoreBehavior(t *testing.T) {
	s := newResultStore()
	r1 := map[string]any{"summary_id": int64(100), "doc_id": "100", "summary_text": "a"}
	r2 := map[string]any{"doc_id": "ch:7", "summary_text": "b"}
	s.put(r1)
	s.put(r2)
	s.put(r1) // 同一对象重复入存：新 result_id 覆盖键（Python 行为一致）

	if s.next != 3 {
		t.Fatalf("next = %d, 期望 3", s.next)
	}
	if r1["result_id"] != 3 { // 第二次 put 覆盖 result_id
		t.Errorf("重复入存应覆盖 result_id, got %v", r1["result_id"])
	}
	if _, ok := s.get(2); !ok {
		t.Error("result_id=2 应存在")
	}
	// bySummary 键：int64(100) 与 float64(100)（JSON 解码）同键
	if _, ok := s.getBySummaryID(float64(100)); !ok {
		t.Error("float64(100) 应命中 int64(100) 键（Python == 语义）")
	}
	if _, ok := s.getBySummaryID("100"); ok {
		t.Error("字符串键不得命中整型键（Python dict 键类型敏感）")
	}
	if _, ok := s.getByDocID("ch:7"); !ok {
		t.Error("doc 索引缺失")
	}

	all := s.getAllResults()
	if len(all) != 2 {
		t.Errorf("getAllResults 去重后 = %d, 期望 2", len(all))
	}
	byIDs := s.getResultsByIDs([]int{1, 99, 2})
	if len(byIDs) != 2 || byIDs[0]["summary_text"] != "a" || byIDs[1]["summary_text"] != "b" {
		t.Errorf("getResultsByIDs 顺序/过滤漂移: %#v", byIDs)
	}

	s.reset()
	if s.next != 0 || len(s.getAllResults()) != 0 {
		t.Error("reset 未清空")
	}
}

// TestNormalizeDecimalFields R21：DECIMAL 文本 → JSON 数值。
func TestNormalizeDecimalFields(t *testing.T) {
	m := map[string]any{"avg_message_length": "123.45", "other": "9"}
	normalizeDecimalFields(m)
	if v, ok := m["avg_message_length"].(float64); !ok || v != 123.45 {
		t.Errorf("DECIMAL 未转数值: %v (%T)", m["avg_message_length"], m["avg_message_length"])
	}
	if m["other"] != "9" {
		t.Error("非目标字段不得被改写")
	}
	m2 := map[string]any{"avg_message_length": []byte("42.00")}
	normalizeDecimalFields(m2)
	if v, ok := m2["avg_message_length"].(float64); !ok || v != 42 {
		t.Errorf("[]byte DECIMAL 未转数值: %v", m2["avg_message_length"])
	}
	normalizeDecimalFields(nil) // 不得 panic
}

// TestPyIntSemantics Python int() 语义的关键分支。
func TestPyIntSemantics(t *testing.T) {
	if n, err := pyInt(float64(1.9)); err != nil || n != 1 {
		t.Errorf("int(1.9) = %v, %v", n, err)
	}
	if n, err := pyInt("  7 "); err != nil || n != 7 {
		t.Errorf(`int("  7 ") = %v, %v`, n, err)
	}
	if _, err := pyInt("3.5"); err == nil || errTypeName(err) != "ValueError" {
		t.Errorf(`int("3.5") 应 ValueError, got %v`, err)
	}
	if _, err := pyInt(nil); err == nil || errTypeName(err) != "TypeError" {
		t.Errorf("int(nil) 应 TypeError, got %v", err)
	}
	if n, err := pyInt(true); err != nil || n != 1 {
		t.Errorf("int(True) = %v, %v", n, err)
	}
	if _, err := pyInt([]any{}); err == nil || errTypeName(err) != "TypeError" {
		t.Errorf("int([]) 应 TypeError, got %v", err)
	}
	// KeyError 文本形态：Python str(KeyError('q')) == "'q'"
	if keyErr("query").Error() != "KeyError: 'query'" {
		t.Errorf("KeyError 形态漂移: %v", keyErr("query").Error())
	}
}
