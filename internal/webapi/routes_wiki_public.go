// Copyright 2026 Sakura-Bot
// SPDX-License-Identifier: AGPL-3.0-or-later

package webapi

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/Sakura520222/Sakura-Bot/internal/wiki"
)

var wikiIDPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)

type wikiQuery struct {
	request *http.Request
	err     string
}

func (q *wikiQuery) text(key, fallback string, limit int) string {
	value := q.request.URL.Query().Get(key)
	if value == "" {
		return fallback
	}
	if limit > 0 && utf8.RuneCountInString(value) > limit {
		q.err = key
	}
	return value
}

func (q *wikiQuery) enum(key, fallback string, options ...string) string {
	value := q.text(key, fallback, 0)
	if !slices.Contains(options, value) {
		q.err = key
	}
	return value
}

func (q *wikiQuery) number(key string, fallback, min, max int) int {
	raw := q.text(key, "", 0)
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v < min || v > max {
		q.err = key
	}
	return v
}

func (q *wikiQuery) id(key string, required bool) string {
	v := q.text(key, "", 0)
	if (v != "" || required) && !wikiIDPattern.MatchString(v) {
		q.err = key
	}
	return v
}

func (q *wikiQuery) date(key string) *time.Time {
	raw := q.text(key, "", 0)
	if raw == "" {
		return nil
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05", time.DateOnly} {
		if value, err := time.Parse(layout, raw); err == nil {
			return &value
		}
	}
	q.err = key
	return nil
}

func (q *wikiQuery) valid(w http.ResponseWriter) bool {
	if q.err != "" {
		writeHTTPError(w, http.StatusUnprocessableEntity, "无效的查询参数: "+q.err)
		return false
	}
	return true
}

func (s *Server) registerPublicWikiRoutes(mux *http.ServeMux) {
	for path, fn := range map[string]http.HandlerFunc{
		"/channels":                                s.publicWikiChannels,
		"/visibility":                              s.publicWikiVisibility,
		"/bookmarks":                               s.publicWikiBookmarks,
		"/feed":                                    s.publicWikiFeed,
		"/search":                                  s.publicWikiSearch,
		"/sitemap":                                 s.publicWikiSitemap,
		"/channels/{channel}":                      s.publicWikiChannel,
		"/channels/{channel}/profile":              s.publicWikiProfile,
		"/channels/{channel}/timeline":             s.publicWikiTimeline,
		"/channels/{channel}/graph":                s.publicWikiGraph,
		"/channels/{channel}/changes":              s.publicWikiChanges,
		"/channels/{channel}/avatar":               s.publicWikiAvatar,
		"/channels/{channel}/cover":                s.publicWikiCover,
		"/channels/{channel}/pages/{slug}":         s.publicWikiPage,
		"/channels/{channel}/pages/{slug}/compare": s.publicWikiCompare,
	} {
		mux.HandleFunc("GET /api/public/wiki"+path, s.requirePublicWiki(fn))
	}
	mux.HandleFunc("GET /wiki-preview/{channel}", s.requirePublicWiki(s.publicWikiPreview))
	mux.HandleFunc("GET /wiki-preview/{channel}/{slug}", s.requirePublicWiki(s.publicWikiPreview))
}

func (s *Server) requirePublicWiki(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !IsPublicWikiReadRequest(r.Method, r.URL.Path) {
			wikiNotFound(w)
			return
		}
		if s.deps.WikiReader == nil || (s.deps.Wiki != nil && !s.deps.Wiki.Ready()) {
			writeHTTPError(w, http.StatusServiceUnavailable, "Wiki 服务尚未启动")
			return
		}
		next(w, r)
	}
}

func wikiNotFound(w http.ResponseWriter) {
	writeHTTPError(w, http.StatusNotFound, "Wiki 内容不存在或尚未公开")
}

func (s *Server) wikiResponse(w http.ResponseWriter, value any, err error, found bool) {
	switch {
	case errors.Is(err, wiki.ErrInvalidCursor):
		writeHTTPError(w, http.StatusBadRequest, err.Error())
	case err != nil:
		s.logger.Error("Wiki 请求失败", "error", err)
		writeHTTPError(w, http.StatusInternalServerError, "Wiki 请求失败")
	case !found:
		wikiNotFound(w)
	default:
		writeJSON(w, http.StatusOK, value)
	}
}

func (s *Server) publicWikiChannels(w http.ResponseWriter, r *http.Request) {
	rows, err := s.deps.WikiReader.Channels(r.Context())
	if rows == nil {
		rows = []map[string]any{}
	}
	s.wikiResponse(w, rows, err, true)
}

func (s *Server) publicWikiVisibility(w http.ResponseWriter, r *http.Request) {
	data, err := s.deps.WikiReader.Visibility(r.Context())
	s.wikiResponse(w, data, err, true)
}

func (s *Server) publicWikiBookmarks(w http.ResponseWriter, r *http.Request) {
	ids := r.URL.Query()["id"]
	if len(ids) == 0 || len(ids) > 50 || slices.ContainsFunc(ids, func(v string) bool { return !wikiIDPattern.MatchString(v) }) {
		writeHTTPError(w, http.StatusUnprocessableEntity, "无效的书签 ID")
		return
	}
	items, err := s.deps.WikiReader.Bookmarks(r.Context(), ids)
	if items == nil {
		items = []map[string]any{}
	}
	s.wikiResponse(w, map[string]any{"items": items}, err, true)
}

func (s *Server) publicWikiChannel(w http.ResponseWriter, r *http.Request) {
	summary := false
	if raw := r.URL.Query().Get("summary"); raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			writeHTTPError(w, http.StatusUnprocessableEntity, "无效的查询参数: summary")
			return
		}
		summary = value
	}
	data, err := s.deps.WikiReader.Channel(r.Context(), r.PathValue("channel"), summary)
	s.wikiResponse(w, data, err, data != nil)
}

func (s *Server) publicWikiProfile(w http.ResponseWriter, r *http.Request) {
	data, err := s.deps.WikiReader.Profile(r.Context(), r.PathValue("channel"))
	s.wikiResponse(w, data, err, data != nil)
}

func (s *Server) publicWikiFeed(w http.ResponseWriter, r *http.Request) {
	q := wikiQuery{request: r}
	opts := wiki.FeedOptions{
		Mode:  q.enum("mode", "latest", "latest", "recommended", "search"),
		Query: q.text("q", "", 200), Channel: q.text("channel", "", 64),
		Kind: q.text("kind", "", 16), Topic: q.text("topic", "", 160),
		After: q.date("after"), Before: q.date("before"),
		Cursor: q.text("cursor", "", 512), Limit: q.number("limit", 20, 1, 50),
	}
	if q.valid(w) {
		data, err := s.deps.WikiReader.Feed(r.Context(), opts)
		s.wikiResponse(w, data, err, data != nil)
	}
}

func (s *Server) publicWikiSearch(w http.ResponseWriter, r *http.Request) {
	q := wikiQuery{request: r}
	opts := wiki.SearchOptions{
		Query: q.text("q", "", 200), Channel: q.text("channel", "", 0),
		Page: q.number("page", 1, 1, 10000), PageSize: q.number("page_size", 20, 1, 50),
		Kind: q.text("kind", "", 16), Topic: q.text("topic", "", 160),
		After: q.date("after"), Before: q.date("before"),
	}
	if q.valid(w) {
		data, err := s.deps.WikiReader.Search(r.Context(), opts)
		s.wikiResponse(w, data, err, data != nil)
	}
}

func (s *Server) publicWikiSitemap(w http.ResponseWriter, r *http.Request) {
	q := wikiQuery{request: r}
	page := q.number("page", 1, 1, 10000)
	if q.valid(w) {
		data, err := s.deps.WikiReader.Sitemap(r.Context(), page)
		s.wikiResponse(w, data, err, data != nil)
	}
}

func (s *Server) publicWikiTimeline(w http.ResponseWriter, r *http.Request) {
	q := wikiQuery{request: r}
	opts := wiki.TimelineOptions{
		Page: q.number("page", 1, 1, 10000), PageSize: q.number("page_size", 20, 1, 50),
		CursorMode: q.enum("pagination", "page", "page", "cursor") == "cursor",
		Cursor:     q.text("cursor", "", 512), Before: q.date("before"),
	}
	if q.valid(w) {
		data, err := s.deps.WikiReader.Timeline(r.Context(), r.PathValue("channel"), opts)
		s.wikiResponse(w, data, err, data != nil)
	}
}

func (s *Server) publicWikiChanges(w http.ResponseWriter, r *http.Request) {
	q := wikiQuery{request: r}
	page, size := q.number("page", 1, 1, 10000), q.number("page_size", 20, 1, 50)
	if !q.valid(w) {
		return
	}
	channel := r.PathValue("channel")
	ch, err := s.deps.WikiReader.Channel(r.Context(), channel, true)
	if err != nil || ch == nil {
		s.wikiResponse(w, nil, err, false)
		return
	}
	data, err := s.deps.WikiReader.Changes(r.Context(), channel, page, size)
	s.wikiResponse(w, data, err, data != nil)
}

func (s *Server) publicWikiGraph(w http.ResponseWriter, r *http.Request) {
	q := wikiQuery{request: r}
	pagination := q.enum("pagination", "legacy", "legacy", "cursor")
	opts := wiki.GraphOptions{
		NodeID: q.id("node_id", false), Depth: q.number("depth", 1, 1, 2),
		Limit: q.number("limit", 60, 1, 120), Kind: q.text("kind", "", 16),
		Topic: q.text("topic", "", 160), Relation: q.text("relation", "", 32),
		Query: q.text("q", "", 200), After: q.date("after"), Before: q.date("before"),
		Cursor: q.text("cursor", "", 512),
	}
	if !q.valid(w) {
		return
	}
	channel := r.PathValue("channel")
	if pagination == "cursor" {
		data, err := s.deps.WikiReader.BrowseGraph(r.Context(), channel, opts)
		s.wikiResponse(w, data, err, data != nil)
		return
	}
	data, err := s.deps.WikiReader.Graph(r.Context(), channel, opts)
	s.wikiResponse(w, data, err, data != nil)
}

func (s *Server) publicWikiPage(w http.ResponseWriter, r *http.Request) {
	q := wikiQuery{request: r}
	opts := wiki.PageReadOptions{
		Part:     q.enum("part", "full", "full", "article", "references", "related"),
		Revision: q.id("revision", false),
	}
	if !q.valid(w) {
		return
	}
	channel, slug := r.PathValue("channel"), r.PathValue("slug")
	if opts.Part == "related" {
		data, err := s.deps.WikiReader.Related(r.Context(), channel, slug)
		s.wikiResponse(w, data, err, data != nil)
		return
	}
	data, err := s.deps.WikiReader.Page(r.Context(), channel, slug, opts)
	s.wikiResponse(w, data, err, data != nil)
}

func (s *Server) publicWikiCompare(w http.ResponseWriter, r *http.Request) {
	q := wikiQuery{request: r}
	opts := wiki.PageReadOptions{CompareBefore: q.id("before", true), CompareAfter: q.id("after", true)}
	if q.valid(w) {
		data, err := s.deps.WikiReader.Page(r.Context(), r.PathValue("channel"), r.PathValue("slug"), opts)
		s.wikiResponse(w, data, err, data != nil)
	}
}

func (s *Server) publicWikiAvatar(w http.ResponseWriter, r *http.Request) {
	if s.deps.WikiAvatars == nil {
		wikiNotFound(w)
		return
	}
	channel, err := s.deps.WikiReader.Channel(r.Context(), r.PathValue("channel"), true)
	if err != nil || channel == nil {
		s.wikiResponse(w, nil, err, false)
		return
	}
	id, _ := channel["id"].(string)
	if id == "" {
		wikiNotFound(w)
		return
	}
	data, mime, photoID, ok := s.deps.WikiAvatars.Get(r.Context(), id)
	if !ok {
		wikiNotFound(w)
		return
	}
	writeWikiImage(w, r, mime, fmt.Sprintf(`"av-%d"`, photoID), data)
}

func (s *Server) publicWikiCover(w http.ResponseWriter, r *http.Request) {
	if s.deps.WikiStore == nil {
		wikiNotFound(w)
		return
	}
	q := wikiQuery{request: r}
	version := q.text("v", "", 64)
	if !q.valid(w) {
		return
	}
	digest, data, err := s.deps.WikiStore.GetPublicCover(r.Context(), r.PathValue("channel"), version)
	if err != nil {
		s.wikiResponse(w, nil, err, false)
		return
	}
	mime := wiki.SniffMediaType(data)
	if mime == "" {
		wikiNotFound(w)
		return
	}
	writeWikiImage(w, r, mime, fmt.Sprintf(`"%s"`, digest), data)
}

func writeWikiImage(w http.ResponseWriter, r *http.Request, mime, etag string, image []byte) {
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "private, no-cache, must-revalidate")
	if matchesIfNoneMatch(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Content-Length", strconv.Itoa(len(image)))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(image)
	}
}

func (s *Server) publicWikiPreview(w http.ResponseWriter, r *http.Request) {
	body, err := s.deps.WikiReader.RenderPreviewHTML(r.Context(), r.PathValue("channel"), r.PathValue("slug"))
	if err != nil || body == "" {
		s.wikiResponse(w, nil, err, false)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
	_, _ = w.Write([]byte(body))
}
