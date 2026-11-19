// Copyright 2026 Sakura-Bot
// SPDX-License-Identifier: AGPL-3.0-or-later

package webapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/Sakura520222/Sakura-Bot/internal/wiki"
)

func (s *Server) wikiRefreshExclusions(w http.ResponseWriter, r *http.Request, service *wiki.Runtime, channel *wiki.ChannelRecord) {
	ctx := r.Context()
	actor := wikiActor(r)
	active := channel.ActiveJob
	if active != "" {
		var status string
		err := service.Store.DB.QueryRowContext(ctx, "SELECT status FROM wiki_jobs WHERE id=? AND channel_id=?", active, channel.ID).Scan(&status)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			s.wikiResponse(w, nil, err, false)
			return
		}
		if errors.Is(err, sql.ErrNoRows) {
			active = ""
		}
		if status == "running" || status == "pending" || status == "partial" {
			if err := service.Store.ControlJob(ctx, channel.ID, active, "pause", actor); err != nil {
				s.wikiProposalError(w, err)
				return
			}
			defer func() {
				cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
				defer cancel()
				if err := service.Store.ControlJob(cleanup, channel.ID, active, "resume", actor); err != nil {
					s.logger.Warn("恢复 Wiki 任务失败", "job_id", active, "error", err)
				}
				service.Wake()
			}()
		}
	}
	restored, err := service.Collector.RepairExclusions(ctx, channel)
	if err != nil {
		s.wikiResponse(w, nil, err, false)
		return
	}
	if restored == nil {
		restored = []int64{}
	}
	err = service.Store.WithTx(ctx, func(tx *sql.Tx) error {
		var channelID string
		err := tx.QueryRowContext(ctx, "SELECT id FROM wiki_channels WHERE id=? FOR UPDATE", channel.ID).Scan(&channelID)
		if err != nil {
			return err
		}
		if len(restored) > 0 {
			if active != "" {
				var cursorRaw, progressRaw []byte
				query := "SELECT `cursor`,progress FROM wiki_jobs WHERE id=? AND channel_id=? FOR UPDATE"
				if err := tx.QueryRowContext(ctx, query, active, channel.ID).Scan(&cursorRaw, &progressRaw); err != nil {
					return err
				}
				cursor, progress := decodeWikiObject(cursorRaw), decodeWikiObject(progressRaw)
				if _, ok := cursor["batch_pipeline"]; ok {
					cursor["source_after"] = ""
					cursor["processed_count"] = 0
					delete(cursor, "page_after")
					cursor["revisions"] = map[string]any{}
					delete(cursor, "dropped_pages")
					progress["processed"] = 0
					progress["processed_chunks"] = 0
					progress["generation_complete"] = false
					progress["review_complete"] = false
					if _, err := tx.ExecContext(ctx, "UPDATE wiki_jobs SET phase='extract',`cursor`=?,progress=? WHERE id=?",
						wiki.EncodeJSON(cursor), wiki.EncodeJSON(progress), active); err != nil {
						return err
					}
				}
			} else if channel.Enabled {
				if _, err := service.Store.EnqueueJob(ctx, tx, channel.ID, "reindex", actor, "", false); err != nil {
					return err
				}
			}
		}
		return service.Store.Audit(ctx, tx, channel.ID, "repair_exclusions", channel.ID, actor, map[string]any{"restored_message_ids": restored})
	})
	service.Wake()
	s.wikiResponse(w, map[string]any{"restored": len(restored), "message_ids": restored}, err, true)
}

var errNoFailedWikiReview = errors.New("没有可重试的审校结果")

func (s *Server) wikiRetryRevision(w http.ResponseWriter, r *http.Request, service *wiki.Runtime, channel *wiki.ChannelRecord) {
	revision := r.PathValue("revision_id")
	if !wikiIDPattern.MatchString(revision) {
		wikiNotFound(w)
		return
	}
	var jobID string
	ctx, actor := r.Context(), wikiActor(r)
	err := service.Store.WithTx(ctx, func(tx *sql.Tx) error {
		var active sql.NullString
		if err := tx.QueryRowContext(ctx, "SELECT active_job FROM wiki_channels WHERE id=? FOR UPDATE", channel.ID).Scan(&active); err != nil {
			return err
		}
		var previousEditStatus string
		var cursorRaw []byte
		query := "SELECT j.status,j.`cursor` FROM wiki_jobs j JOIN wiki_page_revisions r ON r.job_id=j.id WHERE r.id=? AND r.channel_id=? AND j.kind='page_edit' FOR UPDATE"
		editErr := tx.QueryRowContext(ctx, query, revision, channel.ID).Scan(&previousEditStatus, &cursorRaw)
		if editErr != nil && !errors.Is(editErr, sql.ErrNoRows) {
			return editErr
		}
		if editErr == nil && previousEditStatus != "completed" && previousEditStatus != "failed" {
			return errors.New("该词条修订仍在处理中")
		}
		if editErr != nil && active.String != "" {
			return errors.New("请等待当前 Wiki 任务结束")
		}
		previous, err := wiki.QueryRowsMap(ctx, tx, `SELECT rv.policy_key,rv.subject_hash,rv.result,rv.created_at
			FROM wiki_revision_reviews rv JOIN wiki_page_revisions r ON r.id=rv.revision_id
			WHERE r.id=? AND r.channel_id=? AND r.rejected=FALSE AND rv.approved=FALSE FOR UPDATE`, revision, channel.ID)
		if err != nil {
			return err
		}
		if len(previous) == 0 {
			return errNoFailedWikiReview
		}
		for _, row := range previous {
			if raw, ok := row["result"].(string); ok {
				row["result"] = decodeWikiObject([]byte(raw))
			}
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM wiki_revision_reviews WHERE revision_id=? AND approved=FALSE", revision); err != nil {
			return err
		}
		if err := service.Store.Audit(ctx, tx, channel.ID, "retry_revision_review", revision, actor, map[string]any{"previous_reviews": previous}); err != nil {
			return err
		}
		if editErr == nil {
			cursor := decodeWikiObject(cursorRaw)
			pageID, _ := cursor["page_id"].(string)
			base, _ := cursor["base_revision"].(string)
			jobID, err = service.Store.EnqueuePageEdit(ctx, tx, channel.ID, pageID, revision, base, actor)
		} else {
			jobID, err = service.Store.EnqueueJob(ctx, tx, channel.ID, "reindex", actor, "", false)
		}
		return err
	})
	if errors.Is(err, errNoFailedWikiReview) {
		writeHTTPError(w, http.StatusNotFound, err.Error())
		return
	}
	if err != nil {
		s.wikiProposalError(w, err)
		return
	}
	service.Wake()
	writeJSON(w, http.StatusAccepted, map[string]string{"job_id": jobID})
}

func (s *Server) wikiExtractions(w http.ResponseWriter, r *http.Request, service *wiki.Runtime, channel *wiki.ChannelRecord) {
	q := wikiQuery{request: r}
	page := q.number("page", 1, 1, 10000)
	if !q.valid(w) {
		return
	}
	rows, err := wiki.QueryRowsMap(r.Context(), service.Store.DB, `
		SELECT e.cache_key,e.review,e.result,e.created_at,s.source_type
		FROM wiki_extractions e JOIN wiki_chunks chunk ON chunk.id=e.chunk_id
		JOIN wiki_sources s ON s.id=chunk.source_id WHERE e.channel_id=? AND e.approved=FALSE
		AND NOT (JSON_EXTRACT(e.review,'$.mode') <=> 'draft')
		AND s.content_hash=chunk.content_hash AND s.hidden=FALSE AND s.deleted=FALSE
		ORDER BY e.created_at DESC LIMIT 50 OFFSET ?`, channel.ID, (page-1)*50)
	for _, row := range rows {
		for _, key := range []string{"review", "result"} {
			if raw, ok := row[key].(string); ok {
				row[key] = decodeWikiObject([]byte(raw))
			}
		}
	}
	s.wikiRows(w, rows, err)
}

func decodeWikiObject(raw []byte) map[string]any {
	var obj map[string]any
	if json.Unmarshal(raw, &obj) != nil || obj == nil {
		return map[string]any{}
	}
	return obj
}

func (s *Server) wikiRetryExtraction(w http.ResponseWriter, r *http.Request, service *wiki.Runtime, channel *wiki.ChannelRecord) {
	cacheKey := r.PathValue("cache_key")
	if !wikiHashPattern.MatchString(cacheKey) {
		wikiNotFound(w)
		return
	}
	ctx, actor := r.Context(), wikiActor(r)
	var jobID string
	err := service.Store.WithTx(ctx, func(tx *sql.Tx) error {
		var channelID string
		if err := tx.QueryRowContext(ctx, "SELECT id FROM wiki_channels WHERE id=? FOR UPDATE", channel.ID).Scan(&channelID); err != nil {
			return err
		}
		var candidate string
		err := tx.QueryRowContext(ctx, "SELECT cache_key FROM wiki_extractions WHERE cache_key=? AND channel_id=? AND approved=FALSE FOR UPDATE",
			cacheKey, channel.ID).Scan(&candidate)
		if errors.Is(err, sql.ErrNoRows) {
			return errNoFailedWikiReview
		}
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE wiki_extractions SET review=? WHERE cache_key=? AND channel_id=? AND approved=FALSE",
			wiki.EncodeJSON(map[string]bool{"pending": true}), cacheKey, channel.ID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE wiki_channels SET source_generation=source_generation+1,dirty_at=UTC_TIMESTAMP(6) WHERE id=?", channel.ID); err != nil {
			return err
		}
		if err := service.Store.Audit(ctx, tx, channel.ID, "retry_review", cacheKey, actor, nil); err != nil {
			return err
		}
		jobID, err = service.Store.EnqueueJob(ctx, tx, channel.ID, "reindex", actor, "", false)
		return err
	})
	if errors.Is(err, errNoFailedWikiReview) {
		writeHTTPError(w, http.StatusNotFound, "没有可重试的待审候选")
		return
	}
	if err != nil {
		s.wikiProposalError(w, err)
		return
	}
	service.Wake()
	writeJSON(w, http.StatusAccepted, map[string]string{"job_id": jobID})
}
