// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package engine

import (
	"os"
	"strings"
	"testing"
)

// TestPromptTemplatesVerbatimAgainstPython 逐字节校验 Go 内嵌提示词模板与
// qa_engine_v3.py 的 BASE_SYSTEM_TEMPLATE / AGENT_TOOL_INSTRUCTIONS 一致。
// Python 源文件不可用（如独立导出构建）时跳过。
func TestPromptTemplatesVerbatimAgainstPython(t *testing.T) {
	const pyPath = "../../../core/ai/qa_engine_v3.py"
	src, err := os.ReadFile(pyPath)
	if err != nil {
		t.Skipf("Python 源文件不可用，跳过逐字节校验: %v", err)
	}
	s := string(src)

	// extract 从 Python 源码中截取 NAME = """...""" 的模板正文。
	extract := func(name string) string {
		marker := name + ` = """`
		i := strings.Index(s, marker)
		if i < 0 {
			t.Fatalf("未找到 %s 的起始标记", name)
		}
		rest := s[i+len(marker):]
		j := strings.Index(rest, `"""`)
		if j < 0 {
			t.Fatalf("未找到 %s 的收尾三引号", name)
		}
		return rest[:j]
	}

	wantBase := extract("BASE_SYSTEM_TEMPLATE")
	wantTools := extract("AGENT_TOOL_INSTRUCTIONS")

	if baseSystemTemplate != wantBase {
		t.Fatalf("BASE_SYSTEM_TEMPLATE 与 Python 不一致：\nGo:\n%q\nPython:\n%q",
			baseSystemTemplate, wantBase)
	}

	if agentToolInstructions != wantTools {
		t.Fatalf("AGENT_TOOL_INSTRUCTIONS 与 Python 不一致：\nGo:\n%q\nPython:\n%q",
			agentToolInstructions, wantTools)
	}
}
