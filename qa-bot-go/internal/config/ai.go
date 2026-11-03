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

// EffectiveAIConfig 按用途解析 AI 连接，Go QA 进程默认使用 assistant。
// 已分配服务档案时凭据、地址、模型只从该档案读取，不能与其他档案或
// 旧配置混用；未分配档案时使用旧 config.json → env 链，且该用途路由
// 中的模型与图片能力覆盖同样生效（与 Python ai_runtime 一致）。
func (c *Config) EffectiveAIConfig(purposes ...string) contracts.AIConf {
	purpose := "assistant"
	if len(purposes) > 0 {
		purpose = purposes[0]
	}
	if !validAIPurpose(purpose) {
		return contracts.AIConf{}
	}
	cfg, err := c.runtime.read()
	if err != nil {
		// 未经 Init 的调用也必须拒绝无效配置，不能意外启用环境中的密钥。
		return contracts.AIConf{}
	}
	profiles, err := parseAIProfiles(cfg)
	if err != nil {
		return contracts.AIConf{}
	}
	result := contracts.AIConf{
		VisionEnabled:     profiles.vision.enabled,
		VisionOCR:         profiles.vision.ocr,
		VisionDescription: profiles.vision.description,
	}
	if route, ok := profiles.routing[purpose]; ok && route.profileID != "" {
		profile := profiles.profiles[route.profileID]
		result.APIKey = profile.apiKey
		result.BaseURL = profile.baseURL
		result.Model = profile.model
		result.ProfileID = route.profileID
		result.SupportsVision = profile.supportsVision
		if route.model != "" {
			result.Model = route.model
		}
		if route.supportsVision != nil {
			result.SupportsVision = *route.supportsVision
		}
		return result
	}

	result.APIKey = stringField(cfg, "api_key")
	if result.APIKey == "" {
		result.APIKey = c.Env.Get(KeyLLMAPIKey)
		if result.APIKey == "" {
			result.APIKey = c.Env.Get(KeyDeepseekAPIKey)
		}
	}
	result.BaseURL = stringField(cfg, "base_url")
	if result.BaseURL == "" {
		result.BaseURL = c.Env.GetDefault(KeyLLMBaseURL, defaultLLMBaseURL)
	}
	result.Model = stringField(cfg, "model")
	if result.Model == "" {
		result.Model = c.Env.GetDefault(KeyLLMModel, defaultLLMModel)
	}
	if route, ok := profiles.routing[purpose]; ok {
		if route.model != "" {
			result.Model = route.model
		}
		if route.supportsVision != nil {
			result.SupportsVision = *route.supportsVision
		}
	}
	return result
}

// validAIPurpose 与 Python PURPOSES 保持一致。
func validAIPurpose(purpose string) bool {
	switch purpose {
	case "summary", "assistant", "vision", "poll":
		return true
	default:
		return false
	}
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
