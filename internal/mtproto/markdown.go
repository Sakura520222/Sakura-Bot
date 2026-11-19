// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package mtproto

import (
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/gotd/td/tg"
	"golang.org/x/net/html"
)

var (
	reFencedCodeBlock = regexp.MustCompile("(?s)```.*?```")
	reInlineCodeSpan  = regexp.MustCompile("`[^`\n]+`")
	reMarkdownLink    = regexp.MustCompile(`\[([^\[\]]*)\]\(([^()]*)\)`)
	reParagraphBreak  = regexp.MustCompile(`\n\s*\n`)
	reSentenceBreak   = regexp.MustCompile(`[。.?!]\s+`)

	reStripCodeBlock = regexp.MustCompile("(?s)```(?:[a-zA-Z0-9_+#.-]*\n)?(.+?)```")
	reStripBoldStar  = regexp.MustCompile(`(?s)\*\*(.+?)\*\*`)
	reStripBoldUnder = regexp.MustCompile(`(?s)__(.+?)__`)
	reStripStrike    = regexp.MustCompile(`(?s)~~(.+?)~~`)
	reStripCode      = regexp.MustCompile("`([^`]+?)`")
	reStripLink      = regexp.MustCompile(`\[([^\[\]]+?)\]\([^()]*?\)`)
	reStripItalStar  = regexp.MustCompile(`(?s)\*([^*]+?)\*`)
	reStripItalUnder = regexp.MustCompile(`(?s)_([^_]+?)_`)

	mdProtectedPatterns = []*regexp.Regexp{
		regexp.MustCompile("(?s)```.*?```"),
		regexp.MustCompile(`(?s)\*\*.*?\*\*`),
		regexp.MustCompile("(?s)`.*?`"),
		regexp.MustCompile(`(?s)\[.*?\]\(.*?\)`),
		regexp.MustCompile(`(?s)\*.*?\*`),
		regexp.MustCompile(`(?s)_.*?_`),
		regexp.MustCompile(`(?s)~~.*?~~`),
		regexp.MustCompile(`(?s)__.*?__`),
	}
)

// UTF16Len 计算字符串按 Telegram 实体规范表示的 UTF-16 code units 长度。
// BMP 字符（<= U+FFFF，含 ASCII 与常用中日韩汉字）计 1，辅助平面字符（如 🌸 U+1F338）计 2。
func UTF16Len(s string) int {
	n := 0
	for _, r := range s {
		if r > 0xFFFF {
			n += 2
		} else {
			n++
		}
	}
	return n
}

func utf16RuneLen(r rune) int {
	if r > 0xFFFF {
		return 2
	}
	return 1
}

type runeSpan struct {
	start int
	end   int
}

func byteOffsetsToRuneMap(text string) []int {
	byteToRune := make([]int, len(text)+1)
	runeIdx := 0
	for i := 0; i < len(text); {
		byteToRune[i] = runeIdx
		_, width := utf8.DecodeRuneInString(text[i:])
		for k := 1; k < width; k++ {
			byteToRune[i+k] = runeIdx
		}
		i += width
		runeIdx++
	}
	byteToRune[len(text)] = runeIdx
	return byteToRune
}

// SplitMessageSmart 智能分割超长消息，尽量在段落/句子边界切分并保护 Markdown 实体完整性
// （行为对齐 core/telegram/client_utils.py:split_message_smart）。
func SplitMessageSmart(text string, maxLen int, preserveMD bool) []string {
	if maxLen <= 0 {
		return []string{text}
	}
	runes := []rune(text)
	if len(runes) <= maxLen {
		return []string{text}
	}

	if !preserveMD {
		var parts []string
		for i := 0; i < len(runes); i += maxLen {
			end := min(i+maxLen, len(runes))
			parts = append(parts, string(runes[i:end]))
		}
		return parts
	}

	byteToRune := byteOffsetsToRuneMap(text)

	var entities []runeSpan
	for _, pat := range mdProtectedPatterns {
		for _, loc := range pat.FindAllStringIndex(text, -1) {
			entities = append(entities, runeSpan{
				start: byteToRune[loc[0]],
				end:   byteToRune[loc[1]],
			})
		}
	}
	sort.Slice(entities, func(i, j int) bool {
		if entities[i].start != entities[j].start {
			return entities[i].start < entities[j].start
		}
		return entities[i].end < entities[j].end
	})

	var merged []runeSpan
	for _, ent := range entities {
		if len(merged) == 0 || ent.start > merged[len(merged)-1].end {
			merged = append(merged, ent)
		} else if ent.end > merged[len(merged)-1].end {
			merged[len(merged)-1].end = ent.end
		}
	}

	boundarySet := make(map[int]struct{})
	for _, loc := range reParagraphBreak.FindAllStringIndex(text, -1) {
		boundarySet[byteToRune[loc[1]]] = struct{}{}
	}
	for _, loc := range reSentenceBreak.FindAllStringIndex(text, -1) {
		boundarySet[byteToRune[loc[1]]] = struct{}{}
	}
	allBoundaries := make([]int, 0, len(boundarySet))
	for b := range boundarySet {
		allBoundaries = append(allBoundaries, b)
	}
	sort.Ints(allBoundaries)

	const minSegmentLength = 100
	var parts []string
	currentPos := 0

	for currentPos < len(runes) {
		if len(runes)-currentPos <= maxLen {
			tail := strings.TrimSpace(string(runes[currentPos:]))
			if tail != "" {
				parts = append(parts, tail)
			}
			break
		}

		maxEndPos := min(currentPos+maxLen, len(runes))
		bestSplitPos := -1

		for _, b := range allBoundaries {
			if b > currentPos && b <= maxEndPos {
				bestSplitPos = b
				break
			}
		}

		if bestSplitPos != -1 && (bestSplitPos-currentPos) < minSegmentLength {
			for _, b := range allBoundaries {
				if b > bestSplitPos && b <= maxEndPos {
					segLen := b - currentPos
					if segLen >= minSegmentLength && segLen <= maxLen {
						bestSplitPos = b
					} else if segLen < minSegmentLength {
						bestSplitPos = b
					} else {
						break
					}
				}
			}
		}

		if bestSplitPos == -1 {
			for _, ent := range merged {
				if ent.start < maxEndPos && maxEndPos < ent.end {
					if ent.start > currentPos {
						bestSplitPos = ent.start
						break
					} else if ent.end <= currentPos+maxLen*2 && ent.end <= len(runes) {
						bestSplitPos = ent.end
						break
					}
				}
			}
		}

		if bestSplitPos == -1 {
			for i := maxEndPos - 1; i > currentPos; i-- {
				r := runes[i]
				if unicode.IsSpace(r) || strings.ContainsRune("，。,.!?;:", r) {
					bestSplitPos = i + 1
					break
				}
			}
			if bestSplitPos == -1 {
				bestSplitPos = maxEndPos
			}
		}

		if bestSplitPos <= currentPos {
			bestSplitPos = maxEndPos
		}

		part := strings.TrimSpace(string(runes[currentPos:bestSplitPos]))
		if part != "" {
			parts = append(parts, part)
		}
		currentPos = bestSplitPos
	}

	var validated []string
	for _, part := range parts {
		ok, _ := ValidateMessageEntities(part)
		if !ok {
			sanitized := SanitizeMarkdown(part, false)
			sRunes := []rune(sanitized)
			if len(sRunes) > maxLen {
				for i := 0; i < len(sRunes); i += maxLen {
					sub := string(sRunes[i:min(i+maxLen, len(sRunes))])
					if subOK, _ := ValidateMessageEntities(sub); !subOK {
						sub = SanitizeMarkdown(sub, true)
					}
					validated = append(validated, sub)
				}
			} else {
				validated = append(validated, sanitized)
			}
		} else if pRunes := []rune(part); len(pRunes) > maxLen {
			for i := 0; i < len(pRunes); i += maxLen {
				sub := string(pRunes[i:min(i+maxLen, len(pRunes))])
				if subOK, _ := ValidateMessageEntities(sub); !subOK {
					sub = SanitizeMarkdown(sub, false)
				}
				validated = append(validated, sub)
			}
		} else {
			validated = append(validated, part)
		}
	}

	if len(validated) == 0 {
		return []string{text}
	}
	return validated
}

// ValidateMessageEntities 校验消息中的 Markdown 实体配对是否完整
// （对齐 core/telegram/client_utils.py:validate_message_entities）。
func ValidateMessageEntities(text string) (bool, string) {
	if text == "" {
		return true, "空文本，无需验证"
	}

	codeBlockCount := strings.Count(text, "```")
	if codeBlockCount%2 != 0 {
		return false, fmt.Sprintf("代码块标记不匹配: 找到%d个```标记", codeBlockCount)
	}

	// 屏蔽已配对的代码块，避免代码块内的 * / _ / ` 误报。
	masked := reFencedCodeBlock.ReplaceAllStringFunc(text, func(m string) string {
		return strings.Repeat(" ", len(m))
	})

	backtickCount := strings.Count(masked, "`")
	if backtickCount%2 != 0 {
		return false, fmt.Sprintf("内联代码标记不匹配: 找到%d个`标记", backtickCount)
	}
	masked = reInlineCodeSpan.ReplaceAllStringFunc(masked, func(m string) string {
		return strings.Repeat(" ", len(m))
	})

	// 校验链接 [text](url)，同时屏蔽 (url) 内部的下划线避免 URL 参数误报。
	links := reMarkdownLink.FindAllStringSubmatchIndex(masked, -1)
	if len(links) > 0 {
		var buf strings.Builder
		last := 0
		for _, loc := range links {
			fullMatch := masked[loc[0]:loc[1]]
			linkContent := masked[loc[2]:loc[3]]
			if linkContent == "" {
				return false, fmt.Sprintf("链接文本为空: %s", fullMatch)
			}
			// 保留 [linkContent]，仅将 (url) 替换为空格
			buf.WriteString(masked[last:loc[4]])
			buf.WriteString(strings.Repeat(" ", loc[5]-loc[4]))
			buf.WriteString(masked[loc[5]:loc[1]])
			last = loc[1]
		}
		buf.WriteString(masked[last:])
		masked = buf.String()
	}

	boldCount := strings.Count(masked, "**")
	if boldCount%2 != 0 {
		return false, fmt.Sprintf("粗体标记不匹配: 找到%d个**标记", boldCount)
	}

	boldUnderscoreCount := strings.Count(masked, "__")
	if boldUnderscoreCount%2 != 0 {
		return false, fmt.Sprintf("粗体标记不匹配: 找到%d个__标记", boldUnderscoreCount)
	}

	italicCount := strings.Count(masked, "*") - boldCount*2
	if italicCount%2 != 0 {
		return false, fmt.Sprintf("斜体标记不匹配: 找到%d个*标记", italicCount)
	}

	italicUnderscoreCount := countStandaloneUnderscores(masked)
	if italicUnderscoreCount%2 != 0 {
		return false, fmt.Sprintf("斜体标记不匹配: 找到%d个_标记", italicUnderscoreCount)
	}

	strikeCount := strings.Count(masked, "~~")
	if strikeCount%2 != 0 {
		return false, fmt.Sprintf("删除线标记不匹配: 找到%d个~~标记", strikeCount)
	}

	return true, "所有实体完整"
}

// countStandaloneUnderscores 统计非 __ 且非词内 snake_case 的独立斜体 _ 标记数量。
func countStandaloneUnderscores(s string) int {
	withoutDouble := strings.ReplaceAll(s, "__", "  ")
	runes := []rune(withoutDouble)
	count := 0
	for i, r := range runes {
		if r != '_' {
			continue
		}
		var prev, next rune
		if i > 0 {
			prev = runes[i-1]
		}
		if i+1 < len(runes) {
			next = runes[i+1]
		}
		if isASCIIWordRune(prev) && isASCIIWordRune(next) {
			continue
		}
		count++
	}
	return count
}

func isASCIIWordRune(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}

// SanitizeMarkdown 清理和修复不完整的 Markdown 标记；
// aggressive=true 时剥离全部 Markdown 语法只保留纯文本（对齐 client_utils.py:sanitize_markdown）。
func SanitizeMarkdown(text string, aggressive bool) string {
	if text == "" {
		return text
	}

	if aggressive {
		result := text
		result = reStripCodeBlock.ReplaceAllString(result, "$1")
		result = reStripLink.ReplaceAllString(result, "$1")
		result = reStripBoldStar.ReplaceAllString(result, "$1")
		result = reStripBoldUnder.ReplaceAllString(result, "$1")
		result = reStripStrike.ReplaceAllString(result, "$1")
		result = reStripCode.ReplaceAllString(result, "$1")
		result = reStripItalStar.ReplaceAllString(result, "$1")
		result = stripMatchedUnderscoreItalic(result)
		// 清理任何残留的孤立语法标记
		for _, tok := range []string{"```", "**", "__", "~~", "`", "*"} {
			result = strings.ReplaceAll(result, tok, "")
		}
		result = stripUnpairedStandaloneUnderscores(result)
		return result
	}

	result := text
	if strings.Count(result, "```")%2 != 0 {
		result = strings.ReplaceAll(result, "```", "")
	}
	if strings.Count(result, "**")%2 != 0 {
		result = strings.ReplaceAll(result, "**", "")
	}
	if strings.Count(result, "__")%2 != 0 {
		result = strings.ReplaceAll(result, "__", "")
	}
	if strings.Count(result, "`")%2 != 0 {
		result = strings.ReplaceAll(result, "`", "")
	}
	boldCount := strings.Count(result, "**")
	if (strings.Count(result, "*")-boldCount*2)%2 != 0 {
		result = strings.ReplaceAll(result, "**", "")
		result = strings.ReplaceAll(result, "*", "")
	}
	if countStandaloneUnderscores(result)%2 != 0 {
		result = strings.ReplaceAll(result, "__", "")
		result = stripUnpairedStandaloneUnderscores(result)
	}
	if strings.Count(result, "~~")%2 != 0 {
		result = strings.ReplaceAll(result, "~~", "")
	}

	if ok, _ := ValidateMessageEntities(result); !ok {
		return SanitizeMarkdown(result, true)
	}
	return result
}

func stripMatchedUnderscoreItalic(s string) string {
	locs := reStripItalUnder.FindAllStringIndex(s, -1)
	if len(locs) == 0 {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	last := 0
	for _, loc := range locs {
		start, end := loc[0], loc[1]
		var prevBeforeOpen, nextAfterOpen rune
		if start > 0 {
			prevBeforeOpen, _ = utf8.DecodeLastRuneInString(s[:start])
		}
		if start+1 < len(s) {
			nextAfterOpen, _ = utf8.DecodeRuneInString(s[start+1:])
		}
		var prevBeforeClose, nextAfterClose rune
		if end-1 > 0 {
			prevBeforeClose, _ = utf8.DecodeLastRuneInString(s[:end-1])
		}
		if end < len(s) {
			nextAfterClose, _ = utf8.DecodeRuneInString(s[end:])
		}

		inWordOpen := isASCIIWordRune(prevBeforeOpen) && isASCIIWordRune(nextAfterOpen)
		inWordClose := isASCIIWordRune(prevBeforeClose) && isASCIIWordRune(nextAfterClose)
		b.WriteString(s[last:start])
		if inWordOpen || inWordClose {
			b.WriteString(s[start:end])
		} else {
			b.WriteString(s[start+1 : end-1])
		}
		last = end
	}
	b.WriteString(s[last:])
	return b.String()
}

func stripUnpairedStandaloneUnderscores(s string) string {
	runes := []rune(s)
	var b strings.Builder
	b.Grow(len(s))
	for i, r := range runes {
		if r == '_' {
			var prev, next rune
			if i > 0 {
				prev = runes[i-1]
			}
			if i+1 < len(runes) {
				next = runes[i+1]
			}
			if !(isASCIIWordRune(prev) && isASCIIWordRune(next)) {
				continue
			}
		}
		b.WriteRune(r)
	}
	return b.String()
}

// SplitByLinesSmart 按行智能分割，保证结果中每行不超过 maxLen 个字符
// （对齐 core/telegram/client_utils.py:split_by_lines_smart）。
func SplitByLinesSmart(text string, maxLen int) ([]string, error) {
	if maxLen <= 0 {
		return nil, errors.New("max_length 必须大于零")
	}
	lines := strings.Split(text, "\n")
	var result []string

	for _, line := range lines {
		lineRunes := []rune(line)
		if len(lineRunes) <= maxLen {
			result = append(result, line)
			continue
		}

		words := strings.Split(line, " ")
		currentLine := ""
		currentLen := 0

		for _, word := range words {
			wRunes := []rune(word)
			sepLen := 0
			if currentLen > 0 {
				sepLen = 1
			}
			if currentLen+sepLen+len(wRunes) <= maxLen {
				if currentLen > 0 {
					currentLine += " " + word
					currentLen += 1 + len(wRunes)
				} else {
					currentLine = word
					currentLen = len(wRunes)
				}
			} else {
				if currentLen > 0 {
					result = append(result, currentLine)
				}
				for len(wRunes) > maxLen {
					result = append(result, string(wRunes[:maxLen]))
					wRunes = wRunes[maxLen:]
				}
				currentLine = string(wRunes)
				currentLen = len(wRunes)
			}
		}
		if currentLen > 0 {
			result = append(result, currentLine)
		}
	}
	return result, nil
}

// ParseMarkdown 解析 Markdown 文本，返回剥离标记后的纯文本与 UTF-16 精确偏移的 tg.MessageEntityClass 切片。
func ParseMarkdown(text string) (string, []tg.MessageEntityClass) {
	plain, spans := ParseMarkdownSpans(text)
	return plain, EntitiesToTG(spans)
}

// ParseMarkdownSpans 解析 Markdown 文本，支持：
// **bold**, __bold__, *italic*, _italic_, `code`, ```lang\ncode```, ~~strike~~, ||spoiler||, [text](url)。
func ParseMarkdownSpans(text string) (string, []EntitySpan) {
	if text == "" {
		return "", nil
	}
	var out strings.Builder
	out.Grow(len(text))
	var spans []EntitySpan
	parseMarkdownSegment(text, 0, &out, &spans)
	sortEntitySpans(spans)
	return out.String(), spans
}

func parseMarkdownSegment(src string, baseUTF16 int, out *strings.Builder, spans *[]EntitySpan) int {
	curUTF16 := baseUTF16
	i := 0
	n := len(src)

	for i < n {
		// 1. 代码块 ```lang\ncode```
		if strings.HasPrefix(src[i:], "```") {
			if endRel := strings.Index(src[i+3:], "```"); endRel >= 0 {
				rawBlock := src[i+3 : i+3+endRel]
				lang := ""
				codeBody := rawBlock
				if nl := strings.IndexByte(rawBlock, '\n'); nl >= 0 {
					firstLine := strings.TrimRight(rawBlock[:nl], "\r")
					if isValidCodeFenceLang(firstLine) {
						lang = strings.TrimSpace(firstLine)
						codeBody = rawBlock[nl+1:]
					}
				}
				bodyLen := UTF16Len(codeBody)
				if bodyLen > 0 {
					out.WriteString(codeBody)
					*spans = append(*spans, EntitySpan{
						Kind:     "pre",
						Offset:   curUTF16,
						Length:   bodyLen,
						Language: lang,
					})
					curUTF16 += bodyLen
				}
				i += 3 + endRel + 3
				continue
			}
		}

		// 2. 内联代码 `code`
		if src[i] == '`' {
			if endRel := strings.IndexByte(src[i+1:], '`'); endRel > 0 {
				codeBody := src[i+1 : i+1+endRel]
				bodyLen := UTF16Len(codeBody)
				out.WriteString(codeBody)
				*spans = append(*spans, EntitySpan{
					Kind:   "code",
					Offset: curUTF16,
					Length: bodyLen,
				})
				curUTF16 += bodyLen
				i += 1 + endRel + 1
				continue
			}
		}

		// 3. 链接 [text](url)
		if src[i] == '[' {
			if labelEnd, urlStr, totalConsumed, ok := matchMarkdownLinkAt(src[i:]); ok && labelEnd > 1 {
				labelContent := src[i+1 : i+labelEnd]
				startOffset := curUTF16
				curUTF16 = parseMarkdownSegment(labelContent, curUTF16, out, spans)
				spanLen := curUTF16 - startOffset
				if spanLen > 0 && urlStr != "" {
					if uid, okUID := parseTGUserURL(urlStr); okUID {
						*spans = append(*spans, EntitySpan{
							Kind:   "text_mention",
							Offset: startOffset,
							Length: spanLen,
							URL:    urlStr,
							UserID: uid,
						})
					} else {
						*spans = append(*spans, EntitySpan{
							Kind:   "text_url",
							Offset: startOffset,
							Length: spanLen,
							URL:    urlStr,
						})
					}
				}
				i += totalConsumed
				continue
			}
		}

		// 4. 双字符行内标记：**bold**, __bold__, ~~strike~~, ||spoiler||
		if kind, delim := matchDoubleDelimiter(src[i:]); kind != "" {
			if endRel := findClosingDelim(src[i+len(delim):], delim); endRel > 0 {
				inner := src[i+len(delim) : i+len(delim)+endRel]
				startOffset := curUTF16
				curUTF16 = parseMarkdownSegment(inner, curUTF16, out, spans)
				if curUTF16 > startOffset {
					*spans = append(*spans, EntitySpan{
						Kind:   kind,
						Offset: startOffset,
						Length: curUTF16 - startOffset,
					})
				}
				i += len(delim) + endRel + len(delim)
				continue
			}
		}

		// 5. 单星号斜体 *italic*（避免将 ** 当作空斜体）
		if src[i] == '*' && !strings.HasPrefix(src[i:], "**") {
			if endRel := findSingleStarClose(src[i+1:]); endRel > 0 {
				inner := src[i+1 : i+1+endRel]
				startOffset := curUTF16
				curUTF16 = parseMarkdownSegment(inner, curUTF16, out, spans)
				if curUTF16 > startOffset {
					*spans = append(*spans, EntitySpan{
						Kind:   "italic",
						Offset: startOffset,
						Length: curUTF16 - startOffset,
					})
				}
				i += 1 + endRel + 1
				continue
			}
		}

		// 6. 单下划线斜体 _italic_（保护词内 snake_case 如 foo_bar_baz 不被误拆）
		if src[i] == '_' && !strings.HasPrefix(src[i:], "__") {
			var prevRune rune
			if i > 0 {
				prevRune, _ = utf8.DecodeLastRuneInString(src[:i])
			}
			var nextRune rune
			if i+1 < n {
				nextRune, _ = utf8.DecodeRuneInString(src[i+1:])
			}
			if !(isASCIIWordRune(prevRune) && isASCIIWordRune(nextRune)) {
				if endRel := findSingleUnderscoreClose(src[i+1:]); endRel > 0 {
					inner := src[i+1 : i+1+endRel]
					startOffset := curUTF16
					curUTF16 = parseMarkdownSegment(inner, curUTF16, out, spans)
					if curUTF16 > startOffset {
						*spans = append(*spans, EntitySpan{
							Kind:   "italic",
							Offset: startOffset,
							Length: curUTF16 - startOffset,
						})
					}
					i += 1 + endRel + 1
					continue
				}
			}
		}

		r, width := utf8.DecodeRuneInString(src[i:])
		out.WriteRune(r)
		curUTF16 += utf16RuneLen(r)
		i += width
	}
	return curUTF16
}

func isValidCodeFenceLang(s string) bool {
	if s == "" {
		return true
	}
	if len(s) > 32 {
		return false
	}
	for _, r := range s {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
			r == '_' || r == '+' || r == '#' || r == '.' || r == '-') {
			return false
		}
	}
	return true
}

func matchMarkdownLinkAt(s string) (labelEnd int, urlStr string, consumed int, ok bool) {
	if len(s) < 4 || s[0] != '[' {
		return 0, "", 0, false
	}
	depth := 0
	closeBracket := -1
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				closeBracket = i
				i = len(s)
			}
		}
	}
	if closeBracket <= 0 || closeBracket+1 >= len(s) || s[closeBracket+1] != '(' {
		return 0, "", 0, false
	}
	parenStart := closeBracket + 2
	parenDepth := 1
	closeParen := -1
	for i := parenStart; i < len(s); i++ {
		if s[i] == '\n' {
			return 0, "", 0, false
		}
		switch s[i] {
		case '(':
			parenDepth++
		case ')':
			parenDepth--
			if parenDepth == 0 {
				closeParen = i
				i = len(s)
			}
		}
	}
	if closeParen < parenStart {
		return 0, "", 0, false
	}
	return closeBracket, strings.TrimSpace(s[parenStart:closeParen]), closeParen + 1, true
}

func matchDoubleDelimiter(s string) (kind, delim string) {
	switch {
	case strings.HasPrefix(s, "**"):
		return "bold", "**"
	case strings.HasPrefix(s, "__"):
		return "bold", "__"
	case strings.HasPrefix(s, "~~"):
		return "strike", "~~"
	case strings.HasPrefix(s, "||"):
		return "spoiler", "||"
	default:
		return "", ""
	}
}

func findClosingDelim(s, delim string) int {
	for i := 0; i <= len(s)-len(delim); i++ {
		if s[i] == '`' {
			if next := strings.IndexByte(s[i+1:], '`'); next >= 0 {
				i += 1 + next
				continue
			}
		}
		if strings.HasPrefix(s[i:], delim) {
			return i
		}
	}
	return -1
}

func findSingleStarClose(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] == '`' {
			if next := strings.IndexByte(s[i+1:], '`'); next >= 0 {
				i += 1 + next
				continue
			}
		}
		if strings.HasPrefix(s[i:], "**") {
			i++
			continue
		}
		if s[i] == '*' {
			return i
		}
	}
	return -1
}

func findSingleUnderscoreClose(s string) int {
	for i := 0; i < len(s); {
		if s[i] == '`' {
			if next := strings.IndexByte(s[i+1:], '`'); next >= 0 {
				i += 2 + next
				continue
			}
		}
		if strings.HasPrefix(s[i:], "__") {
			i += 2
			continue
		}
		if s[i] == '_' {
			var prevRune, nextRune rune
			if i > 0 {
				prevRune, _ = utf8.DecodeLastRuneInString(s[:i])
			}
			if i+1 < len(s) {
				nextRune, _ = utf8.DecodeRuneInString(s[i+1:])
			}
			if !(isASCIIWordRune(prevRune) && isASCIIWordRune(nextRune)) {
				return i
			}
		}
		_, w := utf8.DecodeRuneInString(s[i:])
		i += w
	}
	return -1
}

func parseTGUserURL(u string) (int64, bool) {
	const prefix = "tg://user?id="
	if !strings.HasPrefix(strings.ToLower(u), prefix) {
		return 0, false
	}
	idPart := u[len(prefix):]
	if amp := strings.IndexByte(idPart, '&'); amp >= 0 {
		idPart = idPart[:amp]
	}
	uid, err := strconv.ParseInt(strings.TrimSpace(idPart), 10, 64)
	if err != nil || uid <= 0 {
		return 0, false
	}
	return uid, true
}

// ParseHTML 解析 Telegram HTML 文本（<b>, <i>, <code>, <pre>, <a href="...">, <s>, <u> 等），
// 返回反转义后的纯文本与精确 UTF-16 偏移的 tg.MessageEntityClass 切片。
func ParseHTML(htmlText string) (string, []tg.MessageEntityClass) {
	plain, spans := ParseHTMLSpans(htmlText)
	return plain, EntitiesToTG(spans)
}

type htmlFrame struct {
	tag        string
	kind       string
	offset     int
	url        string
	language   string
	userID     int64
	mergedCode bool
}

// ParseHTMLSpans 解析 HTML 富文本并返回纯文本与 EntitySpan 列表。
func ParseHTMLSpans(htmlText string) (string, []EntitySpan) {
	if htmlText == "" {
		return "", nil
	}

	z := html.NewTokenizer(strings.NewReader(htmlText))
	var (
		out      strings.Builder
		curUTF16 int
		stack    []htmlFrame
		spans    []EntitySpan
	)
	out.Grow(len(htmlText))

	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			if errors.Is(z.Err(), io.EOF) {
				break
			}
			break
		}

		switch tt {
		case html.TextToken:
			txt := html.UnescapeString(string(z.Raw()))
			if txt != "" {
				out.WriteString(txt)
				curUTF16 += UTF16Len(txt)
			}

		case html.SelfClosingTagToken:
			tn, _ := z.TagName()
			if strings.EqualFold(string(tn), "br") {
				out.WriteByte('\n')
				curUTF16++
			}

		case html.StartTagToken:
			tn, hasAttr := z.TagName()
			tag := strings.ToLower(string(tn))
			if tag == "br" {
				out.WriteByte('\n')
				curUTF16++
				continue
			}
			attrs := make(map[string]string)
			for hasAttr {
				var k, v []byte
				k, v, hasAttr = z.TagAttr()
				attrs[strings.ToLower(string(k))] = html.UnescapeString(string(v))
			}

			frame := htmlFrame{tag: tag, offset: curUTF16}
			switch tag {
			case "b", "strong":
				frame.kind = "bold"
			case "i", "em":
				frame.kind = "italic"
			case "u", "ins":
				frame.kind = "underline"
			case "s", "strike", "del":
				frame.kind = "strike"
			case "tg-spoiler":
				frame.kind = "spoiler"
			case "span":
				if strings.Contains(attrs["class"], "tg-spoiler") {
					frame.kind = "spoiler"
				}
			case "blockquote":
				frame.kind = "blockquote"
			case "pre":
				frame.kind = "pre"
				frame.language = strings.TrimSpace(attrs["language"])
			case "code":
				lang := extractLanguageClass(attrs["class"])
				if len(stack) > 0 && stack[len(stack)-1].kind == "pre" && stack[len(stack)-1].offset == curUTF16 {
					if lang != "" && stack[len(stack)-1].language == "" {
						stack[len(stack)-1].language = lang
					}
					frame.mergedCode = true
				} else {
					frame.kind = "code"
					frame.language = lang
				}
			case "a":
				href := strings.TrimSpace(attrs["href"])
				if href != "" {
					if uid, ok := parseTGUserURL(href); ok {
						frame.kind = "text_mention"
						frame.url = href
						frame.userID = uid
					} else {
						frame.kind = "text_url"
						frame.url = href
					}
				}
			}
			stack = append(stack, frame)

		case html.EndTagToken:
			tn, _ := z.TagName()
			tag := strings.ToLower(string(tn))
			idx := -1
			for i := len(stack) - 1; i >= 0; i-- {
				if stack[i].tag == tag {
					idx = i
					break
				}
			}
			if idx < 0 {
				continue
			}
			frame := stack[idx]
			stack = append(stack[:idx], stack[idx+1:]...)
			length := curUTF16 - frame.offset
			if !frame.mergedCode && frame.kind != "" && length > 0 {
				spans = append(spans, EntitySpan{
					Kind:     frame.kind,
					Offset:   frame.offset,
					Length:   length,
					URL:      frame.url,
					Language: frame.language,
					UserID:   frame.userID,
				})
			}
		}
	}

	sortEntitySpans(spans)
	return out.String(), spans
}

func extractLanguageClass(cls string) string {
	for _, field := range strings.Fields(cls) {
		if strings.HasPrefix(field, "language-") {
			return strings.TrimPrefix(field, "language-")
		}
	}
	return ""
}

func sortEntitySpans(spans []EntitySpan) {
	sort.SliceStable(spans, func(i, j int) bool {
		if spans[i].Offset != spans[j].Offset {
			return spans[i].Offset < spans[j].Offset
		}
		return spans[i].Length > spans[j].Length
	})
}

// EntitiesToTG 将标准化 EntitySpan 切片转换为 gotd/td 的 []tg.MessageEntityClass。
func EntitiesToTG(spans []EntitySpan) []tg.MessageEntityClass {
	return entitiesToTGWithResolver(spans, nil)
}

func entitiesToTGWithResolver(spans []EntitySpan, resolveUser func(userID int64) tg.InputUserClass) []tg.MessageEntityClass {
	if len(spans) == 0 {
		return nil
	}
	out := make([]tg.MessageEntityClass, 0, len(spans))
	for _, sp := range spans {
		if sp.Length <= 0 || sp.Offset < 0 {
			continue
		}
		switch sp.Kind {
		case "bold":
			out = append(out, &tg.MessageEntityBold{Offset: sp.Offset, Length: sp.Length})
		case "italic":
			out = append(out, &tg.MessageEntityItalic{Offset: sp.Offset, Length: sp.Length})
		case "code":
			out = append(out, &tg.MessageEntityCode{Offset: sp.Offset, Length: sp.Length})
		case "pre":
			out = append(out, &tg.MessageEntityPre{Offset: sp.Offset, Length: sp.Length, Language: sp.Language})
		case "text_url":
			out = append(out, &tg.MessageEntityTextURL{Offset: sp.Offset, Length: sp.Length, URL: sp.URL})
		case "url":
			out = append(out, &tg.MessageEntityURL{Offset: sp.Offset, Length: sp.Length})
		case "strike":
			out = append(out, &tg.MessageEntityStrike{Offset: sp.Offset, Length: sp.Length})
		case "underline":
			out = append(out, &tg.MessageEntityUnderline{Offset: sp.Offset, Length: sp.Length})
		case "spoiler":
			out = append(out, &tg.MessageEntitySpoiler{Offset: sp.Offset, Length: sp.Length})
		case "mention":
			out = append(out, &tg.MessageEntityMention{Offset: sp.Offset, Length: sp.Length})
		case "blockquote":
			out = append(out, &tg.MessageEntityBlockquote{Offset: sp.Offset, Length: sp.Length})
		case "text_mention":
			if sp.UserID > 0 {
				var iu tg.InputUserClass = &tg.InputUser{UserID: sp.UserID, AccessHash: 0}
				if resolveUser != nil {
					if resolved := resolveUser(sp.UserID); resolved != nil {
						iu = resolved
					}
				}
				out = append(out, &tg.InputMessageEntityMentionName{
					Offset: sp.Offset,
					Length: sp.Length,
					UserID: iu,
				})
			} else if sp.URL != "" {
				out = append(out, &tg.MessageEntityTextURL{Offset: sp.Offset, Length: sp.Length, URL: sp.URL})
			}
		}
	}
	return out
}

// EntitiesFromTG 将 gotd/td 的 []tg.MessageEntityClass 反向转换为标准化 []EntitySpan。
func EntitiesFromTG(entities []tg.MessageEntityClass) []EntitySpan {
	if len(entities) == 0 {
		return nil
	}
	out := make([]EntitySpan, 0, len(entities))
	for _, ent := range entities {
		if ent == nil {
			continue
		}
		switch v := ent.(type) {
		case *tg.MessageEntityBold:
			out = append(out, EntitySpan{Kind: "bold", Offset: v.Offset, Length: v.Length})
		case *tg.MessageEntityItalic:
			out = append(out, EntitySpan{Kind: "italic", Offset: v.Offset, Length: v.Length})
		case *tg.MessageEntityCode:
			out = append(out, EntitySpan{Kind: "code", Offset: v.Offset, Length: v.Length})
		case *tg.MessageEntityPre:
			out = append(out, EntitySpan{Kind: "pre", Offset: v.Offset, Length: v.Length, Language: v.Language})
		case *tg.MessageEntityTextURL:
			out = append(out, EntitySpan{Kind: "text_url", Offset: v.Offset, Length: v.Length, URL: v.URL})
		case *tg.MessageEntityURL:
			out = append(out, EntitySpan{Kind: "url", Offset: v.Offset, Length: v.Length})
		case *tg.MessageEntityStrike:
			out = append(out, EntitySpan{Kind: "strike", Offset: v.Offset, Length: v.Length})
		case *tg.MessageEntityUnderline:
			out = append(out, EntitySpan{Kind: "underline", Offset: v.Offset, Length: v.Length})
		case *tg.MessageEntitySpoiler:
			out = append(out, EntitySpan{Kind: "spoiler", Offset: v.Offset, Length: v.Length})
		case *tg.MessageEntityMention:
			out = append(out, EntitySpan{Kind: "mention", Offset: v.Offset, Length: v.Length})
		case *tg.MessageEntityBlockquote:
			out = append(out, EntitySpan{Kind: "blockquote", Offset: v.Offset, Length: v.Length})
		case *tg.MessageEntityMentionName:
			out = append(out, EntitySpan{
				Kind:   "text_mention",
				Offset: v.Offset,
				Length: v.Length,
				URL:    fmt.Sprintf("tg://user?id=%d", v.UserID),
				UserID: v.UserID,
			})
		case *tg.InputMessageEntityMentionName:
			var uid int64
			if iu, ok := v.UserID.(*tg.InputUser); ok {
				uid = iu.UserID
			}
			out = append(out, EntitySpan{
				Kind:   "text_mention",
				Offset: v.Offset,
				Length: v.Length,
				URL:    fmt.Sprintf("tg://user?id=%d", uid),
				UserID: uid,
			})
		}
	}
	return out
}
