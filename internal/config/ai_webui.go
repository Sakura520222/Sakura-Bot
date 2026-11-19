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
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/Sakura520222/Sakura-Bot/internal/contracts"
)

// AIPurposes 列出系统支持的全部 AI 用途（与 core/config/ai_profiles.py:PURPOSES 顺序一致）。
var AIPurposes = []string{
	"summary",
	"assistant",
	"vision",
	"poll",
	"wiki",
	"wiki_review",
	"guard",
	"guard_vision",
}

// MaskAPIKeyPreview 返回服务档案 / 厂商的固定掩码预览（对齐 core/web_api/routes/ai_profiles.py:37,208）。
func MaskAPIKeyPreview(apiKey string) string {
	if apiKey == "" {
		return ""
	}
	return "********"
}

// MaskLegacyAPIKeyPreview 返回旧版顶层 AI 配置的脱敏预览（对齐 core/web_api/routes/ai_config.py:71）。
func MaskLegacyAPIKeyPreview(apiKey string) string {
	if apiKey == "" {
		return ""
	}
	if len(apiKey) > 8 {
		return apiKey[:4] + "..." + apiKey[len(apiKey)-4:]
	}
	return "***"
}

// ProfilesRevision 计算 v1 ai_profiles 的乐观锁 SHA-256 摘要，
// JSON 格式逐字节对齐 Python `hashlib.sha256(json.dumps(content, sort_keys=True).encode()).hexdigest()`。
func ProfilesRevision(doc map[string]any) string {
	content := map[string]any{
		"ai_profiles": mapOrDefaultEmpty(doc, "ai_profiles"),
		"ai_routing":  mapOrDefaultEmpty(doc, "ai_routing"),
		"ai_vision":   mapOrDefaultEmpty(doc, "ai_vision"),
	}
	return sha256PythonSortedJSON(content)
}

// ProvidersRevision 计算 v2 ai_providers 的乐观锁 SHA-256 摘要，
// JSON 格式逐字节对齐 Python `providers_revision(config)`。
func ProvidersRevision(doc map[string]any) string {
	fields := []string{
		"ai_profiles",
		"ai_providers",
		"ai_routing",
		"ai_vision",
		"ai_config_version",
		"api_key",
		"base_url",
		"model",
	}
	content := make(map[string]any, len(fields))
	for _, k := range fields {
		if doc != nil {
			content[k] = doc[k]
		} else {
			content[k] = nil
		}
	}
	return sha256PythonSortedJSON(content)
}

func mapOrDefaultEmpty(doc map[string]any, key string) any {
	if doc == nil {
		return map[string]any{}
	}
	if v, exists := doc[key]; exists {
		return v
	}
	return map[string]any{}
}

// ────────────────────────────── v1 AI Profiles 脱敏视图与保存 ──────────────────────────────

// PublicAIProfile 是对外隐藏真实密钥后的 v1 服务档案视图。
type PublicAIProfile struct {
	Name           string `json:"name"`
	Provider       string `json:"provider"`
	BaseURL        string `json:"base_url"`
	Model          string `json:"model"`
	SupportsVision bool   `json:"supports_vision"`
	APIKeySet      bool   `json:"api_key_set"`
	APIKeyPreview  string `json:"api_key_preview"`
}

// PublicAIRoute 是 v1 路由项视图。
type PublicAIRoute struct {
	ProfileID       string `json:"profile_id"`
	Model           string `json:"model"`
	ProviderID      string `json:"provider_id"`
	ModelID         string `json:"model_id"`
	SupportsVision  *bool  `json:"supports_vision"`
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
}

// PublicAIVision 是视觉处理开关视图。
type PublicAIVision struct {
	Enabled     bool `json:"enabled"`
	OCR         bool `json:"ocr"`
	Description bool `json:"description"`
}

// PublicEffectiveProfile 是 v1 各用途最终解析出的档案与模型摘要。
type PublicEffectiveProfile struct {
	ProfileID       string `json:"profile_id"`
	Model           string `json:"model"`
	SupportsVision  bool   `json:"supports_vision"`
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
}

// PublicAIProfilesView 对齐 GET/PUT /api/ai/profiles 的 data 响应结构。
type PublicAIProfilesView struct {
	Profiles  map[string]PublicAIProfile        `json:"profiles"`
	Routing   map[string]PublicAIRoute          `json:"routing"`
	Vision    PublicAIVision                    `json:"vision"`
	Effective map[string]PublicEffectiveProfile `json:"effective"`
	Revision  string                            `json:"revision"`
}

// PublicAIProfiles 读取并构造脱敏的 v1 服务档案配置视图。
func (c *Config) PublicAIProfiles() (*PublicAIProfilesView, error) {
	doc := c.RawSnapshot()
	return BuildPublicAIProfiles(doc, c.Env)
}

// BuildPublicAIProfiles 由给定配置文档构造脱敏的 v1 服务档案视图。
func BuildPublicAIProfiles(doc map[string]any, env *Env) (*PublicAIProfilesView, error) {
	parsed, err := parseAIProfiles(doc)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrConfigValidation, err)
	}
	profiles := make(map[string]PublicAIProfile, len(parsed.profiles))
	for id, p := range parsed.profiles {
		profiles[id] = PublicAIProfile{
			Name:           p.name,
			Provider:       p.provider,
			BaseURL:        p.baseURL,
			Model:          p.model,
			SupportsVision: p.supportsVision,
			APIKeySet:      p.apiKey != "",
			APIKeyPreview:  MaskAPIKeyPreview(p.apiKey),
		}
	}
	routing := make(map[string]PublicAIRoute, len(parsed.routing))
	for purpose, r := range parsed.routing {
		routing[purpose] = PublicAIRoute{
			ProfileID:       r.profileID,
			Model:           r.model,
			ProviderID:      r.providerID,
			ModelID:         r.modelID,
			SupportsVision:  cloneBoolPtr(r.supportsVision),
			ReasoningEffort: r.reasoningEffort,
		}
	}
	effective := make(map[string]PublicEffectiveProfile, len(AIPurposes))
	for _, purpose := range AIPurposes {
		resolved := resolveEffectiveFromDoc(doc, env, purpose)
		effective[purpose] = PublicEffectiveProfile{
			ProfileID:       resolved.ProfileID,
			Model:           resolved.Model,
			SupportsVision:  resolved.SupportsVision,
			ReasoningEffort: resolved.ReasoningEffort,
		}
	}
	return &PublicAIProfilesView{
		Profiles: profiles,
		Routing:  routing,
		Vision: PublicAIVision{
			Enabled:     parsed.vision.enabled,
			OCR:         parsed.vision.ocr,
			Description: parsed.vision.description,
		},
		Effective: effective,
		Revision:  ProfilesRevision(doc),
	}, nil
}

// SaveAIProfiles 校验乐观锁 revision、保留未重传的已有 api_key 并原子保存 v1 服务档案
// （对齐 core/web_api/routes/ai_profiles.py:save_profiles）。
func (c *Config) SaveAIProfiles(body map[string]any) (*PublicAIProfilesView, error) {
	if !hasExactKeys(body, "profiles", "routing", "vision", "revision") {
		return nil, fmt.Errorf("%w: 请求字段无效", ErrConfigValidation)
	}
	rev, ok := body["revision"].(string)
	if !ok {
		return nil, fmt.Errorf("%w: revision 必须为字符串", ErrConfigValidation)
	}
	rawProfiles, ok := body["profiles"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%w: profiles 必须为对象", ErrConfigValidation)
	}

	err := c.UpdateConfig(func(doc map[string]any) error {
		if ver, exists := doc["ai_config_version"]; exists {
			if n, ok := parseLooseInt(ver); !ok || n != 1 {
				return fmt.Errorf("%w: AI 配置已升级到新版，请刷新页面", ErrConfigConflict)
			}
		}
		if rev != ProfilesRevision(doc) {
			return fmt.Errorf("%w: AI 配置版本冲突", ErrConfigConflict)
		}

		savedProfiles, _ := doc["ai_profiles"].(map[string]any)
		candProfiles := deepCopyMap(rawProfiles)
		for key, item := range candProfiles {
			pm, ok := item.(map[string]any)
			if !ok {
				return fmt.Errorf("%w: 档案必须为对象", ErrConfigValidation)
			}
			if _, hasKey := pm["api_key"]; !hasKey {
				existingKey := ""
				if savedProfiles != nil {
					if sp, ok := savedProfiles[key].(map[string]any); ok {
						existingKey, _ = sp["api_key"].(string)
					}
				}
				pm["api_key"] = existingKey
			}
		}

		tempDoc := map[string]any{
			"ai_config_version": 1,
			"ai_profiles":       candProfiles,
			"ai_routing":        deepCopyValue(body["routing"]),
			"ai_vision":         deepCopyValue(body["vision"]),
		}
		parsed, err := parseAIProfiles(tempDoc)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrConfigValidation, err)
		}

		doc["ai_profiles"] = dumpNormalizedProfiles(parsed.profiles)
		doc["ai_routing"] = dumpNormalizedRoutes(parsed.routing)
		doc["ai_vision"] = dumpNormalizedVision(parsed.vision)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return c.PublicAIProfiles()
}

// ────────────────────────────── v2 AI Providers 迁移、视图与保存 ──────────────────────────────

// PublicAIModel 是 v2 厂商下的模型能力描述。
type PublicAIModel struct {
	Name           string `json:"name"`
	SupportsVision bool   `json:"supports_vision"`
}

// PublicAIProvider 是对外隐藏真实密钥后的 v2 厂商视图。
type PublicAIProvider struct {
	Name          string                   `json:"name"`
	Provider      string                   `json:"provider"`
	BaseURL       string                   `json:"base_url"`
	Models        map[string]PublicAIModel `json:"models"`
	APIKeySet     bool                     `json:"api_key_set"`
	APIKeyPreview string                   `json:"api_key_preview"`
}

// PublicProviderRoute 是 v2 用途路由视图。
type PublicProviderRoute struct {
	ProviderID      string `json:"provider_id"`
	ModelID         string `json:"model_id"`
	SupportsVision  *bool  `json:"supports_vision"`
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
}

// PublicEffectiveProvider 是 v2 各用途最终解析出的厂商与模型摘要。
type PublicEffectiveProvider struct {
	ProviderID      string `json:"provider_id"`
	ModelID         string `json:"model_id"`
	SupportsVision  bool   `json:"supports_vision"`
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
}

// PublicAIProvidersView 对齐 GET/PUT /api/ai/providers 的 data 响应结构。
type PublicAIProvidersView struct {
	Version   int                                `json:"version"`
	Providers map[string]PublicAIProvider        `json:"providers"`
	Routing   map[string]PublicProviderRoute     `json:"routing"`
	Vision    PublicAIVision                     `json:"vision"`
	Effective map[string]PublicEffectiveProvider `json:"effective"`
	Revision  string                             `json:"revision"`
}

// migrateAIProvidersInternal 将 v1 配置无损内存迁移为 v2 结构（不写盘，对齐 core/config/ai_profiles.py:migrate_providers）。
func migrateAIProvidersInternal(doc map[string]any, env *Env) (aiProfiles, error) {
	parsed, err := parseAIProfiles(doc)
	if err != nil {
		return aiProfiles{}, fmt.Errorf("%w: %v", ErrConfigValidation, err)
	}
	if parsed.version == 2 {
		return parsed, nil
	}

	providers := make(map[string]aiProvider, len(parsed.profiles)+1)
	for id, prof := range parsed.profiles {
		providers[id] = aiProvider{
			name:     prof.name,
			provider: prof.provider,
			apiKey:   prof.apiKey,
			baseURL:  prof.baseURL,
			models: map[string]aiModel{
				prof.model: {name: "", supportsVision: prof.supportsVision},
			},
		}
	}

	defaultID := "legacy_default"
	for {
		if _, exists := providers[defaultID]; !exists {
			break
		}
		defaultID += "_"
	}

	defAPIKey, defBaseURL, defModel := legacyEnvAIDefaults(env)

	routing := make(map[string]aiRoute, len(AIPurposes))
	for _, purpose := range AIPurposes {
		route := parsed.routing[purpose]
		if route.profileID == "" && route.model == "" {
			routing[purpose] = aiRoute{supportsVision: cloneBoolPtr(route.supportsVision), reasoningEffort: route.reasoningEffort}
			continue
		}
		var (
			provID  string
			modelID string
			vision  bool
		)
		if route.profileID != "" {
			provID = route.profileID
			prof := parsed.profiles[provID]
			modelID = route.model
			if modelID == "" {
				modelID = prof.model
			}
			vision = prof.supportsVision
		} else {
			provID = defaultID
			modelID = route.model
			if modelID == "" {
				modelID = stringField(doc, "model")
				if modelID == "" {
					modelID = defModel
				}
			}
			vision = false
			if _, exists := providers[defaultID]; !exists {
				baseURL := stringField(doc, "base_url")
				if baseURL == "" {
					baseURL = defBaseURL
				}
				apiKey := stringField(doc, "api_key")
				if apiKey == "" {
					apiKey = defAPIKey
				}
				providers[defaultID] = aiProvider{
					name:     "默认服务（迁移）",
					provider: "openai",
					apiKey:   apiKey,
					baseURL:  baseURL,
					models:   make(map[string]aiModel),
				}
			}
		}
		prov := providers[provID]
		if prov.models == nil {
			prov.models = make(map[string]aiModel)
		}
		if _, exists := prov.models[modelID]; !exists {
			prov.models[modelID] = aiModel{name: "", supportsVision: vision}
		}
		providers[provID] = prov
		routing[purpose] = aiRoute{
			providerID:      provID,
			modelID:         modelID,
			supportsVision:  cloneBoolPtr(route.supportsVision),
			reasoningEffort: route.reasoningEffort,
		}
	}

	return aiProfiles{
		version:   2,
		providers: providers,
		routing:   routing,
		vision:    parsed.vision,
	}, nil
}

// PublicAIProviders 读取（含 v1→v2 只读迁移）并构造脱敏的 v2 厂商配置视图。
func (c *Config) PublicAIProviders() (*PublicAIProvidersView, error) {
	doc := c.RawSnapshot()
	return BuildPublicAIProviders(doc, c.Env)
}

// BuildPublicAIProviders 由给定配置文档构造脱敏的 v2 厂商视图（对齐 public_providers）。
func BuildPublicAIProviders(doc map[string]any, env *Env) (*PublicAIProvidersView, error) {
	migrated, err := migrateAIProvidersInternal(doc, env)
	if err != nil {
		return nil, err
	}

	providersView := make(map[string]PublicAIProvider, len(migrated.providers))
	for id, p := range migrated.providers {
		modelsView := make(map[string]PublicAIModel, len(p.models))
		for mid, m := range p.models {
			modelsView[mid] = PublicAIModel{
				Name:           m.name,
				SupportsVision: m.supportsVision,
			}
		}
		providersView[id] = PublicAIProvider{
			Name:          p.name,
			Provider:      p.provider,
			BaseURL:       p.baseURL,
			Models:        modelsView,
			APIKeySet:     p.apiKey != "",
			APIKeyPreview: MaskAPIKeyPreview(p.apiKey),
		}
	}

	routingView := make(map[string]PublicProviderRoute, len(migrated.routing))
	for purpose, r := range migrated.routing {
		routingView[purpose] = PublicProviderRoute{
			ProviderID:      r.providerID,
			ModelID:         r.modelID,
			SupportsVision:  cloneBoolPtr(r.supportsVision),
			ReasoningEffort: r.reasoningEffort,
		}
	}

	resolvedDoc := deepCopyMap(doc)
	if resolvedDoc == nil {
		resolvedDoc = map[string]any{}
	}
	resolvedDoc["ai_config_version"] = 2
	resolvedDoc["ai_providers"] = dumpNormalizedProviders(migrated.providers)
	resolvedDoc["ai_routing"] = dumpNormalizedRoutes(migrated.routing)
	resolvedDoc["ai_vision"] = dumpNormalizedVision(migrated.vision)

	effectiveView := make(map[string]PublicEffectiveProvider, len(AIPurposes))
	for _, purpose := range AIPurposes {
		resolved := resolveEffectiveFromDoc(resolvedDoc, env, purpose)
		effectiveView[purpose] = PublicEffectiveProvider{
			ProviderID:      resolved.ProfileID,
			ModelID:         resolved.Model,
			SupportsVision:  resolved.SupportsVision,
			ReasoningEffort: resolved.ReasoningEffort,
		}
	}

	return &PublicAIProvidersView{
		Version:   2,
		Providers: providersView,
		Routing:   routingView,
		Vision: PublicAIVision{
			Enabled:     migrated.vision.enabled,
			OCR:         migrated.vision.ocr,
			Description: migrated.vision.description,
		},
		Effective: effectiveView,
		Revision:  ProvidersRevision(doc),
	}, nil
}

// SaveAIProviders 校验乐观锁 revision、从迁移基线恢复省略的 api_key 并原子保存 v2 厂商配置
// （对齐 core/web_api/routes/ai_profiles.py:save_providers）。
func (c *Config) SaveAIProviders(body map[string]any) (*PublicAIProvidersView, error) {
	if !hasExactKeys(body, "version", "providers", "routing", "vision", "revision") {
		return nil, fmt.Errorf("%w: 请求字段无效", ErrConfigValidation)
	}
	if !isStrictVersion2(body["version"]) {
		return nil, fmt.Errorf("%w: version 必须为 2", ErrConfigValidation)
	}
	rev, ok := body["revision"].(string)
	if !ok {
		return nil, fmt.Errorf("%w: revision 必须为字符串", ErrConfigValidation)
	}
	rawProviders, ok := body["providers"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%w: providers 必须为对象", ErrConfigValidation)
	}

	err := c.UpdateConfig(func(doc map[string]any) error {
		if rev != ProvidersRevision(doc) {
			return fmt.Errorf("%w: AI 配置已被其他操作修改", ErrConfigConflict)
		}
		previous, err := migrateAIProvidersInternal(doc, c.Env)
		if err != nil {
			return err
		}

		candProviders := deepCopyMap(rawProviders)
		for key, item := range candProviders {
			pm, ok := item.(map[string]any)
			if !ok {
				return fmt.Errorf("%w: 厂商必须为对象", ErrConfigValidation)
			}
			if _, hasKey := pm["api_key"]; !hasKey {
				if prevProv, exists := previous.providers[key]; exists {
					pm["api_key"] = prevProv.apiKey
				} else {
					pm["api_key"] = ""
				}
			}
		}

		tempDoc := map[string]any{
			"ai_config_version": 2,
			"ai_providers":      candProviders,
			"ai_routing":        deepCopyValue(body["routing"]),
			"ai_vision":         deepCopyValue(body["vision"]),
		}
		parsed, err := parseAIProfiles(tempDoc)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrConfigValidation, err)
		}

		doc["ai_config_version"] = 2
		doc["ai_providers"] = dumpNormalizedProviders(parsed.providers)
		doc["ai_routing"] = dumpNormalizedRoutes(parsed.routing)
		doc["ai_vision"] = dumpNormalizedVision(parsed.vision)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return c.PublicAIProviders()
}

// ────────────────────────────── 连接参数解析 & 旧版 AI 配置辅助 ──────────────────────────────

// ResolveProfileConnection 解析 v1 档案列模型/测试使用的连接参数（草稿优先，缺省沿用已保存档案）。
func (c *Config) ResolveProfileConnection(profileID string, body map[string]any) (baseURL, apiKey, savedModel string, err error) {
	parsed, parseErr := parseAIProfiles(c.RawSnapshot())
	if parseErr != nil {
		return "", "", "", fmt.Errorf("%w: %v", ErrConfigValidation, parseErr)
	}
	saved, hasSaved := parsed.profiles[profileID]
	if hasSaved {
		savedModel = saved.model
	}

	draftURL, draftKey, hasDraft, draftErr := parseDraftConnection(body)
	if draftErr != nil {
		return "", "", "", draftErr
	}
	if !hasDraft {
		if !hasSaved {
			return "", "", "", fmt.Errorf("%w: %s", ErrProfileNotFound, profileID)
		}
		return saved.baseURL, saved.apiKey, saved.model, nil
	}
	if draftKey == "" && hasSaved {
		draftKey = saved.apiKey
	}
	return draftURL, draftKey, savedModel, nil
}

// ResolveProviderConnection 解析 v2 厂商列模型/测试使用的连接参数（支持 clear_api_key: true）。
func (c *Config) ResolveProviderConnection(providerID string, body map[string]any) (baseURL, apiKey string, err error) {
	migrated, migErr := migrateAIProvidersInternal(c.RawSnapshot(), c.Env)
	if migErr != nil {
		return "", "", migErr
	}
	saved, hasSaved := migrated.providers[providerID]

	draftURL, draftKey, hasDraft, draftErr := parseDraftConnection(body)
	if draftErr != nil {
		return "", "", draftErr
	}
	if !hasDraft && !hasSaved {
		return "", "", fmt.Errorf("%w: %s", ErrProfileNotFound, providerID)
	}

	if hasDraft {
		baseURL, apiKey = draftURL, draftKey
	} else {
		baseURL, apiKey = saved.baseURL, saved.apiKey
	}

	if clearKey, ok := body["clear_api_key"].(bool); ok && clearKey {
		apiKey = ""
	} else if apiKey == "" && hasSaved {
		apiKey = saved.apiKey
	}
	return baseURL, apiKey, nil
}

// LegacyAIConfigView 对齐 GET /api/ai/config 的脱敏视图。
type LegacyAIConfigView struct {
	BaseURL       string `json:"base_url"`
	Model         string `json:"model"`
	APIKeySet     bool   `json:"api_key_set"`
	APIKeyPreview string `json:"api_key_preview"`
}

// LegacyAIConfig 读取忽略 ai_routing 后的旧版顶层 AI 配置视图（对齐 routes/ai_config.py:61-80）。
func (c *Config) LegacyAIConfig() (LegacyAIConfigView, error) {
	doc := c.RawSnapshot()
	if _, err := parseAIProfiles(doc); err != nil {
		return LegacyAIConfigView{}, fmt.Errorf("%w: %v", ErrConfigValidation, err)
	}
	delete(doc, "ai_routing")
	resolved := resolveEffectiveFromDoc(doc, c.Env, "summary")
	return LegacyAIConfigView{
		BaseURL:       resolved.BaseURL,
		Model:         resolved.Model,
		APIKeySet:     resolved.APIKey != "",
		APIKeyPreview: MaskLegacyAPIKeyPreview(resolved.APIKey),
	}, nil
}

// ────────────────────────────── 内部序列化与 Python 兼容哈希辅助 ──────────────────────────────

func parseDraftConnection(body map[string]any) (baseURL, apiKey string, hasDraft bool, err error) {
	if len(body) == 0 {
		return "", "", false, nil
	}
	rawURL, exists := body["base_url"]
	if !exists || rawURL == nil || rawURL == "" {
		return "", "", false, nil
	}
	u, urlErr := aiString(body, "base_url", "ai_profiles", true, 2048)
	if urlErr != nil {
		return "", "", false, fmt.Errorf("%w: base_url 无效", ErrConfigValidation)
	}
	if httpErr := aiHTTPURL(u); httpErr != nil {
		return "", "", false, fmt.Errorf("%w: base_url 无效", ErrConfigValidation)
	}
	key := ""
	if rawKey, hasKey := body["api_key"]; hasKey && rawKey != nil {
		k, keyErr := aiString(body, "api_key", "ai_profiles", false, 8192)
		if keyErr != nil {
			return "", "", false, fmt.Errorf("%w: api_key 无效", ErrConfigValidation)
		}
		key = k
	}
	return u, key, true, nil
}

func resolveEffectiveFromDoc(doc map[string]any, env *Env, purpose string) contracts.AIConf {
	profiles, err := parseAIProfiles(doc)
	if err != nil {
		return contracts.AIConf{}
	}
	route := profiles.routing[purpose]
	if route.providerID == "" && route.profileID == "" && route.model == "" {
		switch purpose {
		case "wiki_review":
			purpose = "wiki"
		case "guard":
			purpose = "summary"
		case "guard_vision":
			purpose = "vision"
		}
	}
	result := contracts.AIConf{
		ReasoningEffort:   route.reasoningEffort,
		VisionEnabled:     profiles.vision.enabled,
		VisionOCR:         profiles.vision.ocr,
		VisionDescription: profiles.vision.description,
	}
	if r, ok := profiles.routing[purpose]; ok && r.providerID != "" {
		prov := profiles.providers[r.providerID]
		result.APIKey, result.BaseURL = prov.apiKey, prov.baseURL
		result.Model, result.ProfileID = r.modelID, r.providerID
		result.SupportsVision = prov.models[r.modelID].supportsVision
		if r.supportsVision != nil {
			result.SupportsVision = *r.supportsVision
		}
		return result
	}
	if r, ok := profiles.routing[purpose]; ok && r.profileID != "" {
		prof := profiles.profiles[r.profileID]
		result.APIKey = prof.apiKey
		result.BaseURL = prof.baseURL
		result.Model = prof.model
		result.ProfileID = r.profileID
		result.SupportsVision = prof.supportsVision
		if r.model != "" {
			result.Model = r.model
		}
		if r.supportsVision != nil {
			result.SupportsVision = *r.supportsVision
		}
		return result
	}

	defKey, defBaseURL, defModel := legacyEnvAIDefaults(env)
	result.APIKey = stringField(doc, "api_key")
	if result.APIKey == "" {
		result.APIKey = defKey
	}
	result.BaseURL = stringField(doc, "base_url")
	if result.BaseURL == "" {
		result.BaseURL = defBaseURL
	}
	result.Model = stringField(doc, "model")
	if result.Model == "" {
		result.Model = defModel
	}
	if r, ok := profiles.routing[purpose]; ok {
		if r.model != "" {
			result.Model = r.model
		}
		if r.supportsVision != nil {
			result.SupportsVision = *r.supportsVision
		}
	}
	return result
}

func legacyEnvAIDefaults(env *Env) (apiKey, baseURL, model string) {
	baseURL = defaultLLMBaseURL
	model = defaultLLMModel
	if env == nil {
		return "", baseURL, model
	}
	apiKey = env.Get(KeyLLMAPIKey)
	if apiKey == "" {
		apiKey = env.Get(KeyDeepseekAPIKey)
	}
	baseURL = env.GetDefault(KeyLLMBaseURL, defaultLLMBaseURL)
	model = env.GetDefault(KeyLLMModel, defaultLLMModel)
	return apiKey, baseURL, model
}

func dumpNormalizedProfiles(profiles map[string]aiProfile) map[string]any {
	out := make(map[string]any, len(profiles))
	for id, p := range profiles {
		prov := p.provider
		if prov == "" {
			prov = "openai"
		}
		out[id] = map[string]any{
			"name":            p.name,
			"provider":        prov,
			"base_url":        p.baseURL,
			"api_key":         p.apiKey,
			"model":           p.model,
			"supports_vision": p.supportsVision,
		}
	}
	return out
}

func dumpNormalizedProviders(providers map[string]aiProvider) map[string]any {
	out := make(map[string]any, len(providers))
	for id, p := range providers {
		modelsMap := make(map[string]any, len(p.models))
		for mid, m := range p.models {
			modelsMap[mid] = map[string]any{
				"name":            m.name,
				"supports_vision": m.supportsVision,
			}
		}
		prov := p.provider
		if prov == "" {
			prov = "openai"
		}
		out[id] = map[string]any{
			"name":     p.name,
			"provider": prov,
			"base_url": p.baseURL,
			"api_key":  p.apiKey,
			"models":   modelsMap,
		}
	}
	return out
}

func dumpNormalizedRoutes(routes map[string]aiRoute) map[string]any {
	out := make(map[string]any, len(routes))
	for purpose, r := range routes {
		var sv any
		if r.supportsVision != nil {
			sv = *r.supportsVision
		}
		route := map[string]any{
			"profile_id":      r.profileID,
			"model":           r.model,
			"provider_id":     r.providerID,
			"model_id":        r.modelID,
			"supports_vision": sv,
		}
		if r.reasoningEffort != "" {
			route["reasoning_effort"] = r.reasoningEffort
		}
		out[purpose] = route
	}
	return out
}

func dumpNormalizedVision(v aiVision) map[string]any {
	return map[string]any{
		"enabled":     v.enabled,
		"ocr":         v.ocr,
		"description": v.description,
	}
}

func cloneBoolPtr(p *bool) *bool {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

func hasExactKeys(m map[string]any, want ...string) bool {
	if len(m) != len(want) {
		return false
	}
	for _, k := range want {
		if _, exists := m[k]; !exists {
			return false
		}
	}
	return true
}

func isStrictVersion2(v any) bool {
	switch val := v.(type) {
	case int:
		return val == 2
	case int64:
		return val == 2
	case json.Number:
		return val.String() == "2"
	case float64:
		return val == 2
	default:
		return false
	}
}

// sha256PythonSortedJSON 计算与 Python `json.dumps(v, sort_keys=True)` 逐字节一致的 SHA-256 hex。
func sha256PythonSortedJSON(v any) string {
	var buf bytes.Buffer
	writePythonJSON(&buf, v)
	sum := sha256.Sum256(buf.Bytes())
	return hex.EncodeToString(sum[:])
}

func writePythonJSON(buf *bytes.Buffer, v any) {
	if v == nil {
		buf.WriteString("null")
		return
	}
	switch val := v.(type) {
	case bool:
		if val {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
	case string:
		writePythonJSONString(buf, val)
	case json.Number:
		buf.WriteString(val.String())
	case int:
		buf.WriteString(strconv.Itoa(val))
	case int64:
		buf.WriteString(strconv.FormatInt(val, 10))
	case float64:
		buf.WriteString(strconv.FormatFloat(val, 'g', -1, 64))
	case *bool:
		if val == nil {
			buf.WriteString("null")
		} else if *val {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
	case []any:
		buf.WriteByte('[')
		for i, item := range val {
			if i > 0 {
				buf.WriteString(", ")
			}
			writePythonJSON(buf, item)
		}
		buf.WriteByte(']')
	case []string:
		buf.WriteByte('[')
		for i, item := range val {
			if i > 0 {
				buf.WriteString(", ")
			}
			writePythonJSONString(buf, item)
		}
		buf.WriteByte(']')
	case map[string]any:
		buf.WriteByte('{')
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for i, k := range keys {
			if i > 0 {
				buf.WriteString(", ")
			}
			writePythonJSONString(buf, k)
			buf.WriteString(": ")
			writePythonJSON(buf, val[k])
		}
		buf.WriteByte('}')
	default:
		// 兜底：经 JSON 编解码为通用结构后再格式化。
		raw, err := json.Marshal(val)
		if err != nil {
			buf.WriteString("null")
			return
		}
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		var generic any
		if err := dec.Decode(&generic); err != nil {
			buf.WriteString("null")
			return
		}
		writePythonJSON(buf, generic)
	}
}

func writePythonJSONString(buf *bytes.Buffer, s string) {
	buf.WriteByte('"')
	for i := 0; i < len(s); {
		r, width := utf8.DecodeRuneInString(s[i:])
		i += width
		switch r {
		case '"':
			buf.WriteString(`\"`)
		case '\\':
			buf.WriteString(`\\`)
		case '\b':
			buf.WriteString(`\b`)
		case '\f':
			buf.WriteString(`\f`)
		case '\n':
			buf.WriteString(`\n`)
		case '\r':
			buf.WriteString(`\r`)
		case '\t':
			buf.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(buf, `\u%04x`, r)
			} else if r < 0x80 {
				buf.WriteByte(byte(r))
			} else if r <= 0xffff {
				fmt.Fprintf(buf, `\u%04x`, r)
			} else {
				r1, r2 := utf16.EncodeRune(r)
				fmt.Fprintf(buf, `\u%04x\u%04x`, r1, r2)
			}
		}
	}
	buf.WriteByte('"')
}
