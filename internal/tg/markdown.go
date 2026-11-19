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

import "strings"

// FixMarkdown 修复常见的 Markdown 格式错误（qa_bot.py:899-930 _fix_markdown 的逐行移植）。
//
// 策略：按 "\n" 切行，行内以 rune 为单位统计各标记符号出现次数，若为奇数则在行尾
// 补全一个，避免暴力正则替换导致的文本错误。次序固定：先 **（粗体）、再 *（斜体）、
// 最后反引号（代码）。所有计数与拼接均按 rune 处理（R5：禁止按字节操作），
// 中英文与 emoji 混排下与 Python 版逐字符一致。
func FixMarkdown(text string) string {
	lines := strings.Split(text, "\n")
	fixedLines := make([]string, 0, len(lines))
	for _, line := range lines {
		runes := []rune(line)

		// 统计行内 "**" 对数（粗体）；奇数则补全
		boldCount := countDoubleStar(runes)
		if boldCount%2 == 1 {
			runes = append(runes, '*', '*')
		}

		// 去掉 "**" 后统计单个 "*"（斜体）；奇数则补全
		italicCount := countRune(stripDoubleStar(runes), '*')
		if italicCount%2 == 1 {
			runes = append(runes, '*')
		}

		// 统计反引号（代码）；奇数则补全
		if countRune(runes, '`')%2 == 1 {
			runes = append(runes, '`')
		}

		fixedLines = append(fixedLines, string(runes))
	}

	return strings.Join(fixedLines, "\n")
}

// countDoubleStar 以 rune 扫描统计非重叠 "**" 出现次数，
// 等价 Python re.findall(r"\*\*", line) 的长度。
func countDoubleStar(rs []rune) int {
	count := 0
	for i := 0; i < len(rs); i++ {
		if rs[i] == '*' && i+1 < len(rs) && rs[i+1] == '*' {
			count++
			i++ // 非重叠：跳过第二个 '*'
		}
	}
	return count
}

// stripDoubleStar 返回移除全部非重叠 "**" 后的新 rune 切片，
// 等价 Python re.sub(r"\*\*", "", line)。
func stripDoubleStar(rs []rune) []rune {
	out := make([]rune, 0, len(rs))
	for i := 0; i < len(rs); i++ {
		if rs[i] == '*' && i+1 < len(rs) && rs[i+1] == '*' {
			i++
			continue
		}
		out = append(out, rs[i])
	}
	return out
}

// countRune 统计 rune 切片中目标字符出现次数（等价 Python str.count）。
func countRune(rs []rune, target rune) int {
	count := 0
	for _, r := range rs {
		if r == target {
			count++
		}
	}
	return count
}
