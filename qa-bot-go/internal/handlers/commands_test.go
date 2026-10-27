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

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/contracts"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/db"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/engine"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/qauser"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/tg"
)

// ── 测试辅助：fake TG 客户端 ─────────────────────────────────────────────

// sentRecord 记录一次 SendMessage。
type sentRecord struct {
	chatID    int64
	text      string
	parseMode models.ParseMode
	keyboard  bool
}

// fakeTG 实现 TGClient（发送 + 编辑），记录全部调用。
type fakeTG struct {
	sent   []sentRecord
	edits  []string
	seq    int
	sendOK bool // false 时 SendMessage 返回错误
}

func (f *fakeTG) SendMessage(_ context.Context, params *bot.SendMessageParams) (*models.Message, error) {
	f.seq++
	chatID, _ := params.ChatID.(int64)
	f.sent = append(f.sent, sentRecord{
		chatID:    chatID,
		text:      params.Text,
		parseMode: params.ParseMode,
		keyboard:  params.ReplyMarkup != nil,
	})
	if !f.sendOK {
		return nil, context.DeadlineExceeded
	}
	return &models.Message{ID: f.seq, Chat: models.Chat{ID: chatID}}, nil
}

func (f *fakeTG) EditText(_ context.Context, ref tg.MessageRef, text string, _ tg.EditMode) (*tg.MessageRef, error) {
	f.edits = append(f.edits, text)
	return &tg.MessageRef{ChatID: ref.ChatID, MessageID: ref.MessageID}, nil
}

func (f *fakeTG) ReplyText(_ context.Context, ref tg.MessageRef, text string, _ tg.EditMode) (*tg.MessageRef, error) {
	f.seq++
	return &tg.MessageRef{ChatID: ref.ChatID, MessageID: f.seq}, nil
}

// fakeQuota 实现 QuotaManager。
type fakeQuota struct {
	result contracts.QuotaCheckResult
	usage  UsageStatus
	refund int64
}

func (f *fakeQuota) CheckQuota(context.Context, int64) contracts.QuotaCheckResult { return f.result }

func (f *fakeQuota) RefundQuota(context.Context, int64) bool {
	f.refund++
	return true
}

func (f *fakeQuota) GetUsageStatus(context.Context, int64) UsageStatus { return f.usage }

// fakeSessions 实现 Sessions。
type fakeSessions struct {
	info    *engine.SessionInfo
	deleted int64
}

func (f *fakeSessions) GetSessionInfo(context.Context, int64) *engine.SessionInfo { return f.info }
func (f *fakeSessions) ClearUserHistory(context.Context, int64, string) int64 {
	return f.deleted
}

// fakeEngine 实现 StreamEngine：把预设块经通道下发后关闭。
type fakeEngine struct {
	chunks []engine.Chunk
}

func (f *fakeEngine) ProcessQueryStream(context.Context, string, int64, string) <-chan engine.Chunk {
	ch := make(chan engine.Chunk, len(f.chunks)+1)
	for _, c := range f.chunks {
		ch <- c
	}
	close(ch)
	return ch
}

// fakePersona 实现 PersonaProvider。
type fakePersona struct{ value string }

func (f fakePersona) Persona() string { return f.value }

// fakeSubmissions 记录清理调用。
type fakeSubmissions struct{ cleared []int64 }

func (f *fakeSubmissions) ClearUserState(userID int64) { f.cleared = append(f.cleared, userID) }

// ── 测试辅助：update 构造 ────────────────────────────────────────────────

// qaCommandUpdate 构造带 bot_command 实体的消息更新。
func qaCommandUpdate(text string, userID int64) *models.Update {
	return &models.Update{
		Message: &models.Message{
			ID:   1,
			From: &models.User{ID: userID, Username: "tester", FirstName: "Test"},
			Chat: models.Chat{ID: userID},
			Text: text,
			Entities: []models.MessageEntity{{
				Type: models.MessageEntityTypeBotCommand, Offset: 0,
				Length: len(strings.Fields(text)[0]),
			}},
		},
	}
}

// qaTextUpdate 构造普通文本更新（无实体）。
func qaTextUpdate(text string, userID int64) *models.Update {
	return &models.Update{
		Message: &models.Message{
			ID:   1,
			From: &models.User{ID: userID, FirstName: "Test"},
			Chat: models.Chat{ID: userID},
			Text: text,
		},
	}
}

// newTestCommands 装配被测对象。
func newTestCommands(ftg *fakeTG, fq *fakeQuota, fs *fakeSessions, fe *fakeEngine) *Commands {
	return NewCommands(CommandsDeps{
		TG:          ftg,
		Users:       qauser.New(&fakeUserStore{}),
		Quota:       fq,
		Sessions:    fs,
		Engine:      fe,
		Persona:     fakePersona{value: "测试人格"},
		Submissions: &fakeSubmissions{},
	})
}

// ── 测试辅助：fake 用户系统存储 ──────────────────────────────────────────

// fakeUserStore 实现 qauser.Store：目录含两个频道；请求创建恒返回 id=7。
type fakeUserStore struct{}

func (f *fakeUserStore) IsUserRegistered(context.Context, int64) (bool, error) {
	return false, nil
}

func (f *fakeUserStore) UpdateUserActivity(context.Context, int64) error { return nil }

func (f *fakeUserStore) RegisterUser(_ context.Context, _ int64, _, _ *string, _ bool) error {
	return nil
}

func (f *fakeUserStore) GetAllChannels(context.Context) ([]contracts.ChannelInfo, error) {
	return []contracts.ChannelInfo{
		{ChannelID: "https://t.me/durov", ChannelName: "Durov 频道"},
		{ChannelID: "https://t.me/other", ChannelName: "Other 频道"},
	}, nil
}

func (f *fakeUserStore) IsSubscribed(context.Context, int64, string, string) (bool, error) {
	return false, nil
}

func (f *fakeUserStore) AddSubscription(context.Context, int64, string, string, string) (bool, error) {
	return true, nil
}

func (f *fakeUserStore) RemoveSubscription(context.Context, int64, string, string) (int64, error) {
	return 1, nil
}

func (f *fakeUserStore) GetUserSubscriptions(context.Context, int64, string) ([]db.Subscription, error) {
	return nil, nil
}

func (f *fakeUserStore) CreateRequest(context.Context, string, int64, string, map[string]any) (int64, error) {
	return 7, nil
}

func (f *fakeUserStore) GetRequestStatus(context.Context, int64) (*db.Request, error) {
	return nil, nil
}

// ── parseCommand ─────────────────────────────────────────────────────────

func TestParseCommand(t *testing.T) {
	cases := []struct {
		name  string
		text  string
		isCmd bool
		want  string
		argsN int
		noEnt bool
	}{
		{name: "基础", text: "/start", isCmd: true, want: "start"},
		{name: "带参数", text: "/ask @ch 问题 内容", isCmd: true, want: "ask", argsN: 3},
		{name: "botname 后缀", text: "/start@MyBot", isCmd: true, want: "start"},
		{name: "大写", text: "/START", isCmd: true, want: "start"},
		{name: "多空白参数", text: "/ask  @ch   问题", isCmd: true, want: "ask", argsN: 2},
		{name: "无实体斜杠", text: "/notcmd", noEnt: true},
		{name: "普通文本", text: "你好", noEnt: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			upd := qaTextUpdate(tc.text, 1)
			if !tc.noEnt {
				upd = qaCommandUpdate(tc.text, 1)
			}
			cmd, args, ok := parseCommand(upd.Message)
			if ok != tc.isCmd {
				t.Fatalf("isCmd=%v, want %v", ok, tc.isCmd)
			}
			if cmd != tc.want {
				t.Fatalf("cmd=%q, want %q", cmd, tc.want)
			}
			if len(args) != tc.argsN {
				t.Fatalf("args=%v, want %d 个", args, tc.argsN)
			}
		})
	}
}

// ── Route 分发 ───────────────────────────────────────────────────────────

func TestRouteStart(t *testing.T) {
	ftg := &fakeTG{sendOK: true}
	c := newTestCommands(ftg, &fakeQuota{}, &fakeSessions{}, &fakeEngine{})
	c.Route(context.Background(), qaCommandUpdate("/start", 42))

	if len(ftg.sent) != 1 {
		t.Fatalf("应发送 1 条欢迎消息，实发 %d", len(ftg.sent))
	}
	got := ftg.sent[0]
	if !strings.HasPrefix(got.text, "🤖 **你好！我是你的智能助手。**") {
		t.Fatalf("欢迎语文案不符: %q", got.text[:30])
	}
	if got.parseMode != models.ParseModeMarkdownV1 || !got.keyboard {
		t.Fatalf("/start 应为 Markdown + 主键盘")
	}
}

func TestRouteUnknownCommandIgnored(t *testing.T) {
	ftg := &fakeTG{sendOK: true}
	c := newTestCommands(ftg, &fakeQuota{}, &fakeSessions{}, &fakeEngine{})
	c.Route(context.Background(), qaCommandUpdate("/nope", 42))
	if len(ftg.sent) != 0 {
		t.Fatalf("未知命令应静默丢弃")
	}
}

func TestRoutePlainTextGoesToAI(t *testing.T) {
	ftg := &fakeTG{sendOK: true}
	fq := &fakeQuota{result: contracts.QuotaCheckResult{Allowed: true, Remaining: 5}}
	c := newTestCommands(ftg, fq, &fakeSessions{}, &fakeEngine{chunks: []engine.Chunk{
		engine.TextChunk("回答"),
		engine.CtrlChunk(contracts.CtrlDone),
	}})
	c.Route(context.Background(), qaTextUpdate("你好", 42))

	// 占位 + 流式最终编辑。
	var placeholder string
	for _, s := range ftg.sent {
		if s.text == "💭 正在思考..." {
			placeholder = s.text
		}
	}
	if placeholder == "" {
		t.Fatalf("应发送占位消息")
	}
	if fq.refund != 0 {
		t.Fatalf("成功流式不应退款")
	}
	if len(ftg.edits) == 0 || ftg.edits[len(ftg.edits)-1] != "回答" {
		t.Fatalf("最终编辑应为完整回答: %v", ftg.edits)
	}
}

// ── /status ──────────────────────────────────────────────────────────────

func TestStatusNormal(t *testing.T) {
	ftg := &fakeTG{sendOK: true}
	fq := &fakeQuota{usage: UsageStatus{UsedToday: 3, Remaining: 7}}
	fs := &fakeSessions{info: &engine.SessionInfo{
		SessionID: "abcd1234ef567890", MessageCount: 5, IsActive: true,
	}}
	c := newTestCommands(ftg, fq, fs, &fakeEngine{})
	c.Status(context.Background(), qaCommandUpdate("/status", 42))

	want := "📊 <b>系统状态</b>\n\n" +
		"📊 <b>配额状态</b>\n\n• 今日已使用: 3 次\n• 剩余次数: 7 次" +
		"\n\n🧠 <b>当前会话状态</b>\n• 会话ID: <code>abcd1234...</code>\n• 消息数: 5 条\n• 状态: 🟢 活跃中" +
		"\n\n📅 重置时间：每日 00:00 (UTC)"
	if len(ftg.sent) != 1 || ftg.sent[0].text != want {
		t.Fatalf("/status 渲染不符:\n got=%q\nwant=%q", ftg.sent[0].text, want)
	}
	if ftg.sent[0].parseMode != models.ParseModeHTML || !ftg.sent[0].keyboard {
		t.Fatalf("/status 应为 HTML + 主键盘")
	}
}

func TestStatusAdmin(t *testing.T) {
	ftg := &fakeTG{sendOK: true}
	fq := &fakeQuota{usage: UsageStatus{IsAdmin: true, TotalUsedToday: 42}}
	c := newTestCommands(ftg, fq, &fakeSessions{}, &fakeEngine{})
	c.Status(context.Background(), qaCommandUpdate("/status", 42))

	want := "📊 <b>系统状态</b>\n\n" +
		"🌟 <b>管理员状态</b>\n\n你拥有无限制访问的特权。\n\n📊 今日总使用：42次" +
		"\n\n📅 重置时间：每日 00:00 (UTC)"
	if ftg.sent[0].text != want {
		t.Fatalf("管理员 /status 渲染不符:\n got=%q\nwant=%q", ftg.sent[0].text, want)
	}
}

func TestStatusError(t *testing.T) {
	ftg := &fakeTG{sendOK: true}
	fq := &fakeQuota{usage: UsageStatus{Err: "boom"}}
	c := newTestCommands(ftg, fq, &fakeSessions{}, &fakeEngine{})
	c.Status(context.Background(), qaCommandUpdate("/status", 42))

	if len(ftg.sent) != 1 || ftg.sent[0].text != "❌ 获取状态失败，请稍后再试。" {
		t.Fatalf("错误路径文案不符: %+v", ftg.sent)
	}
	if ftg.sent[0].parseMode != "" || ftg.sent[0].keyboard {
		t.Fatalf("错误路径应为纯文本且不带键盘")
	}
}

// ── /clear 与 /view_persona ──────────────────────────────────────────────

func TestClear(t *testing.T) {
	ftg := &fakeTG{sendOK: true}
	fs := &fakeSessions{deleted: 12}
	c := newTestCommands(ftg, &fakeQuota{}, fs, &fakeEngine{})
	c.Clear(context.Background(), qaCommandUpdate("/clear", 42))

	want := "🗑️ **对话记忆已清除**\n\n已清除 **12** 条对话记录。\n\n现在，我们的对话是全新的开始。有什么可以帮你的吗？"
	if ftg.sent[0].text != want || ftg.sent[0].parseMode != models.ParseModeMarkdownV1 {
		t.Fatalf("/clear 文案/parse_mode 不符: %+v", ftg.sent[0])
	}
}

func TestViewPersonaTruncatesAt3500Runes(t *testing.T) {
	ftg := &fakeTG{sendOK: true}
	long := strings.Repeat("字", 3501)
	c := NewCommands(CommandsDeps{
		TG: ftg, Users: qauser.New(&fakeUserStore{}), Quota: &fakeQuota{}, Sessions: &fakeSessions{},
		Engine: &fakeEngine{}, Persona: fakePersona{value: long}, Submissions: &fakeSubmissions{},
	})
	c.ViewPersona(context.Background(), qaCommandUpdate("/view_persona", 42))

	got := ftg.sent[0].text
	if !strings.Contains(got, "... (内容过长，已截断)") {
		t.Fatalf("超长人格应包含截断后缀")
	}
	// ``` 包裹的 preview 应恰为前 3500 字 + 后缀。
	start := strings.Index(got, "```\n") + 4
	end := strings.Index(got[start:], "\n```") + start
	wantPreview := strings.Repeat("字", 3500) + "\n\n... (内容过长，已截断)"
	if preview := got[start:end]; preview != wantPreview {
		t.Fatalf("截断预览不符: 长度=%d", len([]rune(preview)))
	}
}

// ── /ask ─────────────────────────────────────────────────────────────────

func TestAskUsageWithoutTwoArgs(t *testing.T) {
	ftg := &fakeTG{sendOK: true}
	c := newTestCommands(ftg, &fakeQuota{}, &fakeSessions{}, &fakeEngine{})
	c.Ask(context.Background(), qaCommandUpdate("/ask @ch", 42))

	if len(ftg.sent) != 1 || !strings.HasPrefix(ftg.sent[0].text, "🔎 <b>指定频道查询</b>") {
		t.Fatalf("/ask 参数不足应回复用法")
	}
	if ftg.sent[0].parseMode != models.ParseModeHTML || ftg.sent[0].keyboard {
		t.Fatalf("/ask 用法应为 HTML 且不带键盘")
	}
}

func TestAskQuotaRefusal(t *testing.T) {
	ftg := &fakeTG{sendOK: true}
	fq := &fakeQuota{result: contracts.QuotaCheckResult{Allowed: false, Message: "⏰ **今日配额已用完**"}}
	c := newTestCommands(ftg, fq, &fakeSessions{}, &fakeEngine{})
	c.Ask(context.Background(), qaCommandUpdate("/ask @ch 问题", 42))

	if len(ftg.sent) != 1 || ftg.sent[0].text != "⏰ **今日配额已用完**" {
		t.Fatalf("配额拒绝应回 message 字段: %+v", ftg.sent)
	}
	if fq.refund != 0 {
		t.Fatalf("拒绝路径不应退款")
	}
}

func TestAskPlaceholderAndRefundOnError(t *testing.T) {
	ftg := &fakeTG{sendOK: true}
	fq := &fakeQuota{result: contracts.QuotaCheckResult{Allowed: true, Remaining: 5}}
	c := newTestCommands(ftg, fq, &fakeSessions{}, &fakeEngine{chunks: []engine.Chunk{
		engine.ErrChunk("boom"),
	}})
	c.Ask(context.Background(), qaCommandUpdate("/ask @ch 问题", 42))

	found := false
	for _, s := range ftg.sent {
		if s.text == "💭 正在思考（限定频道）..." {
			found = true
		}
	}
	if !found {
		t.Fatalf("应发送限定频道占位文案")
	}
	if fq.refund != 1 {
		t.Fatalf("__ERROR__ 路径应退款一次，实际 %d", fq.refund)
	}
}

// ── handle_message ───────────────────────────────────────────────────────

func TestHandleMessageBlankIgnored(t *testing.T) {
	ftg := &fakeTG{sendOK: true}
	fq := &fakeQuota{}
	c := newTestCommands(ftg, fq, &fakeSessions{}, &fakeEngine{})
	c.HandleMessage(context.Background(), qaTextUpdate("   ", 42))
	if len(ftg.sent) != 0 || fq.refund != 0 {
		t.Fatalf("空白文本应直接返回")
	}
}

func TestHandleMessageRefundOnError(t *testing.T) {
	ftg := &fakeTG{sendOK: false} // 占位消息发送失败 → 异常路径
	fq := &fakeQuota{result: contracts.QuotaCheckResult{Allowed: true, Remaining: 5}}
	c := newTestCommands(ftg, fq, &fakeSessions{}, &fakeEngine{})
	c.HandleMessage(context.Background(), qaTextUpdate("你好", 42))

	if fq.refund != 1 {
		t.Fatalf("异常路径应退款一次，实际 %d", fq.refund)
	}
}

// ── /subscribe 与 /request_summary ───────────────────────────────────────

func TestSubscribeUsesDirectoryThenURLTail(t *testing.T) {
	ftg := &fakeTG{sendOK: true}
	c := newTestCommands(ftg, &fakeQuota{}, &fakeSessions{}, &fakeEngine{})

	// 目录精确匹配 → 使用目录名。
	c.Subscribe(context.Background(), qaCommandUpdate("/subscribe https://t.me/durov", 42))
	want := "✅ 已订阅 `Durov 频道` 的总结推送\n\n当该频道有新总结时，我会及时通知您！"
	if ftg.sent[0].text != want {
		t.Fatalf("订阅回执不符: %q", ftg.sent[0].text)
	}

	// 未收录链接 → URL 尾段兜底。
	c.Subscribe(context.Background(), qaCommandUpdate("/subscribe https://t.me/fallback_name", 42))
	want2 := "✅ 已订阅 `fallback_name` 的总结推送\n\n当该频道有新总结时，我会及时通知您！"
	if ftg.sent[1].text != want2 {
		t.Fatalf("URL 尾段兜底不符: %q", ftg.sent[1].text)
	}
}

func TestSubscribeUsageWithoutArgs(t *testing.T) {
	ftg := &fakeTG{sendOK: true}
	c := newTestCommands(ftg, &fakeQuota{}, &fakeSessions{}, &fakeEngine{})
	c.Subscribe(context.Background(), qaCommandUpdate("/subscribe", 42))
	if !strings.HasPrefix(ftg.sent[0].text, "📖 **订阅频道**") {
		t.Fatalf("/subscribe 无参应回复用法")
	}
}

func TestRequestSummaryReceipt(t *testing.T) {
	ftg := &fakeTG{sendOK: true}
	c := newTestCommands(ftg, &fakeQuota{}, &fakeSessions{}, &fakeEngine{})
	c.RequestSummary(context.Background(), qaCommandUpdate("/request_summary https://t.me/durov", 42))

	want := "📝 已向管理员提交总结请求\n\n频道: Durov 频道\n请求ID: 7\n\n请等待管理员确认并生成总结..."
	if ftg.sent[0].text != want {
		t.Fatalf("请求回执不符: %q", ftg.sent[0].text)
	}
	if ftg.sent[0].parseMode != models.ParseModeHTML {
		t.Fatalf("请求回执应为 HTML")
	}
}
