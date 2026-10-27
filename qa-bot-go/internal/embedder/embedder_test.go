// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package embedder

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

// mapEnv 测试用环境实现（对齐 os.getenv 语义：缺失返回空串）。
type mapEnv map[string]string

func (m mapEnv) Get(k string) string { return m[k] }

func (m mapEnv) GetIntDef(k string, def int64) int64 {
	if v := m[k]; v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	return def
}

// newTestLogger 构造捕获全部级别的日志器。
func newTestLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})), &buf
}

// TestNewGeneratorDefaults 验证 env 缺省时的默认配置（对齐 :29-32）。
func TestNewGeneratorDefaults(t *testing.T) {
	g := NewGenerator(mapEnv{})
	cfg := g.Config()
	if cfg.APIBase != DefaultAPIBase {
		t.Fatalf("默认 apiBase = %q, 期望 %q", cfg.APIBase, DefaultAPIBase)
	}
	if cfg.Model != DefaultModel {
		t.Fatalf("默认 model = %q, 期望 %q", cfg.Model, DefaultModel)
	}
	if cfg.Dimension != DefaultDimension {
		t.Fatalf("默认 dimension = %d, 期望 %d", cfg.Dimension, DefaultDimension)
	}
	if g.IsAvailable() {
		t.Fatal("无 API Key 时应不可用")
	}
}

// TestNewGeneratorInitLogs 验证初始化日志文案逐字对齐（:34-40）。
func TestNewGeneratorInitLogs(t *testing.T) {
	logger, buf := newTestLogger()
	NewGenerator(mapEnv{}, WithLogger(logger))
	if got := buf.String(); !contains(got, "未设置EMBEDDING_API_KEY，Embedding功能将不可用") {
		t.Fatalf("缺 Key 应有告警, 得到: %s", got)
	}

	logger, buf = newTestLogger()
	NewGenerator(mapEnv{EnvAPIKey: "sk-test", EnvModel: "custom-model"}, WithLogger(logger))
	if got := buf.String(); !contains(got, "Embedding生成器初始化成功: custom-model") {
		t.Fatalf("有 Key 应有初始化成功日志, 得到: %s", got)
	}
}

// TestGenerateSuccess 验证单文本生成：请求体/鉴权头/响应解析/日志。
func TestGenerateSuccess(t *testing.T) {
	var gotModel string
	var gotInput []string
	var gotAuth, gotContentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotContentType = r.Header.Get("Content-Type")
		var req embeddingsRequest
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &req)
		gotModel = req.Model
		gotInput = req.Input
		_, _ = w.Write([]byte(`{"data":[{"index":0,"embedding":[0.5,-1.25,2.0]}]}`))
	}))
	defer srv.Close()

	logger, buf := newTestLogger()
	g := NewGenerator(mapEnv{
		EnvAPIKey:  "sk-embed",
		EnvAPIBase: srv.URL,
		EnvModel:   "BAAI/bge-m3",
	}, WithLogger(logger))

	vec, err := g.Generate(context.Background(), "查询文本")
	if err != nil {
		t.Fatalf("Generate 失败: %v", err)
	}
	if gotAuth != "Bearer sk-embed" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if gotContentType != "application/json" {
		t.Fatalf("Content-Type = %q", gotContentType)
	}
	if gotModel != "BAAI/bge-m3" || len(gotInput) != 1 || gotInput[0] != "查询文本" {
		t.Fatalf("请求体错误: model=%q input=%v", gotModel, gotInput)
	}
	if len(vec) != 3 || vec[0] != 0.5 || vec[1] != -1.25 || vec[2] != 2.0 {
		t.Fatalf("向量解析错误: %v", vec)
	}
	if !contains(buf.String(), "成功生成embedding，维度: 3") {
		t.Fatalf("缺少成功日志: %s", buf.String())
	}
}

// TestGenerateUnavailable 验证未配置 Key 时的降级语义（:59-61）。
func TestGenerateUnavailable(t *testing.T) {
	logger, buf := newTestLogger()
	g := NewGenerator(mapEnv{}, WithLogger(logger))

	vec, err := g.Generate(context.Background(), "文本")
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("应返回 ErrUnavailable, 得到 %v", err)
	}
	if vec != nil {
		t.Fatalf("不可用时应返回 nil, 得到 %v", vec)
	}
	if !contains(buf.String(), "Embedding服务不可用") {
		t.Fatalf("缺少不可用告警: %s", buf.String())
	}
}

// TestGenerateAPIError 验证非 200 响应的错误路径与日志（:70-72）。
func TestGenerateAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"invalid api key"}`, http.StatusUnauthorized)
	}))
	defer srv.Close()

	logger, buf := newTestLogger()
	g := NewGenerator(mapEnv{EnvAPIKey: "bad", EnvAPIBase: srv.URL}, WithLogger(logger))

	_, err := g.Generate(context.Background(), "文本")
	if err == nil {
		t.Fatal("非 200 应返回错误")
	}
	if !contains(err.Error(), "401") || !contains(err.Error(), "invalid api key") {
		t.Fatalf("错误应包含状态与响应片段: %v", err)
	}
	if !contains(buf.String(), "生成embedding失败") {
		t.Fatalf("缺少失败日志: %s", buf.String())
	}
}

// TestGenerateEmptyData 验证响应缺数据时对齐 Python 的 IndexError 捕获路径。
func TestGenerateEmptyData(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer srv.Close()

	g := NewGenerator(mapEnv{EnvAPIKey: "k", EnvAPIBase: srv.URL})
	_, err := g.Generate(context.Background(), "文本")
	var te *typedError
	if !errors.As(err, &te) || te.TypeName() != "IndexError" {
		t.Fatalf("应返回 IndexError 语义错误, 得到 %v", err)
	}
}

// TestBatchGenerateSuccess 验证批量生成（:88-93）。
func TestBatchGenerateSuccess(t *testing.T) {
	var gotInput []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req embeddingsRequest
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &req)
		gotInput = req.Input
		_, _ = w.Write([]byte(`{"data":[` +
			`{"index":0,"embedding":[1,2]},` +
			`{"index":1,"embedding":[3,4]}]}`))
	}))
	defer srv.Close()

	logger, buf := newTestLogger()
	g := NewGenerator(mapEnv{EnvAPIKey: "k", EnvAPIBase: srv.URL}, WithLogger(logger))

	vecs := g.BatchGenerate(context.Background(), []string{"a", "b"})
	if len(vecs) != 2 || len(vecs[0]) != 2 || vecs[0][0] != 1 || vecs[1][1] != 4 {
		t.Fatalf("批量向量错误: %v", vecs)
	}
	if len(gotInput) != 2 || gotInput[0] != "a" || gotInput[1] != "b" {
		t.Fatalf("批量请求 input 错误: %v", gotInput)
	}
	if !contains(buf.String(), "成功批量生成2个embedding") {
		t.Fatalf("缺少批量成功日志: %s", buf.String())
	}
}

// TestBatchGenerateUnavailable 验证 [None]*len 降级语义（:84-86）。
func TestBatchGenerateUnavailable(t *testing.T) {
	logger, buf := newTestLogger()
	g := NewGenerator(mapEnv{}, WithLogger(logger))

	vecs := g.BatchGenerate(context.Background(), []string{"a", "b", "c"})
	if len(vecs) != 3 {
		t.Fatalf("应返回等长全 nil 切片, 得到长度 %d", len(vecs))
	}
	for i, v := range vecs {
		if v != nil {
			t.Fatalf("vecs[%d] 应为 nil, 得到 %v", i, v)
		}
	}
	if !contains(buf.String(), "Embedding服务不可用") {
		t.Fatalf("缺少不可用告警: %s", buf.String())
	}
}

// TestBatchGenerateFailure 验证整体失败返回全 nil + 错误日志（:95-97）。
func TestBatchGenerateFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	logger, buf := newTestLogger()
	g := NewGenerator(mapEnv{EnvAPIKey: "k", EnvAPIBase: srv.URL}, WithLogger(logger))

	vecs := g.BatchGenerate(context.Background(), []string{"a", "b"})
	if len(vecs) != 2 || vecs[0] != nil || vecs[1] != nil {
		t.Fatalf("失败应返回全 nil, 得到 %v", vecs)
	}
	if !contains(buf.String(), "批量生成embedding失败") {
		t.Fatalf("缺少批量失败日志: %s", buf.String())
	}
}

// TestGetIntDefOSEnv 验证 OSEnv 的GetIntDef 回退行为。
func TestGetIntDefOSEnv(t *testing.T) {
	t.Setenv("EMBEDDING_DIMENSION_TEST_OK", "2048")
	t.Setenv("EMBEDDING_DIMENSION_TEST_BAD", "abc")
	env := OSEnv{}
	if got := env.GetIntDef("EMBEDDING_DIMENSION_TEST_OK", 1); got != 2048 {
		t.Fatalf("合法值解析错误: %d", got)
	}
	if got := env.GetIntDef("EMBEDDING_DIMENSION_TEST_BAD", 1); got != 1 {
		t.Fatalf("非法值应回退默认: %d", got)
	}
	if got := env.GetIntDef("EMBEDDING_DIMENSION_TEST_MISSING", 1); got != 1 {
		t.Fatalf("缺失键应返回默认: %d", got)
	}
}

// contains 供日志断言使用（避免每处 strings.Contains 导入噪音）。
func contains(s, sub string) bool {
	return bytes.Contains([]byte(s), []byte(sub))
}
