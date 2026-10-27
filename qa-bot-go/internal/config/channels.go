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

import "strings"

// channelURLPrefixes 是可识别的 Telegram 链接前缀（顺序敏感：
// www. 变体必须先于短前缀匹配，对齐 core/config.py:87-100）。
var channelURLPrefixes = []string{
	"https://www.t.me/",
	"https://t.me/",
	"http://www.t.me/",
	"http://t.me/",
	"https://www.telegram.me/",
	"https://telegram.me/",
	"http://www.telegram.me/",
	"http://telegram.me/",
	"www.t.me/",
	"t.me/",
	"www.telegram.me/",
	"telegram.me/",
}

// NormalizeChannelID 移植 core/config.py:58-109 的 normalize_channel_id：
// 任意频道标识归一为 https://t.me/<id> 完整 URL 形式。
func NormalizeChannelID(channelID string) string {
	if channelID == "" {
		return channelID
	}
	// Python str.strip()；TrimSpace 对常见空白等价。
	s := strings.TrimSpace(channelID)

	for _, prefix := range channelURLPrefixes {
		if strings.HasPrefix(s, prefix) {
			rest := strings.TrimPrefix(s, prefix)
			// removeprefix 一次 + rstrip('/')（去掉全部结尾斜杠）。
			return "https://t.me/" + strings.TrimRight(rest, "/")
		}
	}

	s = strings.TrimPrefix(s, "@")
	return "https://t.me/" + s
}

// Channels 返回当前生效频道列表：
//   - config.json 的 channels（必须是字符串数组）整体覆盖 env；
//   - 否则拆分 env TARGET_CHANNEL（逗号分隔）。
//
// 每个条目经 NormalizeChannelID 归一（设计 §3.2）；空条目丢弃
// （Python 会保留空串条目，属明显缺陷，此处有意修正并在差异表中记录）。
func (c *Config) Channels() []string {
	cfg := c.readConfig()
	if raw, ok := cfg["channels"]; ok {
		if arr, ok := raw.([]any); ok {
			out := make([]string, 0, len(arr))
			for _, item := range arr {
				s, ok := item.(string)
				if !ok || strings.TrimSpace(s) == "" {
					continue
				}
				out = append(out, NormalizeChannelID(s))
			}
			return out
		}
		// 类型不符（对齐 load_config 仅接受 list 的分支）：回退 env。
	}

	target := c.Env.Get(KeyTargetChannel)
	if strings.TrimSpace(target) == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(target, ",") {
		normalized := NormalizeChannelID(part)
		if normalized == "" || normalized == "https://t.me/" {
			continue
		}
		out = append(out, normalized)
	}
	return out
}
