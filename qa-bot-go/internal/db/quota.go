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

// QuotaResult 对齐 mysql.py check_and_increment_quota / quota_manager 的
// 结果字典字段。Error 非空对应 Python {"allowed": false, "error": str(e)}
// 分支（DailyLimit/Used 等字段此时为零值，与缺键一致）。
type QuotaResult struct {
	Allowed    bool   // 是否允许
	Remaining  int64  // 今日剩余；管理员为 -1
	Used       int64  // 已用次数
	DailyLimit int64  // 本次检查的用户限额
	IsAdmin    bool   // 管理员直通分支为 true
	Error      string // 数据库异常文案（Python 的 "error" 键）
}

// GetQuotaUsage 获取用户当日配额使用（mysql.py:904-942）。
// date 传空串取今天（UTC）。lastReset 为零值表示无记录（Python None）。
func (m *Manager) GetQuotaUsage(ctx context.Context, userID int64, date string) (usage int64, lastReset sql.NullTime, err error) {
	if date == "" {
		date = QuotaDate(time.Now())
	}
	err = m.DB.QueryRowContext(ctx, `
                        SELECT usage_count, last_reset
                        FROM usage_quota
                        WHERE user_id = ? AND query_date = ?
                    `, userID, date).Scan(&usage, &lastReset)
	if err == sql.ErrNoRows {
		return 0, sql.NullTime{}, nil
	}
	if err != nil {
		slog.Error(fmt.Sprintf("获取配额使用失败: %T: %s", err, err))
		return 0, sql.NullTime{}, err
	}
	return usage, lastReset, nil
}

// CheckAndIncrementQuota 检查并增加配额使用（mysql.py:944-1008）。
//
// 注意：保持 Python 的非原子 SELECT→UPDATE/INSERT 顺序（README 硬规则 7、
// 评审 R6：单实例串行分发下与原子版等价，首版不做原子化）。
// 管理员直接放行（allowed/remaining=-1/used=0，不触库）。
func (m *Manager) CheckAndIncrementQuota(ctx context.Context, userID, dailyLimit int64, isAdmin bool) QuotaResult {
	if isAdmin {
		return QuotaResult{Allowed: true, Remaining: -1, Used: 0, IsAdmin: true}
	}

	date := QuotaDate(time.Now())

	var currentUsage int64
	var hasRow bool
	err := m.DB.QueryRowContext(ctx, `
                        SELECT usage_count FROM usage_quota
                        WHERE user_id = ? AND query_date = ?
                    `, userID, date).Scan(&currentUsage)
	switch {
	case err == nil:
		hasRow = true
	case err == sql.ErrNoRows:
		currentUsage = 0
	default:
		slog.Error(fmt.Sprintf("配额检查失败: %T: %s", err, err))
		return QuotaResult{Error: err.Error()}
	}

	// 检查是否超限（mysql.py:967-974）。
	if currentUsage >= dailyLimit {
		return QuotaResult{
			Allowed:    false,
			Remaining:  0,
			Used:       currentUsage,
			DailyLimit: dailyLimit,
		}
	}

	// 增加使用次数（有行 UPDATE，无行 INSERT；mysql.py:976-994）。
	newUsage := currentUsage + 1
	if hasRow {
		if _, err := m.DB.ExecContext(ctx, `
                            UPDATE usage_quota
                            SET usage_count = ?
                            WHERE user_id = ? AND query_date = ?
                        `, newUsage, userID, date); err != nil {
			slog.Error(fmt.Sprintf("配额检查失败: %T: %s", err, err))
			return QuotaResult{Error: err.Error()}
		}
	} else {
		if _, err := m.DB.ExecContext(ctx, `
                            INSERT INTO usage_quota (user_id, query_date, usage_count)
                            VALUES (?, ?, ?)
                        `, userID, date, newUsage); err != nil {
			slog.Error(fmt.Sprintf("配额检查失败: %T: %s", err, err))
			return QuotaResult{Error: err.Error()}
		}
	}

	slog.Info(fmt.Sprintf("用户 %d 配额使用: %d/%d", userID, newUsage, dailyLimit))
	return QuotaResult{
		Allowed:    true,
		Remaining:  dailyLimit - newUsage,
		Used:       newUsage,
		DailyLimit: dailyLimit,
	}
}

// RefundQuota 回退一次配额使用（mysql.py:1010-1046）。
// date 传空串取今天（UTC）。单条 UPDATE 原子递减，usage_count > 0 条件
// 保证幂等且不出现负数；当日无占用记录时无副作用（rowcount=0 → false）。
func (m *Manager) RefundQuota(ctx context.Context, userID int64, date string) (bool, error) {
	if date == "" {
		date = QuotaDate(time.Now())
	}

	res, err := m.DB.ExecContext(ctx, `
                        UPDATE usage_quota
                        SET usage_count = usage_count - 1
                        WHERE user_id = ? AND query_date = ? AND usage_count > 0
                    `, userID, date)
	if err != nil {
		slog.Error(fmt.Sprintf("回退配额失败: %T: %s", err, err))
		return false, err
	}
	refunded, err := res.RowsAffected()
	if err != nil {
		slog.Error(fmt.Sprintf("回退配额失败: %T: %s", err, err))
		return false, err
	}
	if refunded > 0 {
		slog.Info(fmt.Sprintf("用户 %d 配额已回退一次（%s）", userID, date))
	}
	return refunded > 0, nil
}

// GetTotalDailyUsage 获取指定日期的全局总使用次数（mysql.py:1048-1069）。
// date 传空串取今天（UTC）；SUM 为 NULL（无行）时返回 0。
func (m *Manager) GetTotalDailyUsage(ctx context.Context, date string) (int64, error) {
	if date == "" {
		date = QuotaDate(time.Now())
	}

	var total sql.NullInt64
	err := m.DB.QueryRowContext(ctx, `
                        SELECT SUM(usage_count) FROM usage_quota
                        WHERE query_date = ?
                    `, date).Scan(&total)
	if err != nil {
		slog.Error(fmt.Sprintf("获取总使用次数失败: %T: %s", err, err))
		return 0, err
	}
	if !total.Valid {
		return 0, nil
	}
	return total.Int64, nil
}

// ResetQuotaIfNewDay 检测到新的一天时记录重置日志（mysql.py:1071-1092）。
// 注意：Python 版此处仅记日志（实际重置依赖 query_date 天然隔离），
// Go 版逐字保持该行为，不额外 DELETE/UPDATE。
func (m *Manager) ResetQuotaIfNewDay(ctx context.Context, userID int64) {
	today := QuotaDate(time.Now())
	var latest sql.NullString
	err := m.DB.QueryRowContext(ctx, `
                        SELECT query_date FROM usage_quota
                        WHERE user_id = ?
                        ORDER BY query_date DESC
                        LIMIT 1
                    `, userID).Scan(&latest)
	if err != nil {
		if err != sql.ErrNoRows {
			slog.Error(fmt.Sprintf("重置配额失败: %T: %s", err, err))
		}
		return
	}
	if latest.Valid && latest.String != today {
		slog.Info(fmt.Sprintf("检测到新的一天，重置用户 %d 配额", userID))
	}
}
