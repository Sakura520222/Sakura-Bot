// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Sakura520222/Sakura-Bot/internal/contracts"
)

// 模型列表与连接测试默认超时（对齐 core/web_api/routes/ai_profiles.py:158,188）。
const (
	DefaultListModelsTimeout     = 15 * time.Second
	DefaultTestConnectionTimeout = 20 * time.Second
	maxCompleteBodyBytes         = 32 << 20 // 32MiB 响应上限
)

// ResponseFormat 控制输出格式（如 {"type":"json_object"}）。
type ResponseFormat struct {
	Type       string          `json:"type"`
	JSONSchema json.RawMessage `json:"json_schema,omitempty"`
}

// CompleteRequest /chat/completions 请求参数。
// Temperature 与 MaxTokens 使用指针：nil 时在请求 JSON 中完全省略字段，
// 显式传 0（如 Float64(0)）时原样编码为 0，对齐 summary_pipeline.py 与 JSON 分类器调用语义。
type CompleteRequest struct {
	Model          string          `json:"model,omitempty"`
	Messages       []Message       `json:"messages"`
	Temperature    *float64        `json:"temperature,omitempty"`
	MaxTokens      *int            `json:"max_tokens,omitempty"`
	ResponseFormat *ResponseFormat `json:"response_format,omitempty"`
	Tools          json.RawMessage `json:"tools,omitempty"`
	ToolChoice     string          `json:"tool_choice,omitempty"`
}

// Usage 统计单次模型调用的 token 用量。
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// CompleteResponse /chat/completions 解析结果。
type CompleteResponse struct {
	ID           string     `json:"id"`
	Model        string     `json:"model"`
	Content      string     `json:"content"`
	ToolCalls    []ToolCall `json:"tool_calls,omitempty"`
	FinishReason string     `json:"finish_reason"`
	Usage        Usage      `json:"usage"`
}

// Float64 构造 float64 指针（用于 CompleteRequest.Temperature）。
func Float64(v float64) *float64 { return &v }

// Int 构造 int 指针（用于 CompleteRequest.MaxTokens）。
func Int(v int) *int { return &v }

// PurposeConfigProvider 抽象按用途（purpose）读取运行时 AI 配置的提供方，
// 同时兼容返回 llm.AIConfig 的接口与返回 contracts.AIConf 的 *config.Config。
type PurposeConfigProvider[C AIConfig | contracts.AIConf] interface {
	EffectiveAIConfig(purposes ...string) C
}

// NewPurposeClient 构造绑定指定用途（如 "summary"、"poll"、"wiki"、"guard"）的 LLM 客户端。
// 每次调用 StreamChat / CompleteChat 时均重新读取该用途的最新生效配置。
func NewPurposeClient[C AIConfig | contracts.AIConf, P PurposeConfigProvider[C]](cfg P, purpose string, opts ...Option) *Client {
	provider := AIConfigFunc(func() AIConfig {
		raw := any(cfg.EffectiveAIConfig(purpose))
		switch v := raw.(type) {
		case AIConfig:
			return v
		case contracts.AIConf:
			return AIConfig{
				APIKey:          v.APIKey,
				BaseURL:         v.BaseURL,
				Model:           v.Model,
				SupportsVision:  v.SupportsVision,
				ReasoningEffort: v.ReasoningEffort,
			}
		default:
			return AIConfig{}
		}
	})
	return NewClient(provider, opts...)
}

type chatRequestWire struct {
	Model           string          `json:"model"`
	Messages        []Message       `json:"messages"`
	Temperature     *float64        `json:"temperature,omitempty"`
	MaxTokens       *int            `json:"max_tokens,omitempty"`
	ResponseFormat  *ResponseFormat `json:"response_format,omitempty"`
	ReasoningEffort string          `json:"reasoning_effort,omitempty"`
	Tools           json.RawMessage `json:"tools,omitempty"`
	ToolChoice      string          `json:"tool_choice,omitempty"`
	Stream          bool            `json:"stream"`
	StreamOptions   *streamOptions  `json:"stream_options,omitempty"`
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

// CompleteChat collects a chat completion from the shared SSE client.
// It returns no partial response when the stream breaks or omits its terminal metadata.
func (c *Client) CompleteChat(ctx context.Context, req CompleteRequest) (*CompleteResponse, error) {
	if _, pinned := PinnedConfig(ctx); !pinned {
		ctx = WithConfig(ctx, c.provider.EffectiveAIConfig())
	}
	stream, err := c.openChatStream(ctx, req, true, maxCompleteBodyBytes)
	var apiErr *APIError
	if errors.As(err, &apiErr) && (apiErr.StatusCode == http.StatusBadRequest || apiErr.StatusCode == http.StatusUnprocessableEntity) {
		body := strings.ToLower(apiErr.Body)
		if strings.Contains(body, "stream_options") || strings.Contains(body, "include_usage") {
			stream, err = c.openChatStream(ctx, req, false, maxCompleteBodyBytes)
		}
	}
	if err != nil {
		return nil, err
	}
	defer stream.Close()

	var content strings.Builder
	toolCalls := make(map[int]*ToolCallSlot)
	for {
		delta, err := stream.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		for _, call := range delta.ToolCalls {
			AccumulateToolCall(toolCalls, call)
		}
		content.WriteString(delta.Content)
	}

	result := stream.result
	result.FinishReason = stream.Diagnostics().FinishReason
	if stream.Diagnostics().MalformedChunks > 0 || result.FinishReason == "" || result.FinishReason == "unknown" {
		return nil, fmt.Errorf("LLM 流式响应不完整（finish_reason=%q）", result.FinishReason)
	}
	result.Content = content.String()
	if len(toolCalls) > 0 {
		result.ToolCalls = MaterializeToolCalls(toolCalls)
	}
	return &result, nil
}

// ListModels 请求 GET {baseURL}/models（15s 超时），返回去重且升序排列的合法模型 ID 列表
// （对齐 core/web_api/routes/ai_profiles.py:153-165, 314-332）。
func ListModels(ctx context.Context, baseURL, apiKey string, httpClient *http.Client) ([]string, error) {
	return listModelsWithTimeout(ctx, baseURL, apiKey, httpClient, DefaultListModelsTimeout)
}

func listModelsWithTimeout(ctx context.Context, baseURL, apiKey string, httpClient *http.Client, timeout time.Duration) ([]string, error) {
	cleanBase, err := validateEndpointBaseURL(baseURL)
	if err != nil {
		return nil, err
	}

	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(reqCtx, http.MethodGet, cleanBase+"/models", nil)
	if err != nil {
		return nil, fmt.Errorf("构造模型列表请求失败")
	}
	httpReq.Header.Set("Accept", "application/json")
	if apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	}

	cli := httpClient
	if cli == nil {
		cli = &http.Client{Timeout: timeout}
	}

	resp, err := cli.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("获取模型列表请求失败")
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, &APIError{StatusCode: resp.StatusCode, Body: redactSecret(string(snippet), apiKey)}
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("读取模型列表响应失败")
	}
	var parsed struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("解析模型列表 JSON 失败")
	}

	seen := make(map[string]struct{}, len(parsed.Data))
	out := make([]string, 0, len(parsed.Data))
	for _, item := range parsed.Data {
		if !isSafeModelID(item.ID) {
			continue
		}
		if _, exists := seen[item.ID]; exists {
			continue
		}
		seen[item.ID] = struct{}{}
		out = append(out, item.ID)
	}
	sort.Strings(out)
	return out, nil
}

// TestConnection 执行 AI 档案/厂商测试（20s 超时，对齐 ai_profiles.py:168-199, 334-359）：
//   - kind == "connection"：调用 GET {baseURL}/models 验证连通与鉴权；
//   - kind == "call"：发送最小 1-token CompleteChat 请求验证模型调用权限。
func TestConnection(ctx context.Context, baseURL, apiKey, model, kind string, httpClient *http.Client) error {
	cleanBase, err := validateEndpointBaseURL(baseURL)
	if err != nil {
		return err
	}

	reqCtx, cancel := context.WithTimeout(ctx, DefaultTestConnectionTimeout)
	defer cancel()

	switch kind {
	case "connection":
		_, err := listModelsWithTimeout(reqCtx, cleanBase, apiKey, httpClient, DefaultTestConnectionTimeout)
		return err
	case "call":
		if hasControlChars(model) || utf8.RuneCountInString(model) > 256 {
			return fmt.Errorf("无效的模型名称")
		}
		trimmedModel := strings.TrimSpace(model)
		if trimmedModel == "" {
			return fmt.Errorf("模型调用测试必须指定模型 ID")
		}
		var opts []Option
		if httpClient != nil {
			opts = append(opts, WithHTTPClient(httpClient))
		}
		cli := NewClient(AIConfigFunc(func() AIConfig {
			return AIConfig{
				APIKey:  apiKey,
				BaseURL: cleanBase,
				Model:   trimmedModel,
			}
		}), opts...)
		_, err := cli.CompleteChat(reqCtx, CompleteRequest{
			Model: trimmedModel,
			Messages: []Message{
				{Role: "user", Content: Str("Reply with only OK.")},
			},
			MaxTokens: Int(1),
		})
		return err
	default:
		return fmt.Errorf("不支持的测试类型: %q", kind)
	}
}

func validateEndpointBaseURL(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || len(trimmed) > 2048 || hasControlChars(trimmed) {
		return "", fmt.Errorf("无效的 HTTP(S) 服务地址")
	}
	u, err := url.Parse(trimmed)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" ||
		u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("无效的 HTTP(S) 服务地址")
	}
	return strings.TrimRight(trimmed, "/"), nil
}

func isSafeModelID(id string) bool {
	if id == "" || strings.TrimSpace(id) != id || utf8.RuneCountInString(id) > 256 {
		return false
	}
	return !hasControlChars(id)
}

func hasControlChars(s string) bool {
	for _, r := range s {
		if r < 32 || r == 127 {
			return true
		}
	}
	return false
}

func redactSecret(text, secret string) string {
	if secret != "" && len(secret) >= 4 {
		return strings.ReplaceAll(text, secret, "[REDACTED]")
	}
	return text
}
