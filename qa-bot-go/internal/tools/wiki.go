package tools

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"

	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/contracts"
)

func (e *ToolExecutor) executeWiki(ctx context.Context, name string, args map[string]any) (string, error) {
	if e.wiki == nil {
		return marshalJSON(map[string]any{"available": false, "status": "error", "error_code": "wiki_not_initialized", "error": "Wiki 读取组件未初始化，无法判断知识是否存在。"}), nil
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
		if errors.Is(err, sql.ErrNoRows) {
			return marshalJSON(map[string]any{"available": false, "status": "not_found", "error_code": "wiki_not_found", "error": "请求的 Wiki 页面不存在或已撤回，请先使用 wiki_search 查找当前条目。"}), nil
		}
		slog.Error("Wiki 工具读取失败", "tool", name, "error", err)
		return marshalJSON(map[string]any{"available": false, "status": "error", "error_code": "wiki_read_failed", "data_status": "unknown", "error": "Wiki 查询执行失败，不能据此判断为没有已发布知识。可重试或使用消息检索。"}), nil
	}
	return marshalJSON(result), nil
}
