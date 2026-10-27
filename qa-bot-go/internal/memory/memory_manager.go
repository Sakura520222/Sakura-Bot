// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

// Package memory 移植 core/ai/memory_manager.py 的记忆管理器：
// 频道目录（list_channels）、频道画像上下文（get_channel_context）、
// 最近总结（get_recent_summaries）、关键词检索（search_summaries）、
// 频道解析（resolve_channel）。
//
// 行为对齐要点：
//   - 用户可见文案与日志和 Python 版逐字一致；
//   - Python 数据库层自吞异常返回 []/None/{}，Go 侧同样吞错并记同文日志，
//     不向工具层（internal/tools）上抛错误（对齐 agent_tools 的可观察行为）；
//   - JSON 列按 R13 双解析兼容：空值 → None、解析成功 → 值、失败 → 保留
//     原始文本（pyrepr.go 的 parseJSONText，与 db.ParseJSONColumn 同语义，
//     另区分 int/float 以复刻 Python str() 的数字渲染）；
//   - resolve_channel 的候选匹配全部按小写比较（memory_manager.py:213-243）。
package memory

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/contracts"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/db"
)

// Store 是 memory 包对数据库的窄接口（由 internal/db.Manager 满足；
// 测试注入假实现）。不在本包 import 兄弟开发包之外的任何实现细节。
type Store interface {
	// GetAllChannels 频道目录：summaries 聚合 + config.CHANNELS 合并补零行，
	// 已按 last_summary_time 降序稳定排序（mysql.py:1722-1768）。
	GetAllChannels(ctx context.Context) ([]contracts.ChannelInfo, error)
	// GetSummaries 总结检索：动态 WHERE + created_at 倒序（mysql.py:519-571）。
	GetSummaries(ctx context.Context, channelID string, limit, offset int, startDate, endDate *time.Time) ([]db.Summary, error)
	// GetChannelSummaryStats 单频道总结统计（mysql.py:1770-1806）。
	GetChannelSummaryStats(ctx context.Context, channelID string) (db.ChannelSummaryStats, error)
}

// Querier 是频道画像查询所需的最小 SQL 能力（internal/db.Manager.DB 的
// *sql.DB 天然满足）。画像查询的 SELECT * 位置扫描在包内实现，
// 避免 internal/db 在 W2 交付后追加方法。
type Querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// 编译期断言：internal/db.Manager 满足 Store，*sql.DB 满足 Querier。
var (
	_ Store   = (*db.Manager)(nil)
	_ Querier = (*sql.DB)(nil)
)

// Manager 是记忆管理器（对齐 MemoryManager）。
type Manager struct {
	store   Store   // 目录/总结/统计窄接口
	querier Querier // 频道画像查询（channel_profiles 表）
}

// New 构造记忆管理器并输出与 Python 版逐字一致的初始化日志
// （memory_manager.py:30-33）。装配示例：memory.New(dbMgr, dbMgr.DB)。
func New(store Store, querier Querier) *Manager {
	m := &Manager{store: store, querier: querier}
	slog.Info("记忆管理器初始化完成")
	return m
}

// ChannelEntry 是标准化后的频道目录行，对齐 _normalize_channel_row
// 应用到 get_all_channels 聚合行（mysql.py:1722-1768 SELECT 列序即
// DictCursor 键序，json 序列化键序与 Python 一致）。时间字段为
// isoformat 字符串；无值时为 nil（Python None → JSON null）。
type ChannelEntry struct {
	ChannelID       string `json:"channel_id"`
	ChannelName     string `json:"channel_name"`
	LastSummaryTime any    `json:"last_summary_time"`
	SummaryCount    int64  `json:"summary_count"`
	MessageCount    int64  `json:"message_count"`
}

// channelEntryFromInfo 把 db 目录条目标准化为 JSON 友好形状
// （memory_manager.py:415-425：datetime → isoformat、计数 → int）。
func channelEntryFromInfo(ch contracts.ChannelInfo) ChannelEntry {
	e := ChannelEntry{
		ChannelID:    ch.ChannelID,
		ChannelName:  ch.ChannelName,
		SummaryCount: ch.SummaryCount,
		MessageCount: ch.MessageCount,
	}
	// Python：仅当字段有 isoformat 属性（datetime）才转换，None 原样保留。
	if !ch.LastSummaryTime.IsZero() {
		e.LastSummaryTime = db.PythonISOFormat(ch.LastSummaryTime)
	}
	return e
}

// ListChannels 获取所有可查询频道（memory_manager.py:182-194）。
// 目录聚合/合并/排序在 internal/db.GetAllChannels 完成，此处仅做行
// 标准化（_normalize_channel_row）；异常吞掉返回空列表（Python except 分支）。
func (m *Manager) ListChannels(ctx context.Context) []ChannelEntry {
	channels, err := m.store.GetAllChannels(ctx)
	if err != nil {
		// Python：logger.error(f"获取频道列表失败: ..."); return []
		slog.Error(fmt.Sprintf("获取频道列表失败: %T: %s", err, err))
		return []ChannelEntry{}
	}
	out := make([]ChannelEntry, 0, len(channels))
	for _, ch := range channels {
		out = append(out, channelEntryFromInfo(ch))
	}
	return out
}

// ChannelStatsEntry 是单频道统计行，对齐 _normalize_channel_row 应用到
// get_channel_summary_stats 行（mysql.py:1770-1806 的 SELECT 列序即键序，
// 注意与目录行的键序不同）。
type ChannelStatsEntry struct {
	ChannelID        string `json:"channel_id"`
	ChannelName      string `json:"channel_name"`
	SummaryCount     int64  `json:"summary_count"`
	MessageCount     int64  `json:"message_count"`
	LastSummaryTime  any    `json:"last_summary_time"`
	FirstSummaryTime any    `json:"first_summary_time"`
}

// GetChannelStats 获取频道统计信息（memory_manager.py:283-298）。
// 缺行时 db 层已返回默认聚合（Python 兜底字典）；异常吞掉返回 nil
// （Python 返回空字典 {}，JSON 序列化为 {}）。
func (m *Manager) GetChannelStats(ctx context.Context, channelID string) *ChannelStatsEntry {
	stats, err := m.store.GetChannelSummaryStats(ctx, channelID)
	if err != nil {
		slog.Error(fmt.Sprintf("获取频道统计失败: %T: %s", err, err))
		return nil
	}
	e := &ChannelStatsEntry{
		ChannelID:    stats.ChannelID,
		ChannelName:  stats.ChannelName,
		SummaryCount: stats.SummaryCount,
		MessageCount: stats.MessageCount,
	}
	if stats.LastSummaryTime.Valid {
		e.LastSummaryTime = db.PythonISOFormat(stats.LastSummaryTime.Time)
	}
	if stats.FirstSummaryTime.Valid {
		e.FirstSummaryTime = db.PythonISOFormat(stats.FirstSummaryTime.Time)
	}
	return e
}

// GetRecentSummaries 获取最近总结（memory_manager.py:300-328）。
// timeRangeDays 为 0 等价 Python None：不传时间范围，返回不限时间的
// 最近 N 条；channelID 传空串等价 None（不限频道）。异常吞掉返回空列表。
func (m *Manager) GetRecentSummaries(ctx context.Context, channelID string, limit, timeRangeDays int) []db.Summary {
	var start, end *time.Time
	if timeRangeDays != 0 {
		e := db.NowUTC()
		s := e.AddDate(0, 0, -timeRangeDays) // timedelta(days=...)
		start, end = &s, &e
	}
	out, err := m.store.GetSummaries(ctx, channelID, limit, 0, start, end)
	if err != nil {
		slog.Error(fmt.Sprintf("获取最近总结失败: %T: %s", err, err))
		return []db.Summary{}
	}
	if out == nil {
		// Python 返回 []；Go nil 切片会序列化成 null，统一为空列表。
		return []db.Summary{}
	}
	return out
}

// SearchSummaries 搜索相关总结（memory_manager.py:330-413）。
// keywords/topics 传 nil 等价 Python None（nil keywords + 非空 topics 会
// 复刻 Python 的 TypeError 吞错路径）；timeRangeDays 默认由调用方传
// （memory 层 Python 默认 7，keyword_search 工具传 90）；dateBefore 为
// nil 时取当前 UTC 时间。异常吞掉返回空列表。
func (m *Manager) SearchSummaries(ctx context.Context, keywords, topics []string, timeRangeDays int, channelID string, limit int, dateBefore *time.Time) []db.Summary {
	end := db.NowUTC()
	if dateBefore != nil {
		end = *dateBefore
	}
	start := end.AddDate(0, 0, -timeRangeDays)

	summaries, err := m.store.GetSummaries(ctx, channelID, limit, 0, &start, &end)
	if err != nil {
		slog.Error(fmt.Sprintf("搜索总结失败: %T: %s", err, err))
		return []db.Summary{}
	}

	// Python：if not keywords and not topics: return summaries
	if len(keywords) == 0 && len(topics) == 0 {
		if summaries == nil {
			return []db.Summary{}
		}
		return summaries
	}

	// Python 关键词为 None 而主题非空时，any() 对 None 迭代抛 TypeError，
	// 被外层 except 吞掉返回 []（可观察行为）。
	if keywords == nil {
		slog.Error("搜索总结失败: TypeError: 'NoneType' object is not iterable")
		return []db.Summary{}
	}

	filtered := make([]db.Summary, 0, len(summaries))
	for _, s := range summaries {
		textLower := strings.ToLower(s.SummaryText)

		// R13 双解析（memory_manager.py:376-390）：真值 → json 解析，
		// 失败保留原始文本；空值 → 空列表（str([]) == "[]"）。
		keywordsVal := parseJSONText(s.Keywords)
		if !pythonTruthy(keywordsVal) {
			keywordsVal = []any{}
		}
		topicsVal := parseJSONText(s.Topics)
		if !pythonTruthy(topicsVal) {
			topicsVal = []any{}
		}

		// keyword_match = any(kw in text or kw in str(keywords) for kw in keywords)
		keywordMatch := false
		for _, kw := range keywords {
			k := strings.ToLower(kw)
			if strings.Contains(textLower, k) ||
				strings.Contains(strings.ToLower(pythonStr(keywordsVal)), k) {
				keywordMatch = true
				break
			}
		}

		// topic_match = any(topic in str(topics) for topic in topics) if topics else True
		topicMatch := true
		if len(topics) > 0 {
			topicMatch = false
			topicsLower := strings.ToLower(pythonStr(topicsVal))
			for _, topic := range topics {
				if strings.Contains(topicsLower, strings.ToLower(topic)) {
					topicMatch = true
					break
				}
			}
		}

		if keywordMatch || topicMatch {
			filtered = append(filtered, s)
		}
	}

	slog.Info(fmt.Sprintf("搜索完成: 找到 %d 条匹配总结", len(filtered)))
	// Python filtered[:limit] 超长自动截断；Go 需显式 min。
	if len(filtered) > limit {
		filtered = filtered[:limit]
	}
	return filtered
}

// styleMap 是 get_channel_context 的风格映射（memory_manager.py:169）。
var styleMap = map[string]string{
	"tech":    "技术专业",
	"casual":  "轻松闲聊",
	"neutral": "中立客观",
}

// ChannelProfile 是 channel_profiles 表的一行（get_channel_profile 返回
// 字典的 Go 形状，mysql.py:1096-1120）。Topics/KeywordsFreq 为解析后的
// JSON 值：空列 → nil、解析失败 → 原始文本（Python try/except 语义）。
// AvgMessageLength 保留 DECIMAL 原文（Python Decimal 的字符串形态）。
type ChannelProfile struct {
	ChannelID        string
	ChannelName      sql.NullString
	Style            sql.NullString
	Topics           any
	KeywordsFreq     any
	Tone             sql.NullString
	AvgMessageLength sql.NullString
	TotalSummaries   sql.NullInt64
	LastUpdated      sql.NullTime
}

// getChannelProfile 获取频道画像（mysql.py:1096-1120 移植）。
// SELECT * 按 channel_profiles 建表列序位置扫描（mysql.py:201-213：
// channel_id, channel_name, style, topics, keywords_freq, tone,
// avg_message_length, total_summaries, last_updated；DDL 冻结于
// internal/db/schema.go）。缺行/异常均返回 (nil, nil)（Python 返回 None）。
func (m *Manager) getChannelProfile(ctx context.Context, channelID string) (*ChannelProfile, error) {
	if m.querier == nil {
		// Python：异常 → logger.error("获取频道画像失败: ...") → None
		slog.Error("获取频道画像失败: RuntimeError: 画像查询器未配置")
		return nil, nil
	}
	row := m.querier.QueryRowContext(ctx,
		"SELECT * FROM channel_profiles WHERE channel_id = ?", channelID)

	p := &ChannelProfile{}
	var topicsBytes, freqBytes []byte
	err := row.Scan(&p.ChannelID, &p.ChannelName, &p.Style, &topicsBytes, &freqBytes,
		&p.Tone, &p.AvgMessageLength, &p.TotalSummaries, &p.LastUpdated)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		slog.Error(fmt.Sprintf("获取频道画像失败: %T: %s", err, err))
		return nil, nil
	}

	// Python：try: if row["topics"]: json.loads ... except: pass
	// （单个 try 块；parseJSONText 不会抛错，顺序解析语义一致）。
	p.Topics = parseJSONText(topicsBytes)
	p.KeywordsFreq = parseJSONText(freqBytes)
	return p, nil
}

// GetChannelContext 获取频道上下文信息，用于 AI 生成回答
// （memory_manager.py:146-180，qa_engine_v3.py:237 与
// agent_tools.get_channel_info 消费）。异常路径按 Python 返回 ""。
func (m *Manager) GetChannelContext(ctx context.Context, channelID string) string {
	if channelID == "" {
		return "这是一个多频道总结系统。"
	}

	profile, err := m.getChannelProfile(ctx, channelID)
	if err != nil || profile == nil {
		// Python：无画像 → f"频道: {channel_id.split('/')[-1]}"
		return fmt.Sprintf("频道: %s", lastSegment(channelID))
	}

	// Python profile.get("style", "neutral")：行内键恒存在，NULL/未知值
	// 落到 style_map.get(style, '中立') 的默认分支。
	styleName, ok := styleMap[profile.Style.String]
	if !ok {
		styleName = "中立"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "频道特点: %s\n", styleName)

	// Python：if topics: context += f"常讨论主题: {', '.join(topics)}\n"
	if pythonTruthy(profile.Topics) {
		joined, errDetail, joinOK := pythonJoinComma(profile.Topics)
		if !joinOK {
			// Python join 的 TypeError 被外层 except 捕获：记日志返回 ""。
			slog.Error(fmt.Sprintf("获取频道上下文失败: TypeError: %s", errDetail))
			return ""
		}
		fmt.Fprintf(&b, "常讨论主题: %s\n", joined)
	}

	// Python f"{total_summaries}"：NULL → None → 输出字面量 "None"。
	total := "None"
	if profile.TotalSummaries.Valid {
		total = strconv.FormatInt(profile.TotalSummaries.Int64, 10)
	}
	b.WriteString("已总结次数: " + total)
	return b.String()
}
