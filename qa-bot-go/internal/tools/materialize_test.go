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
	"testing"

	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/llm"
)

func intPtr(i int) *int        { return &i }
func strPtr(s string) *string  { return &s }
func argsPtr(s string) *string { return &s }

// TestMaterializeToolCalls 表驱动覆盖增量 tool_call 聚合与物化
// （qa_engine_v3.py:353-360 / 460-484，R7）：
// 乱序分片、缺 id 兜底 call_{index}、arguments 字符串追加。
func TestMaterializeToolCalls(t *testing.T) {
	t.Run("乱序分片_参数追加", func(t *testing.T) {
		acc := map[int]*llm.ToolCallSlot{}
		// 分片乱序到达：index 1 先于 index 0；arguments 分多次追加
		llm.AccumulateToolCall(acc, llm.ToolCallDelta{
			Index: intPtr(1), ID: strPtr("call_b"),
			Function: &llm.FunctionDelta{Name: strPtr("keyword_search"), Arguments: argsPtr(`{"key`)},
		})
		llm.AccumulateToolCall(acc, llm.ToolCallDelta{
			Index: intPtr(0), ID: strPtr("call_a"),
			Function: &llm.FunctionDelta{Name: strPtr("semantic_search"), Arguments: argsPtr(`{"query":"致`)},
		})
		llm.AccumulateToolCall(acc, llm.ToolCallDelta{
			Index:    intPtr(0),
			Function: &llm.FunctionDelta{Arguments: argsPtr(`力"}`)},
		})
		llm.AccumulateToolCall(acc, llm.ToolCallDelta{
			Index:    intPtr(1),
			Function: &llm.FunctionDelta{Arguments: argsPtr(`words":["go"]}`)},
		})

		calls := MaterializeToolCalls(acc)
		if len(calls) != 2 {
			t.Fatalf("物化数量 = %d, 期望 2", len(calls))
		}
		if calls[0].ID != "call_a" || calls[0].Function.Name != "semantic_search" ||
			calls[0].Function.Arguments != `{"query":"致力"}` {
			t.Errorf("calls[0] = %+v", calls[0])
		}
		if calls[1].ID != "call_b" || calls[1].Function.Name != "keyword_search" ||
			calls[1].Function.Arguments != `{"keywords":["go"]}` {
			t.Errorf("calls[1] = %+v", calls[1])
		}
		if calls[0].Type != "function" {
			t.Errorf("type = %q, 期望 function", calls[0].Type)
		}
	})

	t.Run("缺id兜底", func(t *testing.T) {
		acc := map[int]*llm.ToolCallSlot{}
		// 首片缺 id（部分兼容端点行为）：物化时以 call_{index} 兜底
		llm.AccumulateToolCall(acc, llm.ToolCallDelta{
			Index:    intPtr(0),
			Function: &llm.FunctionDelta{Name: strPtr("list_channels"), Arguments: argsPtr("{}")},
		})
		llm.AccumulateToolCall(acc, llm.ToolCallDelta{
			Index:    intPtr(3),
			Function: &llm.FunctionDelta{Name: strPtr("resolve_channel"), Arguments: argsPtr(`{"channel_hint":"@foo"}`)},
		})

		calls := MaterializeToolCalls(acc)
		if len(calls) != 2 {
			t.Fatalf("物化数量 = %d, 期望 2", len(calls))
		}
		if calls[0].ID != "call_0" {
			t.Errorf("calls[0].ID = %q, 期望 call_0", calls[0].ID)
		}
		if calls[1].ID != "call_3" {
			t.Errorf("calls[1].ID = %q, 期望 call_3", calls[1].ID)
		}
	})

	t.Run("非空覆盖与空槽位", func(t *testing.T) {
		acc := map[int]*llm.ToolCallSlot{}
		// 后片 id 非空覆盖前片（对齐 Python slot["id"] = tc.id if tc.id else slot.get("id")）
		llm.AccumulateToolCall(acc, llm.ToolCallDelta{
			Index: intPtr(0), ID: strPtr("call_x"),
			Function: &llm.FunctionDelta{Name: strPtr("semantic_search")},
		})
		llm.AccumulateToolCall(acc, llm.ToolCallDelta{
			Index: intPtr(0), ID: strPtr("call_final"),
		})
		calls := MaterializeToolCalls(acc)
		if calls[0].ID != "call_final" {
			t.Errorf("ID = %q, 期望 call_final（非空覆盖）", calls[0].ID)
		}

		if got := MaterializeToolCalls(map[int]*llm.ToolCallSlot{}); got == nil || len(got) != 0 {
			t.Errorf("空聚合应得非 nil 空切片, got %#v", got)
		}
	})
}
