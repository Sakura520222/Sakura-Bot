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
	"sort"
	"strconv"

	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/llm"
)

// MaterializeToolCalls 把一轮流式响应累积出的 tool_call 槽位物化为可回填
// 给模型的 assistant.tool_calls 数组（qa_engine_v3.py:353-360 逐字对齐）：
//
//   - 按 index 升序排列（Python sorted(tool_calls_acc.items())）；
//   - id 缺失时以 "call_{index}" 兜底（`slot["id"] or f"call_{index}"`，
//     移植风险 R7）；
//   - type 恒为 "function"；
//   - name / arguments 原样透传（arguments 为未解析的 JSON 字符串）。
//
// 增量聚合使用 internal/llm.AccumulateToolCall（id/name 非空覆盖、
// arguments 字符串追加）；空槽位返回空切片（非 nil）。
func MaterializeToolCalls(acc map[int]*llm.ToolCallSlot) []llm.ToolCall {
	indexes := make([]int, 0, len(acc))
	for idx := range acc {
		indexes = append(indexes, idx)
	}
	sort.Ints(indexes)

	out := make([]llm.ToolCall, 0, len(indexes))
	for _, idx := range indexes {
		slot := acc[idx]
		if slot == nil {
			continue
		}
		id := slot.ID
		if id == "" {
			id = "call_" + strconv.Itoa(idx)
		}
		out = append(out, llm.ToolCall{
			ID:   id,
			Type: "function",
			Function: llm.FunctionCall{
				Name:      slot.Name,
				Arguments: slot.Arguments,
			},
		})
	}
	return out
}
