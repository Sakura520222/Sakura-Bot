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
	"strings"
	"time"
)

// ForwardingMigrationResult 转发消息表迁移结果（对齐 migrate_forwarding_table_v1.py）。
type ForwardingMigrationResult struct {
	Success          bool
	Message          string
	Skipped          bool
	AlreadyUpdated   bool
	TotalRecords     int
	RecoveredRecords int
	LostRecords      int
}

// ReviewedAtFixResult submissions.reviewed_at 修复结果（对齐 fix_reviewed_at_datetime.py）。
type ReviewedAtFixResult struct {
	Success      bool
	Message      string
	FixedCount   int
	InvalidCount int
}

const forwardingTableV1DDL = `CREATE TABLE forwarded_messages (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    message_id VARCHAR(255) NOT NULL,
    source_channel VARCHAR(255) NOT NULL,
    target_channel VARCHAR(255) NOT NULL,
    content_hash VARCHAR(64),
    timestamp BIGINT NOT NULL,
    forward_status VARCHAR(20) DEFAULT 'success',
    error_message TEXT,
    message_type VARCHAR(50) DEFAULT 'unknown',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    UNIQUE KEY unique_message (message_id, source_channel(191), target_channel(191)),
    INDEX idx_source (source_channel(191)),
    INDEX idx_target (target_channel(191)),
    INDEX idx_timestamp (timestamp)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`

// RunMigrations 顺序执行历史数据库迁移（对齐 DatabaseInitializer._run_migrations）。
func (m *Manager) RunMigrations(ctx context.Context) error {
	slog.Info("检查并执行数据库迁移...")
	var errs []error
	if err := m.EnsureForwardingTableUpdated(ctx); err != nil {
		slog.Warn(fmt.Sprintf("数据库迁移执行失败（可忽略）: %T: %v", err, err))
		errs = append(errs, err)
	}
	if err := m.EnsureReviewedAtDatetimeFixed(ctx); err != nil {
		slog.Warn(fmt.Sprintf("datetime 格式修复执行失败（可忽略）: %T: %v", err, err))
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// EnsureForwardingTableUpdated 确保 forwarded_messages 表升级到三字段唯一键与扩展状态列结构
// （移植 core/migrations/migrate_forwarding_table_v1.py::ensure_forwarding_table_updated）。
func (m *Manager) EnsureForwardingTableUpdated(ctx context.Context) error {
	res, err := m.MigrateForwardingTable(ctx)
	if err != nil {
		slog.Error(fmt.Sprintf("❌ 转发消息表迁移异常: %T: %v", err, err))
		return err
	}
	slog.Info(fmt.Sprintf("✅ 转发消息表迁移完成: %s", res.Message))
	return nil
}

// MigrateForwardingTable 检查并迁移旧版 forwarded_messages 表。
func (m *Manager) MigrateForwardingTable(ctx context.Context) (*ForwardingMigrationResult, error) {
	conn, err := m.DB.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close() //nolint:errcheck // 归还池连接

	var tableName string
	err = conn.QueryRowContext(ctx, `
		SELECT TABLE_NAME FROM INFORMATION_SCHEMA.TABLES
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'forwarded_messages'
	`).Scan(&tableName)
	if errors.Is(err, sql.ErrNoRows) {
		slog.Info("forwarded_messages 表不存在，无需迁移")
		return &ForwardingMigrationResult{
			Success: true,
			Message: "表不存在，无需迁移",
			Skipped: true,
		}, nil
	}
	if err != nil {
		return nil, err
	}

	colRows, err := conn.QueryContext(ctx, `
		SELECT COLUMN_NAME FROM INFORMATION_SCHEMA.COLUMNS
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'forwarded_messages'
	`)
	if err != nil {
		return nil, err
	}
	columns := make(map[string]bool)
	for colRows.Next() {
		var col string
		if scanErr := colRows.Scan(&col); scanErr != nil {
			_ = colRows.Close()
			return nil, scanErr
		}
		columns[strings.ToLower(col)] = true
	}
	if err := colRows.Err(); err != nil {
		_ = colRows.Close()
		return nil, err
	}
	_ = colRows.Close()

	if columns["source_channel"] && columns["forward_status"] {
		slog.Info("表结构已是最新的，无需迁移")
		return &ForwardingMigrationResult{
			Success:        true,
			Message:        "表结构已是最新的",
			AlreadyUpdated: true,
		}, nil
	}

	slog.Info("开始迁移 forwarded_messages 表结构...")

	type backupRow struct {
		messageID     string
		sourceChannel string
		targetChannel string
		contentHash   sql.NullString
		timestamp     int64
	}

	selectSQL := `
		SELECT message_id, 'unknown', target_channel, content_hash, timestamp
		FROM forwarded_messages
	`
	if columns["source_channel"] {
		selectSQL = `
			SELECT message_id, COALESCE(NULLIF(source_channel, ''), 'unknown'), target_channel, content_hash, timestamp
			FROM forwarded_messages
		`
	}

	dataRows, err := conn.QueryContext(ctx, selectSQL)
	if err != nil {
		return nil, err
	}
	var existing []backupRow
	for dataRows.Next() {
		var r backupRow
		if scanErr := dataRows.Scan(&r.messageID, &r.sourceChannel, &r.targetChannel, &r.contentHash, &r.timestamp); scanErr != nil {
			_ = dataRows.Close()
			return nil, scanErr
		}
		existing = append(existing, r)
	}
	if err := dataRows.Err(); err != nil {
		_ = dataRows.Close()
		return nil, err
	}
	_ = dataRows.Close()

	slog.Info(fmt.Sprintf("备份了 %d 条现有记录", len(existing)))

	if _, err := conn.ExecContext(ctx, "DROP TABLE IF EXISTS forwarded_messages"); err != nil {
		return nil, err
	}
	if _, err := conn.ExecContext(ctx, forwardingTableV1DDL); err != nil {
		return nil, err
	}

	recovered := 0
	for _, r := range existing {
		var hashArg any
		if r.contentHash.Valid {
			hashArg = r.contentHash.String
		}
		if _, insErr := conn.ExecContext(ctx, `
			INSERT IGNORE INTO forwarded_messages
			(message_id, source_channel, target_channel, content_hash, timestamp, forward_status)
			VALUES (?, ?, ?, ?, ?, 'success')
		`, r.messageID, r.sourceChannel, r.targetChannel, hashArg, r.timestamp); insErr != nil {
			slog.Warn(fmt.Sprintf("恢复转发记录失败: %v", insErr))
			continue
		}
		recovered++
	}

	slog.Info(fmt.Sprintf("迁移完成: 恢复了 %d/%d 条记录", recovered, len(existing)))
	return &ForwardingMigrationResult{
		Success:          true,
		Message:          "MySQL 表迁移成功",
		TotalRecords:     len(existing),
		RecoveredRecords: recovered,
		LostRecords:      len(existing) - recovered,
	}, nil
}

// EnsureReviewedAtDatetimeFixed 修复 submissions.reviewed_at 历史遗留 ISO 时间字符串
// （移植 core/migrations/fix_reviewed_at_datetime.py::ensure_reviewed_at_datetime_fixed）。
func (m *Manager) EnsureReviewedAtDatetimeFixed(ctx context.Context) error {
	res, err := m.FixReviewedAtDatetime(ctx)
	if err != nil {
		slog.Error(fmt.Sprintf("❌ reviewed_at datetime 格式修复异常: %T: %v", err, err))
		return err
	}
	slog.Info(fmt.Sprintf("✅ reviewed_at datetime 格式修复完成: %s", res.Message))
	return nil
}

// FixReviewedAtDatetime 查找并修复包含 'T' 的 ISO 格式 reviewed_at 值。
func (m *Manager) FixReviewedAtDatetime(ctx context.Context) (*ReviewedAtFixResult, error) {
	// 使用 CAST(reviewed_at AS CHAR) 防止 go-sql-driver parseTime=true 在扫描损坏文本时报错。
	rows, err := m.DB.QueryContext(ctx, `
		SELECT id, CAST(reviewed_at AS CHAR) AS reviewed_at_raw
		FROM submissions
		WHERE reviewed_at IS NOT NULL
		  AND CAST(reviewed_at AS CHAR) LIKE '%T%'
	`)
	if err != nil {
		return nil, err
	}
	type badRow struct {
		id  int64
		raw sql.NullString
	}
	var candidates []badRow
	for rows.Next() {
		var r badRow
		if scanErr := rows.Scan(&r.id, &r.raw); scanErr != nil {
			_ = rows.Close()
			return nil, scanErr
		}
		candidates = append(candidates, r)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()

	slog.Info(fmt.Sprintf("找到 %d 条需要修复的 reviewed_at 记录", len(candidates)))

	fixedCount := 0
	invalidCount := 0
	for _, item := range candidates {
		if item.raw.Valid {
			if formatted, ok := parseISOReviewedAt(item.raw.String); ok {
				if _, execErr := m.DB.ExecContext(ctx,
					"UPDATE submissions SET reviewed_at = ? WHERE id = ?",
					formatted, item.id); execErr == nil {
					fixedCount++
					continue
				}
			}
		}
		slog.Warn(fmt.Sprintf("无法修复 submission %d 的 reviewed_at: %q，将设为 NULL", item.id, item.raw.String))
		if _, execErr := m.DB.ExecContext(ctx,
			"UPDATE submissions SET reviewed_at = NULL WHERE id = ?", item.id); execErr != nil {
			return nil, execErr
		}
		invalidCount++
	}

	msg := fmt.Sprintf("修复完成: %d 条记录已修复, %d 条记录无法解析已设为NULL", fixedCount, invalidCount)
	slog.Info(msg)
	return &ReviewedAtFixResult{
		Success:      true,
		Message:      msg,
		FixedCount:   fixedCount,
		InvalidCount: invalidCount,
	}, nil
}

// isoReviewedAtLayouts 覆盖 Python 3.11+ datetime.fromisoformat 常见输入形态。
var isoReviewedAtLayouts = []string{
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02T15:04:05.999999999-0700",
	"2006-01-02T15:04:05-0700",
	"2006-01-02T15:04:05.999999999",
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05.999999999-07:00",
	"2006-01-02 15:04:05-07:00",
	"2006-01-02 15:04:05.999999999Z07:00",
	"2006-01-02 15:04:05.999999999",
	"2006-01-02 15:04:05",
}

// parseISOReviewedAt 解析任意 ISO 日期时间字符串并格式化为 MySQL DATETIME 字符串
// （对齐 fix_reviewed_at_datetime.py:86-87 dt.strftime("%Y-%m-%d %H:%M:%S")）。
func parseISOReviewedAt(raw string) (string, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", false
	}
	for _, layout := range isoReviewedAtLayouts {
		if dt, err := time.Parse(layout, trimmed); err == nil {
			return dt.Format(timeLayout), true
		}
	}
	return "", false
}
