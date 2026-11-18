// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package scheduler

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestCronParseAndNext(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	spec, err := WeeklyCron([]string{"mon", "fri"}, 9, 30)
	if err != nil {
		t.Fatalf("WeeklyCron failed: %v", err)
	}
	// 2026-11-18 是周三
	base := time.Date(2026, 11, 18, 10, 0, 0, 0, loc)
	next := spec.Next(base)
	want := time.Date(2026, 11, 20, 9, 30, 0, 0, loc) // 周五 09:30
	if !next.Equal(want) {
		t.Fatalf("Next = %v, want %v", next, want)
	}

	wikiCron, err := ParseCron("0 */6 * * *")
	if err != nil {
		t.Fatalf("ParseCron failed: %v", err)
	}
	nextWiki := wikiCron.Next(base)
	wantWiki := time.Date(2026, 11, 18, 12, 0, 0, 0, loc)
	if !nextWiki.Equal(wantWiki) {
		t.Fatalf("Next wiki = %v, want %v", nextWiki, wantWiki)
	}
}

func TestSchedulerIntervalPauseResumeAndWake(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sched := New(time.UTC, nil)
	var runs atomic.Int32
	sched.AddInterval("test_job", "summary", 200*time.Millisecond, 20*time.Millisecond, true, func(context.Context) {
		runs.Add(1)
	})
	sched.Start(ctx)

	deadline := time.Now().Add(2 * time.Second)
	for runs.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if runs.Load() == 0 {
		t.Fatal("expected interval job to run at least once")
	}

	if !sched.Pause() || !sched.IsPaused() {
		t.Fatal("expected Pause() to succeed")
	}
	before := runs.Load()
	sched.Wake("test_job", 0)
	time.Sleep(80 * time.Millisecond)
	if got := runs.Load(); got != before {
		t.Fatalf("paused job executed: before=%d got=%d", before, got)
	}

	if !sched.Resume() || sched.IsPaused() {
		t.Fatal("expected Resume() to succeed")
	}
	sched.Wake("test_job", 5*time.Millisecond)
	deadline = time.Now().Add(2 * time.Second)
	for runs.Load() == before && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if runs.Load() <= before {
		t.Fatal("expected job to run after Resume + Wake")
	}

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer stopCancel()
	if err := sched.Stop(stopCtx); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
}
