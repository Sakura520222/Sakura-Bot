// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package i18n

import "testing"

// setLang 临时切换语言并在测试结束后恢复，避免用例间串扰。
func setLang(t *testing.T, lang string) {
	t.Helper()
	old := GetLanguage()
	if !SetLanguage(lang) {
		t.Fatalf("切换语言失败: %s", lang)
	}
	t.Cleanup(func() { SetLanguage(old) })
}

func TestSetLanguageValidation(t *testing.T) {
	old := GetLanguage()
	defer SetLanguage(old)

	if GetLanguage() != LangZhCN {
		t.Fatalf("默认语言应为 zh-CN，实际 %s", GetLanguage())
	}
	if SetLanguage("fr-FR") {
		t.Fatal("不支持的语言应返回 false")
	}
	if GetLanguage() != LangZhCN {
		t.Fatal("设置失败后语言应保持不变")
	}
	if !SetLanguage(LangEnUS) {
		t.Fatal("en-US 应设置成功")
	}
	if GetLanguage() != LangEnUS {
		t.Fatalf("当前语言应为 en-US，实际 %s", GetLanguage())
	}
}

func TestTZhDefault(t *testing.T) {
	setLang(t, LangZhCN)
	// appeal.menu 来自 core/i18n/appeals.py 的 APPEAL_ZH_CN 解包。
	if got := T("appeal.menu", nil); got != "🚫 封禁申诉" {
		t.Fatalf("zh 文案不符: %q", got)
	}
	if got := T("appeal.confirm", nil); got != "✅ 确认提交" {
		t.Fatalf("zh 文案不符: %q", got)
	}
}

func TestTEn(t *testing.T) {
	setLang(t, LangEnUS)
	if got := T("appeal.menu", nil); got != "🚫 Ban appeal" {
		t.Fatalf("en 文案不符: %q", got)
	}
}

// TestTFallbackZhForMissingEn：en 缺 key 时回退 zh（generated 数据中
// database.migrate.warning_keep_backup 仅存在于 zh 字典）。
func TestTFallbackZhForMissingEn(t *testing.T) {
	setLang(t, LangEnUS)
	zhValue, ok := MessagesZhCN["database.migrate.warning_keep_backup"]
	if !ok {
		t.Fatal("测试前置条件不成立：zh 字典缺少该 key")
	}
	if _, ok := MessagesEnUS["database.migrate.warning_keep_backup"]; ok {
		t.Fatal("测试前置条件不成立：en 字典不应含有该 key")
	}
	if got := T("database.migrate.warning_keep_backup", nil); got != zhValue {
		t.Fatalf("en 缺 key 应回退 zh，期望 %q 实际 %q", zhValue, got)
	}
}

// TestTUnknownKeyReturnsKey：所有语言都缺 key 时返回 key 本身。
func TestTUnknownKeyReturnsKey(t *testing.T) {
	setLang(t, LangZhCN)
	for _, lang := range []string{LangZhCN, LangEnUS} {
		setLang(t, lang)
		if got := T("no.such.key_here", nil); got != "no.such.key_here" {
			t.Fatalf("缺 key 应回返回 key 本身，实际 %q", got)
		}
	}
}

// TestTInterpolation：{name} 插值、缺失 token 返回原模板（R14）、多余 kwargs 忽略。
func TestTInterpolation(t *testing.T) {
	setLang(t, LangZhCN)

	cases := []struct {
		name   string
		key    string
		kwargs map[string]string
		want   string
	}{
		{
			name:   "单 token 替换",
			key:    "appeal.blocked",
			kwargs: map[string]string{"error": "超时"},
			want:   "处理提示：超时",
		},
		{
			name:   "缺失 token 返回原模板",
			key:    "appeal.blocked",
			kwargs: map[string]string{"other": "x"},
			want:   "处理提示：{error}",
		},
		{
			name:   "空 kwargs 返回原模板",
			key:    "appeal.blocked",
			kwargs: map[string]string{},
			want:   "处理提示：{error}",
		},
		{
			name:   "nil kwargs 返回原模板",
			key:    "appeal.blocked",
			kwargs: nil,
			want:   "处理提示：{error}",
		},
		{
			name:   "多 token 替换",
			key:    "appeal.last_review",
			kwargs: map[string]string{"action": "解封", "reason": "误封"},
			want:   "审核：解封\n意见：误封",
		},
		{
			name:   "多 token 缺一返回原模板",
			key:    "appeal.last_review",
			kwargs: map[string]string{"action": "解封"},
			want:   "审核：{action}\n意见：{reason}",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := T(tc.key, tc.kwargs); got != tc.want {
				t.Fatalf("期望 %q 实际 %q", tc.want, got)
			}
		})
	}

	setLang(t, LangEnUS)
	if got := T("appeal.blocked", map[string]string{"error": "timeout"}); got != "Processing note: timeout" {
		t.Fatalf("en 插值不符: %q", got)
	}
}

// TestFormatTemplateEscapeAndErrors：{{ }} 转义与非法模板返回原文。
func TestFormatTemplateEscapeAndErrors(t *testing.T) {
	kw := map[string]string{"name": "世界"}

	cases := []struct {
		name    string
		tmpl    string
		want    string
		wantOK  bool
		wantRaw bool // false 时断言返回原模板
	}{
		{name: "普通替换", tmpl: "你好 {name}！", want: "你好 世界！", wantOK: true},
		{name: "双花括号转义", tmpl: "{{name}} 字面量", want: "{name} 字面量", wantOK: true},
		{name: "混合转义与插值", tmpl: "{{{name}}}", want: "{世界}", wantOK: true},
		{name: "孤立右括号返回原文", tmpl: "a } b", wantRaw: true},
		{name: "未闭合左括号返回原文", tmpl: "a { b", wantRaw: true},
		{name: "空字段返回原文", tmpl: "a {} b", wantRaw: true},
		{name: "嵌套字段返回原文", tmpl: "a {name:{spec}} b", wantRaw: true},
		{name: "带格式说明符取 name", tmpl: "n={name:>10}", want: "n=世界", wantOK: true},
		{name: "带转换符取 name", tmpl: "n={name!r}", want: "n=世界", wantOK: true},
		{name: "缺失名字返回原文", tmpl: "n={missing}", wantRaw: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := formatTemplate(tc.tmpl, kw)
			if tc.wantRaw {
				if ok || got != tc.tmpl {
					t.Fatalf("非法模板应返回原模板 %q，实际 (%q, %v)", tc.tmpl, got, ok)
				}
				return
			}
			if !ok || got != tc.want {
				t.Fatalf("期望 (%q, true) 实际 (%q, %v)", tc.want, got, ok)
			}
		})
	}
}

// TestGeneratedDictsConsistency：生成数据的基本一致性防线。
func TestGeneratedDictsConsistency(t *testing.T) {
	if len(MessagesZhCN) == 0 || len(MessagesEnUS) == 0 {
		t.Fatal("生成的字典为空")
	}
	for _, key := range []string{"appeal.menu", "appeal.command", "appeal.reviewer_card"} {
		if _, ok := MessagesZhCN[key]; !ok {
			t.Fatalf("zh 字典缺少 appeal key: %s", key)
		}
		if _, ok := MessagesEnUS[key]; !ok {
			t.Fatalf("en 字典缺少 appeal key: %s", key)
		}
	}
}
