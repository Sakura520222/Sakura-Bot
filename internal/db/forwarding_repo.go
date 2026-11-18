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
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"
)

// ForwardedRecord 转发消息持久化记录（对齐 mysql.py:2511-2548 与 forwarding_handler.py）。
//
// 键生成规则：
//   - MessageKey 非空时直接使用（例如单消息 "12345" 或相册媒体组 "group_987654321"）；
//   - 否则若 GroupedID != 0，生成 "group_<GroupedID>"；
//   - 否则格式化 SourceMsgID。
type ForwardedRecord struct {
	MessageKey    string
	SourceMsgID   int64
	GroupedID     int64
	SourceChannel string
	TargetChannel string
	ContentHash   string
	Timestamp     int64
	ForwardStatus string
	ErrorMessage  string
	MessageType   string
}

func (r ForwardedRecord) resolvedKey() string {
	if r.MessageKey != "" {
		return r.MessageKey
	}
	if r.GroupedID != 0 {
		return fmt.Sprintf("group_%d", r.GroupedID)
	}
	return strconv.FormatInt(r.SourceMsgID, 10)
}

// IsMessageForwarded 检查单条消息是否已转发到目标频道（mysql.py:2478-2510）。
func (m *Manager) IsMessageForwarded(ctx context.Context, sourceChannel string, sourceMsgID int64, targetChannel string) (bool, error) {
	return m.IsMessageKeyForwarded(ctx, sourceChannel, strconv.FormatInt(sourceMsgID, 10), targetChannel)
}

// IsMediaGroupForwarded 检查媒体组（group_<groupedID>）是否已转发到目标频道（forwarding_handler.py:587-591）。
func (m *Manager) IsMediaGroupForwarded(ctx context.Context, sourceChannel string, groupedID int64, targetChannel string) (bool, error) {
	return m.IsMessageKeyForwarded(ctx, sourceChannel, fmt.Sprintf("group_%d", groupedID), targetChannel)
}

// IsMessageKeyForwarded 按字符串消息键（单条 ID 或 group_<grouped_id>）检查转发去重。
// sourceChannel 非空时使用三字段主键匹配，空串时退回二字段兼容匹配（mysql.py:2485-2503）。
func (m *Manager) IsMessageKeyForwarded(ctx context.Context, sourceChannel, messageKey, targetChannel string) (bool, error) {
	var one sql.NullInt64
	var err error
	if sourceChannel != "" {
		err = m.DB.QueryRowContext(ctx, `
			SELECT 1 FROM forwarded_messages
			WHERE message_id = ? AND target_channel = ? AND source_channel = ?
			LIMIT 1
		`, messageKey, targetChannel, sourceChannel).Scan(&one)
	} else {
		err = m.DB.QueryRowContext(ctx, `
			SELECT 1 FROM forwarded_messages
			WHERE message_id = ? AND target_channel = ?
			LIMIT 1
		`, messageKey, targetChannel).Scan(&one)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		slog.Error(fmt.Sprintf("检查消息转发状态失败: %T: %s", err, err))
		return false, err
	}
	return true, nil
}

// RecordForwardedMessage 记录已转发消息（幂等 INSERT IGNORE，对齐 mysql.py:2511-2548）。
func (m *Manager) RecordForwardedMessage(ctx context.Context, rec ForwardedRecord) error {
	_, err := m.AddForwardedMessage(ctx, rec)
	return err
}

// AddForwardedMessage 记录已转发消息并返回是否为新插入行。
func (m *Manager) AddForwardedMessage(ctx context.Context, rec ForwardedRecord) (bool, error) {
	key := rec.resolvedKey()
	ts := rec.Timestamp
	if ts == 0 {
		ts = NowUTC().Unix()
	}
	status := rec.ForwardStatus
	if status == "" {
		status = "success"
	}
	msgType := rec.MessageType
	if msgType == "" {
		msgType = "unknown"
	}

	res, err := m.DB.ExecContext(ctx, `
		INSERT IGNORE INTO forwarded_messages
		(message_id, source_channel, target_channel, content_hash, timestamp, forward_status, error_message, message_type)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, key, rec.SourceChannel, rec.TargetChannel, nullableString(rec.ContentHash), ts, status, nullableString(rec.ErrorMessage), msgType)
	if err != nil {
		slog.Error(fmt.Sprintf("添加转发消息记录失败: %T: %s", err, err))
		return false, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if affected > 0 {
		slog.Debug(fmt.Sprintf("记录已转发消息: %s from %s to %s", key, rec.SourceChannel, rec.TargetChannel))
		return true, nil
	}
	return false, nil
}

// IncrementForwardingStat 增加转发统计计数（对齐 forwarding_stats 表结构与 mysql.py:2608-2634）。
// forwarding_stats 以源频道（缺失时回退 ruleName/targetChannel）聚合 total_forwarded 与 last_forwarded。
func (m *Manager) IncrementForwardingStat(ctx context.Context, ruleName, sourceChannel, targetChannel, _ string, forwardedDelta, _, _ int) error {
	if forwardedDelta <= 0 {
		return nil
	}
	channelKey := sourceChannel
	if channelKey == "" {
		channelKey = ruleName
	}
	if channelKey == "" {
		channelKey = targetChannel
	}
	if channelKey == "" {
		return nil
	}
	return m.UpdateForwardingStats(ctx, channelKey, forwardedDelta)
}

// UpdateForwardingStats 更新指定源频道的转发统计（mysql.py:2608-2634）。
func (m *Manager) UpdateForwardingStats(ctx context.Context, channelID string, increment int) error {
	now := NowUTC().Unix()
	_, err := m.DB.ExecContext(ctx, `
		INSERT INTO forwarding_stats (channel_id, total_forwarded, last_forwarded)
		VALUES (?, ?, ?)
		ON DUPLICATE KEY UPDATE
			total_forwarded = total_forwarded + ?,
			last_forwarded = ?,
			updated_at = NOW()
	`, channelID, increment, now, increment, now)
	if err != nil {
		slog.Error(fmt.Sprintf("更新转发统计失败: %T: %s", err, err))
		return err
	}
	slog.Debug(fmt.Sprintf("更新转发统计: %s +%d", channelID, increment))
	return nil
}

// GetForwardingStats 获取全局转发统计信息（mysql.py:2549-2607；days>0 时附带窗口内记录数）。
func (m *Manager) GetForwardingStats(ctx context.Context, days int) (map[string]any, error) {
	rows, err := m.DB.QueryContext(ctx, `
		SELECT channel_id, COALESCE(total_forwarded, 0), last_forwarded
		FROM forwarding_stats
		ORDER BY total_forwarded DESC
	`)
	if err != nil {
		slog.Error(fmt.Sprintf("获取转发统计失败: %T: %s", err, err))
		return nil, err
	}
	defer rows.Close() //nolint:errcheck // 只读游标

	byChannel := make([]map[string]any, 0)
	var totalAll int64
	for rows.Next() {
		var (
			chID     string
			totalFwd int64
			lastFwd  sql.NullInt64
		)
		if err := rows.Scan(&chID, &totalFwd, &lastFwd); err != nil {
			return nil, err
		}
		var lastVal any
		if lastFwd.Valid {
			lastVal = lastFwd.Int64
		}
		byChannel = append(byChannel, map[string]any{
			"channel_id":      chID,
			"total_forwarded": totalFwd,
			"last_forwarded":  lastVal,
		})
		totalAll += totalFwd
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := map[string]any{
		"total_all_channels": totalAll,
		"by_channel":         byChannel,
	}
	if days > 0 {
		cutoff := NowUTC().Add(-time.Duration(days) * 24 * time.Hour).Unix()
		var windowCount sql.NullInt64
		if err := m.DB.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM forwarded_messages WHERE timestamp >= ?", cutoff).Scan(&windowCount); err != nil {
			return nil, err
		}
		out["window_days"] = days
		out["recent_forwarded"] = windowCount.Int64
	}
	return out, nil
}

// GetChannelForwardingStats 获取单个频道的转发统计（对齐 mysql.py:2554-2577 /forwarding_stats <channel>）。
func (m *Manager) GetChannelForwardingStats(ctx context.Context, channelID string) (map[string]any, error) {
	var (
		totalFwd sql.NullInt64
		lastFwd  sql.NullInt64
	)
	err := m.DB.QueryRowContext(ctx, `
		SELECT total_forwarded, last_forwarded
		FROM forwarding_stats
		WHERE channel_id = ?
	`, channelID).Scan(&totalFwd, &lastFwd)
	if errors.Is(err, sql.ErrNoRows) {
		return map[string]any{
			"channel_id":      channelID,
			"total_forwarded": int64(0),
			"last_forwarded":  nil,
		}, nil
	}
	if err != nil {
		slog.Error(fmt.Sprintf("获取转发统计失败: %T: %s", err, err))
		return nil, err
	}
	var lastVal any
	if lastFwd.Valid {
		lastVal = lastFwd.Int64
	}
	return map[string]any{
		"channel_id":      channelID,
		"total_forwarded": totalFwd.Int64,
		"last_forwarded":  lastVal,
	}, nil
}

// CleanupForwardedMessages 清理超过 days 天的旧转发消息记录（mysql.py:2636-2659）。
func (m *Manager) CleanupForwardedMessages(ctx context.Context, days int) (int64, error) {
	cutoff := NowUTC().Add(-time.Duration(days) * 24 * time.Hour).Unix()
	res, err := m.DB.ExecContext(ctx, "DELETE FROM forwarded_messages WHERE timestamp < ?", cutoff)
	if err != nil {
		slog.Error(fmt.Sprintf("清理旧转发消息记录失败: %T: %s", err, err))
		return 0, err
	}
	deleted, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	slog.Info(fmt.Sprintf("清理旧转发消息记录: 删除 %d 条", deleted))
	return deleted, nil
}
