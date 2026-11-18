// Copyright 2026 Sakura-Bot
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package filelock provides advisory locks held by an open file handle.
package filelock

import (
	"context"
	"os"
	"time"
)

// Acquire returns a locked file. Close it to release the lock. Keep the lock
// file in place so separate processes always lock the same filesystem object.
func Acquire(ctx context.Context, path string) (*os.File, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		locked, err := tryLock(f)
		if err != nil {
			_ = f.Close()
			return nil, err
		}
		if locked {
			return f, nil
		}
		select {
		case <-ctx.Done():
			_ = f.Close()
			return nil, ctx.Err()
		case <-tick.C:
		}
	}
}
