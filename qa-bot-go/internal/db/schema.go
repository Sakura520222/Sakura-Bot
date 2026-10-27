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

	"github.com/go-sql-driver/mysql"
)

// ddlStatements 逐条移植 core/infrastructure/database/mysql.py:138-453
// _create_tables 的建表 DDL 与 db_version 版本号 upsert（原样拷贝，
// 仅 Python 的 %s 无、MySQL 方言不变；启动时幂等执行）。
var ddlStatements = []string{
	// 1. 总结记录主表（mysql.py:153-176）。
	`CREATE TABLE IF NOT EXISTS summaries (
                    id INT AUTO_INCREMENT PRIMARY KEY,
                    channel_id VARCHAR(255) NOT NULL,
                    channel_name VARCHAR(255),
                    summary_text TEXT NOT NULL,
                    message_count INT DEFAULT 0,
                    start_time DATETIME,
                    end_time DATETIME,
                    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
                    ai_model VARCHAR(100),
                    summary_type VARCHAR(20) DEFAULT 'weekly',
                    summary_message_ids JSON,
                    poll_message_id INT,
                    button_message_id INT,
                    keywords JSON,
                    topics JSON,
                    sentiment VARCHAR(50),
                    entities JSON,
                    INDEX idx_channel_created (channel_id, created_at DESC),
                    INDEX idx_created (created_at DESC),
                    INDEX idx_channel (channel_id)
                ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
	// 2. 数据库版本管理表（mysql.py:179-184）。
	`CREATE TABLE IF NOT EXISTS db_version (
                    version INT PRIMARY KEY,
                    upgraded_at DATETIME DEFAULT CURRENT_TIMESTAMP
                ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
	// 3. 配额管理表（mysql.py:187-198）。
	`CREATE TABLE IF NOT EXISTS usage_quota (
                    id INT AUTO_INCREMENT PRIMARY KEY,
                    user_id BIGINT NOT NULL,
                    query_date VARCHAR(10) NOT NULL,
                    usage_count INT DEFAULT 0,
                    last_reset DATETIME DEFAULT CURRENT_TIMESTAMP,
                    UNIQUE KEY unique_user_date (user_id, query_date),
                    INDEX idx_quota_user_date (user_id, query_date),
                    INDEX idx_quota_date (query_date)
                ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
	// 4. 频道画像表（mysql.py:201-213）。
	`CREATE TABLE IF NOT EXISTS channel_profiles (
                    channel_id VARCHAR(255) PRIMARY KEY,
                    channel_name VARCHAR(255),
                    style VARCHAR(50) DEFAULT 'neutral',
                    topics JSON,
                    keywords_freq JSON,
                    tone VARCHAR(50),
                    avg_message_length DECIMAL(10,2) DEFAULT 0,
                    total_summaries INT DEFAULT 0,
                    last_updated DATETIME DEFAULT CURRENT_TIMESTAMP
                ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
	// 5. 对话历史表（mysql.py:216-227）。
	`CREATE TABLE IF NOT EXISTS conversation_history (
                    id INT AUTO_INCREMENT PRIMARY KEY,
                    user_id BIGINT NOT NULL,
                    session_id VARCHAR(100) NOT NULL,
                    role VARCHAR(20) NOT NULL,
                    content TEXT NOT NULL,
                    timestamp DATETIME DEFAULT CURRENT_TIMESTAMP,
                    metadata JSON,
                    INDEX idx_user_session (user_id, session_id, timestamp ASC)
                ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
	// 6. 用户注册表（mysql.py:230-241）。
	`CREATE TABLE IF NOT EXISTS users (
                    user_id BIGINT PRIMARY KEY,
                    username VARCHAR(255),
                    first_name VARCHAR(255),
                    registered_at DATETIME DEFAULT CURRENT_TIMESTAMP,
                    last_active DATETIME DEFAULT CURRENT_TIMESTAMP,
                    is_admin BOOLEAN DEFAULT FALSE,
                    preferences JSON,
                    INDEX idx_users_last_active (last_active DESC)
                ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
	// 7. 订阅配置表（mysql.py:244-257）。
	`CREATE TABLE IF NOT EXISTS subscriptions (
                    id INT AUTO_INCREMENT PRIMARY KEY,
                    user_id BIGINT NOT NULL,
                    channel_id VARCHAR(255) NOT NULL,
                    channel_name VARCHAR(255),
                    sub_type VARCHAR(20) DEFAULT 'summary',
                    enabled BOOLEAN DEFAULT TRUE,
                    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
                    UNIQUE KEY unique_subscription (user_id, channel_id, sub_type),
                    INDEX idx_subscriptions_user (user_id),
                    INDEX idx_subscriptions_channel (channel_id)
                ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
	// 8. 请求队列表（mysql.py:260-274）。
	`CREATE TABLE IF NOT EXISTS request_queue (
                    id INT AUTO_INCREMENT PRIMARY KEY,
                    request_type VARCHAR(50) NOT NULL,
                    requested_by BIGINT NOT NULL,
                    target_channel VARCHAR(255),
                    params JSON,
                    status VARCHAR(20) DEFAULT 'pending',
                    result JSON,
                    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
                    processed_at DATETIME,
                    INDEX idx_request_queue_status (status, created_at),
                    INDEX idx_request_queue_user (requested_by)
                ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
	// 9. 通知队列表（mysql.py:277-288）。
	`CREATE TABLE IF NOT EXISTS notification_queue (
                    id INT AUTO_INCREMENT PRIMARY KEY,
                    user_id BIGINT NOT NULL,
                    notification_type VARCHAR(50) NOT NULL,
                    content JSON,
                    status VARCHAR(20) DEFAULT 'pending',
                    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
                    sent_at DATETIME,
                    INDEX idx_notification_queue_status (status, created_at)
                ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
	// 10. 转发消息记录表（mysql.py:291-305）。
	`CREATE TABLE IF NOT EXISTS forwarded_messages (
                    id INT AUTO_INCREMENT PRIMARY KEY,
                    message_id VARCHAR(100) NOT NULL,
                    source_channel VARCHAR(255) NOT NULL,
                    target_channel VARCHAR(255) NOT NULL,
                    content_hash VARCHAR(64),
                    timestamp BIGINT NOT NULL,
                    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
                    UNIQUE KEY unique_message_target (message_id, target_channel),
                    INDEX idx_forwarded_source (source_channel),
                    INDEX idx_forwarded_target (target_channel),
                    INDEX idx_forwarded_timestamp (timestamp)
                ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
	// 11. 转发统计表（mysql.py:308-318）。
	`CREATE TABLE IF NOT EXISTS forwarding_stats (
                    id INT AUTO_INCREMENT PRIMARY KEY,
                    channel_id VARCHAR(255) NOT NULL UNIQUE,
                    total_forwarded INT DEFAULT 0,
                    last_forwarded BIGINT,
                    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
                    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
                    INDEX idx_forwarding_stats_channel (channel_id)
                ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
	// 12. 投票重新生成表（mysql.py:321-338）。
	`CREATE TABLE IF NOT EXISTS poll_regenerations (
                    id BIGINT AUTO_INCREMENT PRIMARY KEY,
                    channel VARCHAR(512) NOT NULL,
                    summary_msg_id BIGINT NOT NULL,
                    poll_msg_id BIGINT,
                    button_msg_id BIGINT,
                    summary_text TEXT,
                    channel_name VARCHAR(255),
                    timestamp DATETIME DEFAULT CURRENT_TIMESTAMP,
                    send_to_channel BOOLEAN DEFAULT FALSE,
                    vote_count INT DEFAULT 0,
                    discussion_forward_msg_id BIGINT,
                    UNIQUE KEY unique_poll (channel, summary_msg_id),
                    INDEX idx_poll_lookup (channel, summary_msg_id),
                    INDEX idx_poll_timestamp (timestamp)
                ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
	// 13. 投票者表（mysql.py:341-355）。
	`CREATE TABLE IF NOT EXISTS poll_voters (
                    id BIGINT AUTO_INCREMENT PRIMARY KEY,
                    channel VARCHAR(512) NOT NULL,
                    summary_msg_id BIGINT NOT NULL,
                    user_id BIGINT NOT NULL,
                    voted_at DATETIME DEFAULT CURRENT_TIMESTAMP,
                    UNIQUE KEY unique_voter (channel, summary_msg_id, user_id),
                    INDEX idx_voter_lookup (channel, summary_msg_id),
                    INDEX idx_voter_user (user_id),
                    FOREIGN KEY (channel, summary_msg_id)
                        REFERENCES poll_regenerations(channel, summary_msg_id)
                            ON DELETE CASCADE
                ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
	// 14. 投稿表（mysql.py:358-379）。
	`CREATE TABLE IF NOT EXISTS submissions (
                    id INT AUTO_INCREMENT PRIMARY KEY,
                    submitter_id BIGINT NOT NULL,
                    submitter_name VARCHAR(255),
                    title VARCHAR(500) NOT NULL,
                    content TEXT,
                    media_files JSON,
                    target_channel VARCHAR(500),
                    is_anonymous BOOLEAN DEFAULT FALSE,
                    signature_forced BOOLEAN DEFAULT FALSE,
                    status VARCHAR(20) DEFAULT 'pending',
                    ai_optimized_content TEXT,
                    ai_optimized_title VARCHAR(500),
                    review_message_id INT,
                    reviewed_by BIGINT,
                    reviewed_at DATETIME,
                    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
                    INDEX idx_submissions_status (status, created_at),
                    INDEX idx_submissions_submitter (submitter_id)
                ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
	// 15. WebUI 系统运维审计表（mysql.py:430-446）。
	`CREATE TABLE IF NOT EXISTS system_audit_logs (
                    id BIGINT AUTO_INCREMENT PRIMARY KEY,
                    action VARCHAR(100) NOT NULL,
                    actor VARCHAR(100) NOT NULL,
                    target VARCHAR(500),
                    params_summary TEXT,
                    success BOOLEAN DEFAULT TRUE,
                    message TEXT,
                    duration_ms INT DEFAULT 0,
                    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
                    INDEX idx_system_audit_created (created_at DESC),
                    INDEX idx_system_audit_action (action),
                    INDEX idx_system_audit_actor (actor)
                ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
	// 版本号 upsert（mysql.py:448-453）。
	`INSERT INTO db_version (version, upgraded_at)
                    VALUES (6, NOW())
                    ON DUPLICATE KEY UPDATE version = 6, upgraded_at = NOW()`,
}

// submissionAlter 是 submissions 表的幂等补列项（mysql.py:381-428）。
type submissionAlter struct {
	query   string // ALTER 语句
	column  string // 列名（日志用）
	success string // 成功日志（与 Python 逐字一致）
	dupSkip string // Duplicate column 时 debug 日志
}

// submissionAlters 四条幂等补列（mysql.py:382-428；错误 1060 视为已存在）。
var submissionAlters = []submissionAlter{
	{
		query:   "ALTER TABLE submissions ADD COLUMN target_channel VARCHAR(500) AFTER media_files",
		column:  "target_channel",
		success: "投稿表新增 target_channel 列成功",
		dupSkip: "target_channel 列已存在，跳过",
	},
	{
		query:   "ALTER TABLE submissions ADD COLUMN ai_optimized_title VARCHAR(500) AFTER ai_optimized_content",
		column:  "ai_optimized_title",
		success: "投稿表新增 ai_optimized_title 列成功",
		dupSkip: "ai_optimized_title 列已存在，跳过",
	},
	{
		query:   "ALTER TABLE submissions ADD COLUMN is_anonymous BOOLEAN DEFAULT FALSE AFTER target_channel",
		column:  "is_anonymous",
		success: "投稿表新增 is_anonymous 列成功",
		dupSkip: "is_anonymous 列已存在，跳过",
	},
	{
		query:   "ALTER TABLE submissions ADD COLUMN signature_forced BOOLEAN DEFAULT FALSE AFTER is_anonymous",
		column:  "signature_forced",
		success: "投稿表新增 signature_forced 列成功",
		dupSkip: "signature_forced 列已存在，跳过",
	},
}

// mysqlDuplicateColumn 即 MySQL ER_DUP_FIELDNAME（"Duplicate column name"）。
const mysqlDuplicateColumn = 1060

// initSchema 在同一连接上执行全部 DDL（对齐 _create_tables 的执行方式）：
// 先保存并临时清空 sql_mode 以抑制“表已存在”警告，结束后无论成败都恢复
// （mysql.py:140-146 与 finally 恢复 :455-460）。
func initSchema(ctx context.Context, db *sql.DB) error {
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("初始化MySQL数据库失败: %w", err)
	}
	defer conn.Close() //nolint:errcheck // 归还池连接，错误无意义

	exec := func(query string, args ...any) error {
		_, err := conn.ExecContext(ctx, query, args...)
		return err
	}

	// 保存当前 sql_mode，稍后恢复（mysql.py:141-142）。
	var original sql.NullString
	if err := conn.QueryRowContext(ctx, "SELECT @@SESSION.sql_mode").Scan(&original); err != nil {
		return fmt.Errorf("初始化MySQL数据库失败: %w", err)
	}
	// finally：恢复原始 sql_mode；空串与 Python 的 if original_sql_mode 一致跳过。
	if original.Valid && original.String != "" {
		defer func() {
			// 参数化恢复，避免 SQL 注入（mysql.py:456-459 注释）。
			_, _ = conn.ExecContext(ctx, "SET SESSION sql_mode = ?", original.String)
		}()
	}

	// 临时禁用 sql_mode，避免表已存在时的警告/严格模式干扰（mysql.py:146）。
	if err := exec("SET SESSION sql_mode = ''"); err != nil {
		return fmt.Errorf("初始化MySQL数据库失败: %w", err)
	}

	for _, ddl := range ddlStatements {
		if err := exec(ddl); err != nil {
			return fmt.Errorf("初始化MySQL数据库失败: %w", err)
		}
	}

	// 幂等补列：Duplicate column name 视为已存在（debug），其余告警后继续
	//（对齐 mysql.py:382-428 的 try/except 分支）。
	for _, alt := range submissionAlters {
		_, err := conn.ExecContext(ctx, alt.query)
		switch {
		case err == nil:
			slog.Info(alt.success)
		default:
			var mysqlErr *mysql.MySQLError
			if errors.As(err, &mysqlErr) && mysqlErr.Number == mysqlDuplicateColumn {
				slog.Debug(alt.dupSkip)
			} else {
				slog.Warn(fmt.Sprintf("添加 %s 列时出错: %s", alt.column, err))
			}
		}
	}

	slog.Info("MySQL数据库表结构初始化成功")
	return nil
}
