// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package summary

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Sakura520222/Sakura-Bot/internal/config"
	"github.com/Sakura520222/Sakura-Bot/internal/db"
	"github.com/Sakura520222/Sakura-Bot/internal/llm"
	"github.com/Sakura520222/Sakura-Bot/internal/mtproto"
	"github.com/Sakura520222/Sakura-Bot/internal/tg"
	tgbot "github.com/go-telegram/bot"
)

type fakeCompleter struct {
	fn func(ctx context.Context, req llm.CompleteRequest) (*llm.CompleteResponse, error)
}

func (f *fakeCompleter) CompleteChat(ctx context.Context, req llm.CompleteRequest) (*llm.CompleteResponse, error) {
	return f.fn(ctx, req)
}

func TestSplitAndPackTextsUTF8Safe(t *testing.T) {
	src := "樱花🌸Bot开源日报测试文本12345"
	chunks, err := SplitText(src, 12)
	if err != nil {
		t.Fatalf("SplitText error: %v", err)
	}
	if strings.Join(chunks, "") != src {
		t.Fatalf("SplitText lost characters: got %q, want %q", strings.Join(chunks, ""), src)
	}
	for i, c := range chunks {
		if TokenUpperBound(c) > 12 {
			t.Fatalf("chunk[%d] %q exceeded budget 12 (%d)", i, c, TokenUpperBound(c))
		}
	}

	packed, err := PackTexts([]string{"第一条", "第二条", "超长文本需要自动拆分再合并"}, 20)
	if err != nil {
		t.Fatalf("PackTexts error: %v", err)
	}
	if len(packed) < 2 {
		t.Fatalf("expected >=2 groups, got %d", len(packed))
	}
}

func TestSummarizeCompleteHierarchicalAndLengthExpansion(t *testing.T) {
	auditDir := t.TempDir()
	calls := 0
	var seenMaxTokens []int
	comp := &fakeCompleter{
		fn: func(_ context.Context, req llm.CompleteRequest) (*llm.CompleteResponse, error) {
			calls++
			system := *req.Messages[0].Content
			if !strings.Contains(system, "普通 Markdown 总结正文") || strings.Contains(system, "保留关键事实、来源编号") {
				t.Fatalf("unexpected summary system instruction: %s", system)
			}
			if req.MaxTokens != nil {
				seenMaxTokens = append(seenMaxTokens, *req.MaxTokens)
			}
			// 第一次请求返回 finish_reason=length，验证自动扩容 max_tokens 重试。
			if calls == 1 {
				return &llm.CompleteResponse{
					FinishReason: "length",
					Content:      "半截输出",
				}, nil
			}
			userMsg := *req.Messages[1].Content
			if strings.HasPrefix(userMsg, MergePrefix) {
				return &llm.CompleteResponse{
					FinishReason: "stop",
					Content:      "【汇总定稿】关键要点 1 & 2 https://t.me/test/1 https://t.me/test/2",
				}, nil
			}
			return &llm.CompleteResponse{
				FinishReason: "stop",
				Content:      "分块要点 https://t.me/test/1",
			}, nil
		},
	}

	msgs := []string{
		"频道: https://t.me/test\n消息ID: 1\n内容: " + strings.Repeat("甲", 140) + "\n链接: https://t.me/test/1",
		"频道: https://t.me/test\n消息ID: 2\n内容: " + strings.Repeat("乙", 140) + "\n链接: https://t.me/test/2",
	}
	out, err := SummarizeComplete(context.Background(), msgs, "请总结：\n", comp, PipelineOptions{
		AuditDir:      auditDir,
		ContextTokens: 1600,
		OutputTokens:  200,
		Model:         "gpt-test",
		RetryBackoff:  func(int) time.Duration { return 0 },
	})
	if err != nil {
		t.Fatalf("SummarizeComplete failed: %v", err)
	}
	if !strings.Contains(out, "汇总定稿") {
		t.Fatalf("unexpected summary output: %q", out)
	}
	if len(seenMaxTokens) < 2 || seenMaxTokens[1] <= seenMaxTokens[0] {
		t.Fatalf("expected max_tokens expansion on finish_reason=length, got %v", seenMaxTokens)
	}

	entries, err := os.ReadDir(auditDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected 1 audit file, got %d (err=%v)", len(entries), err)
	}
	raw, _ := os.ReadFile(filepath.Join(auditDir, entries[0].Name()))
	var rec RunAuditRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatalf("unmarshal audit record: %v", err)
	}
	if rec.Status != "completed" || rec.ProcessedCount != 2 || rec.FailedCount != 0 {
		t.Fatalf("unexpected audit record: %+v", rec)
	}
}

func TestExtractDateRangeAndTimeManager(t *testing.T) {
	ref := time.Date(2026, 12, 30, 12, 0, 0, 0, time.UTC)
	st, et := ExtractDateRangeFromSummary("**测试频道周报 12.28-1.3**\n\n正文", ref)
	if st == nil || et == nil {
		t.Fatalf("expected cross-year weekly date range")
	}
	if st.Year() != 2026 || et.Year() != 2027 || int(et.Month()) != 1 || et.Day() != 3 {
		t.Fatalf("unexpected cross-year range: %v -> %v", st, et)
	}

	tmpPath := filepath.Join(t.TempDir(), ".last_summary_time.json")
	legacyJSON := `{"https://t.me/demo":{"time":"2026-05-01T08:00:00","report_message_ids":[11,12]}}`
	if err := os.WriteFile(tmpPath, []byte(legacyJSON), 0o644); err != nil {
		t.Fatalf("write legacy file: %v", err)
	}
	tm := NewTimeManager(tmpPath)
	loaded, ok, err := tm.LoadChannel("@demo")
	if err != nil || !ok || loaded == nil {
		t.Fatalf("LoadChannel(@demo) failed: ok=%v err=%v", ok, err)
	}
	if len(loaded.SummaryMessageIDs) != 2 || loaded.SummaryMessageIDs[0] != 11 {
		t.Fatalf("legacy report_message_ids not migrated: %+v", loaded)
	}
	if err := tm.UpdatePollMessageIDs("https://t.me/demo", []int{99}); err != nil {
		t.Fatalf("UpdatePollMessageIDs error: %v", err)
	}
	loaded2, _, _ := tm.LoadChannel("https://t.me/demo")
	if len(loaded2.PollMessageIDs) != 1 || loaded2.PollMessageIDs[0] != 99 {
		t.Fatalf("expected PollMessageIDs=[99], got %+v", loaded2)
	}
}

type fakeMessenger struct {
	mtproto.Messenger
	history []*mtproto.IncomingMessage
	sent    []string
	pinned  []int
}

func (f *fakeMessenger) ResolveChannel(_ context.Context, ref string) (*mtproto.ChannelInfo, error) {
	return &mtproto.ChannelInfo{ID: 123, MarkedID: -100123, Title: "Sakura资讯", Username: "sakura_news"}, nil
}

func (f *fakeMessenger) FetchChannelHistory(_ context.Context, _ string, _ time.Time, excluded map[int]bool) ([]*mtproto.IncomingMessage, *mtproto.ChannelInfo, error) {
	var out []*mtproto.IncomingMessage
	for _, m := range f.history {
		if !excluded[m.ID] {
			out = append(out, m)
		}
	}
	return out, &mtproto.ChannelInfo{ID: 123, MarkedID: -100123, Title: "Sakura资讯", Username: "sakura_news"}, nil
}

func (f *fakeMessenger) SendLongMessage(_ context.Context, _ any, text string, _ mtproto.SendOptions) ([]int, error) {
	f.sent = append(f.sent, text)
	return []int{501}, nil
}

func (f *fakeMessenger) PinMessage(_ context.Context, _ any, messageID int, _ bool) error {
	f.pinned = append(f.pinned, messageID)
	return nil
}

type fakeSummaryRepo struct {
	saved []db.SummaryInsert
}

func (r *fakeSummaryRepo) SaveSummary(_ context.Context, in db.SummaryInsert) (int64, error) {
	r.saved = append(r.saved, in)
	return int64(len(r.saved)), nil
}

type fakeSubRepo struct {
	subs    []int64
	removed []int64
}

func (s *fakeSubRepo) ListChannelSummarySubscribers(_ context.Context, _ string) ([]int64, error) {
	return s.subs, nil
}

func (s *fakeSubRepo) RemoveBlockedSubscriber(_ context.Context, userID int64, _ string) error {
	s.removed = append(s.removed, userID)
	return nil
}

type fakeBotAPISender struct {
	sentTo []any
}

func (b *fakeBotAPISender) SendRichMessage(_ context.Context, _ string, req tgbot.SendRichMessageParams) (int64, error) {
	if req.ChatID == int64(2002) {
		return 0, &tg.BadRequestError{BotAPIError: tg.BotAPIError{Description: "Forbidden: bot was blocked by the user"}}
	}
	b.sentTo = append(b.sentTo, req.ChatID)
	return 1, nil
}

func TestGenerateChannelSummarySingleSaveAndPush(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	jsonPath := filepath.Join(dir, "config.json")
	_ = os.WriteFile(envPath, []byte("REPORT_ADMIN_IDS=9001\nQA_BOT_TOKEN=123:abc\n"), 0o644)
	_ = os.WriteFile(jsonPath, []byte(`{"channels":["https://t.me/sakura_news"],"send_report_to_source":true}`), 0o644)

	cfg := config.New(envPath, jsonPath)
	if err := cfg.Init(); err != nil {
		t.Fatalf("cfg.Init: %v", err)
	}

	bot := &fakeMessenger{
		history: []*mtproto.IncomingMessage{
			{ID: 10, Text: "Go 1.27 正式发布", Date: time.Now().UTC().Add(-2 * time.Hour)},
		},
	}
	repo := &fakeSummaryRepo{}
	subRepo := &fakeSubRepo{subs: []int64{1001, 2002}}
	botAPI := &fakeBotAPISender{}
	push := NewPushHandler(subRepo, botAPI, cfg.QABotToken, nil)

	svc := NewService(ServiceDeps{
		Config:        cfg,
		BotClient:     func() mtproto.Messenger { return bot },
		HistoryClient: func() mtproto.Messenger { return bot },
		Completer: func() (Completer, string) {
			return &fakeCompleter{fn: func(_ context.Context, _ llm.CompleteRequest) (*llm.CompleteResponse, error) {
				return &llm.CompleteResponse{FinishReason: "stop", Content: "• 核心更新：Go 1.27"}, nil
			}}, "gpt-summary"
		},
		DB:            repo,
		TimeManager:   NewTimeManager(filepath.Join(dir, ".last_summary_time.json")),
		PushHandler:   push,
		SourceArchDir: filepath.Join(dir, "sources"),
		AuditDir:      filepath.Join(dir, "runs"),
	})

	res := svc.GenerateChannelSummary(context.Background(), "@sakura_news", false, "manual")
	if !res.Success {
		t.Fatalf("GenerateChannelSummary failed: %s", res.Error)
	}
	// 验证只落库一次（修复 Python 版重复落库两条记录的 bug）。
	if len(repo.saved) != 1 {
		t.Fatalf("expected exactly 1 SaveSummary call, got %d", len(repo.saved))
	}
	if len(repo.saved[0].SummaryMessageIDs) != 1 || repo.saved[0].SummaryMessageIDs[0] != 501 {
		t.Fatalf("unexpected saved SummaryMessageIDs: %+v", repo.saved[0].SummaryMessageIDs)
	}
	if len(bot.pinned) != 1 || bot.pinned[0] != 501 {
		t.Fatalf("expected message 501 pinned, got %v", bot.pinned)
	}
	if len(botAPI.sentTo) != 1 || len(subRepo.removed) != 1 || subRepo.removed[0] != 2002 {
		t.Fatalf("unexpected subscriber push state: sent=%v removed=%v", botAPI.sentTo, subRepo.removed)
	}
}
