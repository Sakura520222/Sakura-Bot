// Copyright 2026 Sakura-Bot
// SPDX-License-Identifier: AGPL-3.0-or-later

package mainbot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/Sakura520222/Sakura-Bot/internal/config"
	"github.com/Sakura520222/Sakura-Bot/internal/filelock"
	"github.com/Sakura520222/Sakura-Bot/internal/vectors"
)

func MigrateVectors(ctx context.Context, verifyOnly bool, out io.Writer) error {
	cfg := config.Default()
	dir := cfg.VectorDBPath()
	lock, err := lockVectorDirectory(ctx, dir)
	if err != nil {
		return err
	}
	defer lock.Close()
	if !verifyOnly {
		_, err = os.Stat(filepath.Join(dir, "lite_index.sqlite3"))
		if err == nil {
			return errors.New("SQLite 向量库已存在，请使用 --verify-vectors")
		}
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	report, err := vectors.MigrateChromaToSQLite(ctx, vectors.MigrateOptions{
		VectorDBPath: dir, ExpectedDim: cfg.EmbeddingDimension(), VerifyOnly: verifyOnly,
	})
	if err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(report)
}

func lockVectorDirectory(ctx context.Context, dir string) (*os.File, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	lockCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	lock, err := filelock.Acquire(lockCtx, filepath.Join(dir, ".sakura-vector.lock"))
	if err != nil {
		return nil, fmt.Errorf("锁定向量目录: %w", err)
	}
	return lock, nil
}

func checkLegacyVectorIndex(dir string) error {
	if _, err := os.Stat(filepath.Join(dir, "lite_index.sqlite3")); !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if _, err := os.Stat(filepath.Join(dir, "chroma.sqlite3")); err == nil {
		return errors.New("检测到待迁移的 ChromaDB 向量库，请停机运行 sakura-main --migrate-vectors")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
