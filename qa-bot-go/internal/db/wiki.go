// Copyright 2026 Sakura-Bot. AGPL-3.0.
package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/contracts"
)

// These predicates deliberately match core/wiki/schema.py. No stored publication is
// readable without rechecking current source visibility and hashes.
const wikiChannelVisible = `c.enabled=TRUE AND c.public=TRUE AND c.access_verified=TRUE
    AND c.username IS NOT NULL AND c.verified_until>UTC_TIMESTAMP(6)
    AND c.published_revision IS NOT NULL`
const wikiRevisionVisible = `r.approved=TRUE AND r.rejected=FALSE AND p.hidden=FALSE
    AND EXISTS (SELECT 1 FROM wiki_evidence ev WHERE ev.revision_id=r.id)
    AND NOT EXISTS (SELECT 1 FROM wiki_evidence ev JOIN wiki_sources s ON s.id=ev.source_id
        WHERE ev.revision_id=r.id AND (s.channel_id<>r.channel_id OR s.deleted=TRUE
        OR s.hidden=TRUE OR s.excluded=TRUE OR s.verified=FALSE OR s.content_hash<>ev.source_hash
        OR (s.source_type='image' AND NOT EXISTS (SELECT 1 FROM wiki_sources parent
            WHERE parent.channel_id=s.channel_id AND parent.source_type='message'
            AND parent.message_id=s.message_id AND parent.media_key=s.media_key
            AND parent.deleted=FALSE AND parent.hidden=FALSE AND parent.excluded=FALSE AND parent.verified=TRUE))))`
const wikiVisible = wikiChannelVisible + ` AND ` + wikiRevisionVisible + `
    AND NOT JSON_CONTAINS(JSON_EXTRACT(c.config,'$.hidden_topics'),JSON_QUOTE(p.topic))`
const wikiPageJoin = ` FROM wiki_channels c JOIN wiki_publication_pages pp ON pp.publication_id=c.published_revision
    JOIN wiki_pages p ON p.id=pp.page_id AND p.channel_id=c.id
    JOIN wiki_page_revisions r ON r.id=pp.revision_id AND r.channel_id=c.id `

var wikiKey = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,180}$`)

// WikiRepository is read-only and owns neither schema migration nor knowledge generation.
type WikiRepository struct{ DB *sql.DB }

func (w *WikiRepository) ReadWiki(ctx context.Context, operation string, opts contracts.WikiOptions) (map[string]any, error) {
	if w == nil || w.DB == nil {
		return nil, errors.New("Wiki unavailable; use existing RAG tools")
	}
	if len([]rune(opts.Query)) > 200 || len(opts.Channel) > 255 {
		return nil, errors.New("invalid Wiki query")
	}
	if opts.Page < 1 {
		opts.Page = 1
	}
	if opts.Page > 10000 {
		opts.Page = 10000
	}
	if opts.PageSize < 1 {
		opts.PageSize = 20
	}
	if opts.PageSize > 50 {
		opts.PageSize = 50
	}
	if (opts.Slug != "" && !wikiKey.MatchString(opts.Slug)) || (opts.NodeID != "" && !wikiKey.MatchString(opts.NodeID)) {
		return nil, errors.New("invalid Wiki identifier")
	}
	for _, date := range []string{opts.After, opts.Before} {
		if date != "" {
			if _, err := time.Parse(time.RFC3339, date); err != nil {
				return nil, errors.New("Wiki dates must use RFC3339")
			}
		}
	}
	tx, err := w.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return nil, errors.New("Wiki unavailable; use existing RAG tools")
	}
	defer func() { _ = tx.Rollback() }()
	if opts.Channel != "" {
		key := normalizeWikiChannel(opts.Channel)
		if err := tx.QueryRowContext(ctx, `SELECT c.id FROM wiki_channels c JOIN wiki_channel_aliases a ON a.channel_id=c.id WHERE a.alias=? AND `+wikiChannelVisible, key).Scan(&opts.Channel); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return map[string]any{"available": false, "items": []any{}, "reason": "No public published Wiki; use existing RAG tools"}, nil
			}
			return nil, errors.New("Wiki unavailable; use existing RAG tools")
		}
	}
	var result map[string]any
	switch operation {
	case "wiki_search", "wiki_get_changes":
		result, err = wikiSearch(ctx, tx, opts)
	case "wiki_get_page":
		result, err = wikiPage(ctx, tx, opts)
	case "wiki_get_related":
		result, err = wikiRelated(ctx, tx, opts)
	case "wiki_get_channel_profile":
		result, err = wikiProfile(ctx, tx, opts)
	default:
		return nil, errors.New("unknown Wiki read operation")
	}
	if err != nil {
		return nil, errors.New("Wiki unavailable or content withdrawn; use existing RAG tools")
	}
	if err = tx.Commit(); err != nil {
		return nil, errors.New("Wiki read interrupted; retry or use RAG")
	}
	return result, nil
}

func normalizeWikiChannel(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	for _, prefix := range []string{"https://t.me/", "http://t.me/", "t.me/", "@"} {
		value = strings.TrimPrefix(value, prefix)
	}
	value = strings.TrimSuffix(value, "/")
	return strings.TrimPrefix(value, "-100")
}

func wikiLike(value string) string {
	return "%" + strings.NewReplacer("=", "==", "%", "=%", "_", "=_").Replace(value) + "%"
}

func wikiSearch(ctx context.Context, tx *sql.Tx, opts contracts.WikiOptions) (map[string]any, error) {
	where, args := wikiVisible, []any{}
	if opts.Channel != "" {
		where += " AND c.id=?"
		args = append(args, opts.Channel)
	}
	if opts.Query != "" {
		where += ` AND (r.search_text LIKE ? ESCAPE '=' OR EXISTS (SELECT 1 FROM wiki_entity_aliases a WHERE a.page_id=p.id AND a.name LIKE ? ESCAPE '='))`
		args = append(args, wikiLike(opts.Query), wikiLike(opts.Query))
	}
	if opts.Kind != "" {
		where += " AND p.kind=?"
		args = append(args, opts.Kind)
	}
	if opts.After != "" {
		where += " AND r.created_at>=?"
		t, _ := time.Parse(time.RFC3339, opts.After)
		args = append(args, t.UTC())
	}
	if opts.Before != "" {
		where += " AND r.created_at<=?"
		t, _ := time.Parse(time.RFC3339, opts.Before)
		args = append(args, t.UTC())
	}
	var total int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*)"+wikiPageJoin+" WHERE "+where, args...).Scan(&total); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT p.id,p.slug,p.title,p.kind,p.topic,c.id,c.title,c.published_revision,r.id,r.created_at,r.document,c.config`+wikiPageJoin+" WHERE "+where+" ORDER BY r.created_at DESC,p.id LIMIT ? OFFSET ?", append(args, opts.PageSize, (opts.Page-1)*opts.PageSize)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	items := []any{}
	for rows.Next() {
		card, _, _, err := scanWikiPage(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, card)
	}
	return map[string]any{"items": items, "total": total, "page": opts.Page, "page_size": opts.PageSize}, rows.Err()
}

type wikiScanner interface{ Scan(...any) error }

func scanWikiPage(scanner wikiScanner) (map[string]any, map[string]any, map[string]any, error) {
	var id, slug, title, kind, topic, channel, channelTitle, pub, revision string
	var created time.Time
	var raw, configRaw []byte
	if err := scanner.Scan(&id, &slug, &title, &kind, &topic, &channel, &channelTitle, &pub, &revision, &created, &raw, &configRaw); err != nil {
		return nil, nil, nil, err
	}
	var document, config map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, nil, nil, err
	}
	if err := json.Unmarshal(configRaw, &config); err != nil {
		return nil, nil, nil, err
	}
	path := "/wiki/" + url.PathEscape(channel) + "/page/" + url.PathEscape(slug)
	card := map[string]any{"id": id, "slug": slug, "title": document["title"], "kind": kind, "topic": topic, "channel_id": channel, "channel_title": channelTitle, "publication_id": pub, "revision_id": revision, "updated_at": created.UTC().Format(time.RFC3339), "url": path, "wiki_url": wikiAbsoluteURL(config, path)}
	if facts, ok := document["claims"].([]any); ok && len(facts) > 0 {
		fact, _ := facts[0].(map[string]any)
		text, _ := fact["text"].(string)
		runes := []rune(text)
		if len(runes) > 240 {
			runes = runes[:240]
		}
		card["excerpt"] = string(runes)
	}
	return card, document, config, nil
}

func wikiAbsoluteURL(config map[string]any, path string) any {
	base, _ := config["public_base_url"].(string)
	parsed, err := url.Parse(base)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.User != nil {
		return nil
	}
	return strings.TrimRight(base, "/") + path
}

func wikiPage(ctx context.Context, tx *sql.Tx, opts contracts.WikiOptions) (map[string]any, error) {
	if opts.Channel == "" || opts.Slug == "" {
		return nil, errors.New("channel and slug required")
	}
	row := tx.QueryRowContext(ctx, `SELECT p.id,p.slug,p.title,p.kind,p.topic,c.id,c.title,c.published_revision,r.id,r.created_at,r.document,c.config`+wikiPageJoin+` WHERE c.id=? AND (p.slug=? OR p.id=? OR EXISTS (SELECT 1 FROM wiki_slug_aliases a WHERE a.channel_id=c.id AND a.page_id=p.id AND a.slug=?)) AND `+wikiVisible+" LIMIT 1", opts.Channel, opts.Slug, opts.Slug, opts.Slug)
	card, document, config, err := scanWikiPage(row)
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT s.id,s.source_type,s.message_id,s.created_at,s.edited_at,c.username FROM wiki_evidence e JOIN wiki_sources s ON s.id=e.source_id JOIN wiki_channels c ON c.id=s.channel_id WHERE e.revision_id=?`, card["revision_id"])
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	sources := map[string]map[string]any{}
	sourceList := []any{}
	for rows.Next() {
		var id, kind, username string
		var mid sql.NullInt64
		var created, edited time.Time
		if err := rows.Scan(&id, &kind, &mid, &created, &edited, &username); err != nil {
			return nil, err
		}
		var link any
		if config["show_telegram_links"] == true && mid.Valid && kind != "summary" && wikiKey.MatchString(username) {
			link = fmt.Sprintf("https://t.me/%s/%d", username, mid.Int64)
		}
		source := map[string]any{"id": id, "type": kind, "created_at": created.UTC().Format(time.RFC3339), "edited_at": edited.UTC().Format(time.RFC3339), "url": link}
		sources[id] = source
		sourceList = append(sourceList, source)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	facts, _ := document["claims"].([]any)
	safeFacts := []any{}
	for _, rawFact := range facts {
		fact, ok := rawFact.(map[string]any)
		if !ok {
			continue
		}
		safe := map[string]any{}
		for _, key := range []string{"key", "text", "status", "confidence", "occurred_at", "valid_until", "source_created_at"} {
			safe[key] = fact[key]
		}
		evidence := []any{}
		refs, _ := fact["evidence"].([]any)
		for _, rawRef := range refs {
			ref, ok := rawRef.(map[string]any)
			if !ok {
				continue
			}
			id, _ := ref["source_id"].(string)
			source := sources[id]
			if source == nil {
				return nil, errors.New("missing evidence")
			}
			var quote any
			if config["show_quotes"] == true {
				quote = ref["quote"]
			}
			evidence = append(evidence, map[string]any{"source_id": id, "start": ref["start"], "end": ref["end"], "quote": quote, "url": source["url"]})
		}
		safe["evidence"] = evidence
		safeFacts = append(safeFacts, safe)
	}
	card["claims"], card["sources"], card["verified_at"] = safeFacts, sourceList, document["verified_at"]
	return card, nil
}

func wikiRelated(ctx context.Context, tx *sql.Tx, opts contracts.WikiOptions) (map[string]any, error) {
	if opts.Channel == "" || opts.NodeID == "" {
		return nil, errors.New("channel and node_id required")
	}
	root, err := wikiPage(ctx, tx, contracts.WikiOptions{Channel: opts.Channel, Slug: opts.NodeID})
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT e.id,e.source_page,e.target_page,e.kind,e.relation_json FROM wiki_publication_edges pe
	JOIN wiki_edges e ON e.id=pe.edge_id JOIN wiki_chunks ch ON ch.id=e.chunk_id JOIN wiki_sources s ON s.id=ch.source_id
	WHERE pe.publication_id=? AND e.channel_id=? AND (e.source_page=? OR e.target_page=?)
	AND s.deleted=FALSE AND s.hidden=FALSE AND s.excluded=FALSE AND s.verified=TRUE AND s.content_hash=e.source_hash ORDER BY e.id LIMIT ?`, root["publication_id"], opts.Channel, root["id"], root["id"], opts.PageSize)
	if err != nil {
		return nil, err
	}
	edges := []map[string]any{}
	for rows.Next() {
		var id, source, target, kind string
		var raw []byte
		if err := rows.Scan(&id, &source, &target, &kind, &raw); err != nil {
			_ = rows.Close()
			return nil, err
		}
		var relation map[string]any
		if err := json.Unmarshal(raw, &relation); err != nil {
			_ = rows.Close()
			return nil, err
		}
		edges = append(edges, map[string]any{"id": id, "source": source, "target": target, "kind": kind, "confidence": relation["confidence"]})
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()
	items := []any{}
	safeEdges := []any{}
	seen := map[string]bool{}
	for _, edge := range edges {
		id, _ := edge["target"].(string)
		if id == root["id"] {
			id, _ = edge["source"].(string)
		}
		page, err := wikiPage(ctx, tx, contracts.WikiOptions{Channel: opts.Channel, Slug: id})
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		safeEdges = append(safeEdges, edge)
		if !seen[id] {
			seen[id] = true
			items = append(items, page)
		}
	}
	return map[string]any{"items": items, "edges": safeEdges, "publication_id": root["publication_id"]}, nil
}

func wikiProfile(ctx context.Context, tx *sql.Tx, opts contracts.WikiOptions) (map[string]any, error) {
	if opts.Channel == "" {
		return nil, errors.New("channel required")
	}
	var pub, title string
	var coverageRaw, configRaw, profileRaw []byte
	var generation int64
	if err := tx.QueryRowContext(ctx, `SELECT c.published_revision,c.title,c.coverage,c.config,c.source_generation,pub.profile FROM wiki_channels c JOIN wiki_publications pub ON pub.id=c.published_revision WHERE c.id=? AND `+wikiChannelVisible, opts.Channel).Scan(&pub, &title, &coverageRaw, &configRaw, &generation, &profileRaw); err != nil {
		return nil, err
	}
	var count int64
	var average float64
	var first, last sql.NullTime
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(AVG(original_length),0),MIN(created_at),MAX(created_at) FROM wiki_sources
	WHERE channel_id=? AND source_type='message' AND deleted=FALSE AND hidden=FALSE AND excluded=FALSE AND verified=TRUE`, opts.Channel).Scan(&count, &average, &first, &last); err != nil {
		return nil, err
	}
	var coverage map[string]any
	if err := json.Unmarshal(coverageRaw, &coverage); err != nil {
		return nil, err
	}
	safeCoverage := map[string]any{}
	for _, key := range []string{"status", "reason", "enumerated", "indexed", "excluded", "estimated_total", "earliest", "latest", "missing_ranges", "failures", "updated_at"} {
		safeCoverage[key] = coverage[key]
	}
	scope := ` FROM wiki_sources WHERE channel_id=? AND source_type='message' AND deleted=FALSE AND hidden=FALSE AND excluded=FALSE AND verified=TRUE `
	hours, err := wikiReadRows(ctx, tx, "SELECT HOUR(created_at) hour,COUNT(*) count"+scope+"GROUP BY hour ORDER BY hour", opts.Channel)
	if err != nil {
		return nil, err
	}
	months, err := wikiReadRows(ctx, tx, "SELECT DATE_FORMAT(created_at,'%Y-%m') month,COUNT(*) count"+scope+"GROUP BY month ORDER BY month", opts.Channel)
	if err != nil {
		return nil, err
	}
	topicScope := ` FROM wiki_publication_pages pp JOIN wiki_pages p ON p.id=pp.page_id
            JOIN wiki_page_revisions r ON r.id=pp.revision_id JOIN wiki_evidence ev ON ev.revision_id=r.id
            JOIN wiki_sources s ON s.id=ev.source_id JOIN wiki_channels c ON c.id=p.channel_id
            WHERE pp.publication_id=? AND p.channel_id=? AND s.source_type='message' AND ` + wikiRevisionVisible + `
            AND NOT JSON_CONTAINS(JSON_EXTRACT(c.config,'$.hidden_topics'),JSON_QUOTE(p.topic)) `
	topics, err := wikiReadRows(ctx, tx, "SELECT p.topic,COUNT(DISTINCT s.id) message_count"+topicScope+"GROUP BY p.topic ORDER BY message_count DESC LIMIT 100", pub, opts.Channel)
	if err != nil {
		return nil, err
	}
	trends, err := wikiReadRows(ctx, tx, "SELECT p.topic,DATE_FORMAT(s.created_at,'%Y-%m') month,COUNT(DISTINCT s.id) message_count"+topicScope+"GROUP BY p.topic,month ORDER BY month,p.topic", pub, opts.Channel)
	if err != nil {
		return nil, err
	}
	var snapshot, config map[string]any
	if err := json.Unmarshal(profileRaw, &snapshot); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(configRaw, &config); err != nil {
		return nil, err
	}
	narrative := map[string]any{}
	hidden, _ := config["hidden_topics"].([]any)
	if snapshot["source_generation"] == float64(generation) && len(hidden) == 0 {
		if raw, ok := snapshot["narrative"].(map[string]any); ok {
			for _, key := range []string{"description", "style", "limitations"} {
				narrative[key] = raw[key]
			}
		}
	}
	return map[string]any{"channel_id": opts.Channel, "title": title, "publication_id": pub, "coverage": safeCoverage, "statistics": map[string]any{"message_count": count, "avg_message_length": average, "earliest": nullWikiTime(first), "latest": nullWikiTime(last)}, "hours_utc": hours, "months": months, "topics": topics, "topic_trends": trends, "narrative": narrative, "as_of": time.Now().UTC().Format(time.RFC3339), "method": "Actual verified messages; topics may overlap. No user profiling."}, nil
}

// Only fixed, field-whitelisted profile SELECTs use this helper.
func wikiReadRows(ctx context.Context, tx *sql.Tx, query string, args ...any) ([]map[string]any, error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			return nil, err
		}
		item := map[string]any{}
		for i, key := range columns {
			if b, ok := values[i].([]byte); ok {
				item[key] = string(b)
			} else {
				item[key] = values[i]
			}
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func nullWikiTime(value sql.NullTime) any {
	if !value.Valid {
		return nil
	}
	return value.Time.UTC().Format(time.RFC3339)
}
