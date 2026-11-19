// Copyright 2026 Sakura-Bot. AGPL-3.0.
package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/url"
	"reflect"
	"strings"

	"github.com/Sakura520222/Sakura-Bot/internal/contracts"
)

// Match the public contributor page: credit only contributions still present
// in the current publication, honoring current account privacy.
const wikiContributorFields = `u.id,u.display_name,IF(u.show_username,u.username,NULL) username,
	u.show_avatar AND u.avatar IS NOT NULL avatar`
const wikiContributionJoin = ` FROM wiki_contributions x
	JOIN wiki_edit_proposals e ON e.id=x.proposal_id JOIN wiki_users u ON u.id=e.user_id
	JOIN wiki_channels c ON c.id=e.channel_id JOIN wiki_pages p ON p.id=e.page_id AND p.channel_id=c.id
	JOIN wiki_publication_pages pp ON pp.publication_id=c.published_revision AND pp.page_id=p.id
	JOIN wiki_page_revisions r ON r.id=pp.revision_id AND r.channel_id=c.id
	AND (r.id=e.revision_id OR JSON_CONTAINS(JSON_EXTRACT(r.document,'$.community_contributions'),JSON_QUOTE(e.id)))
	WHERE e.status='approved' AND u.blocked=FALSE AND u.anonymous_contributions=FALSE AND ` + wikiVisible

func wikiContributors(ctx context.Context, tx *sql.Tx, opts contracts.WikiOptions) (map[string]any, error) {
	where, args := wikiContributionJoin, []any{}
	if opts.Channel != "" {
		where += " AND e.channel_id=?"
		args = append(args, opts.Channel)
	}
	if opts.Slug != "" {
		card, _, _, err := wikiCurrentPage(ctx, tx, opts)
		if err != nil {
			return nil, err
		}
		where += " AND e.page_id=?"
		args = append(args, card["id"])
	}
	selectFields := wikiContributorFields + `,COUNT(*) total,MAX(x.created_at) latest,
		CAST(SUM(e.kind IN ('edit','correction')) AS UNSIGNED) edits,
		CAST(SUM(e.kind IN ('annotation','dispute','outdated','fact_check')) AS UNSIGNED) annotations,
		CAST(SUM(e.kind='source') AS UNSIGNED) sources,
		MIN(NULLIF(c.config->>'$.public_base_url','')) base_url`
	order := " GROUP BY u.id,u.display_name,username,avatar ORDER BY total DESC,latest DESC,u.id"
	if opts.ContributorID != "" {
		where += " AND u.id=?"
		args = append(args, opts.ContributorID)
		selectFields = wikiContributorFields + `,e.kind,p.title,p.slug,e.channel_id,x.created_at,
			c.config->>'$.public_base_url' base_url`
		order = " ORDER BY x.created_at DESC,x.proposal_id"
	}
	items, err := wikiReadRows(ctx, tx, "SELECT "+selectFields+where+order+" LIMIT ? OFFSET ?",
		append(args, opts.PageSize, (opts.Page-1)*opts.PageSize)...)
	if err != nil {
		return nil, err
	}
	if opts.ContributorID != "" && len(items) == 0 && opts.Page == 1 {
		return nil, sql.ErrNoRows
	}
	for _, item := range items {
		config := map[string]any{"public_base_url": item["base_url"]}
		delete(item, "base_url")
		path := "/wiki/contributors/" + url.PathEscape(item["id"].(string))
		item["contributor_url"] = wikiAbsoluteURL(config, path)
		if channel, ok := item["channel_id"].(string); ok {
			path = "/wiki/" + url.PathEscape(channel) + "/page/" + url.PathEscape(item["slug"].(string))
			item["wiki_url"] = wikiAbsoluteURL(config, path)
		}
		item["url"] = path
	}
	return map[string]any{"items": items, "page": opts.Page, "page_size": opts.PageSize}, nil
}

type wikiNoteCredit struct {
	byProposal map[string]map[string]any
	proposals  []wikiNoteProposal
}

type wikiNoteProposal struct {
	id, kind string
	payload  map[string]any
}

func wikiNoteAttributions(ctx context.Context, tx *sql.Tx, page string, document map[string]any) (wikiNoteCredit, error) {
	credit := wikiNoteCredit{byProposal: map[string]map[string]any{}}
	ids, _ := document["community_contributions"].([]any)
	if len(ids) == 0 {
		return credit, nil
	}
	args := append([]any{page}, ids...)
	marks := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	rows, err := tx.QueryContext(ctx, `SELECT e.id,e.kind,e.payload,
		IF(x.proposal_id IS NOT NULL AND u.blocked=FALSE AND u.anonymous_contributions=FALSE,u.id,NULL),
		u.display_name,IF(u.show_username,u.username,NULL),u.show_avatar AND u.avatar IS NOT NULL
		FROM wiki_edit_proposals e JOIN wiki_users u ON u.id=e.user_id
		LEFT JOIN wiki_contributions x ON x.proposal_id=e.id
		WHERE e.page_id=? AND e.id IN (`+marks+`) AND e.status='approved'`, args...)
	if err != nil {
		return credit, err
	}
	defer rows.Close() //nolint:errcheck
	for rows.Next() {
		var proposal wikiNoteProposal
		var contributor, username sql.NullString
		var displayName string
		var avatar bool
		var raw []byte
		if err := rows.Scan(&proposal.id, &proposal.kind, &raw, &contributor, &displayName, &username, &avatar); err != nil {
			return credit, err
		}
		if err := json.Unmarshal(raw, &proposal.payload); err != nil {
			return credit, err
		}
		credit.proposals = append(credit.proposals, proposal)
		if contributor.Valid {
			author := map[string]any{"id": contributor.String, "display_name": displayName, "avatar": avatar}
			if username.Valid {
				author["username"] = username.String
			}
			credit.byProposal[proposal.id] = author
		}
	}
	return credit, rows.Err()
}

func (credit wikiNoteCredit) legacyProposal(note map[string]any, ordinal int) string {
	matched := ""
	for _, proposal := range credit.proposals {
		excerpt, _ := note["excerpt"].(string)
		original, _ := proposal.payload["excerpt"].(string)
		if proposal.kind == note["kind"] && proposal.payload["claim"] == float64(ordinal) &&
			proposal.payload["text"] == note["text"] && excerpt == original &&
			reflect.DeepEqual(proposal.payload["sources"], note["sources"]) {
			if matched != "" {
				return ""
			}
			matched = proposal.id
		}
	}
	return matched
}

func wikiPublicNotes(fact map[string]any, ordinal int, credit wikiNoteCredit) []map[string]any {
	raw, _ := fact["community_notes"].([]any)
	notes := []map[string]any{}
	for _, entry := range raw {
		note, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		public := map[string]any{}
		for _, key := range []string{"kind", "text", "sources", "excerpt", "excerpt_occurrence"} {
			if value, present := note[key]; present {
				public[key] = value
			}
		}
		proposal, _ := note["proposal_id"].(string)
		if proposal == "" {
			proposal = credit.legacyProposal(note, ordinal)
		}
		if author := credit.byProposal[proposal]; author != nil {
			public["contributor"] = author
		}
		notes = append(notes, public)
	}
	return notes
}
