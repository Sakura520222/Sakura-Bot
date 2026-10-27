// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package llm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// flushWriter 在每次写入后刷新，模拟真实 SSE 分片到达。
type flushWriter struct{ http.ResponseWriter }

func (w flushWriter) Write(p []byte) (int, error) {
	n, err := w.ResponseWriter.Write(p)
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
	return n, err
}

// sseLines 把若干 SSE 行写成一个事件块（行间 \n，块尾双 \n）。
func sseLines(lines ...string) string {
	return strings.Join(lines, "\n") + "\n\n"
}

// newTestClient 构造指向 httptest 服务、配置固定的客户端。
func newTestClient(t *testing.T, serverURL string, opts ...Option) *Client {
	t.Helper()
	provider := AIConfigFunc(func() AIConfig {
		return AIConfig{APIKey: "key-1", BaseURL: serverURL, Model: "deepseek-chat"}
	})
	return NewClient(provider, opts...)
}

// collectDeltas 消费整个流，返回全部 delta 与最终错误。
func collectDeltas(t *testing.T, s *Stream) ([]Delta, error) {
	t.Helper()
	defer s.Close()
	var got []Delta
	for {
		d, err := s.Next()
		if errors.Is(err, io.EOF) {
			return got, nil
		}
		if err != nil {
			return got, err
		}
		got = append(got, d)
	}
}

// TestStreamChatContentDeltas 验证 content 增量与 [DONE] 正常终止。
func TestStreamChatContentDeltas(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fw := flushWriter{w}
		_, _ = fw.Write([]byte(sseLines(`data: {"choices":[{"delta":{"role":"assistant","content":"你"}}]}`)))
		_, _ = fw.Write([]byte(sseLines(`data: {"choices":[{"delta":{"content":"好"}}]}`)))
		_, _ = fw.Write([]byte(sseLines(`data: {"choices":[{"delta":{"content":"，世界"}}]}`)))
		_, _ = fw.Write([]byte(sseLines(`data: [DONE]`)))
	}))
	defer srv.Close()

	s, err := newTestClient(t, srv.URL).StreamChat(context.Background(), PlainChatRequest(nil))
	if err != nil {
		t.Fatalf("StreamChat 失败: %v", err)
	}
	deltas, err := collectDeltas(t, s)
	if err != nil {
		t.Fatalf("流消费失败: %v", err)
	}
	var sb strings.Builder
	for _, d := range deltas {
		sb.WriteString(d.Content)
	}
	if got := sb.String(); got != "你好，世界" {
		t.Fatalf("content 增量拼接 = %q, 期望 %q", got, "你好，世界")
	}
	// EOF 后再取仍为 EOF
	if _, err := s.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("终止后 Next = %v, 期望 io.EOF", err)
	}
}

// TestStreamChatToolCallDeltas 验证 tool_calls 增量按协议原样透传。
func TestStreamChatToolCallDeltas(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fw := flushWriter{w}
		_, _ = fw.Write([]byte(sseLines(`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_abc","type":"function","function":{"name":"semantic_search","arguments":""}}]}}]}`)))
		_, _ = fw.Write([]byte(sseLines(`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"query\":"}}]}}]}`)))
		_, _ = fw.Write([]byte(sseLines(`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"测试\"}"}}]}}]}`)))
		_, _ = fw.Write([]byte(sseLines(`data: {"choices":[{"delta":{"tool_calls":[{"index":1,"id":"call_def","type":"function","function":{"name":"list_channels","arguments":"{}"}}]}}]}`)))
		_, _ = fw.Write([]byte(sseLines(`data: [DONE]`)))
	}))
	defer srv.Close()

	s, err := newTestClient(t, srv.URL).StreamChat(context.Background(), PlainChatRequest(nil))
	if err != nil {
		t.Fatalf("StreamChat 失败: %v", err)
	}
	deltas, err := collectDeltas(t, s)
	if err != nil {
		t.Fatalf("流消费失败: %v", err)
	}
	if len(deltas) != 4 {
		t.Fatalf("delta 数 = %d, 期望 4", len(deltas))
	}

	// 分片 0：id/name/index 原样
	d0 := deltas[0]
	if d0.ToolCalls[0].ID == nil || *d0.ToolCalls[0].ID != "call_abc" {
		t.Fatalf("分片0 id = %v, 期望 call_abc", d0.ToolCalls[0].ID)
	}
	if d0.ToolCalls[0].Index == nil || *d0.ToolCalls[0].Index != 0 {
		t.Fatalf("分片0 index = %v, 期望 0", d0.ToolCalls[0].Index)
	}
	if d0.ToolCalls[0].Function == nil || d0.ToolCalls[0].Function.Name == nil ||
		*d0.ToolCalls[0].Function.Name != "semantic_search" {
		t.Fatalf("分片0 name 透传错误: %+v", d0.ToolCalls[0].Function)
	}

	// 分片 1/2：arguments 增量分片原样（无 id/name 字段 → nil）
	if got := *deltas[1].ToolCalls[0].Function.Arguments; got != `{"query":` {
		t.Fatalf("分片1 arguments = %q", got)
	}
	if deltas[1].ToolCalls[0].ID != nil {
		t.Fatalf("分片1 不应携带 id，得到 %v", *deltas[1].ToolCalls[0].ID)
	}
	if got := *deltas[2].ToolCalls[0].Function.Arguments; got != `"测试"}` {
		t.Fatalf("分片2 arguments = %q", got)
	}

	// 分片 3：第二个 tool_call（index=1）
	if got := *deltas[3].ToolCalls[0].Function.Name; got != "list_channels" {
		t.Fatalf("分片3 name = %q", got)
	}
}

// TestStreamChatBrokenStream 验证断流（连接在 [DONE] 前中断）返回错误。
func TestStreamChatBrokenStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fw := flushWriter{w}
		_, _ = fw.Write([]byte(sseLines(`data: {"choices":[{"delta":{"content":"部分"}}]}`)))
		// 直接返回：连接干净关闭，无 [DONE]
	}))
	defer srv.Close()

	s, err := newTestClient(t, srv.URL).StreamChat(context.Background(), PlainChatRequest(nil))
	if err != nil {
		t.Fatalf("StreamChat 失败: %v", err)
	}
	defer s.Close()

	d, err := s.Next()
	if err != nil {
		t.Fatalf("首个分片不应出错: %v", err)
	}
	if d.Content != "部分" {
		t.Fatalf("首个分片 content = %q", d.Content)
	}
	_, err = s.Next()
	if err == nil {
		t.Fatal("断流应返回错误")
	}
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("断流错误应包装 io.ErrUnexpectedEOF, 得到 %v", err)
	}
}

// TestStreamChatNon200 验证非 200 响应返回 *APIError。
func TestStreamChatNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"quota exceeded"}}`, http.StatusTooManyRequests)
	}))
	defer srv.Close()

	_, err := newTestClient(t, srv.URL).StreamChat(context.Background(), PlainChatRequest(nil))
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("应返回 *APIError, 得到 %v", err)
	}
	if apiErr.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("状态码 = %d, 期望 429", apiErr.StatusCode)
	}
	if !strings.Contains(apiErr.Body, "quota exceeded") {
		t.Fatalf("错误体应包含响应片段, 得到 %q", apiErr.Body)
	}
	if !strings.Contains(apiErr.Error(), "429") {
		t.Fatalf("错误信息应包含状态码, 得到 %q", apiErr.Error())
	}
}

// TestStreamChatRequestShape 验证请求方法、路径、鉴权头与请求体逐字段对齐
// qa_engine_v3.py:316-323（agentic 调用点）。
func TestStreamChatRequestShape(t *testing.T) {
	var gotMethod, gotPath, gotAuth, gotContentType string
	var gotBody chatRequestWire
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotContentType = r.Header.Get("Content-Type")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = flushWriter{w}.Write([]byte(sseLines(`data: [DONE]`)))
	}))
	defer srv.Close()

	tools := json.RawMessage(`[{"type":"function","function":{"name":"semantic_search"}}]`)
	msgs := []Message{
		{Role: "system", Content: Str("系统提示")},
		{Role: "user", Content: Str("问题")},
	}
	req := AgenticChatRequest(msgs, tools)
	if req.Temperature != 0.7 || req.ToolChoice != "auto" {
		t.Fatalf("AgenticChatRequest 字段错误: %+v", req)
	}
	plain := PlainChatRequest(msgs)
	if plain.Temperature != 0.7 || plain.ToolChoice != "" || plain.Tools != nil {
		t.Fatalf("PlainChatRequest 字段错误: %+v", plain)
	}

	s, err := newTestClient(t, srv.URL).StreamChat(context.Background(), req)
	if err != nil {
		t.Fatalf("StreamChat 失败: %v", err)
	}
	if _, err := collectDeltas(t, s); err != nil {
		t.Fatalf("流消费失败: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Fatalf("method = %s, 期望 POST", gotMethod)
	}
	if gotPath != "/chat/completions" {
		t.Fatalf("path = %s, 期望 /chat/completions", gotPath)
	}
	if gotAuth != "Bearer key-1" {
		t.Fatalf("Authorization = %q, 期望 Bearer key-1", gotAuth)
	}
	if !strings.HasPrefix(gotContentType, "application/json") {
		t.Fatalf("Content-Type = %q", gotContentType)
	}
	if gotBody.Model != "deepseek-chat" {
		t.Fatalf("model = %q, 期望 deepseek-chat（来自每请求 provider）", gotBody.Model)
	}
	if !gotBody.Stream {
		t.Fatal("stream 应恒为 true")
	}
	if gotBody.Temperature != 0.7 {
		t.Fatalf("temperature = %v, 期望 0.7", gotBody.Temperature)
	}
	if gotBody.ToolChoice != "auto" {
		t.Fatalf("tool_choice = %q, 期望 auto", gotBody.ToolChoice)
	}
	if string(gotBody.Tools) != string(tools) {
		t.Fatalf("tools 原样透传失败: %s", gotBody.Tools)
	}
	if len(gotBody.Messages) != 2 || *gotBody.Messages[0].Content != "系统提示" ||
		*gotBody.Messages[1].Content != "问题" {
		t.Fatalf("messages 编码错误: %+v", gotBody.Messages)
	}
}

// TestStreamChatIdleTimeout 验证空闲读超时中断流（生产默认 120s，此处注入 80ms）。
func TestStreamChatIdleTimeout(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = flushWriter{w}.Write([]byte(sseLines(`data: {"choices":[{"delta":{"content":"首片"}}]}`)))
		<-block // 保持连接打开、不再发送任何数据
	}))
	defer srv.Close()
	defer close(block)

	client := newTestClient(t, srv.URL, WithIdleReadTimeout(80*time.Millisecond))
	s, err := client.StreamChat(context.Background(), PlainChatRequest(nil))
	if err != nil {
		t.Fatalf("StreamChat 失败: %v", err)
	}
	defer s.Close()

	d, err := s.Next()
	if err != nil {
		t.Fatalf("首个分片不应出错: %v", err)
	}
	if d.Content != "首片" {
		t.Fatalf("首个分片 content = %q", d.Content)
	}
	start := time.Now()
	_, err = s.Next()
	if err == nil {
		t.Fatal("空闲超时应返回错误")
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("空闲超时未及时中断: %v", time.Since(start))
	}
	t.Logf("空闲超时错误: %v", err)
}

// TestStreamChatParentContextCancel 验证父上下文取消传播到流读取。
func TestStreamChatParentContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = flushWriter{w}.Write([]byte(sseLines(`data: {"choices":[{"delta":{"content":"x"}}]}`)))
		<-r.Context().Done()
	}))
	defer srv.Close()

	s, err := newTestClient(t, srv.URL).StreamChat(ctx, PlainChatRequest(nil))
	if err != nil {
		t.Fatalf("StreamChat 失败: %v", err)
	}
	defer s.Close()

	if _, err := s.Next(); err != nil {
		t.Fatalf("首个分片不应出错: %v", err)
	}
	cancel()
	_, err = s.Next()
	if err == nil {
		t.Fatal("父上下文取消后应返回错误")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("错误应包装 context.Canceled, 得到 %v", err)
	}
}

// TestSSEDataPayload 验证 data 行提取的两种写法与非 data 行过滤。
func TestSSEDataPayload(t *testing.T) {
	cases := []struct {
		line    string
		payload string
		ok      bool
	}{
		{`data: {"a":1}`, `{"a":1}`, true},
		{`data:{"a":1}`, `{"a":1}`, true},
		{`data: [DONE]`, `[DONE]`, true},
		{"event: message", "", false},
		{": keep-alive", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		payload, ok := sseDataPayload(c.line)
		if ok != c.ok || payload != c.payload {
			t.Fatalf("sseDataPayload(%q) = (%q,%v), 期望 (%q,%v)", c.line, payload, ok, c.payload, c.ok)
		}
	}
}

// intPtr / strPtr 测试辅助。
func intPtr(i int) *int       { return &i }
func strPtr(s string) *string { return &s }

// TestAccumulateToolCall 验证增量聚合规则（qa_engine_v3.py:460-484，R7）：
// index 缺失按 0、id/name 非空覆盖、arguments 追加、多槽位隔离。
func TestAccumulateToolCall(t *testing.T) {
	acc := map[int]*ToolCallSlot{}
	AccumulateToolCall(acc, ToolCallDelta{
		Index: intPtr(0), ID: strPtr("call_a"), Type: strPtr("function"),
		Function: &FunctionDelta{Name: strPtr("semantic_search"), Arguments: strPtr(`{"q`)},
	})
	// 缺 index 的分片落在槽 0，arguments 继续追加
	AccumulateToolCall(acc, ToolCallDelta{
		Function: &FunctionDelta{Arguments: strPtr(`uery":"x"}`)},
	})
	// 第二个槽位
	AccumulateToolCall(acc, ToolCallDelta{
		Index: intPtr(1), ID: strPtr("call_b"),
		Function: &FunctionDelta{Name: strPtr("list_channels"), Arguments: strPtr(`{}`)},
	})
	// 重复非空 name：覆盖语义（协议上首片完整，重发以最新为准）
	AccumulateToolCall(acc, ToolCallDelta{
		Index:    intPtr(1),
		Function: &FunctionDelta{Name: strPtr("list_channels"), Arguments: strPtr("")},
	})

	if len(acc) != 2 {
		t.Fatalf("槽位数 = %d, 期望 2", len(acc))
	}
	s0 := acc[0]
	if s0.ID != "call_a" || s0.Name != "semantic_search" || s0.Arguments != `{"query":"x"}` {
		t.Fatalf("槽0 聚合错误: %+v", s0)
	}
	s1 := acc[1]
	if s1.ID != "call_b" || s1.Name != "list_channels" || s1.Arguments != `{}` {
		t.Fatalf("槽1 聚合错误: %+v", s1)
	}

	// 空 arguments 分片不得追加、空 name 不得覆盖
	AccumulateToolCall(acc, ToolCallDelta{
		Index:    intPtr(0),
		Function: &FunctionDelta{Name: strPtr(""), Arguments: strPtr("")},
	})
	if acc[0].Arguments != `{"query":"x"}` {
		t.Fatalf("空分片不应追加: %q", acc[0].Arguments)
	}
}
