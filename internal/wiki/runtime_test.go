// Copyright 2026 Sakura-Bot
// 本项目采用 AGPL-3.0 许可
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot

package wiki

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/Sakura520222/Sakura-Bot/internal/llm"
	"github.com/Sakura520222/Sakura-Bot/internal/mtproto"
	"github.com/go-sql-driver/mysql"
)

func TestScheduleDuePreservesPreviousWeekdayAndDayFilters(t *testing.T) {
	parse := func(value string) time.Time {
		t.Helper()
		when, err := time.Parse(time.RFC3339, value)
		if err != nil {
			t.Fatal(err)
		}
		return when
	}
	for _, test := range []struct {
		name, frequency, expr, start, now string
		due                               bool
	}{
		{"weekly Sunday excluded", "weekly", "", "2026-11-21T20:00:00Z", "2026-11-22T18:59:00Z", false},
		{"weekly Monday at 03:00 CST", "weekly", "", "2026-11-21T20:00:00Z", "2026-11-22T19:00:00Z", true},
		{"numeric Monday and exact month day", "cron", "0 3 23 nov 0", "2026-11-22T18:58:00Z", "2026-11-22T19:00:00Z", true},
		{"calendar and weekday both required", "cron", "0 3 23 nov sun", "2026-11-21T18:59:00Z", "2026-11-22T19:00:00Z", false},
		{"month last day", "cron", "0 3 last feb *", "2028-02-26T19:01:00Z", "2028-02-28T19:00:00Z", true},
		{"same minute already handled", "daily", "", "2026-11-22T19:00:00Z", "2026-11-22T19:00:00Z", false},
		{"single point with step", "cron", "20/1 3 * * *", "2026-11-22T19:20:00Z", "2026-11-22T19:21:00Z", true},
		{"manual ignores Cron", "manual", "0 3 * * *", "2026-11-21T18:59:00Z", "2026-11-22T19:00:00Z", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			start := parse(test.start)
			cfg := WikiConfig{Frequency: test.frequency, Cron: test.expr, Timezone: "Asia/Shanghai"}
			if got := scheduleDue(cfg, &start, parse(test.now)); got != test.due {
				t.Fatalf("due=%t, want %t", got, test.due)
			}
		})
	}
	if _, err := parseWikiCron("0 3 * * 7"); err == nil {
		t.Fatal("weekday 7 was accepted")
	}
}

func TestJobFailureUsesRateLimitWithoutExposingResponseBody(t *testing.T) {
	_, code, delay := jobFailure(&mtproto.FloodWaitError{Seconds: 43}, 1)
	if code != "FloodWaitError" || delay != 43 {
		t.Fatalf("unexpected Telegram retry %s %d", code, delay)
	}
	detail, code, delay := jobFailure(&llm.APIError{StatusCode: 429, Body: "private JSON"}, 0)
	if detail != "模型服务返回 HTTP 429" || code != "APIError" || delay != 5 {
		t.Fatalf("unexpected provider retry %q %s %d", detail, code, delay)
	}
	if _, _, delay := jobFailure(errors.New("transient"), 100); delay != 10 {
		t.Fatalf("unbounded backoff: %d", delay)
	}
	detail, code, _ = jobFailure(&mysql.MySQLError{Number: 1452, Message: "private database text"}, 0)
	if detail != "Wiki 数据库处理失败（MySQL 1452），已保留当前进度" || code != "DatabaseError" {
		t.Fatalf("unexpected database diagnostic %q %s", detail, code)
	}
}

func TestRuntimeReplaysUnacknowledgedDeletionOnRestart(t *testing.T) {
	store := isolatedWikiStore(t)
	ctx := t.Context()
	first := NewRuntime(store, nil, nil)
	outbox := NewEventOutbox(t.TempDir())
	first.outbox = outbox
	if err := store.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	ch, err := store.RegisterChannel(ctx, ChannelEntity{ID: 900001, Title: "Wiki test", Username: "wiki_test", Broadcast: true}, "")
	if err != nil {
		t.Fatal(err)
	}
	cfg := DefaultWikiConfig()
	cfg.Enabled = true
	if err := store.ConfigureChannel(ctx, ch.ID, cfg, "test"); err != nil {
		t.Fatal(err)
	}
	first.setKnownChannels([]*ChannelRecord{ch})
	mid := int64(101)
	sourceID, err := store.PutSource(ctx, PutSourceInput{
		ChannelID: ch.ID, SourceType: "message", ExternalID: "101", MessageID: &mid,
		CreatedAt: NowUTC(), Body: "test message", Verified: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	first.HandleDelete(ctx, mtproto.DeletedMessagesEvent{ChannelID: 900001, MessageIDs: []int{101}})
	if keys, err := outbox.Keys(); err != nil || len(keys) != 1 {
		t.Fatalf("expected one unacknowledged delete, got %v (%v)", keys, err)
	}
	second := NewRuntime(store, nil, nil)
	second.outbox = outbox
	if err := second.initialize(ctx); err != nil {
		t.Fatal(err)
	}
	source, err := store.GetSource(ctx, sourceID)
	if err != nil || source == nil || !source.Deleted {
		t.Fatalf("delete was not replayed: %+v (%v)", source, err)
	}
	if keys, err := outbox.Keys(); err != nil || len(keys) != 0 {
		t.Fatalf("delete was not acknowledged: %v (%v)", keys, err)
	}
	after, err := store.GetChannel(ctx, ch.ID)
	if err != nil || after == nil || after.AccessVerified || after.VerifiedUntil != nil {
		t.Fatalf("channel remained verified before Telegram refresh: %+v (%v)", after, err)
	}
}

func isolatedWikiStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("MYSQL_TEST_DSN is not configured for integration tests")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.DBName = ""
	cfg.ParseTime = true
	cfg.Loc = time.UTC
	admin, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	name := "sakura_wiki_test_" + NewID()[:12]
	if _, err := admin.ExecContext(t.Context(), "CREATE DATABASE `"+name+"` CHARACTER SET utf8mb4"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := admin.ExecContext(ctx, "DROP DATABASE `"+name+"`"); err != nil {
			t.Error(err)
		}
	})
	cfg.DBName = name
	data, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = data.Close() })
	return NewStore(data)
}
