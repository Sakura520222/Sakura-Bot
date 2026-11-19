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
	"time"
)

// Notification 对齐 notification_queue 表行（SELECT * 列序）。
// Content 为解析后的 JSON 值：NULL → nil、解析失败 → 原始字符串
// （ParseJSONColumn，对齐 mysql.py:2043-2048 的 try json.loads / except: pass）。
type Notification struct {
	ID               int64
	UserID           int64
	NotificationType string
	Content          any
	Status           string
	CreatedAt        time.Time
	SentAt           sql.NullTime
}

// GetPendingNotifications 获取待发送通知（mysql.py:2025-2054）：
// pending、FIFO、LIMIT 50。运维红线（行为规格 §3.3）：通知队列保持
// 单消费者——QA 只允许单实例轮询，不加 FOR UPDATE SKIP LOCKED。
func (m *Manager) GetPendingNotifications(ctx context.Context, limit int) ([]Notification, error) {
	rows, err := m.DB.QueryContext(ctx, `
                        SELECT * FROM notification_queue
                        WHERE status = 'pending'
                        ORDER BY created_at ASC
                        LIMIT ?
                    `, limit)
	if err != nil {
		slog.Error(fmt.Sprintf("获取待发送通知失败: %T: %s", err, err))
		return nil, err
	}
	defer rows.Close() //nolint:errcheck // 只读游标

	var out []Notification
	for rows.Next() {
		var (
			n       Notification
			content []byte
		)
		if err := rows.Scan(&n.ID, &n.UserID, &n.NotificationType, &content,
			&n.Status, &n.CreatedAt, &n.SentAt); err != nil {
			slog.Error(fmt.Sprintf("获取待发送通知失败: %T: %s", err, err))
			return nil, err
		}
		n.Content = ParseJSONColumn(content)
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		slog.Error(fmt.Sprintf("获取待发送通知失败: %T: %s", err, err))
		return nil, err
	}
	return out, nil
}

// UpdateNotificationStatus 更新通知状态（mysql.py:2056-2077）：
// SET status=?, sent_at=NOW()——sent 与 failed 共用同一 UPDATE（QA 侧
// 发送失败同样标 failed 不重试，行为规格 §2.10 / R17）。
func (m *Manager) UpdateNotificationStatus(ctx context.Context, notificationID int64, status string) error {
	if _, err := m.DB.ExecContext(ctx, `
                        UPDATE notification_queue
                        SET status = ?, sent_at = NOW()
                        WHERE id = ?
                    `, status, notificationID); err != nil {
		slog.Error(fmt.Sprintf("更新通知状态失败: %T: %s", err, err))
		return err
	}

	slog.Info(fmt.Sprintf("更新通知状态: id=%d, status=%s", notificationID, status))
	return nil
}

// CleanupOldNotifications 清理旧通知记录（mysql.py:2079-2098）：
// 仅删除 sent/failed 且超过 days 天的行，返回删除行数。
func (m *Manager) CleanupOldNotifications(ctx context.Context, days int) (int64, error) {
	cutoff := MysqlDatetime(NowUTC().Add(-time.Duration(days) * 24 * time.Hour))

	res, err := m.DB.ExecContext(ctx, `
                        DELETE FROM notification_queue
                        WHERE created_at < ? AND status IN ('sent', 'failed')
                    `, cutoff)
	if err != nil {
		slog.Error(fmt.Sprintf("清理旧通知失败: %T: %s", err, err))
		return 0, err
	}

	deleted, err := res.RowsAffected()
	if err != nil {
		slog.Error(fmt.Sprintf("清理旧通知失败: %T: %s", err, err))
		return 0, err
	}

	slog.Info(fmt.Sprintf("清理旧通知: 删除 %d 条记录", deleted))
	return deleted, nil
}
