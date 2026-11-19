// Copyright 2026 Sakura-Bot
// SPDX-License-Identifier: AGPL-3.0-or-later

package mainbot

import (
	"os"
	"os/exec"
)

func restartRelease(release stagedRelease) error {
	// The child inherits the console and sees Ctrl+C alongside its parent.
	command := exec.Command(release.executable, os.Args[1:]...)
	command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, os.Stderr
	command.Env = append(os.Environ(), staticDirEnv+"="+release.staticDir)
	return command.Run()
}
