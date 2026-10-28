// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package handlers

import (
	"context"
	"strings"
	"testing"

	"github.com/go-telegram/bot/models"

	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/i18n"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/tg"
)

// TestMenuButtonRoutesToCommands 6 个主菜单按钮逐一转调对应命令
// （handle_menu_button 的 if/elif 链）。
func TestMenuButtonRoutesToCommands(t *testing.T) {
	statusQuota := &fakeQuota{usage: UsageStatus{UsedToday: 1, Remaining: 9}}
	cases := []struct {
		text  string
		check func(*fakeTG) bool
	}{
		{tg.MenuStatus, func(f *fakeTG) bool {
			return len(f.sent) == 1 && strings.Contains(f.sent[0].text, "📊 <b>系统状态</b>")
		}},
		{tg.MenuHelp, func(f *fakeTG) bool {
			return len(f.sent) == 1 && strings.HasPrefix(f.sent[0].text, "📚 <b>使用帮助</b>")
		}},
		{tg.MenuClear, func(f *fakeTG) bool {
			return len(f.sent) == 1 && strings.HasPrefix(f.sent[0].text, "🗑️ **对话记忆已清除**")
		}},
		{tg.MenuChannels, func(f *fakeTG) bool {
			return len(f.sent) == 1 && strings.HasPrefix(f.sent[0].text, "📋 **可订阅频道列表**")
		}},
		{tg.MenuSubscriptions, func(f *fakeTG) bool {
			return len(f.sent) == 1 && strings.HasPrefix(f.sent[0].text, "您还没有订阅任何频道。")
		}},
		{tg.MenuAsk, func(f *fakeTG) bool {
			return len(f.sent) == 1 && strings.HasPrefix(f.sent[0].text, "🔎 <b>指定频道查询</b>") &&
				f.sent[0].parseMode == models.ParseModeHTML
		}},
	}
	for _, tc := range cases {
		t.Run(tc.text, func(t *testing.T) {
			ftg := &fakeTG{sendOK: true}
			c := newTestCommands(ftg, statusQuota, &fakeSessions{deleted: 3}, &fakeEngine{})
			c.HandleMenuButton(context.Background(), qaTextUpdate(tc.text, 42))
			if !tc.check(ftg) {
				t.Fatalf("菜单按钮 %q 分发结果不符: %+v", tc.text, ftg.sent)
			}
		})
	}
}

// TestMenuAskButtonMessage 校验"指定频道查询"按钮的用法文案逐字一致
// （qa_bot.py:523-531，注意全角冒号与 &lt; 转义）。
func TestMenuAskButtonMessage(t *testing.T) {
	ftg := &fakeTG{sendOK: true}
	c := newTestCommands(ftg, &fakeQuota{}, &fakeSessions{}, &fakeEngine{})
	c.HandleMenuButton(context.Background(), qaTextUpdate(tg.MenuAsk, 42))

	want := "🔎 <b>指定频道查询</b>\n\n" +
		"请按以下格式发送命令：\n" +
		"<code>/ask &lt;频道链接或@用户名&gt; &lt;问题&gt;</code>\n\n" +
		"示例：\n" +
		"<code>/ask @channel_name 最近有什么更新？</code>\n\n" +
		"💡 也可以直接输入自然语言问题，我会自动检索相关记录。"
	if ftg.sent[0].text != want {
		t.Fatalf("菜单 ask 文案不符:\n got=%q\nwant=%q", ftg.sent[0].text, want)
	}
}

// TestStraySubmissionButton 校验游离投稿按钮：i18n 文案 + 纯文本 + 主键盘。
func TestStraySubmissionButton(t *testing.T) {
	ftg := &fakeTG{sendOK: true}
	c := newTestCommands(ftg, &fakeQuota{}, &fakeSessions{}, &fakeEngine{})
	c.HandleStraySubmissionButton(context.Background(), qaTextUpdate(tg.SubmitMenuConfirm, 42))

	want := i18n.T("submission.no_active_submission_restart", nil)
	if len(ftg.sent) != 1 || ftg.sent[0].text != want {
		t.Fatalf("游离投稿按钮回复不符: %+v", ftg.sent)
	}
	if ftg.sent[0].parseMode != "" || !ftg.sent[0].keyboard {
		t.Fatalf("游离投稿按钮应为纯文本 + 主键盘")
	}
}

// TestCancelSubmissionOutside 校验会话外 /cancel_submit：清理状态 + i18n 文案。
func TestCancelSubmissionOutside(t *testing.T) {
	ftg := &fakeTG{sendOK: true}
	subs := &fakeSubmissions{}
	c := newTestCommands(ftg, &fakeQuota{}, &fakeSessions{}, &fakeEngine{})
	c.submissions = subs
	c.CancelSubmissionOutsideConversation(context.Background(), qaCommandUpdate("/cancel_submit", 42))

	want := i18n.T("submission.no_active_submission", nil)
	if len(ftg.sent) != 1 || ftg.sent[0].text != want {
		t.Fatalf("会话外取消投稿回复不符: %+v", ftg.sent)
	}
	if len(subs.cleared) != 1 || subs.cleared[0] != 42 {
		t.Fatalf("应清理一次用户投稿状态: %v", subs.cleared)
	}
}

// TestRouteStrayButton 校验 Route 对 7 个游离投稿按钮全量分发。
func TestRouteStrayButton(t *testing.T) {
	buttons := []string{
		tg.SubmitMenuCancel, tg.SubmitMenuSkipContent, tg.SubmitMenuSkipMedia,
		tg.SubmitMenuDoneMedia, tg.SubmitMenuConfirm, tg.SubmitMenuAnonymousYes,
		tg.SubmitMenuAnonymousNo,
	}
	for _, btn := range buttons {
		ftg := &fakeTG{sendOK: true}
		c := newTestCommands(ftg, &fakeQuota{}, &fakeSessions{}, &fakeEngine{})
		c.Route(context.Background(), qaTextUpdate(btn, 42))
		if len(ftg.sent) != 1 || !strings.Contains(ftg.sent[0].text, "投稿") {
			t.Fatalf("按钮 %q 未被 Route 分发: %+v", btn, ftg.sent)
		}
	}
}
