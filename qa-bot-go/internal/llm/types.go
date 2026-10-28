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

import "encoding/json"

// Message 对齐 OpenAI chat/completions 的消息结构（qa_engine_v3.py:307-309、352-380）。
//
// Content 使用指针以保留空串与 null 的区别：assistant 轮次显式发送 "content":""，
// system/user/tool 恒为非空字符串（用 Str 构造）。ToolCalls 仅 assistant 角色、
// ToolCallID 仅 tool 角色使用，omitempty 保证其余角色的 wire 形态与 Python dict 一致。
type Message struct {
	Role       string     `json:"role"`
	Content    *string    `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

// Str 返回内容字符串的指针，用于构造 Message.Content。
func Str(s string) *string { return &s }

// ToolCall 回填给模型的 assistant.tool_calls 元素
// （qa_engine_v3.py:353-360 聚合产物经此结构原样上行）。
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"` // 恒为 "function"
	Function FunctionCall `json:"function"`
}

// FunctionCall 完整聚合后的函数名与 JSON 字符串参数。
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ChatRequest 一次流式请求的入参（对齐 qa_engine_v3.py 的三处调用点：
// :316-323 agentic 带 tools 与 tool_choice="auto"；:388-393 达到最大迭代后的
// 强制终答与 :420-426 无工具直连均不带 tools/tool_choice；三处 temperature 均为 0.7）。
// stream 字段由 Client 恒置 true，不在此暴露。
type ChatRequest struct {
	Messages []Message `json:"messages"`
	// Tools 工具 schema 数组的原始 JSON（原样透传）；空值表示不携带。
	Tools json.RawMessage `json:"tools,omitempty"`
	// ToolChoice 仅 agentic 请求发送 "auto"；空字符串表示字段缺省。
	ToolChoice string `json:"tool_choice,omitempty"`
	// Temperature 对齐 Python 三处调用的 0.7。
	Temperature float64 `json:"temperature"`
}

// AgenticChatRequest 构造带工具的流式请求（qa_engine_v3.py:316-323 逐字段对齐）。
func AgenticChatRequest(messages []Message, tools json.RawMessage) ChatRequest {
	return ChatRequest{
		Messages:    messages,
		Tools:       tools,
		ToolChoice:  "auto",
		Temperature: 0.7,
	}
}

// PlainChatRequest 构造无工具的流式请求
// （qa_engine_v3.py:388-393 与 :420-426 两处等价调用点的逐字段对齐）。
func PlainChatRequest(messages []Message) ChatRequest {
	return ChatRequest{
		Messages:    messages,
		Temperature: 0.7,
	}
}

// Delta 是 choices[0].delta 的视图（qa_engine_v3.py:330-342 仅消费这两个字段）。
type Delta struct {
	// Content 文本增量；分片缺失该字段时为空串
	//（对应 Python getattr(delta, "content", None) 的 falsy 判定）。
	Content string
	// ToolCalls 本分片携带的 tool_call 增量；缺失时为 nil。
	ToolCalls []ToolCallDelta
}

// ToolCallDelta 流式 tool_call 增量。指针字段保留"字段缺失"与"空值"的区别，
// 按协议原样透传（id/name 首片完整、arguments 字符串分片、index 定位聚合槽）。
type ToolCallDelta struct {
	Index    *int           `json:"index,omitempty"`
	ID       *string        `json:"id,omitempty"`
	Type     *string        `json:"type,omitempty"`
	Function *FunctionDelta `json:"function,omitempty"`
}

// FunctionDelta tool_call 增量的 function 子对象。
type FunctionDelta struct {
	// Name 按协议在首个分片完整到达（聚合采用非空覆盖语义）。
	Name *string `json:"name,omitempty"`
	// Arguments 以字符串分片累加（聚合采用追加语义）。
	Arguments *string `json:"arguments,omitempty"`
}

// streamChunk 是单条 SSE data 载荷的解析载体，只需 choices[0].delta。
type streamChunk struct {
	Choices []struct {
		Delta deltaWire `json:"delta"`
	} `json:"choices"`
}

// deltaWire 与 Delta 分离：JSON 缺失 content 字段时 Unmarshal 得到零值空串，
// 正是消费端所需的 falsy 语义；缺失 tool_calls 时为 nil。
type deltaWire struct {
	Content   string          `json:"content"`
	ToolCalls []ToolCallDelta `json:"tool_calls"`
}

// ToolCallSlot 单个 tool_call 的聚合槽位（qa_engine_v3.py:327-328 的 dict 值结构）。
type ToolCallSlot struct {
	ID        string
	Name      string
	Arguments string
}

// AccumulateToolCall 按 index 把单个 tool_call 增量聚合进 acc
// （qa_engine_v3.py:460-484 逐字对齐，移植风险 R7）：
//   - index 缺失按 0 处理（Python `index = 0 if index is None else index`）；
//   - id / name 非空即覆盖（协议上首片完整给出，覆盖等价于"首片语义"）；
//   - arguments 非空即字符串追加；
//
// acc 由调用方持有，本函数原地修改。调用方按 index 升序物化消息；
// 缺 id 的 call_{index} 兜底发生在物化阶段（qa_engine_v3.py:355），不在本函数。
func AccumulateToolCall(acc map[int]*ToolCallSlot, tc ToolCallDelta) {
	index := 0
	if tc.Index != nil {
		index = *tc.Index
	}
	slot := acc[index]
	if slot == nil {
		slot = &ToolCallSlot{}
		acc[index] = slot
	}
	if tc.ID != nil && *tc.ID != "" {
		slot.ID = *tc.ID
	}
	if tc.Function != nil {
		if tc.Function.Name != nil && *tc.Function.Name != "" {
			slot.Name = *tc.Function.Name
		}
		if tc.Function.Arguments != nil && *tc.Function.Arguments != "" {
			slot.Arguments += *tc.Function.Arguments
		}
	}
}
