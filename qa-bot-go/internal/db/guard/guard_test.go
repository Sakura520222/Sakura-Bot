// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生工作必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package guard

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"

	db "github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/db"
)

// testEnv 准备 guard 集成测试：MYSQL_TEST_DSN 未设置则跳过（设计 §15.2）。
// DSN 规范化为 parseTime=true & loc=UTC（guard 表 DATETIME(6) 列按 time.Time
// 扫描），并幂等执行 guard schema。
func testEnv(t *testing.T) (*sql.DB, *Repository) {
	t.Helper()
	dsn := os.Getenv("MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("MYSQL_TEST_DSN 未设置，跳过 MySQL 集成测试")
	}
	if cfg, err := mysql.ParseDSN(dsn); err == nil {
		cfg.ParseTime = true
		cfg.Loc = time.UTC
		dsn = cfg.FormatDSN()
	}

	ctx := context.Background()
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("打开测试库失败: %v", err)
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		t.Skipf("MySQL 测试库不可达: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	repo := New(db)
	if err := repo.Initialize(ctx); err != nil {
		t.Fatalf("初始化 guard schema 失败: %v", err)
	}
	return db, repo
}

// seedCase 直接插入一条 moderation_cases（主进程侧建案，测试只造前置数据），
// 返回 (case_id, chat_id, user_id)。
func seedCase(t *testing.T, repo *Repository) (string, int64, int64) {
	t.Helper()
	ctx := context.Background()
	caseID := uuid4Hex()
	chatID := -int64(100000 + time.Now().UnixNano()%900000)
	userID := time.Now().UnixNano()
	now := db.MysqlDatetime6(db.NowUTC())
	_, err := repo.DB.ExecContext(ctx, `INSERT INTO moderation_cases
		(id,event_key,chat_id,user_id,action,status,payload,created_at,updated_at)
		VALUES (?,?,?,?,?,?,?,?,?)`,
		caseID, "evt-"+caseID, chatID, userID, "ban", "pending", "{}", now, now)
	if err != nil {
		t.Fatalf("插入前置 case 失败: %v", err)
	}
	t.Cleanup(func() {
		_, _ = repo.DB.Exec("DELETE FROM moderation_appeals WHERE case_id = ?", caseID)
		_, _ = repo.DB.Exec("DELETE FROM moderation_cases WHERE id = ?", caseID)
	})
	return caseID, chatID, userID
}

// TestTransitionRevisionAuditAndTerminal 覆盖（appeal_repository.py:113-136）：
// draft→submitted revision+1 且 active_key 保留、写审计行；
// →rejected（终态）后 active_key 置 NULL。
func TestTransitionRevisionAuditAndTerminal(t *testing.T) {
	_, repo := testEnv(t)
	ctx := context.Background()
	caseID, chatID, userID := seedCase(t, repo)
	actor := fmt.Sprintf("telegram:%d", userID)

	var appealID string
	err := repo.Reader(ctx, func(s *Store) error {
		caseRow, err := s.Case(ctx, caseID)
		if err != nil {
			return err
		}
		appeal, err := s.Create(ctx, caseRow, map[string]any{
			"source":   map[string]any{"id": caseID},
			"evidence": []any{},
		})
		if err != nil {
			return err
		}
		appealID = rowString(appeal, "id")

		if got := rowInt(appeal, "revision"); got != 1 {
			t.Fatalf("初始 revision 应为 1，实际 %v", got)
		}

		// draft → submitted（非终态：active_key 保留）。
		a2, err := s.Transition(ctx, appeal, "submitted", actor, "submitted", TransitionOptions{})
		if err != nil {
			return err
		}
		if rowInt(a2, "revision") != 2 {
			t.Fatalf("submitted 后 revision 应为 2，实际 %v", rowInt(a2, "revision"))
		}
		if rowString(a2, "status") != "submitted" {
			t.Fatalf("status 应为 submitted，实际 %q", rowString(a2, "status"))
		}
		if rowString(a2, "active_key") != ActiveKey(chatID, userID) {
			t.Fatalf("非终态 active_key 应保留: %q", rowString(a2, "active_key"))
		}

		// → rejected（终态：active_key 置 NULL）。
		a3, err := s.Transition(ctx, a2, "rejected", actor, "rejected", TransitionOptions{})
		if err != nil {
			return err
		}
		if rowInt(a3, "revision") != 3 {
			t.Fatalf("rejected 后 revision 应为 3，实际 %v", rowInt(a3, "revision"))
		}
		if ak, ok := a3["active_key"]; !ok || ak != nil {
			t.Fatalf("终态 active_key 应为 NULL，实际 %v", ak)
		}

		// 审计行：最新一条为 rejected（draft→submitted→rejected）。
		events, err := s.Events(ctx, appealID)
		if err != nil {
			return err
		}
		if len(events) < 3 {
			t.Fatalf("审计事件应 ≥3 条（created/submitted/rejected），实际 %d", len(events))
		}
		latest := events[0]
		if rowString(latest, "action") != "rejected" ||
			rowString(latest, "from_status") != "submitted" ||
			rowString(latest, "to_status") != "rejected" {
			t.Fatalf("最新审计行不符: %+v", latest)
		}
		second := events[1]
		if rowString(second, "action") != "submitted" ||
			rowString(second, "from_status") != "draft" ||
			rowString(second, "to_status") != "submitted" {
			t.Fatalf("次新审计行不符: %+v", second)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("transition 流程失败: %v", err)
	}
}

// TestGetLockMutualExclusionAndTimeout 覆盖（README 硬规则 6）：
// 第二连接 GET_LOCK 同名锁 5s 超时返回 0 → ErrLockUnavailable；
// 持锁方释放后可立即再取。
func TestGetLockMutualExclusionAndTimeout(t *testing.T) {
	_, repo := testEnv(t)

	// 第二个独立 *sql.DB 句柄（设计 §15.2-6：两个 sql.DB 句柄）。
	dsn := os.Getenv("MYSQL_TEST_DSN")
	if cfg, err := mysql.ParseDSN(dsn); err == nil {
		cfg.ParseTime = true
		cfg.Loc = time.UTC
		dsn = cfg.FormatDSN()
	}
	db2, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("打开第二连接失败: %v", err)
	}
	t.Cleanup(func() { _ = db2.Close() })
	repo2 := New(db2)

	ctx := context.Background()
	userID := time.Now().UnixNano()

	acquired := make(chan struct{})
	release := make(chan struct{})
	holderErr := make(chan error, 1)
	go func() {
		holderErr <- repo.WithLock(ctx, userID, func(*Store) error {
			close(acquired)
			<-release
			return nil
		})
	}()
	<-acquired

	start := time.Now()
	contentionErr := repo2.WithLock(ctx, userID, func(*Store) error {
		t.Error("竞争方不应获得锁")
		return nil
	})
	elapsed := time.Since(start)
	if contentionErr == nil {
		t.Fatal("竞争方应获取失败")
	}
	if elapsed < 4*time.Second {
		t.Fatalf("GET_LOCK 应等待约 5s，实际 %v", elapsed)
	}
	close(release)
	if err := <-holderErr; err != nil {
		t.Fatalf("持锁方返回错误: %v", err)
	}

	// 释放后可立即再取。
	if err := repo2.WithLock(ctx, userID, func(*Store) error { return nil }); err != nil {
		t.Fatalf("释放后应可再取锁: %v", err)
	}
}

// TestGetLockPanicDoesNotLeak 覆盖（README 硬规则 6 / 设计 §15.2-6）：
// 工作体 panic 后 defer 双保险释放锁，新请求可立即取到。
func TestGetLockPanicDoesNotLeak(t *testing.T) {
	_, repo := testEnv(t)
	ctx := context.Background()
	userID := time.Now().UnixNano()

	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Error("panic 应向上传播")
			}
		}()
		_ = repo.WithLock(ctx, userID, func(*Store) error {
			panic("工作体异常")
		})
	}()

	// panic 路径已由 defer 释放：新请求必须能立即取锁。
	err := repo.WithLock(ctx, userID, func(*Store) error { return nil })
	if err != nil {
		t.Fatalf("panic 后锁泄漏: %v", err)
	}
}

// TestQuickReviewRequestID 验证幂等请求 ID 的派生格式
// （appeal_inbox.py:265-267：origin 为 Python 元组的 repr 形态 "(chat, msg)"，
// 圆括号、逗号后带空格；latest 为空对应 Python ”）。
// sha256Hex 测试内独立实现期望值计算，交叉校验生产实现的派生格式。
func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func TestQuickReviewRequestID(t *testing.T) {
	got := QuickReviewRequestID("abcdef", 42, 7, -100123, 555, "approve", "prev-id")
	want := sha256Hex("review:abcdef:42:7:(-100123, 555):approve:prev-id")
	if got != want {
		t.Fatalf("派生 ID 不符: got=%s want=%s", got, want)
	}
}

// TestActiveKeyAndIsTerminal 覆盖申诉侧纯函数（appeal_repository.py:15,20-21）。
func TestActiveKeyAndIsTerminal(t *testing.T) {
	// sha256("-100123:777") 的十六进制，交叉校验键格式 "{chat_id}:{user_id}"。
	want := sha256Hex("-100123:777")
	if got := ActiveKey(-100123, 777); got != want {
		t.Fatalf("ActiveKey 不符: got=%s want=%s", got, want)
	}
	for _, s := range []string{"approved", "rejected", "withdrawn", "superseded"} {
		if !IsTerminal(s) {
			t.Fatalf("%s 应为终态", s)
		}
	}
	for _, s := range []string{"draft", "submitted", "pending", "need_info", "executing"} {
		if IsTerminal(s) {
			t.Fatalf("%s 不应为终态", s)
		}
	}
}

// TestUUID4Hex 覆盖 uuid4().hex 等价：32 位小写十六进制、版本位为 4。
func TestUUID4Hex(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 64; i++ {
		id := uuid4Hex()
		if len(id) != 32 {
			t.Fatalf("长度应为 32: %s", id)
		}
		for _, c := range id {
			if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
				t.Fatalf("应为小写十六进制: %s", id)
			}
		}
		if id[12] != '4' {
			t.Fatalf("版本位应为 4: %s", id)
		}
		if seen[id] {
			t.Fatalf("不应重复: %s", id)
		}
		seen[id] = true
	}
}
