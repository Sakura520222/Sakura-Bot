// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package poll

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Sakura520222/Sakura-Bot/internal/config"
	"github.com/Sakura520222/Sakura-Bot/internal/llm"
	"github.com/Sakura520222/Sakura-Bot/internal/mtproto"
)

type fakePollCompleter struct {
	fn func(ctx context.Context, req llm.CompleteRequest) (*llm.CompleteResponse, error)
}

func (f *fakePollCompleter) CompleteChat(ctx context.Context, req llm.CompleteRequest) (*llm.CompleteResponse, error) {
	return f.fn(ctx, req)
}

func TestFormatPollPromptAndClamp(t *testing.T) {
	rendered := FormatPollPrompt(config.DefaultPollPrompt(), "本周开源热点汇总内容")
	if strings.Contains(rendered, "{{") || strings.Contains(rendered, "}}") {
		t.Fatalf("expected double braces unescaped, got: %q", rendered)
	}
	if !strings.Contains(rendered, "本周开源热点汇总内容") {
		t.Fatalf("expected summary_text substituted")
	}

	longQ := strings.Repeat("问", 260)
	longO := strings.Repeat("选", 110)
	spec, err := GeneratePollFromSummary(context.Background(), "这是一段足够长的周报正文以触发模型调用", nil, GenerateOptions{
		Completer: &fakePollCompleter{fn: func(_ context.Context, _ llm.CompleteRequest) (*llm.CompleteResponse, error) {
			return &llm.CompleteResponse{
				FinishReason: "stop",
				Content:      "```json\n{\"question\":\"" + longQ + "\",\"options\":[\"" + longO + "\",\"选项B\"]}\n```",
			}, nil
		}},
	})
	if err != nil || spec == nil {
		t.Fatalf("GeneratePollFromSummary failed: %v", err)
	}
	if utf8.RuneCountInString(spec.Question) > 255 || !strings.HasSuffix(spec.Question, "...") {
		t.Fatalf("unexpected clamped question length=%d: %q", utf8.RuneCountInString(spec.Question), spec.Question)
	}
	if utf8.RuneCountInString(spec.Options[0]) > 100 || !strings.HasSuffix(spec.Options[0], "...") {
		t.Fatalf("unexpected clamped option[0] length=%d", utf8.RuneCountInString(spec.Options[0]))
	}
}

type fakePollMessenger struct {
	mtproto.Messenger
	mu            sync.Mutex
	discussionID  int64
	nextPollID    int
	sentPolls     []mtproto.PollSendOptions
	deletedMsgs   [][]int
	editedButtons [][][]mtproto.InlineButton
	callbacks     []string
}

func (m *fakePollMessenger) ResolveChannel(_ context.Context, _ string) (*mtproto.ChannelInfo, error) {
	return &mtproto.ChannelInfo{ID: 888, MarkedID: -100888, Title: "测试频道", Username: "test_ch"}, nil
}

func (m *fakePollMessenger) GetLinkedDiscussionChatID(_ context.Context, _ string) (int64, error) {
	return m.discussionID, nil
}

func (m *fakePollMessenger) SendPoll(_ context.Context, _ any, opts mtproto.PollSendOptions) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextPollID++
	m.sentPolls = append(m.sentPolls, opts)
	return 700 + m.nextPollID, nil
}

func (m *fakePollMessenger) DeleteMessages(_ context.Context, _ any, ids []int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deletedMsgs = append(m.deletedMsgs, append([]int(nil), ids...))
	return nil
}

func (m *fakePollMessenger) EditMessage(_ context.Context, _ any, _ int, _ string, opts mtproto.SendOptions) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.editedButtons = append(m.editedButtons, opts.Buttons)
	return nil
}

func (m *fakePollMessenger) AnswerCallback(_ context.Context, _ int64, msg string, _ bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.callbacks = append(m.callbacks, msg)
	return nil
}

func TestDiscussionWaiterAndVoteThresholdRegeneration(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	jsonPath := filepath.Join(dir, "config.json")
	_ = os.WriteFile(envPath, []byte("REPORT_ADMIN_IDS=9001\nPOLL_REGEN_THRESHOLD=2\n"), 0o644)
	_ = os.WriteFile(jsonPath, []byte(`{"enable_poll":true,"poll_regen_threshold":2,"enable_auto_poll":true}`), 0o644)

	cfg := config.New(envPath, jsonPath)
	if err := cfg.Init(); err != nil {
		t.Fatalf("cfg.Init: %v", err)
	}

	bot := &fakePollMessenger{discussionID: -100999}
	svc := NewService(ServiceDeps{
		Config: cfg,
		Completer: &fakePollCompleter{fn: func(_ context.Context, _ llm.CompleteRequest) (*llm.CompleteResponse, error) {
			return &llm.CompleteResponse{
				FinishReason: "stop",
				Content:      `{"question":"最期待哪项特性？","options":["并发性能","单二进制部署"]}`,
			}, nil
		}},
		RegenStore:        NewRegenStore(filepath.Join(dir, ".poll_regenerations.json")),
		ForwardWaitWindow: 2 * time.Second,
	})

	// 并发模拟 Telegram 在 30ms 后把总结帖自动转发到讨论群。
	go func() {
		time.Sleep(30 * time.Millisecond)
		svc.NotifyDiscussionMessage(&mtproto.IncomingMessage{
			ID:     321,
			ChatID: -100999,
			FwdFrom: &mtproto.ForwardHeader{
				FromID:      -100888,
				ChannelPost: 501,
			},
		})
	}()

	pollID, err := svc.SendSummaryPoll(context.Background(), bot, "https://t.me/test_ch", 501, "这是一份超过十个字的频道周报总结内容")
	if err != nil || pollID != 701 {
		t.Fatalf("SendSummaryPoll = (%d, %v), want 701", pollID, err)
	}
	if bot.sentPolls[0].ReplyToMsgID != 321 {
		t.Fatalf("expected poll replying to discussion forward 321, got %d", bot.sentPolls[0].ReplyToMsgID)
	}

	// 用户 1 点击 request_regen_501（1/2，未达阈值）。
	_ = svc.HandleVoteRegenRequestCallback(context.Background(), bot, mtproto.CallbackEvent{
		QueryID:  1,
		SenderID: 101,
		ChatID:   -100999,
		Data:     []byte("request_regen_501"),
	})
	// 同一用户重复点击应被去重拦截。
	_ = svc.HandleVoteRegenRequestCallback(context.Background(), bot, mtproto.CallbackEvent{
		QueryID:  2,
		SenderID: 101,
		ChatID:   -100999,
		Data:     []byte("request_regen_501"),
	})
	if len(bot.sentPolls) != 1 {
		t.Fatalf("duplicate voter must not trigger regen, sentPolls=%d", len(bot.sentPolls))
	}

	// 用户 2 点击 request_regen_501（2/2，触发自动删除旧投票并重发新投票，且复用转发帖 321）。
	_ = svc.HandleVoteRegenRequestCallback(context.Background(), bot, mtproto.CallbackEvent{
		QueryID:  3,
		SenderID: 102,
		ChatID:   -100999,
		Data:     []byte("request_regen_501"),
	})
	if len(bot.deletedMsgs) != 1 || bot.deletedMsgs[0][0] != 701 {
		t.Fatalf("expected old poll 701 deleted, got %v", bot.deletedMsgs)
	}
	if len(bot.sentPolls) != 2 || bot.sentPolls[1].ReplyToMsgID != 321 {
		t.Fatalf("expected regenerated poll replying to 321, got %+v", bot.sentPolls)
	}

	// 验证 AutoPollHandler 队列消费与去重。
	ap := NewAutoPollHandler(AutoPollDeps{
		Config:    cfg,
		BotClient: func() mtproto.Messenger { return bot },
		Completer: svc.deps.Completer,
	})
	ap.Start(context.Background())
	ok1 := ap.Enqueue(AutoPollTask{
		ChannelID:    "https://t.me/test_ch",
		DiscussionID: -100999,
		ForwardMsgID: 888,
		MessageText:  "这是一条足够长的频道推文，用于测试自动趣味投票队列！",
	})
	ok2 := ap.Enqueue(AutoPollTask{
		ChannelID:    "https://t.me/test_ch",
		DiscussionID: -100999,
		ForwardMsgID: 888,
		MessageText:  "重复推文应命中 LRU 去重",
	})
	ap.Stop()
	if !ok1 || ok2 {
		t.Fatalf("Enqueue dedup mismatch: ok1=%v ok2=%v", ok1, ok2)
	}
	if ap.Stats().ProcessedCount != 1 {
		t.Fatalf("expected AutoPoll ProcessedCount=1, got %+v", ap.Stats())
	}
}
