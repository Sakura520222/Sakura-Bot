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
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/Sakura520222/Sakura-Bot/internal/contracts"
)

type purposeConfigStub struct {
	byPurpose map[string]AIConfig
}

func (s purposeConfigStub) EffectiveAIConfig(purposes ...string) AIConfig {
	p := "assistant"
	if len(purposes) > 0 {
		p = purposes[0]
	}
	return s.byPurpose[p]
}

type contractsPurposeStub struct {
	byPurpose map[string]contracts.AIConf
}

func (s contractsPurposeStub) EffectiveAIConfig(purposes ...string) contracts.AIConf {
	p := "assistant"
	if len(purposes) > 0 {
		p = purposes[0]
	}
	return s.byPurpose[p]
}

func writeCompletionStream(w http.ResponseWriter, chunks ...string) {
	w.Header().Set("Content-Type", "text/event-stream")
	for _, chunk := range chunks {
		_, _ = fmt.Fprintf(w, "data: %s\n\n", chunk)
	}
	_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
}

func TestCompleteChatTemperatureNilAndZeroResponseFormatMaxTokens(t *testing.T) {
	var rawRequests []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/chat/completions" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer key-1" {
			t.Errorf("unexpected auth header: %q", r.Header.Get("Authorization"))
		}
		b, _ := io.ReadAll(r.Body)
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Errorf("unmarshal req failed: %v", err)
		}
		rawRequests = append(rawRequests, m)

		if r.Header.Get("Accept") != "text/event-stream" {
			t.Errorf("unexpected Accept header: %q", r.Header.Get("Accept"))
		}
		writeCompletionStream(w,
			`{"id":"chatcmpl-123","model":"override-model","choices":[{"index":1,"delta":{"content":"ignore me"}},{"index":0,"delta":{"content":"{\"decision\":","tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{"}}]}}]}`,
			`{"choices":[{"index":0,"delta":{"content":"\"ban\"}","tool_calls":[{"index":0,"function":{"arguments":"}"}}]}}]}`,
			`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
			`{"choices":[],"usage":{"prompt_tokens":12,"completion_tokens":8,"total_tokens":20}}`,
		)
	}))
	defer srv.Close()

	cli := newTestClient(t, srv.URL)

	// 1. Temperature == nil：请求体必须省略 temperature（对齐 summary_pipeline.py）。
	resp1, err := cli.CompleteChat(context.Background(), CompleteRequest{
		Messages:  []Message{{Role: "user", Content: Str("总结文本")}},
		MaxTokens: Int(64000),
	})
	if err != nil {
		t.Fatalf("CompleteChat 失败: %v", err)
	}
	if _, exists := rawRequests[0]["temperature"]; exists {
		t.Fatalf("Temperature=nil 时不应发送 temperature 字段: %+v", rawRequests[0])
	}
	if rawRequests[0]["model"] != "deepseek-chat" {
		t.Fatalf("未覆写 Model 时应取 provider 默认模型: %v", rawRequests[0]["model"])
	}
	if rawRequests[0]["max_tokens"] != float64(64000) {
		t.Fatalf("max_tokens 不符: %v", rawRequests[0]["max_tokens"])
	}
	if rawRequests[0]["stream"] != true {
		t.Fatalf("CompleteChat must request SSE: %+v", rawRequests[0])
	}
	if resp1.ID != "chatcmpl-123" || resp1.Model != "override-model" || resp1.Content != `{"decision":"ban"}` || resp1.FinishReason != "stop" {
		t.Fatalf("响应解析不符: %+v", resp1)
	}
	if resp1.Usage.PromptTokens != 12 || resp1.Usage.CompletionTokens != 8 || resp1.Usage.TotalTokens != 20 {
		t.Fatalf("Usage 解析不符: %+v", resp1.Usage)
	}
	if len(resp1.ToolCalls) != 1 || resp1.ToolCalls[0].Function.Name != "lookup" || resp1.ToolCalls[0].Function.Arguments != "{}" {
		t.Fatalf("ToolCalls 解析不符: %+v", resp1.ToolCalls)
	}

	// 2. Temperature = Float64(0) + ResponseFormat + Model 覆写 + Tools：必须原样发送 temperature:0。
	tools := json.RawMessage(`[{"type":"function","function":{"name":"lookup"}}]`)
	_, err = cli.CompleteChat(context.Background(), CompleteRequest{
		Model:          "custom-json-model",
		Messages:       []Message{{Role: "user", Content: Str("分类")}},
		Temperature:    Float64(0),
		MaxTokens:      Int(256),
		ResponseFormat: &ResponseFormat{Type: "json_object"},
		Tools:          tools,
		ToolChoice:     "auto",
	})
	if err != nil {
		t.Fatalf("第二次 CompleteChat 失败: %v", err)
	}
	req2 := rawRequests[1]
	tempVal, exists := req2["temperature"]
	if !exists || tempVal != float64(0) {
		t.Fatalf("Temperature=0 必须显式编码为 0，实际 exists=%v val=%v", exists, tempVal)
	}
	if req2["model"] != "custom-json-model" {
		t.Fatalf("Model 覆写未生效: %v", req2["model"])
	}
	rf, _ := req2["response_format"].(map[string]any)
	if rf["type"] != "json_object" {
		t.Fatalf("response_format 不符: %+v", req2["response_format"])
	}
	if req2["tool_choice"] != "auto" {
		t.Fatalf("tool_choice 不符: %v", req2["tool_choice"])
	}
}

func TestCompleteChatMultimodalVisionGuardAndErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("mode") == "empty_choices" {
			_, _ = w.Write([]byte(`{"id":"x","choices":[]}`))
			return
		}
		if r.URL.Query().Get("mode") == "err500" {
			http.Error(w, "upstream failure sk-secret-123", http.StatusBadGateway)
			return
		}
		writeCompletionStream(w, `{"id":"v1","model":"vl","choices":[{"delta":{"content":"一只猫"},"finish_reason":"stop"}]}`)
	}))
	defer srv.Close()

	imgCtx := WithImages(context.Background(), []string{"data:image/png;base64,AAAA"})
	imgMsg := UserMessage(imgCtx, "图里有什么？")

	// 不支持视觉的模型必须拒绝 image_url。
	noVisionCli := NewClient(AIConfigFunc(func() AIConfig {
		return AIConfig{APIKey: "sk-secret-123", BaseURL: srv.URL, Model: "text-only", SupportsVision: false}
	}))
	if _, err := noVisionCli.CompleteChat(context.Background(), CompleteRequest{Messages: []Message{imgMsg}}); err == nil || !strings.Contains(err.Error(), "does not support images") {
		t.Fatalf("不支持视觉时应拒绝图片请求，实际 err=%v", err)
	}

	// 支持视觉时放行。
	visionCli := NewClient(AIConfigFunc(func() AIConfig {
		return AIConfig{APIKey: "sk-secret-123", BaseURL: srv.URL, Model: "vl", SupportsVision: true}
	}))
	got, err := visionCli.CompleteChat(context.Background(), CompleteRequest{Messages: []Message{imgMsg}})
	if err != nil || got.Content != "一只猫" {
		t.Fatalf("视觉调用失败: got=%+v err=%v", got, err)
	}

	// 非 200 返回 *APIError 且脱敏密钥。
	errSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad key sk-secret-123", http.StatusUnauthorized)
	}))
	defer errSrv.Close()
	errCli := NewClient(AIConfigFunc(func() AIConfig {
		return AIConfig{APIKey: "sk-secret-123", BaseURL: errSrv.URL, Model: "m"}
	}))
	_, err = errCli.CompleteChat(context.Background(), CompleteRequest{Messages: []Message{{Role: "user", Content: Str("hi")}}})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusUnauthorized {
		t.Fatalf("应返回 401 *APIError: %v", err)
	}
	if strings.Contains(apiErr.Body, "sk-secret-123") {
		t.Fatalf("错误响应体未脱敏密钥: %q", apiErr.Body)
	}

	// 空 choices 报错。
	emptyChoicesSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":"x","choices":[]}`))
	}))
	defer emptyChoicesSrv.Close()
	emptyCli := NewClient(AIConfigFunc(func() AIConfig {
		return AIConfig{APIKey: "k", BaseURL: emptyChoicesSrv.URL, Model: "m"}
	}))
	if _, err := emptyCli.CompleteChat(context.Background(), CompleteRequest{Messages: []Message{{Role: "user", Content: Str("hi")}}}); err == nil {
		t.Fatal("choices 为空应返回错误")
	}
}

func TestNewPurposeClientSupportsBothSignatures(t *testing.T) {
	var seenModels []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		seenModels = append(seenModels, body["model"].(string))
		writeCompletionStream(w, `{"id":"1","choices":[{"delta":{"content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer srv.Close()

	// 1. interface{ EffectiveAIConfig(...string) AIConfig }
	var ifaceCfg interface{ EffectiveAIConfig(...string) AIConfig } = purposeConfigStub{
		byPurpose: map[string]AIConfig{
			"summary": {APIKey: "k1", BaseURL: srv.URL, Model: "summary-m"},
		},
	}
	c1 := NewPurposeClient(ifaceCfg, "summary")
	if _, err := c1.CompleteChat(context.Background(), CompleteRequest{Messages: []Message{{Role: "user", Content: Str("x")}}}); err != nil {
		t.Fatal(err)
	}

	// 2. contracts.AIConf 提供方（与 *config.Config.EffectiveAIConfig 同签名）
	c2 := NewPurposeClient(contractsPurposeStub{
		byPurpose: map[string]contracts.AIConf{
			"poll": {APIKey: "k2", BaseURL: srv.URL, Model: "poll-m", SupportsVision: true},
		},
	}, "poll")
	if _, err := c2.CompleteChat(context.Background(), CompleteRequest{Messages: []Message{{Role: "user", Content: Str("x")}}}); err != nil {
		t.Fatal(err)
	}

	want := []string{"summary-m", "poll-m"}
	if !reflect.DeepEqual(seenModels, want) {
		t.Fatalf("NewPurposeClient 用途路由模型不符: got=%v want=%v", seenModels, want)
	}
}

func TestListModelsAndTestConnection(t *testing.T) {
	var lastCompletion map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			if r.Method != http.MethodGet {
				t.Errorf("expected GET /v1/models, got %s", r.Method)
			}
			if r.Header.Get("Authorization") != "Bearer sk-live" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{
				"data":[
					{"id":"gpt-4o"},
					{"id":"deepseek-chat"},
					{"id":"gpt-4o"},
					{"id":""},
					{"id":"bad\nmodel"},
					{"id":" padded "}
				]
			}`))
		case "/v1/chat/completions":
			if r.Header.Get("Authorization") != "Bearer sk-live" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			_ = json.NewDecoder(r.Body).Decode(&lastCompletion)
			writeCompletionStream(w, `{"id":"ok1","model":"deepseek-chat","choices":[{"delta":{"content":"OK"},"finish_reason":"stop"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	baseURL := srv.URL + "/v1/"
	models, err := ListModels(context.Background(), baseURL, "sk-live", srv.Client())
	if err != nil {
		t.Fatalf("ListModels 失败: %v", err)
	}
	wantModels := []string{"deepseek-chat", "gpt-4o"}
	if !reflect.DeepEqual(models, wantModels) {
		t.Fatalf("ListModels 结果不符: got=%v want=%v", models, wantModels)
	}

	// 错误密钥返回失败。
	if _, err := ListModels(context.Background(), baseURL, "wrong", srv.Client()); err == nil {
		t.Fatal("错误密钥应失败")
	}
	// 非法 URL 拒绝。
	if _, err := ListModels(context.Background(), "ftp://bad.example/v1", "sk-live", srv.Client()); err == nil {
		t.Fatal("非法协议应拒绝")
	}

	// TestConnection: connection
	if err := TestConnection(context.Background(), baseURL, "sk-live", "", "connection", srv.Client()); err != nil {
		t.Fatalf("connection 测试应成功: %v", err)
	}

	// TestConnection: call（校验 1-token CompleteChat）
	if err := TestConnection(context.Background(), baseURL, "sk-live", " deepseek-chat ", "call", srv.Client()); err != nil {
		t.Fatalf("call 测试应成功: %v", err)
	}
	if lastCompletion["model"] != "deepseek-chat" || lastCompletion["max_tokens"] != float64(1) {
		t.Fatalf("TestConnection(call) 请求体不符: %+v", lastCompletion)
	}

	// TestConnection: 非法参数
	if err := TestConnection(context.Background(), baseURL, "sk-live", "", "call", srv.Client()); err == nil {
		t.Fatal("call 缺少 model 应失败")
	}
	if err := TestConnection(context.Background(), baseURL, "sk-live", "model\x00", "call", srv.Client()); err == nil {
		t.Fatal("含控制字符的 model 应失败")
	}
	if err := TestConnection(context.Background(), baseURL, "sk-live", "deepseek-chat", "unknown", srv.Client()); err == nil {
		t.Fatal("未知 kind 应失败")
	}
}

func TestCompleteChatRejectsIncompleteSSE(t *testing.T) {
	const content = "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n"
	const stop = "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"
	const done = "data: [DONE]\n\n"
	for name, wire := range map[string]string{
		"missing done":          content + stop,
		"missing finish reason": content + done,
		"malformed chunk":       content + "data: invalid-json\n\n" + stop + done,
		"upstream error":        content + "event: error\ndata: {\"message\":\"inference failed\"}\n\n" + stop + done,
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, wire)
			}))
			defer server.Close()
			resp, err := newTestClient(t, server.URL).CompleteChat(t.Context(), CompleteRequest{})
			if resp != nil || err == nil {
				t.Fatalf("incomplete SSE returned %+v, %v", resp, err)
			}
		})
	}
}

func TestCompleteChatRetainsSSEAndReasoningAcrossUsageFallback(t *testing.T) {
	var reqs = make(chan map[string]any, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("invalid request: %v", err)
		}
		reqs <- request
		if _, ok := request["stream_options"]; ok {
			http.Error(w, "unsupported stream_options", http.StatusBadRequest)
			return
		}
		writeCompletionStream(w, `{"choices":[{"delta":{"content":"partial"},"finish_reason":"length"}]}`)
	}))
	defer server.Close()
	ctx := WithConfig(t.Context(), AIConfig{BaseURL: server.URL, Model: "same-model", ReasoningEffort: "high"})
	resp, err := newTestClient(t, server.URL).CompleteChat(ctx, CompleteRequest{})
	if err != nil || resp == nil || resp.FinishReason != "length" || resp.Model != "same-model" {
		t.Fatalf("unexpected completion: %+v %v", resp, err)
	}
	for i := 0; i < 2; i++ {
		request := <-reqs
		if request["stream"] != true || request["reasoning_effort"] != "high" {
			t.Fatalf("request %d lost stream or effort: %+v", i, request)
		}
	}
}

func TestCompleteChatReportsReasoningAndVisibleCharactersSeparately(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeCompletionStream(w,
			`{"choices":[{"delta":{"reasoning_content":"思💡"}}]}`,
			`{"choices":[{"delta":{"content":"答"} }]}`,
			`{"choices":[{"delta":{"content":"案","reasoning_content":"虑"},"finish_reason":"stop"}]}`,
			`{"choices":[],"usage":{"completion_tokens":5}}`,
		)
	}))
	defer server.Close()
	var seen []CompletionProgress
	resp, err := newTestClient(t, server.URL).CompleteChat(t.Context(), CompleteRequest{
		OnProgress: func(_ context.Context, p CompletionProgress) error {
			seen = append(seen, p)
			return nil
		},
	})
	if err != nil || resp == nil || resp.Content != "答案" {
		t.Fatalf("unexpected visible completion: %+v error=%v", resp, err)
	}
	want := []CompletionProgress{{ReceivedChars: 2}, {ReceivedChars: 3, OutputChars: 1}, {ReceivedChars: 5, OutputChars: 2}}
	if !reflect.DeepEqual(seen, want) {
		t.Fatalf("progress = %+v, want %+v", seen, want)
	}
}
