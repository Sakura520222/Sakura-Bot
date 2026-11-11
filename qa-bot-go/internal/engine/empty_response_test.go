package engine

import (
	"context"
	"reflect"
	"testing"

	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/contracts"
)

func TestEmptyToolResultResponseRetriesWithoutReexecutingTools(t *testing.T) {
	const tool = `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_recent","type":"function","function":{"name":"get_recent_summaries","arguments":"{}"}}]}}]}`
	const empty = `data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`
	h := newHarness(t, []scriptEntry{{lines: []string{tool}}, {lines: []string{empty}}, {lines: []string{contentLine("根据检索到的总结回答")}}})
	ctx := WithTurn(context.Background(), emptyCommentInbox{})
	chunks := collectChunks(t, h.engine.ProcessQueryStream(ctx, "帮我看看流萤的最新消息", 7, ""))
	for _, chunk := range chunks {
		if chunk.Kind == ChunkErr {
			t.Fatal(chunk)
		}
	}
	if !reflect.DeepEqual(h.llm.requestBody(t, 1), h.llm.requestBody(t, 2)) {
		t.Fatal("empty retry rebuilt or modified the tool context")
	}
	retry := h.llm.requestBody(t, 2)
	if len(retry.Messages) != 4 || retry.Messages[2].ToolCalls[0].ID != "call_recent" || retry.Messages[3].ToolCallID != "call_recent" {
		t.Fatal("completed tool results missing", retry)
	}
	if len(h.tools.exec.recorded()) != 1 {
		t.Fatal("retry re-executed a completed tool")
	}
	if chunks[len(chunks)-1].Ctrl != contracts.CtrlDone {
		t.Fatal("successful retry did not complete")
	}
}

func TestPersistentEmptyResponseFailsWithoutSuccessHistory(t *testing.T) {
	h := newHarness(t, []scriptEntry{{lines: nil}, {lines: nil}})
	chunks := collectChunks(t, h.engine.ProcessQueryStream(WithTurn(context.Background(), emptyCommentInbox{}), "问题", 7, ""))
	assertChunks(t, chunks, []Chunk{CtrlChunk(contracts.CtrlNewSession), ErrChunk(emptyAnswerText)})
	if h.llm.requestCount() != 2 {
		t.Fatal("retry must be bounded")
	}
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	for _, item := range h.store.saved {
		if item.Role == "assistant" {
			t.Fatal("failure persisted as assistant answer")
		}
	}
}

func TestEmptyFollowupFailsEvenAfterEarlierAnswer(t *testing.T) {
	h := newHarness(t, []scriptEntry{{lines: []string{contentLine("第一条回答")}}, {lines: nil}, {lines: nil}})
	chunks := collectChunks(t, h.engine.ProcessQueryStream(WithTurn(context.Background(), &stagedInbox{}), "第一条问题", 7, ""))
	if chunks[len(chunks)-1].Kind != ChunkErr {
		t.Fatal("empty followup marked successful", chunks)
	}
	for _, chunk := range chunks {
		if chunk.Ctrl == contracts.CtrlDone {
			t.Fatal("failed turn emitted DONE")
		}
	}
	if !reflect.DeepEqual(h.llm.requestBody(t, 1), h.llm.requestBody(t, 2)) {
		t.Fatal("followup retry lost previous answer or input")
	}
}

func TestEmptyRetryTransportFailureDoesNotRebuildContext(t *testing.T) {
	const tool = `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_recent","type":"function","function":{"name":"get_recent_summaries","arguments":"{}"}}]}}]}`
	h := newHarness(t, []scriptEntry{{lines: []string{tool}}, {lines: nil}, {status: 500}})
	chunks := collectChunks(t, h.engine.ProcessQueryStream(context.Background(), "问题", 7, ""))
	if h.llm.requestCount() != 3 || chunks[len(chunks)-1].Kind != ChunkErr {
		t.Fatal("failed retry restarted the agent", chunks)
	}
	if len(h.tools.exec.recorded()) != 1 {
		t.Fatal("completed tool executed again")
	}
}
