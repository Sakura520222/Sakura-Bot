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
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

const (
	DefaultSummaryDay         = "mon"
	DefaultSummaryHour        = 9
	DefaultSummaryMinute      = 0
	DefaultPollRegenThreshold = 5
)

var validWeekDays = map[string]bool{
	"mon": true,
	"tue": true,
	"wed": true,
	"thu": true,
	"fri": true,
	"sat": true,
	"sun": true,
}

// ────────────────────────────── 定时总结调度 ──────────────────────────────

// SummarySchedule 表示单个频道的自动总结调度配置（对齐 core/config.py:normalize_schedule_config）。
type SummarySchedule struct {
	Frequency string   `json:"frequency"`
	Hour      int      `json:"hour"`
	Minute    int      `json:"minute"`
	Days      []string `json:"days,omitempty"`
}

// DefaultSummarySchedule 返回未单独配置频道时的默认调度（每周一 09:00）。
func DefaultSummarySchedule() SummarySchedule {
	return SummarySchedule{
		Frequency: "weekly",
		Hour:      DefaultSummaryHour,
		Minute:    DefaultSummaryMinute,
		Days:      []string{DefaultSummaryDay},
	}
}

// Validate 校验调度配置合法性（对齐 core/config.py:validate_schedule_v2）。
func (s SummarySchedule) Validate() error {
	switch s.Frequency {
	case "daily":
	case "weekly":
		if len(s.Days) == 0 {
			return fmt.Errorf("weekly 模式必须提供非空 days 列表")
		}
		for _, d := range s.Days {
			if !validWeekDays[strings.ToLower(strings.TrimSpace(d))] {
				return fmt.Errorf("无效的星期几: %q", d)
			}
		}
	default:
		return fmt.Errorf("无效的调度频率: %q", s.Frequency)
	}
	if s.Hour < 0 || s.Hour > 23 {
		return fmt.Errorf("无效的小时: %d（有效范围 0..23）", s.Hour)
	}
	if s.Minute < 0 || s.Minute > 59 {
		return fmt.Errorf("无效的分钟: %d（有效范围 0..59）", s.Minute)
	}
	return nil
}

// SummarySchedules 读取并标准化 config.json["summary_schedules"]（兼容旧版单 day 字段）。
func (c *Config) SummarySchedules() map[string]SummarySchedule {
	rawMap, ok := c.readConfig()["summary_schedules"].(map[string]any)
	if !ok || len(rawMap) == 0 {
		return map[string]SummarySchedule{}
	}
	out := make(map[string]SummarySchedule, len(rawMap))
	for ch, rawItem := range rawMap {
		item, ok := rawItem.(map[string]any)
		if !ok {
			continue
		}
		out[ch] = normalizeScheduleMap(item)
	}
	return out
}

// ChannelSchedule 获取指定频道的生效调度（支持等价频道键匹配，未配置时回退每周一 09:00）。
func (c *Config) ChannelSchedule(channel string) SummarySchedule {
	schedules := c.SummarySchedules()
	if key, ok := FindEquivalentChannelKey(schedules, channel); ok {
		return schedules[key]
	}
	return DefaultSummarySchedule()
}

func normalizeScheduleMap(item map[string]any) SummarySchedule {
	hour := DefaultSummaryHour
	if h, ok := parseLooseInt(item["hour"]); ok && h >= 0 && h <= 23 {
		hour = h
	}
	minute := DefaultSummaryMinute
	if m, ok := parseLooseInt(item["minute"]); ok && m >= 0 && m <= 59 {
		minute = m
	}

	freq, hasFreq := item["frequency"].(string)
	freq = strings.ToLower(strings.TrimSpace(freq))
	if hasFreq && (freq == "daily" || freq == "weekly") {
		if freq == "daily" {
			return SummarySchedule{
				Frequency: "daily",
				Hour:      hour,
				Minute:    minute,
				Days:      []string{},
			}
		}
		days := parseStringList(item["days"])
		if len(days) == 0 {
			if legacyDay, ok := item["day"].(string); ok && strings.TrimSpace(legacyDay) != "" {
				days = []string{strings.ToLower(strings.TrimSpace(legacyDay))}
			} else {
				days = []string{DefaultSummaryDay}
			}
		}
		return SummarySchedule{
			Frequency: "weekly",
			Hour:      hour,
			Minute:    minute,
			Days:      days,
		}
	}

	if legacyDay, ok := item["day"].(string); ok && strings.TrimSpace(legacyDay) != "" {
		return SummarySchedule{
			Frequency: "weekly",
			Hour:      hour,
			Minute:    minute,
			Days:      []string{strings.ToLower(strings.TrimSpace(legacyDay))},
		}
	}

	return SummarySchedule{
		Frequency: "weekly",
		Hour:      hour,
		Minute:    minute,
		Days:      []string{DefaultSummaryDay},
	}
}

// ────────────────────────────── 投票与报告配置 ──────────────────────────────

// ChannelPollSetting 表示单个频道的投票覆盖配置（nil 字段表示继承全局设置）。
type ChannelPollSetting struct {
	Enabled       *bool `json:"enabled,omitempty"`
	SendToChannel *bool `json:"send_to_channel,omitempty"`
	PublicVoters  *bool `json:"public_voters,omitempty"`
}

// PollSettings 返回 config.json["channel_poll_settings"] 的全部频道投票设置。
func (c *Config) PollSettings() map[string]ChannelPollSetting {
	rawMap, ok := c.readConfig()["channel_poll_settings"].(map[string]any)
	if !ok || len(rawMap) == 0 {
		return map[string]ChannelPollSetting{}
	}
	out := make(map[string]ChannelPollSetting, len(rawMap))
	for ch, rawItem := range rawMap {
		item, ok := rawItem.(map[string]any)
		if !ok {
			continue
		}
		var s ChannelPollSetting
		if v, exists := item["enabled"]; exists && v != nil {
			if b, ok := parseLooseBool(v); ok {
				s.Enabled = &b
			}
		}
		if v, exists := item["send_to_channel"]; exists && v != nil {
			if b, ok := parseLooseBool(v); ok {
				s.SendToChannel = &b
			}
		}
		if v, exists := item["public_voters"]; exists && v != nil {
			if b, ok := parseLooseBool(v); ok {
				s.PublicVoters = &b
			}
		}
		out[ch] = s
	}
	return out
}

// ChannelPollConfig 获取单个频道的投票覆盖配置（等价频道键匹配）。
func (c *Config) ChannelPollConfig(channel string) ChannelPollSetting {
	all := c.PollSettings()
	if key, ok := FindEquivalentChannelKey(all, channel); ok {
		return all[key]
	}
	return ChannelPollSetting{}
}

// SendReportToSource 返回是否将总结报告发回源频道（config.json["send_report_to_source"]，默认 true）。
func (c *Config) SendReportToSource() bool {
	if raw, ok := c.readConfig()["send_report_to_source"]; ok && raw != nil {
		if b, ok := parseLooseBool(raw); ok {
			return b
		}
	}
	return true
}

// EnablePoll 返回全局投票开关（config.json["enable_poll"] → env ENABLE_POLL → true）。
func (c *Config) EnablePoll() bool {
	if raw, ok := c.readConfig()["enable_poll"]; ok && raw != nil {
		if b, ok := parseLooseBool(raw); ok {
			return b
		}
	}
	return c.Env.EnablePollEnv()
}

// PollRegenThreshold 返回投票重新生成阈值（config.json["poll_regen_threshold"] → env POLL_REGEN_THRESHOLD → 5）。
func (c *Config) PollRegenThreshold() int {
	if raw, ok := c.readConfig()["poll_regen_threshold"]; ok && raw != nil {
		if n, ok := parseLooseInt(raw); ok && n >= 1 {
			return n
		}
	}
	if n := int(c.Env.GetIntDef(KeyPollRegenThreshold, DefaultPollRegenThreshold)); n >= 1 {
		return n
	}
	return DefaultPollRegenThreshold
}

// EnableVoteRegenRequest 返回是否允许用户发起投票重生成请求（config.json → env → true）。
func (c *Config) EnableVoteRegenRequest() bool {
	if raw, ok := c.readConfig()["enable_vote_regen_request"]; ok && raw != nil {
		if b, ok := parseLooseBool(raw); ok {
			return b
		}
	}
	return c.Env.GetBoolDef(KeyEnableVoteRegenRequest, true)
}

// PollPublicVoters 返回投票是否公开投票人（config.json["public_voters"] → env POLL_PUBLIC_VOTERS → true）。
func (c *Config) PollPublicVoters() bool {
	if raw, ok := c.readConfig()["public_voters"]; ok && raw != nil {
		if b, ok := parseLooseBool(raw); ok {
			return b
		}
	}
	return c.Env.PollPublicVotersEnv()
}

// ────────────────────────────── 自动趣味投票 ──────────────────────────────

// EnableAutoPoll 返回全局自动趣味投票主开关（config.json["enable_auto_poll"]，默认 false）。
func (c *Config) EnableAutoPoll() bool {
	if raw, ok := c.readConfig()["enable_auto_poll"]; ok && raw != nil {
		if b, ok := parseLooseBool(raw); ok {
			return b
		}
	}
	return false
}

// ChannelAutoPollSettings 返回显式配置了 enabled 的频道级自动趣味投票映射。
func (c *Config) ChannelAutoPollSettings() map[string]bool {
	rawMap, ok := c.readConfig()["channel_auto_poll_settings"].(map[string]any)
	if !ok || len(rawMap) == 0 {
		return map[string]bool{}
	}
	out := make(map[string]bool, len(rawMap))
	for ch, rawItem := range rawMap {
		switch v := rawItem.(type) {
		case map[string]any:
			if en, exists := v["enabled"]; exists && en != nil {
				if b, ok := parseLooseBool(en); ok {
					out[ch] = b
				}
			}
		default:
			if b, ok := parseLooseBool(v); ok {
				out[ch] = b
			}
		}
	}
	return out
}

// IsAutoPollEnabledForChannel 判断指定频道的自动趣味投票最终是否启用
// （对齐 core/config.py:is_auto_poll_enabled_for_channel：全局主开关关闭时恒 false；
// 全局开启时无独立配置或 enabled=null 默认继承 true，否则按频道配置值生效）。
func (c *Config) IsAutoPollEnabledForChannel(channel string) bool {
	if !c.EnableAutoPoll() {
		return false
	}
	rawMap, ok := c.readConfig()["channel_auto_poll_settings"].(map[string]any)
	if !ok || len(rawMap) == 0 {
		return true
	}
	matchedKey, found := FindEquivalentChannelKey(rawMap, channel)
	if !found {
		return true
	}
	switch item := rawMap[matchedKey].(type) {
	case map[string]any:
		rawEnabled, exists := item["enabled"]
		if !exists || rawEnabled == nil {
			return true
		}
		if b, ok := parseLooseBool(rawEnabled); ok {
			return b
		}
		return true
	default:
		if b, ok := parseLooseBool(item); ok {
			return b
		}
		return true
	}
}

// ────────────────────────────── 评论区欢迎配置 ──────────────────────────────

// CommentWelcomeConfig 完整的评论区欢迎生效配置（对齐 channel_comment_welcome_config.py）。
type CommentWelcomeConfig struct {
	Enabled        bool   `json:"enabled"`
	WelcomeMessage string `json:"welcome_message"`
	ButtonText     string `json:"button_text"`
	ButtonAction   string `json:"button_action"`
}

// CommentWelcomeOverride 频道级评论区欢迎局部覆盖项（仅非 nil 字段覆盖默认配置）。
type CommentWelcomeOverride struct {
	Enabled        *bool   `json:"enabled,omitempty"`
	WelcomeMessage *string `json:"welcome_message,omitempty"`
	ButtonText     *string `json:"button_text,omitempty"`
	ButtonAction   *string `json:"button_action,omitempty"`
}

// CommentWelcomeSettings 聚合默认评论区欢迎配置与频道覆盖表。
type CommentWelcomeSettings struct {
	Default          CommentWelcomeConfig              `json:"default"`
	ChannelOverrides map[string]CommentWelcomeOverride `json:"channel_overrides"`
}

// DefaultCommentWelcomeConfig 返回内置默认评论区欢迎配置。
func DefaultCommentWelcomeConfig() CommentWelcomeConfig {
	return CommentWelcomeConfig{
		Enabled:        true,
		WelcomeMessage: "comment_welcome.message",
		ButtonText:     "comment_welcome.button",
		ButtonAction:   "request_summary",
	}
}

// GetForChannel 按“频道覆盖 → 用户默认 → 代码默认”三级优先级合并指定频道配置。
func (s CommentWelcomeSettings) GetForChannel(channel string) CommentWelcomeConfig {
	res := s.Default
	if res.WelcomeMessage == "" && res.ButtonText == "" && res.ButtonAction == "" {
		def := DefaultCommentWelcomeConfig()
		if res.WelcomeMessage == "" {
			res.WelcomeMessage = def.WelcomeMessage
		}
		if res.ButtonText == "" {
			res.ButtonText = def.ButtonText
		}
		if res.ButtonAction == "" {
			res.ButtonAction = def.ButtonAction
		}
	}
	if key, ok := FindEquivalentChannelKey(s.ChannelOverrides, channel); ok {
		ov := s.ChannelOverrides[key]
		if ov.Enabled != nil {
			res.Enabled = *ov.Enabled
		}
		if ov.WelcomeMessage != nil {
			res.WelcomeMessage = *ov.WelcomeMessage
		}
		if ov.ButtonText != nil {
			res.ButtonText = *ov.ButtonText
		}
		if ov.ButtonAction != nil {
			res.ButtonAction = *ov.ButtonAction
		}
	}
	return res
}

// CommentWelcomeSettings 读取完整的评论区欢迎设置（comment_welcome.default + channel_comment_welcome）。
func (c *Config) CommentWelcomeSettings() CommentWelcomeSettings {
	cfg := c.readConfig()
	base := DefaultCommentWelcomeConfig()
	if cw, ok := cfg["comment_welcome"].(map[string]any); ok {
		if defMap, ok := cw["default"].(map[string]any); ok {
			if raw, exists := defMap["enabled"]; exists && raw != nil {
				if b, ok := parseLooseBool(raw); ok {
					base.Enabled = b
				}
			}
			if s, ok := defMap["welcome_message"].(string); ok {
				base.WelcomeMessage = s
			}
			if s, ok := defMap["button_text"].(string); ok {
				base.ButtonText = s
			}
			if s, ok := defMap["button_action"].(string); ok {
				base.ButtonAction = s
			}
		}
	}

	overrides := make(map[string]CommentWelcomeOverride)
	if chMap, ok := cfg["channel_comment_welcome"].(map[string]any); ok {
		for ch, rawItem := range chMap {
			item, ok := rawItem.(map[string]any)
			if !ok {
				continue
			}
			var ov CommentWelcomeOverride
			if raw, exists := item["enabled"]; exists && raw != nil {
				if b, ok := parseLooseBool(raw); ok {
					ov.Enabled = &b
				}
			}
			if s, ok := item["welcome_message"].(string); ok {
				cp := s
				ov.WelcomeMessage = &cp
			}
			if s, ok := item["button_text"].(string); ok {
				cp := s
				ov.ButtonText = &cp
			}
			if s, ok := item["button_action"].(string); ok {
				cp := s
				ov.ButtonAction = &cp
			}
			overrides[ch] = ov
		}
	}

	return CommentWelcomeSettings{
		Default:          base,
		ChannelOverrides: overrides,
	}
}

// ────────────────────────────── 频道消息转发 ──────────────────────────────

// ForwardingRule 表示单条频道转发规则（对齐 core/web_api/schemas/forwarding.py:ForwardingRuleBase）。
type ForwardingRule struct {
	SourceChannel       string   `json:"source_channel"`
	TargetChannel       string   `json:"target_channel"`
	Keywords            []string `json:"keywords"`
	Blacklist           []string `json:"blacklist"`
	Patterns            []string `json:"patterns"`
	BlacklistPatterns   []string `json:"blacklist_patterns"`
	CopyMode            bool     `json:"copy_mode"`
	ForwardOriginalOnly bool     `json:"forward_original_only"`
	CustomFooter        string   `json:"custom_footer,omitempty"`
}

// ForwardingConfig 表示频道消息转发总配置（对齐 routes/forwarding.py）。
type ForwardingConfig struct {
	Enabled           bool             `json:"enabled"`
	ShowDefaultFooter bool             `json:"show_default_footer"`
	Rules             []ForwardingRule `json:"rules"`
}

// ForwardingConfig 读取 config.json["forwarding"]（兼容旧版顶层 forwarding_rules 回退）。
func (c *Config) ForwardingConfig() ForwardingConfig {
	cfg := c.readConfig()
	res := ForwardingConfig{
		Enabled:           false,
		ShowDefaultFooter: true,
		Rules:             []ForwardingRule{},
	}

	fwdMap, ok := cfg["forwarding"].(map[string]any)
	var rawRules []any
	if ok {
		if v, exists := fwdMap["enabled"]; exists && v != nil {
			if b, ok := parseLooseBool(v); ok {
				res.Enabled = b
			}
		}
		if v, exists := fwdMap["show_default_footer"]; exists && v != nil {
			if b, ok := parseLooseBool(v); ok {
				res.ShowDefaultFooter = b
			}
		}
		if r, exists := fwdMap["rules"]; exists {
			rawRules, _ = r.([]any)
		} else if legacy, ok := cfg["forwarding_rules"].([]any); ok {
			rawRules = legacy
		}
	} else if legacy, ok := cfg["forwarding_rules"].([]any); ok {
		rawRules = legacy
	}

	if len(rawRules) > 0 {
		rules := make([]ForwardingRule, 0, len(rawRules))
		for _, item := range rawRules {
			rm, ok := item.(map[string]any)
			if !ok {
				continue
			}
			rule := ForwardingRule{
				SourceChannel:     stringField(rm, "source_channel"),
				TargetChannel:     stringField(rm, "target_channel"),
				Keywords:          parseStringListNonNil(rm["keywords"]),
				Blacklist:         parseStringListNonNil(rm["blacklist"]),
				Patterns:          parseStringListNonNil(rm["patterns"]),
				BlacklistPatterns: parseStringListNonNil(rm["blacklist_patterns"]),
				CustomFooter:      stringField(rm, "custom_footer"),
			}
			if b, ok := parseLooseBool(rm["copy_mode"]); ok {
				rule.CopyMode = b
			}
			if b, ok := parseLooseBool(rm["forward_original_only"]); ok {
				rule.ForwardOriginalOnly = b
			}
			rules = append(rules, rule)
		}
		res.Rules = rules
	}
	return res
}

// ────────────────────────────── QA Bot Runtime & 原始子文档访问 ──────────────────────────────

// QABotRuntime 返回生效的 QA Bot 运行时（config.json["qa_bot_runtime"] → env QA_BOT_RUNTIME → "go"，
// 对齐 core/system/process_manager.py:effective_qa_runtime）。
func (c *Config) QABotRuntime() string {
	if raw, ok := c.readConfig()["qa_bot_runtime"]; ok && raw != nil {
		v := strings.ToLower(strings.TrimSpace(fmt.Sprintf("%v", raw)))
		if v == "go" || v == "python" {
			return v
		}
	}
	if envVal, ok := c.Env.Lookup(KeyQABotRuntime); ok {
		v := strings.ToLower(strings.TrimSpace(envVal))
		if v == "go" || v == "python" {
			return v
		}
	}
	return "go"
}

// RawSnapshot 返回当前生效 config.json 的完整深拷贝快照。
func (c *Config) RawSnapshot() map[string]any {
	return deepCopyMap(c.readConfig())
}

// SubSection 返回指定顶层键（如 sakura_guard、wiki_login、wiki_research、ai_profiles、ai_providers）的原始 JSON。
func (c *Config) SubSection(key string) (json.RawMessage, bool) {
	cfg := c.readConfig()
	val, exists := cfg[key]
	if !exists {
		return nil, false
	}
	b, err := json.Marshal(val)
	if err != nil {
		return nil, false
	}
	return json.RawMessage(b), true
}

// ────────────────────────────── 内部解析辅助 ──────────────────────────────

// parseLooseBool 对齐 core/config.py:_parse_bool：支持 bool、字符串（"true"/"1"/"yes"）与数值。
func parseLooseBool(v any) (bool, bool) {
	switch val := v.(type) {
	case bool:
		return val, true
	case string:
		s := strings.ToLower(strings.TrimSpace(val))
		return s == "true" || s == "1" || s == "yes", true
	case json.Number:
		if i, err := val.Int64(); err == nil {
			return i != 0, true
		}
		if f, err := val.Float64(); err == nil {
			return f != 0, true
		}
		return false, false
	case int:
		return val != 0, true
	case int64:
		return val != 0, true
	case float64:
		return val != 0, true
	default:
		return false, false
	}
}

func parseLooseInt(v any) (int, bool) {
	switch val := v.(type) {
	case json.Number:
		i, err := val.Int64()
		if err != nil {
			return 0, false
		}
		return int(i), true
	case int:
		return val, true
	case int64:
		return int(val), true
	case float64:
		if math.Trunc(val) != val || math.IsNaN(val) || math.IsInf(val, 0) {
			return 0, false
		}
		return int(val), true
	case string:
		i, err := strconv.Atoi(strings.TrimSpace(val))
		if err != nil {
			return 0, false
		}
		return i, true
	default:
		return 0, false
	}
}

func parseStringList(v any) []string {
	switch arr := v.(type) {
	case []string:
		out := make([]string, 0, len(arr))
		for _, s := range arr {
			if t := strings.TrimSpace(s); t != "" {
				out = append(out, t)
			}
		}
		return out
	case []any:
		out := make([]string, 0, len(arr))
		for _, item := range arr {
			if s, ok := item.(string); ok {
				if t := strings.TrimSpace(s); t != "" {
					out = append(out, t)
				}
			}
		}
		return out
	default:
		return nil
	}
}

func parseStringListNonNil(v any) []string {
	out := parseStringList(v)
	if out == nil {
		return []string{}
	}
	return out
}
