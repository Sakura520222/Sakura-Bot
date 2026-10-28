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
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// ── 测试辅助：fake Editor ────────────────────────────────────────────────

// editRec 记录一次 EditText 调用。
type editRec struct {
	ref  MessageRef
	text string
	mode EditMode
}

// replyRec 记录一次 ReplyText 调用。
type replyRec struct {
	ref  MessageRef
	text string
	mode EditMode
}

// fakeEditor 记录全部编辑/回复调用，可通过 hook 注入失败。
type fakeEditor struct {
	edits     []editRec
	replies   []replyRec
	editHook  func(seq int, rec editRec) error
	replyHook func(seq int, rec replyRec) (*MessageRef, error)
}

func (f *fakeEditor) EditText(_ context.Context, ref MessageRef, text string, mode EditMode) (*MessageRef, error) {
	rec := editRec{ref: ref, text: text, mode: mode}
	f.edits = append(f.edits, rec)
	if f.editHook != nil {
		if err := f.editHook(len(f.edits)-1, rec); err != nil {
			return nil, err
		}
	}
	return &MessageRef{ChatID: ref.ChatID, MessageID: ref.MessageID}, nil
}

func (f *fakeEditor) ReplyText(_ context.Context, ref MessageRef, text string, mode EditMode) (*MessageRef, error) {
	rec := replyRec{ref: ref, text: text, mode: mode}
	f.replies = append(f.replies, rec)
	if f.replyHook != nil {
		return f.replyHook(len(f.replies)-1, rec)
	}
	return &MessageRef{ChatID: ref.ChatID, MessageID: ref.MessageID + 1}, nil
}

// ── 测试辅助：fake clock ─────────────────────────────────────────────────

// fakeClock 注入的单调时钟，测试手动推进。
type fakeClock struct{ now time.Time }

func newFakeClock() *fakeClock { return &fakeClock{now: time.Unix(1700000000, 0)} }

func (c *fakeClock) Now() time.Time { return c.now }

func (c *fakeClock) Advance(d time.Duration) { c.now = c.now.Add(d) }

// chunksOf 构造已填充完毕的 chunk 通道。
func chunksOf(items ...StreamChunk) <-chan StreamChunk {
	ch := make(chan StreamChunk, len(items)+1)
	for _, item := range items {
		ch <- item
	}
	close(ch)
	return ch
}

func textChunks(texts ...string) <-chan StreamChunk {
	items := make([]StreamChunk, 0, len(texts))
	for _, s := range texts {
		items = append(items, StreamChunk{Text: s})
	}
	return chunksOf(items...)
}

var (
	baseRef   = MessageRef{ChatID: 100, MessageID: 1}
	testQuota = QuotaCheck{IsAdmin: false, Remaining: 5}
)

// TestStreamCursorVerbatim 验证光标与新会话前缀逐字对齐（含 U+FE0F 变体选择符）。
func TestStreamCursorVerbatim(t *testing.T) {
	if Cursor != "\u0020\u270D\uFE0F" {
		t.Fatalf("Cursor 必须为空格+U+270D+U+FE0F，实际 %q", Cursor)
	}
	if NewSessionPrefix != "\U0001F343 _开始新的对话。_\n\n" {
		t.Fatalf("NewSessionPrefix 逐字不一致: %q", NewSessionPrefix)
	}
	if streamTruncateLen != 4066 {
		t.Fatalf("流式截断长度应为 4066（4096-30），实际 %d", streamTruncateLen)
	}
}

// TestStreamFirstBlockNewSession 验证首个文本块：新会话前缀拼接、立即编辑、
// __DONE__ 后最终 Markdown 编辑。
func TestStreamFirstBlockNewSession(t *testing.T) {
	ed := &fakeEditor{}
	ok := StreamAndEdit(context.Background(), ed, NopLogger{}, newFakeClock().Now,
		baseRef, textChunks(CtrlNewSession, "你好", CtrlDone), testQuota)
	if !ok {
		t.Fatal("应返回 true")
	}
	if len(ed.replies) != 0 {
		t.Fatalf("不应有回复消息，实际 %d", len(ed.replies))
	}
	if len(ed.edits) != 2 {
		t.Fatalf("应有 2 次编辑（首块+最终），实际 %d", len(ed.edits))
	}
	first, final := ed.edits[0], ed.edits[1]
	if first.text != NewSessionPrefix+"你好"+Cursor {
		t.Fatalf("首块编辑文本不符: %q", first.text)
	}
	if first.mode != EditPlain {
		t.Fatal("首块编辑应为纯文本")
	}
	if final.text != NewSessionPrefix+"你好" || final.mode != EditMarkdown {
		t.Fatalf("最终编辑应为 Markdown 全文: %q mode=%v", final.text, final.mode)
	}
}

// TestStreamThreshold80Runes 验证 80 rune 阈值：79 rune 不触发，第 80 个触发。
func TestStreamThreshold80Runes(t *testing.T) {
	ed := &fakeEditor{}
	clock := newFakeClock()
	chunks := textChunks("a", strings.Repeat("字", 79), "字", CtrlDone)
	ok := StreamAndEdit(context.Background(), ed, NopLogger{}, clock.Now, baseRef, chunks, testQuota)
	if !ok {
		t.Fatal("应返回 true")
	}
	// 期望编辑：首块 "a"+光标；累计 81 rune 时触发一次；最终 Markdown。
	if len(ed.edits) != 3 {
		t.Fatalf("应有 3 次编辑，实际 %d: %+v", len(ed.edits), ed.edits)
	}
	second := ed.edits[1]
	wantLen := 81 + len([]rune(Cursor))
	if got := len([]rune(second.text)); got != wantLen {
		t.Fatalf("阈值触发的编辑文本应为 81 rune+光标=%d，实际 %d", wantLen, got)
	}
	if second.mode != EditPlain {
		t.Fatal("流式编辑应为纯文本")
	}
	if second.text != "a"+strings.Repeat("字", 80)+Cursor {
		t.Fatalf("阈值编辑文本累积顺序不符: %q", second.text)
	}
}

// TestStreamTimeInterval 验证 1.5s 时间阈值：字符不足也会触发编辑。
func TestStreamTimeInterval(t *testing.T) {
	ed := &fakeEditor{}
	clock := newFakeClock()
	ch := make(chan StreamChunk, 4)
	ch <- StreamChunk{Text: "hello"}
	go func() {
		// 消费端读走首块后再推进时钟并投递下一块
		time.Sleep(10 * time.Millisecond)
		clock.Advance(StreamEditInterval)
		ch <- StreamChunk{Text: "world"}
		close(ch)
	}()
	ok := StreamAndEdit(context.Background(), ed, NopLogger{}, clock.Now, baseRef, ch, testQuota)
	if !ok {
		t.Fatal("应返回 true")
	}
	if len(ed.edits) != 3 {
		t.Fatalf("时间阈值应触发一次额外编辑（共 3 次），实际 %d", len(ed.edits))
	}
	if ed.edits[1].text != "helloworld"+Cursor {
		t.Fatalf("时间阈值编辑文本不符: %q", ed.edits[1].text)
	}
}

// TestStreamErrorControl 验证 __ERROR__: 前缀：编辑错误文案、返回 false、不再 flush。
func TestStreamErrorControl(t *testing.T) {
	ed := &fakeEditor{}
	ok := StreamAndEdit(context.Background(), ed, NopLogger{}, newFakeClock().Now,
		baseRef, textChunks(CtrlErrorPrefix+"模型超载"), testQuota)
	if ok {
		t.Fatal("__ERROR__ 路径应返回 false")
	}
	if len(ed.edits) != 1 || ed.edits[0].text != "模型超载" || ed.edits[0].mode != EditPlain {
		t.Fatalf("应仅编辑一次错误文案（纯文本）: %+v", ed.edits)
	}
}

// TestStreamQuotaHint 验证配额提示：非管理员且剩余 <=1 时追加到最终文本。
func TestStreamQuotaHint(t *testing.T) {
	cases := []struct {
		name    string
		quota   QuotaCheck
		wantEnd string
	}{
		{"剩余1次", QuotaCheck{IsAdmin: false, Remaining: 1}, "\n\n_💡 提示：今日剩余查询次数：1 次_"},
		{"剩余0次", QuotaCheck{IsAdmin: false, Remaining: 0}, "\n\n_💡 提示：今日剩余查询次数：0 次_"},
		{"剩余充足", QuotaCheck{IsAdmin: false, Remaining: 5}, ""},
		{"管理员剩余1", QuotaCheck{IsAdmin: true, Remaining: 1}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ed := &fakeEditor{}
			StreamAndEdit(context.Background(), ed, NopLogger{}, newFakeClock().Now,
				baseRef, textChunks("回答内容", CtrlDone), tc.quota)
			final := ed.edits[len(ed.edits)-1]
			want := "回答内容" + tc.wantEnd
			if final.text != want {
				t.Fatalf("最终文本 = %q, want %q", final.text, want)
			}
		})
	}
}

// TestStreamFinalSplitLong 验证最终 flush 的贪心分片：首片编辑、其余逐片回复 Markdown。
func TestStreamFinalSplitLong(t *testing.T) {
	ed := &fakeEditor{}
	p := strings.Repeat("段", 2000)
	chunks := textChunks(p, "\n\n"+p, "\n\n"+p, CtrlDone)
	ok := StreamAndEdit(context.Background(), ed, NopLogger{}, newFakeClock().Now, baseRef, chunks, testQuota)
	if !ok {
		t.Fatal("应返回 true")
	}
	// 3×2000 + 2×"\n\n" = 6004 rune > 4096：分片为 [p1+"\n\n"+p2, p3]
	final := ed.edits[len(ed.edits)-1]
	if final.mode != EditMarkdown || final.text != p+"\n\n"+p {
		t.Fatalf("首片应为 Markdown 编辑的 p1+p2，实际 mode=%v len=%d", final.mode, len([]rune(final.text)))
	}
	if len(ed.replies) != 1 {
		t.Fatalf("应追加 1 条回复消息，实际 %d", len(ed.replies))
	}
	if ed.replies[0].text != p || ed.replies[0].mode != EditMarkdown {
		t.Fatalf("第二分片应为 Markdown 回复的 p3: %+v", ed.replies[0])
	}
}

// TestStreamMarkdownFallbackChain 验证最终编辑失败链：Markdown → 修复 Markdown → 纯文本。
func TestStreamMarkdownFallbackChain(t *testing.T) {
	ed := &fakeEditor{}
	// 首块纯文本成功；一切 Markdown 编辑失败；纯文本成功
	ed.editHook = func(seq int, rec editRec) error {
		if rec.mode == EditMarkdown {
			return fmt.Errorf("Bad Request: can't parse entities")
		}
		return nil
	}
	ok := StreamAndEdit(context.Background(), ed, NopLogger{}, newFakeClock().Now,
		baseRef, textChunks("内容**", CtrlDone), testQuota)
	if !ok {
		t.Fatal("编辑降级不影响返回值，应仍为 true")
	}
	// 期望 4 次：首块纯文本；最终 Markdown（失败）；修复 Markdown（失败）；纯文本（成功）
	if len(ed.edits) != 4 {
		t.Fatalf("应有 4 次编辑，实际 %d: %+v", len(ed.edits), ed.edits)
	}
	if ed.edits[1].text != "内容**" || ed.edits[1].mode != EditMarkdown {
		t.Fatalf("第 2 次应为原文 Markdown: %+v", ed.edits[1])
	}
	if ed.edits[2].text != FixMarkdown("内容**") || ed.edits[2].mode != EditMarkdown {
		t.Fatalf("第 3 次应为修复后 Markdown: %+v", ed.edits[2])
	}
	if ed.edits[3].text != "内容**" || ed.edits[3].mode != EditPlain {
		t.Fatalf("第 4 次应为原文纯文本兜底: %+v", ed.edits[3])
	}
}

// TestStreamNotModifiedSilence 验证 "message is not modified" 静默且不重试。
func TestStreamNotModifiedSilence(t *testing.T) {
	ed := &fakeEditor{}
	ed.editHook = func(seq int, rec editRec) error {
		return errors.New("Bad Request: message is not modified")
	}
	ok := StreamAndEdit(context.Background(), ed, NopLogger{}, newFakeClock().Now,
		baseRef, textChunks("x", CtrlDone), testQuota)
	if !ok {
		t.Fatal("静默路径不影响返回值")
	}
	// 每次编辑都静默失败且无降级重试：首块 1 次 + 最终 1 次
	if len(ed.edits) != 2 {
		t.Fatalf("not modified 应静默且不重试（期望 2 次调用），实际 %d", len(ed.edits))
	}
}

// TestStreamInterruptedWithContent 验证流中断但已有内容：保留内容最终编辑、返回 false。
func TestStreamInterruptedWithContent(t *testing.T) {
	ed := &fakeEditor{}
	chunks := chunksOf(StreamChunk{Text: "部分内容"}, StreamChunk{Err: errors.New("连接中断")})
	ok := StreamAndEdit(context.Background(), ed, NopLogger{}, newFakeClock().Now, baseRef, chunks, testQuota)
	if ok {
		t.Fatal("流中断应返回 false（调用方退款）")
	}
	last := ed.edits[len(ed.edits)-1]
	if last.text != "部分内容" || last.mode != EditMarkdown {
		t.Fatalf("中断后应保留内容做最终 Markdown 编辑: %+v", last)
	}
}

// TestStreamInterruptedEmpty 验证流中断且无内容：编辑错误文案并返回 false。
func TestStreamInterruptedEmpty(t *testing.T) {
	ed := &fakeEditor{}
	chunks := chunksOf(StreamChunk{Err: errors.New("boom")})
	ok := StreamAndEdit(context.Background(), ed, NopLogger{}, newFakeClock().Now, baseRef, chunks, testQuota)
	if ok {
		t.Fatal("应返回 false")
	}
	if len(ed.edits) != 1 || ed.edits[0].text != ErrQueryFailedText || ed.edits[0].mode != EditPlain {
		t.Fatalf("应仅编辑一次错误文案: %+v", ed.edits)
	}
}

// TestStreamEmptyResult 验证控制标记后无正文：累积为空时兜底文案。
func TestStreamEmptyResult(t *testing.T) {
	ed := &fakeEditor{}
	ok := StreamAndEdit(context.Background(), ed, NopLogger{}, newFakeClock().Now,
		baseRef, textChunks(CtrlNewSession), testQuota)
	if ok {
		t.Fatal("空结果应返回 false")
	}
	if len(ed.edits) != 1 || ed.edits[0].text != ErrNoAnswerText {
		t.Fatalf("应仅编辑一次兜底文案: %+v", ed.edits)
	}
}

// TestReplyWithFallbackChain 验证 _send_with_fallback 等价链：Markdown → 修复 → 纯文本。
func TestReplyWithFallbackChain(t *testing.T) {
	ed := &fakeEditor{}
	ed.replyHook = func(seq int, rec replyRec) (*MessageRef, error) {
		if rec.mode == EditMarkdown {
			return nil, fmt.Errorf("can't parse entities")
		}
		return &MessageRef{ChatID: rec.ref.ChatID, MessageID: 9}, nil
	}
	ref, err := ReplyWithFallback(context.Background(), ed, NopLogger{}, baseRef, "a **b")
	if err != nil || ref == nil {
		t.Fatalf("纯文本兜底应成功: %v", err)
	}
	if len(ed.replies) != 3 {
		t.Fatalf("应有 3 次回复，实际 %d", len(ed.replies))
	}
	if ed.replies[1].text != "a **b**" || ed.replies[1].mode != EditMarkdown {
		t.Fatalf("第 2 次应为修复后 Markdown: %+v", ed.replies[1])
	}
	if ed.replies[2].text != "a **b" || ed.replies[2].mode != EditPlain {
		t.Fatalf("第 3 次应为纯文本兜底: %+v", ed.replies[2])
	}
}

// TestReplyWithFallbackSuccess 验证 Markdown 一次成功时不重试。
func TestReplyWithFallbackSuccess(t *testing.T) {
	ed := &fakeEditor{}
	ref, err := ReplyWithFallback(context.Background(), ed, NopLogger{}, baseRef, "正常**文本**")
	if err != nil || ref == nil {
		t.Fatalf("不应失败: %v", err)
	}
	if len(ed.replies) != 1 || ed.replies[0].mode != EditMarkdown {
		t.Fatalf("应仅一次 Markdown 回复: %+v", ed.replies)
	}
}
