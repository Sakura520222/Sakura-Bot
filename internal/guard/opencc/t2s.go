// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

// Package opencc 实现与 Python OpenCC("t2s") 逐字对齐的纯 Go 繁转简转换器。
//
// 依据 OpenCC t2s.json 定义：
//   - 分词：基于 TSPhrases.txt 的正向最大匹配（mmseg）
//   - 转换链：单级 GroupDict [TSPhrases.txt, TSCharacters.txt]，多候选时取首项
package opencc

import (
	_ "embed"
	"strings"
	"sync"
	"unicode/utf8"
)

//go:embed TSPhrases.txt
var tsPhrasesRaw string

//go:embed TSCharacters.txt
var tsCharactersRaw string

type trieNode struct {
	children map[rune]*trieNode
	value    string
	hasVal   bool
}

type converter struct {
	phrases *trieNode
	chars   map[rune]string
}

var (
	t2sOnce sync.Once
	t2sInst *converter
)

func getT2S() *converter {
	t2sOnce.Do(func() {
		c := &converter{
			phrases: &trieNode{children: make(map[rune]*trieNode)},
			chars:   make(map[rune]string, 4096),
		}
		loadDict(tsPhrasesRaw, func(src, dst string) {
			cur := c.phrases
			for _, r := range src {
				if cur.children == nil {
					cur.children = make(map[rune]*trieNode)
				}
				next := cur.children[r]
				if next == nil {
					next = &trieNode{}
					cur.children[r] = next
				}
				cur = next
			}
			if !cur.hasVal {
				cur.value = dst
				cur.hasVal = true
			}
		})
		loadDict(tsCharactersRaw, func(src, dst string) {
			r, sz := utf8.DecodeRuneInString(src)
			if sz == len(src) && r != utf8.RuneError {
				if _, exists := c.chars[r]; !exists {
					c.chars[r] = dst
				}
			} else {
				// 多码点条目兜底放入前缀树
				cur := c.phrases
				for _, ch := range src {
					if cur.children == nil {
						cur.children = make(map[rune]*trieNode)
					}
					next := cur.children[ch]
					if next == nil {
						next = &trieNode{}
						cur.children[ch] = next
					}
					cur = next
				}
				if !cur.hasVal {
					cur.value = dst
					cur.hasVal = true
				}
			}
		})
		t2sInst = c
	})
	return t2sInst
}

func loadDict(raw string, add func(src, dst string)) {
	for line := range strings.SplitSeq(raw, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		tab := strings.IndexByte(line, '\t')
		if tab <= 0 {
			continue
		}
		src := line[:tab]
		rest := strings.TrimSpace(line[tab+1:])
		if rest == "" {
			continue
		}
		dst := rest
		if sp := strings.IndexByte(rest, ' '); sp > 0 {
			dst = rest[:sp]
		}
		add(src, dst)
	}
}

// T2S 将繁体中文转换为简体中文（与 OpenCC("t2s").convert 等价）。
func T2S(s string) string {
	if s == "" {
		return ""
	}
	// 快速路径：纯 ASCII 直接原样返回，零分配。
	hasNonASCII := false
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			hasNonASCII = true
			break
		}
	}
	if !hasNonASCII {
		return s
	}

	c := getT2S()
	var b strings.Builder
	b.Grow(len(s))

	for i := 0; i < len(s); {
		r, width := utf8.DecodeRuneInString(s[i:])
		if r < utf8.RuneSelf {
			b.WriteByte(s[i])
			i++
			continue
		}

		// 正向最大匹配词组前缀树
		if next := c.phrases.children[r]; next != nil {
			bestEnd := -1
			bestVal := ""
			if next.hasVal {
				bestEnd = i + width
				bestVal = next.value
			}
			cur := next
			for j := i + width; j < len(s); {
				rj, wj := utf8.DecodeRuneInString(s[j:])
				child := cur.children[rj]
				if child == nil {
					break
				}
				j += wj
				cur = child
				if cur.hasVal {
					bestEnd = j
					bestVal = cur.value
				}
			}
			if bestEnd > i {
				b.WriteString(bestVal)
				i = bestEnd
				continue
			}
		}

		if mapped, ok := c.chars[r]; ok {
			b.WriteString(mapped)
		} else {
			b.WriteRune(r)
		}
		i += width
	}
	return b.String()
}

// HasTraditional 判断文本经 T2S 转换后是否发生变化（用于 ban_any_traditional 判定）。
func HasTraditional(s string) bool {
	return T2S(s) != s
}
