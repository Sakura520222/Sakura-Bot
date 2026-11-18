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
	"crypto"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"math/big"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Sakura520222/Sakura-Bot/internal/config"
	"github.com/Sakura520222/Sakura-Bot/internal/tg"
)

const (
	// OAuthIssuer 是官方 Telegram OIDC 签发方根地址。
	OAuthIssuer = "https://oauth.telegram.org"
	// OAuthCallbackPath 是固定第一方 OIDC 回调路径。
	OAuthCallbackPath = "/api/wiki/account/callback"
	// OAuthJWKSURL 是官方 Telegram OIDC JWKS 公钥集端点。
	OAuthJWKSURL = OAuthIssuer + "/.well-known/jwks.json"
	// MaxUserAvatarBytes 是 Wiki 读者个人资料头像的最大允许字节数（512 KiB）。
	MaxUserAvatarBytes = 512 * 1024

	maxImageDimension = 8192
	maxImagePixels    = 12_000_000
)

var (
	reHTTPSOrigin       = regexp.MustCompile(`^https://[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?(?::[1-9][0-9]{0,4})?$`)
	rePrintableASCII    = regexp.MustCompile(`^[\x21-\x7e]{1,255}$`)
	reTelegramUsername  = regexp.MustCompile(`^[a-zA-Z0-9_]{1,64}$`)
	reTelescoAvatarURL  = regexp.MustCompile(`^https://(?:cdn[0-9]{1,2}|[a-z0-9-]+)\.telesco\.pe/file/[A-Za-z0-9_-]{1,1000}[A-Za-z0-9_-]{0,600}(?:\.(?:jpe?g|png))?$`)
	reTMeUserpicURL     = regexp.MustCompile(`^https://t\.me/i/userpic/[0-9]{1,4}/[A-Za-z0-9_-]{1,256}\.(?:jpe?g|png)$`)
	reBotAvatarFilePath = regexp.MustCompile(`^photos/[A-Za-z0-9_-]+\.(?:jpe?g|png)$`)
	reBotTokenFormat    = regexp.MustCompile(`^[1-9][0-9]{0,18}:[A-Za-z0-9_-]+$`)

	knownOAuthProviderErrors = map[string]bool{
		"invalid_client":          true,
		"invalid_grant":           true,
		"invalid_request":         true,
		"unauthorized_client":     true,
		"unsupported_grant_type":  true,
		"access_denied":           true,
		"server_error":            true,
		"temporarily_unavailable": true,
	}
)

// TelegramLoginError 表示 OIDC 授权码换取、JWKS 拉取或 ID Token 校验阶段的脱敏错误。
type TelegramLoginError struct {
	Stage  string
	Reason string
	Status int
}

func (e *TelegramLoginError) Error() string {
	if e == nil {
		return "telegram_login_error"
	}
	if e.Status > 0 {
		return fmt.Sprintf("telegram login %s failed: %s (HTTP %d)", e.Stage, e.Reason, e.Status)
	}
	return fmt.Sprintf("telegram login %s failed: %s", e.Stage, e.Reason)
}

// LoginSettings 表示持久化在 config.json["wiki_login"] 与环境变量中的 Wiki 账户登录设置。
type LoginSettings struct {
	Enabled          bool   `json:"enabled"`
	Origin           string `json:"origin"`
	ClientSecret     string `json:"-"`
	HasSecret        bool   `json:"has_secret"`
	HasBot           bool   `json:"has_bot"`
	AssistantEnabled bool   `json:"assistant_enabled"`
	AssistantCheckin bool   `json:"assistant_checkin"`
}

// LoginConfig 表示通过完整性校验后可用于发起与回调校验的 OIDC 配置。
type LoginConfig struct {
	Origin       string
	ClientID     string
	ClientSecret string
	Issuer       string // 为空时默认 OAuthIssuer（供单测覆盖本地模拟服务）
}

// CallbackURL 返回完整 HTTPS OIDC 回调 URL。
func (c LoginConfig) CallbackURL() string {
	return c.Origin + OAuthCallbackPath
}

func (c LoginConfig) effectiveIssuer() string {
	if strings.TrimSpace(c.Issuer) != "" {
		return strings.TrimRight(strings.TrimSpace(c.Issuer), "/")
	}
	return OAuthIssuer
}

// LoginSettingsFrom 从运行期配置读取当前 Wiki 登录与小妲助手开关状态。
func LoginSettingsFrom(cfg *config.Config) LoginSettings {
	if cfg == nil {
		return LoginSettings{AssistantCheckin: true}
	}
	secret := cfg.WikiTelegramClientSecret()
	token := strings.TrimSpace(cfg.QABotToken())
	return LoginSettings{
		Enabled:          cfg.WikiLoginEnabled(),
		Origin:           cfg.WikiLoginOrigin(),
		ClientSecret:     secret,
		HasSecret:        secret != "",
		HasBot:           token != "",
		AssistantEnabled: cfg.WikiAssistantEnabled(),
		AssistantCheckin: cfg.WikiAssistantCheckinEnabled(),
	}
}

// BuildLoginConfig 严格校验 HTTPS Origin、Client Secret 与 QA Bot Token，不满足时返回 nil（对齐 telegram_login.py:login_config）。
func BuildLoginConfig(enabled bool, rawOrigin, clientSecret, qaBotToken string) *LoginConfig {
	secret := strings.TrimSpace(clientSecret)
	token := strings.TrimSpace(qaBotToken)
	if !enabled || secret == "" || token == "" {
		return nil
	}
	origin := strings.TrimRight(strings.TrimSpace(rawOrigin), "/")
	if len(origin) > 255 || !reHTTPSOrigin.MatchString(origin) {
		return nil
	}
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" {
		return nil
	}
	if portStr := u.Port(); portStr != "" {
		portNum, pErr := strconv.Atoi(portStr)
		if pErr != nil || portNum < 1 || portNum > 65535 {
			return nil
		}
		if portNum == 443 {
			origin = strings.TrimSuffix(origin, ":443")
		}
	}
	botID, ok := tg.BotIDFromToken(token)
	if !ok {
		return nil
	}
	return &LoginConfig{
		Origin:       origin,
		ClientID:     strconv.FormatInt(botID, 10),
		ClientSecret: secret,
		Issuer:       OAuthIssuer,
	}
}

// LoginConfigFrom 从 *config.Config 构造已验证的 LoginConfig（未启用或配置不完整时返回 nil）。
func LoginConfigFrom(cfg *config.Config) *LoginConfig {
	if cfg == nil {
		return nil
	}
	s := LoginSettingsFrom(cfg)
	return BuildLoginConfig(s.Enabled, s.Origin, s.ClientSecret, cfg.QABotToken())
}

// GenerateToken 生成 32 字节密码学随机数的 43 字符无填充 URL-safe Base64 令牌（对齐 secrets.token_urlsafe(32)）。
func GenerateToken() (string, error) {
	var buf [32]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("generate random token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf[:]), nil
}

// PKCEChallengeS256 按 RFC 7636 计算 BASE64URL-NOPAD(SHA256(ASCII(verifier)))。
func PKCEChallengeS256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// AuthorizationURL 构造 Telegram OIDC Authorization Code + PKCE (S256) 跳转 URL（对齐 telegram_login.py:authorization_url）。
func AuthorizationURL(cfg LoginConfig, state, nonce, verifier string) string {
	vals := url.Values{}
	vals.Set("client_id", cfg.ClientID)
	vals.Set("redirect_uri", cfg.CallbackURL())
	vals.Set("response_type", "code")
	vals.Set("scope", "openid profile")
	vals.Set("state", state)
	vals.Set("nonce", nonce)
	vals.Set("code_challenge", PKCEChallengeS256(verifier))
	vals.Set("code_challenge_method", "S256")
	return cfg.effectiveIssuer() + "/auth?" + vals.Encode()
}

// JWK 表示 JSON Web Key 公钥项（支持 RS256 RSA 与 ES256 P-256 EC）。
type JWK struct {
	Kty string `json:"kty"`
	Use string `json:"use,omitempty"`
	Alg string `json:"alg,omitempty"`
	Kid string `json:"kid"`
	Crv string `json:"crv,omitempty"`
	N   string `json:"n,omitempty"`
	E   string `json:"e,omitempty"`
	X   string `json:"x,omitempty"`
	Y   string `json:"y,omitempty"`
}

// TelegramIdentity 表示通过 OIDC ID Token 验签与声明核验后的 Telegram 用户身份。
type TelegramIdentity struct {
	TelegramID  int64
	Subject     []byte // 不可变 ASCII sub 字节序列（存入 VARBINARY(255)）
	DisplayName string
	Username    string
	PictureURL  string
}

// JWKSCache 维护带 1h 正常缓存与 30s 未知 kid 刷新保护的 OIDC JWKS 公钥缓存。
type JWKSCache struct {
	mu      sync.Mutex
	keys    []JWK
	fetched time.Time
	jwksURL string
	client  *http.Client
}

// NewJWKSCache 创建 JWKS 缓存器。
func NewJWKSCache(jwksURL string, client *http.Client) *JWKSCache {
	if strings.TrimSpace(jwksURL) == "" {
		jwksURL = OAuthJWKSURL
	}
	if client == nil {
		client = &http.Client{
			Timeout: 10 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	}
	return &JWKSCache{
		jwksURL: jwksURL,
		client:  client,
	}
}

var defaultJWKSCache = NewJWKSCache(OAuthJWKSURL, nil)

func (c *JWKSCache) findMatching(kid, alg string) (JWK, bool) {
	expectedKty := map[string]string{"RS256": "RSA", "ES256": "EC"}[alg]
	if expectedKty == "" {
		return JWK{}, false
	}
	for _, k := range c.keys {
		use := k.Use
		if use == "" {
			use = "sig"
		}
		keyAlg := k.Alg
		if keyAlg == "" {
			keyAlg = alg
		}
		if k.Kid == kid && k.Kty == expectedKty && use == "sig" && keyAlg == alg {
			return k, true
		}
	}
	return JWK{}, false
}

// SigningKey 按 JWT 头部的 kid 与 alg 获取对应验证公钥（自动处理过期或轮换刷新）。
func (c *JWKSCache) SigningKey(ctx context.Context, kid, alg string) (crypto.PublicKey, error) {
	if alg != "RS256" && alg != "ES256" {
		return nil, errors.New("unsupported signing algorithm")
	}
	if len(kid) < 1 || len(kid) > 200 {
		return nil, errors.New("invalid key id")
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	age := time.Since(c.fetched)
	_, matched := c.findMatching(kid, alg)
	if c.fetched.IsZero() || age >= time.Hour || (age >= 30*time.Second && !matched) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.jwksURL, nil)
		if err != nil {
			return nil, err
		}
		resp, err := c.client.Do(req)
		if err != nil {
			return nil, err
		}
		body, readErr := readBoundedBytes(resp.Body, 64*1024)
		_ = resp.Body.Close()
		if readErr != nil {
			return nil, readErr
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("jwks HTTP %d", resp.StatusCode)
		}
		var doc struct {
			Keys []JWK `json:"keys"`
		}
		if err := json.Unmarshal(body, &doc); err != nil || len(doc.Keys) == 0 {
			return nil, errors.New("invalid jwks payload")
		}
		c.keys = doc.Keys
		c.fetched = time.Now()
	}

	jwk, ok := c.findMatching(kid, alg)
	if !ok {
		return nil, errors.New("unknown signing key")
	}
	return ParseJWKPublicKey(jwk, alg)
}

// ParseJWKPublicKey 将单个 JWK 解析为 *rsa.PublicKey（RS256）或 *ecdsa.PublicKey（ES256 P-256）。
func ParseJWKPublicKey(jwk JWK, expectedAlg string) (crypto.PublicKey, error) {
	switch expectedAlg {
	case "RS256":
		if jwk.Kty != "RSA" || jwk.N == "" || jwk.E == "" {
			return nil, errors.New("invalid RSA JWK")
		}
		nBytes, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(jwk.N, "="))
		if err != nil || len(nBytes) == 0 {
			return nil, errors.New("invalid RSA modulus")
		}
		eBytes, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(jwk.E, "="))
		if err != nil || len(eBytes) == 0 || len(eBytes) > 8 {
			return nil, errors.New("invalid RSA exponent")
		}
		n := new(big.Int).SetBytes(nBytes)
		if n.BitLen() < 2048 {
			return nil, errors.New("RSA key shorter than 2048 bits")
		}
		eBig := new(big.Int).SetBytes(eBytes)
		if !eBig.IsInt64() {
			return nil, errors.New("RSA exponent overflow")
		}
		e := int(eBig.Int64())
		if e < 3 || e%2 == 0 {
			return nil, errors.New("invalid RSA public exponent")
		}
		return &rsa.PublicKey{N: n, E: e}, nil

	case "ES256":
		if jwk.Kty != "EC" || jwk.Crv != "P-256" || jwk.X == "" || jwk.Y == "" {
			return nil, errors.New("invalid P-256 EC JWK")
		}
		xBytes, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(jwk.X, "="))
		if err != nil || len(xBytes) != 32 {
			return nil, errors.New("invalid EC X coordinate")
		}
		yBytes, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(jwk.Y, "="))
		if err != nil || len(yBytes) != 32 {
			return nil, errors.New("invalid EC Y coordinate")
		}
		// 通过标准库 crypto/ecdh 执行完整 NIST P-256 曲线点合法性与无穷远点检查
		uncompressed := make([]byte, 65)
		uncompressed[0] = 0x04
		copy(uncompressed[1:33], xBytes)
		copy(uncompressed[33:65], yBytes)
		if _, err := ecdh.P256().NewPublicKey(uncompressed); err != nil {
			return nil, fmt.Errorf("invalid P-256 curve point: %w", err)
		}
		return &ecdsa.PublicKey{
			Curve: elliptic.P256(),
			X:     new(big.Int).SetBytes(xBytes),
			Y:     new(big.Int).SetBytes(yBytes),
		}, nil

	default:
		return nil, fmt.Errorf("unsupported JWK algorithm %q", expectedAlg)
	}
}

// VerifyJWS 验证 RS256（PKCS#1 v1.5）或 ES256（IEEE P1363 固定 64 字节 R||S）JWT 签名。
func VerifyJWS(signingInput string, signature []byte, alg string, pub crypto.PublicKey) error {
	digest := sha256.Sum256([]byte(signingInput))
	switch alg {
	case "RS256":
		rsaPub, ok := pub.(*rsa.PublicKey)
		if !ok || rsaPub == nil {
			return errors.New("key type mismatch for RS256")
		}
		return rsa.VerifyPKCS1v15(rsaPub, crypto.SHA256, digest[:], signature)

	case "ES256":
		ecPub, ok := pub.(*ecdsa.PublicKey)
		if !ok || ecPub == nil {
			return errors.New("key type mismatch for ES256")
		}
		// RFC 7518 §3.4: ES256 JWS 签名固定为 64 字节大端 R(32) || S(32)，而非 ASN.1 DER
		if len(signature) != 64 {
			return errors.New("invalid ES256 signature length")
		}
		r := new(big.Int).SetBytes(signature[:32])
		s := new(big.Int).SetBytes(signature[32:])
		if r.Sign() <= 0 || s.Sign() <= 0 {
			return errors.New("invalid ES256 signature integers")
		}
		if !ecdsa.Verify(ecPub, digest[:], r, s) {
			return errors.New("ES256 signature verification failed")
		}
		return nil

	default:
		return fmt.Errorf("unsupported algorithm %q", alg)
	}
}

func parseJWTHeader(token string) (alg, kid, signingInput string, payloadBytes, sigBytes []byte, err error) {
	if len(token) < 1 || len(token) > 16384 {
		return "", "", "", nil, nil, errors.New("invalid id_token length")
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return "", "", "", nil, nil, errors.New("malformed JWT structure")
	}
	hdrRaw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", "", "", nil, nil, errors.New("invalid JWT header encoding")
	}
	payloadBytes, err = base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", "", "", nil, nil, errors.New("invalid JWT payload encoding")
	}
	sigBytes, err = base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return "", "", "", nil, nil, errors.New("invalid JWT signature encoding")
	}
	var hdr struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if err := json.Unmarshal(hdrRaw, &hdr); err != nil {
		return "", "", "", nil, nil, errors.New("invalid JWT header json")
	}
	if (hdr.Alg != "RS256" && hdr.Alg != "ES256") || len(hdr.Kid) < 1 || len(hdr.Kid) > 200 {
		return "", "", "", nil, nil, errors.New("invalid signing header")
	}
	return hdr.Alg, hdr.Kid, parts[0] + "." + parts[1], payloadBytes, sigBytes, nil
}

// VerifyIDToken 完整验证 Telegram OIDC ID Token 的密码学签名及 iss / aud / sub / exp / iat / nonce / id 声明（对齐 telegram_login.py:verify_identity）。
func VerifyIDToken(token string, pub crypto.PublicKey, expectedAlg, expectedIssuer, expectedAudience, expectedNonce string, now time.Time) (*TelegramIdentity, error) {
	if expectedIssuer == "" {
		expectedIssuer = OAuthIssuer
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	alg, _, signingInput, payloadBytes, sigBytes, err := parseJWTHeader(token)
	if err != nil {
		return nil, &TelegramLoginError{Stage: "identity", Reason: "invalid_header"}
	}
	if expectedAlg != "" && alg != expectedAlg {
		return nil, &TelegramLoginError{Stage: "identity", Reason: "algorithm_mismatch"}
	}
	if err := VerifyJWS(signingInput, sigBytes, alg, pub); err != nil {
		return nil, &TelegramLoginError{Stage: "identity", Reason: "invalid_signature"}
	}

	dec := json.NewDecoder(bytes.NewReader(payloadBytes))
	dec.UseNumber()
	var claims map[string]any
	if err := dec.Decode(&claims); err != nil || claims == nil {
		return nil, &TelegramLoginError{Stage: "identity", Reason: "invalid_claims"}
	}

	for _, reqKey := range []string{"iss", "aud", "sub", "exp", "iat", "nonce", "id"} {
		if _, exists := claims[reqKey]; !exists {
			return nil, &TelegramLoginError{Stage: "identity", Reason: "missing_" + reqKey}
		}
	}

	iss, ok := claims["iss"].(string)
	if !ok || iss != expectedIssuer {
		return nil, &TelegramLoginError{Stage: "identity", Reason: "invalid_issuer"}
	}

	if !matchesStrictAudience(claims["aud"], expectedAudience) {
		return nil, &TelegramLoginError{Stage: "identity", Reason: "invalid_audience"}
	}

	signedNonce, ok := claims["nonce"].(string)
	if !ok || !isPureASCII(signedNonce) || !hmac.Equal([]byte(expectedNonce), []byte(signedNonce)) {
		return nil, &TelegramLoginError{Stage: "identity", Reason: "nonce_mismatch"}
	}

	nowSec := now.Unix()
	iat, ok := parseStrictInt64Claim(claims["iat"])
	if !ok || iat < nowSec-630 || iat > nowSec+30 {
		return nil, &TelegramLoginError{Stage: "identity", Reason: "invalid_issued_at"}
	}
	exp, ok := parseStrictInt64Claim(claims["exp"])
	if !ok || exp <= iat || exp < nowSec-30 {
		return nil, &TelegramLoginError{Stage: "identity", Reason: "invalid_expiration"}
	}

	sub, ok := claims["sub"].(string)
	if !ok || !rePrintableASCII.MatchString(sub) {
		return nil, &TelegramLoginError{Stage: "identity", Reason: "invalid_subject"}
	}

	userID, ok := parseTelegramUserIDClaim(claims["id"])
	if !ok {
		return nil, &TelegramLoginError{Stage: "identity", Reason: "invalid_user_id"}
	}

	displayName := ""
	if rawName, ok := claims["name"].(string); ok {
		cleaned := strings.TrimSpace(strings.ReplaceAll(rawName, "\x00", ""))
		displayName = truncateRunesSimple(cleaned, 160)
	}
	if displayName == "" {
		displayName = "Telegram 用户"
	}

	username := ""
	if rawUser, ok := claims["preferred_username"].(string); ok && reTelegramUsername.MatchString(rawUser) {
		username = rawUser
	}

	pictureURL := ""
	if rawPic, ok := claims["picture"].(string); ok {
		pictureURL = rawPic
	}

	return &TelegramIdentity{
		TelegramID:  userID,
		Subject:     []byte(sub),
		DisplayName: displayName,
		Username:    username,
		PictureURL:  pictureURL,
	}, nil
}

func matchesStrictAudience(raw any, expected string) bool {
	if expected == "" {
		return false
	}
	switch v := raw.(type) {
	case string:
		return v == expected
	case []any:
		if len(v) != 1 {
			return false
		}
		s, ok := v[0].(string)
		return ok && s == expected
	}
	return false
}

func parseStrictInt64Claim(raw any) (int64, bool) {
	num, ok := raw.(json.Number)
	if !ok {
		return 0, false
	}
	s := num.String()
	if strings.ContainsAny(s, ".eE") {
		return 0, false
	}
	v, err := strconv.ParseInt(s, 10, 64)
	return v, err == nil
}

func parseTelegramUserIDClaim(raw any) (int64, bool) {
	switch v := raw.(type) {
	case json.Number:
		id, ok := parseStrictInt64Claim(v)
		return id, ok && id > 0
	case string:
		if !reCanonicalChannel.MatchString(v) {
			return 0, false
		}
		id, err := strconv.ParseInt(v, 10, 64)
		return id, err == nil && id > 0
	}
	return 0, false
}

func isPureASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] > 0x7f {
			return false
		}
	}
	return true
}

// ExchangeCode 向 Telegram OIDC /token 端点换取授权码并完成 JWKS 签名与身份声明核验（对齐 telegram_login.py:exchange_code）。
func ExchangeCode(ctx context.Context, cfg LoginConfig, code, verifier, nonce string) (*TelegramIdentity, error) {
	return ExchangeCodeWith(ctx, cfg, code, verifier, nonce, nil, defaultJWKSCache)
}

// ExchangeCodeWith 允许测试注入自定义 HTTP 客户端与 JWKS 缓存器执行完整 OIDC 换码流程。
func ExchangeCodeWith(ctx context.Context, cfg LoginConfig, code, verifier, nonce string, httpClient *http.Client, jwks *JWKSCache) (*TelegramIdentity, error) {
	callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if httpClient == nil {
		httpClient = &http.Client{
			Timeout: 10 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	}
	if jwks == nil {
		jwks = defaultJWKSCache
	}

	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", cfg.CallbackURL())
	form.Set("client_id", cfg.ClientID)
	form.Set("code_verifier", verifier)

	req, err := http.NewRequestWithContext(callCtx, http.MethodPost, cfg.effectiveIssuer()+"/token", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, &TelegramLoginError{Stage: "exchange", Reason: "invalid_request"}
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(cfg.ClientID, cfg.ClientSecret)

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, &TelegramLoginError{Stage: "exchange", Reason: "network_error"}
	}
	body, readErr := readBoundedBytes(resp.Body, 32*1024)
	_ = resp.Body.Close()
	if readErr != nil {
		return nil, &TelegramLoginError{Stage: "exchange", Reason: "response_too_large", Status: resp.StatusCode}
	}

	idToken, err := extractIDTokenFromResponse(resp.StatusCode, body)
	if err != nil {
		return nil, err
	}

	alg, kid, _, _, _, hdrErr := parseJWTHeader(idToken)
	if hdrErr != nil {
		return nil, &TelegramLoginError{Stage: "signing_key", Reason: "invalid_header"}
	}
	pubKey, keyErr := jwks.SigningKey(callCtx, kid, alg)
	if keyErr != nil {
		return nil, &TelegramLoginError{Stage: "signing_key", Reason: "jwks_lookup_failed"}
	}
	return VerifyIDToken(idToken, pubKey, alg, cfg.effectiveIssuer(), cfg.ClientID, nonce, time.Now().UTC())
}

func extractIDTokenFromResponse(status int, body []byte) (string, error) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", &TelegramLoginError{Stage: "exchange", Reason: "invalid_json", Status: status}
	}
	if payload == nil {
		return "", &TelegramLoginError{Stage: "exchange", Reason: "invalid_payload", Status: status}
	}
	if errVal, hasErr := payload["error"]; status != http.StatusOK || hasErr {
		reason := "provider_error"
		if errStr, ok := errVal.(string); ok && knownOAuthProviderErrors[errStr] {
			reason = errStr
		}
		return "", &TelegramLoginError{Stage: "exchange", Reason: reason, Status: status}
	}
	tok, ok := payload["id_token"].(string)
	if !ok || len(tok) < 1 || len(tok) > 16384 {
		return "", &TelegramLoginError{Stage: "exchange", Reason: "invalid_id_token", Status: status}
	}
	return tok, nil
}

func readBoundedBytes(r io.Reader, limit int) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > limit {
		return nil, errors.New("response exceeds maximum byte limit")
	}
	return data, nil
}

// ValidateImageBytes 严格校验图片文件头魔数（JPEG/PNG）、字节上限与像素尺寸上限（防解压炸弹，对齐 image_validation.py:validate_image）。
func ValidateImageBytes(data []byte, maxBytes int) error {
	_, err := DetectValidatedImageType(data, maxBytes)
	return err
}

// DetectValidatedImageType 验证图片合法性并返回其真实 MIME 类型（"image/jpeg" 或 "image/png"）。
func DetectValidatedImageType(data []byte, maxBytes int) (string, error) {
	if maxBytes <= 0 {
		maxBytes = MaxChannelAvatarBytes
	}
	if len(data) == 0 {
		return "", errors.New("图片内容为空")
	}
	if len(data) > maxBytes {
		return "", fmt.Errorf("图片大小超过上限（%d bytes）", maxBytes)
	}
	mediaType := SniffMediaType(data)
	if mediaType == "" {
		return "", errors.New("仅支持有效的 JPEG 或 PNG 图片")
	}
	imgCfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return "", errors.New("图片文件头或尺寸结构已损坏")
	}
	if (mediaType == "image/jpeg" && format != "jpeg") || (mediaType == "image/png" && format != "png") {
		return "", errors.New("图片格式与文件头不一致")
	}
	if imgCfg.Width <= 0 || imgCfg.Height <= 0 ||
		imgCfg.Width > maxImageDimension || imgCfg.Height > maxImageDimension ||
		int64(imgCfg.Width)*int64(imgCfg.Height) > maxImagePixels {
		return "", errors.New("图片像素尺寸超出安全限制")
	}
	return mediaType, nil
}

// IsAllowedTelegramAvatarURL 判断 URL 是否属于受信任的官方 Telegram 公开头像 CDN（*.telesco.pe/file/... 或 t.me/i/userpic/...）。
func IsAllowedTelegramAvatarURL(raw string) bool {
	return reTelescoAvatarURL.MatchString(raw) || reTMeUserpicURL.MatchString(raw)
}

// DownloadTelegramAvatarURL 使用抗 SSRF/DNS 重绑定且禁止重定向的客户端下载并校验用户公开头像（最大 512 KiB）。
func DownloadTelegramAvatarURL(ctx context.Context, rawURL string, customClient *http.Client) ([]byte, string) {
	if !IsAllowedTelegramAvatarURL(rawURL) {
		return nil, ""
	}
	cli := customClient
	if cli == nil {
		cli = NewSSRFSafeHTTPClient(4*time.Second, nil)
	} else {
		cp := *cli
		cli = &cp
	}
	cli.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}

	reqCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, ""
	}
	resp, err := cli.Do(req)
	if err != nil {
		return nil, ""
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, ""
	}
	ct, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if ct != "image/jpeg" && ct != "image/png" {
		return nil, ""
	}
	data, err := readBoundedBytes(resp.Body, MaxUserAvatarBytes)
	if err != nil {
		return nil, ""
	}
	mediaType, err := DetectValidatedImageType(data, MaxUserAvatarBytes)
	if err != nil {
		return nil, ""
	}
	return data, mediaType
}

// DownloadBotUserAvatar 通过 tg.DirectBotAPI 回退拉取用户 Telegram 头像（最接近 160px 且 <=512px、<=512KiB）。
func DownloadBotUserAvatar(ctx context.Context, botAPI *tg.DirectBotAPI, qaBotToken string, userID int64) ([]byte, string) {
	secret := strings.TrimSpace(qaBotToken)
	if secret == "" || userID <= 0 || !reBotTokenFormat.MatchString(secret) {
		return nil, ""
	}
	if botAPI == nil {
		botAPI = tg.NewDirectBotAPI(tg.WithDirectTimeout(5 * time.Second))
	}
	callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	photos, err := botAPI.GetUserProfilePhotos(callCtx, secret, userID, 0, 1)
	if err != nil || photos == nil || len(photos.Photos) == 0 || len(photos.Photos[0]) == 0 {
		return nil, ""
	}

	bestFileID := ""
	bestDist := int(^uint(0) >> 1)
	for _, p := range photos.Photos[0] {
		if p.FileID == "" || p.Width <= 0 || p.Width > 512 || p.FileSize > MaxUserAvatarBytes {
			continue
		}
		dist := p.Width - 160
		if dist < 0 {
			dist = -dist
		}
		if dist < bestDist {
			bestDist = dist
			bestFileID = p.FileID
		}
	}
	if bestFileID == "" {
		return nil, ""
	}

	fileMeta, err := botAPI.GetFile(callCtx, secret, bestFileID)
	if err != nil || fileMeta == nil || !reBotAvatarFilePath.MatchString(fileMeta.FilePath) {
		return nil, ""
	}
	data, err := botAPI.DownloadFile(callCtx, secret, fileMeta.FilePath, MaxUserAvatarBytes)
	if err != nil {
		return nil, ""
	}
	mediaType, err := DetectValidatedImageType(data, MaxUserAvatarBytes)
	if err != nil {
		return nil, ""
	}
	return data, mediaType
}

// LoadProfileAvatar 优先从 OIDC picture CDN 安全拉取用户头像，失败时自动回退至 QA Bot API 获取（对齐 telegram_login.py:load_profile_avatar）。
func LoadProfileAvatar(ctx context.Context, botAPI *tg.DirectBotAPI, qaBotToken string, userID int64, pictureURL string) ([]byte, string) {
	if data, mimeType := DownloadTelegramAvatarURL(ctx, pictureURL, nil); len(data) > 0 {
		return data, mimeType
	}
	return DownloadBotUserAvatar(ctx, botAPI, qaBotToken, userID)
}
