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
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/Sakura520222/Sakura-Bot/internal/db"
)

var (
	// ErrPageEditConflict 表示词条发布基线已变化或已被隐藏，需刷新后重试。
	ErrPageEditConflict = errors.New("该词条已有其他发布版本，请在最新版本上重新提交修订")
	// ErrPublicationAccessUnverified 表示频道当前未通过 Telegram 公开性核实，无法发布快照。
	ErrPublicationAccessUnverified = errors.New("暂无法核实频道的 Telegram 公开状态。草稿已保留，核实后可重试发布。")
	errPublicationEvidence         = errors.New("Publication includes revoked, rejected or hidden evidence")
	errPublicationReview           = errors.New("Publication requires independent review of each new revision")
	errPublicationReviewReenabled  = errors.New("已重新启用 AI 来源审校，请重新审校后发布")
)

// RevisionSeedItem 表示页面及其基线修订条目。
type RevisionSeedItem struct {
	PageID     string
	RevisionID string
	Topic      string
}

// EvidenceRow 表示修订证据坐标行。
type EvidenceRow struct {
	SourceID    string `json:"source_id"`
	SourceHash  string `json:"source_hash"`
	ChunkID     string `json:"chunk_id"`
	StartOffset int    `json:"start_offset"`
	EndOffset   int    `json:"end_offset"`
}

// CorrectionOptions 控制 CreateCorrection 的修订行为（替换正文、追加注解或社区投稿归属）。
type CorrectionOptions struct {
	Annotation      map[string]any
	AnnotationIndex *int
	ProposalID      string
	ReplaceText     bool
	Standalone      bool
}

// PageManageOptions 控制管理员对词条的重命名与合并/拆分参数。
type PageManageOptions struct {
	Title string
	Slug  string
	Alias string
}

// PublishOption 配置 Knowledge.Publish。
type PublishOption func(*publishConfig)

type publishConfig struct {
	rollbackID string
	runningJob *JobRecord
}

// WithRollback 指定回滚目标发布快照 ID。
func WithRollback(publicationID string) PublishOption {
	return func(c *publishConfig) {
		c.rollbackID = strings.TrimSpace(publicationID)
	}
}

// WithRunningJob 传入当前持有租约的运行中任务以在同一发布事务内验证租约围栏。
func WithRunningJob(job *JobRecord) PublishOption {
	return func(c *publishConfig) {
		c.runningJob = job
	}
}

// ValidateTitleCorrection 校验词条标题修订合法性（1..160 字符、单行、不含空字节）。
func ValidateTitleCorrection(text string) error {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || utf8.RuneCountInString(trimmed) > 160 ||
		strings.ContainsAny(text, "\r\n\x00") {
		return errors.New("词条标题必须为 1 至 160 个字符且只能有一行")
	}
	return nil
}

// Knowledge 负责增量实体消歧、页面文档装配、审校指纹核验、原子发布/回滚及管理员词条策展。
type Knowledge struct {
	store *Store
}

// NewKnowledge 创建知识装配与发布管理器。
func NewKnowledge(store *Store) *Knowledge {
	return &Knowledge{store: store}
}

// RepairGeneratedTitleSeparators 启动时修复早期生成数据中缺失分类名的前导斜杠标题（对齐 knowledge.py:repair_generated_title_separators）。
func (k *Knowledge) RepairGeneratedTitleSeparators(ctx context.Context) error {
	rows, err := k.store.DB.QueryContext(ctx, `
		SELECT id, channel_id FROM wiki_pages
		WHERE topic='频道资料' AND locked=FALSE
		  AND JSON_EXTRACT(override_json, '$.title') IS NULL
		  AND title REGEXP '^[/／][[:space:]]+'
		ORDER BY channel_id, id
	`)
	if err != nil {
		return err
	}
	type pair struct{ id, ch string }
	var candidates []pair
	for rows.Next() {
		var p pair
		if err := rows.Scan(&p.id, &p.ch); err == nil {
			candidates = append(candidates, p)
		}
	}
	_ = rows.Close()

	for _, c := range candidates {
		if err := k.repairPageTitleSeparator(ctx, c.ch, c.id); err != nil {
			return err
		}
	}
	return nil
}

func (k *Knowledge) repairPageTitleSeparator(ctx context.Context, channelID, pageID string) error {
	return k.store.WithTx(ctx, func(tx *sql.Tx) error {
		var cid string
		if err := tx.QueryRowContext(ctx, "SELECT id FROM wiki_channels WHERE id=? FOR UPDATE", channelID).Scan(&cid); err != nil {
			return nil
		}
		var oldTitle string
		err := tx.QueryRowContext(ctx, `
			SELECT title FROM wiki_pages
			WHERE id=? AND channel_id=? AND topic='频道资料' AND locked=FALSE
			  AND JSON_EXTRACT(override_json, '$.title') IS NULL
		`, pageID, channelID).Scan(&oldTitle)
		if err != nil {
			return nil
		}
		newTitle := StripEmptyTitleCategory(oldTitle)
		if newTitle == oldTitle {
			return nil
		}

		revRows, err := tx.QueryContext(ctx, `
			SELECT id, document, search_text FROM wiki_page_revisions
			WHERE page_id=? AND channel_id=? AND BINARY (document->>'$.title') = BINARY ?
			  AND document->>'$.claims[0].key' LIKE 'md:%'
			  AND NOT COALESCE(JSON_EXTRACT(document, '$.standalone_edit') = TRUE, FALSE)
		`, pageID, channelID, oldTitle)
		if err != nil {
			return err
		}
		type revItem struct {
			id         string
			docRaw     []byte
			searchText string
		}
		var revs []revItem
		for revRows.Next() {
			var r revItem
			if err := revRows.Scan(&r.id, &r.docRaw, &r.searchText); err != nil {
				_ = revRows.Close()
				return err
			}
			revs = append(revs, r)
		}
		_ = revRows.Close()
		if len(revs) == 0 {
			return nil
		}

		if _, err := tx.ExecContext(ctx, "UPDATE wiki_pages SET title=? WHERE id=?", newTitle, pageID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "INSERT IGNORE INTO wiki_entity_aliases (channel_id, alias_key, name, page_id, `manual`) VALUES (?, ?, ?, ?, FALSE)",
			channelID, EntityKey(newTitle), truncateRunesSimple(newTitle, 160), pageID); err != nil {
			return err
		}

		for _, rev := range revs {
			doc := decodeMapJSON(rev.docRaw)
			evList, err := queryEvidenceRowsTx(ctx, tx, rev.id)
			if err != nil {
				return err
			}
			oldSubjectHash := Digest(doc, evList)
			doc["title"] = newTitle
			search := rev.searchText
			if first, rest, hasNL := strings.Cut(search, "\n"); first == oldTitle {
				if hasNL {
					search = newTitle + "\n" + rest
				} else {
					search = newTitle
				}
			}
			contentHash := documentContentHash(doc)
			if _, err := tx.ExecContext(ctx, "UPDATE wiki_page_revisions SET document=?, content_hash=?, search_text=? WHERE id=?",
				EncodeJSON(doc), contentHash, search, rev.id); err != nil {
				return err
			}
			newSubjectHash := Digest(doc, evList)
			_, _ = tx.ExecContext(ctx, "UPDATE IGNORE wiki_revision_reviews SET subject_hash=? WHERE revision_id=? AND subject_hash=?",
				newSubjectHash, rev.id, oldSubjectHash)
		}
		return k.store.Audit(ctx, tx, channelID, "repair_title_separator", pageID, "wiki", map[string]any{
			"from":      oldTitle,
			"to":        newTitle,
			"revisions": len(revs),
		})
	})
}

func documentContentHash(doc map[string]any) string {
	filtered := make(map[string]any, len(doc))
	for k, v := range doc {
		if k != "verified_at" {
			filtered[k] = v
		}
	}
	return Digest(filtered)
}

// ResolveEntity 在事务内按实体名与别名解析或新建词条页面（保留人工 manual=TRUE 绑定，对齐 knowledge.py:entity）。
func (k *Knowledge) ResolveEntity(ctx context.Context, tx *sql.Tx, channelID, name, kind, topic string, aliases []string) (string, error) {
	if kind == "" {
		kind = "Entity"
	}
	if topic == "" {
		topic = "其他"
	}
	key := EntityKey(name)
	var pageID string
	err := tx.QueryRowContext(ctx, "SELECT page_id FROM wiki_entity_aliases WHERE channel_id=? AND alias_key=?", channelID, key).Scan(&pageID)
	if err == nil && pageID != "" {
		return pageID, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}

	for _, alias := range aliases {
		if strings.TrimSpace(alias) == "" {
			continue
		}
		ak := EntityKey(alias)
		var existingPage string
		aErr := tx.QueryRowContext(ctx, "SELECT page_id FROM wiki_entity_aliases WHERE channel_id=? AND alias_key=?", channelID, ak).Scan(&existingPage)
		if aErr == nil && existingPage != "" {
			_, _ = tx.ExecContext(ctx, "INSERT IGNORE INTO wiki_entity_aliases VALUES (?, ?, ?, ?, FALSE)",
				channelID, key, truncateRunesSimple(name, 160), existingPage)
			return existingPage, nil
		}
	}

	pageID = StableID(32, channelID, key)
	cleanTitle := truncateRunesSimple(strings.TrimSpace(name), 160)
	cleanTopic := truncateRunesSimple(strings.TrimSpace(topic), 160)
	if _, err := tx.ExecContext(ctx, `
		INSERT IGNORE INTO wiki_pages
		(id, channel_id, entity_key, title, slug, kind, topic, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, UTC_TIMESTAMP(6))
	`, pageID, channelID, key, cleanTitle, pageID, kind, cleanTopic); err != nil {
		return "", err
	}

	allNames := append([]string{name}, aliases...)
	for _, al := range UniqueStrings(allNames) {
		al = strings.TrimSpace(al)
		if al == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, "INSERT IGNORE INTO wiki_entity_aliases VALUES (?, ?, ?, ?, FALSE)",
			channelID, EntityKey(al), truncateRunesSimple(al, 160), pageID); err != nil {
			return "", err
		}
	}
	return pageID, nil
}

// ApplyExtraction 将单个分块的验证后提取结果落入 wiki_facts、wiki_edges 并登记失效页面（对齐 knowledge.py:apply_extraction）。
func (k *Knowledge) ApplyExtraction(ctx context.Context, job *JobRecord, chunk Chunk, ext *Extraction) error {
	if ext == nil {
		ext = &Extraction{}
	}
	appliedKey := Digest(chunk.ID, chunk.ContentHash, ext)

	return k.store.WithTx(ctx, func(tx *sql.Tx) error {
		var cid string
		if err := tx.QueryRowContext(ctx, "SELECT id FROM wiki_channels WHERE id=? FOR UPDATE", job.ChannelID).Scan(&cid); err != nil {
			return err
		}
		if err := k.store.FenceJob(ctx, tx, job); err != nil {
			return err
		}

		srcRow := tx.QueryRowContext(ctx, `
			SELECT id, channel_id, source_type, external_id, category, message_id, body,
			       original_length, content_hash, revision, created_at, edited_at, observed_at,
			       verified, deleted, hidden, excluded, media_group, media_key, exclusion_reason
			FROM wiki_sources WHERE id=?
		`, chunk.SourceID)
		src, err := scanSourceRow(srcRow)
		if errors.Is(err, sql.ErrNoRows) || src == nil {
			return nil
		}
		if err != nil {
			return err
		}
		if !src.Verified || src.Deleted || src.Hidden || src.MediaGroup != "" || src.ContentHash != chunk.ContentHash {
			return nil
		}

		var curMarker string
		if err := tx.QueryRowContext(ctx, "SELECT applied_key FROM wiki_chunks WHERE id=?", chunk.ID).Scan(&curMarker); err == nil && curMarker == appliedKey {
			return nil
		}

		affected := map[string]bool{}
		oldPages, err := tx.QueryContext(ctx, "SELECT DISTINCT page_id FROM wiki_facts WHERE chunk_id=? AND channel_id=?", chunk.ID, job.ChannelID)
		if err != nil {
			return err
		}
		for oldPages.Next() {
			var pid string
			if err := oldPages.Scan(&pid); err == nil {
				affected[pid] = true
			}
		}
		_ = oldPages.Close()

		if _, err := tx.ExecContext(ctx, "DELETE FROM wiki_facts WHERE chunk_id=? AND channel_id=?", chunk.ID, job.ChannelID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE wiki_edges SET active=FALSE WHERE chunk_id=? AND channel_id=?", chunk.ID, job.ChannelID); err != nil {
			return err
		}

		entities := map[string]string{}
		srcCreatedISO := FormatISO(src.CreatedAt)
		for _, f := range ext.Facts {
			pid, err := k.ResolveEntity(ctx, tx, job.ChannelID, f.Entity, f.Kind, f.Topic, f.Aliases)
			if err != nil {
				return err
			}
			entities[NameKey(f.Entity)] = pid
			affected[pid] = true

			fCopy := f
			fCopy.SourceCreatedAt = srcCreatedISO
			factID := Digest(chunk.ID, pid, f.Key, f.Text)
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO wiki_facts (id, channel_id, page_id, chunk_id, source_hash, fact)
				VALUES (?, ?, ?, ?, ?, ?)
				ON DUPLICATE KEY UPDATE fact=VALUES(fact), page_id=VALUES(page_id)
			`, factID, job.ChannelID, pid, chunk.ID, chunk.ContentHash, EncodeJSON(fCopy)); err != nil {
				return err
			}
		}

		for _, rel := range ext.Relations {
			srcPage := entities[NameKey(rel.Source)]
			if srcPage == "" {
				var err error
				srcPage, err = k.ResolveEntity(ctx, tx, job.ChannelID, rel.Source, "Entity", "其他", nil)
				if err != nil {
					return err
				}
			}
			dstPage := entities[NameKey(rel.Target)]
			if dstPage == "" {
				var err error
				dstPage, err = k.ResolveEntity(ctx, tx, job.ChannelID, rel.Target, "Entity", "其他", nil)
				if err != nil {
					return err
				}
			}
			affected[srcPage] = true
			affected[dstPage] = true

			edgeID := Digest(chunk.ID, srcPage, dstPage, rel)
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO wiki_edges
				(id, channel_id, chunk_id, source_hash, source_page, target_page, kind, relation_json)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?)
				ON DUPLICATE KEY UPDATE active=TRUE
			`, edgeID, job.ChannelID, chunk.ID, chunk.ContentHash, srcPage, dstPage, rel.Kind, EncodeJSON(rel)); err != nil {
				return err
			}
		}

		if _, err := tx.ExecContext(ctx, "UPDATE wiki_chunks SET applied_key=? WHERE id=?", appliedKey, chunk.ID); err != nil {
			return err
		}
		for pid := range affected {
			if _, err := tx.ExecContext(ctx, "INSERT IGNORE INTO wiki_dirty_pages VALUES (?, ?)", job.ChannelID, pid); err != nil {
				return err
			}
		}
		return nil
	})
}

// ApplyAlbum 将多图相册或图文单贴的多模态提取结果原子落入 wiki_pages、wiki_facts、wiki_edges 与 wiki_albums。
func (k *Knowledge) ApplyAlbum(ctx context.Context, job *JobRecord, group string, sources []SourceRecord, title, topic string, facts []map[string]any, relations []Relation) error {
	channelID := job.ChannelID
	return k.store.WithTx(ctx, func(tx *sql.Tx) error {
		var cid string
		if err := tx.QueryRowContext(ctx, "SELECT id FROM wiki_channels WHERE id=? FOR UPDATE", channelID).Scan(&cid); err != nil {
			return err
		}
		if err := k.store.FenceJob(ctx, tx, job); err != nil {
			return err
		}

		current, err := k.store.AlbumSources(ctx, tx, channelID, group)
		if err != nil {
			return err
		}
		if !sameSourceHashes(current, sources) {
			return nil
		}

		rows, err := tx.QueryContext(ctx, `
			SELECT DISTINCT p.id, p.locked, p.hidden
			FROM wiki_sources s
			JOIN wiki_chunks c ON c.source_id=s.id
			JOIN wiki_facts f ON f.chunk_id=c.id
			JOIN wiki_pages p ON p.id=f.page_id
			WHERE s.channel_id=? AND s.media_group=?
		`, channelID, group)
		if err != nil {
			return err
		}
		type oldPageInfo struct {
			id     string
			locked bool
			hidden bool
		}
		var oldPages []oldPageInfo
		affected := map[string]bool{}
		for rows.Next() {
			var op oldPageInfo
			if err := rows.Scan(&op.id, &op.locked, &op.hidden); err == nil {
				oldPages = append(oldPages, op)
				if !op.locked {
					affected[op.id] = true
				}
			}
		}
		_ = rows.Close()

		var (
			albumPageID   sql.NullString
			albumLocked   sql.NullBool
			albumHidden   sql.NullBool
			albumOverride []byte
		)
		if err := tx.QueryRowContext(ctx, `
			SELECT a.page_id, p.locked, p.hidden, p.override_json
			FROM wiki_albums a
			LEFT JOIN wiki_pages p ON p.id=a.page_id
			WHERE a.channel_id=? AND a.media_group=?
		`, channelID, group).Scan(&albumPageID, &albumLocked, &albumHidden, &albumOverride); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}

		pageID := albumPageID.String
		hasOldProtected := false
		for _, op := range oldPages {
			if op.locked || op.hidden {
				hasOldProtected = true
				break
			}
		}
		if albumLocked.Bool || albumHidden.Bool || (pageID == "" && hasOldProtected) {
			_, err := tx.ExecContext(ctx, "UPDATE wiki_albums SET pending=FALSE WHERE channel_id=? AND media_group=?", channelID, group)
			return err
		}

		if len(facts) > 0 && pageID == "" {
			var capPageID string
			var capOverride []byte
			cErr := tx.QueryRowContext(ctx, `
				SELECT p.id, p.override_json
				FROM wiki_pages p
				JOIN wiki_facts f ON f.page_id=p.id
				JOIN wiki_chunks c ON c.id=f.chunk_id
				JOIN wiki_sources s ON s.id=c.source_id
				WHERE s.channel_id=? AND s.media_group=? AND s.source_type='message'
				  AND p.locked=FALSE AND p.hidden=FALSE
				  AND NOT EXISTS (
				      SELECT 1 FROM wiki_facts other
				      JOIN wiki_chunks oc ON oc.id=other.chunk_id
				      JOIN wiki_sources os ON os.id=oc.source_id
				      WHERE other.page_id=p.id AND NOT (os.media_group <=> ?)
				  )
				ORDER BY s.message_id, p.id LIMIT 1
			`, channelID, group, group).Scan(&capPageID, &capOverride)
			if cErr == nil && capPageID != "" {
				pageID = capPageID
				albumOverride = capOverride
			} else {
				pageID = StableID(32, channelID, "album", group)
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT IGNORE INTO wiki_pages
				(id, channel_id, entity_key, title, slug, kind, topic, updated_at)
				VALUES (?, ?, ?, ?, ?, 'Topic', ?, UTC_TIMESTAMP(6))
			`, pageID, channelID, Digest("album", group), truncateRunesSimple(title, 160), pageID, truncateRunesSimple(topic, 160)); err != nil {
				return err
			}
		}

		if _, err := tx.ExecContext(ctx, `
			DELETE f FROM wiki_sources s
			JOIN wiki_chunks c ON c.source_id=s.id
			JOIN wiki_facts f ON f.chunk_id=c.id
			JOIN wiki_pages p ON p.id=f.page_id
			WHERE s.channel_id=? AND s.media_group=? AND p.locked=FALSE
		`, channelID, group); err != nil {
			return err
		}
		if pageID != "" {
			if _, err := tx.ExecContext(ctx, "DELETE FROM wiki_facts WHERE channel_id=? AND page_id=? AND JSON_UNQUOTE(JSON_EXTRACT(fact,'$.media_group'))=?", channelID, pageID, group); err != nil {
				return err
			}
			affected[pageID] = true
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE wiki_edges e
			JOIN wiki_chunks c ON c.id=e.chunk_id
			JOIN wiki_sources s ON s.id=c.source_id
			JOIN wiki_pages p ON p.id=e.source_page
			SET e.active=FALSE
			WHERE s.channel_id=? AND s.media_group=? AND p.locked=FALSE
		`, channelID, group); err != nil {
			return err
		}

		if len(facts) > 0 {
			if mapString(decodeMapJSON(albumOverride), "title") == "" {
				if _, err := tx.ExecContext(ctx, "UPDATE wiki_pages SET title=?, topic=? WHERE id=?", truncateRunesSimple(title, 160), truncateRunesSimple(topic, 160), pageID); err != nil {
					return err
				}
			}
			byID := make(map[string]SourceRecord, len(sources))
			for _, s := range sources {
				byID[s.ID] = s
			}
			var firstAnchorChunk, firstSourceHash string
			for idx, f := range facts {
				evArr, _ := f["evidence"].([]any)
				if len(evArr) == 0 {
					continue
				}
				anchor, _ := evArr[0].(map[string]any)
				srcID := mapString(anchor, "source_id")
				chkID := mapString(anchor, "chunk_id")
				src := byID[srcID]
				if idx == 0 {
					firstAnchorChunk = chkID
					firstSourceHash = src.ContentHash
				}
				if _, err := tx.ExecContext(ctx, "INSERT INTO wiki_facts VALUES (?, ?, ?, ?, ?, ?)",
					Digest(pageID, f["key"]), channelID, pageID, chkID, src.ContentHash, EncodeJSON(f)); err != nil {
					return err
				}
			}
			if firstAnchorChunk != "" {
				for _, rel := range relations {
					targetPage, err := k.ResolveEntity(ctx, tx, channelID, rel.Target, "Entity", "其他", nil)
					if err != nil {
						return err
					}
					if targetPage == pageID {
						continue
					}
					edgeID := Digest("album", pageID, targetPage, firstAnchorChunk, rel.Kind)
					if _, err := tx.ExecContext(ctx, `
						INSERT INTO wiki_edges
						(id, channel_id, chunk_id, source_hash, source_page, target_page, kind, relation_json)
						VALUES (?, ?, ?, ?, ?, ?, ?, ?)
						ON DUPLICATE KEY UPDATE active=TRUE
					`, edgeID, channelID, firstAnchorChunk, firstSourceHash, pageID, targetPage, rel.Kind, EncodeJSON(rel)); err != nil {
						return err
					}
				}
			}
		}

		for pid := range affected {
			if _, err := tx.ExecContext(ctx, "INSERT IGNORE INTO wiki_dirty_pages VALUES (?, ?)", channelID, pid); err != nil {
				return err
			}
		}
		var pageArg any
		if pageID != "" {
			pageArg = pageID
		}
		_, err = tx.ExecContext(ctx, "UPDATE wiki_albums SET page_id=?, pending=FALSE WHERE channel_id=? AND media_group=?", pageArg, channelID, group)
		return err
	})
}

func sameSourceHashes(a, b []SourceRecord) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].ID != b[i].ID || a[i].ContentHash != b[i].ContentHash {
			return false
		}
	}
	return true
}

// MergeFacts 合并重叠分块引用、标记冲突断言为 disputed、标记过期断言为 superseded 并按发生时间排序（对齐 knowledge.py:merge_facts）。
func MergeFacts(facts []map[string]any) []map[string]any {
	type pairKey struct{ k1, k2 string }
	merged := make(map[pairKey]map[string]any)
	var orderKeys []pairKey

	for _, rawFact := range facts {
		fact := cloneMap(rawFact)
		rawK := mapString(fact, "key")
		rawT := mapString(fact, "text")
		k1, k2 := NameKey(rawK), NameKey(rawT)
		if strings.HasPrefix(rawK, "md:") {
			k1 = Digest("markdown", k2)
		}
		pk := pairKey{k1: k1, k2: k2}
		if existing, ok := merged[pk]; !ok {
			merged[pk] = fact
			orderKeys = append(orderKeys, pk)
		} else {
			mergedEv, _ := existing["evidence"].([]any)
			seen := map[string]bool{}
			for _, rawE := range mergedEv {
				if em, ok := rawE.(map[string]any); ok {
					seen[fmt.Sprintf("%s:%d:%d", mapString(em, "source_id"), mapInt(em, "start"), mapInt(em, "end"))] = true
				}
			}
			newEv, _ := fact["evidence"].([]any)
			for _, rawE := range newEv {
				if em, ok := rawE.(map[string]any); ok {
					ek := fmt.Sprintf("%s:%d:%d", mapString(em, "source_id"), mapInt(em, "start"), mapInt(em, "end"))
					if !seen[ek] {
						seen[ek] = true
						mergedEv = append(mergedEv, em)
					}
				}
			}
			existing["evidence"] = mergedEv
		}
	}

	groups := make(map[string][]map[string]any)
	for _, pk := range orderKeys {
		groups[pk.k1] = append(groups[pk.k1], merged[pk])
	}
	now := NowUTC()
	for _, grp := range groups {
		if len(grp) > 1 {
			for _, f := range grp {
				if mapString(f, "status") == "current" {
					f["status"] = "disputed"
				}
			}
		}
		for _, f := range grp {
			if vu := mapString(f, "valid_until"); vu != "" {
				if vt, err := ParseUTCTime(vu); err == nil && vt.Before(now) {
					f["status"] = "superseded"
				}
			}
		}
	}

	out := make([]map[string]any, 0, len(merged))
	for _, pk := range orderKeys {
		out = append(out, merged[pk])
	}
	sort.SliceStable(out, func(i, j int) bool {
		t1 := mapString(out[i], "occurred_at")
		if t1 == "" {
			t1 = mapString(out[i], "source_created_at")
		}
		t2 := mapString(out[j], "occurred_at")
		if t2 == "" {
			t2 = mapString(out[j], "source_created_at")
		}
		if t1 != t2 {
			return t1 < t2
		}
		k1, k2 := mapString(out[i], "key"), mapString(out[j], "key")
		if k1 != k2 {
			return k1 < k2
		}
		return mapString(out[i], "text") < mapString(out[j], "text")
	})
	return out
}

// BuildPage 聚合词条全部有效事实并生成或复用 wiki_page_revisions（对齐 knowledge.py:build_page）。
func (k *Knowledge) BuildPage(ctx context.Context, job *JobRecord, page PageRecord, minSources int) (string, error) {
	rows, err := k.store.DB.QueryContext(ctx, `
		SELECT f.fact FROM wiki_facts f
		JOIN wiki_chunks ch ON ch.id=f.chunk_id
		JOIN wiki_sources s ON s.id=ch.source_id
		WHERE f.page_id=? AND s.deleted=FALSE AND s.hidden=FALSE AND s.excluded=FALSE
		  AND s.verified=TRUE AND s.content_hash=f.source_hash
		ORDER BY s.created_at, f.id
	`, page.ID)
	if err != nil {
		return "", err
	}
	var rawFacts []map[string]any
	for rows.Next() {
		var b []byte
		if err := rows.Scan(&b); err == nil {
			rawFacts = append(rawFacts, decodeMapJSON(b))
		}
	}
	_ = rows.Close()

	facts := MergeFacts(rawFacts)
	if len(facts) == 0 {
		return "", nil
	}

	if minSources > 1 && !page.Locked {
		srcSet := map[string]bool{}
		for _, f := range facts {
			evArr, _ := f["evidence"].([]any)
			for _, re := range evArr {
				if em, ok := re.(map[string]any); ok {
					if sid := mapString(em, "source_id"); sid != "" {
						srcSet[sid] = true
					}
				}
			}
		}
		if len(srcSet) < minSources {
			return "", nil
		}
	}

	var oldID, oldHash string
	var oldRejected bool
	hasOld := false
	err = k.store.DB.QueryRowContext(ctx, `
		SELECT r.id, r.content_hash, r.rejected
		FROM wiki_page_revisions r
		JOIN wiki_channels c ON c.id=r.channel_id
		LEFT JOIN wiki_publication_pages pp
		  ON pp.publication_id=c.published_revision AND pp.page_id=r.page_id AND pp.revision_id=r.id
		WHERE r.page_id=? AND (
			NOT COALESCE(JSON_EXTRACT(r.document,'$.standalone_edit')=TRUE, FALSE)
			OR pp.revision_id IS NOT NULL
		)
		ORDER BY (? AND pp.revision_id IS NOT NULL
			AND COALESCE(JSON_EXTRACT(r.document,'$.standalone_edit')=TRUE, FALSE)) DESC,
			r.created_at DESC, r.id DESC
		LIMIT 1
	`, page.ID, page.Locked).Scan(&oldID, &oldHash, &oldRejected)
	if err == nil {
		hasOld = true
	}

	if page.Locked {
		if !hasOld {
			return "", nil
		}
		var validID string
		vErr := k.store.DB.QueryRowContext(ctx, "SELECT r.id FROM wiki_page_revisions r JOIN wiki_pages p ON p.id=r.page_id WHERE r.id=? AND "+RevisionEvidenceSQL("r", "p"), oldID).Scan(&validID)
		if vErr == nil && validID != "" {
			return oldID, nil
		}
		return "", nil
	}

	title := page.Title
	if ovTitle := mapString(page.OverrideJSON, "title"); ovTitle != "" {
		title = ovTitle
	}
	document := map[string]any{
		"title":       title,
		"kind":        page.Kind,
		"topic":       page.Topic,
		"claims":      facts,
		"verified_at": FormatISO(NowUTC()),
	}
	contentHash := documentContentHash(document)
	if hasOld && oldHash == contentHash && !oldRejected {
		return oldID, nil
	}

	revisionID := NewID()
	searchLines := []string{title, page.Topic}
	for _, f := range facts {
		searchLines = append(searchLines, mapString(f, "text"))
	}
	searchText := strings.Join(searchLines, "\n")

	err = k.store.WithTx(ctx, func(tx *sql.Tx) error {
		var cid string
		if err := tx.QueryRowContext(ctx, "SELECT id FROM wiki_channels WHERE id=? FOR UPDATE", job.ChannelID).Scan(&cid); err != nil {
			return err
		}
		if err := k.store.FenceJob(ctx, tx, job); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO wiki_page_revisions
			(id, channel_id, page_id, job_id, document, content_hash, search_text, approved, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, FALSE, UTC_TIMESTAMP(6))
		`, revisionID, job.ChannelID, page.ID, job.ID, EncodeJSON(document), contentHash, searchText); err != nil {
			return err
		}

		for ordinal, f := range facts {
			var occArg any
			if occStr := mapString(f, "occurred_at"); occStr != "" {
				if t, pErr := ParseUTCTime(occStr); pErr == nil {
					occArg = db.MysqlDatetime6(t)
				}
			}
			srcCreated := NowUTC()
			if scStr := mapString(f, "source_created_at"); scStr != "" {
				if t, pErr := ParseUTCTime(scStr); pErr == nil {
					srcCreated = t
				}
			}
			if _, err := tx.ExecContext(ctx, "INSERT INTO wiki_revision_claims VALUES (?, ?, ?, ?, ?)",
				revisionID, ordinal, occArg, db.MysqlDatetime6(srcCreated), EncodeJSON(f)); err != nil {
				return err
			}
			evArr, _ := f["evidence"].([]any)
			for _, rawEv := range evArr {
				em, ok := rawEv.(map[string]any)
				if !ok {
					continue
				}
				if _, err := tx.ExecContext(ctx, `
					INSERT IGNORE INTO wiki_evidence
					SELECT ?, s.id, ch.content_hash, ch.id, ?, ?
					FROM wiki_chunks ch
					JOIN wiki_sources s ON s.id=ch.source_id
					WHERE ch.id=? AND s.channel_id=?
				`, revisionID, mapInt(em, "start"), mapInt(em, "end"), mapString(em, "chunk_id"), job.ChannelID); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return revisionID, nil
}

// Profile 实时统计频道可见消息规模、UTC 小时分布、月度节奏与最近 24 个月主题趋势（对齐 knowledge.py:profile）。
func (k *Knowledge) Profile(ctx context.Context, exec sqlExecutor, channelID, publicationID string) (map[string]any, error) {
	if exec == nil {
		exec = k.store.exec(ctx)
	}
	scope := "channel_id=? AND source_type='message' AND deleted=FALSE AND hidden=FALSE AND excluded=FALSE AND verified=TRUE"
	var (
		msgCount         int64
		avgLen           float64
		earliest, latest any
	)
	if err := exec.QueryRowContext(ctx, `
		SELECT COUNT(*), COALESCE(AVG(original_length), 0), MIN(created_at), MAX(created_at)
		FROM wiki_sources WHERE `+scope, channelID).Scan(&msgCount, &avgLen, &earliest, &latest); err != nil {
		return nil, err
	}

	hours, err := QueryRowsMap(ctx, exec, "SELECT HOUR(created_at) AS hour, COUNT(*) AS count FROM wiki_sources WHERE "+scope+" GROUP BY HOUR(created_at) ORDER BY hour", channelID)
	if err != nil {
		return nil, err
	}
	months, err := QueryRowsMap(ctx, exec, "SELECT DATE_FORMAT(created_at, '%Y-%m') AS month, COUNT(*) AS count FROM wiki_sources WHERE "+scope+" GROUP BY month ORDER BY month", channelID)
	if err != nil {
		return nil, err
	}

	if publicationID == "" {
		var pub sql.NullString
		_ = exec.QueryRowContext(ctx, "SELECT published_revision FROM wiki_channels WHERE id=?", channelID).Scan(&pub)
		publicationID = pub.String
	}

	topicScope := fmt.Sprintf(`FROM wiki_publication_pages pp
		JOIN wiki_pages p ON p.id=pp.page_id
		JOIN wiki_page_revisions r ON r.id=pp.revision_id
		JOIN wiki_evidence ev ON ev.revision_id=r.id
		JOIN wiki_sources s ON s.id=ev.source_id
		JOIN wiki_channels c ON c.id=p.channel_id
		WHERE pp.publication_id=? AND p.channel_id=? AND s.source_type='message' AND %s
		  AND NOT JSON_CONTAINS(JSON_EXTRACT(c.config,'$.hidden_topics'), JSON_QUOTE(p.topic))`,
		RevisionVisibleSQL("r", "p"))

	topics, err := QueryRowsMap(ctx, exec, "SELECT p.topic, COUNT(DISTINCT s.id) AS message_count "+topicScope+" GROUP BY p.topic ORDER BY message_count DESC LIMIT 100", publicationID, channelID)
	if err != nil {
		return nil, err
	}
	trends, err := QueryRowsMap(ctx, exec, `SELECT p.topic, DATE_FORMAT(s.created_at, '%Y-%m') AS month, COUNT(DISTINCT s.id) AS message_count `+topicScope+`
		AND s.created_at >= DATE_SUB(UTC_TIMESTAMP(6), INTERVAL 24 MONTH)
		GROUP BY p.topic, month ORDER BY month, p.topic`, publicationID, channelID)
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"statistics": map[string]any{
			"message_count":      msgCount,
			"avg_message_length": avgLen,
			"earliest":           formatNullableTime(earliest),
			"latest":             formatNullableTime(latest),
		},
		"hours_utc":    hours,
		"months":       months,
		"topics":       topics,
		"topic_trends": trends,
		"method":       "Actual verified messages; topics may overlap. No user profiling.",
		"as_of":        FormatISO(NowUTC()),
	}, nil
}

// RevisionSeed 返回增量装配/发布的基线页面与修订映射（对齐 knowledge.py:revision_seed）。
func (k *Knowledge) RevisionSeed(ctx context.Context, exec sqlExecutor, ch *ChannelRecord) ([]RevisionSeedItem, error) {
	if exec == nil {
		exec = k.store.exec(ctx)
	}
	var (
		rows *sql.Rows
		err  error
	)
	if ch.PublishedRevision != "" {
		rows, err = exec.QueryContext(ctx, `
			SELECT pp.page_id, pp.revision_id, p.topic
			FROM wiki_publication_pages pp
			JOIN wiki_pages p ON p.id=pp.page_id
			WHERE pp.publication_id=? AND p.hidden=FALSE
		`, ch.PublishedRevision)
	} else {
		rows, err = exec.QueryContext(ctx, fmt.Sprintf(`
			SELECT page_id, revision_id, topic FROM (
				SELECT p.id AS page_id, r.id AS revision_id, p.topic,
				       ROW_NUMBER() OVER (PARTITION BY p.id ORDER BY r.created_at DESC, r.id DESC) AS position_
				FROM wiki_pages p
				JOIN wiki_page_revisions r ON r.page_id=p.id
				WHERE p.channel_id=? AND p.hidden=FALSE AND %s
			) ranked WHERE position_=1
		`, RevisionVisibleSQL("r", "p")), ch.ID)
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []RevisionSeedItem
	for rows.Next() {
		var it RevisionSeedItem
		if err := rows.Scan(&it.PageID, &it.RevisionID, &it.Topic); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// ExpandRevisions 将增量任务修订清单与基线快照及剔除列表合并为完整发布清单（对齐 knowledge.py:expand_revisions）。
func (k *Knowledge) ExpandRevisions(ctx context.Context, exec sqlExecutor, ch *ChannelRecord, manifest map[string]any) (map[string]string, error) {
	hidden := map[string]bool{}
	for _, t := range ch.Config.HiddenTopics {
		hidden[t] = true
	}
	dropped := map[string]bool{}
	for _, pid := range mapStringSlice(manifest["dropped_pages"]) {
		dropped[pid] = true
	}
	seed, err := k.RevisionSeed(ctx, exec, ch)
	if err != nil {
		return nil, err
	}
	merged := make(map[string]string, len(seed))
	for _, row := range seed {
		if !dropped[row.PageID] && !hidden[row.Topic] {
			merged[row.PageID] = row.RevisionID
		}
	}
	switch revs := manifest["revisions"].(type) {
	case map[string]string:
		for k, v := range revs {
			merged[k] = v
		}
	case map[string]any:
		for k, v := range revs {
			if s, ok := v.(string); ok {
				merged[k] = s
			}
		}
	}
	return merged, nil
}

// ReviewSubject 验证修订证据完好性并计算用于独立审校去重的 subject_hash（对齐 knowledge.py:review_subject）。
func (k *Knowledge) ReviewSubject(ctx context.Context, exec sqlExecutor, channelID, pageID, revisionID string) (map[string]any, string, []EvidenceRow, error) {
	if exec == nil {
		exec = k.store.exec(ctx)
	}
	var docRaw []byte
	err := exec.QueryRowContext(ctx, fmt.Sprintf(`
		SELECT r.document FROM wiki_page_revisions r
		JOIN wiki_pages p ON p.id=r.page_id
		WHERE r.channel_id=? AND p.id=? AND r.id=? AND %s
	`, RevisionEvidenceSQL("r", "p")), channelID, pageID, revisionID).Scan(&docRaw)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, "", nil, errPublicationEvidence
		}
		return nil, "", nil, err
	}
	doc := decodeMapJSON(docRaw)
	evList, err := queryEvidenceRowsTx(ctx, exec, revisionID)
	if err != nil {
		return nil, "", nil, err
	}
	return doc, Digest(doc, evList), evList, nil
}

func queryEvidenceRowsTx(ctx context.Context, exec sqlExecutor, revisionID string) ([]EvidenceRow, error) {
	rows, err := exec.QueryContext(ctx, `
		SELECT ev.source_id, ev.source_hash, ev.chunk_id, ev.start_offset, ev.end_offset
		FROM wiki_evidence ev
		WHERE ev.revision_id=?
		ORDER BY ev.chunk_id, ev.start_offset, ev.end_offset
	`, revisionID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []EvidenceRow
	for rows.Next() {
		var r EvidenceRow
		if err := rows.Scan(&r.SourceID, &r.SourceHash, &r.ChunkID, &r.StartOffset, &r.EndOffset); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// PublicationFingerprint 计算发布快照内容指纹（不含时间戳，用于跳过重复快照，对齐 publication_fingerprint）。
func PublicationFingerprint(candidates []RevisionSeedItem, profile map[string]any) string {
	pairs := make([][]string, len(candidates))
	for i, c := range candidates {
		pairs[i] = []string{c.PageID, c.RevisionID}
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i][0] != pairs[j][0] {
			return pairs[i][0] < pairs[j][0]
		}
		return pairs[i][1] < pairs[j][1]
	})
	profFiltered := make(map[string]any, len(profile))
	for k, v := range profile {
		if k != "as_of" {
			profFiltered[k] = v
		}
	}
	return Digest(pairs, profFiltered)
}

// PublishedPage 查询指定发布快照中某词条对应的修订 ID。
func (k *Knowledge) PublishedPage(ctx context.Context, exec sqlExecutor, publicationID, pageID string) (string, error) {
	if publicationID == "" || pageID == "" {
		return "", nil
	}
	if exec == nil {
		exec = k.store.exec(ctx)
	}
	var revID string
	err := exec.QueryRowContext(ctx, "SELECT revision_id FROM wiki_publication_pages WHERE publication_id=? AND page_id=?", publicationID, pageID).Scan(&revID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return revID, err
}

// ApproveRevision 核验候选修订已通过当前审校策略（或人工放行）且证据有效，随后将其标为 approved=TRUE。
func (k *Knowledge) ApproveRevision(ctx context.Context, tx *sql.Tx, channelID, pageID, revisionID, policy string) error {
	if policy == SkippedAIReview {
		var id string
		err := tx.QueryRowContext(ctx, fmt.Sprintf(`
			SELECT r.id FROM wiki_page_revisions r JOIN wiki_pages p ON p.id=r.page_id
			WHERE r.id=? AND r.channel_id=? AND p.id=? AND %s
		`, RevisionEvidenceSQL("r", "p")), revisionID, channelID, pageID).Scan(&id)
		if err != nil {
			return errPublicationEvidence
		}
	} else {
		_, subjectHash, _, err := k.ReviewSubject(ctx, tx, channelID, pageID, revisionID)
		if err != nil {
			return err
		}
		var one int
		err = tx.QueryRowContext(ctx, `
			SELECT 1 FROM wiki_revision_reviews
			WHERE revision_id=? AND policy_key IN (?, ?) AND subject_hash=? AND approved=TRUE
			LIMIT 1
		`, revisionID, policy, ManualReviewPolicy, subjectHash).Scan(&one)
		if err != nil {
			return errPublicationReview
		}
	}
	_, err := tx.ExecContext(ctx, "UPDATE wiki_page_revisions SET approved=TRUE WHERE id=? AND approved=FALSE", revisionID)
	return err
}

// ManuallyApproveRevision 写入管理员人工审校通过凭证（相同 subject_hash 重建时自动继承）。
func (k *Knowledge) ManuallyApproveRevision(ctx context.Context, tx *sql.Tx, channelID, pageID, revisionID, actor string) error {
	_, subjectHash, _, err := k.ReviewSubject(ctx, tx, channelID, pageID, revisionID)
	if err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `
		INSERT INTO wiki_revision_reviews
		(revision_id, policy_key, subject_hash, approved, result, created_at)
		VALUES (?, ?, ?, TRUE, ?, UTC_TIMESTAMP(6))
		ON DUPLICATE KEY UPDATE result=wiki_revision_reviews.result
	`, revisionID, ManualReviewPolicy, subjectHash, EncodeJSON(map[string]any{"manual": true}))
	if err != nil {
		return err
	}
	if affected, _ := res.RowsAffected(); affected > 0 {
		return k.store.Audit(ctx, tx, channelID, "manually_approve_revision", revisionID, actor, map[string]any{"page_id": pageID})
	}
	return nil
}

func validatePublicationAccess(ch *ChannelRecord) error {
	if ch == nil || !ch.Enabled {
		return fmt.Errorf("%w: 频道 Wiki 已停用，无法发布", ErrPublicationAccessUnverified)
	}
	if !ch.AccessVerified || ch.VerifiedUntil == nil || !ch.VerifiedUntil.After(NowUTC()) {
		return ErrPublicationAccessUnverified
	}
	return nil
}

// Publish 原子生成频道发布快照（或回滚至指定历史快照），关联有效边、记录社区贡献并更新 published_revision（对齐 knowledge.py:publish）。
func (k *Knowledge) Publish(ctx context.Context, channelID, jobID, actor string, opts ...PublishOption) (string, error) {
	var pcfg publishConfig
	for _, opt := range opts {
		opt(&pcfg)
	}
	publicationID := NewID()

	err := k.store.WithTx(ctx, func(tx *sql.Tx) error {
		row := tx.QueryRowContext(ctx, `
			SELECT c.id, c.title, c.username, c.enabled, c.public, c.access_verified, c.verified_until,
			       c.config, c.coverage, c.published_revision, c.source_generation, c.active_job,
			       c.dirty_at, c.scheduled_at, c.reconciled_at, c.updated_at, cover.digest
			FROM wiki_channels c
			LEFT JOIN wiki_channel_covers cover ON cover.channel_id=c.id
			WHERE c.id=? FOR UPDATE
		`, channelID)
		ch, err := scanChannelRow(row)
		if err != nil {
			return err
		}
		if err := validatePublicationAccess(ch); err != nil {
			return err
		}
		if pcfg.runningJob != nil {
			if err := k.store.FenceJob(ctx, tx, pcfg.runningJob); err != nil {
				return err
			}
		}

		if jobID != "" {
			var existingPub string
			qErr := tx.QueryRowContext(ctx, "SELECT id FROM wiki_publications WHERE channel_id=? AND job_id=? ORDER BY created_at DESC LIMIT 1", channelID, jobID).Scan(&existingPub)
			if qErr == nil && existingPub != "" {
				if existingPub != ch.PublishedRevision && pcfg.runningJob == nil {
					return errors.New("This job was already published; use explicit rollback for an older snapshot")
				}
				publicationID = existingPub
				return nil
			}
		}

		var (
			contentHash string
			candidates  []RevisionSeedItem
			manifest    = map[string]any{}
			progress    = map[string]any{}
			policy      string
		)

		if pcfg.rollbackID != "" {
			if err := tx.QueryRowContext(ctx, "SELECT content_hash FROM wiki_publications WHERE id=? AND channel_id=?", pcfg.rollbackID, channelID).Scan(&contentHash); err != nil {
				return errors.New("Unknown publication")
			}
			pRows, err := tx.QueryContext(ctx, "SELECT page_id, revision_id FROM wiki_publication_pages WHERE publication_id=?", pcfg.rollbackID)
			if err != nil {
				return err
			}
			for pRows.Next() {
				var it RevisionSeedItem
				if err := pRows.Scan(&it.PageID, &it.RevisionID); err == nil {
					candidates = append(candidates, it)
				}
			}
			_ = pRows.Close()
		} else {
			job, err := getJobTx(ctx, tx, jobID)
			if err != nil || job == nil || job.ChannelID != channelID {
				return errors.New("Job has not completed validation, or sources changed; update before publishing")
			}
			progress = job.Progress
			manifest = job.Cursor
			if (job.Status != "completed" && job.Status != "running") ||
				job.Phase != "ready" ||
				(job.Status == "running" && pcfg.runningJob == nil) ||
				!mapBool(progress, "generation_complete") ||
				!mapBool(progress, "review_complete") {
				return errors.New("Job has not completed validation, or sources changed; update before publishing")
			}
			policy = mapString(manifest, "review_policy")
			if policy == "" {
				return errors.New("Job predates independent review; rebuild before publishing")
			}
			if policy == SkippedAIReview && ch.Config.AIReviewEnabled {
				return errPublicationReviewReenabled
			}
			if job.Kind == "page_edit" {
				pub, err := k.publishPageEditTx(ctx, tx, ch, job, manifest, progress, publicationID, actor)
				publicationID = pub
				return err
			}
			if job.Kind == "albums" && ch.PublishedRevision != "" {
				var profRaw []byte
				if err := tx.QueryRowContext(ctx, "SELECT profile FROM wiki_publications WHERE id=?", ch.PublishedRevision).Scan(&profRaw); err == nil {
					prevProf := decodeMapJSON(profRaw)
					manifest["profile_narrative"] = prevProf["narrative"]
					manifest["profile_generation"] = prevProf["source_generation"]
				}
			}
			var revMap map[string]string
			switch job.Kind {
			case "update", "reconcile", "proposal", "albums":
				revMap, err = k.ExpandRevisions(ctx, tx, ch, manifest)
				if err != nil {
					return err
				}
			default:
				revMap = extractRevisionMap(manifest)
			}

			if ch.PublishedRevision != "" {
				eRows, err := tx.QueryContext(ctx, fmt.Sprintf(`
					SELECT p.id, r.id FROM wiki_publication_pages pp
					JOIN wiki_page_revisions r ON r.id=pp.revision_id
					JOIN wiki_pages p ON p.id=pp.page_id
					JOIN wiki_channels c ON c.id=p.channel_id
					WHERE pp.publication_id=? AND p.locked=TRUE AND %s
					  AND JSON_EXTRACT(r.document, '$.standalone_edit') = TRUE
					  AND NOT JSON_CONTAINS(JSON_EXTRACT(c.config,'$.hidden_topics'), JSON_QUOTE(p.topic))
				`, RevisionVisibleSQL("r", "p")), ch.PublishedRevision)
				if err != nil {
					return err
				}
				for eRows.Next() {
					var pid, rid string
					if err := eRows.Scan(&pid, &rid); err == nil {
						revMap[pid] = rid
						if mRevs, ok := manifest["revisions"].(map[string]any); ok {
							delete(mRevs, pid)
						}
					}
				}
				_ = eRows.Close()
			}
			for pid, rid := range revMap {
				candidates = append(candidates, RevisionSeedItem{PageID: pid, RevisionID: rid})
			}
		}

		if len(candidates) == 0 {
			return errors.New("No reviewed knowledge to publish")
		}

		baseline := map[string]bool{}
		if pcfg.rollbackID == "" && ch.PublishedRevision != "" {
			bRows, err := tx.QueryContext(ctx, "SELECT revision_id FROM wiki_publication_pages WHERE publication_id=?", ch.PublishedRevision)
			if err != nil {
				return err
			}
			for bRows.Next() {
				var rid string
				if err := bRows.Scan(&rid); err == nil {
					baseline[rid] = true
				}
			}
			_ = bRows.Close()
		}

		stagedRevs := map[string]bool{}
		if mRevs, ok := manifest["revisions"].(map[string]any); ok {
			for pid := range mRevs {
				stagedRevs[pid] = true
			}
		}
		for _, cand := range candidates {
			if pcfg.rollbackID == "" && (!baseline[cand.RevisionID] || stagedRevs[cand.PageID]) {
				if err := k.ApproveRevision(ctx, tx, channelID, cand.PageID, cand.RevisionID, policy); err != nil {
					return err
				}
				continue
			}
			var okID string
			err := tx.QueryRowContext(ctx, fmt.Sprintf(`
				SELECT r.id FROM wiki_page_revisions r JOIN wiki_pages p ON p.id=r.page_id
				WHERE r.id=? AND r.channel_id=? AND p.id=? AND %s
			`, RevisionVisibleSQL("r", "p")), cand.RevisionID, channelID, cand.PageID).Scan(&okID)
			if err != nil {
				return errPublicationEvidence
			}
		}

		reusedID := ""
		if pcfg.rollbackID == "" && ch.PublishedRevision != "" {
			prof, err := k.Profile(ctx, tx, channelID, ch.PublishedRevision)
			if err != nil {
				return err
			}
			prof["narrative"] = manifest["profile_narrative"]
			prof["source_generation"] = manifest["profile_generation"]
			fp := PublicationFingerprint(candidates, prof)
			var prevHash string
			if err := tx.QueryRowContext(ctx, "SELECT content_hash FROM wiki_publications WHERE id=? AND channel_id=?", ch.PublishedRevision, channelID).Scan(&prevHash); err == nil && prevHash == fp {
				reusedID = ch.PublishedRevision
			}
		}

		if reusedID != "" {
			publicationID = reusedID
		} else {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO wiki_publications
				(id, channel_id, job_id, profile, coverage, created_at, content_hash)
				VALUES (?, ?, ?, ?, ?, UTC_TIMESTAMP(6), ?)
			`, publicationID, channelID, nilIfEmpty(jobID), EncodeJSON(map[string]any{}), EncodeJSON(ch.Coverage), contentHash); err != nil {
				return err
			}
			for _, cand := range candidates {
				if _, err := tx.ExecContext(ctx, "INSERT INTO wiki_publication_pages VALUES (?, ?, ?)", publicationID, cand.PageID, cand.RevisionID); err != nil {
					return err
				}
			}
			prof, err := k.Profile(ctx, tx, channelID, publicationID)
			if err != nil {
				return err
			}
			if pcfg.rollbackID == "" {
				if narr, ok := manifest["profile_narrative"]; ok && narr != nil {
					prof["narrative"] = narr
				} else {
					prof["narrative"] = map[string]any{}
				}
				prof["source_generation"] = manifest["profile_generation"]
			}
			finalHash := contentHash
			if finalHash == "" {
				finalHash = PublicationFingerprint(candidates, prof)
			}
			if _, err := tx.ExecContext(ctx, "UPDATE wiki_publications SET profile=?, content_hash=? WHERE id=?", EncodeJSON(prof), finalHash, publicationID); err != nil {
				return err
			}

			if pcfg.rollbackID != "" {
				if _, err := tx.ExecContext(ctx, "INSERT INTO wiki_publication_edges SELECT ?, edge_id FROM wiki_publication_edges WHERE publication_id=?", publicationID, pcfg.rollbackID); err != nil {
					return err
				}
			} else {
				if _, err := tx.ExecContext(ctx, `
					INSERT INTO wiki_publication_edges
					SELECT ?, e.id FROM wiki_edges e
					JOIN wiki_chunks ch2 ON ch2.id=e.chunk_id
					JOIN wiki_sources s ON s.id=ch2.source_id
					JOIN wiki_publication_pages a ON a.page_id=e.source_page AND a.publication_id=?
					JOIN wiki_publication_pages b ON b.page_id=e.target_page AND b.publication_id=?
					WHERE e.channel_id=? AND e.active=TRUE AND s.content_hash=e.source_hash
					  AND s.deleted=FALSE AND s.hidden=FALSE AND s.excluded=FALSE AND s.verified=TRUE
					  AND EXISTS (
					      SELECT 1 FROM wiki_evidence ev
					      WHERE ev.revision_id=a.revision_id AND ev.chunk_id=e.chunk_id AND ev.source_hash=e.source_hash
					  )
				`, publicationID, publicationID, publicationID, channelID); err != nil {
					return err
				}
			}
		}

		if _, err := tx.ExecContext(ctx, "UPDATE wiki_channels SET published_revision=?, updated_at=UTC_TIMESTAMP(6) WHERE id=?", publicationID, channelID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT IGNORE INTO wiki_contributions
			SELECT e.id, e.revision_id, ?, UTC_TIMESTAMP(6)
			FROM wiki_edit_proposals e
			JOIN wiki_publication_pages pp ON pp.revision_id=e.revision_id AND pp.publication_id=?
			WHERE e.channel_id=? AND e.status='approved'
		`, publicationID, publicationID, channelID); err != nil {
			return err
		}

		if pcfg.rollbackID == "" && ch.SourceGeneration == mapInt64(progress, "source_generation") && (ch.ActiveJob == "" || ch.ActiveJob == jobID) {
			_, _ = tx.ExecContext(ctx, "DELETE FROM wiki_dirty_pages WHERE channel_id=?", channelID)
		}
		if jobID != "" && pcfg.runningJob == nil {
			progress["published"] = true
			progress["publication_id"] = publicationID
			if _, err := tx.ExecContext(ctx, "UPDATE wiki_jobs SET status='published', progress=?, updated_at=UTC_TIMESTAMP(6) WHERE id=?", EncodeJSON(progress), jobID); err != nil {
				return err
			}
			_, _ = tx.ExecContext(ctx, "UPDATE wiki_channels SET active_job=NULL WHERE id=? AND active_job=?", channelID, jobID)
		}

		action := "publish"
		if pcfg.rollbackID != "" {
			action = "rollback"
		}
		return k.store.Audit(ctx, tx, channelID, action, publicationID, actor, map[string]any{
			"previous":     nilIfEmpty(ch.PublishedRevision),
			"rollback":     nilIfEmpty(pcfg.rollbackID),
			"deduplicated": reusedID != "",
		})
	})
	if err != nil {
		return "", err
	}

	// 发布成功后异步/尽力清理超额历史快照
	if ch, cErr := k.store.GetChannel(ctx, channelID); cErr == nil && ch != nil {
		_, _ = k.Prune(ctx, channelID, ch.Config.RetentionPublications)
	}
	return publicationID, nil
}

func (k *Knowledge) publishPageEditTx(ctx context.Context, tx *sql.Tx, ch *ChannelRecord, job *JobRecord, manifest, progress map[string]any, publicationID, actor string) (string, error) {
	channelID := ch.ID
	previous := ch.PublishedRevision
	pageID := mapString(manifest, "page_id")
	revs := extractRevisionMap(manifest)
	revisionID := revs[pageID]
	baseRev := mapString(manifest, "base_revision")

	curPubRev, err := k.PublishedPage(ctx, tx, previous, pageID)
	if err != nil {
		return "", err
	}
	if curPubRev != baseRev {
		return "", ErrPageEditConflict
	}

	var topic string
	if err := tx.QueryRowContext(ctx, "SELECT topic FROM wiki_pages WHERE id=?", pageID).Scan(&topic); err != nil {
		return "", ErrPageEditConflict
	}
	if slices.Contains(ch.Config.HiddenTopics, topic) {
		return "", errors.New("该词条已隐藏，无法发布修订")
	}

	if err := k.ApproveRevision(ctx, tx, channelID, pageID, revisionID, mapString(manifest, "review_policy")); err != nil {
		return "", err
	}

	var profRaw, covRaw []byte
	_ = tx.QueryRowContext(ctx, "SELECT profile, coverage FROM wiki_publications WHERE id=?", previous).Scan(&profRaw, &covRaw)
	profile := decodeMapJSON(profRaw)
	covJSON := EncodeJSON(ch.Coverage)
	if len(covRaw) > 0 {
		covJSON = string(covRaw)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO wiki_publications (id, channel_id, job_id, profile, coverage, created_at)
		VALUES (?, ?, ?, ?, ?, UTC_TIMESTAMP(6))
	`, publicationID, channelID, job.ID, EncodeJSON(profile), covJSON); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO wiki_publication_pages
		SELECT ?, page_id, revision_id FROM wiki_publication_pages
		WHERE publication_id=? AND page_id<>?
	`, publicationID, previous, pageID); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO wiki_publication_pages VALUES (?, ?, ?)", publicationID, pageID, revisionID); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO wiki_publication_edges
		SELECT ?, pe.edge_id FROM wiki_publication_edges pe
		JOIN wiki_edges e ON e.id=pe.edge_id
		WHERE pe.publication_id=? AND (e.source_page<>? OR EXISTS (
			SELECT 1 FROM wiki_evidence ev
			WHERE ev.revision_id=? AND ev.chunk_id=e.chunk_id AND ev.source_hash=e.source_hash
		))
	`, publicationID, previous, pageID, revisionID); err != nil {
		return "", err
	}

	pRows, err := tx.QueryContext(ctx, "SELECT page_id, revision_id FROM wiki_publication_pages WHERE publication_id=?", publicationID)
	if err != nil {
		return "", err
	}
	var cands []RevisionSeedItem
	for pRows.Next() {
		var it RevisionSeedItem
		if err := pRows.Scan(&it.PageID, &it.RevisionID); err == nil {
			cands = append(cands, it)
		}
	}
	_ = pRows.Close()
	fp := PublicationFingerprint(cands, profile)
	if _, err := tx.ExecContext(ctx, "UPDATE wiki_publications SET content_hash=? WHERE id=?", fp, publicationID); err != nil {
		return "", err
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE wiki_pages p
		JOIN wiki_page_revisions r ON r.id=?
		LEFT JOIN wiki_page_revisions base ON base.id=?
		SET p.locked=TRUE,
		    p.title=CASE
		        WHEN CAST(r.document->>'$.title' AS BINARY) <> CAST(base.document->>'$.title' AS BINARY)
		        THEN r.document->>'$.title'
		        ELSE p.title END
		WHERE p.id=?
	`, revisionID, nilIfEmpty(baseRev), pageID); err != nil {
		return "", err
	}

	if _, err := tx.ExecContext(ctx, "UPDATE wiki_channels SET published_revision=?, updated_at=UTC_TIMESTAMP(6) WHERE id=?", publicationID, channelID); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT IGNORE INTO wiki_contributions
		SELECT id, revision_id, ?, UTC_TIMESTAMP(6)
		FROM wiki_edit_proposals
		WHERE revision_id=? AND channel_id=? AND status='approved'
	`, publicationID, revisionID, channelID); err != nil {
		return "", err
	}
	if job.Status == "completed" {
		progress["published"] = true
		progress["publication_id"] = publicationID
		progress["activity"] = "词条修订已发布"
		if _, err := tx.ExecContext(ctx, "UPDATE wiki_jobs SET status='published', progress=?, updated_at=UTC_TIMESTAMP(6) WHERE id=?", EncodeJSON(progress), job.ID); err != nil {
			return "", err
		}
	}
	if err := k.store.Audit(ctx, tx, channelID, "publish", publicationID, actor, map[string]any{"previous": nilIfEmpty(previous), "page_id": pageID}); err != nil {
		return "", err
	}
	return publicationID, nil
}

// CreateCorrection 在事务内基于既有有效修订构造一条携带相同原文证据的新修订（对齐 knowledge.py:create_correction）。
func (k *Knowledge) CreateCorrection(ctx context.Context, tx *sql.Tx, channelID, revisionID string, ordinal *int, text, actor string, opts CorrectionOptions) (string, error) {
	newID := NewID()
	var pageID string
	var docRaw []byte
	err := tx.QueryRowContext(ctx, fmt.Sprintf(`
		SELECT r.page_id, r.document
		FROM wiki_page_revisions r
		JOIN wiki_pages p ON p.id=r.page_id
		WHERE r.id=? AND r.channel_id=? AND %s
	`, RevisionEvidenceSQL("r", "p")), revisionID, channelID).Scan(&pageID, &docRaw)
	if err != nil {
		return "", errors.New("Correction requires current valid evidence")
	}

	doc := decodeMapJSON(docRaw)
	if opts.Standalone {
		doc["standalone_edit"] = true
	}
	if opts.ProposalID != "" {
		contribs := mapStringSlice(doc["community_contributions"])
		contribs = append(contribs, opts.ProposalID)
		doc["community_contributions"] = contribs
	}

	claimsRaw, _ := doc["claims"].([]any)
	var targetObj map[string]any
	var notesKey string
	if ordinal == nil {
		targetObj = doc
		notesKey = "title_notes"
		if opts.ReplaceText {
			if err := ValidateTitleCorrection(text); err != nil {
				return "", err
			}
			doc["title"] = strings.TrimSpace(text)
		}
	} else {
		if *ordinal < 0 || *ordinal >= len(claimsRaw) {
			return "", errors.New("Unknown claim")
		}
		claimMap, ok := claimsRaw[*ordinal].(map[string]any)
		if !ok {
			return "", errors.New("Unknown claim")
		}
		targetObj = claimMap
		notesKey = "community_notes"
		if opts.ReplaceText {
			targetObj["text"] = text
		}
	}

	if opts.Annotation != nil {
		var notes []any
		if existing, ok := targetObj[notesKey].([]any); ok {
			notes = existing
		}
		if opts.AnnotationIndex == nil {
			notes = append(notes, opts.Annotation)
			targetObj[notesKey] = notes
			annKind := mapString(opts.Annotation, "kind")
			if ordinal != nil && (annKind == "dispute" || annKind == "outdated") {
				if annKind == "dispute" {
					targetObj["status"] = "disputed"
				} else {
					targetObj["status"] = "superseded"
				}
			}
		} else {
			idx := *opts.AnnotationIndex
			if idx < 0 || idx >= len(notes) {
				return "", errors.New("找不到所选注解，请刷新词条后重试")
			}
			origNote, ok := notes[idx].(map[string]any)
			if !ok || mapString(origNote, "kind") != "annotation" {
				return "", errors.New("找不到所选注解，请刷新词条后重试")
			}
			newAnnText := mapString(opts.Annotation, "text")
			if newAnnText != "" {
				updated := cloneMap(origNote)
				updated["text"] = newAnnText
				updated["sources"] = opts.Annotation["sources"]
				notes[idx] = updated
				targetObj[notesKey] = notes
			} else {
				notes = append(notes[:idx], notes[idx+1:]...)
				if len(notes) == 0 {
					delete(targetObj, notesKey)
				} else {
					targetObj[notesKey] = notes
				}
			}
		}
	}

	doc["verified_at"] = FormatISO(NowUTC())
	searchLines := []string{mapString(doc, "title"), mapString(doc, "topic")}
	for _, rc := range claimsRaw {
		if cm, ok := rc.(map[string]any); ok {
			searchLines = append(searchLines, mapString(cm, "text"))
		}
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO wiki_page_revisions
		(id, channel_id, page_id, document, content_hash, search_text, approved, created_at)
		VALUES (?, ?, ?, ?, ?, ?, FALSE, UTC_TIMESTAMP(6))
	`, newID, channelID, pageID, EncodeJSON(doc), Digest(doc), strings.Join(searchLines, "\n")); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO wiki_evidence
		SELECT ?, source_id, source_hash, chunk_id, start_offset, end_offset
		FROM wiki_evidence WHERE revision_id=?
	`, newID, revisionID); err != nil {
		return "", err
	}
	for idx, rc := range claimsRaw {
		cm, ok := rc.(map[string]any)
		if !ok {
			continue
		}
		var occArg any
		if occStr := mapString(cm, "occurred_at"); occStr != "" {
			if t, pErr := ParseUTCTime(occStr); pErr == nil {
				occArg = db.MysqlDatetime6(t)
			}
		}
		srcCreated := NowUTC()
		if scStr := mapString(cm, "source_created_at"); scStr != "" {
			if t, pErr := ParseUTCTime(scStr); pErr == nil {
				srcCreated = t
			}
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO wiki_revision_claims VALUES (?, ?, ?, ?, ?)",
			newID, idx, occArg, db.MysqlDatetime6(srcCreated), EncodeJSON(cm)); err != nil {
			return "", err
		}
	}

	if !opts.Standalone {
		if _, err := tx.ExecContext(ctx, "UPDATE wiki_pages SET locked=TRUE WHERE id=?", pageID); err != nil {
			return "", err
		}
		if _, err := tx.ExecContext(ctx, "INSERT IGNORE INTO wiki_dirty_pages VALUES (?, ?)", channelID, pageID); err != nil {
			return "", err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE wiki_channels SET source_generation=source_generation+1, dirty_at=UTC_TIMESTAMP(6) WHERE id=?", channelID); err != nil {
			return "", err
		}
	}

	var claimVal any
	if ordinal != nil {
		claimVal = *ordinal
	}
	if err := k.store.Audit(ctx, tx, channelID, "correct", newID, actor, map[string]any{"previous": revisionID, "claim": claimVal}); err != nil {
		return "", err
	}
	return newID, nil
}

// EditPage 提交管理员单页段落/标题修订并排入 page_edit 审校任务（对齐 knowledge.py:correct_page）。
func (k *Knowledge) EditPage(ctx context.Context, channelID, revisionID string, ordinal *int, text, actor string) (string, string, error) {
	var newRevID, jobID string
	err := k.store.WithTx(ctx, func(tx *sql.Tx) error {
		var enabled bool
		var pubRev sql.NullString
		if err := tx.QueryRowContext(ctx, "SELECT enabled, published_revision FROM wiki_channels WHERE id=? FOR UPDATE", channelID).Scan(&enabled, &pubRev); err != nil || !enabled {
			return errors.New("请先启用频道 Wiki")
		}
		var pageID string
		var approved bool
		if err := tx.QueryRowContext(ctx, "SELECT page_id, approved FROM wiki_page_revisions WHERE id=? AND channel_id=?", revisionID, channelID).Scan(&pageID, &approved); err != nil {
			return errors.New("找不到要修订的版本")
		}
		base, err := k.PublishedPage(ctx, tx, pubRev.String, pageID)
		if err != nil {
			return err
		}
		if approved && base != revisionID {
			return ErrPageEditConflict
		}
		newRevID, err = k.CreateCorrection(ctx, tx, channelID, revisionID, ordinal, text, actor, CorrectionOptions{
			ReplaceText: true,
			Standalone:  true,
		})
		if err != nil {
			return err
		}
		jobID, err = k.store.EnqueuePageEdit(ctx, tx, channelID, pageID, newRevID, base, actor)
		return err
	})
	return newRevID, jobID, err
}

// LockPage 锁定或解锁词条。
func (k *Knowledge) LockPage(ctx context.Context, channelID, pageID string, locked bool, actor string) error {
	action := "unlock"
	if locked {
		action = "lock"
	}
	return k.ManagePage(ctx, channelID, pageID, action, actor, PageManageOptions{})
}

// HidePage 隐藏或恢复显示词条。
func (k *Knowledge) HidePage(ctx context.Context, channelID, pageID string, hidden bool, actor string) error {
	action := "show"
	if hidden {
		action = "hide"
	}
	return k.ManagePage(ctx, channelID, pageID, action, actor, PageManageOptions{})
}

// RenamePage 重命名词条标题与 URL Slug，并将旧 Slug 写入 wiki_slug_aliases 重定向表。
func (k *Knowledge) RenamePage(ctx context.Context, channelID, pageID, title, slug, actor string) error {
	return k.ManagePage(ctx, channelID, pageID, "rename", actor, PageManageOptions{Title: title, Slug: slug})
}

// MergePages 将指定实体别名合并到目标词条并迁移相关事实。
func (k *Knowledge) MergePages(ctx context.Context, channelID, targetPageID, alias, actor string) error {
	return k.ManagePage(ctx, channelID, targetPageID, "merge", actor, PageManageOptions{Alias: alias})
}

// SplitPage 将指定实体别名重新绑定到目标词条并迁移对应事实。
func (k *Knowledge) SplitPage(ctx context.Context, channelID, targetPageID, alias, actor string) error {
	return k.ManagePage(ctx, channelID, targetPageID, "split", actor, PageManageOptions{Alias: alias})
}

// ManagePage 执行 lock / unlock / hide / show / rename / merge / split 词条管理动作（对齐 knowledge.py:manage_page）。
func (k *Knowledge) ManagePage(ctx context.Context, channelID, pageID, action, actor string, opts PageManageOptions) error {
	return k.store.WithTx(ctx, func(tx *sql.Tx) error {
		var cid string
		if err := tx.QueryRowContext(ctx, "SELECT id FROM wiki_channels WHERE id=? FOR UPDATE", channelID).Scan(&cid); err != nil {
			return err
		}
		var oldSlug string
		if err := tx.QueryRowContext(ctx, "SELECT slug FROM wiki_pages WHERE id=? AND channel_id=? FOR UPDATE", pageID, channelID).Scan(&oldSlug); err != nil {
			return errors.New("Unknown page")
		}

		switch action {
		case "lock", "unlock":
			if _, err := tx.ExecContext(ctx, "UPDATE wiki_pages SET locked=? WHERE id=?", action == "lock", pageID); err != nil {
				return err
			}
		case "hide", "show":
			if _, err := tx.ExecContext(ctx, "UPDATE wiki_pages SET hidden=? WHERE id=?", action == "hide", pageID); err != nil {
				return err
			}
		case "rename":
			title := strings.TrimSpace(opts.Title)
			slug := strings.TrimSpace(opts.Slug)
			if title == "" || slug == "" || !reValidSlug.MatchString(slug) {
				return errors.New("Title and valid slug are required")
			}
			var conflict string
			cErr := tx.QueryRowContext(ctx, `
				SELECT id FROM wiki_pages
				WHERE channel_id=? AND (slug=? OR id=?) AND id<>?
				UNION ALL
				SELECT page_id FROM wiki_slug_aliases
				WHERE channel_id=? AND slug IN (?, ?) AND page_id<>?
				LIMIT 1
			`, channelID, slug, slug, pageID, channelID, slug, oldSlug, pageID).Scan(&conflict)
			if cErr == nil && conflict != "" {
				return errors.New("该链接已被其他页面或其历史版本占用")
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO wiki_slug_aliases VALUES (?, ?, ?)
				ON DUPLICATE KEY UPDATE page_id=VALUES(page_id)
			`, channelID, oldSlug, pageID); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, "UPDATE wiki_pages SET title=?, slug=?, override_json=? WHERE id=?",
				truncateRunesSimple(title, 160), slug, EncodeJSON(map[string]any{"title": title}), pageID); err != nil {
				return err
			}

		case "merge", "split":
			alias := strings.TrimSpace(opts.Alias)
			if alias == "" {
				return errors.New("Alias is required")
			}
			if _, err := tx.ExecContext(ctx, "INSERT INTO wiki_entity_aliases VALUES (?, ?, ?, ?, TRUE) ON DUPLICATE KEY UPDATE page_id=VALUES(page_id), `manual`=TRUE",
				channelID, EntityKey(alias), truncateRunesSimple(alias, 160), pageID); err != nil {
				return err
			}
			fRows, err := tx.QueryContext(ctx, "SELECT id, fact FROM wiki_facts WHERE channel_id=?", channelID)
			if err != nil {
				return err
			}
			targetNK := NameKey(alias)
			var moveIDs []string
			for fRows.Next() {
				var fid string
				var fRaw []byte
				if err := fRows.Scan(&fid, &fRaw); err == nil {
					if NameKey(mapString(decodeMapJSON(fRaw), "entity")) == targetNK {
						moveIDs = append(moveIDs, fid)
					}
				}
			}
			_ = fRows.Close()
			for _, fid := range moveIDs {
				if _, err := tx.ExecContext(ctx, "UPDATE wiki_facts SET page_id=? WHERE id=?", pageID, fid); err != nil {
					return err
				}
			}

		default:
			return errors.New("Unknown page action")
		}

		if _, err := tx.ExecContext(ctx, "INSERT IGNORE INTO wiki_dirty_pages VALUES (?, ?)", channelID, pageID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE wiki_channels SET source_generation=source_generation+1, dirty_at=UTC_TIMESTAMP(6) WHERE id=?", channelID); err != nil {
			return err
		}
		return k.store.Audit(ctx, tx, channelID, action, pageID, actor, opts)
	})
}

// ExcludeSource 代理 Store.ExcludeSource 供管理端调用。
func (k *Knowledge) ExcludeSource(ctx context.Context, channelID, sourceID, actor string) error {
	return k.store.ExcludeSource(ctx, channelID, sourceID, actor)
}

// ReviewRevision 执行管理员对修订的人工批准或拒绝（对齐 routes/wiki.py:manually_approve_revision 与 reject）。
func (k *Knowledge) ReviewRevision(ctx context.Context, channelID, revisionID string, approve bool, actor string) (map[string]any, error) {
	if !approve {
		err := k.store.WithTx(ctx, func(tx *sql.Tx) error {
			var cid string
			if err := tx.QueryRowContext(ctx, "SELECT id FROM wiki_channels WHERE id=? FOR UPDATE", channelID).Scan(&cid); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, "UPDATE wiki_page_revisions SET rejected=TRUE WHERE id=? AND channel_id=?", revisionID, channelID); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `
				UPDATE wiki_pages p JOIN wiki_page_revisions r ON r.page_id=p.id
				SET p.locked=TRUE WHERE r.id=? AND r.channel_id=?
			`, revisionID, channelID); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, "UPDATE wiki_channels SET source_generation=source_generation+1 WHERE id=?", channelID); err != nil {
				return err
			}
			return k.store.Audit(ctx, tx, channelID, "reject", revisionID, actor, nil)
		})
		if err != nil {
			return nil, err
		}
		return map[string]any{"success": true}, nil
	}

	var (
		publicationID      string
		publicationError   string
		publicationPending bool
	)
	err := k.store.WithTx(ctx, func(tx *sql.Tx) error {
		chRow := tx.QueryRowContext(ctx, `
			SELECT c.id, c.title, c.username, c.enabled, c.public, c.access_verified, c.verified_until,
			       c.config, c.coverage, c.published_revision, c.source_generation, c.active_job,
			       c.dirty_at, c.scheduled_at, c.reconciled_at, c.updated_at, cover.digest
			FROM wiki_channels c
			LEFT JOIN wiki_channel_covers cover ON cover.channel_id=c.id
			WHERE c.id=? FOR UPDATE
		`, channelID)
		ch, err := scanChannelRow(chRow)
		if err != nil {
			return err
		}

		var pageID, topic string
		var jobID sql.NullString
		if err := tx.QueryRowContext(ctx, `
			SELECT r.page_id, r.job_id, p.topic
			FROM wiki_page_revisions r JOIN wiki_pages p ON p.id=r.page_id
			WHERE r.id=? AND r.channel_id=?
		`, revisionID, channelID).Scan(&pageID, &jobID, &topic); err != nil {
			return errors.New("找不到该修订")
		}
		if slices.Contains(ch.Config.HiddenTopics, topic) {
			return errors.New("该主题已隐藏")
		}
		if err := k.ManuallyApproveRevision(ctx, tx, channelID, pageID, revisionID, actor); err != nil {
			return errors.New("该修订已拒绝、已隐藏或引用来源已失效")
		}

		// 对齐 routes/wiki.py：同步更新待人工放行的已完成构建任务
		jRows, err := tx.QueryContext(ctx, "SELECT id, `cursor`, progress FROM wiki_jobs WHERE channel_id=? AND status='completed' AND phase='ready' AND JSON_EXTRACT(progress,'$.review_rejections')>0 FOR UPDATE", channelID)
		if err != nil {
			return err
		}
		type pendingJob struct {
			id       string
			manifest map[string]any
			progress map[string]any
		}
		var pJobs []pendingJob
		for jRows.Next() {
			var jid string
			var cRaw, pRaw []byte
			if err := jRows.Scan(&jid, &cRaw, &pRaw); err == nil {
				pJobs = append(pJobs, pendingJob{id: jid, manifest: decodeMapJSON(cRaw), progress: decodeMapJSON(pRaw)})
			}
		}
		_ = jRows.Close()
		for _, pj := range pJobs {
			if reconciled, rErr := k.store.ReconcileManualReviews(ctx, tx, pj.manifest, pj.progress); rErr == nil && reconciled {
				_, _ = tx.ExecContext(ctx, "UPDATE wiki_jobs SET `cursor`=?, progress=?, updated_at=UTC_TIMESTAMP(6) WHERE id=?",
					EncodeJSON(pj.manifest), EncodeJSON(pj.progress), pj.id)
			}
		}

		if jobID.Valid && jobID.String != "" {
			if editJob, jErr := getJobTx(ctx, tx, jobID.String); jErr == nil && editJob != nil && editJob.Kind == "page_edit" {
				curPub, _ := k.PublishedPage(ctx, tx, ch.PublishedRevision, pageID)
				revs := extractRevisionMap(editJob.Cursor)
				if curPub == revisionID {
					publicationID = ch.PublishedRevision
				} else if editJob.Status == "completed" && editJob.Phase == "ready" &&
					mapBool(editJob.Progress, "generation_complete") &&
					mapBool(editJob.Progress, "review_complete") &&
					ch.Config.PublicationMode == "auto" &&
					revs[pageID] == revisionID {
					if accessErr := validatePublicationAccess(ch); accessErr != nil {
						publicationError = "频道暂不满足发布条件。草稿已保留，请核对频道启用和公开状态后重试。"
					} else {
						manifest := cloneMap(editJob.Cursor)
						manifest["review_policy"] = ManualReviewPolicy
						pub, pubErr := k.publishPageEditTx(ctx, tx, ch, editJob, manifest, editJob.Progress, NewID(), actor)
						if pubErr != nil {
							publicationError = "当前词条状态或发布版本已变化，请刷新词条后重试。"
						} else {
							publicationID = pub
						}
					}
				} else {
					publicationPending = true
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	var pubVal any
	if publicationID != "" {
		pubVal = publicationID
	}
	return map[string]any{
		"success":             true,
		"publication_id":      pubVal,
		"publication_error":   publicationError,
		"publication_pending": publicationPending,
	}, nil
}

// Prune 清理未被任何发布引用的失活边、被新修订取代的旧分块及超出保留上限的历史发布快照（对齐 knowledge.py:prune）。
func (k *Knowledge) Prune(ctx context.Context, channelID string, keepPublications int) (map[string]int64, error) {
	if keepPublications < 2 {
		keepPublications = 20
	}
	var edgesDel, chunksDel, pubsDel int64
	err := k.store.WithTx(ctx, func(tx *sql.Tx) error {
		var cid string
		if err := tx.QueryRowContext(ctx, "SELECT id FROM wiki_channels WHERE id=? FOR UPDATE", channelID).Scan(&cid); err != nil {
			return err
		}
		res1, err := tx.ExecContext(ctx, `
			DELETE e FROM wiki_edges e
			LEFT JOIN wiki_publication_edges pe ON pe.edge_id=e.id
			WHERE e.channel_id=? AND e.active=FALSE AND pe.publication_id IS NULL
		`, channelID)
		if err != nil {
			return err
		}
		edgesDel, _ = res1.RowsAffected()

		res2, err := tx.ExecContext(ctx, `
			DELETE ch FROM wiki_sources s
			STRAIGHT_JOIN wiki_chunks ch ON ch.source_id=s.id
			LEFT JOIN wiki_evidence ev ON ev.chunk_id=ch.id
			LEFT JOIN wiki_edges e ON e.chunk_id=ch.id
			LEFT JOIN wiki_publication_edges pe ON pe.edge_id=e.id
			WHERE s.channel_id=? AND s.revision>1 AND ch.content_hash<>s.content_hash
			  AND ev.revision_id IS NULL AND pe.publication_id IS NULL
		`, channelID)
		if err != nil {
			return err
		}
		chunksDel, _ = res2.RowsAffected()

		res3, err := tx.ExecContext(ctx, `
			DELETE p FROM wiki_publications p
			JOIN (
				SELECT id, ROW_NUMBER() OVER (ORDER BY created_at DESC, id DESC) AS position_
				FROM wiki_publications WHERE channel_id=?
			) ranked ON ranked.id=p.id
			WHERE ranked.position_ > ?
			  AND p.id <> COALESCE((SELECT published_revision FROM wiki_channels WHERE id=?), '')
		`, channelID, keepPublications, channelID)
		if err != nil {
			return err
		}
		pubsDel, _ = res3.RowsAffected()

		if edgesDel > 0 || chunksDel > 0 || pubsDel > 0 {
			return k.store.Audit(ctx, tx, channelID, "prune", channelID, "wiki", map[string]any{
				"edges":        edgesDel,
				"chunks":       chunksDel,
				"publications": pubsDel,
			})
		}
		return nil
	})
	return map[string]int64{
		"edges":        edgesDel,
		"chunks":       chunksDel,
		"publications": pubsDel,
	}, err
}
