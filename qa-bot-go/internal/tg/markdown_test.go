// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package tg

import "testing"

// TestFixMarkdown 表驱动覆盖 _fix_markdown 等价实现：
// 中英 emoji 混排、奇偶补齐、先 ** 后 * 的次序、逐行独立处理。
func TestFixMarkdown(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"无标记原文不动", "你好，世界 hello world 🎉", "你好，世界 hello world 🎉"},
		{"粗体成对不动", "a **b** c", "a **b** c"},
		{"斜体成对不动", "a *b* c", "a *b* c"},
		{"代码成对不动", "run `go test` now", "run `go test` now"},
		{"混合成对不动", "**a** 和 *b* 与 `c` ✅", "**a** 和 *b* 与 `c` ✅"},
		{"奇数粗体补齐", "a **b", "a **b**"},
		{"奇数斜体补齐", "a *b", "a *b*"},
		{"奇数反引号补齐", "`code", "`code`"},
		{"粗体斜体次序", "x**y*z", "x**y*z***"},
		{"五连星", "*****", "******"},
		{"中文与emoji混排奇数粗体", "🎉 **你好 世界", "🎉 **你好 世界**"},
		{"emoji斜体", "🚀 *加速", "🚀 *加速*"},
		{"多行独立修复", "a **b\n`c\n*d*", "a **b**\n`c`\n*d*"},
		{"斜体与反引号同行", "*a`", "*a`*`"},
		{"四个星成对", "**", "****"},
		{"空串", "", ""},
		{"空行保留", "a\n\nb", "a\n\nb"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := FixMarkdown(tc.in); got != tc.want {
				t.Fatalf("FixMarkdown(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestFixMarkdownRuneSafety 验证多字节字符不被按字节破坏（R5）。
func TestFixMarkdownRuneSafety(t *testing.T) {
	in := "中文**粗体*斜体🎉"
	got := FixMarkdown(in)
	// ** 1 对补 → ** 尾；去 ** 后单个 * 1 个补 → * 尾
	if got != "中文**粗体*斜体🎉***" {
		t.Fatalf("FixMarkdown rune safety: got %q", got)
	}
	if n := len([]rune(got)); n != len([]rune(in))+3 {
		t.Fatalf("新增 rune 数应为 3，实际 got=%d in=%d", len([]rune(got)), len([]rune(in)))
	}
}
