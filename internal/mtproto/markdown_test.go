// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package mtproto

import (
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/gotd/td/tg"
)

func sliceUTF16(s string, offset, length int) string {
	encoded := utf16.Encode([]rune(s))
	if offset < 0 || offset+length > len(encoded) {
		return "<OUT_OF_BOUNDS>"
	}
	return string(utf16.Decode(encoded[offset : offset+length]))
}

func TestUTF16MarkdownAndHTMLEntities(t *testing.T) {
	// 包含 ASCII、中日韩汉字、辅助平面单码点 emoji 🌸（2 UTF-16 units）与组合盾牌 🛡️（U+1F6E1=2 + U+FE0F=1 -> 3 UTF-16 units）
	mdInput := "Hello 樱花 🌸 🛡️ **粗体🌸测试** *斜体🛡️* `inline_code` ~~删除线~~ [Sakura🌸链接](https://example.com/a_b)\n```go\nfmt.Println(\"🌸\")\n```"

	plain, tgEnts := ParseMarkdown(mdInput)
	spans := EntitiesFromTG(tgEnts)
	if len(spans) != 6 {
		t.Fatalf("expected 6 entities, got %d: %+v (plain=%q)", len(spans), spans, plain)
	}

	wantSubstrings := map[string]string{
		"bold":     "粗体🌸测试",
		"italic":   "斜体🛡️",
		"code":     "inline_code",
		"strike":   "删除线",
		"text_url": "Sakura🌸链接",
		"pre":      "fmt.Println(\"🌸\")\n",
	}

	for _, sp := range spans {
		gotSub := sliceUTF16(plain, sp.Offset, sp.Length)
		wantSub, ok := wantSubstrings[sp.Kind]
		if !ok {
			t.Fatalf("unexpected entity kind %q: %+v", sp.Kind, sp)
		}
		if gotSub != wantSub {
			t.Errorf("entity %q UTF-16 slice mismatch: got %q, want %q (offset=%d len=%d)", sp.Kind, gotSub, wantSub, sp.Offset, sp.Length)
		}
		if sp.Kind == "text_url" && sp.URL != "https://example.com/a_b" {
			t.Errorf("text_url URL mismatch: got %q", sp.URL)
		}
		if sp.Kind == "pre" && sp.Language != "go" {
			t.Errorf("pre Language mismatch: got %q, want \"go\"", sp.Language)
		}
	}

	// 验证 HTML 解析（含转义实体、多平面 Emoji 及 <pre><code class="language-python">）
	htmlInput := `前缀🌸🛡️ &amp; <b>加粗🌸</b> <i>斜体</i> <u>下划线🛡️</u> <s>删除</s> <code>x &lt; y</code> <a href="https://t.me/sakura">频道🛡️</a> <pre><code class="language-python">print("🌸")</code></pre>`
	hPlain, hEnts := ParseHTML(htmlInput)
	hSpans := EntitiesFromTG(hEnts)
	if len(hSpans) != 7 {
		t.Fatalf("expected 7 HTML entities, got %d: %+v (plain=%q)", len(hSpans), hSpans, hPlain)
	}

	wantHTML := map[string]string{
		"bold":      "加粗🌸",
		"italic":    "斜体",
		"underline": "下划线🛡️",
		"strike":    "删除",
		"code":      "x < y",
		"text_url":  "频道🛡️",
		"pre":       `print("🌸")`,
	}
	for _, sp := range hSpans {
		gotSub := sliceUTF16(hPlain, sp.Offset, sp.Length)
		if gotSub != wantHTML[sp.Kind] {
			t.Errorf("HTML entity %q slice mismatch: got %q, want %q", sp.Kind, gotSub, wantHTML[sp.Kind])
		}
		if sp.Kind == "pre" && sp.Language != "python" {
			t.Errorf("expected python pre language, got %q", sp.Language)
		}
	}

	// 验证 tg://user?id=xxx 转换为 InputMessageEntityMentionName 并可往返还原
	mentionPlain, mentionEnts := ParseHTML(`审核员 <a href="tg://user?id=987654">管理员🌸</a>`)
	if len(mentionEnts) != 1 {
		t.Fatalf("expected 1 mention entity, got %d", len(mentionEnts))
	}
	imn, ok := mentionEnts[0].(*tg.InputMessageEntityMentionName)
	if !ok {
		t.Fatalf("expected *tg.InputMessageEntityMentionName, got %T", mentionEnts[0])
	}
	if sliceUTF16(mentionPlain, imn.Offset, imn.Length) != "管理员🌸" {
		t.Fatalf("mention slice mismatch: %q", sliceUTF16(mentionPlain, imn.Offset, imn.Length))
	}
	roundTrip := EntitiesFromTG(mentionEnts)
	if len(roundTrip) != 1 || roundTrip[0].UserID != 987654 {
		t.Fatalf("round-trip mention failed: %+v", roundTrip)
	}
}

func TestSplitMessageSmartAndSanitizeMarkdown(t *testing.T) {
	p1 := "第一段总结内容🌸：" + strings.Repeat("这是完整的段落文本，包含正常的中文标点符号。", 8)
	p2 := "**重要标题🌸**\n" + strings.Repeat("第二段包含[详情链接](https://t.me/test_chan/12)以及`inline_code`说明。", 6)
	longText := p1 + "\n\n" + p2

	parts := SplitMessageSmart(longText, 240, true)
	if len(parts) < 2 {
		t.Fatalf("expected long message to split into >=2 parts, got %d", len(parts))
	}
	for i, part := range parts {
		if len([]rune(part)) > 240 {
			t.Errorf("part %d rune length %d exceeds maxLen 240", i, len([]rune(part)))
		}
		if ok, reason := ValidateMessageEntities(part); !ok {
			t.Errorf("part %d failed entity validation: %s\npart=%q", i, reason, part)
		}
	}

	// 未闭合标记修复测试
	broken := "你好 **未闭合粗体 与 `未闭合代码 以及 foo_bar_baz"
	if ok, _ := ValidateMessageEntities(broken); ok {
		t.Fatalf("expected broken markdown to fail validation")
	}
	mild := SanitizeMarkdown(broken, false)
	if ok, reason := ValidateMessageEntities(mild); !ok {
		t.Fatalf("mild sanitized text should be valid, got error: %s (mild=%q)", reason, mild)
	}
	if !strings.Contains(mild, "foo_bar_baz") {
		t.Fatalf("snake_case identifier should be preserved, got %q", mild)
	}

	aggr := SanitizeMarkdown("**加粗** 与 [链接](https://example.com) 及 ```\ncode\n```", true)
	if strings.ContainsAny(aggr, "*`[]") {
		t.Fatalf("aggressive sanitize should strip all markdown syntax, got %q", aggr)
	}

	if _, err := SplitByLinesSmart("abc", 0); err == nil {
		t.Fatalf("expected error when maxLen <= 0")
	}
	lines, err := SplitByLinesSmart("短行\n"+strings.Repeat("樱花🌸", 10), 8)
	if err != nil {
		t.Fatalf("SplitByLinesSmart failed: %v", err)
	}
	for _, ln := range lines {
		if len([]rune(ln)) > 8 {
			t.Fatalf("line %q exceeds 8 runes", ln)
		}
	}
}
