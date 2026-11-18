// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

// Package links 提供 Telegram 频道帖子链接的纯函数构造。
//
// 逐条复刻 core/infrastructure/utils/telegram_links.py（含其依赖的
// _normalize_channel）：
//   - 公开频道（有 username）：https://t.me/<username>/<message_id>
//   - 私有频道 / 数字内部 ID：https://t.me/c/<channel_id>/<message_id>
//
// 无法验证出合法链接时返回空字符串（对齐 Python 版返回 None），调用方不得把
// 内部 ID、数据库主键或总结报告消息 ID 冒充为频道帖子链接。
package links

import (
	"regexp"
	"strconv"
	"strings"
)

// linkPrefixes t.me 链接的已知 host 前缀（顺序对齐 Python 版 _LINK_PREFIXES）
var linkPrefixes = [...]string{
	"https://t.me/",
	"http://t.me/",
	"https://telegram.me/",
	"http://telegram.me/",
	"t.me/",
	"telegram.me/",
}

// usernamePattern Telegram username 规则：4-32 位，字母开头，仅字母/数字/下划线。
// 官方实际仅允许小写，这里放宽大小写以兼容历史数据。
// Python 版使用 re.fullmatch；Go 的 RE2 无 fullmatch，用 ^...$ 锚点等价复刻。
var usernamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{3,31}$`)

// isDigits 等价 Python str.isdigit() 的 ASCII 子集：非空且全部为 0-9。
func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// normalizeChannel 把各种频道标识归一化为纯 username 或纯数字 ID，失败返回空串。
//
// 逐行对齐 telegram_links.py 的 _normalize_channel：支持 t.me/telegram.me 链接、
// @username、c/<id> 路径、Bot API 的 -100 前缀标记 ID、裸 username、裸数字 ID；
// 邀请链接（+开头）与无法识别的标识返回空串。
func normalizeChannel(channelID string) string {
	channel := strings.TrimSpace(channelID)
	if channel == "" {
		return ""
	}

	for _, prefix := range linkPrefixes {
		if strings.HasPrefix(channel, prefix) {
			channel = channel[len(prefix):]
			break
		}
	}

	channel = strings.Trim(channel, "/")

	// 邀请链接（如 +AbCdEf）无法定位具体帖子
	if strings.HasPrefix(channel, "+") || strings.HasPrefix(strings.Split(channel, "/")[0], "+") {
		return ""
	}

	// 多段路径：取第一段（仅 c/<id> 形式保留多段，交由后续分支处理；
	// 其余如 username/123 帖子路径取首段）
	segments := strings.Split(channel, "/")
	firstSegment := segments[0]
	if len(segments) > 1 && firstSegment != "c" {
		channel = firstSegment
	}

	if strings.HasPrefix(channel, "c/") {
		// c/<id> 或 c/<id>/<message_id>，只保留数字频道 ID 段；c/ 后必须是数字 ID
		channel = strings.Split(channel[2:], "/")[0]
		if isDigits(channel) {
			return channel
		}
		return ""
	}

	if strings.HasPrefix(channel, "@") {
		channel = channel[1:]
	}

	// Bot API 标记 ID：-100<channel_id> → 去掉 -100 前缀
	if strings.HasPrefix(channel, "-100") && isDigits(channel[4:]) {
		channel = channel[4:]
	}

	if channel == "" {
		return ""
	}

	return channel
}

// BuildTelegramPostLink 构造 Telegram 频道帖子链接。
//
// channelID 支持 username、@username、t.me 链接、数字内部 ID、Bot API "-100"
// 标记 ID 与 c/<id> 路径；messageID 必须是正整数（频道内的真实消息 ID）。
//
// 频道标识无法识别、消息 ID 非法时返回空字符串（对齐 Python 版返回 None）。
// 私有频道链接（/c/ 形式）要求访问者拥有频道权限才能打开。
func BuildTelegramPostLink(channelID string, messageID int64) string {
	if messageID <= 0 {
		return ""
	}

	channel := normalizeChannel(channelID)
	if channel == "" {
		return ""
	}

	// 数字频道 ID：私有频道格式
	if isDigits(channel) {
		return "https://t.me/c/" + channel + "/" + strconv.FormatInt(messageID, 10)
	}

	// 公开频道 username
	if usernamePattern.MatchString(channel) {
		return "https://t.me/" + channel + "/" + strconv.FormatInt(messageID, 10)
	}

	return ""
}
