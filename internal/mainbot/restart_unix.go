//go:build !windows

// Copyright 2026 Sakura-Bot
// SPDX-License-Identifier: AGPL-3.0-or-later

package mainbot

import (
	"os"
	"slices"
	"strings"
	"syscall"
)

func restartRelease(release stagedRelease) error {
	env := slices.DeleteFunc(os.Environ(), func(e string) bool {
		return strings.HasPrefix(e, staticDirEnv+"=")
	})
	env = append(env, staticDirEnv+"="+release.staticDir)
	args := append([]string{release.executable}, os.Args[1:]...)
	return syscall.Exec(release.executable, args, env)
}
