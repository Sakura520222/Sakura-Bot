// Copyright 2026 Sakura-Bot. AGPL-3.0.
package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/url"
	"time"

	"github.com/Sakura520222/Sakura-Bot/internal/contracts"
)

// Like PublicReader.PUBLISHED_HISTORY, this is applied only after resolving a
// currently visible article in the same read snapshot.
const wikiPublishedHistory = ` FROM wiki_page_revisions r JOIN wiki_pages p ON p.id=r.page_id
	WHERE p.id=? AND r.channel_id=p.channel_id AND ` + wikiRevisionVisible + `
	AND EXISTS (SELECT 1 FROM wiki_publication_pages pp
		JOIN wiki_publications pub ON pub.id=pp.publication_id
		WHERE pp.page_id=p.id AND pp.revision_id=r.id AND pub.channel_id=p.channel_id)`

func wikiVersionURL(path, revision string) string {
	return path + "?" + url.Values{"revision": {revision}}.Encode()
}

func wikiHistoricalPage(ctx context.Context, tx *sql.Tx, revision string, card, config map[string]any) (map[string]any, error) {
	var raw []byte
	var created time.Time
	if err := tx.QueryRowContext(ctx, "SELECT r.document,r.created_at"+wikiPublishedHistory+" AND r.id=?", card["id"], revision).Scan(&raw, &created); err != nil {
		return nil, err
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, err
	}
	path := wikiVersionURL(card["url"].(string), revision)
	card["url"], card["wiki_url"] = path, wikiAbsoluteURL(config, path)
	card["revision_id"], card["updated_at"] = revision, created.UTC().Format(time.RFC3339)
	wikiDocumentCard(card, document)
	return document, nil
}

func wikiRevisionHistory(ctx context.Context, tx *sql.Tx, opts contracts.WikiOptions) (map[string]any, error) {
	card, _, config, err := wikiCurrentPage(ctx, tx, opts)
	if err != nil {
		return nil, err
	}
	var total int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*)"+wikiPublishedHistory, card["id"]).Scan(&total); err != nil {
		return nil, err
	}
	current := card["revision_id"].(string)
	rows, err := tx.QueryContext(ctx, "SELECT r.id,r.created_at"+wikiPublishedHistory+
		" ORDER BY r.id=? DESC,r.created_at DESC,r.id DESC LIMIT ? OFFSET ?",
		card["id"], current, opts.PageSize, (opts.Page-1)*opts.PageSize)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	items := []map[string]any{}
	for rows.Next() {
		var revision string
		var created time.Time
		if err := rows.Scan(&revision, &created); err != nil {
			return nil, err
		}
		path := wikiVersionURL(card["url"].(string), revision)
		items = append(items, map[string]any{
			"revision_id": revision, "created_at": created.UTC().Format(time.RFC3339),
			"current": revision == current, "url": path, "wiki_url": wikiAbsoluteURL(config, path),
		})
	}
	return map[string]any{
		"items": items, "total": total, "page": opts.Page, "page_size": opts.PageSize,
		"channel_id": opts.Channel, "slug": card["slug"], "title": card["title"],
		"current_revision_id": current, "wiki_url": card["wiki_url"],
	}, rows.Err()
}
