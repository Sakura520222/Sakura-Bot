package tools

import (
	"context"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/contracts"
)

func (e *ToolExecutor) executeWiki(ctx context.Context, name string, args map[string]any) (string, error) {
	if e.wiki == nil {
		return marshalJSON(map[string]any{"available": false, "fallback": "Use semantic_search, keyword_search and get_source_detail"}), nil
	}
	size, err := argInt(args, "page_size", 20)
	if err != nil {
		return "", err
	}
	page, err := argInt(args, "page", 1)
	if err != nil {
		return "", err
	}
	result, err := e.wiki.ReadWiki(ctx, name, contracts.WikiOptions{Query: optArgString(args, "query"), Channel: optArgString(args, "channel_id"), Slug: optArgString(args, "slug"), NodeID: optArgString(args, "node_id"), Kind: optArgString(args, "kind"), After: optArgString(args, "date_after"), Before: optArgString(args, "date_before"), Page: page, PageSize: size})
	if err != nil {
		return marshalJSON(map[string]any{"available": false, "error": "Wiki 暂不可用或内容不可公开，请使用原有 RAG 工具核实。"}), nil
	}
	return marshalJSON(result), nil
}
