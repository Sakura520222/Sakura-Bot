// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package webapi

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

var (
	publicPaths = map[string]bool{
		"/api/auth/login":             true,
		"/api/auth/telegram-callback": true,
		"/api/auth/status":            true,
		"/api/health":                 true,
		"/api/docs":                   true,
		"/api/redoc":                  true,
		"/api/openapi.json":           true,
		"/openapi.json":               true,
	}

	publicWikiRouteRE = regexp.MustCompile(`^/api/public/wiki/(?:channels|visibility|feed|search|sitemap|bookmarks|channels/[A-Za-z0-9_-]{1,64}(?:/(?:profile|timeline|graph|changes|avatar|cover|pages/[A-Za-z0-9_-]{1,180}(?:/compare)?))?)$`)
	wikiPreviewRE     = regexp.MustCompile(`^/wiki-preview/[A-Za-z0-9_-]{1,64}(?:/[A-Za-z0-9_-]{1,180})?$`)
	wikiAvatarRouteRE = regexp.MustCompile(`^/api/public/wiki/channels/[A-Za-z0-9_-]{1,64}/avatar$`)
	wikiTurnRouteRE   = regexp.MustCompile(`^/api/wiki/assistant/chat/[a-f0-9]{32}$`)
	wikiCommunityRE   = regexp.MustCompile(`^/api/wiki/community/(?:contributors/[a-f0-9]{32}(?:/avatar)?|proposals/[a-f0-9]{32})$`)
)

// IsPublicWikiReadRequest 判断请求是否为匿名公开 Wiki 只读路径（对齐 wiki.py:is_public_wiki_request）。
func IsPublicWikiReadRequest(method, path string) bool {
	if method != http.MethodGet && method != http.MethodHead {
		return false
	}
	return publicWikiRouteRE.MatchString(path) || wikiPreviewRE.MatchString(path)
}

// IsWikiFirstPartyCookieRequest 判断请求是否属于第一方 Cookie 保护的 Wiki 账户/助手/社区路径。
func IsWikiFirstPartyCookieRequest(method, path string) bool {
	switch path {
	case "/api/wiki/account/session":
		return method == http.MethodGet
	case "/api/wiki/account/start", "/api/wiki/account/refresh", "/api/wiki/account/logout", "/api/wiki/account/bookmarks":
		if path == "/api/wiki/account/bookmarks" && method == http.MethodGet {
			return true
		}
		return method == http.MethodPost
	case "/api/wiki/account/callback", "/api/wiki/account/avatar":
		return method == http.MethodGet
	case "/api/wiki/account/privacy":
		return method == http.MethodPut
	case "/api/wiki/assistant/status":
		return method == http.MethodGet
	case "/api/wiki/assistant/checkin", "/api/wiki/assistant/chat":
		return method == http.MethodPost
	case "/api/wiki/community/proposals":
		return method == http.MethodGet || method == http.MethodPost
	case "/api/wiki/community/contributors":
		return method == http.MethodGet
	}
	if wikiTurnRouteRE.MatchString(path) && (method == http.MethodGet || method == http.MethodDelete) {
		return true
	}
	if wikiCommunityRE.MatchString(path) && (method == http.MethodGet || method == http.MethodDelete) {
		return true
	}
	return false
}

// ────────────────────────────── JSON 响应与读取辅助工具 ──────────────────────────────

func writeJSON(w http.ResponseWriter, status int, payload any) {
	b, err := json.Marshal(payload)
	if err != nil {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"detail":"internal serialization error"}`))
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

func writeEnvelopeOK(w http.ResponseWriter, data any, message string) {
	resp := map[string]any{"success": true}
	if data != nil {
		resp["data"] = data
	}
	if message != "" {
		resp["message"] = message
	}
	writeJSON(w, http.StatusOK, resp)
}

func writeEnvelopeFail(w http.ResponseWriter, message string, data any) {
	resp := map[string]any{
		"success": false,
		"message": message,
	}
	if data != nil {
		resp["data"] = data
	}
	writeJSON(w, http.StatusOK, resp)
}

func writeHTTPError(w http.ResponseWriter, status int, detail any) {
	writeJSON(w, status, map[string]any{"detail": detail})
}

func readJSONBody(r *http.Request, dst any, maxBytes int64) error {
	if maxBytes <= 0 {
		maxBytes = 2 * 1024 * 1024
	}
	defer r.Body.Close() //nolint:errcheck
	limited := io.LimitReader(r.Body, maxBytes+1)
	raw, err := io.ReadAll(limited)
	if err != nil {
		return err
	}
	if int64(len(raw)) > maxBytes {
		return fmt.Errorf("request body exceeds %d bytes", maxBytes)
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	return json.Unmarshal(raw, dst)
}

func readJSONStrict(r *http.Request, dst any, maxBytes int64) error {
	if maxBytes <= 0 {
		maxBytes = 2 * 1024 * 1024
	}
	defer r.Body.Close() //nolint:errcheck
	limited := io.LimitReader(r.Body, maxBytes+1)
	raw, err := io.ReadAll(limited)
	if err != nil {
		return err
	}
	if int64(len(raw)) > maxBytes {
		return fmt.Errorf("request body exceeds %d bytes", maxBytes)
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return fmt.Errorf("request body must be a JSON object")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	if dec.Decode(new(json.RawMessage)) != io.EOF {
		return fmt.Errorf("request body contains trailing data")
	}
	return nil
}

// ────────────────────────────── ResponseWriter 包装器（保留 Flusher） ──────────────────────────────

type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
	onBeforeHdr func(h http.Header, status int)
}

func (sr *statusRecorder) WriteHeader(code int) {
	if sr.wroteHeader {
		return
	}
	sr.status = code
	sr.wroteHeader = true
	if sr.onBeforeHdr != nil {
		sr.onBeforeHdr(sr.Header(), code)
	}
	sr.ResponseWriter.WriteHeader(code)
}

func (sr *statusRecorder) Write(p []byte) (int, error) {
	if !sr.wroteHeader {
		sr.WriteHeader(http.StatusOK)
	}
	return sr.ResponseWriter.Write(p)
}

func (sr *statusRecorder) Flush() {
	if !sr.wroteHeader {
		sr.WriteHeader(http.StatusOK)
	}
	if fl, ok := sr.ResponseWriter.(http.Flusher); ok {
		fl.Flush()
	}
}

type etagBufferWriter struct {
	header http.Header
	status int
	buf    bytes.Buffer
}

func newETagBufferWriter() *etagBufferWriter {
	return &etagBufferWriter{
		header: make(http.Header),
		status: http.StatusOK,
	}
}

func (bw *etagBufferWriter) Header() http.Header  { return bw.header }
func (bw *etagBufferWriter) WriteHeader(code int) { bw.status = code }
func (bw *etagBufferWriter) Write(p []byte) (int, error) {
	return bw.buf.Write(p)
}

// ────────────────────────────── 中间件链 ──────────────────────────────

func (s *Server) wrapMiddlewares(router http.Handler) http.Handler {
	withCORS := s.corsMiddleware(router)
	withAuth := s.authMiddleware(withCORS)
	return s.accessLogMiddleware(withAuth)
}

func (s *Server) accessLogMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		defer func() {
			if recov := recover(); recov != nil {
				if r.URL.Path == "/api/wiki/account/callback" {
					s.logger.Warn("Wiki Telegram 回调异常")
				} else {
					s.logger.Error("WebUI 请求处理 panic", "path", r.URL.Path, "panic", fmt.Sprintf("%v", recov))
				}
				if !rec.wroteHeader {
					writeHTTPError(rec, http.StatusInternalServerError, "服务器内部错误")
				}
			}
			elapsedMS := float64(time.Since(started).Microseconds()) / 1000.0
			msg := fmt.Sprintf("WebUI 请求: %s %s status=%d client=%s elapsed=%.1fms",
				r.Method, r.URL.Path, rec.status, clientSocketIP(r), elapsedMS)
			if r.URL.Path == "/api/health" {
				s.logger.Debug(msg)
			} else {
				s.logger.Info(msg)
			}
		}()
		next.ServeHTTP(rec, r)
	})
}

func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "" {
			origin = "*"
		}
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS, HEAD")
		reqHdrs := r.Header.Get("Access-Control-Request-Headers")
		if reqHdrs == "" {
			reqHdrs = "Authorization, Content-Type, X-Wiki-Request, If-None-Match"
		}
		w.Header().Set("Access-Control-Allow-Headers", reqHdrs)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func stripCORSHeaders(h http.Header) {
	for k := range h {
		if strings.HasPrefix(strings.ToLower(k), "access-control-") {
			h.Del(k)
		}
	}
}

func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		now := time.Now().UTC()

		// 1. 内部 Wiki 搜索/网页抓取桥接：跳过 WebUI JWT，由路由自行校验内部 HMAC Bearer
		if r.Method == http.MethodPost && path == "/api/internal/wiki-research" {
			w.Header().Set("Cache-Control", "no-store")
			next.ServeHTTP(w, r)
			return
		}

		// 2. 第一方 Cookie 保护的 Wiki 账户 / 助手 / 社区接口（剥离 CORS 响应头并注入安全响应头）
		if IsWikiFirstPartyCookieRequest(r.Method, path) {
			if strings.HasSuffix(path, "/start") || strings.HasSuffix(path, "/callback") {
				if !s.wikiLoginRL.Allow(clientSocketIP(r), now) {
					w.Header().Set("Cache-Control", "no-store")
					w.Header().Set("Retry-After", "60")
					writeHTTPError(w, http.StatusTooManyRequests, "Wiki 登录请求过于频繁")
					return
				}
			}
			rec := &statusRecorder{
				ResponseWriter: w,
				status:         http.StatusOK,
				onBeforeHdr: func(h http.Header, _ int) {
					stripCORSHeaders(h)
					h.Set("Cache-Control", "no-store")
					h.Set("Referrer-Policy", "no-referrer")
					h.Set("X-Content-Type-Options", "nosniff")
					h.Set("Cross-Origin-Resource-Policy", "same-origin")
					h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
				},
			}
			next.ServeHTTP(rec, r)
			return
		}

		// 3. 公开只读 Wiki 接口与爬虫预览（120/min IP 限流 + ETag 条件缓存）
		if IsPublicWikiReadRequest(r.Method, path) {
			if !s.wikiPublicRL.Allow(clientSocketIP(r), now) {
				w.Header().Set("Cache-Control", "no-store")
				w.Header().Set("Retry-After", "60")
				writeHTTPError(w, http.StatusTooManyRequests, "Wiki 请求过于频繁")
				return
			}
			bw := newETagBufferWriter()
			next.ServeHTTP(bw, r)

			for k, vals := range bw.header {
				for _, v := range vals {
					w.Header().Add(k, v)
				}
			}
			w.Header().Set("X-Content-Type-Options", "nosniff")
			if !strings.HasPrefix(path, "/wiki-preview/") {
				w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
			}

			ct := bw.header.Get("Content-Type")
			status := bw.status
			bodyBytes := bw.buf.Bytes()

			if status == http.StatusOK && strings.HasPrefix(ct, "application/json") {
				sum := sha256.Sum256(bodyBytes)
				hexTag := hex.EncodeToString(sum[:])
				etag := fmt.Sprintf(`W/"%s"`, hexTag)
				w.Header().Set("ETag", etag)
				w.Header().Set("Cache-Control", "private, no-cache, must-revalidate")
				if matchesIfNoneMatch(r.Header.Get("If-None-Match"), etag) {
					w.WriteHeader(http.StatusNotModified)
					return
				}
			} else if wikiAvatarRouteRE.MatchString(path) && (status == http.StatusOK || status == http.StatusNotModified) {
				w.Header().Set("Cache-Control", "private, no-cache, must-revalidate")
			} else if (status != http.StatusOK && status != http.StatusNotModified) || w.Header().Get("ETag") == "" {
				w.Header().Set("Cache-Control", "no-store")
			}

			w.WriteHeader(status)
			if r.Method != http.MethodHead && status != http.StatusNotModified {
				_, _ = w.Write(bodyBytes)
			}
			return
		}

		// 4. 非 /api 静态资源与 SPA 路径免鉴权直通
		if !strings.HasPrefix(path, "/api") {
			next.ServeHTTP(w, r)
			return
		}

		// 5. 公开 API 白名单与 OPTIONS 预检直通
		if publicPaths[path] || r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}

		// 6. 管理端 JWT 认证校验
		rawTok := ExtractBearerOrQueryToken(r)
		if rawTok == "" {
			writeHTTPError(w, http.StatusUnauthorized, "未提供认证 Token")
			return
		}
		user, err := VerifyAccessToken(rawTok, s.botToken(), s.devMode(), now)
		if err != nil {
			writeHTTPError(w, http.StatusUnauthorized, "Token 无效或已过期")
			return
		}
		if strings.HasPrefix(path, "/api/wiki/admin") {
			w.Header().Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r.WithContext(ContextWithUser(r.Context(), user)))
	})
}

func matchesIfNoneMatch(headerVal string, etag string) bool {
	if strings.TrimSpace(headerVal) == "" {
		return false
	}
	strongPart := strings.TrimPrefix(etag, "W/")
	for _, item := range strings.Split(headerVal, ",") {
		v := strings.TrimPrefix(strings.TrimSpace(item), "W/")
		if v == "*" || v == strongPart {
			return true
		}
	}
	return false
}
