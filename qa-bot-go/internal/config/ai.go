// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package config

import (
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/contracts"
)

// env 默认值（对齐 settings.py AISettings 与 constants.py:105-106）。
const (
	defaultLLMBaseURL = "https://api.deepseek.com"
	defaultLLMModel   = "deepseek-chat"
)

// EffectiveAIConfig 返回 AI 连接的最终有效配置：
// config.json 的 api_key/base_url/model 优先（仅接受非空字符串），
// 否则回退 env（LLM_API_KEY 缺失/为空时回退 DEEPSEEK_API_KEY）。
// 对齐 core/config/ai_runtime.py:19-29。
func (c *Config) EffectiveAIConfig() contracts.AIConf {
	cfg := c.readConfig()

	key := stringField(cfg, "api_key")
	if key == "" {
		key = c.Env.Get(KeyLLMAPIKey)
		if key == "" {
			key = c.Env.Get(KeyDeepseekAPIKey)
		}
	}
	baseURL := stringField(cfg, "base_url")
	if baseURL == "" {
		baseURL = c.Env.GetDefault(KeyLLMBaseURL, defaultLLMBaseURL)
	}
	model := stringField(cfg, "model")
	if model == "" {
		model = c.Env.GetDefault(KeyLLMModel, defaultLLMModel)
	}
	return contracts.AIConf{APIKey: key, BaseURL: baseURL, Model: model}
}

// stringField 取顶层字符串字段；非字符串或空串一律返回空
// （对齐 Python `config.get(k) or default` 的真值判断链）。
func stringField(cfg map[string]any, key string) string {
	v, ok := cfg[key]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}
