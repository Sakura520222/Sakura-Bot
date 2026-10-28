// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

// Package tools 移植 core/ai/agent_tools.py：9 个 LLM function calling 工具的
// schema 定义（TOOL_SCHEMAS）与每请求独立的 ToolExecutor。
//
// 行为对齐要点（行为规格 §2.4 / 评审红线）：
//   - 所有工具异常一律转为 {"error": ...} JSON 返回给模型，绝不上抛
//     （agent_tools.py:305-335）；
//   - 结果存储按请求内自增 result_id 索引（agent_tools.py:680-692），
//     每请求独立 ToolExecutor，多用户互不污染（qa_engine_v3.py:300）；
//   - 序列化时 summary_text 截断至 500 字符（Python 码点语义 = Go rune，
//     硬规则 3）；
//   - post_url 只对 source_type=message 且解析出真实 Telegram 消息 ID 的
//     结果构造；总结类结果一律 post_url=null、post_links=[]（R9 历史修复
//     红线）；keyword_search 的 summary_message_ids 是机器人发布的总结
//     报告消息 ID，不是频道原帖，绝不能用于拼链接（勿"顺手修好"）；
//   - round(x, 3) = math.Round(x*1000)/1000（README 硬规则 10）。
//
// 依赖（向量存储 / 频道记忆 / 重排序）经本包内定义的窄接口注入
// （interfaces.go），由 main 装配；本包不直接 import 兄弟业务实现。
package tools

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
)

// ToolSchema 是单个工具的 OpenAI function calling schema 视图。
type ToolSchema struct {
	Type string `json:"type"`
	// Function 内嵌 JSON 原样保留 description 与 parameters，
	// 不做结构化建模以免手写漂移。
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
	} `json:"function"`
}

// toolSchemasRaw 为 agent_tools.py:30-277 的 TOOL_SCHEMAS 字面量原样导出
// （提取脚本对 Python 源做 ast.literal_eval 后 json.dump(ensure_ascii=False)，
// 键序与文本逐字保留；数据文件为纯 JSON 无法内嵌注释，出处记录于此）。
//
//go:embed tool_schemas.json
var toolSchemasRaw []byte

// ToolSchemasJSON 是透传给 LLM 请求体 tools 字段的原始 JSON
// （main 装配时直接填入 llm.ChatRequest.Tools，与 Python 传 dict 等价）。
var ToolSchemasJSON = json.RawMessage(toolSchemasRaw)

// ToolSchemas 是解析后的 schema 数组；包初始化时校验，损坏即 panic
// （内嵌数据为编译期常量，损坏属于构建事故，应尽早失败）。
var ToolSchemas = mustParseSchemas(toolSchemasRaw)

// mustParseSchemas 解析并校验内嵌 schema：非空数组、全部为 function 类型、
// 名称唯一且非空。
func mustParseSchemas(raw []byte) []ToolSchema {
	dec := json.NewDecoder(bytes.NewReader(raw))
	var out []ToolSchema
	if err := dec.Decode(&out); err != nil {
		panic(fmt.Sprintf("tools: 内嵌 tool_schemas.json 解析失败: %v", err))
	}
	if len(out) == 0 {
		panic("tools: 内嵌 tool_schemas.json 为空数组")
	}
	seen := make(map[string]bool, len(out))
	for i := range out {
		s := &out[i]
		if s.Type != "function" || s.Function.Name == "" {
			panic(fmt.Sprintf("tools: 第 %d 个 schema 非法: type=%q name=%q", i+1, s.Type, s.Function.Name))
		}
		if seen[s.Function.Name] {
			panic(fmt.Sprintf("tools: 工具名重复: %s", s.Function.Name))
		}
		seen[s.Function.Name] = true
	}
	return out
}

// ToolNames 返回 schema 顺序中的全部工具名（测试与诊断用）。
func ToolNames() []string {
	names := make([]string, len(ToolSchemas))
	for i := range ToolSchemas {
		names[i] = ToolSchemas[i].Function.Name
	}
	return names
}
