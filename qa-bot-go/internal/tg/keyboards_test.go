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
	"testing"

	"github.com/go-telegram/bot/models"
)

// rowTexts 把键盘行转为纯文本行，便于断言。
func rowTexts(kb *models.ReplyKeyboardMarkup) [][]string {
	rows := make([][]string, 0, len(kb.Keyboard))
	for _, row := range kb.Keyboard {
		texts := make([]string, 0, len(row))
		for _, btn := range row {
			texts = append(texts, btn.Text)
		}
		rows = append(rows, texts)
	}
	return rows
}

// TestBuildQAMainMenuKeyboard 验证主菜单键盘布局与文案逐字一致。
func TestBuildQAMainMenuKeyboard(t *testing.T) {
	kb := BuildQAMainMenuKeyboard()
	if !kb.ResizeKeyboard || !kb.IsPersistent {
		t.Fatal("应设置 resize_keyboard 与 is_persistent")
	}
	if kb.InputFieldPlaceholder != "直接输入问题，或点击下方按钮" {
		t.Fatalf("占位文案不符: %q", kb.InputFieldPlaceholder)
	}
	rows := rowTexts(kb)
	want := [][]string{
		{MenuSubmit, MenuAppeal},
		{MenuChannels, MenuSubscriptions},
		{MenuStatus, MenuClear, MenuHelp},
	}
	if len(rows) != len(want) {
		t.Fatalf("行数 = %d, want %d", len(rows), len(want))
	}
	for i, row := range want {
		for j, text := range row {
			if rows[i][j] != text {
				t.Fatalf("按钮(%d,%d) = %q, want %q", i, j, rows[i][j], text)
			}
		}
	}
}

// TestSubmissionKeyboards 验证 5 个投稿阶段键盘的布局与占位文案。
func TestSubmissionKeyboards(t *testing.T) {
	cases := []struct {
		name        string
		build       func() *models.ReplyKeyboardMarkup
		placeholder string
		rows        [][]string
	}{
		{"标题", BuildSubmissionTitleKeyboard, "请输入投稿标题", [][]string{{SubmitMenuCancel}}},
		{"正文", BuildSubmissionContentKeyboard, "请输入正文，或点击跳过", [][]string{{SubmitMenuSkipContent, SubmitMenuCancel}}},
		{"匿名", BuildSubmissionAnonymousKeyboard, "请选择是否匿名投稿", [][]string{{SubmitMenuAnonymousYes, SubmitMenuAnonymousNo}, {SubmitMenuCancel}}},
		{"媒体", BuildSubmissionMediaKeyboard, "发送媒体文件，或点击完成/跳过", [][]string{{SubmitMenuDoneMedia, SubmitMenuSkipMedia}, {SubmitMenuCancel}}},
		{"确认", BuildSubmissionConfirmKeyboard, "请确认是否提交", [][]string{{SubmitMenuConfirm, SubmitMenuCancel}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kb := tc.build()
			if kb.InputFieldPlaceholder != tc.placeholder {
				t.Fatalf("占位文案 = %q, want %q", kb.InputFieldPlaceholder, tc.placeholder)
			}
			rows := rowTexts(kb)
			if len(rows) != len(tc.rows) {
				t.Fatalf("行数 = %d, want %d", len(rows), len(tc.rows))
			}
			for i, row := range tc.rows {
				if len(rows[i]) != len(row) {
					t.Fatalf("行 %d 列数 = %d, want %d", i, len(rows[i]), len(row))
				}
				for j, text := range row {
					if rows[i][j] != text {
						t.Fatalf("按钮(%d,%d) = %q, want %q", i, j, rows[i][j], text)
					}
				}
			}
		})
	}
}

// TestMenuButtonPattern 验证主菜单 Regex 源串与 qa_bot.py:1023-1027 逐字等价。
func TestMenuButtonPattern(t *testing.T) {
	want := "^(" + MenuAsk + "|" + MenuChannels + "|" + MenuSubscriptions + "|" +
		MenuStatus + "|" + MenuClear + "|" + MenuHelp + ")$"
	if got := MenuButtonPattern(); got != want {
		t.Fatalf("MenuButtonPattern = %q, want %q", got, want)
	}
}

// TestSubmissionButtonPattern 验证游离投稿按钮 Regex 源串与 qa_bot.py:1029-1036 逐字等价。
func TestSubmissionButtonPattern(t *testing.T) {
	want := "^(" + SubmitMenuCancel + "|" + SubmitMenuSkipContent + "|" + SubmitMenuSkipMedia + "|" +
		SubmitMenuDoneMedia + "|" + SubmitMenuConfirm + "|" + SubmitMenuAnonymousYes + "|" +
		SubmitMenuAnonymousNo + ")$"
	if got := SubmissionButtonPattern(); got != want {
		t.Fatalf("SubmissionButtonPattern = %q, want %q", got, want)
	}
}

// TestMenuAppealDefault 验证申诉按钮默认值与 zh-CN 词典 t("appeal.menu") 一致。
func TestMenuAppealDefault(t *testing.T) {
	if MenuAppeal != "🚫 封禁申诉" {
		t.Fatalf("MenuAppeal 默认值 = %q, want 🚫 封禁申诉", MenuAppeal)
	}
}
