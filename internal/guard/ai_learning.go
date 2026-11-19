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
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/Sakura520222/Sakura-Bot/internal/config"
	guarddb "github.com/Sakura520222/Sakura-Bot/internal/db/guard"
	"github.com/Sakura520222/Sakura-Bot/internal/llm"
	"github.com/Sakura520222/Sakura-Bot/internal/mtproto"
	"github.com/dlclark/regexp2"
)

var domainDenylist = map[string]bool{
	"t.me":             true,
	"telegram.me":      true,
	"telegram.org":     true,
	"telegram.dog":     true,
	"telegram.cv":      true,
	"telegra.ph":       true,
	"telescop.email":   true,
	"tx.me":            true,
	"telegram.gallery": true,
}

var unanchoredCapturingGroupRE = regexp.MustCompile(`(^|[^\\])\(([^?]|$)`)

// Completer 抽象聚合后的聊天补全接口。
type Completer interface {
	CompleteChat(ctx context.Context, req llm.CompleteRequest) (*llm.CompleteResponse, error)
}

// CandidateRule 表示 AI 规则抽取器返回的单条候选规则（对齐 ai_rule_extractor.py:CandidateRule）。
type CandidateRule struct {
	RuleType          string   `json:"rule_type"`  // "nickname"|"username"|"message"|"link"|"pattern"
	MatchKind         string   `json:"match_kind"` // "keyword"|"regex"|"domain"
	Value             string   `json:"value"`
	Confidence        float64  `json:"confidence"`
	Explanation       string   `json:"explanation"`
	EvidenceSampleIDs []string `json:"evidence_sample_ids"`
}

// ReportVerdict 表示延迟 AI 举报裁决模型输出（对齐 ai_report_classifier.py）。
type ReportVerdict struct {
	Verdict    string          `json:"verdict"` // "violation" | "benign" | "uncertain"
	Category   string          `json:"category"`
	Confidence float64         `json:"confidence"`
	Reason     string          `json:"reason"`
	Evidence   []QuoteEvidence `json:"evidence"`
}

// QuoteEvidence 表示绑定原文引用的裁决证据项。
type QuoteEvidence struct {
	Source string `json:"source"`
	Quote  string `json:"quote"`
}

// SafeRegex 校验 AI 生成的正则安全性：长度 <= 256，禁止 .* / .+ / (?R 以及任何捕获分组，且可正常编译（对齐 ai_rule_extractor.py:safe_regex）。
func SafeRegex(pattern string) bool {
	p := strings.TrimSpace(pattern)
	if p == "" || utf8.RuneCountInString(p) > 256 {
		return false
	}
	if strings.Contains(p, ".*") || strings.Contains(p, ".+") || strings.Contains(p, "(?R") {
		return false
	}
	if unanchoredCapturingGroupRE.MatchString(p) {
		return false
	}
	if reStd, err := regexp.Compile(p); err == nil {
		return reStd.NumSubexp() == 0
	}
	re2, err := regexp2.Compile(p, regexp2.IgnoreCase)
	if err != nil {
		return false
	}
	return len(re2.GetGroupNumbers()) <= 1
}

// SanitizeDomain 提取并规范化域名，拒绝 Telegram 官方域名及非法短域名（对齐 sanitize_candidates domain 分支）。
func SanitizeDomain(raw string) (string, bool) {
	s := strings.TrimSpace(strings.ToLower(raw))
	if s == "" {
		return "", false
	}
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil {
		return "", false
	}
	host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	host = strings.Trim(host, ".")
	if len(host) < 4 || !strings.Contains(host, ".") {
		return "", false
	}
	for blocked := range domainDenylist {
		if host == blocked || strings.HasSuffix(host, "."+blocked) {
			return "", false
		}
	}
	return host, true
}

// SanitizeCandidateRules 过滤并标准化模型输出的候选规则列表（最多保留 10 条，强制证据交集校验）。
func SanitizeCandidateRules(candidates []CandidateRule, validSampleIDs map[string]bool) []CandidateRule {
	var out []CandidateRule
	for _, c := range candidates {
		if len(out) >= 10 {
			break
		}
		switch c.RuleType {
		case "nickname", "username", "message", "link", "pattern":
		default:
			continue
		}
		if c.Confidence < 0 || c.Confidence > 1 {
			continue
		}

		var evIDs []string
		for _, sid := range c.EvidenceSampleIDs {
			if validSampleIDs[sid] && !slices.Contains(evIDs, sid) {
				evIDs = append(evIDs, sid)
			}
		}
		if len(evIDs) == 0 {
			continue
		}
		c.EvidenceSampleIDs = evIDs

		switch c.MatchKind {
		case "regex":
			if c.RuleType != "message" && c.RuleType != "pattern" && c.RuleType != "link" {
				continue
			}
			if !SafeRegex(c.Value) {
				continue
			}
			c.Value = strings.TrimSpace(c.Value)
		case "domain":
			dom, ok := SanitizeDomain(c.Value)
			if !ok {
				continue
			}
			c.Value = dom
		case "keyword":
			normVal := NormalizeText(c.Value)
			if utf8.RuneCountInString(normVal) < 2 {
				continue
			}
			c.Value = strings.TrimSpace(c.Value)
		default:
			continue
		}
		out = append(out, c)
	}
	return out
}

// IsActionableVerdict 强校验 AI 举报裁决是否满足置信度、类别与严格原文子串引用约束（对齐 ai_reports.py:actionable）。
func IsActionableVerdict(
	v ReportVerdict,
	threshold float64,
	allowedCategories []string,
	hasLocalRuleMatch bool,
	sources map[string]string,
) bool {
	if v.Verdict != "violation" || v.Confidence < threshold {
		return false
	}
	if !slices.Contains(allowedCategories, v.Category) {
		return false
	}
	if v.Category == "rule_match" && !hasLocalRuleMatch {
		return false
	}
	if len(v.Evidence) == 0 {
		return false
	}
	for _, item := range v.Evidence {
		q := strings.TrimSpace(item.Quote)
		srcText, exists := sources[item.Source]
		if q == "" || !exists || !strings.Contains(srcText, q) {
			return false
		}
	}
	return true
}

// AIRuleCache 维护进程内活跃 AI 动态规则快照（原子指针交换，消息热路径绝不访问数据库或 LLM）。
type AIRuleCache struct {
	snap atomic.Pointer[map[int64][]guarddb.ActiveAIRule]
}

// NewAIRuleCache 创建动态规则内存缓存。
func NewAIRuleCache() *AIRuleCache {
	c := &AIRuleCache{}
	empty := map[int64][]guarddb.ActiveAIRule{}
	c.snap.Store(&empty)
	return c
}

// Reload 从数据库加载活跃及跨群共享规则并原子替换快照。
func (c *AIRuleCache) Reload(ctx context.Context, repo *guarddb.Repository, sharedEnabled bool) error {
	if repo == nil || repo.DB == nil {
		return nil
	}
	rules, err := repo.ActiveAIRulesSnapshot(ctx)
	if err != nil {
		return err
	}
	byChat := make(map[int64][]guarddb.ActiveAIRule)
	for _, r := range rules {
		byChat[r.ChatID] = append(byChat[r.ChatID], r)
		if sharedEnabled {
			for _, sc := range r.SharedChats {
				if sc != r.ChatID {
					byChat[sc] = append(byChat[sc], r)
				}
			}
		}
	}
	c.snap.Store(&byChat)
	return nil
}

// CachedCount 返回当前缓存中的规则条目总数（供 WebUI /api/guard/ai-learning 展示）。
func (c *AIRuleCache) CachedCount() int {
	m := c.snap.Load()
	if m == nil {
		return 0
	}
	total := 0
	for _, list := range *m {
		total += len(list)
	}
	return total
}

// MatchMessage 在内存快照上匹配指定群组的活跃 AI 规则（单条正则上限 10ms）。
func (c *AIRuleCache) MatchMessage(chatID int64, user mtproto.ProfileSummary, msg *mtproto.IncomingMessage) *Match {
	m := c.snap.Load()
	if m == nil {
		return nil
	}
	rules := (*m)[chatID]
	if len(rules) == 0 {
		return nil
	}

	fullName := strings.TrimSpace(user.FirstName + " " + user.LastName)
	var links []string
	body := ""
	if msg != nil {
		body = msg.Text
		links = ExtractLinks(msg.Text, msg.Entities)
	}

	for _, r := range rules {
		switch r.RuleType {
		case "nickname":
			if hit := matchAIRuleValue(r, "nickname", fullName); hit != nil {
				return hit
			}
		case "username":
			if user.Username != "" {
				if hit := matchAIRuleValue(r, "username", user.Username); hit != nil {
					return hit
				}
			}
			for _, u := range user.ActiveUsernames {
				if hit := matchAIRuleValue(r, "username", u); hit != nil {
					return hit
				}
			}
		case "message":
			if hit := matchAIRuleValue(r, "message", body); hit != nil {
				return hit
			}
		case "link":
			for _, lk := range links {
				if hit := matchAIRuleValue(r, "link", lk); hit != nil {
					return hit
				}
			}
		case "pattern":
			if hit := matchAIRuleValue(r, "message", body); hit != nil {
				return hit
			}
			for _, lk := range links {
				if hit := matchAIRuleValue(r, "link", lk); hit != nil {
					return hit
				}
			}
		}
	}
	return nil
}

func matchAIRuleValue(rule guarddb.ActiveAIRule, target, original string) *Match {
	if strings.TrimSpace(original) == "" {
		return nil
	}
	switch rule.MatchKind {
	case "keyword":
		normH := NormalizeText(original)
		needle := rule.NormalizedValue
		if needle == "" {
			needle = NormalizeText(rule.Value)
		}
		if needle != "" && strings.Contains(normH, needle) {
			return &Match{
				Target:        target,
				RuleID:        "ai:" + rule.ID,
				Action:        rule.Action,
				Reason:        rule.Value,
				Snippet:       EvidenceWindow(original, 0),
				AIRuleID:      rule.ID,
				AIRuleVersion: rule.Version,
			}
		}
	case "domain":
		lower := strings.ToLower(original)
		if strings.Contains(lower, strings.ToLower(rule.Value)) {
			return &Match{
				Target:        target,
				RuleID:        "ai:" + rule.ID,
				Action:        rule.Action,
				Reason:        rule.Value,
				Snippet:       EvidenceWindow(original, 0),
				AIRuleID:      rule.ID,
				AIRuleVersion: rule.Version,
			}
		}
	case "regex":
		normH := NormalizeText(original)
		re2, err := regexp2.Compile(rule.Value, regexp2.IgnoreCase)
		if err != nil {
			return nil
		}
		re2.MatchTimeout = 10 * time.Millisecond
		if found, merr := re2.FindStringMatch(normH); merr == nil && found != nil {
			return &Match{
				Target:        target,
				RuleID:        "ai:" + rule.ID,
				Action:        rule.Action,
				Reason:        rule.Value,
				Snippet:       EvidenceWindow(original, found.Index),
				AIRuleID:      rule.ID,
				AIRuleVersion: rule.Version,
			}
		}
	}
	return nil
}

// AILearning 管理风控样本采集、异步规则抽取、自动激活、负反馈熔断及跨群共享（对齐 ai_learning.py）。
type AILearning struct {
	repo      *guarddb.Repository
	appCfg    *config.Config
	getCfg    func() GuardConfig
	completer Completer
	cache     *AIRuleCache
	wakeCh    chan struct{}
	logger    *slog.Logger

	mu      sync.Mutex
	running bool
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

// NewAILearning 创建 AI 自适应风控引擎。
func NewAILearning(
	repo *guarddb.Repository,
	appCfg *config.Config,
	getCfg func() GuardConfig,
	completer Completer,
	logger *slog.Logger,
) *AILearning {
	if logger == nil {
		logger = slog.Default()
	}
	return &AILearning{
		repo:      repo,
		appCfg:    appCfg,
		getCfg:    getCfg,
		completer: completer,
		cache:     NewAIRuleCache(),
		wakeCh:    make(chan struct{}, 1),
		logger:    logger,
	}
}

// Cache 返回内存规则缓存。
func (l *AILearning) Cache() *AIRuleCache { return l.cache }

// IsRunning 返回后台学习协程是否存活。
func (l *AILearning) IsRunning() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.running
}

// Start 启动后台样本抽取与缓存刷新协程。
func (l *AILearning) Start(ctx context.Context) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.running {
		return
	}
	runCtx, cancel := context.WithCancel(ctx)
	l.cancel = cancel
	l.running = true

	_ = l.cache.Reload(ctx, l.repo, l.getCfg().AILearning.SharedRulesEnabled)
	l.wg.Add(1)
	go func() {
		defer l.wg.Done()
		l.loop(runCtx)
	}()
}

// Stop 停止后台学习协程。
func (l *AILearning) Stop() {
	l.mu.Lock()
	if !l.running {
		l.mu.Unlock()
		return
	}
	l.running = false
	cancel := l.cancel
	l.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	l.wg.Wait()
}

// Wake 唤醒后台学习循环并触发缓存刷新。
func (l *AILearning) Wake() {
	select {
	case l.wakeCh <- struct{}{}:
	default:
	}
}

// Enabled 判断指定群是否开启了 AI 学习总开关与群开关。
func (l *AILearning) Enabled(chatID int64) bool {
	cfg := l.getCfg()
	if !cfg.AILearning.Enabled {
		return false
	}
	grp, ok := cfg.Groups[fmt.Sprintf("%d", chatID)]
	return ok && grp.Enabled && grp.AILearningEnabled
}

// SourceAllowed 判断指定信号来源是否在配置白名单中。
func (l *AILearning) SourceAllowed(source string) bool {
	return slices.Contains(l.getCfg().AILearning.Sources, source)
}

// RecordWhitelist 在管理员加白名单时翻转正向样本为负反馈，并在超过阈值时自动暂停误伤 AI 规则。
func (l *AILearning) RecordWhitelist(ctx context.Context, store *guarddb.Store, chatID, userID int64, actor string) {
	if store == nil {
		return
	}
	samples, _ := store.AISamplesForUserChat(ctx, chatID, userID, 50)
	negLabel := "negative"
	for _, sp := range samples {
		if rowStr(sp, "label") != "negative" {
			_ = store.SaveAISample(ctx, sp, guarddb.AISampleUpdate{Label: &negLabel})
		}
	}
	ruleIDs, _ := store.HitAIRuleIDs(ctx, chatID, userID)
	threshold := l.getCfg().AILearning.NegativePauseThreshold
	for _, rid := range ruleIDs {
		_ = store.AddAIRuleCorrection(ctx, rid)
		_ = store.RecordAIRuleFeedback(ctx, guarddb.AIRuleFeedbackInsert{
			RuleID:   &rid,
			ChatID:   chatID,
			UserID:   userID,
			Kind:     "whitelist",
			Polarity: "negative",
			Actor:    actor,
			Payload:  map[string]any{"reason": "whitelist"},
		})
		if rule, err := store.GetAIRuleLocked(ctx, rid); err == nil && rule != nil {
			if rowStr(rule, "status") == "active" &&
				(rowInt64(rule, "negatives") >= int64(threshold) || rowInt64(rule, "corrections") >= int64(threshold)) {
				errCode := "negative_feedback_threshold"
				_ = store.SetAIRuleStatus(ctx, rule, "disabled", guarddb.AIRuleStatusOptions{
					Actor: "system:feedback",
					Error: &errCode,
				})
			}
		}
	}
	l.Wake()
}

// TransitionRule 执行 WebUI 管理员对 AI 规则的激活/拒绝/暂停/恢复流转。
func (l *AILearning) TransitionRule(
	ctx context.Context,
	store *guarddb.Store,
	rule guarddb.Row,
	targetStatus string,
	actor string,
	activate bool,
	note map[string]any,
) error {
	expDays := l.getCfg().AILearning.RuleExpiryDays
	if err := store.SetAIRuleStatus(ctx, rule, targetStatus, guarddb.AIRuleStatusOptions{
		Actor:       actor,
		Activate:    activate,
		BumpVersion: true,
		ExpiresDays: expDays,
	}); err != nil {
		return err
	}
	rid := rowStr(rule, "id")
	_ = store.RecordAIRuleFeedback(ctx, guarddb.AIRuleFeedbackInsert{
		RuleID:   &rid,
		ChatID:   rowInt64(rule, "chat_id"),
		UserID:   0,
		Kind:     "admin_transition",
		Polarity: "neutral",
		Actor:    actor,
		Payload:  note,
	})
	return nil
}

// SetRuleAction 更新 AI 规则命中动作并递增版本。
func (l *AILearning) SetRuleAction(ctx context.Context, store *guarddb.Store, rule guarddb.Row, action, actor string) (guarddb.Row, error) {
	rid := rowStr(rule, "id")
	updated, err := store.SetAIRuleAction(ctx, rid, action)
	if err != nil {
		return nil, err
	}
	_ = store.RecordAIRuleFeedback(ctx, guarddb.AIRuleFeedbackInsert{
		RuleID:   &rid,
		ChatID:   rowInt64(rule, "chat_id"),
		UserID:   0,
		Kind:     "action_update",
		Polarity: "neutral",
		Actor:    actor,
		Payload:  map[string]any{"action": action},
	})
	return updated, nil
}

// SetRuleShares 更新 AI 规则跨群共享范围（要求各目标群已在配置中存在且不能包含原属群）。
func (l *AILearning) SetRuleShares(ctx context.Context, store *guarddb.Store, rule guarddb.Row, chatIDs []int64, actor string) ([]int64, error) {
	cfg := l.getCfg()
	if !cfg.AILearning.SharedRulesEnabled && len(chatIDs) > 0 {
		return nil, GuardError("guard.ai_learning_sharing_disabled")
	}
	originChat := rowInt64(rule, "chat_id")
	clean := make([]int64, 0, len(chatIDs))
	for _, cid := range chatIDs {
		if cid == originChat {
			continue
		}
		if _, ok := cfg.Groups[fmt.Sprintf("%d", cid)]; !ok {
			return nil, GuardError("guard.group_not_found")
		}
		if !slices.Contains(clean, cid) {
			clean = append(clean, cid)
		}
	}
	rid := rowStr(rule, "id")
	if err := store.SetAIRuleScopes(ctx, rid, clean, actor); err != nil {
		return nil, err
	}
	l.Wake()
	return clean, nil
}

// ReviewSample 处理 WebUI 管理员对样本的 confirm / correct / delete 操作。
func (l *AILearning) ReviewSample(ctx context.Context, sampleID, action, actor string) (bool, error) {
	if l.repo == nil || l.repo.DB == nil {
		return false, GuardError("guard.unavailable")
	}
	var sample guarddb.Row
	_ = l.repo.Reader(ctx, func(st *guarddb.Store) error {
		var err error
		sample, err = st.GetAISample(ctx, sampleID)
		return err
	})
	if sample == nil {
		return false, nil
	}
	userID := rowInt64(sample, "user_id")
	chatID := rowInt64(sample, "chat_id")
	err := l.repo.WithLock(ctx, userID, func(st *guarddb.Store) error {
		cur, err := st.GetAISample(ctx, sampleID)
		if err != nil || cur == nil {
			return err
		}
		switch action {
		case "confirm":
			pos := "positive"
			queued := "queued"
			if err := st.SaveAISample(ctx, cur, guarddb.AISampleUpdate{Label: &pos, Status: &queued, ClearErr: true}); err != nil {
				return err
			}
			return st.EnqueueAILearningJob(ctx, sampleID, chatID, userID)
		case "correct":
			neg := "negative"
			skipped := "skipped"
			if err := st.SaveAISample(ctx, cur, guarddb.AISampleUpdate{Label: &neg, Status: &skipped, ClearErr: true}); err != nil {
				return err
			}
			rules, _ := st.AIRulesForSample(ctx, sampleID)
			for _, r := range rules {
				_ = st.AddAIRuleCorrection(ctx, rowStr(r, "id"))
			}
			return nil
		case "delete":
			inv := "invalid"
			return st.SaveAISample(ctx, cur, guarddb.AISampleUpdate{Status: &inv})
		default:
			return GuardError("guard.invalid_callback")
		}
	})
	if err != nil {
		return false, err
	}
	l.Wake()
	return true, nil
}

// BackfillEnforcement 按游标回填指定群的历史处罚案件为 AI 学习样本（每页最多 50 条）。
func (l *AILearning) BackfillEnforcement(ctx context.Context, chatID int64, cursor string) (map[string]any, error) {
	if l.repo == nil || l.repo.DB == nil {
		return nil, GuardError("guard.unavailable")
	}
	window := l.getCfg().AILearning.AnalysisWindowDays
	since := time.Now().UTC().Add(-time.Duration(window) * 24 * time.Hour)
	cases, err := l.repo.EnforcementHistory(ctx, chatID, cursor, since)
	if err != nil {
		return nil, err
	}
	imported := 0
	nextCursor := ""
	for _, c := range cases {
		cid := rowStr(c, "id")
		nextCursor = cid
		act := rowStr(c, "action")
		st := rowStr(c, "status")
		src := "rule_auto_ban"
		if st == "blacklisted" {
			src = "admin_blacklist"
		} else if act != "ban" {
			continue
		}
		if !l.SourceAllowed(src) {
			continue
		}
		uid := rowInt64(c, "user_id")
		_ = l.repo.WithLock(ctx, uid, func(store *guarddb.Store) error {
			sid := newUUIDHex()
			_, created, cerr := store.CreateAISample(ctx, guarddb.AISampleInsert{
				ID:             sid,
				IdempotencyKey: sha256Hex("ai-case:" + cid),
				ChatID:         chatID,
				UserID:         uid,
				Source:         src,
				CaseID:         &cid,
				Label:          "positive",
				Status:         "queued",
				Payload:        rowPayload(c),
			})
			if cerr == nil && created {
				imported++
				_ = store.EnqueueAILearningJob(ctx, sid, chatID, uid)
			}
			return nil
		})
	}
	if len(cases) < 50 {
		nextCursor = ""
	}
	l.Wake()
	return map[string]any{
		"scanned":     len(cases),
		"imported":    imported,
		"next_cursor": nextCursor,
	}, nil
}

func (l *AILearning) loop(ctx context.Context) {
	pollTicker := time.NewTicker(5 * time.Second)
	cacheTicker := time.NewTicker(30 * time.Second)
	maintTicker := time.NewTicker(10 * time.Minute)
	defer pollTicker.Stop()
	defer cacheTicker.Stop()
	defer maintTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-l.wakeCh:
			_ = l.cache.Reload(ctx, l.repo, l.getCfg().AILearning.SharedRulesEnabled)
			l.processDueJobs(ctx)
		case <-cacheTicker.C:
			_ = l.cache.Reload(ctx, l.repo, l.getCfg().AILearning.SharedRulesEnabled)
		case <-maintTicker.C:
			if l.repo != nil && l.repo.DB != nil {
				now := time.Now().UTC()
				_, _ = l.repo.ExpireAIRules(ctx, now)
				retDays := l.getCfg().AILearning.SampleRetentionDays
				_, _ = l.repo.CleanupAISamples(ctx, retDays)
				_, _ = l.repo.CleanupAIRuleHits(ctx, retDays)
			}
		case <-pollTicker.C:
			l.processDueJobs(ctx)
		}
	}
}

func (l *AILearning) processDueJobs(ctx context.Context) {
	if l.repo == nil || l.repo.DB == nil {
		return
	}
	due, err := l.repo.DueAILearningJobs(ctx, 5)
	if err != nil || len(due) == 0 {
		return
	}
	for _, job := range due {
		if ctx.Err() != nil {
			return
		}
		l.runSingleLearningJob(ctx, job)
	}
	_ = l.cache.Reload(ctx, l.repo, l.getCfg().AILearning.SharedRulesEnabled)
}

func (l *AILearning) runSingleLearningJob(ctx context.Context, rawJob guarddb.Row) {
	sampleID := rowStr(rawJob, "sample_id")
	userID := rowInt64(rawJob, "user_id")
	chatID := rowInt64(rawJob, "chat_id")
	token := rowStr(rawJob, "token")
	if sampleID == "" || userID == 0 {
		return
	}
	if !l.Enabled(chatID) {
		_ = l.repo.WithLock(ctx, userID, func(st *guarddb.Store) error {
			errCode := "learning_disabled"
			_, _ = st.SaveAILearningJob(ctx, sampleID, token, "skipped", map[string]any{}, 0, &errCode)
			return nil
		})
		return
	}

	var claimed guarddb.Row
	var samplesCtx []guarddb.Row
	cfg := l.getCfg().AILearning
	_ = l.repo.WithLock(ctx, userID, func(st *guarddb.Store) error {
		var cerr error
		claimed, cerr = st.ClaimAILearningJob(ctx, sampleID, token, guarddb.AILearningLeaseDuration)
		if cerr != nil || claimed == nil {
			return cerr
		}
		curSample, _ := st.GetAISample(ctx, sampleID)
		if curSample != nil {
			samplesCtx = append(samplesCtx, curSample)
		}
		sim, _ := st.SimilarAISamples(ctx, chatID, time.Now().UTC(), cfg.AnalysisWindowDays, cfg.HistorySampleLimit)
		for _, s := range sim {
			if rowStr(s, "id") != sampleID {
				samplesCtx = append(samplesCtx, s)
			}
		}
		return nil
	})
	if claimed == nil || rowStr(claimed, "status") != "running" {
		return
	}
	newToken := rowStr(claimed, "token")

	// 锁外调用 LLM 抽取候选规则。
	comp, modelName := l.resolveCompleter()
	if comp == nil {
		return
	}
	validIDs := make(map[string]bool, len(samplesCtx))
	sampleSummaries := make([]map[string]any, 0, len(samplesCtx))
	for _, s := range samplesCtx {
		sid := rowStr(s, "id")
		validIDs[sid] = true
		sampleSummaries = append(sampleSummaries, map[string]any{
			"id":      sid,
			"label":   rowStr(s, "label"),
			"payload": rowPayload(s),
		})
	}
	promptBytes, _ := json.Marshal(map[string]any{
		"chat_id": chatID,
		"targets": cfg.Targets,
		"samples": sampleSummaries,
	})
	callCtx, cancel := context.WithTimeout(ctx, 170*time.Second)
	resp, llmErr := comp.CompleteChat(callCtx, llm.CompleteRequest{
		Model:          modelName,
		Temperature:    llm.Float64(0),
		ResponseFormat: &llm.ResponseFormat{Type: "json_object"},
		Messages: []llm.Message{
			{Role: "system", Content: llm.Str(`你是社群风控规则提取器。根据违规样本归纳高准度特征规则，输出严格 JSON: {"rules":[{"rule_type":"message|link|nickname|username|pattern","match_kind":"keyword|domain|regex","value":"...","confidence":0.9,"explanation":"...","evidence_sample_ids":["..."]}]}`)},
			{Role: "user", Content: llm.Str(string(promptBytes))},
		},
	})
	cancel()

	_ = l.repo.WithLock(ctx, userID, func(st *guarddb.Store) error {
		if llmErr != nil || resp == nil || resp.FinishReason != "stop" {
			_, _, _ = st.FailOrRetryAILearningJob(ctx, claimed, map[string]any{}, time.Minute, "extractor_failed")
			return nil
		}
		var parsed struct {
			Rules []CandidateRule `json:"rules"`
		}
		if err := json.Unmarshal([]byte(extractJSONObject(resp.Content)), &parsed); err != nil {
			_, _, _ = st.FailOrRetryAILearningJob(ctx, claimed, map[string]any{}, time.Minute, "invalid_json")
			return nil
		}
		sanitized := SanitizeCandidateRules(parsed.Rules, validIDs)
		for _, cand := range sanitized {
			normVal := cand.Value
			if cand.MatchKind == "keyword" {
				normVal = NormalizeText(cand.Value)
			}
			rKey := guarddb.RuleKey(chatID, cand.RuleType, cand.MatchKind, normVal)
			actorStr := "system:ai_learning"
			upserted, uerr := st.UpsertAIRule(ctx, guarddb.AIRuleUpsert{
				ID:              newUUIDHex(),
				RuleKey:         rKey,
				ChatID:          chatID,
				RuleType:        cand.RuleType,
				MatchKind:       cand.MatchKind,
				Value:           cand.Value,
				NormalizedValue: normVal,
				Action:          cfg.DefaultAction,
				Status:          "candidate",
				Confidence:      cand.Confidence,
				Model:           &modelName,
				Actor:           &actorStr,
				Explanation:     cand.Explanation,
				SampleIDs:       cand.EvidenceSampleIDs,
			})
			if uerr != nil || upserted == nil {
				continue
			}
			allIDs := parseAnyStringSlice(upserted["sample_ids"])
			linkedSamples, _ := st.AISamplesByIDs(ctx, allIDs)
			_ = st.RefreshAIRuleStats(ctx, rowStr(upserted, "id"), linkedSamples)
			refreshed, _ := st.GetAIRuleLocked(ctx, rowStr(upserted, "id"))
			if refreshed != nil && cfg.Activation == "auto" && rowStr(refreshed, "status") == "candidate" {
				if rowInt64(refreshed, "positives") >= int64(cfg.MinPositiveSamples) &&
					rowInt64(refreshed, "reporters") >= int64(cfg.MinReporters) &&
					rowInt64(refreshed, "target_users") >= int64(cfg.MinTargetUsers) &&
					cand.Confidence >= cfg.MinConfidence {
					_ = st.SetAIRuleStatus(ctx, refreshed, "active", guarddb.AIRuleStatusOptions{
						Actor:       "system:auto_activate",
						Activate:    true,
						ExpiresDays: cfg.RuleExpiryDays,
					})
				}
			}
		}
		if sp, _ := st.GetAISample(ctx, sampleID); sp != nil {
			learned := "learned"
			_ = st.SaveAISample(ctx, sp, guarddb.AISampleUpdate{Status: &learned})
		}
		_, _ = st.SaveAILearningJob(ctx, sampleID, newToken, "done", map[string]any{"extracted": len(sanitized)}, 0, nil)
		return nil
	})
}

func (l *AILearning) resolveCompleter() (Completer, string) {
	if l.completer != nil {
		return l.completer, "guard-test"
	}
	if l.appCfg != nil {
		c := l.appCfg.EffectiveAIConfig("guard", "summary")
		return llm.NewPurposeClient(l.appCfg, "guard"), c.Model
	}
	return nil, ""
}

func extractJSONObject(s string) string {
	start := strings.IndexByte(s, '{')
	end := strings.LastIndexByte(s, '}')
	if start >= 0 && end >= start {
		return s[start : end+1]
	}
	return s
}

func parseAnyStringSlice(v any) []string {
	switch arr := v.(type) {
	case []string:
		return arr
	case []any:
		out := make([]string, 0, len(arr))
		for _, it := range arr {
			if s, ok := it.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	case string:
		var out []string
		_ = json.Unmarshal([]byte(arr), &out)
		return out
	default:
		return nil
	}
}
