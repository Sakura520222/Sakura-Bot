// Copyright 2026 Sakura-Bot
// SPDX-License-Identifier: AGPL-3.0-or-later

package webapi

import (
	"math"
	"net/http"

	"github.com/Sakura520222/Sakura-Bot/internal/db"
)

func (s *Server) registerStatsRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/stats", s.handleStats)
	mux.HandleFunc("GET /api/stats/summaries", s.handleStatsSummaries)
	mux.HandleFunc("GET /api/stats/ranking", s.handleStatsRanking)
	mux.HandleFunc("GET /api/stats/export", s.handleStatsExport)
}

func (s *Server) statsDB(w http.ResponseWriter) *db.Manager {
	store := s.deps.DB
	if store == nil || store.DB == nil {
		writeEnvelopeFail(w, "数据库未连接", nil)
		return nil
	}
	return store
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	store := s.statsDB(w)
	if store == nil {
		return
	}
	data, err := store.GetWebStats(r.Context())
	if err != nil {
		s.replyDatabaseError(w, err)
		return
	}
	writeEnvelopeOK(w, data, "")
}

func (s *Server) handleStatsSummaries(w http.ResponseWriter, r *http.Request) {
	query := wikiQuery{request: r}
	limit := query.number("limit", 20, 1, 100)
	offset := query.number("offset", 0, 0, math.MaxInt)
	channel := query.text("channel", "", 0)
	if !query.valid(w) {
		return
	}
	store := s.statsDB(w)
	if store == nil {
		return
	}
	rows, total, err := store.ListSummaries(r.Context(), channel, limit, offset)
	if err != nil {
		s.replyDatabaseError(w, err)
		return
	}
	if rows == nil {
		rows = []db.SummaryRecord{}
	}
	writeEnvelopeOK(w, map[string]any{
		"summaries": rows,
		"total":     total,
		"limit":     limit,
		"offset":    offset,
	}, "")
}

func (s *Server) handleStatsRanking(w http.ResponseWriter, r *http.Request) {
	store := s.statsDB(w)
	if store == nil {
		return
	}
	rows, err := store.GetChannelRanking(r.Context())
	if err != nil {
		s.replyDatabaseError(w, err)
		return
	}
	if rows == nil {
		rows = []db.ChannelRankRow{}
	}
	writeEnvelopeOK(w, rows, "")
}

func (s *Server) handleStatsExport(w http.ResponseWriter, r *http.Request) {
	store := s.statsDB(w)
	if store == nil {
		return
	}
	summaries, err := store.ExportSummaries(r.Context())
	if err != nil {
		s.replyDatabaseError(w, err)
		return
	}
	if summaries == nil {
		summaries = []map[string]any{}
	}
	writeEnvelopeOK(w, summaries, "")
}
