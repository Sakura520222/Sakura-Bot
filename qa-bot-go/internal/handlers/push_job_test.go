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
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/db"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/tg"
)

// ── 测试辅助 ─────────────────────────────────────────────────────────────

// fakePushStore 实现 PushStore，记录状态更新与清理调用。
type fakePushStore struct {
	notifications []db.Notification
	updates       []string
	cleanups      []string
	failUpdate    bool
}

func (f *fakePushStore) GetPendingNotifications(_ context.Context, limit int) ([]db.Notification, error) {
	if limit != 50 {
		// Python get_pending_notifications(limit=50) 的批大小语义。
		return nil, errors.New("批大小应为 50")
	}
	return f.notifications, nil
}

func (f *fakePushStore) UpdateNotificationStatus(_ context.Context, id int64, status string) error {
	if f.failUpdate {
		return errors.New("update failed")
	}
	f.updates = append(f.updates, strconv.FormatInt(id, 10)+"="+status)
	return nil
}

func (f *fakePushStore) CleanupOldNotifications(context.Context, int) (int64, error) {
	f.cleanups = append(f.cleanups, "notification")
	return 0, nil
}

func (f *fakePushStore) CleanupOldRequests(context.Context, int) (int64, error) {
	f.cleanups = append(f.cleanups, "request")
	return 0, nil
}

// fakePushSender 实现 PushSender，按 chat_id 注入失败。
type fakePushSender struct {
	sent    []sentRecord
	failFor map[int64]string
}

func (f *fakePushSender) SendMessage(_ context.Context, params *bot.SendMessageParams) (*models.Message, error) {
	chatID, _ := params.ChatID.(int64)
	f.sent = append(f.sent, sentRecord{chatID: chatID, text: params.Text, parseMode: params.ParseMode})
	if msg, ok := f.failFor[chatID]; ok {
		return nil, errors.New(msg)
	}
	return &models.Message{ID: 1, Chat: models.Chat{ID: chatID}}, nil
}

// notification 构造测试通知。
func notification(id, userID int64, ntype string, content any) db.Notification {
	return db.Notification{
		ID: id, UserID: userID, NotificationType: ntype,
		Content: content, Status: "pending",
		CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

// mapContent 构造 JSON 对象 content。
func mapContent(kv ...any) map[string]any {
	m := make(map[string]any, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i].(string)] = kv[i+1]
	}
	return m
}

// newTestPushJob 装配被测对象。
func newTestPushJob(store PushStore, sender PushSender) *PushJob {
	return NewPushJob(store, sender, "token:TEST", tg.NopLogger{})
}

// ── 渲染分支 ─────────────────────────────────────────────────────────────

func TestPushUnknownTypeFallback(t *testing.T) {
	store := &fakePushStore{notifications: []db.Notification{
		notification(1, 100, "mystery_type", nil),
	}}
	sender := &fakePushSender{}
	j := newTestPushJob(store, sender)

	if got := j.ProcessPendingNotifications(context.Background()); got != 1 {
		t.Fatalf("成功数=%d, want 1", got)
	}
	if sender.sent[0].text != "您有新的通知" {
		t.Fatalf("未知类型文案不符: %q", sender.sent[0].text)
	}
	// 未知类型默认 Markdown。
	if sender.sent[0].parseMode != models.ParseModeMarkdownV1 {
		t.Fatalf("未知类型应为 Markdown")
	}
	if store.updates[0] != "1=sent" {
		t.Fatalf("应标记 sent: %v", store.updates)
	}
}

func TestPushRequestResultRendering(t *testing.T) {
	store := &fakePushStore{notifications: []db.Notification{
		notification(1, 100, "request_result", mapContent(
			"request_id", float64(42), "channel_id", "https://t.me/durov",
			"message", "总结已生成",
		)),
	}}
	sender := &fakePushSender{}
	j := newTestPushJob(store, sender)
	j.ProcessPendingNotifications(context.Background())

	want := "📝 **请求结果通知**\n\n您的总结请求已处理完成！\n\n" +
		"**请求ID**: 42\n**频道**: `https://t.me/durov`\n\n" +
		"**结果**: 总结已生成\n\n💡 感谢使用！"
	if sender.sent[0].text != want {
		t.Fatalf("request_result 渲染不符:\n got=%q\nwant=%q", sender.sent[0].text, want)
	}
	if sender.sent[0].parseMode != models.ParseModeMarkdownV1 {
		t.Fatalf("request_result 应为 Markdown")
	}
}

func TestPushSummaryPushRendering(t *testing.T) {
	store := &fakePushStore{notifications: []db.Notification{
		notification(1, 100, "summary_push", mapContent(
			"channel_name", "Durov", "summary_preview", "预览内容",
		)),
	}}
	sender := &fakePushSender{}
	j := newTestPushJob(store, sender)
	j.ProcessPendingNotifications(context.Background())

	want := "📬 **新总结通知**\n\n频道 `Durov` 有新的总结发布了！\n\n" +
		"**总结预览**:\n预览内容\n\n💡 查看完整总结，请访问频道。"
	if sender.sent[0].text != want {
		t.Fatalf("summary_push 渲染不符:\n got=%q\nwant=%q", sender.sent[0].text, want)
	}
}

func TestPushSubmissionTypes(t *testing.T) {
	cases := []struct {
		ntype     string
		wantPlain bool
	}{
		{"submission_approved", true},
		{"submission_rejected", true},
		{"submission_pending", false},
	}
	for _, tc := range cases {
		store := &fakePushStore{notifications: []db.Notification{
			notification(1, 100, tc.ntype, mapContent("message", "投稿审核通过啦")),
		}}
		sender := &fakePushSender{}
		j := newTestPushJob(store, sender)
		j.ProcessPendingNotifications(context.Background())

		if sender.sent[0].text != "投稿审核通过啦" {
			t.Fatalf("%s 应取 content.message: %q", tc.ntype, sender.sent[0].text)
		}
		gotPlain := sender.sent[0].parseMode == models.ParseMode("")
		if gotPlain != tc.wantPlain {
			t.Fatalf("%s parse_mode 分支不符 (plain=%v)", tc.ntype, gotPlain)
		}
	}
}

func TestPushSubmissionDefaultMessage(t *testing.T) {
	// content.message 缺失 → "您有新的投稿通知"。
	store := &fakePushStore{notifications: []db.Notification{
		notification(1, 100, "submission_approved", mapContent()),
	}}
	sender := &fakePushSender{}
	j := newTestPushJob(store, sender)
	j.ProcessPendingNotifications(context.Background())
	if sender.sent[0].text != "您有新的投稿通知" {
		t.Fatalf("缺 message 键的兜底文案不符: %q", sender.sent[0].text)
	}
}

// ── 失败路径 ─────────────────────────────────────────────────────────────

func TestPushSendFailureMarksFailed(t *testing.T) {
	store := &fakePushStore{notifications: []db.Notification{
		notification(1, 100, "summary_push", mapContent("channel_name", "A", "summary_preview", "p")),
		notification(2, 200, "summary_push", mapContent("channel_name", "B", "summary_preview", "p")),
	}}
	sender := &fakePushSender{failFor: map[int64]string{
		200: "Forbidden: bot was blocked by the user",
	}}
	j := newTestPushJob(store, sender)

	if got := j.ProcessPendingNotifications(context.Background()); got != 1 {
		t.Fatalf("成功数=%d, want 1", got)
	}
	// 两条通知都处理：1=sent，2=failed（不重试，批次不中断）。
	if len(store.updates) != 2 || store.updates[0] != "1=sent" || store.updates[1] != "2=failed" {
		t.Fatalf("状态更新不符: %v", store.updates)
	}
}

func TestPushNonDictContentMarksFailed(t *testing.T) {
	// content 为字符串（JSON 双解析失败的现状回退）→ AttributeError 语义 → failed。
	store := &fakePushStore{notifications: []db.Notification{
		notification(1, 100, "request_result", "raw-string"),
	}}
	sender := &fakePushSender{}
	j := newTestPushJob(store, sender)

	if got := j.ProcessPendingNotifications(context.Background()); got != 0 {
		t.Fatalf("成功数=%d, want 0", got)
	}
	if len(store.updates) != 1 || store.updates[0] != "1=failed" {
		t.Fatalf("非 dict content 应标 failed: %v", store.updates)
	}
	if len(sender.sent) != 0 {
		t.Fatalf("渲染失败不应发送")
	}
}

func TestPushNoTokenSkips(t *testing.T) {
	store := &fakePushStore{}
	j := NewPushJob(store, &fakePushSender{}, "", tg.NopLogger{})
	if got := j.ProcessPendingNotifications(context.Background()); got != 0 {
		t.Fatalf("无 token 应返回 0")
	}
	if len(store.updates) != 0 {
		t.Fatalf("无 token 不应触碰数据库")
	}
}

func TestPushEmptyQueueStillCleans(t *testing.T) {
	store := &fakePushStore{}
	j := newTestPushJob(store, &fakePushSender{})
	if got := j.ProcessPendingNotifications(context.Background()); got != 0 {
		t.Fatalf("空队列应返回 0")
	}
}

func TestPushUpdateFailureContinuesBatch(t *testing.T) {
	store := &fakePushStore{failUpdate: true, notifications: []db.Notification{
		notification(1, 100, "summary_push", mapContent("channel_name", "A", "summary_preview", "p")),
		notification(2, 101, "summary_push", mapContent("channel_name", "B", "summary_preview", "q")),
	}}
	sender := &fakePushSender{}
	j := newTestPushJob(store, sender)

	// Python：update_notification_status 内部吞异常返回 False，调用点不检查
	// 返回值——逐条计成功、本轮继续处理余下通知（审计 B-3）。
	if got := j.ProcessPendingNotifications(context.Background()); got != 2 {
		t.Fatalf("状态更新失败仍应逐条计成功并继续, got %d", got)
	}
	if len(sender.sent) != 2 {
		t.Fatalf("两条通知都应投递: %d", len(sender.sent))
	}
	if len(store.updates) != 0 {
		t.Fatalf("更新失败不应记录状态: %v", store.updates)
	}
}

func TestPushNullMessageMarksFailed(t *testing.T) {
	store := &fakePushStore{notifications: []db.Notification{
		// content.message 显式 null（键存在但值为 None）。
		notification(1, 100, "submission_approved", mapContent("message", nil)),
	}}
	sender := &fakePushSender{}
	j := newTestPushJob(store, sender)

	// Python：text=None → Bot API 400 → BadRequestError → 标 failed（审计 B-4）。
	if got := j.ProcessPendingNotifications(context.Background()); got != 0 {
		t.Fatalf("null message 应标 failed, got %d", got)
	}
	if len(sender.sent) != 0 {
		t.Fatalf("null message 不应投递")
	}
	if len(store.updates) != 1 || store.updates[0] != "1=failed" {
		t.Fatalf("应标记 failed: %v", store.updates)
	}
}

// ── 清理节流（Tick 层） ──────────────────────────────────────────────────

func TestPushCleanupThrottled(t *testing.T) {
	store := &fakePushStore{}
	sender := &fakePushSender{}
	j := NewPushJob(store, sender, "token:TEST", tg.NopLogger{})
	j.cleanupInterval = time.Hour

	j.Tick(context.Background())
	j.Tick(context.Background())
	if len(store.cleanups) != 2 {
		t.Fatalf("首轮应清理两表: %v", store.cleanups)
	}

	// 推进时钟 1 小时后再次允许清理。
	base := time.Now()
	j.now = func() time.Time { return base.Add(time.Hour + time.Minute) }
	j.Tick(context.Background())
	if len(store.cleanups) != 4 {
		t.Fatalf("节流窗口外应再次清理: %v", store.cleanups)
	}
}

// ── Python 值渲染辅助 ────────────────────────────────────────────────────

func TestPyValueString(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{nil, "None"},
		{"文本", "文本"},
		{true, "True"},
		{false, "False"},
		{float64(42), "42"},
		{float64(1.5), "1.5"},
	}
	for _, tc := range cases {
		if got := pyValueString(tc.in); got != tc.want {
			t.Fatalf("pyValueString(%v)=%q, want %q", tc.in, got, tc.want)
		}
	}
}
