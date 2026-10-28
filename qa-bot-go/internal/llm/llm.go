// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

// Package llm 实现手写 net/http 的 OpenAI 兼容流式客户端（设计文档 §6.1）。
//
// 仅覆盖 qa_engine_v3.py 实际使用的 /chat/completions stream=true 子集，
// 通过 bufio.Scanner 解析 SSE 的 data: 行与 [DONE] 终止标记，不引入任何
// OpenAI SDK（依赖白名单约束）。超时对齐设计：连接 30s、响应头 60s、
// 空闲读 120s（LLM 长思考间隙）。
//
// 运行时配置（key/baseURL/model）每次请求都经 AIConfigProvider 重新获取，
// 复刻 core/config/ai_runtime.py::effective_ai_config 的热更新语义。
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"
)

// 超时常量（设计 §6.1：连接 30s / 响应头 60s / 空闲读 120s）。
const (
	DefaultDialTimeout        = 30 * time.Second
	DefaultResponseHdrTimeout = 60 * time.Second
	DefaultIdleReadTimeout    = 120 * time.Second

	// maxScanTokenSize 单行 data 载荷上限：4MB，防止异常超长行撑爆内存。
	maxScanTokenSize = 4 << 20
)

// AIConfig 每请求生效的 AI 运行时配置三元组。
// 对齐 core/config/ai_runtime.py::AIConnectionSettings。
type AIConfig struct {
	APIKey  string
	BaseURL string
	Model   string
}

// AIConfigProvider 由消费方（main 装配）注入的窄接口：客户端在每次
// StreamChat 前调用，复刻 ai_client.py::get_ai_runtime 的"每请求取
// 运行时配置"语义。实现方通常是 internal/config 的 EffectiveAIConfig。
type AIConfigProvider interface {
	EffectiveAIConfig() AIConfig
}

// AIConfigFunc 让普通函数直接充当 AIConfigProvider 的适配器。
type AIConfigFunc func() AIConfig

// EffectiveAIConfig 实现 AIConfigProvider。
func (f AIConfigFunc) EffectiveAIConfig() AIConfig { return f() }

// Client 手写 SSE 的 OpenAI 兼容流式客户端。零值不可用，须经 NewClient 构造。
// 非并发安全的使用约束仅作用于 Stream 迭代；Client 本身可并发复用。
type Client struct {
	provider AIConfigProvider
	http     *http.Client
	logger   *slog.Logger
	idle     time.Duration
}

// Option 是 Client 的构造选项。
type Option func(*Client)

// WithHTTPClient 替换底层 http.Client（测试注入自定义 Transport 用）。
func WithHTTPClient(h *http.Client) Option { return func(c *Client) { c.http = h } }

// WithLogger 注入 slog 日志器；缺省为 slog.Default()。
func WithLogger(l *slog.Logger) Option { return func(c *Client) { c.logger = l } }

// WithIdleReadTimeout 调整空闲读超时（缺省 DefaultIdleReadTimeout=120s；
// 仅测试注入小值用，生产保持默认对齐设计）。
func WithIdleReadTimeout(d time.Duration) Option { return func(c *Client) { c.idle = d } }

// NewClient 构造客户端。provider 不可为 nil。
func NewClient(provider AIConfigProvider, opts ...Option) *Client {
	transport := &http.Transport{
		// 连接超时 30s
		DialContext: (&net.Dialer{Timeout: DefaultDialTimeout}).DialContext,
		// 响应头超时 60s（连接建立后等待服务端返回响应头）
		ResponseHeaderTimeout: DefaultResponseHdrTimeout,
	}
	c := &Client{
		provider: provider,
		http:     &http.Client{Transport: transport},
		logger:   slog.Default(),
		idle:     DefaultIdleReadTimeout,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// chatRequestWire 是发往服务端的请求体。
// stream 恒为 true（本客户端只服务流式路径）；
// Tools 用 json.RawMessage 原样透传工具 schema 数组，避免二次编解码造成漂移；
// omitempty 保证无工具请求体与 Python 不带 tools/tool_choice 的调用点逐字段一致。
type chatRequestWire struct {
	Model       string          `json:"model"`
	Messages    []Message       `json:"messages"`
	Tools       json.RawMessage `json:"tools,omitempty"`
	ToolChoice  string          `json:"tool_choice,omitempty"`
	Temperature float64         `json:"temperature"`
	Stream      bool            `json:"stream"`
}

// APIError 非 200 响应错误。
type APIError struct {
	StatusCode int
	Body       string
}

// Error 实现 error。
func (e *APIError) Error() string {
	return fmt.Sprintf("LLM API 返回非 200 状态 %d: %s", e.StatusCode, e.Body)
}

// StreamChat 发起一次 /chat/completions 流式请求（设计 §6.1）。
//
// 配置（key/baseURL/model）在本次调用开始时经 provider 重新获取。
// 成功返回 *Stream；非 200 返回 *APIError；网络/协议错误返回包装后的 error。
// 调用方须在消费完毕后 defer stream.Close()。
func (c *Client) StreamChat(ctx context.Context, req ChatRequest) (*Stream, error) {
	cfg := c.provider.EffectiveAIConfig()
	baseURL := strings.TrimSuffix(cfg.BaseURL, "/")

	body, err := json.Marshal(chatRequestWire{
		Model:       cfg.Model,
		Messages:    req.Messages,
		Tools:       req.Tools,
		ToolChoice:  req.ToolChoice,
		Temperature: req.Temperature,
		Stream:      true,
	})
	if err != nil {
		return nil, fmt.Errorf("编码 LLM 请求失败: %w", err)
	}

	// 请求级派生上下文：空闲读超时（120s 无任何数据）经定时器触发取消，
	// 中断阻塞中的响应体读取；每次成功读取都会重置定时器。
	// 注意：必须先派生再构造请求（NewRequestWithContext 挂 reqCtx），
	// 否则取消无法中断已建立的响应体读取（idle 超时会永久挂死）。
	reqCtx, cancel := context.WithCancel(ctx)
	timer := time.AfterFunc(c.idle, cancel)

	httpReq, err := http.NewRequestWithContext(reqCtx, http.MethodPost,
		baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		cancel()
		return nil, fmt.Errorf("构造 LLM 请求失败: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("LLM 请求失败: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		cancel()
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, &APIError{StatusCode: resp.StatusCode, Body: string(snippet)}
	}

	s := &Stream{
		ctx:     reqCtx,
		cancel:  cancel,
		timer:   timer,
		idle:    c.idle,
		resp:    resp,
		scanner: bufioScanner(&idleReader{r: resp.Body, timer: timer, idle: c.idle}),
	}
	return s, nil
}
