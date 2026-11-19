// Copyright 2026 Sakura-Bot
// SPDX-License-Identifier: AGPL-3.0-or-later

package mtproto

import (
	"slices"
	"strconv"
	"strings"
)

// StripEmoji removes emoji sequences while retaining ordinary digits, # and *.
func StripEmoji(text string) string {
	cleaned, _ := stripEmojiSpans(text, nil)
	return cleaned
}

func stripEmojiSpans(text string, spans []EntitySpan) (string, []EntitySpan) {
	if text == "" {
		return text, spans
	}
	chars := []rune(text)
	var positions []int
	if len(spans) > 0 {
		positions = []int{0}
	}
	var cleaned strings.Builder
	cleaned.Grow(len(text))
	outUnits, skipUntil := 0, 0
	for i, char := range chars {
		if i >= skipUntil {
			skipUntil = i + emojiSequenceLength(chars[i:])
		}
		keep := i >= skipUntil
		if keep {
			cleaned.WriteRune(char)
		}
		units := 1
		if char > 0xffff {
			units = 2
		}
		for range units {
			if keep {
				outUnits++
			}
			if positions != nil {
				positions = append(positions, outUnits)
			}
		}
	}
	if cleaned.Len() == len(text) {
		return text, spans
	}
	kept := make([]EntitySpan, 0, len(spans))
	for _, span := range spans {
		if span.Offset < 0 || span.Offset >= len(positions) || span.Length <= 0 ||
			span.Length >= len(positions)-span.Offset || span.Kind == "custom_emoji" {
			continue
		}
		start, end := positions[span.Offset], positions[span.Offset+span.Length]
		if end > start {
			span.Offset, span.Length = start, end-start
			kept = append(kept, span)
		}
	}
	return cleaned.String(), kept
}

func emojiSequenceLength(chars []rune) int {
	if char := chars[0]; char >= '0' && char <= '9' || char == '#' || char == '*' {
		end := 1
		if len(chars) > end && chars[end] == '\ufe0f' {
			end++
		}
		if len(chars) > end && chars[end] == '\u20e3' {
			return end + 1
		}
		return 0
	}
	if !isEmojiRune(chars[0]) && chars[0] != '\ufe0f' {
		return 0
	}
	end := 1
	for end < len(chars) {
		char := chars[end]
		switch {
		case char == '\ufe0f' || char == '\ufe0e' || char >= 0x1f3fb && char <= 0x1f3ff ||
			char >= 0xe0020 && char <= 0xe007f:
			end++
		case char == '\u200d' && end+1 < len(chars) && isEmojiRune(chars[end+1]):
			end += 2
		default:
			return end
		}
	}
	return end
}

func isEmojiRune(char rune) bool {
	_, found := slices.BinarySearchFunc(emojiRanges[:], char, func(bounds [2]rune, target rune) int {
		switch {
		case bounds[1] < target:
			return -1
		case bounds[0] > target:
			return 1
		default:
			return 0
		}
	})
	return found
}

func (c *Client) cleanButtons(buttons [][]InlineButton) [][]InlineButton {
	if !c.noEmoji || len(buttons) == 0 {
		return buttons
	}
	rows := make([][]InlineButton, len(buttons))
	for i, row := range buttons {
		rows[i] = slices.Clone(row)
		for j := range rows[i] {
			label := strings.TrimSpace(StripEmoji(rows[i][j].Text))
			if label == "" {
				label = strconv.Itoa(j + 1)
			}
			rows[i][j].Text = label
		}
	}
	return rows
}

func cleanMessageEmoji(text string, opts SendOptions) (string, SendOptions) {
	if text != "" {
		text, opts.Entities = stripEmojiSpans(text, opts.Entities)
		if strings.TrimSpace(text) == "" {
			text = "—"
		}
	}
	return text, opts
}
