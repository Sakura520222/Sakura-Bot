package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/config"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/contracts"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/db"
	"github.com/go-sql-driver/mysql"
)

func TestAnonymousBillingRequiresGroupAllowance(t *testing.T) {
	q := &quotaManager{cfg: newQuotaTestConfig(t, `{}`), log: testLogger()}
	for _, ctx := range []context.Context{
		context.Background(),
		contracts.WithAnonymousQuota(context.Background(), -200, 20, ""),
		contracts.WithAnonymousQuota(context.Background(), -100, 0, ""),
		contracts.WithAnonymousQuota(context.Background(), -100, 20, "https://t.me/disabled"),
	} {
		if got := q.ReserveTurnMessage(ctx, "-100:1", -100, "turn"); got.Allowed || got.Message == "" {
			t.Fatalf("unverified group actor admitted: %+v", got)
		}
	}
}

func TestAnonymousCheckinDoesNotAccessPersonalPoints(t *testing.T) {
	q := &quotaManager{cfg: newQuotaTestConfig(t, `{}`), log: testLogger()}
	if got := q.Checkin(context.Background(), -100); got == "" {
		t.Fatal("anonymous checkin requires an explanatory response")
	}
}

func TestAnonymousBillingRechecksPolicyAndUsesGroupLimit(t *testing.T) {
	dsn := os.Getenv("MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("MYSQL_TEST_DSN not configured")
	}
	dbConfig, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	dbConfig.ParseTime = true
	dbConfig.Loc = time.UTC
	connection, err := sql.Open("mysql", dbConfig.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { connection.Close() })
	ctx := context.Background()
	manager := &db.Manager{DB: connection}
	if err := manager.InitDatabase(ctx); err != nil {
		t.Fatal(err)
	}
	if err := manager.InitReservations(ctx); err != nil {
		t.Fatal(err)
	}
	group := -time.Now().UnixNano()
	t.Cleanup(func() {
		connection.Exec("DELETE FROM usage_quota WHERE user_id=?", group)
		connection.Exec("DELETE FROM qa_message_reservations WHERE user_id=?", group)
	})
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	writePolicy := func(enabled bool, limit int) {
		t.Helper()
		body := fmt.Sprintf(`{"channels":["https://t.me/test"],"qa_bot_quota":{"user_daily_limit":1,"total_daily_limit":10000},"qa_assistant":{"comments":[{"channel":"https://t.me/test","enabled":true,"anonymous_enabled":%t,"anonymous_daily_limit":%d}]}}`, enabled, limit)
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	writePolicy(true, 3)
	cfg := config.New(filepath.Join(dir, ".env"), path)
	if err := cfg.Init(); err != nil {
		t.Fatal(err)
	}
	q := &quotaManager{cfg: cfg, db: manager, log: testLogger()}
	ctx = contracts.WithAnonymousQuota(ctx, group, 3, "https://t.me/test")
	for i := 0; i < 2; i++ {
		id := fmt.Sprintf("%d:%d", group, i)
		result := q.ReserveTurnMessage(ctx, id, group, "group-turn")
		if !result.Allowed || result.IsAdmin {
			t.Fatalf("shared group allowance failed: %+v", result)
		}
		q.FinishReservation(ctx, id, true)
	}
	writePolicy(true, 2)
	if result := q.ReserveTurnMessage(ctx, fmt.Sprintf("%d:limit", group), group, "group-turn"); result.Allowed {
		t.Fatal("stale context bypassed lowered group limit")
	}
	writePolicy(false, 3)
	if result := q.ReserveTurnMessage(ctx, fmt.Sprintf("%d:disabled", group), group, "group-turn"); result.Allowed {
		t.Fatal("stale context bypassed revoked anonymous policy")
	}
	var registered, points int
	if err := connection.QueryRow("SELECT COUNT(*) FROM users WHERE user_id=?", group).Scan(&registered); err != nil {
		t.Fatal(err)
	}
	if err := connection.QueryRow("SELECT COUNT(*) FROM qa_points WHERE user_id=?", group).Scan(&points); err != nil {
		t.Fatal(err)
	}
	if registered != 0 || points != 0 {
		t.Fatal("group actor created a personal account or points balance")
	}
}
