// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/contracts"
)

func profileTestConfig() map[string]any {
	return map[string]any{
		"api_key": "legacy-key", "base_url": "https://legacy.example/v1", "model": "legacy-model",
		"ai_profiles": map[string]any{
			"primary": map[string]any{
				"name": "Primary", "provider": "openai", "api_key": "primary-key",
				"base_url": "https://primary.example/v1", "model": "primary-model", "supports_vision": true,
			},
			"local": map[string]any{
				"name": "Local", "base_url": "http://localhost:8000/v1", "model": "local-model",
			},
		},
		"ai_routing": map[string]any{
			"assistant": map[string]any{"profile_id": "primary"},
		},
	}
}

func profileJSON(t *testing.T, cfg map[string]any) string {
	t.Helper()
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// replaceAIConfig 使用重命名模拟 WebUI 保存，保证不依赖 mtime 分辨率。
func replaceAIConfig(t *testing.T, cfg *Config, data string) {
	t.Helper()
	path := cfg.runtime.path + ".next"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, cfg.runtime.path); err != nil {
		t.Fatal(err)
	}
}

func TestEffectiveAIConfigPurposeRouting(t *testing.T) {
	data := profileTestConfig()
	routes := data["ai_routing"].(map[string]any)
	routes["summary"] = map[string]any{"profile_id": "primary", "model": " summary-model ", "supports_vision": false}
	routes["vision"] = map[string]any{"profile_id": "local", "model": "image-model", "supports_vision": true}
	routes["poll"] = map[string]any{"profile_id": "", "model": "ignored", "supports_vision": true}
	cfg := newTestConfig(t, map[string]string{"LLM_API_KEY": "env-key"}, profileJSON(t, data), t.TempDir())
	if err := cfg.Init(); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		purpose string
		want    contracts.AIConf
	}{
		{"assistant", contracts.AIConf{APIKey: "primary-key", BaseURL: "https://primary.example/v1", Model: "primary-model", ProfileID: "primary", SupportsVision: true, VisionOCR: true, VisionDescription: true}},
		{"summary", contracts.AIConf{APIKey: "primary-key", BaseURL: "https://primary.example/v1", Model: "summary-model", ProfileID: "primary", SupportsVision: false, VisionOCR: true, VisionDescription: true}},
		{"vision", contracts.AIConf{APIKey: "", BaseURL: "http://localhost:8000/v1", Model: "image-model", ProfileID: "local", SupportsVision: true, VisionOCR: true, VisionDescription: true}},
		{"poll", contracts.AIConf{APIKey: "legacy-key", BaseURL: "https://legacy.example/v1", Model: "legacy-model", VisionOCR: true, VisionDescription: true}},
	} {
		t.Run(tc.purpose, func(t *testing.T) {
			if got := cfg.EffectiveAIConfig(tc.purpose); got != tc.want {
				t.Errorf("用途 %s 的配置不符: got=%+v want=%+v", tc.purpose, got, tc.want)
			}
		})
	}
	if cfg.EffectiveAIConfig() != cfg.EffectiveAIConfig("assistant") {
		t.Fatal("默认用途必须为 assistant")
	}
	if got := cfg.EffectiveAIConfig("unknown"); got != (contracts.AIConf{}) {
		t.Fatal("非法用途不得启用旧凭据")
	}
}

func TestAIProfilesDefaultsAndTrimming(t *testing.T) {
	data := profileTestConfig()
	primary := data["ai_profiles"].(map[string]any)["primary"].(map[string]any)
	primary["name"] = "  Primary  "
	primary["api_key"] = " primary-key "
	primary["base_url"] = " https://primary.example/v1 "
	primary["model"] = " primary-model "
	data["ai_vision"] = map[string]any{"enabled": true, "ocr": false, "description": false}
	data["ai_routing"].(map[string]any)["assistant"].(map[string]any)["supports_vision"] = nil
	cfg := newTestConfig(t, nil, profileJSON(t, data), t.TempDir())
	ai := cfg.EffectiveAIConfig()
	if ai.APIKey != "primary-key" || ai.BaseURL != "https://primary.example/v1" || ai.Model != "primary-model" {
		t.Fatal("档案字符串应去掉首尾空白")
	}
	if !ai.SupportsVision || !ai.VisionEnabled || ai.VisionOCR || ai.VisionDescription {
		t.Fatalf("视觉开关或能力继承不符: %+v", ai)
	}
	delete(data, "ai_routing")
	cfg = newTestConfig(t, nil, profileJSON(t, data), t.TempDir())
	if got := cfg.EffectiveAIConfig(); got.APIKey != "legacy-key" || got.ProfileID != "" || got.SupportsVision {
		t.Fatal("未指定用途应保持旧配置且不继承未分配档案能力")
	}
}

func TestAIProfilesRejectInvalidConfiguration(t *testing.T) {
	profileField := func(field string, value any) func(map[string]any) {
		return func(data map[string]any) {
			data["ai_profiles"].(map[string]any)["primary"].(map[string]any)[field] = value
		}
	}
	routeField := func(field string, value any) func(map[string]any) {
		return func(data map[string]any) {
			data["ai_routing"].(map[string]any)["assistant"].(map[string]any)[field] = value
		}
	}
	cases := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"null profiles", func(c map[string]any) { c["ai_profiles"] = nil }},
		{"array profiles", func(c map[string]any) { c["ai_profiles"] = []any{} }},
		{"null routing", func(c map[string]any) { c["ai_routing"] = nil }},
		{"null vision", func(c map[string]any) { c["ai_vision"] = nil }},
		{"invalid profile ID", func(c map[string]any) {
			c["ai_profiles"].(map[string]any)["bad.id"] = c["ai_profiles"].(map[string]any)["primary"]
		}},
		{"nonobject profile", func(c map[string]any) { c["ai_profiles"].(map[string]any)["primary"] = "invalid" }},
		{"missing assigned profile", func(c map[string]any) { delete(c["ai_profiles"].(map[string]any), "primary") }},
		{"unknown purpose", func(c map[string]any) { c["ai_routing"].(map[string]any)["typo"] = map[string]any{} }},
		{"nonobject route", func(c map[string]any) { c["ai_routing"].(map[string]any)["assistant"] = nil }},
		{"unknown profile field", profileField("secret", "not-valid")},
		{"unknown provider", profileField("provider", "anthropic")},
		{"object provider", profileField("provider", map[string]any{})},
		{"missing name", func(c map[string]any) { delete(c["ai_profiles"].(map[string]any)["primary"].(map[string]any), "name") }},
		{"empty name", profileField("name", "   ")},
		{"empty model", profileField("model", " ")},
		{"long name", profileField("name", strings.Repeat("a", 101))},
		{"long model", profileField("model", strings.Repeat("a", 257))},
		{"null key", profileField("api_key", nil)},
		{"key control chars", profileField("api_key", "secret-key\r\n")},
		{"model control chars", profileField("model", "model\x7f")},
		{"string capability", profileField("supports_vision", "true")},
		{"URL scheme", profileField("base_url", "file:///etc/passwd")},
		{"URL host missing", profileField("base_url", "https:///v1")},
		{"URL credentials", profileField("base_url", "https://secret-key@example.com/v1")},
		{"URL query", profileField("base_url", "https://example.com/v1?key=secret-key")},
		{"URL fragment", profileField("base_url", "https://example.com/v1#secret-key")},
		{"URL invalid port", profileField("base_url", "https://example.com:99999/v1")},
		{"URL malformed port", profileField("base_url", "https://example.com:abc/v1")},
		{"null profile ID", routeField("profile_id", nil)},
		{"untrimmed profile ID", routeField("profile_id", " primary ")},
		{"null route model", routeField("model", nil)},
		{"route model control chars", routeField("model", "model\n")},
		{"invalid route capability", routeField("supports_vision", 1)},
		{"unknown route field", routeField("typo", true)},
		{"invalid vision bool", func(c map[string]any) { c["ai_vision"] = map[string]any{"enabled": 1} }},
		{"unknown vision field", func(c map[string]any) { c["ai_vision"] = map[string]any{"typo": true} }},
		{"too many profiles", func(c map[string]any) {
			profiles := c["ai_profiles"].(map[string]any)
			for i := 0; i < 100; i++ {
				profiles[fmt.Sprintf("p%d", i)] = profiles["primary"]
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := profileTestConfig()
			tc.mutate(data)
			cfg := newTestConfig(t, map[string]string{"LLM_API_KEY": "env-key"}, profileJSON(t, data), t.TempDir())
			// Init 被遗漏时也不能静默回退；重复读取同一版本仍应拒绝。
			for i := 0; i < 2; i++ {
				if got := cfg.EffectiveAIConfig(); got != (contracts.AIConf{}) {
					t.Fatal("无效配置启用了后备凭据")
				}
			}
			if err := cfg.Init(); err == nil {
				t.Fatal("启动必须拒绝无效 AI 配置")
			} else if strings.Contains(err.Error(), "secret-key") {
				t.Fatal("配置错误不应泄漏密钥")
			}
		})
	}
}

func TestAIProfilesHotReloadAndRestart(t *testing.T) {
	data := profileTestConfig()
	cfg := newTestConfig(t, map[string]string{"LLM_API_KEY": "env-key"}, profileJSON(t, data), t.TempDir())
	if err := cfg.Init(); err != nil {
		t.Fatal(err)
	}
	initial := cfg.EffectiveAIConfig()

	// 无效档案引用、无效 JSON、临时缺失都不能丢弃生效中的凭据。
	routes := data["ai_routing"].(map[string]any)
	routes["assistant"] = map[string]any{"profile_id": "missing"}
	replaceAIConfig(t, cfg, profileJSON(t, data))
	if got := cfg.EffectiveAIConfig(); got != initial {
		t.Fatal("无效更新必须保留最近一次有效配置")
	}
	restarted := &Config{Env: cfg.Env, runtime: NewRuntimeJSON(cfg.runtime.path)}
	if err := restarted.Init(); err == nil {
		t.Fatal("无效配置重启必须失败")
	}
	replaceAIConfig(t, cfg, "{broken")
	if got := cfg.EffectiveAIConfig(); got != initial {
		t.Fatal("JSON 损坏不能丢弃已生效档案")
	}
	if err := os.Remove(cfg.runtime.path); err != nil {
		t.Fatal(err)
	}
	if got := cfg.EffectiveAIConfig(); got != initial {
		t.Fatal("文件暂时缺失不能丢弃已生效档案")
	}

	// 修复并更换档案后，所有连接字段一起更新；新进程得到相同结果。
	routes["assistant"] = map[string]any{"profile_id": "local", "model": "updated-model", "supports_vision": true}
	data["ai_vision"] = map[string]any{"enabled": true, "description": false}
	replaceAIConfig(t, cfg, profileJSON(t, data))
	updated := cfg.EffectiveAIConfig()
	if updated.ProfileID != "local" || updated.APIKey != "" || updated.BaseURL != "http://localhost:8000/v1" || updated.Model != "updated-model" || !updated.SupportsVision || !updated.VisionEnabled || updated.VisionDescription {
		t.Fatalf("热读配置不符: %+v", updated)
	}
	restarted = &Config{Env: cfg.Env, runtime: NewRuntimeJSON(cfg.runtime.path)}
	if err := restarted.Init(); err != nil {
		t.Fatal(err)
	}
	if got := restarted.EffectiveAIConfig(); got != updated {
		t.Fatal("重启后的配置应与热读相同")
	}

	// 明确清除分配后才恢复旧配置，不能沿用上一个档案的密钥或能力。
	delete(routes, "assistant")
	replaceAIConfig(t, cfg, profileJSON(t, data))
	if got := cfg.EffectiveAIConfig(); got.APIKey != "legacy-key" || got.ProfileID != "" || got.Model != "legacy-model" || got.SupportsVision {
		t.Fatal("取消分配后未恢复旧配置")
	}
}

// TestAIProfilesSharedParity 与 Python 测试读取同一份配置和预期结果。
func TestAIProfilesSharedParity(t *testing.T) {
	data, err := os.ReadFile("testdata/ai_profiles_parity.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []struct {
		Name     string            `json:"name"`
		Config   json.RawMessage   `json:"config"`
		Env      map[string]string `json:"env"`
		Purpose  string            `json:"purpose"`
		Expected struct {
			APIKey            string `json:"api_key"`
			BaseURL           string `json:"base_url"`
			Model             string `json:"model"`
			ProfileID         string `json:"profile_id"`
			SupportsVision    bool   `json:"supports_vision"`
			VisionEnabled     bool   `json:"vision_enabled"`
			VisionOCR         bool   `json:"vision_ocr"`
			VisionDescription bool   `json:"vision_description"`
		} `json:"expected"`
	}
	if err := json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}
	if len(fixtures) == 0 {
		t.Fatal("共享测试数据不能为空")
	}
	for _, tc := range fixtures {
		t.Run(tc.Name, func(t *testing.T) {
			cfg := newTestConfig(t, tc.Env, string(tc.Config), t.TempDir())
			if err := cfg.Init(); err != nil {
				t.Fatal(err)
			}
			want := contracts.AIConf{
				APIKey: tc.Expected.APIKey, BaseURL: tc.Expected.BaseURL, Model: tc.Expected.Model,
				ProfileID: tc.Expected.ProfileID, SupportsVision: tc.Expected.SupportsVision,
				VisionEnabled: tc.Expected.VisionEnabled, VisionOCR: tc.Expected.VisionOCR,
				VisionDescription: tc.Expected.VisionDescription,
			}
			if got := cfg.EffectiveAIConfig(tc.Purpose); got != want {
				t.Fatalf("Go / Python 共享配置预期不符: got=%+v want=%+v", got, want)
			}
		})
	}
}
