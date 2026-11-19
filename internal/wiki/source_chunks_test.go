// Copyright 2026 Sakura-Bot
// SPDX-License-Identifier: AGPL-3.0-or-later

package wiki

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"unicode/utf8"
)

type chunkFixtureModel struct {
	calls atomic.Int32
}

func (m *chunkFixtureModel) Call(_ context.Context, req LLMCallRequest) (*LLMCallResult, error) {
	m.calls.Add(1)
	if !strings.Contains(req.System, "按来源分段") {
		return nil, fmt.Errorf("unexpected model request for cached album or topic")
	}
	return &LLMCallResult{
		Content:      "## [0] 测试 / 缺失分块的普通消息\n\n这是一条验证增量提取可恢复的记录。",
		FinishReason: "stop",
	}, nil
}

func TestWikiUpdateRetainsLegacyEvidenceAndRepairsMissingChunks(t *testing.T) {
	store := isolatedWikiStore(t)
	ctx := t.Context()
	if err := store.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	ch, err := store.RegisterChannel(ctx, ChannelEntity{ID: 900002, Title: "Mixed IDs", Username: "wiki_mixed_ids", Broadcast: true}, "")
	if err != nil {
		t.Fatal(err)
	}
	cfg := DefaultWikiConfig()
	cfg.Enabled = true
	if err := store.ConfigureChannel(ctx, ch.ID, cfg, "integration"); err != nil {
		t.Fatal(err)
	}
	ch.Config = cfg
	at := NowUTC()
	album := "message:10003"
	captionID, imageID, standaloneID := strings.Repeat("a", 64), strings.Repeat("b", 64), strings.Repeat("c", 64)
	captionHash, imageHash, standaloneHash := strings.Repeat("d", 64), strings.Repeat("e", 64), strings.Repeat("f", 64)
	legacyChunkID := strings.Repeat("1", 64)
	const caption = "旧版图文的配文，用于确认证据引用能继续使用。"
	const imageText = "图片 OCR 说明，之前的分块没有写入。"
	const standalone = "单独的一条频道消息，也缺少持久化分块。"

	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := store.DB.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	for _, source := range []struct {
		id, kind, external, body, hash string
		messageID                      int64
		group, mediaKey                any
	}{
		{captionID, "message", "10003", caption, captionHash, 10003, album, "fixture-image"},
		{imageID, "image", "10003", imageText, imageHash, 10003, album, "fixture-image"},
		{standaloneID, "message", "10004", standalone, standaloneHash, 10004, nil, nil},
	} {
		exec(`INSERT INTO wiki_sources
			(id,channel_id,source_type,external_id,message_id,body,original_length,content_hash,
			 created_at,edited_at,observed_at,verified,media_group,media_key)
			 VALUES (?,?,?,?,?,?,?,?,?,?,UTC_TIMESTAMP(6),TRUE,?,?)`,
			source.id, ch.ID, source.kind, source.external, source.messageID, source.body,
			utf8.RuneCountInString(source.body), source.hash, at, at, source.group, source.mediaKey)
	}
	exec("INSERT INTO wiki_chunks (id,source_id,content_hash,start_offset,end_offset) VALUES (?,?,?,?,?)",
		legacyChunkID, captionID, captionHash, 0, utf8.RuneCountInString(caption))

	mid := int64(10003)
	gotID, err := store.PutSource(ctx, PutSourceInput{
		ChannelID: ch.ID, SourceType: "message", ExternalID: "10003", MessageID: &mid,
		Body: caption, MediaKey: "fixture-image", CreatedAt: at, Verified: true,
	})
	if err != nil || gotID != captionID {
		t.Fatalf("legacy source should be refreshed without a new ID: %q, %v", gotID, err)
	}

	const modelName = "fixture-model"
	const fixtureEndpoint = "http://example.test/v1"
	parts := []string{
		Digest(captionID, captionHash, 0, utf8.RuneCountInString(caption)),
		Digest(imageID, imageHash, 0, utf8.RuneCountInString(imageText)),
	}
	cacheKey := Digest("wiki-captioned-message-1", ch.ID, album, cfg.Language, modelName, fixtureEndpoint, parts)
	exec("INSERT INTO wiki_profile_cache VALUES (?,?,?,?,UTC_TIMESTAMP(6))",
		cacheKey, ch.ID, EncodeJSON(map[string]any{"text": "## [0] 测试 / 旧版图文\n\n图文内容来自此前已经保存的模型输出。"}), "{}")
	id, err := store.EnqueueJob(ctx, nil, ch.ID, "update", "integration", "", false)
	if err != nil {
		t.Fatal(err)
	}
	job, err := store.ClaimJob(ctx)
	if err != nil || job == nil || job.ID != id {
		t.Fatalf("did not claim fixture job: %v (%v)", job, err)
	}
	model := &chunkFixtureModel{}
	engine := NewEngine(store, func(string) LLMRoute {
		return LLMRoute{Caller: model, Model: modelName, BaseURL: fixtureEndpoint}
	}, nil)
	if err := engine.Generate(ctx, job, ch); err != nil {
		t.Fatal(err)
	}
	if job.Phase != "assemble" || model.calls.Load() != 1 {
		t.Fatalf("cached album should not call the model, phase=%s calls=%d", job.Phase, model.calls.Load())
	}
	var factCount int
	if err := store.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM wiki_facts WHERE chunk_id=?", legacyChunkID).Scan(&factCount); err != nil || factCount == 0 {
		t.Fatalf("album fact did not use the stored legacy chunk: count=%d err=%v", factCount, err)
	}
	var pending bool
	if err := store.DB.QueryRowContext(ctx, "SELECT pending FROM wiki_albums WHERE channel_id=? AND media_group=?", ch.ID, album).Scan(&pending); err != nil || pending {
		t.Fatalf("album did not finish: pending=%t err=%v", pending, err)
	}
	var repaired int
	if err := store.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM wiki_chunks WHERE source_id IN (?,?)", imageID, standaloneID).Scan(&repaired); err != nil || repaired != 2 {
		t.Fatalf("missing chunks were not recreated once: count=%d err=%v", repaired, err)
	}
}
