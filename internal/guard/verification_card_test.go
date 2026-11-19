// Copyright 2026 Sakura-Bot
// SPDX-License-Identifier: AGPL-3.0-or-later

package guard

import (
	"reflect"
	"strings"
	"testing"
	"time"

	guarddb "github.com/Sakura520222/Sakura-Bot/internal/db/guard"
	"github.com/Sakura520222/Sakura-Bot/internal/i18n"
	"github.com/Sakura520222/Sakura-Bot/internal/mtproto"
)

func TestAssistantVerificationCardMatchesPreviousLayout(t *testing.T) {
	lang := i18n.GetLanguage()
	defer i18n.SetLanguage(lang)
	i18n.SetLanguage(i18n.LangZhCN)
	grp := DefaultGuardGroup()
	grp.VerificationMaxAttempts = 3
	grp.VerificationWrongAction = "ban"
	grp.VerificationTimeoutAction = "kick"
	grp.VerificationAdminActions = []string{"skip", "blacklist", "ban"}
	grp.KickButtonEnabled = true
	now := time.Unix(1000, 0)
	id := strings.Repeat("a", 32)
	secret := strings.Repeat("b", 24)
	p := map[string]any{
		"name":      "示例 成员",
		"assistant": map[string]any{"bot_id": int64(56), "username": "assistant_bot", "secret": secret, "max_attempts": 3},
	}
	text, rows := verificationCard(id, 12345, "waiting", p, now.Add(299*time.Second), now, grp)
	wantText := "Sakura Guard 入群验证\n昵称：示例 成员\n用户名：未设置\n用户 ID：12345\n请在约 299 秒内前往助手 Bot 完成验证。答错 3 次：封禁；超时：踢出（可再加入）。\n下方管理按钮仅管理员可用。"
	if text != wantText {
		t.Fatalf("verification text = %q", text)
	}
	prefix := "sg:v:" + id + ":"
	wantRows := [][]mtproto.InlineButton{
		{{Text: "前往助手 Bot 验证", URL: "https://t.me/assistant_bot?start=verify_" + id + secret}},
		{
			{Text: "一键封禁", Data: prefix + "b"},
			{Text: "封禁并加入黑名单", Data: prefix + "x"},
			{Text: "仅踢出", Data: prefix + "j"},
		},
		{{Text: "跳过验证", Data: prefix + "s"}},
		{{Text: "打开用户资料", URL: "tg://user?id=12345"}},
	}
	if !reflect.DeepEqual(rows, wantRows) {
		t.Fatalf("verification rows = %+v", rows)
	}
	delete(p["assistant"].(map[string]any), "username")
	text, rows = verificationCard(id, 12345, "waiting", p, now.Add(299*time.Second), now, grp)
	if !strings.Contains(text, "助手暂不可用，验证禁言仍然生效") || !reflect.DeepEqual(rows, wantRows[1:]) {
		t.Fatalf("unexpected unavailable assistant card: %q %+v", text, rows)
	}
}

func TestGuardNoticeUsesReadableStatusAndEscapesEvidence(t *testing.T) {
	lang := i18n.GetLanguage()
	defer i18n.SetLanguage(lang)
	i18n.SetLanguage(i18n.LangZhCN)
	row := guarddb.Row{
		"id": "case-1", "chat_id": int64(-1001234), "user_id": int64(12345),
		"action": "approve", "status": "execution_failed", "error": "USER_NOT_PARTICIPANT",
		"payload": map[string]any{
			"name": "<b>示例成员</b>", "chat_title": "测试群", "chat_link": "https://t.me/example_group",
			"evidence": map[string]any{"reason": "approve_then_group_verification", "original": "A & B"},
		},
	}
	html := RenderCaseCardHTML(row)
	text, entities := mtproto.ParseHTMLSpans(html)
	for _, want := range []string{"Sakura Guard · case-1", "群组：测试群 (-1001234)", "用户：<b>示例成员</b>",
		"原文：A & B", "动作：批准入群申请", "状态：执行失败", "原因：批准入群申请，入群后完成验证", "错误：USER_NOT_PARTICIPANT"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in %q", want, text)
		}
	}
	if strings.ContainsAny(text, "🛡🤖") || len(entities) != 2 || entities[0].URL != "https://t.me/example_group" ||
		entities[1].Kind != "text_mention" || entities[1].UserID != 12345 {
		t.Fatalf("unexpected rendered notice: %q %+v", text, entities)
	}
}

func TestGuardNoticeLoadsGroupMetadata(t *testing.T) {
	ops := newFakeOps(1)
	chatID := mtproto.ChannelMarkedID(555)
	ops.channels[chatID] = &mtproto.ChannelInfo{Title: "Example group", Username: "example_group"}
	n := &Notifier{svc: &Service{ops: ops}}
	row := guarddb.Row{"chat_id": chatID, "payload": map[string]any{}}
	n.fillNoticeGroup(t.Context(), row)
	text, entities := mtproto.ParseHTMLSpans(RenderCaseCardHTML(row))
	if !strings.Contains(text, "Example group @example_group") ||
		len(entities) != 1 || entities[0].URL != "https://t.me/example_group" {
		t.Fatalf("group title or link missing: %q %+v", text, entities)
	}
}
