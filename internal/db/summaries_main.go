// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"
)

// SummaryInsert 主 Bot 保存频道总结的参数（对齐 mysql.py:464-518 save_summary）。
type SummaryInsert struct {
	ChannelID         string
	ChannelName       string
	SummaryText       string
	MessageCount      int
	StartTime         *time.Time
	EndTime           *time.Time
	SummaryMessageIDs []int64
	PollMessageID     *int64
	ButtonMessageID   *int64
	AIModel           string
	SummaryType       string
}

// SummaryRecord 面向主 Bot /history、/export 与 WebUI /api/stats/summaries 的结构化总结行。
type SummaryRecord struct {
	ID                int64      `json:"id"`
	ChannelID         string     `json:"channel_id"`
	ChannelName       string     `json:"channel_name"`
	SummaryText       string     `json:"summary_text"`
	MessageCount      int        `json:"message_count"`
	StartTime         *time.Time `json:"start_time"`
	EndTime           *time.Time `json:"end_time"`
	CreatedAt         time.Time  `json:"created_at"`
	AIModel           string     `json:"ai_model"`
	SummaryType       string     `json:"summary_type"`
	SummaryMessageIDs []int64    `json:"summary_message_ids"`
	PollMessageID     *int64     `json:"poll_message_id"`
	ButtonMessageID   *int64     `json:"button_message_id"`
	Keywords          any        `json:"keywords,omitempty"`
	Topics            any        `json:"topics,omitempty"`
	Sentiment         string     `json:"sentiment,omitempty"`
	Entities          any        `json:"entities,omitempty"`
}

// SummaryStats 总结统计数据（对齐 mysql.py:686-795 get_statistics）。
type SummaryStats struct {
	TotalCount      int64            `json:"total_count"`
	TypeStats       map[string]int64 `json:"type_stats"`
	TotalMessages   int64            `json:"total_messages"`
	AvgMessages     float64          `json:"avg_messages"`
	LastSummaryTime *string          `json:"last_summary_time"`
	WeekCount       int64            `json:"week_count"`
	MonthCount      int64            `json:"month_count"`
	WindowCount     int64            `json:"window_count,omitempty"`
}

// ChannelRankRow 频道总结排行行（对齐 mysql.py:796-822 get_channel_ranking）。
type ChannelRankRow struct {
	ChannelID     string `json:"channel_id"`
	ChannelName   string `json:"channel_name"`
	SummaryCount  int64  `json:"summary_count"`
	TotalMessages int64  `json:"total_messages"`
}

// ChannelProfile 频道画像结构（对齐 mysql.py:1096-1121 get_channel_profile）。
type ChannelProfile struct {
	ChannelID        string           `json:"channel_id"`
	ChannelName      string           `json:"channel_name"`
	Style            string           `json:"style"`
	Topics           []string         `json:"topics"`
	KeywordsFreq     map[string]int64 `json:"keywords_freq"`
	Tone             string           `json:"tone"`
	AvgMessageLength float64          `json:"avg_message_length"`
	TotalSummaries   int64            `json:"total_summaries"`
	LastUpdated      *time.Time       `json:"last_updated"`
}

const summaryColumnsSQL = `id, channel_id, channel_name, summary_text, message_count,
	start_time, end_time, created_at, ai_model, summary_type,
	summary_message_ids, poll_message_id, button_message_id,
	keywords, topics, sentiment, entities`

// SaveSummary 保存总结记录并返回自增 ID（mysql.py:464-518）。
func (m *Manager) SaveSummary(ctx context.Context, in SummaryInsert) (int64, error) {
	aiModel := in.AIModel
	if aiModel == "" {
		aiModel = "unknown"
	}
	summaryType := in.SummaryType
	if summaryType == "" {
		summaryType = "weekly"
	}

	var startArg, endArg, msgIDsArg, pollArg, btnArg any
	if in.StartTime != nil {
		startArg = MysqlDatetime(in.StartTime.UTC())
	}
	if in.EndTime != nil {
		endArg = MysqlDatetime(in.EndTime.UTC())
	}
	if len(in.SummaryMessageIDs) > 0 {
		encoded, err := EncodeJSON(in.SummaryMessageIDs)
		if err != nil {
			return 0, err
		}
		msgIDsArg = encoded
	}
	if in.PollMessageID != nil {
		pollArg = *in.PollMessageID
	}
	if in.ButtonMessageID != nil {
		btnArg = *in.ButtonMessageID
	}

	res, err := m.DB.ExecContext(ctx, `
		INSERT INTO summaries (
			channel_id, channel_name, summary_text, message_count,
			start_time, end_time, ai_model, summary_type,
			summary_message_ids, poll_message_id, button_message_id
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, in.ChannelID, nullableString(in.ChannelName), in.SummaryText, in.MessageCount,
		startArg, endArg, aiModel, summaryType, msgIDsArg, pollArg, btnArg)
	if err != nil {
		slog.Error(fmt.Sprintf("保存总结记录失败: %T: %s", err, err))
		return 0, err
	}

	summaryID, err := res.LastInsertId()
	if err != nil {
		slog.Error(fmt.Sprintf("保存总结记录失败: %T: %s", err, err))
		return 0, err
	}
	slog.Info(fmt.Sprintf("成功保存总结记录到MySQL, ID: %d, 频道: %s", summaryID, in.ChannelName))
	return summaryID, nil
}

// CountSummaries 统计符合条件的历史总结总数（mysql.py:573-618）。
func (m *Manager) CountSummaries(ctx context.Context, channelID string, startDate, endDate *time.Time) (int, error) {
	var (
		conditions []string
		params     []any
	)
	if channelID != "" {
		conditions = append(conditions, "channel_id = ?")
		params = append(params, channelID)
	}
	if startDate != nil {
		conditions = append(conditions, "created_at >= ?")
		params = append(params, MysqlDatetime(startDate.UTC()))
	}
	if endDate != nil {
		conditions = append(conditions, "created_at <= ?")
		params = append(params, MysqlDatetime(endDate.UTC()))
	}
	whereClause := "1=1"
	if len(conditions) > 0 {
		whereClause = strings.Join(conditions, " AND ")
	}

	var count sql.NullInt64
	if err := m.DB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM summaries WHERE "+whereClause, params...).Scan(&count); err != nil {
		slog.Error(fmt.Sprintf("统计总结记录失败: %T: %s", err, err))
		return 0, err
	}
	return int(count.Int64), nil
}

// ListSummaries 分页列出总结记录并返回总条数（对齐 stats.py:42-70 /api/stats/summaries）。
func (m *Manager) ListSummaries(ctx context.Context, channelID string, limit, offset int) ([]SummaryRecord, int, error) {
	if limit <= 0 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	total, err := m.CountSummaries(ctx, channelID, nil, nil)
	if err != nil {
		return nil, 0, err
	}

	whereClause := "1=1"
	var params []any
	if channelID != "" {
		whereClause = "channel_id = ?"
		params = append(params, channelID)
	}
	params = append(params, limit, offset)

	rows, err := m.DB.QueryContext(ctx,
		"SELECT "+summaryColumnsSQL+" FROM summaries WHERE "+whereClause+" ORDER BY created_at DESC LIMIT ? OFFSET ?",
		params...)
	if err != nil {
		slog.Error(fmt.Sprintf("查询总结记录失败: %T: %s", err, err))
		return nil, 0, err
	}
	defer rows.Close() //nolint:errcheck // 只读游标

	records, err := scanSummaryRecords(rows)
	if err != nil {
		slog.Error(fmt.Sprintf("查询总结记录失败: %T: %s", err, err))
		return nil, 0, err
	}
	return records, total, nil
}

// GetSummariesSince 按起始时间过滤获取总结列表（供 /history 与 /export 使用；since 为零值时不限起始时间）。
func (m *Manager) GetSummariesSince(ctx context.Context, channelID string, since time.Time, limit int) ([]SummaryRecord, error) {
	if limit <= 0 {
		limit = 10
	}
	var (
		conditions []string
		params     []any
	)
	if channelID != "" {
		conditions = append(conditions, "channel_id = ?")
		params = append(params, channelID)
	}
	if !since.IsZero() {
		conditions = append(conditions, "created_at >= ?")
		params = append(params, MysqlDatetime(since.UTC()))
	}
	whereClause := "1=1"
	if len(conditions) > 0 {
		whereClause = strings.Join(conditions, " AND ")
	}
	params = append(params, limit)

	rows, err := m.DB.QueryContext(ctx,
		"SELECT "+summaryColumnsSQL+" FROM summaries WHERE "+whereClause+" ORDER BY created_at DESC LIMIT ?",
		params...)
	if err != nil {
		slog.Error(fmt.Sprintf("查询总结记录失败: %T: %s", err, err))
		return nil, err
	}
	defer rows.Close() //nolint:errcheck // 只读游标

	return scanSummaryRecords(rows)
}

// GetSummaryByID 按 ID 获取单条总结记录（mysql.py:636-659；不存在返回 nil, nil）。
func (m *Manager) GetSummaryByID(ctx context.Context, summaryID int64) (*SummaryRecord, error) {
	rows, err := m.DB.QueryContext(ctx,
		"SELECT "+summaryColumnsSQL+" FROM summaries WHERE id = ? LIMIT 1", summaryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck // 只读游标

	list, err := scanSummaryRecords(rows)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, nil
	}
	return &list[0], nil
}

// DeleteOldSummaries 删除超过 days 天的旧总结记录（mysql.py:661-685）。
func (m *Manager) DeleteOldSummaries(ctx context.Context, days int) (int64, error) {
	cutoff := MysqlDatetime(NowUTC().Add(-time.Duration(days) * 24 * time.Hour))
	res, err := m.DB.ExecContext(ctx, "DELETE FROM summaries WHERE created_at < ?", cutoff)
	if err != nil {
		slog.Error(fmt.Sprintf("删除旧总结记录失败: %T: %s", err, err))
		return 0, err
	}
	deleted, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	slog.Info(fmt.Sprintf("已删除 %d 条旧总结记录 (超过 %d 天)", deleted, days))
	return deleted, nil
}

// GetSummaryStats 获取总结统计信息（对齐 mysql.py:686-795 get_statistics；days>0 时附带该窗口条数）。
func (m *Manager) GetSummaryStats(ctx context.Context, channelID string, days int) (*SummaryStats, error) {
	var (
		whereCond string
		params    []any
	)
	if channelID != "" {
		whereCond = "WHERE channel_id = ?"
		params = append(params, channelID)
	}

	var totalCount int64
	if err := m.DB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM summaries "+whereCond, params...).Scan(&totalCount); err != nil {
		return nil, err
	}

	typeRows, err := m.DB.QueryContext(ctx,
		"SELECT summary_type, COUNT(*) AS count FROM summaries "+whereCond+" GROUP BY summary_type",
		params...)
	if err != nil {
		return nil, err
	}
	typeStats := make(map[string]int64)
	for typeRows.Next() {
		var (
			st  sql.NullString
			cnt int64
		)
		if scanErr := typeRows.Scan(&st, &cnt); scanErr != nil {
			_ = typeRows.Close()
			return nil, scanErr
		}
		key := st.String
		if !st.Valid || key == "" {
			key = "unknown"
		}
		typeStats[key] = cnt
	}
	if err := typeRows.Err(); err != nil {
		_ = typeRows.Close()
		return nil, err
	}
	_ = typeRows.Close()

	var (
		totalMessages sql.NullInt64
		lastTime      sql.NullTime
	)
	if err := m.DB.QueryRowContext(ctx,
		"SELECT COALESCE(SUM(message_count), 0), MAX(created_at) FROM summaries "+whereCond,
		params...).Scan(&totalMessages, &lastTime); err != nil {
		return nil, err
	}

	avgMessages := 0.0
	if totalCount > 0 {
		avgMessages = math.Round((float64(totalMessages.Int64)/float64(totalCount))*10) / 10
	}

	now := NowUTC()
	weekCount, err := m.countSummariesSince(ctx, channelID, now.Add(-7*24*time.Hour))
	if err != nil {
		return nil, err
	}
	monthCount, err := m.countSummariesSince(ctx, channelID, now.Add(-30*24*time.Hour))
	if err != nil {
		return nil, err
	}

	var windowCount int64
	if days > 0 {
		windowCount, err = m.countSummariesSince(ctx, channelID, now.Add(-time.Duration(days)*24*time.Hour))
		if err != nil {
			return nil, err
		}
	}

	var lastSummaryISO *string
	if lastTime.Valid {
		iso := PythonISOFormat(lastTime.Time)
		lastSummaryISO = &iso
	}

	return &SummaryStats{
		TotalCount:      totalCount,
		TypeStats:       typeStats,
		TotalMessages:   totalMessages.Int64,
		AvgMessages:     avgMessages,
		LastSummaryTime: lastSummaryISO,
		WeekCount:       weekCount,
		MonthCount:      monthCount,
		WindowCount:     windowCount,
	}, nil
}

func (m *Manager) countSummariesSince(ctx context.Context, channelID string, since time.Time) (int64, error) {
	cutoff := MysqlDatetime(since.UTC())
	var cnt sql.NullInt64
	if channelID != "" {
		if err := m.DB.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM summaries WHERE channel_id = ? AND created_at >= ?",
			channelID, cutoff).Scan(&cnt); err != nil {
			return 0, err
		}
		return cnt.Int64, nil
	}
	if err := m.DB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM summaries WHERE created_at >= ?", cutoff).Scan(&cnt); err != nil {
		return 0, err
	}
	return cnt.Int64, nil
}

// GetWebStats 返回 WebUI /api/stats 与 /api/dashboard 所需的字典形态统计数据。
func (m *Manager) GetWebStats(ctx context.Context) (map[string]any, error) {
	s, err := m.GetSummaryStats(ctx, "", 0)
	if err != nil {
		return nil, err
	}
	var lastTime any
	if s.LastSummaryTime != nil {
		lastTime = *s.LastSummaryTime
	}
	return map[string]any{
		"total_count":       s.TotalCount,
		"type_stats":        s.TypeStats,
		"total_messages":    s.TotalMessages,
		"avg_messages":      s.AvgMessages,
		"last_summary_time": lastTime,
		"week_count":        s.WeekCount,
		"month_count":       s.MonthCount,
	}, nil
}

// GetChannelRanking 获取前 10 名频道总结排行（对齐 stats.py:73-86 /api/stats/ranking）。
func (m *Manager) GetChannelRanking(ctx context.Context) ([]ChannelRankRow, error) {
	return m.GetChannelRankingLimit(ctx, 10)
}

// GetChannelRankingLimit 获取指定数量上限的频道总结排行（mysql.py:796-822）。
func (m *Manager) GetChannelRankingLimit(ctx context.Context, limit int) ([]ChannelRankRow, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := m.DB.QueryContext(ctx, `
		SELECT
			channel_id,
			COALESCE(channel_name, '') AS channel_name,
			COUNT(*) AS summary_count,
			COALESCE(SUM(message_count), 0) AS total_messages
		FROM summaries
		GROUP BY channel_id, channel_name
		ORDER BY summary_count DESC
		LIMIT ?
	`, limit)
	if err != nil {
		slog.Error(fmt.Sprintf("获取频道排行失败: %T: %s", err, err))
		return nil, err
	}
	defer rows.Close() //nolint:errcheck // 只读游标

	out := make([]ChannelRankRow, 0)
	for rows.Next() {
		var r ChannelRankRow
		if err := rows.Scan(&r.ChannelID, &r.ChannelName, &r.SummaryCount, &r.TotalMessages); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ExportSummaries 导出全部总结记录（上限 10000 条，对齐 stats.py:89-102 /api/stats/export）。
func (m *Manager) ExportSummaries(ctx context.Context) ([]map[string]any, error) {
	return m.ExportSummariesForChannel(ctx, "", 10000)
}

// ExportSummariesForChannel 按可选频道导出总结记录字典列表（供 /export 命令与 WebUI 共用）。
func (m *Manager) ExportSummariesForChannel(ctx context.Context, channelID string, limit int) ([]map[string]any, error) {
	if limit <= 0 {
		limit = 10000
	}
	records, err := m.GetSummariesSince(ctx, channelID, time.Time{}, limit)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(records))
	for _, r := range records {
		out = append(out, summaryRecordToMap(r))
	}
	return out, nil
}

// UpdateSummaryMetadata 更新单条总结的关键词、主题、情感和实体元数据（mysql.py:1238-1268）。
func (m *Manager) UpdateSummaryMetadata(ctx context.Context, summaryID int64, keywords, topics []string, sentiment string, entities []string) error {
	var kwArg, topArg, sentArg, entArg any
	if len(keywords) > 0 {
		kwArg = EncodeJSONMust(keywords)
	}
	if len(topics) > 0 {
		topArg = EncodeJSONMust(topics)
	}
	if sentiment != "" {
		sentArg = sentiment
	}
	if len(entities) > 0 {
		entArg = EncodeJSONMust(entities)
	}
	_, err := m.DB.ExecContext(ctx, `
		UPDATE summaries
		SET keywords = ?, topics = ?, sentiment = ?, entities = ?
		WHERE id = ?
	`, kwArg, topArg, sentArg, entArg, summaryID)
	return err
}

// GetChannelProfile 读取频道画像（mysql.py:1096-1120；不存在返回 nil, nil）。
func (m *Manager) GetChannelProfile(ctx context.Context, channelID string) (*ChannelProfile, error) {
	var (
		p       ChannelProfile
		name    sql.NullString
		style   sql.NullString
		topics  []byte
		kwFreq  []byte
		tone    sql.NullString
		avgLen  sql.NullFloat64
		total   sql.NullInt64
		updated sql.NullTime
	)
	err := m.DB.QueryRowContext(ctx, `
		SELECT channel_id, channel_name, style, topics, keywords_freq,
		       tone, avg_message_length, total_summaries, last_updated
		FROM channel_profiles
		WHERE channel_id = ?
	`, channelID).Scan(&p.ChannelID, &name, &style, &topics, &kwFreq, &tone, &avgLen, &total, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	p.ChannelName = name.String
	p.Style = style.String
	p.Tone = tone.String
	p.AvgMessageLength = avgLen.Float64
	p.TotalSummaries = total.Int64
	if updated.Valid {
		t := updated.Time
		p.LastUpdated = &t
	}
	if len(topics) > 0 {
		_ = UnmarshalJSONCompat(topics, &p.Topics)
	}
	if len(kwFreq) > 0 {
		_ = UnmarshalJSONCompat(kwFreq, &p.KeywordsFreq)
	}
	if p.KeywordsFreq == nil {
		p.KeywordsFreq = make(map[string]int64)
	}
	return &p, nil
}

// UpdateChannelProfile 更新频道画像（对齐 mysql.py:1122-1236）。
func (m *Manager) UpdateChannelProfile(ctx context.Context, channelID, channelName string, keywords, topics []string, sentiment string, _ []string) error {
	existing, err := m.GetChannelProfile(ctx, channelID)
	if err != nil {
		return err
	}

	var (
		totalSummaries sql.NullInt64
		avgMsgLen      sql.NullFloat64
	)
	if err := m.DB.QueryRowContext(ctx, `
		SELECT COUNT(*) AS count, AVG(message_count) AS avg_len
		FROM summaries
		WHERE channel_id = ?
	`, channelID).Scan(&totalSummaries, &avgMsgLen); err != nil {
		return err
	}

	kwFreq := make(map[string]int64)
	if existing != nil {
		for k, v := range existing.KeywordsFreq {
			kwFreq[k] = v
		}
	}
	for _, kw := range keywords {
		if kw != "" {
			kwFreq[kw]++
		}
	}

	var topicsArg, kwArg any
	if len(topics) > 0 {
		topicsArg = EncodeJSONMust(topics)
	}
	if len(kwFreq) > 0 {
		kwArg = EncodeJSONMust(kwFreq)
	}

	style := "neutral"
	if len(topics) > 0 {
		joined := strings.Join(topics, " ")
		style = "casual"
		for _, techKW := range []string{"AI", "编程", "技术", "开发", "Python", "GPT", "API"} {
			if strings.Contains(joined, techKW) {
				style = "tech"
				break
			}
		}
	}
	tone := sentiment
	if tone == "" {
		tone = "neutral"
	}
	now := MysqlDatetime(NowUTC())

	if existing != nil {
		_, err = m.DB.ExecContext(ctx, `
			UPDATE channel_profiles
			SET channel_name = ?, style = ?, topics = ?,
			    keywords_freq = ?, tone = ?, avg_message_length = ?,
			    total_summaries = ?, last_updated = ?
			WHERE channel_id = ?
		`, channelName, style, topicsArg, kwArg, tone, avgMsgLen.Float64, totalSummaries.Int64, now, channelID)
		return err
	}
	_, err = m.DB.ExecContext(ctx, `
		INSERT INTO channel_profiles (
			channel_id, channel_name, style, topics, keywords_freq,
			tone, avg_message_length, total_summaries, last_updated
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, channelID, channelName, style, topicsArg, kwArg, tone, avgMsgLen.Float64, totalSummaries.Int64, now)
	return err
}

// GetQABotStatistics 获取问答 Bot 全局统计（对齐 mysql.py:2165-2300 get_qa_bot_statistics）。
func (m *Manager) GetQABotStatistics(ctx context.Context) (map[string]any, error) {
	now := NowUTC()
	today := QuotaDate(now)
	weekStart := QuotaDate(now.Add(-7 * 24 * time.Hour))

	scalarInt := func(query string, args ...any) (int64, error) {
		var n sql.NullInt64
		if err := m.DB.QueryRowContext(ctx, query, args...).Scan(&n); err != nil {
			return 0, err
		}
		return n.Int64, nil
	}

	totalUsers, err := scalarInt("SELECT COUNT(*) FROM users")
	if err != nil {
		return nil, err
	}
	activeUsers, err := scalarInt("SELECT COUNT(DISTINCT user_id) FROM usage_quota WHERE query_date >= ?", weekStart)
	if err != nil {
		return nil, err
	}
	newUsersToday, err := scalarInt("SELECT COUNT(*) FROM users WHERE DATE(registered_at) = ?", today)
	if err != nil {
		return nil, err
	}
	queriesToday, err := scalarInt("SELECT COALESCE(SUM(usage_count), 0) FROM usage_quota WHERE query_date = ?", today)
	if err != nil {
		return nil, err
	}
	queriesWeek, err := scalarInt("SELECT COALESCE(SUM(usage_count), 0) FROM usage_quota WHERE query_date >= ?", weekStart)
	if err != nil {
		return nil, err
	}
	totalQueries, err := scalarInt("SELECT COALESCE(SUM(usage_count), 0) FROM usage_quota")
	if err != nil {
		return nil, err
	}
	totalSubs, err := scalarInt("SELECT COUNT(*) FROM subscriptions WHERE enabled = TRUE")
	if err != nil {
		return nil, err
	}
	activeSubs, err := scalarInt(`
		SELECT COUNT(DISTINCT s.user_id) FROM subscriptions s
		INNER JOIN usage_quota q ON s.user_id = q.user_id
		WHERE s.enabled = TRUE AND q.query_date >= ?
	`, weekStart)
	if err != nil {
		return nil, err
	}
	pendingReqs, err := scalarInt("SELECT COUNT(*) FROM request_queue WHERE status = 'pending'")
	if err != nil {
		return nil, err
	}
	completedToday, err := scalarInt(`
		SELECT COUNT(*) FROM request_queue
		WHERE status IN ('completed', 'failed') AND DATE(processed_at) = ?
	`, today)
	if err != nil {
		return nil, err
	}
	totalReqs, err := scalarInt("SELECT COUNT(*) FROM request_queue")
	if err != nil {
		return nil, err
	}

	topRows, err := m.DB.QueryContext(ctx, `
		SELECT u.user_id, u.username, u.first_name, SUM(q.usage_count) AS query_count
		FROM users u
		INNER JOIN usage_quota q ON u.user_id = q.user_id
		GROUP BY u.user_id, u.username, u.first_name
		ORDER BY query_count DESC
		LIMIT 10
	`)
	if err != nil {
		return nil, err
	}
	topUsers := make([]map[string]any, 0)
	for topRows.Next() {
		var (
			uid       int64
			uname, fn sql.NullString
			qcnt      int64
		)
		if scanErr := topRows.Scan(&uid, &uname, &fn, &qcnt); scanErr != nil {
			_ = topRows.Close()
			return nil, scanErr
		}
		var uVal, fnVal any
		if uname.Valid {
			uVal = uname.String
		}
		if fn.Valid {
			fnVal = fn.String
		}
		topUsers = append(topUsers, map[string]any{
			"user_id":     uid,
			"username":    uVal,
			"first_name":  fnVal,
			"query_count": qcnt,
		})
	}
	if err := topRows.Err(); err != nil {
		_ = topRows.Close()
		return nil, err
	}
	_ = topRows.Close()

	subRows, err := m.DB.QueryContext(ctx, `
		SELECT channel_name, COUNT(*) AS count
		FROM subscriptions
		WHERE enabled = TRUE AND channel_name IS NOT NULL
		GROUP BY channel_name
		ORDER BY count DESC
	`)
	if err != nil {
		return nil, err
	}
	channelSubs := make(map[string]int64)
	for subRows.Next() {
		var (
			chName string
			cnt    int64
		)
		if scanErr := subRows.Scan(&chName, &cnt); scanErr != nil {
			_ = subRows.Close()
			return nil, scanErr
		}
		channelSubs[chName] = cnt
	}
	if err := subRows.Err(); err != nil {
		_ = subRows.Close()
		return nil, err
	}
	_ = subRows.Close()

	return map[string]any{
		"total_users":              totalUsers,
		"active_users":             activeUsers,
		"new_users_today":          newUsersToday,
		"queries_today":            queriesToday,
		"queries_week":             queriesWeek,
		"total_queries":            totalQueries,
		"total_subscriptions":      totalSubs,
		"active_subscriptions":     activeSubs,
		"pending_requests":         pendingReqs,
		"completed_requests_today": completedToday,
		"total_requests":           totalReqs,
		"top_users":                topUsers,
		"channel_subscriptions":    channelSubs,
	}, nil
}

func scanSummaryRecords(rows *sql.Rows) ([]SummaryRecord, error) {
	out := make([]SummaryRecord, 0)
	for rows.Next() {
		var (
			r         SummaryRecord
			chName    sql.NullString
			msgCnt    sql.NullInt64
			startAt   sql.NullTime
			endAt     sql.NullTime
			createdAt sql.NullTime
			aiModel   sql.NullString
			sumType   sql.NullString
			msgIDsRaw []byte
			pollID    sql.NullInt64
			btnID     sql.NullInt64
			kwRaw     []byte
			topicsRaw []byte
			sentiment sql.NullString
			entRaw    []byte
		)
		if err := rows.Scan(
			&r.ID, &r.ChannelID, &chName, &r.SummaryText, &msgCnt,
			&startAt, &endAt, &createdAt, &aiModel, &sumType,
			&msgIDsRaw, &pollID, &btnID, &kwRaw, &topicsRaw, &sentiment, &entRaw,
		); err != nil {
			return nil, err
		}
		r.ChannelName = chName.String
		r.MessageCount = int(msgCnt.Int64)
		if startAt.Valid {
			t := startAt.Time
			r.StartTime = &t
		}
		if endAt.Valid {
			t := endAt.Time
			r.EndTime = &t
		}
		if createdAt.Valid {
			r.CreatedAt = createdAt.Time
		}
		r.AIModel = aiModel.String
		r.SummaryType = sumType.String
		if r.SummaryType == "" {
			r.SummaryType = "weekly"
		}
		r.SummaryMessageIDs = parseInt64SliceJSON(msgIDsRaw)
		if pollID.Valid {
			v := pollID.Int64
			r.PollMessageID = &v
		}
		if btnID.Valid {
			v := btnID.Int64
			r.ButtonMessageID = &v
		}
		r.Keywords = ParseJSONColumn(kwRaw)
		r.Topics = ParseJSONColumn(topicsRaw)
		r.Sentiment = sentiment.String
		r.Entities = ParseJSONColumn(entRaw)
		out = append(out, r)
	}
	return out, rows.Err()
}

func parseInt64SliceJSON(raw []byte) []int64 {
	if len(raw) == 0 {
		return []int64{}
	}
	var ids []int64
	if err := json.Unmarshal(raw, &ids); err == nil {
		if ids == nil {
			return []int64{}
		}
		return ids
	}
	var generic []any
	if err := json.Unmarshal(raw, &generic); err == nil {
		out := make([]int64, 0, len(generic))
		for _, item := range generic {
			switch v := item.(type) {
			case float64:
				out = append(out, int64(v))
			case int64:
				out = append(out, v)
			}
		}
		return out
	}
	return []int64{}
}

func summaryRecordToMap(r SummaryRecord) map[string]any {
	var startVal, endVal, createdVal, pollVal, btnVal, chNameVal, aiVal, sentVal any
	if r.ChannelName != "" {
		chNameVal = r.ChannelName
	}
	if r.StartTime != nil {
		startVal = PythonISOFormat(*r.StartTime)
	}
	if r.EndTime != nil {
		endVal = PythonISOFormat(*r.EndTime)
	}
	if !r.CreatedAt.IsZero() {
		createdVal = PythonISOFormat(r.CreatedAt)
	}
	if r.AIModel != "" {
		aiVal = r.AIModel
	}
	if r.PollMessageID != nil {
		pollVal = *r.PollMessageID
	}
	if r.ButtonMessageID != nil {
		btnVal = *r.ButtonMessageID
	}
	if r.Sentiment != "" {
		sentVal = r.Sentiment
	}
	return map[string]any{
		"id":                  r.ID,
		"channel_id":          r.ChannelID,
		"channel_name":        chNameVal,
		"summary_text":        r.SummaryText,
		"message_count":       r.MessageCount,
		"start_time":          startVal,
		"end_time":            endVal,
		"created_at":          createdVal,
		"ai_model":            aiVal,
		"summary_type":        r.SummaryType,
		"summary_message_ids": r.SummaryMessageIDs,
		"poll_message_id":     pollVal,
		"button_message_id":   btnVal,
		"keywords":            r.Keywords,
		"topics":              r.Topics,
		"sentiment":           sentVal,
		"entities":            r.Entities,
	}
}

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
