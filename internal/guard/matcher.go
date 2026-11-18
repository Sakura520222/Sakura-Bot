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
	"container/list"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf16"

	"github.com/Sakura520222/Sakura-Bot/internal/guard/opencc"
	"github.com/Sakura520222/Sakura-Bot/internal/mtproto"
	"github.com/dlclark/regexp2"
	"golang.org/x/text/unicode/norm"
)

const (
	maxMatcherInputRunes = 16384
	perMatcherBudget     = 25 * time.Millisecond
	perRuleRegexTimeout  = 5 * time.Millisecond
)

var plainLinkRE = regexp.MustCompile(`(?i)(?:https?://|tg://|www\.)[^\s<>]+`)

// Match 描述单次规则或过滤器命中证据（对齐 matcher.py:Match）。
type Match struct {
	Target        string `json:"target"`
	RuleID        string `json:"rule_id"`
	Action        string `json:"action"`
	Reason        string `json:"reason"`
	Snippet       string `json:"snippet"`
	AIRuleID      string `json:"ai_rule_id,omitempty"`
	AIRuleVersion int    `json:"ai_rule_version,omitempty"`
}

type normCache struct {
	mu    sync.Mutex
	order *list.List
	items map[string]*list.Element
}

type normEntry struct {
	k, v string
}

var sharedNormCache = &normCache{
	order: list.New(),
	items: make(map[string]*list.Element, 256),
}

// NormalizeText 执行确定性防绕过归一化：NFKC -> 小写折叠 -> 剥离空白与 Cf/Cc/Mn/Me 隐形组合字符 -> OpenCC 繁转简（对齐 matcher.py:normalize）。
func NormalizeText(text string) string {
	if text == "" {
		return ""
	}
	sharedNormCache.mu.Lock()
	if el, ok := sharedNormCache.items[text]; ok {
		sharedNormCache.order.MoveToFront(el)
		val := el.Value.(*normEntry).v
		sharedNormCache.mu.Unlock()
		return val
	}
	sharedNormCache.mu.Unlock()

	nfkc := strings.ToLower(norm.NFKC.String(text))
	var stripped strings.Builder
	stripped.Grow(len(nfkc))
	for _, r := range nfkc {
		if unicode.IsSpace(r) {
			continue
		}
		if unicode.In(r, unicode.Cf, unicode.Cc, unicode.Mn, unicode.Me) {
			continue
		}
		stripped.WriteRune(r)
	}
	converted := opencc.T2S(stripped.String())

	sharedNormCache.mu.Lock()
	if len(text) <= 4096 {
		if el, ok := sharedNormCache.items[text]; ok {
			sharedNormCache.order.MoveToFront(el)
		} else {
			el := sharedNormCache.order.PushFront(&normEntry{k: text, v: converted})
			sharedNormCache.items[text] = el
			for sharedNormCache.order.Len() > 256 {
				back := sharedNormCache.order.Back()
				sharedNormCache.order.Remove(back)
				delete(sharedNormCache.items, back.Value.(*normEntry).k)
			}
		}
	}
	sharedNormCache.mu.Unlock()
	return converted
}

// HasTraditionalChinese 检测文本是否包含繁体字（对齐 ban_any_traditional: _converter.convert(v) != v）。
func HasTraditionalChinese(s string) bool {
	if s == "" {
		return false
	}
	return opencc.T2S(s) != s
}

// EvidenceWindow 二分定位归一化命中下标在原文中的近似位置，提取前后上下文窗口（最多 2048 字符，前向预留 256 字符，对齐 matcher.py:evidence_window）。
func EvidenceWindow(original string, normIndex int) string {
	runes := []rune(original)
	if len(runes) <= 2048 {
		return original
	}
	if normIndex <= 0 {
		return string(runes[:2048])
	}
	lo, hi := 0, len(runes)
	for lo < hi {
		mid := (lo + hi) / 2
		if len([]rune(NormalizeText(string(runes[:mid])))) < normIndex {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	start := lo - 256
	if start < 0 {
		start = 0
	}
	end := start + 2048
	if end > len(runes) {
		end = len(runes)
	}
	return string(runes[start:end])
}

// ExtractLinks 从消息正文与 UTF-16 富文本实体中提取、解码并去重链接（上限 100 条，对齐 matcher.py:extract_links）。
func ExtractLinks(text string, entities []mtproto.EntitySpan) []string {
	seen := make(map[string]bool)
	var out []string
	add := func(raw string) {
		if len(out) >= 100 {
			return
		}
		trimmed := strings.TrimRight(strings.TrimSpace(raw), ".,;:!?)]}\"'")
		if trimmed == "" {
			return
		}
		if unescaped, err := url.QueryUnescape(trimmed); err == nil && unescaped != "" {
			trimmed = unescaped
		}
		if !seen[trimmed] {
			seen[trimmed] = true
			out = append(out, trimmed)
		}
	}

	for _, m := range plainLinkRE.FindAllString(text, 100) {
		add(m)
	}
	if len(entities) > 0 {
		u16 := utf16.Encode([]rune(text))
		for _, ent := range entities {
			switch ent.Kind {
			case "text_url":
				if ent.URL != "" {
					add(ent.URL)
				}
			case "url":
				if ent.Offset >= 0 && ent.Length > 0 && ent.Offset+ent.Length <= len(u16) {
					add(string(utf16.Decode(u16[ent.Offset : ent.Offset+ent.Length])))
				}
			}
		}
	}
	return out
}

// Matcher 封装带总正则时间预算（25ms）的单次检测器。
type Matcher struct {
	remaining time.Duration
}

// NewMatcher 创建单次检测器。
func NewMatcher() *Matcher {
	return &Matcher{remaining: perMatcherBudget}
}

// MatchNickname 检查成员昵称、主用户名及所有活跃用户名（对齐 Matcher.nickname）。
func (m *Matcher) MatchNickname(user mtproto.ProfileSummary, globalCfg GuardConfig, grp GuardGroup) *Match {
	if !grp.NicknameFilter {
		return nil
	}
	if hit := m.matchGlobalNickname(user, globalCfg.NicknameKeywords, grp.NicknameAction); hit != nil {
		return hit
	}
	fullName := strings.TrimSpace(user.FirstName + " " + user.LastName)
	usernames := profileUsernames(user)
	// 2. 群自定义 nickname / username 规则（delete 自动提升为 nickname_action）
	for _, rule := range grp.Rules {
		if rule.Target != "nickname" && rule.Target != "username" {
			continue
		}
		act := rule.Action
		if act == "delete" {
			act = grp.NicknameAction
		}
		if rule.Target == "nickname" && fullName != "" {
			if hit := m.matchSingleRule(rule.Target, rule.ID, act, rule.Kind, rule.Value, fullName); hit != nil {
				return hit
			}
		}
		if rule.Target == "username" {
			for _, u := range usernames {
				if hit := m.matchSingleRule("username", rule.ID, act, rule.Kind, rule.Value, u); hit != nil {
					return hit
				}
			}
		}
	}

	// 3. 拦截一切繁体昵称
	if grp.BanAnyTraditional && fullName != "" && HasTraditionalChinese(fullName) {
		return &Match{
			Target:  "nickname",
			RuleID:  "traditional-chinese-nickname",
			Action:  grp.NicknameAction,
			Reason:  "traditional_chinese",
			Snippet: EvidenceWindow(fullName, 0),
		}
	}
	return nil
}

func profileUsernames(user mtproto.ProfileSummary) []string {
	var usernames []string
	if user.Username != "" {
		usernames = append(usernames, user.Username)
	}
	for _, username := range user.ActiveUsernames {
		if username != "" && username != user.Username {
			usernames = append(usernames, username)
		}
	}
	return usernames
}

// matchGlobalNickname also runs on join requests for groups with local nickname filters disabled.
func (m *Matcher) matchGlobalNickname(user mtproto.ProfileSummary, keywords []string, action string) *Match {
	fullName := strings.TrimSpace(user.FirstName + " " + user.LastName)
	if hit := m.matchKeywords("nickname", "global-nickname-keyword", action, fullName, keywords); hit != nil {
		return hit
	}
	for _, username := range profileUsernames(user) {
		if hit := m.matchKeywords("username", "global-nickname-keyword", action, username, keywords); hit != nil {
			return hit
		}
	}
	return nil
}

// MatchStructural 检查名片、内联按钮、转发与普通 Bot 发言拦截（优先级严格对齐 Moderator.match 第 6-7 步）。
func (m *Matcher) MatchStructural(msg *mtproto.IncomingMessage, grp GuardGroup) *Match {
	if msg == nil {
		return nil
	}
	if grp.BlockContactMessages && msg.Media != nil && msg.Media.Kind == "contact" {
		return &Match{
			Target:  "contact",
			RuleID:  "block-contact-message",
			Action:  grp.ContactMessageAction,
			Reason:  "contact_message",
			Snippet: strings.TrimSpace(msg.Media.ContactFirstName + " " + msg.Media.ContactPhone),
		}
	}
	if grp.BlockButtonMessages && msg.HasButtons {
		return &Match{
			Target:  "button",
			RuleID:  "block-button-message",
			Action:  grp.ButtonMessageAction,
			Reason:  "button_message",
			Snippet: EvidenceWindow(msg.Text, 0),
		}
	}
	if grp.BlockForwardedMessages && msg.FwdFrom != nil {
		return &Match{
			Target:  "forward",
			RuleID:  "block-forwarded-message",
			Action:  grp.ForwardedMessageAction,
			Reason:  "forwarded_message",
			Snippet: EvidenceWindow(msg.Text, 0),
		}
	}
	if grp.BlockBotMessages && (msg.Sender.IsBot || msg.ViaBotID != 0) {
		return &Match{
			Target:  "bot",
			RuleID:  "block-bot-message",
			Action:  grp.BotMessageAction,
			Reason:  "bot_message",
			Snippet: EvidenceWindow(msg.Text, 0),
		}
	}
	return nil
}

// MatchMessageAndLinks 检查正文与链接关键词及自定义规则（对齐 Moderator.match 第 8 步）。
func (m *Matcher) MatchMessageAndLinks(msg *mtproto.IncomingMessage, grp GuardGroup) *Match {
	if msg == nil || !grp.MessageFilter {
		return nil
	}
	body := truncateInputRunes(msg.Text)
	if body != "" {
		if hit := m.matchKeywords("message", "group-message-keyword", grp.MessageAction, body, grp.MessageKeywords); hit != nil {
			return hit
		}
		for _, rule := range grp.Rules {
			if rule.Target == "message" {
				if hit := m.matchSingleRule("message", rule.ID, rule.Action, rule.Kind, rule.Value, body); hit != nil {
					return hit
				}
			}
		}
	}

	links := ExtractLinks(msg.Text, msg.Entities)
	for _, lk := range links {
		if hit := m.matchKeywords("link", "group-message-keyword", grp.MessageAction, lk, grp.MessageKeywords); hit != nil {
			return hit
		}
		for _, rule := range grp.Rules {
			if rule.Target == "link" {
				if hit := m.matchSingleRule("link", rule.ID, rule.Action, rule.Kind, rule.Value, lk); hit != nil {
					return hit
				}
			}
		}
	}
	return nil
}

func (m *Matcher) matchKeywords(target, ruleID, action, original string, keywords []string) *Match {
	if len(keywords) == 0 || original == "" {
		return nil
	}
	normHaystack := NormalizeText(truncateInputRunes(original))
	if normHaystack == "" {
		return nil
	}
	for _, kw := range keywords {
		normNeedle := NormalizeText(kw)
		if normNeedle == "" {
			continue
		}
		if byteIdx := strings.Index(normHaystack, normNeedle); byteIdx >= 0 {
			runePos := len([]rune(normHaystack[:byteIdx]))
			return &Match{
				Target:  target,
				RuleID:  ruleID,
				Action:  action,
				Reason:  kw,
				Snippet: EvidenceWindow(original, runePos),
			}
		}
	}
	return nil
}

func (m *Matcher) matchSingleRule(target, ruleID, action, kind, value, original string) *Match {
	trimmedOriginal := truncateInputRunes(original)
	if trimmedOriginal == "" {
		return nil
	}
	if kind == "keyword" {
		return m.matchKeywords(target, ruleID, action, trimmedOriginal, []string{value})
	}
	if m.remaining <= 0 {
		return nil
	}
	normHaystack := NormalizeText(trimmedOriginal)
	if normHaystack == "" {
		return nil
	}
	timeout := perRuleRegexTimeout
	if m.remaining < timeout {
		timeout = m.remaining
	}
	started := time.Now()
	re, err := regexp2.Compile(value, regexp2.IgnoreCase)
	if err != nil {
		m.remaining -= time.Since(started)
		return nil
	}
	re.MatchTimeout = timeout
	found, merr := re.FindStringMatch(normHaystack)
	m.remaining -= time.Since(started)
	if merr != nil || found == nil {
		return nil
	}
	return &Match{
		Target:  target,
		RuleID:  ruleID,
		Action:  action,
		Reason:  value,
		Snippet: EvidenceWindow(original, found.Index),
	}
}

func truncateInputRunes(s string) string {
	r := []rune(s)
	if len(r) <= maxMatcherInputRunes {
		return s
	}
	return string(r[:maxMatcherInputRunes])
}
