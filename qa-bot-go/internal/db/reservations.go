package db

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/contracts"
	"github.com/go-sql-driver/mysql"
	"math/big"
	"time"
)

// InitReservations is additive; existing UTC quota rows remain authoritative.
func (m *Manager) InitReservations(ctx context.Context) error {
	for _, q := range []string{
		`CREATE TABLE IF NOT EXISTS qa_billing_lock (id INT PRIMARY KEY) ENGINE=InnoDB`,
		`INSERT IGNORE INTO qa_billing_lock(id) VALUES(1)`,
		`CREATE TABLE IF NOT EXISTS qa_message_reservations (
 id VARCHAR(100) PRIMARY KEY, user_id BIGINT NOT NULL, query_date DATE NOT NULL,
 source VARCHAR(16) NOT NULL, state VARCHAR(16) NOT NULL, units INT NOT NULL DEFAULT 0, turn_id VARCHAR(100) NOT NULL DEFAULT '',
 created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
 INDEX(state)) ENGINE=InnoDB`,
		`CREATE TABLE IF NOT EXISTS qa_points(user_id BIGINT PRIMARY KEY, units BIGINT NOT NULL DEFAULT 0) ENGINE=InnoDB`,
		`CREATE TABLE IF NOT EXISTS qa_checkins(user_id BIGINT NOT NULL,billing_day DATE NOT NULL,units INT NOT NULL,PRIMARY KEY(user_id,billing_day)) ENGINE=InnoDB`,
		`CREATE TABLE IF NOT EXISTS qa_points_ledger(id VARCHAR(120) PRIMARY KEY,user_id BIGINT NOT NULL,units BIGINT NOT NULL,reason VARCHAR(32) NOT NULL,created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP) ENGINE=InnoDB`,
		`CREATE TABLE IF NOT EXISTS qa_daily_total(billing_day DATE PRIMARY KEY, messages BIGINT NOT NULL DEFAULT 0) ENGINE=InnoDB`,
	} {
		if _, err := m.DB.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	for _, q := range []string{"ALTER TABLE qa_message_reservations ADD COLUMN units INT NOT NULL DEFAULT 0", "ALTER TABLE qa_message_reservations ADD COLUMN turn_id VARCHAR(100) NOT NULL DEFAULT ''"} {
		if _, e := m.DB.ExecContext(ctx, q); e != nil {
			var me *mysql.MySQLError
			if !errors.As(e, &me) || me.Number != 1060 {
				return e
			}
		}
	}
	return nil
}

func billingTx(ctx context.Context, db *sql.DB) (*sql.Tx, error) {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return nil, err
	}
	var id int
	if err = tx.QueryRowContext(ctx, `SELECT id FROM qa_billing_lock WHERE id=1 FOR UPDATE`).Scan(&id); err != nil {
		tx.Rollback()
		return nil, err
	}
	return tx, nil
}

// ReserveMessage atomically checks the global cap and individual quota and records
// a durable Telegram message identity. Duplicate delivery never runs AI twice.
func (m *Manager) ReserveMessage(ctx context.Context, id string, user int64, limits contracts.QuotaLimits, admin bool) (contracts.QuotaCheckResult, error) {
	return m.ReserveMessageOnDay(ctx, id, user, limits, admin, QuotaDate(time.Now()))
}
func (m *Manager) ReserveMessageOnDay(ctx context.Context, id string, user int64, limits contracts.QuotaLimits, admin bool, date string, turnIDs ...string) (contracts.QuotaCheckResult, error) {
	// Anonymous actors share the current group's negative ID and hard allowance.
	// They cannot receive the real-account administrator exemption or spend points.
	if user < 0 {
		admin = false
	}
	turnID := ""
	if len(turnIDs) > 0 {
		turnID = turnIDs[0]
	}
	tx, err := billingTx(ctx, m.DB)
	if err != nil {
		return contracts.QuotaCheckResult{}, err
	}
	defer tx.Rollback()
	var state string
	err = tx.QueryRowContext(ctx, `SELECT state FROM qa_message_reservations WHERE id=?`, id).Scan(&state)
	if err == nil {
		return contracts.QuotaCheckResult{Message: "这条消息已经处理或接纳，请勿重复提交。"}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return contracts.QuotaCheckResult{}, err
	}

	source := "admin"
	used := int64(0)
	remaining := int64(-1)
	if !admin {
		// Seed the new all-message counter once from legacy UTC free usage.
		if _, err = tx.ExecContext(ctx, `INSERT IGNORE INTO qa_daily_total(billing_day,messages) SELECT ?,COALESCE(SUM(usage_count),0) FROM usage_quota WHERE query_date=?`, date, date); err != nil {
			return contracts.QuotaCheckResult{}, err
		}
		var total int64
		if err = tx.QueryRowContext(ctx, `SELECT messages FROM qa_daily_total WHERE billing_day=?`, date).Scan(&total); err != nil {
			return contracts.QuotaCheckResult{}, err
		}
		if total >= limits.TotalDaily {
			return contracts.QuotaCheckResult{Message: "今日全站额度已用尽，积分不能绕过全站上限。"}, nil
		}

		err = tx.QueryRowContext(ctx, `SELECT usage_count FROM usage_quota WHERE user_id=? AND query_date=?`, user, date).Scan(&used)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return contracts.QuotaCheckResult{}, err
		}
		if used >= limits.UserDaily {
			if user < 0 {
				return contracts.QuotaCheckResult{Used: used, Message: "本群匿名管理员的当日共享额度已用尽，请明日重试或使用真实账号提问。"}, nil
			}
			var units int64
			err = tx.QueryRowContext(ctx, `SELECT units FROM qa_points WHERE user_id=?`, user).Scan(&units)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return contracts.QuotaCheckResult{}, err
			}
			if units < 10 {
				return contracts.QuotaCheckResult{Message: "免费次数已用完且积分不足，可在已启用助手的评论区发送「签到」获取积分。"}, nil
			}
			if _, err = tx.ExecContext(ctx, `UPDATE qa_points SET units=units-10 WHERE user_id=? AND units>=10`, user); err != nil {
				return contracts.QuotaCheckResult{}, err
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO qa_points_ledger(id,user_id,units,reason) VALUES(?,?,-10,'message')`, id, user); err != nil {
				return contracts.QuotaCheckResult{}, err
			}
			source = "points"
			remaining = 0
		} else {
			if _, err = tx.ExecContext(ctx, `INSERT INTO usage_quota(user_id,query_date,usage_count) VALUES(?,?,1) ON DUPLICATE KEY UPDATE usage_count=usage_count+1`, user, date); err != nil {
				return contracts.QuotaCheckResult{}, err
			}
			source = "free"
			used++
			remaining = limits.UserDaily - used
		}
		if _, err = tx.ExecContext(ctx, `UPDATE qa_daily_total SET messages=messages+1 WHERE billing_day=?`, date); err != nil {
			return contracts.QuotaCheckResult{}, err
		}

	}
	_, err = tx.ExecContext(ctx, `INSERT INTO qa_message_reservations(id,user_id,query_date,source,state,units,turn_id) VALUES(?,?,?,?,'reserved',?,?)`, id, user, date, source, map[bool]int{true: 10, false: 0}[source == "points"], turnID)
	if err != nil {
		return contracts.QuotaCheckResult{}, err
	}
	if err = tx.Commit(); err != nil {
		return contracts.QuotaCheckResult{}, err
	}
	return contracts.QuotaCheckResult{Allowed: true, IsAdmin: admin, Used: used, Remaining: remaining}, nil
}

// FinishReservation can be retried after connection loss. Refunds use the original
// billing date and source, independent of midnight or later admin-role changes.
func (m *Manager) FinishReservation(ctx context.Context, id string, success bool) error {
	return m.FinishReservations(ctx, []string{id}, success)
}

func (m *Manager) FinishReservations(ctx context.Context, ids []string, success bool) error {
	if len(ids) == 0 {
		return nil
	}
	tx, err := billingTx(ctx, m.DB)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, id := range ids {
		if err := finishReservation(ctx, tx, id, success); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func finishReservation(ctx context.Context, tx *sql.Tx, id string, success bool) error {
	var user int64
	var date, state, source string
	err := tx.QueryRowContext(ctx, `SELECT user_id,CAST(query_date AS CHAR),source,state FROM qa_message_reservations WHERE id=?`, id).Scan(&user, &date, &source, &state)
	if err != nil {
		return err
	}
	if state != "reserved" {
		return nil
	}
	next := "completed"
	if !success {
		next = "refunded"
		if source != "admin" {
			if _, err = tx.ExecContext(ctx, `UPDATE qa_daily_total SET messages=GREATEST(0,messages-1) WHERE billing_day=?`, date); err != nil {
				return err
			}
		}
		if source == "points" {
			if _, err = tx.ExecContext(ctx, `UPDATE qa_points SET units=units+10 WHERE user_id=?`, user); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO qa_points_ledger(id,user_id,units,reason) VALUES(?,?,10,'refund')`, "refund:"+id, user); err != nil {
				return err
			}
		}
		if source == "free" {
			if _, err = tx.ExecContext(ctx, `UPDATE usage_quota SET usage_count=GREATEST(0,usage_count-1) WHERE user_id=? AND query_date=?`, user, date); err != nil {
				return err
			}
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE qa_message_reservations SET state=? WHERE id=? AND state='reserved'`, next, id); err != nil {
		return err
	}
	return nil
}

// RecoverReservations is used before polling in the single assistant process.
func (m *Manager) RecoverReservations(ctx context.Context) error {
	rows, err := m.DB.QueryContext(ctx, `SELECT id FROM qa_message_reservations WHERE state='reserved'`)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err = m.FinishReservation(ctx, id, false); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) Checkin(ctx context.Context, user int64, day string) (int64, bool, error) {
	tx, e := billingTx(ctx, m.DB)
	if e != nil {
		return 0, false, e
	}
	defer tx.Rollback()
	var previous int64
	e = tx.QueryRowContext(ctx, `SELECT units FROM qa_checkins WHERE user_id=? AND billing_day=?`, user, day).Scan(&previous)
	if e == nil {
		return previous, false, nil
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return 0, false, e
	}
	n, e := rand.Int(rand.Reader, big.NewInt(50))
	if e != nil {
		return 0, false, e
	}
	units := n.Int64() + 1
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO qa_checkins(user_id,billing_day,units) VALUES(?,?,?)`, []any{user, day, units}},
		{`INSERT INTO qa_points(user_id,units) VALUES(?,?) ON DUPLICATE KEY UPDATE units=units+VALUES(units)`, []any{user, units}},
		{`INSERT INTO qa_points_ledger(id,user_id,units,reason) VALUES(?,?,?,'checkin')`, []any{fmt.Sprintf("checkin:%d:%s", user, day), user, units}},
	} {
		if _, e = tx.ExecContext(ctx, q.sql, q.args...); e != nil {
			return 0, false, e
		}
	}
	if e = tx.Commit(); e != nil {
		return 0, false, e
	}
	return units, true, nil
}
func (m *Manager) Points(ctx context.Context, user int64) (int64, error) {
	var n int64
	e := m.DB.QueryRowContext(ctx, `SELECT units FROM qa_points WHERE user_id=?`, user).Scan(&n)
	if errors.Is(e, sql.ErrNoRows) {
		e = nil
	}
	return n, e
}
func (m *Manager) BillingUsage(ctx context.Context, user int64, day string) (int64, int64, error) {
	var used, total int64
	e := m.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM qa_message_reservations WHERE user_id=? AND query_date=? AND state<>'refunded'`, user, day).Scan(&used)
	if e != nil {
		return 0, 0, e
	}
	e = m.DB.QueryRowContext(ctx, `SELECT messages FROM qa_daily_total WHERE billing_day=?`, day).Scan(&total)
	if errors.Is(e, sql.ErrNoRows) {
		e = nil
	}
	return used, total, e
}

type BillingSnapshot struct {
	PointUnits int64 `json:"point_units"`
	FreeUsed   int64 `json:"free_used"`
	SiteUsed   int64 `json:"site_used"`
	CheckedIn  bool  `json:"checked_in"`
}

// BillingSnapshot reads points and both quotas at the same instant as check-in state.
func (m *Manager) BillingSnapshot(ctx context.Context, user int64, day string) (BillingSnapshot, error) {
	var status BillingSnapshot
	err := m.DB.QueryRowContext(ctx, `SELECT
		COALESCE((SELECT units FROM qa_points WHERE user_id=?),0),
		COALESCE((SELECT usage_count FROM usage_quota WHERE user_id=? AND query_date=?),0),
		COALESCE((SELECT messages FROM qa_daily_total WHERE billing_day=?),
		    (SELECT COALESCE(SUM(usage_count),0) FROM usage_quota WHERE query_date=?)),
		EXISTS(SELECT 1 FROM qa_checkins WHERE user_id=? AND billing_day=?)`,
		user, user, day, day, day, user, day).Scan(&status.PointUnits, &status.FreeUsed, &status.SiteUsed, &status.CheckedIn)
	return status, err
}

func (m *Manager) ReservationState(ctx context.Context, id string, user int64) (string, error) {
	var state string
	err := m.DB.QueryRowContext(ctx, `SELECT state FROM qa_message_reservations WHERE id=? AND user_id=?`, id, user).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return state, err
}

// HoldAssistantLease prevents a second runtime from recovering a live process's reservations.
func (m *Manager) HoldAssistantLease(ctx context.Context, stop context.CancelFunc) (func(), error) {
	conn, e := m.DB.Conn(ctx)
	if e != nil {
		return nil, e
	}
	var name string
	if e = conn.QueryRowContext(ctx, `SELECT CONCAT('qa_runtime:',DATABASE())`).Scan(&name); e != nil {
		conn.Close()
		return nil, e
	}
	var locked sql.NullInt64
	if e = conn.QueryRowContext(ctx, `SELECT GET_LOCK(?,0)`, name).Scan(&locked); e != nil || !locked.Valid || locked.Int64 != 1 {
		conn.Close()
		return nil, fmt.Errorf("another assistant runtime holds the database lease")
	}
	monitor, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-monitor.Done():
				return
			case <-ticker.C:
				check, finish := context.WithTimeout(monitor, 3*time.Second)
				var valid int
				e := conn.QueryRowContext(check, `SELECT IS_USED_LOCK(?)=CONNECTION_ID()`, name).Scan(&valid)
				finish()
				if e != nil || valid != 1 {
					stop()
					return
				}
			}
		}
	}()
	return func() {
		cancel()
		<-done
		cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, _ = conn.ExecContext(cleanup, `DO RELEASE_LOCK(?)`, name)
		conn.Close()
	}, nil
}
