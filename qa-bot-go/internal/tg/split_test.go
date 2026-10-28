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

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestSplitLongMessage 表驱动覆盖 _split_long_message 等价实现的分片边界。
func TestSplitLongMessage(t *testing.T) {
	cases := []struct {
		name      string
		text      string
		maxLength int
		want      []string
	}{
		{"短消息原样返回", "hello 🎉", 10, []string{"hello 🎉"}},
		{"恰好等于限长", "aaaa\n\nbbbb", 10, []string{"aaaa\n\nbbbb"}},
		{"两段贪心合并", "aaaa\n\nbbbb\n\ncccc", 10, []string{"aaaa", "bbbb", "cccc"}},
		{"两段可合并时分片", "aaaa\n\nbbbb", 11, []string{"aaaa\n\nbbbb"}},
		{"累计含分隔符计入长度", "aaa\n\nbbbbbb\n\nccc", 11, []string{"aaa", "bbbbbb", "ccc"}},
		{"单个超长段不二次切分", strings.Repeat("字", 20), 10, []string{strings.Repeat("字", 20)}},
		{"尾部分隔符剥离", "aaaa\n\nbbbb\n\n", 10, []string{"aaaa", "bbbb"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SplitLongMessage(tc.text, tc.maxLength)
			if len(got) != len(tc.want) {
				t.Fatalf("分片数 = %d (%q), want %d", len(got), got, len(tc.want))
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("分片 %d = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// TestSplitLongMessageRuneCounting 验证按 rune 计数（R2）：中文字符按 1 计。
func TestSplitLongMessageRuneCounting(t *testing.T) {
	para := strings.Repeat("字", 40) // 40 rune，120 字节
	text := para + "\n\n" + para
	parts := SplitLongMessage(text, 45)
	if len(parts) != 2 {
		t.Fatalf("rune 计数下应分为 2 片，实际 %d 片", len(parts))
	}
	for i, p := range parts {
		if utf8.RuneCountInString(p) > 45 {
			t.Fatalf("分片 %d 超 rune 限长: %d", i, utf8.RuneCountInString(p))
		}
	}
}

// TestTruncateRunes 验证 Python text[:n] 等价的 rune 截断（4066+续尾的截断部分）。
func TestTruncateRunes(t *testing.T) {
	cases := []struct {
		name string
		in   string
		n    int
		want string
	}{
		{"不超长原样", "abc🎉de", 10, "abc🎉de"},
		{"恰好截断", "abc🎉de", 4, "abc🎉"},
		{"emoji边界不劈半", "🎉🎉🎉", 2, "🎉🎉"},
		{"截为0", "abc", 0, ""},
		{"负数", "abc", -1, ""},
		{"超长不误伤", "abc", 99, "abc"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := TruncateRunes(tc.in, tc.n); got != tc.want {
				t.Fatalf("TruncateRunes(%q, %d) = %q, want %q", tc.in, tc.n, got, tc.want)
			}
		})
	}
}
