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
)

// RegisterUser 注册新用户（upsert 语义，mysql.py:1454-1497）。
// 用户存在 → 更新 username/first_name/last_active/is_admin；
// 不存在 → 插入新行。QA 侧恒以 is_admin=false 调用（qa_user_system.py:55）。
// username/firstName 传 nil 表示 Telegram 侧未提供（SQL NULL）。
func (m *Manager) RegisterUser(ctx context.Context, userID int64, username, firstName *string, isAdmin bool) error {
	now := MysqlDatetime(NowUTC())
	adminFlag := 0
	if isAdmin {
		adminFlag = 1
	}

	// 先检查用户是否存在（mysql.py:1464-1467）。
	var registeredAt sql.NullTime
	err := m.DB.QueryRowContext(ctx,
		"SELECT registered_at FROM users WHERE user_id = ?", userID).Scan(&registeredAt)
	switch {
	case err == nil:
		// 用户已存在，更新信息（mysql.py:1471-1478）。
		if _, err := m.DB.ExecContext(ctx, `
                            UPDATE users
                            SET username = ?, first_name = ?, last_active = ?, is_admin = ?
                            WHERE user_id = ?
                        `, username, firstName, now, adminFlag, userID); err != nil {
			slog.Error(fmt.Sprintf("用户注册失败: %T: %s", err, err))
			return err
		}
	case err == sql.ErrNoRows:
		// 新用户，插入记录（mysql.py:1481-1488）。
		if _, err := m.DB.ExecContext(ctx, `
                            INSERT INTO users
                            (user_id, username, first_name, registered_at, last_active, is_admin)
                            VALUES (?, ?, ?, ?, ?, ?)
                        `, userID, username, firstName, now, now, adminFlag); err != nil {
			slog.Error(fmt.Sprintf("用户注册失败: %T: %s", err, err))
			return err
		}
	default:
		slog.Error(fmt.Sprintf("用户注册失败: %T: %s", err, err))
		return err
	}

	slog.Info(fmt.Sprintf("用户注册成功: user_id=%d, username=%s", userID, pyStr(username)))
	return nil
}

// UpdateUserActivity 更新用户最后活跃时间（mysql.py:1521-1541）。
func (m *Manager) UpdateUserActivity(ctx context.Context, userID int64) error {
	now := MysqlDatetime(NowUTC())
	if _, err := m.DB.ExecContext(ctx, `
                        UPDATE users SET last_active = ?
                        WHERE user_id = ?
                    `, now, userID); err != nil {
		slog.Error(fmt.Sprintf("更新用户活跃时间失败: %T: %s", err, err))
		return err
	}
	return nil
}

// IsUserRegistered 检查用户是否已注册（mysql.py:1590-1601）。
func (m *Manager) IsUserRegistered(ctx context.Context, userID int64) (bool, error) {
	var one sql.NullInt64
	err := m.DB.QueryRowContext(ctx, "SELECT 1 FROM users WHERE user_id = ?", userID).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		slog.Error(fmt.Sprintf("检查用户注册状态失败: %T: %s", err, err))
		return false, err
	}
	return true, nil
}

// pyStr 复刻 Python f-string 对 None 的渲染（nil 指针 → "None"），
// 仅用于日志文案逐字对齐。
func pyStr(s *string) string {
	if s == nil {
		return "None"
	}
	return *s
}
