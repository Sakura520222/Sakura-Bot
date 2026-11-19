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
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// Summary 对齐 summaries 表行（SELECT * 列序）。
// Keywords/Topics/Entities 保留原始 JSON 列文本（Python DictCursor 得到 str，
// 由 memory/tools 消费方按 R13 双解析兼容处理）；SummaryMessageIDs 在
// _parse_summary_rows 等价处理：非空即 json 解析、失败或空值置空列表。
type Summary struct {
	ID                int64
	ChannelID         string
	ChannelName       sql.NullString
	SummaryText       string
	MessageCount      sql.NullInt64
	StartTime         sql.NullTime
	EndTime           sql.NullTime
	CreatedAt         sql.NullTime
	AIModel           sql.NullString
	SummaryType       sql.NullString
	SummaryMessageIDs any // list；解析失败/空值 → []any{}
	PollMessageID     sql.NullInt64
	ButtonMessageID   sql.NullInt64
	Keywords          []byte
	Topics            []byte
	Sentiment         sql.NullString
	Entities          []byte
}

// GetSummaries 查询历史总结（mysql.py:519-571）：动态 WHERE + 按时间倒序
// 分页。channelID 传空串等价 None；startDate/endDate 为 nil 时跳过条件
// （Python 侧仅剥离 tzinfo 后传参，Go 调用方约定传 UTC 值）。
func (m *Manager) GetSummaries(ctx context.Context, channelID string, limit, offset int, startDate, endDate *time.Time) ([]Summary, error) {
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
		params = append(params, MysqlDatetime(*startDate))
	}
	if endDate != nil {
		conditions = append(conditions, "created_at <= ?")
		params = append(params, MysqlDatetime(*endDate))
	}

	whereClause := "1=1"
	if len(conditions) > 0 {
		whereClause = strings.Join(conditions, " AND ")
	}

	query := fmt.Sprintf(`
                        SELECT * FROM summaries
                        WHERE %s
                        ORDER BY created_at DESC
                        LIMIT ? OFFSET ?
                    `, whereClause)
	params = append(params, limit, offset)

	rows, err := m.DB.QueryContext(ctx, query, params...)
	if err != nil {
		slog.Error(fmt.Sprintf("查询总结记录失败: %T: %s", err, err))
		return nil, err
	}
	defer rows.Close() //nolint:errcheck // 只读游标

	out, err := scanSummaries(rows)
	if err != nil {
		slog.Error(fmt.Sprintf("查询总结记录失败: %T: %s", err, err))
		return nil, err
	}

	slog.Info(fmt.Sprintf("查询到 %d 条总结记录", len(out)))
	return out, nil
}

// GetRecentSummaries 获取最近总结列表（mysql.py:1808-1821）：转发 get_summaries。
func (m *Manager) GetRecentSummaries(ctx context.Context, channelID string, limit int, startDate, endDate *time.Time) ([]Summary, error) {
	return m.GetSummaries(ctx, channelID, limit, 0, startDate, endDate)
}

// ChannelSummaryStats 对齐 get_channel_summary_stats 的返回字典
// （mysql.py:1770-1806）。缺行时返回默认聚合（Python 的兜底字典：
// channel_name 取 URL 尾段、计数为 0、时间为零值）。
type ChannelSummaryStats struct {
	ChannelID        string
	ChannelName      string
	SummaryCount     int64
	MessageCount     int64
	LastSummaryTime  sql.NullTime
	FirstSummaryTime sql.NullTime
}

// GetChannelSummaryStats 获取指定频道的总结统计（mysql.py:1770-1806）。
func (m *Manager) GetChannelSummaryStats(ctx context.Context, channelID string) (ChannelSummaryStats, error) {
	stats := ChannelSummaryStats{
		ChannelID:   channelID,
		ChannelName: lastPathSegment(channelID),
	}

	var (
		channelName sql.NullString
		summaryCnt  sql.NullInt64
		messageCnt  sql.NullInt64
	)
	err := m.DB.QueryRowContext(ctx, `
                        SELECT
                            channel_id,
                            COALESCE(MAX(NULLIF(channel_name, '')), channel_id) AS channel_name,
                            COUNT(*) AS summary_count,
                            COALESCE(SUM(message_count), 0) AS message_count,
                            MAX(created_at) AS last_summary_time,
                            MIN(created_at) AS first_summary_time
                        FROM summaries
                        WHERE channel_id = ?
                        GROUP BY channel_id
                    `, channelID).Scan(&stats.ChannelID, &channelName, &summaryCnt, &messageCnt,
		&stats.LastSummaryTime, &stats.FirstSummaryTime)
	if err == sql.ErrNoRows {
		// Python 缺行分支：返回上面的默认聚合。
		return stats, nil
	}
	if err != nil {
		slog.Error(fmt.Sprintf("获取频道统计失败: %T: %s", err, err))
		return ChannelSummaryStats{}, err
	}

	if channelName.Valid {
		stats.ChannelName = channelName.String
	}
	if summaryCnt.Valid {
		stats.SummaryCount = summaryCnt.Int64
	}
	if messageCnt.Valid {
		stats.MessageCount = messageCnt.Int64
	}
	return stats, nil
}

// scanSummaries 按 summaries 表 SELECT * 的列序扫描并做
// _parse_summary_rows（mysql.py:619-634）等价的 summary_message_ids 解析。
func scanSummaries(rows *sql.Rows) ([]Summary, error) {
	var out []Summary
	for rows.Next() {
		var (
			s          Summary
			summaryIDs []byte
		)
		if err := rows.Scan(&s.ID, &s.ChannelID, &s.ChannelName, &s.SummaryText,
			&s.MessageCount, &s.StartTime, &s.EndTime, &s.CreatedAt,
			&s.AIModel, &s.SummaryType, &summaryIDs, &s.PollMessageID,
			&s.ButtonMessageID, &s.Keywords, &s.Topics, &s.Sentiment, &s.Entities); err != nil {
			return nil, err
		}
		s.SummaryMessageIDs = parseSummaryMessageIDs(summaryIDs)
		out = append(out, s)
	}
	return out, rows.Err()
}

// parseSummaryMessageIDs 对齐 _parse_summary_rows：真值 → json 解析，
// 失败 → 空列表；空值 → 空列表（mysql.py:624-630）。
func parseSummaryMessageIDs(data []byte) any {
	if len(data) == 0 {
		return []any{}
	}
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return []any{}
	}
	return v
}

// lastPathSegment 等价 Python channel_id.split("/")[-1]
// （https://t.me/foo → foo；以 / 结尾时等价 Python 返回空串）。
func lastPathSegment(s string) string {
	if i := strings.LastIndex(s, "/"); i >= 0 {
		return s[i+1:]
	}
	return s
}
