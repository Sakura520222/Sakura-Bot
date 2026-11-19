// Copyright 2026 Sakura-Bot
// SPDX-License-Identifier: AGPL-3.0-or-later

package filelock

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestAcquireWaitsForOwnerAndHonorsCancellation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.lock")
	first, err := Acquire(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	wait, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	second, err := Acquire(wait, path)
	if second != nil {
		second.Close()
		t.Fatal("lock permitted a second owner")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("wanted cancellation, got %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	reacquire, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	last, err := Acquire(reacquire, path)
	if err != nil {
		t.Fatal(err)
	}
	last.Close()
}
