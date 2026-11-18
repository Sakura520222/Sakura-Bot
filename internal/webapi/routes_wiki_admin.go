// Copyright 2026 Sakura-Bot
// SPDX-License-Identifier: AGPL-3.0-or-later

package webapi

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/Sakura520222/Sakura-Bot/internal/wiki"
)

type wikiChannelHandler func(http.ResponseWriter, *http.Request, *wiki.Runtime, *wiki.ChannelRecord)

var wikiHashPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
var wikiSlugPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,180}$`)

func (s *Server) registerWikiAdminRoutes(mux *http.ServeMux) {
	prefix := "/api/wiki/admin/channels"
	mux.HandleFunc("GET "+prefix, s.wikiAdmin(s.wikiManagedChannels))
	for _, route := range []struct {
		method, path string
		handler      wikiChannelHandler
	}{
		{"GET", "/config", s.wikiGetChannelConfig},
		{"PUT", "/config", s.wikiSetChannelConfig},
		{"PUT", "/cover", s.wikiSetCover},
		{"DELETE", "/cover", s.wikiSetCover},
		{"GET", "/excluded", s.wikiExcludedSources},
		{"POST", "/excluded/refresh", s.wikiRefreshExclusions},
		{"GET", "/jobs", s.wikiJobs},
		{"POST", "/jobs", s.wikiCreateJob},
		{"POST", "/jobs/{job_id}/{action}", s.wikiControlJob},
		{"GET", "/revisions", s.wikiRevisions},
		{"GET", "/revisions/{revision_id}", s.wikiRevisionDetail},
		{"POST", "/revisions/{revision_id}/review/approve", s.wikiReviewRevision},
		{"POST", "/revisions/{revision_id}/reject", s.wikiReviewRevision},
		{"POST", "/revisions/{revision_id}/correct", s.wikiCorrectRevision},
		{"POST", "/revisions/{revision_id}/review/retry", s.wikiRetryRevision},
		{"POST", "/publish", s.wikiPublish},
		{"POST", "/rollback", s.wikiPublish},
		{"GET", "/publications", s.wikiPublications},
		{"GET", "/publications/{publication_id}/pages/{page_id}", s.wikiPublicationPreview},
		{"GET", "/extractions", s.wikiExtractions},
		{"POST", "/extractions/{cache_key}/retry", s.wikiRetryExtraction},
		{"POST", "/withdraw", s.wikiWithdrawChannel},
		{"POST", "/hide-source", s.wikiHideSource},
		{"GET", "/pages/{page_id}", s.wikiPageState},
		{"POST", "/pages/{page_id}", s.wikiPageAction},
		{"GET", "/audit", s.wikiChannelAudit},
		{"POST", "/purge", s.wikiPurgeChannel},
	} {
		mux.HandleFunc(route.method+" "+prefix+"/{channel}"+route.path, s.wikiAdmin(s.wikiManaged(route.handler)))
	}
}

func (s *Server) wikiManaged(next wikiChannelHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		service := s.wikiRuntime(w, true)
		if service == nil {
			return
		}
		channel, err := service.Store.GetChannel(r.Context(), r.PathValue("channel"))
		if err != nil {
			s.wikiResponse(w, nil, err, false)
			return
		}
		if channel == nil {
			writeHTTPError(w, http.StatusNotFound, "频道尚未完成身份核实，请检查 UserBot 和频道配置")
			return
		}
		next(w, r, service, channel)
	}
}

func (s *Server) wikiManagedChannels(w http.ResponseWriter, r *http.Request) {
	service := s.wikiRuntime(w, true)
	if service == nil {
		return
	}
	channels, err := service.Store.ListChannels(r.Context())
	if channels == nil {
		channels = []*wiki.ChannelRecord{}
	}
	s.wikiResponse(w, channels, err, true)
}

func (s *Server) wikiGetChannelConfig(w http.ResponseWriter, _ *http.Request, _ *wiki.Runtime, channel *wiki.ChannelRecord) {
	writeJSON(w, http.StatusOK, channel)
}

func (s *Server) wikiSetChannelConfig(w http.ResponseWriter, r *http.Request, service *wiki.Runtime, channel *wiki.ChannelRecord) {
	cfg := wiki.DefaultWikiConfig()
	if readJSONStrict(r, &cfg, 1<<20) != nil {
		writeHTTPError(w, http.StatusUnprocessableEntity, "Wiki 频道配置无效")
		return
	}
	if err := cfg.Validate(); err != nil {
		writeHTTPError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if cfg.Public && !channel.AccessVerified {
		writeHTTPError(w, http.StatusConflict, "频道不是已核实的公开频道")
		return
	}
	ctx := r.Context()
	if err := service.Store.ConfigureChannel(ctx, channel.ID, cfg, wikiActor(r)); err != nil {
		s.wikiProposalError(w, err)
		return
	}
	updated, err := service.Store.GetChannel(ctx, channel.ID)
	s.wikiResponse(w, updated, err, updated != nil)
}

func (s *Server) wikiSetCover(w http.ResponseWriter, r *http.Request, service *wiki.Runtime, channel *wiki.ChannelRecord) {
	var digest string
	var image []byte
	if r.Method == http.MethodPut {
		const limit = 8 << 20
		defer r.Body.Close()
		var err error
		image, err = io.ReadAll(io.LimitReader(r.Body, limit+1))
		if err != nil {
			writeHTTPError(w, http.StatusBadRequest, "读取频道封面失败")
			return
		}
		if len(image) > limit {
			writeHTTPError(w, http.StatusRequestEntityTooLarge, "频道封面不能超过 8 MiB")
			return
		}
		if err := wiki.ValidateImageBytes(image, limit); err != nil {
			writeHTTPError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		hash := sha256.Sum256(image)
		digest = hex.EncodeToString(hash[:])
	}
	found, err := service.Store.SetCover(r.Context(), channel.ID, digest, image, wikiActor(r))
	var value any
	if digest != "" {
		value = digest
	}
	s.wikiResponse(w, map[string]any{"cover_digest": value}, err, found)
}

func (s *Server) wikiExcludedSources(w http.ResponseWriter, r *http.Request, service *wiki.Runtime, channel *wiki.ChannelRecord) {
	q := wikiQuery{request: r}
	page := q.number("page", 1, 1, 1000000)
	if q.valid(w) {
		var reason *string
		if r.URL.Query().Has("reason") {
			value := r.URL.Query().Get("reason")
			reason = &value
		}
		data, err := service.Store.ListExcludedSources(r.Context(), channel.ID, channel.Username, page, reason)
		s.wikiResponse(w, data, err, data != nil)
	}
}

func (s *Server) wikiJobs(w http.ResponseWriter, r *http.Request, service *wiki.Runtime, channel *wiki.ChannelRecord) {
	q := wikiQuery{request: r}
	page := q.number("page", 1, 1, 10000)
	if q.valid(w) {
		rows, err := service.Store.ListJobs(r.Context(), channel.ID, page)
		s.wikiRows(w, rows, err)
	}
}

func (s *Server) wikiCreateJob(w http.ResponseWriter, r *http.Request, service *wiki.Runtime, channel *wiki.ChannelRecord) {
	body := struct {
		Kind   string `json:"kind"`
		PageID string `json:"page_id"`
	}{Kind: "update"}
	if readJSONStrict(r, &body, 1<<14) != nil ||
		!slices.Contains([]string{"backfill", "update", "reconcile", "reindex", "page", "albums"}, body.Kind) ||
		(body.PageID != "" && !wikiIDPattern.MatchString(body.PageID)) {
		writeHTTPError(w, http.StatusUnprocessableEntity, "无效的 Wiki 任务")
		return
	}
	id, err := service.Store.EnqueueJob(r.Context(), nil, channel.ID, body.Kind, wikiActor(r), body.PageID, true)
	if err != nil {
		s.wikiProposalError(w, err)
		return
	}
	service.Wake()
	writeJSON(w, http.StatusAccepted, map[string]string{"job_id": id})
}

func (s *Server) wikiControlJob(w http.ResponseWriter, r *http.Request, service *wiki.Runtime, channel *wiki.ChannelRecord) {
	action, job := r.PathValue("action"), r.PathValue("job_id")
	if !slices.Contains([]string{"pause", "resume", "retry", "cancel"}, action) || !wikiIDPattern.MatchString(job) {
		writeHTTPError(w, http.StatusUnprocessableEntity, "无效的 Wiki 任务操作")
		return
	}
	if err := service.Store.ControlJob(r.Context(), channel.ID, job, action, wikiActor(r)); err != nil {
		s.wikiProposalError(w, err)
		return
	}
	if action == "resume" || action == "retry" {
		service.Wake()
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func (s *Server) wikiRevisions(w http.ResponseWriter, r *http.Request, service *wiki.Runtime, channel *wiki.ChannelRecord) {
	q := wikiQuery{request: r}
	page := q.number("page", 1, 1, 10000)
	search := q.text("q", "", 160)
	status := q.enum("status", "all", "all", "pending", "failed", "passed", "rejected")
	jobID := q.id("job_id", false)
	if q.valid(w) {
		rows, err := service.Reader.ListRevisions(r.Context(), channel.ID, status, jobID, search, page)
		s.wikiRows(w, rows, err)
	}
}

func (s *Server) wikiRevisionDetail(w http.ResponseWriter, r *http.Request, service *wiki.Runtime, channel *wiki.ChannelRecord) {
	q := wikiQuery{request: r}
	jobID := q.id("job_id", false)
	revision := r.PathValue("revision_id")
	if !wikiIDPattern.MatchString(revision) {
		wikiNotFound(w)
		return
	}
	if q.valid(w) {
		data, err := service.Reader.GetRevisionDetail(r.Context(), channel.ID, revision, jobID)
		s.wikiResponse(w, data, err, data != nil)
	}
}

func (s *Server) wikiReviewRevision(w http.ResponseWriter, r *http.Request, service *wiki.Runtime, channel *wiki.ChannelRecord) {
	revision := r.PathValue("revision_id")
	if !wikiIDPattern.MatchString(revision) {
		wikiNotFound(w)
		return
	}
	approve := strings.HasSuffix(r.URL.Path, "/review/approve")
	result, err := service.Engine.Knowledge.ReviewRevision(r.Context(), channel.ID, revision, approve, wikiActor(r))
	if err != nil {
		s.wikiProposalError(w, err)
		return
	}
	if approve {
		service.Wake()
	} else {
		result = map[string]any{"success": true}
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) wikiCorrectRevision(w http.ResponseWriter, r *http.Request, service *wiki.Runtime, channel *wiki.ChannelRecord) {
	var body struct {
		Claim *int   `json:"claim"`
		Text  string `json:"text"`
	}
	revision := r.PathValue("revision_id")
	if readJSONStrict(r, &body, 1<<16) != nil || strings.TrimSpace(body.Text) == "" ||
		utf8.RuneCountInString(body.Text) > 4000 || (body.Claim != nil && *body.Claim < 0) || !wikiIDPattern.MatchString(revision) {
		writeHTTPError(w, http.StatusUnprocessableEntity, "Wiki 修订内容无效")
		return
	}
	id, job, err := service.Engine.Knowledge.EditPage(r.Context(), channel.ID, revision, body.Claim, body.Text, wikiActor(r))
	if err != nil {
		s.wikiProposalError(w, err)
		return
	}
	service.Wake()
	writeJSON(w, http.StatusOK, map[string]string{"revision_id": id, "job_id": job})
}

func (s *Server) wikiPublish(w http.ResponseWriter, r *http.Request, service *wiki.Runtime, channel *wiki.ChannelRecord) {
	var job string
	var opts []wiki.PublishOption
	if strings.HasSuffix(r.URL.Path, "/rollback") {
		var body struct {
			Publication string `json:"publication_id"`
		}
		if readJSONStrict(r, &body, 1<<14) != nil || !wikiIDPattern.MatchString(body.Publication) {
			writeHTTPError(w, http.StatusUnprocessableEntity, "无效的发布版本 ID")
			return
		}
		opts = append(opts, wiki.WithRollback(body.Publication))
	} else {
		var body struct {
			Job string `json:"job_id"`
		}
		if readJSONStrict(r, &body, 1<<14) != nil || !wikiIDPattern.MatchString(body.Job) {
			writeHTTPError(w, http.StatusUnprocessableEntity, "无效的 Wiki 任务 ID")
			return
		}
		job = body.Job
	}
	id, err := service.Engine.Knowledge.Publish(r.Context(), channel.ID, job, wikiActor(r), opts...)
	if err != nil {
		s.wikiProposalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"publication_id": id})
}

func (s *Server) wikiPublications(w http.ResponseWriter, r *http.Request, service *wiki.Runtime, channel *wiki.ChannelRecord) {
	q := wikiQuery{request: r}
	page := q.number("page", 1, 1, 10000)
	if q.valid(w) {
		rows, err := wiki.QueryRowsMap(r.Context(), service.Store.DB,
			"SELECT id,job_id,created_at FROM wiki_publications WHERE channel_id=? ORDER BY created_at DESC LIMIT 50 OFFSET ?",
			channel.ID, (page-1)*50)
		s.wikiRows(w, rows, err)
	}
}

func (s *Server) wikiPublicationPreview(w http.ResponseWriter, r *http.Request, service *wiki.Runtime, channel *wiki.ChannelRecord) {
	pubID, pageID := r.PathValue("publication_id"), r.PathValue("page_id")
	if !wikiIDPattern.MatchString(pubID) || !wikiIDPattern.MatchString(pageID) {
		wikiNotFound(w)
		return
	}
	data, err := service.Reader.GetPublicationPagePreview(r.Context(), channel.ID, pubID, pageID)
	s.wikiResponse(w, data, err, data != nil)
}

func (s *Server) wikiWithdrawChannel(w http.ResponseWriter, r *http.Request, service *wiki.Runtime, channel *wiki.ChannelRecord) {
	err := service.Store.WithdrawChannel(r.Context(), channel.ID, wikiActor(r))
	s.wikiResponse(w, map[string]bool{"success": true}, err, true)
}

func (s *Server) wikiHideSource(w http.ResponseWriter, r *http.Request, service *wiki.Runtime, channel *wiki.ChannelRecord) {
	var body struct {
		Source string `json:"source_id"`
	}
	if readJSONStrict(r, &body, 1<<14) != nil || !wikiHashPattern.MatchString(body.Source) {
		writeHTTPError(w, http.StatusUnprocessableEntity, "无效的来源 ID")
		return
	}
	err := service.Store.ExcludeSource(r.Context(), channel.ID, body.Source, wikiActor(r))
	s.wikiResponse(w, map[string]bool{"success": true}, err, true)
}

func (s *Server) wikiPageState(w http.ResponseWriter, r *http.Request, service *wiki.Runtime, channel *wiki.ChannelRecord) {
	page := r.PathValue("page_id")
	var id, title string
	var locked, hidden bool
	err := service.Store.DB.QueryRowContext(r.Context(), `SELECT p.id,p.title,p.locked,p.hidden FROM wiki_pages p WHERE p.channel_id=?
		AND (p.id=? OR p.slug=? OR EXISTS (SELECT 1 FROM wiki_slug_aliases a
		WHERE a.channel_id=p.channel_id AND a.page_id=p.id AND a.slug=?))`,
		channel.ID, page, page, page).Scan(&id, &title, &locked, &hidden)
	if errors.Is(err, sql.ErrNoRows) {
		wikiNotFound(w)
		return
	}
	s.wikiResponse(w, map[string]any{"id": id, "title": title, "locked": locked, "hidden": hidden}, err, true)
}

func (s *Server) wikiPageAction(w http.ResponseWriter, r *http.Request, service *wiki.Runtime, channel *wiki.ChannelRecord) {
	var body struct {
		Action string `json:"action"`
		Title  string `json:"title"`
		Slug   string `json:"slug"`
		Alias  string `json:"alias"`
	}
	if readJSONStrict(r, &body, 1<<14) != nil || !slices.Contains([]string{"lock", "unlock", "hide", "show", "rename", "merge", "split"}, body.Action) ||
		utf8.RuneCountInString(body.Title) > 160 || utf8.RuneCountInString(body.Alias) > 160 ||
		(body.Slug != "" && !wikiSlugPattern.MatchString(body.Slug)) {
		writeHTTPError(w, http.StatusUnprocessableEntity, "无效的词条操作")
		return
	}
	if err := service.Engine.Knowledge.ManagePage(r.Context(), channel.ID, r.PathValue("page_id"),
		body.Action, wikiActor(r), wiki.PageManageOptions{Title: body.Title, Slug: body.Slug, Alias: body.Alias}); err != nil {
		s.wikiProposalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func (s *Server) wikiChannelAudit(w http.ResponseWriter, r *http.Request, service *wiki.Runtime, channel *wiki.ChannelRecord) {
	q := wikiQuery{request: r}
	page := q.number("page", 1, 1, 10000)
	if q.valid(w) {
		rows, err := wiki.QueryRowsMap(r.Context(), service.Store.DB,
			"SELECT action,target_id,actor,details,created_at FROM wiki_review_actions WHERE channel_id=? ORDER BY id DESC LIMIT 50 OFFSET ?",
			channel.ID, (page-1)*50)
		s.wikiRows(w, rows, err)
	}
}

func (s *Server) wikiPurgeChannel(w http.ResponseWriter, r *http.Request, service *wiki.Runtime, channel *wiki.ChannelRecord) {
	var body struct {
		Confirm string `json:"confirm_channel"`
	}
	if readJSONStrict(r, &body, 1<<14) != nil || body.Confirm == "" {
		writeHTTPError(w, http.StatusUnprocessableEntity, "缺少频道确认")
		return
	}
	if body.Confirm != channel.ID {
		writeHTTPError(w, http.StatusConflict, "请输入频道数字 ID 确认清除")
		return
	}
	err := service.Store.PurgeChannel(r.Context(), channel.ID, body.Confirm, wikiActor(r))
	s.wikiResponse(w, map[string]bool{"success": true}, err, true)
}
