package engine

import (
	"context"
	"strings"
	"testing"
)

type stagedInbox struct {
	calls      int
	firstFinal bool
}

func (i *stagedInbox) Take(final bool) []TurnInput {
	i.calls++
	if i.calls == 1 {
		i.firstFinal = final
		return []TurnInput{{Query: "  小妲，继续追问"}}
	}
	return nil
}
func TestTurnInjectionPreservesToolBatch(t *testing.T) {
	const tool = `data: {"choices":[{"delta":{"content":"检索中","tool_calls":[{"index":0,"id":"call_x","type":"function","function":{"name":"list_channels","arguments":"{}"}}]}}]}`
	h := newHarness(t, []scriptEntry{{lines: []string{tool}}, {lines: []string{contentLine("结果")}}})
	inbox := &stagedInbox{}
	collectChunks(t, h.engine.ProcessQueryStream(WithTurn(context.Background(), inbox), "初始", 1, ""))
	req := h.llm.requestBody(t, 1)
	msgs := req.Messages
	if inbox.firstFinal {
		t.Fatal("injection should occur after tool results")
	}
	if len(msgs) != 5 || msgs[2].Role != "assistant" || msgs[2].text() != "检索中" || len(msgs[2].ToolCalls) != 1 || msgs[3].Role != "tool" || msgs[3].ToolCallID != "call_x" || msgs[4].text() != "  小妲，继续追问" {
		t.Fatalf("invalid preserved sequence: %+v", msgs)
	}
	if len(h.tools.exec.recorded()) != 1 {
		t.Fatal("tool re-executed")
	}
}
func TestTurnInjectionPreservesAnswer(t *testing.T) {
	h := newHarness(t, []scriptEntry{{lines: []string{contentLine("第一阶段")}}, {lines: []string{contentLine("第二阶段")}}})
	inbox := &stagedInbox{}
	chunks := collectChunks(t, h.engine.ProcessQueryStream(WithTurn(context.Background(), inbox), "初始", 2, ""))
	req := h.llm.requestBody(t, 1)
	if len(req.Messages) != 4 || req.Messages[2].Role != "assistant" || req.Messages[2].text() != "第一阶段" {
		t.Fatalf("answer lost: %+v", req)
	}
	var out string
	for _, c := range chunks {
		out += c.Text
	}
	if !strings.Contains(out, "第一阶段\n\n第二阶段") {
		t.Fatal(out)
	}
}
func TestScopeIsolationAndScopedClear(t *testing.T) {
	c := NewConversations(&fakeStore{})
	a, _ := c.GetOrCreateScopedSession(1, "post:a")
	b, _ := c.GetOrCreateScopedSession(1, "post:b")
	if a == b {
		t.Fatal("scope collision")
	}
	c.ClearUserHistory(WithScope(context.Background(), "post:a"), 1, "")
	still, _ := c.GetOrCreateScopedSession(1, "post:b")
	if still != b {
		t.Fatal("other post cleared")
	}
	if c.SaveMessage(WithScope(context.Background(), "post:a"), 1, a, "assistant", "late", nil) {
		t.Fatal("old write resurrected")
	}
}
func TestTurnPanicBecomesObservableFailure(t *testing.T) {
	const tool = `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_x","type":"function","function":{"name":"list_channels","arguments":"{}"}}]}}]}`
	h := newHarness(t, []scriptEntry{{lines: []string{tool}}})
	h.tools.exec = nil
	chunks := collectChunks(t, h.engine.ProcessQueryStream(WithTurn(context.Background(), &stagedInbox{}), "初始", 1, ""))
	for _, chunk := range chunks {
		if chunk.Kind == ChunkErr {
			return
		}
	}
	t.Fatal("panic did not produce refundable error")
}
