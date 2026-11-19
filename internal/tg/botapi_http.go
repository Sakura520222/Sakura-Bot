// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package tg

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

const (
	// DefaultDirectBotAPIBaseURL 官方 Telegram Bot API 根地址。
	DefaultDirectBotAPIBaseURL = "https://api.telegram.org"
	// DefaultDirectBotAPITimeout 直连 HTTP Bot API 默认超时（对齐 telegram_bot_api.py:_DEFAULT_TIMEOUT = 15.0）。
	DefaultDirectBotAPITimeout = 15 * time.Second
	// defaultMaxDownloadBytes 未显式指定上限时的 Bot API 文件下载硬上限（20MiB）。
	defaultMaxDownloadBytes = 20 << 20
)

var sharedDirectHTTPClient = &http.Client{}

// BotAPIError 表示 Telegram Bot API 返回 ok=false 或 HTTP 层失败的基础错误
// （对齐 core/infrastructure/telegram_bot_api.py:BotAPIError）。
// Error() 直接返回 Description（便于 "bot was blocked by the user" 等子串匹配）。
type BotAPIError struct {
	Description string
	ErrorCode   int
	Cause       error
}

// Error 实现 error。
func (e *BotAPIError) Error() string {
	if e == nil {
		return ""
	}
	return e.Description
}

// Unwrap 返回底层非敏感传输原因（如 context.DeadlineExceeded）。
func (e *BotAPIError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// RetryAfterError 表示 HTTP 429 速率限制错误（RetryAfter 为建议等待秒数）。
type RetryAfterError struct {
	BotAPIError
	RetryAfter float64
}

// Unwrap 允许 errors.As(err, &botErr) 向上匹配 *BotAPIError。
func (e *RetryAfterError) Unwrap() error {
	if e == nil {
		return nil
	}
	return &e.BotAPIError
}

// BadRequestError 表示 HTTP 400 客户端请求错误，Description 已剥离 "Bad Request: " 前缀。
type BadRequestError struct {
	BotAPIError
}

// Unwrap 允许 errors.As(err, &botErr) 向上匹配 *BotAPIError。
type NetworkAPIError struct {
	BotAPIError
}

func (e *BadRequestError) Unwrap() error {
	if e == nil {
		return nil
	}
	return &e.BotAPIError
}

func (e *NetworkAPIError) Unwrap() error {
	if e == nil {
		return nil
	}
	return &e.BotAPIError
}

// BotIDFromToken 从形如 "<bot_id>:<secret>" 的 Bot Token 前缀解析正整数 Bot ID（无网络请求）。
func BotIDFromToken(token string) (int64, bool) {
	trimmed := strings.TrimSpace(token)
	idPart, secretPart, ok := strings.Cut(trimmed, ":")
	if !ok || idPart == "" || secretPart == "" {
		return 0, false
	}
	if idPart[0] < '1' || idPart[0] > '9' {
		return 0, false
	}
	for i := 1; i < len(idPart); i++ {
		if idPart[i] < '0' || idPart[i] > '9' {
			return 0, false
		}
	}
	id, err := strconv.ParseInt(idPart, 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

// cleanBadRequestDescription 剥离 "Bad Request:" 前缀（不区分大小写），保持与 PTB 异常描述对齐。
func cleanBadRequestDescription(desc string) string {
	const prefix = "bad request:"
	if len(desc) >= len(prefix) && strings.EqualFold(desc[:len(prefix)], prefix) {
		return strings.TrimSpace(desc[len(prefix):])
	}
	return desc
}

// DirectBotOption 配置 DirectBotAPI。
type DirectBotOption func(*DirectBotAPI)

// WithDirectBaseURL 覆写 API 根地址（测试指向 httptest.Server 用）。
func WithDirectBaseURL(baseURL string) DirectBotOption {
	return func(d *DirectBotAPI) {
		d.baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	}
}

// WithDirectHTTPClient 注入自定义 *http.Client。
func WithDirectHTTPClient(client *http.Client) DirectBotOption {
	return func(d *DirectBotAPI) {
		d.http = client
	}
}

// WithDirectTimeout 覆写单次调用默认超时（<=0 时回退 DefaultDirectBotAPITimeout=15s）。
func WithDirectTimeout(timeout time.Duration) DirectBotOption {
	return func(d *DirectBotAPI) {
		d.timeout = timeout
	}
}

// DirectBotAPI 轻量级 stdlib net/http Telegram Bot API 客户端（对齐 telegram_bot_api.py）。
// 零值可直接安全调用（自动使用官方根地址、共享连接池与 15s 默认超时）。
type DirectBotAPI struct {
	baseURL string
	http    *http.Client
	timeout time.Duration
}

// NewDirectBotAPI 构造 DirectBotAPI。
func NewDirectBotAPI(opts ...DirectBotOption) *DirectBotAPI {
	d := &DirectBotAPI{
		baseURL: DefaultDirectBotAPIBaseURL,
		http:    sharedDirectHTTPClient,
		timeout: DefaultDirectBotAPITimeout,
	}
	for _, opt := range opts {
		opt(d)
	}
	if d.baseURL == "" {
		d.baseURL = DefaultDirectBotAPIBaseURL
	}
	if d.http == nil {
		d.http = sharedDirectHTTPClient
	}
	if d.timeout <= 0 {
		d.timeout = DefaultDirectBotAPITimeout
	}
	return d
}

func (d *DirectBotAPI) effectiveBaseURL() string {
	if d != nil && d.baseURL != "" {
		return d.baseURL
	}
	return DefaultDirectBotAPIBaseURL
}

func (d *DirectBotAPI) effectiveClient() *http.Client {
	if d != nil && d.http != nil {
		return d.http
	}
	return sharedDirectHTTPClient
}

func (d *DirectBotAPI) effectiveTimeout() time.Duration {
	if d != nil && d.timeout > 0 {
		return d.timeout
	}
	return DefaultDirectBotAPITimeout
}

// DirectSendMessageParams 是 DirectBotAPI.SendMessage 的入参。
type DirectSendMessageParams struct {
	ChatID                any    `json:"chat_id"`
	MessageThreadID       int    `json:"message_thread_id,omitempty"`
	Text                  string `json:"text"`
	ParseMode             string `json:"parse_mode,omitempty"`
	DisableWebPagePreview bool   `json:"disable_web_page_preview,omitempty"`
	DisableNotification   bool   `json:"disable_notification,omitempty"`
	ReplyToMessageID      int64  `json:"reply_to_message_id,omitempty"`
	ReplyMarkup           any    `json:"reply_markup,omitempty"`
}

type botAPIEnvelope struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result"`
	Description string          `json:"description"`
	ErrorCode   int             `json:"error_code"`
	Parameters  *struct {
		RetryAfter *float64 `json:"retry_after"`
	} `json:"parameters"`
}

// CallMethod 调用任意 Telegram Bot API 方法，并将 result 字段反序列化到 out（out 为 nil 时忽略返回值）。
func (d *DirectBotAPI) CallMethod(ctx context.Context, token, method string, payload any, out any) error {
	token = strings.TrimSpace(token)
	method = strings.Trim(strings.TrimSpace(method), "/")
	if token == "" {
		return &BadRequestError{BotAPIError: BotAPIError{Description: "empty bot token", ErrorCode: http.StatusBadRequest}}
	}
	if method == "" {
		return &BadRequestError{BotAPIError: BotAPIError{Description: "empty bot method", ErrorCode: http.StatusBadRequest}}
	}

	var reqBody []byte
	var err error
	if payload == nil {
		reqBody = []byte("{}")
	} else {
		reqBody, err = json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("encode telegram payload for %s: %w", method, err)
		}
	}

	callCtx, cancel := context.WithTimeout(ctx, d.effectiveTimeout())
	defer cancel()

	endpoint := fmt.Sprintf("%s/bot%s/%s", d.effectiveBaseURL(), token, method)
	httpReq, err := http.NewRequestWithContext(callCtx, http.MethodPost, endpoint, bytes.NewReader(reqBody))
	if err != nil {
		return &NetworkAPIError{BotAPIError: BotAPIError{Description: "Network: invalid request"}}
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	resp, err := d.effectiveClient().Do(httpReq)
	if err != nil {
		return classifyTransportError(err)
	}
	defer func() { _ = resp.Body.Close() }()

	rawBytes, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return classifyTransportError(err)
	}

	var env botAPIEnvelope
	if err := json.Unmarshal(rawBytes, &env); err != nil {
		return &NetworkAPIError{
			BotAPIError: BotAPIError{
				Description: fmt.Sprintf("Invalid JSON (HTTP %d)", resp.StatusCode),
				ErrorCode:   resp.StatusCode,
			},
		}
	}

	if env.OK {
		if out != nil && len(env.Result) > 0 && string(env.Result) != "null" {
			if err := json.Unmarshal(env.Result, out); err != nil {
				return fmt.Errorf("decode telegram %s result: %w", method, err)
			}
		}
		return nil
	}

	desc := env.Description
	if desc == "" {
		desc = fmt.Sprintf("HTTP %d", resp.StatusCode)
	}
	code := env.ErrorCode
	if code == 0 {
		code = resp.StatusCode
	}

	if resp.StatusCode == http.StatusTooManyRequests || code == http.StatusTooManyRequests {
		retryAfter := 1.0
		if env.Parameters != nil && env.Parameters.RetryAfter != nil {
			retryAfter = *env.Parameters.RetryAfter
		}
		return &RetryAfterError{
			BotAPIError: BotAPIError{Description: desc, ErrorCode: http.StatusTooManyRequests},
			RetryAfter:  retryAfter,
		}
	}
	if resp.StatusCode == http.StatusBadRequest || code == http.StatusBadRequest {
		return &BadRequestError{
			BotAPIError: BotAPIError{Description: cleanBadRequestDescription(desc), ErrorCode: http.StatusBadRequest},
		}
	}
	return &BotAPIError{Description: desc, ErrorCode: code}
}

// SendMessage 调用 sendMessage 并返回新消息的 message_id。
func (d *DirectBotAPI) SendMessage(ctx context.Context, token string, req DirectSendMessageParams) (int64, error) {
	var res struct {
		MessageID int64 `json:"message_id"`
	}
	if err := d.CallMethod(ctx, token, "sendMessage", req, &res); err != nil {
		return 0, err
	}
	return res.MessageID, nil
}

// SendRichMessage sends a native Rich Message through the selected bot token.
func (d *DirectBotAPI) SendRichMessage(ctx context.Context, token string, req bot.SendRichMessageParams) (int64, error) {
	var res struct {
		MessageID int64 `json:"message_id"`
	}
	if err := d.CallMethod(ctx, token, "sendRichMessage", req, &res); err != nil {
		return 0, err
	}
	return res.MessageID, nil
}

// BanChatMember 调用 banChatMember 封禁群成员（untilDate > 0 时附带 until_date）。
func (d *DirectBotAPI) BanChatMember(ctx context.Context, token string, chatID, userID int64, revokeMessages bool, untilDate int64) error {
	payload := map[string]any{
		"chat_id":         chatID,
		"user_id":         userID,
		"revoke_messages": revokeMessages,
	}
	if untilDate > 0 {
		payload["until_date"] = untilDate
	}
	return d.CallMethod(ctx, token, "banChatMember", payload, nil)
}

// GetMe 调用 getMe 获取当前 Bot 账号信息。
func (d *DirectBotAPI) GetMe(ctx context.Context, token string) (*models.User, error) {
	var user models.User
	if err := d.CallMethod(ctx, token, "getMe", nil, &user); err != nil {
		return nil, err
	}
	return &user, nil
}

// GetChat 调用 getChat 获取聊天完整信息（chatID 支持 int64 或 "@channel" 字符串）。
func (d *DirectBotAPI) GetChat(ctx context.Context, token string, chatID any) (*models.ChatFullInfo, error) {
	var chat models.ChatFullInfo
	if err := d.CallMethod(ctx, token, "getChat", map[string]any{"chat_id": chatID}, &chat); err != nil {
		return nil, err
	}
	return &chat, nil
}

// GetChatMember 调用 getChatMember 查询成员状态与权限。
func (d *DirectBotAPI) GetChatMember(ctx context.Context, token string, chatID any, userID int64) (*models.ChatMember, error) {
	var member models.ChatMember
	payload := map[string]any{
		"chat_id": chatID,
		"user_id": userID,
	}
	if err := d.CallMethod(ctx, token, "getChatMember", payload, &member); err != nil {
		return nil, err
	}
	return &member, nil
}

// GetUserProfilePhotos 调用 getUserProfilePhotos 获取用户头像列表。
func (d *DirectBotAPI) GetUserProfilePhotos(ctx context.Context, token string, userID int64, offset, limit int) (*models.UserProfilePhotos, error) {
	payload := map[string]any{
		"user_id": userID,
	}
	if offset > 0 {
		payload["offset"] = offset
	}
	if limit > 0 {
		payload["limit"] = limit
	}
	var photos models.UserProfilePhotos
	if err := d.CallMethod(ctx, token, "getUserProfilePhotos", payload, &photos); err != nil {
		return nil, err
	}
	return &photos, nil
}

// GetFile 调用 getFile 获取可下载文件元数据。
func (d *DirectBotAPI) GetFile(ctx context.Context, token, fileID string) (*models.File, error) {
	var file models.File
	if err := d.CallMethod(ctx, token, "getFile", map[string]any{"file_id": fileID}, &file); err != nil {
		return nil, err
	}
	return &file, nil
}

// DownloadFile 从 {baseURL}/file/bot{token}/{filePath} 下载文件字节（禁止重定向、强制字节上限保护）。
func (d *DirectBotAPI) DownloadFile(ctx context.Context, token, filePath string, maxBytes int64) ([]byte, error) {
	token = strings.TrimSpace(token)
	cleanPath := strings.TrimPrefix(strings.TrimSpace(filePath), "/")
	if token == "" || cleanPath == "" || strings.Contains(cleanPath, "..") || strings.ContainsAny(cleanPath, `\?#`) {
		return nil, &BadRequestError{
			BotAPIError: BotAPIError{Description: "invalid file path or token", ErrorCode: http.StatusBadRequest},
		}
	}
	if maxBytes <= 0 {
		maxBytes = defaultMaxDownloadBytes
	}

	callCtx, cancel := context.WithTimeout(ctx, d.effectiveTimeout())
	defer cancel()

	downloadURL := fmt.Sprintf("%s/file/bot%s/%s", d.effectiveBaseURL(), token, cleanPath)
	httpReq, err := http.NewRequestWithContext(callCtx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return nil, &NetworkAPIError{BotAPIError: BotAPIError{Description: "Network: invalid download request"}}
	}

	// 复制客户端但禁止自动跟随重定向，防止携带 token 的请求或响应跳转到非预期地址。
	baseCli := d.effectiveClient()
	noRedirectCli := *baseCli
	noRedirectCli.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}

	resp, err := noRedirectCli.Do(httpReq)
	if err != nil {
		return nil, classifyTransportError(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, &BotAPIError{
			Description: fmt.Sprintf("HTTP %d", resp.StatusCode),
			ErrorCode:   resp.StatusCode,
		}
	}
	if resp.ContentLength > maxBytes {
		return nil, fmt.Errorf("file size %d exceeds maxBytes %d", resp.ContentLength, maxBytes)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, classifyTransportError(err)
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("downloaded file exceeds maxBytes %d", maxBytes)
	}
	return data, nil
}

// classifyTransportError 将底层 net/http 错误归类为 *NetworkAPIError，并剥离含 token URL 的 *url.Error。
func classifyTransportError(err error) error {
	cause := err
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Err != nil {
		cause = urlErr.Err
	}
	var netErr net.Error
	if errors.Is(cause, context.DeadlineExceeded) || (errors.As(cause, &netErr) && netErr.Timeout()) {
		return &NetworkAPIError{
			BotAPIError: BotAPIError{
				Description: "Timeout: context deadline exceeded",
				Cause:       cause,
			},
		}
	}
	return &NetworkAPIError{
		BotAPIError: BotAPIError{
			Description: "Network: transport error",
			Cause:       cause,
		},
	}
}
