// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package forwarding

import (
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/Sakura520222/Sakura-Bot/internal/config"
	"github.com/Sakura520222/Sakura-Bot/internal/mtproto"
	"github.com/dlclark/regexp2"
)

// ShouldForwardOriginalOnly 判断原创消息过滤是否放行（对齐 filters.py:should_forward_original_only）。
func ShouldForwardOriginalOnly(isForwarded, forwardOriginalOnly bool) bool {
	if !forwardOriginalOnly {
		return true
	}
	return !isForwarded
}

// ShouldForwardByKeywords 根据黑/白名单关键词（大小写不敏感子串）判断是否放行（对齐 filters.py:should_forward_by_keywords）。
func ShouldForwardByKeywords(text string, keywords, blacklist []string) bool {
	if len(keywords) == 0 && len(blacklist) == 0 {
		return true
	}
	if text == "" {
		return false
	}
	lowerText := strings.ToLower(text)
	for _, kw := range blacklist {
		if kw != "" && strings.Contains(lowerText, strings.ToLower(kw)) {
			return false
		}
	}
	if len(keywords) > 0 {
		for _, kw := range keywords {
			if kw != "" && strings.Contains(lowerText, strings.ToLower(kw)) {
				return true
			}
		}
		return false
	}
	return true
}

// ShouldForwardByRegex 根据黑/白名单正则表达式（大小写不敏感）判断是否放行（对齐 filters.py:should_forward_by_regex）。
func ShouldForwardByRegex(text string, patterns, blacklistPatterns []string, logger *slog.Logger) bool {
	if len(patterns) == 0 && len(blacklistPatterns) == 0 {
		return true
	}
	if text == "" {
		return false
	}
	if logger == nil {
		logger = slog.Default()
	}

	for _, pat := range blacklistPatterns {
		if pat == "" {
			continue
		}
		matched, valid := matchCaseInsensitiveRegex(pat, text)
		if !valid {
			logger.Warn("黑名单正则表达式无效，已跳过", "pattern", pat)
			continue
		}
		if matched {
			return false
		}
	}

	if len(patterns) > 0 {
		for _, pat := range patterns {
			if pat == "" {
				continue
			}
			matched, valid := matchCaseInsensitiveRegex(pat, text)
			if !valid {
				logger.Warn("白名单正则表达式无效，已跳过", "pattern", pat)
				continue
			}
			if matched {
				return true
			}
		}
		return false
	}
	return true
}

// ShouldForwardGroup 对单条消息或媒体组执行三阶段规则过滤（原创性 -> 关键词 -> 正则）。
func ShouldForwardGroup(msgs []*mtproto.IncomingMessage, rule config.ForwardingRule, logger *slog.Logger) bool {
	if len(msgs) == 0 {
		return false
	}
	isForwarded := false
	var combined strings.Builder
	for _, m := range msgs {
		if m == nil {
			continue
		}
		if m.FwdFrom != nil {
			isForwarded = true
		}
		if m.Text != "" {
			if combined.Len() > 0 {
				combined.WriteByte('\n')
			}
			combined.WriteString(m.Text)
		}
	}
	text := combined.String()
	if !ShouldForwardOriginalOnly(isForwarded, rule.ForwardOriginalOnly) {
		return false
	}
	if !ShouldForwardByKeywords(text, rule.Keywords, rule.Blacklist) {
		return false
	}
	if !ShouldForwardByRegex(text, rule.Patterns, rule.BlacklistPatterns, logger) {
		return false
	}
	return true
}

func matchCaseInsensitiveRegex(pattern, text string) (matched bool, valid bool) {
	if re, err := regexp.Compile("(?i)" + pattern); err == nil {
		return re.MatchString(text), true
	}
	re2, err := regexp2.Compile(pattern, regexp2.IgnoreCase|regexp2.Multiline)
	if err != nil {
		return false, false
	}
	re2.MatchTimeout = 200 * time.Millisecond
	ok, merr := re2.MatchString(text)
	if merr != nil {
		return false, true
	}
	return ok, true
}
