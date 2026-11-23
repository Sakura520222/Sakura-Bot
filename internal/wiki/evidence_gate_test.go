// Copyright 2026 Sakura-Bot
// 本项目采用 AGPL-3.0 许可
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot

package wiki

import (
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestPublishableEvidenceSQLMirrorsRevisionEvidenceGate(t *testing.T) {
	space := regexp.MustCompile(`\s+`)
	gate := space.ReplaceAllString(RevisionEvidenceSQL("r", "p"), " ")
	valid := space.ReplaceAllString(publishableEvidenceSQL(), " ")
	// 发布校验拒绝的每一种情况，装配谓词都必须要求其反面成立。
	for _, rejected := range []string{
		"s.deleted=TRUE", "s.hidden=TRUE", "s.excluded=TRUE", "s.verified=FALSE", "s.content_hash<>ev.source_hash",
	} {
		if !strings.Contains(gate, rejected) {
			t.Fatalf("发布校验不再拒绝 %q，装配谓词需要同步调整", rejected)
		}
	}
	for _, required := range []string{
		"s.deleted=FALSE", "s.hidden=FALSE", "s.excluded=FALSE", "s.verified=TRUE", "s.content_hash=ch.content_hash",
	} {
		if !strings.Contains(valid, required) {
			t.Fatalf("装配谓词缺少发布校验要求的条件 %q", required)
		}
	}
	// 孤儿图片：两处必须使用同一段父帖有效性子查询。
	parent := space.ReplaceAllString(imageParentValidSQL(), " ")
	start := strings.Index(parent, "EXISTS (SELECT 1 FROM wiki_sources parent")
	if start < 0 || !strings.HasSuffix(parent, ")") {
		t.Fatalf("unexpected image parent predicate: %s", parent)
	}
	if !strings.Contains(gate, "NOT "+parent[start:len(parent)-1]) {
		t.Fatalf("发布校验与装配谓词对图片父帖的判定不一致：\n%s\n%s", gate, parent)
	}
}

func TestDropEvidenceChunksKeepsOnlyPublishableCitations(t *testing.T) {
	cite := func(chunk string) map[string]any {
		return map[string]any{"chunk_id": chunk, "start": 0, "end": 3}
	}
	twoSources := map[string]any{"key": "a", "text": "同时引用两个来源", "evidence": []any{cite("revoked"), cite("kept")}}
	onlyRevoked := map[string]any{"key": "b", "text": "只引用被撤销的来源", "evidence": []any{cite("revoked")}}
	untouched := map[string]any{"key": "c", "text": "引用仍然有效", "evidence": []any{cite("kept")}}
	noCitation := map[string]any{"key": "d", "text": "本来就没有引证"}
	facts := []map[string]any{twoSources, onlyRevoked, untouched, noCitation}
	// outline 返回事实的骨架：键与各自保留的引证分块，例如 "a[kept]"。
	outline := func(list []map[string]any) string {
		var parts []string
		for _, fact := range list {
			var chunks []string
			if ev, ok := fact["evidence"].([]any); ok {
				for _, raw := range ev {
					chunks = append(chunks, mapString(raw.(map[string]any), "chunk_id"))
				}
			}
			parts = append(parts, mapString(fact, "key")+"["+strings.Join(chunks, " ")+"]")
		}
		return strings.Join(parts, ",")
	}
	const intact = "a[revoked kept],b[revoked],c[kept],d[]"
	if outline(facts) != intact {
		t.Fatalf("unexpected fixture: %s", outline(facts))
	}

	got := dropEvidenceChunks(facts, map[string]bool{"revoked": true})

	if outline(got) != "a[kept],c[kept],d[]" {
		t.Fatalf("失效引证应被剔除、失去全部引证的断言应被丢弃、其余原样保留：%s", outline(got))
	}
	if outline(facts) != intact {
		t.Fatalf("传入的事实被修改：%s", outline(facts))
	}
	if again := dropEvidenceChunks(facts, nil); outline(again) != intact {
		t.Fatalf("没有失效引证时应保留全部事实：%s", outline(again))
	}
}

func TestStaleEvidenceErrorKeepsSentinelAndHidesInternalIDs(t *testing.T) {
	err := error(&staleEvidenceError{PageID: "page-id", RevisionID: "revision-id"})
	if !errors.Is(err, errPublicationEvidence) {
		t.Fatal("带定位信息的错误必须仍被识别为证据失效")
	}
	var stale *staleEvidenceError
	if !errors.As(err, &stale) || stale.PageID != "page-id" || stale.RevisionID != "revision-id" {
		t.Fatal("未能从错误中取回词条与修订")
	}
	if detail, code, delay := jobFailure(err, 0); detail != "修订引用的来源已失效" || code != "ValueError" || delay != 1 {
		t.Fatalf("任务失败分类发生变化：%q %s %d", detail, code, delay)
	}
}

func TestEvidenceRefreshWorthwhile(t *testing.T) {
	for _, test := range []struct {
		name       string
		refreshes  int
		generation any // nil 表示未记录装配时的来源版本
		current    int64
		want       bool
	}{
		{"来源未变：重新整理只会得到同样结果", 0, int64(7), 7, false},
		{"未记录来源版本（升级前装配的任务）：保守刷新", 0, nil, 7, true},
		{"来源已变：刷新可以吸收并发修改", 0, int64(7), 8, true},
		{"从数据库重新加载后的数值类型", 0, float64(7), 7, false},
		{"来源在变但已达到刷新上限", maxEvidenceRefreshes, int64(7), 9, false},
		{"来源未变且已刷新过", 1, int64(7), 7, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			cursor, progress := map[string]any{}, map[string]any{}
			if test.refreshes > 0 {
				cursor["evidence_refreshes"] = test.refreshes
			}
			if test.generation != nil {
				progress["assembled_generation"] = test.generation
			}
			if got := evidenceRefreshWorthwhile(cursor, progress, test.current); got != test.want {
				t.Fatalf("worthwhile=%t, want %t", got, test.want)
			}
		})
	}
}

// evidenceGateFixture 构造一个带图帖子的频道：一条配文、一张配图及各自分块，
// 以及一个同时引用两者的词条。fixture 内每个分块 ID 的首字符互不相同，便于断言证据行。
type evidenceGateFixture struct {
	store     *Store
	knowledge *Knowledge
	channel   string
	page      string
	job       *JobRecord

	caption, image           string
	captionChunk, imageChunk string
	captionHash, imageHash   string
}

func newEvidenceGateFixture(t *testing.T) *evidenceGateFixture {
	t.Helper()
	store := isolatedWikiStore(t)
	ctx := t.Context()
	if err := store.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	ch, err := store.RegisterChannel(ctx, ChannelEntity{ID: 900003, Title: "Evidence gate", Username: "wiki_evidence_gate", Broadcast: true}, "")
	if err != nil {
		t.Fatal(err)
	}
	cfg := DefaultWikiConfig()
	cfg.Enabled = true
	if err := store.ConfigureChannel(ctx, ch.ID, cfg, "integration"); err != nil {
		t.Fatal(err)
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := store.DB.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}

	f := &evidenceGateFixture{
		store: store, knowledge: NewKnowledge(store), channel: ch.ID, page: NewID(),
		caption: strings.Repeat("a", 64), image: strings.Repeat("b", 64),
		captionChunk: strings.Repeat("1", 64), imageChunk: strings.Repeat("2", 64),
		captionHash: strings.Repeat("d", 64), imageHash: strings.Repeat("e", 64),
	}
	at := NowUTC()
	const album = "message:20001"
	for _, source := range []struct{ id, kind, body, hash, chunk string }{
		{f.caption, "message", "图文帖子的配文", f.captionHash, f.captionChunk},
		{f.image, "image", "[Image OCR] 海报", f.imageHash, f.imageChunk},
	} {
		exec(`INSERT INTO wiki_sources
			(id,channel_id,source_type,external_id,message_id,body,original_length,content_hash,
			 created_at,edited_at,observed_at,verified,media_group,media_key)
			VALUES (?,?,?,?,?,?,?,?,?,?,UTC_TIMESTAMP(6),TRUE,?,?)`,
			source.id, ch.ID, source.kind, "20001", 20001, source.body, utf8.RuneCountInString(source.body), source.hash, at, at, album, "fixture-photo")
		exec("INSERT INTO wiki_chunks (id,source_id,content_hash,start_offset,end_offset) VALUES (?,?,?,?,?)",
			source.chunk, source.id, source.hash, 0, utf8.RuneCountInString(source.body))
	}
	exec("INSERT INTO wiki_pages (id,channel_id,entity_key,title,slug,kind,topic,updated_at) VALUES (?,?,?,?,?,?,?,UTC_TIMESTAMP(6))",
		f.page, ch.ID, f.page, "卡住的词条", f.page, "Topic", "活动")
	exec("INSERT INTO wiki_pages (id,channel_id,entity_key,title,slug,kind,topic,hidden,updated_at) VALUES (?,?,?,?,?,?,?,TRUE,UTC_TIMESTAMP(6))",
		"hidden-page", ch.ID, "hidden-key", "已隐藏的词条", "hidden-slug", "Topic", "活动")

	cite := func(source, chunk string, end int) map[string]any {
		return map[string]any{"source_id": source, "chunk_id": chunk, "quote": "引文", "start": 0, "end": end}
	}
	fact := func(key, text, chunk, hash string, citations ...map[string]any) {
		exec("INSERT INTO wiki_facts (id,channel_id,page_id,chunk_id,source_hash,fact) VALUES (?,?,?,?,?,?)",
			Digest(f.page, key), ch.ID, f.page, chunk, hash,
			EncodeJSON(map[string]any{"key": key, "text": text, "entity": "卡住的词条", "kind": "Fact", "topic": "活动",
				"status": "active", "confidence": 0.9, "evidence": citations}))
	}
	fact("时间", "活动于三月举行，同时引用配文和配图", f.captionChunk, f.captionHash, cite(f.caption, f.captionChunk, 4), cite(f.image, f.imageChunk, 4))
	fact("海报", "海报写有日期，只引用配图", f.imageChunk, f.imageHash, cite(f.image, f.imageChunk, 8))

	f.job = f.claimJob(t)
	return f
}

func (f *evidenceGateFixture) claimJob(t *testing.T) *JobRecord {
	t.Helper()
	if _, err := f.store.EnqueueJob(t.Context(), nil, f.channel, "update", "integration", "", false); err != nil {
		t.Fatal(err)
	}
	job, err := f.store.ClaimJob(t.Context())
	if err != nil || job == nil {
		t.Fatalf("did not claim fixture job: %v (%v)", job, err)
	}
	return job
}

// evidence 返回修订证据行的紧凑表示，例如 "1:0,2:0" 表示配文分块与配图分块各一条、起点 0。
func (f *evidenceGateFixture) evidence(t *testing.T, revision string) string {
	t.Helper()
	rows, err := f.store.DB.QueryContext(t.Context(),
		"SELECT chunk_id, start_offset FROM wiki_evidence WHERE revision_id=? ORDER BY chunk_id, start_offset", revision)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var parts []string
	for rows.Next() {
		var chunk string
		var start int
		if err := rows.Scan(&chunk, &start); err != nil {
			t.Fatal(err)
		}
		parts = append(parts, fmt.Sprintf("%c:%d", chunk[0], start))
	}
	return strings.Join(parts, ",")
}

// publishable 用发布校验本身的谓词判断修订此刻能否放行。
func (f *evidenceGateFixture) publishable(t *testing.T, revision string) bool {
	t.Helper()
	var id string
	err := f.store.DB.QueryRowContext(t.Context(), fmt.Sprintf(`
		SELECT r.id FROM wiki_page_revisions r
		JOIN wiki_pages p ON p.id=r.page_id
		WHERE r.id=? AND %s
	`, RevisionEvidenceSQL("r", "p")), revision).Scan(&id)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
	return err == nil
}

func (f *evidenceGateFixture) generation(t *testing.T) int64 {
	t.Helper()
	channel, err := f.store.GetChannel(t.Context(), f.channel)
	if err != nil || channel == nil {
		t.Fatalf("channel unavailable: %v (%v)", channel, err)
	}
	return channel.SourceGeneration
}

func TestBuildPageOnlyStagesEvidenceThePublicationGateAccepts(t *testing.T) {
	f := newEvidenceGateFixture(t)
	store, ctx := f.store, t.Context()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := store.DB.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	page := PageRecord{ID: f.page, ChannelID: f.channel, Title: "卡住的词条", Kind: "Topic", Topic: "活动"}
	build := func() string {
		t.Helper()
		revision, err := f.knowledge.BuildPage(ctx, f.job, page, 0)
		if err != nil {
			t.Fatal(err)
		}
		return revision
	}
	const complete = "1:0,2:0,2:0"

	first := build()
	if first == "" || f.evidence(t, first) != complete || !f.publishable(t, first) {
		t.Fatalf("valid facts should build a publishable revision: %q evidence=%s", first, f.evidence(t, first))
	}
	if again := build(); again != first {
		t.Fatalf("unchanged content should reuse the valid revision: %q != %q", again, first)
	}

	// 配图被屏蔽：锚点在配文上的断言仍然有效，但它对配图的引证必须剔除，否则修订无法发布。
	exec("UPDATE wiki_sources SET excluded=TRUE WHERE id=?", f.image)
	second := build()
	if second == "" || second == first || f.evidence(t, second) != "1:0" || !f.publishable(t, second) {
		t.Fatalf("revoked citation should be pruned: %q evidence=%s", second, f.evidence(t, second))
	}
	var claims, citations int
	if err := store.DB.QueryRowContext(ctx,
		"SELECT JSON_LENGTH(document,'$.claims'), JSON_LENGTH(document,'$.claims[0].evidence') FROM wiki_page_revisions WHERE id=?",
		second).Scan(&claims, &citations); err != nil {
		t.Fatal(err)
	}
	if claims != 1 || citations != 1 {
		t.Fatalf("document should keep one claim with one citation: claims=%d citations=%d", claims, citations)
	}

	// 配图恢复后回到完整内容；随后让这个修订额外带上一条被屏蔽来源的证据，模拟内容没变但证据已失效的旧修订。
	exec("UPDATE wiki_sources SET excluded=FALSE WHERE id=?", f.image)
	third := build()
	if third == second || f.evidence(t, third) != complete {
		t.Fatalf("restored citation should rebuild the complete revision: %q evidence=%s", third, f.evidence(t, third))
	}
	hidden, hiddenChunk, hiddenHash := strings.Repeat("c", 64), strings.Repeat("3", 64), strings.Repeat("f", 64)
	const hiddenBody = "被屏蔽的消息"
	exec(`INSERT INTO wiki_sources
		(id,channel_id,source_type,external_id,message_id,body,original_length,content_hash,
		 created_at,edited_at,observed_at,verified,hidden)
		VALUES (?,?,?,?,?,?,?,?,?,?,UTC_TIMESTAMP(6),TRUE,TRUE)`,
		hidden, f.channel, "message", "20002", 20002, hiddenBody, utf8.RuneCountInString(hiddenBody), hiddenHash, NowUTC(), NowUTC())
	exec("INSERT INTO wiki_chunks (id,source_id,content_hash,start_offset,end_offset) VALUES (?,?,?,?,?)",
		hiddenChunk, hidden, hiddenHash, 0, utf8.RuneCountInString(hiddenBody))
	exec("INSERT INTO wiki_evidence (revision_id,source_id,source_hash,chunk_id,start_offset,end_offset) VALUES (?,?,?,?,0,?)",
		third, hidden, hiddenHash, hiddenChunk, utf8.RuneCountInString(hiddenBody))
	if f.publishable(t, third) {
		t.Fatal("fixture should have made the previous revision unpublishable")
	}
	fourth := build()
	if fourth == third || f.evidence(t, fourth) != complete || !f.publishable(t, fourth) {
		t.Fatalf("a revision with revoked evidence must not be reused: %q evidence=%s", fourth, f.evidence(t, fourth))
	}
	var sameContent int
	if err := store.DB.QueryRowContext(ctx,
		"SELECT a.content_hash=b.content_hash FROM wiki_page_revisions a, wiki_page_revisions b WHERE a.id=? AND b.id=?",
		third, fourth).Scan(&sameContent); err != nil {
		t.Fatal(err)
	}
	if sameContent != 1 {
		t.Fatal("重建的修订应与失效修订内容一致，只是不再带着失效证据")
	}

	// 帖子的照片在 Telegram 侧消失：配文内容版本变化，配图成为孤儿图片，词条不再有可用事实。
	mid := int64(20001)
	if _, err := store.PutSource(ctx, PutSourceInput{
		ChannelID: f.channel, SourceType: "message", ExternalID: "20001", MessageID: &mid,
		Body: "图文帖子的配文", CreatedAt: NowUTC(), Verified: true,
	}); err != nil {
		t.Fatal(err)
	}
	if revision := build(); revision != "" {
		t.Fatalf("a page left without publishable facts should be dropped, got %q evidence=%s", revision, f.evidence(t, revision))
	}
	var revisions int
	if err := store.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM wiki_page_revisions WHERE page_id=?", f.page).Scan(&revisions); err != nil {
		t.Fatal(err)
	}
	if revisions != 4 {
		t.Fatalf("dropping the page must not create a revision: %d revisions", revisions)
	}
}

func TestStaleEvidenceFailureStopsTheJobWithoutSpendingRetries(t *testing.T) {
	f := newEvidenceGateFixture(t)
	store, ctx := f.store, t.Context()
	runtime := NewRuntime(store, nil, nil)
	current := f.generation(t)

	state := func(jobID string) (status string, attempts int, errCode sql.NullString, phase string) {
		t.Helper()
		if err := store.DB.QueryRowContext(ctx,
			"SELECT status, attempts, error_code, phase FROM wiki_jobs WHERE id=?", jobID).
			Scan(&status, &attempts, &errCode, &phase); err != nil {
			t.Fatal(err)
		}
		return status, attempts, errCode, phase
	}
	detail := func(jobID string) string {
		t.Helper()
		job, err := getJobTx(ctx, store.DB, jobID)
		if err != nil || job == nil {
			t.Fatalf("job unavailable: %v (%v)", job, err)
		}
		return mapString(job.Progress, "error_detail")
	}
	dirtyPages := func() int {
		t.Helper()
		var count int
		if err := store.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM wiki_dirty_pages WHERE channel_id=?", f.channel).Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}
	// ready 把一个已认领的任务置为“在发布阶段失败”：assembled 是装配时记录的来源版本。
	// fixture 认领的任务仍占着频道，EnqueueJob 遇到活动任务会原样返回它，所以第一个场景直接复用该任务；
	// 后面的场景等上一个任务结束、释放频道之后再认领新任务。
	ready := func(job *JobRecord, assembled int64, refreshes int) *JobRecord {
		t.Helper()
		job.Phase = "ready"
		job.Progress["assembled_generation"] = assembled
		if refreshes > 0 {
			job.Cursor["evidence_refreshes"] = refreshes
		}
		return job
	}

	// 来源自装配以来没有变化：重新整理不会有新结果，任务直接结束并指出词条。
	job := ready(f.job, current, 0)
	if err := runtime.handleJobError(ctx, job, &staleEvidenceError{PageID: f.page, RevisionID: "stale"}); err != nil {
		t.Fatal(err)
	}
	status, attempts, errCode, phase := state(job.ID)
	if status != "failed" || attempts != 1 || errCode.String != "ValueError" || phase != "assemble" {
		t.Fatalf("deterministic failure should end the job: status=%s attempts=%d code=%q phase=%s", status, attempts, errCode.String, phase)
	}
	if text := detail(job.ID); !strings.Contains(text, "卡住的词条") || !strings.Contains(text, "已停止重试") {
		t.Fatalf("failure should name the page: %q", text)
	}
	if dirtyPages() != 0 {
		t.Fatalf("a deterministic failure must not mark every page dirty: %d", dirtyPages())
	}
	if channel, _ := store.GetChannel(ctx, f.channel); channel == nil || channel.ActiveJob != "" {
		t.Fatalf("failed job should release the channel: %+v", channel)
	}

	// 来源在变，但已经刷新过上限次数：同样不再刷新。
	job = ready(f.claimJob(t), current-1, maxEvidenceRefreshes)
	if err := runtime.handleJobError(ctx, job, &staleEvidenceError{PageID: f.page, RevisionID: "stale"}); err != nil {
		t.Fatal(err)
	}
	if status, _, _, _ = state(job.ID); status != "failed" {
		t.Fatalf("refresh limit should stop the job: %s", status)
	}

	// 没有定位到词条的错误（例如审校阶段发现）也要结束，并给出通用说明。
	job = ready(f.claimJob(t), current, 0)
	if err := runtime.handleJobError(ctx, job, errPublicationEvidence); err != nil {
		t.Fatal(err)
	}
	if status, _, _, _ = state(job.ID); status != "failed" || detail(job.ID) != "部分修订引用了已失效的来源，重新整理也无法修复，已停止重试" {
		t.Fatalf("unexpected result without page info: status=%s detail=%q", status, detail(job.ID))
	}

	// 装配之后来源确实变了：刷新一次。这是恢复步骤，不记错误码，也不占用重试次数。
	job = ready(f.claimJob(t), current-1, 0)
	job.Cursor["revisions"] = map[string]any{f.page: "stale"}
	if err := runtime.handleJobError(ctx, job, &staleEvidenceError{PageID: f.page, RevisionID: "stale"}); err != nil {
		t.Fatal(err)
	}
	status, attempts, errCode, phase = state(job.ID)
	if status != "partial" || attempts != 0 || errCode.Valid || phase != "assemble" {
		t.Fatalf("refresh should be re-queued without spending retries: status=%s attempts=%d code=%q phase=%s", status, attempts, errCode.String, phase)
	}
	after, err := getJobTx(ctx, store.DB, job.ID)
	if err != nil || after == nil {
		t.Fatalf("job unavailable: %v (%v)", after, err)
	}
	if refreshes := mapInt(after.Cursor, "evidence_refreshes"); refreshes != 1 {
		t.Fatalf("refresh should be counted: %d", refreshes)
	}
	if len(extractRevisionMap(after.Cursor)) != 0 || dirtyPages() != 1 {
		t.Fatalf("refresh should clear staged revisions and queue only visible pages: revisions=%d dirty=%d",
			len(extractRevisionMap(after.Cursor)), dirtyPages())
	}
}
