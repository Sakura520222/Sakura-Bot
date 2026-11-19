// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package opencc

import "testing"

func TestT2SConversionAndPhrases(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"hello world 123", "hello world 123"},
		{"主頁看我頭像", "主页看我头像"},
		{"一目瞭然", "一目了然"},
		{"乾隆皇帝與皇后", "乾隆皇帝与皇后"},
		{"乾乾淨淨", "干干净净"},
		{"頭髮與發財", "头发与发财"},
		{"後台管理系統", "后台管理系统"},
	}
	for _, tc := range cases {
		if got := T2S(tc.in); got != tc.want {
			t.Errorf("T2S(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	if !HasTraditional("主頁") {
		t.Fatal("expected HasTraditional('主頁') == true")
	}
	if HasTraditional("主页") {
		t.Fatal("expected HasTraditional('主页') == false")
	}
}
