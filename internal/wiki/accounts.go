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
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const (
	// __Host- 前缀 Cookie 常量（要求 Secure=true、Path="/"、无 Domain 属性，防止子域注入覆盖）。
	CookieAccess  = "__Host-sakura-wiki-access"
	CookieRefresh = "__Host-sakura-wiki-refresh"
	CookieLogout  = "__Host-sakura-wiki-logout"
	CookieBrowser = "__Host-sakura-wiki-login"

	// FlowTTLSeconds 是 OIDC 浏览器绑定登录流有效期（10 分钟）。
	FlowTTLSeconds = 600
	// AccessTTLSeconds 是短期访问令牌有效期（15 分钟）。
	AccessTTLSeconds = 900
	// RefreshTTLSeconds 是刷新令牌与登出令牌有效期（7 天）。
	RefreshTTLSeconds = 7 * 24 * 3600
	// MaxBookmarksPerUser 是单个读者账户最多允许保存的书签上限（500 条）。
	MaxBookmarksPerUser = 500
)

var (
	// ErrAccountDenied 表示账户已被封禁、不可变 sub 冲突或会话已过期失效。
	ErrAccountDenied = errors.New("Wiki account is unavailable")
	// ErrBookmarkLimitExceeded 表示用户书签总数超出 500 条上限。
	ErrBookmarkLimitExceeded = errors.New("书签数量不能超过 500 条")

	reSessionToken   = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
	reSafeWikiPath   = regexp.MustCompile(`^/wiki(?:/[A-Za-z0-9_%/-]*)?$`)
	reWikiPathPrefix = regexp.MustCompile(`^/wiki(?:/|$)`)
)

// LoginFlow 表示一次性消费通过后的 OIDC PKCE 登录上下文。
type LoginFlow struct {
	Nonce          string
	Verifier       string
	RedirectPath   string
	RefreshSession string
}

// PrivacyPreferences 表示读者公开署名隐私偏好设置。
type PrivacyPreferences struct {
	ShowAvatar             bool `json:"show_avatar"`
	ShowUsername           bool `json:"show_username"`
	AnonymousContributions bool `json:"anonymous_contributions"`
}

// WikiUserSession 表示已验证有效的第一方 Wiki 读者会话及用户资料。
type WikiUserSession struct {
	SessionID              string
	RefreshUntil           time.Time
	ID                     string
	TelegramID             int64
	DisplayName            string
	Username               string
	HasAvatar              bool
	ShowAvatar             bool
	ShowUsername           bool
	AnonymousContributions bool
}

// PublicProfile 生成返回给前端 /api/wiki/account/session 的脱敏读者资料。
func (u *WikiUserSession) PublicProfile() map[string]any {
	if u == nil {
		return nil
	}
	var uname any
	if u.Username != "" {
		uname = u.Username
	}
	return map[string]any{
		"id":           u.ID,
		"display_name": u.DisplayName,
		"username":     uname,
		"avatar":       u.HasAvatar,
		"privacy": map[string]any{
			"show_avatar":             u.ShowAvatar,
			"show_username":           u.ShowUsername,
			"anonymous_contributions": u.AnonymousContributions,
		},
	}
}

// SessionTokens 表示登录或轮换成功后签发的一组明文 Cookie 令牌。
type SessionTokens struct {
	Access           string
	Refresh          string
	Logout           string
	RemainingSeconds int
}

// TokenHashBytes 校验 43 字符无填充 Base64URL 令牌格式，并返回用于 BINARY(32) 列的原始 32 字节 SHA-256 摘要。
func TokenHashBytes(token string) ([]byte, bool) {
	if !reSessionToken.MatchString(token) {
		return nil, false
	}
	sum := sha256.Sum256([]byte(token))
	return sum[:], true
}

// TokenHashHex 校验令牌格式并返回 64 位小写十六进制 SHA-256 摘要（对齐 accounts.py:token_hash）。
func TokenHashHex(token string) (string, bool) {
	b, ok := TokenHashBytes(token)
	if !ok {
		return "", false
	}
	return hex.EncodeToString(b), true
}

// BuildSessionCookie 构造满足 __Host- 规范的第一方会话 Cookie（Secure, HttpOnly, SameSite=Lax, Path=/）。
func BuildSessionCookie(name, value string, maxAgeSeconds int) *http.Cookie {
	return &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAgeSeconds,
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	}
}

// ClearSessionCookie 构造立即清除指定 __Host- Cookie 的响应头结构。
func ClearSessionCookie(name string) *http.Cookie {
	return &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		Expires:  time.Unix(0, 0).UTC(),
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	}
}

// SafeWikiPath 严格过滤登录成功后的站内跳回路径，阻止开放重定向、路径穿越、控制字符与 /wiki/admin 跳转（对齐 wiki_account.py:safe_wiki_path）。
func SafeWikiPath(rawPath string) string {
	if rawPath == "" || len(rawPath) > 2048 {
		return "/wiki"
	}
	u, err := url.Parse(rawPath)
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil || strings.HasPrefix(rawPath, "//") {
		return "/wiki"
	}
	unquotedAll, err := url.PathUnescape(rawPath)
	if err != nil {
		return "/wiki"
	}
	for i := 0; i < len(unquotedAll); i++ {
		if unquotedAll[i] < 32 || unquotedAll[i] == '\\' {
			return "/wiki"
		}
	}
	decodedPath, err := url.PathUnescape(u.Path)
	if err != nil {
		return "/wiki"
	}
	if !reWikiPathPrefix.MatchString(decodedPath) ||
		!reSafeWikiPath.MatchString(u.Path) ||
		strings.HasPrefix(decodedPath, "/wiki/admin") ||
		strings.Contains(decodedPath, "%") {
		return "/wiki"
	}
	for _, seg := range strings.Split(decodedPath, "/") {
		if seg == "." || seg == ".." {
			return "/wiki"
		}
	}
	return rawPath
}

// AssistantBearer 派生 WebUI 与小妲助手边车之间的内部鉴权摘要（对齐 cmd/sakura-qa/wiki_web.go:95）。
func AssistantBearer(qaBotToken string) string {
	trimmed := strings.TrimSpace(qaBotToken)
	if trimmed == "" {
		return ""
	}
	sum := sha256.Sum256([]byte("sakura-wiki-assistant:v1:" + trimmed))
	return hex.EncodeToString(sum[:])
}

// VerifyAssistantBearer 常量时间校验 Authorization: Bearer <assistant_token> 请求头。
func VerifyAssistantBearer(authorizationHeader, qaBotToken string) bool {
	expected := AssistantBearer(qaBotToken)
	if expected == "" {
		return false
	}
	const prefix = "Bearer "
	if !strings.HasPrefix(authorizationHeader, prefix) {
		return false
	}
	provided := strings.TrimSpace(strings.TrimPrefix(authorizationHeader, prefix))
	return hmac.Equal([]byte(provided), []byte(expected))
}

// AccountService 管理 Wiki 读者 OIDC 登录流、双令牌会话、书签、隐私偏好与账户生命周期（对齐 core/wiki/accounts.py）。
type AccountService struct {
	store *Store
}

// NewAccountService 创建 Wiki 读者账户服务。
func NewAccountService(store *Store) *AccountService {
	return &AccountService{store: store}
}

// BeginLogin 创建绑定当前浏览器 Cookie 的 10 分钟 PKCE 登录流，返回 state、nonce、verifier 与实际使用的 browserToken。
func (a *AccountService) BeginLogin(ctx context.Context, browserToken, returnTo string, cfg LoginConfig, refreshSessionID string) (
	state, nonce, verifier, effectiveBrowser string, err error,
) {
	effectiveBrowser = browserToken
	browserHash, ok := TokenHashBytes(effectiveBrowser)
	if !ok {
		effectiveBrowser, err = GenerateToken()
		if err != nil {
			return "", "", "", "", err
		}
		browserHash, _ = TokenHashBytes(effectiveBrowser)
	}

	state, err = GenerateToken()
	if err != nil {
		return "", "", "", "", err
	}
	nonce, err = GenerateToken()
	if err != nil {
		return "", "", "", "", err
	}
	verifier, err = GenerateToken()
	if err != nil {
		return "", "", "", "", err
	}
	stateHash, _ := TokenHashBytes(state)
	safePath := SafeWikiPath(returnTo)

	_, _ = a.store.DB.ExecContext(ctx, "DELETE FROM wiki_login_flows WHERE expires_at <= UTC_TIMESTAMP(6) LIMIT 100")
	_, err = a.store.DB.ExecContext(ctx, `
		INSERT INTO wiki_login_flows
		(state_hash, browser_hash, nonce, verifier, redirect_path, refresh_session, client_id, origin, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, DATE_ADD(UTC_TIMESTAMP(6), INTERVAL ? SECOND))
	`, stateHash, browserHash, nonce, verifier, safePath, nilIfEmpty(refreshSessionID), cfg.ClientID, cfg.Origin, FlowTTLSeconds)
	if err != nil {
		return "", "", "", "", err
	}
	return state, nonce, verifier, effectiveBrowser, nil
}

// ConsumeLoginFlow 在写事务内校验并一次性销毁匹配的 OIDC 登录流（不匹配或已过期返回 nil, nil）。
func (a *AccountService) ConsumeLoginFlow(ctx context.Context, stateToken, browserToken string, cfg LoginConfig) (*LoginFlow, error) {
	stateHash, okS := TokenHashBytes(stateToken)
	browserHash, okB := TokenHashBytes(browserToken)
	if !okS || !okB {
		return nil, nil
	}

	var flow *LoginFlow
	err := a.store.WithTx(ctx, func(tx *sql.Tx) error {
		var nonce, verifier, redirectPath string
		var refreshSess sql.NullString
		qErr := tx.QueryRowContext(ctx, `
			SELECT nonce, verifier, redirect_path, refresh_session
			FROM wiki_login_flows
			WHERE state_hash=? AND browser_hash=? AND client_id=? AND origin=? AND expires_at > UTC_TIMESTAMP(6)
			FOR UPDATE
		`, stateHash, browserHash, cfg.ClientID, cfg.Origin).Scan(&nonce, &verifier, &redirectPath, &refreshSess)
		if errors.Is(qErr, sql.ErrNoRows) {
			return nil
		}
		if qErr != nil {
			return qErr
		}
		if _, dErr := tx.ExecContext(ctx, "DELETE FROM wiki_login_flows WHERE state_hash=?", stateHash); dErr != nil {
			return dErr
		}
		flow = &LoginFlow{
			Nonce:          nonce,
			Verifier:       verifier,
			RedirectPath:   redirectPath,
			RefreshSession: refreshSess.String,
		}
		return nil
	})
	return flow, err
}

// GetSession 通过明文访问令牌（refresh=false）或刷新令牌（refresh=true）查询有效且未封禁的会话。
func (a *AccountService) GetSession(ctx context.Context, token string, refresh bool) (*WikiUserSession, error) {
	hashBytes, ok := TokenHashBytes(token)
	if !ok {
		return nil, nil
	}
	col := "access"
	if refresh {
		col = "refresh"
	}
	q := fmt.Sprintf(`
		SELECT s.id, s.refresh_until, u.id, u.telegram_id, u.display_name, u.username,
		       (u.avatar IS NOT NULL) AS has_avatar, u.show_avatar, u.show_username, u.anonymous_contributions
		FROM wiki_sessions s
		JOIN wiki_users u ON u.id = s.user_id
		WHERE s.%s_hash = ? AND s.%s_until > UTC_TIMESTAMP(6) AND s.refresh_until > UTC_TIMESTAMP(6) AND u.blocked = FALSE
	`, col, col)

	var (
		sess    WikiUserSession
		refRaw  any
		uname   sql.NullString
		hasAvat bool
	)
	err := a.store.exec(ctx).QueryRowContext(ctx, q, hashBytes).Scan(
		&sess.SessionID, &refRaw, &sess.ID, &sess.TelegramID, &sess.DisplayName, &uname,
		&hasAvat, &sess.ShowAvatar, &sess.ShowUsername, &sess.AnonymousContributions,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	sess.Username = uname.String
	sess.HasAvatar = hasAvat
	if t := scanNullableTime(refRaw); t != nil {
		sess.RefreshUntil = *t
	}
	return &sess, nil
}

func lockUserTx(ctx context.Context, tx *sql.Tx, userID string) error {
	var blocked bool
	err := tx.QueryRowContext(ctx, "SELECT blocked FROM wiki_users WHERE id=? FOR UPDATE", userID).Scan(&blocked)
	if err != nil || blocked {
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		return ErrAccountDenied
	}
	return nil
}

func lockSessionTx(ctx context.Context, tx *sql.Tx, session *WikiUserSession, accessToken string) error {
	if session == nil {
		return ErrAccountDenied
	}
	if err := lockUserTx(ctx, tx, session.ID); err != nil {
		return err
	}
	accHash, ok := TokenHashBytes(accessToken)
	if !ok {
		return ErrAccountDenied
	}
	var sid string
	err := tx.QueryRowContext(ctx, `
		SELECT id FROM wiki_sessions
		WHERE id=? AND user_id=? AND access_hash=?
		  AND access_until > UTC_TIMESTAMP(6) AND refresh_until > UTC_TIMESTAMP(6)
		FOR UPDATE
	`, session.SessionID, session.ID, accHash).Scan(&sid)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrAccountDenied
		}
		return err
	}
	return nil
}

// CompleteLogin 绑定不可变 Telegram Subject、刷新用户公开资料并签发全新 15m/7d 会话令牌（对齐 accounts.py:login）。
func (a *AccountService) CompleteLogin(
	ctx context.Context,
	identity TelegramIdentity,
	avatarData []byte,
	avatarType string,
	refreshSessionID string,
	oldLogoutToken string,
) (*SessionTokens, error) {
	if identity.TelegramID <= 0 || len(identity.Subject) == 0 {
		return nil, ErrAccountDenied
	}
	accessTok, err := GenerateToken()
	if err != nil {
		return nil, err
	}
	refreshTok, err := GenerateToken()
	if err != nil {
		return nil, err
	}
	logoutTok, err := GenerateToken()
	if err != nil {
		return nil, err
	}
	accHash, _ := TokenHashBytes(accessTok)
	refHash, _ := TokenHashBytes(refreshTok)
	logHash, _ := TokenHashBytes(logoutTok)
	oldLogHash, hasOldLog := TokenHashBytes(oldLogoutToken)

	err = a.store.WithTx(ctx, func(tx *sql.Tx) error {
		candidateID := NewID()
		_, err := tx.ExecContext(ctx, `
			INSERT INTO wiki_users (id, telegram_id, telegram_subject, display_name, username, profile_at)
			VALUES (?, ?, ?, ?, ?, UTC_TIMESTAMP(6))
			ON DUPLICATE KEY UPDATE id = wiki_users.id
		`, candidateID, identity.TelegramID, identity.Subject, identity.DisplayName, nilIfEmpty(identity.Username))
		if err != nil {
			// 若 telegram_subject 已被其他 telegram_id 占用，唯一索引冲突映射为 ErrAccountDenied
			return ErrAccountDenied
		}

		var (
			userID  string
			storedS []byte
			blocked bool
		)
		if err := tx.QueryRowContext(ctx, `
			SELECT id, telegram_subject, blocked
			FROM wiki_users WHERE telegram_id=? FOR UPDATE
		`, identity.TelegramID).Scan(&userID, &storedS, &blocked); err != nil {
			return ErrAccountDenied
		}
		if userID == "" || !bytes.Equal(storedS, identity.Subject) || blocked {
			return ErrAccountDenied
		}

		if refreshSessionID != "" {
			if !hasOldLog {
				return ErrAccountDenied
			}
			var sid string
			qErr := tx.QueryRowContext(ctx, `
				SELECT id FROM wiki_sessions
				WHERE id=? AND user_id=? AND logout_hash=? AND refresh_until > UTC_TIMESTAMP(6)
				FOR UPDATE
			`, refreshSessionID, userID, oldLogHash).Scan(&sid)
			if qErr != nil {
				return ErrAccountDenied
			}
		}

		var avatarArg, typeArg any
		if len(avatarData) > 0 && avatarType != "" {
			avatarArg = avatarData
			typeArg = avatarType
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE wiki_users
			SET display_name=?, username=?, avatar=?, avatar_type=?, profile_at=UTC_TIMESTAMP(6)
			WHERE id=?
		`, identity.DisplayName, nilIfEmpty(identity.Username), avatarArg, typeArg, userID); err != nil {
			return err
		}

		if hasOldLog {
			if _, err := tx.ExecContext(ctx, "DELETE FROM wiki_sessions WHERE logout_hash=?", oldLogHash); err != nil {
				return err
			}
		}

		_, err = tx.ExecContext(ctx, `
			INSERT INTO wiki_sessions
			(id, user_id, access_hash, refresh_hash, logout_hash, access_until, refresh_until)
			VALUES (?, ?, ?, ?, ?,
				DATE_ADD(UTC_TIMESTAMP(6), INTERVAL ? SECOND),
				DATE_ADD(UTC_TIMESTAMP(6), INTERVAL ? SECOND))
		`, NewID(), userID, accHash, refHash, logHash, AccessTTLSeconds, RefreshTTLSeconds)
		return err
	})
	if err != nil {
		return nil, err
	}
	return &SessionTokens{
		Access:           accessTok,
		Refresh:          refreshTok,
		Logout:           logoutTok,
		RemainingSeconds: RefreshTTLSeconds,
	}, nil
}

// RotateSession 使用单次有效的 refresh_token 原子轮换出新一对 access + refresh 令牌（并发重放返回 nil, nil）。
func (a *AccountService) RotateSession(ctx context.Context, refreshToken string) (*SessionTokens, error) {
	sess, err := a.GetSession(ctx, refreshToken, true)
	if err != nil || sess == nil {
		return nil, err
	}
	oldRefHash, ok := TokenHashBytes(refreshToken)
	if !ok {
		return nil, nil
	}
	newAccess, err := GenerateToken()
	if err != nil {
		return nil, err
	}
	newRefresh, err := GenerateToken()
	if err != nil {
		return nil, err
	}
	newAccHash, _ := TokenHashBytes(newAccess)
	newRefHash, _ := TokenHashBytes(newRefresh)

	var updated bool
	err = a.store.WithTx(ctx, func(tx *sql.Tx) error {
		if err := lockUserTx(ctx, tx, sess.ID); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `
			UPDATE wiki_sessions
			SET access_hash=?, refresh_hash=?,
			    access_until=LEAST(refresh_until, DATE_ADD(UTC_TIMESTAMP(6), INTERVAL ? SECOND))
			WHERE id=? AND refresh_hash=? AND refresh_until > UTC_TIMESTAMP(6)
		`, newAccHash, newRefHash, AccessTTLSeconds, sess.SessionID, oldRefHash)
		if err != nil {
			return err
		}
		affected, _ := res.RowsAffected()
		updated = affected == 1
		return nil
	})
	if err != nil {
		if errors.Is(err, ErrAccountDenied) {
			return nil, ErrAccountDenied
		}
		return nil, err
	}
	if !updated {
		return nil, nil
	}

	rem := int(time.Until(sess.RefreshUntil).Seconds())
	if rem < 1 {
		rem = 1
	}
	return &SessionTokens{
		Access:           newAccess,
		Refresh:          newRefresh,
		RemainingSeconds: rem,
	}, nil
}

// Logout 按 logout_token 与 browser_token 销毁当前会话及挂起登录流。
func (a *AccountService) Logout(ctx context.Context, logoutToken, browserToken string) error {
	logHash, hasLog := TokenHashBytes(logoutToken)
	brHash, hasBr := TokenHashBytes(browserToken)
	if !hasLog && !hasBr {
		return nil
	}
	return a.store.WithTx(ctx, func(tx *sql.Tx) error {
		if hasLog {
			if _, err := tx.ExecContext(ctx, "DELETE FROM wiki_sessions WHERE logout_hash=?", logHash); err != nil {
				return err
			}
		}
		if hasBr {
			if _, err := tx.ExecContext(ctx, "DELETE FROM wiki_login_flows WHERE browser_hash=?", brHash); err != nil {
				return err
			}
		}
		return nil
	})
}

// GetBookmarks 按收藏时间倒序返回用户全部书签词条 ID。
func (a *AccountService) GetBookmarks(ctx context.Context, userID string) ([]string, error) {
	rows, err := a.store.exec(ctx).QueryContext(ctx, "SELECT page_id FROM wiki_bookmarks WHERE user_id=? ORDER BY created_at DESC, page_id", userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []string{}
	for rows.Next() {
		var pid string
		if err := rows.Scan(&pid); err == nil && pid != "" {
			out = append(out, pid)
		}
	}
	return out, rows.Err()
}

// UpdateBookmarks 在写事务内加锁当前会话并增删用户书签，强约束单用户总上限 <= 500 条。
func (a *AccountService) UpdateBookmarks(ctx context.Context, session *WikiUserSession, accessToken string, add []string, remove string) error {
	cleanAdd := make([]string, 0, len(add))
	for _, pid := range UniqueStrings(add) {
		if !reHex32.MatchString(pid) {
			return errors.New("invalid bookmark page_id")
		}
		cleanAdd = append(cleanAdd, pid)
	}
	if remove != "" && !reHex32.MatchString(remove) {
		return errors.New("invalid bookmark remove page_id")
	}

	return a.store.WithTx(ctx, func(tx *sql.Tx) error {
		if err := lockSessionTx(ctx, tx, session, accessToken); err != nil {
			return err
		}
		if remove != "" {
			if _, err := tx.ExecContext(ctx, "DELETE FROM wiki_bookmarks WHERE user_id=? AND page_id=?", session.ID, remove); err != nil {
				return err
			}
		}
		if len(cleanAdd) > 0 {
			rows, err := tx.QueryContext(ctx, "SELECT page_id FROM wiki_bookmarks WHERE user_id=? FOR UPDATE", session.ID)
			if err != nil {
				return err
			}
			existing := map[string]bool{}
			for rows.Next() {
				var pid string
				if err := rows.Scan(&pid); err == nil {
					existing[pid] = true
				}
			}
			_ = rows.Close()

			newCount := 0
			for _, pid := range cleanAdd {
				if pid == remove {
					continue
				}
				if !existing[pid] {
					newCount++
				}
			}
			if len(existing)+newCount > MaxBookmarksPerUser {
				return ErrBookmarkLimitExceeded
			}
			for _, pid := range cleanAdd {
				if pid == remove {
					continue
				}
				if _, err := tx.ExecContext(ctx, `
					INSERT INTO wiki_bookmarks (user_id, page_id, created_at)
					VALUES (?, ?, UTC_TIMESTAMP(6))
					ON DUPLICATE KEY UPDATE page_id = wiki_bookmarks.page_id
				`, session.ID, pid); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// UpdatePrivacy 更新读者公开署名偏好（头像展示、用户名展示、匿名投稿开关）。
func (a *AccountService) UpdatePrivacy(ctx context.Context, session *WikiUserSession, accessToken string, prefs PrivacyPreferences) error {
	return a.store.WithTx(ctx, func(tx *sql.Tx) error {
		if err := lockSessionTx(ctx, tx, session, accessToken); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `
			UPDATE wiki_users
			SET show_avatar=?, show_username=?, anonymous_contributions=?
			WHERE id=?
		`, prefs.ShowAvatar, prefs.ShowUsername, prefs.AnonymousContributions, session.ID)
		return err
	})
}

// DeleteAccount 执行读者自助注销账户：在有效会话锁下级联删除用户记录、所有活动会话、书签与关联投稿归属。
func (a *AccountService) DeleteAccount(ctx context.Context, session *WikiUserSession, accessToken string) error {
	return a.store.WithTx(ctx, func(tx *sql.Tx) error {
		if err := lockSessionTx(ctx, tx, session, accessToken); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "DELETE FROM wiki_users WHERE id=?", session.ID)
		return err
	})
}

// SetBlocked 封禁或解封指定 Wiki 读者账户；封禁时立即强制清除其全部在线会话。
func (a *AccountService) SetBlocked(ctx context.Context, userID string, blocked bool) (bool, error) {
	if !reHex32.MatchString(userID) {
		return false, nil
	}
	var found bool
	err := a.store.WithTx(ctx, func(tx *sql.Tx) error {
		var uid string
		if err := tx.QueryRowContext(ctx, "SELECT id FROM wiki_users WHERE id=? FOR UPDATE", userID).Scan(&uid); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return err
		}
		found = true
		if _, err := tx.ExecContext(ctx, "UPDATE wiki_users SET blocked=? WHERE id=?", blocked, userID); err != nil {
			return err
		}
		if blocked {
			if _, err := tx.ExecContext(ctx, "DELETE FROM wiki_sessions WHERE user_id=?", userID); err != nil {
				return err
			}
		}
		return nil
	})
	return found, err
}

// ListAccounts 分页列出管理端可见的 Wiki 读者账户摘要（对齐 routes/wiki_account.py:accounts）。
func (a *AccountService) ListAccounts(ctx context.Context, offset int) ([]map[string]any, error) {
	if offset < 0 {
		offset = 0
	}
	rows, err := QueryRowsMap(ctx, a.store.DB, `
		SELECT id, CAST(telegram_id AS CHAR) AS telegram_id, display_name, username, blocked, profile_at
		FROM wiki_users
		ORDER BY profile_at DESC, id
		LIMIT 50 OFFSET ?
	`, offset)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		row["blocked"] = anyToInt64(row["blocked"]) != 0 || mapBool(row, "blocked")
	}
	return rows, nil
}

// GetUserAvatar 读取用户头像二进制与 MIME 类型；requirePublicOptIn=true 时额外要求 show_avatar=TRUE 且 anonymous_contributions=FALSE。
func (a *AccountService) GetUserAvatar(ctx context.Context, userID string, requirePublicOptIn bool) ([]byte, string, error) {
	if !reHex32.MatchString(userID) {
		return nil, "", nil
	}
	q := "SELECT avatar, avatar_type FROM wiki_users WHERE id=? AND blocked=FALSE"
	if requirePublicOptIn {
		q += " AND show_avatar=TRUE AND anonymous_contributions=FALSE"
	}
	var data []byte
	var mimeType sql.NullString
	err := a.store.exec(ctx).QueryRowContext(ctx, q, userID).Scan(&data, &mimeType)
	if errors.Is(err, sql.ErrNoRows) || len(data) == 0 || mimeType.String == "" {
		return nil, "", nil
	}
	return data, mimeType.String, err
}

// ConsumeLegacyReplay 代理 Store.ConsumeLegacyReplay 供管理端 Telegram Login Widget 防重放校验。
func (a *AccountService) ConsumeLegacyReplay(ctx context.Context, signatureHex string, now time.Time) (bool, error) {
	return a.store.ConsumeLegacyReplay(ctx, signatureHex, now)
}

// Cleanup 定期清理已过期的会话、PKCE 登录流与旧版登录重放签名记录（每次最多 1000 条）。
func (a *AccountService) Cleanup(ctx context.Context) error {
	for _, q := range []string{
		"DELETE FROM wiki_sessions WHERE refresh_until <= UTC_TIMESTAMP(6) LIMIT 1000",
		"DELETE FROM wiki_login_flows WHERE expires_at <= UTC_TIMESTAMP(6) LIMIT 1000",
		"DELETE FROM wiki_legacy_login_replays WHERE expires_at <= UTC_TIMESTAMP(6) LIMIT 1000",
	} {
		if _, err := a.store.DB.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	return nil
}
