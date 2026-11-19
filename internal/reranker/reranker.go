// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

// Package reranker 实现 SiliconFlow rerank 客户端，
// 逐行为对齐 core/ai/reranker.py（bge-reranker-v2-m3，超时 30s，见 :74）。
//
// 关键语义对齐：Python httpx 对非 200 响应不抛异常（无 raise_for_status），
// 因此"响应无 results 字段"的格式异常分支对任何状态码都生效；
// 仅网络错误/超时/JSON 解析失败/响应下标越界走"重排序失败"错误分支。
// 两条分支均降级返回候选前缀（Python 异常兜底语义），不向调用方上抛。
package reranker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"reflect"
	"strconv"
	"time"
)

// 环境变量键与默认值（对齐 reranker.py:30-34）。
const (
	EnvAPIKey  = "RERANKER_API_KEY"
	EnvAPIBase = "RERANKER_API_BASE"
	EnvModel   = "RERANKER_MODEL"
	EnvTopK    = "RERANKER_TOP_K"
	EnvFinalK  = "RERANKER_FINAL"

	DefaultAPIBase = "https://api.siliconflow.cn/v1/rerank"
	DefaultModel   = "BAAI/bge-reranker-v2-m3"
	DefaultTopK    = 20
	DefaultFinalK  = 5

	// DefaultTimeout 对齐 reranker.py:74 的 httpx.Client(timeout=30.0)。
	DefaultTimeout = 30 * time.Second
)

// Document 候选文档，对齐 Python candidates 中 dict 的语义：
// 至少包含 summary_text（重排检索字段，缺失按空串）；
// 运行时还会携带 summary_id/post_url 等键，由工具层序列化。
type Document map[string]any

// EnvReader 环境变量读取的窄接口（消费者包内定义；由 main 装配为
// internal/config 的实现，携带 data/.env override 语义）。
// GetIntDef 采用 int64 与 internal/config.Env 的签名一致，使 *config.Env
// 可零适配直接装配。
type EnvReader interface {
	Get(key string) string
	GetIntDef(key string, def int64) int64
}

// OSEnv 直接读进程环境的默认实现。
type OSEnv struct{}

// Get 实现 EnvReader。
func (OSEnv) Get(key string) string { return os.Getenv(key) }

// GetIntDef 实现 EnvReader。
func (OSEnv) GetIntDef(key string, def int64) int64 {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	return def
}

// Reranker 重排序器（对齐 core/ai/reranker.py::Reranker）。
type Reranker struct {
	apiKey  string
	apiBase string
	model   string
	topK    int64
	finalK  int64
	client  *http.Client
	logger  *slog.Logger
}

// Option 是 Reranker 的构造选项。
type Option func(*Reranker)

// WithLogger 注入 slog 日志器；缺省 slog.Default()。
func WithLogger(l *slog.Logger) Option { return func(r *Reranker) { r.logger = l } }

// WithHTTPClient 替换底层 http.Client（测试注入）。
func WithHTTPClient(c *http.Client) Option { return func(r *Reranker) { r.client = c } }

// New 读取 env 构造重排序器（对齐 reranker.py:28-39：构造期读一次 env；
// 日志文案逐字对齐 :36-39）。
func New(env EnvReader, opts ...Option) *Reranker {
	r := &Reranker{
		apiKey:  env.Get(EnvAPIKey),
		apiBase: env.Get(EnvAPIBase),
		model:   env.Get(EnvModel),
		topK:    env.GetIntDef(EnvTopK, DefaultTopK),
		finalK:  env.GetIntDef(EnvFinalK, DefaultFinalK),
		client:  &http.Client{Timeout: DefaultTimeout},
		logger:  slog.Default(),
	}
	if r.apiBase == "" {
		r.apiBase = DefaultAPIBase
	}
	if r.model == "" {
		r.model = DefaultModel
	}
	for _, opt := range opts {
		opt(r)
	}
	if r.apiKey == "" {
		r.logger.Warn("未设置RERANKER_API_KEY，重排序功能将不可用")
	} else {
		r.logger.Info("Reranker初始化成功: " + r.model)
	}
	return r
}

// Config 返回构造期配置快照（供上层诊断/测试断言）。
func (r *Reranker) Config() Config {
	return Config{
		APIKey: r.apiKey, APIBase: r.apiBase, Model: r.model, TopK: r.topK, FinalK: r.finalK,
	}
}

// Config 构造期配置快照。
type Config struct {
	APIKey  string
	APIBase string
	Model   string
	TopK    int64
	FinalK  int64
}

// IsAvailable 检查Reranker服务是否可用（对齐 reranker.py:41-43）。
func (r *Reranker) IsAvailable() bool { return r.apiKey != "" }

// rerankRequest SiliconFlow rerank 请求体。
type rerankRequest struct {
	Model     string   `json:"model"`
	Query     string   `json:"query"`
	Documents []string `json:"documents"`
	TopN      int      `json:"top_n"`
}

// rerankResponse 响应体（只取所需字段）。
type rerankResponse struct {
	Results []struct {
		Index          *int    `json:"index"`
		RelevanceScore float64 `json:"relevance_score"`
	} `json:"results"`
}

// typedError 带语义化类型名的错误，用于把可预期的失败（响应下标越界/缺失）
// 对齐到 Python 异常类名的日志形态（"{type(e).__name__}: {e}"）。
type typedError struct {
	typeName string
	msg      string
}

// Error 实现 error。
func (e *typedError) Error() string { return e.msg }

// TypeName 返回对齐 Python 的异常类名。
func (e *typedError) TypeName() string { return e.typeName }

// Rerank 对检索结果重排序（对齐 reranker.py:45-109）。
//
// topK<=0 等价 Python 的 top_k=None（使用配置 final_k）。
// 成功时返回候选文档的浅拷贝（对齐 dict.copy()），按服务端相关性降序，
// 每份拷贝追加 rerank_score 键；输入 candidates 不被修改。
// 服务不可用/格式异常/请求失败时降级返回 candidates 前 topK 个浅拷贝
// （Python 切片自动截断长度，此处 min 收口等价）。
func (r *Reranker) Rerank(ctx context.Context, query string, candidates []Document, topK int) []Document {
	if r.apiKey == "" {
		r.logger.Warn("Reranker服务不可用，返回原始结果")
		limit := topK
		if limit <= 0 {
			limit = int(r.finalK) // 对齐 `top_k or self.final_k`
		}
		return truncateCandidates(candidates, limit)
	}
	if len(candidates) == 0 {
		return []Document{}
	}
	if topK <= 0 {
		topK = int(r.finalK)
	}

	documents := make([]string, len(candidates))
	for i, doc := range candidates {
		if s, ok := doc["summary_text"].(string); ok {
			documents[i] = s
		}
	}

	result, err := r.callRerank(ctx, query, documents, topK)
	if err != nil {
		r.logger.Error(fmt.Sprintf("重排序失败: %s: %v", errTypeName(err), err))
		return truncateCandidates(candidates, topK)
	}
	if result == nil {
		return truncateCandidates(candidates, topK)
	}

	reranked := make([]Document, 0, len(result.Results))
	for _, item := range result.Results {
		if item.Index == nil || *item.Index < 0 || *item.Index >= len(candidates) {
			// Python: item["index"] 越界/缺失抛 KeyError/IndexError，被 except 捕获
			err := &typedError{typeName: "IndexError", msg: "rerank 结果下标非法"}
			r.logger.Error(fmt.Sprintf("重排序失败: %s: %v", errTypeName(err), err))
			return truncateCandidates(candidates, topK)
		}
		doc := candidates[*item.Index].copy()
		doc["rerank_score"] = item.RelevanceScore
		reranked = append(reranked, doc)
	}

	r.logger.Info(fmt.Sprintf("重排序完成: %d 个结果", len(reranked)))
	return reranked
}

// callRerank 发起一次 rerank POST。返回 nil result 表示"响应无 results 字段"
// 的格式异常（对齐 Python 无 raise_for_status 的行为），由调用方走告警分支。
func (r *Reranker) callRerank(ctx context.Context, query string, documents []string, topK int) (*rerankResponse, error) {
	body, err := json.Marshal(rerankRequest{
		Model:     r.model,
		Query:     query,
		Documents: documents,
		TopN:      min(len(documents), topK),
	})
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, r.apiBase, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+r.apiKey)

	resp, err := r.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, err
	}
	var parsed rerankResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		// 非 JSON 响应体：对齐 Python response.json() 抛 JSONDecodeError 的错误分支
		return nil, fmt.Errorf("解析 Reranker 响应失败（状态 %d）: %w", resp.StatusCode, err)
	}
	if parsed.Results == nil {
		// 有 JSON 但无 results：对齐 "Reranker API返回格式异常" 分支
		r.logger.Warn(fmt.Sprintf("Reranker API返回格式异常: %s", snippet(respBody)))
		return nil, nil
	}
	return &parsed, nil
}

// copy 浅拷贝文档（对齐 dict.copy()：顶层键复制，嵌套值共享引用）。
func (d Document) copy() Document {
	out := make(Document, len(d)+1)
	for k, v := range d {
		out[k] = v
	}
	return out
}

// truncateCandidates 返回前 limit 个候选的浅拷贝（limit 自动截断到长度，
// 对齐 Python 切片 candidates[:limit]）。
func truncateCandidates(candidates []Document, limit int) []Document {
	if limit > len(candidates) {
		limit = len(candidates)
	}
	if limit < 0 {
		limit = 0
	}
	out := make([]Document, 0, limit)
	for _, doc := range candidates[:limit] {
		out = append(out, doc.copy())
	}
	return out
}

// snippet 截取响应体片段，防止日志爆炸。
func snippet(b []byte) string {
	const max = 512
	s := string(b)
	if len(s) > max {
		return s[:max]
	}
	return s
}

// errTypeName 提取错误类型名，复刻 Python 日志 "{type(e).__name__}: {e}" 的前半段。
func errTypeName(err error) string {
	if err == nil {
		return ""
	}
	var namer interface{ TypeName() string }
	if errors.As(err, &namer) {
		return namer.TypeName()
	}
	return reflect.TypeOf(err).String()
}
