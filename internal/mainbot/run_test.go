// Copyright 2026 Sakura-Bot
// SPDX-License-Identifier: AGPL-3.0-or-later

package mainbot

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sync/errgroup"
)

func TestEnsureConfigLeavesExistingFileIntact(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data", "config.json")
	if err := ensureConfig(t.Context(), path); err != nil {
		t.Fatal(err)
	}
	want := `{"channels":["https://t.me/testfixture"]}`
	if err := os.WriteFile(path, []byte(want), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ensureConfig(t.Context(), path); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != want {
		t.Fatalf("existing config changed: got %q, err %v", got, err)
	}
}

func TestLegacyIndexMustBeMigratedBeforeStarting(t *testing.T) {
	dir := t.TempDir()
	if err := checkLegacyVectorIndex(dir); err != nil {
		t.Fatalf("fresh instance: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "chroma.sqlite3"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := checkLegacyVectorIndex(dir); err == nil {
		t.Fatal("unmigrated Chroma index was accepted")
	}
	if err := os.WriteFile(filepath.Join(dir, "lite_index.sqlite3"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := checkLegacyVectorIndex(dir); err != nil {
		t.Fatalf("existing SQLite index: %v", err)
	}
}

func TestEventQueueOrdersOnePeerWithoutBlockingAnother(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	group, ctx := errgroup.WithContext(ctx)
	events := newEventQueue(ctx, group, slog.Default())
	release := make(chan struct{})
	processed := make(chan int, 3)
	events.submit(ctx, 1, func(ctx context.Context) {
		select {
		case <-release:
			processed <- 1
		case <-ctx.Done():
		}
	})
	events.submit(ctx, 1, func(context.Context) { processed <- 2 })
	events.submit(ctx, 2, func(context.Context) { processed <- 3 })
	next := func(want int) {
		t.Helper()
		select {
		case result := <-processed:
			if result != want {
				t.Fatalf("got event %d, want %d", result, want)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	next(3)
	close(release)
	next(1)
	next(2)
	cancel()
	if err := group.Wait(); err != nil {
		t.Fatal(err)
	}
}
