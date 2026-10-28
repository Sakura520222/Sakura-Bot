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
	"encoding/json"
	"strings"
	"testing"
)

// TestToolSchemasShape 校验内嵌 schema 与 agent_tools.py:30-277 的一致性：
// 9 个工具、名称与顺序、全部为 function 类型、参数为 object。
func TestToolSchemasShape(t *testing.T) {
	wantNames := []string{
		"semantic_search", "keyword_search", "list_channels", "resolve_channel",
		"get_recent_summaries", "get_channel_stats", "get_source_detail",
		"rerank_results", "get_channel_info",
	}
	if got := ToolNames(); strings.Join(got, ",") != strings.Join(wantNames, ",") {
		t.Fatalf("工具名/顺序漂移: got %v, want %v", got, wantNames)
	}
	for i := range ToolSchemas {
		s := &ToolSchemas[i]
		if s.Type != "function" {
			t.Errorf("%s: type = %q, 期望 function", s.Function.Name, s.Type)
		}
		if s.Function.Description == "" {
			t.Errorf("%s: description 为空", s.Function.Name)
		}
		if len(s.Function.Parameters) == 0 {
			t.Errorf("%s: parameters 缺失", s.Function.Name)
		} else {
			var params map[string]any
			if err := json.Unmarshal(s.Function.Parameters, &params); err != nil {
				t.Errorf("%s: parameters 非法 JSON: %v", s.Function.Name, err)
			} else if params["type"] != "object" {
				t.Errorf("%s: parameters.type = %v, 期望 object", s.Function.Name, params["type"])
			}
		}
	}
}

// TestToolSchemasRequiredField 抽查必填字段与默认值文本未漂移
// （与 Python 字面量逐字对齐的关键锚点）。
func TestToolSchemasRequiredField(t *testing.T) {
	var raw []map[string]any
	if err := json.Unmarshal(ToolSchemasJSON, &raw); err != nil {
		t.Fatalf("ToolSchemasJSON 非法: %v", err)
	}
	if len(raw) != 9 {
		t.Fatalf("schema 数量 = %d, 期望 9", len(raw))
	}
	byName := make(map[string]map[string]any, len(raw))
	for _, item := range raw {
		fn := item["function"].(map[string]any)
		byName[fn["name"].(string)] = fn
	}

	sem, ok := byName["semantic_search"]
	if !ok {
		t.Fatal("缺 semantic_search")
	}
	params := sem["parameters"].(map[string]any)
	if got := params["required"].([]any); len(got) != 1 || got[0] != "query" {
		t.Fatalf("semantic_search required = %v, 期望 [\"query\"]", got)
	}
	props := params["properties"].(map[string]any)
	topK := props["top_k"].(map[string]any)
	if topK["default"] != float64(20) {
		t.Fatalf("semantic_search top_k default = %v, 期望 20", topK["default"])
	}
	coll := props["collection"].(map[string]any)
	enum := coll["enum"].([]any)
	if len(enum) != 3 || enum[0] != "all" || enum[1] != "summaries" || enum[2] != "messages" {
		t.Fatalf("collection enum 漂移: %v", enum)
	}

	rr, ok := byName["rerank_results"]
	if !ok {
		t.Fatal("缺 rerank_results")
	}
	rp := rr["parameters"].(map[string]any)
	rreq := rp["required"].([]any)
	if len(rreq) != 2 || rreq[0] != "query" || rreq[1] != "result_ids" {
		t.Fatalf("rerank_results required = %v, 期望 [\"query\",\"result_ids\"]", rreq)
	}

	// 描述为字面 UTF-8（非 \uXXXX 转义），锚定 ensure_ascii=False 的导出形态
	if !strings.Contains(string(ToolSchemasJSON), "搜索与查询语义相关的频道历史总结和消息") {
		t.Fatal("semantic_search 描述文本漂移或被转义")
	}
}
