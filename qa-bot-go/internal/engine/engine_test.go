// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：测试辅助代码，随源码分发，遵循项目 AGPL-3.0 许可
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package engine

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/contracts"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/db"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/llm"
)

// ────────────────────────────── 测试基建 ──────────────────────────────

// discardLogger 静默日志，保持测试输出干净。
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// constPersona / constMemory / errMemory 假依赖实现。
type constPersona string

func (c constPersona) Persona() string { return string(c) }

type constMemory string

func (c constMemory) GetChannelContext(ctx context.Context) (string, error) {
	return string(c), nil
}

type errMemory struct{}

func (errMemory) GetChannelContext(ctx context.Context) (string, error) {
	return "", errors.New("频道上下文故障")
}

// fakeExecutor 记录工具调用并返回固定 JSON 结果。
type fakeExecutor struct {
	mu    sync.Mutex
	calls []toolCallRec
	resp  string
}

// toolCallRec 记录一次工具调用的名称与参数。
type toolCallRec struct {
	name string
	args map[string]any
}

// Execute 实现 engine.ToolExecutor。
func (f *fakeExecutor) Execute(ctx context.Context, name string, args map[string]any) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, toolCallRec{name: name, args: args})
	if f.resp != "" {
		return f.resp, nil
	}
	return `{"result":"ok"}`, nil
}

// recorded 返回调用记录副本。
func (f *fakeExecutor) recorded() []toolCallRec {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]toolCallRec(nil), f.calls...)
}

// fakeTools 每请求返回同一执行器（单引擎实例内顺序测试足够）。
type fakeTools struct {
	schemas json.RawMessage
	exec    *fakeExecutor
}

// ToolSchemasJSON 实现 engine.Tools。
func (f *fakeTools) ToolSchemasJSON() json.RawMessage { return f.schemas }

// NewExecutor 实现 engine.Tools。
func (f *fakeTools) NewExecutor() ToolExecutor { return f.exec }

// fakeStore 内存会话仓储。
type fakeStore struct {
	mu      sync.Mutex
	history []db.HistoryItem // GetConversationHistory 的预置返回
	saved   []db.HistoryItem // SaveConversation 的追加记录
	cleared []string         // ClearUserConversations 收到的 sessionID 实参
	getErr  error
	saveErr error
}

// SaveConversation 实现 engine.ConversationStore。
func (f *fakeStore) SaveConversation(ctx context.Context, userID int64, sessionID, role, content string, metadata map[string]any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.saveErr != nil {
		return f.saveErr
	}
	f.saved = append(f.saved, db.HistoryItem{Role: role, Content: content})
	return nil
}

// GetConversationHistory 实现 engine.ConversationStore。
func (f *fakeStore) GetConversationHistory(ctx context.Context, userID int64, sessionID string, limit int) ([]db.HistoryItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.history, f.getErr
}

// ClearUserConversations 实现 engine.ConversationStore。
func (f *fakeStore) ClearUserConversations(ctx context.Context, userID int64, sessionID string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cleared = append(f.cleared, sessionID)
	return 3, nil
}

// ────────────────────────────── 假 LLM 服务 ──────────────────────────────

// flushWriter 每次写入后刷新，模拟真实 SSE 分片到达。
type flushWriter struct{ http.ResponseWriter }

// Write 实现(io.Writer)。
func (w flushWriter) Write(p []byte) (int, error) {
	n, err := w.ResponseWriter.Write(p)
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
	return n, err
}

// scriptEntry 是一次请求的响应脚本。
type scriptEntry struct {
	lines    []string // SSE data 行（[DONE] 由服务端统一追加）
	status   int      // HTTP 状态码；0 按 200 处理
	truncate bool     // true → 写完 lines 后直接断流（不发 [DONE]）
}

// wireRequest/wireMessage 捕获到的请求体（仅断言用字段）。
type wireRequest struct {
	Model       string          `json:"model"`
	Messages    []wireMessage   `json:"messages"`
	Tools       json.RawMessage `json:"tools"`
	ToolChoice  string          `json:"tool_choice"`
	Temperature float64         `json:"temperature"`
	Stream      bool            `json:"stream"`
}

type wireMessage struct {
	Role       string         `json:"role"`
	Content    *string        `json:"content"`
	ToolCalls  []wireToolCall `json:"tool_calls"`
	ToolCallID string         `json:"tool_call_id"`
}

// text 以 nil 安全方式读取 Content。
func (m wireMessage) text() string {
	if m.Content == nil {
		return ""
	}
	return *m.Content
}

type wireToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// scriptedLLM 按请求序次回放脚本并记录请求体的假 LLM 服务。
type scriptedLLM struct {
	*httptest.Server
	client *llm.Client

	mu      sync.Mutex
	entries []scriptEntry
	bodies  []wireRequest
}

// newScriptedLLM 启动假 LLM 服务。
func newScriptedLLM(t *testing.T, entries []scriptEntry) *scriptedLLM {
	t.Helper()
	s := &scriptedLLM{entries: entries}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var wr wireRequest
		_ = json.Unmarshal(body, &wr)
		s.mu.Lock()
		idx := len(s.bodies)
		s.bodies = append(s.bodies, wr)
		var entry scriptEntry
		if idx < len(s.entries) {
			entry = s.entries[idx]
		} else {
			entry = scriptEntry{status: 500}
		}
		s.mu.Unlock()

		if entry.status != 0 && entry.status != http.StatusOK {
			w.WriteHeader(entry.status)
			_, _ = w.Write([]byte(`{"error":"scripted"}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fw := flushWriter{w}
		for _, line := range entry.lines {
			_, _ = fw.Write([]byte(line + "\n\n"))
		}
		if !entry.truncate {
			_, _ = fw.Write([]byte("data: [DONE]\n\n"))
		}
	}))
	s.client = llm.NewClient(llm.AIConfigFunc(func() llm.AIConfig {
		return llm.AIConfig{APIKey: "k", BaseURL: s.Server.URL, Model: "deepseek-chat"}
	}))
	t.Cleanup(s.Server.Close)
	return s
}

// appendEntry 追加响应脚本（顺序测试用）。
func (s *scriptedLLM) appendEntry(e scriptEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = append(s.entries, e)
}

// requestBody 返回第 i 个请求的解析体。
func (s *scriptedLLM) requestBody(t *testing.T, i int) wireRequest {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if i >= len(s.bodies) {
		t.Fatalf("第 %d 个请求尚未到达（共 %d 个）", i+1, len(s.bodies))
	}
	return s.bodies[i]
}

// requestCount 返回已收到的请求数。
func (s *scriptedLLM) requestCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.bodies)
}

// contentLine 构造一条文本增量的 SSE data 行。
func contentLine(s string) string {
	return `data: {"choices":[{"delta":{"content":` + strconv.Quote(s) + `}}]}`
}

// ────────────────────────────── 装配与收集 ──────────────────────────────

// engineHarness 聚合被测引擎与全部假依赖。
type engineHarness struct {
	engine *Engine
	llm    *scriptedLLM
	tools  *fakeTools
	store  *fakeStore
}

// newHarness 构造带一段响应脚本的引擎测试环境。
func newHarness(t *testing.T, entries []scriptEntry) *engineHarness {
	t.Helper()
	h := &engineHarness{
		llm:   newScriptedLLM(t, entries),
		tools: &fakeTools{schemas: json.RawMessage(`[{"type":"function","function":{"name":"semantic_search"}}]`)},
		store: &fakeStore{},
	}
	h.tools.exec = &fakeExecutor{}
	conv := NewConversations(h.store)
	conv.log = discardLogger()
	h.engine = New(Deps{
		Chat:          h.llm.client,
		Tools:         h.tools,
		Conversations: conv,
		Persona:       constPersona("测试人格"),
		Memory:        constMemory("这是一个多频道总结系统。"),
		Logger:        discardLogger(),
	})
	return h
}

// collectChunks 消费整个输出通道（超时 5s 防挂死）。
func collectChunks(t *testing.T, ch <-chan Chunk) []Chunk {
	t.Helper()
	var out []Chunk
	timeout := time.After(5 * time.Second)
	for {
		select {
		case c, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, c)
		case <-timeout:
			t.Fatalf("等待 chunk 超时，已收到 %d个", len(out))
		}
	}
}

// assertChunks 逐一断言收到的块序列。
func assertChunks(t *testing.T, got, want []Chunk) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("chunk 数 = %d, 期望 %d；got=%+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i].Kind != want[i].Kind || got[i].Text != want[i].Text ||
			got[i].Ctrl != want[i].Ctrl || got[i].Err != want[i].Err {
			t.Fatalf("chunk[%d] = %+v, 期望 %+v", i, got[i], want[i])
		}
	}
}

// ────────────────────────────── 流协议测试 ──────────────────────────────

// TestProcessQueryStreamControlMarkers 控制标记协议 + 会话前缀持久化 + 请求形态。
func TestProcessQueryStreamControlMarkers(t *testing.T) {
	h := newHarness(t, []scriptEntry{
		{lines: []string{contentLine("你"), contentLine("好")}},
	})

	ch := h.engine.ProcessQueryStream(context.Background(), "你好", 42, "")
	chunks := collectChunks(t, ch)

	assertChunks(t, chunks, []Chunk{
		CtrlChunk(contracts.CtrlNewSession),
		TextChunk("你"),
		TextChunk("好"),
		CtrlChunk(contracts.CtrlDone),
	})

	// 持久化顺序与内容：user 先行，assistant 带新会话前缀
	h.store.mu.Lock()
	saved := append([]db.HistoryItem(nil), h.store.saved...)
	h.store.mu.Unlock()
	if len(saved) != 2 {
		t.Fatalf("持久化条数 = %d, 期望 2", len(saved))
	}
	if saved[0].Role != "user" || saved[0].Content != "你好" {
		t.Fatalf("第一条 = %s/%q, 期望 user/你好", saved[0].Role, saved[0].Content)
	}
	if saved[1].Role != "assistant" || saved[1].Content != "🍃 *开始新的对话。*\n\n你好" {
		t.Fatalf("第二条 = %s/%q, 期望 assistant 带 🍃 前缀", saved[1].Role, saved[1].Content)
	}

	// 首个请求体：system + 当前消息（无历史）；带 tools/tool_choice="auto"
	req := h.llm.requestBody(t, 0)
	if len(req.Messages) != 2 {
		t.Fatalf("首请求消息数 = %d, 期望 2", len(req.Messages))
	}
	if req.Messages[0].Role != "system" || !strings.Contains(req.Messages[0].text(), "测试人格") {
		t.Fatalf("首请求应为带人格的 system 消息: %+v", req.Messages[0])
	}
	if req.Messages[1].Role != "user" || req.Messages[1].text() != "你好" {
		t.Fatalf("末条消息应为当前查询: %+v", req.Messages[1])
	}
	if req.Tools == nil {
		t.Fatal("agentic 请求应携带工具 schema")
	}
	if req.ToolChoice != "auto" || req.Temperature != 0.7 || !req.Stream {
		t.Fatalf("请求参数不符: %+v", req)
	}

	// ── 同用户第二问：缓存续用会话，不再有 __NEW_SESSION__；历史进入请求 ──
	h.store.history = append([]db.HistoryItem(nil), saved...)
	h.llm.appendEntry(scriptEntry{lines: []string{contentLine("回答2")}})

	ch2 := h.engine.ProcessQueryStream(context.Background(), "再问", 42, "")
	chunks2 := collectChunks(t, ch2)
	assertChunks(t, chunks2, []Chunk{
		TextChunk("回答2"),
		CtrlChunk(contracts.CtrlDone),
	})

	// 第 4/5 条持久化：assistant 不再带前缀
	h.store.mu.Lock()
	saved2 := append([]db.HistoryItem(nil), h.store.saved...)
	h.store.mu.Unlock()
	if len(saved2) != 4 {
		t.Fatalf("二次持久化条数 = %d, 期望 4", len(saved2))
	}
	if saved2[3].Content != "回答2" {
		t.Fatalf("二次 assistant 持久化 = %q, 期望不带前缀的 %q", saved2[3].Content, "回答2")
	}

	// 第二个请求体：system + 历史(user/assistant) + 当前消息
	req2 := h.llm.requestBody(t, 1)
	if len(req2.Messages) != 4 {
		t.Fatalf("二请求消息数 = %d, 期望 4", len(req2.Messages))
	}
	if req2.Messages[1].Role != "user" || req2.Messages[1].text() != "你好" ||
		req2.Messages[2].Role != "assistant" || req2.Messages[2].text() != "🍃 *开始新的对话。*\n\n你好" {
		t.Fatalf("二请求应携带历史消息: %+v", req2.Messages)
	}
}

// TestProcessQueryStreamToolCallAggregation 对接假 LLM：tool_call 增量聚合、
// 缺 id 兜底 call_{index}、参数解析失败兜底 {}、tool 消息回填。
func TestProcessQueryStreamToolCallAggregation(t *testing.T) {
	h := newHarness(t, []scriptEntry{
		{lines: []string{
			contentLine("我先检索一下"),
			`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_a","type":"function","function":{"name":"semantic_search","arguments":""}}]}}]}`,
			`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"query\":"}}]}}]}`,
			`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"测试\"}"}}]}}]}`,
			// 第二个工具完全缺 id → 物化时兜底 call_1
			`data: {"choices":[{"delta":{"tool_calls":[{"index":1,"function":{"name":"keyword_search","arguments":"{}"}}]}}]}`,
			// 第三个工具参数不是 JSON 对象 → 解析失败兜底 {}
			`data: {"choices":[{"delta":{"tool_calls":[{"index":2,"id":"call_c","type":"function","function":{"name":"get_channel_stats","arguments":"\"oops\""}}]}}]}`,
		}},
		{lines: []string{contentLine("最终回答")}},
	})

	ch := h.engine.ProcessQueryStream(context.Background(), "测试", 7, "")
	chunks := collectChunks(t, ch)
	assertChunks(t, chunks, []Chunk{
		CtrlChunk(contracts.CtrlNewSession),
		TextChunk("我先检索一下"),
		TextChunk("最终回答"),
		CtrlChunk(contracts.CtrlDone),
	})

	// 工具被真实执行：参数为聚合后的完整 JSON
	calls := h.tools.exec.recorded()
	if len(calls) != 3 {
		t.Fatalf("工具调用数 = %d, 期望 3", len(calls))
	}
	if calls[0].name != "semantic_search" || calls[0].args["query"] != "测试" {
		t.Fatalf("工具调用[0] = %+v", calls[0])
	}
	if calls[1].name != "keyword_search" || len(calls[1].args) != 0 {
		t.Fatalf("工具调用[1] = %+v", calls[1])
	}
	if calls[2].name != "get_channel_stats" || len(calls[2].args) != 0 {
		t.Fatalf("工具调用[2]（参数解析失败应兜底空 dict）= %+v", calls[2])
	}

	// 第二个请求体：assistant(聚合 tool_calls) + tool 消息
	req2 := h.llm.requestBody(t, 1)
	var assistant *wireMessage
	var toolMsgs []wireMessage
	for i := range req2.Messages {
		m := req2.Messages[i]
		switch {
		case m.Role == "assistant" && len(m.ToolCalls) > 0:
			assistant = &req2.Messages[i]
		case m.Role == "tool":
			toolMsgs = append(toolMsgs, m)
		}
	}
	if assistant == nil {
		t.Fatal("二请求应包含带 tool_calls 的 assistant 消息")
	}
	if assistant.text() != "我先检索一下" {
		t.Fatalf("assistant.content = %q, 期望 %q", assistant.text(), "我先检索一下")
	}
	if len(assistant.ToolCalls) != 3 {
		t.Fatalf("tool_calls 数 = %d, 期望 3", len(assistant.ToolCalls))
	}
	tc0, tc1, tc2 := assistant.ToolCalls[0], assistant.ToolCalls[1], assistant.ToolCalls[2]
	if tc0.ID != "call_a" || tc0.Type != "function" ||
		tc0.Function.Name != "semantic_search" || tc0.Function.Arguments != `{"query":"测试"}` {
		t.Fatalf("tool_call[0] = %+v", tc0)
	}
	if tc1.ID != "call_1" || tc1.Function.Name != "keyword_search" || tc1.Function.Arguments != "{}" {
		t.Fatalf("tool_call[1] = %+v（缺 id 应兜底 call_1）", tc1)
	}
	if tc2.ID != "call_c" || tc2.Function.Name != "get_channel_stats" || tc2.Function.Arguments != `"oops"` {
		t.Fatalf("tool_call[2] = %+v", tc2)
	}

	// tool 消息：tool_call_id 对应、content 为执行器返回
	if len(toolMsgs) != 3 {
		t.Fatalf("tool 消息数 = %d, 期望 3", len(toolMsgs))
	}
	if toolMsgs[0].ToolCallID != "call_a" || toolMsgs[0].text() != `{"result":"ok"}` {
		t.Fatalf("tool 消息[0] = %+v", toolMsgs[0])
	}
	if toolMsgs[1].ToolCallID != "call_1" || toolMsgs[1].text() != `{"result":"ok"}` {
		t.Fatalf("tool 消息[1] = %+v", toolMsgs[1])
	}
}

// TestProcessQueryStreamHitMaxForcedFinal 10 轮全部以工具调用收尾 →
// 循环跑满（for...else）触发无工具强制终答（R7）。
func TestProcessQueryStreamHitMaxForcedFinal(t *testing.T) {
	const toolLine = `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_x","type":"function","function":{"name":"list_channels","arguments":"{}"}}]}}]}`
	entries := make([]scriptEntry, 0, AgentMaxIterations+1)
	for i := 0; i < AgentMaxIterations; i++ {
		entries = append(entries, scriptEntry{lines: []string{toolLine}})
	}
	entries = append(entries, scriptEntry{lines: []string{contentLine("强制终答")}})
	h := newHarness(t, entries)

	ch := h.engine.ProcessQueryStream(context.Background(), "复杂问题", 9, "")
	chunks := collectChunks(t, ch)
	assertChunks(t, chunks, []Chunk{
		CtrlChunk(contracts.CtrlNewSession),
		TextChunk("强制终答"),
		CtrlChunk(contracts.CtrlDone),
	})

	// 共 11 次请求：10 轮 agentic + 1 次强制终答
	if got := h.llm.requestCount(); got != AgentMaxIterations+1 {
		t.Fatalf("LLM 请求数 = %d, 期望 %d", got, AgentMaxIterations+1)
	}

	// 强制终答请求：无 tools/tool_choice，末条消息为强制终答指令
	req := h.llm.requestBody(t, AgentMaxIterations)
	if req.Tools != nil || req.ToolChoice != "" {
		t.Fatalf("强制终答请求不应携带工具: tools=%s choice=%q", string(req.Tools), req.ToolChoice)
	}
	last := req.Messages[len(req.Messages)-1]
	if last.Role != "user" || last.text() != "请基于已收集的信息直接给出最终回答。" {
		t.Fatalf("强制终答指令消息 = %+v", last)
	}
	// 第 10 轮的 assistant(tool_calls) + tool 消息已入列
	found := false
	for _, m := range req.Messages {
		if m.Role == "tool" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("强制终答请求应携带历史 tool 消息")
	}
}
