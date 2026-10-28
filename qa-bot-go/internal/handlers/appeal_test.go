// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

// appeal_test.go：申诉子系统纯逻辑单测与 MySQL 集成测试
//（集成测试在 MYSQL_TEST_DSN 未设置或库不可达时自动跳过，对齐 guard 包约定）。

package handlers

import (
	"context"
	crand "crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/go-sql-driver/mysql"
	"github.com/go-telegram/bot/models"

	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/db"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/db/guard"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/i18n"
)

// ─────────────────────────────────────────────────────────────────────
// 入口判定与纯函数
// ─────────────────────────────────────────────────────────────────────

// TestAppealParseCommandText 对齐 appeal_handler.py:66-72 的入口判定矩阵。
func TestAppealParseCommandText(t *testing.T) {
	const menu = "🚫 封禁申诉"
	cases := []struct {
		text         string
		command      string
		deep         bool
		entryCommand bool
		partsLen     int
	}{
		{text: "/appeal", command: "/appeal", entryCommand: true, partsLen: 1},
		{text: "/appeal@MyBot", command: "/appeal", entryCommand: true, partsLen: 1},
		{text: "/APPEAL", command: "/appeal", entryCommand: true, partsLen: 1},
		{text: "/appeals", command: "/appeals", entryCommand: true, partsLen: 1},
		{text: "/cancel_appeal@x", command: "/cancel_appeal", entryCommand: true, partsLen: 1},
		{text: menu, command: "🚫", entryCommand: true, partsLen: 2},
		{text: "/start appeal_" + strings.Repeat("a", 32), command: "/start", deep: true, entryCommand: true, partsLen: 2},
		// 前缀命中但正则不匹配：entry_command 仍为真（Python 同构，走列表分支）。
		{text: "/start appeal_short", command: "/start", deep: true, entryCommand: true, partsLen: 2},
		{text: "/start", command: "/start", partsLen: 1},
		{text: "/start submit", command: "/start", partsLen: 2},
		{text: "你好", command: "你好", partsLen: 1},
		{text: "", command: "", partsLen: 0},
	}
	for _, tc := range cases {
		p := parseCommandText(tc.text, menu)
		if p.command != tc.command || p.deep != tc.deep || p.entryCommand != tc.entryCommand || len(p.parts) != tc.partsLen {
			t.Fatalf("parseCommandText(%q) = %+v, want command=%q deep=%v entry=%v parts=%d",
				tc.text, p, tc.command, tc.deep, tc.entryCommand, tc.partsLen)
		}
	}
	// 深链接负载整体保留在 parts[1]（Python split(maxsplit=1) 语义）。
	p := parseCommandText("/start appeal_"+strings.Repeat("b", 32), menu)
	if p.parts[1] != "appeal_"+strings.Repeat("b", 32) {
		t.Fatalf("parts[1] 应为完整深链接负载: %q", p.parts[1])
	}
}

// TestAppealSplitMaxOnce 对齐 Python text.split(maxsplit=1)。
func TestAppealSplitMaxOnce(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"abc", []string{"abc"}},
		{"a b c", []string{"a", "b c"}},
		{"  x   y  ", []string{"x", "y  "}}, // 调用前已 strip，此处验证函数本身
		{"/start\tappeal_x", []string{"/start", "appeal_x"}},
	}
	for _, tc := range cases {
		got := splitMaxOnce(tc.in)
		if len(got) != len(tc.want) {
			t.Fatalf("splitMaxOnce(%q) = %v, want %v", tc.in, got, tc.want)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Fatalf("splitMaxOnce(%q)[%d] = %q, want %q", tc.in, i, got[i], tc.want[i])
			}
		}
	}
}

// TestAppealSourceSignature 对齐 appeals.py:24-31：sha256(json.dumps 默认
// 分隔符的 [id, action, status, review_generation 默认 0])。
func TestAppealSourceSignature(t *testing.T) {
	sha := func(s string) string {
		sum := sha256.Sum256([]byte(s))
		return hex.EncodeToString(sum[:])
	}
	row := guard.Row{
		"id": "abc", "action": "ban", "status": "pending",
		"payload": map[string]any{"review_generation": float64(2)},
	}
	if got := sourceSignature(row); got != sha(`["abc", "ban", "pending", 2]`) {
		t.Fatalf("sourceSignature 有代际不符: %s", got)
	}
	row["payload"] = map[string]any{}
	if got := sourceSignature(row); got != sha(`["abc", "ban", "pending", 0]`) {
		t.Fatalf("sourceSignature 缺省代际不符: %s", got)
	}
}

// TestAppealEligibleCase 对齐 appeals.py:34-39 eligible + notification.py:32。
func TestAppealEligibleCase(t *testing.T) {
	base := guard.Row{"id": "c1", "chat_id": int64(-100), "user_id": int64(1), "action": "ban", "status": "pending", "payload": map[string]any{}}
	if !eligibleCase(base) {
		t.Fatal("ban+pending 应可申诉")
	}
	if eligibleCase(nil) {
		t.Fatal("nil 案件不可申诉")
	}
	report := guard.Row{"action": "report", "status": "pending", "payload": map[string]any{}}
	if eligibleCase(report) {
		t.Fatal("纯举报不可申诉")
	}
	ignored := guard.Row{"action": "ban", "status": "ignored", "payload": map[string]any{}}
	if eligibleCase(ignored) {
		t.Fatal("ignored 状态不可申诉")
	}
	confirmed := guard.Row{"action": "report", "status": "done", "payload": map[string]any{"review_ban_confirmed": true}}
	if !eligibleCase(confirmed) {
		t.Fatal("review_ban_confirmed 的封禁转化可申诉")
	}
	blacklisted := guard.Row{"action": "report", "status": "blacklisted", "payload": map[string]any{}}
	if !eligibleCase(blacklisted) {
		t.Fatal("blacklisted 状态可申诉")
	}
}

// TestAppealValidateInput 对齐 appeal_inbox.py:101-111。
func TestAppealValidateInput(t *testing.T) {
	appeal := guard.Row{"status": "draft", "revision": int64(3), "payload": map[string]any{}}
	draft := guard.Row{"mode": "draft", "payload": map[string]any{}}
	if err := validateInput(draft, appeal); err != nil {
		t.Fatalf("draft 匹配应通过: %v", err)
	}
	submitted := guard.Row{"status": "submitted", "revision": int64(3), "payload": map[string]any{}}
	if err := validateInput(draft, submitted); err == nil {
		t.Fatal("draft 撞 submitted 应 stale")
	}
	if err := validateInput(draft, nil); err == nil {
		t.Fatal("无申诉应 stale")
	}
	supp := guard.Row{"mode": "supplement", "payload": map[string]any{"revision": float64(3)}}
	needInfo := guard.Row{"status": "need_info", "revision": int64(3), "payload": map[string]any{}}
	if err := validateInput(supp, needInfo); err != nil {
		t.Fatalf("supplement 匹配应通过: %v", err)
	}
	if err := validateInput(supp, appeal); err == nil {
		t.Fatal("supplement 撞 draft 应 stale")
	}
	suppBadRev := guard.Row{"mode": "supplement", "payload": map[string]any{"revision": float64(2)}}
	if err := validateInput(suppBadRev, needInfo); err == nil {
		t.Fatal("版本不匹配应 stale")
	}
	review := guard.Row{"mode": "review", "payload": map[string]any{"revision": float64(3)}}
	pending := guard.Row{"status": "pending", "revision": int64(3), "payload": map[string]any{}}
	if err := validateInput(review, pending); err != nil {
		t.Fatalf("review 匹配应通过: %v", err)
	}
}

// TestAppealAppendCapped 对齐 Python [*old, v][-30:]。
func TestAppealAppendCapped(t *testing.T) {
	var list []any
	for i := int64(1); i <= 35; i++ {
		list = appendCapped(list, i, appealMessagesCap)
	}
	if len(list) != appealMessagesCap {
		t.Fatalf("应保留最近 30 条，实际 %d", len(list))
	}
	if pyInt(list[0]) != 6 || pyInt(list[len(list)-1]) != 35 {
		t.Fatalf("应保留 6..35，实际首=%v 尾=%v", list[0], list[len(list)-1])
	}
}

// TestAppealStripTagsUnescape 对齐 unescape(re.sub(r"<[^>]+>", "", s))。
func TestAppealStripTagsUnescape(t *testing.T) {
	got := stripTagsUnescape("<b>a&amp;b</b>中文<br/>")
	if got != "a&b中文" {
		t.Fatalf("stripTagsUnescape = %q", got)
	}
	if n := utf8.RuneCountInString(got); n != 5 {
		t.Fatalf("码点数应为 5，实际 %d", n)
	}
}

// TestAppealPyStrSemantics 验证 Python str() 等价的关键分支。
func TestAppealPyStrSemantics(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{nil, ""},
		{"文本", "文本"},
		{float64(-100123), "-100123"}, // JSON 整数 → Python int 的 str
		{float64(1.5), "1.5"},
		{int64(42), "42"},
		{true, "True"},
	}
	for _, tc := range cases {
		if got := pyStr(tc.in); got != tc.want {
			t.Fatalf("pyStr(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
	if got := pyStr(time.Date(2026, 1, 2, 3, 4, 5, 123456000, time.UTC)); got != "2026-01-02 03:04:05.123456" {
		t.Fatalf("pyStr(datetime 微秒) = %q", got)
	}
	if got := pyStr(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)); got != "2026-01-02 03:04:05" {
		t.Fatalf("pyStr(datetime 整秒) = %q", got)
	}
	if got := pyStr(time.Date(2026, 1, 2, 3, 4, 5, 123000000, time.UTC)); got != "2026-01-02 03:04:05.123000" {
		t.Fatalf("pyStr(datetime 补零微秒) = %q", got)
	}
}

// TestAppealNoticeBackoff 对齐 min(1800, 15 * 2 ** min(attempts, 7)) 秒。
func TestAppealNoticeBackoff(t *testing.T) {
	cases := []struct {
		attempts int64
		want     time.Duration
	}{
		{1, 30 * time.Second},
		{2, 60 * time.Second},
		{6, 960 * time.Second},
		{7, 1800 * time.Second},   // 15*128=1920 → 截到 1800
		{100, 1800 * time.Second}, // 指数封顶
	}
	for _, tc := range cases {
		if got := appealNoticeBackoff(tc.attempts); got != tc.want {
			t.Fatalf("appealNoticeBackoff(%d) = %v, want %v", tc.attempts, got, tc.want)
		}
	}
}

// TestAppealQuickReviewRequestIDFormat 守护 guard.QuickReviewRequestID 与
// Python appeal_inbox.py:265-267 元组 f-string（圆括号）的对齐。
func TestAppealQuickReviewRequestIDFormat(t *testing.T) {
	sum := sha256.Sum256([]byte("review:abc:42:7:(-100, 555):approve:"))
	want := hex.EncodeToString(sum[:])
	if got := guard.QuickReviewRequestID("abc", 42, 7, -100, 555, "approve", ""); got != want {
		t.Fatalf("幂等 ID 派生与 Python 元组形态不符: %s", got)
	}
}

// TestAppealDeepLinkRe 验证 RE2 锚点等价 re.fullmatch。
func TestAppealDeepLinkRe(t *testing.T) {
	good := "appeal_" + strings.Repeat("0123456789abcdef", 2)
	if !deepLinkRe.MatchString(good) {
		t.Fatalf("合法深链接应匹配: %s", good)
	}
	for _, bad := range []string{
		"appeal_" + strings.Repeat("g", 32), // 非 hex
		"appeal_" + strings.Repeat("a", 31), // 长度不足
		"Appeal_" + strings.Repeat("a", 32), // 大写
		"xappeal_" + strings.Repeat("a", 32),
	} {
		if deepLinkRe.MatchString(bad) {
			t.Fatalf("非法深链接不应匹配: %s", bad)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────
// 卡片与键盘 UI
// ─────────────────────────────────────────────────────────────────────

// appealRow 构造测试用申诉行。
func appealRow(status string, revision int64, payload map[string]any) guard.Row {
	return guard.Row{
		"id": "0123456789abcdef0123456789abcdef", "case_id": "case", "chat_id": int64(-100123),
		"user_id": int64(777), "active_key": "k", "status": status, "revision": revision,
		"published_revision": int64(0), "payload": payload, "error": nil,
	}
}

// TestAppealUICard 对齐 appeal_ui.py:17-56 card 的渲染要点。
func TestAppealUICard(t *testing.T) {
	payload := map[string]any{
		"source": map[string]any{
			"id": "case", "action": "ban", "status": "pending",
			"payload": map[string]any{
				"chat_title": "测试<b>群",
				"name":       "张三",
				"evidence":   map[string]any{"reason": "刷屏理由"},
			},
		},
		"blacklisted": false,
		"evidence": []any{
			map[string]any{"text": "第一条申诉", "files": []any{}},
		},
	}
	userCard := uiCard(appealRow("pending", 2, payload), false)
	// zh 模板：📋 <b>{chat} · {status}</b>\n封禁原因：{reason}\n\n申诉：{text}
	want := "📋 <b>测试&lt;b&gt;群 · 等待管理员审核</b>\n封禁原因：刷屏理由\n\n申诉：第一条申诉"
	if userCard != want {
		t.Fatalf("用户卡片不符:\n got=%q\nwant=%q", userCard, want)
	}
	reviewerCard := uiCard(appealRow("pending", 2, payload), true)
	if !strings.Contains(reviewerCard, `🛡 <b>封禁申诉 · 等待管理员审核</b>`) ||
		!strings.Contains(reviewerCard, `<a href="tg://user?id=777">张三</a>（777）`) {
		t.Fatalf("审核人卡片不符: %q", reviewerCard)
	}

	// 多轮材料：追加 latest_evidence 与附件计数。
	payloadMulti := clonePayload(payload)
	payloadMulti["evidence"] = []any{
		map[string]any{"text": "第一条申诉", "files": []any{map[string]any{"type": "photo"}}},
		map[string]any{"text": "补充<b>说明", "files": []any{}},
	}
	multi := uiCard(appealRow("pending", 2, payloadMulti), false)
	if !strings.Contains(multi, "\n\n补充：补充&lt;b&gt;说明") {
		t.Fatalf("应含 latest_evidence: %q", multi)
	}
	if !strings.Contains(multi, "\n📎 附件 1 个") {
		t.Fatalf("应含附件计数: %q", multi)
	}

	// 黑名单覆盖：状态替换 + 尾部提示。
	payloadBlack := clonePayload(payload)
	payloadBlack["blacklisted"] = true
	black := uiCard(appealRow("pending", 2, payloadBlack), false)
	if !strings.Contains(black, "待系统管理员解除全局黑名单") ||
		!strings.Contains(black, "⚠️ 全局黑名单中，需系统管理员明确移除后解封。") {
		t.Fatalf("黑名单卡片不符: %q", black)
	}

	// 审核意见：reviewer 截 500、用户截 1000 后转义。
	longReason := strings.Repeat("意", 600)
	payloadReview := clonePayload(payload)
	payloadReview["last_review"] = map[string]any{"action": "reject", "reason": longReason}
	rv := uiCard(appealRow("need_info", 3, payloadReview), true)
	if !strings.Contains(rv, "审核：驳回\n意见："+strings.Repeat("意", 500)) || strings.Contains(rv, strings.Repeat("意", 501)) {
		t.Fatalf("reviewer 意见应截 500: %q", rv)
	}
	uv := uiCard(appealRow("need_info", 3, payloadReview), false)
	if !strings.Contains(uv, strings.Repeat("意", 600)) {
		t.Fatalf("用户意见保留 600 字: %q", uv)
	}

	// 错误行：已知命名空间翻译、未知显示通用重试。
	withErr := clonePayload(payload)
	withErr["evidence"] = []any{}
	eRow := appealRow("pending", 2, withErr)
	eRow["error"] = "appeal.stale"
	if got := uiCard(eRow, false); !strings.Contains(got, i18n.T("appeal.stale", nil)) {
		t.Fatalf("已知错误键应翻译: %q", got)
	}
	eRow["error"] = "RuntimeError"
	if got := uiCard(eRow, false); !strings.Contains(got, i18n.T("appeal.retrying", nil)) {
		t.Fatalf("未知错误应显示重试提示: %q", got)
	}
}

// clonePayload 深拷贝测试载荷（map/list 两层）。
func clonePayload(src map[string]any) map[string]any {
	dst := make(map[string]any, len(src))
	for k, v := range src {
		if m, ok := v.(map[string]any); ok {
			dst[k] = clonePayload(m)
			continue
		}
		if l, ok := v.([]any); ok {
			cp := make([]any, len(l))
			copy(cp, l)
			dst[k] = cp
			continue
		}
		dst[k] = v
	}
	return dst
}

// TestAppealUIButtons 对齐 appeal_ui.py:59-103 buttons 的行结构。
func TestAppealUIButtons(t *testing.T) {
	id := "0123456789abcdef0123456789abcdef"
	payload := map[string]any{"blacklisted": false}

	user := uiButtons(appealRow("draft", 4, payload), false, false, "")
	if user == nil || len(user.InlineKeyboard) != 2 {
		t.Fatalf("draft 用户键盘应有 2 行: %+v", user)
	}
	if user.InlineKeyboard[0][0].CallbackData != "ap:view:"+id || user.InlineKeyboard[0][1].CallbackData != "ap:list:history:0" {
		t.Fatalf("首行应为刷新+历史: %+v", user.InlineKeyboard[0])
	}
	if user.InlineKeyboard[1][0].CallbackData != "ap:withdraw:"+id+":4" {
		t.Fatalf("次行应为撤回: %+v", user.InlineKeyboard[1])
	}
	needInfo := uiButtons(appealRow("need_info", 4, payload), false, false, "")
	if len(needInfo.InlineKeyboard) != 3 || needInfo.InlineKeyboard[1][0].CallbackData != "ap:supp:"+id+":4" {
		t.Fatalf("need_info 应加补充行: %+v", needInfo.InlineKeyboard)
	}
	done := uiButtons(appealRow("rejected", 4, payload), false, false, "")
	if done.InlineKeyboard[1][0].CallbackData != "ap:list:cases:0" {
		t.Fatalf("终态应给可申诉列表: %+v", done.InlineKeyboard[1])
	}

	rev := uiButtons(appealRow("pending", 4, payload), true, false, "")
	if len(rev.InlineKeyboard) != 3 ||
		rev.InlineKeyboard[0][0].CallbackData != "ap:review:"+id+":4:approve" ||
		rev.InlineKeyboard[0][1].CallbackData != "ap:review:"+id+":4:reject" ||
		rev.InlineKeyboard[1][0].CallbackData != "ap:review:"+id+":4:more" ||
		rev.InlineKeyboard[1][1].CallbackData != "ap:options:"+id+":4" {
		t.Fatalf("审核默认键盘不符: %+v", rev.InlineKeyboard)
	}
	if rev.InlineKeyboard[2][0].CallbackData != "ap:read:"+id+":4:evidence" ||
		rev.InlineKeyboard[2][1].CallbackData != "ap:read:"+id+":4:audit" {
		t.Fatalf("尾行应为材料+审计: %+v", rev.InlineKeyboard[2])
	}

	blacklisted := map[string]any{"blacklisted": true}
	revBlack := uiButtons(appealRow("pending", 4, blacklisted), true, false, "")
	if revBlack.InlineKeyboard[0][0].CallbackData != "ap:check:"+id+":4:global" {
		t.Fatalf("黑名单用户应给 global 快捷键: %+v", revBlack.InlineKeyboard[0])
	}
	revExpanded := uiButtons(appealRow("pending", 4, payload), true, true, "")
	if len(revExpanded.InlineKeyboard) != 3 ||
		revExpanded.InlineKeyboard[0][0].CallbackData != "ap:check:"+id+":4:allow" ||
		revExpanded.InlineKeyboard[1][0].CallbackData != "ap:admin:"+id+":4" {
		t.Fatalf("展开态应给 allow+返回: %+v", revExpanded.InlineKeyboard)
	}
	revExpandedBlack := uiButtons(appealRow("pending", 4, blacklisted), true, true, "")
	if revExpandedBlack.InlineKeyboard[0][0].CallbackData != "ap:check:"+id+":4:global" {
		t.Fatalf("黑名单展开态应仅给 global: %+v", revExpandedBlack.InlineKeyboard)
	}
	revConfirm := uiButtons(appealRow("pending", 4, payload), true, false, "global")
	if revConfirm.InlineKeyboard[0][0].CallbackData != "ap:review:"+id+":4:global" ||
		revConfirm.InlineKeyboard[1][0].CallbackData != "ap:admin:"+id+":4" {
		t.Fatalf("确认态应给确认+返回: %+v", revConfirm.InlineKeyboard)
	}
}

// TestAppealUIInputPromptAndButtons 对齐 appeal_ui.py:106-142。
func TestAppealUIInputPromptAndButtons(t *testing.T) {
	appeal := appealRow("draft", 1, map[string]any{
		"source": map[string]any{
			"id": "case", "action": "ban", "status": "pending",
			"payload": map[string]any{
				"chat_title": "测试群",
				"evidence":   map[string]any{"reason": "原因"},
			},
		},
		"evidence": []any{},
	})
	entry := guard.Row{"mode": "draft", "token": "tok123", "payload": map[string]any{
		"text": "已填内容", "files": []any{map[string]any{"type": "photo"}},
	}}
	prompt := uiInputPrompt(entry, appeal)
	if !strings.Contains(prompt, "📝 <b>测试群 · 封禁申诉</b>\n封禁原因：原因") {
		t.Fatalf("应含上下文头: %q", prompt)
	}
	if !strings.Contains(prompt, "\n\n已填内容") || !strings.Contains(prompt, "\n📎 附件 1 个") {
		t.Fatalf("应含预览与附件计数: %q", prompt)
	}
	if review := uiInputPrompt(guard.Row{"mode": "review", "payload": map[string]any{"action": "approve"}}, nil); !strings.Contains(review, "📝 ✅ 解封\n请发送处理原因，发送后直接提交。") {
		t.Fatalf("review 提示不符: %q", review)
	}

	buttons := uiInputButtons(entry)
	if buttons == nil || len(buttons.InlineKeyboard) != 1 || len(buttons.InlineKeyboard[0]) != 2 {
		t.Fatalf("草稿输入应有两个按钮: %+v", buttons)
	}
	if buttons.InlineKeyboard[0][0].CallbackData != "ap:input:tok123:submit" ||
		buttons.InlineKeyboard[0][1].CallbackData != "ap:input:tok123:cancel" {
		t.Fatalf("提交/取消回调数据不符: %+v", buttons.InlineKeyboard[0])
	}
	emptyEntry := guard.Row{"mode": "draft", "token": "t", "payload": map[string]any{"text": "   ", "files": []any{}}}
	emptyButtons := uiInputButtons(emptyEntry)
	if len(emptyButtons.InlineKeyboard[0]) != 1 || emptyButtons.InlineKeyboard[0][0].CallbackData != "ap:input:t:cancel" {
		t.Fatalf("纯空白文本不给提交: %+v", emptyButtons.InlineKeyboard[0])
	}
	reviewButtons := uiInputButtons(guard.Row{"mode": "review", "token": "t2", "payload": map[string]any{"action": "reject"}})
	if len(reviewButtons.InlineKeyboard[0]) != 1 {
		t.Fatalf("reject 审核输入无确认键: %+v", reviewButtons.InlineKeyboard[0])
	}
}

// TestAppealUIUserAlert 对齐 appeal_ui.py:145-154。
func TestAppealUIUserAlert(t *testing.T) {
	appeal := appealRow("approved", 5, map[string]any{
		"source": map[string]any{"id": "case", "action": "ban", "status": "pending",
			"payload": map[string]any{"chat_title": "群A"}},
		"last_review": map[string]any{"action": "approve", "reason": "已复核"},
	})
	alert := uiUserAlert(appeal)
	want := "✅ 群A 的申诉已通过，封禁已解除，可重新申请入群。"
	if alert != want {
		t.Fatalf("approved 提醒不符: %q", alert)
	}
	if uiUserAlert(appealRow("pending", 1, map[string]any{})) != "" {
		t.Fatal("非终局状态不提醒")
	}
}

// TestAppealPrivateCheck 验证仅私聊判定（appeal_handler.py:52-60）。
func TestAppealPrivateCheck(t *testing.T) {
	private := &models.Message{
		From: &models.User{ID: 42, IsBot: false},
		Chat: models.Chat{ID: 42, Type: models.ChatTypePrivate},
	}
	if !isPrivateMessage(private) {
		t.Fatal("同 ID 私聊应通过")
	}
	group := &models.Message{
		From: &models.User{ID: 42},
		Chat: models.Chat{ID: -100, Type: models.ChatTypeGroup},
	}
	if isPrivateMessage(group) {
		t.Fatal("群聊不通过")
	}
	mismatch := &models.Message{
		From: &models.User{ID: 42},
		Chat: models.Chat{ID: 43, Type: models.ChatTypePrivate},
	}
	if isPrivateMessage(mismatch) {
		t.Fatal("chat 与 user 不同不通过")
	}
	bot := &models.Message{
		From: &models.User{ID: 42, IsBot: true},
		Chat: models.Chat{ID: 42, Type: models.ChatTypePrivate},
	}
	if isPrivateMessage(bot) {
		t.Fatal("bot 用户不通过")
	}
}

// TestAppealMediaExtract 验证 extract_media 的三类与 photo 取最大尺寸。
func TestAppealMediaExtract(t *testing.T) {
	msg := &models.Message{
		ID: 9,
		Photo: []models.PhotoSize{
			{FileID: "small", FileSize: 100},
			{FileID: "large", FileSize: 900},
		},
	}
	media := extractMedia(msg)
	if media["type"] != "photo" || media["file_id"] != "large" || pyInt(media["size"]) != 900 || pyInt(media["message_id"]) != 9 {
		t.Fatalf("photo 附件不符: %v", media)
	}
	doc := &models.Message{ID: 10, Document: &models.Document{FileID: "doc1", FileSize: 5}}
	if media := extractMedia(doc); media["type"] != "document" || media["file_id"] != "doc1" {
		t.Fatalf("document 附件不符: %v", media)
	}
	if extractMedia(&models.Message{ID: 11}) != nil {
		t.Fatal("纯文本无附件")
	}
}

// ─────────────────────────────────────────────────────────────────────
// MySQL 集成测试（MYSQL_TEST_DSN 未设置则跳过）
// ─────────────────────────────────────────────────────────────────────

// appealTestRepo 准备集成测试环境：guard schema 幂等执行；
// MYSQL_TEST_DSN 未设置或库不可达时跳过（对齐 guard 包测试约定）。
func appealTestRepo(t *testing.T) *guard.Repository {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("MYSQL_TEST_DSN"))
	if dsn == "" {
		t.Skip("MYSQL_TEST_DSN 未设置，跳过 MySQL 集成测试")
	}
	if cfg, err := mysql.ParseDSN(dsn); err == nil {
		cfg.ParseTime = true
		cfg.Loc = time.UTC
		dsn = cfg.FormatDSN()
	}
	handle, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Skipf("打开测试库失败: %v", err)
	}
	if err := handle.PingContext(context.Background()); err != nil {
		_ = handle.Close()
		t.Skipf("MySQL 测试库不可达: %v", err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	repo := guard.New(handle)
	if err := repo.Initialize(context.Background()); err != nil {
		t.Fatalf("初始化 guard schema 失败: %v", err)
	}
	return repo
}

// seedAppealCase 直插一条 moderation_cases（主进程建案语义），返回 case 行。
func seedAppealCase(t *testing.T, repo *guard.Repository, chatID, userID int64) guard.Row {
	t.Helper()
	caseID := appealTestUUID()
	now := db.MysqlDatetime6(db.NowUTC())
	_, err := repo.DB.ExecContext(context.Background(), `INSERT INTO moderation_cases
		(id,event_key,chat_id,user_id,action,status,payload,created_at,updated_at)
		VALUES (?,?,?,?,?,?,?,?,?)`,
		caseID, "evt-"+caseID, chatID, userID, "ban", "pending", "{}", now, now)
	if err != nil {
		t.Fatalf("插入前置 case 失败: %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = repo.DB.ExecContext(ctx, "DELETE FROM moderation_appeal_events WHERE appeal_id IN (SELECT id FROM moderation_appeals WHERE case_id=?)", caseID)
		_, _ = repo.DB.ExecContext(ctx, "DELETE FROM moderation_appeal_inputs WHERE appeal_id IN (SELECT id FROM moderation_appeals WHERE case_id=?)", caseID)
		_, _ = repo.DB.ExecContext(ctx, "DELETE FROM moderation_appeal_requests WHERE appeal_id IN (SELECT id FROM moderation_appeals WHERE case_id=?)", caseID)
		_, _ = repo.DB.ExecContext(ctx, "DELETE FROM moderation_appeal_notifications WHERE appeal_id IN (SELECT id FROM moderation_appeals WHERE case_id=?)", caseID)
		_, _ = repo.DB.ExecContext(ctx, "DELETE FROM moderation_appeals WHERE case_id=?", caseID)
		_, _ = repo.DB.ExecContext(ctx, "DELETE FROM moderation_cases WHERE id=?", caseID)
		_, _ = repo.DB.ExecContext(ctx, "DELETE FROM moderation_appeal_inputs WHERE actor_id=?", userID)
	})
	return guard.Row{"id": caseID, "chat_id": chatID, "user_id": userID, "action": "ban", "status": "pending", "payload": map[string]any{}}
}

// TestAppealInboxFlowIntegration 对齐 spec §2.9 的核心链路：
// begin→append（去重/限额）→submit（审计+状态迁移+输入清理）→24h 配额。
func TestAppealInboxFlowIntegration(t *testing.T) {
	repo := appealTestRepo(t)
	ctx := context.Background()
	userID := time.Now().UnixNano() % 1_000_000_000
	chatID := -int64(100000 + time.Now().UnixNano()%900000)
	h := NewAppealHandler(repo, nil, nil, nil)

	caseRow := seedAppealCase(t, repo, chatID, userID)
	caseID := rowStr(caseRow, "id")

	// begin：建 draft + 初始输入。
	appeal, err := h.inboxBegin(ctx, userID, caseID)
	if err != nil {
		t.Fatalf("begin 失败: %v", err)
	}
	if rowStr(appeal, "status") != "draft" || rowInt64(appeal, "revision") != 1 {
		t.Fatalf("初始申诉应为 draft/1: %v/%v", rowStr(appeal, "status"), rowInt64(appeal, "revision"))
	}
	// 再 begin：活跃申诉复用。
	again, err := h.inboxBegin(ctx, userID, caseID)
	if err != nil {
		t.Fatalf("重复 begin 失败: %v", err)
	}
	if rowStr(again, "id") != rowStr(appeal, "id") {
		t.Fatal("活跃申诉应复用")
	}
	entry, err := repo.Input(ctx, userID)
	if err != nil || entry == nil || rowStr(entry, "mode") != "draft" {
		t.Fatalf("begin 应留下 draft 输入: %+v err=%v", entry, err)
	}
	token := rowStr(entry, "token")

	// append：文本 + 消息 ID 去重。
	if _, err := h.inboxAppend(ctx, userID, "第一段", nil, 11); err != nil {
		t.Fatalf("append 文本失败: %v", err)
	}
	sameEntry, err := h.inboxAppend(ctx, userID, "重复消息", nil, 11)
	if err != nil {
		t.Fatalf("去重 append 失败: %v", err)
	}
	if pyStr(asMap(sameEntry["payload"])["text"]) != "第一段" {
		t.Fatalf("重复消息 ID 不应追加文本: %v", asMap(sameEntry["payload"])["text"])
	}
	// append：附件。
	media := map[string]any{"type": "photo", "file_id": "f1", "size": int64(1024), "message_id": int64(12)}
	if _, err := h.inboxAppend(ctx, userID, "", media, 12); err != nil {
		t.Fatalf("append 附件失败: %v", err)
	}
	// append：超限文本。
	if _, err := h.inboxAppend(ctx, userID, strings.Repeat("字", 2001), nil, 13); err == nil {
		t.Fatal("超 2000 字应 text_limit")
	} else if ge, ok := err.(*GuardError); !ok || ge.Key != "appeal.text_limit" {
		t.Fatalf("应报 appeal.text_limit: %v", err)
	}
	// submit。
	submitted, err := h.inboxSubmit(ctx, userID, token)
	if err != nil {
		t.Fatalf("submit 失败: %v", err)
	}
	if rowStr(submitted, "status") != "submitted" || rowInt64(submitted, "revision") != 2 {
		t.Fatalf("submit 后应 submitted/2: %s/%v", rowStr(submitted, "status"), rowInt64(submitted, "revision"))
	}
	cleared, err := repo.Input(ctx, userID)
	if err != nil || cleared != nil {
		t.Fatalf("submit 后输入应清空: %+v err=%v", cleared, err)
	}
	payload := asMap(submitted["payload"])
	if len(asList(payload["evidence"])) != 1 {
		t.Fatalf("应有 1 条材料: %v", payload["evidence"])
	}
	evidence := asMap(asList(payload["evidence"])[0])
	if pyStr(evidence["text"]) != "第一段" || len(asList(evidence["files"])) != 1 {
		t.Fatalf("材料内容不符: %v", evidence)
	}
	// 提交审计与 24h 配额。
	count, err := func() (int64, error) {
		var out int64
		err := repo.Reader(ctx, func(s *guard.Store) error {
			var e error
			out, e = s.SubmissionCount(ctx, userID)
			return e
		})
		return out, err
	}()
	if err != nil || count != 1 {
		t.Fatalf("submission_count 应为 1: %v err=%v", count, err)
	}

	// 冷却：新案件 begin → 3 次/24h 未达（仅 1 次）应正常；连提 3 次后第 4 案冷却。
	var lastCaseID string
	for i := 0; i < 2; i++ {
		next := seedAppealCase(t, repo, chatID-1000-int64(i), userID)
		lastCaseID = rowStr(next, "id")
		a, err := h.inboxBegin(ctx, userID, lastCaseID)
		if err != nil {
			t.Fatalf("第 %d 案 begin 失败: %v", i+2, err)
		}
		e, err := repo.Input(ctx, userID)
		if err != nil {
			t.Fatalf("取输入失败: %v", err)
		}
		if _, err := h.inboxAppend(ctx, userID, "材料", nil, int64(20+i)); err != nil {
			t.Fatalf("第 %d 案 append 失败: %v", i+2, err)
		}
		if _, err := h.inboxSubmit(ctx, userID, rowStr(e, "token")); err != nil {
			t.Fatalf("第 %d 案 submit 失败: %v", i+2, err)
		}
		_ = a
	}
	third := seedAppealCase(t, repo, chatID-9000, userID)
	if _, err := h.inboxBegin(ctx, userID, rowStr(third, "id")); err == nil {
		t.Fatal("24h 内第 4 次 begin 应冷却")
	} else if ge, ok := err.(*GuardError); !ok || ge.Key != "appeal.cooldown" {
		t.Fatalf("应报 appeal.cooldown: %v", err)
	}
	_ = lastCaseID
	_ = caseID
}

// TestAppealQuickReviewIntegration 覆盖 quick_review 队列化、幂等、
// global 管理员门槛与黑名单冲突。
func TestAppealQuickReviewIntegration(t *testing.T) {
	repo := appealTestRepo(t)
	ctx := context.Background()
	userID := time.Now().UnixNano() % 1_000_000_000
	adminID := userID + 7
	origin := originOf(-100999, 77)
	h := NewAppealHandler(repo, nil, nil, nil)
	h.IsAdmin = func(int64) bool { return true }

	caseRow := seedAppealCase(t, repo, -int64(100000+time.Now().UnixNano()%900000), userID)
	caseID := rowStr(caseRow, "id")

	// 走到 pending：begin → submit。
	appeal, err := h.inboxBegin(ctx, userID, caseID)
	if err != nil {
		t.Fatalf("begin 失败: %v", err)
	}
	entry, _ := repo.Input(ctx, userID)
	if _, err := h.inboxAppend(ctx, userID, "材料", nil, 1); err != nil {
		t.Fatalf("append 失败: %v", err)
	}
	if _, err := h.inboxSubmit(ctx, userID, rowStr(entry, "token")); err != nil {
		t.Fatalf("submit 失败: %v", err)
	}
	revision := rowInt64(appeal, "revision") // 1

	// 绑定审核人卡片通知行（check_origin 的前置条件）。
	fresh, err := repo.Get(ctx, rowStr(appeal, "id"))
	if err != nil {
		t.Fatalf("取申诉失败: %v", err)
	}
	err = repo.Reader(ctx, func(s *guard.Store) error {
		return s.SaveNotice(ctx, fresh, adminID, origin.messageID, "reviewer")
	})
	if err != nil {
		t.Fatalf("绑定审核通知失败: %v", err)
	}

	// quick_review：approve 入队。
	if err := h.inboxQuickReview(ctx, adminID, rowStr(appeal, "id"), revision, origin, "approve"); err != nil {
		t.Fatalf("quick_review 失败: %v", err)
	}
	requestID := guard.QuickReviewRequestID(rowStr(appeal, "id"), adminID, revision, origin.chatID, origin.messageID, "approve", "")
	var payloadText string
	var status string
	err = repo.DB.QueryRowContext(ctx,
		"SELECT payload, status FROM moderation_appeal_requests WHERE id=?", requestID).Scan(&payloadText, &status)
	if err != nil {
		t.Fatalf("审核请求应已入队: %v", err)
	}
	if status != "pending" || !strings.Contains(payloadText, "复核后同意解除本次封禁。") {
		t.Fatalf("请求载荷不符: status=%s payload=%s", status, payloadText)
	}
	// 幂等：同键重复点击不新增。
	if err := h.inboxQuickReview(ctx, adminID, rowStr(appeal, "id"), revision, origin, "approve"); err != nil {
		t.Fatalf("重复 quick_review 失败: %v", err)
	}
	var total int
	if err := repo.DB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM moderation_appeal_requests WHERE appeal_id=?", rowStr(appeal, "id")).Scan(&total); err != nil || total != 1 {
		t.Fatalf("重复点击应保持 1 条请求: %v err=%v", total, err)
	}

	// global 非管理员：拒绝。
	h.IsAdmin = func(int64) bool { return false }
	err = h.inboxQuickReview(ctx, adminID+1, rowStr(appeal, "id"), revision, originOf(-100999, 78), "global")
	if ge, ok := err.(*GuardError); !ok || ge.Key != "appeal.global_admin_required" {
		t.Fatalf("global 非管理员应拒绝: %v", err)
	}
}

// appealTestUUID 生成 32 位十六进制随机串（测试用例唯一键）。
func appealTestUUID() string {
	var b [16]byte
	if _, err := crand.Read(b[:]); err != nil {
		panic(fmt.Sprintf("crypto/rand 读取失败: %v", err))
	}
	return hex.EncodeToString(b[:])
}
