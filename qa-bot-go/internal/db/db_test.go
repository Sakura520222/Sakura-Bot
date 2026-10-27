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
	"os"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
)

// testManager 准备集成测试环境：MYSQL_TEST_DSN 未设置则跳过
// （设计 §15.2：本地可用一次性 MySQL 8，服务器指向测试库）。
// DSN 规范化为 parseTime=true & loc=UTC（业务表时间列扫描依赖），
// 并执行完整业务 DDL。
func testManager(t *testing.T) *Manager {
	t.Helper()
	dsn := os.Getenv("MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("MYSQL_TEST_DSN 未设置，跳过 MySQL 集成测试")
	}

	ctx := context.Background()
	db, err := sql.Open("mysql", normalizeTestDSN(dsn))
	if err != nil {
		t.Fatalf("打开测试库失败: %v", err)
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		t.Skipf("MySQL 测试库不可达: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	m := newFromDB(db, nil)
	if err := m.InitDatabase(ctx); err != nil {
		t.Fatalf("初始化业务表失败: %v", err)
	}
	return m
}

// normalizeTestDSN 强制测试 DSN 携带 parseTime=true & loc=UTC
// （同时保证驱动已注册可用）。
func normalizeTestDSN(dsn string) string {
	if cfg, err := mysql.ParseDSN(dsn); err == nil {
		cfg.ParseTime = true
		cfg.Loc = time.UTC
		return cfg.FormatDSN()
	}
	return dsn
}

// cleanupQuota 注册测试结束后清理指定用户的配额行。
func cleanupQuota(t *testing.T, m *Manager, userID int64) {
	t.Cleanup(func() {
		_, _ = m.DB.Exec("DELETE FROM usage_quota WHERE user_id = ?", userID)
	})
}

// TestQuotaReserveLimitRefund 覆盖：预留 → 达限拒绝 → 退款幂等
// （usage_count>0 条件，两次退不出负）→ 总用量聚合 → 管理员直通不落行。
func TestQuotaReserveLimitRefund(t *testing.T) {
	m := testManager(t)
	ctx := context.Background()
	uid := uniqueUserID()

	cleanupQuota(t, m, uid)

	// 预留：1/3 → 2/3 → 3/3，remaining 递减。
	wantRemaining := []int64{2, 1, 0}
	for i, want := range wantRemaining {
		r := m.CheckAndIncrementQuota(ctx, uid, 3, false)
		if !r.Allowed {
			t.Fatalf("第 %d 次预留被拒: %+v", i+1, r)
		}
		if r.Remaining != want || r.Used != int64(i+1) || r.IsAdmin {
			t.Fatalf("第 %d 次预留结果不符: %+v", i+1, r)
		}
	}

	// 达限拒绝：used=3，daily_limit 回显。
	r := m.CheckAndIncrementQuota(ctx, uid, 3, false)
	if r.Allowed || r.Used != 3 || r.DailyLimit != 3 || r.Remaining != 0 {
		t.Fatalf("达限后应拒绝: %+v", r)
	}

	// 退款幂等：首次退回 true，第二次因 usage_count=0 返回 false，不出负。
	if ok, err := m.RefundQuota(ctx, uid, ""); err != nil || !ok {
		t.Fatalf("首次退款应成功: ok=%v err=%v", ok, err)
	}
	if ok, err := m.RefundQuota(ctx, uid, ""); err != nil || ok {
		t.Fatalf("第二次退款应无副作用: ok=%v err=%v", ok, err)
	}
	var usage int
	if err := m.DB.QueryRow("SELECT usage_count FROM usage_quota WHERE user_id=? AND query_date=?",
		uid, QuotaDate(time.Now())).Scan(&usage); err != nil {
		t.Fatalf("查询余额失败: %v", err)
	}
	if usage != 0 {
		t.Fatalf("退款后 usage_count 应为 0，实际 %d", usage)
	}

	// 无记录用户退款：false 且无副作用。
	if ok, err := m.RefundQuota(ctx, uid+1, ""); err != nil || ok {
		t.Fatalf("无记录退款应返回 false: ok=%v err=%v", ok, err)
	}

	// 总用量：至少包含本次操作。
	total, err := m.GetTotalDailyUsage(ctx, "")
	if err != nil || total < 1 {
		t.Fatalf("总用量应 ≥1: total=%d err=%v", total, err)
	}

	// 管理员直通：不落行。
	adminID := uid + 2
	ra := m.CheckAndIncrementQuota(ctx, adminID, 3, true)
	if !ra.Allowed || ra.Remaining != -1 || ra.Used != 0 || !ra.IsAdmin {
		t.Fatalf("管理员应直通: %+v", ra)
	}
	var count int
	if err := m.DB.QueryRow("SELECT COUNT(*) FROM usage_quota WHERE user_id=?", adminID).Scan(&count); err != nil {
		t.Fatalf("查询管理员配额行失败: %v", err)
	}
	if count != 0 {
		t.Fatalf("管理员不应产生配额行，实际 %d 行", count)
	}
}

// TestDBVersion 覆盖 db_version 读取：init 后版本应为 6。
func TestDBVersion(t *testing.T) {
	m := testManager(t)
	v, err := m.DBVersion(context.Background())
	if err != nil {
		t.Fatalf("读取 db_version 失败: %v", err)
	}
	if v != 6 {
		t.Fatalf("db_version 应为 6，实际 %d", v)
	}
}

// uniqueUserID 生成测试用唯一用户 ID（纳秒时间戳，避免污染历史数据）。
func uniqueUserID() int64 {
	return time.Now().UnixNano()
}
