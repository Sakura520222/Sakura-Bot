// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package wiki

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	stdhtml "html"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/Sakura520222/Sakura-Bot/internal/db"
)

const (
	pageJoinSQL = `FROM wiki_channels c
		JOIN wiki_publication_pages pp ON pp.publication_id=c.published_revision
		JOIN wiki_pages p ON p.id=pp.page_id AND p.channel_id=c.id
		JOIN wiki_page_revisions r ON r.id=pp.revision_id AND r.channel_id=c.id`

	cardFieldsSQL = `p.id, p.slug, p.kind, p.topic, c.id AS channel_id, c.title AS channel_title,
		c.published_revision, r.id AS revision_id, r.created_at,
		JSON_UNQUOTE(JSON_EXTRACT(r.document,'$.title')) AS title,
		LEFT(JSON_UNQUOTE(JSON_EXTRACT(r.document,'$.claims[0].text')),240) AS excerpt`

	nodeFieldsSQL = `p.id, p.slug, p.kind, p.topic, c.id AS channel_id,
		JSON_UNQUOTE(JSON_EXTRACT(r.document,'$.title')) AS title,
		r.id AS revision_id, c.published_revision AS publication_id`

	evidenceCountSQL = `(SELECT COUNT(DISTINCT ev.source_id) FROM wiki_evidence ev WHERE ev.revision_id=r.id) AS evidence_count`

	graphEdgeJoinSQL = `FROM wiki_publication_edges pe
		JOIN wiki_edges e ON e.id=pe.edge_id
		JOIN wiki_chunks chunk ON chunk.id=e.chunk_id
		JOIN wiki_sources s ON s.id=chunk.source_id`

	graphEdgeVisibleSQL = `s.deleted=FALSE AND s.hidden=FALSE AND s.excluded=FALSE
		AND s.verified=TRUE AND s.content_hash=e.source_hash`

	latestJoinSQL = `FROM wiki_page_revisions r FORCE INDEX (wiki_feed_latest)
		STRAIGHT_JOIN wiki_pages p ON p.id=r.page_id AND p.channel_id=r.channel_id
		STRAIGHT_JOIN wiki_channels c ON c.id=r.channel_id
		STRAIGHT_JOIN wiki_publication_pages pp ON pp.publication_id=c.published_revision
			AND pp.page_id=p.id AND pp.revision_id=r.id`

	publicationFeedJoinSQL = `FROM wiki_channels c
		STRAIGHT_JOIN wiki_publications pub ON pub.id=c.published_revision
		STRAIGHT_JOIN wiki_publication_pages pp ON pp.publication_id=pub.id
		STRAIGHT_JOIN wiki_pages p ON p.id=pp.page_id AND p.channel_id=c.id
		STRAIGHT_JOIN wiki_page_revisions r ON r.id=pp.revision_id AND r.channel_id=c.id`
)

func publishedHistorySQL() string {
	return fmt.Sprintf(`FROM wiki_page_revisions r
		JOIN wiki_pages p ON p.id=r.page_id
		WHERE p.id=? AND r.channel_id=p.channel_id AND %s
		AND EXISTS (
			SELECT 1 FROM wiki_publication_pages pp
			JOIN wiki_publications pub ON pub.id=pp.publication_id
			WHERE pp.page_id=p.id AND pp.revision_id=r.id AND pub.channel_id=p.channel_id
		)`, RevisionVisibleSQL("r", "p"))
}

// SearchOptions 控制公开搜索查询过滤与分页参数。
type SearchOptions struct {
	Query    string
	Channel  string
	Page     int
	PageSize int
	Kind     string
	Topic    string
	After    *time.Time
	Before   *time.Time
}

// FeedOptions 控制公开信息流查询参数（latest / recommended / search）。
type FeedOptions struct {
	Mode    string
	Query   string
	Channel string
	Topic   string
	Kind    string
	After   *time.Time
	Before  *time.Time
	Cursor  string
	Limit   int
}

// PageReadOptions 控制单页公开读取粒度与修订对比参数。
type PageReadOptions struct {
	Part          string // "full" | "article" | "references" | "related"
	Revision      string
	CompareBefore string
	CompareAfter  string
}

// TimelineOptions 控制公开时间线读取参数（支持页码模式与延续游标模式）。
type TimelineOptions struct {
	Page       int
	PageSize   int
	CursorMode bool
	Cursor     string
	Before     *time.Time
}

// GraphOptions 控制知识图谱 BFS 与游标浏览参数。
type GraphOptions struct {
	NodeID   string
	Depth    int
	Limit    int
	Kind     string
	Topic    string
	Relation string
	Query    string
	After    *time.Time
	Before   *time.Time
	Cursor   string
}

// ContributorsOptions 控制公开社区贡献榜与个人贡献历史查询参数。
type ContributorsOptions struct {
	Channel string
	PageID  string
	UserID  string
	Offset  int
	History bool
}

// LikePattern 生成使用 '=' 作为 ESCAPE 字符的安全 LIKE 子串模式（对齐 reader.py:like）。
func LikePattern(value string) string {
	escaped := strings.ReplaceAll(value, "=", "==")
	escaped = strings.ReplaceAll(escaped, "%", "=%")
	escaped = strings.ReplaceAll(escaped, "_", "=_")
	return "%" + escaped + "%"
}

// RevisionBlocks 将修订 document 展开为可逐块比对的线性文本块列表（对齐 reader.py:revision_changes.blocks）。
func RevisionBlocks(document map[string]any) []string {
	if document == nil {
		return nil
	}
	var out []string
	for _, key := range []string{"title", "topic"} {
		if v, exists := document[key]; exists {
			if s, ok := v.(string); ok {
				out = append(out, s)
			}
		}
	}

	appendNotes := func(rawNotes any) {
		arr, ok := rawNotes.([]any)
		if !ok {
			if mSlice, ok2 := rawNotes.([]map[string]any); ok2 {
				for _, nm := range mSlice {
					arr = append(arr, nm)
				}
			}
		}
		for _, rn := range arr {
			note, ok := rn.(map[string]any)
			if !ok {
				continue
			}
			excerpt := mapString(note, "excerpt")
			prefix := "注解"
			if excerpt != "" {
				prefix = fmt.Sprintf("注解「%s」", excerpt)
			}
			lines := []string{fmt.Sprintf("%s　%s", prefix, mapString(note, "text"))}
			lines = append(lines, mapStringSlice(note["sources"])...)
			out = append(out, strings.Join(lines, "\n"))
		}
	}

	appendNotes(document["title_notes"])
	claimsRaw, _ := document["claims"].([]any)
	for _, rc := range claimsRaw {
		if cm, ok := rc.(map[string]any); ok {
			out = append(out, mapString(cm, "text"))
			appendNotes(cm["community_notes"])
		}
	}
	return out
}

// RevisionChanges 计算两个修订文档之间的块级差异对（最多返回 100 项，超出置 truncated=true，对齐 reader.py:revision_changes）。
func RevisionChanges(previous, current map[string]any) ([]map[string]any, bool) {
	oldBlocks := RevisionBlocks(previous)
	newBlocks := RevisionBlocks(current)
	edits := computeDiffEdits(oldBlocks, newBlocks)

	changes := []map[string]any{}
	i := 0
	for i < len(edits) {
		if edits[i].op == ' ' {
			i++
			continue
		}
		var removed, added []string
		for i < len(edits) && edits[i].op != ' ' {
			if edits[i].op == '-' {
				removed = append(removed, edits[i].text)
			} else if edits[i].op == '+' {
				added = append(added, edits[i].text)
			}
			i++
		}
		pairCount := max(len(removed), len(added))
		for k := 0; k < pairCount; k++ {
			if len(changes) == 100 {
				return changes, true
			}
			var bef, aft any
			if k < len(removed) {
				bef = removed[k]
			}
			if k < len(added) {
				aft = added[k]
			}
			changes = append(changes, map[string]any{
				"before": bef,
				"after":  aft,
			})
		}
	}
	return changes, false
}

// RevisionUnifiedDiff 基于两个修订文档的线性文本行生成标准 Unified Diff 字符串。
func RevisionUnifiedDiff(previous, current map[string]any, fromLabel, toLabel string) string {
	oldText := strings.Join(RevisionBlocks(previous), "\n")
	newText := strings.Join(RevisionBlocks(current), "\n")
	var oldLines, newLines []string
	if oldText != "" {
		oldLines = strings.Split(oldText, "\n")
	}
	if newText != "" {
		newLines = strings.Split(newText, "\n")
	}
	lines := UnifiedDiff(oldLines, newLines, fromLabel, toLabel)
	return strings.Join(lines, "\n")
}

// PublicCoverage 白名单脱敏输出公开频道覆盖率字典（对齐 reader.py:public_coverage）。
func PublicCoverage(cov map[string]any) map[string]any {
	keys := []string{
		"status", "reason", "enumerated", "indexed", "excluded",
		"estimated_total", "earliest", "latest", "missing_ranges",
		"failures", "updated_at",
	}
	out := make(map[string]any, len(keys))
	for _, k := range keys {
		out[k] = cov[k]
	}
	return out
}

// PublicNotes 白名单序列化公开社区注解列表（对齐 reader.py:public_notes）。
func PublicNotes(raw any) []map[string]any {
	var items []map[string]any
	switch arr := raw.(type) {
	case []map[string]any:
		items = arr
	case []any:
		for _, item := range arr {
			if m, ok := item.(map[string]any); ok {
				items = append(items, m)
			}
		}
	}
	allowed := []string{"kind", "text", "sources", "excerpt", "excerpt_occurrence"}
	out := make([]map[string]any, 0, len(items))
	for _, note := range items {
		clean := make(map[string]any)
		for _, k := range allowed {
			if v, exists := note[k]; exists {
				clean[k] = v
			}
		}
		out = append(out, clean)
	}
	return out
}

func formatSourceCard(src map[string]any, username string, cfg WikiConfig) map[string]any {
	srcType := mapString(src, "source_type")
	msgID := mapInt64(src, "message_id")
	var link any
	if username != "" && msgID > 0 && cfg.ShowTelegramLinks && srcType != "summary" {
		link = fmt.Sprintf("https://t.me/%s/%d", username, msgID)
	}
	return map[string]any{
		"id":         src["id"],
		"type":       srcType,
		"created_at": src["created_at"],
		"edited_at":  src["edited_at"],
		"url":        link,
	}
}

func formatPublicClaim(fact map[string]any, sources map[string]map[string]any, username string, cfg WikiConfig) map[string]any {
	var evOut []map[string]any
	evArr, _ := fact["evidence"].([]any)
	for _, re := range evArr {
		em, ok := re.(map[string]any)
		if !ok {
			continue
		}
		sid := mapString(em, "source_id")
		var quoteVal any
		if cfg.ShowQuotes {
			quoteVal = em["quote"]
		}
		var urlVal any
		if srcRow, ok := sources[sid]; ok {
			urlVal = formatSourceCard(srcRow, username, cfg)["url"]
		}
		evOut = append(evOut, map[string]any{
			"source_id": sid,
			"start":     em["start"],
			"end":       em["end"],
			"quote":     quoteVal,
			"url":       urlVal,
		})
	}
	if evOut == nil {
		evOut = []map[string]any{}
	}
	return map[string]any{
		"community_notes":   PublicNotes(fact["community_notes"]),
		"key":               fact["key"],
		"text":              fact["text"],
		"status":            fact["status"],
		"confidence":        fact["confidence"],
		"occurred_at":       fact["occurred_at"],
		"valid_until":       fact["valid_until"],
		"source_created_at": fact["source_created_at"],
		"evidence":          evOut,
	}
}

func formatPageCard(row map[string]any) map[string]any {
	var (
		title         string
		excerpt       string
		evidenceCount int
	)
	if docRaw, hasDoc := row["document"]; hasDoc && docRaw != nil {
		var doc map[string]any
		switch v := docRaw.(type) {
		case string:
			doc = decodeMapJSON([]byte(v))
		case []byte:
			doc = decodeMapJSON(v)
		case map[string]any:
			doc = v
		}
		title = mapString(doc, "title")
		claimsRaw, _ := doc["claims"].([]any)
		if len(claimsRaw) > 0 {
			if c0, ok := claimsRaw[0].(map[string]any); ok {
				excerpt = truncateRunesSimple(mapString(c0, "text"), 240)
			}
		}
		seenSrc := map[string]bool{}
		for _, rc := range claimsRaw {
			if cm, ok := rc.(map[string]any); ok {
				evArr, _ := cm["evidence"].([]any)
				for _, re := range evArr {
					if em, ok := re.(map[string]any); ok {
						if sid := mapString(em, "source_id"); sid != "" {
							seenSrc[sid] = true
						}
					}
				}
			}
		}
		evidenceCount = len(seenSrc)
	} else {
		title = mapString(row, "title")
		excerpt = mapString(row, "excerpt")
		evidenceCount = mapInt(row, "evidence_count")
	}

	channelID := mapString(row, "channel_id")
	slug := mapString(row, "slug")
	return map[string]any{
		"id":             row["id"],
		"slug":           slug,
		"title":          title,
		"kind":           row["kind"],
		"topic":          row["topic"],
		"channel_id":     channelID,
		"channel_title":  row["channel_title"],
		"publication_id": row["published_revision"],
		"revision_id":    row["revision_id"],
		"updated_at":     row["created_at"],
		"excerpt":        excerpt,
		"evidence_count": evidenceCount,
		"url":            fmt.Sprintf("/wiki/%s/page/%s", channelID, slug),
	}
}

func formatGraphNode(row map[string]any) map[string]any {
	return map[string]any{
		"id":             row["id"],
		"title":          row["title"],
		"kind":           row["kind"],
		"topic":          row["topic"],
		"revision_id":    row["revision_id"],
		"publication_id": row["publication_id"],
		"url":            fmt.Sprintf("/wiki/%s/page/%s", mapString(row, "channel_id"), mapString(row, "slug")),
	}
}

// PublicReader 实现严格脱敏、REPEATABLE READ 一致性快照隔离的公开 Wiki 只读模型（对齐 core/wiki/reader.py）。
type PublicReader struct {
	store     *Store
	knowledge *Knowledge
}

// NewPublicReader 创建公开 Wiki 读取器。
func NewPublicReader(store *Store) *PublicReader {
	return &PublicReader{
		store:     store,
		knowledge: NewKnowledge(store),
	}
}

func (r *PublicReader) isChannelVisible(ctx context.Context, channelID string) bool {
	var id string
	q := "SELECT c.id FROM wiki_channels c WHERE c.id=? AND " + ChannelVisibleSQL("c")
	err := r.store.exec(ctx).QueryRowContext(ctx, q, channelID).Scan(&id)
	return err == nil && id != ""
}

// Visibility 返回当前所有公开可见频道的 `{published_revision}:{source_generation}:{cover_digest}` 摘要字典。
func (r *PublicReader) Visibility(ctx context.Context) (map[string]string, error) {
	q := fmt.Sprintf(`
		SELECT c.id, c.published_revision, c.source_generation, cover.digest AS cover_digest
		FROM wiki_channels c
		LEFT JOIN wiki_channel_covers cover ON cover.channel_id=c.id
		WHERE %s ORDER BY c.id
	`, ChannelVisibleSQL("c"))
	rows, err := r.store.exec(ctx).QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := map[string]string{}
	for rows.Next() {
		var id, pubRev string
		var gen int64
		var cover sql.NullString
		if err := rows.Scan(&id, &pubRev, &gen, &cover); err != nil {
			return nil, err
		}
		out[id] = fmt.Sprintf("%s:%d:%s", pubRev, gen, cover.String)
	}
	return out, rows.Err()
}

// Bookmarks 按传入 pageIDs 顺序返回当前仍公开可见的词条卡片列表。
func (r *PublicReader) Bookmarks(ctx context.Context, pageIDs []string) ([]map[string]any, error) {
	ordered := UniqueStrings(pageIDs)
	if len(ordered) == 0 {
		return []map[string]any{}, nil
	}
	var out []map[string]any
	err := r.store.WithSnapshot(ctx, func(sCtx context.Context, tx *sql.Tx) error {
		marks, args := stringPlaceholders(ordered)
		q := fmt.Sprintf("SELECT %s, %s %s WHERE p.id IN (%s) AND %s",
			cardFieldsSQL, evidenceCountSQL, pageJoinSQL, marks, VisibleSQL("c", "r", "p"))
		rows, err := QueryRowsMap(sCtx, tx, q, args...)
		if err != nil {
			return err
		}
		byID := make(map[string]map[string]any, len(rows))
		for _, row := range rows {
			byID[mapString(row, "id")] = formatPageCard(row)
		}
		out = make([]map[string]any, 0, len(byID))
		for _, pid := range ordered {
			if card, ok := byID[pid]; ok {
				out = append(out, card)
			}
		}
		return nil
	})
	return out, err
}

// Channels 返回所有拥有至少一个公开可见词条的频道列表（最多 200 个）。
func (r *PublicReader) Channels(ctx context.Context) ([]map[string]any, error) {
	var out []map[string]any
	err := r.store.WithSnapshot(ctx, func(sCtx context.Context, tx *sql.Tx) error {
		q := fmt.Sprintf(`
			SELECT c.id, c.title, c.username, c.coverage, c.updated_at,
			       c.published_revision, COUNT(p.id) AS page_count
			%s WHERE %s
			GROUP BY c.id
			ORDER BY c.updated_at DESC
			LIMIT 200
		`, pageJoinSQL, VisibleSQL("c", "r", "p"))
		rows, err := QueryRowsMap(sCtx, tx, q)
		if err != nil {
			return err
		}
		out = make([]map[string]any, 0, len(rows))
		for _, row := range rows {
			cov := decodeMapJSON([]byte(mapString(row, "coverage")))
			cid := mapString(row, "id")
			out = append(out, map[string]any{
				"id":             cid,
				"title":          row["title"],
				"username":       row["username"],
				"coverage":       PublicCoverage(cov),
				"updated_at":     row["updated_at"],
				"publication_id": row["published_revision"],
				"page_count":     mapInt(row, "page_count"),
				"url":            "/wiki/" + cid,
			})
		}
		return nil
	})
	return out, err
}

// Channel 返回单个公开频道门户信息；若频道不存在或未满足公开可见性条件，返回 (nil, nil)。
func (r *PublicReader) Channel(ctx context.Context, channel string, summary bool) (map[string]any, error) {
	var result map[string]any
	err := r.store.WithSnapshot(ctx, func(sCtx context.Context, tx *sql.Tx) error {
		ch, err := r.store.GetChannel(sCtx, channel)
		if err != nil || ch == nil {
			return err
		}
		if !r.isChannelVisible(sCtx, ch.ID) {
			return nil
		}
		var coverVal any
		if ch.CoverDigest != "" {
			coverVal = fmt.Sprintf("/api/public/wiki/channels/%s/cover?v=%s", ch.ID, ch.CoverDigest)
		} else if ch.Config.CoverURL != "" {
			coverVal = ch.Config.CoverURL
		}
		var usernameVal any
		if ch.Username != "" {
			usernameVal = ch.Username
		}
		base := map[string]any{
			"id":             ch.ID,
			"title":          ch.Title,
			"username":       usernameVal,
			"publication_id": ch.PublishedRevision,
			"updated_at":     FormatISO(ch.UpdatedAt),
			"coverage":       PublicCoverage(ch.Coverage),
			"columns":        ch.Config.Columns,
			"cover_url":      coverVal,
			"description":    ch.Config.Description,
			"source_policy": map[string]any{
				"quotes":         ch.Config.ShowQuotes,
				"telegram_links": ch.Config.ShowTelegramLinks,
			},
		}
		if summary {
			var total int
			qCount := fmt.Sprintf("SELECT COUNT(*) %s WHERE c.id=? AND %s", publicationFeedJoinSQL, VisibleSQL("c", "r", "p"))
			if err := tx.QueryRowContext(sCtx, qCount, ch.ID).Scan(&total); err != nil {
				return err
			}
			qTopics := fmt.Sprintf(`
				SELECT p.topic, COUNT(*) AS total %s
				WHERE c.id=? AND %s
				GROUP BY p.topic ORDER BY total DESC, p.topic LIMIT 8
			`, publicationFeedJoinSQL, VisibleSQL("c", "r", "p"))
			tRows, err := tx.QueryContext(sCtx, qTopics, ch.ID)
			if err != nil {
				return err
			}
			topics := []string{}
			for tRows.Next() {
				var tp string
				var cnt int
				if err := tRows.Scan(&tp, &cnt); err == nil {
					topics = append(topics, tp)
				}
			}
			_ = tRows.Close()
			base["page_count"] = total
			base["topics"] = topics
			result = base
			return nil
		}

		pages, err := r.Search(sCtx, SearchOptions{Channel: ch.ID, Page: 1, PageSize: 12})
		if err != nil {
			return err
		}
		base["pages"] = pages["items"]
		base["page_count"] = pages["total"]
		result = base
		return nil
	})
	return result, err
}

func (r *PublicReader) buildPageFilters(q, resolvedChannelID, kind, topic string, after, before *time.Time, hasChannel bool) (
	conditions []string, params []any, terms []string, ranking string, rankParams []any,
) {
	conditions = []string{VisibleSQL("c", "r", "p")}
	if hasChannel {
		if resolvedChannelID == "" {
			conditions = append(conditions, "FALSE")
		} else {
			conditions = append(conditions, "c.id=?")
			params = append(params, resolvedChannelID)
		}
	}

	terms = UniqueStrings(strings.Fields(q))
	if len(terms) > 0 {
		var matches []string
		var ranks []string
		for _, term := range terms {
			pat := LikePattern(term)
			matches = append(matches, "(r.search_text LIKE ? ESCAPE '=' OR EXISTS (SELECT 1 FROM wiki_entity_aliases a WHERE a.page_id=p.id AND a.name LIKE ? ESCAPE '='))")
			params = append(params, pat, pat)
			ranks = append(ranks, "(CASE WHEN p.title LIKE ? ESCAPE '=' THEN 4 ELSE 0 END + CASE WHEN r.search_text LIKE ? ESCAPE '=' THEN 1 ELSE 0 END)")
			rankParams = append(rankParams, pat, pat)
		}
		conditions = append(conditions, "("+strings.Join(matches, " OR ")+")")
		ranking = strings.Join(ranks, "+")
	} else {
		terms = []string{}
	}

	if kind != "" {
		conditions = append(conditions, "p.kind=?")
		params = append(params, kind)
	}
	if topic != "" {
		conditions = append(conditions, "p.topic=?")
		params = append(params, topic)
	}
	if after != nil && !after.IsZero() {
		conditions = append(conditions, "r.created_at>=?")
		params = append(params, db.MysqlDatetime6(after.UTC()))
	}
	if before != nil && !before.IsZero() {
		conditions = append(conditions, "r.created_at<=?")
		params = append(params, db.MysqlDatetime6(before.UTC()))
	}
	return conditions, params, terms, ranking, rankParams
}

// Search 执行公开百科全文与实体别名加权搜索（标题权重 4 + 全文权重 1）。
func (r *PublicReader) Search(ctx context.Context, opts SearchOptions) (map[string]any, error) {
	page := min(max(opts.Page, 1), 10000)
	pageSize := opts.PageSize
	if pageSize <= 0 {
		pageSize = 20
	}
	pageSize = min(max(pageSize, 1), 50)

	var out map[string]any
	err := r.store.WithSnapshot(ctx, func(sCtx context.Context, tx *sql.Tx) error {
		var resolvedID string
		hasChannel := strings.TrimSpace(opts.Channel) != ""
		if hasChannel {
			ch, err := r.store.GetChannel(sCtx, opts.Channel)
			if err != nil {
				return err
			}
			if ch != nil {
				resolvedID = ch.ID
			}
		}
		conds, params, terms, ranking, rankParams := r.buildPageFilters(
			opts.Query, resolvedID, opts.Kind, opts.Topic, opts.After, opts.Before, hasChannel,
		)
		where := strings.Join(conds, " AND ")
		order := "r.created_at DESC, p.id"
		if ranking != "" {
			order = fmt.Sprintf("(%s) DESC, %s", ranking, order)
		}

		var total int
		if err := tx.QueryRowContext(sCtx, fmt.Sprintf("SELECT COUNT(*) %s WHERE %s", pageJoinSQL, where), params...).Scan(&total); err != nil {
			return err
		}

		allArgs := make([]any, 0, len(params)+len(rankParams)+2)
		allArgs = append(allArgs, params...)
		allArgs = append(allArgs, rankParams...)
		allArgs = append(allArgs, pageSize, (page-1)*pageSize)

		q := fmt.Sprintf(`
			SELECT p.id, p.slug, p.title, p.kind, p.topic, c.id AS channel_id, c.title AS channel_title,
			       c.published_revision, r.id AS revision_id, r.created_at, r.document
			%s WHERE %s
			ORDER BY %s LIMIT ? OFFSET ?
		`, pageJoinSQL, where, order)
		rows, err := QueryRowsMap(sCtx, tx, q, allArgs...)
		if err != nil {
			return err
		}
		items := make([]map[string]any, 0, len(rows))
		for _, row := range rows {
			items = append(items, formatPageCard(row))
		}
		out = map[string]any{
			"items":     items,
			"total":     total,
			"page":      page,
			"page_size": pageSize,
			"terms":     terms,
		}
		return nil
	})
	return out, err
}

// Feed 返回支持延续游标的公开信息流（mode = "latest" | "recommended" | "search"）。
func (r *PublicReader) Feed(ctx context.Context, opts FeedOptions) (map[string]any, error) {
	mode := opts.Mode
	if mode == "" {
		mode = "latest"
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = 20
	}
	limit = min(max(limit, 1), 50)

	var out map[string]any
	err := r.store.WithSnapshot(ctx, func(sCtx context.Context, tx *sql.Tx) error {
		var channelID string
		if strings.TrimSpace(opts.Channel) != "" {
			ch, err := r.store.GetChannel(sCtx, opts.Channel)
			if err != nil || ch == nil || !r.isChannelVisible(sCtx, ch.ID) {
				return err
			}
			channelID = ch.ID
		}
		var afterArg, beforeArg any
		if opts.After != nil && !opts.After.IsZero() {
			afterArg = FormatISO(opts.After.UTC())
		}
		if opts.Before != nil && !opts.Before.IsZero() {
			beforeArg = FormatISO(opts.Before.UTC())
		}
		scope := ScopeKey("feed", mode, nilIfEmpty(channelID), nilIfEmpty(opts.Topic), nilIfEmpty(opts.Kind), opts.Query, afterArg, beforeArg)
		shuffle := mode == "recommended"
		start, seed, position, err := ReadOrInitCursor(opts.Cursor, scope, shuffle)
		if err != nil {
			return err
		}

		conds, params, terms, ranking, rankParams := r.buildPageFilters(
			opts.Query, channelID, opts.Kind, opts.Topic, opts.After, opts.Before, channelID != "",
		)
		if shuffle {
			ranking = `(CASE WHEN r.created_at >= ? THEN 200 ELSE 0 END
				+ MOD(CRC32(CONCAT(?, ':', c.id, ':', p.topic)) + MOD(CRC32(CONCAT(?, ':', p.id)), 97), 127))`
			rankParams = []any{db.MysqlDatetime6(start.Add(-7 * 24 * time.Hour)), seed, seed}
		}

		sortExpr := "0"
		orderPrefix := ""
		if ranking != "" {
			sortExpr = fmt.Sprintf("CAST((%s) AS SIGNED)", ranking)
			orderPrefix = "sort_rank DESC, "
		}
		indexed := ranking == "" && channelID == "" && opts.Topic == "" && opts.Kind == ""
		joinSQL := publicationFeedJoinSQL
		if indexed {
			joinSQL = latestJoinSQL
			conds = append(conds, "(SELECT pub.created_at FROM wiki_publications pub WHERE pub.id=c.published_revision) <= ?")
		} else {
			conds = append(conds, "pub.created_at <= ?")
		}
		params = append(params, db.MysqlDatetime6(start))

		keyset := ""
		if position != nil {
			posDate := db.MysqlDatetime6(position.Date)
			if ranking != "" {
				keyset = "HAVING sort_rank < ? OR (sort_rank = ? AND (created_at, id) < (?, ?))"
				params = append(params, position.Order, position.Order, posDate, position.ID)
			} else {
				conds = append(conds, "(r.created_at < ? OR (r.created_at = ? AND r.page_id < ?))")
				params = append(params, posDate, posDate, position.ID)
			}
		}

		where := strings.Join(conds, " AND ")
		q := fmt.Sprintf(`
			SELECT %s, batch.sort_rank, %s
			FROM (
				SELECT c.id AS channel_id, p.id, r.id AS revision_id, r.created_at, %s AS sort_rank
				%s
				WHERE %s %s
				ORDER BY %s r.created_at DESC, r.page_id DESC
				LIMIT ?
			) batch
			JOIN wiki_channels c ON c.id=batch.channel_id
			JOIN wiki_pages p ON p.id=batch.id
			JOIN wiki_page_revisions r ON r.id=batch.revision_id
			ORDER BY batch.sort_rank DESC, r.created_at DESC, p.id DESC
		`, cardFieldsSQL, evidenceCountSQL, sortExpr, joinSQL, where, keyset, orderPrefix)

		allArgs := make([]any, 0, len(rankParams)+len(params)+1)
		allArgs = append(allArgs, rankParams...)
		allArgs = append(allArgs, params...)
		allArgs = append(allArgs, limit+1)

		rows, err := QueryRowsMap(sCtx, tx, q, allArgs...)
		if err != nil {
			return err
		}
		hasMore := len(rows) > limit
		if hasMore {
			rows = rows[:limit]
		}
		var nextCursor any
		if hasMore && len(rows) > 0 {
			last := rows[len(rows)-1]
			tok, encErr := EncodeCursor(scope, []any{
				mapInt(last, "sort_rank"),
				mapString(last, "created_at"),
				mapString(last, "id"),
			}, start, seed)
			if encErr == nil {
				nextCursor = tok
			}
		}

		items := make([]map[string]any, 0, len(rows))
		for _, row := range rows {
			items = append(items, formatPageCard(row))
		}
		out = map[string]any{
			"items":       items,
			"has_more":    hasMore,
			"next_cursor": nextCursor,
			"terms":       terms,
		}
		return nil
	})
	return out, err
}

// Page 读取公开词条全文或局部段落（part = full|article|references|related），支持历史别名重定向、原文引用剥离、相册链接、贡献者署名与事实核查徽章。
func (r *PublicReader) Page(ctx context.Context, channel, slug string, opts PageReadOptions) (map[string]any, error) {
	part := opts.Part
	if part == "" {
		part = "full"
	}
	if part == "related" {
		return r.Related(ctx, channel, slug)
	}

	var result map[string]any
	err := r.store.WithSnapshot(ctx, func(sCtx context.Context, tx *sql.Tx) error {
		ch, err := r.store.GetChannel(sCtx, channel)
		if err != nil || ch == nil {
			return err
		}
		q := fmt.Sprintf(`
			SELECT p.id, p.slug, p.title, p.kind, p.topic, c.id AS channel_id, c.title AS channel_title,
			       c.username, c.config, c.published_revision, r.id AS revision_id, r.created_at, r.document
			%s
			WHERE c.id=? AND (p.slug=? OR p.id=? OR EXISTS (
				SELECT 1 FROM wiki_slug_aliases sa WHERE sa.channel_id=c.id AND sa.page_id=p.id AND sa.slug=?
			)) AND %s
			LIMIT 1
		`, pageJoinSQL, VisibleSQL("c", "r", "p"))
		rows, err := QueryRowsMap(sCtx, tx, q, ch.ID, slug, slug, slug)
		if err != nil || len(rows) == 0 {
			return err
		}
		row := rows[0]
		pageID := mapString(row, "id")

		if opts.CompareBefore != "" && opts.CompareAfter != "" {
			cmp, cmpErr := r.CompareRevisions(sCtx, pageID, opts.CompareBefore, opts.CompareAfter)
			if cmpErr != nil {
				return cmpErr
			}
			result = cmp
			return nil
		}

		currentRevision := mapString(row, "revision_id")
		historical := opts.Revision != "" && opts.Revision != currentRevision
		if historical {
			vRows, vErr := QueryRowsMap(sCtx, tx, fmt.Sprintf("SELECT r.id AS revision_id, r.created_at, r.document %s AND r.id=?", publishedHistorySQL()), pageID, opts.Revision)
			if vErr != nil || len(vRows) == 0 {
				return vErr
			}
			row["revision_id"] = vRows[0]["revision_id"]
			row["created_at"] = vRows[0]["created_at"]
			row["document"] = vRows[0]["document"]
		}

		cfg, cfgErr := ParseWikiConfig([]byte(mapString(row, "config")))
		if cfgErr != nil {
			cfg = ch.Config
		}
		username := mapString(row, "username")
		revID := mapString(row, "revision_id")

		evRows, err := QueryRowsMap(sCtx, tx, `
			SELECT DISTINCT s.id, s.source_type, s.message_id, s.created_at, s.edited_at
			FROM wiki_evidence e
			JOIN wiki_sources s ON s.id=e.source_id
			WHERE e.revision_id=?
			ORDER BY s.created_at DESC, s.id
			LIMIT 500
		`, revID)
		if err != nil {
			return err
		}
		evidenceMap := make(map[string]map[string]any, len(evRows))
		for _, er := range evRows {
			evidenceMap[mapString(er, "id")] = er
		}

		card := formatPageCard(row)
		doc := decodeMapJSON([]byte(mapString(row, "document")))
		card["verified_at"] = doc["verified_at"]
		card["current_revision_id"] = currentRevision
		card["proposals_enabled"] = !historical && cfg.ProposalsEnabled
		card["annotations_enabled"] = !historical && cfg.AnnotationsEnabled

		if part == "article" {
			var histTotal int
			if err := tx.QueryRowContext(sCtx, "SELECT COUNT(*) "+publishedHistorySQL(), pageID).Scan(&histTotal); err != nil {
				return err
			}
			card["history_total"] = histTotal
		}

		claimsRaw, _ := doc["claims"].([]any)
		pubClaims := make([]map[string]any, 0, len(claimsRaw))
		for _, rc := range claimsRaw {
			if cm, ok := rc.(map[string]any); ok {
				pubClaims = append(pubClaims, formatPublicClaim(cm, evidenceMap, username, cfg))
			}
		}
		card["claims"] = pubClaims
		rawTitleNotes := extractMapSlice(doc["title_notes"])
		pubTitleNotes := PublicNotes(rawTitleNotes)
		card["title_notes"] = pubTitleNotes

		// 相册多贴直达链接（对齐 reader.py:409-435）
		if username != "" && cfg.ShowTelegramLinks && part != "references" {
			albumGroupSet := map[string]bool{}
			for _, rc := range claimsRaw {
				if cm, ok := rc.(map[string]any); ok {
					mg := mapString(cm, "media_group")
					if mg != "" && !strings.HasPrefix(mg, MessageMediaPrefix) {
						albumGroupSet[mg] = true
					}
				}
			}
			if len(albumGroupSet) > 0 {
				groups := SortedKeys(albumGroupSet)
				var links []map[string]any
				for _, grp := range groups {
					members, mErr := r.store.AlbumSources(sCtx, tx, ch.ID, grp)
					if mErr != nil {
						return mErr
					}
					publishedIDs := map[int64]bool{}
					for _, rc := range claimsRaw {
						cm, ok := rc.(map[string]any)
						if !ok || mapString(cm, "media_group") != grp {
							continue
						}
						if arr, ok := cm["album_messages"].([]any); ok {
							for _, v := range arr {
								if id := anyToInt64(v); id > 0 {
									publishedIDs[id] = true
								}
							}
						}
					}
					for _, m := range members {
						if m.SourceType == "message" && m.MessageID != nil && publishedIDs[*m.MessageID] {
							links = append(links, map[string]any{
								"message_id": *m.MessageID,
								"url":        fmt.Sprintf("https://t.me/%s/%d", username, *m.MessageID),
							})
						}
					}
				}
				if links == nil {
					links = []map[string]any{}
				}
				card["album_links"] = links
			}
		}

		// 社区贡献署名（尊重 blocked=FALSE 且 anonymous_contributions=FALSE 与 show_username/show_avatar）
		proposalIDs := mapStringSlice(doc["community_contributions"])
		if part != "references" && len(proposalIDs) > 0 {
			var passages []annotationPassage
			passages = append(passages, annotationPassage{ordinal: nil, rawNotes: rawTitleNotes, pubNotes: pubTitleNotes})
			for idx, rc := range claimsRaw {
				if cm, ok := rc.(map[string]any); ok && idx < len(pubClaims) {
					ord := idx
					pNotes, _ := pubClaims[idx]["community_notes"].([]map[string]any)
					passages = append(passages, annotationPassage{
						ordinal:  &ord,
						rawNotes: extractMapSlice(cm["community_notes"]),
						pubNotes: pNotes,
					})
				}
			}
			if err := r.creditAnnotations(sCtx, tx, pageID, proposalIDs, passages); err != nil {
				return err
			}
		}

		// 外部事实核查徽章
		if part != "references" {
			var revResultRaw []byte
			rErr := tx.QueryRowContext(sCtx, `
				SELECT result FROM wiki_revision_reviews
				WHERE revision_id=? AND approved=TRUE AND policy_key<>?
				ORDER BY created_at DESC LIMIT 1
			`, revID, ManualReviewPolicy).Scan(&revResultRaw)
			if rErr == nil && len(revResultRaw) > 0 {
				revRes := decodeMapJSON(revResultRaw)
				fcArr, _ := revRes["fact_checks"].([]any)
				for _, rfc := range fcArr {
					fc, ok := rfc.(map[string]any)
					if !ok {
						continue
					}
					cIdx := mapInt(fc, "index")
					if cIdx >= 0 && cIdx < len(pubClaims) {
						var fcSources []map[string]any
						sArr, _ := fc["sources"].([]any)
						for _, rs := range sArr {
							if sm, ok := rs.(map[string]any); ok {
								fcSources = append(fcSources, map[string]any{
									"title": mapString(sm, "title"),
									"url":   mapString(sm, "url"),
								})
							}
						}
						if fcSources == nil {
							fcSources = []map[string]any{}
						}
						pubClaims[cIdx]["fact_check"] = map[string]any{
							"status":     fc["status"],
							"checked_at": fc["checked_at"],
							"sources":    fcSources,
						}
					}
				}
			}
		}

		sourcesOut := make([]map[string]any, 0, len(evRows))
		for _, er := range evRows {
			sourcesOut = append(sourcesOut, formatSourceCard(er, username, cfg))
		}
		card["sources"] = sourcesOut
		if part == "article" {
			result = card
			return nil
		}

		sourcesTotal := len(evRows)
		if len(evRows) == 500 {
			_ = tx.QueryRowContext(sCtx, "SELECT COUNT(DISTINCT s.id) FROM wiki_evidence e JOIN wiki_sources s ON s.id=e.source_id WHERE e.revision_id=?", revID).Scan(&sourcesTotal)
		}
		card["sources_total"] = sourcesTotal

		histRows, err := QueryRowsMap(sCtx, tx, fmt.Sprintf(`
			SELECT r.id, r.created_at %s
			ORDER BY (r.id=?) DESC, r.created_at DESC, r.id DESC
			LIMIT 50
		`, publishedHistorySQL()), pageID, currentRevision)
		if err != nil {
			return err
		}
		histTotal := len(histRows)
		if len(histRows) == 50 {
			_ = tx.QueryRowContext(sCtx, "SELECT COUNT(*) "+publishedHistorySQL(), pageID).Scan(&histTotal)
		}
		histOut := make([]map[string]any, 0, len(histRows))
		for _, hr := range histRows {
			histOut = append(histOut, map[string]any{
				"revision_id": hr["id"],
				"created_at":  hr["created_at"],
			})
		}
		card["history"] = histOut
		card["history_total"] = histTotal

		// 结束读取前在同一快照内二次复核频道与发布可见性
		var validID string
		recheckQ := fmt.Sprintf("SELECT r.id %s WHERE c.id=? AND c.published_revision=? AND r.id=? AND %s",
			pageJoinSQL, VisibleSQL("c", "r", "p"))
		if err := tx.QueryRowContext(sCtx, recheckQ, ch.ID, row["published_revision"], currentRevision).Scan(&validID); err != nil || validID == "" {
			return nil
		}

		if part == "references" {
			result = map[string]any{
				"revision_id":         card["revision_id"],
				"current_revision_id": card["current_revision_id"],
				"publication_id":      card["publication_id"],
				"sources":             card["sources"],
				"sources_total":       card["sources_total"],
				"history":             card["history"],
				"history_total":       card["history_total"],
			}
			return nil
		}
		result = card
		return nil
	})
	return result, err
}

type annotationPassage struct {
	ordinal  *int
	rawNotes []map[string]any
	pubNotes []map[string]any
}

func extractMapSlice(raw any) []map[string]any {
	switch arr := raw.(type) {
	case []map[string]any:
		return arr
	case []any:
		out := make([]map[string]any, 0, len(arr))
		for _, item := range arr {
			if m, ok := item.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	}
	return nil
}

func anyToInt64(v any) int64 {
	switch val := v.(type) {
	case int64:
		return val
	case int:
		return int64(val)
	case float64:
		return int64(val)
	case json.Number:
		n, _ := val.Int64()
		return n
	}
	return 0
}

func (r *PublicReader) creditAnnotations(ctx context.Context, tx *sql.Tx, pageID string, proposalIDs []string, passages []annotationPassage) error {
	hasAny := false
	hasMissingProposalID := false
	for _, p := range passages {
		if len(p.rawNotes) > 0 {
			hasAny = true
			for _, n := range p.rawNotes {
				if mapString(n, "proposal_id") == "" {
					hasMissingProposalID = true
				}
			}
		}
	}
	if !hasAny || len(proposalIDs) == 0 {
		return nil
	}

	marks, pArgs := stringPlaceholders(proposalIDs)
	args := append([]any{pageID}, pArgs...)
	rows, err := QueryRowsMap(ctx, tx, fmt.Sprintf(`
		SELECT e.id, u.id AS contributor_id, u.display_name,
		       IF(u.show_username, u.username, NULL) AS username,
		       (u.show_avatar AND u.avatar IS NOT NULL) AS avatar
		FROM wiki_contributions x
		JOIN wiki_edit_proposals e ON e.id=x.proposal_id
		JOIN wiki_users u ON u.id=e.user_id
		WHERE e.page_id=? AND e.id IN (%s) AND e.status='approved'
		  AND u.blocked=FALSE AND u.anonymous_contributions=FALSE
	`, marks), args...)
	if err != nil {
		return err
	}
	byID := make(map[string]map[string]any, len(rows))
	for _, row := range rows {
		byID[mapString(row, "id")] = row
	}

	type legacyEntry struct {
		id      string
		kind    string
		payload map[string]any
	}
	var legacy []legacyEntry
	if hasMissingProposalID {
		lRows, err := QueryRowsMap(ctx, tx, fmt.Sprintf(`
			SELECT id, kind, payload FROM wiki_edit_proposals
			WHERE page_id=? AND id IN (%s) AND status='approved'
		`, marks), args...)
		if err != nil {
			return err
		}
		for _, lr := range lRows {
			legacy = append(legacy, legacyEntry{
				id:      mapString(lr, "id"),
				kind:    mapString(lr, "kind"),
				payload: decodeMapJSON([]byte(mapString(lr, "payload"))),
			})
		}
	}

	for _, passage := range passages {
		limit := min(len(passage.rawNotes), len(passage.pubNotes))
		for i := 0; i < limit; i++ {
			rawNote := passage.rawNotes[i]
			pubNote := passage.pubNotes[i]
			propID := mapString(rawNote, "proposal_id")
			if propID == "" && len(legacy) > 0 {
				var matches []string
				for _, entry := range legacy {
					if entry.kind != mapString(rawNote, "kind") {
						continue
					}
					if !sameOptionalOrdinal(entry.payload["claim"], passage.ordinal) {
						continue
					}
					if mapString(entry.payload, "text") != mapString(rawNote, "text") {
						continue
					}
					if !slices.Equal(mapStringSlice(entry.payload["sources"]), mapStringSlice(rawNote["sources"])) {
						continue
					}
					if mapString(entry.payload, "excerpt") != mapString(rawNote, "excerpt") {
						continue
					}
					matches = append(matches, entry.id)
				}
				if len(matches) == 1 {
					propID = matches[0]
				}
			}
			if row, ok := byID[propID]; ok {
				pubNote["contributor"] = map[string]any{
					"id":           row["contributor_id"],
					"display_name": row["display_name"],
					"username":     row["username"],
					"avatar":       anyToInt64(row["avatar"]) != 0 || mapBool(row, "avatar"),
				}
			}
		}
	}
	return nil
}

func sameOptionalOrdinal(raw any, ord *int) bool {
	if raw == nil {
		return ord == nil
	}
	if ord == nil {
		return false
	}
	return int(anyToInt64(raw)) == *ord
}

// CompareRevisions 比较同一词条的两个已公开发布修订，返回块级变更对与 Unified Diff 字符串（对齐 reader.py:compare_revisions）。
func (r *PublicReader) CompareRevisions(ctx context.Context, pageID, beforeRevID, afterRevID string) (map[string]any, error) {
	var out map[string]any
	err := r.store.WithSnapshot(ctx, func(sCtx context.Context, tx *sql.Tx) error {
		rows, err := QueryRowsMap(sCtx, tx, fmt.Sprintf("SELECT r.id, r.created_at, r.document %s AND r.id IN (?, ?)", publishedHistorySQL()), pageID, beforeRevID, afterRevID)
		if err != nil {
			return err
		}
		versions := make(map[string]map[string]any, len(rows))
		for _, row := range rows {
			versions[mapString(row, "id")] = row
		}
		befRow, okBef := versions[beforeRevID]
		aftRow, okAft := versions[afterRevID]
		if !okBef || !okAft {
			return nil
		}
		befDoc := decodeMapJSON([]byte(mapString(befRow, "document")))
		aftDoc := decodeMapJSON([]byte(mapString(aftRow, "document")))
		changes, truncated := RevisionChanges(befDoc, aftDoc)
		diff := RevisionUnifiedDiff(befDoc, aftDoc, beforeRevID, afterRevID)
		out = map[string]any{
			"before": map[string]any{
				"id":         befRow["id"],
				"created_at": befRow["created_at"],
			},
			"after": map[string]any{
				"id":         aftRow["id"],
				"created_at": aftRow["created_at"],
			},
			"changes":   changes,
			"truncated": truncated,
			"diff":      diff,
		}
		return nil
	})
	return out, err
}

// Related 返回同一频道同一主题下的上一篇/下一篇相邻词条节点。
func (r *PublicReader) Related(ctx context.Context, channel, slug string) (map[string]any, error) {
	var out map[string]any
	err := r.store.WithSnapshot(ctx, func(sCtx context.Context, tx *sql.Tx) error {
		ch, err := r.store.GetChannel(sCtx, channel)
		if err != nil || ch == nil {
			return err
		}
		qCur := fmt.Sprintf(`
			SELECT p.id, p.topic, r.created_at %s
			WHERE c.id=? AND (p.slug=? OR p.id=? OR EXISTS (
				SELECT 1 FROM wiki_slug_aliases a WHERE a.channel_id=c.id AND a.page_id=p.id AND a.slug=?
			)) AND %s LIMIT 1
		`, pageJoinSQL, VisibleSQL("c", "r", "p"))
		curRows, err := QueryRowsMap(sCtx, tx, qCur, ch.ID, slug, slug, slug)
		if err != nil || len(curRows) == 0 {
			return err
		}
		cur := curRows[0]
		topic := mapString(cur, "topic")
		curCreated := cur["created_at"]
		curID := cur["id"]

		res := map[string]any{"previous": nil, "next": nil}
		for _, spec := range []struct {
			label, op, sortDir string
		}{
			{"previous", "<", "DESC"},
			{"next", ">", "ASC"},
		} {
			qAdj := fmt.Sprintf(`
				SELECT %s %s
				WHERE c.id=? AND p.topic=? AND %s
				  AND (r.created_at, p.id) %s (?, ?)
				ORDER BY r.created_at %s, p.id %s LIMIT 1
			`, nodeFieldsSQL, pageJoinSQL, VisibleSQL("c", "r", "p"), spec.op, spec.sortDir, spec.sortDir)
			adjRows, err := QueryRowsMap(sCtx, tx, qAdj, ch.ID, topic, curCreated, curID)
			if err != nil {
				return err
			}
			if len(adjRows) > 0 {
				res[spec.label] = formatGraphNode(adjRows[0])
			}
		}
		out = res
		return nil
	})
	return out, err
}

// Revisions 分页返回公开词条曾正式发布过的历史修订元数据列表。
func (r *PublicReader) Revisions(ctx context.Context, channel, slug string, page, pageSize int) (map[string]any, error) {
	page = min(max(page, 1), 10000)
	if pageSize <= 0 {
		pageSize = 20
	}
	pageSize = min(max(pageSize, 1), 50)

	var out map[string]any
	err := r.store.WithSnapshot(ctx, func(sCtx context.Context, tx *sql.Tx) error {
		ch, err := r.store.GetChannel(sCtx, channel)
		if err != nil || ch == nil {
			return err
		}
		var pageID, currentRev string
		qPage := fmt.Sprintf(`
			SELECT p.id, r.id %s
			WHERE c.id=? AND (p.slug=? OR p.id=? OR EXISTS (
				SELECT 1 FROM wiki_slug_aliases a WHERE a.channel_id=c.id AND a.page_id=p.id AND a.slug=?
			)) AND %s LIMIT 1
		`, pageJoinSQL, VisibleSQL("c", "r", "p"))
		if err := tx.QueryRowContext(sCtx, qPage, ch.ID, slug, slug, slug).Scan(&pageID, &currentRev); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return err
		}
		var total int
		if err := tx.QueryRowContext(sCtx, "SELECT COUNT(*) "+publishedHistorySQL(), pageID).Scan(&total); err != nil {
			return err
		}
		rows, err := QueryRowsMap(sCtx, tx, fmt.Sprintf(`
			SELECT r.id AS revision_id, r.created_at %s
			ORDER BY (r.id=?) DESC, r.created_at DESC, r.id DESC
			LIMIT ? OFFSET ?
		`, publishedHistorySQL()), pageID, currentRev, pageSize, (page-1)*pageSize)
		if err != nil {
			return err
		}
		out = map[string]any{
			"page_id":             pageID,
			"current_revision_id": currentRev,
			"items":               rows,
			"total":               total,
			"page":                page,
			"page_size":           pageSize,
		}
		return nil
	})
	return out, err
}

// Contributors 查询公开社区贡献榜或单个非匿名贡献者的已发布贡献记录。
func (r *PublicReader) Contributors(ctx context.Context, opts ContributorsOptions) ([]map[string]any, error) {
	var out []map[string]any
	err := r.store.WithSnapshot(ctx, func(sCtx context.Context, tx *sql.Tx) error {
		conds := []string{VisibleSQL("c", "r", "p"), "u.blocked=FALSE", "u.anonymous_contributions=FALSE"}
		var args []any
		if opts.Channel != "" {
			conds = append(conds, "e.channel_id=?")
			args = append(args, opts.Channel)
		}
		if opts.PageID != "" {
			conds = append(conds, "e.page_id=?")
			args = append(args, opts.PageID)
		}
		if opts.UserID != "" {
			conds = append(conds, "u.id=?")
			args = append(args, opts.UserID)
		}

		selectCols := "u.id, u.display_name, IF(u.show_username, u.username, NULL) AS username, (u.show_avatar AND u.avatar IS NOT NULL) AS avatar"
		groupBy := "GROUP BY u.id, u.display_name, username, avatar"
		orderBy := "total DESC, latest DESC, u.id"
		if opts.History {
			selectCols += ", e.kind, p.title, p.slug, e.channel_id, e.revision_id, x.created_at"
			groupBy = ""
			orderBy = "x.created_at DESC, x.proposal_id"
		} else {
			selectCols += `, COUNT(*) AS total, MAX(x.created_at) AS latest,
				SUM(e.kind IN ('edit','correction')) AS edits,
				SUM(e.kind IN ('annotation','dispute','outdated','fact_check')) AS annotations,
				SUM(e.kind='source') AS sources`
		}

		inPubRev := `(r.id=e.revision_id OR JSON_CONTAINS(JSON_EXTRACT(r.document,'$.community_contributions'), JSON_QUOTE(e.id)))`
		args = append(args, max(0, opts.Offset))
		q := fmt.Sprintf(`
			SELECT %s
			FROM wiki_contributions x
			JOIN wiki_edit_proposals e ON e.id=x.proposal_id
			JOIN wiki_users u ON u.id=e.user_id
			JOIN wiki_channels c ON c.id=e.channel_id
			JOIN wiki_pages p ON p.id=e.page_id
			JOIN wiki_publication_pages pp ON pp.publication_id=c.published_revision AND pp.page_id=p.id
			JOIN wiki_page_revisions r ON r.id=pp.revision_id AND %s
			WHERE %s %s
			ORDER BY %s LIMIT 30 OFFSET ?
		`, selectCols, inPubRev, strings.Join(conds, " AND "), groupBy, orderBy)
		rows, err := QueryRowsMap(sCtx, tx, q, args...)
		if err != nil {
			return err
		}
		for _, row := range rows {
			row["avatar"] = anyToInt64(row["avatar"]) != 0 || mapBool(row, "avatar")
		}
		out = rows
		return nil
	})
	return out, err
}

// Profile 返回公开频道实时统计画像、24 个月主题趋势与同代际频道叙述（对齐 reader.py:profile）。
func (r *PublicReader) Profile(ctx context.Context, channel string) (map[string]any, error) {
	var out map[string]any
	err := r.store.WithSnapshot(ctx, func(sCtx context.Context, tx *sql.Tx) error {
		ch, err := r.store.GetChannel(sCtx, channel)
		if err != nil || ch == nil {
			return err
		}
		var (
			profRaw, covRaw []byte
			pubID           string
			pubCreated      any
		)
		q := fmt.Sprintf(`
			SELECT pub.profile, pub.coverage, pub.id, pub.created_at
			FROM wiki_channels c
			JOIN wiki_publications pub ON pub.id=c.published_revision
			WHERE c.id=? AND %s
		`, ChannelVisibleSQL("c"))
		if err := tx.QueryRowContext(sCtx, q, ch.ID).Scan(&profRaw, &covRaw, &pubID, &pubCreated); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return err
		}

		live, err := r.knowledge.Profile(sCtx, tx, ch.ID, pubID)
		if err != nil {
			return err
		}
		if topics, ok := live["topics"].([]map[string]any); ok && len(ch.Config.HiddenTopics) > 0 {
			filtered := make([]map[string]any, 0, len(topics))
			for _, t := range topics {
				if !slices.Contains(ch.Config.HiddenTopics, mapString(t, "topic")) {
					filtered = append(filtered, t)
				}
			}
			live["topics"] = filtered
		}
		snap := decodeMapJSON(profRaw)
		narrative := any(map[string]any{})
		if len(ch.Config.HiddenTopics) == 0 && mapInt64(snap, "source_generation") == ch.SourceGeneration {
			if narr, ok := snap["narrative"].(map[string]any); ok && narr != nil {
				narrative = narr
			}
		}
		live["narrative"] = narrative
		live["title"] = ch.Title
		live["publication_id"] = pubID
		live["published_at"] = formatNullableTime(pubCreated)
		live["coverage"] = PublicCoverage(ch.Coverage)
		out = live
		return nil
	})
	return out, err
}

// Changes 返回频道当前已公开发布且仍有效的词条最近更新列表。
func (r *PublicReader) Changes(ctx context.Context, channel string, page, pageSize int) (map[string]any, error) {
	return r.Search(ctx, SearchOptions{
		Channel:  channel,
		Page:     page,
		PageSize: pageSize,
	})
}

// Timeline 返回频道事件时间线（同时支持传统页码分页与 2 小时一致性延续游标模式，对齐 reader.py:timeline）。
func (r *PublicReader) Timeline(ctx context.Context, channel string, opts TimelineOptions) (map[string]any, error) {
	page := min(max(opts.Page, 1), 10000)
	pageSize := opts.PageSize
	if pageSize <= 0 {
		pageSize = 20
	}
	pageSize = min(max(pageSize, 1), 50)

	var out map[string]any
	err := r.store.WithSnapshot(ctx, func(sCtx context.Context, tx *sql.Tx) error {
		ch, err := r.store.GetChannel(sCtx, channel)
		if err != nil {
			return err
		}
		if ch == nil {
			if opts.CursorMode {
				return nil
			}
			out = map[string]any{"items": []map[string]any{}, "page": page, "page_size": pageSize, "total_pages": 0}
			return nil
		}
		if opts.CursorMode && !r.isChannelVisible(sCtx, ch.ID) {
			return nil
		}

		join := pageJoinSQL + " JOIN wiki_revision_claims wc ON wc.revision_id=r.id "
		eventTime := "COALESCE(wc.occurred_at, wc.source_created_at)"
		where := fmt.Sprintf("c.id=? AND %s", VisibleSQL("c", "r", "p"))
		params := []any{ch.ID}

		var (
			scope      string
			start      time.Time
			seed       string
			totalCount int
			pagination string
		)
		if opts.CursorMode {
			var beforeISO any
			if opts.Before != nil && !opts.Before.IsZero() {
				beforeISO = FormatISO(opts.Before.UTC())
			}
			scope = ScopeKey("timeline", ch.ID, ch.PublishedRevision, beforeISO)
			var pos *CursorPosition
			start, seed, pos, err = ReadOrInitCursor(opts.Cursor, scope, false)
			if err != nil {
				return err
			}
			if opts.Before != nil && !opts.Before.IsZero() {
				where += fmt.Sprintf(" AND %s <= ?", eventTime)
				params = append(params, db.MysqlDatetime6(opts.Before.UTC()))
			}
			if pos != nil {
				posDate := db.MysqlDatetime6(pos.Date)
				where += fmt.Sprintf(" AND (%s < ? OR (%s = ? AND (r.id, wc.ordinal) > (?, ?)))", eventTime, eventTime)
				params = append(params, posDate, posDate, pos.ID, pos.Order)
			}
			pagination = "LIMIT ?"
			params = append(params, pageSize+1)
		} else {
			if err := tx.QueryRowContext(sCtx, fmt.Sprintf("SELECT COUNT(*) %s WHERE %s", join, where), params...).Scan(&totalCount); err != nil {
				return err
			}
			pagination = "LIMIT ? OFFSET ?"
			params = append(params, pageSize, (page-1)*pageSize)
		}

		q := fmt.Sprintf(`
			SELECT p.id, p.slug, c.id AS channel_id, c.username, c.config, c.published_revision,
			       JSON_UNQUOTE(JSON_EXTRACT(r.document,'$.title')) AS title, wc.payload,
			       r.id AS revision_id, wc.ordinal, %s AS event_time
			%s WHERE %s
			ORDER BY event_time DESC, r.id, wc.ordinal %s
		`, eventTime, join, where, pagination)
		rows, err := QueryRowsMap(sCtx, tx, q, params...)
		if err != nil {
			return err
		}

		hasMore := opts.CursorMode && len(rows) > pageSize
		if len(rows) > pageSize {
			rows = rows[:pageSize]
		}

		sourceIDSet := map[string]bool{}
		for _, row := range rows {
			payload := decodeMapJSON([]byte(mapString(row, "payload")))
			evArr, _ := payload["evidence"].([]any)
			for _, re := range evArr {
				if em, ok := re.(map[string]any); ok {
					if sid := mapString(em, "source_id"); sid != "" {
						sourceIDSet[sid] = true
					}
				}
			}
		}
		sourceIDs := SortedKeys(sourceIDSet)
		sources := map[string]map[string]any{}
		for offset := 0; offset < len(sourceIDs); offset += 500 {
			batch := sourceIDs[offset:min(len(sourceIDs), offset+500)]
			marks, bArgs := stringPlaceholders(batch)
			sArgs := append([]any{ch.ID}, bArgs...)
			sRows, err := QueryRowsMap(sCtx, tx, fmt.Sprintf("SELECT id, source_type, message_id, created_at, edited_at FROM wiki_sources WHERE channel_id=? AND id IN (%s)", marks), sArgs...)
			if err != nil {
				return err
			}
			for _, sr := range sRows {
				sources[mapString(sr, "id")] = sr
			}
		}

		events := make([]map[string]any, 0, len(rows))
		for _, row := range rows {
			cfg, cfgErr := ParseWikiConfig([]byte(mapString(row, "config")))
			if cfgErr != nil {
				cfg = ch.Config
			}
			payload := decodeMapJSON([]byte(mapString(row, "payload")))
			claimMap := formatPublicClaim(payload, sources, mapString(row, "username"), cfg)
			claimMap["page_id"] = row["id"]
			claimMap["title"] = row["title"]
			claimMap["url"] = fmt.Sprintf("/wiki/%s/page/%s", mapString(row, "channel_id"), mapString(row, "slug"))
			claimMap["publication_id"] = row["published_revision"]
			claimMap["event_id"] = fmt.Sprintf("%s:%d", mapString(row, "revision_id"), mapInt(row, "ordinal"))
			events = append(events, claimMap)
		}

		if opts.CursorMode {
			var nextCursor any
			if hasMore && len(rows) > 0 {
				last := rows[len(rows)-1]
				tok, encErr := EncodeCursor(scope, []any{
					mapInt(last, "ordinal"),
					mapString(last, "event_time"),
					mapString(last, "revision_id"),
				}, start, seed)
				if encErr == nil {
					nextCursor = tok
				}
			}
			out = map[string]any{
				"items":          events,
				"has_more":       hasMore,
				"next_cursor":    nextCursor,
				"publication_id": ch.PublishedRevision,
			}
			return nil
		}

		out = map[string]any{
			"items":       events,
			"page":        page,
			"page_size":   pageSize,
			"total_pages": (totalCount + pageSize - 1) / pageSize,
		}
		return nil
	})
	return out, err
}

// BrowseGraph 以延续游标方式分页浏览知识图谱节点与聚合有向边（对齐 reader.py:browse_graph）。
func (r *PublicReader) BrowseGraph(ctx context.Context, channel string, opts GraphOptions) (map[string]any, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = 40
	}
	limit = min(max(limit, 1), 50)

	var out map[string]any
	err := r.store.WithSnapshot(ctx, func(sCtx context.Context, tx *sql.Tx) error {
		ch, err := r.store.GetChannel(sCtx, channel)
		if err != nil || ch == nil || !r.isChannelVisible(sCtx, ch.ID) {
			return err
		}
		publication := ch.PublishedRevision
		var afterArg, beforeArg any
		if opts.After != nil && !opts.After.IsZero() {
			afterArg = FormatISO(opts.After.UTC())
		}
		if opts.Before != nil && !opts.Before.IsZero() {
			beforeArg = FormatISO(opts.Before.UTC())
		}
		scope := ScopeKey("graph", ch.ID, publication, nilIfEmpty(opts.NodeID), limit, nilIfEmpty(opts.Kind), nilIfEmpty(opts.Topic), nilIfEmpty(opts.Relation), opts.Query, afterArg, beforeArg)
		start, _, pos, err := ReadOrInitCursor(opts.Cursor, scope, false)
		if err != nil {
			return err
		}

		edgeConds := []string{"pe.publication_id=?", "e.channel_id=?", graphEdgeVisibleSQL}
		edgeParams := []any{publication, ch.ID}
		if opts.Relation != "" {
			edgeConds = append(edgeConds, "e.kind=?")
			edgeParams = append(edgeParams, opts.Relation)
		}
		if opts.After != nil && !opts.After.IsZero() {
			edgeConds = append(edgeConds, "s.created_at>=?")
			edgeParams = append(edgeParams, db.MysqlDatetime6(opts.After.UTC()))
		}
		if opts.Before != nil && !opts.Before.IsZero() {
			edgeConds = append(edgeConds, "s.created_at<=?")
			edgeParams = append(edgeParams, db.MysqlDatetime6(opts.Before.UTC()))
		}
		edgeWhere := strings.Join(edgeConds, " AND ")

		var filterAfter, filterBefore *time.Time
		if opts.NodeID == "" {
			filterAfter = opts.After
			filterBefore = opts.Before
		}
		conds, params, _, ranking, rankParams := r.buildPageFilters(opts.Query, ch.ID, opts.Kind, opts.Topic, filterAfter, filterBefore, true)

		var rootRow map[string]any
		if opts.NodeID != "" {
			rRows, err := QueryRowsMap(sCtx, tx, fmt.Sprintf("SELECT %s %s WHERE c.id=? AND p.id=? AND %s", nodeFieldsSQL, pageJoinSQL, VisibleSQL("c", "r", "p")), ch.ID, opts.NodeID)
			if err != nil || len(rRows) == 0 {
				return err
			}
			rootRow = rRows[0]
		}
		if opts.NodeID != "" || opts.Relation != "" {
			nodeArg := nilIfEmpty(opts.NodeID)
			conds = append(conds, fmt.Sprintf(`EXISTS (
				SELECT 1 %s
				WHERE %s AND (
					(e.source_page=p.id AND (? IS NULL OR e.target_page=?))
					OR (e.target_page=p.id AND (? IS NULL OR e.source_page=?))
				)
			)`, graphEdgeJoinSQL, edgeWhere))
			params = append(params, edgeParams...)
			params = append(params, nodeArg, nodeArg, nodeArg, nodeArg)
		}
		if opts.NodeID != "" {
			conds = append(conds, "p.id<>?")
			params = append(params, opts.NodeID)
		}

		sortExpr := "0"
		if ranking != "" {
			sortExpr = fmt.Sprintf("CAST((%s) AS SIGNED)", ranking)
		}
		continuation := ""
		if pos != nil {
			posDate := db.MysqlDatetime6(pos.Date)
			continuation = "HAVING sort_rank < ? OR (sort_rank = ? AND (created_at, id) < (?, ?))"
			params = append(params, pos.Order, pos.Order, posDate, pos.ID)
		}
		where := strings.Join(conds, " AND ")

		allArgs := make([]any, 0, len(rankParams)+len(params)+1)
		allArgs = append(allArgs, rankParams...)
		allArgs = append(allArgs, params...)
		allArgs = append(allArgs, limit+1)

		rows, err := QueryRowsMap(sCtx, tx, fmt.Sprintf(`
			SELECT %s, r.created_at, %s AS sort_rank
			%s WHERE %s %s
			ORDER BY sort_rank DESC, r.created_at DESC, p.id DESC LIMIT ?
		`, nodeFieldsSQL, sortExpr, pageJoinSQL, where, continuation), allArgs...)
		if err != nil {
			return err
		}
		hasMore := len(rows) > limit
		if hasMore {
			rows = rows[:limit]
		}
		var nextCursor any
		if hasMore && len(rows) > 0 {
			last := rows[len(rows)-1]
			tok, encErr := EncodeCursor(scope, []any{
				mapInt(last, "sort_rank"),
				mapString(last, "created_at"),
				mapString(last, "id"),
			}, start, "")
			if encErr == nil {
				nextCursor = tok
			}
		}

		nodes := make([]map[string]any, 0, len(rows)+1)
		if rootRow != nil {
			nodes = append(nodes, formatGraphNode(rootRow))
		}
		for _, row := range rows {
			nodes = append(nodes, formatGraphNode(row))
		}

		edges := []map[string]any{}
		if len(nodes) > 0 {
			ids := make([]string, 0, len(nodes))
			for _, n := range nodes {
				ids = append(ids, mapString(n, "id"))
			}
			marks, idArgs := stringPlaceholders(ids)
			eArgs := make([]any, 0, len(edgeParams)+len(idArgs)*2)
			eArgs = append(eArgs, edgeParams...)
			eArgs = append(eArgs, idArgs...)
			eArgs = append(eArgs, idArgs...)

			eRows, err := QueryRowsMap(sCtx, tx, fmt.Sprintf(`
				SELECT MIN(e.id) AS id, e.source_page AS source, e.target_page AS target, e.kind,
				       MAX(JSON_EXTRACT(e.relation_json,'$.confidence')+0) AS confidence,
				       SUM(JSON_LENGTH(e.relation_json,'$.evidence')) AS evidence_count
				%s WHERE %s
				  AND e.source_page IN (%s) AND e.target_page IN (%s)
				GROUP BY e.source_page, e.target_page, e.kind
				ORDER BY id
			`, graphEdgeJoinSQL, edgeWhere, marks, marks), eArgs...)
			if err != nil {
				return err
			}
			edges = eRows
		}

		out = map[string]any{
			"nodes":          nodes,
			"edges":          edges,
			"publication_id": publication,
			"limit":          limit,
			"truncated":      hasMore,
			"next_cursor":    nextCursor,
		}
		return nil
	})
	return out, err
}

// Graph 执行最多 2 跳广度优先搜索（BFS）构造局部知识图谱子图（对齐 reader.py:graph）。
func (r *PublicReader) Graph(ctx context.Context, channel string, opts GraphOptions) (map[string]any, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = 60
	}
	limit = min(max(limit, 1), 120)
	depth := opts.Depth
	if depth <= 0 {
		depth = 1
	}
	depth = min(max(depth, 1), 2)

	var out map[string]any
	err := r.store.WithSnapshot(ctx, func(sCtx context.Context, tx *sql.Tx) error {
		ch, err := r.store.GetChannel(sCtx, channel)
		if err != nil || ch == nil || !r.isChannelVisible(sCtx, ch.ID) {
			return err
		}

		nodeMap := map[string]map[string]any{}
		var nodeOrder []string
		addNode := func(id string, n map[string]any) {
			if _, exists := nodeMap[id]; !exists {
				nodeMap[id] = n
				nodeOrder = append(nodeOrder, id)
			}
		}

		if opts.NodeID != "" {
			rRows, err := QueryRowsMap(sCtx, tx, fmt.Sprintf(`
				SELECT %s %s
				WHERE c.id=? AND (p.id=? OR p.slug=? OR EXISTS (
					SELECT 1 FROM wiki_slug_aliases a WHERE a.channel_id=c.id AND a.page_id=p.id AND a.slug=?
				)) AND %s LIMIT 1
			`, nodeFieldsSQL, pageJoinSQL, VisibleSQL("c", "r", "p")), ch.ID, opts.NodeID, opts.NodeID, opts.NodeID)
			if err != nil || len(rRows) == 0 {
				return err
			}
			gn := formatGraphNode(rRows[0])
			addNode(mapString(gn, "id"), gn)
		} else {
			initial, err := r.Feed(sCtx, FeedOptions{
				Mode:    "latest",
				Query:   opts.Query,
				Channel: ch.ID,
				Kind:    opts.Kind,
				Topic:   opts.Topic,
				Limit:   min(20, limit),
				After:   opts.After,
				Before:  opts.Before,
			})
			if err != nil {
				return err
			}
			if items, ok := initial["items"].([]map[string]any); ok {
				for _, item := range items {
					addNode(mapString(item, "id"), item)
				}
			}
		}

		edgeMap := map[string]map[string]any{}
		var edgeOrder []string
		frontier := append([]string(nil), nodeOrder...)

		for step := 0; step < depth; step++ {
			if len(frontier) == 0 || len(nodeMap) >= limit {
				break
			}
			marks, fArgs := stringPlaceholders(frontier)
			eArgs := make([]any, 0, 2+len(fArgs)*2+1)
			eArgs = append(eArgs, ch.PublishedRevision, ch.ID)
			eArgs = append(eArgs, fArgs...)
			eArgs = append(eArgs, fArgs...)
			eArgs = append(eArgs, limit*3)

			eRows, err := QueryRowsMap(sCtx, tx, fmt.Sprintf(`
				SELECT e.id, e.source_page, e.target_page, e.kind, e.relation_json, s.created_at
				FROM wiki_edges e
				JOIN wiki_publication_edges pe ON pe.edge_id=e.id
				JOIN wiki_chunks chunk ON chunk.id=e.chunk_id
				JOIN wiki_sources s ON s.id=chunk.source_id
				WHERE pe.publication_id=? AND e.channel_id=? AND %s
				  AND (e.source_page IN (%s) OR e.target_page IN (%s))
				ORDER BY e.id LIMIT ?
			`, graphEdgeVisibleSQL, marks, marks), eArgs...)
			if err != nil {
				return err
			}

			candSet := map[string]bool{}
			for _, row := range eRows {
				for _, k := range []string{"source_page", "target_page"} {
					pid := mapString(row, k)
					if pid != "" && nodeMap[pid] == nil {
						candSet[pid] = true
					}
				}
			}
			candidates := SortedKeys(candSet)
			neighbours := map[string]map[string]any{}
			if len(candidates) > 0 {
				cMarks, cArgs := stringPlaceholders(candidates)
				conds, fParams, _, _, _ := r.buildPageFilters("", "", opts.Kind, opts.Topic, nil, nil, false)
				nArgs := make([]any, 0, 1+len(cArgs)+len(fParams))
				nArgs = append(nArgs, ch.ID)
				nArgs = append(nArgs, cArgs...)
				nArgs = append(nArgs, fParams...)
				vRows, err := QueryRowsMap(sCtx, tx, fmt.Sprintf("SELECT %s %s WHERE c.id=? AND p.id IN (%s) AND %s",
					nodeFieldsSQL, pageJoinSQL, cMarks, strings.Join(conds, " AND ")), nArgs...)
				if err != nil {
					return err
				}
				for _, vr := range vRows {
					neighbours[mapString(vr, "id")] = formatGraphNode(vr)
				}
			}

			frontier = nil
			for _, row := range eRows {
				if opts.Relation != "" && mapString(row, "kind") != opts.Relation {
					continue
				}
				if createdT, tErr := ParseUTCTime(mapString(row, "created_at")); tErr == nil {
					if opts.After != nil && !opts.After.IsZero() && createdT.Before(opts.After.UTC()) {
						continue
					}
					if opts.Before != nil && !opts.Before.IsZero() && createdT.After(opts.Before.UTC()) {
						continue
					}
				}
				srcPage := mapString(row, "source_page")
				tgtPage := mapString(row, "target_page")
				valid := true
				for _, pid := range []string{srcPage, tgtPage} {
					if nodeMap[pid] == nil {
						if len(nodeMap) >= limit {
							valid = false
							break
						}
						nb, ok := neighbours[pid]
						if !ok {
							valid = false
							break
						}
						addNode(pid, nb)
						frontier = append(frontier, pid)
					}
				}
				if valid {
					eid := mapString(row, "id")
					if edgeMap[eid] == nil {
						payload := decodeMapJSON([]byte(mapString(row, "relation_json")))
						evArr, _ := payload["evidence"].([]any)
						edgeMap[eid] = map[string]any{
							"id":             eid,
							"source":         srcPage,
							"target":         tgtPage,
							"kind":           row["kind"],
							"confidence":     payload["confidence"],
							"evidence_count": len(evArr),
						}
						edgeOrder = append(edgeOrder, eid)
					}
				}
			}
		}

		nodesOut := make([]map[string]any, 0, len(nodeOrder))
		for _, id := range nodeOrder {
			nodesOut = append(nodesOut, nodeMap[id])
		}
		edgesOut := make([]map[string]any, 0, len(edgeOrder))
		for _, eid := range edgeOrder {
			edgesOut = append(edgesOut, edgeMap[eid])
		}
		out = map[string]any{
			"nodes":          nodesOut,
			"edges":          edgesOut,
			"publication_id": ch.PublishedRevision,
			"limit":          limit,
			"truncated":      len(nodesOut) >= limit,
		}
		return nil
	})
	return out, err
}

// Sitemap 返回全站公开词条相对路径分页列表（绝不信任外部请求头 Host）。
func (r *PublicReader) Sitemap(ctx context.Context, page int) (map[string]any, error) {
	res, err := r.Search(ctx, SearchOptions{Page: page, PageSize: 50})
	if err != nil {
		return nil, err
	}
	paths := []string{}
	if items, ok := res["items"].([]map[string]any); ok {
		for _, item := range items {
			if u := mapString(item, "url"); u != "" {
				paths = append(paths, u)
			}
		}
	}
	return map[string]any{
		"paths": paths,
		"page":  res["page"],
		"total": res["total"],
	}, nil
}

// RenderPreviewHTML 渲染面向搜索引擎爬虫与社交分享卡片的静态转义 HTML（对齐 routes/wiki.py:crawler_html）。
// 若目标频道或词条不存在/未公开，返回 ("", nil)。
func (r *PublicReader) RenderPreviewHTML(ctx context.Context, channel, slug string) (string, error) {
	var (
		data map[string]any
		err  error
	)
	if strings.TrimSpace(slug) != "" {
		data, err = r.Page(ctx, channel, slug, PageReadOptions{Part: "full"})
	} else {
		data, err = r.Channel(ctx, channel, false)
	}
	if err != nil || data == nil {
		return "", err
	}

	ogImage := ""
	if ch, cErr := r.store.GetChannel(ctx, channel); cErr == nil && ch != nil {
		base := strings.TrimSpace(ch.Config.PublicBaseURL)
		if strings.HasPrefix(base, "http://") || strings.HasPrefix(base, "https://") {
			cid := mapString(data, "channel_id")
			if cid == "" {
				cid = mapString(data, "id")
			}
			avatarURL := strings.TrimRight(base, "/") + "/api/public/wiki/channels/" + cid + "/avatar"
			ogImage = fmt.Sprintf(`<meta property="og:image" content="%s">`, stdhtml.EscapeString(avatarURL))
		}
	}

	title := stdhtml.EscapeString(mapString(data, "title"))
	var body strings.Builder
	if claims, ok := data["claims"].([]map[string]any); ok {
		for _, cl := range claims {
			body.WriteString("<p>")
			body.WriteString(stdhtml.EscapeString(mapString(cl, "text")))
			body.WriteString("</p>")
		}
	}
	if pages, ok := data["pages"].([]map[string]any); ok {
		for _, card := range pages {
			body.WriteString(`<p><a href="`)
			body.WriteString(stdhtml.EscapeString(mapString(card, "url")))
			body.WriteString(`">`)
			body.WriteString(stdhtml.EscapeString(mapString(card, "title")))
			body.WriteString("</a></p>")
		}
	}

	path := mapString(data, "url")
	if path == "" {
		path = "/wiki/" + mapString(data, "id")
	}
	escPath := stdhtml.EscapeString(path)
	return fmt.Sprintf(
		`<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><title>%s · Sakura Wiki</title><meta property="og:title" content="%s">%s<link rel="canonical" href="%s"></head><body><h1>%s</h1>%s<a href="%s">打开交互页面</a></body></html>`,
		title, title, ogImage, escPath, title, body.String(), escPath,
	), nil
}

// ────────────────────────────── 管理端草稿、快照与审计查询辅助 ──────────────────────────────

// ListRevisions 查询频道管理端修订草稿列表（支持按状态、任务与关键词筛选，对齐 routes/wiki.py:revisions）。
func (r *PublicReader) ListRevisions(ctx context.Context, channelID, status, jobID, query string, page int) ([]map[string]any, error) {
	page = min(max(page, 1), 10000)
	ch, err := r.store.GetChannel(ctx, channelID)
	if err != nil || ch == nil {
		return nil, err
	}

	conditions := []string{"r.channel_id=?"}
	args := []any{ch.ID}
	var policy string

	if jobID != "" {
		job, _ := getJobTx(ctx, r.store.DB, jobID)
		if job != nil && job.ChannelID == ch.ID {
			policy = mapString(job.Cursor, "review_policy")
			if policy == SkippedAIReview {
				policy = ""
			}
			candidates := extractRevisionMap(job.Cursor)
			if len(candidates) == 0 && job.Cursor["review_revisions"] == nil && ch.PublishedRevision == "" && incrementalJobKinds[job.Kind] {
				expanded, _ := r.knowledge.ExpandRevisions(ctx, nil, ch, job.Cursor)
				candidates = expanded
			}
			if len(candidates) > 0 {
				var ids []string
				for _, rid := range candidates {
					ids = append(ids, rid)
				}
				sort.Strings(ids)
				conditions = append(conditions, "r.id IN (SELECT candidate.id FROM JSON_TABLE(?, '$[*]' COLUMNS(id CHAR(32) CHARACTER SET ascii PATH '$')) AS candidate)")
				args = append(args, EncodeJSON(ids))
			} else {
				conditions = append(conditions, "FALSE")
			}
		} else {
			conditions = append(conditions, "r.job_id=?")
			args = append(args, jobID)
		}
	}

	if trimmedQ := strings.TrimSpace(query); trimmedQ != "" {
		esc := strings.ReplaceAll(trimmedQ, "!", "!!")
		esc = strings.ReplaceAll(esc, "%", "!%")
		esc = strings.ReplaceAll(esc, "_", "!_")
		pat := "%" + esc + "%"
		conditions = append(conditions, "(p.title LIKE ? ESCAPE '!' OR r.search_text LIKE ? ESCAPE '!')")
		args = append(args, pat, pat)
	}

	reviewFilter := ""
	var prefixArgs []any
	if policy != "" {
		reviewFilter = "AND rr.policy_key IN (?, ?)"
		prefixArgs = append(prefixArgs, policy, ManualReviewPolicy)
	}
	prefixArgs = append(prefixArgs, ManualReviewPolicy, ManualReviewPolicy)

	statusFilter := "1=1"
	switch status {
	case "pending":
		statusFilter = "rejected=FALSE AND approved=FALSE AND review_result IS NULL"
	case "failed":
		statusFilter = "rejected=FALSE AND review_result=FALSE"
	case "passed":
		statusFilter = "rejected=FALSE AND (review_result=TRUE OR (review_result IS NULL AND approved=TRUE))"
	case "rejected":
		statusFilter = "rejected=TRUE"
	}

	q := fmt.Sprintf(`
		SELECT * FROM (
			SELECT r.id, r.page_id, r.job_id, r.approved, r.rejected, r.created_at,
			       p.title, p.slug, p.locked, p.hidden,
			       LEFT(JSON_UNQUOTE(JSON_EXTRACT(r.document,'$.claims[0].text')), 200) AS excerpt,
			       (SELECT rr.approved FROM wiki_revision_reviews rr WHERE rr.revision_id=r.id %s
			        ORDER BY (rr.policy_key=?) DESC, rr.created_at DESC, rr.policy_key, rr.subject_hash LIMIT 1) AS review_result,
			       EXISTS (SELECT 1 FROM wiki_revision_reviews rr WHERE rr.revision_id=r.id AND rr.policy_key=? AND rr.approved=TRUE) AS manually_approved
			FROM wiki_page_revisions r
			JOIN wiki_pages p ON p.id=r.page_id
			WHERE %s
		) listed
		WHERE %s
		ORDER BY created_at DESC, id DESC LIMIT 50 OFFSET ?
	`, reviewFilter, strings.Join(conditions, " AND "), statusFilter)

	allArgs := append(prefixArgs, args...)
	allArgs = append(allArgs, (page-1)*50)
	return QueryRowsMap(ctx, r.store.DB, q, allArgs...)
}

// GetRevisionDetail 查询管理端单条修订详情、与上一版本对比 Diff、原始证据与审校结论（对齐 routes/wiki.py:revision_detail）。
func (r *PublicReader) GetRevisionDetail(ctx context.Context, channelID, revisionID, jobID string) (map[string]any, error) {
	rows, err := QueryRowsMap(ctx, r.store.DB, "SELECT id, page_id, document, created_at FROM wiki_page_revisions WHERE id=? AND channel_id=?", revisionID, channelID)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	row := rows[0]
	doc := decodeMapJSON([]byte(mapString(row, "document")))

	var prevDoc map[string]any
	oldRows, _ := QueryRowsMap(ctx, r.store.DB, "SELECT document FROM wiki_page_revisions WHERE page_id=? AND created_at<? ORDER BY created_at DESC LIMIT 1", row["page_id"], row["created_at"])
	if len(oldRows) > 0 {
		prevDoc = decodeMapJSON([]byte(mapString(oldRows[0], "document")))
	} else {
		prevDoc = map[string]any{}
	}

	changes, truncated := RevisionChanges(prevDoc, doc)
	diffLines := UnifiedDiff(
		strings.Split(EncodeJSON(prevDoc), "\n"),
		strings.Split(EncodeJSON(doc), "\n"),
		"previous",
		"draft",
	)

	evidence, err := QueryRowsMap(ctx, r.store.DB, `
		SELECT e.source_id, e.start_offset, e.end_offset, s.source_type, s.verified, s.deleted, s.hidden, s.body
		FROM wiki_evidence e JOIN wiki_sources s ON s.id=e.source_id
		WHERE e.revision_id=?
	`, revisionID)
	if err != nil {
		return nil, err
	}

	var jobArg any
	if jobID != "" {
		jobArg = jobID
	}
	var reviewOut any
	revRows, _ := QueryRowsMap(ctx, r.store.DB, `
		SELECT rr.approved, rr.result, rr.created_at
		FROM wiki_revision_reviews rr
		WHERE rr.revision_id=? AND rr.policy_key<>?
		  AND (? IS NULL OR rr.policy_key=(
		      SELECT CAST(JSON_UNQUOTE(JSON_EXTRACT(j.`+"`cursor`"+`,'$.review_policy')) AS CHAR CHARACTER SET ascii)
		      FROM wiki_jobs j WHERE j.channel_id=? AND j.id=?
		  ))
		ORDER BY rr.created_at DESC, rr.policy_key, rr.subject_hash LIMIT 1
	`, revisionID, ManualReviewPolicy, jobArg, channelID, jobArg)
	if len(revRows) > 0 {
		revRows[0]["result"] = decodeMapJSON([]byte(mapString(revRows[0], "result")))
		reviewOut = revRows[0]
	}

	return map[string]any{
		"id":                revisionID,
		"document":          doc,
		"diff":              strings.Join(diffLines, "\n"),
		"changes":           changes,
		"changes_truncated": truncated,
		"evidence":          evidence,
		"review":            reviewOut,
	}, nil
}

// GetPublicationPagePreview 预览指定历史发布快照中的单页内容及与当前线上版本的差异（对齐 routes/wiki.py:publication_page_preview）。
func (r *PublicReader) GetPublicationPagePreview(ctx context.Context, channelID, publicationID, pageID string) (map[string]any, error) {
	var out map[string]any
	err := r.store.WithSnapshot(ctx, func(sCtx context.Context, tx *sql.Tx) error {
		pRows, err := QueryRowsMap(sCtx, tx, `
			SELECT pub.id, pub.created_at, c.published_revision
			FROM wiki_publications pub
			JOIN wiki_channels c ON c.id=pub.channel_id
			JOIN wiki_pages p ON p.channel_id=c.id
			WHERE pub.channel_id=? AND pub.id=? AND p.id=?
		`, channelID, publicationID, pageID)
		if err != nil || len(pRows) == 0 {
			return err
		}
		pub := pRows[0]
		curPubRev := mapString(pub, "published_revision")

		vRows, err := QueryRowsMap(sCtx, tx, fmt.Sprintf(`
			SELECT pp.publication_id, r.id, r.document,
			       (%s AND NOT COALESCE(JSON_CONTAINS(JSON_EXTRACT(c.config,'$.hidden_topics'), JSON_QUOTE(p.topic)), FALSE)) AS visible
			FROM wiki_publication_pages pp
			JOIN wiki_pages p ON p.id=pp.page_id
			JOIN wiki_page_revisions r ON r.id=pp.revision_id AND r.page_id=p.id AND r.channel_id=p.channel_id
			JOIN wiki_channels c ON c.id=p.channel_id
			WHERE c.id=? AND p.id=? AND pp.publication_id IN (?, ?)
		`, RevisionVisibleSQL("r", "p")), channelID, pageID, publicationID, curPubRev)
		if err != nil {
			return err
		}
		versions := map[string]map[string]any{}
		for _, vr := range vRows {
			versions[mapString(vr, "publication_id")] = vr
		}

		target := versions[publicationID]
		current := versions[curPubRev]
		isRowVisible := func(m map[string]any) bool {
			return m != nil && (anyToInt64(m["visible"]) != 0 || mapBool(m, "visible"))
		}

		var doc map[string]any
		if isRowVisible(target) {
			doc = decodeMapJSON([]byte(mapString(target, "document")))
		}
		curDoc := map[string]any{}
		if isRowVisible(current) {
			curDoc = decodeMapJSON([]byte(mapString(current, "document")))
		}

		changes := []map[string]any{}
		truncated := false
		status := "missing"
		var docOut any
		if doc != nil {
			status = "available"
			changes, truncated = RevisionChanges(curDoc, doc)
			var claimsOut []map[string]any
			claimsRaw, _ := doc["claims"].([]any)
			for _, rc := range claimsRaw {
				if cm, ok := rc.(map[string]any); ok {
					claimsOut = append(claimsOut, map[string]any{
						"key":             cm["key"],
						"text":            cm["text"],
						"community_notes": PublicNotes(cm["community_notes"]),
					})
				}
			}
			if claimsOut == nil {
				claimsOut = []map[string]any{}
			}
			docOut = map[string]any{
				"title":       doc["title"],
				"title_notes": PublicNotes(doc["title_notes"]),
				"claims":      claimsOut,
			}
		} else if target != nil {
			status = "unavailable"
		}

		out = map[string]any{
			"publication_id": publicationID,
			"created_at":     pub["created_at"],
			"current":        publicationID == curPubRev,
			"status":         status,
			"document":       docOut,
			"changes":        changes,
			"truncated":      truncated,
		}
		return nil
	})
	return out, err
}
