// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
)

// ToolExecutor 工具执行器：将 LLM 的 tool_call 请求路由到实际的检索组件
// （对齐 agent_tools.py:283-335）。每个请求独立实例（qa_engine_v3.py:300），
// 结果存储请求内自增 result_id；非并发安全，由单请求串行循环独占使用
// （README 硬规则 1 的串行分发模型）。
//
// 已知类别分叉（对拍审计 A-5，模型畸形参数下的边缘输入）：Python 把显式
// null 参数原样传入内层，由内层类型错误/短路收敛；Go 在参数提取处将
// null 归一为 Go 零值（semantic_search 的 query=null → "" 继续检索、
// keyword_search 的 time_range_days=null → 即刻 TypeError、结果 metadata 非
// dict 视为缺失）。两侧最终都以错误或空结果收敛，仅中间错误文案不同。
type ToolExecutor struct {
	vectorStore VectorStore
	memoryMgr   MemoryManager
	reranker    Reranker
	resultStore *resultStore
}

// NewToolExecutor 创建每请求隔离的工具执行上下文。
func NewToolExecutor(vs VectorStore, mm MemoryManager, rr Reranker) *ToolExecutor {
	return &ToolExecutor{
		vectorStore: vs,
		memoryMgr:   mm,
		reranker:    rr,
		resultStore: newResultStore(),
	}
}

// Reset 重置结果存储（对齐 ToolExecutor.reset，agent_tools.py:298-303）。
func (e *ToolExecutor) Reset() { e.resultStore.reset() }

// GetAllResults 获取全部累积结果（完整文本、不截断）。
func (e *ToolExecutor) GetAllResults() []map[string]any { return e.resultStore.getAllResults() }

// GetResultsByIDs 按 ID 列表取结果（保持入参顺序，跳过缺失 ID）。
func (e *ToolExecutor) GetResultsByIDs(ids []int) []map[string]any {
	return e.resultStore.getResultsByIDs(ids)
}

// ExecuteJSON 是带参数 JSON 解析的执行入口，对齐引擎循环里的调用前解析
// （qa_engine_v3.py:366-370）：空参数等价空字典；解析失败（含非对象载荷）
// 记警告并以空字典继续。
func (e *ToolExecutor) ExecuteJSON(ctx context.Context, toolName, argumentsJSON string) string {
	var args map[string]any
	if strings.TrimSpace(argumentsJSON) != "" {
		if err := json.Unmarshal([]byte(argumentsJSON), &args); err != nil {
			slog.Warn("[agent] 工具参数解析失败: " + argumentsJSON)
			args = map[string]any{}
		}
	}
	return e.Execute(ctx, toolName, args)
}

// Execute 执行工具调用，返回 JSON 字符串格式的结果。
//
// 所有异常都被捕获并作为错误结果返回，不会向上抛出
// （agent_tools.py:305-335；defer recover 兜底等价 Python 全量
// except Exception 的语义，panic 也不逃逸）。
func (e *ToolExecutor) Execute(ctx context.Context, toolName string, args map[string]any) (result string) {
	defer func() {
		if rv := recover(); rv != nil {
			err := panicError(rv)
			slog.Error(fmt.Sprintf("工具执行失败 [%s]: %s", toolName, err.Error()))
			result = errorResultJSON(err)
		}
	}()

	out, err := e.dispatch(ctx, toolName, args)
	if err != nil {
		// 工具异常统一转错误结果（agent_tools.py:331-335），
		// 日志文案对齐 f"工具执行失败 [{tool_name}]: {type(e).__name__}: {e}"
		slog.Error(fmt.Sprintf("工具执行失败 [%s]: %s", toolName, err.Error()))
		return errorResultJSON(err)
	}
	return out
}

// dispatch 路由到各工具实现；返回的 error 表示工具层异常（非业务 error
// JSON 载荷），由 Execute 统一记录并包装。
func (e *ToolExecutor) dispatch(ctx context.Context, toolName string, args map[string]any) (string, error) {
	switch toolName {
	case "semantic_search":
		return e.executeSemanticSearch(ctx, args)
	case "keyword_search":
		return e.executeKeywordSearch(ctx, args)
	case "rerank_results":
		return e.executeRerank(ctx, args)
	case "list_channels":
		return e.executeListChannels(ctx, args)
	case "resolve_channel":
		return e.executeResolveChannel(ctx, args)
	case "get_recent_summaries":
		return e.executeRecentSummaries(ctx, args)
	case "get_channel_stats":
		return e.executeChannelStats(ctx, args)
	case "get_source_detail":
		return e.executeSourceDetail(args)
	case "get_channel_info":
		return e.executeChannelInfo(ctx, args)
	default:
		return marshalJSON(map[string]any{"error": fmt.Sprintf("未知工具: %s", toolName)}), nil
	}
}

// panicError 把 recover 值转为带类型名的错误。
func panicError(rv any) error {
	if err, ok := rv.(error); ok {
		return fmt.Errorf("%s: %s", errTypeName(err), err.Error())
	}
	return fmt.Errorf("Exception: %v", rv)
}

// errorResultJSON 构造 "工具执行失败: {类型}: {消息}" 的错误载荷
// （agent_tools.py:333-335 的 JSON 形态）。
func errorResultJSON(err error) string {
	return marshalJSON(map[string]any{"error": "工具执行失败: " + err.Error()})
}

// ---------------------------------------------------------------------------
// 各工具实现（方法名对齐 Python 的 _execute_*；string 为 JSON 结果，
// error 为需上抛到统一捕获点的工具异常）
// ---------------------------------------------------------------------------

// executeSemanticSearch 对齐 _execute_semantic_search（agent_tools.py:339-396）。
// collection 路由逐分支对齐 if/elif/else 链：messages（需消息库可用，否则
// 落到默认分支搜总结——Python 链式行为）/ summaries / all（默认）。
func (e *ToolExecutor) executeSemanticSearch(ctx context.Context, args map[string]any) (string, error) {
	if !e.vectorStore.IsAvailable() {
		return marshalJSON(map[string]any{"error": "向量存储不可用", "results": []any{}, "count": 0}), nil
	}

	var filterMetadata map[string]any
	if ch := args["channel_id"]; truthy(ch) {
		filterMetadata = map[string]any{"channel_id": ch}
	}

	// args.get("collection", "all")：None 与缺失都落入默认分支
	collection := "all"
	if c, ok := args["collection"]; ok && c != nil {
		collection = pythonStr(c)
	}

	queryArg, hasQuery := args["query"]
	if !hasQuery {
		return "", keyErr("query")
	}
	query := pythonStr(queryArg) // 显式 null → 空串，由装配层映射 Python None 语义
	topK, err := argInt(args, "top_k", 20)
	if err != nil {
		return "", err
	}
	dateAfter := optArgString(args, "date_after")
	dateBefore := optArgString(args, "date_before")

	var results []map[string]any
	switch {
	case collection == "messages" && e.vectorStore.IsMessagesAvailable():
		results, err = e.vectorStore.SearchMessages(ctx, query, topK, filterMetadata, dateAfter, dateBefore)
	case collection == "summaries":
		results, err = e.vectorStore.SearchSimilar(ctx, query, topK, filterMetadata, dateAfter, dateBefore)
	default:
		// 默认: 同时搜索 summaries + messages
		if e.vectorStore.IsMessagesAvailable() {
			results, err = e.vectorStore.SearchAll(ctx, query, topK, filterMetadata, dateAfter, dateBefore)
		} else {
			results, err = e.vectorStore.SearchSimilar(ctx, query, topK, filterMetadata, dateAfter, dateBefore)
		}
	}
	if err != nil {
		return "", err
	}

	// 累积到 result_store 并序列化（截断长文本）
	serialized := make([]serializedResult, 0, len(results))
	for _, r := range results {
		e.resultStore.put(r)
		serialized = append(serialized, serializeResult(r))
	}
	return marshalJSON(map[string]any{"results": serialized, "count": len(serialized)}), nil
}

// executeKeywordSearch 对齐 _execute_keyword_search（agent_tools.py:398-431）。
// keyword_search 返回原始 DB 行，需归一化为统一格式。注意：DB 行的
// summary_message_ids 是机器人发布的总结报告消息 ID，不是频道原帖 ID，
// 不能用于构造帖子链接（历史 Bug，勿"顺手修好"——总结类一律无 post_url）。
func (e *ToolExecutor) executeKeywordSearch(ctx context.Context, args map[string]any) (string, error) {
	keywords := argStringList(args, "keywords")
	topics := optArgStringList(args, "topics")
	timeRangeDays, err := argInt(args, "time_range_days", 90)
	if err != nil {
		return "", err
	}
	channelID := optArgString(args, "channel_id")
	limit, err := argInt(args, "limit", 10)
	if err != nil {
		return "", err
	}

	results, err := e.memoryMgr.SearchSummaries(ctx, keywords, topics, timeRangeDays, channelID, limit)
	if err != nil {
		return "", err
	}

	serialized := make([]serializedResult, 0, len(results))
	for _, r := range results {
		sid := r["id"]
		if sid == nil {
			continue
		}
		normalized := summaryRowResult(r, sid)
		e.resultStore.put(normalized)
		serialized = append(serialized, serializeResult(normalized))
	}
	return marshalJSON(map[string]any{"results": serialized, "count": len(serialized)}), nil
}

// executeRerank 对齐 _execute_rerank（agent_tools.py:433-466）：候选取自
// 结果存储（result_id 原始键判定语义见 dictKeyID），调用远端重排后结果带
// rerank_score 回存。
func (e *ToolExecutor) executeRerank(ctx context.Context, args map[string]any) (string, error) {
	if !e.reranker.IsAvailable() {
		return marshalJSON(map[string]any{"error": "重排序服务不可用", "results": []any{}, "count": 0}), nil
	}

	rawIDs, hasIDs := args["result_ids"]
	if !hasIDs {
		rawIDs = []any{}
	}
	// Python `for rid in result_ids`（agent_tools.py:440）：list 逐元素、str 逐
	// 字符、dict 逐键均可迭代（元素可哈希，`rid in store` 全 miss → "未找到有
	// 效的结果ID"）；仅 None/int/float/bool 抛 "不可迭代" TypeError（审计 A-3）。
	items, iterable := pyIterableValues(rawIDs)
	if !iterable {
		return "", &pyErr{"TypeError", fmt.Sprintf("'%s' object is not iterable", pyTypeName(rawIDs))}
	}
	candidates := make([]map[string]any, 0, len(items))
	for _, item := range items {
		id, valid, err := dictKeyID(item)
		if err != nil {
			return "", err
		}
		if !valid {
			continue // 哈希不等价于任何整型键 → `rid in store` 为假
		}
		if r, found := e.resultStore.get(id); found {
			candidates = append(candidates, r)
		}
	}
	if len(candidates) == 0 {
		return marshalJSON(map[string]any{"error": "未找到有效的结果ID", "results": []any{}, "count": 0}), nil
	}

	queryArg, hasQuery := args["query"]
	if !hasQuery {
		return "", keyErr("query")
	}
	topK, err := argInt(args, "top_k", 5)
	if err != nil {
		return "", err
	}
	reranked := e.reranker.Rerank(ctx, pythonStr(queryArg), candidates, topK)

	// 更新 result_store 中的结果（含 rerank_score）
	serialized := make([]serializedResult, 0, len(reranked))
	for _, r := range reranked {
		e.resultStore.put(r)
		serialized = append(serialized, serializeResult(r))
	}
	return marshalJSON(map[string]any{"results": serialized, "count": len(serialized)}), nil
}

// executeListChannels 对齐 _execute_list_channels（agent_tools.py:468-478）：
// limit = max(1, min(int(args.get("limit", 50)), 100))。
func (e *ToolExecutor) executeListChannels(ctx context.Context, args map[string]any) (string, error) {
	channels, err := e.memoryMgr.ListChannels(ctx)
	if err != nil {
		return "", err
	}
	limit, err := argInt(args, "limit", 50)
	if err != nil {
		return "", err
	}
	if limit < 1 {
		limit = 1
	}
	if limit > 100 {
		limit = 100
	}
	if channels == nil {
		channels = []map[string]any{}
	}
	for _, ch := range channels {
		normalizeDecimalFields(ch) // R21：原始行可能携带 DECIMAL 字段
	}
	shown := limit
	if shown > len(channels) {
		shown = len(channels)
	}
	return marshalJSON(map[string]any{
		"channels": channels[:shown],
		"count":    shown,
		"total":    len(channels),
	}), nil
}

// executeResolveChannel 对齐 _execute_resolve_channel（agent_tools.py:480-482）：
// 解析结果字典原样透传。
func (e *ToolExecutor) executeResolveChannel(ctx context.Context, args map[string]any) (string, error) {
	result, err := e.memoryMgr.ResolveChannel(ctx, optArgString(args, "channel_hint"))
	if err != nil {
		return "", err
	}
	return marshalJSON(result), nil
}

// executeRecentSummaries 对齐 _execute_recent_summaries（agent_tools.py:484-509）。
// 与 keyword_search 相同：总结记录不构造帖子链接。
func (e *ToolExecutor) executeRecentSummaries(ctx context.Context, args map[string]any) (string, error) {
	channelID := optArgString(args, "channel_id")
	limit, err := argInt(args, "limit", 5)
	if err != nil {
		return "", err
	}
	timeRangeDays := optArgInt(args, "time_range_days") // 0 = Python None

	results, err := e.memoryMgr.GetRecentSummaries(ctx, channelID, limit, timeRangeDays)
	if err != nil {
		return "", err
	}

	serialized := make([]serializedResult, 0, len(results))
	for _, r := range results {
		// r.get("id") or r.get("summary_id")：Python or 真值回退
		sid := r["id"]
		if !truthy(sid) {
			sid = r["summary_id"]
		}
		if sid == nil {
			continue
		}
		normalized := summaryRowResult(r, sid)
		e.resultStore.put(normalized)
		serialized = append(serialized, serializeResult(normalized))
	}
	return marshalJSON(map[string]any{"results": serialized, "count": len(serialized)}), nil
}

// executeChannelStats 对齐 _execute_channel_stats（agent_tools.py:511-513）。
func (e *ToolExecutor) executeChannelStats(ctx context.Context, args map[string]any) (string, error) {
	stats, err := e.memoryMgr.GetChannelStats(ctx, optArgString(args, "channel_id"))
	if err != nil {
		return "", err
	}
	normalizeDecimalFields(stats) // R21：DECIMAL → JSON 数值
	return marshalJSON(map[string]any{"stats": stats}), nil
}

// executeSourceDetail 对齐 _execute_source_detail（agent_tools.py:515-542）：
// 优先内部 result_id；兼容旧的 summary_id / doc_id 引用方式。
func (e *ToolExecutor) executeSourceDetail(args map[string]any) (string, error) {
	var result map[string]any
	resultID, hasResultID := args["result_id"]
	summaryID := args["summary_id"]
	docID := args["doc_id"]

	// 优先内部 result_id；int 转换失败按未提供处理（对齐 except (TypeError, ValueError) → None）
	if hasResultID && resultID != nil {
		if id, err := pyInt(resultID); err == nil {
			result, _ = e.resultStore.get(id)
		}
	}
	if result == nil && summaryID != nil {
		// Python dict.get(不可哈希键) 抛 TypeError: unhashable type: '...'
		// （agent_tools.py:529，execute 统一包装为错误载荷）；显式预判以消除
		// Go 侧 map 运行时 panic-recover 路径（审计 A-4）。
		switch summaryID.(type) {
		case []any, map[string]any:
			return "", &pyErr{"TypeError", fmt.Sprintf("unhashable type: '%s'", pyTypeName(summaryID))}
		}
		result, _ = e.resultStore.getBySummaryID(summaryID)
	}
	if result == nil && truthy(docID) {
		result, _ = e.resultStore.getByDocID(pythonStr(docID))
	}

	if result == nil {
		return marshalJSON(map[string]any{
			"error": "未找到来源详情，请先执行搜索或提供有效的 summary_id/doc_id",
		}), nil
	}

	detail := serializeResult(result)
	detail.SummaryText, _ = result["summary_text"].(string)
	return marshalJSON(map[string]any{"detail": detail}), nil
}

// executeChannelInfo 对齐 _execute_channel_info（agent_tools.py:544-556）。
// 无 channel_id（Python 真值判定为假：null/空串/0 等）时返回频道列表；
// Python 末尾的通用信息分支不可达（channel_id 此时必真值），不再移植。
func (e *ToolExecutor) executeChannelInfo(ctx context.Context, args map[string]any) (string, error) {
	channelID := args["channel_id"]
	if !truthy(channelID) {
		channels, err := e.memoryMgr.ListChannels(ctx)
		if err != nil {
			return "", err
		}
		if channels == nil {
			channels = []map[string]any{}
		}
		for _, ch := range channels {
			normalizeDecimalFields(ch)
		}
		return marshalJSON(map[string]any{"channels": channels, "count": len(channels)}), nil
	}

	context, err := e.memoryMgr.GetChannelContext(ctx, pythonStr(channelID))
	if err != nil {
		return "", err
	}
	return marshalJSON(map[string]any{"channel_id": channelID, "context": context}), nil
}

// ---------------------------------------------------------------------------
// 辅助方法
// ---------------------------------------------------------------------------

// summaryRowResult 把 DB 总结行归一化为统一结果格式
// （keyword_search / get_recent_summaries 共用，agent_tools.py:415-424, 496-505）。
// source="summary" ⇒ 永不构造 post_url（R9 红线）。
func summaryRowResult(r map[string]any, sid any) map[string]any {
	text, _ := r["summary_text"].(string)
	meta := map[string]any{
		"channel_id":   r["channel_id"],
		"channel_name": r["channel_name"],
		"created_at":   r["created_at"],
	}
	return map[string]any{
		"summary_id":   sid,
		"summary_text": text,
		"source":       "summary",
		"metadata":     meta,
	}
}

// argInt 取整型参数并应用默认值。键缺失 → 默认值；键存在（含显式 null）
// → Python int(值) 语义，失败返回工具异常（对齐 Python 把原值传给下游、
// 由异常路径兜底的行为）。
func argInt(args map[string]any, key string, def int) (int, error) {
	v, ok := args[key]
	if !ok {
		return def, nil
	}
	return pyInt(v)
}

// optArgInt 取可选整型参数；缺失或 null 返回 0（调用点以 0 表 Python None）。
func optArgInt(args map[string]any, key string) int {
	v, ok := args[key]
	if !ok || v == nil {
		return 0
	}
	n, err := pyInt(v)
	if err != nil {
		return 0
	}
	return n
}

// optArgString 取可选字符串参数；缺失/null 返回空串（等价 Python None）。
func optArgString(args map[string]any, key string) string {
	v, ok := args[key]
	if !ok || v == nil {
		return ""
	}
	return pythonStr(v)
}

// argStringList 取字符串列表参数；缺失等价 Python []（args.get("keywords", [])）。
// 非列表值按单元素处理；元素经 pythonStr 归一（Python 侧组件内 str 化）。
func argStringList(args map[string]any, key string) []string {
	v, ok := args[key]
	if !ok || v == nil {
		return []string{}
	}
	return anyToStringList(v)
}

// optArgStringList 取可选字符串列表参数；缺失等价 Python None（返回 nil）。
func optArgStringList(args map[string]any, key string) []string {
	v, ok := args[key]
	if !ok || v == nil {
		return nil
	}
	return anyToStringList(v)
}

// anyToStringList 把任意参数值归一为字符串列表。
func anyToStringList(v any) []string {
	list, ok := v.([]any)
	if !ok {
		return []string{pythonStr(v)}
	}
	out := make([]string, 0, len(list))
	for _, item := range list {
		out = append(out, pythonStr(item))
	}
	return out
}

// marshalJSON 序列化工具结果：禁用 HTML 转义以对齐 Python
// json.dumps(ensure_ascii=False) 的输出形态；序列化失败（等价 Python 的
// TypeError）转错误 JSON 由模型感知。
func marshalJSON(v any) string {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		// json.Encoder 对本包实际出现的载荷类型不会失败；兜底走
		// 标准 json.Marshal（map[string]string 不会失败），不递归。
		fallback, _ := json.Marshal(map[string]any{
			"error": "工具执行失败: TypeError: " + err.Error(),
		})
		return string(fallback)
	}
	return strings.TrimSuffix(b.String(), "\n")
}
