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
	"container/list"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	jwtDefaultFallbackBotToken = "sakura-bot-default-secret"
	jwtTTLSeconds              = 7 * 24 * 3600 // 604800 秒（7 天）
)

type ctxUserKey struct{}

// WebUIUser 表示通过 WebUI JWT 或开发模式认证的管理端会话身份（对齐 core/web_api/auth.py:WebUIUser）。
type WebUIUser struct {
	UserID string `json:"user_id"`
	IsDev  bool   `json:"is_dev"`
}

// ContextWithUser 将认证用户绑定到请求 Context。
func ContextWithUser(ctx context.Context, user WebUIUser) context.Context {
	return context.WithValue(ctx, ctxUserKey{}, user)
}

// UserFromContext 从请求 Context 读取当前认证用户。
func UserFromContext(ctx context.Context) (WebUIUser, bool) {
	u, ok := ctx.Value(ctxUserKey{}).(WebUIUser)
	return u, ok
}

// ComputeAdminToken 根据主 Bot Token 派生 16 位确定性 WebUI 管理密码（SHA256(botToken)[:16]，对齐 routes/auth.py:_compute_admin_token）。
func ComputeAdminToken(botToken string) string {
	t := strings.TrimSpace(botToken)
	if t == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(t))
	return hex.EncodeToString(sum[:])[:16]
}

// jwtSigningSecret 计算 HS256 JWT 签名密钥字符串：SHA256("webui:" + botToken) 的十六进制编码（对齐 auth.py:_get_jwt_secret）。
func jwtSigningSecret(botToken string) []byte {
	tok := strings.TrimSpace(botToken)
	if tok == "" {
		tok = jwtDefaultFallbackBotToken
	}
	sum := sha256.Sum256([]byte("webui:" + tok))
	return []byte(hex.EncodeToString(sum[:]))
}

// CreateAccessToken 签发 HS256 WebUI JWT（7 天有效期，scope="webui"，逐字节对齐 Python PyJWT 输出）。
func CreateAccessToken(userID string, botToken string, now time.Time) (string, error) {
	if strings.TrimSpace(userID) == "" {
		return "", errors.New("empty user_id")
	}
	headerJSON := `{"alg":"HS256","typ":"JWT"}`
	iat := now.UTC().Unix()
	exp := iat + jwtTTLSeconds
	payloadBytes, err := json.Marshal(map[string]any{
		"sub":   userID,
		"iat":   iat,
		"exp":   exp,
		"scope": "webui",
	})
	if err != nil {
		return "", err
	}
	enc := base64.RawURLEncoding
	signingInput := enc.EncodeToString([]byte(headerJSON)) + "." + enc.EncodeToString(payloadBytes)
	mac := hmac.New(sha256.New, jwtSigningSecret(botToken))
	_, _ = mac.Write([]byte(signingInput))
	sig := enc.EncodeToString(mac.Sum(nil))
	return signingInput + "." + sig, nil
}

// VerifyAccessToken 校验 WebUI Token（支持开发模式字面量 "dev" 及标准 HS256 JWT）。
func VerifyAccessToken(rawToken string, botToken string, devMode bool, now time.Time) (WebUIUser, error) {
	tok := strings.TrimSpace(rawToken)
	if tok == "" {
		return WebUIUser{}, errors.New("missing token")
	}
	if devMode && tok == "dev" {
		return WebUIUser{UserID: "dev", IsDev: true}, nil
	}
	if !devMode && strings.TrimSpace(botToken) == "" {
		return WebUIUser{}, errors.New("webui signing key is unavailable")
	}

	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return WebUIUser{}, errors.New("invalid jwt format")
	}
	enc := base64.RawURLEncoding

	headerBytes, err := enc.DecodeString(parts[0])
	if err != nil {
		return WebUIUser{}, errors.New("invalid jwt header")
	}
	var hdr struct {
		Alg string `json:"alg"`
	}
	if err := json.Unmarshal(headerBytes, &hdr); err != nil || hdr.Alg != "HS256" {
		return WebUIUser{}, errors.New("unsupported jwt alg")
	}

	expectedMAC := hmac.New(sha256.New, jwtSigningSecret(botToken))
	_, _ = expectedMAC.Write([]byte(parts[0] + "." + parts[1]))
	gotSig, err := enc.DecodeString(parts[2])
	if err != nil || !hmac.Equal(gotSig, expectedMAC.Sum(nil)) {
		return WebUIUser{}, errors.New("invalid jwt signature")
	}

	payloadBytes, err := enc.DecodeString(parts[1])
	if err != nil {
		return WebUIUser{}, errors.New("invalid jwt payload")
	}
	var claims struct {
		Sub   string `json:"sub"`
		Exp   int64  `json:"exp"`
		Scope string `json:"scope"`
	}
	if err := json.Unmarshal(payloadBytes, &claims); err != nil {
		return WebUIUser{}, errors.New("invalid jwt claims")
	}
	if claims.Scope != "webui" || claims.Sub == "" {
		return WebUIUser{}, errors.New("invalid jwt scope")
	}
	if claims.Exp <= now.UTC().Unix() {
		return WebUIUser{}, errors.New("jwt expired")
	}
	return WebUIUser{
		UserID: claims.Sub,
		IsDev:  claims.Sub == "dev",
	}, nil
}

// ExtractBearerOrQueryToken 按 Python AuthMiddleware 优先级提取 ?token= 或 Authorization: Bearer <token>。
func ExtractBearerOrQueryToken(r *http.Request) string {
	if r == nil {
		return ""
	}
	if q := strings.TrimSpace(r.URL.Query().Get("token")); q != "" {
		return q
	}
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	if strings.HasPrefix(auth, "Bearer ") || strings.HasPrefix(auth, "bearer ") {
		return strings.TrimSpace(auth[7:])
	}
	return ""
}

// VerifyTelegramLoginWidget 校验旧版 Telegram Login Widget HMAC-SHA256 签名与时间窗（对齐 deps.py:verify_telegram_auth）。
func VerifyTelegramLoginWidget(fields map[string]string, botToken string, now time.Time) (int64, string, error) {
	tok := strings.TrimSpace(botToken)
	if tok == "" {
		return 0, "", errors.New("bot token unconfigured")
	}
	rawHash := strings.ToLower(strings.TrimSpace(fields["hash"]))
	if len(rawHash) != 64 {
		return 0, "", errors.New("invalid hash")
	}
	hashBytes, err := hex.DecodeString(rawHash)
	if err != nil {
		return 0, "", errors.New("invalid hash hex")
	}

	authDate, err := strconv.ParseInt(strings.TrimSpace(fields["auth_date"]), 10, 64)
	if err != nil {
		return 0, "", errors.New("invalid auth_date")
	}
	diff := now.UTC().Unix() - authDate
	if diff < -30 || diff > 300 {
		return 0, "", errors.New("auth_date expired")
	}

	uid, err := strconv.ParseInt(strings.TrimSpace(fields["id"]), 10, 64)
	if err != nil || uid <= 0 {
		return 0, "", errors.New("invalid user id")
	}

	keys := make([]string, 0, len(fields))
	for k, v := range fields {
		if k == "hash" || v == "" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, k := range keys {
		lines = append(lines, fmt.Sprintf("%s=%s", k, fields[k]))
	}
	dataCheckString := strings.Join(lines, "\n")

	secretKey := sha256.Sum256([]byte(tok))
	mac := hmac.New(sha256.New, secretKey[:])
	_, _ = mac.Write([]byte(dataCheckString))
	if !hmac.Equal(hashBytes, mac.Sum(nil)) {
		return 0, "", errors.New("signature mismatch")
	}
	return uid, rawHash, nil
}

// ────────────────────────────── 有界 LRU IP 速率限制器 ──────────────────────────────

type ipBucket struct {
	ip         string
	timestamps []int64
}

// IPRateLimiter 实现按客户端 Socket IP 的有界 LRU 滑动时间窗限流（最多保留 maxEntries 个活跃 IP，对齐 middleware.py）。
type IPRateLimiter struct {
	mu         sync.Mutex
	limit      int
	windowSec  int64
	maxEntries int
	order      *list.List
	items      map[string]*list.Element
}

// NewIPRateLimiter 创建滑动窗口限流器。
func NewIPRateLimiter(limit int, window time.Duration, maxEntries int) *IPRateLimiter {
	if maxEntries <= 0 {
		maxEntries = 10000
	}
	winSec := int64(window.Seconds())
	if winSec <= 0 {
		winSec = 60
	}
	return &IPRateLimiter{
		limit:      limit,
		windowSec:  winSec,
		maxEntries: maxEntries,
		order:      list.New(),
		items:      make(map[string]*list.Element, 256),
	}
}

// Allow 判断 ip 在当前时刻是否允许通过；超出速率上限返回 false。
func (rl *IPRateLimiter) Allow(ip string, now time.Time) bool {
	if ip == "" {
		ip = "unknown"
	}
	nowSec := now.UTC().Unix()
	cutoff := nowSec - rl.windowSec

	rl.mu.Lock()
	defer rl.mu.Unlock()

	var b *ipBucket
	if el, ok := rl.items[ip]; ok {
		rl.order.MoveToFront(el)
		b = el.Value.(*ipBucket)
	} else {
		b = &ipBucket{ip: ip}
		el := rl.order.PushFront(b)
		rl.items[ip] = el
		for rl.order.Len() > rl.maxEntries {
			back := rl.order.Back()
			rl.order.Remove(back)
			delete(rl.items, back.Value.(*ipBucket).ip)
		}
	}

	valid := b.timestamps[:0]
	for _, ts := range b.timestamps {
		if ts > cutoff {
			valid = append(valid, ts)
		}
	}
	if len(valid) >= rl.limit {
		b.timestamps = valid
		return false
	}
	b.timestamps = append(valid, nowSec)
	return true
}

func clientSocketIP(r *http.Request) string {
	if r == nil || r.RemoteAddr == "" {
		return "127.0.0.1"
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil && host != "" {
		return host
	}
	return r.RemoteAddr
}
