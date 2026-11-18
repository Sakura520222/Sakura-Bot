// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package welcome

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Sakura520222/Sakura-Bot/internal/config"
	"github.com/Sakura520222/Sakura-Bot/internal/mtproto"
)

type fakeWelcomeRepo struct {
	mu         sync.Mutex
	registered map[int64]bool
	pending    bool
	enqueued   int
}

func (r *fakeWelcomeRepo) IsUserRegistered(_ context.Context, uid int64) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.registered[uid], nil
}

func (r *fakeWelcomeRepo) HasRecentSummaryRequest(_ context.Context, _ int64, _ string, _ time.Duration) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.pending, nil
}

func (r *fakeWelcomeRepo) EnqueueSummaryRequest(_ context.Context, _ int64, _ string, _ int64, _ map[string]any) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.enqueued++
	return int64(r.enqueued), nil
}

type fakeWelcomeMessenger struct {
	mtproto.Messenger
	mu       sync.Mutex
	sent     []string
	edited   []mtproto.SendOptions
	answered []string
}

func (m *fakeWelcomeMessenger) GetLinkedDiscussionChatID(_ context.Context, _ string) (int64, error) {
	return -100555, nil
}

func (m *fakeWelcomeMessenger) ResolveChannel(_ context.Context, _ string) (*mtproto.ChannelInfo, error) {
	return &mtproto.ChannelInfo{ID: 555, MarkedID: -100555, Title: "开源周刊", Username: "oss_weekly"}, nil
}

func (m *fakeWelcomeMessenger) SendMessage(_ context.Context, _ any, text string, _ mtproto.SendOptions) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, text)
	return 100 + len(m.sent), nil
}

func (m *fakeWelcomeMessenger) EditMessage(_ context.Context, _ any, _ int, _ string, opts mtproto.SendOptions) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.edited = append(m.edited, opts)
	return nil
}

func (m *fakeWelcomeMessenger) AnswerCallback(_ context.Context, _ int64, msg string, _ bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.answered = append(m.answered, msg)
	return nil
}

func TestParseSummaryCallbackAndUnregisteredRestore(t *testing.T) {
	ch, id, ok := ParseSummaryCallbackData("req_summary:https://t.me/oss_weekly:42")
	if !ok || ch != "https://t.me/oss_weekly" || id != 42 {
		t.Fatalf("ParseSummaryCallbackData = (%q, %d, %v)", ch, id, ok)
	}

	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	jsonPath := filepath.Join(dir, "config.json")
	_ = os.WriteFile(envPath, []byte("QA_BOT_USERNAME=sakura_qa\n"), 0o644)
	_ = os.WriteFile(jsonPath, []byte(`{"channels":["https://t.me/oss_weekly"]}`), 0o644)

	cfg := config.New(envPath, jsonPath)
	_ = cfg.Init()

	repo := &fakeWelcomeRepo{registered: map[int64]bool{}}
	bot := &fakeWelcomeMessenger{}
	h := NewHandler(Deps{
		Config:         cfg,
		Repo:           repo,
		BotClient:      func() mtproto.Messenger { return bot },
		MinDelay:       -1,
		MaxDelay:       -1,
		RestorePollInt: 10 * time.Millisecond,
		RestoreChecks:  2,
	})
	h.Start(context.Background())

	h.HandleDiscussionMessage(context.Background(), &mtproto.IncomingMessage{
		ID:           99,
		ChatID:       -100777,
		IsSupergroup: true,
		GroupedID:    1234,
		FwdFrom:      &mtproto.ForwardHeader{FromID: -100555, ChannelPost: 42},
	})
	// 同一相册第二张图片不重复发送欢迎卡片。
	h.HandleDiscussionMessage(context.Background(), &mtproto.IncomingMessage{
		ID:           100,
		ChatID:       -100777,
		IsSupergroup: true,
		GroupedID:    1234,
		FwdFrom:      &mtproto.ForwardHeader{FromID: -100555, ChannelPost: 43},
	})
	time.Sleep(40 * time.Millisecond)

	// 未注册用户点击按钮 -> 临时切换为 URL 按钮并在超时后自动恢复原始回调按钮。
	_ = h.HandleSummaryRequestCallback(context.Background(), mtproto.CallbackEvent{
		QueryID:   1,
		SenderID:  8001,
		ChatID:    -100777,
		MessageID: 101,
		Data:      []byte("req_summary:https://t.me/oss_weekly:42"),
	})
	time.Sleep(50 * time.Millisecond)

	// 注册后点击按钮 -> 成功写入 request_queue。
	repo.mu.Lock()
	repo.registered[8001] = true
	repo.mu.Unlock()
	_ = h.HandleSummaryRequestCallback(context.Background(), mtproto.CallbackEvent{
		QueryID:   2,
		SenderID:  8001,
		ChatID:    -100777,
		MessageID: 101,
		Data:      []byte("req_summary:https://t.me/oss_weekly:42"),
	})
	h.Stop()

	bot.mu.Lock()
	defer bot.mu.Unlock()
	if len(bot.sent) != 1 {
		t.Fatalf("expected 1 welcome sent for album, got %d", len(bot.sent))
	}
	if len(bot.edited) < 2 || bot.edited[0].Buttons[0][0].URL == "" || bot.edited[1].Buttons[0][0].Data == "" {
		t.Fatalf("expected URL button followed by restored callback button, got %+v", bot.edited)
	}
	if repo.enqueued != 1 {
		t.Fatalf("expected 1 enqueued summary request, got %d", repo.enqueued)
	}
}
