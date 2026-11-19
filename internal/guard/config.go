// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package guard

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// GuardError 表示 Sakura Guard 业务层校验或状态冲突错误（对应 i18n 键或明确可读消息）。
type GuardError string

func (e GuardError) Error() string { return string(e) }

// ChatCleanupError 表示群聊退群清理、历史记录清理或销号清理业务前置条件不满足（对应 HTTP 409）。
type ChatCleanupError string

func (e ChatCleanupError) Error() string { return string(e) }

var ruleIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

var (
	validActions       = map[string]bool{"delete": true, "warn": true, "mute": true, "kick": true, "ban": true}
	validMemberActions = map[string]bool{"warn": true, "mute": true, "kick": true, "ban": true, "allow": true}
	validNickActions   = map[string]bool{"warn": true, "mute": true, "kick": true, "ban": true}
	validAISources     = map[string]bool{
		"admin_report": true, "member_report": true, "review_outcome": true,
		"ai_review": true, "rule_auto_ban": true, "admin_blacklist": true,
	}
	validAITargets = map[string]bool{
		"nickname": true, "username": true, "message": true, "caption": true, "link": true,
	}
	validNoticeEvents = map[string]bool{
		"moderation": true, "verification_start": true, "verification_end": true,
		"verification_fail": true, "admin_action": true, "join_request": true,
		"whitelist": true, "blacklist": true,
	}
	validAIReportCategories = map[string]bool{
		"rule_match": true, "spam": true, "scam": true, "malware": true, "harassment": true, "other": true,
	}
)

// DeletedUserCleanupSettings 已销号成员定期清理配置（对齐 config.py:DeletedUserCleanupSettings）。
type DeletedUserCleanupSettings struct {
	Enabled       bool `json:"enabled"`
	IntervalHours int  `json:"interval_hours"`
}

// DefaultDeletedUserCleanupSettings 返回默认销号清理配置。
func DefaultDeletedUserCleanupSettings() DeletedUserCleanupSettings {
	return DeletedUserCleanupSettings{Enabled: false, IntervalHours: 24}
}

// AILearningSettings AI 自适应风控学习配置（对齐 config.py:AILearningSettings）。
type AILearningSettings struct {
	Enabled                bool     `json:"enabled"`
	SharedRulesEnabled     bool     `json:"shared_rules_enabled"`
	Sources                []string `json:"sources"`
	Targets                []string `json:"targets"`
	Activation             string   `json:"activation"`
	MinPositiveSamples     int      `json:"min_positive_samples"`
	MinReporters           int      `json:"min_reporters"`
	MinTargetUsers         int      `json:"min_target_users"`
	MinConfidence          float64  `json:"min_confidence"`
	DefaultAction          string   `json:"default_action"`
	RuleExpiryDays         int      `json:"rule_expiry_days"`
	NegativePauseThreshold int      `json:"negative_pause_threshold"`
	AnalysisWindowDays     int      `json:"analysis_window_days"`
	HistorySampleLimit     int      `json:"history_sample_limit"`
	SampleRetentionDays    int      `json:"sample_retention_days"`
}

// DefaultAILearningSettings 返回默认 AI 学习配置。
func DefaultAILearningSettings() AILearningSettings {
	return AILearningSettings{
		Enabled:                false,
		SharedRulesEnabled:     false,
		Sources:                []string{"admin_report", "member_report", "review_outcome", "ai_review"},
		Targets:                []string{"nickname", "username", "message", "caption", "link"},
		Activation:             "manual",
		MinPositiveSamples:     3,
		MinReporters:           2,
		MinTargetUsers:         2,
		MinConfidence:          0.8,
		DefaultAction:          "delete",
		RuleExpiryDays:         30,
		NegativePauseThreshold: 2,
		AnalysisWindowDays:     14,
		HistorySampleLimit:     20,
		SampleRetentionDays:    90,
	}
}

// GuardRule 单条自定义群管规则（对齐 config.py:GuardRule）。
type GuardRule struct {
	ID     string `json:"id"`
	Target string `json:"target"` // "nickname" | "username" | "message" | "link"
	Kind   string `json:"kind"`   // "keyword" | "regex"
	Value  string `json:"value"`
	Action string `json:"action"` // "delete" | "warn" | "mute" | "kick" | "ban"
}

// GuardGroup 单群完整 Sakura Guard 配置（对齐 config.py:GuardGroup）。
type GuardGroup struct {
	Enabled                         bool           `json:"enabled"`
	KickButtonEnabled               bool           `json:"kick_button_enabled"`
	VerifyJoin                      bool           `json:"verify_join"`
	VerificationMode                string         `json:"verification_mode"`
	VerificationAssistantUsername   string         `json:"verification_assistant_username"`
	VerificationSteps               int            `json:"verification_steps"`
	VerificationQuestionTypes       []string       `json:"verification_question_types"`
	VerificationRiskThreshold       int            `json:"verification_risk_threshold"`
	VerificationRiskAction          string         `json:"verification_risk_action"`
	VerificationRiskExtraSteps      int            `json:"verification_risk_extra_steps"`
	VerificationRiskWeights         map[string]int `json:"verification_risk_weights"`
	VerificationTimeout             int            `json:"verification_timeout"`
	VerificationMaxAttempts         int            `json:"verification_max_attempts"`
	VerificationTimeoutAction       string         `json:"verification_timeout_action"`
	VerificationWrongAction         string         `json:"verification_wrong_action"`
	VerificationMessageMode         string         `json:"verification_message_mode"`
	VerificationAdminActions        []string       `json:"verification_admin_actions"`
	JoinRequests                    string         `json:"join_requests"`
	JoinRequestMatchAction          string         `json:"join_request_match_action"`
	ObservationMinutes              int            `json:"observation_minutes"`
	ObservationMessages             int            `json:"observation_messages"`
	ObservationAction               string         `json:"observation_action"`
	ObservationImageMode            string         `json:"observation_image_mode"`
	ObservationAIReview             bool           `json:"observation_ai_review"`
	ObservationAIConfidence         float64        `json:"observation_ai_confidence"`
	ObservationSharePatterns        bool           `json:"observation_share_patterns"`
	NicknameFilter                  bool           `json:"nickname_filter"`
	NicknameAction                  string         `json:"nickname_action"`
	BanAnyTraditional               bool           `json:"ban_any_traditional"`
	MessageFilter                   bool           `json:"message_filter"`
	MessageKeywords                 []string       `json:"message_keywords"`
	MessageAction                   string         `json:"message_action"`
	BlockBotMessages                bool           `json:"block_bot_messages"`
	BotMessageAction                string         `json:"bot_message_action"`
	BlockButtonMessages             bool           `json:"block_button_messages"`
	ButtonMessageAction             string         `json:"button_message_action"`
	BlockForwardedMessages          bool           `json:"block_forwarded_messages"`
	ForwardedMessageAction          string         `json:"forwarded_message_action"`
	BlockContactMessages            bool           `json:"block_contact_messages"`
	ContactMessageAction            string         `json:"contact_message_action"`
	MuteSeconds                     int            `json:"mute_seconds"`
	Rules                           []GuardRule    `json:"rules"`
	ShareGlobalBlacklist            bool           `json:"share_global_blacklist"`
	TrustedBotIDs                   []int64        `json:"trusted_bot_ids"`
	AIReportReviewEnabled           bool           `json:"ai_report_review_enabled"`
	AIReportReviewDelayMinutes      int            `json:"ai_report_review_delay_minutes"`
	AIReportReviewAction            string         `json:"ai_report_review_action"`
	AIReportReviewConfidence        float64        `json:"ai_report_review_confidence"`
	AIReportReviewCategories        []string       `json:"ai_report_review_categories"`
	AIReportReviewHistoryWindowDays int            `json:"ai_report_review_history_window_days"`
	AIReportReviewHistoryLimit      int            `json:"ai_report_review_history_limit"`
	AILearningEnabled               bool           `json:"ai_learning_enabled"`
	AllowAnonymousAdminReport       bool           `json:"allow_anonymous_admin_report"`
	ReviewerIDs                     []int64        `json:"reviewer_ids"`
	AllowGroupAdminReview           bool           `json:"allow_group_admin_review"`
	NotificationIDs                 []int64        `json:"notification_ids"`
	NotificationEnabled             bool           `json:"notification_enabled"`
	VerificationNoticeCleanup       bool           `json:"verification_notice_cleanup"`
	NotificationEvents              []string       `json:"notification_events"`
	ReviewChatID                    *int64         `json:"review_chat_id"`
}

// DefaultGuardGroup 返回新群默认配置（逐字段对齐 config.py:GuardGroup）。
func DefaultGuardGroup() GuardGroup {
	return GuardGroup{
		Enabled:                       false,
		KickButtonEnabled:             true,
		VerifyJoin:                    true,
		VerificationMode:              "legacy",
		VerificationAssistantUsername: "",
		VerificationSteps:             2,
		VerificationQuestionTypes:     []string{"word", "position"},
		VerificationRiskThreshold:     4,
		VerificationRiskAction:        "extra_steps",
		VerificationRiskExtraSteps:    1,
		VerificationRiskWeights: map[string]int{
			"no_username":       1,
			"no_photo":          1,
			"random_name":       2,
			"similar_names":     2,
			"prior_enforcement": 4,
		},
		VerificationTimeout:             120,
		VerificationMaxAttempts:         3,
		VerificationTimeoutAction:       "ban",
		VerificationWrongAction:         "ban",
		VerificationMessageMode:         "delete",
		VerificationAdminActions:        []string{"ban", "blacklist", "skip"},
		JoinRequests:                    "manual",
		JoinRequestMatchAction:          "reject",
		ObservationMinutes:              30,
		ObservationMessages:             5,
		ObservationAction:               "delete",
		ObservationImageMode:            "qr",
		ObservationAIReview:             false,
		ObservationAIConfidence:         0.97,
		ObservationSharePatterns:        false,
		NicknameFilter:                  true,
		NicknameAction:                  "ban",
		BanAnyTraditional:               false,
		MessageFilter:                   false,
		MessageKeywords:                 []string{},
		MessageAction:                   "delete",
		BlockBotMessages:                false,
		BotMessageAction:                "delete",
		BlockButtonMessages:             false,
		ButtonMessageAction:             "delete",
		BlockForwardedMessages:          false,
		ForwardedMessageAction:          "delete",
		BlockContactMessages:            false,
		ContactMessageAction:            "ban",
		MuteSeconds:                     600,
		Rules:                           []GuardRule{},
		ShareGlobalBlacklist:            false,
		TrustedBotIDs:                   []int64{},
		AIReportReviewEnabled:           false,
		AIReportReviewDelayMinutes:      10,
		AIReportReviewAction:            "ban",
		AIReportReviewConfidence:        0.95,
		AIReportReviewCategories:        []string{"rule_match", "spam", "scam", "malware", "harassment"},
		AIReportReviewHistoryWindowDays: 14,
		AIReportReviewHistoryLimit:      10,
		AILearningEnabled:               false,
		AllowAnonymousAdminReport:       false,
		ReviewerIDs:                     []int64{},
		AllowGroupAdminReview:           false,
		NotificationIDs:                 []int64{},
		NotificationEnabled:             true,
		VerificationNoticeCleanup:       true,
		NotificationEvents: []string{
			"moderation", "verification_start", "verification_end", "verification_fail",
			"admin_action", "join_request", "whitelist", "blacklist",
		},
		ReviewChatID: nil,
	}
}

// GuardConfig 对应 config.json["sakura_guard"] 根节点（对齐 config.py:GuardConfig）。
type GuardConfig struct {
	NicknameKeywords   []string                   `json:"nickname_keywords"`
	DeletedUserCleanup DeletedUserCleanupSettings `json:"deleted_user_cleanup"`
	AILearning         AILearningSettings         `json:"ai_learning"`
	Groups             map[string]GuardGroup      `json:"groups"`
	RetentionDays      int                        `json:"retention_days"`
}

// DefaultGuardConfig 返回内置默认全局群管配置。
func DefaultGuardConfig() GuardConfig {
	return GuardConfig{
		NicknameKeywords:   []string{"主页", "看我", "头像", "小学生"},
		DeletedUserCleanup: DefaultDeletedUserCleanupSettings(),
		AILearning:         DefaultAILearningSettings(),
		Groups:             map[string]GuardGroup{},
		RetentionDays:      90,
	}
}

// ValidateWords 校验并去重关键词数组（每个词去首尾空白后 1..128 字符，总数不超过 maxItems）。
func ValidateWords(words []string, maxItems int) ([]string, error) {
	if maxItems <= 0 {
		maxItems = 50000
	}
	seen := make(map[string]bool, len(words))
	out := make([]string, 0, len(words))
	for _, w := range words {
		t := strings.TrimSpace(w)
		n := utf8.RuneCountInString(t)
		if n < 1 || n > 128 {
			return nil, fmt.Errorf("关键词长度需在 1..128 字符之间")
		}
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	if len(out) > maxItems {
		return nil, fmt.Errorf("关键词数量超过上限 %d", maxItems)
	}
	return out, nil
}

// Validate 严格校验 GuardConfig 所有范围与枚举约束。
func (c *GuardConfig) Validate() error {
	if c == nil {
		return errors.New("guard config is nil")
	}
	words, err := ValidateWords(c.NicknameKeywords, 50000)
	if err != nil {
		return err
	}
	c.NicknameKeywords = words

	if c.RetentionDays < 7 || c.RetentionDays > 3650 {
		return fmt.Errorf("retention_days 需在 7..3650 之间")
	}
	if err := c.DeletedUserCleanup.Validate(); err != nil {
		return err
	}
	if err := c.AILearning.Validate(); err != nil {
		return err
	}
	if len(c.Groups) > 500 {
		return fmt.Errorf("groups 数量不能超过 500")
	}
	if c.Groups == nil {
		c.Groups = map[string]GuardGroup{}
	}
	for k, grp := range c.Groups {
		chatID, perr := strconv.ParseInt(k, 10, 64)
		if perr != nil || chatID >= 0 || strconv.FormatInt(chatID, 10) != k {
			return fmt.Errorf("非法的群组键 %q：必须为规范负整数字符串", k)
		}
		if err := grp.Validate(); err != nil {
			return fmt.Errorf("groups[%s]: %w", k, err)
		}
		c.Groups[k] = grp
	}
	return nil
}

// Validate 校验销号清理设置。
func (s *DeletedUserCleanupSettings) Validate() error {
	if s.IntervalHours < 1 || s.IntervalHours > 720 {
		return fmt.Errorf("deleted_user_cleanup.interval_hours 需在 1..720 之间")
	}
	return nil
}

// Validate 校验 AI 学习设置。
func (s *AILearningSettings) Validate() error {
	if s.Activation != "manual" && s.Activation != "auto" {
		return fmt.Errorf("ai_learning.activation 必须是 manual 或 auto")
	}
	if !validActions[s.DefaultAction] {
		return fmt.Errorf("ai_learning.default_action 非法: %q", s.DefaultAction)
	}
	if s.MinPositiveSamples < 1 || s.MinPositiveSamples > 50 ||
		s.MinReporters < 1 || s.MinReporters > 50 ||
		s.MinTargetUsers < 1 || s.MinTargetUsers > 50 {
		return fmt.Errorf("ai_learning 样本阈值需在 1..50 之间")
	}
	if math.IsNaN(s.MinConfidence) || s.MinConfidence < 0.5 || s.MinConfidence > 1.0 {
		return fmt.Errorf("ai_learning.min_confidence 需在 0.5..1.0 之间")
	}
	if s.RuleExpiryDays < 1 || s.RuleExpiryDays > 365 {
		return fmt.Errorf("ai_learning.rule_expiry_days 需在 1..365 之间")
	}
	if s.NegativePauseThreshold < 1 || s.NegativePauseThreshold > 10 {
		return fmt.Errorf("ai_learning.negative_pause_threshold 需在 1..10 之间")
	}
	if s.AnalysisWindowDays < 1 || s.AnalysisWindowDays > 90 {
		return fmt.Errorf("ai_learning.analysis_window_days 需在 1..90 之间")
	}
	if s.HistorySampleLimit < 1 || s.HistorySampleLimit > 100 {
		return fmt.Errorf("ai_learning.history_sample_limit 需在 1..100 之间")
	}
	if s.SampleRetentionDays < 7 || s.SampleRetentionDays > 3650 {
		return fmt.Errorf("ai_learning.sample_retention_days 需在 7..3650 之间")
	}
	for _, src := range s.Sources {
		if !validAISources[src] {
			return fmt.Errorf("ai_learning.sources 包含非法项: %q", src)
		}
	}
	for _, tgt := range s.Targets {
		if !validAITargets[tgt] {
			return fmt.Errorf("ai_learning.targets 包含非法项: %q", tgt)
		}
	}
	return nil
}

// Validate 校验单群配置。
func (g *GuardGroup) Validate() error {
	if g.VerificationMode != "legacy" && g.VerificationMode != "assistant" {
		return fmt.Errorf("verification_mode 非法: %q", g.VerificationMode)
	}
	if g.VerificationSteps < 2 || g.VerificationSteps > 5 {
		return fmt.Errorf("verification_steps 需在 2..5 之间")
	}
	if len(g.VerificationQuestionTypes) == 0 {
		return fmt.Errorf("verification_question_types 不能为空")
	}
	for _, qt := range g.VerificationQuestionTypes {
		if qt != "word" && qt != "position" {
			return fmt.Errorf("verification_question_types 非法项: %q", qt)
		}
	}
	if g.VerificationRiskThreshold < 1 || g.VerificationRiskThreshold > 30 {
		return fmt.Errorf("verification_risk_threshold 需在 1..30 之间")
	}
	if g.VerificationRiskAction != "extra_steps" && g.VerificationRiskAction != "hold" {
		return fmt.Errorf("verification_risk_action 非法")
	}
	if g.VerificationRiskExtraSteps < 1 || g.VerificationRiskExtraSteps > 3 {
		return fmt.Errorf("verification_risk_extra_steps 需在 1..3 之间")
	}
	if g.VerificationTimeout < 30 || g.VerificationTimeout > 600 {
		return fmt.Errorf("verification_timeout 需在 30..600 之间")
	}
	if g.VerificationMaxAttempts < 1 || g.VerificationMaxAttempts > 10 {
		return fmt.Errorf("verification_max_attempts 需在 1..10 之间")
	}
	if !validMemberActions[g.VerificationTimeoutAction] || !validMemberActions[g.VerificationWrongAction] {
		return fmt.Errorf("入群验证失败动作非法")
	}
	if g.VerificationMessageMode != "delete" && g.VerificationMessageMode != "result" {
		return fmt.Errorf("verification_message_mode 非法")
	}
	if g.JoinRequests != "manual" && g.JoinRequests != "approve" {
		return fmt.Errorf("join_requests 非法")
	}
	if g.JoinRequestMatchAction != "reject" && g.JoinRequestMatchAction != "manual" && g.JoinRequestMatchAction != "approve" {
		return fmt.Errorf("join_request_match_action 非法")
	}
	if g.ObservationMinutes < 0 || g.ObservationMinutes > 1440 {
		return fmt.Errorf("observation_minutes 需在 0..1440 之间")
	}
	if g.ObservationMessages < 1 || g.ObservationMessages > 50 {
		return fmt.Errorf("observation_messages 需在 1..50 之间")
	}
	if g.ObservationAction != "delete" && g.ObservationAction != "extend" && g.ObservationAction != "mute" {
		return fmt.Errorf("observation_action 非法")
	}
	if g.ObservationImageMode != "off" && g.ObservationImageMode != "qr" && g.ObservationImageMode != "ocr" {
		return fmt.Errorf("observation_image_mode 非法")
	}
	if math.IsNaN(g.ObservationAIConfidence) || g.ObservationAIConfidence < 0.5 || g.ObservationAIConfidence > 1.0 {
		return fmt.Errorf("observation_ai_confidence 需在 0.5..1.0 之间")
	}
	if !validNickActions[g.NicknameAction] {
		return fmt.Errorf("nickname_action 非法")
	}
	for _, act := range []string{g.MessageAction, g.BotMessageAction, g.ButtonMessageAction, g.ForwardedMessageAction, g.ContactMessageAction, g.AIReportReviewAction} {
		if !validActions[act] {
			return fmt.Errorf("惩罚动作非法: %q", act)
		}
	}
	kw, err := ValidateWords(g.MessageKeywords, 100)
	if err != nil {
		return err
	}
	g.MessageKeywords = kw
	if g.MuteSeconds < 30 || g.MuteSeconds > 2592000 {
		return fmt.Errorf("mute_seconds 需在 30..2592000 之间")
	}
	if len(g.Rules) > 100 {
		return fmt.Errorf("rules 数量不能超过 100")
	}
	seenIDs := make(map[string]bool, len(g.Rules))
	for _, r := range g.Rules {
		if !ruleIDPattern.MatchString(r.ID) || seenIDs[r.ID] {
			return fmt.Errorf("非法的或重复的规则 ID: %q", r.ID)
		}
		seenIDs[r.ID] = true
		if r.Target != "nickname" && r.Target != "username" && r.Target != "message" && r.Target != "link" {
			return fmt.Errorf("规则 target 非法: %q", r.Target)
		}
		if r.Kind != "keyword" && r.Kind != "regex" {
			return fmt.Errorf("规则 kind 非法: %q", r.Kind)
		}
		vLen := utf8.RuneCountInString(strings.TrimSpace(r.Value))
		if vLen < 1 || vLen > 256 {
			return fmt.Errorf("规则 value 长度需在 1..256 之间")
		}
		if !validActions[r.Action] {
			return fmt.Errorf("规则 action 非法: %q", r.Action)
		}
	}
	if g.AIReportReviewDelayMinutes < 1 || g.AIReportReviewDelayMinutes > 1440 {
		return fmt.Errorf("ai_report_review_delay_minutes 需在 1..1440 之间")
	}
	if math.IsNaN(g.AIReportReviewConfidence) || g.AIReportReviewConfidence < 0.5 || g.AIReportReviewConfidence > 1.0 {
		return fmt.Errorf("ai_report_review_confidence 需在 0.5..1.0 之间")
	}
	for _, cat := range g.AIReportReviewCategories {
		if !validAIReportCategories[cat] {
			return fmt.Errorf("ai_report_review_categories 非法项: %q", cat)
		}
	}
	if g.AIReportReviewHistoryWindowDays < 1 || g.AIReportReviewHistoryWindowDays > 90 ||
		g.AIReportReviewHistoryLimit < 1 || g.AIReportReviewHistoryLimit > 50 {
		return fmt.Errorf("ai_report_review 历史窗口配置超出范围")
	}
	for _, ev := range g.NotificationEvents {
		if !validNoticeEvents[ev] {
			return fmt.Errorf("notification_events 非法项: %q", ev)
		}
	}
	if g.ReviewChatID != nil && *g.ReviewChatID >= 0 {
		return fmt.Errorf("review_chat_id 必须为负整数")
	}
	return nil
}

// MigrateNicknameKeywords 将旧版 groups[*].nickname_keywords 提升合并至 sakura_guard.nickname_keywords（对齐 migrate_nickname_keywords）。
func MigrateNicknameKeywords(rootDoc map[string]any) bool {
	if rootDoc == nil {
		return false
	}
	sg, ok := rootDoc["sakura_guard"].(map[string]any)
	if !ok || sg == nil {
		return false
	}
	groups, ok := sg["groups"].(map[string]any)
	if !ok || len(groups) == 0 {
		return false
	}

	var lifted []string
	migrated := false
	for _, rawGrp := range groups {
		gm, ok := rawGrp.(map[string]any)
		if !ok {
			continue
		}
		rawWords, exists := gm["nickname_keywords"]
		if !exists {
			continue
		}
		delete(gm, "nickname_keywords")
		migrated = true
		if arr, ok := rawWords.([]any); ok {
			for _, w := range arr {
				if s, ok := w.(string); ok && strings.TrimSpace(s) != "" {
					lifted = append(lifted, strings.TrimSpace(s))
				}
			}
		}
	}
	if !migrated {
		return false
	}

	cur := DefaultGuardConfig().NicknameKeywords
	if existing, ok := sg["nickname_keywords"].([]any); ok {
		cur = make([]string, 0, len(existing))
		for _, item := range existing {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				cur = append(cur, strings.TrimSpace(s))
			}
		}
	}
	for _, w := range lifted {
		if !slices.Contains(cur, w) {
			cur = append(cur, w)
		}
	}
	sg["nickname_keywords"] = cur
	return true
}

// ParseGuardConfig 从完整 config.json map 解析并填充默认值的 GuardConfig（未知字段报错）。
func ParseGuardConfig(rootDoc map[string]any) (GuardConfig, error) {
	cfg := DefaultGuardConfig()
	if rootDoc == nil {
		return cfg, nil
	}
	rawSG, exists := rootDoc["sakura_guard"]
	if !exists || rawSG == nil {
		return cfg, nil
	}
	sgMap, ok := rawSG.(map[string]any)
	if !ok {
		return cfg, fmt.Errorf("sakura_guard 必须是对象")
	}

	// 为每个群先合并默认值，再做 DisallowUnknownFields 解码。
	cloned := deepCloneAnyMap(sgMap)
	if rawGroups, ok := cloned["groups"].(map[string]any); ok {
		mergedGroups := make(map[string]any, len(rawGroups))
		defGrpMap := StructToMap(DefaultGuardGroup())
		for k, v := range rawGroups {
			gm, ok := v.(map[string]any)
			if !ok {
				return cfg, fmt.Errorf("sakura_guard.groups[%s] 必须是对象", k)
			}
			delete(gm, "nickname_keywords")
			merged := deepCloneAnyMap(defGrpMap)
			for fk, fv := range gm {
				merged[fk] = fv
			}
			mergedGroups[k] = merged
		}
		cloned["groups"] = mergedGroups
	}
	if rawDel, ok := cloned["deleted_user_cleanup"].(map[string]any); ok {
		merged := StructToMap(DefaultDeletedUserCleanupSettings())
		for fk, fv := range rawDel {
			merged[fk] = fv
		}
		cloned["deleted_user_cleanup"] = merged
	}
	if rawLearn, ok := cloned["ai_learning"].(map[string]any); ok {
		merged := StructToMap(DefaultAILearningSettings())
		for fk, fv := range rawLearn {
			merged[fk] = fv
		}
		cloned["ai_learning"] = merged
	}

	encoded, err := json.Marshal(cloned)
	if err != nil {
		return cfg, err
	}
	dec := json.NewDecoder(bytes.NewReader(encoded))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return cfg, err
	}
	if err := cfg.Validate(); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// ParseGuardGroupWithPatch 将局部字段补丁合并到基准群配置并执行严格校验。
func ParseGuardGroupWithPatch(base GuardGroup, patch map[string]any) (GuardGroup, error) {
	merged := StructToMap(base)
	for k, v := range patch {
		merged[k] = v
	}
	encoded, err := json.Marshal(merged)
	if err != nil {
		return base, err
	}
	out := DefaultGuardGroup()
	dec := json.NewDecoder(bytes.NewReader(encoded))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&out); err != nil {
		return base, err
	}
	if err := out.Validate(); err != nil {
		return base, err
	}
	return out, nil
}

// StructToMap 将配置结构体转换为标准 JSON map（供 WebUI 输出与乐观锁比较）。
func StructToMap(v any) map[string]any {
	b, err := json.Marshal(v)
	if err != nil {
		return map[string]any{}
	}
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	if m == nil {
		return map[string]any{}
	}
	return m
}

func deepCloneAnyMap(in map[string]any) map[string]any {
	b, _ := json.Marshal(in)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	if out == nil {
		return map[string]any{}
	}
	return out
}
