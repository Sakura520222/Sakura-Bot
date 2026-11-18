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
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Sakura520222/Sakura-Bot/internal/config"
	"github.com/Sakura520222/Sakura-Bot/internal/db"
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
