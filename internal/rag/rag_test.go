// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package rag

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Sakura520222/Sakura-Bot/internal/config"
	"github.com/Sakura520222/Sakura-Bot/internal/db"
	"github.com/Sakura520222/Sakura-Bot/internal/media"
	"github.com/Sakura520222/Sakura-Bot/internal/mtproto"
	"github.com/Sakura520222/Sakura-Bot/internal/vectors"
)

func TestRealtimeRAGUpsertEditStaleSkipAndDelete(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	jsonPath := filepath.Join(dir, "config.json")
	_ = os.WriteFile(envPath, []byte(""), 0o644)
	_ = os.WriteFile(jsonPath, []byte(`{"channels":["https://t.me/rag_ch"]}`), 0o644)

	cfg := config.New(envPath, jsonPath)
	_ = cfg.Init()

	store := vectors.NewReadWrite(filepath.Join(dir, "vectors"), 4)
	defer store.Close()

	embedFn := func(_ context.Context, texts []string) ([][]float32, error) {
		out := make([][]float32, len(texts))
		for i := range texts {
			out[i] = []float32{0.5, 0.5, 0.5, 0.5}
		}
		return out, nil
	}

	h := NewHandler(Deps{
		Config:        cfg,
		VectorStore:   store,
		EmbedFn:       embedFn,
		SourceArchDir: filepath.Join(dir, "sources"),
		BatchSize:     2,
		BatchInterval: 30 * time.Millisecond,
	})
	h.Start(context.Background())

	// 同一条消息连续编辑两次，只有最新一代应被写入向量库。
	h.HandleIncomingMessage(context.Background(), &mtproto.IncomingMessage{
		ID:           15,
		ChatID:       -100333,
		ChatTitle:    "RAG测试频道",
		ChatUsername: "rag_ch",
		IsBroadcast:  true,
		Text:         "旧版正文",
		Date:         time.Now().UTC(),
	})
	editNow := time.Now().UTC()
	h.HandleIncomingMessage(context.Background(), &mtproto.IncomingMessage{
		ID:           15,
		ChatID:       -100333,
		ChatTitle:    "RAG测试频道",
		ChatUsername: "rag_ch",
		IsBroadcast:  true,
		Text:         "新版修订正文",
		Date:         time.Now().UTC(),
		EditDate:     &editNow,
	})

	// 同步测试 IndexImageText（media.IndexPublisher 接口）。
	ragUser := "rag_ch"
	ok, err := h.IndexImageText(context.Background(), db.MessageImageJob{
		ChannelID:       "333",
		ChannelName:     "RAG测试频道",
		ChannelUsername: &ragUser,
		MessageID:       16,
		SourceDate:      time.Now().UTC(),
	}, "图片说明\n\n[Image OCR]\nOCR内容")
	if err != nil || !ok {
		t.Fatalf("IndexImageText = (%v, %v)", ok, err)
	}

	h.Stop()

	cnt, err := store.Count(context.Background(), "messages")
	if err != nil || cnt != 2 {
		t.Fatalf("expected 2 indexed messages, got %d (err=%v)", cnt, err)
	}
	row, err := store.GetDocument(context.Background(), "messages", "333:15")
	if err != nil || row == nil || row.Document != "新版修订正文" {
		t.Fatalf("expected latest edited document '新版修订正文', got %+v (err=%v)", row, err)
	}

	// 删除消息 15 后，向量与归档文件均被清除。
	h.HandleDeletedEvent(context.Background(), mtproto.DeletedMessagesEvent{
		ChannelID:  333,
		MessageIDs: []int{15},
	})
	cntAfter, _ := store.Count(context.Background(), "messages")
	if cntAfter != 1 {
		t.Fatalf("expected 1 message after delete, got %d", cntAfter)
	}
	if _, statErr := os.Stat(vectors.SourcePath(filepath.Join(dir, "sources"), "333", 15)); !os.IsNotExist(statErr) {
		t.Fatalf("expected source archive file deleted, statErr=%v", statErr)
	}
}

func TestOlderBatchCannotOverwriteImageIndex(t *testing.T) {
	dir := t.TempDir()
	store := vectors.NewReadWrite(filepath.Join(dir, "vectors"), 4)
	defer store.Close()

	started := make(chan struct{})
	release := make(chan struct{})
	releaseEmbedding := sync.OnceFunc(func() { close(release) })
	h := NewHandler(Deps{
		VectorStore: store, SourceArchDir: filepath.Join(dir, "sources"), BatchSize: 1,
		EmbedFn: func(ctx context.Context, texts []string) ([][]float32, error) {
			if texts[0] == "caption" {
				close(started)
				select {
				case <-release:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			return testEmbeddings(texts), nil
		},
	})
	h.Start(t.Context())
	defer func() {
		releaseEmbedding()
		h.Stop()
	}()
	if !h.EnqueueMessage("333", "channel", "", 15, 42, "caption", time.Now()) {
		t.Fatal("enqueue failed")
	}
	awaitIndexTestSignal(t, started)

	const recognized = "caption\n\n[Image OCR]\nrecognized text"
	ok, err := h.IndexImageText(t.Context(), db.MessageImageJob{ChannelID: "333", MessageID: 15}, recognized)
	if err != nil || !ok {
		t.Fatalf("publish OCR: %v, %v", ok, err)
	}
	releaseEmbedding()
	h.Stop()

	row, err := store.GetDocument(t.Context(), "messages", "333:15")
	if err != nil || row == nil || row.Document != recognized {
		t.Fatalf("OCR should survive the older batch, got %+v (%v)", row, err)
	}
	if len(h.generations) != 0 {
		t.Fatalf("generation state was retained: %v", h.generations)
	}
}

func TestChannelDeletionRevokesImagePublication(t *testing.T) {
	dir := t.TempDir()
	store := vectors.NewReadWrite(filepath.Join(dir, "vectors"), 4)
	defer store.Close()

	started := make(chan struct{})
	release := make(chan struct{})
	releaseEmbedding := sync.OnceFunc(func() { close(release) })
	defer releaseEmbedding()
	lease := "claimed"
	job := db.MessageImageJob{ChannelID: "333", MessageID: 15, Revision: "current", LeaseToken: &lease}
	repo := &testImageJobRepo{job: job}
	h := NewHandler(Deps{
		VectorStore: store, SourceArchDir: filepath.Join(dir, "sources"),
		ImagePipeline: media.NewImagePipeline(media.PipelineOptions{Store: repo, RootDir: filepath.Join(dir, "images")}),
		EmbedFn: func(ctx context.Context, texts []string) ([][]float32, error) {
			close(started)
			select {
			case <-release:
				return testEmbeddings(texts), nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		},
	})
	published := make(chan error, 1)
	go func() {
		_, err := h.IndexImageText(t.Context(), job, "OCR content")
		published <- err
	}()
	awaitIndexTestSignal(t, started)
	h.HandleDeletedEvent(t.Context(), mtproto.DeletedMessagesEvent{ChannelID: 333, MessageIDs: []int{15}})
	if !repo.job.Deleted || repo.job.LeaseToken != nil || repo.job.Revision == job.Revision {
		t.Fatalf("image job was not invalidated: %+v", repo.job)
	}
	releaseEmbedding()
	select {
	case err := <-published:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("image publisher did not return")
	}
	// A worker that read the job before deletion must also be rejected at admission.
	if _, err := h.IndexImageText(t.Context(), job, "late OCR content"); err != nil {
		t.Fatal(err)
	}
	if n, err := store.Count(t.Context(), "messages"); err != nil || n != 0 {
		t.Fatalf("deleted content was indexed: count=%d, err=%v", n, err)
	}
	if _, err := os.Stat(vectors.SourcePath(h.deps.SourceArchDir, "333", 15)); !os.IsNotExist(err) {
		t.Fatalf("deleted content was archived: %v", err)
	}
	if len(h.generations) != 0 {
		t.Fatalf("generation state was retained: %v", h.generations)
	}
}

func TestFailedBatchClearsGeneration(t *testing.T) {
	dir := t.TempDir()
	store := vectors.NewReadWrite(filepath.Join(dir, "vectors"), 4)
	defer store.Close()
	embedErr := errors.New("embedding failed")
	h := NewHandler(Deps{
		VectorStore: store, SourceArchDir: filepath.Join(dir, "sources"),
		EmbedFn: func(context.Context, []string) ([][]float32, error) { return nil, embedErr },
	})
	_, err := h.IndexImageText(t.Context(), db.MessageImageJob{ChannelID: "333", MessageID: 15}, "caption")
	if !errors.Is(err, embedErr) || len(h.generations) != 0 {
		t.Fatalf("want embedding failure with no retained generation, got error %v, generations %v", err, h.generations)
	}
}

type testImageJobRepo struct {
	media.ImageRepo
	job db.MessageImageJob
}

func (r *testImageJobRepo) MarkMessageImageDeleted(_ context.Context, channelID string, messageID int64) error {
	if channelID == r.job.ChannelID && messageID == r.job.MessageID {
		r.job.Deleted = true
		r.job.Revision = "deleted"
		r.job.LeaseToken = nil
	}
	return nil
}

func (r *testImageJobRepo) GetMessageImage(context.Context, string, int64) (*db.MessageImageJob, error) {
	copy := r.job
	return &copy, nil
}

func awaitIndexTestSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(3 * time.Second):
		t.Fatal("embedding did not start")
	}
}

func testEmbeddings(texts []string) [][]float32 {
	embedded := make([][]float32, len(texts))
	for i := range embedded {
		embedded[i] = []float32{0.5, 0.5, 0.5, 0.5}
	}
	return embedded
}
