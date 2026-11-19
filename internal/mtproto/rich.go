// Copyright 2026 Sakura-Bot
// SPDX-License-Identifier: AGPL-3.0-or-later

package mtproto

import (
	"context"
	"slices"
	"strings"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

func (c *Client) inputRichMessage(ctx context.Context, text string, opts SendOptions) tg.InputRichMessageClass {
	if len(opts.Entities) != 0 {
		return c.explicitRichMessage(ctx, text, opts.Entities)
	}
	switch strings.ToLower(strings.TrimSpace(opts.ParseMode)) {
	case "markdown", "md":
		return &tg.InputRichMessageMarkdown{Markdown: text}
	case "html":
		// Legacy HTML strings use literal newlines. Converting them locally
		// preserves the card layout without HTML whitespace normalization.
		plain, spans := ParseHTMLSpans(text)
		return c.explicitRichMessage(ctx, plain, spans)
	default:
		return plainRichMessage(text)
	}
}

func plainRichMessage(text string) *tg.InputRichMessage {
	return &tg.InputRichMessage{Blocks: []tg.PageBlockClass{&tg.PageBlockParagraph{Text: &tg.TextPlain{Text: text}}}}
}

func isRichMarkupMode(opts SendOptions) bool {
	if len(opts.Entities) != 0 {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(opts.ParseMode)) {
	case "markdown", "md", "html":
		return true
	}
	return false
}

func isRichFormattingError(err error) bool {
	return tgerr.Is(err, "RICH_MESSAGE_INVALID", "RICH_TEXT_INVALID", "RICH_MESSAGE_PARSE_FAILED",
		"RICH_MESSAGE_MARKDOWN_INVALID", "RICH_MESSAGE_HTML_INVALID", "MESSAGE_EMPTY")
}

func richMarkupPlainText(text, mode string) string {
	if strings.EqualFold(strings.TrimSpace(mode), "html") {
		plain, _ := ParseHTMLSpans(text)
		return plain
	}
	plain, _ := ParseMarkdownSpans(text)
	return plain
}

// Explicit spans are counted in UTF-16 even though the native rich text nodes
// store strings. Ignore invalid spans rather than slicing through a surrogate.
func (c *Client) explicitRichMessage(ctx context.Context, text string, spans []EntitySpan) *tg.InputRichMessage {
	byteAt := map[int]int{0: 0}
	units := 0
	for start, char := range text {
		byteAt[units] = start
		units++
		if char > 0xffff {
			units++
		}
	}
	byteAt[units] = len(text)
	points := []int{0, units}
	valid := make([]EntitySpan, 0, len(spans))
	for _, span := range spans {
		if span.Offset < 0 || span.Offset > units || span.Length <= 0 || span.Length > units-span.Offset {
			continue
		}
		end := span.Offset + span.Length
		_, startOK := byteAt[span.Offset]
		_, endOK := byteAt[end]
		if startOK && endOK {
			points = append(points, span.Offset, end)
			valid = append(valid, span)
		}
	}
	slices.Sort(points)
	points = slices.Compact(points)
	message := &tg.InputRichMessage{}
	users := make(map[int64]tg.InputUserClass)
	appendBlock := func(kind, language string, fragments []tg.RichTextClass) {
		if len(fragments) == 0 {
			return
		}
		formatted := fragments[0]
		if len(fragments) > 1 {
			formatted = &tg.TextConcat{Texts: fragments}
		}
		switch kind {
		case "pre":
			message.Blocks = append(message.Blocks, &tg.PageBlockPreformatted{Text: formatted, Language: language})
		case "blockquote":
			message.Blocks = append(message.Blocks, &tg.PageBlockBlockquote{Text: formatted, Caption: &tg.TextEmpty{}})
		default:
			message.Blocks = append(message.Blocks, &tg.PageBlockParagraph{Text: formatted})
		}
	}
	var fragments []tg.RichTextClass
	blockKind, blockLanguage := "", ""
	for i := 1; i < len(points); i++ {
		start, end := points[i-1], points[i]
		part := text[byteAt[start]:byteAt[end]]
		var node tg.RichTextClass = &tg.TextPlain{Text: part}
		kind, language := "", ""
		for _, span := range valid {
			if span.Offset > start || span.Offset+span.Length < end {
				continue
			}
			switch span.Kind {
			case "bold":
				node = &tg.TextBold{Text: node}
			case "italic":
				node = &tg.TextItalic{Text: node}
			case "underline":
				node = &tg.TextUnderline{Text: node}
			case "strike":
				node = &tg.TextStrike{Text: node}
			case "spoiler":
				node = &tg.TextSpoiler{Text: node}
			case "code":
				node = &tg.TextFixed{Text: node}
			case "text_url":
				node = &tg.TextURL{Text: node, URL: span.URL}
			case "url":
				node = &tg.TextURL{Text: node, URL: text[byteAt[span.Offset]:byteAt[span.Offset+span.Length]]}
			case "mention":
				node = &tg.TextURL{Text: node, URL: "https://t.me/" + strings.TrimPrefix(text[byteAt[span.Offset]:byteAt[span.Offset+span.Length]], "@")}
			case "text_mention":
				if _, known := users[span.UserID]; !known {
					user, err := c.Peers().ResolveInputUser(ctx, nil, span.UserID)
					if err == nil && user != nil {
						users[span.UserID] = user
						message.Users = append(message.Users, user)
					}
				}
				if users[span.UserID] != nil {
					node = &tg.TextMentionName{Text: node, UserID: span.UserID}
				}
			case "pre", "blockquote":
				kind, language = span.Kind, span.Language
			}
		}
		if kind != blockKind || language != blockLanguage {
			appendBlock(blockKind, blockLanguage, fragments)
			fragments = nil
			blockKind, blockLanguage = kind, language
		}
		fragments = append(fragments, node)
	}
	appendBlock(blockKind, blockLanguage, fragments)
	if len(message.Blocks) == 0 {
		return plainRichMessage(text)
	}
	return message
}
