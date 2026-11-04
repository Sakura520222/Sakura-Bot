// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package config

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// AI 档案的严格校验与 Python core/config/ai_profiles.py 保持一致。
// 错误仅包含字段路径，不记录实际值，避免密钥写入日志。
type aiProfile struct {
	apiKey         string
	baseURL        string
	model          string
	supportsVision bool
}

type aiRoute struct {
	profileID      string
	model          string
	providerID     string
	modelID        string
	supportsVision *bool
}

type aiVision struct {
	enabled     bool
	ocr         bool
	description bool
}

type aiProfiles struct {
	profiles  map[string]aiProfile
	providers map[string]aiProvider
	routing   map[string]aiRoute
	vision    aiVision
}

var aiProfileIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

func parseAIProfiles(cfg map[string]any) (aiProfiles, error) {
	result := aiProfiles{
		profiles: make(map[string]aiProfile),
		routing:  make(map[string]aiRoute),
		vision:   aiVision{ocr: true, description: true},
	}
	version := 1
	if raw, ok := cfg["ai_config_version"]; ok {
		switch v := raw.(type) {
		case json.Number:
			n, err := v.Int64()
			if err != nil || (n != 1 && n != 2) {
				return result, fmt.Errorf("ai_config_version: 版本无效")
			}
			version = int(n)
		case float64:
			if v != 1 && v != 2 {
				return result, fmt.Errorf("ai_config_version: 版本无效")
			}
			version = int(v)
		case int:
			version = v
		default:
			return result, fmt.Errorf("ai_config_version: 版本无效")
		}
		if version != 1 && version != 2 {
			return result, fmt.Errorf("ai_config_version: 版本无效")
		}
	}
	var providerErr error
	result.providers, providerErr = parseAIProviders(cfg)
	if providerErr != nil {
		return result, providerErr
	}
	if version == 1 && len(result.providers) > 0 {
		return result, fmt.Errorf("ai_providers: 需要配置版本 2")
	}
	profiles, err := aiObject(cfg, "ai_profiles")
	if err != nil {
		return result, err
	}
	if len(profiles) > 100 {
		return result, fmt.Errorf("ai_profiles: 服务档案超过 100 个")
	}
	for id, raw := range profiles {
		if !aiProfileIDPattern.MatchString(id) {
			return result, fmt.Errorf("ai_profiles: 档案 ID 无效")
		}
		profile, ok := raw.(map[string]any)
		if !ok {
			return result, fmt.Errorf("ai_profiles: 档案必须为对象")
		}
		if err := aiKnownFields(profile, "ai_profiles", "name", "provider", "api_key", "base_url", "model", "supports_vision"); err != nil {
			return result, err
		}
		if _, err := aiString(profile, "name", "ai_profiles", true, 100); err != nil {
			return result, err
		}
		if provider, exists := profile["provider"]; exists && provider != "openai" {
			return result, fmt.Errorf("ai_profiles.provider: 仅支持 openai")
		}
		baseURL, err := aiString(profile, "base_url", "ai_profiles", true, 2048)
		if err != nil {
			return result, err
		}
		if err := aiHTTPURL(baseURL); err != nil {
			return result, err
		}
		model, err := aiString(profile, "model", "ai_profiles", true, 256)
		if err != nil {
			return result, err
		}
		key, err := aiString(profile, "api_key", "ai_profiles", false, 8192)
		if err != nil {
			return result, err
		}
		vision, err := aiBool(profile, "supports_vision", "ai_profiles", false)
		if err != nil {
			return result, err
		}
		result.profiles[id] = aiProfile{apiKey: key, baseURL: baseURL, model: model, supportsVision: vision}
	}

	routes, err := aiObject(cfg, "ai_routing")
	if err != nil {
		return result, err
	}
	for purpose, raw := range routes {
		if !validAIPurpose(purpose) {
			return result, fmt.Errorf("ai_routing: 用途无效")
		}
		route, ok := raw.(map[string]any)
		if !ok {
			return result, fmt.Errorf("ai_routing: 路由必须为对象")
		}
		if err := aiKnownFields(route, "ai_routing", "profile_id", "model", "provider_id", "model_id", "supports_vision"); err != nil {
			return result, err
		}
		id := ""
		if rawID, exists := route["profile_id"]; exists {
			var ok bool
			id, ok = rawID.(string)
			if !ok {
				return result, fmt.Errorf("ai_routing.profile_id: 必须为字符串")
			}
		}
		if id != "" {
			if _, exists := result.profiles[id]; !exists {
				return result, fmt.Errorf("ai_routing.profile_id: 服务档案不存在")
			}
		}
		model, err := aiString(route, "model", "ai_routing", false, 256)
		if err != nil {
			return result, err
		}
		var vision *bool
		if rawVision, exists := route["supports_vision"]; exists && rawVision != nil {
			value, ok := rawVision.(bool)
			if !ok {
				return result, fmt.Errorf("ai_routing.supports_vision: 必须为布尔或 null")
			}
			vision = &value
		}
		providerID, err := aiString(route, "provider_id", "ai_routing", false, 64)
		if err != nil {
			return result, err
		}
		modelID, err := aiString(route, "model_id", "ai_routing", false, 256)
		if err != nil {
			return result, err
		}
		if providerID != "" {
			provider, exists := result.providers[providerID]
			if !exists || id != "" || model != "" || modelID == "" {
				return result, fmt.Errorf("ai_routing: 厂商或模型引用无效")
			}
			if _, ok := provider.models[modelID]; !ok {
				return result, fmt.Errorf("ai_routing: 模型不存在")
			}
		} else if modelID != "" {
			return result, fmt.Errorf("ai_routing: 模型缺少厂商")
		}
		if version == 2 && (id != "" || model != "") {
			return result, fmt.Errorf("ai_routing: 旧路由不能写入新版配置")
		}
		result.routing[purpose] = aiRoute{profileID: id, model: model, providerID: providerID, modelID: modelID, supportsVision: vision}
	}

	vision, err := aiObject(cfg, "ai_vision")
	if err != nil {
		return result, err
	}
	if err := aiKnownFields(vision, "ai_vision", "enabled", "ocr", "description"); err != nil {
		return result, err
	}
	result.vision.enabled, err = aiBool(vision, "enabled", "ai_vision", false)
	if err != nil {
		return result, err
	}
	result.vision.ocr, err = aiBool(vision, "ocr", "ai_vision", true)
	if err != nil {
		return result, err
	}
	result.vision.description, err = aiBool(vision, "description", "ai_vision", true)
	return result, err
}

func aiObject(cfg map[string]any, field string) (map[string]any, error) {
	raw, exists := cfg[field]
	if !exists {
		return map[string]any{}, nil
	}
	result, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s: 必须为对象", field)
	}
	return result, nil
}

func aiKnownFields(object map[string]any, path string, names ...string) error {
	for field := range object {
		known := false
		for _, name := range names {
			if name == field {
				known = true
				break
			}
		}
		if !known {
			return fmt.Errorf("%s: 包含未知字段", path)
		}
	}
	return nil
}

func aiString(object map[string]any, field, path string, required bool, maxLength int) (string, error) {
	raw, exists := object[field]
	if !exists && !required {
		return "", nil
	}
	value, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("%s.%s: 必须为字符串", path, field)
	}
	// 档案字段在裁剪后校验长度；路由 model 与 Python after 校验器一致。
	lengthValue := value
	if path == "ai_profiles" {
		lengthValue = strings.TrimSpace(value)
	}
	if utf8.RuneCountInString(lengthValue) > maxLength {
		return "", fmt.Errorf("%s.%s: 字符串长度无效", path, field)
	}
	for _, ch := range value {
		if ch < 32 || ch == 127 {
			return "", fmt.Errorf("%s.%s: 不允许控制字符", path, field)
		}
	}
	value = strings.TrimSpace(value)
	if required && value == "" {
		return "", fmt.Errorf("%s.%s: 不能为空", path, field)
	}
	return value, nil
}

func aiBool(object map[string]any, field, path string, defaultValue bool) (bool, error) {
	raw, exists := object[field]
	if !exists {
		return defaultValue, nil
	}
	value, ok := raw.(bool)
	if !ok {
		return false, fmt.Errorf("%s.%s: 必须为布尔", path, field)
	}
	return value, nil
}

func aiHTTPURL(value string) error {
	u, err := url.Parse(value)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("ai_profiles.base_url: HTTP(S) 地址无效")
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 0 || n > 65535 {
			return fmt.Errorf("ai_profiles.base_url: 端口无效")
		}
	}
	return nil
}
