// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

// Package embedder 实现 SiliconFlow/OpenAI 兼容的 embeddings 客户端，
// 逐行为对齐 core/ai/embedding_generator.py（单 POST，model BAAI/bge-m3，
// 维度 1024）。仅使用 stdlib，不引入 OpenAI SDK。
//
// 配置在构造时从 EnvReader 读取一次（对齐 Python __init__ 的 os.getenv），
// 不做热更新；data/.env 的 override 语义由 main 装配的 EnvReader 实现承担。
package embedder

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
	"strings"
	"time"
)

// 环境变量键与默认值（对齐 embedding_generator.py:29-32）。
const (
	EnvAPIKey    = "EMBEDDING_API_KEY"
	EnvAPIBase   = "EMBEDDING_API_BASE"
	EnvModel     = "EMBEDDING_MODEL"
	EnvDimension = "EMBEDDING_DIMENSION"

	DefaultAPIBase   = "https://api.siliconflow.cn/v1/embeddings"
	DefaultModel     = "BAAI/bge-m3"
	DefaultDimension = 1024

	// defaultTimeout 对齐 OpenAI SDK 默认总超时（Python 端未显式配置超时）。
	defaultTimeout = 600 * time.Second

	// maxResponseBytes 响应体读取上限：128MB（1024 维 float 批量上限远小于此）。
	maxResponseBytes = 128 << 20
)

// ErrUnavailable 服务不可用（未配置 API Key）。
// 对齐 Python generate 返回 None 的调用方可判定语义。
var ErrUnavailable = errors.New("embedding 服务不可用（未设置EMBEDDING_API_KEY）")

// EnvReader 环境变量读取的窄接口（Go 惯例：消费者包内定义；由 main 装配为
// internal/config 的实现，以携带 data/.env override 语义）。
// Get 对齐 os.getenv(key) 的缺失返回空串；GetIntDef 对齐 int(os.getenv(key, 默认))，
// 值非法时回退默认（Python 会在构造时抛 ValueError，视为部署错误，此处宽松处理）。
// GetIntDef 采用 int64 与 internal/config.Env 的签名一致，使 *config.Env
// 可零适配直接装配。
type EnvReader interface {
	Get(key string) string
	GetIntDef(key string, def int64) int64
}

// OSEnv 直接读进程环境的默认实现（无 data/.env 叠加；本地裸用与测试场景）。
type OSEnv struct{}

// Get 实现 EnvReader。
func (OSEnv) Get(key string) string { return os.Getenv(key) }

// GetIntDef 实现 EnvReader。
func (OSEnv) GetIntDef(key string, def int64) int64 {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	return def
}

// Generator Embedding生成器（对齐 core/ai/embedding_generator.py::EmbeddingGenerator）。
type Generator struct {
	apiKey    string
	apiBase   string
	model     string
	dimension int64
	client    *http.Client
	logger    *slog.Logger
}

// Option 是 Generator 的构造选项。
type Option func(*Generator)

// WithLogger 注入 slog 日志器；缺省 slog.Default()。
func WithLogger(l *slog.Logger) Option { return func(g *Generator) { g.logger = l } }

// WithHTTPClient 替换底层 http.Client（测试注入）。
func WithHTTPClient(c *http.Client) Option { return func(g *Generator) { g.client = c } }

// NewGenerator 读取 env 构造生成器。
// 未设置 EMBEDDING_API_KEY 时告警且 IsAvailable()==false
// （日志文案对齐 embedding_generator.py:34-40）。
// Python 端"客户端构造失败"分支在 Go（net/http 惰性建连）下不存在，省略。
func NewGenerator(env EnvReader, opts ...Option) *Generator {
	g := &Generator{
		apiKey:    env.Get(EnvAPIKey),
		apiBase:   env.Get(EnvAPIBase),
		model:     env.Get(EnvModel),
		dimension: env.GetIntDef(EnvDimension, DefaultDimension),
		client:    &http.Client{Timeout: defaultTimeout},
		logger:    slog.Default(),
	}
	if g.apiBase == "" {
		g.apiBase = DefaultAPIBase
	}
	if g.model == "" {
		g.model = DefaultModel
	}
	for _, opt := range opts {
		opt(g)
	}
	if g.apiKey == "" {
		g.logger.Warn("未设置EMBEDDING_API_KEY，Embedding功能将不可用")
	} else {
		g.logger.Info("Embedding生成器初始化成功: " + g.model)
	}
	return g
}

// Config 返回构造期生效的配置快照（供上层诊断/测试断言）。
func (g *Generator) Config() Config {
	return Config{
		APIKey: g.apiKey, APIBase: g.apiBase, Model: g.model, Dimension: g.dimension,
	}
}

// Config 构造期配置快照。
type Config struct {
	APIKey    string
	APIBase   string
	Model     string
	Dimension int64
}

// IsAvailable 检查Embedding服务是否可用（对齐 embedding_generator.py:45-47）。
func (g *Generator) IsAvailable() bool { return g.apiKey != "" }

// embeddingsRequest OpenAI 兼容 embeddings 请求体。
type embeddingsRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

// embeddingsResponse OpenAI 兼容 embeddings 响应体（只取所需字段）。
type embeddingsResponse struct {
	Data []struct {
		Index     int       `json:"index"`
		Embedding []float64 `json:"embedding"`
	} `json:"data"`
}

// Generate 生成单个文本的 embedding（对齐 embedding_generator.py:49-72）。
// 返回 []float32：Go 侧向量存储契约（SQLite BLOB = 小端 float32×1024）的载体
// 类型即 float32，在此收口转换；精度损失 <1e-6，属评审已接受风险。
// 服务不可用或请求失败返回 (nil, err)；Python 语义为 None + 告警/错误日志。
func (g *Generator) Generate(ctx context.Context, text string) ([]float32, error) {
	if !g.IsAvailable() {
		g.logger.Warn("Embedding服务不可用")
		return nil, ErrUnavailable
	}
	vecs, err := g.callEmbeddings(ctx, []string{text})
	if err != nil {
		g.logger.Error(fmt.Sprintf("生成embedding失败: %s: %v", errTypeName(err), err))
		return nil, err
	}
	if len(vecs) == 0 || len(vecs[0]) == 0 {
		// Python: response.data[0] 越界会被 except 捕获，日志类型名为 IndexError
		err := &typedError{typeName: "IndexError", msg: "Embedding 响应缺少向量数据"}
		g.logger.Error(fmt.Sprintf("生成embedding失败: %s: %v", errTypeName(err), err))
		return nil, err
	}
	vec := vecs[0]
	g.logger.Debug(fmt.Sprintf("成功生成embedding，维度: %d", len(vec)))
	return vec, nil
}

// BatchGenerate 批量生成 embedding（对齐 embedding_generator.py:74-97）。
// 不可用或整体失败时返回与 texts 等长的全 nil 切片（对齐 Python [None]*len 语义，
// 调用方按 nil 元素降级）；成功时按响应顺序返回向量（Python 忽略 index 字段）。
func (g *Generator) BatchGenerate(ctx context.Context, texts []string) [][]float32 {
	if !g.IsAvailable() {
		g.logger.Warn("Embedding服务不可用")
		return nilSlice(len(texts))
	}
	vecs, err := g.callEmbeddings(ctx, texts)
	if err != nil {
		g.logger.Error(fmt.Sprintf("批量生成embedding失败: %s: %v", errTypeName(err), err))
		return nilSlice(len(texts))
	}
	g.logger.Info(fmt.Sprintf("成功批量生成%d个embedding", len(vecs)))
	return vecs
}

// callEmbeddings 发起一次 embeddings POST 并解析 data[].embedding。
func (g *Generator) callEmbeddings(ctx context.Context, input []string) ([][]float32, error) {
	body, err := json.Marshal(embeddingsRequest{Model: g.model, Input: input})
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, g.apiBase, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+g.apiKey)

	resp, err := g.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Embedding API 返回非 200 状态 %d: %s",
			resp.StatusCode, snippet(respBody))
	}
	var parsed embeddingsResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, fmt.Errorf("解析 Embedding 响应失败: %w", err)
	}
	out := make([][]float32, 0, len(parsed.Data))
	for _, item := range parsed.Data {
		out = append(out, toFloat32(item.Embedding))
	}
	return out, nil
}

// toFloat32 把 JSON 数值向量收口为存储契约的 float32。
func toFloat32(in []float64) []float32 {
	out := make([]float32, len(in))
	for i, v := range in {
		out[i] = float32(v)
	}
	return out
}

// nilSlice 构造 n 个 nil 元素的切片。
func nilSlice(n int) [][]float32 {
	out := make([][]float32, n)
	return out
}

// snippet 截取错误响应体片段，防止日志爆炸。
func snippet(b []byte) string {
	const max = 512
	s := string(b)
	if len(s) > max {
		return s[:max]
	}
	return s
}

// typedError 带语义化类型名的错误：errTypeName 提取 TypeName 优先于 Go 反射
// 类型名，用于把可预期的失败路径（如响应缺数据 → IndexError）对齐到
// Python 异常类名的日志形态。
type typedError struct {
	typeName string
	msg      string
}

// Error 实现 error。
func (e *typedError) Error() string { return e.msg }

// TypeName 返回对齐 Python 的异常类名。
func (e *typedError) TypeName() string { return e.typeName }

// errTypeName 提取错误类型名，复刻 Python 日志 "{type(e).__name__}: {e}" 的前半段。
// 预置语义化类型名的错误（如越界/IndexError）优先，否则回退 Go 反射类型名
// （HTTP 层异常类名无法与 httpx 逐字一致，属已记录偏差）。
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
