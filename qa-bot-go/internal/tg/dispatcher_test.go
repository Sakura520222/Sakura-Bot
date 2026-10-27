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
	"context"
	"strings"
	"testing"

	"github.com/go-telegram/bot/models"
)

// dispatcher_test.go：分发链的纯逻辑测试——链序与短路、^ap: 过滤器、
// panic 兜底与 update 摘要（README 硬规则 1 / 对抗评审 B-4 的回归防线）。

// fakeLayer 记录调用次序，并可注入返回值与 panic 注入点。
type fakeLayer struct {
	name string
	// calls 指向共享的调用记录切片。
	calls *[]string
	// retTrue 控制 Handle*/Route 系列方法的返回值（Route 无返回值）。
	retTrue bool
	// panicAt 指定在哪个方法 panic（"" 表示不 panic）。
	panicAt string
}

func (f *fakeLayer) note(method string) {
	*f.calls = append(*f.calls, f.name+"."+method)
	if f.panicAt == method {
		panic("boom:" + f.name + ":" + method)
	}
}

// HandleCallback 实现 AppealLayer。
func (f *fakeLayer) HandleCallback(ctx context.Context, upd *models.Update) bool {
	f.note("callback")
	return f.retTrue
}

// HandleMessage 实现 AppealLayer。
func (f *fakeLayer) HandleMessage(ctx context.Context, upd *models.Update) bool {
	f.note("message")
	return f.retTrue
}

// HandleUpdate 实现 SubmissionLayer。
func (f *fakeLayer) HandleUpdate(ctx context.Context, upd *models.Update) bool {
	f.note("submission")
	return f.retTrue
}

// Route 实现 CommandLayer。
func (f *fakeLayer) Route(ctx context.Context, upd *models.Update) {
	f.note("route")
}

// newTestDispatcher 构造三假层分发器与共享调用记录。
func newTestDispatcher(retTrue bool) (*Dispatcher, *[]string, *fakeLayer, *fakeLayer, *fakeLayer) {
	calls := []string{}
	appeal := &fakeLayer{name: "appeal", calls: &calls, retTrue: retTrue}
	submission := &fakeLayer{name: "submission", calls: &calls, retTrue: retTrue}
	commands := &fakeLayer{name: "commands", calls: &calls, retTrue: retTrue}
	d := NewDispatcher(NopLogger{})
	d.Appeal = appeal
	d.Submission = submission
	d.Commands = commands
	return d, &calls, appeal, submission, commands
}

// msgUpdate 构造带文本的 message update。
func msgUpdate(text string) *models.Update {
	return &models.Update{Message: &models.Message{
		Text: text,
	}}
}

// TestDispatcherChainOrderAllFallThrough 验证无层短路时按链序依次调用：
// 申诉消息层 → 投稿 FSM → 命令层。
func TestDispatcherChainOrderAllFallThrough(t *testing.T) {
	d, calls, _, _, _ := newTestDispatcher(false)
	d.HandleUpdate(context.Background(), msgUpdate("你好"))
	want := "appeal.message,submission.submission,commands.route"
	if got := strings.Join(*calls, ","); got != want {
		t.Fatalf("链序不符: got %q, want %q", got, want)
	}
}

// TestDispatcherAppealMessageShortCircuits 验证申诉消息层消费后不再下传。
func TestDispatcherAppealMessageShortCircuits(t *testing.T) {
	d, calls, _, _, _ := newTestDispatcher(true)
	d.HandleUpdate(context.Background(), msgUpdate("/appeal"))
	want := "appeal.message"
	if got := strings.Join(*calls, ","); got != want {
		t.Fatalf("申诉层应短路: got %q, want %q", got, want)
	}
}

// TestDispatcherSubmissionShortCircuits 验证申诉层放行、投稿 FSM 消费。
func TestDispatcherSubmissionShortCircuits(t *testing.T) {
	calls2 := []string{}
	appeal := &fakeLayer{name: "appeal", calls: &calls2, retTrue: false}
	submission := &fakeLayer{name: "submission", calls: &calls2, retTrue: true}
	d2 := NewDispatcher(NopLogger{})
	d2.Appeal = appeal
	d2.Submission = submission
	d2.HandleUpdate(context.Background(), msgUpdate("/submit"))
	want := "appeal.message,submission.submission"
	if got := strings.Join(calls2, ","); got != want {
		t.Fatalf("投稿 FSM 应短路: got %q, want %q", got, want)
	}
}

// TestDispatcherAppealCallbackRoutedToCallbackLayer 验证 ^ap: 回调走
// callback 入口，且消费后不再进入 message/submission/route。
func TestDispatcherAppealCallbackRoutedToCallbackLayer(t *testing.T) {
	d, calls, _, _, _ := newTestDispatcher(true)
	upd := &models.Update{CallbackQuery: &models.CallbackQuery{
		Data: "ap:list:cases:0",
	}}
	d.HandleUpdate(context.Background(), upd)
	want := "appeal.callback"
	if got := strings.Join(*calls, ","); got != want {
		t.Fatalf("^ap: 回调应走申诉回调层并短路: got %q, want %q", got, want)
	}
}

// TestDispatcherNonAppealCallbackSkipsCallbackLayer 验证非 ^ap: 回调
// （如投稿 ch_ 按钮）不触发申诉回调层，继续走 message/submission 层。
func TestDispatcherNonAppealCallbackSkipsCallbackLayer(t *testing.T) {
	d, calls, _, _, _ := newTestDispatcher(false)
	upd := &models.Update{CallbackQuery: &models.CallbackQuery{
		Data: "ch_0",
	}}
	d.HandleUpdate(context.Background(), upd)
	got := strings.Join(*calls, ",")
	if strings.Contains(got, "appeal.callback") {
		t.Fatalf("非 ^ap: 回调不应触发申诉回调层: %q", got)
	}
	want := "appeal.message,submission.submission,commands.route"
	if got != want {
		t.Fatalf("链序不符: got %q, want %q", got, want)
	}
}

// TestDispatcherRecoversPanicKeepsServing 验证单层 panic 不杀进程、
// 后续 update 照常处理（B-4 回归用例）。
func TestDispatcherRecoversPanicKeepsServing(t *testing.T) {
	d, calls, appeal, _, _ := newTestDispatcher(true)
	appeal.panicAt = "message"

	// 首条 update 触发 panic——HandleUpdate 自身不得 panic 外泄。
	d.HandleUpdate(context.Background(), msgUpdate("第一条"))
	if got := strings.Join(*calls, ","); got != "appeal.message" {
		t.Fatalf("panic 前应已进入申诉层: %q", got)
	}

	// 后续 update 照常处理。
	*calls = nil
	d.HandleUpdate(context.Background(), msgUpdate("第二条"))
	if got := strings.Join(*calls, ","); got != "appeal.message" {
		t.Fatalf("panic 后进程应继续服务: %q", got)
	}
}

// TestInFlightCounter 验证在途计数在处理完成后归零。
func TestInFlightCounter(t *testing.T) {
	d, _, _, _, _ := newTestDispatcher(false)
	if d.InFlight() != 0 {
		t.Fatalf("初始在途数应为 0: %d", d.InFlight())
	}
	d.HandleUpdate(context.Background(), msgUpdate("x"))
	if d.InFlight() != 0 {
		t.Fatalf("处理完成后在途数应归零: %d", d.InFlight())
	}
}

// TestIsAppealCallbackData 验证 ^ap: 前缀过滤器边界。
func TestIsAppealCallbackData(t *testing.T) {
	cases := []struct {
		data string
		want bool
	}{
		{"ap:list:cases:0", true},
		{"ap:", true},
		{"ap", false},
		{"ch_0", false},
		{"", false},
		{"AP:list", false}, // Python re ^ap: 大小写敏感
	}
	for _, c := range cases {
		if got := IsAppealCallbackData(c.data); got != c.want {
			t.Fatalf("IsAppealCallbackData(%q) = %v, want %v", c.data, got, c.want)
		}
	}
}

// TestUpdateJSONSummary 验证摘要生成与 rune 截断。
func TestUpdateJSONSummary(t *testing.T) {
	upd := msgUpdate("你好世界")
	summary := UpdateJSONSummary(upd, 0)
	if !strings.Contains(summary, "你好世界") {
		t.Fatalf("摘要应包含原文: %q", summary)
	}

	// 截断：按 rune 计数，中文不出现半字符乱码。
	truncated := UpdateJSONSummary(upd, 5)
	runes := []rune(strings.TrimSuffix(truncated, "…"))
	if len(runes) > 5 {
		t.Fatalf("截断后应 ≤5 rune: %q (%d)", truncated, len(runes))
	}

	if UpdateJSONSummary(nil, 10) != "" {
		t.Fatalf("nil update 摘要应为空串")
	}
}
