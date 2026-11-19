// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package reranker

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

// mapEnv 测试用环境实现。
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

func newTestLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})), &buf
}

// sampleCandidates 构造三个候选文档。
func sampleCandidates() []Document {
	return []Document{
		{"summary_id": "1", "summary_text": "第一个候选", "post_url": "https://t.me/c/1/2"},
		{"summary_id": "2", "summary_text": "第二个候选"},
		{"summary_id": "3", "summary_text": "第三个候选"},
	}
}

// TestNewInitLogs 验证初始化日志文案逐字对齐（reranker.py:36-39）。
func TestNewInitLogs(t *testing.T) {
	logger, buf := newTestLogger()
	New(mapEnv{}, WithLogger(logger))
	if !bytes.Contains(buf.Bytes(), []byte("未设置RERANKER_API_KEY，重排序功能将不可用")) {
		t.Fatalf("缺 Key 应有告警, 得到: %s", buf.String())
	}

	logger, buf = newTestLogger()
	r := New(mapEnv{EnvAPIKey: "rk-1"}, WithLogger(logger))
	if !bytes.Contains(buf.Bytes(), []byte("Reranker初始化成功: "+DefaultModel)) {
		t.Fatalf("有 Key 应有初始化成功日志, 得到: %s", buf.String())
	}
	cfg := r.Config()
	if cfg.APIBase != DefaultAPIBase || cfg.Model != DefaultModel || cfg.TopK != DefaultTopK || cfg.FinalK != DefaultFinalK {
		t.Fatalf("默认配置错误: %+v", cfg)
	}
	if r.client.Timeout != DefaultTimeout {
		t.Fatalf("超时应为 30s（reranker.py:74）, 得到 %v", r.client.Timeout)
	}
}

// TestRerankUnavailable 验证无 Key 降级：候选前缀 + 告警（:59-61）。
func TestRerankUnavailable(t *testing.T) {
	logger, buf := newTestLogger()
	r := New(mapEnv{}, WithLogger(logger))
	if r.IsAvailable() {
		t.Fatal("无 Key 应不可用")
	}

	// topK 未指定（<=0）→ finalK=5 截断，本例候选 3 个全保留
	got := r.Rerank(context.Background(), "q", sampleCandidates(), 0)
	if len(got) != 3 {
		t.Fatalf("无 Key 应返回前缀候选, 得到 %d 个", len(got))
	}
	if !bytes.Contains(buf.Bytes(), []byte("Reranker服务不可用，返回原始结果")) {
		t.Fatalf("缺少不可用告警: %s", buf.String())
	}

	// 显式 topK=2 → 截断到 2
	got = r.Rerank(context.Background(), "q", sampleCandidates(), 2)
	if len(got) != 2 {
		t.Fatalf("topK=2 应截断到 2, 得到 %d 个", len(got))
	}
}

// TestRerankEmpty 验证空候选直接返回空列表（:63-64），且不发起请求。
func TestRerankEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("空候选不应发起请求")
	}))
	defer srv.Close()

	r := New(mapEnv{EnvAPIKey: "k", EnvAPIBase: srv.URL})
	got := r.Rerank(context.Background(), "q", []Document{}, 5)
	if got == nil || len(got) != 0 {
		t.Fatalf("空候选应返回空切片, 得到 %v", got)
	}
}

// TestRerankSuccess 验证成功路径：请求体、结果重排、rerank_score、浅拷贝语义。
func TestRerankSuccess(t *testing.T) {
	var gotReq rerankRequest
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotReq)
		_, _ = w.Write([]byte(`{"results":[` +
			`{"index":2,"relevance_score":0.95},` +
			`{"index":0,"relevance_score":0.42}]}`))
	}))
	defer srv.Close()

	logger, buf := newTestLogger()
	r := New(mapEnv{EnvAPIKey: "rk", EnvAPIBase: srv.URL}, WithLogger(logger))

	candidates := sampleCandidates()
	got := r.Rerank(context.Background(), "查询词", candidates, 5)

	if gotAuth != "Bearer rk" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if gotReq.Model != DefaultModel || gotReq.Query != "查询词" {
		t.Fatalf("请求体 model/query 错误: %+v", gotReq)
	}
	if len(gotReq.Documents) != 3 || gotReq.Documents[0] != "第一个候选" {
		t.Fatalf("documents 提取错误: %v", gotReq.Documents)
	}
	if gotReq.TopN != 3 { // min(len(documents), top_k)
		t.Fatalf("top_n = %d, 期望 3", gotReq.TopN)
	}

	// 顺序 = 服务端 results 顺序：index 2 在前、index 0 在后
	if len(got) != 2 {
		t.Fatalf("重排结果数 = %d, 期望 2", len(got))
	}
	if got[0]["summary_id"] != "3" || got[1]["summary_id"] != "1" {
		t.Fatalf("重排顺序错误: %v / %v", got[0], got[1])
	}
	if score, ok := got[0]["rerank_score"].(float64); !ok || score != 0.95 {
		t.Fatalf("rerank_score 错误: %v", got[0]["rerank_score"])
	}
	if score, ok := got[1]["rerank_score"].(float64); !ok || score != 0.42 {
		t.Fatalf("rerank_score 错误: %v", got[1]["rerank_score"])
	}
	// 输入候选不被修改（dict.copy 语义）
	if _, mutated := candidates[2]["rerank_score"]; mutated {
		t.Fatal("输入候选不应被追加 rerank_score")
	}
	// 浅拷贝：顶层新增键不回写原 map，嵌套共享值仍可见
	if !bytes.Contains(buf.Bytes(), []byte("重排序完成: 2 个结果")) {
		t.Fatalf("缺少完成日志: %s", buf.String())
	}
}

// TestRerankMissingRelevanceScore 验证缺 relevance_score 时默认 0（item.get 语义）。
func TestRerankMissingRelevanceScore(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"results":[{"index":1}]}`))
	}))
	defer srv.Close()

	r := New(mapEnv{EnvAPIKey: "k", EnvAPIBase: srv.URL})
	got := r.Rerank(context.Background(), "q", sampleCandidates(), 5)
	if len(got) != 1 {
		t.Fatalf("结果数 = %d, 期望 1", len(got))
	}
	if score, ok := got[0]["rerank_score"].(float64); !ok || score != 0 {
		t.Fatalf("缺 relevance_score 应为 0, 得到 %v", got[0]["rerank_score"])
	}
}

// TestRerankBadFormat 验证响应无 results 字段时的告警降级（:103-105）。
// 注意：Python httpx 对非 200 不抛异常，因此 500 + JSON 无 results 同样走此分支。
func TestRerankBadFormat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"boom"}`))
	}))
	defer srv.Close()

	logger, buf := newTestLogger()
	r := New(mapEnv{EnvAPIKey: "k", EnvAPIBase: srv.URL}, WithLogger(logger))

	got := r.Rerank(context.Background(), "q", sampleCandidates(), 2)
	if len(got) != 2 {
		t.Fatalf("格式异常应降级返回前 topK 个, 得到 %d 个", len(got))
	}
	if !bytes.Contains(buf.Bytes(), []byte("Reranker API返回格式异常")) {
		t.Fatalf("缺少格式异常告警: %s", buf.String())
	}
}

// TestRerankNetworkError 验证非 JSON 响应走"重排序失败"错误分支（:107-109）。
func TestRerankNetworkError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("<html>bad gateway</html>"))
	}))
	defer srv.Close()

	logger, buf := newTestLogger()
	r := New(mapEnv{EnvAPIKey: "k", EnvAPIBase: srv.URL}, WithLogger(logger))

	got := r.Rerank(context.Background(), "q", sampleCandidates(), 2)
	if len(got) != 2 {
		t.Fatalf("失败应降级返回前 topK 个, 得到 %d 个", len(got))
	}
	if !bytes.Contains(buf.Bytes(), []byte("重排序失败")) {
		t.Fatalf("缺少失败日志: %s", buf.String())
	}
}

// TestRerankIndexOutOfRange 验证 results 下标越界不 panic、走失败分支。
func TestRerankIndexOutOfRange(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"results":[{"index":9,"relevance_score":0.5}]}`))
	}))
	defer srv.Close()

	logger, buf := newTestLogger()
	r := New(mapEnv{EnvAPIKey: "k", EnvAPIBase: srv.URL}, WithLogger(logger))

	got := r.Rerank(context.Background(), "q", sampleCandidates(), 5)
	if len(got) != 3 {
		t.Fatalf("越界应降级返回全部候选前缀, 得到 %d 个", len(got))
	}
	if !bytes.Contains(buf.Bytes(), []byte("重排序失败: IndexError")) {
		t.Fatalf("应记录 IndexError 类型日志: %s", buf.String())
	}
}

// TestRerankTimeoutPropagation 验证上下文取消向上传播到 HTTP 调用并走失败分支。
func TestRerankTimeoutPropagation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	defer srv.Close()

	logger, buf := newTestLogger()
	r := New(mapEnv{EnvAPIKey: "k", EnvAPIBase: srv.URL}, WithLogger(logger))

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	got := r.Rerank(ctx, "q", sampleCandidates(), 5)
	if len(got) != 3 {
		t.Fatalf("取消应降级返回候选前缀, 得到 %d 个", len(got))
	}
	if !bytes.Contains(buf.Bytes(), []byte("重排序失败")) {
		t.Fatalf("缺少失败日志: %s", buf.String())
	}
}

// TestTruncateCandidates 验证切片截断与空输入。
func TestTruncateCandidates(t *testing.T) {
	cands := sampleCandidates()
	if got := truncateCandidates(cands, 99); len(got) != 3 {
		t.Fatalf("limit 超长应截断到长度, 得到 %d", len(got))
	}
	if got := truncateCandidates(nil, 5); got == nil || len(got) != 0 {
		t.Fatalf("nil 候选应返回空切片, 得到 %v", got)
	}
}
