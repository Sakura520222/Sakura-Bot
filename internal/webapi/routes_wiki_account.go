// Copyright 2026 Sakura-Bot
// SPDX-License-Identifier: AGPL-3.0-or-later

package webapi

import (
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/Sakura520222/Sakura-Bot/internal/wiki"
)

func (s *Server) registerWikiAccountRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/wiki/account/session", s.wikiAccountStatus)
	mux.HandleFunc("POST /api/wiki/account/start", s.wikiAccountStart)
	mux.HandleFunc("GET /api/wiki/account/callback", s.wikiAccountCallback)
	mux.HandleFunc("POST /api/wiki/account/refresh", s.wikiAccountRefresh)
	mux.HandleFunc("POST /api/wiki/account/logout", s.wikiAccountLogout)
	mux.HandleFunc("PUT /api/wiki/account/privacy", s.wikiAccountPrivacy)
	mux.HandleFunc("GET /api/wiki/account/avatar", s.wikiAccountAvatar)
	mux.HandleFunc("GET /api/wiki/account/bookmarks", s.wikiGetBookmarks)
	mux.HandleFunc("POST /api/wiki/account/bookmarks", s.wikiSaveBookmarks)
	mux.HandleFunc("GET /api/wiki/admin/status", s.wikiAdmin(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]bool{"admin": true})
	}))
	mux.HandleFunc("GET /api/wiki/admin/account-settings", s.wikiAdmin(s.withConfig(s.wikiAccountSettings)))
	mux.HandleFunc("PUT /api/wiki/admin/account-settings", s.wikiAdmin(s.withConfig(s.wikiSaveAccountSettings)))
	mux.HandleFunc("GET /api/wiki/admin/accounts", s.wikiAdmin(s.wikiListAccounts))
	mux.HandleFunc("PUT /api/wiki/admin/accounts/{user_id}/blocked", s.wikiAdmin(s.wikiBlockAccount))
}

func (s *Server) wikiAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := UserFromContext(r.Context())
		if !ok {
			writeHTTPError(w, http.StatusUnauthorized, "需要管理员登录")
			return
		}
		uid, _ := strconv.ParseInt(u.UserID, 10, 64)
		if !(u.IsDev || u.UserID == "admin" || (s.deps.Config != nil && s.deps.Config.IsAdmin(uid))) {
			writeHTTPError(w, http.StatusForbidden, "需要管理员权限")
			return
		}
		next(w, r)
	}
}

func (s *Server) wikiAccountsReady(w http.ResponseWriter) bool {
	if s.deps.WikiAccounts == nil || (s.deps.Wiki != nil && !s.deps.Wiki.Ready()) {
		writeHTTPError(w, http.StatusServiceUnavailable, "Wiki 服务尚未启动")
		return false
	}
	return true
}

func (s *Server) wikiLoginOnHost(r *http.Request) *wiki.LoginConfig {
	cfg := wiki.LoginConfigFrom(s.deps.Config)
	if cfg == nil {
		return nil
	}
	origin, err := url.Parse(cfg.Origin)
	if err != nil || !strings.EqualFold(r.Host, origin.Host) {
		return nil
	}
	return cfg
}

func (s *Server) wikiSameOrigin(w http.ResponseWriter, r *http.Request) *wiki.LoginConfig {
	cfg := s.wikiLoginOnHost(r)
	if cfg == nil {
		writeHTTPError(w, http.StatusNotFound, "Wiki 登录未启用")
		return nil
	}
	if r.Header.Get("Origin") != cfg.Origin || r.Header.Get("X-Wiki-Request") != "1" {
		writeHTTPError(w, http.StatusForbidden, "Wiki 请求来源无效")
		return nil
	}
	return cfg
}

func wikiCookie(r *http.Request, name string) string {
	cookie, err := r.Cookie(name)
	if err != nil {
		return ""
	}
	return cookie.Value
}

func setWikiCookie(w http.ResponseWriter, name, value string, age int) {
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: value, MaxAge: age, Path: "/",
		Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
}

func setWikiSession(w http.ResponseWriter, tokens *wiki.SessionTokens) {
	setWikiCookie(w, wiki.CookieAccess, tokens.Access, min(wiki.AccessTTLSeconds, tokens.RemainingSeconds))
	setWikiCookie(w, wiki.CookieRefresh, tokens.Refresh, tokens.RemainingSeconds)
	if tokens.Logout != "" {
		setWikiCookie(w, wiki.CookieLogout, tokens.Logout, tokens.RemainingSeconds)
	}
}

func (s *Server) wikiAccountError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, wiki.ErrAccountDenied):
		writeHTTPError(w, http.StatusUnauthorized, "Wiki 会话已失效")
	case errors.Is(err, wiki.ErrBookmarkLimitExceeded):
		writeHTTPError(w, http.StatusUnprocessableEntity, err.Error())
	default:
		s.logger.Error("Wiki 账户服务请求失败", "error", err)
		writeHTTPError(w, http.StatusInternalServerError, "Wiki 账户服务请求失败")
	}
}

func (s *Server) wikiRequireUser(w http.ResponseWriter, r *http.Request) *wiki.WikiUserSession {
	if s.wikiLoginOnHost(r) == nil {
		writeHTTPError(w, http.StatusUnauthorized, "请先登录 Wiki")
		return nil
	}
	if !s.wikiAccountsReady(w) {
		return nil
	}
	user, err := s.deps.WikiAccounts.GetSession(r.Context(), wikiCookie(r, wiki.CookieAccess), false)
	if err != nil {
		s.wikiAccountError(w, err)
		return nil
	}
	if user == nil {
		writeHTTPError(w, http.StatusUnauthorized, "请先登录 Wiki")
	}
	return user
}

func (s *Server) wikiAccountStatus(w http.ResponseWriter, r *http.Request) {
	if s.wikiLoginOnHost(r) == nil {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": false, "user": nil, "can_refresh": false})
		return
	}
	if !s.wikiAccountsReady(w) {
		return
	}
	user, err := s.deps.WikiAccounts.GetSession(r.Context(), wikiCookie(r, wiki.CookieAccess), false)
	if err != nil {
		s.wikiAccountError(w, err)
		return
	}
	_, hasRefresh := wiki.TokenHashBytes(wikiCookie(r, wiki.CookieRefresh))
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled": true, "user": user.PublicProfile(), "can_refresh": user == nil && hasRefresh,
		"assistant_enabled": wiki.LoginSettingsFrom(s.deps.Config).AssistantEnabled,
	})
}

func (s *Server) wikiAccountStart(w http.ResponseWriter, r *http.Request) {
	cfg := s.wikiSameOrigin(w, r)
	if cfg == nil || !s.wikiAccountsReady(w) {
		return
	}
	var body struct {
		ReturnTo       string `json:"return_to"`
		RefreshProfile bool   `json:"refresh_profile"`
	}
	body.ReturnTo = "/wiki"
	if readJSONStrict(r, &body, 1<<14) != nil || len(body.ReturnTo) > 2048 {
		writeHTTPError(w, http.StatusUnprocessableEntity, "Wiki 登录请求无效")
		return
	}
	var previous string
	if body.RefreshProfile {
		user := s.wikiRequireUser(w, r)
		if user == nil {
			return
		}
		previous = user.SessionID
	}
	state, nonce, verifier, browser, err := s.deps.WikiAccounts.BeginLogin(r.Context(), wikiCookie(r, wiki.CookieBrowser), body.ReturnTo, *cfg, previous)
	if err != nil {
		s.wikiAccountError(w, err)
		return
	}
	setWikiCookie(w, wiki.CookieBrowser, browser, wiki.FlowTTLSeconds)
	writeJSON(w, http.StatusOK, map[string]string{
		"authorization_url": wiki.AuthorizationURL(*cfg, state, nonce, verifier),
	})
}

func (s *Server) wikiAccountCallback(w http.ResponseWriter, r *http.Request) {
	cfg := s.wikiLoginOnHost(r)
	if cfg == nil {
		writeHTTPError(w, http.StatusNotFound, "Wiki 登录未启用")
		return
	}
	if !s.wikiAccountsReady(w) {
		return
	}
	params := r.URL.Query()
	reason := "expired"
	if len(params["state"]) == 1 {
		flow, err := s.deps.WikiAccounts.ConsumeLoginFlow(r.Context(), params.Get("state"), wikiCookie(r, wiki.CookieBrowser), *cfg)
		if err != nil {
			s.logger.Warn("读取 Wiki 登录状态失败")
			reason = "failed"
		} else if flow != nil {
			if params.Get("error") == "access_denied" {
				reason = "cancelled"
			} else if code := params.Get("code"); len(params["code"]) == 1 && len(code) > 0 && len(code) <= 2048 && !params.Has("error") {
				tokens, err := s.wikiFinishAccountLogin(r, *cfg, flow, code)
				if err == nil {
					setWikiSession(w, tokens)
					http.Redirect(w, r, cfg.Origin+wiki.SafeWikiPath(flow.RedirectPath), http.StatusSeeOther)
					return
				}
				reason = "failed"
				if errors.Is(err, wiki.ErrAccountDenied) {
					reason = "unavailable"
				}
			}
		}
	}
	http.Redirect(w, r, cfg.Origin+"/wiki?login_error="+url.QueryEscape(reason), http.StatusSeeOther)
}

func (s *Server) wikiFinishAccountLogin(r *http.Request, cfg wiki.LoginConfig, flow *wiki.LoginFlow, code string) (*wiki.SessionTokens, error) {
	ctx := r.Context()
	identity, err := wiki.ExchangeCode(ctx, cfg, code, flow.Verifier, flow.Nonce)
	if err != nil {
		var failure *wiki.TelegramLoginError
		if errors.As(err, &failure) {
			s.logger.Warn("Wiki Telegram 登录失败", "stage", failure.Stage, "reason", failure.Reason, "status", failure.Status)
		}
		return nil, err
	}
	avatar, mime := wiki.LoadProfileAvatar(ctx, s.deps.DirectBotAPI, s.deps.Config.QABotToken(), identity.TelegramID, identity.PictureURL)
	return s.deps.WikiAccounts.CompleteLogin(ctx, *identity, avatar, mime, flow.RefreshSession, wikiCookie(r, wiki.CookieLogout))
}

func (s *Server) wikiAccountRefresh(w http.ResponseWriter, r *http.Request) {
	if s.wikiSameOrigin(w, r) == nil || !s.wikiAccountsReady(w) {
		return
	}
	tokens, err := s.deps.WikiAccounts.RotateSession(r.Context(), wikiCookie(r, wiki.CookieRefresh))
	if err != nil {
		s.wikiAccountError(w, err)
		return
	}
	if tokens == nil {
		s.wikiAccountError(w, wiki.ErrAccountDenied)
		return
	}
	setWikiSession(w, tokens)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) wikiAccountLogout(w http.ResponseWriter, r *http.Request) {
	if s.wikiSameOrigin(w, r) == nil || !s.wikiAccountsReady(w) {
		return
	}
	err := s.deps.WikiAccounts.Logout(r.Context(), wikiCookie(r, wiki.CookieLogout), wikiCookie(r, wiki.CookieBrowser))
	if err != nil {
		s.wikiAccountError(w, err)
		return
	}
	for _, name := range []string{wiki.CookieAccess, wiki.CookieRefresh, wiki.CookieLogout, wiki.CookieBrowser} {
		setWikiCookie(w, name, "", -1)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) wikiAccountPrivacy(w http.ResponseWriter, r *http.Request) {
	if s.wikiSameOrigin(w, r) == nil {
		return
	}
	user := s.wikiRequireUser(w, r)
	if user == nil {
		return
	}
	var body struct {
		ShowAvatar             *bool `json:"show_avatar"`
		ShowUsername           *bool `json:"show_username"`
		AnonymousContributions *bool `json:"anonymous_contributions"`
	}
	if readJSONStrict(r, &body, 1<<14) != nil || body.ShowAvatar == nil || body.ShowUsername == nil || body.AnonymousContributions == nil {
		writeHTTPError(w, http.StatusUnprocessableEntity, "Wiki 隐私设置无效")
		return
	}
	prefs := wiki.PrivacyPreferences{
		ShowAvatar: *body.ShowAvatar, ShowUsername: *body.ShowUsername, AnonymousContributions: *body.AnonymousContributions,
	}
	if err := s.deps.WikiAccounts.UpdatePrivacy(r.Context(), user, wikiCookie(r, wiki.CookieAccess), prefs); err != nil {
		s.wikiAccountError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, prefs)
}

func (s *Server) wikiAccountAvatar(w http.ResponseWriter, r *http.Request) {
	user := s.wikiRequireUser(w, r)
	if user == nil {
		return
	}
	data, mime, err := s.deps.WikiAccounts.GetUserAvatar(r.Context(), user.ID, false)
	if err != nil {
		s.wikiAccountError(w, err)
		return
	}
	if len(data) == 0 {
		wikiNotFound(w)
		return
	}
	w.Header().Set("Content-Type", mime)
	_, _ = w.Write(data)
}

func (s *Server) wikiGetBookmarks(w http.ResponseWriter, r *http.Request) {
	user := s.wikiRequireUser(w, r)
	if user == nil {
		return
	}
	ids, err := s.deps.WikiAccounts.GetBookmarks(r.Context(), user.ID)
	if err != nil {
		s.wikiAccountError(w, err)
		return
	}
	if ids == nil {
		ids = []string{}
	}
	writeJSON(w, http.StatusOK, map[string][]string{"ids": ids})
}

func (s *Server) wikiSaveBookmarks(w http.ResponseWriter, r *http.Request) {
	if s.wikiSameOrigin(w, r) == nil {
		return
	}
	user := s.wikiRequireUser(w, r)
	if user == nil {
		return
	}
	var body struct {
		Add    []string `json:"add"`
		Remove *string  `json:"remove"`
	}
	if readJSONStrict(r, &body, 1<<16) != nil || len(body.Add) > 100 ||
		(body.Remove != nil && !wikiIDPattern.MatchString(*body.Remove)) ||
		slices.ContainsFunc(body.Add, func(v string) bool { return !wikiIDPattern.MatchString(v) }) {
		writeHTTPError(w, http.StatusUnprocessableEntity, "无效的书签 ID")
		return
	}
	remove := ""
	if body.Remove != nil {
		remove = *body.Remove
	}
	if err := s.deps.WikiAccounts.UpdateBookmarks(r.Context(), user, wikiCookie(r, wiki.CookieAccess), body.Add, remove); err != nil {
		s.wikiAccountError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) wikiAccountSettings(w http.ResponseWriter, _ *http.Request) {
	settings := wiki.LoginSettingsFrom(s.deps.Config)
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled": settings.Enabled, "origin": settings.Origin, "has_secret": settings.ClientSecret != "",
		"has_bot": s.deps.Config.QABotToken() != "", "assistant_enabled": settings.AssistantEnabled,
		"assistant_checkin": settings.AssistantCheckin,
	})
}

func (s *Server) wikiSaveAccountSettings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled          *bool   `json:"enabled"`
		Origin           *string `json:"origin"`
		ClientSecret     *string `json:"client_secret"`
		AssistantEnabled *bool   `json:"assistant_enabled"`
		AssistantCheckin *bool   `json:"assistant_checkin"`
	}
	if readJSONStrict(r, &body, 1<<14) != nil || body.Enabled == nil || body.Origin == nil ||
		body.AssistantEnabled == nil || body.AssistantCheckin == nil || len(*body.Origin) > 255 ||
		(body.ClientSecret != nil && len(*body.ClientSecret) > 1024) {
		writeHTTPError(w, http.StatusUnprocessableEntity, "Wiki 登录设置无效")
		return
	}
	if *body.AssistantEnabled && !*body.Enabled {
		writeHTTPError(w, http.StatusUnprocessableEntity, "启用小妲助手前，请先启用 Telegram 登录")
		return
	}
	origin := strings.TrimRight(strings.TrimSpace(*body.Origin), "/")
	errInvalid := errors.New("invalid wiki origin or credentials")
	err := s.deps.Config.UpdateConfig(func(doc map[string]any) error {
		raw := configObject(doc, "wiki_login")
		secret, exists := raw["client_secret"].(string)
		if !exists {
			secret = s.deps.Config.WikiTelegramClientSecret()
		}
		if body.ClientSecret != nil {
			secret = strings.TrimSpace(*body.ClientSecret)
			raw["client_secret"] = secret
		}
		if *body.Enabled && wiki.BuildLoginConfig(true, origin, secret, s.deps.Config.QABotToken()) == nil {
			return errInvalid
		}
		raw["origin"] = origin
		raw["enabled"] = *body.Enabled
		raw["assistant_enabled"] = *body.AssistantEnabled
		raw["assistant_checkin"] = *body.AssistantCheckin
		return nil
	})
	if errors.Is(err, errInvalid) {
		writeHTTPError(w, http.StatusUnprocessableEntity, "请配置有效的 HTTPS 站点地址、BotFather Client Secret 和助手 Bot")
		return
	}
	if err != nil {
		s.configWriteError(w, err)
		return
	}
	s.wikiAccountSettings(w, r)
}

func (s *Server) wikiListAccounts(w http.ResponseWriter, r *http.Request) {
	if !s.wikiAccountsReady(w) {
		return
	}
	q := wikiQuery{request: r}
	offset := q.number("offset", 0, 0, 100000)
	if !q.valid(w) {
		return
	}
	rows, err := s.deps.WikiAccounts.ListAccounts(r.Context(), offset)
	if rows == nil {
		rows = []map[string]any{}
	}
	s.wikiResponse(w, rows, err, true)
}

func (s *Server) wikiBlockAccount(w http.ResponseWriter, r *http.Request) {
	if !s.wikiAccountsReady(w) {
		return
	}
	id := r.PathValue("user_id")
	if !wikiIDPattern.MatchString(id) {
		writeHTTPError(w, http.StatusNotFound, "Wiki 用户不存在")
		return
	}
	var body struct {
		Blocked *bool `json:"blocked"`
	}
	if readJSONStrict(r, &body, 1<<14) != nil || body.Blocked == nil {
		writeHTTPError(w, http.StatusUnprocessableEntity, "Wiki 用户设置无效")
		return
	}
	updated, err := s.deps.WikiAccounts.SetBlocked(r.Context(), id, *body.Blocked)
	if err != nil {
		s.wikiAccountError(w, err)
		return
	}
	if !updated {
		writeHTTPError(w, http.StatusNotFound, "Wiki 用户不存在")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"blocked": *body.Blocked})
}
