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
	"fmt"
	"log/slog"
	"strings"
)

// Subscription 对齐 subscriptions 表行（get_user_subscriptions 返回的
// DictCursor 字典）；channelName.Valid=false 等价 Python None。
type Subscription struct {
	ID          int64
	UserID      int64
	ChannelID   string
	ChannelName sql.NullString
	SubType     sql.NullString
	Enabled     bool
	CreatedAt   sql.NullTime
}

// AddSubscription 添加订阅（mysql.py:1605-1635）。
// INSERT IGNORE + 唯一键(user_id, channel_id, sub_type)：受影响行数 0
// 表示“已订阅”，返回 false；新增成功返回 true。
// channelName 传空串写 NULL（qa_user_system 调用侧恒有值）。
func (m *Manager) AddSubscription(ctx context.Context, userID int64, channelID, channelName, subType string) (bool, error) {
	var channelNameArg any
	if channelName != "" {
		channelNameArg = channelName
	}

	res, err := m.DB.ExecContext(ctx, `
                        INSERT IGNORE INTO subscriptions
                        (user_id, channel_id, channel_name, sub_type)
                        VALUES (?, ?, ?, ?)
                    `, userID, channelID, channelNameArg, subType)
	if err != nil {
		slog.Error(fmt.Sprintf("添加订阅失败: %T: %s", err, err))
		return false, err
	}

	affected, err := res.RowsAffected()
	if err != nil {
		slog.Error(fmt.Sprintf("添加订阅失败: %T: %s", err, err))
		return false, err
	}

	if affected > 0 {
		slog.Info(fmt.Sprintf("添加订阅成功: user_id=%d, channel=%s, type=%s", userID, channelName, subType))
		return true, nil
	}
	slog.Info(fmt.Sprintf("订阅已存在: user_id=%d, channel=%s", userID, channelName))
	return false, nil
}

// RemoveSubscription 移除订阅（mysql.py:1637-1666）。
// channelID / subType 传空串等价 Python None（跳过对应条件）；返回删除行数。
func (m *Manager) RemoveSubscription(ctx context.Context, userID int64, channelID, subType string) (int64, error) {
	conditions := []string{"user_id = ?"}
	params := []any{userID}

	if channelID != "" {
		conditions = append(conditions, "channel_id = ?")
		params = append(params, channelID)
	}
	if subType != "" {
		conditions = append(conditions, "sub_type = ?")
		params = append(params, subType)
	}

	query := "DELETE FROM subscriptions WHERE " + strings.Join(conditions, " AND ")
	res, err := m.DB.ExecContext(ctx, query, params...)
	if err != nil {
		slog.Error(fmt.Sprintf("移除订阅失败: %T: %s", err, err))
		return 0, err
	}

	deleted, err := res.RowsAffected()
	if err != nil {
		slog.Error(fmt.Sprintf("移除订阅失败: %T: %s", err, err))
		return 0, err
	}

	slog.Info(fmt.Sprintf("移除订阅: user_id=%d, 删除%d条", userID, deleted))
	return deleted, nil
}

// GetUserSubscriptions 获取用户的订阅列表（mysql.py:1668-1698）：
// 仅 enabled=TRUE，ORDER BY created_at DESC。subType 传空串等价 None。
func (m *Manager) GetUserSubscriptions(ctx context.Context, userID int64, subType string) ([]Subscription, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if subType != "" {
		rows, err = m.DB.QueryContext(ctx, `
                            SELECT * FROM subscriptions
                            WHERE user_id = ? AND sub_type = ? AND enabled = TRUE
                            ORDER BY created_at DESC
                        `, userID, subType)
	} else {
		rows, err = m.DB.QueryContext(ctx, `
                            SELECT * FROM subscriptions
                            WHERE user_id = ? AND enabled = TRUE
                            ORDER BY created_at DESC
                        `, userID)
	}
	if err != nil {
		slog.Error(fmt.Sprintf("获取用户订阅失败: %T: %s", err, err))
		return nil, err
	}
	defer rows.Close() //nolint:errcheck // 只读游标

	out, err := scanSubscriptions(rows)
	if err != nil {
		slog.Error(fmt.Sprintf("获取用户订阅失败: %T: %s", err, err))
		return nil, err
	}
	return out, nil
}

// IsSubscribed 检查用户是否已订阅某频道（mysql.py:1823-1842）。
func (m *Manager) IsSubscribed(ctx context.Context, userID int64, channelID, subType string) (bool, error) {
	var one sql.NullInt64
	err := m.DB.QueryRowContext(ctx, `
                        SELECT 1 FROM subscriptions
                        WHERE user_id = ? AND channel_id = ?
                              AND sub_type = ? AND enabled = TRUE
                    `, userID, channelID, subType).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		slog.Error(fmt.Sprintf("检查订阅状态失败: %T: %s", err, err))
		return false, err
	}
	return true, nil
}

// scanSubscriptions 按 subscriptions 表列序扫描 SELECT * 结果
// （id, user_id, channel_id, channel_name, sub_type, enabled, created_at）。
func scanSubscriptions(rows *sql.Rows) ([]Subscription, error) {
	var out []Subscription
	for rows.Next() {
		var s Subscription
		if err := rows.Scan(&s.ID, &s.UserID, &s.ChannelID, &s.ChannelName,
			&s.SubType, &s.Enabled, &s.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
