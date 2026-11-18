// Copyright 2026 Sakura-Bot
// SPDX-License-Identifier: AGPL-3.0-or-later

package webapi

import (
	"errors"
	"net/http"

	"github.com/Sakura520222/Sakura-Bot/internal/wiki"
	"github.com/go-sql-driver/mysql"
)

func (s *Server) registerWikiCommunityRoutes(mux *http.ServeMux) {
	prefix := "/api/wiki/community"
	mux.HandleFunc("POST "+prefix+"/proposals", s.wikiSubmitProposal)
	mux.HandleFunc("GET "+prefix+"/proposals", s.wikiOwnProposals)
	mux.HandleFunc("DELETE "+prefix+"/proposals/{identifier}", s.wikiWithdrawProposal)
	mux.HandleFunc("GET "+prefix+"/contributors", s.wikiContributors)
	mux.HandleFunc("GET "+prefix+"/contributors/{identifier}", s.wikiContributors)
	mux.HandleFunc("GET "+prefix+"/contributors/{identifier}/avatar", s.wikiContributorAvatar)

	admin := "/api/wiki/admin"
	mux.HandleFunc("GET "+admin+"/proposals", s.wikiAdmin(s.wikiProposalQueue))
	mux.HandleFunc("GET "+admin+"/proposals/{identifier}", s.wikiAdmin(s.wikiProposalDetail))
	mux.HandleFunc("GET "+admin+"/pages/{page}/proposals", s.wikiAdmin(s.wikiProposalQueue))
	mux.HandleFunc("POST "+admin+"/proposals/{identifier}", s.wikiAdmin(s.wikiDecideProposal))
}

func (s *Server) wikiRuntime(w http.ResponseWriter, admin bool) *wiki.Runtime {
	if s.deps.Wiki == nil || !s.deps.Wiki.Ready() {
		detail := "Wiki 服务尚未启动"
		if admin && s.deps.Wiki != nil && s.deps.Wiki.StartupError() != "" {
			detail = s.deps.Wiki.StartupError()
		}
		writeHTTPError(w, http.StatusServiceUnavailable, detail)
		return nil
	}
	return s.deps.Wiki
}

func wikiActor(r *http.Request) string {
	u, _ := UserFromContext(r.Context())
	return u.UserID
}

func (s *Server) wikiProposalError(w http.ResponseWriter, err error) {
	var dbErr *mysql.MySQLError
	if errors.As(err, &dbErr) {
		s.wikiResponse(w, nil, err, false)
		return
	}
	writeHTTPError(w, http.StatusConflict, err.Error())
}

func (s *Server) wikiSubmitProposal(w http.ResponseWriter, r *http.Request) {
	if s.wikiSameOrigin(w, r) == nil {
		return
	}
	user := s.wikiRequireUser(w, r)
	if user == nil {
		return
	}
	service := s.wikiRuntime(w, false)
	if service == nil {
		return
	}
	var body struct {
		wiki.ProposalInput
		Channel string `json:"channel"`
		Page    string `json:"page"`
	}
	if readJSONStrict(r, &body, 1<<16) != nil || !wikiChannelIDPattern.MatchString(body.Channel) || !wikiIDPattern.MatchString(body.Page) {
		writeHTTPError(w, http.StatusUnprocessableEntity, "修订建议无效")
		return
	}
	if err := body.ProposalInput.ValidateAndNormalize(); err != nil {
		writeHTTPError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	id, notify, err := service.Community.Submit(r.Context(), user.ID, body.Channel, body.Page, body.ProposalInput)
	if err != nil {
		s.wikiProposalError(w, err)
		return
	}
	if notify {
		service.NotifyProposal(r.Context(), id)
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": id, "status": "pending"})
}

func (s *Server) wikiOwnProposals(w http.ResponseWriter, r *http.Request) {
	user := s.wikiRequireUser(w, r)
	if user == nil {
		return
	}
	service := s.wikiRuntime(w, false)
	if service == nil {
		return
	}
	q := wikiQuery{request: r}
	opts := wiki.ProposalQueryOptions{UserID: user.ID, Offset: q.number("offset", 0, 0, 100000)}
	if q.valid(w) {
		rows, err := service.Community.ListProposals(r.Context(), opts)
		s.wikiRows(w, rows, err)
	}
}

func (s *Server) wikiRows(w http.ResponseWriter, rows []map[string]any, err error) {
	if rows == nil {
		rows = []map[string]any{}
	}
	s.wikiResponse(w, rows, err, true)
}

func (s *Server) wikiWithdrawProposal(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("identifier")
	if !wikiIDPattern.MatchString(id) {
		wikiNotFound(w)
		return
	}
	if s.wikiSameOrigin(w, r) == nil {
		return
	}
	user := s.wikiRequireUser(w, r)
	if user == nil {
		return
	}
	service := s.wikiRuntime(w, false)
	if service == nil {
		return
	}
	found, err := service.Community.Withdraw(r.Context(), id, user.ID)
	if !found || err != nil {
		s.wikiResponse(w, nil, err, false)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) wikiContributors(w http.ResponseWriter, r *http.Request) {
	service := s.wikiRuntime(w, false)
	if service == nil {
		return
	}
	q := wikiQuery{request: r}
	id := r.PathValue("identifier")
	opts := wiki.ContributorsOptions{
		UserID: id, History: id != "", Channel: q.text("channel", "", 0),
		PageID: q.text("page", "", 0), Offset: q.number("offset", 0, 0, 100000),
	}
	if (id != "" && !wikiIDPattern.MatchString(id)) ||
		(opts.Channel != "" && !wikiChannelIDPattern.MatchString(opts.Channel)) ||
		(opts.PageID != "" && !wikiIDPattern.MatchString(opts.PageID)) {
		wikiNotFound(w)
		return
	}
	if !q.valid(w) {
		return
	}
	rows, err := service.Community.ListContributions(r.Context(), opts)
	if opts.History && opts.Offset == 0 && len(rows) == 0 {
		s.wikiResponse(w, nil, err, false)
		return
	}
	s.wikiRows(w, rows, err)
}

func (s *Server) wikiContributorAvatar(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("identifier")
	if !wikiIDPattern.MatchString(id) {
		wikiNotFound(w)
		return
	}
	service := s.wikiRuntime(w, false)
	if service == nil {
		return
	}
	rows, err := service.Community.ListContributions(r.Context(), wiki.ContributorsOptions{UserID: id})
	if err != nil || len(rows) == 0 || rows[0]["avatar"] != true {
		s.wikiResponse(w, nil, err, false)
		return
	}
	data, mime, err := service.Accounts.GetUserAvatar(r.Context(), id, true)
	if err != nil || len(data) == 0 {
		s.wikiResponse(w, nil, err, false)
		return
	}
	w.Header().Set("Content-Type", mime)
	_, _ = w.Write(data)
}

func (s *Server) wikiProposalQueue(w http.ResponseWriter, r *http.Request) {
	service := s.wikiRuntime(w, true)
	if service == nil {
		return
	}
	q := wikiQuery{request: r}
	opts := wiki.ProposalQueryOptions{
		Status: "pending", PageID: r.PathValue("page"), Offset: q.number("offset", 0, 0, 100000), Review: true,
	}
	if opts.PageID == "" {
		opts.Status = q.enum("status", "pending", "pending", "approved", "rejected", "returned", "withdrawn", "queued")
		if opts.Status == "queued" {
			opts.Status = ""
			opts.Queued = true
		}
	} else if !wikiIDPattern.MatchString(opts.PageID) {
		wikiNotFound(w)
		return
	}
	if q.valid(w) {
		rows, err := service.Community.ListProposals(r.Context(), opts)
		s.wikiRows(w, rows, err)
	}
}

func (s *Server) wikiProposalDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("identifier")
	if !wikiIDPattern.MatchString(id) {
		wikiNotFound(w)
		return
	}
	service := s.wikiRuntime(w, true)
	if service == nil {
		return
	}
	rows, err := service.Community.ListProposals(r.Context(), wiki.ProposalQueryOptions{ProposalID: id, Review: true})
	if err != nil || len(rows) == 0 {
		s.wikiResponse(w, nil, err, false)
		return
	}
	writeJSON(w, http.StatusOK, rows[0])
}

func (s *Server) wikiDecideProposal(w http.ResponseWriter, r *http.Request) {
	service := s.wikiRuntime(w, true)
	if service == nil {
		return
	}
	var body struct {
		Action string `json:"action"`
		Note   string `json:"note"`
	}
	id := r.PathValue("identifier")
	if readJSONStrict(r, &body, 1<<14) != nil || len([]rune(body.Note)) > 1000 ||
		(body.Action != "approved" && body.Action != "rejected" && body.Action != "returned") {
		writeHTTPError(w, http.StatusUnprocessableEntity, "修订建议操作无效")
		return
	}
	if !wikiIDPattern.MatchString(id) {
		wikiNotFound(w)
		return
	}
	data, err := service.Community.Decide(r.Context(), id, body.Action, wikiActor(r), body.Note, false)
	if err != nil {
		s.wikiProposalError(w, err)
		return
	}
	service.Wake()
	writeJSON(w, http.StatusOK, data)
}
