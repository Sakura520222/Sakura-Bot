package config

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

type aiModel struct{ supportsVision bool }
type aiProvider struct {
	apiKey  string
	baseURL string
	models  map[string]aiModel
}

// The provider + model pair owns capabilities; credentials belong to the provider.
func parseAIProviders(cfg map[string]any) (map[string]aiProvider, error) {
	result := make(map[string]aiProvider)
	providers, err := aiObject(cfg, "ai_providers")
	if err != nil {
		return nil, err
	}
	if len(providers) > 100 {
		return nil, fmt.Errorf("ai_providers: 厂商超过 100 个")
	}
	for id, raw := range providers {
		if !aiProfileIDPattern.MatchString(id) {
			return nil, fmt.Errorf("ai_providers: 厂商 ID 无效")
		}
		provider, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("ai_providers: 厂商必须为对象")
		}
		if err := aiKnownFields(provider, "ai_providers", "name", "provider", "api_key", "base_url", "models"); err != nil {
			return nil, err
		}
		if _, err := aiString(provider, "name", "ai_profiles", true, 100); err != nil {
			return nil, err
		}
		if v, exists := provider["provider"]; exists && v != "openai" {
			return nil, fmt.Errorf("ai_providers.provider: 协议无效")
		}
		baseURL, err := aiString(provider, "base_url", "ai_profiles", true, 2048)
		if err != nil {
			return nil, err
		}
		if err := aiHTTPURL(baseURL); err != nil {
			return nil, err
		}
		key, err := aiString(provider, "api_key", "ai_profiles", false, 8192)
		if err != nil {
			return nil, err
		}
		models, err := aiObject(provider, "models")
		if err != nil {
			return nil, err
		}
		if len(models) > 500 {
			return nil, fmt.Errorf("ai_providers.models: 模型过多")
		}
		parsed := aiProvider{apiKey: key, baseURL: baseURL, models: make(map[string]aiModel)}
		for modelID, rawModel := range models {
			if modelID == "" || strings.TrimSpace(modelID) != modelID || utf8.RuneCountInString(modelID) > 256 {
				return nil, fmt.Errorf("ai_providers.models: 模型 ID 无效")
			}
			if _, err := aiString(map[string]any{"id": modelID}, "id", "models", true, 256); err != nil {
				return nil, err
			}
			model, ok := rawModel.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("ai_providers.models: 模型必须为对象")
			}
			if err := aiKnownFields(model, "ai_providers.models", "name", "supports_vision"); err != nil {
				return nil, err
			}
			if _, err := aiString(model, "name", "ai_profiles", false, 100); err != nil {
				return nil, err
			}
			vision, err := aiBool(model, "supports_vision", "models", false)
			if err != nil {
				return nil, err
			}
			parsed.models[modelID] = aiModel{supportsVision: vision}
		}
		result[id] = parsed
	}
	return result, nil
}
