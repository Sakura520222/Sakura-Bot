// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package submission

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Sakura520222/Sakura-Bot/internal/config"
	"github.com/Sakura520222/Sakura-Bot/internal/db"
	"github.com/Sakura520222/Sakura-Bot/internal/llm"
	"github.com/Sakura520222/Sakura-Bot/internal/mtproto"
)

type fakeSubRepo struct {
	sub           db.Submission
	notifications []db.NotificationInsert
}

func (r *fakeSubRepo) ListPendingSubmissions(_ context.Context) ([]db.Submission, error) {
	return []db.Submission{r.sub}, nil
}

func (r *fakeSubRepo) GetSubmissionByID(_ context.Context, _ int64) (*db.Submission, error) {
	cp := r.sub
	return &cp, nil
}

func (r *fakeSubRepo) SetSubmissionReviewMessageID(_ context.Context, _, msgID int64) error {
	r.sub.ReviewMessageID = &msgID
	return nil
}

func (r *fakeSubRepo) SaveAIOptimization(_ context.Context, _ int64, title, content string) error {
	r.sub.AIOptimizedTitle = &title
	r.sub.AIOptimizedContent = &content
	return nil
}

func (r *fakeSubRepo) ClearAIOptimization(_ context.Context, _ int64) error {
	r.sub.AIOptimizedTitle = nil
	r.sub.AIOptimizedContent = nil
	return nil
}

func (r *fakeSubRepo) ApproveSubmission(_ context.Context, _, reviewedBy int64, sigForced bool) (*db.Submission, error) {
	r.sub.Status = "approved"
	r.sub.ReviewedBy = &reviewedBy
	if sigForced {
		r.sub.SignatureForced = true
	}
	cp := r.sub
	return &cp, nil
}

func (r *fakeSubRepo) RejectSubmission(_ context.Context, _, reviewedBy int64) error {
	r.sub.Status = "rejected"
	r.sub.ReviewedBy = &reviewedBy
	return nil
}

func (r *fakeSubRepo) EnqueueNotification(_ context.Context, in db.NotificationInsert) (int64, error) {
	r.notifications = append(r.notifications, in)
	return int64(len(r.notifications)), nil
}

type fakeSubMessenger struct {
	mtproto.Messenger
	mediaSent []mtproto.MediaUpload
	edited    []string
}

func (m *fakeSubMessenger) SendMessage(_ context.Context, _ any, _ string, _ mtproto.SendOptions) (int, error) {
	return 301, nil
}

func (m *fakeSubMessenger) SendMedia(_ context.Context, _ any, item mtproto.MediaUpload, _ mtproto.SendOptions) (int, error) {
	m.mediaSent = append(m.mediaSent, item)
	return 401, nil
}

func (m *fakeSubMessenger) EditMessage(_ context.Context, _ any, _ int, text string, _ mtproto.SendOptions) error {
	m.edited = append(m.edited, text)
	return nil
}

func (m *fakeSubMessenger) AnswerCallback(_ context.Context, _ int64, _ string, _ bool) error {
	return nil
}

func (m *fakeSubMessenger) ResolveChannel(_ context.Context, _ string) (*mtproto.ChannelInfo, error) {
	return &mtproto.ChannelInfo{ID: 77, MarkedID: -10077, Title: "社区频道", Username: "community_ch"}, nil
}

type fakeSubCompleter struct{}

func (fakeSubCompleter) CompleteChat(_ context.Context, _ llm.CompleteRequest) (*llm.CompleteResponse, error) {
	return &llm.CompleteResponse{
		FinishReason: "stop",
		Content:      `{"title":"润色后的标题","content":"润色后的清晰正文"}`,
	}, nil
}

func TestSubmissionReviewAIOptimizeAndForceSignApprove(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	jsonPath := filepath.Join(dir, "config.json")
	_ = os.WriteFile(envPath, []byte("REPORT_ADMIN_IDS=9001\n"), 0o644)
	_ = os.WriteFile(jsonPath, []byte(`{}`), 0o644)
	cfg := config.New(envPath, jsonPath)
	_ = cfg.Init()

	repo := &fakeSubRepo{
		sub: db.Submission{
			ID:            7,
			SubmitterID:   5001,
			SubmitterName: "alice",
			Title:         "草稿标题",
			Content:       "草稿正文",
			TargetChannel: "https://t.me/community_ch",
			IsAnonymous:   true,
			MediaFiles: []map[string]any{{
				"type":      "photo",
				"file_data": base64.StdEncoding.EncodeToString([]byte("img")),
			}},
		},
	}
	bot := &fakeSubMessenger{}
	svc := NewReviewService(ReviewDeps{
		Config:    cfg,
		Repo:      repo,
		BotClient: func() mtproto.Messenger { return bot },
		Completer: func() (Completer, string) { return fakeSubCompleter{}, "gpt-edit" },
	})

	svc.CheckPendingSubmissions(context.Background())
	if repo.sub.ReviewMessageID == nil || *repo.sub.ReviewMessageID != 301 {
		t.Fatalf("expected ReviewMessageID=301, got %v", repo.sub.ReviewMessageID)
	}

	// AI 优化
	_ = svc.HandleCallback(context.Background(), mtproto.CallbackEvent{
		QueryID:   1,
		SenderID:  9001,
		ChatID:    9001,
		MessageID: 301,
		Data:      []byte("submission_aiopt_7"),
	})
	if repo.sub.AIOptimizedTitle == nil || *repo.sub.AIOptimizedTitle != "润色后的标题" {
		t.Fatalf("unexpected AIOptimizedTitle: %v", repo.sub.AIOptimizedTitle)
	}

	// 强制署名通过 -> 发布携带 @alice 与优化后标题，并通知投稿人。
	_ = svc.HandleCallback(context.Background(), mtproto.CallbackEvent{
		QueryID:   2,
		SenderID:  9001,
		ChatID:    9001,
		MessageID: 301,
		Data:      []byte("submission_signapprove_7"),
	})
	lastMedia := bot.mediaSent[len(bot.mediaSent)-1]
	if !strings.Contains(lastMedia.Caption, "**润色后的标题**") || !strings.Contains(lastMedia.Caption, "—— 投稿者: @alice") {
		t.Fatalf("unexpected published caption: %q", lastMedia.Caption)
	}
	if len(repo.notifications) != 1 || repo.notifications[0].NotificationType != "submission_approved" {
		t.Fatalf("expected submission_approved notification, got %+v", repo.notifications)
	}
}
