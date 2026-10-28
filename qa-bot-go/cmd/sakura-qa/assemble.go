// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

// assemble.go：main 装配层的适配器集合——把已完成的兄弟包（db/config/
// memory/vectors/embedder/reranker/tools）拼装成各消费者包定义的窄接口。
// 本文件不含业务分支逻辑的重新设计，仅做行为对齐的形态转换与
// core/ai/quota_manager.py、submission_repo.py 的逐行移植。
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"strings"

	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/config"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/contracts"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/db"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/embedder"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/engine"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/handlers"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/llm"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/memory"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/reranker"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/tg"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/tools"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/vectors"
)

// ────────────────────────────── 日志桥接 ──────────────────────────────

// slogBridge 把 *slog.Logger 适配为 tg.Logger（Debugf/Infof/Warningf/Errorf）。
type slogBridge struct{ l *slog.Logger }

func (b slogBridge) Debugf(format string, args ...any) {
	b.l.Debug(fmt.Sprintf(format, args...))
}

func (b slogBridge) Infof(format string, args ...any) {
	b.l.Info(fmt.Sprintf(format, args...))
}

func (b slogBridge) Warningf(format string, args ...any) {
	b.l.Warn(fmt.Sprintf(format, args...))
}

func (b slogBridge) Errorf(format string, args ...any) {
	b.l.Error(fmt.Sprintf(format, args...))
}

// ────────────────────────────── GOMEMLIMIT ──────────────────────────────

// parseMemLimit 解析 Go 运行时语法的内存上限值（如 "96MiB"、"1GiB"、
// 纯数字按字节）；与 runtime/debug 对 GOMEMLIMIT 的接受格式一致。
func parseMemLimit(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, errors.New("空值")
	}
	units := []struct {
		suffix string
		mult   int64
	}{
		{"kib", 1 << 10}, {"mib", 1 << 20}, {"gib", 1 << 30}, {"tib", 1 << 40},
		{"b", 1},
	}
	lower := strings.ToLower(s)
	for _, u := range units {
		if strings.HasSuffix(lower, u.suffix) {
			numPart := strings.TrimSpace(s[:len(s)-len(u.suffix)])
			return bytesFromFloat(numPart, u.mult)
		}
	}
	return bytesFromFloat(s, 1)
}

// bytesFromFloat 解析数值并乘以单位倍率；非法/非正/溢出一律报错
// （调用方回退默认值）。
func bytesFromFloat(num string, mult int64) (int64, error) {
	f, err := strconv.ParseFloat(num, 64)
	if err != nil {
		return 0, fmt.Errorf("非法数值 %q", num)
	}
	if math.IsNaN(f) || f <= 0 {
		return 0, fmt.Errorf("数值必须为正: %g", f)
	}
	bytes := f * float64(mult)
	if bytes > math.MaxInt64 {
		return 0, errors.New("数值溢出")
	}
	return int64(bytes), nil
}

// ────────────────────────────── 配额管理器 ──────────────────────────────

// quotaManager 是 core/ai/quota_manager.py::QuotaManager 的移植：
// check_quota（总配额→用户配额的两级检查与拒绝文案）、refund_quota、
// get_usage_status。Python 三方法内部自捕获异常从不向上抛，Go 侧对齐
// （仅返回值、无 error）；EffectiveQuota 校验失败等价 model_validate raise
// → 整体拒绝（评审 #10：config.json 越界是"拒绝"而非 clamp 放宽）。
type quotaManager struct {
	cfg *config.Config
	db  *db.Manager
	log *slog.Logger
}

// newQuotaManager 构造并完成构造期限额刷新（__init__ 的 _refresh_limits；
// config.json 非法时 Python 直接抛 ValidationError 使进程崩溃，Go 对齐为
// 返回 error 由 main 非零退出）。日志文案逐字对齐 quota_manager.py:37-39。
func newQuotaManager(cfg *config.Config, mgr *db.Manager, log *slog.Logger) (*quotaManager, error) {
	limits, err := cfg.EffectiveQuota()
	if err != nil {
		return nil, err
	}
	log.Info(fmt.Sprintf("配额管理器初始化完成: 用户限额=%d, 总限额=%d", limits.UserDaily, limits.TotalDaily))
	return &quotaManager{cfg: cfg, db: mgr, log: log}, nil
}

// quotaSystemError 配额检查异常分支的统一拒绝文案（quota_manager.py:128-134）。
func quotaSystemError() contracts.QuotaCheckResult {
	return contracts.QuotaCheckResult{
		Allowed:   false,
		Remaining: 0,
		Used:      0,
		Message:   "⚠️ **系统错误**\n\n配额检查失败，请稍后再试。",
	}
}

// CheckQuota 检查并预留一次配额（quota_manager.py:51-136 check_quota 的
// 逐分支移植）：总配额 → check_and_increment → 允许/拒绝文案。
func (q *quotaManager) CheckQuota(ctx context.Context, userID int64) contracts.QuotaCheckResult {
	limits, err := q.cfg.EffectiveQuota()
	if err != nil {
		// model_validate raise → except 分支（评审 #10）。
		q.log.Error(fmt.Sprintf("配额检查失败: ValidationError: %v", err))
		return quotaSystemError()
	}
	isAdmin := q.cfg.IsAdmin(userID)

	// 检查每日总限额（管理员跳过判定，但查询本身无条件执行，对齐 Python 顺序）。
	// Python get_total_daily_usage 内部吞异常（mysql.py:1067-1069，日志
	// "获取总使用次数失败: ..."）并返回 0，quota_manager 无感知继续——DB 故障
	// 下不拒绝（审计 B-1，管理员亦不例外）。
	totalUsed, err := q.db.GetTotalDailyUsage(ctx, "")
	if err != nil {
		q.log.Error(fmt.Sprintf("获取总使用次数失败: Exception: %v", err))
		totalUsed = 0
	}
	if totalUsed >= limits.TotalDaily && !isAdmin {
		q.log.Warn(fmt.Sprintf("今日总配额已用尽: %d/%d", totalUsed, limits.TotalDaily))
		return contracts.QuotaCheckResult{
			Allowed: false,
			Message: fmt.Sprintf("⏰ **今日配额已用完**\n\n系统今日已处理 %d 次查询。\n请在明日配额重置后继续使用。\n\n🌙 **重置时间：每日00:00**", totalUsed),
		}
	}

	// 检查并增加用户配额（管理员直通；非原子 SELECT+UPDATE 保持 Python 现状）。
	res := q.db.CheckAndIncrementQuota(ctx, userID, limits.UserDaily, isAdmin)
	// Python check_and_increment_quota 内部吞异常（mysql.py:1006-1008，日志
	// "配额检查失败: ..."）返回 {"allowed": false, "error": ...}；quota_manager
	// 不感知异常，与正常超限共用 :91-102 的拒绝文案（used 缺省 0、daily_limit
	// 取用户限额）——同为 fail-closed，仅文案对齐（审计 B-2）。
	if res.Error != "" {
		q.log.Error(fmt.Sprintf("配额检查失败: Exception: %s", res.Error))
		res.Allowed = false
		res.Remaining = 0
		res.Used = 0
		res.DailyLimit = limits.UserDaily
	}
	if !res.Allowed {
		q.log.Info(fmt.Sprintf("用户 %d 配额已用尽: %d/%d", userID, res.Used, res.DailyLimit))
		return contracts.QuotaCheckResult{
			Allowed: false,
			Message: fmt.Sprintf("⏰ **今日配额已用完**\n\n你今天已经使用了 %d 次查询。\n休息一下，明天配额重置后再来吧。\n\n🌙 **重置时间：每日00:00**", res.Used),
		}
	}

	remaining := res.Remaining
	var message string
	if isAdmin {
		message = "🌟 **管理员权限**\n\n你拥有无限制访问的特权。"
	} else {
		totalRemaining := limits.TotalDaily - totalUsed - 1
		message = fmt.Sprintf("✅ **查询成功**\n\n💡 今日剩余次数：%d/%d\n📊 系统总剩余：%d次", remaining, limits.UserDaily, totalRemaining)
	}
	q.log.Info(fmt.Sprintf("用户 %d 配额检查通过: %d/%d (剩余%d)", userID, res.Used, limits.UserDaily, remaining))
	return contracts.QuotaCheckResult{
		Allowed:   true,
		Remaining: remaining,
		Used:      res.Used,
		IsAdmin:   isAdmin,
		Message:   message,
	}
}

// RefundQuota 回退一次配额占用（quota_manager.py:139-160 refund_quota）：
// 管理员无操作恒 true；幂等 UPDATE 由 db 层保证（usage_count > 0 条件）。
func (q *quotaManager) RefundQuota(ctx context.Context, userID int64) bool {
	if q.cfg.IsAdmin(userID) {
		return true
	}
	ok, err := q.db.RefundQuota(ctx, userID, "")
	if err != nil {
		q.log.Error(fmt.Sprintf("回退配额失败: %T: %v", err, err))
		return false
	}
	if ok {
		q.log.Info(fmt.Sprintf("用户 %d 的请求处理失败，已回退本次配额占用", userID))
	} else {
		q.log.Info(fmt.Sprintf("用户 %d 无需回退配额（当日无占用记录）", userID))
	}
	return ok
}

// GetUsageStatus 获取使用状态、不消耗配额（quota_manager.py:163-205
// get_usage_status）。Err 非空对应 Python 返回 dict 含 "error" 键。
func (q *quotaManager) GetUsageStatus(ctx context.Context, userID int64) handlers.UsageStatus {
	limits, err := q.cfg.EffectiveQuota()
	if err != nil {
		return handlers.UsageStatus{Err: err.Error()}
	}
	isAdmin := q.cfg.IsAdmin(userID)
	used, _, err := q.db.GetQuotaUsage(ctx, userID, "")
	if err != nil {
		return handlers.UsageStatus{Err: err.Error()}
	}
	totalUsed, err := q.db.GetTotalDailyUsage(ctx, "")
	if err != nil {
		return handlers.UsageStatus{Err: err.Error()}
	}
	if isAdmin {
		return handlers.UsageStatus{
			IsAdmin:        true,
			UsedToday:      0,
			Remaining:      -1, // -1 表示无限制
			TotalUsedToday: totalUsed,
		}
	}
	remaining := maxInt64(0, limits.UserDaily-used)
	return handlers.UsageStatus{
		IsAdmin:        false,
		UsedToday:      used,
		Remaining:      remaining,
		TotalUsedToday: totalUsed,
	}
}

// maxInt64 Python max(0, x) 等价。
func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// ────────────────────────────── 问答引擎装配 ──────────────────────────────

// assembleEngine 装配 QAEngineV3 等价物：SSE 客户端（每请求热读 AI 配置）、
// 工具层（向量+记忆+重排适配）、会话管理器、人格与频道上下文。
func assembleEngine(cfg *config.Config, mgr *db.Manager, conversations *engine.Conversations, vecStore *vectors.Store, logger *slog.Logger) *engine.Engine {
	chat := llm.NewClient(llm.AIConfigFunc(func() llm.AIConfig {
		c := cfg.EffectiveAIConfig()
		return llm.AIConfig{APIKey: c.APIKey, BaseURL: c.BaseURL, Model: c.Model}
	}), llm.WithLogger(logger))

	memMgr := memory.New(mgr, mgr.DB)
	embGen := embedder.NewGenerator(cfg.Env)
	rr := reranker.New(cfg.Env)

	toolKit := &engineTools{
		vs: &toolVectorStore{vec: vecStore, emb: embGen, log: logger},
		mm: memoryTools{m: memMgr},
		rr: rerankTools{r: rr},
	}

	return engine.New(engine.Deps{
		Chat:          chat,
		Tools:         toolKit,
		Conversations: conversations,
		Persona:       cfg,
		Memory:        engineMemory{m: memMgr},
		Logger:        logger,
	})
}

// engineTools 实现引擎的 Tools 窄接口：schema 原样透传 + 每请求独立执行器
// （qa_engine_v3.py:300 的 per-request ToolExecutor）。
type engineTools struct {
	vs tools.VectorStore
	mm tools.MemoryManager
	rr tools.Reranker
}

// ToolSchemasJSON 实现 engine.Tools。
func (t *engineTools) ToolSchemasJSON() json.RawMessage { return tools.ToolSchemasJSON }

// NewExecutor 实现 engine.Tools。
func (t *engineTools) NewExecutor() engine.ToolExecutor {
	return executorAdapter{inner: tools.NewToolExecutor(t.vs, t.mm, t.rr)}
}

// executorAdapter 把 *tools.ToolExecutor 适配为 engine.ToolExecutor：
// tools.Execute 已把工具异常转为 {"error": ...} JSON 返回给模型、绝不向上抛
// （agent_tools.py:305-335 语义），与 Python 引擎侧"execute 不抛异常"一致，
// 故 error 恒为 nil——引擎的 error 降级分支仅对应 Python 的不可达路径。
type executorAdapter struct{ inner *tools.ToolExecutor }

// Execute 实现 engine.ToolExecutor。
func (a executorAdapter) Execute(ctx context.Context, name string, args map[string]any) (string, error) {
	return a.inner.Execute(ctx, name, args), nil
}

// rerankTools 实现工具层的 Reranker 窄接口：[]map[string]any ↔
// []reranker.Document（Document 底层类型即 map[string]any，逐元素转换
// 零拷贝，保持与结果存储共享同一 map 的身份语义）。
type rerankTools struct{ r *reranker.Reranker }

// IsAvailable 实现 tools.Reranker。
func (t rerankTools) IsAvailable() bool { return t.r.IsAvailable() }

// Rerank 实现 tools.Reranker（实现方返回候选浅拷贝并追加 rerank_score）。
func (t rerankTools) Rerank(ctx context.Context, query string, candidates []map[string]any, topK int) []map[string]any {
	docs := make([]reranker.Document, 0, len(candidates))
	for _, c := range candidates {
		docs = append(docs, reranker.Document(c))
	}
	out := t.r.Rerank(ctx, query, docs, topK)
	res := make([]map[string]any, 0, len(out))
	for _, d := range out {
		res = append(res, map[string]any(d))
	}
	return res
}

// engineMemory 实现引擎的频道上下文窄接口（get_channel_context 的无参形态，
// memory_manager.py:146-180）。
type engineMemory struct{ m *memory.Manager }

// GetChannelContext 实现 engine.ChannelContextProvider；组件内部自捕获异常
// 返回空串，error 恒 nil（引擎对 error 的降级分支不可达，对齐 Python）。
func (e engineMemory) GetChannelContext(ctx context.Context) (string, error) {
	return e.m.GetChannelContext(ctx, ""), nil
}

// ────────────────────────────── 向量检索适配 ──────────────────────────────

// toolVectorStore 实现工具层的 VectorStore 窄接口：组合 embedder（查询文本
// → 向量）与 vectors（SQLite 只读暴力余弦），移植 core/ai/vector_store.py
// 的 search_similar / search_messages / search_all 语义。
type toolVectorStore struct {
	vec *vectors.Store
	emb *embedder.Generator
	log *slog.Logger
}

// IsAvailable 实现 VectorStore（vector_store.py:154-160：库就绪与否每次
// 重新探测，失败不锁存）。
func (t *toolVectorStore) IsAvailable() bool {
	return t.vec.Ready(context.Background()) == nil
}

// IsMessagesAvailable 实现 VectorStore（:281-285；契约下两表同建，探测等价）。
func (t *toolVectorStore) IsMessagesAvailable() bool {
	return t.vec.Ready(context.Background()) == nil
}

// SearchSimilar 实现 VectorStore：仅总结 collection（:214-279）。
func (t *toolVectorStore) SearchSimilar(ctx context.Context, query string, topK int, filterMetadata map[string]any, dateAfter, dateBefore string) ([]map[string]any, error) {
	hits, err := t.searchOne(ctx, "summaries", query, topK, filterMetadata, dateAfter, dateBefore)
	if err != nil {
		return nil, err
	}
	return tools.HitsToResults(hits, ""), nil
}

// SearchMessages 实现 VectorStore：仅消息 collection（:369-404）。
func (t *toolVectorStore) SearchMessages(ctx context.Context, query string, topK int, filterMetadata map[string]any, dateAfter, dateBefore string) ([]map[string]any, error) {
	hits, err := t.searchOne(ctx, "messages", query, topK, filterMetadata, dateAfter, dateBefore)
	if err != nil {
		return nil, err
	}
	return tools.HitsToResults(hits, ""), nil
}

// SearchAll 实现 VectorStore：两 collection 合并按相似度降序截 top_k
// （:406-464）；单侧失败记日志继续（Python 每侧独立 try/except）。
func (t *toolVectorStore) SearchAll(ctx context.Context, query string, topK int, filterMetadata map[string]any, dateAfter, dateBefore string) ([]map[string]any, error) {
	results := make([]map[string]any, 0, topK)

	sumHits, err := t.searchOne(ctx, "summaries", query, topK, filterMetadata, dateAfter, dateBefore)
	if err != nil {
		t.log.Error(fmt.Sprintf("搜索summaries失败: %T: %v", err, err))
		sumHits = nil
	}
	results = append(results, tools.HitsToResults(sumHits, "summary")...)

	msgHits, err := t.searchOne(ctx, "messages", query, topK, filterMetadata, dateAfter, dateBefore)
	if err != nil {
		t.log.Error(fmt.Sprintf("搜索messages失败: %T: %v", err, err))
		msgHits = nil
	}
	results = append(results, tools.HitsToResults(msgHits, "message")...)

	tools.SortResultsBySimilarity(results)
	if len(results) > topK {
		results = results[:topK]
	}
	return results, nil
}

// searchOne 生成查询向量并检索单个 collection。embedding 不可用/生成失败
// 返回空切片（对齐 _search_collection 的 is_available/None → [] 分支，
// vector_store.py:556-564）。
func (t *toolVectorStore) searchOne(ctx context.Context, collection, query string, topK int, filterMetadata map[string]any, dateAfter, dateBefore string) ([]vectors.Hit, error) {
	if !t.emb.IsAvailable() {
		return nil, nil
	}
	queryVec, err := t.emb.Generate(ctx, query)
	if err != nil || queryVec == nil {
		t.log.Warn(fmt.Sprintf("生成查询向量失败: %v", err))
		return nil, nil
	}
	return t.vec.Search(ctx, queryVec, vectors.Options{
		Collection: collection,
		TopK:       topK,
		ChannelID:  filterChannelID(filterMetadata),
		DateAfter:  dateAfter,
		DateBefore: dateBefore,
	})
}

// filterChannelID 提取 filter_metadata 的 channel_id（执行器只构造
// {"channel_id": <arg>} 一种过滤形态）。
func filterChannelID(m map[string]any) string {
	if m == nil {
		return ""
	}
	switch v := m["channel_id"].(type) {
	case string:
		return v
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", v)
	}
}

// ────────────────────────────── 记忆管理适配 ──────────────────────────────

// memoryTools 实现工具层的 MemoryManager 窄接口：把 memory.Manager 的
// 强类型返回值转换为 Python DictCursor 行形态的 map（键名 = DB 列名，
// 对齐 agent_tools 读取的 r.get("id")/r.get("summary_text") 等键）。
type memoryTools struct{ m *memory.Manager }

// SearchSummaries 实现 MemoryManager（memory_manager.py:330-413；topics
// nil 等价 Python None，dateBefore=nil 表示无显式上界→以当前时间为界）。
func (t memoryTools) SearchSummaries(ctx context.Context, keywords, topics []string, timeRangeDays int, channelID string, limit int) ([]map[string]any, error) {
	rows := t.m.SearchSummaries(ctx, keywords, topics, timeRangeDays, channelID, limit, nil)
	out := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		out = append(out, summaryToMap(r))
	}
	return out, nil
}

// ListChannels 实现 MemoryManager（:182-194，行经 _normalize_channel_row）。
func (t memoryTools) ListChannels(ctx context.Context) ([]map[string]any, error) {
	entries := t.m.ListChannels(ctx)
	out := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		out = append(out, channelEntryToMap(e))
	}
	return out, nil
}

// ResolveChannel 实现 MemoryManager（:196-281，结果字典原样透传；
// JSON 往返保持 omitempty 键集与 Python 字典一致）。
func (t memoryTools) ResolveChannel(ctx context.Context, channelHint string) (map[string]any, error) {
	return toMap(t.m.ResolveChannel(ctx, channelHint)), nil
}

// GetRecentSummaries 实现 MemoryManager（:300-328；timeRangeDays=0 等价
// Python None——不限时间范围）。
func (t memoryTools) GetRecentSummaries(ctx context.Context, channelID string, limit, timeRangeDays int) ([]map[string]any, error) {
	rows := t.m.GetRecentSummaries(ctx, channelID, limit, timeRangeDays)
	out := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		out = append(out, summaryToMap(r))
	}
	return out, nil
}

// GetChannelStats 实现 MemoryManager（:283-298，_normalize_channel_row；
// 无统计返回空 dict）。
func (t memoryTools) GetChannelStats(ctx context.Context, channelID string) (map[string]any, error) {
	entry := t.m.GetChannelStats(ctx, channelID)
	if entry == nil {
		return map[string]any{}, nil
	}
	return channelStatsToMap(*entry), nil
}

// GetChannelContext 实现 MemoryManager（:146-180 的带参形态）。
func (t memoryTools) GetChannelContext(ctx context.Context, channelID string) (string, error) {
	return t.m.GetChannelContext(ctx, channelID), nil
}

// toMap 经 JSON 往返把强类型结果转为 map[string]any（仅用于 JSON 标签
// 已与 Python 键集对齐的结构，如 ResolveResult）。
func toMap(v any) map[string]any {
	raw, err := json.Marshal(v)
	if err != nil {
		return map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return map[string]any{}
	}
	return out
}

// channelEntryToMap 频道目录行 → Python _normalize_channel_row 键集。
func channelEntryToMap(e memory.ChannelEntry) map[string]any {
	return map[string]any{
		"channel_id":        e.ChannelID,
		"channel_name":      e.ChannelName,
		"last_summary_time": e.LastSummaryTime,
		"summary_count":     e.SummaryCount,
		"message_count":     e.MessageCount,
	}
}

// channelStatsToMap 频道统计行 → 同上键集 + first_summary_time。
func channelStatsToMap(e memory.ChannelStatsEntry) map[string]any {
	return map[string]any{
		"channel_id":         e.ChannelID,
		"channel_name":       e.ChannelName,
		"summary_count":      e.SummaryCount,
		"message_count":      e.MessageCount,
		"last_summary_time":  e.LastSummaryTime,
		"first_summary_time": e.FirstSummaryTime,
	}
}

// summaryToMap summaries 表 DictCursor 行 → map（键名 = SELECT * 列名；
// NULL → nil；summary_message_ids 已由 db 层解析为列表——对齐
// _parse_summary_rows；keywords/topics/entities 保持原始 JSON 文本，
// 与 DictCursor 返回 str 的行为一致）。
func summaryToMap(s db.Summary) map[string]any {
	return map[string]any{
		"id":                  s.ID,
		"channel_id":          s.ChannelID,
		"channel_name":        nullStringVal(s.ChannelName),
		"summary_text":        s.SummaryText,
		"message_count":       nullIntVal(s.MessageCount),
		"start_time":          nullTimeVal(s.StartTime),
		"end_time":            nullTimeVal(s.EndTime),
		"created_at":          nullTimeVal(s.CreatedAt),
		"ai_model":            nullStringVal(s.AIModel),
		"summary_type":        nullStringVal(s.SummaryType),
		"summary_message_ids": s.SummaryMessageIDs,
		"poll_message_id":     nullIntVal(s.PollMessageID),
		"button_message_id":   nullIntVal(s.ButtonMessageID),
		"keywords":            rawJSONText(s.Keywords),
		"topics":              rawJSONText(s.Topics),
		"sentiment":           nullStringVal(s.Sentiment),
		"entities":            rawJSONText(s.Entities),
	}
}

// nullStringVal / nullIntVal / nullTimeVal / rawJSONText：SQL 空值 → Python
// None（nil）形态；datetime → time.Time（工具层序列化时转 isoformat）。
func nullStringVal(v sql.NullString) any {
	if !v.Valid {
		return nil
	}
	return v.String
}

func nullIntVal(v sql.NullInt64) any {
	if !v.Valid {
		return nil
	}
	return v.Int64
}

func nullTimeVal(v sql.NullTime) any {
	if !v.Valid {
		return nil
	}
	return v.Time
}

func rawJSONText(v []byte) any {
	if len(v) == 0 {
		return nil
	}
	return string(v)
}

// ────────────────────────────── 投稿入库 ──────────────────────────────

// submissionStore 实现 handlers.SubmissionStore：
// submission_repo.py:48-99 create_submission 的移植（单条 INSERT +
// status='pending'；管理员通知由主进程轮询完成，QA 不发）。
type submissionStore struct{ db *db.Manager }

// CreateSubmission 实现 SubmissionStore。失败返回非 nil error
// （对应 Python 捕获异常后返回 None）。
func (s *submissionStore) CreateSubmission(ctx context.Context, rec *handlers.SubmissionRecord) (int64, error) {
	res, err := s.db.DB.ExecContext(ctx, `
                        INSERT INTO submissions
                        (submitter_id, submitter_name, title, content, media_files,
                         target_channel, is_anonymous, status)
                        VALUES (?, ?, ?, ?, ?, ?, ?, 'pending')
                    `, rec.SubmitterID, rec.SubmitterName, rec.Title,
		rec.Content, rec.MediaJSON, rec.TargetChannel, rec.IsAnonymous)
	if err != nil {
		slog.Error(fmt.Sprintf("创建投稿记录失败: %T: %v", err, err))
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		slog.Error(fmt.Sprintf("创建投稿记录失败: %T: %v", err, err))
		return 0, err
	}
	slog.Info(fmt.Sprintf("创建投稿记录成功: ID=%d, 投稿者=%s", id, rec.SubmitterName))
	return id, nil
}

// 编译期断言：装配产物满足各消费者窄接口。
var (
	_ handlers.QuotaManager         = (*quotaManager)(nil)
	_ tools.VectorStore             = (*toolVectorStore)(nil)
	_ tools.MemoryManager           = memoryTools{}
	_ engine.ChannelContextProvider = engineMemory{}
	_ handlers.SubmissionStore      = (*submissionStore)(nil)
	_ tg.Logger                     = slogBridge{}
)
