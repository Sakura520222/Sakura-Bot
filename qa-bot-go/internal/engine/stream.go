// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

// 本文件是 qa_engine_v3.py 的 Agentic 流式循环（:286-435）与
// 历史消息转换（:262-280）、tool_call 增量聚合（:460-484，规格 R7）的移植。

package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/db"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/llm"
)

// agenticStream 原生流式 Agentic 循环（qa_engine_v3.py:286-402）。
//
// 每一轮都是一次流式请求，同时接收文本增量与 tool_call 增量：
//   - 模型不调用工具 → 内容已按流式输出，直接结束（无二次生成）；
//   - 模型调用工具 → 执行工具、追加 tool 消息后进入下一轮；
//   - 达到 AgentMaxIterations 轮仍以工具调用收尾 → 附加强制终答指令、
//     不带工具再生成一次（循环体无 break，Python for...else 恒执行，R7）。
//
// 文本增量经 send 即时下发并累加进 *fullAnswer；消费端停止（errConsumerGone）
// 或上游异常原样返回，由调用方决定降级。
func (e *Engine) agenticStream(
	ctx context.Context,
	send func(Chunk) bool,
	fullAnswer *string,
	query string,
	history []db.HistoryItem,
	channelHint string,
) error {
	// 每请求隔离的工具执行上下文（多用户并发互不污染）
	executor := e.tools.NewExecutor()

	systemPrompt := e.buildSystemPrompt(ctx, channelHint, true)

	// 真实角色序列：system + 历史 + 当前用户消息（原样传递）
	messages := make([]llm.Message, 0, 2*MaxHistoryMessages+2+AgentMaxIterations*2)
	messages = append(messages, llm.Message{Role: "system", Content: llm.Str(systemPrompt)})
	messages = append(messages, historyToMessages(history)...)
	messages = append(messages, llm.Message{Role: "user", Content: llm.Str(query)})

	schemas := e.tools.ToolSchemasJSON()

	// 循环体只有 return 出口、无 break：跑满即等价 Python for...else 的 else
	// 分支（R7），无需显式标志。
	for iteration := 0; iteration < AgentMaxIterations; iteration++ {
		e.log.Info(fmt.Sprintf("[agent] 迭代 %d/%d", iteration+1, AgentMaxIterations))

		stream, err := e.llm.StreamChat(ctx, llm.AgenticChatRequest(messages, schemas))
		if err != nil {
			return err
		}
		content, toolCalls, err := consumeAgentStream(stream, send, fullAnswer)
		if err != nil {
			return err
		}

		// 无工具调用 → 最终回答已在上面流式输出完毕
		if len(toolCalls) == 0 {
			e.log.Info(fmt.Sprintf("[agent] LLM 未调用工具（迭代 %d），回答完成", iteration+1))
			return nil
		}

		// 有工具调用：记录 assistant 消息（保留已输出内容与 tool_calls）
		calls := materializeToolCalls(toolCalls)
		messages = append(messages, llm.Message{
			Role:      "assistant",
			Content:   llm.Str(content),
			ToolCalls: calls,
		})

		// 执行所有工具调用并追加 tool 消息（按 index 升序）
		for _, call := range calls {
			toolArgs := map[string]any{}
			if call.Function.Arguments != "" {
				if err := json.Unmarshal([]byte(call.Function.Arguments), &toolArgs); err != nil {
					e.log.Warn(fmt.Sprintf("[agent] 工具参数解析失败: %s", call.Function.Arguments))
					toolArgs = map[string]any{}
				}
			}
			if toolArgs == nil {
				toolArgs = map[string]any{} // "null" 字面量防御，保持 dict 语义
			}

			e.log.Info(fmt.Sprintf("[agent] 调用工具: %s", call.Function.Name))
			resultStr, err := executor.Execute(ctx, call.Function.Name, toolArgs)
			if err != nil {
				// Python execute 内部捕获一切异常；error 到达此处视为
				// 工具链路异常，走 agentic 异常 → 降级路径
				return err
			}
			messages = append(messages, llm.Message{
				Role:       "tool",
				ToolCallID: call.ID,
				Content:    llm.Str(resultStr),
			})
		}
	}

	// 达到最大迭代：附加提示后强制生成最终回答（不带工具，qa_engine_v3.py:381-402）
	e.log.Warn(fmt.Sprintf("[agent] 达到最大迭代 %d，强制生成回答", AgentMaxIterations))
	messages = append(messages, llm.Message{Role: "user", Content: llm.Str(forcedFinalPrompt)})

	stream, err := e.llm.StreamChat(ctx, llm.PlainChatRequest(messages))
	if err != nil {
		return err
	}
	// 该路径只消费文本增量（模型若再给 tool_call 亦被忽略，对齐 Python）
	if _, _, err := consumeAgentStream(stream, send, fullAnswer); err != nil {
		return err
	}
	return nil
}

// plainChatStream 无工具降级：直连对话流式生成（qa_engine_v3.py:404-435）。
// 注意系统提示词不带 /ask 频道约束、不附加工具说明（include_tools=False）。
func (e *Engine) plainChatStream(
	ctx context.Context,
	send func(Chunk) bool,
	fullAnswer *string,
	query string,
	history []db.HistoryItem,
) error {
	systemPrompt := e.buildSystemPrompt(ctx, "", false)
	messages := make([]llm.Message, 0, 2*MaxHistoryMessages+2)
	messages = append(messages, llm.Message{Role: "system", Content: llm.Str(systemPrompt)})
	messages = append(messages, historyToMessages(history)...)
	messages = append(messages, llm.Message{Role: "user", Content: llm.Str(query)})

	e.log.Info("[fallback] 使用无工具直连对话降级")

	stream, err := e.llm.StreamChat(ctx, llm.PlainChatRequest(messages))
	if err != nil {
		return err
	}
	_, _, err = consumeAgentStream(stream, send, fullAnswer)
	return err
}

// consumeAgentStream 消费一次流式响应直至 [DONE]（_iter_stream_chunks +
// _get_stream_delta 的合并形态）。
//
//   - content：本轮流中全部文本增量（用于回填 assistant 消息）；
//   - toolCalls：按 index 聚合的 tool_call 增量槽位；
//   - 每个文本增量经 send 下发并累加进 *fullAnswer（文本一到达即向外流式输出）。
//
// io.EOF（[DONE]）返回 nil 错误；其余错误原样返回。stream 总是被关闭。
func consumeAgentStream(
	stream *llm.Stream,
	send func(Chunk) bool,
	fullAnswer *string,
) (content string, toolCalls map[int]*llm.ToolCallSlot, err error) {
	defer stream.Close()

	var contentParts strings.Builder
	toolCalls = make(map[int]*llm.ToolCallSlot)

	for {
		delta, nextErr := stream.Next()
		if errors.Is(nextErr, io.EOF) {
			return contentParts.String(), toolCalls, nil
		}
		if nextErr != nil {
			return contentParts.String(), toolCalls, nextErr
		}

		if delta.Content != "" {
			if !send(TextChunk(delta.Content)) {
				return contentParts.String(), toolCalls, errConsumerGone
			}
			contentParts.WriteString(delta.Content)
			*fullAnswer += delta.Content
		}

		for _, tc := range delta.ToolCalls {
			llm.AccumulateToolCall(toolCalls, tc)
		}
	}
}

// historyToMessages 把数据库中的对话历史转换为真实的 user/assistant 消息序列
// （qa_engine_v3.py:262-280）：
//
//   - 只保留 user/assistant 角色，跳过异常数据与空白内容；
//   - 单条内容超长时按 rune 截断（Python len 语义，硬规则 3）并追加省略号；
//   - 只保留最近 MaxHistoryMessages 条。
func historyToMessages(history []db.HistoryItem) []llm.Message {
	messages := make([]llm.Message, 0, len(history))
	for _, item := range history {
		role := item.Role
		content := item.Content
		if (role != "user" && role != "assistant") || strings.TrimSpace(content) == "" {
			continue
		}
		if utf8.RuneCountInString(content) > HistoryMessageMaxChars {
			content = string([]rune(content)[:HistoryMessageMaxChars]) + "…"
		}
		messages = append(messages, llm.Message{Role: role, Content: llm.Str(content)})
	}

	if len(messages) > MaxHistoryMessages {
		messages = messages[len(messages)-MaxHistoryMessages:]
	}
	return messages
}

// materializeToolCalls 把聚合槽位物化为回传给模型的 tool_calls 数组
// （qa_engine_v3.py:352-360）：按 index 升序；缺 id 时以 call_{index} 兜底；
// type 恒为 "function"。
func materializeToolCalls(acc map[int]*llm.ToolCallSlot) []llm.ToolCall {
	indexes := make([]int, 0, len(acc))
	for idx := range acc {
		indexes = append(indexes, idx)
	}
	sort.Ints(indexes)

	calls := make([]llm.ToolCall, 0, len(acc))
	for _, idx := range indexes {
		slot := acc[idx]
		if slot == nil {
			continue
		}
		id := slot.ID
		if id == "" {
			id = fmt.Sprintf("call_%d", idx)
		}
		calls = append(calls, llm.ToolCall{
			ID:   id,
			Type: "function",
			Function: llm.FunctionCall{
				Name:      slot.Name,
				Arguments: slot.Arguments,
			},
		})
	}
	return calls
}
