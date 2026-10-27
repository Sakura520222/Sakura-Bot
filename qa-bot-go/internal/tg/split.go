// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package tg

import (
	"strings"
	"unicode/utf8"
)

// SplitLongMessage 将超长消息按 "\n\n" 段落贪心分片
// （qa_bot.py:858-878 _split_long_message 的逐行移植）。
//
// 长度判定全部按 rune 计（R2）。行为要点与 Python 版逐条一致：
//   - 短于等于 max_length 时原样返回单分片；
//   - 当前累计加上下一段后仍不超限时追加（含段间 "\n\n"）；
//   - 超限时先落袋当前累计（去除首尾空白）再开启新分片；
//   - 单个段落本身超过 max_length 时不做二次切分、原样成片
//     （该分片可能仍超限，由发送失败后的降级路径兜底），此处保持一致。
func SplitLongMessage(text string, maxLength int) []string {
	if utf8.RuneCountInString(text) <= maxLength {
		return []string{text}
	}

	parts := []string{}
	var current strings.Builder
	paragraphs := strings.Split(text, "\n\n")

	for _, para := range paragraphs {
		if utf8.RuneCountInString(current.String())+utf8.RuneCountInString(para)+2 <= maxLength {
			current.WriteString(para)
			current.WriteString("\n\n")
		} else {
			if current.Len() > 0 {
				parts = append(parts, strings.TrimSpace(current.String()))
			}
			current.Reset()
			current.WriteString(para)
			current.WriteString("\n\n")
		}
	}

	if current.Len() > 0 {
		parts = append(parts, strings.TrimSpace(current.String()))
	}

	return parts
}

// TruncateRunes 按 rune 截断字符串到至多 n 个字符（等价 Python text[:n]，
// R2：禁止按字节切片，避免截断多字节字符）。
func TruncateRunes(text string, n int) string {
	if n <= 0 {
		return ""
	}
	count := 0
	for i := range text {
		if count == n {
			return text[:i]
		}
		count++
	}
	return text
}
