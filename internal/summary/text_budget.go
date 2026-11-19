// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package summary

import (
	"fmt"
	"unicode/utf8"
)

// TokenUpperBound 返回保守的 Token 预算上界：UTF-8 字节数是 byte-level tokenizer 的严格上界（对齐 text_budget.py:token_upper_bound）。
func TokenUpperBound(text string) int {
	return len(text)
}

// SplitText 按 UTF-8 字节预算切分文本，绝不拆坏 Unicode 码点，不打乱顺序（对齐 text_budget.py:split_text）。
func SplitText(text string, budget int) ([]string, error) {
	if budget < 4 {
		return nil, fmt.Errorf("Token budget must be at least 4")
	}
	if text == "" {
		return []string{""}, nil
	}
	var chunks []string
	start := 0
	size := 0
	for idx, r := range text {
		w := utf8.RuneLen(r)
		if w < 0 {
			w = 1
		}
		if size+w > budget {
			chunks = append(chunks, text[start:idx])
			start = idx
			size = 0
		}
		size += w
	}
	if start < len(text) {
		chunks = append(chunks, text[start:])
	}
	if len(chunks) == 0 {
		return []string{""}, nil
	}
	return chunks, nil
}

// PackTexts 将多条文本合并为不超过预算的分组；单条超长会先按 SplitText 拆开，保留全部片段（对齐 text_budget.py:pack_texts）。
func PackTexts(texts []string, budget int) ([]string, error) {
	if budget < 4 {
		return nil, fmt.Errorf("Token budget must be at least 4")
	}
	var groups []string
	current := ""
	for _, text := range texts {
		parts, err := SplitText(text, budget)
		if err != nil {
			return nil, err
		}
		for _, part := range parts {
			candidate := part
			if current != "" {
				candidate = current + "\n\n" + part
			}
			if current != "" && TokenUpperBound(candidate) > budget {
				groups = append(groups, current)
				current = part
			} else {
				current = candidate
			}
		}
	}
	if current != "" {
		groups = append(groups, current)
	}
	return groups, nil
}
