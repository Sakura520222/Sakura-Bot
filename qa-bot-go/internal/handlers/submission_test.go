// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：测试辅助代码，随源码分发，遵循项目 AGPL-3.0 许可
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package handlers

import (
	"context"
	"io"
	"log/slog"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/tg"
)

// ────────────────────────────── 测试基建 ──────────────────────────────

// testLogger 静默日志。
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// fakeSubAPI 记录发送/编辑/应答的假 Telegram API。
type fakeSubAPI struct {
	mu       sync.Mutex
	sent     []*bot.SendMessageParams
	edited   []*bot.EditMessageTextParams
	answered int
	file     *models.File
	getErr   error
}

func (f *fakeSubAPI) SendMessage(_ context.Context, params *bot.SendMessageParams) (*models.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, params)
	return &models.Message{ID: len(f.sent)}, nil
}

func (f *fakeSubAPI) EditMessageText(_ context.Context, params *bot.EditMessageTextParams) (*models.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.edited = append(f.edited, params)
	return &models.Message{ID: 42}, nil
}

func (f *fakeSubAPI) AnswerCallbackQuery(_ context.Context, _ *bot.AnswerCallbackQueryParams) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answered++
	return true, nil
}

func (f *fakeSubAPI) GetFile(_ context.Context, _ string) (*models.File, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getErr != nil {
		return nil, f.getErr
	}
	if f.file != nil {
		return f.file, nil
	}
	return &models.File{FileID: "fid", FileUniqueID: "fu", FileSize: 5, FilePath: "docs/x"}, nil
}

func (f *fakeSubAPI) DownloadLink(*models.File) string { return "http://cdn.example/file" }

func (f *fakeSubAPI) sentTexts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.sent))
	for _, p := range f.sent {
		out = append(out, p.Text)
	}
	return out
}

// fakeSubStore 记录入库记录的假投稿存储。
type fakeSubStore struct {
	mu   sync.Mutex
	recs []*SubmissionRecord
	id   int64
	err  error
}

func (s *fakeSubStore) CreateSubmission(_ context.Context, rec *SubmissionRecord) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recs = append(s.recs, rec)
	if s.err != nil {
		return 0, s.err
	}
	return s.id, nil
}

// fakeRegistrar 记录深链接注册调用。
type fakeRegistrar struct {
	calls []int64
	err   error
}

func (r *fakeRegistrar) RegisterUser(_ context.Context, userID int64, _, _ *string, _ bool) error {
	r.calls = append(r.calls, userID)
	return r.err
}

// newTestHandler 构建被测处理器（注入固定频道列表与成功 ID）。
func newTestHandler(t *testing.T, channels []string) (*SubmissionHandler, *fakeSubAPI, *fakeSubStore, *fakeRegistrar) {
	t.Helper()
	api := &fakeSubAPI{}
	store := &fakeSubStore{id: 77}
	reg := &fakeRegistrar{}
	h := NewSubmissionHandler(Deps{
		API:       api,
		Store:     store,
		Registrar: reg,
		Channels:  func() []string { return channels },
		Log:       testLogger(),
	})
	// 覆盖下载注入点：返回固定字节，避免测试触网。
	h.download = func(context.Context, TelegramAPI, *models.File) ([]byte, error) {
		return []byte("hello"), nil
	}
	return h, api, store, reg
}

// textUser 私聊用户。
func textUser(id int64) *models.User {
	return &models.User{ID: id, Username: "tester", FirstName: "Tester"}
}

// textUpdate 构建文本消息 update。
func textUpdate(userID int64, text string) *models.Update {
	return &models.Update{Message: &models.Message{
		Chat: models.Chat{ID: userID},
		From: textUser(userID),
		Text: text,
	}}
}

// callbackUpdate 构建回调查询 update。
func callbackUpdate(userID int64, data string) *models.Update {
	return &models.Update{CallbackQuery: &models.CallbackQuery{
		ID:   "cbq1",
		From: *textUser(userID),
		Data: data,
		Message: models.MaybeInaccessibleMessage{Message: &models.Message{
			Chat: models.Chat{ID: userID},
			ID:   42,
		}},
	}}
}

// videoUpdate 构建带视频的消息 update。
func videoUpdate(userID int64, fileSize int64) *models.Update {
	return &models.Update{Message: &models.Message{
		Chat: models.Chat{ID: userID},
		From: textUser(userID),
		Video: &models.Video{
			FileID: "vid1", FileUniqueID: "u1",
			MimeType: "video/mp4", FileName: "clip.mp4", FileSize: fileSize,
		},
	}}
}

// photoUpdate 构建带照片的消息 update（多尺寸，取末位）。
func photoUpdate(userID int64) *models.Update {
	return &models.Update{Message: &models.Message{
		Chat: models.Chat{ID: userID},
		From: textUser(userID),
		Photo: []models.PhotoSize{
			{FileID: "small", FileUniqueID: "s1", FileSize: 100},
			{FileID: "p1", FileUniqueID: "u2", FileSize: 3},
		},
	}}
}

// drive 依次投递 update 并断言 handled。
func drive(t *testing.T, h *SubmissionHandler, updates ...*models.Update) {
	t.Helper()
	for i, u := range updates {
		if !h.HandleUpdate(context.Background(), u) {
			t.Fatalf("update #%d 未被投稿 FSM 消费", i)
		}
	}
}

// startToMedia 从零推进到 WAITING_MEDIA（标题 T → 跳过正文 → 署名投稿）。
func startToMedia(t *testing.T, h *SubmissionHandler, userID int64) {
	t.Helper()
	drive(t, h,
		textUpdate(userID, "📝 投稿"),
		textUpdate(userID, "T"),
		textUpdate(userID, "⏭ 跳过正文"),
		textUpdate(userID, "👤 署名投稿"),
	)
}

// ────────────────────────────── 入口 ──────────────────────────────

func TestSubmissionEntryPoints(t *testing.T) {
	h, api, _, reg := newTestHandler(t, []string{"https://t.me/foo"})
	uid := int64(1)

	// 普通文本无会话 → 放行。
	if h.HandleUpdate(context.Background(), textUpdate(uid, "你好")) {
		t.Fatal("普通文本不应被投稿 FSM 消费")
	}
	// 裸 /start → 放行（深链接裁决入口仅 /start submit）。
	if h.HandleUpdate(context.Background(), textUpdate(uid, "/start")) {
		t.Fatal("裸 /start 不应被投稿 FSM 消费")
	}
	// 📝 投稿 按钮 → 入口。
	if !h.HandleUpdate(context.Background(), textUpdate(uid, "📝 投稿")) {
		t.Fatal("📝 投稿 应被入口消费")
	}
	if !h.GetUserState(uid) {
		t.Fatal("入口后应有活跃会话")
	}
	if n := len(api.sentTexts()); n != 1 {
		t.Fatalf("入口应发送 1 条引导消息，实际 %d", n)
	}
	if got := api.sentTexts()[0]; got != promptTitleMessage {
		t.Fatalf("标题引导文案漂移:\n%q", got)
	}
	if !reflect.DeepEqual(api.sent[0].ReplyMarkup, tg.BuildSubmissionTitleKeyboard()) {
		t.Fatal("标题阶段应挂标题键盘")
	}

	// 深链接 /start submit：注册 + 引导。
	h.ClearUserState(uid)
	if !h.HandleUpdate(context.Background(), textUpdate(uid, "/start submit")) {
		t.Fatal("/start submit 应被入口消费")
	}
	if len(reg.calls) != 1 || reg.calls[0] != uid {
		t.Fatalf("深链接应注册用户一次，calls=%v", reg.calls)
	}
	if !strings.Contains(api.sentTexts()[len(api.sentTexts())-1], "📝 投稿流程") {
		t.Fatal("深链接应进入投稿流程")
	}
}

func TestSubmissionEntryInsideConversationIgnored(t *testing.T) {
	h, api, _, _ := newTestHandler(t, nil)
	uid := int64(2)

	drive(t, h, textUpdate(uid, "/submit"))
	before := len(api.sent)
	// 会话内 /submit 不匹配状态处理器 → 放行且会话保持（PTB 语义）。
	if h.HandleUpdate(context.Background(), textUpdate(uid, "/submit")) {
		t.Fatal("会话内 /submit 应放行")
	}
	if !h.GetUserState(uid) {
		t.Fatal("会话应保持")
	}
	if len(api.sent) != before {
		t.Fatal("会话内 /submit 不应产生新消息")
	}
}

// ────────────────────────────── 标题 / 正文 / 匿名 ──────────────────────────────

func TestSubmissionTitleLengthBoundary(t *testing.T) {
	h, api, _, _ := newTestHandler(t, nil)
	uid := int64(3)

	drive(t, h, textUpdate(uid, "/submit"))

	// 501 码点拒绝。
	long := strings.Repeat("标", 501)
	if !h.HandleUpdate(context.Background(), textUpdate(uid, long)) {
		t.Fatal("标题输入应被消费")
	}
	last := api.sentTexts()[len(api.sentTexts())-1]
	if last != "❌ 标题不能超过 500 个字符，请重新输入。" {
		t.Fatalf("超长标题回复漂移: %q", last)
	}
	if h.getSession(uid).state != subWaitingTitle {
		t.Fatal("超长标题应停留在标题态")
	}

	// 500 码点接受。
	ok := strings.Repeat("标", 500)
	if !h.HandleUpdate(context.Background(), textUpdate(uid, ok)) {
		t.Fatal("合法标题输入应被消费")
	}
	last = api.sentTexts()[len(api.sentTexts())-1]
	if !strings.HasPrefix(last, "📝 标题已记录："+ok) {
		t.Fatal("标题已记录文案应回显完整标题")
	}
	if h.getSession(uid).state != subWaitingContent {
		t.Fatal("标题接受后应进入正文态")
	}
}

func TestSubmissionContentSkipAndAnonymous(t *testing.T) {
	h, api, _, _ := newTestHandler(t, nil)
	uid := int64(4)

	drive(t, h,
		textUpdate(uid, "📝 投稿"),
		textUpdate(uid, "T"),
		textUpdate(uid, "⏭ 跳过正文"),
	)
	last := api.sentTexts()[len(api.sentTexts())-1]
	if !strings.HasPrefix(last, "📝 已跳过正文。\n\n请选择是否匿名投稿：") {
		t.Fatalf("跳过正文文案漂移:\n%q", last)
	}

	// 匿名态输入无关文本 → 放行且会话保持（对齐 PTB states 未命中）。
	if h.HandleUpdate(context.Background(), textUpdate(uid, "随便聊聊")) {
		t.Fatal("匿名态无关文本应放行")
	}
	if h.getSession(uid).state != subWaitingAnonymous {
		t.Fatal("匿名态应保持")
	}

	drive(t, h, textUpdate(uid, "🕶 匿名投稿"))
	last = api.sentTexts()[len(api.sentTexts())-1]
	if !strings.HasPrefix(last, "✅ 已选择：匿名投稿\n\n现在请发送媒体文件") {
		t.Fatalf("匿名选择文案漂移:\n%q", last)
	}
	if !h.getSession(uid).isAnonymous {
		t.Fatal("匿名标志应为真")
	}
}

func TestSubmissionDiscardNotice(t *testing.T) {
	h, api, _, _ := newTestHandler(t, nil)
	uid := int64(5)

	// PTB 语义下入口仅在无活跃会话时触发（会话内 "📝 投稿" 会被当作标题文本），
	// start_submission 的弃置分支与 Python 一致属防御路径，直接调用处理器验证。
	su := normalizeSubmissionUpdate(textUpdate(uid, "📝 投稿"))
	ctx := context.Background()

	// 有标题的旧稿：截 20 码点 + "..."。
	h.setSession(uid, &subSession{state: subWaitingTitle, title: "aaaaaaaaaaaaaaaaaaaaXYZ"})
	h.startSubmission(ctx, su)
	texts := api.sentTexts()
	if got := texts[len(texts)-2]; got != "ℹ️ 旧的投稿（aaaaaaaaaaaaaaaaaaaa...）已取消，开始新投稿。" {
		t.Fatalf("弃置提示漂移: %q", got)
	}
	if got := texts[len(texts)-1]; got != promptTitleMessage {
		t.Fatalf("弃置后应紧跟新标题引导: %q", got)
	}
	if s := h.getSession(uid); s == nil || s.title != "" || s.state != subWaitingTitle {
		t.Fatalf("弃置后应重建空白会话: %+v", s)
	}

	// 无标题旧稿。
	h.setSession(uid, &subSession{state: subWaitingTitle})
	h.startSubmission(ctx, su)
	texts = api.sentTexts()
	if got := texts[len(texts)-2]; got != "ℹ️ 旧的未完成投稿已取消，开始新投稿。" {
		t.Fatalf("无标题弃置提示漂移: %q", got)
	}

	// 会话内 "📝 投稿" 不触发入口，而是进入标题接收（PTB TEXT 匹配优先）。
	before := len(api.sent)
	if !h.HandleUpdate(ctx, textUpdate(uid, "📝 投稿")) {
		t.Fatal("会话内文本应被标题态消费")
	}
	if s := h.getSession(uid); s == nil || s.title != "📝 投稿" || s.state != subWaitingContent {
		t.Fatalf("会话内 📝 投稿 应被当作标题文本: %+v", s)
	}
	if len(api.sent) != before+1 {
		t.Fatal("会话内标题接收应只发一条记录消息")
	}
}

// ────────────────────────────── 媒体 ──────────────────────────────

func TestSubmissionMediaMetadataOversize(t *testing.T) {
	h, api, _, _ := newTestHandler(t, nil)
	uid := int64(6)
	startToMedia(t, h, uid)

	// 21MB 元数据 → 预检拒绝，不触发下载（GetFile 未被调用）。
	drive(t, h, videoUpdate(uid, 21*1024*1024))
	last := api.sentTexts()[len(api.sentTexts())-1]
	if last != "❌ 文件过大（21.0MB），最大支持 20MB。\n请发送更小的文件。" {
		t.Fatalf("超大文件回复漂移: %q", last)
	}
	if n := len(h.getSession(uid).mediaFiles); n != 0 {
		t.Fatalf("超大文件不应入会话，实际 %d", n)
	}
}

func TestSubmissionMediaReceiveVideoAndPhoto(t *testing.T) {
	h, api, _, _ := newTestHandler(t, nil)
	uid := int64(7)
	startToMedia(t, h, uid)

	drive(t, h, videoUpdate(uid, 1024))
	last := api.sentTexts()[len(api.sentTexts())-1]
	if !strings.HasPrefix(last, "✅ 已接收第 1 个媒体文件。") || !strings.Contains(last, "跳过（如果没有更多媒体）") {
		t.Fatalf("媒体接收文案漂移:\n%q", last)
	}

	drive(t, h, photoUpdate(uid))
	last = api.sentTexts()[len(api.sentTexts())-1]
	if !strings.HasPrefix(last, "✅ 已接收第 2 个媒体文件。") {
		t.Fatalf("第二媒体接收文案漂移:\n%q", last)
	}

	files := h.getSession(uid).mediaFiles
	if len(files) != 2 || files[0].kind != "video" || files[1].kind != "photo" {
		t.Fatalf("会话媒体记录异常: %+v", files)
	}
	// 照片取最大尺寸（数组末位）。
	if files[1].fileID != "p1" {
		t.Fatalf("照片应取末位最大尺寸，实际 %q", files[1].fileID)
	}
	if files[0].fileData != "aGVsbG8=" || files[0].fileSize != 5 {
		t.Fatalf("base64/大小未按实际字节记录: %+v", files[0])
	}
}

func TestSubmissionMediaDownloadFailure(t *testing.T) {
	h, api, _, _ := newTestHandler(t, nil)
	uid := int64(8)
	startToMedia(t, h, uid)

	api.mu.Lock()
	api.getErr = context.DeadlineExceeded
	api.mu.Unlock()
	drive(t, h, videoUpdate(uid, 1024))
	last := api.sentTexts()[len(api.sentTexts())-1]
	if last != "❌ 文件下载失败，请重新发送或跳过。" {
		t.Fatalf("下载失败回复漂移: %q", last)
	}

	// 流式超限（评审 B-3b 偏差点）同样落到通用失败文案。
	api.mu.Lock()
	api.getErr = nil
	api.mu.Unlock()
	h.download = func(context.Context, TelegramAPI, *models.File) ([]byte, error) {
		return nil, errMediaTooLarge
	}
	drive(t, h, videoUpdate(uid, 1024))
	last = api.sentTexts()[len(api.sentTexts())-1]
	if last != "❌ 文件下载失败，请重新发送或跳过。" {
		t.Fatalf("流式超限回复漂移: %q", last)
	}
}

// ────────────────────────────── 频道选择与确认 ──────────────────────────────

func TestSubmissionChannelSelectAndConfirmJSONOrder(t *testing.T) {
	h, api, store, _ := newTestHandler(t, []string{"https://t.me/foo", "https://t.me/bar/"})
	uid := int64(9)
	startToMedia(t, h, uid)

	// 接收一个视频媒体，再完成投稿。
	drive(t, h, videoUpdate(uid, 1024))

	// 完成媒体 → 频道选择内联键盘。
	drive(t, h, textUpdate(uid, "✅ 完成投稿"))
	last := api.sent[len(api.sent)-1]
	kb, ok := last.ReplyMarkup.(*models.InlineKeyboardMarkup)
	if !ok {
		t.Fatalf("应回复内联键盘，实际 %T", last.ReplyMarkup)
	}
	if last.Text != "请选择投稿目标频道：" {
		t.Fatalf("频道选择文案漂移: %q", last.Text)
	}
	if kb.InlineKeyboard[0][0].Text != "📢 foo" || kb.InlineKeyboard[0][0].CallbackData != "ch_0" {
		t.Fatalf("ch_0 按钮漂移: %+v", kb.InlineKeyboard[0][0])
	}
	if kb.InlineKeyboard[1][0].Text != "📢 bar" || kb.InlineKeyboard[1][0].CallbackData != "ch_1" {
		t.Fatalf("ch_1 按钮漂移（rstrip 语义）: %+v", kb.InlineKeyboard[1][0])
	}

	// 选择 ch_1 → 编辑已选 + 预览。
	drive(t, h, callbackUpdate(uid, "ch_1"))
	if api.answered != 1 {
		t.Fatal("回调应被应答一次")
	}
	if got := api.edited[len(api.edited)-1].Text; got != "✅ 已选择频道: bar" {
		t.Fatalf("已选频道文案漂移: %q", got)
	}
	preview := api.sentTexts()[len(api.sentTexts())-1]
	want := "📝 投稿预览确认\n\n标题：T\n正文：(无)\n媒体文件：1 个\n匿名投稿：否\n目标频道：bar\n\n" +
		"请确认是否提交：\n• /confirm — 确认提交\n• /cancel_submit — 取消投稿"
	if preview != want {
		t.Fatalf("预览文案漂移:\n%q\n期望:\n%q", preview, want)
	}

	// 确认提交 → 入库 + 回执。
	drive(t, h, textUpdate(uid, "✅ 确认提交"))
	if n := len(store.recs); n != 1 {
		t.Fatalf("应入库一次，实际 %d", n)
	}
	rec := store.recs[0]
	if rec.SubmitterID != uid || rec.SubmitterName != "tester" || rec.Title != "T" {
		t.Fatalf("入库记录漂移: %+v", rec)
	}
	if rec.Content != nil {
		t.Fatalf("跳过正文的 Content 应为 NULL，实际 %v", *rec.Content)
	}
	if rec.TargetChannel == nil || *rec.TargetChannel != "https://t.me/bar/" {
		t.Fatalf("目标频道漂移: %v", rec.TargetChannel)
	}
	// file_size 记录下载实际字节数（5 = len("hello")），而非元数据 1024。
	const wantMedia = `[{"file_id": "vid1", "type": "video", "file_unique_id": "u1", "mime_type": "video/mp4", "file_name": "clip.mp4", "file_data": "aGVsbG8=", "file_size": 5}]`
	if rec.MediaJSON == nil || *rec.MediaJSON != wantMedia {
		t.Fatalf("媒体 JSON 键序/分隔符漂移:\n%s\n期望:\n%s", derefJSON(rec.MediaJSON), wantMedia)
	}
	if !strings.Contains(api.sentTexts()[len(api.sentTexts())-1], "您的投稿ID: 77") {
		t.Fatalf("成功回执漂移: %q", api.sentTexts()[len(api.sentTexts())-1])
	}
	if h.GetUserState(uid) {
		t.Fatal("确认后会话应清除")
	}
}

// derefJSON 测试辅助：安全解引用媒体 JSON 指针。
func derefJSON(p *string) string {
	if p == nil {
		return "<nil>"
	}
	return *p
}

func TestSubmissionConfirmWithoutChannels(t *testing.T) {
	h, api, store, _ := newTestHandler(t, nil)
	uid := int64(10)
	startToMedia(t, h, uid)

	// 无频道配置 → 直接进入确认（submission_handler.py:296-298）。
	drive(t, h, textUpdate(uid, "/skip"))
	preview := api.sentTexts()[len(api.sentTexts())-1]
	if !strings.Contains(preview, "目标频道：(未选择)") {
		t.Fatalf("无频道预览漂移:\n%q", preview)
	}
	drive(t, h, textUpdate(uid, "/confirm"))
	rec := store.recs[0]
	if rec.TargetChannel != nil {
		t.Fatalf("未选频道应为 NULL，实际 %v", *rec.TargetChannel)
	}
	if rec.MediaJSON != nil {
		t.Fatalf("无媒体应为 NULL，实际 %s", *rec.MediaJSON)
	}
}

func TestSubmissionSelectChannelInvalid(t *testing.T) {
	h, api, _, _ := newTestHandler(t, []string{"https://t.me/foo"})
	uid := int64(11)
	startToMedia(t, h, uid)
	drive(t, h, textUpdate(uid, "✅ 完成投稿"))

	drive(t, h, callbackUpdate(uid, "ch_99"))
	if got := api.edited[len(api.edited)-1].Text; got != "❌ 频道选择无效，请重新发送 /submit。" {
		t.Fatalf("无效频道回复漂移: %q", got)
	}
	if h.GetUserState(uid) {
		t.Fatal("无效频道应清除会话")
	}
}

// ────────────────────────────── 取消与杂项 ──────────────────────────────

func TestSubmissionCancelFromStates(t *testing.T) {
	h, api, _, _ := newTestHandler(t, nil)
	uid := int64(12)

	// 标题态文本按钮。
	drive(t, h, textUpdate(uid, "/submit"), textUpdate(uid, "❌ 取消投稿"))
	last := api.sentTexts()[len(api.sentTexts())-1]
	if last != "✅ 投稿已取消。" {
		t.Fatalf("取消文案漂移: %q", last)
	}
	if _, ok := api.sent[len(api.sent)-1].ReplyMarkup.(*models.ReplyKeyboardMarkup); !ok {
		t.Fatal("取消后应回主菜单键盘")
	}
	if h.GetUserState(uid) {
		t.Fatal("取消后会话应清除")
	}

	// 媒体态命令。
	startToMedia(t, h, uid)
	drive(t, h, textUpdate(uid, "/cancel_submit"))
	if h.GetUserState(uid) {
		t.Fatal("媒体态取消后会话应清除")
	}

	// ClearUserState（会话外 /cancel_submit 的对端）。
	drive(t, h, textUpdate(uid, "/submit"))
	h.ClearUserState(uid)
	if h.GetUserState(uid) {
		t.Fatal("ClearUserState 应清除会话")
	}
}

func TestSubmissionFallbackNotHandled(t *testing.T) {
	h, _, _, _ := newTestHandler(t, nil)
	uid := int64(13)

	drive(t, h, textUpdate(uid, "/submit"), textUpdate(uid, "T"))
	// 正文态发送命令以外未匹配文本 → receiveContent（TEXT 匹配，属正常路径）；
	// 这里验证媒体态的普通文本被放行。
	drive(t, h, textUpdate(uid, "⏭ 跳过正文"), textUpdate(uid, "👤 署名投稿"))
	if h.HandleUpdate(context.Background(), textUpdate(uid, "普通文本")) {
		t.Fatal("媒体态普通文本应放行")
	}
	if st := h.getSession(uid).state; st != subWaitingMedia {
		t.Fatalf("媒体态应保持，实际 %v", st)
	}
}

func TestSubmissionSubmitterNameFallback(t *testing.T) {
	h, _, store, _ := newTestHandler(t, nil)
	uid := int64(14)

	// 全流程使用无 username/first_name 的用户 → 投稿者名兜底 str(user_id)。
	anon := func(text string) *models.Update {
		return &models.Update{Message: &models.Message{
			Chat: models.Chat{ID: uid},
			From: &models.User{ID: uid},
			Text: text,
		}}
	}
	drive(t, h,
		anon("/submit"),
		anon("T"),
		anon("⏭ 跳过正文"),
		anon("👤 署名投稿"),
		anon("✅ 完成投稿"),
		anon("✅ 确认提交"),
	)
	if got := store.recs[0].SubmitterName; got != strconv.FormatInt(uid, 10) {
		t.Fatalf("投稿者名兜底漂移: %q", got)
	}
}

func TestSubmissionMediaJSONPhotoKeyOrder(t *testing.T) {
	h, _, store, _ := newTestHandler(t, nil)
	uid := int64(15)

	drive(t, h,
		textUpdate(uid, "/submit"),
		textUpdate(uid, "T"),
		textUpdate(uid, "⏭ 跳过正文"),
		textUpdate(uid, "👤 署名投稿"),
		photoUpdate(uid),
		textUpdate(uid, "/done"),
		textUpdate(uid, "/confirm"),
	)
	const want = `[{"file_id": "p1", "type": "photo", "file_unique_id": "u2", "file_data": "aGVsbG8=", "file_size": 5}]`
	if store.recs[0].MediaJSON == nil || *store.recs[0].MediaJSON != want {
		t.Fatalf("照片媒体 JSON 漂移:\n%s\n期望:\n%s", derefJSON(store.recs[0].MediaJSON), want)
	}
}

func TestSubmissionHelpers(t *testing.T) {
	// pyAnchoredMatch 的 $ 换行容忍。
	if !pyAnchoredMatch("📝 投稿", "📝 投稿\n") {
		t.Fatal("$ 应容忍结尾换行")
	}
	if pyAnchoredMatch("📝 投稿", "📝 投稿\n\n") {
		t.Fatal("多换行不应匹配")
	}
	// lastSegment 的 rstrip("/").split("/")[-1] 语义。
	if got := lastSegment("https://t.me/foo/"); got != "foo" {
		t.Fatalf("lastSegment 漂移: %q", got)
	}
	if got := lastSegment(""); got != "" {
		t.Fatalf("空串 lastSegment 漂移: %q", got)
	}
	// 错误信息截 100 码点。
	long := strings.Repeat("错", 150)
	if got := truncateRunes(long, 100); len([]rune(got)) != 100 {
		t.Fatalf("truncateRunes 漂移: %d", len([]rune(got)))
	}
	// 展示名兜底链 username → first_name → id。
	if got := displayName(&models.User{ID: 9, Username: "u", FirstName: "f"}); got != "u" {
		t.Fatalf("displayName 漂移: %q", got)
	}
	if got := displayName(&models.User{ID: 9, FirstName: "f"}); got != "f" {
		t.Fatalf("displayName 漂移: %q", got)
	}
}

func TestSubmissionPreviewLongContentTruncation(t *testing.T) {
	h, api, _, _ := newTestHandler(t, nil)
	uid := int64(16)

	content := strings.Repeat("内", 200) + "末尾"
	drive(t, h,
		textUpdate(uid, "/submit"),
		textUpdate(uid, "T"),
		textUpdate(uid, content),
		textUpdate(uid, "👤 署名投稿"),
		textUpdate(uid, "/skip"),
	)
	preview := api.sentTexts()[len(api.sentTexts())-1]
	if !strings.Contains(preview, strings.Repeat("内", 200)+"...\n") {
		t.Fatal("正文应截 200 码点并追加省略号")
	}
	if strings.Contains(preview, "末尾") {
		t.Fatal("截断后不应残留第 201 码点之后的内容")
	}
}

// 编译期守卫：键盘常量与按钮匹配一致性（防 tg 包文案漂移静默破坏过滤器）。
func TestSubmissionKeyboardConstantsWiring(t *testing.T) {
	if tg.MenuSubmit == "" || tg.SubmitMenuCancel == "" {
		t.Fatal("tg 按钮常量不应为空")
	}
	// 确认键盘首按钮必须与 CONFIRM 态过滤器一致。
	kb := tg.BuildSubmissionConfirmKeyboard()
	if got := kb.Keyboard[0][0].Text; got != tg.SubmitMenuConfirm {
		t.Fatalf("确认键盘按钮与常量不一致: %q", got)
	}
}
