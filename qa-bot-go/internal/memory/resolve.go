// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package memory

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/config"
)

// ResolveResult 是 resolve_channel 的返回结构（memory_manager.py:196-281
// 返回字典的 Go 形状）。字段顺序与 Python 字典插入顺序一致，json 序列化
// 键序逐字对齐；omitempty 保证不同分支只出现 Python 字典实际含有的键
// （成功分支无 error/candidates，错误分支无 channel_* 等）。
// Candidates 为 any：错误分支固定空列表 []（JSON "[]"，非 null），
// 多候选分支为 []ChannelEntry。
type ResolveResult struct {
	Success     bool   `json:"success"`
	ChannelID   string `json:"channel_id,omitempty"`
	ChannelName string `json:"channel_name,omitempty"`
	MatchType   string `json:"match_type,omitempty"`
	Channel     any    `json:"channel,omitempty"`
	Error       string `json:"error,omitempty"`
	Candidates  any    `json:"candidates,omitempty"`
}

// channelNameRow 是标准化兜底分支的 channel 字段（memory_manager.py:271-275
// 仅含 channel_id/channel_name 两个键的字典）。
type channelNameRow struct {
	ChannelID   string `json:"channel_id"`
	ChannelName string `json:"channel_name"`
}

// resolveError 构造错误分支结果：{"success": False, "error": msg, "candidates": []}。
func resolveError(msg string) ResolveResult {
	return ResolveResult{Success: false, Error: msg, Candidates: []any{}}
}

// telegramHintPrefixes 是"看起来像 Telegram 标识"的前缀表
// （memory_manager.py:265，startswith 元组逐字一致）。
var telegramHintPrefixes = []string{"@", "http://", "https://", "t.me/", "telegram.me/"}

// ResolveChannel 将用户输入的频道提示解析为标准频道链接
// （memory_manager.py:196-281 逐行移植）。唯一匹配 success=true；
// 多候选返回前 10 条；目录无匹配但输入形如 Telegram 标识时返回标准化链接。
// Python 侧整个方法体包在 try/except 中，Go 以 recover 等价兜底。
func (m *Manager) ResolveChannel(ctx context.Context, channelHint string) (res ResolveResult) {
	defer func() {
		// Python except 兜底：log + {"success": False, "error": f"解析频道失败: {e}"}
		if r := recover(); r != nil {
			slog.Error(fmt.Sprintf("解析频道失败: panic: %v", r))
			res = ResolveResult{
				Success:    false,
				Error:      fmt.Sprintf("解析频道失败: %v", r),
				Candidates: []any{},
			}
		}
	}()
	return m.resolveChannel(ctx, channelHint)
}

// resolveChannel 是 ResolveChannel 的主体（memory_manager.py:207-277）。
func (m *Manager) resolveChannel(ctx context.Context, channelHint string) ResolveResult {
	// hint = (channel_hint or "").strip()
	hint := strings.TrimSpace(channelHint)
	if hint == "" {
		return resolveError("频道提示为空")
	}

	// Python：list_channels 自吞异常返回 []，此处永不失败。
	channels := m.ListChannels(ctx)

	normalizedHint := config.NormalizeChannelID(hint)
	// Python .lstrip("@") 去除全部前导 @。
	hintLower := strings.TrimLeft(strings.ToLower(hint), "@")
	normalizedLower := strings.ToLower(normalizedHint)
	hintTailLower := strings.TrimLeft(
		strings.ToLower(lastSegment(strings.TrimRight(normalizedHint, "/"))), "@")

	var exactMatches, fuzzyMatches []ChannelEntry

	for _, ch := range channels {
		channelID := ch.ChannelID
		channelName := ch.ChannelName // Python `or ""`：nil 名已由目录层保证非 nil
		channelTail := lastSegment(strings.TrimRight(channelID, "/"))
		candidates := []string{
			strings.ToLower(channelID),
			strings.ToLower(config.NormalizeChannelID(channelID)),
			strings.ToLower(channelTail),
			strings.ToLower(channelName),
		}

		if containsStr(candidates, normalizedLower) ||
			containsStr(candidates, hintLower) ||
			containsStr(candidates, hintTailLower) {
			exactMatches = append(exactMatches, ch)
			continue
		}

		if strings.Contains(strings.ToLower(channelName), hintLower) ||
			strings.Contains(strings.ToLower(channelTail), hintLower) ||
			strings.Contains(strings.ToLower(channelTail), hintTailLower) {
			fuzzyMatches = append(fuzzyMatches, ch)
		}
	}

	// matches = exact_matches or fuzzy_matches
	matches := exactMatches
	if len(matches) == 0 {
		matches = fuzzyMatches
	}

	if len(matches) == 1 {
		match := matches[0]
		matchType := "fuzzy"
		if len(exactMatches) > 0 {
			matchType = "exact"
		}
		return ResolveResult{
			Success:     true,
			ChannelID:   match.ChannelID,
			ChannelName: match.ChannelName,
			MatchType:   matchType,
			Channel:     &match,
		}
	}

	if len(matches) > 1 {
		n := len(matches)
		if n > 10 {
			n = 10
		}
		return ResolveResult{
			Success:    false,
			Error:      "找到多个匹配频道，请提供更完整的频道链接或名称",
			Candidates: matches[:n],
		}
	}

	// 没有目录匹配时，若输入看起来像 Telegram 标识，返回标准化链接供后续检索尝试。
	for _, prefix := range telegramHintPrefixes {
		if strings.HasPrefix(hint, prefix) {
			tail := lastSegment(normalizedHint)
			return ResolveResult{
				Success:     true,
				ChannelID:   normalizedHint,
				ChannelName: tail,
				MatchType:   "normalized",
				Channel:     &channelNameRow{ChannelID: normalizedHint, ChannelName: tail},
			}
		}
	}

	return resolveError("未找到匹配频道")
}

// lastSegment 等价 Python s.split("/")[-1]（含以 / 结尾返回空串的边界）。
func lastSegment(s string) string {
	if i := strings.LastIndex(s, "/"); i >= 0 {
		return s[i+1:]
	}
	return s
}

// containsStr 等价 Python 集合成员判断（candidates 规模 ≤4，线性扫描即可）。
func containsStr(items []string, v string) bool {
	for _, it := range items {
		if it == v {
			return true
		}
	}
	return false
}
